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
	m := newCallGuardModel(&cutModel{reason: finishReasonLength, args: brokenArgs})

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
	m := newCallGuardModel(&cutModel{reason: "tool_calls", args: args})

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
	m := newCallGuardModel(&cutModel{reason: finishReasonLength, args: brokenArgs})

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
	m := newCallGuardModel(&cutModel{reason: "tool_calls", args: args})

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
	mw := newCallGuardMiddleware()

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
	mw := newCallGuardMiddleware()

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
// tool that could read half of a `content` value would write half a file.
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

// ★ The marker has to be found in the string the framework hands over, and
// that string is the concatenation of the fragments the provider sent: the
// concatenation of two markers is not JSON, so a parse would miss the very
// calls the guard exists for.
func TestTheMarkerIsFoundInAConcatenation(t *testing.T) {
	one := markTruncatedArgs(brokenArgs)
	two := one + one

	if !isTruncatedArgs(two) {
		t.Fatalf("the marker must be found in a concatenation: %s", two)
	}
	if isMalformedArgs(two) {
		t.Fatal("a cut-off call is not a malformed one")
	}
}

// ★ The shapes a provider really produces: the arguments are a complete
// object and a piece of it comes again at the end. Every one of them was
// read off a live session.
func TestATailThatArrivedTwiceIsRepaired(t *testing.T) {
	const prefix = `{"baseRevision": "3f4d7624", "content": "body", ` +
		`"path": "skills/openheron-config-templates/SKILL.md"}`

	for _, tail := range []string{`KILL.md"}`, `.md"}`, `"}`, `}`, `/SKILL.md"}`} {
		fixed, ok := repairDuplicatedTail(prefix + tail)
		if !ok {
			t.Fatalf("the tail %q must be repaired away", tail)
		}
		if fixed != prefix {
			t.Fatalf("the repaired arguments must be the object: %q", fixed)
		}
		if !isJSONObject(fixed) {
			t.Fatalf("the repaired arguments must be one JSON object: %q", fixed)
		}
	}
}

// ★ The other way round: a call that is broken in a way that is not a
// repeat must not be guessed at. A repair that fires here would write
// something the model never said.
func TestABrokenCallIsNotRepaired(t *testing.T) {
	for _, args := range []string{
		brokenArgs,                       // cut off in the middle of a string
		`{"path":"a.md"} and some prose`, // a stray tail
		`{"path":"a.md"}{"path":"b.md"}`, // two objects
		`{"path":"a.md","content":"x",}`, // a trailing comma
		`[1,2,3]`,                        // not an object at all
		`{"path":"a.md"} `,               // nothing after the object
	} {
		if fixed, ok := repairDuplicatedTail(args); ok {
			t.Fatalf("these must not be repaired: %q -> %q", args, fixed)
		}
	}
}

// ★ The guard runs the call with the repaired arguments: the object in
// front of the repeat is what the model meant to send.
func TestTheGuardRepairsADuplicatedTail(t *testing.T) {
	const prefix = `{"path":"a.md","content":"b"}`

	var seen string
	mw := newCallGuardMiddleware()
	endpoint, err := mw.WrapInvokableToolCall(context.Background(),
		func(_ context.Context, args string, _ ...tool.Option) (string, error) {
			seen = args
			return "ran", nil
		}, &adk.ToolContext{Name: "config_write", CallID: "call_1"})
	if err != nil {
		t.Fatal(err)
	}

	out, err := endpoint(context.Background(), prefix+`}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "ran" {
		t.Fatalf("the call must run: %q", out)
	}
	if seen != prefix {
		t.Fatalf("the tool must get the repaired arguments: %q", seen)
	}
}

// ★ A call that is broken in any other way is refused with a reason the
// model can act on — not with the Go JSON error, which names neither the
// fault nor the way out.
func TestTheGuardRefusesABrokenCall(t *testing.T) {
	ran := false
	mw := newCallGuardMiddleware()
	endpoint, err := mw.WrapInvokableToolCall(context.Background(),
		func(context.Context, string, ...tool.Option) (string, error) {
			ran = true
			return "ran", nil
		}, &adk.ToolContext{Name: "config_write", CallID: "call_1"})
	if err != nil {
		t.Fatal(err)
	}

	out, err := endpoint(context.Background(), brokenArgs)
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("a broken call must not run")
	}
	if !strings.Contains(out, "complete JSON object") {
		t.Fatalf("the refusal must say what happened: %q", out)
	}
	if !strings.Contains(out, "append") {
		t.Fatalf("the refusal must say the way out: %q", out)
	}
}

// ★ The same, for the streamable path: the refusal is a message the model
// reads, not an error.
func TestTheStreamableGuardRefusesABrokenCall(t *testing.T) {
	mw := newCallGuardMiddleware()
	endpoint, err := mw.WrapStreamableToolCall(context.Background(),
		func(context.Context, string, ...tool.Option) (*schema.StreamReader[string], error) {
			t.Fatal("a broken call must not run")
			return nil, nil
		}, &adk.ToolContext{Name: "config_write", CallID: "call_1"})
	if err != nil {
		t.Fatal(err)
	}

	sr, err := endpoint(context.Background(), brokenArgs)
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()

	msg, rerr := sr.Recv()
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(msg, "complete JSON object") {
		t.Fatalf("the refusal must say what happened: %q", msg)
	}
}

// ★ A broken call is marked, so the guard can see it — and the marker
// stands for the call, not for the wreck.
func TestABrokenCallIsMarked(t *testing.T) {
	m := newCallGuardModel(&cutModel{reason: "tool_calls", args: brokenArgs})

	msg, err := m.Generate(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	args := msg.ToolCalls[0].Function.Arguments
	if !isMalformedArgs(args) {
		t.Fatalf("a broken call must carry the marker: %s", args)
	}
	if !isJSONObject(args) {
		t.Fatalf("the marked arguments must be one JSON object: %s", args)
	}
	if strings.Contains(args, "the body stops here") {
		t.Fatalf("the broken text must not be carried along: %s", args)
	}
}
