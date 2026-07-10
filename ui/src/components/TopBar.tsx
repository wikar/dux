import type { Component, JSX } from "solid-js";
import styles from "./TopBar.module.css";
import { EyeOffIcon } from "./icons";

interface Props {
  activeTab: string;
  onTabChange: (id: string) => void;
  showHidden: boolean;
  onToggleShowHidden: () => void;
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
      <div class={styles.actions}>
        <button
          class={styles.actionBtn}
          classList={{ [styles.actionBtnActive]: props.showHidden }}
          title={props.showHidden ? "Hide hidden tables and columns" : "Show hidden tables and columns"}
          onClick={() => props.onToggleShowHidden()}
        >
          <EyeOffIcon />
          Show hidden
        </button>
        {props.actions}
      </div>
      <img src="/dux_logo_20x20.png" alt="DUX" class={styles.logo} />
    </div>
  );
};

export default TopBar;
