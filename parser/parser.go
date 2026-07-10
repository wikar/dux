// Package parser constructs the DUX parser and exposes the Parse entry point.
package parser

import (
	"errors"

	"github.com/alecthomas/participle/v2"
)

// duxParser is the compiled participle parser. Built once at init time.
var duxParser = participle.MustBuild[Query](
	// Use the DUX stateful lexer (see lexer.go).
	participle.Lexer(lexerDefinition),
	// Drop whitespace and comment tokens before the parser sees them.
	participle.Elide("Whitespace", "LineComment", "BlockComment"),
	// Allow keywords to be written in any case (EVALUATE, evaluate, Evaluate…).
	participle.CaseInsensitive("Keyword"),
	// Look ahead 2 tokens so the grammar can distinguish FuncCall from a bare
	// Ident/ColRef when both alternatives start with the same token type.
	participle.UseLookahead(2),
)

// Parse parses a DUX query string and returns the root AST node.
func Parse(input string) (*Query, error) {
	return duxParser.ParseString("", input)
}

// ErrorDetails returns the 1-based source position and bare message of a
// parse error when it carries one (participle errors do).
func ErrorDetails(err error) (line, col int, msg string, ok bool) {
	var perr participle.Error
	if errors.As(err, &perr) {
		pos := perr.Position()
		return pos.Line, pos.Column, perr.Message(), true
	}
	return 0, 0, "", false
}

// ParseMeasures parses a standalone measures file that contains only a DEFINE
// block — typically a committed `measures.dux` file that seeds the global
// central measure store at startup.
//
// Because the grammar requires an EVALUATE clause, a stub `EVALUATE x` is
// unconditionally appended. Only the DEFINE declarations are returned;
// the EVALUATE node is discarded.
//
// An empty (or whitespace-only) input is valid and returns nil, nil.
func ParseMeasures(input string) ([]*MeasureDefinition, error) {
	q, err := Parse(input + "\nEVALUATE x")
	if err != nil {
		return nil, err
	}
	return q.Defines, nil
}
