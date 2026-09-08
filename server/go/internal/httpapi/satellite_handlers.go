package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"aagasa/internal/auth"
	"aagasa/internal/catalog"
	"aagasa/internal/domain"
	"aagasa/internal/store"
)

// tleHistoryLimit bounds how much orbital history one response carries.
const tleHistoryLimit = 20

type satelliteView struct {
	ID            string         `json:"id"`
	NoradID       int            `json:"norad_id"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Metadata      map[string]any `json:"metadata"`
	IsSchedulable bool           `json:"is_schedulable"`
	CreatedAt     time.Time      `json:"created_at"`
}

func toSatelliteView(satellite domain.Satellite) satelliteView {
	metadata := satellite.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	return satelliteView{
		ID: satellite.ID.String(), NoradID: satellite.NoradID,
		Name: satellite.Name, Description: satellite.Description,
		Metadata: metadata, IsSchedulable: satellite.IsSchedulable,
		CreatedAt: satellite.CreatedAt,
	}
}

type tleView struct {
	Line1     string    `json:"line1"`
	Line2     string    `json:"line2"`
	Epoch     time.Time `json:"epoch"`
	Source    string    `json:"source"`
	FetchedAt time.Time `json:"fetched_at"`
	// AgeHours lets the UI show freshness without doing date arithmetic.
	AgeHours float64 `json:"age_hours"`
}

func toTLEView(record domain.TLERecord, now time.Time) tleView {
	return tleView{
		Line1: record.Line1, Line2: record.Line2, Epoch: record.Epoch,
		Source: string(record.Source), FetchedAt: record.FetchedAt,
		AgeHours: now.Sub(record.Epoch).Hours(),
	}
}

// handleListSatellites returns the catalogue. Any authenticated user may read
// it: satellites are not private information.
func (s *Server) handleListSatellites(w http.ResponseWriter, r *http.Request) {
	satellites, err := s.repo.ListSatellites(r.Context())
	if err != nil {
		s.internalError(w, "list satellites", err)
		return
	}
	views := make([]satelliteView, 0, len(satellites))
	for _, satellite := range satellites {
		views = append(views, toSatelliteView(satellite))
	}
	writeJSON(s.logger, w, http.StatusOK, map[string]any{"satellites": views})
}

func (s *Server) handleGetSatellite(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathUUID(w, r)
	if !ok {
		return
	}
	satellite, err := s.repo.GetSatelliteByID(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, "get satellite", err)
		return
	}

	response := map[string]any{"satellite": toSatelliteView(satellite)}

	// A satellite with no orbital data is still a valid record; report the
	// absence rather than failing the read.
	if record, err := s.catalog.CurrentTLE(r.Context(), satellite.ID); err == nil {
		response["current_tle"] = toTLEView(record, time.Now().UTC())
	} else if !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, "load current tle", err)
		return
	}

	writeJSON(s.logger, w, http.StatusOK, response)
}

func (s *Server) handleGetSatelliteTLEs(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathUUID(w, r)
	if !ok {
		return
	}
	records, err := s.repo.ListTLERecords(r.Context(), id, tleHistoryLimit)
	if err != nil {
		s.writeDomainError(w, "list tle records", err)
		return
	}
	now := time.Now().UTC()
	views := make([]tleView, 0, len(records))
	for _, record := range records {
		views = append(views, toTLEView(record, now))
	}
	writeJSON(s.logger, w, http.StatusOK, map[string]any{"tles": views})
}

type addSatelliteRequest struct {
	NoradID int    `json:"norad_id"`
	Name    string `json:"name"`
}

// handleAddSatellite registers a satellite by catalog number. Root only, per
// spec.md section 17.1's catalogue administration.
func (s *Server) handleAddSatellite(w http.ResponseWriter, r *http.Request) {
	if !auth.CanConfigureStation(currentUser(r)) {
		writeError(s.logger, w, http.StatusForbidden, "forbidden", "catalogue administration is root only")
		return
	}

	var request addSatelliteRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	if request.NoradID <= 0 {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "norad_id must be positive")
		return
	}

	satellite, err := s.catalog.AddSatellite(r.Context(), request.NoradID, strings.TrimSpace(request.Name))
	switch {
	case errors.Is(err, catalog.ErrAlreadyExists):
		writeError(s.logger, w, http.StatusConflict, "conflict", "satellite is already in the catalogue")
		return
	case errors.Is(err, catalog.ErrNoOrbitalData):
		// The providers are external; a failure there is not our fault and
		// not the client's.
		writeError(s.logger, w, http.StatusBadGateway, "no_orbital_data",
			"no orbital data is available for that catalog number")
		return
	case err != nil:
		s.writeDomainError(w, "add satellite", err)
		return
	}

	s.audit(r, "satellite.added", "satellite", satellite.ID.String(), map[string]any{
		"norad_id": satellite.NoradID, "name": satellite.Name,
	})
	writeJSON(s.logger, w, http.StatusCreated, toSatelliteView(satellite))
}

type updateSatelliteRequest struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	IsSchedulable *bool   `json:"is_schedulable"`
}

func (s *Server) handleUpdateSatellite(w http.ResponseWriter, r *http.Request) {
	if !auth.CanConfigureStation(currentUser(r)) {
		writeError(s.logger, w, http.StatusForbidden, "forbidden", "catalogue administration is root only")
		return
	}
	id, ok := s.pathUUID(w, r)
	if !ok {
		return
	}

	var request updateSatelliteRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	existing, err := s.repo.GetSatelliteByID(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, "get satellite", err)
		return
	}

	name, description, schedulable := existing.Name, existing.Description, existing.IsSchedulable
	if request.Name != nil {
		if strings.TrimSpace(*request.Name) == "" {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "name cannot be empty")
			return
		}
		name = strings.TrimSpace(*request.Name)
	}
	if request.Description != nil {
		description = *request.Description
	}
	if request.IsSchedulable != nil {
		schedulable = *request.IsSchedulable
	}

	updated, err := s.repo.UpdateSatellite(r.Context(), id, name, description, schedulable)
	if err != nil {
		s.writeDomainError(w, "update satellite", err)
		return
	}

	s.audit(r, "satellite.updated", "satellite", id.String(), map[string]any{
		"name": updated.Name, "is_schedulable": updated.IsSchedulable,
	})
	writeJSON(s.logger, w, http.StatusOK, toSatelliteView(updated))
}

func (s *Server) handleDeleteSatellite(w http.ResponseWriter, r *http.Request) {
	if !auth.CanConfigureStation(currentUser(r)) {
		writeError(s.logger, w, http.StatusForbidden, "forbidden", "catalogue administration is root only")
		return
	}
	id, ok := s.pathUUID(w, r)
	if !ok {
		return
	}

	satellite, err := s.repo.GetSatelliteByID(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, "get satellite", err)
		return
	}

	if err := s.repo.DeleteSatellite(r.Context(), id); err != nil {
		// A satellite with passes cannot be removed: history is preserved
		// rather than deleted (spec.md section 21).
		writeError(s.logger, w, http.StatusConflict, "conflict",
			"this satellite has passes and cannot be deleted")
		return
	}

	s.audit(r, "satellite.deleted", "satellite", id.String(), map[string]any{
		"norad_id": satellite.NoradID, "name": satellite.Name,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleRefreshTLEs triggers an immediate refresh of the whole catalogue.
func (s *Server) handleRefreshTLEs(w http.ResponseWriter, r *http.Request) {
	if !auth.CanManageUsers(currentUser(r)) {
		writeError(s.logger, w, http.StatusForbidden, "forbidden", "not permitted")
		return
	}

	outcomes := s.catalog.RefreshAll(r.Context(), true)

	results := make([]map[string]any, 0, len(outcomes))
	updated, kept, failed := 0, 0, 0
	for _, outcome := range outcomes {
		entry := map[string]any{
			"norad_id":             outcome.NoradID,
			"updated":              outcome.Updated,
			"used_last_known_good": outcome.UsedLastKnownGood,
		}
		if outcome.Source != "" {
			entry["source"] = string(outcome.Source)
		}
		switch {
		case outcome.Err != nil:
			failed++
			entry["error"] = "no orbital data available"
		case outcome.UsedLastKnownGood:
			kept++
		case outcome.Updated:
			updated++
		}
		results = append(results, entry)
	}

	s.audit(r, "tle.refreshed", "catalogue", "", map[string]any{
		"updated": updated, "kept_last_known_good": kept, "failed": failed,
	})
	writeJSON(s.logger, w, http.StatusOK, map[string]any{
		"updated": updated, "kept_last_known_good": kept, "failed": failed,
		"results": results,
	})
}

// handleRefreshSatelliteMetadata re-fetches display metadata for one satellite.
// Useful when enrichment failed transiently when the satellite was added.
func (s *Server) handleRefreshSatelliteMetadata(w http.ResponseWriter, r *http.Request) {
	if !auth.CanConfigureStation(currentUser(r)) {
		writeError(s.logger, w, http.StatusForbidden, "forbidden", "catalogue administration is root only")
		return
	}
	id, ok := s.pathUUID(w, r)
	if !ok {
		return
	}

	satellite, err := s.repo.GetSatelliteByID(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, "get satellite", err)
		return
	}

	metadata, err := s.catalog.RefreshMetadata(r.Context(), satellite)
	if err != nil {
		// The provider is external; its outage is not a client error.
		writeError(s.logger, w, http.StatusBadGateway, "metadata_unavailable",
			"the metadata provider did not answer")
		return
	}

	s.audit(r, "satellite.metadata_refreshed", "satellite", id.String(), nil)
	writeJSON(s.logger, w, http.StatusOK, map[string]any{"metadata": metadata})
}
