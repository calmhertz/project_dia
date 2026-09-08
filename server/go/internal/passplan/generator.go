// Package passplan turns an approved pass into the self-contained instruction
// the Worker executes.
//
// The Worker may be running through a Server outage when the pass comes up, so
// a plan must carry everything execution needs and nothing that would require
// a call back (spec.md section 13.2).
package passplan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"aagasa/internal/domain"
	workerv1 "aagasa/internal/gen/worker/v1"
	"aagasa/internal/prediction"
	"aagasa/internal/store"
)

// PlanVersion is the schema version stamped into every plan. Raise it when a
// change would make an older Worker misread a plan.
const PlanVersion = 1

// TrackStep is the spacing of the pointing timeline.
//
// Five seconds keeps a low-Earth-orbit pass to roughly a hundred points while
// staying well inside the one-degree pointing accuracy a G-550 can hold.
const TrackStep = 5 * time.Second

// ErrNotApproved means the pass is not in a state that can be executed.
var ErrNotApproved = errors.New("only an approved pass has an executable plan")

// ErrStationNotConfigured means the station is missing a setting the plan
// needs. It is an operator's job to fix, not a fault, so callers report it as
// such rather than as an internal error.
var ErrStationNotConfigured = errors.New("the station is not fully configured")

// Predictor supplies the pointing timeline.
type Predictor interface {
	PredictPasses(ctx context.Context, request prediction.Request) (prediction.Result, error)
}

// Generator builds plans.
type Generator struct {
	repo      *store.Repository
	predictor Predictor
}

// NewGenerator builds a plan generator.
func NewGenerator(repo *store.Repository, predictor Predictor) *Generator {
	return &Generator{repo: repo, predictor: predictor}
}

// Generate builds the executable plan for a pass.
//
// It is deterministic: the same pass, elements, station and configuration
// always produce the same generation hash, so an unchanged plan is
// recognisable without comparing it field by field.
func (g *Generator) Generate(ctx context.Context, pass domain.Pass) (*workerv1.PassPlan, error) {
	if pass.Status != domain.PassApproved {
		return nil, fmt.Errorf("%w: pass is %s", ErrNotApproved, pass.Status)
	}

	satellite, err := g.repo.GetSatelliteByID(ctx, pass.SatelliteID)
	if err != nil {
		return nil, fmt.Errorf("load satellite: %w", err)
	}
	station, err := g.repo.GetStationByID(ctx, pass.StationID)
	if err != nil {
		return nil, fmt.Errorf("load station: %w", err)
	}
	// The plan is built from the TLE the pass was planned with, not the
	// newest one, so a later refresh cannot alter an existing plan.
	elements, err := g.repo.GetTLERecordByID(ctx, pass.TLERecordID)
	if err != nil {
		return nil, fmt.Errorf("load orbital data: %w", err)
	}
	worker, err := g.repo.GetStationWorker(ctx, pass.StationID)
	if err != nil {
		return nil, fmt.Errorf("load worker: %w", err)
	}

	radio, err := g.radioFor(ctx, station, pass)
	if err != nil {
		return nil, err
	}
	pipeline, err := g.pipelineFor(ctx, pass)
	if err != nil {
		return nil, err
	}

	track, err := g.trackFor(ctx, station, pass, elements)
	if err != nil {
		return nil, err
	}

	plan := &workerv1.PassPlan{
		PlanVersion: PlanVersion,
		PassId:      pass.ID.String(),
		StationId:   pass.StationID.String(),
		WorkerId:    worker.ID.String(),

		NoradId:       int32(satellite.NoradID),
		SatelliteName: satellite.Name,

		Elements: &workerv1.OrbitalElements{
			Line1:       elements.Line1,
			Line2:       elements.Line2,
			Epoch:       timestamppb.New(elements.Epoch.UTC()),
			TleRecordId: elements.ID.String(),
		},

		Aos:                 timestamppb.New(pass.AOSAt.UTC()),
		Los:                 timestamppb.New(pass.LOSAt.UTC()),
		MaxElevationDegrees: pass.MaxElevationDegrees,

		// Recording margins, not the scheduling buffers: the Worker has no
		// business knowing how the Server reserves the station.
		RecordingStart: timestamppb.New(pass.AOSAt.Add(-pass.RecordingPreRoll).UTC()),
		RecordingEnd:   timestamppb.New(pass.LOSAt.Add(pass.RecordingPostRoll).UTC()),

		Band:          bandToProto(pass.Band),
		Radio:         radio,
		RecordingMode: recordingModeToProto(pass.RecordingMode),
		Pipeline:      pipeline,
		Track:         track,
	}
	if pass.TCAAt != nil {
		plan.Tca = timestamppb.New(pass.TCAAt.UTC())
	}

	generation, err := Generation(plan)
	if err != nil {
		return nil, err
	}
	plan.Generation = generation
	return plan, nil
}

// GenerateAndStore builds a plan and persists it, reporting whether the stored
// plan changed.
func (g *Generator) GenerateAndStore(ctx context.Context, pass domain.Pass) (*workerv1.PassPlan, bool, error) {
	plan, err := g.Generate(ctx, pass)
	if err != nil {
		return nil, false, err
	}

	// generated_at is stamped at store time and deliberately excluded from the
	// generation hash, so regenerating an unchanged plan stays a no-op.
	plan.GeneratedAt = timestamppb.New(time.Now().UTC())

	encoded, err := Marshal(plan)
	if err != nil {
		return nil, false, err
	}

	changed, err := g.repo.UpsertPassPlan(ctx, store.StoredPassPlan{
		PassID:      pass.ID,
		Generation:  plan.GetGeneration(),
		PlanVersion: int(plan.GetPlanVersion()),
		Encoded:     encoded,
		GeneratedAt: plan.GetGeneratedAt().AsTime(),
	})
	if err != nil {
		return nil, false, err
	}
	return plan, changed, nil
}

// trackFor computes the pointing timeline for the pass window.
func (g *Generator) trackFor(ctx context.Context, station domain.Station,
	pass domain.Pass, elements domain.TLERecord) ([]*workerv1.TrackPoint, error) {
	config, err := g.repo.GetSchedulingConfigByID(ctx, pass.SchedulingConfigID)
	if err != nil {
		return nil, fmt.Errorf("load scheduling config: %w", err)
	}

	// Bracket the pass so the predictor returns it whole, then match on AOS.
	result, err := g.predictor.PredictPasses(ctx, prediction.Request{
		Line1:                   elements.Line1,
		Line2:                   elements.Line2,
		LatitudeDegrees:         station.Latitude,
		LongitudeDegrees:        station.Longitude,
		AltitudeM:               station.AltitudeM,
		MinimumElevationDegrees: config.MinimumElevationDegrees,
		SearchStart:             pass.AOSAt.Add(-time.Hour),
		SearchEnd:               pass.LOSAt.Add(time.Hour),
		TrackStep:               TrackStep,
	})
	if err != nil {
		return nil, fmt.Errorf("compute track: %w", err)
	}

	for _, candidate := range result.Passes {
		difference := candidate.AOS.Sub(pass.AOSAt)
		if difference < 0 {
			difference = -difference
		}
		if difference > time.Minute {
			continue
		}
		points := make([]*workerv1.TrackPoint, 0, len(candidate.Track))
		for _, point := range candidate.Track {
			points = append(points, &workerv1.TrackPoint{
				At:               timestamppb.New(point.At.UTC()),
				AzimuthDegrees:   point.AzimuthDegrees,
				ElevationDegrees: point.ElevationDegrees,
				RangeKm:          point.RangeKm,
			})
		}
		return points, nil
	}

	// A plan with no pointing timeline would leave the antenna still, so this
	// is a failure rather than an empty track.
	return nil, fmt.Errorf("no predicted pass matches the stored pass window")
}

// radioFor resolves the radio settings for the pass's band.
//
// A pass may override the station defaults through its radio_settings; those
// values win, and anything absent falls back to the station's band config.
func (g *Generator) radioFor(ctx context.Context, station domain.Station,
	pass domain.Pass) (*workerv1.RadioSettings, error) {
	configs, err := g.repo.GetStationBandConfigs(ctx, station.ID)
	if err != nil {
		return nil, fmt.Errorf("load band configuration: %w", err)
	}

	radio := &workerv1.RadioSettings{Source: "rtlsdr"}
	for _, config := range configs {
		if config.Band != pass.Band {
			continue
		}
		if config.CenterFrequencyHz != nil {
			radio.FrequencyHz = *config.CenterFrequencyHz
		}
		if config.SampleRateHz != nil {
			radio.SampleRateHz = int64(*config.SampleRateHz)
		}
		if config.GainDB != nil {
			radio.GainDb = *config.GainDB
		}
		radio.PpmCorrection = config.PPMCorrection
		radio.BiasTeeEnabled = config.BiasTeeEnabled
		break
	}

	applyRadioOverrides(radio, pass.RadioSettings)

	if radio.GetFrequencyHz() <= 0 {
		return nil, fmt.Errorf("%w: the %s band has no centre frequency",
			ErrStationNotConfigured, pass.Band)
	}
	if radio.GetSampleRateHz() <= 0 {
		return nil, fmt.Errorf("%w: the %s band has no sample rate",
			ErrStationNotConfigured, pass.Band)
	}
	return radio, nil
}

// applyRadioOverrides layers per-pass values over the station defaults.
func applyRadioOverrides(radio *workerv1.RadioSettings, overrides map[string]any) {
	if value, ok := numeric(overrides["frequency_hz"]); ok {
		radio.FrequencyHz = int64(value)
	}
	if value, ok := numeric(overrides["sample_rate_hz"]); ok {
		radio.SampleRateHz = int64(value)
	}
	if value, ok := numeric(overrides["gain_db"]); ok {
		radio.GainDb = value
	}
	if value, ok := numeric(overrides["ppm_correction"]); ok {
		radio.PpmCorrection = int32(value)
	}
	if value, ok := overrides["bias_tee_enabled"].(bool); ok {
		radio.BiasTeeEnabled = value
	}
	if value, ok := overrides["source"].(string); ok && value != "" {
		radio.Source = value
	}
}

func numeric(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}

// pipelineFor resolves the SatDump pipeline, inlining a custom definition.
func (g *Generator) pipelineFor(ctx context.Context, pass domain.Pass) (*workerv1.SatDumpPipeline, error) {
	if pass.PipelineID == nil {
		// No pipeline is valid for a raw baseband recording.
		return &workerv1.SatDumpPipeline{}, nil
	}

	pipeline, err := g.repo.GetPipelineByID(ctx, *pass.PipelineID)
	if err != nil {
		return nil, fmt.Errorf("load pipeline: %w", err)
	}

	out := &workerv1.SatDumpPipeline{
		Identifier:     pipeline.Name,
		IsCustom:       pipeline.IsCustom,
		ChecksumSha256: pipeline.ChecksumSHA256,
	}
	// A custom definition travels with the plan; the Worker cannot fetch it
	// mid-pass if the Server is unreachable.
	if pipeline.IsCustom {
		out.DefinitionJson = pipeline.Definition
	}
	return out, nil
}

// Generation is the content hash of a plan, excluding generated_at.
//
// Deterministic protobuf marshalling is not a canonical form across versions,
// so the hash is taken over a copy with the timestamp and any existing
// generation cleared.
func Generation(plan *workerv1.PassPlan) (string, error) {
	copied, ok := proto.Clone(plan).(*workerv1.PassPlan)
	if !ok {
		return "", errors.New("clone pass plan")
	}
	copied.GeneratedAt = nil
	copied.Generation = ""

	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(copied)
	if err != nil {
		return "", fmt.Errorf("hash pass plan: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// Marshal serializes a plan for storage or transmission.
func Marshal(plan *workerv1.PassPlan) ([]byte, error) {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(plan)
	if err != nil {
		return nil, fmt.Errorf("encode pass plan: %w", err)
	}
	return encoded, nil
}

// Unmarshal decodes a stored plan.
func Unmarshal(encoded []byte) (*workerv1.PassPlan, error) {
	var plan workerv1.PassPlan
	if err := proto.Unmarshal(encoded, &plan); err != nil {
		return nil, fmt.Errorf("decode pass plan: %w", err)
	}
	return &plan, nil
}

// SetGeneration hashes the member plans into one value describing a whole
// desired state (spec.md section 6.2).
func SetGeneration(plans []*workerv1.PassPlan) string {
	hash := sha256.New()
	for _, plan := range plans {
		hash.Write([]byte(plan.GetGeneration()))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func bandToProto(band domain.RFBand) workerv1.RFBand {
	switch band {
	case domain.BandVHF:
		return workerv1.RFBand_RF_BAND_VHF
	case domain.BandUHF:
		return workerv1.RFBand_RF_BAND_UHF
	default:
		return workerv1.RFBand_RF_BAND_UNSPECIFIED
	}
}

func recordingModeToProto(mode domain.RecordingMode) workerv1.RecordingMode {
	switch mode {
	case domain.RecordRaw:
		return workerv1.RecordingMode_RECORDING_MODE_RAW
	case domain.RecordProcess:
		return workerv1.RecordingMode_RECORDING_MODE_PROCESS
	case domain.RecordRawAndProcess:
		return workerv1.RecordingMode_RECORDING_MODE_RAW_AND_PROCESS
	default:
		return workerv1.RecordingMode_RECORDING_MODE_UNSPECIFIED
	}
}
