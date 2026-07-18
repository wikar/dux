// CSV export: the element's current result set, raw values (no display
// formatting) so the file round-trips into other tools.
import type { QueryResponse } from "@dux/core";

/** RFC 4180-style escaping: quote when the value contains , " or newlines. */
function csvCell(v: unknown): string {
  if (v === null || v === undefined) return "";
  const s = String(v);
  return /[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

export function toCsv(columns: string[], rows: readonly unknown[][]): string {
  const lines = [columns.map(csvCell).join(",")];
  for (const r of rows) lines.push(r.map(csvCell).join(","));
  return lines.join("\r\n") + "\r\n";
}

/** Trigger a client-side download of a query result as CSV. */
export function downloadCsv(res: QueryResponse, name: string) {
  const safe = (name.trim() || "data").replace(/[\\/:*?"<>|]+/g, "_");
  const blob = new Blob([toCsv(res.columns, res.rows)], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `${safe}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}
