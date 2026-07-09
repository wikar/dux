package emitter

// Internal unit tests for measure clustering (cluster.go).

import (
	"testing"

	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// scArgs parses a SUMMARIZECOLUMNS query and returns its argument list.
func scArgs(t *testing.T, dux string) []*parser.Expr {
	t.Helper()
	q, err := parser.Parse(dux)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fc := q.Evaluate.Table.Func
	if fc == nil {
		t.Fatalf("query is not a function call")
	}
	return fc.Args
}

func clusterSchema() *semantic.Schema {
	s := semantic.NewSchema()
	for _, name := range []string{"dates", "fact_sales", "fact_returns", "bev.Orders"} {
		s.Tables[name] = &semantic.Table{Name: name, Columns: map[string]*semantic.Column{}}
	}
	return s
}

func TestClusterMeasures(t *testing.T) {
	t.Run("SingleFactOneCluster", func(t *testing.T) {
		// Two measures over the same fact must share one cluster.
		args := scArgs(t, `EVALUATE SUMMARIZECOLUMNS(dates[year],
			"A", SUM(fact_sales[qty]), "B", COUNT(fact_sales[qty]))`)
		e := &Emitter{Schema: clusterSchema()}
		clusters := e.planMeasures(args[1:], nil).clusters
		if len(clusters) != 1 || tableClusterCount(clusters) != 1 {
			t.Fatalf("expected 1 cluster, got %d (%d with tables)", len(clusters), tableClusterCount(clusters))
		}
		if got := clusters[0].pairIdx; len(got) != 2 || got[0] != 0 || got[1] != 2 {
			t.Errorf("pairIdx = %v, want [0 2]", got)
		}
	})

	t.Run("TwoFactsTwoClusters", func(t *testing.T) {
		args := scArgs(t, `EVALUATE SUMMARIZECOLUMNS(dates[year],
			"A", SUM(fact_sales[qty]), "B", SUM(fact_returns[rqty]))`)
		e := &Emitter{Schema: clusterSchema()}
		clusters := e.planMeasures(args[1:], nil).clusters
		if tableClusterCount(clusters) != 2 {
			t.Fatalf("expected 2 table clusters, got %d", tableClusterCount(clusters))
		}
	})

	t.Run("QualifiedAndBareNamesMerge", func(t *testing.T) {
		// "Orders" resolves to schema key "bev.Orders" -- one cluster, not two.
		args := scArgs(t, `EVALUATE SUMMARIZECOLUMNS(dates[year],
			"A", SUM(bev.Orders[amount]), "B", COUNT(Orders[id]))`)
		e := &Emitter{Schema: clusterSchema()}
		clusters := e.planMeasures(args[1:], nil).clusters
		if tableClusterCount(clusters) != 1 {
			t.Fatalf("expected 1 table cluster, got %d", tableClusterCount(clusters))
		}
	})

	t.Run("ScalarOnlyMeasureSortsLast", func(t *testing.T) {
		args := scArgs(t, `EVALUATE SUMMARIZECOLUMNS(dates[year],
			"Two", 1 + 1, "A", SUM(fact_sales[qty]))`)
		e := &Emitter{Schema: clusterSchema()}
		clusters := e.planMeasures(args[1:], nil).clusters
		if len(clusters) != 2 || tableClusterCount(clusters) != 1 {
			t.Fatalf("expected 2 clusters (1 with tables), got %d (%d)", len(clusters), tableClusterCount(clusters))
		}
		if clusters[0].key == "" || clusters[1].key != "" {
			t.Errorf("scalar cluster must sort last: keys %q, %q", clusters[0].key, clusters[1].key)
		}
	})

	t.Run("StoredMeasureExpandsToItsTables", func(t *testing.T) {
		// A measure stored on fact_sales referencing fact_returns clusters by
		// its EXPRESSION's tables, not its host table.
		defs, err := parser.ParseMeasures("DEFINE\n    MEASURE fact_sales[NetQty] = SUM(fact_returns[rqty])")
		if err != nil {
			t.Fatalf("parse measures: %v", err)
		}
		s := clusterSchema()
		s.Measures["fact_sales"] = map[string]*parser.MeasureDefinition{"NetQty": defs[0]}
		args := scArgs(t, `EVALUATE SUMMARIZECOLUMNS(dates[year],
			"A", fact_sales[NetQty], "B", SUM(fact_returns[rqty]))`)
		e := &Emitter{Schema: s}
		clusters := e.planMeasures(args[1:], nil).clusters
		if tableClusterCount(clusters) != 1 {
			t.Fatalf("expected the stored measure to share fact_returns' cluster, got %d clusters", tableClusterCount(clusters))
		}
	})

	t.Run("MultiTableMeasureClustersBySet", func(t *testing.T) {
		// A measure spanning fact+dimension is one cluster; the same fact alone
		// is a different set, hence a different cluster key.
		args := scArgs(t, `EVALUATE SUMMARIZECOLUMNS(
			"A", SUM(fact_sales[qty]),
			"B", SUMX(fact_sales, fact_sales[qty] * dates[year]))`)
		e := &Emitter{Schema: clusterSchema()}
		clusters := e.planMeasures(args, nil).clusters
		if tableClusterCount(clusters) != 2 {
			t.Fatalf("expected 2 table clusters (different table sets), got %d", tableClusterCount(clusters))
		}
	})

	t.Run("CrossClusterExpressionSplits", func(t *testing.T) {
		// SUM(a)/SUM(b) across facts: the pair is split, each aggregate lifted
		// into its own cluster, and the plan reports it as a split pair.
		args := scArgs(t, `EVALUATE SUMMARIZECOLUMNS(dates[year],
			"Ratio", SUM(fact_sales[qty]) / SUM(fact_returns[rqty]))`)
		e := &Emitter{Schema: clusterSchema()}
		plan := e.planMeasures(args[1:], nil)
		if tableClusterCount(plan.clusters) != 2 {
			t.Fatalf("expected 2 table clusters, got %d", tableClusterCount(plan.clusters))
		}
		if len(plan.splitPairs) != 1 || plan.splitPairs[0] != 0 {
			t.Fatalf("splitPairs = %v, want [0]", plan.splitPairs)
		}
		lifted := 0
		for _, c := range plan.clusters {
			lifted += len(c.lifted)
			if len(c.pairIdx) != 0 {
				t.Errorf("split pair must not be whole-assigned, cluster %q has pairIdx %v", c.key, c.pairIdx)
			}
		}
		if lifted != 2 {
			t.Errorf("expected 2 lifted aggregates, got %d", lifted)
		}
	})

	t.Run("SameClusterArithmeticStaysWhole", func(t *testing.T) {
		// SUM(a)+COUNT(a) over one fact stays a whole-pair assignment.
		args := scArgs(t, `EVALUATE SUMMARIZECOLUMNS(dates[year],
			"Both", SUM(fact_sales[qty]) + COUNT(fact_sales[qty]))`)
		e := &Emitter{Schema: clusterSchema()}
		plan := e.planMeasures(args[1:], nil)
		if len(plan.splitPairs) != 0 {
			t.Fatalf("same-cluster arithmetic must not split, got splitPairs %v", plan.splitPairs)
		}
		if tableClusterCount(plan.clusters) != 1 {
			t.Fatalf("expected 1 cluster, got %d", tableClusterCount(plan.clusters))
		}
	})
}
