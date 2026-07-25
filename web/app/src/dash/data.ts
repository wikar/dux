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
import type { CrossMark } from "./store";
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

// ─── Cross-filter fan-out (chart clicks → every other element) ───────────────

function dedupeValues(vs: (string | number)[]): (string | number)[] {
  const seen = new Set<string>();
  const out: (string | number)[] = [];
  for (const v of vs) {
    const k = String(v);
    if (!seen.has(k)) {
      seen.add(k);
      out.push(v);
    }
  }
  return out;
}

/** Value a mark carries for a given dim column (marks from one source share
 *  the same dim columns, so this always resolves). */
function markValue(m: CrossMark, table: string, column: string): string | number {
  return m.dims.find((d) => d.table === table && d.column === column)?.value ?? "";
}

/** Translate the cross-filter selections of every *other* visual into external
 *  filters for this element. Exact for the common cases; a multi-select of
 *  multi-dimensional marks becomes one `in_tuples` filter when its columns
 *  share a table, else degrades to per-column `in` (approximate cross-product). */
export function crossExternalFilters(
  forId: string | null,
  crossFilters: Record<string, CrossMark[]>
): ExternalFilter[] {
  const out: ExternalFilter[] = [];
  for (const [sourceId, marks] of Object.entries(crossFilters)) {
    if (sourceId === forId || !marks || marks.length === 0) continue;
    const cols = marks[0].dims.map((d) => ({ table: d.table, column: d.column }));
    if (cols.length === 0) continue;

    if (cols.length === 1) {
      // Single-dim source → one IN with the union of selected values.
      const c = cols[0];
      out.push({ table: c.table, column: c.column, op: "in", values: dedupeValues(marks.map((m) => m.dims[0].value)) });
    } else if (marks.length === 1) {
      // One multi-dim mark → AND of per-column equality (exact).
      for (const d of marks[0].dims) out.push({ table: d.table, column: d.column, op: "in", values: [d.value] });
    } else if (cols.every((c) => c.table === cols[0].table)) {
      // Multiple multi-dim marks, one table → exact OR-of-tuples.
      out.push({ op: "in_tuples", columns: cols, tuples: marks.map((m) => cols.map((c) => markValue(m, c.table, c.column))) });
    } else {
      // Columns span tables → degrade to per-column IN (approximate).
      for (const c of cols) {
        out.push({ table: c.table, column: c.column, op: "in", values: dedupeValues(marks.map((m) => markValue(m, c.table, c.column))) });
      }
    }
  }
  return out;
}

/** The external filters that apply to this element right now: the other
 *  slicers' filters (which also make slicer lists cascade) plus every other
 *  visual's cross-filter selection. */
export function useExternalFilters(el: DashElement): ExternalFilter[] {
  const doc = useDocStore((s) => s.doc);
  const selections = useUiStore((s) => s.slicerSelections);
  const crossFilters = useUiStore((s) => s.crossFilters);
  return useMemo(
    () => [
      ...buildExternalFilters(el.id, doc, selections, el.interactions?.ignoreSlicers ?? []),
      ...crossExternalFilters(el.id, crossFilters),
    ],
    [el.id, el.interactions, doc, selections, crossFilters]
  );
}

/** Human-readable list of every filter affecting this element, grouped by
 *  source (own query filters, active slicers, cross-filtering visuals). Powers
 *  the header funnel popover. */
export function useAffectingFilters(el: DashElement): { source: string; text: string }[] {
  const doc = useDocStore((s) => s.doc);
  const slicerSelections = useUiStore((s) => s.slicerSelections);
  const crossFilters = useUiStore((s) => s.crossFilters);
  return useMemo(() => {
    const fmtVals = (vs: (string | number)[]): string => {
      const shown = vs.slice(0, 6).map(String);
      return shown.join(", ") + (vs.length > 6 ? `, +${vs.length - 6} more` : "");
    };
    const out: { source: string; text: string }[] = [];

    // 1. This visual's own query filters.
    for (const f of el.query?.filters ?? []) {
      out.push({ source: "This visual", text: `${f.name} ${f.op ?? "="} ${f.value ?? ""}`.trim() });
    }

    // 2. Active slicers (minus opted-out ones and self).
    const ignore = el.interactions?.ignoreSlicers ?? [];
    for (const s of doc?.elements ?? []) {
      if (s.type !== "slicer" || s.id === el.id || ignore.includes(s.id)) continue;
      const sel = slicerSelections[s.id];
      if (!sel) continue;
      const label = `Slicer: ${s.title?.text || s.slicer?.column || s.id}`;
      const col = s.slicer?.column ?? "";
      if (sel.kind === "values" && sel.values.length > 0) {
        out.push({ source: label, text: `${col} in ${fmtVals(sel.values)}` });
      } else if (sel.kind === "range") {
        const parts: string[] = [];
        if (sel.from) parts.push(`≥ ${sel.from}`);
        if (sel.to) parts.push(`≤ ${sel.to}`);
        if (parts.length) out.push({ source: label, text: `${col} ${parts.join(" and ")}` });
      }
    }

    // 3. Cross-filters from other visuals.
    for (const [sourceId, marks] of Object.entries(crossFilters)) {
      if (sourceId === el.id || !marks || marks.length === 0) continue;
      const src = doc?.elements.find((e) => e.id === sourceId);
      const label = `Cross-filter: ${src?.title?.text || sourceId}`;
      const byCol = new Map<string, (string | number)[]>();
      for (const m of marks) for (const d of m.dims) {
        const arr = byCol.get(d.column) ?? [];
        arr.push(d.value);
        byCol.set(d.column, arr);
      }
      for (const [col, vals] of byCol) out.push({ source: label, text: `${col} in ${fmtVals(dedupeValues(vals))}` });
    }

    return out;
  }, [el, doc, slicerSelections, crossFilters]);
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
  const grandTotal = viz.grandTotal ?? true;
  const totalCol = (viz.totalCol ?? true) && colDims.length > 0;
  const filters = elementFilters(el);

  // Intermediate levels feed both subtotal rows and collapsed group rows —
  // they're fetched whenever the pivot nests, regardless of the toggles.
  const levels: number[] = [];
  for (let l = 1; l < rowDims.length; l++) levels.push(l);
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
