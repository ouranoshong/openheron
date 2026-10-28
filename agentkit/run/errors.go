package run

import "errors"

var (
	// ErrSuperseded is returned by Turn.Wait when a newer turn took over.
	ErrSuperseded = errors.New("turn superseded")
	// ErrStartTimeout is returned when the turn did not become ready in time.
	ErrStartTimeout = errors.New("turn start timeout")
	// ErrNoPendingInterrupt rejects an approval that has no pending record.
	ErrNoPendingInterrupt = errors.New("no pending interrupt")

	// ErrNoCheckpoint rejects a resume whose checkpoint is gone.
	ErrNoCheckpoint = errors.New("no checkpoint to resume")
	// ErrSessionNotFound signals an unknown session id.
	ErrSessionNotFound = errors.New("session not found")

	// errInterrupted is internal: end the loop and record the pending approval.
	errInterrupted = errors.New("interrupted")
)
