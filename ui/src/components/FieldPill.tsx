import { Show } from "solid-js";
import type { Component } from "solid-js";
import type { DropField, FilterField, Aggregate } from "dux-client";
import TypeIcon from "./TypeIcon";
import styles from "./FieldPill.module.css";

const AGGREGATES: Aggregate[] = ["SUM", "COUNT", "AVERAGE", "MIN", "MAX", "DISTINCTCOUNT", "VALUES"];

const isNumeric = (dt: string) =>
  /^(TINYINT|SMALLINT|INTEGER|BIGINT|HUGEINT|DOUBLE|FLOAT|REAL|DECIMAL|NUMERIC)/i.test(dt);

interface FieldPillProps {
  field: DropField | FilterField;
  zone: "fields" | "filters";
  index: number;
  onRemove: () => void;
  onReorder: (toIndex: number) => void;
  onAggChange?: (agg: Aggregate) => void;
  onValueChange?: (val: string) => void;
}

const FieldPill: Component<FieldPillProps> = (props) => {
  const field = () => props.field as DropField & FilterField;

  // Reorder via drag within the same zone — encode index in dataTransfer
  function handleDragStart(e: DragEvent) {
    e.dataTransfer!.effectAllowed = "move";
    e.dataTransfer!.setData("application/dux-index", String(props.index));
  }

  function handleDragOver(e: DragEvent) {
    if (e.dataTransfer?.types.includes("application/dux-index")) {
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
    }
  }

  function handleDrop(e: DragEvent) {
    e.preventDefault();
    e.stopPropagation();
    const fromStr = e.dataTransfer?.getData("application/dux-index");
    if (fromStr === undefined || fromStr === "") return;
    const from = Number(fromStr);
    if (!isNaN(from) && from !== props.index) {
      props.onReorder(from);
    }
  }

  return (
    <div
      draggable={true}
      class={styles.pill}
      onDragStart={handleDragStart}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      {/* drag handle visual */}
      <span class={styles.handle}>⠿</span>

      {/* icon */}
      <Show when={field().kind === "measure"} fallback={
        <TypeIcon dataType={field().dataType ?? ""} />
      }>
        <span class={styles.measureIcon}>ƒx</span>
      </Show>

      {/* name */}
      <span class={styles.name}>{field().name}</span>

      {/* aggregate selector (fields zone, numeric column) */}
      <Show when={
        props.zone === "fields" &&
        field().kind === "column" &&
        isNumeric(field().dataType ?? "")
      }>
        <select
          class={styles.aggSelect}
          value={field().aggregate ?? "SUM"}
          onChange={(e) => props.onAggChange?.(e.currentTarget.value as Aggregate)}
        >
          {AGGREGATES.map((a) => <option value={a}>{a}</option>)}
        </select>
      </Show>

      {/* value input (filters zone) — comma-separated values for TREATAS */}
      <Show when={props.zone === "filters"}>
        <span class={styles.eq}>∈</span>
        <input
          class={styles.filterInput}
          type="text"
          placeholder="val1, val2, …"
          value={field().value ?? ""}
          onInput={(e) => props.onValueChange?.(e.currentTarget.value)}
        />
      </Show>

      {/* remove */}
      <button class={styles.remove} onClick={props.onRemove} title="Remove">×</button>
    </div>
  );
};

export default FieldPill;
