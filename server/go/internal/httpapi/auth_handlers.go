package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aagasa/internal/auth"
	"aagasa/internal/domain"
)

type userView struct {
	ID                 string    `json:"id"`
	Username           string    `json:"username"`
	Role               string    `json:"role"`
	MustChangePassword bool      `json:"must_change_password"`
	CreatedAt          time.Time `json:"created_at"`
}

func toUserView(user domain.User) userView {
	return userView{
		ID:                 user.ID.String(),
		Username:           user.Username,
		Role:               string(user.Role),
		MustChangePassword: user.MustChangePassword,
		CreatedAt:          user.CreatedAt,
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token              string    `json:"token"`
	ExpiresAt          time.Time `json:"expires_at"`
	MustChangePassword bool      `json:"must_change_password"`
	User               userView  `json:"user"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	if strings.TrimSpace(request.Username) == "" || request.Password == "" {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "username and password are required")
		return
	}

	client := clientAddress(r)
	if s.loginBlocked(w, r, request.Username, client) {
		return
	}

	result, err := s.auth.Login(r.Context(), request.Username, request.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			s.recordLoginFailure(r, request.Username, client)
		}
		s.writeDomainError(w, "login", err)
		return
	}
	s.clearLoginFailures(r, request.Username)

	writeJSON(s.logger, w, http.StatusOK, loginResponse{
		Token:              result.Token,
		ExpiresAt:          result.ExpiresAt,
		MustChangePassword: result.MustChangePassword,
		User:               toUserView(result.User),
	})
}

func (s *Server) loginBlocked(w http.ResponseWriter, r *http.Request, username, client string) bool {
	if s.attempts == nil {
		return false
	}
	blocked, retryAfter, err := s.attempts.Blocked(r.Context(), username, client)
	if err != nil {
		s.logger.Error("check login attempts", slog.String("error", err.Error()))
		return false
	}
	if !blocked {
		return false
	}
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	s.logger.Warn("login refused; too many failed attempts", slog.String("client", client))
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeError(s.logger, w, http.StatusTooManyRequests, "too_many_attempts",
		fmt.Sprintf("too many failed sign-in attempts; try again in %d minute(s)",
			int(math.Ceil(float64(seconds)/60))))
	return true
}

func (s *Server) recordLoginFailure(r *http.Request, username, client string) {
	if s.attempts == nil {
		return
	}
	if err := s.attempts.RecordFailure(r.Context(), username, client); err != nil {
		s.logger.Error("record login failure", slog.String("error", err.Error()))
	}
}

func (s *Server) clearLoginFailures(r *http.Request, username string) {
	if s.attempts == nil {
		return
	}
	if err := s.attempts.Succeeded(r.Context(), username); err != nil {
		s.logger.Error("clear login failures", slog.String("error", err.Error()))
	}
}

// clientAddress is the TCP peer. X-Forwarded-For is deliberately ignored: any
// client can set it, which would let an attacker pick a fresh identity per try.
func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Logout(r.Context(), currentToken(r)); err != nil {
		s.internalError(w, "logout", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(s.logger, w, http.StatusOK, toUserView(currentUser(r)))
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var request changePasswordRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	user := currentUser(r)
	if err := s.auth.ChangePassword(r.Context(), user.ID, request.CurrentPassword, request.NewPassword); err != nil {
		s.writeDomainError(w, "change password", err)
		return
	}

	if err := s.auth.Logout(r.Context(), currentToken(r)); err != nil {
		s.internalError(w, "revoke session after password change", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
