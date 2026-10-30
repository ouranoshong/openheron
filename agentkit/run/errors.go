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

	// ErrSessionBusy rejects a run against a session that already has a
	// live turn or a pending approval.
	//
	// ★ It is a conflict, not a failure: the caller may retry once the
	//   session goes idle. The HTTP layer maps it to 409.
	//
	// ★ A session has at most one live turn. The second run is refused
	//   rather than preempting the first one — preemption cannot tell
	//   "the user interrupting themselves" from "another tab cutting in",
	//   and the second case destroys a turn the user cannot see, along
	//   with its checkpoint and its pending approval.
	ErrSessionBusy = errors.New("session is busy")

	// errInterrupted is internal: end the loop and record the pending approval.
	errInterrupted = errors.New("interrupted")
)
