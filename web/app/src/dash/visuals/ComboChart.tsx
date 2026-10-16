// Combo chart: bars plus lines, the lines optionally on a secondary right axis.
import { renderBarSeries, renderLineSeries } from "../charts/series";
import type { SeriesContext, SeriesSpec } from "../charts/seriesTypes";
import { S, stroke } from "../glyphs";
import CartesianFrame from "../charts/CartesianFrame";
import { AXIS, color, DATA_CONTROLS, LEGEND, SHOW_EMPTY } from "./common";
import type { DataBodyProps, QueryVisualDef } from "./types";

const comboChart: QueryVisualDef = {
  type: "combo",
  label: "Combo chart",
  icon: (
    <svg {...S}>
      <rect x="2.5" y="9" width="3" height="7" fill="currentColor" opacity="0.7" />
      <rect x="7.5" y="6" width="3" height="10" fill="currentColor" opacity="0.7" />
      <rect x="12.5" y="11" width="3" height="5" fill="currentColor" opacity="0.7" />
      <polyline points="2,8 8,3 15,6" {...stroke} />
    </svg>
  ),
  size: { w: 420, h: 260 },
  controls: DATA_CONTROLS,
  data: {
    wells: [AXIS, { id: "bars", label: "Bars" }, { id: "lines", label: "Lines" }],
    chart: true,
    sortByDims: true,
  },
  options: [SHOW_EMPTY, { key: "zoomY", label: "Fit vertical range to data", kind: "check", default: false }, { key: "smoothLines", label: "Smooth lines", kind: "check", default: false }, { key: "lineY2", label: "Lines on right axis", kind: "check", default: true }, LEGEND],
  Body: ComboChartBody,
};

export default comboChart;

export function buildComboSeries(ctx: SeriesContext): SeriesSpec[] {
  const lineKeys = (ctx.viz.lines ?? []).filter((n) => ctx.keys.includes(n));
  const barKeys = ctx.keys.filter((k) => !lineKeys.includes(k));
  const right = (ctx.viz.lineY2 ?? true) && lineKeys.length > 0;
  const bars: SeriesSpec[] = barKeys.map((key, i) => ({
    key,
    kind: "bar",
    color: color(ctx.palette, i),
    radius: 2,
  }));
  // Lines continue the palette where the bars left off.
  const lines: SeriesSpec[] = lineKeys.map((key, i) => ({
    key,
    kind: "line",
    color: color(ctx.palette, barKeys.length + i),
    axis: right ? "right" : "left",
    smooth: ctx.viz.smoothLines,
    dot: { base: "auto", highlight: true },
  }));
  return [...bars, ...lines];
}

function ComboChartBody({ rows, keys, formats, palette, textColor, viz, legend, interaction }: DataBodyProps) {
  const series = buildComboSeries({ viz, palette, keys, split: !!interaction.seriesDim });
  return (
    <CartesianFrame data={rows} series={series} formats={formats} legend={legend} interaction={interaction} zoomY={viz.zoomY}>
      {series.filter((s) => s.kind === "bar").map((s) => renderBarSeries(s, { data: rows, interaction, markStroke: textColor }))}
      {series.filter((s) => s.kind === "line").map((s) => renderLineSeries(s, { data: rows, interaction, markStroke: textColor }))}
    </CartesianFrame>
  );
}
