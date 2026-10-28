// Package transport 把 openheron 的 Turn 事件流转成 AG-UI 1.0 协议事件。
//
// 分帧由调用方负责：本包只写「一行一个 JSON 事件」，与 a2ui 的写法一致。
// 不要引入 SDK 的 pkg/encoding/sse —— 它自己会输出 data: 帧，而 openheron 用的
// hertz-contrib/sse 已经分过一次帧，两者混用会双重分帧导致前端全量丢弃。
//
// 本包是全仓唯一 import AG-UI SDK 的地方。SDK 锁在 pseudo-version（无 semver），
// 升级只应改这一处。
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
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"openheron/agentkit/obs"
	"openheron/contract"
	"openheron/helpers"
	"openheron/message"
)

// Options 控制一次 run 的协议身份。
type Options struct {
	// RunID 是本轮的协议标识。留空则自动生成。
	RunID string
	// RootAgent 是本轮**入口 agent** 的名字（会话选的那个）。
	// 事件来自它时不标 subagentRunId；
	// 来自其它 agent 时用它作归属，便于前端把子 agent 输出分组。
	RootAgent string
}

// Result 回给 agentkit/run 做归档的内容。
//
// StreamBroken 沿用 a2ui 的语义：sink 断了要停止渲染，但**必须继续消费事件流**，
// 否则 Outcome.Intermediates 不完整，归档的消息会缺。
type Result struct {
	Intermediates []*schema.Message
	// Interrupts 是本轮结束时挂起的人审点，正常结束时为空。
	Interrupts []types.Interrupt
	// Pending 是与 Interrupts 一一对应的待批详情，顺序相同。
	//
	// ★ 它是**一组**：eino 可以在同一轮里抛出多个独立中断（模型一次请求
	//
	//	四个都要审批的工具）。每个都带自己的 id，而重新中断时 id 会变
	//	（框架每次发新的 uuid）—— 所以必须一次答完整组，见
	//	storage.PendingApproval。
	//
	//	Details 在流关闭后就不存在了：工具名与参数只在中断事件里出现过一次。
	Pending      []contract.PendingInterrupt
	Usage        contract.Usage
	StreamBroken bool
	// Stopped 表示这一轮是**被取消**的（用户按了停止，或客户端断开），
	// 不是失败的。
	//
	// ★ 它与 StreamBroken 是两回事：流断在中途 ≠ 出错。
	Stopped bool
}

// RUN_ERROR.code 取值。与 api 层的错误码表保持一致。
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

// EventSource 是事件流来源。*adk.AsyncIterator 直接满足它。
type EventSource interface {
	Next() (*adk.TypedAgentEvent[*schema.Message], bool)
}

// Stream 消费 turn 的事件流，向 w 写出 AG-UI 事件序列。
//
// 调用方负责在拿到结果后调用 turn.Complete(agentkit/run.Outcome{...})。
func Stream(w io.Writer, turn contract.TurnView, opts Options) (Result, error) {
	if turn == nil {
		return Result{}, errors.New("transport: nil turn")
	}
	return RunStream(w, turn.SessionID(), turn.Events(), opts)
}

// RunStream 是与 Turn 解耦的核心，便于单测直接喂事件序列。
func RunStream(w io.Writer, threadID string, src EventSource, opts Options) (Result, error) {
	runID := opts.RunID
	if runID == "" {
		runID = uuid.NewString()
	}

	e := &emitter{w: w, threadID: threadID, runID: runID}
	e.emit(events.NewRunStartedEvent(threadID, runID))

	res := streamEvents(e, src, opts.RootAgent)

	// 终局：正常结束 / 等待人审 / 之前已发过 RUN_ERROR
	switch {
	case len(res.Interrupts) > 0:
		e.emit(events.NewRunFinishedEventWithOptions(threadID, runID,
			events.WithInterruptOutcome(res.Interrupts)))
	case e.runErr != nil:
		// 流中已发 RUN_ERROR，不再补 RUN_FINISHED
	default:
		e.emit(events.NewRunFinishedEventWithOptions(threadID, runID,
			events.WithSuccessOutcome()))
	}
	return res, e.runErr
}

// streamEvents 走完一轮事件流，产出待归档消息与（若有）中断。
func streamEvents(e *emitter, src EventSource, mainAgent string) Result {
	var res Result

	for {
		ev, ok := src.Next()
		if !ok {
			res.Usage = aggregateUsage(res.Intermediates)
			return res
		}

		// 子 agent 归属：入口 agent 不标，其余标成 subagentRunId。
		// a2ui/streamer.go 从不读 ev.AgentName，所以旧前端无法区分输出来自哪个 agent。
		sub := ""
		if ev.AgentName != "" && ev.AgentName != mainAgent {
			sub = ev.AgentName
		}
		if ev.Err != nil {
			// ★ 用户主动中止（或客户端断开）**不是错误**。
			//
			//	eino 把 ctx 取消包成 "[GraphRunError] context has been
			//	canceled"，而 compose 的 internalError 实现了 Unwrap ⇒
			//	errors.Is 能判出底下的 context.Canceled。
			//
			//	不判的后果：按「停止」会在服务端日志里留下一条
			//	"run ended with a stream error" —— 而那一轮是**用户要求**停的，
			//	说成失败既误导，又让人以为停止按钮坏了。
			//
			//	（界面不受影响：前端 stop() 先断本地 SSE，服务端的 RUN_ERROR
			//	  压根到不了浏览器。）
			if errors.Is(ev.Err, context.Canceled) {
				res.Stopped = true
				// ★ 记下来但不**发** RUN_ERROR：调用方据此知道这一轮是被
				//   取消的（可观测），而线上不会多一个 RUN_ERROR 帧。
				e.setRunErr(ev.Err)
				return res
			}
			if helpers.IsModelRetryInProgress(ev.Err) {
				// 重试不是错误：发 CUSTOM 让前端显示进度，然后继续读
				//
				// ★ 日志里也给一行 —— 只发事件的话，「界面卡了几秒」在服务端
				//   日志里查无实据：排障时问的第一句是「服务端有没有在重试」，
				//   而日志里什么都没有，只能去翻前端事件流。
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

		// 人审中断
		if ev.Action != nil && ev.Action.Interrupted != nil {
			// ★ 归属**逐个**待批推导，不共用事件级的 sub：并行时一个事件
			//   里的待批可能来自不同的子 agent，而事件的 AgentName 是根。
			res.Interrupts, res.Pending = interruptsFrom(ev, mainAgent)
			res.StreamBroken = e.broken()
			return res
		}

		hasExit := ev.Action != nil && ev.Action.Exit
		if hasOutput(ev) {
			mo := ev.Output.MessageOutput
			// ★ 子 agent 的产出：打上**作用域**标记（产生在委派内部）。
			//
			//	它照样归档 —— 那是可观测性的来源（界面上能看到专家读了什么、
			//		按 agent 分账能分出它的 token）；但下一轮建上下文时会被剔掉，
			//		隔离因此不破。判据是 `sub != ""`（事件来自非根 agent），
			//		与「谁在当根」无关。
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

		if hasExit {
			res.StreamBroken = e.broken()
			res.Usage = aggregateUsage(res.Intermediates)
			return res
		}
	}
}

func hasOutput(ev *adk.TypedAgentEvent[*schema.Message]) bool {
	return ev.Output != nil && ev.Output.MessageOutput != nil
}

// interruptsFrom 把 eino 的 InterruptContexts 翻成 AG-UI Interrupt。
//
// 根因（IsRootCause）优先：它才是真正卡住的工具调用；链上其它 context 描述的
// 是同一件事的上层。与 a2ui/streamer.go 的挑法一致。
// ★ 返回的是**一组**，不是一个。
//
//	框架的 ToInterruptContexts 只返回 root cause（eino
//	internal/core/interrupt.go:297），所以每个 context 都是一个**独立**的
//	决定，有自己的 id。模型一次请求四个都要审批的工具 → 四个 context。
//
//	★ 每个都用**自己的** ic.ID。
//
//	原先的写法用 rootID 覆盖 id，让几张卡共用一个 id —— 而服务端只存一个
//	pending，于是只有一张卡能答，其余全报「interruptId does not match」。
//	框架的契约是「每个 InterruptCtx.ID 都能当 ResumeParams.Targets 的 key」
//	（eino adk/cancel.go:188），所以本来就该各用各的。
//
//	★ 每个待批的请求方也**逐个**推导（见 [requestedBy]）。
//
//	并行时这一组待批可能来自**不同的**子 agent：两个子 agent 同时被拦下，
//	框架把它们合成**一个**中断事件（eino adk/chatmodel.go:970 把子信号
//	收成 composite，再由 core.ToInterruptContexts 摊平成一组 root cause）。
//	事件级的 AgentName 是根 —— 所以共用一个 sub 就会把两个人的卡都标成
//	同一个请求方。
func interruptsFrom(ev *adk.TypedAgentEvent[*schema.Message], mainAgent string) ([]types.Interrupt, []contract.PendingInterrupt) {
	var out []types.Interrupt
	var pending []contract.PendingInterrupt
	for _, ic := range ev.Action.Interrupted.InterruptContexts {
		if ic == nil {
			continue
		}
		item := types.Interrupt{
			ID: ic.ID,
			// ★ 不能用 %v：Info 是接口值，会把 *agent.ApprovalInfo 打平成
			//   &{write_file {"file_path": ...}}。这段文本经 AG-UI 事件流
			//   直达前端审批卡，用户看到的就是它。
			Reason:   "tool_call",
			Message:  describe(ic.Info),
			Metadata: approvalMetadata(ic.Info),
		}
		// ★ 逐个待批各自的请求方，不是事件级的一个。
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

// renderStream 处理流式消息：文本边到边发，工具参数也边到边发。
//
// 这比 a2ui 现有做法更好 —— a2ui 要等流结束才把工具调用发成 chip，因为它的
// 组件槽位模型要求先知道有几条工具调用；AG-UI 的 START/ARGS/END 天然支持流式。
func renderStream(e *emitter, mo *adk.TypedMessageVariant[*schema.Message], sub, owner string) streamOutcome {
	st := newStreamState(e, mo.Role, sub, owner)

	for {
		chunk, recvErr := mo.MessageStream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			if helpers.IsModelRetryInProgress(recvErr) {
				// ★ 同上：流里的重试也要在服务端日志里留痕。
				obs.Logger().Warn("[transport] model retry", "err", recvErr)
				e.emit(events.NewCustomEvent("model_retry",
					events.WithValue(map[string]any{"message": recvErr.Error()})))
				// ★ 这里**不**收尾：重试时这一轮还没产出可归档的东西，
				//	提前收尾会发出不该发的 END 帧。
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

// streamState 是**一条消息**的流式状态。
//
// ★ 抽成结构体不是为了「把函数拆小」，而是因为这些状态**互相牵制**：
//
//	开着的正文段、开着的推理段、已开过的正文段、工具调用表 —— 四个状态
//	一起决定「下一个 chunk 该发什么、该先收掉谁」。塞在闭包里时，
//	每个闭包都捕获同一堆变量，读的时候要来回对照才知道谁改了谁。
type streamState struct {
	e     *emitter
	role  schema.RoleType
	msgID string
	sub   string
	owner string

	// textOpen / reasonOpen：两条流各自开着没有。
	// ★ 推理与正文是**互斥**的：模型先想、再答，切换时必须先把上一条
	//   收尾，否则前端会看到未闭合的 part（AG-UI 的 part 靠 END 闭合）。
	textOpen   bool
	reasonOpen bool
	// textSegmentStarted：这条消息**开过正文段**没有。
	// hasContent：这条消息有没有内容（正文 / 推理 / 工具调用）。
	textSegmentStarted bool
	hasContent         bool

	acc    strings.Builder
	chunks []*schema.Message
	// tc.Index → 协议侧 toolCallId；index 在一条消息内稳定，可安全做键
	calls map[int]*openCall
	order []int
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

// consume 消化一个 chunk：工具调用、推理、正文三样，按这个顺序。
//
// ★ 顺序有意义：工具调用先于推理与正文（模型先发起调用，再想，最后答），
//
//	而推理与正文互斥 —— 一个 chunk 只可能带其中一样。
func (s *streamState) consume(chunk *schema.Message) {
	s.chunks = append(s.chunks, chunk)

	s.consumeToolCalls(chunk)

	// ★ 推理与正文都要先收掉对方：AG-UI 的 part 靠 END 闭合，
	//   两条流交叉而不闭合，前端会拿到一个横跨两段的 part。
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

// finish 收尾：关掉两条流、结束工具调用，并拼出归档消息。
func (s *streamState) finish() streamOutcome {
	s.closeAll()
	for _, idx := range s.order {
		s.e.emitToolEnd(s.calls[idx].id, s.sub)
	}
	// ★ 这条消息有内容、却**没有正文段**（只有推理或只有工具调用）时，
	//   补一个声明，把归属说清楚（见 [emitter.announce]）。
	//
	//   ★ 有正文的消息**完全不受影响**：它的正文段本来就带 name。
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
	// ★ 把思维链用时写进归档消息 —— 事件流不落盘，不存下来就没有
	//   「刷新后想了多久」（前端回放时从消息的 Extra 读）。
	for _, m := range out.intermediates {
		message.StampReasoningMs(m, s.e.reasoningMs(s.msgID))
	}
	return out
}

// emitMessage 处理非流式消息：文本走 TEXT_MESSAGE_*，工具调用走 TOOL_CALL_*。
func emitMessage(e *emitter, msg *schema.Message, sub, owner string) []*schema.Message {
	if msg == nil {
		return nil
	}
	// 推理先于正文，与流式路径保持同样的顺序。
	//
	// ★ 推理与正文是**两条**消息（各自的 id）—— 非流式路径的形状与流式
	//   路径不同（流式路径里两者共用一个 msgID）。
	reasonID := ""
	if reason := message.AssistantDeltaReasoning(msg); reason != "" {
		reasonID = uuid.NewString()
		e.emitReasoningStart(reasonID, roleOf(msg.Role))
		e.emitReasoningContent(reasonID, reason)
		e.emitReasoningEnd(reasonID)
	}
	// ★ msgID 提到「有没有正文」之外：工具调用要挂到**这条消息**上，
	//   所以它得先知道这条消息的 id。
	msgID := uuid.NewString()
	text := message.AssistantText(msg)
	calls := message.ToolCalls(msg)

	// ★ 有正文的消息：正文段自己的 START/END 就是它的「开出来」——
	//   START 带 name，前端据此建出这条消息并记下它属于哪个 agent。
	//
	//   ★ 与流式路径（renderStream）对齐，也与历史回放对齐：
	//     历史里工具调用是**同一条** assistant 消息的 toolCalls。
	if text != "" {
		e.emitTextStart(msgID, roleOf(msg.Role), owner)
		e.emitTextContent(msgID, text, sub)
		e.emitTextEnd(msgID)
	}
	for _, tc := range calls {
		id := firstNonEmpty(tc.ID, uuid.NewString())
		// ★ parentMessageID 指向这条消息 —— 前端据此把工具卡并进来，
		//   归属随这条消息一起走。传空串的话工具卡会落进一条匿名消息
		//   （`tool:<callId>`），于是「谁调的」就丢了。
		e.emitToolStart(id, tc.Name, msgID, sub)
		if tc.Args != "" {
			e.emitToolArgs(id, tc.Args, sub)
		}
		e.emitToolEnd(id, sub)
	}
	// ★ 两条消息各自的声明：
	//   · 正文消息：正文段自己的 START 已经带了 name ✓ 不用补。
	//   · 推理消息：只有 REASONING_* 事件，**不带归属** → 补一个声明。
	//   · 只有工具调用没有正文时：工具卡挂在这条消息上，而这条消息是
	//     由 TOOL_CALL_START 顺带建出来的 → 同样补一个声明。
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

// collectedCalls 把流内累积的工具调用还原成可归档的形态。
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

// ── 辅助 ─────────────────────────────────────────────────────────────────

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

// openCall 跟踪一次流式工具调用的协议状态。
type openCall struct {
	id         string
	externalID string
	name       string
	args       strings.Builder
}

// streamOutcome 是 renderStream 的产出。
type streamOutcome struct {
	lastContent   string
	intermediates []*schema.Message
}

// ── emitter ─────────────────────────────────────────────────────────────

// emitter 写事件并跟踪 sink 是否已断。
//
// 写失败后不再产出可渲染事件，但**继续消费事件流** —— 归档优先于呈现，
// 这是 a2ui/streamer.go 里 writerBroken 注释所要求的语义。
type emitter struct {
	w        io.Writer
	threadID string
	runID    string

	brokenFlag bool
	runErr     error

	// ★ 思维链计时：按 AG-UI 的 messageId 记起止时刻。
	//
	//	记在**发射时**而不是事后从事件里找：这里就是「模型真的在想」
	//	的那段时间的起点与终点（事件的时间戳也是这一刻打的）。
	//
	//	用途：把用时写进归档消息的 Extra（[message.ExtraReasoningMs]）——
	//	事件流不落盘，不存下来就没有「刷新后想了多久」。
	reasonStart map[string]int64
	reasonEnd   map[string]int64
}

// markReasonStart 记下这条消息的思维链**开始**时刻（只记第一次）。
func (e *emitter) markReasonStart(msgID string) {
	if e.reasonStart == nil {
		e.reasonStart = map[string]int64{}
	}
	if _, ok := e.reasonStart[msgID]; !ok {
		e.reasonStart[msgID] = time.Now().UnixMilli()
	}
}

// markReasonEnd 记下这条消息的思维链**结束**时刻。
func (e *emitter) markReasonEnd(msgID string) {
	if e.reasonEnd == nil {
		e.reasonEnd = map[string]int64{}
	}
	e.reasonEnd[msgID] = time.Now().UnixMilli()
}

// reasoningMs 返回这条消息的思维链用时（毫秒）；0 = 没有思维链或没掐到头。
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
//
// AG-UI 的推理事件是五段式：REASONING_START 开启一段思考，
// REASONING_MESSAGE_START/CONTENT/END 承载文本，REASONING_END 收尾。
// 类型名与前端 @ag-ui/core 的枚举逐字对应，所以不需要自定义事件。
//
// ★ 为什么要用 AG-UI 自带的这套而不是 events.NewCustomEvent：
//   自定义事件前端也得自己认，而 AG-UI 的这族事件在 TS 侧有现成的
//   类型枚举与 zod schema —— 用它们等于免费拿到前端侧的校验与类型。

// reasoningRole 是 REASONING_MESSAGE_START 的 role 取值。
//
// ★ 不是 "assistant"：前端 @ag-ui/core 的 schema 把它定义成
//
//	zod.literal("reasoning")，传 "assistant" 会让整个事件被 zod 拒绝，
//	前端只看到一条 ZodError，整条流的内容都丢。
//
//	Go 侧的 events.Validate() 不查这个字段，所以契约测试全绿也照样漏 —
//	跨语言的这道校验只有前端有。取值写死在这里，别再从 roleOf 传进来。
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

// announce 补一个「声明」：TEXT_MESSAGE_START(name) + TEXT_MESSAGE_END。
//
// ★ 存在的理由：前端是由 REASONING_START / TOOL_CALL_START 顺带建出那些
//
//	消息的，而这两个事件**不带归属** —— 于是那条消息会是匿名的。
//	而「这个 agent 调了什么工具」「这段思考是谁的」恰恰是多 agent 下最该
//	看见的东西。
//
// ★ 为什么是「START + END」这对空事件：AG-UI 的 ValidateSequence 规定
//
//	「一条消息一对 START/END」，而消息开着的时候再发 START 会被判
//	"message ... already started"。所以声明要么在内容之前（那它就成了
//	内容的开始）、要么在内容之后（补上去）。
//
// ★ 为什么放在**之后**：内容之前抢发的话，正文的 TEXT_MESSAGE_CONTENT
//
//	会因为「消息已结束」被拒（"cannot add content to message ... that was
//	not started"）。所以正文消息的声明由它自己的正文段承担，只有**没有
//	正文段**的消息才需要在末尾补。
//
// ★ 补在末尾时前端已经把消息建出来了，这条 START 只是把归属**补**上去
//
//	（ensureMessage 找到已有的那条，改它的 agent）。
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

// errorCode 把 LLM 错误映射到协议错误码。
//
// 刻意不 import llm/common：那是协议层的实现细节，transport 只做文本兜底
// 判断，保证前端拿得到可分流的 code。真正的分类在 llm/common 内已完成。
func errorCode(err error) string {
	if err == nil {
		return CodeModelUnknown
	}
	s := err.Error()
	switch {
	// quota 必须排在 rate_limit 之前：端点返回 "429 insufficient_quota" 时
	// 同时命中两者，但两者的处置完全不同（见 llm/common/error.go 的 hintFor）
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
