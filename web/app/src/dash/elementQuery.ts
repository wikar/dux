// The DUX query behind a query-backed element, and the hook that runs it.
//
// Kept out of data.ts on purpose: the sort default is a per-visual fact, so
// this module reads the visual registry — and the registry's bodies read
// data.ts. Splitting the two keeps that graph acyclic.
import { useMemo } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { duxClient, generateQuery, isMetricField } from "@dux/core";
import type { QueryResponse } from "@dux/core";
import {
  elementFields,
  elementFilters,
  splitElementFields,
  useExternalFilters,
  useRefreshInterval,
} from "./data";
import type { DashElement } from "./types";
import { VISUALS } from "./visuals";

/** The dim an element fans out into one series per value, when the visual
 *  supports a series split and the chosen dim is actually in the query. */
export function elementSeriesSplit(el: DashElement): string | undefined {
  if (el.query?.mode === "raw" || !VISUALS[el.type]?.data?.seriesSplit) return undefined;
  return splitElementFields(el).dims.find((f) => f.name === el.viz?.series)?.name;
}

/** The DUX query an element executes ("" = nothing to run yet). */
export function buildElementDux(el: DashElement): string {
  const q = el.query;
  if (!q) return "";
  if (q.mode === "raw") return (q.raw ?? "").trim();
  let sort = q.sort;
  // Line-shaped charts default to ordering by their axis columns ascending
  // (first axis column, then the second, …) so series connect sensibly.
  if ((!sort || sort.length === 0) && VISUALS[el.type]?.data?.sortByDims) {
    sort = elementFields(el)
      .filter((f) => !isMetricField(f))
      .map((f) => ({ field: f.name, dir: "asc" as const }));
  }
  // A split query groups by the series dim too, so TOPN would keep the top n
  // single segments instead of the top n stacks. Fetch every group and trim
  // by stack total after the pivot instead.
  const topN = elementSeriesSplit(el) ? undefined : q.topN ?? undefined;
  return generateQuery(elementFields(el), elementFilters(el), { sort, topN });
}

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
