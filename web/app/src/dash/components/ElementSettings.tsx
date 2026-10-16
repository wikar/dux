import { type ComponentType } from "react";
import styles from "./ElementsPane.module.css";
import { duplicateElement, removeElement, swapElementType } from "../docOps";
import { updateElement } from "../store";
import type { DashElement, ElementType, ImageFit } from "../types";
import { QUERY_TYPES, TYPE_LABEL, VISUALS } from "../visuals";
import type { OptionSpec } from "../visuals/types";
import QuerySettings from "./QuerySettings";
import MapSettings from "./MapSettings";
import SlicerSettings from "./SlicerSettings";

// ─── Element settings ────────────────────────────────────────────────────────

export default function ElementSettings({ el }: { el: DashElement }) {
  const OwnSection = OWN_SECTIONS[el.type];
  const setLayout = (k: "x" | "y" | "w" | "h" | "z", v: number) => {
    if (!Number.isFinite(v)) return;
    updateElement(el.id, (e) => ({ ...e, layout: { ...e.layout, [k]: v } }));
  };

  return (
    <>
      <div className={styles.section}>
        <div className={styles.heading}>
          {TYPE_LABEL[el.type]} <span className={styles.id}>{el.id}</span>
        </div>

        {QUERY_TYPES.has(el.type) && (
          <>
            <label className={styles.label}>Type</label>
            <select
              className={styles.input}
              value={el.type}
              onChange={(e) => swapElementType(el.id, e.target.value as ElementType)}
            >
              {[...QUERY_TYPES].map((t) => (
                <option key={t} value={t}>
                  {TYPE_LABEL[t]}
                </option>
              ))}
            </select>
          </>
        )}

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
          <label className={styles.numField}>
            <span>z</span>
            <input
              type="number"
              className={styles.input}
              value={el.layout.z ?? 0}
              onChange={(e) => setLayout("z", Number(e.target.value))}
            />
          </label>
        </div>

        <div className={styles.row}>
          <button className={styles.btn} onClick={() => duplicateElement(el.id)}>
            Duplicate
          </button>
          <button className={styles.btnDanger} onClick={() => removeElement(el.id)}>
            Delete
          </button>
        </div>
      </div>

      {VISUALS[el.type].data && <QuerySettings el={el} />}
      <VizSection el={el} />
      {OwnSection && <OwnSection el={el} />}
    </>
  );
}

/** Visuals whose configuration doesn't fit the wells + display-options idiom
 *  bring their own section. Everything else is registry-driven. */
const OWN_SECTIONS: Partial<Record<ElementType, ComponentType<{ el: DashElement }>>> = {
  map: MapSettings,
  slicer: SlicerSettings,
  text: TextSection,
  image: ImageSection,
};

// ─── Text / image sections ───────────────────────────────────────────────────

function TextSection({ el }: { el: DashElement }) {
  return (
    <div className={styles.section}>
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
    </div>
  );
}

const IMAGE_FITS: ImageFit[] = ["contain", "cover", "fill"];

function ImageSection({ el }: { el: DashElement }) {
  return (
    <div className={styles.section}>
      <label className={styles.label}>Image URL</label>
      <input
        key={`img:${el.id}:${el.image?.url ?? ""}`}
        className={styles.input}
        defaultValue={el.image?.url ?? ""}
        placeholder="https://… or an /api/dash/assets/ path"
        onBlur={(e) => {
          const url = e.target.value.trim();
          updateElement(el.id, (x) => ({ ...x, image: { ...x.image, url } }));
        }}
      />
      <label className={styles.label}>Fit</label>
      <select
        className={styles.input}
        value={el.image?.fit ?? "contain"}
        onChange={(e) =>
          updateElement(el.id, (x) => ({ ...x, image: { ...x.image, fit: e.target.value as ImageFit } }))
        }
      >
        {IMAGE_FITS.map((f) => (
          <option key={f} value={f}>
            {f}
          </option>
        ))}
      </select>
    </div>
  );
}

// ─── Viz section ─────────────────────────────────────────────────────────────

/** Display options come from the visual registry: each spec names an
 *  element.viz key, so a new visual declares its controls instead of adding a
 *  branch here. */
function VizSection({ el }: { el: DashElement }) {
  const options = VISUALS[el.type].options ?? [];
  if (options.length === 0) return null;
  const viz = (el.viz ?? {}) as Record<string, unknown>;
  const setViz = (key: string, value: unknown) =>
    updateElement(el.id, (x) => ({ ...x, viz: { ...x.viz, [key]: value } }));

  return (
    <div className={styles.section}>
      <div className={styles.heading}>Display</div>
      {options.map((o) => (
        <VizOption key={o.key} spec={o} value={viz[o.key]} onChange={(v) => setViz(o.key, v)} />
      ))}
    </div>
  );
}

function VizOption({
  spec,
  value,
  onChange,
}: {
  spec: OptionSpec;
  value: unknown;
  onChange: (v: unknown) => void;
}) {
  if (spec.kind === "check") {
    return (
      <label className={styles.check}>
        <input
          type="checkbox"
          checked={(value as boolean | undefined) ?? spec.default}
          onChange={(e) => onChange(e.target.checked)}
        />
        {spec.label}
      </label>
    );
  }
  if (spec.kind === "select") {
    return (
      <>
        <label className={styles.label}>{spec.label}</label>
        <select
          className={styles.input}
          value={(value as string | undefined) ?? spec.default}
          onChange={(e) => onChange(e.target.value)}
        >
          {spec.choices.map((c) => (
            <option key={c.value} value={c.value}>
              {c.label}
            </option>
          ))}
        </select>
      </>
    );
  }
  if (spec.kind === "number") {
    return (
      <label className={styles.numField}>
        <span>{spec.label}</span>
        <input
          type="number"
          className={styles.input}
          value={(value as number | undefined) ?? spec.default}
          onChange={(e) => onChange(Number(e.target.value))}
        />
      </label>
    );
  }
  // Tri-state: unset means the visual decides (e.g. legend on multi-series).
  return (
    <>
      <label className={styles.label}>{spec.label}</label>
      <select
        className={styles.input}
        value={value === undefined ? "auto" : value ? "show" : "hide"}
        onChange={(e) => onChange(e.target.value === "auto" ? undefined : e.target.value === "show")}
      >
        <option value="auto">{spec.autoLabel}</option>
        <option value="show">Show</option>
        <option value="hide">Hide</option>
      </select>
    </>
  );
}
