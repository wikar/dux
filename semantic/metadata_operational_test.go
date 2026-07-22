package semantic

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestOperationalMetadataSchema(t *testing.T) {
	metadata, err := OpenMetadataDB(filepath.Join(t.TempDir(), "dux.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer metadata.Close()
	for table, want := range map[string]string{
		"dux_maintenance_runs": "[id operation source status requested_at started_at finished_at summary_json error]",
		"dux_imports":          "[id idempotency_key request_hash schema_name table_name status create_if_missing file_count requested_at started_at finished_at files_json summary_json error]",
		"dux_import_files":     "[import_id schema_name table_name source_path target_path sha256 size_bytes row_count]",
	} {
		rows, err := metadata.DB().Query(`SELECT column_name FROM information_schema.columns WHERE table_catalog = 'dux_meta' AND table_name = ? ORDER BY ordinal_position`, table)
		if err != nil {
			t.Fatal(err)
		}
		var columns []string
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			columns = append(columns, column)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprint(columns); got != want {
			t.Fatalf("%s columns = %s, want %s", table, got, want)
		}
	}
}

func TestSemanticMetadataSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dux.sqlite")
	metadata, err := OpenMetadataDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := metadata.SaveMeasure("sales", "Revenue", "SUM(sales[amount])", &MeasureFormat{Kind: "currency", Currency: "SEK"}); err != nil {
		t.Fatal(err)
	}
	if err := metadata.SaveRelationship("sales", "customer_id", "customers", "id", false); err != nil {
		t.Fatal(err)
	}
	if err := metadata.SaveDateTable("dates", "date"); err != nil {
		t.Fatal(err)
	}
	if err := metadata.SaveHidden("sales", "internal_id"); err != nil {
		t.Fatal(err)
	}
	if err := metadata.Close(); err != nil {
		t.Fatal(err)
	}
	metadata, err = OpenMetadataDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer metadata.Close()
	schema := NewSchema()
	if err := metadata.LoadIntoSchema(schema); err != nil {
		t.Fatal(err)
	}
	format := schema.MeasureFormatFor("sales", "Revenue")
	if len(schema.Relationships) != 1 || schema.Measures["sales"]["Revenue"] == nil || format == nil || format.Currency != "SEK" {
		t.Fatalf("semantic metadata after restart = %#v", schema)
	}
	if column, ok := schema.DateColumn("dates"); !ok || column != "date" || !schema.HiddenColumns["sales"]["internal_id"] {
		t.Fatalf("date/hidden metadata after restart = %#v %#v", schema.DateTables, schema.HiddenColumns)
	}
}
