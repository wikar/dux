import { useState } from "react";
import type { Schema, MeasureFormat } from "@dux/core";
import { isMetaTable } from "@dux/core";
import type { QueryClient } from "@tanstack/react-query";
import DuxEditor from "./DuxEditor";
import Modal, { modalStyles } from "./Modal";
import styles from "./SchemaTree.module.css";
import { deleteModelMeasure, saveModelMeasure } from "../model/modelMutations";

export type MeasureTarget = { table: string; name: string; expression: string; format?: MeasureFormat };

const FORMAT_KINDS = ["number", "decimal", "percent", "currency", "compact"] as const;

export default function AddMeasureModal(props: {
  schema: Schema;
  initial?: MeasureTarget;
  queryClient: QueryClient;
  onClose: () => void;
  onSaved: () => void;
}) {
  const isEdit = !!props.initial;
  const [table, setTable] = useState(props.initial?.table ?? "");
  const [name, setName] = useState(props.initial?.name ?? "");
  const [expression, setExpression] = useState(props.initial?.expression ?? "");
  // Display format ("" = none). Editing pre-fills from the stored format so a
  // re-save never silently clears it (POST /measures replaces the whole measure).
  const [formatKind, setFormatKind] = useState<string>(props.initial?.format?.kind ?? "");
  const [decimals, setDecimals] = useState<string>(
    props.initial?.format?.decimals !== undefined ? String(props.initial.format.decimals) : "");
  const [currency, setCurrency] = useState(props.initial?.format?.currency ?? "");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  const tables = Object.keys(props.schema.Tables).filter((n) => !isMetaTable(n)).sort();

  function buildFormat(): MeasureFormat | undefined {
    if (!formatKind) return undefined;
    const f: MeasureFormat = { kind: formatKind as MeasureFormat["kind"] };
    if (decimals.trim() !== "") f.decimals = Number(decimals);
    if (formatKind === "currency") f.currency = currency.trim().toUpperCase();
    return f;
  }

  async function remove() {
    const orig = props.initial;
    if (!orig) return;
    setSaving(true);
    setError("");
    try {
      await deleteModelMeasure(props.queryClient, orig.table, orig.name);
      props.onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setSaving(false);
    }
  }

  async function save() {
    if (!table || !name || !expression) {
      setError("Table, name, and expression are required.");
      return;
    }
    setSaving(true);
    setError("");
    try {
      await saveModelMeasure(props.queryClient, { table, name, expression, format: buildFormat() }, props.initial);
      props.onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal
      title={isEdit ? "Edit Measure" : "New Measure"}
      onClose={props.onClose}
      footer={
        <>
          {isEdit && (
            <button className={modalStyles.btnDanger} onClick={remove} disabled={saving} style={{ marginRight: "auto" }}>
              Delete
            </button>
          )}
          <button className={modalStyles.btn} onClick={props.onClose} disabled={saving}>Cancel</button>
          <button className={modalStyles.btnPrimary} onClick={save} disabled={saving}>
            {saving ? "Saving…" : "Save"}
          </button>
        </>
      }
    >
      <label className={styles.modalLabel}>Table</label>
      {isEdit ? (
        <div className={styles.modalInput} style={{ opacity: 0.6, cursor: "default" }}>{table}</div>
      ) : (
        <select
          className={styles.modalSelect}
          value={table}
          onChange={(e) => setTable(e.currentTarget.value)}
        >
          <option value="">— select table —</option>
          {tables.map((t) => <option key={t} value={t}>{t}</option>)}
        </select>
      )}

      <label className={styles.modalLabel}>Name</label>
      <input
        className={styles.modalInput}
        type="text"
        placeholder="Total Matches"
        value={name}
        onChange={(e) => setName(e.currentTarget.value)}
      />

      <label className={styles.modalLabel}>Expression</label>
      <DuxEditor
        className={styles.exprWrap}
        value={expression}
        onChange={setExpression}
        schema={props.schema}
        placeholder="SUM(Sales[NetRevenue])"
        excludeMetaTables
      />

      <label className={styles.modalLabel}>Format</label>
      <div style={{ display: "flex", gap: 6 }}>
        <select
          className={styles.modalSelect}
          style={{ flex: 1 }}
          value={formatKind}
          onChange={(e) => setFormatKind(e.currentTarget.value)}
        >
          <option value="">— none —</option>
          {FORMAT_KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
        </select>
        {formatKind && (
          <input
            className={styles.modalInput}
            style={{ width: 80 }}
            type="number"
            min={0}
            max={10}
            placeholder="decimals"
            title="Fraction digits (blank = default)"
            value={decimals}
            onChange={(e) => setDecimals(e.currentTarget.value)}
          />
        )}
        {formatKind === "currency" && (
          <input
            className={styles.modalInput}
            style={{ width: 64 }}
            type="text"
            maxLength={3}
            placeholder="SEK"
            title="ISO 4217 currency code"
            value={currency}
            onChange={(e) => setCurrency(e.currentTarget.value)}
          />
        )}
      </div>

      {error && <div className={styles.modalError}>{error}</div>}
    </Modal>
  );
}

