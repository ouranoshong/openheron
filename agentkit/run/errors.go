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

	// ErrAgentUnavailable says the turn could not start because the agent
	// itself could not be built — the configuration is broken, not the turn.
	//
	// ★ It is its own error because the answer the user needs is "fix the
	//   configuration", and "turn start timeout" is not it: a timeout names
	//   a symptom, and the reason it caused is a file and a line. The
	//   wrapped error carries that reason — an agent left out of the tree
	//   says which model is missing.
	//
	// ★ The run package cannot import the builder (that would invert the
	//   dependency), so the sentinel lives here and the builder's own error
	//   is wrapped around it.
	ErrAgentUnavailable = errors.New("the agent is not available")

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

// agentUnavailable is the error a turn fails with when the agent cannot be
// built: the builder's reason, and the sentinel for classification.
//
// ★ The message is the builder's **own**: "agent \"main\" is left out of the
//
//	tree: the model … is not declared under llm/" already says everything.
//	Prefixing it with the sentinel would make the user read the same fact
//	twice, and the code on the wire is what tells the client how to react.
type agentUnavailable struct {
	reason error
}

func (e agentUnavailable) Error() string { return e.reason.Error() }

// Unwrap keeps the builder's reason reachable: errors.Is finds it.
func (e agentUnavailable) Unwrap() error { return e.reason }

// Is answers for the sentinel alone — the classification is the run's, not
// the builder's.
func (e agentUnavailable) Is(target error) bool { return target == ErrAgentUnavailable }
