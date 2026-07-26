import styles from "./TreeCaret.module.css";

/** Expand/collapse marker shared by the schema pane and the dashboard picker —
 *  same small bordered +/− square the pivot table uses for its row groups.
 *  A span, not a button: every caller already sits inside a clickable header. */
export default function TreeCaret({ open }: { open: boolean }) {
  return (
    <span className={styles.caret} aria-hidden="true">
      {open ? "−" : "+"}
    </span>
  );
}
