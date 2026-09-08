package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"aagasa/internal/auth"
	"aagasa/internal/domain"
)

type contextKey string

const (
	userContextKey  contextKey = "user"
	tokenContextKey contextKey = "token"
)

const bearerPrefix = "Bearer "

// currentUser returns the authenticated user placed by requireAuth.
func currentUser(r *http.Request) domain.User {
	user, _ := r.Context().Value(userContextKey).(domain.User)
	return user
}

func currentToken(r *http.Request) string {
	token, _ := r.Context().Value(tokenContextKey).(string)
	return token
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, bearerPrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, bearerPrefix))
}

// requireAuth rejects requests without a valid session.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeError(s.logger, w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}

		user, err := s.auth.Resolve(r.Context(), token)
		if err != nil {
			if errors.Is(err, auth.ErrUnauthenticated) {
				writeError(s.logger, w, http.StatusUnauthorized, "unauthenticated", "authentication required")
				return
			}
			s.internalError(w, "resolve session", err)
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, user)
		ctx = context.WithValue(ctx, tokenContextKey, token)
		next(w, r.WithContext(ctx))
	}
}

// requirePasswordChanged blocks every operation for a user who still owes a
// password change. spec.md section 17.1 requires the change to happen before
// normal use, so this closes the bootstrap credential's window.
func (s *Server) requirePasswordChanged(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r).MustChangePassword {
			writeError(s.logger, w, http.StatusForbidden, "password_change_required",
				"change your password before using the system")
			return
		}
		next(w, r)
	}
}

// requireRole rejects a caller below the required authority.
func (s *Server) requireRole(required domain.UserRole, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth.AtLeast(currentUser(r), required) {
			writeError(s.logger, w, http.StatusForbidden, "forbidden", "not permitted")
			return
		}
		next(w, r)
	}
}

// authenticated composes the checks every normal endpoint needs.
func (s *Server) authenticated(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(s.requirePasswordChanged(next))
}

// roleGated composes authentication, the password-change gate and a role check.
func (s *Server) roleGated(required domain.UserRole, next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(s.requirePasswordChanged(s.requireRole(required, next)))
}
