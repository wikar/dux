import type { DropField, FilterField } from "../types/schema";

const isNumeric = (dt: string) =>
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

  // ── TREATAS filter args (only when a value has been entered) ──
  const treatasArgs = filters
    .filter((f) => f.value.trim() !== "")
    .map((f) => {
      const vals = f.value
        .split(",")
        .map((v) => v.trim())
        .filter(Boolean);
      const numeric = isNumeric(f.dataType);
      const valList = vals.map((v) => (numeric ? v : `"${v}"`)).join(", ");
      return `TREATAS({${valList}}, ${f.table}[${f.name}])`;
    });

  // Order: dimensions → filters → measures (SUMMARIZECOLUMNS spec)
  const allArgs = [...dimArgs, ...treatasArgs, ...measureArgs];

  return `EVALUATE SUMMARIZECOLUMNS(\n    ${allArgs.join(",\n    ")}\n)`;
}
