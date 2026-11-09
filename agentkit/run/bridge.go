package run

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
)

// loopItem is the TurnLoop's unit of work. One user query or one approval
// decision travels as a single item.
type loopItem struct {
	Query string

	// Approvals maps an interrupt id to the decision for it. It is empty for
	// a normal turn, and holds **every** outstanding decision for a resume
	// turn — see [storage.PendingApproval] for why a resume must answer them all at
	// once.
	Approvals map[string]any
}

// sessionState is the per-session runtime state. It is deliberately not the
// session: sessions are durable data, this is volatile execution state.
type sessionState struct {
	mu   sync.Mutex
	loop *adk.TurnLoop[loopItem, *schema.Message]
	turn *Turn
	// startedAt and lastError back SessionStatus. Both were declared but
	// never assigned, which made every reported session look like it had
	// just started and never failed.
	startedAt time.Time
	lastError error
}

func (st *sessionState) markStarted(t time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.startedAt = t
	st.lastError = nil
}

// markTurnDone releases the turn pointer once its consumer has reported.
// Only the matching handle is cleared, so a turn superseded in the meantime
// is not disturbed.
//
// ★ It also releases whoever waits on the handle. The pointer is what the
// admission gate reads: the moment it is nil the session is free, however
// long its loop lives on waiting for the next item. Clearing it and
// releasing the waiter are one step on purpose — an HTTP response that ends
// in between hands the client a session it cannot use, and the client is
// told "a turn is already running" about the turn it just finished.
func (st *sessionState) markTurnDone(done *Turn) {
	st.mu.Lock()
	if st.turn == done {
		st.turn = nil
	}
	st.mu.Unlock()
	if done != nil {
		done.markReleased()
	}
}

func (st *sessionState) markError(err error) {
	if err == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.lastError = err
}

func (st *sessionState) currentTurn() *Turn {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.turn
}

func (st *sessionState) currentLoop() *adk.TurnLoop[loopItem, *schema.Message] {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.loop
}

func (st *sessionState) setLoop(l *adk.TurnLoop[loopItem, *schema.Message]) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.loop = l
}

func (st *sessionState) clearTurn() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.turn = nil
	st.startedAt = time.Time{}
}

// startLoop runs a freshly built loop and drops the session state when it
// exits, so a later Submit builds a new loop rather than reusing a dead one.
//
// ★ The loop was already installed by the claim (see claimTurn); setting
//
//	it again here would be a redundant write under the lock.
func (rt *Runtime) startLoop(ctx context.Context, sessionID string, turn *Turn,
	loop *adk.TurnLoop[loopItem, *schema.Message], cancel context.CancelFunc) {
	go func() {
		loop.Run(ctx)
		exit := loop.Wait()
		rt.loopExited(sessionID, turn, loop, exit, cancel)
	}()
}

// loopExited clears runtime state when a loop finishes.
//
// ★ Only the loop that is CURRENTLY installed may clear the state. An
//
//	older loop finishing late must not wipe a newer turn's bookkeeping —
//	that would make a live session look idle, and the admission gate
//	would then let a second turn in.
func (rt *Runtime) loopExited(sessionID string, turn *Turn, loop *adk.TurnLoop[loopItem, *schema.Message],
	exit *adk.TurnLoopExitState[loopItem, *schema.Message], cancel context.CancelFunc) {
	st := rt.stateFor(sessionID)
	st.mu.Lock()
	current := st.loop
	stale := current != nil && current != loop
	if current == loop {
		st.loop = nil
		st.turn = nil
	}
	st.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if stale {
		// A newer loop took over while this one was winding down. Its
		// state is not ours to clear.
		rt.closeOpenRow(turn, exit)
		// ★ The run is over even so: its subscribers must not wait for a
		//   stream that will never come.
		turn.publisher.Close()
		return
	}
	if exit != nil && exit.ExitReason != nil {
		rt.log.Info("[openheronrt] loop exited", "session", sessionID, "reason", exit.ExitReason)
	}
	// ★ The loop is gone: nobody holds the session any more.
	turn.lease.release()
	rt.writeHeldApproval(sessionID, turn)
	rt.closeApprovalRow(turn)
	rt.closeOpenRow(turn, exit)
	// ★ The publisher closes here for the run that ended by raising an
	//   interrupt: the approval is on disk now, so a client that answers
	//   it cannot be refused.
	turn.publisher.Close()
	rt.removeStateIfIdle(sessionID)
}

// closeApprovalRow writes the terminal row of a run that ended by raising an
// approval.
//
// ★ It runs after the approval is persisted. The row is the barrier, and the
//
//	barrier has to stand behind everything the user needs: the approval card
//	and the checkpoint that makes it answerable.
func (rt *Runtime) closeApprovalRow(turn *Turn) {
	if turn == nil || !turn.awaitingApproval {
		return
	}
	turn.awaitingApproval = false
	if turn.row == nil {
		return
	}
	turn.row.finish(storage.RunWaitingApproval, "", "")
}

// closeOpenRow closes a row that is still open.
//
// ★ It is the safety net: whatever happened, a run whose loop has exited
//
//	does not own the session any more. Leaving the row open would make the
//	scan report an owner that nobody is, and the session would read as busy
//	for ever.
//
// ★ The loop's exit reason decides how the run ended. An error is a
//
//	failure; a clean exit that the run never accounted for is an
//	interruption — nobody finished it and nobody archived it.
func (rt *Runtime) closeOpenRow(turn *Turn, exit *adk.TurnLoopExitState[loopItem, *schema.Message]) {
	if turn == nil || turn.row == nil {
		return
	}
	if turn.row.isClosed() {
		return
	}
	if exit != nil && exit.ExitReason != nil {
		state, code := failedState(exit.ExitReason)
		turn.row.finish(state, code, exit.ExitReason.Error())
		return
	}
	turn.row.finish(storage.RunInterrupted, "interrupted",
		"the run's loop exited before the run finished")
}

// writeHeldApproval persists the approval the turn raised.
//
// ★ It runs when the loop exits, not when the turn's callback returns.
//
//	The framework writes the checkpoint at the loop's exit — the callback
//	returns first — and an approval is only answerable if the checkpoint
//	it resumes from exists. Writing it earlier would leave a window where
//	the user sees the card but answering it fails with no_checkpoint.
func (rt *Runtime) writeHeldApproval(sessionID string, turn *Turn) {
	interrupts := turn.takePending()
	if len(interrupts) == 0 {
		return
	}
	sess, err := rt.cfg.SessionManager.Get(context.Background(), sessionID)
	if err != nil {
		rt.logf("session=%s failed to persist the pending approval: %v", sessionID, err)
		return
	}
	if err := sess.SetPendingApproval(&storage.PendingApproval{Interrupts: interrupts}); err != nil {
		// Fail-closed: without a persisted approval the user could never
		// approve, so surface it loudly rather than silently degrading.
		rt.logf("session=%s FAILED to persist pending approval %v: %v",
			sessionID, interrupts[0].InterruptID, err)
	}
}

func (rt *Runtime) stateFor(sessionID string) *sessionState {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	st, ok := rt.states[sessionID]
	if !ok {
		st = &sessionState{}
		rt.states[sessionID] = st
	}
	return st
}

func (rt *Runtime) removeStateIfIdle(sessionID string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	st, ok := rt.states[sessionID]
	if !ok {
		return
	}
	// st fields are guarded by st.mu, never by rt.mu alone.
	st.mu.Lock()
	idle := st.loop == nil && st.turn == nil
	st.mu.Unlock()
	if !idle {
		return
	}
	delete(rt.states, sessionID)
}

// newTurnHandle creates what a turn needs: the readiness signal, the archive
// signal and the event bus.
//
// ★ There is no "result return path" any more. The consumer used to report
//
//	back what it had seen; the run derives it now, so there is nothing to
//	report.
func newTurnHandle(sessionID string, kind contract.TurnKind) *Turn {
	return &Turn{
		sessionID: sessionID,
		Kind:      kind,
		ready:     make(chan struct{}),
		archived:  make(chan struct{}),
		released:  make(chan struct{}),
		abort:     make(chan struct{}),
		publisher: newPublisher(),
	}
}
