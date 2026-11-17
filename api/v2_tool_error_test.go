package api

import (
	"testing"

	"github.com/ouranoshong/openheron/message"
)

// ★ The history has to show the same failure the live stream showed: a
//
//	reload that lost it would show a failed call as a successful one, and
//	the two surfaces would disagree about the same call.
func TestTheHistoryCarriesTheToolFailure(t *testing.T) {
	msg := message.NewToolResult("call-1", "read_file", "[tool error] file not found: /tmp/missing.txt")
	message.StampToolError(msg, "file not found: /tmp/missing.txt")

	out, ok := toAgUiMessage(msg, 0)
	if !ok {
		t.Fatal("the tool result must survive the conversion")
	}
	reason, present := out.Metadata["toolError"]
	if !present {
		t.Fatalf("the metadata must carry the failure: %v", out.Metadata)
	}
	if reason != "file not found: /tmp/missing.txt" {
		t.Fatalf("toolError = %v, want the reason the call gave", reason)
	}
	if out.ToolCallID != "call-1" {
		t.Fatalf("the result lost its call: %q", out.ToolCallID)
	}
}

// ★ An empty reason is still a failure: the presence of the key is the
//
//	fact, and a call that failed without saying why is still a failed call.
func TestAFailureWithoutAReasonIsStillAFailure(t *testing.T) {
	msg := message.NewToolResult("call-1", "read_file", "[tool error]")
	message.StampToolError(msg, "")

	out, ok := toAgUiMessage(msg, 0)
	if !ok {
		t.Fatal("the tool result must survive the conversion")
	}
	if _, present := out.Metadata["toolError"]; !present {
		t.Fatalf("the metadata must carry the failure: %v", out.Metadata)
	}
}

// A call that worked says nothing about failure.
func TestAnOrdinaryToolResultHasNoFailure(t *testing.T) {
	msg := message.NewToolResult("call-1", "read_file", "package main\n")

	out, ok := toAgUiMessage(msg, 0)
	if !ok {
		t.Fatal("the tool result must survive the conversion")
	}
	if _, has := out.Metadata["toolError"]; has {
		t.Fatalf("the metadata must not carry a failure: %v", out.Metadata)
	}
}
