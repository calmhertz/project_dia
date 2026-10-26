package tle

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

var ErrNotFound = errors.New("no TLE available from provider")

type Source string

const (
	SourceCelestrak Source = "celestrak"
	SourceSatNOGS   Source = "satnogs"
)

type Provider interface {
	Name() Source
	Fetch(ctx context.Context, noradID int) (Set, error)
}

const defaultTimeout = 20 * time.Second

func newHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &http.Client{Timeout: timeout}
}

type Chain struct {
	providers []Provider
	logger    *slog.Logger
}

func NewChain(logger *slog.Logger, providers ...Provider) *Chain {
	return &Chain{providers: providers, logger: logger}
}

func (c *Chain) Providers() []Provider { return c.providers }

func (c *Chain) Fetch(ctx context.Context, noradID int) (Set, Source, error) {
	if len(c.providers) == 0 {
		return Set{}, "", errors.New("no TLE providers configured")
	}

	var lastErr error
	for _, provider := range c.providers {
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
