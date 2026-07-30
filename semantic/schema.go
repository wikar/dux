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
	Tables   map[string]*Table
	Measures map[string]map[string]*parser.MeasureDefinition // table → name → def
	// MeasureFormats holds optional display formats, keyed identically to
	// Measures (table → measure name). Parallel map because the measure
	// definition itself is a parser type that cannot reference semantic types.
	MeasureFormats map[string]map[string]*MeasureFormat
	Relationships  []*Relationship
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

// Table represents a table or view in the schema. SQLName is the fully
// qualified physical DuckDB name when it differs from the public DUX key.
type Table struct {
	Name    string
	SQLName string `json:"-"`
	IsView  bool   // true when introspected as a VIEW rather than a BASE TABLE
	Hidden  bool   // true when the table is marked hidden
	Columns map[string]*Column
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

// FindTable returns the *Table for name using an exact key match first and a
// case-insensitive scan as fallback, along with the canonical schema key.
// Returns (nil, "") when the table is unknown.
func (s *Schema) FindTable(name string) (*Table, string) {
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

// FindColumn resolves a table and column case-insensitively and returns their
// canonical schema names.
func (s *Schema) FindColumn(table, column string) (*Column, string, string) {
	t, tableKey := s.FindTable(table)
	if t == nil {
		return nil, "", ""
	}
	if c, ok := t.Columns[column]; ok {
		return c, tableKey, column
	}
	for key, c := range t.Columns {
		if strings.EqualFold(key, column) {
			return c, tableKey, key
		}
	}
	return nil, tableKey, ""
}

// SetTableHidden marks table (any casing) as hidden or visible, updating both
// the persistent HiddenTables map and the live Table flag when present.
func (s *Schema) SetTableHidden(table string, hidden bool) {
	key := strings.ToLower(table)
	if hidden {
		s.HiddenTables[key] = true
	} else {
		delete(s.HiddenTables, key)
	}
	if t, _ := s.FindTable(table); t != nil {
		t.Hidden = hidden
	}
}

// SetColumnHidden marks a column of table (any casing) as hidden or visible,
// updating both the persistent HiddenColumns map and the live Column flag.
func (s *Schema) SetColumnHidden(table, column string, hidden bool) {
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
	t, _ := s.FindTable(table)
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
			// catalog holds the attachment alias (e.g. "analytics") for attached databases,
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
		physicalName := qualifiedTableKey(dbAlias, schemaPart, tableName)
		key := physicalName
		if catalog == "ducklake" {
			// Always retain the full internal catalog and schema even though main
			// is intentionally omitted from the public DUX name.
			physicalName = qualifiedTableKeyWithMain(catalog, schemaName, tableName)
			key = qualifiedTableKey("", schemaPart, tableName)
		}

		t, ok := schema.Tables[key]
		if !ok {
			t = &Table{
				Name:    tableName,
				SQLName: physicalName,
				IsView:  tableType == "VIEW",
				Columns: make(map[string]*Column),
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
			kcu.table_catalog AS from_catalog,
			kcu.table_schema  AS from_schema,
			kcu.table_name    AS from_table,
			kcu.column_name   AS from_column,
			ukcu.table_catalog AS to_catalog,
			ukcu.table_schema  AS to_schema,
			ukcu.table_name    AS to_table,
			ukcu.column_name   AS to_column
		FROM information_schema.referential_constraints rc
		JOIN information_schema.key_column_usage kcu
			ON  kcu.constraint_catalog = rc.constraint_catalog
			AND kcu.constraint_schema = rc.constraint_schema
			AND kcu.constraint_name = rc.constraint_name
		JOIN information_schema.key_column_usage ukcu
			ON  ukcu.constraint_catalog = rc.unique_constraint_catalog
			AND ukcu.constraint_schema = rc.unique_constraint_schema
			AND ukcu.constraint_name = rc.unique_constraint_name
			AND ukcu.ordinal_position = kcu.position_in_unique_constraint
		WHERE kcu.table_schema NOT IN ('information_schema', 'pg_catalog')
		ORDER BY kcu.table_catalog, kcu.table_schema, kcu.table_name, kcu.ordinal_position
	`
	rows, err := db.Query(q)
	if err != nil {
		// FK metadata is unavailable (Parquet/CSV sources, older DuckDB builds).
		// Relationships can be supplied via dux.toml instead.
		return nil
	}
	defer rows.Close()

	for rows.Next() {
		var fromCatalog, fromSchema, fromTable, fromColumn string
		var toCatalog, toSchema, toTable, toColumn string
		if err := rows.Scan(&fromCatalog, &fromSchema, &fromTable, &fromColumn,
			&toCatalog, &toSchema, &toTable, &toColumn); err != nil {
			return fmt.Errorf("scan relationship row: %w", err)
		}
		schema.Relationships = append(schema.Relationships, &Relationship{
			FromTable:  qualifiedTableKey(fromCatalog, fromSchema, fromTable),
			FromColumn: fromColumn,
			ToTable:    qualifiedTableKey(toCatalog, toSchema, toTable),
			ToColumn:   toColumn,
		})
	}
	return rows.Err()
}

func qualifiedTableKey(catalog, schemaName, table string) string {
	parts := make([]string, 0, 3)
	if catalog != "" && catalog != "memory" {
		parts = append(parts, catalog)
	}
	if schemaName != "" && schemaName != "main" {
		parts = append(parts, schemaName)
	}
	return strings.Join(append(parts, table), ".")
}

func qualifiedTableKeyWithMain(catalog, schemaName, table string) string {
	parts := make([]string, 0, 3)
	if catalog != "" && catalog != "memory" {
		parts = append(parts, catalog)
	}
	if schemaName != "" {
		parts = append(parts, schemaName)
	}
	return strings.Join(append(parts, table), ".")
}
