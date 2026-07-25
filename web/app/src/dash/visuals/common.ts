// Registry fragments shared by more than one visual. A visual composes these
// into its own entry rather than restating them.
import type { OptionSpec, SeriesContext, WellSpec } from "./types";

const SERIES_HINT = "Drop a column — Splits the first Values measure into one series per value";

export const AXIS: WellSpec = { id: "axis", label: "Axis" };
export const CATEGORY: WellSpec = { id: "axis", label: "Category" };
export const VALUES: WellSpec = { id: "values", label: "Values" };
export const SERIES_BY: WellSpec = { id: "series", label: "Series by", max: 1, hint: SERIES_HINT };

export const SHOW_EMPTY: OptionSpec = {
  key: "showEmpty",
  label: "Show items with no data",
  kind: "check",
  default: false,
};
export const STACKED: OptionSpec = { key: "stacked", label: "Stacked", kind: "check", default: false };
export const LEGEND: OptionSpec = {
  key: "legend",
  label: "Legend",
  kind: "tri",
  autoLabel: "Auto (multi-series)",
};

/** Every query visual answers the same header controls. */
export const DATA_CONTROLS = { funnel: true, csv: true } as const;

/** Cycle the palette so a series index always resolves to a color. */
export const color = (palette: string[], i: number) => palette[i % palette.length];

/** Bar/area series share one stack id when viz.stacked is on. */
export const stackId = (ctx: SeriesContext) => (ctx.viz.stacked ? "stack" : undefined);
