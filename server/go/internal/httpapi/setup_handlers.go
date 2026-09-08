package httpapi

import (
	"net/http"
	"strings"
	"time"

	"aagasa/internal/auth"
	"aagasa/internal/domain"
)

type setupStatusResponse struct {
	Initialized   bool       `json:"initialized"`
	InitializedAt *time.Time `json:"initialized_at,omitempty"`
}

// handleSetupStatus lets the Client decide whether to show the setup flow. It
// is available to any authenticated user because the UI needs it before roles
// matter, and it discloses nothing beyond whether setup is done.
func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	at, err := s.repo.SystemInitializedAt(r.Context())
	if err != nil {
		s.internalError(w, "read system state", err)
		return
	}
	writeJSON(s.logger, w, http.StatusOK, setupStatusResponse{Initialized: at != nil, InitializedAt: at})
}

type bandConfigRequest struct {
	Band               string   `json:"band"`
	AntennaDescription string   `json:"antenna_description"`
	CenterFrequencyHz  *int64   `json:"center_frequency_hz"`
	SampleRateHz       *int32   `json:"sample_rate_hz"`
	GainDB             *float64 `json:"gain_db"`
	PPMCorrection      int32    `json:"ppm_correction"`
	BiasTeeEnabled     bool     `json:"bias_tee_enabled"`
}

type setupRequest struct {
	StationName  string  `json:"station_name"`
	Latitude     float64 `json:"latitude"`
	Longitude    float64 `json:"longitude"`
	AltitudeM    float64 `json:"altitude_m"`
	Timezone     string  `json:"timezone"`
	ActiveRFBand string  `json:"active_rf_band"`

	Bands []bandConfigRequest `json:"bands"`

	RotatorSerialPort           string `json:"rotator_serial_port"`
	RotatorBaudRate             int32  `json:"rotator_baud_rate"`
	RotatorParkAzimuthDegrees   int32  `json:"rotator_park_azimuth_degrees"`
	RotatorParkElevationDegrees int32  `json:"rotator_park_elevation_degrees"`
	SDRDeviceIdentifier         string `json:"sdr_device_identifier"`

	MinimumLeadTimeSeconds   int64   `json:"minimum_lead_time_seconds"`
	PrePassBufferSeconds     int64   `json:"pre_pass_buffer_seconds"`
	PostPassBufferSeconds    int64   `json:"post_pass_buffer_seconds"`
	RecordingPreRollSeconds  int64   `json:"recording_pre_roll_seconds"`
	RecordingPostRollSeconds int64   `json:"recording_post_roll_seconds"`
	MinimumElevationDegrees  float64 `json:"minimum_elevation_degrees"`

	WorkerName string `json:"worker_name"`
}

// handleSetup performs first-run configuration: station identity and location,
// per-band RF settings, rotator and SDR settings, the initial scheduling
// configuration, and the station's Worker (spec.md section 23).
//
// It runs once. Everything is written in one transaction so a partial setup
// cannot be left behind.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r)
	if !auth.CanRunFirstRunSetup(actor) {
		writeError(s.logger, w, http.StatusForbidden, "forbidden", "first-run setup is root only")
		return
	}

	var request setupRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	if message, ok := validateSetup(&request); !ok {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", message)
		return
	}

	alreadyAt, err := s.repo.SystemInitializedAt(r.Context())
	if err != nil {
		s.internalError(w, "read system state", err)
		return
	}
	if alreadyAt != nil {
		writeError(s.logger, w, http.StatusConflict, "already_initialized",
			"the system has already completed first-run setup")
		return
	}

	now := time.Now().UTC()
	var stationID string

	err = s.repo.InTx(r.Context(), func(tx *storeRepository) error {
		station, err := tx.CreateStation(r.Context(), domain.Station{
			Name: request.StationName, Latitude: request.Latitude,
			Longitude: request.Longitude, AltitudeM: request.AltitudeM,
			Timezone: request.Timezone, ActiveRFBand: domain.RFBand(request.ActiveRFBand),
		})
		if err != nil {
			return err
		}
		stationID = station.ID.String()

		for _, band := range request.Bands {
			if err := tx.UpsertStationBandConfig(r.Context(), domain.StationBandConfig{
				StationID: station.ID, Band: domain.RFBand(band.Band),
				AntennaDescription: band.AntennaDescription,
				CenterFrequencyHz:  band.CenterFrequencyHz,
				SampleRateHz:       band.SampleRateHz, GainDB: band.GainDB,
				PPMCorrection:  band.PPMCorrection,
				BiasTeeEnabled: band.BiasTeeEnabled,
			}); err != nil {
				return err
			}
		}

		if err := tx.UpsertStationHardwareConfig(r.Context(), domain.StationHardwareConfig{
			StationID:                   station.ID,
			RotatorSerialPort:           request.RotatorSerialPort,
			RotatorBaudRate:             request.RotatorBaudRate,
			RotatorParkAzimuthDegrees:   request.RotatorParkAzimuthDegrees,
			RotatorParkElevationDegrees: request.RotatorParkElevationDegrees,
			SDRDeviceIdentifier:         request.SDRDeviceIdentifier,
		}); err != nil {
			return err
		}

		// The first configuration is effective immediately; there is no earlier
		// policy for spec.md section 13.6's delay to protect.
		if _, err := tx.CreateSchedulingConfig(r.Context(), domain.SchedulingConfig{
			StationID:               station.ID,
			MinimumLeadTime:         time.Duration(request.MinimumLeadTimeSeconds) * time.Second,
			PrePassBuffer:           time.Duration(request.PrePassBufferSeconds) * time.Second,
			PostPassBuffer:          time.Duration(request.PostPassBufferSeconds) * time.Second,
			RecordingPreRoll:        time.Duration(request.RecordingPreRollSeconds) * time.Second,
			RecordingPostRoll:       time.Duration(request.RecordingPostRollSeconds) * time.Second,
			MinimumElevationDegrees: request.MinimumElevationDegrees,
			CreatedBy:               &actor.ID,
			EffectiveFrom:           now,
		}); err != nil {
			return err
		}

		if _, err := tx.CreateWorker(r.Context(), domain.Worker{
			StationID: station.ID, Name: request.WorkerName,
			ConnectionState: domain.WorkerOffline,
		}); err != nil {
			return err
		}

		return tx.MarkSystemInitialized(r.Context(), now)
	})
	if err != nil {
		s.writeDomainError(w, "first-run setup", err)
		return
	}

	s.audit(r, "system.initialized", "station", stationID, map[string]any{
		"station_name": request.StationName, "active_rf_band": request.ActiveRFBand,
	})
	writeJSON(s.logger, w, http.StatusCreated, map[string]any{
		"station_id": stationID, "initialized_at": now,
	})
}

// validateSetup checks the request before any write. The database repeats most
// of these checks; failing early gives the operator a usable message.
func validateSetup(request *setupRequest) (string, bool) {
	if strings.TrimSpace(request.StationName) == "" {
		return "station_name is required", false
	}
	if request.Latitude < -90 || request.Latitude > 90 {
		return "latitude must be between -90 and 90", false
	}
	if request.Longitude < -180 || request.Longitude > 180 {
		return "longitude must be between -180 and 180", false
	}
	if request.Timezone == "" {
		request.Timezone = "UTC"
	}
	if !isBand(request.ActiveRFBand) {
		return "active_rf_band must be vhf or uhf", false
	}
	for _, band := range request.Bands {
		if !isBand(band.Band) {
			return "band must be vhf or uhf", false
		}
	}
	if strings.TrimSpace(request.RotatorSerialPort) == "" {
		return "rotator_serial_port is required", false
	}
	if request.RotatorBaudRate <= 0 {
		return "rotator_baud_rate must be positive", false
	}
	// spec.md section 9: the G-550 accepts these ranges and no others.
	if request.RotatorParkAzimuthDegrees < 0 || request.RotatorParkAzimuthDegrees > 359 {
		return "rotator_park_azimuth_degrees must be between 0 and 359", false
	}
	if request.RotatorParkElevationDegrees < 0 || request.RotatorParkElevationDegrees > 90 {
		return "rotator_park_elevation_degrees must be between 0 and 90", false
	}
	for name, value := range map[string]int64{
		"minimum_lead_time_seconds":   request.MinimumLeadTimeSeconds,
		"pre_pass_buffer_seconds":     request.PrePassBufferSeconds,
		"post_pass_buffer_seconds":    request.PostPassBufferSeconds,
		"recording_pre_roll_seconds":  request.RecordingPreRollSeconds,
		"recording_post_roll_seconds": request.RecordingPostRollSeconds,
	} {
		if value < 0 {
			return name + " cannot be negative", false
		}
	}
	if request.MinimumElevationDegrees < 0 || request.MinimumElevationDegrees > 90 {
		return "minimum_elevation_degrees must be between 0 and 90", false
	}
	if strings.TrimSpace(request.WorkerName) == "" {
		return "worker_name is required", false
	}
	return "", true
}

func isBand(value string) bool {
	return value == string(domain.BandVHF) || value == string(domain.BandUHF)
}
