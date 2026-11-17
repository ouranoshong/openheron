package build

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// oneChunkModel answers with a single chunk that carries the tool call and
// the finish reason together — the shape a provider sends when it closes the
// turn on the same frame as the last fragment of the call.
type oneChunkModel struct {
	args string
}

func (m *oneChunkModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: schema.FunctionCall{Name: "config_write", Arguments: m.args},
		}},
		ResponseMeta: &schema.ResponseMeta{FinishReason: "tool_calls"},
	}, nil
}

func (m *oneChunkModel) Stream(context.Context, []*schema.Message, ...model.Option) (
	*schema.StreamReader[*schema.Message], error) {

	return schema.StreamReaderFromArray([]*schema.Message{
		{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID:       "call_1",
				Type:     "function",
				Function: schema.FunctionCall{Name: "config_write", Arguments: m.args},
			}},
			ResponseMeta: &schema.ResponseMeta{FinishReason: "tool_calls"},
		},
	}), nil
}

func (m *oneChunkModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *oneChunkModel) GetType() string { return "one-chunk" }

// fragmentModel answers with a call whose arguments arrive in pieces, the
// way a provider streams them.
type fragmentModel struct {
	pieces   []string
	reason   string
	lastWith bool
}

func (m *fragmentModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return &schema.Message{
		Role: schema.Assistant,
		ToolCalls: []schema.ToolCall{{
			ID:       "call_1",
			Type:     "function",
			Function: schema.FunctionCall{Name: "config_write", Arguments: strings.Join(m.pieces, "")},
		}},
		ResponseMeta: &schema.ResponseMeta{FinishReason: m.reason},
	}, nil
}

func (m *fragmentModel) Stream(context.Context, []*schema.Message, ...model.Option) (
	*schema.StreamReader[*schema.Message], error) {

	msgs := make([]*schema.Message, 0, len(m.pieces)+1)
	for i, p := range m.pieces {
		meta := &schema.ResponseMeta{}
		if i == len(m.pieces)-1 && m.lastWith {
			meta.FinishReason = m.reason
		}
		msgs = append(msgs, &schema.Message{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID:       "call_1",
				Type:     "function",
				Function: schema.FunctionCall{Name: "config_write", Arguments: p},
			}},
			ResponseMeta: meta,
		})
	}
	if !m.lastWith {
		msgs = append(msgs, &schema.Message{
			Role:         schema.Assistant,
			ResponseMeta: &schema.ResponseMeta{FinishReason: m.reason},
		})
	}
	return schema.StreamReaderFromArray(msgs), nil
}

func (m *fragmentModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *fragmentModel) GetType() string { return "fragment" }

// collectArgs reads a stream and joins the tool call arguments the way the
// framework does.
func collectArgs(t *testing.T, m model.ToolCallingChatModel) string {
	t.Helper()

	sr, err := m.Stream(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()

	var sb strings.Builder
	var calls int
	for {
		msg, rerr := sr.Recv()
		if rerr != nil {
			break
		}
		if msg == nil {
			continue
		}
		for _, tc := range msg.ToolCalls {
			calls++
			sb.WriteString(tc.Function.Arguments)
		}
	}
	if calls == 0 {
		t.Fatal("the tool call never arrived")
	}
	return sb.String()
}

// ★ The concatenation the framework does is the sum of what the model layer
// sends. A chunk that is sent twice is therefore an argument string that
// carries its own tail twice — and that is exactly what a live session
// produced: `{"path":"…/SKILL.md"}` followed by `"}`.
func TestTheStreamDoesNotSendTheCallChunkTwice(t *testing.T) {
	const args = `{"path":"a.md","content":"b"}`
	m := newCallGuardModel(&oneChunkModel{args: args})

	if got := collectArgs(t, m); got != args {
		t.Fatalf("the arguments must arrive once: %q", got)
	}
}

// ★ The same, with the reason on a chunk of its own: the held call is
// released once.
func TestTheStreamReleasesTheHeldCallOnce(t *testing.T) {
	const args = `{"path":"a.md","content":"b"}`
	m := newCallGuardModel(&cutModel{reason: "tool_calls", args: args})

	if got := collectArgs(t, m); got != args {
		t.Fatalf("the arguments must arrive once: %q", got)
	}
}

// ★ The call arrives in pieces and the reason rides on the last one: the
// pieces are concatenated once, and the arguments are the object.
func TestTheStreamJoinsThePiecesOnce(t *testing.T) {
	pieces := []string{`{"path":"a.md",`, `"content":"b"}`}
	m := newCallGuardModel(&fragmentModel{pieces: pieces, reason: "tool_calls", lastWith: true})

	if got := collectArgs(t, m); got != `{"path":"a.md","content":"b"}` {
		t.Fatalf("the pieces must be joined once: %q", got)
	}
}

// ★ A call whose pieces carry a tail twice is repaired: the object in
// front of the repeat is what the model meant to send.
func TestTheStreamRepairsADuplicatedTail(t *testing.T) {
	pieces := []string{`{"path":"a.md",`, `"content":"b"}`, `"}`}
	m := newCallGuardModel(&fragmentModel{pieces: pieces, reason: "tool_calls", lastWith: true})

	if got := collectArgs(t, m); got != `{"path":"a.md","content":"b"}` {
		t.Fatalf("the duplicated tail must be repaired away: %q", got)
	}
}

// ★ A cut-off call has to arrive as **one** marker. ★ Marking every piece
// would concatenate the marker N times, and the concatenation is not JSON —
// so nothing downstream could read it, and the guard would let the call
// through.
func TestTheStreamMarksACutCallOnce(t *testing.T) {
	pieces := []string{`{"path":"a.md",`, `"content":"b`}
	m := newCallGuardModel(&fragmentModel{pieces: pieces, reason: finishReasonLength, lastWith: true})

	got := collectArgs(t, m)
	if !isTruncatedArgs(got) {
		t.Fatalf("the call must carry the marker: %q", got)
	}
	if !isJSONObject(got) {
		t.Fatalf("the marked arguments must be one JSON object: %q", got)
	}
}
