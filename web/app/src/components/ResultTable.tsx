import { useEffect, useMemo, useRef, useState } from "react";
import type { QueryResponse, MeasureFormat } from "@dux/core";
import { duxClient as client, QueryFailedError, formatValue } from "@dux/core";
import { compareCellsDir } from "../compare";
import styles from "./ResultTable.module.css";

type SortDir = "asc" | "desc";

function isNumericCell(v: string | number | null): boolean {
  if (v === null) return false;
  if (typeof v === "number") return !isNaN(v);
  const n = Number(v);
  return !isNaN(n) && String(v).trim() !== "";
}

export default function ResultTable(props: {
  query: string;
  includeEmpty: boolean;
  /** Called with a getter that returns the displayed dataset as
   *  Excel-pasteable TSV (header + filtered/sorted rows), or null if empty. */
  registerCopyProvider?: (fn: () => string | null) => void;
  /** Reports the current query failure (null when the query succeeds or clears). */
  onQueryError?: (err: QueryFailedError | null) => void;
}) {
  const [data, setData] = useState<QueryResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sortCol, setSortCol] = useState(-1);
  const [sortDir, setSortDir] = useState<SortDir>("desc");
  // Measure display formats by measure name, loaded once per query run.
  const [formats, setFormats] = useState<Record<string, MeasureFormat>>({});

  /** Set of column indices where every non-null value is numeric. */
  const numericCols = useMemo(() => {
    if (!data) return new Set<number>();
    const s = new Set<number>();
    for (let ci = 0; ci < data.columns.length; ci++) {
      const vals = data.rows.map((r) => r[ci]).filter((v) => v !== null);
      if (vals.length > 0 && vals.every(isNumericCell)) s.add(ci);
    }
    return s;
  }, [data]);

  const onQueryErrorRef = useRef(props.onQueryError);
  onQueryErrorRef.current = props.onQueryError;

  useEffect(() => {
    if (!props.query) {
      setData(null);
      setError(null);
      onQueryErrorRef.current?.(null);
      return;
    }
    let stale = false;
    const debounceTimer = setTimeout(async () => {
      setLoading(true);
      setError(null);
      try {
        const [json, measures] = await Promise.all([
          client.executeQuery(props.query),
          client.fetchMeasures().catch(() => []),
        ]);
        if (stale) return;
        const fmts: Record<string, MeasureFormat> = {};
        for (const m of measures) if (m.format) fmts[m.name] = m.format;
        setFormats(fmts);
        setData(json);
        onQueryErrorRef.current?.(null);
      } catch (err) {
        if (stale) return;
        if (err instanceof QueryFailedError) {
          const pos = err.line > 0 ? ` (line ${err.line}, column ${err.column})` : "";
          setError(`${err.stage ? err.stage + ": " : ""}${err.message}${pos}`);
          onQueryErrorRef.current?.(err);
        } else {
          setError(err instanceof Error ? err.message : String(err));
          onQueryErrorRef.current?.(null);
        }
        setData(null);
      } finally {
        if (!stale) setLoading(false);
      }
    }, 300);
    return () => {
      stale = true;
      clearTimeout(debounceTimer);
    };
  }, [props.query]);

  // Default sort for each new result: DESC by the first numeric column.
  useEffect(() => {
    const first = numericCols.values().next();
    setSortCol(first.done ? -1 : first.value);
    setSortDir("desc");
  }, [numericCols]);

  function handleHeaderClick(ci: number) {
    if (sortCol === ci) {
      setSortDir((d) => (d === "desc" ? "asc" : "desc"));
    } else {
      setSortCol(ci);
      setSortDir("desc");
    }
  }

  /** Column indices holding measures — result columns whose name matches a
   *  quoted alias in the query ("Name", expr pairs in SUMMARIZECOLUMNS). */
  const measureCols = useMemo(() => {
    if (!data) return new Set<number>();
    const aliases = new Set<string>();
    for (const m of props.query.matchAll(/"([^"]*)"/g)) aliases.add(m[1]);
    const s = new Set<number>();
    data.columns.forEach((c, i) => {
      if (aliases.has(c)) s.add(i);
    });
    return s;
  }, [data, props.query]);

  /** Rows after the Include Empty filter: unless the toggle is on, drop rows
   *  where every measure column is null (empty combinations). */
  const visibleRows = useMemo(() => {
    if (!data) return [];
    if (props.includeEmpty || measureCols.size === 0) return data.rows;
    const idxs = [...measureCols];
    return data.rows.filter((row) => idxs.some((i) => row[i] !== null));
  }, [data, props.includeEmpty, measureCols]);

  const sortedRows = useMemo(() => {
    if (sortCol === -1) return visibleRows;
    return [...visibleRows].sort((a, b) => compareCellsDir(a[sortCol], b[sortCol], sortDir));
  }, [visibleRows, sortCol, sortDir]);

  // Expose the displayed dataset as TSV so the Copy Results button
  // (in the query toolbar) can put it on the clipboard for Excel.
  const copyStateRef = useRef({ data, sortedRows });
  copyStateRef.current = { data, sortedRows };
  useEffect(() => {
    props.registerCopyProvider?.(() => {
      const { data: d, sortedRows: rows } = copyStateRef.current;
      if (!d) return null;
      // Tabs/newlines inside a cell would break the grid shape on paste.
      const esc = (v: string | number | null) =>
        v === null ? "" : String(v).replace(/[\t\r\n]+/g, " ");
      const lines = [d.columns.map(esc).join("\t")];
      for (const row of rows) lines.push(row.map(esc).join("\t"));
      return lines.join("\r\n");
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  /** Render a cell without changing the raw value used by sort/copy. */
  function renderCell(cell: string | number | null, ci: number) {
    if (cell === null) return <span className={styles.null}>null</span>;
    if (!data) return String(cell);
    const fmt = measureCols.has(ci) ? formats[data.columns[ci]] : undefined;
    return formatValue(cell, fmt);
  }

  return (
    <div className={styles.panel}>
      <div className={styles.header}>
        Results
        {loading && <span className={styles.spinner} title="Loading…" />}
        {data && (
          <span className={styles.rowCount}>
            {visibleRows.length} row{visibleRows.length !== 1 ? "s" : ""}
            {visibleRows.length !== data.rows.length ? ` (${data.rows.length - visibleRows.length} empty hidden)` : ""}
          </span>
        )}
      </div>

      {error && <div className={styles.errorBanner}>{error}</div>}

      {data && !loading && (
        <div className={styles.tableWrap}>
          <table className={styles.table}>
            <thead>
              <tr>
                {data.columns.map((col, ci) => (
                  <th
                    key={ci}
                    className={[
                      styles.sortable,
                      sortCol === ci ? styles.sortActive : "",
                      numericCols.has(ci) ? styles.numeric : "",
                    ].filter(Boolean).join(" ")}
                    onClick={() => handleHeaderClick(ci)}
                  >
                    <span className={styles.thInner}>
                      {col}
                      <span className={styles.sortArrow}>
                        {sortCol === ci ? (sortDir === "desc" ? "↓" : "↑") : "↕"}
                      </span>
                    </span>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {sortedRows.map((row, ri) => (
                <tr key={ri}>
                  {row.map((cell, ci) => (
                    <td key={ci} className={numericCols.has(ci) ? styles.numeric : undefined}>
                      {renderCell(cell, ci)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
