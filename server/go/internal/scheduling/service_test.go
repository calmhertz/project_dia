package scheduling_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aagasa/internal/domain"
	"aagasa/internal/prediction"
	"aagasa/internal/scheduling"
	"aagasa/internal/store"
	"aagasa/internal/testsupport"
)

// The scheduler is tested without the Worker or any hardware, which is the
// V7 exit criterion.

const argon2idHash = "$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHR2YWx1ZQ$aGFzaHZhbHVlaGFzaHZhbHVlaGFzaHZhbA"

const (
	issLine1 = "1 25544U 98067A   26236.17729445  .00008773  00000+0  16369-3 0  9990"
	issLine2 = "2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// fakePredictor returns scripted passes, so scheduling is tested without
// depending on the prediction service being up.
type fakePredictor struct {
	mu     sync.Mutex
	passes []prediction.Pass
	err    error
	calls  int
}

func (f *fakePredictor) PredictPasses(ctx context.Context, request prediction.Request) (prediction.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return prediction.Result{}, f.err
	}
	// Mimic the real service: return only passes inside the search window.
	var inWindow []prediction.Pass
	for _, candidate := range f.passes {
		if !candidate.AOS.Before(request.SearchStart) && !candidate.AOS.After(request.SearchEnd) {
			inWindow = append(inWindow, candidate)
		}
	}
	return prediction.Result{NoradID: 25544, Passes: inWindow}, nil
}

func passAt(aos time.Time, duration time.Duration) prediction.Pass {
	return prediction.Pass{
		AOS: aos, TCA: aos.Add(duration / 2), LOS: aos.Add(duration),
		AOSAzimuthDegrees: 190, TCAAzimuthDegrees: 130, LOSAzimuthDegrees: 60,
		MaxElevationDegrees: 31, Duration: duration,
	}
}

// world is a fully configured station with everything a pass needs.
type world struct {
	repo      *store.Repository
	service   *scheduling.Service
	predictor *fakePredictor

	station   domain.Station
	satellite domain.Satellite
	worker    domain.Worker
	config    domain.SchedulingConfig

	user  domain.User
	other domain.User
	admin domain.User
	root  domain.User
}

func newWorld(t *testing.T, leadTime time.Duration) *world {
	t.Helper()
	ctx := context.Background()

	url := testsupport.NewDatabase(t)
	if err := store.MigrateUp(ctx, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	repo := store.NewRepository(pool)
	predictor := &fakePredictor{}
	service := scheduling.NewService(repo, predictor, nil, discardLogger())

	w := &world{repo: repo, service: service, predictor: predictor}

	w.user = mustUser(t, repo, "requester", domain.RoleUser)
	w.other = mustUser(t, repo, "other", domain.RoleUser)
	w.admin = mustUser(t, repo, "approver", domain.RoleAdmin)
	w.root = mustUser(t, repo, "root", domain.RoleRoot)

	w.station, err = repo.CreateStation(ctx, domain.Station{
		Name: "SJCIT", Latitude: 13.394944, Longitude: 77.729444,
		AltitudeM: 915, Timezone: "UTC", ActiveRFBand: domain.BandVHF,
	})
	if err != nil {
		t.Fatalf("create station: %v", err)
	}

	w.config, err = repo.CreateSchedulingConfig(ctx, domain.SchedulingConfig{
		StationID: w.station.ID, MinimumLeadTime: leadTime,
		PrePassBuffer: 2 * time.Minute, PostPassBuffer: 2 * time.Minute,
		RecordingPreRoll: 10 * time.Second, RecordingPostRoll: 10 * time.Second,
		MinimumElevationDegrees: 10,
		// Already effective, so scheduling can happen immediately.
		EffectiveFrom: time.Now().UTC().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("create scheduling config: %v", err)
	}

	w.satellite, err = repo.CreateSatellite(ctx, domain.Satellite{
		NoradID: 25544, Name: "ISS (ZARYA)", IsSchedulable: true,
	})
	if err != nil {
		t.Fatalf("create satellite: %v", err)
	}
	if _, err := repo.CreateTLERecord(ctx, domain.TLERecord{
		SatelliteID: w.satellite.ID, Line1: issLine1, Line2: issLine2,
		Epoch: time.Now().UTC().Add(-6 * time.Hour), Source: domain.SourceCelestrak,
	}); err != nil {
		t.Fatalf("create tle: %v", err)
	}

	w.worker, err = repo.CreateWorker(ctx, domain.Worker{
		StationID: w.station.ID, Name: "worker-1", ConnectionState: domain.WorkerOnline,
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	return w
}

func mustUser(t *testing.T, repo *store.Repository, username string, role domain.UserRole) domain.User {
	t.Helper()
	user, err := repo.CreateUser(context.Background(), domain.User{
		Username: username, PasswordHash: argon2idHash, Role: role,
	})
	if err != nil {
		t.Fatalf("create %s: %v", username, err)
	}
	return user
}

func (w *world) request(actor domain.User, aos time.Time) scheduling.RequestInput {
	return scheduling.RequestInput{
		SatelliteID: w.satellite.ID, RequestedAOS: aos,
		Band: domain.BandVHF, RecordingMode: domain.RecordRaw,
		Visibility: domain.VisibilityPrivate,
	}
}

// Requesting ---------------------------------------------------------------

func TestRequestPassCreatesAPendingPass(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	w.predictor.passes = []prediction.Pass{passAt(aos, 10*time.Minute)}

	result, err := w.service.RequestPass(ctx, w.user, w.request(w.user, aos))
	if err != nil {
		t.Fatalf("RequestPass: %v", err)
	}

	if result.Pass.Status != domain.PassPendingApproval {
		t.Errorf("status = %s, want pending_approval", result.Pass.Status)
	}
	if result.Pass.RequestedBy != w.user.ID {
		t.Error("pass is not attributed to the requester")
	}
	// The reservation is the pass widened by the configured buffers.
	if !result.Pass.ReservedFrom.Equal(aos.Add(-2 * time.Minute)) {
		t.Errorf("ReservedFrom = %s, want AOS minus 2m", result.Pass.ReservedFrom)
	}
	if !result.Pass.ReservedTo.Equal(aos.Add(12 * time.Minute)) {
		t.Errorf("ReservedTo = %s, want LOS plus 2m", result.Pass.ReservedTo)
	}
	// Recording margins are separate from the reservation buffers.
	if result.Pass.RecordingPreRoll != 10*time.Second {
		t.Errorf("RecordingPreRoll = %v, want 10s", result.Pass.RecordingPreRoll)
	}
}

// The Server computes pass timings; a client cannot dictate them.
func TestClientSuppliedTimingIsNotTrusted(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	realAOS := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	w.predictor.passes = []prediction.Pass{passAt(realAOS, 10*time.Minute)}

	// Ask using a time a little off from the real pass; within tolerance it
	// still resolves, but to the predicted numbers.
	result, err := w.service.RequestPass(ctx, w.user, w.request(w.user, realAOS.Add(30*time.Second)))
	if err != nil {
		t.Fatalf("RequestPass: %v", err)
	}
	if !result.Pass.AOSAt.Equal(realAOS) {
		t.Errorf("AOS = %s, want the predicted %s", result.Pass.AOSAt, realAOS)
	}
}

func TestRequestingAPassThatDoesNotExistIsRefused(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)
	w.predictor.passes = []prediction.Pass{
		passAt(time.Now().UTC().Add(4*time.Hour), 10*time.Minute),
	}

	// Hours away from any predicted pass.
	_, err := w.service.RequestPass(ctx, w.user, w.request(w.user, time.Now().UTC().Add(10*time.Hour)))
	if !errors.Is(err, scheduling.ErrPassNotFound) {
		t.Errorf("error = %v, want ErrPassNotFound", err)
	}
}

func TestUnschedulableSatelliteIsRefused(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)
	if _, err := w.repo.UpdateSatellite(ctx, w.satellite.ID, "ISS", "", false); err != nil {
		t.Fatalf("update satellite: %v", err)
	}

	aos := time.Now().UTC().Add(4 * time.Hour)
	w.predictor.passes = []prediction.Pass{passAt(aos, 10*time.Minute)}

	if _, err := w.service.RequestPass(ctx, w.user, w.request(w.user, aos)); !errors.Is(err, scheduling.ErrNotSchedulable) {
		t.Errorf("error = %v, want ErrNotSchedulable", err)
	}
}

// Overlap ------------------------------------------------------------------

// spec.md section 13.3: any overlap on the same station conflicts, whatever
// the satellite, band or user.
func TestOverlappingRequestIsRejected(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	first := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	// 10:00-10:15 and 10:10-10:20, the spec's example.
	w.predictor.passes = []prediction.Pass{
		passAt(first, 15*time.Minute),
		passAt(first.Add(10*time.Minute), 10*time.Minute),
	}

	if _, err := w.service.RequestPass(ctx, w.user, w.request(w.user, first)); err != nil {
		t.Fatalf("first request: %v", err)
	}

	// A different user, and it still conflicts: the station is one resource.
	_, err := w.service.RequestPass(ctx, w.other, w.request(w.other, first.Add(10*time.Minute)))
	if !errors.Is(err, scheduling.ErrStationConflict) {
		t.Fatalf("error = %v, want ErrStationConflict", err)
	}

	// The conflict message exposes the occupied window and nothing else.
	var conflict *scheduling.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error is not a ConflictError: %v", err)
	}
	if conflict.OccupiedFrom.IsZero() || conflict.OccupiedTo.IsZero() {
		t.Error("the conflict does not report the occupied window")
	}
}

// spec.md section 15 and global test property 14: a conflict must not reveal
// the other user.
func TestConflictDoesNotLeakTheOtherUser(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	aos := time.Now().UTC().Add(4 * time.Hour)
	w.predictor.passes = []prediction.Pass{passAt(aos, 15*time.Minute)}

	if _, err := w.service.RequestPass(ctx, w.user, w.request(w.user, aos)); err != nil {
		t.Fatalf("first request: %v", err)
	}
	_, err := w.service.RequestPass(ctx, w.other, w.request(w.other, aos))
	if err == nil {
		t.Fatal("expected a conflict")
	}

	message := err.Error()
	for _, secret := range []string{w.user.Username, w.user.ID.String()} {
		if strings.Contains(message, secret) {
			t.Errorf("conflict message leaks %q: %s", secret, message)
		}
	}
}

func TestNonOverlappingPassesAreBothAccepted(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	first := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	// Second pass starts well after the first reservation ends.
	second := first.Add(40 * time.Minute)
	w.predictor.passes = []prediction.Pass{
		passAt(first, 10*time.Minute), passAt(second, 10*time.Minute),
	}

	if _, err := w.service.RequestPass(ctx, w.user, w.request(w.user, first)); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := w.service.RequestPass(ctx, w.other, w.request(w.other, second)); err != nil {
		t.Errorf("second, non-overlapping request rejected: %v", err)
	}
}

// Global test property 2: concurrency cannot bypass the conflict rule.
func TestConcurrentRequestsCannotRaceThroughTheConflictCheck(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	w.predictor.passes = []prediction.Pass{passAt(aos, 10*time.Minute)}

	const attempts = 10
	var waitGroup sync.WaitGroup
	results := make(chan error, attempts)

	for i := 0; i < attempts; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, err := w.service.RequestPass(ctx, w.user, w.request(w.user, aos))
			results <- err
		}()
	}
	waitGroup.Wait()
	close(results)

	accepted, conflicted := 0, 0
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, scheduling.ErrStationConflict):
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

// Lead time ----------------------------------------------------------------

func TestLeadTimeBlocksAPassStartingTooSoon(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 2*time.Hour)

	soon := time.Now().UTC().Add(30 * time.Minute)
	w.predictor.passes = []prediction.Pass{passAt(soon, 10*time.Minute)}

	if _, err := w.service.RequestPass(ctx, w.user, w.request(w.user, soon)); !errors.Is(err, scheduling.ErrLeadTime) {
		t.Errorf("error = %v, want ErrLeadTime", err)
	}
}

// Global test property 4: Root does not get to skip the lead-time rule.
func TestRootCannotBypassTheLeadTime(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 2*time.Hour)

	soon := time.Now().UTC().Add(30 * time.Minute)
	w.predictor.passes = []prediction.Pass{passAt(soon, 10*time.Minute)}

	if _, err := w.service.RequestPass(ctx, w.root, w.request(w.root, soon)); !errors.Is(err, scheduling.ErrLeadTime) {
		t.Errorf("root plain request error = %v, want ErrLeadTime", err)
	}
	// Not even through the override path, which bypasses conflicts only.
	if _, err := w.service.RequestPassWithRootOverride(ctx, w.root, w.request(w.root, soon)); !errors.Is(err, scheduling.ErrLeadTime) {
		t.Errorf("root override error = %v, want ErrLeadTime", err)
	}
}

// Global test property 5: changing the lead time cannot bypass the wait.
func TestShortenedLeadTimeIsNotUsableImmediately(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 4*time.Hour)

	// Root shortens the lead time to five minutes.
	saved, err := w.service.UpdateSchedulingConfig(ctx, w.root, domain.SchedulingConfig{
		MinimumLeadTime: 5 * time.Minute,
		PrePassBuffer:   2 * time.Minute, PostPassBuffer: 2 * time.Minute,
		RecordingPreRoll: 10 * time.Second, RecordingPostRoll: 10 * time.Second,
		MinimumElevationDegrees: 10,
	})
	if err != nil {
		t.Fatalf("UpdateSchedulingConfig: %v", err)
	}
	if !saved.EffectiveFrom.After(time.Now().UTC()) {
		t.Error("the new configuration became effective immediately")
	}

	// A pass 30 minutes out would be legal under the new value but not the
	// old one, and the old one is still in force.
	soon := time.Now().UTC().Add(30 * time.Minute)
	w.predictor.passes = []prediction.Pass{passAt(soon, 10*time.Minute)}

	if _, err := w.service.RequestPass(ctx, w.root, w.request(w.root, soon)); !errors.Is(err, scheduling.ErrLeadTime) {
		t.Errorf("error = %v, want the old lead time still enforced", err)
	}
}

func TestOnlyRootCanChangeSchedulingConfiguration(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, time.Hour)

	for _, actor := range []domain.User{w.user, w.admin} {
		_, err := w.service.UpdateSchedulingConfig(ctx, actor, domain.SchedulingConfig{
			MinimumLeadTime: time.Hour, MinimumElevationDegrees: 10,
		})
		if !errors.Is(err, scheduling.ErrForbidden) {
			t.Errorf("%s changing config: error = %v, want ErrForbidden", actor.Role, err)
		}
	}
}

// Approval -----------------------------------------------------------------

func TestAdminApprovesAndRejects(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	aos := time.Now().UTC().Add(4 * time.Hour)
	w.predictor.passes = []prediction.Pass{passAt(aos, 10*time.Minute)}
	result, err := w.service.RequestPass(ctx, w.user, w.request(w.user, aos))
	if err != nil {
		t.Fatalf("RequestPass: %v", err)
	}

	approved, err := w.service.Approve(ctx, w.admin, result.Pass.ID)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approved.Status != domain.PassApproved {
		t.Errorf("status = %s, want approved", approved.Status)
	}
	if approved.ApprovedBy == nil || *approved.ApprovedBy != w.admin.ID {
		t.Error("the approver is not recorded")
	}

	// Approving twice is refused by the state machine.
	if _, err := w.service.Approve(ctx, w.admin, result.Pass.ID); err == nil {
		t.Error("approving an approved pass must be refused")
	}
}

func TestNormalUserCannotApprove(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	aos := time.Now().UTC().Add(4 * time.Hour)
	w.predictor.passes = []prediction.Pass{passAt(aos, 10*time.Minute)}
	result, err := w.service.RequestPass(ctx, w.user, w.request(w.user, aos))
	if err != nil {
		t.Fatalf("RequestPass: %v", err)
	}

	// Not even their own pass.
	if _, err := w.service.Approve(ctx, w.user, result.Pass.ID); !errors.Is(err, scheduling.ErrForbidden) {
		t.Errorf("error = %v, want ErrForbidden", err)
	}
}

func TestRejectedPassReleasesTheStation(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	w.predictor.passes = []prediction.Pass{passAt(aos, 10*time.Minute)}

	first, err := w.service.RequestPass(ctx, w.user, w.request(w.user, aos))
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	if _, err := w.service.Reject(ctx, w.admin, first.Pass.ID); err != nil {
		t.Fatalf("Reject: %v", err)
	}

	// The slot is free again.
	if _, err := w.service.RequestPass(ctx, w.other, w.request(w.other, aos)); err != nil {
		t.Errorf("slot not released after rejection: %v", err)
	}
}

// Cancellation -------------------------------------------------------------

func TestOwnerCancelsAndSlotIsReleased(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	w.predictor.passes = []prediction.Pass{passAt(aos, 10*time.Minute)}

	result, err := w.service.RequestPass(ctx, w.user, w.request(w.user, aos))
	if err != nil {
		t.Fatalf("RequestPass: %v", err)
	}

	cancelled, err := w.service.Cancel(ctx, w.user, result.Pass.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if cancelled.Status != domain.PassCancelled {
		t.Errorf("status = %s, want cancelled", cancelled.Status)
	}
	if cancelled.CancellationReason == nil || *cancelled.CancellationReason != domain.CancelledByOwner {
		t.Error("the cancellation reason is not recorded as owner")
	}

	if _, err := w.service.RequestPass(ctx, w.other, w.request(w.other, aos)); err != nil {
		t.Errorf("slot not released after cancellation: %v", err)
	}
}

func TestStrangerCannotCancelSomeoneElsesPass(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	aos := time.Now().UTC().Add(4 * time.Hour)
	w.predictor.passes = []prediction.Pass{passAt(aos, 10*time.Minute)}
	result, err := w.service.RequestPass(ctx, w.user, w.request(w.user, aos))
	if err != nil {
		t.Fatalf("RequestPass: %v", err)
	}

	if _, err := w.service.Cancel(ctx, w.other, result.Pass.ID); !errors.Is(err, scheduling.ErrForbidden) {
		t.Errorf("error = %v, want ErrForbidden", err)
	}
}

// Root override ------------------------------------------------------------

// Global test property 6 and spec.md section 13.7.
func TestRootOverrideCancelsTheConflictingPassAndKeepsItAuditable(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	w.predictor.passes = []prediction.Pass{passAt(aos, 10*time.Minute)}

	victim, err := w.service.RequestPass(ctx, w.user, w.request(w.user, aos))
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	if _, err := w.service.Approve(ctx, w.admin, victim.Pass.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	result, err := w.service.RequestPassWithRootOverride(ctx, w.root, w.request(w.root, aos))
	if err != nil {
		t.Fatalf("RequestPassWithRootOverride: %v", err)
	}

	// Root's pass is live and already approved.
	if result.Pass.Status != domain.PassApproved {
		t.Errorf("root pass status = %s, want approved", result.Pass.Status)
	}
	if len(result.OverriddenPassIDs) != 1 || result.OverriddenPassIDs[0] != victim.Pass.ID {
		t.Errorf("overridden = %v, want the first pass", result.OverriddenPassIDs)
	}

	// The displaced pass still exists, cancelled with the override reason.
	displaced, err := w.repo.GetPassByID(ctx, victim.Pass.ID)
	if err != nil {
		t.Fatalf("the overridden pass was deleted: %v", err)
	}
	if displaced.Status != domain.PassCancelled {
		t.Errorf("displaced status = %s, want cancelled", displaced.Status)
	}
	if displaced.CancellationReason == nil ||
		*displaced.CancellationReason != domain.CancelledByRootOverride {
		t.Errorf("displaced reason = %v, want cancelled_by_root_override", displaced.CancellationReason)
	}

	// And it is auditable.
	count, err := w.repo.CountAuditRecords(ctx, "pass", victim.Pass.ID.String())
	if err != nil {
		t.Fatalf("CountAuditRecords: %v", err)
	}
	if count < 2 {
		t.Errorf("audit records = %d, want the request and the override", count)
	}
}

func TestOnlyRootCanOverride(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	aos := time.Now().UTC().Add(4 * time.Hour)
	w.predictor.passes = []prediction.Pass{passAt(aos, 10*time.Minute)}

	for _, actor := range []domain.User{w.user, w.admin} {
		if _, err := w.service.RequestPassWithRootOverride(ctx, actor, w.request(actor, aos)); !errors.Is(err, scheduling.ErrForbidden) {
			t.Errorf("%s override: error = %v, want ErrForbidden", actor.Role, err)
		}
	}
}

func TestRootOverrideWithNoConflictJustSchedules(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	aos := time.Now().UTC().Add(4 * time.Hour)
	w.predictor.passes = []prediction.Pass{passAt(aos, 10*time.Minute)}

	result, err := w.service.RequestPassWithRootOverride(ctx, w.root, w.request(w.root, aos))
	if err != nil {
		t.Fatalf("RequestPassWithRootOverride: %v", err)
	}
	if len(result.OverriddenPassIDs) != 0 {
		t.Errorf("overridden = %v, want none", result.OverriddenPassIDs)
	}
}

// Worker availability ------------------------------------------------------

// spec.md section 6.3 and global test property 12.
func TestOfflineWorkerBlocksNewScheduling(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	if err := w.repo.SetWorkerConnectionState(ctx, w.worker.ID, domain.WorkerOffline, nil); err != nil {
		t.Fatalf("set worker offline: %v", err)
	}

	aos := time.Now().UTC().Add(4 * time.Hour)
	w.predictor.passes = []prediction.Pass{passAt(aos, 10*time.Minute)}

	if _, err := w.service.RequestPass(ctx, w.user, w.request(w.user, aos)); !errors.Is(err, scheduling.ErrWorkerOffline) {
		t.Errorf("error = %v, want ErrWorkerOffline", err)
	}
	// Root is not exempt: the station genuinely cannot execute.
	if _, err := w.service.RequestPassWithRootOverride(ctx, w.root, w.request(w.root, aos)); !errors.Is(err, scheduling.ErrWorkerOffline) {
		t.Errorf("root override error = %v, want ErrWorkerOffline", err)
	}
}

// Global test property 13: switching the station band must not destroy
// existing approved passes.
func TestChangingTheStationBandDoesNotDisturbApprovedPasses(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	aos := time.Now().UTC().Add(4 * time.Hour)
	w.predictor.passes = []prediction.Pass{passAt(aos, 10*time.Minute)}
	result, err := w.service.RequestPass(ctx, w.user, w.request(w.user, aos))
	if err != nil {
		t.Fatalf("RequestPass: %v", err)
	}
	if _, err := w.service.Approve(ctx, w.admin, result.Pass.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// The station switches to the other band.
	if err := w.repo.SetStationActiveBand(ctx, w.station.ID, domain.BandUHF); err != nil {
		t.Fatalf("SetStationActiveBand: %v", err)
	}

	after, err := w.repo.GetPassByID(ctx, result.Pass.ID)
	if err != nil {
		t.Fatalf("GetPassByID: %v", err)
	}
	if after.Status != domain.PassApproved {
		t.Errorf("status = %s, want the pass untouched at approved", after.Status)
	}
	if after.Band != domain.BandVHF {
		t.Errorf("band = %s, want the pass to keep its own band", after.Band)
	}
}

// A mismatch warns, it does not block (spec.md section 8).
func TestBandMismatchWarnsButStillSchedules(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	aos := time.Now().UTC().Add(4 * time.Hour)
	w.predictor.passes = []prediction.Pass{passAt(aos, 10*time.Minute)}

	// The station is VHF; ask for a UHF pass.
	input := w.request(w.user, aos)
	input.Band = domain.BandUHF

	result, err := w.service.RequestPass(ctx, w.user, input)
	if err != nil {
		t.Fatalf("a band mismatch must not block scheduling: %v", err)
	}
	if len(result.Warnings) != 1 || result.Warnings[0].Code != "band_mismatch" {
		t.Errorf("warnings = %+v, want one band_mismatch", result.Warnings)
	}
	if result.Pass.Status != domain.PassPendingApproval {
		t.Errorf("status = %s, want the pass accepted", result.Pass.Status)
	}
}

// Prediction failure -------------------------------------------------------

func TestPredictionOutageDoesNotCreateAPass(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)
	w.predictor.err = errors.New("prediction service unavailable")

	aos := time.Now().UTC().Add(4 * time.Hour)
	if _, err := w.service.RequestPass(ctx, w.user, w.request(w.user, aos)); err == nil {
		t.Fatal("expected an error when prediction is down")
	}

	passes, err := w.repo.ListPasses(ctx, store.PassFilter{})
	if err != nil {
		t.Fatalf("ListPasses: %v", err)
	}
	if len(passes) != 0 {
		t.Errorf("passes = %d, want none created during an outage", len(passes))
	}
}

func TestListPassesFiltersByOwnerAndStatus(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, 30*time.Minute)

	first := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	second := first.Add(40 * time.Minute)
	w.predictor.passes = []prediction.Pass{
		passAt(first, 10*time.Minute), passAt(second, 10*time.Minute),
	}

	mine, err := w.service.RequestPass(ctx, w.user, w.request(w.user, first))
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := w.service.RequestPass(ctx, w.other, w.request(w.other, second)); err != nil {
		t.Fatalf("second: %v", err)
	}

	owned, err := w.repo.ListPasses(ctx, store.PassFilter{RequestedBy: &w.user.ID})
	if err != nil {
		t.Fatalf("ListPasses: %v", err)
	}
	if len(owned) != 1 || owned[0].ID != mine.Pass.ID {
		t.Errorf("owner filter returned %d passes", len(owned))
	}

	pending, err := w.repo.ListPasses(ctx, store.PassFilter{
		Statuses: []domain.PassStatus{domain.PassPendingApproval},
	})
	if err != nil {
		t.Fatalf("ListPasses: %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("pending = %d, want 2", len(pending))
	}
}
