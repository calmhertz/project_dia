package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	seedUsers = []struct {
		Username string
		Password string
		Role     string
	}{
		{"operator", "operator-development-password", "user"},
		{"approver", "approver-development-password", "admin"},
	}

	seedSatellites = []int{25544, 33591, 40069}
)

const bootstrapPassword = "toor"

const exitAlreadyConfigured = 3

var errAlreadyConfigured = errors.New("already configured with its own credentials")

type client struct {
	base  string
	token string
	http  *http.Client
}

func main() {
	server := flag.String("server", "http://127.0.0.1:8080", "Server base URL")
	rootPassword := flag.String("root-password", "", "root password; the bootstrap one is changed to this on first run")
	stationName := flag.String("station", "Development Station", "station name for first-run setup")
	latitude := flag.Float64("latitude", 13.394944, "station latitude")
	longitude := flag.Float64("longitude", 77.729444, "station longitude")
	altitude := flag.Float64("altitude", 915, "station altitude in metres")
	workerName := flag.String("worker", "worker-1", "the name the Worker registers under")
	leadTime := flag.Int("lead-time-seconds", 60, "minimum scheduling lead time")
	flag.Parse()

	if strings.TrimSpace(*rootPassword) == "" {
		fail(errors.New("--root-password is required"))
	}
	if len(*rootPassword) < 12 {
		fail(errors.New("--root-password must be at least 12 characters, as the Server requires"))
	}

	c := &client{base: strings.TrimRight(*server, "/"), http: &http.Client{Timeout: 30 * time.Second}}

	if err := c.waitForServer(2 * time.Minute); err != nil {
		fail(err)
	}
	if err := c.signInAsRoot(*rootPassword); err != nil {
		if errors.Is(err, errAlreadyConfigured) {
			fmt.Fprintln(os.Stderr, "seed:", err)
			os.Exit(exitAlreadyConfigured)
		}
		fail(err)
	}
	if err := c.ensureSetup(setupRequest{
		StationName: *stationName, Latitude: *latitude, Longitude: *longitude,
		AltitudeM: *altitude, WorkerName: *workerName, LeadTimeSeconds: *leadTime,
	}); err != nil {
		fail(err)
	}
	if err := c.ensureUsers(); err != nil {
		fail(err)
	}
	if err := c.ensureSatellites(); err != nil {
		fail(err)
	}

	fmt.Println("seeded")
	for _, user := range seedUsers {
		fmt.Printf("  %-9s %-6s %s\n", user.Username, user.Role, user.Password)
	}
	fmt.Printf("  %-9s %-6s %s\n", "root", "root", *rootPassword)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "seed:", err)
	os.Exit(1)
}

func (c *client) waitForServer(limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		response, err := c.http.Get(c.base + "/readyz")
		if err == nil {
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
			if time.Now().Add(5 * time.Second).After(deadline) {
				return fmt.Errorf("server not ready: %s", strings.TrimSpace(string(body)))
			}
		}
		time.Sleep(2 * time.Second)
	}
	return errors.New("the server did not become ready")
}

func (c *client) signInAsRoot(password string) error {
	token, mustChange, err := c.login("root", password)
	if err == nil {
		c.token = token
		if mustChange {
			return c.changePassword(password, password)
		}
		return nil
	}

	token, mustChange, bootstrapErr := c.login("root", bootstrapPassword)
	if bootstrapErr != nil {
		return fmt.Errorf("%w: root's password is neither the bootstrap one nor the one "+
			"given, so pass the real one with --root-password "+
			"(AAGASA_ROOT_PASSWORD for dev-env.sh), or start a clean environment "+
			"with 'scripts/dev-env.sh reset'", errAlreadyConfigured)
	}
	c.token = token
	if !mustChange {
		return errors.New("the bootstrap password works but no change is demanded, which should be impossible")
	}
	if err := c.changePassword(bootstrapPassword, password); err != nil {
		return err
	}
	token, _, err = c.login("root", password)
	if err != nil {
		return fmt.Errorf("sign in after the password change: %w", err)
	}
	c.token = token
	return nil
}

func (c *client) login(username, password string) (string, bool, error) {
	var response struct {
		Token              string `json:"token"`
		MustChangePassword bool   `json:"must_change_password"`
	}
	err := c.do(http.MethodPost, "/api/auth/login",
		map[string]any{"username": username, "password": password}, &response)
	return response.Token, response.MustChangePassword, err
}

func (c *client) changePassword(current, next string) error {
	if err := c.do(http.MethodPost, "/api/auth/change-password",
		map[string]any{"current_password": current, "new_password": next}, nil); err != nil {
		return fmt.Errorf("change the root password: %w", err)
	}
	token, _, err := c.login("root", next)
	if err != nil {
		return err
	}
	c.token = token
	return nil
}

type setupRequest struct {
	StationName     string
	Latitude        float64
	Longitude       float64
	AltitudeM       float64
	WorkerName      string
	LeadTimeSeconds int
}

func (c *client) ensureSetup(request setupRequest) error {
	var status struct {
		Initialized bool `json:"initialized"`
	}
	if err := c.do(http.MethodGet, "/api/setup", nil, &status); err != nil {
		return fmt.Errorf("read setup status: %w", err)
	}
	if status.Initialized {
		fmt.Println("setup: already done")
		return nil
	}

	body := map[string]any{
		"station_name": request.StationName,
		"latitude":     request.Latitude, "longitude": request.Longitude,
		"altitude_m": request.AltitudeM, "timezone": "UTC",
		"active_rf_band": "vhf",
		"bands": []map[string]any{
			{"band": "vhf", "antenna_description": "Turnstile",
				"center_frequency_hz": 137100000, "sample_rate_hz": 2048000, "gain_db": 40.0},
			{"band": "uhf", "antenna_description": "Yagi",
				"center_frequency_hz": 435000000, "sample_rate_hz": 2048000, "gain_db": 40.0},
		},
		"rotator_serial_port":            os.Getenv("AAGASA_ROTATOR_SERIAL_PORT"),
		"rotator_baud_rate":              9600,
		"rotator_park_azimuth_degrees":   0,
		"rotator_park_elevation_degrees": 0,
		"sdr_device_identifier":          "rtlsdr",
		"minimum_lead_time_seconds":      request.LeadTimeSeconds,
		"pre_pass_buffer_seconds":        60,
		"post_pass_buffer_seconds":       60,
		"recording_pre_roll_seconds":     10,
		"recording_post_roll_seconds":    10,
		"minimum_elevation_degrees":      10.0,
		"worker_name":                    request.WorkerName,
	}
	if body["rotator_serial_port"] == "" {
		body["rotator_serial_port"] = "/tmp/aagasa-rotator"
	}

	if err := c.do(http.MethodPost, "/api/setup", body, nil); err != nil {
		return fmt.Errorf("first-run setup: %w", err)
	}
	fmt.Println("setup: done")
	return nil
}

func (c *client) ensureUsers() error {
	rootToken := c.token
	defer func() { c.token = rootToken }()

	for _, user := range seedUsers {
		initial := user.Password + "-initial"

		c.token = rootToken
		err := c.do(http.MethodPost, "/api/users", map[string]any{
			"username": user.Username, "password": initial, "role": user.Role,
		}, nil)
		switch {
		case err == nil:
			fmt.Printf("user: created %s\n", user.Username)
		case isConflict(err):
			fmt.Printf("user: %s already exists\n", user.Username)
			continue
		default:
			return fmt.Errorf("create %s: %w", user.Username, err)
		}

		token, mustChange, err := c.login(user.Username, initial)
		if err != nil {
			return fmt.Errorf("sign in as %s: %w", user.Username, err)
		}
		if !mustChange {
			return fmt.Errorf("%s was created without needing a password change", user.Username)
		}
		c.token = token
		if err := c.do(http.MethodPost, "/api/auth/change-password", map[string]any{
			"current_password": initial, "new_password": user.Password,
		}, nil); err != nil {
			return fmt.Errorf("first password change for %s: %w", user.Username, err)
		}
	}
	return nil
}

func (c *client) ensureSatellites() error {
	for _, noradID := range seedSatellites {
		err := c.do(http.MethodPost, "/api/satellites",
			map[string]any{"norad_id": noradID}, nil)
		switch {
		case err == nil:
			fmt.Printf("satellite: added %d\n", noradID)
		case isConflict(err):
			fmt.Printf("satellite: %d already in the catalogue\n", noradID)
		default:
			fmt.Printf("satellite: %d could not be added (%v)\n", noradID, err)
		}
	}
	return nil
}

type apiError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *apiError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s (%d %s)", e.Message, e.StatusCode, e.Code)
	}
	return fmt.Sprintf("%d %s", e.StatusCode, e.Code)
}

func isConflict(err error) bool {
	var api *apiError
	return errors.As(err, &api) && api.StatusCode == http.StatusConflict
}

func (c *client) do(method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode >= 400 {
		failure := &apiError{StatusCode: response.StatusCode}
		var decoded struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &decoded) == nil {
			failure.Code, failure.Message = decoded.Error, decoded.Message
		}
		return failure
	}
	if target != nil && len(raw) > 0 {
		return json.Unmarshal(raw, target)
	}
	return nil
}
