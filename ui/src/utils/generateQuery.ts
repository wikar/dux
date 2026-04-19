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

  // Group-by columns (non-numeric)
  const groupCols = fields.filter(
    (f) => f.kind === "column" && !isNumeric(f.dataType)
  );

  // Aggregated numeric columns
  const aggCols = fields.filter(
    (f) => f.kind === "column" && isNumeric(f.dataType)
  );

  // Measures (ColRef style)
  const measureCols = fields.filter((f) => f.kind === "measure");

  // ── Group-by columns then measure ColRefs ──
  const groupByArgs = [
    ...groupCols.map((f) => `${f.table}[${f.name}]`),
    ...measureCols.map((f) => `${f.table}[${f.name}]`),
  ];

  // ── Named aggregates ──
  const aggArgs = aggCols.flatMap((f) => {
    const agg = f.aggregate ?? "SUM";
    return [`"${f.name}"`, `${agg}(${f.table}[${f.name}])`];
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

  const allArgs = [...groupByArgs, ...aggArgs, ...treatasArgs];

  return `EVALUATE SUMMARIZECOLUMNS(\n    ${allArgs.join(",\n    ")}\n)`;
}
