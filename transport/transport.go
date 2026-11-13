// Package transport renders a run's events as AG-UI frames.
//
// ★ It renders; it does not derive. The derivation — folding the agent's
//
//	events into the messages a run archives — lives in agentkit/emit,
//	because it can only happen once and the run has to own it: a renderer
//	that derived would own the run's output, and the run would die with
//	the connection.
//
// ★ What is left here is the wire format and nothing else. The run's
//
//	events arrive on a channel; they go out as frames.
package transport

import (
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/google/uuid"

	"github.com/ouranoshong/openheron/contract"
)

// The failure codes the run reports on the wire.
//
// ★ They are the codes the client branches on. The provider's failures are
//
//	classified by the derivation (agentkit/emit); the turn's own failures
//	are the transport's.
const (
	CodeTurnSuperseded   = "turn_superseded"
	CodeTurnStartTimeout = "turn_start_timeout"
	// CodeAgentUnavailable says the agent could not be built.
	//
	// ★ It is not the start timeout: a timeout names a symptom, and this
	//   names the configuration problem behind it — the model that is not
	//   declared, the file that does not parse. The client shows the
	//   message; the code says "fix a file, do not retry".
	CodeAgentUnavailable = "agent_unavailable"
)

// Render writes the run's events as AG-UI frames.
//
// ★ It returns when the channel closes — which happens when the run ends.
//
//	A connection that dies does not stop the run: the run publishes to
//	its subscribers, and this is one of them.
//
// ★ The frames are the same as before the split, one for one. The split
//
//	changed who derives them, not what they say.
func Render(w io.Writer, threadID, runID string, ch <-chan contract.RunEvent) error {
	return RenderStream(w, threadID, runID, ch, nil)
}

// RenderStream is Render with the ability to tell a finished stream from a
// cut one.
//
// ★ A subscriber the run dropped is not a run that finished: the renderer
// must not close its stream with a terminal frame. The client reads a
// terminal frame as "the run is over, this is the whole outcome", and the
// approval it did not receive would then be invisible until a reload. An
// open-ended stream sends the client down its "the connection dropped, come
// back to the run" path instead, which reads the transcript and therefore
// the approval.
func RenderStream(w io.Writer, threadID, runID string, ch <-chan contract.RunEvent,
	truncated func() bool) error {

	if runID == "" {
		runID = uuid.NewString()
	}
	r := &renderer{w: w, threadID: threadID, runID: runID}
	r.emit(events.NewRunStartedEvent(threadID, runID))

	for ev := range ch {
		r.render(ev)
	}

	switch {
	case truncated != nil && truncated():
		// ★ No terminal frame: the stream was cut, and a "success" here
		//   would tell the client the run said everything it had to say.
	case len(r.interrupts) > 0:
		r.emit(events.NewRunFinishedEventWithOptions(threadID, runID,
			events.WithInterruptOutcome(r.interrupts)))
	case r.runErr != nil:
		// ★ The error frame already ended the stream. A success frame
		//   after it would tell the client the run finished cleanly.
	default:
		r.emit(events.NewRunFinishedEventWithOptions(threadID, runID,
			events.WithSuccessOutcome()))
	}
	return r.runErr
}

// errRunFailed marks that the run failed.
//
// ★ The renderer does not carry the run's error — the run knows it. The
//
//	renderer only needs to know that the stream ended badly, so that it
//	does not close the stream with a success frame.
var errRunFailed = errors.New("the run failed")

// ── renderer ─────────────────────────────────────────────────────────────

type renderer struct {
	w        io.Writer
	threadID string
	runID    string

	brokenFlag bool
	runErr     error

	// interrupts are the approvals the run is waiting for, in the wire's
	// shape. They ride along in the RUN_FINISHED frame's outcome.
	interrupts []types.Interrupt
}

// render turns one event into frames.
func (r *renderer) render(ev contract.RunEvent) {
	switch ev.Kind {
	case contract.RunEventTextStart:
		r.emitTextStart(ev.MsgID, ev.Role, ev.Owner)
	case contract.RunEventTextDelta:
		r.emitTextContent(ev.MsgID, ev.Text, ev.Sub)
	case contract.RunEventTextEnd:
		r.emitTextEnd(ev.MsgID)
	case contract.RunEventReasoningStart:
		r.emitReasoningStart(ev.MsgID, ev.Role)
	case contract.RunEventReasoningDelta:
		r.emitReasoningContent(ev.MsgID, ev.Text)
	case contract.RunEventReasoningEnd:
		r.emitReasoningEnd(ev.MsgID)
	case contract.RunEventToolStart:
		r.emitToolStart(ev.ToolID, ev.ToolName, ev.MsgID, ev.Sub)
	case contract.RunEventToolArgs:
		r.emitToolArgs(ev.ToolID, ev.Text, ev.Sub)
	case contract.RunEventToolEnd:
		r.emitToolEnd(ev.ToolID, ev.Sub)
	case contract.RunEventToolResult:
		r.emitToolResult(ev.Text, ev.ToolID, ev.Sub)
	case contract.RunEventAnnounce:
		r.announce(ev.MsgID, ev.Role, ev.Owner)
	case contract.RunEventCompaction:
		r.emitCompaction(ev.Agent, ev.At)
	case contract.RunEventContextUsage:
		r.emitContextUsage(ev.Usage)
	case contract.RunEventInterrupt:
		r.interrupts = append(r.interrupts, toWireInterrupts(ev.Interrupts)...)
	case contract.RunEventRetry:
		r.emitRetry(ev.Text, ev.Agent)
	case contract.RunEventError:
		r.emitRunError(ev.Text, ev.Code)
	}
}

// toWireInterrupts maps openheron's approvals onto the wire's shape.
//
// ★ The mapping is one for one, and it happens here because the wire's
//
//	types belong to the wire.
func toWireInterrupts(in []contract.Interrupt) []types.Interrupt {
	if len(in) == 0 {
		return nil
	}
	out := make([]types.Interrupt, 0, len(in))
	for _, i := range in {
		item := types.Interrupt{
			ID:       i.ID,
			Reason:   i.Reason,
			Message:  i.Message,
			Metadata: types.Metadata(i.Metadata),
		}
		if i.SubagentRunID != "" {
			item.SubagentRunID = i.SubagentRunID
		}
		out = append(out, item)
	}
	return out
}

func (r *renderer) emit(ev events.Event) {
	if r.brokenFlag {
		return
	}
	data, err := json.Marshal(ev)
	if err != nil {
		r.setRunErr(err)
		return
	}
	if _, err := r.w.Write(append(data, '\n')); err != nil {
		r.brokenFlag = true
	}
}

func (r *renderer) setRunErr(err error) {
	if r.runErr == nil {
		r.runErr = err
	}
}

// emitContextUsage publishes one agent's context occupancy.
//
// The payload is the whole snapshot for that agent: the client replaces, it
// does not merge, so a dropped event cannot leave a wrong number behind.
func (r *renderer) emitContextUsage(u contract.ContextUsage) {
	r.emit(events.NewCustomEvent("context_usage",
		events.WithValue(map[string]any{
			"agent":          u.Agent,
			"provider":       u.Provider,
			"model":          u.Model,
			"used_tokens":    u.UsedTokens,
			"context_window": u.ContextWindow,
			"summarize_at":   u.SummarizeAt,
			"updated_at":     u.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})))
}

// emitCompaction publishes the compaction that just happened.
//
// The event carries the fact, the agent and the time — not the numbers. The
// numbers (how many messages were superseded, how many tokens the summary
// saved) are measured against the transcript, and the transcript is the run
// loop's business: it writes the marker when it archives the turn. The
// client learns the numbers from the marker, on the next load.
func (r *renderer) emitCompaction(agent string, at time.Time) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	r.emit(events.NewCustomEvent("context_compacted",
		events.WithValue(map[string]any{
			"agent": agent,
			"at":    at.UTC().Format(time.RFC3339Nano),
		})))
}

// emitRetry publishes that the provider is retrying.
//
// ★ It is not an error and not a terminal frame: the run goes on, and the
//
//	client shows a note.
func (r *renderer) emitRetry(message, agent string) {
	value := map[string]any{"message": message}
	if agent != "" {
		value["agent"] = agent
	}
	r.emit(events.NewCustomEvent("model_retry", events.WithValue(value)))
}

// emitRunError publishes the run's failure.
//
// ★ The code is the classification the derivation made; the frame carries
//
//	it so the client can say something more useful than "it broke".
func (r *renderer) emitRunError(message, code string) {
	r.emit(events.NewRunErrorEvent(message,
		events.WithRunID(r.runID), events.WithErrorCode(code)))
	r.setRunErr(errRunFailed)
}

func (r *renderer) emitTextStart(msgID, role, name string) {
	ev := events.NewTextMessageStartEvent(msgID)
	if role != "" {
		ev.Role = &role
	}
	if name != "" {
		ev.Name = name
	}
	r.emit(ev)
}

func (r *renderer) emitTextContent(msgID, delta, sub string) {
	ev := events.NewTextMessageContentEvent(msgID, delta)
	ev.SubagentRunID = sub
	r.emit(ev)
}

func (r *renderer) emitTextEnd(msgID string) { r.emit(events.NewTextMessageEndEvent(msgID)) }

// ── reasoning ──

const reasoningRole = "reasoning"

func (r *renderer) emitReasoningStart(msgID, _ string) {
	r.emit(events.NewReasoningStartEvent(msgID))
	r.emit(events.NewReasoningMessageStartEvent(msgID, reasoningRole))
}

func (r *renderer) emitReasoningContent(msgID, delta string) {
	r.emit(events.NewReasoningMessageContentEvent(msgID, delta))
}

func (r *renderer) emitReasoningEnd(msgID string) {
	r.emit(events.NewReasoningMessageEndEvent(msgID))
	r.emit(events.NewReasoningEndEvent(msgID))
}

func (r *renderer) emitToolStart(callID, name, parentMsgID, sub string) {
	ev := events.NewToolCallStartEvent(callID, name)
	if parentMsgID != "" {
		p := parentMsgID
		ev.ParentMessageID = &p
	}
	ev.SubagentRunID = sub
	r.emit(ev)
}

func (r *renderer) emitToolArgs(callID, delta, sub string) {
	ev := events.NewToolCallArgsEvent(callID, delta)
	ev.SubagentRunID = sub
	r.emit(ev)
}

func (r *renderer) emitToolEnd(callID, sub string) {
	ev := events.NewToolCallEndEvent(callID)
	ev.SubagentRunID = sub
	r.emit(ev)
}

func (r *renderer) announce(messageID, role, owner string) {
	r.emitTextStart(messageID, role, owner)
	r.emitTextEnd(messageID)
}

func (r *renderer) emitToolResult(content, callID, sub string) {
	if callID == "" {
		callID = uuid.NewString()
	}
	ev := events.NewToolCallResultEvent(uuid.NewString(), callID, content)
	ev.SubagentRunID = sub
	r.emit(ev)
}
