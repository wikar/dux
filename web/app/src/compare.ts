// Shared cell comparator for sortable tables/pivots.

type Cell = string | number | null | undefined;

/** Blank = null, undefined, or empty string — always sorts last, in both
 *  directions, matching spreadsheet behavior. */
const isBlank = (v: Cell) => v === null || v === undefined || v === "";

/** Ascending compare: blanks last; when both sides are numbers (or numeric
 *  strings) compare numerically; otherwise a numeric-aware, case-insensitive
 *  locale compare. */
export function compareCells(a: Cell, b: Cell): number {
  if (isBlank(a) || isBlank(b)) return isBlank(a) === isBlank(b) ? 0 : isBlank(a) ? 1 : -1;
  const na = typeof a === "number" ? a : Number(a);
  const nb = typeof b === "number" ? b : Number(b);
  if (!Number.isNaN(na) && !Number.isNaN(nb)) return na - nb;
  return String(a).localeCompare(String(b), undefined, { numeric: true, sensitivity: "base" });
}

/** Direction-aware compare that keeps blanks last regardless of direction, so a
 *  descending sort never floats empty cells to the top. */
export function compareCellsDir(a: Cell, b: Cell, dir: "asc" | "desc"): number {
  if (isBlank(a) || isBlank(b)) return isBlank(a) === isBlank(b) ? 0 : isBlank(a) ? 1 : -1;
  return dir === "desc" ? -compareCells(a, b) : compareCells(a, b);
}
