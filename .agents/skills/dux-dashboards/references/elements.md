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
  { "table": "Date", "name": "MonthName", "kind": "column", "dataType": "VARCHAR" },
  { "table": "Product", "name": "Category", "kind": "column", "dataType": "VARCHAR" },
  { "table": "Sales", "name": "NetRevenue", "kind": "column", "dataType": "DECIMAL(14,4)", "aggregate": "SUM" }
]},
"viz": { "series": "Category", "stacked": true }
```

While active, extra metrics are ignored; NULL/empty series values label as
"(blank)". Note `topN` applies to the flat (axis × series) rows.

### Common chart viz keys

- `viz.legend`: `true` | `false`; omit for auto (shown when multi-series).
- `viz.showEmpty`: show axis items whose metrics are all NULL (default
  hidden).

Numeric axis ticks render scaled (`€15.3M` — thousands T, millions M,
billions B) and size themselves to their widest label; tooltips still show
the full measure format. Nothing to configure.

The empty-row drop keys on *all* metrics being NULL, so one measure that
outlives the data keeps the row. A date dimension usually spans years beyond
what is loaded, and a time-intelligence measure (`SAMEPERIODLASTYEAR`,
`DATEADD`) is non-NULL for the year *after* the last loaded one — enough to
stretch the axis into empty space. Bound such elements with an explicit
`query.filters` range rather than relying on the drop.

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
    { "table": "Region", "name": "RegionName", "kind": "column", "dataType": "VARCHAR" },
    { "table": "Product", "name": "Category", "kind": "column", "dataType": "VARCHAR" },
    { "table": "Customer", "name": "CustomerType", "kind": "column", "dataType": "VARCHAR" },
    { "table": "Sales", "name": "NetRevenue", "kind": "column", "dataType": "DECIMAL(14,4)", "aggregate": "SUM" }
  ]},
  "viz": { "cols": ["CustomerType"] }
}
```

## map

A MapLibre canvas with one or two point layers. Layers are fetched per layer
rather than through the shared query pipeline, so the element carries
`query.filters` (for slicer/filter fan-out) but no `query.fields`.

```json
{
  "type": "map",
  "query": { "mode": "builder", "filters": [] },
  "viz": {
    "layers": [{
      "id": "layer-1",
      "kind": "circle",
      "lng": { "table": "Venue", "name": "Longitude", "kind": "column", "dataType": "DECIMAL(10,7)" },
      "lat": { "table": "Venue", "name": "Latitude",  "kind": "column", "dataType": "DECIMAL(10,7)" },
      "size": { "table": "Sales", "name": "NetRevenue", "kind": "measure" },
      "category": { "table": "Venue", "name": "VenueType", "kind": "column", "dataType": "VARCHAR" }
    }],
    "center": [10.45, 51.16],
    "zoom": 4.6
  }
}
```

- `kind`: `circle` | `pin` | `heatmap`.
- `lng` / `lat` are required; the layer renders nothing without both. They
  are numeric columns used as **dimensions**, so the layer query applies
  `aggregate: "VALUES"` to them for you.
- `size` (optional) scales the mark by a measure, relative to the layer max.
- `category` (optional) colors marks by a dim's values from the palette.
  Only use it when the distinct count fits the palette — beyond that the
  hues cycle and two categories share a color.
- `center` / `zoom` set the initial view; omit both to fit the extent.
- The basemap is styled from the theme, so the element reads as part of the
  dashboard rather than a pasted-in map.

## slicer

```json
{
  "id": "slicer-1", "type": "slicer",
  "layout": { "x": 8, "y": 176, "w": 240, "h": 88 },
  "title": { "text": "Category", "show": true },
  "slicer": {
    "table": "Product", "column": "Category",
    "dataType": "VARCHAR",
    "kind": "buttons",
    "multi": true,
    "limit": 20,
    "measure": { "table": "Sales", "name": "NetRevenue", "kind": "column", "dataType": "DECIMAL(14,4)", "aggregate": "SUM" }
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
- Clearing is an eraser control in the slicer's title bar, shown only while
  something is selected. It needs no configuration — but it does need a
  title bar, so leave `title.show` on unless the element is deliberately
  chrome-less (a titleless slicer falls back to a floating control).

Heights are a layout concern, not a slicer one — `dropdown` is 68px,
`daterange`/`range` 96px, and `buttons` is sized to its pill rows. See
[layout.md](layout.md).

## text

`"text": { "markdown": "## Title\n\nBody with **GFM** support." }`

## image

`"image": { "url": "https://… or /api/dash/assets/logo.png", "fit": "contain" | "cover" | "fill" }`

## Per-element interactions

`"interactions": { "ignoreSlicers": ["slicer-1"] }` — element ids of
slicers this element opts out of.

Chart cross-filtering (clicking a mark/row to filter the other visuals, in
view/fullscreen mode) is automatic for every query-backed element and has
no per-element document config — see the "Slicers and cross-filtering"
section of the skill overview.
