package build

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// cutModel answers with one tool call and the finish reason the test asks
// for.
type cutModel struct {
	reason string
	args   string
}

func (m *cutModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return m.message(), nil
}

func (m *cutModel) Stream(context.Context, []*schema.Message, ...model.Option) (
	*schema.StreamReader[*schema.Message], error) {

	// ★ The tool call comes first and the reason comes after it — that is
	//   the order a provider sends them in, and the order the guard has to
	//   cope with.
	return schema.StreamReaderFromArray([]*schema.Message{
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: schema.FunctionCall{Name: "config_write", Arguments: m.args},
		}}},
		{Role: schema.Assistant, ResponseMeta: &schema.ResponseMeta{FinishReason: m.reason}},
	}), nil
}

func (m *cutModel) message() *schema.Message {
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: schema.FunctionCall{Name: "config_write", Arguments: m.args},
		}},
		ResponseMeta: &schema.ResponseMeta{FinishReason: m.reason},
	}
}

func (m *cutModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) { return m, nil }

func (m *cutModel) GetType() string { return "cut" }

// The arguments a provider leaves behind when it stops in the middle of the
// content: not valid JSON at all.
const brokenArgs = `{"path":"skills/x/SKILL.md","content":"---\nname: x\n\n# the body stops here`

func TestAGenerationThatRanOutOfRoomMarksItsToolCall(t *testing.T) {
	m := newTruncationAwareModel(&cutModel{reason: finishReasonLength, args: brokenArgs})

	msg, err := m.Generate(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !isTruncatedArgs(msg.ToolCalls[0].Function.Arguments) {
		t.Fatalf("a cut-off call must carry the marker: %s", msg.ToolCalls[0].Function.Arguments)
	}
}

func TestACompleteGenerationKeepsItsToolCall(t *testing.T) {
	args := `{"path":"skills/x/SKILL.md","content":"body"}`
	m := newTruncationAwareModel(&cutModel{reason: "tool_calls", args: args})

	msg, err := m.Generate(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.ToolCalls[0].Function.Arguments; got != args {
		t.Fatalf("a complete call must not be marked: %s", got)
	}
}

// ★ The stream is the path the agent actually takes, and the reason arrives
// after the call it is about.
func TestTheStreamMarksACutCall(t *testing.T) {
	m := newTruncationAwareModel(&cutModel{reason: finishReasonLength, args: brokenArgs})

	sr, err := m.Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()

	var found bool
	for {
		msg, rerr := sr.Recv()
		if rerr != nil {
			break
		}
		if msg == nil || len(msg.ToolCalls) == 0 {
			continue
		}
		found = true
		if !isTruncatedArgs(msg.ToolCalls[0].Function.Arguments) {
			t.Fatalf("a cut-off call must carry the marker: %s", msg.ToolCalls[0].Function.Arguments)
		}
	}
	if !found {
		t.Fatal("the tool call never arrived")
	}
}

func TestTheStreamKeepsACompleteCall(t *testing.T) {
	args := `{"path":"skills/x/SKILL.md","content":"body"}`
	m := newTruncationAwareModel(&cutModel{reason: "tool_calls", args: args})

	sr, err := m.Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()

	for {
		msg, rerr := sr.Recv()
		if rerr != nil {
			break
		}
		if msg == nil || len(msg.ToolCalls) == 0 {
			continue
		}
		if got := msg.ToolCalls[0].Function.Arguments; got != args {
			t.Fatalf("a complete call must not be marked: %s", got)
		}
	}
}

func TestTheGuardRefusesACutCall(t *testing.T) {
	ran := false
	mw := newTruncationGuardMiddleware()

	endpoint, err := mw.WrapInvokableToolCall(context.Background(),
		func(context.Context, string, ...tool.Option) (string, error) {
			ran = true
			return "ran", nil
		}, &adk.ToolContext{Name: "config_write", CallID: "call_1"})
	if err != nil {
		t.Fatal(err)
	}

	out, err := endpoint(context.Background(), markTruncatedArgs(brokenArgs))
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("a cut-off call must not run")
	}
	if !strings.Contains(out, "cut off") {
		t.Fatalf("the refusal must say what happened: %q", out)
	}
	if !strings.Contains(out, "append") {
		t.Fatalf("the refusal must say the way out: %q", out)
	}
}

func TestTheGuardPassesACleanCall(t *testing.T) {
	ran := false
	mw := newTruncationGuardMiddleware()

	endpoint, err := mw.WrapInvokableToolCall(context.Background(),
		func(context.Context, string, ...tool.Option) (string, error) {
			ran = true
			return "ran", nil
		}, &adk.ToolContext{Name: "config_write", CallID: "call_1"})
	if err != nil {
		t.Fatal(err)
	}

	out, err := endpoint(context.Background(), `{"path":"a.md","content":"b"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !ran || out != "ran" {
		t.Fatalf("a clean call must run: %q", out)
	}
}

// ★ The last line: a tool that ran with half of its arguments would write
// half a file.
func TestAConfigToolRefusesACutCall(t *testing.T) {
	_, _, tools := configToolEnv(t)

	_, err := callTool(t, tools, "config_write", markTruncatedArgs(brokenArgs))
	if err == nil {
		t.Fatal("a cut-off call must be refused")
	}
	if !strings.Contains(err.Error(), "cut off") {
		t.Fatalf("the refusal must say what happened: %v", err)
	}
}

// ★ The marker replaces the broken text rather than carrying it along: a
// tool that could read half a `content` value would write half a file.
func TestTheMarkerKeepsTheFieldsThatArrived(t *testing.T) {
	marked := markTruncatedArgs(`{"path":"a.md","content":"body"}`)
	if !isTruncatedArgs(marked) {
		t.Fatalf("the marker must be there: %s", marked)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(marked), &fields); err != nil {
		t.Fatalf("the marked arguments must be valid JSON: %v", err)
	}
	if fields["path"] != "a.md" || fields["content"] != "body" {
		t.Fatalf("the fields that arrived must survive: %#v", fields)
	}

	// ★ The broken half is not carried along: it is not JSON, and the tool
	//   must not be able to read half of a content value.
	marked = markTruncatedArgs(brokenArgs)
	fields = nil
	if err := json.Unmarshal([]byte(marked), &fields); err != nil {
		t.Fatalf("the marked arguments must be valid JSON: %v", err)
	}
	if len(fields) != 1 || !isTruncatedArgs(marked) {
		t.Fatalf("only the marker may survive a broken call: %#v", fields)
	}
}
