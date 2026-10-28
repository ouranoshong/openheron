package transport

import (
	"bytes"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"openheron/message"
)

// ── 思维链的用时 ─────────────────────────────────────────────────────────
//
//	本文件只测**发射器**：它怎么量出这段思维链花了多久。
//
//	「用时怎么写进消息、怎么在 JSONL 往返后读回来」是 message 包的语义，
//	那几条用例住在 message/provenance_value_test.go。

// ★ 发射器要能答出「这条消息的思维链想了多久」——
//
//	事件流不落盘，所以这个数只有在这里算；算出来写进归档消息的 Extra，
//	刷新之后才还在（前端回放时从消息里读）。
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

// 没有思维链的消息答不出用时 —— 0 是「不知道」，不是「想了 0 毫秒」。
func TestTheEmitterHasNoDurationWithoutReasoning(t *testing.T) {
	var buf bytes.Buffer
	e := &emitter{w: &buf, threadID: "t", runID: "r"}
	if got := e.reasoningMs("m1"); got != 0 {
		t.Fatalf("duration = %d, want 0 (unknown)", got)
	}
}

// ★ 归档的消息上要带着用时 —— 不带的话刷新一次就没了。
func TestTheArchivedMessageCarriesTheReasoningDuration(t *testing.T) {
	var buf bytes.Buffer
	e := &emitter{w: &buf, threadID: "t", runID: "r"}

	// 先让发射器知道这段思维链花了多久。
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
