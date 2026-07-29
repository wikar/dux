// The Dash section (dashboards editor). Composed by src/App.tsx.
//
// Import boundary: src/dash may import from src/components, src/router, and
// @dux/core — never from Home/Explorer code — and nothing outside the app
// entry imports from src/dash. This keeps a future dash-only duxuid bundle a
// matter of adding a second Vite entry over src/dash + src/components.
import { useEffect, useRef, useState } from "react";
import styles from "./DashApp.module.css";
import { fullscreenFromUrl, openDashboard, redo, save, seedSlicerSelections, setFullscreen, undo } from "./actions";
import { nudgeElement, removeElement } from "./docOps";
import { useDirty, useDocStore, useUiStore } from "./store";
import { setNavigationBlocker, useSearch } from "../router";
import CollapsiblePanel from "../components/CollapsiblePanel";
import SchemaTree from "../components/SchemaTree";
import Canvas from "./components/Canvas";
import ConflictDialog from "./components/ConflictDialog";
import ElementsPane from "./components/ElementsPane";
import Home from "./components/Home";

interface Props {
  /** Dashboard identity from the URL ("" = landing list). */
  path: string;
  showHidden: boolean;
}

export default function DashApp({ path, showHidden }: Props) {
  const search = useSearch();
  const loadedPath = useUiStore((s) => s.loadedPath);
  const loadError = useUiStore((s) => s.loadError);
  const mode = useUiStore((s) => s.mode);
  const fullscreen = useUiStore((s) => s.fullscreen);
  const conflict = useUiStore((s) => s.conflict);
  const doc = useDocStore((s) => s.doc);
  const dirty = useDirty();
  const setPath = useUiStore((s) => s.setPath);
  const [exitVisible, setExitVisible] = useState(false);
  const exitTimer = useRef<number | null>(null);

  const revealExit = () => {
    setExitVisible(true);
    if (exitTimer.current !== null) window.clearTimeout(exitTimer.current);
    exitTimer.current = window.setTimeout(() => setExitVisible(false), 1200);
  };

  useEffect(() => {
    if (!fullscreen) setExitVisible(false);
    return () => {
      if (exitTimer.current !== null) window.clearTimeout(exitTimer.current);
    };
  }, [fullscreen]);

  // Mirror the URL path into the dash UI store (DashActions reads it).
  useEffect(() => {
    setPath(path);
  }, [path, setPath]);

  // Keep query-string deep links synchronized on browser back/forward.
  useEffect(() => {
    useUiStore.getState().setFullscreen(fullscreenFromUrl());
    const currentDoc = useDocStore.getState().doc;
    const ui = useUiStore.getState();
    if (currentDoc && path === ui.loadedPath) seedSlicerSelections(currentDoc);
  }, [path, search]);

  // One shared guard covers tabs, dashboard links, browser history and unload.
  useEffect(() => {
    if (!dirty) return;
    const confirmLeave = () => window.confirm("Discard unsaved dashboard changes?");
    const beforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault();
      e.returnValue = "";
    };
    setNavigationBlocker(confirmLeave);
    window.addEventListener("beforeunload", beforeUnload);
    return () => {
      setNavigationBlocker(null);
      window.removeEventListener("beforeunload", beforeUnload);
    };
  }, [dirty]);

  // Load the dashboard whenever the URL points somewhere new. A freshly
  // created (unsaved) dashboard already has loadedPath set, so no fetch.
  useEffect(() => {
    if (path && path !== loadedPath) void openDashboard(path);
  }, [path, loadedPath]);

  // Keyboard shortcuts — mounted only while the Dash tab is active.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      const inField =
        t instanceof HTMLInputElement || t instanceof HTMLTextAreaElement || t.isContentEditable;
      const mod = e.ctrlKey || e.metaKey;
      const ui = useUiStore.getState();

      if (mod && e.key.toLowerCase() === "s") {
        e.preventDefault();
        void save();
        return;
      }
      if (inField) return;

      if (mod && e.key.toLowerCase() === "z" && !e.shiftKey) {
        e.preventDefault();
        if (ui.mode === "edit") undo();
        return;
      }
      if (mod && (e.key.toLowerCase() === "y" || (e.key.toLowerCase() === "z" && e.shiftKey))) {
        e.preventDefault();
        if (ui.mode === "edit") redo();
        return;
      }
      if (e.key === "Escape") {
        if (ui.fullscreen) setFullscreen(false);
        else ui.select(null);
        return;
      }
      if (ui.mode !== "edit" || !ui.selectedId) return;

      if (e.key === "Delete" || e.key === "Backspace") {
        e.preventDefault();
        removeElement(ui.selectedId);
        return;
      }
      const step = e.shiftKey ? 1 : 8;
      const arrows: Record<string, [number, number]> = {
        ArrowLeft: [-step, 0],
        ArrowRight: [step, 0],
        ArrowUp: [0, -step],
        ArrowDown: [0, step],
      };
      if (arrows[e.key]) {
        e.preventDefault();
        nudgeElement(ui.selectedId, ...arrows[e.key]);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  if (!path) return <Home />;

  return (
    <div className={styles.main} onPointerMove={fullscreen ? revealExit : undefined}>
      {mode === "edit" && doc && (
        <CollapsiblePanel title="Schema" side="left" width={260} storageKey="dux.dash.schema">
          <SchemaTree bare showHidden={showHidden} />
        </CollapsiblePanel>
      )}
      {mode === "edit" && doc && (
        <CollapsiblePanel title="Elements" side="left" width={280} storageKey="dux.dash.elements">
          <ElementsPane />
        </CollapsiblePanel>
      )}
      {fullscreen && (
        <button
          className={`${styles.exitFullscreen} ${exitVisible ? styles.exitFullscreenVisible : ""}`}
          title="Exit full screen (Esc)"
          onClick={() => setFullscreen(false)}
        >
          ✕
        </button>
      )}
      <div className={styles.center}>
        {loadError ? (
          <div className={styles.loadError}>
            <h2>Can't open “{path}”</h2>
            <p>{loadError}</p>
          </div>
        ) : doc ? (
          <Canvas />
        ) : (
          <div className={styles.loading}>Loading…</div>
        )}
      </div>
      {conflict && <ConflictDialog />}
    </div>
  );
}
