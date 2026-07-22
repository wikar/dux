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
		key := strings.ToLower(e.canonTable(t))
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

	// A dimension-only query (no table-bearing measures) routed here for bidi
	// handling still needs one CTE to carry the group keys.
	clusters := plan.clusters
	if tableClusterCount(clusters) == 0 && numKeys > 0 {
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

		// Cluster tables: group-key tables first (they root the join tree,
		// mirroring the flat path's primary), then the measure tables.
		seen := map[string]bool{}
		var tables []string
		add := func(t string) {
			key := strings.ToLower(e.canonTable(t))
			if !seen[key] {
				seen[key] = true
				tables = append(tables, t)
			}
		}
		for _, gk := range groupKeys {
			add(gk.table)
		}
		for _, t := range c.tables {
			add(t)
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
			if seen[strings.ToLower(e.canonTable(p.table))] {
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

		from, whereConds, err := e.stitchedClusterFrom(tables, needed, clusterTagged)
		if err != nil {
			return "", err
		}

		// Emit this cluster's measures in ITS filter context: the group keys
		// plus only the predicates routed to this cluster. CALCULATE modifier
		// resolution (filterctx.go) then removes/replicates the right filters.
		var items []string
		for ki, gc := range groupCols {
			items = append(items, fmt.Sprintf("%s AS k%d", gc, ki))
		}
		kn := len(groupCols)
		for gi, el := range rollupElems {
			for _, col := range el.cols {
				items = append(items, fmt.Sprintf("%s AS k%d", col, kn))
				kn++
			}
			// Raw 0/1 grouping level: part of the stitch key, and the source
			// of the ISSUBTOTAL indicator in the outer SELECT.
			items = append(items, fmt.Sprintf("GROUPING(%s) AS g%d", el.cols[0], gi))
		}
		prevCtx := e.groupCtx
		e.groupCtx = &groupContext{keys: groupKeys, preds: clusterTagged}
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
		if len(rollupElems) > 0 {
			fmt.Fprintf(&body, "\nGROUP BY %s", rollupGroupingSets(groupCols, rollupElems))
		} else if len(groupCols) > 0 {
			fmt.Fprintf(&body, "\nGROUP BY %s", strings.Join(groupCols, ", "))
		}
		ctes = append(ctes, stitchedCTE{name: cteName, body: body.String()})
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
	return sb.String(), nil
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

// stitchedClusterFrom builds a cluster CTE's FROM clause and WHERE conditions.
// needed is the set of tables the CTE's SELECT list references (group keys and
// measure tables); preds are the TREATAS predicates routed to this cluster.
//
// Steps toward needed tables emit as flat LEFT JOINs. A bidirectional step
// whose subtree contains only filter-source tables is carved into an EXISTS
// chain, with predicates on carved tables moved inside; remaining predicates
// are returned as plain conditions.
func (e *Emitter) stitchedClusterFrom(tables []string, needed map[string]bool, preds []taggedPred) (string, []string, error) {
	predSQL := func(ps []taggedPred) []string {
		out := make([]string, len(ps))
		for i, p := range ps {
			out[i] = p.sql
		}
		return out
	}
	if len(tables) == 1 {
		return e.sqlTable(tables[0]), predSQL(preds), nil
	}
	if e.Schema == nil {
		return "", nil, fmt.Errorf("SUMMARIZECOLUMNS: multi-table measures require a schema for join inference")
	}
	jp, err := semantic.InferJoinPath(e.Schema, tables)
	if err != nil {
		return "", nil, err
	}

	lower := func(t string) string { return strings.ToLower(e.canonTable(t)) }

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

	inChain := map[string]*existsChain{}
	var chains []*existsChain

	var fromBuf strings.Builder
	fromBuf.WriteString(e.sqlTable(tables[0]))
	for _, s := range jp.Steps {
		fl, tl := lower(s.FromTable), lower(s.Table)
		if ch := inChain[fl]; ch != nil {
			// Continuation of a carved chain.
			ch.steps = append(ch.steps, s)
			ch.tables[tl] = true
			inChain[tl] = ch
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

	var conds []string
	predUsed := make([]bool, len(preds))
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
	return fromBuf.String(), conds, nil
}

// quoteIdent double-quotes name as a SQL identifier, escaping embedded quotes.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
