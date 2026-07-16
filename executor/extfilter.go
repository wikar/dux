// External filter injection: structured filters supplied alongside a query
// (e.g. by dashboard slicers) are desugared into DUX AST nodes after parsing,
// so they compose with the query's own filter context exactly like filters the
// user could have written by hand — including CALCULATE modifier interaction
// (ALL, KEEPFILTERS) and cross-cluster routing.
//
// Injection strategy, by query shape:
//
//  1. The evaluate expression is (or wraps, via TOPN/ADDCOLUMNS/SELECTCOLUMNS/
//     FILTER) a SUMMARIZECOLUMNS call: each filter becomes a group-position
//     argument — TREATAS({...}, T[C]) for set membership, FILTER(T, pred) for
//     ranges and comparisons. This is the same form the query builder emits,
//     handled by the existing emitter machinery.
//  2. Otherwise, when the expression resolves to a base table and every filter
//     targets that table, the whole expression is wrapped in
//     FILTER(expr, pred1 && pred2 && ...).
//  3. Anything else (VAR returns, UNION, ...) is rejected with a clear error.
package executor

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// ExternalFilter is one structured filter to apply to a query's outermost
// filter context. Exactly one of Values (op "in"), Value (scalar ops and
// "contains"), or From/To (op "between") must be populated.
type ExternalFilter struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	// Op is one of: "in", "between", "=", "!=", "<", "<=", ">", ">=", "contains".
	Op     string `json:"op"`
	Values []any  `json:"values,omitempty"`
	Value  any    `json:"value,omitempty"`
	From   any    `json:"from,omitempty"`
	To     any    `json:"to,omitempty"`
}

// resolvedFilter is an ExternalFilter validated against the schema.
type resolvedFilter struct {
	table   string // canonical schema table key
	column  string // canonical column name
	op      string
	numeric bool
	values  []*parser.Literal // in
	value   *parser.Literal   // scalar ops, contains
	from    *parser.Literal   // between
	to      *parser.Literal
}

var numericTypeRe = regexp.MustCompile(`(?i)^(TINYINT|SMALLINT|INTEGER|BIGINT|HUGEINT|UTINYINT|USMALLINT|UINTEGER|UBIGINT|DOUBLE|FLOAT|REAL|DECIMAL|NUMERIC)`)

var validFilterOps = map[string]bool{
	"in": true, "between": true, "contains": true,
	"=": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true,
}

// ApplyExternalFilters validates filters against schema and rewrites q's
// evaluate expression so the filters apply to its outermost filter context.
func ApplyExternalFilters(q *parser.Query, schema *semantic.Schema, filters []ExternalFilter) error {
	if len(filters) == 0 {
		return nil
	}
	resolved := make([]*resolvedFilter, len(filters))
	for i, f := range filters {
		rf, err := resolveFilter(schema, f)
		if err != nil {
			return fmt.Errorf("external filter %d (%s[%s]): %w", i+1, f.Table, f.Column, err)
		}
		resolved[i] = rf
	}

	if sc := findSummarizeColumns(q.Evaluate.Table); sc != nil {
		injectSummarizeArgs(sc, resolved)
		return nil
	}

	base := baseTableOfTableExpr(q.Evaluate.Table)
	var baseKey string
	if base != "" {
		if tbl, key := schema.FindTable(base); tbl != nil {
			baseKey = key
		}
	}
	if baseKey == "" {
		// No injectable SUMMARIZECOLUMNS and no schema base table (VAR
		// returns, UNION, ...) — nothing safe to attach the filters to.
		return fmt.Errorf("external filters require a SUMMARIZECOLUMNS query or a query over a single base table")
	}
	for i, rf := range resolved {
		if !strings.EqualFold(rf.table, baseKey) && !strings.EqualFold(rf.table, base) {
			return fmt.Errorf(
				"external filter %d targets table %q, but this query shape only accepts filters on its base table %q",
				i+1, rf.table, base)
		}
	}
	wrapInFilter(q.Evaluate, resolved)
	return nil
}

// resolveFilter validates one filter against the schema and coerces its
// operand values to literals matching the column type.
func resolveFilter(schema *semantic.Schema, f ExternalFilter) (*resolvedFilter, error) {
	if f.Table == "" || f.Column == "" {
		return nil, fmt.Errorf("table and column are required")
	}
	table, tableKey := schema.FindTable(f.Table)
	if table == nil {
		return nil, fmt.Errorf("unknown table")
	}
	col := table.Columns[f.Column]
	if col == nil {
		for name, c := range table.Columns {
			if strings.EqualFold(name, f.Column) {
				col = c
				break
			}
		}
	}
	if col == nil {
		return nil, fmt.Errorf("unknown column")
	}
	op := f.Op
	if op == "" {
		op = "in"
	}
	if !validFilterOps[op] {
		return nil, fmt.Errorf("unsupported op %q", op)
	}

	rf := &resolvedFilter{
		table:   tableKey,
		column:  col.Name,
		op:      op,
		numeric: numericTypeRe.MatchString(col.DataType),
	}

	lit := func(v any, what string) (*parser.Literal, error) {
		l, err := literalFor(v, rf.numeric)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}
		return l, nil
	}

	var err error
	switch op {
	case "in":
		if len(f.Values) == 0 {
			return nil, fmt.Errorf(`op "in" requires a non-empty values array`)
		}
		for _, v := range f.Values {
			l, err := lit(v, "values")
			if err != nil {
				return nil, err
			}
			rf.values = append(rf.values, l)
		}
	case "between":
		if f.From == nil || f.To == nil {
			return nil, fmt.Errorf(`op "between" requires from and to`)
		}
		if rf.from, err = lit(f.From, "from"); err != nil {
			return nil, err
		}
		if rf.to, err = lit(f.To, "to"); err != nil {
			return nil, err
		}
	case "contains":
		if f.Value == nil {
			return nil, fmt.Errorf(`op %q requires a value`, op)
		}
		if rf.numeric {
			return nil, fmt.Errorf(`op "contains" is not valid for numeric columns`)
		}
		if rf.value, err = lit(f.Value, "value"); err != nil {
			return nil, err
		}
	default: // scalar comparisons
		if f.Value == nil {
			return nil, fmt.Errorf(`op %q requires a value`, op)
		}
		if rf.value, err = lit(f.Value, "value"); err != nil {
			return nil, err
		}
	}
	return rf, nil
}

// literalFor converts a JSON-decoded value into a parser literal, coercing to
// the column's type: numeric columns get Number literals, everything else
// (strings, dates, ...) gets String literals.
//
// Note: parser.Literal.String holds the raw lexer token INCLUDING surrounding
// double quotes (the emitter strips them), so synthetic literals must be
// stored in the same form.
func literalFor(v any, numeric bool) (*parser.Literal, error) {
	str := func(s string) *parser.Literal {
		raw := `"` + s + `"`
		return &parser.Literal{String: &raw}
	}
	num := func(n float64) *parser.Literal { return &parser.Literal{Number: &n} }

	switch t := v.(type) {
	case float64:
		if numeric {
			return num(t), nil
		}
		return str(strconv.FormatFloat(t, 'f', -1, 64)), nil
	case int:
		return literalFor(float64(t), numeric)
	case int32:
		return literalFor(float64(t), numeric)
	case int64:
		return literalFor(float64(t), numeric)
	case float32:
		return literalFor(float64(t), numeric)
	case string:
		if numeric {
			n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
			if err != nil {
				return nil, fmt.Errorf("%q is not numeric", t)
			}
			return num(n), nil
		}
		return str(t), nil
	case bool:
		kw := "FALSE"
		if t {
			kw = "TRUE"
		}
		return &parser.Literal{Boolean: &kw}, nil
	case nil:
		return nil, fmt.Errorf("value must not be null")
	default:
		return nil, fmt.Errorf("unsupported value type %T", v)
	}
}

// ─── AST construction helpers ────────────────────────────────────────────────

var plainIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// tableToken returns the raw token form of a table name as the parser would
// have captured it: dotted names stay bare (QualifiedIdent), plain identifiers
// stay bare (Ident), anything else is single-quoted (QuotedIdent).
func tableToken(name string) string {
	if strings.Contains(name, ".") || plainIdentRe.MatchString(name) {
		return name
	}
	return "'" + name + "'"
}

// tableTerm builds a bare-table Term (e.g. the first argument of FILTER).
func tableTerm(name string) *parser.Term {
	switch {
	case strings.Contains(name, "."):
		return &parser.Term{QualifiedIdent: name}
	case plainIdentRe.MatchString(name):
		return &parser.Term{Ident: name}
	default:
		return &parser.Term{QuotedIdent: "'" + name + "'"}
	}
}

func exprOf(t *parser.Term) *parser.Expr { return &parser.Expr{Left: t} }

func colRefExpr(table, column string) *parser.Expr {
	return exprOf(&parser.Term{ColRef: &parser.ColRef{
		Table:  tableToken(table),
		Column: "[" + column + "]",
	}})
}

func litExpr(l *parser.Literal) *parser.Expr {
	return exprOf(&parser.Term{Literal: l})
}

func funcExpr(name string, args ...*parser.Expr) *parser.Expr {
	return exprOf(&parser.Term{FuncCall: &parser.FuncCall{Name: name, Args: args}})
}

// binary builds `left <op> right` as a flat two-term expression.
func binary(left *parser.Expr, op string, right *parser.Expr) *parser.Expr {
	return &parser.Expr{Left: left.Left, Right: append(append([]*parser.OpExpr{}, left.Right...),
		&parser.OpExpr{Op: op, Right: sub(right)})}
}

// sub wraps an expression as a parenthesised term so multi-operator chains
// emit with explicit grouping (the emitter does not encode precedence).
func sub(e *parser.Expr) *parser.Term {
	if len(e.Right) == 0 && e.Left != nil {
		return e.Left
	}
	return &parser.Term{SubExpr: e}
}

// conjoin ANDs the expressions together with explicit grouping.
func conjoin(preds []*parser.Expr) *parser.Expr {
	out := &parser.Expr{Left: sub(preds[0])}
	for _, p := range preds[1:] {
		out.Right = append(out.Right, &parser.OpExpr{Op: "&&", Right: sub(p)})
	}
	return out
}

// predicate builds the boolean expression form of a filter (used both inside
// FILTER(T, pred) arguments and for the whole-query FILTER wrap).
func (rf *resolvedFilter) predicate() *parser.Expr {
	col := func() *parser.Expr { return colRefExpr(rf.table, rf.column) }
	switch rf.op {
	case "in":
		ors := make([]*parser.Expr, len(rf.values))
		for i, v := range rf.values {
			ors[i] = binary(col(), "=", litExpr(v))
		}
		if len(ors) == 1 {
			return ors[0]
		}
		out := &parser.Expr{Left: &parser.Term{SubExpr: ors[0]}}
		for _, o := range ors[1:] {
			out.Right = append(out.Right, &parser.OpExpr{Op: "||", Right: &parser.Term{SubExpr: o}})
		}
		return out
	case "between":
		ge := binary(col(), ">=", litExpr(rf.from))
		le := binary(col(), "<=", litExpr(rf.to))
		return &parser.Expr{
			Left:  &parser.Term{SubExpr: ge},
			Right: []*parser.OpExpr{{Op: "&&", Right: &parser.Term{SubExpr: le}}},
		}
	case "contains":
		zero := 0.0
		return binary(funcExpr("SEARCH", litExpr(rf.value), col()), ">", litExpr(&parser.Literal{Number: &zero}))
	default:
		op := rf.op
		if op == "!=" {
			op = "<>"
		}
		return binary(col(), op, litExpr(rf.value))
	}
}

// filterArg builds the SUMMARIZECOLUMNS group-position argument form of a
// filter: TREATAS for set membership, FILTER(T, pred) for everything else.
func (rf *resolvedFilter) filterArg() *parser.Expr {
	if rf.op == "in" {
		values := make([]*parser.Expr, len(rf.values))
		for i, v := range rf.values {
			values[i] = litExpr(v)
		}
		return funcExpr("TREATAS",
			exprOf(&parser.Term{TableConstructor: &parser.TableConstructor{Values: values}}),
			colRefExpr(rf.table, rf.column))
	}
	return funcExpr("FILTER", exprOf(tableTerm(rf.table)), rf.predicate())
}

// ─── Injection ───────────────────────────────────────────────────────────────

// findSummarizeColumns walks the evaluate expression through row-preserving
// wrappers to the SUMMARIZECOLUMNS call filters can be injected into.
func findSummarizeColumns(t *parser.TableExpr) *parser.FuncCall {
	fc := t.Func
	for fc != nil {
		switch strings.ToUpper(fc.Name) {
		case "SUMMARIZECOLUMNS":
			return fc
		case "TOPN":
			if len(fc.Args) < 2 {
				return nil
			}
			fc = funcCallOf(fc.Args[1])
		case "ADDCOLUMNS", "SELECTCOLUMNS", "FILTER":
			if len(fc.Args) < 1 {
				return nil
			}
			fc = funcCallOf(fc.Args[0])
		default:
			return nil
		}
	}
	return nil
}

// funcCallOf returns the FuncCall when expr is a bare function-call term.
func funcCallOf(e *parser.Expr) *parser.FuncCall {
	if e == nil || e.Left == nil || len(e.Right) > 0 || e.Left.Neg {
		return nil
	}
	return e.Left.FuncCall
}

// injectSummarizeArgs inserts the filters as group-position arguments, before
// the first string literal (where SUMMARIZECOLUMNS's name/expression measure
// pairs begin).
func injectSummarizeArgs(sc *parser.FuncCall, filters []*resolvedFilter) {
	split := len(sc.Args)
	for i, arg := range sc.Args {
		if arg != nil && len(arg.Right) == 0 && arg.Left != nil &&
			arg.Left.Literal != nil && arg.Left.Literal.String != nil {
			split = i
			break
		}
	}
	args := make([]*parser.Expr, 0, len(sc.Args)+len(filters))
	args = append(args, sc.Args[:split]...)
	for _, rf := range filters {
		args = append(args, rf.filterArg())
	}
	args = append(args, sc.Args[split:]...)
	sc.Args = args
}

// baseTableOfTableExpr walks row-preserving functions down to the base table
// the evaluate expression iterates, mirroring the emitter's underlyingTableName.
// Returns "" when the shape has no single base table.
func baseTableOfTableExpr(t *parser.TableExpr) string {
	switch {
	case t.QualifiedTable != "":
		return t.QualifiedTable
	case t.QuotedTable != "":
		return semantic.StripSingleQuotes(t.QuotedTable)
	case t.Table != "":
		return t.Table
	case t.Func != nil:
		return baseTableOfExprFunc(t.Func)
	}
	return ""
}

func baseTableOfExprFunc(fc *parser.FuncCall) string {
	switch strings.ToUpper(fc.Name) {
	case "FILTER", "ALL", "ALLEXCEPT", "ADDCOLUMNS":
		if len(fc.Args) >= 1 {
			return baseTableOfExpr(fc.Args[0])
		}
	case "TOPN":
		if len(fc.Args) >= 2 {
			return baseTableOfExpr(fc.Args[1])
		}
	case "VALUES", "DISTINCT":
		if len(fc.Args) == 1 {
			if e := fc.Args[0]; e != nil && e.Left != nil && e.Left.ColRef != nil && len(e.Right) == 0 {
				return semantic.StripSingleQuotes(e.Left.ColRef.Table)
			}
			return baseTableOfExpr(fc.Args[0])
		}
	}
	return ""
}

func baseTableOfExpr(e *parser.Expr) string {
	if e == nil || e.Left == nil || len(e.Right) > 0 {
		return ""
	}
	t := e.Left
	switch {
	case t.Ident != "":
		return t.Ident
	case t.QuotedIdent != "":
		return semantic.StripSingleQuotes(t.QuotedIdent)
	case t.QualifiedIdent != "":
		return t.QualifiedIdent
	case t.FuncCall != nil:
		return baseTableOfExprFunc(t.FuncCall)
	}
	return ""
}

// wrapInFilter replaces the evaluate expression with
// FILTER(<original>, <conjunction of filter predicates>).
func wrapInFilter(ev *parser.EvaluateClause, filters []*resolvedFilter) {
	orig := ev.Table
	var innerTerm *parser.Term
	switch {
	case orig.Func != nil:
		innerTerm = &parser.Term{FuncCall: orig.Func}
	case orig.QualifiedTable != "":
		innerTerm = &parser.Term{QualifiedIdent: orig.QualifiedTable}
	case orig.QuotedTable != "":
		innerTerm = &parser.Term{QuotedIdent: orig.QuotedTable}
	default:
		innerTerm = &parser.Term{Ident: orig.Table}
	}
	preds := make([]*parser.Expr, len(filters))
	for i, rf := range filters {
		preds[i] = rf.predicate()
	}
	ev.Table = &parser.TableExpr{
		Pos: orig.Pos,
		Func: &parser.FuncCall{
			Name: "FILTER",
			Args: []*parser.Expr{exprOf(innerTerm), conjoin(preds)},
		},
	}
}
