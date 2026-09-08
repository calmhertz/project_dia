package tle

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// ErrNotFound means the provider has no element set for that catalog number.
// It is distinct from a transport failure: an unknown satellite is an answer,
// not an outage.
var ErrNotFound = errors.New("no TLE available from provider")

// Source names a provider, matching the tle_source database enum.
type Source string

const (
	SourceCelestrak Source = "celestrak"
	SourceSatNOGS   Source = "satnogs"
)

// Provider fetches orbital data for one satellite.
//
// The interface exists so sources can be replaced or extended
// (spec.md section 11.3), not as speculative generality.
type Provider interface {
	// Name identifies the provider for storage and diagnostics.
	Name() Source
	// Fetch returns the current element set, or ErrNotFound.
	Fetch(ctx context.Context, noradID int) (Set, error)
}

const defaultTimeout = 20 * time.Second

// newHTTPClient builds a client with a bounded timeout so a hung provider
// cannot stall a refresh.
func newHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &http.Client{Timeout: timeout}
}

// Chain tries providers in order and returns the first success.
//
// spec.md section 11.3: preferred provider, then fallback, and only then the
// caller's last known-good record. The chain never invents data; exhausting
// it is an error the caller resolves from storage.
type Chain struct {
	providers []Provider
	logger    *slog.Logger
}

// NewChain builds a fallback chain in preference order.
func NewChain(logger *slog.Logger, providers ...Provider) *Chain {
	return &Chain{providers: providers, logger: logger}
}

// Providers returns the chain in preference order.
func (c *Chain) Providers() []Provider { return c.providers }

// Fetch tries each provider until one answers.
func (c *Chain) Fetch(ctx context.Context, noradID int) (Set, Source, error) {
	if len(c.providers) == 0 {
		return Set{}, "", errors.New("no TLE providers configured")
	}

	var lastErr error
	for _, provider := range c.providers {
		// A cancelled context means stop, not fall through to the next
		// provider and repeat the same failure.
		if ctx.Err() != nil {
			return Set{}, "", ctx.Err()
		}

		set, err := provider.Fetch(ctx, noradID)
		if err == nil {
			return set, provider.Name(), nil
		}
		lastErr = err
		c.logger.Warn("tle provider failed",
			slog.String("provider", string(provider.Name())),
			slog.Int("norad_id", noradID),
			slog.String("error", err.Error()),
		)
	}

	return Set{}, "", lastErr
}
