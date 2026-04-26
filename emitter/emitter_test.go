package emitter_test

import (
	"strings"
	"testing"

	"github.com/danielwikar/dux/emitter"
	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func mustParse(t *testing.T, input string) *parser.Query {
	t.Helper()
	q, err := parser.Parse(input)
	if err != nil {
		t.Fatalf("parse %q: %v", input, err)
	}
	return q
}

// minSchema builds a tiny in-memory schema sufficient for emit-level tests.
// It contains two tables: "sales" and "products", linked by the "product" column.
func minSchema() *semantic.Schema {
	s := semantic.NewSchema()
	s.Tables["sales"] = &semantic.Table{
		Name:     "sales",
		Database: "",
		Columns: map[string]*semantic.Column{
			"id":      {Name: "id", DataType: "INTEGER"},
			"product": {Name: "product", DataType: "TEXT"},
			"amount":  {Name: "amount", DataType: "DOUBLE"},
			"qty":     {Name: "qty", DataType: "INTEGER"},
			"region":  {Name: "region", DataType: "TEXT"},
		},
	}
	s.Tables["products"] = &semantic.Table{
		Name:     "products",
		Database: "",
		Columns: map[string]*semantic.Column{
			"product":  {Name: "product", DataType: "TEXT"},
			"category": {Name: "category", DataType: "TEXT"},
		},
	}
	s.Relationships = append(s.Relationships, &semantic.Relationship{
		FromTable:  "sales",
		FromColumn: "product",
		ToTable:    "products",
		ToColumn:   "product",
	})
	return s
}

func emit(t *testing.T, dux string) string {
	t.Helper()
	q := mustParse(t, dux)
	em := &emitter.Emitter{Schema: minSchema()}
	sql, err := em.Emit(q)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return sql
}

func assertContains(t *testing.T, sql string, fragments ...string) {
	t.Helper()
	for _, f := range fragments {
		if !strings.Contains(sql, f) {
			t.Errorf("expected SQL to contain %q\ngot:\n%s", f, sql)
		}
	}
}

func assertNotContains(t *testing.T, sql string, fragments ...string) {
	t.Helper()
	for _, f := range fragments {
		if strings.Contains(sql, f) {
			t.Errorf("expected SQL NOT to contain %q\ngot:\n%s", f, sql)
		}
	}
}

// ─── Aggregation ─────────────────────────────────────────────────────────────

func TestAggregation(t *testing.T) {
	tests := []struct {
		name  string
		dux   string
		wants []string
	}{
		{
			"SUM",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Total", SUM(sales[amount]))`,
			[]string{"SUM(", "amount", "GROUP BY"},
		},
		{
			"AVERAGE",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Avg", AVERAGE(sales[amount]))`,
			[]string{"AVG(", "amount"},
		},
		{
			"COUNT",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "N", COUNT(sales[id]))`,
			[]string{"COUNT(", "id"},
		},
		{
			"COUNTA",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "N", COUNTA(sales[id]))`,
			[]string{"COUNT(", "id"},
		},
		{
			"COUNTBLANK",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Blanks", COUNTBLANK(sales[amount]))`,
			[]string{"COUNT(*) - COUNT(", "amount"},
		},
		{
			"MIN",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Min", MIN(sales[amount]))`,
			[]string{"MIN(", "amount"},
		},
		{
			"MAX",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Max", MAX(sales[amount]))`,
			[]string{"MAX(", "amount"},
		},
		{
			"MEDIAN",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Med", MEDIAN(sales[amount]))`,
			[]string{"MEDIAN(", "amount"},
		},
		{
			"COUNTROWS",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Rows", COUNTROWS(sales))`,
			[]string{"COUNT(*)"},
		},
		{
			"DISTINCTCOUNT",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "DC", DISTINCTCOUNT(sales[product]))`,
			[]string{"COUNT(DISTINCT", "product"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql := emit(t, tt.dux)
			assertContains(t, sql, tt.wants...)
		})
	}
}

// ─── Iterator (X) functions ───────────────────────────────────────────────────

func TestIterators(t *testing.T) {
	tests := []struct {
		name  string
		dux   string
		wants []string
	}{
		{
			"SUMX",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Rev", SUMX(sales, sales[amount] * sales[qty]))`,
			[]string{"SELECT SUM(", "FROM sales AS __row_sales"},
		},
		{
			"AVERAGEX",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Avg", AVERAGEX(sales, sales[amount]))`,
			[]string{"SELECT AVG(", "FROM sales AS __row_sales"},
		},
		{
			"COUNTX",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "N", COUNTX(sales, sales[id]))`,
			[]string{"SELECT COUNT(", "FROM sales AS __row_sales"},
		},
		{
			"MINX",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Min", MINX(sales, sales[amount]))`,
			[]string{"SELECT MIN(", "FROM sales AS __row_sales"},
		},
		{
			"MAXX",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Max", MAXX(sales, sales[amount]))`,
			[]string{"SELECT MAX(", "FROM sales AS __row_sales"},
		},
		{
			"CONCATENATEX",
			`EVALUATE CONCATENATEX(sales, sales[region], ", ")`,
			[]string{"string_agg(", "FROM sales AS __row_sales"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql := emit(t, tt.dux)
			assertContains(t, sql, tt.wants...)
		})
	}
}

// ─── Table operations ─────────────────────────────────────────────────────────

func TestTableOps(t *testing.T) {
	tests := []struct {
		name  string
		dux   string
		wants []string
	}{
		{
			"SUMMARIZECOLUMNS",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Total", SUM(sales[amount]))`,
			[]string{"SELECT", "GROUP BY"},
		},
		{
			"SUMMARIZECOLUMNS_MultiTable",
			`EVALUATE SUMMARIZECOLUMNS(products[category], "Total", SUM(sales[amount]))`,
			[]string{"SELECT", "LEFT JOIN", "GROUP BY"},
		},
		{
			"ADDCOLUMNS",
			`EVALUATE ADDCOLUMNS(sales, "Double", sales[amount] * 2)`,
			[]string{"SELECT *", "FROM sales"},
		},
		{
			"SELECTCOLUMNS",
			`EVALUATE SELECTCOLUMNS(sales, "Region", sales[region], "Amount", sales[amount])`,
			[]string{"SELECT", "AS 'Region'", "AS 'Amount'", "FROM sales"},
		},
		{
			"FILTER",
			`EVALUATE FILTER(sales, sales[region] = "North")`,
			[]string{"SELECT * FROM sales WHERE", "'North'"},
		},
		{
			"TOPN",
			`EVALUATE TOPN(5, sales, sales[amount])`,
			[]string{"SELECT * FROM sales ORDER BY", "DESC LIMIT 5"},
		},
		{
			"UNION",
			`EVALUATE UNION(FILTER(sales, sales[region] = "North"), FILTER(sales, sales[region] = "South"))`,
			[]string{"UNION ALL"},
		},
		{
			"INTERSECT",
			`EVALUATE INTERSECT(FILTER(sales, sales[region] = "North"), FILTER(sales, sales[region] = "South"))`,
			[]string{"INTERSECT"},
		},
		{
			"EXCEPT",
			`EVALUATE EXCEPT(FILTER(sales, sales[region] = "North"), FILTER(sales, sales[region] = "South"))`,
			[]string{"EXCEPT"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql := emit(t, tt.dux)
			assertContains(t, sql, tt.wants...)
		})
	}
}

// ─── Filter context ───────────────────────────────────────────────────────────

func TestFilterContext(t *testing.T) {
	t.Run("TREATAS_Literal", func(t *testing.T) {
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			TREATAS({"North", "South"}, sales[region]),
			"Total", SUM(sales[amount])
		)`)
		assertContains(t, sql, "WHERE", "IN (", "'North'", "'South'")
		assertNotContains(t, sql, "GROUP BY sales[region]")
	})

	t.Run("TREATAS_VALUES", func(t *testing.T) {
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			TREATAS(VALUES(products[product]), sales[product]),
			"Total", SUM(sales[amount])
		)`)
		assertContains(t, sql, "WHERE", "IN (SELECT DISTINCT", "FROM products")
	})

	t.Run("CALCULATE_standalone", func(t *testing.T) {
		sql := emit(t, `EVALUATE CALCULATE(SUM(sales[amount]), TREATAS({"North"}, sales[region]))`)
		assertContains(t, sql, "(SELECT", "SUM(", "WHERE", "IN (", "'North'")
	})

	t.Run("CALCULATE_in_SUMMARIZECOLUMNS", func(t *testing.T) {
		// Inside SUMMARIZECOLUMNS, CALCULATE should use FILTER (WHERE ...) syntax
		// so the aggregate respects the outer GROUP BY.
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"Filtered", CALCULATE(SUM(sales[amount]), sales[qty] > 2)
		)`)
		assertContains(t, sql, "FILTER (WHERE", "GROUP BY")
		// Must NOT produce a scalar subquery.
		assertNotContains(t, sql, "(SELECT SUM(")
	})

	t.Run("VALUES", func(t *testing.T) {
		sql := emit(t, `EVALUATE VALUES(sales[region])`)
		assertContains(t, sql, "SELECT DISTINCT", "FROM sales")
	})

	t.Run("DISTINCT", func(t *testing.T) {
		sql := emit(t, `EVALUATE DISTINCT(sales[region])`)
		assertContains(t, sql, "SELECT DISTINCT", "FROM sales")
	})
}

// ─── Scalar / logical ─────────────────────────────────────────────────────────

func TestScalarLogical(t *testing.T) {
	tests := []struct {
		name  string
		dux   string
		wants []string
	}{
		{
			"DIVIDE",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Ratio", DIVIDE(SUM(sales[amount]), COUNT(sales[id])))`,
			[]string{"CASE WHEN", "= 0 THEN NULL ELSE", "END"},
		},
		{
			"IF_two_args",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Label", IF(SUM(sales[amount]) > 100, "High"))`,
			[]string{"CASE WHEN", "THEN"},
		},
		{
			"IF_three_args",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Label", IF(SUM(sales[amount]) > 100, "High", "Low"))`,
			[]string{"CASE WHEN", "THEN", "ELSE"},
		},
		{
			"SWITCH",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Cat", SWITCH(sales[region], "North", "N", "South", "S", "Other"))`,
			[]string{"CASE", "WHEN", "THEN", "ELSE"},
		},
		{
			"ISBLANK",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Blank", ISBLANK(sales[amount]))`,
			[]string{"IS NULL"},
		},
		{
			"BLANK",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Blank", BLANK())`,
			[]string{"NULL"},
		},
		{
			"NOT",
			`EVALUATE FILTER(sales, NOT(sales[region] = "North"))`,
			[]string{"NOT (", "'North'"},
		},
		{
			"AND",
			`EVALUATE FILTER(sales, AND(sales[region] = "North", sales[amount] > 100))`,
			[]string{"AND"},
		},
		{
			"OR",
			`EVALUATE FILTER(sales, OR(sales[region] = "North", sales[region] = "South"))`,
			[]string{"OR"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sql := emit(t, tt.dux)
			assertContains(t, sql, tt.wants...)
		})
	}
}

// ─── Special cases ────────────────────────────────────────────────────────────

func TestSpecial(t *testing.T) {
	t.Run("SUMMARIZECOLUMNS_TREATAS_WHERE", func(t *testing.T) {
		// TREATAS in the group-by position should become a WHERE clause, not a SELECT column.
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			TREATAS({"North"}, sales[region]),
			"Total", SUM(sales[amount])
		)`)
		assertContains(t, sql, "WHERE", "IN (", "'North'")
		// The TREATAS predicate must NOT appear in GROUP BY or SELECT as a column.
		assertNotContains(t, sql, "GROUP BY region IN")
	})

	t.Run("BareTableRef", func(t *testing.T) {
		sql := emit(t, `EVALUATE sales`)
		assertContains(t, sql, "SELECT * FROM sales")
	})

	t.Run("QualifiedTableRef", func(t *testing.T) {
		q := mustParse(t, `EVALUATE atp.matches`)
		em := &emitter.Emitter{}
		sql, err := em.Emit(q)
		if err != nil {
			t.Fatalf("emit: %v", err)
		}
		assertContains(t, sql, "SELECT * FROM")
	})

	t.Run("Passthrough_function", func(t *testing.T) {
		// Functions not explicitly handled should pass through to DuckDB.
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(sales[region], "X", ABS(SUM(sales[amount])))`)
		assertContains(t, sql, "ABS(")
	})

	t.Run("Infix_AND_keyword", func(t *testing.T) {
		sql := emit(t, `EVALUATE FILTER(sales, sales[region] = "North" AND sales[amount] > 100)`)
		assertContains(t, sql, "WHERE", "AND")
	})

	t.Run("Infix_OR_keyword", func(t *testing.T) {
		sql := emit(t, `EVALUATE FILTER(sales, sales[region] = "North" OR sales[region] = "South")`)
		assertContains(t, sql, "WHERE", "OR")
	})
}

// ─── Error cases ──────────────────────────────────────────────────────────────

func TestEmitErrors(t *testing.T) {
	tests := []struct {
		name    string
		dux     string
		wantErr string
	}{
		{
			"ALL_not_implemented",
			`EVALUATE ALL(sales[region])`,
			"not yet implemented",
		},
		{
			"TREATAS_wrong_arg_count",
			`EVALUATE TREATAS({"x"})`,
			"TREATAS requires exactly 2 arguments",
		},
		{
			"DIVIDE_too_few_args",
			`EVALUATE DIVIDE(sales[amount])`,
			"DIVIDE requires at least 2 arguments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := parser.Parse(tt.dux)
			if err != nil {
				// Parse error acceptable for some malformed inputs
				return
			}
			em := &emitter.Emitter{Schema: minSchema()}
			_, err = em.Emit(q)
			if err == nil {
				t.Fatalf("expected emit error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}
