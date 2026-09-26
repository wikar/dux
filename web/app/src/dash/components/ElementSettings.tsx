import { useState, type ComponentType } from "react";
import styles from "./ElementsPane.module.css";
import DuxEditor from "../../components/DuxEditor";
import FieldPill from "../../components/FieldPill";
import { useDuxSchema } from "../data";
import { buildElementDux, useElementData } from "../elementQuery";
import {
  addFieldToWell, addMapLayer, addFilterToElement, duplicateElement, removeElement,
  removeMapLayer, replaceFieldInWell, reorderFieldsInElement, reorderFiltersInElement,
  removeFieldFromElement, removeFilter, setFieldAggregate, setMapLayerField,
  setMapLayerKind, swapElementType, updateFilter, type MapFieldSlot,
} from "../docOps";
import { updateElement, useDocStore } from "../store";
import type { DashElement, ElementType, ImageFit, MapLayer, MapLayerKind, SlicerConfig, SlicerKind } from "../types";
import { QUERY_TYPES, TYPE_LABEL, VISUALS } from "../visuals";
import type { OptionSpec, WellId } from "../visuals/types";
import { asDropField, wellMembers } from "../visuals/wells";
import { applySlicerSelection } from "../actions";
import { isNumeric, QueryFailedError } from "@dux/core";
import type { Aggregate, DragPayload, FilterField, FilterOp } from "@dux/core";
import { displayMessage } from "../message";

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

      {VISUALS[el.type].data && <DataSection el={el} />}
      <VizSection el={el} />
      {OwnSection && <OwnSection el={el} />}
    </>
  );
}

/** Visuals whose configuration doesn't fit the wells + display-options idiom
 *  bring their own section. Everything else is registry-driven. */
const OWN_SECTIONS: Partial<Record<ElementType, ComponentType<{ el: DashElement }>>> = {
  map: MapSection,
  slicer: SlicerSection,
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

// ─── Data section (wells / filters / sort / raw) ─────────────────────────────

function DataSection({ el }: { el: DashElement }) {
  const mode = el.query?.mode ?? "builder";

  const setMode = (m: "builder" | "raw") => {
    if (m === mode) return;
    updateElement(el.id, (x) => {
      const q = x.query ?? { mode: m };
      // Entering raw mode seeds the editor with the current generated query.
      const raw = m === "raw" && !q.raw ? buildElementDux(x) : q.raw;
      return { ...x, query: { ...q, mode: m, raw } };
    });
  };

  return (
    <div className={styles.section}>
      <div className={styles.heading}>Data</div>
      <div className={styles.segmented}>
        <button
          className={`${styles.segBtn}${mode === "builder" ? ` ${styles.segBtnActive}` : ""}`}
          onClick={() => setMode("builder")}
        >
          Builder
        </button>
        <button
          className={`${styles.segBtn}${mode === "raw" ? ` ${styles.segBtnActive}` : ""}`}
          onClick={() => setMode("raw")}
        >
          Raw DUX
        </button>
      </div>
      {mode === "raw" ? <RawSection el={el} /> : <BuilderSection el={el} />}
      <IgnoreSlicers el={el} />
    </div>
  );
}

/** Opt an element out of specific slicers (interactions.ignoreSlicers). */
function IgnoreSlicers({ el }: { el: DashElement }) {
  const doc = useDocStore((s) => s.doc);
  const slicers = (doc?.elements ?? []).filter((e) => e.type === "slicer" && e.id !== el.id);
  if (slicers.length === 0) return null;
  const ignored = el.interactions?.ignoreSlicers ?? [];

  const toggle = (id: string) =>
    updateElement(el.id, (x) => {
      const cur = new Set(x.interactions?.ignoreSlicers ?? []);
      if (cur.has(id)) cur.delete(id);
      else cur.add(id);
      const arr = [...cur];
      const next = { ...x };
      if (arr.length > 0) next.interactions = { ...x.interactions, ignoreSlicers: arr };
      else delete next.interactions;
      return next;
    });

  return (
    <>
      <label className={styles.label}>Ignore slicers</label>
      {slicers.map((sl) => (
        <label key={sl.id} className={styles.check}>
          <input type="checkbox" checked={ignored.includes(sl.id)} onChange={() => toggle(sl.id)} />
          {sl.title?.text || sl.id} <span className={styles.id}>{sl.id}</span>
        </label>
      ))}
    </>
  );
}

// ─── Slicer settings ─────────────────────────────────────────────────────────

const SLICER_KINDS: { v: SlicerKind; label: string }[] = [
  { v: "buttons", label: "Buttons (value pills)" },
  { v: "dropdown", label: "Dropdown (multi-select, search)" },
  { v: "range", label: "Range" },
  { v: "daterange", label: "Date range" },
];

/** Shared drag-over highlight + application/dux drop parsing for the wells.
 *  Returns the `over` highlight flag and handlers to spread on the drop div. */
function useDuxDrop(onDrop: (p: DragPayload) => void) {
  const [over, setOver] = useState(false);
  const dropProps = {
    onDragOver: (e: React.DragEvent) => {
      if (e.dataTransfer.types.includes("application/dux")) {
        e.preventDefault();
        e.dataTransfer.dropEffect = "copy";
        setOver(true);
      }
    },
    onDragLeave: () => setOver(false),
    onDrop: (e: React.DragEvent) => {
      e.preventDefault();
      setOver(false);
      const raw = e.dataTransfer.getData("application/dux");
      if (raw) onDrop(JSON.parse(raw) as DragPayload);
    },
  };
  return { over, dropProps };
}

/** Generic single-purpose drop target in the well idiom. */
function DropTarget({
  label,
  hint,
  onDrop,
  children,
}: {
  label: string;
  hint: string;
  onDrop: (p: DragPayload) => void;
  children?: React.ReactNode;
}) {
  const { over, dropProps } = useDuxDrop(onDrop);
  return (
    <div>
      <label className={styles.label}>{label}</label>
      <div className={`${styles.well}${over ? ` ${styles.wellOver}` : ""}`} {...dropProps}>
        {children ?? <span className={styles.wellHint}>{hint}</span>}
      </div>
    </div>
  );
}

const MAP_SLOTS: { slot: MapFieldSlot; label: string; hint: string }[] = [
  { slot: "lng", label: "Longitude", hint: "Drop a numeric column" },
  { slot: "lat", label: "Latitude", hint: "Drop a numeric column" },
  { slot: "size", label: "Size", hint: "Optional measure or numeric column" },
  { slot: "category", label: "Category", hint: "Optional column used for filtering" },
];

function MapSection({ el }: { el: DashElement }) {
  const layers = el.viz?.layers ?? [];
  const accepts = (slot: MapFieldSlot, p: DragPayload) => {
    if (slot === "size") return p.kind === "measure" || isNumeric(p.dataType);
    if (p.kind !== "column") return false;
    return slot === "category" || isNumeric(p.dataType);
  };

  const fieldChip = (layer: MapLayer, slot: MapFieldSlot) => {
    const field = layer[slot];
    if (!field) return undefined;
    return (
      <div className={styles.fieldChip}>
        <span className={styles.fieldChipName}>{field.table}[{field.name}]</span>
        <button className={styles.fieldChipRemove} title="Remove" onClick={() => setMapLayerField(el.id, layer.id, slot, null)}>×</button>
      </div>
    );
  };

  return (
    <div className={styles.section}>
      <div className={styles.heading}>Map</div>
      {layers.map((layer, index) => (
        <div key={layer.id}>
          <label className={styles.label}>Layer {index + 1}</label>
          <div className={styles.row}>
            <select
              className={styles.input}
              value={layer.kind}
              onChange={(e) => setMapLayerKind(el.id, layer.id, e.target.value as MapLayerKind)}
            >
              <option value="circle">Circle</option>
              <option value="pin">Pin</option>
              <option value="heatmap">Heatmap</option>
            </select>
            <button className={styles.btnDanger} onClick={() => removeMapLayer(el.id, layer.id)}>Remove</button>
          </div>
          {MAP_SLOTS.map(({ slot, label, hint }) => (
            <DropTarget
              key={slot}
              label={label}
              hint={hint}
              onDrop={(p) => accepts(slot, p) && setMapLayerField(el.id, layer.id, slot, p)}
            >
              {fieldChip(layer, slot)}
            </DropTarget>
          ))}
        </div>
      ))}
      <button className={styles.btn} disabled={layers.length >= 2} onClick={() => addMapLayer(el.id)}>Add layer</button>

      <button
        className={styles.btn}
        title="Save the map's current center and zoom as this visual's default view"
        onClick={() => window.dispatchEvent(new CustomEvent("dux-map-save-view", { detail: el.id }))}
      >
        Use current view
      </button>
      {el.viz?.center && <div className={styles.hint}>{el.viz.center.map((v) => v.toFixed(4)).join(", ")} · zoom {(el.viz.zoom ?? 1.2).toFixed(1)}</div>}
      <FiltersWell el={el} />
      <IgnoreSlicers el={el} />
    </div>
  );
}

function SlicerSection({ el }: { el: DashElement }) {
  const s = el.slicer;

  const setSlicer = (patch: Partial<SlicerConfig>) =>
    updateElement(el.id, (x) => ({
      ...x,
      slicer: { table: "", column: "", kind: "buttons" as SlicerKind, ...x.slicer, ...patch },
    }));

  const rangeKind = s?.kind === "range" || s?.kind === "daterange";

  return (
    <div className={styles.section}>
      <div className={styles.heading}>Slicer</div>

      <DropTarget
        label="Field"
        hint="Drop a column from Schema"
        onDrop={(p) => {
          if (p.kind !== "column") return;
          updateElement(el.id, (x) => ({
            ...x,
            // A new column invalidates the old selection and preset alike.
            slicer: { kind: "buttons" as SlicerKind, ...x.slicer, table: p.table, column: p.name, dataType: p.dataType, default: undefined },
            // The column names the slicer — still manually renameable after.
            title: { ...x.title, text: p.name, show: x.title?.show ?? true },
          }));
          // The old selection filtered a different column — drop it.
          applySlicerSelection(el.id, null);
        }}
      >
        {s?.column ? (
          <div className={styles.fieldChip}>
            <span className={styles.fieldChipName}>
              {s.table}[{s.column}]
            </span>
            <button
              className={styles.fieldChipRemove}
              title="Remove"
              onClick={() => {
                updateElement(el.id, (x) => ({
                  ...x,
                  slicer: { kind: "buttons" as SlicerKind, ...x.slicer, table: "", column: "", dataType: undefined, default: undefined },
                  title: { ...x.title, text: TYPE_LABEL.slicer, show: x.title?.show ?? true },
                }));
                applySlicerSelection(el.id, null);
              }}
            >
              ×
            </button>
          </div>
        ) : undefined}
      </DropTarget>

      <DropTarget
        label="Trim by"
        hint="Drop a measure or numeric column — Hides values where it is null"
        onDrop={(p) => {
          if (p.kind === "measure") {
            setSlicer({ measure: { table: p.table, name: p.name, kind: "measure" } });
          } else if (isNumeric(p.dataType)) {
            setSlicer({
              measure: { table: p.table, name: p.name, kind: "column", dataType: p.dataType, aggregate: "SUM" },
            });
          }
        }}
      >
        {s?.measure ? (
          <FieldPill
            field={{
              table: s.measure.table,
              name: s.measure.name,
              kind: s.measure.kind ?? "measure",
              dataType: s.measure.dataType ?? "",
              aggregate: s.measure.aggregate as Aggregate | undefined,
            }}
            zone="fields"
            index={0}
            onRemove={() =>
              updateElement(el.id, (x) => {
                if (!x.slicer) return x;
                const { measure: _drop, ...rest } = x.slicer;
                return { ...x, slicer: rest };
              })
            }
            onReorder={() => {}}
            onAggChange={(agg) =>
              updateElement(el.id, (x) =>
                x.slicer?.measure
                  ? { ...x, slicer: { ...x.slicer, measure: { ...x.slicer.measure, aggregate: agg } } }
                  : x
              )
            }
          />
        ) : undefined}
      </DropTarget>

      <label className={styles.label}>Style</label>
      <select
        className={styles.input}
        value={s?.kind ?? "buttons"}
        onChange={(e) => {
          // range/daterange and the value kinds take different selection
          // shapes, so switching style drops both selection and preset.
          setSlicer({ kind: e.target.value as SlicerKind, default: undefined });
          applySlicerSelection(el.id, null);
        }}
      >
        {SLICER_KINDS.map((k) => (
          <option key={k.v} value={k.v}>
            {k.label}
          </option>
        ))}
      </select>

      {!rangeKind && (
        <label className={styles.check}>
          <input
            type="checkbox"
            checked={s?.multi ?? true}
            onChange={(e) => setSlicer({ multi: e.target.checked })}
          />
          Multi-select
        </label>
      )}

      {!rangeKind && (
        <>
          <label className={styles.label}>Sort</label>
          <select
            className={styles.input}
            value={s?.sort ?? "asc"}
            onChange={(e) => setSlicer({ sort: e.target.value as "asc" | "desc" })}
          >
            <option value="asc">Ascending</option>
            <option value="desc">Descending</option>
          </select>
        </>
      )}

      {(s?.kind ?? "buttons") === "buttons" && (
        <label className={styles.numField}>
          <span style={{ minWidth: 56 }}>Max pills</span>
          <input
            type="number"
            className={styles.input}
            min={1}
            value={s?.limit ?? 20}
            onChange={(e) => {
              const v = Math.round(Number(e.target.value));
              if (Number.isFinite(v) && v >= 1) setSlicer({ limit: v });
            }}
          />
        </label>
      )}

      {/* The preset filter has no control of its own: in edit mode the slicer's
          live selection IS slicer.default, saved with the document. */}
      <IgnoreSlicers el={el} />
    </div>
  );
}

function BuilderSection({ el }: { el: DashElement }) {
  const data = VISUALS[el.type].data;
  const wells = data?.wells ?? [];
  const fieldNames = (el.query?.fields ?? []).map((f) => f.name);
  const sort = el.query?.sort?.[0];

  const setSort = (field: string, dir: "asc" | "desc") =>
    updateElement(el.id, (x) => ({
      ...x,
      query: { ...(x.query ?? { mode: "builder" }), sort: field ? [{ field, dir }] : [] },
    }));

  const setTopN = (n: number | null) =>
    updateElement(el.id, (x) => ({
      ...x,
      query: { ...(x.query ?? { mode: "builder" }), topN: n },
    }));

  return (
    <>
      {wells.map((w) => (
        <Well key={w.id} el={el} well={w.id} label={w.label} max={w.max} hint={w.hint} />
      ))}
      <FiltersWell el={el} />

      {data?.sortable !== false && (
        <>
          <label className={styles.label}>Sort by</label>
          <div className={styles.row}>
            <select
              className={styles.input}
              value={sort?.field ?? ""}
              onChange={(e) => setSort(e.target.value, sort?.dir ?? "desc")}
            >
              <option value="">None</option>
              {fieldNames.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
            <select
              className={styles.input}
              style={{ width: 70 }}
              value={sort?.dir ?? "desc"}
              disabled={!sort?.field}
              onChange={(e) => sort?.field && setSort(sort.field, e.target.value as "asc" | "desc")}
            >
              <option value="desc">desc</option>
              <option value="asc">asc</option>
            </select>
          </div>
          <label className={styles.numField}>
            <span style={{ minWidth: 38 }}>Top N</span>
            <input
              type="number"
              className={styles.input}
              min={1}
              value={el.query?.topN ?? ""}
              placeholder="all"
              onChange={(e) => {
                const v = e.target.value === "" ? null : Number(e.target.value);
                if (v === null || (Number.isFinite(v) && v >= 1)) setTopN(v);
              }}
            />
          </label>
        </>
      )}
    </>
  );
}

function Well({
  el,
  well,
  label,
  max,
  hint,
}: {
  el: DashElement;
  well: WellId;
  label: string;
  max?: number;
  hint?: string;
}) {
  const members = wellMembers(el, well);
  const { over, dropProps } = useDuxDrop((p) => {
    // A full single-slot well swaps its member for the dropped field.
    if (max !== undefined && members.length >= max) {
      replaceFieldInWell(el.id, well, members.map((m) => m.name), p);
    } else {
      addFieldToWell(el.id, well, p);
    }
  });

  return (
    <div>
      <label className={styles.label}>{label}</label>
      <div className={`${styles.well}${over ? ` ${styles.wellOver}` : ""}`} {...dropProps}>
        {members.length === 0 && (
          <span className={styles.wellHint}>{hint ?? "Drop a field from Schema"}</span>
        )}
        {members.map((f, i) => (
          <FieldPill
            key={`${f.table}.${f.name}`}
            field={asDropField(f)}
            zone="fields"
            index={i}
            onRemove={() => removeFieldFromElement(el.id, f.name)}
            onReorder={(from) => reorderFieldsInElement(el.id, members, from, i)}
            onAggChange={(agg) => setFieldAggregate(el.id, f.name, agg)}
          />
        ))}
      </div>
    </div>
  );
}

function FiltersWell({ el }: { el: DashElement }) {
  const filters = el.query?.filters ?? [];
  const { over, dropProps } = useDuxDrop((p) => addFilterToElement(el.id, p));

  return (
    <div>
      <label className={styles.label}>Filters</label>
      <div className={`${styles.well}${over ? ` ${styles.wellOver}` : ""}`} {...dropProps}>
        {filters.length === 0 && <span className={styles.wellHint}>Drop a field to filter on</span>}
        {filters.map((f, i) => (
          <FieldPill
            key={`${f.table}.${f.name}`}
            field={{
              table: f.table,
              name: f.name,
              dataType: f.dataType ?? "",
              op: (f.op ?? "=") as FilterOp,
              value: f.value ?? "",
            } satisfies FilterField}
            zone="filters"
            index={i}
            onRemove={() => removeFilter(el.id, i)}
            onReorder={(from) => reorderFiltersInElement(el.id, from, i)}
            onOpChange={(op) => updateFilter(el.id, i, { op })}
            onValueChange={(value) => updateFilter(el.id, i, { value })}
          />
        ))}
      </div>
    </div>
  );
}

function RawSection({ el }: { el: DashElement }) {
  const schema = useDuxSchema();
  const { error } = useElementData(el);
  const committed = el.query?.raw ?? "";
  const [draft, setDraft] = useState(committed);

  const commit = () => {
    if (draft === committed) return;
    updateElement(el.id, (x) => ({
      ...x,
      query: { ...(x.query ?? { mode: "raw" }), mode: "raw", raw: draft },
    }));
  };

  return (
    <div
      onBlur={(e) => {
        if (e.currentTarget.contains(e.relatedTarget as Node)) return;
        commit();
      }}
    >
      <DuxEditor
        value={draft}
        onChange={setDraft}
        schema={schema}
        excludeMetaTables
        className={styles.rawWrap}
        placeholder="EVALUATE SUMMARIZECOLUMNS( … )"
        error={error instanceof QueryFailedError ? error : null}
      />
      {error && (
        <div className={styles.error}>
          {displayMessage(error)}
          {error instanceof QueryFailedError && error.line > 0 && ` (line ${error.line}, col ${error.column})`}
        </div>
      )}
      <div className={styles.hint}>Runs when the editor loses focus.</div>
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
