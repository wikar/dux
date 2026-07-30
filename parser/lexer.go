package parser

import (
	participelexer "github.com/alecthomas/participle/v2/lexer"
)

// lexerDefinition is the stateful lexer for the DUX query language.
// Rules are evaluated in order; first match wins.
var lexerDefinition = participelexer.MustStateful(participelexer.Rules{
	"Root": {
		// Whitespace is listed first so the stateful lexer matches it before any
		// other rule; token priority is determined by order of appearance.
		{Name: "Whitespace", Pattern: `\s+`},
		// Keywords must be tried before Ident so that e.g. "SUM" is a Keyword,
		// not an Ident. The (?i) flag makes the pattern case-insensitive.
		{Name: "Keyword", Pattern: `(?i)\b(DEFINE|EVALUATE|MEASURE|VAR|RETURN|TRUE|FALSE|AND|OR|ORDER|BY|START|AT|ASC|DESC)\b`},
		// QualifiedIdent matches a db-qualified table name with one or more
		// dot-separated segments (e.g. analytics.Sales, or analytics.sales.Customer for a
		// table in a non-default DuckDB schema). Must appear before Ident so
		// that the dots are consumed here and not treated as unknown Punct tokens.
		{Name: "QualifiedIdent", Pattern: `[a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*)+`},
		// Ident matches any unrecognised alphanumeric name (table names, user identifiers).
		{Name: "Ident", Pattern: `[a-zA-Z_][a-zA-Z0-9_]*`},
		// QuotedIdent matches a single-quoted table name, used when the table name
		// contains spaces: 'Order Lines'[Amount].  Single quotes are part of the token.
		{Name: "QuotedIdent", Pattern: `'(?:[^']|'')*'`},
		// ColRef matches [ColumnName] as a single token; brackets are stripped by the parser.
		{Name: "ColRef", Pattern: `\[(?:[^\]]|\]\])+\]`},
		// String matches a double-quoted string literal.
		{Name: "String", Pattern: `"(?:[^"]|"")*"`},
		// Number matches integer and decimal literals.
		{Name: "Number", Pattern: `[0-9]+(\.[0-9]+)?`},
		// LineComment matches a // comment to end of line.
		{Name: "LineComment", Pattern: `//[^\n]*`},
		// BlockComment matches a /* ... */ comment, including across newlines.
		{Name: "BlockComment", Pattern: `(?s)/\*.*?\*/`},
		// Op matches comparison and arithmetic operators.
		{Name: "Op", Pattern: `[+\-*/=<>!&|^]+`},
		// Punct matches structural punctuation used in the grammar, including
		// braces for the DAX-style table constructor { v1, v2, ... }.
		{Name: "Punct", Pattern: `[(),.{}]`},
	},
})
