import { createSignal, Show, onMount, onCleanup } from "solid-js";
import type { Component } from "solid-js";
import type { Schema } from "dux-client";
import { isMetaTable, resolveTable } from "dux-client";
import type { RelTarget } from "dux-client";
import styles from "./SchemaTree.module.css";
import { useDuxClient } from "../clientContext";

export type { RelTarget };

const AddRelationshipModal: Component<{
  schema: Schema;
  /** Existing relationship being edited — enables the delete-old flow. */
  initial?: RelTarget;
  /** Pre-fill values for a new relationship (e.g. from drag-to-relate). */
  prefill?: Partial<RelTarget>;
  onClose: () => void;
  onSaved: () => void;
}> = (props) => {
  const client = useDuxClient();
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
          <label class={styles.modalLabel}>From table</label>
          <select
            class={styles.modalSelect}
            onChange={(e) => {
              setFromTable((e.currentTarget as HTMLSelectElement).value);
              setFromCol("");
            }}
          >
            <option value="">— select table —</option>
            {tables().map((t) => (
              <option value={t} selected={t === fromTable()}>
                {t}
              </option>
            ))}
          </select>

          <label class={styles.modalLabel}>From column</label>
          <select
            class={styles.modalSelect}
            onChange={(e) => setFromCol((e.currentTarget as HTMLSelectElement).value)}
          >
            <option value="">— select column —</option>
            {colsFor(fromTable()).map((c) => (
              <option value={c} selected={c === fromCol()}>
                {c}
              </option>
            ))}
          </select>

          <label class={styles.modalLabel}>To table</label>
          <select
            class={styles.modalSelect}
            onChange={(e) => {
              setToTable((e.currentTarget as HTMLSelectElement).value);
              setToCol("");
            }}
          >
            <option value="">— select table —</option>
            {tables().map((t) => (
              <option value={t} selected={t === toTable()}>
                {t}
              </option>
            ))}
          </select>

          <label class={styles.modalLabel}>To column</label>
          <select
            class={styles.modalSelect}
            onChange={(e) => setToCol((e.currentTarget as HTMLSelectElement).value)}
          >
            <option value="">— select column —</option>
            {colsFor(toTable()).map((c) => (
              <option value={c} selected={c === toCol()}>
                {c}
              </option>
            ))}
          </select>

          <Show when={error()}>
            <div class={styles.modalError}>{error()}</div>
          </Show>
        </div>
        <div class={styles.modalFooter}>
          <button class={styles.modalBtn} onClick={reverse} disabled={saving()} style="margin-right:auto">
            ⇄ Reverse
          </button>
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
