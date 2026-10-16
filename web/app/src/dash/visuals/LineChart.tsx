// Line chart: one line per measure, with an optional secondary right axis, or
// one line per value of a "Series by" dim.
import { renderLineSeries } from "../charts/series";
import type { SeriesContext, SeriesSpec } from "../charts/seriesTypes";
import { S, stroke } from "../glyphs";
import CartesianFrame from "../charts/CartesianFrame";
import { AXIS, color, DATA_CONTROLS, LEGEND, SERIES_BY, SHOW_EMPTY, VALUES } from "./common";
import type { DataBodyProps, QueryVisualDef } from "./types";

const lineChart: QueryVisualDef = {
  type: "line",
  label: "Line chart",
  icon: (
    <svg {...S}>
      <polyline points="2,13 6.5,7 10.5,10 16,3" {...stroke} />
    </svg>
  ),
  size: { w: 360, h: 240 },
  controls: DATA_CONTROLS,
  data: {
    wells: [AXIS, VALUES, { id: "y2", label: "Values · right axis" }, SERIES_BY],
    chart: true,
    seriesSplit: true,
    sortByDims: true,
  },
  options: [SHOW_EMPTY, { key: "zoomY", label: "Fit vertical range to data", kind: "check", default: false }, { key: "smoothLines", label: "Smooth lines", kind: "check", default: false }, LEGEND],
  Body: LineChartBody,
};

export default lineChart;

export function buildLineSeries(ctx: SeriesContext): SeriesSpec[] {
  // Split series are dim values, never right-axis metrics. Left series are
  // colored first so the palette order matches the plotted order.
  const y2 = new Set(ctx.split ? [] : ctx.viz.y2 ?? []);
  const ordered = [...ctx.keys.filter((k) => !y2.has(k)), ...ctx.keys.filter((k) => y2.has(k))];
  return ordered.map((key, i) => ({
    key,
    kind: "line",
    color: color(ctx.palette, i),
    axis: y2.has(key) ? "right" : "left",
    smooth: ctx.viz.smoothLines,
    dot: { base: "auto", highlight: true },
  }));
}

function LineChartBody({ rows, keys, formats, palette, textColor, viz, legend, interaction }: DataBodyProps) {
  const series = buildLineSeries({ viz, palette, keys, split: !!interaction.seriesDim });
  return (
    <CartesianFrame data={rows} series={series} formats={formats} legend={legend} interaction={interaction} zoomY={viz.zoomY}>
      {series.map((s) => renderLineSeries(s, { data: rows, interaction, markStroke: textColor }))}
    </CartesianFrame>
  );
}
