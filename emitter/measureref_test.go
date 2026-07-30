package emitter_test

// Tests for measure references whose home table is not named by the reference
// itself: a bare [Measure] carries no table qualifier, so every FROM clause the
// emitter builds must expand the measure to find the tables its body reads.
//
// Also covers ROW (emitted as a group-key-less SUMMARIZECOLUMNS) and the guard
// that rejects an inline aggregate in a row-context position instead of
// emitting SQL DuckDB cannot bind.

import (
	"strings"
	"testing"

	"github.com/danielwikar/dux/emitter"
	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// measureSchema is minSchema plus a global measure stored on "sales", so a bare
// [NetSalesAmount] reference must reach "sales" through the measure store.
func measureSchema(t *testing.T) *semantic.Schema {
	t.Helper()
	s := minSchema()
	defs, err := parser.ParseMeasures(`DEFINE
    MEASURE sales[NetSalesAmount] = SUM(sales[amount])
    MEASURE products[CategoryCount] = COUNT(products[category])`)
	if err != nil {
		t.Fatalf("parse measures: %v", err)
	}
	s.Measures["sales"] = map[string]*parser.MeasureDefinition{"NetSalesAmount": defs[0]}
	s.Measures["products"] = map[string]*parser.MeasureDefinition{"CategoryCount": defs[1]}
	return s
}

func emitMeasure(t *testing.T, dux string) (string, error) {
	t.Helper()
	em := &emitter.Emitter{Schema: measureSchema(t)}
	return em.Emit(mustParse(t, dux))
}

func emitMeasureOK(t *testing.T, dux string) string {
	t.Helper()
	sql, err := emitMeasure(t, dux)
	if err != nil {
		t.Fatalf("emit %q: %v", dux, err)
	}
	return sql
}

func emitMeasureErr(t *testing.T, dux, wantFragment string) {
	t.Helper()
	sql, err := emitMeasure(t, dux)
	if err == nil {
		t.Fatalf("emit %q: expected an error, got SQL:\n%s", dux, sql)
	}
	if !strings.Contains(err.Error(), wantFragment) {
		t.Errorf("emit %q: error %q does not mention %q", dux, err, wantFragment)
	}
}

// ─── Bare measure references join their home table ───────────────────────────

func TestBareMeasureJoinsHomeTable(t *testing.T) {
	t.Run("SummarizeColumns", func(t *testing.T) {
		// The group key lives on products; the measure body reads sales, which
		// only the measure store can reveal.
		sql := emitMeasureOK(t, `EVALUATE SUMMARIZECOLUMNS(products[category], "Net", [NetSalesAmount])`)
		assertContains(t, sql, "SUM(sales.amount)", "FROM sales", "LEFT JOIN products")
	})

	t.Run("SummarizeColumnsMatchesQualified", func(t *testing.T) {
		// A qualified reference names its home table on the ColRef itself and
		// has always worked; the bare form must emit the same SQL.
		bare := emitMeasureOK(t, `EVALUATE SUMMARIZECOLUMNS(products[category], "Net", [NetSalesAmount])`)
		qualified := emitMeasureOK(t, `EVALUATE SUMMARIZECOLUMNS(products[category], "Net", sales[NetSalesAmount])`)
		if bare != qualified {
			t.Errorf("bare and qualified references differ\nbare:\n%s\nqualified:\n%s", bare, qualified)
		}
	})

	t.Run("SummarizeColumnsNoGroupKeys", func(t *testing.T) {
		sql := emitMeasureOK(t, `EVALUATE SUMMARIZECOLUMNS("Net", [NetSalesAmount])`)
		assertContains(t, sql, "SUM(sales.amount)", "FROM sales")
	})

	t.Run("MeasureInGroupPosition", func(t *testing.T) {
		sql := emitMeasureOK(t, `EVALUATE SUMMARIZECOLUMNS(products[category], [NetSalesAmount])`)
		assertContains(t, sql, "SUM(sales.amount)", "FROM products", "LEFT JOIN sales")
	})

	t.Run("ArithmeticOverBareMeasure", func(t *testing.T) {
		sql := emitMeasureOK(t, `EVALUATE SUMMARIZECOLUMNS(products[category], "Net", [NetSalesAmount] * 2)`)
		assertContains(t, sql, "SUM(sales.amount) * 2", "FROM sales", "LEFT JOIN products")
	})

	t.Run("StandaloneCalculate", func(t *testing.T) {
		// The filter argument names products, so only measure expansion can
		// bring sales — the table the aggregate actually reads — into the FROM.
		sql := emitMeasureOK(t, `EVALUATE SUMMARIZECOLUMNS("Net", CALCULATE([NetSalesAmount], products[category] = "A"))`)
		assertContains(t, sql, "SUM(sales.amount)", "sales")
	})

	t.Run("GroupedCalculateWithRemoval", func(t *testing.T) {
		// A CALCULATE that removes a group filter emits its own context CTE,
		// which needs the measure's home table in that CTE's FROM.
		sql := emitMeasureOK(t, `EVALUATE SUMMARIZECOLUMNS(products[category],
			"AllNet", CALCULATE([NetSalesAmount], ALL(products)))`)
		assertContains(t, sql, "SUM(sales.amount)", "sales")
	})

	t.Run("MeasureOnGroupKeyTableAddsNoJoin", func(t *testing.T) {
		// The measure body reads only products, so sales must stay out.
		sql := emitMeasureOK(t, `EVALUATE SUMMARIZECOLUMNS(products[category], "N", [CategoryCount])`)
		assertContains(t, sql, "COUNT(", "FROM products")
		assertNotContains(t, sql, "sales")
	})
}

// ─── ROW ─────────────────────────────────────────────────────────────────────

func TestRow(t *testing.T) {
	t.Run("BareMeasure", func(t *testing.T) {
		sql := emitMeasureOK(t, `EVALUATE ROW("Net", [NetSalesAmount])`)
		assertContains(t, sql, "SUM(sales.amount) AS 'Net'", "FROM sales")
		assertNotContains(t, sql, "GROUP BY")
	})

	t.Run("MultiplePairs", func(t *testing.T) {
		sql := emitMeasureOK(t, `EVALUATE ROW("Net", [NetSalesAmount], "Qty", SUM(sales[qty]))`)
		assertContains(t, sql, "AS 'Net'", "AS 'Qty'", "FROM sales")
	})

	t.Run("SpansTwoTables", func(t *testing.T) {
		// Measures over different tables cluster and stitch, exactly as they do
		// under SUMMARIZECOLUMNS.
		sql := emitMeasureOK(t, `EVALUATE ROW("Net", [NetSalesAmount], "Cats", [CategoryCount])`)
		assertContains(t, sql, "AS 'Net'", "AS 'Cats'")
	})

	t.Run("OddArgumentCount", func(t *testing.T) {
		emitMeasureErr(t, `EVALUATE ROW("Net", [NetSalesAmount], "Qty")`, "ROW:")
	})

	t.Run("UnnamedColumn", func(t *testing.T) {
		emitMeasureErr(t, `EVALUATE ROW(products[category], [NetSalesAmount])`, "must be a quoted string")
	})
}

// ─── Inline aggregates in row-context positions ──────────────────────────────

func TestInlineAggInRowContextRejected(t *testing.T) {
	// DUX does not perform context transition: these must fail as DUX errors
	// rather than reach DuckDB as unbindable SQL.
	rejected := []struct {
		name string
		dux  string
	}{
		{"AddColumnsBareMeasure", `EVALUATE ADDCOLUMNS(products, "Net", [NetSalesAmount])`},
		{"AddColumnsQualifiedMeasure", `EVALUATE ADDCOLUMNS(products, "Net", sales[NetSalesAmount])`},
		{"AddColumnsAggregate", `EVALUATE ADDCOLUMNS(products, "Net", SUM(sales[amount]))`},
		{"AddColumnsCountRowsBareTable", `EVALUATE ADDCOLUMNS(products, "N", COUNTROWS(sales))`},
		{"SelectColumnsBareMeasure", `EVALUATE SELECTCOLUMNS(products, "Net", [NetSalesAmount])`},
		{"FilterPredicate", `EVALUATE FILTER(products, [NetSalesAmount] > 1)`},
		{"IteratorBody", `EVALUATE SUMMARIZECOLUMNS("Net", SUMX(products, [NetSalesAmount]))`},
		{"ConcatenateXBody", `EVALUATE SUMMARIZECOLUMNS("Net", CONCATENATEX(products, [NetSalesAmount]))`},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			emitMeasureErr(t, tc.dux, "context transition")
		})
	}

	// Aggregates that carry their own context stay accepted.
	accepted := []struct {
		name string
		dux  string
	}{
		{"AddColumnsCalculate", `EVALUATE ADDCOLUMNS(products, "Net", CALCULATE([NetSalesAmount], sales[product] = products[product]))`},
		{"AddColumnsIterator", `EVALUATE ADDCOLUMNS(products, "Net", SUMX(sales, sales[amount]))`},
		{"AddColumnsCountRowsTableExpr", `EVALUATE ADDCOLUMNS(products, "N", COUNTROWS(FILTER(sales, sales[qty] > 1)))`},
		{"AddColumnsRowExpression", `EVALUATE ADDCOLUMNS(sales, "Line", sales[qty] * sales[amount])`},
		{"FilterPlainPredicate", `EVALUATE FILTER(products, products[category] = "A")`},
		{"IteratorPlainBody", `EVALUATE SUMMARIZECOLUMNS("Net", SUMX(sales, sales[amount]))`},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			emitMeasureOK(t, tc.dux)
		})
	}
}
