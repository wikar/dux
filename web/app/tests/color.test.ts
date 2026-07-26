import { expect, test } from "bun:test";
import { normalizeColor, parseColor, toHexa, withAlpha } from "../src/dash/components/color";

test("parseColor accepts the hex shapes a paste actually carries", () => {
  // Canonical, and the same value without the # a copy often drops.
  expect(parseColor("#1e66f5")).toEqual({ r: 30, g: 102, b: 245, a: 1 });
  expect(parseColor("1e66f5")).toEqual({ r: 30, g: 102, b: 245, a: 1 });
  expect(parseColor("  #1E66F5  ")).toEqual({ r: 30, g: 102, b: 245, a: 1 });
  // Shorthand expands per digit, with and without an alpha nibble.
  expect(parseColor("#abc")).toEqual({ r: 170, g: 187, b: 204, a: 1 });
  expect(parseColor("#abcf")).toEqual({ r: 170, g: 187, b: 204, a: 1 });
  // 8-digit hex carries alpha.
  expect(parseColor("#1e66f580")?.a).toBeCloseTo(128 / 255, 5);
  // rgb / rgba, including the percentage alpha form.
  expect(parseColor("rgba(24, 24, 37, 0.82)")).toEqual({ r: 24, g: 24, b: 37, a: 0.82 });
  expect(parseColor("rgb(24 24 37)")).toEqual({ r: 24, g: 24, b: 37, a: 1 });
  expect(parseColor("rgba(24, 24, 37, 50%)")?.a).toBeCloseTo(0.5, 5);
  // Not a color we can decompose — callers keep the raw string.
  expect(parseColor("transparent")).toBeNull();
  expect(parseColor("")).toBeNull();
  expect(parseColor("#12345")).toBeNull();
});

test("normalizeColor emits rgba only when alpha is set", () => {
  expect(normalizeColor("1e66f5")).toBe("#1e66f5");
  expect(normalizeColor("#ABC")).toBe("#aabbcc");
  expect(normalizeColor("rgba(30, 102, 245, 1)")).toBe("#1e66f5");
  expect(normalizeColor("#1e66f580")).toBe("rgba(30, 102, 245, 0.502)");
  expect(normalizeColor("transparent")).toBe("transparent");
});

test("toHexa always hands the picker 8 digits", () => {
  expect(toHexa("#1e66f5")).toBe("#1e66f5ff");
  expect(toHexa("rgba(30, 102, 245, 0.5)")).toBe("#1e66f580");
  // Unparseable → the neutral base, never an empty string the panel rejects.
  expect(toHexa("transparent")).toBe("#1e1e2eff");
});

test("withAlpha keeps RGB and clamps the channel", () => {
  expect(withAlpha("#1e66f5", 0.4)).toBe("rgba(30, 102, 245, 0.4)");
  // Fully opaque collapses back to hex rather than a redundant rgba().
  expect(withAlpha("rgba(30, 102, 245, 0.4)", 1)).toBe("#1e66f5");
  expect(withAlpha("#1e66f5", 0)).toBe("rgba(30, 102, 245, 0)");
  expect(withAlpha("#1e66f5", 5)).toBe("#1e66f5");
  expect(withAlpha("#1e66f5", -1)).toBe("rgba(30, 102, 245, 0)");
  // A blurred-empty alpha box must not destroy the color.
  expect(withAlpha("#1e66f5", NaN)).toBe("#1e66f5");
  expect(withAlpha("transparent", 0.5)).toBe("transparent");
});
