package storage

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
)

// ErrSessionNotFound signals an unknown session id.
var ErrSessionNotFound = errors.New("session not found")

// SessionManager is the session store contract.
type SessionManager interface {
	// Create mints a new session with a fresh UUID.
	Create(ctx context.Context, agentID string) (Session, error)
	// Get returns an existing session, or ErrSessionNotFound.
	Get(ctx context.Context, id string) (Session, error)
	// List returns metadata for every known session.
	List(ctx context.Context) ([]Meta, error)
	// Delete removes the session and its derived artefacts.
	Delete(ctx context.Context, id string) error
	// Inspect reconciles session state against checkpoint state at startup.
	Inspect(ctx context.Context) (Report, error)
}

// Session is the aggregate root of one conversation.
// Messages, pending approval, cursor and workspace are all persisted state;
// memory is only a cache. Every read returns a snapshot the caller must not
// mutate in place.
type Session interface {
	ID() string
	CreatedAt() time.Time

	// Message log. Append-only.
	Append(msg *schema.Message) error
	// Messages returns the session's messages.
	//
	// effective selects the view: false — the default — returns the whole
	// transcript, which is the audit trail and what the UI renders. true
	// returns the effective history: the last compaction message and
	// everything after it, which is what the agent's context is built from.
	Messages(effective bool) []*schema.Message
	// Len reports how many messages the session holds. It counts the whole
	// transcript: it is a fact about the record, not about the context.
	Len() int

	// SetPendingApproval is fail-closed: if the state cannot be persisted the
	// approval must not be admitted, so an error is returned and nothing is
	// held in memory either.
	SetPendingApproval(p *PendingApproval) error
	// PendingApproval peeks without consuming. The render path needs this:
	// calling TakePendingApproval there would swallow the approval.
	PendingApproval() (*PendingApproval, error)
	// TakePendingApproval atomically takes and clears the pending approval.
	TakePendingApproval() (*PendingApproval, error)
	ClearPendingApproval() error

	// RecordDecision appends approval decisions to the session's audit log.
	RecordDecision(recs ...ApprovalRecord) error
	// Decisions returns the recorded approval decisions, oldest first.
	Decisions() []ApprovalRecord

	// Title is the cached display title. Empty means the cache was never
	// populated; callers fall back to deriving it from the transcript.
	Title() string
	// SetTitle overrides the cached title. Rejects an empty title so the
	// cache is never silently cleared.
	SetTitle(title string) error

	// AgentID is the session's agent. Empty means main — that is what
	// sessions written before the field existed read as.
	AgentID() string
	// SetAgentID persists a new root agent. It only affects later turns.
	//   not visible here, and a session that is mid-turn must not be mutated
	//   underneath the loop.
	SetAgentID(id string) error

	Workspace() string
	PutFile(name string, r io.Reader) (string, error)

	// ResolveWorkspaceFile turns a name relative to this session's
	// workspace into an absolute path inside it.
	//
	// ★ It is the **only** way the outside world may name a file in the
	//   workspace. A name that escapes the workspace is refused, not
	//   clamped: a clamped path would silently read a different file, and
	//   the model would answer about the wrong thing.
	//
	// ★ The path is resolved, not checked for existence — the caller
	//   decides what a missing file means.
	ResolveWorkspaceFile(name string) (string, error)

	// DeleteWorkspaceFile removes a file from this session's workspace.
	//
	// ★ The name is relative to the workspace, and a name that escapes it is
	//   refused — the same rule as ResolveWorkspaceFile, and for the same
	//   reason: a delete that lands outside would remove a file the session
	//   never owned.
	//
	// ★ The granularity is the **file**, not the message. A file may be
	//   referenced by several messages, and the attachment list of a
	//   message is history — it is not a reference count to maintain.
	DeleteWorkspaceFile(name string) error

	// RecordUpload remembers the name a workspace file had before it was
	// renamed.
	//
	// ★ A file is stored under a name that carries a hash of its own
	//   content and time, so that two uploads of the same name do not
	//   overwrite each other. The name the user recognises exists **only**
	//   here — and the listing endpoint needs it, because the interface
	//   shows names, not hashes.
	//
	// ★ The path is relative to the workspace, like every other name this
	//   interface speaks.
	RecordUpload(path, originalName string) error

	// OriginalNames returns the map from workspace-relative paths to the
	// names the files carried when they were uploaded.
	//
	// ★ It is a map, not a lookup, because the listing walks every file
	//   in the workspace: one lookup per file would read the map once per
	//   file.
	//
	// ★ It may be empty — a session whose files were never renamed has
	//   nothing to map, and a file that was never renamed is simply not
	//   in it.
	OriginalNames() map[string]string

	Init(agentID string) error
	Delete() error
	Meta() Meta
	HasPending() bool
	HasCheckpoint(ctx context.Context) (bool, error)
	GetCheckPointStore() (adk.CheckPointStore, adk.CheckPointDeleter)

	// RunLedger returns this session's run ledger.
	//
	// ★ The ledger is a durable record of the session's runs: who ran,
	//	when, and how it ended. It is what a reader scans to learn whether
	//	the session is busy — the volatile runtime state cannot answer that
	//	after a crash.
	RunLedger() RunLedger
}

// PendingApproval is the set of outstanding human decisions.
type PendingApproval struct {
	// Interrupts are every outstanding decision, in the order the framework
	// reported them. Never empty for a live pending approval.
	Interrupts []contract.PendingInterrupt
}

// Has reports whether id is one of the outstanding decisions.
func (p *PendingApproval) Has(id string) bool {
	if p == nil {
		return false
	}
	for _, it := range p.Interrupts {
		if it.InterruptID == id {
			return true
		}
	}
	return false
}

type ApprovalRecord struct {
	InterruptID      string    `json:"interrupt_id"`
	ToolName         string    `json:"tool_name,omitempty"`
	Approved         bool      `json:"approved"`
	Reason           string    `json:"reason,omitempty"`
	DecidedAt        time.Time `json:"decided_at"`
	RequestedByAgent string    `json:"requested_by_agent,omitempty"`
}

// Meta summarises a session for listing.
type Meta struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt advances on every append, so listings can sort by activity
	// instead of falling back to directory order.
	UpdatedAt time.Time `json:"updated_at"`
	// MessageCount is the number of persisted messages, so the sidebar can
	// show it without loading the transcript.
	MessageCount int `json:"message_count"`
}

// Report counts the outcomes of the startup consistency pass.
type Report struct {
	Clean          int // no pending, no checkpoint
	PendingOK      int // pending and checkpoint agree
	Orphan         int // pending without checkpoint — kept, so the card stays visible
	Ownerless      int // checkpoint without pending, unrecoverable
	SkippedBadJSON int // unreadable state, skipped
	// InterruptedRuns counts the runs that were still open when the process
	// last stopped. They are closed as "interrupted" during the pass, so a
	// crash leaves a trace instead of a session that reads as idle.
	InterruptedRuns int
}
