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

  // Emit each field in its original order so reordering is reflected immediately.
  const fieldArgs = fields.flatMap((f) => {
    if (f.kind === "measure") {
      // Pre-defined measure → named pair
      return [`"${f.name}"`, `${f.table}[${f.name}]`];
    }
    if (isNumeric(f.dataType)) {
      // VALUES = treat numeric as a dimension group-by key
      if (f.aggregate === "VALUES") {
        return [`${f.table}[${f.name}]`];
      }
      // Numeric column → named aggregate pair
      const agg = f.aggregate ?? "SUM";
      return [`"${f.name}"`, `${agg}(${f.table}[${f.name}])`];
    }
    // Non-numeric column → group-by ColRef
    return [`${f.table}[${f.name}]`];
  });

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

  const allArgs = [...fieldArgs, ...treatasArgs];

  return `EVALUATE SUMMARIZECOLUMNS(\n    ${allArgs.join(",\n    ")}\n)`;
}
