package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"aagasa/internal/store"
)

// The recording endpoints the V13 client uses. A recording is reachable only
// through the pass it belongs to, so its visibility is the pass's
// (spec.md section 16).

// seedRecording writes a file and its metadata for an existing pass.
func (e *testEnv) seedRecording(t *testing.T, passID, content string) (string, string) {
	t.Helper()
	ctx := context.Background()

	pass, err := e.repo.GetPassByID(ctx, uuid.MustParse(passID))
	if err != nil {
		t.Fatalf("get pass: %v", err)
	}

	directory := t.TempDir()
	stored := filepath.Join(directory, "capture.wav")
	if err := os.WriteFile(stored, []byte(content), 0o600); err != nil {
		t.Fatalf("write recording: %v", err)
	}
	sum := sha256.Sum256([]byte(content))
	id := sha256.Sum256([]byte(passID + "/capture.wav"))
	recordingID := hex.EncodeToString(id[:])

	if _, err := e.repo.InsertRecordingIfAbsent(ctx, store.Recording{
		RecordingID: recordingID, PassID: pass.ID, StationID: pass.StationID,
		RelativePath: "capture.wav", StoredPath: stored,
		SizeBytes: int64(len(content)), ChecksumSHA256: hex.EncodeToString(sum[:]),
		Status: "stored",
	}); err != nil {
		t.Fatalf("insert recording: %v", err)
	}
	return recordingID, stored
}

// scheduleOwnedPass requests a pass and returns its id.
func (e *testEnv) scheduleOwnedPass(t *testing.T, token, satelliteID string, visibility string) string {
	t.Helper()
	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	e.predictor.setPasses(passAt(aos, 10*time.Minute))
	code, body := e.schedulePass(t, token, satelliteID, aos, map[string]any{"visibility": visibility})
	if code != http.StatusCreated {
		t.Fatalf("request pass = %d %v", code, body)
	}
	return body["pass"].(map[string]any)["id"].(string)
}

// download returns the status and body of a raw GET, which the JSON helper
// cannot express.
func (e *testEnv) download(t *testing.T, path, token string) (int, string, http.Header) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, e.server.URL+path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := e.server.Client().Do(request)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(raw), response.Header
}

func TestOwnerListsAndDownloadsTheirRecording(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, ownerToken := env.createUser(t, rootToken, "owner", "user")

	passID := env.scheduleOwnedPass(t, ownerToken, satelliteID, "private")
	recordingID, _ := env.seedRecording(t, passID, "captured-signal")

	code, body := env.do(t, http.MethodGet, "/api/passes/"+passID+"/recordings", ownerToken, nil)
	if code != http.StatusOK {
		t.Fatalf("list = %d %v", code, body)
	}
	recordings := body["recordings"].([]any)
	if len(recordings) != 1 {
		t.Fatalf("recordings = %d, want 1", len(recordings))
	}
	first := recordings[0].(map[string]any)
	if first["recording_id"] != recordingID {
		t.Errorf("recording_id = %v", first["recording_id"])
	}
	// The client follows this link rather than building one.
	url, _ := first["download_url"].(string)
	if url != "/api/recordings/"+recordingID+"/download" {
		t.Fatalf("download_url = %q", url)
	}
	// The stored path is an internal detail and must not be published.
	if _, present := first["stored_path"]; present {
		t.Error("the response discloses the server filesystem path")
	}

	status, content, headers := env.download(t, url, ownerToken)
	if status != http.StatusOK {
		t.Fatalf("download = %d %q", status, content)
	}
	if content != "captured-signal" {
		t.Errorf("content = %q", content)
	}
	if got := headers.Get("Content-Disposition"); got != `attachment; filename="capture.wav"` {
		t.Errorf("Content-Disposition = %q", got)
	}
}

func TestRecordingsOfAPrivatePassAreHiddenFromOtherUsers(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, ownerToken := env.createUser(t, rootToken, "recordingowner", "user")
	_, strangerToken := env.createUser(t, rootToken, "recordingstranger", "user")

	passID := env.scheduleOwnedPass(t, ownerToken, satelliteID, "private")
	recordingID, _ := env.seedRecording(t, passID, "private-signal")

	// The listing is 404, matching the pass: even existence is private.
	if code, _ := env.do(t, http.MethodGet, "/api/passes/"+passID+"/recordings", strangerToken, nil); code != http.StatusNotFound {
		t.Errorf("stranger listing = %d, want 404", code)
	}
	// A leaked link does not work either, because the right comes from the
	// pass and not from knowing the id.
	status, content, _ := env.download(t, "/api/recordings/"+recordingID+"/download", strangerToken)
	if status != http.StatusNotFound {
		t.Errorf("stranger download = %d, want 404", status)
	}
	if content == "private-signal" {
		t.Error("a stranger received the recording content")
	}
	// Root oversees everything.
	if status, _, _ := env.download(t, "/api/recordings/"+recordingID+"/download", rootToken); status != http.StatusOK {
		t.Errorf("root download = %d, want 200", status)
	}
}

func TestPublicPassRecordingsAreReadableByAnySignedInUser(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, ownerToken := env.createUser(t, rootToken, "publicrecowner", "user")
	_, otherToken := env.createUser(t, rootToken, "publicreconlooker", "user")

	passID := env.scheduleOwnedPass(t, ownerToken, satelliteID, "public")
	env.seedRecording(t, passID, "shared-signal")

	code, body := env.do(t, http.MethodGet, "/api/passes/"+passID+"/recordings", otherToken, nil)
	if code != http.StatusOK {
		t.Fatalf("list = %d %v", code, body)
	}
	if len(body["recordings"].([]any)) != 1 {
		t.Errorf("recordings = %v", body["recordings"])
	}
}

func TestRecordingDownloadRequiresAuthentication(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, ownerToken := env.createUser(t, rootToken, "authowner", "user")

	passID := env.scheduleOwnedPass(t, ownerToken, satelliteID, "public")
	recordingID, _ := env.seedRecording(t, passID, "signal")

	if status, _, _ := env.download(t, "/api/recordings/"+recordingID+"/download", ""); status != http.StatusUnauthorized {
		t.Errorf("anonymous download = %d, want 401", status)
	}
}

// A recording id is a hash, so anything else is a bad request rather than a
// database lookup or, worse, a path.
func TestMalformedRecordingIDIsRejected(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)

	for _, id := range []string{"../../etc/passwd", "not-a-hash", ""} {
		status, _, _ := env.download(t, "/api/recordings/"+id+"/download", rootToken)
		if status != http.StatusBadRequest && status != http.StatusNotFound {
			t.Errorf("download %q = %d, want 400 or 404", id, status)
		}
	}
}

// The metadata can outlive the file, for instance after a disk is replaced.
// That must be reported, not served as an empty body.
func TestMissingRecordingContentIsReported(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, ownerToken := env.createUser(t, rootToken, "gonefileowner", "user")

	passID := env.scheduleOwnedPass(t, ownerToken, satelliteID, "private")
	recordingID, stored := env.seedRecording(t, passID, "temporary")
	if err := os.Remove(stored); err != nil {
		t.Fatalf("remove recording: %v", err)
	}

	status, body, _ := env.download(t, "/api/recordings/"+recordingID+"/download", ownerToken)
	if status != http.StatusNotFound {
		t.Errorf("download = %d, want 404", status)
	}
	if body == "" {
		t.Error("the response body is empty; the client cannot tell why")
	}
}

// The recordings listing (client-spec section 20). Visibility is applied in
// the query, so a private recording cannot appear in someone else's list.

func TestRecordingsListingRespectsVisibility(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, ownerToken := env.createUser(t, rootToken, "listowner", "user")
	_, strangerToken := env.createUser(t, rootToken, "liststranger", "user")

	privateID := env.scheduleOwnedPass(t, ownerToken, satelliteID, "private")
	env.seedRecording(t, privateID, "private-content")

	// The owner sees their own.
	code, body := env.do(t, http.MethodGet, "/api/recordings", ownerToken, nil)
	if code != http.StatusOK {
		t.Fatalf("owner list = %d %v", code, body)
	}
	if len(body["recordings"].([]any)) != 1 {
		t.Fatalf("owner sees %v", body["recordings"])
	}
	first := body["recordings"].([]any)[0].(map[string]any)
	// Enough to read without another request per row.
	for _, field := range []string{"satellite_id", "pass_aos", "visibility", "download_url"} {
		if _, present := first[field]; !present {
			t.Errorf("the listing is missing %q", field)
		}
	}

	// A stranger sees nothing at all, in either scope.
	for _, scope := range []string{"", "?scope=mine", "?scope=public"} {
		_, seen := env.do(t, http.MethodGet, "/api/recordings"+scope, strangerToken, nil)
		if len(seen["recordings"].([]any)) != 0 {
			t.Errorf("a stranger sees %v with scope %q", seen["recordings"], scope)
		}
	}
	// And cannot ask for everything.
	if code, _ := env.do(t, http.MethodGet, "/api/recordings?scope=all", strangerToken, nil); code != http.StatusForbidden {
		t.Errorf("stranger scope=all = %d, want 403", code)
	}
	// Root can.
	_, all := env.do(t, http.MethodGet, "/api/recordings?scope=all", rootToken, nil)
	if len(all["recordings"].([]any)) != 1 {
		t.Errorf("root scope=all sees %v", all["recordings"])
	}
}

func TestPublicRecordingsAppearInThePublicScope(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, ownerToken := env.createUser(t, rootToken, "publiclistowner", "user")
	_, otherToken := env.createUser(t, rootToken, "publiclistviewer", "user")

	publicID := env.scheduleOwnedPass(t, ownerToken, satelliteID, "public")
	env.seedRecording(t, publicID, "shared-content")

	_, body := env.do(t, http.MethodGet, "/api/recordings?scope=public", otherToken, nil)
	if len(body["recordings"].([]any)) != 1 {
		t.Fatalf("public scope shows %v", body["recordings"])
	}
	// The listing says nothing about who owns it.
	entry := body["recordings"].([]any)[0].(map[string]any)
	if _, present := entry["requested_by"]; present {
		t.Error("the public listing names the pass owner")
	}
}

func TestRecordingsListingRejectsAnUnknownScope(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)

	code, body := env.do(t, http.MethodGet, "/api/recordings?scope=everything", rootToken, nil)
	if code != http.StatusBadRequest {
		t.Errorf("unknown scope = %d %v, want 400", code, body)
	}
}
