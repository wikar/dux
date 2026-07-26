import { expect, test } from "bun:test";
import { formatCompactValue, formatValue } from "./format";

test("formats untyped numbers with locale-aware grouping", () => {
  expect(formatValue("1234567.89", undefined, "en-US")).toBe("1,234,567.89");
  expect(formatValue("1234567.89", undefined, "de-DE")).toBe("1.234.567,89");
  expect(formatValue("SKU-1234", undefined, "en-US")).toBe("SKU-1234");
});

const EUR = { kind: "currency" as const, decimals: 2, currency: "EUR" };

test("scales axis values to T / M / B", () => {
  expect(formatCompactValue(1500, undefined, "en-US")).toBe("1.5T");
  expect(formatCompactValue(15_300_000, undefined, "en-US")).toBe("15.3M");
  expect(formatCompactValue(526_650_915, undefined, "en-US")).toBe("527M");
  expect(formatCompactValue(2_400_000_000, undefined, "en-US")).toBe("2.4B");
  // No T for trillions — the scale keeps counting in B.
  expect(formatCompactValue(1e12, undefined, "en-US")).toBe("1,000B");
});

test("keeps the currency symbol in its locale position when scaling", () => {
  expect(formatCompactValue(15_300_000, EUR, "en-US")).toBe("€15.3M");
  expect(formatCompactValue(-15_300_000, EUR, "en-US")).toBe("-€15.3M");
  // de-DE separates the amount from the symbol with a non-breaking space.
  expect(formatCompactValue(15_300_000, EUR, "de-DE")).toBe("15,3M €");
});

test("leaves short values and percentages to formatValue", () => {
  expect(formatCompactValue(999, EUR, "en-US")).toBe("€999.00");
  expect(formatCompactValue(0.924, { kind: "percent", decimals: 1 }, "en-US")).toBe("92.4%");
  expect(formatCompactValue(null, EUR, "en-US")).toBe("");
});
