// App state: the dashboard document (undoable, zustand + zundo) and UI
// state (selection, mode, save bookkeeping — never part of undo history).
import { create } from "zustand";
import { useStore } from "zustand";
import { temporal } from "zundo";
import type { Dashboard, SlicerSelection } from "./types";

// ─── Cross-filter (chart click) selection ────────────────────────────────────

/** One selected mark = the dim column→value tuple that identifies it. */
export type CrossMark = { dims: { table: string; column: string; value: string | number }[] };

/** Stable key for a mark (order-independent over its dims) — used to dedupe
 *  selections and to test membership when highlighting. */
export function markKey(m: CrossMark): string {
  const dims = m.dims.map((d) => [d.table, d.column, d.value] as const);
  dims.sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
  return JSON.stringify(dims);
}

// ─── Document store (undo/redo via zundo) ────────────────────────────────────

interface DocState {
  doc: Dashboard | null;
  setDoc: (doc: Dashboard | null) => void;
  /** Apply an immutable transform; each call is one undo step. */
  update: (fn: (d: Dashboard) => Dashboard) => void;
}

export const useDocStore = create<DocState>()(
  temporal(
    (set) => ({
      doc: null,
      setDoc: (doc) => set({ doc }),
      update: (fn) => set((s) => (s.doc ? { doc: fn(s.doc) } : s)),
    }),
    { partialize: (s) => ({ doc: s.doc }), limit: 100 }
  )
);

/** Replace the document and reset undo history (load/reload/new). */
export function loadDoc(doc: Dashboard | null) {
  useDocStore.getState().setDoc(doc);
  useDocStore.temporal.getState().clear();
}

export function undo() {
  useDocStore.temporal.getState().undo();
}

export function redo() {
  useDocStore.temporal.getState().redo();
}

/** Reactive selector over the undo history (for enabling toolbar buttons). */
export function useTemporal<T>(selector: (s: { pastStates: unknown[]; futureStates: unknown[] }) => T): T {
  return useStore(useDocStore.temporal, selector);
}

// ─── UI store ────────────────────────────────────────────────────────────────

export interface ConflictInfo {
  message: string;
  currentEtag: string;
  modified: string;
}

interface UiState {
  /** Current dashboard identity from the URL ("" = home). */
  path: string;
  /** Path whose document is currently in the doc store. */
  loadedPath: string | null;
  /** Server etag of the loaded file; null = new, not yet saved. */
  etag: string | null;
  /** Serialized doc as of the last load/save, for dirty tracking. */
  savedJson: string | null;
  /** Load failure (fetch error or invalid file) for the current path. */
  loadError: string | null;
  mode: "edit" | "view";
  selectedId: string | null;
  conflict: ConflictInfo | null;
  saveError: string | null;
  saving: boolean;
  /** Runtime slicer selections by element id (deep-linked via ?f=, not saved). */
  slicerSelections: Record<string, SlicerSelection>;
  /** Runtime chart cross-filter selections, keyed by the SOURCE element id.
   *  Transient — cleared on open/mode change/outside click, never saved. */
  crossFilters: Record<string, CrossMark[]>;
  /** Chrome-less full-screen view (?fullscreen deep link). */
  fullscreen: boolean;

  setPath: (path: string) => void;
  opened: (path: string, etag: string | null, savedJson: string | null, loadError?: string | null) => void;
  markSaved: (etag: string, savedJson: string) => void;
  setMode: (mode: "edit" | "view") => void;
  select: (id: string | null) => void;
  setConflict: (c: ConflictInfo | null) => void;
  setSaveError: (msg: string | null) => void;
  setSaving: (saving: boolean) => void;
  setSlicerSelection: (id: string, sel: SlicerSelection | null) => void;
  setSlicerSelections: (all: Record<string, SlicerSelection>) => void;
  /** Toggle a clicked mark. additive (Ctrl/⌘) accumulates within a source and
   *  keeps other sources; a plain click replaces the whole selection. */
  toggleCrossMark: (sourceId: string, mark: CrossMark, additive: boolean) => void;
  clearCrossFilters: () => void;
  setFullscreen: (on: boolean) => void;
}

export const useUiStore = create<UiState>()((set) => ({
  path: "",
  loadedPath: null,
  etag: null,
  savedJson: null,
  loadError: null,
  mode: "edit",
  selectedId: null,
  conflict: null,
  saveError: null,
  saving: false,
  slicerSelections: {},
  crossFilters: {},
  fullscreen: false,

  setPath: (path) => set({ path }),
  opened: (path, etag, savedJson, loadError = null) =>
    set({
      loadedPath: path,
      etag,
      savedJson,
      loadError,
      selectedId: null,
      conflict: null,
      saveError: null,
      crossFilters: {},
    }),
  setSlicerSelection: (id, sel) =>
    set((s) => {
      const slicerSelections = { ...s.slicerSelections };
      if (sel === null) delete slicerSelections[id];
      else slicerSelections[id] = sel;
      return { slicerSelections };
    }),
  setSlicerSelections: (slicerSelections) => set({ slicerSelections }),
  toggleCrossMark: (sourceId, mark, additive) =>
    set((s) => {
      if (!additive) {
        // Plain-clicking an already selected mark clears every visual;
        // otherwise this mark becomes the entire selection.
        const key = markKey(mark);
        if ((s.crossFilters[sourceId] ?? []).some((m) => markKey(m) === key)) {
          return { crossFilters: {} };
        }
        return { crossFilters: { [sourceId]: [mark] } };
      }
      const key = markKey(mark);
      const existing = s.crossFilters[sourceId] ?? [];
      const kept = existing.filter((m) => markKey(m) !== key);
      const next = { ...s.crossFilters };
      if (kept.length === existing.length) next[sourceId] = [...existing, mark]; // add
      else if (kept.length === 0) delete next[sourceId]; // removed last → drop key
      else next[sourceId] = kept; // removed one
      return { crossFilters: next };
    }),
  clearCrossFilters: () => set((s) => (Object.keys(s.crossFilters).length ? { crossFilters: {} } : s)),
  setFullscreen: (on) =>
    set(on ? { fullscreen: true, mode: "view", selectedId: null, crossFilters: {} } : { fullscreen: false }),
  markSaved: (etag, savedJson) => set({ etag, savedJson, conflict: null, saveError: null }),
  setMode: (mode) => set({ mode, selectedId: null, crossFilters: {} }),
  select: (id) => set({ selectedId: id }),
  setConflict: (conflict) => set({ conflict }),
  setSaveError: (saveError) => set({ saveError }),
  setSaving: (saving) => set({ saving }),
}));

/** True when the document differs from its last loaded/saved state. */
export function useDirty(): boolean {
  const doc = useDocStore((s) => s.doc);
  const savedJson = useUiStore((s) => s.savedJson);
  if (!doc) return false;
  return JSON.stringify(doc) !== savedJson;
}
