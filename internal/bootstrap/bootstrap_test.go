package bootstrap

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/danielwikar/dux/semantic"
)

func TestReattachDataDBsPicksUpReplacedFile(t *testing.T) {
	root := t.TempDir()
	dbDir := filepath.Join(root, "db")
	if err := os.Mkdir(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(dbDir, "dux.duckdb")
	dataPath := filepath.Join(dbDir, "data.duckdb")
	createTestDB(t, dataPath, `CREATE TABLE old_table (id INTEGER)`)

	metaDB, err := semantic.OpenMetadataDB(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = metaDB.Close() })
	if err := AttachDataDBs(metaDB.DB(), dbDir, metaPath); err != nil {
		t.Fatal(err)
	}
	assertTable(t, metaDB.DB(), "data.old_table", true)

	replacementDir := filepath.Join(root, "replacement")
	if err := os.Mkdir(replacementDir, 0o755); err != nil {
		t.Fatal(err)
	}
	replacementPath := filepath.Join(replacementDir, "data.duckdb")
	createTestDB(t, replacementPath, `CREATE TABLE new_table (id INTEGER, name VARCHAR)`)
	if err := os.Rename(dataPath, filepath.Join(root, "old-data.duckdb")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementPath, dataPath); err != nil {
		t.Fatal(err)
	}

	// The existing attachment still exposes the old catalog until refresh.
	assertTable(t, metaDB.DB(), "data.old_table", true)
	assertTable(t, metaDB.DB(), "data.new_table", false)

	if err := ReattachDataDBs(metaDB.DB(), dbDir, metaPath); err != nil {
		t.Fatal(err)
	}
	assertTable(t, metaDB.DB(), "data.old_table", false)
	assertTable(t, metaDB.DB(), "data.new_table", true)
}

func createTestDB(t *testing.T, path, ddl string) {
	t.Helper()
	db, err := sql.Open("duckdb", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ddl); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertTable(t *testing.T, db *sql.DB, name string, want bool) {
	t.Helper()
	schema, err := semantic.IntrospectDuckDB(db)
	if err != nil {
		t.Fatal(err)
	}
	_, got := schema.Tables[name]
	if got != want {
		t.Fatalf("table %q present = %v, want %v", name, got, want)
	}
}
