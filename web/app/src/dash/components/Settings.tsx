import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import styles from "./ElementsPane.module.css";
import Modal, { modalStyles } from "../../components/Modal";
import DuxEditor from "../../components/DuxEditor";
import FieldPill from "../../components/FieldPill";
import { putTheme } from "../api";
import {
  buildElementDux,
  DEFAULT_THEME,
  useDuxSchema,
  useElementData,
  useGlobalResolvedTheme,
  useGlobalTheme,
} from "../data";
import {
  addFieldToWell,
  addMapLayer,
  addFilterToElement,
  duplicateElement,
  removeElement,
  removeMapLayer,
  replaceFieldInWell,
  reorderFieldsInElement,
  reorderFiltersInElement,
  removeFieldFromElement,
  removeFilter,
  setFieldAggregate,
  setMapLayerField,
  setMapLayerKind,
  swapElementType,
  updateElement,
  updateFilter,
  clamp,
  TYPE_LABEL,
  type MapFieldSlot,
  type WellId,
} from "../docOps";
import { useDocStore } from "../store";
import type {
  BuilderFieldRef,
  DashElement,
  ElementType,
  ImageFit,
  MapLayer,
  MapLayerKind,
  SlicerConfig,
  SlicerKind,
  ThemeTokens,
} from "../types";
import { QUERY_TYPES } from "../types";
import { applySlicerSelection } from "../actions";
import { isMetricField, isNumeric, QueryFailedError } from "@dux/core";
import type { Aggregate, DragPayload, DropField, FilterField, FilterOp } from "@dux/core";

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

      {QUERY_TYPES.has(el.type) && <DataSection el={el} />}
      {QUERY_TYPES.has(el.type) && <VizSection el={el} />}
      {el.type === "map" && <MapSection el={el} />}
      {el.type === "slicer" && <SlicerSection el={el} />}
      {el.type === "text" && <TextSection el={el} />}
      {el.type === "image" && <ImageSection el={el} />}
    </>
  );
}

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

const SERIES_HINT = "Drop a column — Splits the first Values measure into one series per value";

// max marks single-slot wells: dropping onto a full one replaces the member.
const WELLS: Partial<
  Record<ElementType, { id: WellId; label: string; max?: number; hint?: string }[]>
> = {
  bar: [
    { id: "axis", label: "Axis" },
    { id: "values", label: "Values" },
    { id: "series", label: "Series by", max: 1, hint: SERIES_HINT },
  ],
  line: [
    { id: "axis", label: "Axis" },
    { id: "values", label: "Values" },
    { id: "y2", label: "Values · right axis" },
    { id: "series", label: "Series by", max: 1, hint: SERIES_HINT },
  ],
  combo: [
    { id: "axis", label: "Axis" },
    { id: "bars", label: "Bars" },
    { id: "lines", label: "Lines" },
  ],
  area: [
    { id: "axis", label: "Category" },
    { id: "values", label: "Values" },
    { id: "series", label: "Series by", max: 1, hint: SERIES_HINT },
  ],
  donut: [
    { id: "axis", label: "Category", max: 1 },
    { id: "values", label: "Value", max: 1 },
  ],
  kpi: [{ id: "values", label: "Value", max: 1 }],
  table: [{ id: "fields", label: "Columns" }],
  pivot: [
    { id: "rows", label: "Rows" },
    { id: "cols", label: "Columns" },
    { id: "values", label: "Values" },
  ],
};

function wellMembers(el: DashElement, well: WellId): BuilderFieldRef[] {
  const fields = el.query?.fields ?? [];
  const isMetric = (f: BuilderFieldRef) => isMetricField(asDropField(f));
  const metrics = fields.filter(isMetric);
  const y2 = new Set(el.viz?.y2 ?? []);
  const lines = new Set(el.viz?.lines ?? []);
  const cols = new Set(el.viz?.cols ?? []);
  const series = el.viz?.series;
  switch (well) {
    case "axis":
      return fields.filter((f) => !isMetric(f) && f.name !== series);
    case "series":
      return fields.filter((f) => !isMetric(f) && f.name === series);
    case "fields":
      return fields;
    case "values":
      return el.type === "line" ? metrics.filter((f) => !y2.has(f.name)) : metrics;
    case "y2":
      return metrics.filter((f) => y2.has(f.name));
    case "bars":
      return metrics.filter((f) => !lines.has(f.name));
    case "lines":
      return metrics.filter((f) => lines.has(f.name));
    case "rows":
      return fields.filter((f) => !isMetric(f) && !cols.has(f.name));
    case "cols":
      return fields.filter((f) => !isMetric(f) && cols.has(f.name));
  }
}

function asDropField(f: BuilderFieldRef): DropField {
  return {
    table: f.table,
    name: f.name,
    kind: f.kind ?? "column",
    dataType: f.dataType ?? "",
    aggregate: f.aggregate as Aggregate | undefined,
  };
}

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
            slicer: { kind: "buttons" as SlicerKind, ...x.slicer, table: p.table, column: p.name, dataType: p.dataType },
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
                  slicer: { kind: "buttons" as SlicerKind, ...x.slicer, table: "", column: "", dataType: undefined },
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
          setSlicer({ kind: e.target.value as SlicerKind });
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

      <IgnoreSlicers el={el} />
    </div>
  );
}

function BuilderSection({ el }: { el: DashElement }) {
  const wells = WELLS[el.type] ?? [];
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
          {error.message}
          {error instanceof QueryFailedError && error.line > 0 && ` (line ${error.line}, col ${error.column})`}
        </div>
      )}
      <div className={styles.hint}>Runs when the editor loses focus.</div>
    </div>
  );
}

// ─── Viz section ─────────────────────────────────────────────────────────────

function VizSection({ el }: { el: DashElement }) {
  const viz = el.viz ?? {};
  const setViz = (patch: Record<string, unknown>) =>
    updateElement(el.id, (x) => ({ ...x, viz: { ...x.viz, ...patch } }));

  const isChart =
    el.type === "bar" || el.type === "line" || el.type === "combo" ||
    el.type === "area" || el.type === "donut";
  // Everything with an axis/rows can hide no-data items; KPI has no axis.
  const hasEmptyToggle = el.type !== "kpi";
  if (!isChart && !hasEmptyToggle) return null;

  return (
    <div className={styles.section}>
      <div className={styles.heading}>Display</div>

      {hasEmptyToggle && (
        <label className={styles.check}>
          <input
            type="checkbox"
            checked={viz.showEmpty ?? false}
            onChange={(e) => setViz({ showEmpty: e.target.checked })}
          />
          Show items with no data
        </label>
      )}

      {el.type === "bar" && (
        <>
          <label className={styles.label}>Orientation</label>
          <select
            className={styles.input}
            value={viz.orientation ?? "vertical"}
            onChange={(e) => setViz({ orientation: e.target.value })}
          >
            <option value="vertical">Vertical</option>
            <option value="horizontal">Horizontal</option>
          </select>
        </>
      )}

      {(el.type === "bar" || el.type === "area") && (
        <label className={styles.check}>
          <input
            type="checkbox"
            checked={viz.stacked ?? false}
            onChange={(e) => setViz({ stacked: e.target.checked })}
          />
          Stacked
        </label>
      )}

      {el.type === "pivot" && (
        <>
          <label className={styles.check}>
            <input
              type="checkbox"
              checked={viz.subtotals ?? true}
              onChange={(e) => setViz({ subtotals: e.target.checked })}
            />
            Subtotals
          </label>
          <label className={styles.check}>
            <input
              type="checkbox"
              checked={viz.grandTotal ?? true}
              onChange={(e) => setViz({ grandTotal: e.target.checked })}
            />
            Grand total
          </label>
          <label className={styles.check}>
            <input
              type="checkbox"
              checked={viz.totalCol ?? true}
              onChange={(e) => setViz({ totalCol: e.target.checked })}
            />
            Total column
          </label>
          <label className={styles.check}>
            <input
              type="checkbox"
              checked={viz.collapsed ?? true}
              onChange={(e) => setViz({ collapsed: e.target.checked })}
            />
            Start collapsed
          </label>
        </>
      )}

      {el.type === "combo" && (
        <label className={styles.check}>
          <input
            type="checkbox"
            checked={viz.lineY2 ?? true}
            onChange={(e) => setViz({ lineY2: e.target.checked })}
          />
          Lines on right axis
        </label>
      )}

      {isChart && (
        <>
          <label className={styles.label}>Legend</label>
          <select
            className={styles.input}
            value={viz.legend === undefined ? "auto" : viz.legend ? "show" : "hide"}
            onChange={(e) => {
              const v = e.target.value;
              setViz({ legend: v === "auto" ? undefined : v === "show" });
            }}
          >
            <option value="auto">Auto (multi-series)</option>
            <option value="show">Show</option>
            <option value="hide">Hide</option>
          </select>
        </>
      )}
    </div>
  );
}

// ─── Dashboard settings ──────────────────────────────────────────────────────
// Background color/image moved to the Theme section (M4.5): the theme tokens
// own the canvas look; legacy canvas.background is still rendered and gets
// cleared when the corresponding theme token is written.

function DashboardSettings() {
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

// ─── Theme (all visual tokens; global template ← per-dashboard overrides) ────

const COLOR_TOKENS: { key: keyof ThemeTokens; label: string }[] = [
  { key: "background", label: "Background color" },
  { key: "elementBackground", label: "Element background" },
  { key: "titleBackground", label: "Title background" },
  { key: "border", label: "Border color" },
  { key: "text", label: "Text color" },
];

// ── Color plumbing ──
// The native picker gets the `alpha` attribute (Chromium 133+ shows an alpha
// slider; elsewhere it degrades to the plain picker). Committed values are
// normalised to #rrggbb, or rgba(r, g, b, a) only when alpha < 1.

function parseColor(c: string): { r: number; g: number; b: number; a: number } | null {
  const s = c.trim();
  let m = /^#([0-9a-f]{6})([0-9a-f]{2})?$/i.exec(s);
  if (m) {
    const n = parseInt(m[1], 16);
    return {
      r: (n >> 16) & 255,
      g: (n >> 8) & 255,
      b: n & 255,
      a: m[2] ? parseInt(m[2], 16) / 255 : 1,
    };
  }
  m = /^rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)(?:[,\s/]+([\d.]+%?))?\s*\)$/i.exec(s);
  if (m) {
    let a = 1;
    if (m[4] !== undefined) a = m[4].endsWith("%") ? parseFloat(m[4]) / 100 : parseFloat(m[4]);
    return { r: +m[1], g: +m[2], b: +m[3], a };
  }
  return null;
}

const hex2 = (n: number) => Math.max(0, Math.min(255, Math.round(n))).toString(16).padStart(2, "0");

const pickerHasAlpha =
  typeof document !== "undefined" && "alpha" in document.createElement("input");

/** Value for the native picker: #rrggbbaa when the picker supports alpha,
 *  plain #rrggbb otherwise. Unparseable colors fall back to a neutral base. */
function toPickerValue(c: string): string {
  const p = parseColor(c) ?? { r: 30, g: 30, b: 46, a: 1 };
  const base = `#${hex2(p.r)}${hex2(p.g)}${hex2(p.b)}`;
  return pickerHasAlpha ? base + hex2(p.a * 255) : base;
}

/** Normalise a committed color: rgba only when the alpha channel is set. */
function normalizeColor(v: string): string {
  const p = parseColor(v);
  if (!p) return v;
  if (p.a >= 1) return `#${hex2(p.r)}${hex2(p.g)}${hex2(p.b)}`;
  return `rgba(${p.r}, ${p.g}, ${p.b}, ${Math.round(p.a * 1000) / 1000})`;
}

/** Marks a native color input as alpha-capable (no React typing for the
 *  attribute yet; unknown attributes are ignored by older browsers). */
const enableAlpha = (el: HTMLInputElement | null) => el?.setAttribute("alpha", "");

/** One themable color: swatch + native picker (with alpha slider where
 *  supported) and a text field accepting any CSS color incl. alpha
 *  (#rrggbbaa, rgba(…)). Empty = inherit; both commit on blur. */
function ColorField({
  value,
  placeholder,
  onCommit,
}: {
  value: string;
  placeholder: string;
  onCommit: (v: string) => void;
}) {
  return (
    <div className={styles.row}>
      <span className={styles.swatch} style={{ background: value || placeholder }} title={value || `inherited: ${placeholder}`} />
      <input
        type="color"
        ref={enableAlpha}
        className={styles.color}
        key={`p:${value}`}
        defaultValue={toPickerValue(value || placeholder)}
        onBlur={(e) => onCommit(normalizeColor(e.target.value))}
      />
      <input
        className={styles.input}
        key={`t:${value}`}
        defaultValue={value}
        placeholder={placeholder}
        onBlur={(e) => onCommit(normalizeColor(e.target.value.trim()))}
      />
    </div>
  );
}

interface ThemeEditorProps {
  /** Sparse tokens being edited (empty/absent = inherit). */
  tokens: ThemeTokens;
  /** Effective values one level up — shown as placeholders/previews. */
  inherited: Required<ThemeTokens>;
  onToken: (key: keyof ThemeTokens, value: string | string[] | undefined) => void;
}

/** Shared token editor for both the global theme and dashboard overrides. */
function ThemeEditor({ tokens, inherited, onToken }: ThemeEditorProps) {
  return (
    <>
      {COLOR_TOKENS.map((t) => (
        <div key={t.key}>
          <label className={styles.label}>{t.label}</label>
          <ColorField
            value={(tokens[t.key] as string | undefined) ?? ""}
            placeholder={inherited[t.key] as string}
            onCommit={(v) => onToken(t.key, v || undefined)}
          />
        </div>
      ))}

      <label className={styles.label}>Background image URL</label>
      <input
        className={styles.input}
        key={`bgi:${tokens.backgroundImage ?? ""}`}
        defaultValue={tokens.backgroundImage ?? ""}
        placeholder={inherited.backgroundImage || "none"}
        onBlur={(e) => onToken("backgroundImage", e.target.value.trim() || undefined)}
      />

      <label className={styles.label}>Background image fit</label>
      <select
        className={styles.input}
        value={tokens.backgroundFit ?? ""}
        onChange={(e) => onToken("backgroundFit", e.target.value || undefined)}
      >
        <option value="">inherit ({inherited.backgroundFit})</option>
        <option value="cover">cover</option>
        <option value="contain">contain</option>
        <option value="fill">fill</option>
        <option value="tile">tile</option>
      </select>

      <label className={styles.label}>Font family</label>
      <input
        className={styles.input}
        key={`ff:${tokens.fontFamily ?? ""}`}
        defaultValue={tokens.fontFamily ?? ""}
        placeholder={inherited.fontFamily}
        onBlur={(e) => onToken("fontFamily", e.target.value.trim() || undefined)}
      />

      <label className={styles.label}>Data colors (left → right)</label>
      {tokens.palette ? (
        <>
          <PaletteEditor colors={tokens.palette} onChange={(p) => onToken("palette", p.length ? p : undefined)} />
          <button className={styles.btn} onClick={() => onToken("palette", undefined)}>
            Reset to inherited
          </button>
        </>
      ) : (
        <>
          <div className={styles.swatchRow}>
            {inherited.palette.map((c, i) => (
              <span key={i} className={styles.swatch} style={{ background: c }} title={c} />
            ))}
          </div>
          <button className={styles.btn} onClick={() => onToken("palette", [...inherited.palette])}>
            Customize
          </button>
        </>
      )}
    </>
  );
}

function ThemeSection() {
  const doc = useDocStore((s) => s.doc)!;
  const inherited = useGlobalResolvedTheme();
  const [globalOpen, setGlobalOpen] = useState(false);
  const tokens = (doc.theme ?? {}) as ThemeTokens;

  const onToken = (key: keyof ThemeTokens, value: string | string[] | undefined) =>
    useDocStore.getState().update((d) => {
      const theme = { ...(d.theme ?? {}) } as Record<string, unknown>;
      if (value === undefined) delete theme[key];
      else theme[key] = value;
      const next = { ...d };
      if (Object.keys(theme).length > 0) next.theme = theme;
      else delete next.theme;
      // The theme tokens own the background now — writing one clears the
      // legacy canvas.background field it supersedes.
      if (key === "background" || key === "backgroundImage" || key === "backgroundFit") {
        const bg = { ...next.canvas.background };
        if (key === "background") delete bg.color;
        if (key === "backgroundImage") {
          delete bg.url;
          delete bg.asset;
        }
        if (key === "backgroundFit") delete bg.fit;
        next.canvas = { ...next.canvas };
        if (Object.keys(bg).length > 0) next.canvas.background = bg;
        else delete next.canvas.background;
      }
      return next;
    });

  return (
    <div className={styles.section}>
      <div className={styles.heading}>
        Theme <span className={styles.id}>overrides the global theme</span>
      </div>
      <ThemeEditor tokens={tokens} inherited={inherited} onToken={onToken} />
      <button className={styles.btn} onClick={() => setGlobalOpen(true)}>
        Edit global theme…
      </button>
      {globalOpen && <GlobalThemeModal onClose={() => setGlobalOpen(false)} />}
    </div>
  );
}

function PaletteEditor({ colors, onChange }: { colors: string[]; onChange: (c: string[]) => void }) {
  return (
    <div className={styles.swatchRow}>
      {colors.map((c, i) => (
        <span key={i} className={styles.swatchEdit}>
          <input
            type="color"
            ref={enableAlpha}
            className={styles.swatchInput}
            key={`c:${i}:${c}`}
            defaultValue={toPickerValue(c)}
            title={c}
            onBlur={(e) => onChange(colors.map((x, j) => (j === i ? normalizeColor(e.target.value) : x)))}
          />
          <button
            className={styles.swatchRemove}
            title="Remove color"
            onClick={() => onChange(colors.filter((_, j) => j !== i))}
          >
            ×
          </button>
        </span>
      ))}
      <button className={styles.btn} onClick={() => onChange([...colors, "#89b4fa"])} title="Add color">
        +
      </button>
    </div>
  );
}

function GlobalThemeModal({ onClose }: { onClose: () => void }) {
  const { data } = useGlobalTheme();
  const queryClient = useQueryClient();
  const [tokens, setTokens] = useState<Record<string, unknown>>(() => ({ ...(data?.tokens ?? {}) }));
  const [error, setError] = useState<string | null>(null);

  const onToken = (key: keyof ThemeTokens, value: string | string[] | undefined) =>
    setTokens((t) => {
      const next = { ...t };
      if (value === undefined) delete next[key];
      else next[key] = value;
      return next;
    });

  const save = async () => {
    try {
      await putTheme(tokens, data?.etag ?? null);
      await queryClient.invalidateQueries({ queryKey: ["dash-theme"] });
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <Modal
      title="Global theme"
      onClose={onClose}
      footer={
        <>
          <button className={modalStyles.btn} onClick={onClose}>
            Cancel
          </button>
          <button className={modalStyles.btnPrimary} onClick={() => void save()}>
            Save
          </button>
        </>
      }
    >
      <p className={modalStyles.hint}>
        Saved to <code>dashboards/theme.json</code> — the template every dashboard inherits
        (each dashboard can override single tokens). Also exportable/importable via{" "}
        <code>GET/PUT /api/dash/theme</code>.
      </p>
      <div className={styles.themeModalBody}>
        <ThemeEditor tokens={tokens as ThemeTokens} inherited={DEFAULT_THEME} onToken={onToken} />
      </div>
      {error && <div className={modalStyles.error}>{error}</div>}
    </Modal>
  );
}
