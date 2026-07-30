package semantic

import (
	"errors"
	"fmt"
	"strings"

	"github.com/danielwikar/dux/parser"
)

// SemanticError is a structured semantic-analysis failure.
type SemanticError struct {
	Message string
	// Line and Column are the 1-based source position of the offending
	// reference; 0 when unknown.
	Line   int
	Column int
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
	// allowBareColumns is limited to predicates/order keys over computed tables,
	// whose output columns are not part of the semantic model.
	allowBareColumns bool
	measureState     map[*parser.MeasureDefinition]uint8
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
	r.measureState = map[*parser.MeasureDefinition]uint8{}
	if err := r.resolveMeasures(q.Defines); err != nil {
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
	for table := range measures {
		if def := FindMeasure(table, name, measures); def != nil {
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

// FindMeasure resolves a table-qualified measure case-insensitively.
func FindMeasure(table, name string, measures map[string]map[string]*parser.MeasureDefinition) *parser.MeasureDefinition {
	for tableKey, defs := range measures {
		if !strings.EqualFold(tableKey, table) {
			continue
		}
		for measureName, def := range defs {
			if strings.EqualFold(measureName, name) {
				return def
			}
		}
	}
	return nil
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
		for existingTable := range target {
			if strings.EqualFold(existingTable, table) {
				table = existingTable
				break
			}
		}
		name := StripBrackets(def.Column)
		if existingTable, conflicts := MeasureNameConflict(target, table, name); conflicts {
			return &SemanticError{
				Message: fmt.Sprintf("measure name %q already defined in table %q; measure names must be unique", name, existingTable),
			}
		}
		if target[table] == nil {
			target[table] = make(map[string]*parser.MeasureDefinition)
		}
		storedName := name
		for existingName := range target[table] {
			if strings.EqualFold(existingName, name) {
				storedName = existingName
				break
			}
		}
		target[table][storedName] = def
	}
	return nil
}

// resolveMeasures validates the complete global + query-local dependency graph
// before the emitter expands any definition.
func (r *Resolver) resolveMeasures(definitions []*parser.MeasureDefinition) error {
	var visit func(*parser.MeasureDefinition) error
	visit = func(def *parser.MeasureDefinition) error {
		switch r.measureState[def] {
		case 1:
			return &SemanticError{Message: fmt.Sprintf("circular measure reference involving %s%s", def.Table, def.Column)}
		case 2:
			return nil
		}
		r.measureState[def] = 1
		if err := r.resolveMeasureExpr(def.Expr, visit); err != nil {
			return err
		}
		r.measureState[def] = 2
		return nil
	}
	for _, def := range definitions {
		if err := visit(def); err != nil {
			return err
		}
	}
	return nil
}

func (r *Resolver) resolveMeasureExpr(expr *parser.Expr, visit func(*parser.MeasureDefinition) error) error {
	if expr == nil {
		return nil
	}
	terms := []*parser.Term{expr.Left}
	for _, op := range expr.Right {
		terms = append(terms, op.Right)
	}
	for _, term := range terms {
		if term == nil {
			continue
		}
		if term.ColRef != nil {
			cr := term.ColRef
			name := StripBrackets(cr.Column)
			var def *parser.MeasureDefinition
			if cr.Table == "" {
				var err error
				def, err = FindMeasureByName(name, r.localMeasures)
				if err != nil {
					return err
				}
				if def == nil {
					return &SemanticError{Message: fmt.Sprintf("unknown measure %q", name), Line: cr.Pos.Line, Column: cr.Pos.Column}
				}
			} else {
				def = FindMeasure(StripSingleQuotes(cr.Table), name, r.localMeasures)
				if def == nil {
					if err := r.resolveColRef(cr); err != nil {
						return err
					}
					continue
				}
			}
			if err := visit(def); err != nil {
				var semanticErr *SemanticError
				if errors.As(err, &semanticErr) && semanticErr.Line == 0 {
					semanticErr.Line, semanticErr.Column = cr.Pos.Line, cr.Pos.Column
				}
				return err
			}
		}
		if term.FuncCall != nil {
			for _, arg := range term.FuncCall.Args {
				if err := r.resolveMeasureExpr(arg, visit); err != nil {
					return err
				}
			}
		}
		if term.SubExpr != nil {
			if err := r.resolveMeasureExpr(term.SubExpr, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

// MeasureNameConflict reports the first table in measures — other than the
// given table — that already defines name. Measure names must be unique across
// tables so that bare [MeasureName] references stay unambiguous.
func MeasureNameConflict(measures map[string]map[string]*parser.MeasureDefinition, table, name string) (string, bool) {
	for existingTable, defs := range measures {
		if strings.EqualFold(existingTable, table) {
			continue
		}
		for existingName := range defs {
			if strings.EqualFold(existingName, name) {
				return existingTable, true
			}
		}
	}
	return "", false
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
			Line:    t.Pos.Line,
			Column:  t.Pos.Column,
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
		for _, row := range t.TableConstructor.Rows {
			for _, v := range row.Values {
				if err := r.resolveExpr(v); err != nil {
					return err
				}
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
		// db.table as a bare term (e.g. first arg of FILTER(analytics.Sales, ...))
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
	for i, arg := range fc.Args {
		// TOPN order keys over computed tables name output columns, not model
		// measures. The emitter validates their shape against the table result.
		if strings.EqualFold(fc.Name, "TOPN") && i >= 2 && arg != nil && arg.Left != nil &&
			arg.Left.ColRef != nil && arg.Left.ColRef.Table == "" && len(arg.Right) == 0 {
			continue
		}
		if strings.EqualFold(fc.Name, "FILTER") && i == 1 {
			prev := r.allowBareColumns
			r.allowBareColumns = true
			err := r.resolveExpr(arg)
			r.allowBareColumns = prev
			if err != nil {
				return err
			}
			continue
		}
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
		if r.allowBareColumns {
			return nil
		}
		// Try bare measure lookup first.
		name := StripBrackets(cr.Column)
		def, err := FindMeasureByName(name, r.localMeasures)
		if err != nil {
			// Ambiguous — anchor the error to this reference.
			var se *SemanticError
			if errors.As(err, &se) && se.Line == 0 {
				se.Line, se.Column = cr.Pos.Line, cr.Pos.Column
			}
			return err
		}
		if def == nil {
			return &SemanticError{Message: fmt.Sprintf("unknown measure %q", name), Line: cr.Pos.Line, Column: cr.Pos.Column}
		}
		return r.resolveMeasures([]*parser.MeasureDefinition{def})
	}
	tableName := StripSingleQuotes(cr.Table)
	// VAR-declared names are not in the schema but are valid at execution time.
	if r.varNames[strings.ToLower(tableName)] {
		return nil
	}
	col := StripBrackets(cr.Column)
	// Check the measure store before the column list — Table[MeasureName] is
	// valid when the name is a declared measure in that table.
	if def := FindMeasure(tableName, col, r.localMeasures); def != nil {
		return r.resolveMeasures([]*parser.MeasureDefinition{def})
	}
	// Look up the table in the schema. Accept both the bare name and the
	// qualified name (db.table) transparently.
	t, _, canonicalCol := r.Schema.FindColumn(tableName, col)
	if t == nil {
		return &SemanticError{
			Message: fmt.Sprintf("unknown table %q", tableName),
			Line:    cr.Pos.Line,
			Column:  cr.Pos.Column,
		}
	}
	if canonicalCol == "" {
		return &SemanticError{
			Message: fmt.Sprintf("unknown column %q in table %q", col, tableName),
			Line:    cr.Pos.Line,
			Column:  cr.Pos.Column,
		}
	}
	return nil
}

// tableExists reports whether tableName is present in the schema. It checks
// both the raw key and, for unqualified names, scans all qualified keys so
// that "Sales" resolves when tables are keyed as "analytics.Sales".
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
		return strings.ReplaceAll(s[1:len(s)-1], "]]", "]")
	}
	return s
}

// StripSingleQuotes removes surrounding single quotes from a QuotedIdent token.
//
//	"'Order Lines'" → "Order Lines"
//	"Sales"         → "Sales"  (no-op for unquoted names)
func StripSingleQuotes(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}
