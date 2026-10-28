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

func DefaultLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: levelFromEnv()}))
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
