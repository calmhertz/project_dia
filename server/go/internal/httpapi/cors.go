package httpapi

import (
	"net/http"
	"net/url"
	"strings"
)

// CORS for the browser client.
//
// The Flutter web build runs on its own origin, so without this every call
// from it is blocked before it reaches a handler. The session is a bearer
// token in a header rather than a cookie, so no credentialed requests are
// permitted and the wildcard origin is never used: the allowed origin is
// echoed back only when it matches, and the reply always varies on Origin.

// CORSOptions says which browser origins may call the API.
type CORSOptions struct {
	// AllowedOrigins are matched exactly, scheme and port included.
	AllowedOrigins []string
	// AllowLocalhost additionally permits any port on localhost. It exists
	// for development, where the Flutter dev server picks a port at random,
	// and should stay off in production.
	AllowLocalhost bool
}

// Enabled reports whether any origin could be allowed.
func (o CORSOptions) Enabled() bool {
	return len(o.AllowedOrigins) > 0 || o.AllowLocalhost
}

func (o CORSOptions) allows(origin string) bool {
	if origin == "" {
		return false
	}
	for _, allowed := range o.AllowedOrigins {
		if allowed == origin {
			return true
		}
	}
	if !o.AllowLocalhost {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	// Hostname() drops the port, so any port on the loopback host matches and
	// nothing else does. "localhost.example.com" is not localhost.
	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

// WithCORS wraps a handler with cross-origin support.
//
// A request from an origin that is not allowed is served without any CORS
// headers, which the browser then blocks. That is deliberate: refusing here
// with an error page would tell a hostile page more than silence does.
func WithCORS(next http.Handler, options CORSOptions) http.Handler {
	if !options.Enabled() {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		// Cached responses must not be shared between origins.
		w.Header().Add("Vary", "Origin")

		if options.allows(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition")
		}

		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			if !options.allows(origin) {
				// No CORS headers, so the browser refuses the real request.
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Methods",
				strings.Join([]string{
					http.MethodGet, http.MethodPost, http.MethodPatch,
					http.MethodPut, http.MethodDelete, http.MethodOptions,
				}, ", "))
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
