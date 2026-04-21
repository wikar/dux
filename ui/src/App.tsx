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

  // activeQuery is what gets sent to the server. Resets to the generated query
  // whenever fields/filters change, but the user can freely edit it manually.
  const [activeQuery, setActiveQuery] = createSignal("");
  createEffect(() => { setActiveQuery(query()); });

  function addToFields(payload: DragPayload) {
    if (state.fields.some((f) => f.table === payload.table && f.name === payload.name)) return;
    const field: DropField = {
      table: payload.table,
      name: payload.name,
      kind: payload.kind,
      dataType: payload.dataType,
      aggregate: payload.kind === "column" && isNumeric(payload.dataType) ? "SUM" : undefined,
    };
    setState("fields", (f) => [...f, field]);
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
    setState("fields", produce((arr) => {
      const [item] = arr.splice(from, 1);
      arr.splice(to, 0, item);
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
        <QueryPreview query={activeQuery()} onQueryChange={setActiveQuery} />
        <ResultTable query={activeQuery()} />
      </div>
    </div>
  );
};

export default App;

