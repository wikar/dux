---
name: dux-querying
description: Write and execute DUX queries against a duxd server or the dux CLI. DUX is a DAX-inspired analytical query language on DuckDB with SUMMARIZECOLUMNS, CALCULATE, filter context, iterators, and time intelligence. Use when the user asks to query a DUX/duxd semantic model, aggregate or analyze data with DUX, translate a question into DUX, or debug a DUX query error.
license: MIT
metadata:
  author: wikar
  project: dux
---

# Querying with DUX

DUX is an analytical query language with DAX-like syntax, executed against a
DuckDB-backed semantic model served by `duxd`. Named measures and
relationships declared in the model apply automatically to every query.

## Query anatomy

Every query has one `EVALUATE` returning a table. Optional: a `DEFINE` block
before it (reusable measures) and `ORDER BY` after it.

```dux
DEFINE
    MEASURE Sales[Avg Order Value] = AVERAGE(Sales[NetRevenue])

EVALUATE
    SUMMARIZECOLUMNS(
        Product[Category],
        "Avg Order Value", Sales[Avg Order Value]
    )
    ORDER BY [Avg Order Value] DESC, Product[Category]
```

References:
- Column: `table[column]`. DuckLake `main` tables use `Table`; non-default
  schemas use `schema.Table`. Views behave exactly like tables. Never add the
  internal `ducklake` catalog prefix to a DUX query.
- Stored measure: `table[Measure Name]` (looks like a column ref; the model
  resolves it).
- Output alias: `[Alias]` — valid in `ORDER BY` and as the `TOPN` key.
- `ORDER BY` sort keys: aliases as `[Alias]`, dims as `table[column]`;
  `DESC` optional per key.

## The core pattern

`SUMMARIZECOLUMNS(dims..., filters..., "Alias", expr...)` — group-by
aggregation. Argument order: dimension columns, then filter arguments, then
alias/expression pairs.

```dux
EVALUATE
    SUMMARIZECOLUMNS(
        Product[Category],
        TREATAS({"Water", "Soft Drinks"}, Product[Category]),
        "Orders",    COUNT(Sales[OrderId]),
        "Avg Value", AVERAGE(Sales[NetRevenue])
    )
```

Filter arguments:
- `TREATAS({v1, v2}, table[col])` — equality on a value set.
- `FILTER(table, predicate)` — arbitrary predicate,
  e.g. `FILTER(Sales, Sales[Quantity] >= 10)`.

Top-N (always highest-first by the key expression):

```dux
EVALUATE TOPN(5, SUMMARIZECOLUMNS(...), [Matches])
```

## Common recipes

Percent of total:

```dux
EVALUATE
    SUMMARIZECOLUMNS(
        Product[Category],
        "Net Revenue", SUM(Sales[NetRevenue]),
        "Share",   DIVIDE(
            SUM(Sales[NetRevenue]),
            CALCULATE(SUM(Sales[NetRevenue]), ALL(Product))
        )
    )
```

Filter then aggregate with a VAR:

```dux
EVALUATE
    VAR discounted = FILTER(Sales, Sales[DiscountRate] > 0)
    RETURN SUMMARIZECOLUMNS(
        discounted[VenueKey],
        "Orders", COUNT(discounted[OrderId])
    )
```

Whole-table dump (small tables only): `EVALUATE Product`.

For the complete function catalog (aggregations, iterators, table functions,
CALCULATE/filter-context semantics, time intelligence, scalar library), read
[references/functions.md](references/functions.md).

## Executing queries

Against a running duxd (default `http://localhost:8080`):

```
POST /query
Content-Type: text/plain

EVALUATE SUMMARIZECOLUMNS(...)
```

Response: `{"columns": ["Category", "Orders"], "rows": [["Water", 750851], ...]}`.
Row values are JSON scalars; NULL comes through as `null`.

`scripts/run-query.sh` wraps this (reads the query from a file or stdin).

To inject filters from outside the query text (slicer-style, without editing
the DUX), POST a JSON envelope instead — see
[references/external-filters.md](references/external-filters.md).

There is also a CLI: `dux query.dux` runs a file; bare `dux` opens a REPL
(finish a multi-line query with a blank line).

The running server documents its full HTTP API at `GET /openapi.json`
(OpenAPI 3.1; browsable Scalar UI at `/docs`) — consult it when this skill
and the server disagree; the server is newer.

## Errors and debugging

Query errors return HTTP 4xx/5xx JSON:
`{"error": "...", "line": 2, "column": 14, "stage": "parse|semantic|execute query"}`.
`line`/`column` are 1-based positions in the submitted query text (0 = no
position). Typical causes:

- Unknown column/table → check `GET /schema` for exact dot-qualified names;
  names are case-preserving.
- A measure referenced as `[Name]` outside ORDER BY/TOPN → use
  `table[Name]` in expressions.
- Aggregating without a group (no dims, bare `[Measure]` refs) → reference
  measures table-qualified: `SUMMARIZECOLUMNS("X", Sales[X])`.
- A filter that reaches none of the measure tables → error by design
  (filters must relate to what they filter).

Before writing queries against an unfamiliar model, always fetch
`GET /schema` — it returns tables, columns (with types), relationships, and
stored measures in one call.
