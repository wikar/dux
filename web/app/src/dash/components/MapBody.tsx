import { useEffect, useMemo, useRef } from "react";
import maplibregl from "maplibre-gl";
import "maplibre-gl/dist/maplibre-gl.css";
import { formatValue } from "@dux/core";
import styles from "./MapBody.module.css";
import { useFormats, usePalette, useResolvedTheme } from "../data";
import { categoryColor, coordinateExtent, isDarkColor, layerCategories, layerGeoJSON, mapThemeColors, resolveMapStyle, useMapLayerData } from "../mapData";
import { updateElement } from "../docOps";
import { markKey, useDocStore, useUiStore } from "../store";
import type { DashElement, MapLayer } from "../types";

const sourceId = (id: string) => `dux-map-source-${id}`;
const renderId = (layer: MapLayer) => `dux-map-${layer.kind}-${layer.id}`;
const EMPTY = { type: "FeatureCollection" as const, features: [] };
const pinImageId = (index: number) => `dux-map-pin-${index}`;

function syncPinImages(map: maplibregl.Map, palette: string[], stroke: string, center: string) {
  palette.forEach((color, index) => {
    const id = pinImageId(index);
    if (map.hasImage(id)) map.removeImage(id);
    const canvas = document.createElement("canvas");
    canvas.width = 48;
    canvas.height = 56;
    const ctx = canvas.getContext("2d")!;
    ctx.scale(2, 2);
    ctx.beginPath();
    ctx.moveTo(12, 27);
    ctx.bezierCurveTo(10.5, 24, 2, 17, 2, 10.5);
    ctx.bezierCurveTo(2, 4.7, 6.5, 1, 12, 1);
    ctx.bezierCurveTo(17.5, 1, 22, 4.7, 22, 10.5);
    ctx.bezierCurveTo(22, 17, 13.5, 24, 12, 27);
    ctx.closePath();
    ctx.fillStyle = color;
    ctx.fill();
    ctx.lineWidth = 1.5;
    ctx.strokeStyle = stroke;
    ctx.stroke();
    ctx.beginPath();
    ctx.arc(12, 10.5, 4, 0, Math.PI * 2);
    ctx.fillStyle = center;
    ctx.fill();
    map.addImage(id, ctx.getImageData(0, 0, canvas.width, canvas.height), { pixelRatio: 2 });
  });
}

function categoryMatch(categories: string[], values: string[], fallback: string) {
  if (categories.length === 0) return fallback;
  return ["match", ["get", "category"], ...categories.flatMap((category, index) => [category, values[index]]), fallback];
}

function syncBasemapTheme(map: maplibregl.Map, theme: ReturnType<typeof mapThemeColors>) {
  for (const layer of map.getStyle().layers ?? []) {
    const id = layer.id;
    const subject = `${id} ${"source-layer" in layer ? layer["source-layer"] ?? "" : ""}`.toLowerCase();
    if (layer.type === "background") {
      map.setPaintProperty(id, "background-color", theme.background);
    } else if (layer.type === "fill") {
      map.setPaintProperty(id, "fill-color", subject.includes("water") ? theme.water : theme.land);
      map.setPaintProperty(id, "fill-outline-color", theme.border);
    } else if (layer.type === "line") {
      map.setPaintProperty(id, "line-color", subject.includes("boundary") ? theme.border : subject.includes("water") ? theme.water : theme.roads);
    } else if (layer.type === "symbol") {
      map.setPaintProperty(id, "text-color", theme.text);
      map.setPaintProperty(id, "text-halo-color", theme.land);
      map.setPaintProperty(id, "text-halo-width", 1);
    }
  }
}

function popupContent(label: string, value: string) {
  const root = document.createElement("div");
  root.className = styles.tooltip;
  const title = document.createElement("div");
  title.className = styles.tooltipLabel;
  title.textContent = label;
  root.appendChild(title);
  if (value) {
    const metric = document.createElement("div");
    metric.className = styles.tooltipValue;
    metric.textContent = value;
    root.appendChild(metric);
  }
  return root;
}

export default function MapBody({ el }: { el: DashElement }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const mapRef = useRef<maplibregl.Map | null>(null);
  const readyRef = useRef(false);
  const managedLayersRef = useRef<string[]>([]);
  const managedSourcesRef = useRef<string[]>([]);
  const popupRef = useRef<maplibregl.Popup | null>(null);
  const frameRef = useRef(0);
  const extentRef = useRef("");
  const skipInitialFitRef = useRef(Boolean(el.viz?.center || el.viz?.zoom));

  const doc = useDocStore((s) => s.doc);
  const mode = useUiStore((s) => s.mode);
  const crossSelection = useUiStore((s) => s.crossFilters[el.id]);
  const toggleCrossMark = useUiStore((s) => s.toggleCrossMark);
  const clearCrossFilters = useUiStore((s) => s.clearCrossFilters);
  const select = useUiStore((s) => s.select);
  const theme = useResolvedTheme(doc);
  const palette = usePalette(doc);
  const formats = useFormats();
  const layers = useMemo(() => el.viz?.layers ?? [], [el.viz]);
  const selectedKeys = useMemo(() => new Set((crossSelection ?? []).map(markKey)), [crossSelection]);
  const styleUrl = resolveMapStyle();
  const { byLayer, loading, error } = useMapLayerData(el);

  const latest = useRef({ layers, palette, theme, byLayer, selectedKeys, mode, formats });
  latest.current = { layers, palette, theme, byLayer, selectedKeys, mode, formats };

  function syncLayers() {
    const map = mapRef.current;
    if (!map || !readyRef.current || !map.isStyleLoaded()) return;
    for (const id of managedLayersRef.current) if (map.getLayer(id)) map.removeLayer(id);
    const nextSources = latest.current.layers.map((layer) => sourceId(layer.id));
    for (const id of managedSourcesRef.current) {
      if (!nextSources.includes(id) && map.getSource(id)) map.removeSource(id);
    }
    managedLayersRef.current = [];
    managedSourcesRef.current = nextSources;
    syncBasemapTheme(map, mapThemeColors(latest.current.theme));
    if (latest.current.layers.some((layer) => layer.kind === "pin")) {
      syncPinImages(map, latest.current.palette, latest.current.theme.text, latest.current.theme.elementBackground);
    }

    latest.current.layers.forEach((layer, index) => {
      const source = sourceId(layer.id);
      if (!map.getSource(source)) map.addSource(source, { type: "geojson", data: EMPTY });
      const id = renderId(layer);
      const palette = latest.current.palette.length ? latest.current.palette : ["#89b4fa"];
      const color = palette[index % palette.length];
      const categories = layerCategories(layer, latest.current.byLayer[layer.id]);
      if (layer.kind === "heatmap") {
        map.addLayer({
          id,
          type: "heatmap",
          source,
          paint: {
            "heatmap-weight": layer.size ? ["interpolate", ["linear"], ["get", "w"], 0, 0, 1, 1] : 0.6,
            "heatmap-intensity": 1,
            "heatmap-radius": 42,
            "heatmap-color": ["interpolate", ["linear"], ["heatmap-density"], 0, "rgba(0,0,0,0)", 0.5, color, 1, color],
            "heatmap-opacity": 0.85,
          },
        } as maplibregl.HeatmapLayerSpecification);
      } else if (layer.kind === "pin") {
        const icons = palette.map((_, i) => pinImageId(i)).reverse();
        const categoryIcons = categories.map((_, categoryPosition) => icons[categoryPosition % icons.length]);
        map.addLayer({
          id,
          type: "symbol",
          source,
          layout: {
            "icon-image": layer.category
              ? categoryMatch(categories, categoryIcons, icons[index % icons.length])
              : icons[index % icons.length],
            "icon-anchor": "bottom",
            "icon-allow-overlap": true,
          },
          paint: {
            "icon-opacity": ["case", ["get", "__selected"], 1, ["get", "__dimmed"], 0.25, 0.95],
          },
        } as maplibregl.SymbolLayerSpecification);
      } else {
        map.addLayer({
          id,
          type: "circle",
          source,
          paint: {
            "circle-color": layer.category ? ["to-color", ["get", "__color"]] : color,
            "circle-stroke-color": latest.current.theme.text,
            "circle-stroke-width": 1,
            "circle-opacity": ["case", ["get", "__selected"], 0.95, ["get", "__dimmed"], 0.25, 0.75],
            "circle-radius": layer.size
              ? ["interpolate", ["linear"], ["get", "w"], 0, 4, 1, 24]
              : 6,
          },
        } as maplibregl.CircleLayerSpecification);
      }
      managedLayersRef.current.push(id);
    });
  }

  function syncData() {
    const map = mapRef.current;
    if (!map || !readyRef.current) return;
    const points: [number, number][] = [];
    const palette = latest.current.palette.length ? latest.current.palette : ["#89b4fa"];
    for (const [layerIndex, layer] of latest.current.layers.entries()) {
      const source = map.getSource(sourceId(layer.id)) as maplibregl.GeoJSONSource | undefined;
      const data = layerGeoJSON(layer, latest.current.byLayer[layer.id], latest.current.selectedKeys);
      const categories = layerCategories(layer, latest.current.byLayer[layer.id]);
      const fallback = palette[layerIndex % palette.length];
      for (const feature of data.features) {
        feature.properties.__color = categoryColor(feature.properties.category, categories, palette, fallback);
      }
      source?.setData(data);
      for (const feature of data.features) points.push(feature.geometry.coordinates);
    }
    const extent = coordinateExtent(points);
    if (!extent) {
      extentRef.current = "";
      return;
    }
    const signature = extent.flat().join(",");
    if (signature === extentRef.current) return;
    extentRef.current = signature;
    if (skipInitialFitRef.current) {
      skipInitialFitRef.current = false;
      return;
    }
    map.fitBounds(extent, { padding: 32, maxZoom: 12, duration: 0 });
  }

  function interactiveLayerIds() {
    const map = mapRef.current;
    return map ? latest.current.layers.filter((l) => l.kind !== "heatmap").map(renderId).filter((id) => map.getLayer(id)) : [];
  }

  useEffect(() => {
    if (!containerRef.current) return;
    const map = new maplibregl.Map({
      container: containerRef.current,
      style: styleUrl,
      center: el.viz?.center ?? [0, 20],
      zoom: el.viz?.zoom ?? 1.2,
      attributionControl: { compact: true },
    });
    mapRef.current = map;
    popupRef.current = new maplibregl.Popup({ closeButton: false, closeOnClick: false, offset: 10, className: "dux-map-popup" });
    map.addControl(new maplibregl.NavigationControl({ showCompass: false }), "top-right");
    map.on("load", () => {
      readyRef.current = true;
      syncLayers();
      syncData();
    });
    map.on("click", (event) => {
      if (latest.current.mode !== "view") return;
      const ids = interactiveLayerIds();
      const hit = ids.length ? map.queryRenderedFeatures(event.point, { layers: ids })[0] : undefined;
      if (!hit) return clearCrossFilters();
      const raw = hit.properties?.__dims;
      const dims = typeof raw === "string" ? JSON.parse(raw) : raw;
      if (Array.isArray(dims) && dims.length) {
        toggleCrossMark(el.id, { dims }, event.originalEvent.ctrlKey || event.originalEvent.metaKey);
      }
    });
    map.on("mousemove", (event) => {
      const ids = interactiveLayerIds();
      const hit = ids.length ? map.queryRenderedFeatures(event.point, { layers: ids })[0] : undefined;
      map.getCanvas().style.cursor = hit ? "pointer" : "";
      if (!hit) return popupRef.current?.remove();
      const value = hit.properties?.value;
      const layer = latest.current.layers.find((l) => renderId(l) === hit.layer.id);
      const formatted = value == null || value === "" ? "" : formatValue(Number(value), layer?.size ? latest.current.formats[layer.size.name] : undefined);
      popupRef.current?.setLngLat(event.lngLat).setDOMContent(popupContent(String(hit.properties?.label ?? ""), formatted)).addTo(map);
    });
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(frameRef.current);
      frameRef.current = requestAnimationFrame(() => map.resize());
    });
    observer.observe(containerRef.current);
    const saveView = (event: Event) => {
      if ((event as CustomEvent<string>).detail !== el.id) return;
      const center = map.getCenter();
      updateElement(el.id, (x) => ({ ...x, viz: { ...x.viz, center: [center.lng, center.lat], zoom: map.getZoom() } }));
    };
    window.addEventListener("dux-map-save-view", saveView);
    return () => {
      window.removeEventListener("dux-map-save-view", saveView);
      observer.disconnect();
      cancelAnimationFrame(frameRef.current);
      popupRef.current?.remove();
      map.remove();
      mapRef.current = null;
      readyRef.current = false;
    };
    // The instance deliberately survives data, theme, selection, and mode changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const map = mapRef.current;
    if (!map || !readyRef.current) return;
    readyRef.current = false;
    map.setStyle(styleUrl);
    map.once("style.load", () => {
      readyRef.current = true;
      managedLayersRef.current = [];
      managedSourcesRef.current = [];
      syncLayers();
      syncData();
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [styleUrl]);

  useEffect(() => {
    syncLayers();
    syncData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [layers, palette, theme.text, theme.elementBackground, theme.border]);
  useEffect(() => {
    syncLayers();
    syncData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [byLayer]);
  useEffect(() => { syncData(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [selectedKeys]);

  const renderable = layers.some((layer) => layer.lng && layer.lat);
  const legendGroups = layers.flatMap((layer) => {
    if (!layer.category || layer.kind === "heatmap") return [];
    const colors = palette.length ? palette : ["#89b4fa"];
    const reversed = layer.kind === "pin" ? [...colors].reverse() : colors;
    return [{
      id: layer.id,
      title: layer.category.name,
      entries: layerCategories(layer, byLayer[layer.id]).map((category, categoryPosition) => ({
        category,
        color: reversed[categoryPosition % reversed.length],
      })),
    }];
  }).filter((group) => group.entries.length > 0);
  return (
    <div
      className={styles.wrap}
      style={{
        "--map-bg": theme.elementBackground,
        "--map-text": theme.text,
        "--map-border": theme.border,
        "--map-icon-filter": isDarkColor(theme.text) ? "none" : "invert(1)",
      } as React.CSSProperties}
      onPointerDown={(event) => {
        if (mode !== "edit") return;
        event.stopPropagation();
        select(el.id);
      }}
    >
      <div ref={containerRef} className={styles.canvas} />
      {!renderable && <div className={styles.message}>Add longitude and latitude to a layer</div>}
      {loading && renderable && <div className={styles.loading} />}
      {error && <div className={styles.error}>{error.message}</div>}
      {legendGroups.length > 0 && (
        <div className={styles.legend}>
          {legendGroups.map((group) => (
            <div key={group.id} className={styles.legendGroup}>
              <div className={styles.legendTitle}>{group.title}</div>
              {group.entries.map((entry) => (
                <div key={entry.category} className={styles.legendItem}>
                  <span className={styles.legendSwatch} style={{ background: entry.color }} />
                  <span>{entry.category}</span>
                </div>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
