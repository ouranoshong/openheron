package run

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// rejectUnanswerableToolCalls 复刻 OpenAI 兼容端点的那条硬校验。
//
// ★ 为什么要照抄端点的措辞：它决定了「历史长什么样才算合法」，
//
//	而这条不变式不是 openheron 定的。openheron 只需要保证**送出去之前**满足它。
//	各家措辞略有出入（有的还额外要求工具消息紧邻），但这一条是一致的。
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

// TestHistorySatisfiesTheEndpointContract 才是真正的验收：
// 同一份坏历史，修复前被端点拒、修复后通过。
//
// ★ 单测 normalizeHistory 的形态是不够的 —— 形态对了但仍可能被端点拒，
//
//	而这里直接跑端点那条不变式。
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
			// 前置：这确实是端点会拒的形状，否则这条用例什么都没证明。
			if err := rejectUnanswerableToolCalls(hist); err == nil {
				t.Fatalf("precondition failed: the endpoint would accept this history as-is, "+
					"so the case proves nothing: %s", describeHistory(hist))
			}

			in, err := InputConfig{}.buildInput(context.Background(), inputParams{History: hist}, "next")
			if err != nil {
				t.Fatal(err)
			}
			if err := rejectUnanswerableToolCalls(in.Messages); err != nil {
				t.Fatalf("the endpoint would still reject it: %v\n%s", err, describeHistory(in.Messages))
			}
		})
	}
}

// TestNormalizeHistoryDoesNotFabricateResults 守住一个容易写错的点：
//
// 补出来的消息必须**如实说没跑成**，不能编一个看起来合理的结果。
// 编出来的结果会让模型照着不存在的事实往下推 —— 那比 400 更难查。
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
