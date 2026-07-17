// Dashboard document types — mirror dash/schema.json (document format v1).

export type ElementType =
  | "bar" | "line" | "area" | "donut" | "table" | "pivot" | "kpi" | "slicer" | "text";

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

export interface SlicerConfig {
  table: string;
  column: string;
  kind: "list" | "dropdown" | "range" | "daterange";
  multi?: boolean;
}

export interface DashElement {
  id: string;
  type: ElementType;
  layout: Layout;
  title?: ElementTitle;
  query?: ElementQuery;
  viz?: Record<string, unknown>;
  text?: { markdown?: string };
  slicer?: SlicerConfig;
  interactions?: { ignoreSlicers?: string[] };
}

export type BackgroundFit = "cover" | "contain" | "fill" | "tile";

export interface CanvasBackground {
  color?: string;
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

export interface Dashboard {
  $schema?: string;
  version: 1;
  canvas: CanvasSpec;
  theme?: Record<string, unknown>;
  refresh?: RefreshSpec;
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
