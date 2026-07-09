// ROLLUPADDISSUBTOTAL / ROLLUPGROUP support for SUMMARIZECOLUMNS.
//
// ROLLUPADDISSUBTOTAL(col1, "IsSub1"[, col2, "IsSub2", ...]) adds subtotal
// rows for its columns, hierarchically: the emitted SQL uses GROUPING SETS
// covering the detail level plus one set per rolled-up suffix. Each name adds
// a boolean indicator column emitted as GROUPING(col) = 1, which is what
// distinguishes a subtotal row from a genuine NULL group value.
// ROLLUPGROUP(c1, c2, ...) treats several columns as one rollup unit.
package emitter

import (
	"fmt"
	"strings"

	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// rollupElement is one rollup unit: a column (or ROLLUPGROUP of columns) plus
// the name of its subtotal-indicator column.
type rollupElement struct {
	cols    []string // emitted SQL of the grouped column(s)
	nameSQL string   // SQL string literal for the indicator column alias
}

// parseRollup destructures ROLLUPADDISSUBTOTAL's alternating column/name
// arguments. Each column argument may be a plain column reference or a
// ROLLUPGROUP(...) call; every parsed column is also registered as a group
// key so CALCULATE filter-context modifiers can clear it.
func (e *Emitter) parseRollup(fc *parser.FuncCall, groupKeys *[]groupKey) ([]rollupElement, error) {
	if len(fc.Args) == 0 || len(fc.Args)%2 != 0 {
		return nil, fmt.Errorf("ROLLUPADDISSUBTOTAL requires column and name pairs")
	}
	var elems []rollupElement
	for i := 0; i < len(fc.Args); i += 2 {
		colArg, nameArg := fc.Args[i], fc.Args[i+1]
		if !isStringLiteral(nameArg) {
			return nil, fmt.Errorf("ROLLUPADDISSUBTOTAL: argument %d must be a quoted subtotal column name", i+2)
		}
		nameSQL, err := e.emitExpr(nameArg)
		if err != nil {
			return nil, err
		}

		var colExprs []*parser.Expr
		if colArg.Left != nil && colArg.Left.FuncCall != nil &&
			strings.ToUpper(colArg.Left.FuncCall.Name) == "ROLLUPGROUP" && len(colArg.Right) == 0 {
			if len(colArg.Left.FuncCall.Args) == 0 {
				return nil, fmt.Errorf("ROLLUPGROUP requires at least 1 column reference")
			}
			colExprs = colArg.Left.FuncCall.Args
		} else {
			colExprs = []*parser.Expr{colArg}
		}

		elem := rollupElement{nameSQL: nameSQL}
		for _, ce := range colExprs {
			sql, err := e.emitExpr(ce)
			if err != nil {
				return nil, err
			}
			elem.cols = append(elem.cols, sql)
			if ce.Left != nil && ce.Left.ColRef != nil && ce.Left.ColRef.Table != "" && len(ce.Right) == 0 {
				tbl := semantic.StripSingleQuotes(ce.Left.ColRef.Table)
				*groupKeys = append(*groupKeys, groupKey{
					table: tbl,
					col:   e.resolveColName(tbl, semantic.StripBrackets(ce.Left.ColRef.Column)),
				})
			}
		}
		elems = append(elems, elem)
	}
	return elems, nil
}

// rollupSelectItems returns the SELECT-list entries for the rollup elements:
// each element's columns followed by its subtotal indicator.
func rollupSelectItems(elems []rollupElement) []string {
	var items []string
	for _, el := range elems {
		items = append(items, el.cols...)
		items = append(items, fmt.Sprintf("(GROUPING(%s) = 1) AS %s", el.cols[0], el.nameSQL))
	}
	return items
}

// rollupGroupingSets builds the GROUP BY GROUPING SETS clause body: the plain
// group columns combined with every prefix of the rollup elements, from the
// full detail level down to the plain columns alone (the grand total when
// there are none).
func rollupGroupingSets(plainCols []string, elems []rollupElement) string {
	var sets []string
	for k := len(elems); k >= 0; k-- {
		cur := append([]string{}, plainCols...)
		for i := 0; i < k; i++ {
			cur = append(cur, elems[i].cols...)
		}
		sets = append(sets, "("+strings.Join(cur, ", ")+")")
	}
	return "GROUPING SETS (" + strings.Join(sets, ", ") + ")"
}
