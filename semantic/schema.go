// Package semantic provides schema modeling, reference resolution, and context
// tracking for the DUX query language.
package semantic

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/danielwikar/dux/parser"
)

// Schema holds the full set of tables, columns, measures, and relationships
// known to the system. It is populated at startup by introspecting DuckDB
// and optionally merging metadata from dux.toml or the metadata database.
type Schema struct {
	Tables        map[string]*Table
	Measures      map[string]map[string]*parser.MeasureDefinition // table → name → def
	Relationships []*Relationship
	// DateTables maps a lower-cased table key to the name of its date column.
	// A designated date table gets DAX "mark as date table" semantics: time
	// intelligence functions over any of its columns clear ALL filters on the
	// table (not just the date column) before applying their date range.
	DateTables map[string]string
	// HiddenTables maps a lower-cased table key to true when the whole table
	// (or view) is marked hidden.
	HiddenTables map[string]bool
	// HiddenColumns maps a lower-cased table key to a set of lower-cased
	// column names marked hidden.
	HiddenColumns map[string]map[string]bool
}

// Table represents a table or view in the schema.
type Table struct {
	Name     string
	Database string // empty for the primary (main) database; attachment alias otherwise
	Schema   string // empty for the default ("main") schema; DuckDB schema name otherwise
	IsView   bool   // true when introspected as a VIEW rather than a BASE TABLE
	Hidden   bool   // true when the table is marked hidden
	Columns  map[string]*Column
}

// Column represents a single column within a Table.
type Column struct {
	Name     string
	DataType string // "TEXT", "BIGINT", "DOUBLE", "DATE", etc.
	Hidden   bool   // true when the column is marked hidden
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
		Tables:        make(map[string]*Table),
		Measures:      make(map[string]map[string]*parser.MeasureDefinition),
		DateTables:    make(map[string]string),
		HiddenTables:  make(map[string]bool),
		HiddenColumns: make(map[string]map[string]bool),
	}
}

// AddMeasureFromExpr parses a measure expression through the DUX parser and
// stores the resulting definition under the parsed table and measure name.
func (s *Schema) AddMeasureFromExpr(table, name, expr string) error {
	defines, err := parser.ParseMeasures(
		fmt.Sprintf("DEFINE\n    MEASURE %s[%s] = %s", table, name, expr),
	)
	if err != nil {
		return err
	}
	if len(defines) == 0 {
		return fmt.Errorf("no measure parsed")
	}
	for _, def := range defines {
		def.Expression = expr
		t := StripSingleQuotes(def.Table)
		n := StripBrackets(def.Column)
		if s.Measures[t] == nil {
			s.Measures[t] = make(map[string]*parser.MeasureDefinition)
		}
		s.Measures[t][n] = def
	}
	return nil
}

// findTable returns the *Table for name using an exact key match first and a
// case-insensitive scan as fallback, along with the canonical schema key.
func (s *Schema) findTable(name string) (*Table, string) {
	if t, ok := s.Tables[name]; ok {
		return t, name
	}
	lower := strings.ToLower(name)
	for k, t := range s.Tables {
		if strings.ToLower(k) == lower {
			return t, k
		}
	}
	return nil, ""
}

// SetTableHidden marks table (any casing) as hidden or visible, updating both
// the persistent HiddenTables map and the live Table flag when present.
func (s *Schema) SetTableHidden(table string, hidden bool) {
	if s.HiddenTables == nil {
		s.HiddenTables = make(map[string]bool)
	}
	key := strings.ToLower(table)
	if hidden {
		s.HiddenTables[key] = true
	} else {
		delete(s.HiddenTables, key)
	}
	if t, _ := s.findTable(table); t != nil {
		t.Hidden = hidden
	}
}

// SetColumnHidden marks a column of table (any casing) as hidden or visible,
// updating both the persistent HiddenColumns map and the live Column flag.
func (s *Schema) SetColumnHidden(table, column string, hidden bool) {
	if s.HiddenColumns == nil {
		s.HiddenColumns = make(map[string]map[string]bool)
	}
	tKey := strings.ToLower(table)
	cKey := strings.ToLower(column)
	if hidden {
		if s.HiddenColumns[tKey] == nil {
			s.HiddenColumns[tKey] = make(map[string]bool)
		}
		s.HiddenColumns[tKey][cKey] = true
	} else if cols := s.HiddenColumns[tKey]; cols != nil {
		delete(cols, cKey)
		if len(cols) == 0 {
			delete(s.HiddenColumns, tKey)
		}
	}
	t, _ := s.findTable(table)
	if t == nil {
		return
	}
	for _, c := range t.Columns {
		if strings.EqualFold(c.Name, column) {
			c.Hidden = hidden
		}
	}
}

// ClearHidden removes all hidden designations from the maps and the live
// Table/Column flags.
func (s *Schema) ClearHidden() {
	s.HiddenTables = make(map[string]bool)
	s.HiddenColumns = make(map[string]map[string]bool)
	for _, t := range s.Tables {
		t.Hidden = false
		for _, c := range t.Columns {
			c.Hidden = false
		}
	}
}

// ApplyHiddenFlags stamps the Hidden flag onto Tables and Columns from the
// HiddenTables / HiddenColumns maps. Call after (re-)introspection replaces
// the Table structs.
func (s *Schema) ApplyHiddenFlags() {
	for key, t := range s.Tables {
		lk := strings.ToLower(key)
		t.Hidden = s.HiddenTables[lk]
		hiddenCols := s.HiddenColumns[lk]
		for _, c := range t.Columns {
			c.Hidden = hiddenCols != nil && hiddenCols[strings.ToLower(c.Name)]
		}
	}
}

// SetDateTable designates table (any casing) as a date table with the given
// date column. An empty column removes the designation.
func (s *Schema) SetDateTable(table, column string) {
	if s.DateTables == nil {
		s.DateTables = make(map[string]string)
	}
	key := strings.ToLower(table)
	if column == "" {
		delete(s.DateTables, key)
		return
	}
	s.DateTables[key] = column
}

// DateColumn returns the designated date column for table (any casing) and
// whether the table is a designated date table.
func (s *Schema) DateColumn(table string) (string, bool) {
	if s.DateTables == nil {
		return "", false
	}
	col, ok := s.DateTables[strings.ToLower(table)]
	return col, ok
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
		SELECT c.table_catalog, c.table_schema, c.table_name, c.column_name, c.data_type,
		       COALESCE(t.table_type, 'BASE TABLE')
		FROM information_schema.columns c
		LEFT JOIN information_schema.tables t
			ON  t.table_catalog = c.table_catalog
			AND t.table_schema  = c.table_schema
			AND t.table_name    = c.table_name
		WHERE c.table_schema NOT IN ('information_schema', 'pg_catalog')
		ORDER BY c.table_catalog, c.table_schema, c.table_name, c.ordinal_position
	`)
	if err != nil {
		return nil, fmt.Errorf("introspect columns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var catalog, schemaName, tableName, columnName, dataType, tableType string
		if err := rows.Scan(&catalog, &schemaName, &tableName, &columnName, &dataType, &tableType); err != nil {
			return nil, fmt.Errorf("scan column row: %w", err)
		}
		// For attached databases the catalog differs from the primary db name.
		// We key the table as "db.table" so that qualified DUX references resolve
		// unambiguously. The primary database uses a bare table name as key.
		// Tables outside the default "main" schema additionally carry the schema
		// segment: "db.schema.table" (or "schema.table" for the primary db).
		dbAlias := ""
		if catalog != "memory" && catalog != "" {
			// catalog holds the attachment alias (e.g. "atp") for attached databases,
			// and the filename stem for the primary. We distinguish them by checking
			// whether the catalog matches the reserved primary marker stored in the
			// schema — but since we don't know the primary name here, we key ALL
			// non-memory catalogs with a qualified key and let the resolver strip the
			// prefix for plain (unqualified) references.
			dbAlias = catalog
		}
		schemaPart := ""
		if schemaName != "main" && schemaName != "" {
			schemaPart = schemaName
		}
		var parts []string
		if dbAlias != "" {
			parts = append(parts, dbAlias)
		}
		if schemaPart != "" {
			parts = append(parts, schemaPart)
		}
		parts = append(parts, tableName)
		key := strings.Join(parts, ".")

		t, ok := schema.Tables[key]
		if !ok {
			t = &Table{
				Name:     tableName,
				Database: dbAlias,
				Schema:   schemaPart,
				IsView:   tableType == "VIEW",
				Columns:  make(map[string]*Column),
			}
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
