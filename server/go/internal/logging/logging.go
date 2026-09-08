// Package logging builds the Server's structured logger.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// New returns a JSON structured logger at the given level.
// Output is ASCII-only; callers must never log credentials (RULES.md section 2.3).
func New(level string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(level)}))
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
