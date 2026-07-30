// Filter-context modifier support for CALCULATE: ALL, ALLEXCEPT,
// REMOVEFILTERS, KEEPFILTERS, and the FILTER(ALL(T), pred) replacement pattern.
//
// SUMMARIZECOLUMNS establishes a groupContext on the Emitter before emitting
// its measure expressions. Any CALCULATE reached during that emission — as the
// direct pair value, nested inside another function such as DIVIDE, or via
// inline measure expansion — consults the group context to decide which
// group-by filters its modifiers remove. Removed filters translate to a
// correlated scalar subquery that correlates only on the kept group keys;
// removing every key yields an uncorrelated subquery (the grand total).
package emitter

import (
	"fmt"
	"strings"

	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// groupKey identifies one group-by column of the enclosing SUMMARIZECOLUMNS.
type groupKey struct {
	table  string // table reference (quotes stripped); compare through tableKey
	col    string // resolved SQL column name
	expr   string // SQL expression naming this key in the current query scope
	line   int
	column int
}

func (e *Emitter) validateGroupKeys(keys []groupKey, measureTables []string) error {
	if e.Schema == nil || len(measureTables) == 0 {
		return nil
	}
	measureSet := map[string]bool{}
	for _, table := range measureTables {
		measureSet[e.tableKey(table)] = true
	}
	for _, key := range keys {
		if measureSet[e.tableKey(key.table)] || semantic.FilterReaches(e.Schema, key.table, measureTables) {
			continue
		}
		return &semantic.SemanticError{
			Message: fmt.Sprintf("measure over %s cannot be grouped by %s[%s]: filters from %s do not safely propagate to the measure tables; group by a shared dimension instead",
				strings.Join(measureTables, ", "), key.table, key.col, key.table),
			Line: key.line, Column: key.column,
		}
	}
	return nil
}

// groupContext carries the enclosing SUMMARIZECOLUMNS grouping state.
type groupContext struct {
	keys  []groupKey
	preds []taggedPred // outer WHERE predicates (from TREATAS group arguments)
}

// calcModifiers is the classified view of CALCULATE's filter arguments.
type calcModifiers struct {
	removeAll    bool               // ALL() with no arguments
	removeTables map[string]bool    // lower(table): ALL(T) / REMOVEFILTERS(T) / ALLEXCEPT(T, …)
	removeCols   map[string]bool    // colKey(table, col): ALL(T[C]) / REMOVEFILTERS(T[C])
	exceptCols   map[string]bool    // colKey(table, col): columns kept by ALLEXCEPT
	preds        []*parser.Expr     // plain predicates — override filters on their columns
	keepPreds    []*parser.Expr     // KEEPFILTERS predicates — additive, never override
	timeFilters  []*timeIntelFilter // DATESYTD, DATEADD, … (see timeintel.go)
}

func (cm *calcModifiers) hasRemovals() bool {
	return cm.removeAll || len(cm.removeTables) > 0 || len(cm.removeCols) > 0 ||
		len(cm.timeFilters) > 0
}

// removed reports whether the filter on table.col (a resolved SQL column name;
// empty when only the table is known) is cleared by the modifiers.
func (cm *calcModifiers) removed(tableKey, col string) bool {
	ck := tableKey + "\x00" + strings.ToLower(col)
	if col != "" && cm.removeCols[ck] {
		return true
	}
	if (cm.removeAll || cm.removeTables[tableKey]) && !(col != "" && cm.exceptCols[ck]) {
		return true
	}
	return false
}

// colKey builds the lookup key for a (table, DAX column name) pair, resolving
// the column through the schema so that it matches groupKey.col.
func (e *Emitter) colKey(table, daxCol string) string {
	return e.resolvedColKey(table, e.resolveColName(table, daxCol))
}

func (e *Emitter) resolvedColKey(table, col string) string {
	return e.tableKey(table) + "\x00" + strings.ToLower(col)
}

// classifyCalcArgs partitions CALCULATE's filter arguments (everything after
// the first argument) into modifiers and predicates.
func (e *Emitter) classifyCalcArgs(args []*parser.Expr) (*calcModifiers, error) {
	cm := &calcModifiers{
		removeTables: map[string]bool{},
		removeCols:   map[string]bool{},
		exceptCols:   map[string]bool{},
	}
	for _, arg := range args {
		if err := e.classifyCalcArg(arg, cm); err != nil {
			return nil, err
		}
	}
	return cm, nil
}

func (e *Emitter) classifyCalcArg(arg *parser.Expr, cm *calcModifiers) error {
	if arg != nil && arg.Left != nil && arg.Left.FuncCall != nil && len(arg.Right) == 0 {
		fc := arg.Left.FuncCall
		switch strings.ToUpper(fc.Name) {
		case "ALL", "REMOVEFILTERS":
			return e.classifyAllArgs(fc, cm)
		case "ALLEXCEPT":
			return e.classifyAllExcept(fc, cm)
		case "KEEPFILTERS":
			if len(fc.Args) != 1 {
				return fmt.Errorf("KEEPFILTERS requires exactly 1 argument")
			}
			cm.keepPreds = append(cm.keepPreds, fc.Args[0])
			return nil
		case "FILTER":
			if len(fc.Args) != 2 {
				return fmt.Errorf("FILTER requires exactly 2 arguments")
			}
			// FILTER(ALL(T), pred) — the canonical DAX filter-replacement
			// pattern: clear T's filters, then apply pred in the cleared context.
			if exprIsAllFamily(fc.Args[0]) {
				if err := e.classifyCalcArg(fc.Args[0], cm); err != nil {
					return err
				}
				cm.preds = append(cm.preds, fc.Args[1])
				return nil
			}
			// FILTER(T, pred) keeps the current context — additive predicate.
			cm.keepPreds = append(cm.keepPreds, fc.Args[1])
			return nil
		}
		if name := strings.ToUpper(fc.Name); isTimeIntelRangeFunc(name) {
			tf, err := e.parseTimeIntel(fc)
			if err != nil {
				return err
			}
			// A designated date table gets full "mark as date table" semantics:
			// all its filters are cleared. Otherwise only the date column's
			// filter is replaced.
			if e.isDesignatedDateTable(tf.table) {
				cm.removeTables[e.tableKey(tf.table)] = true
			} else {
				cm.removeCols[e.resolvedColKey(tf.table, tf.col)] = true
			}
			cm.timeFilters = append(cm.timeFilters, tf)
			return nil
		}
	}
	// Anything else is a plain predicate (including TREATAS calls, which emit
	// as IN predicates).
	cm.preds = append(cm.preds, arg)
	return nil
}

// classifyAllArgs records the removals declared by ALL(...) / REMOVEFILTERS(...).
func (e *Emitter) classifyAllArgs(fc *parser.FuncCall, cm *calcModifiers) error {
	if len(fc.Args) == 0 {
		cm.removeAll = true
		return nil
	}
	for _, a := range fc.Args {
		t := a.Left
		if t == nil || len(a.Right) > 0 {
			return fmt.Errorf("%s: arguments must be a table or column references", strings.ToUpper(fc.Name))
		}
		switch {
		case t.ColRef != nil && t.ColRef.Table != "":
			tbl := semantic.StripSingleQuotes(t.ColRef.Table)
			cm.removeCols[e.colKey(tbl, semantic.StripBrackets(t.ColRef.Column))] = true
		case t.Ident != "":
			cm.removeTables[e.tableKey(t.Ident)] = true
		case t.QuotedIdent != "":
			cm.removeTables[e.tableKey(semantic.StripSingleQuotes(t.QuotedIdent))] = true
		case t.QualifiedIdent != "":
			cm.removeTables[e.tableKey(t.QualifiedIdent)] = true
		default:
			return fmt.Errorf("%s: argument must be a table or a table-qualified column reference", strings.ToUpper(fc.Name))
		}
	}
	return nil
}

// classifyAllExcept records ALLEXCEPT(T, T[C]...): clear all filters on T
// except those on the listed columns.
func (e *Emitter) classifyAllExcept(fc *parser.FuncCall, cm *calcModifiers) error {
	if len(fc.Args) < 2 {
		return fmt.Errorf("ALLEXCEPT requires a table and at least one column reference")
	}
	tbl, err := e.tableNameFromExpr(fc.Args[0])
	if err != nil {
		return fmt.Errorf("ALLEXCEPT: first argument must be a table reference: %w", err)
	}
	cm.removeTables[e.tableKey(tbl)] = true
	for _, a := range fc.Args[1:] {
		t := a.Left
		if t == nil || t.ColRef == nil || t.ColRef.Table == "" || len(a.Right) > 0 {
			return fmt.Errorf("ALLEXCEPT: arguments after the table must be table-qualified column references")
		}
		colTbl := semantic.StripSingleQuotes(t.ColRef.Table)
		cm.exceptCols[e.colKey(colTbl, semantic.StripBrackets(t.ColRef.Column))] = true
	}
	return nil
}

// exprIsAllFamily reports whether expr is a bare ALL / REMOVEFILTERS /
// ALLEXCEPT call.
func exprIsAllFamily(expr *parser.Expr) bool {
	if expr == nil || expr.Left == nil || len(expr.Right) > 0 || expr.Left.FuncCall == nil {
		return false
	}
	switch strings.ToUpper(expr.Left.FuncCall.Name) {
	case "ALL", "REMOVEFILTERS", "ALLEXCEPT":
		return true
	}
	return false
}

// predColKeys returns the colKey set of every table-qualified column reference
// inside preds. A plain predicate on a column overrides (replaces) any group
// or outer filter on that same column — DAX's CALCULATE shorthand semantics.
func (e *Emitter) predColKeys(preds []*parser.Expr) map[string]bool {
	keys := map[string]bool{}
	for _, p := range preds {
		for _, cr := range collectColRefs(p) {
			tbl := semantic.StripSingleQuotes(cr.Table)
			if tbl == "" {
				continue
			}
			keys[e.colKey(tbl, semantic.StripBrackets(cr.Column))] = true
		}
	}
	return keys
}

// anyPredTouchesGroupKey reports whether any plain predicate references one of
// the enclosing group-by columns (which forces the subquery path so the
// override can take effect).
func (e *Emitter) anyPredTouchesGroupKey(preds []*parser.Expr) bool {
	if e.groupCtx == nil || len(e.groupCtx.keys) == 0 {
		return false
	}
	predKeys := e.predColKeys(preds)
	for _, gk := range e.groupCtx.keys {
		if predKeys[e.resolvedColKey(gk.table, gk.col)] {
			return true
		}
	}
	return false
}

// contextModifying reports whether fc is a CALCULATE (or TOTAL*TD) call whose
// filter arguments modify the enclosing group filter context — removals, time
// intelligence, or predicates overriding a group key. Classification errors
// report false; emission surfaces them later with their usual messages.
func (e *Emitter) contextModifying(fc *parser.FuncCall) bool {
	cf := calcForm(fc)
	if cf == nil || len(cf.Args) == 0 {
		return false
	}
	cm, err := e.classifyCalcArgs(cf.Args[1:])
	if err != nil {
		return false
	}
	return cm.hasRemovals() || e.anyPredTouchesGroupKey(cm.preds) ||
		!e.inlineFilterExpr(cf.Args[0]) || e.calcCrossesBidi(cf, cm)
}

// calcCrossesBidi reports whether a CALCULATE predicate reaches the value
// tables through a bidirectional relationship. Such predicates cannot use an
// aggregate FILTER over a flat join: bridge multiplicity would fan out the
// value rows. They are lifted into a context CTE where the bridge is carved
// into a correlated EXISTS semi-join.
func (e *Emitter) calcCrossesBidi(fc *parser.FuncCall, cm *calcModifiers) bool {
	if e.Schema == nil || len(fc.Args) == 0 {
		return false
	}
	valueTables := e.measureValueTables(fc.Args[0])
	if len(valueTables) == 0 {
		return false
	}
	for _, pred := range append(append([]*parser.Expr{}, cm.preds...), cm.keepPreds...) {
		for _, filterTable := range e.measureExprTables(pred) {
			if !semantic.FilterReaches(e.Schema, filterTable, valueTables) {
				continue
			}
			path, err := semantic.InferJoinPath(e.Schema, append(append([]string{}, valueTables...), filterTable))
			if err != nil {
				continue
			}
			for _, step := range path.Steps {
				if step.Bidirectional {
					return true
				}
			}
		}
	}
	return false
}

// inlineFilterExpr reports whether expr emits as one aggregate call that can
// legally receive SQL's FILTER clause. Stored measure references are unwrapped
// recursively; composed arithmetic and context-bearing calls are not inline.
func (e *Emitter) inlineFilterExpr(expr *parser.Expr) bool {
	visiting := map[*parser.MeasureDefinition]bool{}
	var check func(*parser.Expr) bool
	check = func(current *parser.Expr) bool {
		if current == nil || current.Left == nil || len(current.Right) > 0 || current.Left.Neg {
			return false
		}
		term := current.Left
		if term.SubExpr != nil {
			return check(term.SubExpr)
		}
		if term.ColRef != nil {
			def := e.resolveMeasureDef(term.ColRef)
			if def == nil || def.Expr == nil || visiting[def] {
				return false
			}
			visiting[def] = true
			ok := check(def.Expr)
			delete(visiting, def)
			return ok
		}
		if term.FuncCall != nil {
			switch strings.ToUpper(term.FuncCall.Name) {
			case "DISTINCTCOUNT", "COUNTBLANK":
				return false
			}
		}
		return term.FuncCall != nil && emitsInline(term.FuncCall)
	}
	return check(expr)
}

// emitCalculateGrouped emits CALCULATE inside a SUMMARIZECOLUMNS group context.
//
// Fast path — no modifier removes or overrides a group filter: the SQL
// aggregate FILTER syntax keeps the aggregate correlated with the outer
// GROUP BY for free:
//
//	SUM(amount) FILTER (WHERE qty > 2)
//
// Subquery path — some group filters are removed/overridden: a scalar
// subquery that re-joins the needed tables (aliased to avoid capture) and
// correlates back to the outer query only on the kept group keys:
//
//	(SELECT SUM(amount) FROM sales AS __cal_sales
//	 WHERE __cal_sales.region = sales.region)   -- ALL(sales[product])
func (e *Emitter) emitCalculateGrouped(fc *parser.FuncCall) (string, error) {
	cm, err := e.classifyCalcArgs(fc.Args[1:])
	if err != nil {
		return "", err
	}

	if !cm.hasRemovals() && !e.anyPredTouchesGroupKey(cm.preds) {
		return e.emitCalculateFastPath(fc.Args[0], cm)
	}

	overridden := e.predColKeys(cm.preds)

	// Group keys that survive the modifiers become correlation predicates.
	var keptKeys []groupKey
	for _, gk := range e.groupCtx.keys {
		if cm.removed(e.tableKey(gk.table), gk.col) || overridden[e.resolvedColKey(gk.table, gk.col)] {
			continue
		}
		keptKeys = append(keptKeys, gk)
	}

	// Outer WHERE predicates (TREATAS filters) that survive the modifiers are
	// replicated inside the subquery.
	var keptOuter []taggedPred
	for _, p := range e.groupCtx.preds {
		if p.table != "" && cm.removed(p.table, p.col) {
			continue
		}
		if p.table != "" && p.col != "" && overridden[p.table+"\x00"+p.col] {
			continue
		}
		keptOuter = append(keptOuter, p)
	}

	// Collect every table the subquery needs in its FROM clause.
	seen := map[string]bool{}
	var tables []string
	add := func(t string) {
		tl := strings.ToLower(t)
		if !seen[tl] {
			seen[tl] = true
			tables = append(tables, t)
		}
	}
	for _, t := range e.measureExprTables(fc.Args[0]) {
		add(t)
	}
	for _, p := range cm.preds {
		for _, t := range e.measureExprTables(p) {
			add(t)
		}
	}
	for _, p := range cm.keepPreds {
		for _, t := range e.measureExprTables(p) {
			add(t)
		}
	}
	for _, gk := range keptKeys {
		add(gk.table)
	}
	for _, p := range keptOuter {
		if p.table != "" {
			add(p.table)
		}
	}
	for _, tf := range cm.timeFilters {
		add(tf.table)
	}
	if len(tables) == 0 {
		return "", fmt.Errorf("CALCULATE: cannot determine a source table for the cleared filter context")
	}

	// FROM clause: every table aliased so correlation predicates can reference
	// the outer query's tables by their unaliased names.
	var from strings.Builder
	fmt.Fprintf(&from, "%s AS %s", e.sqlTable(tables[0]), calcAlias(tables[0]))
	if len(tables) > 1 {
		if e.Schema != nil {
			jp, jpErr := semantic.InferJoinPath(e.Schema, tables)
			if jpErr != nil {
				return "", jpErr
			}
			for _, step := range jp.Steps {
				fmt.Fprintf(&from, "\nLEFT JOIN %s AS %s ON %s.%s = %s.%s",
					e.sqlTable(step.Table), calcAlias(step.Table),
					calcAlias(step.FromTable), step.OnFromCol,
					calcAlias(step.Table), step.OnToCol,
				)
			}
		} else {
			for _, t := range tables[1:] {
				fmt.Fprintf(&from, ", %s AS %s", e.sqlTable(t), calcAlias(t))
			}
		}
	}
	bindings := make(map[string]string, len(tables))
	for _, table := range tables {
		bindings[e.tableKey(table)] = calcAlias(table)
	}
	popBindings := e.pushSQLBindings(bindings)
	defer popBindings()

	var conds []string
	for _, p := range keptOuter {
		if p.expr == nil {
			conds = append(conds, p.sql)
			continue
		}
		s, err := e.emitExpr(p.expr)
		if err != nil {
			return "", err
		}
		conds = append(conds, s)
	}
	for _, gk := range keptKeys {
		conds = append(conds, fmt.Sprintf("%s.%s = %s.%s",
			calcAlias(gk.table), gk.col, e.sqlTable(gk.table), gk.col))
	}
	for _, p := range cm.preds {
		s, err := e.emitExpr(p)
		if err != nil {
			return "", err
		}
		conds = append(conds, s)
	}
	for _, p := range cm.keepPreds {
		s, err := e.emitExpr(p)
		if err != nil {
			return "", err
		}
		conds = append(conds, s)
	}
	for _, tf := range cm.timeFilters {
		pred, err := e.emitTimeIntelPred(tf, calcAlias(tf.table)+"."+tf.col)
		if err != nil {
			return "", err
		}
		conds = append(conds, pred)
	}

	inner, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "(SELECT %s FROM %s", inner, from.String())
	if len(conds) > 0 {
		fmt.Fprintf(&sb, " WHERE %s", strings.Join(conds, " AND "))
	}
	sb.WriteString(")")
	return sb.String(), nil
}

// emitCalculateFastPath emits the aggregate FILTER (WHERE ...) form used when
// no group filter is removed or overridden.
func (e *Emitter) emitCalculateFastPath(innerExpr *parser.Expr, cm *calcModifiers) (string, error) {
	inner, err := e.emitExpr(innerExpr)
	if err != nil {
		return "", err
	}
	var filters []string
	for _, p := range append(append([]*parser.Expr{}, cm.preds...), cm.keepPreds...) {
		s, err := e.emitExpr(p)
		if err != nil {
			return "", err
		}
		filters = append(filters, s)
	}
	if len(filters) == 0 {
		return inner, nil
	}
	return fmt.Sprintf("%s FILTER (WHERE %s)", inner, strings.Join(filters, " AND ")), nil
}

// calcAlias returns the subquery alias for a table inside a cleared-context
// CALCULATE subquery (e.g. "analytics.Sales" → "__cal_analytics_sales").
func calcAlias(name string) string {
	return "__cal_" + sanitizeAliasSuffix(name)
}

// sanitizeAliasSuffix lowers name and replaces every non-alphanumeric rune
// with '_' so it can be embedded in a SQL alias.
func sanitizeAliasSuffix(name string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

// collectColRefs returns every table-qualified ColRef inside expr, traversing
// nested function-call arguments.
func collectColRefs(expr *parser.Expr) []*parser.ColRef {
	var result []*parser.ColRef
	walkTerms(expr, func(t *parser.Term) bool {
		if t.ColRef != nil && t.ColRef.Table != "" {
			result = append(result, t.ColRef)
		}
		return true
	})
	return result
}
