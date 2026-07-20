// Package semantic — join inference.
//
// This file implements automatic join-path discovery over the declared
// schema.Relationship graph. The emitter calls InferJoinPath to obtain the
// ordered set of LEFT JOINs needed to connect all tables referenced in a
// SUMMARIZECOLUMNS expression.
package semantic

import (
	"fmt"
	"slices"
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
		if joined[strings.ToLower(ResolveTable(schema, target))] {
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
			nextTable := ResolveTable(schema, nextRaw)
			nextLower := strings.ToLower(nextTable)
			if visited[nextLower] {
				continue
			}
			visited[nextLower] = true

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

// ResolveTable returns the canonical schema key for name: exact
// case-insensitive match first, then bare-name suffix match for unqualified
// names. Returns name unchanged if no schema entry is found. Used across the
// package and by the emitter's measure clustering.
func ResolveTable(schema *Schema, name string) string {
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

// ValidateBidiPaths checks that no two tables are reachable from each other
// via more than one path once bidirectional edges are resolved as undirected.
// An ambiguous schema causes non-deterministic CTE codegen and is rejected
// with a SemanticError describing the conflicting paths.
//
// The algorithm builds the undirected reachability graph (all directed edges
// are traversable in both directions; bidi edges just formalise this) and then
// verifies that the edge set does not create a cycle — i.e. that the graph
// remains a forest. Any cycle means two tables have multiple paths between them.
func ValidateBidiPaths(schema *Schema) error {
	// Only run when there is at least one bidi edge — unidirectional-only
	// schemas cannot have the ambiguity problem.
	if !slices.ContainsFunc(schema.Relationships, func(r *Relationship) bool { return r.Bidirectional }) {
		return nil
	}

	// Collect all table names involved in any relationship.
	tableSet := map[string]struct{}{}
	for _, r := range schema.Relationships {
		tableSet[strings.ToLower(r.FromTable)] = struct{}{}
		tableSet[strings.ToLower(r.ToTable)] = struct{}{}
	}

	// For each ordered pair (src, dst) that appears in any relationship, use
	// BFS over the undirected filter graph to count how many distinct shortest
	// paths exist between all pairs of tables. Any pair with >1 path is ambiguous.
	//
	// Practical approach: for each pair of tables that can reach each other,
	// remove one edge at a time from the undirected graph and check whether the
	// two endpoints are still connected. If yes, there is an alternative path →
	// ambiguous. This is the classic "check for bridge" inverse.
	//
	// Build adjacency list (undirected).
	type edge struct {
		to  string
		rel *Relationship
	}
	adj := map[string][]edge{}
	for _, r := range schema.Relationships {
		fl := strings.ToLower(r.FromTable)
		tl := strings.ToLower(r.ToTable)
		adj[fl] = append(adj[fl], edge{to: tl, rel: r})
		adj[tl] = append(adj[tl], edge{to: fl, rel: r})
	}

	// Normalise table names to the lower-case set we built.
	norm := func(t string) string { return strings.ToLower(t) }

	// reachable returns true if dst can be reached from src without using the
	// excluded relationship.
	reachable := func(src, dst string, exclude *Relationship) bool {
		visited := map[string]bool{src: true}
		q := []string{src}
		for len(q) > 0 {
			curr := q[0]
			q = q[1:]
			for _, e := range adj[curr] {
				if e.rel == exclude {
					continue
				}
				if !visited[e.to] {
					if e.to == dst {
						return true
					}
					visited[e.to] = true
					q = append(q, e.to)
				}
			}
		}
		return false
	}

	// pathStr returns a human-readable description of the BFS path from src to
	// dst excluding one relationship, for error messages.
	pathStr := func(src, dst string, exclude *Relationship) string {
		type state struct {
			table string
			path  []string
		}
		visited := map[string]bool{src: true}
		q := []state{{src, []string{src}}}
		for len(q) > 0 {
			curr := q[0]
			q = q[1:]
			for _, e := range adj[curr.table] {
				if e.rel == exclude {
					continue
				}
				if !visited[e.to] {
					newPath := append(append([]string{}, curr.path...), e.to)
					if e.to == dst {
						return strings.Join(newPath, " → ")
					}
					visited[e.to] = true
					q = append(q, state{e.to, newPath})
				}
			}
		}
		return src + " → ... → " + dst
	}

	// For every bidi edge: if both endpoints can still reach each other when
	// that edge is excluded, the graph is ambiguous.
	for _, r := range schema.Relationships {
		if !r.Bidirectional {
			continue
		}
		fl := norm(r.FromTable)
		tl := norm(r.ToTable)
		if reachable(fl, tl, r) {
			// Describe the two conflicting paths.
			directPath := fmt.Sprintf("%s ↔ %s (bidi edge)", r.FromTable, r.ToTable)
			altPath := pathStr(fl, tl, r)
			// Collect all tables for a stable sort in the error message.
			tables := []string{r.FromTable, r.ToTable}
			sort.Strings(tables)
			return &SemanticError{
				Message: fmt.Sprintf(
					"ambiguous filter graph: tables %q and %q are connected by more than one path:\n"+
						"  [1] %s\n"+
						"  [2] %s\n"+
						"remove one of the conflicting relationships or set bidirectional = false",
					tables[0], tables[1], directPath, altPath,
				),
			}
		}
	}
	return nil
}
