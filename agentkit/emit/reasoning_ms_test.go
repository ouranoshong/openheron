package emit

import (
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

// The derivation measures the reasoning's duration, because the derivation is
// what sees the reasoning start and end.
func TestTheDerivationMeasuresTheReasoningDuration(t *testing.T) {
	d := &deriver{opts: Options{}}
	s := newStreamState(d, schema.Assistant, "", "")

	s.markReasonStart("m1")
	time.Sleep(12 * time.Millisecond)
	s.markReasonEnd("m1")

	ms := s.reasoningMs("m1")
	if ms < 5 {
		t.Fatalf("reasoning duration = %dms, want at least a few ms", ms)
	}
	if ms > 5000 {
		t.Fatalf("reasoning duration = %dms, want the measured gap (not the whole run)", ms)
	}
}

func TestTheDerivationHasNoDurationWithoutReasoning(t *testing.T) {
	d := &deriver{opts: Options{}}
	s := newStreamState(d, schema.Assistant, "", "")
	if got := s.reasoningMs("m1"); got != 0 {
		t.Fatalf("duration = %d, want 0 (unknown)", got)
	}
}

func TestTheArchivedMessageCarriesTheReasoningDuration(t *testing.T) {
	d := &deriver{opts: Options{}}
	s := newStreamState(d, schema.Assistant, "", "")

	s.markReasonStart("m1")
	time.Sleep(12 * time.Millisecond)
	s.markReasonEnd("m1")

	msg := schema.AssistantMessage("答完了", nil)
	message.StampReasoningMs(msg, s.reasoningMs("m1"))

	ms := message.ReasoningMs(msg)
	if ms < 5 {
		t.Fatalf("the archived message must carry the duration, got %d", ms)
	}
}
