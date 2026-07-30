package executor_test

// Multi-table ("stitched codegen") executor tests.
//
// setupMultiTableDB builds the canonical fan-out fixture: one dimension
// (dates) with two fact tables (fact_sales, fact_returns) hanging off it.
// A single flat join tree over this shape multiplies fact rows (2 sales
// rows × 2 returns rows on the same date) and inflates every aggregate;
// per-measure stitched codegen must return the true per-fact sums.

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/danielwikar/dux/executor"
	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// executorExecute is a thin non-fataling wrapper so error-path tests can
// assert on the returned error.
func executorExecute(db *sql.DB, schema *semantic.Schema, dux string) ([]string, []map[string]any, error) {
	return executor.Execute(db, schema, dux)
}

// addMeasure stores a named measure on the schema, parsed from its DUX
// expression (mirrors MetadataDB.loadMeasures).
func addMeasure(t *testing.T, schema *semantic.Schema, table, name, expression string) {
	t.Helper()
	defs, err := parser.ParseMeasures(fmt.Sprintf("DEFINE\n    MEASURE %s[%s] = %s", table, name, expression))
	if err != nil {
		t.Fatalf("parse measure %s[%s]: %v", table, name, err)
	}
	if schema.Measures[table] == nil {
		schema.Measures[table] = map[string]*parser.MeasureDefinition{}
	}
	schema.Measures[table][name] = defs[0]
}

// setupMultiTableDB returns an in-memory DuckDB with:
//
//	dates:        (datekey, year)            3 rows, 2 years
//	fact_sales:   (datekey, qty)             2024: 10+20+5 = 35, 2025: 7
//	fact_returns: (datekey, rqty)            2024: 1+2+3   = 6,  2025: 4
//
// datekey=1 has two rows in BOTH facts, so a flat join yields 4 combined
// rows for that date and inflates 2024 to Sold=65, Returned=9.
func setupMultiTableDB(t *testing.T) (*sql.DB, *semantic.Schema) {
	t.Helper()

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ddl := []string{
		`CREATE TABLE dates (datekey INTEGER, year INTEGER)`,
		`INSERT INTO dates VALUES (1, 2024), (2, 2024), (3, 2025)`,
		`CREATE TABLE fact_sales (datekey INTEGER, qty INTEGER, pkey INTEGER)`,
		`INSERT INTO fact_sales VALUES (1, 10, 1), (1, 20, 2), (2, 5, 1), (3, 7, 2)`,
		`CREATE TABLE fact_returns (datekey INTEGER, rqty INTEGER)`,
		`INSERT INTO fact_returns VALUES (1, 1), (1, 2), (2, 3), (3, 4)`,
		`CREATE TABLE products (pkey INTEGER, pname VARCHAR)`,
		`INSERT INTO products VALUES (1, 'A'), (2, 'B')`,
	}
	for _, s := range ddl {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("setup: %v — %s", err, s)
		}
	}

	schema, err := semantic.IntrospectDuckDB(db)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	schema.Relationships = append(schema.Relationships,
		&semantic.Relationship{FromTable: "fact_sales", FromColumn: "datekey", ToTable: "dates", ToColumn: "datekey"},
		&semantic.Relationship{FromTable: "fact_returns", FromColumn: "datekey", ToTable: "dates", ToColumn: "datekey"},
		&semantic.Relationship{FromTable: "fact_sales", FromColumn: "pkey", ToTable: "products", ToColumn: "pkey"},
	)
	return db, schema
}

func setupBidiBridgeDB(t *testing.T) (*sql.DB, *semantic.Schema) {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ddl := []string{
		`CREATE TABLE clubs (clubkey INTEGER, style VARCHAR, name VARCHAR)`,
		`INSERT INTO clubs VALUES (100, 'Beer', 'A'), (101, 'Beer', 'B'), (102, 'Wine', 'C')`,
		`CREATE TABLE memberships (clubkey INTEGER, customerkey INTEGER)`,
		`INSERT INTO memberships VALUES (100, 1), (100, 1), (100, 2), (101, 1), (102, 3)`,
		`CREATE TABLE customers (customerkey INTEGER)`,
		`INSERT INTO customers VALUES (1), (2), (3)`,
		`CREATE TABLE dates (year INTEGER)`,
		`INSERT INTO dates VALUES (2024), (2025)`,
		`CREATE TABLE bidi_sales (customerkey INTEGER, year INTEGER, amount INTEGER, discount INTEGER)`,
		`INSERT INTO bidi_sales VALUES (1, 2024, 10, 1), (2, 2024, 20, 2), (3, 2025, 30, 3)`,
	}
	for _, stmt := range ddl {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("setup: %v — %s", err, stmt)
		}
	}
	schema, err := semantic.IntrospectDuckDB(db)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	schema.Relationships = append(schema.Relationships,
		&semantic.Relationship{FromTable: "memberships", FromColumn: "clubkey", ToTable: "clubs", ToColumn: "clubkey"},
		&semantic.Relationship{FromTable: "memberships", FromColumn: "customerkey", ToTable: "customers", ToColumn: "customerkey", Bidirectional: true},
		&semantic.Relationship{FromTable: "bidi_sales", FromColumn: "customerkey", ToTable: "customers", ToColumn: "customerkey"},
		&semantic.Relationship{FromTable: "bidi_sales", FromColumn: "year", ToTable: "dates", ToColumn: "year"},
	)
	addMeasure(t, schema, "bidi_sales", "Net", `SUM(bidi_sales[amount]) - SUM(bidi_sales[discount])`)
	return db, schema
}

func TestStitched_BidirectionalGroupKeyFiltersMeasure(t *testing.T) {
	db, schema := setupBidiBridgeDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		clubs[name],
		"Net", bidi_sales[Net]
	)`)
	if len(rows) != 3 {
		t.Fatalf("expected three clubs, got %d: %v", len(rows), rows)
	}
	want := map[string]float64{"A": 27, "B": 9, "C": 27}
	for _, row := range rows {
		name := cell(t, row, "name").(string)
		if got := toFloat(cell(t, row, "Net")); got != want[name] {
			t.Errorf("%s Net = %v, want %v", name, got, want[name])
		}
	}
}

func TestGroupOnlySlicerIgnoresExternalFilterFromAnotherTable(t *testing.T) {
	db, schema := setupBidiBridgeDB(t)
	_, rows := runFiltered(t, db, schema,
		`EVALUATE SUMMARIZECOLUMNS(clubs[name])`,
		[]executor.ExternalFilter{{Table: "dates", Column: "year", Op: "in", Values: []any{2024}}})
	if len(rows) != 3 {
		t.Fatalf("expected all three untrimmed Club options, got %d: %v", len(rows), rows)
	}
}

func TestStitched_BidirectionalCalculateDoesNotFanOut(t *testing.T) {
	db, schema := setupBidiBridgeDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		"Amount", CALCULATE(SUM(bidi_sales[amount]), clubs[style] = "Beer"),
		"Net", CALCULATE([Net], clubs[style] = "Beer"),
		"OneClub", CALCULATE([Net], clubs[name] = "A")
	)`)
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %d: %v", len(rows), rows)
	}
	if got := toFloat(cell(t, rows[0], "Amount")); got != 30 {
		t.Errorf("Amount = %v, want 30 (bridge fan-out would give 40)", got)
	}
	if got := toFloat(cell(t, rows[0], "Net")); got != 27 {
		t.Errorf("Net = %v, want 27 (bridge fan-out would give 36)", got)
	}
	if got := toFloat(cell(t, rows[0], "OneClub")); got != 27 {
		t.Errorf("OneClub = %v, want 27", got)
	}
}

func TestStandaloneCalculateDoesNotFanOutAcrossBidirectionalBridge(t *testing.T) {
	db, schema := setupBidiBridgeDB(t)
	cols, rows := run(t, db, schema, `EVALUATE CALCULATE([Net], clubs[style] = "Beer")`)
	if len(rows) != 1 || len(cols) != 1 || toFloat(rows[0][cols[0]]) != 27 {
		t.Fatalf("standalone CALCULATE = %v, want Net 27", rows)
	}
}

// rowByKey returns the row whose column k equals v.
func rowByKey(t *testing.T, rows []map[string]any, k string, v int64) map[string]any {
	t.Helper()
	for _, r := range rows {
		if toFloat(r[k]) == float64(v) {
			return r
		}
	}
	t.Fatalf("no row with %s=%d in %v", k, v, rows)
	return nil
}

func TestStitched_MultiTableFanOut(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		"Sold",     SUM(fact_sales[qty]),
		"Returned", SUM(fact_returns[rqty])
	)`)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(rows), rows)
	}
	r2024 := rowByKey(t, rows, "year", 2024)
	r2025 := rowByKey(t, rows, "year", 2025)
	if got := toFloat(cell(t, r2024, "Sold")); got != 35 {
		t.Errorf("2024 Sold = %v, want 35 (fan-out would give 65)", got)
	}
	if got := toFloat(cell(t, r2024, "Returned")); got != 6 {
		t.Errorf("2024 Returned = %v, want 6 (fan-out would give 9)", got)
	}
	if got := toFloat(cell(t, r2025, "Sold")); got != 7 {
		t.Errorf("2025 Sold = %v, want 7", got)
	}
	if got := toFloat(cell(t, r2025, "Returned")); got != 4 {
		t.Errorf("2025 Returned = %v, want 4", got)
	}
}

func TestCrossFactGroupKeyRejected(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, _, err := executorExecute(db, schema, `EVALUATE SUMMARIZECOLUMNS(
		fact_returns[datekey],
		"Sold", SUM(fact_sales[qty]))`)
	if err == nil {
		t.Fatal("expected unsafe cross-fact grouping error")
	}
	for _, want := range []string{"fact_returns", "fact_sales", "shared dimension"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	var qe *executor.QueryError
	if !errors.As(err, &qe) || qe.Line != 2 || qe.Column == 0 {
		t.Errorf("error position = %#v, want line 2 and a non-zero column", err)
	}
}

// An iterator aggregate participates in its cluster's group context: SUMX
// over one fact aggregates per group cell inside that cluster's CTE, not as
// an uncorrelated whole-table subquery.
func TestStitched_IteratorAggregateInCluster(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		"SoldX",    SUMX(fact_sales, fact_sales[qty] * 2),
		"Returned", SUM(fact_returns[rqty])
	)`)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(rows), rows)
	}
	r2024 := rowByKey(t, rows, "year", 2024)
	r2025 := rowByKey(t, rows, "year", 2025)
	if got := toFloat(cell(t, r2024, "SoldX")); got != 70 {
		t.Errorf("2024 SoldX = %v, want 70 (whole-table subquery would give 84)", got)
	}
	if got := toFloat(cell(t, r2025, "SoldX")); got != 14 {
		t.Errorf("2025 SoldX = %v, want 14", got)
	}
	if got := toFloat(cell(t, r2024, "Returned")); got != 6 {
		t.Errorf("2024 Returned = %v, want 6", got)
	}
}

// A FILTER(table, pred) argument on the shared dimension routes into both
// cluster CTEs, exactly like a TREATAS filter.
func TestStitched_FilterArgReachesAllClusters(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		FILTER(dates, dates[year] >= 2025),
		"Sold",     SUM(fact_sales[qty]),
		"Returned", SUM(fact_returns[rqty])
	)`)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (2025 only), got %d: %v", len(rows), rows)
	}
	if got := toFloat(cell(t, rows[0], "Sold")); got != 7 {
		t.Errorf("Sold = %v, want 7", got)
	}
	if got := toFloat(cell(t, rows[0], "Returned")); got != 4 {
		t.Errorf("Returned = %v, want 4", got)
	}
}

// A TREATAS filter on the shared dimension must gate BOTH clusters.
func TestStitched_SharedDimFilterReachesAllClusters(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		TREATAS({2024}, dates[year]),
		"Sold",     SUM(fact_sales[qty]),
		"Returned", SUM(fact_returns[rqty])
	)`)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (2024 only), got %d: %v", len(rows), rows)
	}
	if got := toFloat(cell(t, rows[0], "Sold")); got != 35 {
		t.Errorf("Sold = %v, want 35", got)
	}
	if got := toFloat(cell(t, rows[0], "Returned")); got != 6 {
		t.Errorf("Returned = %v, want 6", got)
	}
}

// A TREATAS filter on a dimension related only to fact_sales must gate the
// sales cluster and leave fact_returns untouched. Filters do NOT propagate
// through an unrelated fact (products → fact_sales ↛ dates ↛ fact_returns).
func TestStitched_FilterPropagatesToOwnClusterOnly(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		TREATAS({"A"}, products[pname]),
		"Sold",     SUM(fact_sales[qty]),
		"Returned", SUM(fact_returns[rqty])
	)`)
	// Product A sales: datekey1 qty=10, datekey2 qty=5 → 2024: 15; no 2025
	// sales survive the filter, but the returns cluster still has 2025.
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d: %v", len(rows), rows)
	}
	r2024 := rowByKey(t, rows, "year", 2024)
	r2025 := rowByKey(t, rows, "year", 2025)
	if got := toFloat(cell(t, r2024, "Sold")); got != 15 {
		t.Errorf("2024 Sold = %v, want 15 (product A only)", got)
	}
	if got := toFloat(cell(t, r2024, "Returned")); got != 6 {
		t.Errorf("2024 Returned = %v, want 6 (unaffected by product filter)", got)
	}
	if v := cell(t, r2025, "Sold"); v != nil {
		t.Errorf("2025 Sold = %v, want NULL (no product-A sales in 2025)", v)
	}
	if got := toFloat(cell(t, r2025, "Returned")); got != 4 {
		t.Errorf("2025 Returned = %v, want 4", got)
	}
}

// Measures with no group columns: each cluster is a one-row aggregate and the
// stitch is a single combined row.
func TestStitched_NoGroupKeys(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		"Sold",     SUM(fact_sales[qty]),
		"Returned", SUM(fact_returns[rqty])
	)`)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %v", len(rows), rows)
	}
	if got := toFloat(cell(t, rows[0], "Sold")); got != 42 {
		t.Errorf("Sold = %v, want 42", got)
	}
	if got := toFloat(cell(t, rows[0], "Returned")); got != 10 {
		t.Errorf("Returned = %v, want 10", got)
	}
}

// A scalar measure alongside multi-table measures emits in the outer SELECT.
func TestStitched_ScalarMeasurePassthrough(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		"Sold",     SUM(fact_sales[qty]),
		"Returned", SUM(fact_returns[rqty]),
		"Two",      1 + 1
	)`)
	r2024 := rowByKey(t, rows, "year", 2024)
	if got := toFloat(cell(t, r2024, "Two")); got != 2 {
		t.Errorf("Two = %v, want 2", got)
	}
}

// EVALUATE ... ORDER BY must compose over the stitched (CTE-shaped) query.
func TestStitched_OrderByComposition(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		"Sold",     SUM(fact_sales[qty]),
		"Returned", SUM(fact_returns[rqty])
	) ORDER BY [Sold] DESC`)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if got := toFloat(cell(t, rows[0], "Sold")); got != 35 {
		t.Errorf("rows[0].Sold = %v, want 35 (DESC order)", got)
	}
	if got := toFloat(cell(t, rows[1], "Sold")); got != 7 {
		t.Errorf("rows[1].Sold = %v, want 7", got)
	}
}

// A NULL group-key value must stitch into ONE row across clusters
// (IS NOT DISTINCT FROM join), not one row per side of the FULL JOIN.
func TestStitched_NullGroupKeyStitchesOnce(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	for _, s := range []string{
		`INSERT INTO dates VALUES (4, NULL)`,
		`INSERT INTO fact_sales VALUES (4, 100, 1)`,
		`INSERT INTO fact_returns VALUES (4, 50)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		"Sold",     SUM(fact_sales[qty]),
		"Returned", SUM(fact_returns[rqty])
	)`)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows (2024, 2025, NULL), got %d: %v", len(rows), rows)
	}
	var nullRow map[string]any
	for _, r := range rows {
		if r["year"] == nil {
			if nullRow != nil {
				t.Fatalf("NULL year stitched into more than one row: %v", rows)
			}
			nullRow = r
		}
	}
	if nullRow == nil {
		t.Fatalf("no NULL-year row found: %v", rows)
	}
	if got := toFloat(cell(t, nullRow, "Sold")); got != 100 {
		t.Errorf("NULL-year Sold = %v, want 100", got)
	}
	if got := toFloat(cell(t, nullRow, "Returned")); got != 50 {
		t.Errorf("NULL-year Returned = %v, want 50", got)
	}
}

// Measure names with spaces survive the CTE round-trip.
func TestStitched_MeasureNameWithSpaces(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		"Total Sold",     SUM(fact_sales[qty]),
		"Total Returned", SUM(fact_returns[rqty])
	)`)
	r2024 := rowByKey(t, rows, "year", 2024)
	if got := toFloat(cell(t, r2024, "Total Sold")); got != 35 {
		t.Errorf("Total Sold = %v, want 35", got)
	}
	if got := toFloat(cell(t, r2024, "Total Returned")); got != 6 {
		t.Errorf("Total Returned = %v, want 6", got)
	}
}

// A single expression spanning two facts: each aggregate computes in its own
// cluster and the arithmetic happens over the stitched columns.
func TestStitched_CrossClusterExpression(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		"NetQty", SUM(fact_sales[qty]) - SUM(fact_returns[rqty]),
		"Ratio",  DIVIDE(SUM(fact_sales[qty]), SUM(fact_returns[rqty]))
	)`)
	r2024 := rowByKey(t, rows, "year", 2024)
	r2025 := rowByKey(t, rows, "year", 2025)
	if got := toFloat(cell(t, r2024, "NetQty")); got != 29 {
		t.Errorf("2024 NetQty = %v, want 29 (35-6; fan-out would give 56)", got)
	}
	if got := toFloat(cell(t, r2025, "NetQty")); got != 3 {
		t.Errorf("2025 NetQty = %v, want 3 (7-4)", got)
	}
	if got := toFloat(cell(t, r2024, "Ratio")); got < 5.83 || got > 5.84 {
		t.Errorf("2024 Ratio = %v, want ~5.833 (35/6)", got)
	}
}

// A stored measure whose expression spans two facts splits transparently.
func TestStitched_CrossClusterStoredMeasure(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	addMeasure(t, schema, "fact_sales", "Net", "SUM(fact_sales[qty]) - SUM(fact_returns[rqty])")
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		"NetOut", fact_sales[Net],
		"Sold",   SUM(fact_sales[qty])
	)`)
	r2024 := rowByKey(t, rows, "year", 2024)
	if got := toFloat(cell(t, r2024, "NetOut")); got != 29 {
		t.Errorf("2024 NetOut = %v, want 29", got)
	}
	if got := toFloat(cell(t, r2024, "Sold")); got != 35 {
		t.Errorf("2024 Sold = %v, want 35", got)
	}
}

// Measure references in the group position participate in clustering and are
// emitted with the measure's own name as the output column.
func TestStitched_InlineMeasureInGroupPosition(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	addMeasure(t, schema, "fact_returns", "Total Returned", "SUM(fact_returns[rqty])")
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		fact_returns[Total Returned],
		"Sold", SUM(fact_sales[qty])
	)`)
	r2024 := rowByKey(t, rows, "year", 2024)
	if got := toFloat(cell(t, r2024, "Total Returned")); got != 6 {
		t.Errorf("2024 Total Returned = %v, want 6", got)
	}
	if got := toFloat(cell(t, r2024, "Sold")); got != 35 {
		t.Errorf("2024 Sold = %v, want 35", got)
	}
}

// CALCULATE with a plain predicate (fast path: aggregate FILTER clause)
// inside a multi-table query gates only its own cluster.
func TestStitched_CalculatePlainPredicate(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		"BigSold",  CALCULATE(SUM(fact_sales[qty]), fact_sales[qty] > 5),
		"Returned", SUM(fact_returns[rqty])
	)`)
	r2024 := rowByKey(t, rows, "year", 2024)
	r2025 := rowByKey(t, rows, "year", 2025)
	if got := toFloat(cell(t, r2024, "BigSold")); got != 30 {
		t.Errorf("2024 BigSold = %v, want 30 (10+20, qty 5 excluded)", got)
	}
	if got := toFloat(cell(t, r2024, "Returned")); got != 6 {
		t.Errorf("2024 Returned = %v, want 6", got)
	}
	if got := toFloat(cell(t, r2025, "BigSold")); got != 7 {
		t.Errorf("2025 BigSold = %v, want 7", got)
	}
}

// CALCULATE(..., ALL(dates[year])) removes the group filter within its own
// cluster: the grand total repeats on every row while other measures group.
func TestStitched_CalculateAllRemovesGroupFilter(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		"AllSold",  CALCULATE(SUM(fact_sales[qty]), ALL(dates[year])),
		"Returned", SUM(fact_returns[rqty])
	)`)
	r2024 := rowByKey(t, rows, "year", 2024)
	r2025 := rowByKey(t, rows, "year", 2025)
	if got := toFloat(cell(t, r2024, "AllSold")); got != 42 {
		t.Errorf("2024 AllSold = %v, want 42 (grand total)", got)
	}
	if got := toFloat(cell(t, r2025, "AllSold")); got != 42 {
		t.Errorf("2025 AllSold = %v, want 42 (grand total)", got)
	}
	if got := toFloat(cell(t, r2025, "Returned")); got != 4 {
		t.Errorf("2025 Returned = %v, want 4", got)
	}
}

// A TREATAS filter routed to a cluster is cleared by ALL inside CALCULATE for
// that measure only; the other cluster's measure keeps the filter.
func TestStitched_CalculateClearsRoutedFilter(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		TREATAS({2024}, dates[year]),
		"AllSold",  CALCULATE(SUM(fact_sales[qty]), ALL(dates[year])),
		"Returned", SUM(fact_returns[rqty])
	)`)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (2024), got %d: %v", len(rows), rows)
	}
	if got := toFloat(cell(t, rows[0], "AllSold")); got != 42 {
		t.Errorf("AllSold = %v, want 42 (year filter cleared)", got)
	}
	if got := toFloat(cell(t, rows[0], "Returned")); got != 6 {
		t.Errorf("Returned = %v, want 6 (year filter kept)", got)
	}
}

// Time intelligence alongside a second fact: TOTALYTD's anchor subqueries
// correlate inside its own cluster CTE; the refunds cluster is independent.
func TestStitched_TimeIntelligenceAcrossFacts(t *testing.T) {
	db, schema := setupTimeDB(t)
	for _, s := range []string{
		`CREATE TABLE refunds (refund_date DATE, ramount DOUBLE)`,
		`INSERT INTO refunds VALUES
			(DATE '2023-02-20', 5.0), (DATE '2023-03-15', 7.0), (DATE '2024-01-25', 11.0)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("setup refunds: %v", err)
		}
	}
	var err error
	schema2, err := semantic.IntrospectDuckDB(db)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	schema2.Relationships = append(schema.Relationships, &semantic.Relationship{
		FromTable: "refunds", FromColumn: "refund_date", ToTable: "dates", ToColumn: "date",
	})
	schema2.SetDateTable("dates", "date")

	_, rows := run(t, db, schema2, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		dates[month],
		"YTD",     TOTALYTD(SUM(orders[amount]), dates[date]),
		"Refunds", SUM(refunds[ramount])
	)`)
	feb23 := monthRow(t, rows, 2023, 2)
	mar23 := monthRow(t, rows, 2023, 3)
	jan24 := monthRow(t, rows, 2024, 1)
	if got := toFloat(cell(t, feb23, "YTD")); got != 30 {
		t.Errorf("2023-02 YTD = %v, want 30 (10+20)", got)
	}
	if got := toFloat(cell(t, feb23, "Refunds")); got != 5 {
		t.Errorf("2023-02 Refunds = %v, want 5", got)
	}
	if got := toFloat(cell(t, mar23, "YTD")); got != 60 {
		t.Errorf("2023-03 YTD = %v, want 60 (10+20+30)", got)
	}
	if got := toFloat(cell(t, mar23, "Refunds")); got != 7 {
		t.Errorf("2023-03 Refunds = %v, want 7", got)
	}
	if got := toFloat(cell(t, jan24, "YTD")); got != 100 {
		t.Errorf("2024-01 YTD = %v, want 100 (YTD resets)", got)
	}
	if got := toFloat(cell(t, jan24, "Refunds")); got != 11 {
		t.Errorf("2024-01 Refunds = %v, want 11", got)
	}
}

// ROLLUPADDISSUBTOTAL over two facts: subtotal rows from each cluster must
// pair with the matching subtotal level, never with detail rows.
func TestStitched_RollupSubtotals(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		ROLLUPADDISSUBTOTAL(dates[year], "IsTotal"),
		"Sold",     SUM(fact_sales[qty]),
		"Returned", SUM(fact_returns[rqty])
	)`)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows (2024, 2025, grand total), got %d: %v", len(rows), rows)
	}
	var total map[string]any
	for _, r := range rows {
		if r["IsTotal"] == true {
			if total != nil {
				t.Fatalf("more than one grand-total row: %v", rows)
			}
			total = r
		}
	}
	if total == nil {
		t.Fatalf("no grand-total row found: %v", rows)
	}
	if got := toFloat(cell(t, total, "Sold")); got != 42 {
		t.Errorf("grand total Sold = %v, want 42", got)
	}
	if got := toFloat(cell(t, total, "Returned")); got != 10 {
		t.Errorf("grand total Returned = %v, want 10", got)
	}
	r2024 := rowByKey(t, rows, "year", 2024)
	if got := toFloat(cell(t, r2024, "Sold")); got != 35 {
		t.Errorf("2024 Sold = %v, want 35", got)
	}
	if got, want := cell(t, r2024, "IsTotal"), false; got != want {
		t.Errorf("2024 IsTotal = %v, want false", got)
	}
}

// Hierarchical rollup (year → month) across two facts: each grouping level
// stitches independently.
func TestStitched_RollupHierarchyAcrossFacts(t *testing.T) {
	db, schema := setupTimeDB(t)
	for _, s := range []string{
		`CREATE TABLE refunds (refund_date DATE, ramount DOUBLE)`,
		`INSERT INTO refunds VALUES
			(DATE '2023-02-20', 5.0), (DATE '2023-03-15', 7.0), (DATE '2024-01-25', 11.0)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("setup refunds: %v", err)
		}
	}
	schema2, err := semantic.IntrospectDuckDB(db)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	schema2.Relationships = append(schema.Relationships, &semantic.Relationship{
		FromTable: "refunds", FromColumn: "refund_date", ToTable: "dates", ToColumn: "date",
	})

	_, rows := run(t, db, schema2, `EVALUATE SUMMARIZECOLUMNS(
		ROLLUPADDISSUBTOTAL(dates[year], "YearTotal", dates[month], "MonthTotal"),
		"Orders",  SUM(orders[amount]),
		"Refunds", SUM(refunds[ramount])
	)`)

	// Year-subtotal rows: MonthTotal=true, YearTotal=false.
	var y2023 map[string]any
	var grand map[string]any
	for _, r := range rows {
		switch {
		case r["YearTotal"] == true:
			grand = r
		case r["MonthTotal"] == true && toFloat(r["year"]) == 2023:
			y2023 = r
		}
	}
	if y2023 == nil {
		t.Fatalf("no 2023 year-subtotal row: %v", rows)
	}
	if got := toFloat(cell(t, y2023, "Orders")); got != 60 {
		t.Errorf("2023 subtotal Orders = %v, want 60", got)
	}
	if got := toFloat(cell(t, y2023, "Refunds")); got != 12 {
		t.Errorf("2023 subtotal Refunds = %v, want 12", got)
	}
	if grand == nil {
		t.Fatalf("no grand-total row: %v", rows)
	}
	if got := toFloat(cell(t, grand, "Orders")); got != 660 {
		t.Errorf("grand total Orders = %v, want 660", got)
	}
	if got := toFloat(cell(t, grand, "Refunds")); got != 23 {
		t.Errorf("grand total Refunds = %v, want 23", got)
	}
}

// setupBidiDB builds the many-to-many bridge fixture:
//
//	dima(1 X, 2 Y, 3 Z) ← bridge ↔ dimb(10, 20) ← factmeasures
//	bridge: (1,10), (2,10), (3,20) — dimb 10 relates to BOTH dima 1 and 2.
func setupBidiDB(t *testing.T) (*sql.DB, *semantic.Schema) {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	for _, s := range []string{
		`CREATE TABLE dima (dimakey INTEGER, category VARCHAR)`,
		`INSERT INTO dima VALUES (1, 'X'), (2, 'Y'), (3, 'Z')`,
		`CREATE TABLE bridge (dimakey INTEGER, dimbkey INTEGER)`,
		`INSERT INTO bridge VALUES (1, 10), (2, 10), (3, 20)`,
		`CREATE TABLE dimb (dimbkey INTEGER, name VARCHAR)`,
		`INSERT INTO dimb VALUES (10, 'B10'), (20, 'B20')`,
		`CREATE TABLE factmeasures (dimbkey INTEGER, amount DOUBLE)`,
		`INSERT INTO factmeasures VALUES (10, 100.0), (10, 50.0), (20, 7.0)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("setup: %v — %s", err, s)
		}
	}
	schema, err := semantic.IntrospectDuckDB(db)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	schema.Relationships = append(schema.Relationships,
		&semantic.Relationship{FromTable: "bridge", FromColumn: "dimakey", ToTable: "dima", ToColumn: "dimakey"},
		&semantic.Relationship{FromTable: "bridge", FromColumn: "dimbkey", ToTable: "dimb", ToColumn: "dimbkey", Bidirectional: true},
		&semantic.Relationship{FromTable: "factmeasures", FromColumn: "dimbkey", ToTable: "dimb", ToColumn: "dimbkey"},
	)
	return db, schema
}

// The EXISTS semi-join must gate fact rows WITHOUT duplicating them when the
// filter matches more than one bridge row (dimb 10 ↔ dima {X, Y}).
func TestStitched_BidiSemiJoinNoFanOut(t *testing.T) {
	db, schema := setupBidiDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		TREATAS({"X","Y"}, dima[category]),
		"Total", SUM(factmeasures[amount])
	)`)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d: %v", len(rows), rows)
	}
	if got := toFloat(cell(t, rows[0], "Total")); got != 150 {
		t.Errorf("Total = %v, want 150 (dimb 10 counted once; a flat join through 2 bridge rows would give 300)", got)
	}
}

// Grouped by the bidi target: only gated dimension rows survive, with
// per-group sums intact.
func TestStitched_BidiFullChainValues(t *testing.T) {
	db, schema := setupBidiDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dimb[name],
		TREATAS({"X"}, dima[category]),
		"Total", SUM(factmeasures[amount])
	)`)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (B10 only), got %d: %v", len(rows), rows)
	}
	if got := cell(t, rows[0], "name"); got != "B10" {
		t.Errorf("name = %v, want B10", got)
	}
	if got := toFloat(cell(t, rows[0], "Total")); got != 150 {
		t.Errorf("Total = %v, want 150", got)
	}
}

// A filter unrelated to every measure is an error, not a silent no-op.
func TestStitched_UnroutableFilterErrors(t *testing.T) {
	db, schema := setupMultiTableDB(t)
	// products only reaches fact_sales; with measures over fact_returns and
	// dates there is no cluster it can gate... build one measuring returns
	// and the dimension itself.
	_, _, err := executorExecute(db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[year],
		TREATAS({"A"}, products[pname]),
		"Returned",  SUM(fact_returns[rqty]),
		"YearCount", COUNT(dates[datekey])
	)`)
	if err == nil {
		t.Fatalf("expected an error for a filter unrelated to every measure")
	}
	if !strings.Contains(err.Error(), "unrelated") {
		t.Errorf("error should mention the unrelated filter, got: %v", err)
	}
}
