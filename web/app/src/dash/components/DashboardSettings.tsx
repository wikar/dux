import styles from "./ElementsPane.module.css";
import { clamp } from "../docOps";
import { useDocStore } from "../store";
import ThemeSection from "./ThemeSettings";

// ─── Dashboard settings ──────────────────────────────────────────────────────
// Background color/image moved to the Theme section (M4.5): the theme tokens
// own the canvas look; legacy canvas.background is still rendered and gets
// cleared when the corresponding theme token is written.

export default function DashboardSettings() {
  const doc = useDocStore((s) => s.doc)!;

  // Committed on blur so typing never fights a live clamp; only sanity
  // bounds (a positive integer within the schema's 16384 max) apply.
  const setSize = (k: "width" | "height", raw: string) => {
    const v = Math.round(Number(raw));
    if (!Number.isFinite(v) || v < 1 || v > 16384) return;
    useDocStore.getState().update((d) => ({ ...d, canvas: { ...d.canvas, [k]: v } }));
  };

  return (
    <>
      <div className={styles.section}>
        <div className={styles.heading}>Dashboard</div>

        <label className={styles.label}>Canvas size</label>
        <div className={styles.grid4}>
          <label className={styles.numField}>
            <span>w</span>
            <input
              type="number"
              className={styles.input}
              key={`cw:${doc.canvas.width}`}
              defaultValue={doc.canvas.width}
              onBlur={(e) => setSize("width", e.target.value)}
            />
          </label>
          <label className={styles.numField}>
            <span>h</span>
            <input
              type="number"
              className={styles.input}
              key={`ch:${doc.canvas.height}`}
              defaultValue={doc.canvas.height}
              onBlur={(e) => setSize("height", e.target.value)}
            />
          </label>
        </div>

        <label className={styles.label}>Live refresh</label>
        <label className={styles.check}>
          <input
            type="checkbox"
            checked={doc.refresh?.enabled ?? false}
            onChange={(e) =>
              useDocStore.getState().update((d) => ({
                ...d,
                refresh: { enabled: e.target.checked, intervalSeconds: d.refresh?.intervalSeconds ?? 60 },
              }))
            }
          />
          Enabled
        </label>
        {doc.refresh?.enabled && (
          <label className={styles.numField}>
            <span>every (s)</span>
            <input
              type="number"
              className={styles.input}
              style={{ width: 70 }}
              min={5}
              value={doc.refresh.intervalSeconds}
              onChange={(e) => {
                const v = Number(e.target.value);
                if (!Number.isFinite(v)) return;
                useDocStore.getState().update((d) => ({
                  ...d,
                  refresh: { enabled: true, intervalSeconds: clamp(Math.round(v), 1, 86400) },
                }));
              }}
            />
          </label>
        )}
        <div className={styles.hint}>Elements refetch on this interval, staggered ±10% (5s floor).</div>

        <label className={styles.label}>Element header controls</label>
        <label className={styles.check}>
          <input
            type="checkbox"
            checked={doc.controls?.funnel !== false}
            onChange={(e) =>
              useDocStore.getState().update((d) => ({
                ...d,
                controls: { ...d.controls, funnel: e.target.checked },
              }))
            }
          />
          Filter funnel icon
        </label>
        <label className={styles.check}>
          <input
            type="checkbox"
            checked={doc.controls?.csv !== false}
            onChange={(e) =>
              useDocStore.getState().update((d) => ({
                ...d,
                controls: { ...d.controls, csv: e.target.checked },
              }))
            }
          />
          CSV download icon
        </label>
        <div className={styles.hint}>Shown on every chart, table, and pivot header.</div>
      </div>

      <ThemeSection />
    </>
  );
}
