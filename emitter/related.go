// RELATED and RELATEDTABLE: relationship traversal in row context.
//
// RELATED(Dim[col]) fetches a column from the one-side of a many-to-one
// relationship for the current row of the many-side (e.g. the product
// category for a sales row). RELATEDTABLE(Fact) is the reverse: the fact rows
// related to the current dimension row. Both emit correlated subqueries that
// reference the enclosing row — via the active row-context alias inside
// iterators, or the table itself inside FILTER/ADDCOLUMNS.
package emitter

import (
	"fmt"
	"strings"

	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// emitRelated emits RELATED(Dim[col]) as a correlated scalar subquery.
func (e *Emitter) emitRelated(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 1 || fc.Args[0].Left == nil || fc.Args[0].Left.ColRef == nil ||
		fc.Args[0].Left.ColRef.Table == "" {
		return "", fmt.Errorf("RELATED requires a table-qualified column reference (e.g. Products[Category])")
	}
	cr := fc.Args[0].Left.ColRef
	dim := semantic.StripSingleQuotes(cr.Table)
	col := e.resolveColName(dim, semantic.StripBrackets(cr.Column))

	rel, err := e.findRelationship(func(r *semantic.Relationship) bool {
		return strings.EqualFold(r.ToTable, dim)
	}, "RELATED", dim)
	if err != nil {
		return "", err
	}

	outer := e.rowRef(rel.FromTable)
	alias := e.nextAlias("__rel")
	return fmt.Sprintf("(SELECT %s.%s FROM %s AS %s WHERE %s.%s = %s.%s)",
		alias, col, e.sqlTable(rel.ToTable), alias,
		alias, rel.ToColumn, outer, rel.FromColumn), nil
}

// emitRelatedTable emits RELATEDTABLE(Fact) as a correlated table subquery.
func (e *Emitter) emitRelatedTable(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 1 {
		return "", fmt.Errorf("RELATEDTABLE requires exactly 1 argument")
	}
	fact, err := e.tableNameFromExpr(fc.Args[0])
	if err != nil {
		return "", fmt.Errorf("RELATEDTABLE: argument must be a table reference: %w", err)
	}

	rel, err := e.findRelationship(func(r *semantic.Relationship) bool {
		return strings.EqualFold(r.FromTable, fact)
	}, "RELATEDTABLE", fact)
	if err != nil {
		return "", err
	}

	outer := e.rowRef(rel.ToTable)
	alias := e.nextAlias("__rel")
	return fmt.Sprintf("(SELECT * FROM %s AS %s WHERE %s.%s = %s.%s)",
		e.sqlTable(rel.FromTable), alias,
		alias, rel.FromColumn, outer, rel.ToColumn), nil
}

// findRelationship returns the relationship matching the predicate. When
// several match, the one whose other side has an active row-context binding
// wins; otherwise the choice must be unique.
func (e *Emitter) findRelationship(match func(*semantic.Relationship) bool, fn, table string) (*semantic.Relationship, error) {
	if e.Schema == nil {
		return nil, fmt.Errorf("%s requires a schema with relationships", fn)
	}
	var candidates []*semantic.Relationship
	for _, r := range e.Schema.Relationships {
		if match(r) {
			candidates = append(candidates, r)
		}
	}
	switch len(candidates) {
	case 0:
		return nil, fmt.Errorf("%s: no relationship involving table %q", fn, table)
	case 1:
		return candidates[0], nil
	}
	// Disambiguate using the active row context.
	for _, r := range candidates {
		if _, ok := e.rowCtx.ResolveAlias(r.FromTable); ok {
			return r, nil
		}
		if _, ok := e.rowCtx.ResolveAlias(r.ToTable); ok {
			return r, nil
		}
	}
	return nil, fmt.Errorf("%s: table %q participates in multiple relationships; cannot disambiguate", fn, table)
}

// rowRef returns the SQL reference for the current row of table: its
// row-context alias inside iterators, or the table identifier itself when the
// enclosing FROM clause exposes it directly (FILTER, ADDCOLUMNS, GENERATE).
func (e *Emitter) rowRef(table string) string {
	if alias, ok := e.rowCtx.ResolveAlias(table); ok {
		return alias
	}
	return e.sqlTable(table)
}
