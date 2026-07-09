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
