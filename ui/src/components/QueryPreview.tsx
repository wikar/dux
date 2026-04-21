import type { Component } from "solid-js";
import styles from "./QueryPreview.module.css";

const QueryPreview: Component<{ query: string; onQueryChange: (q: string) => void }> = (props) => {
  return (
    <div class={styles.panel}>
      <div class={styles.header}>Generated Query</div>
      <textarea
        class={styles.code}
        value={props.query}
        placeholder="// Drop fields to build a query"
        spellcheck={false}
        onInput={(e) => props.onQueryChange(e.currentTarget.value)}
      />
    </div>
  );
};

export default QueryPreview;
