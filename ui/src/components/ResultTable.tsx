import { createSignal, createEffect, For, Show } from "solid-js";
import type { Component } from "solid-js";
import styles from "./ResultTable.module.css";

interface QueryResponse {
  columns: string[];
  rows: (string | number | null)[][];
}

const ResultTable: Component<{ query: string }> = (props) => {
  const [data, setData] = createSignal<QueryResponse | null>(null);
  const [loading, setLoading] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);

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
                  {(col) => <th>{col}</th>}
                </For>
              </tr>
            </thead>
            <tbody>
              <For each={data()!.rows}>
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
