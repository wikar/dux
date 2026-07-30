import { describe, expect, test } from "bun:test";
import type { Schema } from "@dux/core";
import { computeRelationshipLayout, countRelationshipCrossings } from "../src/components/Explorer";

function crossings(
  layout: Record<string, { x: number; y: number }>,
  edges: [string, string][],
  heights: Record<string, number> = {},
): number {
  const turn = (a: string, b: string, c: string) => {
    const point = (name: string) => ({ x: layout[name].x + 120, y: layout[name].y + (heights[name] ?? 0) / 2 });
    const p = point(a), q = point(b), r = point(c);
    return (q.x - p.x) * (r.y - p.y) - (q.y - p.y) * (r.x - p.x);
  };
  let n = 0;
  for (let i = 0; i < edges.length; i++) for (let j = i + 1; j < edges.length; j++) {
    const [a, b] = edges[i], [c, d] = edges[j];
    if (a === c || a === d || b === c || b === d) continue;
    if (turn(a, b, c) * turn(a, b, d) < 0 && turn(c, d, a) * turn(c, d, b) < 0) n++;
  }
  return n;
}

describe("Explorer relationship layout", () => {
  test("removes crossings from the alphabetical grid when a zero-crossing order exists", () => {
    const names = ["A", "B", "C", "D", "E", "F"];
    const edges: [string, string][] = [["A", "F"], ["C", "D"]];
    const schema = {
      Tables: Object.fromEntries(names.map((Name) => [Name, { Name, Columns: {} }])),
      Relationships: edges.map(([FromTable, ToTable]) => ({
        FromTable, FromColumn: "id", ToTable, ToColumn: "id", Bidirectional: false,
      })),
    } as Schema;

    expect(crossings(computeRelationshipLayout(schema), edges)).toBe(0);
  });

  test("lays out the Sales, Stock, and Club model without crossings", () => {
    const names = ["Club", "ClubMembership", "Customer", "Date", "Product", "Region", "Sales", "Stock", "Venue"];
    const visibleColumns: Record<string, number> = {
      Club: 2, ClubMembership: 0, Customer: 2, Date: 17, Product: 2,
      Region: 1, Sales: 0, Stock: 0, Venue: 4,
    };
    const edges: [string, string][] = [
      ["ClubMembership", "Club"], ["ClubMembership", "Customer"], ["Sales", "Customer"],
      ["Sales", "Date"], ["Sales", "Product"], ["Sales", "Region"], ["Sales", "Venue"],
      ["Stock", "Date"], ["Stock", "Product"], ["Stock", "Region"], ["Stock", "Venue"],
    ];
    const schema = {
      Tables: Object.fromEntries(names.map((Name) => [Name, {
        Name,
        Columns: Object.fromEntries(Array.from({ length: visibleColumns[Name] }, (_, i) => [String(i), { Name: String(i) }])),
      }])),
      Relationships: edges.map(([FromTable, ToTable]) => ({
        FromTable, FromColumn: "id", ToTable, ToColumn: "id", Bidirectional: false,
      })),
    } as Schema;

    const layout = computeRelationshipLayout(schema);
    expect(countRelationshipCrossings(schema, layout)).toBe(0);
  });
});
