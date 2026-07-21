import { expect, test } from "bun:test";
import { formatCategoryTick } from "../src/dash/charts/ChartKit";

test("category ticks compact ISO timestamps without losing other dimensions", () => {
  expect(formatCategoryTick("2022-08-06T00:00:00Z")).toBe("2022-08-06");
  expect(formatCategoryTick("2022-08-06T14:35:42Z · North")).toBe("2022-08-06 14:35 · North");
  expect(formatCategoryTick("North")).toBe("North");
});
