import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import type { ChangeEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { duxClient as client, generateQuery, isMetricField, isNumeric } from "@dux/core";
import type { QueryFailedError, DropField, FilterField, DragPayload, Aggregate, FilterOp } from "@dux/core";
import SchemaTree from "./components/SchemaTree";
import DropZone from "./components/DropZone";
import QueryPreview from "./components/QueryPreview";
import ResultTable from "./components/ResultTable";
import TopBar from "./components/TopBar";
import { dashPathFromPathname, navigate, tabFromPathname, usePathname } from "./router";
// The dash section is composed here and only here — src/components must never
// import from src/dash, so a future dash-only duxuid build can ship
// src/dash + src/components behind its own entry point.
//
// Dash (Recharts, markdown, TanStack) and Explorer are code-split:
// they load on first tab visit, keeping the entry chunk to the Home
// workspace. Only the small dash store/api modules stay in the entry (the
// top bar needs the fullscreen flag and last-open path before the chunk
// loads).
import { encodePath } from "./dash/api";
import { useUiStore } from "./dash/store";
import styles from "./App.module.css";

const Explorer = lazy(() => import("./components/Explorer"));
const DashApp = lazy(() => import("./dash/DashApp"));
const DashActions = lazy(() => import("./dash/components/DashActions"));

// A field is a "metric" if it's a pre-defined measure or a numeric column
// (isMetricField from @dux/core). Non-metric (group-by) columns sort first.

export default function App() {
  const queryClient = useQueryClient();
  const pathname = usePathname();
  const tab = tabFromPathname(pathname);
  const dashPath = dashPathFromPathname(pathname);
  const { data: version } = useQuery({ queryKey: ["version"], queryFn: () => client.fetchVersion() });
  const dashEnabled = version?.capabilities?.dashboards === true;

  const dashFullscreen = useUiStore((s) => s.fullscreen);
  const importInputRef = useRef<HTMLInputElement>(null);
  const [showHidden, setShowHidden] = useState(false);
  const [includeEmpty, setIncludeEmpty] = useState(false);
  const [queryError, setQueryError] = useState<QueryFailedError | null>(null);
  const [fields, setFields] = useState<DropField[]>([]);
  const [filters, setFilters] = useState<FilterField[]>([]);

  const query = useMemo(() => generateQuery(fields, filters), [fields, filters]);

  // activeQuery mirrors what is shown in the Query panel.
  // committedQuery is what ResultTable actually executes.
  // When the generated query changes (drag-drop), both update and isDirty resets.
  // When the user edits manually, only activeQuery updates and isDirty becomes true.
  const [activeQuery, setActiveQuery] = useState("");
  const [committedQuery, setCommittedQuery] = useState("");
  const [isDirty, setIsDirty] = useState(false);

  useEffect(() => {
    setActiveQuery(query);
    setCommittedQuery(query);
    setIsDirty(false);
  }, [query]);

  function handleQueryChange(q: string) {
    setActiveQuery(q);
    setIsDirty(true);
    // The marker position refers to the text as it was when the query ran.
    setQueryError(null);
  }

  function commitQuery() {
    setCommittedQuery(activeQuery);
    setIsDirty(false);
  }

  // Getter registered by ResultTable; returns the displayed dataset as TSV.
  const resultsTsv = useRef<(() => string | null) | undefined>(undefined);

  async function copyResults(): Promise<boolean> {
    const tsv = resultsTsv.current?.();
    if (!tsv) return false;
    await navigator.clipboard.writeText(tsv);
    return true;
  }

  function addToFields(payload: DragPayload) {
    setFields((prev) => {
      if (prev.some((f) => f.table === payload.table && f.name === payload.name)) return prev;
      const field: DropField = {
        table: payload.table,
        name: payload.name,
        kind: payload.kind,
        dataType: payload.dataType,
        aggregate: payload.kind === "column" && isNumeric(payload.dataType) ? "SUM" : undefined,
      };
      if (isMetricField(field)) {
        // Metrics go at the end.
        return [...prev, field];
      }
      // Non-metric columns insert before the first metric.
      const insertAt = prev.findIndex(isMetricField);
      if (insertAt === -1) return [...prev, field];
      const next = [...prev];
      next.splice(insertAt, 0, field);
      return next;
    });
  }

  function addToFilters(payload: DragPayload) {
    setFilters((prev) => {
      if (prev.some((f) => f.table === payload.table && f.name === payload.name)) return prev;
      const filter: FilterField = {
        table: payload.table,
        name: payload.name,
        dataType: payload.dataType,
        op: "=",
        value: "",
      };
      return [...prev, filter];
    });
  }

  function handleAggChange(i: number, agg: Aggregate) {
    setFields((prev) => {
      const updated: DropField = { ...prev[i], aggregate: agg };
      const wasMetric = isMetricField(prev[i]);
      const nowMetric = isMetricField(updated);
      const arr = [...prev];
      arr[i] = updated;
      if (wasMetric === nowMetric) {
        // No positional change needed — just update the value.
        return arr;
      }
      // The field crossed the dimension/metric boundary — update and reposition.
      const [item] = arr.splice(i, 1);
      if (nowMetric) {
        // Became a metric: move to after the last non-metric.
        arr.push(item);
      } else {
        // Became a dimension (VALUES): insert before the first metric.
        const insertAt = arr.findIndex(isMetricField);
        if (insertAt === -1) arr.push(item);
        else arr.splice(insertAt, 0, item);
      }
      return arr;
    });
  }

  function reorderFields(from: number, to: number) {
    setFields((prev) => {
      const item = prev[from];
      // Non-metric columns always precede metrics. Clamp target to enforce this.
      let clamped = to;
      // Count of non-metric items excluding the dragged item.
      const nonMetricCount = prev.filter((f, i) => i !== from && !isMetricField(f)).length;
      if (isMetricField(item)) {
        // Metric: cannot go before a non-metric column.
        clamped = Math.max(to, nonMetricCount);
      } else {
        // Non-metric: cannot go after a metric.
        clamped = Math.min(to, nonMetricCount);
      }
      if (clamped === from) return prev;
      const arr = [...prev];
      const [moved] = arr.splice(from, 1);
      arr.splice(clamped, 0, moved);
      return arr;
    });
  }

  function reorderFilters(from: number, to: number) {
    setFilters((prev) => {
      const arr = [...prev];
      const [item] = arr.splice(from, 1);
      arr.splice(to, 0, item);
      return arr;
    });
  }

  function handleExportToml() {
    const a = document.createElement("a");
    a.href = client.exportTomlUrl();
    a.download = "dux.toml";
    a.click();
  }

  async function handleImportFile(e: ChangeEvent<HTMLInputElement>) {
    const input = e.currentTarget;
    const file = input.files?.[0];
    if (!file) return;
    const text = await file.text();
    await client.importToml(text);
    input.value = "";
  }

  async function handleRefresh() {
    await client.refresh();
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["schema"] }),
      queryClient.invalidateQueries({ queryKey: ["measures"] }),
    ]);
  }

  function handleTabChange(id: string) {
    if (id === "home") navigate("/");
    else if (id === "explorer") navigate("/explorer");
    // Dash returns to the dashboard that was last open in this session.
    else if (id === "dash") {
      const last = useUiStore.getState().path;
      navigate(last ? `/dash/${encodePath(last)}` : "/dash/");
    }
  }

  const homeActions = (
    <>
      <button className={styles.actionBtn} onClick={handleRefresh}>Refresh Schema</button>
      <button className={styles.actionBtn} onClick={handleExportToml}>Export TOML</button>
      <button className={styles.actionBtn} onClick={() => importInputRef.current?.click()}>Import TOML</button>
      <input
        ref={importInputRef}
        type="file"
        accept=".toml"
        style={{ display: "none" }}
        onChange={handleImportFile}
      />
    </>
  );

  const explorerActions = (
    <button className={styles.actionBtn} onClick={handleRefresh}>Refresh Schema</button>
  );

  // Chrome-less full-screen dash view: no top bar, canvas only.
  const chromeless = tab === "dash" && dashFullscreen;

  return (
    <div className={styles.appShell}>
      {!chromeless && (
        <TopBar
          activeTab={tab}
          onTabChange={handleTabChange}
          showDash={dashEnabled}
          showHidden={showHidden}
          onToggleShowHidden={() => setShowHidden((v) => !v)}
          actions={
            tab === "home" ? homeActions
            : tab === "explorer" ? explorerActions
            : dashEnabled ? <Suspense fallback={null}><DashActions /></Suspense>
            : undefined
          }
        />
      )}
      {/* The Home workspace stays mounted so switching tabs never loses the
          in-progress query; Explorer and Dash manage their own state. */}
      <div
        className={styles.layout}
        style={tab === "home" ? undefined : { display: "none" }}
      >
        {/* Column 1 — Schema tree */}
        <div className={styles.col1}>
          <SchemaTree showHidden={showHidden} />
        </div>

        {/* Column 2 — Drop zones */}
        <div className={styles.col2}>
          <DropZone
            label="Columns / Measures"
            id="fields"
            items={fields}
            onDrop={addToFields}
            onRemove={(i) => setFields((prev) => prev.filter((_, idx) => idx !== i))}
            onAggChange={(i, agg) => handleAggChange(i, agg as Aggregate)}
            onReorder={reorderFields}
          />
          <DropZone
            label="Filters"
            id="filters"
            items={filters}
            onDrop={addToFilters}
            onRemove={(i) => setFilters((prev) => prev.filter((_, idx) => idx !== i))}
            onValueChange={(i, val) =>
              setFilters((prev) => prev.map((f, idx) => (idx === i ? { ...f, value: val } : f)))}
            onOpChange={(i, op) =>
              setFilters((prev) => prev.map((f, idx) => (idx === i ? { ...f, op: op as FilterOp } : f)))}
            onReorder={reorderFilters}
          />
        </div>

        {/* Column 3 — Query + results */}
        <div className={styles.col3}>
          <QueryPreview
            query={activeQuery}
            isDirty={isDirty}
            onQueryChange={handleQueryChange}
            onRun={commitQuery}
            includeEmpty={includeEmpty}
            onToggleIncludeEmpty={() => setIncludeEmpty((v) => !v)}
            onCopyResults={copyResults}
            error={queryError}
          />
          <ResultTable
            query={committedQuery}
            includeEmpty={includeEmpty}
            registerCopyProvider={(fn) => (resultsTsv.current = fn)}
            onQueryError={setQueryError}
          />
        </div>
      </div>
      {tab === "explorer" && (
        <Suspense fallback={<div className={styles.chunkLoading}>Loading…</div>}>
          <Explorer showHidden={showHidden} />
        </Suspense>
      )}
      {tab === "dash" &&
        (dashEnabled || version === undefined ? (
          <Suspense fallback={<div className={styles.chunkLoading}>Loading…</div>}>
            <DashApp path={dashPath} showHidden={showHidden} />
          </Suspense>
        ) : (
          <div className={styles.dashDisabled}>
            Dashboards are disabled on this server (DUX_DASH=0).
          </div>
        ))}
    </div>
  );
}
