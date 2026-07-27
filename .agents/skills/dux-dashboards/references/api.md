# /api/dash API reference

Dashboards are files under the server's `dashboards/` directory; the path
(without `.json`) is the identity. Disable the whole module with
`DUX_DASH=0` (the endpoints then return 404).

The running server documents its full HTTP API at `GET /openapi.json`
(OpenAPI 3.1, includes these endpoints; browsable Scalar UI at `/docs`) —
consult it when this reference and the server disagree; the server is
newer.

## Documents

```
GET    /api/dash/dashboards                 → [{"path": "sales/overview", "valid": true}, ...]
GET    /api/dash/dashboards/{path}          → document JSON + ETag header
GET    /api/dash/dashboards/{path}?raw=1    → the file verbatim (download; ETag header)
PUT    /api/dash/dashboards/{path}          ← document JSON body
DELETE /api/dash/dashboards/{path}
```

### Optimistic concurrency (If-Match)

| Operation | `If-Match` header | Behavior |
|-----------|-------------------|----------|
| Create | *(none)* | 201 if the path is free; 428 Precondition Required if it exists |
| Update | `<etag>` from the last GET/PUT | 200; **409 Conflict** if someone saved in between |
| Create-or-overwrite | `*` | Always writes (agent/tooling path) |
| Delete | `<etag>` or `*` | 204; same conflict rules |

PUT responses: `{"etag": "...", "created": true|false}` (+ ETag header).
A 409 body includes `{"currentEtag": "...", "modified": "..."}` — re-GET,
merge, retry with the fresh etag (or force with `*` when overwriting is
intended).

### Validation

Every PUT is validated against the document JSON schema
(`GET /api/dash/schema.json` serves it). Invalid documents → **422** with
the violation message; nothing is written. Also enforced: unique element
ids, path shape (lowercase letters, digits, space, `-`, `_`, folders with
`/`), the reserved name `theme`, and the refresh-interval floor.

## Theme

```
GET /api/dash/theme        → {"tokens": {...}}  (ETag when the file exists)
PUT /api/dash/theme        ← {"tokens": {...}}  (If-Match: <etag> or *)
```

Stored as `dashboards/theme.json` — the global template every dashboard
inherits per token.

## Assets (read-only)

```
GET /api/dash/assets/{path}    → the image (proper Content-Type; SVG served with a no-script CSP)
```

Images referenced in documents (`image.url`, `backgroundImage`) are external
URLs, or image files (.png/.jpg/.jpeg/.webp/.svg) placed **on disk under the
dashboards root** — deploy them alongside the documents. There is
deliberately no upload endpoint: assets are versioned and lifecycle-managed
as files, like the dashboards themselves.

The `{path}` is relative to the dashboards root, so a file in a subfolder
carries that segment too — a common source of unexpected 404s:

```
dashboards/logo.png          →  GET /api/dash/assets/logo.png
dashboards/assets/logo.png   →  GET /api/dash/assets/assets/logo.png
```

Any other extension is a 404 even when the file exists. Paths are matched
case-insensitively. Assets are read from disk per request, so a file copied
into a running server is served immediately — no restart, no rebuild, and
nothing to invalidate.

In a document, write the path **relative to the dashboards root** and let the
client resolve it (`"url": "assets/logo.png"`). Values starting with `http:`,
`https:`, `data:`, or `/` are used verbatim instead.

## Typical agent operations

Publish (create or overwrite):

```sh
curl -sS -X PUT "$DUXD_URL/api/dash/dashboards/sales/overview" \
  -H 'If-Match: *' -H 'Content-Type: application/json' \
  --data-binary @overview.json
```

Backup / restore verbatim:

```sh
curl -sS "$DUXD_URL/api/dash/dashboards/sales/overview?raw=1" -o overview.json
```

Safe edit loop: GET (note the ETag) → modify → PUT with `If-Match: <etag>`
→ on 409, re-GET and reconcile.
