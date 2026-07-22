import { expect, test } from "bun:test";
import { formatValue } from "./format";

test("formats untyped numbers with locale-aware grouping", () => {
  expect(formatValue("1234567.89", undefined, "en-US")).toBe("1,234,567.89");
  expect(formatValue("1234567.89", undefined, "de-DE")).toBe("1.234.567,89");
  expect(formatValue("SKU-1234", undefined, "en-US")).toBe("SKU-1234");
});
