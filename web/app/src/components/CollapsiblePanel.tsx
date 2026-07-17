import { useState, type ReactNode } from "react";
import styles from "./CollapsiblePanel.module.css";

interface Props {
  title: string;
  /** Which edge the panel sits on — decides border and chevron direction. */
  side: "left" | "right";
  /** Expanded width in px. */
  width: number;
  /** localStorage key persisting the collapsed state. */
  storageKey: string;
  children: ReactNode;
}

/** Side panel in the builder's panel idiom (uppercase header, surface1
 *  borders) that collapses to a thin rail to give the center pane room. */
export default function CollapsiblePanel({ title, side, width, storageKey, children }: Props) {
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(storageKey) === "1");

  const toggle = () => {
    setCollapsed((c) => {
      localStorage.setItem(storageKey, c ? "0" : "1");
      return !c;
    });
  };

  const sideClass = side === "left" ? styles.left : styles.right;
  // "‹ ›" point toward where the panel goes when collapsing.
  const chevron = collapsed !== (side === "right") ? "›" : "‹";

  if (collapsed) {
    return (
      <button
        className={`${styles.rail} ${sideClass}`}
        title={`Expand ${title}`}
        onClick={toggle}
      >
        <span className={styles.railChevron}>{chevron}</span>
        <span className={styles.railTitle}>{title}</span>
      </button>
    );
  }

  return (
    <div className={`${styles.panel} ${sideClass}`} style={{ width }}>
      <div className={styles.header}>
        <span className={styles.title}>{title}</span>
        <button className={styles.collapseBtn} title={`Collapse ${title}`} onClick={toggle}>
          {chevron}
        </button>
      </div>
      <div className={styles.body}>{children}</div>
    </div>
  );
}
