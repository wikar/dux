import { useQuery } from "@tanstack/react-query";
import styles from "./Home.module.css";
import { listDashboards } from "../api";
import { gotoDashboard } from "../actions";

/** Landing view at /dash/ — pick an existing dashboard or start from the
 *  "+ New" button in the top bar. */
export default function Home() {
  const { data: entries, isLoading, error } = useQuery({
    queryKey: ["dashboards"],
    queryFn: listDashboards,
  });

  return (
    <div className={styles.home}>
      <div className={styles.panel}>
        <h2>Dashboards</h2>
        {isLoading && <div className={styles.dim}>Loading…</div>}
        {error != null && <div className={styles.error}>{String(error)}</div>}
        {entries && entries.length === 0 && (
          <div className={styles.dim}>
            No dashboards yet — create one with <b>+ New</b> in the top bar.
          </div>
        )}
        {entries?.map((e) => (
          <button
            key={e.path}
            className={styles.card}
            disabled={!e.valid}
            title={e.valid ? undefined : e.error}
            onClick={() => gotoDashboard(e.path)}
          >
            <span className={styles.cardName}>{e.name}</span>
            <span className={styles.cardPath}>{e.path}</span>
            {!e.valid && <span className={styles.invalid}>⚠ invalid</span>}
          </button>
        ))}
      </div>
    </div>
  );
}
