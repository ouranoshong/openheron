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

// assertAnswerable 是本文件所有用例的共同判据：**每个 tool_call 都必须被回答**。
//
// ★ 断言「形态」而不是「内容」：端点校验的就是这条不变式，
//
//	而内容随修复策略变（补消息 vs 删消息），形态不变。
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
		// 非 tool 消息之前，上一批 tool_call 必须都已回答。
		checkPending("message " + strconv.Itoa(i))
		for _, tc := range msg.ToolCalls {
			answered[tc.ID] = false
		}
	}
	// 序列末尾同样不能留悬空的 tool_call。
	checkPending("the tail")
}

// TestNormalizeHistoryFillsTheMissingResult 是这个文件存在的理由。
//
// ★ 复现的是真机上那条 400 的形状：
//
//	An assistant message with 'tool_calls' must be followed by tool messages
//	responding to each 'tool_call_id'. (insufficient tool messages following
//	tool_calls message)
//
//	成因是上一轮的工具**没跑出结果**就结束了（未知工具、abort、抢占…），
//	而 archive 无条件把 assistant 消息落盘 —— 于是这条历史**永久**不合法。
func TestNormalizeHistoryFillsTheMissingResult(t *testing.T) {
	hist := []*schema.Message{
		schema.UserMessage("find it"),
		assistantWithCalls("c1"),
		// ← 少了 c1 的 tool 消息
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
	// ★ 不能编造原因：模型会照着编出来的原因往下推。
	if strings.Contains(got[2].Content, "abort") ||
		strings.Contains(got[2].Content, "rejected") ||
		strings.Contains(got[2].Content, "denied") {
		t.Fatalf("synthesized result must not guess a cause: %q", got[2].Content)
	}
	if !strings.Contains(got[2].Content, "not executed") {
		t.Fatalf("synthesized result must say it did not run: %q", got[2].Content)
	}
}

// TestNormalizeHistoryDropsOrphanToolMessages 覆盖另一种 400 形状：
// 没有对应 tool_call 的 tool 消息（中断恢复写重、游标错位都会产生）。
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

// TestNormalizeHistoryFillsOnlyTheMissingOnes 一条 assistant 消息发两个调用、
// 只回来一个 —— 补的那个不能顶掉已有的。
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

// TestNormalizeHistoryLeavesCleanHistoryAlone 守住快路径。
//
// ★ 干净历史必须**原样返回同一个切片**（不是等值副本）：
//
//	Build 每轮都调，历史能有几百条；每次拷贝是白付的钱。
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

// TestNormalizeHistoryHandlesSeveralBadTurns 一个会话里可能不止一处坏。
// 逐条修完才是真的能救活老会话 —— 只修第一处的话，第二次提问还是 400。
func TestNormalizeHistoryHandlesSeveralBadTurns(t *testing.T) {
	hist := []*schema.Message{
		schema.UserMessage("q1"),
		assistantWithCalls("c1"), // 坏：没结果
		schema.UserMessage("q2"),
		assistantWithCalls("c2", "c3"),
		toolResult("c2", "ok"),
		schema.AssistantMessage("mid", nil),
		assistantWithCalls("c4"), // 坏：没结果
	}
	got := normalizeHistory(hist)
	assertAnswerable(t, got)

	// ★ 每一条坏 turn 都要各自补上，不是只补最后一条。
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

// TestNormalizeHistorySkipsNil Build 的调用方可能塞 nil 进来。
func TestNormalizeHistorySkipsNil(t *testing.T) {
	got := normalizeHistory([]*schema.Message{nil, schema.UserMessage("q"), nil})
	assertAnswerable(t, got)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %s", len(got), describeHistory(got))
	}
}

// TestBuildRunsHistoryThroughNormalize 是接线测试：修好的历史必须真的进了
// agent input。上面几条测的是纯函数，漏掉调用点的话照样全绿。
func TestBuildRunsHistoryThroughNormalize(t *testing.T) {
	c := InputConfig{ProjectRoot: "/tmp/x"}
	in, err := c.buildInput(context.Background(), inputParams{
		History: []*schema.Message{
			schema.UserMessage("q"),
			assistantWithCalls("c1"),
		},
	}, "next")
	if err != nil {
		t.Fatal(err)
	}
	// 第一条是 system prompt，不参与 tool_call 配平。
	assertAnswerable(t, in.Messages[1:])
}
