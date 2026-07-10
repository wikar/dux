import { createSignal, For, Show } from "solid-js";
import type { Component } from "solid-js";
import type { DropField, FilterField, DragPayload, FilterOp } from "../dux/types";
import FieldPill from "./FieldPill";
import styles from "./DropZone.module.css";

interface DropZoneProps {
  label: string;
  id: "fields" | "filters";
  items: (DropField | FilterField)[];
  onDrop: (payload: DragPayload) => void;
  onRemove: (idx: number) => void;
  onAggChange?: (idx: number, agg: DropField["aggregate"]) => void;
  onValueChange?: (idx: number, value: string) => void;
  onOpChange?: (idx: number, op: FilterOp) => void;
  onReorder: (from: number, to: number) => void;
}

const DropZone: Component<DropZoneProps> = (props) => {
  const [over, setOver] = createSignal(false);

  function handleDragOver(e: DragEvent) {
    if (e.dataTransfer?.types.includes("application/dux")) {
      e.preventDefault();
      e.dataTransfer.dropEffect = "copy";
      setOver(true);
    }
  }

  function handleDragLeave() {
    setOver(false);
  }

  function handleDrop(e: DragEvent) {
    e.preventDefault();
    setOver(false);
    const raw = e.dataTransfer?.getData("application/dux");
    if (!raw) return;
    try {
      const payload = JSON.parse(raw) as DragPayload;
      props.onDrop(payload);
    } catch {}
  }

  return (
    <div
      class={styles.zone}
      classList={{ [styles.over]: over() }}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <div class={styles.label}>{props.label}</div>
      <div class={styles.pillList}>
        <Show
          when={props.items.length > 0}
          fallback={<div class={styles.empty}>Drag fields here</div>}
        >
          <For each={props.items}>
            {(item, idx) => (
              <FieldPill
                field={item}
                zone={props.id}
                index={idx()}
                onRemove={() => props.onRemove(idx())}
                onReorder={(to) => props.onReorder(idx(), to)}
                onAggChange={(agg) => props.onAggChange?.(idx(), agg)}
                onValueChange={(val) => props.onValueChange?.(idx(), val)}
                onOpChange={(op) => props.onOpChange?.(idx(), op)}
              />
            )}
          </For>
        </Show>
      </div>
    </div>
  );
};

export default DropZone;
