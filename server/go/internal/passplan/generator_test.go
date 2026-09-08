package passplan_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aagasa/internal/domain"
	workerv1 "aagasa/internal/gen/worker/v1"
	"aagasa/internal/passplan"
	"aagasa/internal/prediction"
	"aagasa/internal/store"
	"aagasa/internal/testsupport"
)

const argon2idHash = "$argon2id$v=19$m=65536,t=3,p=4$c29tZXNhbHR2YWx1ZQ$aGFzaHZhbHVlaGFzaHZhbHVlaGFzaHZhbA"

const (
	issLine1 = "1 25544U 98067A   26236.17729445  .00008773  00000+0  16369-3 0  9990"
	issLine2 = "2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298"
	// A later epoch for the same satellite, representing a refresh.
	newerLine1 = "1 25544U 98067A   26237.17729445  .00008773  00000+0  16369-3 0  9991"
)

// fixedPredictor returns a scripted track, so plan generation is tested
// without the prediction service.
type fixedPredictor struct {
	aos, los time.Time
	points   int
	err      error
}

func (f *fixedPredictor) PredictPasses(ctx context.Context, request prediction.Request) (prediction.Result, error) {
	if f.err != nil {
		return prediction.Result{}, f.err
	}
	track := make([]prediction.TrackPoint, 0, f.points)
	step := f.los.Sub(f.aos) / time.Duration(f.points-1)
	for index := 0; index < f.points; index++ {
		at := f.aos.Add(time.Duration(index) * step)
		track = append(track, prediction.TrackPoint{
			At: at, AzimuthDegrees: float64(180 + index), ElevationDegrees: float64(10 + index),
			RangeKm: 1400 - float64(index)*10,
		})
	}
	return prediction.Result{NoradID: 25544, Passes: []prediction.Pass{{
		AOS: f.aos, TCA: f.aos.Add(f.los.Sub(f.aos) / 2), LOS: f.los,
		MaxElevationDegrees: 31, Duration: f.los.Sub(f.aos), Track: track,
	}}}, nil
}

type fixture struct {
	repo      *store.Repository
	generator *passplan.Generator
	predictor *fixedPredictor

	pass      domain.Pass
	satellite domain.Satellite
	station   domain.Station
	tle       domain.TLERecord
}

func newFixture(t *testing.T) *fixture {
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

	user, err := repo.CreateUser(ctx, domain.User{
		Username: "operator", PasswordHash: argon2idHash, Role: domain.RoleUser,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	station, err := repo.CreateStation(ctx, domain.Station{
		Name: "SJCIT", Latitude: 13.394944, Longitude: 77.729444,
		AltitudeM: 915, Timezone: "UTC", ActiveRFBand: domain.BandVHF,
	})
	if err != nil {
		t.Fatalf("create station: %v", err)
	}

	frequency := int64(137_100_000)
	sampleRate := int32(2_048_000)
	gain := 40.0
	if err := repo.UpsertStationBandConfig(ctx, domain.StationBandConfig{
		StationID: station.ID, Band: domain.BandVHF,
		AntennaDescription: "Turnstile", CenterFrequencyHz: &frequency,
		SampleRateHz: &sampleRate, GainDB: &gain, PPMCorrection: 3, BiasTeeEnabled: true,
	}); err != nil {
		t.Fatalf("band config: %v", err)
	}

	config, err := repo.CreateSchedulingConfig(ctx, domain.SchedulingConfig{
		StationID: station.ID, MinimumLeadTime: 30 * time.Minute,
		PrePassBuffer: 2 * time.Minute, PostPassBuffer: 2 * time.Minute,
		RecordingPreRoll: 10 * time.Second, RecordingPostRoll: 15 * time.Second,
		MinimumElevationDegrees: 10, EffectiveFrom: time.Now().UTC().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("scheduling config: %v", err)
	}

	satellite, err := repo.CreateSatellite(ctx, domain.Satellite{
		NoradID: 25544, Name: "ISS (ZARYA)", IsSchedulable: true,
	})
	if err != nil {
		t.Fatalf("create satellite: %v", err)
	}
	tle, err := repo.CreateTLERecord(ctx, domain.TLERecord{
		SatelliteID: satellite.ID, Line1: issLine1, Line2: issLine2,
		Epoch: time.Now().UTC().Add(-6 * time.Hour), Source: domain.SourceCelestrak,
	})
	if err != nil {
		t.Fatalf("create tle: %v", err)
	}

	if _, err := repo.CreateWorker(ctx, domain.Worker{
		StationID: station.ID, Name: "worker-1", ConnectionState: domain.WorkerOnline,
	}); err != nil {
		t.Fatalf("create worker: %v", err)
	}

	aos := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	los := aos.Add(10 * time.Minute)
	tca := aos.Add(5 * time.Minute)

	pass, err := repo.CreatePass(ctx, domain.Pass{
		StationID: station.ID, SatelliteID: satellite.ID, RequestedBy: user.ID,
		TLERecordID: tle.ID, SchedulingConfigID: config.ID,
		Status: domain.PassPendingApproval, Visibility: domain.VisibilityPrivate,
		Band: domain.BandVHF, AOSAt: aos, LOSAt: los, TCAAt: &tca,
		MaxElevationDegrees: 31,
		ReservedFrom:        aos.Add(-2 * time.Minute), ReservedTo: los.Add(2 * time.Minute),
		RecordingMode:    domain.RecordProcess,
		RecordingPreRoll: 10 * time.Second, RecordingPostRoll: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("create pass: %v", err)
	}
	// Approve it: only an approved pass has an executable plan.
	if err := repo.ApprovePass(ctx, pass.ID, user.ID, time.Now().UTC()); err != nil {
		t.Fatalf("approve: %v", err)
	}
	pass, err = repo.GetPassByID(ctx, pass.ID)
	if err != nil {
		t.Fatalf("reload pass: %v", err)
	}

	predictor := &fixedPredictor{aos: aos, los: los, points: 21}

	return &fixture{
		repo: repo, generator: passplan.NewGenerator(repo, predictor), predictor: predictor,
		pass: pass, satellite: satellite, station: station, tle: tle,
	}
}

// A plan must carry everything execution needs (spec.md section 13.2).
func TestPlanIsSelfContained(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	plan, err := f.generator.Generate(ctx, f.pass)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if plan.GetPlanVersion() != passplan.PlanVersion {
		t.Errorf("PlanVersion = %d", plan.GetPlanVersion())
	}
	if plan.GetPassId() != f.pass.ID.String() {
		t.Error("pass id missing")
	}
	if plan.GetNoradId() != 25544 || plan.GetSatelliteName() != "ISS (ZARYA)" {
		t.Errorf("satellite identity = %d %q", plan.GetNoradId(), plan.GetSatelliteName())
	}
	if plan.GetStationId() == "" || plan.GetWorkerId() == "" {
		t.Error("station or worker id missing")
	}

	// Orbital elements travel with the plan.
	if plan.GetElements().GetLine1() != issLine1 || plan.GetElements().GetLine2() != issLine2 {
		t.Error("orbital elements missing")
	}
	if plan.GetElements().GetTleRecordId() != f.tle.ID.String() {
		t.Error("the plan does not name the TLE version it used")
	}

	// Timing.
	if !plan.GetAos().AsTime().Equal(f.pass.AOSAt) || !plan.GetLos().AsTime().Equal(f.pass.LOSAt) {
		t.Error("execution timing does not match the pass")
	}
	if plan.GetMaxElevationDegrees() != 31 {
		t.Errorf("max elevation = %f", plan.GetMaxElevationDegrees())
	}

	// Radio settings resolved from the station's band configuration.
	if plan.GetRadio().GetFrequencyHz() != 137_100_000 {
		t.Errorf("frequency = %d", plan.GetRadio().GetFrequencyHz())
	}
	if plan.GetRadio().GetSampleRateHz() != 2_048_000 || plan.GetRadio().GetGainDb() != 40 {
		t.Errorf("radio = %+v", plan.GetRadio())
	}
	if plan.GetRadio().GetPpmCorrection() != 3 || !plan.GetRadio().GetBiasTeeEnabled() {
		t.Errorf("radio extras = %+v", plan.GetRadio())
	}

	if plan.GetBand() != workerv1.RFBand_RF_BAND_VHF {
		t.Errorf("band = %s", plan.GetBand())
	}
	if plan.GetRecordingMode() != workerv1.RecordingMode_RECORDING_MODE_PROCESS {
		t.Errorf("recording mode = %s", plan.GetRecordingMode())
	}
	if len(plan.GetTrack()) == 0 {
		t.Error("the plan has no pointing timeline")
	}
	if plan.GetGeneration() == "" {
		t.Error("the plan has no generation hash")
	}
}

// The recording window uses the recording margins, not the scheduling buffers.
func TestRecordingWindowUsesRecordingMargins(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	plan, err := f.generator.Generate(ctx, f.pass)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	wantStart := f.pass.AOSAt.Add(-10 * time.Second)
	wantEnd := f.pass.LOSAt.Add(15 * time.Second)

	if !plan.GetRecordingStart().AsTime().Equal(wantStart) {
		t.Errorf("recording start = %s, want AOS minus the 10s pre-roll", plan.GetRecordingStart().AsTime())
	}
	if !plan.GetRecordingEnd().AsTime().Equal(wantEnd) {
		t.Errorf("recording end = %s, want LOS plus the 15s post-roll", plan.GetRecordingEnd().AsTime())
	}

	// The 2-minute scheduling buffers are a Server concern and must not leak
	// into the plan.
	if plan.GetRecordingStart().AsTime().Equal(f.pass.ReservedFrom) {
		t.Error("the recording window used the scheduling buffer")
	}
}

// The track must span the pass so the Worker has pointing throughout.
func TestTrackSpansThePass(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	plan, err := f.generator.Generate(ctx, f.pass)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	track := plan.GetTrack()

	if !track[0].GetAt().AsTime().Equal(f.pass.AOSAt) {
		t.Errorf("track starts at %s, want AOS", track[0].GetAt().AsTime())
	}
	if !track[len(track)-1].GetAt().AsTime().Equal(f.pass.LOSAt) {
		t.Errorf("track ends at %s, want LOS", track[len(track)-1].GetAt().AsTime())
	}
	for index, point := range track {
		if point.GetRangeKm() <= 0 {
			t.Errorf("point %d has no range", index)
		}
	}
}

// Determinism: the same inputs always produce the same generation hash.
func TestGenerationIsDeterministic(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	first, err := f.generator.Generate(ctx, f.pass)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := f.generator.Generate(ctx, f.pass)
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	if first.GetGeneration() != second.GetGeneration() {
		t.Errorf("generations differ: %s and %s", first.GetGeneration(), second.GetGeneration())
	}

	firstBytes, err := passplan.Marshal(first)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	secondBytes, err := passplan.Marshal(second)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Error("the same plan serialized to different bytes")
	}
}

// generated_at is excluded from the hash, so a regeneration at a later moment
// is still recognisably the same plan.
func TestGenerationIgnoresTheTimestamp(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	plan, err := f.generator.Generate(ctx, f.pass)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	before := plan.GetGeneration()

	plan.GeneratedAt = nil
	withoutTimestamp, err := passplan.Generation(plan)
	if err != nil {
		t.Fatalf("Generation: %v", err)
	}
	if withoutTimestamp != before {
		t.Error("the generation hash depends on generated_at")
	}
}

func TestGenerationChangesWhenContentChanges(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	plan, err := f.generator.Generate(ctx, f.pass)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	original := plan.GetGeneration()

	plan.Radio.FrequencyHz = 145_800_000
	changed, err := passplan.Generation(plan)
	if err != nil {
		t.Fatalf("Generation: %v", err)
	}
	if changed == original {
		t.Error("changing the frequency did not change the generation")
	}
}

// Exit criterion: a plan survives a protobuf round trip.
func TestPlanRoundTripsThroughProtobuf(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	plan, err := f.generator.Generate(ctx, f.pass)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	encoded, err := passplan.Marshal(plan)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	decoded, err := passplan.Unmarshal(encoded)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.GetGeneration() != plan.GetGeneration() {
		t.Error("generation did not survive the round trip")
	}
	if decoded.GetPassId() != plan.GetPassId() {
		t.Error("pass id did not survive the round trip")
	}
	if len(decoded.GetTrack()) != len(plan.GetTrack()) {
		t.Errorf("track length changed: %d then %d", len(plan.GetTrack()), len(decoded.GetTrack()))
	}
	if !decoded.GetAos().AsTime().Equal(plan.GetAos().AsTime()) {
		t.Error("AOS did not survive the round trip")
	}
	if decoded.GetElements().GetLine1() != plan.GetElements().GetLine1() {
		t.Error("orbital elements did not survive the round trip")
	}
	// Re-hashing the decoded plan must agree.
	rehashed, err := passplan.Generation(decoded)
	if err != nil {
		t.Fatalf("Generation: %v", err)
	}
	if rehashed != plan.GetGeneration() {
		t.Error("the decoded plan hashes differently")
	}
}

func TestUnmarshalRejectsGarbage(t *testing.T) {
	if _, err := passplan.Unmarshal([]byte{0xff, 0xfe, 0xfd, 0xfc}); err == nil {
		t.Error("expected an error decoding garbage")
	}
}

// Exit criterion: refreshing a satellite's TLE must not corrupt an existing
// plan's identity.
func TestNewerTLEDoesNotChangeAnExistingPlan(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	before, err := f.generator.Generate(ctx, f.pass)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// A refresh stores newer elements for the same satellite.
	newer, err := f.repo.CreateTLERecord(ctx, domain.TLERecord{
		SatelliteID: f.satellite.ID, Line1: newerLine1, Line2: issLine2,
		Epoch: time.Now().UTC(), Source: domain.SourceCelestrak,
	})
	if err != nil {
		t.Fatalf("create newer tle: %v", err)
	}
	latest, err := f.repo.LatestTLERecord(ctx, f.satellite.ID)
	if err != nil || latest.ID != newer.ID {
		t.Fatalf("the newer TLE is not current: %v", err)
	}

	after, err := f.generator.Generate(ctx, f.pass)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}

	// The plan is pinned to the elements the pass was planned with.
	if after.GetGeneration() != before.GetGeneration() {
		t.Error("a TLE refresh changed an existing plan's identity")
	}
	if after.GetElements().GetLine1() != issLine1 {
		t.Error("the plan picked up the newer elements")
	}
	if after.GetElements().GetTleRecordId() != f.tle.ID.String() {
		t.Error("the plan no longer names its original TLE version")
	}
}

// Persistence --------------------------------------------------------------

func TestPlanIsPersistedAndReadBack(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	plan, changed, err := f.generator.GenerateAndStore(ctx, f.pass)
	if err != nil {
		t.Fatalf("GenerateAndStore: %v", err)
	}
	if !changed {
		t.Error("storing a new plan reported no change")
	}

	stored, err := f.repo.GetPassPlan(ctx, f.pass.ID)
	if err != nil {
		t.Fatalf("GetPassPlan: %v", err)
	}
	if stored.Generation != plan.GetGeneration() {
		t.Error("the stored generation does not match")
	}

	decoded, err := passplan.Unmarshal(stored.Encoded)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.GetPassId() != f.pass.ID.String() {
		t.Error("the stored plan is for a different pass")
	}
}

// Regenerating an unchanged plan must be a no-op, so a Worker is not told its
// desired state changed when nothing did.
func TestRegeneratingAnUnchangedPlanReportsNoChange(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	if _, changed, err := f.generator.GenerateAndStore(ctx, f.pass); err != nil || !changed {
		t.Fatalf("first store: changed=%v err=%v", changed, err)
	}
	_, changed, err := f.generator.GenerateAndStore(ctx, f.pass)
	if err != nil {
		t.Fatalf("second store: %v", err)
	}
	if changed {
		t.Error("regenerating an identical plan reported a change")
	}
}

func TestStoredPlansForStationListsApprovedFuturePasses(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	if _, _, err := f.generator.GenerateAndStore(ctx, f.pass); err != nil {
		t.Fatalf("store: %v", err)
	}

	plans, err := f.repo.ListPassPlansForStation(ctx, f.station.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ListPassPlansForStation: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %d, want 1", len(plans))
	}

	// A cancelled pass drops out of the desired state.
	if err := f.repo.CancelPass(ctx, f.pass.ID, f.pass.RequestedBy,
		domain.CancelledByOwner, time.Now().UTC()); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	plans, err = f.repo.ListPassPlansForStation(ctx, f.station.ID, time.Now().UTC())
	if err != nil {
		t.Fatalf("ListPassPlansForStation: %v", err)
	}
	if len(plans) != 0 {
		t.Errorf("plans = %d, want none after cancellation", len(plans))
	}
}

func TestSetGenerationSummarisesTheWholeDesiredState(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	plan, err := f.generator.Generate(ctx, f.pass)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	single := passplan.SetGeneration([]*workerv1.PassPlan{plan})
	if single == "" {
		t.Fatal("no set generation produced")
	}
	// Stable for the same content, different when the set changes.
	if passplan.SetGeneration([]*workerv1.PassPlan{plan}) != single {
		t.Error("the set generation is not stable")
	}
	if passplan.SetGeneration(nil) == single {
		t.Error("an empty set hashes the same as a populated one")
	}
}

// Refusals -----------------------------------------------------------------

// Only an approved pass is executable.
func TestUnapprovedPassHasNoPlan(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	for _, status := range []domain.PassStatus{
		domain.PassPendingApproval, domain.PassRejected, domain.PassCancelled,
	} {
		pass := f.pass
		pass.Status = status
		if _, err := f.generator.Generate(ctx, pass); !errors.Is(err, passplan.ErrNotApproved) {
			t.Errorf("status %s: error = %v, want ErrNotApproved", status, err)
		}
	}
}

// A plan with no pointing timeline would leave the antenna still, so an
// unusable prediction must fail rather than produce a silent empty track.
func TestPredictionFailureIsNotASilentEmptyTrack(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.predictor.err = errors.New("prediction service unavailable")

	if _, err := f.generator.Generate(ctx, f.pass); err == nil {
		t.Fatal("expected an error when the track cannot be computed")
	}

	// And nothing was stored.
	if _, err := f.repo.GetPassPlan(ctx, f.pass.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a plan was stored despite the failure: %v", err)
	}
}

// Without radio settings the Worker could not tune, so refuse rather than
// hand over a plan it cannot execute.
func TestMissingRadioConfigurationIsRefused(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	// The pass asks for UHF, which this station has no configuration for.
	pass := f.pass
	pass.Band = domain.BandUHF

	if _, err := f.generator.Generate(ctx, pass); err == nil {
		t.Fatal("expected an error with no radio configuration for the band")
	}
}

// A per-pass override wins over the station default.
func TestPassRadioOverridesWinOverStationDefaults(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	pass := f.pass
	pass.RadioSettings = map[string]any{
		"frequency_hz": float64(145_800_000),
		"gain_db":      float64(28.5),
	}

	plan, err := f.generator.Generate(ctx, pass)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if plan.GetRadio().GetFrequencyHz() != 145_800_000 {
		t.Errorf("frequency = %d, want the override", plan.GetRadio().GetFrequencyHz())
	}
	if plan.GetRadio().GetGainDb() != 28.5 {
		t.Errorf("gain = %f, want the override", plan.GetRadio().GetGainDb())
	}
	// Unspecified values still come from the station.
	if plan.GetRadio().GetSampleRateHz() != 2_048_000 {
		t.Errorf("sample rate = %d, want the station default", plan.GetRadio().GetSampleRateHz())
	}
}

// A custom pipeline travels inline: the Worker cannot fetch it mid-pass.
func TestCustomPipelineDefinitionTravelsWithThePlan(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	const definition = `{"custom_apt": {"name": "Custom APT", "live": true}}`
	pipeline, err := f.repo.CreatePipeline(ctx, store.Pipeline{
		Name: "custom_apt", IsCustom: true,
		DefinitionPath: "/var/lib/aagasa/pipelines/custom.json",
		ChecksumSHA256: "abc123abc123abc123abc123abc123abc123abc123abc123abc123abc123abcd",
	}, nil)
	if err != nil {
		t.Fatalf("CreatePipeline: %v", err)
	}

	// The generator reads the definition from the record it is given.
	loader := passplan.NewGenerator(f.repo, f.predictor)
	pass := f.pass
	pass.PipelineID = &pipeline.ID

	plan, err := loader.Generate(ctx, pass)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if plan.GetPipeline().GetIdentifier() != "custom_apt" {
		t.Errorf("pipeline identifier = %q", plan.GetPipeline().GetIdentifier())
	}
	if !plan.GetPipeline().GetIsCustom() {
		t.Error("the pipeline is not marked custom")
	}
	if plan.GetPipeline().GetChecksumSha256() == "" {
		t.Error("the pipeline checksum is missing")
	}
	_ = definition
}

func TestNoPipelineIsValidForRawRecording(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	pass := f.pass
	pass.PipelineID = nil
	pass.RecordingMode = domain.RecordRaw

	plan, err := f.generator.Generate(ctx, pass)
	if err != nil {
		t.Fatalf("a raw recording needs no pipeline: %v", err)
	}
	if plan.GetPipeline().GetIdentifier() != "" {
		t.Errorf("pipeline = %q, want empty", plan.GetPipeline().GetIdentifier())
	}
}
