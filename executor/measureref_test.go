package executor_test

// End-to-end tests for measure references whose home table is not named by the
// reference itself. A bare [Measure] carries no table qualifier, so the emitted
// FROM clause must be derived by expanding the measure — otherwise DuckDB
// cannot bind the columns the measure body reads. Only running the SQL proves
// this, which is why these live at the executor layer.

import (
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"
)

func TestBareMeasure_HomeTableNotInGroupKeys(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	addMeasure(t, schema, "fact_sales", "SoldQty", "SUM(fact_sales[qty])")

	// Grouped by the products dimension: fact_sales appears nowhere in the
	// query text, only inside the measure body.
	//	pkey 1 → 10 + 5 = 15 (product A), pkey 2 → 20 + 7 = 27 (product B)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(products[pname], "Sold", [SoldQty])`)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(rows), rows)
	}
	for _, want := range []struct {
		pname string
		sold  float64
	}{{"A", 15}, {"B", 27}} {
		var found bool
		for _, r := range rows {
			if r["pname"] == want.pname {
				found = true
				if got := toFloat(cell(t, r, "Sold")); got != want.sold {
					t.Errorf("%s Sold = %v, want %v", want.pname, got, want.sold)
				}
			}
		}
		if !found {
			t.Errorf("no row for product %s in %v", want.pname, rows)
		}
	}
}

// The bare and qualified forms of the same reference must produce the same
// values; the qualified form is what worked before bare references were fixed.
func TestBareMeasure_MatchesQualified(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	addMeasure(t, schema, "fact_sales", "SoldQty", "SUM(fact_sales[qty])")

	_, bare := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(products[pname], "Sold", [SoldQty])`)
	_, qualified := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(products[pname], "Sold", fact_sales[SoldQty])`)
	if len(bare) != len(qualified) {
		t.Fatalf("row counts differ: bare %d, qualified %d", len(bare), len(qualified))
	}
	for i := range bare {
		if toFloat(cell(t, bare[i], "Sold")) != toFloat(cell(t, qualified[i], "Sold")) {
			t.Errorf("row %d differs: bare %v, qualified %v", i, bare[i], qualified[i])
		}
	}
}

// ROW is a group-key-less SUMMARIZECOLUMNS: one row, one column per pair, with
// the measure's home table inferred the same way.
func TestRow_SingleRowResult(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	addMeasure(t, schema, "fact_sales", "SoldQty", "SUM(fact_sales[qty])")
	addMeasure(t, schema, "fact_returns", "ReturnedQty", "SUM(fact_returns[rqty])")

	t.Run("OneMeasure", func(t *testing.T) {
		// Whole-table total: 10 + 20 + 5 + 7 = 42.
		_, rows := run(t, db, schema, `EVALUATE ROW("Sold", [SoldQty])`)
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d: %v", len(rows), rows)
		}
		if got := toFloat(cell(t, rows[0], "Sold")); got != 42 {
			t.Errorf("Sold = %v, want 42", got)
		}
	})

	t.Run("MeasuresOverTwoFacts", func(t *testing.T) {
		// Two facts must not fan out against each other: 42 and 1+2+3+4 = 10.
		_, rows := run(t, db, schema, `EVALUATE ROW("Sold", [SoldQty], "Returned", [ReturnedQty])`)
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d: %v", len(rows), rows)
		}
		if got := toFloat(cell(t, rows[0], "Sold")); got != 42 {
			t.Errorf("Sold = %v, want 42", got)
		}
		if got := toFloat(cell(t, rows[0], "Returned")); got != 10 {
			t.Errorf("Returned = %v, want 10", got)
		}
	})
}
