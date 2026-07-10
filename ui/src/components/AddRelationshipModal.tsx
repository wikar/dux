import { createSignal, Show, onMount, onCleanup } from "solid-js";
import type { Component } from "solid-js";
import type { Schema, Relationship } from "../dux/types";
import { isMetaTable, resolveTable } from "../dux/schemaHelpers";
import styles from "./SchemaTree.module.css";
import { duxClient as client } from "../dux/client";

const AddRelationshipModal: Component<{
  schema: Schema;
  /** Existing relationship being edited — enables the delete-old flow. */
  initial?: Relationship;
  /** Pre-fill values for a new relationship (e.g. from drag-to-relate). */
  prefill?: Partial<Relationship>;
  onClose: () => void;
  onSaved: () => void;
}> = (props) => {
  const isEdit = () => !!props.initial;
  const tables = () => Object.keys(props.schema.Tables).filter((n) => !isMetaTable(n)).sort();

  const colsFor = (t: string) => {
    const tbl = props.schema.Tables[t];
    return tbl ? Object.values(tbl.Columns).map((c) => c.Name).sort() : [];
  };

  const initFrom = resolveTable(
    props.prefill?.FromTable ?? props.initial?.FromTable ?? "",
    tables()
  );
  const initTo = resolveTable(
    props.prefill?.ToTable ?? props.initial?.ToTable ?? "",
    tables()
  );
  const [fromTable, setFromTable] = createSignal(initFrom);
  const [fromCol, setFromCol] = createSignal(
    props.prefill?.FromColumn ?? props.initial?.FromColumn ?? ""
  );
  const [toTable, setToTable] = createSignal(initTo);
  const [toCol, setToCol] = createSignal(
    props.prefill?.ToColumn ?? props.initial?.ToColumn ?? ""
  );
  const [bidi, setBidi] = createSignal(
    props.initial?.Bidirectional ?? false
  );
  const [error, setError] = createSignal("");
  const [saving, setSaving] = createSignal(false);

  onMount(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") props.onClose();
    }
    document.addEventListener("keydown", onKey);
    onCleanup(() => document.removeEventListener("keydown", onKey));
  });

  function reverse() {
    const ft = fromTable(), fc = fromCol(), tt = toTable(), tc = toCol();
    setFromTable(tt);
    setFromCol(tc);
    setToTable(ft);
    setToCol(fc);
  }

  async function save() {
    if (!fromTable() || !fromCol() || !toTable() || !toCol()) {
      setError("All four fields are required.");
      return;
    }
    setSaving(true);
    setError("");
    try {
      const orig = props.initial;
      const changed =
        orig &&
        (orig.FromTable !== fromTable() ||
          orig.FromColumn !== fromCol() ||
          orig.ToTable !== toTable() ||
          orig.ToColumn !== toCol());

      // Check if the exact reverse already exists and delete it.
      const reverseExists = (props.schema.Relationships ?? []).some(
        (r) =>
          r.FromTable === toTable() &&
          r.FromColumn === toCol() &&
          r.ToTable === fromTable() &&
          r.ToColumn === fromCol()
      );
      if (reverseExists) {
        await client.deleteRelationship({
          from_table: toTable(), from_column: toCol(),
          to_table: fromTable(), to_column: fromCol(),
        });
      }
      await client.addRelationship({
        from_table: fromTable(), from_column: fromCol(),
        to_table: toTable(), to_column: toCol(),
        bidirectional: bidi(),
      });
      // Only delete the old relationship after the new one is saved.
      if (changed) {
        await client.deleteRelationship({
          from_table: orig!.FromTable, from_column: orig!.FromColumn,
          to_table: orig!.ToTable, to_column: orig!.ToColumn,
        });
      }
      props.onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  const select = (
    label: string,
    placeholder: string,
    options: string[],
    value: () => string,
    onChange: (v: string) => void
  ) => (
    <>
      <label class={styles.modalLabel}>{label}</label>
      <select
        class={styles.modalSelect}
        onChange={(e) => onChange((e.currentTarget as HTMLSelectElement).value)}
      >
        <option value="">{placeholder}</option>
        {options.map((o) => (
          <option value={o} selected={o === value()}>
            {o}
          </option>
        ))}
      </select>
    </>
  );

  return (
    <div
      class={styles.modalOverlay}
      onClick={(e) => {
        if (e.target === e.currentTarget) props.onClose();
      }}
    >
      <div class={styles.modal}>
        <div class={styles.modalHeader}>
          <span>{isEdit() ? "Edit Relationship" : "New Relationship"}</span>
          <button class={styles.modalClose} onClick={props.onClose}>
            ✕
          </button>
        </div>
        <div class={styles.modalBody}>
          {select("From table", "— select table —", tables(), fromTable, (v) => { setFromTable(v); setFromCol(""); })}
          {select("From column", "— select column —", colsFor(fromTable()), fromCol, setFromCol)}
          {select("To table", "— select table —", tables(), toTable, (v) => { setToTable(v); setToCol(""); })}
          {select("To column", "— select column —", colsFor(toTable()), toCol, setToCol)}

          <Show when={error()}>
            <div class={styles.modalError}>{error()}</div>
          </Show>
        </div>
        <div class={styles.modalFooter}>
          <button class={styles.modalBtn} onClick={reverse} disabled={saving()} style="margin-right:8px">
            ⇄ Reverse
          </button>
          <label style="display:flex;align-items:center;gap:6px;margin-right:auto;cursor:pointer;font-size:0.875rem">
            <input
              type="checkbox"
              checked={bidi()}
              onChange={(e) => setBidi((e.currentTarget as HTMLInputElement).checked)}
              disabled={saving()}
            />
            Bidirectional
          </label>
          <button class={styles.modalBtn} onClick={props.onClose} disabled={saving()}>
            Cancel
          </button>
          <button
            class={`${styles.modalBtn} ${styles.modalBtnPrimary}`}
            onClick={save}
            disabled={saving()}
          >
            {saving() ? "Saving…" : "Save"}
          </button>
        </div>
      </div>
    </div>
  );
};

export default AddRelationshipModal;
