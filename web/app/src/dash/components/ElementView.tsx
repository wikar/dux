import { useRef, useState } from "react";
import styles from "./ElementView.module.css";
import { applyGesture, updateElement, type GestureKind } from "../docOps";
import { useUiStore } from "../store";
import type { CanvasSpec, DashElement, Layout } from "../types";
import { QUERY_TYPES } from "../types";
import ElementBody, { FunnelButton, TitleCsvButton } from "./ElementBody";

const HANDLES: GestureKind[] = ["n", "s", "e", "w", "ne", "nw", "se", "sw"];

interface Props {
  el: DashElement;
  canvas: CanvasSpec;
  scale: number;
  onContextMenu: (x: number, y: number) => void;
}

/** Base element container: title bar, selection ring, drag/resize gestures,
 *  spinner/error overlays. During a gesture the layout lives in local state
 *  (the ghost) and is committed to the document once on release — one undo
 *  step per drag. */
export default function ElementView({ el, canvas, scale, onContextMenu }: Props) {
  const mode = useUiStore((s) => s.mode);
  const selected = useUiStore((s) => s.selectedId === el.id);
  const select = useUiStore((s) => s.select);
  const [ghost, setGhost] = useState<Layout | null>(null);
  const gesture = useRef<{ kind: GestureKind; startX: number; startY: number; orig: Layout } | null>(null);

  const editing = mode === "edit";
  const layout = ghost ?? el.layout;

  const begin = (e: React.PointerEvent, kind: GestureKind) => {
    if (!editing || e.button !== 0) return;
    e.stopPropagation();
    select(el.id);
    gesture.current = { kind, startX: e.clientX, startY: e.clientY, orig: { ...el.layout } };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  };

  const resolve = (e: React.PointerEvent): Layout | null => {
    const g = gesture.current;
    if (!g) return null;
    const dx = (e.clientX - g.startX) / scale;
    const dy = (e.clientY - g.startY) / scale;
    return applyGesture(g.kind, g.orig, dx, dy, canvas);
  };

  const move = (e: React.PointerEvent) => {
    const next = resolve(e);
    if (next) setGhost(next);
  };

  const end = (e: React.PointerEvent) => {
    const final = resolve(e);
    gesture.current = null;
    setGhost(null);
    if (final && JSON.stringify(final) !== JSON.stringify(el.layout)) {
      updateElement(el.id, (x) => ({ ...x, layout: final }));
    }
  };

  const showTitle = el.title?.show !== false && !!el.title?.text;

  return (
    <div
      data-element-id={el.id}
      className={`${styles.el} ${editing ? styles.editing : ""} ${selected ? styles.selected : ""}`}
      style={{ left: layout.x, top: layout.y, width: layout.w, height: layout.h, zIndex: layout.z ?? 0 }}
      onPointerDown={(e) => begin(e, "move")}
      onPointerMove={move}
      onPointerUp={end}
      onContextMenu={(e) => {
        if (!editing) return;
        e.preventDefault();
        e.stopPropagation();
        select(el.id);
        onContextMenu(e.clientX, e.clientY);
      }}
    >
      {showTitle && (
        <div className={styles.titleBar}>
          <span className={styles.titleText}>{el.title!.text}</span>
          {QUERY_TYPES.has(el.type) && <FunnelButton el={el} />}
          {QUERY_TYPES.has(el.type) && <TitleCsvButton el={el} className={styles.titleCsv} />}
        </div>
      )}
      <div className={styles.body}>
        <ElementBody el={el} />
      </div>
      {editing && selected && (
        <>
          {HANDLES.map((dir) => (
            <div
              key={dir}
              className={`${styles.handle} ${styles[`h_${dir}`]}`}
              onPointerDown={(e) => begin(e, dir)}
              onPointerMove={move}
              onPointerUp={end}
            />
          ))}
        </>
      )}
    </div>
  );
}

