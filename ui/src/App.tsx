import { createMemo, createSignal, createEffect, Show } from "solid-js";
import { createStore, produce } from "solid-js/store";
import type { Component } from "solid-js";
import type { DropField, FilterField, DragPayload, Aggregate } from "./types/schema";
import { generateQuery } from "./utils/generateQuery";
import SchemaTree from "./components/SchemaTree";
import DropZone from "./components/DropZone";
import QueryPreview from "./components/QueryPreview";
import ResultTable from "./components/ResultTable";
import TopBar from "./components/TopBar";
import type { Tab } from "./components/TopBar";
import Explorer from "./components/Explorer";
import styles from "./App.module.css";

const isNumeric = (dt: string) =>
  /^(TINYINT|SMALLINT|INTEGER|BIGINT|HUGEINT|DOUBLE|FLOAT|REAL|DECIMAL|NUMERIC)/i.test(dt);

const TABS: Tab[] = [
  { id: "home", label: "Home" },
  { id: "explorer", label: "Explorer" },
];

const App: Component = () => {
  const [activeTab, setActiveTab] = createSignal("home");
  let importInputRef: HTMLInputElement | undefined;
  const [refreshCount, setRefreshCount] = createSignal(0);
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
    f.kind === "measure" || (f.kind === "column" && isNumeric(f.dataType) && f.aggregate !== "VALUES");

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

  function handleAggChange(i: number, agg: Aggregate) {
    const updated: DropField = { ...state.fields[i], aggregate: agg };
    const wasMetric = isMetric(state.fields[i]);
    const nowMetric = isMetric(updated);
    if (wasMetric === nowMetric) {
      // No positional change needed — just update the value.
      setState("fields", i, "aggregate", agg);
      return;
    }
    // The field crossed the dimension/metric boundary — update and reposition.
    setState("fields", produce((arr) => {
      arr[i] = updated;
      const [item] = arr.splice(i, 1);
      if (nowMetric) {
        // Became a metric: move to after the last non-metric.
        arr.push(item);
      } else {
        // Became a dimension (VALUES): insert before the first metric.
        const insertAt = arr.findIndex(isMetric);
        if (insertAt === -1) arr.push(item);
        else arr.splice(insertAt, 0, item);
      }
    }));
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

  function handleExportToml() {
    const a = document.createElement("a");
    a.href = "/export";
    a.download = "dux.toml";
    a.click();
  }

  async function handleImportFile(e: Event) {
    const file = (e.currentTarget as HTMLInputElement).files?.[0];
    if (!file) return;
    const text = await file.text();
    await fetch("/import", {
      method: "POST",
      headers: { "Content-Type": "text/plain" },
      body: text,
    });
    (e.currentTarget as HTMLInputElement).value = "";
  }

  async function handleRefresh() {
    await fetch("/refresh", { method: "POST" });
    setRefreshCount((c) => c + 1);
  }

  const homeActions = () => (
    <>
      <button class={styles.actionBtn} onClick={handleRefresh}>Refresh Schema</button>
      <button class={styles.actionBtn} onClick={handleExportToml}>Export TOML</button>
      <button class={styles.actionBtn} onClick={() => importInputRef?.click()}>Import TOML</button>
      <input
        ref={importInputRef}
        type="file"
        accept=".toml"
        style="display:none"
        onChange={handleImportFile}
      />
    </>
  );

  return (
    <div class={styles.appShell}>
      <TopBar
        tabs={TABS}
        activeTab={activeTab()}
        onTabChange={setActiveTab}
        actions={activeTab() === "home" ? homeActions() : undefined}
      />
      <Show when={activeTab() === "home"}>
        <div class={styles.layout}>
      {/* Column 1 — Schema tree */}
      <div class={styles.col1}>
        <SchemaTree refetchSignal={refreshCount} />
      </div>

      {/* Column 2 — Drop zones */}
      <div class={styles.col2}>
        <DropZone
          label="Columns / Measures"
          id="fields"
          items={state.fields}
          onDrop={addToFields}
          onRemove={(i) => setState("fields", produce((f) => { f.splice(i, 1); }))}
          onAggChange={(i, agg) => handleAggChange(i, agg as Aggregate)}
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
      </Show>
      <Show when={activeTab() === "explorer"}>
        <Explorer />
      </Show>
    </div>
  );
};

export default App;

