// Package semantic provides schema modeling, reference resolution, and context
// tracking for the DUX query language.
package semantic

import (
	"database/sql"
	"fmt"

	"github.com/danielwikar/dux/parser"
)

// Schema holds the full set of tables, columns, measures, and relationships
// known to the system. It is populated at startup by introspecting DuckDB
// and optionally merging metadata from dux.toml or the metadata database.
type Schema struct {
	Tables        map[string]*Table
	Measures      map[string]map[string]*parser.MeasureDefinition // table → name → def
	Relationships []*Relationship
}

// Table represents a table in the schema.
type Table struct {
	Name     string
	Database string // empty for the primary (main) database; attachment alias otherwise
	Columns  map[string]*Column
}

// Column represents a single column within a Table.
type Column struct {
	Name     string
	DataType string // "TEXT", "BIGINT", "DOUBLE", "DATE", etc.
}

// Relationship models a foreign-key edge from the fact side (Many) to the
// dimension side (One).
type Relationship struct {
	FromTable     string
	FromColumn    string
	ToTable       string
	ToColumn      string
	Bidirectional bool
}

// NewSchema returns an empty, initialised Schema.
func NewSchema() *Schema {
	return &Schema{
		Tables:   make(map[string]*Table),
		Measures: make(map[string]map[string]*parser.MeasureDefinition),
	}
}

// IntrospectDuckDB populates a Schema by querying the information_schema of
// the provided DuckDB connection. Columns are read from information_schema.columns;
// foreign-key relationships are read from information_schema.referential_constraints
// joined with key_column_usage where those are declared.
//
// When DuckDB is used over flat Parquet sources without declared foreign keys,
// relationships must be supplied via dux.toml or the metadata database.
func IntrospectDuckDB(db *sql.DB) (*Schema, error) {
	schema := NewSchema()

	rows, err := db.Query(`
		SELECT table_catalog, table_name, column_name, data_type
		FROM information_schema.columns
		WHERE table_schema = 'main'
		ORDER BY table_catalog, table_name, ordinal_position
	`)
	if err != nil {
		return nil, fmt.Errorf("introspect columns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var catalog, tableName, columnName, dataType string
		if err := rows.Scan(&catalog, &tableName, &columnName, &dataType); err != nil {
			return nil, fmt.Errorf("scan column row: %w", err)
		}
		// For attached databases the catalog differs from the primary db name.
		// We key the table as "db.table" so that qualified DUX references resolve
		// unambiguously. The primary database uses a bare table name as key.
		key := tableName
		dbAlias := ""
		if catalog != "memory" && catalog != "" {
			// catalog holds the attachment alias (e.g. "atp") for attached databases,
			// and the filename stem for the primary. We distinguish them by checking
			// whether the catalog matches the reserved primary marker stored in the
			// schema — but since we don't know the primary name here, we key ALL
			// non-memory catalogs with a qualified key and let the resolver strip the
			// prefix for plain (unqualified) references.
			key = catalog + "." + tableName
			dbAlias = catalog
		}
		t, ok := schema.Tables[key]
		if !ok {
			t = &Table{Name: tableName, Database: dbAlias, Columns: make(map[string]*Column)}
			schema.Tables[key] = t
		}
		t.Columns[columnName] = &Column{Name: columnName, DataType: dataType}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := introspectRelationships(db, schema); err != nil {
		return nil, err
	}

	return schema, nil
}

// introspectRelationships populates schema.Relationships from DuckDB's
// information_schema. This succeeds only when foreign keys are declared via
// CREATE TABLE … FOREIGN KEY; for Parquet/CSV sources without FK metadata the
// query returns zero rows without error.
func introspectRelationships(db *sql.DB, schema *Schema) error {
	const q = `
		SELECT
			kcu.table_name   AS from_table,
			kcu.column_name  AS from_column,
			ccu.table_name   AS to_table,
			ccu.column_name  AS to_column
		FROM information_schema.referential_constraints rc
		JOIN information_schema.key_column_usage kcu
			ON  kcu.constraint_name   = rc.constraint_name
			AND kcu.constraint_schema = rc.constraint_schema
		JOIN information_schema.constraint_column_usage ccu
			ON  ccu.constraint_name   = rc.unique_constraint_name
			AND ccu.constraint_schema = rc.unique_constraint_schema
		WHERE kcu.table_schema = 'main'
		ORDER BY kcu.table_name, kcu.ordinal_position
	`
	rows, err := db.Query(q)
	if err != nil {
		// FK metadata is unavailable (Parquet/CSV sources, older DuckDB builds).
		// Relationships can be supplied via dux.toml instead.
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var fromTable, fromColumn, toTable, toColumn string
		if err := rows.Scan(&fromTable, &fromColumn, &toTable, &toColumn); err != nil {
			return fmt.Errorf("scan relationship row: %w", err)
		}
		schema.Relationships = append(schema.Relationships, &Relationship{
			FromTable:  fromTable,
			FromColumn: fromColumn,
			ToTable:    toTable,
			ToColumn:   toColumn,
		})
	}
	return rows.Err()
}
