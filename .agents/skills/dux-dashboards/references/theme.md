# Theme reference

All visual styling flows from **theme tokens** with a per-token cascade:

```
built-in defaults  ←  dashboards/theme.json (global)  ←  dashboard.theme (per dashboard)
```

Empty/absent token = inherit from the level above. The global file is plain
JSON on disk (git-friendly, agent-editable) and also round-trips through
the API:

```
GET /api/dash/theme            → {"tokens": {...}}  (ETag header)
PUT /api/dash/theme            ← {"tokens": {...}}  (If-Match: <etag> or *)
```

A dashboard overrides single tokens in its own `"theme"` object.

## Tokens

| Token | Meaning |
|-------|---------|
| `palette` | Data series colors, prioritized left → right (array of CSS colors) |
| `background` | Canvas background color |
| `backgroundImage` | Canvas background image URL (external or `/api/dash/assets/...`) |
| `backgroundFit` | `cover` \| `contain` \| `fill` \| `tile` |
| `elementBackground` | Element container background (translucency recommended) |
| `titleBackground` | Element title-bar background |
| `border` | Element border color |
| `text` | Text color inside the canvas |
| `fontFamily` | Font family for the canvas |

Every color accepts alpha — `#rrggbbaa` or `rgba(r, g, b, a)`. Convention:
write `#rrggbb` when opaque, `rgba(...)` only when alpha < 1.

## Example

Global `dashboards/theme.json`:

```json
{
  "background": "#1e1e2e",
  "backgroundFit": "cover",
  "border": "#45475a",
  "elementBackground": "rgba(24, 24, 37, 0.82)",
  "fontFamily": "-apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif",
  "palette": ["#89b4fa", "#cba6f7", "#a6e3a1", "#fab387", "#f38ba8", "#94e2d5", "#f9e2af", "#b4befe"],
  "text": "#cdd6f4",
  "titleBackground": "transparent"
}
```

Per-dashboard override (only what differs):

```json
"theme": { "background": "#101020", "palette": ["#f38ba8", "#cba6f7", "#a6e3a1"] }
```

## Notes

- Sticky table/pivot headers render frosted glass over `elementBackground`
  — translucent element backgrounds look intentional, not broken.
- Legacy documents may carry `canvas.background` (color/url/fit); it still
  renders and wins over the tokens, but new documents should use theme
  tokens only.
- The file name `theme` is reserved under `dashboards/` — you cannot create
  a dashboard at that path.
