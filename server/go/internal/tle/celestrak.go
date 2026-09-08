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

// DefaultCelestrakURL is the GP endpoint, verified live: it answers
// ?CATNR=<id>&FORMAT=TLE with a three-line CRLF response, and returns HTTP 404
// with the body "No GP data found" for an unknown catalog number.
const DefaultCelestrakURL = "https://celestrak.org/NORAD/elements/gp.php"

// maxResponseBytes bounds a provider response; a TLE is a few hundred bytes.
const maxResponseBytes = 1 << 20

// Celestrak is the primary orbital data provider.
type Celestrak struct {
	baseURL string
	client  *http.Client
}

// NewCelestrak builds the CelesTrak provider. An empty baseURL uses the
// default endpoint.
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
	// CelesTrak asks clients to identify themselves.
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
	// CelesTrak has also been observed answering 200 with this body.
	if strings.Contains(text, "No GP data found") {
		return Set{}, fmt.Errorf("%w: celestrak has no entry for %d", ErrNotFound, noradID)
	}

	set, err := Parse(text)
	if err != nil {
		return Set{}, fmt.Errorf("celestrak response: %w", err)
	}
	// Guard against a query returning a different object than requested.
	if set.NoradID != noradID {
		return Set{}, fmt.Errorf("celestrak returned catalog number %d, expected %d", set.NoradID, noradID)
	}
	return set, nil
}
