import { createSignal, createResource, createEffect, on, For, Show, onMount, onCleanup } from "solid-js";
import type { Accessor, Component } from "solid-js";
import type { Schema, DragPayload, Relationship } from "dux-client";
import { isMetaTable, resolveTable } from "dux-client";
import TypeIcon from "./TypeIcon";
import DuxEditor from "./DuxEditor";
import styles from "./SchemaTree.module.css";
import { useDuxClient } from "../clientContext";
import AddRelationshipModal from "./AddRelationshipModal";

// ─── Icons ───────────────────────────────────────────────────────────────────

/** Outline eye-off icon (slashed eye) for the hidden toggle. */
const EyeOffIcon: Component<{ size?: number }> = (props) => (
  <svg width={props.size ?? 12} height={props.size ?? 12} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" />
    <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
    <line x1="1" y1="1" x2="23" y2="23" />
  </svg>
);

// ─── Draggable field row ─────────────────────────────────────────────────────

const FieldRow: Component<{
  payload: DragPayload;
  hidden?: boolean;
  onToggleHidden?: () => void;
}> = (props) => {
  function handleDragStart(e: DragEvent) {
    e.dataTransfer!.effectAllowed = "copy";
    e.dataTransfer!.setData("application/dux", JSON.stringify(props.payload));
  }

  return (
    <div
      draggable={true}
      class={styles.fieldRow}
      classList={{ [styles.fieldRowHidden]: props.hidden === true }}
      onDragStart={handleDragStart}
    >
      <Show when={props.payload.kind === "measure"} fallback={
        <TypeIcon dataType={props.payload.dataType} />
      }>
        <span class={styles.measureIcon} title="Measure">ƒx</span>
      </Show>
      <span class={styles.fieldName}>{props.payload.name}</span>
      <Show when={props.onToggleHidden}>
        <button
          class={styles.hideBtn}
          classList={{ [styles.hideBtnActive]: props.hidden === true }}
          title={props.hidden ? "Hidden — click to unhide" : "Hide this column"}
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            props.onToggleHidden?.();
          }}
        >
          <EyeOffIcon size={11} />
        </button>
      </Show>
    </div>
  );
};

// ─── Collapsible table group ─────────────────────────────────────────────────

const TableGroup: Component<{
  tableName: string;
  schema: Schema;
  showHidden?: boolean;
  onToggleHidden?: () => void;
  onToggleColumnHidden?: (colName: string) => void;
}> = (props) => {
  const [open, setOpen] = createSignal(false);

  const table = () => props.schema.Tables[props.tableName];
  const isHidden = () => table()?.Hidden === true;

  const columns = () => {
    const tbl = table();
    if (!tbl) return [];
    return Object.values(tbl.Columns)
      .filter((c) => props.showHidden || !c.Hidden)
      .sort((a, b) => a.Name.localeCompare(b.Name));
  };

  const measures = () => {
    const m = props.schema.Measures;
    if (!m) return [];
    // Measures may be keyed by bare name ("matches") or qualified ("atp.matches").
    // Try both so qualified table names in the schema panel still find their measures.
    const dot = props.tableName.indexOf(".");
    const bare = dot >= 0 ? props.tableName.slice(dot + 1) : props.tableName;
    const tblMeasures = m[props.tableName] ?? m[bare];
    if (!tblMeasures) return [];
    return Object.keys(tblMeasures).sort();
  };

  const total = () => columns().length + measures().length;

  return (
    <div class={styles.tableGroup}>
      <button
        class={styles.tableHeader}
        classList={{ [styles.tableHeaderHidden]: isHidden() }}
        onClick={() => setOpen((v) => !v)}
        title={`${total()} fields`}
      >
        <span class={styles.chevron}>{open() ? "▾" : "▸"}</span>
        <span class={styles.tableName}>{props.tableName}</span>
        <Show when={table()?.IsView}>
          <span class={styles.viewBadge} title="DuckDB view">view</span>
        </Show>
        <span class={styles.fieldCount}>{total()}</span>
        <span
          class={styles.hideBtn}
          classList={{ [styles.hideBtnActive]: isHidden() }}
          title={isHidden() ? "Hidden — click to unhide" : "Hide this table"}
          role="button"
          tabIndex={0}
          onClick={(e) => { e.stopPropagation(); props.onToggleHidden?.(); }}
          onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.stopPropagation(); props.onToggleHidden?.(); } }}
        >
          <EyeOffIcon />
        </span>
      </button>

      <Show when={open()}>
        <div class={styles.fieldList}>
          <For each={columns()}>
            {(col) => (
              <FieldRow
                payload={{
                  table: props.tableName,
                  name: col.Name,
                  kind: "column",
                  dataType: col.DataType,
                }}
                hidden={col.Hidden === true}
                onToggleHidden={() => props.onToggleColumnHidden?.(col.Name)}
              />
            )}
          </For>
          <For each={measures()}>
            {(measureName) => (
              <FieldRow payload={{
                table: props.tableName,
                name: measureName,
                kind: "measure",
                dataType: "",
              }} />
            )}
          </For>
        </div>
      </Show>
    </div>
  );
};

// ─── Add / edit measure modal ─────────────────────────────────────────────────

type EditTarget = { table: string; name: string; expression: string };

const AddMeasureModal: Component<{
  schema: Schema;
  initial?: EditTarget;
  onClose: () => void;
  onSaved: () => void;
}> = (props) => {
  const client = useDuxClient();
  const isEdit = () => !!props.initial;
  const [table, setTable] = createSignal(props.initial?.table ?? "");
  const [name, setName] = createSignal(props.initial?.name ?? "");
  const [expression, setExpression] = createSignal(props.initial?.expression ?? "");
  const [error, setError] = createSignal("");
  const [saving, setSaving] = createSignal(false);

  const tables = () =>
    Object.keys(props.schema.Tables).filter((n) => !isMetaTable(n)).sort();

  onMount(() => {
    function onKey(e: KeyboardEvent) { if (e.key === "Escape") props.onClose(); }
    document.addEventListener("keydown", onKey);
    onCleanup(() => document.removeEventListener("keydown", onKey));
  });

  async function save() {
    if (!table() || !name() || !expression()) {
      setError("Table, name, and expression are required.");
      return;
    }
    setSaving(true);
    setError("");
    try {
      const orig = props.initial;
      const nameChanged = orig && (orig.table !== table() || orig.name !== name());
      await client.addMeasure(table(), name(), expression());
      // Only delete the old entry after the new one is saved.
      if (nameChanged) {
        await client.deleteMeasure(orig!.table, orig!.name);
      }
      props.onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div class={styles.modalOverlay} onClick={(e) => { if (e.target === e.currentTarget) props.onClose(); }}>
      <div class={styles.modal}>
        <div class={styles.modalHeader}>
          <span>{isEdit() ? "Edit Measure" : "New Measure"}</span>
          <button class={styles.modalClose} onClick={props.onClose}>✕</button>
        </div>
        <div class={styles.modalBody}>
          <label class={styles.modalLabel}>Table</label>
          <Show
            when={!isEdit()}
            fallback={<div class={styles.modalInput} style="opacity:0.6;cursor:default">{table()}</div>}
          >
            <select
              class={styles.modalSelect}
              value={table()}
              onChange={(e) => setTable((e.currentTarget as HTMLSelectElement).value)}
            >
              <option value="">— select table —</option>
              <For each={tables()}>{(t) => <option value={t}>{t}</option>}</For>
            </select>
          </Show>

          <label class={styles.modalLabel}>Name</label>
          <input
            class={styles.modalInput}
            type="text"
            placeholder="Total Matches"
            value={name()}
            onInput={(e) => setName((e.currentTarget as HTMLInputElement).value)}
          />

          <label class={styles.modalLabel}>Expression</label>
          <DuxEditor
            class={styles.exprWrap}
            value={expression()}
            onChange={setExpression}
            schema={props.schema}
            placeholder="COUNT(matches[match_num])"
            excludeMetaTables
          />

          <Show when={error()}>
            <div class={styles.modalError}>{error()}</div>
          </Show>
        </div>
        <div class={styles.modalFooter}>
          <button class={styles.modalBtn} onClick={props.onClose} disabled={saving()}>Cancel</button>
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

// ─── Relationships section ────────────────────────────────────────────────────

const RelationshipsSection: Component<{
  schema: Schema;
  onAdd: () => void;
  onEdit: (r: Relationship) => void;
  onDelete: (r: Relationship) => void;
}> = (props) => {
  const [open, setOpen] = createSignal(false);
  const rels = () => props.schema.Relationships ?? [];

  return (
    <div class={styles.sideSection}>
      <button class={styles.sideSectionHeader} onClick={() => setOpen((v) => !v)}>
        <span class={styles.chevron}>{open() ? "▾" : "▸"}</span>
        <span class={styles.sideSectionTitle}>Relationships</span>
        <span class={styles.fieldCount}>{rels().length}</span>
        <span
          class={styles.addBtn}
          title="Add relationship"
          role="button"
          tabIndex={0}
          onClick={(e) => { e.stopPropagation(); props.onAdd(); }}
          onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.stopPropagation(); props.onAdd(); } }}
        >+</span>
      </button>
      <Show when={open()}>
        <div class={styles.sideSectionBody}>
          <Show
            when={rels().length > 0}
            fallback={<div class={styles.sideSectionEmpty}>No relationships defined</div>}
          >
            <For each={rels()}>
              {(r) => (
                <div
                  class={styles.relRow}
                  style="cursor:pointer"
                  onClick={() => props.onEdit(r)}
                >
                  <span class={styles.relLabel}>
                    {(() => {
                      const tkeys = Object.keys(props.schema.Tables).filter((n) => !isMetaTable(n));
                      const from = resolveTable(r.FromTable, tkeys);
                      const to = resolveTable(r.ToTable, tkeys);
                      return (
                        <>
                          <span class={styles.relTable}>{from}</span>
                          <span class={styles.relCol}>[{r.FromColumn}]</span>
                          <span class={styles.relArrow}>→</span>
                          <span class={styles.relTable}>{to}</span>
                          <span class={styles.relCol}>[{r.ToColumn}]</span>
                        </>
                      );
                    })()}
                  </span>
                  <button
                    class={styles.deleteBtn}
                    title="Remove relationship"
                    onClick={(e) => { e.stopPropagation(); props.onDelete(r); }}
                  >−</button>
                </div>
              )}
            </For>
          </Show>
        </div>
      </Show>
    </div>
  );
};

// ─── Measures section ─────────────────────────────────────────────────────────

const MeasuresSection: Component<{
  schema: Schema;
  onAdd: () => void;
  onEdit: (table: string, name: string, expression: string) => void;
  onDelete: (table: string, name: string) => void;
}> = (props) => {
  const [open, setOpen] = createSignal(false);

  type Entry = { table: string; name: string };
  const entries = (): Entry[] => {
    const result: Entry[] = [];
    const measures = props.schema.Measures;
    if (!measures) return result;
    for (const [table, defs] of Object.entries(measures))
      for (const name of Object.keys(defs))
        result.push({ table, name });
    return result.sort((a, b) => a.table.localeCompare(b.table) || a.name.localeCompare(b.name));
  };

  return (
    <div class={styles.sideSection}>
      <button class={styles.sideSectionHeader} onClick={() => setOpen((v) => !v)}>
        <span class={styles.chevron}>{open() ? "▾" : "▸"}</span>
        <span class={styles.sideSectionTitle}>Measures</span>
        <span class={styles.fieldCount}>{entries().length}</span>
        <span
          class={styles.addBtn}
          title="Add measure"
          role="button"
          tabIndex={0}
          onClick={(e) => { e.stopPropagation(); props.onAdd(); }}
          onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.stopPropagation(); props.onAdd(); } }}
        >+</span>
      </button>
      <Show when={open()}>
        <div class={styles.sideSectionBody}>
          <Show
            when={entries().length > 0}
            fallback={<div class={styles.sideSectionEmpty}>No measures defined</div>}
          >
            <For each={entries()}>
              {(m) => (
                <div
                  class={styles.measureRow}
                  onClick={() => {
                    const expr = props.schema.Measures?.[m.table]?.[m.name]?.Expression ?? "";
                    props.onEdit(m.table, m.name, expr);
                  }}
                  style="cursor:pointer"
                >
                  <span class={styles.measureIcon} title="Measure">ƒx</span>
                  <span class={styles.measureName}>{m.table}[{m.name}]</span>
                  <button
                    class={styles.deleteBtn}
                    title="Remove measure"
                    onClick={(e) => { e.stopPropagation(); props.onDelete(m.table, m.name); }}
                  >−</button>
                </div>
              )}
            </For>
          </Show>
        </div>
      </Show>
    </div>
  );
};

// ─── Schema tree panel ───────────────────────────────────────────────────────

type ModalMode = "measure-add" | "measure-edit" | "relationship-add" | "relationship-edit";

const SchemaTree: Component<{ refetchSignal?: Accessor<number>; showHidden?: boolean }> = (props) => {
  const client = useDuxClient();
  const [schema, { refetch }] = createResource(() => client.fetchSchema());

  // Re-fetch when the parent bumps the signal (e.g. after POST /refresh).
  createEffect(on(() => props.refetchSignal?.(), () => refetch(), { defer: true }));

  const [modal, setModal] = createSignal<ModalMode | null>(null);
  const [editTarget, setEditTarget] = createSignal<EditTarget | undefined>(undefined);
  const [relEditTarget, setRelEditTarget] = createSignal<Relationship | undefined>(undefined);

  const tableNames = () => {
    const s = schema();
    if (!s) return [];
    return Object.keys(s.Tables)
      .filter((n) => !isMetaTable(n))
      .filter((n) => props.showHidden || !s.Tables[n].Hidden)
      .sort();
  };

  const [deleteError, setDeleteError] = createSignal("");

  async function toggleTableHidden(name: string) {
    const s = schema();
    if (!s) return;
    try {
      if (s.Tables[name]?.Hidden) {
        await client.clearHidden(name);
      } else {
        await client.setHidden(name);
      }
      setDeleteError("");
      refetch();
    } catch (e) {
      setDeleteError(`Failed to toggle hidden: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function toggleColumnHidden(name: string, col: string) {
    const s = schema();
    if (!s) return;
    try {
      if (s.Tables[name]?.Columns[col]?.Hidden) {
        await client.clearHidden(name, col);
      } else {
        await client.setHidden(name, col);
      }
      setDeleteError("");
      refetch();
    } catch (e) {
      setDeleteError(`Failed to toggle hidden: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function deleteMeasure(table: string, name: string) {
    try {
      await client.deleteMeasure(table, name);
      setDeleteError("");
      refetch();
    } catch (e) {
      setDeleteError(`Failed to delete measure: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  async function deleteRelationship(r: { FromTable: string; FromColumn: string; ToTable: string; ToColumn: string }) {
    try {
      await client.deleteRelationship({
        from_table: r.FromTable, from_column: r.FromColumn,
        to_table: r.ToTable, to_column: r.ToColumn,
      });
      setDeleteError("");
      refetch();
    } catch (e) {
      setDeleteError(`Failed to delete relationship: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  function openMeasureAdd() { setEditTarget(undefined); setModal("measure-add"); }
  function openMeasureEdit(table: string, name: string, expression: string) {
    setEditTarget({ table, name, expression }); setModal("measure-edit");
  }
  function openRelEdit(r: Relationship) { setRelEditTarget(r); setModal("relationship-edit"); }
  function closeModal() { setModal(null); }
  function saved() { closeModal(); refetch(); }

  return (
    <div class={styles.panel}>
      <div class={styles.panelHeader}>Schema</div>
      <div class={styles.scrollArea}>
        <Show when={schema.loading}>
          <div class={styles.status}>Loading…</div>
        </Show>
        <Show when={schema.error}>
          <div class={styles.statusError}>
            {(schema.error as Error).message}
          </div>
        </Show>
        <Show when={schema()}>
          <For each={tableNames()}>
            {(name) => (
              <TableGroup
                tableName={name}
                schema={schema()!}
                showHidden={props.showHidden}
                onToggleHidden={() => toggleTableHidden(name)}
                onToggleColumnHidden={(col) => toggleColumnHidden(name, col)}
              />
            )}
          </For>
        </Show>
      </div>
      <Show when={schema()}>
        <RelationshipsSection
          schema={schema()!}
          onAdd={() => { setRelEditTarget(undefined); setModal("relationship-add"); }}
          onEdit={openRelEdit}
          onDelete={deleteRelationship}
        />
        <MeasuresSection
          schema={schema()!}
          onAdd={openMeasureAdd}
          onEdit={openMeasureEdit}
          onDelete={deleteMeasure}
        />
      </Show>
      <Show when={deleteError()}>
        <div class={styles.statusError} style="padding:6px 12px;font-size:11px">{deleteError()}</div>
      </Show>
      <Show when={(modal() === "measure-add" || modal() === "measure-edit") && schema()}>
        <AddMeasureModal
          schema={schema()!}
          initial={editTarget()}
          onClose={closeModal}
          onSaved={saved}
        />
      </Show>
      <Show when={(modal() === "relationship-add" || modal() === "relationship-edit") && schema()}>
        <AddRelationshipModal
          schema={schema()!}
          initial={relEditTarget()}
          onClose={closeModal}
          onSaved={saved}
        />
      </Show>
    </div>
  );
};

export default SchemaTree;
