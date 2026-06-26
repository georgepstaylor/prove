// Package logging builds the application's *slog.Logger. slog stays the logging
// interface throughout the codebase; this just picks the handler — Charm's
// human-friendly console output for a terminal, JSON for production.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"

	charmlog "github.com/charmbracelet/log"
	"github.com/mattn/go-isatty"
)

// New returns the configured logger. Format is chosen by PROVE_LOG_FORMAT
// (pretty | json), defaulting to pretty on a TTY and json otherwise. Level is
// PROVE_LOG_LEVEL (debug | info | warn | error), default info.
func New() *slog.Logger {
	return build(os.Stdout, resolvePretty(os.Getenv("PROVE_LOG_FORMAT"), os.Stdout), parseLevel(os.Getenv("PROVE_LOG_LEVEL")))
}

// build constructs a logger over w (pretty or JSON). Separated from New so it can
// be tested against a buffer.
func build(w io.Writer, pretty bool, level slog.Level) *slog.Logger {
	if pretty {
		return slog.New(charmlog.NewWithOptions(w, charmlog.Options{
			ReportTimestamp: true,
			TimeFormat:      "15:04:05",
			Level:           charmlog.Level(level), // Charm levels match slog's numerics
		}))
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// resolvePretty decides the handler format. An explicit PROVE_LOG_FORMAT wins;
// otherwise pretty when the output is an interactive terminal.
func resolvePretty(format string, w io.Writer) bool {
	switch strings.ToLower(format) {
	case "pretty", "text", "console":
		return true
	case "json":
		return false
	default:
		f, ok := w.(*os.File)
		return ok && isatty.IsTerminal(f.Fd())
	}
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
