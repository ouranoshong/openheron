package transport

import (
	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"openheron/message"
)

// agentSegment 造一个 agent 地址段。
func agentSegment(name string) adk.AddressSegment {
	return adk.AddressSegment{Type: adk.AddressSegmentAgent, ID: name}
}

// toolSegment 造一个工具地址段。
func toolSegment(name string) adk.AddressSegment {
	return adk.AddressSegment{Type: adk.AddressSegmentTool, ID: name}
}

// parallelInterruptEvent 造一个「并行子 agent 同时被拦下」的中断事件。
//
// ★ 形状必须与框架一致：两个子 agent 的中断被合成**一个**事件，
//
//	每个 context 带着**自己的**地址链（eino adk/chatmodel.go:970 把子信号
//	收成 composite，再由 core.ToInterruptContexts 摊平成一组 root cause）。
func parallelInterruptEvent() *adk.TypedAgentEvent[*schema.Message] {
	return &adk.TypedAgentEvent[*schema.Message]{
		// 事件是**根** agent 发的 —— 这正是归属必须另寻出处的理由。
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

// ★ 并行下最关键的一条：两个子 agent 同时要审批，两张卡的请求方**各归各的**。
//
// 旧实现把整个事件共用一个 `sub`（事件的 AgentName，即入口 agent）——
// 于是两张卡要么都显示成 main，要么都显示成同一个子 agent。
func TestParallelInterruptsKeepTheirOwnRequester(t *testing.T) {
	evs, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{parallelInterruptEvent()},
		Options{RootAgent: "main"})
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

	// 持久化的待批详情也要带请求方 —— 历史回放只有它，没有实时事件。
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

// 入口 agent 自己发起的调用不标归属 —— 与实时事件的规则一致。
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

	evs, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{ev}, Options{RootAgent: "main"})
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

// 没有地址链的老数据（升级前落盘的 checkpoint）不该崩，也不该编出请求方。
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

	evs, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{ev}, Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	fin := evs[1].(*events.RunFinishedEvent)
	if got := fin.Outcome.Interrupts[0].SubagentRunID; got != "" {
		t.Fatalf("an interrupt without an address must not be attributed, got %q", got)
	}
}

// ★ 只有工具调用、没有正文时，消息也必须带归属。
//
// 工具卡是这条消息**唯一**可见的内容 —— 「这个 agent 调了什么工具」
// 恰恰是多 agent 下最该看见的东西。而前端是由 TOOL_CALL_START 顺带
// 建出这条消息的，那个事件不带归属 → 不补声明的话它是匿名的。
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
	}, Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	// 声明 = 一对 TEXT_MESSAGE_START/END，且 START 带 name。
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

// ★ 正文的 TEXT_MESSAGE_START 必须带 name —— 那是「这条消息属于谁」的
//
//	唯一来源（前端据此建出消息并记下 agent）。
//
//	现有的契约测试只钉了 subagentRunId（协议层的「子 agent 运行 id」），
//	而前端过滤用的是 name —— 两者不是一回事。
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
	}, Options{RootAgent: "main"})
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

// 工具调用要挂在**产出它的那条消息**上（parentMessageId）——
// 前端据此把工具卡并进那条消息，归属随之继承。
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
	}, Options{RootAgent: "main"})
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

// ★ 中间消息要带**产生时刻** —— 并行委派下它是「谁先谁后」的唯一依据。
//
// 归档是一轮结束时一次性做的，若在归档时打戳，一轮内所有消息的时间几乎
// 相同，交错顺序的信息就整个丢了。
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
	}, Options{RootAgent: "main"})
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

// 中间消息要带归属标记 —— 分账与「按 agent 过滤历史」都靠它。
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
	}, Options{RootAgent: "main"})
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

// 工具结果也要带归属 —— 「task 改了哪些文件」靠的就是这一条。
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
	}, Options{RootAgent: "main"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if got := message.MessageAgent(res.Intermediates[0]); got != "researcher" {
		t.Fatalf("the tool result's agent = %q, want researcher", got)
	}
}
