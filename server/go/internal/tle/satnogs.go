package tle

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const DefaultSatNOGSURL = "https://db.satnogs.org/api"

type SatNOGS struct {
	baseURL string
	client  *http.Client
}

func NewSatNOGS(baseURL string, timeout time.Duration) *SatNOGS {
	if baseURL == "" {
		baseURL = DefaultSatNOGSURL
	}
	return &SatNOGS{baseURL: baseURL, client: newHTTPClient(timeout)}
}

func (s *SatNOGS) Name() Source { return SourceSatNOGS }

type satnogsTLE struct {
	Title      string `json:"tle0"`
	Line1      string `json:"tle1"`
	Line2      string `json:"tle2"`
	NoradCatID int    `json:"norad_cat_id"`
	Source     string `json:"tle_source"`
	Updated    string `json:"updated"`
}

func (s *SatNOGS) Fetch(ctx context.Context, noradID int) (Set, error) {
	endpoint, err := url.Parse(s.baseURL + "/tle/")
	if err != nil {
		return Set{}, fmt.Errorf("satnogs url: %w", err)
	}
	query := endpoint.Query()
	query.Set("norad_cat_id", strconv.Itoa(noradID))
	query.Set("format", "json")
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Set{}, fmt.Errorf("satnogs request: %w", err)
	}
	request.Header.Set("User-Agent", "aagasa-ground-station/0.1")
	request.Header.Set("Accept", "application/json")

	response, err := s.client.Do(request)
	if err != nil {
		return Set{}, fmt.Errorf("satnogs fetch: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return Set{}, fmt.Errorf("satnogs returned status %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return Set{}, fmt.Errorf("satnogs read: %w", err)
	}

	var entries []satnogsTLE
	if err := json.Unmarshal(body, &entries); err != nil {
		return Set{}, fmt.Errorf("satnogs response: %w", err)
	}
	if len(entries) == 0 {
		return Set{}, fmt.Errorf("%w: satnogs has no entry for %d", ErrNotFound, noradID)
	}

	entry := entries[0]
	set, err := Parse(entry.Title + "\n" + entry.Line1 + "\n" + entry.Line2)
	if err != nil {
		return Set{}, fmt.Errorf("satnogs response: %w", err)
	}
	if set.NoradID != noradID {
		return Set{}, fmt.Errorf("satnogs returned catalog number %d, expected %d", set.NoradID, noradID)
	}
	return set, nil
}

type Metadata struct {
	Name       string `json:"name"`
	AltNames   string `json:"names"`
	Status     string `json:"status"`
	Website    string `json:"website"`
	Operator   string `json:"operator"`
	Countries  string `json:"countries"`
	LaunchedAt string `json:"launched"`
	ImagePath  string `json:"image"`
	SatNOGSID  string `json:"sat_id"`
	NoradCatID int    `json:"norad_cat_id"`
}

func (s *SatNOGS) FetchMetadata(ctx context.Context, noradID int) (Metadata, error) {
	endpoint, err := url.Parse(s.baseURL + "/satellites/")
	if err != nil {
		return Metadata{}, fmt.Errorf("satnogs url: %w", err)
	}
	query := endpoint.Query()
	query.Set("norad_cat_id", strconv.Itoa(noradID))
	query.Set("format", "json")
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Metadata{}, fmt.Errorf("satnogs request: %w", err)
	}
	request.Header.Set("User-Agent", "aagasa-ground-station/0.1")
	request.Header.Set("Accept", "application/json")

	response, err := s.client.Do(request)
	if err != nil {
		return Metadata{}, fmt.Errorf("satnogs metadata fetch: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return Metadata{}, fmt.Errorf("satnogs metadata returned status %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return Metadata{}, fmt.Errorf("satnogs metadata read: %w", err)
	}

	var entries []Metadata
	if err := json.Unmarshal(body, &entries); err != nil {
		return Metadata{}, fmt.Errorf("satnogs metadata response: %w", err)
	}
	if len(entries) == 0 {
		return Metadata{}, fmt.Errorf("%w: satnogs has no metadata for %d", ErrNotFound, noradID)
	}
	return entries[0], nil
}

func (m Metadata) AsMap() map[string]any {
	fields := map[string]string{
		"name": m.Name, "alt_names": m.AltNames, "status": m.Status,
		"website": m.Website, "operator": m.Operator, "countries": m.Countries,
		"launched": m.LaunchedAt, "image": m.ImagePath, "satnogs_id": m.SatNOGSID,
	}
	out := map[string]any{"source": "satnogs"}
	for key, value := range fields {
		if value != "" {
			out[key] = value
		}
	}
	return out
}
