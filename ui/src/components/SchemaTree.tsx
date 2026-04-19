import { createSignal, createResource, For, Show } from "solid-js";
import type { Component } from "solid-js";
import type { Schema, DragPayload } from "../types/schema";
import TypeIcon from "./TypeIcon";
import styles from "./SchemaTree.module.css";

async function fetchSchema(): Promise<Schema> {
  const res = await fetch("/schema");
  if (!res.ok) throw new Error(`schema fetch failed: ${res.status}`);
  return res.json();
}

// ─── Draggable field row ─────────────────────────────────────────────────────

const FieldRow: Component<{ payload: DragPayload }> = (props) => {
  function handleDragStart(e: DragEvent) {
    e.dataTransfer!.effectAllowed = "copy";
    e.dataTransfer!.setData("application/dux", JSON.stringify(props.payload));
  }

  return (
    <div
      draggable={true}
      class={styles.fieldRow}
      onDragStart={handleDragStart}
    >
      <Show when={props.payload.kind === "measure"} fallback={
        <TypeIcon dataType={props.payload.dataType} />
      }>
        <span class={styles.measureIcon} title="Measure">ƒx</span>
      </Show>
      <span class={styles.fieldName}>{props.payload.name}</span>
    </div>
  );
};

// ─── Collapsible table group ─────────────────────────────────────────────────

const TableGroup: Component<{
  tableName: string;
  schema: Schema;
}> = (props) => {
  const [open, setOpen] = createSignal(false);

  const columns = () => {
    const tbl = props.schema.Tables[props.tableName];
    if (!tbl) return [];
    return Object.values(tbl.Columns).sort((a, b) => a.Name.localeCompare(b.Name));
  };

  const measures = () => {
    const tblMeasures = props.schema.Measures?.[props.tableName];
    if (!tblMeasures) return [];
    return Object.keys(tblMeasures).sort();
  };

  const total = () => columns().length + measures().length;

  return (
    <div class={styles.tableGroup}>
      <button
        class={styles.tableHeader}
        onClick={() => setOpen((v) => !v)}
        title={`${total()} fields`}
      >
        <span class={styles.chevron}>{open() ? "▾" : "▸"}</span>
        <span class={styles.tableName}>{props.tableName}</span>
        <span class={styles.fieldCount}>{total()}</span>
      </button>

      <Show when={open()}>
        <div class={styles.fieldList}>
          <For each={columns()}>
            {(col) => (
              <FieldRow payload={{
                table: props.tableName,
                name: col.Name,
                kind: "column",
                dataType: col.DataType,
              }} />
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

// ─── Schema tree panel ───────────────────────────────────────────────────────

const SchemaTree: Component = () => {
  const [schema] = createResource(fetchSchema);

  const tableNames = () =>
    Object.keys(schema()?.Tables ?? {}).sort();

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
            {(name) => <TableGroup tableName={name} schema={schema()!} />}
          </For>
        </Show>
      </div>
    </div>
  );
};

export default SchemaTree;
