// Chart kit: thin adapters translating lib-agnostic element.viz settings +
// theme palette into Recharts components. Persisted documents never
// reference Recharts — swapping engines means new adapters, not migrations.
import {
  Bar,
  CartesianGrid,
  ComposedChart,
  Legend,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { formatValue } from "@dux/core";
import type { MeasureFormat, QueryResponse } from "@dux/core";

// Catppuccin chrome (literal values — SVG attributes can't resolve CSS vars).
const GRID = "#313244";
const AXIS = "#a6adc8";
const TOOLTIP_STYLE = {
  backgroundColor: "#181825",
  border: "1px solid #45475a",
  borderRadius: 6,
  fontSize: 12,
} as const;

export type ChartRow = Record<string, string | number | null>;

/** Pivot a query result into Recharts rows: one row per result row, __x =
 *  the joined dim values, one numeric key per metric column. */
export function toChartData(res: QueryResponse, dimCols: string[], metricCols: string[]): ChartRow[] {
  const dimIdx = dimCols.map((c) => res.columns.indexOf(c)).filter((i) => i >= 0);
  const metIdx = metricCols
    .map((c) => [c, res.columns.indexOf(c)] as const)
    .filter(([, i]) => i >= 0);
  return res.rows.map((r) => {
    const row: ChartRow = { __x: dimIdx.map((i) => String(r[i] ?? "")).join(" · ") };
    for (const [c, i] of metIdx) {
      const v = r[i];
      row[c] = v === null || v === undefined ? null : Number(v);
    }
    return row;
  });
}

type Formats = Record<string, MeasureFormat>;

const compact = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 });

/** Axis tick formatter using the first series' measure format (falls back
 *  to compact notation so large values stay readable). */
function tickFormatter(formats: Formats, series: string[]) {
  const fmt = series.map((s) => formats[s]).find(Boolean);
  return (v: number) => (fmt ? formatValue(v, fmt) : compact.format(v));
}

function tooltipFormatter(formats: Formats) {
  return (value: unknown, name: unknown): [string, string] => {
    const n = String(name ?? "");
    const v = typeof value === "number" ? value : String(value ?? "");
    return [formatValue(v, formats[n]) || String(v), n];
  };
}

const commonAxis = { stroke: AXIS, tick: { fill: AXIS, fontSize: 10 }, tickLine: false } as const;

interface BaseProps {
  data: ChartRow[];
  palette: string[];
  formats: Formats;
  legend?: boolean;
}

// ─── Bar (clustered / stacked, vertical / horizontal) ────────────────────────

interface BarProps extends BaseProps {
  series: string[];
  orientation?: "vertical" | "horizontal";
  stacked?: boolean;
}

export function BarChartViz({ data, series, palette, formats, orientation, stacked, legend }: BarProps) {
  const horizontal = orientation === "horizontal";
  const valueFmt = tickFormatter(formats, series);
  return (
    <ResponsiveContainer width="100%" height="100%">
      <ComposedChart
        data={data}
        layout={horizontal ? "vertical" : "horizontal"}
        margin={{ top: 8, right: 12, bottom: 4, left: 4 }}
      >
        <CartesianGrid stroke={GRID} strokeDasharray="3 3" horizontal={!horizontal} vertical={horizontal} />
        {horizontal ? (
          <>
            <XAxis type="number" {...commonAxis} tickFormatter={valueFmt} />
            <YAxis type="category" dataKey="__x" {...commonAxis} width={92} />
          </>
        ) : (
          <>
            <XAxis type="category" dataKey="__x" {...commonAxis} />
            <YAxis type="number" {...commonAxis} tickFormatter={valueFmt} width={48} />
          </>
        )}
        <Tooltip formatter={tooltipFormatter(formats)} contentStyle={TOOLTIP_STYLE} cursor={{ fill: "#31324455" }} />
        {legend && <Legend wrapperStyle={{ fontSize: 11 }} />}
        {series.map((s, i) => (
          <Bar
            key={s}
            dataKey={s}
            fill={palette[i % palette.length]}
            stackId={stacked ? "stack" : undefined}
            radius={stacked ? 0 : 2}
            isAnimationActive={false}
          />
        ))}
      </ComposedChart>
    </ResponsiveContainer>
  );
}

// ─── Line (optional secondary y axis) ────────────────────────────────────────

interface LineProps extends BaseProps {
  left: string[];
  right: string[];
}

export function LineChartViz({ data, left, right, palette, formats, legend }: LineProps) {
  const all = [...left, ...right];
  return (
    <ResponsiveContainer width="100%" height="100%">
      <ComposedChart data={data} margin={{ top: 8, right: 12, bottom: 4, left: 4 }}>
        <CartesianGrid stroke={GRID} strokeDasharray="3 3" vertical={false} />
        <XAxis type="category" dataKey="__x" {...commonAxis} />
        <YAxis yAxisId="left" type="number" {...commonAxis} tickFormatter={tickFormatter(formats, left)} width={48} />
        {right.length > 0 && (
          <YAxis
            yAxisId="right"
            orientation="right"
            type="number"
            {...commonAxis}
            tickFormatter={tickFormatter(formats, right)}
            width={48}
          />
        )}
        <Tooltip formatter={tooltipFormatter(formats)} contentStyle={TOOLTIP_STYLE} />
        {legend && <Legend wrapperStyle={{ fontSize: 11 }} />}
        {all.map((s, i) => (
          <Line
            key={s}
            dataKey={s}
            yAxisId={right.includes(s) ? "right" : "left"}
            stroke={palette[i % palette.length]}
            strokeWidth={2}
            dot={data.length <= 40}
            isAnimationActive={false}
          />
        ))}
      </ComposedChart>
    </ResponsiveContainer>
  );
}

// ─── Combo (bars + lines, lines optionally on the right axis) ────────────────

interface ComboProps extends BaseProps {
  bars: string[];
  lines: string[];
  /** Put line series on a secondary right axis (default true). */
  lineY2?: boolean;
}

export function ComboChartViz({ data, bars, lines, lineY2 = true, palette, formats, legend }: ComboProps) {
  const rightAxis = lineY2 && lines.length > 0;
  return (
    <ResponsiveContainer width="100%" height="100%">
      <ComposedChart data={data} margin={{ top: 8, right: 12, bottom: 4, left: 4 }}>
        <CartesianGrid stroke={GRID} strokeDasharray="3 3" vertical={false} />
        <XAxis type="category" dataKey="__x" {...commonAxis} />
        <YAxis yAxisId="left" type="number" {...commonAxis} tickFormatter={tickFormatter(formats, bars)} width={48} />
        {rightAxis && (
          <YAxis
            yAxisId="right"
            orientation="right"
            type="number"
            {...commonAxis}
            tickFormatter={tickFormatter(formats, lines)}
            width={48}
          />
        )}
        <Tooltip formatter={tooltipFormatter(formats)} contentStyle={TOOLTIP_STYLE} cursor={{ fill: "#31324455" }} />
        {legend && <Legend wrapperStyle={{ fontSize: 11 }} />}
        {bars.map((s, i) => (
          <Bar key={s} dataKey={s} yAxisId="left" fill={palette[i % palette.length]} radius={2} isAnimationActive={false} />
        ))}
        {lines.map((s, i) => (
          <Line
            key={s}
            dataKey={s}
            yAxisId={rightAxis ? "right" : "left"}
            stroke={palette[(bars.length + i) % palette.length]}
            strokeWidth={2}
            dot={data.length <= 40}
            isAnimationActive={false}
          />
        ))}
      </ComposedChart>
    </ResponsiveContainer>
  );
}
