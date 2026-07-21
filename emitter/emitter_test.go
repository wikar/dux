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
		Name: "sales",
		Columns: map[string]*semantic.Column{
			"id":      {Name: "id", DataType: "INTEGER"},
			"product": {Name: "product", DataType: "TEXT"},
			"amount":  {Name: "amount", DataType: "DOUBLE"},
			"qty":     {Name: "qty", DataType: "INTEGER"},
			"region":  {Name: "region", DataType: "TEXT"},
		},
	}
	s.Tables["products"] = &semantic.Table{
		Name: "products",
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
		// Inside SUMMARIZECOLUMNS an iterator over a bare table aggregates
		// inline over the grouped FROM rows (group-context correlation).
		{
			"SUMX",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Rev", SUMX(sales, sales[amount] * sales[qty]))`,
			[]string{"SUM(amount * qty)", "GROUP BY region"},
		},
		{
			"AVERAGEX",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Avg", AVERAGEX(sales, sales[amount]))`,
			[]string{"AVG(amount)", "GROUP BY region"},
		},
		{
			"COUNTX",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "N", COUNTX(sales, sales[id]))`,
			[]string{"COUNT(id)", "GROUP BY region"},
		},
		{
			"MINX",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Min", MINX(sales, sales[amount]))`,
			[]string{"MIN(amount)", "GROUP BY region"},
		},
		{
			"MAXX",
			`EVALUATE SUMMARIZECOLUMNS(sales[region], "Max", MAXX(sales, sales[amount]))`,
			[]string{"MAX(amount)", "GROUP BY region"},
		},
		// Outside a group context the scalar-subquery form is kept.
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
			if strings.Contains(tt.dux, "SUMMARIZECOLUMNS") {
				assertNotContains(t, sql, "__row_sales")
			}
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

	t.Run("TREATAS_MultiColumn", func(t *testing.T) {
		// A multi-column set membership emits as OR-of-ANDs (no row-value IN).
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			TREATAS({("North", "A"), ("South", "B")}, sales[region], sales[product]),
			"Total", SUM(sales[amount])
		)`)
		assertContains(t, sql, "WHERE",
			"region = 'North'", "product = 'A'",
			"region = 'South'", "product = 'B'", " OR ")
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

	t.Run("ALL_table", func(t *testing.T) {
		sql := emit(t, `EVALUATE ALL(sales)`)
		assertContains(t, sql, "SELECT * FROM sales")
	})

	t.Run("ALL_column", func(t *testing.T) {
		sql := emit(t, `EVALUATE ALL(sales[region])`)
		assertContains(t, sql, "SELECT DISTINCT region FROM sales")
	})

	t.Run("ALL_multi_column", func(t *testing.T) {
		sql := emit(t, `EVALUATE ALL(sales[region], sales[product])`)
		assertContains(t, sql, "SELECT DISTINCT region, product FROM sales")
	})

	t.Run("FILTER_over_ALL_table", func(t *testing.T) {
		sql := emit(t, `EVALUATE FILTER(ALL(sales), sales[amount] > 100)`)
		assertContains(t, sql, "SELECT * FROM sales WHERE", "> 100")
	})

	t.Run("DISTINCT", func(t *testing.T) {
		sql := emit(t, `EVALUATE DISTINCT(sales[region])`)
		assertContains(t, sql, "SELECT DISTINCT", "FROM sales")
	})
}

// ─── Filter-context modifiers (ALL / ALLEXCEPT / REMOVEFILTERS / KEEPFILTERS) ──

func TestFilterContextModifiers(t *testing.T) {
	t.Run("ALL_table_grand_total", func(t *testing.T) {
		// ALL(sales) removes the region group filter → uncorrelated subquery.
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"Total", SUM(sales[amount]),
			"Grand", CALCULATE(SUM(sales[amount]), ALL(sales))
		)`)
		assertContains(t, sql, "(SELECT SUM(amount) FROM sales AS __cal_sales)", "GROUP BY region")
		assertNotContains(t, sql, "__cal_sales.region = sales.region")
	})

	t.Run("ALL_inside_DIVIDE_pct_of_total", func(t *testing.T) {
		// The group context must reach CALCULATE nested inside DIVIDE.
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"Pct", DIVIDE(SUM(sales[amount]), CALCULATE(SUM(sales[amount]), ALL(sales)))
		)`)
		assertContains(t, sql, "(SELECT SUM(amount) FROM sales AS __cal_sales)", "CASE WHEN")
	})

	t.Run("ALL_column_keeps_other_keys", func(t *testing.T) {
		// ALL(sales[product]) clears only the product key; region stays correlated.
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			sales[product],
			"RegionTotal", CALCULATE(SUM(sales[amount]), ALL(sales[product]))
		)`)
		assertContains(t, sql, "__cal_sales.region = sales.region")
		assertNotContains(t, sql, "__cal_sales.product = sales.product")
	})

	t.Run("ALLEXCEPT_keeps_listed_column", func(t *testing.T) {
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			sales[product],
			"RegionTotal", CALCULATE(SUM(sales[amount]), ALLEXCEPT(sales, sales[region]))
		)`)
		assertContains(t, sql, "__cal_sales.region = sales.region")
		assertNotContains(t, sql, "__cal_sales.product = sales.product")
	})

	t.Run("REMOVEFILTERS_is_ALL", func(t *testing.T) {
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"Grand", CALCULATE(SUM(sales[amount]), REMOVEFILTERS(sales))
		)`)
		assertContains(t, sql, "(SELECT SUM(amount) FROM sales AS __cal_sales)")
	})

	t.Run("KEEPFILTERS_stays_additive", func(t *testing.T) {
		// KEEPFILTERS never removes group filters → fast path FILTER (WHERE ...).
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"North", CALCULATE(SUM(sales[amount]), KEEPFILTERS(sales[region] = "North"))
		)`)
		assertContains(t, sql, "FILTER (WHERE", "'North'")
		assertNotContains(t, sql, "__cal_sales")
	})

	t.Run("predicate_overrides_group_key", func(t *testing.T) {
		// DAX shorthand: a plain predicate on a grouped column replaces that
		// group filter — every region row shows the North value.
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"NorthTotal", CALCULATE(SUM(sales[amount]), sales[region] = "North")
		)`)
		assertContains(t, sql, "__cal_sales", "region = 'North'")
		assertNotContains(t, sql, "__cal_sales.region = sales.region")
	})

	t.Run("predicate_on_nongrouped_column_stays_fast_path", func(t *testing.T) {
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"BigQty", CALCULATE(SUM(sales[amount]), sales[qty] > 2)
		)`)
		assertContains(t, sql, "FILTER (WHERE")
		assertNotContains(t, sql, "__cal_sales")
	})

	t.Run("FILTER_ALL_pattern", func(t *testing.T) {
		// CALCULATE(x, FILTER(ALL(T), pred)) — canonical DAX replacement filter.
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"NorthTotal", CALCULATE(SUM(sales[amount]), FILTER(ALL(sales), sales[region] = "North"))
		)`)
		assertContains(t, sql, "__cal_sales", "region = 'North'")
		assertNotContains(t, sql, "__cal_sales.region = sales.region")
	})

	t.Run("ALL_with_joined_dimension_key_kept", func(t *testing.T) {
		// Group by products[category]; ALL(sales) keeps the category filter,
		// which must be re-joined and correlated inside the subquery.
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			products[category],
			"AllSales", CALCULATE(SUM(sales[amount]), ALL(sales))
		)`)
		assertContains(t, sql,
			"LEFT JOIN products AS __cal_products",
			"__cal_products.category = products.category")
	})

	t.Run("ALL_removes_TREATAS_filter_on_same_table", func(t *testing.T) {
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			TREATAS({"North"}, sales[region]),
			"Grand", CALCULATE(SUM(sales[amount]), ALL(sales))
		)`)
		assertContains(t, sql, "(SELECT SUM(amount) FROM sales AS __cal_sales)")
		// The outer query keeps its TREATAS filter.
		assertContains(t, sql, "WHERE region IN ('North')")
	})

	t.Run("standalone_CALCULATE_with_ALL", func(t *testing.T) {
		// Outside SUMMARIZECOLUMNS there is no ambient context — ALL just
		// yields the unfiltered aggregate as a complete SELECT.
		sql := emit(t, `EVALUATE CALCULATE(SUM(sales[amount]), ALL(sales))`)
		assertContains(t, sql, "(SELECT SUM(amount) FROM sales)")
	})
}

// ─── Composable tables / ORDER BY / combinators ───────────────────────────────

func TestComposableTables(t *testing.T) {
	t.Run("FILTER_over_SUMMARIZECOLUMNS", func(t *testing.T) {
		sql := emit(t, `EVALUATE FILTER(
			SUMMARIZECOLUMNS(sales[region], "Total", SUM(sales[amount])),
			[Total] > 400
		)`)
		assertContains(t, sql, "SELECT * FROM (", ") AS __src", "WHERE")
	})

	t.Run("TOPN_over_computed_table_uses_output_column", func(t *testing.T) {
		sql := emit(t, `EVALUATE TOPN(
			2,
			SUMMARIZECOLUMNS(sales[product], "Total", SUM(sales[amount])),
			[Total]
		)`)
		// The order key must reference the output column, not re-aggregate.
		assertContains(t, sql, `ORDER BY "Total" DESC LIMIT 2`)
	})

	t.Run("SUMX_over_FILTER_keeps_row_binding", func(t *testing.T) {
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			"X", SUMX(FILTER(sales, sales[qty] > 1), sales[amount])
		)`)
		assertContains(t, sql, "__row_sales.amount", "FROM (SELECT * FROM sales WHERE")
	})

	t.Run("ORDER_BY_wraps_query", func(t *testing.T) {
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(sales[region], "Total", SUM(sales[amount]))
			ORDER BY [Total] DESC, sales[region]`)
		assertContains(t, sql, ") AS __q", `ORDER BY "Total" DESC, "region"`)
	})

	t.Run("START_AT_tuple_filter", func(t *testing.T) {
		sql := emit(t, `EVALUATE sales ORDER BY sales[region] START AT "North"`)
		assertContains(t, sql, `WHERE ("region") >= ('North')`, `ORDER BY "region"`)
	})

	t.Run("START_AT_with_DESC_errors", func(t *testing.T) {
		q := mustParse(t, `EVALUATE sales ORDER BY sales[region] DESC START AT "North"`)
		em := &emitter.Emitter{Schema: minSchema()}
		if _, err := em.Emit(q); err == nil || !strings.Contains(err.Error(), "ascending") {
			t.Errorf("expected ascending-only START AT error, got %v", err)
		}
	})

	t.Run("CROSSJOIN", func(t *testing.T) {
		sql := emit(t, `EVALUATE CROSSJOIN(VALUES(sales[region]), VALUES(products[category]))`)
		assertContains(t, sql, "CROSS JOIN")
	})

	t.Run("GENERATE_lateral_with_row_context", func(t *testing.T) {
		sql := emit(t, `EVALUATE GENERATE(products, FILTER(sales, sales[product] = products[product]))`)
		assertContains(t, sql,
			"products AS __gen_products",
			"CROSS JOIN LATERAL",
			"= __gen_products.product")
	})

	t.Run("GENERATEALL_left_lateral", func(t *testing.T) {
		sql := emit(t, `EVALUATE GENERATEALL(products, FILTER(sales, sales[product] = products[product]))`)
		assertContains(t, sql, "LEFT JOIN LATERAL", "ON TRUE")
	})
}

// ─── ROLLUPADDISSUBTOTAL / ROLLUPGROUP ────────────────────────────────────────

func TestRollup(t *testing.T) {
	t.Run("grouping_sets_and_indicator", func(t *testing.T) {
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			products[category],
			ROLLUPADDISSUBTOTAL(sales[region], "RegionTotal"),
			"Total", SUM(sales[amount])
		)`)
		assertContains(t, sql,
			"(GROUPING(region) = 1) AS 'RegionTotal'",
			"GROUP BY GROUPING SETS ((category, region), (category))")
	})

	t.Run("hierarchical_sets", func(t *testing.T) {
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			ROLLUPADDISSUBTOTAL(sales[region], "RT", sales[product], "PT"),
			"Total", SUM(sales[amount])
		)`)
		assertContains(t, sql, "GROUPING SETS ((region, product), (region), ())")
	})

	t.Run("ROLLUPGROUP_composite_unit", func(t *testing.T) {
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			ROLLUPADDISSUBTOTAL(ROLLUPGROUP(sales[region], sales[product]), "IsTotal"),
			"Total", SUM(sales[amount])
		)`)
		assertContains(t, sql, "GROUPING SETS ((region, product), ())")
	})

	t.Run("bare_ROLLUPGROUP_errors", func(t *testing.T) {
		q := mustParse(t, `EVALUATE SUMMARIZECOLUMNS(ROLLUPGROUP(sales[region]), "T", SUM(sales[amount]))`)
		em := &emitter.Emitter{Schema: minSchema()}
		if _, err := em.Emit(q); err == nil || !strings.Contains(err.Error(), "ROLLUPADDISSUBTOTAL") {
			t.Errorf("expected bare-ROLLUPGROUP error, got %v", err)
		}
	})
}

// ─── Time intelligence ────────────────────────────────────────────────────────

// timeSchema builds a schema with a dates dimension and an orders fact.
// When designate is true, dates is marked as the model's date table.
func timeSchema(designate bool) *semantic.Schema {
	s := semantic.NewSchema()
	s.Tables["dates"] = &semantic.Table{
		Name: "dates",
		Columns: map[string]*semantic.Column{
			"date":  {Name: "date", DataType: "DATE"},
			"year":  {Name: "year", DataType: "INTEGER"},
			"month": {Name: "month", DataType: "INTEGER"},
		},
	}
	s.Tables["orders"] = &semantic.Table{
		Name: "orders",
		Columns: map[string]*semantic.Column{
			"order_date": {Name: "order_date", DataType: "DATE"},
			"amount":     {Name: "amount", DataType: "DOUBLE"},
		},
	}
	s.Relationships = append(s.Relationships, &semantic.Relationship{
		FromTable:  "orders",
		FromColumn: "order_date",
		ToTable:    "dates",
		ToColumn:   "date",
	})
	if designate {
		s.SetDateTable("dates", "date")
	}
	return s
}

func emitTime(t *testing.T, designate bool, dux string) string {
	t.Helper()
	q := mustParse(t, dux)
	em := &emitter.Emitter{Schema: timeSchema(designate)}
	sql, err := em.Emit(q)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	return sql
}

func TestTimeIntelligence(t *testing.T) {
	t.Run("TOTALYTD_anchored_to_group_context", func(t *testing.T) {
		sql := emitTime(t, true, `EVALUATE SUMMARIZECOLUMNS(
			dates[year],
			dates[month],
			"YTD", TOTALYTD(SUM(orders[amount]), dates[date])
		)`)
		// Anchor correlates on the date table's group keys...
		assertContains(t, sql,
			"date_trunc('year'",
			"__ti_dates.year = dates.year",
			"__ti_dates.month = dates.month")
		// ...and the designated date table's own group correlations are cleared.
		assertNotContains(t, sql, "__cal_dates.year = dates.year")
	})

	t.Run("undesignated_table_keeps_other_column_filters", func(t *testing.T) {
		// Without the designation only the date column's filter is replaced,
		// so the year group key stays correlated (DAX behaviour for a column
		// that is not on a marked date table).
		sql := emitTime(t, false, `EVALUATE SUMMARIZECOLUMNS(
			dates[year],
			"YTD", CALCULATE(SUM(orders[amount]), DATESYTD(dates[date]))
		)`)
		assertContains(t, sql, "__cal_dates.year = dates.year")
	})

	t.Run("DATEADD_shifts_range", func(t *testing.T) {
		sql := emitTime(t, true, `EVALUATE SUMMARIZECOLUMNS(
			dates[year],
			"PY", CALCULATE(SUM(orders[amount]), DATEADD(dates[date], -1, YEAR))
		)`)
		assertContains(t, sql, "* INTERVAL 1 YEAR", "-(1)")
	})

	t.Run("standalone_DATESYTD_is_a_table", func(t *testing.T) {
		sql := emitTime(t, true, `EVALUATE DATESYTD(dates[date])`)
		assertContains(t, sql, "SELECT DISTINCT date FROM dates WHERE", "date_trunc('year'")
	})

	t.Run("CALENDAR", func(t *testing.T) {
		sql := emitTime(t, true, `EVALUATE CALENDAR(DATE(2024, 1, 1), DATE(2024, 12, 31))`)
		assertContains(t, sql, "generate_series", "make_date", `AS "Date"`)
	})

	t.Run("CALENDARAUTO_scans_date_columns", func(t *testing.T) {
		sql := emitTime(t, true, `EVALUATE CALENDARAUTO()`)
		assertContains(t, sql,
			"SELECT MIN(date) AS mn, MAX(date) AS mx FROM dates",
			"SELECT MIN(order_date) AS mn, MAX(order_date) AS mx FROM orders",
			"make_date")
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
			"REMOVEFILTERS_outside_CALCULATE",
			`EVALUATE REMOVEFILTERS(sales)`,
			"only valid as a CALCULATE filter argument",
		},
		{
			"ALL_no_args_outside_CALCULATE",
			`EVALUATE ALL()`,
			"only valid inside CALCULATE",
		},
		{
			"TREATAS_wrong_arg_count",
			`EVALUATE TREATAS({"x"})`,
			"TREATAS requires at least 2 arguments",
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

// ─── Bidirectional relationships ──────────────────────────────────────────────

// bidiSchema builds a schema that mirrors the spec example:
//
//	DimA.DimAKey → Bridge.DimAKey   (normal, many→one)
//	Bridge.DimBKey ↔ DimB.DimBKey   (bidirectional)
//	FactMeasures.DimBKey → DimB.DimBKey (normal, many→one)
func bidiSchema() *semantic.Schema {
	s := semantic.NewSchema()
	s.Tables["DimA"] = &semantic.Table{Name: "DimA", Columns: map[string]*semantic.Column{
		"DimAKey":  {Name: "DimAKey", DataType: "INTEGER"},
		"Category": {Name: "Category", DataType: "TEXT"},
	}}
	s.Tables["Bridge"] = &semantic.Table{Name: "Bridge", Columns: map[string]*semantic.Column{
		"DimAKey": {Name: "DimAKey", DataType: "INTEGER"},
		"DimBKey": {Name: "DimBKey", DataType: "INTEGER"},
	}}
	s.Tables["DimB"] = &semantic.Table{Name: "DimB", Columns: map[string]*semantic.Column{
		"DimBKey": {Name: "DimBKey", DataType: "INTEGER"},
		"Name":    {Name: "Name", DataType: "TEXT"},
	}}
	s.Tables["FactMeasures"] = &semantic.Table{Name: "FactMeasures", Columns: map[string]*semantic.Column{
		"DimBKey": {Name: "DimBKey", DataType: "INTEGER"},
		"Amount":  {Name: "Amount", DataType: "DOUBLE"},
	}}
	// Bridge.DimAKey → DimA.DimAKey (normal)
	s.Relationships = append(s.Relationships, &semantic.Relationship{
		FromTable: "Bridge", FromColumn: "DimAKey",
		ToTable: "DimA", ToColumn: "DimAKey",
	})
	// Bridge.DimBKey ↔ DimB.DimBKey (bidirectional)
	s.Relationships = append(s.Relationships, &semantic.Relationship{
		FromTable: "Bridge", FromColumn: "DimBKey",
		ToTable: "DimB", ToColumn: "DimBKey",
		Bidirectional: true,
	})
	// FactMeasures.DimBKey → DimB.DimBKey (normal)
	s.Relationships = append(s.Relationships, &semantic.Relationship{
		FromTable: "FactMeasures", FromColumn: "DimBKey",
		ToTable: "DimB", ToColumn: "DimBKey",
	})
	return s
}

func emitBidi(t *testing.T, dux string) string {
	t.Helper()
	q := mustParse(t, dux)
	em := &emitter.Emitter{Schema: bidiSchema()}
	sql, err := em.Emit(q)
	if err != nil {
		t.Fatalf("emit (bidi): %v", err)
	}
	return sql
}

func TestBidirectionalCTE(t *testing.T) {
	// Bidirectional queries route through stitched codegen: the filter chain
	// beyond the bidi edge (bridge → DimA) is carved into a correlated EXISTS
	// semi-join, gating rows without many-to-many fan-out.

	// TC-01: Measure only, filtered by DimA via TREATAS.
	t.Run("TC01_MeasureOnly", func(t *testing.T) {
		sql := emitBidi(t,
			`EVALUATE SUMMARIZECOLUMNS(
				TREATAS({"X"}, DimA[Category]),
				"Total", SUM(FactMeasures[Amount])
			)`)
		assertContains(t, sql,
			"WITH _mc0 AS",
			"FROM factmeasures",
			"EXISTS (SELECT 1 FROM bridge",
			"JOIN dima",
			"Category IN ('X')",
		)
		// DimA is filter-only: gated inside the EXISTS, never joined flat.
		assertNotContains(t, sql, "_bd_", "LEFT JOIN dima")
	})

	// TC-02: DimB attributes only, filtered by DimA.
	// DimB carries the group key; FactMeasures must not appear.
	t.Run("TC02_DimBOnly", func(t *testing.T) {
		sql := emitBidi(t,
			`EVALUATE SUMMARIZECOLUMNS(
				DimB[Name],
				TREATAS({"X"}, DimA[Category])
			)`)
		assertContains(t, sql,
			"WITH _mc0 AS",
			"FROM dimb",
			"EXISTS (SELECT 1 FROM bridge",
			"JOIN dima",
		)
		assertNotContains(t, sql, "_bd_", "LEFT JOIN dima", "factmeasures")
	})

	// TC-03: DimB attributes + measure, filtered by DimA (full chain).
	t.Run("TC03_FullChain", func(t *testing.T) {
		sql := emitBidi(t,
			`EVALUATE SUMMARIZECOLUMNS(
				DimB[Name],
				TREATAS({"X"}, DimA[Category]),
				"Total", SUM(FactMeasures[Amount])
			)`)
		assertContains(t, sql,
			"WITH _mc0 AS",
			"FROM dimb",
			"LEFT JOIN factmeasures",
			"EXISTS (SELECT 1 FROM bridge",
			"GROUP BY",
		)
		assertNotContains(t, sql, "_bd_", "LEFT JOIN dima")
	})

	// TC-04: Multiple DimA values — the semi-join gates without duplicating
	// DimB rows that match more than one bridge row.
	t.Run("TC04_SemiJoinGuard", func(t *testing.T) {
		sql := emitBidi(t,
			`EVALUATE SUMMARIZECOLUMNS(
				DimB[Name],
				TREATAS({"X","Y"}, DimA[Category])
			)`)
		assertContains(t, sql, "EXISTS (SELECT 1 FROM bridge", "Category IN ('X', 'Y')")
	})

	// TC-05: Empty filter context — same SQL shape as TC-01; no special-casing.
	t.Run("TC05_EmptyFilter", func(t *testing.T) {
		sql := emitBidi(t,
			`EVALUATE SUMMARIZECOLUMNS(
				TREATAS({"NONEXISTENT"}, DimA[Category]),
				"Total", SUM(FactMeasures[Amount])
			)`)
		assertContains(t, sql, "WITH _mc0 AS", "EXISTS (SELECT 1 FROM bridge")
	})

	// Db-qualified table names emit as-is inside the EXISTS chain; no
	// generated identifier may contain a dot.
	t.Run("QualifiedTableName", func(t *testing.T) {
		s := bidiSchema()
		qualified := semantic.NewSchema()
		for name, tbl := range s.Tables {
			qualified.Tables["atp."+name] = &semantic.Table{Name: "atp." + tbl.Name, Columns: tbl.Columns}
		}
		for _, r := range s.Relationships {
			qualified.Relationships = append(qualified.Relationships, &semantic.Relationship{
				FromTable: "atp." + r.FromTable, FromColumn: r.FromColumn,
				ToTable: "atp." + r.ToTable, ToColumn: r.ToColumn,
				Bidirectional: r.Bidirectional,
			})
		}
		q := mustParse(t,
			`EVALUATE SUMMARIZECOLUMNS(
				TREATAS({"X"}, atp.DimA[Category]),
				"Total", SUM(atp.FactMeasures[Amount])
			)`)
		em := &emitter.Emitter{Schema: qualified}
		sql, err := em.Emit(q)
		if err != nil {
			t.Fatalf("emit (qualified bidi): %v", err)
		}
		assertContains(t, sql,
			"WITH _mc0 AS",
			"FROM atp.factmeasures",
			"EXISTS (SELECT 1 FROM atp.bridge",
			"JOIN atp.dima",
		)
		assertNotContains(t, sql, "_bd_")
	})

	// The bidi bridge (bev.Date) is the group-by table with measures over two
	// different facts — a multi-table query. Stitched codegen must evaluate
	// each measure in its own cluster CTE; a flat join would fan the facts
	// out against each other (and the old bidi CTE emitted "FROM bev.date
	// JOIN bev.date" — a duplicate alias).
	t.Run("ProjectedBridgeGoesStitched", func(t *testing.T) {
		s := semantic.NewSchema()
		s.Tables["bev.Date"] = &semantic.Table{Name: "bev.Date", Columns: map[string]*semantic.Column{
			"DateKey":  {Name: "DateKey", DataType: "INTEGER"},
			"FullDate": {Name: "FullDate", DataType: "DATE"},
			"Year":     {Name: "Year", DataType: "INTEGER"},
		}}
		s.Tables["bev.Sales"] = &semantic.Table{Name: "bev.Sales", Columns: map[string]*semantic.Column{
			"DateKey":  {Name: "DateKey", DataType: "INTEGER"},
			"Quantity": {Name: "Quantity", DataType: "INTEGER"},
		}}
		s.Tables["atp.players"] = &semantic.Table{Name: "atp.players", Columns: map[string]*semantic.Column{
			"player_id": {Name: "player_id", DataType: "INTEGER"},
			"dob":       {Name: "dob", DataType: "DATE"},
		}}
		s.Tables["atp.matches"] = &semantic.Table{Name: "atp.matches", Columns: map[string]*semantic.Column{
			"winner_id":  {Name: "winner_id", DataType: "INTEGER"},
			"winner_age": {Name: "winner_age", DataType: "DOUBLE"},
		}}
		s.Relationships = append(s.Relationships,
			&semantic.Relationship{FromTable: "bev.Sales", FromColumn: "DateKey", ToTable: "bev.Date", ToColumn: "DateKey"},
			&semantic.Relationship{FromTable: "bev.Date", FromColumn: "FullDate", ToTable: "atp.players", ToColumn: "dob", Bidirectional: true},
			&semantic.Relationship{FromTable: "atp.matches", FromColumn: "winner_id", ToTable: "atp.players", ToColumn: "player_id"},
		)
		q := mustParse(t,
			`EVALUATE SUMMARIZECOLUMNS(
				bev.Date[Year],
				"Quantity", SUM(bev.Sales[Quantity]),
				"winner_age", SUM(atp.matches[winner_age])
			)`)
		em := &emitter.Emitter{Schema: s}
		sql, err := em.Emit(q)
		if err != nil {
			t.Fatalf("emit (projected bridge): %v", err)
		}
		assertNotContains(t, sql, "_bd_")
		assertContains(t, sql,
			"WITH _mc0 AS",
			"_mc1 AS",
			"FULL OUTER JOIN _mc1",
			"IS NOT DISTINCT FROM",
			"LEFT JOIN bev.sales",
			"LEFT JOIN atp.players",
			"LEFT JOIN atp.matches",
		)
		// The two facts must never share one join tree: the sales fact and the
		// atp fact belong to different CTEs.
		if i := strings.Index(sql, "bev.sales"); i >= 0 {
			cte := sql[:strings.Index(sql, "_mc1")]
			if strings.Contains(cte, "atp.matches") {
				t.Errorf("bev.sales and atp.matches share a join tree:\n%s", sql)
			}
		}
		if n := strings.Count(strings.ToLower(sql), "join bev.date"); n != 0 {
			t.Errorf("bev.date must only appear in FROM clauses, found %d JOINs:\n%s", n, sql)
		}
	})

	// Unidirectional relationships must still emit LEFT JOIN, never a CTE.
	t.Run("UniDirNotAffected", func(t *testing.T) {
		sql := emit(t, `EVALUATE SUMMARIZECOLUMNS(
			products[category],
			"Total", SUM(sales[amount])
		)`)
		assertContains(t, sql, "LEFT JOIN")
		assertNotContains(t, sql, "WITH", "_bd_")
	})
}

// ─── Bidirectional validation ─────────────────────────────────────────────────

func TestValidateBidiPaths(t *testing.T) {
	t.Run("ValidSchema", func(t *testing.T) {
		if err := semantic.ValidateBidiPaths(bidiSchema()); err != nil {
			t.Errorf("expected no error for valid bidi schema, got: %v", err)
		}
	})

	t.Run("TC06_AmbiguousSchema", func(t *testing.T) {
		s := bidiSchema()
		// Add a direct DimA → FactMeasures relationship — now two paths exist:
		//   [1] DimA → Bridge ↔ DimB → FactMeasures
		//   [2] DimA → FactMeasures (direct)
		s.Relationships = append(s.Relationships, &semantic.Relationship{
			FromTable: "FactMeasures", FromColumn: "DimBKey",
			ToTable: "DimA", ToColumn: "DimAKey",
		})
		// Mark the bidi edge to trigger the ambiguity check.
		err := semantic.ValidateBidiPaths(s)
		if err == nil {
			t.Fatal("expected ambiguity error, got nil")
		}
		if !strings.Contains(err.Error(), "ambiguous") {
			t.Errorf("expected error to mention 'ambiguous', got: %v", err)
		}
	})

	t.Run("NoBidiEdges", func(t *testing.T) {
		s := minSchema()
		if err := semantic.ValidateBidiPaths(s); err != nil {
			t.Errorf("expected no error when no bidi edges present, got: %v", err)
		}
	})
}
