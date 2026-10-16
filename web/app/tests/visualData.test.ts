import { expect, test } from "bun:test";
import { prepareVisualData } from "../src/dash/charts/chartData";
import type { MeasureFormat, QueryResponse } from "@dux/core";
import type { DashElement } from "../src/dash/types";

const element: DashElement = {
  id: "chart", type: "bar", layout: { x: 0, y: 0, w: 360, h: 240 },
  query: { mode: "builder", fields: [
    { table: "Sales", name: "Region", kind: "column" },
    { table: "Sales", name: "Channel", kind: "column" },
    { table: "Sales", name: "Revenue", kind: "measure" },
  ] },
};
const spec = { wells: [], chart: true };
const formats: Record<string, MeasureFormat> = { Revenue: { kind: "currency", currency: "EUR" } };
const splitData: QueryResponse = {
  columns: ["Region", "Channel", "Revenue"],
  rows: [["North", "Web", 30], ["North", "Store", 5], ["South", "Store", 50], ["South", "Web", 10], ["East", "Web", 40]],
};

test("visual preparation drops null-only rows while preserving zero and dimension identity", () => {
  const input: QueryResponse = { columns: ["Region", "Channel", "Revenue"], rows: [["North", "Web", null], ["South", "Store", 0], ["East", "Web", 12]] };
  const result = prepareVisualData(element, spec, input, formats);
  expect(result.rows.map((r) => r.Revenue)).toEqual([0, 12]);
  expect(result.rows[0].__dims).toEqual([
    { table: "Sales", column: "Region", value: "South" },
    { table: "Sales", column: "Channel", value: "Store" },
  ]);
  expect(result.crossFilterable).toBe(true);
  expect(prepareVisualData({ ...element, viz: { showEmpty: true } }, spec, input, formats).rows).toHaveLength(3);
  expect(prepareVisualData(element, { ...spec, dropEmpty: false }, input, formats).rows).toHaveLength(3);
  expect(input.rows).toHaveLength(3);
});

test("raw visual preparation infers category and metrics and disables cross-filtering", () => {
  const raw: DashElement = { ...element, query: { mode: "raw", raw: "EVALUATE Sales" } };
  const result = prepareVisualData(raw, spec, { columns: ["Category", "Amount", "Count"], rows: [["A", 4, 0]] }, formats);
  expect(result.keys).toEqual(["Amount", "Count"]);
  expect(result.rows[0]).toMatchObject({ __x: "A", Amount: 4, Count: 0 });
  expect(result.crossFilterable).toBe(false);
});

test("split Top N selects whole stack totals and preserves query order without a metric sort", () => {
  const el = { ...element, query: { ...element.query!, topN: 2 } };
  const result = prepareVisualData(el, spec, splitData, formats, "Channel");
  expect(result.rows.map((r) => r.__x)).toEqual(["South", "East"]);
  expect(result.keys).toEqual(["Store", "Web"]);
  expect(result.rows[0]).toMatchObject({ Store: 50, Web: 10, __dims: [{ table: "Sales", column: "Region", value: "South" }] });
  expect(result.seriesDim).toEqual({ table: "Sales", column: "Channel" });
  expect(result.formats).toEqual({ Store: formats.Revenue, Web: formats.Revenue });
  expect(splitData.rows).toHaveLength(5);
});

test("split metric sorting uses totals after Top N for either direction", () => {
  for (const dir of ["asc", "desc"] as const) {
    const el = { ...element, query: { ...element.query!, topN: 2, sort: [{ field: "Revenue", dir }] } };
    const result = prepareVisualData(el, spec, splitData, formats, "Channel");
    expect(result.rows.map((r) => r.__x)).toEqual(dir === "asc" ? ["East", "South"] : ["South", "East"]);
  }
});

test("split preparation retains blank series and missing segment values", () => {
  const result = prepareVisualData(element, spec, { columns: splitData.columns, rows: [["A", null, 5], ["B", "Web", 7]] }, {}, "Channel");
  expect(result.keys).toEqual(["(blank)", "Web"]);
  expect(result.rows.map((r) => [r["(blank)"], r.Web])).toEqual([[5, undefined], [undefined, 7]]);
});

test("non-chart and empty results preserve data without inventing plotted series", () => {
  const table = prepareVisualData(element, { wells: [] }, splitData, formats);
  expect(table.data).toEqual(splitData);
  expect(table.rows).toEqual([]);
  expect(table.keys).toEqual([]);
  const empty = prepareVisualData(element, spec, { columns: splitData.columns, rows: [] }, formats);
  expect(empty.rows).toEqual([]);
  expect(empty.keys).toEqual(["Revenue"]);
  const scalar = { ...element, query: { mode: "builder" as const, fields: [element.query!.fields![2]] } };
  expect(prepareVisualData(scalar, spec, splitData, formats).crossFilterable).toBe(false);
});
