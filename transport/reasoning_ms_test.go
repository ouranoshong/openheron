package transport

import (
	"bytes"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

func TestTheEmitterMeasuresTheReasoningDuration(t *testing.T) {
	var buf bytes.Buffer
	e := &emitter{w: &buf, threadID: "t", runID: "r"}

	e.emitReasoningContent("m1", "想一下")
	time.Sleep(12 * time.Millisecond)
	e.emitReasoningEnd("m1")

	ms := e.reasoningMs("m1")
	if ms < 5 {
		t.Fatalf("reasoning duration = %dms, want at least a few ms", ms)
	}
	if ms > 5000 {
		t.Fatalf("reasoning duration = %dms, want the measured gap (not the whole run)", ms)
	}
}

func TestTheEmitterHasNoDurationWithoutReasoning(t *testing.T) {
	var buf bytes.Buffer
	e := &emitter{w: &buf, threadID: "t", runID: "r"}
	if got := e.reasoningMs("m1"); got != 0 {
		t.Fatalf("duration = %d, want 0 (unknown)", got)
	}
}

func TestTheArchivedMessageCarriesTheReasoningDuration(t *testing.T) {
	var buf bytes.Buffer
	e := &emitter{w: &buf, threadID: "t", runID: "r"}

	e.emitReasoningContent("m1", "想一下")
	time.Sleep(12 * time.Millisecond)
	e.emitReasoningEnd("m1")

	msg := schema.AssistantMessage("答完了", nil)
	message.StampReasoningMs(msg, e.reasoningMs("m1"))

	ms := message.ReasoningMs(msg)
	if ms < 5 {
		t.Fatalf("the archived message must carry the duration, got %d", ms)
	}
}
