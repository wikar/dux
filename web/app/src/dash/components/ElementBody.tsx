import { Component, useMemo, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useVirtualizer } from "@tanstack/react-virtual";
import styles from "./ElementBody.module.css";
import { compareCellsDir } from "../../compare";
import { imageUrl } from "../api";
import { downloadCsv } from "../csv";
import {
  dropEmptyRows,
  splitElementFields,
  useAffectingFilters,
  useElementData,
  useFormats,
  usePalette,
} from "../data";
import { TYPE_LABEL, updateElement } from "../docOps";
import { markKey, useDocStore, useUiStore } from "../store";
import type { DashElement } from "../types";
import { QUERY_TYPES } from "../types";
import { formatValue, QueryFailedError } from "@dux/core";
import type { MeasureFormat, QueryResponse } from "@dux/core";
import {
  AreaChartViz,
  BarChartViz,
  ComboChartViz,
  DonutChartViz,
  LineChartViz,
  toChartData,
  toSeriesData,
  type ChartDim,
  type Interaction,
} from "../charts/ChartKit";
import PivotBody from "./PivotBody";
import SlicerBody from "./SlicerBody";
import MapBody from "./MapBody";
import { downloadIcon, funnelIcon, typeIcon } from "./typeIcons";

/** CSV download in the title bar (rendered by ElementView for query types).
 *  useElementData hits the cache — the body already runs the same query. */
export function TitleCsvButton({ el, className }: { el: DashElement; className: string }) {
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
    return <Placeholder el={this.props.el} note={error.message.split("\n")[0]} />;
  }
}

export default function ElementBody({ el }: { el: DashElement }) {
  return (
    <BodyBoundary key={el.type} el={el}>
      <TypedBody el={el} />
    </BodyBoundary>
  );
}

/** Per-type element body: query-backed types render live data, the rest are
 *  static content (text/image) or controls (slicer). */
function TypedBody({ el }: { el: DashElement }) {
  if (el.type === "text") {
    return (
      <div className={styles.markdown}>
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{el.text?.markdown ?? ""}</ReactMarkdown>
      </div>
    );
  }
  if (el.type === "image") return <ImageBody el={el} />;
  if (el.type === "slicer") return <SlicerBody el={el} />;
  if (el.type === "map") return <MapElementBody el={el} />;
  if (QUERY_TYPES.has(el.type)) return <DataBody el={el} />;
  return <Placeholder el={el} note="unknown type" />;
}

function MapElementBody({ el }: { el: DashElement }) {
  const showFunnel = useDocStore((s) => s.doc?.controls?.funnel) !== false;
  const titleless = el.title?.show === false || !el.title?.text;
  return (
    <div className={styles.dataWrap}>
      <MapBody el={el} />
      {showFunnel && titleless && (
        <div className={styles.floatingActions}>
          <FunnelButton el={el} floating />
        </div>
      )}
    </div>
  );
}

function Placeholder({ el, note }: { el: DashElement; note: string }) {
  return (
    <div className={styles.placeholder}>
      <span className={styles.icon}>{typeIcon(el.type)}</span>
      <span>{TYPE_LABEL[el.type]}</span>
      <span className={styles.hint}>{note}</span>
    </div>
  );
}

// ─── Image ───────────────────────────────────────────────────────────────────

function ImageBody({ el }: { el: DashElement }) {
  const url = el.image?.url?.trim();
  if (!url) {
    return (
      <div className={styles.placeholder}>
        <span className={styles.icon}>{typeIcon("image")}</span>
        <span className={styles.hint}>Set an image URL in the settings pane</span>
      </div>
    );
  }
  return (
    <img
      className={styles.image}
      src={imageUrl(url)}
      alt={el.title?.text ?? ""}
      style={{ objectFit: el.image?.fit ?? "contain" }}
      draggable={false}
    />
  );
}

// ─── Query-backed bodies ─────────────────────────────────────────────────────

function DataBody({ el }: { el: DashElement }) {
  const { dux, data, error, loading } = useElementData(el);
  const formats = useFormats();
  const doc = useDocStore((s) => s.doc);
  const palette = usePalette(doc);

  if (!dux) {
    return (
      <div className={styles.placeholder}>
        <span className={styles.icon}>{typeIcon(el.type)}</span>
        <span className={styles.hint}>Add fields in the settings pane</span>
      </div>
    );
  }

  // The CSV button lives in the title bar; elements without one keep a
  // floating hover button so export stays reachable.
  const titleShown = el.title?.show !== false && !!el.title?.text;
  const showFunnel = doc?.controls?.funnel !== false;
  const showCsv = el.type !== "kpi" && doc?.controls?.csv !== false;

  return (
    <div className={styles.dataWrap}>
      {data && <DataViz el={el} data={data} formats={formats} palette={palette} />}
      {data && !titleShown && (
        <div className={styles.floatingActions}>
          {showFunnel && <FunnelButton el={el} floating />}
          {showCsv && (
            <button
              className={styles.exportBtn}
              title="Download CSV"
              onPointerDown={(e) => e.stopPropagation()}
              onClick={() => downloadCsv(data, el.title?.text || el.id)}
            >
              {downloadIcon}
            </button>
          )}
        </div>
      )}
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
  data: QueryResponse;
  formats: Record<string, MeasureFormat>;
  palette: string[];
}

function DataViz({ el, data: rawData, formats, palette }: VizProps) {
  const { dims, metrics } = splitElementFields(el);
  const viz = el.viz ?? {};

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
  const interaction: Interaction = { onMarkClick, selectedKeys };

  if (el.type === "kpi") return <KpiBody data={rawData} metricCols={metricCols} formats={formats} />;

  // Axis items with no data (all metrics null) are hidden unless toggled on.
  const data = dropEmptyRows(rawData, metricCols, viz.showEmpty ?? false);

  if (el.type === "table") return <TableBody el={el} data={data} formats={formats} />;
  if (el.type === "pivot") return <PivotBody el={el} data={data} formats={formats} />;

  // Series split (the "Series by" well): the first Values measure fans out
  // into one series per value of the chosen dim (bar / line / area).
  const splitBy =
    !raw && (el.type === "bar" || el.type === "line" || el.type === "area")
      ? dims.find((f) => f.name === viz.series)?.name
      : undefined;
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
    const legend = viz.legend ?? series.length > 1;
    // A clicked segment carries the series dim value too.
    const splitInteraction: Interaction = {
      ...interaction,
      seriesDim: { table: dimTables[splitBy] ?? "", column: splitBy },
    };
    if (el.type === "bar") {
      return (
        <BarChartViz
          data={sData}
          series={series}
          palette={palette}
          formats={sFormats}
          orientation={viz.orientation}
          stacked={viz.stacked}
          legend={legend}
          interaction={splitInteraction}
        />
      );
    }
    if (el.type === "line") {
      return (
        <LineChartViz
          data={sData}
          left={series}
          right={[]}
          palette={palette}
          formats={sFormats}
          legend={legend}
          interaction={splitInteraction}
        />
      );
    }
    return (
      <AreaChartViz
        data={sData}
        series={series}
        stacked={viz.stacked}
        palette={palette}
        formats={sFormats}
        legend={legend}
        interaction={splitInteraction}
      />
    );
  }

  const chartData = toChartData(data, dimCols, metricCols, dimTables);
  if (el.type === "bar") {
    return (
      <BarChartViz
        data={chartData}
        series={metricCols}
        palette={palette}
        formats={formats}
        orientation={viz.orientation}
        stacked={viz.stacked}
        legend={viz.legend ?? metricCols.length > 1}
        interaction={interaction}
      />
    );
  }
  if (el.type === "line") {
    const y2 = (viz.y2 ?? []).filter((n) => metricCols.includes(n));
    const left = metricCols.filter((n) => !y2.includes(n));
    return (
      <LineChartViz
        data={chartData}
        left={left}
        right={y2}
        palette={palette}
        formats={formats}
        legend={viz.legend ?? metricCols.length > 1}
        interaction={interaction}
      />
    );
  }
  if (el.type === "combo") {
    const lines = (viz.lines ?? []).filter((n) => metricCols.includes(n));
    const bars = metricCols.filter((n) => !lines.includes(n));
    return (
      <ComboChartViz
        data={chartData}
        bars={bars}
        lines={lines}
        lineY2={viz.lineY2 ?? true}
        palette={palette}
        formats={formats}
        legend={viz.legend ?? metricCols.length > 1}
        interaction={interaction}
      />
    );
  }
  if (el.type === "area") {
    return (
      <AreaChartViz
        data={chartData}
        series={metricCols}
        stacked={viz.stacked}
        palette={palette}
        formats={formats}
        legend={viz.legend ?? metricCols.length > 1}
        interaction={interaction}
      />
    );
  }
  if (el.type === "donut") {
    // A donut slices its first metric across the axis categories.
    const metric = metricCols[0];
    if (!metric) return null;
    return (
      <DonutChartViz
        data={chartData}
        metric={metric}
        palette={palette}
        formats={formats}
        legend={viz.legend ?? true}
        interaction={interaction}
      />
    );
  }
  return null;
}

// ─── KPI ─────────────────────────────────────────────────────────────────────

function KpiBody({
  data,
  metricCols,
  formats,
}: {
  data: QueryResponse;
  metricCols: string[];
  formats: Record<string, MeasureFormat>;
}) {
  const col = metricCols[0] ?? data.columns[data.columns.length - 1];
  const idx = data.columns.indexOf(col);
  const value = idx >= 0 && data.rows.length > 0 ? data.rows[0][idx] : null;
  return (
    <div className={styles.kpi}>
      <span className={styles.kpiValue}>
        {value === null || value === undefined ? "—" : formatValue(value, formats[col])}
      </span>
      <span className={styles.kpiLabel}>{col}</span>
    </div>
  );
}

// ─── Table (useState sorting + Virtual for unbounded rows) ───────────────────

type ResultRow = readonly (string | number | null)[];
type SortDir = "asc" | "desc";

const TABLE_ROW_H = 25;
const COL_MEASURE_CAP = 2000; // rows sampled when sizing columns
const COL_PAD = 22; // horizontal cell padding (10+10) plus a little slack
const COL_MIN = 44;
const COL_MAX = 460;

let measureCtx: CanvasRenderingContext2D | null = null;

/** Column widths (px) measured from the header plus a sample of the formatted
 *  body, so `table-layout: fixed` yields stable, content-accurate columns that
 *  a virtualized row window can't reflow — the sticky header stops shifting as
 *  you scroll. Measuring the full data (not just the visible rows) is what
 *  keeps numeric columns from ever truncating. */
function measureColWidths(
  data: QueryResponse,
  formats: Record<string, MeasureFormat>,
  fontFamily: string
): number[] {
  const cv = (measureCtx ??= document.createElement("canvas").getContext("2d"));
  const n = data.columns.length;
  const w = new Array<number>(n).fill(COL_MIN);
  if (!cv) return w.map(() => 120); // no 2D context — fall back to a fixed width
  // Headers are semibold and carry a sort caret; leave room for it.
  cv.font = `600 12px ${fontFamily}`;
  for (let i = 0; i < n; i++) w[i] = cv.measureText(data.columns[i]).width + 18;
  // Body sample at regular weight.
  cv.font = `12px ${fontFamily}`;
  const rows = data.rows;
  const step = rows.length > COL_MEASURE_CAP ? Math.ceil(rows.length / COL_MEASURE_CAP) : 1;
  for (let r = 0; r < rows.length; r += step) {
    const row = rows[r];
    for (let i = 0; i < n; i++) {
      const v = row[i];
      if (v === null || v === undefined) continue;
      const fmt = formats[data.columns[i]];
      const tw = cv.measureText(formatValue(v, fmt)).width;
      if (tw > w[i]) w[i] = tw;
    }
  }
  return w.map((x) => Math.max(COL_MIN, Math.min(COL_MAX, Math.ceil(x) + COL_PAD)));
}

function TableBody({
  el,
  data,
  formats,
}: {
  el: DashElement;
  data: QueryResponse;
  formats: Record<string, MeasureFormat>;
}) {
  const editing = useUiStore((s) => s.mode === "edit");
  const crossSel = useUiStore((s) => s.crossFilters[el.id]);
  const toggleCrossMark = useUiStore((s) => s.toggleCrossMark);

  // Cross-filter: a clicked data row selects its dim-column tuple (view mode).
  const dimIdx = useMemo(
    () =>
      splitElementFields(el)
        .dims.map((d) => ({ table: d.table, column: d.name, i: data.columns.indexOf(d.name) }))
        .filter((d) => d.i >= 0),
    [el, data.columns]
  );
  const selectedKeys = useMemo(() => new Set((crossSel ?? []).map(markKey)), [crossSel]);
  const canCross = !editing && dimIdx.length > 0;
  const rowDims = (r: ResultRow): ChartDim[] =>
    dimIdx.map((d) => ({ table: d.table, column: d.column, value: (r[d.i] ?? "") as string | number }));

  // The persisted sort lives in el.query.sort, so the header, the Settings
  // "Sort by" control, and (for builder queries) the server-side ORDER BY all
  // stay in sync. In view mode we keep an ephemeral local override instead, so
  // a viewer sorting a shared/wall dashboard never dirties the saved document.
  const saved = el.query?.sort?.[0];
  const savedCol = saved ? data.columns.indexOf(saved.field) : -1;
  const [localSort, setLocalSort] = useState<{ col: number; dir: SortDir } | null>(null);

  const active =
    !editing && localSort
      ? localSort
      : savedCol >= 0
      ? { col: savedCol, dir: (saved?.dir ?? "desc") as SortDir }
      : null;

  function handleHeaderClick(ci: number) {
    const dir: SortDir = active?.col === ci && active.dir === "desc" ? "asc" : "desc";
    if (editing) {
      updateElement(el.id, (x) => ({
        ...x,
        query: { ...(x.query ?? { mode: "builder" }), sort: [{ field: data.columns[ci], dir }] },
      }));
    } else {
      setLocalSort({ col: ci, dir });
    }
  }

  const rows = useMemo(() => {
    const base = data.rows as ResultRow[];
    if (!active) return base;
    const { col, dir } = active;
    return [...base].sort((a, b) => compareCellsDir(a[col], b[col], dir));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data.rows, active?.col, active?.dir]);

  const wrapRef = useRef<HTMLDivElement>(null);

  // Fixed column widths measured from the full result (see measureColWidths).
  const colWidths = useMemo(() => {
    const ff = wrapRef.current ? getComputedStyle(wrapRef.current).fontFamily : "sans-serif";
    return measureColWidths(data, formats, ff);
  }, [data, formats]);
  const tableWidth = colWidths.reduce((a, b) => a + b, 0);

  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => wrapRef.current,
    estimateSize: () => TABLE_ROW_H,
    overscan: 12,
  });
  const items = virtualizer.getVirtualItems();
  const padTop = items.length > 0 ? items[0].start : 0;
  const padBottom = items.length > 0 ? virtualizer.getTotalSize() - items[items.length - 1].end : 0;

  return (
    <div className={styles.tableWrap} ref={wrapRef}>
      <table className={styles.table} style={{ width: tableWidth, minWidth: "100%" }}>
        <colgroup>
          {colWidths.map((w, i) => (
            <col key={i} style={{ width: w }} />
          ))}
        </colgroup>
        <thead>
          <tr>
            {data.columns.map((c, ci) => (
              <th
                key={ci}
                className={formats[c] ? styles.num : undefined}
                // Stop the canvas drag/select from swallowing the click in edit
                // mode (same guard the pivot caret uses).
                onPointerDown={(e) => e.stopPropagation()}
                onClick={() => handleHeaderClick(ci)}
                title="Sort"
              >
                {c}
                {active?.col === ci ? (active.dir === "asc" ? " ▲" : " ▼") : ""}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {padTop > 0 && (
            <tr>
              <td colSpan={data.columns.length} style={{ height: padTop, padding: 0, border: "none" }} />
            </tr>
          )}
          {items.map((vi) => {
            const r = rows[vi.index];
            const selected = canCross && selectedKeys.size > 0 && selectedKeys.has(markKey({ dims: rowDims(r) }));
            return (
              <tr
                key={vi.index}
                style={{ height: TABLE_ROW_H, cursor: canCross ? "pointer" : undefined }}
                className={selected ? styles.rowSelected : undefined}
                onClick={canCross ? (e) => toggleCrossMark(el.id, { dims: rowDims(r) }, e.ctrlKey || e.metaKey) : undefined}
              >
                {r.map((v, j) => {
                  const col = data.columns[j];
                  const fmt = formats[col];
                  return (
                    <td key={j} className={fmt || typeof v === "number" ? styles.num : undefined}>
                      {v === null || v === undefined ? "" : formatValue(v, fmt)}
                    </td>
                  );
                })}
              </tr>
            );
          })}
          {padBottom > 0 && (
            <tr>
              <td colSpan={data.columns.length} style={{ height: padBottom, padding: 0, border: "none" }} />
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}
