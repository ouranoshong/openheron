package run

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func assistantWithCalls(ids ...string) *schema.Message {
	m := &schema.Message{Role: schema.Assistant}
	for _, id := range ids {
		m.ToolCalls = append(m.ToolCalls, schema.ToolCall{
			ID:       id,
			Type:     "function",
			Function: schema.FunctionCall{Name: "grep", Arguments: `{"pattern":"x"}`},
		})
	}
	return m
}

func toolResult(id, text string) *schema.Message {
	return schema.ToolMessage(text, id, schema.WithToolName("grep"))
}

func assertAnswerable(t *testing.T, hist []*schema.Message) {
	t.Helper()
	answered := map[string]bool{}
	checkPending := func(where string) {
		for id, ok := range answered {
			if !ok {
				t.Fatalf("tool_call %q is unanswered at %s: %s", id, where, describeHistory(hist))
			}
		}
	}

	for i, msg := range hist {
		if msg == nil {
			t.Fatalf("nil message survived normalization at %d: %s", i, describeHistory(hist))
		}
		if msg.Role == schema.Tool {
			if _, known := answered[msg.ToolCallID]; !known {
				t.Fatalf("orphan tool message %q at %d: %s", msg.ToolCallID, i, describeHistory(hist))
			}
			answered[msg.ToolCallID] = true
			continue
		}
		checkPending("message " + strconv.Itoa(i))
		for _, tc := range msg.ToolCalls {
			answered[tc.ID] = false
		}
	}
	checkPending("the tail")
}

// An assistant message with 'tool_calls' must be followed by tool messages
// responding to each 'tool_call_id'. (insufficient tool messages following
// tool_calls message)
func TestNormalizeHistoryFillsTheMissingResult(t *testing.T) {
	hist := []*schema.Message{
		schema.UserMessage("find it"),
		assistantWithCalls("c1"),
	}
	got := normalizeHistory(hist)

	assertAnswerable(t, got)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %s", len(got), describeHistory(got))
	}
	if got[2].Role != schema.Tool || got[2].ToolCallID != "c1" {
		t.Fatalf("a tool message for c1 must be synthesized: %s", describeHistory(got))
	}
	if got[0] != hist[0] {
		t.Fatal("the user's message must be preserved as-is")
	}
	if strings.Contains(got[2].Content, "abort") ||
		strings.Contains(got[2].Content, "rejected") ||
		strings.Contains(got[2].Content, "denied") {
		t.Fatalf("synthesized result must not guess a cause: %q", got[2].Content)
	}
	if !strings.Contains(got[2].Content, "not executed") {
		t.Fatalf("synthesized result must say it did not run: %q", got[2].Content)
	}
}

func TestNormalizeHistoryDropsOrphanToolMessages(t *testing.T) {
	hist := []*schema.Message{
		schema.UserMessage("hi"),
		toolResult("ghost", "result of a call nobody made"),
		assistantWithCalls("c1"),
		toolResult("c1", "real result"),
	}
	got := normalizeHistory(hist)

	assertAnswerable(t, got)
	for _, msg := range got {
		if msg.Role == schema.Tool && msg.ToolCallID == "ghost" {
			t.Fatalf("the orphan tool message survived: %s", describeHistory(got))
		}
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %s", len(got), describeHistory(got))
	}
}

func TestNormalizeHistoryFillsOnlyTheMissingOnes(t *testing.T) {
	hist := []*schema.Message{
		assistantWithCalls("c1", "c2"),
		toolResult("c1", "found 3 files"),
	}
	got := normalizeHistory(hist)

	assertAnswerable(t, got)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %s", len(got), describeHistory(got))
	}
	if got[1].Content != "found 3 files" {
		t.Fatalf("the real result must be kept verbatim, got %q", got[1].Content)
	}
	if got[2].ToolCallID != "c2" {
		t.Fatalf("the synthesized one must be for c2: %s", describeHistory(got))
	}
}

func TestNormalizeHistoryLeavesCleanHistoryAlone(t *testing.T) {
	hist := []*schema.Message{
		schema.UserMessage("q1"),
		assistantWithCalls("c1"),
		toolResult("c1", "r1"),
		schema.AssistantMessage("done", nil),
		schema.UserMessage("q2"),
	}
	got := normalizeHistory(hist)

	if len(got) != len(hist) {
		t.Fatalf("len = %d, want %d: %s", len(got), len(hist), describeHistory(got))
	}
	for i := range hist {
		if got[i] != hist[i] {
			t.Fatalf("message %d was rewritten: %s", i, describeHistory(got))
		}
	}
}

func TestNormalizeHistoryHandlesSeveralBadTurns(t *testing.T) {
	hist := []*schema.Message{
		schema.UserMessage("q1"),
		assistantWithCalls("c1"),
		schema.UserMessage("q2"),
		assistantWithCalls("c2", "c3"),
		toolResult("c2", "ok"),
		schema.AssistantMessage("mid", nil),
		assistantWithCalls("c4"),
	}
	got := normalizeHistory(hist)
	assertAnswerable(t, got)

	ids := map[string]bool{}
	for _, msg := range got {
		if msg.Role == schema.Tool {
			ids[msg.ToolCallID] = true
		}
	}
	for _, want := range []string{"c1", "c2", "c3", "c4"} {
		if !ids[want] {
			t.Fatalf("no tool message for %s: %s", want, describeHistory(got))
		}
	}
}

func TestNormalizeHistorySkipsNil(t *testing.T) {
	got := normalizeHistory([]*schema.Message{nil, schema.UserMessage("q"), nil})
	assertAnswerable(t, got)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %s", len(got), describeHistory(got))
	}
}

func TestBuildRunsHistoryThroughNormalize(t *testing.T) {
	c := InputConfig{ProjectRoot: "/tmp/x"}
	in, err := c.buildInput(context.Background(), inputParams{
		History: []*schema.Message{
			schema.UserMessage("q"),
			assistantWithCalls("c1"),
		},
	}, schema.UserMessage("next"))
	if err != nil {
		t.Fatal(err)
	}
	assertAnswerable(t, in.Messages[1:])
}
