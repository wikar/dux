package semantic_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielwikar/dux/semantic"
)

func intPtr(n int) *int { return &n }

func TestMeasureFormatValidate(t *testing.T) {
	valid := []semantic.MeasureFormat{
		{Kind: "number"},
		{Kind: "decimal", Decimals: intPtr(2)},
		{Kind: "percent", Decimals: intPtr(1)},
		{Kind: "currency", Currency: "SEK", Decimals: intPtr(0)},
		{Kind: "compact"},
	}
	for _, f := range valid {
		if err := f.Validate(); err != nil {
			t.Errorf("expected %+v to be valid, got %v", f, err)
		}
	}

	invalid := []semantic.MeasureFormat{
		{Kind: "money"},                            // unknown kind
		{Kind: ""},                                 // missing kind
		{Kind: "decimal", Decimals: intPtr(11)},    // out of range
		{Kind: "decimal", Decimals: intPtr(-1)},    // out of range
		{Kind: "currency"},                         // missing currency code
		{Kind: "currency", Currency: "KRONOR"},     // not 3 letters
		{Kind: "number", Currency: "SEK"},          // currency on non-currency kind
	}
	for _, f := range invalid {
		if err := f.Validate(); err == nil {
			t.Errorf("expected %+v to be invalid", f)
		}
	}
}

func TestMeasureFormatTOMLRoundTrip(t *testing.T) {
	schema := semantic.NewSchema()
	if err := semantic.LoadDuxTOMLBytes([]byte(`
[[measure]]
table      = "sales"
name       = "Revenue"
expression = "SUM(sales[amount])"
  [measure.format]
  kind     = "currency"
  decimals = 0
  currency = "SEK"

[[measure]]
table      = "sales"
name       = "Margin"
expression = "DIVIDE(SUM(sales[profit]), SUM(sales[amount]))"
`), schema); err != nil {
		t.Fatalf("load: %v", err)
	}

	f := schema.MeasureFormatFor("sales", "Revenue")
	if f == nil || f.Kind != "currency" || f.Currency != "SEK" || f.Decimals == nil || *f.Decimals != 0 {
		t.Fatalf("expected currency/SEK/0 format, got %+v", f)
	}
	if schema.MeasureFormatFor("sales", "Margin") != nil {
		t.Error("Margin should have no format")
	}

	out, err := semantic.ExportDuxTOML(schema)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.Contains(string(out), "[measure.format]") ||
		!strings.Contains(string(out), `currency = "SEK"`) {
		t.Errorf("export missing measure format:\n%s", out)
	}

	// Re-import the export: format survives, format-less measure stays bare.
	schema2 := semantic.NewSchema()
	if err := semantic.LoadDuxTOMLBytes(out, schema2); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	f2 := schema2.MeasureFormatFor("sales", "Revenue")
	if f2 == nil || f2.Kind != "currency" || f2.Currency != "SEK" || f2.Decimals == nil || *f2.Decimals != 0 {
		t.Errorf("round trip lost format, got %+v", f2)
	}
	if schema2.MeasureFormatFor("sales", "Margin") != nil {
		t.Error("round trip invented a format for Margin")
	}
}

func TestMeasureFormatTOMLInvalid(t *testing.T) {
	schema := semantic.NewSchema()
	err := semantic.LoadDuxTOMLBytes([]byte(`
[[measure]]
table      = "sales"
name       = "Revenue"
expression = "SUM(sales[amount])"
  [measure.format]
  kind = "money"
`), schema)
	if err == nil || !strings.Contains(err.Error(), "format kind") {
		t.Fatalf("expected invalid-kind error, got %v", err)
	}
}

func TestMeasureFormatMetadataRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.duckdb")
	m, err := semantic.OpenMetadataDB(path)
	if err != nil {
		t.Fatalf("open metadata db: %v", err)
	}
	defer m.Close()

	format := &semantic.MeasureFormat{Kind: "percent", Decimals: intPtr(1)}
	if err := m.SaveMeasure("sales", "Margin", "DIVIDE(SUM(sales[profit]), SUM(sales[amount]))", format); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := m.SaveMeasure("sales", "Revenue", "SUM(sales[amount])", nil); err != nil {
		t.Fatalf("save without format: %v", err)
	}

	schema := semantic.NewSchema()
	if err := m.LoadIntoSchema(schema); err != nil {
		t.Fatalf("load into schema: %v", err)
	}
	f := schema.MeasureFormatFor("sales", "Margin")
	if f == nil || f.Kind != "percent" || f.Decimals == nil || *f.Decimals != 1 {
		t.Fatalf("expected percent/1 format after reload, got %+v", f)
	}
	if schema.MeasureFormatFor("sales", "Revenue") != nil {
		t.Error("Revenue should have no format")
	}

	// Upsert with nil format clears the stored format.
	if err := m.SaveMeasure("sales", "Margin", "DIVIDE(SUM(sales[profit]), SUM(sales[amount]))", nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	schema2 := semantic.NewSchema()
	if err := m.LoadIntoSchema(schema2); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if schema2.MeasureFormatFor("sales", "Margin") != nil {
		t.Error("nil-format upsert should clear the stored format")
	}

	// ReplaceAllFromSchema (import path) carries formats through.
	fresh := semantic.NewSchema()
	if err := fresh.AddMeasureFromExpr("sales", "Total", "SUM(sales[amount])"); err != nil {
		t.Fatalf("add measure: %v", err)
	}
	fresh.SetMeasureFormat("sales", "Total", &semantic.MeasureFormat{Kind: "compact"})
	if err := m.ReplaceAllFromSchema(fresh); err != nil {
		t.Fatalf("replace all: %v", err)
	}
	schema3 := semantic.NewSchema()
	if err := m.LoadIntoSchema(schema3); err != nil {
		t.Fatalf("reload after replace: %v", err)
	}
	if f := schema3.MeasureFormatFor("sales", "Total"); f == nil || f.Kind != "compact" {
		t.Errorf("ReplaceAllFromSchema lost the format, got %+v", f)
	}
}
