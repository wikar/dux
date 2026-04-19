import type { Component } from "solid-js";
import styles from "./QueryPreview.module.css";

const QueryPreview: Component<{ query: string }> = (props) => {
  return (
    <div class={styles.panel}>
      <div class={styles.header}>Generated Query</div>
      <pre class={styles.code}>
        {props.query || <span class={styles.empty}>// Drop fields to build a query</span>}
      </pre>
    </div>
  );
};

export default QueryPreview;
