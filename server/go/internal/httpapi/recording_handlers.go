package httpapi

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"

	"aagasa/internal/auth"
	"aagasa/internal/domain"
	"aagasa/internal/scheduling"
	"aagasa/internal/store"
)

type recordingView struct {
	RecordingID  string    `json:"recording_id"`
	PassID       string    `json:"pass_id"`
	RelativePath string    `json:"relative_path"`
	SizeBytes    int64     `json:"size_bytes"`
	Checksum     string    `json:"checksum_sha256"`
	Status       string    `json:"status"`
	ReceivedAt   time.Time `json:"received_at"`
	DownloadURL  string    `json:"download_url"`
}

func toRecordingView(recording store.Recording) recordingView {
	return recordingView{
		RecordingID: recording.RecordingID, PassID: recording.PassID.String(),
		RelativePath: recording.RelativePath, SizeBytes: recording.SizeBytes,
		Checksum: recording.ChecksumSHA256, Status: recording.Status,
		ReceivedAt:  recording.ReceivedAt,
		DownloadURL: "/api/recordings/" + recording.RecordingID + "/download",
	}
}

const recordingsListLimit = 200

type recordingListView struct {
	recordingView
	SatelliteID string    `json:"satellite_id"`
	PassAOS     time.Time `json:"pass_aos"`
	Visibility  string    `json:"visibility"`
}

func (s *Server) handleListRecordings(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r)

	var ownerID *uuid.UUID
	publicOnly, includeAll := false, false

	switch r.URL.Query().Get("scope") {
	case "", "mine":
		ownerID = &actor.ID
	case "public":
		publicOnly = true
	case "all":
		if !auth.AtLeast(actor, domain.RoleAdmin) {
			writeError(s.logger, w, http.StatusForbidden, "forbidden", "not permitted")
			return
		}
		includeAll = true
	default:
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
			"scope must be mine, public or all")
		return
	}

	recordings, err := s.repo.ListRecordingsVisibleTo(r.Context(), ownerID,
		publicOnly, includeAll, recordingsListLimit)
	if err != nil {
		s.internalError(w, "list recordings", err)
		return
	}

	views := make([]recordingListView, 0, len(recordings))
	for _, entry := range recordings {
		views = append(views, recordingListView{
			recordingView: toRecordingView(entry.Recording),
			SatelliteID:   entry.SatelliteID.String(),
			PassAOS:       entry.PassAOS,
			Visibility:    string(entry.Visibility),
		})
	}
	writeJSON(s.logger, w, http.StatusOK, map[string]any{"recordings": views})
}

func (s *Server) handleListPassRecordings(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathUUID(w, r)
	if !ok {
		return
	}

	pass, err := s.repo.GetPassByID(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, "get pass", err)
		return
	}
	if !scheduling.CanViewPass(currentUser(r), pass) {
		writeError(s.logger, w, http.StatusNotFound, "not_found", "not found")
		return
	}

	recordings, err := s.repo.ListRecordingsForPass(r.Context(), id)
	if err != nil {
		s.internalError(w, "list recordings", err)
		return
	}

	views := make([]recordingView, 0, len(recordings))
	for _, recording := range recordings {
		views = append(views, toRecordingView(recording))
	}
	writeJSON(s.logger, w, http.StatusOK, map[string]any{"recordings": views})
}

func (s *Server) handleDownloadRecording(w http.ResponseWriter, r *http.Request) {
	recordingID := r.PathValue("id")
	if !isSHA256Hex(recordingID) {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "invalid recording id")
		return
	}

	recording, err := s.repo.GetRecording(r.Context(), recordingID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(s.logger, w, http.StatusNotFound, "not_found", "not found")
			return
		}
		s.internalError(w, "get recording", err)
		return
	}

	pass, err := s.repo.GetPassByID(r.Context(), recording.PassID)
	if err != nil {
		s.internalError(w, "get pass for recording", err)
		return
	}
	if !scheduling.CanViewPass(currentUser(r), pass) {
		writeError(s.logger, w, http.StatusNotFound, "not_found", "not found")
		return
	}

	file, err := os.Open(recording.StoredPath)
	if err != nil {
		s.logger.Error("open recording", "error", err.Error(),
			"recording_id", recording.RecordingID)
		writeError(s.logger, w, http.StatusNotFound, "content_missing",
			"the recording content is no longer available")
		return
	}
	defer file.Close()

	name := filepath.Base(recording.RelativePath)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(recording.SizeBytes, 10))
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)

	if _, err := io.Copy(w, file); err != nil {
		s.logger.Warn("recording download interrupted", "error", err.Error())
	}
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		switch {
		case character >= '0' && character <= '9':
		case character >= 'a' && character <= 'f':
		default:
			return false
		}
	}
	return true
}
