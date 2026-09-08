package tle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// CelesTrak, replayed from a captured live response.
func TestCelestrakFetch(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, celestrakISS)
	}))
	defer server.Close()

	set, err := NewCelestrak(server.URL, time.Second).Fetch(context.Background(), 25544)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if set.NoradID != 25544 {
		t.Errorf("NoradID = %d, want 25544", set.NoradID)
	}
	// The verified query shape.
	if gotQuery != "CATNR=25544&FORMAT=TLE" {
		t.Errorf("query = %q, want CATNR=25544&FORMAT=TLE", gotQuery)
	}
}

// The live endpoint answers 404 with "No GP data found" for an unknown object.
func TestCelestrakUnknownSatelliteIsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, "No GP data found")
	}))
	defer server.Close()

	_, err := NewCelestrak(server.URL, time.Second).Fetch(context.Background(), 99999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// It has also been seen answering 200 with the same body.
func TestCelestrakNoDataBodyWithStatus200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "No GP data found")
	}))
	defer server.Close()

	_, err := NewCelestrak(server.URL, time.Second).Fetch(context.Background(), 99999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestCelestrakServerErrorIsNotMistakenForMissingData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := NewCelestrak(server.URL, time.Second).Fetch(context.Background(), 25544)
	if err == nil {
		t.Fatal("expected an error")
	}
	// An outage must stay distinct from "this satellite does not exist".
	if errors.Is(err, ErrNotFound) {
		t.Error("a 500 was reported as ErrNotFound")
	}
}

// A provider returning the wrong object must never be stored under our id.
func TestCelestrakRejectsMismatchedCatalogNumber(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, celestrakNOAA19)
	}))
	defer server.Close()

	_, err := NewCelestrak(server.URL, time.Second).Fetch(context.Background(), 25544)
	if err == nil {
		t.Fatal("expected a mismatch error")
	}
}

// SatNOGS, replayed from a captured live response.
func TestSatNOGSFetch(t *testing.T) {
	const body = `[{"tle0":"0 ISS (ZARYA)",
	  "tle1":"1 25544U 98067A   26236.17729445  .00008773  00000-0  16369-3 0  9991",
	  "tle2":"2 25544  51.6333 323.5788 0007697  77.8713 282.3138 15.49600847582298",
	  "tle_source":"Space-Track.org","norad_cat_id":25544}]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()

	set, err := NewSatNOGS(server.URL, time.Second).Fetch(context.Background(), 25544)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if set.NoradID != 25544 || set.Name != "ISS (ZARYA)" {
		t.Errorf("set = %+v", set)
	}
}

// The live endpoint returns an empty array with HTTP 200 for an unknown object.
func TestSatNOGSEmptyArrayIsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "[]")
	}))
	defer server.Close()

	_, err := NewSatNOGS(server.URL, time.Second).Fetch(context.Background(), 99999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestSatNOGSMetadata(t *testing.T) {
	const body = `[{"sat_id":"XSKZ-5603","norad_cat_id":25544,"name":"ISS",
	  "names":"ZARYA, RS0ISS","status":"alive","operator":"None","countries":"RU,US",
	  "website":"https://www.nasa.gov/","launched":"1998-11-20T00:00:00Z"}]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()

	metadata, err := NewSatNOGS(server.URL, time.Second).FetchMetadata(context.Background(), 25544)
	if err != nil {
		t.Fatalf("FetchMetadata: %v", err)
	}
	if metadata.Name != "ISS" || metadata.Status != "alive" {
		t.Errorf("metadata = %+v", metadata)
	}

	asMap := metadata.AsMap()
	if asMap["source"] != "satnogs" || asMap["countries"] != "RU,US" {
		t.Errorf("AsMap = %v", asMap)
	}
	// Empty fields are omitted so absence stays visible.
	if _, present := asMap["image"]; present {
		t.Error("an empty field was stored")
	}
}

// Fallback chain -----------------------------------------------------------

type stubProvider struct {
	name  Source
	set   Set
	err   error
	calls *int
}

func (s stubProvider) Name() Source { return s.name }

func (s stubProvider) Fetch(ctx context.Context, noradID int) (Set, error) {
	if s.calls != nil {
		*s.calls++
	}
	return s.set, s.err
}

func mustParse(t *testing.T, text string) Set {
	t.Helper()
	set, err := Parse(text)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return set
}

func TestChainUsesThePreferredProviderFirst(t *testing.T) {
	primaryCalls, fallbackCalls := 0, 0
	chain := NewChain(discardLogger(),
		stubProvider{name: SourceCelestrak, set: mustParse(t, celestrakISS), calls: &primaryCalls},
		stubProvider{name: SourceSatNOGS, set: mustParse(t, satnogsISS), calls: &fallbackCalls},
	)

	_, source, err := chain.Fetch(context.Background(), 25544)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if source != SourceCelestrak {
		t.Errorf("source = %s, want celestrak", source)
	}
	if fallbackCalls != 0 {
		t.Error("the fallback was called even though the primary succeeded")
	}
}

// spec.md section 11.3: primary failure falls through to the fallback.
func TestChainFallsBackWhenThePrimaryFails(t *testing.T) {
	chain := NewChain(discardLogger(),
		stubProvider{name: SourceCelestrak, err: errors.New("connection refused")},
		stubProvider{name: SourceSatNOGS, set: mustParse(t, satnogsISS)},
	)

	set, source, err := chain.Fetch(context.Background(), 25544)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if source != SourceSatNOGS {
		t.Errorf("source = %s, want satnogs", source)
	}
	if set.NoradID != 25544 {
		t.Errorf("NoradID = %d", set.NoradID)
	}
}

func TestChainReportsFailureWhenEveryProviderFails(t *testing.T) {
	chain := NewChain(discardLogger(),
		stubProvider{name: SourceCelestrak, err: errors.New("down")},
		stubProvider{name: SourceSatNOGS, err: errors.New("also down")},
	)

	if _, _, err := chain.Fetch(context.Background(), 25544); err == nil {
		t.Error("expected an error when the whole chain fails")
	}
}

// A cancelled request must stop, not retry the same failure per provider.
func TestChainStopsOnCancellation(t *testing.T) {
	calls := 0
	chain := NewChain(discardLogger(),
		stubProvider{name: SourceCelestrak, err: errors.New("down"), calls: &calls},
		stubProvider{name: SourceSatNOGS, err: errors.New("down"), calls: &calls},
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := chain.Fetch(ctx, 25544); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Errorf("providers called %d times after cancellation", calls)
	}
}

func TestEmptyChainIsAnError(t *testing.T) {
	if _, _, err := NewChain(discardLogger()).Fetch(context.Background(), 25544); err == nil {
		t.Error("expected an error from an empty chain")
	}
}
