package emitter

import (
	"fmt"
	"sort"
	"strings"

	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

type rowFrame struct {
	id    uint64
	alias string
	shape semantic.TableShape
}

type evalContext struct {
	rows               []rowFrame
	rowSeq             uint64
	transitioned       map[uint64]bool
	transitionDepth    int
	valueDepth         int
	forceValueSubquery int
	predicateOuter     map[string]bool
}

func (e *Emitter) tableShape(expr *parser.Expr) semantic.TableShape {
	if e.Resolution != nil {
		if shape, ok := e.Resolution.ExprShapes[expr]; ok {
			return shape
		}
	}
	name := bareTableArg(expr)
	if name == "" || e.Schema == nil {
		return semantic.TableShape{}
	}
	table, key := e.Schema.FindTable(name)
	if table == nil {
		return semantic.TableShape{}
	}
	names := make([]string, 0, len(table.Columns))
	for name := range table.Columns {
		names = append(names, name)
	}
	sort.Strings(names)
	shape := semantic.TableShape{Known: true}
	for _, name := range names {
		lineage := &semantic.ColumnKey{Table: key, Column: name}
		shape.Columns = append(shape.Columns, semantic.ShapeColumn{Output: name, Lineage: lineage})
	}
	return shape
}

func (e *Emitter) pushRow(expr *parser.Expr, alias string) func() {
	e.ctx.rowSeq++
	e.ctx.rows = append(e.ctx.rows, rowFrame{id: e.ctx.rowSeq, alias: alias, shape: e.tableShape(expr)})
	return func() { e.ctx.rows = e.ctx.rows[:len(e.ctx.rows)-1] }
}

func (e *Emitter) rowColumn(ref semantic.ResolvedRef) (string, bool) {
	for i := len(e.ctx.rows) - 1; i >= 0; i-- {
		for _, col := range e.ctx.rows[i].shape.Columns {
			if col.Lineage != nil && strings.EqualFold(col.Lineage.Table, ref.Table) && strings.EqualFold(col.Lineage.Column, ref.Column) {
				return e.ctx.rows[i].alias + "." + quoteIdent(col.Output), true
			}
		}
	}
	return "", false
}

func (e *Emitter) rowTableBound(table string) bool {
	_, ok := e.rowAliasForTable(table)
	return ok
}

func (e *Emitter) rowAliasForTable(table string) (string, bool) {
	for i := len(e.ctx.rows) - 1; i >= 0; i-- {
		frame := e.ctx.rows[i]
		for _, col := range frame.shape.Columns {
			if col.Lineage != nil && strings.EqualFold(col.Lineage.Table, table) {
				return frame.alias, true
			}
		}
	}
	return "", false
}

func (e *Emitter) withContextTransition(fn func() (string, error)) (string, error) {
	previousGroup := e.groupCtx
	keys := []groupKey{}
	if previousGroup != nil {
		keys = append(keys, previousGroup.keys...)
	}
	preds := []taggedPred{}
	if previousGroup != nil {
		preds = append(preds, previousGroup.preds...)
	}
	if e.ctx.transitioned == nil {
		e.ctx.transitioned = map[uint64]bool{}
	}
	marked := []uint64{}
	for _, frame := range e.ctx.rows {
		if e.ctx.transitioned[frame.id] {
			continue
		}
		if !frame.shape.Known {
			return "", &semantic.SemanticError{Message: "context transition requires a table expression with known column lineage"}
		}
		for _, col := range frame.shape.Columns {
			if col.Lineage == nil {
				continue
			}
			keys = append(keys, groupKey{
				table:   col.Lineage.Table,
				col:     col.Lineage.Column,
				expr:    frame.alias + "." + quoteIdent(col.Output),
				frameID: frame.id,
			})
		}
		if e.Schema != nil {
			expanded, err := e.expandedRowKeys(frame)
			if err != nil {
				return "", err
			}
			keys = append(keys, expanded...)
		}
		e.ctx.transitioned[frame.id] = true
		marked = append(marked, frame.id)
	}
	e.groupCtx = &groupContext{keys: keys, preds: preds}
	e.ctx.transitionDepth++
	defer func() {
		e.ctx.transitionDepth--
		e.groupCtx = previousGroup
		for _, id := range marked {
			delete(e.ctx.transitioned, id)
		}
	}()
	return fn()
}

func (e *Emitter) expandedRowKeys(frame rowFrame) ([]groupKey, error) {
	type expansion struct {
		table, keyColumn, keyExpr string
	}
	direct := map[string]string{}
	for _, col := range frame.shape.Columns {
		if col.Lineage != nil {
			direct[e.resolvedColKey(col.Lineage.Table, col.Lineage.Column)] = frame.alias + "." + quoteIdent(col.Output)
		}
	}
	queue := []expansion{}
	for _, rel := range e.Schema.Relationships {
		if expr := direct[e.resolvedColKey(rel.FromTable, rel.FromColumn)]; expr != "" {
			queue = append(queue, expansion{table: rel.ToTable, keyColumn: rel.ToColumn, keyExpr: expr})
		}
	}
	seen := map[string]bool{}
	var keys []groupKey
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		tableKey := e.tableKey(current.table)
		if seen[tableKey] {
			return nil, &semantic.SemanticError{Message: fmt.Sprintf("ambiguous expanded-row path to table %q", current.table)}
		}
		seen[tableKey] = true
		table, canonical := e.Schema.FindTable(current.table)
		if table == nil {
			continue
		}
		columns := make([]string, 0, len(table.Columns))
		for name := range table.Columns {
			columns = append(columns, name)
		}
		sort.Strings(columns)
		columnExpr := func(column string) string {
			if strings.EqualFold(column, current.keyColumn) {
				return current.keyExpr
			}
			alias := e.nextAlias("__expanded")
			return fmt.Sprintf("(SELECT %s.%s FROM %s AS %s WHERE %s.%s IS NOT DISTINCT FROM %s)",
				alias, column, e.sqlTable(canonical), alias, alias, current.keyColumn, current.keyExpr)
		}
		for _, column := range columns {
			keys = append(keys, groupKey{table: canonical, col: column, expr: columnExpr(column), frameID: frame.id})
		}
		for _, rel := range e.Schema.Relationships {
			if strings.EqualFold(rel.FromTable, canonical) {
				queue = append(queue, expansion{table: rel.ToTable, keyColumn: rel.ToColumn, keyExpr: columnExpr(rel.FromColumn)})
			}
		}
	}
	return keys, nil
}

func (e *Emitter) emitMeasureInContext(def *parser.MeasureDefinition) (string, error) {
	if def == nil || def.Expr == nil {
		return "", fmt.Errorf("measure has no expression")
	}
	if len(e.ctx.rows) == 0 {
		return e.emitExpr(def.Expr)
	}
	return e.withContextTransition(func() (string, error) {
		if e.ctx.valueDepth > 0 {
			return e.emitExpr(def.Expr)
		}
		if def.Expr.Left != nil && len(def.Expr.Right) == 0 && def.Expr.Left.FuncCall != nil {
			top := def.Expr.Left.FuncCall
			switch strings.ToUpper(top.Name) {
			case "CALCULATE", "TOTALYTD", "TOTALQTD", "TOTALMTD":
				return e.emitExpr(def.Expr)
			case "SUMX", "AVERAGEX", "COUNTX", "MINX", "MAXX", "CONCATENATEX":
				return e.emitExpr(def.Expr)
			case "COUNTROWS":
				if len(top.Args) == 1 && top.Args[0].Left != nil && top.Args[0].Left.FuncCall != nil {
					return e.emitExpr(def.Expr)
				}
			}
		}
		fake := &parser.FuncCall{Name: "CALCULATE", Args: []*parser.Expr{def.Expr}}
		return e.emitCalculateGrouped(fake)
	})
}

func (e *Emitter) emitAggregateValue(expr *parser.Expr) (string, error) {
	previous := e.groupCtx
	if e.groupCtx == nil {
		e.groupCtx = &groupContext{}
	}
	e.ctx.forceValueSubquery++
	defer func() {
		e.ctx.forceValueSubquery--
		e.groupCtx = previous
	}()
	return e.emitCalculateGrouped(&parser.FuncCall{Name: "CALCULATE", Args: []*parser.Expr{expr}})
}
