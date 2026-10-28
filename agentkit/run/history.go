package run

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// normalizeHistory 修好「有 tool_calls 却没有 tool 消息」的历史。
//
// ★ 为什么要在这里修，而不是修产生它的那一端：
//
//	OpenAI 兼容端点对这条是**硬校验**——
//	「An assistant message with 'tool_calls' must be followed by tool messages
//	responding to each 'tool_call_id'」，不满足直接 400。
//	而 history 是**持久化**的（openheronrt.Runtime.archive 无条件 append），
//	所以一条坏 turn 会把会话**永久毒化**：此后每一轮都 400，
//	重开会话、换模型、改配置都救不回来。
//
//	产生孤儿的位置不止一处 —— 未知工具、abort、turn 被抢占、
//	max_iterations 耗尽、SSE 断裂时的中断……每堵一处都会再冒出新的。
//	在读取侧修一次，**同时治好已经坏掉的老会话**；
//	这是「归档优先于呈现」的另一面：坏数据要能带着往前走，不能要求用户删会话。
//
// ★ 补的是**合成的 tool 消息**而不是删掉那条 assistant 消息：
//
//	删掉的话，模型为这次调用做的推理、它已经看到的计划，就一起消失了 ——
//	而那正是「接着说」需要的上下文。合成一条如实说明「没跑成」的结果，
//	既让序列合法，又告诉模型事实。
func normalizeHistory(hist []*schema.Message) []*schema.Message {
	if !historyNeedsRepair(hist) {
		// ★ 快路径：绝大多数轮次历史是干净的，原样返回。
		//   不拷贝是刻意的 —— Build 每轮都调，而历史能有几百条。
		return hist
	}
	return repairHistory(hist)
}

func repairHistory(hist []*schema.Message) []*schema.Message {
	out := make([]*schema.Message, 0, len(hist)+4)
	// pending 是「已发出但还没收到结果」的调用，按 assistant 消息里的顺序存。
	// 用切片而不是 map：一次 tool_calls 通常只有一两条，线性扫比建 map 便宜，
	// 而且天然保序 —— 补出来的消息必须与原调用一一对应。
	var pending []pendingCall

	flush := func() {
		for _, p := range pending {
			out = append(out, schema.ToolMessage(missingResultText(p.name), p.id,
				schema.WithToolName(p.name)))
		}
		pending = pending[:0]
	}

	for _, msg := range hist {
		if msg == nil {
			continue
		}
		if msg.Role == schema.Tool {
			// 孤儿 tool 消息：没有对应的 tool_call。OpenAI 兼容端点同样会拒。
			i := pendingIndex(pending, msg.ToolCallID)
			if i < 0 {
				continue
			}
			pending = append(pending[:i], pending[i+1:]...)
			out = append(out, msg)
			continue
		}

		// 遇到任何非 tool 消息之前，先把上一批没等到结果的调用补齐 ——
		// 顺序反了的话，合成消息会落在错误的 assistant 消息后面。
		flush()
		out = append(out, msg)
		for _, tc := range msg.ToolCalls {
			pending = append(pending, pendingCall{id: tc.ID, name: tc.Function.Name})
		}
	}
	flush()
	return out
}

// pendingCall 是一个「模型已经发起、结果还没回来」的调用。
type pendingCall struct {
	id   string
	name string
}

func pendingIndex(p []pendingCall, id string) int {
	for i, c := range p {
		if c.id == id {
			return i
		}
	}
	return -1
}

// historyNeedsRepair 判断历史是否会被端点拒。
//
// ★ 单遍，且 map 延迟到第一条 tool_call 才建：没有工具调用的历史
//
//	（最常见的那种）一路不分配。
func historyNeedsRepair(hist []*schema.Message) bool {
	var pending map[string]bool
	unmatched := 0
	for _, msg := range hist {
		if msg == nil {
			// ★ nil 必须在这里就判：快路径原样返回切片，nil 会漏到下游。
			return true
		}
		if msg.Role == schema.Tool {
			if !pending[msg.ToolCallID] {
				// 孤儿，或者排在它该回答的那条 assistant 消息之外 —— 都要修。
				return true
			}
			delete(pending, msg.ToolCallID)
			unmatched--
			continue
		}
		if unmatched > 0 {
			// 上一批 tool_call 还没等到结果，就又来了一条非 tool 消息。
			return true
		}
		for _, tc := range msg.ToolCalls {
			if pending == nil {
				pending = make(map[string]bool, len(msg.ToolCalls))
			}
			pending[tc.ID] = true
			unmatched++
		}
	}
	return unmatched > 0
}

// missingResultText 是补出来的工具结果文案。
//
// ★ 英文：它会被模型读到，而按 AGENTS.md 的语言约定，
//
//	凡是运行时输出（日志、错误、工具结果）一律英文。
//
// ★ 措辞刻意只说事实、**不猜原因**：「没跑成」是能证实的，
//
//	「因为 abort / 因为 400 / 因为被拒绝」都是猜测，而模型会照着猜出来的
//	原因往下编 —— 比如认定「工具被拒了」于是换条绕过路线。
func missingResultText(name string) string {
	tool := name
	if tool == "" {
		tool = "the tool"
	}
	return fmt.Sprintf(
		"[no result] %q did not run: the turn ended before its result came back. "+
			"Treat it as not executed — do not assume anything about what it would have returned, "+
			"and do not call it again in this turn.", tool)
}

// toolCallNamesOf 是给测试与自省用的读口：按出现顺序列出历史里全部 tool_call 名。
func toolCallNamesOf(hist []*schema.Message) []string {
	var out []string
	for _, msg := range hist {
		if msg == nil {
			continue
		}
		for _, tc := range msg.ToolCalls {
			out = append(out, tc.Function.Name)
		}
	}
	return out
}

// describeHistory 排错用：把历史压成一行角色序列。
func describeHistory(hist []*schema.Message) string {
	parts := make([]string, 0, len(hist))
	for _, msg := range hist {
		if msg == nil {
			parts = append(parts, "nil")
			continue
		}
		switch {
		case msg.Role == schema.Tool:
			parts = append(parts, "tool("+msg.ToolCallID+")")
		case len(msg.ToolCalls) > 0:
			parts = append(parts, "assistant+calls("+strings.Join(toolCallNamesOf([]*schema.Message{msg}), ",")+")")
		default:
			parts = append(parts, string(msg.Role))
		}
	}
	return strings.Join(parts, " | ")
}
