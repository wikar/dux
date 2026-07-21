package semantic

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestIntrospectRelationshipsKeepsCatalogsSeparate(t *testing.T) {
	root := t.TempDir()
	aPath := filepath.Join(root, "a.duckdb")
	bPath := filepath.Join(root, "b.duckdb")
	createRelatedTables(t, aPath)
	createRelatedTables(t, bPath)

	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for alias, path := range map[string]string{"a": aPath, "b": bPath} {
		path = strings.ReplaceAll(path, "'", "''")
		if _, err := db.Exec(fmt.Sprintf("ATTACH '%s' AS %s (READ_ONLY)", path, alias)); err != nil {
			t.Fatal(err)
		}
	}

	schema, err := IntrospectDuckDB(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(schema.Relationships) != 2 {
		t.Fatalf("got %d relationships, want 2: %#v", len(schema.Relationships), schema.Relationships)
	}
	want := map[string]bool{
		"a.child.parent_id->a.parent.id": true,
		"b.child.parent_id->b.parent.id": true,
	}
	for _, rel := range schema.Relationships {
		key := fmt.Sprintf("%s.%s->%s.%s", rel.FromTable, rel.FromColumn, rel.ToTable, rel.ToColumn)
		if !want[key] {
			t.Errorf("unexpected relationship %s", key)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing relationships: %v", want)
	}
}

func createRelatedTables(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE parent(id INTEGER PRIMARY KEY);
		CREATE TABLE child(id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id));
	`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
