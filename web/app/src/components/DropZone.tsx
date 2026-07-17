import { useState } from "react";
import type { DragEvent } from "react";
import type { DropField, FilterField, DragPayload, FilterOp } from "@dux/core";
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

export default function DropZone(props: DropZoneProps) {
  const [over, setOver] = useState(false);

  function handleDragOver(e: DragEvent) {
    if (e.dataTransfer.types.includes("application/dux")) {
      e.preventDefault();
      e.dataTransfer.dropEffect = "copy";
      setOver(true);
    }
  }

  function handleDrop(e: DragEvent) {
    e.preventDefault();
    setOver(false);
    const raw = e.dataTransfer.getData("application/dux");
    if (!raw) return;
    try {
      const payload = JSON.parse(raw) as DragPayload;
      props.onDrop(payload);
    } catch {}
  }

  return (
    <div
      className={`${styles.zone}${over ? ` ${styles.over}` : ""}`}
      onDragOver={handleDragOver}
      onDragLeave={() => setOver(false)}
      onDrop={handleDrop}
    >
      <div className={styles.label}>{props.label}</div>
      <div className={styles.pillList}>
        {props.items.length > 0 ? (
          props.items.map((item, idx) => (
            <FieldPill
              key={`${item.table}\0${item.name}`}
              field={item}
              zone={props.id}
              index={idx}
              onRemove={() => props.onRemove(idx)}
              onReorder={(draggedIdx) => props.onReorder(idx, draggedIdx)}
              onAggChange={(agg) => props.onAggChange?.(idx, agg)}
              onValueChange={(val) => props.onValueChange?.(idx, val)}
              onOpChange={(op) => props.onOpChange?.(idx, op)}
            />
          ))
        ) : (
          <div className={styles.empty}>Drag fields here</div>
        )}
      </div>
    </div>
  );
}
