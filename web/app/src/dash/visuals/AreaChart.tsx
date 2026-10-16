// Area chart: overlapping or stacked bands, optionally split into one band per
// value of a "Series by" dim.
import { renderAreaSeries } from "../charts/series";
import type { SeriesContext, SeriesSpec } from "../charts/seriesTypes";
import { S } from "../glyphs";
import CartesianFrame from "../charts/CartesianFrame";
import { CATEGORY, color, DATA_CONTROLS, LEGEND, SERIES_BY, SHOW_EMPTY, stackId, STACKED, VALUES } from "./common";
import type { DataBodyProps, QueryVisualDef } from "./types";

const areaChart: QueryVisualDef = {
  type: "area",
  label: "Area chart",
  icon: (
    <svg {...S}>
      <path d="M2 15 L2 11 L6.5 6 L10.5 9 L16 3 L16 15 Z" fill="currentColor" opacity="0.75" />
    </svg>
  ),
  size: { w: 360, h: 240 },
  controls: DATA_CONTROLS,
  data: { wells: [CATEGORY, VALUES, SERIES_BY], chart: true, seriesSplit: true, sortByDims: true },
  options: [SHOW_EMPTY, STACKED, LEGEND],
  Body: AreaChartBody,
};

export default areaChart;

export function buildAreaSeries(ctx: SeriesContext): SeriesSpec[] {
  return ctx.keys.map((key, i) => ({
    key,
    kind: "area",
    color: color(ctx.palette, i),
    stackId: stackId(ctx),
    fillOpacity: ctx.viz.stacked ? 0.7 : 0.3,
    // Only the selected point gets a marker; the band carries the shape.
    dot: { base: 0, highlight: true },
  }));
}

function AreaChartBody({ rows, keys, formats, palette, textColor, viz, legend, interaction }: DataBodyProps) {
  const series = buildAreaSeries({ viz, palette, keys, split: !!interaction.seriesDim });
  return (
    <CartesianFrame data={rows} series={series} formats={formats} legend={legend} interaction={interaction}>
      {series.map((s) => renderAreaSeries(s, { data: rows, interaction, markStroke: textColor }))}
    </CartesianFrame>
  );
}
