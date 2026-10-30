package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/ouranoshong/openheron/agentkit/emit"
	"github.com/ouranoshong/openheron/contract"
)

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

// run derives the events and renders them, and reports both.
//
// ★ The two halves are separate now: the derivation belongs to the run
//
//	(agentkit/emit) and the rendering to the transport. The test drives
//	both, because what it checks is the frame sequence they produce
//	together — the wire must not change because the plumbing did.
func run(t *testing.T, evs []*adk.TypedAgentEvent[*schema.Message], opts emit.Options) ([]events.Event, emit.Artifacts, error) {
	t.Helper()
	return runErr(t, evs, opts)
}

func runErr(t *testing.T, evs []*adk.TypedAgentEvent[*schema.Message], opts emit.Options) ([]events.Event, emit.Artifacts, error) {
	t.Helper()
	var buf bytes.Buffer
	ch := make(chan contract.RunEvent, 4096)
	arts, derr := emit.Derive(&sliceEvents{evs: evs}, opts, sinkFunc(func(ev contract.RunEvent) { ch <- ev }), nil)
	close(ch)
	Render(&buf, "thread-1", "run-1", ch)
	return decode(t, buf.Bytes()), arts, derr
}

// sinkFunc adapts a function to emit.Sink.
type sinkFunc func(contract.RunEvent)

func (f sinkFunc) Publish(ev contract.RunEvent) { f(ev) }

// runStream derives the events and renders them, the way the run does.
//
// ★ It is the test's stand-in for the run: in production the run derives
//
//	and the transport renders, and the two are separate now. The test
//	drives both, because the wire must not change because the plumbing
//	did.
func runStream(t *testing.T, w io.Writer, threadID string, src *sliceEvents, opts emit.Options) (emit.Artifacts, error) {
	t.Helper()
	ch := make(chan contract.RunEvent, 4096)
	arts, derr := emit.Derive(src, opts, sinkFunc(func(ev contract.RunEvent) { ch <- ev }), nil)
	close(ch)
	Render(w, threadID, opts.RunID, ch)
	return arts, derr
}

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

func TestContractEmptyRun(t *testing.T) {
	evs, _, _ := run(t, nil, emit.Options{})

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
	}, emit.Options{})

	got := typesOf(evs)
	want := []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeTextMessageStart,
		events.EventTypeTextMessageContent,
		events.EventTypeTextMessageEnd,
		events.EventTypeRunFinished,
	}
	assertSeq(t, got, want)

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
	}, emit.Options{})

	assertSeq(t, typesOf(evs), []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeTextMessageStart,
		events.EventTypeTextMessageContent,
		events.EventTypeTextMessageEnd,
		events.EventTypeToolCallResult,
		events.EventTypeRunFinished,
	})

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
	}, emit.Options{})

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

	evs, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{ev}, emit.Options{})
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
		emit.Options{RootAgent: "main"})

	content := evs[2].(*events.TextMessageContentEvent)
	if content.SubagentRunID != "researcher" {
		t.Fatalf("subagentRunId = %q, want researcher", content.SubagentRunID)
	}
}

func TestContractRootAgentNotAttributed(t *testing.T) {
	evs, _, _ := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		{AgentName: "main", Output: &adk.TypedAgentOutput[*schema.Message]{
			MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
				Role: schema.Assistant, Message: assistantText("root"),
			},
		}},
		exitEvent(),
	}, emit.Options{RootAgent: "main"})

	if got := evs[2].(*events.TextMessageContentEvent).SubagentRunID; got != "" {
		t.Fatalf("the entry agent must not carry subagentRunId, got %q", got)
	}
}

type wrappedError struct {
	msg   string
	cause error
}

func (e *wrappedError) Error() string { return e.msg }
func (e *wrappedError) Unwrap() error { return e.cause }

func TestContractCancelIsNotAnError(t *testing.T) {
	ev := &adk.TypedAgentEvent[*schema.Message]{
		Err: &wrappedError{
			msg:   "[GraphRunError] context has been canceled: context canceled",
			cause: context.Canceled,
		},
	}

	evs, res, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{ev}, emit.Options{})
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
}

func TestContractRunErrorEndsStream(t *testing.T) {
	ev := &adk.TypedAgentEvent[*schema.Message]{
		Err: errors.New("401 auth -- invalid credentials"),
	}

	evs, _, err := runErr(t, []*adk.TypedAgentEvent[*schema.Message]{ev}, emit.Options{})
	if err == nil {
		t.Fatal("an error was expected")
	}
	assertSeq(t, typesOf(evs), []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeRunError,
	})

	re := evs[1].(*events.RunErrorEvent)
	if re.Code == nil || *re.Code != emit.CodeModelAuth {
		t.Fatalf("code = %v, want %s", re.Code, emit.CodeModelAuth)
	}
}

func TestContractModelRetryIsNotTerminal(t *testing.T) {
	evs, _, _ := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		{Output: &adk.TypedAgentOutput[*schema.Message]{
			MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
				Role: schema.Assistant, Message: assistantText("重试后成功"),
			},
		}},
		exitEvent(),
	}, emit.Options{})

	assertSeq(t, typesOf(evs), []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeTextMessageStart,
		events.EventTypeTextMessageContent,
		events.EventTypeTextMessageEnd,
		events.EventTypeRunFinished,
	})
}

// A broken sink loses the frames — and nothing else.
//
// ★ The archive is the run's business now: the renderer cannot stop the
//
//	run, and the run's messages land on disk whatever the connection
//	does. The run's own test covers the archive; this one covers the
//	renderer.
func TestContractBrokenSinkLosesOnlyTheFrames(t *testing.T) {
	ch := make(chan contract.RunEvent, 4096)
	arts, derr := emit.Derive(&sliceEvents{evs: []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(assistantText("第一")),
		textEvent(assistantText("第二")),
		exitEvent(),
	}}, emit.Options{}, sinkFunc(func(ev contract.RunEvent) { ch <- ev }), nil)
	close(ch)

	if derr != nil {
		t.Fatalf("Derive: %v", derr)
	}
	if len(arts.Intermediates) != 2 {
		t.Fatalf("the derivation must keep the messages, got %d", len(arts.Intermediates))
	}
	// The renderer writes to a sink that refuses every byte: it must stop
	// quietly instead of taking the run down with it.
	Render(&failingWriter{}, "thread-1", "run-1", ch)
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("sink closed") }

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

func TestDecodeRejectsMalformed(t *testing.T) {
	bad := []byte(`{"type":"NOT_A_REAL_TYPE"}`)
	if _, err := events.EventFromJSON(bad); err == nil {
		t.Fatal("an invalid event type must fail to parse")
	}

	badSeq := []byte(`{"type":"TEXT_MESSAGE_CONTENT","messageId":"x","delta":"hi"}` + "\n")
	ev, err := events.EventFromJSON(badSeq)
	if err != nil {
		t.Fatalf("a single event must parse: %v", err)
	}
	if err := events.ValidateSequence([]events.Event{ev}); err == nil {
		t.Fatal("an orphan TEXT_MESSAGE_CONTENT must be rejected by ValidateSequence")
	}
}

func TestErrorCodeMapping(t *testing.T) {
	cases := map[string]string{
		"401 auth —— …":                emit.CodeModelAuth,
		"429 rate_limit —— …":          emit.CodeModelRateLimit,
		"429 insufficient quota":       emit.CodeModelQuota,
		"503 overload":                 emit.CodeModelOverload,
		"request timed out":            emit.CodeModelTimeout,
		"dial tcp: connection refused": emit.CodeModelNetwork,
		"随便什么错":                        emit.CodeModelUnknown,
	}
	for in, want := range cases {
		if got := emit.ErrorCode(errors.New(in)); got != want {
			t.Fatalf("ErrorCode(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestRunIDDefaults(t *testing.T) {
	var buf bytes.Buffer
	if _, err := runStream(t, &buf, "thread-1", &sliceEvents{}, emit.Options{}); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	evs := decode(t, buf.Bytes())
	start, fin := evs[0].(*events.RunStartedEvent), evs[1].(*events.RunFinishedEvent)
	if start.RunID() == "" || start.RunID() != fin.RunID() {
		t.Fatalf("the runId must be generated and match on both ends: %q vs %q", start.RunID(), fin.RunID())
	}
	if uuid.Validate(start.RunID()) != nil {
		t.Logf("runId is not a UUID: %q (does not affect the protocol, informational only)", start.RunID())
	}
}

func TestJSONShape(t *testing.T) {
	var buf bytes.Buffer
	_, err := runStream(t, &buf, "thread-1", &sliceEvents{evs: []*adk.TypedAgentEvent[*schema.Message]{
		textEvent(assistantText("hi")),
	}}, emit.Options{RunID: "r1"})
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

	for _, bad := range []string{"surfaceId", "dataModelUpdate", "beginRendering", "interruptRequest"} {
		if strings.Contains(buf.String(), bad) {
			t.Fatalf("the output still contains the A2UI field %s", bad)
		}
	}
	for i, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var v map[string]any
		if err := json.Unmarshal(line, &v); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, err)
		}
	}
}

// ── reasoning ────────────────────────────────────────────────────────────

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
	}, emit.Options{})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

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
	evs, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		streamEvent(reasoningChunk("想了一下")),
	}, emit.Options{})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
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
	}, emit.Options{})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	want := []events.EventType{
		events.EventTypeRunStarted,
		events.EventTypeReasoningStart,
		events.EventTypeReasoningMessageStart,
		events.EventTypeReasoningMessageContent,
		events.EventTypeReasoningMessageEnd,
		events.EventTypeReasoningEnd,
		events.EventTypeToolCallStart,
		events.EventTypeToolCallArgs,
		events.EventTypeToolCallEnd,
		events.EventTypeTextMessageStart,
		events.EventTypeTextMessageEnd,
		events.EventTypeRunFinished,
	}
	assertTypes(t, typesOf(evs), want)

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
		t.Fatalf("REASONING_END(%d) must come before TOOL_CALL_START(%d)", reasonEnd, toolStart)
	}
}

func TestContractFlatReasoningContent(t *testing.T) {
	msg := &schema.Message{Role: schema.Assistant, ReasoningContent: "扁平推理"}
	evs, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{textEvent(msg)}, emit.Options{})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
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

func TestContractReasoningRoleIsTheReasoningLiteral(t *testing.T) {
	if _, _, err := run(t, []*adk.TypedAgentEvent[*schema.Message]{
		streamEvent(reasoningChunk("想一想")),
	}, emit.Options{}); err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	var buf bytes.Buffer
	_, _ = runStream(t, &buf, "thread-1", &sliceEvents{evs: []*adk.TypedAgentEvent[*schema.Message]{
		streamEvent(reasoningChunk("想一想")),
	}}, emit.Options{})

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
					"(the frontend schema is zod.literal(\"reasoning\"); any other value is rejected outright)",
					e.Role, "reasoning")
			}
			return
		}
	}
	t.Fatalf("no REASONING_MESSAGE_START in stream:\n%s", buf.String())
}
