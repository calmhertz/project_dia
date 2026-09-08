package catalog_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aagasa/internal/catalog"
	"aagasa/internal/store"
	"aagasa/internal/testsupport"
	"aagasa/internal/tle"
)

// Real element sets captured from the live providers on 2026-08-24.
const (
	celestrakISS = "ISS (ZARYA)             \r\n" +
		"1 25544U 98067A   26236.17729445  .00008773  00000+0  16369-3 0  9990\r\n" +
		"2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298\r\n"

	satnogsISS = "0 ISS (ZARYA)\n" +
		"1 25544U 98067A   26236.17729445  .00008773  00000-0  16369-3 0  9991\n" +
		"2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298\n"

	// A later epoch for the same object, to represent a refresh.
	celestrakISSNewer = "ISS (ZARYA)             \r\n" +
		"1 25544U 98067A   26237.17729445  .00008773  00000+0  16369-3 0  9991\r\n" +
		"2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298\r\n"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func newRepo(t *testing.T) *store.Repository {
	t.Helper()
	url := testsupport.NewDatabase(t)
	ctx := context.Background()
	if err := store.MigrateUp(ctx, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return store.NewRepository(pool)
}

// fakeProvider serves a scripted response and counts calls.
type fakeProvider struct {
	name  tle.Source
	text  string
	err   error
	calls int
}

func (f *fakeProvider) Name() tle.Source { return f.name }

func (f *fakeProvider) Fetch(ctx context.Context, noradID int) (tle.Set, error) {
	f.calls++
	if f.err != nil {
		return tle.Set{}, f.err
	}
	return tle.Parse(f.text)
}

type fakeMetadata struct {
	metadata tle.Metadata
	err      error
}

func (f fakeMetadata) FetchMetadata(ctx context.Context, noradID int) (tle.Metadata, error) {
	return f.metadata, f.err
}

func newService(t *testing.T, repo *store.Repository, metadata catalog.MetadataProvider,
	providers ...tle.Provider) *catalog.Service {
	t.Helper()
	return catalog.NewService(repo, tle.NewChain(discardLogger(), providers...), metadata, discardLogger())
}

func TestAddSatelliteStoresRecordAndOrbitalData(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	service := newService(t, repo,
		fakeMetadata{metadata: tle.Metadata{Name: "ISS", Status: "alive", Countries: "RU,US"}},
		&fakeProvider{name: tle.SourceCelestrak, text: celestrakISS})

	satellite, err := service.AddSatellite(ctx, 25544, "")
	if err != nil {
		t.Fatalf("AddSatellite: %v", err)
	}

	if satellite.NoradID != 25544 {
		t.Errorf("NoradID = %d, want 25544", satellite.NoradID)
	}
	if satellite.Metadata["status"] != "alive" {
		t.Errorf("metadata = %v", satellite.Metadata)
	}

	record, err := service.CurrentTLE(ctx, satellite.ID)
	if err != nil {
		t.Fatalf("CurrentTLE: %v", err)
	}
	if record.Source != "celestrak" {
		t.Errorf("source = %s, want celestrak", record.Source)
	}
}

// spec.md section 11.2: missing metadata must not block tracking.
func TestSatelliteIsUsableWithoutMetadata(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	service := newService(t, repo,
		fakeMetadata{err: errors.New("satnogs unreachable")},
		&fakeProvider{name: tle.SourceCelestrak, text: celestrakISS})

	satellite, err := service.AddSatellite(ctx, 25544, "")
	if err != nil {
		t.Fatalf("AddSatellite: %v", err)
	}

	// The name falls back to the TLE title, and orbital data is present.
	if satellite.Name != "ISS (ZARYA)" {
		t.Errorf("Name = %q, want the TLE title", satellite.Name)
	}
	if _, err := service.CurrentTLE(ctx, satellite.ID); err != nil {
		t.Errorf("no orbital data despite a working provider: %v", err)
	}
}

// Without orbital data a satellite cannot be tracked, so adding must fail.
func TestAddSatelliteFailsWhenNoProviderHasData(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	service := newService(t, repo, nil,
		&fakeProvider{name: tle.SourceCelestrak, err: tle.ErrNotFound},
		&fakeProvider{name: tle.SourceSatNOGS, err: tle.ErrNotFound})

	_, err := service.AddSatellite(ctx, 99999, "")
	if !errors.Is(err, catalog.ErrNoOrbitalData) {
		t.Fatalf("error = %v, want ErrNoOrbitalData", err)
	}

	// Nothing partial was left behind.
	if satellites, err := repo.ListSatellites(ctx); err != nil || len(satellites) != 0 {
		t.Errorf("satellites = %d, %v; want none", len(satellites), err)
	}
}

func TestAddingTheSameCatalogNumberTwiceIsRejected(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	service := newService(t, repo, nil, &fakeProvider{name: tle.SourceCelestrak, text: celestrakISS})

	if _, err := service.AddSatellite(ctx, 25544, ""); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := service.AddSatellite(ctx, 25544, ""); !errors.Is(err, catalog.ErrAlreadyExists) {
		t.Errorf("second add error = %v, want ErrAlreadyExists", err)
	}
}

// spec.md section 11.3: the fallback answers when the primary is down.
func TestRefreshFallsBackToTheSecondProvider(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	primary := &fakeProvider{name: tle.SourceCelestrak, err: errors.New("celestrak down")}
	fallback := &fakeProvider{name: tle.SourceSatNOGS, text: satnogsISS}
	service := newService(t, repo, nil, primary, fallback)

	satellite, err := service.AddSatellite(ctx, 25544, "ISS")
	if err != nil {
		t.Fatalf("AddSatellite: %v", err)
	}

	record, err := service.CurrentTLE(ctx, satellite.ID)
	if err != nil {
		t.Fatalf("CurrentTLE: %v", err)
	}
	if record.Source != "satnogs" {
		t.Errorf("source = %s, want satnogs", record.Source)
	}
}

// The critical rule: a provider outage must never discard stored orbital data.
func TestProviderOutagePreservesLastKnownGood(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	provider := &fakeProvider{name: tle.SourceCelestrak, text: celestrakISS}
	service := newService(t, repo, nil, provider)

	satellite, err := service.AddSatellite(ctx, 25544, "ISS")
	if err != nil {
		t.Fatalf("AddSatellite: %v", err)
	}
	original, err := service.CurrentTLE(ctx, satellite.ID)
	if err != nil {
		t.Fatalf("CurrentTLE: %v", err)
	}

	// Every provider now fails.
	provider.err = errors.New("network unreachable")
	outcome := service.Refresh(ctx, satellite)

	if outcome.Err != nil {
		t.Errorf("refresh reported an error instead of keeping known-good: %v", outcome.Err)
	}
	if !outcome.UsedLastKnownGood {
		t.Error("UsedLastKnownGood is false during a total outage")
	}
	if outcome.Updated {
		t.Error("refresh claimed an update while every provider was down")
	}

	// The stored record is untouched and still readable.
	after, err := service.CurrentTLE(ctx, satellite.ID)
	if err != nil {
		t.Fatalf("orbital data lost after a failed refresh: %v", err)
	}
	if after.ID != original.ID || after.Line1 != original.Line1 {
		t.Error("the last known-good record was replaced")
	}
}

func TestRefreshStoresANewerEpochAsANewVersion(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	provider := &fakeProvider{name: tle.SourceCelestrak, text: celestrakISS}
	service := newService(t, repo, nil, provider)

	satellite, err := service.AddSatellite(ctx, 25544, "ISS")
	if err != nil {
		t.Fatalf("AddSatellite: %v", err)
	}

	provider.text = celestrakISSNewer
	outcome := service.Refresh(ctx, satellite)

	if !outcome.Updated {
		t.Error("a newer epoch was not stored")
	}

	records, err := repo.ListTLERecords(ctx, satellite.ID, 10)
	if err != nil {
		t.Fatalf("ListTLERecords: %v", err)
	}
	// Both versions are retained: history is not overwritten.
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if !records[0].Epoch.After(records[1].Epoch) {
		t.Error("records are not newest-first")
	}
}

// Re-fetching unchanged elements must not create a duplicate version.
func TestRefreshWithUnchangedElementsStoresNothingNew(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	service := newService(t, repo, nil, &fakeProvider{name: tle.SourceCelestrak, text: celestrakISS})

	satellite, err := service.AddSatellite(ctx, 25544, "ISS")
	if err != nil {
		t.Fatalf("AddSatellite: %v", err)
	}

	outcome := service.Refresh(ctx, satellite)
	if outcome.Updated {
		t.Error("an unchanged TLE was stored as a new version")
	}
	if outcome.Err != nil {
		t.Errorf("unexpected error: %v", outcome.Err)
	}

	records, err := repo.ListTLERecords(ctx, satellite.ID, 10)
	if err != nil || len(records) != 1 {
		t.Errorf("records = %d, %v; want 1", len(records), err)
	}
}

// One satellite's outage must not stop the rest of the sweep.
func TestRefreshAllContinuesPastAFailure(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	provider := &fakeProvider{name: tle.SourceCelestrak, text: celestrakISS}
	service := newService(t, repo, nil, provider)

	if _, err := service.AddSatellite(ctx, 25544, "ISS"); err != nil {
		t.Fatalf("add iss: %v", err)
	}
	provider.text = "NOAA 19                 \r\n" +
		"1 33591U 09005A   26236.28857781  .00000013  00000+0  30729-4 0  9998\r\n" +
		"2 33591  98.9473 307.1961 0013555 193.3958 166.6856 14.13483201904106\r\n"
	if _, err := service.AddSatellite(ctx, 33591, "NOAA 19"); err != nil {
		t.Fatalf("add noaa: %v", err)
	}

	provider.err = errors.New("provider down")
	outcomes := service.RefreshAll(ctx, true)

	if len(outcomes) != 2 {
		t.Fatalf("outcomes = %d, want 2", len(outcomes))
	}
	for _, outcome := range outcomes {
		if !outcome.UsedLastKnownGood {
			t.Errorf("satellite %d did not fall back to known-good", outcome.NoradID)
		}
	}
}

// Fresh data must not be re-fetched on every tick.
func TestRefreshAllSkipsRecentlyFetchedSatellites(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	provider := &fakeProvider{name: tle.SourceCelestrak, text: celestrakISS}
	service := newService(t, repo, nil, provider)

	if _, err := service.AddSatellite(ctx, 25544, "ISS"); err != nil {
		t.Fatalf("AddSatellite: %v", err)
	}
	callsAfterAdd := provider.calls

	if outcomes := service.RefreshAll(ctx, false); len(outcomes) != 0 {
		t.Errorf("outcomes = %d, want 0 for freshly fetched data", len(outcomes))
	}
	if provider.calls != callsAfterAdd {
		t.Error("a provider was contacted for data fetched moments ago")
	}

	// Forcing bypasses the age check.
	if outcomes := service.RefreshAll(ctx, true); len(outcomes) != 1 {
		t.Errorf("forced outcomes = %d, want 1", len(outcomes))
	}
}

func TestRefreshIntervalMatchesTheSpecifiedPolicy(t *testing.T) {
	// spec.md section 11.3 requires at least every 24 hours.
	if catalog.RefreshInterval > 24*time.Hour {
		t.Errorf("RefreshInterval = %v, must not exceed 24h", catalog.RefreshInterval)
	}
}

// A provider that answers with corrupt data is as bad as one that is down:
// the stored record must survive either way.
func TestCorruptProviderResponsePreservesLastKnownGood(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	provider := &fakeProvider{name: tle.SourceCelestrak, text: celestrakISS}
	service := newService(t, repo, nil, provider)

	satellite, err := service.AddSatellite(ctx, 25544, "ISS")
	if err != nil {
		t.Fatalf("AddSatellite: %v", err)
	}
	original, err := service.CurrentTLE(ctx, satellite.ID)
	if err != nil {
		t.Fatalf("CurrentTLE: %v", err)
	}

	// Same lines, one digit flipped, so the checksum no longer matches.
	provider.text = "ISS (ZARYA)\r\n" +
		"1 25544U 98067A   26237.17729445  .00008773  00000+0  16369-3 0  9990\r\n" +
		"2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298\r\n"

	outcome := service.Refresh(ctx, satellite)

	if outcome.Updated {
		t.Error("a checksum-invalid TLE was stored")
	}
	if !outcome.UsedLastKnownGood {
		t.Error("UsedLastKnownGood is false after a corrupt response")
	}

	after, err := service.CurrentTLE(ctx, satellite.ID)
	if err != nil || after.ID != original.ID {
		t.Error("the stored record did not survive a corrupt provider response")
	}
}
