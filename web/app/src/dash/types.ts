// Dashboard document types — mirror dash/schema.json (document format v1).

export type ElementType =
  | "bar" | "line" | "combo" | "area" | "donut" | "table" | "pivot" | "kpi"
  | "slicer" | "text" | "image" | "map";

// Which types are query-backed, how they render and what they expose is
// declared once per visual under visuals/ (QUERY_TYPES is derived there).

export interface Layout {
  x: number;
  y: number;
  w: number;
  h: number;
  z?: number;
}

export interface ElementTitle {
  text?: string;
  show?: boolean;
}

export interface BuilderFieldRef {
  table: string;
  name: string;
  kind?: "column" | "measure";
  dataType?: string;
  aggregate?: string;
}

export type MapLayerKind = "circle" | "pin" | "heatmap";

export interface MapLayer {
  id: string;
  kind: MapLayerKind;
  lng?: BuilderFieldRef;
  lat?: BuilderFieldRef;
  size?: BuilderFieldRef;
  category?: BuilderFieldRef;
}

export interface MapViz {
  layers?: MapLayer[];
  center?: [number, number];
  zoom?: number;
}

/** Chart display settings (element.viz — deliberately open in the schema).
 *  Metric wells reference output column names (measure name / column alias). */
export interface VizSettings extends MapViz {
  /** bar: horizontal renders category on the y axis. */
  orientation?: "vertical" | "horizontal";
  /** bar / area: stack series instead of clustering/overlapping. */
  stacked?: boolean;
  /** line: metric names plotted on the secondary (right) y axis. */
  y2?: string[];
  /** combo: metric names rendered as lines (the rest are bars). */
  lines?: string[];
  /** combo: put line series on the secondary (right) y axis. Default true. */
  lineY2?: boolean;
  legend?: boolean;
  /** Show axis items whose metrics are all null (hidden by default). */
  showEmpty?: boolean;
  /** bar / line / area: dim whose values split the first Values measure
   *  into one series each (PBI's "Legend" well). */
  series?: string;
  /** pivot: dim names pivoted to columns (the rest are rows). */
  cols?: string[];
  /** pivot: subtotal rows per row-group level (default true). */
  subtotals?: boolean;
  /** pivot: row groups start collapsed (default true; false = expanded). */
  collapsed?: boolean;
  /** pivot: grand-total row (default true). */
  grandTotal?: boolean;
  /** pivot: total column across the column dims (default true). */
  totalCol?: boolean;
}

export interface BuilderFilterRef {
  table: string;
  name: string;
  dataType?: string;
  op?: string;
  value?: string;
}

export interface ElementQuery {
  mode: "builder" | "raw";
  fields?: BuilderFieldRef[];
  filters?: BuilderFilterRef[];
  sort?: { field: string; dir?: "asc" | "desc" }[];
  topN?: number | null;
  raw?: string | null;
}

export type SlicerKind = "buttons" | "dropdown" | "range" | "daterange";

export interface SlicerConfig {
  table: string;
  column: string;
  /** DuckDB column type — shapes filter value types and the range kinds. */
  dataType?: string;
  kind: SlicerKind;
  multi?: boolean;
  /** Max value pills shown by the buttons kind (default 20). */
  limit?: number;
  /** Option list order by column value (default "asc"). */
  sort?: "asc" | "desc";
  /** Preset selection applied on open (a ?f= selection wins over it). It is
   *  configuration, not session state: values missing from the data are
   *  dropped from the live selection on load, never from the preset. */
  default?: SlicerSelection;
  /** Optional metric that trims the option list to non-null rows: a measure,
   *  or a numeric column with an aggregate. */
  measure?: {
    table: string;
    name: string;
    kind?: "column" | "measure";
    dataType?: string;
    aggregate?: string;
  };
}

/** A slicer's runtime selection (shared via the ?f= deep-link parameter; the
 *  document stores only the preset that seeds it — SlicerConfig.default). */
export type SlicerSelection =
  | { kind: "values"; values: string[] }
  | { kind: "range"; from?: string; to?: string };

export type ImageFit = "contain" | "cover" | "fill";

export interface ImageConfig {
  /** Image URL (external or an /api/dash/assets/... path). */
  url?: string;
  fit?: ImageFit;
}

export interface DashElement {
  id: string;
  type: ElementType;
  layout: Layout;
  title?: ElementTitle;
  query?: ElementQuery;
  viz?: VizSettings & Record<string, unknown>;
  text?: { markdown?: string };
  image?: ImageConfig;
  slicer?: SlicerConfig;
  interactions?: { ignoreSlicers?: string[] };
}

export type BackgroundFit = "cover" | "contain" | "fill" | "tile";

/** Legacy per-dashboard background (M3/M4 documents). Since M4.5 the theme
 *  tokens (background/backgroundImage/backgroundFit) own this — the renderer
 *  still honors these fields, and the theme editor clears them on write. */
export interface CanvasBackground {
  color?: string;
  /** Background image URL (external or an /api/dash/assets/... path). */
  url?: string | null;
  /** Legacy asset path (pre-URL uploads); url wins when both are set. */
  asset?: string | null;
  fit?: BackgroundFit;
}

export interface CanvasSpec {
  width: number;
  height: number;
  background?: CanvasBackground;
}

export interface RefreshSpec {
  enabled: boolean;
  intervalSeconds: number;
}

/** Theme tokens. Cascade per token: defaults ← dashboards/theme.json ←
 *  dashboard.theme. All colors accept any CSS color, including alpha
 *  (#rrggbbaa / rgba(...)). */
export interface ThemeTokens {
  /** Data series colors, prioritized left → right (bars, lines, …). */
  palette?: string[];
  /** Canvas background color (legacy canvas.background wins when present). */
  background?: string;
  /** Canvas background image URL (legacy canvas.background url/asset wins). */
  backgroundImage?: string;
  /** Background image sizing. */
  backgroundFit?: "cover" | "contain" | "fill" | "tile";
  /** Element container background. */
  elementBackground?: string;
  /** Element title-bar background. */
  titleBackground?: string;
  /** Element border color. */
  border?: string;
  /** Text color inside the canvas. */
  text?: string;
  /** Font family for the canvas. */
  fontFamily?: string;
}

/** Visibility of the per-element header controls (both default to shown). */
export interface ControlsSpec {
  csv?: boolean;
  funnel?: boolean;
}

export interface Dashboard {
  $schema?: string;
  version: 1;
  canvas: CanvasSpec;
  theme?: Record<string, unknown>;
  refresh?: RefreshSpec;
  controls?: ControlsSpec;
  elements: DashElement[];
}

/** A fresh empty dashboard on the standard 1280×720 canvas. */
export function newDashboard(): Dashboard {
  return {
    version: 1,
    canvas: { width: 1280, height: 720, background: { color: "#1e1e2e" } },
    elements: [],
  };
}
