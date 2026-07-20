package semantic

import (
	"bytes"
	"fmt"
	"os"
	"sort"

	"github.com/BurntSushi/toml"
	"github.com/danielwikar/dux/parser"
)

// ─── TOML wire types ──────────────────────────────────────────────────────────

// duxTOML is the top-level shape of a dux.toml file.
type duxTOML struct {
	Relationship []tomlRelationship `toml:"relationship"`
	Measure      []tomlMeasure      `toml:"measure"`
	DateTable    []tomlDateTable    `toml:"date_table"`
	Hidden       []tomlHidden       `toml:"hidden"`
}

// tomlRelationship is one [[relationship]] entry.
type tomlRelationship struct {
	FromTable     string `toml:"from_table"`
	FromColumn    string `toml:"from_column"`
	ToTable       string `toml:"to_table"`
	ToColumn      string `toml:"to_column"`
	Bidirectional bool   `toml:"bidirectional,omitempty"`
}

// tomlMeasure is one [[measure]] entry. Format is optional and serialises as
// a nested [measure.format] table.
type tomlMeasure struct {
	Table      string         `toml:"table"`
	Name       string         `toml:"name"`
	Expression string         `toml:"expression"`
	Format     *MeasureFormat `toml:"format,omitempty"`
}

// tomlDateTable is one [[date_table]] entry — designates a table as the model's
// date table (DAX "mark as date table") with its date column.
type tomlDateTable struct {
	Table  string `toml:"table"`
	Column string `toml:"column"`
}

// tomlHidden is one [[hidden]] entry — marks a table (or view) as hidden when
// column is omitted, or a single column when it is present.
type tomlHidden struct {
	Table  string `toml:"table"`
	Column string `toml:"column,omitempty"`
}

// ─── Load ─────────────────────────────────────────────────────────────────────

// LoadDuxTOML reads path as a dux.toml file and merges its relationships and
// measures into schema. If path does not exist, nil is returned without error.
func LoadDuxTOML(path string, schema *Schema) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read dux.toml %q: %w", path, err)
	}
	if err := LoadDuxTOMLBytes(data, schema); err != nil {
		return fmt.Errorf("dux.toml %q: %w", path, err)
	}
	return nil
}

// LoadDuxTOMLBytes is like LoadDuxTOML but reads from an in-memory byte slice.
func LoadDuxTOMLBytes(data []byte, schema *Schema) error {
	var doc duxTOML
	if err := toml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse dux.toml: %w", err)
	}

	// Merge relationships.
	for _, r := range doc.Relationship {
		schema.Relationships = append(schema.Relationships, &Relationship{
			FromTable:     r.FromTable,
			FromColumn:    r.FromColumn,
			ToTable:       r.ToTable,
			ToColumn:      r.ToColumn,
			Bidirectional: r.Bidirectional,
		})
	}

	// Merge measures.
	for _, m := range doc.Measure {
		if m.Table == "" || m.Name == "" || m.Expression == "" {
			return fmt.Errorf("measure entry missing table, name, or expression")
		}
		// Measure names must be unique across tables so that bare
		// [MeasureName] references stay unambiguous.
		table := StripSingleQuotes(m.Table)
		if existingTable, conflicts := MeasureNameConflict(schema.Measures, table, m.Name); conflicts {
			return fmt.Errorf("measure %q already defined in table %q", m.Name, existingTable)
		}
		if err := schema.AddMeasureFromExpr(m.Table, m.Name, m.Expression); err != nil {
			return fmt.Errorf("measure %q: %w", m.Name, err)
		}
		if m.Format != nil {
			if err := m.Format.Validate(); err != nil {
				return fmt.Errorf("measure %q: %w", m.Name, err)
			}
			schema.SetMeasureFormat(m.Table, m.Name, m.Format)
		}
	}

	// Merge date-table designations.
	for _, dt := range doc.DateTable {
		if dt.Table == "" || dt.Column == "" {
			return fmt.Errorf("date_table entry missing table or column")
		}
		schema.SetDateTable(dt.Table, dt.Column)
	}

	// Merge hidden designations.
	for _, h := range doc.Hidden {
		if h.Table == "" {
			return fmt.Errorf("hidden entry missing table")
		}
		if h.Column == "" {
			schema.SetTableHidden(h.Table, true)
		} else {
			schema.SetColumnHidden(h.Table, h.Column, true)
		}
	}

	return nil
}

// ─── Export ───────────────────────────────────────────────────────────────────

// ExportDuxTOML serialises the relationships and measures from schema into a
// dux.toml byte slice. The output is deterministically sorted.
func ExportDuxTOML(schema *Schema) ([]byte, error) {
	var doc duxTOML

	// Relationships — sort for deterministic output.
	for _, r := range schema.Relationships {
		doc.Relationship = append(doc.Relationship, tomlRelationship{
			FromTable:     r.FromTable,
			FromColumn:    r.FromColumn,
			ToTable:       r.ToTable,
			ToColumn:      r.ToColumn,
			Bidirectional: r.Bidirectional,
		})
	}
	sort.Slice(doc.Relationship, func(i, j int) bool {
		a, b := doc.Relationship[i], doc.Relationship[j]
		if a.FromTable != b.FromTable {
			return a.FromTable < b.FromTable
		}
		return a.FromColumn < b.FromColumn
	})

	// Measures — sorted by table then name.
	type measureEntry struct {
		table string
		name  string
		def   *parser.MeasureDefinition
	}
	var entries []measureEntry
	for table, defs := range schema.Measures {
		for name, def := range defs {
			entries = append(entries, measureEntry{table: table, name: name, def: def})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].table != entries[j].table {
			return entries[i].table < entries[j].table
		}
		return entries[i].name < entries[j].name
	})

	for _, entry := range entries {
		doc.Measure = append(doc.Measure, tomlMeasure{
			Table:      entry.table,
			Name:       entry.name,
			Expression: entry.def.Expression,
			Format:     schema.MeasureFormatFor(entry.table, entry.name),
		})
	}

	// Date tables — sorted by table name.
	for table, column := range schema.DateTables {
		doc.DateTable = append(doc.DateTable, tomlDateTable{Table: table, Column: column})
	}
	sort.Slice(doc.DateTable, func(i, j int) bool {
		return doc.DateTable[i].Table < doc.DateTable[j].Table
	})

	// Hidden designations — tables first, then columns, sorted for determinism.
	for table := range schema.HiddenTables {
		doc.Hidden = append(doc.Hidden, tomlHidden{Table: table})
	}
	for table, cols := range schema.HiddenColumns {
		for column := range cols {
			doc.Hidden = append(doc.Hidden, tomlHidden{Table: table, Column: column})
		}
	}
	sort.Slice(doc.Hidden, func(i, j int) bool {
		a, b := doc.Hidden[i], doc.Hidden[j]
		if a.Table != b.Table {
			return a.Table < b.Table
		}
		return a.Column < b.Column
	})

	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.Indent = "  "
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encode dux.toml: %w", err)
	}
	return buf.Bytes(), nil
}

// WriteDuxTOML exports schema to a dux.toml file at path.
func WriteDuxTOML(path string, schema *Schema) error {
	data, err := ExportDuxTOML(schema)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
