package transport

import (
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"openheron/message"
)

func TestSubagentMessagesAreArchivedWithTheScopeFlag(t *testing.T) {
	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		{
			AgentName: "coder",
			Output: &adk.TypedAgentOutput[*schema.Message]{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role: schema.Assistant, Message: assistantText("专家的正文"),
				},
			},
		},
		exitEvent(),
	}, Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	if len(res.Intermediates) == 0 {
		t.Fatal("the subagent's message must be archived — that is where observability comes from")
	}
	for _, m := range res.Intermediates {
		if !message.IsSubagent(m) {
			t.Fatalf("a subagent message was archived without the scope flag: %+v", m)
		}
		if got := message.MessageAgent(m); got != "coder" {
			t.Fatalf("the message's owner = %q, want coder", got)
		}
	}
}

func TestRootMessagesDoNotCarryTheScopeFlag(t *testing.T) {
	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		{
			AgentName: "main",
			Output: &adk.TypedAgentOutput[*schema.Message]{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role: schema.Assistant, Message: assistantText("根的正文"),
				},
			},
		},
		exitEvent(),
	}, Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	for _, m := range res.Intermediates {
		if message.IsSubagent(m) {
			t.Fatalf("a root message was marked as a subagent one: %+v", m)
		}
	}
}

func TestTheScopeFlagFollowsTheEventNotTheAgentName(t *testing.T) {
	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		{
			AgentName: "other",
			Output: &adk.TypedAgentOutput[*schema.Message]{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role: schema.Assistant, Message: assistantText("子 agent 的正文"),
				},
			},
		},
		exitEvent(),
	}, Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	for _, m := range res.Intermediates {
		if !message.IsSubagent(m) {
			t.Fatalf("a subagent message was archived without the scope flag: %+v", m)
		}
	}
}
