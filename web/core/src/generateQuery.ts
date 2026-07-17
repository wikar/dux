import type { DropField, FilterField } from "./types";

/** True when the DuckDB column type is numeric. */
export const isNumeric = (dt: string) =>
  /^(TINYINT|SMALLINT|INTEGER|BIGINT|HUGEINT|DOUBLE|FLOAT|REAL|DECIMAL|NUMERIC)/i.test(dt);

/** True when the field produces a metric output column (vs a group-by dim). */
export const isMetricField = (f: DropField) =>
  f.kind === "measure" || (isNumeric(f.dataType) && f.aggregate !== "VALUES");

/** One sort key; field is the output column name (dim column or metric alias). */
export interface QuerySort {
  field: string;
  dir?: "asc" | "desc";
}

export interface QueryOptions {
  /** EVALUATE ... ORDER BY keys, applied in order. */
  sort?: QuerySort[];
  /** Wrap in TOPN(n, ..., key): top n rows by the first sort key (or the
   *  last metric when no sort is set). TOPN is always highest-first. */
  topN?: number | null;
}

/**
 * Generate a DUX EVALUATE query from the current builder state.
 * Returns empty string if no fields are selected.
 *
 * Filters become TREATAS({val1, val2, ...}, table[col]) arguments
 * passed directly into SUMMARIZECOLUMNS.
 */
export function generateQuery(
  fields: DropField[],
  filters: FilterField[],
  opts: QueryOptions = {}
): string {
  if (fields.length === 0) return "";

  // Split fields into dimensions (group-by) and measure pairs.
  const dimArgs: string[] = [];
  const measureArgs: string[] = [];

  for (const f of fields) {
    if (f.kind === "measure") {
      measureArgs.push(`"${f.name}"`, `${f.table}[${f.name}]`);
    } else if (isNumeric(f.dataType)) {
      if (f.aggregate === "VALUES") {
        dimArgs.push(`${f.table}[${f.name}]`);
      } else {
        const agg = f.aggregate ?? "SUM";
        measureArgs.push(`"${f.name}"`, `${agg}(${f.table}[${f.name}])`);
      }
    } else {
      dimArgs.push(`${f.table}[${f.name}]`);
    }
  }

  // Filter args (only when a value has been entered): "=" compiles to
  // TREATAS equality (multi-value), other operators to FILTER(table, pred).
  const filterArgs = filters
    .filter((f) => f.value.trim() !== "")
    .map((f) => {
      const numeric = isNumeric(f.dataType);
      const col = `${f.table}[${f.name}]`;
      const op = f.op || "=";
      if (op === "=") {
        const vals = f.value
          .split(",")
          .map((v) => v.trim())
          .filter(Boolean);
        const valList = vals.map((v) => (numeric ? v : `"${v}"`)).join(", ");
        return `TREATAS({${valList}}, ${col})`;
      }
      const v = f.value.trim();
      if (op === "contains") {
        return `FILTER(${f.table}, SEARCH("${v}", ${col}) > 0)`;
      }
      return `FILTER(${f.table}, ${col} ${op} ${numeric ? v : `"${v}"`})`;
    });

  // Order: dimensions → filters → measures (SUMMARIZECOLUMNS spec)
  const allArgs = [...dimArgs, ...filterArgs, ...measureArgs];

  let body = `SUMMARIZECOLUMNS(\n    ${allArgs.join(",\n    ")}\n)`;

  // Resolve an output column name to its ORDER BY / TOPN key expression:
  // metric aliases become [alias], dims become table[col].
  const keyExpr = (name: string): string => {
    const f = fields.find((x) => x.name === name);
    if (f && !isMetricField(f)) return `${f.table}[${f.name}]`;
    return `[${name}]`;
  };

  if (opts.topN && opts.topN > 0) {
    const metricNames = fields.filter(isMetricField).map((f) => f.name);
    // Prefer a metric key: the first sort key if it is a metric, else the
    // last metric ("top N by the measure"), else fall back to the sort dim.
    const sortField = opts.sort?.[0]?.field;
    const sortIsMetric = sortField !== undefined && metricNames.includes(sortField);
    const key = sortIsMetric ? sortField : metricNames[metricNames.length - 1] ?? sortField;
    if (key) body = `TOPN(${opts.topN}, ${body}, ${keyExpr(key)})`;
  }

  let query = `EVALUATE ${body}`;
  const sorts = (opts.sort ?? []).filter((s) => s.field);
  if (sorts.length > 0) {
    query += `\nORDER BY ${sorts
      .map((s) => `${keyExpr(s.field)}${s.dir === "desc" ? " DESC" : ""}`)
      .join(", ")}`;
  }
  return query;
}
