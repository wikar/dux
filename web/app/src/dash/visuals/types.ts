// Visual registry contracts. Everything a visual declares about itself —
// icon, label, default size, field wells, display options, header controls —
// lives in one VisualDef exported by that visual's module; index.ts only rolls
// those definitions up into the registry.
//
// This module is a leaf on purpose: it must stay importable from docOps, data
// and the settings pane without dragging in body components.
import type { ComponentType, JSX } from "react";
import type { MeasureFormat, QueryResponse } from "@dux/core";
import type { ChartRow, Interaction, SeriesSpec } from "../charts/ChartKit";
import type { DashElement, ElementType, VizSettings } from "../types";

/** Field wells a visual can expose. Membership is derived from the element's
 *  flat query.fields plus the viz keys — see wells.ts. */
export type WellId =
  | "axis" | "values" | "y2" | "bars" | "lines" | "fields" | "rows" | "cols" | "series";

/** max marks single-slot wells: dropping onto a full one replaces the member. */
export interface WellSpec {
  id: WellId;
  label: string;
  max?: number;
  hint?: string;
}

/** One control in the settings "Display" section, bound to an element.viz key.
 *  "tri" is a boolean that also has an unset (auto) state. */
export type OptionSpec =
  | { key: string; label: string; kind: "check"; default: boolean }
  | { key: string; label: string; kind: "select"; default: string; choices: { value: string; label: string }[] }
  | { key: string; label: string; kind: "number"; default: number }
  | { key: string; label: string; kind: "tri"; autoLabel: string };

export interface DataSpec {
  wells: WellSpec[];
  /** Body consumes pivoted chart rows (rows/keys in DataBodyProps). */
  chart?: boolean;
  /** The first Values measure can fan out over a "Series by" dim. */
  seriesSplit?: boolean;
  /** Order by the axis dims ascending when the element has no explicit sort. */
  sortByDims?: boolean;
  /** Hide axis items whose metrics are all null (default true; viz.showEmpty
   *  turns it off per element). */
  dropEmpty?: boolean;
  /** Hide ordering controls when the visual consumes one scalar cell. */
  sortable?: boolean;
}

/** Per-element header controls this visual supports; the dashboard's
 *  controls spec can still hide them globally. */
export interface ControlSpec {
  funnel?: boolean;
  csv?: boolean;
  /** Eraser that drops this element's slicer selection. Unlike funnel/csv it
   *  is not chrome the document can hide — clearing a filter is the only way
   *  back out of one. */
  clear?: boolean;
}

/** Inputs a cartesian visual turns into its Recharts series. */
export interface SeriesContext {
  viz: VizSettings;
  palette: string[];
  /** Plotted keys: metric columns, or the split dim's values. */
  keys: string[];
  /** True when keys are series-split dim values rather than measures. */
  split: boolean;
}

export interface VisualMeta {
  label: string;
  icon: JSX.Element;
  /** Size a freshly inserted element gets. */
  size: { w: number; h: number };
  /** false = inserted without a default title (text / image). */
  titled?: boolean;
  /** Element fields seeded on insert, beyond id/type/layout/title/query. */
  seed?: () => Partial<DashElement>;
  /** Present = the visual runs a DUX query. */
  data?: DataSpec;
  options?: OptionSpec[];
  controls?: ControlSpec;
  /** Set by visuals rendered through CartesianChart. */
  cartesian?: {
    /** Honors viz.orientation (category on the y axis). */
    orientable?: boolean;
  };
  series?: (ctx: SeriesContext) => SeriesSpec[];
  /** Transparent element container (the map paints its own surface). */
  bare?: boolean;
  /** Live-refresh probe: is a fetch for this element in flight? Visuals with a
   *  `data` spec are covered by the shared element query and need not declare
   *  it; the ones running their own queries (slicer, map) do. Hits the same
   *  cache entry the body already reads, so it costs no extra request. */
  useFetching?: (el: DashElement) => boolean;
}

export interface StaticBodyProps {
  el: DashElement;
}

/** Props every query-backed body receives. The shared pipeline in ElementBody
 *  does the column inference, empty-row drop, chart pivot and cross-filter
 *  wiring once, so a body only renders. */
export interface DataBodyProps extends StaticBodyProps {
  /** Query result after the empty-row drop. */
  data: QueryResponse;
  /** Measure formats keyed by plotted key (split series inherit the metric's). */
  formats: Record<string, MeasureFormat>;
  palette: string[];
  /** Resolved theme text color, for chrome a visual draws itself. */
  textColor: string;
  viz: VizSettings;
  /** Chart rows (empty unless data.chart). */
  rows: ChartRow[];
  /** Plotted keys (empty unless data.chart). */
  keys: string[];
  /** Metric output columns, in query order. */
  metricCols: string[];
  /** Resolved legend visibility (viz.legend, else multi-series). */
  legend: boolean;
  interaction: Interaction;
  meta: VisualMeta;
}

export interface VisualDef extends VisualMeta {
  type: ElementType;
  /** Query visuals get DataBodyProps; the rest only receive `el`. */
  Body: ComponentType<DataBodyProps> | ComponentType<StaticBodyProps>;
}
