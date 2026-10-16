import { describe, expect, test } from "bun:test";
import type { DragPayload } from "@dux/core";
import { homeQueryReducer, initialHomeQueryState } from "../src/home/homeQueryState";

const column = (name: string, dataType = "TEXT"): DragPayload => ({
  table: "Sales", name, kind: "column", dataType,
});

describe("Home query state", () => {
  test("builder events commit generated queries and keep dimensions before metrics", () => {
    let state = homeQueryReducer(initialHomeQueryState, { type: "add-field", payload: column("Amount", "DOUBLE") });
    expect(state.fields.map((field) => field.name)).toEqual(["Amount"]);
    expect(state.committedQuery).toContain("SUM(Sales[Amount])");
    const firstRun = state.runId;

    state = homeQueryReducer(state, { type: "add-field", payload: column("Region") });
    expect(state.fields.map((field) => field.name)).toEqual(["Region", "Amount"]);
    expect(state.committedQuery).toContain("Sales[Region]");
    expect(state.isDirty).toBe(false);
    expect(state.runId).toBe(firstRun + 1);
  });

  test("manual drafts stay separate until Run and identical text can run again", () => {
    const draft = "EVALUATE Sales";
    let state = homeQueryReducer(initialHomeQueryState, { type: "edit-query", value: draft });
    expect(state.isDirty).toBe(true);
    expect(state.committedQuery).toBe("");

    state = homeQueryReducer(state, { type: "run-query" });
    const firstRun = state.runId;
    expect(state.committedQuery).toBe(draft);
    expect(state.isDirty).toBe(false);
    state = homeQueryReducer(state, { type: "run-query" });
    expect(state.runId).toBe(firstRun + 1);
    expect(state.committedQuery).toBe(draft);
  });

  test("field reorder uses source then target and rejects an invalid source", () => {
    let state = initialHomeQueryState;
    for (const name of ["A", "B", "C"]) state = homeQueryReducer(state, { type: "add-field", payload: column(name) });
    state = homeQueryReducer(state, { type: "reorder-fields", from: 0, to: 2 });
    expect(state.fields.map((field) => field.name)).toEqual(["B", "C", "A"]);
    expect(homeQueryReducer(state, { type: "reorder-fields", from: 9, to: 0 })).toBe(state);
  });
});
