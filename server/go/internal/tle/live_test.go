package tle_test

import (
	"context"
	"os"
	"testing"
	"time"

	"aagasa/internal/tle"
)

// Live provider checks. Skipped unless explicitly enabled, so the normal test
// run stays offline and deterministic:
//
//	AAGASA_TEST_LIVE_PROVIDERS=1 go test ./internal/tle/...
func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("AAGASA_TEST_LIVE_PROVIDERS") == "" {
		t.Skip("AAGASA_TEST_LIVE_PROVIDERS not set")
	}
}

func TestLiveCelestrak(t *testing.T) {
	requireLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	set, err := tle.NewCelestrak("", 0).Fetch(ctx, 25544)
	if err != nil {
		t.Fatalf("live celestrak fetch: %v", err)
	}
	if set.NoradID != 25544 {
		t.Errorf("NoradID = %d, want 25544", set.NoradID)
	}
	// Orbital data for an active satellite should be days old at most.
	if age := set.Age(time.Now().UTC()); age > 14*24*time.Hour {
		t.Errorf("epoch is %v old; expected recent data", age)
	}
}

func TestLiveCelestrakUnknownSatellite(t *testing.T) {
	requireLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := tle.NewCelestrak("", 0).Fetch(ctx, 99999); err == nil {
		t.Error("expected an error for an unknown catalog number")
	}
}

func TestLiveSatNOGS(t *testing.T) {
	requireLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	set, err := tle.NewSatNOGS("", 0).Fetch(ctx, 25544)
	if err != nil {
		t.Fatalf("live satnogs fetch: %v", err)
	}
	if set.NoradID != 25544 {
		t.Errorf("NoradID = %d, want 25544", set.NoradID)
	}
}

func TestLiveSatNOGSMetadata(t *testing.T) {
	requireLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	metadata, err := tle.NewSatNOGS("", 0).FetchMetadata(ctx, 25544)
	if err != nil {
		t.Fatalf("live satnogs metadata: %v", err)
	}
	if metadata.Name == "" {
		t.Error("metadata has no name")
	}
}

// Both live providers must describe the same orbit for the same object.
func TestLiveProvidersAgree(t *testing.T) {
	requireLive(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fromCelestrak, err := tle.NewCelestrak("", 0).Fetch(ctx, 25544)
	if err != nil {
		t.Fatalf("celestrak: %v", err)
	}
	fromSatNOGS, err := tle.NewSatNOGS("", 0).Fetch(ctx, 25544)
	if err != nil {
		t.Fatalf("satnogs: %v", err)
	}

	// Feeds update at different times, so allow a couple of days of drift.
	difference := fromCelestrak.Epoch.Sub(fromSatNOGS.Epoch)
	if difference < 0 {
		difference = -difference
	}
	if difference > 48*time.Hour {
		t.Errorf("provider epochs differ by %v", difference)
	}
}
