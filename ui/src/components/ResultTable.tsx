import { createSignal, createEffect, createMemo, For, Show } from "solid-js";
import type { Component } from "solid-js";
import styles from "./ResultTable.module.css";

interface QueryResponse {
  columns: string[];
  rows: (string | number | null)[][];
}

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

/** Return the index of the first column where all non-null values are numeric (number or numeric string). */
function firstNumericCol(data: QueryResponse): number {
  for (let ci = 0; ci < data.columns.length; ci++) {
    const vals = data.rows.map((r) => r[ci]).filter((v) => v !== null);
    if (vals.length > 0 && vals.every(isNumeric)) return ci;
  }
  return -1;
}

const ResultTable: Component<{ query: string }> = (props) => {
  const [data, setData] = createSignal<QueryResponse | null>(null);
  const [loading, setLoading] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [sortCol, setSortCol] = createSignal<number>(-1);
  const [sortDir, setSortDir] = createSignal<SortDir>("desc");

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
        const res = await fetch("/query", {
          method: "POST",
          headers: { "Content-Type": "text/plain" },
          body: q,
        });
        const text = await res.text();
        if (!res.ok) {
          setError(text || `Error ${res.status}`);
          setData(null);
        } else {
          const json = JSON.parse(text) as QueryResponse;
          // Default: sort DESC by first numeric column
          const firstNum = firstNumericCol(json);
          setSortCol(firstNum);
          setSortDir("desc");
          setData(json);
        }
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

  const sortedRows = createMemo(() => {
    const d = data();
    if (!d) return [];
    const ci = sortCol();
    if (ci === -1) return d.rows;
    const dir = sortDir() === "asc" ? 1 : -1;
    return [...d.rows].sort((a, b) => dir * cmpCells(a[ci], b[ci]));
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
            {data()!.rows.length} row{data()!.rows.length !== 1 ? "s" : ""}
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
                      classList={{ [styles.sortActive]: sortCol() === ci() }}
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
                      {(cell) => <td>{cell === null ? <span class={styles.null}>null</span> : String(cell)}</td>}
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
