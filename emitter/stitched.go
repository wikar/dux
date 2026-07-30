// Stitched codegen for multi-table SUMMARIZECOLUMNS.
//
// When the measures of a SUMMARIZECOLUMNS call span more than one table
// cluster (see cluster.go), a single flat join tree would fan the clusters out
// against each other and inflate every aggregate. Instead each cluster is
// evaluated in its own grouped CTE — its private filter context — and the
// per-cluster results are stitched back together on the group keys:
//
//	WITH _mc0 AS (
//	  SELECT year AS k0, SUM(qty) AS a0 FROM dates LEFT JOIN fact_sales ... GROUP BY year
//	), _mc1 AS (
//	  SELECT year AS k0, SUM(rqty) AS a1 FROM dates LEFT JOIN fact_returns ... GROUP BY year
//	)
//	SELECT COALESCE(_mc0.k0, _mc1.k0) AS "year", _mc0.a0 AS 'Sold', _mc1.a1 AS 'Returned'
//	FROM _mc0
//	FULL OUTER JOIN _mc1 ON _mc0.k0 IS NOT DISTINCT FROM _mc1.k0
//
// TREATAS predicates are routed into every cluster whose join tree contains
// (or can reach) the predicate's table; a filter unrelated to a cluster's
// tables does not affect that cluster's measures — matching DAX filter
// propagation through relationships.
package emitter

import (
	"fmt"
	"slices"
	"strings"

	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// stitchForBidi reports whether the query's join graph crosses a bidirectional
// relationship. Such queries are emitted stitched even with a single measure
// cluster: the cluster FROM builder gates bidi filter chains with EXISTS
// semi-joins, which the flat join tree cannot express without fan-out.
func (e *Emitter) stitchForBidi(plan *measurePlan, groupKeys []groupKey, preds []taggedPred) bool {
	if e.Schema == nil {
		return false
	}
	if !slices.ContainsFunc(e.Schema.Relationships,
		func(r *semantic.Relationship) bool { return r.Bidirectional }) {
		return false
	}
	seen := map[string]bool{}
	var tables []string
	add := func(t string) {
		key := e.tableKey(t)
		if t != "" && !seen[key] {
			seen[key] = true
			tables = append(tables, t)
		}
	}
	for _, gk := range groupKeys {
		add(gk.table)
	}
	for _, c := range plan.clusters {
		for _, t := range c.tables {
			add(t)
		}
	}
	for _, p := range preds {
		add(p.table)
	}
	if len(tables) <= 1 {
		return false
	}
	jp, err := semantic.InferJoinPath(e.Schema, tables)
	if err != nil {
		// Let the flat path surface the join error with its usual message.
		return false
	}
	return slices.ContainsFunc(jp.Steps, func(s semantic.JoinStep) bool { return s.Bidirectional })
}

// stitchedCTE is one emitted cluster CTE.
type stitchedCTE struct {
	name string
	body string
}

// emitStitched emits the multi-cluster form of SUMMARIZECOLUMNS.
// groupCols are the emitted SQL group expressions, plainKeys their
// table+column identities (one per group column — computed group expressions
// are rejected), rollupElems/rollupKeys the ROLLUPADDISSUBTOTAL units and
// their column identities, pairArgs the alternating name/expr measure
// arguments, inlineArgs the measure references from the group position, plan
// the partition from planMeasures, and preds the TREATAS predicates.
//
// With rollups, every cluster CTE emits the SAME grouping sets plus one
// GROUPING() flag per rollup element; the stitch matches on all key columns
// AND the flags, so a subtotal row only pairs with the matching subtotal
// level of the other clusters.
func (e *Emitter) emitStitched(
	groupCols []string,
	plainKeys []groupKey,
	rollupElems []rollupElement,
	rollupKeys []groupKey,
	pairArgs []*parser.Expr,
	inlineArgs []*parser.Expr,
	plan *measurePlan,
	preds []taggedPred,
) (string, error) {
	if len(groupCols) != len(plainKeys) {
		return "", fmt.Errorf(
			"SUMMARIZECOLUMNS: computed group expressions are not supported when measures span multiple tables; group by plain columns")
	}
	totalRollupCols := 0
	for _, el := range rollupElems {
		totalRollupCols += len(el.cols)
	}
	if totalRollupCols != len(rollupKeys) {
		return "", fmt.Errorf(
			"SUMMARIZECOLUMNS: computed ROLLUPADDISSUBTOTAL columns are not supported when measures span multiple tables; roll up plain columns")
	}
	groupKeys := append(append([]groupKey{}, plainKeys...), rollupKeys...)
	numKeys := len(groupCols) + totalRollupCols

	predUsed := make([]bool, len(preds))
	measureRef := make(map[int]string) // name-arg index → outer SELECT reference
	inlineRef := make(map[int]string)  // inline index → outer SELECT reference
	subst := make(map[*parser.FuncCall]string)
	liftedSeq := 0
	var ctes []stitchedCTE

	// Context clusters (CALCULATE with removals, see contextcte.go) are
	// emitted after the regular clusters: they carry a subset of the group
	// keys and LEFT JOIN onto the stitch instead of defining cells.
	var calcClusters []*measureCluster
	var clusters []*measureCluster
	for _, c := range plan.clusters {
		if c.calc != nil {
			calcClusters = append(calcClusters, c)
			continue
		}
		clusters = append(clusters, c)
	}
	// A query with no regular table-bearing measures (dimension-only, or only
	// context-modifying measures) still needs one CTE to define the cells.
	if regularTableClusterCount(plan.clusters) == 0 && numKeys > 0 {
		clusters = append([]*measureCluster{{key: "__group__"}}, clusters...)
	}

	for _, c := range clusters {
		if c.key == "" {
			// Pure scalar measures need no context — emit in the outer SELECT.
			for _, pi := range c.pairIdx {
				exprSQL, err := e.emitExpr(pairArgs[pi+1])
				if err != nil {
					return "", err
				}
				measureRef[pi] = "(" + exprSQL + ")"
			}
			for _, j := range c.inlineIdx {
				exprSQL, err := e.emitExpr(inlineArgs[j])
				if err != nil {
					return "", err
				}
				inlineRef[j] = "(" + exprSQL + ")"
			}
			continue
		}

		cteName := fmt.Sprintf("_mc%d", len(ctes))

		// Ordinary clusters root at their value table so conformed dimensions do
		// not choose a path through an unrelated fact. Context CTEs can make a
		// dimension key non-BLANK without a row in the ordinary fact, so their
		// shared key scan remains dimension-rooted.
		seen := map[string]bool{}
		var tables []string
		add := func(t string) {
			key := e.tableKey(t)
			if !seen[key] {
				seen[key] = true
				tables = append(tables, t)
			}
		}
		if plan.hasContextClusters() {
			for _, gk := range groupKeys {
				add(gk.table)
			}
			for _, t := range c.tables {
				add(t)
			}
			// Prefer the dimension key scan only when it identifies one join tree.
			// With conformed dimensions, retry from this cluster's fact rather than
			// guessing which sibling fact connects the keys.
			if e.Schema != nil {
				if _, err := semantic.InferJoinPath(e.Schema, tables); semantic.IsAmbiguousPath(err) {
					seen = map[string]bool{}
					tables = nil
					for _, t := range c.tables {
						add(t)
					}
					for _, gk := range groupKeys {
						add(gk.table)
					}
				}
			}
		} else {
			for _, t := range c.tables {
				add(t)
			}
			for _, gk := range groupKeys {
				add(gk.table)
			}
		}
		// needed: tables the CTE's SELECT list references. Predicate tables
		// added below are filter-only and eligible for EXISTS carving.
		needed := make(map[string]bool, len(seen))
		for k := range seen {
			needed[k] = true
		}

		// Route predicates into this cluster when their table is present, or
		// when the filter propagates to the cluster's tables under DAX rules
		// (dimension → fact; both ways across bidi edges). A filter that does
		// not propagate leaves this cluster's measures unaffected.
		var clusterTagged []taggedPred
		for pi, p := range preds {
			if p.table == "" {
				return "", fmt.Errorf(
					"SUMMARIZECOLUMNS: cannot route the filter %q to a measure context when measures span multiple tables", p.sql)
			}
			if seen[e.tableKey(p.table)] {
				clusterTagged = append(clusterTagged, p)
				predUsed[pi] = true
				continue
			}
			if e.Schema != nil && semantic.FilterReaches(e.Schema, p.table, tables) {
				add(p.table)
				clusterTagged = append(clusterTagged, p)
				predUsed[pi] = true
			}
		}

		valueTables := map[string]bool{}
		for _, table := range c.tables {
			valueTables[e.tableKey(table)] = true
		}
		from, whereConds, groupRefs, err := e.stitchedClusterFrom(tables, needed, clusterTagged, valueTables, groupKeys...)
		if err != nil {
			return "", err
		}
		clusterKeys := append([]groupKey{}, groupKeys...)
		for i := range clusterKeys {
			if ref := groupRefs[e.resolvedColKey(clusterKeys[i].table, clusterKeys[i].col)]; ref != "" {
				clusterKeys[i].expr = ref
			}
		}
		clusterGroupCols := append([]string{}, groupCols...)
		for i := range plainKeys {
			clusterGroupCols[i] = clusterKeys[i].expr
		}
		clusterRollups := make([]rollupElement, len(rollupElems))
		keyIndex := len(plainKeys)
		for i, el := range rollupElems {
			clusterRollups[i] = rollupElement{nameSQL: el.nameSQL}
			for range el.cols {
				clusterRollups[i].cols = append(clusterRollups[i].cols, clusterKeys[keyIndex].expr)
				keyIndex++
			}
		}

		// Emit this cluster's measures in ITS filter context: the group keys
		// plus only the predicates routed to this cluster. CALCULATE modifier
		// resolution (filterctx.go) then removes/replicates the right filters.
		var items []string
		for ki, gc := range clusterGroupCols {
			items = append(items, fmt.Sprintf("%s AS k%d", gc, ki))
		}
		kn := len(clusterGroupCols)
		for gi, el := range clusterRollups {
			for _, col := range el.cols {
				items = append(items, fmt.Sprintf("%s AS k%d", col, kn))
				kn++
			}
			// Raw 0/1 grouping level: part of the stitch key, and the source
			// of the ISSUBTOTAL indicator in the outer SELECT.
			items = append(items, fmt.Sprintf("GROUPING(%s) AS g%d", el.cols[0], gi))
		}
		prevCtx := e.groupCtx
		e.groupCtx = &groupContext{keys: clusterKeys, preds: clusterTagged}
		emitErr := func() error {
			for _, pi := range c.pairIdx {
				exprSQL, err := e.emitExpr(pairArgs[pi+1])
				if err != nil {
					return err
				}
				items = append(items, fmt.Sprintf("(%s) AS a%d", exprSQL, pi/2))
				measureRef[pi] = fmt.Sprintf("%s.a%d", cteName, pi/2)
			}
			for _, j := range c.inlineIdx {
				exprSQL, err := e.emitExpr(inlineArgs[j])
				if err != nil {
					return err
				}
				items = append(items, fmt.Sprintf("(%s) AS i%d", exprSQL, j))
				inlineRef[j] = fmt.Sprintf("%s.i%d", cteName, j)
			}
			for _, fc := range c.lifted {
				if _, done := subst[fc]; done {
					continue // same stored-measure subtree lifted more than once
				}
				exprSQL, err := e.emitExpr(exprOfFunc(fc))
				if err != nil {
					return err
				}
				items = append(items, fmt.Sprintf("(%s) AS x%d", exprSQL, liftedSeq))
				subst[fc] = fmt.Sprintf("%s.x%d", cteName, liftedSeq)
				liftedSeq++
			}
			return nil
		}()
		e.groupCtx = prevCtx
		if emitErr != nil {
			return "", emitErr
		}

		var body strings.Builder
		fmt.Fprintf(&body, "SELECT %s\nFROM %s", strings.Join(items, ", "), from)
		if len(whereConds) > 0 {
			fmt.Fprintf(&body, "\nWHERE %s", strings.Join(whereConds, " AND "))
		}
		if len(clusterRollups) > 0 {
			fmt.Fprintf(&body, "\nGROUP BY %s", rollupGroupingSets(clusterGroupCols, clusterRollups))
		} else if len(clusterGroupCols) > 0 {
			fmt.Fprintf(&body, "\nGROUP BY %s", strings.Join(clusterGroupCols, ", "))
		}
		ctes = append(ctes, stitchedCTE{name: cteName, body: body.String()})
	}

	// Emit the context clusters as private grouped CTEs (contextcte.go). Their
	// value column substitutes for the lifted subtree in the outer arithmetic.
	var cctes []*contextCTE
	contextByShape := map[string]*contextCTE{}
	for _, c := range calcClusters {
		cf := calcForm(c.calc)
		if cf == nil {
			return "", fmt.Errorf("internal: context cluster subtree %s is not a CALCULATE form", c.calc.Name)
		}
		inCluster := map[string]bool{}
		for _, t := range c.tables {
			inCluster[e.tableKey(t)] = true
		}
		var routed []taggedPred
		for pi, p := range preds {
			if p.table == "" {
				return "", fmt.Errorf(
					"SUMMARIZECOLUMNS: cannot route the filter %q to a measure context when measures span multiple tables", p.sql)
			}
			if inCluster[e.tableKey(p.table)] ||
				(e.Schema != nil && semantic.FilterReaches(e.Schema, p.table, c.tables)) {
				routed = append(routed, p)
				predUsed[pi] = true
			}
		}
		prevCtx := e.groupCtx
		e.groupCtx = &groupContext{keys: groupKeys, preds: routed}
		cte, err := e.emitContextCTE("", cf, c.tables)
		e.groupCtx = prevCtx
		if err != nil {
			return "", err
		}
		if existing := contextByShape[cte.fusionKey()]; existing != nil {
			alias := existing.addValue(cte.values[0])
			subst[c.calc] = existing.name + "." + alias
		} else {
			cte.name = fmt.Sprintf("_cc%d", len(cctes))
			contextByShape[cte.fusionKey()] = cte
			subst[c.calc] = cte.name + ".v"
			cctes = append(cctes, cte)
		}
	}

	for pi, used := range predUsed {
		if !used {
			return "", fmt.Errorf(
				"SUMMARIZECOLUMNS: the filter on table %q is unrelated to every measure's tables; it would have no effect", preds[pi].table)
		}
	}

	// Cross-cluster expressions: emit the outer arithmetic with every lifted
	// aggregate substituted by its cluster CTE column.
	if len(plan.splitPairs) > 0 || len(plan.splitInline) > 0 {
		e.stitchSubst = subst
		for _, pi := range plan.splitPairs {
			exprSQL, err := e.emitExpr(pairArgs[pi+1])
			if err != nil {
				e.stitchSubst = nil
				return "", err
			}
			measureRef[pi] = "(" + exprSQL + ")"
		}
		for _, j := range plan.splitInline {
			exprSQL, err := e.emitExpr(inlineArgs[j])
			if err != nil {
				e.stitchSubst = nil
				return "", err
			}
			inlineRef[j] = "(" + exprSQL + ")"
		}
		e.stitchSubst = nil
	}

	// ── Assemble WITH + stitched outer SELECT ────────────────────────────────
	var sb strings.Builder
	sb.WriteString("WITH ")
	for i, c := range ctes {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%s AS (\n%s\n)", c.name, c.body)
	}
	for i, cc := range cctes {
		if i > 0 || len(ctes) > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%s AS (\n%s\n)", cc.name, cc.sqlBody())
	}

	var outItems []string
	for ki, gk := range plainKeys {
		outItems = append(outItems, fmt.Sprintf("%s AS %s",
			stitchedColRef(ctes, fmt.Sprintf("k%d", ki)), quoteIdent(gk.col)))
	}
	kn := len(groupCols)
	rk := 0
	for gi, el := range rollupElems {
		for range el.cols {
			outItems = append(outItems, fmt.Sprintf("%s AS %s",
				stitchedColRef(ctes, fmt.Sprintf("k%d", kn)), quoteIdent(rollupKeys[rk].col)))
			kn++
			rk++
		}
		outItems = append(outItems, fmt.Sprintf("(%s = 1) AS %s",
			stitchedColRef(ctes, fmt.Sprintf("g%d", gi)), el.nameSQL))
	}
	for j := range inlineArgs {
		outItems = append(outItems, fmt.Sprintf("%s AS %s", inlineRef[j], quoteIdent(inlineMeasureAlias(inlineArgs[j], j))))
	}
	for i := 0; i+1 < len(pairArgs); i += 2 {
		nameSQL, err := e.emitExpr(pairArgs[i])
		if err != nil {
			return "", err
		}
		outItems = append(outItems, fmt.Sprintf("%s AS %s", measureRef[i], nameSQL))
	}

	if len(ctes) == 0 {
		// No group keys and no regular measures: every context CTE is a
		// single aggregate row (the group-key-less grand total).
		fmt.Fprintf(&sb, "\nSELECT %s\nFROM %s", strings.Join(outItems, ", "), cctes[0].name)
		for _, cc := range cctes[1:] {
			fmt.Fprintf(&sb, "\nLEFT JOIN %s ON TRUE", cc.name)
		}
		return blankPrune(sb.String(), pairArgs, inlineArgs), nil
	}

	fmt.Fprintf(&sb, "\nSELECT %s\nFROM %s", strings.Join(outItems, ", "), ctes[0].name)
	for i := 1; i < len(ctes); i++ {
		var conds []string
		for ki := 0; ki < numKeys; ki++ {
			conds = append(conds, fmt.Sprintf("%s IS NOT DISTINCT FROM %s.k%d",
				stitchedColRef(ctes[:i], fmt.Sprintf("k%d", ki)), ctes[i].name, ki))
		}
		// Subtotal rows only pair with the same grouping level.
		for gi := range rollupElems {
			conds = append(conds, fmt.Sprintf("%s IS NOT DISTINCT FROM %s.g%d",
				stitchedColRef(ctes[:i], fmt.Sprintf("g%d", gi)), ctes[i].name, gi))
		}
		if len(conds) == 0 {
			// No group keys: every CTE is a single aggregate row.
			conds = []string{"TRUE"}
		}
		fmt.Fprintf(&sb, "\nFULL OUTER JOIN %s ON %s", ctes[i].name, strings.Join(conds, " AND "))
	}
	// Context CTEs never define cells: LEFT JOIN on the group keys they carry
	// (a removed key is absent, so the value repeats across it).
	for _, cc := range cctes {
		var conds []string
		for _, ki := range cc.keyIdxs {
			conds = append(conds, fmt.Sprintf("%s.k%d IS NOT DISTINCT FROM %s",
				cc.name, ki, stitchedColRef(ctes, fmt.Sprintf("k%d", ki))))
		}
		if len(conds) == 0 {
			conds = []string{"TRUE"}
		}
		fmt.Fprintf(&sb, "\nLEFT JOIN %s ON %s", cc.name, strings.Join(conds, " AND "))
	}
	return blankPrune(sb.String(), pairArgs, inlineArgs), nil
}

// inlineMeasureAlias names the output column of an inline measure reference:
// the measure's own name when the argument is a plain reference, else a
// positional fallback.
func inlineMeasureAlias(arg *parser.Expr, j int) string {
	if arg != nil && arg.Left != nil && arg.Left.ColRef != nil {
		return semantic.StripBrackets(arg.Left.ColRef.Column)
	}
	return fmt.Sprintf("measure%d", j)
}

// stitchedColRef returns the SQL referencing column col across the given
// CTEs: the bare column for one CTE, COALESCE over all of them otherwise
// (any CTE may be the NULL side of the FULL OUTER JOIN chain).
func stitchedColRef(ctes []stitchedCTE, col string) string {
	if len(ctes) == 1 {
		return fmt.Sprintf("%s.%s", ctes[0].name, col)
	}
	refs := make([]string, len(ctes))
	for i, c := range ctes {
		refs[i] = fmt.Sprintf("%s.%s", c.name, col)
	}
	return "COALESCE(" + strings.Join(refs, ", ") + ")"
}

// existsChain is one carved bidi filter chain: a subtree of the join path
// hanging off a bidirectional edge that contains only filter-source tables.
// It is emitted as a correlated EXISTS semi-join so many-to-many bridge rows
// gate the cluster without fanning its rows out.
type existsChain struct {
	anchor semantic.JoinStep   // the bidi step; FromTable stays in the outer FROM
	steps  []semantic.JoinStep // joins within the EXISTS body
	tables map[string]bool     // lower-cased canonical tables inside the chain
}

// groupChain is the projected counterpart of existsChain. A group key beyond
// a bidirectional edge must be projected, so a semi-join cannot carry it. A
// DISTINCT key map carries the group columns without multiplying value rows
// when several bridge rows reach the same grouped value.
type groupChain struct {
	anchor semantic.JoinStep
	steps  []semantic.JoinStep
	tables map[string]bool
}

// stitchedClusterFrom builds a cluster CTE's FROM clause and WHERE conditions.
// needed is the set of tables the CTE's SELECT list references (group keys and
// measure tables); preds are the TREATAS predicates routed to this cluster.
//
// Steps toward needed tables emit as flat LEFT JOINs. A bidirectional step
// whose subtree contains only filter-source tables is carved into an EXISTS
// chain, with predicates on carved tables moved inside; remaining predicates
// are returned as plain conditions.
func (e *Emitter) stitchedClusterFrom(tables []string, needed map[string]bool, preds []taggedPred, valueTables map[string]bool, groupKeys ...groupKey) (string, []string, map[string]string, error) {
	predSQL := func(ps []taggedPred) []string {
		out := make([]string, len(ps))
		for i, p := range ps {
			out[i] = p.sql
		}
		return out
	}
	if len(tables) == 1 {
		return e.sqlTable(tables[0]), predSQL(preds), nil, nil
	}
	if e.Schema == nil {
		return "", nil, nil, fmt.Errorf("SUMMARIZECOLUMNS: multi-table measures require a schema for join inference")
	}
	jp, err := semantic.InferJoinPath(e.Schema, tables)
	if err != nil {
		return "", nil, nil, err
	}

	lower := e.tableKey

	// The join path is a tree; find, per table, whether its subtree reaches a
	// table the SELECT list needs. A bidi step into a subtree with no needed
	// tables is a pure filter chain — carve it.
	kids := map[string][]semantic.JoinStep{}
	for _, s := range jp.Steps {
		kids[lower(s.FromTable)] = append(kids[lower(s.FromTable)], s)
	}
	var subtreeNeeded func(t string) bool
	subtreeNeeded = func(t string) bool {
		if needed[t] {
			return true
		}
		for _, s := range kids[t] {
			if subtreeNeeded(lower(s.Table)) {
				return true
			}
		}
		return false
	}
	groupTables := map[string]bool{}
	for _, key := range groupKeys {
		groupTables[lower(key.table)] = true
	}
	if valueTables == nil {
		valueTables = map[string]bool{}
		for table := range needed {
			if !groupTables[table] {
				valueTables[table] = true
			}
		}
	}
	var subtreeGroup func(t string) bool
	subtreeGroup = func(t string) bool {
		if groupTables[t] {
			return true
		}
		for _, s := range kids[t] {
			if subtreeGroup(lower(s.Table)) {
				return true
			}
		}
		return false
	}
	var subtreeValue func(t string) bool
	subtreeValue = func(t string) bool {
		if valueTables[t] {
			return true
		}
		for _, s := range kids[t] {
			if subtreeValue(lower(s.Table)) {
				return true
			}
		}
		return false
	}

	inChain := map[string]*existsChain{}
	var chains []*existsChain
	inGroupChain := map[string]*groupChain{}
	var groupChains []*groupChain

	var fromBuf strings.Builder
	fromBuf.WriteString(e.sqlTable(tables[0]))
	for _, s := range jp.Steps {
		fl, tl := lower(s.FromTable), lower(s.Table)
		if ch := inGroupChain[fl]; ch != nil {
			ch.steps = append(ch.steps, s)
			ch.tables[tl] = true
			inGroupChain[tl] = ch
			continue
		}
		if ch := inChain[fl]; ch != nil {
			// Continuation of a carved chain.
			ch.steps = append(ch.steps, s)
			ch.tables[tl] = true
			inChain[tl] = ch
			continue
		}
		if s.Bidirectional && subtreeGroup(tl) && !subtreeValue(tl) {
			ch := &groupChain{anchor: s, tables: map[string]bool{tl: true}}
			inGroupChain[tl] = ch
			groupChains = append(groupChains, ch)
			continue
		}
		if s.Bidirectional && !subtreeNeeded(tl) {
			ch := &existsChain{anchor: s, tables: map[string]bool{tl: true}}
			inChain[tl] = ch
			chains = append(chains, ch)
			continue
		}
		fmt.Fprintf(&fromBuf, "\nLEFT JOIN %s ON %s.%s = %s.%s",
			e.sqlTable(s.Table),
			e.sqlTable(s.FromTable), s.OnFromCol,
			e.sqlTable(s.Table), s.OnToCol,
		)
	}

	predUsed := make([]bool, len(preds))
	groupRefs := map[string]string{}
	for ci, ch := range groupChains {
		alias := fmt.Sprintf("__grp%d", ci)
		var selected []string
		selected = append(selected, fmt.Sprintf("%s.%s AS __anchor",
			e.sqlTable(ch.anchor.Table), ch.anchor.OnToCol))
		for _, key := range groupKeys {
			if !ch.tables[lower(key.table)] {
				continue
			}
			name := fmt.Sprintf("g%d", len(selected)-1)
			selected = append(selected, fmt.Sprintf("%s.%s AS %s",
				e.sqlTable(key.table), key.col, name))
			groupRefs[e.resolvedColKey(key.table, key.col)] = alias + "." + name
		}
		var body strings.Builder
		fmt.Fprintf(&body, "SELECT DISTINCT %s FROM %s",
			strings.Join(selected, ", "), e.sqlTable(ch.anchor.Table))
		for _, s := range ch.steps {
			fmt.Fprintf(&body, " LEFT JOIN %s ON %s.%s = %s.%s",
				e.sqlTable(s.Table),
				e.sqlTable(s.FromTable), s.OnFromCol,
				e.sqlTable(s.Table), s.OnToCol)
		}
		var groupConds []string
		for pi, pred := range preds {
			if ch.tables[lower(pred.table)] {
				groupConds = append(groupConds, pred.sql)
				predUsed[pi] = true
			}
		}
		if len(groupConds) > 0 {
			fmt.Fprintf(&body, " WHERE %s", strings.Join(groupConds, " AND "))
		}
		fmt.Fprintf(&fromBuf, "\nLEFT JOIN (%s) AS %s ON %s.__anchor IS NOT DISTINCT FROM %s.%s",
			body.String(), alias, alias,
			e.sqlTable(ch.anchor.FromTable), ch.anchor.OnFromCol)
	}

	var conds []string
	for _, ch := range chains {
		var b strings.Builder
		fmt.Fprintf(&b, "EXISTS (SELECT 1 FROM %s", e.sqlTable(ch.anchor.Table))
		for _, s := range ch.steps {
			fmt.Fprintf(&b, " JOIN %s ON %s.%s = %s.%s",
				e.sqlTable(s.Table),
				e.sqlTable(s.FromTable), s.OnFromCol,
				e.sqlTable(s.Table), s.OnToCol,
			)
		}
		// Correlate the chain's bridge key back to the outer FROM.
		fmt.Fprintf(&b, " WHERE %s.%s = %s.%s",
			e.sqlTable(ch.anchor.Table), ch.anchor.OnToCol,
			e.sqlTable(ch.anchor.FromTable), ch.anchor.OnFromCol,
		)
		for pi, p := range preds {
			if ch.tables[lower(p.table)] {
				fmt.Fprintf(&b, " AND %s", p.sql)
				predUsed[pi] = true
			}
		}
		b.WriteString(")")
		conds = append(conds, b.String())
	}
	for pi, p := range preds {
		if !predUsed[pi] {
			conds = append(conds, p.sql)
		}
	}
	return fromBuf.String(), conds, groupRefs, nil
}

// quoteIdent double-quotes name as a SQL identifier, escaping embedded quotes.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
