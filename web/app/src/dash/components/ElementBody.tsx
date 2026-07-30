// Element body shell: chrome shared by every visual (error boundary, header
// controls, loading/error overlays) plus the one query pipeline that feeds the
// query-backed bodies. What each visual *is* lives in its ../visuals module.
import { Component, useMemo, useRef, useState, type ComponentType, type ReactNode } from "react";
import { createPortal } from "react-dom";
import styles from "./ElementBody.module.css";
import { applySlicerSelection } from "../actions";
import { downloadCsv } from "../csv";
import {
  dropEmptyRows,
  splitElementFields,
  useAffectingFilters,
  useFormats,
  useRefreshInterval,
  useResolvedTheme,
} from "../data";
import { elementSeriesSplit, useElementData } from "../elementQuery";
import { markKey, useDocStore, useUiStore } from "../store";
import type { DashElement, ElementType, VizSettings } from "../types";
import { QueryFailedError } from "@dux/core";
import { displayMessage } from "../message";
import type { MeasureFormat, QueryResponse } from "@dux/core";
import { toChartData, toSeriesData, type ChartDim, type ChartRow, type Interaction } from "../charts/ChartKit";
import { downloadIcon, eraserIcon, funnelIcon } from "../glyphs";
import { TYPE_LABEL, VISUALS } from "../visuals";
import type { DataBodyProps, StaticBodyProps, VisualDef } from "../visuals/types";

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

/** Live-refresh status light, rightmost in the header. It exists only while the
 *  dashboard refreshes on an interval: a muted static ring at rest that spins
 *  while a fetch is in flight. Refreshing a visual in place is what keeps the
 *  body from being veiled by the loading overlay on every tick. */
export function RefreshDot({ el }: { el: DashElement }) {
  const def = VISUALS[el.type];
  // Which query to watch is a registry fact — never a type branch here. Most
  // visuals ride the shared element query; the ones that don't declare a probe.
  const probe = def?.useFetching ?? (def?.data ? useElementFetching : undefined);
  if (!probe) return null;
  // Keyed on the type: re-typing an element in edit mode swaps the probe hook,
  // which is only safe across a remount.
  return <RefreshProbe key={el.type} el={el} use={probe} />;
}

function useElementFetching(el: DashElement): boolean {
  return useElementData(el).loading;
}

function RefreshProbe({ el, use }: { el: DashElement; use: (el: DashElement) => boolean }) {
  const live = useRefreshInterval(el.id) !== false;
  const fetching = use(el);
  if (!live) return null;
  return (
    <span
      className={`${styles.refreshDot}${fetching ? ` ${styles.refreshDotOn}` : ""}`}
      title={fetching ? "Refreshing…" : "Live refresh on"}
    />
  );
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
function FloatingActions({ el, def }: { el: DashElement; def: VisualDef }) {
  const controls = useDocStore((s) => s.doc?.controls);
  const live = useRefreshInterval(el.id) !== false;
  const titleShown = el.title?.show !== false && !!el.title?.text;
  if (titleShown) return null;
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
        <RefreshDot el={el} />
      </ControlsBoundary>
    </div>
  );
}

/** Nothing an element renders may reach the React root: an escaping error
 *  unmounts the whole SPA, leaving a blank page instead of one broken element.
 *  fallback turns the caught error into what takes its place. */
export class ElementBoundary extends Component<
  { fallback: (error: Error) => ReactNode; children: ReactNode },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  render() {
    const { error } = this.state;
    return error ? this.props.fallback(error) : this.props.children;
  }
}

/** Header/floating controls run the same data hooks as the body (the CSV button
 *  and the refresh dot both read the element query), so they can throw for the
 *  same reasons — and they hang outside the body's boundary. Chrome that fails
 *  is worth less than the visual it decorates: it disappears silently. */
export function ControlsBoundary({ el, children }: { el: DashElement; children: ReactNode }) {
  // Keyed on the type, like the body: re-typing an element in edit mode swaps
  // which hooks run, and that is only safe across a remount.
  return (
    <ElementBoundary key={el.type} fallback={() => null}>
      {children}
    </ElementBoundary>
  );
}

export default function ElementBody({ el }: { el: DashElement }) {
  return (
    <ElementBoundary
      key={el.type}
      fallback={(error) => <Placeholder type={el.type} note={displayMessage(error).split("\n")[0]} />}
    >
      <TypedBody el={el} />
    </ElementBoundary>
  );
}

/** Registry dispatch: query-backed visuals go through the shared data
 *  pipeline, the rest render straight from the element. */
function TypedBody({ el }: { el: DashElement }) {
  const def = VISUALS[el.type];
  if (!def) return <Placeholder type={el.type} note="Unknown type" />;
  if (def.data) return <DataBody el={el} def={def} />;

  const Body = def.Body as ComponentType<StaticBodyProps>;
  if (!def.controls) return <Body el={el} />;
  // Controls need a positioned wrapper to float in (the map).
  return (
    <div className={styles.dataWrap}>
      <Body el={el} />
      <FloatingActions el={el} def={def} />
    </div>
  );
}

function Placeholder({ type, note }: { type: ElementType; note: string }) {
  const def = VISUALS[type];
  return (
    <div className={styles.placeholder}>
      <span className={styles.icon}>{def?.icon}</span>
      <span>{TYPE_LABEL[type] ?? type}</span>
      <span className={styles.hint}>{note}</span>
    </div>
  );
}

// ─── Query-backed bodies ─────────────────────────────────────────────────────

function DataBody({ el, def }: { el: DashElement; def: VisualDef }) {
  const { dux, data, error, loading } = useElementData(el);
  const formats = useFormats();
  const doc = useDocStore((s) => s.doc);
  // One resolved theme covers both the series palette and the chrome a
  // visual draws itself (selected-marker outlines).
  const theme = useResolvedTheme(doc);

  if (!dux) {
    return (
      <div className={styles.placeholder}>
        <span className={styles.icon}>{def.icon}</span>
        <span className={styles.hint}>Add fields in the settings pane</span>
      </div>
    );
  }

  return (
    <div className={styles.dataWrap}>
      {data && (
        <DataViz el={el} def={def} data={data} formats={formats} palette={theme.palette} textColor={theme.text} />
      )}
      {data && <FloatingActions el={el} def={def} />}
      {/* First load only. A refresh keeps the previous result on screen
          (keepPreviousData) — veiling it every interval would make a live
          dashboard flash. The header dot carries the in-flight state. */}
      {loading && !data && (
        <div className={styles.overlay}>
          <div className={styles.spinner} />
        </div>
      )}
      {error && <ErrorOverlay error={error} />}
    </div>
  );
}

/** Query failure overlay; positioned duxd errors show their source location. */
function ErrorOverlay({ error }: { error: Error }) {
  const message = displayMessage(error);
  const pos =
    error instanceof QueryFailedError && error.line > 0
      ? ` (line ${error.line}, col ${error.column})`
      : "";
  return (
    <div className={styles.overlayError} title={message}>
      <span>⚠</span>
      <span className={styles.overlayErrorText}>
        {message}
        {pos}
      </span>
    </div>
  );
}

interface VizProps {
  el: DashElement;
  def: VisualDef;
  data: QueryResponse;
  formats: Record<string, MeasureFormat>;
  palette: string[];
  textColor: string;
}

/** The shared half of every query visual: column inference, empty-row drop,
 *  chart pivot and cross-filter wiring, done once for whichever body renders. */
function DataViz({ el, def, data: rawData, formats, palette, textColor }: VizProps) {
  const spec = def.data!;
  const { dims, metrics } = splitElementFields(el);
  const viz: VizSettings = el.viz ?? {};

  // Cross-filter interaction: clicking a mark filters the other visuals
  // (view mode + builder queries only; raw queries lack per-dim tables).
  const mode = useUiStore((s) => s.mode);
  const crossSel = useUiStore((s) => s.crossFilters[el.id]);
  const toggleCrossMark = useUiStore((s) => s.toggleCrossMark);

  // Raw mode has no builder fields, so charts treat the first result column
  // as x and the rest as series.
  const raw = el.query?.mode === "raw";
  const dimCols = raw ? rawData.columns.slice(0, 1) : dims.map((f) => f.name);
  const metricCols = raw
    ? rawData.columns.slice(1)
    : metrics.map((f) => f.name).filter((n) => rawData.columns.includes(n));

  const dimTables = useMemo(() => Object.fromEntries(dims.map((f) => [f.name, f.table])), [dims]);
  const selectedKeys = useMemo(() => new Set((crossSel ?? []).map(markKey)), [crossSel]);
  const onMarkClick =
    mode === "view" && !raw && dims.length > 0
      ? (d: ChartDim[], additive: boolean) => {
          if (d.length > 0) toggleCrossMark(el.id, { dims: d }, additive);
        }
      : undefined;

  // Axis items with no data (all metrics null) are hidden unless toggled on.
  const data = dropEmptyRows(rawData, metricCols, spec.dropEmpty === false || (viz.showEmpty ?? false));

  // Series split (the "Series by" well): the first Values measure fans out
  // into one series per value of the chosen dim.
  const splitBy = elementSeriesSplit(el);
  const dimKey = dimCols.join("\u0000");
  const metricKey = metricCols.join("\u0000");
  const sortKey = (el.query?.sort ?? []).map((s) => `${s.field}:${s.dir ?? "asc"}`).join("|");
  const pivot = useMemo(() => {
    const none = { rows: [] as ChartRow[], keys: [] as string[], fmts: formats, seriesDim: undefined };
    if (!spec.chart) return none;
    if (splitBy && metricCols.length > 0) {
      const metric = metricCols[0];
      const { data: sData, series } = toSeriesData(
        data,
        dimCols.filter((c) => c !== splitBy),
        splitBy,
        metric,
        dimTables
      );
      // The query ran at (x, series) grain, so ORDER BY sorted single cells
      // and TOPN would have kept single segments. Both belong to the stack:
      // redo them here over the series totals (buildElementDux drops TOPN
      // from a split query so every group is available to trim).
      const total = (r: ChartRow) => series.reduce((t, s) => t + (Number(r[s]) || 0), 0);
      let rows = sData;
      const topN = el.query?.topN ?? 0;
      if (topN > 0 && rows.length > topN) {
        const kept = new Set([...rows].sort((a, b) => total(b) - total(a)).slice(0, topN));
        rows = rows.filter((r) => kept.has(r)); // trim, but keep the query's order
      }
      const sort = el.query?.sort?.[0];
      if (sort?.field === metric) {
        rows = [...rows].sort((a, b) => (sort.dir === "desc" ? total(b) - total(a) : total(a) - total(b)));
      }
      // Every series carries the split measure — its format applies to all.
      const sFormats: Record<string, MeasureFormat> = {};
      if (formats[metric]) for (const s of series) sFormats[s] = formats[metric];
      return {
        rows,
        keys: series,
        fmts: sFormats,
        seriesDim: { table: dimTables[splitBy] ?? "", column: splitBy },
      };
    }
    return { rows: toChartData(data, dimCols, metricCols, dimTables), keys: metricCols, fmts: formats, seriesDim: undefined };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [spec.chart, data, splitBy, dimKey, metricKey, sortKey, el.query?.topN, dimTables, formats]);

  const interaction: Interaction = { onMarkClick, selectedKeys, seriesDim: pivot.seriesDim };
  const Body = def.Body as ComponentType<DataBodyProps>;
  return (
    <Body
      el={el}
      data={data}
      formats={pivot.fmts}
      palette={palette}
      textColor={textColor}
      viz={viz}
      rows={pivot.rows}
      keys={pivot.keys}
      metricCols={metricCols}
      legend={viz.legend ?? pivot.keys.length > 1}
      interaction={interaction}
      meta={def}
    />
  );
}
