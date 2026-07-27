import { expect, test } from "bun:test";
import { reorderFieldsInElement } from "../src/dash/docOps";
import { loadDoc, useDocStore } from "../src/dash/store";
import type { BuilderFieldRef, DashElement } from "../src/dash/types";

const field = (name: string): BuilderFieldRef => ({ table: "analytics.Sales", name, kind: "column" });
const element = (id: string, type: "table" | "pivot", fields: BuilderFieldRef[]): DashElement => ({
  id,
  type,
  layout: { x: 0, y: 0, w: 100, h: 100 },
  query: { mode: "builder", fields },
});

test("reorders table columns and pivot rows", () => {
  const tableFields = [field("Region"), field("Venue"), field("Sales")];
  const pivotFields = [field("Region"), field("Year"), field("Venue"), field("Sales")];
  const table = element("table", "table", tableFields);
  const pivot = { ...element("pivot", "pivot", pivotFields), viz: { cols: ["Year"] } };
  loadDoc({ version: 1, canvas: { width: 100, height: 100 }, elements: [table, pivot] });

  reorderFieldsInElement("table", tableFields, 0, 2);
  reorderFieldsInElement("pivot", [pivotFields[0], pivotFields[2]], 0, 1);

  const [updatedTable, updatedPivot] = useDocStore.getState().doc!.elements;
  expect(updatedTable.query!.fields!.map((f) => f.name)).toEqual(["Venue", "Sales", "Region"]);
  expect(updatedPivot.query!.fields!.map((f) => f.name)).toEqual(["Venue", "Year", "Region", "Sales"]);
});
