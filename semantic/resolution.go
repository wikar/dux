package semantic

import (
	"fmt"
	"sort"
	"strings"

	"github.com/danielwikar/dux/parser"
)

type RefKind uint8

const (
	RefColumn RefKind = iota + 1
	RefMeasure
	RefOutput
)

type ColumnKey struct {
	Table  string
	Column string
}

type ResolvedRef struct {
	Kind    RefKind
	Table   string
	Column  string
	Measure *parser.MeasureDefinition
}

type ShapeColumn struct {
	Output  string
	Lineage *ColumnKey
}

type TableShape struct {
	Known   bool
	Columns []ShapeColumn
}

type Resolution struct {
	Measures    map[string]map[string]*parser.MeasureDefinition
	Refs        map[*parser.ColRef]ResolvedRef
	ExprShapes  map[*parser.Expr]TableShape
	TableShapes map[*parser.TableExpr]TableShape
	VarShapes   map[string]TableShape
}

func (r *Resolver) initResolution() {
	r.result = &Resolution{
		Measures:    r.localMeasures,
		Refs:        map[*parser.ColRef]ResolvedRef{},
		ExprShapes:  map[*parser.Expr]TableShape{},
		TableShapes: map[*parser.TableExpr]TableShape{},
		VarShapes:   map[string]TableShape{},
	}
}

func (r *Resolver) Result() *Resolution { return r.result }

func (r *Resolver) modelTableShape(name string) TableShape {
	resolved := ResolveTable(r.Schema, StripSingleQuotes(name))
	table, key := r.Schema.FindTable(resolved)
	if table == nil {
		return TableShape{}
	}
	names := make([]string, 0, len(table.Columns))
	for name := range table.Columns {
		names = append(names, name)
	}
	sort.Strings(names)
	shape := TableShape{Known: true, Columns: make([]ShapeColumn, 0, len(names))}
	for _, name := range names {
		lineage := &ColumnKey{Table: key, Column: name}
		shape.Columns = append(shape.Columns, ShapeColumn{Output: name, Lineage: lineage})
	}
	return shape
}

func (r *Resolver) shapeExpr(expr *parser.Expr) (TableShape, error) {
	if expr == nil || expr.Left == nil || len(expr.Right) > 0 {
		return TableShape{}, nil
	}
	if shape, ok := r.result.ExprShapes[expr]; ok {
		return shape, nil
	}
	t := expr.Left
	var shape TableShape
	switch {
	case t.Ident != "":
		shape = r.result.VarShapes[strings.ToLower(t.Ident)]
		if !shape.Known {
			shape = r.modelTableShape(t.Ident)
		}
	case t.QuotedIdent != "":
		shape = r.modelTableShape(StripSingleQuotes(t.QuotedIdent))
	case t.QualifiedIdent != "":
		shape = r.modelTableShape(t.QualifiedIdent)
	case t.ColRef != nil:
		if ref, ok := r.result.Refs[t.ColRef]; ok && ref.Kind == RefColumn {
			lineage := &ColumnKey{Table: ref.Table, Column: ref.Column}
			shape = TableShape{Known: true, Columns: []ShapeColumn{{Output: ref.Column, Lineage: lineage}}}
		}
	case t.FuncCall != nil:
		var err error
		shape, err = r.shapeFunc(t.FuncCall)
		if err != nil {
			return TableShape{}, err
		}
	}
	r.result.ExprShapes[expr] = shape
	return shape, nil
}

func (r *Resolver) shapeFunc(fc *parser.FuncCall) (TableShape, error) {
	name := strings.ToUpper(fc.Name)
	input := func(index int) (TableShape, error) {
		if index >= len(fc.Args) {
			return TableShape{}, nil
		}
		return r.shapeExpr(fc.Args[index])
	}
	switch name {
	case "FILTER", "ALL", "ALLEXCEPT", "ADDCOLUMNS", "CALCULATETABLE":
		shape, err := input(0)
		if err != nil || name != "ADDCOLUMNS" || !shape.Known {
			return shape, err
		}
		for i := 1; i+1 < len(fc.Args); i += 2 {
			shape.Columns = append(shape.Columns, ShapeColumn{Output: stringLiteralValue(fc.Args[i])})
		}
		return shape, nil
	case "TOPN":
		return input(1)
	case "VALUES", "DISTINCT":
		return input(0)
	case "SELECTCOLUMNS":
		shape := TableShape{Known: true}
		for i := 1; i+1 < len(fc.Args); i += 2 {
			col := ShapeColumn{Output: stringLiteralValue(fc.Args[i])}
			value := fc.Args[i+1]
			if value != nil && value.Left != nil && value.Left.ColRef != nil && len(value.Right) == 0 {
				if ref, ok := r.result.Refs[value.Left.ColRef]; ok && ref.Kind == RefColumn {
					col.Lineage = &ColumnKey{Table: ref.Table, Column: ref.Column}
				}
			}
			shape.Columns = append(shape.Columns, col)
		}
		return shape, nil
	case "CROSSJOIN", "GENERATE", "GENERATEALL":
		shape := TableShape{Known: true}
		seen := map[string]bool{}
		for _, arg := range fc.Args {
			part, err := r.shapeExpr(arg)
			if err != nil || !part.Known {
				return TableShape{}, err
			}
			for _, col := range part.Columns {
				key := strings.ToLower(col.Output)
				if seen[key] {
					return TableShape{}, fmt.Errorf("%s: duplicate output column %q", name, col.Output)
				}
				seen[key] = true
				shape.Columns = append(shape.Columns, col)
			}
		}
		return shape, nil
	case "UNION", "INTERSECT", "EXCEPT":
		left, err := input(0)
		if err != nil || !left.Known || name != "UNION" {
			return left, err
		}
		right, err := input(1)
		if err != nil || !right.Known || len(left.Columns) != len(right.Columns) {
			return TableShape{}, err
		}
		for i := range left.Columns {
			if left.Columns[i].Lineage == nil || right.Columns[i].Lineage == nil || *left.Columns[i].Lineage != *right.Columns[i].Lineage {
				left.Columns[i].Lineage = nil
			}
		}
		return left, nil
	case "SUMMARIZECOLUMNS":
		shape := TableShape{Known: true}
		for i := 0; i < len(fc.Args); i++ {
			arg := fc.Args[i]
			if stringLiteralValue(arg) != "" {
				if i+1 < len(fc.Args) {
					shape.Columns = append(shape.Columns, ShapeColumn{Output: stringLiteralValue(arg)})
					i++
				}
				continue
			}
			if arg != nil && arg.Left != nil && arg.Left.ColRef != nil {
				if ref, ok := r.result.Refs[arg.Left.ColRef]; ok && ref.Kind == RefColumn {
					lineage := &ColumnKey{Table: ref.Table, Column: ref.Column}
					shape.Columns = append(shape.Columns, ShapeColumn{Output: ref.Column, Lineage: lineage})
				}
			}
		}
		return shape, nil
	case "ROW":
		shape := TableShape{Known: true}
		for i := 0; i+1 < len(fc.Args); i += 2 {
			shape.Columns = append(shape.Columns, ShapeColumn{Output: stringLiteralValue(fc.Args[i])})
		}
		return shape, nil
	}
	return TableShape{}, nil
}

func stringLiteralValue(expr *parser.Expr) string {
	if expr == nil || expr.Left == nil || expr.Left.Literal == nil || expr.Left.Literal.String == nil {
		return ""
	}
	raw := *expr.Left.Literal.String
	if len(raw) < 2 {
		return ""
	}
	return strings.ReplaceAll(raw[1:len(raw)-1], `""`, `"`)
}

func (r *Resolver) shapeTableExpr(expr *parser.TableExpr) (TableShape, error) {
	if expr == nil {
		return TableShape{}, nil
	}
	var shape TableShape
	var err error
	if expr.Func != nil {
		shape, err = r.shapeFunc(expr.Func)
	} else {
		name := expr.Table
		if expr.QualifiedTable != "" {
			name = expr.QualifiedTable
		} else if expr.QuotedTable != "" {
			name = StripSingleQuotes(expr.QuotedTable)
		}
		shape = r.result.VarShapes[strings.ToLower(name)]
		if !shape.Known {
			shape = r.modelTableShape(name)
		}
	}
	if err != nil {
		return TableShape{}, fmt.Errorf("resolve table shape: %w", err)
	}
	r.result.TableShapes[expr] = shape
	return shape, nil
}
