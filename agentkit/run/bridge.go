package run

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
)

// loopItem is the TurnLoop's unit of work. One user query or one approval
// decision travels as a single item.
type loopItem struct {
	Query string

	// Attachments are the files that came with this query.
	//
	// ★ They travel with the query because the attachment list belongs to
	//   the **message**: the turn persists one user message, and that
	//   message carries what it came with. A session-level list could only
	//   answer "what does this session have" — not "that image you just
	//   sent".
	Attachments []message.Attachment

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
//
// ★ ★ WHEN it runs is the round's business, not the loop's. ★ A round that
// raised an approval is not over until that approval is on disk and its
// ledger row has closed (bridge.go) — releasing the turn earlier would let an
// attach be refused with "the run is no longer live" while the approval the
// client needs is nowhere to be found.
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

// releaseRound gives the session's slot back at the end of a round.
//
// ★ The loop pointer and the turn pointer go **together**. ★ The admission
//
//	gate reads the pair, and a half-released session reads "free" while a
//	round is still standing — so a new query could take the session over
//	in the middle of the round's own bookkeeping.
//
// ★ They go **behind the barrier**: the approval is on disk, the ledger's
//
//	row is terminal and the publisher is closed. ★ Until then an attach must
//	still find the run, and a new query must not be able to take the session
//	over — ★ the takeover branch does not write the approval, so a query
//	that squeezed in during the window would take the user's question with
//	it.
//
// ★ Only the matching handles are cleared: a turn superseded in the meantime
//
//	keeps its slot.
func (st *sessionState) releaseRound(done *Turn, loop *adk.TurnLoop[loopItem, *schema.Message]) {
	st.mu.Lock()
	if st.loop == loop {
		st.loop = nil
	}
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
//
// ★ ★ The turn is released LAST, after the approval is on disk and the
//
//	ledger's row is closed. ★ The server refuses an attach on the
//	runtime's turn, and the approval is written after that turn would
//	have been released: a refusal in between tells the client "the run is
//	no longer live" while the approval it is waiting for is nowhere on
//	disk — and the transcript, which is where the client would look for
//	it, cannot answer that yet.
//
// ★ ★ The LOOP pointer waits with it. ★ The admission gate reads the turn
//
//	and the loop as a pair, so a loop that is released early makes the
//	session look free while the round is still standing: a new query would
//	be admitted, it would take the session over, and ★ the takeover branch
//	does not write the approval — the user's question would be gone with
//	nothing saying so.
func (rt *Runtime) loopExited(sessionID string, turn *Turn, loop *adk.TurnLoop[loopItem, *schema.Message],
	exit *adk.TurnLoopExitState[loopItem, *schema.Message], cancel context.CancelFunc) {
	st := rt.stateFor(sessionID)
	st.mu.Lock()
	current := st.loop
	stale := current != nil && current != loop
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
		// ★ The slot is not ours to give back — a newer turn owns it — but
		//   the waiter is ours: the response for the run that was
		//   superseded still has to end.
		st.markTurnDone(turn)
		return
	}
	if exit != nil && exit.ExitReason != nil {
		rt.log.Info("[openheronrt] loop exited", "session", sessionID, "reason", exit.ExitReason)
	}
	// ★ The loop is gone: nobody holds the session any more.
	turn.lease.release()
	pending := rt.writeHeldApproval(sessionID, turn)
	rt.closeApprovalRow(turn)
	rt.closeOpenRow(turn, exit)
	// ★ The session hears that a card is waiting. ★ It runs **after** the
	//   barrier: the ledger's row is terminal now, so the fact and the
	//   ledger say the same thing — a surface that shows the card and then
	//   scans the ledger must not find one "running" and the other
	//   "waiting_approval".
	//
	// ★ Only approvals that are on disk are announced: an approval that
	//   failed to persist cannot be answered, and a card the server
	//   refuses every answer on is worse than no card.
	if len(pending) > 0 {
		rt.announceApprovalPending(sessionID, pending, turn.RunID)
	}
	// ★ The publisher closes here for the run that ended by raising an
	//   interrupt: the approval is on disk now, so a client that answers
	//   it cannot be refused.
	turn.publisher.Close()
	// ★ Now the round is over in every sense: the approval is on disk, the
	//   ledger's row is terminal, the publisher is closed. ★ Only now does
	//   the turn stop being live — and a client that attaches after this
	//   reads the transcript, where the approval is. ★ The loop pointer
	//   goes with it, so a query that arrives in between is refused
	//   instead of taking the session over.
	st.releaseRound(turn, loop)
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
	if row := turn.runRow(); row != nil {
		row.finish(storage.RunWaitingApproval, "", "")
	}
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
	if turn == nil {
		return
	}
	row := turn.runRow()
	if row == nil || row.isClosed() {
		return
	}
	if exit != nil && exit.ExitReason != nil {
		state, code := failedState(exit.ExitReason)
		row.finish(state, code, exit.ExitReason.Error())
		return
	}
	row.finish(storage.RunInterrupted, "interrupted",
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
//
// ★ It answers with the approvals that actually landed on disk. ★ An
//
//	approval that failed to persist is **not** in the answer: it cannot be
//	answered, so it must not be announced either — a card the server
//	refuses every answer on is worse than no card.
func (rt *Runtime) writeHeldApproval(sessionID string, turn *Turn) []contract.PendingInterrupt {
	interrupts := turn.takePending()
	if len(interrupts) == 0 {
		return nil
	}
	sess, err := rt.cfg.SessionManager.Get(context.Background(), sessionID)
	if err != nil {
		rt.logf("session=%s failed to persist the pending approval: %v", sessionID, err)
		return nil
	}
	if err := sess.SetPendingApproval(&storage.PendingApproval{Interrupts: interrupts}); err != nil {
		// Fail-closed: without a persisted approval the user could never
		// approve, so surface it loudly rather than silently degrading.
		rt.logf("session=%s FAILED to persist pending approval %v: %v",
			sessionID, interrupts[0].InterruptID, err)
		return nil
	}
	return interrupts
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
