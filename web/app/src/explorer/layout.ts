import type { Schema } from "@dux/core";
import { isMetaTable, resolveTable } from "@dux/core";

// ─── Layout constants ────────────────────────────────────────────────────────
export const CARD_WIDTH = 240;
export const GRID_COLS = 3;
export const GRID_COL_STEP = CARD_WIDTH + 80;
// Rows clear a full-height card (its columns area caps out around 356) with room
// to spare, so a line between vertically adjacent cards is not a stub.
export const GRID_ROW_STEP = 430;
const CARD_BASE_HEIGHT = 44;
const CARD_ROW_HEIGHT = 19;
const CARD_MAX_HEIGHT = 356;

export type Pos = { x: number; y: number };
export type Rect = { left: number; top: number; right: number; bottom: number; cx: number; cy: number };
export type Side = "left" | "right" | "top" | "bottom";
type EndPoint = { table: string; side: Side; rect: Rect };
export type Curve = { p1: Pos; c1: Pos; c2: Pos; p2: Pos };

const NORMAL: Record<Side, Pos> = {
  left: { x: -1, y: 0 },
  right: { x: 1, y: 0 },
  top: { x: 0, y: -1 },
  bottom: { x: 0, y: 1 },
};

type LayoutEdge = readonly [string, string];
type LayoutScore = readonly [crossings: number, length: number];

function sideToward(rect: Rect, other: Rect): Side {
  const w = rect.right - rect.left;
  const h = rect.bottom - rect.top;
  const dx = other.cx - rect.cx;
  const dy = other.cy - rect.cy;
  return Math.abs(dx) * h > Math.abs(dy) * w
    ? dx > 0 ? "right" : "left"
    : dy > 0 ? "bottom" : "top";
}

function gridPoint(i: number, height: number): Pos {
  return {
    x: (i % GRID_COLS) * GRID_COL_STEP + CARD_WIDTH / 2,
    y: Math.floor(i / GRID_COLS) * GRID_ROW_STEP + height / 2,
  };
}

function segmentsIntersect(a: Pos, b: Pos, c: Pos, d: Pos): boolean {
  const turn = (p: Pos, q: Pos, r: Pos) => (q.x - p.x) * (r.y - p.y) - (q.y - p.y) * (r.x - p.x);
  const abC = turn(a, b, c), abD = turn(a, b, d), cdA = turn(c, d, a), cdB = turn(c, d, b);
  return abC * abD < 0 && cdA * cdB < 0;
}

export function routeEdges(edges: LayoutEdge[], rects: Map<string, Rect>): Curve[] {
  const ends = edges.map(([a, b]) => {
    const ar = rects.get(a)!, br = rects.get(b)!;
    return [
      { table: a, rect: ar, side: sideToward(ar, br) },
      { table: b, rect: br, side: sideToward(br, ar) },
    ] as const;
  });
  const totals = new Map<string, number>();
  for (const pair of ends) for (const end of pair) {
    const key = `${end.table}\0${end.side}`;
    totals.set(key, (totals.get(key) ?? 0) + 1);
  }
  const seen = new Map<string, number>();
  const place = (end: EndPoint): Pos => {
    const key = `${end.table}\0${end.side}`;
    const i = seen.get(key) ?? 0;
    seen.set(key, i + 1);
    const f = (i + 1) / ((totals.get(key) ?? 1) + 1);
    const { rect, side } = end;
    if (side === "left" || side === "right") {
      return { x: side === "left" ? rect.left : rect.right, y: rect.top + (rect.bottom - rect.top) * f };
    }
    return { x: rect.left + (rect.right - rect.left) * f, y: side === "top" ? rect.top : rect.bottom };
  };

  return ends.map(([from, to]) => {
    const p1 = place(from), p2 = place(to);
    const dx = p2.x - p1.x, dy = p2.y - p1.y;
    const n1 = NORMAL[from.side], n2 = NORMAL[to.side];
    const floor = Math.min(60, Math.hypot(dx, dy) / 2);
    const off1 = Math.max(floor, Math.abs(n1.x * dx + n1.y * dy) / 2);
    const off2 = Math.max(floor, Math.abs(n2.x * -dx + n2.y * -dy) / 2);
    return {
      p1,
      c1: { x: p1.x + n1.x * off1, y: p1.y + n1.y * off1 },
      c2: { x: p2.x + n2.x * off2, y: p2.y + n2.y * off2 },
      p2,
    };
  });
}

function curvePoint(curve: Curve, t: number): Pos {
  const u = 1 - t;
  return {
    x: u ** 3 * curve.p1.x + 3 * u ** 2 * t * curve.c1.x + 3 * u * t ** 2 * curve.c2.x + t ** 3 * curve.p2.x,
    y: u ** 3 * curve.p1.y + 3 * u ** 2 * t * curve.c1.y + 3 * u * t ** 2 * curve.c2.y + t ** 3 * curve.p2.y,
  };
}

function curvesIntersect(a: Curve, b: Curve): boolean {
  let a1 = a.p1, b1 = b.p1;
  for (let i = 1; i <= 12; i++) {
    const a2 = curvePoint(a, i / 12);
    let c1 = b1;
    for (let j = 1; j <= 12; j++) {
      const c2 = curvePoint(b, j / 12);
      if (segmentsIntersect(a1, a2, c1, c2)) return true;
      c1 = c2;
    }
    a1 = a2;
  }
  return false;
}

function layoutScore(order: string[], edges: LayoutEdge[], heights: Map<string, number>): LayoutScore {
  const points = new Map(order.map((name, i) => [name, gridPoint(i, heights.get(name)!)]));
  const rects = new Map(order.map((name, i) => {
    const p = gridPoint(i, heights.get(name)!);
    const height = heights.get(name)!;
    return [name, { left: p.x - CARD_WIDTH / 2, top: p.y - height / 2, right: p.x + CARD_WIDTH / 2, bottom: p.y + height / 2, cx: p.x, cy: p.y }];
  }));
  const curves = routeEdges(edges, rects);
  let crossings = 0;
  let length = 0;
  for (let i = 0; i < edges.length; i++) {
    const [a, b] = edges[i];
    const pa = points.get(a)!, pb = points.get(b)!;
    length += (pb.x - pa.x) ** 2 + (pb.y - pa.y) ** 2;
    for (let j = i + 1; j < edges.length; j++) {
      if (curvesIntersect(curves[i], curves[j])) crossings++;
    }
  }
  return [crossings, length];
}

function better(a: LayoutScore, b: LayoutScore): boolean {
  return a[0] < b[0] || (a[0] === b[0] && a[1] < b[1]);
}

/** Deterministic move search: crossings first, total relationship length
 *  second. Crossing minimisation is NP-hard; this cheap search is plenty for a
 *  semantic model and leaves the cards draggable when a human can do better. */
function improveOrder(seed: string[], edges: LayoutEdge[], heights: Map<string, number>, budget: { remaining: number }): string[] {
  let order = seed.slice();
  let score = layoutScore(order, edges, heights);
  for (;;) {
    let bestOrder = order;
    let bestScore = score;
    for (let from = 0; from < order.length && budget.remaining > 0; from++) {
      for (let to = 0; to < order.length && budget.remaining > 0; to++) {
        if (from === to) continue;
        const candidate = order.slice();
        const [name] = candidate.splice(from, 1);
        candidate.splice(to, 0, name);
        budget.remaining--;
        const candidateScore = layoutScore(candidate, edges, heights);
        if (better(candidateScore, bestScore)) {
          bestOrder = candidate;
          bestScore = candidateScore;
        }
      }
    }
    if (bestOrder === order || budget.remaining === 0) return bestOrder;
    order = bestOrder;
    score = bestScore;
  }
}

function layoutGraph(s: Schema, showHidden: boolean) {
  const tnames = Object.keys(s.Tables).filter((n) => !isMetaTable(n)).sort();
  const names = new Set(tnames);
  const heights = new Map(tnames.map((name) => {
    const columnCount = Object.values(s.Tables[name].Columns).filter((column) => showHidden || !column.Hidden).length;
    return [name, Math.min(CARD_BASE_HEIGHT + columnCount * CARD_ROW_HEIGHT, CARD_MAX_HEIGHT)];
  }));
  const edgeKeys = new Set<string>();
  const edges: LayoutEdge[] = [];
  for (const rel of s.Relationships ?? []) {
    const a = resolveTable(rel.FromTable, tnames);
    const b = resolveTable(rel.ToTable, tnames);
    if (a === b || !names.has(a) || !names.has(b)) continue;
    const edge = a < b ? [a, b] as const : [b, a] as const;
    const key = `${edge[0]}\0${edge[1]}`;
    if (edgeKeys.has(key)) continue;
    edgeKeys.add(key);
    edges.push(edge);
  }
  return { tnames, heights, edges };
}

export function countRelationshipCrossings(s: Schema, positions: Record<string, Pos>, showHidden = false): number {
  const { heights, edges } = layoutGraph(s, showHidden);
  const rects = new Map(Object.entries(positions).map(([name, p]) => {
    const height = heights.get(name)!;
    return [name, { left: p.x, top: p.y, right: p.x + CARD_WIDTH, bottom: p.y + height, cx: p.x + CARD_WIDTH / 2, cy: p.y + height / 2 }];
  }));
  const curves = routeEdges(edges, rects);
  let crossings = 0;
  for (let i = 0; i < edges.length; i++) for (let j = i + 1; j < edges.length; j++) {
    if (curvesIntersect(curves[i], curves[j])) crossings++;
  }
  return crossings;
}

/** Seed layout: relationship-aware ordering in the existing fixed grid.
 *  Positions are the starting point only — cards stay user-draggable. */
export function computeRelationshipLayout(s: Schema, showHidden = false): Record<string, Pos> {
  const { tnames, heights, edges } = layoutGraph(s, showHidden);

  let order = tnames;
  if (edges.length > 0) {
    const degree = new Map(tnames.map((name) => [name, 0]));
    for (const [a, b] of edges) {
      degree.set(a, degree.get(a)! + 1);
      degree.set(b, degree.get(b)! + 1);
    }
    const byDegree = tnames.slice().sort((a, b) => degree.get(b)! - degree.get(a)! || a.localeCompare(b));
    const seeds = [tnames, tnames.slice().reverse(), byDegree, byDegree.slice().reverse()];
    const perSeedBudget = Math.floor(768 / seeds.length);
    const candidates = seeds.map((seed) =>
      improveOrder(seed, edges, heights, { remaining: perSeedBudget })
    );
    order = candidates.reduce((best, candidate) =>
      better(layoutScore(candidate, edges, heights), layoutScore(best, edges, heights)) ? candidate : best);
  }

  const result: Record<string, Pos> = {};
  order.forEach((name, i) => {
    result[name] = {
      x: 24 + (i % GRID_COLS) * GRID_COL_STEP,
      y: 24 + Math.floor(i / GRID_COLS) * GRID_ROW_STEP,
    };
  });
  return result;
}

