package httpapi

import (
	"net/http"
	"time"

	"aagasa/internal/domain"
	"aagasa/internal/workerapi"
)

type workerView struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	StationID       string     `json:"station_id"`
	ConnectionState string     `json:"connection_state"`
	WorkerVersion   string     `json:"worker_version,omitempty"`
	LastSeenAt      *time.Time `json:"last_seen_at,omitempty"`
	LastSyncAt      *time.Time `json:"last_sync_at,omitempty"`
	InSync bool `json:"in_sync"`
	PendingUploads int `json:"pending_uploads"`
	PendingReports int `json:"pending_reports"`
	SecondsSinceSeen *float64 `json:"seconds_since_seen,omitempty"`
}

func (s *Server) handleListWorkers(w http.ResponseWriter, r *http.Request) {
	workers, err := s.repo.ListWorkers(r.Context())
	if err != nil {
		s.internalError(w, "list workers", err)
		return
	}

	now := time.Now().UTC()
	views := make([]workerView, 0, len(workers))
	for _, worker := range workers {
		view := workerView{
			ID: worker.ID.String(), Name: worker.Name,
			StationID:       worker.StationID.String(),
			ConnectionState: string(worker.ConnectionState),
			WorkerVersion:   worker.WorkerVersion,
			LastSeenAt:      worker.LastSeenAt,
			LastSyncAt:      worker.LastSyncAt,
			PendingUploads:  worker.PendingUploads,
			PendingReports:  worker.PendingReports,
		}
		if worker.LastSeenAt != nil {
			elapsed := now.Sub(*worker.LastSeenAt).Seconds()
			view.SecondsSinceSeen = &elapsed
		}
		view.InSync = worker.SyncedGeneration != "" &&
			worker.ConnectionState == domain.WorkerOnline
		views = append(views, view)
	}

	writeJSON(s.logger, w, http.StatusOK, map[string]any{
		"workers": views,
		"heartbeat_interval_seconds": int(workerapi.HeartbeatInterval.Seconds()),
		"offline_after_seconds":      int(workerapi.StaleAfter.Seconds()),
	})
}
