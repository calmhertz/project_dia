package tle

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultCelestrakURL = "https://celestrak.org/NORAD/elements/gp.php"

const maxResponseBytes = 1 << 20

type Celestrak struct {
	baseURL string
	client  *http.Client
}

func NewCelestrak(baseURL string, timeout time.Duration) *Celestrak {
	if baseURL == "" {
		baseURL = DefaultCelestrakURL
	}
	return &Celestrak{baseURL: baseURL, client: newHTTPClient(timeout)}
}

func (c *Celestrak) Name() Source { return SourceCelestrak }

func (c *Celestrak) Fetch(ctx context.Context, noradID int) (Set, error) {
	endpoint, err := url.Parse(c.baseURL)
	if err != nil {
		return Set{}, fmt.Errorf("celestrak url: %w", err)
	}
	query := endpoint.Query()
	query.Set("CATNR", strconv.Itoa(noradID))
	query.Set("FORMAT", "TLE")
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Set{}, fmt.Errorf("celestrak request: %w", err)
	}
	request.Header.Set("User-Agent", "aagasa-ground-station/0.1")

	response, err := c.client.Do(request)
	if err != nil {
		return Set{}, fmt.Errorf("celestrak fetch: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return Set{}, fmt.Errorf("celestrak read: %w", err)
	}
	text := string(body)

	if response.StatusCode == http.StatusNotFound {
		return Set{}, fmt.Errorf("%w: celestrak has no entry for %d", ErrNotFound, noradID)
	}
	if response.StatusCode != http.StatusOK {
		return Set{}, fmt.Errorf("celestrak returned status %d", response.StatusCode)
	}
	if strings.Contains(text, "No GP data found") {
		return Set{}, fmt.Errorf("%w: celestrak has no entry for %d", ErrNotFound, noradID)
	}

	set, err := Parse(text)
	if err != nil {
		return Set{}, fmt.Errorf("celestrak response: %w", err)
	}
	if set.NoradID != noradID {
		return Set{}, fmt.Errorf("celestrak returned catalog number %d, expected %d", set.NoradID, noradID)
	}
	return set, nil
}
