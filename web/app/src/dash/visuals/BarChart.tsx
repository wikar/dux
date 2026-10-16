// Bar chart: clustered or stacked, vertical or horizontal, optionally split
// into one series per value of a "Series by" dim.
import { renderBarSeries } from "../charts/series";
import type { SeriesContext, SeriesSpec } from "../charts/seriesTypes";
import { S } from "../glyphs";
import CartesianFrame from "../charts/CartesianFrame";
import { AXIS, color, DATA_CONTROLS, LEGEND, SERIES_BY, SHOW_EMPTY, stackId, STACKED, VALUES } from "./common";
import type { DataBodyProps, QueryVisualDef } from "./types";

const barChart: QueryVisualDef = {
  type: "bar",
  label: "Bar chart",
  icon: (
    <svg {...S}>
      <rect x="2" y="9" width="3.4" height="7" fill="currentColor" />
      <rect x="7.3" y="4" width="3.4" height="12" fill="currentColor" />
      <rect x="12.6" y="7" width="3.4" height="9" fill="currentColor" />
    </svg>
  ),
  size: { w: 360, h: 240 },
  controls: DATA_CONTROLS,
  data: { wells: [AXIS, VALUES, SERIES_BY], chart: true, seriesSplit: true },
  options: [
    SHOW_EMPTY,
    {
      key: "orientation",
      label: "Orientation",
      kind: "select",
      default: "vertical",
      choices: [
        { value: "vertical", label: "Vertical" },
        { value: "horizontal", label: "Horizontal" },
      ],
    },
    STACKED,
    LEGEND,
  ],
  Body: BarChartBody,
};

export default barChart;

export function buildBarSeries(ctx: SeriesContext): SeriesSpec[] {
  return ctx.keys.map((key, i) => ({
    key,
    kind: "bar",
    color: color(ctx.palette, i),
    stackId: stackId(ctx),
    radius: ctx.viz.stacked ? 0 : 2,
  }));
}

function BarChartBody({ rows, keys, formats, palette, viz, legend, interaction }: DataBodyProps) {
  const series = buildBarSeries({ viz, palette, keys, split: !!interaction.seriesDim });
  const horizontal = viz.orientation === "horizontal";
  return (
    <CartesianFrame data={rows} series={series} formats={formats} legend={legend} interaction={interaction} horizontal={horizontal}>
      {series.map((s) => renderBarSeries(s, { data: rows, interaction, horizontal }))}
    </CartesianFrame>
  );
}
