import type { Component, JSX } from "solid-js";
import styles from "./TopBar.module.css";

interface Props {
  activeTab: string;
  onTabChange: (id: string) => void;
  actions?: JSX.Element;
}

const TopBar: Component<Props> = (props) => {
  const tabClass = (id: string) =>
    `${styles.tab}${props.activeTab === id ? ` ${styles.tabActive}` : ""}`;

  return (
    <div class={styles.topBar}>
      <nav class={styles.tabs}>
        <button class={tabClass("home")} onClick={() => props.onTabChange("home")}>
          Home
        </button>
        <button class={tabClass("explorer")} onClick={() => props.onTabChange("explorer")}>
          Explorer
        </button>
      </nav>
      <div class={styles.spacer} />
      <div class={styles.actions}>{props.actions}</div>
      <img src="/dux_logo_20x20.png" alt="DUX" class={styles.logo} />
    </div>
  );
};

export default TopBar;
