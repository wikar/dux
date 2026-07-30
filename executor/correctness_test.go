package executor_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/danielwikar/dux/executor"
)

func TestDAXSetCardinalityAndNestedComposition(t *testing.T) {
	db, schema := setupTestDB(t)
	_, rows := run(t, db, schema, `EVALUATE INTERSECT(
		UNION(ROW("X", 1), ROW("X", 1)), ROW("X", 1))`)
	if len(rows) != 2 {
		t.Fatalf("INTERSECT retained %d first-table rows, want 2", len(rows))
	}

	_, rows = run(t, db, schema, `EVALUATE EXCEPT(
		UNION(ROW("X", 1), ROW("X", 1)), ROW("X", 2))`)
	if len(rows) != 2 {
		t.Fatalf("EXCEPT retained %d first-table rows, want 2", len(rows))
	}

	_, rows = run(t, db, schema, `EVALUATE TOPN(1,
		UNION(UNION(ROW("X", 3), ROW("X", 1)), ROW("X", 2)), [X], ASC)`)
	if len(rows) != 1 || toFloat(rows[0]["X"]) != 1 {
		t.Fatalf("nested TOPN = %v, want X=1", rows)
	}
}

func TestTOPNRetainsTies(t *testing.T) {
	db, schema := setupTestDB(t)
	_, rows := run(t, db, schema, `EVALUATE TOPN(1,
		UNION(UNION(ROW("Score", 10), ROW("Score", 10)), ROW("Score", 5)), [Score])`)
	if len(rows) != 2 {
		t.Fatalf("TOPN returned %d tied rows, want 2", len(rows))
	}
}

func TestBlankAndExactLiteralSemantics(t *testing.T) {
	db, schema := setupTestDB(t)
	if _, err := db.Exec(`INSERT INTO sales VALUES (7, 'Blank', 1, 1, NULL)`); err != nil {
		t.Fatal(err)
	}

	_, rows := run(t, db, schema, `EVALUATE ROW(
		"N", 9007199254740993,
		"Text", "a" & "b",
		"Left", LEFT(123, 2),
		"Trim", TRIM("  a   b  "),
		"Round", ROUND(-1.15, 1),
		"StrictBlank", BLANK() == BLANK(),
		"Distinct", DISTINCTCOUNT(sales[region]),
		"NoRows", CALCULATE(COUNTBLANK(sales[region]), sales[id] < 0))`)
	row := rows[0]
	if got := fmt.Sprint(row["N"]); got != "9007199254740993" {
		t.Fatalf("exact integer = %s", got)
	}
	if row["Text"] != "ab" || row["StrictBlank"] != true {
		t.Fatalf("text/strict BLANK semantics = %v", row)
	}
	if fmt.Sprint(row["Left"]) != "12" || fmt.Sprint(row["Trim"]) != "a b" || fmt.Sprint(row["Round"]) != "-1.2" {
		t.Fatalf("scalar DAX mappings = %v", row)
	}
	if toFloat(row["Distinct"]) != 3 {
		t.Fatalf("DISTINCTCOUNT including BLANK = %v, want 3", row["Distinct"])
	}
	if row["NoRows"] != nil {
		t.Fatalf("COUNTBLANK over no rows = %v, want BLANK", row["NoRows"])
	}

	_, rows = run(t, db, schema, `EVALUATE SUMMARIZECOLUMNS(sales[region], "X", BLANK())`)
	if len(rows) != 0 {
		t.Fatalf("all-BLANK SUMMARIZECOLUMNS returned %d rows", len(rows))
	}
}

func TestInvalidFunctionsFailInDUX(t *testing.T) {
	db, schema := setupTestDB(t)
	for _, query := range []string{
		`EVALUATE ROW("X", BLANK(1))`,
		`EVALUATE ROW("X", DEFINITELYNOTDAX(1))`,
		`EVALUATE ROW("X", ROUND(1))`,
	} {
		_, _, err := executor.Execute(db, schema, query)
		if err == nil || !strings.Contains(err.Error(), "emit") {
			t.Fatalf("Execute(%q) error = %v", query, err)
		}
	}
}
