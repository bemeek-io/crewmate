package server

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// SPAHandler serves the embedded frontend build with an index.html fallback
// for client-side routes. Hashed assets get long-lived caching; index.html and
// the service worker must always revalidate.
func SPAHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(dist, p); err != nil {
			// Client-side route — serve the app shell.
			r.URL.Path = "/"
			w.Header().Set("Cache-Control", "no-cache")
			fileServer.ServeHTTP(w, r)
			return
		}
		switch {
		case strings.HasPrefix(p, "assets/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case p == "sw.js":
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Service-Worker-Allowed", "/")
		default:
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}
