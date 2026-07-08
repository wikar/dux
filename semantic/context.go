package semantic

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
