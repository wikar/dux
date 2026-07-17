import styles from "./RightPane.module.css";
import { addElement, TYPE_LABEL } from "../docOps";
import { useDocStore, useUiStore } from "../store";
import type { ElementType } from "../types";
import Settings from "./Settings";
import { typeIcon } from "./typeIcons";

const TYPES: ElementType[] = ["bar", "line", "area", "donut", "table", "pivot", "kpi", "slicer", "text"];

export default function RightPane() {
  const selectedId = useUiStore((s) => s.selectedId);
  const selected = useDocStore((s) =>
    selectedId ? s.doc?.elements.find((e) => e.id === selectedId) ?? null : null
  );

  return (
    <div className={styles.pane}>
      <div className={styles.section}>
        <div className={styles.palette}>
          {TYPES.map((t) => (
            <button key={t} className={styles.paletteBtn} title={TYPE_LABEL[t]} onClick={() => addElement(t)}>
              {typeIcon(t)}
              <span>{TYPE_LABEL[t]}</span>
            </button>
          ))}
        </div>
      </div>
      <Settings el={selected} />
    </div>
  );
}
