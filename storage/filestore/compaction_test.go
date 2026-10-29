package filestore

import (
	"context"
	"testing"

	"github.com/ouranoshong/openheron/message"
)

// The effective history starts at the last compaction; the whole transcript
// stays available.
func TestEffectiveMessagesStartAtTheLastCompaction(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := sess.Append(message.NewUser("first question")); err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(message.NewAssistant("first answer", nil)); err != nil {
		t.Fatal(err)
	}
	compacted := message.NewUser("summary of the conversation so far")
	message.StampCompaction(compacted, message.Compaction{Superseded: 2, SavedTokens: 1200})
	if err := sess.Append(compacted); err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(message.NewUser("second question")); err != nil {
		t.Fatal(err)
	}

	all := sess.Messages(false)
	if len(all) != 4 {
		t.Fatalf("the whole transcript must keep every message, got %d", len(all))
	}

	effective := sess.Messages(true)
	if len(effective) != 2 {
		t.Fatalf("the effective history has %d messages, want 2", len(effective))
	}
	if message.UserText(effective[0]) != "summary of the conversation so far" {
		t.Fatalf("the effective history must start at the compaction, got %q",
			message.UserText(effective[0]))
	}
	if message.UserText(effective[1]) != "second question" {
		t.Fatalf("unexpected message after the compaction: %q", message.UserText(effective[1]))
	}
}

// A session that was never compacted has no effective view of its own: the
// whole transcript is the context.
func TestEffectiveMessagesWithoutACompaction(t *testing.T) {
	ctx := context.Background()
	fm, err := NewFileManager(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(message.NewUser("hello")); err != nil {
		t.Fatal(err)
	}

	if got := len(sess.Messages(true)); got != 1 {
		t.Fatalf("the effective history has %d messages, want the whole transcript", got)
	}
}

// The compaction has to outlive the process: it is written to the transcript,
// so a reopened session still knows where its context starts.
func TestEffectiveMessagesSurviveRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	fm, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Append(message.NewUser("first question")); err != nil {
		t.Fatal(err)
	}
	compacted := message.NewUser("summary of the conversation so far")
	message.StampCompaction(compacted, message.Compaction{Superseded: 1, SavedTokens: 900})
	if err := sess.Append(compacted); err != nil {
		t.Fatal(err)
	}
	id := sess.ID()

	fresh, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := fresh.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	if got := len(reloaded.Messages(false)); got != 2 {
		t.Fatalf("the transcript must keep both messages, got %d", got)
	}
	effective := reloaded.Messages(true)
	if len(effective) != 1 {
		t.Fatalf("the effective history has %d messages after a restart, want 1", len(effective))
	}
	c, ok := message.ReadCompaction(effective[0])
	if !ok {
		t.Fatal("the compaction marker did not survive the restart")
	}
	if c.Superseded != 1 || c.SavedTokens != 900 {
		t.Fatalf("the compaction lost its numbers: %+v", c)
	}
}
