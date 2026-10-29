package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/helpers"
	"github.com/ouranoshong/openheron/message"
)

type Options struct {
	RunID     string
	RootAgent string
	// Contexts reports what each agent's context allows: the model's window
	// and the compaction threshold. The transport needs the window to turn a
	// token count into a ratio; it must not resolve model refs itself — the
	// builder owns that lookup.
	Contexts map[string]contract.AgentContext
}

type Result struct {
	Intermediates []*schema.Message
	Interrupts    []types.Interrupt
	Pending       []contract.PendingInterrupt
	Usage         contract.Usage
	// Compaction is the message that replaced the conversation so far, when
	// the context was compacted during this turn. It is written to the
	// transcript before the turn's own messages, because it summarizes
	// everything that came before them.
	Compaction   *schema.Message
	StreamBroken bool
	Stopped      bool
}

const (
	CodeTurnSuperseded    = "turn_superseded"
	CodeTurnStartTimeout  = "turn_start_timeout"
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

type EventSource interface {
	Next() (*adk.TypedAgentEvent[*schema.Message], bool)
}

func Stream(w io.Writer, turn contract.TurnView, opts Options) (Result, error) {
	if turn == nil {
		return Result{}, errors.New("transport: nil turn")
	}
	return RunStream(w, turn.SessionID(), turn.Events(), opts)
}

func RunStream(w io.Writer, threadID string, src EventSource, opts Options) (Result, error) {
	runID := opts.RunID
	if runID == "" {
		runID = uuid.NewString()
	}

	e := &emitter{w: w, threadID: threadID, runID: runID}
	e.emit(events.NewRunStartedEvent(threadID, runID))

	res := streamEvents(e, src, opts)

	switch {
	case len(res.Interrupts) > 0:
		e.emit(events.NewRunFinishedEventWithOptions(threadID, runID,
			events.WithInterruptOutcome(res.Interrupts)))
	case e.runErr != nil:
	default:
		e.emit(events.NewRunFinishedEventWithOptions(threadID, runID,
			events.WithSuccessOutcome()))
	}
	return res, e.runErr
}

func streamEvents(e *emitter, src EventSource, opts Options) Result {
	var res Result
	mainAgent := opts.RootAgent

	for {
		ev, ok := src.Next()
		if !ok {
			res.Usage = aggregateUsage(res.Intermediates)
			return res
		}

		sub := ""
		if ev.AgentName != "" && ev.AgentName != mainAgent {
			sub = ev.AgentName
		}
		if ev.Err != nil {
			if errors.Is(ev.Err, context.Canceled) {
				res.Stopped = true
				e.setRunErr(ev.Err)
				return res
			}
			if helpers.IsModelRetryInProgress(ev.Err) {
				obs.Logger().Warn("[transport] model retry", "err", ev.Err)
				e.emit(events.NewCustomEvent("model_retry",
					events.WithValue(map[string]any{
						"message": ev.Err.Error(),
						"agent":   ev.AgentName,
					})))
				continue
			}
			e.emit(events.NewRunErrorEvent(ev.Err.Error(),
				events.WithRunID(e.runID), events.WithErrorCode(errorCode(ev.Err))))
			e.setRunErr(ev.Err)
			res.StreamBroken = e.broken()
			return res
		}

		if ev.Action != nil && ev.Action.CustomizedAction != nil {
			// Only the root agent's compaction is a boundary of the
			// session's history. A sub-agent compacts inside its own run,
			// and that summary must never reach the transcript: it would cut
			// the root's effective history at a sub-agent's summary.
			if ownerOf(ev, mainAgent) == mainAgent {
				if cm := compactionFrom(ev.Action.CustomizedAction); cm != nil {
					res.Compaction = cm
					e.emitCompaction(mainAgent)
				}
			}
		}

		if ev.Action != nil && ev.Action.Interrupted != nil {
			res.Interrupts, res.Pending = interruptsFrom(ev, mainAgent)
			res.StreamBroken = e.broken()
			return res
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
				e.emitToolResult(content, callID, sub)
				out := []*schema.Message{message.NewToolResult(callID, name, content)}
				stamp(out[0], ownerOf(ev, mainAgent))
				res.Intermediates = append(res.Intermediates, out...)

			case mo.IsStreaming && mo.MessageStream != nil:
				so := renderStream(e, mo, sub, ownerOf(ev, mainAgent))
				for _, m := range so.intermediates {
					stamp(m, ownerOf(ev, mainAgent))
				}
				res.Intermediates = append(res.Intermediates, so.intermediates...)
				res.StreamBroken = e.broken()

			default:
				out := emitMessage(e, mo.Message, sub, ownerOf(ev, mainAgent))
				for _, m := range out {
					stamp(m, ownerOf(ev, mainAgent))
				}
				res.Intermediates = append(res.Intermediates, out...)
			}
		}

		// The occupancy changes with every model call, and a meter that only
		// moves at the end of a run is a meter nobody watches.
		if n := len(res.Intermediates); n > 0 {
			if u, ok := contextUsageOfMessage(res.Intermediates[n-1], opts.Contexts); ok {
				e.emitContextUsage(u)
			}
		}

		if hasExit {
			res.StreamBroken = e.broken()
			res.Usage = aggregateUsage(res.Intermediates)
			return res
		}
	}
}

// compactionFrom reads the compacted history off the summarization
// middleware's after_summarize event.
//
// The event carries the replacement history. openheron's finalizer leaves the
// summary alone in it (the agent's system prompt is transient and must not be
// persisted), so the last message of that history is the compaction.
//
// The transport is where the agent's events are read, so this is where the
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
			obs.Logger().Warn("[transport] compaction carried more than one message; " +
				"keeping the last one")
		}
		out = m
	}
	return out
}

func hasOutput(ev *adk.TypedAgentEvent[*schema.Message]) bool {
	return ev.Output != nil && ev.Output.MessageOutput != nil
}

func interruptsFrom(ev *adk.TypedAgentEvent[*schema.Message], mainAgent string) ([]types.Interrupt, []contract.PendingInterrupt) {
	var out []types.Interrupt
	var pending []contract.PendingInterrupt
	for _, ic := range ev.Action.Interrupted.InterruptContexts {
		if ic == nil {
			continue
		}
		item := types.Interrupt{
			ID:       ic.ID,
			Reason:   "tool_call",
			Message:  describe(ic.Info),
			Metadata: approvalMetadata(ic.Info),
		}
		if sub := requestedBy(ic, mainAgent); sub != "" {
			item.SubagentRunID = sub
		}
		out = append(out, item)
		pending = append(pending, contract.PendingInterrupt{
			InterruptID: ic.ID,
			Detail:      interruptDetailOf(ic, requestedBy(ic, mainAgent)),
		})
	}
	return out, pending
}

func renderStream(e *emitter, mo *adk.TypedMessageVariant[*schema.Message], sub, owner string) streamOutcome {
	st := newStreamState(e, mo.Role, sub, owner)

	for {
		chunk, recvErr := mo.MessageStream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			if helpers.IsModelRetryInProgress(recvErr) {
				obs.Logger().Warn("[transport] model retry", "err", recvErr)
				e.emit(events.NewCustomEvent("model_retry",
					events.WithValue(map[string]any{"message": recvErr.Error()})))
				return streamOutcome{}
			}
			obs.Logger().Error("[transport] stream recv failed", "err", recvErr)
			e.emit(events.NewRunErrorEvent(recvErr.Error(),
				events.WithRunID(e.runID), events.WithErrorCode(CodeStreamRecvFailure)))
			e.setRunErr(recvErr)
			break
		}

		st.consume(chunk)
	}

	return st.finish()
}

type streamState struct {
	e     *emitter
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
}

func newStreamState(e *emitter, role schema.RoleType, sub, owner string) *streamState {
	return &streamState{
		e:     e,
		role:  role,
		msgID: uuid.NewString(),
		sub:   sub,
		owner: owner,
		calls: map[int]*openCall{},
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
			s.e.emitToolStart(c.id, c.name, s.msgID, s.sub)
		}
		if tc.Name != "" && c.name == "" {
			c.name = tc.Name
		}
		if tc.ID != "" {
			c.externalID = tc.ID
		}
		if tc.Args != "" {
			s.e.emitToolArgs(c.id, tc.Args, s.sub)
			c.args.WriteString(tc.Args)
		}
	}
}

func (s *streamState) consumeReasoning(reason string) {
	if !s.reasonOpen {
		s.closeText()
		s.hasContent = true
		s.e.emitReasoningStart(s.msgID, roleOf(s.role))
		s.reasonOpen = true
	}
	s.e.emitReasoningContent(s.msgID, reason)
}

func (s *streamState) consumeText(text string) {
	if text == "" {
		return
	}
	if !s.textOpen {
		s.closeReasoning()
		s.e.emitTextStart(s.msgID, roleOf(s.role), s.owner)
		s.textOpen = true
		s.textSegmentStarted = true
		s.hasContent = true
	}
	s.acc.WriteString(text)
	s.e.emitTextContent(s.msgID, text, s.sub)
}

func (s *streamState) closeText() {
	if s.textOpen {
		s.e.emitTextEnd(s.msgID)
		s.textOpen = false
	}
}

func (s *streamState) closeReasoning() {
	if s.reasonOpen {
		s.e.emitReasoningEnd(s.msgID)
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
		s.e.emitToolEnd(s.calls[idx].id, s.sub)
	}
	if s.hasContent && !s.textSegmentStarted {
		s.e.announce(s.msgID, roleOf(s.role), s.owner)
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
		message.StampReasoningMs(m, s.e.reasoningMs(s.msgID))
	}
	return out
}

func emitMessage(e *emitter, msg *schema.Message, sub, owner string) []*schema.Message {
	if msg == nil {
		return nil
	}
	reasonID := ""
	if reason := message.AssistantDeltaReasoning(msg); reason != "" {
		reasonID = uuid.NewString()
		e.emitReasoningStart(reasonID, roleOf(msg.Role))
		e.emitReasoningContent(reasonID, reason)
		e.emitReasoningEnd(reasonID)
	}
	msgID := uuid.NewString()
	text := message.AssistantText(msg)
	calls := message.ToolCalls(msg)

	if text != "" {
		e.emitTextStart(msgID, roleOf(msg.Role), owner)
		e.emitTextContent(msgID, text, sub)
		e.emitTextEnd(msgID)
	}
	for _, tc := range calls {
		id := firstNonEmpty(tc.ID, uuid.NewString())
		e.emitToolStart(id, tc.Name, msgID, sub)
		if tc.Args != "" {
			e.emitToolArgs(id, tc.Args, sub)
		}
		e.emitToolEnd(id, sub)
	}
	if reasonID != "" {
		e.announce(reasonID, roleOf(msg.Role), owner)
	}
	if text == "" && len(calls) > 0 {
		e.announce(msgID, roleOf(msg.Role), owner)
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

// ── emitter ─────────────────────────────────────────────────────────────

type emitter struct {
	w        io.Writer
	threadID string
	runID    string

	brokenFlag bool
	runErr     error

	reasonStart map[string]int64
	reasonEnd   map[string]int64
}

func (e *emitter) markReasonStart(msgID string) {
	if e.reasonStart == nil {
		e.reasonStart = map[string]int64{}
	}
	if _, ok := e.reasonStart[msgID]; !ok {
		e.reasonStart[msgID] = time.Now().UnixMilli()
	}
}

func (e *emitter) markReasonEnd(msgID string) {
	if e.reasonEnd == nil {
		e.reasonEnd = map[string]int64{}
	}
	e.reasonEnd[msgID] = time.Now().UnixMilli()
}

func (e *emitter) reasoningMs(msgID string) int64 {
	start, ok := e.reasonStart[msgID]
	if !ok {
		return 0
	}
	end, ok := e.reasonEnd[msgID]
	if !ok {
		return 0
	}
	return end - start
}

func (e *emitter) broken() bool { return e.brokenFlag }

func (e *emitter) setRunErr(err error) {
	if e.runErr == nil {
		e.runErr = err
	}
}

// emitContextUsage publishes one agent's context occupancy.
//
// The payload is the whole snapshot for that agent: the client replaces, it
// does not merge, so a dropped event cannot leave a wrong number behind.
func (e *emitter) emitContextUsage(u contract.ContextUsage) {
	e.emit(events.NewCustomEvent("context_usage",
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
func (e *emitter) emitCompaction(agent string) {
	e.emit(events.NewCustomEvent("context_compacted",
		events.WithValue(map[string]any{
			"agent": agent,
			"at":    time.Now().UTC().Format(time.RFC3339Nano),
		})))
}

func (e *emitter) emit(ev events.Event) {
	if e.brokenFlag {
		return
	}
	data, err := json.Marshal(ev)
	if err != nil {
		e.setRunErr(err)
		return
	}
	if _, err := e.w.Write(append(data, '\n')); err != nil {
		e.brokenFlag = true
	}
}

func (e *emitter) emitTextStart(msgID, role, name string) {
	ev := events.NewTextMessageStartEvent(msgID)
	if role != "" {
		ev.Role = &role
	}
	if name != "" {
		ev.Name = name
	}
	e.emit(ev)
}

func (e *emitter) emitTextContent(msgID, delta, sub string) {
	ev := events.NewTextMessageContentEvent(msgID, delta)
	ev.SubagentRunID = sub
	e.emit(ev)
}

func (e *emitter) emitTextEnd(msgID string) { e.emit(events.NewTextMessageEndEvent(msgID)) }

// ── reasoning ──

const reasoningRole = "reasoning"

func (e *emitter) emitReasoningStart(msgID, _ string) {
	e.emit(events.NewReasoningStartEvent(msgID))
	e.emit(events.NewReasoningMessageStartEvent(msgID, reasoningRole))
}

func (e *emitter) emitReasoningContent(msgID, delta string) {
	e.markReasonStart(msgID)
	e.emit(events.NewReasoningMessageContentEvent(msgID, delta))
}

func (e *emitter) emitReasoningEnd(msgID string) {
	e.markReasonEnd(msgID)
	e.emit(events.NewReasoningMessageEndEvent(msgID))
	e.emit(events.NewReasoningEndEvent(msgID))
}

func (e *emitter) emitToolStart(callID, name, parentMsgID, sub string) {
	ev := events.NewToolCallStartEvent(callID, name)
	if parentMsgID != "" {
		p := parentMsgID
		ev.ParentMessageID = &p
	}
	ev.SubagentRunID = sub
	e.emit(ev)
}

func (e *emitter) emitToolArgs(callID, delta, sub string) {
	ev := events.NewToolCallArgsEvent(callID, delta)
	ev.SubagentRunID = sub
	e.emit(ev)
}

func (e *emitter) emitToolEnd(callID, sub string) {
	ev := events.NewToolCallEndEvent(callID)
	ev.SubagentRunID = sub
	e.emit(ev)
}

func (e *emitter) announce(messageID, role, owner string) {
	e.emitTextStart(messageID, role, owner)
	e.emitTextEnd(messageID)
}

func (e *emitter) emitToolResult(content, callID, sub string) {
	if callID == "" {
		callID = uuid.NewString()
	}
	ev := events.NewToolCallResultEvent(uuid.NewString(), callID, content)
	ev.SubagentRunID = sub
	e.emit(ev)
}

func errorCode(err error) string {
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
