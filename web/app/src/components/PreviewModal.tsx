import { useEffect, useState } from "react";
import type { QueryResponse } from "@dux/core";
import { duxClient as client } from "@dux/core";
import Modal, { modalStyles } from "./Modal";
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

  return (
    <Modal
      title={`Preview — ${props.tableName}`}
      onClose={props.onClose}
      width={900}
      footer={
        <button className={modalStyles.btn} onClick={props.onClose}>
          Close
        </button>
      }
    >
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
    </Modal>
  );
}
