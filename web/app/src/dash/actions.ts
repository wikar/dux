// Cross-component actions: navigation, load, save, and conflict resolution.
import { DashConflictError, encodePath, getDashboard, putDashboard } from "./api";
import { navigate } from "../router";
import { loadDoc, useDocStore, useUiStore } from "./store";
import { newDashboard } from "./types";

/** Navigate to a dashboard by identity (updates the /dash/<path> URL). */
export function gotoDashboard(path: string) {
  navigate(`/dash/${encodePath(path)}`);
}

// ─── Load / create ───────────────────────────────────────────────────────────

/** Fetch and open the dashboard at path (called when the URL path changes). */
export async function openDashboard(path: string) {
  const ui = useUiStore.getState();
  try {
    const d = await getDashboard(path);
    if (!d.valid || !d.document) {
      loadDoc(null);
      ui.opened(path, d.etag, null, d.error || "this file is not a valid dashboard — fix it on disk and reload");
      return;
    }
    loadDoc(d.document);
    ui.opened(path, d.etag, JSON.stringify(d.document));
  } catch (e) {
    loadDoc(null);
    ui.opened(path, null, null, e instanceof Error ? e.message : String(e));
  }
}

/** Start a new, unsaved dashboard at path and navigate to it. */
export function createDashboard(path: string) {
  const doc = newDashboard();
  loadDoc(doc);
  useUiStore.getState().opened(path, null, null);
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
