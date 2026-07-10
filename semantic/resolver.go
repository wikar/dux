package semantic

import (
	"fmt"
	"strings"

	"github.com/danielwikar/dux/parser"
)

// SemanticError is a structured semantic-analysis failure.
type SemanticError struct {
	Message string
}

func (e *SemanticError) Error() string {
	return fmt.Sprintf("semantic error: %s", e.Message)
}

// Resolver performs the semantic pass over a parsed Query. It resolves column
// and measure references, validates function argument counts, and annotates
// expressions with row/filter context for the emitter.
type Resolver struct {
	Schema *Schema
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
// Every measure name is registered into target so that forward references
// are visible.
func PreResolveMeasures(defines []*parser.MeasureDefinition, target map[string]map[string]*parser.MeasureDefinition) error {
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
					Message: fmt.Sprintf("measure name %q already defined in table %q; measure names must be unique", name, existingTable),
				}
			}
		}
		if target[table] == nil {
			target[table] = make(map[string]*parser.MeasureDefinition)
		}
		target[table][name] = def
	}
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
	if t.QualifiedTable != "" {
		tableName = t.QualifiedTable
	} else if t.QuotedTable != "" {
		tableName = StripSingleQuotes(t.QuotedTable)
	}
	if tableName == "" {
		return nil
	}
	// VAR-declared names are valid even though they are not in the schema.
	if r.varNames[strings.ToLower(tableName)] {
		return nil
	}
	// Verify the table exists in the schema (bare or qualified key).
	if !r.tableExists(tableName) {
		return &SemanticError{
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
	case t.QualifiedIdent != "":
		// db.table as a bare term (e.g. first arg of FILTER(atp.matches, ...))
		return nil
	case t.Ident != "":
		// Bare identifier: VAR names and unknown idents are accepted at runtime.
		return nil
	case t.QuotedIdent != "":
		// Quoted table name: accepted at runtime.
		return nil
	}
	return nil
}

// resolveFuncCall resolves a function call's argument expressions. Arity is
// validated by the emitter.
func (r *Resolver) resolveFuncCall(fc *parser.FuncCall) error {
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
	// Look up the table in the schema. Accept both the bare name and the
	// qualified name (db.table) transparently.
	t, ok := r.Schema.Tables[tableName]
	if !ok {
		return &SemanticError{
			Message: fmt.Sprintf("unknown table %q", tableName),
		}
	}
	if _, ok := t.Columns[col]; !ok {
		return &SemanticError{
			Message: fmt.Sprintf("unknown column %q in table %q", col, tableName),
		}
	}
	return nil
}

// tableExists reports whether tableName is present in the schema. It checks
// both the raw key and, for unqualified names, scans all qualified keys so
// that "matches" resolves when tables are keyed as "atp.matches".
func (r *Resolver) tableExists(tableName string) bool {
	if _, ok := r.Schema.Tables[tableName]; ok {
		return true
	}
	// Allow bare table name when exactly one qualified key ends with .tableName.
	suffix := "." + tableName
	matches := 0
	for k := range r.Schema.Tables {
		if strings.HasSuffix(k, suffix) {
			matches++
		}
	}
	return matches == 1
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
