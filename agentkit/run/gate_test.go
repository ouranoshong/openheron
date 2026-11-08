package run

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
)

// blockingAgent holds every turn open until the framework cancels it.
func blockingAgent(started chan struct{}) func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
	return func(call int, kind contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			started <- struct{}{}
			<-ctx.Done()
			emit(exitEvent())
		}}
	}
}

// slowBuild is a builder that takes its time.
//
// ★ The turn becomes ready when the agent **starts**, not when it emits
//
//	(see `makeOnAgentEvents`), so an agent that stays quiet is still a ready
//	turn. The only way to reach the start timeout is a build that has not
//	come back yet — and that is what this is.
//
// ★ It does come back, eventually: the loop then runs and exits, and the
//
//	session is free again. A build that hung for ever would leave the
//	loop alive, and the session busy, which is a different story.
func slowBuild(delay time.Duration) func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
	return func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		time.Sleep(delay)
		return &fakeAgent{}
	}
}

// A second Submit on a session with a live turn must be refused, and the
// refusal must change nothing: not the running turn, not the transcript,
// not the checkpoint, not the pending approval.
func TestSubmitRefusalIsInert(t *testing.T) {
	started := make(chan struct{}, 4)
	b := &fakeBuilder{agent: blockingAgent(started)}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	<-started
	firstDone := drainBounded(t, turn)

	// An approval raised while the turn runs: it must survive the refusal.
	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{InterruptID: "int-1"}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := rt.Submit(ctx, sess.ID(), "", "second"); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("want ErrSessionBusy, got %v", err)
	}

	// Nothing was consumed.
	if p, _ := sess.PendingApproval(); p == nil || len(p.Interrupts) != 1 {
		t.Fatalf("the refusal consumed the pending approval: %+v", p)
	}

	// Nothing was persisted.
	for _, m := range sess.Messages(false) {
		if q := message.UserText(m); q == "second" {
			t.Fatalf("the refused query was persisted anyway: %q", q)
		}
	}

	// The running turn was not released.
	select {
	case <-firstDone:
		t.Fatal("the refused submit released the running turn")
	case <-time.After(300 * time.Millisecond):
	}
}

// A pending approval blocks a new query: the context belongs to the
// pending decision.
func TestSubmitRefusesWhileAnApprovalIsPending(t *testing.T) {
	b := &fakeBuilder{agent: blockingAgent(make(chan struct{}, 4))}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{InterruptID: "int-1"}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := rt.Submit(ctx, sess.ID(), "", "hello"); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("want ErrSessionBusy while an approval is pending, got %v", err)
	}
}

// A resume must still work while an approval is pending — that is what it
// is FOR.
func TestResumeIsAllowedWhileAnApprovalIsPending(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("resumed"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{InterruptID: "int-42"}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := rt.Resume(ctx, sess.ID(),
		"", []ApprovalDecision{{InterruptID: "int-42", Approved: true, Reason: "ok"}}); err != nil {
		t.Fatalf("Resume must be allowed while an approval is pending: %v", err)
	}
}

// A refused resume must leave the pending approval alone. TakePendingApproval
// consumes it and writes the disk, so a refusal after that point would eat
// the user's decision.
func TestRefusedResumeKeepsTheApproval(t *testing.T) {
	started := make(chan struct{}, 4)
	b := &fakeBuilder{agent: blockingAgent(started)}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	// A live QUERY turn holds the session.
	turn, err := rt.Submit(ctx, sess.ID(), "", "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	<-started
	_ = drainBounded(t, turn)

	// An approval is pending, and the resume is refused.
	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{InterruptID: "int-7"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Resume(ctx, sess.ID(),
		"", []ApprovalDecision{{InterruptID: "int-7", Approved: true}}); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("want ErrSessionBusy, got %v", err)
	}

	// ★ The approval must still be there: the user's decision was not
	// consumed by the refusal.
	p, _ := sess.PendingApproval()
	if p == nil || len(p.Interrupts) != 1 || p.Interrupts[0].InterruptID != "int-7" {
		t.Fatalf("the refusal consumed the pending approval: %+v", p)
	}
}

// A turn that has already produced its answer must not keep the session
// busy: the pointer lags the real state, and refusing on it would be a
// false positive.
func TestSubmitIsNotRefusedWhileTheTurnIsArchiving(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	drain(t, turn)

	// Once the outcome is archived the session is free again.
	waitFor(t, func() bool {
		st := rt.stateFor(sess.ID())
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.turn == nil
	})

	if _, err := rt.Submit(ctx, sess.ID(), "", "second"); err != nil {
		t.Fatalf("the session must be free once the turn is archived: %v", err)
	}
}

// A turn that never becomes streamable must not hold the session forever.
// The start timeout fails the turn, which releases the slot.
func TestAStartTimeoutReleasesTheSession(t *testing.T) {
	// ★ The agent is built but never produces an event, so the turn never
	//   becomes ready — a **genuine** start timeout.
	//
	//   It used to be a builder that failed, which was a different thing
	//   wearing the same coat: that path now reports its own reason (see
	//   `TestBuilderFailureSurfacesAsLoopError`), so a test that wanted the
	//   timeout had to stop borrowing it.
	b := &fakeBuilder{agent: slowBuild(300 * time.Millisecond)}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "never starts")
	if err != nil {
		t.Fatal(err)
	}
	// A short timeout: the turn is not going to become ready.
	if err := turn.Wait(100 * time.Millisecond); !errors.Is(err, ErrStartTimeout) {
		t.Fatalf("Wait = %v, want ErrStartTimeout", err)
	}

	// The session must not stay busy: a turn that can neither run nor
	// finish must not hold the slot.
	waitFor(t, func() bool {
		st := rt.stateFor(sess.ID())
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.turn == nil
	})

	if _, err := rt.Submit(ctx, sess.ID(), "", "the next one"); err != nil {
		t.Fatalf("the session must be free after a start timeout: %v", err)
	}
}

// Aborting a turn that was waiting for approval clears that approval: the
// decision is void, and leaving it behind would keep the session locked —
// "stop, then take over" would not take over.
func TestAbortClearsTheVoidApproval(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("about to write"))
			emit(interruptedEvent("int-1", "write_file needs approval"))
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "write the config")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	drain(t, turn)
	waitFor(t, func() bool {
		p, _ := sess.PendingApproval()
		return p != nil && len(p.Interrupts) == 1
	})

	if err := rt.Abort(ctx, sess.ID()); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	if p, _ := sess.PendingApproval(); p != nil && len(p.Interrupts) > 0 {
		t.Fatalf("the void approval survived the abort: %+v", p.Interrupts)
	}

	// And the session is usable again.
	if _, err := rt.Submit(ctx, sess.ID(), "", "something else"); err != nil {
		t.Fatalf("the session must be usable after the abort: %v", err)
	}
}

// ★ A response that ends before the session is released hands the client a
// session it cannot use: it reads the end of the stream, sends its next
// message, and is told "a turn is already running" about the turn it just
// finished — a false 409, which is the one thing a busy signal must never be.
//
// ★ `archived` is not that moment. The archive lands inside the loop's own
// callback; the pointer the gate reads is cleared a step later, after the
// run's row is written. `released` is the channel that says "the pointer is
// gone", and it has to be closed by the same code that clears it.
func TestTheSessionIsReleasedBeforeTheResponseCanEnd(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)
	turn.WaitArchived()

	// This is what the HTTP handler does before it flushes.
	released := make(chan struct{})
	go func() { turn.WaitReleased(); close(released) }()
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("the turn was archived but the session was never released; " +
			"the client's next message would be refused as busy")
	}

	// And the thing the client actually does next.
	if _, err := rt.Submit(ctx, sess.ID(), "", "second"); err != nil {
		t.Fatalf("the session must accept the next turn once the response may end: %v", err)
	}
}

// A turn that can never run must release the waiter too: the start-timeout
// path closes the archive channel for exactly this reason, and `released`
// must not be the one that is forgotten.
func TestAFailedTurnReleasesItsWaiter(t *testing.T) {
	// ★ A turn that never becomes ready — the genuine timeout path, for the
	//   reason spelled out in `TestAStartTimeoutReleasesTheSession`.
	b := &fakeBuilder{agent: slowBuild(300 * time.Millisecond)}
	rt, sm := newTestRuntime(t, b)
	sess := mustSession(t, sm)

	turn, err := rt.Submit(context.Background(), sess.ID(), "", "never starts")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(100 * time.Millisecond); !errors.Is(err, ErrStartTimeout) {
		t.Fatalf("Wait = %v, want ErrStartTimeout", err)
	}

	released := make(chan struct{})
	go func() { turn.WaitReleased(); close(released) }()
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("a failed turn kept its waiter blocked")
	}
}
