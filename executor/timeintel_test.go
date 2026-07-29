package executor_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/danielwikar/dux/semantic"
)

// setupTimeDB builds a star schema with a designated date table:
//
//	dates  — one row per day for 2023-01-01 .. 2024-12-31, with year/month
//	orders — a handful of orders across months
//
// Order amounts: 2023: Jan 10, Feb 20, Mar 30 — 2024: Jan 100, Feb 200, Mar 300.
func setupTimeDB(t *testing.T) (*sql.DB, *semantic.Schema) {
	return setupTimeDBWithDesignation(t, true)
}

func setupTimeDBWithDesignation(t *testing.T, designate bool) (*sql.DB, *semantic.Schema) {
	t.Helper()

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ddl := []string{
		`CREATE TABLE dates AS
			SELECT CAST(generate_series AS DATE)          AS date,
			       CAST(year(generate_series) AS INT)    AS year,
			       CAST(month(generate_series) AS INT)   AS month,
			       dayofweek(generate_series) BETWEEN 1 AND 5 AS is_workday
			FROM generate_series(DATE '2023-01-01', DATE '2024-12-31', INTERVAL 1 DAY)`,
		`CREATE TABLE fiscal (year INTEGER)`,
		`INSERT INTO fiscal VALUES (2023), (2024)`,
		`CREATE TABLE orders (
			id         INTEGER,
			order_date DATE,
			amount     DOUBLE
		)`,
		`INSERT INTO orders VALUES
			(1, DATE '2023-01-15', 10.0),
			(2, DATE '2023-02-10', 20.0),
			(3, DATE '2023-03-05', 30.0),
			(4, DATE '2024-01-20', 100.0),
			(5, DATE '2024-02-15', 200.0),
			(6, DATE '2024-03-10', 300.0)`,
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
		FromTable:  "orders",
		FromColumn: "order_date",
		ToTable:    "dates",
		ToColumn:   "date",
	})
	schema.Relationships = append(schema.Relationships, &semantic.Relationship{
		FromTable:  "dates",
		FromColumn: "year",
		ToTable:    "fiscal",
		ToColumn:   "year",
	})
	if designate {
		schema.SetDateTable("dates", "date")
	}

	return db, schema
}

// monthRow finds the row for a given year and month.
func monthRow(t *testing.T, rows []map[string]any, year, month float64) map[string]any {
	t.Helper()
	for _, row := range rows {
		if toFloat(row["year"]) == year && toFloat(row["month"]) == month {
			return row
		}
	}
	t.Fatalf("no row for year=%v month=%v", year, month)
	return nil
}

func TestExecute_TimeIntelligence(t *testing.T) {
	db, schema := setupTimeDB(t)

	t.Run("TOTALYTD", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year],
			dates[month],
			"YTD", TOTALYTD(SUM(orders[amount]), dates[date])
		)`)
		checks := []struct{ y, m, want float64 }{
			{2023, 1, 10}, {2023, 2, 30}, {2023, 3, 60}, {2023, 12, 60},
			{2024, 1, 100}, {2024, 2, 300}, {2024, 3, 600},
		}
		for _, c := range checks {
			if v := toFloat(cell(t, monthRow(t, rows, c.y, c.m), "YTD")); v != c.want {
				t.Errorf("YTD %v-%v: expected %v, got %v", c.y, c.m, c.want, v)
			}
		}
	})

	t.Run("CALCULATE_DATESYTD_equals_TOTALYTD", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year],
			dates[month],
			"YTD", CALCULATE(SUM(orders[amount]), DATESYTD(dates[date]))
		)`)
		if v := toFloat(cell(t, monthRow(t, rows, 2024, 2), "YTD")); v != 300 {
			t.Errorf("expected 300, got %v", v)
		}
	})

	t.Run("SAMEPERIODLASTYEAR", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year],
			dates[month],
			"PY", CALCULATE(SUM(orders[amount]), SAMEPERIODLASTYEAR(dates[date]))
		)`)
		if v := toFloat(cell(t, monthRow(t, rows, 2024, 2), "PY")); v != 20 {
			t.Errorf("2024-02 PY: expected 20, got %v", v)
		}
		if v := monthRow(t, rows, 2023, 2)["PY"]; v != nil {
			t.Errorf("2023-02 PY: expected NULL (no 2022 data), got %v", v)
		}
	})

	t.Run("DATEADD_minus_one_year", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year],
			dates[month],
			"PY", CALCULATE(SUM(orders[amount]), DATEADD(dates[date], -1, YEAR))
		)`)
		if v := toFloat(cell(t, monthRow(t, rows, 2024, 3), "PY")); v != 30 {
			t.Errorf("2024-03 PY: expected 30, got %v", v)
		}
	})

	t.Run("PREVIOUSMONTH", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year],
			dates[month],
			"PM", CALCULATE(SUM(orders[amount]), PREVIOUSMONTH(dates[date]))
		)`)
		if v := toFloat(cell(t, monthRow(t, rows, 2023, 2), "PM")); v != 10 {
			t.Errorf("2023-02 PM: expected 10, got %v", v)
		}
		if v := toFloat(cell(t, monthRow(t, rows, 2024, 2), "PM")); v != 100 {
			t.Errorf("2024-02 PM: expected 100, got %v", v)
		}
	})

	t.Run("NEXTMONTH", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year],
			dates[month],
			"NM", CALCULATE(SUM(orders[amount]), NEXTMONTH(dates[date]))
		)`)
		if v := toFloat(cell(t, monthRow(t, rows, 2024, 1), "NM")); v != 200 {
			t.Errorf("2024-01 NM: expected 200, got %v", v)
		}
	})

	t.Run("DATESBETWEEN_literals", func(t *testing.T) {
		_, rows := run(t, db, schema,
			`EVALUATE CALCULATE(SUM(orders[amount]), DATESBETWEEN(dates[date], "2023-01-01", "2023-12-31"))`)
		if len(rows) != 1 {
			t.Fatalf("expected 1 row, got %d", len(rows))
		}
		for _, v := range rows[0] {
			if toFloat(v) != 60 {
				t.Errorf("expected 60 (all 2023 orders), got %v", v)
			}
		}
	})

	t.Run("DATESBETWEEN_DATE_ctor", func(t *testing.T) {
		_, rows := run(t, db, schema,
			`EVALUATE CALCULATE(SUM(orders[amount]), DATESBETWEEN(dates[date], DATE(2024, 1, 1), BLANK()))`)
		for _, v := range rows[0] {
			if toFloat(v) != 600 {
				t.Errorf("expected 600 (all 2024 orders), got %v", v)
			}
		}
	})

	t.Run("DATESINPERIOD_trailing_two_months", func(t *testing.T) {
		// Two months back from the last date in context (2024-03-31 global max
		// of the date table): (2024-01-31, 2024-03-31] → Feb + Mar 2024.
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year],
			dates[month],
			"R2M", CALCULATE(SUM(orders[amount]), DATESINPERIOD(dates[date], MAX(dates[date]), -2, MONTH))
		)`)
		if v := toFloat(cell(t, monthRow(t, rows, 2024, 3), "R2M")); v != 500 {
			t.Errorf("2024-03 rolling 2 months: expected 500, got %v", v)
		}
	})

	t.Run("DATESINPERIOD_rolling_7_days_at_date_grain", func(t *testing.T) {
		// The dashboard line-chart shape: a rolling window grouped by the
		// date column itself (one cell per calendar day). Compiled as a
		// context CTE with an anchor scan (contextcte.go) — the correlated
		// form was quadratic in |dates| × |orders|.
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[date],
			"Total", SUM(orders[amount]),
			"R7D", CALCULATE(SUM(orders[amount]), DATESINPERIOD(dates[date], MAX(dates[date]), -7, DAY))
		)`)
		if len(rows) != 731 {
			t.Fatalf("expected 731 rows (2023+2024 days), got %d", len(rows))
		}
		dayRow := func(day string) map[string]any {
			for _, row := range rows {
				if v, ok := row["date"]; ok && strings.HasPrefix(fmt.Sprint(v), day) {
					return row
				}
			}
			t.Fatalf("no row for %s", day)
			return nil
		}
		// Window is (d-7, d], inclusive of d. The 2023-01-15 order is visible
		// through 2023-01-21 and gone on 2023-01-22.
		if v := toFloat(cell(t, dayRow("2023-01-15"), "R7D")); v != 10 {
			t.Errorf("R7D on 2023-01-15: expected 10, got %v", v)
		}
		if v := toFloat(cell(t, dayRow("2023-01-21"), "R7D")); v != 10 {
			t.Errorf("R7D on 2023-01-21: expected 10, got %v", v)
		}
		if v := dayRow("2023-01-22")["R7D"]; v != nil {
			t.Errorf("R7D on 2023-01-22: expected NULL, got %v", v)
		}
		// The plain measure stays at day grain next to the windowed one.
		if v := toFloat(cell(t, dayRow("2023-01-15"), "Total")); v != 10 {
			t.Errorf("Total on 2023-01-15: expected 10, got %v", v)
		}
		if v := dayRow("2023-01-16")["Total"]; v != nil {
			t.Errorf("Total on 2023-01-16: expected NULL, got %v", v)
		}
	})

	t.Run("nested_time_intelligence_uses_the_outer_cell_anchor", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[date],
			"Nested", CALCULATE(
				CALCULATE(SUM(orders[amount]), DATESINPERIOD(dates[date], MAX(dates[date]), -7, DAY)),
				DATESYTD(dates[date]))
		)`)
		dayRow := func(day string) map[string]any {
			for _, row := range rows {
				if v, ok := row["date"]; ok && strings.HasPrefix(fmt.Sprint(v), day) {
					return row
				}
			}
			t.Fatalf("no row for %s", day)
			return nil
		}
		if v := toFloat(cell(t, dayRow("2023-01-15"), "Nested")); v != 10 {
			t.Errorf("nested window on 2023-01-15: expected 10, got %v", v)
		}
		if v := toFloat(cell(t, dayRow("2023-01-21"), "Nested")); v != 10 {
			t.Errorf("nested window on 2023-01-21: expected 10, got %v", v)
		}
		if v := dayRow("2023-01-22")["Nested"]; v != nil {
			t.Errorf("nested window on 2023-01-22: expected NULL, got %v", v)
		}
	})

	t.Run("DATESINPERIOD_inside_DIVIDE", func(t *testing.T) {
		// A context-modifying subtree composed in outer arithmetic: the
		// rolling aggregate and the plain aggregate evaluate in separate
		// contexts and recombine in the stitched SELECT.
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year],
			dates[month],
			"Ratio", DIVIDE(CALCULATE(SUM(orders[amount]), DATESINPERIOD(dates[date], MAX(dates[date]), -2, MONTH)), SUM(orders[amount]))
		)`)
		got := toFloat(cell(t, monthRow(t, rows, 2024, 3), "Ratio"))
		if want := 500.0 / 300.0; got < want-1e-9 || got > want+1e-9 {
			t.Errorf("2024-03 ratio: expected %v, got %v", want, got)
		}
	})

	t.Run("standalone_DATESYTD_table", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE DATESYTD(dates[date])`)
		// Global max is 2024-12-31 → YTD covers all of 2024 = 366 days (leap year).
		if len(rows) != 366 {
			t.Errorf("expected 366 rows, got %d", len(rows))
		}
	})

	t.Run("CALENDAR", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE CALENDAR("2024-01-01", "2024-01-31")`)
		if len(rows) != 31 {
			t.Errorf("expected 31 rows, got %d", len(rows))
		}
	})

	t.Run("CALENDARAUTO", func(t *testing.T) {
		// Date extremes across the model span 2023–2024 → whole years = 731 days.
		_, rows := run(t, db, schema, `EVALUATE CALENDARAUTO()`)
		if len(rows) != 731 {
			t.Errorf("expected 731 rows, got %d", len(rows))
		}
	})

	t.Run("undesignated_date_column", func(t *testing.T) {
		// Time intel on a raw fact date column (orders is NOT a designated
		// date table) still works standalone.
		_, rows := run(t, db, schema,
			`EVALUATE CALCULATE(SUM(orders[amount]), DATESYTD(orders[order_date]))`)
		// Global max order date is 2024-03-10 → YTD 2024 = 600.
		for _, v := range rows[0] {
			if toFloat(v) != 600 {
				t.Errorf("expected 600, got %v", v)
			}
		}
	})
}

func TestExecute_TimeIntelligenceContextCTECharacterization(t *testing.T) {
	t.Run("grand_total_no_group_keys", func(t *testing.T) {
		db, schema := setupTimeDB(t)
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			"YTD", CALCULATE(SUM(orders[amount]), DATESYTD(dates[date])))`)
		if got := toFloat(cell(t, rows[0], "YTD")); got != 600 {
			t.Fatalf("YTD = %v, want 600", got)
		}
	})

	t.Run("undesignated_date_table_keeps_kept_key_equality", func(t *testing.T) {
		db, schema := setupTimeDBWithDesignation(t, false)
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year],
			"YTD", CALCULATE(SUM(orders[amount]), DATESYTD(dates[date])))`)
		for _, c := range []struct{ year, want float64 }{{2023, 60}, {2024, 600}} {
			var found map[string]any
			for _, row := range rows {
				if toFloat(row["year"]) == c.year {
					found = row
					break
				}
			}
			if found == nil || toFloat(cell(t, found, "YTD")) != c.want {
				t.Fatalf("year %v row = %v, want YTD %v", c.year, found, c.want)
			}
		}
	})

	t.Run("time_intel_with_TREATAS_on_the_date_table", func(t *testing.T) {
		db, schema := setupTimeDB(t)
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year], dates[month], TREATAS({2024}, dates[year]),
			"YTD", CALCULATE(SUM(orders[amount]), DATESYTD(dates[date])))`)
		if got := toFloat(cell(t, monthRow(t, rows, 2024, 2), "YTD")); got != 300 {
			t.Fatalf("YTD = %v, want 300", got)
		}
	})

	t.Run("time_intel_with_FILTER_on_the_fact_table", func(t *testing.T) {
		db, schema := setupTimeDB(t)
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year], dates[month], FILTER(orders, orders[id] = 5),
			"YTD", CALCULATE(SUM(orders[amount]), DATESYTD(dates[date])))`)
		if got := toFloat(cell(t, monthRow(t, rows, 2024, 2), "YTD")); got != 200 {
			t.Fatalf("YTD = %v, want 200", got)
		}
	})

	t.Run("time_intel_with_CALCULATE_pred_on_the_date_table", func(t *testing.T) {
		db, schema := setupTimeDB(t)
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year], dates[month],
			"YTD", CALCULATE(SUM(orders[amount]), DATESYTD(dates[date]), dates[is_workday] = TRUE()))`)
		if got := toFloat(cell(t, monthRow(t, rows, 2024, 2), "YTD")); got != 200 {
			t.Fatalf("YTD = %v, want 200", got)
		}
	})

	t.Run("time_intel_with_CALCULATE_pred_as_KEEPFILTERS", func(t *testing.T) {
		db, schema := setupTimeDB(t)
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year], dates[month],
			"YTD", CALCULATE(SUM(orders[amount]), DATESYTD(dates[date]), KEEPFILTERS(dates[is_workday] = TRUE())))`)
		if got := toFloat(cell(t, monthRow(t, rows, 2024, 2), "YTD")); got != 200 {
			t.Fatalf("YTD = %v, want 200", got)
		}
	})

	t.Run("two_time_intel_measures_one_query", func(t *testing.T) {
		db, schema := setupTimeDB(t)
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year], dates[month],
			"YTD", CALCULATE(SUM(orders[amount]), DATESYTD(dates[date])),
			"PY", CALCULATE(SUM(orders[amount]), SAMEPERIODLASTYEAR(dates[date])))`)
		row := monthRow(t, rows, 2024, 2)
		if ytd, py := toFloat(cell(t, row, "YTD")), toFloat(cell(t, row, "PY")); ytd != 300 || py != 20 {
			t.Fatalf("YTD/PY = %v/%v, want 300/20", ytd, py)
		}
	})

	t.Run("identical_time_contexts_share_one_fact_scan", func(t *testing.T) {
		db, schema := setupTimeDB(t)
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year], dates[month],
			"YTD Amount", CALCULATE(SUM(orders[amount]), DATESYTD(dates[date])),
			"YTD Count", CALCULATE(COUNT(orders[id]), DATESYTD(dates[date])))`)
		row := monthRow(t, rows, 2024, 2)
		if amount, count := toFloat(cell(t, row, "YTD Amount")), toFloat(cell(t, row, "YTD Count")); amount != 300 || count != 2 {
			t.Fatalf("YTD amount/count = %v/%v, want 300/2", amount, count)
		}

		_, grand := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			"YTD Amount", CALCULATE(SUM(orders[amount]), DATESYTD(dates[date])),
			"YTD Count", CALCULATE(COUNT(orders[id]), DATESYTD(dates[date])))`)
		if amount, count := toFloat(cell(t, grand[0], "YTD Amount")), toFloat(cell(t, grand[0], "YTD Count")); amount != 600 || count != 3 {
			t.Fatalf("grand YTD amount/count = %v/%v, want 600/3", amount, count)
		}
	})

	t.Run("time_intelligence_combined_with_ALL", func(t *testing.T) {
		db, schema := setupTimeDB(t)
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			dates[year], dates[month],
			"Combined",
				CALCULATE(SUM(orders[amount]), DATESYTD(dates[date]))
				+ CALCULATE(SUM(orders[amount]), ALL(dates)))`)
		if got := toFloat(cell(t, monthRow(t, rows, 2024, 2), "Combined")); got != 960 {
			t.Fatalf("combined YTD + grand total = %v, want 960", got)
		}
	})

	t.Run("time_intel_through_a_non_leaf_date_table", func(t *testing.T) {
		db, schema := setupTimeDB(t)
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			fiscal[year],
			"YTD", CALCULATE(SUM(orders[amount]), DATESYTD(dates[date])))`)
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want 2", len(rows))
		}
	})
}
