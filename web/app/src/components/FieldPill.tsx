import { useId, useRef, useState } from "react";
import type { DragEvent } from "react";
import type { DropField, FilterField, Aggregate, FilterOp } from "@dux/core";
import { isNumeric, isDateType, duxClient } from "@dux/core";
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
  /** Invoked with the dragged pill's index when another pill is dropped here. */
  onReorder: (draggedIndex: number) => void;
  onAggChange?: (agg: Aggregate) => void;
  onValueChange?: (val: string) => void;
  onOpChange?: (op: FilterOp) => void;
}

export default function FieldPill(props: FieldPillProps) {
  const field = props.field as DropField & FilterField;
  const listId = useId();
  const [suggestions, setSuggestions] = useState<string[]>([]);
  const suggestTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Fetch distinct column values (debounced) to feed the datalist. For a
  // multi-value "=" filter, suggest completions for the term after the last comma.
  function loadSuggestions(raw: string) {
    if (props.zone !== "filters") return;
    const term = raw.split(",").pop()?.trim() ?? "";
    clearTimeout(suggestTimer.current);
    suggestTimer.current = setTimeout(() => {
      const prefix = raw.slice(0, raw.lastIndexOf(",") + 1);
      duxClient
        .fetchValues(field.table, field.name, term)
        .then((vals) => setSuggestions(prefix ? vals.map((v) => `${prefix} ${v}`) : vals))
        .catch(() => setSuggestions([]));
    }, 150);
  }

  // Reorder via drag within the same zone — encode index in dataTransfer
  function handleDragStart(e: DragEvent) {
    e.dataTransfer.effectAllowed = "move";
    e.dataTransfer.setData("application/dux-index", String(props.index));
  }

  function handleDragOver(e: DragEvent) {
    if (e.dataTransfer.types.includes("application/dux-index")) {
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
    }
  }

  function handleDrop(e: DragEvent) {
    // Only intercept pill-reorder drops. A schema-field drop that lands on a
    // pill must bubble to the containing well/zone and add the field there —
    // a populated well is mostly pill surface.
    if (!e.dataTransfer.types.includes("application/dux-index")) return;
    e.preventDefault();
    e.stopPropagation();
    const fromStr = e.dataTransfer.getData("application/dux-index");
    if (fromStr === "") return;
    const from = Number(fromStr);
    if (!isNaN(from) && from !== props.index) {
      props.onReorder(from);
    }
  }

  return (
    <div
      draggable={true}
      className={styles.pill}
      onDragStart={handleDragStart}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      {/* drag handle visual */}
      <span className={styles.handle}>⠿</span>

      {/* icon */}
      {field.kind === "measure" ? (
        <span className={styles.measureIcon}>ƒx</span>
      ) : (
        <TypeIcon dataType={field.dataType ?? ""} />
      )}

      {/* name */}
      <span className={styles.name}>{field.name}</span>

      {/* aggregate selector (fields zone, numeric column) */}
      {props.zone === "fields" && field.kind === "column" && isNumeric(field.dataType ?? "") && (
        <select
          className={styles.aggSelect}
          value={field.aggregate ?? "SUM"}
          onChange={(e) => props.onAggChange?.(e.currentTarget.value as Aggregate)}
        >
          {AGGREGATES.map((a) => <option key={a} value={a}>{a}</option>)}
        </select>
      )}

      {/* operator + value input (filters zone) */}
      {props.zone === "filters" && (
        <>
          <select
            className={styles.aggSelect}
            value={field.op ?? "="}
            onChange={(e) => props.onOpChange?.(e.currentTarget.value as FilterOp)}
          >
            {(isNumeric(field.dataType ?? "") || isDateType(field.dataType ?? "")
              ? COMPARE_OPS
              : TEXT_OPS
            ).map((o) => <option key={o} value={o}>{o}</option>)}
          </select>
          <input
            className={styles.filterInput}
            type="text"
            placeholder={(field.op ?? "=") === "=" ? "val1, val2, …" : "value"}
            value={field.value ?? ""}
            list={listId}
            onChange={(e) => {
              props.onValueChange?.(e.currentTarget.value);
              loadSuggestions(e.currentTarget.value);
            }}
            onFocus={() => loadSuggestions(field.value ?? "")}
          />
          <datalist id={listId}>
            {suggestions.map((v) => <option key={v} value={v} />)}
          </datalist>
        </>
      )}

      {/* remove */}
      <button className={styles.remove} onClick={props.onRemove} title="Remove">×</button>
    </div>
  );
}
