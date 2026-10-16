import type { HTMLAttributes, ReactNode } from "react";
import { useDocStore } from "../store";
import type { DashElement, Layout } from "../types";
import type { VisualDef } from "../visuals/types";
import { ControlsBoundary } from "./ElementBoundary";
import { ClearButton, CsvButton, FunnelButton, RefreshDot, hasElementTitle } from "./ElementControls";
import styles from "./ElementView.module.css";

type FrameEvents = Pick<HTMLAttributes<HTMLDivElement>, "onPointerDown" | "onPointerMove" | "onPointerUp" | "onContextMenu">;

export default function ElementFrame({ el, meta, layout, editing, selected, children, editorOverlay, ...events }: FrameEvents & {
  el: DashElement;
  meta: VisualDef;
  layout: Layout;
  editing: boolean;
  selected: boolean;
  children: ReactNode;
  editorOverlay?: ReactNode;
}) {
  const showFunnel = useDocStore((s) => s.doc?.controls?.funnel) !== false;
  const showCsv = useDocStore((s) => s.doc?.controls?.csv) !== false;
  return (
    <div
      data-element-id={el.id}
      className={`${styles.el} ${meta?.bare ? styles.map : ""} ${el.viz?.transparent ? styles.transparent : ""} ${editing ? styles.editing : ""} ${selected ? styles.selected : ""}`}
      style={{ left: layout.x, top: layout.y, width: layout.w, height: layout.h, zIndex: layout.z ?? 0 }}
      {...events}
    >
      {hasElementTitle(el) && (
        <div className={styles.titleBar}>
          <span className={styles.titleText}>{el.title!.text}</span>
          <div className={styles.titleActions}>
            <ControlsBoundary el={el}>
              {meta?.controls?.clear && <ClearButton el={el} className={styles.titleCsv} />}
              {meta?.controls?.funnel && showFunnel && <FunnelButton el={el} />}
              {meta?.controls?.csv && showCsv && <CsvButton el={el} className={styles.titleCsv} />}
              <RefreshDot el={el} def={meta} />
            </ControlsBoundary>
          </div>
        </div>
      )}
      <div className={styles.body}>
        {children}
      </div>
      {editorOverlay}
    </div>
  );
}
