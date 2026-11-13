package build

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// truncationMarker is the one thing a cut-off tool call carries.
//
// ★ The arguments that arrive after a truncation are usually **not** valid
//
//	JSON: the provider stopped in the middle of a string. The framework
//	hands them to the tool as they are, and the tool's own unmarshal
//	fails with "unexpected end of JSON input" — a message about the tool,
//	about a problem that is really about the model. ★ And the other half of
//	the time the provider closes the JSON for us, so the arguments parse,
//	the tool runs, and a file lands with half a document in it. That is
//	the failure nobody can see afterwards: the run says "completed", the
//	ledger says "completed", and the file is half a file.
//
// So the cut is made explicit here. The arguments become JSON that says
// the call was cut off, and the guard below refuses it.
const truncationMarker = "__truncated_by_token_limit"

// finishReasonLength is the provider's word for "the output stopped
// because it ran out of room".
const finishReasonLength = "length"

// markTruncatedArgs returns the arguments of a cut-off tool call as JSON
// that says so.
//
// ★ The broken text is replaced rather than carried along: a tool that
//
//	reads half a `content` value would write half a file, and the point of
//	the marker is that the call must not run. What survives is the fields
//	that did arrive — they are what a refusal message can point at.
func markTruncatedArgs(raw string) string {
	out := map[string]json.RawMessage{truncationMarker: json.RawMessage("true")}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err == nil {
		for k, v := range fields {
			if k == truncationMarker {
				continue
			}
			out[k] = v
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return `{"` + truncationMarker + `":true}`
	}
	return string(b)
}

// isTruncatedArgs reports whether the arguments carry the marker.
func isTruncatedArgs(args string) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(args), &fields); err != nil {
		return false
	}
	_, ok := fields[truncationMarker]
	return ok
}

// finishReasonOf reads the provider's reason for ending a generation.
func finishReasonOf(msg *schema.Message) string {
	if msg == nil || msg.ResponseMeta == nil {
		return ""
	}
	return msg.ResponseMeta.FinishReason
}

// markCallsTruncated marks every tool call of a message whose generation
// ran out of room.
//
// ★ The caller decides **whether** the generation was cut: in the streaming
//
//	path the chunk that carries the tool call does not carry the reason —
//	the reason arrives later, in its own chunk — so asking the chunk
//	whether it was cut would always say no.
func markCallsTruncated(msg *schema.Message) {
	if msg == nil || len(msg.ToolCalls) == 0 {
		return
	}
	for i := range msg.ToolCalls {
		msg.ToolCalls[i].Function.Arguments = markTruncatedArgs(msg.ToolCalls[i].Function.Arguments)
	}
}

// truncationAwareModel marks the tool calls of a generation that ran out
// of room.
//
// ★ The model is the only layer that knows. The cut is visible as the
//
//	provider's finish reason, and everything below it — the tool, the
//	editor, the ledger — sees a call that looks complete.
type truncationAwareModel struct {
	inner model.ToolCallingChatModel
}

func newTruncationAwareModel(inner model.ToolCallingChatModel) model.ToolCallingChatModel {
	return &truncationAwareModel{inner: inner}
}

func (m *truncationAwareModel) Generate(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.Message, error) {

	msg, err := m.inner.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	if finishReasonOf(msg) == finishReasonLength {
		markCallsTruncated(msg)
	}
	return msg, nil
}

func (m *truncationAwareModel) Stream(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {

	sr, err := m.inner.Stream(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	out, sw := schema.Pipe[*schema.Message](1)

	go func() {
		defer sw.Close()

		// ★ The tool call arrives **before** the chunk that says why the
		//	stream ended, so the call has to wait for that answer. Holding
		//	it costs nothing: a tool call is the last thing a turn says,
		//	and the text before it has already gone out.
		var held []*schema.Message

		release := func(cut bool) {
			for _, h := range held {
				if cut {
					markCallsTruncated(h)
				}
				if sw.Send(h, nil) {
					return
				}
			}
			held = nil
		}

		for {
			msg, rerr := sr.Recv()
			if rerr != nil {
				// ★ The stream ended without saying why. Nothing is known
				//   about the call, so it goes as it is: the guard must
				//   not refuse a call it cannot say anything about.
				release(false)
				_ = sw.Send(nil, rerr)
				return
			}
			if msg == nil {
				continue
			}
			reason := finishReasonOf(msg)
			if len(msg.ToolCalls) > 0 {
				held = append(held, msg)
				if reason == "" {
					continue
				}
			}
			if reason == "" {
				if sw.Send(msg, nil) {
					return
				}
				continue
			}
			cut := reason == finishReasonLength
			release(cut)
			if cut {
				markCallsTruncated(msg)
			}
			if sw.Send(msg, nil) {
				return
			}
		}
	}()

	return out, nil
}

func (m *truncationAwareModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &truncationAwareModel{inner: inner}, nil
}

func (m *truncationAwareModel) GetType() string {
	if t, ok := m.inner.(interface{ GetType() string }); ok {
		return t.GetType()
	}
	return ""
}

// newTruncationGuardMiddleware refuses a tool call whose arguments were cut
// off by the model's own output limit.
//
// ★ It sits **first** in the chain, so it sees the call before the approval
//
//	gate does: a broken call must not reach a human as a card to approve,
//	and it must not run either. ★ The answer is a message rather than an
//	error, because a model that reads "the call was cut off" can pick
//	another way — and the way it needs is a smaller one.
func newTruncationGuardMiddleware() adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return &truncationGuardMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.Message]{},
	}
}

type truncationGuardMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
}

func (m *truncationGuardMiddleware) WrapInvokableToolCall(
	_ context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {

	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		if isTruncatedArgs(args) {
			return truncationRefusal(tCtx), nil
		}
		return endpoint(ctx, args, opts...)
	}, nil
}

func (m *truncationGuardMiddleware) WrapStreamableToolCall(
	_ context.Context, endpoint adk.StreamableToolCallEndpoint, tCtx *adk.ToolContext,
) (adk.StreamableToolCallEndpoint, error) {

	return func(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
		if isTruncatedArgs(args) {
			return singleChunk(truncationRefusal(tCtx)), nil
		}
		return endpoint(ctx, args, opts...)
	}, nil
}

// truncationRefusal is what the model reads instead of a broken call.
//
// ★ It says three things, because the model needs all three to recover: the
//
//	call was cut off, nothing was done, and the way out is a smaller call.
//	"unexpected end of JSON input" says none of them — it reads like a bug
//	in the tool, and the model's answer to that is to try the same call
//	again.
func truncationRefusal(tCtx *adk.ToolContext) string {
	name := ""
	if tCtx != nil {
		name = tCtx.Name
	}
	return fmt.Sprintf(
		"tool %q was refused: the model's output was cut off at its token limit "+
			"(finish_reason=length), so this call's arguments are incomplete and nothing was done. "+
			"Call it again with less in it: build a long file in pieces that each fit in one call "+
			"(config_write with append: true adds one piece to what is already on disk), "+
			"change one line with edit_file instead of replacing the whole file, "+
			"or give the agent a larger max_tokens.",
		name)
}
