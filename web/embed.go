// Package web embeds the compiled web UI (built with Vite into web/dist).
package web

import "embed"

// Dist holds the built UI. In a fresh clone it only contains .keep; run
// `mise run build` to compile the UI into the binary.
//
//go:embed all:dist
var Dist embed.FS
