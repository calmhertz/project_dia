package httpapi

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"aagasa/internal/auth"
	"aagasa/internal/domain"
)

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.repo.ListUsers(r.Context())
	if err != nil {
		s.internalError(w, "list users", err)
		return
	}
	views := make([]userView, 0, len(users))
	for _, user := range users {
		views = append(views, toUserView(user))
	}
	writeJSON(s.logger, w, http.StatusOK, map[string]any{"users": views})
}

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var request createUserRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	username := strings.TrimSpace(request.Username)
	if username == "" {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "username is required")
		return
	}
	role := domain.UserRole(request.Role)
	if request.Role == "" {
		role = domain.RoleUser
	}

	actor := currentUser(r)
	if !auth.CanCreateUserWithRole(actor, role) {
		writeError(s.logger, w, http.StatusForbidden, "forbidden", "not permitted to create a user with that role")
		return
	}
	if err := auth.ValidatePassword(request.Password); err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_password", err.Error())
		return
	}

	hash, err := auth.HashPassword(request.Password)
	if err != nil {
		s.internalError(w, "hash password", err)
		return
	}

	created, err := s.repo.CreateUser(r.Context(), domain.User{
		Username: username, PasswordHash: hash, Role: role,
		// A new account changes its password on first login.
		MustChangePassword: true,
	})
	if err != nil {
		s.writeDomainError(w, "create user", err)
		return
	}

	s.audit(r, "user.created", "user", created.ID.String(), map[string]any{
		"username": created.Username, "role": string(created.Role),
	})
	writeJSON(s.logger, w, http.StatusCreated, toUserView(created))
}

type updateUserRequest struct {
	Role *string `json:"role"`
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	targetID, ok := s.pathUUID(w, r)
	if !ok {
		return
	}

	var request updateUserRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	if request.Role == nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "role is required")
		return
	}

	target, err := s.repo.GetUserByID(r.Context(), targetID)
	if err != nil {
		s.writeDomainError(w, "load user", err)
		return
	}

	newRole := domain.UserRole(*request.Role)
	actor := currentUser(r)
	if !auth.CanChangeRole(actor, target, newRole) {
		writeError(s.logger, w, http.StatusForbidden, "forbidden", "not permitted to change that role")
		return
	}

	if err := s.repo.SetUserRole(r.Context(), targetID, newRole); err != nil {
		s.writeDomainError(w, "set role", err)
		return
	}

	s.audit(r, "user.role_changed", "user", targetID.String(), map[string]any{
		"previous_role": string(target.Role), "new_role": string(newRole),
	})

	updated, err := s.repo.GetUserByID(r.Context(), targetID)
	if err != nil {
		s.internalError(w, "reload user", err)
		return
	}
	writeJSON(s.logger, w, http.StatusOK, toUserView(updated))
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	targetID, ok := s.pathUUID(w, r)
	if !ok {
		return
	}

	target, err := s.repo.GetUserByID(r.Context(), targetID)
	if err != nil {
		s.writeDomainError(w, "load user", err)
		return
	}

	if !auth.CanDeleteUser(currentUser(r), target) {
		writeError(s.logger, w, http.StatusForbidden, "forbidden", "not permitted to delete that user")
		return
	}

	if err := s.repo.DeleteUser(r.Context(), targetID); err != nil {
		s.writeDomainError(w, "delete user", err)
		return
	}

	s.audit(r, "user.deleted", "user", targetID.String(), map[string]any{
		"username": target.Username, "role": string(target.Role),
	})
	w.WriteHeader(http.StatusNoContent)
}

// pathUUID reads and validates the {id} path segment.
func (s *Server) pathUUID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "invalid id")
		return uuid.UUID{}, false
	}
	return id, true
}
