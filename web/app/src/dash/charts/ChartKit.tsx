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
import { formatCompactValue, formatValue } from "@dux/core";
import type { MeasureFormat, QueryResponse } from "@dux/core";
import { markKey, type CrossMark } from "../store";

// Chart chrome from the dashboard theme. SVG presentation attributes are CSS
// properties, so fill/stroke resolve var() the same as any style does.
const GRID = "var(--th-border)";
const AXIS = "var(--th-muted)";
const TEXT = "var(--th-text)";
// Opaque, unlike the frosted element background: a tooltip sits over the marks
// it describes and has to stay legible against them.
const TOOLTIP_STYLE = {
  backgroundColor: "var(--th-bg)",
  border: "1px solid var(--th-border)",
  borderRadius: 6,
  fontSize: 12,
  color: TEXT,
} as const;
const TOOLTIP_LABEL_STYLE = { color: TEXT } as const;
const LEGEND_STYLE = { fontSize: 11, color: TEXT } as const;

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

/** Axis tick formatter using the first series' measure format. Ticks are
 *  scaled ("€15.3M") — a tick label has no room for the full format, and the
 *  tooltip still spells the value out in full. */
function tickFormatter(formats: Formats, series: string[]) {
  const fmt = series.map((s) => formats[s]).find(Boolean);
  return (v: number) => formatCompactValue(v, fmt);
}

function tooltipFormatter(formats: Formats) {
  return (value: unknown, name: unknown): [string, string] => {
    const n = String(name ?? "");
    const v = typeof value === "number" ? value : String(value ?? "");
    return [formatValue(v, formats[n]) || String(v), n];
  };
}

const commonAxis = { stroke: AXIS, tick: { fill: AXIS, fontSize: 10 }, tickLine: false } as const;

const TICK_FONT_PX = 10;
/** Tick label → axis line, plus slack for a numeric tick landing slightly wider
 *  than the extremes we measure (Recharts rounds the domain outward). */
const TICK_GAP = 12;
const AXIS_WIDTH_MIN = 40;
/** A pathological category label takes plot width up to here, then wraps. */
const AXIS_WIDTH_MAX = 220;

let measureCtx: CanvasRenderingContext2D | null | undefined;

/** Width of the widest label at the tick font, in CSS pixels.
 *
 *  Not Recharts' width:"auto": that measures tick text with
 *  getBoundingClientRect *inside* the canvas, which Canvas.tsx CSS-scales to
 *  fit the viewport, and then applies the post-transform number as an
 *  untransformed SVG unit. Every auto axis therefore comes out short by the
 *  scale factor and its longest label overhangs the element border — visible
 *  at any scale below 1, i.e. every window that isn't exactly canvas-sized.
 *  A 2D context measures in CSS pixels whatever the ancestors do. */
function axisWidth(labels: string[]): number {
  if (measureCtx === undefined) measureCtx = document.createElement("canvas").getContext("2d");
  if (!measureCtx) return AXIS_WIDTH_MAX;
  measureCtx.font = `${TICK_FONT_PX}px ${getComputedStyle(document.body).fontFamily}`;
  const widest = labels.reduce((w, s) => Math.max(w, measureCtx!.measureText(s).width), 0);
  return Math.min(AXIS_WIDTH_MAX, Math.max(AXIS_WIDTH_MIN, Math.ceil(widest) + TICK_GAP));
}

/** Formatted extremes of the series plotted on one numeric axis — the widest
 *  tick is one of them, give or take the domain rounding TICK_GAP absorbs. */
function numericAxisWidth(data: ChartRow[], keys: string[], fmt: (v: number) => string): number {
  let lo = 0;
  let hi = 0;
  for (const row of data) {
    for (const k of keys) {
      const v = row[k];
      if (typeof v !== "number") continue;
      lo = Math.min(lo, v);
      hi = Math.max(hi, v);
    }
  }
  return axisWidth([fmt(lo), fmt(hi)]);
}

/** Inner margin, in canvas pixels. Matches the dashboard's own 8px gap so a
 *  chart's labels sit off the element border by the same amount its neighbours
 *  do. The right edge needs no extra room for a half-overhanging end label —
 *  edgeSafeTick anchors those inward instead. */
const CHART_MARGIN = { top: 8, right: 8, bottom: 8, left: 8 } as const;

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
  // Kept even though edgeSafeTick draws the label: Recharts measures formatted
  // ticks to decide which ones "auto" interval can fit.
  tickFormatter: formatCategoryTick,
} as const;

/** Bottom-axis tick that cannot cross the plot edge: the first and last labels
 *  anchor inward instead of centering on their tick. A centered end label
 *  overhangs by half its width, and how wide that is depends on the measure
 *  format ("€160M" vs "€1,234.56"), so no fixed margin can cover every case. */
function edgeSafeTick(format: (value: unknown) => string) {
  return function EdgeSafeTick({ x, y, payload, index, visibleTicksCount, className }: XAxisTickContentProps) {
    return (
      <Text
        className={className}
        x={x}
        y={y}
        fill={AXIS}
        fontSize={10}
        textAnchor={index === 0 ? "start" : index === visibleTicksCount - 1 ? "end" : "middle"}
        verticalAnchor="start"
      >
        {format(payload.value)}
      </Text>
    );
  };
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

// ─── Cartesian frame (bar / line / area / combo) ─────────────────────────────
//
// One ComposedChart drives every cartesian visual: the grid, axes, tooltip and
// legend are identical, so a visual only declares its series (see the registry
// in the per-visual registry entries under ../visuals).

/** Stroke weight of a line/area curve. The selected-point outline matches it,
 *  so the marker reads as part of the same drawing. */
const SERIES_STROKE = 2;

export type SeriesKind = "bar" | "line" | "area";

export interface SeriesSpec {
  /** Row key plotted by this series. */
  key: string;
  kind: SeriesKind;
  color: string;
  /** Secondary y axis; the axis is rendered only when a series asks for it. */
  axis?: "left" | "right";
  /** Shared id stacks series instead of clustering/overlapping them. */
  stackId?: string;
  /** bar: corner radius. */
  radius?: number;
  /** area: band opacity. */
  fillOpacity?: number;
  /** line / area point markers. base "auto" shows them only on sparse data;
   *  highlight enlarges the marks that are cross-filter selected. */
  dot?: { base: number | "auto"; highlight?: boolean };
}

interface CartesianProps {
  data: ChartRow[];
  series: SeriesSpec[];
  formats: Formats;
  legend?: boolean;
  interaction?: Interaction;
  /** Category on the y axis (horizontal bars). */
  horizontal?: boolean;
  /** Outline for a cross-filter-selected marker — the theme's text color, so
   *  the mark separates from its own line without borrowing a series color. */
  markStroke?: string;
}

/** Point marker for a line/area series: a boolean for Recharts default dot,
 *  or a renderer that emphasizes the selected marks (dimming a continuous
 *  line reads poorly, so line highlight is intentionally point-level).
 *  A curve mark is keyed on the axis value alone - see chartRowClick. */
function seriesDot(
  s: SeriesSpec,
  data: ChartRow[],
  sel: Set<string> | undefined,
  markStroke: string | undefined
) {
  if (!s.dot) return false;
  // "auto" shows markers only while the data is sparse enough to read.
  const auto = s.dot.base === "auto" && data.length <= 40;
  if (!s.dot.highlight || !sel || sel.size === 0) return auto;
  // Dimmed markers are context for the selected one; on a dense series they
  // just smear the curve, so there only the selection is drawn.
  const r = s.dot.base === "auto" ? (auto ? 2 : 0) : s.dot.base;
  return (props: { cx?: number; cy?: number; index?: number }) => {
    const { cx, cy, index } = props;
    if (cx === undefined || cy === undefined) return <g />;
    const on = sel.has(markKey({ dims: markDims(data[index ?? -1]) } as CrossMark));
    if (!on && r === 0) return <g />;
    // The selected point keeps its series fill and gains a hairline outline;
    // the dimmed ones stay plain so the line still reads as one.
    return (
      <circle
        cx={cx}
        cy={cy}
        r={on ? 4.5 : r}
        fill={s.color}
        fillOpacity={on ? 1 : 0.35}
        stroke={on ? markStroke : undefined}
        strokeWidth={on ? SERIES_STROKE : undefined}
      />
    );
  };
}

/** Recharts click state: the active-tooltip fields that also place the
 *  vertical cursor line. */
interface ChartClickState {
  activeIndex?: number | string | null;
  activeTooltipIndex?: number | string | null;
  activeLabel?: string | number | null;
}

/** Index of the row the cursor line sits on, or -1. */
function activeRow(state: ChartClickState | undefined, data: ChartRow[]): number {
  const n = Number(state?.activeIndex ?? state?.activeTooltipIndex);
  if (Number.isInteger(n) && n >= 0 && n < data.length) return n;
  const label = state?.activeLabel;
  if (label !== undefined && label !== null) return data.findIndex((r) => r.__x === String(label));
  return -1;
}

export function CartesianChart({
  data,
  series,
  formats,
  legend,
  interaction,
  horizontal,
  markStroke,
}: CartesianProps) {
  const split = interaction?.seriesDim; // when set, each series key is a dim value
  const click = interaction?.onMarkClick;
  const sel = interaction?.selectedKeys;
  const right = series.filter((s) => s.axis === "right");
  const leftKeys = series.filter((s) => s.axis !== "right").map((s) => s.key);
  const rightKeys = right.map((s) => s.key);
  const leftFmt = tickFormatter(formats, leftKeys);
  const rightFmt = tickFormatter(formats, rightKeys);
  // A horizontal layout has a single numeric axis, so series carry no axis id.
  const axisId = (s: SeriesSpec) => (horizontal ? undefined : s.axis ?? "left");

  // Recharts reports a Line/Area click as the *series* — the handler never
  // learns which point was under the cursor — so a curve can't cross-filter
  // through its own onClick. The chart-level click can: it carries the active
  // tooltip row, i.e. exactly the axis value the vertical cursor line marks.
  // Bars stay on their own handler (a rectangle already is one row, and a
  // split stack knows its series too), so they opt out of this one.
  const curves = series.some((s) => s.kind !== "bar");
  const chartClick =
    click && curves
      ? (...args: unknown[]) => {
          const event = args[1] as { target?: EventTarget; ctrlKey?: boolean; metaKey?: boolean } | undefined;
          const target = event?.target as Element | undefined;
          if (target?.closest?.(".recharts-bar-rectangle, .recharts-legend-wrapper")) return;
          const i = activeRow(args[0] as ChartClickState | undefined, data);
          if (i < 0) return;
          const dims = markDims(data[i]);
          if (dims.length > 0) click(dims, additiveOf(event));
        }
      : undefined;

  const renderSeries = (s: SeriesSpec) => {
    const shared = {
      dataKey: s.key,
      yAxisId: axisId(s),
      isAnimationActive: false,
    } as const;
    if (s.kind === "bar") {
      // A bar rectangle is its own mark: Recharts hands the clicked row over.
      const seriesVal = split ? s.key : undefined;
      return (
        <Bar
          key={s.key}
          {...shared}
          fill={s.color}
          stackId={s.stackId}
          radius={s.radius ?? 2}
          cursor={click ? "pointer" : undefined}
          onClick={markClickHandler(click, split, seriesVal)}
        >
          {data.map((row, ri) => (
            <Cell key={ri} fill={s.color} fillOpacity={markOpacity(sel, markDims(row, split, seriesVal))} />
          ))}
        </Bar>
      );
    }
    const dot = seriesDot(s, data, sel, markStroke);
    if (s.kind === "area") {
      return (
        <Area
          key={s.key}
          {...shared}
          stackId={s.stackId}
          stroke={s.color}
          fill={s.color}
          fillOpacity={s.fillOpacity ?? 0.3}
          strokeWidth={SERIES_STROKE}
          dot={dot}
        />
      );
    }
    return <Line key={s.key} {...shared} stroke={s.color} strokeWidth={SERIES_STROKE} dot={dot} />;
  };

  return (
    <ResponsiveContainer width="100%" height="100%">
      <ComposedChart
        data={data}
        layout={horizontal ? "vertical" : "horizontal"}
        margin={CHART_MARGIN}
        onClick={chartClick}
        // The whole plot is the target, so say so.
        style={chartClick ? { cursor: "pointer" } : undefined}
      >
        <CartesianGrid stroke={GRID} strokeDasharray="3 3" horizontal={!horizontal} vertical={horizontal} />
        {horizontal ? (
          <>
            <XAxis
              type="number"
              {...commonAxis}
              tickFormatter={leftFmt}
              tick={edgeSafeTick((v) => leftFmt(Number(v)))}
            />
            <YAxis
              type="category"
              dataKey="__x"
              {...commonAxis}
              tickFormatter={formatCategoryTick}
              width={axisWidth(data.map((r) => formatCategoryTick(r.__x)))}
            />
          </>
        ) : (
          <>
            <XAxis type="category" dataKey="__x" {...categoryAxis} tick={edgeSafeTick(formatCategoryTick)} />
            <YAxis
              yAxisId="left"
              type="number"
              {...commonAxis}
              tickFormatter={leftFmt}
              width={numericAxisWidth(data, leftKeys, leftFmt)}
            />
            {right.length > 0 && (
              <YAxis
                yAxisId="right"
                orientation="right"
                type="number"
                {...commonAxis}
                tickFormatter={rightFmt}
                width={numericAxisWidth(data, rightKeys, rightFmt)}
              />
            )}
          </>
        )}
        <Tooltip
          formatter={tooltipFormatter(formats)}
          contentStyle={TOOLTIP_STYLE}
          labelStyle={TOOLTIP_LABEL_STYLE}
          // Bars get a band cursor; a line/area hover would be obscured by it.
          cursor={series.some((s) => s.kind === "bar") ? { fill: "var(--th-tint)" } : undefined}
        />
        {legend && <Legend wrapperStyle={LEGEND_STYLE} />}
        {series.map(renderSeries)}
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
