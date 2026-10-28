package transport

import (
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"openheron/message"
)

// ── 子 agent 的产出：归档 + 打作用域标记 ─────────────────────────────────

// ★ 子 agent 的事件进了父的流（EmitInternalEvents 打开之后），于是它们
//
//	**照样归档** —— 那是可观测性的来源。但必须打上**作用域**标记：
//	下一轮建上下文时靠它把专家的过程剔出父的上下文。
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

// ★ 根 agent 的消息**不能**带这个标记 —— 隔离的方向是单向的：
//
//	剔的是专家的过程，根自己的话一个字都不能少。
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

// ★ 判据是**事件来自非根 agent**，不是「agent 名不等于某个值」——
//
//	会话可以换根 agent，换过之后旧根的消息看起来与根的毫无区别。
//	而「产生在委派内部」是这一轮的结构事实，与谁在当根无关。
func TestTheScopeFlagFollowsTheEventNotTheAgentName(t *testing.T) {
	// 一个**曾经是根**的 agent（"main"）在委派内部产出 —— 它仍被标记。
	_, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		{
			AgentName: "other", // 本轮的根是 "main"，所以 "other" 是子 agent
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
