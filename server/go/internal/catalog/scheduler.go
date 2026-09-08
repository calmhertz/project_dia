package catalog

import (
	"context"
	"log/slog"
	"time"
)

// checkInterval is how often the Server looks for stale orbital data. It is
// well below RefreshInterval so a satellite added mid-cycle is not left stale
// for a whole day.
const checkInterval = time.Hour

// RunRefreshLoop keeps orbital data under 24 hours old (spec.md section 11.3).
//
// It returns when ctx is cancelled. A sweep that fails is logged and retried
// on the next tick rather than stopping the loop: a provider outage must not
// permanently disable refreshing.
func (s *Service) RunRefreshLoop(ctx context.Context) {
	s.logger.Info("tle refresh loop started",
		slog.Duration("check_interval", checkInterval),
		slog.Duration("refresh_interval", RefreshInterval))

	// Sweep once at startup so a Server that was down past the interval
	// catches up rather than waiting a full tick.
	s.sweep(ctx)

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("tle refresh loop stopped")
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

func (s *Service) sweep(ctx context.Context) {
	outcomes := s.RefreshAll(ctx, false)
	if len(outcomes) == 0 {
		return
	}

	updated, kept, failed := 0, 0, 0
	for _, outcome := range outcomes {
		switch {
		case outcome.Err != nil:
			failed++
		case outcome.UsedLastKnownGood:
			kept++
		case outcome.Updated:
			updated++
		}
	}
	s.logger.Info("tle refresh sweep complete",
		slog.Int("checked", len(outcomes)), slog.Int("updated", updated),
		slog.Int("kept_last_known_good", kept), slog.Int("failed", failed))
}
