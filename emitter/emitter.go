// Package emitter walks a resolved AST and produces a DuckDB SQL string.
package emitter

import (
	"fmt"
	"strings"

	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// Emitter walks a semantically-resolved AST and produces DuckDB SQL.
// Schema is used for join inference and column-name resolution.
// Measures is the effective (global + per-query) measure map built by the
// Resolver; when non-nil it takes precedence over Schema.Measures for inline
// measure expansion.
// ScalarVars holds the materialised values of scalar VAR bindings (lowercased
// name → Go value). When a bare identifier in an expression matches a scalar
// VAR name, its literal SQL value is substituted at emit time.
type Emitter struct {
	Schema     *semantic.Schema
	Measures   map[string]map[string]*parser.MeasureDefinition
	Resolution *semantic.Resolution
	ScalarVars map[string]any
	// ctx owns row frames and scoped transition/value-query state. SQL aliases
	// remain emitter-only; semantic lineage comes from Resolution.
	ctx evalContext
	// sqlScopes maps canonical table identities to aliases in nested FROM
	// scopes. The innermost binding wins; unaliased tables need no entry because
	// table-qualified references render with their canonical SQL table name.
	sqlScopes []map[string]string
	// groupCtx is set while emitting SUMMARIZECOLUMNS measure expressions so
	// nested CALCULATE calls can resolve filter-context modifiers (ALL etc.)
	// against the enclosing group-by keys. See filterctx.go.
	groupCtx *groupContext
	// aliasSeq numbers generated subquery aliases (see nextAlias in tables.go).
	aliasSeq int
	// stitchSubst maps aggregate subtrees lifted into stitched cluster CTEs to
	// their CTE column references. Set only while emitting the outer arithmetic
	// of a cross-cluster measure expression (see stitched.go).
	stitchSubst map[*parser.FuncCall]string
	// anchorScans, when non-nil, redirects time-intelligence anchors to the
	// per-table anchor scans of the context CTE being emitted (contextcte.go)
	// instead of correlated scalar subqueries.
	anchorScans *anchorCollector
}

// taggedPred pairs an emitted SQL WHERE predicate with the lower-cased
// canonical name of the table (and, when known, the resolved column) it
// logically filters (e.g. from a TREATAS call). The bidi CTE builder uses the
// table to route predicates into CTE bodies; CALCULATE filter-context
// modifiers use table+col to decide whether ALL(...) clears the predicate.
type taggedPred struct {
	table string // lower-cased canonical table name; "" if unknown
	col   string // lower-cased resolved column name; "" if unknown
	sql   string
	expr  *parser.Expr // original expression, re-emitted inside aliased scopes
}

// Emit produces a DuckDB SQL string from a resolved Query.
// If the query has VAR bindings the caller is responsible for materialising
// them via EmitVarCreate before running this SQL; see executor.Execute.
func (e *Emitter) Emit(q *parser.Query) (string, error) {
	sql, err := e.emitTableExpr(q.Evaluate.Table)
	if err != nil {
		return "", err
	}
	return e.applyOrderBy(sql, q.Evaluate)
}

// EmitVarCreate returns the SQL that creates a session-scoped temp table named
// after the VAR, populated by the result of the VAR's expression. The executor
// calls this once per VarBinding on a pinned connection before running Emit.
func (e *Emitter) EmitVarCreate(name string, expr *parser.Expr) (string, error) {
	body, err := e.emitExprAsTable(expr)
	if err != nil {
		return "", fmt.Errorf("VAR %s: %w", name, err)
	}
	return fmt.Sprintf("CREATE OR REPLACE TEMP TABLE %s AS\n%s", sqlIdent(name), body), nil
}

// ─── Table-level emission ────────────────────────────────────────────────────

func (e *Emitter) emitTableExpr(t *parser.TableExpr) (string, error) {
	if t.Func != nil {
		return e.emitFuncCall(t.Func)
	}
	if t.QualifiedTable != "" {
		return fmt.Sprintf("SELECT * FROM %s", e.sqlTable(t.QualifiedTable)), nil
	}
	if t.QuotedTable != "" {
		return fmt.Sprintf("SELECT * FROM %s", e.sqlTable(semantic.StripSingleQuotes(t.QuotedTable))), nil
	}
	return fmt.Sprintf("SELECT * FROM %s", e.sqlTable(t.Table)), nil
}

// ─── Expression emission ─────────────────────────────────────────────────────

func (e *Emitter) emitExpr(expr *parser.Expr) (string, error) {
	if expr == nil {
		return "", fmt.Errorf("nil expression")
	}
	left, err := e.emitTerm(expr.Left)
	if err != nil {
		return "", err
	}
	if len(expr.Right) == 0 {
		return left, nil
	}
	sql := left
	for _, op := range expr.Right {
		right, err := e.emitTerm(op.Right)
		if err != nil {
			return "", err
		}
		if op.Op == "&" {
			sql = fmt.Sprintf("concat(%s, %s)", sql, right)
			continue
		}
		if op.Op == "==" {
			sql = fmt.Sprintf("%s IS NOT DISTINCT FROM %s", sql, right)
			continue
		}
		sql += " " + normaliseOp(op.Op) + " " + right
	}
	return sql, nil
}

func (e *Emitter) emitTerm(t *parser.Term) (string, error) {
	s, err := e.emitTermBase(t)
	if err != nil {
		return "", err
	}
	if t.Neg {
		return "-(" + s + ")", nil
	}
	return s, nil
}

func (e *Emitter) emitTermBase(t *parser.Term) (string, error) {
	if t == nil {
		return "", fmt.Errorf("nil term")
	}
	switch {
	case t.TableConstructor != nil:
		// A bare table constructor { ... } outside TREATAS is a user error.
		return "", fmt.Errorf("table constructor {...} is only valid as the first argument to TREATAS")
	case t.FuncCall != nil:
		return e.emitFuncCall(t.FuncCall)
	case t.ColRef != nil:
		return e.emitColRef(t.ColRef)
	case t.Literal != nil:
		return e.emitLiteral(t.Literal), nil
	case t.SubExpr != nil:
		s, err := e.emitExpr(t.SubExpr)
		if err != nil {
			return "", err
		}
		return "(" + s + ")", nil
	case t.QuotedIdent != "":
		// Single-quoted table name as a bare term (e.g. 'Order Lines' argument).
		return e.sqlTable(semantic.StripSingleQuotes(t.QuotedIdent)), nil
	case t.QualifiedIdent != "":
		// db.table as a bare term (e.g. first argument of FILTER(analytics.Sales, ...)).
		return e.sqlTable(t.QualifiedIdent), nil
	case t.Ident != "":
		// Check scalar VAR substitution before treating as a table name.
		if e.ScalarVars != nil {
			if val, ok := e.ScalarVars[strings.ToLower(t.Ident)]; ok {
				return anyToSQL(val), nil
			}
		}
		// Bare table name as a term (e.g. first argument of FILTER/ADDCOLUMNS).
		return e.sqlTable(t.Ident), nil
	}
	return "", fmt.Errorf("empty term node")
}

// ─── Column references ───────────────────────────────────────────────────────

// emitColRef emits a column reference. Resolution order:
//  1. Iterator row-context alias (highest priority).
//  2. Table-qualified measure expansion: Table[Name] → inline expression.
//  3. Bare measure expansion: [Name] with no table qualifier, looked up across
//     all tables in the measure store (requires the name to be unique).
//  4. Plain column name (lowest priority, no table qualifier).
func (e *Emitter) emitColRef(cr *parser.ColRef) (string, error) {
	stripped := semantic.StripBrackets(cr.Column)

	// Bare name used for lookups (row context, measure store) — strip quotes.
	tableKey := semantic.StripSingleQuotes(cr.Table)

	if e.Resolution != nil {
		if ref, ok := e.Resolution.Refs[cr]; ok {
			if ref.Kind == semantic.RefMeasure {
				return e.emitMeasureInContext(ref.Measure)
			}
			if e.ctx.valueDepth == 0 || e.ctx.predicateOuter[e.tableKey(ref.Table)] {
				if col, ok := e.rowColumn(ref); ok {
					return col, nil
				}
			}
			tableKey, stripped = ref.Table, ref.Column
		}
	}
	if e.Resolution == nil {
		if def := e.resolveMeasureDef(cr); def != nil {
			return e.emitMeasureInContext(def)
		}
		if alias, ok := e.rowAliasForTable(e.tableKey(tableKey)); ok {
			return alias + "." + e.resolveColName(tableKey, stripped), nil
		}
	}

	if e.Resolution == nil {
		if measures := e.effectiveMeasures(); measures != nil {
			if tableKey != "" {
				// Table-qualified measure expansion.
				if def := semantic.FindMeasure(tableKey, stripped, measures); def != nil && def.Expr != nil {
					return e.emitExpr(def.Expr)
				}
			} else {
				// Bare [MeasureName] — scan all tables; name must be unique.
				def, err := semantic.FindMeasureByName(stripped, measures)
				if err != nil {
					return "", err
				}
				if def != nil && def.Expr != nil {
					return e.emitExpr(def.Expr)
				}
			}
		}
	}

	// Plain column reference — preserve schema casing and qualify every
	// table-qualified reference. Bare references remain bare because they name
	// measures or output columns in the few positions where DUX permits them.
	col := e.resolveColName(tableKey, stripped)
	if tableKey == "" {
		return col, nil
	}
	if alias, ok := e.sqlBinding(e.tableKey(tableKey)); ok {
		return alias + "." + col, nil
	}
	return e.sqlTable(tableKey) + "." + col, nil
}

func (e *Emitter) pushSQLBindings(bindings map[string]string) func() {
	e.sqlScopes = append(e.sqlScopes, bindings)
	return func() { e.sqlScopes = e.sqlScopes[:len(e.sqlScopes)-1] }
}

func (e *Emitter) sqlBinding(tableKey string) (string, bool) {
	for i := len(e.sqlScopes) - 1; i >= 0; i-- {
		if alias, ok := e.sqlScopes[i][tableKey]; ok {
			return alias, true
		}
	}
	return "", false
}

// effectiveMeasures returns the measure lookup map: e.Measures (explicit,
// global+per-query merged) if set, falling back to e.Schema.Measures.
// Returns nil when neither is available (e.g. in schema-free unit tests).
func (e *Emitter) effectiveMeasures() map[string]map[string]*parser.MeasureDefinition {
	if e.Measures != nil {
		return e.Measures
	}
	if e.Schema != nil {
		return e.Schema.Measures
	}
	return nil
}

// resolveColName returns the exact column name to use in emitted SQL.
// When the schema is available and the table+column are found, the schema's
// own casing is used verbatim (e.g. "l_1stIn" stays "l_1stIn"). Otherwise the
// bracket-stripped name is emitted as-is, letting DuckDB validate it.
func (e *Emitter) resolveColName(table, stripped string) string {
	if e.Schema != nil && table != "" {
		if _, _, name := e.Schema.FindColumn(table, stripped); name != "" {
			return name
		}
	}
	return stripped
}

// ─── Literals ────────────────────────────────────────────────────────────────

func (e *Emitter) emitLiteral(l *parser.Literal) string {
	switch {
	case l.String != nil:
		// Raw token includes surrounding double-quotes; convert to SQL single-quoted.
		inner := strings.ReplaceAll((*l.String)[1:len(*l.String)-1], `""`, `"`)
		return "'" + strings.ReplaceAll(inner, "'", "''") + "'"
	case l.Number != nil:
		return *l.Number
	case l.Boolean != nil:
		return strings.ToUpper(*l.Boolean)
	}
	return "NULL"
}

// ─── Function dispatch ───────────────────────────────────────────────────────

// simpleAggs maps DAX aggregate function names to their DuckDB spelling.
var simpleAggs = map[string]string{
	"SUM": "SUM", "AVERAGE": "AVG", "COUNT": "COUNT", "COUNTA": "COUNT",
	"MIN": "MIN", "MAX": "MAX", "MEDIAN": "MEDIAN",
}

// iterAggs maps DAX iterator (row-context) aggregates to their DuckDB spelling.
var iterAggs = map[string]string{
	"SUMX": "SUM", "AVERAGEX": "AVG", "COUNTX": "COUNT", "MINX": "MIN", "MAXX": "MAX",
}

func (e *Emitter) emitFuncCall(fc *parser.FuncCall) (string, error) {
	// A lifted aggregate inside a cross-cluster measure expression emits as
	// its stitched cluster CTE column (see stitched.go).
	if e.stitchSubst != nil {
		if ref, ok := e.stitchSubst[fc]; ok {
			return ref, nil
		}
	}
	name := strings.ToUpper(fc.Name)
	if sqlFn, ok := simpleAggs[name]; ok {
		return e.emitSimpleAgg(sqlFn, fc)
	}
	if sqlFn, ok := iterAggs[name]; ok {
		return e.emitIterAgg(sqlFn, fc)
	}
	switch name {
	// Aggregation
	case "COUNTBLANK":
		return e.emitCountBlank(fc)
	case "COUNTROWS":
		return e.emitCountRows(fc)
	case "DISTINCTCOUNT":
		return e.emitDistinctCount(fc)
	case "CONCATENATEX":
		return e.emitConcatenateX(fc)

	// Filter context
	case "CALCULATE":
		return e.emitCalculate(fc)
	case "CALCULATETABLE":
		return e.emitCalculateTable(fc)
	case "TREATAS":
		return e.emitTreatas(fc)
	case "FILTER":
		return e.emitFilter(fc)
	case "ALL", "ALLEXCEPT":
		return e.emitAll(fc)
	case "REMOVEFILTERS", "KEEPFILTERS":
		// Intercepted by classifyCalcArgs when used inside CALCULATE; reaching
		// here means the call appeared somewhere it has no meaning.
		return "", fmt.Errorf("%s is only valid as a CALCULATE filter argument", strings.ToUpper(fc.Name))
	case "VALUES":
		return e.emitValuesOrDistinct("VALUES", fc)
	case "DISTINCT":
		return e.emitValuesOrDistinct("DISTINCT", fc)

	// Time intelligence (see timeintel.go). Range-family functions reached
	// here are standalone table expressions; inside CALCULATE they are
	// intercepted by classifyCalcArgs.
	case "DATESYTD", "DATESQTD", "DATESMTD",
		"SAMEPERIODLASTYEAR", "DATEADD",
		"PREVIOUSYEAR", "PREVIOUSQUARTER", "PREVIOUSMONTH", "PREVIOUSDAY",
		"NEXTYEAR", "NEXTQUARTER", "NEXTMONTH", "NEXTDAY",
		"DATESBETWEEN", "DATESINPERIOD":
		return e.emitTimeIntelTable(fc)
	case "TOTALYTD", "TOTALQTD", "TOTALMTD":
		return e.emitTotalPeriod(fc)
	case "LASTNONBLANK":
		err := &semantic.SemanticError{Message: "LASTNONBLANK is currently supported only as a DATESINPERIOD or DATESBETWEEN bound"}
		if len(fc.Args) > 0 && fc.Args[0].Left != nil && fc.Args[0].Left.ColRef != nil {
			err.Line = fc.Args[0].Left.ColRef.Pos.Line
			err.Column = fc.Args[0].Left.ColRef.Pos.Column
		}
		return "", err
	case "CALENDAR":
		return e.emitCalendar(fc)
	case "CALENDARAUTO":
		return e.emitCalendarAuto(fc)
	case "DATE":
		return e.emitDateCtor(fc)

	// Relationship traversal (see related.go)
	case "RELATED":
		return e.emitRelated(fc)
	case "RELATEDTABLE":
		return e.emitRelatedTable(fc)

	// Table constructors / operations
	case "SUMMARIZECOLUMNS":
		return e.emitSummarizeColumns(fc)
	case "ROW":
		return e.emitRow(fc)
	case "ADDCOLUMNS":
		return e.emitProjectColumns(fc, "SELECT *, ")
	case "SELECTCOLUMNS":
		return e.emitProjectColumns(fc, "SELECT ")
	case "UNION":
		return e.emitSetOp("UNION ALL", fc)
	case "INTERSECT":
		return e.emitSetOp("INTERSECT", fc)
	case "EXCEPT":
		return e.emitSetOp("EXCEPT", fc)
	case "TOPN":
		return e.emitTopN(fc)
	case "CROSSJOIN":
		return e.emitCrossJoin(fc)
	case "GENERATE":
		return e.emitGenerate(fc, false)
	case "GENERATEALL":
		return e.emitGenerate(fc, true)

	// Scalar / logical
	case "DIVIDE":
		return e.emitDivide(fc)
	case "ISBLANK":
		return e.emitIsBlank(fc)
	case "BLANK":
		if len(fc.Args) != 0 {
			return "", functionError(fc, "BLANK requires no arguments")
		}
		return "NULL", nil
	case "IF":
		return e.emitIf(fc)
	case "SWITCH":
		return e.emitSwitch(fc)
	case "NOT":
		return e.emitNot(fc)
	case "AND":
		return e.emitAndOr("AND", fc)
	case "OR":
		return e.emitAndOr("OR", fc)

	default:
		// Declaratively mapped scalar functions (see scalarfuncs.go).
		if fn, ok := scalarFuncs[strings.ToUpper(fc.Name)]; ok {
			return e.emitScalarMapped(strings.ToUpper(fc.Name), fn, fc)
		}
		if fn, ok := identityFuncs[strings.ToUpper(fc.Name)]; ok {
			return e.emitScalarMapped(strings.ToUpper(fc.Name), fn, fc)
		}
		return "", functionError(fc, fmt.Sprintf("unknown function %s", strings.ToUpper(fc.Name)))
	}
}

func functionError(fc *parser.FuncCall, message string) error {
	return &semantic.SemanticError{Message: message, Line: fc.Pos.Line, Column: fc.Pos.Column}
}

// ─── Aggregation functions ───────────────────────────────────────────────────

func (e *Emitter) emitSimpleAgg(duckName string, fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 1 {
		return "", fmt.Errorf("%s requires exactly 1 argument", fc.Name)
	}
	if len(e.ctx.rows) > 0 && e.ctx.valueDepth == 0 {
		expr := &parser.Expr{Left: &parser.Term{FuncCall: fc}}
		return e.emitAggregateValue(expr)
	}
	arg, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s(%s)", duckName, arg), nil
}

func (e *Emitter) emitCountRows(fc *parser.FuncCall) (string, error) {
	if len(e.ctx.rows) > 0 && e.ctx.valueDepth == 0 && len(fc.Args) == 1 && bareTableArg(fc.Args[0]) != "" {
		return e.emitAggregateValue(&parser.Expr{Left: &parser.Term{FuncCall: fc}})
	}
	// COUNTROWS(<table function>) → count the computed table in a subquery.
	if len(fc.Args) == 1 && fc.Args[0].Left != nil && fc.Args[0].Left.FuncCall != nil {
		sub, err := e.emitExprAsTable(fc.Args[0])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("(SELECT COUNT(*) FROM (%s) AS %s)", sub, e.nextAlias("__cnt")), nil
	}
	// COUNTROWS(Table) → COUNT(*); the table argument is used in the FROM
	// clause of the enclosing query, not inside COUNT.
	return "COUNT(*)", nil
}

func (e *Emitter) emitDistinctCount(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 1 {
		return "", fmt.Errorf("DISTINCTCOUNT requires exactly 1 argument")
	}
	if len(e.ctx.rows) > 0 && e.ctx.valueDepth == 0 {
		return e.emitAggregateValue(&parser.Expr{Left: &parser.Term{FuncCall: fc}})
	}
	arg, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("CASE WHEN COUNT(*) = 0 THEN NULL ELSE COUNT(DISTINCT %s) + "+
		"CASE WHEN COUNT(*) > COUNT(%s) THEN 1 ELSE 0 END END", arg, arg), nil
}

// emitCountBlank emits COUNTBLANK(T[C]) as COUNT(*) - COUNT(col).
// Since BLANK maps to NULL, this counts all rows where the column is NULL.
func (e *Emitter) emitCountBlank(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 1 {
		return "", fmt.Errorf("COUNTBLANK requires exactly 1 argument")
	}
	if len(e.ctx.rows) > 0 && e.ctx.valueDepth == 0 {
		return e.emitAggregateValue(&parser.Expr{Left: &parser.Term{FuncCall: fc}})
	}
	arg, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("COUNT_IF(%s IS NULL)", arg), nil
}

// ─── Iterator (X) functions ──────────────────────────────────────────────────

// emitIterAgg emits an X-function.
//
// Inside a SUMMARIZECOLUMNS measure context, an iterator over a bare schema
// table participates in the group's filter context: its rows ARE the joined,
// grouped FROM rows, so the aggregate emits inline exactly like SUM/MIN/…
// (a subquery here would scan the whole table and repeat the table-wide value
// in every group cell). Nested table expressions (FILTER(...), VALUES(...))
// and iterators nested inside another iterator's row context keep the scalar
// subquery form:
//
//	SELECT <agg>(__row.<expr>) FROM <table> AS __row
func (e *Emitter) emitIterAgg(agg string, fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 2 {
		return "", fmt.Errorf("%s requires exactly 2 arguments", fc.Name)
	}
	// The iterated expression evaluates per row: an inline aggregate inside it
	// would nest as agg(agg(...)).
	if e.groupedIterInline(fc.Args[0], fc.Args[1]) {
		inner, err := e.emitExpr(fc.Args[1])
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s(%s)", agg, inner), nil
	}

	src, alias, pop, err := e.iterSource(fc.Args[0])
	if err != nil {
		return "", fmt.Errorf("%s: first argument must be a table expression: %w", fc.Name, err)
	}
	defer pop()

	inner, err := e.emitExpr(fc.Args[1])
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"(SELECT %s(%s) FROM %s AS %s)",
		agg, inner, src.sql, alias,
	), nil
}

// groupedIterInline reports whether an iterator's table argument should
// aggregate inline over the enclosing grouped FROM rather than in a scalar
// subquery (see emitIterAgg). True only inside a SUMMARIZECOLUMNS measure
// context, at aggregate top level (no active row context), for a bare table
// known to the schema — VAR temp tables and nested table expressions keep the
// subquery form.
func (e *Emitter) groupedIterInline(arg, value *parser.Expr) bool {
	if e.groupCtx == nil || len(e.ctx.rows) > 0 || e.Schema == nil {
		return false
	}
	name := bareTableArg(arg)
	if name == "" {
		return false
	}
	_, ok := e.Schema.Tables[semantic.ResolveTable(e.Schema, name)]
	return ok && !e.exprNeedsValueQuery(value)
}

func (e *Emitter) exprNeedsValueQuery(expr *parser.Expr) bool {
	needed := false
	walkTerms(expr, func(term *parser.Term) bool {
		if term.ColRef != nil && e.resolveMeasureDef(term.ColRef) != nil {
			needed = true
			return false
		}
		if term.FuncCall != nil {
			name := strings.ToUpper(term.FuncCall.Name)
			if name == "CALCULATE" || emitsInline(term.FuncCall) {
				needed = true
				return false
			}
		}
		return !needed
	})
	return needed
}

// bareTableArg returns the table name when expr is a plain bare table
// reference (Ident, QuotedIdent, or QualifiedIdent), "" otherwise.
func bareTableArg(expr *parser.Expr) string {
	if expr == nil || expr.Left == nil || len(expr.Right) > 0 {
		return ""
	}
	switch t := expr.Left; {
	case t.Ident != "":
		return t.Ident
	case t.QuotedIdent != "":
		return semantic.StripSingleQuotes(t.QuotedIdent)
	case t.QualifiedIdent != "":
		return t.QualifiedIdent
	}
	return ""
}

// iterSource resolves an iterator function's table argument, binding row
// context for the underlying table (when there is one) so column references
// inside the iterated expression resolve to the row alias. The returned pop
// function must be deferred by the caller.
func (e *Emitter) iterSource(arg *parser.Expr) (*tableSource, string, func(), error) {
	src, err := e.tableSourceFromExpr(arg)
	if err != nil {
		return nil, "", nil, err
	}
	alias := e.nextAlias("__row")
	return src, alias, e.pushRow(arg, alias), nil
}

// emitConcatenateX emits CONCATENATEX as string_agg. The same group-context
// inlining as emitIterAgg applies inside SUMMARIZECOLUMNS.
func (e *Emitter) emitConcatenateX(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) < 2 || len(fc.Args) > 3 {
		return "", fmt.Errorf("CONCATENATEX requires 2 or 3 arguments")
	}

	if e.groupedIterInline(fc.Args[0], fc.Args[1]) {
		inner, delim, err := e.concatenateXArgs(fc)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("string_agg(%s, %s)", inner, delim), nil
	}

	src, alias, pop, err := e.iterSource(fc.Args[0])
	if err != nil {
		return "", fmt.Errorf("CONCATENATEX: first argument must be a table expression: %w", err)
	}
	defer pop()

	inner, delim, err := e.concatenateXArgs(fc)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"(SELECT string_agg(%s, %s) FROM %s AS %s)",
		inner, delim, src.sql, alias,
	), nil
}

// concatenateXArgs emits CONCATENATEX's expression and (optional) delimiter
// arguments; the delimiter defaults to ", ".
func (e *Emitter) concatenateXArgs(fc *parser.FuncCall) (inner, delim string, err error) {
	inner, err = e.emitExpr(fc.Args[1])
	if err != nil {
		return "", "", err
	}
	delim = "', '"
	if len(fc.Args) == 3 {
		if delim, err = e.emitExpr(fc.Args[2]); err != nil {
			return "", "", err
		}
	}
	return inner, delim, nil
}

// ─── Filter context functions ────────────────────────────────────────────────

// emitCalculate emits: SELECT <inner> FROM <primary_table> WHERE <filters>.
// When there are no filter arguments the inner expression is returned as-is.
// At the top level this is a complete SQL statement; when used as a scalar
// expression inside ADDCOLUMNS column lists, the caller wraps it in parens.
//
// Inside a SUMMARIZECOLUMNS group context, emission is delegated to
// emitCalculateGrouped (filterctx.go), which understands filter-context
// modifiers (ALL, ALLEXCEPT, REMOVEFILTERS, KEEPFILTERS).
func (e *Emitter) emitCalculate(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) == 0 {
		return "", fmt.Errorf("CALCULATE requires at least 1 argument")
	}
	if len(e.ctx.rows) > 0 && e.ctx.transitionDepth == 0 {
		return e.withContextTransition(func() (string, error) { return e.emitCalculate(fc) })
	}

	if e.groupCtx != nil {
		return e.emitCalculateGrouped(fc)
	}

	// Standalone context: there are no ambient filters, so ALL-family
	// modifiers have nothing to remove — classify to strip them and unwrap
	// KEEPFILTERS / FILTER(ALL(T), pred) into plain predicates.
	cm, err := e.classifyCalcArgs(fc.Args[1:])
	if err != nil {
		return "", err
	}
	preds := append(append([]*parser.Expr{}, cm.preds...), cm.keepPreds...)

	// Emit the inner expression (aggregate or measure reference).
	inner, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}

	// Collect all tables: inner expression establishes the primary table,
	// filter arguments may reference additional tables that need to be joined.
	seen := map[string]bool{}
	var allTables []string
	addTbl := func(t string) {
		tl := strings.ToLower(t)
		if !seen[tl] {
			seen[tl] = true
			allTables = append(allTables, t)
		}
	}
	for _, t := range e.measureExprTables(fc.Args[0]) {
		addTbl(t)
	}
	for _, arg := range preds {
		for _, t := range e.measureExprTablesOutsideRowContext(arg) {
			addTbl(t)
		}
	}
	for _, tf := range cm.timeFilters {
		addTbl(tf.table)
	}

	// Emit filter predicates with their source table so bidirectional bridge
	// chains can be carved into correlated EXISTS predicates.
	var filters []taggedPred
	for _, arg := range preds {
		f, err := e.emitExpr(arg)
		if err != nil {
			return "", err
		}
		tables := e.measureExprTables(arg)
		table := ""
		if len(tables) == 1 {
			table = e.tableKey(tables[0])
		}
		filters = append(filters, taggedPred{sql: f, table: table})
	}
	for _, tf := range cm.timeFilters {
		pred, err := e.emitTimeIntelPred(tf, e.sqlTable(tf.table)+"."+tf.col)
		if err != nil {
			return "", err
		}
		filters = append(filters, taggedPred{sql: pred, table: e.tableKey(tf.table), col: strings.ToLower(tf.col)})
	}

	// If no predicates remain (none given, or only ALL-family modifiers),
	// emit a plain aggregate over the unfiltered tables.
	if len(filters) == 0 {
		if cm.hasRemovals() && len(allTables) > 0 {
			fromClause, fErr := e.calcFromClause(allTables)
			if fErr != nil {
				return "", fErr
			}
			return fmt.Sprintf("(SELECT %s FROM %s)", inner, fromClause), nil
		}
		return inner, nil
	}
	// Build FROM clause, carving pure filter chains after bidirectional edges
	// into EXISTS so bridge multiplicity cannot fan out the value rows.
	if len(allTables) == 0 {
		conds := make([]string, len(filters))
		for i, p := range filters {
			conds[i] = p.sql
		}
		return fmt.Sprintf("(SELECT %s WHERE %s)", inner, strings.Join(conds, " AND ")), nil
	}
	needed := map[string]bool{}
	for _, table := range e.measureValueTables(fc.Args[0]) {
		needed[e.tableKey(table)] = true
	}
	fromClause, conds, _, err := e.stitchedClusterFrom(allTables, needed, filters, nil)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("(SELECT %s FROM %s WHERE %s)", inner, fromClause, strings.Join(conds, " AND ")), nil
}

// calcFromClause builds a FROM clause over allTables, inferring join steps
// through the schema when more than one table is involved.
func (e *Emitter) calcFromClause(allTables []string) (string, error) {
	if len(allTables) == 1 {
		return e.sqlTable(allTables[0]), nil
	}
	if e.Schema == nil {
		parts := make([]string, len(allTables))
		for i, t := range allTables {
			parts[i] = e.sqlTable(t)
		}
		return strings.Join(parts, ", "), nil
	}
	jp, err := semantic.InferJoinPath(e.Schema, allTables)
	if err != nil {
		return "", err
	}
	return e.emitFlatJoins(allTables[0], jp), nil
}

// emitFlatJoins renders a single flat LEFT JOIN tree rooted at primary,
// following the inferred join path in order.
func (e *Emitter) emitFlatJoins(primary string, jp *semantic.JoinPath) string {
	var fbuf strings.Builder
	fbuf.WriteString(e.sqlTable(primary))
	for _, step := range jp.Steps {
		fmt.Fprintf(&fbuf, "\nLEFT JOIN %s ON %s.%s IS NOT DISTINCT FROM %s.%s",
			e.sqlTable(step.Table),
			e.sqlTable(step.FromTable), step.OnFromCol,
			e.sqlTable(step.Table), step.OnToCol,
		)
	}
	return fbuf.String()
}

// emitTreatas emits TREATAS(source, t[col], ...) as a SQL predicate for use
// inside CALCULATE filter arguments.
//
//	Pattern A: TREATAS({"Water","Soft Drinks"}, Product[Category])
//	         → Category IN ('Water', 'Soft Drinks')
//
//	Pattern B: TREATAS(VALUES(Product[ProductKey]), Sales[ProductKey])
//	         → ProductKey IN (SELECT DISTINCT ProductKey FROM Product)
//
//	Pattern C: TREATAS({("SE",2020),("NO",2021)}, sales[country], sales[year])
//	         → ((country = 'SE' AND year = 2020) OR (country = 'NO' AND year = 2021))
//	           (multi-column set membership as OR-of-ANDs — no reliance on
//	           row-value IN; the target columns must resolve on one table so the
//	           predicate routes to a single cluster.)
func (e *Emitter) emitTreatas(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) < 2 {
		return "", fmt.Errorf("TREATAS requires at least 2 arguments")
	}

	// arg[1:]: one or more target column references.
	cols := make([]string, len(fc.Args)-1)
	for i, a := range fc.Args[1:] {
		t := a.Left
		if t == nil || t.ColRef == nil || len(a.Right) > 0 {
			return "", fmt.Errorf("TREATAS: target arguments must be column references (e.g. Product[Category])")
		}
		col, err := e.emitColRef(t.ColRef)
		if err != nil {
			return "", err
		}
		cols[i] = col
	}

	// arg[0]: source — TableConstructor or VALUES(t[c]).
	srcTerm := fc.Args[0].Left
	if srcTerm == nil {
		return "", fmt.Errorf("TREATAS: first argument must be a table constructor {...} or VALUES(...)")
	}
	switch {
	case srcTerm.TableConstructor != nil:
		rows := srcTerm.TableConstructor.Rows
		if len(rows) == 0 {
			return "", fmt.Errorf("TREATAS: value set must not be empty")
		}
		// Single column, single value per row → the plain IN form (Pattern A).
		if len(cols) == 1 {
			vals := make([]string, len(rows))
			for i, row := range rows {
				if len(row.Values) != 1 {
					return "", fmt.Errorf("TREATAS: single-column set must not contain tuples")
				}
				s, err := e.emitExpr(row.Values[0])
				if err != nil {
					return "", err
				}
				vals[i] = s
			}
			return fmt.Sprintf("%s IN (%s)", cols[0], strings.Join(vals, ", ")), nil
		}
		// Multi-column set → OR-of-ANDs (Pattern C).
		ors := make([]string, len(rows))
		for i, row := range rows {
			if len(row.Values) != len(cols) {
				return "", fmt.Errorf("TREATAS: each tuple must have %d value(s) to match the target columns", len(cols))
			}
			ands := make([]string, len(cols))
			for j, v := range row.Values {
				s, err := e.emitExpr(v)
				if err != nil {
					return "", err
				}
				ands[j] = fmt.Sprintf("%s = %s", cols[j], s)
			}
			ors[i] = "(" + strings.Join(ands, " AND ") + ")"
		}
		return "(" + strings.Join(ors, " OR ") + ")", nil

	case srcTerm.FuncCall != nil && strings.ToUpper(srcTerm.FuncCall.Name) == "VALUES":
		// Pattern B: VALUES(t[c])
		if len(cols) != 1 {
			return "", fmt.Errorf("TREATAS: VALUES source supports a single target column")
		}
		if len(srcTerm.FuncCall.Args) != 1 {
			return "", fmt.Errorf("TREATAS: VALUES requires exactly 1 argument")
		}
		vcr := srcTerm.FuncCall.Args[0].Left
		if vcr == nil || vcr.ColRef == nil {
			return "", fmt.Errorf("TREATAS: VALUES argument must be a column reference")
		}
		srcCol, err := e.emitColRef(vcr.ColRef)
		if err != nil {
			return "", err
		}
		srcTable := e.sqlTable(semantic.StripSingleQuotes(vcr.ColRef.Table))
		return fmt.Sprintf("%s IN (SELECT DISTINCT %s FROM %s)", cols[0], srcCol, srcTable), nil

	default:
		return "", fmt.Errorf("TREATAS: first argument must be a table constructor {...} or VALUES(...)")
	}
}

// emitFilter emits FILTER(Table, predicate) as a subquery. The table argument
// may itself be a table expression (e.g. FILTER(SUMMARIZECOLUMNS(...), ...)).
func (e *Emitter) emitFilter(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 2 {
		return "", fmt.Errorf("FILTER requires exactly 2 arguments")
	}
	src, err := e.tableSourceFromExpr(fc.Args[0])
	if err != nil {
		return "", fmt.Errorf("FILTER: first argument must be a table expression: %w", err)
	}
	from := src.sql
	alias := e.nextAlias("__row")
	var pop func()
	if src.nested {
		from += " AS " + alias
		bindings := map[string]string{}
		if src.name != "" {
			bindings[e.tableKey(src.name)] = alias
		} else {
			// A computed table exposes output columns, not its base table aliases.
			// Bind qualified predicate references to the computed source alias.
			for _, cr := range collectColRefs(fc.Args[1]) {
				if cr.Table != "" {
					bindings[e.tableKey(semantic.StripSingleQuotes(cr.Table))] = alias
				}
			}
		}
		if len(bindings) > 0 {
			pop = e.pushSQLBindings(bindings)
		}
	}
	if !src.nested {
		from += " AS " + alias
	}
	if pop != nil {
		defer pop()
	}
	popRow := e.pushRow(fc.Args[0], alias)
	defer popRow()
	pred, err := e.emitExpr(fc.Args[1])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("(SELECT * FROM %s WHERE %s)", from, pred), nil
}

// emitAll emits ALL / ALLEXCEPT as a table expression. When used as a
// CALCULATE filter argument, these calls are intercepted by classifyCalcArgs
// (filterctx.go) and never reach this function.
//
//	ALL(T)             → SELECT * FROM t              (T without filters)
//	ALL(T[C], T[D]...) → SELECT DISTINCT c, d FROM t  (distinct combinations)
//	ALLEXCEPT(T, ...)  → SELECT * FROM t              (no ambient filters to keep)
func (e *Emitter) emitAll(fc *parser.FuncCall) (string, error) {
	name := strings.ToUpper(fc.Name)
	if len(fc.Args) == 0 {
		return "", fmt.Errorf("%s() with no arguments is only valid inside CALCULATE", name)
	}

	// Bare table form (first argument is not a column reference).
	if t := fc.Args[0].Left; t != nil && t.ColRef == nil {
		table, err := e.tableNameFromExpr(fc.Args[0])
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		return fmt.Sprintf("SELECT * FROM %s", e.sqlTable(table)), nil
	}

	// Column form: one or more column references from the same table.
	var table string
	var cols []string
	for _, a := range fc.Args {
		t := a.Left
		if t == nil || t.ColRef == nil || len(a.Right) > 0 {
			return "", fmt.Errorf("%s: arguments must all be column references", name)
		}
		tbl := semantic.StripSingleQuotes(t.ColRef.Table)
		if tbl == "" {
			return "", fmt.Errorf("%s: column reference requires a table qualifier", name)
		}
		switch {
		case table == "":
			table = tbl
		case e.tableKey(tbl) != e.tableKey(table):
			return "", fmt.Errorf("%s: all column references must belong to the same table", name)
		}
		cols = append(cols, e.resolveColName(tbl, semantic.StripBrackets(t.ColRef.Column)))
	}
	return fmt.Sprintf("(SELECT DISTINCT %s FROM %s)", strings.Join(cols, ", "), e.sqlTable(table)), nil
}

func (e *Emitter) emitValuesOrDistinct(name string, fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 1 {
		return "", fmt.Errorf("%s requires exactly 1 argument", name)
	}
	// The argument is a column reference: extract the table it belongs to so we
	// can emit a valid FROM clause.
	col, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	table := e.primaryTableFromExpr(fc.Args[0])
	if table == "" {
		return "", fmt.Errorf("%s: cannot determine source table from argument", name)
	}
	return fmt.Sprintf("(SELECT DISTINCT %s FROM %s)", col, e.sqlTable(table)), nil
}

// ─── Table functions ─────────────────────────────────────────────────────────

// emitSummarizeColumns emits a GROUP BY query:
//
//	SELECT group_col_1, ..., <expr1> AS 'Name1', ...
//	FROM   <tables inferred from group columns>
//	GROUP BY group_col_1, ...
//
// Arguments are: [ColRef...] ["Name", Expr]...
// Leading non-string arguments are group columns; once a string literal is
// encountered all remaining arguments are treated as name/expr pairs.
func (e *Emitter) emitSummarizeColumns(fc *parser.FuncCall) (string, error) {
	outerContext := e.groupCtx
	outerScopes := e.sqlScopes
	e.sqlScopes = nil
	defer func() { e.sqlScopes = outerScopes }()
	if len(e.ctx.rows) > 0 {
		e.ctx.valueDepth++
		defer func() { e.ctx.valueDepth-- }()
	}
	if len(fc.Args) < 1 {
		return "", fmt.Errorf("SUMMARIZECOLUMNS requires at least 1 argument")
	}

	// Partition arguments: group columns precede the first string literal.
	split := len(fc.Args)
	for i, arg := range fc.Args {
		if isStringLiteral(arg) {
			split = i
			break
		}
	}
	groupArgs := fc.Args[:split]
	pairArgs := fc.Args[split:]

	if len(pairArgs)%2 != 0 {
		return "", fmt.Errorf(
			"SUMMARIZECOLUMNS: expected name/expression pairs after the group columns, "+
				"but got %d argument(s) — each measure must have a quoted name followed by its expression",
			len(pairArgs))
	}

	// Emit group column SQL expressions. Arguments that resolve to measures are
	// treated as unnamed aggregate outputs (SELECT only) rather than grouping
	// keys — placing an aggregate in GROUP BY is invalid SQL.
	// TREATAS calls are separated into WHERE predicates, paired with their
	// target table/column for bidi CTE routing and CALCULATE modifier checks.
	var groupCols []string          // emitted SQL for true group-by keys
	var plainKeys []groupKey        // table+column of plain ColRef group keys
	var rollupKeys []groupKey       // table+column of rollup columns (see rollup.go)
	var rollupElems []rollupElement // ROLLUPADDISSUBTOTAL units (see rollup.go)
	var measureArgs []*parser.Expr  // measure refs in the group position (emitted later)
	var wherePreds []taggedPred     // TREATAS filter predicates with their source tables
	for _, arg := range groupArgs {
		// ROLLUPADDISSUBTOTAL adds subtotal grouping sets; a bare ROLLUPGROUP
		// outside it has no meaning.
		if arg.Left != nil && arg.Left.FuncCall != nil && len(arg.Right) == 0 {
			switch strings.ToUpper(arg.Left.FuncCall.Name) {
			case "ROLLUPADDISSUBTOTAL":
				elems, err := e.parseRollup(arg.Left.FuncCall, &rollupKeys)
				if err != nil {
					return "", err
				}
				rollupElems = append(rollupElems, elems...)
				continue
			case "ROLLUPGROUP":
				return "", fmt.Errorf("ROLLUPGROUP is only valid inside ROLLUPADDISSUBTOTAL")
			}
		}
		// Check for a TREATAS call — emit as a WHERE predicate, not a group column.
		if arg.Left != nil && arg.Left.FuncCall != nil &&
			strings.ToUpper(arg.Left.FuncCall.Name) == "TREATAS" && len(arg.Right) == 0 {
			treatasFC := arg.Left.FuncCall
			// Extract the target table and column for routing. A multi-column
			// TREATAS tags on its first target column (all target columns
			// resolve on one table, so one tag routes the whole predicate).
			var predTable, predCol string
			if len(treatasFC.Args) >= 2 && treatasFC.Args[1].Left != nil && treatasFC.Args[1].Left.ColRef != nil {
				cr := treatasFC.Args[1].Left.ColRef
				tbl := semantic.StripSingleQuotes(cr.Table)
				predTable = e.tableKey(tbl)
				predCol = strings.ToLower(e.resolveColName(tbl, semantic.StripBrackets(cr.Column)))
			}
			pred, err := e.emitTreatas(treatasFC)
			if err != nil {
				return "", err
			}
			wherePreds = append(wherePreds, taggedPred{table: predTable, col: predCol, sql: pred, expr: arg})
			continue
		}
		// FILTER(Table, pred) in the group position is a filter argument:
		// the predicate restricts the filter context, like TREATAS but for
		// arbitrary comparisons (ranges, inequality, string search).
		if arg.Left != nil && arg.Left.FuncCall != nil &&
			strings.ToUpper(arg.Left.FuncCall.Name) == "FILTER" && len(arg.Right) == 0 {
			filterFC := arg.Left.FuncCall
			if len(filterFC.Args) != 2 {
				return "", fmt.Errorf("FILTER requires exactly 2 arguments")
			}
			tbl := bareTableArg(filterFC.Args[0])
			if tbl == "" {
				return "", fmt.Errorf("SUMMARIZECOLUMNS: a FILTER argument must name a bare table (e.g. FILTER(Sales, Sales[qty] > 1))")
			}
			pred, err := e.emitExpr(filterFC.Args[1])
			if err != nil {
				return "", err
			}
			predTable := e.tableKey(semantic.StripSingleQuotes(tbl))
			// The first predicate column on the filtered table identifies the
			// filter for CALCULATE modifier checks (ALL(t[c]) removal).
			var predCol string
			for _, cr := range collectColRefs(filterFC.Args[1]) {
				crTable := semantic.StripSingleQuotes(cr.Table)
				if e.tableKey(crTable) == predTable {
					predCol = strings.ToLower(e.resolveColName(crTable, semantic.StripBrackets(cr.Column)))
					break
				}
			}
			wherePreds = append(wherePreds, taggedPred{table: predTable, col: predCol, sql: pred, expr: filterFC.Args[1]})
			continue
		}
		// Any other table-valued argument would be emitted into the SELECT
		// list as a group column, producing invalid SQL. TREATAS and FILTER
		// (handled above) are the filter-argument forms DUX supports.
		if arg.Left != nil && arg.Left.FuncCall != nil && len(arg.Right) == 0 &&
			isTableFunc(arg.Left.FuncCall.Name) {
			return "", fmt.Errorf(
				"SUMMARIZECOLUMNS: %s(...) returns a table; a filter argument must be "+
					"TREATAS(values, table[column]) or FILTER(table, predicate)",
				strings.ToUpper(arg.Left.FuncCall.Name))
		}
		if e.isMeasureColRef(arg) {
			measureArgs = append(measureArgs, arg)
		} else {
			gc, err := e.emitExpr(arg)
			if err != nil {
				return "", err
			}
			groupCols = append(groupCols, gc)
			// Plain column refs become group keys that CALCULATE modifiers
			// can remove; computed group expressions are not removable.
			if arg.Left != nil && arg.Left.ColRef != nil && arg.Left.ColRef.Table != "" && len(arg.Right) == 0 {
				tbl := semantic.StripSingleQuotes(arg.Left.ColRef.Table)
				col := e.resolveColName(tbl, semantic.StripBrackets(arg.Left.ColRef.Column))
				plainKeys = append(plainKeys, groupKey{
					table:  tbl,
					col:    col,
					expr:   e.sqlTable(tbl) + "." + col,
					line:   arg.Left.ColRef.Pos.Line,
					column: arg.Left.ColRef.Pos.Column,
				})
			}
		}
	}
	if outerContext != nil {
		for _, key := range outerContext.keys {
			if key.frameID == 0 {
				continue
			}
			wherePreds = append(wherePreds, taggedPred{
				table: e.tableKey(key.table), col: strings.ToLower(key.col),
				sql: fmt.Sprintf("%s.%s IS NOT DISTINCT FROM %s", e.sqlTable(key.table), key.col, key.expr),
			})
		}
		wherePreds = append(wherePreds, outerContext.preds...)
	}
	groupKeys := append(append([]groupKey{}, plainKeys...), rollupKeys...)
	// A group-only SUMMARIZECOLUMNS has no measure context for filters on
	// an unrelated table to affect. This is also the shape used to populate an
	// untrimmed slicer. Related filters still cascade to the group table.
	if len(pairArgs) == 0 && len(measureArgs) == 0 {
		groupTables := make([]string, len(groupKeys))
		for i, key := range groupKeys {
			groupTables[i] = key.table
		}
		if e.Schema != nil && len(groupTables) > 0 {
			kept := wherePreds[:0]
			for _, pred := range wherePreds {
				if semantic.FilterReaches(e.Schema, pred.table, groupTables) {
					kept = append(kept, pred)
				}
			}
			wherePreds = kept
		}
	}

	// Establish the group context so CALCULATE calls inside measure
	// expressions (direct, nested, or via measure expansion) can resolve
	// filter-context modifiers against the group-by keys.
	prevCtx := e.groupCtx
	e.groupCtx = &groupContext{keys: groupKeys, preds: wherePreds}
	defer func() { e.groupCtx = prevCtx }()

	// Stitched codegen (stitched.go) applies when:
	//  - measures span more than one table cluster: a single flat join tree
	//    would fan the clusters out against each other and inflate every
	//    aggregate;
	//  - a measure modifies the group filter context (CALCULATE removals,
	//    time intelligence): it evaluates in a private context CTE
	//    (contextcte.go) instead of a correlated scalar subquery; or
	//  - the join graph crosses a bidirectional relationship: filter chains
	//    through the bidi edge must gate via EXISTS semi-joins, per measure
	//    context, to avoid many-to-many bridge fan-out.
	// Grouping sets and computed group keys keep the correlated fallback for
	// context-modifying measures (liftContext false).
	liftContext := len(rollupElems) == 0 && len(groupCols) == len(plainKeys)
	plan := e.planMeasures(pairArgs, measureArgs, liftContext)
	for _, cluster := range plan.clusters {
		if err := e.validateGroupKeys(groupKeys, cluster.tables); err != nil {
			return "", err
		}
	}
	if tableClusterCount(plan.clusters) > 1 || plan.hasContextClusters() || e.stitchForBidi(plan, groupKeys, wherePreds) {
		return e.emitStitched(groupCols, plainKeys, rollupElems, rollupKeys, pairArgs, measureArgs, plan, wherePreds)
	}

	// Emit measure refs that appeared in the group position.
	var inlineMeasures []string
	for _, arg := range measureArgs {
		sql, err := e.emitExpr(arg)
		if err != nil {
			return "", err
		}
		inlineMeasures = append(inlineMeasures, sql)
	}

	// Emit name/expr measure pairs. CALCULATE emission is group-context aware:
	// plain predicates use the SQL aggregate FILTER syntax so they respect the
	// outer GROUP BY; filter-context modifiers produce correlated subqueries.
	var measures []string
	for i := 0; i < len(pairArgs); i += 2 {
		nameSQL, err := e.emitExpr(pairArgs[i])
		if err != nil {
			return "", err
		}
		valSQL, err := e.emitExpr(pairArgs[i+1])
		if err != nil {
			return "", err
		}
		measures = append(measures, fmt.Sprintf("%s AS %s", valSQL, nameSQL))
	}

	// Collect value tables first so the fact table drives join inference; shared
	// dimensions can otherwise offer equal paths through unrelated fact tables.
	seen := map[string]bool{}
	var allTables []string
	addTbl := func(t string) {
		tl := strings.ToLower(t)
		if !seen[tl] {
			seen[tl] = true
			allTables = append(allTables, t)
		}
	}
	for i := 1; i < len(pairArgs); i += 2 {
		for _, t := range e.measureExprTables(pairArgs[i]) {
			addTbl(t)
		}
	}
	for _, arg := range groupArgs {
		if arg.Left != nil && arg.Left.FuncCall != nil && len(arg.Right) == 0 {
			name := strings.ToUpper(arg.Left.FuncCall.Name)
			if name == "TREATAS" || name == "FILTER" {
				continue
			}
		}
		for _, t := range e.measureExprTables(arg) {
			addTbl(t)
		}
	}
	for _, pred := range wherePreds {
		addTbl(pred.table)
	}

	// Build FROM clause, using join inference when multiple tables are present.
	// Bidirectional relationships never reach this path — stitchForBidi routes
	// them through stitched codegen above.
	var fromClause string
	var outerPreds []string
	for _, p := range wherePreds {
		outerPreds = append(outerPreds, p.sql)
	}
	if len(allTables) > 0 {
		var err error
		if fromClause, err = e.calcFromClause(allTables); err != nil {
			return "", err
		}
	}

	// Build SELECT list.
	var selects []string
	selects = append(selects, groupCols...)
	selects = append(selects, rollupSelectItems(rollupElems)...)
	selects = append(selects, inlineMeasures...)
	selects = append(selects, measures...)

	var sb strings.Builder
	fmt.Fprintf(&sb, "SELECT %s", strings.Join(selects, ", "))
	if fromClause != "" {
		fmt.Fprintf(&sb, "\nFROM %s", fromClause)
	}
	if len(outerPreds) > 0 {
		fmt.Fprintf(&sb, "\nWHERE %s", strings.Join(outerPreds, " AND "))
	}
	if len(rollupElems) > 0 {
		fmt.Fprintf(&sb, "\nGROUP BY %s", rollupGroupingSets(groupCols, rollupElems))
	} else if len(groupCols) > 0 {
		fmt.Fprintf(&sb, "\nGROUP BY %s", strings.Join(groupCols, ", "))
	}
	return blankPrune(sb.String(), pairArgs, measureArgs), nil
}

// emitRow emits ROW("Name", Expr, ...), the single-row table constructor.
//
// A ROW is a SUMMARIZECOLUMNS with no group columns: the same name/expression
// pairs, evaluated over the same inferred join tree, producing exactly one row.
// Emission is delegated so that measure expansion, table collection, join
// inference and measure clustering behave identically in both.
func (e *Emitter) emitRow(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) < 2 || len(fc.Args)%2 != 0 {
		return "", fmt.Errorf(
			"ROW: expected name/expression pairs, but got %d argument(s) — each column must have a quoted name followed by its expression",
			len(fc.Args))
	}
	for i := 0; i < len(fc.Args); i += 2 {
		if !isStringLiteral(fc.Args[i]) {
			return "", fmt.Errorf("ROW: column name at argument %d must be a quoted string", i+1)
		}
	}
	return e.emitSummarizeColumns(fc)
}

// emitProjectColumns emits ADDCOLUMNS / SELECTCOLUMNS:
//
//	<selectPrefix>(expr1) AS "name1", ... FROM table
//
// selectPrefix is "SELECT *, " for ADDCOLUMNS and "SELECT " for SELECTCOLUMNS.
// Column references inside the expression arguments are emitted without table
// qualifiers because the source table is the sole table in the FROM clause.
func (e *Emitter) emitProjectColumns(fc *parser.FuncCall, selectPrefix string) (string, error) {
	name := strings.ToUpper(fc.Name)
	if len(fc.Args) < 3 || (len(fc.Args)-1)%2 != 0 {
		return "", fmt.Errorf("%s requires a table then name/expr pairs", name)
	}
	src, err := e.tableSourceFromExpr(fc.Args[0])
	if err != nil {
		return "", fmt.Errorf("%s: first argument must be a table expression: %w", name, err)
	}

	alias := e.nextAlias("__row")
	popRow := e.pushRow(fc.Args[0], alias)
	defer popRow()

	var cols []string
	for i := 1; i < len(fc.Args); i += 2 {
		nameExpr, err := e.emitExpr(fc.Args[i])
		if err != nil {
			return "", err
		}
		valExpr, err := e.emitExpr(fc.Args[i+1])
		if err != nil {
			return "", err
		}
		cols = append(cols, fmt.Sprintf("(%s) AS %s", valExpr, nameExpr))
	}

	return fmt.Sprintf("%s%s FROM %s AS %s", selectPrefix, strings.Join(cols, ", "), src.sql, alias), nil
}

func (e *Emitter) emitSetOp(op string, fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 2 {
		return "", fmt.Errorf("%s requires exactly 2 arguments", op)
	}
	left, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	right, err := e.emitExpr(fc.Args[1])
	if err != nil {
		return "", err
	}
	if op == "UNION ALL" {
		return fmt.Sprintf("SELECT * FROM ((%s)\n%s\n(%s))", left, op, right), nil
	}
	left, err = normaliseToSelect(left)
	if err != nil {
		return "", fmt.Errorf("%s: first argument must be a table expression: %w", op, err)
	}
	right, err = normaliseToSelect(right)
	if err != nil {
		return "", fmt.Errorf("%s: second argument must be a table expression: %w", op, err)
	}
	exists := "EXISTS"
	if op == "EXCEPT" {
		exists = "NOT EXISTS"
	}
	return fmt.Sprintf("SELECT __set_l.* FROM (%s) AS __set_l WHERE %s "+
		"(SELECT 1 FROM (%s) AS __set_r WHERE __set_l IS NOT DISTINCT FROM __set_r)",
		left, exists, right), nil
}

// emitTopN uses RANK so ties at the Nth row are retained as DAX requires.
func (e *Emitter) emitTopN(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) < 3 {
		return "", fmt.Errorf("TOPN requires at least 3 arguments (n, table, expr)")
	}
	n, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	src, err := e.tableSourceFromExpr(fc.Args[1])
	if err != nil {
		return "", fmt.Errorf("TOPN: second argument must be a table expression: %w", err)
	}
	alias := e.nextAlias("__top")
	popRow := e.pushRow(fc.Args[1], alias)
	defer popRow()
	var orders []string
	for i := 2; i < len(fc.Args); i++ {
		var orderExpr string
		if src.nested {
			orderExpr, err = e.emitOrderKey(fc.Args[i])
		} else {
			orderExpr, err = e.emitExpr(fc.Args[i])
		}
		if err != nil {
			return "", err
		}
		dir := "DESC"
		if i+1 < len(fc.Args) && isOrderDirection(fc.Args[i+1]) {
			dir = strings.ToUpper(fc.Args[i+1].Left.Ident)
			i++
		}
		orders = append(orders, orderExpr+" "+dir)
	}
	return fmt.Sprintf("SELECT * FROM %s AS %s QUALIFY RANK() OVER (ORDER BY %s) <= %s",
		src.sql, alias, strings.Join(orders, ", "), n), nil
}

func isOrderDirection(expr *parser.Expr) bool {
	return expr != nil && expr.Left != nil && len(expr.Right) == 0 &&
		(strings.EqualFold(expr.Left.Ident, "ASC") || strings.EqualFold(expr.Left.Ident, "DESC"))
}

// ─── Scalar / logical functions ──────────────────────────────────────────────

// emitDivide emits null-safe division:
//
//	CASE WHEN (b) = 0 THEN NULL ELSE (a) / (b) END
func (e *Emitter) emitDivide(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) < 2 {
		return "", fmt.Errorf("DIVIDE requires at least 2 arguments")
	}
	a, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	b, err := e.emitExpr(fc.Args[1])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("CASE WHEN (%s) = 0 THEN NULL ELSE (%s) / (%s) END", b, a, b), nil
}

// emitIsBlank emits: (expr) IS NULL
func (e *Emitter) emitIsBlank(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 1 {
		return "", fmt.Errorf("ISBLANK requires exactly 1 argument")
	}
	arg, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("(%s) IS NULL", arg), nil
}

// emitIf emits a CASE WHEN expression.
func (e *Emitter) emitIf(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) < 2 || len(fc.Args) > 3 {
		return "", fmt.Errorf("IF requires 2 or 3 arguments")
	}
	cond, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	then, err := e.emitExpr(fc.Args[1])
	if err != nil {
		return "", err
	}
	if len(fc.Args) == 2 {
		return fmt.Sprintf("CASE WHEN %s THEN %s END", cond, then), nil
	}
	els, err := e.emitExpr(fc.Args[2])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("CASE WHEN %s THEN %s ELSE %s END", cond, then, els), nil
}

// emitSwitch emits SWITCH(expr, val1, res1, val2, res2, ...[, else]) as CASE WHEN.
func (e *Emitter) emitSwitch(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) < 3 {
		return "", fmt.Errorf("SWITCH requires at least 3 arguments")
	}
	subject, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("CASE ")
	sb.WriteString(subject)

	i := 1
	for i+1 < len(fc.Args) {
		val, err := e.emitExpr(fc.Args[i])
		if err != nil {
			return "", err
		}
		res, err := e.emitExpr(fc.Args[i+1])
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&sb, " WHEN %s THEN %s", val, res)
		i += 2
	}
	if i < len(fc.Args) {
		// Odd argument — trailing ELSE clause.
		els, err := e.emitExpr(fc.Args[i])
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&sb, " ELSE %s", els)
	}
	sb.WriteString(" END")
	return sb.String(), nil
}

func (e *Emitter) emitAndOr(op string, fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 2 {
		return "", fmt.Errorf("%s requires exactly 2 arguments", op)
	}
	left, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	right, err := e.emitExpr(fc.Args[1])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("(%s) %s (%s)", left, op, right), nil
}

func (e *Emitter) emitNot(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 1 {
		return "", fmt.Errorf("NOT requires exactly 1 argument")
	}
	arg, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("NOT (%s)", arg), nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// sqlIdent returns a DuckDB-safe SQL identifier for a bare (unquoted) table
// name. Names that contain spaces are wrapped in double quotes; simple
// all-lowercase names are returned as-is in lowercase.
// For db-qualified names (db.table) use sqlQualifiedIdent instead.
func sqlIdent(name string) string {
	if strings.Contains(name, " ") {
		// Escape any embedded double-quotes and wrap in double-quotes.
		return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	}
	// Qualified names (db.table) are passed through without lowercasing the dot.
	if strings.Contains(name, ".") {
		return sqlQualifiedIdent(name)
	}
	return strings.ToLower(name)
}

// sqlTable maps the public DUX table key to its physical DuckDB name. Unit
// schemas and transient tables have no SQLName and keep the historical form.
func (e *Emitter) sqlTable(name string) string {
	if e != nil && e.Schema != nil {
		if table, _ := e.Schema.FindTable(name); table != nil && table.SQLName != "" {
			return sqlQualifiedIdent(table.SQLName)
		}
	}
	return sqlQualifiedIdent(name)
}

// sqlQualifiedIdent returns a DuckDB-safe SQL identifier for a qualified
// table name with any number of segments (e.g. "analytics.Sales" → analytics.Sales,
// "analytics.sales.Customer" → analytics.sales.customer, "my db.my table" → "my db"."my table").
func sqlQualifiedIdent(name string) string {
	parts := strings.Split(name, ".")
	if len(parts) < 2 {
		return strings.ToLower(name)
	}
	for i, p := range parts {
		parts[i] = sqlIdent(p)
	}
	return strings.Join(parts, ".")
}

// isStringLiteral reports whether expr is a bare string literal node.
func isStringLiteral(expr *parser.Expr) bool {
	if expr == nil || expr.Left == nil {
		return false
	}
	return expr.Left.Literal != nil && expr.Left.Literal.String != nil
}

// blankPrune applies SUMMARIZECOLUMNS' rule that rows where every expression
// is BLANK are omitted. Grouping and subtotal columns do not define a row.
func blankPrune(sql string, pairArgs, inlineArgs []*parser.Expr) string {
	var aliases []string
	for i := 0; i+1 < len(pairArgs); i += 2 {
		raw := *pairArgs[i].Left.Literal.String
		aliases = append(aliases, raw[1:len(raw)-1])
	}
	for i, arg := range inlineArgs {
		aliases = append(aliases, inlineMeasureAlias(arg, i))
	}
	if len(aliases) == 0 {
		return sql
	}
	conds := make([]string, len(aliases))
	for i, alias := range aliases {
		conds[i] = "__sc." + quoteIdent(alias) + " IS NOT NULL"
	}
	return "SELECT * FROM (\n" + sql + "\n) AS __sc\nWHERE " + strings.Join(conds, " OR ")
}

// isMeasureColRef reports whether expr is a single ColRef that resolves to a
// measure in the effective measure store. Used by emitSummarizeColumns to
// avoid placing aggregate expressions in the GROUP BY clause.
func (e *Emitter) isMeasureColRef(expr *parser.Expr) bool {
	if expr == nil || expr.Left == nil || len(expr.Right) > 0 {
		return false
	}
	cr := expr.Left.ColRef
	if cr == nil {
		return false
	}
	measures := e.effectiveMeasures()
	if measures == nil {
		return false
	}
	name := semantic.StripBrackets(cr.Column)
	tableKey := semantic.StripSingleQuotes(cr.Table)
	if tableKey != "" {
		return semantic.FindMeasure(tableKey, name, measures) != nil
	}
	// Bare [Name] — check across all tables.
	def, err := semantic.FindMeasureByName(name, measures)
	return err == nil && def != nil
}

// walkTerms visits every Term within expr in document order, descending into
// function-call arguments and parenthesised sub-expressions. The visit
// callback returns false to skip descending into the current term's children.
func walkTerms(expr *parser.Expr, visit func(*parser.Term) bool) {
	if expr == nil {
		return
	}
	terms := append([]*parser.Term{expr.Left}, make([]*parser.Term, 0, len(expr.Right))...)
	for _, op := range expr.Right {
		terms = append(terms, op.Right)
	}
	for _, t := range terms {
		if t == nil || !visit(t) {
			continue
		}
		if t.FuncCall != nil {
			for _, arg := range t.FuncCall.Args {
				walkTerms(arg, visit)
			}
		}
		if t.SubExpr != nil {
			walkTerms(t.SubExpr, visit)
		}
	}
}

// emitsInline reports whether an aggregate call emits inline into the
// enclosing SELECT — relying on the caller's FROM and GROUP BY — rather than as
// a self-contained subquery. CALCULATE, the iterators and the time-intelligence
// totals carry their own context and are never inline.
func emitsInline(fc *parser.FuncCall) bool {
	name := strings.ToUpper(fc.Name)
	if _, ok := simpleAggs[name]; ok {
		return true
	}
	switch name {
	case "DISTINCTCOUNT", "COUNTBLANK":
		return true
	case "COUNTROWS":
		// COUNTROWS over a table expression — RELATEDTABLE(...), FILTER(...) —
		// counts that table in its own subquery (see emitCountRows); only the
		// bare-table form emits COUNT(*) against the caller's FROM.
		return len(fc.Args) != 1 || fc.Args[0].Left == nil || fc.Args[0].Left.FuncCall == nil
	}
	return false
}

// iterBareTable returns the bare-table first argument of an iterator
// aggregate call, or "" when the call is not an iterator or its source is a
// nested table expression.
func iterBareTable(fc *parser.FuncCall) string {
	name := strings.ToUpper(fc.Name)
	if _, ok := iterAggs[name]; !ok && name != "CONCATENATEX" {
		return ""
	}
	if len(fc.Args) == 0 {
		return ""
	}
	return bareTableArg(fc.Args[0])
}

// tableNameFromExpr extracts a bare table name from the first argument of a
// table function. It handles the common case where the argument is a bare Ident
// (table name), a Term.Ident, or a table-prefix ColRef.
func (e *Emitter) tableNameFromExpr(expr *parser.Expr) (string, error) {
	if expr == nil || expr.Left == nil {
		return "", fmt.Errorf("expected table name")
	}
	t := expr.Left
	switch {
	case t.Ident != "":
		return t.Ident, nil
	case t.QuotedIdent != "":
		return semantic.StripSingleQuotes(t.QuotedIdent), nil
	case t.QualifiedIdent != "":
		return t.QualifiedIdent, nil
	case t.ColRef != nil && t.ColRef.Table != "":
		return t.ColRef.Table, nil
	case t.FuncCall != nil:
		// ALL(T) / ALLEXCEPT(T, ...) over a bare table is just the unfiltered
		// table — unwrap to the table name (there is no ambient filter context
		// at the table-expression level).
		name := strings.ToUpper(t.FuncCall.Name)
		if (name == "ALL" || name == "ALLEXCEPT") && len(t.FuncCall.Args) >= 1 {
			if inner := t.FuncCall.Args[0].Left; inner != nil && inner.ColRef == nil {
				return e.tableNameFromExpr(t.FuncCall.Args[0])
			}
		}
		// A nested table function has no single name — callers that can accept
		// a subquery use tableSourceFromExpr (tables.go) instead.
		return "", fmt.Errorf("expected a table name, got a table expression")
	}
	return "", fmt.Errorf("cannot determine table name from expression")
}

// emitExprAsTable converts an expression into a standalone SELECT statement
// suitable for use in CREATE TEMP TABLE … AS <sql>. The input is already
// emitted SQL; we normalise the three shapes the emitter can produce:
//
//  1. Already a SELECT statement → return as-is.
//  2. Wrapped in a single matching outer pair of parens → strip them.
//  3. Bare identifier (table name with no spaces) → wrap in SELECT * FROM.
//
// Scalar expressions (no SELECT, no parens wrapping a subquery) are rejected
// so the caller gets a clear error rather than invalid SQL.
func (e *Emitter) emitExprAsTable(expr *parser.Expr) (string, error) {
	sql, err := e.emitExpr(expr)
	if err != nil {
		return "", err
	}
	return normaliseToSelect(sql)
}

// normaliseToSelect converts an emitted SQL fragment to a full SELECT statement.
func normaliseToSelect(sql string) (string, error) {
	s := strings.TrimSpace(sql)

	upper := strings.ToUpper(s)
	if strings.HasPrefix(upper, "SELECT") {
		return s, nil
	}
	// CTE-shaped queries (stitched multi-table, bidi) are complete statements;
	// DuckDB accepts WITH inside a parenthesised subquery.
	if strings.HasPrefix(upper, "WITH") {
		return s, nil
	}

	if hasMatchingOuterParens(s) {
		inner := strings.TrimSpace(s[1 : len(s)-1])
		if strings.HasPrefix(strings.ToUpper(inner), "SELECT") {
			return inner, nil
		}
	}

	// Bare table name: no spaces, no operators — treat as SELECT * FROM <name>.
	if !strings.ContainsAny(s, " \t\n()") {
		return "SELECT * FROM " + s, nil
	}

	return "", fmt.Errorf("VAR expression does not produce a table (got: %s)", s)
}

// hasMatchingOuterParens reports whether s starts with '(' and its matching ')'
// is the very last character — i.e. the entire string is wrapped in one pair.
func hasMatchingOuterParens(s string) bool {
	if len(s) < 2 || s[0] != '(' {
		return false
	}
	depth := 0
	for i, ch := range s {
		switch ch {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i == len(s)-1
			}
		}
	}
	return false
}

// ─── Scalar VAR support ───────────────────────────────────────────────────────

// IsTableExpr reports whether expr evaluates to a table result set. Function
// kind comes from the implemented DUX function registry, never emitted SQL.
func (e *Emitter) IsTableExpr(expr *parser.Expr) (bool, error) {
	if expr == nil || expr.Left == nil {
		return false, fmt.Errorf("empty expression")
	}
	// Binary ops at top level are always scalar (arithmetic / comparisons).
	if len(expr.Right) > 0 {
		return false, nil
	}
	t := expr.Left
	switch {
	case t.Literal != nil:
		return false, nil
	case t.Ident != "":
		// Bare identifier — check if it is a known scalar VAR first.
		if e.ScalarVars != nil {
			if _, ok := e.ScalarVars[strings.ToLower(t.Ident)]; ok {
				return false, nil
			}
		}
		return true, nil // treat as a bare table reference
	case t.QuotedIdent != "":
		return true, nil
	case t.FuncCall != nil:
		return isTableFunc(t.FuncCall.Name), nil
	}
	return false, nil
}

// EmitScalarQuery returns a complete SQL SELECT statement that evaluates expr
// as a scalar value. The result set will have one row and one column.
//
// COUNTROWS is handled specially: its table argument is wrapped in a subquery
// so that filter expressions (e.g. COUNTROWS(FILTER(...))) work correctly.
//
// For all other aggregations, the primary table is inferred from column
// references in the expression and appended as the FROM clause.
//
// Iterator functions (SUMX, AVERAGEX, etc.) already emit as self-contained
// subqueries; the outer parens are stripped to produce a bare SELECT.
func (e *Emitter) EmitScalarQuery(expr *parser.Expr) (string, error) {
	// COUNTROWS needs its table argument wrapped, not inlined.
	if expr.Left != nil && expr.Left.FuncCall != nil && len(expr.Right) == 0 {
		if strings.ToUpper(expr.Left.FuncCall.Name) == "COUNTROWS" {
			return e.emitCountRowsScalar(expr.Left.FuncCall)
		}
	}

	sql, err := e.emitExpr(expr)
	if err != nil {
		return "", err
	}

	// Iterator functions emit as (SELECT agg FROM table AS alias).
	// Strip the outer parens to make a standalone SELECT.
	if hasMatchingOuterParens(sql) {
		inner := strings.TrimSpace(sql[1 : len(sql)-1])
		if strings.HasPrefix(strings.ToUpper(inner), "SELECT") {
			return inner, nil
		}
	}

	// Derive a FROM clause from tables referenced in the expression.
	table := e.primaryTableFromExpr(expr)
	if table == "" {
		return "SELECT " + sql, nil
	}
	return fmt.Sprintf("SELECT %s FROM %s", sql, e.sqlTable(table)), nil
}

// emitCountRowsScalar emits COUNTROWS as a standalone scalar SELECT.
// The table argument may be a bare table name or a table function like FILTER.
func (e *Emitter) emitCountRowsScalar(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) == 0 {
		return "SELECT COUNT(*)", nil
	}
	tableSQL, err := e.emitExprAsTable(fc.Args[0])
	if err != nil {
		return "", fmt.Errorf("COUNTROWS: argument is not a table expression: %w", err)
	}
	return fmt.Sprintf("SELECT COUNT(*) FROM (%s) AS __counted", tableSQL), nil
}

// primaryTableFromExpr returns the first table name found in the expression,
// by scanning ColRef table qualifiers and, as a fallback, the first Ident
// inside aggregation function arguments (for COUNTROWS(Table) patterns).
func (e *Emitter) primaryTableFromExpr(expr *parser.Expr) string {
	if tables := e.measureExprTables(expr); len(tables) > 0 {
		return tables[0]
	}
	return firstIdentInFuncArgs(expr)
}

// firstIdentInFuncArgs returns the first bare Ident found inside any function
// argument in the expression tree. Used to pick up the table name from
// COUNTROWS(tableName) patterns where collectTables finds nothing.
func firstIdentInFuncArgs(expr *parser.Expr) string {
	var found string
	walkTerms(expr, func(t *parser.Term) bool {
		if found != "" {
			return false
		}
		if t.FuncCall != nil {
			for _, arg := range t.FuncCall.Args {
				if arg.Left != nil && arg.Left.Ident != "" && len(arg.Right) == 0 {
					found = strings.ToLower(arg.Left.Ident)
					return false
				}
			}
		}
		return true
	})
	return found
}

// anyToSQL converts a Go value scanned from database/sql into a SQL literal
// string suitable for embedding in a DuckDB query.
func anyToSQL(v any) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case int64:
		return fmt.Sprintf("%d", val)
	case int32:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%g", val)
	case float32:
		return fmt.Sprintf("%g", val)
	case string:
		return "'" + strings.ReplaceAll(val, "'", "''") + "'"
	case []byte:
		return "'" + strings.ReplaceAll(string(val), "'", "''") + "'"
	case bool:
		if val {
			return "TRUE"
		}
		return "FALSE"
	default:
		return "'" + strings.ReplaceAll(fmt.Sprintf("%v", val), "'", "''") + "'"
	}
}

// normaliseOp maps DUX operator tokens to their DuckDB equivalents.
func normaliseOp(op string) string {
	switch strings.ToUpper(op) {
	case "&&", "AND":
		return "AND"
	case "||", "OR":
		return "OR"
	case "<>", "!=":
		return "<>"
	default:
		return op
	}
}
