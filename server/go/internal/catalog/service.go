package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"aagasa/internal/domain"
	"aagasa/internal/store"
	"aagasa/internal/tle"
)

const RefreshInterval = 24 * time.Hour

var (
	ErrNoOrbitalData = errors.New("no orbital data available")
	ErrAlreadyExists = errors.New("satellite already in the catalogue")
)

type MetadataProvider interface {
	FetchMetadata(ctx context.Context, noradID int) (tle.Metadata, error)
}

type Service struct {
	repo     *store.Repository
	chain    *tle.Chain
	metadata MetadataProvider
	logger   *slog.Logger
	now      func() time.Time
}

func NewService(repo *store.Repository, chain *tle.Chain, metadata MetadataProvider, logger *slog.Logger) *Service {
	return &Service{repo: repo, chain: chain, metadata: metadata, logger: logger, now: time.Now}
}

type RefreshOutcome struct {
	NoradID int
	Updated bool
	Source tle.Source
	UsedLastKnownGood bool
	Epoch             time.Time
	Err               error
}

func (s *Service) AddSatellite(ctx context.Context, noradID int, name string) (domain.Satellite, error) {
	if noradID <= 0 {
		return domain.Satellite{}, fmt.Errorf("norad id must be positive")
	}

	if _, err := s.repo.GetSatelliteByNoradID(ctx, noradID); err == nil {
		return domain.Satellite{}, ErrAlreadyExists
	} else if !errors.Is(err, store.ErrNotFound) {
		return domain.Satellite{}, err
	}

	metadata := map[string]any{}
	if s.metadata != nil {
		if fetched, err := s.metadata.FetchMetadata(ctx, noradID); err != nil {
			s.logger.Warn("satellite metadata unavailable",
				slog.Int("norad_id", noradID), slog.String("error", err.Error()))
		} else {
			metadata = fetched.AsMap()
			if name == "" {
				name = fetched.Name
			}
		}
	}

	set, source, err := s.chain.Fetch(ctx, noradID)
	if err != nil {
		return domain.Satellite{}, fmt.Errorf("%w for %d: %v", ErrNoOrbitalData, noradID, err)
	}
	if name == "" {
		name = set.Name
	}
	if name == "" {
		name = fmt.Sprintf("NORAD %d", noradID)
	}

	satellite, err := s.repo.CreateSatellite(ctx, domain.Satellite{
		NoradID: noradID, Name: name, Metadata: metadata, IsSchedulable: true,
	})
	if err != nil {
		return domain.Satellite{}, err
	}

	if _, err := s.storeTLE(ctx, satellite.ID, set, source); err != nil {
		return domain.Satellite{}, err
	}

	s.logger.Info("satellite added",
		slog.Int("norad_id", noradID), slog.String("source", string(source)))
	return satellite, nil
}

func (s *Service) Refresh(ctx context.Context, satellite domain.Satellite) RefreshOutcome {
	outcome := RefreshOutcome{NoradID: satellite.NoradID}

	set, source, err := s.chain.Fetch(ctx, satellite.NoradID)
	if err != nil {
		existing, storeErr := s.repo.LatestTLERecord(ctx, satellite.ID)
		if storeErr != nil {
			outcome.Err = fmt.Errorf("%w for %d: %v", ErrNoOrbitalData, satellite.NoradID, err)
			return outcome
		}
		s.logger.Warn("tle refresh failed; keeping last known-good",
			slog.Int("norad_id", satellite.NoradID),
			slog.Time("epoch", existing.Epoch),
			slog.String("error", err.Error()))
		outcome.UsedLastKnownGood = true
		outcome.Epoch = existing.Epoch
		return outcome
	}

	stored, err := s.storeTLE(ctx, satellite.ID, set, source)
	if err != nil {
		outcome.Err = err
		return outcome
	}

	outcome.Updated = stored
	outcome.Source = source
	outcome.Epoch = set.Epoch
	return outcome
}

func (s *Service) storeTLE(ctx context.Context, satelliteID uuid.UUID, set tle.Set, source tle.Source) (bool, error) {
	_, err := s.repo.CreateTLERecord(ctx, domain.TLERecord{
		SatelliteID: satelliteID,
		Line1:       set.Line1,
		Line2:       set.Line2,
		Epoch:       set.Epoch,
		Source:      domain.TLESource(source),
	})
	if errors.Is(err, store.ErrDuplicate) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) RefreshAll(ctx context.Context, force bool) []RefreshOutcome {
	satellites, err := s.repo.ListSatellites(ctx)
	if err != nil {
		s.logger.Error("listing satellites for refresh failed", slog.String("error", err.Error()))
		return nil
	}

	var outcomes []RefreshOutcome
	for _, satellite := range satellites {
		if ctx.Err() != nil {
			break
		}
		if !force && !s.needsRefresh(ctx, satellite) {
			continue
		}
		outcomes = append(outcomes, s.Refresh(ctx, satellite))
	}
	return outcomes
}

func (s *Service) needsRefresh(ctx context.Context, satellite domain.Satellite) bool {
	record, err := s.repo.LatestTLERecord(ctx, satellite.ID)
	if err != nil {
		return true
	}
	return s.now().UTC().Sub(record.FetchedAt) >= RefreshInterval
}

func (s *Service) CurrentTLE(ctx context.Context, satelliteID uuid.UUID) (domain.TLERecord, error) {
	return s.repo.LatestTLERecord(ctx, satelliteID)
}

func (s *Service) RefreshMetadata(ctx context.Context, satellite domain.Satellite) (map[string]any, error) {
	if s.metadata == nil {
		return nil, errors.New("no metadata provider configured")
	}

	fetched, err := s.metadata.FetchMetadata(ctx, satellite.NoradID)
	if err != nil {
		return nil, fmt.Errorf("metadata unavailable for %d: %w", satellite.NoradID, err)
	}

	enriched := fetched.AsMap()
	if err := s.repo.SetSatelliteMetadata(ctx, satellite.ID, enriched); err != nil {
		return nil, err
	}
	s.logger.Info("satellite metadata refreshed", slog.Int("norad_id", satellite.NoradID))
	return enriched, nil
}
