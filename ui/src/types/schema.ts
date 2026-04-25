export interface Column {
  Name: string;
  DataType: string;
}

export interface Table {
  Name: string;
  Database?: string;
  Columns: Record<string, Column>;
}

export interface MeasureDef {
  Table: string;
  Column: string;
}

export interface Relationship {
  FromTable: string;
  FromColumn: string;
  ToTable: string;
  ToColumn: string;
}

export interface Schema {
  Tables: Record<string, Table>;
  Measures: Record<string, Record<string, MeasureDef>> | null;
  Relationships: Relationship[] | null;
}

// ─── Query builder state types ──────────────────────────────────────────────

export type Aggregate = "SUM" | "COUNT" | "AVERAGE" | "MIN" | "MAX" | "DISTINCTCOUNT";

export interface DropField {
  table: string;
  name: string;
  /** "column" = plain table column; "measure" = defined measure */
  kind: "column" | "measure";
  dataType: string;
  /** Only set for numeric columns; undefined for grouping dims and measures */
  aggregate?: Aggregate;
}

export interface FilterField {
  table: string;
  name: string;
  dataType: string;
  value: string;
}

// Drag data payload attached to each draggable schema-tree item.
export interface DragPayload {
  table: string;
  name: string;
  kind: "column" | "measure";
  dataType: string;
}
