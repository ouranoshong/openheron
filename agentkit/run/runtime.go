// Package openheronrt owns turn orchestration: admission, preemption, recovery and
// the exact order in which results reach durable storage.
// It renders nothing, speaks no HTTP, and never decides on its own whether a
// run may resume — the checkpoint is the authority on that.
package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/ouranoshong/openheron/approval"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
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
	// InstanceID names this process in the run ledger's rows.
	//
	// ★ It answers "which instance wrote this row" — the question a
	//	post-mortem asks first. It defaults to the host name and the pid.
	InstanceID string
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

	// instanceID is the owner written into the ledger's rows.
	instanceID string

	mu     sync.Mutex
	states map[string]*sessionState
}

// New builds a Runtime. It performs no IO and starts nothing.
func New(cfg Config) *Runtime {
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = DefaultStartTimeout
	}
	if cfg.InstanceID == "" {
		cfg.InstanceID = defaultInstanceID()
	}
	return &Runtime{cfg: cfg, instanceID: cfg.InstanceID, states: make(map[string]*sessionState)}
}

// defaultInstanceID names this process.
//
// ★ The host name and the pid are enough to tell two processes apart, and
//
//	they survive a restart as a trace: a row written by an instance that
//	no longer exists is exactly what a reconciler looks for.
func defaultInstanceID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s:%d", host, os.Getpid())
}

// newRunID mints a protocol run id.
//
// ★ The id is the run's identity: it is what the client sees in every SSE
//
//	frame, what the ledger's rows are keyed by, and what a reconnecting
//	client sends back to re-attach. It is minted once, here, and never
//	reused.
func (rt *Runtime) newRunID() string { return uuid.NewString() }

// Submit admits a query into the session.
//
// ★ A session has at most one live turn. A second Submit on a busy
//
//	session is refused with ErrSessionBusy rather than preempting the
//	first one: preemption cannot tell "the user interrupting themselves"
//	from "another tab cutting in", and the second case destroys a turn
//	the user cannot see — along with its checkpoint and its pending
//	approval.
//
// ★ The gate runs FIRST, and everything it protects runs after it. The
//
//	checkpoint discard and the pending-approval clear below are
//	destructive; a refused request must change nothing.
//
// ★ runID is the run's identity. An empty one is minted by the runtime —
//
//	a caller that already has one (a client re-attaching to a run it
//	knows) hands it in, and the run carries it from the first row to the
//	last.
func (rt *Runtime) Submit(ctx context.Context, sessionID, runID, query string) (*Turn, error) {
	sess, err := rt.cfg.SessionManager.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if query == "" {
		return nil, errors.New("query must not be empty")
	}

	if err := rt.ensureIdle(ctx, sess); err != nil {
		return nil, err
	}

	turn, loop, prev, _, err := rt.claimTurn(ctx, sess, contract.TurnQuery, runID, "")
	if err != nil {
		return nil, err
	}

	// From here the session is ours: the checkpoint and the pending
	// approval belong to a turn that is over, and discarding them is what
	// makes the next turn start from a clean slate.
	rt.discardCheckpoint(ctx, sess)

	if pending, perr := sess.PendingApproval(); perr == nil && pending != nil && len(pending.Interrupts) > 0 {
		if err := sess.ClearPendingApproval(); err != nil {
			rt.logf("session=%s failed to clear the stale pending approval: %v", sessionID, err)
		}
	}

	rt.runClaimedTurn(ctx, sess, turn, loop, prev, loopItem{Query: query})
	return turn, nil
}

// claimTurn reserves the session's turn slot for a new turn.
//
// ★ The busy check and the takeover happen under one lock, so two
//
//	simultaneous requests cannot both pass it. The gate in Submit and
//	Resume is the friendly error; this is the rule.
//
// ★ It returns the turn it claimed, the loop that will run it, and the
//
//	turn it displaced. The displaced one has already completed — a live
//	one would have been refused — so it only needs its handshake
//	released.
//
// ★ The claimed turn gets a row in the session's run ledger before the
//
//	caller is told anything. A run that is not recorded cannot be scanned,
//	and a run that cannot be scanned is a run whose busy state is a guess.
func (rt *Runtime) claimTurn(ctx context.Context, sess storage.Session, kind contract.TurnKind,
	runID, parentRunID string) (
	*Turn, *adk.TurnLoop[loopItem, *schema.Message], *Turn, *adk.TurnLoop[loopItem, *schema.Message], error) {
	if runID == "" {
		runID = rt.newRunID()
	}
	turn := newTurnHandle(sess.ID(), kind)
	turn.RunID = runID
	st := rt.stateFor(sess.ID())
	loop := rt.newLoop(ctx, sess, turn)

	st.mu.Lock()
	prev := st.turn
	prevLoop := st.loop
	busy := prev != nil && st.loop != nil && conflictTurns(kind, prev.Kind)
	if !busy {
		st.turn = turn
		st.loop = loop
		st.startedAt = time.Now()
		st.lastError = nil
	}
	st.mu.Unlock()

	if busy {
		return nil, nil, nil, nil, fmt.Errorf("%w: a turn is already running on this session", ErrSessionBusy)
	}

	row, err := rt.openRunRow(ctx, sess, turn, kind, parentRunID)
	if err != nil {
		// ★ The run cannot be recorded, so it cannot be owned: give the
		//   slot back and refuse. A run that exists without a row would
		//   leave the session unreadable to every scan.
		rt.releaseClaim(sess, turn, prev, prevLoop)
		return nil, nil, nil, nil, err
	}
	turn.row = row

	return turn, loop, prev, prevLoop, nil
}

// openRunRow writes the run's first row and returns the writer that keeps
// it up to date.
//
// ★ The row is written BEFORE the caller is told the run started. That is
//
//	the whole point of the ledger: the run's existence is a fact on disk,
//	not a promise in memory.
//
// ★ A session without a ledger gets no row. That is not a failure — the
//
//	ledger is a storage driver, and a session that has none simply keeps
//	the volatile behaviour it had before.
func (rt *Runtime) openRunRow(ctx context.Context, sess storage.Session, turn *Turn,
	kind contract.TurnKind, parentRunID string) (*runRow, error) {
	ledger := sess.RunLedger()
	if ledger == nil {
		return nil, nil
	}

	epoch, err := ledger.NextEpoch(ctx)
	if err != nil {
		return nil, fmt.Errorf("allocate the run epoch: %w", err)
	}

	now := time.Now().UTC()
	row := storage.Run{
		RunID:       turn.RunID,
		SessionID:   sess.ID(),
		Kind:        kind,
		State:       storage.RunStarting,
		Epoch:       epoch,
		Owner:       rt.instanceID,
		ParentRunID: parentRunID,
		StartedAt:   now,
		UpdatedAt:   now,
	}
	if err := ledger.Append(ctx, row); err != nil {
		return nil, fmt.Errorf("write the run row: %w", err)
	}
	return newRunRow(ledger, row), nil
}

// releaseClaim gives the slot back when a claimed turn cannot start.
//
// ★ Only the claim's own turn is released: a turn that replaced it in
//
//	the meantime keeps its slot.
//
// ★ The previous turn's loop is put back, not dropped. It may still be
//
//	archiving: the claim replaced the pointer while that loop was alive,
//	and clearing it would make the session look idle while the archive is
//	still in flight — the next turn would then read an incomplete
//	history.
func (rt *Runtime) releaseClaim(sess storage.Session, turn *Turn, prev *Turn, prevLoop *adk.TurnLoop[loopItem, *schema.Message]) {
	st := rt.stateFor(sess.ID())
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.turn != turn {
		// A newer turn took over in the meantime; its state is not ours
		// to touch.
		return
	}
	st.turn = prev
	st.loop = prevLoop
}

// runClaimedTurn starts the loop for a turn that has already claimed the
// session's slot.
func (rt *Runtime) runClaimedTurn(ctx context.Context, sess storage.Session, turn *Turn,
	loop *adk.TurnLoop[loopItem, *schema.Message], prev *Turn, item loopItem) {

	if prev != nil && prev != turn {
		// The outgoing turn has already completed — the claim would have
		// refused a live one. Release its handshake so the OnAgentEvents
		// side is not left waiting for a Complete that will never arrive.
		prev.fail(ErrSuperseded)
	}

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	turn.cancel = cancel

	rt.startLoop(runCtx, sess.ID(), turn, loop, cancel)

	if ok, _ := loop.Push(item, adk.WithPreempt[loopItem, *schema.Message](adk.AfterToolCalls)); !ok {
		rt.logf("session=%s push was rejected: the loop is stopping or its buffer is full", sess.ID())
	}
}

// ensureIdle refuses the request when the session already owes the user
// something.
//
// ★ Both cases mean the context belongs to something else right now:
//   - a live turn: it is executing;
//   - a pending approval: the checkpoint it would resume from is the
//     session's context, and answering it is the only way forward.
//
// ★ The check is a read. It does not touch the checkpoint and it does not
//
//	clear the pending approval — a refused request must change nothing.
//	Those two side effects live in Submit, after the gate.
func (rt *Runtime) ensureIdle(ctx context.Context, sess storage.Session) error {
	if p, err := sess.PendingApproval(); err == nil && p != nil && len(p.Interrupts) > 0 {
		return fmt.Errorf("%w: an approval is pending on this session", ErrSessionBusy)
	}
	return rt.ensureNoLiveTurn(ctx, sess)
}

// ensureNoLiveTurn refuses the request when a turn is executing.
//
// ★ It asks about the LOOP, not the turn pointer alone. st.turn is
//
//	cleared when the turn finishes archiving (markTurnDone), so it lags:
//	a turn that has already produced its answer still reads as "running"
//	for a moment, and refusing on that would be a false positive. The
//	loop is the thing that is actually executing.
//
// ★ Both are needed. st.turn is set while the turn runs and cleared once
//
//	its outcome is archived; st.loop is set while the loop lives, which
//	is longer — the loop stays alive waiting for the next item. The pair
//	reads "busy" only while a turn is genuinely in flight.
func (rt *Runtime) ensureNoLiveTurn(ctx context.Context, sess storage.Session) error {
	st, ok := rt.lookupState(sess.ID())
	if !ok {
		return nil
	}
	st.mu.Lock()
	turn, loop := st.turn, st.loop
	st.mu.Unlock()
	if turn != nil && loop != nil {
		return fmt.Errorf("%w: a turn is already running on this session", ErrSessionBusy)
	}
	return nil
}

// conflictTurns reports whether a new turn of kind "kind" may not start
// while a turn of kind "live" is running.
//
// ★ A resume continues the interrupted turn it answers — the same round
//
//	of the conversation — so two resumes never conflict. A query is a
//	new round, and that is exactly what must not overlap: the
//	cross-client case, where a second surface's message would take the
//	running turn over.
//
// ★ So the only conflicting pair is query-behind-query. Everything else
//
//	— a resume behind a query (the user answering an approval raised by
//	the running query), or a resume behind a resume — is the same
//	conversation continuing.
func conflictTurns(kind, live contract.TurnKind) bool {
	if live == contract.TurnResume {
		return false
	}
	return kind == contract.TurnQuery
}

// ensureNoLiveQuery refuses the request when a QUERY turn is executing.
//
// ★ A resume turn is not a conflict with another resume. A resume
//
//	continues the interrupted turn it answers — it is the same round of
//	the conversation, not a new one — so the user may answer a second
//	group of approvals while the first resume is still settling. What
//	must never overlap is a fresh query: that is the "another tab cuts
//	in" case, and it is what destroys the running turn.
//
// ★ So the gate is about the KIND of the live turn:
//   - a query turn running → refuse (a new query would take it over);
//   - a resume turn running → allow (it is the same conversation).
func (rt *Runtime) ensureNoLiveQuery(ctx context.Context, sess storage.Session) error {
	st, ok := rt.lookupState(sess.ID())
	if !ok {
		return nil
	}
	st.mu.Lock()
	turn, loop := st.turn, st.loop
	st.mu.Unlock()
	if turn != nil && loop != nil && turn.Kind != contract.TurnResume {
		return fmt.Errorf("%w: a turn is already running on this session", ErrSessionBusy)
	}
	return nil
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
//
// ★ A resume is a NEW run, not a continuation of the old one: the run that
//
//	raised the approval has ended. The new run names it as its parent, so
//	the ledger can tell which question the answer belongs to.
func (rt *Runtime) Resume(ctx context.Context, sessionID, runID string, decisions []ApprovalDecision) (*Turn, error) {
	sess, err := rt.cfg.SessionManager.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	// ★ The pending approval is what this call is FOR — do not refuse on
	//   it. Only a live turn is a conflict: somebody else's turn is
	//   running, and this one would take it over.
	//
	// ★ This is a friendly pre-check; the authoritative one is the claim
	//   below, which happens under a lock.
	if err := rt.ensureNoLiveQuery(ctx, sess); err != nil {
		return nil, err
	}

	if !rt.hasCheckpoint(ctx, sess) {
		return nil, fmt.Errorf("%w (session %s)", ErrNoCheckpoint, sessionID)
	}

	// ★ Claim the slot BEFORE consuming the approval. TakePendingApproval
	//   clears the record and writes the disk, so a refusal after that
	//   point would eat the user's decision: the card disappears and the
	//   approval can never be answered again. Claiming first means a
	//   refusal leaves the approval untouched.
	turn, loop, prev, prevLoop, err := rt.claimTurn(ctx, sess, contract.TurnResume, runID,
		rt.parentRunOf(ctx, sess))
	if err != nil {
		return nil, err
	}

	pending, err := sess.TakePendingApproval()
	if err != nil {
		rt.releaseClaim(sess, turn, prev, prevLoop)
		return nil, fmt.Errorf("%w: %v", ErrNoPendingInterrupt, err)
	}
	if pending == nil || len(pending.Interrupts) == 0 {
		rt.releaseClaim(sess, turn, prev, prevLoop)
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
		rt.releaseClaim(sess, turn, prev, prevLoop)
		return nil, ErrNoPendingInterrupt
	}

	rt.recordDecisions(sess, pending, decisions)

	item := loopItem{Approvals: approvals}
	rt.runClaimedTurn(ctx, sess, turn, loop, prev, item)
	return turn, nil
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

// Abort stops the current turn immediately.
func (rt *Runtime) Abort(ctx context.Context, sessionID string) error {
	st, ok := rt.lookupState(sessionID)
	if !ok {
		// ★ No runtime state, but the session may still owe a decision: a
		//   turn that stopped to ask for approval leaves its approval on
		//   disk, and the runtime forgets the session once it goes idle.
		//   Clearing it is what lets the user take over.
		rt.clearVoidApproval(ctx, sessionID)
		return nil
	}
	loop := st.currentLoop()
	if loop == nil {
		// ★ No loop to stop, but the session may still owe a decision: a
		//   turn that stopped to ask for approval leaves its approval
		//   behind. Clearing it is what lets the user take over.
		rt.clearVoidApproval(ctx, sessionID)
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

	// ★ The pending approval belonged to the turn that just stopped. It is
	//   void now: there is no decision left to make, and leaving it behind
	//   would keep the session locked — the gate refuses a query while an
	//   approval is pending, so "stop, then take over" would not take over.
	//
	// ★ Only an ORPHANED approval (the checkpoint is gone) is worth
	//   keeping; that one cannot be answered either, but it is not this
	//   call's business and the card stays on screen.
	rt.clearVoidApproval(ctx, sessionID)

	return nil
}

// clearVoidApproval drops a pending approval that no turn is waiting for.
//
// ★ It runs after the turn stopped, so the approval it clears belongs to a
//
//	turn that no longer exists.
func (rt *Runtime) clearVoidApproval(ctx context.Context, sessionID string) {
	sess, err := rt.cfg.SessionManager.Get(ctx, sessionID)
	if err != nil {
		return
	}
	pending, perr := sess.PendingApproval()
	if perr != nil || pending == nil || len(pending.Interrupts) == 0 {
		return
	}
	if err := sess.ClearPendingApproval(); err != nil {
		rt.logf("session=%s failed to clear the void approval on abort: %v", sessionID, err)
	}
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
//
// ★ The phase comes from the run ledger, not from the volatile map. The map
//
//	says "a turn handle exists"; the ledger says "a run is open", and the
//	second survives a restart while the first does not.
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
	if active, ok := rt.activeRun(ctx, sess); ok {
		// ★ A live run outranks a pending approval: the run is executing
		//   now, and the approval it may raise comes later.
		out.Phase = PhaseRunning
		out.RunID = active.RunID
		out.Kind = active.Kind
	}
	if st, ok := rt.lookupState(sessionID); ok {
		st.mu.Lock()
		out.StartedAt, out.LastError = st.startedAt, st.lastError
		st.mu.Unlock()
	}
	return out, true
}

// activeRun returns the run that currently owns the session.
//
// ★ It is a scan of the ledger, never the volatile state: the ledger is
//
//	the only source that knows about a run this process did not start.
func (rt *Runtime) activeRun(ctx context.Context, sess storage.Session) (storage.Run, bool) {
	ledger := sess.RunLedger()
	if ledger == nil {
		return storage.Run{}, false
	}
	runs, err := ledger.List(ctx)
	if err != nil {
		logf("session=%s failed to read the run ledger: %v", sess.ID(), err)
		return storage.Run{}, false
	}
	return storage.ActiveRun(runs)
}

// parentRunOf returns the run a resume continues.
//
// ★ It is the newest run that ended by raising an approval. The pending
//
//	approval itself does not carry the run's id — the record that holds
//	the interrupt id is the checkpoint's business — so the ledger is where
//	the question's owner is looked up.
func (rt *Runtime) parentRunOf(ctx context.Context, sess storage.Session) string {
	ledger := sess.RunLedger()
	if ledger == nil {
		return ""
	}
	runs, err := ledger.List(ctx)
	if err != nil {
		logf("session=%s failed to read the run ledger: %v", sess.ID(), err)
		return ""
	}
	storage.SortRunsByStart(runs)
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].State == storage.RunWaitingApproval {
			return runs[i].RunID
		}
	}
	return ""
}

// RunSnapshot is the scan of one session's runs.
//
// ★ It is what a reader gets: the rows, and the one run that owns the
//
//	session right now. The two are separate on purpose — the rows are the
//	history, the active run is the present.
type RunSnapshot struct {
	SessionID   string
	Runs        []storage.Run
	ActiveRunID string
}

// ScanRuns reads a session's run ledger.
//
// ★ This is the truth. The volatile runtime state may say "running" for a
//
//	turn that already ended, and says nothing at all about a turn another
//	instance started.
func (rt *Runtime) ScanRuns(ctx context.Context, sessionID string) (RunSnapshot, error) {
	sm := rt.cfg.SessionManager
	if sm == nil {
		return RunSnapshot{}, errors.New("no session manager is configured")
	}
	sess, err := sm.Get(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}

	out := RunSnapshot{SessionID: sess.ID(), Runs: []storage.Run{}}
	ledger := sess.RunLedger()
	if ledger == nil {
		return out, nil
	}
	runs, err := ledger.List(ctx)
	if err != nil {
		return out, err
	}
	storage.SortRunsByStart(runs)
	out.Runs = runs
	if active, ok := storage.ActiveRun(runs); ok {
		out.ActiveRunID = active.RunID
	}
	return out, nil
}

func (rt *Runtime) lookupState(sessionID string) (*sessionState, bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	st, ok := rt.states[sessionID]
	return st, ok
}
