package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// spa serves the built React app, falling back to index.html for client routes.
func (s *Server) spa() http.Handler {
	if s.Web == nil {
		return http.NotFoundHandler()
	}
	files := http.FileServerFS(s.Web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(s.Web, p); err != nil {
			// Unknown path: client-side route → index.html.
			p = "index.html"
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			r = r2
		}
		switch {
		case strings.HasPrefix(p, "assets/"):
			// Vite fingerprints asset names: cache forever.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case p == "sw.js" || p == "index.html" || p == "manifest.webmanifest":
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
