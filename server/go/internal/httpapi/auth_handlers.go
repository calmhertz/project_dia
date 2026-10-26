package httpapi

import (
	"net/http"
	"strings"
	"time"

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

	result, err := s.auth.Login(r.Context(), request.Username, request.Password)
	if err != nil {
		s.writeDomainError(w, "login", err)
		return
	}

	writeJSON(s.logger, w, http.StatusOK, loginResponse{
		Token:              result.Token,
		ExpiresAt:          result.ExpiresAt,
		MustChangePassword: result.MustChangePassword,
		User:               toUserView(result.User),
	})
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
