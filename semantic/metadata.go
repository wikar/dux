package semantic

import (
	"database/sql"
	"fmt"

	"github.com/danielwikar/dux/parser"
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
	return m.loadMeasures(schema)
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
		SELECT table_name, name, expression
		FROM dux_measures
		ORDER BY table_name, name
	`)
	if err != nil {
		return fmt.Errorf("load measures: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tableName, name, expression string
		if err := rows.Scan(&tableName, &name, &expression); err != nil {
			return fmt.Errorf("scan measure: %w", err)
		}
		defines, err := parser.ParseMeasures(
			fmt.Sprintf("DEFINE\n    MEASURE %s[%s] = %s", tableName, name, expression),
		)
		if err != nil {
			return fmt.Errorf("parse stored measure %q.%q: %w", tableName, name, err)
		}
		for _, def := range defines {
			def.Expression = expression
			table := StripSingleQuotes(def.Table)
			n := StripBrackets(def.Column)
			if schema.Measures[table] == nil {
				schema.Measures[table] = make(map[string]*parser.MeasureDefinition)
			}
			schema.Measures[table][n] = def
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
func (m *MetadataDB) SaveMeasure(tableName, name, expression string) error {
	_, err := m.db.Exec(`
		INSERT INTO dux_measures (id, table_name, name, expression)
		VALUES (nextval('dux_measures_id_seq'), ?, ?, ?)
		ON CONFLICT (table_name, name) DO UPDATE SET expression = excluded.expression
	`, tableName, name, expression)
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
			if _, err := tx.Exec(`
				INSERT INTO dux_measures (id, table_name, name, expression)
				VALUES (nextval('dux_measures_id_seq'), ?, ?, ?)
			`, tableName, measureName, def.Expression); err != nil {
				return fmt.Errorf("insert measure %q: %w", measureName, err)
			}
		}
	}

	return tx.Commit()
}
