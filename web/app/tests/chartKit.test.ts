import { expect, test } from "bun:test";
import { formatCategoryTick } from "../src/dash/charts/ChartKit";
import comboChart from "../src/dash/visuals/ComboChart";
import lineChart from "../src/dash/visuals/LineChart";

test("category ticks compact ISO timestamps without losing other dimensions", () => {
  expect(formatCategoryTick("2022-08-06T00:00:00Z")).toBe("2022-08-06");
  expect(formatCategoryTick("2022-08-06T14:35:42Z · North")).toBe("2022-08-06 14:35 · North");
  expect(formatCategoryTick("North")).toBe("North");
});

test("line chart smooth option applies to every series and defaults off", () => {
  const series = (smoothLines?: boolean) => lineChart.series!({
    viz: { smoothLines }, palette: ["red", "blue"], keys: ["Sales", "Profit"], split: false,
  });
  expect(lineChart.options?.some((o) => o.key === "smoothLines")).toBe(true);
  expect(series().map((s) => s.smooth)).toEqual([undefined, undefined]);
  expect(series(true).map((s) => s.smooth)).toEqual([true, true]);
});

test("combo line settings leave its bars straight and on the left axis", () => {
  const series = comboChart.series!({
    viz: { lines: ["Margin"], smoothLines: true, zoomY: true },
    palette: ["red", "blue"], keys: ["Sales", "Margin"], split: false,
  });
  expect(comboChart.options?.map((o) => o.key)).toContain("zoomY");
  expect(comboChart.options?.map((o) => o.key)).toContain("smoothLines");
  expect(series.map((s) => [s.kind, s.axis ?? "left", s.smooth])).toEqual([
    ["bar", "left", undefined], ["line", "right", true],
  ]);
});
