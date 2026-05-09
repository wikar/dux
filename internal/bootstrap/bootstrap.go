// Package bootstrap provides shared startup logic for the dux CLI and duxd
// server: opening the metadata database, attaching data files, introspecting
// schemas, and importing/exporting TOML configuration.
package bootstrap

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/danielwikar/dux/semantic"
)

// Bootstrap performs the common startup sequence shared by both the dux CLI
// and the duxd server:
//
//  1. Open (or create) the metadata database at metaPath.
//  2. Attach all *.duckdb / *.db files in dbDir (except the metadata DB).
//  3. Introspect the schema from all attached databases.
//  4. Load persisted metadata (relationships + measures) from the metadata DB.
//  5. Load dux.toml (if present) to supplement the metadata DB.
//
// The caller is responsible for closing the returned MetadataDB.
func Bootstrap(dbDir, metaPath, tomlPath string) (*semantic.MetadataDB, *sql.DB, *semantic.Schema, error) {
	metaDB, err := semantic.OpenMetadataDB(metaPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open metadata db: %w", err)
	}

	db := metaDB.DB()

	if err := AttachDataDBs(db, dbDir, metaPath); err != nil {
		metaDB.Close()
		return nil, nil, nil, fmt.Errorf("attach data databases: %w", err)
	}

	schema, err := semantic.IntrospectDuckDB(db)
	if err != nil {
		metaDB.Close()
		return nil, nil, nil, fmt.Errorf("introspect schema: %w", err)
	}

	if err := metaDB.LoadIntoSchema(schema); err != nil {
		metaDB.Close()
		return nil, nil, nil, fmt.Errorf("load metadata: %w", err)
	}

	if err := semantic.LoadDuxTOML(tomlPath, schema); err != nil {
		log.Printf("warning: loading dux.toml: %v", err)
	}

	return metaDB, db, schema, nil
}

// AttachDataDBs attaches every *.duckdb and *.db file in dir (except the
// metadata DB itself) to db as a read-only named attachment.
// The attachment alias is the filename stem (e.g. "atp.duckdb" → alias "atp").
func AttachDataDBs(db *sql.DB, dir, metaPath string) error {
	absMetaPath, _ := filepath.Abs(metaPath)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // db-dir not created yet — no-op
		}
		return fmt.Errorf("read db-dir %q: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".duckdb" && ext != ".db" {
			continue
		}

		absPath, _ := filepath.Abs(filepath.Join(dir, name))
		if absPath == absMetaPath {
			continue // skip the metadata DB itself
		}

		stem := strings.TrimSuffix(name, filepath.Ext(name))
		escapedPath := strings.ReplaceAll(absPath, "'", "''")
		quotedStem := `"` + strings.ReplaceAll(stem, `"`, `""`) + `"`
		q := fmt.Sprintf("ATTACH '%s' AS %s (READ_ONLY)", escapedPath, quotedStem)
		if _, err := db.Exec(q); err != nil {
			log.Printf("warning: attach %q as %q: %v", absPath, stem, err)
		} else {
			log.Printf("attached %q as %q (read-only)", name, stem)
		}
	}
	return nil
}

// ImportTOML parses a dux.toml file, persists its contents to the metadata
// database, and reloads the schema with the newly imported data.
func ImportTOML(metaDB *semantic.MetadataDB, path string, schema *semantic.Schema) error {
	importSchema := semantic.NewSchema()
	if err := semantic.LoadDuxTOML(path, importSchema); err != nil {
		return fmt.Errorf("parse %q: %w", path, err)
	}
	if err := metaDB.ReplaceAllFromSchema(importSchema); err != nil {
		return fmt.Errorf("write to metadata DB: %w", err)
	}
	if err := metaDB.LoadIntoSchema(schema); err != nil {
		return fmt.Errorf("reload schema: %w", err)
	}
	log.Printf("imported %q into metadata DB", path)
	return nil
}

// ExportTOML writes the current schema (measures + relationships) as a
// dux.toml file at the given path.
func ExportTOML(path string, schema *semantic.Schema) error {
	if err := semantic.WriteDuxTOML(path, schema); err != nil {
		return err
	}
	log.Printf("exported schema to %q", path)
	return nil
}
