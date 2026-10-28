package message

import "github.com/cloudwego/eino/schema"

// ToolCall is the common function-tool-call shape used across openheron.
type ToolCall struct {
	ID    string
	Name  string
	Args  string
	Index int
}

// ToolResult is the common function-tool-result shape used across openheron.
type ToolResult struct {
	ID      string
	Name    string
	Content string
}

// NewUser constructs a user message.
func NewUser(text string) *schema.Message {
	return schema.UserMessage(text)
}

// NewAssistant constructs an assistant message with optional function tool calls.
func NewAssistant(text string, calls []ToolCall) *schema.Message {
	schemaCalls := make([]schema.ToolCall, 0, len(calls))
	for _, call := range calls {
		idx := call.Index
		schemaCalls = append(schemaCalls, schema.ToolCall{
			ID:       call.ID,
			Index:    &idx,
			Function: schema.FunctionCall{Name: call.Name, Arguments: call.Args},
		})
	}
	return schema.AssistantMessage(text, schemaCalls)
}

// NewToolResult constructs a tool-result message.
func NewToolResult(id, name, content string) *schema.Message {
	return schema.ToolMessage(content, id, schema.WithToolName(name))
}
