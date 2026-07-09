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
  Expression?: string;
}

export interface Relationship {
  FromTable: string;
  FromColumn: string;
  ToTable: string;
  ToColumn: string;
  Bidirectional?: boolean;
}

export interface Schema {
  Tables: Record<string, Table>;
  Measures: Record<string, Record<string, MeasureDef>> | null;
  Relationships: Relationship[] | null;
  /** Lower-cased table key → designated date column ("mark as date table"). */
  DateTables?: Record<string, string> | null;
}

// ─── Query builder state types ───────────────────────────────────────────────

export type Aggregate = "SUM" | "COUNT" | "AVERAGE" | "MIN" | "MAX" | "DISTINCTCOUNT" | "VALUES";

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

export interface DragPayload {
  table: string;
  name: string;
  kind: "column" | "measure";
  dataType: string;
}

export interface QueryResponse {
  columns: string[];
  rows: (string | number | null)[][];
}
