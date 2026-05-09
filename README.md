# DUX

An analytical query language and semantic modelling platform built on top of DuckDB. Syntax inspired by [DAX](https://learn.microsoft.com/en-us/dax/dax-overview) — column references, named measures, filter context, and iterator functions — without requiring a cube engine.

DUX is more than a query interpreter. It ships with:

- **A semantic model** — tables, relationships, and named measures are declared once in `dux.toml` or managed at runtime, and are automatically applied to every query.
- **An HTTP server (`duxd`)** — exposes a REST API for executing queries, inspecting the schema, and managing measures and relationships. Embeds an interactive query builder UI and Scalar API reference.
- **A CLI (`dux`)** — run one-off queries against the DUX semantic model directly from the terminal.

## Quick start with Docker

```sh
docker run -d -v /db:/app/db ghcr.io/wikar/dux:latest
```

Mount your database directory to `/app/db` — the container runs `duxd` and listens on port 8080.

## Requirements

- Go 1.25+
- A C compiler (required by `go-duckdb` via CGO)
- [Bun](https://bun.sh) (for building the UI)

### Installing a C compiler

**Windows** — install [MSYS2](https://www.msys2.org/), then run:
```sh
pacman -S mingw-w64-ucrt-x86_64-gcc
```

**macOS** — Xcode Command Line Tools include `clang`:
```sh
xcode-select --install
```

**Linux** — install GCC via your package manager:
```sh
# Debian / Ubuntu
sudo apt install build-essential

# Fedora / RHEL
sudo dnf install gcc
```

### Installing Bun

**Windows**
```sh
powershell -c "irm bun.sh/install.ps1 | iex"
```

**macOS / Linux**
```sh
curl -fsSL https://bun.sh/install | bash
```

## Build

Build the UI first — `duxd` embeds the compiled assets at Go build time:

```sh
cd ui
bun install
bun run build
cd ..
```

Then build the binaries:

```sh
go build ./cmd/dux     # CLI
go build ./cmd/duxd    # query server
```

## Project layout

```
db/                  Data and metadata databases
  dux.duckdb         Created automatically on first startup (measures, relationships)
  *.duckdb / *.db    Your data files — attached read-only
dux.toml             Portable export of measures and relationships
samples/             Example .dux queries
```

Both `dux` and `duxd` share the same database model: `db/dux.duckdb` is the read-write metadata store, and every other `*.duckdb` / `*.db` file in the directory is attached read-only. Tables inside an attachment are referenced with a dot-qualified name (e.g. `atp.matches`).

## CLI (`dux`)

Run a `.dux` file:

```sh
dux query.dux
```

Interactive REPL — enter a query over multiple lines, then press Enter on a blank line to run:

```sh
dux
```

**Flags**

| Flag | Default | Description |
|------|---------|-------------|
| `--db-dir` | `db` | Directory containing data and metadata databases |
| `--dux` | `<db-dir>/dux.duckdb` | Path to the metadata database |
| `--toml` | `dux.toml` | Load measures and relationships from a `dux.toml` file |
| `--export` | — | Write current schema to a `dux.toml` file and exit |
| `--import` | — | Import a `dux.toml` into the metadata DB and exit |

## Server (`duxd`)

Starts a long-running query server. Uses the same `db/` directory convention as the CLI.

```sh
duxd
```

Listens on `:8080`.

**Flags**

| Flag | Default | Description |
|------|---------|-------------|
| `--db-dir` | `db` | Directory containing data and metadata databases |
| `--dux` | `<db-dir>/dux.duckdb` | Path to the metadata database |
| `--toml` | `dux.toml` | Load measures and relationships from a `dux.toml` file |
| `--import` | — | Import a `dux.toml` into the metadata DB on startup |
| `--export` | — | Export current schema to a `dux.toml` file and exit |

## Multi-database queries

When `atp.duckdb` is present in `db/`, it is attached as `atp`. Tables inside it can be referenced with a dot-qualified name:

```dux
EVALUATE atp.matches
```

```dux
EVALUATE
    SUMMARIZECOLUMNS(
        atp.matches[surface],
        "Matches", COUNT(atp.matches[match_num])
    )
```

## HTTP API

### Query

```
POST /query
Content-Type: text/plain

EVALUATE SUMMARIZECOLUMNS(atp.matches[surface], "Matches", COUNT(atp.matches[match_num]))
```

```json
{
  "columns": ["surface", "Matches"],
  "rows": [
    ["Clay", 1247],
    ["Grass", 892],
    ["Hard", 3105]
  ]
}
```

### Schema

```
GET /schema
```

Returns tables, columns, relationships, and measures as JSON.

### Import / export

```
GET  /export          → dux.toml download of all measures and relationships
POST /import          ← dux.toml body; replaces all measures and relationships
```

### Measures

```
GET /measures
→ [{"table": "atp.matches", "name": "Total Matches", "expression": "COUNT(atp.matches[match_num])"}]

POST /measures
{"table": "atp.matches", "name": "Total Matches", "expression": "COUNT(atp.matches[match_num])"}
→ 201 Created

DELETE /measures/:table/:name
→ 204 No Content
```

### Relationships

```
GET /relationships
→ [{"from_table": "atp.matches", "from_column": "winner_id", "to_table": "atp.players", "to_column": "player_id"}]

POST /relationships
{"from_table": "atp.matches", "from_column": "winner_id", "to_table": "atp.players", "to_column": "player_id"}
→ 201 Created

DELETE /relationships
{"from_table": "atp.matches", "from_column": "winner_id", "to_table": "atp.players", "to_column": "player_id"}
→ 204 No Content
```

### Reference UI

```
GET /docs/*     Interactive API reference (Scalar)
GET /           Query builder UI
```

## Measures and relationships

All measures and relationships are persisted in `db/dux.duckdb` and are available immediately to every query without restarting the server. They can also be round-tripped as a portable `dux.toml` file:

```toml
[[relationship]]
from_table  = "atp.matches"
from_column = "winner_id"
to_table    = "atp.players"
to_column   = "player_id"

[[measure]]
table      = "atp.matches"
name       = "Total Matches"
expression = "COUNT(atp.matches[match_num])"

[[measure]]
table      = "atp.matches"
name       = "Avg Winner Age"
expression = "AVERAGE(atp.matches[winner_age])"
```

Export the current state, edit offline, re-import:

```sh
curl http://localhost/export > dux.toml
# ... edit dux.toml ...
curl -X POST http://localhost/import --data-binary @dux.toml
```

## Query syntax

Every query starts with `EVALUATE`. An optional `DEFINE` block declares reusable measures.

**Aggregate by a column:**

```dux
EVALUATE
    SUMMARIZECOLUMNS(
        atp.matches[surface],
        "Matches", COUNT(atp.matches[match_num])
    )
```

**Filter, then aggregate:**

```dux
EVALUATE
    VAR gs_finals = FILTER(
        atp.matches,
        atp.matches[round] = "F" AND atp.matches[tourney_level] = "G"
    )
    RETURN SUMMARIZECOLUMNS(
        gs_finals[winner_name],
        "Titles", COUNT(gs_finals[match_num])
    )
```

**Named measures:**

```dux
DEFINE
    MEASURE atp.matches[Avg Winner Age] =
        AVERAGE(atp.matches[winner_age])

EVALUATE
    SUMMARIZECOLUMNS(
        atp.matches[surface],
        "Avg Age", atp.matches[Avg Winner Age]
    )
```

See [`samples/`](samples/) for more examples.

## Supported functions

### Aggregation

| Function | Description |
|----------|-------------|
| `SUM(T[C])` | Sum of a column |
| `AVERAGE(T[C])` | Mean of a column |
| `COUNT(T[C])` | Count of non-blank values |
| `COUNTA(T[C])` | Alias for `COUNT` |
| `COUNTBLANK(T[C])` | Count of blank (NULL) values |
| `COUNTROWS(T)` | Row count of a table |
| `DISTINCTCOUNT(T[C])` | Count of distinct values |
| `MIN(T[C])` | Minimum value |
| `MAX(T[C])` | Maximum value |
| `MEDIAN(T[C])` | Median value |

### Iterator (row-context)

These evaluate an expression row-by-row over a table.

| Function | Description |
|----------|-------------|
| `SUMX(T, expr)` | Sum of `expr` over each row of `T` |
| `AVERAGEX(T, expr)` | Average of `expr` over each row of `T` |
| `COUNTX(T, expr)` | Count of non-blank `expr` values |
| `MINX(T, expr)` | Minimum of `expr` |
| `MAXX(T, expr)` | Maximum of `expr` |
| `CONCATENATEX(T, expr [, delim])` | Concatenate `expr` values with optional delimiter |

### Table functions

| Function | Description |
|----------|-------------|
| `SUMMARIZECOLUMNS(cols..., "Name", expr...)` | Group-by aggregation |
| `FILTER(T, predicate)` | Rows of `T` matching a predicate |
| `ADDCOLUMNS(T, "Name", expr...)` | Add computed columns to a table |
| `SELECTCOLUMNS(T, "Name", expr...)` | Project to named computed columns |
| `TOPN(n, T, expr)` | Top `n` rows of `T` ordered by `expr` descending |
| `UNION(T1, T2)` | Union of two tables (duplicates included) |
| `INTERSECT(T1, T2)` | Rows present in both tables |
| `EXCEPT(T1, T2)` | Rows in `T1` not in `T2` |
| `VALUES(T[C])` | Distinct values of a column as a table |
| `DISTINCT(T[C])` | Alias for `VALUES` |

### Filter context

| Function | Description |
|----------|-------------|
| `CALCULATE(expr, filters...)` | Evaluate `expr` with additional filter predicates |
| `TREATAS(source, T[C])` | Apply a set of values as a filter on `T[C]` |

### Scalar / logical

| Function | Description |
|----------|-------------|
| `DIVIDE(a, b)` | Null-safe division; returns NULL when `b` is 0 |
| `IF(cond, then [, else])` | Conditional expression |
| `SWITCH(expr, val, result... [, else])` | Multi-branch conditional |
| `AND(a, b)` | Logical AND (also usable as `&&` or the `AND` keyword) |
| `OR(a, b)` | Logical OR (also usable as `\|\|` or the `OR` keyword) |
| `NOT(expr)` | Logical negation |
| `ISBLANK(expr)` | TRUE when `expr` is NULL |
| `BLANK()` | NULL constant |