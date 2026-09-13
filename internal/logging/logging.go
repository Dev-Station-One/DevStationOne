// Package logging provides a shared structured logger for both binaries.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// New builds a JSON slog.Logger at the given level ("debug", "info", "warn", "error").
// Terminal I/O, file contents, and credentials must never be passed to it.
func New(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler)
}
