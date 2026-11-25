package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticHandlerServesFlutterClient(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "web")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<html>app</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.dart.js"), []byte("console.log('app')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("must not leak"), 0o644); err != nil {
		t.Fatal(err)
	}

	handler := staticHandler(root)
	serve := func(method, target string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
		return rec
	}

	t.Run("root serves the shell and is not cached", func(t *testing.T) {
		rec := serve(http.MethodGet, "/")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Body.String(); !strings.Contains(got, "<html>app</html>") {
			t.Fatalf("body = %q, want index.html", got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("Cache-Control = %q, want no-cache", got)
		}
	})

	t.Run("unknown routes fall back to the shell", func(t *testing.T) {
		rec := serve(http.MethodGet, "/screens/passes")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<html>app</html>") {
			t.Fatalf("status = %d body = %q, want index.html", rec.Code, rec.Body.String())
		}
	})

	t.Run("assets are served as files", func(t *testing.T) {
		rec := serve(http.MethodGet, "/main.dart.js")
		if rec.Code != http.StatusOK || rec.Body.String() != "console.log('app')" {
			t.Fatalf("status = %d body = %q", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got == "no-cache" {
			t.Fatal("hashed build assets should be cacheable")
		}
	})

	t.Run("unknown API paths stay JSON 404s", func(t *testing.T) {
		rec := serve(http.MethodGet, "/api/not-a-route")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<html>app</html>") {
			t.Fatal("API 404 fell through to index.html")
		}
	})

	t.Run("traversal cannot escape the web root", func(t *testing.T) {
		rec := serve(http.MethodGet, "/../secret.txt")
		if strings.Contains(rec.Body.String(), "must not leak") {
			t.Fatal("served a file outside the web root")
		}
	})
}
