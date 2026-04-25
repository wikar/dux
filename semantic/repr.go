package semantic

// emitterRepr is a minimal AST → DUX source reconstructor used only for
// serialising measure expressions back to their string form (e.g. when
// exporting to dux.toml). It does NOT produce SQL — that is the emitter's job.
// It re-creates the original DUX syntax as closely as the AST allows.

import (
	"fmt"
	"strings"

	"github.com/danielwikar/dux/parser"
)

type emitterRepr struct{}

func (r *emitterRepr) expr(e *parser.Expr) string {
	if e == nil {
		return ""
	}
	s := r.term(e.Left)
	for _, op := range e.Right {
		s += " " + op.Op + " " + r.term(op.Right)
	}
	return s
}

func (r *emitterRepr) term(t *parser.Term) string {
	if t == nil {
		return ""
	}
	switch {
	case t.TableConstructor != nil:
		parts := make([]string, len(t.TableConstructor.Values))
		for i, v := range t.TableConstructor.Values {
			parts[i] = r.expr(v)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case t.FuncCall != nil:
		args := make([]string, len(t.FuncCall.Args))
		for i, a := range t.FuncCall.Args {
			args[i] = r.expr(a)
		}
		return t.FuncCall.Name + "(" + strings.Join(args, ", ") + ")"
	case t.ColRef != nil:
		if t.ColRef.Table != "" {
			return t.ColRef.Table + t.ColRef.Column
		}
		return t.ColRef.Column
	case t.Literal != nil:
		return r.literal(t.Literal)
	case t.SubExpr != nil:
		return "(" + r.expr(t.SubExpr) + ")"
	case t.QuotedIdent != "":
		return t.QuotedIdent
	case t.Ident != "":
		return t.Ident
	}
	return ""
}

func (r *emitterRepr) literal(l *parser.Literal) string {
	switch {
	case l.String != nil:
		return *l.String // already double-quoted from the lexer
	case l.Number != nil:
		return fmt.Sprintf("%g", *l.Number)
	case l.Boolean != nil:
		return strings.ToUpper(*l.Boolean)
	}
	return "BLANK()"
}
