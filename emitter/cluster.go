// Measure clustering for SUMMARIZECOLUMNS.
//
// A DAX measure is defined against its own filter context: conceptually each
// measure gets a private query per group cell. Emitting every measure over one
// flat join tree breaks that the moment two measures aggregate different fact
// tables — the facts fan out against each other and every SUM inflates.
//
// clusterMeasures partitions the measure expressions of a SUMMARIZECOLUMNS
// call by the set of tables they reference (expanding stored measures), so the
// emitter can evaluate each cluster in its own grouped CTE and stitch the
// results on the group keys ("stitched codegen", see stitched.go).
package emitter

import (
	"sort"
	"strings"

	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// measureCluster is one group of measure units sharing a join tree.
type measureCluster struct {
	// key is the canonical cluster identity: the sorted, lower-cased,
	// schema-resolved table set joined with '\x00'. Empty for measures that
	// reference no table at all (pure scalar expressions).
	key string
	// tables holds the referenced tables in first-seen order, original
	// spelling, deduplicated case-insensitively.
	tables []string
	// pairIdx are indices of the NAME argument of each name/expr measure pair
	// whose WHOLE expression evaluates in this cluster (the expression is at
	// pairIdx+1).
	pairIdx []int
	// inlineIdx are indices into the inline measure args (measure references
	// appearing in the group-column position) evaluated whole in this cluster.
	inlineIdx []int
	// lifted are aggregate subtrees hoisted out of cross-cluster expressions:
	// each evaluates in this cluster's CTE and is referenced by the stitched
	// outer SELECT via the substitution map (see emitStitched).
	lifted []*parser.FuncCall
}

// measurePlan is the full clustering result for one SUMMARIZECOLUMNS call.
type measurePlan struct {
	clusters []*measureCluster
	// splitPairs are name-arg indices of pairs whose aggregates span more than
	// one cluster; their outer arithmetic is emitted in the stitched SELECT
	// with each aggregate substituted by its cluster CTE column.
	splitPairs []int
	// splitInline is the same for inline measure references.
	splitInline []int
}

// planMeasures partitions the measure units of a SUMMARIZECOLUMNS call into
// clusters. The unit of clustering is the maximal aggregate subtree: an
// expression whose aggregates all share one table set is assigned whole,
// while a cross-cluster expression (e.g. SUM(a[x]) / SUM(b[y])) has each
// aggregate lifted into its own cluster. Clusters are returned in
// first-appearance order; a scalar-only cluster (key == "") sorts last.
func (e *Emitter) planMeasures(pairArgs, inlineArgs []*parser.Expr) *measurePlan {
	p := &measurePlan{}
	byKey := map[string]*measureCluster{}

	clusterFor := func(tables []string) *measureCluster {
		key := e.clusterKey(tables)
		if c, ok := byKey[key]; ok {
			return c
		}
		c := &measureCluster{key: key, tables: tables}
		byKey[key] = c
		p.clusters = append(p.clusters, c)
		return c
	}

	// assign clusters one measure expression, reporting either the whole-expr
	// home cluster (nil for split) via the return value.
	assign := func(expr *parser.Expr) (whole *measureCluster) {
		subtrees := e.aggSubtrees(expr)
		distinct := map[string]bool{}
		for _, st := range subtrees {
			distinct[e.clusterKey(e.measureExprTables(exprOfFunc(st)))] = true
		}
		if len(distinct) <= 1 {
			// Zero or one aggregate table-set: the whole expression lives in
			// one context, keyed by everything it references.
			return clusterFor(e.measureExprTables(expr))
		}
		for _, st := range subtrees {
			c := clusterFor(e.measureExprTables(exprOfFunc(st)))
			c.lifted = append(c.lifted, st)
		}
		return nil
	}

	for i := 0; i+1 < len(pairArgs); i += 2 {
		if c := assign(pairArgs[i+1]); c != nil {
			c.pairIdx = append(c.pairIdx, i)
		} else {
			p.splitPairs = append(p.splitPairs, i)
		}
	}
	for j, arg := range inlineArgs {
		if c := assign(arg); c != nil {
			c.inlineIdx = append(c.inlineIdx, j)
		} else {
			p.splitInline = append(p.splitInline, j)
		}
	}

	// Scalar-only measures (no tables) last, so cluster 0 is always a real one.
	sort.SliceStable(p.clusters, func(a, b int) bool {
		return (p.clusters[a].key != "") && (p.clusters[b].key == "")
	})
	return p
}

// liftableAggFuncs are the aggregate functions treated as maximal subtrees by
// the cross-cluster rewriter: each evaluates to one value per group cell and
// can be computed inside a cluster CTE and referenced from the outer SELECT.
// CALCULATE and the time-intelligence totals are also opaque units — their
// filter arguments (including aggregates like MAX(dates[date]) inside
// DATESINPERIOD) belong to the measure's own context and must never be
// clustered separately.
var liftableAggFuncs = map[string]bool{
	"SUM": true, "AVERAGE": true, "COUNT": true, "COUNTA": true,
	"COUNTBLANK": true, "MIN": true, "MAX": true, "MEDIAN": true,
	"COUNTROWS": true, "DISTINCTCOUNT": true,
	"SUMX": true, "AVERAGEX": true, "COUNTX": true, "MINX": true,
	"MAXX": true, "CONCATENATEX": true,
	"CALCULATE": true, "TOTALYTD": true, "TOTALQTD": true, "TOTALMTD": true,
}

// isLiftable reports whether a function call is a maximal clustering unit.
func isLiftable(name string) bool {
	return liftableAggFuncs[name] || isTimeIntelRangeFunc(name)
}

// aggSubtrees returns the maximal aggregate subtrees of expr, expanding
// stored measure references (with a cycle guard) and never descending into a
// lifted aggregate.
func (e *Emitter) aggSubtrees(expr *parser.Expr) []*parser.FuncCall {
	var result []*parser.FuncCall
	visiting := map[*parser.MeasureDefinition]bool{}

	var visit func(*parser.Term) bool
	visit = func(t *parser.Term) bool {
		if t.ColRef != nil {
			if def := e.resolveMeasureDef(t.ColRef); def != nil && def.Expr != nil && !visiting[def] {
				visiting[def] = true
				walkTerms(def.Expr, visit)
				delete(visiting, def)
			}
		}
		if t.FuncCall != nil && isLiftable(strings.ToUpper(t.FuncCall.Name)) {
			result = append(result, t.FuncCall)
			return false // maximal: do not descend
		}
		return true
	}

	walkTerms(expr, visit)
	return result
}

// resolveMeasureDef returns the stored measure definition a ColRef resolves
// to, or nil when it is a plain column reference.
func (e *Emitter) resolveMeasureDef(cr *parser.ColRef) *parser.MeasureDefinition {
	measures := e.effectiveMeasures()
	if measures == nil {
		return nil
	}
	name := semantic.StripBrackets(cr.Column)
	if tbl := semantic.StripSingleQuotes(cr.Table); tbl != "" {
		if tm, ok := measures[tbl]; ok {
			return tm[name]
		}
		return nil
	}
	def, err := semantic.FindMeasureByName(name, measures)
	if err != nil {
		return nil
	}
	return def
}

// exprOfFunc wraps a FuncCall back into an Expr for helpers that walk
// expressions.
func exprOfFunc(fc *parser.FuncCall) *parser.Expr {
	return &parser.Expr{Left: &parser.Term{FuncCall: fc}}
}

// tableClusterCount returns how many clusters reference at least one table.
// Stitched codegen activates when this exceeds 1.
func tableClusterCount(clusters []*measureCluster) int {
	n := 0
	for _, c := range clusters {
		if c.key != "" {
			n++
		}
	}
	return n
}

// clusterKey canonicalises a table list: schema-resolved, lower-cased, sorted,
// '\x00'-joined. "Sales" and "bev.Sales" resolve to the same key when the
// schema stores the qualified name.
func (e *Emitter) clusterKey(tables []string) string {
	if len(tables) == 0 {
		return ""
	}
	canon := make([]string, len(tables))
	for i, t := range tables {
		canon[i] = strings.ToLower(e.canonTable(t))
	}
	sort.Strings(canon)
	return strings.Join(canon, "\x00")
}

// canonTable resolves a table reference to its canonical schema key when a
// schema is available, else returns it unchanged.
func (e *Emitter) canonTable(name string) string {
	if e.Schema != nil {
		return semantic.ResolveTable(e.Schema, name)
	}
	return name
}

// measureExprTables returns every table referenced by expr, expanding measure
// references (Table[Measure] and bare [Measure]) through the effective measure
// store so a stored measure's underlying tables are attributed to the
// referencing expression. Cycles between stored measures are guarded.
func (e *Emitter) measureExprTables(expr *parser.Expr) []string {
	seen := map[string]bool{}
	var result []string
	visiting := map[*parser.MeasureDefinition]bool{}

	add := func(t string) {
		key := strings.ToLower(e.canonTable(t))
		if !seen[key] {
			seen[key] = true
			result = append(result, t)
		}
	}

	var visit func(*parser.Term) bool
	visit = func(t *parser.Term) bool {
		if t.ColRef != nil {
			if def := e.resolveMeasureDef(t.ColRef); def != nil && def.Expr != nil {
				if !visiting[def] {
					visiting[def] = true
					walkTerms(def.Expr, visit)
					delete(visiting, def)
				}
			} else if t.ColRef.Table != "" {
				add(semantic.StripSingleQuotes(t.ColRef.Table))
			}
		}
		if t.FuncCall != nil {
			// Attribute an iterator's bare-table source to the expression so
			// the table lands in the measure's cluster (see emitIterAgg).
			if tbl := iterBareTable(t.FuncCall); tbl != "" {
				add(tbl)
			}
		}
		return true
	}

	walkTerms(expr, visit)
	return result
}
