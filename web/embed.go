// Package web embeds the built frontend (Vite output in dist/) into the
// binary so one container serves both the API and the PWA from one origin.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Dist returns the frontend build rooted at dist/.
func Dist() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}
