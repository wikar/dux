package semantic_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielwikar/dux/semantic"
)

func TestDateTableTOMLRoundTrip(t *testing.T) {
	schema := semantic.NewSchema()
	if err := semantic.LoadDuxTOMLBytes([]byte(`
[[date_table]]
table  = "Dates"
column = "date"
`), schema); err != nil {
		t.Fatalf("load: %v", err)
	}

	col, ok := schema.DateColumn("dates")
	if !ok || col != "date" {
		t.Fatalf("expected dates → date designation, got (%q, %v)", col, ok)
	}
	// Lookup is case-insensitive on the table name.
	if _, ok := schema.DateColumn("DATES"); !ok {
		t.Error("expected case-insensitive date table lookup")
	}

	out, err := semantic.ExportDuxTOML(schema)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.Contains(string(out), "[[date_table]]") ||
		!strings.Contains(string(out), `column = "date"`) {
		t.Errorf("export missing date_table entry:\n%s", out)
	}

	// Re-import the export.
	schema2 := semantic.NewSchema()
	if err := semantic.LoadDuxTOMLBytes(out, schema2); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if col, ok := schema2.DateColumn("dates"); !ok || col != "date" {
		t.Errorf("round trip lost designation, got (%q, %v)", col, ok)
	}
}

func TestDateTableMetadataRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.sqlite")
	m, err := semantic.OpenMetadataDB(path)
	if err != nil {
		t.Fatalf("open metadata db: %v", err)
	}
	defer m.Close()

	if err := m.SaveDateTable("dates", "date"); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Upsert replaces the column.
	if err := m.SaveDateTable("dates", "day"); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	schema := semantic.NewSchema()
	if err := m.LoadIntoSchema(schema); err != nil {
		t.Fatalf("load into schema: %v", err)
	}
	if col, ok := schema.DateColumn("dates"); !ok || col != "day" {
		t.Fatalf("expected dates → day after upsert, got (%q, %v)", col, ok)
	}

	// ReplaceAllFromSchema persists designations.
	fresh := semantic.NewSchema()
	fresh.SetDateTable("calendar", "d")
	if err := m.ReplaceAllFromSchema(fresh); err != nil {
		t.Fatalf("replace all: %v", err)
	}
	schema2 := semantic.NewSchema()
	if err := m.LoadIntoSchema(schema2); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := schema2.DateColumn("dates"); ok {
		t.Error("old designation should have been cleared by replace")
	}
	if col, ok := schema2.DateColumn("calendar"); !ok || col != "d" {
		t.Errorf("expected calendar → d, got (%q, %v)", col, ok)
	}

	// Delete removes it.
	if err := m.DeleteDateTable("calendar"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	schema3 := semantic.NewSchema()
	if err := m.LoadIntoSchema(schema3); err != nil {
		t.Fatalf("reload after delete: %v", err)
	}
	if _, ok := schema3.DateColumn("calendar"); ok {
		t.Error("designation should be gone after delete")
	}
}
