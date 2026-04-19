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
		if joined[strings.ToLower(target)] {
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
func bfsJoin(schema *Schema, from, to string) ([]JoinStep, error) {
	if from == to {
		return nil, nil
	}

	type state struct {
		table string
		steps []JoinStep
	}

	visited := map[string]bool{from: true}
	queue := []state{{table: from}}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		for _, rel := range schema.Relationships {
			var nextTable, fromCol, toCol string

			switch {
			case rel.FromTable == curr.table:
				nextTable = rel.ToTable
				fromCol = rel.FromColumn
				toCol = rel.ToColumn
			case rel.ToTable == curr.table:
				nextTable = rel.FromTable
				fromCol = rel.ToColumn
				toCol = rel.FromColumn
			default:
				continue
			}

			if visited[nextTable] {
				continue
			}
			visited[nextTable] = true

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

			if nextTable == to {
				return newSteps, nil
			}
			queue = append(queue, state{table: nextTable, steps: newSteps})
		}
	}

	return nil, &SemanticError{
		Message: fmt.Sprintf("no relationship path between tables %q and %q; "+
			"declare the relationship in schema.dux.json or as a DuckDB foreign key",
			from, to),
	}
}

// uniqueSlice returns ss with duplicates removed, preserving first-occurrence order.
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
