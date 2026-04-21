import type { Component } from "solid-js";
import styles from "./QueryPreview.module.css";

const QueryPreview: Component<{
  query: string;
  isDirty: boolean;
  onQueryChange: (q: string) => void;
  onRun: () => void;
}> = (props) => {
  function onKeyDown(e: KeyboardEvent) {
    if (e.key !== "Tab") return;
    e.preventDefault();
    const el = e.currentTarget as HTMLTextAreaElement;
    const start = el.selectionStart;
    const end = el.selectionEnd;
    const next = el.value.slice(0, start) + "    " + el.value.slice(end);
    props.onQueryChange(next);
    // Restore cursor after the inserted spaces.
    requestAnimationFrame(() => {
      el.selectionStart = el.selectionEnd = start + 4;
    });
  }

  return (
    <div class={styles.panel}>
      <div class={styles.header}>Query</div>
      <textarea
        class={styles.code}
        value={props.query}
        placeholder="// Drop fields to build a query"
        spellcheck={false}
        onInput={(e) => props.onQueryChange(e.currentTarget.value)}
        onKeyDown={onKeyDown}
      />
      <div class={styles.toolbar}>
          <button
            class={`${styles.runBtn} ${props.isDirty ? styles.runBtnActive : ""}`}
            title={props.isDirty ? "Run query" : "Query up to date"}
            onClick={props.onRun}
          >
            <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
              <path d="M3 2.5l10 5.5-10 5.5V2.5z" />
            </svg>
            Run
          </button>
      </div>
    </div>
  );
};

export default QueryPreview;
