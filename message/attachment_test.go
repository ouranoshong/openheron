package message

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// ★ The attachment list has to survive the checkpoint encoding.
//
//	The checkpoint encodes a message with gob, and gob cannot encode a
//	value of an unregistered type inside an interface. A list stored as a
//	struct slice would fail the checkpoint write at stop time — and the
//	failure would surface as a half-written approval, long after the
//	message that caused it.
func TestTheAttachmentListSurvivesTheCheckpointEncoding(t *testing.T) {
	msg := NewUser("hello")
	StampAttachments(msg, []Attachment{
		{Name: "a.png", Path: "a.png", Size: 12, MIME: "image/png"},
		{Name: "notes.md", Path: "notes.md", Size: 34},
	})

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(msg); err != nil {
		t.Fatalf("the checkpoint encoder must accept a message with attachments: %v", err)
	}

	var got schema.Message
	if err := gob.NewDecoder(&buf).Decode(&got); err != nil {
		t.Fatalf("the checkpoint decoder must accept it too: %v", err)
	}
	atts, ok := ReadAttachments(&got)
	if !ok {
		t.Fatal("the attachment list must survive the round trip")
	}
	if len(atts) != 2 {
		t.Fatalf("the list holds %d entries, want 2", len(atts))
	}
	if atts[0].Name != "a.png" || atts[0].Path != "a.png" || atts[0].MIME != "image/png" {
		t.Fatalf("the first entry did not survive: %+v", atts[0])
	}
	if atts[1].Size != 34 {
		t.Fatalf("the size did not survive: %+v", atts[1])
	}
}

// ★ The list survives the transcript's JSON round trip — that is the shape
// a session reload reads back.
func TestTheAttachmentListSurvivesTheTranscriptRoundTrip(t *testing.T) {
	msg := NewUser("hello")
	StampAttachments(msg, []Attachment{{Name: "a.png", Path: "a.png", Size: 12, MIME: "image/png"}})

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var got schema.Message
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	atts, ok := ReadAttachments(&got)
	if !ok || len(atts) != 1 {
		t.Fatalf("the list did not survive the JSON round trip: %+v", atts)
	}
	if atts[0].Name != "a.png" || atts[0].MIME != "image/png" {
		t.Fatalf("the entry did not survive: %+v", atts[0])
	}
}

// ★ The list is written once, like every other stamp.
func TestTheAttachmentListIsFirstWriteWins(t *testing.T) {
	msg := NewUser("hello")
	StampAttachments(msg, []Attachment{{Name: "first.png", Path: "first.png"}})
	StampAttachments(msg, []Attachment{{Name: "second.png", Path: "second.png"}})

	atts, _ := ReadAttachments(msg)
	if len(atts) != 1 || atts[0].Name != "first.png" {
		t.Fatalf("the list was rewritten: %+v", atts)
	}
}

// ★ A message with no attachments is a normal state.
func TestAMessageWithoutAttachmentsReadsAsEmpty(t *testing.T) {
	msg := NewUser("hello")

	if HasAttachments(msg) {
		t.Fatal("a message with no stamp must not claim attachments")
	}
	if atts, ok := ReadAttachments(msg); ok || atts != nil {
		t.Fatalf("atts = %+v, want none", atts)
	}
	if atts, ok := ReadAttachments(nil); ok || atts != nil {
		t.Fatalf("atts = %+v, want none", atts)
	}
}
