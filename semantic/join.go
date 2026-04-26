// Package semantic — join inference.
//
// This file implements automatic join-path discovery over the declared
// schema.Relationship graph. The emitter calls InferJoinPath to obtain the
// ordered set of LEFT JOINs needed to connect all tables referenced in a
// SUMMARIZECOLUMNS expression.
package semantic

import (
	"fmt"
	"strings"
)

// JoinPath describes the ordered sequence of LEFT JOINs needed to connect
// multiple tables in a single SQL FROM clause.
type JoinPath struct {
	Steps []JoinStep
}

// JoinStep is one LEFT JOIN within a JoinPath.
type JoinStep struct {
	// FromTable is the table that OnFromCol belongs to (the driving side).
	FromTable string
	// Table is the joined table name (unquoted, original casing from schema).
	Table string
	// Alias is the unique SQL alias assigned to this table in the FROM clause.
	Alias string
	// OnFromCol is the column on the driving (FromTable) side of the join predicate.
	OnFromCol string
	// OnToCol is the column on the joined (Table) side.
	OnToCol string
}

// InferJoinPath performs a BFS over the Relationship graph to find the minimum
// set of LEFT JOINs that connects all supplied tables. tables[0] is the
// primary (fact) table; all other tables are targets to be joined.
//
// Returns a SemanticError if no path exists between two tables, or if the
// supplied slice is empty.
func InferJoinPath(schema *Schema, tables []string) (*JoinPath, error) {
	if len(tables) <= 1 {
		return &JoinPath{}, nil
	}

	primary := tables[0]
	targets := uniqueSlice(tables[1:])

	// joined tracks every table already present in the FROM clause so that
	// intermediate tables introduced while finding a path to one target are
	// not added again when they themselves are also direct targets.
	joined := map[string]bool{strings.ToLower(primary): true}

	var steps []JoinStep
	for _, target := range targets {
		if joined[strings.ToLower(resolveSchemaTable(schema, target))] {
			// Already introduced as an intermediate step — nothing to emit.
			continue
		}
		path, err := bfsJoin(schema, primary, target)
		if err != nil {
			return nil, err
		}
		for _, step := range path {
			if !joined[strings.ToLower(step.Table)] {
				joined[strings.ToLower(step.Table)] = true
				steps = append(steps, step)
			}
		}
	}
	return &JoinPath{Steps: steps}, nil
}

// bfsJoin finds the shortest relationship path from 'from' to 'to' via BFS.
// Both FK directions (FromTable→ToTable and ToTable→FromTable) are traversed
// so that fact→dimension and dimension→fact traversal both work.
// Table name comparisons tolerate case differences and the presence or absence
// of a database qualifier (e.g. "bev.Sales" matches "Sales" stored in the
// relationship).
func bfsJoin(schema *Schema, from, to string) ([]JoinStep, error) {
	if tableNamesMatch(from, to) {
		return nil, nil
	}

	type state struct {
		table string // canonical (possibly qualified) form
		steps []JoinStep
	}

	visited := map[string]bool{strings.ToLower(from): true}
	queue := []state{{table: from}}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, rel := range schema.Relationships {
			var nextRaw, fromCol, toCol string

			switch {
			case tableNamesMatch(rel.FromTable, curr.table):
				nextRaw = rel.ToTable
				fromCol = rel.FromColumn
				toCol = rel.ToColumn
			case tableNamesMatch(rel.ToTable, curr.table):
				nextRaw = rel.FromTable
				fromCol = rel.ToColumn
				toCol = rel.FromColumn
			default:
				continue
			}

			// Resolve to the canonical schema key (e.g. bare "Sales" → "bev.Sales").
			nextTable := resolveSchemaTable(schema, nextRaw)
			nextLower := strings.ToLower(nextTable)
			if visited[nextLower] {
				continue
			}
			visited[nextLower] = true

			step := JoinStep{
				FromTable: curr.table,
				Table:     nextTable,
				Alias:     nextTable,
				OnFromCol: fromCol,
				OnToCol:   toCol,
			}
			// Copy the slice to avoid aliasing across BFS branches.
			newSteps := make([]JoinStep, len(curr.steps)+1)
			copy(newSteps, curr.steps)
			newSteps[len(curr.steps)] = step

			if tableNamesMatch(nextTable, to) {
				return newSteps, nil
			}
			queue = append(queue, state{table: nextTable, steps: newSteps})
		}
	}

	return nil, &SemanticError{
		Message: fmt.Sprintf("no relationship path between tables %q and %q; "+
			"declare the relationship in dux.toml or via the UI",
			from, to),
	}
}

// tableNamesMatch reports whether two table name strings refer to the same
// table, tolerating both case differences and the presence or absence of a
// database qualifier. This allows relationships stored with bare names (e.g.
// "Sales") to be traversed against schema tables keyed with a qualifier (e.g.
// "bev.Sales").
func tableNamesMatch(a, b string) bool {
	al := strings.ToLower(a)
	bl := strings.ToLower(b)
	if al == bl {
		return true
	}
	// One side may be qualified (db.table), the other bare (table).
	if i := strings.LastIndex(al, "."); i >= 0 {
		if al[i+1:] == bl {
			return true
		}
	}
	if i := strings.LastIndex(bl, "."); i >= 0 {
		if bl[i+1:] == al {
			return true
		}
	}
	return false
}

// resolveSchemaTable returns the canonical schema key for name: exact
// case-insensitive match first, then bare-name suffix match for unqualified
// names. Returns name unchanged if no schema entry is found.
func resolveSchemaTable(schema *Schema, name string) string {
	nl := strings.ToLower(name)
	for key := range schema.Tables {
		if strings.ToLower(key) == nl {
			return key
		}
	}
	if !strings.Contains(name, ".") {
		for key := range schema.Tables {
			if i := strings.LastIndex(key, "."); i >= 0 {
				if strings.ToLower(key[i+1:]) == nl {
					return key
				}
			}
		}
	}
	return name
}

func uniqueSlice(ss []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
