// Client for the /api/dash dashboards API (same-origin).
import type { Dashboard } from "./types";

/** One dashboard in the GET /api/dash/dashboards listing. */
export interface DashEntry {
  path: string;
  name: string;
  modified: string;
  etag: string;
  valid: boolean;
  error?: string;
}

/** GET envelope for a single dashboard. Invalid files carry raw instead. */
export interface DashDocument extends DashEntry {
  document?: Dashboard;
  raw?: string;
}

/** Save rejected because the file changed since it was loaded (HTTP 409/428). */
export class DashConflictError extends Error {
  constructor(
    message: string,
    public currentEtag: string,
    public modified: string
  ) {
    super(message);
  }
}

async function errText(res: Response): Promise<string> {
  const text = await res.text();
  try {
    const j = JSON.parse(text);
    if (j && typeof j.error === "string") return j.error;
  } catch {
    /* not JSON */
  }
  return text || `request failed: ${res.status}`;
}

/** Percent-encode a slash-separated dashboard/asset path for a URL. */
export function encodePath(p: string): string {
  return p.split("/").map(encodeURIComponent).join("/");
}

export async function listDashboards(): Promise<DashEntry[]> {
  const res = await fetch("/api/dash/dashboards");
  if (!res.ok) throw new Error(await errText(res));
  return (await res.json()) ?? [];
}

export async function getDashboard(path: string): Promise<DashDocument> {
  const res = await fetch(`/api/dash/dashboards/${encodePath(path)}`);
  if (!res.ok) throw new Error(await errText(res));
  return res.json();
}

/** Create (no etag) or update (etag = If-Match) a dashboard document. */
export async function putDashboard(
  path: string,
  doc: Dashboard,
  etag?: string | null
): Promise<{ etag: string; created: boolean }> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (etag) headers["If-Match"] = etag;
  const res = await fetch(`/api/dash/dashboards/${encodePath(path)}`, {
    method: "PUT",
    headers,
    body: JSON.stringify(doc),
  });
  if (res.status === 409) {
    const j = await res.json();
    throw new DashConflictError(j.error, j.currentEtag ?? "", j.modified ?? "");
  }
  if (res.status === 428) {
    // Tried to create over an existing file: fetch its etag so the conflict
    // dialog can offer overwrite/reload like any other conflict.
    const cur = await getDashboard(path);
    throw new DashConflictError(
      `a dashboard already exists at "${path}"`,
      cur.etag,
      cur.modified
    );
  }
  if (!res.ok) throw new Error(await errText(res));
  return res.json();
}

// Assets are read-only: image files live on disk under the dashboards root
// and are only served (no upload endpoint).
export function assetUrl(path: string): string {
  return `/api/dash/assets/${encodePath(path)}`;
}

/** Resolve an image reference: absolute/data URLs pass through, anything
 *  else is treated as an asset path under /api/dash/assets/. */
export function imageUrl(ref: string): string {
  if (/^(https?:|data:|\/)/i.test(ref)) return ref;
  return assetUrl(ref);
}

// ─── Theme ───────────────────────────────────────────────────────────────────

export interface ThemeDoc {
  tokens: Record<string, unknown>;
  etag: string | null;
}

export async function getTheme(): Promise<ThemeDoc> {
  const res = await fetch("/api/dash/theme");
  if (!res.ok) throw new Error(await errText(res));
  return { tokens: (await res.json()) ?? {}, etag: res.headers.get("ETag") };
}

/** Replace the global theme (etag = If-Match; null when creating). */
export async function putTheme(
  tokens: Record<string, unknown>,
  etag: string | null
): Promise<{ etag: string }> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (etag) headers["If-Match"] = etag;
  const res = await fetch("/api/dash/theme", {
    method: "PUT",
    headers,
    body: JSON.stringify(tokens),
  });
  if (!res.ok) throw new Error(await errText(res));
  return res.json();
}
