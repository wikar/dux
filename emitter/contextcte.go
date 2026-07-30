// Context CTEs: per-measure codegen for CALCULATE calls that modify the group
// filter context (ALL-family removals, group-key overrides, time intelligence).
//
// The former correlated-subquery form nested the time anchor — itself a
// correlated scalar subquery — inside the CALCULATE removal subquery. DuckDB
// cannot convert a range predicate on a nested subquery result into a join
// condition, so the range filter landed above a |group values| × |fact| delim
// join (a day-grain rolling window against a 3M-row fact materialised tens of
// gigabytes before being killed).
//
// Instead, each context-modifying CALCULATE is evaluated in its own grouped
// CTE — its private filter context — and LEFT JOINed back onto the stitched
// group keys it retains (see stitched.go). Time anchors become columns of an
// uncorrelated per-table "anchor scan" enumerating that table's group cells
// with the required MIN/MAX extremes; the range predicate then joins the
// anchor scan to the cleared table copy directly:
//
//	_cc0 AS (
//	  SELECT __anch0.Date AS k0, SUM(_Quantity) AS v
//	  FROM Sales
//	  LEFT JOIN Date ON Sales.DateKey = Date.DateKey
//	  CROSS JOIN (SELECT Date, MAX(Date) AS a0 FROM Date GROUP BY Date) AS __anch0
//	  WHERE Date.Date > __anch0.a0 + (-7) * INTERVAL 1 DAY AND Date.Date <= __anch0.a0
//	  GROUP BY __anch0.Date
//	)
//
// Every intermediate is |group cells|- or |fact|-sized and the query contains
// no correlated subqueries at all.
package emitter

import (
	"fmt"
	"strings"

	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// anchorCollector gathers the MIN/MAX anchor requests made while emitting the
// time-intelligence predicates of one context CTE. One anchor scan is built
// per anchored table: its group-key cells with the requested extremes,
// computed in the PRE-clearing filter context (outer predicates on the table
// still apply; the clearing only affects the measure scan).
type anchorCollector struct {
	scans []*anchorScan
}

type anchorScan struct {
	table  string         // canonical schema table used for SQL rendering
	key    string         // canonical, case-insensitive table identity
	alias  string         // __anch0, __anch1, ...
	aggs   []anchorAgg    // requested aggregates in first-use order
	groups map[int]string // enclosing group-key index → projected gN alias
}

type anchorAgg struct {
	agg  string // MIN, MAX, or LASTNONBLANK
	col  string // resolved SQL column name
	expr *parser.Expr
}

func (c *anchorCollector) refLastNonBlank(table, key, col string, expr *parser.Expr) string {
	var scan *anchorScan
	for _, s := range c.scans {
		if s.key == key {
			scan = s
			break
		}
	}
	if scan == nil {
		scan = &anchorScan{table: table, key: key, alias: fmt.Sprintf("__anch%d", len(c.scans)), groups: map[int]string{}}
		c.scans = append(c.scans, scan)
	}
	for i, agg := range scan.aggs {
		if agg.agg == "LASTNONBLANK" && strings.EqualFold(agg.col, col) && agg.expr == expr {
			return fmt.Sprintf("%s.a%d", scan.alias, i)
		}
	}
	scan.aggs = append(scan.aggs, anchorAgg{agg: "LASTNONBLANK", col: col, expr: expr})
	return fmt.Sprintf("%s.a%d", scan.alias, len(scan.aggs)-1)
}

// ref returns the anchor-scan column reference for agg(table.col), creating
// the scan and aggregate slots on first use.
func (c *anchorCollector) ref(agg, table, key, col string) string {
	var scan *anchorScan
	for _, s := range c.scans {
		if s.key == key {
			scan = s
			break
		}
	}
	if scan == nil {
		scan = &anchorScan{table: table, key: key, alias: fmt.Sprintf("__anch%d", len(c.scans)), groups: map[int]string{}}
		c.scans = append(c.scans, scan)
	}
	for i, a := range scan.aggs {
		if a.agg == agg && strings.EqualFold(a.col, col) {
			return fmt.Sprintf("%s.a%d", scan.alias, i)
		}
	}
	scan.aggs = append(scan.aggs, anchorAgg{agg: agg, col: col})
	return fmt.Sprintf("%s.a%d", scan.alias, len(scan.aggs)-1)
}

// contextCTE is one emitted per-measure context CTE.
type contextCTE struct {
	name     string
	keyItems []string
	values   []string
	fromBody string
	// keyIdxs are the indices (into the enclosing group keys) the CTE groups
	// by and joins back on. Removed keys are absent, so the value repeats
	// across them — ALL semantics.
	keyIdxs []int
}

func (c *contextCTE) fusionKey() string {
	return strings.Join(c.keyItems, "\x00") + "\x01" + c.fromBody
}

func (c *contextCTE) addValue(value string) string {
	alias := "v"
	if len(c.values) > 0 {
		alias = fmt.Sprintf("v%d", len(c.values))
	}
	c.values = append(c.values, value)
	return alias
}

func (c *contextCTE) sqlBody() string {
	items := append([]string{}, c.keyItems...)
	for i, value := range c.values {
		alias := "v"
		if i > 0 {
			alias = fmt.Sprintf("v%d", i)
		}
		items = append(items, fmt.Sprintf("(%s) AS %s", value, alias))
	}
	return "SELECT " + strings.Join(items, ", ") + "\n" + c.fromBody
}

// bridgePlan detaches one anchored table from the cluster join tree. The value
// may not read that table; every other reference must be an anchor-only
// predicate that can move into the bridge. The decision is structural and
// local to the query; it is never configuration.
type bridgePlan struct {
	scan     *anchorScan
	edge     semantic.JoinStep
	alias    string
	keyExprs map[int]string // enclosing group-key index -> bridge projection
}

func (e *Emitter) planBridge(
	fc *parser.FuncCall,
	coll *anchorCollector,
	cm *calcModifiers,
	tables []string,
	keptOuter []taggedPred,
) (*bridgePlan, error) {
	if e.Schema == nil || len(coll.scans) != 1 || len(tables) <= 1 {
		return nil, nil
	}
	scan := coll.scans[0]
	for _, agg := range scan.aggs {
		if agg.expr != nil {
			return nil, nil
		}
	}
	measureTables := e.measureValueTables(fc.Args[0])
	for _, table := range measureTables {
		if e.tableKey(table) == scan.key {
			return nil, nil
		}
	}
	withinAnchor := func(expr *parser.Expr) bool {
		for _, table := range e.measureExprTables(expr) {
			if e.tableKey(table) != scan.key {
				return false
			}
		}
		return true
	}
	for _, pred := range append(append([]*parser.Expr{}, cm.preds...), cm.keepPreds...) {
		if !withinAnchor(pred) {
			return nil, nil
		}
	}
	for _, tf := range cm.timeFilters {
		for _, arg := range tf.args {
			if !withinAnchor(arg) {
				return nil, nil
			}
		}
	}
	// The spike showed that any predicate which reduces the measure scan can
	// make a fact-blind bridge slower. This catches direct fact filters and
	// dimension filters which propagate to the measure tables.
	for _, pred := range keptOuter {
		if pred.table != "" && pred.table != scan.key &&
			semantic.FilterReaches(e.Schema, pred.table, measureTables) {
			return nil, nil
		}
	}
	jp, err := semantic.InferJoinPath(e.Schema, tables)
	if err != nil {
		return nil, err
	}
	edge, ok := jp.LeafEdge(e.Schema, scan.table)
	if !ok {
		return nil, nil
	}
	return &bridgePlan{scan: scan, edge: edge, alias: "__brdg0", keyExprs: map[int]string{}}, nil
}

// emitContextCTE emits fc — a context-modifying CALCULATE (use calcForm for
// TOTAL*TD) — as a grouped CTE. clusterTables is the measure-expanded table
// set of the subtree; e.groupCtx must hold the full group keys and the
// predicates routed to this cluster.
func (e *Emitter) emitContextCTE(name string, fc *parser.FuncCall, clusterTables []string) (*contextCTE, error) {
	cm, err := e.classifyCalcArgs(fc.Args[1:])
	if err != nil {
		return nil, err
	}
	overridden := e.predColKeys(cm.preds)

	// Emit the time-intelligence range predicates with the anchor collector
	// active: every anchor becomes a column of a per-table anchor scan.
	coll := &anchorCollector{}
	prevScans := e.anchorScans
	e.anchorScans = coll
	var timeConds []string
	for _, tf := range cm.timeFilters {
		pred, predErr := e.emitTimeIntelPred(tf, e.sqlTable(tf.table)+"."+tf.col)
		if predErr != nil {
			e.anchorScans = prevScans
			return nil, predErr
		}
		timeConds = append(timeConds, pred)
	}
	// Restored before the value expression is emitted: a nested CALCULATE in
	// there is not part of this CTE's anchor scans.
	e.anchorScans = prevScans

	anchored := map[string]*anchorScan{}
	for _, s := range coll.scans {
		anchored[s.key] = s
	}

	// Partition the group keys. Keys on an anchored table re-enter through the
	// anchor scan even when the modifiers clear that table's filters — the
	// anchor is a function of the group cell. Kept keys on an anchored table
	// additionally constrain the cleared table copy (the old correlation,
	// now an equality against the anchor scan).
	type carriedKey struct {
		idx  int
		gk   groupKey
		scan *anchorScan // non-nil when provided by an anchor scan
		kept bool        // still filters the cleared table copy
	}
	var carried []carriedKey
	for i, gk := range e.groupCtx.keys {
		removed := cm.removed(e.tableKey(gk.table), gk.col) ||
			overridden[e.resolvedColKey(gk.table, gk.col)]
		var groupScan *anchorScan
		if s := anchored[e.tableKey(gk.table)]; s != nil {
			groupScan = s
		} else if e.Schema != nil {
			for _, s := range coll.scans {
				targets := []string{s.table}
				for _, agg := range s.aggs {
					if agg.expr != nil {
						targets = append(targets, e.measureExprTables(agg.expr)...)
					}
				}
				if semantic.GroupingReaches(e.Schema, gk.table, targets) {
					groupScan = s
					break
				}
			}
		}
		if groupScan != nil {
			groupScan.groups[i] = fmt.Sprintf("g%d", len(groupScan.groups))
			carried = append(carried, carriedKey{idx: i, gk: gk, scan: groupScan, kept: !removed})
		} else if !removed {
			carried = append(carried, carriedKey{idx: i, gk: gk, kept: true})
		}
	}

	// Outer predicates that survive the modifiers apply to the measure scan.
	// Predicates on anchored tables always apply to the anchor scan below,
	// removed or not (the anchor sees the pre-clearing context).
	var keptOuter []taggedPred
	for _, p := range e.groupCtx.preds {
		if p.table != "" && cm.removed(p.table, p.col) {
			continue
		}
		if p.table != "" && p.col != "" && overridden[p.table+"\x00"+p.col] {
			continue
		}
		keptOuter = append(keptOuter, p)
	}

	// CALCULATE predicates are filter-only tables. Tag them before building the
	// FROM clause so a chain crossing a bidirectional edge can be carved into
	// the same correlated EXISTS semi-join used for outer filters.
	var calcTagged []taggedPred
	for _, pred := range append(append([]*parser.Expr{}, cm.preds...), cm.keepPreds...) {
		s, predErr := e.emitExpr(pred)
		if predErr != nil {
			return nil, predErr
		}
		tagged := taggedPred{sql: s, expr: pred}
		for _, cr := range collectColRefs(pred) {
			if cr.Table == "" {
				continue
			}
			table := semantic.StripSingleQuotes(cr.Table)
			tagged.table = e.tableKey(table)
			tagged.col = strings.ToLower(e.resolveColName(table, semantic.StripBrackets(cr.Column)))
			break
		}
		calcTagged = append(calcTagged, tagged)
	}

	// Tables the CTE joins: the measure subtree's own tables, kept group-key
	// tables, and surviving predicate tables (the latter filter-only, so they
	// stay eligible for bidi EXISTS carving in stitchedClusterFrom).
	seen := map[string]bool{}
	var tables []string
	needed := map[string]bool{}
	add := func(t string, need bool) {
		key := e.tableKey(t)
		if !seen[key] {
			seen[key] = true
			tables = append(tables, t)
		}
		if need {
			needed[key] = true
		}
	}
	valueTables := map[string]bool{}
	for _, table := range e.measureValueTables(fc.Args[0]) {
		valueTables[e.tableKey(table)] = true
	}
	for _, t := range clusterTables {
		add(t, valueTables[e.tableKey(t)])
	}
	for _, ck := range carried {
		add(ck.gk.table, true)
	}
	for _, p := range keptOuter {
		if p.table != "" {
			add(p.table, false)
		}
	}
	for _, p := range calcTagged {
		if p.table != "" {
			add(p.table, false)
		}
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("CALCULATE: cannot determine a source table for the cleared filter context")
	}

	bp, err := e.planBridge(fc, coll, cm, tables, keptOuter)
	if err != nil {
		return nil, err
	}
	clusterPreds := keptOuter
	if bp == nil {
		clusterPreds = append(append([]taggedPred{}, keptOuter...), calcTagged...)
	}
	var bridgeKept []taggedPred
	if bp != nil {
		clusterPreds = nil
		for _, pred := range keptOuter {
			if pred.table == bp.scan.key {
				bridgeKept = append(bridgeKept, pred)
			} else {
				clusterPreds = append(clusterPreds, pred)
			}
		}
		keptTables := tables[:0]
		for _, table := range tables {
			if e.tableKey(table) != bp.scan.key {
				keptTables = append(keptTables, table)
			}
		}
		tables = keptTables
		delete(needed, bp.scan.key)
	}

	from, conds, _, err := e.stitchedClusterFrom(tables, needed, clusterPreds, nil)
	if err != nil {
		return nil, err
	}

	var outerCalcConds, bridgeCalcConds []string
	if bp != nil {
		for _, pred := range calcTagged {
			if len(e.measureExprTables(pred.expr)) > 0 {
				bridgeCalcConds = append(bridgeCalcConds, pred.sql)
			} else {
				outerCalcConds = append(outerCalcConds, pred.sql)
			}
		}
	}

	var fromBuf strings.Builder
	fromBuf.WriteString(from)
	if bp == nil {
		// Current form: append one anchor scan and keep the range predicate in
		// the context CTE's outer WHERE.
		for _, scan := range coll.scans {
			scanSQL, scanErr := e.emitAnchorScan(scan)
			if scanErr != nil {
				return nil, scanErr
			}
			fmt.Fprintf(&fromBuf, "\nCROSS JOIN %s", scanSQL)
		}
		for _, ck := range carried {
			if ck.scan != nil && ck.kept {
				conds = append(conds, fmt.Sprintf("%s.%s IS NOT DISTINCT FROM %s.%s",
					e.sqlTable(ck.gk.table), ck.gk.col, ck.scan.alias, ck.scan.groups[ck.idx]))
			}
		}
		conds = append(conds, timeConds...)
	} else {
		// Bridge form: compute date-window membership before joining facts.
		var anchorItems, anchorGroups, bridgeItems, bridgeConds []string
		groupAlias := map[int]string{}
		for gi, gk := range e.groupCtx.keys {
			if e.tableKey(gk.table) == bp.scan.key {
				alias := fmt.Sprintf("g%d", len(groupAlias))
				groupAlias[gi] = alias
				anchorItems = append(anchorItems, fmt.Sprintf("%s AS %s", gk.col, alias))
				anchorGroups = append(anchorGroups, gk.col)
			}
		}
		for i, agg := range bp.scan.aggs {
			anchorItems = append(anchorItems, fmt.Sprintf("%s(%s) AS a%d", agg.agg, agg.col, i))
		}
		var scanPreds []string
		for _, pred := range e.groupCtx.preds {
			if pred.table == bp.scan.key {
				scanPreds = append(scanPreds, pred.sql)
			}
		}
		var anchor strings.Builder
		fmt.Fprintf(&anchor, "(SELECT %s FROM %s", strings.Join(anchorItems, ", "), e.sqlTable(bp.scan.table))
		if len(scanPreds) > 0 {
			fmt.Fprintf(&anchor, " WHERE %s", strings.Join(scanPreds, " AND "))
		}
		if len(anchorGroups) > 0 {
			fmt.Fprintf(&anchor, " GROUP BY %s", strings.Join(anchorGroups, ", "))
		}
		fmt.Fprintf(&anchor, ") AS %s", bp.scan.alias)

		bridgeItems = append(bridgeItems, fmt.Sprintf("%s.%s AS bk", e.sqlTable(bp.scan.table), bp.edge.OnToCol))
		bridgeKey := 0
		for _, ck := range carried {
			if ck.scan == bp.scan {
				ga := groupAlias[ck.idx]
				ba := fmt.Sprintf("b%d", bridgeKey)
				bridgeItems = append(bridgeItems, fmt.Sprintf("%s.%s AS %s", bp.scan.alias, ga, ba))
				bp.keyExprs[ck.idx] = bp.alias + "." + ba
				bridgeKey++
				if ck.kept {
					bridgeConds = append(bridgeConds, fmt.Sprintf("%s.%s IS NOT DISTINCT FROM %s.%s",
						e.sqlTable(ck.gk.table), ck.gk.col, bp.scan.alias, ga))
				}
			}
		}
		for _, pred := range bridgeKept {
			bridgeConds = append(bridgeConds, pred.sql)
		}
		bridgeConds = append(bridgeConds, timeConds...)
		bridgeConds = append(bridgeConds, bridgeCalcConds...)

		fmt.Fprintf(&fromBuf, "\nJOIN (SELECT %s FROM %s\nCROSS JOIN %s",
			strings.Join(bridgeItems, ", "), e.sqlTable(bp.scan.table), anchor.String())
		if len(bridgeConds) > 0 {
			fmt.Fprintf(&fromBuf, " WHERE %s", strings.Join(bridgeConds, " AND "))
		}
		fmt.Fprintf(&fromBuf, ") AS %s ON %s.%s = %s.bk",
			bp.alias, e.sqlTable(bp.edge.FromTable), bp.edge.OnFromCol, bp.alias)
	}
	conds = append(conds, outerCalcConds...)

	// The CTE's own grouped context: nested CALCULATE calls inside the value
	// expression resolve their modifiers against the carried keys.
	carriedKeys := make([]groupKey, len(carried))
	keyExprs := make([]string, len(carried))
	keyIdxs := make([]int, len(carried))
	for i, ck := range carried {
		carriedKeys[i] = ck.gk
		keyIdxs[i] = ck.idx
		if bp != nil && ck.scan == bp.scan {
			keyExprs[i] = bp.keyExprs[ck.idx]
		} else if ck.scan != nil {
			keyExprs[i] = ck.scan.alias + "." + ck.scan.groups[ck.idx]
		} else {
			keyExprs[i] = e.sqlTable(ck.gk.table) + "." + ck.gk.col
		}
		carriedKeys[i].expr = keyExprs[i]
	}
	prevCtx := e.groupCtx
	e.groupCtx = &groupContext{keys: carriedKeys, preds: append(append([]taggedPred{}, keptOuter...), calcTagged...)}
	inner, err := e.emitExpr(fc.Args[0])
	e.groupCtx = prevCtx
	if err != nil {
		return nil, err
	}

	var keyItems []string
	for i, ck := range carried {
		keyItems = append(keyItems, fmt.Sprintf("%s AS k%d", keyExprs[i], ck.idx))
	}

	var fromBody strings.Builder
	fmt.Fprintf(&fromBody, "FROM %s", fromBuf.String())
	if len(conds) > 0 {
		fmt.Fprintf(&fromBody, "\nWHERE %s", strings.Join(conds, " AND "))
	}
	if len(keyExprs) > 0 {
		fmt.Fprintf(&fromBody, "\nGROUP BY %s", strings.Join(keyExprs, ", "))
	}
	return &contextCTE{name: name, keyItems: keyItems, values: []string{inner}, fromBody: fromBody.String(), keyIdxs: keyIdxs}, nil
}

func (e *Emitter) emitAnchorScan(scan *anchorScan) (string, error) {
	var groupItems, groupExprs, scanTables []string
	scanTables = append(scanTables, scan.table)
	for gi, gk := range e.groupCtx.keys {
		alias, ok := scan.groups[gi]
		if !ok {
			continue
		}
		expr := e.sqlTable(gk.table) + "." + gk.col
		groupItems = append(groupItems, fmt.Sprintf("%s AS %s", expr, alias))
		groupExprs = append(groupExprs, expr)
		scanTables = append(scanTables, gk.table)
	}

	hasNonBlank := false
	var expressionTables []string
	for _, agg := range scan.aggs {
		if agg.expr == nil {
			continue
		}
		hasNonBlank = true
		expressionTables = append(expressionTables, e.measureExprTables(agg.expr)...)
		scanTables = append(scanTables, e.measureExprTables(agg.expr)...)
	}

	var scanPreds []string
	for _, pred := range e.groupCtx.preds {
		if pred.table == scan.key || (pred.table != "" && semantic.GroupingReaches(e.Schema, pred.table, expressionTables)) {
			scanPreds = append(scanPreds, pred.sql)
			scanTables = append(scanTables, pred.table)
		}
	}

	seen := map[string]bool{}
	uniqueTables := make([]string, 0, len(scanTables))
	primary := scan.table
	if hasNonBlank && len(expressionTables) > 0 {
		// LASTNONBLANK evaluates the expression at candidate grain. Rooting the
		// join at the expression fact keeps conform dimensions on that fact's
		// relationship path instead of accidentally reaching them through a
		// sibling fact hanging off the candidate-date table.
		primary = expressionTables[0]
		uniqueTables = append(uniqueTables, primary)
		seen[e.tableKey(primary)] = true
	}
	for _, table := range scanTables {
		key := e.tableKey(table)
		if !seen[key] {
			seen[key] = true
			uniqueTables = append(uniqueTables, table)
		}
	}
	scanFrom := e.sqlTable(primary)
	if len(uniqueTables) > 1 {
		jp, err := semantic.InferJoinPath(e.Schema, uniqueTables)
		if err != nil {
			return "", err
		}
		scanFrom = e.emitFlatJoins(primary, jp)
	}

	if !hasNonBlank {
		items := append([]string{}, groupItems...)
		for i, agg := range scan.aggs {
			items = append(items, fmt.Sprintf("%s(%s.%s) AS a%d", agg.agg, e.sqlTable(scan.table), agg.col, i))
		}
		var b strings.Builder
		fmt.Fprintf(&b, "(SELECT %s FROM %s", strings.Join(items, ", "), scanFrom)
		if len(scanPreds) > 0 {
			fmt.Fprintf(&b, " WHERE %s", strings.Join(scanPreds, " AND "))
		}
		if len(groupExprs) > 0 {
			fmt.Fprintf(&b, " GROUP BY %s", strings.Join(groupExprs, ", "))
		}
		fmt.Fprintf(&b, ") AS %s", scan.alias)
		return b.String(), nil
	}

	innerItems := append([]string{}, groupItems...)
	innerGroups := append([]string{}, groupExprs...)
	outerItems := make([]string, 0, len(groupItems)+len(scan.aggs))
	outerGroups := make([]string, 0, len(groupItems))
	for gi := range e.groupCtx.keys {
		if alias, ok := scan.groups[gi]; ok {
			outerItems = append(outerItems, alias)
			outerGroups = append(outerGroups, alias)
		}
	}
	for i, agg := range scan.aggs {
		candidate := e.sqlTable(scan.table) + "." + agg.col
		innerItems = append(innerItems, fmt.Sprintf("%s AS c%d", candidate, i))
		innerGroups = append(innerGroups, candidate)
		if agg.expr == nil {
			outerItems = append(outerItems, fmt.Sprintf("%s(c%d) AS a%d", agg.agg, i, i))
			continue
		}
		value, err := e.emitExpr(agg.expr)
		if err != nil {
			return "", err
		}
		innerItems = append(innerItems, fmt.Sprintf("%s AS n%d", value, i))
		outerItems = append(outerItems, fmt.Sprintf("MAX(c%d) FILTER (WHERE n%d IS NOT NULL) AS a%d", i, i, i))
	}

	var inner strings.Builder
	fmt.Fprintf(&inner, "SELECT %s FROM %s", strings.Join(innerItems, ", "), scanFrom)
	if len(scanPreds) > 0 {
		fmt.Fprintf(&inner, " WHERE %s", strings.Join(scanPreds, " AND "))
	}
	if len(innerGroups) > 0 {
		fmt.Fprintf(&inner, " GROUP BY %s", strings.Join(innerGroups, ", "))
	}
	var outer strings.Builder
	fmt.Fprintf(&outer, "(SELECT %s FROM (%s) AS __nb", strings.Join(outerItems, ", "), inner.String())
	if len(outerGroups) > 0 {
		fmt.Fprintf(&outer, " GROUP BY %s", strings.Join(outerGroups, ", "))
	}
	fmt.Fprintf(&outer, ") AS %s", scan.alias)
	return outer.String(), nil
}
