package run

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

func normalizeHistory(hist []*schema.Message) []*schema.Message {
	if !historyNeedsRepair(hist) {
		return hist
	}
	return repairHistory(hist)
}

func repairHistory(hist []*schema.Message) []*schema.Message {
	out := make([]*schema.Message, 0, len(hist)+4)
	var pending []pendingCall

	flush := func() {
		for _, p := range pending {
			out = append(out, schema.ToolMessage(missingResultText(p.name), p.id,
				schema.WithToolName(p.name)))
		}
		pending = pending[:0]
	}

	for _, msg := range hist {
		if msg == nil {
			continue
		}
		if msg.Role == schema.Tool {
			i := pendingIndex(pending, msg.ToolCallID)
			if i < 0 {
				continue
			}
			pending = append(pending[:i], pending[i+1:]...)
			out = append(out, msg)
			continue
		}

		flush()
		out = append(out, msg)
		for _, tc := range msg.ToolCalls {
			pending = append(pending, pendingCall{id: tc.ID, name: tc.Function.Name})
		}
	}
	flush()
	return out
}

type pendingCall struct {
	id   string
	name string
}

func pendingIndex(p []pendingCall, id string) int {
	for i, c := range p {
		if c.id == id {
			return i
		}
	}
	return -1
}

func historyNeedsRepair(hist []*schema.Message) bool {
	var pending map[string]bool
	unmatched := 0
	for _, msg := range hist {
		if msg == nil {
			return true
		}
		if msg.Role == schema.Tool {
			if !pending[msg.ToolCallID] {
				return true
			}
			delete(pending, msg.ToolCallID)
			unmatched--
			continue
		}
		if unmatched > 0 {
			return true
		}
		for _, tc := range msg.ToolCalls {
			if pending == nil {
				pending = make(map[string]bool, len(msg.ToolCalls))
			}
			pending[tc.ID] = true
			unmatched++
		}
	}
	return unmatched > 0
}

func missingResultText(name string) string {
	tool := name
	if tool == "" {
		tool = "the tool"
	}
	return fmt.Sprintf(
		"[no result] %q did not run: the turn ended before its result came back. "+
			"Treat it as not executed — do not assume anything about what it would have returned, "+
			"and do not call it again in this turn.", tool)
}

func toolCallNamesOf(hist []*schema.Message) []string {
	var out []string
	for _, msg := range hist {
		if msg == nil {
			continue
		}
		for _, tc := range msg.ToolCalls {
			out = append(out, tc.Function.Name)
		}
	}
	return out
}

func describeHistory(hist []*schema.Message) string {
	parts := make([]string, 0, len(hist))
	for _, msg := range hist {
		if msg == nil {
			parts = append(parts, "nil")
			continue
		}
		switch {
		case msg.Role == schema.Tool:
			parts = append(parts, "tool("+msg.ToolCallID+")")
		case len(msg.ToolCalls) > 0:
			parts = append(parts, "assistant+calls("+strings.Join(toolCallNamesOf([]*schema.Message{msg}), ",")+")")
		default:
			parts = append(parts, string(msg.Role))
		}
	}
	return strings.Join(parts, " | ")
}
