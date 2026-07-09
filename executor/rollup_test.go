package executor_test

import (
	"testing"

	"github.com/danielwikar/dux/executor"
)

// Fixture recap: North = 350, South = 475, grand total = 825.
// Per region+product: North Widget 100, North Gadget 200, North Doohickey 50,
// South Widget 150, South Gadget 250, South Doohickey 75.

func TestExecute_Rollup(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("grand_total_row", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			ROLLUPADDISSUBTOTAL(sales[region], "IsTotal"),
			"Total", SUM(sales[amount])
		)`)
		if len(rows) != 3 {
			t.Fatalf("expected 3 rows (2 regions + grand total), got %d", len(rows))
		}
		var sawTotal bool
		for _, row := range rows {
			isTotal := row["IsTotal"] == true
			if isTotal {
				sawTotal = true
				if row["region"] != nil {
					t.Errorf("subtotal row should have NULL region, got %v", row["region"])
				}
				if v := toFloat(cell(t, row, "Total")); v != 825 {
					t.Errorf("grand total: expected 825, got %v", v)
				}
			}
		}
		if !sawTotal {
			t.Error("no subtotal row found")
		}
	})

	t.Run("subtotal_within_plain_group", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			ROLLUPADDISSUBTOTAL(sales[product], "ProductTotal"),
			"Total", SUM(sales[amount])
		)`)
		// 6 detail rows + 2 per-region subtotal rows.
		if len(rows) != 8 {
			t.Fatalf("expected 8 rows, got %d", len(rows))
		}
		for _, row := range rows {
			if row["ProductTotal"] != true {
				continue
			}
			if row["product"] != nil {
				t.Errorf("subtotal row should have NULL product, got %v", row["product"])
			}
			want := 350.0
			if cell(t, row, "region") == "South" {
				want = 475.0
			}
			if v := toFloat(cell(t, row, "Total")); v != want {
				t.Errorf("region %v subtotal: expected %v, got %v", row["region"], want, v)
			}
		}
	})

	t.Run("hierarchical_rollup", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			ROLLUPADDISSUBTOTAL(sales[region], "RegionTotal", sales[product], "ProductTotal"),
			"Total", SUM(sales[amount])
		)`)
		// 6 detail + 2 region subtotals + 1 grand total.
		if len(rows) != 9 {
			t.Fatalf("expected 9 rows, got %d", len(rows))
		}
		var grand, regionSubs int
		for _, row := range rows {
			regionTotal := row["RegionTotal"] == true
			productTotal := row["ProductTotal"] == true
			switch {
			case regionTotal && productTotal:
				grand++
				if v := toFloat(cell(t, row, "Total")); v != 825 {
					t.Errorf("grand total: expected 825, got %v", v)
				}
			case productTotal:
				regionSubs++
			case regionTotal:
				t.Error("region rolled up but product not — impossible in hierarchical rollup")
			}
		}
		if grand != 1 || regionSubs != 2 {
			t.Errorf("expected 1 grand total and 2 region subtotals, got %d and %d", grand, regionSubs)
		}
	})

	t.Run("ROLLUPGROUP_single_unit", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			ROLLUPADDISSUBTOTAL(ROLLUPGROUP(sales[region], sales[product]), "IsTotal"),
			"Total", SUM(sales[amount])
		)`)
		// Region+product roll up together: 6 detail + 1 grand total, no
		// intermediate region subtotals.
		if len(rows) != 7 {
			t.Fatalf("expected 7 rows, got %d", len(rows))
		}
	})

	t.Run("ORDER_BY_over_rollup", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			ROLLUPADDISSUBTOTAL(sales[region], "IsTotal"),
			"Total", SUM(sales[amount])
		) ORDER BY [Total] DESC`)
		// Grand total (825) sorts first.
		if v := toFloat(cell(t, rows[0], "Total")); v != 825 {
			t.Errorf("expected grand total first, got %v", v)
		}
	})

	t.Run("bare_ROLLUPGROUP_errors", func(t *testing.T) {
		_, _, err := executor.Execute(db, schema, `EVALUATE SUMMARIZECOLUMNS(
			ROLLUPGROUP(sales[region], sales[product]),
			"Total", SUM(sales[amount])
		)`)
		if err == nil {
			t.Fatal("expected error for bare ROLLUPGROUP")
		}
	})
}
