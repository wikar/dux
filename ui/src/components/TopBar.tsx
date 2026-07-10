import type { Component, JSX } from "solid-js";
import styles from "./TopBar.module.css";

/** Outline eye-off icon (slashed eye) for the "Show hidden" toggle. */
const EyeOffIcon: Component = () => (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94" />
    <path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19" />
    <line x1="1" y1="1" x2="23" y2="23" />
  </svg>
);

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
