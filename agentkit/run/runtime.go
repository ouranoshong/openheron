// Package openheronrt owns turn orchestration: admission, preemption, recovery and
// the exact order in which results reach durable storage.
// It renders nothing, speaks no HTTP, and never decides on its own whether a
// run may resume — the checkpoint is the authority on that.
package run

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"openheron/approval"
	"openheron/contract"
	"openheron/storage"
)

// DefaultStartTimeout bounds how long a caller waits for a turn to become
// streamable before giving up on it.
const DefaultStartTimeout = 60 * time.Second

// AgentBuilder produces the agent for one turn. It is injected so openheronrt
// never imports the agent package, which keeps its tests free of a real model.
type AgentBuilder interface {
	Build(ctx context.Context, sessionID, agentID string, kind contract.TurnKind) (adk.TypedAgent[*schema.Message], error)
}

// Config wires the Runtime's collaborators.
type Config struct {
	AgentBuilder   AgentBuilder
	SessionManager storage.SessionManager
	Input          InputConfig
	StartTimeout   time.Duration
}

// ApprovalDecision is one decision about one outstanding interrupt.
type ApprovalDecision struct {
	// InterruptID is the decision's target: the id of one outstanding
	// interrupt. It is the framework's address, not a UUID.
	InterruptID string `json:"interruptId"`
	Approved    bool   `json:"approved"`
	Reason      string `json:"reason,omitempty"`
}

// TurnPhase is the observable stage of a session.
type TurnPhase uint8

const (
	PhaseIdle TurnPhase = iota
	PhaseRunning
	PhaseWaitingApproval
	PhaseStopped
)

func (p TurnPhase) String() string {
	switch p {
	case PhaseIdle:
		return "idle"
	case PhaseRunning:
		return "running"
	case PhaseWaitingApproval:
		return "waiting_approval"
	case PhaseStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// SessionStatus is the projection returned by Status.
type SessionStatus struct {
	SessionID string
	Kind      contract.TurnKind
	Phase     TurnPhase
	StartedAt time.Time
	LastError error
	// RunID identifies the in-flight turn. Empty when idle — the protocol id
	// only exists while a run is open.
	RunID string
}

// Runtime orchestrates turns across sessions.
type Runtime struct {
	cfg Config

	mu     sync.Mutex
	states map[string]*sessionState
}

// New builds a Runtime. It performs no IO and starts nothing.
func New(cfg Config) *Runtime {
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = DefaultStartTimeout
	}
	return &Runtime{cfg: cfg, states: make(map[string]*sessionState)}
}

// Submit admits a query, preempting any turn already running for the session.
func (rt *Runtime) Submit(ctx context.Context, sessionID, query string) (*Turn, error) {
	sess, err := rt.cfg.SessionManager.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if query == "" {
		return nil, errors.New("query must not be empty")
	}

	rt.discardCheckpoint(ctx, sess)

	if pending, perr := sess.PendingApproval(); perr == nil && pending != nil && len(pending.Interrupts) > 0 {
		if err := sess.ClearPendingApproval(); err != nil {
			rt.logf("session=%s failed to clear the stale pending approval: %v", sessionID, err)
		}
	}

	return rt.startTurn(ctx, sess, loopItem{Query: query}, contract.TurnQuery)
}

func (rt *Runtime) discardCheckpoint(ctx context.Context, sess storage.Session) {
	_, deleter := sess.GetCheckPointStore()
	if deleter == nil {
		return
	}
	if err := deleter.Delete(ctx, sess.ID()); err != nil {
		rt.logf("session=%s failed to discard the stale checkpoint: %v", sess.ID(), err)
	}
}

// Resume delivers approval decisions for the pending interrupts.
// The decisions are refused unless the session holds a persisted pending
// record: the framework exposes no interrupt id to the application, so that
// record is the only gate (architecture.md decision 7 and 8).
func (rt *Runtime) Resume(ctx context.Context, sessionID string, decisions []ApprovalDecision) (*Turn, error) {
	sess, err := rt.cfg.SessionManager.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	if !rt.hasCheckpoint(ctx, sess) {
		return nil, fmt.Errorf("%w (session %s)", ErrNoCheckpoint, sessionID)
	}

	pending, err := sess.TakePendingApproval()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoPendingInterrupt, err)
	}
	if pending == nil || len(pending.Interrupts) == 0 {
		return nil, ErrNoPendingInterrupt
	}

	approvals := map[string]any{}
	for _, d := range decisions {
		if d.InterruptID == "" {
			continue
		}
		approvals[d.InterruptID] = approvalResult(d)
	}
	if len(approvals) == 0 {
		return nil, ErrNoPendingInterrupt
	}

	rt.recordDecisions(sess, pending, decisions)

	item := loopItem{Approvals: approvals}
	return rt.startTurn(ctx, sess, item, contract.TurnResume)
}

func (rt *Runtime) recordDecisions(sess storage.Session, pending *storage.PendingApproval, decisions []ApprovalDecision) {
	details := map[string]*contract.InterruptDetail{}
	if pending != nil {
		for _, it := range pending.Interrupts {
			details[it.InterruptID] = it.Detail
		}
	}

	now := time.Now().UTC()
	recs := make([]storage.ApprovalRecord, 0, len(decisions))
	for _, d := range decisions {
		if d.InterruptID == "" {
			continue
		}
		rec := storage.ApprovalRecord{
			InterruptID: d.InterruptID,
			Approved:    d.Approved,
			Reason:      d.Reason,
			DecidedAt:   now,
		}
		if det := details[d.InterruptID]; det != nil {
			rec.ToolName = det.ToolName
			rec.RequestedByAgent = det.RequestedByAgent
		}
		recs = append(recs, rec)
	}
	if len(recs) == 0 {
		return
	}
	if err := sess.RecordDecision(recs...); err != nil {
		rt.logf("session=%s failed to record the approval decisions: %v", sess.ID(), err)
	}
}

func (rt *Runtime) hasCheckpoint(ctx context.Context, sess storage.Session) bool {
	store, _ := sess.GetCheckPointStore()
	if store == nil {
		return true
	}
	ok, err := sess.HasCheckpoint(ctx)
	if err != nil {
		rt.logf("session=%s failed to read the checkpoint: %v", sess.ID(), err)
		return false
	}
	return ok
}

// approvalResult maps the HTTP decision onto the payload the gated tool reads
// back on resume.
func approvalResult(d ApprovalDecision) any {
	return &approval.Decision{Approved: d.Approved, Reason: d.Reason}
}

// startTurn binds a fresh turn, superseding any predecessor, and gets a loop
// running for it.
func (rt *Runtime) startTurn(ctx context.Context, sess storage.Session, item loopItem, kind contract.TurnKind) (*Turn, error) {
	turn := newTurnHandle(sess.ID(), kind)

	st := rt.stateFor(sess.ID())

	st.markStarted(time.Now())

	st.mu.Lock()
	prev := st.turn
	st.turn = turn
	st.mu.Unlock()

	if prev != nil && prev != turn {
		// Release the outgoing turn: its Wait must fail with ErrSuperseded and
		// its OnAgentEvents handshake must not block on a Complete that will
		// never arrive.
		prev.fail(ErrSuperseded)
	}

	loop := rt.newLoop(ctx, sess, turn)
	st.setLoop(loop)

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	turn.cancel = cancel

	rt.startLoop(runCtx, sess.ID(), loop, cancel)

	if ok, _ := loop.Push(item, adk.WithPreempt[loopItem, *schema.Message](adk.AfterToolCalls)); !ok {
		rt.logf("session=%s push was rejected: the loop is stopping or its buffer is full", sess.ID())
	}

	return turn, nil
}

// Abort stops the current turn immediately.
func (rt *Runtime) Abort(ctx context.Context, sessionID string) error {
	st, ok := rt.lookupState(sessionID)
	if !ok {
		return nil
	}
	loop := st.currentLoop()
	if loop == nil {
		return nil
	}

	loop.Stop(adk.WithImmediate())

	// Release the in-flight turn as well. Without this the loop would wait on
	// an OnAgentEvents handshake that nobody can complete: a consumer that
	// stopped reading would block the loop, which would keep the iterator
	// open, which would block the consumer. Fail the turn to break the cycle.
	if turn := st.currentTurn(); turn != nil {
		turn.fail(context.Canceled)
	}

	loop.Wait()

	st.clearTurn()
	rt.removeStateIfIdle(sessionID)
	return nil
}

// Cleanup stops the turn, removes the checkpoint and forgets the session.
// It is safe to call for an unknown id, which is what lets the session layer
// inject it as a teardown hook without first checking existence.
func (rt *Runtime) Cleanup(ctx context.Context, sessionID string) error {
	_ = rt.Abort(ctx, sessionID)

	if sess, err := rt.cfg.SessionManager.Get(ctx, sessionID); err == nil {
		if _, deleter := sess.GetCheckPointStore(); deleter != nil {
			if err := deleter.Delete(ctx, sessionID); err != nil {
				rt.logf("session=%s checkpoint delete failed during cleanup: %v", sessionID, err)
			}
		}
	}

	rt.mu.Lock()
	delete(rt.states, sessionID)
	rt.mu.Unlock()
	return nil
}

// Shutdown stops every running loop.
func (rt *Runtime) Shutdown(ctx context.Context) error {
	rt.mu.Lock()
	ids := make([]string, 0, len(rt.states))
	for id := range rt.states {
		ids = append(ids, id)
	}
	rt.mu.Unlock()

	for _, id := range ids {
		if err := rt.Abort(ctx, id); err != nil {
			rt.logf("shutdown: session=%s abort failed: %v", id, err)
		}
	}
	return nil
}

// Status reports the observable state of every known session.
func (rt *Runtime) Status() []SessionStatus {
	rt.mu.Lock()
	ids := make([]string, 0, len(rt.states))
	for id := range rt.states {
		ids = append(ids, id)
	}
	rt.mu.Unlock()

	out := make([]SessionStatus, 0, len(ids))
	for _, id := range ids {
		if st, ok := rt.lookupState(id); ok {
			st.mu.Lock()
			phase := PhaseIdle
			if st.turn != nil {
				phase = PhaseRunning
			}
			kind := contract.TurnQuery
			if st.turn != nil {
				kind = st.turn.Kind
			}
			started, lastErr := st.startedAt, st.lastError
			st.mu.Unlock()
			out = append(out, SessionStatus{
				SessionID: id, Phase: phase, Kind: kind,
				StartedAt: started, LastError: lastErr,
			})
		}
	}
	return out
}

// StatusOf reports one session's phase, resolving the waiting-approval state
// from the session itself.
//
//	approval lives in the session file, not in the volatile runtime state, and
//	a session with a pending approval has no loop running — it would report
//	PhaseIdle and the sidebar dot would lie.
func (rt *Runtime) StatusOf(ctx context.Context, sessionID string) (SessionStatus, bool) {
	sm := rt.cfg.SessionManager
	if sm == nil {
		return SessionStatus{}, false
	}
	sess, err := sm.Get(ctx, sessionID)
	if err != nil {
		return SessionStatus{}, false
	}
	pending, err := sess.PendingApproval()
	if err != nil {
		return SessionStatus{}, false
	}

	out := SessionStatus{SessionID: sessionID, Phase: PhaseIdle}
	if pending != nil {
		out.Phase = PhaseWaitingApproval
	}
	if st, ok := rt.lookupState(sessionID); ok {
		st.mu.Lock()
		out.StartedAt, out.LastError, out.Kind = st.startedAt, st.lastError, contract.TurnQuery
		if st.turn != nil {
			if out.Phase != PhaseWaitingApproval {
				out.Phase = PhaseRunning
			}
			out.Kind = st.turn.Kind
		}
		st.mu.Unlock()
	}
	return out, true
}

func (rt *Runtime) lookupState(sessionID string) (*sessionState, bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	st, ok := rt.states[sessionID]
	return st, ok
}
