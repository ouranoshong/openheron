package run

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/contract"
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
func (st *sessionState) markTurnDone(done *Turn) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.turn == done {
		st.turn = nil
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
func (rt *Runtime) startLoop(ctx context.Context, sessionID string, loop *adk.TurnLoop[loopItem, *schema.Message], cancel context.CancelFunc) {
	st := rt.stateFor(sessionID)
	st.setLoop(loop)

	go func() {
		loop.Run(ctx)
		exit := loop.Wait()
		rt.loopExited(sessionID, exit, cancel)
	}()
}

// loopExited clears runtime state when a loop finishes.
func (rt *Runtime) loopExited(sessionID string, exit *adk.TurnLoopExitState[loopItem, *schema.Message], cancel context.CancelFunc) {
	st := rt.stateFor(sessionID)
	st.mu.Lock()
	current := st.loop
	st.loop = nil
	st.turn = nil
	st.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if current != nil && exit != nil && exit.ExitReason != nil {
		obs.Logger().Info("[openheronrt] loop exited", "session", sessionID, "reason", exit.ExitReason)
	}
	rt.removeStateIfIdle(sessionID)
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

// newTurnHandle creates the three channels a turn needs: readiness, the event
// hand-off and the result return path.
func newTurnHandle(sessionID string, kind contract.TurnKind) *Turn {
	return &Turn{
		sessionID: sessionID,
		Kind:      kind,
		ready:     make(chan struct{}),
		done:      make(chan outcome, 1),
	}
}
