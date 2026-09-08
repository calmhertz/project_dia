package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"aagasa/internal/prediction"
	"aagasa/internal/store"
)

const (
	defaultPredictionHours = 48
	maxPredictionHours     = 24 * 14
	maxTrackStepSeconds    = 600
)

type predictedPassView struct {
	AOS time.Time `json:"aos"`
	TCA time.Time `json:"tca"`
	LOS time.Time `json:"los"`

	AOSAzimuthDegrees float64 `json:"aos_azimuth_degrees"`
	TCAAzimuthDegrees float64 `json:"tca_azimuth_degrees"`
	LOSAzimuthDegrees float64 `json:"los_azimuth_degrees"`

	MaxElevationDegrees float64 `json:"max_elevation_degrees"`
	DurationSeconds     float64 `json:"duration_seconds"`

	Track []trackPointView `json:"track,omitempty"`
}

type trackPointView struct {
	At               time.Time `json:"at"`
	AzimuthDegrees   float64   `json:"azimuth_degrees"`
	ElevationDegrees float64   `json:"elevation_degrees"`
	RangeKm          float64   `json:"range_km"`
}

// handlePredictSatellitePasses lists upcoming passes for one satellite over
// the configured station.
//
// Read-only: it computes nothing itself and reserves nothing. Requesting and
// approving a pass is V7.
func (s *Server) handlePredictSatellitePasses(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathUUID(w, r)
	if !ok {
		return
	}

	hours, ok := s.intQuery(w, r, "hours", defaultPredictionHours, 1, maxPredictionHours)
	if !ok {
		return
	}
	trackStep, ok := s.intQuery(w, r, "track_step_seconds", 0, 0, maxTrackStepSeconds)
	if !ok {
		return
	}

	satellite, err := s.repo.GetSatelliteByID(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, "get satellite", err)
		return
	}

	// The prediction is built from the exact stored element set, so it is
	// reproducible against that TLE version.
	record, err := s.catalog.CurrentTLE(r.Context(), satellite.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(s.logger, w, http.StatusConflict, "no_orbital_data",
				"this satellite has no orbital data yet")
			return
		}
		s.internalError(w, "load current tle", err)
		return
	}

	stations, err := s.repo.ListStations(r.Context())
	if err != nil {
		s.internalError(w, "list stations", err)
		return
	}
	if len(stations) == 0 {
		writeError(s.logger, w, http.StatusConflict, "not_initialized",
			"complete first-run setup before predicting passes")
		return
	}
	station := stations[0]

	now := time.Now().UTC()
	config, err := s.repo.LatestEffectiveSchedulingConfig(r.Context(), station.ID, now)
	if err != nil {
		s.internalError(w, "load scheduling config", err)
		return
	}

	result, err := s.prediction.PredictPasses(r.Context(), prediction.Request{
		Line1:                   record.Line1,
		Line2:                   record.Line2,
		LatitudeDegrees:         station.Latitude,
		LongitudeDegrees:        station.Longitude,
		AltitudeM:               station.AltitudeM,
		MinimumElevationDegrees: config.MinimumElevationDegrees,
		SearchStart:             now,
		SearchEnd:               now.Add(time.Duration(hours) * time.Hour),
		TrackStep:               time.Duration(trackStep) * time.Second,
	})
	if err != nil {
		if errors.Is(err, prediction.ErrInvalidRequest) {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "prediction rejected the request")
			return
		}
		// The prediction service is an internal dependency; its outage is not
		// the client's fault.
		s.logger.Error("prediction failed", "error", err.Error())
		writeError(s.logger, w, http.StatusBadGateway, "prediction_unavailable",
			"the prediction service is unavailable")
		return
	}

	views := make([]predictedPassView, 0, len(result.Passes))
	for _, pass := range result.Passes {
		view := predictedPassView{
			AOS: pass.AOS, TCA: pass.TCA, LOS: pass.LOS,
			AOSAzimuthDegrees:   pass.AOSAzimuthDegrees,
			TCAAzimuthDegrees:   pass.TCAAzimuthDegrees,
			LOSAzimuthDegrees:   pass.LOSAzimuthDegrees,
			MaxElevationDegrees: pass.MaxElevationDegrees,
			DurationSeconds:     pass.Duration.Seconds(),
		}
		for _, point := range pass.Track {
			view.Track = append(view.Track, trackPointView{
				At: point.At, AzimuthDegrees: point.AzimuthDegrees,
				ElevationDegrees: point.ElevationDegrees, RangeKm: point.RangeKm,
			})
		}
		views = append(views, view)
	}

	writeJSON(s.logger, w, http.StatusOK, map[string]any{
		"norad_id":                  result.NoradID,
		"minimum_elevation_degrees": config.MinimumElevationDegrees,
		"station_active_band":       string(station.ActiveRFBand),
		"tle_epoch":                 record.Epoch,
		"passes":                    views,
	})
}

// intQuery reads a bounded integer query parameter.
func (s *Server) intQuery(w http.ResponseWriter, r *http.Request, name string, fallback, low, high int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < low || value > high {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
			name+" must be a whole number between "+strconv.Itoa(low)+" and "+strconv.Itoa(high))
		return 0, false
	}
	return value, true
}
