import { createSignal, onMount, onCleanup, For, Show } from "solid-js";
import type { Component } from "solid-js";
import treeStyles from "./SchemaTree.module.css";
import styles from "./PreviewModal.module.css";

interface QueryResponse {
  columns: string[];
  rows: (string | number | null)[][];
}

const PreviewModal: Component<{
  tableName: string;
  onClose: () => void;
}> = (props) => {
  const [data, setData] = createSignal<QueryResponse | null>(null);
  const [loading, setLoading] = createSignal(true);
  const [error, setError] = createSignal<string | null>(null);

  onMount(() => {
    async function load() {
      try {
        const res = await fetch("/query", {
          method: "POST",
          headers: { "Content-Type": "text/plain" },
          body: `EVALUATE ${props.tableName}`,
        });
        const text = await res.text();
        if (!res.ok) {
          setError(text || `Error ${res.status}`);
          return;
        }
        const json = JSON.parse(text) as QueryResponse;
        setData({ columns: json.columns, rows: json.rows.slice(0, 10) });
      } catch (e) {
        setError(String(e));
      } finally {
        setLoading(false);
      }
    }
    load();

    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") props.onClose();
    }
    document.addEventListener("keydown", onKey);
    onCleanup(() => document.removeEventListener("keydown", onKey));
  });

  return (
    <div
      class={treeStyles.modalOverlay}
      onClick={(e) => {
        if (e.target === e.currentTarget) props.onClose();
      }}
    >
      <div class={styles.previewModal}>
        <div class={treeStyles.modalHeader}>
          <span>Preview — {props.tableName}</span>
          <button class={treeStyles.modalClose} onClick={props.onClose}>
            ✕
          </button>
        </div>

        <div class={styles.previewBody}>
          <Show when={loading()}>
            <div class={styles.previewStatus}>Loading…</div>
          </Show>
          <Show when={error()}>
            <div class={styles.previewError}>{error()}</div>
          </Show>
          <Show when={data() && !loading()}>
            <div class={styles.previewTableWrap}>
              <table class={styles.previewTable}>
                <thead>
                  <tr>
                    <For each={data()!.columns}>{(col) => <th>{col}</th>}</For>
                  </tr>
                </thead>
                <tbody>
                  <For each={data()!.rows}>
                    {(row) => (
                      <tr>
                        <For each={row}>
                          {(cell) => (
                            <td>
                              {cell === null ? (
                                <span class={styles.null}>null</span>
                              ) : (
                                String(cell)
                              )}
                            </td>
                          )}
                        </For>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </div>
            <div class={styles.previewNote}>Showing up to 10 rows</div>
          </Show>
        </div>

        <div class={treeStyles.modalFooter}>
          <button class={treeStyles.modalBtn} onClick={props.onClose}>
            Close
          </button>
        </div>
      </div>
    </div>
  );
};

export default PreviewModal;
