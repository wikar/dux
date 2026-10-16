import { lazy, Suspense, useRef, useState } from "react";
import type { ChangeEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { duxClient as client } from "@dux/core";
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
import HomeWorkspace from "./home/HomeWorkspace";
import { refreshModel } from "./model/modelQueries";
import styles from "./App.module.css";

const ExplorerWorkspace = lazy(() => import("./explorer/ExplorerWorkspace"));
const DashWorkspace = lazy(() => import("./dash/DashWorkspace"));
const DashActions = lazy(() => import("./dash/components/DashActions"));

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
    await refreshModel(queryClient);
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
            : dashEnabled ? <Suspense fallback={null}><DashActions routePath={dashPath} /></Suspense>
            : undefined
          }
        />
      )}
      <HomeWorkspace active={tab === "home"} showHidden={showHidden} />
      {tab === "explorer" && (
        <Suspense fallback={<div className={styles.chunkLoading}>Loading…</div>}>
          <ExplorerWorkspace showHidden={showHidden} />
        </Suspense>
      )}
      {tab === "dash" &&
        (dashEnabled || version === undefined ? (
          <Suspense fallback={<div className={styles.chunkLoading}>Loading…</div>}>
            <DashWorkspace path={dashPath} showHidden={showHidden} />
          </Suspense>
        ) : (
          <div className={styles.dashDisabled}>
            Dashboards are disabled on this server (DUX_DASH=0).
          </div>
        ))}
    </div>
  );
}
