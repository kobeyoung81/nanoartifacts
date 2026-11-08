package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// staticHandler serves the library SPA.
//
// Routing lives in the fragment, so every unknown path is still the app: there
// is no server-side route table to keep in step. The API and MCP patterns are
// more specific than "/" and win in the mux, which is why this handler can be
// the catch-all.
func (s *Server) staticHandler() http.Handler {
	files := http.FileServer(http.Dir(s.WebDir))
	index := filepath.Join(s.WebDir, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// index.html must never be cached: it names the hashed asset files, and
		// a stale copy keeps loading the previous build's JS.
		w.Header().Set("Cache-Control", "no-cache")

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if info, err := os.Stat(filepath.Join(s.WebDir, filepath.Clean(path))); err == nil && !info.IsDir() {
				// Vite fingerprints asset names, so they are safe to cache hard.
				// The plugin bundle keeps the same layout one level down
				// (`plugins/nanoartifacts/assets/...`), and its chunks are hashed
				// the same way; `remoteEntry.js` itself is not, so it falls through
				// to no-cache above.
				if strings.HasPrefix(path, "assets/") || strings.Contains(path, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}

		http.ServeFile(w, r, index)
	})
}
