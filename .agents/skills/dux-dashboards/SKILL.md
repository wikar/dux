---
name: dux-dashboards
description: Build, edit, and publish DUX dashboards (Dash) as JSON documents through the duxd /api/dash HTTP API - canvas layout, chart/table/pivot/KPI elements backed by DUX queries, slicers with cross-filtering, themes, live refresh, and shareable deep links. Use when the user asks to create or modify a dashboard or report on a DUX/duxd server, add charts or slicers, restyle a dashboard theme, or automate dashboard deployment.
license: MIT
metadata:
  author: wikar
  project: dux
---

# Building DUX dashboards

A dashboard is **one JSON file**: a fixed-size canvas of positioned
elements, each backed by a DUX query. The file's path under the server's
`dashboards/` directory is its identity (`sales/overview` ↔
`dashboards/sales/overview.json`) and the UI serves it at
`/dash/sales/overview`. Agents create dashboards by writing this JSON and
PUTting it to the API — no UI interaction needed.

## Recommended workflow

1. `GET /schema` (the DUX one) — learn tables, columns, measures.
2. Test each element's query via `POST /query` first — an element is just a
   query plus presentation.
3. Build the document (start from
   [assets/example-dashboard.json](assets/example-dashboard.json)).
4. `PUT /api/dash/dashboards/{path}` with `If-Match: *`
   (create-or-overwrite). The server validates against its JSON schema and
   returns 422 with the reason on invalid documents.
5. Open `/dash/{path}` (add `?fullscreen` for a chrome-less wall display).

`scripts/put-dashboard.sh` and `scripts/get-dashboard.sh` wrap the API; the
authoritative document schema is served at `GET /api/dash/schema.json`.
Full endpoint semantics (ETags, conflicts, raw download):
[references/api.md](references/api.md).

## Document skeleton

```json
{
  "version": 1,
  "canvas": { "width": 1280, "height": 720 },
  "refresh": { "enabled": false, "intervalSeconds": 60 },
  "controls": { "csv": true, "funnel": true },
  "theme": { "background": "#101020" },
  "elements": [ ... ]
}
```

- Layout is absolute: each element has
  `"layout": {"x", "y", "w", "h", "z"}` in canvas pixels (the UI snaps to
  an 8px grid; agents should too). The canvas scales to fit the viewer.
- `refresh` re-runs every element's query on the interval (5s server
  floor), staggered per element.
- `controls` toggles the per-element header icons — `csv` (export) and
  `funnel` (filter provenance). Both default **on**; set either to `false`
  for a cleaner, non-interactive look.
- `theme` is a sparse per-dashboard override of the global theme — see
  [references/theme.md](references/theme.md).

## Elements

Eleven types. `bar`, `line`, `combo`, `area`, `donut`, `table`, `pivot`,
`kpi` are **query-backed**; `slicer` filters the others; `text` (markdown)
and `image` (URL) are static.

A query-backed element in builder mode:

```json
{
  "id": "bar-1", "type": "bar",
  "layout": { "x": 16, "y": 16, "w": 400, "h": 240, "z": 1 },
  "title": { "text": "Matches by surface", "show": true },
  "query": {
    "mode": "builder",
    "fields": [
      { "table": "atp.matches", "name": "surface", "kind": "column", "dataType": "VARCHAR" },
      { "table": "atp.matches", "name": "Matches", "kind": "measure" }
    ],
    "sort": [{ "field": "Matches", "dir": "desc" }],
    "topN": 5
  },
  "viz": { "stacked": false, "legend": true }
}
```

Field rules (order matters — dims before metrics):

- Dimension: a column ref; numeric columns need `"aggregate": "VALUES"` to
  act as a dim.
- Metric: a measure ref (`"kind": "measure"`), or a numeric column with an
  aggregate (`SUM`, `AVERAGE`, `COUNT`, `MIN`, `MAX`, …).
- `query.filters` adds per-element filters
  (`{table, name, dataType, op, value}`).
- `"mode": "raw"` with `"raw": "EVALUATE …"` runs hand-written DUX instead
  (first result column = x axis, rest = series).

Per-type wells, `viz` settings (stacking, dual axes, series split, pivot
rows/columns/totals), and slicer configuration:
[references/elements.md](references/elements.md).

## Slicers and cross-filtering

A slicer element declares a column; its runtime **selections are never
stored in the document** — they live in the page URL's `?f=` parameter,
which is also the deep-link/share mechanism:

```
/dash/sales/overview?f={"slicer-1":["G"],"slicer-2":["Grass"]}
```

Selections fan out to every other element as external filters (AND
semantics), slicers cascade each other's option lists, and
`"interactions": {"ignoreSlicers": ["slicer-1"]}` opts an element out.
`?fullscreen` (composable with `?f=`) hides all chrome.

**Chart cross-filtering** (view/fullscreen only): clicking a bar, line
point, donut slice, or table/pivot row filters every *other* query visual
by that data point and highlights the selection in the clicked visual.
Ctrl/⌘-click multi-selects within and across visuals; a left-click on empty
canvas clears it. These selections are **transient** (session only — not in
the document, not in `?f=`) and need no configuration: every query visual
both emits and receives them. Nothing to author — it's automatic at view
time.

**Filter funnel**: each query visual's header carries a funnel control;
hovering (or clicking to pin) lists every filter currently affecting that
visual, grouped by source — its own `query.filters`, each active slicer,
and each cross-filtering visual.

## Element IDs and hygiene

- `id` must be unique in the document (pattern `[A-Za-z0-9_-]{1,64}`); the
  UI convention is `type-N`.
- The path is lowercase letters, digits, space, `-`, `_`, with `/` for
  folders; the name `theme` is reserved.
- Don't write `viz` keys you don't mean: the schema keeps `viz` open, but
  unknown keys are still saved and shared with future renderers.
