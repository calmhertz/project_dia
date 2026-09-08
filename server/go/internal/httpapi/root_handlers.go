package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"aagasa/internal/domain"
	"aagasa/internal/store"
)

// The Root control surface (V15): the complete station configuration, and the
// audit trail.
//
// Two rules run through all of it. A configuration change never rewrites an
// already-approved pass; it reports how many are affected and leaves them
// alone (client-spec section 29). And every change here is audited, because
// these are the actions that most need to be answerable for later.

// upcomingPassWarning counts the passes a configuration change may affect.
//
// Counting rather than changing is the point: the operator is told what is
// already committed under the old settings and decides what to do about it.
func (s *Server) upcomingPassWarning(r *http.Request) (int, error) {
	passes, err := s.repo.ListPasses(r.Context(), store.PassFilter{
		Statuses: []domain.PassStatus{domain.PassPendingApproval, domain.PassApproved},
		From:     time.Now().UTC(),
	})
	if err != nil {
		return 0, err
	}
	return len(passes), nil
}

type bandConfigView struct {
	Band               string   `json:"band"`
	AntennaDescription string   `json:"antenna_description"`
	CenterFrequencyHz  *int64   `json:"center_frequency_hz,omitempty"`
	SampleRateHz       *int32   `json:"sample_rate_hz,omitempty"`
	GainDB             *float64 `json:"gain_db,omitempty"`
	PPMCorrection      int32    `json:"ppm_correction"`
	BiasTeeEnabled     bool     `json:"bias_tee_enabled"`
}

type hardwareConfigView struct {
	RotatorSerialPort           string `json:"rotator_serial_port"`
	RotatorBaudRate             int32  `json:"rotator_baud_rate"`
	RotatorParkAzimuthDegrees   int32  `json:"rotator_park_azimuth_degrees"`
	RotatorParkElevationDegrees int32  `json:"rotator_park_elevation_degrees"`
	SDRDeviceIdentifier         string `json:"sdr_device_identifier"`
}

type stationConfigResponse struct {
	StationID        string             `json:"station_id"`
	Name             string             `json:"name"`
	LatitudeDegrees  float64            `json:"latitude_degrees"`
	LongitudeDegrees float64            `json:"longitude_degrees"`
	AltitudeM        float64            `json:"altitude_m"`
	Timezone         string             `json:"timezone"`
	ActiveRFBand     string             `json:"active_rf_band"`
	Bands            []bandConfigView   `json:"bands"`
	Hardware         hardwareConfigView `json:"hardware"`
	UpcomingPasses   int                `json:"upcoming_passes"`
}

// handleGetStationConfig returns the complete configuration, Root only.
func (s *Server) handleGetStationConfig(w http.ResponseWriter, r *http.Request) {
	station, ok := s.primaryStation(w, r)
	if !ok {
		return
	}

	bands, err := s.repo.GetStationBandConfigs(r.Context(), station.ID)
	if err != nil {
		s.internalError(w, "load band configs", err)
		return
	}
	hardware, err := s.repo.GetStationHardwareConfig(r.Context(), station.ID)
	if err != nil {
		s.internalError(w, "load hardware config", err)
		return
	}
	upcoming, err := s.upcomingPassWarning(r)
	if err != nil {
		s.internalError(w, "count upcoming passes", err)
		return
	}

	views := make([]bandConfigView, 0, len(bands))
	for _, band := range bands {
		views = append(views, bandConfigView{
			Band: string(band.Band), AntennaDescription: band.AntennaDescription,
			CenterFrequencyHz: band.CenterFrequencyHz, SampleRateHz: band.SampleRateHz,
			GainDB: band.GainDB, PPMCorrection: band.PPMCorrection,
			BiasTeeEnabled: band.BiasTeeEnabled,
		})
	}

	writeJSON(s.logger, w, http.StatusOK, stationConfigResponse{
		StationID: station.ID.String(), Name: station.Name,
		LatitudeDegrees: station.Latitude, LongitudeDegrees: station.Longitude,
		AltitudeM: station.AltitudeM, Timezone: station.Timezone,
		ActiveRFBand: string(station.ActiveRFBand),
		Bands:        views,
		Hardware: hardwareConfigView{
			RotatorSerialPort:           hardware.RotatorSerialPort,
			RotatorBaudRate:             hardware.RotatorBaudRate,
			RotatorParkAzimuthDegrees:   hardware.RotatorParkAzimuthDegrees,
			RotatorParkElevationDegrees: hardware.RotatorParkElevationDegrees,
			SDRDeviceIdentifier:         hardware.SDRDeviceIdentifier,
		},
		UpcomingPasses: upcoming,
	})
}

type updateStationRequest struct {
	Name             *string  `json:"name"`
	LatitudeDegrees  *float64 `json:"latitude_degrees"`
	LongitudeDegrees *float64 `json:"longitude_degrees"`
	AltitudeM        *float64 `json:"altitude_m"`
	Timezone         *string  `json:"timezone"`
	ActiveRFBand     *string  `json:"active_rf_band"`
}

// handleUpdateStation changes station identity, location or active band.
//
// Moving the station changes every future prediction, and switching bands
// changes what the next pass records. Neither rewrites a pass that is already
// approved (spec.md section 13.8).
func (s *Server) handleUpdateStation(w http.ResponseWriter, r *http.Request) {
	var request updateStationRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	station, ok := s.primaryStation(w, r)
	if !ok {
		return
	}
	previous := station

	updated := station
	if request.Name != nil {
		if strings.TrimSpace(*request.Name) == "" {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "name cannot be empty")
			return
		}
		updated.Name = strings.TrimSpace(*request.Name)
	}
	if request.LatitudeDegrees != nil {
		if *request.LatitudeDegrees < -90 || *request.LatitudeDegrees > 90 {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
				"latitude_degrees must be between -90 and 90")
			return
		}
		updated.Latitude = *request.LatitudeDegrees
	}
	if request.LongitudeDegrees != nil {
		if *request.LongitudeDegrees < -180 || *request.LongitudeDegrees > 180 {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
				"longitude_degrees must be between -180 and 180")
			return
		}
		updated.Longitude = *request.LongitudeDegrees
	}
	if request.AltitudeM != nil {
		updated.AltitudeM = *request.AltitudeM
	}
	if request.Timezone != nil && strings.TrimSpace(*request.Timezone) != "" {
		updated.Timezone = strings.TrimSpace(*request.Timezone)
	}
	if request.ActiveRFBand != nil && !isBand(*request.ActiveRFBand) {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
			"active_rf_band must be vhf or uhf")
		return
	}

	saved, err := s.repo.UpdateStation(r.Context(), updated)
	if err != nil {
		s.writeDomainError(w, "update station", err)
		return
	}
	if request.ActiveRFBand != nil && *request.ActiveRFBand != string(previous.ActiveRFBand) {
		if err := s.repo.SetStationActiveBand(r.Context(), station.ID,
			domain.RFBand(*request.ActiveRFBand)); err != nil {
			s.writeDomainError(w, "set active band", err)
			return
		}
		saved.ActiveRFBand = domain.RFBand(*request.ActiveRFBand)
	}

	upcoming, err := s.upcomingPassWarning(r)
	if err != nil {
		s.internalError(w, "count upcoming passes", err)
		return
	}

	s.auditChange(r, "station.updated", "station", station.ID.String(),
		map[string]any{
			"name": previous.Name, "latitude": previous.Latitude,
			"longitude": previous.Longitude, "altitude_m": previous.AltitudeM,
			"timezone": previous.Timezone, "active_rf_band": string(previous.ActiveRFBand),
		},
		map[string]any{
			"name": saved.Name, "latitude": saved.Latitude,
			"longitude": saved.Longitude, "altitude_m": saved.AltitudeM,
			"timezone": saved.Timezone, "active_rf_band": string(saved.ActiveRFBand),
		})

	writeJSON(s.logger, w, http.StatusOK, map[string]any{
		"station_id": saved.ID.String(), "name": saved.Name,
		"latitude_degrees": saved.Latitude, "longitude_degrees": saved.Longitude,
		"altitude_m": saved.AltitudeM, "timezone": saved.Timezone,
		"active_rf_band": string(saved.ActiveRFBand),
		// Not a refusal. The operator is told what is already committed under
		// the previous settings; nothing was rewritten.
		"upcoming_passes_unchanged": upcoming,
	})
}

type updateBandRequest struct {
	AntennaDescription *string  `json:"antenna_description"`
	CenterFrequencyHz  *int64   `json:"center_frequency_hz"`
	SampleRateHz       *int32   `json:"sample_rate_hz"`
	GainDB             *float64 `json:"gain_db"`
	PPMCorrection      *int32   `json:"ppm_correction"`
	BiasTeeEnabled     *bool    `json:"bias_tee_enabled"`
}

// handleUpdateBandConfig changes the antenna and radio settings for one band.
func (s *Server) handleUpdateBandConfig(w http.ResponseWriter, r *http.Request) {
	band := r.PathValue("band")
	if !isBand(band) {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
			"band must be vhf or uhf")
		return
	}

	var request updateBandRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	station, ok := s.primaryStation(w, r)
	if !ok {
		return
	}

	existing, err := s.repo.GetStationBandConfigs(r.Context(), station.ID)
	if err != nil {
		s.internalError(w, "load band configs", err)
		return
	}
	current := domain.StationBandConfig{StationID: station.ID, Band: domain.RFBand(band)}
	for _, entry := range existing {
		if string(entry.Band) == band {
			current = entry
		}
	}
	previous := current

	if request.AntennaDescription != nil {
		current.AntennaDescription = strings.TrimSpace(*request.AntennaDescription)
	}
	if request.CenterFrequencyHz != nil {
		if *request.CenterFrequencyHz <= 0 {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
				"center_frequency_hz must be positive")
			return
		}
		current.CenterFrequencyHz = request.CenterFrequencyHz
	}
	if request.SampleRateHz != nil {
		if *request.SampleRateHz <= 0 {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
				"sample_rate_hz must be positive")
			return
		}
		current.SampleRateHz = request.SampleRateHz
	}
	if request.GainDB != nil {
		current.GainDB = request.GainDB
	}
	if request.PPMCorrection != nil {
		current.PPMCorrection = *request.PPMCorrection
	}
	if request.BiasTeeEnabled != nil {
		current.BiasTeeEnabled = *request.BiasTeeEnabled
	}

	if err := s.repo.UpsertStationBandConfig(r.Context(), current); err != nil {
		s.writeDomainError(w, "update band config", err)
		return
	}

	s.auditChange(r, "station.band_updated", "station", station.ID.String(),
		map[string]any{"band": band, "antenna": previous.AntennaDescription},
		map[string]any{"band": band, "antenna": current.AntennaDescription})

	writeJSON(s.logger, w, http.StatusOK, map[string]any{
		"band": bandConfigView{
			Band: band, AntennaDescription: current.AntennaDescription,
			CenterFrequencyHz: current.CenterFrequencyHz,
			SampleRateHz:      current.SampleRateHz, GainDB: current.GainDB,
			PPMCorrection: current.PPMCorrection, BiasTeeEnabled: current.BiasTeeEnabled,
		},
	})
}

type updateHardwareRequest struct {
	RotatorSerialPort           *string `json:"rotator_serial_port"`
	RotatorBaudRate             *int32  `json:"rotator_baud_rate"`
	RotatorParkAzimuthDegrees   *int32  `json:"rotator_park_azimuth_degrees"`
	RotatorParkElevationDegrees *int32  `json:"rotator_park_elevation_degrees"`
	SDRDeviceIdentifier         *string `json:"sdr_device_identifier"`
}

// handleUpdateHardwareConfig changes the rotator and SDR settings.
//
// Park angles are validated against the G-550's real limits before they are
// stored, because an out-of-range angle would be sent to the rotator
// (spec.md section 9).
func (s *Server) handleUpdateHardwareConfig(w http.ResponseWriter, r *http.Request) {
	var request updateHardwareRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	station, ok := s.primaryStation(w, r)
	if !ok {
		return
	}

	current, err := s.repo.GetStationHardwareConfig(r.Context(), station.ID)
	if err != nil {
		s.internalError(w, "load hardware config", err)
		return
	}
	previous := current

	if request.RotatorSerialPort != nil {
		port := strings.TrimSpace(*request.RotatorSerialPort)
		if port == "" {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
				"rotator_serial_port cannot be empty")
			return
		}
		current.RotatorSerialPort = port
	}
	if request.RotatorBaudRate != nil {
		if *request.RotatorBaudRate <= 0 {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
				"rotator_baud_rate must be positive")
			return
		}
		current.RotatorBaudRate = *request.RotatorBaudRate
	}
	if request.RotatorParkAzimuthDegrees != nil {
		if *request.RotatorParkAzimuthDegrees < 0 || *request.RotatorParkAzimuthDegrees > 359 {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
				"rotator_park_azimuth_degrees must be between 0 and 359")
			return
		}
		current.RotatorParkAzimuthDegrees = *request.RotatorParkAzimuthDegrees
	}
	if request.RotatorParkElevationDegrees != nil {
		if *request.RotatorParkElevationDegrees < 0 || *request.RotatorParkElevationDegrees > 90 {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
				"rotator_park_elevation_degrees must be between 0 and 90")
			return
		}
		current.RotatorParkElevationDegrees = *request.RotatorParkElevationDegrees
	}
	if request.SDRDeviceIdentifier != nil {
		identifier := strings.TrimSpace(*request.SDRDeviceIdentifier)
		if identifier == "" {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
				"sdr_device_identifier cannot be empty")
			return
		}
		current.SDRDeviceIdentifier = identifier
	}

	if err := s.repo.UpsertStationHardwareConfig(r.Context(), current); err != nil {
		s.writeDomainError(w, "update hardware config", err)
		return
	}
	upcoming, err := s.upcomingPassWarning(r)
	if err != nil {
		s.internalError(w, "count upcoming passes", err)
		return
	}

	// The serial port and device identifier are configuration, not secrets,
	// but the audit record keeps them for exactly the same reason the change
	// is audited at all: someone will need to know what changed and when.
	s.auditChange(r, "station.hardware_updated", "station", station.ID.String(),
		map[string]any{
			"rotator_serial_port": previous.RotatorSerialPort,
			"rotator_baud_rate":   previous.RotatorBaudRate,
			"park_azimuth":        previous.RotatorParkAzimuthDegrees,
			"park_elevation":      previous.RotatorParkElevationDegrees,
			"sdr_device":          previous.SDRDeviceIdentifier,
		},
		map[string]any{
			"rotator_serial_port": current.RotatorSerialPort,
			"rotator_baud_rate":   current.RotatorBaudRate,
			"park_azimuth":        current.RotatorParkAzimuthDegrees,
			"park_elevation":      current.RotatorParkElevationDegrees,
			"sdr_device":          current.SDRDeviceIdentifier,
		})

	writeJSON(s.logger, w, http.StatusOK, map[string]any{
		"hardware": hardwareConfigView{
			RotatorSerialPort:           current.RotatorSerialPort,
			RotatorBaudRate:             current.RotatorBaudRate,
			RotatorParkAzimuthDegrees:   current.RotatorParkAzimuthDegrees,
			RotatorParkElevationDegrees: current.RotatorParkElevationDegrees,
			SDRDeviceIdentifier:         current.SDRDeviceIdentifier,
		},
		"upcoming_passes_unchanged": upcoming,
	})
}

const auditListLimit = 200

type auditView struct {
	ID          int64     `json:"id"`
	Action      string    `json:"action"`
	EntityType  string    `json:"entity_type"`
	EntityID    string    `json:"entity_id"`
	ActorUserID string    `json:"actor_user_id,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// handleListAudit returns the audit trail, Root only.
//
// The recorded state is deliberately not returned: it is written for forensic
// reading at the database, and some of it names configuration an operator
// should not be able to browse casually over HTTP. What, who, which subject
// and when is what an administrator needs.
func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	limit := auditListLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > auditListLimit {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
				"limit must be between 1 and "+strconv.Itoa(auditListLimit))
			return
		}
		limit = parsed
	}

	records, err := s.repo.ListAuditRecords(r.Context(), store.AuditFilter{
		Action:     r.URL.Query().Get("action"),
		EntityType: r.URL.Query().Get("entity_type"),
		EntityID:   r.URL.Query().Get("entity_id"),
		Limit:      limit,
	})
	if err != nil {
		s.internalError(w, "list audit records", err)
		return
	}

	views := make([]auditView, 0, len(records))
	for _, record := range records {
		view := auditView{
			ID: record.ID, Action: record.Action, EntityType: record.EntityType,
			EntityID: record.EntityID, Reason: record.Reason, CreatedAt: record.CreatedAt,
		}
		if record.ActorUserID != nil {
			view.ActorUserID = record.ActorUserID.String()
		}
		views = append(views, view)
	}
	writeJSON(s.logger, w, http.StatusOK, map[string]any{
		"records": views, "limit": limit,
	})
}
