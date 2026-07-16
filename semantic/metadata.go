package semantic

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/duckdb/duckdb-go/v2"
)

// MetadataDB wraps the read-write dux.duckdb connection and provides
// helpers to persist and load measures and relationships.
type MetadataDB struct {
	db *sql.DB
}

// OpenMetadataDB opens (or creates) a DuckDB database at path for use as the
// dux metadata store. The two metadata tables are created if they do not exist.
func OpenMetadataDB(path string) (*MetadataDB, error) {
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, fmt.Errorf("open metadata db %q: %w", path, err)
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

// DB returns the underlying *sql.DB for use as the primary query connection
// when dux.duckdb acts as the main database and data DBs are attached to it.
func (m *MetadataDB) DB() *sql.DB {
	return m.db
}

// initSchema creates the metadata tables if they do not already exist.
func (m *MetadataDB) initSchema() error {
	const ddl = `
	CREATE TABLE IF NOT EXISTS dux_relationships (
		id          INTEGER PRIMARY KEY,
		from_table  TEXT NOT NULL,
		from_column TEXT NOT NULL,
		to_table    TEXT NOT NULL,
		to_column   TEXT NOT NULL,
		bidirectional BOOLEAN NOT NULL DEFAULT FALSE,
		UNIQUE (from_table, from_column, to_table, to_column)
	);

	CREATE SEQUENCE IF NOT EXISTS dux_relationships_id_seq START 1;

	CREATE TABLE IF NOT EXISTS dux_measures (
		id         INTEGER PRIMARY KEY,
		table_name TEXT NOT NULL,
		name       TEXT NOT NULL,
		expression TEXT NOT NULL,
		UNIQUE (table_name, name)
	);

	CREATE SEQUENCE IF NOT EXISTS dux_measures_id_seq START 1;

	CREATE TABLE IF NOT EXISTS dux_date_tables (
		table_name  TEXT PRIMARY KEY,
		column_name TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS dux_hidden (
		table_name  TEXT NOT NULL,
		column_name TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (table_name, column_name)
	);
	`
	if _, err := m.db.Exec(ddl); err != nil {
		return fmt.Errorf("init metadata schema: %w", err)
	}
	// Migration for databases created before measure formats existed.
	if _, err := m.db.Exec(`ALTER TABLE dux_measures ADD COLUMN IF NOT EXISTS format TEXT`); err != nil {
		return fmt.Errorf("migrate dux_measures.format: %w", err)
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
	rows, err := m.db.Query(`SELECT table_name, column_name FROM dux_hidden ORDER BY table_name, column_name`)
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
	rows, err := m.db.Query(`SELECT table_name, column_name FROM dux_date_tables ORDER BY table_name`)
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
		FROM dux_relationships
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
		FROM dux_measures
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
	_, err := m.db.Exec(`
		INSERT INTO dux_relationships (id, from_table, from_column, to_table, to_column, bidirectional)
		VALUES (nextval('dux_relationships_id_seq'), ?, ?, ?, ?, ?)
		ON CONFLICT (from_table, from_column, to_table, to_column) DO UPDATE SET bidirectional = excluded.bidirectional
	`, fromTable, fromColumn, toTable, toColumn, bidirectional)
	if err != nil {
		return fmt.Errorf("save relationship: %w", err)
	}
	return nil
}

// SaveMeasure inserts or replaces a measure in the metadata DB.
// expression should be the raw DUX expression string (e.g. "COUNT(matches[match_num])").
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
	_, err := m.db.Exec(`
		INSERT INTO dux_measures (id, table_name, name, expression, format)
		VALUES (nextval('dux_measures_id_seq'), ?, ?, ?, ?)
		ON CONFLICT (table_name, name) DO UPDATE SET
			expression = excluded.expression,
			format     = excluded.format
	`, tableName, name, expression, formatJSON)
	if err != nil {
		return fmt.Errorf("save measure: %w", err)
	}
	return nil
}

// DeleteMeasure removes a measure from the metadata DB.
func (m *MetadataDB) DeleteMeasure(tableName, name string) error {
	_, err := m.db.Exec(`DELETE FROM dux_measures WHERE table_name = ? AND name = ?`, tableName, name)
	return err
}

// SaveDateTable inserts or replaces a date-table designation in the metadata DB.
func (m *MetadataDB) SaveDateTable(tableName, columnName string) error {
	_, err := m.db.Exec(`
		INSERT INTO dux_date_tables (table_name, column_name) VALUES (?, ?)
		ON CONFLICT (table_name) DO UPDATE SET column_name = excluded.column_name
	`, tableName, columnName)
	if err != nil {
		return fmt.Errorf("save date table: %w", err)
	}
	return nil
}

// DeleteDateTable removes a date-table designation from the metadata DB.
func (m *MetadataDB) DeleteDateTable(tableName string) error {
	_, err := m.db.Exec(`DELETE FROM dux_date_tables WHERE table_name = ?`, tableName)
	return err
}

// SaveHidden marks a table (columnName == "") or a single column as hidden.
func (m *MetadataDB) SaveHidden(tableName, columnName string) error {
	_, err := m.db.Exec(`
		INSERT INTO dux_hidden (table_name, column_name) VALUES (?, ?)
		ON CONFLICT (table_name, column_name) DO NOTHING
	`, tableName, columnName)
	if err != nil {
		return fmt.Errorf("save hidden: %w", err)
	}
	return nil
}

// DeleteHidden removes a hidden designation for a table (columnName == "") or column.
func (m *MetadataDB) DeleteHidden(tableName, columnName string) error {
	_, err := m.db.Exec(`DELETE FROM dux_hidden WHERE table_name = ? AND column_name = ?`, tableName, columnName)
	return err
}

// DeleteRelationship removes a relationship from the metadata DB.
func (m *MetadataDB) DeleteRelationship(fromTable, fromColumn, toTable, toColumn string) error {
	_, err := m.db.Exec(`
		DELETE FROM dux_relationships
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

	if _, err := tx.Exec(`DELETE FROM dux_relationships`); err != nil {
		return fmt.Errorf("clear relationships: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM dux_measures`); err != nil {
		return fmt.Errorf("clear measures: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM dux_date_tables`); err != nil {
		return fmt.Errorf("clear date tables: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM dux_hidden`); err != nil {
		return fmt.Errorf("clear hidden: %w", err)
	}

	for _, r := range schema.Relationships {
		if _, err := tx.Exec(`
			INSERT INTO dux_relationships (id, from_table, from_column, to_table, to_column, bidirectional)
			VALUES (nextval('dux_relationships_id_seq'), ?, ?, ?, ?, ?)
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
				INSERT INTO dux_measures (id, table_name, name, expression, format)
				VALUES (nextval('dux_measures_id_seq'), ?, ?, ?, ?)
			`, tableName, measureName, def.Expression, formatJSON); err != nil {
				return fmt.Errorf("insert measure %q: %w", measureName, err)
			}
		}
	}

	for tableName, columnName := range schema.DateTables {
		if _, err := tx.Exec(`
			INSERT INTO dux_date_tables (table_name, column_name) VALUES (?, ?)
		`, tableName, columnName); err != nil {
			return fmt.Errorf("insert date table %q: %w", tableName, err)
		}
	}

	for tableName := range schema.HiddenTables {
		if _, err := tx.Exec(`
			INSERT INTO dux_hidden (table_name, column_name) VALUES (?, '')
		`, tableName); err != nil {
			return fmt.Errorf("insert hidden table %q: %w", tableName, err)
		}
	}
	for tableName, cols := range schema.HiddenColumns {
		for columnName := range cols {
			if _, err := tx.Exec(`
				INSERT INTO dux_hidden (table_name, column_name) VALUES (?, ?)
			`, tableName, columnName); err != nil {
				return fmt.Errorf("insert hidden column %q.%q: %w", tableName, columnName, err)
			}
		}
	}

	return tx.Commit()
}
