package api

import (
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

// The compacted message is a boundary: the client draws a divider on it, so
// the marker has to reach the browser with the numbers.
func TestTheHistoryCarriesTheCompactionMarker(t *testing.T) {
	m := &schema.Message{Role: schema.User, Content: "摘要"}
	at := time.Date(2026, 10, 29, 13, 4, 11, 0, time.UTC)
	message.StampCompaction(m, message.Compaction{Superseded: 12, SavedTokens: 62000, At: at})

	out, ok := toAgUiMessage(m, 0)
	if !ok {
		t.Fatal("the compacted message must survive the conversion")
	}
	mark, _ := out.Metadata["compaction"].(map[string]any)
	if mark == nil {
		t.Fatalf("metadata must carry a compaction marker: %v", out.Metadata)
	}
	if got, _ := mark["superseded"].(int); got != 12 {
		t.Fatalf("superseded = %v, want 12", mark["superseded"])
	}
	if got, _ := mark["savedTokens"].(int); got != 62000 {
		t.Fatalf("savedTokens = %v, want 62000", mark["savedTokens"])
	}
	if got, _ := mark["at"].(string); got != at.Format(time.RFC3339Nano) {
		t.Fatalf("at = %v, want %s", mark["at"], at.Format(time.RFC3339Nano))
	}
}

// A boundary is worth showing even when the summary behind it is empty: the
// marker is the whole point of the message.
func TestACompactionWithoutContentIsNotDropped(t *testing.T) {
	m := &schema.Message{Role: schema.User}
	message.StampCompaction(m, message.Compaction{Superseded: 3, SavedTokens: 900})

	out, ok := toAgUiMessage(m, 0)
	if !ok {
		t.Fatal("a compaction must not be dropped")
	}
	if _, has := out.Metadata["compaction"]; !has {
		t.Fatalf("metadata must carry a compaction marker: %v", out.Metadata)
	}
}

// An ordinary message is not a boundary.
func TestAnOrdinaryMessageHasNoCompactionMarker(t *testing.T) {
	m := &schema.Message{Role: schema.User, Content: "问一句"}
	out, ok := toAgUiMessage(m, 0)
	if !ok {
		t.Fatal("the message must survive the conversion")
	}
	if _, has := out.Metadata["compaction"]; has {
		t.Fatalf("metadata must not carry a compaction marker: %v", out.Metadata)
	}
}
