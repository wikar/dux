import { afterEach, expect, test } from "bun:test";
import { DuxClient, QueryFailedError, ServerBusyError } from "./client";

const realFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = realFetch;
});

type Reply = { status: number; body: string; retryAfter?: string };

/** Installs a fetch stub that walks `replies`, recording each request. */
function stubFetch(replies: Reply[]): { calls: RequestInit[] } {
  const calls: RequestInit[] = [];
  let i = 0;
  globalThis.fetch = ((_url: string, init: RequestInit) => {
    calls.push(init);
    const reply = replies[Math.min(i++, replies.length - 1)];
    const headers = new Headers();
    if (reply.retryAfter) headers.set("Retry-After", reply.retryAfter);
    return Promise.resolve(new Response(reply.body, { status: reply.status, headers }));
  }) as typeof fetch;
  return { calls };
}

const OK: Reply = { status: 200, body: JSON.stringify({ columns: ["a"], rows: [[1]] }) };
const BUSY: Reply = { status: 503, body: JSON.stringify({ error: "server busy" }) };

test("retries a shed query and returns the eventual result", async () => {
  const { calls } = stubFetch([BUSY, BUSY, OK]);
  const res = await new DuxClient().executeQuery("EVALUATE sales");
  expect(res.rows).toEqual([[1]]);
  expect(calls.length).toBe(3);
});

test("gives up as ServerBusyError once retries are exhausted", async () => {
  const { calls } = stubFetch([BUSY]);
  const client = new DuxClient();
  await expect(client.executeQuery("EVALUATE sales")).rejects.toBeInstanceOf(ServerBusyError);
  // The initial attempt plus BUSY_RETRIES, and no more.
  expect(calls.length).toBe(3);
});

test("does not retry a query error — only load shedding is replayable", async () => {
  const { calls } = stubFetch([
    { status: 400, body: JSON.stringify({ error: "unknown column", stage: "resolve", line: 1, column: 9 }) },
  ]);
  await expect(new DuxClient().executeQuery("EVALUATE nope")).rejects.toBeInstanceOf(QueryFailedError);
  expect(calls.length).toBe(1);
});

test("retries filtered queries too, preserving the JSON envelope", async () => {
  const { calls } = stubFetch([BUSY, OK]);
  const res = await new DuxClient().executeQueryFiltered("EVALUATE sales", [
    { table: "Date", column: "Year", op: "=", value: 2025 },
  ]);
  expect(res.rows).toEqual([[1]]);
  expect(calls.length).toBe(2);
  for (const call of calls) {
    expect(JSON.parse(call.body as string).filters).toHaveLength(1);
  }
});

test("waits at least Retry-After before retrying", async () => {
  stubFetch([{ ...BUSY, retryAfter: "1" }, OK]);
  const start = performance.now();
  await new DuxClient().executeQuery("EVALUATE sales");
  expect(performance.now() - start).toBeGreaterThanOrEqual(1000);
});

test("an aborted signal stops the retry loop instead of sleeping it out", async () => {
  stubFetch([{ ...BUSY, retryAfter: "30" }]);
  const controller = new AbortController();
  const pending = new DuxClient().executeQuery("EVALUATE sales", { signal: controller.signal });
  controller.abort(new Error("navigated away"));
  await expect(pending).rejects.toThrow("navigated away");
});
