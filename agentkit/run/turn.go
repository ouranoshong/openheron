package run

import (
	"context"
	"github.com/ouranoshong/openheron/contract"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// Turn is the handle a caller drives for one round.
// Lifecycle: Submit/Resume -> Wait -> consume Events() -> Complete(Outcome).
// The handle is what removes the baseline's mutual hostage situation, where
// OnAgentEvents and the HTTP handler each had to hold a reference to the other
// and the result could be delivered to the wrong request.
type Turn struct {
	sessionID string
	Kind      contract.TurnKind
	// RunID is the protocol-level run identifier, minted by the transport so
	// the client can correlate its run with our events.
	RunID string

	events  *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]]
	ready   chan struct{}
	done    chan outcome
	failErr error

	completeOnce sync.Once
	readyOnce    sync.Once
	cancel       context.CancelFunc
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
func (t *Turn) Complete(out Outcome) {
	t.completeOnce.Do(func() { t.done <- outcome{out: out} })
}

// fail releases a turn that can never run: it unblocks both Wait and the
// OnAgentEvents handshake without waiting for a Complete that will not come.
func (t *Turn) fail(err error) {
	t.failErr = err
	t.completeOnce.Do(func() { t.done <- outcome{err: err} })
	t.signalReady()
}

func (t *Turn) signalReady() {
	t.readyOnce.Do(func() { close(t.ready) })
}
