// Pure dashboard-document operations, applied through useDocStore.update so
// each call is exactly one undo step.
import { isNumeric } from "@dux/core";
import type { Aggregate, DragPayload } from "@dux/core";
import { applySlicerSelection } from "./actions";
import { useDocStore, useUiStore } from "./store";
import type { BuilderFieldRef, Dashboard, DashElement, ElementType, Layout } from "./types";
import { QUERY_TYPES } from "./types";

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
  combo: { w: 420, h: 260 },
  area: { w: 360, h: 240 },
  donut: { w: 280, h: 240 },
  table: { w: 420, h: 280 },
  pivot: { w: 420, h: 280 },
  kpi: { w: 200, h: 112 },
  slicer: { w: 200, h: 240 },
  text: { w: 280, h: 160 },
  image: { w: 200, h: 120 },
};

export const TYPE_LABEL: Record<ElementType, string> = {
  bar: "Bar chart",
  line: "Line chart",
  combo: "Combo chart",
  area: "Area chart",
  donut: "Donut chart",
  table: "Table",
  pivot: "Pivot",
  kpi: "KPI card",
  slicer: "Slicer",
  text: "Text",
  image: "Image",
};

/** Compact palette-button labels ("chart"/"card" dropped for space). */
export const SHORT_LABEL: Record<ElementType, string> = {
  bar: "Bar",
  line: "Line",
  combo: "Combo",
  area: "Area",
  donut: "Donut",
  table: "Table",
  pivot: "Pivot",
  kpi: "KPI",
  slicer: "Slicer",
  text: "Text",
  image: "Image",
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
    } else if (type === "image") {
      el.image = { fit: "contain" };
    } else if (type === "slicer") {
      el.title = { text: TYPE_LABEL[type], show: true };
      el.slicer = { table: "", column: "", kind: "buttons", multi: true };
    } else {
      el.title = { text: TYPE_LABEL[type], show: true };
      if (QUERY_TYPES.has(type)) el.query = { mode: "builder", fields: [] };
    }
    return { ...doc, elements: [...doc.elements, el] };
  });
  if (newId) useUiStore.getState().select(newId);
}

/** Swap a query element to another query type, remapping well memberships
 *  instead of resetting them. Non-destructive: viz keys the new type ignores
 *  stay in the document so swapping back restores the old look. */
export function swapElementType(id: string, type: ElementType) {
  updateElement(id, (el) => {
    if (el.type === type || !QUERY_TYPES.has(el.type) || !QUERY_TYPES.has(type)) return el;
    const next: DashElement = { ...el, type };
    // A default title follows the type; a custom one is kept.
    if ((el.title?.text ?? "") === TYPE_LABEL[el.type]) {
      next.title = { ...el.title, text: TYPE_LABEL[type] };
    }
    // The right-axis series of a line map to the combo's lines and back.
    const viz = { ...el.viz };
    if (type === "combo" && el.type === "line" && !viz.lines?.length && viz.y2?.length) {
      viz.lines = viz.y2;
    }
    if (type === "line" && el.type === "combo" && !viz.y2?.length && viz.lines?.length) {
      viz.y2 = viz.lines;
    }
    next.viz = viz;
    return next;
  });
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
  // A removed slicer's selection must stop filtering (and leave the URL).
  if (ui.slicerSelections[id]) applySlicerSelection(id, null);
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

// ─── Field wells ─────────────────────────────────────────────────────────────
//
// The element's query.fields is one flat ordered list (dims before metrics —
// the DUX query derives from it alone). Wells are views over it: metric
// membership in the line/combo secondary wells lives in viz.y2 / viz.lines.

export type WellId =
  | "axis" | "values" | "y2" | "bars" | "lines" | "fields" | "rows" | "cols" | "series";

/** True when the field produces a metric output column. */
export function isMetricRef(f: BuilderFieldRef): boolean {
  return f.kind === "measure" || (isNumeric(f.dataType ?? "") && f.aggregate !== "VALUES");
}

/** Insert a dim before the first metric so dims lead the column order. */
function insertDim(fields: BuilderFieldRef[], field: BuilderFieldRef) {
  const at = fields.findIndex(isMetricRef);
  if (at === -1) fields.push(field);
  else fields.splice(at, 0, field);
}

/** Pure add: drop a schema field into a well. Incompatible drops are ignored:
 *  measures can't be dims, non-numeric columns can't be metrics. */
function addFieldPure(el: DashElement, well: WellId, p: DragPayload): DashElement {
  const q = el.query ?? { mode: "builder" as const };
  const fields = [...(q.fields ?? [])];
  if (fields.some((f) => f.table === p.table && f.name === p.name)) return el;
  const numeric = isNumeric(p.dataType);

  const field: BuilderFieldRef = { table: p.table, name: p.name, kind: p.kind, dataType: p.dataType };
  if (well === "axis" || well === "rows" || well === "cols" || well === "series") {
    if (p.kind === "measure") return el;
    if (numeric) field.aggregate = "VALUES"; // numeric column as a dim
    insertDim(fields, field);
  } else if (well === "fields") {
    if (p.kind === "column" && numeric) field.aggregate = "SUM";
    if (isMetricRef(field)) fields.push(field);
    else insertDim(fields, field);
  } else {
    if (p.kind !== "measure" && !numeric) return el;
    if (p.kind === "column") field.aggregate = "SUM";
    fields.push(field);
  }

  const viz = { ...el.viz };
  if (well === "y2") viz.y2 = [...(viz.y2 ?? []), p.name];
  if (well === "lines") viz.lines = [...(viz.lines ?? []), p.name];
  if (well === "cols") viz.cols = [...(viz.cols ?? []), p.name];
  if (well === "series") viz.series = p.name;
  return { ...el, query: { ...q, fields }, viz };
}

/** Pure remove: a field leaves the list, well memberships, and sort keys. */
function removeFieldPure(el: DashElement, name: string): DashElement {
  const q = el.query ?? { mode: "builder" as const };
  const viz = { ...el.viz };
  if (viz.y2) viz.y2 = viz.y2.filter((n) => n !== name);
  if (viz.lines) viz.lines = viz.lines.filter((n) => n !== name);
  if (viz.cols) viz.cols = viz.cols.filter((n) => n !== name);
  if (viz.series === name) delete viz.series;
  return {
    ...el,
    query: {
      ...q,
      fields: (q.fields ?? []).filter((f) => f.name !== name),
      sort: (q.sort ?? []).filter((s) => s.field !== name),
    },
    viz,
  };
}

export function addFieldToWell(id: string, well: WellId, p: DragPayload) {
  updateElement(id, (el) => addFieldPure(el, well, p));
}

/** Drop into a single-slot well: the current member(s) are replaced by the
 *  new field in one undo step. */
export function replaceFieldInWell(id: string, well: WellId, names: string[], p: DragPayload) {
  updateElement(id, (el) => addFieldPure(names.reduce(removeFieldPure, el), well, p));
}

export function removeFieldFromElement(id: string, name: string) {
  updateElement(id, (el) => removeFieldPure(el, name));
}

export function setFieldAggregate(id: string, name: string, aggregate: Aggregate) {
  updateElement(id, (el) => {
    const q = el.query ?? { mode: "builder" as const };
    const viz = { ...el.viz };
    if (aggregate === "VALUES") {
      // Became a dim — drop any metric-well membership.
      if (viz.y2) viz.y2 = viz.y2.filter((n) => n !== name);
      if (viz.lines) viz.lines = viz.lines.filter((n) => n !== name);
    } else {
      // Became a metric — dim-well memberships can't stay.
      if (viz.cols) viz.cols = viz.cols.filter((n) => n !== name);
      if (viz.series === name) delete viz.series;
    }
    return {
      ...el,
      query: {
        ...q,
        fields: (q.fields ?? []).map((f) => (f.name === name ? { ...f, aggregate } : f)),
      },
      viz,
    };
  });
}

export function addFilterToElement(id: string, p: DragPayload) {
  updateElement(id, (el) => {
    const q = el.query ?? { mode: "builder" as const };
    const filters = q.filters ?? [];
    if (filters.some((f) => f.table === p.table && f.name === p.name)) return el;
    return {
      ...el,
      query: {
        ...q,
        filters: [...filters, { table: p.table, name: p.name, dataType: p.dataType, op: "=", value: "" }],
      },
    };
  });
}

export function updateFilter(id: string, index: number, patch: { op?: string; value?: string }) {
  updateElement(id, (el) => {
    const q = el.query ?? { mode: "builder" as const };
    return {
      ...el,
      query: {
        ...q,
        filters: (q.filters ?? []).map((f, i) => (i === index ? { ...f, ...patch } : f)),
      },
    };
  });
}

export function removeFilter(id: string, index: number) {
  updateElement(id, (el) => {
    const q = el.query ?? { mode: "builder" as const };
    return { ...el, query: { ...q, filters: (q.filters ?? []).filter((_, i) => i !== index) } };
  });
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
