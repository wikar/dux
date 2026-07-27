import { useRef, useState } from "react";
import styles from "./ElementView.module.css";
import { applyGesture, type GestureKind } from "../docOps";
import { updateElement, useDocStore, useUiStore } from "../store";
import type { CanvasSpec, DashElement, Layout } from "../types";
import { VISUALS } from "../visuals";
import ElementBody, { ClearButton, ControlsBoundary, CsvButton, FunnelButton, RefreshDot } from "./ElementBody";

const HANDLES: GestureKind[] = ["n", "s", "e", "w", "ne", "nw", "se", "sw"];

/** Screen-pixel slop a press must exceed before it counts as a drag. Without
 *  it, clicking to select re-runs the gesture math at zero delta and the grid
 *  snap shifts any element the settings pane placed off-grid. */
const DRAG_SLOP = 3;

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
  const showFunnel = useDocStore((s) => s.doc?.controls?.funnel) !== false;
  const showCsv = useDocStore((s) => s.doc?.controls?.csv) !== false;
  const [ghost, setGhost] = useState<Layout | null>(null);
  const gesture = useRef<{
    kind: GestureKind;
    startX: number;
    startY: number;
    orig: Layout;
    live: boolean;
  } | null>(null);

  const editing = mode === "edit";
  const layout = ghost ?? el.layout;

  const begin = (e: React.PointerEvent, kind: GestureKind) => {
    if (!editing || e.button !== 0) return;
    e.stopPropagation();
    select(el.id);
    gesture.current = { kind, startX: e.clientX, startY: e.clientY, orig: { ...el.layout }, live: false };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  };

  const resolve = (e: React.PointerEvent): Layout | null => {
    const g = gesture.current;
    if (!g) return null;
    const sx = e.clientX - g.startX;
    const sy = e.clientY - g.startY;
    // A press stays inert until it clears the slop, so selecting leaves the
    // layout byte-identical; once dragging, it stays live for the gesture.
    if (!g.live) {
      if (Math.abs(sx) < DRAG_SLOP && Math.abs(sy) < DRAG_SLOP) return null;
      g.live = true;
    }
    return applyGesture(g.kind, g.orig, sx / scale, sy / scale, canvas);
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
  const meta = VISUALS[el.type];

  return (
    <div
      data-element-id={el.id}
      className={`${styles.el} ${meta?.bare ? styles.map : ""} ${el.viz?.transparent ? styles.transparent : ""} ${editing ? styles.editing : ""} ${selected ? styles.selected : ""}`}
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
          <div className={styles.titleActions}>
            <ControlsBoundary el={el}>
              {meta?.controls?.clear && <ClearButton el={el} className={styles.titleCsv} />}
              {meta?.controls?.funnel && showFunnel && <FunnelButton el={el} />}
              {meta?.controls?.csv && showCsv && <CsvButton el={el} className={styles.titleCsv} />}
              <RefreshDot el={el} />
            </ControlsBoundary>
          </div>
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
