package httpapi

import (
	"errors"
	"math"
	"net/http"
	"time"

	"aagasa/internal/domain"
	"aagasa/internal/store"
)

// Read-only operational views for Admin and Root (V14).
//
// These disclose station operation, not station configuration: serial ports,
// gains and device identifiers belong to the Root surface in V15
// (client-spec section 4).

type stationStatusResponse struct {
	StationID           string    `json:"station_id"`
	Name                string    `json:"name"`
	LatitudeDegrees     float64   `json:"latitude_degrees"`
	LongitudeDegrees    float64   `json:"longitude_degrees"`
	AltitudeM           float64   `json:"altitude_m"`
	Timezone            string    `json:"timezone"`
	ActiveRFBand        string    `json:"active_rf_band"`
	InitializedAt       time.Time `json:"initialized_at"`
	ConfiguredBands     []string  `json:"configured_bands"`
	AntennaDescriptions []string  `json:"antenna_descriptions"`
}

// handleStationStatus reports where the station is and what it is listening
// on, which an Admin needs to interpret every other operational screen.
func (s *Server) handleStationStatus(w http.ResponseWriter, r *http.Request) {
	station, ok := s.primaryStation(w, r)
	if !ok {
		return
	}

	bands, err := s.repo.GetStationBandConfigs(r.Context(), station.ID)
	if err != nil {
		s.internalError(w, "load band configs", err)
		return
	}
	names := make([]string, 0, len(bands))
	antennas := make([]string, 0, len(bands))
	for _, band := range bands {
		names = append(names, string(band.Band))
		antennas = append(antennas, band.AntennaDescription)
	}

	writeJSON(s.logger, w, http.StatusOK, stationStatusResponse{
		StationID: station.ID.String(), Name: station.Name,
		LatitudeDegrees: station.Latitude, LongitudeDegrees: station.Longitude,
		AltitudeM: station.AltitudeM, Timezone: station.Timezone,
		ActiveRFBand: string(station.ActiveRFBand), InitializedAt: station.CreatedAt,
		ConfiguredBands: names, AntennaDescriptions: antennas,
	})
}

type schedulingConfigResponse struct {
	MinimumLeadTimeSeconds   int64     `json:"minimum_lead_time_seconds"`
	PrePassBufferSeconds     int64     `json:"pre_pass_buffer_seconds"`
	PostPassBufferSeconds    int64     `json:"post_pass_buffer_seconds"`
	RecordingPreRollSeconds  int64     `json:"recording_pre_roll_seconds"`
	RecordingPostRollSeconds int64     `json:"recording_post_roll_seconds"`
	MinimumElevationDegrees  float64   `json:"minimum_elevation_degrees"`
	EffectiveFrom            time.Time `json:"effective_from"`
	CreatedAt                time.Time `json:"created_at"`

	// Pending is a saved change that has not taken effect yet. Without it a
	// Root who shortens the lead time sees the old values return and
	// concludes the save was lost.
	Pending *pendingConfigView `json:"pending,omitempty"`
}

type pendingConfigView struct {
	MinimumLeadTimeSeconds   int64     `json:"minimum_lead_time_seconds"`
	PrePassBufferSeconds     int64     `json:"pre_pass_buffer_seconds"`
	PostPassBufferSeconds    int64     `json:"post_pass_buffer_seconds"`
	RecordingPreRollSeconds  int64     `json:"recording_pre_roll_seconds"`
	RecordingPostRollSeconds int64     `json:"recording_post_roll_seconds"`
	MinimumElevationDegrees  float64   `json:"minimum_elevation_degrees"`
	EffectiveFrom            time.Time `json:"effective_from"`
}

// handleGetSchedulingConfig returns the configuration in force now.
//
// Readable by Admin, writable only by Root: an Admin has to know the rules
// their approvals are judged by, which is not the same as being able to
// change them (spec.md section 13.6).
func (s *Server) handleGetSchedulingConfig(w http.ResponseWriter, r *http.Request) {
	station, ok := s.primaryStation(w, r)
	if !ok {
		return
	}

	config, err := s.repo.LatestEffectiveSchedulingConfig(r.Context(), station.ID, time.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(s.logger, w, http.StatusConflict, "not_initialized",
				"complete first-run setup first")
			return
		}
		s.internalError(w, "load scheduling config", err)
		return
	}

	response := schedulingConfigResponse{
		MinimumLeadTimeSeconds:   int64(config.MinimumLeadTime.Seconds()),
		PrePassBufferSeconds:     int64(config.PrePassBuffer.Seconds()),
		PostPassBufferSeconds:    int64(config.PostPassBuffer.Seconds()),
		RecordingPreRollSeconds:  int64(config.RecordingPreRoll.Seconds()),
		RecordingPostRollSeconds: int64(config.RecordingPostRoll.Seconds()),
		MinimumElevationDegrees:  config.MinimumElevationDegrees,
		EffectiveFrom:            config.EffectiveFrom,
		CreatedAt:                config.CreatedAt,
	}

	if pending, err := s.repo.PendingSchedulingConfig(r.Context(), station.ID, time.Now().UTC()); err == nil {
		response.Pending = &pendingConfigView{
			MinimumLeadTimeSeconds:   int64(pending.MinimumLeadTime.Seconds()),
			PrePassBufferSeconds:     int64(pending.PrePassBuffer.Seconds()),
			PostPassBufferSeconds:    int64(pending.PostPassBuffer.Seconds()),
			RecordingPreRollSeconds:  int64(pending.RecordingPreRoll.Seconds()),
			RecordingPostRollSeconds: int64(pending.RecordingPostRoll.Seconds()),
			MinimumElevationDegrees:  pending.MinimumElevationDegrees,
			EffectiveFrom:            pending.EffectiveFrom,
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		s.internalError(w, "load pending scheduling config", err)
		return
	}

	writeJSON(s.logger, w, http.StatusOK, response)
}

// staleTLEHours is when orbital data stops being good enough to plan with.
// SGP4 accuracy degrades over days, so a week is already a warning.
const staleTLEHours = 72

type tleStatusEntry struct {
	SatelliteID  string     `json:"satellite_id"`
	NoradID      int        `json:"norad_id"`
	Name         string     `json:"name"`
	HasOrbitData bool       `json:"has_orbital_data"`
	Epoch        *time.Time `json:"epoch,omitempty"`
	Source       string     `json:"source,omitempty"`
	FetchedAt    *time.Time `json:"fetched_at,omitempty"`
	AgeHours     *float64   `json:"age_hours,omitempty"`
	Stale        bool       `json:"stale"`
}

type tleStatusResponse struct {
	Satellites    []tleStatusEntry `json:"satellites"`
	StaleAfterHrs int              `json:"stale_after_hours"`
	MissingCount  int              `json:"missing_count"`
	StaleCount    int              `json:"stale_count"`
}

// handleTLEStatus reports the freshness of the whole catalogue's orbital data.
//
// An Admin can refresh TLEs, so they need to see which satellites are stale
// or have never had data at all; a per-satellite page cannot answer that.
func (s *Server) handleTLEStatus(w http.ResponseWriter, r *http.Request) {
	satellites, err := s.repo.ListSatellites(r.Context())
	if err != nil {
		s.internalError(w, "list satellites", err)
		return
	}
	latest, err := s.repo.LatestTLERecordForEach(r.Context())
	if err != nil {
		s.internalError(w, "list latest tles", err)
		return
	}

	now := time.Now().UTC()
	response := tleStatusResponse{
		Satellites:    make([]tleStatusEntry, 0, len(satellites)),
		StaleAfterHrs: staleTLEHours,
	}

	for _, satellite := range satellites {
		entry := tleStatusEntry{
			SatelliteID: satellite.ID.String(), NoradID: satellite.NoradID,
			Name: satellite.Name,
		}
		record, present := latest[satellite.ID]
		if !present {
			// Never had orbital data. Distinct from stale, and distinct from
			// an error: the catalogue entry is real, the data is not there.
			response.MissingCount++
			response.Satellites = append(response.Satellites, entry)
			continue
		}

		age := math.Round(now.Sub(record.Epoch).Hours()*10) / 10
		epoch, fetched := record.Epoch, record.FetchedAt
		entry.HasOrbitData = true
		entry.Epoch = &epoch
		entry.Source = string(record.Source)
		entry.FetchedAt = &fetched
		entry.AgeHours = &age
		entry.Stale = age > staleTLEHours
		if entry.Stale {
			response.StaleCount++
		}
		response.Satellites = append(response.Satellites, entry)
	}

	writeJSON(s.logger, w, http.StatusOK, response)
}

// primaryStation resolves the single station this deployment runs, answering
// the caller itself when there is none yet.
func (s *Server) primaryStation(w http.ResponseWriter, r *http.Request) (domain.Station, bool) {
	stations, err := s.repo.ListStations(r.Context())
	if err != nil {
		s.internalError(w, "list stations", err)
		return domain.Station{}, false
	}
	if len(stations) == 0 {
		writeError(s.logger, w, http.StatusConflict, "not_initialized",
			"complete first-run setup first")
		return domain.Station{}, false
	}
	return stations[0], true
}
