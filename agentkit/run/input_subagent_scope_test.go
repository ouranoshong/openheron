package run

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"

	"openheron/message"
)

// ── 子 agent 的中间过程：留档，但不进父的上下文 ───────────────────────────

// ★ 这条守的是「Agent 即工具」那条隔离线：
//
//	专家的中间过程**留在历史里**（可观测 —— 界面能看到它读了什么、
//	按 agent 分账能分出它的 token），但**不进父 agent 的下一轮上下文**。
//
//	隔离靠的是这一步，不是靠「不记录」。
func TestSubagentMessagesAreKeptOutOfTheModelInput(t *testing.T) {
	c := InputConfig{ProjectRoot: "/tmp/x"}

	// 父：调了一次 task，拿到专家的总结。
	parentCall := assistantWithCalls("c1")
	parentResult := schema.ToolMessage("专家的总结", "c1", schema.WithToolName("task"))

	// 专家：自己的推理、自己的工具调用、自己的工具返回。
	expertCall := assistantWithCalls("c2")
	expertResult := schema.ToolMessage("read_file 的原始输出", "c2", schema.WithToolName("read_file"))
	for _, m := range []*schema.Message{expertCall, expertResult} {
		message.StampAgent(m, "task")
		message.StampSubagent(m)
	}

	in, err := c.buildInput(context.Background(), inputParams{
		History: []*schema.Message{
			schema.UserMessage("q"),
			parentCall,
			expertCall,
			expertResult,
			parentResult,
		},
	}, "next")
	if err != nil {
		t.Fatal(err)
	}

	// 第一条是 system prompt。
	got := in.Messages[1:]
	for _, m := range got {
		if message.IsSubagent(m) {
			t.Fatalf("a subagent message leaked into the model input: %+v", m)
		}
		if m == expertCall || m == expertResult {
			t.Fatal("the expert's own messages must not reach the parent's context")
		}
	}
	// ★ 但父自己的那对（assistant(tool_calls) → tool 结果）必须还在，
	//   而且配对仍然合法 —— 剔的是专家的整组，不是把配对拆散。
	//   末尾多出来的那条 user 是本轮的新问题。
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4 (user + parent call + parent result + new query): %s",
			len(got), describeHistory(got))
	}
	assertAnswerable(t, got)
}

// ★ 剔专家的消息**不能**打乱父的配对 —— 专家的过程夹在父的
//
//	assistant(tool_calls=[task]) 与它的 tool 结果之间。
func TestRemovingSubagentMessagesKeepsTheParentsPairsIntact(t *testing.T) {
	c := InputConfig{ProjectRoot: "/tmp/x"}

	parentCall := assistantWithCalls("c1")
	parentResult := schema.ToolMessage("专家的总结", "c1", schema.WithToolName("task"))
	expertCall := assistantWithCalls("c2")
	expertResult := schema.ToolMessage("read_file 的原始输出", "c2", schema.WithToolName("read_file"))
	for _, m := range []*schema.Message{expertCall, expertResult} {
		message.StampSubagent(m)
	}

	in, err := c.buildInput(context.Background(), inputParams{
		History: []*schema.Message{
			schema.UserMessage("q"),
			parentCall,
			expertCall,
			expertResult,
			parentResult,
		},
	}, "next")
	if err != nil {
		t.Fatal(err)
	}
	got := in.Messages[1:]

	// 父的 assistant(tool_calls=[c1]) 后面必须紧跟它的 tool 结果。
	var sawCall bool
	for _, m := range got {
		if len(m.ToolCalls) > 0 {
			sawCall = true
			continue
		}
		if sawCall {
			if m.ToolCallID != "c1" {
				t.Fatalf("the parent's tool result was lost or mismatched: %+v", m)
			}
			break
		}
	}
	assertAnswerable(t, got)
}

// ★ 过滤必须**跨轮**：专家的过程在任何一轮都不该进父的上下文。
//
//	这正是「按 agent 名过滤」做不到的 —— 会话可以换根 agent，
//	换过之后旧根的消息看起来与根 agent 的毫无区别。
func TestSubagentMessagesAreFilteredInEveryTurn(t *testing.T) {
	c := InputConfig{ProjectRoot: "/tmp/x"}
	expert := schema.AssistantMessage("专家的中间推理", nil)
	message.StampAgent(expert, "main") // 旧根 agent 产出的
	message.StampSubagent(expert)

	for i := 0; i < 3; i++ {
		in, err := c.buildInput(context.Background(), inputParams{
			History: []*schema.Message{schema.UserMessage("q"), expert},
		}, "next")
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range in.Messages {
			if message.IsSubagent(m) {
				t.Fatalf("turn %d: a subagent message leaked into the model input", i)
			}
		}
	}
}

// withoutSubagentMessages 不改原切片 —— Build 每轮都调，
// 而历史能有几百条；就地改会踩到归档路径正在读的那一份。
func TestWithoutSubagentMessagesDoesNotMutateTheInput(t *testing.T) {
	kept := schema.UserMessage("q")
	expert := schema.AssistantMessage("专家的", nil)
	message.StampSubagent(expert)
	hist := []*schema.Message{kept, expert}

	out := withoutSubagentMessages(hist)
	if len(out) != 1 || out[0] != kept {
		t.Fatalf("out = %d messages, want the single root message", len(out))
	}
	if len(hist) != 2 {
		t.Fatalf("the input slice was mutated: %d messages, want 2", len(hist))
	}
}
