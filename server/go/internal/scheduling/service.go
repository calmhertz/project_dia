package scheduling

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"aagasa/internal/auth"
	"aagasa/internal/domain"
	workerv1 "aagasa/internal/gen/worker/v1"
	"aagasa/internal/prediction"
	"aagasa/internal/store"
)

// matchTolerance is how far a requested AOS may sit from a predicted one and
// still be treated as the same pass. The client picks a pass from a listing
// that may be seconds stale.
const matchTolerance = 2 * time.Minute

// searchMargin brackets the requested AOS when re-predicting.
const searchMargin = 3 * time.Hour

// trackStep is the pointing timeline spacing stored with an approved pass.
const trackStep = 5 * time.Second

// PlanGenerator turns an approved pass into an executable plan. A pass that
// cannot be turned into a plan must not stay approved, because the Worker
// would have nothing to execute.
type PlanGenerator interface {
	GenerateAndStore(ctx context.Context, pass domain.Pass) (*workerv1.PassPlan, bool, error)
}

// Predictor is the pass prediction dependency.
type Predictor interface {
	PredictPasses(ctx context.Context, request prediction.Request) (prediction.Result, error)
}

// Service creates and decides passes. It is the only authority on scheduling.
type Service struct {
	repo      *store.Repository
	predictor Predictor
	plans     PlanGenerator
	logger    *slog.Logger
	now       func() time.Time
}

// NewService builds the scheduler. plans may be nil, in which case approval
// does not generate an executable plan; that is only useful in tests of the
// scheduling rules themselves.
func NewService(repo *store.Repository, predictor Predictor, plans PlanGenerator, logger *slog.Logger) *Service {
	return &Service{repo: repo, predictor: predictor, plans: plans, logger: logger, now: func() time.Time {
		return time.Now().UTC()
	}}
}

// RequestInput is a user's pass request.
//
// The client supplies which pass it wants by start time; the Server
// re-predicts and uses its own numbers, so client-supplied timings are never
// trusted.
type RequestInput struct {
	SatelliteID uuid.UUID
	// RequestedAOS identifies the pass the user picked.
	RequestedAOS time.Time

	Band          domain.RFBand
	RecordingMode domain.RecordingMode
	Visibility    domain.PassVisibility
	PipelineID    *uuid.UUID
	RadioSettings map[string]any
}

// Result is the outcome of a scheduling request.
type Result struct {
	Pass domain.Pass
	// Warnings are advisory and never block the request.
	Warnings []Warning
	// OverriddenPassIDs lists passes a Root override cancelled.
	OverriddenPassIDs []uuid.UUID
}

// ConflictError reports a rejected overlapping request.
//
// It carries only sanitized detail: the occupied window, never the owner
// (spec.md section 15).
type ConflictError struct {
	OccupiedFrom time.Time
	OccupiedTo   time.Time
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s: %s to %s", ErrStationConflict,
		e.OccupiedFrom.UTC().Format(time.RFC3339), e.OccupiedTo.UTC().Format(time.RFC3339))
}

func (e *ConflictError) Unwrap() error { return ErrStationConflict }

// RequestPass creates a pass request for the actor.
//
// Order matters: validate the satellite, resolve the real pass from the
// predictor, apply the lead-time rule, then let the database enforce the
// overlap rule as it inserts (server-spec section 11).
func (s *Service) RequestPass(ctx context.Context, actor domain.User, input RequestInput) (Result, error) {
	satellite, err := s.repo.GetSatelliteByID(ctx, input.SatelliteID)
	if err != nil {
		return Result{}, err
	}
	if !satellite.IsSchedulable {
		return Result{}, ErrNotSchedulable
	}
	if input.Band != domain.BandVHF && input.Band != domain.BandUHF {
		return Result{}, fmt.Errorf("band must be vhf or uhf")
	}

	station, config, err := s.stationAndConfig(ctx)
	if err != nil {
		return Result{}, err
	}

	// spec.md section 6.3: no new scheduling while the station cannot execute.
	if err := s.requireWorkerOnline(ctx, station.ID); err != nil {
		return Result{}, err
	}

	tleRecord, err := s.repo.LatestTLERecord(ctx, satellite.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Result{}, fmt.Errorf("satellite %d has no orbital data", satellite.NoradID)
		}
		return Result{}, err
	}

	predicted, err := s.resolvePass(ctx, station, config, tleRecord, input.RequestedAOS)
	if err != nil {
		return Result{}, err
	}

	now := s.now()
	// The lead-time rule binds every role, Root included.
	if err := CheckLeadTime(now, predicted.AOS, config); err != nil {
		return Result{}, err
	}

	reservation := ReservationFor(predicted.AOS, predicted.LOS, config)

	var warnings []Warning
	if warning := BandMismatchWarning(input.Band, station.ActiveRFBand); warning != nil {
		warnings = append(warnings, *warning)
	}

	visibility := input.Visibility
	if visibility == "" {
		visibility = domain.VisibilityPrivate
	}
	recordingMode := input.RecordingMode
	if recordingMode == "" {
		recordingMode = domain.RecordRaw
	}
	if needsPipeline(recordingMode) && input.PipelineID == nil {
		return Result{}, ErrPipelineRequired
	}

	pass := domain.Pass{
		StationID: station.ID, SatelliteID: satellite.ID, RequestedBy: actor.ID,
		TLERecordID: tleRecord.ID, SchedulingConfigID: config.ID,
		Status: domain.PassPendingApproval, Visibility: visibility, Band: input.Band,
		AOSAt: predicted.AOS, LOSAt: predicted.LOS, TCAAt: &predicted.TCA,
		MaxElevationDegrees: predicted.MaxElevationDegrees,
		ReservedFrom:        reservation.From, ReservedTo: reservation.To,
		RecordingMode:     recordingMode,
		RecordingPreRoll:  config.RecordingPreRoll,
		RecordingPostRoll: config.RecordingPostRoll,
		PipelineID:        input.PipelineID,
		RadioSettings:     input.RadioSettings,
	}

	created, err := s.repo.CreatePass(ctx, pass)
	if err != nil {
		if errors.Is(err, store.ErrStationOverlap) {
			return Result{}, s.conflictFor(ctx, station.ID, reservation)
		}
		return Result{}, err
	}

	s.audit(ctx, &actor.ID, "pass.requested", created.ID, map[string]any{
		"satellite_norad_id": satellite.NoradID,
		"aos":                created.AOSAt,
		"band":               string(created.Band),
		"visibility":         string(created.Visibility),
	})

	return Result{Pass: created, Warnings: warnings}, nil
}

// RequestPassWithRootOverride creates a pass for Root, cancelling any live
// passes in the way.
//
// spec.md section 13.7: the displaced passes are not deleted. They are marked
// cancelled by root override and stay auditable, and the whole thing happens
// in one transaction so the station is never left double-booked or empty.
func (s *Service) RequestPassWithRootOverride(ctx context.Context, actor domain.User, input RequestInput) (Result, error) {
	if actor.Role != domain.RoleRoot {
		return Result{}, ErrForbidden
	}

	satellite, err := s.repo.GetSatelliteByID(ctx, input.SatelliteID)
	if err != nil {
		return Result{}, err
	}
	if !satellite.IsSchedulable {
		return Result{}, ErrNotSchedulable
	}

	station, config, err := s.stationAndConfig(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := s.requireWorkerOnline(ctx, station.ID); err != nil {
		return Result{}, err
	}

	tleRecord, err := s.repo.LatestTLERecord(ctx, satellite.ID)
	if err != nil {
		return Result{}, err
	}

	predicted, err := s.resolvePass(ctx, station, config, tleRecord, input.RequestedAOS)
	if err != nil {
		return Result{}, err
	}

	now := s.now()
	// Root overrides conflicts, not the lead-time rule (global test property 4).
	if err := CheckLeadTime(now, predicted.AOS, config); err != nil {
		return Result{}, err
	}

	reservation := ReservationFor(predicted.AOS, predicted.LOS, config)

	var warnings []Warning
	if warning := BandMismatchWarning(input.Band, station.ActiveRFBand); warning != nil {
		warnings = append(warnings, *warning)
	}

	visibility := input.Visibility
	if visibility == "" {
		visibility = domain.VisibilityPrivate
	}
	recordingMode := input.RecordingMode
	if recordingMode == "" {
		recordingMode = domain.RecordRaw
	}
	// Root override bypasses conflicts, not the rules that make a pass
	// executable.
	if needsPipeline(recordingMode) && input.PipelineID == nil {
		return Result{}, ErrPipelineRequired
	}

	var result Result
	err = s.repo.InTx(ctx, func(tx *store.Repository) error {
		conflicts, err := tx.FindConflictingPasses(ctx, station.ID,
			reservation.From, reservation.To, LiveStatuses())
		if err != nil {
			return err
		}

		for _, conflicting := range conflicts {
			if err := tx.CancelPass(ctx, conflicting.ID, actor.ID,
				domain.CancelledByRootOverride, now); err != nil {
				return fmt.Errorf("cancel conflicting pass: %w", err)
			}
			result.OverriddenPassIDs = append(result.OverriddenPassIDs, conflicting.ID)

			// Audit inside the transaction: if the insert fails, the
			// cancellations roll back and so must their records.
			if _, err := tx.AppendAudit(ctx, domain.AuditRecord{
				ActorUserID: &actor.ID,
				Action:      "pass.cancelled_by_root_override",
				EntityType:  "pass", EntityID: conflicting.ID.String(),
				PreviousState: map[string]any{"status": string(conflicting.Status)},
				NewState:      map[string]any{"status": string(domain.PassCancelled)},
				Reason:        "root override",
			}); err != nil {
				return err
			}
		}

		created, err := tx.CreatePass(ctx, domain.Pass{
			StationID: station.ID, SatelliteID: satellite.ID, RequestedBy: actor.ID,
			TLERecordID: tleRecord.ID, SchedulingConfigID: config.ID,
			// Created pending and approved below, so a Root pass becomes
			// approved through the same path as any other and records who
			// approved it.
			Status: domain.PassPendingApproval, Visibility: visibility, Band: input.Band,
			AOSAt: predicted.AOS, LOSAt: predicted.LOS, TCAAt: &predicted.TCA,
			MaxElevationDegrees: predicted.MaxElevationDegrees,
			ReservedFrom:        reservation.From, ReservedTo: reservation.To,
			RecordingMode:     recordingMode,
			RecordingPreRoll:  config.RecordingPreRoll,
			RecordingPostRoll: config.RecordingPostRoll,
			PipelineID:        input.PipelineID,
			RadioSettings:     input.RadioSettings,
		})
		if err != nil {
			return err
		}

		if err := tx.ApprovePass(ctx, created.ID, actor.ID, now); err != nil {
			return err
		}
		created.Status = domain.PassApproved
		created.ApprovedBy = &actor.ID
		created.ApprovedAt = &now
		result.Pass = created

		_, err = tx.AppendAudit(ctx, domain.AuditRecord{
			ActorUserID: &actor.ID, Action: "pass.root_override_created",
			EntityType: "pass", EntityID: created.ID.String(),
			NewState: map[string]any{
				"overridden_pass_count": len(result.OverriddenPassIDs),
				"aos":                   created.AOSAt,
			},
		})
		return err
	})
	if err != nil {
		return Result{}, err
	}

	if err := s.generatePlan(ctx, result.Pass); err != nil {
		return Result{}, err
	}

	result.Warnings = warnings
	s.logger.Info("root override scheduled a pass",
		slog.Int("overridden", len(result.OverriddenPassIDs)))
	return result, nil
}

// Approve moves a pending pass to approved.
func (s *Service) Approve(ctx context.Context, actor domain.User, passID uuid.UUID) (domain.Pass, error) {
	return s.decide(ctx, actor, passID, domain.PassApproved)
}

// Reject refuses a pending pass.
func (s *Service) Reject(ctx context.Context, actor domain.User, passID uuid.UUID) (domain.Pass, error) {
	return s.decide(ctx, actor, passID, domain.PassRejected)
}

func (s *Service) decide(ctx context.Context, actor domain.User, passID uuid.UUID,
	target domain.PassStatus) (domain.Pass, error) {
	if !auth.CanApprovePasses(actor) {
		return domain.Pass{}, ErrForbidden
	}

	pass, err := s.repo.GetPassByID(ctx, passID)
	if err != nil {
		return domain.Pass{}, err
	}
	if err := ValidateTransition(pass.Status, target); err != nil {
		return domain.Pass{}, err
	}

	// The plan is built before the approval, not after. Approving first and
	// then failing to build a plan would leave a pass that the Worker cannot
	// execute and that can no longer be rejected, which is exactly what the
	// PlanGenerator contract above forbids. A plan stored for a pass that is
	// not approved is invisible to the Worker, so the failed order is safe.
	if target == domain.PassApproved {
		approved := pass
		approved.Status = domain.PassApproved
		if err := s.generatePlan(ctx, approved); err != nil {
			return domain.Pass{}, err
		}
	}

	now := s.now()
	if target == domain.PassApproved {
		err = s.repo.ApprovePass(ctx, passID, actor.ID, now)
	} else {
		err = s.repo.RejectPass(ctx, passID, actor.ID, now)
	}
	if err != nil {
		return domain.Pass{}, err
	}

	s.audit(ctx, &actor.ID, "pass."+string(target), passID, map[string]any{
		"previous_status": string(pass.Status),
	})

	return s.repo.GetPassByID(ctx, passID)
}

// Cancel withdraws a pass. Owners cancel their own; Admin and Root cancel any.
func (s *Service) Cancel(ctx context.Context, actor domain.User, passID uuid.UUID) (domain.Pass, error) {
	pass, err := s.repo.GetPassByID(ctx, passID)
	if err != nil {
		return domain.Pass{}, err
	}
	if !CanCancelPass(actor, pass) {
		return domain.Pass{}, ErrForbidden
	}
	if err := ValidateTransition(pass.Status, domain.PassCancelled); err != nil {
		return domain.Pass{}, err
	}

	reason := CancellationReasonFor(actor, pass)
	if err := s.repo.CancelPass(ctx, passID, actor.ID, reason, s.now()); err != nil {
		return domain.Pass{}, err
	}

	s.audit(ctx, &actor.ID, "pass.cancelled", passID, map[string]any{
		"previous_status": string(pass.Status),
		"reason":          string(reason),
	})
	return s.repo.GetPassByID(ctx, passID)
}

// UpdateSchedulingConfig saves a new configuration version.
//
// spec.md section 13.6: the new values become usable only after the newly
// chosen lead time has elapsed, so shortening the lead time cannot be used to
// book something sooner than the old rule allowed. Existing approved passes
// keep the configuration they were accepted under (section 13.8).
func (s *Service) UpdateSchedulingConfig(ctx context.Context, actor domain.User,
	config domain.SchedulingConfig) (domain.SchedulingConfig, error) {
	if !auth.CanConfigureStation(actor) {
		return domain.SchedulingConfig{}, ErrForbidden
	}

	station, current, err := s.stationAndConfig(ctx)
	if err != nil {
		return domain.SchedulingConfig{}, err
	}

	now := s.now()
	config.StationID = station.ID
	config.CreatedBy = &actor.ID
	config.EffectiveFrom = EffectiveFromForNewConfig(
		now, current.MinimumLeadTime, config.MinimumLeadTime)

	saved, err := s.repo.CreateSchedulingConfig(ctx, config)
	if err != nil {
		return domain.SchedulingConfig{}, err
	}

	s.audit(ctx, &actor.ID, "scheduling_config.updated", saved.ID, map[string]any{
		"minimum_lead_time_seconds": config.MinimumLeadTime.Seconds(),
		"effective_from":            saved.EffectiveFrom,
	})
	return saved, nil
}

// resolvePass re-predicts and matches the pass the user asked for.
//
// The Server never stores client-supplied pass timings: it computes them.
func (s *Service) resolvePass(ctx context.Context, station domain.Station,
	config domain.SchedulingConfig, tleRecord domain.TLERecord,
	requestedAOS time.Time) (prediction.Pass, error) {
	result, err := s.predictor.PredictPasses(ctx, prediction.Request{
		Line1:                   tleRecord.Line1,
		Line2:                   tleRecord.Line2,
		LatitudeDegrees:         station.Latitude,
		LongitudeDegrees:        station.Longitude,
		AltitudeM:               station.AltitudeM,
		MinimumElevationDegrees: config.MinimumElevationDegrees,
		SearchStart:             requestedAOS.Add(-searchMargin),
		SearchEnd:               requestedAOS.Add(searchMargin),
		TrackStep:               trackStep,
	})
	if err != nil {
		return prediction.Pass{}, fmt.Errorf("predict pass: %w", err)
	}

	for _, candidate := range result.Passes {
		difference := candidate.AOS.Sub(requestedAOS)
		if difference < 0 {
			difference = -difference
		}
		if difference <= matchTolerance {
			return candidate, nil
		}
	}
	return prediction.Pass{}, ErrPassNotFound
}

// conflictFor builds a sanitized conflict error from the passes in the way.
func (s *Service) conflictFor(ctx context.Context, stationID uuid.UUID, reservation Reservation) error {
	conflicts, err := s.repo.FindConflictingPasses(ctx, stationID,
		reservation.From, reservation.To, LiveStatuses())
	if err != nil || len(conflicts) == 0 {
		// The row vanished between the failed insert and this read; report the
		// conflict against the requested window rather than inventing detail.
		return &ConflictError{OccupiedFrom: reservation.From, OccupiedTo: reservation.To}
	}

	occupiedFrom, occupiedTo := conflicts[0].ReservedFrom, conflicts[0].ReservedTo
	for _, conflicting := range conflicts[1:] {
		if conflicting.ReservedFrom.Before(occupiedFrom) {
			occupiedFrom = conflicting.ReservedFrom
		}
		if conflicting.ReservedTo.After(occupiedTo) {
			occupiedTo = conflicting.ReservedTo
		}
	}
	return &ConflictError{OccupiedFrom: occupiedFrom, OccupiedTo: occupiedTo}
}

// stationAndConfig loads the station and its currently effective configuration.
func (s *Service) stationAndConfig(ctx context.Context) (domain.Station, domain.SchedulingConfig, error) {
	stations, err := s.repo.ListStations(ctx)
	if err != nil {
		return domain.Station{}, domain.SchedulingConfig{}, err
	}
	if len(stations) == 0 {
		return domain.Station{}, domain.SchedulingConfig{},
			errors.New("no station is configured; complete first-run setup")
	}
	station := stations[0]

	config, err := s.repo.LatestEffectiveSchedulingConfig(ctx, station.ID, s.now())
	if err != nil {
		return domain.Station{}, domain.SchedulingConfig{}, err
	}
	return station, config, nil
}

func (s *Service) requireWorkerOnline(ctx context.Context, stationID uuid.UUID) error {
	worker, err := s.repo.GetStationWorker(ctx, stationID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrWorkerOffline
		}
		return err
	}
	if worker.ConnectionState != domain.WorkerOnline {
		return ErrWorkerOffline
	}
	return nil
}

// generatePlan builds the executable plan for a newly approved pass.
//
// A failure here is fatal to the approval: leaving a pass approved with no
// plan would mean the Worker silently never runs it.
func (s *Service) generatePlan(ctx context.Context, pass domain.Pass) error {
	if s.plans == nil {
		return nil
	}
	plan, changed, err := s.plans.GenerateAndStore(ctx, pass)
	if err != nil {
		return fmt.Errorf("generate pass plan: %w", err)
	}
	s.logger.Info("pass plan generated",
		slog.String("pass_id", pass.ID.String()),
		slog.String("generation", plan.GetGeneration()),
		slog.Bool("changed", changed))
	return nil
}

func (s *Service) audit(ctx context.Context, actor *uuid.UUID, action string,
	passID uuid.UUID, details map[string]any) {
	if _, err := s.repo.AppendAudit(ctx, domain.AuditRecord{
		ActorUserID: actor, Action: action, EntityType: "pass",
		EntityID: passID.String(), NewState: details,
	}); err != nil {
		s.logger.Error("audit write failed",
			slog.String("action", action), slog.String("error", err.Error()))
	}
}

// needsPipeline reports whether a mode produces a decoded result, which
// cannot be done without knowing which pipeline to run.
func needsPipeline(mode domain.RecordingMode) bool {
	return mode == domain.RecordProcess || mode == domain.RecordRawAndProcess
}
