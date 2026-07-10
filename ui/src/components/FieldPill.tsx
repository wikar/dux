import { Show, createSignal, createUniqueId } from "solid-js";
import type { Component } from "solid-js";
import type { DropField, FilterField, Aggregate, FilterOp } from "../dux/types";
import { isNumeric } from "../dux/generateQuery";
import { isDateType } from "../dux/schemaHelpers";
import { duxClient } from "../dux/client";
import TypeIcon from "./TypeIcon";
import styles from "./FieldPill.module.css";

const AGGREGATES: Aggregate[] = ["SUM", "COUNT", "AVERAGE", "MIN", "MAX", "DISTINCTCOUNT", "VALUES"];

const COMPARE_OPS: FilterOp[] = ["=", "<>", ">", ">=", "<", "<="];
const TEXT_OPS: FilterOp[] = ["=", "<>", "contains"];

interface FieldPillProps {
  field: DropField | FilterField;
  zone: "fields" | "filters";
  index: number;
  onRemove: () => void;
  onReorder: (toIndex: number) => void;
  onAggChange?: (agg: Aggregate) => void;
  onValueChange?: (val: string) => void;
  onOpChange?: (op: FilterOp) => void;
}

const FieldPill: Component<FieldPillProps> = (props) => {
  const field = () => props.field as DropField & FilterField;
  const listId = createUniqueId();
  const [suggestions, setSuggestions] = createSignal<string[]>([]);
  let suggestTimer: ReturnType<typeof setTimeout> | undefined;

  // Fetch distinct column values (debounced) to feed the datalist. For a
  // multi-value "=" filter, suggest completions for the term after the last comma.
  function loadSuggestions(raw: string) {
    if (props.zone !== "filters") return;
    const term = raw.split(",").pop()?.trim() ?? "";
    clearTimeout(suggestTimer);
    suggestTimer = setTimeout(() => {
      const prefix = raw.slice(0, raw.lastIndexOf(",") + 1);
      duxClient
        .fetchValues(field().table, field().name, term)
        .then((vals) => setSuggestions(prefix ? vals.map((v) => `${prefix} ${v}`) : vals))
        .catch(() => setSuggestions([]));
    }, 150);
  }

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

      {/* operator + value input (filters zone) */}
      <Show when={props.zone === "filters"}>
        <select
          class={styles.aggSelect}
          value={field().op ?? "="}
          onChange={(e) => props.onOpChange?.(e.currentTarget.value as FilterOp)}
        >
          {(isNumeric(field().dataType ?? "") || isDateType(field().dataType ?? "")
            ? COMPARE_OPS
            : TEXT_OPS
          ).map((o) => <option value={o}>{o}</option>)}
        </select>
        <input
          class={styles.filterInput}
          type="text"
          placeholder={(field().op ?? "=") === "=" ? "val1, val2, …" : "value"}
          value={field().value ?? ""}
          list={listId}
          onInput={(e) => {
            props.onValueChange?.(e.currentTarget.value);
            loadSuggestions(e.currentTarget.value);
          }}
          onFocus={() => loadSuggestions(field().value ?? "")}
        />
        <datalist id={listId}>
          {suggestions().map((v) => <option value={v} />)}
        </datalist>
      </Show>

      {/* remove */}
      <button class={styles.remove} onClick={props.onRemove} title="Remove">×</button>
    </div>
  );
};

export default FieldPill;
