package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"aagasa/internal/domain"
	"aagasa/internal/store"
	"aagasa/internal/testsupport"
)

// These tests need real databases. They are skipped unless the environment
// points at them, so the default "go test ./..." stays hermetic.
//
//	AAGASA_TEST_POSTGRES_URL=postgres://... \
//	AAGASA_TEST_REDIS_URL=redis://...       \
//	AAGASA_TEST_MONGO_URL=mongodb://...     go test ./internal/store/...

// argon2idHash is a syntactically valid Argon2id hash; the schema rejects any
// other format. Real hashing arrives in V3.
const argon2idHash = "$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHR2YWx1ZQ$aGFzaHZhbHVlaGFzaHZhbHVlaGFzaHZhbA"

// newTestPool migrates a fresh schema and returns a pool.
func newTestPool(t *testing.T) (*pgxpool.Pool, *store.Repository) {
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
	return pool, store.NewRepository(pool)
}

// fixture builds the records a pass depends on.
type fixture struct {
	user      domain.User
	station   domain.Station
	config    domain.SchedulingConfig
	satellite domain.Satellite
	tle       domain.TLERecord
}

func newFixture(t *testing.T, ctx context.Context, repo *store.Repository, suffix string) fixture {
	t.Helper()

	user, err := repo.CreateUser(ctx, domain.User{
		Username:     "operator-" + suffix,
		PasswordHash: argon2idHash,
		Role:         domain.RoleUser,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	station, err := repo.CreateStation(ctx, domain.Station{
		Name: "station-" + suffix, Latitude: 13.394944, Longitude: 77.729444,
		AltitudeM: 915, Timezone: "Asia/Kolkata", ActiveRFBand: domain.BandVHF,
	})
	if err != nil {
		t.Fatalf("create station: %v", err)
	}

	now := time.Now().UTC()
	config, err := repo.CreateSchedulingConfig(ctx, domain.SchedulingConfig{
		StationID: station.ID, MinimumLeadTime: 30 * time.Minute,
		PrePassBuffer: 2 * time.Minute, PostPassBuffer: 2 * time.Minute,
		RecordingPreRoll: 10 * time.Second, RecordingPostRoll: 10 * time.Second,
		MinimumElevationDegrees: 10, CreatedBy: &user.ID, EffectiveFrom: now,
	})
	if err != nil {
		t.Fatalf("create scheduling config: %v", err)
	}

	satellite, err := repo.CreateSatellite(ctx, domain.Satellite{
		NoradID: 25544, Name: "ISS (ZARYA)",
		Metadata: map[string]any{"source": "celestrak"}, IsSchedulable: true,
	})
	if err != nil {
		t.Fatalf("create satellite: %v", err)
	}

	tle, err := repo.CreateTLERecord(ctx, domain.TLERecord{
		SatelliteID: satellite.ID,
		Line1:       "1 25544U 98067A   24001.50000000  .00016717  00000-0  10270-3 0  9005",
		Line2:       "2 25544  51.6416 247.4627 0006703 130.5360 325.0288 15.49815310 10000",
		Epoch:       now.Add(-12 * time.Hour), Source: domain.SourceCelestrak,
	})
	if err != nil {
		t.Fatalf("create tle: %v", err)
	}

	return fixture{user: user, station: station, config: config, satellite: satellite, tle: tle}
}

// passAt builds a pass reserving [start, start+duration) plus buffers.
func (f fixture) passAt(start time.Time, duration time.Duration) domain.Pass {
	buffer := 2 * time.Minute
	return domain.Pass{
		StationID: f.station.ID, SatelliteID: f.satellite.ID, RequestedBy: f.user.ID,
		TLERecordID: f.tle.ID, SchedulingConfigID: f.config.ID,
		Status: domain.PassPendingApproval, Visibility: domain.VisibilityPrivate,
		Band:  domain.BandVHF,
		AOSAt: start, LOSAt: start.Add(duration), MaxElevationDegrees: 42.5,
		ReservedFrom: start.Add(-buffer), ReservedTo: start.Add(duration + buffer),
		RecordingMode:    domain.RecordRaw,
		RecordingPreRoll: 10 * time.Second, RecordingPostRoll: 10 * time.Second,
		RadioSettings: map[string]any{"frequency_hz": float64(145800000)},
	}
}

func TestCreateAndReadDomainRecords(t *testing.T) {
	ctx := context.Background()
	_, repo := newTestPool(t)
	f := newFixture(t, ctx, repo, "read")

	worker, err := repo.CreateWorker(ctx, domain.Worker{
		StationID: f.station.ID, Name: "worker-read", WorkerVersion: "0.1.0",
		ConnectionState: domain.WorkerOffline,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	pass, err := repo.CreatePass(ctx, f.passAt(time.Now().UTC().Add(2*time.Hour), 10*time.Minute))
	if err != nil {
		t.Fatalf("create pass: %v", err)
	}

	// Read every record back and confirm the round trip preserved values.
	if got, err := repo.GetUserByUsername(ctx, "operator-read"); err != nil || got.ID != f.user.ID {
		t.Errorf("GetUserByUsername = %v, %v", got.ID, err)
	}
	if got, err := repo.GetStationByID(ctx, f.station.ID); err != nil || got.Name != f.station.Name {
		t.Errorf("GetStationByID = %v, %v", got.Name, err)
	}
	if got, err := repo.GetWorkerByName(ctx, "worker-read"); err != nil || got.ID != worker.ID {
		t.Errorf("GetWorkerByName = %v, %v", got.ID, err)
	}
	if got, err := repo.GetSatelliteByNoradID(ctx, 25544); err != nil || got.Metadata["source"] != "celestrak" {
		t.Errorf("GetSatelliteByNoradID = %v, %v", got.Metadata, err)
	}
	if got, err := repo.LatestTLERecord(ctx, f.satellite.ID); err != nil || got.ID != f.tle.ID {
		t.Errorf("LatestTLERecord = %v, %v", got.ID, err)
	}

	readBack, err := repo.GetPassByID(ctx, pass.ID)
	if err != nil {
		t.Fatalf("GetPassByID: %v", err)
	}
	if readBack.RecordingPreRoll != 10*time.Second {
		t.Errorf("RecordingPreRoll = %v, want 10s", readBack.RecordingPreRoll)
	}
	if readBack.RadioSettings["frequency_hz"] != float64(145800000) {
		t.Errorf("RadioSettings = %v", readBack.RadioSettings)
	}

	cfg, err := repo.LatestEffectiveSchedulingConfig(ctx, f.station.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("LatestEffectiveSchedulingConfig: %v", err)
	}
	if cfg.MinimumLeadTime != 30*time.Minute {
		t.Errorf("MinimumLeadTime = %v, want 30m", cfg.MinimumLeadTime)
	}
}

// spec.md section 13.3: any overlapping reservation on the same station is a
// conflict, regardless of satellite, band or user.
func TestOverlappingStationReservationIsRejected(t *testing.T) {
	ctx := context.Background()
	_, repo := newTestPool(t)
	f := newFixture(t, ctx, repo, "overlap")

	start := time.Now().UTC().Add(4 * time.Hour)
	if _, err := repo.CreatePass(ctx, f.passAt(start, 15*time.Minute)); err != nil {
		t.Fatalf("first pass: %v", err)
	}

	// Pass A 10:00-10:15, pass B 10:10-10:20 -> B must be rejected.
	_, err := repo.CreatePass(ctx, f.passAt(start.Add(10*time.Minute), 10*time.Minute))
	if !errors.Is(err, store.ErrStationOverlap) {
		t.Fatalf("overlapping pass error = %v, want ErrStationOverlap", err)
	}

	// A non-overlapping window after the buffers must still be accepted.
	if _, err := repo.CreatePass(ctx, f.passAt(start.Add(30*time.Minute), 10*time.Minute)); err != nil {
		t.Errorf("non-overlapping pass rejected: %v", err)
	}
}

// A terminal pass releases the station so the slot can be reused.
func TestTerminalPassReleasesReservation(t *testing.T) {
	ctx := context.Background()
	_, repo := newTestPool(t)
	f := newFixture(t, ctx, repo, "release")

	start := time.Now().UTC().Add(6 * time.Hour)
	first, err := repo.CreatePass(ctx, f.passAt(start, 10*time.Minute))
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}

	if _, err := repo.CreatePass(ctx, f.passAt(start, 10*time.Minute)); !errors.Is(err, store.ErrStationOverlap) {
		t.Fatalf("duplicate window error = %v, want ErrStationOverlap", err)
	}

	if err := repo.SetPassStatus(ctx, first.ID, domain.PassCancelled); err == nil {
		t.Fatal("expected cancelling without a reason to be rejected")
	}
	if err := repo.SetPassStatus(ctx, first.ID, domain.PassMissed); err != nil {
		t.Fatalf("set missed: %v", err)
	}
	if _, err := repo.CreatePass(ctx, f.passAt(start, 10*time.Minute)); err != nil {
		t.Errorf("slot not released after terminal status: %v", err)
	}
}

// Concurrent requests must not race past the conflict rule.
func TestConcurrentOverlappingRequestsProduceOneWinner(t *testing.T) {
	ctx := context.Background()
	_, repo := newTestPool(t)
	f := newFixture(t, ctx, repo, "concurrent")

	const attempts = 8
	start := time.Now().UTC().Add(8 * time.Hour)
	results := make(chan error, attempts)

	for i := 0; i < attempts; i++ {
		go func() {
			_, err := repo.CreatePass(ctx, f.passAt(start, 10*time.Minute))
			results <- err
		}()
	}

	accepted, conflicted := 0, 0
	for i := 0; i < attempts; i++ {
		switch err := <-results; {
		case err == nil:
			accepted++
		case errors.Is(err, store.ErrStationOverlap):
			conflicted++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}

	if accepted != 1 {
		t.Errorf("accepted = %d, want exactly 1", accepted)
	}
	if conflicted != attempts-1 {
		t.Errorf("conflicted = %d, want %d", conflicted, attempts-1)
	}
}

// spec.md section 21: no code path may persist a non-Argon2id password hash.
func TestNonArgon2idPasswordHashIsRejected(t *testing.T) {
	ctx := context.Background()
	_, repo := newTestPool(t)

	for _, hash := range []string{"plaintext", "$2y$10$abcdefghijklmnop", "$argon2i$v=19$m=1,t=1,p=1$x$y"} {
		_, err := repo.CreateUser(ctx, domain.User{
			Username: fmt.Sprintf("user-%d", len(hash)), PasswordHash: hash, Role: domain.RoleUser,
		})
		if err == nil {
			t.Errorf("hash %q was accepted", hash)
		}
	}
}

// spec.md section 17.1: Root is singular.
func TestSecondRootIsRejected(t *testing.T) {
	ctx := context.Background()
	_, repo := newTestPool(t)

	if _, err := repo.CreateUser(ctx, domain.User{
		Username: "root", PasswordHash: argon2idHash, Role: domain.RoleRoot,
	}); err != nil {
		t.Fatalf("create root: %v", err)
	}

	_, err := repo.CreateUser(ctx, domain.User{
		Username: "root2", PasswordHash: argon2idHash, Role: domain.RoleRoot,
	})
	if !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("second root error = %v, want ErrDuplicate", err)
	}
}

// spec.md section 22: audit history must not be rewritten.
func TestAuditRecordsAreAppendOnly(t *testing.T) {
	ctx := context.Background()
	pool, repo := newTestPool(t)
	f := newFixture(t, ctx, repo, "audit")

	record, err := repo.AppendAudit(ctx, domain.AuditRecord{
		ActorUserID: &f.user.ID, Action: "station.updated",
		EntityType: "station", EntityID: f.station.ID.String(),
		PreviousState: map[string]any{"active_rf_band": "vhf"},
		NewState:      map[string]any{"active_rf_band": "uhf"},
		Reason:        "band switch",
	})
	if err != nil {
		t.Fatalf("append audit: %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE audit_records SET action = 'tampered' WHERE id = $1`, record.ID); err == nil {
		t.Error("audit record was updatable")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM audit_records WHERE id = $1`, record.ID); err == nil {
		t.Error("audit record was deletable")
	}

	count, err := repo.CountAuditRecords(ctx, "station", f.station.ID.String())
	if err != nil || count != 1 {
		t.Errorf("CountAuditRecords = %d, %v; want 1", count, err)
	}
}

// A rolled-back transaction must leave nothing behind.
func TestTransactionRollbackDiscardsWrites(t *testing.T) {
	ctx := context.Background()
	_, repo := newTestPool(t)

	sentinel := errors.New("deliberate failure")
	err := repo.InTx(ctx, func(tx *store.Repository) error {
		if _, err := tx.CreateUser(ctx, domain.User{
			Username: "rolled-back", PasswordHash: argon2idHash, Role: domain.RoleUser,
		}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InTx error = %v, want sentinel", err)
	}

	if _, err := repo.GetUserByUsername(ctx, "rolled-back"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("user survived rollback: %v", err)
	}
}

func TestSchedulingConfigVersionsAreRetained(t *testing.T) {
	ctx := context.Background()
	_, repo := newTestPool(t)
	f := newFixture(t, ctx, repo, "config")

	now := time.Now().UTC()
	// A new lead time only becomes usable later (spec.md section 13.6).
	future, err := repo.CreateSchedulingConfig(ctx, domain.SchedulingConfig{
		StationID: f.station.ID, MinimumLeadTime: 90 * time.Minute,
		PrePassBuffer: time.Minute, PostPassBuffer: time.Minute,
		RecordingPreRoll: 5 * time.Second, RecordingPostRoll: 5 * time.Second,
		MinimumElevationDegrees: 15, CreatedBy: &f.user.ID,
		EffectiveFrom: now.Add(90 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create future config: %v", err)
	}

	// Right now the old configuration is still the effective one.
	current, err := repo.LatestEffectiveSchedulingConfig(ctx, f.station.ID, now)
	if err != nil {
		t.Fatalf("current config: %v", err)
	}
	if current.ID != f.config.ID {
		t.Errorf("effective config = %v, want the original %v", current.ID, f.config.ID)
	}

	// After the delay the new configuration takes over.
	later, err := repo.LatestEffectiveSchedulingConfig(ctx, f.station.ID, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("later config: %v", err)
	}
	if later.ID != future.ID {
		t.Errorf("effective config = %v, want the new %v", later.ID, future.ID)
	}
	if later.MinimumLeadTime != 90*time.Minute {
		t.Errorf("MinimumLeadTime = %v, want 90m", later.MinimumLeadTime)
	}
}

// The TLE we planned with must remain readable after a newer one arrives.
func TestNewTLEDoesNotDisturbHistoricalPass(t *testing.T) {
	ctx := context.Background()
	_, repo := newTestPool(t)
	f := newFixture(t, ctx, repo, "tle")

	pass, err := repo.CreatePass(ctx, f.passAt(time.Now().UTC().Add(12*time.Hour), 10*time.Minute))
	if err != nil {
		t.Fatalf("create pass: %v", err)
	}

	newer, err := repo.CreateTLERecord(ctx, domain.TLERecord{
		SatelliteID: f.satellite.ID,
		Line1:       "1 25544U 98067A   24002.50000000  .00016717  00000-0  10270-3 0  9016",
		Line2:       "2 25544  51.6416 247.4627 0006703 130.5360 325.0288 15.49815310 10011",
		Epoch:       time.Now().UTC(), Source: domain.SourceCelestrak,
	})
	if err != nil {
		t.Fatalf("create newer tle: %v", err)
	}

	latest, err := repo.LatestTLERecord(ctx, f.satellite.ID)
	if err != nil || latest.ID != newer.ID {
		t.Fatalf("LatestTLERecord = %v, %v; want newer", latest.ID, err)
	}

	readBack, err := repo.GetPassByID(ctx, pass.ID)
	if err != nil {
		t.Fatalf("GetPassByID: %v", err)
	}
	if readBack.TLERecordID != f.tle.ID {
		t.Errorf("pass TLE = %v, want the original %v", readBack.TLERecordID, f.tle.ID)
	}
}

func TestDuplicateTLEIsNotStoredTwice(t *testing.T) {
	ctx := context.Background()
	_, repo := newTestPool(t)
	f := newFixture(t, ctx, repo, "dupetle")

	_, err := repo.CreateTLERecord(ctx, domain.TLERecord{
		SatelliteID: f.satellite.ID, Line1: f.tle.Line1, Line2: f.tle.Line2,
		Epoch: f.tle.Epoch, Source: domain.SourceSatNOGS,
	})
	if !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("re-fetch error = %v, want ErrDuplicate", err)
	}
}

func TestUnknownRecordIsNotFound(t *testing.T) {
	ctx := context.Background()
	_, repo := newTestPool(t)

	if _, err := repo.GetPassByID(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// A change saved later must not be replaced by an earlier one that happens to
// become effective afterwards.
//
// Saving a long lead time and then thinking better of it leaves two pending
// changes. The short one becomes effective first; when the long one's moment
// arrives it must not come back to life and undo the operator's latest
// decision.
func TestASupersededConfigurationDoesNotReturn(t *testing.T) {
	ctx := context.Background()
	_, repo := newTestPool(t)
	f := newFixture(t, ctx, repo, "superseded")

	now := time.Now().UTC()

	// Saved first, effective last.
	if _, err := repo.CreateSchedulingConfig(ctx, domain.SchedulingConfig{
		StationID: f.station.ID, MinimumLeadTime: 90 * time.Minute,
		PrePassBuffer: time.Minute, PostPassBuffer: time.Minute,
		RecordingPreRoll: 5 * time.Second, RecordingPostRoll: 5 * time.Second,
		MinimumElevationDegrees: 15, CreatedBy: &f.user.ID,
		EffectiveFrom: now.Add(90 * time.Minute),
	}); err != nil {
		t.Fatalf("create long config: %v", err)
	}

	// Saved second, effective sooner. This is the operator's latest intent.
	latest, err := repo.CreateSchedulingConfig(ctx, domain.SchedulingConfig{
		StationID: f.station.ID, MinimumLeadTime: 10 * time.Minute,
		PrePassBuffer: time.Minute, PostPassBuffer: time.Minute,
		RecordingPreRoll: 5 * time.Second, RecordingPostRoll: 5 * time.Second,
		MinimumElevationDegrees: 12, CreatedBy: &f.user.ID,
		EffectiveFrom: now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create short config: %v", err)
	}

	// Once both are effective, the one saved last still governs.
	current, err := repo.LatestEffectiveSchedulingConfig(ctx, f.station.ID, now.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("current config: %v", err)
	}
	if current.ID != latest.ID {
		t.Errorf("effective config = %v, want the latest saved %v", current.ID, latest.ID)
	}
	if current.MinimumLeadTime != 10*time.Minute {
		t.Errorf("MinimumLeadTime = %v, want 10m", current.MinimumLeadTime)
	}
}

// A change that has not taken effect is still worth reporting, so an operator
// can see that their save landed.
func TestPendingConfigurationIsVisible(t *testing.T) {
	ctx := context.Background()
	_, repo := newTestPool(t)
	f := newFixture(t, ctx, repo, "pending")

	now := time.Now().UTC()
	if _, err := repo.PendingSchedulingConfig(ctx, f.station.ID, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("pending before any change = %v, want not found", err)
	}

	saved, err := repo.CreateSchedulingConfig(ctx, domain.SchedulingConfig{
		StationID: f.station.ID, MinimumLeadTime: 45 * time.Minute,
		PrePassBuffer: time.Minute, PostPassBuffer: time.Minute,
		RecordingPreRoll: 5 * time.Second, RecordingPostRoll: 5 * time.Second,
		MinimumElevationDegrees: 11, CreatedBy: &f.user.ID,
		EffectiveFrom: now.Add(45 * time.Minute),
	})
	if err != nil {
		t.Fatalf("create config: %v", err)
	}

	pending, err := repo.PendingSchedulingConfig(ctx, f.station.ID, now)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if pending.ID != saved.ID {
		t.Errorf("pending = %v, want %v", pending.ID, saved.ID)
	}

	// Once it is in force it is no longer pending.
	if _, err := repo.PendingSchedulingConfig(ctx, f.station.ID, now.Add(time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("pending after it applies = %v, want not found", err)
	}
}

// A pending change that a later, already-effective change has superseded is
// not waiting for anything: it will never apply, and saying otherwise would
// mislead the operator who saved the later one.
func TestASupersededPendingChangeIsNotReported(t *testing.T) {
	ctx := context.Background()
	_, repo := newTestPool(t)
	f := newFixture(t, ctx, repo, "supersededpending")

	now := time.Now().UTC()

	// Relaxation, saved first, still waiting.
	if _, err := repo.CreateSchedulingConfig(ctx, domain.SchedulingConfig{
		StationID: f.station.ID, MinimumLeadTime: time.Minute,
		PrePassBuffer: time.Minute, PostPassBuffer: time.Minute,
		RecordingPreRoll: 5 * time.Second, RecordingPostRoll: 5 * time.Second,
		MinimumElevationDegrees: 10, CreatedBy: &f.user.ID,
		EffectiveFrom: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("create relaxation: %v", err)
	}

	// Tightening, saved second, effective at once.
	tightened, err := repo.CreateSchedulingConfig(ctx, domain.SchedulingConfig{
		StationID: f.station.ID, MinimumLeadTime: time.Hour,
		PrePassBuffer: time.Minute, PostPassBuffer: time.Minute,
		RecordingPreRoll: 5 * time.Second, RecordingPostRoll: 5 * time.Second,
		MinimumElevationDegrees: 15, CreatedBy: &f.user.ID,
		EffectiveFrom: now,
	})
	if err != nil {
		t.Fatalf("create tightening: %v", err)
	}

	if _, err := repo.PendingSchedulingConfig(ctx, f.station.ID, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("pending = %v, want none: the relaxation was superseded", err)
	}

	// And it stays superseded once its moment passes.
	current, err := repo.LatestEffectiveSchedulingConfig(ctx, f.station.ID, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if current.ID != tightened.ID {
		t.Errorf("effective = %v, want the tightening %v", current.ID, tightened.ID)
	}
}
