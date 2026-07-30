package semantic

import (
	"strings"
	"testing"

	"github.com/danielwikar/dux/parser"
)

func resolverSchema() *Schema {
	s := NewSchema()
	s.Tables["Sales"] = &Table{Name: "Sales", Columns: map[string]*Column{
		"Amount": {Name: "Amount", DataType: "BIGINT"},
	}}
	return s
}

func resolveQuery(t *testing.T, s *Schema, input string) error {
	t.Helper()
	q, err := parser.Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	return (&Resolver{Schema: s}).Resolve(q)
}

func TestResolverRejectsUnknownAndCircularMeasures(t *testing.T) {
	s := resolverSchema()
	if err := resolveQuery(t, s, `EVALUATE ROW("X", [Missing])`); err == nil || !strings.Contains(err.Error(), "unknown measure") {
		t.Fatalf("unknown measure error = %v", err)
	}
	if err := s.AddMeasureFromExpr("Sales", "A", `[B] + 1`); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMeasureFromExpr("Sales", "B", `[A] + 1`); err != nil {
		t.Fatal(err)
	}
	if err := resolveQuery(t, s, `EVALUATE ROW("X", [A])`); err == nil || !strings.Contains(err.Error(), "circular measure") {
		t.Fatalf("circular measure error = %v", err)
	}
}

func TestResolverUsesDAXCaseInsensitiveNames(t *testing.T) {
	s := resolverSchema()
	if err := s.AddMeasureFromExpr("Sales", "Total", `SUM(sales[amount])`); err != nil {
		t.Fatal(err)
	}
	if err := resolveQuery(t, s, `EVALUATE ROW("X", [total])`); err != nil {
		t.Fatal(err)
	}
}
