import { createMemo, createSignal, createEffect } from "solid-js";
import { createStore, produce } from "solid-js/store";
import type { Component } from "solid-js";
import type { DropField, FilterField, DragPayload, Aggregate } from "./types/schema";
import { generateQuery } from "./utils/generateQuery";
import SchemaTree from "./components/SchemaTree";
import DropZone from "./components/DropZone";
import QueryPreview from "./components/QueryPreview";
import ResultTable from "./components/ResultTable";
import styles from "./App.module.css";

const isNumeric = (dt: string) =>
  /^(TINYINT|SMALLINT|INTEGER|BIGINT|HUGEINT|DOUBLE|FLOAT|REAL|DECIMAL|NUMERIC)/i.test(dt);

const App: Component = () => {
  const [state, setState] = createStore<{ fields: DropField[]; filters: FilterField[] }>({
    fields: [],
    filters: [],
  });

  const query = createMemo(() => generateQuery(state.fields, state.filters));

  // activeQuery mirrors what is shown in the Query panel.
  // committedQuery is what ResultTable actually executes.
  // When the generated query changes (drag-drop), both update and isDirty resets.
  // When the user edits manually, only activeQuery updates and isDirty becomes true.
  const [activeQuery, setActiveQuery] = createSignal("");
  const [committedQuery, setCommittedQuery] = createSignal("");
  const [isDirty, setIsDirty] = createSignal(false);

  createEffect(() => {
    const q = query();
    setActiveQuery(q);
    setCommittedQuery(q);
    setIsDirty(false);
  });

  function handleQueryChange(q: string) {
    setActiveQuery(q);
    setIsDirty(true);
  }

  function commitQuery() {
    setCommittedQuery(activeQuery());
    setIsDirty(false);
  }

  // A field is a "metric" if it's a pre-defined measure or a numeric column.
  // Non-metric (group-by) columns always sort before metrics.
  const isMetric = (f: DropField) =>
    f.kind === "measure" || (f.kind === "column" && isNumeric(f.dataType));

  function addToFields(payload: DragPayload) {
    if (state.fields.some((f) => f.table === payload.table && f.name === payload.name)) return;
    const field: DropField = {
      table: payload.table,
      name: payload.name,
      kind: payload.kind,
      dataType: payload.dataType,
      aggregate: payload.kind === "column" && isNumeric(payload.dataType) ? "SUM" : undefined,
    };
    if (isMetric(field)) {
      // Metrics go at the end.
      setState("fields", (f) => [...f, field]);
    } else {
      // Non-metric columns insert before the first metric.
      setState("fields", (f) => {
        const insertAt = f.findIndex(isMetric);
        if (insertAt === -1) return [...f, field];
        const next = [...f];
        next.splice(insertAt, 0, field);
        return next;
      });
    }
  }

  function addToFilters(payload: DragPayload) {
    if (state.filters.some((f) => f.table === payload.table && f.name === payload.name)) return;
    const filter: FilterField = {
      table: payload.table,
      name: payload.name,
      dataType: payload.dataType,
      value: "",
    };
    setState("filters", (f) => [...f, filter]);
  }

  function reorderFields(from: number, to: number) {
    const item = state.fields[from];
    // Non-metric columns always precede metrics. Clamp target to enforce this.
    let clamped = to;
    // Count of non-metric items excluding the dragged item.
    const nonMetricCount = state.fields.filter((f, i) => i !== from && !isMetric(f)).length;
    if (isMetric(item)) {
      // Metric: cannot go before a non-metric column.
      clamped = Math.max(to, nonMetricCount);
    } else {
      // Non-metric: cannot go after a metric.
      clamped = Math.min(to, nonMetricCount);
    }
    if (clamped === from) return;
    setState("fields", produce((arr) => {
      const [moved] = arr.splice(from, 1);
      arr.splice(clamped, 0, moved);
    }));
  }

  function reorderFilters(from: number, to: number) {
    setState("filters", produce((arr) => {
      const [item] = arr.splice(from, 1);
      arr.splice(to, 0, item);
    }));
  }

  return (
    <div class={styles.layout}>
      {/* Column 1 — Schema tree */}
      <div class={styles.col1}>
        <SchemaTree />
      </div>

      {/* Column 2 — Drop zones */}
      <div class={styles.col2}>
        <DropZone
          label="Columns / Measures"
          id="fields"
          items={state.fields}
          onDrop={addToFields}
          onRemove={(i) => setState("fields", produce((f) => { f.splice(i, 1); }))}
          onAggChange={(i, agg) => setState("fields", i, "aggregate", agg as Aggregate)}
          onReorder={reorderFields}
        />
        <DropZone
          label="Filters"
          id="filters"
          items={state.filters}
          onDrop={addToFilters}
          onRemove={(i) => setState("filters", produce((f) => { f.splice(i, 1); }))}
          onValueChange={(i, val) => setState("filters", i, "value", val)}
          onReorder={reorderFilters}
        />
      </div>

      {/* Column 3 — Query + results */}
      <div class={styles.col3}>
        <QueryPreview query={activeQuery()} isDirty={isDirty()} onQueryChange={handleQueryChange} onRun={commitQuery} />
        <ResultTable query={committedQuery()} />
      </div>
    </div>
  );
};

export default App;

