// Package web embeds the pre-built frontend served by duxd.
package web

import "embed"

// App embeds the compiled DUX UI SPA (web/app/dist) — query builder,
// explorer, and dashboards in one app — built with `bun run build` from
// the web/ workspace.
//
//go:embed app/dist
var App embed.FS
