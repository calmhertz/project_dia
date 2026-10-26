package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment string
	LogLevel    string

	HTTPAddress string
	GRPCAddress string

	PostgresURL string
	RedisURL    string
	MongoURL    string
	MongoDB     string

	PredictionAddress string

	RecordingsDir string
	PipelinesDir  string

	WebDir string

	CelestrakURL string
	SatNOGSURL   string
	TLERefreshEnabled bool

	WorkerSharedSecret string

	AllowedOrigins []string

	TLSCertFile string
	TLSKeyFile  string

	StartupTimeout time.Duration
	SessionTTL     time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Environment:        env("AAGASA_ENV", "development"),
		LogLevel:           env("AAGASA_LOG_LEVEL", "info"),
		HTTPAddress:        env("AAGASA_HTTP_ADDRESS", ":8080"),
		GRPCAddress:        env("AAGASA_GRPC_ADDRESS", ":9090"),
		PostgresURL:        os.Getenv("AAGASA_POSTGRES_URL"),
		RedisURL:           os.Getenv("AAGASA_REDIS_URL"),
		MongoURL:           os.Getenv("AAGASA_MONGO_URL"),
		MongoDB:            env("AAGASA_MONGO_DATABASE", "aagasa"),
		PredictionAddress:  env("AAGASA_PREDICTION_ADDRESS", "127.0.0.1:9091"),
		CelestrakURL:       os.Getenv("AAGASA_CELESTRAK_URL"),
		SatNOGSURL:         os.Getenv("AAGASA_SATNOGS_URL"),
		TLERefreshEnabled:  envBool("AAGASA_TLE_REFRESH_ENABLED", true),
		RecordingsDir:      env("AAGASA_RECORDINGS_DIR", "/var/lib/aagasa/recordings"),
		PipelinesDir:       env("AAGASA_PIPELINES_DIR", "/var/lib/aagasa/pipelines"),
		WebDir:             env("AAGASA_WEB_DIR", "/app/web"),
		WorkerSharedSecret: os.Getenv("AAGASA_WORKER_SHARED_SECRET"),
		AllowedOrigins:     envList("AAGASA_ALLOWED_ORIGINS"),
		TLSCertFile:        os.Getenv("AAGASA_TLS_CERT_FILE"),
		TLSKeyFile:         os.Getenv("AAGASA_TLS_KEY_FILE"),
	}

	timeout, err := envDuration("AAGASA_STARTUP_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	cfg.StartupTimeout = timeout

	sessionTTL, err := envDuration("AAGASA_SESSION_TTL", 12*time.Hour)
	if err != nil {
		return Config{}, err
	}
	cfg.SessionTTL = sessionTTL

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	var missing []string
	for name, value := range map[string]string{
		"AAGASA_POSTGRES_URL":         c.PostgresURL,
		"AAGASA_REDIS_URL":            c.RedisURL,
		"AAGASA_MONGO_URL":            c.MongoURL,
		"AAGASA_WORKER_SHARED_SECRET": c.WorkerSharedSecret,
	} {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(sorted(missing), ", "))
	}
	if len(c.WorkerSharedSecret) < 32 {
		return fmt.Errorf("AAGASA_WORKER_SHARED_SECRET must be at least 32 characters")
	}

	certGiven := strings.TrimSpace(c.TLSCertFile) != ""
	keyGiven := strings.TrimSpace(c.TLSKeyFile) != ""
	if certGiven != keyGiven {
		return fmt.Errorf("AAGASA_TLS_CERT_FILE and AAGASA_TLS_KEY_FILE must be set together")
	}
	if certGiven {
		for name, path := range map[string]string{
			"AAGASA_TLS_CERT_FILE": c.TLSCertFile,
			"AAGASA_TLS_KEY_FILE":  c.TLSKeyFile,
		} {
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("%s is not readable: %w", name, err)
			}
		}
	}
	return nil
}

func (c Config) TLSEnabled() bool {
	return strings.TrimSpace(c.TLSCertFile) != "" && strings.TrimSpace(c.TLSKeyFile) != ""
}

func (c Config) AllowLocalhostOrigins() bool {
	return c.Environment == "development"
}

func envList(name string) []string {
	var values []string
	for _, part := range strings.Split(os.Getenv(name), ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "":
		return fallback
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number of seconds", name)
	}
	if seconds <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return time.Duration(seconds) * time.Second, nil
}

func sorted(values []string) []string {
	out := append([]string(nil), values...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
