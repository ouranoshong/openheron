package message

import (
	"bytes"
	"encoding/gob"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestStampsSurviveTheCheckpointEncoder(t *testing.T) {
	msg := NewAssistant("hi", nil)
	StampAgent(msg, "main")
	StampNow(msg)
	StampSubagent(msg)
	StampReasoningMs(msg, 1234)
	StampModel(msg, "deepseek/deepseek-chat", "high")

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(msg); err != nil {
		t.Fatalf("the checkpoint encoder must accept a stamped message: %v", err)
	}
}

func TestStampsAreFirstWriteWins(t *testing.T) {
	first := NewAssistant("hi", nil)
	StampAgent(first, "main")
	StampModel(first, "a/b", "high")

	second := NewAssistant("hi", nil)

	merged, err := ConcatChunks([]*schema.Message{first, second})
	if err != nil {
		t.Fatalf("concat: %v", err)
	}
	agent, ok := Read(merged)
	if !ok {
		t.Fatal("expected a provenance on the merged message")
	}
	if agent.Agent != "main" {
		t.Fatalf("agent = %q, want main", agent.Agent)
	}
	if agent.Model != "a/b" {
		t.Fatalf("model = %q, want a/b", agent.Model)
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(merged); err != nil {
		t.Fatalf("the checkpoint encoder must accept the merged message: %v", err)
	}

	both1 := NewAssistant("hi", nil)
	StampAgent(both1, "main")
	both2 := NewAssistant("hi", nil)
	StampAgent(both2, "other")

	corrupted, err := ConcatChunks([]*schema.Message{both1, both2})
	if err != nil {
		t.Fatalf("concat: %v", err)
	}
	if got := MessageAgent(corrupted); got == "main" {
		t.Fatal("eino no longer concatenates same-key values — " +
			"the first-write-wins rule can be relaxed (and the merged value stays clean either way)")
	}
}

func TestProvenanceValuesAreGobEncodable(t *testing.T) {
	msg := NewAssistant("hi", nil)
	StampAgent(msg, "main")
	StampNow(msg)
	StampSubagent(msg)
	StampReasoningMs(msg, 1234)
	StampModel(msg, "a/b", "high")

	for k, v := range msg.Extra {
		switch v.(type) {
		case string, int64, int, bool, float64, []byte:
		default:
			t.Fatalf("Extra[%q] has type %T — gob cannot encode an unregistered interface value, "+
				"and the checkpoint write would fail at stop time", k, v)
		}
	}

	ts := TSString(msg)
	if ts == "" {
		t.Fatal("expected a timestamp")
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000000000Z07:00", ts); err != nil {
		t.Fatalf("the timestamp must be fixed width: %v", err)
	}
}
