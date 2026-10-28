package run

import (
	"context"
	"openheron/contract"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// Turn is the handle a caller drives for one round.
//
// Lifecycle: Submit/Resume -> Wait -> consume Events() -> Complete(Outcome).
//
// The handle is what removes the baseline's mutual hostage situation, where
// OnAgentEvents and the HTTP handler each had to hold a reference to the other
// and the result could be delivered to the wrong request.
type Turn struct {
	sessionID string
	Kind      contract.TurnKind
	// RunID is the protocol-level run identifier, minted by the transport so
	// the client can correlate its run with our events.
	RunID string

	events *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]]
	ready  chan struct{}
	done   chan outcome
	// failErr 是「这轮起跑前就判死了」的原因（见 [Turn.fail]）。
	//
	// ★ 它必须在 signalReady **之前**写：Wait 观察到 ready 时就能读到它，
	//   而 ready 的关闭与接收之间有 happens-before ✓。
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
	//
	// ★ 每个都带自己的 id，且重新中断时 id 会变（框架每次都发新的 uuid）——
	//
	//	所以它们必须**一次答完**，不能一个一个来。见 [storage.PendingApproval]。
	//	The consumer fills them while the interrupt event is still in hand.
	Interrupts []contract.PendingInterrupt
	// Usage is the token accounting for this turn, aggregated from the
	// assistant messages' ResponseMeta. Zero when the endpoint reports none.
	Usage contract.Usage
	Err   error
}

// Wait blocks until the turn is ready to stream, is superseded by a newer turn,
// or exceeds the start timeout.
//
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
// SessionID 是这一轮所属的会话。
//
// ★ 它是个**方法**而不是字段：渲染层拿到的是 [contract.TurnView]，
//
//	而那个接口是只读的 —— 活对象不该给渲染层可改的字段。
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
//
// ★ 它把原因记在 turn 上：**Wait 要能报出「为什么没起来」** ——
//
//	判死的轮次**没有事件流**（事件流是 makeOnAgentEvents 里才挂上的），
//	调用方若把它当成「起来了」，读 Events() 会拿到 nil 迭代器 ——
//	于是接口在 nil 上崩，而不是给客户端一句「这一轮没起来」。
func (t *Turn) fail(err error) {
	t.failErr = err
	t.completeOnce.Do(func() { t.done <- outcome{err: err} })
	t.signalReady()
}

func (t *Turn) signalReady() {
	t.readyOnce.Do(func() { close(t.ready) })
}
