// Element data pipeline: builder state → DUX text → TanStack Query.
// Identical queries share one cache entry (dedupe); superseded fetches are
// aborted via the query's AbortSignal (stale cancellation).
import { useMemo } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { duxClient, generateQuery, isMetricField } from "@dux/core";
import type {
  DropField,
  FilterField,
  FilterOp,
  MeasureFormat,
  QueryResponse,
  Schema,
} from "@dux/core";
import { getTheme } from "./api";
import type { Dashboard, DashElement, ThemeTokens } from "./types";

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
  if ((!sort || sort.length === 0) && (el.type === "line" || el.type === "combo")) {
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

// ─── Element data hook ───────────────────────────────────────────────────────

export interface ElementDataState {
  dux: string;
  data: QueryResponse | undefined;
  error: Error | null;
  loading: boolean;
}

/** Run an element's query. External filters (slicers) join the key in M5. */
export function useElementData(el: DashElement): ElementDataState {
  const dux = useMemo(() => buildElementDux(el), [el.query]);
  const q = useQuery({
    queryKey: ["eldata", dux],
    enabled: dux !== "",
    queryFn: ({ signal }) => duxClient.executeQueryFiltered(dux, [], { signal }),
    placeholderData: keepPreviousData,
    retry: 0,
    staleTime: 15_000,
  });
  return {
    dux,
    data: q.data,
    error: (q.error as Error | null) ?? null,
    loading: q.isFetching,
  };
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
