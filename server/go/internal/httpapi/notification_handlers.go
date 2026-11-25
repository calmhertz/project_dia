package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"aagasa/internal/store"
)

const notificationListLimit = 100

type notificationView struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`
	Severity   string     `json:"severity"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	EntityType string     `json:"entity_type"`
	EntityID   string     `json:"entity_id,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ReadAt     *time.Time `json:"read_at"`
}

// GET /api/notifications?after_id=N&limit=M
func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	query := r.URL.Query()

	limit := 50
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > notificationListLimit {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
				"limit must be between 1 and "+strconv.Itoa(notificationListLimit))
			return
		}
		limit = parsed
	}
	var afterID int64
	if raw := query.Get("after_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
				"after_id must be a non-negative integer")
			return
		}
		afterID = parsed
	}

	items, err := s.repo.ListNotifications(r.Context(), user.ID, afterID, limit)
	if err != nil {
		s.internalError(w, "list notifications", err)
		return
	}
	unread, err := s.repo.CountUnreadNotifications(r.Context(), user.ID)
	if err != nil {
		s.internalError(w, "count notifications", err)
		return
	}

	views := make([]notificationView, 0, len(items))
	for _, n := range items {
		views = append(views, notificationView{
			ID: n.ID, Kind: n.Kind, Severity: n.Severity, Title: n.Title, Body: n.Body,
			EntityType: n.EntityType, EntityID: n.EntityID,
			CreatedAt: n.CreatedAt, ReadAt: n.ReadAt,
		})
	}
	writeJSON(s.logger, w, http.StatusOK, map[string]any{
		"notifications": views, "unread_count": unread,
	})
}

// POST /api/notifications/{id}/read
func (s *Server) handleMarkNotificationRead(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "invalid notification id")
		return
	}
	if err := s.repo.MarkNotificationRead(r.Context(), currentUser(r).ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(s.logger, w, http.StatusNotFound, "not_found", "notification not found")
			return
		}
		s.internalError(w, "mark notification read", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/notifications/read-all
func (s *Server) handleMarkAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	if err := s.repo.MarkAllNotificationsRead(r.Context(), currentUser(r).ID); err != nil {
		s.internalError(w, "mark notifications read", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
