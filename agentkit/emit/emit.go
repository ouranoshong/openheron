// Package emit derives a run's facts from the agent's event stream.
//
// ★ It is the half of the old transport that was never about the wire: the
//
//	part that reads the agent's events, folds them into the messages the
//	run archives, and says what happened in openheron's own vocabulary.
//	The transport kept the other half — turning those facts into AG-UI
//	frames — and now that is all it does.
//
// ★ The split exists because the derivation cannot be done twice. Folding
//
//	the model's stream chunks into one message consumes the stream, so
//	whoever reads the events owns the run's output. When the renderer
//	owned them, the SSE connection owned the run: a dead connection meant
//	a turn that never archived. Now the run reads its own events, and a
//	connection is one subscriber among the possible ones.
package emit

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/helpers"
	"github.com/ouranoshong/openheron/message"
)

// The failure codes the run reports.
//
// ★ They are the codes the client branches on, and they are about the
//
//	provider's failure — not the transport's. A second wire format would
//	still carry the same codes.
const (
	CodeModelAuth         = "model_auth"
	CodeModelRateLimit    = "model_rate_limit"
	CodeModelQuota        = "model_quota"
	CodeModelOverload     = "model_overload"
	CodeModelBadRequest   = "model_bad_request"
	CodeModelUpstream     = "model_upstream"
	CodeModelNetwork      = "model_network"
	CodeModelTimeout      = "model_timeout"
	CodeModelUnknown      = "model_unknown"
	CodeStreamRecvFailure = "stream_recv_failed"
)

// Source is the agent's event stream.
type Source interface {
	Next() (*adk.TypedAgentEvent[*schema.Message], bool)
}

// Sink receives the derived events as they happen.
//
// ★ A sink must not block. The derivation happens on the run's own
//
//	goroutine, and a run that waits for a slow subscriber is a run that
//	stalls because somebody's connection is slow — the coupling this
//	package exists to remove.
type Sink interface {
	Publish(contract.RunEvent)
}

// Options are the knobs the derivation needs.
type Options struct {
	// RunID is the run the events belong to. It rides along on every event
	// so a subscriber can tell them apart from another run's.
	RunID string
	// RootAgent is the root agent's name. A sub-agent's events are
	// attributed to it, and the root's compaction is the session's.
	RootAgent string
	// Contexts reports what each agent's context allows: the model's window
	// and the compaction threshold. The derivation needs the window to turn
	// a token count into a ratio; it must not resolve model refs itself —
	// the builder owns that lookup.
	Contexts map[string]contract.AgentContext
}

// Artifacts are what the run archives.
//
// ★ They are the run's facts; the events are what the subscribers watch.
//
//	Both come out of one pass over the agent's events, because that pass
//	can only happen once.
type Artifacts struct {
	// Intermediates are the tool-call and tool-result messages to archive.
	Intermediates []*schema.Message
	// Pending is the approval the run is waiting for, if any.
	Pending []contract.PendingInterrupt
	// Usage is the token accounting for this turn.
	Usage contract.Usage
	// Compaction is the message that replaced the conversation so far.
	Compaction *schema.Message
	// Stopped says the user stopped the run.
	Stopped bool
}

// Derive reads the agent's events and derives the run's artifacts.
//
// ★ The events are published to the sink as they are derived, so a
//
//	subscriber sees the run while it happens. The artifacts come back at
//	the end, because that is when the run knows what it produced.
//
// ★ cancel ends the derivation even when the agent does not. An abort has
//
//	to work on an agent that ignores the cancellation — otherwise the run
//	waits for an agent that will never stop, and the abort waits with it.
func Derive(src Source, opts Options, sink Sink, cancel <-chan struct{}) (Artifacts, error) {
	d := &deriver{
		opts:   opts,
		sink:   sink,
		events: make(chan *adk.TypedAgentEvent[*schema.Message], 64),
	}
	go d.pump(src, cancel)
	return d.run(cancel)
}

type deriver struct {
	opts Options
	sink Sink

	// events is the agent's stream, pumped by a goroutine.
	events chan *adk.TypedAgentEvent[*schema.Message]

	intermediates []*schema.Message
	pending       []contract.PendingInterrupt
	usage         contract.Usage
	compaction    *schema.Message
	stopped       bool
}

// pump moves the agent's events onto the deriver's channel.
//
// ★ It is a goroutine because the derivation has to be able to stop on
//
//	its own: an agent that ignores the cancellation would otherwise hold
//	the derivation — and with it the run — for ever.
//
// ★ The agent's stream still has exactly one reader at a time. The pump
//
//	is that reader; the framework's teardown may drain it afterwards, and
//	the events it takes are the ones the cancelled run no longer wants.
func (d *deriver) pump(src Source, cancel <-chan struct{}) {
	defer close(d.events)
	for {
		ev, ok := src.Next()
		if !ok {
			return
		}
		select {
		case d.events <- ev:
		case <-cancel:
			return
		}
	}
}

func (d *deriver) run(cancel <-chan struct{}) (Artifacts, error) {
	mainAgent := d.opts.RootAgent

	for {
		var ev *adk.TypedAgentEvent[*schema.Message]
		var ok bool
		select {
		case ev, ok = <-d.events:
			if !ok {
				d.usage = aggregateUsage(d.intermediates)
				return d.artifacts(), nil
			}
		case <-cancel:
			// ★ The run was cancelled: the agent may still be running,
			//   but this run is over.
			return d.artifacts(), context.Canceled
		}

		sub := ""
		if ev.AgentName != "" && ev.AgentName != mainAgent {
			sub = ev.AgentName
		}
		if ev.Err != nil {
			if errors.Is(ev.Err, context.Canceled) {
				// ★ The user stopped the run. It is not a failure: the run
				//   ends, and the row says "aborted".
				d.stopped = true
				return d.artifacts(), ev.Err
			}
			if helpers.IsModelRetryInProgress(ev.Err) {
				obs.Logger().Warn("[emit] model retry", "err", ev.Err)
				d.publish(contract.RunEvent{
					Kind:  contract.RunEventRetry,
					RunID: d.opts.RunID,
					Text:  ev.Err.Error(),
					Agent: ev.AgentName,
				})
				continue
			}
			d.publish(contract.RunEvent{
				Kind:  contract.RunEventError,
				RunID: d.opts.RunID,
				Text:  ev.Err.Error(),
				Code:  ErrorCode(ev.Err),
				Agent: ev.AgentName,
			})
			return d.artifacts(), ev.Err
		}

		if ev.Action != nil && ev.Action.CustomizedAction != nil {
			// Only the root agent's compaction is a boundary of the
			// session's history. A sub-agent compacts inside its own run,
			// and that summary must never reach the transcript: it would cut
			// the root's effective history at a sub-agent's summary.
			if ownerOf(ev, mainAgent) == mainAgent {
				if cm := compactionFrom(ev.Action.CustomizedAction); cm != nil {
					d.compaction = cm
					d.publish(contract.RunEvent{
						Kind:  contract.RunEventCompaction,
						RunID: d.opts.RunID,
						Agent: mainAgent,
						At:    time.Now().UTC(),
					})
				}
			}
		}

		if ev.Action != nil && ev.Action.Interrupted != nil {
			d.pending = pendingInterrupts(ev, mainAgent)
			d.publish(contract.RunEvent{
				Kind:       contract.RunEventInterrupt,
				RunID:      d.opts.RunID,
				Interrupts: interruptsFrom(ev, mainAgent),
				Pending:    d.pending,
			})
			return d.artifacts(), nil
		}

		hasExit := ev.Action != nil && ev.Action.Exit
		if hasOutput(ev) {
			mo := ev.Output.MessageOutput
			stamp := stampMessage
			if sub != "" {
				stamp = stampSubagentMessage
			}
			switch {
			case message.VariantIsToolResult(mo):
				content, callID, name := message.DrainToolResult(mo)
				d.publish(contract.RunEvent{
					Kind:   contract.RunEventToolResult,
					RunID:  d.opts.RunID,
					Text:   content,
					ToolID: callID,
					Sub:    sub,
				})
				out := []*schema.Message{message.NewToolResult(callID, name, content)}
				stamp(out[0], ownerOf(ev, mainAgent))
				d.intermediates = append(d.intermediates, out...)

			case mo.IsStreaming && mo.MessageStream != nil:
				so := d.renderStream(mo, sub, ownerOf(ev, mainAgent))
				for _, m := range so.intermediates {
					stamp(m, ownerOf(ev, mainAgent))
				}
				d.intermediates = append(d.intermediates, so.intermediates...)

			default:
				out := d.emitMessage(mo.Message, sub, ownerOf(ev, mainAgent))
				for _, m := range out {
					stamp(m, ownerOf(ev, mainAgent))
				}
				d.intermediates = append(d.intermediates, out...)
			}
		}

		// The occupancy changes with every model call, and a meter that only
		// moves at the end of a run is a meter nobody watches.
		if n := len(d.intermediates); n > 0 {
			if u, ok := contextUsageOfMessage(d.intermediates[n-1], d.opts.Contexts); ok {
				d.publish(contract.RunEvent{
					Kind:  contract.RunEventContextUsage,
					RunID: d.opts.RunID,
					Usage: u,
				})
			}
		}

		if hasExit {
			d.usage = aggregateUsage(d.intermediates)
			return d.artifacts(), nil
		}
	}
}

func (d *deriver) artifacts() Artifacts {
	return Artifacts{
		Intermediates: d.intermediates,
		Pending:       d.pending,
		Usage:         d.usage,
		Compaction:    d.compaction,
		Stopped:       d.stopped,
	}
}

// publish hands one event to the sink.
//
// ★ A sink that is gone publishes nothing: the run does not care whether
//
//	anybody is watching.
func (d *deriver) publish(ev contract.RunEvent) {
	if d.sink == nil {
		return
	}
	d.sink.Publish(ev)
}

// compactionFrom reads the compacted history off the summarization
// middleware's after_summarize event.
//
// The event carries the replacement history. openheron's finalizer leaves the
// summary alone in it (the agent's system prompt is transient and must not be
// persisted), so the last message of that history is the compaction.
//
// The derivation is where the agent's events are read, so this is where the
// compacted message is picked up on its way to the transcript.
func compactionFrom(action any) *schema.Message {
	a, ok := action.(*summarization.TypedCustomizedAction[*schema.Message])
	if !ok || a == nil || a.Type != summarization.ActionTypeAfterSummarize || a.After == nil {
		return nil
	}
	var out *schema.Message
	for _, m := range a.After.Messages {
		if m == nil {
			continue
		}
		// The replacement history keeps the agent's system messages — the
		// context prompt lives there. Only the summary is worth keeping:
		// it is what the agent carries forward, and the only part that
		// belongs in the transcript.
		if m.Role == schema.System {
			continue
		}
		if out != nil {
			obs.Logger().Warn("[emit] compaction carried more than one message; " +
				"keeping the last one")
		}
		out = m
	}
	return out
}

func hasOutput(ev *adk.TypedAgentEvent[*schema.Message]) bool {
	return ev.Output != nil && ev.Output.MessageOutput != nil
}

// interruptsFrom derives the approvals in the shape the wire wants.
//
// ★ The wire's own type lives in the transport; here the approvals are
//
//	openheron's facts, and the renderer maps them.
func interruptsFrom(ev *adk.TypedAgentEvent[*schema.Message], mainAgent string) []contract.Interrupt {
	var out []contract.Interrupt
	for _, ic := range ev.Action.Interrupted.InterruptContexts {
		if ic == nil {
			continue
		}
		item := contract.Interrupt{
			ID:       ic.ID,
			Reason:   "tool_call",
			Message:  describe(ic.Info),
			Metadata: approvalMetadata(ic.Info),
		}
		if sub := requestedBy(ic, mainAgent); sub != "" {
			item.SubagentRunID = sub
		}
		out = append(out, item)
	}
	return out
}

// pendingInterrupts derives the approvals in the shape the session stores.
func pendingInterrupts(ev *adk.TypedAgentEvent[*schema.Message], mainAgent string) []contract.PendingInterrupt {
	var pending []contract.PendingInterrupt
	for _, ic := range ev.Action.Interrupted.InterruptContexts {
		if ic == nil {
			continue
		}
		pending = append(pending, contract.PendingInterrupt{
			InterruptID: ic.ID,
			Detail:      interruptDetailOf(ic, requestedBy(ic, mainAgent)),
		})
	}
	return pending
}

// renderStream folds the model's stream into one message.
//
// ★ It is the one consumer of the model's stream: the chunks cannot be
//
//	read twice, so this pass is the run's, not the renderer's.
func (d *deriver) renderStream(mo *adk.TypedMessageVariant[*schema.Message], sub, owner string) streamOutcome {
	st := newStreamState(d, mo.Role, sub, owner)

	for {
		chunk, recvErr := mo.MessageStream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			if helpers.IsModelRetryInProgress(recvErr) {
				obs.Logger().Warn("[emit] model retry", "err", recvErr)
				d.publish(contract.RunEvent{
					Kind:  contract.RunEventRetry,
					RunID: d.opts.RunID,
					Text:  recvErr.Error(),
				})
				return streamOutcome{}
			}
			obs.Logger().Error("[emit] stream recv failed", "err", recvErr)
			d.publish(contract.RunEvent{
				Kind:  contract.RunEventError,
				RunID: d.opts.RunID,
				Text:  recvErr.Error(),
				Code:  CodeStreamRecvFailure,
			})
			break
		}

		st.consume(chunk)
	}

	return st.finish()
}

type streamState struct {
	d     *deriver
	role  schema.RoleType
	msgID string
	sub   string
	owner string

	textOpen           bool
	reasonOpen         bool
	textSegmentStarted bool
	hasContent         bool

	acc    strings.Builder
	chunks []*schema.Message
	calls  map[int]*openCall
	order  []int

	// The reasoning's duration is measured here, because the derivation is
	// what knows when the reasoning started and ended.
	reasonStart map[string]int64
	reasonEnd   map[string]int64
}

func newStreamState(d *deriver, role schema.RoleType, sub, owner string) *streamState {
	return &streamState{
		d:           d,
		role:        role,
		msgID:       uuid.NewString(),
		sub:         sub,
		owner:       owner,
		calls:       map[int]*openCall{},
		reasonStart: map[string]int64{},
		reasonEnd:   map[string]int64{},
	}
}

func (s *streamState) consume(chunk *schema.Message) {
	s.chunks = append(s.chunks, chunk)

	s.consumeToolCalls(chunk)

	if reason := message.AssistantDeltaReasoning(chunk); reason != "" {
		s.consumeReasoning(reason)
		return
	}
	s.consumeText(message.AssistantDeltaText(chunk))
}

func (s *streamState) consumeToolCalls(chunk *schema.Message) {
	for _, tc := range message.ToolCalls(chunk) {
		c, seen := s.calls[tc.Index]
		if !seen {
			c = &openCall{id: toolCallID(tc, s.msgID, tc.Index), name: tc.Name}
			s.calls[tc.Index] = c
			s.order = append(s.order, tc.Index)
			s.closeAll()
			s.hasContent = true
			s.publish(contract.RunEvent{
				Kind:     contract.RunEventToolStart,
				RunID:    s.d.opts.RunID,
				ToolID:   c.id,
				ToolName: c.name,
				MsgID:    s.msgID,
				Sub:      s.sub,
			})
		}
		if tc.Name != "" && c.name == "" {
			c.name = tc.Name
		}
		if tc.ID != "" {
			c.externalID = tc.ID
		}
		if tc.Args != "" {
			s.publish(contract.RunEvent{
				Kind:   contract.RunEventToolArgs,
				RunID:  s.d.opts.RunID,
				ToolID: c.id,
				Text:   tc.Args,
				Sub:    s.sub,
			})
			c.args.WriteString(tc.Args)
		}
	}
}

func (s *streamState) consumeReasoning(reason string) {
	if !s.reasonOpen {
		s.closeText()
		s.hasContent = true
		s.markReasonStart(s.msgID)
		s.publish(contract.RunEvent{
			Kind:  contract.RunEventReasoningStart,
			RunID: s.d.opts.RunID,
			MsgID: s.msgID,
		})
		s.reasonOpen = true
	}
	s.publish(contract.RunEvent{
		Kind:  contract.RunEventReasoningDelta,
		RunID: s.d.opts.RunID,
		MsgID: s.msgID,
		Text:  reason,
	})
}

func (s *streamState) consumeText(text string) {
	if text == "" {
		return
	}
	if !s.textOpen {
		s.closeReasoning()
		s.publish(contract.RunEvent{
			Kind:  contract.RunEventTextStart,
			RunID: s.d.opts.RunID,
			MsgID: s.msgID,
			Role:  roleOf(s.role),
			Owner: s.owner,
		})
		s.textOpen = true
		s.textSegmentStarted = true
		s.hasContent = true
	}
	s.acc.WriteString(text)
	s.publish(contract.RunEvent{
		Kind:  contract.RunEventTextDelta,
		RunID: s.d.opts.RunID,
		MsgID: s.msgID,
		Text:  text,
		Sub:   s.sub,
	})
}

func (s *streamState) closeText() {
	if s.textOpen {
		s.publish(contract.RunEvent{
			Kind:  contract.RunEventTextEnd,
			RunID: s.d.opts.RunID,
			MsgID: s.msgID,
		})
		s.textOpen = false
	}
}

func (s *streamState) closeReasoning() {
	if s.reasonOpen {
		s.markReasonEnd(s.msgID)
		s.publish(contract.RunEvent{
			Kind:  contract.RunEventReasoningEnd,
			RunID: s.d.opts.RunID,
			MsgID: s.msgID,
		})
		s.reasonOpen = false
	}
}

func (s *streamState) closeAll() {
	s.closeReasoning()
	s.closeText()
}

func (s *streamState) finish() streamOutcome {
	s.closeAll()
	for _, idx := range s.order {
		s.publish(contract.RunEvent{
			Kind:   contract.RunEventToolEnd,
			RunID:  s.d.opts.RunID,
			ToolID: s.calls[idx].id,
			Sub:    s.sub,
		})
	}
	if s.hasContent && !s.textSegmentStarted {
		s.publish(contract.RunEvent{
			Kind:  contract.RunEventAnnounce,
			RunID: s.d.opts.RunID,
			MsgID: s.msgID,
			Role:  roleOf(s.role),
			Owner: s.owner,
		})
	}

	var out streamOutcome
	out.lastContent = s.acc.String()
	switch {
	case len(s.chunks) > 0:
		if merged, err := message.ConcatChunks(s.chunks); err == nil && message.HasContent(merged) {
			out.intermediates = append(out.intermediates, merged)
		} else {
			out.intermediates = append(out.intermediates,
				message.NewAssistant(s.acc.String(), collectedCalls(s.calls, s.order)))
		}
	case s.acc.Len() > 0 || len(s.order) > 0:
		out.intermediates = append(out.intermediates,
			message.NewAssistant(s.acc.String(), collectedCalls(s.calls, s.order)))
	}
	for _, m := range out.intermediates {
		message.StampReasoningMs(m, s.reasoningMs(s.msgID))
	}
	return out
}

// publish hands one event to the derivation's sink.
func (s *streamState) publish(ev contract.RunEvent) {
	s.d.publish(ev)
}

// reasoningMs measures how long the reasoning took.
//
// ★ The measurement lives with the derivation because the derivation is what
//
//	sees the start and the end. The renderer would have to carry the same
//	timestamps across the wire to learn the same number.
func (s *streamState) reasoningMs(msgID string) int64 {
	start, ok := s.reasonStart[msgID]
	if !ok {
		return 0
	}
	end, ok := s.reasonEnd[msgID]
	if !ok {
		return 0
	}
	return end - start
}

// markReasonStart records when the reasoning started.
func (s *streamState) markReasonStart(msgID string) {
	if _, ok := s.reasonStart[msgID]; !ok {
		s.reasonStart[msgID] = time.Now().UnixMilli()
	}
}

// markReasonEnd records when the reasoning ended.
func (s *streamState) markReasonEnd(msgID string) {
	s.reasonEnd[msgID] = time.Now().UnixMilli()
}

func (d *deriver) emitMessage(msg *schema.Message, sub, owner string) []*schema.Message {
	if msg == nil {
		return nil
	}
	reasonID := ""
	if reason := message.AssistantDeltaReasoning(msg); reason != "" {
		reasonID = uuid.NewString()
		d.publish(contract.RunEvent{
			Kind:  contract.RunEventReasoningStart,
			RunID: d.opts.RunID,
			MsgID: reasonID,
		})
		d.publish(contract.RunEvent{
			Kind:  contract.RunEventReasoningDelta,
			RunID: d.opts.RunID,
			MsgID: reasonID,
			Text:  reason,
		})
		d.publish(contract.RunEvent{
			Kind:  contract.RunEventReasoningEnd,
			RunID: d.opts.RunID,
			MsgID: reasonID,
		})
	}
	msgID := uuid.NewString()
	text := message.AssistantText(msg)
	calls := message.ToolCalls(msg)

	if text != "" {
		d.publish(contract.RunEvent{
			Kind:  contract.RunEventTextStart,
			RunID: d.opts.RunID,
			MsgID: msgID,
			Role:  roleOf(msg.Role),
			Owner: owner,
		})
		d.publish(contract.RunEvent{
			Kind:  contract.RunEventTextDelta,
			RunID: d.opts.RunID,
			MsgID: msgID,
			Text:  text,
			Sub:   sub,
		})
		d.publish(contract.RunEvent{
			Kind:  contract.RunEventTextEnd,
			RunID: d.opts.RunID,
			MsgID: msgID,
		})
	}
	for _, tc := range calls {
		id := firstNonEmpty(tc.ID, uuid.NewString())
		d.publish(contract.RunEvent{
			Kind:     contract.RunEventToolStart,
			RunID:    d.opts.RunID,
			ToolID:   id,
			ToolName: tc.Name,
			MsgID:    msgID,
			Sub:      sub,
		})
		if tc.Args != "" {
			d.publish(contract.RunEvent{
				Kind:   contract.RunEventToolArgs,
				RunID:  d.opts.RunID,
				ToolID: id,
				Text:   tc.Args,
				Sub:    sub,
			})
		}
		d.publish(contract.RunEvent{
			Kind:   contract.RunEventToolEnd,
			RunID:  d.opts.RunID,
			ToolID: id,
			Sub:    sub,
		})
	}
	if reasonID != "" {
		d.publish(contract.RunEvent{
			Kind:  contract.RunEventAnnounce,
			RunID: d.opts.RunID,
			MsgID: reasonID,
			Role:  roleOf(msg.Role),
			Owner: owner,
		})
	}
	if text == "" && len(calls) > 0 {
		d.publish(contract.RunEvent{
			Kind:  contract.RunEventAnnounce,
			RunID: d.opts.RunID,
			MsgID: msgID,
			Role:  roleOf(msg.Role),
			Owner: owner,
		})
	}
	if message.HasContent(msg) {
		return []*schema.Message{msg}
	}
	return nil
}

func collectedCalls(calls map[int]*openCall, order []int) []message.ToolCall {
	out := make([]message.ToolCall, 0, len(order))
	for _, idx := range order {
		c := calls[idx]
		out = append(out, message.ToolCall{
			ID:    firstNonEmpty(c.externalID, c.id),
			Name:  c.name,
			Args:  c.args.String(),
			Index: idx,
		})
	}
	return out
}

func roleOf(r schema.RoleType) string {
	switch r {
	case schema.User:
		return "user"
	case schema.System:
		return "developer"
	case schema.Tool:
		return "tool"
	case schema.Assistant, "":
		return "assistant"
	default:
		return "assistant"
	}
}

func toolCallID(tc message.ToolCall, msgID string, idx int) string {
	if tc.ID != "" {
		return tc.ID
	}
	return msgID + ":tc" + strconv.Itoa(idx)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

type openCall struct {
	id         string
	externalID string
	name       string
	args       strings.Builder
}

type streamOutcome struct {
	lastContent   string
	intermediates []*schema.Message
}

// ErrorCode classifies a provider's failure.
//
// ★ The classification is a guess from the message: there is no typed
//
//	error from the provider. The client branches on the code, so the codes
//	are part of the contract and live next to the failure they describe.
func ErrorCode(err error) string {
	if err == nil {
		return CodeModelUnknown
	}
	s := err.Error()
	switch {
	case containsAny(s, "quota"):
		return CodeModelQuota
	case containsAny(s, "401", "403", "auth"):
		return CodeModelAuth
	case containsAny(s, "429", "rate_limit", "Too Many Requests"):
		return CodeModelRateLimit
	case containsAny(s, "529", "503", "overload"):
		return CodeModelOverload
	case containsAny(s, "network", "TLS", "dial", "certificate"):
		return CodeModelNetwork
	case containsAny(s, "timed out", "timeout", "DeadlineExceeded"):
		return CodeModelTimeout
	case containsAny(s, "bad_request", "400"):
		return CodeModelBadRequest
	case containsAny(s, "upstream"):
		return CodeModelUpstream
	default:
		return CodeModelUnknown
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
