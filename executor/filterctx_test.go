package executor_test

import (
	"math"
	"testing"
)

// Seed data recap (see setupTestDB): North = 100+200+50 = 350,
// South = 150+250+75 = 475, grand total = 825.
// Categories: Electronics (Widget+Gadget) = 700, Misc (Doohickey) = 125.

func TestExecute_FilterContextModifiers(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("ALL_grand_total_per_row", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"Total", SUM(sales[amount]),
			"Grand", CALCULATE(SUM(sales[amount]), ALL(sales))
		)`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		for _, row := range rows {
			if v := toFloat(cell(t, row, "Grand")); v != 825 {
				t.Errorf("expected Grand=825 in every row, got %v", v)
			}
		}
	})

	t.Run("percent_of_total", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"Pct", DIVIDE(SUM(sales[amount]), CALCULATE(SUM(sales[amount]), ALL(sales)))
		)`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		sum := 0.0
		for _, row := range rows {
			pct := toFloat(cell(t, row, "Pct"))
			sum += pct
			var want float64
			switch cell(t, row, "region") {
			case "North":
				want = 350.0 / 825.0
			case "South":
				want = 475.0 / 825.0
			}
			if math.Abs(pct-want) > 1e-9 {
				t.Errorf("region %v: expected pct %v, got %v", cell(t, row, "region"), want, pct)
			}
		}
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("percentages should sum to 1, got %v", sum)
		}
	})

	t.Run("ALL_column_keeps_region_subtotal", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			sales[product],
			"RegionTotal", CALCULATE(SUM(sales[amount]), ALL(sales[product]))
		)`)
		if len(rows) != 6 {
			t.Fatalf("expected 6 rows, got %d", len(rows))
		}
		for _, row := range rows {
			want := 350.0
			if cell(t, row, "region") == "South" {
				want = 475.0
			}
			if v := toFloat(cell(t, row, "RegionTotal")); v != want {
				t.Errorf("region %v: expected RegionTotal=%v, got %v", cell(t, row, "region"), want, v)
			}
		}
	})

	t.Run("ALLEXCEPT_keeps_region_subtotal", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			sales[product],
			"RegionTotal", CALCULATE(SUM(sales[amount]), ALLEXCEPT(sales, sales[region]))
		)`)
		for _, row := range rows {
			want := 350.0
			if cell(t, row, "region") == "South" {
				want = 475.0
			}
			if v := toFloat(cell(t, row, "RegionTotal")); v != want {
				t.Errorf("region %v: expected RegionTotal=%v, got %v", cell(t, row, "region"), want, v)
			}
		}
	})

	t.Run("predicate_overrides_group_filter", func(t *testing.T) {
		// DAX shorthand semantics: the predicate replaces the region filter,
		// so both rows show the North total.
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"NorthTotal", CALCULATE(SUM(sales[amount]), sales[region] = "North")
		)`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		for _, row := range rows {
			if v := toFloat(cell(t, row, "NorthTotal")); v != 350 {
				t.Errorf("expected NorthTotal=350 in every row, got %v", v)
			}
		}
	})

	t.Run("KEEPFILTERS_intersects_instead_of_overriding", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"NorthOnly", CALCULATE(SUM(sales[amount]), KEEPFILTERS(sales[region] = "North"))
		)`)
		for _, row := range rows {
			v := row["NorthOnly"]
			switch cell(t, row, "region") {
			case "North":
				if toFloat(v) != 350 {
					t.Errorf("North: expected 350, got %v", v)
				}
			case "South":
				if v != nil {
					t.Errorf("South: expected NULL (filters intersect to empty), got %v", v)
				}
			}
		}
	})

	t.Run("FILTER_ALL_replacement_pattern", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"NorthTotal", CALCULATE(SUM(sales[amount]), FILTER(ALL(sales), sales[region] = "North"))
		)`)
		for _, row := range rows {
			if v := toFloat(cell(t, row, "NorthTotal")); v != 350 {
				t.Errorf("expected NorthTotal=350 in every row, got %v", v)
			}
		}
	})

	t.Run("ALL_on_fact_keeps_dimension_filter", func(t *testing.T) {
		// ALL(sales) clears sales filters only; the category context (on
		// products) survives and correlates through the relationship.
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			products[category],
			"Total", SUM(sales[amount]),
			"AllSales", CALCULATE(SUM(sales[amount]), ALL(sales))
		)`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		for _, row := range rows {
			want := 700.0
			if cell(t, row, "category") == "Misc" {
				want = 125.0
			}
			if v := toFloat(cell(t, row, "AllSales")); v != want {
				t.Errorf("category %v: expected AllSales=%v, got %v", cell(t, row, "category"), want, v)
			}
		}
	})

	t.Run("ALL_noargs_clears_everything", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			products[category],
			"Grand", CALCULATE(SUM(sales[amount]), ALL())
		)`)
		for _, row := range rows {
			if v := toFloat(cell(t, row, "Grand")); v != 825 {
				t.Errorf("expected Grand=825 in every row, got %v", v)
			}
		}
	})

	t.Run("ALL_removes_TREATAS_slicer", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			TREATAS({"North"}, sales[region]),
			"Total", SUM(sales[amount]),
			"Grand", CALCULATE(SUM(sales[amount]), ALL(sales))
		)`)
		if len(rows) != 1 {
			t.Fatalf("expected 1 row (North only), got %d", len(rows))
		}
		if v := toFloat(cell(t, rows[0], "Total")); v != 350 {
			t.Errorf("expected Total=350, got %v", v)
		}
		if v := toFloat(cell(t, rows[0], "Grand")); v != 825 {
			t.Errorf("expected Grand=825 (TREATAS cleared by ALL), got %v", v)
		}
	})

	t.Run("standalone_CALCULATE_ALL", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE CALCULATE(SUM(sales[amount]), ALL(sales))`)
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		for _, v := range rows[0] {
			if toFloat(v) != 825 {
				t.Errorf("expected 825, got %v", v)
			}
		}
	})

	t.Run("ALL_column_as_table_expression", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE ALL(sales[region])`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 distinct regions, got %d", len(rows))
		}
	})
}
