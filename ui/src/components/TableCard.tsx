import { For } from "solid-js";
import type { Component } from "solid-js";
import type { Table } from "../types/schema";
import TypeIcon from "./TypeIcon";
import styles from "./TableCard.module.css";

const TableCard: Component<{
  tableName: string;
  table: Table;
  x: number;
  y: number;
  /** Called when the user mousedowns on the card header to drag it. */
  onHeaderMouseDown: (e: MouseEvent) => void;
  /** Called when the user mousedowns on a column dot to start a relationship drag. */
  onColDotMouseDown: (e: MouseEvent, colName: string) => void;
  /** Called when the column list is scrolled (to update relationship lines). */
  onColumnsScroll?: () => void;
  /** Called when the preview button is clicked. */
  onPreview: () => void;
}> = (props) => {
  const columns = () =>
    Object.values(props.table.Columns).sort((a, b) => a.Name.localeCompare(b.Name));

  return (
    <div
      class={styles.card}
      data-card={props.tableName}
      style={`left:${props.x}px;top:${props.y}px`}
    >
      <div class={styles.cardHeader} onMouseDown={props.onHeaderMouseDown}>
        <span class={styles.cardTableName} title={props.tableName}>
          {props.tableName}
        </span>
        <button
          class={styles.previewBtn}
          title="Preview top 10 rows"
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            props.onPreview();
          }}
        >
          ⊡
        </button>
      </div>

      <div class={styles.cardColumns} data-card-columns onScroll={() => props.onColumnsScroll?.()}>
        <For each={columns()}>
          {(col) => (
            <div
              class={styles.colRow}
              data-table={props.tableName}
              data-col={col.Name}
            >
              <TypeIcon dataType={col.DataType} />
              <span class={styles.colName} title={col.Name}>
                {col.Name}
              </span>
              <span class={styles.colType}>
                {col.DataType.split("(")[0].toUpperCase()}
              </span>
              <div
                class={styles.colDot}
                data-dot-table={props.tableName}
                data-dot-col={col.Name}
                title="Drag to create relationship"
                onMouseDown={(e) => {
                  e.stopPropagation();
                  props.onColDotMouseDown(e, col.Name);
                }}
              />
            </div>
          )}
        </For>
      </div>
    </div>
  );
};

export default TableCard;
