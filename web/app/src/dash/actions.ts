// Cross-component actions: navigation, load, save, and conflict resolution.
import { DashConflictError, encodePath, getDashboard, putDashboard } from "./api";
import { navigate } from "../router";
import { loadDoc, useDocStore, useUiStore } from "./store";
import { newDashboard } from "./types";
import type { SlicerSelection } from "./types";

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

/** Set (or clear) one slicer's selection and mirror it into the deep link. */
export function applySlicerSelection(id: string, sel: SlicerSelection | null) {
  const ui = useUiStore.getState();
  ui.setSlicerSelection(id, sel);
  selectionsToUrl(useUiStore.getState().slicerSelections);
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
    // Deep link / reload persistence: seed slicer selections from ?f=.
    ui.setSlicerSelections(selectionsFromUrl());
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
