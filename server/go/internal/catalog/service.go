// Package catalog owns the satellite catalogue and its orbital data.
//
// The NORAD catalog number is the satellite's identity; TLEs are versioned
// orbital data belonging to it (spec.md section 11.1).
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

// RefreshInterval is the maximum age of orbital data before the Server
// refreshes it (spec.md section 11.3).
const RefreshInterval = 24 * time.Hour

var (
	// ErrNoOrbitalData means the satellite has no TLE at all: no provider
	// answered and nothing was stored previously.
	ErrNoOrbitalData = errors.New("no orbital data available")
	// ErrAlreadyExists means the catalog number is already registered.
	ErrAlreadyExists = errors.New("satellite already in the catalogue")
)

// MetadataProvider supplies optional presentation metadata.
type MetadataProvider interface {
	FetchMetadata(ctx context.Context, noradID int) (tle.Metadata, error)
}

// Service manages satellites and their orbital data.
type Service struct {
	repo     *store.Repository
	chain    *tle.Chain
	metadata MetadataProvider
	logger   *slog.Logger
	now      func() time.Time
}

// NewService builds the catalogue service. metadata may be nil, in which case
// satellites are stored without enrichment.
func NewService(repo *store.Repository, chain *tle.Chain, metadata MetadataProvider, logger *slog.Logger) *Service {
	return &Service{repo: repo, chain: chain, metadata: metadata, logger: logger, now: time.Now}
}

// RefreshOutcome describes what a refresh did, for reporting and audit.
type RefreshOutcome struct {
	NoradID int
	// Updated is true when a new TLE version was stored.
	Updated bool
	// Source is the provider that answered, empty when none did.
	Source tle.Source
	// UsedLastKnownGood is true when every provider failed but stored data
	// remains usable.
	UsedLastKnownGood bool
	Epoch             time.Time
	Err               error
}

// AddSatellite registers a satellite by catalog number and fetches its first
// orbital data.
//
// Metadata enrichment is best effort: a satellite with valid orbital data is
// trackable whether or not its display metadata arrived (spec.md section 11.2).
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

	// A satellite needs orbital data to be useful, so fetch before creating
	// the record and fail the whole operation if nothing is available.
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

// Refresh updates one satellite's orbital data.
//
// A provider failure never discards what is already stored: the last
// known-good record stays in place and is reported as still usable
// (spec.md section 11.3).
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

// storeTLE persists an element set, reporting whether it was new.
//
// Re-fetching unchanged elements is normal and must not create a second row;
// the database unique constraint is the authority.
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

// RefreshAll refreshes every satellite whose data is older than the interval.
//
// One satellite's failure does not stop the sweep: an outage affecting a
// single object must not leave the rest of the catalogue stale.
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

// needsRefresh reports whether stored data has aged past the interval.
func (s *Service) needsRefresh(ctx context.Context, satellite domain.Satellite) bool {
	record, err := s.repo.LatestTLERecord(ctx, satellite.ID)
	if err != nil {
		// No stored data at all: it certainly needs fetching.
		return true
	}
	return s.now().UTC().Sub(record.FetchedAt) >= RefreshInterval
}

// CurrentTLE returns the satellite's newest orbital data.
func (s *Service) CurrentTLE(ctx context.Context, satelliteID uuid.UUID) (domain.TLERecord, error) {
	return s.repo.LatestTLERecord(ctx, satelliteID)
}

// RefreshMetadata re-fetches presentation metadata for one satellite.
//
// Enrichment is best effort at add time, so a transient provider failure would
// otherwise leave a satellite with empty metadata permanently. This gives an
// operator a way to fill it in later without deleting the record and the
// orbital history attached to it.
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
