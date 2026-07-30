package executor_test

import (
	"database/sql"
	"testing"

	"github.com/danielwikar/dux/semantic"
)

func TestIteratorContextTransitionExpandsSnowflakeRows(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE facts (childkey INTEGER)`,
		`INSERT INTO facts VALUES (1), (2)`,
		`CREATE TABLE children (childkey INTEGER, parentkey INTEGER)`,
		`INSERT INTO children VALUES (1, 10), (2, 20)`,
		`CREATE TABLE parents (parentkey INTEGER, label VARCHAR)`,
		`INSERT INTO parents VALUES (10, 'A'), (20, 'B')`,
		`CREATE TABLE sibling (parentkey INTEGER, amount INTEGER)`,
		`INSERT INTO sibling VALUES (10, 100), (20, 200)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := semantic.IntrospectDuckDB(db)
	if err != nil {
		t.Fatal(err)
	}
	schema.Relationships = append(schema.Relationships,
		&semantic.Relationship{FromTable: "facts", FromColumn: "childkey", ToTable: "children", ToColumn: "childkey"},
		&semantic.Relationship{FromTable: "children", FromColumn: "parentkey", ToTable: "parents", ToColumn: "parentkey"},
		&semantic.Relationship{FromTable: "sibling", FromColumn: "parentkey", ToTable: "parents", ToColumn: "parentkey"},
	)
	if err := schema.AddMeasureFromExpr("sibling", "Sibling Amount", "SUM(sibling[amount])"); err != nil {
		t.Fatal(err)
	}
	_, rows := run(t, db, schema, `EVALUATE ADDCOLUMNS(facts, "Amount", [Sibling Amount])`)
	want := map[float64]float64{1: 100, 2: 200}
	for _, row := range rows {
		if got := toFloat(cell(t, row, "Amount")); got != want[toFloat(cell(t, row, "childkey"))] {
			t.Errorf("%v", row)
		}
	}
}

func TestIteratorContextTransitionUsesValuesAndMatchesBlankKeys(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, stmt := range []string{
		`CREATE TABLE duplicate_facts (amount INTEGER)`,
		`INSERT INTO duplicate_facts VALUES (10), (10)`,
		`CREATE TABLE blank_dim (k INTEGER, label VARCHAR)`,
		`INSERT INTO blank_dim VALUES (NULL, 'Blank')`,
		`CREATE TABLE blank_facts (k INTEGER, amount INTEGER)`,
		`INSERT INTO blank_facts VALUES (NULL, 5)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := semantic.IntrospectDuckDB(db)
	if err != nil {
		t.Fatal(err)
	}
	schema.Relationships = append(schema.Relationships,
		&semantic.Relationship{FromTable: "blank_facts", FromColumn: "k", ToTable: "blank_dim", ToColumn: "k"})
	if err := schema.AddMeasureFromExpr("duplicate_facts", "Duplicate Amount", "SUM(duplicate_facts[amount])"); err != nil {
		t.Fatal(err)
	}
	if err := schema.AddMeasureFromExpr("blank_facts", "Blank Amount", "SUM(blank_facts[amount])"); err != nil {
		t.Fatal(err)
	}
	_, duplicateRows := run(t, db, schema, `EVALUATE ROW("Amount", SUMX(duplicate_facts, [Duplicate Amount]))`)
	if got := toFloat(cell(t, duplicateRows[0], "Amount")); got != 40 {
		t.Fatalf("duplicate-row transition should match both identical rows per iteration: got %v", got)
	}
	_, blankRows := run(t, db, schema, `EVALUATE ADDCOLUMNS(blank_dim, "Amount", [Blank Amount])`)
	if got := toFloat(cell(t, blankRows[0], "Amount")); got != 5 {
		t.Fatalf("BLANK relationship key should match null-safely: got %v", got)
	}
}

func contextTransitionFixture(t *testing.T) (*semantic.Schema, func(string) []map[string]any) {
	t.Helper()
	db, schema := setupTestDB(t)
	if err := schema.AddMeasureFromExpr("sales", "Sales Amount", "SUM(sales[amount])"); err != nil {
		t.Fatal(err)
	}
	if err := schema.AddMeasureFromExpr("sales", "Nested Sales Amount", "[Sales Amount]"); err != nil {
		t.Fatal(err)
	}
	if err := schema.AddMeasureFromExpr("sales", "All Product Sales", "CALCULATE([Sales Amount], ALL(products))"); err != nil {
		t.Fatal(err)
	}
	if err := schema.AddMeasureFromExpr("sales", "Regional Rows", `COUNTROWS(
		SUMMARIZECOLUMNS(sales[region], "Amount", SUM(sales[amount])))`); err != nil {
		t.Fatal(err)
	}
	return schema, func(query string) []map[string]any {
		_, rows := run(t, db, schema, query)
		return rows
	}
}

func TestIteratorContextTransition(t *testing.T) {
	_, query := contextTransitionFixture(t)

	t.Run("measure_transitions_dimension_row", func(t *testing.T) {
		rows := query(`EVALUATE ADDCOLUMNS(products, "Sales", [Sales Amount])`)
		want := map[string]float64{"Widget": 250, "Gadget": 450, "Doohickey": 125}
		for _, row := range rows {
			if got := toFloat(cell(t, row, "Sales")); got != want[cell(t, row, "product").(string)] {
				t.Errorf("%v: got %v", row, got)
			}
		}
	})

	t.Run("nested_measure_does_not_transition_twice", func(t *testing.T) {
		rows := query(`EVALUATE ADDCOLUMNS(products, "Sales", [Nested Sales Amount])`)
		want := map[string]float64{"Widget": 250, "Gadget": 450, "Doohickey": 125}
		for _, row := range rows {
			if got := toFloat(cell(t, row, "Sales")); got != want[cell(t, row, "product").(string)] {
				t.Errorf("%v: got %v", row, got)
			}
		}
	})

	t.Run("measure_and_naked_aggregate_differ", func(t *testing.T) {
		rows := query(`EVALUATE ROW(
			"Transitioned", SUMX(products, [Sales Amount]),
			"Untransitioned", SUMX(products, SUM(sales[amount])))`)
		if got := toFloat(cell(t, rows[0], "Transitioned")); got != 825 {
			t.Fatalf("transitioned: got %v", got)
		}
		if got := toFloat(cell(t, rows[0], "Untransitioned")); got != 2475 {
			t.Fatalf("untransitioned: got %v", got)
		}
	})

	t.Run("explicit_calculate_transitions", func(t *testing.T) {
		rows := query(`EVALUATE ADDCOLUMNS(products, "Sales", CALCULATE(SUM(sales[amount])))`)
		want := map[string]float64{"Widget": 250, "Gadget": 450, "Doohickey": 125}
		for _, row := range rows {
			if got := toFloat(cell(t, row, "Sales")); got != want[cell(t, row, "product").(string)] {
				t.Errorf("%v: got %v", row, got)
			}
		}
	})

	t.Run("all_removes_transitioned_table", func(t *testing.T) {
		rows := query(`EVALUATE ADDCOLUMNS(products, "Sales", CALCULATE(SUM(sales[amount]), ALL(products)))`)
		for _, row := range rows {
			if got := toFloat(cell(t, row, "Sales")); got != 825 {
				t.Errorf("%v: got %v", row, got)
			}
		}
	})

	t.Run("nested_measure_does_not_reintroduce_removed_transition", func(t *testing.T) {
		rows := query(`EVALUATE ADDCOLUMNS(products, "Sales", [All Product Sales])`)
		for _, row := range rows {
			if got := toFloat(cell(t, row, "Sales")); got != 825 {
				t.Errorf("%v: got %v", row, got)
			}
		}
	})

	t.Run("calculatetable_uses_the_same_transition", func(t *testing.T) {
		rows := query(`EVALUATE ADDCOLUMNS(products, "Rows", COUNTROWS(CALCULATETABLE(sales)))`)
		for _, row := range rows {
			if got := toFloat(cell(t, row, "Rows")); got != 2 {
				t.Errorf("%v: got %v", row, got)
			}
		}
	})

	t.Run("calculatetable_applies_filters_after_transition", func(t *testing.T) {
		rows := query(`EVALUATE ADDCOLUMNS(products,
			"Large", COUNTROWS(CALCULATETABLE(sales, sales[amount] > 100)),
			"All", COUNTROWS(CALCULATETABLE(sales, ALL(products))))`)
		want := map[string]float64{"Widget": 1, "Gadget": 2, "Doohickey": 0}
		for _, row := range rows {
			product := cell(t, row, "product").(string)
			if got := toFloat(cell(t, row, "Large")); got != want[product] {
				t.Errorf("%s Large = %v", product, got)
			}
			if got := toFloat(cell(t, row, "All")); got != 6 {
				t.Errorf("%s All = %v", product, got)
			}
		}
	})

	t.Run("relatedtable_uses_contextual_table_evaluation", func(t *testing.T) {
		rows := query(`EVALUATE ADDCOLUMNS(products, "Rows", COUNTROWS(RELATEDTABLE(sales)))`)
		for _, row := range rows {
			if got := toFloat(cell(t, row, "Rows")); got != 2 {
				t.Errorf("%v: got %v", row, got)
			}
		}
	})

	t.Run("renamed_projection_keeps_lineage", func(t *testing.T) {
		rows := query(`EVALUATE ROW("Sales", SUMX(
			SELECTCOLUMNS(products, "P", products[product]),
			[Sales Amount]))`)
		if got := toFloat(cell(t, rows[0], "Sales")); got != 825 {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("table_var_keeps_lineage", func(t *testing.T) {
		rows := query(`EVALUATE
			VAR ps = SELECTCOLUMNS(products, "P", products[product])
			RETURN ROW("Sales", SUMX(ps, [Sales Amount]))`)
		if got := toFloat(cell(t, rows[0], "Sales")); got != 825 {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("nested_iterators_transition_all_active_rows", func(t *testing.T) {
		rows := query(`EVALUATE ROW("Sales", SUMX(products,
			SUMX(FILTER(sales, sales[product] = products[product]), [Sales Amount])))`)
		if got := toFloat(cell(t, rows[0], "Sales")); got != 825 {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("nested_frames_over_same_table_intersect", func(t *testing.T) {
		rows := query(`EVALUATE ROW("Sales", SUMX(VALUES(products[category]),
			SUMX(VALUES(products[product]), [Sales Amount])))`)
		if got := toFloat(cell(t, rows[0], "Sales")); got != 825 {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("summarizecolumns_runs_inside_transitioned_measure", func(t *testing.T) {
		rows := query(`EVALUATE ADDCOLUMNS(products, "Regions", [Regional Rows])`)
		for _, row := range rows {
			if got := toFloat(cell(t, row, "Regions")); got != 2 {
				t.Errorf("%v", row)
			}
		}
	})

	t.Run("ordinary_filter_replaces_and_keepfilters_intersects", func(t *testing.T) {
		rows := query(`EVALUATE ADDCOLUMNS(products,
			"Replace", CALCULATE([Sales Amount], products[product] = "Widget"),
			"Keep", CALCULATE([Sales Amount], KEEPFILTERS(products[product] = "Widget")))`)
		for _, row := range rows {
			product := cell(t, row, "product").(string)
			switch product {
			case "Widget":
				if toFloat(cell(t, row, "Replace")) != 250 || toFloat(cell(t, row, "Keep")) != 250 {
					t.Errorf("%v", row)
				}
			case "Gadget":
				if toFloat(cell(t, row, "Replace")) != 250 || cell(t, row, "Keep") != nil {
					t.Errorf("%v", row)
				}
			case "Doohickey":
				if cell(t, row, "Replace") != nil || cell(t, row, "Keep") != nil {
					t.Errorf("%v", row)
				}
			}
		}
	})

	t.Run("filter_and_topn_evaluate_measures_per_row", func(t *testing.T) {
		filtered := query(`EVALUATE FILTER(products, [Sales Amount] > 200)`)
		if len(filtered) != 2 {
			t.Fatalf("FILTER returned %d rows: %v", len(filtered), filtered)
		}
		top := query(`EVALUATE TOPN(1, products, [Sales Amount])`)
		if len(top) != 1 || cell(t, top[0], "product") != "Gadget" {
			t.Fatalf("TOPN = %v", top)
		}
	})
}
