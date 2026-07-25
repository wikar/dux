// Donut chart: the first Values measure sliced across the axis categories.
import { DonutChartViz } from "../charts/ChartKit";
import { S, stroke } from "../glyphs";
import { CATEGORY, DATA_CONTROLS, LEGEND, SHOW_EMPTY } from "./common";
import type { DataBodyProps, VisualDef } from "./types";

function DonutBody({ rows, keys, formats, palette, viz, interaction }: DataBodyProps) {
  const metric = keys[0];
  if (!metric) return null;
  return (
    <DonutChartViz
      data={rows}
      metric={metric}
      palette={palette}
      formats={formats}
      // A single ring of slices is a legend's whole purpose.
      legend={viz.legend ?? true}
      interaction={interaction}
    />
  );
}

const donutChart: VisualDef = {
  type: "donut",
  label: "Donut chart",
  icon: (
    <svg {...S}>
      <circle cx="9" cy="9" r="6" {...stroke} strokeWidth="3.5" />
    </svg>
  ),
  size: { w: 280, h: 240 },
  controls: DATA_CONTROLS,
  data: {
    wells: [{ ...CATEGORY, max: 1 }, { id: "values", label: "Value", max: 1 }],
    chart: true,
  },
  options: [SHOW_EMPTY, LEGEND],
  Body: DonutBody,
};

export default donutChart;
