import { afterEach, expect, test } from "bun:test";
import { save } from "../src/dash/actions";
import { loadDoc, useUiStore } from "../src/dash/store";
import type { Dashboard } from "../src/dash/types";

const originalFetch = globalThis.fetch;
afterEach(() => { globalThis.fetch = originalFetch; });

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
