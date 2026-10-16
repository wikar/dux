import { useState } from "react";
import DuxEditor from "../../components/DuxEditor";
import FieldPill from "../../components/FieldPill";
import { useDuxSchema } from "../data";
import { buildElementDux, useElementData } from "../elementQuery";
import {
  addFieldToWell, addFilterToElement, removeFieldFromElement, removeFilter,
  replaceFieldInWell, reorderFieldsInElement, reorderFiltersInElement,
  setFieldAggregate, updateFilter,
} from "../docOps";
import { updateElement } from "../store";
import type { DashElement } from "../types";
import { QUERY_TYPES, TYPE_LABEL, VISUALS } from "../visuals";
import type { WellId } from "../visuals/types";
import { asDropField, wellMembers } from "../visuals/wells";
import { isNumeric, QueryFailedError } from "@dux/core";
import type { Aggregate, DragPayload, FilterField, FilterOp } from "@dux/core";
import { displayMessage } from "../message";
import { IgnoreSlicers, useDuxDrop } from "./SettingsDropTarget";
import styles from "./ElementsPane.module.css";

export default function QuerySettings({ el }: { el: DashElement }) {
// ─── Data section (wells / filters / sort / raw) ─────────────────────────────


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
      {mode === "raw" ? <RawSection key={`${el.id}:${el.query?.raw ?? ""}`} el={el} /> : <BuilderSection el={el} />}
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
            listId={`well:${el.id}:${well}`}
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

export function FiltersWell({ el }: { el: DashElement }) {
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
            listId={`filters:${el.id}`}
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

