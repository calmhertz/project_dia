package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"aagasa/internal/store"
)

// SatDump pipelines (spec.md section 19.1). Standard ones come from the
// station's own installation; custom ones are uploaded JSON.

func TestPipelineListStartsEmptyUntilAStationReports(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)

	code, body := env.do(t, http.MethodGet, "/api/pipelines", rootToken, nil)
	if code != http.StatusOK {
		t.Fatalf("list = %d %v", code, body)
	}
	if len(body["pipelines"].([]any)) != 0 {
		t.Errorf("pipelines = %v, want none", body["pipelines"])
	}
	// Nothing to choose from reads differently from an empty catalogue.
	if body["reported_by_station"] != false {
		t.Errorf("reported_by_station = %v, want false", body["reported_by_station"])
	}
}

func TestStandardPipelinesComeFromTheStation(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	ctx := context.Background()

	if _, err := env.repo.ReplaceStandardPipelines(ctx,
		[]string{"noaa_apt", "meteor_m2-x_lrpt"}); err != nil {
		t.Fatalf("record inventory: %v", err)
	}

	code, body := env.do(t, http.MethodGet, "/api/pipelines", rootToken, nil)
	if code != http.StatusOK {
		t.Fatalf("list = %d %v", code, body)
	}
	pipelines := body["pipelines"].([]any)
	if len(pipelines) != 2 {
		t.Fatalf("pipelines = %d, want 2", len(pipelines))
	}
	for _, entry := range pipelines {
		if entry.(map[string]any)["is_custom"] != false {
			t.Errorf("a reported pipeline is marked custom: %v", entry)
		}
	}

	// A reinstall with fewer pipelines withdraws the ones that are gone.
	if _, err := env.repo.ReplaceStandardPipelines(ctx, []string{"noaa_apt"}); err != nil {
		t.Fatalf("second inventory: %v", err)
	}
	_, body = env.do(t, http.MethodGet, "/api/pipelines", rootToken, nil)
	if len(body["pipelines"].([]any)) != 1 {
		t.Errorf("after a smaller inventory: %v", body["pipelines"])
	}
}

// An empty report means SatDump could not be read, not that the station has
// none. Wiping the list on a failed read would break every future request.
func TestAnEmptyInventoryChangesNothing(t *testing.T) {
	env := newTestEnv(t)
	env.rootReady(t)
	ctx := context.Background()

	if _, err := env.repo.ReplaceStandardPipelines(ctx, []string{"noaa_apt"}); err != nil {
		t.Fatalf("record inventory: %v", err)
	}
	if _, err := env.repo.ReplaceStandardPipelines(ctx, nil); err != nil {
		t.Fatalf("empty inventory: %v", err)
	}

	pipelines, err := env.repo.ListPipelines(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pipelines) != 1 {
		t.Errorf("pipelines = %d, want the previous one kept", len(pipelines))
	}
}

func TestUploadingACustomPipeline(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	_, adminToken := env.createUser(t, rootToken, "pipelineadmin", "admin")
	_, userToken := env.createUser(t, rootToken, "pipelineuser", "user")

	definition := `{"my_apt": {"name": "My APT", "live": true, "work": {}}}`

	if code, _ := env.do(t, http.MethodPost, "/api/pipelines", userToken,
		map[string]any{"name": "My APT", "definition": definition}); code != http.StatusForbidden {
		t.Errorf("a normal user uploaded a pipeline: %d", code)
	}

	code, body := env.do(t, http.MethodPost, "/api/pipelines", adminToken,
		map[string]any{"name": "My APT", "definition": definition})
	if code != http.StatusCreated {
		t.Fatalf("upload = %d %v", code, body)
	}
	if body["is_custom"] != true {
		t.Errorf("is_custom = %v", body["is_custom"])
	}
	// The checksum identifies the content, so a pass can be tied to exactly
	// this definition.
	checksum, _ := body["checksum_sha256"].(string)
	if len(checksum) != 64 {
		t.Errorf("checksum = %q", checksum)
	}

	// It appears in the list, and the definition is not published there.
	_, list := env.do(t, http.MethodGet, "/api/pipelines", userToken, nil)
	found := false
	for _, entry := range list["pipelines"].([]any) {
		record := entry.(map[string]any)
		if record["name"] == "My APT" {
			found = true
			if _, present := record["definition"]; present {
				t.Error("the listing publishes the pipeline definition")
			}
		}
	}
	if !found {
		t.Error("the uploaded pipeline is missing from the list")
	}
}

// Structure only: SatDump's semantics are its own business, but something
// that cannot be a pipeline file at all is refused.
func TestMalformedPipelineJSONIsRefused(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)

	for name, definition := range map[string]string{
		"not json":      "{ this is not json",
		"an array":      `["noaa_apt"]`,
		"empty object":  `{}`,
		"not an object": `{"noaa_apt": "run it"}`,
	} {
		code, body := env.do(t, http.MethodPost, "/api/pipelines", rootToken,
			map[string]any{"name": name, "definition": definition})
		if code != http.StatusBadRequest {
			t.Errorf("%s = %d %v, want 400", name, code, body)
		}
		if body["error"] != "invalid_pipeline" && body["error"] != "invalid_request" {
			t.Errorf("%s error = %v", name, body["error"])
		}
	}
}

// A decoded result cannot be produced without saying how. Without this the
// Worker falls back to a raw capture and the user quietly gets something
// else.
func TestProcessingWithoutAPipelineIsRefused(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, userToken := env.createUser(t, rootToken, "decoder", "user")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))

	code, body := env.schedulePass(t, userToken, satelliteID, aos,
		map[string]any{"recording_mode": "process"})
	if code != http.StatusBadRequest {
		t.Fatalf("process without a pipeline = %d %v, want 400", code, body)
	}
	if body["error"] != "pipeline_required" {
		t.Errorf("error = %v, want pipeline_required", body["error"])
	}

	// With one, the same request is accepted and the plan carries it.
	pipeline, err := env.repo.CreatePipeline(context.Background(), store.Pipeline{
		Name: "noaa_apt", IsCustom: false}, nil)
	if err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	code, body = env.schedulePass(t, userToken, satelliteID, aos, map[string]any{
		"recording_mode": "process", "pipeline_id": pipeline.ID.String()})
	if code != http.StatusCreated {
		t.Fatalf("process with a pipeline = %d %v", code, body)
	}

	passID := body["pass"].(map[string]any)["id"].(string)
	if code, _ := env.do(t, http.MethodPost, "/api/passes/"+passID+"/approve", rootToken, nil); code != http.StatusOK {
		t.Fatalf("approve failed")
	}
	_, plan := env.do(t, http.MethodGet, "/api/passes/"+passID+"/plan", rootToken, nil)
	if plan["pipeline"] != "noaa_apt" {
		t.Errorf("the plan carries pipeline %v, want noaa_apt", plan["pipeline"])
	}
}

// Raw needs no pipeline, and must not start demanding one.
func TestRawRecordingNeedsNoPipeline(t *testing.T) {
	env := newTestEnv(t)
	rootToken := env.rootReady(t)
	env.setupStation(t, rootToken)
	satelliteID := env.seedSatellite(t)
	_, userToken := env.createUser(t, rootToken, "rawuser", "user")

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	env.predictor.setPasses(passAt(aos, 10*time.Minute))

	if code, body := env.schedulePass(t, userToken, satelliteID, aos, nil); code != http.StatusCreated {
		t.Fatalf("raw request = %d %v", code, body)
	}
}
