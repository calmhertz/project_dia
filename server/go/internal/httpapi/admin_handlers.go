package httpapi

import (
	"errors"
	"math"
	"net/http"
	"time"

	"aagasa/internal/domain"
	"aagasa/internal/store"
)


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
