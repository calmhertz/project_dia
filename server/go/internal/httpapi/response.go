package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"aagasa/internal/auth"
	"aagasa/internal/store"
)

const maxRequestBody = 1 << 20

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeJSON(logger *slog.Logger, w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if payload == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		logger.Error("write response failed", slog.String("error", err.Error()))
	}
}

func writeError(logger *slog.Logger, w http.ResponseWriter, code int, kind, message string) {
	writeJSON(logger, w, code, errorResponse{Error: kind, Message: message})
}

func (s *Server) internalError(w http.ResponseWriter, context string, err error) {
	s.logger.Error(context, slog.String("error", err.Error()))
	writeError(s.logger, w, http.StatusInternalServerError, "internal_error", "internal server error")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("body must contain a single JSON object")
	}
	return nil
}

func (s *Server) writeDomainError(w http.ResponseWriter, context string, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(s.logger, w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
	case errors.Is(err, auth.ErrPasswordChangeRequired):
		writeError(s.logger, w, http.StatusForbidden, "password_change_required", "change your password before using the system")
	case errors.Is(err, auth.ErrForbidden):
		writeError(s.logger, w, http.StatusForbidden, "forbidden", "not permitted")
	case errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordIsBootstrap):
		writeError(s.logger, w, http.StatusBadRequest, "invalid_password", err.Error())
	case errors.Is(err, store.ErrRootProtected):
		writeError(s.logger, w, http.StatusForbidden, "root_protected", "the root account cannot be deleted or demoted")
	case errors.Is(err, store.ErrNotFound):
		writeError(s.logger, w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, store.ErrDuplicate):
		writeError(s.logger, w, http.StatusConflict, "conflict", "already exists")
	case errors.Is(err, store.ErrReferenced):
		writeError(s.logger, w, http.StatusConflict, "in_use",
			"other records still reference this, so it cannot be removed")
	default:
		s.internalError(w, context, err)
	}
}
