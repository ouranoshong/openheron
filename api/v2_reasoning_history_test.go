package api

import (
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

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
