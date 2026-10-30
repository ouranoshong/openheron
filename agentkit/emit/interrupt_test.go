package emit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/approval"
)

func TestTheInterruptDescribesTheApprovalInfo(t *testing.T) {
	ev := &adk.TypedAgentEvent[*schema.Message]{
		AgentName: "main",
		Action: &adk.AgentAction{Interrupted: &adk.InterruptInfo{
			InterruptContexts: []*adk.InterruptCtx{{
				ID: "ic-9a1", IsRootCause: true,
				Info: &approval.ApprovalInfo{
					ToolName:        "write_file",
					ArgumentsInJSON: `{"file_path":"/tmp/probe.txt","content":"hi"}`,
				},
			}},
		}},
	}

	got := interruptsFrom(ev, "")
	if len(got) != 1 {
		t.Fatalf("want 1 interrupt, got %d", len(got))
	}
	if strings.Contains(got[0].Message, "&{") || strings.Contains(got[0].Message, "ArgumentsInJSON") {
		t.Fatalf("interrupt message leaked the Go struct: %q", got[0].Message)
	}
	if got[0].Message != "tool write_file requires approval" {
		t.Fatalf("unexpected message: %q", got[0].Message)
	}
	if got[0].Metadata["toolName"] != "write_file" {
		t.Fatalf("metadata.toolName = %v, want write_file", got[0].Metadata["toolName"])
	}
	args, ok := got[0].Metadata["arguments"].(json.RawMessage)
	if !ok || !strings.Contains(string(args), `"file_path":"/tmp/probe.txt"`) {
		t.Fatalf("metadata.arguments = %v, want the raw tool arguments", got[0].Metadata["arguments"])
	}
}
