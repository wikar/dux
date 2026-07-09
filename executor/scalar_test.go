package executor_test

import (
	"testing"
)

// one runs a query expected to produce a single row and returns the named cell.
func one(t *testing.T, dux, col string) any {
	t.Helper()
	db, schema := setupTestDB(t)
	_, rows := run(t, db, schema, dux)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	return cell(t, rows[0], col)
}

func TestExecute_ScalarFunctions(t *testing.T) {
	scalar := func(expr string) string {
		return `EVALUATE SUMMARIZECOLUMNS("V", ` + expr + `)`
	}

	tests := []struct {
		name string
		expr string
		want any
	}{
		// Text
		{"LEN", `LEN("hello")`, int64(5)},
		{"MID", `MID("abcdef", 2, 3)`, "bcd"},
		{"SUBSTITUTE", `SUBSTITUTE("a-b-c", "-", "+")`, "a+b+c"},
		{"REPLACE", `REPLACE("abcdef", 2, 3, "XY")`, "aXYef"},
		{"CONCATENATE", `CONCATENATE("foo", "bar")`, "foobar"},
		{"SEARCH_case_insensitive", `SEARCH("B", "abc")`, int64(2)},
		{"FIND_case_sensitive_miss", `FIND("B", "abc")`, int64(0)},
		{"REPT", `REPT("ab", 3)`, "ababab"},
		{"EXACT_true", `EXACT("a", "a")`, true},
		{"EXACT_false", `EXACT("a", "A")`, false},
		{"VALUE", `VALUE("42.5")`, 42.5},
		{"UNICHAR", `UNICHAR(65)`, "A"},

		// Math
		{"INT", `INT(7.9)`, int64(7)},
		{"INT_negative", `INT(-7.1)`, int64(-8)},
		{"MOD_excel_sign", `MOD(-3, 2)`, float64(1)},
		{"ROUNDUP", `ROUNDUP(1.211, 2)`, 1.22},
		{"ROUNDDOWN", `ROUNDDOWN(1.219, 2)`, 1.21},
		{"ROUNDUP_negative", `ROUNDUP(-1.211, 2)`, -1.22},
		{"TRUNC", `TRUNC(-7.9)`, float64(-7)},
		{"CEILING_significance", `CEILING(23, 5)`, float64(25)},
		{"FLOOR_significance", `FLOOR(23, 5)`, float64(20)},
		{"LOG_default_base10", `LOG(100)`, float64(2)},
		{"LOG_base2", `LOG(8, 2)`, float64(3)},
		{"POWER", `POWER(2, 10)`, float64(1024)},

		// Date
		{"YEAR", `YEAR(DATE(2024, 3, 15))`, int64(2024)},
		{"MONTH", `MONTH(DATE(2024, 3, 15))`, int64(3)},
		{"DAY", `DAY(DATE(2024, 3, 15))`, int64(15)},
		{"QUARTER", `QUARTER(DATE(2024, 8, 1))`, int64(3)},
		{"EOMONTH", `DAY(EOMONTH(DATE(2024, 2, 5), 0))`, int64(29)},
		{"EDATE", `MONTH(EDATE(DATE(2024, 1, 31), 1))`, int64(2)},
		{"WEEKDAY_sunday_is_1", `WEEKDAY(DATE(2024, 3, 17))`, int64(1)},
		{"WEEKDAY_type2_monday_1", `WEEKDAY(DATE(2024, 3, 18), 2)`, int64(1)},
		{"DATEDIFF_days", `DATEDIFF(DATE(2024, 1, 1), DATE(2024, 3, 1), DAY)`, int64(60)},
		{"DATEDIFF_months", `DATEDIFF(DATE(2023, 1, 15), DATE(2024, 3, 15), MONTH)`, int64(14)},

		// FORMAT
		{"FORMAT_date", `FORMAT(DATE(2024, 3, 5), "yyyy-MM-dd")`, "2024-03-05"},
		{"FORMAT_numeric", `FORMAT(3.14159, "0.00")`, "3.14"},
		{"FORMAT_thousands", `FORMAT(1234567.891, "#,##0.00")`, "1,234,567.89"},
		{"FORMAT_percent", `FORMAT(0.1234, "Percent")`, "12.34%"},

		// Logical
		{"TRUE_fn", `IF(TRUE(), "y", "n")`, "y"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := one(t, scalar(tt.expr), "V")
			switch want := tt.want.(type) {
			case float64:
				g := toFloat(got)
				if diff := g - want; diff > 1e-9 || diff < -1e-9 {
					t.Errorf("%s: expected %v, got %v", tt.expr, want, got)
				}
			case int64:
				if toFloat(got) != float64(want) {
					t.Errorf("%s: expected %v, got %v", tt.expr, want, got)
				}
			default:
				if got != tt.want {
					t.Errorf("%s: expected %v (%T), got %v (%T)", tt.expr, tt.want, tt.want, got, got)
				}
			}
		})
	}
}

func TestExecute_Related(t *testing.T) {
	db, schema := setupTestDB(t)

	t.Run("RELATED_in_ADDCOLUMNS", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE ADDCOLUMNS(
			sales,
			"Category", RELATED(products[category])
		)`)
		if len(rows) != 6 {
			t.Fatalf("expected 6 rows, got %d", len(rows))
		}
		for _, row := range rows {
			want := "Electronics"
			if cell(t, row, "product") == "Doohickey" {
				want = "Misc"
			}
			if v := cell(t, row, "Category"); v != want {
				t.Errorf("product %v: expected %s, got %v", row["product"], want, v)
			}
		}
	})

	t.Run("RELATED_in_FILTER", func(t *testing.T) {
		_, rows := run(t, db, schema,
			`EVALUATE FILTER(sales, RELATED(products[category]) = "Misc")`)
		if len(rows) != 2 {
			t.Fatalf("expected 2 Doohickey rows, got %d", len(rows))
		}
	})

	t.Run("RELATED_in_iterator", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(
			"MiscRev", SUMX(sales, IF(RELATED(products[category]) = "Misc", sales[amount], 0))
		)`)
		// Doohickey: 50 + 75 = 125.
		if v := toFloat(cell(t, rows[0], "MiscRev")); v != 125 {
			t.Errorf("expected 125, got %v", v)
		}
	})

	t.Run("RELATEDTABLE_rowcount", func(t *testing.T) {
		_, rows := run(t, db, schema, `EVALUATE ADDCOLUMNS(
			products,
			"Sales Count", COUNTROWS(RELATEDTABLE(sales))
		)`)
		if len(rows) != 3 {
			t.Fatalf("expected 3 rows, got %d", len(rows))
		}
		for _, row := range rows {
			if v := toFloat(cell(t, row, "Sales Count")); v != 2 {
				t.Errorf("product %v: expected 2 sales, got %v", row["product"], v)
			}
		}
	})
}
