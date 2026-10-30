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

// The compacted message summarizes everything that came before it, so it has
// to be written ahead of the turn's own messages.
func TestTheCompactedMessageIsArchivedBeforeTheTurnMessages(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(compactionEvent(message.NewUser("summary of the conversation")))
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)

	waitFor(t, func() bool { return sess.Len() >= 3 })

	msgs := sess.Messages(false)
	if len(msgs) != 3 {
		t.Fatalf("the transcript has %d messages, want 3", len(msgs))
	}
	if message.UserText(msgs[0]) != "hello there" {
		t.Fatalf("the user message must come first, got %q", message.Text(msgs[0]))
	}
	if message.UserText(msgs[1]) != "summary of the conversation" {
		t.Fatalf("the compacted message must be archived before the turn's messages, got %q",
			message.Text(msgs[1]))
	}
	if message.AssistantText(msgs[2]) != "working" {
		t.Fatalf("unexpected last message: %#v", msgs[2])
	}

	c, ok := message.ReadCompaction(msgs[1])
	if !ok {
		t.Fatal("the archived message carries no compaction marker")
	}
	if c.Superseded != 1 {
		t.Fatalf("the compaction superseded %d messages, want 1", c.Superseded)
	}
	if c.At.IsZero() {
		t.Fatal("the compaction carries no timestamp")
	}

	// The context starts at the compaction: the user message above it is
	// superseded and must not be sent to the model again.
	effective := sess.Messages(true)
	if len(effective) != 2 {
		t.Fatalf("the effective history has %d messages, want 2", len(effective))
	}
	if message.UserText(effective[0]) != "summary of the conversation" {
		t.Fatalf("the effective history must start at the compaction, got %q",
			message.UserText(effective[0]))
	}
}

// A turn without a compaction leaves the transcript as it was.
func TestATurnWithoutACompaction(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)

	waitFor(t, func() bool { return sess.Len() >= 2 })

	if got := len(sess.Messages(true)); got != 2 {
		t.Fatalf("without a compaction the effective history is the whole transcript, got %d", got)
	}
}
