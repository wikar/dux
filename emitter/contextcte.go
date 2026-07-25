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
	table string      // canonical table name as written
	alias string      // __anch0, __anch1, ...
	aggs  []anchorAgg // requested aggregates in first-use order
}

type anchorAgg struct {
	agg string // MIN or MAX
	col string // resolved SQL column name
}

// ref returns the anchor-scan column reference for agg(table.col), creating
// the scan and aggregate slots on first use.
func (c *anchorCollector) ref(agg, table, col string) string {
	var scan *anchorScan
	for _, s := range c.scans {
		if strings.EqualFold(s.table, table) {
			scan = s
			break
		}
	}
	if scan == nil {
		scan = &anchorScan{table: table, alias: fmt.Sprintf("__anch%d", len(c.scans))}
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
	name string
	body string
	// keyIdxs are the indices (into the enclosing group keys) the CTE groups
	// by and joins back on. Removed keys are absent, so the value repeats
	// across them — ALL semantics.
	keyIdxs []int
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
		anchored[strings.ToLower(s.table)] = s
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
		removed := cm.removed(gk.table, gk.col) ||
			overridden[strings.ToLower(gk.table)+"\x00"+strings.ToLower(gk.col)]
		if s := anchored[strings.ToLower(gk.table)]; s != nil {
			carried = append(carried, carriedKey{idx: i, gk: gk, scan: s, kept: !removed})
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

	// Tables the CTE joins: the measure subtree's own tables, kept group-key
	// tables, and surviving predicate tables (the latter filter-only, so they
	// stay eligible for bidi EXISTS carving in stitchedClusterFrom).
	seen := map[string]bool{}
	var tables []string
	needed := map[string]bool{}
	add := func(t string, need bool) {
		key := strings.ToLower(e.canonTable(t))
		if !seen[key] {
			seen[key] = true
			tables = append(tables, t)
		}
		if need {
			needed[key] = true
		}
	}
	for _, t := range clusterTables {
		add(t, true)
	}
	for _, ck := range carried {
		add(ck.gk.table, true)
	}
	for _, p := range keptOuter {
		if p.table != "" {
			add(p.table, false)
		}
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("CALCULATE: cannot determine a source table for the cleared filter context")
	}

	from, conds, err := e.stitchedClusterFrom(tables, needed, keptOuter)
	if err != nil {
		return nil, err
	}

	// Append one uncorrelated anchor scan per anchored table. The range
	// predicates in the WHERE clause tie each scan cell to its window rows.
	var fromBuf strings.Builder
	fromBuf.WriteString(from)
	for _, s := range coll.scans {
		var items []string
		var keys []string
		for _, gk := range e.groupCtx.keys {
			if strings.EqualFold(gk.table, s.table) {
				items = append(items, gk.col)
				keys = append(keys, gk.col)
			}
		}
		for i, a := range s.aggs {
			items = append(items, fmt.Sprintf("%s(%s) AS a%d", a.agg, a.col, i))
		}
		var scanPreds []string
		for _, p := range e.groupCtx.preds {
			if p.table == strings.ToLower(s.table) {
				scanPreds = append(scanPreds, p.sql)
			}
		}
		fmt.Fprintf(&fromBuf, "\nCROSS JOIN (SELECT %s FROM %s", strings.Join(items, ", "), e.sqlTable(s.table))
		if len(scanPreds) > 0 {
			fmt.Fprintf(&fromBuf, " WHERE %s", strings.Join(scanPreds, " AND "))
		}
		if len(keys) > 0 {
			fmt.Fprintf(&fromBuf, " GROUP BY %s", strings.Join(keys, ", "))
		}
		fmt.Fprintf(&fromBuf, ") AS %s", s.alias)
	}

	// Kept keys on an anchored table pin the cleared copy to the cell.
	for _, ck := range carried {
		if ck.scan != nil && ck.kept {
			conds = append(conds, fmt.Sprintf("%s.%s = %s.%s",
				e.sqlTable(ck.gk.table), ck.gk.col, ck.scan.alias, ck.gk.col))
		}
	}
	conds = append(conds, timeConds...)
	for _, p := range append(append([]*parser.Expr{}, cm.preds...), cm.keepPreds...) {
		s, predErr := e.emitExpr(p)
		if predErr != nil {
			return nil, predErr
		}
		conds = append(conds, s)
	}

	// The CTE's own grouped context: nested CALCULATE calls inside the value
	// expression resolve their modifiers against the carried keys.
	carriedKeys := make([]groupKey, len(carried))
	keyExprs := make([]string, len(carried))
	keyIdxs := make([]int, len(carried))
	for i, ck := range carried {
		carriedKeys[i] = ck.gk
		keyIdxs[i] = ck.idx
		if ck.scan != nil {
			keyExprs[i] = ck.scan.alias + "." + ck.gk.col
		} else {
			keyExprs[i] = e.sqlTable(ck.gk.table) + "." + ck.gk.col
		}
	}
	prevCtx := e.groupCtx
	e.groupCtx = &groupContext{keys: carriedKeys, preds: keptOuter}
	inner, err := e.emitExpr(fc.Args[0])
	e.groupCtx = prevCtx
	if err != nil {
		return nil, err
	}

	var items []string
	for i, ck := range carried {
		items = append(items, fmt.Sprintf("%s AS k%d", keyExprs[i], ck.idx))
	}
	items = append(items, fmt.Sprintf("(%s) AS v", inner))

	var body strings.Builder
	fmt.Fprintf(&body, "SELECT %s\nFROM %s", strings.Join(items, ", "), fromBuf.String())
	if len(conds) > 0 {
		fmt.Fprintf(&body, "\nWHERE %s", strings.Join(conds, " AND "))
	}
	if len(keyExprs) > 0 {
		fmt.Fprintf(&body, "\nGROUP BY %s", strings.Join(keyExprs, ", "))
	}
	return &contextCTE{name: name, body: body.String(), keyIdxs: keyIdxs}, nil
}
