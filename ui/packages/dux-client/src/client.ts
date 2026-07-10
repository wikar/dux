import type { Schema, QueryResponse } from "./types";

export interface RelInput {
  from_table: string;
  from_column: string;
  to_table: string;
  to_column: string;
  bidirectional?: boolean;
}

/**
 * Typed client for the DUX backend API.
 *
 * @param baseUrl  Origin of the DUX backend, e.g. "http://localhost:80".
 *                 Leave empty to use the same origin as the consuming app (default).
 */
export class DuxClient {
  constructor(public readonly baseUrl: string = "") {}

  private url(path: string): string {
    return `${this.baseUrl}${path}`;
  }

  async fetchSchema(): Promise<Schema> {
    const res = await fetch(this.url("/schema"));
    if (!res.ok) throw new Error(`schema fetch failed: ${res.status}`);
    return res.json() as Promise<Schema>;
  }

  async executeQuery(query: string): Promise<QueryResponse> {
    const res = await fetch(this.url("/query"), {
      method: "POST",
      headers: { "Content-Type": "text/plain" },
      body: query,
    });
    const text = await res.text();
    if (!res.ok) throw new Error(text || `query failed: ${res.status}`);
    return JSON.parse(text) as QueryResponse;
  }

  async importToml(text: string): Promise<void> {
    const res = await fetch(this.url("/import"), {
      method: "POST",
      headers: { "Content-Type": "text/plain" },
      body: text,
    });
    if (!res.ok) throw new Error(`import failed: ${res.status}`);
  }

  exportTomlUrl(): string {
    return this.url("/export");
  }

  async refresh(): Promise<void> {
    const res = await fetch(this.url("/refresh"), { method: "POST" });
    if (!res.ok) throw new Error(`refresh failed: ${res.status}`);
  }

  async addMeasure(
    table: string,
    name: string,
    expression: string
  ): Promise<void> {
    const res = await fetch(this.url("/measures"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ table, name, expression }),
    });
    if (!res.ok) throw new Error(await res.text());
  }

  async deleteMeasure(table: string, name: string): Promise<void> {
    const res = await fetch(
      this.url(`/measures/${encodeURIComponent(table)}/${encodeURIComponent(name)}`),
      { method: "DELETE" }
    );
    if (!res.ok) throw new Error(await res.text());
  }

  /** Designate table/column as the model's date table (replaces any previous). */
  async setDateTable(table: string, column: string): Promise<void> {
    const res = await fetch(this.url("/datetable"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ table, column }),
    });
    if (!res.ok) throw new Error(await res.text());
  }

  /** Clear the date-table designation. */
  async clearDateTable(): Promise<void> {
    const res = await fetch(this.url("/datetable"), { method: "DELETE" });
    if (!res.ok) throw new Error(await res.text());
  }

  /** Mark a table/view (no column) or a single column as hidden. */
  async setHidden(table: string, column?: string): Promise<void> {
    const res = await fetch(this.url("/hidden"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(column ? { table, column } : { table }),
    });
    if (!res.ok) throw new Error(await res.text());
  }

  /** Clear a hidden designation for a table/view (no column) or column. */
  async clearHidden(table: string, column?: string): Promise<void> {
    const res = await fetch(this.url("/hidden"), {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(column ? { table, column } : { table }),
    });
    if (!res.ok) throw new Error(await res.text());
  }

  async addRelationship(rel: RelInput): Promise<void> {
    const res = await fetch(this.url("/relationships"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(rel),
    });
    if (!res.ok) throw new Error(await res.text());
  }

  async deleteRelationship(rel: RelInput): Promise<void> {
    const res = await fetch(this.url("/relationships"), {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(rel),
    });
    if (!res.ok) throw new Error(await res.text());
  }
}
