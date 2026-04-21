import { createMemo } from "solid-js";
import type { Component } from "solid-js";
import hljs from "highlight.js/lib/core";
import duxLanguage from "../utils/duxLanguage";
import styles from "./QueryPreview.module.css";

hljs.registerLanguage("dux", duxLanguage);

const QueryPreview: Component<{
  query: string;
  isDirty: boolean;
  onQueryChange: (q: string) => void;
  onRun: () => void;
}> = (props) => {
  let highlightEl: HTMLPreElement | undefined;

  const highlighted = createMemo(() =>
    props.query
      ? hljs.highlight(props.query, { language: "dux" }).value
      : ""
  );

  function onKeyDown(e: KeyboardEvent) {
    if (e.key !== "Tab") return;
    e.preventDefault();
    const el = e.currentTarget as HTMLTextAreaElement;
    const start = el.selectionStart;
    const end = el.selectionEnd;
    const next = el.value.slice(0, start) + "    " + el.value.slice(end);
    props.onQueryChange(next);
    requestAnimationFrame(() => {
      el.selectionStart = el.selectionEnd = start + 4;
    });
  }

  function syncScroll(e: Event) {
    if (!highlightEl) return;
    const ta = e.currentTarget as HTMLTextAreaElement;
    highlightEl.scrollTop = ta.scrollTop;
    highlightEl.scrollLeft = ta.scrollLeft;
  }

  return (
    <div class={styles.panel}>
      <div class={styles.header}>Query</div>
      <div class={styles.codeWrapper}>
        <pre
          ref={highlightEl}
          class={styles.highlight}
          aria-hidden="true"
          innerHTML={highlighted()}
        />
        <textarea
          class={styles.code}
          value={props.query}
          placeholder="// Drop fields to build a query"
          spellcheck={false}
          onInput={(e) => props.onQueryChange(e.currentTarget.value)}
          onKeyDown={onKeyDown}
          onScroll={syncScroll}
        />
      </div>
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
