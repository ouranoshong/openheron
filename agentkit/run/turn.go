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
// Lifecycle: Submit/Resume -> Wait -> consume Events() -> Complete(Outcome).
// The handle is what removes the baseline's mutual hostage situation, where
// OnAgentEvents and the HTTP handler each had to hold a reference to the other
// and the result could be delivered to the wrong request.
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
	done    chan outcome
	failErr error

	completeOnce sync.Once
	archiveOnce  sync.Once
	readyOnce    sync.Once
	cancel       context.CancelFunc

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

type outcome struct {
	out Outcome
	err error
}

// Outcome is what a consumer reports back after draining the event stream.
// openheronrt owns the archiving so the consumer never touches the session.
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
// nil, and the caller then drains Events() until it closes.
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

// Events returns the event stream for this turn. Valid after Wait succeeds.
func (t *Turn) SessionID() string { return t.sessionID }

func (t *Turn) Events() *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]] {
	return t.events
}

// History is not meaningful for a resume turn: the agent state comes from the
// checkpoint, not from a replayed history.
func (t *Turn) History() []*schema.Message { return nil }

// Complete hands the result back to openheronrt for archiving. It is safe to call
// once; later calls are ignored.
//
// ★ It returns only after the archive has landed. The archive is what
//
//	puts the turn's messages into the transcript, and the next turn's
//	input is built from that transcript — so a caller that sends the next
//	message as soon as this returns must find the history complete.
//
// ★ That is also what makes the admission gate honest. The gate asks
//
//	whether the session is busy; the session becomes free when the
//	archive lands. If the HTTP response went out before that, the client
//	would be told "your turn is done" while the server still considered
//	the session busy — and its very next message would be refused.
//
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

func (t *Turn) Complete(out Outcome) {
	t.completeOnce.Do(func() { t.done <- outcome{out: out} })
	// ★ The wait is bounded. The archive is on the other side of this
	//   channel, and a bug there must not hang the HTTP request: the
	//   client would wait forever for a response that is never coming.
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

// fail releases a turn that can never run: it unblocks both Wait and the
// OnAgentEvents handshake without waiting for a Complete that will not come.
func (t *Turn) fail(err error) {
	t.failErr = err
	t.completeOnce.Do(func() { t.done <- outcome{err: err} })
	t.signalReady()
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
