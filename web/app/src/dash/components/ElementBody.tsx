import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import styles from "./ElementBody.module.css";
import { imageUrl } from "../api";
import {
  dropEmptyRows,
  splitElementFields,
  useElementData,
  useFormats,
  usePalette,
} from "../data";
import { TYPE_LABEL } from "../docOps";
import { useDocStore } from "../store";
import type { DashElement, ImageFit } from "../types";
import { QUERY_TYPES } from "../types";
import { formatValue, QueryFailedError } from "@dux/core";
import type { MeasureFormat, QueryResponse } from "@dux/core";
import { BarChartViz, ComboChartViz, LineChartViz, toChartData } from "../charts/ChartKit";
import SlicerBody from "./SlicerBody";
import { typeIcon } from "./typeIcons";

/** Per-type element body. Query-backed types render live data (M4); area,
 *  donut, and pivot arrive in M6; slicers in M5. */
export default function ElementBody({ el }: { el: DashElement }) {
  if (el.type === "text") {
    return (
      <div className={styles.markdown}>
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{el.text?.markdown ?? ""}</ReactMarkdown>
      </div>
    );
  }
  if (el.type === "image") return <ImageBody el={el} />;
  if (el.type === "slicer") return <SlicerBody el={el} />;
  if (el.type === "area" || el.type === "donut" || el.type === "pivot") {
    return <Placeholder el={el} note="arrives in M6" />;
  }
  if (QUERY_TYPES.has(el.type)) return <DataBody el={el} />;
  return <Placeholder el={el} note="unknown type" />;
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

const IMAGE_FIT: Record<ImageFit, React.CSSProperties["objectFit"]> = {
  contain: "contain",
  cover: "cover",
  fill: "fill",
};

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
      style={{ objectFit: IMAGE_FIT[el.image?.fit ?? "contain"] }}
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

  return (
    <div className={styles.dataWrap}>
      {data && <DataViz el={el} data={data} formats={formats} palette={palette} />}
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

  // Raw mode has no builder fields, so charts treat the first result column
  // as x and the rest as series.
  const raw = el.query?.mode === "raw";
  const dimCols = raw ? rawData.columns.slice(0, 1) : dims.map((f) => f.name);
  const metricCols = raw
    ? rawData.columns.slice(1)
    : metrics.map((f) => f.name).filter((n) => rawData.columns.includes(n));

  if (el.type === "kpi") return <KpiBody data={rawData} metricCols={metricCols} formats={formats} />;

  // Axis items with no data (all metrics null) are hidden unless toggled on.
  const data = dropEmptyRows(rawData, metricCols, viz.showEmpty ?? false);

  if (el.type === "table") return <TableBody data={data} formats={formats} />;

  const chartData = toChartData(data, dimCols, metricCols);
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

// ─── Table ───────────────────────────────────────────────────────────────────

// Plain table capped at 500 rows for M4; TanStack Table + Virtual arrives
// with the pivot in M6.
const TABLE_ROW_CAP = 500;

function TableBody({
  data,
  formats,
}: {
  data: QueryResponse;
  formats: Record<string, MeasureFormat>;
}) {
  const rows = data.rows.slice(0, TABLE_ROW_CAP);
  return (
    <div className={styles.tableWrap}>
      <table className={styles.table}>
        <thead>
          <tr>
            {data.columns.map((c) => (
              <th key={c} className={formats[c] ? styles.num : undefined}>
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i}>
              {r.map((v, j) => {
                const col = data.columns[j];
                const fmt = formats[col];
                return (
                  <td key={j} className={fmt || typeof v === "number" ? styles.num : undefined}>
                    {v === null || v === undefined ? "" : fmt ? formatValue(v, fmt) : String(v)}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
      {data.rows.length > TABLE_ROW_CAP && (
        <div className={styles.tableCap}>
          showing {TABLE_ROW_CAP} of {data.rows.length} rows
        </div>
      )}
    </div>
  );
}
