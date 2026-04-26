package executor_test

import (
	"database/sql"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/danielwikar/dux/executor"
	"github.com/danielwikar/dux/semantic"
)

// ─── test fixture ─────────────────────────────────────────────────────────────

// setupTestDB opens an in-memory DuckDB, creates two tables with seed data,
// introspects them into a Schema, and adds a relationship + a named measure.
// The returned cleanup function must be called by the test (usually via t.Cleanup).
func setupTestDB(t *testing.T) (*sql.DB, *semantic.Schema) {
	t.Helper()

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ddl := []string{
		`CREATE TABLE sales (
			id      INTEGER,
			product VARCHAR,
			amount  DOUBLE,
			qty     INTEGER,
			region  VARCHAR
		)`,
		`INSERT INTO sales VALUES
			(1, 'Widget', 100.0, 2, 'North'),
			(2, 'Widget', 150.0, 3, 'South'),
			(3, 'Gadget', 200.0, 1, 'North'),
			(4, 'Gadget', 250.0, 4, 'South'),
			(5, 'Doohickey', 50.0,  1, 'North'),
			(6, 'Doohickey', 75.0,  2, 'South')`,
		`CREATE TABLE products (
			product  VARCHAR,
			category VARCHAR
		)`,
		`INSERT INTO products VALUES
			('Widget',    'Electronics'),
			('Gadget',    'Electronics'),
			('Doohickey', 'Misc')`,
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

	schema.Relationships = append(schema.Relationships, &semantic.Relationship{
		FromTable:  "sales",
		FromColumn: "product",
		ToTable:    "products",
		ToColumn:   "product",
	})

	return db, schema
}

// run executes a DUX query and returns (columns, rows, error).
func run(t *testing.T, db *sql.DB, schema *semantic.Schema, dux string) ([]string, []map[string]any) {
	t.Helper()
	cols, rows, err := executor.Execute(db, schema, dux)
	if err != nil {
		t.Fatalf("Execute(%q): %v", dux, err)
	}
	return cols, rows
}

func cell(t *testing.T, row map[string]any, col string) any {
	t.Helper()
	v, ok := row[col]
	if !ok {
		t.Fatalf("column %q not found in row", col)
	}
	return v
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case int:
		return float64(n)
	}
	return 0
}

// ─── Aggregation ─────────────────────────────────────────────────────────────

func TestExecute_Aggregation(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("SUM", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "Total", SUM(sales[amount]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
	})

	t.Run("AVERAGE", func(t *testing.T) {
		cols, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "Avg", AVERAGE(sales[amount]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		if len(cols) < 2 {
			t.Fatalf("expected at least 2 columns, got %d", len(cols))
		}
	})

	t.Run("COUNT", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "N", COUNT(sales[id]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
	})

	t.Run("COUNTROWS", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "Rows", COUNTROWS(sales))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		// Each region has exactly 3 rows
		for _, row := range rows {
			v := toFloat(cell(t, row, "Rows"))
			if v != 3 {
				t.Errorf("expected 3 rows per region, got %v", v)
			}
		}
	})

	t.Run("DISTINCTCOUNT", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "DC", DISTINCTCOUNT(sales[product]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		// Each region has 3 distinct products
		for _, row := range rows {
			v := toFloat(cell(t, row, "DC"))
			if v != 3 {
				t.Errorf("expected 3 distinct products, got %v", v)
			}
		}
	})

	t.Run("MIN_MAX", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "Min", MIN(sales[amount]), "Max", MAX(sales[amount]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
	})

	t.Run("MEDIAN", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "Med", MEDIAN(sales[amount]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
	})
}

// ─── Iterator functions ───────────────────────────────────────────────────────

func TestExecute_Iterators(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("SUMX", func(t *testing.T) {
		// Iterator subqueries run over the whole table (no group-context push-down).
		// Both North and South will therefore show the table-wide total of 2050.
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "Rev", SUMX(sales, sales[amount] * sales[qty]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		for _, row := range rows {
			v := toFloat(row["Rev"])
			if v != 2050 {
				t.Errorf("expected table-wide SUMX total 2050, got %v", v)
			}
		}
	})

	t.Run("AVERAGEX", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "AvgRev", AVERAGEX(sales, sales[amount] * sales[qty]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
	})

	t.Run("COUNTX", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "N", COUNTX(sales, sales[id]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
	})

	t.Run("MINX", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "MinRev", MINX(sales, sales[amount] * sales[qty]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
	})

	t.Run("MAXX", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "MaxRev", MAXX(sales, sales[amount] * sales[qty]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
	})
}

// ─── Table operations ─────────────────────────────────────────────────────────

func TestExecute_TableOps(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("SUMMARIZECOLUMNS", func(t *testing.T) {
		cols, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "Total", SUM(sales[amount]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d: %v", len(rows), rows)
		}
		if len(cols) != 2 {
			t.Fatalf("expected 2 columns, got %v", cols)
		}
	})

	t.Run("SUMMARIZECOLUMNS_MultiTable", func(t *testing.T) {
		cols, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(products[category], "Total", SUM(sales[amount]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows (Electronics, Misc), got %d", len(rows))
		}
		if len(cols) != 2 {
			t.Fatalf("expected 2 columns, got %v", cols)
		}
	})

	t.Run("ADDCOLUMNS", func(t *testing.T) {
		cols, rows := run(t, db, schema, `EVALUATE ADDCOLUMNS(sales, "Revenue", sales[amount] * sales[qty])`)
		if len(rows) != 6 {
			t.Fatalf("expected 6 rows, got %d", len(rows))
		}
		found := false
		for _, c := range cols {
			if c == "Revenue" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected column 'Revenue', got %v", cols)
		}
	})

	t.Run("SELECTCOLUMNS", func(t *testing.T) {
		cols, rows := run(t, db, schema, `EVALUATE SELECTCOLUMNS(sales, "Reg", sales[region], "Amt", sales[amount])`)
		if len(rows) != 6 {
			t.Fatalf("expected 6 rows, got %d", len(rows))
		}
		if len(cols) != 2 {
			t.Fatalf("expected 2 columns, got %v", cols)
		}
	})

	t.Run("FILTER", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE FILTER(sales, sales[region] = "North")`)
		if len(rows) != 3 {
			t.Fatalf("expected 3 North rows, got %d", len(rows))
		}
	})

	t.Run("TOPN", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE TOPN(3, sales, sales[amount])`)
		if len(rows) != 3 {
			t.Fatalf("expected 3 rows, got %d", len(rows))
		}
		// First row should be highest amount (250)
		if toFloat(rows[0]["amount"]) != 250 {
			t.Errorf("expected top row amount=250, got %v", rows[0]["amount"])
		}
	})

	t.Run("UNION", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE UNION(FILTER(sales, sales[region] = "North"), FILTER(sales, sales[region] = "South"))`)
		if len(rows) != 6 {
			t.Fatalf("expected 6 rows (3+3), got %d", len(rows))
		}
	})

	t.Run("EXCEPT", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE EXCEPT(FILTER(sales, sales[region] = "North"), FILTER(sales, sales[region] = "South"))`)
		if len(rows) != 3 {
			t.Fatalf("expected 3 North rows after EXCEPT South, got %d", len(rows))
		}
	})
}

// ─── Filter context ───────────────────────────────────────────────────────────

func TestExecute_FilterContext(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("TREATAS_Literal", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			TREATAS({"North"}, sales[region]),
			"Total", SUM(sales[amount])
		)`)
		if len(rows) != 1 {
			t.Fatalf("expected 1 row (North only), got %d", len(rows))
		}
		region, _ := rows[0]["region"].(string)
		if region != "North" {
			t.Errorf("expected region=North, got %q", region)
		}
	})

	t.Run("TREATAS_Multi", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[product],
			TREATAS({"Widget", "Gadget"}, sales[product]),
			"Total", SUM(sales[amount])
		)`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
	})

	t.Run("VALUES", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE VALUES(sales[region])`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 distinct regions, got %d", len(rows))
		}
	})

	t.Run("DISTINCT", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE DISTINCT(sales[region])`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 distinct regions, got %d", len(rows))
		}
	})
}

// ─── Scalar / logical ─────────────────────────────────────────────────────────

func TestExecute_ScalarLogical(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("DIVIDE_safe", func(t *testing.T) {
		// DIVIDE should not error on valid division
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "Ratio", DIVIDE(SUM(sales[amount]), COUNT(sales[id])))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
	})

	t.Run("IF", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SELECTCOLUMNS(sales, "Label", IF(sales[amount] > 100, "High", "Low"))`)
		if len(rows) != 6 {
			t.Fatalf("expected 6 rows, got %d", len(rows))
		}
	})

	t.Run("SWITCH", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SELECTCOLUMNS(sales, "Cat", SWITCH(sales[region], "North", "N", "South", "S", "Other"))`)
		if len(rows) != 6 {
			t.Fatalf("expected 6 rows, got %d", len(rows))
		}
	})

	t.Run("ISBLANK", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE FILTER(sales, NOT(ISBLANK(sales[region])))`)
		if len(rows) != 6 {
			t.Fatalf("expected 6 non-blank rows, got %d", len(rows))
		}
	})

	t.Run("NOT", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE FILTER(sales, NOT(sales[region] = "North"))`)
		if len(rows) != 3 {
			t.Fatalf("expected 3 South rows, got %d", len(rows))
		}
	})

	t.Run("AND_infix", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE FILTER(sales, sales[region] = "North" AND sales[amount] > 100)`)
		if len(rows) != 1 {
			t.Fatalf("expected 1 row (North, amount>100), got %d", len(rows))
		}
	})

	t.Run("OR_infix", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE FILTER(sales, sales[region] = "North" OR sales[product] = "Widget")`)
		// North (3) + South Widget (1) = 4 rows (Widget North already counted)
		if len(rows) != 4 {
			t.Fatalf("expected 4 rows, got %d", len(rows))
		}
	})
}

// ─── VAR / RETURN ─────────────────────────────────────────────────────────────

func TestExecute_VAR(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("TableVAR", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE
			VAR north_sales = FILTER(sales, sales[region] = "North")
			RETURN SUMMARIZECOLUMNS(north_sales[product], "Total", SUM(north_sales[amount]))`)
		if len(rows) != 3 {
			t.Fatalf("expected 3 products in North, got %d", len(rows))
		}
	})
}

// ─── Error cases ──────────────────────────────────────────────────────────────

func TestExecute_Errors(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("ParseError", func(t *testing.T) {
		_, _, err := executor.Execute(db, schema, `NOT VALID DUX`)
		if err == nil {
			t.Fatal("expected parse error")
		}
	})

	t.Run("SUMMARIZECOLUMNS_OddPairs", func(t *testing.T) {
		_, _, err := executor.Execute(db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "Orphan")`)
		if err == nil {
			t.Fatal("expected error for odd-count SUMMARIZECOLUMNS pairs")
		}
	})
}
