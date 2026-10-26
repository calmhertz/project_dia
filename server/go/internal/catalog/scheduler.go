package catalog

import (
	"context"
	"log/slog"
	"time"
)

const checkInterval = time.Hour

func (s *Service) RunRefreshLoop(ctx context.Context) {
	s.logger.Info("tle refresh loop started",
		slog.Duration("check_interval", checkInterval),
		slog.Duration("refresh_interval", RefreshInterval))

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
