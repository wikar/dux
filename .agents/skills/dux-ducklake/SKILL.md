---
name: dux-ducklake
description: Operate the DuckLake instance behind DUX through duxd, including first-class Parquet imports, import status, maintenance jobs, DuckLake health, and guidance for direct local pipelines. Use when adding pipeline output to DUX, importing Parquet, creating a DuckLake table, checking versions or snapshots, compacting files, expiring time-travel snapshots, cleaning unreferenced files, or designing a pipeline that writes to the DUX-owned DuckLake instance.
---

# DUX DuckLake

Use the `duxd` DuckLake API for ownership operations. Treat `duxd` as the DuckLake owner and do not manipulate its internal files.

## Inspect first

Call `GET /api/ducklake/status`. Confirm DuckDB/DuckLake versions, snapshot and schema versions, local paths, and scheduler intervals before changing anything.

## Import Parquet

1. Write complete `.parquet` files beneath the inbox configured by `--import-dir`. Publish final names atomically; never expose a file still being written.
2. Send paths relative to that directory. Never send absolute paths, traversal paths, symlinks, URLs, or multipart data.
3. Generate and retain one `Idempotency-Key` per logical import. Reuse it only for an identical request.
4. Call:

```http
POST /api/ducklake/imports
Idempotency-Key: pipeline-run-2026-07-22T12:00:00Z
Content-Type: application/json

{
  "schema": "main",
  "table": "sales",
  "files": ["sales/part-0001.parquet"],
  "createIfMissing": false
}
```

5. A valid request returns `202`; poll `GET /api/ducklake/imports/{id}` until `succeeded` or `failed`. A `409` naming a prior import means identical content already belongs to that target and must not be submitted again.

Set `createIfMissing` only when the Parquet schema should define a new table. Existing tables require exactly matching columns and types. DUX copies accepted files into its DuckLake data path before transferring their ownership to DuckLake. Do not edit or delete those copies. An explicitly empty `--import-dir` disables this mutating endpoint.

## Run maintenance

Call `POST /api/ducklake/maintenance` with `compact` or `checkpoint`. Poll `GET /api/ducklake/maintenance/{id}`. A `409` means another DUX-owned operation is active; retry later rather than running work in parallel.

- `compact` merges small adjacent files without removing logical rows.
- `checkpoint` performs DuckLake's configured snapshot expiration, file rewrite/compaction, cleanup, and orphan handling. It never deletes rows still present in the current table version.

Scheduled maintenance is automatic. Use manual jobs for operational needs, not as a second scheduler.

## Guide direct pipelines

Permit direct pipeline writers only when the DuckLake SQLite catalog and Parquet data are on the same native local filesystem visible to every process. Use the DuckLake client, bulk transactions, and micro-batches; recommend intervals of five minutes or more and batch/cache lower-latency events. Retry a whole failed batch with jittered backoff, never row by row. Disable data inlining. Let each pipeline change only its documented schema/tables. This ownership rule is a documented contract, not runtime authorization.

Do not use SMB, NFS, a network filesystem, or a Docker Desktop host-filesystem bridge for concurrent direct catalog writers. Treat the controlled import API as the first-class path for Parquet-native pipelines and for pipelines that publish into a mounted or bridged folder.

## Use public table names

Refer to tables in DUX as `Table` for DuckLake `main`, or `schema.Table` for another schema. Never expose or use the internal `ducklake.main.Table` name in DUX queries, relationships, or measures.
