import { useMemo } from "react";
import { keepPreviousData, useQueries } from "@tanstack/react-query";
import { duxClient, generateQuery } from "@dux/core";
import type { DropField, QueryResponse } from "@dux/core";
import { elementFilters, useExternalFilters, useRefreshInterval } from "./data";
import { markKey, type CrossMark } from "./store";
import type { DashElement, MapLayer, ThemeTokens } from "./types";

const LIGHT_STYLE = "https://basemaps.cartocdn.com/gl/positron-gl-style/style.json";

function rgba(color: string): [number, number, number, number] | null {
  const hex = /^#([0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})$/i.exec(color.trim())?.[1];
  if (hex) {
    const h = hex.length === 3 ? [...hex].map((c) => c + c).join("") : hex;
    return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16), h.length === 8 ? parseInt(h.slice(6, 8), 16) / 255 : 1];
  }
  const parts = /^rgba?\(\s*([\d.]+)[, ]+\s*([\d.]+)[, ]+\s*([\d.]+)(?:\s*[,/]\s*([\d.]+))?/i.exec(color.trim());
  return parts ? [Number(parts[1]), Number(parts[2]), Number(parts[3]), parts[4] == null ? 1 : Number(parts[4])] : null;
}

export function isDarkColor(color: string): boolean {
  const c = rgba(color);
  return !!c && 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2] < 140;
}

export function resolveMapStyle(): string {
  return LIGHT_STYLE;
}

function withAlpha(color: string, factor: number): string {
  const c = rgba(color);
  return c ? `rgba(${c[0]}, ${c[1]}, ${c[2]}, ${Number(Math.max(0, Math.min(1, c[3] * factor)).toFixed(3))})` : color;
}

function inverseWithAlpha(color: string, alpha: number): string {
  const c = rgba(color);
  return c ? `rgba(${255 - c[0]}, ${255 - c[1]}, ${255 - c[2]}, ${alpha})` : withAlpha(color, alpha);
}

export function mapThemeColors(theme: Required<ThemeTokens>) {
  const water = inverseWithAlpha(theme.elementBackground, 0.08);
  return {
    background: theme.elementBackground,
    land: theme.elementBackground,
    water,
    border: theme.border,
    roads: withAlpha(theme.border, 0.55),
    text: theme.text,
  };
}

function dim(f: NonNullable<MapLayer["lng"]>): DropField {
  return { table: f.table, name: f.name, kind: "column", dataType: f.dataType ?? "", aggregate: "VALUES" };
}

export function layerFields(layer: MapLayer): DropField[] {
  if (!layer.lng || !layer.lat) return [];
  const fields: DropField[] = [];
  if (layer.category) fields.push(dim(layer.category));
  fields.push(dim(layer.lng), dim(layer.lat));
  if (layer.size) {
    fields.push({
      table: layer.size.table,
      name: layer.size.name,
      kind: layer.size.kind ?? "measure",
      dataType: layer.size.dataType ?? "",
      aggregate: layer.size.kind === "measure" ? undefined : (layer.size.aggregate as DropField["aggregate"]) ?? "SUM",
    });
  }
  return fields;
}

export function layerDux(el: DashElement, layer: MapLayer): string {
  const fields = layerFields(layer);
  return fields.length ? generateQuery(fields, elementFilters(el)) : "";
}

export function useMapLayerData(el: DashElement): {
  byLayer: Record<string, QueryResponse | undefined>;
  loading: boolean;
  error: Error | null;
} {
  const layers = el.viz?.layers ?? [];
  const specs = layers.map((layer) => ({ id: layer.id, dux: layerDux(el, layer) }));
  const filters = useExternalFilters(el);
  const filterKey = JSON.stringify(filters);
  const refetchInterval = useRefreshInterval(el.id);
  const results = useQueries({
    queries: specs.map((spec) => ({
      queryKey: ["eldata", spec.dux, filterKey],
      enabled: spec.dux !== "",
      queryFn: ({ signal }: { signal: AbortSignal }) => duxClient.executeQueryFiltered(spec.dux, filters, { signal }),
      placeholderData: keepPreviousData,
      retry: 0,
      staleTime: 15_000,
      refetchInterval,
      refetchIntervalInBackground: true,
    })),
  });
  return {
    byLayer: Object.fromEntries(specs.map((spec, i) => [spec.id, results[i]?.data])),
    loading: results.some((r) => r.isFetching),
    error: (results.find((r) => r.error)?.error as Error | undefined) ?? null,
  };
}

export interface MapFeatureProperties {
  value: number | null;
  w: number;
  label: string;
  category: string;
  __color?: string;
  __dims: string;
  __selected: boolean;
  __dimmed: boolean;
}

export function categoryColor(category: string, categories: string[], palette: string[], fallback: string): string {
  const index = categories.indexOf(category);
  return index >= 0 && palette.length ? palette[index % palette.length] : fallback;
}

export function layerCategories(layer: MapLayer, res: QueryResponse | undefined): string[] {
  if (!layer.category || !res) return [];
  const index = res.columns.indexOf(layer.category.name);
  if (index < 0) return [];
  return [...new Set(res.rows.map((row) => String(row[index] ?? "")))].sort((a, b) => a.localeCompare(b));
}

export function coordinateExtent(points: [number, number][]): [[number, number], [number, number]] | null {
  if (!points.length) return null;
  let west = Infinity, south = Infinity, east = -Infinity, north = -Infinity;
  for (const [lng, lat] of points) {
    west = Math.min(west, lng); south = Math.min(south, lat);
    east = Math.max(east, lng); north = Math.max(north, lat);
  }
  return [[west, south], [east, north]];
}

export function layerGeoJSON(layer: MapLayer, res: QueryResponse | undefined, selectedKeys: Set<string>) {
  const features: Array<{
    type: "Feature";
    geometry: { type: "Point"; coordinates: [number, number] };
    properties: MapFeatureProperties;
  }> = [];
  if (!res || !layer.lng || !layer.lat) return { type: "FeatureCollection" as const, features };
  const li = res.columns.indexOf(layer.lng.name);
  const ai = res.columns.indexOf(layer.lat.name);
  const si = layer.size ? res.columns.indexOf(layer.size.name) : -1;
  const ci = layer.category ? res.columns.indexOf(layer.category.name) : -1;
  if (li < 0 || ai < 0) return { type: "FeatureCollection" as const, features };
  const max = si < 0 ? 0 : res.rows.reduce((m, row) => {
    const lng = Number(row[li]);
    const lat = Number(row[ai]);
    return Number.isFinite(lng) && lng >= -180 && lng <= 180 && Number.isFinite(lat) && lat >= -90 && lat <= 90
      ? Math.max(m, Number(row[si]) || 0)
      : m;
  }, 0);

  for (const row of res.rows) {
    const lng = Number(row[li]);
    const lat = Number(row[ai]);
    if (!Number.isFinite(lng) || !Number.isFinite(lat) || lng < -180 || lng > 180 || lat < -90 || lat > 90) continue;
    const value = si >= 0 && row[si] != null && Number.isFinite(Number(row[si])) ? Number(row[si]) : null;
    const dims = layer.category && ci >= 0
      ? [{ table: layer.category.table, column: layer.category.name, value: (row[ci] ?? "") as string | number }]
      : [
          { table: layer.lng.table, column: layer.lng.name, value: lng },
          { table: layer.lat.table, column: layer.lat.name, value: lat },
        ];
    const key = markKey({ dims } as CrossMark);
    const selected = selectedKeys.has(key);
    features.push({
      type: "Feature",
      geometry: { type: "Point", coordinates: [lng, lat] },
      properties: {
        value,
        w: max > 0 && value != null ? Math.max(0, value) / max : 0,
        label: ci >= 0 ? String(row[ci] ?? "") : `${lat.toFixed(4)}, ${lng.toFixed(4)}`,
        category: ci >= 0 ? String(row[ci] ?? "") : "",
        __dims: JSON.stringify(dims),
        __selected: selected,
        __dimmed: selectedKeys.size > 0 && !selected,
      },
    });
  }
  return { type: "FeatureCollection" as const, features };
}
