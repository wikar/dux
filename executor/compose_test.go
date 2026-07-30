package executor_test

import "testing"

// Tests for composable table expressions, EVALUATE ... ORDER BY ... START AT,
// and CROSSJOIN / GENERATE / GENERATEALL. Uses the sales/products fixture:
// North = 350 (Widget 100, Gadget 200, Doohickey 50),
// South = 475 (Widget 150, Gadget 250, Doohickey 75).

func TestExecute_ComposableTables(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("FILTER_over_SUMMARIZECOLUMNS", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE FILTER(
			SUMMARIZECOLUMNS(sales[region], "Total", SUM(sales[amount])),
			[Total] > 400
		)`)
		if len(rows) != 1 {
			t.Fatalf("expected 1 row (South only), got %d", len(rows))
		}
		if v := cell(t, rows[0], "region"); v != "South" {
			t.Errorf("expected South, got %v", v)
		}
	})

	t.Run("TOPN_over_SUMMARIZECOLUMNS", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE TOPN(
			2,
			SUMMARIZECOLUMNS(sales[product], "Total", SUM(sales[amount])),
			[Total]
		)`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		// Gadget (450) and Widget (250) beat Doohickey (125).
		for _, row := range rows {
			if p := cell(t, row, "product"); p == "Doohickey" {
				t.Errorf("Doohickey should not be in the top 2")
			}
		}
	})

	t.Run("ADDCOLUMNS_over_FILTER", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE ADDCOLUMNS(
			FILTER(sales, sales[region] = "North"),
			"Double", sales[amount] * 2
		)`)
		if len(rows) != 3 {
			t.Fatalf("expected 3 rows, got %d", len(rows))
		}
		for _, row := range rows {
			if toFloat(cell(t, row, "Double")) != 2*toFloat(cell(t, row, "amount")) {
				t.Errorf("Double should be 2×amount")
			}
		}
	})

	t.Run("SUMX_over_FILTER", func(t *testing.T) {
		_, rows := run(t, db, schema,
			`EVALUATE SUMMARIZECOLUMNS("QtyRev", SUMX(FILTER(sales, sales[region] = "North"), sales[amount] * sales[qty]))`)
		// North: 100*2 + 200*1 + 50*1 = 450
		if v := toFloat(cell(t, rows[0], "QtyRev")); v != 450 {
			t.Errorf("expected 450, got %v", v)
		}
	})

	t.Run("COUNTROWS_of_nested_TOPN", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			"N", COUNTROWS(TOPN(4, sales, sales[amount]))
		)`)
		if v := toFloat(cell(t, rows[0], "N")); v != 4 {
			t.Errorf("expected 4, got %v", v)
		}
	})
}

func TestExecute_OrderBy(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("ORDER_BY_measure_desc", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE
			SUMMARIZECOLUMNS(sales[product], "Total", SUM(sales[amount]))
			ORDER BY [Total] DESC`)
		if len(rows) != 3 {
			t.Fatalf("expected 3 rows, got %d", len(rows))
		}
		want := []string{"Gadget", "Widget", "Doohickey"}
		for i, row := range rows {
			if v := cell(t, row, "product"); v != want[i] {
				t.Errorf("row %d: expected %s, got %v", i, want[i], v)
			}
		}
	})

	t.Run("ORDER_BY_column_asc_default", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE
			SUMMARIZECOLUMNS(sales[region], "Total", SUM(sales[amount]))
			ORDER BY sales[region]`)
		if cell(t, rows[0], "region") != "North" || cell(t, rows[1], "region") != "South" {
			t.Errorf("expected North, South order")
		}
	})

	t.Run("ORDER_BY_multiple_keys", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE sales ORDER BY sales[region], sales[amount] DESC`)
		if len(rows) != 6 {
			t.Fatalf("expected 6 rows, got %d", len(rows))
		}
		// First row: North with its highest amount (200).
		if cell(t, rows[0], "region") != "North" || toFloat(cell(t, rows[0], "amount")) != 200 {
			t.Errorf("expected North/200 first, got %v/%v", rows[0]["region"], rows[0]["amount"])
		}
	})

	t.Run("START_AT", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE
			SUMMARIZECOLUMNS(sales[product], "Total", SUM(sales[amount]))
			ORDER BY sales[product]
			START AT "Gadget"`)
		// Alphabetical from Gadget: Gadget, Widget (Doohickey excluded).
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		if cell(t, rows[0], "product") != "Gadget" {
			t.Errorf("expected Gadget first, got %v", rows[0]["product"])
		}
	})
}

func TestExecute_JoinCombinators(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("CROSSJOIN", func(t *testing.T) {
		_, rows := run(t, db, schema,
			`EVALUATE CROSSJOIN(VALUES(sales[region]), VALUES(products[category]))`)
		// 2 regions × 2 categories = 4 rows.
		if len(rows) != 4 {
			t.Fatalf("expected 4 rows, got %d", len(rows))
		}
	})

	t.Run("GENERATE_top1_per_region", func(t *testing.T) {
		// For each region, its single largest sale.
		_, rows := run(t, db, schema, `EVALUATE GENERATE(
			VALUES(sales[region]),
			SELECTCOLUMNS(
				TOPN(1, CALCULATETABLE(sales), sales[amount]),
				"amount", sales[amount])
		)`)
		if len(rows) < 2 {
			t.Fatalf("expected at least 2 rows, got %d", len(rows))
		}
	})

	t.Run("GENERATEALL_keeps_unmatched", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE GENERATEALL(
			VALUES(products[category]),
			FILTER(sales, sales[amount] > 10000)
		)`)
		// No sale exceeds 10000 → every category row survives with NULLs.
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		for _, row := range rows {
			if row["amount"] != nil {
				t.Errorf("expected NULL amount, got %v", row["amount"])
			}
		}
	})
}
