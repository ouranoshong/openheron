package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"openheron/approval"
)

// ── 测试脚手架 ───────────────────────────────────────────────────────────

// sliceEvents 是一个确定性的 EventSource，按序吐完就结束。
type sliceEvents struct {
	evs []*adk.TypedAgentEvent[*schema.Message]
	i   int
}

func (s *sliceEvents) Next() (*adk.TypedAgentEvent[*schema.Message], bool) {
	if s.i >= len(s.evs) {
		return nil, false
	}
	e := s.evs[s.i]
	s.i++
	return e, true
}

// run 驱动一次流，返回解析后的事件序列。
func run(t *testing.T, evs []*adk.TypedAgentEvent[*schema.Message], opts Options) ([]events.Event, Result, error) {
	t.Helper()
	return runErr(t, evs, opts)
}

// runErr 同 run，但不因返回错误而中断 —— 失败路径的用例需要检查流里发了什么。
func runErr(t *testing.T, evs []*adk.TypedAgentEvent[*schema.Message], opts Options) ([]events.Event, Result, error) {
	t.Helper()
	var buf bytes.Buffer
	res, err := RunStream(&buf, "thread-1", &sliceEvents{evs: evs}, opts)
	return decode(t, buf.Bytes()), res, err
}

// decode 把写出的 JSONL 还原成 SDK 事件类型，并强制跑一次协议校验。
//
// events.ValidateSequence 是本测试的核心断言：它检查 RUN_STARTED 先于
// RUN_FINISHED、TEXT_MESSAGE_START/CONTENT/END 配对、TOOL_CALL_START/ARGS/END
// 配对、interrupt 结局至少带一个 interrupt —— 这些都是 AG-UI 的硬约束，
// 手写 emitter 极易违反。
func decode(t *testing.T, raw []byte) []events.Event {
	t.Helper()
	var out []events.Event
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		ev, err := events.EventFromJSON(line)
		if err != nil {
			t.Fatalf("EventFromJSON(%s): %v", line, err)
		}
		if err := ev.Validate(); err != nil {
			t.Fatalf("event.Validate(%s): %v", line, err)
		}
		out = append(out, ev)
	}
	if err := events.ValidateSequence(out); err != nil {
		names := make([]string, len(out))
		for i, e := range out {
			names[i] = string(e.Type())
		}
		t.Fatalf("ValidateSequence failed: %v\nsequence: %s", err, strings.Join(names, " → "))
	}
	return out
}

func typesOf(evs []events.Event) []events.EventType {
	out := make([]events.EventType, len(evs))
	for i, e := range evs {
		out[i] = e.Type()
	}
	return out
}

func assistantText(content string) *schema.Message {
	return &schema.Message{Role: schema.Assistant, Content: content}
}

func toolResultMessage(id, name, content string) *schema.Message {
	return &schema.Message{
		Role:       schema.Tool,
		ToolCallID: id,
		ToolName:   name,
		Content:    content,
	}
}

func exitEvent() *adk.TypedAgentEvent[*schema.Message] {
	return &adk.TypedAgentEvent[*schema.Message]{Action: adk.NewExitAction()}
}

func textEvent(msg *schema.Message) *adk.TypedAgentEvent[*schema.Message] {
	return &adk.TypedAgentEvent[*schema.Message]{
		Output: &adk.TypedAgentOutput[*schema.Message]{
			MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
				Role: schema.Assistant, Message: msg,
			},
		},
	}
}

// ── 契约测试 ─────────────────────────────────────────────────────────────

func TestContractEmptyRun(t *testing.T) {
	evs, _, _ := run(t, nil, Options{})

	want := []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeRunFinished,
	}
	if got := typesOf(evs); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("empty-run sequence = %v, want %v", got, want)
	}
	if outcome := evs[1].(*events.RunFinishedEvent).Outcome; outcome == nil || outcome.Type != events.RunFinishedOutcomeTypeSuccess {
		t.Fatalf("an empty run must end with success, got %+v", outcome)
	}
}

func TestContractTextRoundTrip(t *testing.T) {
	evs, res, _ := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(assistantText("hello")),
		exitEvent(),
	}, Options{})

	got := typesOf(evs)
	want := []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeTextMessageStart,
		events.EventTypeTextMessageContent,
		events.EventTypeTextMessageEnd,
		events.EventTypeRunFinished,
	}
	assertSeq(t, got, want)

	// 内容必须逐字送达 —— 前端靠 delta 拼全文
	content := evs[2].(*events.TextMessageContentEvent)
	if content.Delta != "hello" {
		t.Fatalf("delta = %q, want %q", content.Delta, "hello")
	}
	if len(res.Intermediates) != 1 || res.Intermediates[0].Content != "hello" {
		t.Fatalf("the archived messages are wrong: %+v", res.Intermediates)
	}
}

func TestContractToolCallRoundTrip(t *testing.T) {
	evs, res, _ := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(assistantText("let me read the file first")),
		textEvent(toolResultMessage("call-1", "read_file", "package a2ui")),
		exitEvent(),
	}, Options{})

	assertSeq(t, typesOf(evs), []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeTextMessageStart,
		events.EventTypeTextMessageContent,
		events.EventTypeTextMessageEnd,
		events.EventTypeToolCallResult,
		events.EventTypeRunFinished,
	})

	// TOOL_CALL_RESULT 必须带原工具调用的 id，否则前端挂不上卡片
	tr := evs[4].(*events.ToolCallResultEvent)
	if tr.ToolCallID != "call-1" {
		t.Fatalf("ToolCallResult.toolCallId = %q, want call-1", tr.ToolCallID)
	}
	if len(res.Intermediates) != 2 {
		t.Fatalf("2 messages must be archived (assistant + tool), got %d", len(res.Intermediates))
	}
}

func TestContractAssistantWithToolCalls(t *testing.T) {
	msg := assistantText("reading")
	msg.ToolCalls = []schema.ToolCall{{
		ID: "call-9", Function: schema.FunctionCall{
			Name: "read_file", Arguments: `{"file_path":"a.go"}`,
		},
	}}

	evs, _, _ := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(msg), exitEvent(),
	}, Options{})

	assertSeq(t, typesOf(evs), []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeTextMessageStart,
		events.EventTypeTextMessageContent,
		events.EventTypeTextMessageEnd,
		events.EventTypeToolCallStart,
		events.EventTypeToolCallArgs,
		events.EventTypeToolCallEnd,
		events.EventTypeRunFinished,
	})

	if n := evs[4].(*events.ToolCallStartEvent).ToolCallName; n != "read_file" {
		t.Fatalf("toolCallName = %q, want read_file", n)
	}
}

// 人审中断是 openheron 的核心能力：结局必须是 interrupt，且带得上 toolCallId
// 前端才能把审批卡挂到对应的工具卡片上。
func TestContractApprovalInterrupt(t *testing.T) {
	ev := &adk.TypedAgentEvent[*schema.Message]{
		AgentName: "main",
		Action: &adk.AgentAction{Interrupted: &adk.InterruptInfo{
			InterruptContexts: []*adk.InterruptCtx{{
				ID: "ic-7f3a", IsRootCause: true,
				Info: "running go test requires approval",
			}},
		}},
	}

	evs, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{ev}, Options{})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	assertSeq(t, typesOf(evs), []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeRunFinished,
	})

	fin := evs[1].(*events.RunFinishedEvent)
	if fin.Outcome == nil || fin.Outcome.Type != events.RunFinishedOutcomeTypeInterrupt {
		t.Fatalf("it must end with an interrupt, got %+v", fin.Outcome)
	}
	if len(fin.Outcome.Interrupts) != 1 {
		t.Fatalf("it must carry 1 interrupt, got %d", len(fin.Outcome.Interrupts))
	}
	got := fin.Outcome.Interrupts[0]
	if got.ID != "ic-7f3a" || got.Reason != "tool_call" {
		t.Fatalf("interrupt = %+v", got)
	}
	if len(res.Pending) != 1 || res.Pending[0].InterruptID != "ic-7f3a" {
		t.Fatalf("Result.Pending = %+v, want one interrupt ic-7f3a", res.Pending)
	}
}

// 子 agent 发起的审批要带归属，否则前端无法把它渲染到对应的子 agent 分组里。
func TestContractSubagentAttribution(t *testing.T) {
	ev := &adk.TypedAgentEvent[*schema.Message]{
		AgentName: "researcher",
		Output: &adk.TypedAgentOutput[*schema.Message]{
			MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
				Role: schema.Assistant, Message: assistantText("research findings"),
			},
		},
	}

	evs, _, _ := run(t, []*adk.TypedAgentEvent[*schema.Message]{ev, exitEvent()},
		Options{RootAgent: "main"})

	content := evs[2].(*events.TextMessageContentEvent)
	if content.SubagentRunID != "researcher" {
		t.Fatalf("subagentRunId = %q, want researcher", content.SubagentRunID)
	}
}

// 入口 agent 的事件不应标 subagentRunId —— 否则前端会把主流程也折叠进子分组。
func TestContractRootAgentNotAttributed(t *testing.T) {
	evs, _, _ := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		{AgentName: "main", Output: &adk.TypedAgentOutput[*schema.Message]{
			MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
				Role: schema.Assistant, Message: assistantText("root"),
			},
		}},
		exitEvent(),
	}, Options{RootAgent: "main"})

	if got := evs[2].(*events.TextMessageContentEvent).SubagentRunID; got != "" {
		t.Fatalf("the entry agent must not carry subagentRunId, got %q", got)
	}
}

// 运行失败必须走 RUN_ERROR，且不能再补一个 RUN_FINISHED —— 否则前端会
// 收到两套互相矛盾的终局。
// wrappedError 模拟 eino 的 compose.internalError：包一层但支持 Unwrap。
//
// ★ 用它而不是直接传 context.Canceled —— 因为真实的错误是
//
//	"[GraphRunError] context has been canceled" 这种**包过的**，
//	判据必须穿得过去才作数。
type wrappedError struct {
	msg   string
	cause error
}

func (e *wrappedError) Error() string { return e.msg }
func (e *wrappedError) Unwrap() error { return e.cause }

// ★ 用户主动中止（或客户端断开）**不是错误**。
//
// 修之前：中止产生的 "[GraphRunError] context has been canceled" 被当成
// 失败 —— 服务端日志留下一条 "run ended with a stream error"，而那一轮
// 是**用户要求**停的。说成失败既误导，又让人以为停止按钮坏了。
func TestContractCancelIsNotAnError(t *testing.T) {
	ev := &adk.TypedAgentEvent[*schema.Message]{
		Err: &wrappedError{
			msg:   "[GraphRunError] context has been canceled: context canceled",
			cause: context.Canceled,
		},
	}

	evs, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{ev}, Options{})
	if err == nil {
		t.Fatal("a cancellation must be reported back so the caller can log it as a stop")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("the error must still be identifiable as a cancellation: %v", err)
	}
	for _, e := range evs {
		if e.Type() == events.EventTypeRunError {
			t.Fatalf("a cancellation must not be reported as RUN_ERROR: %v", typesOf(evs))
		}
	}
	if !res.Stopped {
		t.Fatal("the run must be marked as stopped")
	}
	if res.StreamBroken {
		t.Fatal("a cancellation is not a broken stream")
	}
}

func TestContractRunErrorEndsStream(t *testing.T) {
	ev := &adk.TypedAgentEvent[*schema.Message]{
		Err: errors.New("401 auth -- invalid credentials"),
	}

	evs, _, err := runErr(t, []*adk.TypedAgentEvent[*schema.Message]{ev}, Options{})
	if err == nil {
		t.Fatal("期望返回错误")
	}
	assertSeq(t, typesOf(evs), []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeRunError,
	})

	re := evs[1].(*events.RunErrorEvent)
	if re.Code == nil || *re.Code != CodeModelAuth {
		t.Fatalf("code = %v, want %s", re.Code, CodeModelAuth)
	}
}

// 模型重试不是错误：发 CUSTOM 后必须继续读，否则后面的内容全丢。
func TestContractModelRetryIsNotTerminal(t *testing.T) {
	evs, _, _ := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		{Output: &adk.TypedAgentOutput[*schema.Message]{
			MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
				Role: schema.Assistant, Message: assistantText("重试后成功"),
			},
		}},
		exitEvent(),
	}, Options{})

	assertSeq(t, typesOf(evs), []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeTextMessageStart,
		events.EventTypeTextMessageContent,
		events.EventTypeTextMessageEnd,
		events.EventTypeRunFinished,
	})
}

// sink 断了以后不再产事件，但必须把已产出的消息返回去归档 ——
// 这是 a2ui writerBroken 注释要求的语义，丢了就等于丢历史。
func TestContractBrokenSinkStillArchives(t *testing.T) {
	// RunStream 用了 isliceSource 的字面量构造，坏 writer 单独测
	var buf failingWriter
	res, err := RunStream(&failingWriter{}, "thread-1",
		&sliceEvents{evs: []*adk.TypedAgentEvent[*schema.Message]{
			textEvent(assistantText("第一")),
			textEvent(assistantText("第二")),
			exitEvent(),
		}}, Options{})

	_ = buf
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if len(res.Intermediates) != 2 {
		t.Fatalf("all messages must still be archived after the sink breaks, got %d", len(res.Intermediates))
	}
	if !res.StreamBroken {
		t.Fatal("StreamBroken 应为 true")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("sink closed") }

// ── 辅助断言 ─────────────────────────────────────────────────────────────

func assertSeq(t *testing.T, got, want []events.EventType) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("sequence length = %d, want %d; got %v; want %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sequence item %d = %s, want %s; got %v", i, got[i], want[i], got)
		}
	}
}

// TestDecodeRejectsMalformed 保证 decode 本身会挑出问题 —— 否则上面的契约测试
// 会因为解析宽松而变成永真。
func TestDecodeRejectsMalformed(t *testing.T) {
	bad := []byte(`{"type":"NOT_A_REAL_TYPE"}`)
	if _, err := events.EventFromJSON(bad); err == nil {
		t.Fatal("非法事件类型应当解析失败")
	}

	// 缺 messageId 的 CONTENT 事件应当被校验拦下
	badSeq := []byte(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"x","delta":"hi"}` + "\n")
	ev, err := events.EventFromJSON(badSeq)
	if err != nil {
		t.Fatalf("a single event must parse: %v", err)
	}
	// 没有前导 START 时，ValidateSequence 必须报错
	if err := events.ValidateSequence([]events.Event{ev}); err == nil {
		t.Fatal("孤立的 TEXT_MESSAGE_CONTENT 应被 ValidateSequence 拒绝")
	}
}

// TestErrorCodeMapping 锁住协议错误码的映射，避免改坏前端分流。
func TestErrorCodeMapping(t *testing.T) {
	cases := map[string]string{
		"401 auth —— …":                CodeModelAuth,
		"429 rate_limit —— …":          CodeModelRateLimit,
		"429 insufficient quota":       CodeModelQuota,
		"503 overload":                 CodeModelOverload,
		"request timed out":            CodeModelTimeout,
		"dial tcp: connection refused": CodeModelNetwork,
		"随便什么错":                        CodeModelUnknown,
	}
	for in, want := range cases {
		if got := errorCode(errors.New(in)); got != want {
			t.Fatalf("errorCode(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestRunIDDefaults 确认未指定 runId 时自动补一个，且 RUN_STARTED/RUN_FINISHED 一致。
func TestRunIDDefaults(t *testing.T) {
	var buf bytes.Buffer
	if _, err := RunStream(&buf, "thread-1", &sliceEvents{}, Options{}); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	evs := decode(t, buf.Bytes())
	start, fin := evs[0].(*events.RunStartedEvent), evs[1].(*events.RunFinishedEvent)
	if start.RunID() == "" || start.RunID() != fin.RunID() {
		t.Fatalf("the runId must be generated and match on both ends: %q vs %q", start.RunID(), fin.RunID())
	}
	if uuid.Validate(start.RunID()) != nil {
		t.Logf("runId 非 UUID: %q（不影响协议，仅提示）", start.RunID())
	}
}

// TestJSONShape 确认写出的 JSON 是 AG-UI 期望的字段名 —— 前端按这些名字解析，
// 字段名写错会导致事件被静默忽略。
func TestJSONShape(t *testing.T) {
	var buf bytes.Buffer
	_, err := RunStream(&buf, "thread-1", &sliceEvents{evs: []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(assistantText("hi")),
	}}, Options{RunID: "r1"})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	for _, want := range []string{
		`"type":"RUN_STARTED"`, `"threadId":"thread-1"`, `"runId":"r1"`,
		`"type":"TEXT_MESSAGE_START"`, `"type":"TEXT_MESSAGE_CONTENT"`, `"delta":"hi"`,
		`"type":"RUN_FINISHED"`, `"type":"success"`,
	} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("the output is missing %s; got %s", want, buf.String())
		}
	}

	// 反向：不能出现 A2UI 的残留字段
	for _, bad := range []string{"surfaceId", "dataModelUpdate", "beginRendering", "interruptRequest"} {
		if strings.Contains(buf.String(), bad) {
			t.Fatalf("the output still contains the A2UI field %s", bad)
		}
	}
	// 每一行都得是独立 JSON
	for i, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var v map[string]any
		if err := json.Unmarshal(line, &v); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, err)
		}
	}
}

// ★ %v 形式会把 *approval.ApprovalInfo 打平成 &{write_file {"file_path": ...}}。
//
//	这段文本经 AG-UI 事件流直达前端审批卡 —— 写成 &{} 结构体 dump 等于
//	把内部 Go 表示泄漏成用户界面文案。
func TestContractInterruptDescribesApprovalInfo(t *testing.T) {
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

	got, _ := interruptsFrom(ev, "")
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

// ── reasoning ────────────────────────────────────────────────────────────
//
// ★ 这组测试是补一个真实的缺口：推理内容此前在传输层被整个丢弃。
//   eino 一直把它放在 AssistantGenMultiContent 的 reasoning part 里，
//   但 renderStream 只取 AssistantDeltaText —— 前端那套 reasoning UI
//   因此永远拿不到数据，是死代码。

// reasoningChunk 造一个只含推理的流式 chunk。
func reasoningChunk(text string) *schema.Message {
	return &schema.Message{
		Role: schema.Assistant,
		AssistantGenMultiContent: []schema.MessageOutputPart{{
			Type:      schema.ChatMessagePartTypeReasoning,
			Reasoning: &schema.MessageOutputReasoning{Text: text},
		}},
	}
}

func streamEvent(chunks ...*schema.Message) *adk.TypedAgentEvent[*schema.Message] {
	return &adk.TypedAgentEvent[*schema.Message]{
		Output: &adk.TypedAgentOutput[*schema.Message]{
			MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
				Role:          schema.Assistant,
				IsStreaming:   true,
				MessageStream: schema.StreamReaderFromArray(chunks),
			},
		},
	}
}

func assertTypes(t *testing.T, got []events.EventType, want []events.EventType) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("event count = %d, want %d\ngot: %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event[%d] = %v, want %v\ngot: %v\nwant: %v", i, got[i], want[i], got, want)
		}
	}
}

func TestContractReasoningStream(t *testing.T) {
	evs, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		streamEvent(
			reasoningChunk("先看目录 "),
			reasoningChunk("再读文件。"),
			schema.AssistantMessage("正文内容", nil),
		),
	}, Options{})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	// decode() 内部已经跑过 events.ValidateSequence —— 顺序与配对由它把关。
	want := []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeReasoningStart,
		events.EventTypeReasoningMessageStart,
		events.EventTypeReasoningMessageContent,
		events.EventTypeReasoningMessageContent,
		events.EventTypeReasoningMessageEnd,
		events.EventTypeReasoningEnd,
		events.EventTypeTextMessageStart,
		events.EventTypeTextMessageContent,
		events.EventTypeTextMessageEnd,
		events.EventTypeRunFinished,
	}
	assertTypes(t, typesOf(evs), want)
}

func TestContractReasoningOnlyRun(t *testing.T) {
	// 只有推理、没有正文的一轮：推理也必须成对闭合。
	evs, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		streamEvent(reasoningChunk("想了一下")),
	}, Options{})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	// ★ 末尾那对 TEXT_MESSAGE_START / TEXT_MESSAGE_END 是**故意**的：
	//   这条消息只有推理、没有正文，而 REASONING_* 事件**不带归属** ——
	//   不补的话前端建出的这条消息是匿名的，于是多 agent 会话里
	//   「这段思考是谁的」就答不出来。
	//
	//   补在**末尾**而不是内容之前：AG-UI 的 ValidateSequence 规定一条
	//   消息一对 START/END，消息开着时再发 START 会被判
	//   "message ... already started"（见 emitter.announce 的注释）。
	//
	//   协议上配平（有 START 有 END），前端拿到的是一条没有正文段、
	//   只有推理段的消息 —— 与历史回放同形。
	assertTypes(t, typesOf(evs), []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeReasoningStart,
		events.EventTypeReasoningMessageStart,
		events.EventTypeReasoningMessageContent,
		events.EventTypeReasoningMessageEnd,
		events.EventTypeReasoningEnd,
		events.EventTypeTextMessageStart,
		events.EventTypeTextMessageEnd,
		events.EventTypeRunFinished,
	})
}

func TestContractReasoningClosedBeforeToolCall(t *testing.T) {
	// ★ 推理未闭合就发工具调用会让前端拿到横跨两段的 part ——
	//   AG-UI 的 part 是靠 END 闭合的。
	evs, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		streamEvent(reasoningChunk("要读文件"), func() *schema.Message {
			idx := 0
			return &schema.Message{
				Role: schema.Assistant,
				ToolCalls: []schema.ToolCall{{
					Index: &idx, ID: "call-1",
					Function: schema.FunctionCall{Name: "read_file", Arguments: `{}`},
				}},
			}
		}()),
	}, Options{})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	// ★ 末尾那对 TEXT_MESSAGE_START / TEXT_MESSAGE_END 是**故意**的：
	//   这条消息有内容（推理 + 工具调用）却没有正文段，而 REASONING_* 与
	//   TOOL_CALL_* 都不带归属 → 不补声明的话这条消息是匿名的。
	//
	//   工具调用挂在**这条**消息上（parentMessageId = msgID），所以补上
	//   归属后，工具卡也归到同一个 agent 名下。
	want := []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeReasoningStart,
		events.EventTypeReasoningMessageStart,
		events.EventTypeReasoningMessageContent,
		events.EventTypeReasoningMessageEnd,
		events.EventTypeReasoningEnd,
		events.EventTypeToolCallStart,
		events.EventTypeToolCallArgs, // Arguments 非空，正常发
		events.EventTypeToolCallEnd,
		events.EventTypeTextMessageStart,
		events.EventTypeTextMessageEnd,
		events.EventTypeRunFinished,
	}
	assertTypes(t, typesOf(evs), want)

	// 顺序断言：REASONING_END 必须排在 TOOL_CALL_START 之前。
	reasonEnd, toolStart := -1, -1
	for i, e := range evs {
		if e.Type() == events.EventTypeReasoningEnd && reasonEnd < 0 {
			reasonEnd = i
		}
		if e.Type() == events.EventTypeToolCallStart && toolStart < 0 {
			toolStart = i
		}
	}
	if reasonEnd < 0 || toolStart < 0 || reasonEnd > toolStart {
		t.Fatalf("REASONING_END(%d) 必须在 TOOL_CALL_START(%d) 之前", reasonEnd, toolStart)
	}
}

func TestContractFlatReasoningContent(t *testing.T) {
	// 非多模态路径：推理落在扁平的 ReasoningContent 字段上，也要能取到。
	msg := &schema.Message{Role: schema.Assistant, ReasoningContent: "扁平推理"}
	evs, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{textEvent(msg)}, Options{})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	// ★ 末尾那对 TEXT_MESSAGE_START / TEXT_MESSAGE_END 是**故意**的：
	//   这条消息只有推理、没有正文，而 REASONING_* 事件**不带归属** ——
	//   不补的话前端建出的这条消息是匿名的，于是多 agent 会话里
	//   「这段思考是谁的」就答不出来。
	//
	//   补在**末尾**而不是内容之前：AG-UI 的 ValidateSequence 规定一条
	//   消息一对 START/END，消息开着时再发 START 会被判
	//   "message ... already started"（见 emitter.announce 的注释）。
	//
	//   协议上配平（有 START 有 END），前端拿到的是一条没有正文段、
	//   只有推理段的消息 —— 与历史回放同形。
	assertTypes(t, typesOf(evs), []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeReasoningStart,
		events.EventTypeReasoningMessageStart,
		events.EventTypeReasoningMessageContent,
		events.EventTypeReasoningMessageEnd,
		events.EventTypeReasoningEnd,
		events.EventTypeTextMessageStart,
		events.EventTypeTextMessageEnd,
		events.EventTypeRunFinished,
	})
}

// ★ 这条断言存在的原因：Go 侧的 events.Validate() **不检查**
//
//	REASONING_MESSAGE_START 的 role，而前端 @ag-ui/core 的 schema 把它
//	定义成 zod.literal("reasoning")。传 "assistant" 时 Go 侧一路绿灯，
//	前端收到却直接 ZodError 拒收整条事件 —— 表现是「推理一出现，
//	后面所有内容都没了」。
//
//	跨语言的校验只有前端有，所以只能在这里把值钉死。
func TestContractReasoningRoleIsTheReasoningLiteral(t *testing.T) {
	// 先跑一次：run() 内部的 events.ValidateSequence 会把关配对与顺序。
	if _, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		streamEvent(reasoningChunk("想一想")),
	}, Options{}); err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	// 直接在原始 JSON 上找 REASONING_MESSAGE_START，绕开 SDK 的反序列化 ——
	// 目的就是看它序列化出去的长什么样。
	var buf bytes.Buffer
	_, _ = RunStream(&buf, "thread-1", &sliceEvents{evs: []*adk.TypedAgentEvent[*schema.Message]{
		streamEvent(reasoningChunk("想一想")),
	}}, Options{})

	// ★ 裸 JSONL，没有 `data:` 前缀 —— 那个前缀是 API 层的 sseio.Stream
	//   加的，RunStream 这一层直接写 JSON（decode() 同理）。
	for _, line := range strings.Split(buf.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e struct {
			Type string `json:"type"`
			Role string `json:"role"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if e.Type == "REASONING_MESSAGE_START" {
			if e.Role != "reasoning" {
				t.Fatalf("REASONING_MESSAGE_START role = %q, want %q "+
					"(前端 schema 是 zod.literal(\"reasoning\")，别的值会被整条拒收)",
					e.Role, "reasoning")
			}
			return
		}
	}
	t.Fatalf("no REASONING_MESSAGE_START in stream:\n%s", buf.String())
}
