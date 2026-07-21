import { expect, test } from "bun:test";
import { categoryColor, coordinateExtent, layerCategories, layerDux, layerGeoJSON, mapThemeColors, resolveMapStyle } from "../src/dash/mapData";
import type { DashElement, MapLayer, ThemeTokens } from "../src/dash/types";

const layer: MapLayer = {
  id: "layer-1",
  kind: "circle",
  lng: { table: "Store", name: "Longitude", kind: "column", dataType: "DOUBLE" },
  lat: { table: "Store", name: "Latitude", kind: "column", dataType: "DOUBLE" },
  size: { table: "Sales", name: "Revenue", kind: "measure" },
  category: { table: "Store", name: "Name", kind: "column", dataType: "VARCHAR" },
};

test("map layer query and GeoJSON keep filter identity", () => {
  const el = {
    id: "map-1",
    type: "map",
    layout: { x: 0, y: 0, w: 420, h: 320 },
    query: { mode: "builder", filters: [] },
  } satisfies DashElement;
  const dux = layerDux(el, layer);
  expect(dux).toContain("Longitude");
  expect(dux).toContain("Revenue");

  const geo = layerGeoJSON(layer, {
    columns: ["Name", "Longitude", "Latitude", "Revenue"],
    rows: [["Stockholm", 18.0686, 59.3293, 25], ["bad", 999, 0, 100]],
  }, new Set());
  expect(geo.features).toHaveLength(1);
  expect(geo.features[0].properties.w).toBe(1);
  expect(geo.features[0].properties.category).toBe("Stockholm");
  expect(JSON.parse(geo.features[0].properties.__dims)).toEqual([
    { table: "Store", column: "Name", value: "Stockholm" },
  ]);
});

test("map categories and basemap colors follow the theme", () => {
  const theme = {
    background: "#101010",
    elementBackground: "rgba(24, 24, 37, 0.8)",
    border: "#45475a",
    text: "#cdd6f4",
  } as Required<ThemeTokens>;
  expect(resolveMapStyle()).toContain("positron");
  expect(mapThemeColors(theme)).toEqual({
    background: "rgba(24, 24, 37, 0.8)",
    land: "rgba(24, 24, 37, 0.8)",
    water: "rgba(231, 231, 218, 0.08)",
    border: "#45475a",
    roads: "rgba(69, 71, 90, 0.55)",
    text: "#cdd6f4",
  });
  expect(layerCategories(layer, {
    columns: ["Name"],
    rows: [["Cafe"], ["Bar"], ["Cafe"]],
  })).toEqual(["Bar", "Cafe"]);
});

test("map extent contains every rendered point", () => {
  expect(coordinateExtent([[13.4, 52.5], [9.9, 53.6], [11.6, 48.1]])).toEqual([[9.9, 48.1], [13.4, 53.6]]);
  expect(coordinateExtent([])).toBeNull();
});

test("first map category uses the first theme color", () => {
  const categories = ["Bar", "Cafe", "Restaurant"];
  const palette = ["#first", "#second", "#third"];
  expect(categoryColor("Bar", categories, palette, "#fallback")).toBe("#first");
  expect(categoryColor("Cafe", categories, palette, "#fallback")).toBe("#second");
});
