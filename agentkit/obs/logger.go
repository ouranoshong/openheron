package obs

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

const LevelEnv = "OPENHERON_LOG_LEVEL"

var (
	loggerMu sync.RWMutex
	logger   *slog.Logger
)

func Logger() *slog.Logger {
	loggerMu.RLock()
	l := logger
	loggerMu.RUnlock()
	if l != nil {
		return l
	}
	l = DefaultLogger(os.Stderr)
	loggerMu.Lock()
	logger = l
	loggerMu.Unlock()
	return l
}

func SetLogger(l *slog.Logger) {
	if l == nil {
		return
	}
	loggerMu.Lock()
	logger = l
	loggerMu.Unlock()
}

// DefaultLogger builds the logger openheron writes through.
//
// The format is hertz's: the date, the time with microseconds, the caller's
// file and line, the level, then the message. See Handler.
func DefaultLogger(w io.Writer) *slog.Logger {
	return slog.New(NewHandler(w, levelFromEnv()))
}

func levelFromEnv() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(LevelEnv))) {
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
