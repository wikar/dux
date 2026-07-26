import { useState } from "react";
import type { Schema, Relationship } from "@dux/core";
import { isMetaTable, resolveTable, duxClient as client } from "@dux/core";
import Modal, { modalStyles } from "./Modal";
import styles from "./SchemaTree.module.css";

export default function AddRelationshipModal(props: {
  schema: Schema;
  /** Existing relationship being edited — enables the delete-old flow. */
  initial?: Relationship;
  /** Pre-fill values for a new relationship (e.g. from drag-to-relate). */
  prefill?: Partial<Relationship>;
  onClose: () => void;
  onSaved: () => void;
}) {
  const isEdit = !!props.initial;
  const tables = Object.keys(props.schema.Tables).filter((n) => !isMetaTable(n)).sort();

  const colsFor = (t: string) => {
    const tbl = props.schema.Tables[t];
    return tbl ? Object.values(tbl.Columns).map((c) => c.Name).sort() : [];
  };

  const [fromTable, setFromTable] = useState(() =>
    resolveTable(props.prefill?.FromTable ?? props.initial?.FromTable ?? "", tables));
  const [fromCol, setFromCol] = useState(
    props.prefill?.FromColumn ?? props.initial?.FromColumn ?? "");
  const [toTable, setToTable] = useState(() =>
    resolveTable(props.prefill?.ToTable ?? props.initial?.ToTable ?? "", tables));
  const [toCol, setToCol] = useState(
    props.prefill?.ToColumn ?? props.initial?.ToColumn ?? "");
  const [bidi, setBidi] = useState(props.initial?.Bidirectional ?? false);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  function reverse() {
    setFromTable(toTable);
    setFromCol(toCol);
    setToTable(fromTable);
    setToCol(fromCol);
  }

  async function remove() {
    const orig = props.initial;
    if (!orig) return;
    setSaving(true);
    setError("");
    try {
      await client.deleteRelationship({
        from_table: orig.FromTable, from_column: orig.FromColumn,
        to_table: orig.ToTable, to_column: orig.ToColumn,
      });
      props.onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setSaving(false);
    }
  }

  async function save() {
    if (!fromTable || !fromCol || !toTable || !toCol) {
      setError("All four fields are required.");
      return;
    }
    setSaving(true);
    setError("");
    try {
      const orig = props.initial;
      const changed =
        orig &&
        (orig.FromTable !== fromTable ||
          orig.FromColumn !== fromCol ||
          orig.ToTable !== toTable ||
          orig.ToColumn !== toCol);

      // Check if the exact reverse already exists and delete it.
      const reverseExists = (props.schema.Relationships ?? []).some(
        (r) =>
          r.FromTable === toTable &&
          r.FromColumn === toCol &&
          r.ToTable === fromTable &&
          r.ToColumn === fromCol
      );
      if (reverseExists) {
        await client.deleteRelationship({
          from_table: toTable, from_column: toCol,
          to_table: fromTable, to_column: fromCol,
        });
      }
      await client.addRelationship({
        from_table: fromTable, from_column: fromCol,
        to_table: toTable, to_column: toCol,
        bidirectional: bidi,
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
    value: string,
    onChange: (v: string) => void
  ) => (
    <>
      <label className={styles.modalLabel}>{label}</label>
      <select
        className={styles.modalSelect}
        value={value}
        onChange={(e) => onChange(e.currentTarget.value)}
      >
        <option value="">{placeholder}</option>
        {options.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    </>
  );

  return (
    <Modal
      title={isEdit ? "Edit Relationship" : "New Relationship"}
      onClose={props.onClose}
      footer={
        <>
          {isEdit && (
            <button className={modalStyles.btnDanger} onClick={remove} disabled={saving} style={{ marginRight: 8 }}>
              Delete
            </button>
          )}
          <button className={modalStyles.btn} onClick={reverse} disabled={saving} style={{ marginRight: 8 }}>
            ⇄ Reverse
          </button>
          <label style={{ display: "flex", alignItems: "center", gap: 6, marginRight: "auto", cursor: "pointer", fontSize: "0.875rem" }}>
            <input
              type="checkbox"
              checked={bidi}
              onChange={(e) => setBidi(e.currentTarget.checked)}
              disabled={saving}
            />
            Bidirectional
          </label>
          <button className={modalStyles.btn} onClick={props.onClose} disabled={saving}>
            Cancel
          </button>
          <button className={modalStyles.btnPrimary} onClick={save} disabled={saving}>
            {saving ? "Saving…" : "Save"}
          </button>
        </>
      }
    >
      {select("From table", "— select table —", tables, fromTable, (v) => { setFromTable(v); setFromCol(""); })}
      {select("From column", "— select column —", colsFor(fromTable), fromCol, setFromCol)}
      {select("To table", "— select table —", tables, toTable, (v) => { setToTable(v); setToCol(""); })}
      {select("To column", "— select column —", colsFor(toTable), toCol, setToCol)}

      {error && <div className={styles.modalError}>{error}</div>}
    </Modal>
  );
}
