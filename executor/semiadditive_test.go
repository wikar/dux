package executor_test

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/danielwikar/dux/semantic"
)

func setupSemiAdditiveDB(t *testing.T) (*sql.DB, *semantic.Schema) {
	t.Helper()
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ddl := []string{
		`CREATE TABLE dates AS
			SELECT CAST(generate_series AS DATE) AS dt,
			       CAST(strftime(generate_series, '%Y%m') AS INTEGER) AS ymon
			FROM generate_series(DATE '2024-01-01', DATE '2024-02-29', INTERVAL 1 DAY)`,
		`CREATE TABLE products (pkey INTEGER, pname VARCHAR)`,
		`INSERT INTO products VALUES (1, 'A'), (2, 'B')`,
		`CREATE TABLE venues (vkey INTEGER, vname VARCHAR)`,
		`INSERT INTO venues VALUES (1, 'Retail'), (2, 'Wholesale')`,
		`CREATE TABLE sales AS
			SELECT d.dt AS d, p.pkey, v.vkey,
			       CAST((p.pkey - 1) * 2 + v.vkey AS INTEGER) AS amount
			FROM dates d CROSS JOIN products p CROSS JOIN venues v`,
		// A snapshot need not share a physical date with a sales row. This also
		// prevents LASTNONBLANK's candidate scan from taking a sibling-fact path.
		`DELETE FROM sales WHERE d = DATE '2024-02-25' AND pkey = 2 AND vkey = 2`,
		`CREATE TABLE stock (d DATE, pkey INTEGER, vkey INTEGER, qty INTEGER)`,
		`INSERT INTO stock VALUES
			(DATE '2024-01-07', 1, 1, 10), (DATE '2024-01-14', 1, 1, 12),
			(DATE '2024-01-21', 1, 1, 14), (DATE '2024-01-28', 1, 1, 16),
			(DATE '2024-01-07', 1, 2, 20), (DATE '2024-01-14', 1, 2, 22),
			(DATE '2024-01-21', 1, 2, 24), (DATE '2024-01-28', 1, 2, 26),
			(DATE '2024-01-07', 2, 1, 100), (DATE '2024-01-14', 2, 1, 120),
			(DATE '2024-01-21', 2, 1, 140), (DATE '2024-01-28', 2, 1, 160),
			(DATE '2024-01-07', 2, 2, 200), (DATE '2024-01-14', 2, 2, 220),
			(DATE '2024-01-21', 2, 2, 240), (DATE '2024-01-28', 2, 2, 260),
			(DATE '2024-02-04', 1, 1, 17), (DATE '2024-02-11', 1, 1, 19),
			(DATE '2024-02-18', 1, 1, 21), (DATE '2024-02-25', 1, 1, 23),
			(DATE '2024-02-04', 1, 2, 27), (DATE '2024-02-11', 1, 2, 29),
			(DATE '2024-02-18', 1, 2, 31), (DATE '2024-02-25', 1, 2, 33),
			(DATE '2024-02-04', 2, 1, 170), (DATE '2024-02-11', 2, 1, 190),
			(DATE '2024-02-18', 2, 1, 210), (DATE '2024-02-25', 2, 1, 230),
			(DATE '2024-02-04', 2, 2, 270), (DATE '2024-02-11', 2, 2, 290),
			(DATE '2024-02-18', 2, 2, 310), (DATE '2024-02-25', 2, 2, 330)`,
	}
	for _, statement := range ddl {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("setup: %v — %s", err, statement)
		}
	}

	schema, err := semantic.IntrospectDuckDB(db)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}
	schema.Relationships = append(schema.Relationships,
		&semantic.Relationship{FromTable: "sales", FromColumn: "d", ToTable: "dates", ToColumn: "dt"},
		&semantic.Relationship{FromTable: "sales", FromColumn: "pkey", ToTable: "products", ToColumn: "pkey"},
		&semantic.Relationship{FromTable: "sales", FromColumn: "vkey", ToTable: "venues", ToColumn: "vkey"},
		&semantic.Relationship{FromTable: "stock", FromColumn: "d", ToTable: "dates", ToColumn: "dt"},
		&semantic.Relationship{FromTable: "stock", FromColumn: "pkey", ToTable: "products", ToColumn: "pkey"},
		&semantic.Relationship{FromTable: "stock", FromColumn: "vkey", ToTable: "venues", ToColumn: "vkey"},
	)
	schema.SetDateTable("dates", "dt")
	addMeasure(t, schema, "sales", "SalesAmount", `SUM(sales[amount])`)
	addMeasure(t, schema, "stock", "RawStockQuantity", `SUM(stock[qty])`)
	addMeasure(t, schema, "stock", "StockQuantity", `CALCULATE(
		stock[RawStockQuantity],
		DATESINPERIOD(dates[dt], LASTNONBLANK(dates[dt], stock[RawStockQuantity]), 1, DAY))`)
	addMeasure(t, schema, "stock", "FactAnchoredStock", `CALCULATE(
		SUM(stock[qty]),
		DATESINPERIOD(stock[d], MAX(stock[d]), 1, DAY))`)
	return db, schema
}

func semiCell(t *testing.T, rows []map[string]any, month int, product, venue string) map[string]any {
	t.Helper()
	for _, row := range rows {
		if int(toFloat(row["ymon"])) == month && fmt.Sprint(row["pname"]) == product && fmt.Sprint(row["vname"]) == venue {
			return row
		}
	}
	t.Fatalf("no row for %d/%s/%s in %v", month, product, venue, rows)
	return nil
}

func TestDailySalesAndMonthlyClosingStockUseConformedDimensions(t *testing.T) {
	db, schema := setupSemiAdditiveDB(t)
	_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
		dates[ymon], products[pname], venues[vname],
		"Sales", [SalesAmount],
		"Stock", [StockQuantity],
		"Fact Stock", [FactAnchoredStock]
	)`)

	wants := []struct {
		month          int
		product, venue string
		sales, stock   float64
	}{
		{202401, "A", "Retail", 31, 16}, {202402, "A", "Retail", 29, 23},
		{202401, "A", "Wholesale", 62, 26}, {202402, "A", "Wholesale", 58, 33},
		{202401, "B", "Retail", 93, 160}, {202402, "B", "Retail", 87, 230},
		{202401, "B", "Wholesale", 124, 260}, {202402, "B", "Wholesale", 112, 330},
	}
	for _, want := range wants {
		row := semiCell(t, rows, want.month, want.product, want.venue)
		if got := toFloat(cell(t, row, "Sales")); got != want.sales {
			t.Errorf("%d/%s/%s Sales: want %v, got %v", want.month, want.product, want.venue, want.sales, got)
		}
		for _, measure := range []string{"Stock", "Fact Stock"} {
			if got := toFloat(cell(t, row, measure)); got != want.stock {
				t.Errorf("%d/%s/%s %s: want %v, got %v", want.month, want.product, want.venue, measure, want.stock, got)
			}
		}
	}
}

func TestDailySalesAndClosingStockGrandTotals(t *testing.T) {
	db, schema := setupSemiAdditiveDB(t)
	_, rows := run(t, db, schema, `EVALUATE ROW(
		"Sales", [SalesAmount],
		"Stock", [StockQuantity],
		"Fact Stock", [FactAnchoredStock]
	)`)
	if got := toFloat(cell(t, rows[0], "Sales")); got != 596 {
		t.Fatalf("Sales: want 596, got %v", got)
	}
	for _, measure := range []string{"Stock", "Fact Stock"} {
		if got := toFloat(cell(t, rows[0], measure)); got != 616 {
			t.Errorf("%s: want 616, got %v", measure, got)
		}
	}
}
