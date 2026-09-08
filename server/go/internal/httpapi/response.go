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

// maxRequestBody bounds request bodies so a large upload cannot exhaust memory.
const maxRequestBody = 1 << 20 // 1 MiB

// errorResponse is the single error shape the API returns.
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

// internalError logs the detail and returns an opaque message. Internal
// diagnostics never reach the client (RULES.md section 13).
func (s *Server) internalError(w http.ResponseWriter, context string, err error) {
	s.logger.Error(context, slog.String("error", err.Error()))
	writeError(s.logger, w, http.StatusInternalServerError, "internal_error", "internal server error")
}

// decodeJSON reads a bounded JSON body and rejects unknown fields.
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	// A second JSON value in one body is a sign of a confused or hostile client.
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("body must contain a single JSON object")
	}
	return nil
}

// writeDomainError maps known service errors onto status codes.
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
		// The database refuses on purpose: passes and their history outlive
		// the account that requested them (spec.md section 13).
		writeError(s.logger, w, http.StatusConflict, "in_use",
			"other records still reference this, so it cannot be removed")
	default:
		s.internalError(w, context, err)
	}
}
