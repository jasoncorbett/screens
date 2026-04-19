package logging

import (
	"log/slog"
	"os"
	"strings"
)

// Setup creates and sets the default slog logger. In dev mode, output is
// colorized and human-readable. Otherwise, output is JSON.
func Setup(level string, devMode bool) *slog.Logger {
	lvl := parseLevel(level)

	var handler slog.Handler
	if devMode {
		handler = newDevHandler(os.Stderr, lvl)
	} else {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
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
