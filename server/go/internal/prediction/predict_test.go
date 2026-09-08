package prediction_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"aagasa/internal/prediction"
)

// These tests drive the real Python prediction service over gRPC, which is
// the V6 exit criterion "Go can consume predictions". They skip unless the
// service address is configured:
//
//	AAGASA_TEST_PREDICTION_ADDRESS=127.0.0.1:9091 go test ./internal/prediction/...

const (
	issLine1 = "1 25544U 98067A   26236.17729445  .00008773  00000+0  16369-3 0  9990"
	issLine2 = "2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298"
)

func newClient(t *testing.T) *prediction.Client {
	t.Helper()
	address := os.Getenv("AAGASA_TEST_PREDICTION_ADDRESS")
	if address == "" {
		t.Skip("AAGASA_TEST_PREDICTION_ADDRESS not set")
	}
	client, err := prediction.Dial(address)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func baseRequest() prediction.Request {
	return prediction.Request{
		Line1:                   issLine1,
		Line2:                   issLine2,
		LatitudeDegrees:         13.394944,
		LongitudeDegrees:        77.729444,
		AltitudeM:               915,
		MinimumElevationDegrees: 10,
		SearchStart:             time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC),
		SearchEnd:               time.Date(2026, time.August, 26, 0, 0, 0, 0, time.UTC),
	}
}

func TestGoConsumesPredictions(t *testing.T) {
	client := newClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := client.PredictPasses(ctx, baseRequest())
	if err != nil {
		t.Fatalf("PredictPasses: %v", err)
	}

	if result.NoradID != 25544 {
		t.Errorf("NoradID = %d, want 25544", result.NoradID)
	}
	if len(result.Passes) < 2 {
		t.Fatalf("passes = %d, want at least 2", len(result.Passes))
	}

	for _, pass := range result.Passes {
		if !pass.AOS.Before(pass.TCA) || !pass.TCA.Before(pass.LOS) {
			t.Errorf("times out of order: %s %s %s", pass.AOS, pass.TCA, pass.LOS)
		}
		if pass.MaxElevationDegrees < 10 {
			t.Errorf("MaxElevationDegrees = %f, below the requested threshold", pass.MaxElevationDegrees)
		}
		if pass.Duration <= 0 || pass.Duration > 30*time.Minute {
			t.Errorf("Duration = %v, implausible for low Earth orbit", pass.Duration)
		}
		// Timestamps must survive the protobuf round trip as UTC.
		if pass.AOS.Location() != time.UTC {
			t.Errorf("AOS is not UTC: %s", pass.AOS.Location())
		}
	}
}

func TestTrackCrossesTheWireIntact(t *testing.T) {
	client := newClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	request := baseRequest()
	request.TrackStep = 30 * time.Second
	request.MaxPasses = 1

	result, err := client.PredictPasses(ctx, request)
	if err != nil {
		t.Fatalf("PredictPasses: %v", err)
	}
	if len(result.Passes) != 1 {
		t.Fatalf("passes = %d, want 1", len(result.Passes))
	}

	pass := result.Passes[0]
	if len(pass.Track) < 2 {
		t.Fatalf("track points = %d, want several", len(pass.Track))
	}

	// The track must start at AOS and finish at LOS so the Worker has an
	// explicit start and end to drive the rotator between.
	if !pass.Track[0].At.Equal(pass.AOS) {
		t.Errorf("track starts at %s, want AOS %s", pass.Track[0].At, pass.AOS)
	}
	if difference := pass.LOS.Sub(pass.Track[len(pass.Track)-1].At); difference > time.Second {
		t.Errorf("track ends %v before LOS", difference)
	}

	for _, point := range pass.Track {
		if point.AzimuthDegrees < 0 || point.AzimuthDegrees > 360 {
			t.Errorf("azimuth %f outside 0..360", point.AzimuthDegrees)
		}
		// The Worker clamps to the G-550 range, but a track point should not
		// be wildly outside it in the first place.
		if point.ElevationDegrees < -1 || point.ElevationDegrees > 90 {
			t.Errorf("elevation %f implausible", point.ElevationDegrees)
		}
		if point.RangeKm <= 0 {
			t.Errorf("range %f must be positive", point.RangeKm)
		}
	}
}

func TestNoTrackWhenNoneRequested(t *testing.T) {
	client := newClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := client.PredictPasses(ctx, baseRequest())
	if err != nil {
		t.Fatalf("PredictPasses: %v", err)
	}
	for _, pass := range result.Passes {
		if len(pass.Track) != 0 {
			t.Errorf("track returned unrequested: %d points", len(pass.Track))
		}
	}
}

// The same request must give the same answer: scheduling depends on it.
func TestPredictionsAreDeterministicAcrossCalls(t *testing.T) {
	client := newClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	first, err := client.PredictPasses(ctx, baseRequest())
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := client.PredictPasses(ctx, baseRequest())
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if len(first.Passes) != len(second.Passes) {
		t.Fatalf("pass counts differ: %d and %d", len(first.Passes), len(second.Passes))
	}
	for index := range first.Passes {
		if !first.Passes[index].AOS.Equal(second.Passes[index].AOS) {
			t.Errorf("pass %d AOS differs between calls", index)
		}
		if first.Passes[index].MaxElevationDegrees != second.Passes[index].MaxElevationDegrees {
			t.Errorf("pass %d max elevation differs between calls", index)
		}
	}
}

// A rejected request must be distinguishable from an outage, so the API layer
// can answer 400 rather than 502.
func TestInvalidRequestIsTyped(t *testing.T) {
	client := newClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cases := map[string]func(*prediction.Request){
		"latitude":     func(r *prediction.Request) { r.LatitudeDegrees = 120 },
		"longitude":    func(r *prediction.Request) { r.LongitudeDegrees = 999 },
		"elevation":    func(r *prediction.Request) { r.MinimumElevationDegrees = 95 },
		"time order":   func(r *prediction.Request) { r.SearchEnd = r.SearchStart.Add(-time.Hour) },
		"unusable tle": func(r *prediction.Request) { r.Line1, r.Line2 = "garbage", "garbage" },
	}

	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			request := baseRequest()
			corrupt(&request)

			_, err := client.PredictPasses(ctx, request)
			if !errors.Is(err, prediction.ErrInvalidRequest) {
				t.Errorf("error = %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestMaxPassesIsHonoured(t *testing.T) {
	client := newClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	request := baseRequest()
	request.MaxPasses = 1

	result, err := client.PredictPasses(ctx, request)
	if err != nil {
		t.Fatalf("PredictPasses: %v", err)
	}
	if len(result.Passes) != 1 {
		t.Errorf("passes = %d, want 1", len(result.Passes))
	}
}

// An unreachable service must surface as an error, not a silent empty result.
func TestUnreachableServiceIsAnError(t *testing.T) {
	client, err := prediction.Dial("127.0.0.1:9")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.PredictPasses(ctx, baseRequest())
	if err == nil {
		t.Fatal("expected an error from an unreachable service")
	}
	if errors.Is(err, prediction.ErrInvalidRequest) {
		t.Error("an outage was reported as an invalid request")
	}
}
