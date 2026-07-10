import { createResource } from "solid-js";
import type { Component } from "solid-js";
import DuxEditor from "./DuxEditor";
import styles from "./QueryPreview.module.css";
import { useDuxClient } from "../clientContext";

const QueryPreview: Component<{
  query: string;
  isDirty: boolean;
  onQueryChange: (q: string) => void;
  onRun: () => void;
  includeEmpty: boolean;
  onToggleIncludeEmpty: () => void;
}> = (props) => {
  const client = useDuxClient();
  const [schema] = createResource(() => client.fetchSchema());

  return (
    <div class={styles.panel}>
      <div class={styles.header}>Query</div>
      <DuxEditor
        class={styles.codeWrapper}
        value={props.query}
        onChange={props.onQueryChange}
        schema={schema()}
        placeholder="// Drop fields to build a query"
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
        <label
          class={styles.toggle}
          title={props.includeEmpty ? "Showing rows where all measures are null" : "Hiding rows where all measures are null"}
        >
          <input
            type="checkbox"
            class={styles.toggleInput}
            checked={props.includeEmpty}
            onChange={props.onToggleIncludeEmpty}
          />
          <span class={styles.toggleTrack}>
            <span class={styles.toggleKnob} />
          </span>
          Include Empty
        </label>
      </div>
    </div>
  );
};

export default QueryPreview;
