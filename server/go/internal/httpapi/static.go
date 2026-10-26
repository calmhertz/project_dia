package httpapi

import (
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

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

func staticHandler(root string) http.Handler {
	fileServer := http.FileServer(http.Dir(root))
	indexPath := filepath.Join(root, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		requested := filepath.Join(root, filepath.FromSlash(path.Clean("/"+r.URL.Path)))
		info, err := os.Stat(requested)
		if err != nil || info.IsDir() {
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
