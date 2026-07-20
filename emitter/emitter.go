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
	ScalarVars map[string]any
	rowCtx     semantic.RowContext
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
		// db.table syntax — emit as-is; DuckDB resolves the attachment alias.
		return fmt.Sprintf("SELECT * FROM %s", sqlQualifiedIdent(t.QualifiedTable)), nil
	}
	if t.QuotedTable != "" {
		return fmt.Sprintf("SELECT * FROM %s", sqlIdent(semantic.StripSingleQuotes(t.QuotedTable))), nil
	}
	return fmt.Sprintf("SELECT * FROM %s", strings.ToLower(t.Table)), nil
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
	var sb strings.Builder
	sb.WriteString(left)
	for _, op := range expr.Right {
		right, err := e.emitTerm(op.Right)
		if err != nil {
			return "", err
		}
		sb.WriteString(" ")
		sb.WriteString(normaliseOp(op.Op))
		sb.WriteString(" ")
		sb.WriteString(right)
	}
	return sb.String(), nil
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
		return sqlIdent(semantic.StripSingleQuotes(t.QuotedIdent)), nil
	case t.QualifiedIdent != "":
		// db.table as a bare term (e.g. first argument of FILTER(atp.matches, ...)).
		return sqlQualifiedIdent(t.QualifiedIdent), nil
	case t.Ident != "":
		// Check scalar VAR substitution before treating as a table name.
		if e.ScalarVars != nil {
			if val, ok := e.ScalarVars[strings.ToLower(t.Ident)]; ok {
				return anyToSQL(val), nil
			}
		}
		// Bare table name as a term (e.g. first argument of FILTER/ADDCOLUMNS).
		return strings.ToLower(t.Ident), nil
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

	// Iterator row-context alias takes highest priority.
	if alias, ok := e.rowCtx.ResolveAlias(tableKey); ok {
		return alias + "." + e.resolveColName(tableKey, stripped), nil
	}

	if measures := e.effectiveMeasures(); measures != nil {
		if tableKey != "" {
			// Table-qualified measure expansion.
			if tableMeasures, ok := measures[tableKey]; ok {
				if def, ok := tableMeasures[stripped]; ok && def.Expr != nil {
					return e.emitExpr(def.Expr)
				}
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

	// Plain column reference — use the exact name from the schema when available
	// so that mixed-case column names (e.g. l_1stIn) are preserved verbatim.
	return e.resolveColName(tableKey, stripped), nil
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
		if t, ok := e.Schema.Tables[table]; ok {
			if col, ok := t.Columns[stripped]; ok {
				return col.Name
			}
		}
	}
	return stripped
}

// ─── Literals ────────────────────────────────────────────────────────────────

func (e *Emitter) emitLiteral(l *parser.Literal) string {
	switch {
	case l.String != nil:
		// Raw token includes surrounding double-quotes; convert to SQL single-quoted.
		inner := (*l.String)[1 : len(*l.String)-1]
		return "'" + strings.ReplaceAll(inner, "'", "''") + "'"
	case l.Number != nil:
		return fmt.Sprintf("%g", *l.Number)
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
		// Unknown / passthrough function: emit as-is and let DuckDB validate.
		return e.emitPassthrough(fc)
	}
}

// ─── Aggregation functions ───────────────────────────────────────────────────

func (e *Emitter) emitSimpleAgg(duckName string, fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 1 {
		return "", fmt.Errorf("%s requires exactly 1 argument", fc.Name)
	}
	arg, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s(%s)", duckName, arg), nil
}

func (e *Emitter) emitCountRows(fc *parser.FuncCall) (string, error) {
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
	arg, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("COUNT(DISTINCT %s)", arg), nil
}

// emitCountBlank emits COUNTBLANK(T[C]) as COUNT(*) - COUNT(col).
// Since BLANK maps to NULL, this counts all rows where the column is NULL.
func (e *Emitter) emitCountBlank(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 1 {
		return "", fmt.Errorf("COUNTBLANK requires exactly 1 argument")
	}
	arg, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("(COUNT(*) - COUNT(%s))", arg), nil
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

	if e.groupedIterInline(fc.Args[0]) {
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
func (e *Emitter) groupedIterInline(arg *parser.Expr) bool {
	if e.groupCtx == nil || len(e.rowCtx.Bindings) > 0 || e.Schema == nil {
		return false
	}
	name := bareTableArg(arg)
	if name == "" {
		return false
	}
	_, ok := e.Schema.Tables[semantic.ResolveTable(e.Schema, name)]
	return ok
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
	if src.name != "" {
		alias := "__row_" + sanitizeAliasSuffix(src.name)
		e.rowCtx.Push(semantic.RowBinding{Table: src.name, Alias: alias})
		return src, alias, func() { e.rowCtx.Pop() }, nil
	}
	return src, e.nextAlias("__row"), func() {}, nil
}

// emitConcatenateX emits CONCATENATEX as string_agg. The same group-context
// inlining as emitIterAgg applies inside SUMMARIZECOLUMNS.
func (e *Emitter) emitConcatenateX(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) < 2 || len(fc.Args) > 3 {
		return "", fmt.Errorf("CONCATENATEX requires 2 or 3 arguments")
	}

	if e.groupedIterInline(fc.Args[0]) {
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
	for _, t := range collectTables(fc.Args[0]) {
		addTbl(t)
	}
	for _, arg := range preds {
		for _, t := range collectTables(arg) {
			addTbl(t)
		}
	}
	for _, tf := range cm.timeFilters {
		addTbl(tf.table)
	}

	// Emit filter predicates.
	var filters []string
	for _, arg := range preds {
		f, err := e.emitExpr(arg)
		if err != nil {
			return "", err
		}
		filters = append(filters, f)
	}
	for _, tf := range cm.timeFilters {
		pred, err := e.emitTimeIntelPred(tf, sqlIdent(tf.table)+"."+tf.col)
		if err != nil {
			return "", err
		}
		filters = append(filters, pred)
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
	whereClause := strings.Join(filters, " AND ")

	// Build FROM clause, joining additional tables when needed.
	if len(allTables) == 0 {
		return fmt.Sprintf("(SELECT %s WHERE %s)", inner, whereClause), nil
	}
	fromClause, err := e.calcFromClause(allTables)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("(SELECT %s FROM %s WHERE %s)", inner, fromClause, whereClause), nil
}

// calcFromClause builds a FROM clause over allTables, inferring join steps
// through the schema when more than one table is involved.
func (e *Emitter) calcFromClause(allTables []string) (string, error) {
	if len(allTables) == 1 {
		return sqlIdent(allTables[0]), nil
	}
	if e.Schema == nil {
		parts := make([]string, len(allTables))
		for i, t := range allTables {
			parts[i] = sqlIdent(t)
		}
		return strings.Join(parts, ", "), nil
	}
	jp, err := semantic.InferJoinPath(e.Schema, allTables)
	if err != nil {
		return "", err
	}
	return emitFlatJoins(allTables[0], jp), nil
}

// emitFlatJoins renders a single flat LEFT JOIN tree rooted at primary,
// following the inferred join path in order.
func emitFlatJoins(primary string, jp *semantic.JoinPath) string {
	var fbuf strings.Builder
	fbuf.WriteString(sqlIdent(primary))
	for _, step := range jp.Steps {
		fmt.Fprintf(&fbuf, "\nLEFT JOIN %s ON %s.%s = %s.%s",
			sqlIdent(step.Table),
			sqlIdent(step.FromTable), step.OnFromCol,
			sqlIdent(step.Table), step.OnToCol,
		)
	}
	return fbuf.String()
}

// emitTreatas emits TREATAS(source, t[col]) as a SQL IN predicate for use
// inside CALCULATE filter arguments.
//
//	Pattern A: TREATAS({"Clay","Grass"}, matches[surface])
//	         → surface IN ('Clay', 'Grass')
//
//	Pattern B: TREATAS(VALUES(players[player_id]), matches[winner_id])
//	         → winner_id IN (SELECT DISTINCT player_id FROM players)
func (e *Emitter) emitTreatas(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 2 {
		return "", fmt.Errorf("TREATAS requires exactly 2 arguments")
	}

	// arg[1]: target column reference.
	targetTerm := fc.Args[1].Left
	if targetTerm == nil || targetTerm.ColRef == nil {
		return "", fmt.Errorf("TREATAS: second argument must be a column reference (e.g. matches[surface])")
	}
	col := e.resolveColName(semantic.StripSingleQuotes(targetTerm.ColRef.Table), semantic.StripBrackets(targetTerm.ColRef.Column))

	// arg[0]: source — TableConstructor or VALUES(t[c]).
	srcTerm := fc.Args[0].Left
	if srcTerm == nil {
		return "", fmt.Errorf("TREATAS: first argument must be a table constructor {...} or VALUES(...)")
	}
	switch {
	case srcTerm.TableConstructor != nil:
		// Pattern A: {"v1", "v2", ...}
		var vals []string
		for _, v := range srcTerm.TableConstructor.Values {
			s, err := e.emitExpr(v)
			if err != nil {
				return "", err
			}
			vals = append(vals, s)
		}
		return fmt.Sprintf("%s IN (%s)", col, strings.Join(vals, ", ")), nil

	case srcTerm.FuncCall != nil && strings.ToUpper(srcTerm.FuncCall.Name) == "VALUES":
		// Pattern B: VALUES(t[c])
		if len(srcTerm.FuncCall.Args) != 1 {
			return "", fmt.Errorf("TREATAS: VALUES requires exactly 1 argument")
		}
		vcr := srcTerm.FuncCall.Args[0].Left
		if vcr == nil || vcr.ColRef == nil {
			return "", fmt.Errorf("TREATAS: VALUES argument must be a column reference")
		}
		srcCol := e.resolveColName(semantic.StripSingleQuotes(vcr.ColRef.Table), semantic.StripBrackets(vcr.ColRef.Column))
		srcTable := sqlIdent(semantic.StripSingleQuotes(vcr.ColRef.Table))
		return fmt.Sprintf("%s IN (SELECT DISTINCT %s FROM %s)", col, srcCol, srcTable), nil

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
	pred, err := e.emitExpr(fc.Args[1])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("(SELECT * FROM %s WHERE %s)", e.fromClauseSQL(src), pred), nil
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
		return fmt.Sprintf("SELECT * FROM %s", sqlIdent(table)), nil
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
		case !strings.EqualFold(tbl, table):
			return "", fmt.Errorf("%s: all column references must belong to the same table", name)
		}
		cols = append(cols, e.resolveColName(tbl, semantic.StripBrackets(t.ColRef.Column)))
	}
	return fmt.Sprintf("(SELECT DISTINCT %s FROM %s)", strings.Join(cols, ", "), sqlIdent(table)), nil
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
	table := primaryTableFromExpr(fc.Args[0])
	if table == "" {
		return "", fmt.Errorf("%s: cannot determine source table from argument", name)
	}
	return fmt.Sprintf("(SELECT DISTINCT %s FROM %s)", col, sqlIdent(table)), nil
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
			// Extract the target table and column for routing.
			var predTable, predCol string
			if len(treatasFC.Args) == 2 && treatasFC.Args[1].Left != nil && treatasFC.Args[1].Left.ColRef != nil {
				cr := treatasFC.Args[1].Left.ColRef
				tbl := semantic.StripSingleQuotes(cr.Table)
				predTable = strings.ToLower(tbl)
				predCol = strings.ToLower(e.resolveColName(tbl, semantic.StripBrackets(cr.Column)))
			}
			pred, err := e.emitTreatas(treatasFC)
			if err != nil {
				return "", err
			}
			wherePreds = append(wherePreds, taggedPred{table: predTable, col: predCol, sql: pred})
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
			predTable := strings.ToLower(semantic.StripSingleQuotes(tbl))
			// The first predicate column on the filtered table identifies the
			// filter for CALCULATE modifier checks (ALL(t[c]) removal).
			var predCol string
			for _, cr := range collectColRefs(filterFC.Args[1]) {
				crTable := semantic.StripSingleQuotes(cr.Table)
				if strings.ToLower(crTable) == predTable {
					predCol = strings.ToLower(e.resolveColName(crTable, semantic.StripBrackets(cr.Column)))
					break
				}
			}
			wherePreds = append(wherePreds, taggedPred{table: predTable, col: predCol, sql: pred})
			continue
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
				plainKeys = append(plainKeys, groupKey{
					table: tbl,
					col:   e.resolveColName(tbl, semantic.StripBrackets(arg.Left.ColRef.Column)),
				})
			}
		}
	}
	groupKeys := append(append([]groupKey{}, plainKeys...), rollupKeys...)

	// Establish the group context so CALCULATE calls inside measure
	// expressions (direct, nested, or via measure expansion) can resolve
	// filter-context modifiers against the group-by keys.
	prevCtx := e.groupCtx
	e.groupCtx = &groupContext{keys: groupKeys, preds: wherePreds}
	defer func() { e.groupCtx = prevCtx }()

	// Stitched codegen (stitched.go) applies when:
	//  - measures span more than one table cluster: a single flat join tree
	//    would fan the clusters out against each other and inflate every
	//    aggregate; or
	//  - the join graph crosses a bidirectional relationship: filter chains
	//    through the bidi edge must gate via EXISTS semi-joins, per measure
	//    context, to avoid many-to-many bridge fan-out.
	plan := e.planMeasures(pairArgs, measureArgs)
	if tableClusterCount(plan.clusters) > 1 || e.stitchForBidi(plan, groupKeys, wherePreds) {
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

	// Collect all referenced tables: group columns first, then measure expressions.
	// Group columns establish the primary (driving) table; measure expressions may
	// reference additional tables that need to be joined in.
	seen := map[string]bool{}
	var allTables []string
	addTbl := func(t string) {
		tl := strings.ToLower(t)
		if !seen[tl] {
			seen[tl] = true
			allTables = append(allTables, t)
		}
	}
	for _, arg := range groupArgs {
		for _, t := range collectTables(arg) {
			addTbl(t)
		}
	}
	for i := 1; i < len(pairArgs); i += 2 {
		for _, t := range collectTables(pairArgs[i]) {
			addTbl(t)
		}
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
	return sb.String(), nil
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

	return fmt.Sprintf("%s%s FROM %s", selectPrefix, strings.Join(cols, ", "), e.fromClauseSQL(src)), nil
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
	return fmt.Sprintf("(%s)\n%s\n(%s)", left, op, right), nil
}

// emitTopN emits TOPN as ORDER BY … DESC LIMIT n. The table argument may be a
// nested table expression (e.g. TOPN(5, SUMMARIZECOLUMNS(...), [Total])).
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
	// Over a computed table the order expression names an output column, so it
	// must not be re-expanded as a measure — reference it like an ORDER BY key.
	var orderExpr string
	if src.nested {
		orderExpr, err = e.emitOrderKey(fc.Args[2])
	} else {
		orderExpr, err = e.emitExpr(fc.Args[2])
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("SELECT * FROM %s ORDER BY %s DESC LIMIT %s",
		e.fromClauseSQL(src), orderExpr, n), nil
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

// emitPassthrough emits an unrecognised function call verbatim so that DuckDB
// built-ins not explicitly handled (LEFT, RIGHT, LEN, ABS, ROUND, etc.) pass
// through naturally.
func (e *Emitter) emitPassthrough(fc *parser.FuncCall) (string, error) {
	args := make([]string, 0, len(fc.Args))
	for _, a := range fc.Args {
		s, err := e.emitExpr(a)
		if err != nil {
			return "", err
		}
		args = append(args, s)
	}
	return fmt.Sprintf("%s(%s)", strings.ToUpper(fc.Name), strings.Join(args, ", ")), nil
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

// sqlQualifiedIdent returns a DuckDB-safe SQL identifier for a qualified
// table name with any number of segments (e.g. "atp.matches" → atp.matches,
// "bev.sales.Customer" → bev.sales.customer, "my db.my table" → "my db"."my table").
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
		if tableMeasures, ok := measures[tableKey]; ok {
			_, isMeasure := tableMeasures[name]
			return isMeasure
		}
		return false
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

// collectTables returns the distinct table names (in encounter order) that are
// directly referenced via ColRef nodes within expr. Nested function call
// arguments are traversed recursively.
func collectTables(expr *parser.Expr) []string {
	seen := map[string]bool{}
	var result []string
	add := func(tableName string) {
		if !seen[tableName] {
			seen[tableName] = true
			result = append(result, tableName)
		}
	}
	walkTerms(expr, func(t *parser.Term) bool {
		if t.ColRef != nil && t.ColRef.Table != "" {
			add(semantic.StripSingleQuotes(t.ColRef.Table))
		}
		// An iterator's bare-table source joins the enclosing FROM so the
		// inline aggregate (see emitIterAgg) has rows to aggregate over.
		if t.FuncCall != nil {
			if tbl := iterBareTable(t.FuncCall); tbl != "" {
				add(tbl)
			}
		}
		return true
	})
	return result
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

// IsTableExpr reports whether expr evaluates to a table result set (true) or a
// scalar value (false). The classification is AST-first for known function
// names, falling back to SQL normalisation for unknown/passthrough functions.
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
		switch strings.ToUpper(t.FuncCall.Name) {
		// Known table-returning functions.
		case "FILTER", "SUMMARIZECOLUMNS", "ADDCOLUMNS", "SELECTCOLUMNS",
			"UNION", "INTERSECT", "EXCEPT", "TOPN", "DISTINCT", "VALUES",
			"ALL", "ALLEXCEPT", "CROSSJOIN", "GENERATE", "GENERATEALL",
			"DATESYTD", "DATESQTD", "DATESMTD", "SAMEPERIODLASTYEAR", "DATEADD",
			"PREVIOUSYEAR", "PREVIOUSQUARTER", "PREVIOUSMONTH", "PREVIOUSDAY",
			"NEXTYEAR", "NEXTQUARTER", "NEXTMONTH", "NEXTDAY",
			"DATESBETWEEN", "DATESINPERIOD", "CALENDAR", "CALENDARAUTO",
			"RELATEDTABLE":
			return true, nil
		// Known scalar / aggregation functions.
		case "SUM", "AVERAGE", "COUNT", "COUNTA", "COUNTBLANK", "COUNTROWS",
			"DISTINCTCOUNT", "MIN", "MAX", "MEDIAN",
			"SUMX", "AVERAGEX", "COUNTX", "MINX", "MAXX", "CONCATENATEX",
			"DIVIDE", "IF", "SWITCH", "NOT", "AND", "OR", "ISBLANK", "BLANK",
			"TOTALYTD", "TOTALQTD", "TOTALMTD", "DATE":
			return false, nil
		}
		// Fallthrough: use SQL normalisation for passthrough / unknown functions.
	}
	sql, err := e.emitExpr(expr)
	if err != nil {
		return false, err
	}
	_, normalErr := normaliseToSelect(sql)
	return normalErr == nil, nil
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
	table := primaryTableFromExpr(expr)
	if table == "" {
		return "SELECT " + sql, nil
	}
	return fmt.Sprintf("SELECT %s FROM %s", sql, sqlIdent(table)), nil
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
func primaryTableFromExpr(expr *parser.Expr) string {
	if tables := collectTables(expr); len(tables) > 0 {
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
