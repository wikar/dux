package semantic

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/duckdb/duckdb-go/v2"
)

// MetadataDB wraps a transient DuckDB connection with dux.sqlite attached and provides
// helpers to persist and load measures and relationships.
type MetadataDB struct {
	db *sql.DB
}

// OpenMetadataDB opens (or creates) a SQLite database at path through DuckDB's
// SQLite extension. No native DuckDB file is persisted.
func OpenMetadataDB(path string) (*MetadataDB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create metadata directory: %w", err)
	}
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open metadata db %q: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`LOAD sqlite`); err != nil {
		if _, err := db.Exec(`INSTALL sqlite`); err != nil {
			db.Close()
			return nil, fmt.Errorf("install SQLite extension: %w", err)
		}
		if _, err := db.Exec(`LOAD sqlite`); err != nil {
			db.Close()
			return nil, fmt.Errorf("load SQLite extension: %w", err)
		}
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("resolve metadata db %q: %w", path, err)
	}
	quotedPath := "'" + strings.ReplaceAll(filepath.ToSlash(absPath), "'", "''") + "'"
	if _, err := db.Exec(`ATTACH ` + quotedPath + ` AS dux_meta (TYPE sqlite)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("attach metadata db %q: %w", path, err)
	}
	m := &MetadataDB{db: db}
	if err := m.initSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return m, nil
}

// Close releases the underlying database connection.
func (m *MetadataDB) Close() error {
	return m.db.Close()
}

// DB returns the transient connection used to access the attached SQLite store.
func (m *MetadataDB) DB() *sql.DB {
	return m.db
}

// initSchema creates the metadata tables if they do not already exist.
func (m *MetadataDB) initSchema() error {
	const ddl = `
	CREATE TABLE IF NOT EXISTS dux_meta.dux_relationships (
		id          INTEGER PRIMARY KEY,
		from_table  TEXT NOT NULL,
		from_column TEXT NOT NULL,
		to_table    TEXT NOT NULL,
		to_column   TEXT NOT NULL,
		bidirectional BOOLEAN NOT NULL DEFAULT FALSE,
		UNIQUE (from_table, from_column, to_table, to_column)
	);

	CREATE TABLE IF NOT EXISTS dux_meta.dux_measures (
		id         INTEGER PRIMARY KEY,
		table_name TEXT NOT NULL,
		name       TEXT NOT NULL,
		expression TEXT NOT NULL,
		format     TEXT,
		UNIQUE (table_name, name)
	);

	CREATE TABLE IF NOT EXISTS dux_meta.dux_date_tables (
		table_name  TEXT PRIMARY KEY,
		column_name TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS dux_meta.dux_hidden (
		table_name  TEXT NOT NULL,
		column_name TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (table_name, column_name)
	);

	CREATE TABLE IF NOT EXISTS dux_meta.dux_maintenance_runs (
		id TEXT PRIMARY KEY,
		operation TEXT NOT NULL,
		source TEXT NOT NULL,
		status TEXT NOT NULL,
		requested_at TEXT NOT NULL,
		started_at TEXT,
		finished_at TEXT,
		summary_json TEXT,
		error TEXT
	);

	CREATE TABLE IF NOT EXISTS dux_meta.dux_imports (
		id TEXT PRIMARY KEY,
		idempotency_key TEXT UNIQUE,
		request_hash TEXT NOT NULL,
		schema_name TEXT NOT NULL,
		table_name TEXT NOT NULL,
		status TEXT NOT NULL,
		create_if_missing BOOLEAN NOT NULL,
		file_count INTEGER NOT NULL,
		requested_at TEXT NOT NULL,
		started_at TEXT,
		finished_at TEXT,
		files_json TEXT NOT NULL,
		summary_json TEXT,
		error TEXT
	);

	CREATE TABLE IF NOT EXISTS dux_meta.dux_import_files (
		import_id TEXT NOT NULL,
		schema_name TEXT NOT NULL,
		table_name TEXT NOT NULL,
		source_path TEXT NOT NULL,
		target_path TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		size_bytes BIGINT NOT NULL,
		row_count BIGINT NOT NULL,
		PRIMARY KEY (import_id, target_path),
		UNIQUE (schema_name, table_name, sha256)
	);
	`
	if _, err := m.db.Exec(ddl); err != nil {
		return fmt.Errorf("init metadata schema: %w", err)
	}
	return nil
}

// ─── Load ─────────────────────────────────────────────────────────────────────

// LoadIntoSchema reads all rows from the metadata tables and merges them into
// schema. Call this after IntrospectDuckDB to layer in the stored metadata.
func (m *MetadataDB) LoadIntoSchema(schema *Schema) error {
	if err := m.loadRelationships(schema); err != nil {
		return err
	}
	if err := m.loadMeasures(schema); err != nil {
		return err
	}
	if err := m.loadDateTables(schema); err != nil {
		return err
	}
	return m.loadHidden(schema)
}

// loadHidden reads the hidden designations. An empty column_name marks the
// whole table (or view) as hidden.
func (m *MetadataDB) loadHidden(schema *Schema) error {
	rows, err := m.db.Query(`SELECT table_name, column_name FROM dux_meta.dux_hidden ORDER BY table_name, column_name`)
	if err != nil {
		return fmt.Errorf("load hidden: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tableName, columnName string
		if err := rows.Scan(&tableName, &columnName); err != nil {
			return fmt.Errorf("scan hidden: %w", err)
		}
		if columnName == "" {
			schema.SetTableHidden(tableName, true)
		} else {
			schema.SetColumnHidden(tableName, columnName, true)
		}
	}
	return rows.Err()
}

func (m *MetadataDB) loadDateTables(schema *Schema) error {
	rows, err := m.db.Query(`SELECT table_name, column_name FROM dux_meta.dux_date_tables ORDER BY table_name`)
	if err != nil {
		return fmt.Errorf("load date tables: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tableName, columnName string
		if err := rows.Scan(&tableName, &columnName); err != nil {
			return fmt.Errorf("scan date table: %w", err)
		}
		schema.SetDateTable(tableName, columnName)
	}
	return rows.Err()
}

func (m *MetadataDB) loadRelationships(schema *Schema) error {
	rows, err := m.db.Query(`
		SELECT from_table, from_column, to_table, to_column, bidirectional
		FROM dux_meta.dux_relationships
		ORDER BY id
	`)
	if err != nil {
		return fmt.Errorf("load relationships: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var fromTable, fromColumn, toTable, toColumn string
		var bidirectional sql.NullBool
		if err := rows.Scan(&fromTable, &fromColumn, &toTable, &toColumn, &bidirectional); err != nil {
			return fmt.Errorf("scan relationship: %w", err)
		}
		schema.Relationships = append(schema.Relationships, &Relationship{
			FromTable:     fromTable,
			FromColumn:    fromColumn,
			ToTable:       toTable,
			ToColumn:      toColumn,
			Bidirectional: bidirectional.Bool,
		})
	}
	return rows.Err()
}

func (m *MetadataDB) loadMeasures(schema *Schema) error {
	rows, err := m.db.Query(`
		SELECT table_name, name, expression, format
		FROM dux_meta.dux_measures
		ORDER BY table_name, name
	`)
	if err != nil {
		return fmt.Errorf("load measures: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tableName, name, expression string
		var format sql.NullString
		if err := rows.Scan(&tableName, &name, &expression, &format); err != nil {
			return fmt.Errorf("scan measure: %w", err)
		}
		if err := schema.AddMeasureFromExpr(tableName, name, expression); err != nil {
			return fmt.Errorf("parse stored measure %q.%q: %w", tableName, name, err)
		}
		if format.Valid && format.String != "" {
			var f MeasureFormat
			if err := json.Unmarshal([]byte(format.String), &f); err != nil {
				return fmt.Errorf("parse stored format for %q.%q: %w", tableName, name, err)
			}
			schema.SetMeasureFormat(tableName, name, &f)
		}
	}
	return rows.Err()
}

// ─── Save ─────────────────────────────────────────────────────────────────────

// SaveRelationship inserts or replaces a relationship in the metadata DB.
func (m *MetadataDB) SaveRelationship(fromTable, fromColumn, toTable, toColumn string, bidirectional bool) error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("save relationship: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	result, err := tx.Exec(`
		UPDATE dux_meta.dux_relationships SET bidirectional = ?
		WHERE from_table = ? AND from_column = ? AND to_table = ? AND to_column = ?
	`, bidirectional, fromTable, fromColumn, toTable, toColumn)
	if err != nil {
		return fmt.Errorf("save relationship: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("save relationship: %w", err)
	}
	if affected == 0 {
		if _, err := tx.Exec(`
			INSERT INTO dux_meta.dux_relationships (from_table, from_column, to_table, to_column, bidirectional)
			VALUES (?, ?, ?, ?, ?)
		`, fromTable, fromColumn, toTable, toColumn, bidirectional); err != nil {
			return fmt.Errorf("save relationship: %w", err)
		}
	}
	return tx.Commit()
}

// SaveMeasure inserts or replaces a measure in the metadata DB.
// expression should be the raw DUX expression string (e.g. "SUM(bev.Sales[NetRevenue])").
// format is the optional display format; nil clears any stored format.
func (m *MetadataDB) SaveMeasure(tableName, name, expression string, format *MeasureFormat) error {
	var formatJSON any // NULL when no format
	if format != nil {
		data, err := json.Marshal(format)
		if err != nil {
			return fmt.Errorf("encode measure format: %w", err)
		}
		formatJSON = string(data)
	}
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("save measure: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	result, err := tx.Exec(`
		UPDATE dux_meta.dux_measures SET expression = ?, format = ?
		WHERE table_name = ? AND name = ?
	`, expression, formatJSON, tableName, name)
	if err != nil {
		return fmt.Errorf("save measure: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("save measure: %w", err)
	}
	if affected == 0 {
		if _, err := tx.Exec(`
			INSERT INTO dux_meta.dux_measures (table_name, name, expression, format)
			VALUES (?, ?, ?, ?)
		`, tableName, name, expression, formatJSON); err != nil {
			return fmt.Errorf("save measure: %w", err)
		}
	}
	return tx.Commit()
}

// DeleteMeasure removes a measure from the metadata DB.
func (m *MetadataDB) DeleteMeasure(tableName, name string) error {
	_, err := m.db.Exec(`DELETE FROM dux_meta.dux_measures WHERE table_name = ? AND name = ?`, tableName, name)
	return err
}

// SaveDateTable inserts or replaces a date-table designation in the metadata DB.
func (m *MetadataDB) SaveDateTable(tableName, columnName string) error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("save date table: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	result, err := tx.Exec(`UPDATE dux_meta.dux_date_tables SET column_name = ? WHERE table_name = ?`, columnName, tableName)
	if err != nil {
		return fmt.Errorf("save date table: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("save date table: %w", err)
	}
	if affected == 0 {
		if _, err := tx.Exec(`INSERT INTO dux_meta.dux_date_tables (table_name, column_name) VALUES (?, ?)`, tableName, columnName); err != nil {
			return fmt.Errorf("save date table: %w", err)
		}
	}
	return tx.Commit()
}

// DeleteDateTable removes a date-table designation from the metadata DB.
func (m *MetadataDB) DeleteDateTable(tableName string) error {
	_, err := m.db.Exec(`DELETE FROM dux_meta.dux_date_tables WHERE table_name = ?`, tableName)
	return err
}

// ReplaceDateTable atomically replaces every date-table designation.
func (m *MetadataDB) ReplaceDateTable(tableName, columnName string) error {
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`DELETE FROM dux_meta.dux_date_tables`); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO dux_meta.dux_date_tables (table_name, column_name) VALUES (?, ?)`, tableName, columnName); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearDateTables atomically removes every date-table designation.
func (m *MetadataDB) ClearDateTables() error {
	_, err := m.db.Exec(`DELETE FROM dux_meta.dux_date_tables`)
	return err
}

// SaveHidden marks a table (columnName == "") or a single column as hidden.
func (m *MetadataDB) SaveHidden(tableName, columnName string) error {
	_, err := m.db.Exec(`
		INSERT INTO dux_meta.dux_hidden (table_name, column_name)
		SELECT ?, ?
		WHERE NOT EXISTS (
			SELECT 1 FROM dux_meta.dux_hidden WHERE table_name = ? AND column_name = ?
		)
	`, tableName, columnName, tableName, columnName)
	if err != nil {
		return fmt.Errorf("save hidden: %w", err)
	}
	return nil
}

// DeleteHidden removes a hidden designation for a table (columnName == "") or column.
func (m *MetadataDB) DeleteHidden(tableName, columnName string) error {
	_, err := m.db.Exec(`DELETE FROM dux_meta.dux_hidden WHERE table_name = ? AND column_name = ?`, tableName, columnName)
	return err
}

// DeleteRelationship removes a relationship from the metadata DB.
func (m *MetadataDB) DeleteRelationship(fromTable, fromColumn, toTable, toColumn string) error {
	_, err := m.db.Exec(`
		DELETE FROM dux_meta.dux_relationships
		WHERE from_table = ? AND from_column = ? AND to_table = ? AND to_column = ?
	`, fromTable, fromColumn, toTable, toColumn)
	return err
}

// ─── Replace all ─────────────────────────────────────────────────────────────

// ReplaceAllFromSchema replaces all measures and relationships in the metadata
// DB with the current contents of schema. Used during import.
func (m *MetadataDB) ReplaceAllFromSchema(schema *Schema) error {
	tx, err := m.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec(`DELETE FROM dux_meta.dux_relationships`); err != nil {
		return fmt.Errorf("clear relationships: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM dux_meta.dux_measures`); err != nil {
		return fmt.Errorf("clear measures: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM dux_meta.dux_date_tables`); err != nil {
		return fmt.Errorf("clear date tables: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM dux_meta.dux_hidden`); err != nil {
		return fmt.Errorf("clear hidden: %w", err)
	}

	for _, r := range schema.Relationships {
		if _, err := tx.Exec(`
			INSERT INTO dux_meta.dux_relationships (from_table, from_column, to_table, to_column, bidirectional)
			VALUES (?, ?, ?, ?, ?)
		`, r.FromTable, r.FromColumn, r.ToTable, r.ToColumn, r.Bidirectional); err != nil {
			return fmt.Errorf("insert relationship: %w", err)
		}
	}

	for tableName, defs := range schema.Measures {
		for measureName, def := range defs {
			var formatJSON any // NULL when no format
			if f := schema.MeasureFormatFor(tableName, measureName); f != nil {
				data, err := json.Marshal(f)
				if err != nil {
					return fmt.Errorf("encode format for measure %q: %w", measureName, err)
				}
				formatJSON = string(data)
			}
			if _, err := tx.Exec(`
				INSERT INTO dux_meta.dux_measures (table_name, name, expression, format)
				VALUES (?, ?, ?, ?)
			`, tableName, measureName, def.Expression, formatJSON); err != nil {
				return fmt.Errorf("insert measure %q: %w", measureName, err)
			}
		}
	}

	for tableName, columnName := range schema.DateTables {
		if _, err := tx.Exec(`
			INSERT INTO dux_meta.dux_date_tables (table_name, column_name) VALUES (?, ?)
		`, tableName, columnName); err != nil {
			return fmt.Errorf("insert date table %q: %w", tableName, err)
		}
	}

	for tableName := range schema.HiddenTables {
		if _, err := tx.Exec(`
			INSERT INTO dux_meta.dux_hidden (table_name, column_name) VALUES (?, '')
		`, tableName); err != nil {
			return fmt.Errorf("insert hidden table %q: %w", tableName, err)
		}
	}
	for tableName, cols := range schema.HiddenColumns {
		for columnName := range cols {
			if _, err := tx.Exec(`
				INSERT INTO dux_meta.dux_hidden (table_name, column_name) VALUES (?, ?)
			`, tableName, columnName); err != nil {
				return fmt.Errorf("insert hidden column %q.%q: %w", tableName, columnName, err)
			}
		}
	}

	return tx.Commit()
}
