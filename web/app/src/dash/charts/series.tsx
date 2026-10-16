import { Area, Bar, Cell, Line } from "recharts";
import { markKey, type CrossMark } from "../store";
import type { ChartDim, ChartRow, Interaction, MarkClick } from "./chartData";
import type { SeriesSpec } from "./seriesTypes";

const SERIES_STROKE = 2;

export function additiveOf(e: unknown): boolean {
  const me = e as { ctrlKey?: boolean; metaKey?: boolean } | undefined;
  return !!(me?.ctrlKey || me?.metaKey);
}

/** Full dim tuple of a mark: the row's dims plus the series value when split. */
export function markDims(row: ChartRow | undefined, seriesDim?: Interaction["seriesDim"], seriesVal?: string): ChartDim[] {
  const dims = [...(row?.__dims ?? [])];
  if (seriesDim && seriesVal !== undefined) dims.push({ ...seriesDim, value: seriesVal });
  return dims;
}

/** Opacity for a mark given the current selection: 1 when nothing is selected
 *  or this mark is selected, dimmed otherwise. */
export function markOpacity(sel: Set<string> | undefined, dims: ChartDim[]): number {
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
export function markClickHandler(click: MarkClick | undefined, seriesDim?: Interaction["seriesDim"], seriesVal?: string) {
  if (!click) return undefined;
  return (...args: unknown[]) => click(markDims(rowOf(args[0]), seriesDim, seriesVal), additiveOf(args[2]));
}

/** Point marker for a line/area series: a boolean for Recharts default dot,
 *  or a renderer that emphasizes the selected marks (dimming a continuous
 *  line reads poorly, so line highlight is intentionally point-level).
 *  A curve mark is keyed on the axis value alone, as in CartesianFrame. */
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

interface SeriesRenderContext {
  data: ChartRow[];
  interaction?: Interaction;
  horizontal?: boolean;
  markStroke?: string;
}

export function renderBarSeries(s: SeriesSpec, { data, interaction, horizontal }: SeriesRenderContext) {
  const split = interaction?.seriesDim;
  const click = interaction?.onMarkClick;
  const seriesVal = split ? s.key : undefined;
  return (
    <Bar
      key={s.key}
      dataKey={s.key}
      yAxisId={horizontal ? undefined : s.axis ?? "left"}
      isAnimationActive={false}
      fill={s.color}
      stackId={s.stackId}
      radius={s.radius ?? 2}
      cursor={click ? "pointer" : undefined}
      onClick={markClickHandler(click, split, seriesVal)}
    >
      {data.map((row, ri) => (
        <Cell key={ri} fill={s.color} fillOpacity={markOpacity(interaction?.selectedKeys, markDims(row, split, seriesVal))} />
      ))}
    </Bar>
  );
}

export function renderLineSeries(s: SeriesSpec, { data, interaction, markStroke }: SeriesRenderContext) {
  return (
    <Line
      key={s.key}
      dataKey={s.key}
      yAxisId={s.axis ?? "left"}
      isAnimationActive={false}
      type={s.smooth ? "natural" : "linear"}
      connectNulls
      stroke={s.color}
      strokeWidth={SERIES_STROKE}
      dot={seriesDot(s, data, interaction?.selectedKeys, markStroke)}
    />
  );
}

export function renderAreaSeries(s: SeriesSpec, { data, interaction, markStroke }: SeriesRenderContext) {
  return (
    <Area
      key={s.key}
      dataKey={s.key}
      yAxisId={s.axis ?? "left"}
      isAnimationActive={false}
      stackId={s.stackId}
      stroke={s.color}
      fill={s.color}
      fillOpacity={s.fillOpacity ?? 0.3}
      strokeWidth={SERIES_STROKE}
      dot={seriesDot(s, data, interaction?.selectedKeys, markStroke)}
    />
  );
}
