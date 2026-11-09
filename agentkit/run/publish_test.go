package run

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
)

// A run with nobody watching still archives.
//
// ★ This is the whole point of the split: the run reads its own events, so
//
//	the connection is not in the archive's way. Before it, the transcript
//	was written by the renderer — and a renderer that never ran meant a
//	turn that never happened.
func TestARunWithNoSubscriberStillArchives(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello there", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	// No subscription at all: the run is on its own.
	drain(t, turn)

	waitFor(t, func() bool { return sess.Len() >= 2 })
	msgs := sess.Messages(false)
	if message.AssistantText(msgs[1]) != "working" {
		t.Fatalf("the answer must be on disk without a subscriber: %#v", msgs)
	}
}

// A subscriber that vanishes does not stop the run.
func TestASubscriberThatVanishesDoesNotStopTheRun(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello there", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	sub := turn.Subscribe()
	sub.Close() // the reader is gone before the run produces anything

	drain(t, turn)
	waitFor(t, func() bool { return sess.Len() >= 2 })
	if message.AssistantText(sess.Messages(false)[1]) != "working" {
		t.Fatal("the run must keep archiving after its subscriber left")
	}
}

// A subscriber that stops reading is dropped, and the run keeps archiving.
//
// ★ The publisher never waits for a slow reader: a run that waits for a
//
//	connection is the coupling this design removes.
func TestASubscriberThatStallsIsDroppedAndTheRunKeepsGoing(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			for i := 0; i < subscriberQueue+50; i++ {
				emit(textEvent("chunk"))
			}
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello there", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// A subscriber that never reads: its queue fills up.
	sub := turn.Subscribe()

	drain(t, turn)
	waitFor(t, func() bool { return sess.Len() >= 2 })

	// The run finished and archived, even though nobody was reading.
	if message.AssistantText(sess.Messages(false)[1]) == "" {
		t.Fatal("the run must archive even when its subscriber stalls")
	}
	// And the stalled subscriber was dropped rather than waited for.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-sub.Events():
			if !ok {
				return // the subscription was closed: it was dropped
			}
		case <-deadline:
			t.Fatal("the stalled subscriber must be dropped")
		}
	}
}

// The run's window is bounded: an unwatched turn pays a constant cost.
func TestTheEventWindowIsBounded(t *testing.T) {
	p := newPublisher()
	for i := 0; i < windowSize+100; i++ {
		p.Publish(contract.RunEvent{Kind: contract.RunEventTextDelta, Text: "x"})
	}
	p.mu.Lock()
	n := len(p.window)
	p.mu.Unlock()
	if n > windowSize {
		t.Fatalf("the window must stay bounded, got %d events", n)
	}
	if n == 0 {
		t.Fatal("the window must keep the recent events")
	}
}

// ★ A subscriber that arrives **after** the run closed still gets the
//
//	window. The connection subscribes after the turn signals ready, and the
//	run may finish in between — an approval raised in that window would
//	otherwise be dropped, and the client would see a run that finished
//	without ever having asked.
func TestALateSubscriberAfterTheRunClosedStillGetsTheWindow(t *testing.T) {
	p := newPublisher()
	p.Publish(contract.RunEvent{Kind: contract.RunEventInterrupt, Interrupts: []contract.Interrupt{{ID: "int-1"}}})
	p.Close()

	sub := p.Subscribe()
	ev, ok := <-sub.Events()
	if !ok {
		t.Fatal("the closed run must still deliver what it published")
	}
	if ev.Kind != contract.RunEventInterrupt || len(ev.Interrupts) != 1 || ev.Interrupts[0].ID != "int-1" {
		t.Fatalf("the approval must survive the close, got %+v", ev)
	}
	if _, ok := <-sub.Events(); ok {
		t.Fatal("a closed run must not deliver anything more")
	}
}

// A late subscriber sees the run's tail: the window is replayed into its
// queue, and the events that follow are appended.
func TestALateSubscriberGetsTheWindow(t *testing.T) {
	p := newPublisher()
	p.Publish(contract.RunEvent{Kind: contract.RunEventTextDelta, Text: "before"})
	p.Publish(contract.RunEvent{Kind: contract.RunEventTextDelta, Text: "after"})

	sub := p.Subscribe()
	first := <-sub.Events()
	if first.Text != "before" {
		t.Fatalf("the window must be replayed first, got %q", first.Text)
	}
	second := <-sub.Events()
	if second.Text != "after" {
		t.Fatalf("the replayed window must be complete, got %q", second.Text)
	}
	p.Publish(contract.RunEvent{Kind: contract.RunEventTextDelta, Text: "live"})
	live := <-sub.Events()
	if live.Text != "live" {
		t.Fatalf("the subscriber must follow the live events, got %q", live.Text)
	}
}
