package message

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestProvenanceRoundTrip(t *testing.T) {
	msg := NewAssistant("hi", nil)
	ts := time.Date(2026, 10, 27, 12, 0, 0, 123456789, time.UTC)

	Provenance{
		Agent:       "main",
		TS:          ts,
		Subagent:    true,
		ReasoningMs: 1234,
		Model:       "deepseek/deepseek-chat",
		Thinking:    "high",
	}.Stamp(msg)

	p, ok := Read(msg)
	if !ok {
		t.Fatal("expected the provenance to be found")
	}
	if p.Agent != "main" {
		t.Errorf("agent = %q, want main", p.Agent)
	}
	if !p.TS.Equal(ts) {
		t.Errorf("ts = %v, want %v", p.TS, ts)
	}
	if !p.Subagent {
		t.Error("subagent = false, want true")
	}
	if p.ReasoningMs != 1234 {
		t.Errorf("reasoningMs = %d, want 1234", p.ReasoningMs)
	}
	if p.Model != "deepseek/deepseek-chat" {
		t.Errorf("model = %q", p.Model)
	}
	if p.Thinking != "high" {
		t.Errorf("thinking = %q", p.Thinking)
	}
}

func TestReadReportsAbsence(t *testing.T) {
	if _, ok := Read(NewAssistant("hi", nil)); ok {
		t.Fatal("a message without any stamp must report absence")
	}
	if _, ok := Read(nil); ok {
		t.Fatal("a nil message must report absence")
	}
}

func TestStampDoesNotWriteZeroValues(t *testing.T) {
	msg := NewAssistant("hi", nil)
	Provenance{}.Stamp(msg)

	if msg.Extra != nil {
		t.Fatalf("a zero provenance must not create Extra, got %v", msg.Extra)
	}
	if _, ok := Read(msg); ok {
		t.Fatal("a zero provenance must report absence")
	}
}

func TestStampIsFirstWriteWins(t *testing.T) {
	msg := NewAssistant("hi", nil)
	ts := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

	Provenance{Agent: "main", TS: ts, ReasoningMs: 100, Model: "a/b", Thinking: "high"}.Stamp(msg)
	Provenance{Agent: "other", TS: ts.Add(time.Hour), ReasoningMs: 900, Model: "c/d", Thinking: "low"}.Stamp(msg)

	p, _ := Read(msg)
	if p.Agent != "main" {
		t.Errorf("agent = %q, want main (the first writer wins)", p.Agent)
	}
	if !p.TS.Equal(ts) {
		t.Errorf("ts = %v, want %v", p.TS, ts)
	}
	if p.ReasoningMs != 100 {
		t.Errorf("reasoningMs = %d, want 100", p.ReasoningMs)
	}
	if p.Model != "a/b" {
		t.Errorf("model = %q, want a/b", p.Model)
	}
	if p.Thinking != "high" {
		t.Errorf("thinking = %q, want high", p.Thinking)
	}
}

func TestStampWrappersMatchTheValue(t *testing.T) {
	msg := NewAssistant("hi", nil)
	ts := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

	StampAgent(msg, "main")
	StampTS(msg, ts)
	StampSubagent(msg)
	StampReasoningMs(msg, 42)
	StampModel(msg, "a/b", "high")

	p, ok := Read(msg)
	if !ok {
		t.Fatal("expected the provenance to be found")
	}
	if p.Agent != "main" || !p.TS.Equal(ts) || !p.Subagent ||
		p.ReasoningMs != 42 || p.Model != "a/b" || p.Thinking != "high" {
		t.Fatalf("provenance = %+v", p)
	}
}

func TestProvenanceTSStringReproducesTheStoredBytes(t *testing.T) {
	msg := NewAssistant("hi", nil)
	ts := time.Date(2026, 10, 27, 12, 0, 0, 123456789, time.UTC)
	Provenance{TS: ts}.Stamp(msg)

	stored, _ := msg.Extra[ExtraTS].(string)
	p, _ := Read(msg)
	if got := p.TSString(); got != stored {
		t.Fatalf("TSString = %q, want %q", got, stored)
	}
	if stored != "2026-10-27T12:00:00.123456789Z" {
		t.Fatalf("the stored timestamp is not fixed width: %q", stored)
	}
}

func TestProvenanceSurvivesTheJSONRoundTrip(t *testing.T) {
	msg := NewAssistant("hi", nil)
	ts := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	Provenance{Agent: "main", TS: ts, Subagent: true, ReasoningMs: 7,
		Model: "a/b", Thinking: "high"}.Stamp(msg)

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back schema.Message
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	p, ok := Read(&back)
	if !ok {
		t.Fatal("expected the provenance to survive")
	}
	if p.Agent != "main" || !p.TS.Equal(ts) || !p.Subagent || p.ReasoningMs != 7 ||
		p.Model != "a/b" || p.Thinking != "high" {
		t.Fatalf("provenance after the round trip = %+v", p)
	}
}

func TestZeroDurationIsNotStamped(t *testing.T) {
	msg := schema.AssistantMessage("答完了", nil)
	StampReasoningMs(msg, 0)
	if got := ReasoningMs(msg); got != 0 {
		t.Fatalf("duration = %d, want 0", got)
	}
	if _, ok := msg.Extra[ExtraReasoningMs]; ok {
		t.Fatalf("a zero duration must not be written at all: %v", msg.Extra)
	}
}

func TestTheDurationSurvivesTheJSONRoundTrip(t *testing.T) {
	msg := schema.AssistantMessage("答完了", nil)
	StampReasoningMs(msg, 20258)

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var back schema.Message
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if got := ReasoningMs(&back); got != 20258 {
		t.Fatalf("duration = %d, want 20258", got)
	}
}

func TestTheDurationOnlyLandsOnMessagesThatThought(t *testing.T) {
	plain := schema.AssistantMessage("直接答", nil)
	if got := ReasoningMs(plain); got != 0 {
		t.Fatalf("duration = %d, want 0", got)
	}
}
