import { expect, test } from "bun:test";
import { buildBarSeries } from "../src/dash/visuals/BarChart";
import { buildAreaSeries } from "../src/dash/visuals/AreaChart";
import { markClickHandler, markDims, markOpacity } from "../src/dash/charts/series";
import { markKey } from "../src/dash/store";
import { formatCategoryTick } from "../src/dash/charts/CartesianFrame";
import comboChart, { buildComboSeries } from "../src/dash/visuals/ComboChart";
import lineChart, { buildLineSeries } from "../src/dash/visuals/LineChart";

test("category ticks compact ISO timestamps without losing other dimensions", () => {
  expect(formatCategoryTick("2022-08-06T00:00:00Z")).toBe("2022-08-06");
  expect(formatCategoryTick("2022-08-06T14:35:42Z · North")).toBe("2022-08-06 14:35 · North");
  expect(formatCategoryTick("North")).toBe("North");
});

test("line chart smooth option applies to every series and defaults off", () => {
  const series = (smoothLines?: boolean) => buildLineSeries({
    viz: { smoothLines }, palette: ["red", "blue"], keys: ["Sales", "Profit"], split: false,
  });
  expect(lineChart.options?.some((o) => o.key === "smoothLines")).toBe(true);
  expect(series().map((s) => s.smooth)).toEqual([undefined, undefined]);
  expect(series(true).map((s) => s.smooth)).toEqual([true, true]);
});

test("combo line settings leave its bars straight and on the left axis", () => {
  const series = buildComboSeries({
    viz: { lines: ["Margin"], smoothLines: true, zoomY: true },
    palette: ["red", "blue"], keys: ["Sales", "Margin"], split: false,
  });
  expect(comboChart.options?.map((o) => o.key)).toContain("zoomY");
  expect(comboChart.options?.map((o) => o.key)).toContain("smoothLines");
  expect(series.map((s) => [s.kind, s.axis ?? "left", s.smooth])).toEqual([
    ["bar", "left", undefined], ["line", "right", true],
  ]);
});

test("bar and area preserve stacking, palette order, and their different mark styles", () => {
  for (const stacked of [false, true]) {
    const ctx = { viz: { stacked }, palette: ["red", "blue"], keys: ["Sales", "Profit", "Costs"], split: false };
    const bars = buildBarSeries(ctx);
    const areas = buildAreaSeries(ctx);
    expect(bars.map((s) => s.color)).toEqual(["red", "blue", "red"]);
    expect(bars.map((s) => s.stackId)).toEqual(Array(3).fill(stacked ? "stack" : undefined));
    expect(bars.map((s) => s.radius)).toEqual(Array(3).fill(stacked ? 0 : 2));
    expect(areas.map((s) => [s.stackId, s.fillOpacity, s.dot])).toEqual(Array(3).fill([stacked ? "stack" : undefined, stacked ? 0.7 : 0.3, { base: 0, highlight: true }]));
  }
});

test("line series order matches the axis and palette, while split values ignore metric axis settings", () => {
  const ctx = { viz: { y2: ["Profit"] }, palette: ["red", "blue"], keys: ["Profit", "Sales"], split: false };
  expect(buildLineSeries(ctx).map((s) => [s.key, s.axis, s.color])).toEqual([["Sales", "left", "red"], ["Profit", "right", "blue"]]);
  expect(buildLineSeries({ ...ctx, split: true }).map((s) => [s.key, s.axis])).toEqual([["Profit", "left"], ["Sales", "left"]]);
});

test("combo ignores unavailable line fields and can put its lines on the left", () => {
  const series = buildComboSeries({ viz: { lines: ["Missing", "Profit"], lineY2: false }, palette: ["red", "blue"], keys: ["Profit", "Sales"], split: false });
  expect(series.map((s) => [s.key, s.kind, s.axis ?? "left", s.color])).toEqual([["Sales", "bar", "left", "red"], ["Profit", "line", "left", "blue"]]);
});

test("mark handlers preserve row and split identities, additive selection, and disabled interaction", () => {
  const row = { __x: "North", __dims: [{ table: "Sales", column: "Region", value: "North" }] };
  const split = { table: "Sales", column: "Channel" };
  const dims = [...row.__dims, { ...split, value: "Web" }];
  const clicks: unknown[] = [];
  const handler = markClickHandler((d, additive) => clicks.push([d, additive]), split, "Web")!;
  handler({ payload: row }, 0, { ctrlKey: true });
  handler(row, 0, { metaKey: true });
  handler(row, 0, {});
  expect(clicks).toEqual([[dims, true], [dims, true], [dims, false]]);
  expect(markClickHandler(undefined)).toBeUndefined();
  expect(markDims(row, split, "Web")).toEqual(dims);
  expect(row.__dims).toHaveLength(1);
  const selected = new Set([markKey({ dims })]);
  expect(markOpacity(selected, dims)).toBe(1);
  expect(markOpacity(selected, row.__dims)).toBe(0.25);
  expect(markOpacity(new Set(), row.__dims)).toBe(1);
});
