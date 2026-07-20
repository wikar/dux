import type { ReactNode } from "react";
import styles from "./TopBar.module.css";
import { EyeOffIcon } from "./icons";

interface Props {
  activeTab: string;
  onTabChange: (id: string) => void;
  /** Show the Dash tab (capabilities.dashboards from /version). */
  showDash?: boolean;
  showHidden: boolean;
  onToggleShowHidden: () => void;
  actions?: ReactNode;
}

export default function TopBar(props: Props) {
  const tabClass = (id: string) =>
    `${styles.tab}${props.activeTab === id ? ` ${styles.tabActive}` : ""}`;

  return (
    <div className={styles.topBar}>
      <nav className={styles.tabs}>
        <button className={tabClass("home")} onClick={() => props.onTabChange("home")}>
          Home
        </button>
        <button className={tabClass("explorer")} onClick={() => props.onTabChange("explorer")}>
          Explorer
        </button>
        {props.showDash && (
          <button className={tabClass("dash")} onClick={() => props.onTabChange("dash")}>
            Dash
          </button>
        )}
      </nav>
      <div className={styles.spacer} />
      <div className={styles.actions}>
        <button
          className={`${styles.actionBtn}${props.showHidden ? ` ${styles.actionBtnActive}` : ""}`}
          title={props.showHidden ? "Hide hidden tables and columns" : "Show hidden tables and columns"}
          onClick={props.onToggleShowHidden}
        >
          <EyeOffIcon />
          Show hidden
        </button>
        {props.actions}
      </div>
      <img src="/dux_logo_20x20.png" alt="DUX" className={styles.logo} />
    </div>
  );
}
