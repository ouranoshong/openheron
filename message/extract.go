package message

import (
	"strings"

	"github.com/cloudwego/eino/schema"
)

// Text returns the user-facing text found in a message.
func Text(msg *schema.Message) string {
	return messageText(msg)
}

// AssistantText returns generated assistant text from a message.
func AssistantText(msg *schema.Message) string {
	return messageAssistantText(msg)
}

// AssistantDeltaText returns assistant text from one streaming chunk.
func AssistantDeltaText(msg *schema.Message) string {
	return messageAssistantText(msg)
}

// AssistantDeltaReasoning returns the model's reasoning text from one streaming chunk.
//
// ★ 为什么单独一个取值函数：eino 把推理放在两处不同形状里 ——
//
//	多模态路径是 AssistantGenMultiContent 里 type=reasoning 的 part，
//	走扁平的 ReasoningContent 字段。先看 part 再看扁平字段，因为
//	同时存在时 part 更具体（部分模型会同时填两处）。
//
// ★ 为什么要读它：transport 此前只取 AssistantDeltaText，推理内容
//
//	在传输层被整个丢弃 —— 前端那 456 行 reasoning UI 因此永远拿不到
//	数据，是死代码。
func AssistantDeltaReasoning(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	var parts []string
	for _, part := range msg.AssistantGenMultiContent {
		if part.Type != schema.ChatMessagePartTypeReasoning {
			continue
		}
		// 优先 Reasoning.Text；部分实现只填 part.Text。
		switch {
		case part.Reasoning != nil && part.Reasoning.Text != "":
			parts = append(parts, part.Reasoning.Text)
		case part.Text != "":
			parts = append(parts, part.Text)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "")
	}
	return msg.ReasoningContent
}

// HasContent reports whether a message carries any content worth persisting.
func HasContent(msg *schema.Message) bool {
	if msg == nil {
		return false
	}
	return msg.Content != "" ||
		msg.ReasoningContent != "" ||
		len(msg.ToolCalls) > 0 ||
		len(msg.MultiContent) > 0 ||
		len(msg.AssistantGenMultiContent) > 0
}

// UserText returns text only for user-role messages.
func UserText(msg *schema.Message) string {
	if msg == nil || msg.Role != schema.User {
		return ""
	}
	return messageText(msg)
}

// ToolCalls extracts function tool calls from a message or streaming chunk.
func ToolCalls(msg *schema.Message) []ToolCall {
	if msg == nil {
		return nil
	}
	out := make([]ToolCall, 0, len(msg.ToolCalls))
	for _, tc := range msg.ToolCalls {
		idx := 0
		if tc.Index != nil {
			idx = *tc.Index
		}
		out = append(out, ToolCall{
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Args:  tc.Function.Arguments,
			Index: idx,
		})
	}
	return out
}

// toolResults extracts function tool results from a message or streaming chunk.
//
// ★ 不导出：只有本包用它（variant.go 判定与抽取）—— 对外暴露等于给
//
//	调用方一个「消息里有工具结果」的承诺，而那个承诺没人接。
func toolResults(msg *schema.Message) []ToolResult {
	if msg == nil || (msg.Role != schema.Tool && msg.ToolCallID == "") {
		return nil
	}
	return []ToolResult{{
		ID:      msg.ToolCallID,
		Name:    msg.ToolName,
		Content: messageText(msg),
	}}
}

func messageText(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	if msg.Content != "" {
		return msg.Content
	}
	var parts []string
	for _, part := range msg.UserInputMultiContent {
		if part.Type == schema.ChatMessagePartTypeText && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	for _, part := range msg.AssistantGenMultiContent {
		if part.Type == schema.ChatMessagePartTypeText && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func messageAssistantText(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	if msg.Content != "" {
		return msg.Content
	}
	var parts []string
	for _, part := range msg.AssistantGenMultiContent {
		if part.Type == schema.ChatMessagePartTypeText && part.Text != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "")
}
