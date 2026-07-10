import type { Schema, QueryResponse } from "./types";

export interface RelInput {
  from_table: string;
  from_column: string;
  to_table: string;
  to_column: string;
  bidirectional?: boolean;
}

/** Query failure with pipeline stage and 1-based source position (0 = unknown). */
export class QueryFailedError extends Error {
  constructor(
    message: string,
    public stage = "",
    public line = 0,
    public column = 0
  ) {
    super(message);
  }
}

/** Typed client for the DUX backend API (same-origin). */
export class DuxClient {
  async fetchSchema(): Promise<Schema> {
    const res = await fetch("/schema");
    if (!res.ok) throw new Error(`schema fetch failed: ${res.status}`);
    return res.json() as Promise<Schema>;
  }

  /** Distinct values of a column (max 50), optionally narrowed by a search term. */
  async fetchValues(table: string, column: string, q = ""): Promise<string[]> {
    const params = new URLSearchParams({ table, column });
    if (q) params.set("q", q);
    const res = await fetch(`/values?${params}`);
    if (!res.ok) throw new Error(await res.text());
    return res.json() as Promise<string[]>;
  }

  async executeQuery(query: string): Promise<QueryResponse> {
    const res = await fetch("/query", {
      method: "POST",
      headers: { "Content-Type": "text/plain" },
      body: query,
    });
    const text = await res.text();
    if (!res.ok) {
      // Pipeline errors arrive as {error, stage, line, column} JSON.
      try {
        const j = JSON.parse(text);
        if (j && typeof j.error === "string") {
          throw new QueryFailedError(j.error, j.stage ?? "", j.line ?? 0, j.column ?? 0);
        }
      } catch (e) {
        if (e instanceof QueryFailedError) throw e;
      }
      throw new Error(text || `query failed: ${res.status}`);
    }
    return JSON.parse(text) as QueryResponse;
  }

  async importToml(text: string): Promise<void> {
    const res = await fetch("/import", {
      method: "POST",
      headers: { "Content-Type": "text/plain" },
      body: text,
    });
    if (!res.ok) throw new Error(`import failed: ${res.status}`);
  }

  exportTomlUrl(): string {
    return "/export";
  }

  async refresh(): Promise<void> {
    const res = await fetch("/refresh", { method: "POST" });
    if (!res.ok) throw new Error(`refresh failed: ${res.status}`);
  }

  async addMeasure(
    table: string,
    name: string,
    expression: string
  ): Promise<void> {
    const res = await fetch("/measures", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ table, name, expression }),
    });
    if (!res.ok) throw new Error(await res.text());
  }

  async deleteMeasure(table: string, name: string): Promise<void> {
    const res = await fetch(
      `/measures/${encodeURIComponent(table)}/${encodeURIComponent(name)}`,
      { method: "DELETE" }
    );
    if (!res.ok) throw new Error(await res.text());
  }

  /** Designate table/column as the model's date table (replaces any previous). */
  async setDateTable(table: string, column: string): Promise<void> {
    const res = await fetch("/datetable", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ table, column }),
    });
    if (!res.ok) throw new Error(await res.text());
  }

  /** Clear the date-table designation. */
  async clearDateTable(): Promise<void> {
    const res = await fetch("/datetable", { method: "DELETE" });
    if (!res.ok) throw new Error(await res.text());
  }

  /** Mark a table/view (no column) or a single column as hidden. */
  async setHidden(table: string, column?: string): Promise<void> {
    const res = await fetch("/hidden", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(column ? { table, column } : { table }),
    });
    if (!res.ok) throw new Error(await res.text());
  }

  /** Clear a hidden designation for a table/view (no column) or column. */
  async clearHidden(table: string, column?: string): Promise<void> {
    const res = await fetch("/hidden", {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(column ? { table, column } : { table }),
    });
    if (!res.ok) throw new Error(await res.text());
  }

  async addRelationship(rel: RelInput): Promise<void> {
    const res = await fetch("/relationships", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(rel),
    });
    if (!res.ok) throw new Error(await res.text());
  }

  async deleteRelationship(rel: RelInput): Promise<void> {
    const res = await fetch("/relationships", {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(rel),
    });
    if (!res.ok) throw new Error(await res.text());
  }
}

/** Shared client instance used across the UI. */
export const duxClient = new DuxClient();
