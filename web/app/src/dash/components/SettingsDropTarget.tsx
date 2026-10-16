import { useState, type DragEvent, type ReactNode } from "react";
import styles from "./ElementsPane.module.css";
import { updateElement, useDocStore } from "../store";
import type { DashElement } from "../types";
import type { DragPayload } from "@dux/core";

/** Opt an element out of specific slicers (interactions.ignoreSlicers). */
export function IgnoreSlicers({ el }: { el: DashElement }) {
  const doc = useDocStore((s) => s.doc);
  const slicers = (doc?.elements ?? []).filter((e) => e.type === "slicer" && e.id !== el.id);
  if (slicers.length === 0) return null;
  const ignored = el.interactions?.ignoreSlicers ?? [];

  const toggle = (id: string) =>
    updateElement(el.id, (x) => {
      const cur = new Set(x.interactions?.ignoreSlicers ?? []);
      if (cur.has(id)) cur.delete(id);
      else cur.add(id);
      const arr = [...cur];
      const next = { ...x };
      if (arr.length > 0) next.interactions = { ...x.interactions, ignoreSlicers: arr };
      else delete next.interactions;
      return next;
    });

  return (
    <>
      <label className={styles.label}>Ignore slicers</label>
      {slicers.map((sl) => (
        <label key={sl.id} className={styles.check}>
          <input type="checkbox" checked={ignored.includes(sl.id)} onChange={() => toggle(sl.id)} />
          {sl.title?.text || sl.id} <span className={styles.id}>{sl.id}</span>
        </label>
      ))}
    </>
  );
}

/** Shared drag-over highlight + application/dux drop parsing for the wells.
 *  Returns the `over` highlight flag and handlers to spread on the drop div. */
export function useDuxDrop(onDrop: (p: DragPayload) => void) {
  const [over, setOver] = useState(false);
  const dropProps = {
    onDragOver: (e: DragEvent) => {
      if (e.dataTransfer.types.includes("application/dux")) {
        e.preventDefault();
        e.dataTransfer.dropEffect = "copy";
        setOver(true);
      }
    },
    onDragLeave: () => setOver(false),
    onDrop: (e: DragEvent) => {
      e.preventDefault();
      setOver(false);
      const raw = e.dataTransfer.getData("application/dux");
      if (raw) onDrop(JSON.parse(raw) as DragPayload);
    },
  };
  return { over, dropProps };
}

/** Generic single-purpose drop target in the well idiom. */
export function DropTarget({
  label,
  hint,
  onDrop,
  children,
}: {
  label: string;
  hint: string;
  onDrop: (p: DragPayload) => void;
  children?: ReactNode;
}) {
  const { over, dropProps } = useDuxDrop(onDrop);
  return (
    <div>
      <label className={styles.label}>{label}</label>
      <div className={`${styles.well}${over ? ` ${styles.wellOver}` : ""}`} {...dropProps}>
        {children ?? <span className={styles.wellHint}>{hint}</span>}
      </div>
    </div>
  );
}

