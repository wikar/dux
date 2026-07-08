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
}

// tomlRelationship is one [[relationship]] entry.
type tomlRelationship struct {
	FromTable     string `toml:"from_table"`
	FromColumn    string `toml:"from_column"`
	ToTable       string `toml:"to_table"`
	ToColumn      string `toml:"to_column"`
	Bidirectional bool   `toml:"bidirectional,omitempty"`
}

// tomlMeasure is one [[measure]] entry.
type tomlMeasure struct {
	Table      string `toml:"table"`
	Name       string `toml:"name"`
	Expression string `toml:"expression"`
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
		defines, err := parser.ParseMeasures(
			fmt.Sprintf("DEFINE\n    MEASURE %s[%s] = %s", m.Table, m.Name, m.Expression),
		)
		if err != nil {
			return fmt.Errorf("measure %q: %w", m.Name, err)
		}
		for _, def := range defines {
			def.Expression = m.Expression
			table := StripSingleQuotes(def.Table)
			name := StripBrackets(def.Column)
			// Measure names must be unique across tables so that bare
			// [MeasureName] references stay unambiguous.
			for existingTable, defs := range schema.Measures {
				if existingTable == table {
					continue
				}
				if _, conflicts := defs[name]; conflicts {
					return fmt.Errorf("measure %q already defined in table %q", name, existingTable)
				}
			}
			if schema.Measures[table] == nil {
				schema.Measures[table] = make(map[string]*parser.MeasureDefinition)
			}
			schema.Measures[table][name] = def
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
		})
	}

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
