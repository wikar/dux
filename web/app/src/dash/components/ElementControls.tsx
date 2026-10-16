import { useRef, useState } from "react";
import { createPortal } from "react-dom";
import { applySlicerSelection } from "../actions";
import { downloadCsv } from "../csv";
import { useAffectingFilters, useRefreshInterval } from "../data";
import { useElementData } from "../elementQuery";
import { useDocStore, useUiStore } from "../store";
import type { DashElement } from "../types";
import type { VisualDef } from "../visuals/types";
import { downloadIcon, eraserIcon, funnelIcon } from "../glyphs";
import { ControlsBoundary } from "./ElementBoundary";
import styles from "./ElementBody.module.css";

/** CSV download (title bar, or floating for a titleless element).
 *  useElementData hits the cache — the body already runs the same query. */
export function CsvButton({ el, className }: { el: DashElement; className: string }) {
  const { data } = useElementData(el);
  if (!data) return null;
  return (
    <button
      className={className}
      title="Download CSV"
      onPointerDown={(e) => e.stopPropagation()}
      onClick={() => downloadCsv(data, el.title?.text || el.id)}
    >
      {downloadIcon}
    </button>
  );
}

/** Clears a slicer's selection from its title bar. Renders nothing until
 *  something is selected, so the header stays quiet on an untouched slicer. */
export function ClearButton({ el, className }: { el: DashElement; className: string }) {
  const sel = useUiStore((s) => s.slicerSelections[el.id]);
  if (!sel) return null;
  const n = sel.kind === "values" ? sel.values.length : 1;
  return (
    <button
      className={className}
      title={`Clear selection (${n})`}
      onPointerDown={(e) => e.stopPropagation()}
      onClick={() => applySlicerSelection(el.id, null)}
    >
      {eraserIcon}
    </button>
  );
}

/** Status components call their own data hooks statically. */
export function RefreshDot({ el, def }: { el: DashElement; def: VisualDef }) {
  const Status = def.RefreshStatus ?? (def.data ? QueryRefreshStatus : undefined);
  return Status ? <Status key={el.type} el={el} /> : null;
}

function QueryRefreshStatus({ el }: { el: DashElement }) {
  const live = useRefreshInterval(el.id) !== false;
  const { loading } = useElementData(el);
  return <RefreshIndicator live={live} fetching={loading} />;
}

export function RefreshIndicator({ live, fetching }: { live: boolean; fetching: boolean }) {
  if (!live) return null;
  return (
    <span
      className={`${styles.refreshDot}${fetching ? ` ${styles.refreshDotOn}` : ""}`}
      title={fetching ? "Refreshing…" : "Live refresh on"}
    />
  );
}

export function hasElementTitle(el: DashElement): boolean {
  return el.type !== "kpi" && el.title?.show !== false && !!el.title?.text;
}

/** Header/floating control that reveals every filter affecting this visual
 *  (own query filters, active slicers, cross-filtering visuals) in a popover.
 *  The badge shows the count; the button stays lit while filters are active. */
export function FunnelButton({ el, floating }: { el: DashElement; floating?: boolean }) {
  const filters = useAffectingFilters(el);
  const ref = useRef<HTMLButtonElement>(null);
  const [rect, setRect] = useState<DOMRect | null>(null);
  const [pinned, setPinned] = useState(false);
  const count = filters.length;
  const openPop = () => setRect(ref.current?.getBoundingClientRect() ?? null);
  const closePop = () => {
    if (!pinned) setRect(null);
  };
  return (
    <>
      <button
        ref={ref}
        className={`${styles.funnelBtn} ${floating ? styles.funnelFloat : ""}`}
        title="Filters affecting this visual"
        onPointerDown={(e) => e.stopPropagation()}
        onMouseEnter={openPop}
        onMouseLeave={closePop}
        onClick={(e) => {
          // Hover shows it; a click pins it open (toggle) so it survives
          // moving the pointer away.
          e.stopPropagation();
          if (pinned) {
            setPinned(false);
            setRect(null);
          } else {
            setPinned(true);
            openPop();
          }
        }}
      >
        {funnelIcon}
      </button>
      {rect &&
        createPortal(
          <div
            className={styles.funnelPop}
            style={{ top: rect.bottom + 4, left: Math.max(8, Math.min(rect.left, window.innerWidth - 268)) }}
            onMouseEnter={openPop}
            onMouseLeave={closePop}
          >
            {count === 0 ? (
              <div className={styles.funnelEmpty}>No filters affecting this visual.</div>
            ) : (
              filters.map((f, i) => (
                <div key={i} className={styles.funnelItem}>
                  <span className={styles.funnelSrc}>{f.source}</span>
                  <span className={styles.funnelTxt}>{f.text}</span>
                </div>
              ))
            )}
          </div>,
          document.body
        )}
    </>
  );
}

/** Hover controls for an element with no title bar to hang them in. */
export function FloatingActions({ el, def }: { el: DashElement; def: VisualDef }) {
  const controls = useDocStore((s) => s.doc?.controls);
  const live = useRefreshInterval(el.id) !== false;
  if (hasElementTitle(el)) return null;
  const funnel = !!def.controls?.funnel && controls?.funnel !== false;
  const csv = !!def.controls?.csv && controls?.csv !== false;
  // Clear is not document-toggleable: without a title bar this is the only
  // way back out of a selection.
  const clear = !!def.controls?.clear;
  if (!funnel && !csv && !clear && !live) return null;
  return (
    <div className={styles.floatingActions}>
      <ControlsBoundary el={el}>
        {clear && <ClearButton el={el} className={styles.exportBtn} />}
        {funnel && <FunnelButton el={el} floating />}
        {csv && <CsvButton el={el} className={styles.exportBtn} />}
        <RefreshDot el={el} def={def} />
      </ControlsBoundary>
    </div>
  );
}
