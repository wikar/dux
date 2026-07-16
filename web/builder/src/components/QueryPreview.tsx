import { useRef, useState } from "react";
import DuxEditor from "./DuxEditor";
import styles from "./QueryPreview.module.css";
import { duxClient as client } from "@dux/core";
import type { QueryFailedError } from "@dux/core";
import { useFetch } from "../hooks";

export default function QueryPreview(props: {
  query: string;
  isDirty: boolean;
  onQueryChange: (q: string) => void;
  onRun: () => void;
  includeEmpty: boolean;
  onToggleIncludeEmpty: () => void;
  /** Copies the displayed results to the clipboard; resolves false when there is nothing to copy. */
  onCopyResults: () => Promise<boolean>;
  /** Current query failure, marked at its source position in the editor. */
  error?: QueryFailedError | null;
}) {
  const { data: schema } = useFetch(() => client.fetchSchema());
  const [copied, setCopied] = useState(false);
  const copiedTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  async function handleCopy() {
    if (!(await props.onCopyResults())) return;
    setCopied(true);
    clearTimeout(copiedTimer.current);
    copiedTimer.current = setTimeout(() => setCopied(false), 1500);
  }

  return (
    <div className={styles.panel}>
      <div className={styles.header}>Query</div>
      <DuxEditor
        className={styles.codeWrapper}
        value={props.query}
        onChange={props.onQueryChange}
        schema={schema}
        placeholder="// Drop fields to build a query"
        error={props.error}
      />
      <div className={styles.toolbar}>
        <button
          className={`${styles.runBtn} ${props.isDirty ? styles.runBtnActive : ""}`}
          title={props.isDirty ? "Run query" : "Query up to date"}
          onClick={props.onRun}
        >
          <svg viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
            <path d="M3 2.5l10 5.5-10 5.5V2.5z" />
          </svg>
          Run
        </button>
        <button
          className={`${styles.runBtn} ${copied ? styles.copiedBtn : ""}`}
          title="Copy results to clipboard (paste into Excel)"
          onClick={handleCopy}
        >
          {copied ? (
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M2.5 8.5l4 4 7-8" />
            </svg>
          ) : (
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <rect x="5.5" y="5.5" width="8" height="8" rx="1.5" />
              <path d="M10.5 5.5v-2a1 1 0 0 0-1-1h-6a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2" />
            </svg>
          )}
          {copied ? "Copied" : "Copy Results"}
        </button>
        <label
          className={styles.toggle}
          title={props.includeEmpty ? "Showing rows where all measures are null" : "Hiding rows where all measures are null"}
        >
          <input
            type="checkbox"
            className={styles.toggleInput}
            checked={props.includeEmpty}
            onChange={props.onToggleIncludeEmpty}
          />
          <span className={styles.toggleTrack}>
            <span className={styles.toggleKnob} />
          </span>
          Include Empty
        </label>
      </div>
    </div>
  );
}
