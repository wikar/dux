import { useRef, useState } from "react";
import styles from "./RightPane.module.css";
import { uploadAsset } from "../api";
import { duplicateElement, removeElement, updateCanvas, updateElement, clamp, TYPE_LABEL } from "../docOps";
import { useDocStore } from "../store";
import type { BackgroundFit, DashElement } from "../types";

/** Settings for the selected element, or the dashboard when none selected.
 *  Text fields commit on blur (one undo step per edit); everything else
 *  commits immediately. */
export default function Settings({ el }: { el: DashElement | null }) {
  return el ? <ElementSettings el={el} /> : <DashboardSettings />;
}

// ─── Element settings ────────────────────────────────────────────────────────

function ElementSettings({ el }: { el: DashElement }) {
  const setLayout = (k: "x" | "y" | "w" | "h" | "z", v: number) => {
    if (!Number.isFinite(v)) return;
    updateElement(el.id, (e) => ({ ...e, layout: { ...e.layout, [k]: v } }));
  };

  return (
    <div className={styles.section}>
      <div className={styles.heading}>
        {TYPE_LABEL[el.type]} <span className={styles.id}>{el.id}</span>
      </div>

      <label className={styles.label}>Title</label>
      <input
        key={`title:${el.id}:${el.title?.text ?? ""}`}
        className={styles.input}
        defaultValue={el.title?.text ?? ""}
        placeholder="No title"
        onBlur={(e) => {
          const text = e.target.value;
          updateElement(el.id, (x) => ({ ...x, title: { ...x.title, text, show: x.title?.show ?? true } }));
        }}
      />
      <label className={styles.check}>
        <input
          type="checkbox"
          checked={el.title?.show ?? true}
          onChange={(e) =>
            updateElement(el.id, (x) => ({ ...x, title: { ...x.title, show: e.target.checked } }))
          }
        />
        Show title
      </label>

      <label className={styles.label}>Layout</label>
      <div className={styles.grid4}>
        {(["x", "y", "w", "h"] as const).map((k) => (
          <label key={k} className={styles.numField}>
            <span>{k}</span>
            <input
              type="number"
              className={styles.input}
              value={el.layout[k]}
              min={0}
              onChange={(e) => setLayout(k, Number(e.target.value))}
            />
          </label>
        ))}
      </div>
      <label className={styles.numField}>
        <span>z</span>
        <input
          type="number"
          className={styles.input}
          value={el.layout.z ?? 0}
          onChange={(e) => setLayout("z", Number(e.target.value))}
        />
      </label>

      {el.type === "text" && (
        <>
          <label className={styles.label}>Markdown</label>
          <textarea
            key={`md:${el.id}:${el.text?.markdown ?? ""}`}
            className={styles.textarea}
            rows={10}
            defaultValue={el.text?.markdown ?? ""}
            onBlur={(e) => {
              const markdown = e.target.value;
              updateElement(el.id, (x) => ({ ...x, text: { markdown } }));
            }}
          />
          <div className={styles.hint}>Applied when the field loses focus.</div>
        </>
      )}

      <div className={styles.row}>
        <button className={styles.btn} onClick={() => duplicateElement(el.id)}>
          Duplicate
        </button>
        <button className={styles.btnDanger} onClick={() => removeElement(el.id)}>
          Delete
        </button>
      </div>
    </div>
  );
}

// ─── Dashboard settings ──────────────────────────────────────────────────────

const FITS: BackgroundFit[] = ["cover", "contain", "fill", "tile"];

function DashboardSettings() {
  const doc = useDocStore((s) => s.doc)!;
  const fileRef = useRef<HTMLInputElement>(null);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const bg = doc.canvas.background;

  const setSize = (k: "width" | "height", v: number) => {
    if (!Number.isFinite(v)) return;
    updateCanvas((d) => ({ ...d, canvas: { ...d.canvas, [k]: clamp(Math.round(v), 320, 16384) } }));
  };

  const setBackground = (patch: Partial<NonNullable<typeof bg>>) =>
    updateCanvas((d) => ({
      ...d,
      canvas: { ...d.canvas, background: { ...d.canvas.background, ...patch } },
    }));

  const onUpload = async (file: File) => {
    setUploadError(null);
    // Assets live under backgrounds/ with a sanitised lower-case filename.
    const name = file.name.toLowerCase().replace(/[^a-z0-9._-]+/g, "_");
    try {
      const r = await uploadAsset(`backgrounds/${name}`, file);
      setBackground({ asset: r.path, fit: bg?.fit ?? "cover" });
    } catch (e) {
      setUploadError(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <div className={styles.section}>
      <div className={styles.heading}>Dashboard</div>

      <label className={styles.label}>Canvas size</label>
      <div className={styles.grid4}>
        <label className={styles.numField}>
          <span>w</span>
          <input
            type="number"
            className={styles.input}
            value={doc.canvas.width}
            onChange={(e) => setSize("width", Number(e.target.value))}
          />
        </label>
        <label className={styles.numField}>
          <span>h</span>
          <input
            type="number"
            className={styles.input}
            value={doc.canvas.height}
            onChange={(e) => setSize("height", Number(e.target.value))}
          />
        </label>
      </div>

      <label className={styles.label}>Background color</label>
      <div className={styles.row}>
        <input
          type="color"
          className={styles.color}
          value={/^#[0-9a-f]{6}$/i.test(bg?.color ?? "") ? bg!.color! : "#1e1e2e"}
          onChange={(e) => setBackground({ color: e.target.value })}
        />
        <input
          key={`bgc:${bg?.color ?? ""}`}
          className={styles.input}
          defaultValue={bg?.color ?? ""}
          placeholder="#1e1e2e"
          onBlur={(e) => setBackground({ color: e.target.value })}
        />
      </div>

      <label className={styles.label}>Background image</label>
      {bg?.asset ? (
        <div className={styles.row}>
          <span className={styles.assetName} title={bg.asset}>
            {bg.asset}
          </span>
          <button className={styles.btn} onClick={() => setBackground({ asset: null })}>
            Clear
          </button>
        </div>
      ) : (
        <button className={styles.btn} onClick={() => fileRef.current?.click()}>
          Upload image…
        </button>
      )}
      <input
        ref={fileRef}
        type="file"
        accept=".png,.jpg,.jpeg,.webp,.svg"
        style={{ display: "none" }}
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) void onUpload(f);
          e.target.value = "";
        }}
      />
      {uploadError && <div className={styles.error}>{uploadError}</div>}
      {bg?.asset && (
        <>
          <label className={styles.label}>Image fit</label>
          <select
            className={styles.input}
            value={bg.fit ?? "cover"}
            onChange={(e) => setBackground({ fit: e.target.value as BackgroundFit })}
          >
            {FITS.map((f) => (
              <option key={f} value={f}>
                {f}
              </option>
            ))}
          </select>
        </>
      )}

      <label className={styles.label}>Live refresh</label>
      <label className={styles.check}>
        <input
          type="checkbox"
          checked={doc.refresh?.enabled ?? false}
          onChange={(e) =>
            updateCanvas((d) => ({
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
            min={5}
            value={doc.refresh.intervalSeconds}
            onChange={(e) => {
              const v = Number(e.target.value);
              if (!Number.isFinite(v)) return;
              updateCanvas((d) => ({
                ...d,
                refresh: { enabled: true, intervalSeconds: clamp(Math.round(v), 1, 86400) },
              }));
            }}
          />
        </label>
      )}
      <div className={styles.hint}>The refresh timer itself arrives in M5; the setting is saved now.</div>
    </div>
  );
}
