// Element body shell: chrome shared by every visual (error boundary, header
// controls, loading/error overlays) plus the one query pipeline that feeds the
// query-backed bodies. What each visual *is* lives in its ../visuals module.
import { Component, useMemo, useRef, useState, type ComponentType, type ReactNode } from "react";
import { createPortal } from "react-dom";
import styles from "./ElementBody.module.css";
import { downloadCsv } from "../csv";
import { dropEmptyRows, splitElementFields, useAffectingFilters, useFormats, useResolvedTheme } from "../data";
import { useElementData } from "../elementQuery";
import { markKey, useDocStore, useUiStore } from "../store";
import type { DashElement, ElementType, VizSettings } from "../types";
import { QueryFailedError } from "@dux/core";
import type { MeasureFormat, QueryResponse } from "@dux/core";
import { toChartData, toSeriesData, type ChartDim, type ChartRow, type Interaction } from "../charts/ChartKit";
import { downloadIcon, funnelIcon } from "../glyphs";
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
  const titleShown = el.title?.show !== false && !!el.title?.text;
  if (titleShown) return null;
  const funnel = !!def.controls?.funnel && controls?.funnel !== false;
  const csv = !!def.controls?.csv && controls?.csv !== false;
  if (!funnel && !csv) return null;
  return (
    <div className={styles.floatingActions}>
      {funnel && <FunnelButton el={el} floating />}
      {csv && <CsvButton el={el} className={styles.exportBtn} />}
    </div>
  );
}

/** A throwing body must not reach the React root: an escaping error unmounts the
 *  whole SPA, leaving a blank page instead of one broken element. Keyed on the
 *  type so re-typing an element in edit mode gives it a fresh attempt. */
class BodyBoundary extends Component<{ el: DashElement; children: ReactNode }, { error: Error | null }> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;
    return <Placeholder type={this.props.el.type} note={error.message.split("\n")[0]} />;
  }
}

export default function ElementBody({ el }: { el: DashElement }) {
  return (
    <BodyBoundary key={el.type} el={el}>
      <TypedBody el={el} />
    </BodyBoundary>
  );
}

/** Registry dispatch: query-backed visuals go through the shared data
 *  pipeline, the rest render straight from the element. */
function TypedBody({ el }: { el: DashElement }) {
  const def = VISUALS[el.type];
  if (!def) return <Placeholder type={el.type} note="unknown type" />;
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
      {loading && (
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
  const pos =
    error instanceof QueryFailedError && error.line > 0
      ? ` (line ${error.line}, col ${error.column})`
      : "";
  return (
    <div className={styles.overlayError} title={error.message}>
      <span>⚠</span>
      <span className={styles.overlayErrorText}>
        {error.message}
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
  const splitBy = spec.seriesSplit && !raw ? dims.find((f) => f.name === viz.series)?.name : undefined;
  const dimKey = dimCols.join("\u0000");
  const metricKey = metricCols.join("\u0000");
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
      // Every series carries the split measure — its format applies to all.
      const sFormats: Record<string, MeasureFormat> = {};
      if (formats[metric]) for (const s of series) sFormats[s] = formats[metric];
      return {
        rows: sData,
        keys: series,
        fmts: sFormats,
        seriesDim: { table: dimTables[splitBy] ?? "", column: splitBy },
      };
    }
    return { rows: toChartData(data, dimCols, metricCols, dimTables), keys: metricCols, fmts: formats, seriesDim: undefined };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [spec.chart, data, splitBy, dimKey, metricKey, dimTables, formats]);

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
