package semantic

import (
	"fmt"
	"strings"

	"github.com/danielwikar/dux/parser"
)

// SemanticError is a structured semantic-analysis failure that carries the
// offending AST node for richer error messages.
type SemanticError struct {
	Node    any
	Message string
}

func (e *SemanticError) Error() string {
	return fmt.Sprintf("semantic error: %s", e.Message)
}

// Resolver performs the semantic pass over a parsed Query. It resolves column
// and measure references, validates function argument counts, and annotates
// expressions with row/filter context for the emitter.
type Resolver struct {
	Schema    *Schema
	FilterCtx FilterContext
	RowCtx    RowContext
	// localMeasures is the per-query measure overlay populated from DEFINE blocks.
	// It is initialised as a clone of Schema.Measures so that global measures are
	// visible, and per-query definitions are written here — never into Schema.
	localMeasures map[string]map[string]*parser.MeasureDefinition
	// varNames is the set of lowercased VAR names declared in the current query.
	// Tables with these names are accepted by the resolver without requiring a
	// schema entry, because they are materialised as temp tables at execution time.
	varNames map[string]bool
}

// Resolve runs the full semantic pass: measure pre-resolution followed by the
// main reference-resolution walk over the EVALUATE expression.
//
// Global measures from Schema.Measures and per-query DEFINE measures are merged
// into a per-query overlay so that the shared Schema is never mutated.
func (r *Resolver) Resolve(q *parser.Query) error {
	// Clone the global store so per-query defines layer on top without leaking.
	r.localMeasures = cloneMeasures(r.Schema.Measures)
	if err := PreResolveMeasures(q.Defines, r.localMeasures); err != nil {
		return err
	}
	// Collect VAR names so the resolver can accept them as valid table references
	// in subsequent VAR expressions and in the RETURN clause.
	r.varNames = make(map[string]bool, len(q.Evaluate.Vars))
	// Validate column/measure references in each VAR expression.
	for _, v := range q.Evaluate.Vars {
		if err := r.resolveExpr(v.Expr); err != nil {
			return err
		}
		r.varNames[strings.ToLower(v.Name)] = true
	}
	return r.resolveTableExpr(q.Evaluate.Table)
}

// EffectiveMeasures returns the merged (global + per-query) measure map that
// should be passed to the Emitter. This view is built once per query by
// Resolve and is safe to read until the next Resolve call.
func (r *Resolver) EffectiveMeasures() map[string]map[string]*parser.MeasureDefinition {
	return r.localMeasures
}

// FindMeasureByName searches all tables in measures for a measure with the
// given unqualified name. Returns the definition when exactly one match exists.
// Returns a SemanticError when the name is ambiguous (present in multiple
// tables). Returns nil, nil when no match is found.
func FindMeasureByName(name string, measures map[string]map[string]*parser.MeasureDefinition) (*parser.MeasureDefinition, error) {
	var found *parser.MeasureDefinition
	var foundTable string
	for table, defs := range measures {
		if def, ok := defs[name]; ok {
			if found != nil {
				return nil, &SemanticError{
					Message: fmt.Sprintf(
						"ambiguous measure reference [%s]: defined in both %q and %q; use a table qualifier",
						name, foundTable, table,
					),
				}
			}
			found = def
			foundTable = table
		}
	}
	return found, nil
}

// PreResolveMeasures registers and resolves all DEFINE MEASURE declarations
// into target. target is the Resolver's per-query localMeasures overlay (which
// starts as a clone of Schema.Measures), so the shared Schema is never mutated.
//
// Step 1 registers every measure name into target so that forward references
// are visible. Step 2 walks each measure's expression to resolve its
// column/measure references. Circular references are detected via a visited
// set and returned as a SemanticError.
func PreResolveMeasures(defines []*parser.MeasureDefinition, target map[string]map[string]*parser.MeasureDefinition) error {
	// Step 1: register all names without resolving expressions.
	// Measure names must be unique across all tables within the store; a name
	// that already exists in a different table is rejected to preserve the
	// guarantee that bare [MeasureName] references are unambiguous.
	for _, def := range defines {
		table := StripSingleQuotes(def.Table)
		name := StripBrackets(def.Column)
		// Uniqueness check: is this name already registered under a different table?
		for existingTable, defs := range target {
			if existingTable == table {
				continue
			}
			if _, conflicts := defs[name]; conflicts {
				return &SemanticError{
					Node:    def,
					Message: fmt.Sprintf("measure name %q already defined in table %q; measure names must be unique", name, existingTable),
				}
			}
		}
		if target[table] == nil {
			target[table] = make(map[string]*parser.MeasureDefinition)
		}
		target[table][name] = def
	}

	// Step 2: resolve expressions with cycle detection.
	visited := make(map[string]bool)
	for _, def := range defines {
		if err := resolveMeasureExpr(def, visited); err != nil {
			return err
		}
	}
	return nil
}

func resolveMeasureExpr(def *parser.MeasureDefinition, visited map[string]bool) error {
	key := def.Table + "[" + StripBrackets(def.Column) + "]"
	if visited[key] {
		return &SemanticError{
			Node:    def,
			Message: fmt.Sprintf("circular measure reference detected: %s", key),
		}
	}
	visited[key] = true
	// TODO: walk def.Expr through a full resolver pass
	return nil
}

// cloneMeasures returns a deep copy of a measure map so that per-query
// definitions can be added without modifying the original.
func cloneMeasures(src map[string]map[string]*parser.MeasureDefinition) map[string]map[string]*parser.MeasureDefinition {
	dst := make(map[string]map[string]*parser.MeasureDefinition, len(src))
	for table, defs := range src {
		cloned := make(map[string]*parser.MeasureDefinition, len(defs))
		for name, def := range defs {
			cloned[name] = def
		}
		dst[table] = cloned
	}
	return dst
}

// resolveTableExpr resolves a table-returning expression at the top level.
func (r *Resolver) resolveTableExpr(t *parser.TableExpr) error {
	if t == nil {
		return nil
	}
	if t.Func != nil {
		return r.resolveFuncCall(t.Func)
	}
	// Determine the bare table name (strip quotes if needed).
	tableName := t.Table
	if t.QuotedTable != "" {
		tableName = StripSingleQuotes(t.QuotedTable)
	}
	if tableName == "" {
		return nil
	}
	// VAR-declared names are valid even though they are not in the schema.
	if r.varNames[strings.ToLower(tableName)] {
		return nil
	}
	// Verify the table exists in the schema.
	if _, ok := r.Schema.Tables[tableName]; !ok {
		return &SemanticError{
			Node:    t,
			Message: fmt.Sprintf("unknown table %q", tableName),
		}
	}
	return nil
}

// resolveExpr recursively resolves all references within a binary expression.
func (r *Resolver) resolveExpr(e *parser.Expr) error {
	if e == nil {
		return nil
	}
	if err := r.resolveTerm(e.Left); err != nil {
		return err
	}
	for _, op := range e.Right {
		if err := r.resolveTerm(op.Right); err != nil {
			return err
		}
	}
	return nil
}

// resolveTerm dispatches to the appropriate resolver for each term variant.
func (r *Resolver) resolveTerm(t *parser.Term) error {
	if t == nil {
		return nil
	}
	switch {
	case t.TableConstructor != nil:
		for _, v := range t.TableConstructor.Values {
			if err := r.resolveExpr(v); err != nil {
				return err
			}
		}
		return nil
	case t.FuncCall != nil:
		return r.resolveFuncCall(t.FuncCall)
	case t.ColRef != nil:
		return r.resolveColRef(t.ColRef)
	case t.Literal != nil:
		return nil // literals always valid
	case t.SubExpr != nil:
		return r.resolveExpr(t.SubExpr)
	case t.Ident != "":
		// Bare identifier: VAR names and unknown idents are accepted at runtime.
		return nil
	case t.QuotedIdent != "":
		// Quoted table name: accepted at runtime.
		return nil
	}
	return nil
}

// resolveFuncCall validates a function name and resolves its arguments.
// Iterator functions (SUMX, ADDCOLUMNS, etc.) push a RowBinding before
// resolving their expression arguments.
func (r *Resolver) resolveFuncCall(fc *parser.FuncCall) error {
	name := strings.ToUpper(fc.Name)

	switch name {
	case "SUMX", "AVERAGEX", "COUNTX", "MINX", "MAXX", "CONCATENATEX",
		"ADDCOLUMNS", "SELECTCOLUMNS":
		return r.resolveIterFunc(fc)
	case "CALCULATE":
		return r.resolveCalculate(fc)
	default:
		// Generic resolution: just walk all argument expressions.
		for _, arg := range fc.Args {
			if err := r.resolveExpr(arg); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveIterFunc resolves the source table and inner expression of an
// iterator function, pushing a RowBinding for the duration.
func (r *Resolver) resolveIterFunc(fc *parser.FuncCall) error {
	if len(fc.Args) < 1 {
		return &SemanticError{Node: fc, Message: fmt.Sprintf("%s requires at least 1 argument", fc.Name)}
	}

	// First argument must be a table expression (bare Ident or TableExpr FuncCall).
	// For resolution purposes, just walk the expression.
	if err := r.resolveExpr(fc.Args[0]); err != nil {
		return err
	}

	// Push a row binding for the source table so inner ColRefs resolve to
	// the lateral alias rather than the outer aggregate scope.
	tableName := extractTableName(fc.Args[0])
	alias := "__row_" + strings.ToLower(tableName)
	r.RowCtx.Push(RowBinding{Table: tableName, Alias: alias})
	defer r.RowCtx.Pop()

	for _, arg := range fc.Args[1:] {
		if err := r.resolveExpr(arg); err != nil {
			return err
		}
	}
	return nil
}

// resolveCalculate resolves the inner expression and filter arguments of CALCULATE.
func (r *Resolver) resolveCalculate(fc *parser.FuncCall) error {
	if len(fc.Args) == 0 {
		return &SemanticError{Node: fc, Message: "CALCULATE requires at least 1 argument"}
	}
	// TODO: parse filter arguments and push a FilterSet onto FilterCtx
	for _, arg := range fc.Args {
		if err := r.resolveExpr(arg); err != nil {
			return err
		}
	}
	return nil
}

// resolveColRef verifies that a column reference names a known table and column.
// A bare [Name] with no table qualifier is first checked against the measure
// store; if found as a measure the reference is valid. If not found as a
// measure it is treated as a plain column reference (deferred to runtime).
func (r *Resolver) resolveColRef(cr *parser.ColRef) error {
	if cr.Table == "" {
		// Try bare measure lookup first.
		name := StripBrackets(cr.Column)
		_, err := FindMeasureByName(name, r.localMeasures)
		if err != nil {
			return err // ambiguous
		}
		// Whether or not it's a measure, we don't error — a bare column ref
		// without a table qualifier is resolved at runtime against the active scope.
		return nil
	}
	tableName := StripSingleQuotes(cr.Table)
	// VAR-declared names are not in the schema but are valid at execution time.
	if r.varNames[strings.ToLower(tableName)] {
		return nil
	}
	col := StripBrackets(cr.Column)
	// Check the measure store before the column list — Table[MeasureName] is
	// valid when the name is a declared measure in that table.
	if tableMeasures, ok := r.localMeasures[tableName]; ok {
		if _, isMeasure := tableMeasures[col]; isMeasure {
			return nil
		}
	}
	t, ok := r.Schema.Tables[tableName]
	if !ok {
		return &SemanticError{
			Node:    cr,
			Message: fmt.Sprintf("unknown table %q", tableName),
		}
	}
	if _, ok := t.Columns[col]; !ok {
		return &SemanticError{
			Node:    cr,
			Message: fmt.Sprintf("unknown column %q in table %q", col, tableName),
		}
	}
	return nil
}

// StripBrackets removes the surrounding [ ] from a ColRef token value.
//
//	"[Amount]"        → "Amount"
//	"[Total Revenue]" → "Total Revenue"
func StripBrackets(s string) string {
	if len(s) >= 2 && s[0] == '[' && s[len(s)-1] == ']' {
		return s[1 : len(s)-1]
	}
	return s
}

// StripSingleQuotes removes surrounding single quotes from a QuotedIdent token.
//
//	"'Order Lines'" → "Order Lines"
//	"Sales"         → "Sales"  (no-op for unquoted names)
func StripSingleQuotes(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	return s
}

// extractTableName attempts to determine the table name from the first
// argument to an iterator function. Returns an empty string if the table
// cannot be statically determined. The returned name is always stripped of
// surrounding single quotes.
func extractTableName(e *parser.Expr) string {
	if e == nil || e.Left == nil {
		return ""
	}
	t := e.Left
	if t.QuotedIdent != "" {
		return StripSingleQuotes(t.QuotedIdent)
	}
	if t.Ident != "" {
		return t.Ident
	}
	if t.ColRef != nil && t.ColRef.Table != "" {
		return t.ColRef.Table
	}
	if t.FuncCall != nil {
		// e.g. FILTER(Sales, ...) — table is determined at runtime
		return ""
	}
	return ""
}
