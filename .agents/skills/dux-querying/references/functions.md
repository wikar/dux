# DUX function reference

Complete catalog of functions supported by the DUX engine.

## Aggregation

| Function | Description |
|----------|-------------|
| `SUM(T[C])` | Sum of a column |
| `AVERAGE(T[C])` | Mean of a column |
| `COUNT(T[C])` | Count of non-blank values |
| `COUNTA(T[C])` | Alias for `COUNT` |
| `COUNTBLANK(T[C])` | Count of blank (NULL) values |
| `COUNTROWS(T)` | Row count of a table |
| `DISTINCTCOUNT(T[C])` | Count of distinct values |
| `MIN(T[C])` / `MAX(T[C])` | Minimum / maximum value |
| `MEDIAN(T[C])` | Median value |

## Iterators (row context)

Evaluate an expression row-by-row over a table.

| Function | Description |
|----------|-------------|
| `SUMX(T, expr)` | Sum of `expr` over each row of `T` |
| `AVERAGEX(T, expr)` | Average of `expr` |
| `COUNTX(T, expr)` | Count of non-blank `expr` values |
| `MINX(T, expr)` / `MAXX(T, expr)` | Min / max of `expr` |
| `CONCATENATEX(T, expr [, delim])` | Concatenate values with optional delimiter |

## Table functions

| Function | Description |
|----------|-------------|
| `SUMMARIZECOLUMNS(cols..., filters..., "Name", expr...)` | Group-by aggregation — the workhorse |
| `ROLLUPADDISSUBTOTAL(col, "IsSubtotal", ...)` | Group argument adding subtotal rows; the named boolean column is TRUE on subtotal rows (compiles to `GROUPING SETS`) |
| `ROLLUPGROUP(c1, c2, ...)` | Roll several columns up as one unit inside `ROLLUPADDISSUBTOTAL` |
| `FILTER(T, predicate)` | Rows of `T` matching a predicate |
| `ADDCOLUMNS(T, "Name", expr...)` | Add computed columns |
| `SELECTCOLUMNS(T, "Name", expr...)` | Project to named computed columns |
| `TOPN(n, T, expr)` | Top `n` rows ordered by `expr` **descending** (always) |
| `UNION(T1, T2)` | Union (duplicates included) |
| `INTERSECT(T1, T2)` / `EXCEPT(T1, T2)` | Set intersection / difference |
| `VALUES(T[C])` / `DISTINCT(T[C])` | Distinct values of a column as a table |
| `CROSSJOIN(T1, T2, ...)` | Cartesian product |
| `GENERATE(T1, T2)` | Evaluate `T2` per row of `T1` (lateral join; `T1` columns in scope) |
| `GENERATEALL(T1, T2)` | Like `GENERATE`, keeping `T1` rows with no matches |

Table arguments compose — any table function accepts a nested table
expression where a table name is expected.

## Filter context

| Function | Description |
|----------|-------------|
| `CALCULATE(expr, filters...)` | Evaluate `expr` under a modified filter context |
| `TREATAS(source, T[C])` | Apply a value set as a filter on `T[C]` |
| `ALL(T)` / `ALL(T[C]...)` | Remove filters; as a table expression, the unfiltered table / distinct values |
| `ALLEXCEPT(T, T[C]...)` | Remove all filters on `T` except the listed columns |
| `REMOVEFILTERS(...)` | Alias of `ALL(...)` inside CALCULATE |
| `KEEPFILTERS(pred)` | Intersect `pred` with the existing context instead of overriding |

Inside `CALCULATE`, a plain predicate on a column **replaces** any existing
filter on that column (DAX shorthand semantics) — wrap in `KEEPFILTERS` to
intersect instead.

## Time intelligence

Works best with a designated date table (see the dux-semantic skill).
Date ranges anchor to the dates visible in the current filter context.

| Function | Description |
|----------|-------------|
| `DATESYTD/QTD/MTD(D[c])` | Start of year/quarter/month → last date in context |
| `TOTALYTD/QTD/MTD(expr, D[c])` | Shorthand for `CALCULATE(expr, DATESYTD(D[c]))` |
| `SAMEPERIODLASTYEAR(D[c])` | Context's range shifted back one year |
| `DATEADD(D[c], n, YEAR\|QUARTER\|MONTH\|DAY)` | Range shifted by `n` intervals |
| `PREVIOUSYEAR/QUARTER/MONTH/DAY(D[c])` | Full period before the first date in context |
| `NEXTYEAR/QUARTER/MONTH/DAY(D[c])` | Full period after the last date in context |
| `DATESBETWEEN(D[c], start, end)` | Dates in `[start, end]`; either bound may be `BLANK()` |
| `DATESINPERIOD(D[c], start, n, interval)` | `n` intervals from `start` (negative = backwards) |
| `CALENDAR(start, end)` | Generated date table with a `Date` column |
| `CALENDARAUTO()` | Like `CALENDAR`, spanning whole years across every date column |
| `DATE(y, m, d)` | Date constructor |

`MAX/MIN/LASTDATE/FIRSTDATE(D[c])` and `TODAY()` are context-aware anchors in
the `start`/`end` positions of `DATESBETWEEN` / `DATESINPERIOD`.

On a **designated** date table, time-intelligence functions clear all filters
on the table before applying their range (DAX "mark as date table"
behaviour) — this makes YTD work when grouping by year/month columns. On an
undesignated table only the date column's own filter is replaced.

## Scalar / logical

| Function | Description |
|----------|-------------|
| `DIVIDE(a, b)` | Null-safe division (NULL when `b` = 0) |
| `IF(cond, then [, else])` | Conditional |
| `SWITCH(expr, val, result... [, else])` | Multi-branch conditional |
| `AND(a, b)` / `OR(a, b)` / `NOT(x)` | Logical (also `&&`, `\|\|`, keywords) |
| `ISBLANK(expr)` | TRUE when NULL |
| `BLANK()` / `TRUE()` / `FALSE()` | Constants |

## Relationship traversal

| Function | Description |
|----------|-------------|
| `RELATED(Dim[col])` | Fetch a one-side column for the current row (in `FILTER`, `ADDCOLUMNS`, iterators) |
| `RELATEDTABLE(Fact)` | Fact rows related to the current dimension row, e.g. `COUNTROWS(RELATEDTABLE(Sales))` |

## Scalar function library

DAX scalar functions translate to DuckDB built-ins — passed through when the
spelling matches (`ABS`, `ROUND`, `SQRT`, `UPPER`, `LOWER`, `TRIM`, `LEFT`,
`RIGHT`, `COALESCE`, …) or mapped when semantics differ:

- **Date/time** — `YEAR`, `MONTH`, `DAY`, `HOUR`, `MINUTE`, `SECOND`,
  `QUARTER`, `WEEKDAY` (types 1–3), `WEEKNUM`, `EOMONTH`, `EDATE`, `TODAY`,
  `NOW`, `DATE`, `TIME`, `DATEVALUE`, `DATEDIFF`
- **Math** — `INT`, `MOD` (Excel sign semantics), `POWER`, `ROUNDUP`,
  `ROUNDDOWN`, `TRUNC`, `CEILING`/`FLOOR` (significance form), `LOG`
  (default base 10)
- **Text** — `LEN`, `MID`, `SUBSTITUTE`, `REPLACE`, `CONCATENATE`,
  `SEARCH` (case-insensitive), `FIND`, `REPT`, `UNICHAR`, `EXACT`, `VALUE`,
  `FORMAT`

`FORMAT` supports named formats (`"Percent"`, `"Fixed"`, `"Standard"`,
`"Scientific"`, `"General Number"`), date patterns (`"yyyy-MM-dd"`,
`"MMM d"`, …), and numeric masks (`"0.00"`, `"#,##0.00"`); the format string
must be a literal.

## Multi-table semantics (what to expect)

When one query's measures reach more than one table, each table cluster is
evaluated in its own grouped CTE and results are stitched on the group keys
with FULL OUTER JOIN semantics:

- A group produced by only one measure shows the other measures as NULL
  (rows are never dropped, matching DAX).
- Filters propagate along relationship direction (one → many; both ways
  across bidirectional edges). A filter that cannot reach any measure's
  table is an error.
- Bidirectional (bridge) edges gate rows via `EXISTS` semi-joins, so
  many-to-many bridges never fan out measure rows.
