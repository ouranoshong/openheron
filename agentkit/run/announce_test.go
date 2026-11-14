package run

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
)

// announceRuntime wires a runtime whose first round asks for approval and
// whose second round answers it.
//
// ★ The checkpoint store is not optional: a resume is refused without one,
//
//	and the refusal would swallow the announcement under test.
func announceRuntime(t *testing.T) (*Runtime, *fakeSession) {
	t.Helper()
	b := &fakeBuilder{agent: func(call int, kind contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			if call == 1 {
				emit(textEvent("about to write"))
				emit(interruptedEvent("int-1", "write_file needs approval"))
				emit(exitEvent())
				return
			}
			emit(textEvent("written"))
			emit(exitEvent())
		}}
	}}
	sm := newFakeSessionManager()
	sm.ckpt = newFakeCheckpoints()
	rt := New(Config{
		AgentBuilder:   b,
		SessionManager: sm,
		Input:          InputConfig{},
		StartTimeout:   5 * time.Second,
	})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	return rt, mustSession(t, sm)
}

// awaitEvent reads one event from the session's channel, or fails.
func awaitEvent(t *testing.T, sub *subscription) contract.RunEvent {
	t.Helper()
	select {
	case ev, ok := <-sub.Events():
		if !ok {
			t.Fatal("the session's channel closed before the fact arrived")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("the session was not told")
		return contract.RunEvent{}
	}
}

// expectNothing reads the channel and fails if anything arrives.
//
// ★ The window is short: a fact that does not come must not come *at all*,
//
//	and a test that only waited for a moment would pass a channel that
//	replays.
func expectNothing(t *testing.T, sub *subscription) {
	t.Helper()
	select {
	case ev, ok := <-sub.Events():
		if ok {
			t.Fatalf("the session must not have been told, got %+v", ev)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

// A round that begins is a fact about the **session**, not about the run.
//
// ★ The surface that did not ask for the round has no stream of its own —
//
//	and it is exactly that surface that has to learn that a round began.
func TestTheSessionHearsThatARoundBegan(t *testing.T) {
	rt, sess := announceRuntime(t)
	ctx := context.Background()

	sub := rt.Announce(sess.ID())
	defer sub.Close()

	turn, err := rt.Submit(ctx, sess.ID(), "", "write the config", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)

	ev := awaitEvent(t, sub)
	if ev.Kind != contract.RunEventRunStarted {
		t.Fatalf("want the session to hear that a round began, got %v", ev.Kind)
	}
	if ev.RunID != turn.RunID {
		t.Fatalf("want the round's id %q, got %q", turn.RunID, ev.RunID)
	}
	if ev.TurnKind != contract.TurnQuery {
		t.Fatalf("want a query round, got %s", ev.TurnKind)
	}
}

// The approval is answered: every surface that holds the card must hear it.
//
// ★ This is the fact the second surface needs. Without it the card stays on
//
//	screen and every answer it offers is refused by the server.
func TestTheSessionHearsThatTheApprovalWasAnswered(t *testing.T) {
	rt, sess := announceRuntime(t)
	ctx := context.Background()

	sub := rt.Announce(sess.ID())
	defer sub.Close()

	turn, err := rt.Submit(ctx, sess.ID(), "", "write the config", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)
	waitFor(t, func() bool {
		p, _ := sess.PendingApproval()
		return p != nil && len(p.Interrupts) == 1
	})

	resume, err := rt.Resume(ctx, sess.ID(), "", []ApprovalDecision{
		{InterruptID: "int-1", Approved: true, Reason: "it is my own config"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// ★ The first fact is the round that began, the second is the answer.
	//	★ The order matters: the approval is answered by a **new** run, and
	//	the surface that follows the round must know the card is stale
	//	before it attaches.
	started := awaitEvent(t, sub)
	if started.Kind != contract.RunEventRunStarted {
		t.Fatalf("want the round that began, got %v", started.Kind)
	}
	if started.TurnKind != contract.TurnQuery {
		t.Fatalf("want a query round, got %s", started.TurnKind)
	}

	resolved := awaitEvent(t, sub)
	if resolved.Kind != contract.RunEventApprovalResolved {
		t.Fatalf("want the approval to be resolved, got %v", resolved.Kind)
	}
	if resolved.InterruptID != "int-1" {
		t.Fatalf("want the approval of %q, got %q", "int-1", resolved.InterruptID)
	}
	if !resolved.Approved {
		t.Fatal("the approval was granted, so the fact must say so")
	}
	if resolved.Reason != "it is my own config" {
		t.Fatalf("want the reason to travel with the fact, got %q", resolved.Reason)
	}
	if resolved.RunID != resume.RunID {
		t.Fatalf("want the answering run %q, got %q", resume.RunID, resolved.RunID)
	}
}

// A refused approval is a fact too.
//
// ★ The surface that was refused has to learn it as well: otherwise it
//
//	keeps a card that the server will never answer.
func TestTheSessionHearsThatTheApprovalWasRefused(t *testing.T) {
	rt, sess := announceRuntime(t)
	ctx := context.Background()

	sub := rt.Announce(sess.ID())
	defer sub.Close()

	turn, err := rt.Submit(ctx, sess.ID(), "", "write the config", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)
	waitFor(t, func() bool {
		p, _ := sess.PendingApproval()
		return p != nil && len(p.Interrupts) == 1
	})

	if _, err := rt.Resume(ctx, sess.ID(), "", []ApprovalDecision{
		{InterruptID: "int-1", Approved: false, Reason: "not now"},
	}); err != nil {
		t.Fatal(err)
	}

	_ = awaitEvent(t, sub) // the round that began
	resolved := awaitEvent(t, sub)
	if resolved.Kind != contract.RunEventApprovalResolved {
		t.Fatalf("want the approval to be resolved, got %v", resolved.Kind)
	}
	if resolved.Approved {
		t.Fatal("the approval was refused, so the fact must say so")
	}
	if resolved.Reason != "not now" {
		t.Fatalf("want the reason to travel with the fact, got %q", resolved.Reason)
	}
}

// The session's channel is **not** the run's channel.
//
// ★ The run that raised the approval is over by the time anybody can
//
//	answer it. A fact that rides that run's bus would be published into a
//	stream nobody reads any more — which is exactly the bug this channel
//	exists to fix.
func TestTheApprovalFactOutlivesTheRunThatRaisedIt(t *testing.T) {
	rt, sess := announceRuntime(t)
	ctx := context.Background()

	sub := rt.Announce(sess.ID())
	defer sub.Close()

	turn, err := rt.Submit(ctx, sess.ID(), "", "write the config", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)
	_ = awaitEvent(t, sub) // the round that began

	// ★ The run's own stream is finished: the round ended with the
	//	approval. The session's channel is not.
	waitFor(t, func() bool {
		runs := ledgerOf(t, sess)
		return len(runs) == 1 && runs[0].State == storage.RunWaitingApproval
	})

	if _, err := rt.Resume(ctx, sess.ID(), "", []ApprovalDecision{
		{InterruptID: "int-1", Approved: true},
	}); err != nil {
		t.Fatal(err)
	}

	resolved := awaitEvent(t, sub)
	if resolved.Kind != contract.RunEventApprovalResolved {
		t.Fatalf("want the approval to be resolved, got %v", resolved.Kind)
	}
}

// The session's channel has no window.
//
// ★ Its facts are live notifications, and "what happened before you
//
//	arrived" is the scan's job. ★ A channel that replayed would send a
//	late surface after a round that ended hours ago — and it would attach
//	to a run that is long gone.
func TestTheSessionChannelHasNoReplay(t *testing.T) {
	rt, sess := announceRuntime(t)
	ctx := context.Background()

	sub := rt.Announce(sess.ID())
	turn, err := rt.Submit(ctx, sess.ID(), "", "write the config", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)
	_ = awaitEvent(t, sub)
	sub.Close()

	// ★ A surface that arrives late gets nothing from the past.
	late := rt.Announce(sess.ID())
	defer late.Close()
	expectNothing(t, late)
	_ = turn
}

// The channel is dropped with its last reader.
//
// ★ A bus kept for a session nobody watches is a leak with one entry per
//
//	session the process ever saw.
func TestTheSessionChannelIsDroppedWithItsLastReader(t *testing.T) {
	rt, sess := announceRuntime(t)

	sub := rt.Announce(sess.ID())
	rt.announcer.mu.Lock()
	held := len(rt.announcer.buses)
	rt.announcer.mu.Unlock()
	if held != 1 {
		t.Fatalf("want one bus while a reader holds it, got %d", held)
	}

	sub.Close()
	rt.ReleaseAnnounce(sess.ID())

	rt.announcer.mu.Lock()
	left := len(rt.announcer.buses)
	rt.announcer.mu.Unlock()
	if left != 0 {
		t.Fatalf("want the bus to be dropped with its last reader, got %d", left)
	}
}

// A session nobody watches keeps no bus at all.
//
// ★ The bus is created by a **subscriber**: a fact nobody listens for has
//
//	no one to deliver it to.
func TestAFactForNobodyIsNotKept(t *testing.T) {
	rt, sess := announceRuntime(t)

	rt.announceRunStarted(sess.ID(), "run-1", contract.TurnQuery)

	rt.announcer.mu.Lock()
	left := len(rt.announcer.buses)
	rt.announcer.mu.Unlock()
	if left != 0 {
		t.Fatalf("want no bus for a session nobody watches, got %d", left)
	}
}
