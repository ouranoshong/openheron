package api

import (
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

// ★ A message that carries its own id keeps it, whatever the transcript
// around it looks like.
//
//	The derived id mixes the content with the message's **position**, so a
//	compaction marker inserted in front of it renumbered it. The client keys
//	its messages by this id, so every compaction rebuilt the whole
//	conversation.
func TestAMessageKeepsItsOwnIDAcrossACompaction(t *testing.T) {
	q2 := schema.UserMessage("q2")
	message.StampMessageID(q2, "msg-q2-fixed")

	// Before the compaction: the message sits at index 1.
	before, ok := toAgUiMessage(q2, 1)
	if !ok {
		t.Fatal("the message must survive the conversion")
	}
	if before.ID != "msg-q2-fixed" {
		t.Fatalf("id = %q, want the message's own id", before.ID)
	}

	// A compaction marker lands in front of it, so the position changes.
	marker := schema.AssistantMessage("summary", nil)
	message.StampCompaction(marker, message.Compaction{Superseded: 1, SavedTokens: 10})
	after, ok := toAgUiMessage(q2, 2)
	if !ok {
		t.Fatal("the message must survive the conversion")
	}
	if after.ID != before.ID {
		t.Fatalf("the id changed with the position: %q became %q", before.ID, after.ID)
	}
}

// ★ A transcript written before messages carried ids still gets a usable id:
// the derived one, exactly as before. That is what makes this safe to ship.
func TestAMessageWithoutAnIDFallsBackToTheDerivedOne(t *testing.T) {
	m := schema.UserMessage("q1")

	out, ok := toAgUiMessage(m, 0)
	if !ok {
		t.Fatal("the message must survive the conversion")
	}
	if out.ID != stableID(m, 0) {
		t.Fatalf("id = %q, want the derived one %q", out.ID, stableID(m, 0))
	}
	if out.ID == "" {
		t.Fatal("the id must not be empty")
	}
}

// ★ Two messages that carry no id of their own still get different ids at
// different positions — the fallback must keep its uniqueness.
func TestTheDerivedIDIsStillUniquePerPosition(t *testing.T) {
	a := schema.UserMessage("same")
	b := schema.UserMessage("same")

	if stableID(a, 0) == stableID(b, 1) {
		t.Fatal("identical content at different positions must get different ids")
	}
	if stableID(a, 0) != stableID(a, 0) {
		t.Fatal("the derived id must be deterministic")
	}
}
