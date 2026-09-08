package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"aagasa/internal/auth"
	"aagasa/internal/domain"
	"aagasa/internal/passplan"
	"aagasa/internal/scheduling"
	"aagasa/internal/store"
)

const passListLimit = 200

// passView is a pass as shown to a caller allowed to see it.
//
// RequestedBy is omitted unless the caller is the owner, an Admin or Root, so
// a public pass does not disclose who booked it (spec.md section 16).
type passView struct {
	ID          string `json:"id"`
	SatelliteID string `json:"satellite_id"`
	Status      string `json:"status"`
	Visibility  string `json:"visibility"`
	Band        string `json:"band"`

	AOS time.Time  `json:"aos"`
	TCA *time.Time `json:"tca,omitempty"`
	LOS time.Time  `json:"los"`

	MaxElevationDegrees float64 `json:"max_elevation_degrees"`

	ReservedFrom time.Time `json:"reserved_from"`
	ReservedTo   time.Time `json:"reserved_to"`

	RecordingMode            string `json:"recording_mode"`
	RecordingPreRollSeconds  int64  `json:"recording_pre_roll_seconds"`
	RecordingPostRollSeconds int64  `json:"recording_post_roll_seconds"`

	RequestedBy        string     `json:"requested_by,omitempty"`
	ApprovedAt         *time.Time `json:"approved_at,omitempty"`
	CancellationReason string     `json:"cancellation_reason,omitempty"`
	CancelledAt        *time.Time `json:"cancelled_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
}

func toPassView(pass domain.Pass, actor domain.User) passView {
	view := passView{
		ID: pass.ID.String(), SatelliteID: pass.SatelliteID.String(),
		Status: string(pass.Status), Visibility: string(pass.Visibility),
		Band: string(pass.Band),
		AOS:  pass.AOSAt, TCA: pass.TCAAt, LOS: pass.LOSAt,
		MaxElevationDegrees: pass.MaxElevationDegrees,
		ReservedFrom:        pass.ReservedFrom, ReservedTo: pass.ReservedTo,
		RecordingMode:            string(pass.RecordingMode),
		RecordingPreRollSeconds:  int64(pass.RecordingPreRoll.Seconds()),
		RecordingPostRollSeconds: int64(pass.RecordingPostRoll.Seconds()),
		ApprovedAt:               pass.ApprovedAt,
		CancelledAt:              pass.CancelledAt,
		CreatedAt:                pass.CreatedAt,
	}
	if pass.CancellationReason != nil {
		view.CancellationReason = string(*pass.CancellationReason)
	}
	// Owner identity is privileged information.
	if pass.RequestedBy == actor.ID || auth.AtLeast(actor, domain.RoleAdmin) {
		view.RequestedBy = pass.RequestedBy.String()
	}
	return view
}

type requestPassRequest struct {
	SatelliteID   string         `json:"satellite_id"`
	AOS           time.Time      `json:"aos"`
	Band          string         `json:"band"`
	RecordingMode string         `json:"recording_mode"`
	Visibility    string         `json:"visibility"`
	PipelineID    string         `json:"pipeline_id"`
	RadioSettings map[string]any `json:"radio_settings"`
	// Override asks for a Root override of any conflicting pass.
	Override bool `json:"override"`
}

func (s *Server) handleRequestPass(w http.ResponseWriter, r *http.Request) {
	var request requestPassRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	satelliteID, err := uuid.Parse(request.SatelliteID)
	if err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "invalid satellite_id")
		return
	}
	if request.AOS.IsZero() {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "aos is required")
		return
	}

	input := scheduling.RequestInput{
		SatelliteID:   satelliteID,
		RequestedAOS:  request.AOS.UTC(),
		Band:          domain.RFBand(request.Band),
		RecordingMode: domain.RecordingMode(request.RecordingMode),
		Visibility:    domain.PassVisibility(request.Visibility),
		RadioSettings: request.RadioSettings,
	}
	if request.PipelineID != "" {
		pipelineID, err := uuid.Parse(request.PipelineID)
		if err != nil {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "invalid pipeline_id")
			return
		}
		input.PipelineID = &pipelineID
	}

	actor := currentUser(r)
	var result scheduling.Result
	if request.Override {
		result, err = s.scheduling.RequestPassWithRootOverride(r.Context(), actor, input)
	} else {
		result, err = s.scheduling.RequestPass(r.Context(), actor, input)
	}
	if err != nil {
		s.writeSchedulingError(w, err)
		return
	}

	response := map[string]any{
		"pass":     toPassView(result.Pass, actor),
		"warnings": result.Warnings,
	}
	if len(result.OverriddenPassIDs) > 0 {
		overridden := make([]string, 0, len(result.OverriddenPassIDs))
		for _, id := range result.OverriddenPassIDs {
			overridden = append(overridden, id.String())
		}
		response["overridden_pass_ids"] = overridden
	}
	writeJSON(s.logger, w, http.StatusCreated, response)
}

// handleListPasses returns the caller's own passes by default.
func (s *Server) handleListPasses(w http.ResponseWriter, r *http.Request) {
	actor := currentUser(r)
	filter := store.PassFilter{Limit: passListLimit}

	switch r.URL.Query().Get("scope") {
	case "", "mine":
		filter.RequestedBy = &actor.ID
	case "public":
		filter.OnlyPublic = true
	case "all":
		// Only Admin and Root may see everything.
		if !auth.AtLeast(actor, domain.RoleAdmin) {
			writeError(s.logger, w, http.StatusForbidden, "forbidden", "not permitted")
			return
		}
	default:
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "scope must be mine, public or all")
		return
	}

	if status := r.URL.Query().Get("status"); status != "" {
		filter.Statuses = []domain.PassStatus{domain.PassStatus(status)}
	}

	passes, err := s.repo.ListPasses(r.Context(), filter)
	if err != nil {
		s.internalError(w, "list passes", err)
		return
	}

	views := make([]passView, 0, len(passes))
	for _, pass := range passes {
		// Defence in depth: filter again on the way out, so a query mistake
		// cannot disclose a private pass.
		if !scheduling.CanViewPass(actor, pass) {
			continue
		}
		views = append(views, toPassView(pass, actor))
	}
	writeJSON(s.logger, w, http.StatusOK, map[string]any{"passes": views})
}

func (s *Server) handleGetPass(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathUUID(w, r)
	if !ok {
		return
	}
	pass, err := s.repo.GetPassByID(r.Context(), id)
	if err != nil {
		s.writeDomainError(w, "get pass", err)
		return
	}

	actor := currentUser(r)
	if !scheduling.CanViewPass(actor, pass) {
		// Not "forbidden": revealing that the pass exists would itself leak
		// information about another user's private booking.
		writeError(s.logger, w, http.StatusNotFound, "not_found", "not found")
		return
	}
	writeJSON(s.logger, w, http.StatusOK, map[string]any{"pass": toPassView(pass, actor)})
}

func (s *Server) handleApprovePass(w http.ResponseWriter, r *http.Request) {
	s.decidePass(w, r, true)
}

func (s *Server) handleRejectPass(w http.ResponseWriter, r *http.Request) {
	s.decidePass(w, r, false)
}

func (s *Server) decidePass(w http.ResponseWriter, r *http.Request, approve bool) {
	id, ok := s.pathUUID(w, r)
	if !ok {
		return
	}
	actor := currentUser(r)

	var pass domain.Pass
	var err error
	if approve {
		pass, err = s.scheduling.Approve(r.Context(), actor, id)
	} else {
		pass, err = s.scheduling.Reject(r.Context(), actor, id)
	}
	if err != nil {
		s.writeSchedulingError(w, err)
		return
	}
	writeJSON(s.logger, w, http.StatusOK, map[string]any{"pass": toPassView(pass, actor)})
}

func (s *Server) handleCancelPass(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathUUID(w, r)
	if !ok {
		return
	}
	actor := currentUser(r)

	pass, err := s.scheduling.Cancel(r.Context(), actor, id)
	if err != nil {
		s.writeSchedulingError(w, err)
		return
	}
	writeJSON(s.logger, w, http.StatusOK, map[string]any{"pass": toPassView(pass, actor)})
}

type schedulingConfigRequest struct {
	MinimumLeadTimeSeconds   int64   `json:"minimum_lead_time_seconds"`
	PrePassBufferSeconds     int64   `json:"pre_pass_buffer_seconds"`
	PostPassBufferSeconds    int64   `json:"post_pass_buffer_seconds"`
	RecordingPreRollSeconds  int64   `json:"recording_pre_roll_seconds"`
	RecordingPostRollSeconds int64   `json:"recording_post_roll_seconds"`
	MinimumElevationDegrees  float64 `json:"minimum_elevation_degrees"`
}

func (s *Server) handleUpdateSchedulingConfig(w http.ResponseWriter, r *http.Request) {
	var request schedulingConfigRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}
	for name, value := range map[string]int64{
		"minimum_lead_time_seconds":   request.MinimumLeadTimeSeconds,
		"pre_pass_buffer_seconds":     request.PrePassBufferSeconds,
		"post_pass_buffer_seconds":    request.PostPassBufferSeconds,
		"recording_pre_roll_seconds":  request.RecordingPreRollSeconds,
		"recording_post_roll_seconds": request.RecordingPostRollSeconds,
	} {
		if value < 0 {
			writeError(s.logger, w, http.StatusBadRequest, "invalid_request", name+" cannot be negative")
			return
		}
	}
	if request.MinimumElevationDegrees < 0 || request.MinimumElevationDegrees >= 90 {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
			"minimum_elevation_degrees must be at least 0 and below 90")
		return
	}

	saved, err := s.scheduling.UpdateSchedulingConfig(r.Context(), currentUser(r), domain.SchedulingConfig{
		MinimumLeadTime:         time.Duration(request.MinimumLeadTimeSeconds) * time.Second,
		PrePassBuffer:           time.Duration(request.PrePassBufferSeconds) * time.Second,
		PostPassBuffer:          time.Duration(request.PostPassBufferSeconds) * time.Second,
		RecordingPreRoll:        time.Duration(request.RecordingPreRollSeconds) * time.Second,
		RecordingPostRoll:       time.Duration(request.RecordingPostRollSeconds) * time.Second,
		MinimumElevationDegrees: request.MinimumElevationDegrees,
	})
	if err != nil {
		s.writeSchedulingError(w, err)
		return
	}

	writeJSON(s.logger, w, http.StatusCreated, map[string]any{
		"minimum_lead_time_seconds": int64(saved.MinimumLeadTime.Seconds()),
		// The new values are not usable until this moment
		// (spec.md section 13.6).
		"effective_from": saved.EffectiveFrom,
	})
}

// writeSchedulingError maps scheduler errors onto status codes.
func (s *Server) writeSchedulingError(w http.ResponseWriter, err error) {
	var conflict *scheduling.ConflictError
	switch {
	case errors.As(err, &conflict):
		// Sanitized: the occupied window only, never the other user
		// (spec.md section 15).
		writeJSON(s.logger, w, http.StatusConflict, map[string]any{
			"error":         "station_conflict",
			"message":       "This time is already occupied.",
			"occupied_from": conflict.OccupiedFrom,
			"occupied_to":   conflict.OccupiedTo,
		})
	case errors.Is(err, scheduling.ErrLeadTime):
		writeError(s.logger, w, http.StatusConflict, "lead_time", err.Error())
	case errors.Is(err, scheduling.ErrWorkerOffline):
		writeError(s.logger, w, http.StatusConflict, "worker_offline",
			"the station worker is offline, so new passes cannot be scheduled")
	case errors.Is(err, scheduling.ErrPipelineRequired):
		writeError(s.logger, w, http.StatusBadRequest, "pipeline_required", err.Error())
	case errors.Is(err, scheduling.ErrNotSchedulable):
		writeError(s.logger, w, http.StatusConflict, "not_schedulable", err.Error())
	case errors.Is(err, scheduling.ErrPassNotFound):
		writeError(s.logger, w, http.StatusNotFound, "pass_not_found",
			"no predicted pass matches that time")
	case errors.Is(err, passplan.ErrStationNotConfigured):
		// Actionable by Root, so it says what is missing rather than hiding
		// behind an internal error.
		writeError(s.logger, w, http.StatusConflict, "station_not_configured", err.Error())
	case errors.Is(err, scheduling.ErrInvalidTransition):
		// Someone else usually got there first.
		writeError(s.logger, w, http.StatusConflict, "invalid_state", err.Error())
	case errors.Is(err, scheduling.ErrForbidden):
		writeError(s.logger, w, http.StatusForbidden, "forbidden", "not permitted")
	case errors.Is(err, store.ErrNotFound):
		writeError(s.logger, w, http.StatusNotFound, "not_found", "not found")
	default:
		s.internalError(w, "scheduling", err)
	}
}

// handleGetPassPlan returns the executable plan generated for a pass.
//
// Operational detail for Admin and Root: it exposes station and worker
// identifiers and the full pointing timeline.
func (s *Server) handleGetPassPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := s.pathUUID(w, r)
	if !ok {
		return
	}

	stored, err := s.repo.GetPassPlan(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(s.logger, w, http.StatusNotFound, "no_plan",
				"this pass has no executable plan; only an approved pass does")
			return
		}
		s.internalError(w, "load pass plan", err)
		return
	}

	plan, err := passplan.Unmarshal(stored.Encoded)
	if err != nil {
		s.internalError(w, "decode pass plan", err)
		return
	}

	track := make([]map[string]any, 0, len(plan.GetTrack()))
	for _, point := range plan.GetTrack() {
		track = append(track, map[string]any{
			"at":                point.GetAt().AsTime(),
			"azimuth_degrees":   point.GetAzimuthDegrees(),
			"elevation_degrees": point.GetElevationDegrees(),
			"range_km":          point.GetRangeKm(),
		})
	}

	writeJSON(s.logger, w, http.StatusOK, map[string]any{
		"plan_version":    plan.GetPlanVersion(),
		"generation":      plan.GetGeneration(),
		"generated_at":    plan.GetGeneratedAt().AsTime(),
		"encoded_bytes":   len(stored.Encoded),
		"pass_id":         plan.GetPassId(),
		"station_id":      plan.GetStationId(),
		"worker_id":       plan.GetWorkerId(),
		"norad_id":        plan.GetNoradId(),
		"satellite_name":  plan.GetSatelliteName(),
		"tle_record_id":   plan.GetElements().GetTleRecordId(),
		"tle_epoch":       plan.GetElements().GetEpoch().AsTime(),
		"aos":             plan.GetAos().AsTime(),
		"tca":             plan.GetTca().AsTime(),
		"los":             plan.GetLos().AsTime(),
		"recording_start": plan.GetRecordingStart().AsTime(),
		"recording_end":   plan.GetRecordingEnd().AsTime(),
		"band":            plan.GetBand().String(),
		"recording_mode":  plan.GetRecordingMode().String(),
		"radio": map[string]any{
			"source":         plan.GetRadio().GetSource(),
			"frequency_hz":   plan.GetRadio().GetFrequencyHz(),
			"sample_rate_hz": plan.GetRadio().GetSampleRateHz(),
			"gain_db":        plan.GetRadio().GetGainDb(),
		},
		"pipeline":          plan.GetPipeline().GetIdentifier(),
		"track_point_count": len(track),
		"track":             track,
	})
}
