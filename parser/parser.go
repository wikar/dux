// Package parser constructs the DUX parser and exposes the Parse entry point.
package parser

import (
	"fmt"

	"github.com/alecthomas/participle/v2"
	duxlexer "github.com/danielwikar/dux/lexer"
)

// ParseError is a structured parse failure with an optional source position.
type ParseError struct {
	Pos     string // human-readable "file:line:col", may be empty
	Message string
}

func (e *ParseError) Error() string {
	if e.Pos != "" {
		return fmt.Sprintf("%s: %s", e.Pos, e.Message)
	}
	return e.Message
}

// duxParser is the compiled participle parser. Built once at init time.
var duxParser = participle.MustBuild[Query](
	// Use the DUX stateful lexer.
	participle.Lexer(duxlexer.Definition),
	// Drop whitespace and comment tokens before the parser sees them.
	participle.Elide("Whitespace", "LineComment", "BlockComment"),
	// Allow keywords to be written in any case (EVALUATE, evaluate, Evaluate…).
	participle.CaseInsensitive("Keyword"),
	// Look ahead 2 tokens so the grammar can distinguish FuncCall from a bare
	// Ident/ColRef when both alternatives start with the same token type.
	participle.UseLookahead(2),
)

// Parse parses a DUX query string and returns the root AST node.
// Syntax errors are returned as *ParseError.
func Parse(input string) (*Query, error) {
	q, err := duxParser.ParseString("", input)
	if err != nil {
		return nil, &ParseError{Message: err.Error()}
	}
	return q, nil
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
