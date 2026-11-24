package core

import (
	"net/http"
	"path/filepath"
	"strings"
)

// Serve the SPA entry for dashboard pages only. Assets and unknown API paths
// retain the file server's normal handling.
func dashboardFiles(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	index := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			switch strings.TrimSuffix(r.URL.Path, "/") {
			case "/sites", "/overview", "/pages", "/clicks", "/elements", "/devices", "/sessions", "/events", "/collection", "/security", "/system", "/system-info":
				http.ServeFile(w, r, index)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}
