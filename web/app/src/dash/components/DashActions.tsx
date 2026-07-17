import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import styles from "./DashActions.module.css";
import Modal, { modalStyles } from "../../components/Modal";
import { listDashboards, type DashEntry } from "../api";
import { createDashboard, gotoDashboard, save } from "../actions";
import { redo, undo, useDirty, useTemporal, useUiStore } from "../store";

/** Top-bar actions while the Dash tab is active: dashboard picker, new,
 *  save, undo/redo, edit/view mode. Rendered inside the shared TopBar. */
export default function DashActions() {
  const path = useUiStore((s) => s.path);
  const mode = useUiStore((s) => s.mode);
  const setMode = useUiStore((s) => s.setMode);
  const saving = useUiStore((s) => s.saving);
  const saveError = useUiStore((s) => s.saveError);
  const setSaveError = useUiStore((s) => s.setSaveError);
  const dirty = useDirty();
  const canUndo = useTemporal((s) => s.pastStates.length > 0);
  const canRedo = useTemporal((s) => s.futureStates.length > 0);
  const [newOpen, setNewOpen] = useState(false);

  return (
    <>
      {saveError && (
        <span className={styles.error} title={saveError}>
          save failed: {saveError}
          <button className={styles.dismiss} title="Dismiss" onClick={() => setSaveError(null)}>
            ✕
          </button>
        </span>
      )}
      <DashboardPicker />
      <button className={styles.actionBtn} onClick={() => setNewOpen(true)} title="New dashboard">
        + New
      </button>
      {path && (
        <>
          <button
            className={styles.actionBtn}
            onClick={() => void save()}
            disabled={!dirty || saving}
            title="Save (Ctrl+S)"
          >
            {saving ? "Saving…" : dirty ? "Save" : "Saved"}
            {dirty && !saving && <span className={styles.dirtyDot}>●</span>}
          </button>
          <button
            className={styles.actionBtn}
            onClick={undo}
            disabled={!canUndo || mode !== "edit"}
            title="Undo (Ctrl+Z)"
          >
            ⤺
          </button>
          <button
            className={styles.actionBtn}
            onClick={redo}
            disabled={!canRedo || mode !== "edit"}
            title="Redo (Ctrl+Y)"
          >
            ⤻
          </button>
          <div className={styles.modeToggle}>
            <button
              className={`${styles.modeBtn}${mode === "edit" ? ` ${styles.modeBtnActive}` : ""}`}
              onClick={() => setMode("edit")}
            >
              Edit
            </button>
            <button
              className={`${styles.modeBtn}${mode === "view" ? ` ${styles.modeBtnActive}` : ""}`}
              onClick={() => setMode("view")}
            >
              View
            </button>
          </div>
        </>
      )}
      {newOpen && <NewDashboardModal onClose={() => setNewOpen(false)} />}
    </>
  );
}

// ─── Dashboard picker (hierarchical tree from the flat path list) ────────────

interface TreeNode {
  name: string;
  entry?: DashEntry;
  children: TreeNode[];
}

function buildTree(entries: DashEntry[]): TreeNode[] {
  const root: TreeNode = { name: "", children: [] };
  for (const entry of [...entries].sort((a, b) => a.path.localeCompare(b.path))) {
    const segments = entry.path.split("/");
    let node = root;
    for (let i = 0; i < segments.length - 1; i++) {
      let child = node.children.find((c) => !c.entry && c.name === segments[i]);
      if (!child) {
        child = { name: segments[i], children: [] };
        node.children.push(child);
      }
      node = child;
    }
    node.children.push({ name: segments[segments.length - 1], entry, children: [] });
  }
  return root.children;
}

function DashboardPicker() {
  const path = useUiStore((s) => s.path);
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const queryClient = useQueryClient();
  const { data: entries, isLoading, error } = useQuery({
    queryKey: ["dashboards"],
    queryFn: listDashboards,
    enabled: open,
  });

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    window.addEventListener("mousedown", onDown);
    return () => window.removeEventListener("mousedown", onDown);
  }, [open]);

  const pick = (p: string) => {
    setOpen(false);
    void queryClient.invalidateQueries({ queryKey: ["dashboards"] });
    gotoDashboard(p);
  };

  return (
    <div className={styles.picker} ref={ref}>
      <button className={styles.pickerBtn} onClick={() => setOpen((o) => !o)}>
        <span className={styles.pickerLabel}>{path || "Open dashboard"}</span>
        <span className={styles.caret}>▾</span>
      </button>
      {open && (
        <div className={styles.dropdown}>
          {isLoading && <div className={styles.dim}>Loading…</div>}
          {error != null && <div className={styles.dim}>failed to list: {String(error)}</div>}
          {entries && entries.length === 0 && <div className={styles.dim}>No dashboards yet</div>}
          {entries && <TreeLevel nodes={buildTree(entries)} depth={0} onPick={pick} current={path} />}
        </div>
      )}
    </div>
  );
}

function TreeLevel({
  nodes,
  depth,
  onPick,
  current,
}: {
  nodes: TreeNode[];
  depth: number;
  onPick: (path: string) => void;
  current: string;
}) {
  return (
    <>
      {nodes.map((n) =>
        n.entry ? (
          <button
            key={n.entry.path}
            className={`${styles.item}${n.entry.path === current ? ` ${styles.itemActive}` : ""}`}
            style={{ paddingLeft: 12 + depth * 14 }}
            disabled={!n.entry.valid}
            title={n.entry.valid ? n.entry.path : n.entry.error}
            onClick={() => onPick(n.entry!.path)}
          >
            {n.name}
            {!n.entry.valid && <span className={styles.invalid}> ⚠ invalid</span>}
          </button>
        ) : (
          <div key={`dir:${depth}:${n.name}`}>
            <div className={styles.folder} style={{ paddingLeft: 12 + depth * 14 }}>
              {n.name}/
            </div>
            <TreeLevel nodes={n.children} depth={depth + 1} onPick={onPick} current={current} />
          </div>
        )
      )}
    </>
  );
}

// ─── New dashboard modal ─────────────────────────────────────────────────────

const PATH_RE = /^[a-z0-9 _-]+(\/[a-z0-9 _-]+)*$/;

function NewDashboardModal({ onClose }: { onClose: () => void }) {
  const [path, setPath] = useState("");
  const [error, setError] = useState<string | null>(null);

  const submit = () => {
    const p = path.trim().toLowerCase().replace(/^\/+|\/+$/g, "");
    if (!p) return setError("path is required");
    if (p.endsWith(".json")) return setError("leave out the .json extension — the path is the identity");
    if (!PATH_RE.test(p)) return setError("use lower-case letters, digits, space, - and _; folders with /");
    if (p === "theme") return setError('"theme" is reserved for the global theme');
    createDashboard(p);
    onClose();
  };

  return (
    <Modal
      title="New dashboard"
      onClose={onClose}
      footer={
        <>
          <button className={modalStyles.btn} onClick={onClose}>
            Cancel
          </button>
          <button className={modalStyles.btnPrimary} onClick={submit}>
            Create
          </button>
        </>
      }
    >
      <p className={modalStyles.hint}>
        The path is the dashboard's identity and its file on disk. Use <code>/</code> for folders,
        e.g. <code>sales/overview</code>.
      </p>
      <input
        autoFocus
        className={modalStyles.input}
        value={path}
        placeholder="sales/overview"
        onChange={(e) => {
          setPath(e.target.value);
          setError(null);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") submit();
          if (e.key === "Escape") onClose();
        }}
      />
      {error && <div className={modalStyles.error}>{error}</div>}
    </Modal>
  );
}
