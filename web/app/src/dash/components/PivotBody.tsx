// Pivot/matrix: rows × columns × measures. The element's flat query over all
// dims is the main fetch (passed in); subtotal and grand-total rows come from
// the extra per-level queries in usePivotTotals — measures aren't additive,
// so totals can never be summed client-side. Row rendering is virtualized.
import { useMemo, useRef } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import styles from "./ElementBody.module.css";
import { pivotParts, totalsKey, usePivotTotals } from "../data";
import type { DashElement } from "../types";
import { formatValue } from "@dux/core";
import type { MeasureFormat, QueryResponse } from "@dux/core";

const SEP = "\u0000";
const JOIN = "\u0001";
const ROW_H = 24;
const HEADER_W = 170;
const CELL_W = 92;
/** Column-dim combinations explode fast; the surplus is reported below. */
const COL_CAP = 60;

/** Numeric compare when both sides parse as numbers, else locale strings. */
function cmpVals(a: string, b: string): number {
  const na = Number(a);
  const nb = Number(b);
  if (a !== "" && b !== "" && !Number.isNaN(na) && !Number.isNaN(nb)) return na - nb;
  return a.localeCompare(b);
}

/** NULL/empty dim values need a visible label. */
const lbl = (s: string) => (s === "" ? "(blank)" : s);

function cmpTuples(a: string[], b: string[]): number {
  for (let i = 0; i < Math.min(a.length, b.length); i++) {
    const c = cmpVals(a[i], b[i]);
    if (c !== 0) return c;
  }
  return a.length - b.length;
}

type CellValues = (string | number | null)[];

/** rowKey + colKey → metric values, from one query result. */
function buildLookup(
  res: QueryResponse | undefined,
  rowNames: string[],
  colNames: string[],
  metricNames: string[]
): Map<string, CellValues> {
  const map = new Map<string, CellValues>();
  if (!res) return map;
  const rIdx = rowNames.map((n) => res.columns.indexOf(n));
  const cIdx = colNames.map((n) => res.columns.indexOf(n));
  const mIdx = metricNames.map((n) => res.columns.indexOf(n));
  for (const row of res.rows) {
    const rk = rIdx.map((i) => (i >= 0 ? String(row[i] ?? "") : "")).join(SEP);
    const ck = cIdx.map((i) => (i >= 0 ? String(row[i] ?? "") : "")).join(SEP);
    map.set(rk + JOIN + ck, mIdx.map((i) => (i >= 0 ? (row[i] as string | number | null) : null)));
  }
  return map;
}

interface DisplayRow {
  kind: "group" | "leaf" | "subtotal" | "grand";
  /** Indent level of the row header. */
  indent: number;
  label: string;
  /** Lookup key: the joined row-dim tuple (full for leaves, prefix for totals). */
  key: string;
  /** Row-dim prefix length — selects the totals query for subtotal/grand rows. */
  len: number;
}

interface Props {
  el: DashElement;
  /** Main flat result (already showEmpty-filtered by the caller). */
  data: QueryResponse;
  formats: Record<string, MeasureFormat>;
}

export default function PivotBody({ el, data, formats }: Props) {
  const raw = el.query?.mode === "raw";
  const viz = el.viz ?? {};
  const { byKey, loading: totalsLoading } = usePivotTotals(el);

  // Raw mode can't know the row/col split — first column becomes the row dim,
  // the rest are values; totals are off (no queries to derive them from).
  const { rowNames, colNames, metricNames } = useMemo(() => {
    if (raw) {
      return {
        rowNames: data.columns.slice(0, 1),
        colNames: [] as string[],
        metricNames: data.columns.slice(1),
      };
    }
    const parts = pivotParts(el);
    return {
      rowNames: parts.rowDims.map((f) => f.name),
      colNames: parts.colDims.map((f) => f.name),
      metricNames: parts.metrics.map((f) => f.name).filter((n) => data.columns.includes(n)),
    };
  }, [raw, el, data.columns]);

  const R = rowNames.length;
  const hasCols = colNames.length > 0;
  const subtotalsOn = !raw && (viz.subtotals ?? true) && R > 1;
  const grandOn = !raw && (viz.grandTotal ?? true) && R > 0;
  const totalColOn = !raw && (viz.totalCol ?? true) && hasCols;

  // Distinct column combinations, sorted; the axis is capped for sanity.
  const colAxisFull = useMemo(() => {
    if (!hasCols) return [[]] as string[][];
    const cIdx = colNames.map((n) => data.columns.indexOf(n));
    const seen = new Map<string, string[]>();
    for (const row of data.rows) {
      const t = cIdx.map((i) => (i >= 0 ? String(row[i] ?? "") : ""));
      seen.set(t.join(SEP), t);
    }
    return [...seen.values()].sort(cmpTuples);
  }, [data, colNames, hasCols]);
  const colAxis = colAxisFull.slice(0, COL_CAP);
  const colsDropped = colAxisFull.length - colAxis.length;

  // Row structure: sorted unique row tuples → group headers, leaves,
  // subtotal rows on group close, one grand-total row at the end.
  const displayRows = useMemo(() => {
    const rIdx = rowNames.map((n) => data.columns.indexOf(n));
    const seen = new Map<string, string[]>();
    for (const row of data.rows) {
      const t = rIdx.map((i) => (i >= 0 ? String(row[i] ?? "") : ""));
      seen.set(t.join(SEP), t);
    }
    const tuples = [...seen.values()].sort(cmpTuples);

    const out: DisplayRow[] = [];
    if (R === 0) {
      out.push({ kind: "leaf", indent: 0, label: "Total", key: "", len: 0 });
      return out;
    }
    const subtotal = (l: number, t: string[]): DisplayRow => ({
      kind: "subtotal",
      indent: l - 1,
      label: `${lbl(t[l - 1])} Total`,
      key: t.slice(0, l).join(SEP),
      len: l,
    });
    let prev: string[] | null = null;
    for (const t of tuples) {
      let common = 0;
      if (prev) while (common < R - 1 && prev[common] === t[common]) common++;
      if (prev && subtotalsOn) for (let l = R - 1; l > common; l--) out.push(subtotal(l, prev));
      for (let l = common + 1; l <= R - 1; l++) {
        out.push({ kind: "group", indent: l - 1, label: lbl(t[l - 1]), key: t.slice(0, l).join(SEP), len: l });
      }
      out.push({ kind: "leaf", indent: R > 1 ? R - 1 : 0, label: lbl(t[R - 1] ?? ""), key: t.join(SEP), len: R });
      prev = t;
    }
    if (prev && subtotalsOn) for (let l = R - 1; l >= 1; l--) out.push(subtotal(l, prev));
    if (grandOn) out.push({ kind: "grand", indent: 0, label: "Total", key: "", len: 0 });
    return out;
  }, [data, rowNames, R, subtotalsOn, grandOn]);

  // One lookup per result: main + each totals level (with/without cols).
  const lookups = useMemo(() => {
    const m = new Map<string, Map<string, CellValues>>();
    m.set("main", buildLookup(data, rowNames, colNames, metricNames));
    for (const [k, res] of Object.entries(byKey)) {
      const [lStr, kind] = k.split("|");
      const l = Number(lStr);
      m.set(k, buildLookup(res, rowNames.slice(0, l), kind === "c" ? colNames : [], metricNames));
    }
    return m;
  }, [data, byKey, rowNames, colNames, metricNames]);

  const cellsFor = (dr: DisplayRow): Map<string, CellValues> | undefined =>
    dr.kind === "leaf" ? lookups.get("main") : lookups.get(totalsKey(dr.len, true));
  const totalColFor = (dr: DisplayRow): Map<string, CellValues> | undefined =>
    lookups.get(totalsKey(dr.len, false));

  const scrollRef = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: displayRows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_H,
    overscan: 12,
  });

  const nMetrics = metricNames.length;
  const leafCols = colAxis.length * nMetrics + (totalColOn ? nMetrics : 0);
  const totalWidth = HEADER_W + leafCols * CELL_W;
  const twoHeaderRows = hasCols && nMetrics > 1;

  const cell = (values: CellValues | undefined, mi: number) => {
    const v = values?.[mi];
    if (v === null || v === undefined) return "";
    return formatValue(v, formats[metricNames[mi]]) || String(v);
  };

  const rowClass: Record<DisplayRow["kind"], string> = {
    group: styles.pvGroup,
    leaf: "",
    subtotal: styles.pvSubtotal,
    grand: styles.pvGrand,
  };

  if (nMetrics === 0) {
    return (
      <div className={styles.placeholder}>
        <span className={styles.hint}>Drop a measure into Values</span>
      </div>
    );
  }

  return (
    <div className={styles.tableWrap} ref={scrollRef}>
      <div style={{ minWidth: totalWidth }}>
        <div className={styles.pvHeader}>
          {hasCols && (
            <div className={styles.pvRow}>
              <div className={styles.pvCorner} style={{ width: HEADER_W }} />
              {colAxis.map((t, i) => (
                <div
                  key={i}
                  className={styles.pvColHead}
                  style={{ width: CELL_W * nMetrics }}
                  title={t.map(lbl).join(" · ")}
                >
                  {t.map(lbl).join(" · ")}
                </div>
              ))}
              {totalColOn && (
                <div className={styles.pvColHead} style={{ width: CELL_W * nMetrics }}>
                  Total
                </div>
              )}
            </div>
          )}
          {(twoHeaderRows || !hasCols) && (
            <div className={styles.pvRow}>
              <div className={styles.pvCorner} style={{ width: HEADER_W }}>
                {!hasCols ? rowNames.join(" · ") : ""}
              </div>
              {colAxis.map((_, i) =>
                metricNames.map((mn) => (
                  <div key={`${i}:${mn}`} className={styles.pvMetricHead} style={{ width: CELL_W }} title={mn}>
                    {mn}
                  </div>
                ))
              )}
              {totalColOn &&
                metricNames.map((mn) => (
                  <div key={`t:${mn}`} className={styles.pvMetricHead} style={{ width: CELL_W }} title={mn}>
                    {mn}
                  </div>
                ))}
            </div>
          )}
        </div>
        <div style={{ position: "relative", height: virtualizer.getTotalSize() }}>
          {virtualizer.getVirtualItems().map((vi) => {
            const dr = displayRows[vi.index];
            const cells = dr.kind === "group" ? undefined : cellsFor(dr);
            const totals = dr.kind === "group" ? undefined : totalColFor(dr);
            return (
              <div
                key={vi.key}
                className={`${styles.pvRow} ${rowClass[dr.kind]}`}
                style={{ position: "absolute", top: 0, left: 0, right: 0, height: ROW_H, transform: `translateY(${vi.start}px)` }}
              >
                <div
                  className={styles.pvRowHead}
                  style={{ width: HEADER_W, paddingLeft: 8 + dr.indent * 14 }}
                  title={dr.label}
                >
                  {dr.label}
                </div>
                {colAxis.map((t, i) => {
                  const values = cells?.get(dr.key + JOIN + t.join(SEP));
                  return metricNames.map((_, mi) => (
                    <div key={`${i}:${mi}`} className={styles.pvCell} style={{ width: CELL_W }}>
                      {cell(values, mi)}
                    </div>
                  ));
                })}
                {totalColOn &&
                  metricNames.map((_, mi) => (
                    <div key={`t:${mi}`} className={`${styles.pvCell} ${styles.pvTotalCol}`} style={{ width: CELL_W }}>
                      {cell(totals?.get(dr.key + JOIN), mi)}
                    </div>
                  ))}
              </div>
            );
          })}
        </div>
        {(colsDropped > 0 || totalsLoading) && (
          <div className={styles.tableCap}>
            {colsDropped > 0 && `showing ${colAxis.length} of ${colAxisFull.length} columns`}
            {colsDropped > 0 && totalsLoading && " · "}
            {totalsLoading && "loading totals…"}
          </div>
        )}
      </div>
    </div>
  );
}
