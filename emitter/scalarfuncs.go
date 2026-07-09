// Declarative DAX → DuckDB scalar function mapping.
//
// Most DAX scalar functions translate 1:1 (or nearly) to DuckDB built-ins.
// Functions whose DuckDB spelling is identical (ABS, ROUND, SQRT, UPPER, ...)
// flow through emitPassthrough untouched; this table covers the ones that
// need renaming, argument reshuffling, or semantic adjustment. Entries are
// consulted by emitFuncCall before falling back to passthrough.
package emitter

import (
	"fmt"
	"strings"

	"github.com/danielwikar/dux/parser"
)

// scalarFn describes one mapped scalar function. emit receives the
// already-emitted SQL of each argument.
type scalarFn struct {
	minArgs, maxArgs int
	emit             func(e *Emitter, fc *parser.FuncCall, args []string) (string, error)
}

// tmpl builds a scalarFn from a pattern with {1}, {2}, ... placeholders.
func tmpl(minArgs, maxArgs int, pattern string) scalarFn {
	return scalarFn{
		minArgs: minArgs,
		maxArgs: maxArgs,
		emit: func(_ *Emitter, _ *parser.FuncCall, args []string) (string, error) {
			out := pattern
			for i, a := range args {
				out = strings.ReplaceAll(out, fmt.Sprintf("{%d}", i+1), a)
			}
			return out, nil
		},
	}
}

var scalarFuncs = map[string]scalarFn{
	// ── Date / time ─────────────────────────────────────────────────────────
	"YEAR":      tmpl(1, 1, "year({1})"),
	"MONTH":     tmpl(1, 1, "month({1})"),
	"DAY":       tmpl(1, 1, "day({1})"),
	"HOUR":      tmpl(1, 1, "hour({1})"),
	"MINUTE":    tmpl(1, 1, "minute({1})"),
	"SECOND":    tmpl(1, 1, "second({1})"),
	"QUARTER":   tmpl(1, 1, "quarter({1})"),
	"WEEKNUM":   tmpl(1, 1, "weekofyear({1})"),
	"EOMONTH":   tmpl(2, 2, "last_day(CAST({1} AS DATE) + ({2}) * INTERVAL 1 MONTH)"),
	"EDATE":     tmpl(2, 2, "(CAST({1} AS DATE) + ({2}) * INTERVAL 1 MONTH)"),
	"TODAY":     tmpl(0, 0, "current_date"),
	"NOW":       tmpl(0, 0, "now()"),
	"DATEVALUE": tmpl(1, 1, "CAST({1} AS DATE)"),
	"TIME":      tmpl(3, 3, "make_time(CAST({1} AS INTEGER), CAST({2} AS INTEGER), CAST({3} AS DOUBLE))"),
	"WEEKDAY":   {minArgs: 1, maxArgs: 2, emit: emitWeekday},
	"DATEDIFF":  {minArgs: 3, maxArgs: 3, emit: emitDateDiff},

	// ── Math ────────────────────────────────────────────────────────────────
	"INT": tmpl(1, 1, "CAST(floor({1}) AS BIGINT)"),
	// Excel/DAX MOD: result carries the sign of the divisor.
	"MOD":       tmpl(2, 2, "((({1}) % ({2}) + ({2})) % ({2}))"),
	"POWER":     tmpl(2, 2, "pow({1}, {2})"),
	"ROUNDUP":   tmpl(2, 2, "(sign({1}) * ceil(abs({1}) * pow(10, ({2}))) / pow(10, ({2})))"),
	"ROUNDDOWN": tmpl(2, 2, "(sign({1}) * floor(abs({1}) * pow(10, ({2}))) / pow(10, ({2})))"),
	"TRUNC":     {minArgs: 1, maxArgs: 2, emit: emitTrunc},
	// DAX CEILING/FLOOR round to a multiple of the significance argument;
	// the plain 1-argument SQL form is accepted too.
	"CEILING": {minArgs: 1, maxArgs: 2, emit: emitCeilingFloor("ceil")},
	"FLOOR":   {minArgs: 1, maxArgs: 2, emit: emitCeilingFloor("floor")},
	"LOG":     {minArgs: 1, maxArgs: 2, emit: emitLog},

	// ── Text ────────────────────────────────────────────────────────────────
	"LEN":         tmpl(1, 1, "length({1})"),
	"MID":         tmpl(3, 3, "substr({1}, CAST({2} AS INTEGER), CAST({3} AS INTEGER))"),
	"VALUE":       tmpl(1, 1, "CAST({1} AS DOUBLE)"),
	"SEARCH":      tmpl(2, 2, "strpos(lower({2}), lower({1}))"),
	"FIND":        tmpl(2, 2, "strpos({2}, {1})"),
	"REPT":        tmpl(2, 2, "repeat({1}, CAST({2} AS INTEGER))"),
	"UNICHAR":     tmpl(1, 1, "chr(CAST({1} AS INTEGER))"),
	"EXACT":       tmpl(2, 2, "(({1}) = ({2}))"),
	"REPLACE":     tmpl(4, 4, "concat(substr({1}, 1, CAST({2} AS INTEGER) - 1), {4}, substr({1}, CAST({2} AS INTEGER) + CAST({3} AS INTEGER)))"),
	"SUBSTITUTE":  {minArgs: 3, maxArgs: 4, emit: emitSubstitute},
	"CONCATENATE": {minArgs: 2, maxArgs: 64, emit: emitConcatN},
	"FORMAT":      {minArgs: 2, maxArgs: 2, emit: emitFormat},

	// ── Logical ─────────────────────────────────────────────────────────────
	"TRUE":  tmpl(0, 0, "TRUE"),
	"FALSE": tmpl(0, 0, "FALSE"),
}

// emitScalarMapped validates arity, emits the arguments, and applies the
// mapping.
func (e *Emitter) emitScalarMapped(name string, fn scalarFn, fc *parser.FuncCall) (string, error) {
	if len(fc.Args) < fn.minArgs || len(fc.Args) > fn.maxArgs {
		if fn.minArgs == fn.maxArgs {
			return "", fmt.Errorf("%s requires exactly %d argument(s)", name, fn.minArgs)
		}
		return "", fmt.Errorf("%s requires between %d and %d arguments", name, fn.minArgs, fn.maxArgs)
	}
	args := make([]string, len(fc.Args))
	for i, a := range fc.Args {
		s, err := e.emitExpr(a)
		if err != nil {
			return "", err
		}
		args[i] = s
	}
	return fn.emit(e, fc, args)
}

// emitWeekday maps DAX WEEKDAY return types onto DuckDB day-of-week functions.
// Type 1 (default): Sunday=1..Saturday=7; type 2: Monday=1..Sunday=7;
// type 3: Monday=0..Sunday=6.
func emitWeekday(_ *Emitter, fc *parser.FuncCall, args []string) (string, error) {
	typ := 1.0
	if len(args) == 2 {
		v, ok := literalNumber(fc.Args[1])
		if !ok {
			return "", fmt.Errorf("WEEKDAY: return type must be a literal 1, 2, or 3")
		}
		typ = v
	}
	switch typ {
	case 1:
		return fmt.Sprintf("(dayofweek(%s) + 1)", args[0]), nil
	case 2:
		return fmt.Sprintf("isodow(%s)", args[0]), nil
	case 3:
		return fmt.Sprintf("(isodow(%s) - 1)", args[0]), nil
	}
	return "", fmt.Errorf("WEEKDAY: return type must be 1, 2, or 3")
}

// emitDateDiff maps DATEDIFF(start, end, interval) to date_diff.
func emitDateDiff(_ *Emitter, fc *parser.FuncCall, args []string) (string, error) {
	unitExpr := fc.Args[2]
	if unitExpr == nil || unitExpr.Left == nil || unitExpr.Left.Ident == "" {
		return "", fmt.Errorf("DATEDIFF: interval must be SECOND, MINUTE, HOUR, DAY, WEEK, MONTH, QUARTER, or YEAR")
	}
	unit := strings.ToLower(unitExpr.Left.Ident)
	switch unit {
	case "second", "minute", "hour", "day", "week", "month", "quarter", "year":
		return fmt.Sprintf("date_diff('%s', %s, %s)", unit, args[0], args[1]), nil
	}
	return "", fmt.Errorf("DATEDIFF: unsupported interval %q", unitExpr.Left.Ident)
}

// emitTrunc emits TRUNC(x[, digits]) — truncation toward zero.
func emitTrunc(_ *Emitter, _ *parser.FuncCall, args []string) (string, error) {
	digits := "0"
	if len(args) == 2 {
		digits = args[1]
	}
	return fmt.Sprintf("(sign(%s) * floor(abs(%s) * pow(10, (%s))) / pow(10, (%s)))",
		args[0], args[0], digits, digits), nil
}

// emitCeilingFloor emits DAX CEILING/FLOOR (x rounded to a multiple of the
// significance) with a plain SQL fallback for the 1-argument form.
func emitCeilingFloor(duck string) func(*Emitter, *parser.FuncCall, []string) (string, error) {
	return func(_ *Emitter, _ *parser.FuncCall, args []string) (string, error) {
		if len(args) == 1 {
			return fmt.Sprintf("%s(%s)", duck, args[0]), nil
		}
		return fmt.Sprintf("(%s((%s) / (%s)) * (%s))", duck, args[0], args[1], args[1]), nil
	}
}

// emitLog emits LOG(x[, base]); DAX defaults the base to 10.
func emitLog(_ *Emitter, _ *parser.FuncCall, args []string) (string, error) {
	if len(args) == 1 {
		return fmt.Sprintf("log10(%s)", args[0]), nil
	}
	return fmt.Sprintf("(ln(%s) / ln(%s))", args[0], args[1]), nil
}

// emitSubstitute emits SUBSTITUTE(text, old, new); the positional 4th
// argument (replace only the n-th occurrence) is not supported.
func emitSubstitute(_ *Emitter, _ *parser.FuncCall, args []string) (string, error) {
	if len(args) == 4 {
		return "", fmt.Errorf("SUBSTITUTE: the instance argument is not supported")
	}
	return fmt.Sprintf("replace(%s, %s, %s)", args[0], args[1], args[2]), nil
}

// emitConcatN emits CONCATENATE as DuckDB's variadic concat.
func emitConcatN(_ *Emitter, _ *parser.FuncCall, args []string) (string, error) {
	return "concat(" + strings.Join(args, ", ") + ")", nil
}

// ─── FORMAT ──────────────────────────────────────────────────────────────────

// emitFormat translates a subset of DAX/VB format strings:
//   - named formats: "General Number", "Fixed", "Standard", "Percent", "Scientific"
//   - date patterns using yyyy/yy/MMMM/MMM/MM/M/dddd/ddd/dd/d/HH/hh/mm/ss → strftime
//   - numeric masks like "0.00" and "#,##0.00" → printf / format
//
// The format string must be a literal.
func emitFormat(e *Emitter, fc *parser.FuncCall, args []string) (string, error) {
	lit := fc.Args[1]
	if lit == nil || lit.Left == nil || lit.Left.Literal == nil || lit.Left.Literal.String == nil {
		return "", fmt.Errorf("FORMAT: the format argument must be a literal string")
	}
	raw := *lit.Left.Literal.String
	pattern := raw[1 : len(raw)-1] // strip surrounding double quotes

	switch strings.ToLower(pattern) {
	case "general number":
		return fmt.Sprintf("CAST(%s AS VARCHAR)", args[0]), nil
	case "fixed":
		return fmt.Sprintf("printf('%%.2f', CAST(%s AS DOUBLE))", args[0]), nil
	case "standard":
		return fmt.Sprintf("format('{:,.2f}', CAST(%s AS DOUBLE))", args[0]), nil
	case "percent":
		return fmt.Sprintf("printf('%%.2f%%%%', CAST(%s AS DOUBLE) * 100)", args[0]), nil
	case "scientific":
		return fmt.Sprintf("printf('%%.2e', CAST(%s AS DOUBLE))", args[0]), nil
	}

	if strings.ContainsAny(pattern, "yMdHhs") {
		return fmt.Sprintf("strftime(%s, '%s')", args[0], escapeSQLString(translateDateFormat(pattern))), nil
	}
	if strings.ContainsAny(pattern, "0#") {
		decimals := 0
		if dot := strings.IndexByte(pattern, '.'); dot >= 0 {
			decimals = len(pattern) - dot - 1
		}
		if strings.Contains(pattern, ",") {
			return fmt.Sprintf("format('{:,.%df}', CAST(%s AS DOUBLE))", decimals, args[0]), nil
		}
		return fmt.Sprintf("printf('%%.%df', CAST(%s AS DOUBLE))", decimals, args[0]), nil
	}
	return "", fmt.Errorf("FORMAT: unsupported format string %q", pattern)
}

// formatDateTokens maps DAX/VB date tokens to strftime, longest first.
var formatDateTokens = []struct{ dax, strf string }{
	{"yyyy", "%Y"}, {"yy", "%y"},
	{"MMMM", "%B"}, {"MMM", "%b"}, {"MM", "%m"}, {"M", "%-m"},
	{"dddd", "%A"}, {"ddd", "%a"}, {"dd", "%d"}, {"d", "%-d"},
	{"HH", "%H"}, {"hh", "%I"},
	{"mm", "%M"}, {"ss", "%S"},
}

// translateDateFormat rewrites a DAX date pattern into a strftime pattern,
// scanning left to right and matching the longest token at each position so
// already-produced % sequences are never re-substituted.
func translateDateFormat(pattern string) string {
	var sb strings.Builder
	for i := 0; i < len(pattern); {
		matched := false
		for _, tok := range formatDateTokens {
			if strings.HasPrefix(pattern[i:], tok.dax) {
				sb.WriteString(tok.strf)
				i += len(tok.dax)
				matched = true
				break
			}
		}
		if !matched {
			sb.WriteByte(pattern[i])
			i++
		}
	}
	return sb.String()
}

// escapeSQLString escapes single quotes for embedding in a SQL literal.
func escapeSQLString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
