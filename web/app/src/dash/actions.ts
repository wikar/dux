// Cross-component actions: navigation, load, save, and conflict resolution.
import { DashConflictError, encodePath, getDashboard, putDashboard } from "./api";
import { navigate } from "../router";
import { loadDoc, updateElement, useDocStore, useUiStore } from "./store";
import { newDashboard } from "./types";
import type { Dashboard, SlicerSelection } from "./types";

/** Navigate to a dashboard by identity (updates the /dash/<path> URL). */
export function gotoDashboard(path: string) {
  navigate(`/dash/${encodePath(path)}`);
}

// ─── Slicer selections ↔ ?f= deep link ───────────────────────────────────────

/** Parse the ?f= parameter: {elementId: ["v1","v2"] | {from,to}}. */
function selectionsFromUrl(): Record<string, SlicerSelection> {
  const raw = new URLSearchParams(window.location.search).get("f");
  if (!raw) return {};
  try {
    const obj = JSON.parse(raw) as Record<string, unknown>;
    const out: Record<string, SlicerSelection> = {};
    for (const [id, v] of Object.entries(obj)) {
      if (Array.isArray(v)) {
        const values = v.filter((x): x is string => typeof x === "string");
        if (values.length > 0) out[id] = { kind: "values", values };
      } else if (v && typeof v === "object") {
        const r = v as { from?: unknown; to?: unknown };
        const from = typeof r.from === "string" ? r.from : undefined;
        const to = typeof r.to === "string" ? r.to : undefined;
        if (from !== undefined || to !== undefined) out[id] = { kind: "range", from, to };
      }
    }
    return out;
  } catch {
    return {};
  }
}

/** Write the current selections into ?f= (replaceState — no history spam). */
function selectionsToUrl(selections: Record<string, SlicerSelection>) {
  const obj: Record<string, unknown> = {};
  for (const [id, sel] of Object.entries(selections)) {
    if (sel.kind === "values" && sel.values.length > 0) obj[id] = sel.values;
    else if (sel.kind === "range" && (sel.from !== undefined || sel.to !== undefined)) {
      obj[id] = { from: sel.from, to: sel.to };
    }
  }
  const url = new URL(window.location.href);
  if (Object.keys(obj).length > 0) url.searchParams.set("f", JSON.stringify(obj));
  else url.searchParams.delete("f");
  window.history.replaceState({}, "", url);
}

/** Set (or clear) one slicer's selection: session state plus the deep link. */
function syncSlicerSelection(id: string, sel: SlicerSelection | null) {
  const ui = useUiStore.getState();
  ui.setSlicerSelection(id, sel);
  selectionsToUrl(useUiStore.getState().slicerSelections);
}

/** A user changing a slicer. In edit mode the selection is also the element's
 *  preset (slicer.default), so what the author leaves selected is what the
 *  dashboard opens with — clearing it removes the preset. In view mode the
 *  selection stays session state and the document is untouched. */
export function applySlicerSelection(id: string, sel: SlicerSelection | null) {
  const ui = useUiStore.getState();
  // An explicit choice supersedes the seeded preset — never prune it after.
  if (ui.presetPending[id]) ui.resolveSlicerPreset(id, []);
  syncSlicerSelection(id, sel);
  if (ui.mode === "edit") setSlicerPreset(id, sel);
}

// ─── Slicer presets (slicer.default) ─────────────────────────────────────────

/** Write (or remove) one slicer's preset in the document — one undo step, and
 *  it makes the dashboard dirty like any other authoring change. */
function setSlicerPreset(id: string, sel: SlicerSelection | null) {
  updateElement(id, (el) => {
    if (!el.slicer) return el;
    const { default: prev, ...rest } = el.slicer;
    if (!sel) return prev ? { ...el, slicer: rest } : el;
    if (JSON.stringify(prev) === JSON.stringify(sel)) return el;
    return { ...el, slicer: { ...rest, default: sel } };
  });
}

/** Drop seeded values the data no longer offers, and record which they were.
 *  Session state only: the document keeps the preset, so a value that returns
 *  to the source data (or an author who never saves) is unaffected. */
export function dropStaleSlicerValues(id: string, kept: string[], dropped: string[]) {
  if (dropped.length > 0) syncSlicerSelection(id, kept.length > 0 ? { kind: "values", values: kept } : null);
  useUiStore.getState().resolveSlicerPreset(id, dropped);
}

/** The document's preset selections, keyed by slicer element id. */
function presetSelections(doc: Dashboard | null): Record<string, SlicerSelection> {
  const out: Record<string, SlicerSelection> = {};
  for (const el of doc?.elements ?? []) {
    const d = el.slicer?.default;
    if (!d) continue;
    if (d.kind === "values" && d.values.length > 0) out[el.id] = { kind: "values", values: d.values };
    else if (d.kind === "range" && (d.from !== undefined || d.to !== undefined)) {
      out[el.id] = { kind: "range", from: d.from, to: d.to };
    }
  }
  return out;
}

// ─── Undo / redo ─────────────────────────────────────────────────────────────

/** Step the document history, then realign the live selection of every slicer
 *  whose preset the step actually changed — otherwise undoing a preset edit
 *  leaves the slicer visibly selected while the document says otherwise.
 *
 *  Comparing before against after, rather than reseeding wholesale, is what
 *  keeps an unrelated undo from disturbing a selection that came from a ?f=
 *  link and has no preset of its own. A restored preset is queued for a fresh
 *  data check: redo can bring back an older preset whose values are gone. */
function stepHistory(step: () => void) {
  const before = presetSelections(useDocStore.getState().doc);
  step();
  const after = presetSelections(useDocStore.getState().doc);
  for (const id of new Set([...Object.keys(before), ...Object.keys(after)])) {
    if (JSON.stringify(before[id]) === JSON.stringify(after[id])) continue;
    syncSlicerSelection(id, after[id] ?? null);
    if (after[id]) useUiStore.getState().recheckSlicerPreset(id);
    else useUiStore.getState().resolveSlicerPreset(id, []);
  }
}

export function undo() {
  stepHistory(() => useDocStore.temporal.getState().undo());
}

export function redo() {
  stepHistory(() => useDocStore.temporal.getState().redo());
}

/** Seed the live selections for a freshly opened document: presets first, then
 *  the ?f= deep link on top (an explicit link always wins). Every seeded slicer
 *  is marked pending, so values that no longer exist in the data are dropped
 *  once its option list arrives — a preset or a link must not silently filter a
 *  dashboard by a category that has since been deleted or renamed. See
 *  useSlicerPreset in visuals/Slicer. */
export function seedSlicerSelections(doc: Dashboard) {
  const all = { ...presetSelections(doc), ...selectionsFromUrl() };
  useUiStore.getState().setSlicerSelections(all, Object.keys(all));
  // Presets are not in the URL yet — mirror them so the link a viewer copies
  // reproduces what they see.
  selectionsToUrl(all);
}

// ─── Full-screen (chrome-less) view ──────────────────────────────────────────

/** True when the URL asks for full-screen (?fullscreen / ?fullscreen=true). */
export function fullscreenFromUrl(): boolean {
  const v = new URLSearchParams(window.location.search).get("fullscreen");
  return v !== null && v !== "false" && v !== "0";
}

/** Enter/exit chrome-less view and mirror it into the ?fullscreen param. */
export function setFullscreen(on: boolean) {
  useUiStore.getState().setFullscreen(on);
  const url = new URL(window.location.href);
  if (on) url.searchParams.set("fullscreen", "");
  else url.searchParams.delete("fullscreen");
  // searchParams serializes "fullscreen=" — trim the dangling "=" for a
  // cleaner shareable link.
  window.history.replaceState({}, "", url.toString().replace(/\?fullscreen=(&|$)/, "?fullscreen$1").replace(/&fullscreen=(&|$)/, "&fullscreen$1"));
}

// ─── Load / create ───────────────────────────────────────────────────────────

/** Fetch and open the dashboard at path (called when the URL path changes). */
export async function openDashboard(path: string) {
  const ui = useUiStore.getState();
  try {
    const d = await getDashboard(path);
    if (useUiStore.getState().path !== path) return;
    if (!d.valid || !d.document) {
      loadDoc(null);
      ui.opened(path, d.etag, null, d.error || "this file is not a valid dashboard — fix it on disk and reload");
      return;
    }
    loadDoc(d.document);
    ui.opened(path, d.etag, JSON.stringify(d.document));
    // Presets from the document, then the ?f= deep link on top.
    seedSlicerSelections(d.document);
  } catch (e) {
    if (useUiStore.getState().path !== path) return;
    loadDoc(null);
    ui.opened(path, null, null, e instanceof Error ? e.message : String(e));
  }
}

/** Start a new, unsaved dashboard at path and navigate to it. */
export function createDashboard(path: string) {
  const doc = newDashboard();
  loadDoc(doc);
  useUiStore.getState().opened(path, null, null);
  useUiStore.getState().setSlicerSelections({});
  gotoDashboard(path);
}

// ─── Save / conflicts ────────────────────────────────────────────────────────

export async function save(): Promise<void> {
  const ui = useUiStore.getState();
  const doc = useDocStore.getState().doc;
  if (!ui.path || !doc || ui.saving) return;
  ui.setSaving(true);
  try {
    const r = await putDashboard(ui.path, doc, ui.etag);
    ui.markSaved(r.etag, JSON.stringify(doc));
  } catch (e) {
    if (e instanceof DashConflictError) {
      ui.setConflict({ message: e.message, currentEtag: e.currentEtag, modified: e.modified });
    } else {
      ui.setSaveError(e instanceof Error ? e.message : String(e));
    }
  } finally {
    ui.setSaving(false);
  }
}

/** Conflict dialog: force-save over the version currently on disk. */
export async function overwriteConflict(): Promise<void> {
  const ui = useUiStore.getState();
  const doc = useDocStore.getState().doc;
  const conflict = ui.conflict;
  if (!ui.path || !doc || !conflict) return;
  try {
    const r = await putDashboard(ui.path, doc, conflict.currentEtag);
    ui.markSaved(r.etag, JSON.stringify(doc));
  } catch (e) {
    ui.setConflict(null);
    if (e instanceof DashConflictError) {
      // Changed again since the dialog opened — surface the fresh conflict.
      ui.setConflict({ message: e.message, currentEtag: e.currentEtag, modified: e.modified });
    } else {
      ui.setSaveError(e instanceof Error ? e.message : String(e));
    }
  }
}

/** Conflict dialog: discard local changes and reload the disk version. */
export async function reloadConflict(): Promise<void> {
  const ui = useUiStore.getState();
  ui.setConflict(null);
  if (ui.path) await openDashboard(ui.path);
}
