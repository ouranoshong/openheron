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
func AssistantDeltaReasoning(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	var parts []string
	for _, part := range msg.AssistantGenMultiContent {
		if part.Type != schema.ChatMessagePartTypeReasoning {
			continue
		}
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
