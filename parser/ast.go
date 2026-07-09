// Package parser defines the DUX AST node types and the participle-annotated grammar.
package parser

// Query is the root node of a DUX program.
type Query struct {
	// Defines holds zero or more MEASURE declarations from a DEFINE block.
	Defines []*MeasureDefinition `parser:"( 'DEFINE' @@+ )?"`
	// Evaluate is the body of the EVALUATE clause, optionally containing VAR bindings.
	Evaluate *EvaluateClause `parser:"'EVALUATE' @@"`
}

// MeasureDefinition represents a single MEASURE declaration inside a DEFINE block.
//
//	MEASURE Sales[Total Revenue] = SUMX(Sales, Sales[Quantity] * Sales[UnitPrice])
//	MEASURE 'Order Lines'[Amount] = SUM('Order Lines'[Amount])
//	MEASURE atp.matches[Total] = COUNT(atp.matches[match_num])
type MeasureDefinition struct {
	// Table is the table name that owns this measure. For tables with spaces it
	// is stored with surrounding single quotes (e.g. "'Order Lines'"); use
	// semantic.StripSingleQuotes to obtain the bare name. For db-qualified tables
	// it is stored as "db.table" (e.g. "atp.matches").
	Table string `parser:"'MEASURE' @( QualifiedIdent | Ident | QuotedIdent )"`
	// Column is the raw ColRef token (e.g. "[Total Revenue]"). Brackets are
	// stripped by the semantic resolver.
	Column string `parser:"@ColRef '='"`
	Expr   *Expr  `parser:"@@"`
	// Expression holds the raw DUX expression string as entered by the user
	// (e.g. "COUNT(matches[match_num])"). Populated at load time; not parsed.
	Expression string `parser:""`
}

// TableExpr is a table-returning expression. It is either a function call
// (FILTER, SUMMARIZECOLUMNS, ADDCOLUMNS, etc.) or a bare table reference.
// Bare references may be a plain Ident (e.g. Sales), a QuotedIdent for
// table names that contain spaces (e.g. 'Order Lines'), or a QualifiedIdent
// for db-qualified names (e.g. atp.matches).
type TableExpr struct {
	Func           *FuncCall `parser:"  @@"`
	QualifiedTable string    `parser:"| @QualifiedIdent"`
	QuotedTable    string    `parser:"| @QuotedIdent"`
	Table          string    `parser:"| @Ident"`
}

// Expr is a binary expression. Operator precedence is not encoded in the
// grammar — it must be resolved during semantic analysis or emitted
// with explicit parenthesisation.
type Expr struct {
	Left  *Term     `parser:"@@"`
	Right []*OpExpr `parser:"@@*"`
}

// OpExpr is the right-hand side of a binary operation.
// Operators are either symbolic (Op tokens: +, -, *, /, =, <>, &&, || …) or
// the keyword forms AND / OR, which DAX uses as infix boolean operators.
type OpExpr struct {
	Op    string `parser:"( @Op | @( 'AND' | 'OR' ) )"`
	Right *Term  `parser:"@@"`
}

// TableConstructor is a DAX-style inline value set: { expr, expr, ... }.
// It is the first argument to TREATAS and enumerates a literal filter list.
//
//	{"Clay", "Grass"}
//	{100, 200, 300}
type TableConstructor struct {
	Values []*Expr `parser:"'{' ( @@ ( ',' @@ )* )? '}'"`
}

// Term is a single expression unit, optionally preceded by a unary minus
// (e.g. the -1 in DATEADD(Dates[Date], -1, YEAR)).
// Alternatives are tried in order:
// TableConstructor is tried first — '{' is unambiguous, no lookahead needed.
// FuncCall is tried next because it and a bare column ref both begin with
// an identifier token; UseLookahead(2) in the parser lets participle peek
// at the token after the name to decide which branch to commit to.
// QualifiedIdent (db.table) is tried before QuotedIdent and plain Ident.
// QuotedIdent handles single-quoted table names as bare arguments to table
// functions (e.g. FILTER('Order Lines', ...)).
// A bare Ident (no following '(' or ColRef token) is matched last.
type Term struct {
	Neg              bool              `parser:"@'-'? ("`
	TableConstructor *TableConstructor `parser:"  @@"`
	FuncCall         *FuncCall         `parser:"| @@"`
	ColRef           *ColRef           `parser:"| @@"`
	Literal          *Literal          `parser:"| @@"`
	SubExpr          *Expr             `parser:"| '(' @@ ')'"`
	QualifiedIdent   string            `parser:"| @QualifiedIdent"`
	QuotedIdent      string            `parser:"| @QuotedIdent"`
	Ident            string            `parser:"| @Ident )"`
}

// FuncCall is a named function invocation with zero or more arguments.
type FuncCall struct {
	// Name matches either a keyword (SUM, CALCULATE, …) or a user-defined
	// identifier to allow calling unknown/passthrough functions.
	Name string  `parser:"@( Keyword | Ident )"`
	Args []*Expr `parser:"'(' ( @@ ( ',' @@ )* )? ')'"`
}

// ColRef is a table-qualified or bare column reference.
//
//	Sales[Amount]          → Table="Sales",         Column="[Amount]"
//	'Order Lines'[Amount]  → Table="'Order Lines'",  Column="[Amount]"
//	atp.matches[Amount]    → Table="atp.matches",    Column="[Amount]"
//	[Amount]               → Table="",               Column="[Amount]"
//
// No dot separator is used between the table name and the column bracket.
// The surrounding brackets in Column are stripped by StripBrackets during
// semantic resolution and SQL emission. The surrounding single quotes in Table
// are stripped by StripSingleQuotes when used as a schema key or SQL identifier.
type ColRef struct {
	// Table is the optional table qualifier — a plain Ident, a single-quoted
	// QuotedIdent, or a dot-separated QualifiedIdent (db.table).
	Table  string `parser:"( @( QualifiedIdent | Ident | QuotedIdent ) )?"`
	Column string `parser:"@ColRef"`
}

// Literal is a scalar constant: a string, number, or boolean keyword.
//
// Note: BLANK() is intentionally omitted here because the parser would
// always match it as a FuncCall (Name="BLANK", Args=[]) before reaching
// the Literal alternatives. The emitter maps FuncCall{Name:"BLANK"} → NULL.
type Literal struct {
	String  *string  `parser:"  @String"`
	Number  *float64 `parser:"| @Number"`
	Boolean *string  `parser:"| @( 'TRUE' | 'FALSE' )"`
}

// EvaluateClause is the body following the EVALUATE keyword.
// It optionally contains zero or more VAR bindings followed by either a bare
// table expression or a RETURN keyword preceding the final table expression.
//
//	EVALUATE SUMMARIZECOLUMNS(...)          — no VARs, no RETURN
//	EVALUATE VAR x = FILTER(...) RETURN x   — one VAR, RETURN required
type EvaluateClause struct {
	Vars  []*VarBinding `parser:"@@*"`
	Table *TableExpr    `parser:"( 'RETURN' @@ | @@ )"`
}

// VarBinding is a single VAR declaration inside an EVALUATE clause.
//
//	VAR GrassMatches = FILTER(matches, matches[surface] = "Grass")
//
// The Name is the plain identifier used to reference this result in subsequent
// VARs or in the RETURN expression. The Expr is the table expression whose
// result is materialised (as a session temp table) before execution continues.
type VarBinding struct {
	Name string `parser:"'VAR' @Ident '='"`
	Expr *Expr  `parser:"@@"`
}
