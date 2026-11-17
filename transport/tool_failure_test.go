package transport

import (
	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/emit"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/helpers"
	"github.com/ouranoshong/openheron/message"
)

// ★ The tool result frame carries the content and nothing else, and the
//
//	content says what the **model** read — not what happened to the call.
//	⇒ The failure needs a frame of its own, and it needs the call id to
//	join with.
func TestAFailedCallIsRenderedAsACustomFrame(t *testing.T) {
	failures := helpers.NewToolFailures()
	failures.Record(helpers.ToolFailure{
		ToolID:   "call-1",
		ToolName: "read_file",
		Error:    "file not found: /tmp/missing.txt",
	})

	evs, _, _ := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(toolResultMessage("call-1", "read_file", "[tool error] file not found: /tmp/missing.txt")),
		exitEvent(),
	}, emit.Options{ToolFailures: failures})

	custom := customEvent(t, evs, "tool_failed")
	value, _ := custom.Value.(map[string]any)
	if value["tool_call_id"] != "call-1" {
		t.Fatalf("the frame must name the call it belongs to: %v", value)
	}
	if value["tool_name"] != "read_file" {
		t.Fatalf("the frame must name the tool: %v", value)
	}
	if value["error"] != "file not found: /tmp/missing.txt" {
		t.Fatalf("the frame must say why the call failed: %v", value)
	}
}

// ★ The failure is a fact about the **call**, and the call's result still
//
//	travels: the model read it, and the interface shows it. A failure that
//	swallowed the content would hide what the model was told.
func TestTheContentOfAFailedCallStillTravels(t *testing.T) {
	failures := helpers.NewToolFailures()
	failures.Record(helpers.ToolFailure{ToolID: "call-1", Error: "file not found"})

	evs, _, _ := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(toolResultMessage("call-1", "read_file", "[tool error] file not found")),
		exitEvent(),
	}, emit.Options{ToolFailures: failures})

	found := false
	for _, ev := range evs {
		if res, ok := ev.(*events.ToolCallResultEvent); ok {
			found = true
			if res.Content != "[tool error] file not found" {
				t.Fatalf("content = %q, want the text the model read", res.Content)
			}
		}
	}
	if !found {
		t.Fatalf("the result must still travel: %v", typesOf(evs))
	}
}

// ★ The history has to say the same thing as the live stream: a reload that
//
//	lost the failure would show a failed call as a successful one, and the
//	two surfaces would disagree about the same call.
func TestTheArchivedResultCarriesTheFailure(t *testing.T) {
	failures := helpers.NewToolFailures()
	failures.Record(helpers.ToolFailure{ToolID: "call-1", ToolName: "read_file", Error: "file not found"})

	_, arts, _ := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(toolResultMessage("call-1", "read_file", "[tool error] file not found")),
		exitEvent(),
	}, emit.Options{ToolFailures: failures})

	if len(arts.Intermediates) == 0 {
		t.Fatal("the tool result must be archived")
	}
	reason, failed := message.ReadToolError(arts.Intermediates[0])
	if !failed {
		t.Fatal("★ the archived result carries no failure — the history would show a failed call as a successful one")
	}
	if reason != "file not found" {
		t.Fatalf("reason = %q, want the tool's own words", reason)
	}
}

// A call that worked says nothing about failure: the frame is the absence of
// a fact, and it must stay absent.
func TestASuccessfulCallSaysNothing(t *testing.T) {
	failures := helpers.NewToolFailures()

	evs, arts, _ := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(toolResultMessage("call-1", "read_file", "package main\n")),
		exitEvent(),
	}, emit.Options{ToolFailures: failures})

	for _, ev := range evs {
		if custom, ok := ev.(*events.CustomEvent); ok && custom.Name == "tool_failed" {
			t.Fatal("★ a call that worked was announced as failed")
		}
	}
	if len(arts.Intermediates) > 0 && message.IsToolError(arts.Intermediates[0]) {
		t.Fatal("★ a call that worked was archived as failed")
	}
}

// The failure belongs to the call it names: a collector that holds another
// call's failure must not colour this one.
func TestAFailureBelongsToItsOwnCall(t *testing.T) {
	failures := helpers.NewToolFailures()
	failures.Record(helpers.ToolFailure{ToolID: "call-9", Error: "another call's failure"})

	evs, arts, _ := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(toolResultMessage("call-1", "read_file", "package main\n")),
		exitEvent(),
	}, emit.Options{ToolFailures: failures})

	for _, ev := range evs {
		if custom, ok := ev.(*events.CustomEvent); ok && custom.Name == "tool_failed" {
			t.Fatal("★ another call's failure was announced for this one")
		}
	}
	if len(arts.Intermediates) > 0 && message.IsToolError(arts.Intermediates[0]) {
		t.Fatal("★ another call's failure was archived on this one")
	}
}

// customEvent finds the custom frame with the given name.
func customEvent(t *testing.T, evs []events.Event, name string) *events.CustomEvent {
	t.Helper()
	for _, ev := range evs {
		if custom, ok := ev.(*events.CustomEvent); ok && custom.Name == name {
			return custom
		}
	}
	t.Fatalf("no %q frame on the wire: %v", name, typesOf(evs))
	return nil
}

// The frame is a run's fact, and a run's facts travel on the run's channel.
var _ = contract.RunEventToolFailed
