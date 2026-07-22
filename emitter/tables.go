// Composable table sources and table-combinator functions.
//
// tableSourceFromExpr lets every table-taking function (FILTER, TOPN,
// ADDCOLUMNS, SUMX, CROSSJOIN, ...) accept either a bare table reference or a
// nested table expression, which is emitted as a parenthesised subquery with a
// generated alias. applyOrderBy implements EVALUATE ... ORDER BY ... START AT.
package emitter

import (
	"fmt"
	"strings"

	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// tableSource is a FROM-clause-ready table argument.
type tableSource struct {
	sql    string // bare identifier (already SQL-quoted) or "(SELECT ...)"
	name   string // underlying table name for row-context binding; "" if none
	nested bool   // true when sql is a parenthesised subquery needing an alias
}

// tableSourceFromExpr resolves the table argument of a table function. Bare
// references (table names, VAR names, ALL(T)) resolve to their identifier;
// nested table functions emit as subqueries.
func (e *Emitter) tableSourceFromExpr(expr *parser.Expr) (*tableSource, error) {
	name, nameErr := e.tableNameFromExpr(expr)
	if nameErr == nil {
		return &tableSource{sql: e.sqlTable(name), name: name}, nil
	}
	if expr != nil && expr.Left != nil && expr.Left.FuncCall != nil {
		sub, err := e.emitExprAsTable(expr)
		if err != nil {
			return nil, err
		}
		return &tableSource{sql: "(" + sub + ")", name: underlyingTableName(expr), nested: true}, nil
	}
	return nil, nameErr
}

// fromClauseSQL returns the source ready for a FROM clause, attaching a unique
// alias when the source is a subquery.
func (e *Emitter) fromClauseSQL(src *tableSource) string {
	if src.nested {
		return src.sql + " AS " + e.nextAlias("__src")
	}
	return src.sql
}

// nextAlias returns a unique SQL alias with the given prefix.
func (e *Emitter) nextAlias(prefix string) string {
	e.aliasSeq++
	return fmt.Sprintf("%s%d", prefix, e.aliasSeq)
}

// underlyingTableName walks row-preserving table functions (FILTER, ALL,
// ADDCOLUMNS, TOPN, ...) down to the base table they iterate, so iterator
// functions can still bind row context for column references against it.
// Returns "" for shape-changing functions (SUMMARIZECOLUMNS, SELECTCOLUMNS,
// UNION, ...), whose output columns are referenced bare.
func underlyingTableName(expr *parser.Expr) string {
	if expr == nil || expr.Left == nil {
		return ""
	}
	t := expr.Left
	switch {
	case t.Ident != "":
		return t.Ident
	case t.QuotedIdent != "":
		return semantic.StripSingleQuotes(t.QuotedIdent)
	case t.QualifiedIdent != "":
		return t.QualifiedIdent
	case t.ColRef != nil && t.ColRef.Table != "":
		return t.ColRef.Table
	case t.FuncCall != nil:
		switch strings.ToUpper(t.FuncCall.Name) {
		case "FILTER", "ALL", "ALLEXCEPT", "ADDCOLUMNS", "CALCULATETABLE":
			if len(t.FuncCall.Args) >= 1 {
				return underlyingTableName(t.FuncCall.Args[0])
			}
		case "TOPN":
			if len(t.FuncCall.Args) >= 2 {
				return underlyingTableName(t.FuncCall.Args[1])
			}
		}
	}
	return ""
}

// ─── CROSSJOIN / GENERATE / GENERATEALL ─────────────────────────────────────

// emitCrossJoin emits CROSSJOIN(T1, T2, ...) as chained CROSS JOINs.
func (e *Emitter) emitCrossJoin(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) < 2 {
		return "", fmt.Errorf("CROSSJOIN requires at least 2 arguments")
	}
	parts := make([]string, 0, len(fc.Args))
	for _, arg := range fc.Args {
		src, err := e.tableSourceFromExpr(arg)
		if err != nil {
			return "", fmt.Errorf("CROSSJOIN: %w", err)
		}
		parts = append(parts, e.fromClauseSQL(src))
	}
	return "SELECT * FROM " + strings.Join(parts, " CROSS JOIN "), nil
}

// emitGenerate emits GENERATE / GENERATEALL as a LATERAL join: the second
// table expression is evaluated once per row of the first, with the first
// table's columns available via row context (e.g. products[product] inside
// the second argument refers to the current outer row).
func (e *Emitter) emitGenerate(fc *parser.FuncCall, leftJoin bool) (string, error) {
	name := strings.ToUpper(fc.Name)
	if len(fc.Args) != 2 {
		return "", fmt.Errorf("%s requires exactly 2 arguments", name)
	}
	src, err := e.tableSourceFromExpr(fc.Args[0])
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}

	var alias string
	if src.name != "" {
		alias = "__gen_" + sanitizeAliasSuffix(src.name)
		e.rowCtx.Push(semantic.RowBinding{Table: src.name, Alias: alias})
		defer e.rowCtx.Pop()
	} else {
		alias = e.nextAlias("__gen")
	}

	inner, err := e.emitExprAsTable(fc.Args[1])
	if err != nil {
		return "", fmt.Errorf("%s: second argument must be a table expression: %w", name, err)
	}
	innerAlias := e.nextAlias("__gen")

	if leftJoin {
		return fmt.Sprintf("SELECT * FROM %s AS %s LEFT JOIN LATERAL (%s) AS %s ON TRUE",
			src.sql, alias, inner, innerAlias), nil
	}
	return fmt.Sprintf("SELECT * FROM %s AS %s CROSS JOIN LATERAL (%s) AS %s",
		src.sql, alias, inner, innerAlias), nil
}

// ─── EVALUATE ... ORDER BY ... START AT ──────────────────────────────────────

// applyOrderBy wraps the emitted query in an outer SELECT applying the
// EVALUATE clause's ORDER BY (and START AT) over the result columns.
func (e *Emitter) applyOrderBy(sql string, ev *parser.EvaluateClause) (string, error) {
	if len(ev.OrderBy) == 0 {
		if len(ev.StartAt) > 0 {
			return "", fmt.Errorf("START AT requires an ORDER BY clause")
		}
		return sql, nil
	}

	inner, err := normaliseToSelect(sql)
	if err != nil {
		return "", fmt.Errorf("ORDER BY: %w", err)
	}

	keys := make([]string, len(ev.OrderBy))
	descs := make([]bool, len(ev.OrderBy))
	orderItems := make([]string, len(ev.OrderBy))
	for i, ob := range ev.OrderBy {
		k, err := e.emitOrderKey(ob.Expr)
		if err != nil {
			return "", err
		}
		keys[i] = k
		descs[i] = strings.EqualFold(ob.Dir, "DESC")
		orderItems[i] = k
		if descs[i] {
			orderItems[i] += " DESC"
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "SELECT * FROM (\n%s\n) AS __q", inner)

	if len(ev.StartAt) > 0 {
		if len(ev.StartAt) > len(ev.OrderBy) {
			return "", fmt.Errorf("START AT has more values than ORDER BY keys")
		}
		vals := make([]string, len(ev.StartAt))
		for i, v := range ev.StartAt {
			if descs[i] {
				return "", fmt.Errorf("START AT is only supported with ascending sort keys")
			}
			s, err := e.emitExpr(v)
			if err != nil {
				return "", err
			}
			vals[i] = s
		}
		fmt.Fprintf(&sb, "\nWHERE (%s) >= (%s)",
			strings.Join(keys[:len(vals)], ", "), strings.Join(vals, ", "))
	}

	fmt.Fprintf(&sb, "\nORDER BY %s", strings.Join(orderItems, ", "))
	return sb.String(), nil
}

// emitOrderKey emits one ORDER BY sort key. A plain column reference names an
// output column of the wrapped query and is emitted as a quoted identifier —
// never expanded as a measure — so both group columns and measure aliases
// ("Total Matches") sort correctly. Any other expression emits normally.
func (e *Emitter) emitOrderKey(expr *parser.Expr) (string, error) {
	if expr != nil && expr.Left != nil && expr.Left.ColRef != nil &&
		len(expr.Right) == 0 && !expr.Left.Neg {
		cr := expr.Left.ColRef
		name := semantic.StripBrackets(cr.Column)
		if cr.Table != "" {
			name = e.resolveColName(semantic.StripSingleQuotes(cr.Table), name)
		}
		return quoteIdent(name), nil
	}
	return e.emitExpr(expr)
}
