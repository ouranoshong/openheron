package build

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

// malformedMarker is the one thing a call whose arguments arrived broken
// carries.
//
// ★ The arguments of a tool call are one JSON object, and a provider can
//
//	deliver them in a shape no tool can read: cut off in the middle of a
//	string, or with a piece of itself repeated at the end. ★ The second
//	one is the reason this marker stands beside the truncation marker: the
//	arguments *look* complete — they end with a closing brace — and the
//	only thing wrong with them is that the last fragment is there twice.
//	Without a marker of its own such a call looks like a call that is
//	merely broken, and the tool answers with the Go JSON error, which says
//	nothing the model can act on.
const malformedMarker = "__malformed_arguments"

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

// markMalformedArgs returns the arguments of a broken tool call as JSON
// that says so.
//
// ★ The broken text is dropped: nothing in it can be read, and a tool that
//
//	could read half of a `content` value would write half a file. What the
//	model gets is the reason, not the wreck.
func markMalformedArgs() string {
	return `{"` + malformedMarker + `":true}`
}

// isTruncatedArgs reports whether the arguments carry the marker.
//
// ★ The check is a substring and not a parse. The marker stands for a
//
//	call whose fragments were concatenated on the way here, and the
//	concatenation of two markers is not JSON — so a parse would fail on
//	exactly the calls this has to catch.
func isTruncatedArgs(args string) bool {
	return strings.Contains(args, `"`+truncationMarker+`":true`)
}

// isMalformedArgs reports whether the arguments carry the marker.
func isMalformedArgs(args string) bool {
	return strings.Contains(args, `"`+malformedMarker+`":true`)
}

// isJSONObject reports whether the arguments are one complete JSON object.
//
// ★ "One" is the whole point: a call whose arguments arrived with their
//
//	own tail twice parses as an object **and** as the tail of one, and
//	only the whole string tells the two apart.
func isJSONObject(args string) bool {
	trimmed := strings.TrimSpace(args)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var obj map[string]json.RawMessage
	return json.Unmarshal([]byte(trimmed), &obj) == nil
}

// repairDuplicatedTail removes a tail that arrived twice.
//
// ★ A call can carry arguments that are a complete object **plus** a piece
//
//	of that object repeated at the end: the string ends with a fragment of
//	itself, and the object in front of the repeat is what the model meant
//	to send. The tail is dropped and the object is what the tool gets.
//
// ★ The test is strict, because the alternative is guessing: the leftover
//
//	must be a **suffix** of the part in front of it. A stray word, a second
//	object, a string cut in half — none of those is a repeat, and a call
//	that is broken in another way must not be repaired into something the
//	model never said.
func repairDuplicatedTail(args string) (string, bool) {
	trimmed := strings.TrimSpace(args)
	if !strings.HasPrefix(trimmed, "{") {
		return "", false
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	var obj map[string]json.RawMessage
	if err := dec.Decode(&obj); err != nil {
		return "", false
	}
	off := int(dec.InputOffset())
	prefix := trimmed[:off]
	rest := strings.TrimLeft(trimmed[off:], " \t\r\n")
	if rest == "" {
		return "", false
	}
	// ★ The prefix has to be the whole object on its own: a decoder that
	//   stopped in the middle would leave a prefix that does not parse,
	//   and repairing to that would hand the tool half an object.
	var check map[string]json.RawMessage
	if err := json.Unmarshal([]byte(prefix), &check); err != nil {
		return "", false
	}
	if !strings.HasSuffix(prefix, rest) {
		return "", false
	}
	return prefix, true
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

// callGuardModel makes sure the tool calls of a generation carry arguments
// a tool can read.
//
// ★ The model is the only layer that knows. The cut is visible as the
//
//	provider's finish reason, and everything below it — the tool, the
//	editor, the ledger — sees a call that looks complete.
type callGuardModel struct {
	inner model.ToolCallingChatModel
}

func newCallGuardModel(inner model.ToolCallingChatModel) model.ToolCallingChatModel {
	return &callGuardModel{inner: inner}
}

func (m *callGuardModel) Generate(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.Message, error) {

	msg, err := m.inner.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	if finishReasonOf(msg) == finishReasonLength {
		markCallsTruncated(msg)
		return msg, nil
	}
	guardCalls(msg)
	return msg, nil
}

func (m *callGuardModel) Stream(ctx context.Context, in []*schema.Message,
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

		// ★ release sends the call **once**. ★ A chunk can carry the
		//	finish reason and the call at the same time — a provider that
		//	closes the turn on the same frame as the last fragment of the
		//	call — and such a chunk belongs to the call's fragments, so it
		//	joins them here instead of being sent a second time. ★ Sending
		//	it twice is how the arguments of a call arrive with their own
		//	tail twice: the framework concatenates what it gets, and the
		//	last fragment lands twice.
		release := func(cut bool, last *schema.Message) {
			chunks := held
			if last != nil && len(last.ToolCalls) > 0 {
				chunks = append(append(make([]*schema.Message, 0, len(held)+1), held...), last)
			}
			if cut {
				markChunksTruncated(chunks)
			} else {
				guardChunks(chunks)
			}
			for _, h := range chunks {
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
				//	about the call, so it goes as it is: the guard must
				//	not refuse a call it cannot say anything about.
				release(false, nil)
				_ = sw.Send(nil, rerr)
				return
			}
			if msg == nil {
				continue
			}
			reason := finishReasonOf(msg)
			// ★ A chunk that carries the reason is not held: it goes out
			//	with the held ones, once.
			if len(msg.ToolCalls) > 0 && reason == "" {
				held = append(held, msg)
				continue
			}
			if reason == "" {
				if sw.Send(msg, nil) {
					return
				}
				continue
			}
			cut := reason == finishReasonLength
			release(cut, msg)
			// ★ The chunk that carried the call has already gone out with
			//	the held ones; a chunk that carried only the reason has
			//	not.
			if len(msg.ToolCalls) == 0 {
				if sw.Send(msg, nil) {
					return
				}
			}
		}
	}()

	return out, nil
}

func (m *callGuardModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &callGuardModel{inner: inner}, nil
}

func (m *callGuardModel) GetType() string {
	if t, ok := m.inner.(interface{ GetType() string }); ok {
		return t.GetType()
	}
	return ""
}

// callFragment is one piece of a tool call's arguments, and where it sits.
type callFragment struct {
	chunk int
	call  int
	text  string
}

// fragmentsOf groups the tool call arguments the chunks carry by the call
// they belong to.
//
// ★ The index says which call a fragment belongs to: a turn can carry
//
//	several calls at once, and the framework groups them the same way.
func fragmentsOf(chunks []*schema.Message) map[int][]callFragment {
	out := map[int][]callFragment{}
	for ci, m := range chunks {
		if m == nil {
			continue
		}
		for ti := range m.ToolCalls {
			idx := 0
			if m.ToolCalls[ti].Index != nil {
				idx = *m.ToolCalls[ti].Index
			}
			out[idx] = append(out[idx], callFragment{
				chunk: ci,
				call:  ti,
				text:  m.ToolCalls[ti].Function.Arguments,
			})
		}
	}
	return out
}

// assemble joins the fragments of one call back into the string the
// framework will hand to the tool.
func assemble(frags []callFragment) string {
	var sb strings.Builder
	for _, f := range frags {
		sb.WriteString(f.text)
	}
	return sb.String()
}

// spread puts a text back over the fragments it came from.
//
// ★ The fragments are what the framework concatenates, so a repaired text
//
//	has to be given back in the same pieces: the client renders the
//	arguments as they arrive.
func spread(chunks []*schema.Message, frags []callFragment, text string) {
	off := 0
	for _, f := range frags {
		end := off + len(f.text)
		if end > len(text) {
			end = len(text)
		}
		chunks[f.chunk].ToolCalls[f.call].Function.Arguments = text[off:end]
		off = end
	}
}

// replace puts one text where the fragments of a call were.
//
// ★ A call that is broken has to be replaced by **one** text: a marker per
//
//	fragment would concatenate into a string that is not JSON any more, and
//	nothing downstream could read it.
func replace(chunks []*schema.Message, frags []callFragment, text string) {
	for i, f := range frags {
		if i == len(frags)-1 {
			chunks[f.chunk].ToolCalls[f.call].Function.Arguments = text
			continue
		}
		chunks[f.chunk].ToolCalls[f.call].Function.Arguments = ""
	}
}

// guardCalls makes sure every tool call of a message carries arguments a
// tool can read.
//
// ★ Three things can happen to the arguments of a call: they arrive whole,
//
//	they arrive with a piece of themselves repeated at the end, or they
//	arrive broken. The second is repaired — the object in front of the
//	repeat is what the model meant to send. The third is marked, and the
//	guard refuses the call with a reason.
func guardCalls(msg *schema.Message) {
	if msg == nil {
		return
	}
	for i := range msg.ToolCalls {
		args := msg.ToolCalls[i].Function.Arguments
		if args == "" || isJSONObject(args) {
			continue
		}
		if fixed, ok := repairDuplicatedTail(args); ok {
			msg.ToolCalls[i].Function.Arguments = fixed
			continue
		}
		msg.ToolCalls[i].Function.Arguments = markMalformedArgs()
	}
}

// guardChunks is guardCalls for the fragments of a stream.
//
// ★ The check runs on the concatenation: a fragment on its own is not a
//
//	JSON object, and the fault this guards against only shows up in the
//	whole — a call whose arguments arrived with their own tail twice is
//	complete JSON apart from the tail.
func guardChunks(chunks []*schema.Message) {
	for _, frags := range fragmentsOf(chunks) {
		text := assemble(frags)
		if text == "" || isJSONObject(text) {
			continue
		}
		if fixed, ok := repairDuplicatedTail(text); ok {
			spread(chunks, frags, fixed)
			continue
		}
		replace(chunks, frags, markMalformedArgs())
	}
}

// markChunksTruncated marks the last fragment of every cut-off call and
// drops the fragments before it.
//
// ★ The fragments are what the framework concatenates, so marking every
//
//	one of them would concatenate the marker N times — and the result is
//	not JSON, so nothing downstream could read the marker. ★ The call has
//	to arrive as one marker.
func markChunksTruncated(chunks []*schema.Message) {
	for _, frags := range fragmentsOf(chunks) {
		text := assemble(frags)
		replace(chunks, frags, markTruncatedArgs(text))
	}
}

// newCallGuardMiddleware refuses a tool call whose arguments were cut off
// by the model's own output limit, and repairs one that arrived with a
// piece of itself repeated.
//
// ★ It sits **first** in the chain, so it sees the call before the approval
//
//	gate does: a broken call must not reach a human as a card to approve,
//	and it must not run either. ★ The answer is a message rather than an
//	error, because a model that reads "the call was cut off" can pick
//	another way — and the way it needs is a smaller one.
func newCallGuardMiddleware() adk.TypedChatModelAgentMiddleware[*schema.Message] {
	return &callGuardMiddleware{
		TypedBaseChatModelAgentMiddleware: &adk.TypedBaseChatModelAgentMiddleware[*schema.Message]{},
	}
}

type callGuardMiddleware struct {
	*adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
}

func (m *callGuardMiddleware) WrapInvokableToolCall(
	_ context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {

	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		reply, runArgs, ok := guardArgs(tCtx, args)
		if !ok {
			return reply, nil
		}
		return endpoint(ctx, runArgs, opts...)
	}, nil
}

func (m *callGuardMiddleware) WrapStreamableToolCall(
	_ context.Context, endpoint adk.StreamableToolCallEndpoint, tCtx *adk.ToolContext,
) (adk.StreamableToolCallEndpoint, error) {

	return func(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
		reply, runArgs, ok := guardArgs(tCtx, args)
		if !ok {
			// The refusal is a message to the model, not an error: the
			// synchronous path says so too, and a model that reads "the tool
			// was rejected" can pick another way instead of falling over.
			return singleChunk(reply), nil
		}
		return endpoint(ctx, runArgs, opts...)
	}, nil
}

// guardArgs is the guard's one decision.
//
// ★ It returns the arguments to run with and true when the call may run,
//
//	and the model's reply and false when it may not. ★ A call whose
//	arguments arrived with a piece of themselves repeated is repaired and
//	runs: the object in front of the repeat is what the model meant to
//	send, and the model cannot fix a transport that mangles its output.
func guardArgs(tCtx *adk.ToolContext, args string) (reply string, runArgs string, ok bool) {
	if isTruncatedArgs(args) {
		return truncationRefusal(tCtx), "", false
	}
	if isMalformedArgs(args) {
		return malformedRefusal(tCtx), "", false
	}
	if args == "" || isJSONObject(args) {
		return "", args, true
	}
	if fixed, ok := repairDuplicatedTail(args); ok {
		return "", fixed, true
	}
	return malformedRefusal(tCtx), "", false
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

// malformedRefusal is what the model reads instead of arguments that are
// not one JSON object.
//
// ★ It names the two ways this happens and the way out of each, because
//
//	"invalid character '\"' after top-level value" names neither: it reads
//	like a bug in the tool, and the model's answer to that is to try the
//	same call again — and again.
func malformedRefusal(tCtx *adk.ToolContext) string {
	name := ""
	if tCtx != nil {
		name = tCtx.Name
	}
	return fmt.Sprintf(
		"tool %q was refused: its arguments did not arrive as one complete JSON object, "+
			"so nothing was done. The call was either cut off in transit or arrived with a piece "+
			"of itself repeated at the end. Send it again; if it keeps failing, send less in one "+
			"call (build a long file in pieces with config_write append: true, or change one line "+
			"with edit_file instead of replacing the whole file).",
		name)
}
