// Package ui embeds the pre-built query builder UI.
package ui

import "embed"

//go:embed dist
var Dist embed.FS
