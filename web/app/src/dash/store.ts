// App state: the dashboard document (undoable, zustand + zundo) and UI
// state (selection, mode, save bookkeeping — never part of undo history).
import { create } from "zustand";
import { useStore } from "zustand";
import { temporal } from "zundo";
import type { Dashboard } from "./types";

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

  setPath: (path: string) => void;
  opened: (path: string, etag: string | null, savedJson: string | null, loadError?: string | null) => void;
  markSaved: (etag: string, savedJson: string) => void;
  setMode: (mode: "edit" | "view") => void;
  select: (id: string | null) => void;
  setConflict: (c: ConflictInfo | null) => void;
  setSaveError: (msg: string | null) => void;
  setSaving: (saving: boolean) => void;
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

  setPath: (path) => set({ path }),
  opened: (path, etag, savedJson, loadError = null) =>
    set({ loadedPath: path, etag, savedJson, loadError, selectedId: null, conflict: null, saveError: null }),
  markSaved: (etag, savedJson) => set({ etag, savedJson, conflict: null, saveError: null }),
  setMode: (mode) => set({ mode, selectedId: null }),
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
