# Element reference

Per-type configuration for dashboard elements. All query-backed types share
the `query` block (fields / filters / sort / topN, or raw DUX); `viz` keys
below are per type. Metric wells reference **output column names** (the
measure name or the numeric column's name).

## Charts

### bar

- Fields: n dims (axis) + n metrics (values).
- `viz.orientation`: `"vertical"` (default) | `"horizontal"`.
- `viz.stacked`: stack series instead of clustering.

### line

- Fields: n dims + n metrics.
- `viz.y2`: array of metric names plotted on the right axis (two-scale).
- Default sort when unset: axis columns ascending.

### combo (bars + lines)

- Fields: n dims + n metrics.
- `viz.lines`: metric names rendered as lines (the rest are bars).
- `viz.lineY2`: lines on the right axis (default `true`).

### area

- Fields: n dims + n metrics.
- `viz.stacked`: stacked areas (default overlapping translucent).
- Default sort when unset: axis columns ascending.

### donut

- Fields: exactly 1 dim (category) + 1 metric (value); extra metrics are
  ignored. Formatted total renders in the center.

### Series split ("Series by" — bar, line, area)

`viz.series` names a **dim** in the fields list whose values fan the first
metric out into one series each (PBI's Legend well):

```json
"query": { "fields": [
  { "table": "atp.matches", "name": "round",   "kind": "column", "dataType": "VARCHAR" },
  { "table": "atp.matches", "name": "surface", "kind": "column", "dataType": "VARCHAR" },
  { "table": "atp.matches", "name": "Matches", "kind": "measure" }
]},
"viz": { "series": "surface", "stacked": true }
```

While active, extra metrics are ignored; NULL/empty series values label as
"(blank)". Note `topN` applies to the flat (axis × series) rows.

### Common chart viz keys

- `viz.legend`: `true` | `false`; omit for auto (shown when multi-series).
- `viz.showEmpty`: show axis items whose metrics are all NULL (default
  hidden).

## kpi

- Fields: 1 metric (first one renders). Shows the formatted value large,
  with the metric name as label.

## table

- Fields well is one flat list (dims and metrics both allowed). Renders all
  columns in field order; rows virtualized (no cap); headers click-sort.

## pivot (matrix)

- Fields: dims + metrics. `viz.cols` lists the dim names pivoted to
  **columns**; remaining dims are rows (nested in field order).
- Subtotals and grand totals are separate server queries per grouping level
  (correct for non-additive measures); the element's filters and slicer
  fan-out apply to them, `topN` does not.
- `viz.subtotals` / `viz.grandTotal` / `viz.totalCol` — all default `true`.
- Row groups expand/collapse with ▸/▾ carets (collapsed groups show their
  totals inline). Groups start collapsed by default; `viz.collapsed: false`
  starts them expanded. The expansion state itself is per-session, never
  stored in the document.
- Column combinations render capped at 60 (a "showing n of m" note
  appears).

```json
{
  "type": "pivot",
  "query": { "mode": "builder", "fields": [
    { "table": "atp.matches", "name": "tourney_level", "kind": "column", "dataType": "VARCHAR" },
    { "table": "atp.matches", "name": "surface", "kind": "column", "dataType": "VARCHAR" },
    { "table": "atp.matches", "name": "best_of", "kind": "column", "dataType": "BIGINT", "aggregate": "VALUES" },
    { "table": "atp.matches", "name": "Matches", "kind": "measure" }
  ]},
  "viz": { "cols": ["best_of"] }
}
```

## slicer

```json
{
  "id": "slicer-1", "type": "slicer",
  "layout": { "x": 16, "y": 300, "w": 200, "h": 240 },
  "title": { "text": "tourney_level", "show": true },
  "slicer": {
    "table": "atp.matches", "column": "tourney_level",
    "dataType": "VARCHAR",
    "kind": "buttons",
    "multi": true,
    "limit": 20,
    "measure": { "table": "atp.matches", "name": "Matches", "kind": "measure" }
  }
}
```

- `kind`: `buttons` (value pills) | `dropdown` (searchable multi-select) |
  `range` | `daterange`. (`list` is accepted and renders as buttons.)
- `dataType` should be the column's DuckDB type — it types the filter
  values (numeric columns get numeric filters) and drives the range kinds.
- `limit`: max pills for the buttons kind (default 20; "+n more" chip).
- `measure` (optional "Trim by"): hides values where the metric is NULL —
  a measure, or a numeric column as
  `{"kind": "column", "dataType": "...", "aggregate": "SUM"}`.
- Slicers receive each other's filters (cascading option lists).
  Selections live only in the page URL (`?f=`), never in the document.

## text

`"text": { "markdown": "## Title\n\nBody with **GFM** support." }`

## image

`"image": { "url": "https://… or /api/dash/assets/logo.png", "fit": "contain" | "cover" | "fill" }`

## Per-element interactions

`"interactions": { "ignoreSlicers": ["slicer-1"] }` — element ids of
slicers this element opts out of.
