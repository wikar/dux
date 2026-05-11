import { createSignal, For, Show } from "solid-js";
import type { Component } from "solid-js";
import type { DropField, FilterField, DragPayload } from "dux-client";
import FieldPill from "./FieldPill";
import styles from "./DropZone.module.css";

type DropZoneProps =
  | {
      label: string;
      id: "fields";
      items: DropField[];
      onDrop: (payload: DragPayload) => void;
      onRemove: (idx: number) => void;
      onAggChange: (idx: number, agg: DropField["aggregate"]) => void;
      onReorder: (from: number, to: number) => void;
    }
  | {
      label: string;
      id: "filters";
      items: FilterField[];
      onDrop: (payload: DragPayload) => void;
      onRemove: (idx: number) => void;
      onValueChange: (idx: number, value: string) => void;
      onReorder: (from: number, to: number) => void;
    };

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
          when={(props.items as unknown[]).length > 0}
          fallback={<div class={styles.empty}>Drag fields here</div>}
        >
          <For each={props.items as Array<DropField | FilterField>}>
            {(item, idx) => (
              <FieldPill
                field={item}
                zone={props.id}
                index={idx()}
                onRemove={() => props.onRemove(idx())}
                onReorder={(to) => props.onReorder(idx(), to)}
                onAggChange={
                  props.id === "fields"
                    ? (agg) =>
                        (props as Extract<DropZoneProps, { id: "fields" }>).onAggChange(
                          idx(),
                          agg
                        )
                    : undefined
                }
                onValueChange={
                  props.id === "filters"
                    ? (val) =>
                        (props as Extract<DropZoneProps, { id: "filters" }>).onValueChange(
                          idx(),
                          val
                        )
                    : undefined
                }
              />
            )}
          </For>
        </Show>
      </div>
    </div>
  );
};

export default DropZone;
