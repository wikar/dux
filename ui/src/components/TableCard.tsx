import { For, Show } from "solid-js";
import type { Component } from "solid-js";
import type { Table } from "../dux/types";
import { isDateType } from "../dux/schemaHelpers";
import TypeIcon from "./TypeIcon";
import styles from "./TableCard.module.css";
import { EyeOffIcon } from "./icons";

/** Outline eye icon for the row-preview button. */
const EyeIcon: Component = () => (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7z" />
    <circle cx="12" cy="12" r="3" />
  </svg>
);

/** Outline calendar icon for date-table designation. */
const CalendarIcon: Component<{ size?: number }> = (props) => (
  <svg width={props.size ?? 13} height={props.size ?? 13} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    <rect x="3" y="4" width="18" height="17" rx="2" />
    <line x1="16" y1="2" x2="16" y2="6" />
    <line x1="8" y1="2" x2="8" y2="6" />
    <line x1="3" y1="9.5" x2="21" y2="9.5" />
  </svg>
);

const TableCard: Component<{
  tableName: string;
  table: Table;
  x: number;
  y: number;
  /** The designated date column when this table is the model's date table. */
  dateColumn?: string | null;
  /** When true, hidden columns are rendered (muted) instead of filtered out. */
  showHidden?: boolean;
  /** Called when the user mousedowns on the card header to drag it. */
  onHeaderMouseDown: (e: MouseEvent) => void;
  /** Called when the user mousedowns on a column dot to start a relationship drag. */
  onColDotMouseDown: (e: MouseEvent, colName: string) => void;
  /** Called when the column list is scrolled (to update relationship lines). */
  onColumnsScroll?: () => void;
  /** Called when the preview button is clicked. */
  onPreview: () => void;
  /** Toggle this table as the model's date table (only one allowed). */
  onToggleDateTable?: () => void;
  /** Switch the designated date column within this (already designated) table. */
  onSetDateColumn?: (colName: string) => void;
  /** Toggle the hidden flag on the whole table/view. */
  onToggleHidden?: () => void;
  /** Toggle the hidden flag on a single column. */
  onToggleColumnHidden?: (colName: string) => void;
}> = (props) => {
  const columns = () =>
    Object.values(props.table.Columns)
      .filter((c) => props.showHidden || !c.Hidden)
      .sort((a, b) => a.Name.localeCompare(b.Name));

  const dateColumns = () => columns().filter((c) => isDateType(c.DataType));
  const isDateTable = () => props.dateColumn != null;
  const isHidden = () => props.table.Hidden === true;

  return (
    <div
      class={styles.card}
      classList={{ [styles.cardHiddenState]: isHidden() }}
      data-card={props.tableName}
      style={`left:${props.x}px;top:${props.y}px`}
    >
      <div class={styles.cardHeader} onMouseDown={props.onHeaderMouseDown}>
        <span class={styles.cardTableName} title={props.tableName}>
          {props.tableName}
        </span>
        <Show when={props.table.IsView}>
          <span class={styles.viewBadge} title="DuckDB view">view</span>
        </Show>
        <Show when={dateColumns().length > 0}>
          <button
            class={styles.iconBtn}
            classList={{ [styles.dateBtnActive]: isDateTable() }}
            title={
              isDateTable()
                ? `Date table (column: ${props.dateColumn}) — click to unmark`
                : "Mark as date table"
            }
            onMouseDown={(e) => e.stopPropagation()}
            onClick={(e) => {
              e.stopPropagation();
              props.onToggleDateTable?.();
            }}
          >
            <CalendarIcon />
          </button>
        </Show>
        <button
          class={styles.iconBtn}
          classList={{ [styles.hiddenBtnActive]: isHidden() }}
          title={isHidden() ? "Hidden — click to unhide" : "Hide this table"}
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            props.onToggleHidden?.();
          }}
        >
          <EyeOffIcon />
        </button>
        <button
          class={styles.iconBtn}
          title="Preview top 10 rows"
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            props.onPreview();
          }}
        >
          <EyeIcon />
        </button>
      </div>

      <div class={styles.cardColumns} data-card-columns onScroll={() => props.onColumnsScroll?.()}>
        <For each={columns()}>
          {(col) => (
            <div
              class={styles.colRow}
              classList={{ [styles.colRowHidden]: col.Hidden === true }}
              data-table={props.tableName}
              data-col={col.Name}
            >
              <TypeIcon dataType={col.DataType} />
              <span class={styles.colName} title={col.Name}>
                {col.Name}
              </span>
              {/* When the designated date table has several date columns, each
                  shows a calendar toggle to pick which one is THE date column. */}
              <Show when={isDateTable() && dateColumns().length > 1 && isDateType(col.DataType)}>
                <button
                  class={styles.colCalBtn}
                  classList={{ [styles.colCalBtnActive]: props.dateColumn === col.Name }}
                  title={
                    props.dateColumn === col.Name
                      ? "Current date column"
                      : "Use as the date column"
                  }
                  onMouseDown={(e) => e.stopPropagation()}
                  onClick={(e) => {
                    e.stopPropagation();
                    if (props.dateColumn !== col.Name) props.onSetDateColumn?.(col.Name);
                  }}
                >
                  <CalendarIcon size={11} />
                </button>
              </Show>
              <button
                class={styles.colHideBtn}
                classList={{ [styles.colHideBtnActive]: col.Hidden === true }}
                title={col.Hidden ? "Hidden — click to unhide" : "Hide this column"}
                onMouseDown={(e) => e.stopPropagation()}
                onClick={(e) => {
                  e.stopPropagation();
                  props.onToggleColumnHidden?.(col.Name);
                }}
              >
                <EyeOffIcon size={11} />
              </button>
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
