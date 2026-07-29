// Flat result table: header sorting plus a virtualized row window, so an
// unbounded result stays responsive.
import { useMemo, useRef, useState } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import styles from "../components/ElementBody.module.css";
import { compareCellsDir } from "../../compare";
import { splitElementFields } from "../data";
import { S, stroke } from "../glyphs";
import { markKey, updateElement, useUiStore } from "../store";
import type { DashElement } from "../types";
import type { ChartDim } from "../charts/ChartKit";
import { DATA_CONTROLS, SHOW_EMPTY } from "./common";
import type { DataBodyProps, VisualDef } from "./types";
import { formatValue } from "@dux/core";
import type { MeasureFormat, QueryResponse } from "@dux/core";

const table: VisualDef = {
  type: "table",
  label: "Table",
  icon: (
    <svg {...S}>
      <rect x="2" y="3" width="14" height="12" {...stroke} />
      <line x1="2" y1="7" x2="16" y2="7" {...stroke} />
      <line x1="9" y1="3" x2="9" y2="15" {...stroke} />
    </svg>
  ),
  size: { w: 420, h: 280 },
  controls: DATA_CONTROLS,
  data: { wells: [{ id: "fields", label: "Columns" }] },
  options: [SHOW_EMPTY],
  Body: ({ el, data, formats }: DataBodyProps) => <TableBody el={el} data={data} formats={formats} />,
};

export default table;

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
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    handleHeaderClick(ci);
                  }
                }}
                tabIndex={0}
                aria-sort={active?.col === ci ? (active.dir === "asc" ? "ascending" : "descending") : "none"}
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
                onKeyDown={canCross ? (e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    toggleCrossMark(el.id, { dims: rowDims(r) }, e.ctrlKey || e.metaKey);
                  }
                } : undefined}
                tabIndex={canCross ? 0 : undefined}
                aria-selected={canCross ? selected : undefined}
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
