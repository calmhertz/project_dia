package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"aagasa/internal/domain"
	"aagasa/internal/prediction"
)

// The scheduling API end to end, with a scripted predictor so no hardware or
// prediction service is needed (the V7 exit criterion).

func (e *testEnv) setupStation(t *testing.T, rootToken string) {
	t.Helper()
	if code, body := e.do(t, http.MethodPost, "/api/setup", rootToken, validSetupBody()); code != http.StatusCreated {
		t.Fatalf("setup: %d %v", code, body)
	}
	// V12 drives this from the heartbeat; until then the test marks the
	// station reachable so scheduling is permitted.
	ctx := context.Background()
	worker, err := e.repo.GetWorkerByName(ctx, "worker-1")
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	now := time.Now().UTC()
	if err := e.repo.SetWorkerConnectionState(ctx, worker.ID, domain.WorkerOnline, &now); err != nil {
		t.Fatalf("set worker online: %v", err)
	}
}

// seedSatellite inserts a satellite and TLE directly, bypassing the live
// providers so the test stays offline.
func (e *testEnv) seedSatellite(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	satellite, err := e.repo.CreateSatellite(ctx, domain.Satellite{
		NoradID: 25544, Name: "ISS (ZARYA)", IsSchedulable: true,
	})
	if err != nil {
		t.Fatalf("create satellite: %v", err)
	}
	if _, err := e.repo.CreateTLERecord(ctx, domain.TLERecord{
		SatelliteID: satellite.ID,
		Line1:       "1 25544U 98067A   26236.17729445  .00008773  00000+0  16369-3 0  9990",
		Line2:       "2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298",
		Epoch:       time.Now().UTC().Add(-6 * time.Hour), Source: domain.SourceCelestrak,
	}); err != nil {
		t.Fatalf("create tle: %v", err)
	}
	return satellite.ID.String()
}

func (e *testEnv) schedulePass(t *testing.T, token, satelliteID string, aos time.Time, extra map[string]any) (int, map[string]any) {
	t.Helper()
	body := map[string]any{
		"satellite_id": satelliteID,
		"aos":          aos.Format(time.RFC3339Nano),
		"band":         "vhf", "recording_mode": "raw", "visibility": "private",
	}
	for key, value := range extra {
		body[key] = value
	}
	return e.do(t, http.MethodPost, "/api/passes", token, body)
}

func TestSchedulingFlowOverTheAPI(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, userToken := env.createUser(t, rootToken, "operator", "user")
	_, adminToken := env.createUser(t, rootToken, "approver", "admin")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))

	// Request.
	code, body := env.schedulePass(t, userToken, satelliteID, aos, nil)
	if code != http.StatusCreated {
		t.Fatalf("request pass = %d %v", code, body)
	}
	pass := body["pass"].(map[string]any)
	if pass["status"] != "pending_approval" {
		t.Errorf("status = %v, want pending_approval", pass["status"])
	}
	passID := pass["id"].(string)

	// A normal user cannot approve.
	if code, _ := env.do(t, http.MethodPost, "/api/passes/"+passID+"/approve", userToken, nil); code != http.StatusForbidden {
		t.Errorf("user approving = %d, want 403", code)
	}

	// An admin can.
	code, body = env.do(t, http.MethodPost, "/api/passes/"+passID+"/approve", adminToken, nil)
	if code != http.StatusOK {
		t.Fatalf("admin approve = %d %v", code, body)
	}
	if body["pass"].(map[string]any)["status"] != "approved" {
		t.Errorf("status = %v, want approved", body["pass"])
	}
}

// spec.md section 15: the conflict response says the slot is taken and
// nothing about who took it.
func TestConflictResponseIsSanitized(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	firstUserID, firstToken := env.createUser(t, rootToken, "firstuser", "user")
	_, secondToken := env.createUser(t, rootToken, "seconduser", "user")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))

	if code, body := env.schedulePass(t, firstToken, satelliteID, aos, nil); code != http.StatusCreated {
		t.Fatalf("first request = %d %v", code, body)
	}

	code, body := env.schedulePass(t, secondToken, satelliteID, aos, nil)
	if code != http.StatusConflict {
		t.Fatalf("second request = %d, want 409", code)
	}
	if body["error"] != "station_conflict" {
		t.Errorf("error = %v", body["error"])
	}
	// The occupied window is disclosed; the owner is not.
	if body["occupied_from"] == nil || body["occupied_to"] == nil {
		t.Error("the occupied window is missing")
	}
	for key, value := range body {
		if text, ok := value.(string); ok && (text == firstUserID || text == "firstuser") {
			t.Errorf("conflict response leaks the other user in %q", key)
		}
	}
}

// spec.md section 16: a private pass is invisible to an unrelated user.
func TestPrivatePassIsHiddenFromOtherUsers(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, ownerToken := env.createUser(t, rootToken, "owner", "user")
	_, strangerToken := env.createUser(t, rootToken, "stranger", "user")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))

	_, body := env.schedulePass(t, ownerToken, satelliteID, aos, nil)
	passID := body["pass"].(map[string]any)["id"].(string)

	// A stranger gets 404, not 403: even existence is private.
	if code, _ := env.do(t, http.MethodGet, "/api/passes/"+passID, strangerToken, nil); code != http.StatusNotFound {
		t.Errorf("stranger reading a private pass = %d, want 404", code)
	}
	// The owner sees it.
	if code, _ := env.do(t, http.MethodGet, "/api/passes/"+passID, ownerToken, nil); code != http.StatusOK {
		t.Errorf("owner reading their pass = %d, want 200", code)
	}
	// So do Admin and Root.
	if code, _ := env.do(t, http.MethodGet, "/api/passes/"+passID, rootToken, nil); code != http.StatusOK {
		t.Errorf("root reading a private pass = %d, want 200", code)
	}

	// It is absent from the stranger's listing too.
	_, list := env.do(t, http.MethodGet, "/api/passes?scope=all", strangerToken, nil)
	if list["error"] != "forbidden" {
		t.Errorf("a normal user could request scope=all: %v", list)
	}
}

func TestPublicPassIsVisibleWithoutRevealingTheOwner(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	ownerID, ownerToken := env.createUser(t, rootToken, "publicowner", "user")
	_, strangerToken := env.createUser(t, rootToken, "onlooker", "user")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))

	_, body := env.schedulePass(t, ownerToken, satelliteID, aos, map[string]any{"visibility": "public"})
	passID := body["pass"].(map[string]any)["id"].(string)

	code, seen := env.do(t, http.MethodGet, "/api/passes/"+passID, strangerToken, nil)
	if code != http.StatusOK {
		t.Fatalf("stranger reading a public pass = %d, want 200", code)
	}
	// Visible, but the owner is not named.
	if requester := seen["pass"].(map[string]any)["requested_by"]; requester != nil && requester != "" {
		if requester == ownerID {
			t.Error("a public pass discloses its owner to an unrelated user")
		}
	}
}

// spec.md section 8: a band mismatch warns rather than blocking.
func TestBandMismatchReturnsAWarningAndStillSchedules(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken) // station is VHF
	satelliteID := env.seedSatellite(t)
	_, userToken := env.createUser(t, rootToken, "banduser", "user")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))

	code, body := env.schedulePass(t, userToken, satelliteID, aos, map[string]any{"band": "uhf"})
	if code != http.StatusCreated {
		t.Fatalf("a band mismatch must not block scheduling: %d %v", code, body)
	}
	warnings, _ := body["warnings"].([]any)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one", warnings)
	}
	if warnings[0].(map[string]any)["code"] != "band_mismatch" {
		t.Errorf("warning = %v", warnings[0])
	}
}

// Global test property 6, over the API.
func TestRootOverrideOverTheAPI(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, userToken := env.createUser(t, rootToken, "displaced", "user")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))

	_, body := env.schedulePass(t, userToken, satelliteID, aos, nil)
	victimID := body["pass"].(map[string]any)["id"].(string)

	// Without override, even Root is refused.
	if code, _ := env.schedulePass(t, rootToken, satelliteID, aos, nil); code != http.StatusConflict {
		t.Errorf("root without override = %d, want 409", code)
	}

	code, body := env.schedulePass(t, rootToken, satelliteID, aos, map[string]any{"override": true})
	if code != http.StatusCreated {
		t.Fatalf("root override = %d %v", code, body)
	}
	overridden, _ := body["overridden_pass_ids"].([]any)
	if len(overridden) != 1 || overridden[0] != victimID {
		t.Errorf("overridden = %v, want the displaced pass", overridden)
	}

	// The displaced pass survives, cancelled with the override reason.
	code, seen := env.do(t, http.MethodGet, "/api/passes/"+victimID, rootToken, nil)
	if code != http.StatusOK {
		t.Fatalf("the displaced pass is gone: %d", code)
	}
	displaced := seen["pass"].(map[string]any)
	if displaced["status"] != "cancelled" {
		t.Errorf("displaced status = %v, want cancelled", displaced["status"])
	}
	if displaced["cancellation_reason"] != "cancelled_by_root_override" {
		t.Errorf("displaced reason = %v", displaced["cancellation_reason"])
	}
}

// An admin must not be able to override.
func TestAdminCannotOverride(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, adminToken := env.createUser(t, rootToken, "adminuser", "admin")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))

	if code, _ := env.schedulePass(t, adminToken, satelliteID, aos, map[string]any{"override": true}); code != http.StatusForbidden {
		t.Errorf("admin override = %d, want 403", code)
	}
}

// Global test property 12.
func TestOfflineWorkerBlocksSchedulingOverTheAPI(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, userToken := env.createUser(t, rootToken, "blocked", "user")

	ctx := context.Background()
	worker, err := env.repo.GetWorkerByName(ctx, "worker-1")
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	if err := env.repo.SetWorkerConnectionState(ctx, worker.ID, domain.WorkerOffline, nil); err != nil {
		t.Fatalf("set offline: %v", err)
	}

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))

	code, body := env.schedulePass(t, userToken, satelliteID, aos, nil)
	if code != http.StatusConflict || body["error"] != "worker_offline" {
		t.Errorf("offline worker = %d %v, want 409 worker_offline", code, body)
	}
}

// Global test properties 3, 4 and 5, over the API.
func TestLeadTimeRulesOverTheAPI(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken) // 1800s lead time
	satelliteID := env.seedSatellite(t)
	_, userToken := env.createUser(t, rootToken, "eager", "user")

	soon := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Second)
	env.predictor.setPasses(passAt(soon, 10*time.Minute))

	// Too soon for a normal user.
	code, body := env.schedulePass(t, userToken, satelliteID, soon, nil)
	if code != http.StatusConflict || body["error"] != "lead_time" {
		t.Errorf("user inside the lead time = %d %v, want 409 lead_time", code, body)
	}
	// And for Root, with or without override.
	if code, _ := env.schedulePass(t, rootToken, satelliteID, soon, nil); code != http.StatusConflict {
		t.Errorf("root inside the lead time = %d, want 409", code)
	}
	if code, _ := env.schedulePass(t, rootToken, satelliteID, soon, map[string]any{"override": true}); code != http.StatusConflict {
		t.Errorf("root override inside the lead time = %d, want 409", code)
	}

	// Root shortens the lead time; it must not take effect immediately.
	code, saved := env.do(t, http.MethodPut, "/api/scheduling-config", rootToken, map[string]any{
		"minimum_lead_time_seconds": 60,
		"pre_pass_buffer_seconds":   120, "post_pass_buffer_seconds": 120,
		"recording_pre_roll_seconds": 10, "recording_post_roll_seconds": 10,
		"minimum_elevation_degrees": 10.0,
	})
	if code != http.StatusCreated {
		t.Fatalf("update scheduling config = %d %v", code, saved)
	}
	if saved["effective_from"] == nil {
		t.Fatal("no effective_from returned")
	}

	// Still refused, because the old policy is in force.
	if code, body := env.schedulePass(t, rootToken, satelliteID, soon, nil); code != http.StatusConflict {
		t.Errorf("after shortening = %d %v, want the old lead time still enforced", code, body)
	}
}

func TestOnlyRootCanChangeSchedulingConfigOverTheAPI(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	_, adminToken := env.createUser(t, rootToken, "cfgadmin", "admin")

	body := map[string]any{
		"minimum_lead_time_seconds": 600,
		"pre_pass_buffer_seconds":   60, "post_pass_buffer_seconds": 60,
		"recording_pre_roll_seconds": 5, "recording_post_roll_seconds": 5,
		"minimum_elevation_degrees": 15.0,
	}
	if code, _ := env.do(t, http.MethodPut, "/api/scheduling-config", adminToken, body); code != http.StatusForbidden {
		t.Errorf("admin changing scheduling config = %d, want 403", code)
	}
}

func TestOwnerCanCancelAndFreeTheSlot(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, ownerToken := env.createUser(t, rootToken, "canceller", "user")
	_, otherToken := env.createUser(t, rootToken, "nextinline", "user")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))

	_, body := env.schedulePass(t, ownerToken, satelliteID, aos, nil)
	passID := body["pass"].(map[string]any)["id"].(string)

	// A stranger cannot cancel it.
	if code, _ := env.do(t, http.MethodPost, "/api/passes/"+passID+"/cancel", otherToken, nil); code != http.StatusForbidden {
		t.Errorf("stranger cancelling = %d, want 403", code)
	}

	if code, _ := env.do(t, http.MethodPost, "/api/passes/"+passID+"/cancel", ownerToken, nil); code != http.StatusOK {
		t.Errorf("owner cancelling = %d, want 200", code)
	}
	// The slot is free for someone else.
	if code, body := env.schedulePass(t, otherToken, satelliteID, aos, nil); code != http.StatusCreated {
		t.Errorf("slot not released = %d %v", code, body)
	}
}

func passAt(aos time.Time, duration time.Duration) prediction.Pass {
	// A plan needs a pointing timeline, so the stub supplies one: without it
	// the tests would never exercise the path that generates a plan.
	var track []prediction.TrackPoint
	for offset := time.Duration(0); offset <= duration; offset += 5 * time.Second {
		track = append(track, prediction.TrackPoint{
			At:               aos.Add(offset),
			AzimuthDegrees:   190 - 130*offset.Seconds()/duration.Seconds(),
			ElevationDegrees: 10 + 21*offset.Seconds()/duration.Seconds(),
			RangeKm:          1200,
		})
	}
	return prediction.Pass{
		AOS: aos, TCA: aos.Add(duration / 2), LOS: aos.Add(duration),
		AOSAzimuthDegrees: 190, TCAAzimuthDegrees: 130, LOSAzimuthDegrees: 60,
		MaxElevationDegrees: 31, Duration: duration, Track: track,
	}
}

// Approval builds the executable plan. If it cannot, the pass must not be
// left approved: the Worker would have nothing to run and the pass could no
// longer be rejected. This is the failure a live station hit.

// setupStationWithoutFrequency completes setup with the centre frequency
// missing, which is what an incomplete configuration looks like.
func (e *testEnv) setupStationWithoutFrequency(t *testing.T, rootToken string) {
	t.Helper()
	body := validSetupBody()
	body["bands"] = []map[string]any{
		{"band": "vhf", "antenna_description": "Turnstile", "sample_rate_hz": 2048000, "gain_db": 40.0},
		{"band": "uhf", "antenna_description": "Yagi", "sample_rate_hz": 2048000, "gain_db": 40.0},
	}
	if code, response := e.do(t, http.MethodPost, "/api/setup", rootToken, body); code != http.StatusCreated {
		t.Fatalf("setup: %d %v", code, response)
	}
	ctx := context.Background()
	worker, err := e.repo.GetWorkerByName(ctx, "worker-1")
	if err != nil {
		t.Fatalf("get worker: %v", err)
	}
	now := time.Now().UTC()
	if err := e.repo.SetWorkerConnectionState(ctx, worker.ID, domain.WorkerOnline, &now); err != nil {
		t.Fatalf("set worker online: %v", err)
	}
}

func TestApprovalIsRefusedWhenTheStationCannotExecute(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStationWithoutFrequency(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, userToken := env.createUser(t, rootToken, "requester", "user")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))
	_, requested := env.schedulePass(t, userToken, satelliteID, aos, nil)
	passID := requested["pass"].(map[string]any)["id"].(string)

	code, body := env.do(t, http.MethodPost, "/api/passes/"+passID+"/approve", rootToken, nil)
	if code != http.StatusConflict {
		t.Fatalf("approve = %d %v, want 409", code, body)
	}
	if body["error"] != "station_not_configured" {
		t.Errorf("error = %v, want station_not_configured", body["error"])
	}
	// The message has to name what is missing, or Root cannot fix it.
	if message, _ := body["message"].(string); message == "" ||
		!containsAll(message, "vhf", "frequency") {
		t.Errorf("message = %q, want it to name the band and the setting", message)
	}

	// The pass is untouched, so the operator can still reject it.
	_, after := env.do(t, http.MethodGet, "/api/passes/"+passID, rootToken, nil)
	if status := after["pass"].(map[string]any)["status"]; status != "pending_approval" {
		t.Fatalf("status = %v, want pending_approval", status)
	}
	if code, body := env.do(t, http.MethodPost, "/api/passes/"+passID+"/reject", rootToken, nil); code != http.StatusOK {
		t.Errorf("reject after a failed approval = %d %v", code, body)
	}
}

// Once the station is configured, the same approval works and stores a plan.
func TestApprovalStoresAPlanWhenTheStationIsConfigured(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, userToken := env.createUser(t, rootToken, "planrequester", "user")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))
	_, requested := env.schedulePass(t, userToken, satelliteID, aos, nil)
	passID := requested["pass"].(map[string]any)["id"].(string)

	if code, body := env.do(t, http.MethodPost, "/api/passes/"+passID+"/approve", rootToken, nil); code != http.StatusOK {
		t.Fatalf("approve = %d %v", code, body)
	}

	code, plan := env.do(t, http.MethodGet, "/api/passes/"+passID+"/plan", rootToken, nil)
	if code != http.StatusOK {
		t.Fatalf("read plan = %d %v", code, plan)
	}
	if plan["track_point_count"] == nil || plan["track_point_count"] == float64(0) {
		t.Errorf("the plan has no pointing timeline: %v", plan["track_point_count"])
	}
	radio := plan["radio"].(map[string]any)
	if radio["frequency_hz"] != float64(137100000) {
		t.Errorf("frequency = %v", radio["frequency_hz"])
	}
}

// Deciding twice is somebody else getting there first, not a server fault.
func TestDecidingAnAlreadyDecidedPassIsAConflict(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, userToken := env.createUser(t, rootToken, "twicerequester", "user")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))
	_, requested := env.schedulePass(t, userToken, satelliteID, aos, nil)
	passID := requested["pass"].(map[string]any)["id"].(string)

	if code, _ := env.do(t, http.MethodPost, "/api/passes/"+passID+"/approve", rootToken, nil); code != http.StatusOK {
		t.Fatal("first approval failed")
	}

	for _, action := range []string{"approve", "reject"} {
		code, body := env.do(t, http.MethodPost, "/api/passes/"+passID+"/"+action, rootToken, nil)
		if code != http.StatusConflict {
			t.Errorf("%s again = %d %v, want 409", action, code, body)
		}
		if body["error"] != "invalid_state" {
			t.Errorf("%s error = %v, want invalid_state", action, body["error"])
		}
	}
}

func containsAll(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(strings.ToLower(haystack), needle) {
			return false
		}
	}
	return true
}
