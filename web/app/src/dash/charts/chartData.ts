import type { MeasureFormat, QueryResponse } from "@dux/core";
import type { DashElement } from "../types";
import type { DataSpec } from "../visuals/types";
import { dropEmptyRows, splitElementFields } from "../data";

/** One dim column→value pair carried by a chart row, so a clicked mark can be
 *  turned back into a cross-filter (the joined __x string loses this). */
export type ChartDim = { table: string; column: string; value: string | number };

export interface ChartRow {
  /** Joined dim values — the category axis / slice label. */
  __x: string;
  /** Structured dim values behind __x; raw results have no table identity. */
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

/** Prepare query output without subscriptions or rendering. */
export function prepareVisualData(
  el: DashElement,
  spec: DataSpec,
  rawData: QueryResponse,
  formats: Record<string, MeasureFormat>,
  splitBy?: string,
) {
  const { dims, metrics } = splitElementFields(el);
  const viz = el.viz ?? {};
  // Raw mode has no builder fields, so charts treat the first result column
  // as x and the rest as series.
  const raw = el.query?.mode === "raw";
  const dimCols = raw ? rawData.columns.slice(0, 1) : dims.map((f) => f.name);
  const metricCols = raw
    ? rawData.columns.slice(1)
    : metrics.map((f) => f.name).filter((n) => rawData.columns.includes(n));

  const dimTables = Object.fromEntries(dims.map((f) => [f.name, f.table]));
  const data = dropEmptyRows(rawData, metricCols, spec.dropEmpty === false || (viz.showEmpty ?? false));
  const base = { data, metricCols, crossFilterable: !raw && dims.length > 0 };
  const none = { ...base, rows: [] as ChartRow[], keys: [] as string[], formats, seriesDim: undefined };
  if (!spec.chart) return none;
  if (splitBy && metricCols.length > 0) {
    const metric = metricCols[0];
    const { data: sData, series } = toSeriesData(data, dimCols.filter((c) => c !== splitBy), splitBy, metric, dimTables);
    // Split queries return every segment: trim and sort by whole stack totals.
    const total = (r: ChartRow) => series.reduce((t, s) => t + (Number(r[s]) || 0), 0);
    let rows = sData;
    const topN = el.query?.topN ?? 0;
    if (topN > 0 && rows.length > topN) {
      const kept = new Set([...rows].sort((a, b) => total(b) - total(a)).slice(0, topN));
      rows = rows.filter((r) => kept.has(r));
    }
    const sort = el.query?.sort?.[0];
    if (sort?.field === metric) {
      rows = [...rows].sort((a, b) => sort.dir === "desc" ? total(b) - total(a) : total(a) - total(b));
    }
    const sFormats: Record<string, MeasureFormat> = {};
    if (formats[metric]) for (const s of series) sFormats[s] = formats[metric];
    return { ...base, rows, keys: series, formats: sFormats, seriesDim: { table: dimTables[splitBy] ?? "", column: splitBy } };
  }
  return { ...base, rows: toChartData(data, dimCols, metricCols, dimTables), keys: metricCols, formats, seriesDim: undefined };
}
