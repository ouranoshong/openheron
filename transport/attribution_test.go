package transport

import (
	"github.com/ouranoshong/openheron/agentkit/emit"

	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/message"
)

func agentSegment(name string) adk.AddressSegment {
	return adk.AddressSegment{Type: adk.AddressSegmentAgent, ID: name}
}

func toolSegment(name string) adk.AddressSegment {
	return adk.AddressSegment{Type: adk.AddressSegmentTool, ID: name}
}

func parallelInterruptEvent() *adk.TypedAgentEvent[*schema.Message] {
	return &adk.TypedAgentEvent[*schema.Message]{
		AgentName: "main",
		Action: &adk.AgentAction{Interrupted: &adk.InterruptInfo{
			InterruptContexts: []*adk.InterruptCtx{
				{
					ID: "ic-researcher", IsRootCause: true,
					Address: adk.Address{
						agentSegment("main"),
						toolSegment("task"),
						agentSegment("researcher"),
						toolSegment("write_file"),
					},
					Info: "write_file needs approval",
				},
				{
					ID: "ic-coder", IsRootCause: true,
					Address: adk.Address{
						agentSegment("main"),
						toolSegment("task"),
						agentSegment("coder"),
						toolSegment("execute"),
					},
					Info: "execute needs approval",
				},
			},
		}},
	}
}

func TestParallelInterruptsKeepTheirOwnRequester(t *testing.T) {
	evs, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{parallelInterruptEvent()},
		emit.Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	fin := evs[1].(*events.RunFinishedEvent)
	if fin.Outcome == nil || fin.Outcome.Type != events.RunFinishedOutcomeTypeInterrupt {
		t.Fatalf("it must end with an interrupt, got %+v", fin.Outcome)
	}
	if len(fin.Outcome.Interrupts) != 2 {
		t.Fatalf("it must carry 2 interrupts, got %d", len(fin.Outcome.Interrupts))
	}

	want := map[string]string{
		"ic-researcher": "researcher",
		"ic-coder":      "coder",
	}
	for _, it := range fin.Outcome.Interrupts {
		if got := it.SubagentRunID; got != want[it.ID] {
			t.Fatalf("interrupt %s: subagentRunId = %q, want %q", it.ID, got, want[it.ID])
		}
	}

	if len(res.Pending) != 2 {
		t.Fatalf("Result.Pending must carry 2 entries, got %d", len(res.Pending))
	}
	for _, p := range res.Pending {
		if p.Detail == nil {
			t.Fatalf("pending %s has no detail", p.InterruptID)
		}
		if got := p.Detail.RequestedByAgent; got != want[p.InterruptID] {
			t.Fatalf("pending %s: requestedByAgent = %q, want %q", p.InterruptID, got, want[p.InterruptID])
		}
		if got := p.Detail.SubagentRunID; got != want[p.InterruptID] {
			t.Fatalf("pending %s: subagentRunId = %q, want %q", p.InterruptID, got, want[p.InterruptID])
		}
	}
}

func TestRootInterruptHasNoRequester(t *testing.T) {
	ev := &adk.TypedAgentEvent[*schema.Message]{
		AgentName: "main",
		Action: &adk.AgentAction{Interrupted: &adk.InterruptInfo{
			InterruptContexts: []*adk.InterruptCtx{{
				ID: "ic-root", IsRootCause: true,
				Address: adk.Address{agentSegment("main"), toolSegment("write_file")},
				Info:    "write_file needs approval",
			}},
		}},
	}

	evs, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{ev}, emit.Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	fin := evs[1].(*events.RunFinishedEvent)
	if got := fin.Outcome.Interrupts[0].SubagentRunID; got != "" {
		t.Fatalf("the entry agent must not be attributed, got %q", got)
	}
	if res.Pending[0].Detail.RequestedByAgent != "" {
		t.Fatalf("the entry agent must not be a requester, got %q", res.Pending[0].Detail.RequestedByAgent)
	}
}

func TestInterruptWithoutAddressHasNoRequester(t *testing.T) {
	ev := &adk.TypedAgentEvent[*schema.Message]{
		AgentName: "main",
		Action: &adk.AgentAction{Interrupted: &adk.InterruptInfo{
			InterruptContexts: []*adk.InterruptCtx{{
				ID: "ic-legacy", IsRootCause: true,
				Info: "write_file needs approval",
			}},
		}},
	}

	evs, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{ev}, emit.Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	fin := evs[1].(*events.RunFinishedEvent)
	if got := fin.Outcome.Interrupts[0].SubagentRunID; got != "" {
		t.Fatalf("an interrupt without an address must not be attributed, got %q", got)
	}
}

func TestToolOnlyMessageStillCarriesItsAgent(t *testing.T) {
	idx := 0
	evs, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		{
			AgentName: "coder",
			Output: &adk.TypedAgentOutput[*schema.Message]{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role: schema.Assistant,
					Message: &schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							Index: &idx, ID: "call-1",
							Function: schema.FunctionCall{Name: "write_file", Arguments: "{}"},
						}},
					},
				},
			},
		},
		exitEvent(),
	}, emit.Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	var start, end int
	for i, e := range evs {
		switch e.Type() {
		case events.EventTypeTextMessageStart:
			start++
			tm, ok := e.(*events.TextMessageStartEvent)
			if !ok {
				t.Fatalf("event %d is not a TextMessageStartEvent", i)
			}
			if tm.Name != "coder" {
				t.Fatalf("the message's name = %q, want coder", tm.Name)
			}
		case events.EventTypeTextMessageEnd:
			end++
		}
	}
	if start != 1 || end != 1 {
		t.Fatalf("there must be exactly one announce pair, got %d starts / %d ends", start, end)
	}
}

func TestTextMessageCarriesItsAgentName(t *testing.T) {
	evs, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		{
			AgentName: "coder",
			Output: &adk.TypedAgentOutput[*schema.Message]{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role: schema.Assistant, Message: assistantText("patching"),
				},
			},
		},
		exitEvent(),
	}, emit.Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	var start *events.TextMessageStartEvent
	for _, e := range evs {
		if ts, ok := e.(*events.TextMessageStartEvent); ok {
			start = ts
		}
	}
	if start == nil {
		t.Fatal("no TEXT_MESSAGE_START event")
	}
	if start.Name != "coder" {
		t.Fatalf("the message's name = %q, want coder", start.Name)
	}
}

func TestToolCallCarriesItsParentMessage(t *testing.T) {
	idx := 0
	evs, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		{
			AgentName: "coder",
			Output: &adk.TypedAgentOutput[*schema.Message]{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role: schema.Assistant,
					Message: &schema.Message{
						Role: schema.Assistant,
						ToolCalls: []schema.ToolCall{{
							Index: &idx, ID: "call-1",
							Function: schema.FunctionCall{Name: "write_file", Arguments: "{}"},
						}},
					},
				},
			},
		},
		exitEvent(),
	}, emit.Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	var toolStart *events.ToolCallStartEvent
	for _, e := range evs {
		if ts, ok := e.(*events.ToolCallStartEvent); ok {
			toolStart = ts
		}
	}
	if toolStart == nil {
		t.Fatal("no TOOL_CALL_START event")
	}
	if toolStart.ParentMessageID == nil || *toolStart.ParentMessageID == "" {
		t.Fatal("the tool call must point at its parent message")
	}
}

func TestIntermediateMessagesCarryTheirTimestamp(t *testing.T) {
	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		{
			AgentName: "main",
			Output: &adk.TypedAgentOutput[*schema.Message]{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role: schema.Assistant, Message: assistantText("planning"),
				},
			},
		},
		{
			AgentName: "coder",
			Output: &adk.TypedAgentOutput[*schema.Message]{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role: schema.Tool, Message: toolResultMessage("call-1", "write_file", "ok"),
				},
			},
		},
		exitEvent(),
	}, emit.Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	if len(res.Intermediates) != 2 {
		t.Fatalf("intermediates = %d, want 2", len(res.Intermediates))
	}
	for i, m := range res.Intermediates {
		ts, ok := message.MessageTS(m)
		if !ok {
			t.Fatalf("message %d has no timestamp", i)
		}
		if ts.IsZero() {
			t.Fatalf("message %d has a zero timestamp", i)
		}
	}
}

func TestIntermediateMessagesCarryAgentStamp(t *testing.T) {
	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		{
			AgentName: "main",
			Output: &adk.TypedAgentOutput[*schema.Message]{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role: schema.Assistant, Message: assistantText("planning"),
				},
			},
		},
		{
			AgentName: "coder",
			Output: &adk.TypedAgentOutput[*schema.Message]{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role: schema.Assistant, Message: assistantText("patching"),
				},
			},
		},
		{
			AgentName: "coder",
			Output: &adk.TypedAgentOutput[*schema.Message]{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role: schema.Tool, Message: toolResultMessage("call-1", "write_file", "ok"),
				},
			},
		},
		exitEvent(),
	}, emit.Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	if len(res.Intermediates) != 3 {
		t.Fatalf("intermediates = %d, want 3", len(res.Intermediates))
	}
	want := []string{"main", "coder", "coder"}
	for i, m := range res.Intermediates {
		if got := message.MessageAgent(m); got != want[i] {
			t.Fatalf("message %d: agent = %q, want %q", i, got, want[i])
		}
	}
}

func TestToolResultCarriesAgentStamp(t *testing.T) {
	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		{
			AgentName: "researcher",
			Output: &adk.TypedAgentOutput[*schema.Message]{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role: schema.Tool, Message: toolResultMessage("call-9", "read_file", "package main"),
				},
			},
		},
		exitEvent(),
	}, emit.Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if got := message.MessageAgent(res.Intermediates[0]); got != "researcher" {
		t.Fatalf("the tool result's agent = %q, want researcher", got)
	}
}
