import styles from "./ElementsPane.module.css";
import { addElement, swapElementType, TYPE_LABEL } from "../docOps";
import { useDocStore, useUiStore } from "../store";
import type { ElementType } from "../types";
import { QUERY_TYPES } from "../types";
import Settings from "./Settings";
import { typeIcon } from "./typeIcons";

const TYPES: ElementType[] = [
  "bar", "line", "combo", "area", "donut", "table", "pivot", "kpi", "slicer", "text", "image",
];

export default function ElementsPane() {
  const selectedId = useUiStore((s) => s.selectedId);
  const selected = useDocStore((s) =>
    selectedId ? s.doc?.elements.find((e) => e.id === selectedId) ?? null : null
  );

  // With a query element selected, palette clicks convert it (PBI-style);
  // deselect (Esc / click the canvas) to add a new element instead.
  const converts = (t: ElementType) =>
    !!selected && QUERY_TYPES.has(selected.type) && QUERY_TYPES.has(t) && selected.type !== t;

  const onType = (t: ElementType) => {
    if (converts(t)) swapElementType(selected!.id, t);
    else if (!selected || !QUERY_TYPES.has(selected.type) || !QUERY_TYPES.has(t)) addElement(t);
  };

  return (
    <div className={styles.pane}>
      <div className={styles.section}>
        <div className={styles.palette}>
          {TYPES.map((t) => (
            <button
              key={t}
              className={styles.paletteBtn}
              title={converts(t) ? `Convert ${selected!.id} to ${TYPE_LABEL[t].toLowerCase()}` : TYPE_LABEL[t]}
              onClick={() => onType(t)}
            >
              {typeIcon(t)}
              <span>{TYPE_LABEL[t].split(" ")[0]}</span>
            </button>
          ))}
        </div>
      </div>
      <Settings el={selected} />
    </div>
  );
}
