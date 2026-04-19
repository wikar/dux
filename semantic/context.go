package semantic

import "github.com/danielwikar/dux/parser"

// ColIdentity uniquely identifies a column within the schema by its
// table and column names (both unbracketed).
type ColIdentity struct {
	Table  string
	Column string
}

// Predicate is a simple equality or comparison filter on a column.
type Predicate struct {
	Op    string          // "=", ">", "<", ">=", "<=", "<>"
	Value *parser.Literal // the right-hand scalar
}

// FilterSet is an immutable snapshot of filter predicates active in a
// CALCULATE expression. It maps each filtered column to its predicate.
// Multiple predicates for a single column are ANDed together.
type FilterSet map[ColIdentity]Predicate

// FilterContext is a stack of FilterSets. Each CALCULATE call pushes a new
// layer; the emitter merges all active layers into the WHERE clause.
type FilterContext struct {
	stack []FilterSet
}

// Push adds fs as the innermost filter layer.
func (fc *FilterContext) Push(fs FilterSet) {
	fc.stack = append(fc.stack, fs)
}

// Pop removes the innermost filter layer. No-op on an empty stack.
func (fc *FilterContext) Pop() {
	if len(fc.stack) > 0 {
		fc.stack = fc.stack[:len(fc.stack)-1]
	}
}

// Active returns a merged view of all currently active filters. A column
// present in multiple layers retains only the innermost predicate
// (last writer wins), which matches CALCULATE override semantics.
func (fc *FilterContext) Active() FilterSet {
	merged := make(FilterSet)
	for _, fs := range fc.stack {
		for k, v := range fs {
			merged[k] = v
		}
	}
	return merged
}

// ClearTable removes all filters for the given table name from the context.
// Used to implement ALL(Table).
func (fc *FilterContext) ClearTable(table string) {
	for _, fs := range fc.stack {
		for k := range fs {
			if k.Table == table {
				delete(fs, k)
			}
		}
	}
}

// ClearColumn removes the filter for a specific column.
// Used to implement ALL(Table[Column]).
func (fc *FilterContext) ClearColumn(table, column string) {
	key := ColIdentity{Table: table, Column: column}
	for _, fs := range fc.stack {
		delete(fs, key)
	}
}

// RowBinding associates a source table with the unique lateral alias used for
// it inside an iterator function. Aliases take the form "__row_<table>".
type RowBinding struct {
	Table string
	Alias string // e.g. "__row_sales"
}

// RowContext tracks the stack of active row bindings introduced by iterator
// functions (SUMX, AVERAGEX, ADDCOLUMNS, SELECTCOLUMNS, etc.).
// Bindings are stored innermost-last so the innermost alias is found first
// during column resolution.
type RowContext struct {
	Bindings []RowBinding
}

// Push adds a new row binding for table. The caller must Pop after the
// iterator's expression argument has been resolved/emitted.
func (rc *RowContext) Push(b RowBinding) {
	rc.Bindings = append(rc.Bindings, b)
}

// Pop removes the innermost row binding. No-op on an empty stack.
func (rc *RowContext) Pop() {
	if len(rc.Bindings) > 0 {
		rc.Bindings = rc.Bindings[:len(rc.Bindings)-1]
	}
}

// ResolveAlias returns the lateral alias for the given table if it has an
// active row binding, searching innermost-first.
func (rc *RowContext) ResolveAlias(table string) (alias string, ok bool) {
	for i := len(rc.Bindings) - 1; i >= 0; i-- {
		if rc.Bindings[i].Table == table {
			return rc.Bindings[i].Alias, true
		}
	}
	return "", false
}
