// Package web embeds the built SPA so the binary is self-contained.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the embedded build output rooted at its contents.
func FS() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
