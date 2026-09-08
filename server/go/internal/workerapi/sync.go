package workerapi

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"aagasa/internal/domain"
	workerv1 "aagasa/internal/gen/worker/v1"
	"aagasa/internal/passplan"
	"aagasa/internal/store"
)

// HeartbeatInterval is how often a Worker is expected to report in, and
// StaleAfter is how long the Server waits before calling it offline
// (spec.md section 7).
const (
	HeartbeatInterval = 5 * time.Second
	StaleAfter        = 15 * time.Second
	// How often the Server looks for workers that have stopped reporting.
	StaleCheckInterval = 5 * time.Second
)

// Register resolves a Worker's identity from its configured name.
func (s *Service) Register(ctx context.Context,
	request *workerv1.RegisterRequest) (*workerv1.RegisterResponse, error) {
	name := request.GetWorkerName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "worker_name is required")
	}

	worker, err := s.repo.GetWorkerByName(ctx, name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// The Worker is created during first-run setup, so an unknown
			// name means the station has not been configured for it.
			return nil, status.Errorf(codes.NotFound,
				"no worker named %q is configured for this server", name)
		}
		s.logger.Error("look up worker", slog.String("error", err.Error()))
		return nil, status.Error(codes.Internal, "could not resolve the worker")
	}

	station, err := s.repo.GetStationByID(ctx, worker.StationID)
	if err != nil {
		s.logger.Error("load station", slog.String("error", err.Error()))
		return nil, status.Error(codes.Internal, "could not resolve the station")
	}

	// What this station's SatDump can actually do. The Server has none of its
	// own, so a registration is the only moment it learns (server-spec
	// section 21).
	if names := request.GetAvailablePipelines(); len(names) > 0 {
		if removed, err := s.repo.ReplaceStandardPipelines(ctx, names); err != nil {
			s.logger.Error("record available pipelines", slog.String("error", err.Error()))
		} else {
			s.logger.Info("pipeline inventory recorded",
				slog.Int("available", len(names)), slog.Int("withdrawn", removed))
		}
	}

	s.logger.Info("worker registered",
		slog.String("worker", name),
		slog.String("worker_id", worker.ID.String()),
		slog.String("station", station.Name))
	// A station binding its identity is privileged, so it leaves a trace.
	s.auditWorker(ctx, "worker.registered", worker.ID.String(), map[string]any{
		"worker": name, "station": station.Name,
		"worker_version": request.GetWorkerVersion(),
	})

	return &workerv1.RegisterResponse{
		WorkerId:    worker.ID.String(),
		StationId:   station.ID.String(),
		StationName: station.Name,
	}, nil
}

// Heartbeat records liveness and tells the Worker whether its state is current.
func (s *Service) Heartbeat(ctx context.Context,
	request *workerv1.HeartbeatRequest) (*workerv1.HeartbeatResponse, error) {
	workerID, err := uuid.Parse(request.GetWorkerId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid worker id")
	}

	worker, err := s.repo.GetWorkerByID(ctx, workerID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "unknown worker")
		}
		return nil, status.Error(codes.Internal, "could not load the worker")
	}

	now := time.Now().UTC()
	if err := s.repo.RecordHeartbeat(ctx, store.HeartbeatUpdate{
		WorkerID:       workerID,
		SeenAt:         now,
		WorkerVersion:  request.GetWorkerVersion(),
		PendingUploads: int(request.GetPendingUploads()),
		PendingReports: int(request.GetPendingReports()),
	}); err != nil {
		s.logger.Error("record heartbeat", slog.String("error", err.Error()))
		return nil, status.Error(codes.Internal, "could not record the heartbeat")
	}

	if worker.ConnectionState != domain.WorkerOnline {
		s.logger.Info("worker is online",
			slog.String("worker_id", workerID.String()),
			slog.Uint64("pending_uploads", uint64(request.GetPendingUploads())))
		// spec.md section 22 counts worker state changes as auditable. Only
		// the transition is recorded; a heartbeat every five seconds is not
		// an event worth keeping.
		s.auditWorker(ctx, "worker.online", workerID.String(), map[string]any{
			"previous_state":  string(worker.ConnectionState),
			"pending_uploads": request.GetPendingUploads(),
			"pending_reports": request.GetPendingReports(),
			"worker_version":  request.GetWorkerVersion(),
		})
	}

	desired, err := s.desiredGeneration(ctx, worker.StationID)
	if err != nil {
		return nil, err
	}
	current := desired == request.GetStateGeneration()

	// Only the moment a Worker becomes current counts as a completed sync.
	if current {
		if err := s.repo.RecordWorkerSync(ctx, workerID, now, desired); err != nil {
			s.logger.Error("record worker sync", slog.String("error", err.Error()))
		}
	}

	return &workerv1.HeartbeatResponse{
		ServerTimeUnixMs:  now.UnixMilli(),
		DesiredGeneration: desired,
		StateIsCurrent:    current,
	}, nil
}

// desiredGeneration hashes the station's current plan set.
func (s *Service) desiredGeneration(ctx context.Context, stationID uuid.UUID) (string, error) {
	stored, err := s.repo.ListPassPlansForStation(ctx, stationID, time.Now().UTC())
	if err != nil {
		s.logger.Error("load pass plans", slog.String("error", err.Error()))
		return "", status.Error(codes.Internal, "could not load the desired state")
	}
	plans := make([]*workerv1.PassPlan, 0, len(stored))
	for _, record := range stored {
		plan, err := passplan.Unmarshal(record.Encoded)
		if err != nil {
			return "", status.Error(codes.Internal, "a stored pass plan is unreadable")
		}
		plans = append(plans, plan)
	}
	return passplan.SetGeneration(plans), nil
}

// RunStaleWorkerMonitor marks workers offline once they stop reporting.
//
// spec.md section 6.3: the Server must clearly report an unavailable station,
// because scheduling depends on it. Returns when ctx is cancelled.
func RunStaleWorkerMonitor(ctx context.Context, repo *store.Repository, logger *slog.Logger) {
	logger.Info("worker monitor started",
		slog.Duration("stale_after", StaleAfter))

	ticker := time.NewTicker(StaleCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("worker monitor stopped")
			return
		case <-ticker.C:
			names, err := repo.MarkStaleWorkersOffline(ctx, time.Now().UTC().Add(-StaleAfter))
			if err != nil {
				logger.Error("mark stale workers offline", slog.String("error", err.Error()))
				continue
			}
			for _, name := range names {
				logger.Warn("worker is offline; new scheduling is blocked for its station",
					slog.String("worker", name))
				if _, err := repo.AppendAudit(ctx, domain.AuditRecord{
					Action: "worker.offline", EntityType: "worker", EntityID: name,
					NewState: map[string]any{"detected_by": "heartbeat timeout"},
				}); err != nil {
					logger.Error("audit worker offline", slog.String("error", err.Error()))
				}
			}
		}
	}
}

// auditWorker records a worker state change. There is no human actor, so the
// record carries none: the trail says the system did it.
func (s *Service) auditWorker(ctx context.Context, action, workerID string,
	details map[string]any) {
	if _, err := s.repo.AppendAudit(ctx, domain.AuditRecord{
		Action: action, EntityType: "worker", EntityID: workerID,
		NewState: details,
	}); err != nil {
		s.logger.Error("audit worker state",
			slog.String("action", action), slog.String("error", err.Error()))
	}
}
