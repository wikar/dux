package executor_test

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/danielwikar/dux/executor"
	"github.com/danielwikar/dux/semantic"
)

// runFiltered executes a DUX query with external filters attached.
func runFiltered(t *testing.T, db *sql.DB, schema *semantic.Schema, dux string, filters []executor.ExternalFilter) ([]string, []map[string]any) {
	t.Helper()
	cols, rows, err := executor.ExecuteFiltered(db, schema, dux, filters)
	if err != nil {
		t.Fatalf("ExecuteFiltered(%q): %v", dux, err)
	}
	return cols, rows
}

// Seed data recap (see setupTestDB): North = 100+200+50 = 350,
// South = 150+250+75 = 475, grand total = 825.
// Products: Widget 250, Gadget 450, Doohickey 125.
// Categories (products table): Electronics (Widget+Gadget), Misc (Doohickey).

func TestExternalFilters_SummarizeColumns(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("in_single_value", func(t *testing.T) {
		_, rows := runFiltered(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region], "Total", SUM(sales[amount])
		)`, []executor.ExternalFilter{
			{Table: "sales", Column: "region", Op: "in", Values: []any{"North"}},
		})
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		if v := toFloat(cell(t, rows[0], "Total")); v != 350 {
			t.Errorf("expected North total 350, got %v", v)
		}
	})

	t.Run("in_matches_handwritten_treatas", func(t *testing.T) {
		_, want := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[product],
			TREATAS({"North"}, sales[region]),
			"Total", SUM(sales[amount])
		)`)
		_, got := runFiltered(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[product], "Total", SUM(sales[amount])
		)`, []executor.ExternalFilter{
			{Table: "sales", Column: "region", Op: "in", Values: []any{"North"}},
		})
		if len(got) != len(want) {
			t.Fatalf("row count mismatch: handwritten %d, injected %d", len(want), len(got))
		}
		totals := map[string]float64{}
		for _, row := range want {
			totals[cell(t, row, "product").(string)] = toFloat(cell(t, row, "Total"))
		}
		for _, row := range got {
			p := cell(t, row, "product").(string)
			if v := toFloat(cell(t, row, "Total")); v != totals[p] {
				t.Errorf("product %s: handwritten %v, injected %v", p, totals[p], v)
			}
		}
	})

	t.Run("in_on_related_dimension_table", func(t *testing.T) {
		// Filter on products.category flows to sales through the relationship.
		_, rows := runFiltered(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region], "Total", SUM(sales[amount])
		)`, []executor.ExternalFilter{
			{Table: "products", Column: "category", Op: "in", Values: []any{"Electronics"}},
		})
		var total float64
		for _, row := range rows {
			total += toFloat(cell(t, row, "Total"))
		}
		if total != 700 {
			t.Errorf("expected Electronics total 700, got %v", total)
		}
	})

	t.Run("between_numeric", func(t *testing.T) {
		_, rows := runFiltered(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region], "Total", SUM(sales[amount])
		)`, []executor.ExternalFilter{
			{Table: "sales", Column: "amount", Op: "between", From: 100, To: 200},
		})
		// Qualifying rows: 100 (North), 150 (South), 200 (North).
		var total float64
		for _, row := range rows {
			total += toFloat(cell(t, row, "Total"))
		}
		if total != 450 {
			t.Errorf("expected between total 450, got %v", total)
		}
	})

	t.Run("scalar_gt", func(t *testing.T) {
		_, rows := runFiltered(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region], "Total", SUM(sales[amount])
		)`, []executor.ExternalFilter{
			{Table: "sales", Column: "qty", Op: ">", Value: 2},
		})
		// qty > 2: 150 (qty 3), 250 (qty 4) — both South.
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		if v := toFloat(cell(t, rows[0], "Total")); v != 400 {
			t.Errorf("expected total 400, got %v", v)
		}
	})

	t.Run("contains", func(t *testing.T) {
		_, rows := runFiltered(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[product], "Total", SUM(sales[amount])
		)`, []executor.ExternalFilter{
			{Table: "sales", Column: "product", Op: "contains", Value: "get"},
		})
		// "get" matches Widget and Gadget (case-insensitive strpos).
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows (Widget, Gadget), got %d", len(rows))
		}
	})

	t.Run("numeric_values_sent_as_strings_coerce", func(t *testing.T) {
		_, rows := runFiltered(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region], "Total", SUM(sales[amount])
		)`, []executor.ExternalFilter{
			{Table: "sales", Column: "qty", Op: "in", Values: []any{"1", "2"}},
		})
		// qty ∈ {1,2}: 100+200+50+75 = 425.
		var total float64
		for _, row := range rows {
			total += toFloat(cell(t, row, "Total"))
		}
		if total != 425 {
			t.Errorf("expected total 425, got %v", total)
		}
	})

	t.Run("in_tuples_multi_column", func(t *testing.T) {
		// Multi-select of multi-dimensional marks: (North,Widget) and
		// (South,Gadget) — OR-of-tuples, not a cross-product.
		_, rows := runFiltered(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region], "Total", SUM(sales[amount])
		)`, []executor.ExternalFilter{
			{Op: "in_tuples",
				Columns: []executor.FilterColumn{
					{Table: "sales", Column: "region"},
					{Table: "sales", Column: "product"},
				},
				Tuples: [][]any{{"North", "Widget"}, {"South", "Gadget"}}},
		})
		totals := map[string]float64{}
		for _, row := range rows {
			totals[cell(t, row, "region").(string)] = toFloat(cell(t, row, "Total"))
		}
		if totals["North"] != 100 {
			t.Errorf("expected (North,Widget) total 100, got %v", totals["North"])
		}
		if totals["South"] != 250 {
			t.Errorf("expected (South,Gadget) total 250, got %v", totals["South"])
		}
	})

	t.Run("multiple_filters_AND", func(t *testing.T) {
		_, rows := runFiltered(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[product], "Total", SUM(sales[amount])
		)`, []executor.ExternalFilter{
			{Table: "sales", Column: "region", Op: "in", Values: []any{"North"}},
			{Table: "sales", Column: "amount", Op: ">=", Value: 100},
		})
		// North AND amount>=100: Widget 100, Gadget 200.
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
	})

	t.Run("define_measure_query", func(t *testing.T) {
		_, rows := runFiltered(t, db, schema, `
			DEFINE MEASURE sales[Total] = SUM(sales[amount])
			EVALUATE SUMMARIZECOLUMNS(sales[region], "T", sales[Total])
		`, []executor.ExternalFilter{
			{Table: "sales", Column: "region", Op: "in", Values: []any{"South"}},
		})
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		if v := toFloat(cell(t, rows[0], "T")); v != 475 {
			t.Errorf("expected South total 475, got %v", v)
		}
	})

	t.Run("topn_wrapper_injects_inside", func(t *testing.T) {
		_, rows := runFiltered(t, db, schema, `EVALUATE TOPN(1,
			SUMMARIZECOLUMNS(sales[product], "Total", SUM(sales[amount])),
			[Total]
		)`, []executor.ExternalFilter{
			{Table: "sales", Column: "region", Op: "in", Values: []any{"North"}},
		})
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		// North: Widget 100, Gadget 200, Doohickey 50 → top is Gadget.
		if p := cell(t, rows[0], "product"); p != "Gadget" {
			t.Errorf("expected top product Gadget under North filter, got %v", p)
		}
	})

	t.Run("order_by_preserved", func(t *testing.T) {
		_, rows := runFiltered(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[product], "Total", SUM(sales[amount])
		) ORDER BY [Total] DESC`, []executor.ExternalFilter{
			{Table: "sales", Column: "region", Op: "in", Values: []any{"South"}},
		})
		if len(rows) != 3 {
			t.Fatalf("expected 3 rows, got %d", len(rows))
		}
		// South totals: Gadget 250, Widget 150, Doohickey 75.
		if p := cell(t, rows[0], "product"); p != "Gadget" {
			t.Errorf("expected Gadget first, got %v", p)
		}
		if p := cell(t, rows[2], "product"); p != "Doohickey" {
			t.Errorf("expected Doohickey last, got %v", p)
		}
	})
}

func TestExternalFilters_CalculateInteraction(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("ALL_removes_external_filter", func(t *testing.T) {
		// DAX semantics: an inner ALL(sales) clears the outer (external)
		// filter context, exactly as it clears a handwritten TREATAS.
		_, rows := runFiltered(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"Total", SUM(sales[amount]),
			"Grand", CALCULATE(SUM(sales[amount]), ALL(sales))
		)`, []executor.ExternalFilter{
			{Table: "sales", Column: "region", Op: "in", Values: []any{"North"}},
		})
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		if v := toFloat(cell(t, rows[0], "Total")); v != 350 {
			t.Errorf("expected filtered total 350, got %v", v)
		}
		if v := toFloat(cell(t, rows[0], "Grand")); v != 825 {
			t.Errorf("expected ALL() to clear the external filter (825), got %v", v)
		}
	})

	t.Run("percent_of_filtered_total_with_ALL_column", func(t *testing.T) {
		// ALL(sales[product]) clears only the product grouping; the external
		// region filter must survive inside the CALCULATE.
		_, rows := runFiltered(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[product],
			"RegionTotal", CALCULATE(SUM(sales[amount]), ALL(sales[product]))
		)`, []executor.ExternalFilter{
			{Table: "sales", Column: "region", Op: "in", Values: []any{"North"}},
		})
		if len(rows) != 3 {
			t.Fatalf("expected 3 rows, got %d", len(rows))
		}
		for _, row := range rows {
			if v := toFloat(cell(t, row, "RegionTotal")); v != 350 {
				t.Errorf("product %v: expected RegionTotal 350 (external filter kept), got %v",
					cell(t, row, "product"), v)
			}
		}
	})
}

func TestExternalFilters_NonSummarizeShapes(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("bare_table_wrap", func(t *testing.T) {
		_, rows := runFiltered(t, db, schema, `EVALUATE sales`,
			[]executor.ExternalFilter{
				{Table: "sales", Column: "region", Op: "in", Values: []any{"North"}},
				{Table: "sales", Column: "amount", Op: ">", Value: 60},
			})
		// North AND amount>60: 100, 200.
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
	})

	t.Run("filter_query_wrap", func(t *testing.T) {
		_, rows := runFiltered(t, db, schema,
			`EVALUATE FILTER(sales, sales[qty] >= 2)`,
			[]executor.ExternalFilter{
				{Table: "sales", Column: "region", Op: "in", Values: []any{"South"}},
			})
		// South AND qty>=2: 150 (qty 3), 250 (qty 4), 75 (qty 2).
		if len(rows) != 3 {
			t.Fatalf("expected 3 rows, got %d", len(rows))
		}
	})

	t.Run("values_wrap", func(t *testing.T) {
		_, rows := runFiltered(t, db, schema,
			`EVALUATE VALUES(sales[product])`,
			[]executor.ExternalFilter{
				{Table: "sales", Column: "product", Op: "contains", Value: "get"},
			})
		if len(rows) != 2 {
			t.Fatalf("expected 2 distinct products, got %d", len(rows))
		}
	})
}

func TestExternalFilters_Errors(t *testing.T) {
	db, schema := setupTestDB(t)

	expectFilterErr := func(t *testing.T, dux string, filters []executor.ExternalFilter, wantSubstr string) {
		t.Helper()
		_, _, err := executor.ExecuteFiltered(db, schema, dux, filters)
		if err == nil {
			t.Fatalf("expected error containing %q, got success", wantSubstr)
		}
		var qe *executor.QueryError
		if !errors.As(err, &qe) || qe.Stage != "filters" {
			t.Fatalf("expected stage=filters QueryError, got %v", err)
		}
		if !strings.Contains(err.Error(), wantSubstr) {
			t.Errorf("expected error containing %q, got %q", wantSubstr, err.Error())
		}
	}

	q := `EVALUATE SUMMARIZECOLUMNS(sales[region], "Total", SUM(sales[amount]))`

	expectFilterErr(t, q, []executor.ExternalFilter{
		{Table: "nope", Column: "region", Op: "in", Values: []any{"North"}},
	}, "unknown table")

	expectFilterErr(t, q, []executor.ExternalFilter{
		{Table: "sales", Column: "nope", Op: "in", Values: []any{"North"}},
	}, "unknown column")

	expectFilterErr(t, q, []executor.ExternalFilter{
		{Table: "sales", Column: "region", Op: "like", Value: "N%"},
	}, "unsupported op")

	expectFilterErr(t, q, []executor.ExternalFilter{
		{Table: "sales", Column: "region", Op: "in"},
	}, "non-empty values")

	expectFilterErr(t, q, []executor.ExternalFilter{
		{Table: "sales", Column: "amount", Op: ">", Value: "abc"},
	}, "not numeric")

	expectFilterErr(t, q, []executor.ExternalFilter{
		{Table: "sales", Column: "amount", Op: "between", From: 1},
	}, "requires from and to")

	expectFilterErr(t, q, []executor.ExternalFilter{
		{Table: "sales", Column: "amount", Op: "contains", Value: "1"},
	}, "not valid for numeric")

	// in_tuples columns spanning two tables are rejected (the frontend then
	// degrades to per-column "in").
	expectFilterErr(t, q, []executor.ExternalFilter{
		{Op: "in_tuples",
			Columns: []executor.FilterColumn{
				{Table: "sales", Column: "region"},
				{Table: "products", Column: "category"},
			},
			Tuples: [][]any{{"North", "Electronics"}}},
	}, "one table")

	// A bare-table query only accepts filters on its own base table.
	expectFilterErr(t, `EVALUATE sales`, []executor.ExternalFilter{
		{Table: "products", Column: "category", Op: "in", Values: []any{"Misc"}},
	}, "base table")

	// VAR-returning queries have no injectable shape.
	expectFilterErr(t,
		`EVALUATE VAR x = FILTER(sales, sales[qty] > 1) RETURN x`,
		[]executor.ExternalFilter{
			{Table: "sales", Column: "region", Op: "in", Values: []any{"North"}},
		}, "SUMMARIZECOLUMNS")
}
