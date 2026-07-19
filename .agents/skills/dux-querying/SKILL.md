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
    MEASURE atp.matches[Avg Winner Age] = AVERAGE(atp.matches[winner_age])

EVALUATE
    SUMMARIZECOLUMNS(
        atp.matches[surface],
        "Avg Age", atp.matches[Avg Winner Age]
    )
    ORDER BY [Avg Age] DESC, atp.matches[surface]
```

References:
- Column: `table[column]`. Table names are dot-qualified per attached
  database: `atp.matches`, and carry the schema when non-default:
  `db.schema.table`. Views behave exactly like tables.
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
        atp.matches[surface],
        TREATAS({"G", "M"}, atp.matches[tourney_level]),
        "Matches",  COUNT(atp.matches[match_num]),
        "Avg Age",  AVERAGE(atp.matches[loser_age])
    )
```

Filter arguments:
- `TREATAS({v1, v2}, table[col])` — equality on a value set.
- `FILTER(table, predicate)` — arbitrary predicate,
  e.g. `FILTER(atp.matches, atp.matches[draw_size] >= 32)`.

Top-N (always highest-first by the key expression):

```dux
EVALUATE TOPN(5, SUMMARIZECOLUMNS(...), [Matches])
```

## Common recipes

Percent of total:

```dux
EVALUATE
    SUMMARIZECOLUMNS(
        atp.matches[surface],
        "Matches", COUNT(atp.matches[match_num]),
        "Share",   DIVIDE(
            COUNT(atp.matches[match_num]),
            CALCULATE(COUNT(atp.matches[match_num]), ALL(atp.matches))
        )
    )
```

Filter then aggregate with a VAR:

```dux
EVALUATE
    VAR finals = FILTER(atp.matches, atp.matches[round] = "F")
    RETURN SUMMARIZECOLUMNS(
        finals[winner_name],
        "Titles", COUNT(finals[match_num])
    )
```

Whole-table dump (small tables only): `EVALUATE atp.matches`.

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

Response: `{"columns": ["surface", "Matches"], "rows": [["Clay", 1247], ...]}`.
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
  measures table-qualified: `SUMMARIZECOLUMNS("X", atp.matches[X])`.
- A filter that reaches none of the measure tables → error by design
  (filters must relate to what they filter).

Before writing queries against an unfamiliar model, always fetch
`GET /schema` — it returns tables, columns (with types), relationships, and
stored measures in one call.
