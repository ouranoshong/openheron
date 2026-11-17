package storage

import (
	"context"
	"time"
)

// OperationKind names one thing that happened to a session.
//
// ★ The names are a wire contract: the client reads them and the log file
//
//	holds them. A rename is a rename forever — the timeline's whole value is
//	that a reader can look at an old line and know what it means.
type OperationKind string

const (
	// OpSessionCreated says the session exists.
	OpSessionCreated OperationKind = "session.created"
	// OpSessionRenamed says the session's title changed.
	OpSessionRenamed OperationKind = "session.renamed"
	// OpSessionAgentChanged says the session's root agent changed.
	OpSessionAgentChanged OperationKind = "session.agent_changed"
	// OpSessionAuthorizationConfirmed says the person confirmed full
	// authorization for this session, once.
	//
	// ★ It is a line of its own, not a field on the mode change: the
	//   confirmation and the change are two facts, and either can happen
	//   without the other — a confirmation that was recorded while the mode
	//   write failed is still a confirmation.
	OpSessionAuthorizationConfirmed OperationKind = "session.authorization_confirmed"
	// OpSessionAuthorizationChanged says the approval mode changed.
	OpSessionAuthorizationChanged OperationKind = "session.authorization_changed"
	// OpFileUploaded says a file landed in the session's workspace.
	OpFileUploaded OperationKind = "file.uploaded"
	// OpFileDeleted says a file left the session's workspace.
	OpFileDeleted OperationKind = "file.deleted"
	// OpRunStarted says a round took the session.
	OpRunStarted OperationKind = "run.started"
	// OpRunFinished says the round's row reached its terminal state.
	OpRunFinished OperationKind = "run.finished"
	// OpApprovalRequested says an approval is waiting for an answer.
	OpApprovalRequested OperationKind = "approval.requested"
	// OpApprovalResolved says the approval was answered.
	OpApprovalResolved OperationKind = "approval.resolved"
)

// OperationActor says who did it.
type OperationActor string

const (
	// ActorUser means a person did it.
	//
	// ★ It says who, not which code wrote the line: the answer to an
	//   approval is written by the runtime, but a person answered it.
	ActorUser OperationActor = "user"
	// ActorSystem means the server did it.
	ActorSystem OperationActor = "system"
)

// Operation is one entry of a session's operation log.
//
// ★ It is a **projection**, not the truth: every field here points at the
//
//	place that owns the fact (state.json, runs.jsonl, session.jsonl). No
//	decision anywhere reads this struct back — the gate reads the session
//	record, the run scan reads the ledger, the transcript reads the
//	transcript.
type Operation struct {
	// Seq is the session-local sequence number. The log assigns it.
	Seq uint64 `json:"seq"`
	// At is when the operation happened.
	At time.Time `json:"at"`
	// Kind is what happened.
	Kind OperationKind `json:"kind"`
	// Actor is who did it.
	Actor OperationActor `json:"actor"`

	// RunID names the round the operation belongs to.
	RunID string `json:"run_id,omitempty"`
	// TurnKind says what kind of round it was — query or resume.
	//
	// ★ It is not `kind`: that field already says what the *operation* is.
	TurnKind string `json:"turn_kind,omitempty"`
	// InterruptID names the approval the operation is about.
	InterruptID string `json:"interrupt_id,omitempty"`
	// ToolName is the tool the operation is about.
	ToolName string `json:"tool_name,omitempty"`
	// Approved carries the decision.
	//
	// ★ It is a pointer: a rejected approval must be distinguishable from
	//   an operation that is not about an approval at all. A plain bool with
	//   omitempty drops the false, and the rejection disappears.
	Approved *bool `json:"approved,omitempty"`
	// AgentID is the agent the operation is about.
	AgentID string `json:"agent_id,omitempty"`
	// ProjectID is the project the session belongs to.
	ProjectID string `json:"project_id,omitempty"`
	// Mode is the authorization mode.
	Mode string `json:"mode,omitempty"`
	// Title is the session's new title.
	Title string `json:"title,omitempty"`
	// Path is the file's name, relative to the session's workspace.
	Path string `json:"path,omitempty"`
	// Name is the file's name as the person knows it.
	Name string `json:"name,omitempty"`
	// Size is the file's size in bytes.
	Size int64 `json:"size,omitempty"`
	// State is the round's terminal state.
	State string `json:"state,omitempty"`
}

// OperationLog is a session's operation log.
//
// ★ It is append-only and never trimmed: a line that is gone is an
//
//	operation that cannot be looked up again. The run ledger's bound is
//	different in kind — that file is a snapshot view, where the last row per
//	run stands for the run.
type OperationLog interface {
	// Append writes one operation.
	//
	// ★ The log assigns the sequence number and, when the caller left the
	//   time empty, the timestamp. It is the only writer of the number: the
	//   number's order and the file's order have to be the same order.
	Append(ctx context.Context, op Operation) error
	// List returns the session's operations, oldest first.
	List(ctx context.Context) ([]Operation, error)
}
