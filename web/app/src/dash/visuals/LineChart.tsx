// Line chart: one line per measure, with an optional secondary right axis, or
// one line per value of a "Series by" dim.
import { S, stroke } from "../glyphs";
import CartesianBody from "./CartesianBody";
import { AXIS, color, DATA_CONTROLS, LEGEND, SERIES_BY, SHOW_EMPTY, VALUES } from "./common";
import type { VisualDef } from "./types";

const lineChart: VisualDef = {
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
  options: [SHOW_EMPTY, LEGEND],
  series: (ctx) => {
    // Split series are dim values, never right-axis metrics. Left series are
    // colored first so the palette order matches the plotted order.
    const y2 = new Set(ctx.split ? [] : ctx.viz.y2 ?? []);
    const ordered = [...ctx.keys.filter((k) => !y2.has(k)), ...ctx.keys.filter((k) => y2.has(k))];
    return ordered.map((key, i) => ({
      key,
      kind: "line",
      color: color(ctx.palette, i),
      axis: y2.has(key) ? "right" : "left",
      dot: { base: "auto", highlight: true },
    }));
  },
  Body: CartesianBody,
};

export default lineChart;
