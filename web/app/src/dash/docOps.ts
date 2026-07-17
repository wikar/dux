// Pure dashboard-document operations, applied through useDocStore.update so
// each call is exactly one undo step.
import { useDocStore, useUiStore } from "./store";
import type { Dashboard, DashElement, ElementType, Layout } from "./types";

export const SNAP = 8;
export const MIN_W = 40;
export const MIN_H = 24;

export function snap(v: number): number {
  return Math.round(v / SNAP) * SNAP;
}

export function clamp(v: number, lo: number, hi: number): number {
  return Math.min(Math.max(v, lo), Math.max(lo, hi));
}

const DEFAULT_SIZE: Record<ElementType, { w: number; h: number }> = {
  bar: { w: 360, h: 240 },
  line: { w: 360, h: 240 },
  area: { w: 360, h: 240 },
  donut: { w: 280, h: 240 },
  table: { w: 420, h: 280 },
  pivot: { w: 420, h: 280 },
  kpi: { w: 200, h: 112 },
  slicer: { w: 200, h: 240 },
  text: { w: 280, h: 160 },
};

export const TYPE_LABEL: Record<ElementType, string> = {
  bar: "Bar chart",
  line: "Line chart",
  area: "Area chart",
  donut: "Donut chart",
  table: "Table",
  pivot: "Pivot",
  kpi: "KPI card",
  slicer: "Slicer",
  text: "Text",
};

/** First free "type-N" id in the document. */
function nextId(doc: Dashboard, type: ElementType): string {
  const taken = new Set(doc.elements.map((e) => e.id));
  for (let n = 1; ; n++) {
    const id = `${type}-${n}`;
    if (!taken.has(id)) return id;
  }
}

function maxZ(doc: Dashboard): number {
  return doc.elements.reduce((m, e) => Math.max(m, e.layout.z ?? 0), 0);
}

function minZ(doc: Dashboard): number {
  return doc.elements.reduce((m, e) => Math.min(m, e.layout.z ?? 0), 0);
}

/** Add a new element of the given type near the canvas center; selects it. */
export function addElement(type: ElementType) {
  let newId = "";
  useDocStore.getState().update((doc) => {
    const size = DEFAULT_SIZE[type];
    const count = doc.elements.length;
    // Cascade placement so stacked inserts stay visible.
    const x = clamp(snap((doc.canvas.width - size.w) / 2 + (count % 5) * 24), 0, doc.canvas.width - size.w);
    const y = clamp(snap((doc.canvas.height - size.h) / 2 + (count % 5) * 24), 0, doc.canvas.height - size.h);
    newId = nextId(doc, type);
    const el: DashElement = {
      id: newId,
      type,
      layout: { x, y, w: size.w, h: size.h, z: maxZ(doc) + 1 },
    };
    if (type === "text") {
      el.text = { markdown: "## Text\n\nEdit the markdown in the settings pane." };
    } else {
      el.title = { text: TYPE_LABEL[type], show: true };
    }
    return { ...doc, elements: [...doc.elements, el] };
  });
  if (newId) useUiStore.getState().select(newId);
}

export function updateElement(id: string, fn: (el: DashElement) => DashElement) {
  useDocStore.getState().update((doc) => ({
    ...doc,
    elements: doc.elements.map((e) => (e.id === id ? fn(e) : e)),
  }));
}

export function removeElement(id: string) {
  useDocStore.getState().update((doc) => ({
    ...doc,
    elements: doc.elements.filter((e) => e.id !== id),
  }));
  const ui = useUiStore.getState();
  if (ui.selectedId === id) ui.select(null);
}

export function duplicateElement(id: string) {
  let newId = "";
  useDocStore.getState().update((doc) => {
    const src = doc.elements.find((e) => e.id === id);
    if (!src) return doc;
    newId = nextId(doc, src.type);
    const copy: DashElement = structuredClone(src);
    copy.id = newId;
    copy.layout = {
      ...copy.layout,
      x: clamp(copy.layout.x + 2 * SNAP, 0, doc.canvas.width - copy.layout.w),
      y: clamp(copy.layout.y + 2 * SNAP, 0, doc.canvas.height - copy.layout.h),
      z: maxZ(doc) + 1,
    };
    return { ...doc, elements: [...doc.elements, copy] };
  });
  if (newId) useUiStore.getState().select(newId);
}

export function bringToFront(id: string) {
  useDocStore.getState().update((doc) => {
    const z = maxZ(doc) + 1;
    return {
      ...doc,
      elements: doc.elements.map((e) => (e.id === id ? { ...e, layout: { ...e.layout, z } } : e)),
    };
  });
}

export function sendToBack(id: string) {
  useDocStore.getState().update((doc) => {
    const z = minZ(doc) - 1;
    return {
      ...doc,
      elements: doc.elements.map((e) => (e.id === id ? { ...e, layout: { ...e.layout, z } } : e)),
    };
  });
}

/** Move the selected element by (dx, dy), clamped to the canvas. */
export function nudgeElement(id: string, dx: number, dy: number) {
  useDocStore.getState().update((doc) => ({
    ...doc,
    elements: doc.elements.map((e) => {
      if (e.id !== id) return e;
      const l = e.layout;
      return {
        ...e,
        layout: {
          ...l,
          x: clamp(l.x + dx, 0, doc.canvas.width - l.w),
          y: clamp(l.y + dy, 0, doc.canvas.height - l.h),
        },
      };
    }),
  }));
}

export function updateCanvas(fn: (doc: Dashboard) => Dashboard) {
  useDocStore.getState().update(fn);
}

// ─── Drag/resize gesture math ────────────────────────────────────────────────

export type GestureKind = "move" | "n" | "s" | "e" | "w" | "ne" | "nw" | "se" | "sw";

/** Compute the layout for a drag/resize gesture: canvas-space deltas are the
 *  screen deltas divided by the canvas scale; results snap to the grid and
 *  stay inside the canvas. */
export function applyGesture(
  kind: GestureKind,
  orig: Layout,
  dx: number,
  dy: number,
  canvas: { width: number; height: number }
): Layout {
  if (kind === "move") {
    return {
      ...orig,
      x: clamp(snap(orig.x + dx), 0, Math.max(0, canvas.width - orig.w)),
      y: clamp(snap(orig.y + dy), 0, Math.max(0, canvas.height - orig.h)),
    };
  }
  let { x, y, w, h } = orig;
  const right = orig.x + orig.w;
  const bottom = orig.y + orig.h;
  if (kind.includes("e")) w = clamp(snap(orig.w + dx), MIN_W, canvas.width - orig.x);
  if (kind.includes("s")) h = clamp(snap(orig.h + dy), MIN_H, canvas.height - orig.y);
  if (kind.includes("w")) {
    x = clamp(snap(orig.x + dx), 0, right - MIN_W);
    w = right - x;
  }
  if (kind.includes("n")) {
    y = clamp(snap(orig.y + dy), 0, bottom - MIN_H);
    h = bottom - y;
  }
  return { ...orig, x, y, w, h };
}
