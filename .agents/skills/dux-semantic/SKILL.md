---
name: dux-semantic
description: Inspect and manage a DUX semantic model - tables, relationships (including bidirectional), named measures with display formats, hidden objects, and the designated date table - via the duxd HTTP API or dux.toml files. Use when the user asks to add or edit measures, define relationships between tables, hide columns, set up a date table, or export/import a DUX model.
license: MIT
metadata:
  author: wikar
  project: dux
---

# Managing the DUX semantic model

The semantic model is the layer that makes DUX queries work: relationships
route filter context between tables, named measures are reusable
expressions, and presentation metadata (formats, hidden flags, date table)
shapes how clients render results. Everything is persisted in the metadata
database (`db/dux.sqlite`) and takes effect immediately — no server restart.

## Database layout

`duxd` owns one DuckLake instance under `--db-dir` (default `db/`):

- `dux.sqlite` — DUX measures, relationships, presentation metadata, and operation history.
- `ducklake.sqlite` — DuckLake's catalog. Never edit it directly.
- `ducklake/` — DuckLake-owned Parquet data. Never edit or delete files directly.
- A DuckLake table in `main` is public as `Table`; another schema is `schema.Table`.
- DuckLake views behave exactly like tables. The internal `ducklake.schema.Table` name is never used in DUX model definitions.

DuckLake does not support primary or foreign keys. DUX relationships are semantic metadata persisted in `dux.sqlite`; they do not alter DuckLake tables.

## Start here: read the schema

```
GET /schema
```

One call returns tables, columns with types, relationships, measures (with
formats), hidden designations, and the date table. Always fetch it before
modifying a model — names must match exactly (dot-qualified, case
preserved).

The running server documents its full HTTP API at `GET /openapi.json`
(OpenAPI 3.1; browsable Scalar UI at `/docs`) — consult it when this skill
and the server disagree; the server is newer.

## Measures

A measure is a named DUX expression bound to a home table:

```
POST /measures
{"table": "Sales", "name": "Total Revenue",
 "expression": "SUM(Sales[NetRevenue])",
 "format": {"kind": "compact"}}
```

- The expression is any scalar DUX expression; it may reference other
  measures and span tables.
- `format` is optional display metadata (a structured enum, not a format
  string): `kind` ∈ `number` | `decimal` | `percent` | `currency` |
  `compact`; optional `decimals` (0–10); `currency` = ISO 4217 code,
  required for (and only valid with) the `currency` kind.
- `DELETE /measures/{table}/{name}` removes one. `GET /measures` lists all.
- Verify a new measure immediately with a query:
  `EVALUATE SUMMARIZECOLUMNS("X", Sales[Total Revenue])`.

## Relationships

```
POST /relationships
{"from_table": "Sales", "from_column": "ProductKey",
 "to_table": "Product", "to_column": "ProductKey"}
```

- Direction matters: `from` is the many side, `to` the one side. Filter
  context flows one side → many side (a filter on `players` reaches
  `matches`, not vice versa).
- `"bidirectional": true` lets context flow both ways — the bridge-table
  pattern. The server **rejects** schemas where a bidi edge makes two
  tables reachable via more than one path (ambiguous filter graph), at
  startup and at this endpoint.
- `DELETE /relationships` with the same body removes one.

## Presentation metadata

- **Hidden** — `POST /hidden {"table": "...", "column": "..."}` (column
  optional; omit to hide the whole table/view). `DELETE /hidden` with the
  same body unhides. Hidden objects stay fully queryable — the flag only
  affects UI presentation.
- **Date table** — `POST /datetable {"table": "dates", "column": "date"}`
  designates the model's one date table (`DELETE /datetable` clears it).
  On a designated table, time-intelligence functions clear all its filters
  before applying date ranges — required for YTD grouped by year/month.

## dux.toml round-trip

The whole model exports as one portable TOML file:

```
GET  /export          → dux.toml (measures, relationships, date table, hidden)
POST /import          ← dux.toml body; REPLACES all of the above
```

`scripts/export-model.sh` / `scripts/import-model.sh` wrap these. Import is
a full replace, not a merge — export first, edit, re-import. For the exact
TOML shapes see [references/dux-toml.md](references/dux-toml.md).

## Recommended workflow

1. `GET /schema` — learn exact table/column names and what already exists.
2. Make the change via the API (measure/relationship/hidden/datetable).
3. Verify with a real query through `POST /query` — e.g. after adding a
   relationship, group a fact measure by a dimension column and check the
   numbers split.
4. For bulk edits: `GET /export`, edit the TOML, `POST /import`.

Pitfalls:

- Measure names are unique per table and referenced as
  `table[Measure Name]` in queries.
- A relationship pair must have matching value types on both columns.
- Adding a bidirectional edge to a cyclic graph fails with an
  "ambiguous filter graph" error that prints both paths — resolve by
  removing one edge or making it unidirectional.
- `POST /import` replaces hidden/date-table designations too, not just
  measures and relationships.
