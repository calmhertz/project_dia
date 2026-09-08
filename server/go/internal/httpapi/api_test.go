package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aagasa/internal/auth"
	"aagasa/internal/domain"
	"aagasa/internal/filestore"
	"aagasa/internal/httpapi"
	"aagasa/internal/passplan"
	"aagasa/internal/prediction"
	"aagasa/internal/scheduling"
	"aagasa/internal/store"
	"aagasa/internal/testsupport"
)

// stubPredictor lets a test script the passes the scheduler will see, so the
// API tests need neither the prediction service nor a network.
type stubPredictor struct {
	mu     sync.Mutex
	passes []prediction.Pass
}

func (s *stubPredictor) setPasses(passes ...prediction.Pass) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.passes = passes
}

func (s *stubPredictor) PredictPasses(ctx context.Context, request prediction.Request) (prediction.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var inWindow []prediction.Pass
	for _, candidate := range s.passes {
		if !candidate.AOS.Before(request.SearchStart) && !candidate.AOS.After(request.SearchEnd) {
			inWindow = append(inWindow, candidate)
		}
	}
	return prediction.Result{NoradID: 25544, Passes: inWindow}, nil
}

// These tests exercise the real HTTP surface against real PostgreSQL and
// Redis. They skip unless both are configured.
//
//	AAGASA_TEST_POSTGRES_URL=... AAGASA_TEST_REDIS_URL=... go test ./internal/httpapi/...

const strongPassword = "a-sufficiently-long-password"

type testEnv struct {
	server    *httptest.Server
	repo      *store.Repository
	predictor *stubPredictor
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	postgresURL := testsupport.NewDatabase(t)
	ctx := context.Background()

	if err := store.MigrateUp(ctx, postgresURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, err := pgxpool.New(ctx, postgresURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	redisClient := testsupport.NewRedis(t)

	repo := store.NewRepository(pool)
	sessions := store.NewSessionStore(redisClient)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	authService := auth.NewService(repo, sessions, logger, time.Hour)

	if err := authService.EnsureBootstrapRoot(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	predictor := &stubPredictor{}
	// A real plan generator, so approving a pass over the API exercises the
	// same path production does. With nil here, a station that cannot produce
	// a plan looked fine in every test.
	planGenerator := passplan.NewGenerator(repo, predictor)
	schedulingService := scheduling.NewService(repo, predictor, planGenerator, logger)

	files, err := filestore.New(t.TempDir()+"/recordings", t.TempDir()+"/pipelines")
	if err != nil {
		t.Fatalf("file storage: %v", err)
	}

	mux := httpapi.NewMux(logger, "test", httpapi.Dependencies{
		Files:      files,
		Repository: repo, Auth: authService, Scheduling: schedulingService,
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return &testEnv{server: server, repo: repo, predictor: predictor}
}

// do issues a request, optionally authenticated, and decodes the JSON body.
func (e *testEnv) do(t *testing.T, method, path, token string, body any) (int, map[string]any) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := e.server.Client().Do(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer response.Body.Close()

	payload := map[string]any{}
	raw, _ := io.ReadAll(response.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	return response.StatusCode, payload
}

func (e *testEnv) login(t *testing.T, username, password string) (int, string, bool) {
	t.Helper()
	code, body := e.do(t, http.MethodPost, "/api/auth/login", "", map[string]any{
		"username": username, "password": password,
	})
	token, _ := body["token"].(string)
	mustChange, _ := body["must_change_password"].(bool)
	return code, token, mustChange
}

// rootReady completes the forced password change and returns a usable token.
func (e *testEnv) rootReady(t *testing.T) string {
	t.Helper()
	_, token, _ := e.login(t, auth.BootstrapUsername, auth.BootstrapPassword)
	if code, _ := e.do(t, http.MethodPost, "/api/auth/change-password", token, map[string]any{
		"current_password": auth.BootstrapPassword, "new_password": strongPassword,
	}); code != http.StatusNoContent {
		t.Fatalf("change password: %d", code)
	}
	_, token, _ = e.login(t, auth.BootstrapUsername, strongPassword)
	return token
}

// createUser makes an account and returns its id and a ready-to-use token.
func (e *testEnv) createUser(t *testing.T, rootToken, username, role string) (string, string) {
	t.Helper()
	code, body := e.do(t, http.MethodPost, "/api/users", rootToken, map[string]any{
		"username": username, "password": strongPassword, "role": role,
	})
	if code != http.StatusCreated {
		t.Fatalf("create %s: %d %v", username, code, body)
	}
	id, _ := body["id"].(string)

	// New accounts must change their password before they can do anything.
	_, token, _ := e.login(t, username, strongPassword)
	newPassword := strongPassword + "-" + username
	if code, _ := e.do(t, http.MethodPost, "/api/auth/change-password", token, map[string]any{
		"current_password": strongPassword, "new_password": newPassword,
	}); code != http.StatusNoContent {
		t.Fatalf("change password for %s: %d", username, code)
	}
	_, token, _ = e.login(t, username, newPassword)
	return id, token
}

// spec.md section 17.1: the first start creates root/toor and demands a change.
func TestFirstStartCreatesRootRequiringPasswordChange(t *testing.T) {
	env := newTestEnv(t)

	code, token, mustChange := env.login(t, auth.BootstrapUsername, auth.BootstrapPassword)
	if code != http.StatusOK {
		t.Fatalf("bootstrap login = %d, want 200", code)
	}
	if !mustChange {
		t.Error("must_change_password is false on the bootstrap account")
	}
	if token == "" {
		t.Fatal("no session token issued")
	}

	root, err := env.repo.GetUserByUsername(context.Background(), auth.BootstrapUsername)
	if err != nil {
		t.Fatalf("load root: %v", err)
	}
	if root.Role != domain.RoleRoot {
		t.Errorf("role = %q, want root", root.Role)
	}
	// Global test property 20.
	if err := auth.VerifyPassword(auth.BootstrapPassword, root.PasswordHash); err != nil {
		t.Errorf("stored hash does not verify: %v", err)
	}
}

// Until the password changes, everything except the change itself is refused.
func TestPasswordChangeIsRequiredBeforeAnythingElse(t *testing.T) {
	env := newTestEnv(t)
	_, token, _ := env.login(t, auth.BootstrapUsername, auth.BootstrapPassword)

	blocked := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/users", nil},
		{http.MethodPost, "/api/users", map[string]any{"username": "x", "password": strongPassword}},
		{http.MethodGet, "/api/setup", nil},
		{http.MethodPost, "/api/setup", map[string]any{"station_name": "x"}},
	}
	for _, call := range blocked {
		code, body := env.do(t, call.method, call.path, token, call.body)
		if code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", call.method, call.path, code)
		}
		if body["error"] != "password_change_required" {
			t.Errorf("%s %s error = %v, want password_change_required", call.method, call.path, body["error"])
		}
	}

	// /me is allowed so the client can discover why it is blocked.
	if code, _ := env.do(t, http.MethodGet, "/api/auth/me", token, nil); code != http.StatusOK {
		t.Errorf("GET /api/auth/me = %d, want 200", code)
	}
}

// spec.md section 17.1: the bootstrap credential stops working after the change.
func TestBootstrapCredentialStopsWorking(t *testing.T) {
	env := newTestEnv(t)
	env.rootReady(t)

	if code, _, _ := env.login(t, auth.BootstrapUsername, auth.BootstrapPassword); code != http.StatusUnauthorized {
		t.Errorf("bootstrap login after change = %d, want 401", code)
	}
	if code, _, mustChange := env.login(t, auth.BootstrapUsername, strongPassword); code != http.StatusOK || mustChange {
		t.Errorf("new-password login = %d, must_change=%v; want 200, false", code, mustChange)
	}
}

// Root must not be able to keep the well-known bootstrap password.
func TestBootstrapPasswordCannotBeReused(t *testing.T) {
	env := newTestEnv(t)
	_, token, _ := env.login(t, auth.BootstrapUsername, auth.BootstrapPassword)

	code, body := env.do(t, http.MethodPost, "/api/auth/change-password", token, map[string]any{
		"current_password": auth.BootstrapPassword, "new_password": auth.BootstrapPassword,
	})
	if code != http.StatusBadRequest {
		t.Errorf("reusing bootstrap password = %d, want 400 (%v)", code, body)
	}
}

func TestShortPasswordIsRejected(t *testing.T) {
	env := newTestEnv(t)
	_, token, _ := env.login(t, auth.BootstrapUsername, auth.BootstrapPassword)

	if code, _ := env.do(t, http.MethodPost, "/api/auth/change-password", token, map[string]any{
		"current_password": auth.BootstrapPassword, "new_password": "short",
	}); code != http.StatusBadRequest {
		t.Errorf("short password = %d, want 400", code)
	}
}

func TestWrongCurrentPasswordIsRejected(t *testing.T) {
	env := newTestEnv(t)
	_, token, _ := env.login(t, auth.BootstrapUsername, auth.BootstrapPassword)

	if code, _ := env.do(t, http.MethodPost, "/api/auth/change-password", token, map[string]any{
		"current_password": "not-the-password", "new_password": strongPassword,
	}); code != http.StatusUnauthorized {
		t.Errorf("wrong current password = %d, want 401", code)
	}
}

func TestUnauthenticatedRequestsAreRejected(t *testing.T) {
	env := newTestEnv(t)

	for _, path := range []string{"/api/auth/me", "/api/users", "/api/setup"} {
		if code, _ := env.do(t, http.MethodGet, path, "", nil); code != http.StatusUnauthorized {
			t.Errorf("GET %s without a token = %d, want 401", path, code)
		}
		if code, _ := env.do(t, http.MethodGet, path, "not-a-real-token", nil); code != http.StatusUnauthorized {
			t.Errorf("GET %s with a bogus token = %d, want 401", path, code)
		}
	}
}

func TestLogoutRevokesTheSession(t *testing.T) {
	env := newTestEnv(t)
	token := env.rootReady(t)

	if code, _ := env.do(t, http.MethodPost, "/api/auth/logout", token, nil); code != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", code)
	}
	if code, _ := env.do(t, http.MethodGet, "/api/auth/me", token, nil); code != http.StatusUnauthorized {
		t.Errorf("reusing a logged-out token = %d, want 401", code)
	}
}

// Changing the password must not leave the old session usable.
func TestPasswordChangeRevokesTheSession(t *testing.T) {
	env := newTestEnv(t)
	_, token, _ := env.login(t, auth.BootstrapUsername, auth.BootstrapPassword)

	if code, _ := env.do(t, http.MethodPost, "/api/auth/change-password", token, map[string]any{
		"current_password": auth.BootstrapPassword, "new_password": strongPassword,
	}); code != http.StatusNoContent {
		t.Fatalf("change password: %d", code)
	}
	if code, _ := env.do(t, http.MethodGet, "/api/auth/me", token, nil); code != http.StatusUnauthorized {
		t.Errorf("old session after password change = %d, want 401", code)
	}
}

// Exit criterion: a normal user cannot perform an admin operation.
func TestNormalUserCannotPerformAdminOperations(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	_, userToken := env.createUser(t, rootToken, "normal", "user")

	calls := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/api/users", nil},
		{http.MethodPost, "/api/users", map[string]any{"username": "another", "password": strongPassword}},
		{http.MethodPost, "/api/setup", map[string]any{"station_name": "x"}},
	}
	for _, call := range calls {
		if code, _ := env.do(t, call.method, call.path, userToken, call.body); code != http.StatusForbidden {
			t.Errorf("%s %s as normal user = %d, want 403", call.method, call.path, code)
		}
	}
}

// Exit criterion: an admin cannot perform a Root-only operation.
func TestAdminCannotPerformRootOnlyOperations(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	_, adminToken := env.createUser(t, rootToken, "admin1", "admin")
	targetID, _ := env.createUser(t, rootToken, "target", "user")

	// Root-only: promotion, deletion, first-run setup.
	if code, _ := env.do(t, http.MethodPatch, "/api/users/"+targetID, adminToken,
		map[string]any{"role": "admin"}); code != http.StatusForbidden {
		t.Errorf("admin promoting a user = %d, want 403", code)
	}
	if code, _ := env.do(t, http.MethodDelete, "/api/users/"+targetID, adminToken, nil); code != http.StatusForbidden {
		t.Errorf("admin deleting a user = %d, want 403", code)
	}
	if code, _ := env.do(t, http.MethodPost, "/api/setup", adminToken,
		validSetupBody()); code != http.StatusForbidden {
		t.Errorf("admin running setup = %d, want 403", code)
	}
	// An admin may still create normal users and list accounts.
	if code, _ := env.do(t, http.MethodGet, "/api/users", adminToken, nil); code != http.StatusOK {
		t.Errorf("admin listing users = %d, want 200", code)
	}
	if code, _ := env.do(t, http.MethodPost, "/api/users", adminToken, map[string]any{
		"username": "made-by-admin", "password": strongPassword, "role": "user",
	}); code != http.StatusCreated {
		t.Errorf("admin creating a normal user = %d, want 201", code)
	}
	// But not another admin.
	if code, _ := env.do(t, http.MethodPost, "/api/users", adminToken, map[string]any{
		"username": "admin2", "password": strongPassword, "role": "admin",
	}); code != http.StatusForbidden {
		t.Errorf("admin creating an admin = %d, want 403", code)
	}
}

// Exit criteria: Root cannot be deleted and cannot be demoted.
func TestRootCannotBeDeletedOrDemoted(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)

	root, err := env.repo.GetUserByUsername(context.Background(), auth.BootstrapUsername)
	if err != nil {
		t.Fatalf("load root: %v", err)
	}
	rootID := root.ID.String()

	if code, _ := env.do(t, http.MethodDelete, "/api/users/"+rootID, rootToken, nil); code != http.StatusForbidden {
		t.Errorf("deleting root = %d, want 403", code)
	}
	for _, role := range []string{"admin", "user"} {
		if code, _ := env.do(t, http.MethodPatch, "/api/users/"+rootID, rootToken,
			map[string]any{"role": role}); code != http.StatusForbidden {
			t.Errorf("demoting root to %s = %d, want 403", role, code)
		}
	}

	// Root is still Root and still able to sign in.
	after, err := env.repo.GetUserByUsername(context.Background(), auth.BootstrapUsername)
	if err != nil {
		t.Fatalf("reload root: %v", err)
	}
	if after.Role != domain.RoleRoot {
		t.Errorf("role after attempts = %q, want root", after.Role)
	}
}

// The database refuses even if the API layer is bypassed entirely.
func TestDatabaseRefusesRootDeletionDirectly(t *testing.T) {
	env := newTestEnv(t)
	env.rootReady(t)
	ctx := context.Background()

	root, err := env.repo.GetUserByUsername(ctx, auth.BootstrapUsername)
	if err != nil {
		t.Fatalf("load root: %v", err)
	}

	if err := env.repo.DeleteUser(ctx, root.ID); err == nil {
		t.Error("repository deleted root")
	}
	if err := env.repo.SetUserRole(ctx, root.ID, domain.RoleUser); err == nil {
		t.Error("repository demoted root")
	}
}

func TestRootCanPromoteAndDemoteAdmins(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	targetID, _ := env.createUser(t, rootToken, "promotable", "user")

	code, body := env.do(t, http.MethodPatch, "/api/users/"+targetID, rootToken,
		map[string]any{"role": "admin"})
	if code != http.StatusOK {
		t.Fatalf("promote = %d, want 200", code)
	}
	if body["role"] != "admin" {
		t.Errorf("role = %v, want admin", body["role"])
	}

	code, body = env.do(t, http.MethodPatch, "/api/users/"+targetID, rootToken,
		map[string]any{"role": "user"})
	if code != http.StatusOK || body["role"] != "user" {
		t.Errorf("demote = %d, role = %v", code, body["role"])
	}
}

func TestNobodyCanCreateASecondRoot(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)

	if code, _ := env.do(t, http.MethodPost, "/api/users", rootToken, map[string]any{
		"username": "root2", "password": strongPassword, "role": "root",
	}); code != http.StatusForbidden {
		t.Errorf("creating a second root = %d, want 403", code)
	}

	targetID, _ := env.createUser(t, rootToken, "aspiring", "user")
	if code, _ := env.do(t, http.MethodPatch, "/api/users/"+targetID, rootToken,
		map[string]any{"role": "root"}); code != http.StatusForbidden {
		t.Errorf("promoting to root = %d, want 403", code)
	}
}

// A response must never carry a password hash.
func TestUserResponsesNeverIncludeThePasswordHash(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.createUser(t, rootToken, "listed", "user")

	request, err := http.NewRequest(http.MethodGet, env.server.URL+"/api/users", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+rootToken)
	response, err := env.server.Client().Do(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer response.Body.Close()

	raw, _ := io.ReadAll(response.Body)
	for _, forbidden := range []string{"password_hash", "argon2id", "PasswordHash"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Errorf("user list response leaks %q", forbidden)
		}
	}
}

// A failed login must not reveal whether the account exists.
func TestLoginDoesNotDiscloseAccountExistence(t *testing.T) {
	env := newTestEnv(t)
	env.rootReady(t)

	_, unknownBody := env.do(t, http.MethodPost, "/api/auth/login", "", map[string]any{
		"username": "no-such-user", "password": "whatever-password",
	})
	_, wrongBody := env.do(t, http.MethodPost, "/api/auth/login", "", map[string]any{
		"username": auth.BootstrapUsername, "password": "wrong-password",
	})

	if unknownBody["error"] != wrongBody["error"] || unknownBody["message"] != wrongBody["message"] {
		t.Errorf("responses differ: unknown=%v wrong=%v", unknownBody, wrongBody)
	}
}

func validSetupBody() map[string]any {
	return map[string]any{
		"station_name":   "SJCIT Ground Station",
		"latitude":       13.394944,
		"longitude":      77.729444,
		"altitude_m":     915.0,
		"timezone":       "Asia/Kolkata",
		"active_rf_band": "vhf",
		"bands": []map[string]any{
			{"band": "vhf", "antenna_description": "Turnstile", "center_frequency_hz": 137100000,
				"sample_rate_hz": 2048000, "gain_db": 40.0},
			{"band": "uhf", "antenna_description": "Yagi", "center_frequency_hz": 435000000,
				"sample_rate_hz": 2048000, "gain_db": 40.0},
		},
		"rotator_serial_port":            "/dev/ttyUSB0",
		"rotator_baud_rate":              9600,
		"rotator_park_azimuth_degrees":   0,
		"rotator_park_elevation_degrees": 0,
		"sdr_device_identifier":          "rtlsdr",
		"minimum_lead_time_seconds":      1800,
		"pre_pass_buffer_seconds":        120,
		"post_pass_buffer_seconds":       120,
		"recording_pre_roll_seconds":     10,
		"recording_post_roll_seconds":    10,
		"minimum_elevation_degrees":      10.0,
		"worker_name":                    "worker-1",
	}
}

// spec.md section 23: first-run setup persists the station configuration.
func TestFirstRunSetupPersistsStationConfiguration(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	ctx := context.Background()

	if _, body := env.do(t, http.MethodGet, "/api/setup", rootToken, nil); body["initialized"] != false {
		t.Errorf("initialized before setup = %v, want false", body["initialized"])
	}

	code, body := env.do(t, http.MethodPost, "/api/setup", rootToken, validSetupBody())
	if code != http.StatusCreated {
		t.Fatalf("setup = %d, want 201 (%v)", code, body)
	}

	if _, body := env.do(t, http.MethodGet, "/api/setup", rootToken, nil); body["initialized"] != true {
		t.Errorf("initialized after setup = %v, want true", body["initialized"])
	}

	stations, err := env.repo.ListStations(ctx)
	if err != nil || len(stations) != 1 {
		t.Fatalf("stations = %d, %v; want 1", len(stations), err)
	}
	station := stations[0]
	if station.ActiveRFBand != domain.BandVHF || station.Timezone != "Asia/Kolkata" {
		t.Errorf("station = %+v", station)
	}

	bands, err := env.repo.GetStationBandConfigs(ctx, station.ID)
	if err != nil || len(bands) != 2 {
		t.Fatalf("band configs = %d, %v; want 2", len(bands), err)
	}

	hardware, err := env.repo.GetStationHardwareConfig(ctx, station.ID)
	if err != nil {
		t.Fatalf("hardware config: %v", err)
	}
	if hardware.RotatorSerialPort != "/dev/ttyUSB0" || hardware.RotatorBaudRate != 9600 {
		t.Errorf("hardware = %+v", hardware)
	}

	config, err := env.repo.LatestEffectiveSchedulingConfig(ctx, station.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("scheduling config: %v", err)
	}
	if config.MinimumLeadTime != 30*time.Minute || config.PrePassBuffer != 2*time.Minute {
		t.Errorf("scheduling config = %+v", config)
	}

	if _, err := env.repo.GetWorkerByName(ctx, "worker-1"); err != nil {
		t.Errorf("worker not created: %v", err)
	}
}

func TestSetupRunsOnlyOnce(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)

	if code, _ := env.do(t, http.MethodPost, "/api/setup", rootToken, validSetupBody()); code != http.StatusCreated {
		t.Fatalf("first setup did not succeed")
	}
	if code, body := env.do(t, http.MethodPost, "/api/setup", rootToken, validSetupBody()); code != http.StatusConflict {
		t.Errorf("second setup = %d, want 409 (%v)", code, body)
	}
}

// A rejected setup must not leave a partially configured station behind.
func TestInvalidSetupLeavesNothingBehind(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	ctx := context.Background()

	bad := validSetupBody()
	bad["rotator_park_elevation_degrees"] = 120 // outside the G-550 range

	if code, _ := env.do(t, http.MethodPost, "/api/setup", rootToken, bad); code != http.StatusBadRequest {
		t.Fatalf("invalid setup = %d, want 400", code)
	}

	stations, err := env.repo.ListStations(ctx)
	if err != nil {
		t.Fatalf("list stations: %v", err)
	}
	if len(stations) != 0 {
		t.Errorf("stations = %d, want 0 after a rejected setup", len(stations))
	}
	at, err := env.repo.SystemInitializedAt(ctx)
	if err != nil || at != nil {
		t.Errorf("system marked initialized after a rejected setup")
	}
}

// spec.md section 22: privileged actions leave an audit trail.
func TestPrivilegedActionsAreAudited(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	ctx := context.Background()

	targetID, _ := env.createUser(t, rootToken, "audited", "user")
	if code, _ := env.do(t, http.MethodPatch, "/api/users/"+targetID, rootToken,
		map[string]any{"role": "admin"}); code != http.StatusOK {
		t.Fatal("promotion failed")
	}

	count, err := env.repo.CountAuditRecords(ctx, "user", targetID)
	if err != nil {
		t.Fatalf("count audit: %v", err)
	}
	// Creation plus the role change.
	if count < 2 {
		t.Errorf("audit records = %d, want at least 2", count)
	}
}

func TestMalformedRequestBodiesAreRejected(t *testing.T) {
	env := newTestEnv(t)
	token := env.rootReady(t)

	request, err := http.NewRequest(http.MethodPost, env.server.URL+"/api/users",
		bytes.NewReader([]byte(`{"username":"x","unexpected_field":true}`)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := env.server.Client().Do(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown field = %d, want 400", response.StatusCode)
	}
}

// An account that requested passes cannot be deleted: the database refuses it
// so the history outlives the account (spec.md section 13). The API has to
// say that rather than reporting a fault.
func TestDeletingAUserWithPassesIsRefusedClearly(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	userID, userToken := env.createUser(t, rootToken, "departing", "user")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))
	if code, body := env.schedulePass(t, userToken, satelliteID, aos, nil); code != http.StatusCreated {
		t.Fatalf("request pass = %d %v", code, body)
	}

	code, body := env.do(t, http.MethodDelete, "/api/users/"+userID, rootToken, nil)
	if code != http.StatusConflict {
		t.Fatalf("delete = %d %v, want 409", code, body)
	}
	if body["error"] != "in_use" {
		t.Errorf("error = %v, want in_use", body["error"])
	}

	// The account is still there and still usable, not half-deleted.
	_, list := env.do(t, http.MethodGet, "/api/users", rootToken, nil)
	found := false
	for _, entry := range list["users"].([]any) {
		if entry.(map[string]any)["id"] == userID {
			found = true
		}
	}
	if !found {
		t.Error("the account disappeared from the listing after a refused delete")
	}
}

// An account with no history deletes normally, so the refusal above is about
// the references and not about deletion being broken.
func TestDeletingAUserWithoutPassesWorks(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	userID, _ := env.createUser(t, rootToken, "nevertried", "user")

	if code, body := env.do(t, http.MethodDelete, "/api/users/"+userID, rootToken, nil); code != http.StatusNoContent {
		t.Fatalf("delete = %d %v, want 204", code, body)
	}
}

// Deleting an account must not be blocked by its own audit trail. Every login
// is audited, so if the trail's append-only rule fought the account's foreign
// key, no account that had ever signed in could be removed.
func TestAuditTrailDoesNotBlockAccountDeletion(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	userID, userToken := env.createUser(t, rootToken, "auditedaccount", "user")

	// Signing in and reading are audited, so the account now has history.
	if code, _ := env.do(t, http.MethodGet, "/api/auth/me", userToken, nil); code != http.StatusOK {
		t.Fatal("the new account cannot read its own profile")
	}

	if code, body := env.do(t, http.MethodDelete, "/api/users/"+userID, rootToken, nil); code != http.StatusNoContent {
		t.Fatalf("delete = %d %v, want 204", code, body)
	}

	// The trail keeps the actor identifier: an audit record that forgets who
	// acted is worth less than one naming an account that no longer exists.
	_, trail := env.do(t, http.MethodGet, "/api/audit?action=auth.login", rootToken, nil)
	kept := false
	for _, entry := range trail["records"].([]any) {
		if entry.(map[string]any)["actor_user_id"] == userID {
			kept = true
		}
	}
	if !kept {
		t.Error("deleting the account erased who acted in the audit trail")
	}
}
