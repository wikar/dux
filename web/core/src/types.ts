export interface Column {
  Name: string;
  DataType: string;
  /** True when the column is marked hidden. */
  Hidden?: boolean;
}

export interface Table {
  Name: string;
  /** True when introspected as a VIEW rather than a BASE TABLE. */
  IsView?: boolean;
  /** True when the table/view is marked hidden. */
  Hidden?: boolean;
  Columns: Record<string, Column>;
}

export interface MeasureDef {
  Table: string;
  Column: string;
  Expression?: string;
}

/** Structured display format for a measure (see semantic.MeasureFormat). */
export interface MeasureFormat {
  kind: "number" | "decimal" | "percent" | "currency" | "compact";
  /** Fraction digits (0–10); undefined = client default for the kind. */
  decimals?: number;
  /** ISO 4217 code; only set when kind is "currency". */
  currency?: string;
}

/** One entry of GET /measures. */
export interface MeasureListItem {
  table: string;
  name: string;
  expression: string;
  format?: MeasureFormat;
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
  /** Optional display formats, keyed like Measures (table → measure name). */
  MeasureFormats?: Record<string, Record<string, MeasureFormat>> | null;
  Relationships: Relationship[] | null;
  /** Lower-cased table key → designated date column ("mark as date table"). */
  DateTables?: Record<string, string> | null;
  /** Lower-cased table key → true when the whole table/view is hidden. */
  HiddenTables?: Record<string, boolean> | null;
  /** Lower-cased table key → set of lower-cased hidden column names. */
  HiddenColumns?: Record<string, Record<string, boolean>> | null;
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

/** Filter comparison operator. "=" compiles to TREATAS (multi-value, comma-
 *  separated); the rest compile to FILTER(table, pred) arguments. */
export type FilterOp = "=" | "<>" | ">" | ">=" | "<" | "<=" | "contains";

export interface FilterField {
  table: string;
  name: string;
  dataType: string;
  op: FilterOp;
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
