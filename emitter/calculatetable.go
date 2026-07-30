package emitter

import (
	"fmt"
	"strings"

	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

func (e *Emitter) emitCalculateTable(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) == 0 {
		return "", functionError(fc, "CALCULATETABLE requires at least 1 argument")
	}
	if len(e.ctx.rows) > 0 && e.ctx.transitionDepth == 0 {
		return e.withContextTransition(func() (string, error) { return e.emitCalculateTable(fc) })
	}

	src, err := e.tableSourceFromExpr(fc.Args[0])
	if err != nil {
		return "", fmt.Errorf("CALCULATETABLE: first argument must be a table expression: %w", err)
	}
	if src.name == "" {
		if !e.tableShape(fc.Args[0]).Known {
			return "", functionError(fc, "CALCULATETABLE requires a table expression with known column lineage")
		}
		if len(fc.Args) > 1 {
			return "", functionError(fc, "CALCULATETABLE filters over a shape-changing table expression are not supported")
		}
		return normaliseToSelect(src.sql)
	}
	cm, err := e.classifyCalcArgs(fc.Args[1:])
	if err != nil {
		return "", err
	}
	valueTables := []string{src.name}
	overridden := e.calcPredColKeys(cm.preds, valueTables)
	keys := []groupKey{}
	outerPreds := []taggedPred{}
	transitionKeys := 0
	reachableTransitionKeys := 0
	if e.groupCtx != nil {
		for _, key := range e.groupCtx.keys {
			if cm.removed(e.tableKey(key.table), key.col) || overridden[e.resolvedColKey(key.table, key.col)] {
				continue
			}
			if key.frameID != 0 {
				transitionKeys++
			}
			if key.frameID != 0 && e.Schema != nil && !semantic.FilterReaches(e.Schema, key.table, []string{src.name}) {
				continue
			}
			if key.frameID != 0 {
				reachableTransitionKeys++
			}
			keys = append(keys, key)
		}
		for _, pred := range e.groupCtx.preds {
			if pred.table != "" && (cm.removed(pred.table, pred.col) || overridden[pred.table+"\x00"+pred.col]) {
				continue
			}
			outerPreds = append(outerPreds, pred)
		}
	}
	if transitionKeys > 0 && reachableTransitionKeys == 0 {
		return "", functionError(fc, fmt.Sprintf("CALCULATETABLE: row context is disconnected from table %q", src.name))
	}

	tables := []string{src.name}
	seen := map[string]bool{e.tableKey(src.name): true}
	add := func(table string) {
		key := e.tableKey(table)
		if !seen[key] {
			seen[key] = true
			tables = append(tables, table)
		}
	}
	for _, key := range keys {
		add(key.table)
	}
	for _, pred := range append(append([]*parser.Expr{}, cm.preds...), cm.keepPreds...) {
		for _, table := range e.measureExprTables(pred) {
			add(table)
		}
	}
	for _, filter := range cm.timeFilters {
		add(filter.table)
	}

	aliases := map[string]string{}
	aliasFor := func(table string) string {
		key := e.tableKey(table)
		if aliases[key] == "" {
			aliases[key] = e.nextAlias("__ct")
		}
		return aliases[key]
	}
	for _, table := range tables {
		aliasFor(table)
	}
	var from strings.Builder
	fmt.Fprintf(&from, "%s AS %s", e.sqlTable(tables[0]), aliases[e.tableKey(tables[0])])
	if len(tables) > 1 {
		path, err := semantic.InferJoinPath(e.Schema, tables)
		if err != nil {
			return "", err
		}
		for _, step := range path.Steps {
			fmt.Fprintf(&from, "\nLEFT JOIN %s AS %s ON %s.%s IS NOT DISTINCT FROM %s.%s",
				e.sqlTable(step.Table), aliasFor(step.Table),
				aliasFor(step.FromTable), step.OnFromCol,
				aliasFor(step.Table), step.OnToCol)
		}
	}
	popBindings := e.pushSQLBindings(aliases)
	defer popBindings()
	e.ctx.valueDepth++
	defer func() { e.ctx.valueDepth-- }()

	conds := []string{}
	for _, key := range keys {
		conds = append(conds, fmt.Sprintf("%s.%s IS NOT DISTINCT FROM %s",
			aliases[e.tableKey(key.table)], key.col, key.expr))
	}
	for _, pred := range outerPreds {
		if pred.expr == nil {
			conds = append(conds, pred.sql)
			continue
		}
		sql, err := e.emitExpr(pred.expr)
		if err != nil {
			return "", err
		}
		conds = append(conds, sql)
	}
	for _, pred := range append(append([]*parser.Expr{}, cm.preds...), cm.keepPreds...) {
		sql, err := e.emitCalcPredicate(pred, valueTables)
		if err != nil {
			return "", err
		}
		conds = append(conds, sql)
	}
	for _, filter := range cm.timeFilters {
		pred, err := e.emitTimeIntelPred(filter, aliases[e.tableKey(filter.table)]+"."+filter.col)
		if err != nil {
			return "", err
		}
		conds = append(conds, pred)
	}

	result := "SELECT " + aliases[e.tableKey(src.name)] + ".* FROM " + from.String()
	if len(conds) > 0 {
		result += " WHERE " + strings.Join(conds, " AND ")
	}
	return result, nil
}
