import FieldPill from "../../components/FieldPill";
import { updateElement } from "../store";
import type { DashElement, SlicerConfig, SlicerKind } from "../types";
import { TYPE_LABEL } from "../visuals";
import { applySlicerSelection } from "../actions";
import { isNumeric } from "@dux/core";
import type { Aggregate, DragPayload } from "@dux/core";
import { IgnoreSlicers, DropTarget } from "./SettingsDropTarget";
import styles from "./ElementsPane.module.css";

const SLICER_KINDS: { v: SlicerKind; label: string }[] = [
  { v: "buttons", label: "Buttons (value pills)" },
  { v: "dropdown", label: "Dropdown (multi-select, search)" },
  { v: "range", label: "Range" },
  { v: "daterange", label: "Date range" },
];

/** Shared drag-over highlight + application/dux drop parsing for the wells.
 *  Returns the `over` highlight flag and handlers to spread on the drop div. */
export default function SlicerSettings({ el }: { el: DashElement }) {
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

