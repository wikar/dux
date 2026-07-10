import type { DropField, FilterField } from "./types";

/** True when the DuckDB column type is numeric. */
export const isNumeric = (dt: string) =>
  /^(TINYINT|SMALLINT|INTEGER|BIGINT|HUGEINT|DOUBLE|FLOAT|REAL|DECIMAL|NUMERIC)/i.test(dt);

/**
 * Generate a DUX EVALUATE query from the current builder state.
 * Returns empty string if no fields are selected.
 *
 * Filters become TREATAS({val1, val2, ...}, table[col]) arguments
 * passed directly into SUMMARIZECOLUMNS.
 */
export function generateQuery(fields: DropField[], filters: FilterField[]): string {
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

  return `EVALUATE SUMMARIZECOLUMNS(\n    ${allArgs.join(",\n    ")}\n)`;
}
