package message

import (
	"bytes"
	"encoding/gob"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// ★ The message id travels through the checkpoint, so it has to survive gob.
//
//	The checkpoint encodes a message with gob, and gob cannot encode a value
//	of an unregistered type inside an interface — a struct id would break the
//	checkpoint write at stop time, and the session would be left with a
//	half-written approval.
func TestAMessageIDSurvivesTheCheckpointEncoding(t *testing.T) {
	msg := NewUser("hello")
	StampMessageID(msg, "msg-abc-123")

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(msg); err != nil {
		t.Fatalf("the checkpoint encoder must accept a message with an id: %v", err)
	}

	var got schema.Message
	if err := gob.NewDecoder(&buf).Decode(&got); err != nil {
		t.Fatalf("the checkpoint decoder must accept it too: %v", err)
	}
	if id := MessageID(&got); id != "msg-abc-123" {
		t.Fatalf("id = %q, want %q", id, "msg-abc-123")
	}
}

// ★ The id is written once. A message that is stamped twice keeps the
// identity it was born with — that is what makes it stable.
func TestAMessageIDIsFirstWriteWins(t *testing.T) {
	msg := NewUser("hello")
	StampMessageID(msg, "first")
	StampMessageID(msg, "second")

	if id := MessageID(msg); id != "first" {
		t.Fatalf("id = %q, want %q", id, "first")
	}
}

// ★ A message with no id is a normal state — a transcript written before
// messages carried ids. It reads as empty, and the caller falls back.
func TestAMessageWithoutAnIDReadsAsEmpty(t *testing.T) {
	msg := NewUser("hello")

	if HasMessageID(msg) {
		t.Fatal("a message with no stamp must not claim an id")
	}
	if id := MessageID(msg); id != "" {
		t.Fatalf("id = %q, want empty", id)
	}
	if id := MessageID(nil); id != "" {
		t.Fatalf("id = %q, want empty", id)
	}
}
