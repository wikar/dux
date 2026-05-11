import type { Schema } from "../types/schema";
export { isMetaTable, resolveTable } from "dux-client";
export type { RelTarget } from "dux-client";

export async function fetchSchema(): Promise<Schema> {
  const res = await fetch("/schema");
  if (!res.ok) throw new Error(`schema fetch failed: ${res.status}`);
  return res.json();
}

