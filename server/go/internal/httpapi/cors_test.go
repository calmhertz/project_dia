package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aagasa/internal/httpapi"
)


func corsHandler(options httpapi.CORSOptions) http.Handler {
	return httpapi.WithCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("reached"))
	}), options)
}

func corsResponse(options httpapi.CORSOptions, method, origin string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, "/api/satellites", nil)
	request.Header.Set("Origin", origin)
	corsHandler(options).ServeHTTP(recorder, request)
	return recorder
}

func TestCORSIsOffUntilConfigured(t *testing.T) {
	recorder := corsResponse(httpapi.CORSOptions{}, http.MethodGet, "http://localhost:5000")

	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("an unconfigured server allows %q", got)
	}
	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", recorder.Code)
	}
}

func TestPreflightFromAnAllowedOrigin(t *testing.T) {
	options := httpapi.CORSOptions{AllowedOrigins: []string{"https://ground.example"}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
	request.Header.Set("Origin", "https://ground.example")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "content-type")

	corsHandler(options).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("preflight = %d, want 204", recorder.Code)
	}
	header := recorder.Header()
	if header.Get("Access-Control-Allow-Origin") != "https://ground.example" {
		t.Errorf("allow-origin = %q", header.Get("Access-Control-Allow-Origin"))
	}
	allowed := header.Get("Access-Control-Allow-Headers")
	if !strings.Contains(strings.ToLower(allowed), "authorization") {
		t.Errorf("allow-headers = %q, want Authorization", allowed)
	}
	if header.Get("Vary") == "" {
		t.Error("the response does not vary on Origin")
	}
}

func TestPreflightFromAnUnknownOriginIsRefused(t *testing.T) {
	options := httpapi.CORSOptions{AllowedOrigins: []string{"https://ground.example"}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/api/auth/login", nil)
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("Access-Control-Request-Method", "POST")

	corsHandler(options).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Errorf("preflight = %d, want 403", recorder.Code)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("a refused origin was echoed back: %q", got)
	}
}

func TestCORSNeverAllowsCredentialsOrTheWildcard(t *testing.T) {
	recorder := corsResponse(httpapi.CORSOptions{
		AllowedOrigins: []string{"https://ground.example"},
		AllowLocalhost: true,
	}, http.MethodGet, "https://ground.example")

	if recorder.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Error("credentialed cross-origin requests are permitted")
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") == "*" {
		t.Error("the wildcard origin is used")
	}
}

func TestLocalhostAllowanceIsExactlyLocalhost(t *testing.T) {
	options := httpapi.CORSOptions{AllowLocalhost: true}

	for _, origin := range []string{
		"http://localhost:5000", "http://127.0.0.1:8081", "http://localhost:1",
	} {
		recorder := corsResponse(options, http.MethodGet, origin)
		if recorder.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Errorf("%s was not allowed", origin)
		}
	}
	for _, origin := range []string{
		"http://localhost.evil.example", "http://notlocalhost:5000",
		"https://evil.example", "file://", "null", "",
	} {
		recorder := corsResponse(options, http.MethodGet, origin)
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%q was allowed", origin)
		}
	}
}

func TestContentDispositionIsExposed(t *testing.T) {
	recorder := corsResponse(httpapi.CORSOptions{AllowLocalhost: true},
		http.MethodGet, "http://localhost:5000")

	exposed := recorder.Header().Get("Access-Control-Expose-Headers")
	if !strings.Contains(exposed, "Content-Disposition") {
		t.Errorf("expose-headers = %q", exposed)
	}
}
