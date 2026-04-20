# DUX

An analytical query language that compiles to DuckDB SQL. Syntax inspired by [DAX](https://learn.microsoft.com/en-us/dax/dax-overview) — column references, named measures, filter context, and iterator functions — without requiring a cube engine.

## Requirements

- Go 1.25+
- A C compiler (required by `go-duckdb` via CGO)
- [Bun](https://bun.sh) (for building the UI)
- A DuckDB `.duckdb` file

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

## CLI

Run a `.dux` file:

```sh
dux --db ./data.duckdb query.dux
```

Interactive REPL — enter a query over multiple lines, then press Enter on a blank line to run:

```sh
dux --db ./data.duckdb
```

**Flags**

| Flag | Default | Description |
|------|---------|-------------|
| `--db` | _(required)_ | Path to the DuckDB file |
| `--measures` | `measures.dux` | Global measure definitions (optional) |

## Server (`duxd`)

```sh
duxd --db ./data.duckdb
```

Listens on `:80`.

| Endpoint | Description |
|----------|-------------|
| `POST /query` | Execute a DUX query, returns JSON |
| `GET /schema` | Tables, columns, and relationships |
| `GET /docs/*` | Interactive API reference |
| `GET /` | Query builder UI |

**Flags**

| Flag | Default | Description |
|------|---------|-------------|
| `--db` | _(required)_ | Path to the DuckDB file |
| `--schema` | `schema.dux.json` | Sidecar relationship declarations (optional) |
| `--measures` | `measures.dux` | Global measure definitions (optional) |

## Query syntax

Every query starts with `EVALUATE`. An optional `DEFINE` block declares reusable measures.

**Aggregate by a column:**

```dux
EVALUATE
    SUMMARIZECOLUMNS(
        matches[surface],
        "Matches", COUNT(matches[match_num])
    )
```

**Filter, then aggregate:**

```dux
EVALUATE
    VAR gs_finals = FILTER(
        matches,
        matches[round] = "F" AND matches[tourney_level] = "G"
    )
    RETURN SUMMARIZECOLUMNS(
        gs_finals[winner_name],
        "Titles", COUNT(gs_finals[match_num])
    )
```

**Named measures:**

```dux
DEFINE
    MEASURE matches[Avg Winner Age] =
        AVERAGE(matches[winner_age])

EVALUATE
    SUMMARIZECOLUMNS(
        matches[surface],
        "Avg Age", matches[Avg Winner Age]
    )
```

See [`samples/`](samples/) for more examples.

## Relationships

Join paths are inferred automatically from DuckDB foreign keys. For sources without declared foreign keys (Parquet, CSV), you can optionally add a `schema.dux.json` alongside the database file to declare them manually:

```json
{
  "relationships": [
    {
      "fromTable": "matches",
      "fromColumn": "winner_id",
      "toTable": "players",
      "toColumn": "player_id"
    }
  ]
}
```

## Global measures

Measures in `measures.dux` are available to every query without a `DEFINE` block:

```dux
DEFINE
    MEASURE matches[Total Matches] =
        COUNT(matches[match_num])

    MEASURE matches[Avg Winner Age] =
        AVERAGE(matches[winner_age])
```

## HTTP API

```
POST /query
Content-Type: text/plain

EVALUATE SUMMARIZECOLUMNS(matches[surface], "Matches", COUNT(matches[match_num]))
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