package prediction

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	predictionv1 "aagasa/internal/gen/prediction/v1"
)

// ErrInvalidRequest means the prediction service rejected the request as
// unusable. It is distinct from an outage: the caller must fix the input.
var ErrInvalidRequest = errors.New("invalid prediction request")

// TrackPoint is one antenna pointing instruction.
type TrackPoint struct {
	At               time.Time
	AzimuthDegrees   float64
	ElevationDegrees float64
	RangeKm          float64
}

// Pass is one predicted satellite pass over the station.
type Pass struct {
	AOS time.Time
	TCA time.Time
	LOS time.Time

	AOSAzimuthDegrees float64
	TCAAzimuthDegrees float64
	LOSAzimuthDegrees float64

	MaxElevationDegrees float64
	Duration            time.Duration

	// Track is the pointing timeline, empty unless requested.
	Track []TrackPoint
}

// Request describes a prediction to compute.
type Request struct {
	Line1 string
	Line2 string

	LatitudeDegrees  float64
	LongitudeDegrees float64
	AltitudeM        float64

	MinimumElevationDegrees float64

	SearchStart time.Time
	SearchEnd   time.Time

	// TrackStep of zero omits the pointing timeline.
	TrackStep time.Duration
	MaxPasses int
}

// Result carries the predicted passes.
type Result struct {
	NoradID int
	Passes  []Pass
}

// PredictPasses asks the Python service to compute passes.
//
// The Server never computes passes itself: there is exactly one predictor in
// Aagasa (spec.md section 12).
func (c *Client) PredictPasses(ctx context.Context, request Request) (Result, error) {
	proto := &predictionv1.PredictPassesRequest{
		Tle: &predictionv1.TwoLineElements{Line1: request.Line1, Line2: request.Line2},
		Observer: &predictionv1.ObserverLocation{
			LatitudeDegrees:  request.LatitudeDegrees,
			LongitudeDegrees: request.LongitudeDegrees,
			AltitudeM:        request.AltitudeM,
		},
		MinimumElevationDegrees: request.MinimumElevationDegrees,
		SearchStart:             timestamppb.New(request.SearchStart.UTC()),
		SearchEnd:               timestamppb.New(request.SearchEnd.UTC()),
		TrackStepSeconds:        uint32(request.TrackStep.Seconds()),
		MaxPasses:               uint32(request.MaxPasses),
	}

	response, err := c.client.PredictPasses(ctx, proto)
	if err != nil {
		if status.Code(err) == codes.InvalidArgument {
			return Result{}, fmt.Errorf("%w: %s", ErrInvalidRequest, status.Convert(err).Message())
		}
		return Result{}, fmt.Errorf("predict passes: %w", err)
	}

	result := Result{
		NoradID: int(response.GetNoradId()),
		Passes:  make([]Pass, 0, len(response.GetPasses())),
	}
	for _, predicted := range response.GetPasses() {
		result.Passes = append(result.Passes, fromProto(predicted))
	}
	return result, nil
}

func fromProto(predicted *predictionv1.PredictedPass) Pass {
	pass := Pass{
		AOS:                 predicted.GetAos().AsTime(),
		TCA:                 predicted.GetTca().AsTime(),
		LOS:                 predicted.GetLos().AsTime(),
		AOSAzimuthDegrees:   predicted.GetAosAzimuthDegrees(),
		TCAAzimuthDegrees:   predicted.GetTcaAzimuthDegrees(),
		LOSAzimuthDegrees:   predicted.GetLosAzimuthDegrees(),
		MaxElevationDegrees: predicted.GetMaxElevationDegrees(),
		Duration:            time.Duration(predicted.GetDurationSeconds() * float64(time.Second)),
	}
	for _, point := range predicted.GetTrack() {
		pass.Track = append(pass.Track, TrackPoint{
			At:               point.GetAt().AsTime(),
			AzimuthDegrees:   point.GetAzimuthDegrees(),
			ElevationDegrees: point.GetElevationDegrees(),
			RangeKm:          point.GetRangeKm(),
		})
	}
	return pass
}
