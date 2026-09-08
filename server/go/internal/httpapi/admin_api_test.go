package httpapi_test

import (
	"net/http"
	"testing"

	"aagasa/internal/domain"
)

// The V14 admin read surface. Each of these is Admin-or-Root: an Admin needs
// to see how the station is operating, without being able to reconfigure it.

func TestStationStatusIsVisibleToAdminOnly(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	_, userToken := env.createUser(t, rootToken, "stationreader", "user")
	_, adminToken := env.createUser(t, rootToken, "stationadmin", "admin")

	if code, _ := env.do(t, http.MethodGet, "/api/station", userToken, nil); code != http.StatusForbidden {
		t.Errorf("user reading station status = %d, want 403", code)
	}

	code, body := env.do(t, http.MethodGet, "/api/station", adminToken, nil)
	if code != http.StatusOK {
		t.Fatalf("admin reading station status = %d %v", code, body)
	}
	if body["name"] != "SJCIT Ground Station" {
		t.Errorf("name = %v", body["name"])
	}
	if body["active_rf_band"] != "vhf" {
		t.Errorf("active_rf_band = %v", body["active_rf_band"])
	}
	// Station operation, not station configuration: hardware settings are the
	// Root surface in V15 and must not leak here.
	for _, field := range []string{
		"rotator_serial_port", "rotator_baud_rate", "sdr_device_identifier",
		"gain_db", "ppm_correction",
	} {
		if _, present := body[field]; present {
			t.Errorf("station status discloses %q to an Admin", field)
		}
	}
}

func TestStationStatusBeforeSetup(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)

	code, body := env.do(t, http.MethodGet, "/api/station", rootToken, nil)
	if code != http.StatusConflict {
		t.Fatalf("station status before setup = %d %v", code, body)
	}
	if body["error"] != "not_initialized" {
		t.Errorf("error = %v, want not_initialized", body["error"])
	}
}

// An Admin approves passes, so they must be able to read the rules those
// approvals are judged by. Changing them stays with Root.
func TestAdminReadsSchedulingConfigButCannotWriteIt(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	_, adminToken := env.createUser(t, rootToken, "configadmin", "admin")
	_, userToken := env.createUser(t, rootToken, "configuser", "user")

	code, body := env.do(t, http.MethodGet, "/api/scheduling-config", adminToken, nil)
	if code != http.StatusOK {
		t.Fatalf("admin reading config = %d %v", code, body)
	}
	for _, field := range []string{
		"minimum_lead_time_seconds", "pre_pass_buffer_seconds",
		"post_pass_buffer_seconds", "recording_pre_roll_seconds",
		"recording_post_roll_seconds", "minimum_elevation_degrees",
		"effective_from",
	} {
		if _, present := body[field]; !present {
			t.Errorf("the response is missing %q", field)
		}
	}

	// Writing is refused for both.
	update := map[string]any{
		"minimum_lead_time_seconds": 600, "pre_pass_buffer_seconds": 60,
		"post_pass_buffer_seconds": 60, "recording_pre_roll_seconds": 5,
		"recording_post_roll_seconds": 5, "minimum_elevation_degrees": 12,
	}
	if code, _ := env.do(t, http.MethodPut, "/api/scheduling-config", adminToken, update); code != http.StatusForbidden {
		t.Errorf("admin writing config = %d, want 403", code)
	}
	if code, _ := env.do(t, http.MethodGet, "/api/scheduling-config", userToken, nil); code != http.StatusForbidden {
		t.Errorf("user reading config = %d, want 403", code)
	}
}

// A Root change to the rules must be visible to the Admin who approves under
// them, or the two are working from different books.
//
// Setup leaves a 1800s lead time, so 600s is a relaxation and waits; the
// read still reports what is in force.
func TestSchedulingConfigReadReflectsTheLatestVersion(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)

	update := map[string]any{
		"minimum_lead_time_seconds": 600, "pre_pass_buffer_seconds": 90,
		"post_pass_buffer_seconds": 90, "recording_pre_roll_seconds": 7,
		"recording_post_roll_seconds": 7, "minimum_elevation_degrees": 12.5,
	}
	if code, body := env.do(t, http.MethodPut, "/api/scheduling-config", rootToken, update); code != http.StatusCreated {
		t.Fatalf("root updating config = %d %v", code, body)
	}

	code, body := env.do(t, http.MethodGet, "/api/scheduling-config", rootToken, nil)
	if code != http.StatusOK {
		t.Fatalf("read = %d %v", code, body)
	}
	if body["minimum_lead_time_seconds"] != float64(1800) {
		t.Errorf("lead time = %v, want the version still in force (1800)",
			body["minimum_lead_time_seconds"])
	}
}

// Raising the lead time is a tightening, not a bypass, so it applies at once.
// Waiting the new period out would leave the looser rule in force for exactly
// as long as the new value, which is how a station ends up looking stuck.
func TestTighteningTheLeadTimeAppliesImmediately(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)

	// Relax first, and wait it out is not possible in a test, so relax then
	// tighten back: the tightening is what must be immediate.
	relax := map[string]any{
		"minimum_lead_time_seconds": 60, "pre_pass_buffer_seconds": 120,
		"post_pass_buffer_seconds": 120, "recording_pre_roll_seconds": 10,
		"recording_post_roll_seconds": 10, "minimum_elevation_degrees": 10.0,
	}
	if code, _ := env.do(t, http.MethodPut, "/api/scheduling-config", rootToken, relax); code != http.StatusCreated {
		t.Fatal("relaxing failed")
	}
	// Still 1800 in force, with the relaxation pending.
	_, body := env.do(t, http.MethodGet, "/api/scheduling-config", rootToken, nil)
	if body["minimum_lead_time_seconds"] != float64(1800) {
		t.Fatalf("after relaxing, in force = %v", body["minimum_lead_time_seconds"])
	}

	tighten := map[string]any{
		"minimum_lead_time_seconds": 3600, "pre_pass_buffer_seconds": 120,
		"post_pass_buffer_seconds": 120, "recording_pre_roll_seconds": 10,
		"recording_post_roll_seconds": 10, "minimum_elevation_degrees": 15.0,
	}
	if code, body := env.do(t, http.MethodPut, "/api/scheduling-config", rootToken, tighten); code != http.StatusCreated {
		t.Fatalf("tightening = %d %v", code, body)
	}

	_, body = env.do(t, http.MethodGet, "/api/scheduling-config", rootToken, nil)
	if body["minimum_lead_time_seconds"] != float64(3600) {
		t.Errorf("in force = %v, want the tightened 3600 straight away",
			body["minimum_lead_time_seconds"])
	}
	if body["minimum_elevation_degrees"] != 15.0 {
		t.Errorf("elevation = %v, want 15", body["minimum_elevation_degrees"])
	}
	// The superseded relaxation must not come back later.
	if pending, ok := body["pending"].(map[string]any); ok {
		if pending["minimum_lead_time_seconds"] == float64(60) {
			t.Error("the superseded relaxation is still queued to apply")
		}
	}
}

func TestTLEStatusReportsFreshnessAndGaps(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	_, adminToken := env.createUser(t, rootToken, "tleadmin", "admin")
	_, userToken := env.createUser(t, rootToken, "tleuser", "user")

	// One satellite with recent orbital data.
	env.seedSatellite(t)

	if code, _ := env.do(t, http.MethodGet, "/api/tle-status", userToken, nil); code != http.StatusForbidden {
		t.Errorf("user reading tle status = %d, want 403", code)
	}

	code, body := env.do(t, http.MethodGet, "/api/tle-status", adminToken, nil)
	if code != http.StatusOK {
		t.Fatalf("admin reading tle status = %d %v", code, body)
	}
	satellites := body["satellites"].([]any)
	if len(satellites) != 1 {
		t.Fatalf("satellites = %d, want 1", len(satellites))
	}
	entry := satellites[0].(map[string]any)
	if entry["has_orbital_data"] != true {
		t.Errorf("has_orbital_data = %v", entry["has_orbital_data"])
	}
	if entry["stale"] != false {
		t.Errorf("six-hour-old data reported stale: %v", entry)
	}
	if entry["age_hours"] == nil {
		t.Error("age_hours is missing")
	}
	if body["missing_count"] != float64(0) || body["stale_count"] != float64(0) {
		t.Errorf("counts = %v / %v", body["missing_count"], body["stale_count"])
	}
	if body["stale_after_hours"] == nil {
		t.Error("the client cannot tell what stale means without the threshold")
	}
}

// A catalogue entry with no orbital data at all is a real state, and is not
// the same as stale.
func TestTLEStatusSeparatesMissingFromStale(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)

	if _, err := env.repo.CreateSatellite(t.Context(), satelliteWithoutTLE()); err != nil {
		t.Fatalf("create satellite: %v", err)
	}

	code, body := env.do(t, http.MethodGet, "/api/tle-status", rootToken, nil)
	if code != http.StatusOK {
		t.Fatalf("read = %d %v", code, body)
	}
	entry := body["satellites"].([]any)[0].(map[string]any)
	if entry["has_orbital_data"] != false {
		t.Errorf("has_orbital_data = %v", entry["has_orbital_data"])
	}
	if entry["stale"] != false {
		t.Error("a satellite that never had data is reported as stale")
	}
	if _, present := entry["age_hours"]; present {
		t.Error("age_hours is present for a satellite with no orbital data")
	}
	if body["missing_count"] != float64(1) {
		t.Errorf("missing_count = %v, want 1", body["missing_count"])
	}
}

// satelliteWithoutTLE is a catalogue entry with no orbital data behind it,
// which is what a freshly added satellite looks like before a refresh.
func satelliteWithoutTLE() domain.Satellite {
	return domain.Satellite{NoradID: 99999, Name: "NEW-SAT", IsSchedulable: true}
}

// A saved configuration change is invisible until it takes effect, which
// reads exactly like a save that failed (spec.md section 13.6 makes the delay
// deliberate, but it has to be visible).
func TestASavedChangeIsReportedWhileItWaits(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)

	update := map[string]any{
		"minimum_lead_time_seconds": 600, "pre_pass_buffer_seconds": 90,
		"post_pass_buffer_seconds": 90, "recording_pre_roll_seconds": 7,
		"recording_post_roll_seconds": 7, "minimum_elevation_degrees": 12.5,
	}
	if code, body := env.do(t, http.MethodPut, "/api/scheduling-config", rootToken, update); code != http.StatusCreated {
		t.Fatalf("update = %d %v", code, body)
	}

	code, body := env.do(t, http.MethodGet, "/api/scheduling-config", rootToken, nil)
	if code != http.StatusOK {
		t.Fatalf("read = %d %v", code, body)
	}
	// The values in force are still the old ones.
	if body["minimum_lead_time_seconds"] != float64(1800) {
		t.Errorf("in force = %v, want 1800", body["minimum_lead_time_seconds"])
	}
	// But the save is visible, or Root concludes it was lost.
	pending, ok := body["pending"].(map[string]any)
	if !ok {
		t.Fatalf("no pending change reported: %v", body)
	}
	if pending["minimum_lead_time_seconds"] != float64(600) {
		t.Errorf("pending lead time = %v, want 600", pending["minimum_lead_time_seconds"])
	}
	if pending["minimum_elevation_degrees"] != 12.5 {
		t.Errorf("pending elevation = %v", pending["minimum_elevation_degrees"])
	}
	if pending["effective_from"] == nil {
		t.Error("the pending change does not say when it applies")
	}
}

func TestNoPendingChangeIsReportedWhenThereIsNone(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)

	_, body := env.do(t, http.MethodGet, "/api/scheduling-config", rootToken, nil)
	if _, present := body["pending"]; present {
		t.Errorf("a pending change was invented: %v", body["pending"])
	}
}
