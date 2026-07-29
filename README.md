# DUX

A **semantic stack** for analytics: durable lakehouse storage, a semantic model, a DAX-inspired query language, a server, and dashboards — one Go binary on top of DuckDB. Query syntax follows [DAX](https://learn.microsoft.com/en-us/dax/dax-overview) — column references, named measures, filter context, and iterator functions — without requiring a cube engine.

Each layer has one owner and one storage path:

| Layer | Component | Owns |
|-------|-----------|------|
| **Storage** | DuckLake | Durable analytical data as Parquet, with a transactional SQLite catalog and time-travel snapshots. DuckDB is the transient embedded engine that reads it. |
| **Semantics** | `dux.sqlite` / `dux.toml` | Relationships, named measures and their formats, the designated date table, and hidden objects — applied to every query without a restart. |
| **Language** | DUX | `parse → resolve → emit DuckDB SQL → execute`. Join inference, filter and row context, time intelligence, and multi-table stitched codegen. |
| **Service** | `duxd` | A REST API over query, schema, semantic model, and DuckLake operations, plus controlled Parquet imports, schema refresh, and scheduled maintenance. |
| **Presentation** | Dash | Canvas dashboards of DUX-backed visuals, stored as plain JSON files that diff, version, and deploy like code. |

Three clients sit on top: the embedded **web UI** (query builder, model Explorer, dashboard designer), the **`dux` CLI** for one-off queries, and **agent skills** — packaged instructions for AI agents (querying, modelling, dashboards, DuckLake operations) under [`.agents/skills/`](.agents/skills/), zipped onto every release.

```
    UI · dux CLI · agent skills · your HTTP client
                       │
                   duxd (REST)
      ┌────────────────┼────────────────┐
    Dash        DUX pipeline      DuckLake ops
  dashboards/    ↑ semantic model   imports · maintenance
   (JSON)          dux.sqlite            │
                                     DuckLake
                             ducklake.sqlite + Parquet
```

## Quick start with Docker

The published image is the fastest way to a running instance — no Go toolchain, no C compiler, no UI build.

**1. Run it.** One volume is all it needs:

```sh
docker run -d -p 8080:8080 -v dux-db:/app/db ghcr.io/wikar/dux:latest
```

**2. Open [http://localhost:8080](http://localhost:8080).** The query builder, model Explorer, and dashboard designer are all served from there.

**3. Put something in it.** A fresh instance is empty — [install the sample data](#sample-data) for 3,000,000 rows, a semantic model, and a dashboard in one command.

> **`duxd` has no authentication.** Every endpoint, including the mutating semantic-model, import, and maintenance ones, assumes a trusted network. Do not publish the port to an untrusted network.

### Volumes

```sh
docker run -d -p 8080:8080 \
  -v dux-db:/app/db \
  -v /local/inbox:/app/inbox \
  -v /local/dashboards:/app/dashboards \
  ghcr.io/wikar/dux:latest
```

| Path | Required | Contents |
|------|----------|----------|
| `/app/db` | **yes** | DUX-owned state: the DuckLake catalog and Parquet data plus `dux.sqlite`. Use a Docker named volume or a native local Linux path |
| `/app/inbox` | no | The controlled Parquet inbox — a *sibling* of `/app/db`, never a child, so the two mount separately. It may be a host bridge: pipelines publish completed files here and ask DUX to import them |
| `/app/dashboards` | no | Dashboard JSON and `theme.json`; mount it to persist them |

Version the mounted dashboards directory from the host or a dedicated Git sidecar — Git is not included in the DUX image. Set `DUX_DASH=0` if dashboards are not needed.

### Flags and the CLI

Flags append to the entrypoint, so any [`duxd` flag](#server-duxd) works directly:

```sh
docker run -d -p 8080:8080 -v dux-db:/app/db ghcr.io/wikar/dux:latest --max-connections 5
```

A container's command is fixed at creation, so changing flags means recreating
it (`docker rm -f` then `docker run`), not `docker restart`. The image also
ships the `dux` CLI: reach it with `--entrypoint ./dux`.

## Sample data

An empty instance is hard to explore. The **[sample data bundle](https://github.com/wikar/dux/releases/tag/dux-sample-v1)** fills one in a single command: 3,000,000 order lines of German beverage sales as a star schema, a semantic model of 23 formatted measures with time intelligence, and a 19-element demo dashboard.

Download `dux-sample-v1.zip` from the release and extract it anywhere — the bundle has no required location. It ships `install.sh` and `install.ps1`, which take the server URL, the controlled import inbox, and the dashboards root; they need filesystem access to the latter two, so run them on the machine hosting `duxd`.

**Against the Docker container above**, pointing at the host side of the mounts:

```sh
cd dux-sample && chmod +x install.sh && DUXD_URL=http://localhost:8080 DUX_IMPORT_DIR=/local/inbox DUX_DASH_DIR=/local/dashboards ./install.sh
```

**Against a source checkout**, extract into `samples/` and the defaults already resolve:

```sh
cd samples/dux-sample && chmod +x install.sh && ./install.sh
```

The installer imports the six Parquet tables through the [import API](#parquet-import-api), loads the model through `POST /import`, copies the dashboard asset into place, and restores the dashboard at [`/dash/sales/sales_test`](http://localhost:8080/dash/sales/sales_test). Install into an **empty** DuckLake instance — imports append, so a second run over existing tables duplicates every row.

The bundle is versioned independently of DUX and records which versions it was verified against; the release page carries the current compatibility record.

## Requirements

- Go 1.25+
- A C compiler (required by `go-duckdb` via CGO)
- [Bun](https://bun.sh) (for building the UI)
- DuckDB 1.5.4 with its matching DuckLake extension. DUX pins the tested official Go binding rather than following releases automatically. Future versions are adopted and pinned only after extension, concurrency, import, and container compatibility tests pass.

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
cd web
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
db/                  DUX-owned state
  dux.sqlite         Measures, relationships, and operation history
  ducklake.sqlite    DuckLake catalog
  ducklake/          DuckLake-managed Parquet files
inbox/               Inbox for controlled Parquet imports
dashboards/          Dashboard documents (one JSON file each) + theme.json
dux.toml             Portable export of the semantic model
samples/             Example .dux queries
  dux-sample/        Sample data bundle — extracted from a release, not tracked
.agents/skills/      Agent skills (packaged per release)
```

Source layout follows the pipeline stages:

```
parser/              Grammar and AST
semantic/            Schema, measures, relationships, resolution, join inference
emitter/             Resolved AST → DuckDB SQL
executor/            SQL execution and tabular results
internal/ducklake/   DuckLake owner/query runtimes, imports, maintenance
internal/bootstrap/  Shared CLI/server startup and schema refresh
dash/                CGO-free dashboard file store and HTTP handlers
web/core/            Framework-independent TS types, API client, query generation
web/app/             The React SPA (embedded into duxd at build time)
cmd/dux, cmd/duxd    CLI and server entry points
```

`duxd` creates and owns one DuckLake instance. Durable analytical data is Parquet, its transactional catalog is `ducklake.sqlite`, and DUX semantic metadata is isolated in `dux.sqlite`. DuckDB remains the transient embedded query engine; no native `.duckdb` database file is opened or watched.

## CLI (`dux`)

Run a `.dux` file:

```sh
dux query.dux
```

Interactive REPL — enter a query over multiple lines, then press Enter on a blank line to run:

```sh
dux
```

The CLI is a read-only DuckLake client. Start `duxd` once to initialize an empty DuckLake instance before using `dux`.

**Flags**

| Flag | Default | Description |
|------|---------|-------------|
| `--db-dir` | `db` | DUX state directory |
| `--dux` | `<db-dir>/dux.sqlite` | DUX semantic metadata SQLite path |
| `--ducklake-catalog` | `<db-dir>/ducklake.sqlite` | DuckLake SQLite catalog path |
| `--ducklake-data` | `<db-dir>/ducklake` | DuckLake Parquet directory |
| `--time-travel-retention` | `720h` (30d) | Expected DuckLake snapshot retention |
| `--file-delete-delay` | `168h` (7d) | Expected unreferenced-file delay |
| `--memory-limit` | — | DuckDB memory cap per instance (e.g. `4GB`); default is DuckDB's own 80% of available RAM. Work exceeding it spills to `<db-dir>/tmp` |
| `--max-temp-size` | — | Cap on the spill directory (e.g. `16GB`); by default spilling is bounded only by free disk space |
| `--query-timeout` | `60s` | Interrupt a query after this much *execution*, timed from when it acquires a connection |
| `--threads` | GOMAXPROCS | DuckDB worker threads per query. Honours container CPU limits |
| `--toml` | `dux.toml` | Load measures and relationships from a `dux.toml` file |
| `--export` | — | Write current schema to a `dux.toml` file and exit |
| `--import` | — | Import a `dux.toml` into the metadata DB and exit |
| `--version` | — | Print version and exit |

## Server (`duxd`)

Starts a long-running query server. Uses the same `db/` directory convention as the CLI.

```sh
duxd
```

Listens on `:8080` (`--listen`).

**Flags**

| Flag | Default | Description |
|------|---------|-------------|
| `--listen` | `:8080` | HTTP listen address |
| `--db-dir` | `db` | DUX state directory |
| `--dux` | `<db-dir>/dux.sqlite` | DUX semantic metadata SQLite path |
| `--ducklake-catalog` | `<db-dir>/ducklake.sqlite` | DuckLake SQLite catalog path |
| `--ducklake-data` | `<db-dir>/ducklake` | DuckLake Parquet directory |
| `--import-dir` | `inbox` alongside `<db-dir>` | Controlled Parquet inbox; an explicitly empty value disables imports |
| `--import-max-files` | `100` | Maximum files in one import request |
| `--import-timeout` | `30m` | Maximum runtime for one import |
| `--schema-refresh-interval` | `30s` | Poll for DuckLake DDL changes (`0` disables) |
| `--maintenance-compact-interval` | `1h` | Scheduled small-file compaction (`0` disables) |
| `--maintenance-checkpoint-interval` | `24h` | Scheduled checkpoint (`0` disables) |
| `--maintenance-timeout` | `30m` | Maximum runtime for one maintenance operation |
| `--maintenance-max-compactions` | `10` | Bound work per compaction call |
| `--time-travel-retention` | `720h` (30d) | Retain historical snapshots |
| `--file-delete-delay` | `168h` (7d) | Delay deletion of unreferenced files |
| `--memory-limit` | — | DuckDB memory cap per instance (e.g. `4GB`); default is DuckDB's own 80% of available RAM. Work exceeding it spills to `<db-dir>/tmp` |
| `--max-temp-size` | — | Cap on the spill directory (e.g. `16GB`); by default spilling is bounded only by free disk space |
| `--query-timeout` | `60s` | Interrupt a query after this much *execution*, timed from when it acquires a connection — queueing does not count against it |
| `--admission-timeout` | `5s` | How long a query waits for a free connection before it is shed with `503`. See [Concurrency](#concurrency) |
| `--max-connections` | `5` | Connection pool size; one is pinned for ownership, so one fewer queries run at once. See [Concurrency](#concurrency) |
| `--threads` | GOMAXPROCS | DuckDB worker threads per query. Honours container CPU limits. See [Concurrency](#concurrency) |
| `--toml` | `dux.toml` | Load measures and relationships from a `dux.toml` file |
| `--import` | — | Import a `dux.toml` into the metadata DB on startup |
| `--export` | — | Export current schema to a `dux.toml` file and exit |
| `--dash-dir` | `dashboards` | Dashboard file store directory |
| `--version` | — | Print version and exit |

Set `DUX_DASH=0` to disable the dashboards module (`/dash/` and `/api/dash/` return 404).

### Concurrency

Two flags control how much work runs at once, and they are **not**
interchangeable:

- `--max-connections` is how many queries run **at the same time** (minus the
  one pinned for ownership, so the default of 5 gives four query lanes).
- `--threads` is how many worker threads DuckDB may use **within one query**.

duxd states the resolved configuration at startup, so you never have to infer
it:

```text
query concurrency: 4 lanes x 20 threads/query = up to 80 workers on a CPU budget of 20
```

**Leave both alone unless you have measured a reason not to.** Parallelism
inside a query is cheap; concurrency between queries is not, because every
admitted query holds a lane and its memory until it finishes. Measured on a
3M-row instance on a 20-thread host:

| Change | Throughput | p99 latency |
|--------|------------|-------------|
| Lanes 2 → 11 | unchanged | **3.2x worse** |
| Threads 2 → 20 | unchanged | 17% better |

Throughput did not move in either direction. Extra lanes only convert fast
`503` shedding into slow in-server queueing, so the tail collapses while the
server does no more work. Do not tune these against a combined worker budget —
the worker total in the startup line is worst-case demand, not a target, and
the product does not predict behaviour.

Raise `--max-connections` only if queries are being shed while the CPU sits
idle. Lower it if the tail is bad and the shed rate is acceptable.

**Under load, duxd sheds rather than queues.** A query that waits longer than
`--admission-timeout` for a connection is rejected with `503` and a
`Retry-After` header, before any SQL runs, so it is always safe to retry — the
bundled UI does so automatically with jittered backoff. `--query-timeout` is
separate and starts only once a query holds a connection, so a busy queue never
eats into execution time.

**In containers**, `--threads` defaults to Go's `GOMAXPROCS`, which honours
cgroup CPU quotas; DuckDB's own default reads host hardware and would ignore
them. Confirm what the engine actually applied — no shell or DuckDB CLI needed:

```sh
curl -s localhost:8080/api/ducklake/status | grep -o '"threads":"[0-9]*"'
```

## Dashboards (Dash)

`duxd` serves a dashboard designer and viewer at `/dash/`. Each dashboard is **one JSON file** under `dashboards/` — the path is its identity (`dashboards/sales/overview.json` ↔ `/dash/sales/overview`) — so dashboards diff, version, and deploy like code.

For version control, mount `dashboards/` and manage that directory with Git on
the host or in a dedicated sidecar container. DUX only reads and writes the
dashboard files; it does not ship Git, run repository commands, or manage
credentials and remotes.

Capabilities:

- **Elements**: bar / line / combo / area / donut charts (with stacking, dual axes, and a series-split "Series by" well), tables (virtualized, sortable), pivot/matrix with query-backed subtotals (correct for non-additive measures), KPI cards, maps (MapLibre, lazy-loaded so dashboards without one never pay for the engine), markdown text, and images. Element types hot-swap with their field wells remapped.
- **Slicers & cross-filtering**: buttons / dropdown / range / date-range slicers filter every element (AND semantics) and cascade each other's option lists. Selections live in the URL's `?f=` parameter — a shareable deep link — and `?fullscreen` gives a chrome-less wall-display view. In view/fullscreen mode, clicking a bar / line point / donut slice / table row cross-filters the other visuals and highlights the selection (Ctrl/⌘ to multi-select, click empty canvas to clear). A header funnel shows every filter affecting each visual. Per-element CSV export.
- **Themes**: all styling flows from theme tokens (palette, backgrounds, borders, text, font — every color with alpha) cascading defaults ← `dashboards/theme.json` ← per-dashboard overrides.
- **Live refresh**: per-dashboard interval with a server-side floor and per-element stagger.
- **API-first**: everything the UI does goes through `/api/dash` — list/get/put/delete documents with ETag concurrency (`If-Match: *` for unconditional agent writes, `?raw=1` for verbatim download), theme, and the document JSON schema at `GET /api/dash/schema.json`. Every PUT is schema-validated server-side.

```sh
# Publish a dashboard from a file (create-or-overwrite)
curl -X PUT http://localhost:8080/api/dash/dashboards/sales/overview \
  -H 'If-Match: *' --data-binary @overview.json
```

### Assets

Images — logos, background art — are ordinary files placed under the
dashboards directory and served read-only. The path after
`/api/dash/assets/` is relative to that directory, so a file in a subfolder
carries the subfolder segment too:

```
dashboards/logo.png          →  GET /api/dash/assets/logo.png
dashboards/assets/logo.png   →  GET /api/dash/assets/assets/logo.png
dashboards/sales/bg.webp     →  GET /api/dash/assets/sales/bg.webp
```

Served extensions are `.png`, `.jpg`, `.jpeg`, `.webp`, and `.svg`; any other
file under `dashboards/` returns 404 even when it exists, and SVG is served
with `nosniff` and a no-script CSP. Paths are matched case-insensitively.

Assets are read from disk per request, so **adding one to a running server
needs no restart and no rebuild** — copy the file in and it is served on the
next request. (The UI itself is different: it is embedded into `duxd` at Go
build time and does require a rebuild.) There is deliberately no upload
endpoint — assets are versioned and deployed as files, like the documents.

To use an asset, set an element's `image.url` — or the theme's
`backgroundImage` — to the path relative to the dashboards directory:

```json
{ "image": { "url": "assets/logo.png", "fit": "contain" } }
```

Values starting with `http:`, `https:`, `data:`, or `/` are used verbatim;
anything else is resolved against `/api/dash/assets/`.

The [`dux-dashboards`](.agents/skills/dux-dashboards/) agent skill documents the document format and API in full.

## Agent skills

[`.agents/skills/`](.agents/skills/) contains four [Agent Skills](https://agentskills.io) for AI agents working against a DUX server: **dux-querying** (the query language), **dux-semantic** (model management), **dux-dashboards** (dashboard JSON + API), and **dux-ducklake** (Parquet imports, DuckLake maintenance, and pipeline rules). Each release attaches them as an individual `.zip` file.

## DuckLake tables, schemas, and views

DUX exposes its DuckLake instance without an artificial catalog prefix. A table in `main` is referenced by its table name:

```dux
EVALUATE Product
```

```dux
EVALUATE
    SUMMARIZECOLUMNS(
        Product[Category],
        "Net Sales", SUM(Sales[_NetSalesAmount])
    )
```

### Views and schemas

DuckDB **views** are introspected alongside tables and behave exactly like them in queries, relationships, and measures. The Explorer marks them with a `VIEW` badge.

Tables and views in a **non-default schema** carry the schema segment as `schema.Table`. The `main` segment is omitted.

```dux
EVALUATE
    SUMMARIZECOLUMNS(
        sales.Customer[name],
        "Orders", COUNTROWS(RELATEDTABLE(sales.Orders))
    )
```

## Loading data with pipelines

Parquet imports and direct DuckLake writes are both first-class production
paths. Choose based on what the pipeline naturally produces and which storage
it can safely access.

| Path | Best fit | Pipeline dependencies | Coordination |
|------|----------|-----------------------|--------------|
| **Parquet import API** | Pipelines that already produce Parquet files | Parquet writer and HTTP client | `duxd` validates and serializes DuckLake registration |
| **Direct DuckLake writer** | Pipelines that want native DuckLake SQL and transactions | Compatible DuckDB, DuckLake, and SQLite extensions | Pipeline retries whole transactions during catalog contention |

### Parquet import API

This is the simplest path for a Parquet-native pipeline. The pipeline does not
need DuckDB or DuckLake and never opens the DuckLake catalog. It publishes
complete Parquet files under `--import-dir`, then asks DUX to import them.
Paths are relative to the import directory; the API deliberately does not
upload file contents.

During the request DUX validates and copies each file once into the DuckLake
storage while calculating its SHA-256. Incompatible files fail immediately,
and content already registered to the target returns `409` with the prior
import ID. Registration then completes transactionally through the serialized
DuckLake worker.

```sh
curl -X POST http://localhost:8080/api/ducklake/imports \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: sales-2026-07-22T12:00:00Z' \
  -d '{"schema":"main","table":"Sales","files":["sales/part-0001.parquet"],"createIfMissing":false}'
```

An accepted request returns `202`; poll `GET /api/ducklake/imports/{id}` for
completion. `createIfMissing` creates an empty DuckLake table from the first
file's schema before registering all files. Existing tables require exactly
compatible columns and types. Imported files become DuckLake-owned and must
not be changed afterward. The inbox may be a Docker host bridge or
another delivery mount because DUX copies files into the native local DuckLake
storage before registration. Set `--import-dir=` explicitly to disable the
mutating import endpoint.

### Direct DuckLake writers

Use this path when a pipeline wants to manage tables and transactions with
normal DuckLake SQL. A reasonable number of local pipeline processes may
attach to the same DuckLake SQLite catalog and write while `duxd` keeps
querying. The pipeline only needs to know it is writing DuckLake; it does not
need to know DUX exists.

Keep the SQLite catalog and Parquet directory on one native local filesystem
visible at the same paths to every process. Do not use SMB, NFS, another
network filesystem, or a Docker Desktop host-filesystem bridge for concurrent
direct writers. Use bulk transactions and micro-batches, not row-at-a-time
commits. Five minutes or more is the recommended ingestion interval; cache and
batch lower-latency events. Data inlining is disabled. Each pipeline should
change only its documented schemas/tables; this is currently an operational
contract, not access-control enforcement.

```sql
INSTALL ducklake;
INSTALL sqlite;
LOAD ducklake;
LOAD sqlite;
ATTACH 'ducklake:sqlite:/srv/dux/db/ducklake.sqlite'
    AS ducklake (DATA_INLINING_ROW_LIMIT 0);
USE ducklake;

BEGIN;
INSERT INTO pipeline_sales.orders
SELECT * FROM read_parquet('/srv/pipeline/batch-20260722.parquet');
COMMIT;
DETACH ducklake;
```

Retry the whole transaction after a temporary SQLite lock or retry-exhaustion
error. Use backoff with jitter so independent pipelines do not recreate the
same lock convoy; do not retry individual rows.

`duxd` polls the DuckLake schema and automatically discovers pipeline DDL.
Public DUX names are `Table` for `main` and `schema.Table` otherwise.

DuckLake does not provide the primary-key/foreign-key relationship metadata
DUX needs for analytical joins. Define those relationships through the DUX
semantic API or `dux.toml`; they are semantic metadata, not DuckLake
constraints.

DuckLake health is available at `GET /api/ducklake/status`. Maintenance runs automatically; operators can also post `compact` or `checkpoint` to `/api/ducklake/maintenance`, list recent jobs with `GET /api/ducklake/maintenance`, and poll one with `GET /api/ducklake/maintenance/{id}`. Snapshot retention governs time-travel history, not current business rows; cleanup only affects files DuckLake has already marked unreferenced.

These mutating DuckLake endpoints share DUX's trusted-network boundary. DUX does not yet provide authentication or an administrative role; do not expose `duxd` directly to an untrusted network.

Back up `dux.sqlite`, `ducklake.sqlite`, and `ducklake/` as one logical unit after coordinating writers and completing a checkpoint. Copying only the catalog or only Parquet files is not a valid DuckLake backup. Dashboard files are separate and should be backed up/versioned with their mounted directory.

SQLite remains the catalog while this periodic, local workload is reliable. Move the DuckLake catalog to PostgreSQL when direct writers must be remote, more than one `duxd` replica is required, ingestion becomes continuous, writers require hard authorization boundaries, or measured SQLite contention violates the supported workload.

## HTTP API

### Query

```
POST /query
Content-Type: text/plain

EVALUATE SUMMARIZECOLUMNS(Product[Category], "Net Sales", SUM(Sales[_NetSalesAmount]))
```

```json
{
  "columns": ["Category", "Net Sales"],
  "rows": [
    ["Alcoholic Beverages", "435511175.75"],
    ["Juices", "53084544.89"],
    ["Soft Drinks", "32910749.31"],
    ["Water", "19499875.97"]
  ]
}
```

A JSON body injects **external filters** without touching the query text (this is what dashboard slicers use):

```
POST /query
Content-Type: application/json

{"query": "EVALUATE ...", "filters": [
  {"table": "Product", "column": "Category", "op": "in", "values": ["Water", "Soft Drinks"]}
]}
```

Ops: `in`, `between` (`from`/`to`), `=`, `!=`, `<`, `<=`, `>`, `>=`, `contains` (single `value`). Filters propagate through relationships like `TREATAS` arguments.

### Version

```
GET /version
→ {"version":"v0.4.0","capabilities":{"dashboards":true,"externalFilters":true,"measureFormats":true,"ducklake":true,"ducklakeMaintenance":true,"parquetImport":true}}
```

### Schema

```
GET /schema
```

Returns tables, columns, relationships, and measures as JSON.

```
POST /refresh
```

Re-introspects the DuckLake schema immediately and swaps the live model, rather than waiting for `--schema-refresh-interval`.

### Column values

```
GET /values?table=Product&column=Category&q=wat
```

Distinct values of a column, optionally filtered by a substring — what slicers and the filter editors use to populate their option lists.

### Import / export

```
GET  /export          → dux.toml download of all measures, relationships, date-table, and hidden designations
POST /import          ← dux.toml body; replaces all of the above
```

### Measures

```
GET /measures
→ [{"table": "Sales", "name": "Total Revenue", "expression": "SUM(Sales[_NetSalesAmount])"}]

POST /measures
{"table": "Sales", "name": "Total Revenue", "expression": "SUM(Sales[_NetSalesAmount])"}
→ 201 Created

DELETE /measures/:table/:name
→ 204 No Content
```

### Relationships

```
GET /relationships
→ [
    {"from_table": "Sales", "from_column": "ProductKey", "to_table": "Product", "to_column": "ProductKey"},
    {"from_table": "Bridge", "from_column": "DimBKey", "to_table": "DimB", "to_column": "DimBKey", "bidirectional": true}
  ]

POST /relationships
{"from_table": "Sales", "from_column": "ProductKey", "to_table": "Product", "to_column": "ProductKey"}
→ 201 Created

POST /relationships          (bidirectional)
{"from_table": "Bridge", "from_column": "DimBKey", "to_table": "DimB", "to_column": "DimBKey", "bidirectional": true}
→ 201 Created

DELETE /relationships
{"from_table": "Sales", "from_column": "ProductKey", "to_table": "Product", "to_column": "ProductKey"}
→ 204 No Content
```

### Date table

```
POST /datetable
{"table": "dates", "column": "date"}
→ 201 Created

DELETE /datetable
→ 204 No Content
```

### Hidden

```
POST /hidden                 (hide a table or view)
{"table": "Venue"}
→ 201 Created

POST /hidden                 (hide a single column)
{"table": "Sales", "column": "OrderId"}
→ 201 Created

DELETE /hidden               (unhide — same body shapes)
{"table": "Venue"}
→ 204 No Content
```

### Reference UI

```
GET /docs           Interactive API reference (Scalar)
GET /openapi.json   The OpenAPI 3.1 spec behind it (machine-readable)
GET /               DUX UI — query builder, Explorer (/explorer), and dashboards (/dash/)
*   /api/dash/*     Dashboards API (see Dashboards above)
*   /api/ducklake/* DuckLake status, imports, and maintenance (see Loading data below)
```

## Measures and relationships

All measures and relationships are persisted in `db/dux.sqlite` and are available immediately to every query without restarting the server. They can also be round-tripped as a portable `dux.toml` file:

```toml
[[relationship]]
from_table  = "Sales"
from_column = "ProductKey"
to_table    = "Product"
to_column   = "ProductKey"

# Bidirectional — filter context propagates in both directions through Bridge
[[relationship]]
from_table     = "Bridge"
from_column    = "DimBKey"
to_table       = "DimB"
to_column      = "DimBKey"
bidirectional  = true

[[measure]]
table      = "Sales"
name       = "Total Revenue"
expression = "SUM(Sales[_NetSalesAmount])"

[[measure]]
table      = "Sales"
name       = "Avg Order Value"
expression = "AVERAGE(Sales[_NetSalesAmount])"

# Optional display format — structured enum, rendered locale-aware by the UI
[measure.format]
kind     = "decimal"    # number | decimal | percent | currency | compact
decimals = 1
```

Export the current state, edit offline, re-import:

```sh
curl http://localhost:8080/export > dux.toml
# ... edit dux.toml ...
curl -X POST http://localhost:8080/import --data-binary @dux.toml
```

`POST /import` replaces the whole semantic model: existing relationships, measures, hidden designations, and date-table configuration in `dux.sqlite` are cleared and rebuilt from the file in one transaction. Export first if you need the current state back.

## Hiding tables and columns

Tables, views, and individual columns can be marked **hidden**. Hidden objects stay fully queryable — the flag only affects presentation, matching Power BI's "hide in report view" semantics. Like measures and relationships, hidden designations are persisted in `db/dux.sqlite` and round-trip through `dux.toml`:

```toml
# Hide a whole table or view
[[hidden]]
table = "Venue"

# Hide a single column
[[hidden]]
table  = "Sales"
column = "OrderId"
```

In the UI, the **Show hidden** toggle in the navbar (off by default) controls whether hidden objects are displayed at all — when shown, they render with muted colors. Every table header and column row has an eye-off toggle to hide or unhide it, styled like the date-table calendar toggle: hover a row to reveal it, yellow when active. Works on both the Home schema tree and the Explorer canvas.

Programmatically: `POST /hidden {"table": "...", "column": "..."}` (column optional) / `DELETE /hidden` with the same body.

## Query syntax

Every query starts with `EVALUATE`. An optional `DEFINE` block declares reusable measures. The result can be sorted with `ORDER BY` (with optional `START AT`, ascending keys only):

```dux
EVALUATE
    SUMMARIZECOLUMNS(
        Product[Category],
        "Net Sales", SUM(Sales[_NetSalesAmount])
    )
    ORDER BY [Net Sales] DESC, Product[Category]
```

**Aggregate by a column:**

```dux
EVALUATE
    SUMMARIZECOLUMNS(
        Product[Category],
        "Net Sales", SUM(Sales[_NetSalesAmount])
    )
```

**Filter, then aggregate:**

```dux
EVALUATE
    VAR discounted_sales = FILTER(
        Sales,
        Sales[_DiscountRate] > 0
    )
    RETURN SUMMARIZECOLUMNS(
        discounted_sales[VenueKey],
        "Orders", COUNT(discounted_sales[OrderId])
    )
```

**Named measures:**

```dux
DEFINE
    MEASURE Sales[Avg Order Value] =
        AVERAGE(Sales[_NetSalesAmount])

EVALUATE
    SUMMARIZECOLUMNS(
        Product[Category],
        "Avg Order Value", Sales[Avg Order Value]
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
| `ROLLUPADDISSUBTOTAL(col, "IsSubtotal", ...)` | Group argument adding subtotal rows; the named boolean column is TRUE on subtotal rows (compiles to `GROUPING SETS`) |
| `ROLLUPGROUP(c1, c2, ...)` | Roll several columns up as one unit inside `ROLLUPADDISSUBTOTAL` |
| `FILTER(T, predicate)` | Rows of `T` matching a predicate |
| `ADDCOLUMNS(T, "Name", expr...)` | Add computed columns to a table |
| `SELECTCOLUMNS(T, "Name", expr...)` | Project to named computed columns |
| `TOPN(n, T, expr)` | Top `n` rows of `T` ordered by `expr` descending |
| `UNION(T1, T2)` | Union of two tables (duplicates included) |
| `INTERSECT(T1, T2)` | Rows present in both tables |
| `EXCEPT(T1, T2)` | Rows in `T1` not in `T2` |
| `VALUES(T[C])` | Distinct values of a column as a table |
| `DISTINCT(T[C])` | Alias for `VALUES` |
| `CROSSJOIN(T1, T2, ...)` | Cartesian product of two or more tables |
| `GENERATE(T1, T2)` | Evaluate `T2` for each row of `T1` (lateral join); `T1`'s columns are in scope inside `T2` |
| `GENERATEALL(T1, T2)` | Like `GENERATE`, but keeps `T1` rows with no matches |

Table arguments compose: any table function accepts a nested table expression where a table name is expected.

```dux
EVALUATE
    TOPN(
        5,
        SUMMARIZECOLUMNS(Venue[Venue], "Net Sales", SUM(Sales[_NetSalesAmount])),
        [Net Sales]
    )
```

### Filter context

| Function | Description |
|----------|-------------|
| `CALCULATE(expr, filters...)` | Evaluate `expr` under a modified filter context |
| `TREATAS(source, T[C])` | Apply a set of values as a filter on `T[C]` |
| `ALL(T)` / `ALL(T[C]...)` | Remove filters from a table or specific columns; as a table expression, the unfiltered table / distinct column values |
| `ALLEXCEPT(T, T[C]...)` | Remove all filters on `T` except those on the listed columns |
| `REMOVEFILTERS(...)` | Alias of `ALL(...)` inside CALCULATE |
| `KEEPFILTERS(pred)` | Intersect `pred` with the existing filter context instead of overriding it |

Inside `CALCULATE`, a plain predicate on a column **replaces** any existing filter on that column (DAX shorthand semantics) — use `KEEPFILTERS` to intersect instead. The canonical percent-of-total pattern works as expected:

```dux
EVALUATE
    SUMMARIZECOLUMNS(
        Product[Category],
        "Net Sales", SUM(Sales[_NetSalesAmount]),
        "Share",   DIVIDE(
            SUM(Sales[_NetSalesAmount]),
            CALCULATE(SUM(Sales[_NetSalesAmount]), ALL(Product))
        )
    )
```

### Time intelligence

Time intelligence works best with a **designated date table** (see below). The date ranges are anchored to the dates visible in the current filter context — e.g. grouped by year and month, `TOTALYTD` accumulates from January 1st to the end of each month.

| Function | Description |
|----------|-------------|
| `DATESYTD(D[c])` / `DATESQTD` / `DATESMTD` | Dates from the start of the year / quarter / month to the last date in context |
| `TOTALYTD(expr, D[c])` / `TOTALQTD` / `TOTALMTD` | Shorthand for `CALCULATE(expr, DATESYTD(D[c]))` |
| `SAMEPERIODLASTYEAR(D[c])` | The context's date range shifted back one year |
| `DATEADD(D[c], n, YEAR\|QUARTER\|MONTH\|DAY)` | The context's date range shifted by `n` intervals |
| `PREVIOUSYEAR/QUARTER/MONTH/DAY(D[c])` | The full period before the first date in context |
| `NEXTYEAR/QUARTER/MONTH/DAY(D[c])` | The full period after the last date in context |
| `DATESBETWEEN(D[c], start, end)` | Dates in `[start, end]`; either bound may be `BLANK()` |
| `DATESINPERIOD(D[c], start, n, interval)` | `n` intervals of dates from `start` (negative `n` = backwards) |
| `CALENDAR(start, end)` | Generated date table with a `Date` column |
| `CALENDARAUTO()` | Like `CALENDAR`, spanning whole years across every date column in the model |
| `DATE(y, m, d)` | Date constructor |

`MAX(D[c])` / `MIN(D[c])` / `LASTDATE(D[c])` / `FIRSTDATE(D[c])` / `TODAY()` are understood as context-aware anchors in the `start`/`end` positions of `DATESBETWEEN` and `DATESINPERIOD`.

```dux
EVALUATE
    SUMMARIZECOLUMNS(
        dates[year],
        dates[month],
        "Sales",       SUM(orders[amount]),
        "Sales YTD",   TOTALYTD(SUM(orders[amount]), dates[date]),
        "Sales PY",    CALCULATE(SUM(orders[amount]), SAMEPERIODLASTYEAR(dates[date]))
    )
```

#### Designating a date table

Mark a table as the model's date table in `dux.toml`:

```toml
[[date_table]]
table  = "dates"
column = "date"
```

Or use the Explorer UI: every table with a `DATE`/`TIMESTAMP` column shows a calendar icon in its header — click it to designate the table (only one table can be the date table). When the table has several date columns, per-column calendar icons let you pick which one is the date column. Programmatically: `POST /datetable {"table": "dates", "column": "date"}` / `DELETE /datetable`.

On a designated date table, time-intelligence functions clear **all** filters on the table before applying their date range (the DAX "mark as date table" behaviour) — this is what makes YTD work when grouping by the table's year/month columns. On an undesignated table only the date column's own filter is replaced.

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
| `TRUE()` / `FALSE()` | Boolean constants |

### Relationship traversal

| Function | Description |
|----------|-------------|
| `RELATED(Dim[col])` | Fetch a column from the one-side of a relationship for the current row (in `FILTER`, `ADDCOLUMNS`, iterators) |
| `RELATEDTABLE(Fact)` | The fact rows related to the current dimension row (e.g. `COUNTROWS(RELATEDTABLE(Sales))`) |

### Scalar function library

DAX scalar functions are translated to DuckDB built-ins — either passed through directly when the spelling matches (`ABS`, `ROUND`, `SQRT`, `UPPER`, `LOWER`, `TRIM`, `LEFT`, `RIGHT`, `COALESCE`, …) or mapped when the semantics differ:

- **Date/time** — `YEAR`, `MONTH`, `DAY`, `HOUR`, `MINUTE`, `SECOND`, `QUARTER`, `WEEKDAY` (types 1–3), `WEEKNUM`, `EOMONTH`, `EDATE`, `TODAY`, `NOW`, `DATE`, `TIME`, `DATEVALUE`, `DATEDIFF`
- **Math** — `INT`, `MOD` (Excel sign semantics), `POWER`, `ROUNDUP`, `ROUNDDOWN`, `TRUNC`, `CEILING`/`FLOOR` (significance form), `LOG` (default base 10)
- **Text** — `LEN`, `MID`, `SUBSTITUTE`, `REPLACE`, `CONCATENATE`, `SEARCH` (case-insensitive), `FIND`, `REPT`, `UNICHAR`, `EXACT`, `VALUE`, `FORMAT`

`FORMAT` supports named formats (`"Percent"`, `"Fixed"`, `"Standard"`, `"Scientific"`, `"General Number"`), date patterns (`"yyyy-MM-dd"`, `"MMM d"`, …), and numeric masks (`"0.00"`, `"#,##0.00"`); the format string must be a literal.

## Multi-table measures (stitched codegen)

A DAX measure is evaluated in its own filter context. When the measures of a SUMMARIZECOLUMNS call reach more than one table (e.g. `SUM(Sales[Qty])` next to `SUM(Returns[Qty])` grouped by `Date[Year]`), a single flat join tree would pair every sales row with every returns row on the same date and inflate both sums. DUX instead evaluates each **table cluster** in its own grouped CTE and stitches the results on the group keys:

```sql
WITH _mc0 AS (
    SELECT year AS k0, (SUM(qty)) AS a0
    FROM dates LEFT JOIN fact_sales ON dates.datekey = fact_sales.datekey
    GROUP BY year
), _mc1 AS (
    SELECT year AS k0, (SUM(rqty)) AS a1
    FROM dates LEFT JOIN fact_returns ON dates.datekey = fact_returns.datekey
    GROUP BY year
)
SELECT COALESCE(_mc0.k0, _mc1.k0) AS "year", _mc0.a0 AS 'Sold', _mc1.a1 AS 'Returned'
FROM _mc0
FULL OUTER JOIN _mc1 ON _mc0.k0 IS NOT DISTINCT FROM _mc1.k0
```

Properties of stitched queries:

- **Clustering** is by the table set each aggregate references (stored measures are expanded first). Measures over the same table share one CTE; a single expression spanning two tables — `DIVIDE(SUM(Sales[Qty]), COUNT(matches[id]))` — has each aggregate lifted into its own cluster and the arithmetic evaluated over the stitched columns.
- **Filters propagate like DAX**: a TREATAS filter is routed into every cluster whose tables it can reach following relationship direction (one side → many side, both ways across bidirectional edges). A filter on a dimension related only to table A leaves table B's measures untouched; a filter unrelated to *every* measure is an error.
- **Row semantics**: the FULL OUTER JOIN returns the union of key combinations produced by any cluster — a group that only exists for one measure shows the other measures as NULL (matching DAX, and unlike a flat join which would drop the row).
- CALCULATE, time intelligence, and ROLLUPADDISSUBTOTAL all evaluate inside their cluster's context; rollup grouping levels stitch on the keys *and* the `GROUPING()` flags so subtotal rows pair only with matching subtotal rows.

Single-table queries keep the plain flat-join emission — stitched codegen activates for multi-table queries, for join graphs that cross a bidirectional relationship (next section), and for measures that modify the filter context (below).

### Context CTEs

A measure whose `CALCULATE` *modifies* the group filter context — an `ALL`-family removal, a predicate overriding a group key, or any time-intelligence range — is evaluated in its own grouped CTE rather than inline. The CTE groups by only the keys the measure retains and is `LEFT JOIN`ed back onto them, so a removed key makes the value repeat across that key (`ALL` semantics) and a cleared key set yields one grand-total row.

Time-intelligence anchors (`MAX(D[c])` and friends) become columns of an uncorrelated **anchor scan** — one row per group cell of the date table with the required extremes — and the date range becomes a join between that scan and the cleared copy of the date table. A rolling 7-day measure grouped by day compiles to:

```sql
WITH _cc0 AS (
    SELECT __anch0.date AS k0, (SUM(amount)) AS v
    FROM orders
    LEFT JOIN dates ON orders.order_date = dates.date
    CROSS JOIN (SELECT date, MAX(date) AS a0 FROM dates GROUP BY date) AS __anch0
    WHERE dates.date > __anch0.a0 + (-7) * INTERVAL 1 DAY AND dates.date <= __anch0.a0
    GROUP BY __anch0.date
)
SELECT _mc0.k0 AS "date", (_cc0.v) AS 'R7D'
FROM _mc0
LEFT JOIN _cc0 ON _cc0.k0 IS NOT DISTINCT FROM _mc0.k0
```

Every intermediate is bounded by the number of group cells or the fact table's own size, and the anchor is emitted with no correlation to the outer query — a deliberate invariant for this shape. (Correlated `EXISTS` semi-joins are still used for bidirectional bridges, where they are the point: see [Bidirectional relationships](#bidirectional-relationships).) DuckDB decorrelates a single level of correlation well but not a correlated anchor nested inside a correlated context subquery: that shape forced the range filter above a *group cells × fact rows* delim join, and a daily rolling window over a few million fact rows consumed tens of gigabytes before finishing. Grouping-set (`ROLLUPADDISSUBTOTAL`) and computed-group-key queries keep the older correlated-subquery form, where the anchor is inlined to the group column whenever the anchored column is itself a group key.

## Bidirectional relationships

By default, a relationship is unidirectional: filter context flows from the `from` table toward the `to` table and the emitter produces a `LEFT JOIN`. Setting `bidirectional = true` on a relationship allows filter context to propagate in both directions through a bridge (junction) table.

### Schema pattern

```
DimA ←── Bridge ↔ DimB ──→ FactMeasures
```

```toml
[[relationship]]
from_table  = "Bridge"
from_column = "DimAKey"
to_table    = "DimA"
to_column   = "DimAKey"

[[relationship]]
from_table     = "Bridge"
from_column    = "DimBKey"
to_table       = "DimB"
to_column      = "DimBKey"
bidirectional  = true

[[relationship]]
from_table  = "FactMeasures"
from_column = "DimBKey"
to_table    = "DimB"
to_column   = "DimBKey"
```

### What the codegen emits

Queries whose join graph crosses a `bidirectional = true` edge are emitted through **stitched codegen** (see *Multi-table measures* above). Within each measure's cluster CTE, the filter chain beyond the bidi edge is carved into a correlated `EXISTS` semi-join: rows are gated by the bridge without ever being joined to it, so many-to-many bridge rows cannot fan the measure's rows out:

```sql
WITH _mc0 AS (
    SELECT (SUM(Amount)) AS a0
    FROM factmeasures
    LEFT JOIN dimb ON factmeasures.DimBKey = dimb.DimBKey
    WHERE EXISTS (SELECT 1 FROM bridge
                  JOIN dima ON bridge.DimAKey = dima.DimAKey
                  WHERE bridge.DimBKey = dimb.DimBKey AND Category IN ('X'))
)
SELECT _mc0.a0 AS 'Total'
FROM _mc0
```

### Ambiguity detection

Bidirectional edges can create ambiguous filter graphs when two tables are reachable from each other via more than one path. DUX rejects such schemas at startup and at the `POST /relationships` endpoint — ambiguity is never silently resolved at query time:

```
schema validation: ambiguous filter graph: tables "DimA" and "DimB" are connected
by more than one path:
  [1] DimA ↔ DimB (bidi edge)
  [2] dima → ... → dimb
```

### UI

In the Explorer canvas, bidirectional relationship lines are rendered with a **30 % orange / 40 % blue / 30 % orange** gradient to distinguish them from standard (orange → blue) unidirectional lines. The relationship modal has a **Bidirectional** checkbox next to the ⇄ Reverse button.

## License

[MIT](LICENSE).
