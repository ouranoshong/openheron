package obs

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

const LevelEnv = "OPENHERON_LOG_LEVEL"

// New builds a logger that writes to w at the level level carries.
//
// ★ The LevelVar is shared on purpose: the handler reads it for every record,
// so moving it moves the whole output, including loggers derived with With
// and WithGroup. That is what lets the composition root install the logger
// before the configuration is read and then apply the configured level.
func New(w io.Writer, level *slog.LevelVar) *slog.Logger {
	// ★ nil is checked here, on the concrete type: a nil *LevelVar stored in
	// a slog.Leveler is not a nil interface, and Level() would panic.
	if level == nil {
		level = new(slog.LevelVar)
	}
	return slog.New(NewHandlerWithLevel(w, level))
}

// Discard returns a logger that writes nothing. Constructors default to it,
// so a caller without a logger never has to pass nil around.
func Discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ParseLevel reads a configured level. Empty means info; the aliases and the
// case-insensitivity match what LevelEnv accepted before.
//
// ★ A malformed value returns info together with the error: the caller
// reports the fallback before it changes the threshold.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level %q (debug, info, warn, error)", s)
	}
}

// LevelName is the canonical spelling ParseLevel accepts, for the places that
// have to show the level in effect (the introspection endpoint, for one).
func LevelName(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	default:
		return "error"
	}
}

// LevelFromEnv reads LevelEnv. The error says the value was malformed while
// the level is already the safe fallback.
func LevelFromEnv() (slog.Level, error) {
	return ParseLevel(os.Getenv(LevelEnv))
}
