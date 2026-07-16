// Package web embeds the pre-built frontend apps served by duxd.
package web

import "embed"

// Builder embeds the compiled query builder SPA (web/builder/dist),
// built with `bun run build` from the web/ workspace.
//
//go:embed builder/dist
var Builder embed.FS
