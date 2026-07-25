// Combo chart: bars plus lines, the lines optionally on a secondary right axis.
import type { SeriesSpec } from "../charts/ChartKit";
import { S, stroke } from "../glyphs";
import CartesianBody from "./CartesianBody";
import { AXIS, color, DATA_CONTROLS, LEGEND, SHOW_EMPTY } from "./common";
import type { VisualDef } from "./types";

const comboChart: VisualDef = {
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
  options: [SHOW_EMPTY, { key: "lineY2", label: "Lines on right axis", kind: "check", default: true }, LEGEND],
  series: (ctx) => {
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
      dot: { base: "auto", highlight: true },
    }));
    return [...bars, ...lines];
  },
  Body: CartesianBody,
};

export default comboChart;
