// Donut chart: the first Values measure sliced across the axis categories.
import { Cell, Legend, Pie, PieChart, ResponsiveContainer, Tooltip } from "recharts";
import { formatValue } from "@dux/core";
import { TEXT, TOOLTIP_STYLE, TOOLTIP_LABEL_STYLE, LEGEND_STYLE } from "../charts/chartStyle";
import { markClickHandler, markDims, markOpacity } from "../charts/series";
import { S, stroke } from "../glyphs";
import { CATEGORY, DATA_CONTROLS, LEGEND, SHOW_EMPTY } from "./common";
import type { DataBodyProps, QueryVisualDef } from "./types";

function DonutChartBody({ rows: data, keys, formats, palette, viz, interaction }: DataBodyProps) {
  const metric = keys[0];
  if (!metric) return null;
  // A single ring of slices is a legend's whole purpose.
  const legend = viz.legend ?? true;
  const fmt = formats[metric];
  const total = data.reduce((sum, r) => sum + (typeof r[metric] === "number" ? (r[metric] as number) : 0), 0);
  const click = interaction?.onMarkClick;
  return (
    <ResponsiveContainer width="100%" height="100%">
      <PieChart margin={{ top: 8, right: 8, bottom: 4, left: 8 }}>
        <Pie
          data={data}
          dataKey={metric}
          nameKey="__x"
          innerRadius="55%"
          outerRadius="85%"
          paddingAngle={1}
          stroke="none"
          isAnimationActive={false}
          cursor={click ? "pointer" : undefined}
          onClick={markClickHandler(click)}
        >
          {data.map((row, i) => (
            <Cell
              key={i}
              fill={palette[i % palette.length]}
              fillOpacity={markOpacity(interaction?.selectedKeys, markDims(row))}
            />
          ))}
        </Pie>
        {/* Category values format like the sliced metric. */}
        <Tooltip
          formatter={(value: unknown, name: unknown): [string, string] => {
            const v = typeof value === "number" ? value : String(value ?? "");
            return [formatValue(v, fmt) || String(v), String(name ?? "")];
          }}
          contentStyle={TOOLTIP_STYLE}
          labelStyle={TOOLTIP_LABEL_STYLE}
        />
        {legend && <Legend wrapperStyle={LEGEND_STYLE} />}
        <text x="50%" y="50%" textAnchor="middle" dominantBaseline="middle" fill={TEXT} fontSize={13} fontWeight={600}>
          {formatValue(total, fmt)}
        </text>
      </PieChart>
    </ResponsiveContainer>
  );
}

const donutChart: QueryVisualDef = {
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
  Body: DonutChartBody,
};

export default donutChart;
