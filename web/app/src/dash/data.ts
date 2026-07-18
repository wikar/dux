// Element data pipeline: builder state → DUX text → TanStack Query.
// Identical queries share one cache entry (dedupe); superseded fetches are
// aborted via the query's AbortSignal (stale cancellation).
import { useMemo } from "react";
import { keepPreviousData, useQueries, useQuery } from "@tanstack/react-query";
import { duxClient, generateQuery, isMetricField, isNumeric } from "@dux/core";
import type {
  DropField,
  ExternalFilter,
  FilterField,
  FilterOp,
  MeasureFormat,
  QueryResponse,
  Schema,
} from "@dux/core";
import { getTheme } from "./api";
import { useDocStore, useUiStore } from "./store";
import type { Dashboard, DashElement, SlicerSelection, ThemeTokens } from "./types";

// ─── Schema / formats ────────────────────────────────────────────────────────

export function useDuxSchema(): Schema | undefined {
  return useQuery({ queryKey: ["schema"], queryFn: () => duxClient.fetchSchema() }).data;
}

/** Measure display formats keyed by output column name (= measure name). */
export function useFormats(): Record<string, MeasureFormat> {
  const { data } = useQuery({ queryKey: ["measures"], queryFn: () => duxClient.fetchMeasures() });
  return useMemo(() => {
    const map: Record<string, MeasureFormat> = {};
    for (const m of data ?? []) {
      if (m.format && !(m.name in map)) map[m.name] = m.format;
    }
    return map;
  }, [data]);
}

// ─── Builder-state helpers ───────────────────────────────────────────────────

/** Element query fields normalised to core DropField shape. */
export function elementFields(el: DashElement): DropField[] {
  return (el.query?.fields ?? []).map((f) => ({
    table: f.table,
    name: f.name,
    kind: f.kind ?? "column",
    dataType: f.dataType ?? "",
    aggregate: f.aggregate as DropField["aggregate"],
  }));
}

export function elementFilters(el: DashElement): FilterField[] {
  return (el.query?.filters ?? []).map((f) => ({
    table: f.table,
    name: f.name,
    dataType: f.dataType ?? "",
    op: (f.op ?? "=") as FilterOp,
    value: f.value ?? "",
  }));
}

/** Group-by dims and metric fields of an element, in stored order. */
export function splitElementFields(el: DashElement): { dims: DropField[]; metrics: DropField[] } {
  const fields = elementFields(el);
  return {
    dims: fields.filter((f) => !isMetricField(f)),
    metrics: fields.filter(isMetricField),
  };
}

/** The DUX query an element executes ("" = nothing to run yet). */
export function buildElementDux(el: DashElement): string {
  const q = el.query;
  if (!q) return "";
  if (q.mode === "raw") return (q.raw ?? "").trim();
  let sort = q.sort;
  // Line-shaped charts default to ordering by their axis columns ascending
  // (first axis column, then the second, …) so series connect sensibly.
  if ((!sort || sort.length === 0) && (el.type === "line" || el.type === "combo" || el.type === "area")) {
    sort = elementFields(el)
      .filter((f) => !isMetricField(f))
      .map((f) => ({ field: f.name, dir: "asc" as const }));
  }
  return generateQuery(elementFields(el), elementFilters(el), {
    sort,
    topN: q.topN ?? undefined,
  });
}

/** Drop rows whose metric columns are all null (SUMMARIZECOLUMNS keeps axis
 *  values with no data); viz.showEmpty turns the filter off. */
export function dropEmptyRows(res: QueryResponse, metricCols: string[], showEmpty: boolean): QueryResponse {
  if (showEmpty) return res;
  const idx = metricCols.map((c) => res.columns.indexOf(c)).filter((i) => i >= 0);
  if (idx.length === 0) return res;
  return { ...res, rows: res.rows.filter((r) => idx.some((i) => r[i] !== null && r[i] !== undefined)) };
}

// ─── External filter fan-out (slicers → every other element) ─────────────────

/** Translate one slicer's selection into an external filter. Numeric columns
 *  get numeric values so the injected predicates type-match. */
function slicerFilter(el: DashElement, sel: SlicerSelection): ExternalFilter | null {
  const s = el.slicer;
  if (!s?.table || !s?.column) return null;
  const numeric = isNumeric(s.dataType ?? "");
  const conv = (v: string): string | number => (numeric ? Number(v) : v);

  if (sel.kind === "values" && sel.values.length > 0) {
    return { table: s.table, column: s.column, op: "in", values: sel.values.map(conv) };
  }
  if (sel.kind === "range") {
    const from = sel.from !== undefined && sel.from !== "" ? conv(sel.from) : undefined;
    const to = sel.to !== undefined && sel.to !== "" ? conv(sel.to) : undefined;
    if (from !== undefined && to !== undefined)
      return { table: s.table, column: s.column, op: "between", from, to };
    if (from !== undefined) return { table: s.table, column: s.column, op: ">=", value: from };
    if (to !== undefined) return { table: s.table, column: s.column, op: "<=", value: to };
  }
  return null;
}

/** Active filters for an element: every other slicer's selection, minus the
 *  ones it opts out of. AND semantics — one filter per slicer. */
export function buildExternalFilters(
  forId: string | null,
  doc: Dashboard | null,
  selections: Record<string, SlicerSelection>,
  ignore: readonly string[]
): ExternalFilter[] {
  if (!doc) return [];
  const out: ExternalFilter[] = [];
  for (const el of doc.elements) {
    if (el.type !== "slicer" || el.id === forId || ignore.includes(el.id)) continue;
    const sel = selections[el.id];
    if (!sel) continue;
    const f = slicerFilter(el, sel);
    if (f) out.push(f);
  }
  return out;
}

/** The external filters that apply to this element right now. Slicers get
 *  the other slicers' filters — that's what makes their lists cascade. */
export function useExternalFilters(el: DashElement): ExternalFilter[] {
  const doc = useDocStore((s) => s.doc);
  const selections = useUiStore((s) => s.slicerSelections);
  return useMemo(
    () => buildExternalFilters(el.id, doc, selections, el.interactions?.ignoreSlicers ?? []),
    [el.id, el.interactions, doc, selections]
  );
}

// ─── Live refresh ────────────────────────────────────────────────────────────

/** Client-side floor matching the server default (dash.Config). */
export const REFRESH_FLOOR_S = 5;

/** Per-element refetch interval: dashboard interval with a deterministic
 *  ±10% jitter seeded by the element id, so refreshes stagger instead of
 *  thundering in together. false = refresh disabled. */
export function useRefreshInterval(seed: string): number | false {
  const refresh = useDocStore((s) => s.doc?.refresh);
  return useMemo(() => {
    if (!refresh?.enabled) return false;
    const base = Math.max(REFRESH_FLOOR_S, refresh.intervalSeconds) * 1000;
    let h = 0;
    for (const c of seed) h = (h * 31 + c.charCodeAt(0)) | 0;
    const jitter = ((Math.abs(h) % 2001) / 2000 - 0.5) * 0.2; // -10% … +10%
    return Math.round(base * (1 + jitter));
  }, [refresh, seed]);
}

// ─── Element data hook ───────────────────────────────────────────────────────

export interface ElementDataState {
  dux: string;
  data: QueryResponse | undefined;
  error: Error | null;
  loading: boolean;
}

/** Run an element's query with the active slicer filters applied. */
export function useElementData(el: DashElement): ElementDataState {
  const dux = useMemo(() => buildElementDux(el), [el.query]);
  const filters = useExternalFilters(el);
  const filterKey = JSON.stringify(filters);
  const refetchInterval = useRefreshInterval(el.id);
  const q = useQuery({
    queryKey: ["eldata", dux, filterKey],
    enabled: dux !== "",
    queryFn: ({ signal }) => duxClient.executeQueryFiltered(dux, filters, { signal }),
    placeholderData: keepPreviousData,
    retry: 0,
    staleTime: 15_000,
    refetchInterval,
    // Wall-display dashboards refresh even when the window isn't focused.
    refetchIntervalInBackground: true,
  });
  return {
    dux,
    data: q.data,
    error: (q.error as Error | null) ?? null,
    loading: q.isFetching,
  };
}

// ─── Pivot totals ────────────────────────────────────────────────────────────
//
// The pivot's main data is the flat query over all dims (rows + cols) that
// useElementData already runs. Subtotals and grand totals are separate
// queries per grouping level — measures aren't additive, so totals can never
// be summed client-side. All share the "eldata" cache namespace (dedupe with
// the main fetch) and reflect the element's filters and the slicer fan-out;
// topN deliberately doesn't apply to totals.

/** Row dims, column dims (viz.cols membership), and metrics of a pivot. */
export function pivotParts(el: DashElement): { rowDims: DropField[]; colDims: DropField[]; metrics: DropField[] } {
  const { dims, metrics } = splitElementFields(el);
  const cols = new Set(el.viz?.cols ?? []);
  return {
    rowDims: dims.filter((f) => !cols.has(f.name)),
    colDims: dims.filter((f) => cols.has(f.name)),
    metrics,
  };
}

/** Key of one totals query: row-group level + whether the column dims are in.
 *  Level l groups by the first l row dims (0 = grand total). */
export function totalsKey(level: number, withCols: boolean): string {
  return `${level}|${withCols ? "c" : "t"}`;
}

interface TotalsSpec {
  key: string;
  dux: string;
}

/** The totals queries a pivot needs beside its main flat query. */
export function buildPivotTotalQueries(el: DashElement): TotalsSpec[] {
  if (el.query?.mode === "raw") return [];
  const { rowDims, colDims, metrics } = pivotParts(el);
  if (metrics.length === 0) return [];
  const viz = el.viz ?? {};
  const subtotals = viz.subtotals ?? true;
  const grandTotal = viz.grandTotal ?? true;
  const totalCol = (viz.totalCol ?? true) && colDims.length > 0;
  const filters = elementFilters(el);

  const levels: number[] = [];
  if (subtotals) for (let l = 1; l < rowDims.length; l++) levels.push(l);
  if (grandTotal) levels.push(0);

  const out: TotalsSpec[] = [];
  for (const l of levels) {
    out.push({
      key: totalsKey(l, true),
      dux: generateQuery([...rowDims.slice(0, l), ...colDims, ...metrics], filters),
    });
  }
  if (totalCol) {
    // The total column needs every row's cross-column total too (level R).
    for (const l of [...levels, rowDims.length]) {
      out.push({ key: totalsKey(l, false), dux: generateQuery([...rowDims.slice(0, l), ...metrics], filters) });
    }
  }
  // Dedupe (level R with no cols equals level R with cols when cols is empty …).
  const seen = new Set<string>();
  return out.filter((s) => !seen.has(s.key) && (seen.add(s.key), s.dux !== ""));
}

export interface PivotTotalsState {
  /** Query results keyed by totalsKey(level, withCols). */
  byKey: Record<string, QueryResponse | undefined>;
  loading: boolean;
}

/** Fetch a pivot's subtotal/grand-total queries alongside the main fetch. */
export function usePivotTotals(el: DashElement): PivotTotalsState {
  const specs = useMemo(() => buildPivotTotalQueries(el), [el.query, el.viz]);
  const filters = useExternalFilters(el);
  const filterKey = JSON.stringify(filters);
  const refetchInterval = useRefreshInterval(el.id);
  const results = useQueries({
    queries: specs.map((s) => ({
      queryKey: ["eldata", s.dux, filterKey],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        duxClient.executeQueryFiltered(s.dux, filters, { signal }),
      placeholderData: keepPreviousData,
      retry: 0,
      staleTime: 15_000,
      refetchInterval,
      refetchIntervalInBackground: true,
    })),
  });
  const byKey: Record<string, QueryResponse | undefined> = {};
  specs.forEach((s, i) => (byKey[s.key] = results[i]?.data));
  return { byKey, loading: results.some((r) => r.isFetching) };
}

// ─── Slicer options ──────────────────────────────────────────────────────────

export interface SlicerOptionsState {
  options: string[];
  loading: boolean;
  error: Error | null;
}

/** Distinct values for a slicer, cascaded by the other slicers' filters and
 *  optionally trimmed to rows where the configured measure is non-null. */
export function useSlicerOptions(el: DashElement): SlicerOptionsState {
  const s = el.slicer;
  const filters = useExternalFilters(el);
  const filterKey = JSON.stringify(filters);
  const refetchInterval = useRefreshInterval(el.id);

  const dux = useMemo(() => {
    if (!s?.table || !s?.column) return "";
    const fields: DropField[] = [
      {
        table: s.table,
        name: s.column,
        kind: "column",
        dataType: s.dataType ?? "",
        aggregate: isNumeric(s.dataType ?? "") ? "VALUES" : undefined,
      },
    ];
    if (s.measure) {
      // Trim metric: a measure, or a numeric column with an aggregate.
      const m = s.measure;
      fields.push({
        table: m.table,
        name: m.name,
        kind: m.kind ?? "measure",
        dataType: m.dataType ?? "",
        aggregate: (m.kind === "column" ? (m.aggregate as DropField["aggregate"]) ?? "SUM" : undefined),
      });
    }
    return generateQuery(fields, [], { sort: [{ field: s.column, dir: "asc" }] });
  }, [s]);

  const q = useQuery({
    queryKey: ["slicer-options", dux, filterKey],
    enabled: dux !== "",
    queryFn: ({ signal }) => duxClient.executeQueryFiltered(dux, filters, { signal }),
    placeholderData: keepPreviousData,
    retry: 0,
    staleTime: 15_000,
    refetchInterval,
    refetchIntervalInBackground: true,
  });

  const options = useMemo(() => {
    if (!q.data) return [];
    const mIdx = s?.measure ? q.data.columns.indexOf(s.measure.name) : -1;
    const seen = new Set<string>();
    const out: string[] = [];
    for (const r of q.data.rows) {
      if (mIdx >= 0 && (r[mIdx] === null || r[mIdx] === undefined)) continue;
      if (r[0] === null || r[0] === undefined) continue;
      const v = String(r[0]);
      if (!seen.has(v)) {
        seen.add(v);
        out.push(v);
      }
    }
    return out;
  }, [q.data, s?.measure]);

  return { options, loading: q.isFetching, error: (q.error as Error | null) ?? null };
}

// ─── Theme cascade (defaults ← theme.json ← dashboard.theme, per token) ──────

export const DEFAULT_PALETTE = [
  "#89b4fa", "#cba6f7", "#a6e3a1", "#fab387",
  "#f38ba8", "#94e2d5", "#f9e2af", "#b4befe",
];

/** Built-in look (matches the app chrome); every token can be overridden in
 *  dashboards/theme.json and again per dashboard. */
export const DEFAULT_THEME: Required<ThemeTokens> = {
  palette: DEFAULT_PALETTE,
  background: "#1e1e2e",
  backgroundImage: "",
  backgroundFit: "cover",
  elementBackground: "rgba(24, 24, 37, 0.82)",
  titleBackground: "transparent",
  border: "#45475a",
  text: "#cdd6f4",
  fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
};

export function useGlobalTheme() {
  return useQuery({ queryKey: ["dash-theme"], queryFn: getTheme });
}

function mergeTokens(base: Required<ThemeTokens>, over: ThemeTokens | undefined): Required<ThemeTokens> {
  if (!over) return base;
  const out = { ...base };
  for (const k of Object.keys(DEFAULT_THEME) as (keyof ThemeTokens)[]) {
    const v = over[k];
    if (k === "palette") {
      if (Array.isArray(v) && v.length > 0) out.palette = v as string[];
    } else if (typeof v === "string" && v !== "") {
      (out[k] as string) = v;
    }
  }
  return out;
}

/** Effective theme for a dashboard: defaults ← theme.json ← dashboard.theme. */
export function useResolvedTheme(doc: Dashboard | null): Required<ThemeTokens> {
  const { data } = useGlobalTheme();
  return useMemo(() => {
    const withGlobal = mergeTokens(DEFAULT_THEME, data?.tokens as ThemeTokens | undefined);
    return mergeTokens(withGlobal, doc?.theme as ThemeTokens | undefined);
  }, [data, doc?.theme]);
}

/** Effective global theme (defaults ← theme.json) — the inherited values a
 *  dashboard-level override falls back to. */
export function useGlobalResolvedTheme(): Required<ThemeTokens> {
  const { data } = useGlobalTheme();
  return useMemo(
    () => mergeTokens(DEFAULT_THEME, data?.tokens as ThemeTokens | undefined),
    [data]
  );
}

export function usePalette(doc: Dashboard | null): string[] {
  return useResolvedTheme(doc).palette;
}
