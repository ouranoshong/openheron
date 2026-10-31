package run

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
)

// Turn is the handle a caller drives for one round.
//
// ★ The lifecycle changed when the run took over its own consumption:
//
//	Submit/Resume -> Wait -> Subscribe -> render. The caller no longer
//	drains the agent's events and reports back — the run does that, and
//	the caller watches a subscription.
//
// ★ The handle is what removes the baseline's mutual hostage situation,
//
//	where OnAgentEvents and the HTTP handler each had to hold a reference
//	to the other and the result could be delivered to the wrong request.
type Turn struct {
	sessionID string
	Kind      contract.TurnKind
	// RunID is the protocol-level run identifier: the id the client sees
	// in every SSE frame, the key the ledger's rows are filed under, and
	// the token a reconnecting client sends back to re-attach.
	//
	// ★ It is minted once, when the turn is claimed, and never changed.
	//	Before this it was declared and never assigned — which is why the
	//	server had no run identity of its own.
	RunID string

	events  *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]]
	ready   chan struct{}
	failErr error

	archiveOnce sync.Once
	readyOnce   sync.Once
	abortOnce   sync.Once
	cancel      context.CancelFunc

	// abort ends the derivation when the turn cannot finish.
	//
	// ★ It exists because the run consumes its own events now: an abort
	//   has to be able to stop the run without waiting for an agent that
	//   may ignore the cancellation.
	abort chan struct{}

	// publisher is the run's event bus. The run publishes to it; the
	// connections subscribe.
	publisher *publisher

	// lease is the run's claim on its session. Nil when no arbiter is
	// wired — the runtime then behaves as it did before the lease.
	lease *runLease

	// row is the run's durable record. Nil when the session has no ledger.
	row *runRow

	// awaitingApproval says the run ended by raising an interrupt. The
	// terminal row waits for the approval to be persisted — the loop's
	// exit writes it, and it must be on disk before the run may call
	// itself over.
	awaitingApproval bool

	// pending is the approval the turn raised, kept until the checkpoint
	// that would resume it exists.
	//
	// ★ An approval is only answerable if the checkpoint it resumes from
	//   is on disk, and the framework writes that checkpoint when the loop
	//   stops — AFTER this turn's callback returns. Writing the approval
	//   here would leave a window where the user sees the card but
	//   answering it fails with no_checkpoint.
	pending []contract.PendingInterrupt

	// archived is closed once the turn's messages are on disk.
	//
	// ★ The HTTP response goes out after the answer but potentially
	//   before the archive lands. The client that sends its next message
	//   right away would then meet a session the server still considers
	//   busy — and be refused, wrongly. Complete waits on this channel so
	//   the two cannot drift apart.
	archived chan struct{}
}

// Outcome is what the run produced.
//
// ★ It used to be what a consumer reported back after draining the event
//
//	stream. The run derives it now — the consumer drains nothing and
//	reports nothing, which is the whole point of the split.
type Outcome struct {
	// Intermediates are the tool-call and tool-result messages to archive.
	Intermediates []*schema.Message
	// Interrupts is non-empty when the agent paused for human approval. It can
	// hold more than one: eino can raise several independent interrupts in a
	// single turn (a model asking for four tool calls that all need approval).
	//	The consumer fills them while the interrupt event is still in hand.
	Interrupts []contract.PendingInterrupt
	// Usage is the token accounting for this turn, aggregated from the
	// assistant messages' ResponseMeta. Zero when the endpoint reports none.
	Usage contract.Usage
	// Compaction is the message that replaced the conversation so far, when
	// the context was compacted during this turn. It is archived before the
	// turn's own messages: it summarizes everything that came before them.
	Compaction *schema.Message
	Err        error
}

// Wait blocks until the turn is ready to stream, is superseded by a newer turn,
// or exceeds the start timeout.
// Readiness is not execution: the agent may still be running when Wait returns
// nil, and the caller then watches the subscription until it closes.
func (t *Turn) Wait(timeout time.Duration) error {
	select {
	case <-t.ready:
		return t.failErr
	default:
	}

	if timeout <= 0 {
		<-t.ready
		return t.failErr
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-t.ready:
		return t.failErr
	case <-timer.C:
		// ★ The turn never became streamable. It will never archive
		//   anything either, so fail it here: that releases the session's
		//   slot and the archived channel. Without this the session would
		//   stay busy forever — a turn that can neither run nor finish.
		t.fail(ErrStartTimeout)
		return ErrStartTimeout
	}
}

// SessionID is the session the turn belongs to.
func (t *Turn) SessionID() string { return t.sessionID }

// Subscribe returns a view of the run's events.
//
// ★ The subscription replays the run's window first, so a subscriber that
//
//	joins late still sees the turn's tail. It is valid any time: before
//	the run starts it is empty, and it closes when the run ends.
func (t *Turn) Subscribe() *subscription {
	return t.publisher.Subscribe()
}

// Events returns the agent's event stream.
//
// ★ It is the run's, not the caller's: the run consumes it and publishes
//
//	what it derived. It stays exported for the run's own plumbing — the
//	caller must not drain it.
func (t *Turn) Events() *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]] {
	return t.events
}

// History is not meaningful for a resume turn: the agent state comes from the
// checkpoint, not from a replayed history.
func (t *Turn) History() []*schema.Message { return nil }

// holdPending keeps the approval the turn raised until the checkpoint lands.
func (t *Turn) holdPending(interrupts []contract.PendingInterrupt) {
	t.pending = interrupts
}

// takePending returns the held approval and clears it.
func (t *Turn) takePending() []contract.PendingInterrupt {
	out := t.pending
	t.pending = nil
	return out
}

// markArchived releases whoever waits in Complete: the turn's messages are
// on disk.
func (t *Turn) markArchived() {
	t.archiveOnce.Do(func() { close(t.archived) })
}

// WaitArchived blocks until the turn's messages are on disk.
//
// ★ The wait is bounded. The archive is the run's own work, and a bug
//
//	there must not hang the HTTP request: the client would wait forever
//	for a response that is never coming.
func (t *Turn) WaitArchived() {
	select {
	case <-t.archived:
	case <-time.After(archiveWaitTimeout):
	}
}

// archiveWaitTimeout bounds how long Complete waits for the archive.
//
// ★ It is a backstop, not a budget: the archive is a few file appends.
//
//	A turn that hits it has already lost its messages to the transcript,
//	and the client must not hang on that.
const archiveWaitTimeout = 30 * time.Second

// fail releases a turn that can never run: it unblocks Wait without waiting
// for a run that will never come.
func (t *Turn) fail(err error) {
	t.failErr = err
	t.signalReady()
	// ★ The derivation may be waiting for an agent that never stops.
	t.abortOnce.Do(func() { close(t.abort) })
	// ★ A dead turn archives nothing, so nobody is going to close the
	//   archived channel from the archive path. Release it here or a
	//   caller waiting in Complete waits forever.
	t.markArchived()
	// ★ The turn is over, and the loop may never reach the archive: the
	//   input generator can fail before a single event is produced. The
	//   row has to be closed here, or the run stays open in the ledger and
	//   the session reads as busy for ever.
	t.closeRow(err)
}

// closeRow writes the terminal row of a turn that will not run.
func (t *Turn) closeRow(err error) {
	if t.row == nil {
		return
	}
	state, code := failedState(err)
	t.row.finish(state, code, err.Error())
}

// failedState maps a fatal error onto the run's terminal state.
//
// ★ The distinction is the user's: "aborted" means the user stopped it,
//
//	"failed" means it broke. A superseded turn is aborted as well —
//	nothing is broken, the run was simply replaced.
func failedState(err error) (storage.RunState, string) {
	switch {
	case errors.Is(err, context.Canceled):
		return storage.RunAborted, "aborted"
	case errors.Is(err, ErrSuperseded):
		return storage.RunAborted, "superseded"
	default:
		return storage.RunFailed, "failed"
	}
}

func (t *Turn) signalReady() {
	t.readyOnce.Do(func() { close(t.ready) })
}
