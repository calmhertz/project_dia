package httpapi_test

import (
	"net/http"
	"testing"
	"time"
)

// The V15 Root control surface. Root configures the station; an Admin may
// not, and a change never rewrites a pass that is already committed.

func TestStationConfigIsRootOnly(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	_, adminToken := env.createUser(t, rootToken, "configviewer", "admin")

	if code, _ := env.do(t, http.MethodGet, "/api/station/config", adminToken, nil); code != http.StatusForbidden {
		t.Errorf("admin reading full config = %d, want 403", code)
	}

	code, body := env.do(t, http.MethodGet, "/api/station/config", rootToken, nil)
	if code != http.StatusOK {
		t.Fatalf("root reading full config = %d %v", code, body)
	}
	hardware := body["hardware"].(map[string]any)
	if hardware["rotator_serial_port"] != "/dev/ttyUSB0" {
		t.Errorf("rotator_serial_port = %v", hardware["rotator_serial_port"])
	}
	if len(body["bands"].([]any)) != 2 {
		t.Errorf("bands = %v", body["bands"])
	}
}

func TestRootUpdatesStationIdentityAndBand(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	_, adminToken := env.createUser(t, rootToken, "bandadmin", "admin")

	update := map[string]any{"name": "Relocated Station", "active_rf_band": "uhf"}
	if code, _ := env.do(t, http.MethodPatch, "/api/station", adminToken, update); code != http.StatusForbidden {
		t.Errorf("admin updating the station = %d, want 403", code)
	}

	code, body := env.do(t, http.MethodPatch, "/api/station", rootToken, update)
	if code != http.StatusOK {
		t.Fatalf("root updating the station = %d %v", code, body)
	}
	if body["name"] != "Relocated Station" || body["active_rf_band"] != "uhf" {
		t.Errorf("update did not take: %v", body)
	}

	// The change is visible to the Admin operational view too.
	_, status := env.do(t, http.MethodGet, "/api/station", adminToken, nil)
	if status["active_rf_band"] != "uhf" {
		t.Errorf("station status still reports %v", status["active_rf_band"])
	}
}

// Nonsense must be refused before it reaches the database or the rotator.
func TestStationUpdateRejectsImpossibleValues(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)

	for _, body := range []map[string]any{
		{"latitude_degrees": 91.0},
		{"longitude_degrees": -181.0},
		{"active_rf_band": "sband"},
		{"name": "   "},
	} {
		code, response := env.do(t, http.MethodPatch, "/api/station", rootToken, body)
		if code != http.StatusBadRequest {
			t.Errorf("update %v = %d, want 400 (%v)", body, code, response)
		}
	}
}

// client-spec section 29: Root is warned that scheduled work exists, and the
// work itself is left alone.
func TestConfigurationChangeReportsButDoesNotRewriteScheduledPasses(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, ownerToken := env.createUser(t, rootToken, "committedowner", "user")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))
	_, requested := env.schedulePass(t, ownerToken, satelliteID, aos, nil)
	pass := requested["pass"].(map[string]any)
	passID := pass["id"].(string)
	bandBefore := pass["band"]

	code, body := env.do(t, http.MethodPatch, "/api/station", rootToken,
		map[string]any{"active_rf_band": "uhf"})
	if code != http.StatusOK {
		t.Fatalf("update = %d %v", code, body)
	}
	if body["upcoming_passes_unchanged"] != float64(1) {
		t.Errorf("upcoming_passes_unchanged = %v, want 1",
			body["upcoming_passes_unchanged"])
	}

	_, after := env.do(t, http.MethodGet, "/api/passes/"+passID, rootToken, nil)
	if got := after["pass"].(map[string]any)["band"]; got != bandBefore {
		t.Errorf("the committed pass was rewritten to band %v", got)
	}
}

func TestRootUpdatesBandAndHardwareConfiguration(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)

	code, body := env.do(t, http.MethodPut, "/api/station/bands/uhf", rootToken,
		map[string]any{"antenna_description": "Cross Yagi", "gain_db": 32.5})
	if code != http.StatusOK {
		t.Fatalf("update band = %d %v", code, body)
	}
	if body["band"].(map[string]any)["antenna_description"] != "Cross Yagi" {
		t.Errorf("band = %v", body["band"])
	}

	code, body = env.do(t, http.MethodPut, "/api/station/hardware", rootToken,
		map[string]any{"rotator_serial_port": "/dev/ttyUSB1", "rotator_park_azimuth_degrees": 180})
	if code != http.StatusOK {
		t.Fatalf("update hardware = %d %v", code, body)
	}
	hardware := body["hardware"].(map[string]any)
	if hardware["rotator_serial_port"] != "/dev/ttyUSB1" {
		t.Errorf("serial port = %v", hardware["rotator_serial_port"])
	}
	// Untouched fields keep their value rather than being zeroed.
	if hardware["rotator_baud_rate"] != float64(9600) {
		t.Errorf("baud rate = %v, want the previous 9600", hardware["rotator_baud_rate"])
	}
}

// spec.md section 9: the G-550 accepts azimuth 0..359 and elevation 0..90.
// An out-of-range park angle would be commanded to real hardware.
func TestHardwareUpdateEnforcesRotatorLimits(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)

	for _, body := range []map[string]any{
		{"rotator_park_azimuth_degrees": 360},
		{"rotator_park_azimuth_degrees": -1},
		{"rotator_park_elevation_degrees": 91},
		{"rotator_baud_rate": 0},
		{"rotator_serial_port": ""},
	} {
		code, response := env.do(t, http.MethodPut, "/api/station/hardware", rootToken, body)
		if code != http.StatusBadRequest {
			t.Errorf("hardware %v = %d, want 400 (%v)", body, code, response)
		}
	}

	// Nothing was stored: the configuration still reads as it was set up.
	_, config := env.do(t, http.MethodGet, "/api/station/config", rootToken, nil)
	hardware := config["hardware"].(map[string]any)
	if hardware["rotator_park_azimuth_degrees"] != float64(0) {
		t.Errorf("a rejected value was stored: %v", hardware)
	}
}

func TestBandUpdateRejectsAnUnknownBand(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)

	code, _ := env.do(t, http.MethodPut, "/api/station/bands/sband", rootToken,
		map[string]any{"antenna_description": "Dish"})
	if code != http.StatusBadRequest {
		t.Errorf("unknown band = %d, want 400", code)
	}
}

func TestAuditTrailIsRootOnlyAndRecordsConfigurationChanges(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	_, adminToken := env.createUser(t, rootToken, "auditadmin", "admin")

	if code, _ := env.do(t, http.MethodGet, "/api/audit", adminToken, nil); code != http.StatusForbidden {
		t.Errorf("admin reading the audit trail = %d, want 403", code)
	}

	if code, _ := env.do(t, http.MethodPatch, "/api/station", rootToken,
		map[string]any{"name": "Audited Station"}); code != http.StatusOK {
		t.Fatal("station update failed")
	}

	code, body := env.do(t, http.MethodGet, "/api/audit?action=station.updated", rootToken, nil)
	if code != http.StatusOK {
		t.Fatalf("read audit = %d %v", code, body)
	}
	records := body["records"].([]any)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	record := records[0].(map[string]any)
	if record["action"] != "station.updated" || record["entity_type"] != "station" {
		t.Errorf("record = %v", record)
	}
	if record["actor_user_id"] == nil {
		t.Error("the record does not say who did it")
	}
	// The recorded before/after state is for forensic reading at the database
	// and is deliberately not published over HTTP.
	for _, field := range []string{"previous_state", "new_state"} {
		if _, present := record[field]; present {
			t.Errorf("the audit listing publishes %q", field)
		}
	}
}

// Every privileged action leaves a trace, including ones taken long before
// this endpoint existed.
func TestAuditTrailIncludesEarlierPrivilegedActions(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	env.createUser(t, rootToken, "traced", "user")

	_, body := env.do(t, http.MethodGet, "/api/audit", rootToken, nil)
	actions := map[string]bool{}
	for _, entry := range body["records"].([]any) {
		actions[entry.(map[string]any)["action"].(string)] = true
	}
	for _, expected := range []string{
		"system.initialized", "user.created", "auth.login", "auth.password_changed",
	} {
		if !actions[expected] {
			t.Errorf("the audit trail is missing %q: %v", expected, actions)
		}
	}
}

func TestAuditLimitIsBounded(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)

	for _, limit := range []string{"0", "-1", "1000", "abc"} {
		code, _ := env.do(t, http.MethodGet, "/api/audit?limit="+limit, rootToken, nil)
		if code != http.StatusBadRequest {
			t.Errorf("limit=%s = %d, want 400", limit, code)
		}
	}
}
