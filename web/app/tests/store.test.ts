import { expect, test } from "bun:test";
import { markKey, useUiStore, type CrossMark } from "../src/dash/store";

const mark = (value: string): CrossMark => ({
  dims: [{ table: "Sales", column: "Region", value }],
});

test("cross-filter click semantics", () => {
  const ui = useUiStore.getState();
  ui.clearCrossFilters();

  ui.toggleCrossMark("chart-a", mark("North"), false);
  ui.toggleCrossMark("chart-b", mark("South"), true);
  ui.toggleCrossMark("chart-a", mark("North"), false);
  expect(useUiStore.getState().crossFilters).toEqual({});

  ui.toggleCrossMark("chart-a", mark("North"), true);
  ui.toggleCrossMark("chart-a", mark("South"), true);
  ui.toggleCrossMark("chart-a", mark("North"), true);
  expect(useUiStore.getState().crossFilters["chart-a"]?.map(markKey)).toEqual([markKey(mark("South"))]);
});
