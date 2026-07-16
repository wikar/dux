import { useEffect, useState } from "react";
import type { QueryResponse } from "@dux/core";
import { duxClient as client } from "@dux/core";
import treeStyles from "./SchemaTree.module.css";
import styles from "./PreviewModal.module.css";

export default function PreviewModal(props: {
  tableName: string;
  onClose: () => void;
}) {
  const [data, setData] = useState<QueryResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    (async () => {
      try {
        const json = await client.executeQuery(`EVALUATE ${props.tableName}`);
        if (alive) setData({ columns: json.columns, rows: json.rows.slice(0, 10) });
      } catch (e) {
        if (alive) setError(e instanceof Error ? e.message : String(e));
      } finally {
        if (alive) setLoading(false);
      }
    })();
    return () => { alive = false; };
  }, [props.tableName]);

  useEffect(() => {
    function onKey(e: globalThis.KeyboardEvent) {
      if (e.key === "Escape") props.onClose();
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div
      className={treeStyles.modalOverlay}
      onClick={(e) => {
        if (e.target === e.currentTarget) props.onClose();
      }}
    >
      <div className={styles.previewModal}>
        <div className={treeStyles.modalHeader}>
          <span>Preview — {props.tableName}</span>
          <button className={treeStyles.modalClose} onClick={props.onClose}>
            ✕
          </button>
        </div>

        <div className={styles.previewBody}>
          {loading && (
            <div className={styles.previewStatus}>
              <span className={styles.spinner} />
            </div>
          )}
          {error && <div className={styles.previewError}>{error}</div>}
          {data && !loading && (
            <>
              <div className={styles.previewTableWrap}>
                <table className={styles.previewTable}>
                  <thead>
                    <tr>
                      {data.columns.map((col) => <th key={col}>{col}</th>)}
                    </tr>
                  </thead>
                  <tbody>
                    {data.rows.map((row, ri) => (
                      <tr key={ri}>
                        {row.map((cell, ci) => (
                          <td key={ci}>
                            {cell === null ? (
                              <span className={styles.null}>null</span>
                            ) : (
                              String(cell)
                            )}
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className={styles.previewNote}>Showing up to 10 rows</div>
            </>
          )}
        </div>

        <div className={treeStyles.modalFooter}>
          <button className={treeStyles.modalBtn} onClick={props.onClose}>
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
