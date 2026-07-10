import { createSignal, createEffect, createMemo, For, Show } from "solid-js";
import type { Component } from "solid-js";
import type { QueryResponse } from "dux-client";
import styles from "./ResultTable.module.css";
import { useDuxClient } from "../clientContext";

type SortDir = "asc" | "desc";

function isNumeric(v: string | number | null): boolean {
  if (v === null) return false;
  if (typeof v === "number") return !isNaN(v);
  const n = Number(v);
  return !isNaN(n) && String(v).trim() !== "";
}

/** Compare two cell values for sorting. Numbers sort numerically; everything else lexicographically. */
function cmpCells(a: string | number | null, b: string | number | null): number {
  if (a === null && b === null) return 0;
  if (a === null) return 1;
  if (b === null) return -1;
  const na = typeof a === "number" ? a : Number(a);
  const nb = typeof b === "number" ? b : Number(b);
  if (!isNaN(na) && !isNaN(nb)) return na - nb;
  return String(a).localeCompare(String(b), undefined, { numeric: true, sensitivity: "base" });
}

const ResultTable: Component<{ query: string; includeEmpty: boolean }> = (props) => {
  const client = useDuxClient();
  const [data, setData] = createSignal<QueryResponse | null>(null);
  const [loading, setLoading] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [sortCol, setSortCol] = createSignal<number>(-1);
  const [sortDir, setSortDir] = createSignal<SortDir>("desc");

  /** Set of column indices where every non-null value is numeric (ascending order). */
  const numericCols = createMemo(() => {
    const d = data();
    if (!d) return new Set<number>();
    const s = new Set<number>();
    for (let ci = 0; ci < d.columns.length; ci++) {
      const vals = d.rows.map((r) => r[ci]).filter((v) => v !== null);
      if (vals.length > 0 && vals.every(isNumeric)) s.add(ci);
    }
    return s;
  });

  let debounceTimer: ReturnType<typeof setTimeout>;

  createEffect(() => {
    const q = props.query;
    clearTimeout(debounceTimer);
    if (!q) {
      setData(null);
      setError(null);
      return;
    }
    debounceTimer = setTimeout(async () => {
      setLoading(true);
      setError(null);
      try {
        const json = await client.executeQuery(q);
        setData(json);
        // Default: sort DESC by first numeric column
        const first = numericCols().values().next();
        setSortCol(first.done ? -1 : first.value);
        setSortDir("desc");
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err));
        setData(null);
      } finally {
        setLoading(false);
      }
    }, 300);
  });

  function handleHeaderClick(ci: number) {
    if (sortCol() === ci) {
      setSortDir((d) => (d === "desc" ? "asc" : "desc"));
    } else {
      setSortCol(ci);
      setSortDir("desc");
    }
  }

  /** Column indices holding measures — result columns whose name matches a
   *  quoted alias in the query ("Name", expr pairs in SUMMARIZECOLUMNS). */
  const measureCols = createMemo(() => {
    const d = data();
    if (!d) return new Set<number>();
    const aliases = new Set<string>();
    for (const m of props.query.matchAll(/"([^"]*)"/g)) aliases.add(m[1]);
    const s = new Set<number>();
    d.columns.forEach((c, i) => {
      if (aliases.has(c)) s.add(i);
    });
    return s;
  });

  /** Rows after the Include Empty filter: unless the toggle is on, drop rows
   *  where every measure column is null (empty combinations). */
  const visibleRows = createMemo(() => {
    const d = data();
    if (!d) return [];
    const mc = measureCols();
    if (props.includeEmpty || mc.size === 0) return d.rows;
    const idxs = [...mc];
    return d.rows.filter((row) => idxs.some((i) => row[i] !== null));
  });

  const sortedRows = createMemo(() => {
    const rows = visibleRows();
    const ci = sortCol();
    if (ci === -1) return rows;
    const dir = sortDir() === "asc" ? 1 : -1;
    return [...rows].sort((a, b) => dir * cmpCells(a[ci], b[ci]));
  });

  return (
    <div class={styles.panel}>
      <div class={styles.header}>
        Results
        <Show when={loading()}>
          <span class={styles.spinner} title="Loading…" />
        </Show>
        <Show when={data()}>
          <span class={styles.rowCount}>
            {visibleRows().length} row{visibleRows().length !== 1 ? "s" : ""}
            {visibleRows().length !== data()!.rows.length ? ` (${data()!.rows.length - visibleRows().length} empty hidden)` : ""}
          </span>
        </Show>
      </div>

      <Show when={error()}>
        <div class={styles.errorBanner}>{error()}</div>
      </Show>

      <Show when={data() && !loading()}>
        <div class={styles.tableWrap}>
          <table class={styles.table}>
            <thead>
              <tr>
                <For each={data()!.columns}>
                  {(col, ci) => (
                    <th
                      class={styles.sortable}
                      classList={{ [styles.sortActive]: sortCol() === ci(), [styles.numeric]: numericCols().has(ci()) }}
                      onClick={() => handleHeaderClick(ci())}
                    >
                      <span class={styles.thInner}>
                        {col}
                        <span class={styles.sortArrow}>
                          {sortCol() === ci() ? (sortDir() === "desc" ? "↓" : "↑") : "↕"}
                        </span>
                      </span>
                    </th>
                  )}
                </For>
              </tr>
            </thead>
            <tbody>
              <For each={sortedRows()}>
                {(row) => (
                  <tr>
                    <For each={row}>
                      {(cell, ci) => <td classList={{ [styles.numeric]: numericCols().has(ci()) }}>{cell === null ? <span class={styles.null}>null</span> : String(cell)}</td>}
                    </For>
                  </tr>
                )}
              </For>
            </tbody>
          </table>
        </div>
      </Show>
    </div>
  );
};

export default ResultTable;
