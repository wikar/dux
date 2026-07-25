import styles from "./ElementsPane.module.css";
import { addElement, swapElementType } from "../docOps";
import { useDocStore, useUiStore } from "../store";
import type { ElementType } from "../types";
import { QUERY_TYPES, VISUALS, VISUAL_TYPES } from "../visuals";
import Settings from "./Settings";

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
        {/* Registry-driven: a new visual appears here in declaration order. */}
        <div className={styles.palette}>
          {VISUAL_TYPES.map((t) => {
            const meta = VISUALS[t];
            return (
              <button
                key={t}
                className={styles.paletteBtn}
                title={converts(t) ? `Convert ${selected!.id} to ${meta.label.toLowerCase()}` : meta.label}
                onClick={() => onType(t)}
              >
                {meta.icon}
                <span>{meta.label.split(" ")[0]}</span>
              </button>
            );
          })}
        </div>
      </div>
      <Settings el={selected} />
    </div>
  );
}
