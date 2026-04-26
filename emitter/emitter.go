// Package emitter walks a resolved AST and produces a DuckDB SQL string.
package emitter

import (
	"fmt"
	"strings"
	"unicode"

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
}

// Emit produces a DuckDB SQL string from a resolved Query.
// If the query has VAR bindings the caller is responsible for materialising
// them via EmitVarCreate before running this SQL; see executor.Execute.
func (e *Emitter) Emit(q *parser.Query) (string, error) {
	return e.emitTableExpr(q.Evaluate.Table)
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
// own casing is used verbatim (e.g. "l_1stIn" stays "l_1stIn").
// Falls back to toSnakeCase for schema-free unit tests or unknown columns.
func (e *Emitter) resolveColName(table, stripped string) string {
	if e.Schema != nil && table != "" {
		if t, ok := e.Schema.Tables[table]; ok {
			if col, ok := t.Columns[stripped]; ok {
				return col.Name
			}
		}
	}
	return toSnakeCase(stripped)
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

func (e *Emitter) emitFuncCall(fc *parser.FuncCall) (string, error) {
	switch strings.ToUpper(fc.Name) {
	// Aggregation
	case "SUM":
		return e.emitSimpleAgg("SUM", fc)
	case "AVERAGE":
		return e.emitSimpleAgg("AVG", fc)
	case "COUNT":
		return e.emitSimpleAgg("COUNT", fc)
	case "COUNTA":
		return e.emitSimpleAgg("COUNT", fc)
	case "COUNTBLANK":
		return e.emitCountBlank(fc)
	case "MIN":
		return e.emitSimpleAgg("MIN", fc)
	case "MAX":
		return e.emitSimpleAgg("MAX", fc)
	case "MEDIAN":
		return e.emitSimpleAgg("MEDIAN", fc)
	case "COUNTROWS":
		return e.emitCountRows(fc)
	case "DISTINCTCOUNT":
		return e.emitDistinctCount(fc)

	// Iterator (row-context) functions
	case "SUMX":
		return e.emitIterAgg("SUM", fc)
	case "AVERAGEX":
		return e.emitIterAgg("AVG", fc)
	case "COUNTX":
		return e.emitIterAgg("COUNT", fc)
	case "MINX":
		return e.emitIterAgg("MIN", fc)
	case "MAXX":
		return e.emitIterAgg("MAX", fc)
	case "CONCATENATEX":
		return e.emitConcatenateX(fc)

	// Filter context
	case "CALCULATE":
		return e.emitCalculate(fc)
	case "TREATAS":
		return e.emitTreatas(fc)
	case "FILTER":
		return e.emitFilter(fc)
	case "ALL":
		return e.emitAll(fc)
	case "VALUES":
		return e.emitValuesOrDistinct("VALUES", fc)
	case "DISTINCT":
		return e.emitValuesOrDistinct("DISTINCT", fc)

	// Table constructors / operations
	case "SUMMARIZECOLUMNS":
		return e.emitSummarizeColumns(fc)
	case "ADDCOLUMNS":
		return e.emitAddColumns(fc)
	case "SELECTCOLUMNS":
		return e.emitSelectColumns(fc)
	case "UNION":
		return e.emitSetOp("UNION ALL", fc)
	case "INTERSECT":
		return e.emitSetOp("INTERSECT", fc)
	case "EXCEPT":
		return e.emitSetOp("EXCEPT", fc)
	case "TOPN":
		return e.emitTopN(fc)

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

func (e *Emitter) emitCountRows(_ *parser.FuncCall) (string, error) {
	// COUNTROWS(Table) → COUNT(*)
	// The table argument is used in the FROM clause, not inside COUNT.
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

// emitIterAgg emits an X-function as:
//
//	SELECT <agg>(__row.<expr>) FROM <table> AS __row
//
// When inside SUMMARIZECOLUMNS the outer GROUP BY is correlated automatically
// by the SUMMARIZECOLUMNS emitter.
func (e *Emitter) emitIterAgg(agg string, fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 2 {
		return "", fmt.Errorf("%s requires exactly 2 arguments", fc.Name)
	}

	tableName, err := e.tableNameFromExpr(fc.Args[0])
	if err != nil {
		return "", fmt.Errorf("%s: first argument must be a table reference: %w", fc.Name, err)
	}

	alias := "__row_" + strings.ToLower(strings.ReplaceAll(tableName, " ", "_"))
	e.rowCtx.Push(semantic.RowBinding{Table: tableName, Alias: alias})
	defer e.rowCtx.Pop()

	inner, err := e.emitExpr(fc.Args[1])
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"(SELECT %s(%s) FROM %s AS %s)",
		agg, inner, sqlIdent(tableName), alias,
	), nil
}

// emitConcatenateX emits CONCATENATEX as string_agg.
func (e *Emitter) emitConcatenateX(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) < 2 || len(fc.Args) > 3 {
		return "", fmt.Errorf("CONCATENATEX requires 2 or 3 arguments")
	}

	tableName, err := e.tableNameFromExpr(fc.Args[0])
	if err != nil {
		return "", fmt.Errorf("CONCATENATEX: first argument must be a table reference: %w", err)
	}

	alias := "__row_" + strings.ToLower(strings.ReplaceAll(tableName, " ", "_"))
	e.rowCtx.Push(semantic.RowBinding{Table: tableName, Alias: alias})
	defer e.rowCtx.Pop()

	inner, err := e.emitExpr(fc.Args[1])
	if err != nil {
		return "", err
	}

	delim := "', '"
	if len(fc.Args) == 3 {
		d, err := e.emitExpr(fc.Args[2])
		if err != nil {
			return "", err
		}
		delim = d
	}

	return fmt.Sprintf(
		"(SELECT string_agg(%s, %s) FROM %s AS %s)",
		inner, delim, sqlIdent(tableName), alias,
	), nil
}

// ─── Filter context functions ────────────────────────────────────────────────

// emitCalculate emits: SELECT <inner> FROM <primary_table> WHERE <filters>.
// When there are no filter arguments the inner expression is returned as-is.
// At the top level this is a complete SQL statement; when used as a scalar
// expression inside ADDCOLUMNS column lists, the caller wraps it in parens.
func (e *Emitter) emitCalculate(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) == 0 {
		return "", fmt.Errorf("CALCULATE requires at least 1 argument")
	}

	// Emit the inner expression (aggregate or measure reference).
	inner, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}

	// If no filters, just return the expression.
	if len(fc.Args) == 1 {
		return inner, nil
	}

	// Determine the primary table from the inner expression's column refs.
	tables := collectTables(fc.Args[0])
	primaryTable := ""
	if len(tables) > 0 {
		primaryTable = sqlIdent(tables[0])
	}

	// Emit filter predicates. Column refs are emitted without table qualifiers
	// (the table qualifier identifies the filter context but is dropped in SQL).
	var filters []string
	for _, arg := range fc.Args[1:] {
		f, err := e.emitExpr(arg)
		if err != nil {
			return "", err
		}
		filters = append(filters, f)
	}
	whereClause := strings.Join(filters, " AND ")

	if primaryTable == "" {
		return fmt.Sprintf("SELECT %s WHERE %s", inner, whereClause), nil
	}
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s", inner, primaryTable, whereClause), nil
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

// emitFilter emits FILTER(Table, predicate) as a CTE subquery.
func (e *Emitter) emitFilter(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 2 {
		return "", fmt.Errorf("FILTER requires exactly 2 arguments")
	}
	table, err := e.tableNameFromExpr(fc.Args[0])
	if err != nil {
		return "", fmt.Errorf("FILTER: first argument must be a table reference: %w", err)
	}
	pred, err := e.emitExpr(fc.Args[1])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("(SELECT * FROM %s WHERE %s)", sqlIdent(table), pred), nil
}

// emitAll stubs ALL filter-clearing. Full semantics require filter-context integration.
func (e *Emitter) emitAll(_ *parser.FuncCall) (string, error) {
	// ALL() requires filter-context propagation which is not yet implemented.
	// Returning an error is safer than silently emitting a no-op SQL comment.
	return "", fmt.Errorf("ALL() requires filter-context support which is not yet implemented")
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
	// TREATAS calls are separated into WHERE predicates.
	var groupCols []string      // emitted SQL for true group-by keys
	var inlineMeasures []string // emitted SQL for measure refs in the group position
	var wherePreds []string     // emitted SQL for TREATAS filter predicates
	for _, arg := range groupArgs {
		// Check for a TREATAS call — emit as a WHERE predicate, not a group column.
		if arg.Left != nil && arg.Left.FuncCall != nil &&
			strings.ToUpper(arg.Left.FuncCall.Name) == "TREATAS" && len(arg.Right) == 0 {
			pred, err := e.emitTreatas(arg.Left.FuncCall)
			if err != nil {
				return "", err
			}
			wherePreds = append(wherePreds, pred)
			continue
		}
		if e.isMeasureColRef(arg) {
			sql, err := e.emitExpr(arg)
			if err != nil {
				return "", err
			}
			inlineMeasures = append(inlineMeasures, sql)
		} else {
			gc, err := e.emitExpr(arg)
			if err != nil {
				return "", err
			}
			groupCols = append(groupCols, gc)
		}
	}

	// Emit name/expr measure pairs.
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
	var fromClause string
	switch len(allTables) {
	case 0:
		// No tables referenced — no FROM clause.
	case 1:
		fromClause = sqlIdent(allTables[0])
	default:
		if e.Schema != nil {
			jp, jpErr := semantic.InferJoinPath(e.Schema, allTables)
			if jpErr != nil {
				return "", jpErr
			}
			var fbuf strings.Builder
			fbuf.WriteString(sqlIdent(allTables[0]))
			for _, step := range jp.Steps {
				fmt.Fprintf(&fbuf, "\nLEFT JOIN %s ON %s.%s = %s.%s",
					sqlIdent(step.Table),
					sqlIdent(step.FromTable), step.OnFromCol,
					sqlIdent(step.Table), step.OnToCol,
				)
			}
			fromClause = fbuf.String()
		} else {
			// No schema available — comma-join and let DuckDB report any errors.
			parts := make([]string, len(allTables))
			for i, t := range allTables {
				parts[i] = sqlIdent(t)
			}
			fromClause = strings.Join(parts, ", ")
		}
	}

	// Build SELECT list.
	var selects []string
	selects = append(selects, groupCols...)
	selects = append(selects, inlineMeasures...)
	selects = append(selects, measures...)

	var sb strings.Builder
	fmt.Fprintf(&sb, "SELECT %s", strings.Join(selects, ", "))
	if fromClause != "" {
		fmt.Fprintf(&sb, "\nFROM %s", fromClause)
	}
	if len(wherePreds) > 0 {
		fmt.Fprintf(&sb, "\nWHERE %s", strings.Join(wherePreds, " AND "))
	}
	if len(groupCols) > 0 {
		fmt.Fprintf(&sb, "\nGROUP BY %s", strings.Join(groupCols, ", "))
	}
	return sb.String(), nil
}

// emitAddColumns emits:
//
//	SELECT *, (expr1) AS "name1", ... FROM table
//
// Column references inside the expression arguments are emitted without table
// qualifiers because the source table is the sole table in the FROM clause.
func (e *Emitter) emitAddColumns(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) < 3 || (len(fc.Args)-1)%2 != 0 {
		return "", fmt.Errorf("ADDCOLUMNS requires a table then name/expr pairs")
	}
	table, err := e.tableNameFromExpr(fc.Args[0])
	if err != nil {
		return "", fmt.Errorf("ADDCOLUMNS: first argument must be a table reference: %w", err)
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

	return fmt.Sprintf("SELECT *, %s FROM %s", strings.Join(cols, ", "), sqlIdent(table)), nil
}

// emitSelectColumns emits:
//
//	SELECT (expr1) AS "name1", ... FROM table
//
// Column references inside the expression arguments are emitted without table
// qualifiers because the source table is the sole table in the FROM clause.
func (e *Emitter) emitSelectColumns(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) < 3 || (len(fc.Args)-1)%2 != 0 {
		return "", fmt.Errorf("SELECTCOLUMNS requires a table then name/expr pairs")
	}
	table, err := e.tableNameFromExpr(fc.Args[0])
	if err != nil {
		return "", fmt.Errorf("SELECTCOLUMNS: first argument must be a table reference: %w", err)
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

	return fmt.Sprintf("SELECT %s FROM %s", strings.Join(cols, ", "), sqlIdent(table)), nil
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

// emitTopN emits TOPN as ORDER BY … DESC LIMIT n.
func (e *Emitter) emitTopN(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) < 3 {
		return "", fmt.Errorf("TOPN requires at least 3 arguments (n, table, expr)")
	}
	n, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	table, err := e.tableNameFromExpr(fc.Args[1])
	if err != nil {
		return "", fmt.Errorf("TOPN: second argument must be a table reference: %w", err)
	}
	orderExpr, err := e.emitExpr(fc.Args[2])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("SELECT * FROM %s ORDER BY %s DESC LIMIT %s",
		sqlIdent(table), orderExpr, n), nil
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

// sqlQualifiedIdent returns a DuckDB-safe SQL identifier for a db-qualified
// table name (e.g. "atp.matches" → atp.matches, "my db.my table" → "my db"."my table").
func sqlQualifiedIdent(name string) string {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) != 2 {
		return strings.ToLower(name)
	}
	return sqlIdent(parts[0]) + "." + sqlIdent(parts[1])
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

// collectTables returns the distinct table names (in encounter order) that are
// directly referenced via ColRef nodes within expr. Nested function call
// arguments are traversed recursively.
func collectTables(expr *parser.Expr) []string {
	seen := map[string]bool{}
	var result []string

	var walkExpr func(*parser.Expr)
	var walkTerm func(*parser.Term)

	walkExpr = func(e *parser.Expr) {
		if e == nil {
			return
		}
		walkTerm(e.Left)
		for _, op := range e.Right {
			walkTerm(op.Right)
		}
	}
	walkTerm = func(t *parser.Term) {
		if t == nil {
			return
		}
		if t.ColRef != nil && t.ColRef.Table != "" {
			tableName := semantic.StripSingleQuotes(t.ColRef.Table)
			if !seen[tableName] {
				seen[tableName] = true
				result = append(result, tableName)
			}
		}
		if t.FuncCall != nil {
			for _, arg := range t.FuncCall.Args {
				walkExpr(arg)
			}
		}
		if t.SubExpr != nil {
			walkExpr(t.SubExpr)
		}
	}

	walkExpr(expr)
	return result
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
		// Nested table function (e.g. FILTER inside ADDCOLUMNS): emit and alias.
		// TODO: generate a unique alias for nested table expressions.
		return "", fmt.Errorf("nested table expressions not yet supported")
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
			"CROSSJOIN", "GENERATE", "GENERATEALL":
			return true, nil
		// Known scalar / aggregation functions.
		case "SUM", "AVERAGE", "COUNT", "COUNTA", "COUNTBLANK", "COUNTROWS",
			"DISTINCTCOUNT", "MIN", "MAX", "MEDIAN",
			"SUMX", "AVERAGEX", "COUNTX", "MINX", "MAXX", "CONCATENATEX",
			"DIVIDE", "IF", "SWITCH", "NOT", "AND", "OR", "ISBLANK", "BLANK":
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
	if expr == nil {
		return ""
	}
	var walkExpr func(*parser.Expr) string
	var walkTerm func(*parser.Term) string
	walkExpr = func(e *parser.Expr) string {
		if e == nil {
			return ""
		}
		if s := walkTerm(e.Left); s != "" {
			return s
		}
		for _, op := range e.Right {
			if s := walkTerm(op.Right); s != "" {
				return s
			}
		}
		return ""
	}
	walkTerm = func(t *parser.Term) string {
		if t == nil {
			return ""
		}
		if t.FuncCall != nil {
			for _, arg := range t.FuncCall.Args {
				if arg.Left != nil && arg.Left.Ident != "" && len(arg.Right) == 0 {
					return strings.ToLower(arg.Left.Ident)
				}
				if s := walkExpr(arg); s != "" {
					return s
				}
			}
		}
		return ""
	}
	return walkExpr(expr)
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

// toSnakeCase converts a CamelCase or PascalCase column name to snake_case.
// For column names that are already lower-cased, this is a no-op.
//
//	"UnitPrice"  → "unit_price"
//	"OrderID"    → "order_id"
//	"amount"     → "amount"
func toSnakeCase(s string) string {
	runes := []rune(s)
	var sb strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) && i > 0 {
			prev := runes[i-1]
			// Insert underscore before an uppercase letter when preceded by a
			// lowercase letter (e.g. unitPrice → unit_Price), or when this is
			// the start of a new word after an acronym (e.g. OrderID → Order_ID
			// only at the boundary where the next char is lowercase).
			if unicode.IsLower(prev) {
				sb.WriteRune('_')
			} else if unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
				sb.WriteRune('_')
			}
		}
		sb.WriteRune(unicode.ToLower(r))
	}
	return sb.String()
}
