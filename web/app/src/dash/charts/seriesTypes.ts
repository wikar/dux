import type { VizSettings } from "../types";

/** Inputs a cartesian visual turns into its Recharts series. */
export interface SeriesContext {
  viz: VizSettings;
  palette: string[];
  /** Plotted keys: metric columns, or the split dim's values. */
  keys: string[];
  /** True when keys are series-split dim values rather than measures. */
  split: boolean;
}

export type SeriesKind = "bar" | "line" | "area";

export interface SeriesSpec {
  /** Row key plotted by this series. */
  key: string;
  kind: SeriesKind;
  color: string;
  /** Secondary y axis; the axis is rendered only when a series asks for it. */
  axis?: "left" | "right";
  /** Shared id stacks series instead of clustering/overlapping them. */
  stackId?: string;
  /** bar: corner radius. */
  radius?: number;
  /** area: band opacity. */
  fillOpacity?: number;
  /** line: use a natural cubic spline through the points. */
  smooth?: boolean;
  /** line / area point markers. base "auto" shows them only on sparse data;
   *  highlight enlarges the marks that are cross-filter selected. */
  dot?: { base: number | "auto"; highlight?: boolean };
}
