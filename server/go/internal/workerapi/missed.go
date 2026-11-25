package workerapi

import (
	"context"
	"log/slog"
	"time"

	"aagasa/internal/domain"
	"aagasa/internal/store"
)

const (
	// MissedGrace is how long after a pass's recording ends the server waits
	// for the worker's report before calling the pass missed. A late report of
	// completed or failed still replaces missed.
	MissedGrace = 30 * time.Minute
	// ExecutingGrace is longer: a worker that started a pass may be offline
	// or still uploading, and will report when it can.
	ExecutingGrace      = 6 * time.Hour
	MissedSweepInterval = time.Minute
)

func RunMissedPassSweeper(ctx context.Context, repo *store.Repository, logger *slog.Logger) {
	logger.Info("missed pass sweeper started", slog.Duration("grace", MissedGrace))

	ticker := time.NewTicker(MissedSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("missed pass sweeper stopped")
			return
		case <-ticker.C:
			if _, err := SweepMissedPasses(ctx, repo, logger, time.Now().UTC()); err != nil {
				logger.Error("sweep missed passes", slog.String("error", err.Error()))
			}
		}
	}
}

// SweepMissedPasses marks passes missed that were never reported: approved
// ones more than MissedGrace after their recording ended, executing ones more
// than ExecutingGrace after. Each is audited.
func SweepMissedPasses(ctx context.Context, repo *store.Repository, logger *slog.Logger,
	now time.Time) (int, error) {
	ids, err := repo.MarkOverduePassesMissed(ctx, now.Add(-MissedGrace), now.Add(-ExecutingGrace))
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		logger.Warn("pass marked missed; the worker never reported it",
			slog.String("pass_id", id.String()))
		if _, err := repo.AppendAudit(ctx, domain.AuditRecord{
			Action: "pass.missed", EntityType: "pass", EntityID: id.String(),
			NewState: map[string]any{
				"status":      string(domain.PassMissed),
				"detected_by": "no worker report",
			},
		}); err != nil {
			logger.Error("audit missed pass", slog.String("error", err.Error()))
		}
	}
	return len(ids), nil
}
