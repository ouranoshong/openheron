package run

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func rejectUnanswerableToolCalls(hist []*schema.Message) error {
	open := map[string]bool{}
	for _, msg := range hist {
		if msg == nil {
			continue
		}
		if msg.Role == schema.Tool {
			if !open[msg.ToolCallID] {
				return errf("insufficient tool messages following tool_calls message")
			}
			open[msg.ToolCallID] = false
			continue
		}
		for id, waiting := range open {
			if waiting {
				return errf("assistant message with 'tool_calls' must be followed by "+
					"tool messages responding to each 'tool_call_id' (missing %q)", id)
			}
		}
		for _, tc := range msg.ToolCalls {
			open[tc.ID] = true
		}
	}
	for id, waiting := range open {
		if waiting {
			return errf("assistant message with 'tool_calls' must be followed by "+
				"tool messages responding to each 'tool_call_id' (missing %q)", id)
		}
	}
	return nil
}

type strErr string

func (e strErr) Error() string { return string(e) }

func errf(format string, args ...any) error {
	var b strings.Builder
	_, _ = b.WriteString(format)
	_ = args
	return strErr(b.String())
}

func TestHistorySatisfiesTheEndpointContract(t *testing.T) {
	cases := map[string][]*schema.Message{
		"unknown tool never produced a result": {
			schema.UserMessage("q"),
			assistantWithCalls("c1"),
		},
		"turn aborted between the call and its result": {
			schema.UserMessage("q"),
			assistantWithCalls("c1", "c2"),
			toolResult("c1", "ok"),
		},
		"interrupt replay wrote a duplicate tool message": {
			schema.UserMessage("q"),
			assistantWithCalls("c1"),
			toolResult("c1", "ok"),
			toolResult("c1", "ok"),
		},
		"three bad turns in one session": {
			schema.UserMessage("q1"),
			assistantWithCalls("c1"),
			schema.UserMessage("q2"),
			assistantWithCalls("c2"),
			schema.UserMessage("q3"),
			assistantWithCalls("c3"),
		},
	}

	for name, hist := range cases {
		t.Run(name, func(t *testing.T) {
			if err := rejectUnanswerableToolCalls(hist); err == nil {
				t.Fatalf("precondition failed: the endpoint would accept this history as-is, "+
					"so the case proves nothing: %s", describeHistory(hist))
			}

			in, err := InputConfig{}.buildInput(context.Background(), inputParams{History: hist}, schema.UserMessage("next"))
			if err != nil {
				t.Fatal(err)
			}
			if err := rejectUnanswerableToolCalls(in.Messages); err != nil {
				t.Fatalf("the endpoint would still reject it: %v\n%s", err, describeHistory(in.Messages))
			}
		})
	}
}

func TestNormalizeHistoryDoesNotFabricateResults(t *testing.T) {
	hist := []*schema.Message{schema.UserMessage("q"), assistantWithCalls("c1")}
	got := normalizeHistory(hist)

	last := got[len(got)-1]
	if last.Role != schema.Tool {
		t.Fatalf("expected a synthesized tool message, got %s", last.Role)
	}
	for _, forbidden := range []string{"found", "matches", "No matches", "results:"} {
		if strings.Contains(last.Content, forbidden) {
			t.Fatalf("synthesized result must not claim a finding: %q", last.Content)
		}
	}
	if !strings.Contains(last.Content, "did not run") {
		t.Fatalf("synthesized result must state it did not run: %q", last.Content)
	}
}
