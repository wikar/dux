// Chart kit: thin adapters translating lib-agnostic element.viz settings +
// theme palette into Recharts components. Persisted documents never
// reference Recharts — swapping engines means new adapters, not migrations.
import {
  Area,
  Bar,
  CartesianGrid,
  Cell,
  ComposedChart,
  Legend,
  Line,
  Pie,
  PieChart,
  ResponsiveContainer,
  Text,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { XAxisTickContentProps } from "recharts";
import { formatValue } from "@dux/core";
import type { MeasureFormat, QueryResponse } from "@dux/core";
import { markKey, type CrossMark } from "../store";

// Catppuccin chrome (literal values — SVG attributes can't resolve CSS vars).
const GRID = "#313244";
const AXIS = "#a6adc8";
const TOOLTIP_STYLE = {
  backgroundColor: "#181825",
  border: "1px solid #45475a",
  borderRadius: 6,
  fontSize: 12,
} as const;

/** One dim column→value pair carried by a chart row, so a clicked mark can be
 *  turned back into a cross-filter (the joined __x string loses this). */
export type ChartDim = { table: string; column: string; value: string | number };

export interface ChartRow {
  /** Joined dim values — the category axis / slice label. */
  __x: string;
  /** Structured dim values behind __x (absent in raw mode). */
  __dims?: ChartDim[];
  [key: string]: string | number | null | ChartDim[] | undefined;
}

/** Pivot a query result into Recharts rows: one row per result row, __x =
 *  the joined dim values, __dims = the structured dim tuple, one numeric key
 *  per metric column. dimTables maps a dim column to its table. */
export function toChartData(
  res: QueryResponse,
  dimCols: string[],
  metricCols: string[],
  dimTables: Record<string, string> = {}
): ChartRow[] {
  const dimIdx = dimCols
    .map((c) => [c, res.columns.indexOf(c)] as const)
    .filter(([, i]) => i >= 0);
  const metIdx = metricCols
    .map((c) => [c, res.columns.indexOf(c)] as const)
    .filter(([, i]) => i >= 0);
  return res.rows.map((r) => {
    const dims: ChartDim[] = dimIdx.map(([c, i]) => ({
      table: dimTables[c] ?? "",
      column: c,
      value: (r[i] ?? "") as string | number,
    }));
    const row: ChartRow = { __x: dims.map((d) => String(d.value)).join(" · "), __dims: dims };
    for (const [c, i] of metIdx) {
      const v = r[i];
      row[c] = v === null || v === undefined ? null : Number(v);
    }
    return row;
  });
}

/** Series-split ("Legend") pivot: one row per axis value, one numeric key per
 *  distinct value of the series dim, all carrying metricCol's measure. Series
 *  are sorted; x keeps the server's row order. NULL series label as (blank). */
export function toSeriesData(
  res: QueryResponse,
  dimCols: string[],
  seriesCol: string,
  metricCol: string,
  dimTables: Record<string, string> = {}
): { data: ChartRow[]; series: string[] } {
  const dimIdx = dimCols
    .map((c) => [c, res.columns.indexOf(c)] as const)
    .filter(([, i]) => i >= 0);
  const sIdx = res.columns.indexOf(seriesCol);
  const mIdx = res.columns.indexOf(metricCol);
  if (sIdx < 0 || mIdx < 0) return { data: [], series: [] };
  const byX = new Map<string, ChartRow>();
  const series = new Set<string>();
  for (const r of res.rows) {
    const dims: ChartDim[] = dimIdx.map(([c, i]) => ({
      table: dimTables[c] ?? "",
      column: c,
      value: (r[i] ?? "") as string | number,
    }));
    const x = dims.map((d) => String(d.value)).join(" · ");
    const raw = r[sIdx];
    const s = raw === null || raw === undefined || raw === "" ? "(blank)" : String(raw);
    series.add(s);
    let row = byX.get(x);
    if (!row) {
      row = { __x: x, __dims: dims };
      byX.set(x, row);
    }
    const v = r[mIdx];
    row[s] = v === null || v === undefined ? null : Number(v);
  }
  return { data: [...byX.values()], series: [...series].sort((a, b) => a.localeCompare(b)) };
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

/** Compact ISO timestamps on axes; the underlying value remains untouched for
 * tooltips and cross-filtering. Multiple dimension labels are handled too. */
export function formatCategoryTick(value: unknown): string {
  return String(value ?? "").replace(
    /(\d{4}-\d{2}-\d{2})T(\d{2}):(\d{2}):\d{2}(?:\.\d+)?Z?/g,
    (_, date: string, hour: string, minute: string) =>
      hour === "00" && minute === "00" ? date : `${date} ${hour}:${minute}`
  );
}

const categoryAxis = {
  ...commonAxis,
  padding: { left: 12, right: 12 },
  minTickGap: 16,
  tickFormatter: formatCategoryTick,
} as const;

function lineCategoryTick({ x, y, payload, index, visibleTicksCount, className }: XAxisTickContentProps) {
  const inset = index === 0 ? 8 : index === visibleTicksCount - 1 ? -8 : 0;
  return (
    <Text
      className={className}
      x={Number(x) + inset}
      y={y}
      fill={AXIS}
      fontSize={10}
      textAnchor="middle"
      verticalAnchor="start"
    >
      {formatCategoryTick(payload.value)}
    </Text>
  );
}

// ─── Cross-filter interaction (click a mark → filter the other visuals) ──────

/** Called when a mark is clicked; additive = Ctrl/⌘ held. */
export type MarkClick = (dims: ChartDim[], additive: boolean) => void;

export interface Interaction {
  /** Present only when clicking should cross-filter (view mode, builder query). */
  onMarkClick?: MarkClick;
  /** markKey() values currently selected in THIS visual (for highlighting). */
  selectedKeys?: Set<string>;
  /** Series-split ("Legend") dim, so a clicked segment carries the series value. */
  seriesDim?: { table: string; column: string };
}

function additiveOf(e: unknown): boolean {
  const me = e as { ctrlKey?: boolean; metaKey?: boolean } | undefined;
  return !!(me?.ctrlKey || me?.metaKey);
}

/** Full dim tuple of a mark: the row's dims plus the series value when split. */
function markDims(row: ChartRow | undefined, seriesDim?: Interaction["seriesDim"], seriesVal?: string): ChartDim[] {
  const dims = [...(row?.__dims ?? [])];
  if (seriesDim && seriesVal !== undefined) dims.push({ ...seriesDim, value: seriesVal });
  return dims;
}

/** Opacity for a mark given the current selection: 1 when nothing is selected
 *  or this mark is selected, dimmed otherwise. */
function markOpacity(sel: Set<string> | undefined, dims: ChartDim[]): number {
  if (!sel || sel.size === 0) return 1;
  return sel.has(markKey({ dims } as CrossMark)) ? 1 : 0.25;
}

/** Row extracted from a Recharts click payload. */
function rowOf(d: unknown): ChartRow | undefined {
  const o = d as { payload?: ChartRow; __dims?: ChartDim[] } | undefined;
  return (o?.payload ?? (o as ChartRow)) || undefined;
}

/** A Recharts onClick handler that turns a clicked mark into a cross-filter.
 *  Uses rest args so it satisfies both the Bar/Pie and Curve handler types
 *  (which pass (data, index, event)). Returns undefined when not clickable. */
function markClickHandler(click: MarkClick | undefined, seriesDim?: Interaction["seriesDim"], seriesVal?: string) {
  if (!click) return undefined;
  return (...args: unknown[]) => click(markDims(rowOf(args[0]), seriesDim, seriesVal), additiveOf(args[2]));
}

interface BaseProps {
  data: ChartRow[];
  palette: string[];
  formats: Formats;
  legend?: boolean;
  interaction?: Interaction;
}

// ─── Bar (clustered / stacked, vertical / horizontal) ────────────────────────

interface BarProps extends BaseProps {
  series: string[];
  orientation?: "vertical" | "horizontal";
  stacked?: boolean;
}

export function BarChartViz({ data, series, palette, formats, orientation, stacked, legend, interaction }: BarProps) {
  const horizontal = orientation === "horizontal";
  const valueFmt = tickFormatter(formats, series);
  const split = interaction?.seriesDim; // when set, each series key is a dim value
  const click = interaction?.onMarkClick;
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
            <YAxis type="category" dataKey="__x" {...commonAxis} tickFormatter={formatCategoryTick} width={92} />
          </>
        ) : (
          <>
            <XAxis type="category" dataKey="__x" {...categoryAxis} />
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
            cursor={click ? "pointer" : undefined}
            onClick={markClickHandler(click, split, split ? s : undefined)}
          >
            {data.map((row, ri) => (
              <Cell
                key={ri}
                fill={palette[i % palette.length]}
                fillOpacity={markOpacity(interaction?.selectedKeys, markDims(row, split, split ? s : undefined))}
              />
            ))}
          </Bar>
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

export function LineChartViz({ data, left, right, palette, formats, legend, interaction }: LineProps) {
  const all = [...left, ...right];
  const split = interaction?.seriesDim;
  const click = interaction?.onMarkClick;
  const sel = interaction?.selectedKeys;
  const hasSel = !!sel && sel.size > 0;
  // Custom dot: emphasize points whose mark is selected (line highlight is
  // intentionally point-level — dimming a continuous line reads poorly).
  const dotFor = (s: string, color: string) =>
    hasSel
      ? (props: { cx?: number; cy?: number; index?: number }) => {
          const { cx, cy, index } = props;
          if (cx === undefined || cy === undefined) return <g />;
          const on = sel!.has(markKey({ dims: markDims(data[index ?? -1], split, split ? s : undefined) } as CrossMark));
          return <circle cx={cx} cy={cy} r={on ? 4.5 : 2} fill={color} fillOpacity={on ? 1 : 0.35} />;
        }
      : data.length <= 40;
  return (
    <ResponsiveContainer width="100%" height="100%">
      <ComposedChart data={data} margin={{ top: 8, right: 12, bottom: 4, left: 4 }}>
        <CartesianGrid stroke={GRID} strokeDasharray="3 3" vertical={false} />
        <XAxis type="category" dataKey="__x" {...categoryAxis} tick={lineCategoryTick} />
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
            dot={dotFor(s, palette[i % palette.length])}
            isAnimationActive={false}
            cursor={click ? "pointer" : undefined}
            onClick={markClickHandler(click, split, split ? s : undefined)}
          />
        ))}
      </ComposedChart>
    </ResponsiveContainer>
  );
}

// ─── Area (overlapping / stacked) ────────────────────────────────────────────

interface AreaProps extends BaseProps {
  series: string[];
  stacked?: boolean;
}

export function AreaChartViz({ data, series, palette, formats, stacked, legend, interaction }: AreaProps) {
  const split = interaction?.seriesDim;
  const click = interaction?.onMarkClick;
  const sel = interaction?.selectedKeys;
  const hasSel = !!sel && sel.size > 0;
  const dotFor = (s: string, color: string) =>
    hasSel
      ? (props: { cx?: number; cy?: number; index?: number }) => {
          const { cx, cy, index } = props;
          if (cx === undefined || cy === undefined) return <g />;
          const on = sel!.has(markKey({ dims: markDims(data[index ?? -1], split, split ? s : undefined) } as CrossMark));
          return <circle cx={cx} cy={cy} r={on ? 4.5 : 0} fill={color} fillOpacity={on ? 1 : 0} />;
        }
      : false;
  return (
    <ResponsiveContainer width="100%" height="100%">
      <ComposedChart data={data} margin={{ top: 8, right: 12, bottom: 4, left: 4 }}>
        <CartesianGrid stroke={GRID} strokeDasharray="3 3" vertical={false} />
        <XAxis type="category" dataKey="__x" {...categoryAxis} />
        <YAxis type="number" {...commonAxis} tickFormatter={tickFormatter(formats, series)} width={48} />
        <Tooltip formatter={tooltipFormatter(formats)} contentStyle={TOOLTIP_STYLE} />
        {legend && <Legend wrapperStyle={{ fontSize: 11 }} />}
        {series.map((s, i) => (
          <Area
            key={s}
            dataKey={s}
            stackId={stacked ? "stack" : undefined}
            stroke={palette[i % palette.length]}
            fill={palette[i % palette.length]}
            fillOpacity={stacked ? 0.7 : 0.3}
            strokeWidth={2}
            isAnimationActive={false}
            dot={dotFor(s, palette[i % palette.length])}
            cursor={click ? "pointer" : undefined}
            onClick={markClickHandler(click, split, split ? s : undefined)}
          />
        ))}
      </ComposedChart>
    </ResponsiveContainer>
  );
}

// ─── Donut (one metric across the axis categories) ───────────────────────────

interface DonutProps extends BaseProps {
  /** Metric sliced across the categories (first Values field). */
  metric: string;
}

export function DonutChartViz({ data, metric, palette, formats, legend, interaction }: DonutProps) {
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
        />
        {legend && <Legend wrapperStyle={{ fontSize: 11 }} />}
        <text x="50%" y="50%" textAnchor="middle" dominantBaseline="middle" fill={AXIS} fontSize={13} fontWeight={600}>
          {fmt ? formatValue(total, fmt) : compact.format(total)}
        </text>
      </PieChart>
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

export function ComboChartViz({ data, bars, lines, lineY2 = true, palette, formats, legend, interaction }: ComboProps) {
  const rightAxis = lineY2 && lines.length > 0;
  const click = interaction?.onMarkClick; // combo series are metrics → dims = row.__dims
  return (
    <ResponsiveContainer width="100%" height="100%">
      <ComposedChart data={data} margin={{ top: 8, right: 12, bottom: 4, left: 4 }}>
        <CartesianGrid stroke={GRID} strokeDasharray="3 3" vertical={false} />
        <XAxis type="category" dataKey="__x" {...categoryAxis} />
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
          <Bar
            key={s}
            dataKey={s}
            yAxisId="left"
            fill={palette[i % palette.length]}
            radius={2}
            isAnimationActive={false}
            cursor={click ? "pointer" : undefined}
            onClick={markClickHandler(click)}
          >
            {data.map((row, ri) => (
              <Cell
                key={ri}
                fill={palette[i % palette.length]}
                fillOpacity={markOpacity(interaction?.selectedKeys, markDims(row))}
              />
            ))}
          </Bar>
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
            cursor={click ? "pointer" : undefined}
            onClick={markClickHandler(click)}
          />
        ))}
      </ComposedChart>
    </ResponsiveContainer>
  );
}
