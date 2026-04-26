import type { Component, JSX } from "solid-js";
import { For } from "solid-js";
import styles from "./TopBar.module.css";

export interface Tab {
  id: string;
  label: string;
}

interface Props {
  tabs: Tab[];
  activeTab: string;
  onTabChange: (id: string) => void;
  actions?: JSX.Element;
}

const TopBar: Component<Props> = (props) => {
  return (
    <div class={styles.topBar}>
      <nav class={styles.tabs}>
        <For each={props.tabs}>
          {(tab) => (
            <button
              class={`${styles.tab}${props.activeTab === tab.id ? ` ${styles.tabActive}` : ""}`}
              onClick={() => props.onTabChange(tab.id)}
            >
              {tab.label}
            </button>
          )}
        </For>
      </nav>
      <div class={styles.spacer} />
      <div class={styles.actions}>{props.actions}</div>
    </div>
  );
};

export default TopBar;
