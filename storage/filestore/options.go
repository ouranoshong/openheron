package filestore

import (
	"context"
	"log/slog"
)

// Teardown stops a session's runtime state before its files are removed.
type Teardown interface {
	Cleanup(ctx context.Context, sessionID string) error
}

// Options carries the runtime dependencies injected after construction.
// Teardown may be nil at construction time: the Runtime that Teardown refers to
// is itself built with a reference to this manager.
type Options struct {
	Teardown             Teardown
	CheckpointLegacyRoot string
	// Logger is injected by the assembly layer. Nil drops the lines, which is
	// what a test that does not care about them gets.
	Logger *slog.Logger
}
