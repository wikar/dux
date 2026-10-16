import type { ReactNode } from "react";
import { QueryFailedError } from "@dux/core";
import { displayMessage } from "../message";
import styles from "./ElementBody.module.css";

/** A refresh leaves previous children visible; loading is only for first load. */
export default function VisualSurface({ children, actions, loading, error }: {
  children: ReactNode;
  actions?: ReactNode;
  loading?: boolean;
  error?: Error | null;
}) {
  return (
    <div className={styles.dataWrap}>
      {children}
      {actions}
      {loading && <div className={styles.overlay}><div className={styles.spinner} /></div>}
      {error && <ErrorOverlay error={error} />}
    </div>
  );
}

/** Query failure overlay; positioned duxd errors show their source location. */
function ErrorOverlay({ error }: { error: Error }) {
  const message = displayMessage(error);
  const pos =
    error instanceof QueryFailedError && error.line > 0
      ? ` (line ${error.line}, col ${error.column})`
      : "";
  return (
    <div className={styles.overlayError} title={message}>
      <span>⚠</span>
      <span className={styles.overlayErrorText}>
        {message}
        {pos}
      </span>
    </div>
  );
}
