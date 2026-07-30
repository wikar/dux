// Package semantic — join inference.
//
// This file implements automatic join-path discovery over the declared
// schema.Relationship graph. The emitter calls InferJoinPath to obtain the
// ordered set of LEFT JOINs needed to connect all tables referenced in a
// SUMMARIZECOLUMNS expression.
package semantic

import (
	"errors"
	"fmt"
	"sort"
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
	// OnFromCol is the column on the driving (FromTable) side of the join predicate.
	OnFromCol string
	// OnToCol is the column on the joined (Table) side.
	OnToCol string
	// Bidirectional indicates that this edge was declared bidirectional in the
	// schema.
	Bidirectional bool
}

// AmbiguousPathError identifies a relationship path that cannot be chosen
// deterministically. It unwraps to SemanticError for normal query positioning.
type AmbiguousPathError struct{ err *SemanticError }

func (e *AmbiguousPathError) Error() string { return e.err.Error() }
func (e *AmbiguousPathError) Unwrap() error { return e.err }

func IsAmbiguousPath(err error) bool {
	var target *AmbiguousPathError
	return errors.As(err, &target)
}

// LeafEdge returns the single step attaching table to the rest of this join
// path. A table that is primary, absent, reached more than once, or itself the
// source of another step cannot be detached without changing the path.
func (p *JoinPath) LeafEdge(schema *Schema, table string) (JoinStep, bool) {
	table = ResolveTable(schema, table)
	var edge JoinStep
	found := false
	for _, step := range p.Steps {
		if tableNamesMatch(step.FromTable, table) {
			return JoinStep{}, false
		}
		if tableNamesMatch(step.Table, table) {
			if found {
				return JoinStep{}, false
			}
			edge, found = step, true
		}
	}
	return edge, found
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
	primary, err := resolveTableStrict(schema, tables[0])
	if err != nil {
		return nil, err
	}
	targets := make([]string, 0, len(tables)-1)
	for _, table := range tables[1:] {
		resolved, err := resolveTableStrict(schema, table)
		if err != nil {
			return nil, err
		}
		targets = append(targets, resolved)
	}
	targets = uniqueSlice(targets)

	// joined tracks every table already present in the FROM clause so that
	// intermediate tables introduced while finding a path to one target are
	// not added again when they themselves are also direct targets.
	joined := map[string]bool{strings.ToLower(primary): true}
	joinedTables := []string{primary}

	var steps []JoinStep
	for _, target := range targets {
		if joined[strings.ToLower(target)] {
			// Already introduced as an intermediate step — nothing to emit.
			continue
		}
		path, err := bfsJoin(schema, joinedTables, target)
		if err != nil {
			return nil, err
		}
		for _, step := range path {
			if !joined[strings.ToLower(step.Table)] {
				joined[strings.ToLower(step.Table)] = true
				joinedTables = append(joinedTables, step.Table)
				steps = append(steps, step)
			}
		}
	}
	return &JoinPath{Steps: steps}, nil
}

// bfsJoin finds the shortest relationship path from the existing join tree to
// 'to' via multi-source BFS.
// Both FK directions (FromTable→ToTable and ToTable→FromTable) are traversed
// so that fact→dimension and dimension→fact traversal both work.
// Table name comparisons tolerate case differences and the presence or absence
// of a database qualifier (e.g. "analytics.Sales" matches "Sales" stored in the
// relationship).
func bfsJoin(schema *Schema, from []string, to string) ([]JoinStep, error) {
	for _, table := range from {
		if tableNamesMatch(table, to) {
			return nil, nil
		}
	}

	type state struct {
		table string // canonical (possibly qualified) form
		steps []JoinStep
	}

	bestDepth := make(map[string]int, len(from))
	queue := make([]state, len(from))
	for i, table := range from {
		bestDepth[strings.ToLower(table)] = 0
		queue[i] = state{table: table}
	}
	var found []JoinStep

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		if found != nil && len(curr.steps) >= len(found) {
			continue
		}

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

			// Resolve to the canonical schema key (e.g. bare "Sales" → "analytics.Sales").
			nextTable, err := resolveTableStrict(schema, nextRaw)
			if err != nil {
				return nil, err
			}
			nextLower := strings.ToLower(nextTable)
			step := JoinStep{
				FromTable:     curr.table,
				Table:         nextTable,
				OnFromCol:     fromCol,
				OnToCol:       toCol,
				Bidirectional: rel.Bidirectional,
			}
			// Copy the slice to avoid aliasing across BFS branches.
			newSteps := make([]JoinStep, len(curr.steps)+1)
			copy(newSteps, curr.steps)
			newSteps[len(curr.steps)] = step

			if tableNamesMatch(nextTable, to) {
				if found != nil && len(found) == len(newSteps) {
					return nil, &AmbiguousPathError{err: &SemanticError{Message: fmt.Sprintf("ambiguous relationship path between tables %q and %q; qualify the model with a single path", from[0], to)}}
				}
				found = newSteps
				continue
			}
			depth := len(newSteps)
			if previous, ok := bestDepth[nextLower]; ok && previous < depth {
				continue
			}
			bestDepth[nextLower] = depth
			queue = append(queue, state{table: nextTable, steps: newSteps})
		}
	}
	if found != nil {
		return found, nil
	}

	return nil, &SemanticError{
		Message: fmt.Sprintf("no relationship path between tables %q and %q; "+
			"declare the relationship in dux.toml or via the UI",
			from[0], to),
	}
}

// tableNamesMatch reports whether two table name strings refer to the same
// table, tolerating both case differences and the presence or absence of a
// database qualifier. This allows relationships stored with bare names (e.g.
// "Sales") to be traversed against schema tables keyed with a qualifier (e.g.
// "analytics.Sales").
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

// FilterReaches reports whether a filter applied to table src propagates to
// any of the target tables under DAX propagation rules: filters flow from the
// one side of a relationship (ToTable) to the many side (FromTable), and in
// both directions across bidirectional relationships. Reachability through an
// unrelated fact does NOT propagate (a filter on DimA never reaches FactB via
// DimA ← FactA → ... chains, because FactA → its dimensions is against the
// filter direction).
func FilterReaches(schema *Schema, src string, targets []string) bool {
	targetSet := map[string]bool{}
	for _, t := range targets {
		targetSet[strings.ToLower(ResolveTable(schema, t))] = true
	}
	src = ResolveTable(schema, src)
	if targetSet[strings.ToLower(src)] {
		return true
	}

	visited := map[string]bool{strings.ToLower(src): true}
	queue := []string{src}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		for _, rel := range schema.Relationships {
			var next string
			switch {
			case tableNamesMatch(rel.ToTable, curr):
				next = rel.FromTable // one side → many side: always propagates
			case rel.Bidirectional && tableNamesMatch(rel.FromTable, curr):
				next = rel.ToTable // many side → one side: only when bidirectional
			default:
				continue
			}
			next = ResolveTable(schema, next)
			nl := strings.ToLower(next)
			if visited[nl] {
				continue
			}
			visited[nl] = true
			if targetSet[nl] {
				return true
			}
			queue = append(queue, next)
		}
	}
	return false
}

// GroupingReaches reports whether a group filter reaches any target using only
// declared one-to-many propagation (dimension/ToTable → fact/FromTable).
// Bidirectional reverse traversal is intentionally excluded: the emitter's
// grouped LEFT JOIN shape cannot represent that filter without possible
// many-side fan-out.
func GroupingReaches(schema *Schema, src string, targets []string) bool {
	targetSet := map[string]bool{}
	for _, target := range targets {
		targetSet[strings.ToLower(ResolveTable(schema, target))] = true
	}
	src = ResolveTable(schema, src)
	if targetSet[strings.ToLower(src)] {
		return true
	}

	visited := map[string]bool{strings.ToLower(src): true}
	queue := []string{src}
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		for _, rel := range schema.Relationships {
			if !tableNamesMatch(rel.ToTable, curr) {
				continue
			}
			next := ResolveTable(schema, rel.FromTable)
			nl := strings.ToLower(next)
			if visited[nl] {
				continue
			}
			if targetSet[nl] {
				return true
			}
			visited[nl] = true
			queue = append(queue, next)
		}
	}
	return false
}

// ResolveTable returns the canonical schema key for name: exact
// case-insensitive match first, then bare-name suffix match for unqualified
// names. Returns name unchanged if no schema entry is found. Used across the
// package and by the emitter's measure clustering.
func ResolveTable(schema *Schema, name string) string {
	matches := resolveTableMatches(schema, name)
	if len(matches) == 1 {
		return matches[0]
	}
	return name
}

func resolveTableStrict(schema *Schema, name string) (string, error) {
	matches := resolveTableMatches(schema, name)
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		sort.Strings(matches)
		return "", &SemanticError{Message: fmt.Sprintf(
			"ambiguous table %q; qualify it as one of: %s", name, strings.Join(matches, ", "))}
	}
	return name, nil
}

func resolveTableMatches(schema *Schema, name string) []string {
	nl := strings.ToLower(name)
	for key := range schema.Tables {
		if strings.ToLower(key) == nl {
			return []string{key}
		}
	}
	var matches []string
	if !strings.Contains(name, ".") {
		for key := range schema.Tables {
			if i := strings.LastIndex(key, "."); i >= 0 {
				if strings.ToLower(key[i+1:]) == nl {
					matches = append(matches, key)
				}
			}
		}
	}
	return matches
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

// ValidateFilterPaths rejects a bidirectional edge when another relationship
// path already connects its endpoints, making filter propagation ambiguous.
func ValidateFilterPaths(schema *Schema) error {
	for _, r := range schema.Relationships {
		if !r.Bidirectional {
			continue
		}
		src, dst := strings.ToLower(r.FromTable), strings.ToLower(r.ToTable)
		seen := map[string]bool{src: true}
		queue := []string{src}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, other := range schema.Relationships {
				if other == r {
					continue
				}
				from, to := strings.ToLower(other.FromTable), strings.ToLower(other.ToTable)
				next := ""
				if from == current {
					next = to
				} else if to == current {
					next = from
				}
				if next != "" && !seen[next] {
					seen[next] = true
					queue = append(queue, next)
				}
			}
		}
		if seen[dst] {
			return &SemanticError{
				Message: fmt.Sprintf("ambiguous relationship graph: %q and %q are connected by more than one path; remove one of the conflicting relationships", r.FromTable, r.ToTable),
			}
		}
	}
	return nil
}
