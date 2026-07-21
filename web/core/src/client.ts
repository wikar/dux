import type { Schema, QueryResponse, MeasureFormat, MeasureListItem } from "./types";

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

/** External filter injected into a query's outermost filter context (mirrors
 *  executor.ExternalFilter; ops: in, between, =, !=, <, <=, >, >=, contains,
 *  in_tuples). For in_tuples, table/column/values are omitted and
 *  columns+tuples carry a multi-column OR-of-tuples set (columns must share
 *  one table). */
export interface ExternalFilter {
  table?: string;
  column?: string;
  op: string;
  values?: (string | number)[];
  value?: string | number;
  from?: string | number;
  to?: string | number;
  columns?: { table: string; column: string }[];
  tuples?: (string | number)[][];
}

async function errorText(res: Response): Promise<string> {
  const text = await res.text();
  try {
    const body = JSON.parse(text);
    if (body && typeof body.error === "string") return body.error;
  } catch {
    // Non-JSON responses still occur before the request reaches duxd.
  }
  return text || `request failed: ${res.status}`;
}

async function parseQueryResponse(res: Response): Promise<QueryResponse> {
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

/** GET /version response: server version plus API capability flags. */
export interface VersionInfo {
  version: string;
  capabilities: Record<string, boolean>;
}

/** Typed client for the DUX backend API (same-origin). */
export class DuxClient {
  /** Server version and capabilities (feature-gates UI sections, e.g. dashboards). */
  async fetchVersion(): Promise<VersionInfo> {
    const res = await fetch("/version");
    if (!res.ok) throw new Error(await errorText(res));
    return res.json() as Promise<VersionInfo>;
  }

  async fetchSchema(): Promise<Schema> {
    const res = await fetch("/schema");
    if (!res.ok) throw new Error(await errorText(res));
    return res.json() as Promise<Schema>;
  }

  /** Distinct values of a column (max 50), optionally narrowed by a search term. */
  async fetchValues(table: string, column: string, q = ""): Promise<string[]> {
    const params = new URLSearchParams({ table, column });
    if (q) params.set("q", q);
    const res = await fetch(`/values?${params}`);
    if (!res.ok) throw new Error(await errorText(res));
    return res.json() as Promise<string[]>;
  }

  async executeQuery(query: string, opts?: { signal?: AbortSignal }): Promise<QueryResponse> {
    const res = await fetch("/query", {
      method: "POST",
      headers: { "Content-Type": "text/plain" },
      body: query,
      signal: opts?.signal,
    });
    return parseQueryResponse(res);
  }

  /** Execute a query with external filters (dashboard slicers) applied to its
   *  outermost filter context via the JSON envelope of POST /query. */
  async executeQueryFiltered(
    query: string,
    filters: ExternalFilter[],
    opts?: { signal?: AbortSignal }
  ): Promise<QueryResponse> {
    if (filters.length === 0) return this.executeQuery(query, opts);
    const res = await fetch("/query", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ query, filters }),
      signal: opts?.signal,
    });
    return parseQueryResponse(res);
  }

  async importToml(text: string): Promise<void> {
    const res = await fetch("/import", {
      method: "POST",
      headers: { "Content-Type": "text/plain" },
      body: text,
    });
    if (!res.ok) throw new Error(await errorText(res));
  }

  exportTomlUrl(): string {
    return "/export";
  }

  async refresh(): Promise<void> {
    const res = await fetch("/refresh", { method: "POST" });
    if (!res.ok) throw new Error(await errorText(res));
  }

  /** List all measures with their display formats. */
  async fetchMeasures(): Promise<MeasureListItem[]> {
    const res = await fetch("/measures");
    if (!res.ok) throw new Error(await errorText(res));
    return res.json() as Promise<MeasureListItem[]>;
  }

  /** Add or replace a measure. Omitting format clears any stored format —
   *  callers editing an existing measure must pass its current format through. */
  async addMeasure(
    table: string,
    name: string,
    expression: string,
    format?: MeasureFormat
  ): Promise<void> {
    const res = await fetch("/measures", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(format ? { table, name, expression, format } : { table, name, expression }),
    });
    if (!res.ok) throw new Error(await errorText(res));
  }

  async deleteMeasure(table: string, name: string): Promise<void> {
    const res = await fetch(
      `/measures/${encodeURIComponent(table)}/${encodeURIComponent(name)}`,
      { method: "DELETE" }
    );
    if (!res.ok) throw new Error(await errorText(res));
  }

  /** Designate table/column as the model's date table (replaces any previous). */
  async setDateTable(table: string, column: string): Promise<void> {
    const res = await fetch("/datetable", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ table, column }),
    });
    if (!res.ok) throw new Error(await errorText(res));
  }

  /** Clear the date-table designation. */
  async clearDateTable(): Promise<void> {
    const res = await fetch("/datetable", { method: "DELETE" });
    if (!res.ok) throw new Error(await errorText(res));
  }

  /** Mark a table/view (no column) or a single column as hidden. */
  async setHidden(table: string, column?: string): Promise<void> {
    const res = await fetch("/hidden", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(column ? { table, column } : { table }),
    });
    if (!res.ok) throw new Error(await errorText(res));
  }

  /** Clear a hidden designation for a table/view (no column) or column. */
  async clearHidden(table: string, column?: string): Promise<void> {
    const res = await fetch("/hidden", {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(column ? { table, column } : { table }),
    });
    if (!res.ok) throw new Error(await errorText(res));
  }

  async addRelationship(rel: RelInput): Promise<void> {
    const res = await fetch("/relationships", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(rel),
    });
    if (!res.ok) throw new Error(await errorText(res));
  }

  async deleteRelationship(rel: RelInput): Promise<void> {
    const res = await fetch("/relationships", {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(rel),
    });
    if (!res.ok) throw new Error(await errorText(res));
  }
}

/** Shared client instance used across the UI. */
export const duxClient = new DuxClient();
