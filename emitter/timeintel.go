// Time-intelligence support: DATESYTD/QTD/MTD, TOTALYTD/QTD/MTD, DATEADD,
// SAMEPERIODLASTYEAR, PREVIOUS*/NEXT*, DATESBETWEEN, DATESINPERIOD, CALENDAR,
// and CALENDARAUTO.
//
// The range-family functions translate to a date-range predicate on the date
// column. The range is anchored to the dates visible in the CURRENT filter
// context: the anchor is a scalar subquery over the date table correlated on
// the enclosing group-by keys of that table (e.g. grouped by Dates[Year],
// Dates[Month] the anchor MAX is the last date of that month). Used inside
// CALCULATE they also clear existing filters on the date table — the whole
// table when it is a designated date table (DAX "mark as date table"), or just
// the date column otherwise — via the calcModifiers machinery in filterctx.go.
package emitter

import (
	"fmt"
	"sort"
	"strings"

	"github.com/danielwikar/dux/parser"
	"github.com/danielwikar/dux/semantic"
)

// timeIntelFilter is a parsed range-family time-intelligence call.
type timeIntelFilter struct {
	fn    string         // upper-cased function name (DATESYTD, DATEADD, ...)
	table string         // canonical date table name as written (quotes stripped)
	col   string         // resolved SQL date column name
	args  []*parser.Expr // arguments after the date column
}

// isTimeIntelRangeFunc reports whether name is a time-intelligence function
// that evaluates to a set of dates (usable as a CALCULATE filter argument or
// as a standalone table expression).
func isTimeIntelRangeFunc(name string) bool {
	switch name {
	case "DATESYTD", "DATESQTD", "DATESMTD",
		"SAMEPERIODLASTYEAR", "DATEADD",
		"PREVIOUSYEAR", "PREVIOUSQUARTER", "PREVIOUSMONTH", "PREVIOUSDAY",
		"NEXTYEAR", "NEXTQUARTER", "NEXTMONTH", "NEXTDAY",
		"DATESBETWEEN", "DATESINPERIOD":
		return true
	}
	return false
}

// parseTimeIntel validates and destructures a range-family call. The first
// argument must be a table-qualified column reference naming the date column.
func (e *Emitter) parseTimeIntel(fc *parser.FuncCall) (*timeIntelFilter, error) {
	name := strings.ToUpper(fc.Name)
	if len(fc.Args) < 1 {
		return nil, fmt.Errorf("%s requires a date column argument", name)
	}
	t := fc.Args[0].Left
	if t == nil || t.ColRef == nil || t.ColRef.Table == "" || len(fc.Args[0].Right) > 0 {
		return nil, fmt.Errorf("%s: first argument must be a table-qualified date column (e.g. Dates[Date])", name)
	}
	tbl := semantic.StripSingleQuotes(t.ColRef.Table)
	return &timeIntelFilter{
		fn:    name,
		table: tbl,
		col:   e.resolveColName(tbl, semantic.StripBrackets(t.ColRef.Column)),
		args:  fc.Args[1:],
	}, nil
}

// isDesignatedDateTable reports whether table is marked as a date table in the
// schema (DAX "mark as date table").
func (e *Emitter) isDesignatedDateTable(table string) bool {
	if e.Schema == nil {
		return false
	}
	_, ok := e.Schema.DateColumn(table)
	return ok
}

// timeAnchor returns a scalar subquery computing agg (MIN or MAX) of the date
// column over the CURRENT filter context of the date table: correlated on the
// enclosing group-by keys of that table plus any outer predicates on it. With
// no group context the anchor is the table-wide extreme.
func (e *Emitter) timeAnchor(agg, table, col string) string {
	alias := "__ti_" + sanitizeAliasSuffix(table)
	var conds []string
	if e.groupCtx != nil {
		for _, gk := range e.groupCtx.keys {
			if strings.EqualFold(gk.table, table) {
				conds = append(conds, fmt.Sprintf("%s.%s = %s.%s",
					alias, gk.col, e.sqlTable(gk.table), gk.col))
			}
		}
		for _, p := range e.groupCtx.preds {
			if p.table == strings.ToLower(table) {
				conds = append(conds, p.sql)
			}
		}
	}
	s := fmt.Sprintf("(SELECT %s(%s.%s) FROM %s AS %s", agg, alias, col, e.sqlTable(table), alias)
	if len(conds) > 0 {
		s += " WHERE " + strings.Join(conds, " AND ")
	}
	return s + ")"
}

// emitTimeIntelPred emits the date-range predicate for tf against target,
// the SQL reference of the date column in the enclosing FROM clause (aliased
// inside CALCULATE subqueries, table-qualified otherwise).
func (e *Emitter) emitTimeIntelPred(tf *timeIntelFilter, target string) (string, error) {
	amax := func() string { return e.timeAnchor("MAX", tf.table, tf.col) }
	amin := func() string { return e.timeAnchor("MIN", tf.table, tf.col) }

	switch tf.fn {
	case "DATESYTD", "DATESQTD", "DATESMTD":
		if len(tf.args) != 0 {
			return "", fmt.Errorf("%s: the optional year-end argument is not supported", tf.fn)
		}
		unit := map[string]string{"DATESYTD": "year", "DATESQTD": "quarter", "DATESMTD": "month"}[tf.fn]
		a := amax()
		return fmt.Sprintf("%s >= date_trunc('%s', %s) AND %s <= %s",
			target, unit, a, target, a), nil

	case "SAMEPERIODLASTYEAR":
		if len(tf.args) != 0 {
			return "", fmt.Errorf("SAMEPERIODLASTYEAR requires exactly 1 argument")
		}
		return fmt.Sprintf("%s >= %s - INTERVAL 1 YEAR AND %s <= %s - INTERVAL 1 YEAR",
			target, amin(), target, amax()), nil

	case "DATEADD":
		if len(tf.args) != 2 {
			return "", fmt.Errorf("DATEADD requires 3 arguments (dates, number, interval)")
		}
		n, err := e.emitExpr(tf.args[0])
		if err != nil {
			return "", err
		}
		unit, err := intervalUnit(tf.args[1], "DATEADD")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s >= %s + (%s) * INTERVAL 1 %s AND %s <= %s + (%s) * INTERVAL 1 %s",
			target, amin(), n, unit, target, amax(), n, unit), nil

	case "PREVIOUSYEAR", "PREVIOUSQUARTER", "PREVIOUSMONTH", "PREVIOUSDAY":
		if len(tf.args) != 0 {
			return "", fmt.Errorf("%s requires exactly 1 argument", tf.fn)
		}
		unit := strings.ToUpper(strings.TrimPrefix(tf.fn, "PREVIOUS"))
		base := fmt.Sprintf("date_trunc('%s', %s)", strings.ToLower(unit), amin())
		return fmt.Sprintf("%s >= %s - INTERVAL 1 %s AND %s < %s",
			target, base, unit, target, base), nil

	case "NEXTYEAR", "NEXTQUARTER", "NEXTMONTH", "NEXTDAY":
		if len(tf.args) != 0 {
			return "", fmt.Errorf("%s requires exactly 1 argument", tf.fn)
		}
		unit := strings.ToUpper(strings.TrimPrefix(tf.fn, "NEXT"))
		base := fmt.Sprintf("date_trunc('%s', %s) + INTERVAL 1 %s", strings.ToLower(unit), amax(), unit)
		return fmt.Sprintf("%s >= %s AND %s < %s + INTERVAL 1 %s",
			target, base, target, base, unit), nil

	case "DATESBETWEEN":
		if len(tf.args) != 2 {
			return "", fmt.Errorf("DATESBETWEEN requires 3 arguments (dates, start, end)")
		}
		var bounds []string
		if !isBlankExpr(tf.args[0]) {
			lo, err := e.emitDateBound(tf.args[0])
			if err != nil {
				return "", err
			}
			bounds = append(bounds, fmt.Sprintf("%s >= %s", target, lo))
		}
		if !isBlankExpr(tf.args[1]) {
			hi, err := e.emitDateBound(tf.args[1])
			if err != nil {
				return "", err
			}
			bounds = append(bounds, fmt.Sprintf("%s <= %s", target, hi))
		}
		if len(bounds) == 0 {
			return "", fmt.Errorf("DATESBETWEEN: start and end cannot both be BLANK")
		}
		return strings.Join(bounds, " AND "), nil

	case "DATESINPERIOD":
		if len(tf.args) != 3 {
			return "", fmt.Errorf("DATESINPERIOD requires 4 arguments (dates, start, number, interval)")
		}
		start, err := e.emitDateBound(tf.args[0])
		if err != nil {
			return "", err
		}
		n, ok := literalNumber(tf.args[1])
		if !ok {
			return "", fmt.Errorf("DATESINPERIOD: the number of intervals must be a numeric literal")
		}
		unit, err := intervalUnit(tf.args[2], "DATESINPERIOD")
		if err != nil {
			return "", err
		}
		if n < 0 {
			return fmt.Sprintf("%s > %s + (%g) * INTERVAL 1 %s AND %s <= %s",
				target, start, n, unit, target, start), nil
		}
		return fmt.Sprintf("%s >= %s AND %s < %s + (%g) * INTERVAL 1 %s",
			target, start, target, start, n, unit), nil
	}
	return "", fmt.Errorf("unsupported time-intelligence function %s", tf.fn)
}

// emitDateBound emits a scalar date expression used as a range bound.
// MAX / LASTDATE and MIN / FIRSTDATE over a date column become context-aware
// anchors; TODAY() becomes current_date; anything else emits as-is.
func (e *Emitter) emitDateBound(expr *parser.Expr) (string, error) {
	if expr != nil && expr.Left != nil && expr.Left.FuncCall != nil && len(expr.Right) == 0 {
		fc := expr.Left.FuncCall
		switch strings.ToUpper(fc.Name) {
		case "MAX", "LASTDATE", "MIN", "FIRSTDATE":
			if len(fc.Args) == 1 && fc.Args[0].Left != nil && fc.Args[0].Left.ColRef != nil &&
				fc.Args[0].Left.ColRef.Table != "" {
				cr := fc.Args[0].Left.ColRef
				tbl := semantic.StripSingleQuotes(cr.Table)
				col := e.resolveColName(tbl, semantic.StripBrackets(cr.Column))
				agg := "MAX"
				if strings.ToUpper(fc.Name) == "MIN" || strings.ToUpper(fc.Name) == "FIRSTDATE" {
					agg = "MIN"
				}
				return e.timeAnchor(agg, tbl, col), nil
			}
		case "TODAY":
			return "current_date", nil
		}
	}
	return e.emitExpr(expr)
}

// emitTimeIntelTable emits a range-family function as a standalone table
// expression: the distinct dates of the date column within the range.
func (e *Emitter) emitTimeIntelTable(fc *parser.FuncCall) (string, error) {
	tf, err := e.parseTimeIntel(fc)
	if err != nil {
		return "", err
	}
	pred, err := e.emitTimeIntelPred(tf, tf.col)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("(SELECT DISTINCT %s FROM %s WHERE %s)",
		tf.col, e.sqlTable(tf.table), pred), nil
}

// emitTotalPeriod rewrites TOTALYTD/TOTALQTD/TOTALMTD(expr, dates[, filters...])
// as CALCULATE(expr, DATES*TD(dates), filters...).
func (e *Emitter) emitTotalPeriod(fc *parser.FuncCall) (string, error) {
	name := strings.ToUpper(fc.Name)
	if len(fc.Args) < 2 {
		return "", fmt.Errorf("%s requires an expression and a date column", name)
	}
	datesFn := strings.Replace(name, "TOTAL", "DATES", 1)
	args := []*parser.Expr{
		fc.Args[0],
		{Left: &parser.Term{FuncCall: &parser.FuncCall{Name: datesFn, Args: fc.Args[1:2]}}},
	}
	args = append(args, fc.Args[2:]...)
	return e.emitCalculate(&parser.FuncCall{Name: "CALCULATE", Args: args})
}

// emitCalendar emits CALENDAR(start, end) as a generated date table with a
// single "Date" column.
func (e *Emitter) emitCalendar(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 2 {
		return "", fmt.Errorf("CALENDAR requires exactly 2 arguments (start, end)")
	}
	start, err := e.emitExpr(fc.Args[0])
	if err != nil {
		return "", err
	}
	end, err := e.emitExpr(fc.Args[1])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		`(SELECT CAST(generate_series AS DATE) AS "Date" FROM generate_series(CAST(%s AS DATE), CAST(%s AS DATE), INTERVAL 1 DAY))`,
		start, end), nil
}

// emitCalendarAuto emits CALENDARAUTO() by scanning the schema for DATE and
// TIMESTAMP columns and generating whole calendar years spanning their extremes.
func (e *Emitter) emitCalendarAuto(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 0 {
		return "", fmt.Errorf("CALENDARAUTO: the fiscal year-end month argument is not supported")
	}
	if e.Schema == nil {
		return "", fmt.Errorf("CALENDARAUTO requires a schema")
	}

	var scans []string
	tableKeys := make([]string, 0, len(e.Schema.Tables))
	for k := range e.Schema.Tables {
		tableKeys = append(tableKeys, k)
	}
	sort.Strings(tableKeys)
	for _, tk := range tableKeys {
		table := e.Schema.Tables[tk]
		colNames := make([]string, 0, len(table.Columns))
		for c := range table.Columns {
			colNames = append(colNames, c)
		}
		sort.Strings(colNames)
		for _, c := range colNames {
			dt := strings.ToUpper(table.Columns[c].DataType)
			if dt == "DATE" || strings.HasPrefix(dt, "TIMESTAMP") {
				scans = append(scans, fmt.Sprintf(
					"SELECT MIN(%s) AS mn, MAX(%s) AS mx FROM %s",
					table.Columns[c].Name, table.Columns[c].Name, e.sqlTable(tk)))
			}
		}
	}
	if len(scans) == 0 {
		return "", fmt.Errorf("CALENDARAUTO: no DATE or TIMESTAMP columns found in the schema")
	}

	union := strings.Join(scans, " UNION ALL ")
	lo := fmt.Sprintf("(SELECT make_date(CAST(year(MIN(mn)) AS INTEGER), 1, 1) FROM (%s))", union)
	hi := fmt.Sprintf("(SELECT make_date(CAST(year(MAX(mx)) AS INTEGER), 12, 31) FROM (%s))", union)
	return fmt.Sprintf(
		`(SELECT CAST(generate_series AS DATE) AS "Date" FROM generate_series(%s, %s, INTERVAL 1 DAY))`,
		lo, hi), nil
}

// emitDateCtor emits DATE(year, month, day) as make_date.
func (e *Emitter) emitDateCtor(fc *parser.FuncCall) (string, error) {
	if len(fc.Args) != 3 {
		return "", fmt.Errorf("DATE requires exactly 3 arguments (year, month, day)")
	}
	parts := make([]string, 3)
	for i, a := range fc.Args {
		s, err := e.emitExpr(a)
		if err != nil {
			return "", err
		}
		parts[i] = fmt.Sprintf("CAST(%s AS INTEGER)", s)
	}
	return fmt.Sprintf("make_date(%s, %s, %s)", parts[0], parts[1], parts[2]), nil
}

// intervalUnit extracts a YEAR/QUARTER/MONTH/DAY interval keyword argument.
func intervalUnit(expr *parser.Expr, fn string) (string, error) {
	if expr != nil && expr.Left != nil && expr.Left.Ident != "" && len(expr.Right) == 0 {
		u := strings.ToUpper(expr.Left.Ident)
		switch u {
		case "YEAR", "QUARTER", "MONTH", "DAY":
			return u, nil
		}
	}
	return "", fmt.Errorf("%s: interval must be YEAR, QUARTER, MONTH, or DAY", fn)
}

// literalNumber returns the numeric value of a bare (possibly negated) number
// literal expression.
func literalNumber(expr *parser.Expr) (float64, bool) {
	if expr == nil || expr.Left == nil || len(expr.Right) > 0 {
		return 0, false
	}
	t := expr.Left
	if t.Literal == nil || t.Literal.Number == nil {
		return 0, false
	}
	v := *t.Literal.Number
	if t.Neg {
		v = -v
	}
	return v, true
}

// isBlankExpr reports whether expr is a bare BLANK() call.
func isBlankExpr(expr *parser.Expr) bool {
	return expr != nil && expr.Left != nil && len(expr.Right) == 0 &&
		expr.Left.FuncCall != nil &&
		strings.ToUpper(expr.Left.FuncCall.Name) == "BLANK" &&
		len(expr.Left.FuncCall.Args) == 0
}
