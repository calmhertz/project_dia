package httpapi

import (
	"net/http"
	"net/url"
	"strings"
)


type CORSOptions struct {
	AllowedOrigins []string
	AllowLocalhost bool
}

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
	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func WithCORS(next http.Handler, options CORSOptions) http.Handler {
	if !options.Enabled() {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		w.Header().Add("Vary", "Origin")

		if options.allows(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Expose-Headers", "Content-Disposition")
		}

		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Add("Vary", "Access-Control-Request-Method")
			w.Header().Add("Vary", "Access-Control-Request-Headers")
			if !options.allows(origin) {
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
