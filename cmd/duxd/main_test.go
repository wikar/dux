package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/danielwikar/dux/semantic"
)

func TestBodyLimit(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(strings.Repeat("x", maxRequestBodyBytes+1)))
	_, err := readBody(httptest.NewRecorder(), req)
	if err == nil || bodyErrorStatus(err) != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 body error, got %v", err)
	}
}

func TestMergeRelationshipsDeduplicates(t *testing.T) {
	r := &semantic.Relationship{FromTable: "a", FromColumn: "id", ToTable: "b", ToColumn: "a_id"}
	schema := semantic.NewSchema()
	schema.Relationships = []*semantic.Relationship{r}
	mergeRelationships(schema, []*semantic.Relationship{r})
	if len(schema.Relationships) != 1 {
		t.Fatalf("expected one relationship, got %d", len(schema.Relationships))
	}
}

func TestReplaceSchemaIncludesMeasureFormats(t *testing.T) {
	dst, src := semantic.NewSchema(), semantic.NewSchema()
	src.SetMeasureFormat("sales", "Revenue", &semantic.MeasureFormat{Kind: "currency", Currency: "SEK"})
	replaceSchema(dst, src)
	if got := dst.MeasureFormatFor("sales", "Revenue"); got == nil || got.Currency != "SEK" {
		t.Fatalf("measure format was not replaced: %#v", got)
	}
}

func TestClearSchemaMetadataRemovesMeasureFormats(t *testing.T) {
	schema := semantic.NewSchema()
	schema.SetMeasureFormat("sales", "Revenue", &semantic.MeasureFormat{Kind: "currency", Currency: "SEK"})
	clearSchemaMetadata(schema)
	if got := schema.MeasureFormatFor("sales", "Revenue"); got != nil {
		t.Fatalf("stale measure format survived metadata reset: %#v", got)
	}
}

func TestRejectedRelationshipUpdatePreservesStoredValue(t *testing.T) {
	metaDB, err := semantic.OpenMetadataDB(t.TempDir() + "/dux.duckdb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = metaDB.Close() })

	rels := []*semantic.Relationship{
		{FromTable: "a", FromColumn: "id", ToTable: "b", ToColumn: "a_id"},
		{FromTable: "b", FromColumn: "id", ToTable: "c", ToColumn: "b_id"},
		{FromTable: "a", FromColumn: "id", ToTable: "c", ToColumn: "a_id"},
	}
	for _, r := range rels {
		if err := metaDB.SaveRelationship(r.FromTable, r.FromColumn, r.ToTable, r.ToColumn, false); err != nil {
			t.Fatal(err)
		}
	}
	schema := semantic.NewSchema()
	schema.Relationships = rels

	body := `{"from_table":"a","from_column":"id","to_table":"c","to_column":"a_id","bidirectional":true}`
	rec := httptest.NewRecorder()
	addRelationshipHandler(metaDB, schema, &sync.RWMutex{})(rec, httptest.NewRequest(http.MethodPost, "/relationships", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected rejected update, got %d: %s", rec.Code, rec.Body.String())
	}
	if schema.Relationships[2].Bidirectional {
		t.Fatal("in-memory relationship was not restored")
	}

	loaded := semantic.NewSchema()
	if err := metaDB.LoadIntoSchema(loaded); err != nil {
		t.Fatal(err)
	}
	if len(loaded.Relationships) != 3 || loaded.Relationships[2].Bidirectional {
		t.Fatalf("stored relationship was not preserved: %#v", loaded.Relationships)
	}
}
