// Shared Cartesian axes and chrome; each visual supplies its own marks.
import type { ReactNode } from "react";
import { CartesianGrid, ComposedChart, Legend, ResponsiveContainer, Text, Tooltip, XAxis, YAxis } from "recharts";
import type { XAxisTickContentProps } from "recharts";
import { formatCompactValue, formatValue } from "@dux/core";
import type { MeasureFormat } from "@dux/core";
import type { ChartRow, Interaction } from "./chartData";
import type { SeriesSpec } from "./seriesTypes";
import { additiveOf, markDims } from "./series";
import { TOOLTIP_STYLE, TOOLTIP_LABEL_STYLE, LEGEND_STYLE } from "./chartStyle";

const GRID = "var(--th-border)";
const AXIS = "var(--th-muted)";

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

interface CartesianProps {
  children: ReactNode;
  data: ChartRow[];
  series: SeriesSpec[];
  formats: Formats;
  legend?: boolean;
  interaction?: Interaction;
  /** Category on the y axis (horizontal bars). */
  horizontal?: boolean;
  /** Fit line-only vertical axes to the data instead of including zero. */
  zoomY?: boolean;
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

export default function CartesianFrame({
  data,
  series,
  formats,
  legend,
  interaction,
  horizontal,
  zoomY,
  children,
}: CartesianProps) {
  const click = interaction?.onMarkClick;
  const right = series.filter((s) => s.axis === "right");
  const left = series.filter((s) => s.axis !== "right");
  const leftKeys = left.map((s) => s.key);
  const rightKeys = right.map((s) => s.key);
  const lineDomain = (axisSeries: SeriesSpec[]) =>
    zoomY && axisSeries.length > 0 && axisSeries.every((s) => s.kind === "line") ? ["auto", "auto"] as const : undefined;
  const leftFmt = tickFormatter(formats, leftKeys);
  const rightFmt = tickFormatter(formats, rightKeys);
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
              domain={lineDomain(left)}
              {...commonAxis}
              tickFormatter={leftFmt}
              width={numericAxisWidth(data, leftKeys, leftFmt)}
            />
            {right.length > 0 && (
              <YAxis
                yAxisId="right"
                orientation="right"
                type="number"
                domain={lineDomain(right)}
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
        {children}
      </ComposedChart>
    </ResponsiveContainer>
  );
}
