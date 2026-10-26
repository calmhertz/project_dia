package httpapi

import (
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// registerStatic wires the web client into the mux when a build is present.
// A missing directory is a valid API-only deployment, so it is a warning
// rather than a startup failure.
func registerStatic(mux *http.ServeMux, logger *slog.Logger, root string) {
	if root == "" {
		return
	}
	if _, err := os.Stat(filepath.Join(root, "index.html")); err != nil {
		logger.Warn("web client not found; serving the API only",
			slog.String("dir", root), slog.String("error", err.Error()))
		return
	}
	mux.Handle("GET /", staticHandler(root))
	logger.Info("serving web client", slog.String("dir", root))
}

// staticHandler serves the built Flutter web client from root.
//
// The Client is same-origin with the API in a normal deployment: opening the
// Server's HTTP address shows the UI, and the UI calls /api on that same
// origin. Paths under /api that did not match a route stay API 404s instead of
// falling through to index.html, so a typo in a request is never answered with
// a page.
//
// Flutter's router is hash-based by default, but serving index.html for any
// unknown path keeps deep links working if it is switched to path URLs.
func staticHandler(root string) http.Handler {
	fileServer := http.FileServer(http.Dir(root))
	indexPath := filepath.Join(root, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// path.Clean on a rooted path removes "..", so the joined path can
		// never escape root.
		requested := filepath.Join(root, filepath.FromSlash(path.Clean("/"+r.URL.Path)))
		info, err := os.Stat(requested)
		if err != nil || info.IsDir() {
			// The shell and the service worker are updated on every deploy;
			// caching either would leave browsers on an old build.
			if isUncachedPath(r.URL.Path) {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeFile(w, r, indexPath)
			return
		}

		if isUncachedPath(r.URL.Path) {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}

func isUncachedPath(urlPath string) bool {
	if urlPath == "/" || strings.HasSuffix(urlPath, "/index.html") {
		return true
	}
	switch path.Clean(urlPath) {
	case "/flutter_service_worker.js", "/flutter_bootstrap.js", "/version.json":
		return true
	}
	return false
}
