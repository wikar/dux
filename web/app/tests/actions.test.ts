import { afterEach, expect, test } from "bun:test";
import { applySlicerSelection, save, seedSlicerSelections } from "../src/dash/actions";
import { loadDoc, useUiStore } from "../src/dash/store";
import type { Dashboard } from "../src/dash/types";

const originalFetch = globalThis.fetch;
const originalWindow = globalThis.window;
afterEach(() => {
  globalThis.fetch = originalFetch;
  Object.defineProperty(globalThis, "window", { configurable: true, value: originalWindow });
});

const dashboard = (width: number): Dashboard => ({
  version: 1,
  canvas: { width, height: 100 },
  elements: [],
});

test("a late save response cannot mark another dashboard saved", async () => {
  let finish!: (response: Response) => void;
  globalThis.fetch = () => new Promise<Response>((resolve) => { finish = resolve; });

  const first = dashboard(100);
  loadDoc(first);
  useUiStore.getState().setPath("first");
  useUiStore.getState().opened("first", "etag-first", JSON.stringify(first));
  useUiStore.getState().setSaving(false);

  const pending = save();
  await Promise.resolve();

  const second = dashboard(200);
  loadDoc(second);
  useUiStore.getState().setPath("second");
  useUiStore.getState().opened("second", "etag-second", JSON.stringify(second));
  finish(new Response(JSON.stringify({ etag: "etag-late", created: false }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  }));
  await pending;

  expect(useUiStore.getState().etag).toBe("etag-second");
  expect(useUiStore.getState().savedJson).toBe(JSON.stringify(second));
});

test("clearing a preset slicer in view mode stays All after URL reseeding", () => {
  let url = new URL("http://dux.test/dash/report");
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: {
      get location() { return url; },
      history: { replaceState: (_: unknown, __: string, to: string) => { url = new URL(to, url); } },
    },
  });
  const doc: Dashboard = {
    version: 1,
    canvas: { width: 100, height: 100 },
    elements: [{
      id: "year",
      type: "slicer",
      layout: { x: 0, y: 0, w: 100, h: 100 },
      slicer: { table: "Date", column: "Year", kind: "buttons", default: { kind: "values", values: ["2025"] } },
    }],
  };
  loadDoc(doc);
  useUiStore.getState().setMode("view");

  seedSlicerSelections(doc);
  applySlicerSelection("year", null);
  seedSlicerSelections(doc); // DashApp reacts to the updated query string.

  expect(useUiStore.getState().slicerSelections.year).toBeUndefined();
  expect(JSON.parse(url.searchParams.get("f")!)).toEqual({ year: [] });
});
