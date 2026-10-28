package api

import (
	"testing"

	"github.com/cloudwego/eino/schema"

	"openheron/message"
)

// ── 历史里的思维链 ───────────────────────────────────────────────────────

// ★ 历史里原来**只有正文** —— 推理内容从来没进过历史，于是刷新一次，
//
//	思维链整个没了。现在正文与用时一起带出去（走 AG-UI 的 metadata）。
func TestTheHistoryCarriesTheReasoningAndItsDuration(t *testing.T) {
	m := &schema.Message{
		Role:    schema.Assistant,
		Content: "答完了",
		AssistantGenMultiContent: []schema.MessageOutputPart{
			{Type: schema.ChatMessagePartTypeReasoning, Text: "先看看这个仓库"},
		},
	}
	message.StampReasoningMs(m, 20258)

	out, ok := toAgUiMessage(m, 0)
	if !ok {
		t.Fatal("the message must survive the conversion")
	}
	reasoning, _ := out.Metadata["reasoning"].(string)
	if reasoning != "先看看这个仓库" {
		t.Fatalf("reasoning = %q, want the archived thought", reasoning)
	}
	ms, _ := out.Metadata["reasoningMs"].(int64)
	if ms != 20258 {
		t.Fatalf("reasoningMs = %v, want 20258", out.Metadata["reasoningMs"])
	}
}

// ★ 没有思维链的消息不带这两个键 —— 空 = 不知道，不是「想了 0 毫秒」。
func TestTheHistoryHasNoReasoningKeysWithoutAThought(t *testing.T) {
	m := &schema.Message{Role: schema.Assistant, Content: "直接答"}
	out, ok := toAgUiMessage(m, 0)
	if !ok {
		t.Fatal("the message must survive the conversion")
	}
	if _, has := out.Metadata["reasoning"]; has {
		t.Fatalf("metadata must not carry a reasoning key: %v", out.Metadata)
	}
	if _, has := out.Metadata["reasoningMs"]; has {
		t.Fatalf("metadata must not carry a reasoningMs key: %v", out.Metadata)
	}
}

// ★ 只有思维链、没有正文的消息**也要留着** ——
//
//	丢掉它等于「这一轮它只想了、没说话」被抹掉了。
func TestAMessageWithOnlyAThoughtIsNotDropped(t *testing.T) {
	m := &schema.Message{
		Role: schema.Assistant,
		AssistantGenMultiContent: []schema.MessageOutputPart{
			{Type: schema.ChatMessagePartTypeReasoning, Text: "只想了一下"},
		},
	}
	out, ok := toAgUiMessage(m, 0)
	if !ok {
		t.Fatal("a message that only thought must not be dropped")
	}
	if reasoning, _ := out.Metadata["reasoning"].(string); reasoning != "只想了一下" {
		t.Fatalf("reasoning = %q", reasoning)
	}
}

// ── 历史里的模型与思考档位 ───────────────────────────────────────────────

// ★ 事后要能回答「这条是哪个模型、哪个思考档位产的」—— 对账与复现都要它。
//
//	没有就是「不知道」，不是「没配」。
func TestTheHistoryCarriesTheModelAndThinkingTier(t *testing.T) {
	m := &schema.Message{Role: schema.Assistant, Content: "答完了"}
	message.StampModel(m, "deepseek/deepseek-flash", "high")

	out, ok := toAgUiMessage(m, 0)
	if !ok {
		t.Fatal("the message must survive the conversion")
	}
	if got, _ := out.Metadata["model"].(string); got != "deepseek/deepseek-flash" {
		t.Fatalf("model = %q, want deepseek/deepseek-flash", got)
	}
	if got, _ := out.Metadata["thinking"].(string); got != "high" {
		t.Fatalf("thinking = %q, want high", got)
	}
}

// 老消息（没盖戳）不带这两个键。
func TestTheHistoryHasNoModelKeysWithoutAStamp(t *testing.T) {
	m := &schema.Message{Role: schema.Assistant, Content: "直接答"}
	out, ok := toAgUiMessage(m, 0)
	if !ok {
		t.Fatal("the message must survive the conversion")
	}
	if _, has := out.Metadata["model"]; has {
		t.Fatalf("metadata must not carry a model key: %v", out.Metadata)
	}
	if _, has := out.Metadata["thinking"]; has {
		t.Fatalf("metadata must not carry a thinking key: %v", out.Metadata)
	}
}
