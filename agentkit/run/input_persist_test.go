package run

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

// ★ The turn's question is persisted **after** the input is assembled.
//
//	The order is what makes the model and the transcript agree: the message
//	the model sees is the same object the transcript holds. A copy rebuilt
//	from the text would carry none of what was stamped on it.
//
// ★ It is also what keeps a failed assembly from leaving a question behind
//
//	that nobody answered — the client takes its optimistic bubble back, and
//	the transcript agrees with it.
func TestTheQuestionIsPersistedAfterTheInputIsBuilt(t *testing.T) {
	rt, sm := newTestRuntime(t, &fakeBuilder{})
	sess := mustSession(t, sm)

	turn, err := rt.Submit(context.Background(), sess.ID(), "", "hello")
	if err != nil {
		t.Fatal(err)
	}
	drain(t, turn)

	var found *schema.Message
	for _, m := range sess.Messages(false) {
		if message.UserText(m) == "hello" {
			found = m
		}
	}
	if found == nil {
		t.Fatal("the question must be in the transcript")
	}
	if !message.HasMessageID(found) {
		t.Fatal("the persisted question must carry an id of its own")
	}
}

// ★ Each persisted message gets its own id, so the client can key it
// whatever the transcript looks like later.
func TestEachPersistedMessageGetsItsOwnID(t *testing.T) {
	rt, sm := newTestRuntime(t, &fakeBuilder{})
	sess := mustSession(t, sm)

	for _, q := range []string{"first", "second"} {
		turn, err := rt.Submit(context.Background(), sess.ID(), "", q)
		if err != nil {
			t.Fatal(err)
		}
		// ★ Wait for the session to be **free**, not merely for the archive:
		//   the admission gate reads the live turn, so a second Submit can
		//   be refused while the first turn's messages are already on disk.
		turn.WaitReleased()
		drain(t, turn)
	}

	seen := map[string]bool{}
	n := 0
	for _, m := range sess.Messages(false) {
		if message.UserText(m) == "" {
			continue
		}
		id := message.MessageID(m)
		if id == "" {
			t.Fatalf("the message %q carries no id", message.UserText(m))
		}
		if seen[id] {
			t.Fatalf("two messages share the id %q", id)
		}
		seen[id] = true
		n++
	}
	if n < 2 {
		t.Fatalf("the transcript holds %d question(s), want at least 2", n)
	}
}
