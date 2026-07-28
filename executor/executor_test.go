package executor_test

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/danielwikar/dux/executor"
	"github.com/danielwikar/dux/semantic"
)

func TestExecute_CanceledContext(t *testing.T) {
	db, schema := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := executor.ExecuteContext(ctx, db, schema, `EVALUATE sales`)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

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
	case *big.Int:
		// DuckDB returns HUGEINT (e.g. SUM over INTEGER) as *big.Int.
		f, _ := new(big.Float).SetInt(n).Float64()
		return f
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

func TestExecute_PositionedErrors(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("resolve error carries source position", func(t *testing.T) {
		_, _, err := executorExecute(db, schema, "EVALUATE SUMMARIZECOLUMNS(\n    sales[region],\n    \"X\", SUM(sales[nope])\n)")
		var qe *executor.QueryError
		if !errors.As(err, &qe) {
			t.Fatalf("expected *executor.QueryError, got %T: %v", err, err)
		}
		if qe.Stage != "resolve" {
			t.Errorf("Stage = %q, want resolve", qe.Stage)
		}
		if qe.Line != 3 || qe.Column <= 0 {
			t.Errorf("position = %d:%d, want line 3 with a positive column", qe.Line, qe.Column)
		}
	})

	t.Run("parse error carries source position", func(t *testing.T) {
		_, _, err := executorExecute(db, schema, "EVALUATE SUMMARIZECOLUMNS(sales[region],")
		var qe *executor.QueryError
		if !errors.As(err, &qe) {
			t.Fatalf("expected *executor.QueryError, got %T: %v", err, err)
		}
		if qe.Stage != "parse" || qe.Line < 1 {
			t.Errorf("stage/position = %q %d:%d, want parse with a position", qe.Stage, qe.Line, qe.Column)
		}
	})
}

func TestExecute_Iterators(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("SUMX", func(t *testing.T) {
		// Iterators over a bare table respect the group's filter context:
		// each region aggregates only its own rows.
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "Rev", SUMX(sales, sales[amount] * sales[qty]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		want := map[string]float64{"North": 450, "South": 1600}
		for _, row := range rows {
			region := row["region"].(string)
			if v := toFloat(row["Rev"]); v != want[region] {
				t.Errorf("region %s: expected SUMX %v, got %v", region, want[region], v)
			}
		}
	})

	t.Run("SUMX_grouped_by_related_dimension", func(t *testing.T) {
		// Group key on the one-side dimension; the iterated fact table joins
		// through the relationship and aggregates per group.
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(products[category], "Rev", SUMX(sales, sales[amount] * sales[qty]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		want := map[string]float64{"Electronics": 1850, "Misc": 200}
		for _, row := range rows {
			cat := row["category"].(string)
			if v := toFloat(row["Rev"]); v != want[cat] {
				t.Errorf("category %s: expected SUMX %v, got %v", cat, want[cat], v)
			}
		}
	})

	t.Run("SUMX_constant_expression_joins_iterated_table", func(t *testing.T) {
		// The iterated table reaches the FROM clause even when the inner
		// expression references no columns.
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(products[category], "N", SUMX(sales, 1))`)
		want := map[string]float64{"Electronics": 4, "Misc": 2}
		for _, row := range rows {
			cat := row["category"].(string)
			if v := toFloat(row["N"]); v != want[cat] {
				t.Errorf("category %s: expected %v rows, got %v", cat, want[cat], v)
			}
		}
	})

	t.Run("FILTER_argument_restricts_context", func(t *testing.T) {
		// FILTER(table, pred) in the group position acts as a filter argument
		// (range/comparison filters, complementing TREATAS equality).
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], FILTER(sales, sales[qty] >= 3), "Rev", SUM(sales[amount]))`)
		if len(rows) != 1 {
			t.Fatalf("expected 1 row (South only), got %d: %v", len(rows), rows)
		}
		if v := toFloat(rows[0]["Rev"]); v != 400 {
			t.Errorf("expected Rev 400 (150+250), got %v", v)
		}
	})

	t.Run("SUMX_over_FILTER_keeps_whole_table_scan", func(t *testing.T) {
		// A nested table expression is an explicit iteration source — it does
		// not inherit the group context (matches the scalar-subquery form).
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "Rev", SUMX(FILTER(sales, sales[qty] > 3), sales[amount] * sales[qty]))`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		for _, row := range rows {
			if v := toFloat(row["Rev"]); v != 1000 {
				t.Errorf("expected table-wide filtered total 1000, got %v", v)
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

	t.Run("CALCULATE_in_SUMMARIZECOLUMNS", func(t *testing.T) {
		// CALCULATE inside SUMMARIZECOLUMNS must produce per-group results.
		// Seed data qty: North 2,1,1 → only qty=2 passes filter → amount=100
		//                South 3,4,2 → qty=3,4 pass → amount=150+250=400
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			sales[region],
			"BigQty", CALCULATE(SUM(sales[amount]), sales[qty] > 2)
		)`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		totals := map[string]float64{}
		for _, row := range rows {
			region, _ := row["region"].(string)
			totals[region] = toFloat(row["BigQty"])
		}
		if totals["North"] != 0 {
			// North has no rows with qty > 2, so FILTER excludes all → NULL (mapped to 0)
			t.Errorf("North CALCULATE SUM: expected 0, got %v", totals["North"])
		}
		if totals["South"] != 400 {
			t.Errorf("South CALCULATE SUM: expected 400, got %v", totals["South"])
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

// ─── admission control ────────────────────────────────────────────────────────

// holdOnlyConn caps the pool at one connection and takes it, so the next
// query has to queue. The returned func releases it.
func holdOnlyConn(t *testing.T, db *sql.DB) func() {
	t.Helper()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("pin connection: %v", err)
	}
	return func() { conn.Close() }
}

func TestExecute_ServerBusyWhenPoolExhausted(t *testing.T) {
	db, schema := setupTestDB(t)
	defer holdOnlyConn(t, db)()

	defer func(d time.Duration) { executor.AdmissionTimeout = d }(executor.AdmissionTimeout)
	executor.AdmissionTimeout = 100 * time.Millisecond

	start := time.Now()
	_, _, err := executor.ExecuteContext(context.Background(), db, schema, `EVALUATE sales`)
	elapsed := time.Since(start)

	if !errors.Is(err, executor.ErrServerBusy) {
		t.Fatalf("expected ErrServerBusy, got %v", err)
	}
	// Shedding must be bounded by AdmissionTimeout, not QueryTimeout.
	if elapsed > time.Second {
		t.Errorf("shed after %v, want close to the 100ms admission timeout", elapsed)
	}
}

// A caller that gives up while queued gets its own context error, not the
// server-busy signal: nothing was shed, the client left.
func TestExecute_CallerCancellationBeatsServerBusy(t *testing.T) {
	db, schema := setupTestDB(t)
	defer holdOnlyConn(t, db)()

	defer func(d time.Duration) { executor.AdmissionTimeout = d }(executor.AdmissionTimeout)
	executor.AdmissionTimeout = 10 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, err := executor.ExecuteContext(ctx, db, schema, `EVALUATE sales`)

	if errors.Is(err, executor.ErrServerBusy) {
		t.Fatalf("caller cancellation reported as server-busy: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected the caller's deadline, got %v", err)
	}
}

// Queue wait must not consume the execution budget: a query that waits longer
// than QueryTimeout for a connection still gets its full budget once it holds
// one. Before admission and execution were split this returned a deadline
// error without ever running.
func TestExecute_QueueWaitDoesNotConsumeQueryBudget(t *testing.T) {
	db, schema := setupTestDB(t)
	release := holdOnlyConn(t, db)

	defer func(a, q time.Duration) {
		executor.AdmissionTimeout, executor.QueryTimeout = a, q
	}(executor.AdmissionTimeout, executor.QueryTimeout)
	executor.AdmissionTimeout = 10 * time.Second
	executor.QueryTimeout = time.Second

	// Held for twice the query budget, well inside the admission budget.
	go func() {
		time.Sleep(2 * time.Second)
		release()
	}()

	cols, rows, err := executor.ExecuteContext(context.Background(), db, schema, `EVALUATE sales`)
	if err != nil {
		t.Fatalf("query after a long queue wait: %v", err)
	}
	if len(cols) == 0 || len(rows) != 6 {
		t.Fatalf("got %d columns and %d rows, want 6 rows", len(cols), len(rows))
	}
}
