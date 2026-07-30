package parser_test

import (
	"testing"

	"github.com/danielwikar/dux/parser"
)

func TestParseNegativeNumbers(t *testing.T) {
	tests := []string{
		`EVALUATE FILTER(sales, sales[amount] > -100)`,
		`EVALUATE SUMMARIZECOLUMNS(d[y], "PY", CALCULATE(SUM(o[a]), DATEADD(d[c], -1, YEAR)))`,
		`EVALUATE CALCULATE(SUM(o[a]), DATESINPERIOD(d[c], MAX(d[c]), -2, MONTH))`,
		`EVALUATE SUMMARIZECOLUMNS(s[r], "X", SUM(s[a]) * -1)`,
	}
	for _, dux := range tests {
		if _, err := parser.Parse(dux); err != nil {
			t.Errorf("Parse(%q): %v", dux, err)
		}
	}
}

func TestParseExactAndEscapedSyntax(t *testing.T) {
	queries := []string{
		`EVALUATE ROW("N", 9007199254740993, "S", "say ""hello""")`,
		`EVALUATE ROW("X", "a" & "b", "P", 2 ^ 3)`,
		`EVALUATE TOPN(2, sales, sales[amount], ASC, sales[id], DESC)`,
		`EVALUATE FILTER('Owner''s Sales', 'Owner''s Sales'[Gross]]Amount] > 0)`,
	}
	for _, query := range queries {
		if _, err := parser.Parse(query); err != nil {
			t.Errorf("Parse(%q): %v", query, err)
		}
	}
}
