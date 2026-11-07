// Package web embeds the built SPA (web/dist) into the Go binary.
package web

import "embed"

// Dist holds the Vite build output. Only .gitkeep exists until `npm run build`.
//
//go:embed all:dist
var Dist embed.FS
