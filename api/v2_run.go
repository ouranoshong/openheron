package api

import (
	"context"
	"errors"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"openheron/agentkit/obs"
	"openheron/agentkit/run"
	"openheron/storage"
	"openheron/transport"
)

var (
	errEmptyMessage      = errors.New("message must not be empty")
	errInterruptMismatch = errors.New("interruptId does not match the pending approval")
)

// runInput is the subset of AG-UI's RunAgentInput openheron acts on.
//
// The remaining fields (state, tools, context, forwardedProps) are accepted
// and ignored: they belong to the client's view of the run, and openheron's
// authority is the server.
type runInput struct {
	// ThreadID is optional. Empty means "start a conversation": the server
	// mints the id and announces it in RUN_STARTED.
	//
	// ★ **必填**：会话只由 `POST /v2/sessions` 建，`/v2/run` 一律不建。
	//
	//   「建不建会话」的决定权在**客户端**：它知道当前有没有会话 ——
	//   服务端只能从「有没有 threadId」推断，而那是**结果**不是**决定**。
	//
	//   ★ 之前它是可选的（服务端在缺省时懒创建），为的是少一次往返。
	//     代价是「谁决定建会话」这件事没有归属：客户端以为服务端会建，
	//     服务端以为客户端会传 id —— 于是客户端只能先造一个本地占位 id，
	//     每个会话前后有两个 id，需要一整套注册表去收敛，而收敛失败时
	//     history 会打 404、历史整个空掉。
	//
	//   现在规则只有一条：**要跑，先有会话**。
	ThreadID string `json:"threadId"`
	RunID    string `json:"runId"`

	// Query is the user's question. Preferred over Messages.
	//
	// ★ 为什么加它：服务端只读最后一条 user 文本，却要求客户端构造完整的
	//   AG-UI 消息数组 —— 契约不诚实，而且让客户端以为自己的历史副本有意义。
	//   Messages 保留是为了协议兼容（AG-UI 客户端只会发这个），取值为空时
	//   回退到它。
	Query string `json:"query"`

	// Messages is AG-UI's transcript. Read-only, and only for the last user
	// text; see Query.
	Messages []types.Message `json:"messages"`
	// Resume 是审批答复。入口 agent 由会话自己带着（建会话时定，或 PATCH 改），
	// 这里**不再**能改 —— 审批必须回到发起它的那个 agent，在恢复途中换根会让
	// interrupt 找不到自己的闸门。
	Resume []resumeEntry `json:"resume"`
}

// resumeEntry is one interrupt answer.
//
// ★ payload carries the decision. AG-UI's vocabulary here is only
//
//	resolved | cancelled, which cannot express "denied, and here is why",
//	and openheron's ApprovalResult needs both fields. Both ends of this wire are
//	openheron, so the server branches on payload.approved.
//
// ★ The protocol's own `status` field is deliberately absent rather than
//
//	merely ignored: a field that arrives and does nothing teaches every
//	future reader that status decides something. Dropping it from the struct
//	means the decision has exactly one home.
type resumeEntry struct {
	InterruptID string `json:"interruptId"`
	Payload     *struct {
		Approved bool   `json:"approved"`
		Reason   string `json:"reason"`
	} `json:"payload"`
}

// v2Run is the single entry point for acting on a thread: ask a question, or
// answer an approval.
//
// ★ One endpoint, not two. /v1 had /chat and /approve whose only difference
//
//	was the request shape; AG-UI folds that distinction into the presence of
//	RunAgentInput.resume, so the branch lives here instead of the route table.
func (s *Server) v2Run(ctx context.Context, c *app.RequestContext) {
	var in runInput
	if err := c.Bind(&in); err != nil {
		v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "invalid body", false)
		return
	}

	// ★ runId 贯穿这一轮：日志前缀、SSE 协议、运行时是**同一个** id。
	//
	//	客户端可以自带（AG-UI 的 runId），没带就由服务端造一个 ——
	//	此前是 transport 在渲染时才造，于是日志与协议里的 id
	//	是两回事：拿协议里的 id 去 grep 日志，什么都找不到。
	//
	//	★ 造 id 的位置必须在这里（进运行时之前）：
	//	它是这一轮的**起点**，晚一步就有一段日志没有前缀。
	runID := in.RunID
	if runID == "" {
		runID = uuid.NewString()
	}
	ctx = obs.WithRunID(ctx, runID)

	// ★ 会话必须已经存在：threadId 必填。
	//
	//   缺了就是**请求不合法**，不是「顺便建一个」—— 服务端不知道客户端
	//   是想续跑还是想新开，而猜错的后果是消息落进一个用户看不见的会话。
	if trimSpace(in.ThreadID) == "" {
		v2Fail(c, consts.StatusBadRequest, codeInvalidBody,
			"threadId is required; create the session with POST /v2/sessions first", false)
		return
	}

	sess, err := s.sm.Get(ctx, trimSpace(in.ThreadID))
	if err != nil {
		v2FailErr(c, err)
		return
	}
	in.ThreadID = sess.ID()

	// ★ 这一轮的入口 agent，**在开跑之前**就从 state 里取定。
	//
	//   取定之后再开跑，是为了老会话（state 里没有根的那批）：补齐先发生，
	//   于是运行时装配与事件归属读到的是同一个值，不会一个按默认根、
	//   一个按别的。
	root := s.activeAgentID(sess)

	// ★ 这一轮开始的标记：它是「有前缀的第一行日志」。
	//
	//	没有它，一次没调到模型的轮次（配置错、模型不存在）在日志里
	//	不留任何痕迹 —— 而那正是最需要被看见的一类失败。
	obs.Logger().Info(obs.Prefix(ctx)+"run started",
		"thread", in.ThreadID, "agent", root, "resume", len(in.Resume) > 0)

	turn, err := s.startRun(ctx, sess, in)
	if err != nil {
		v2FailErr(c, err)
		return
	}

	stream := s.newStream(c)

	// ★ Wait before touching Events(): turn.events is only assigned inside
	//   makeOnAgentEvents, so reading it earlier hands back a nil iterator.
	//   Readying is not executing — the agent may still be running after this
	//   returns, and the drain below is what follows it to completion.
	if err := turn.Wait(s.startTimeout()); err != nil {
		// The failed wait already released the turn's handshake; there is
		// nothing to Complete and nothing left to drain.
		s.reportRunStartFailure(stream, err)
		stream.Flush()
		return
	}
	// Keepalives start before draining: a preempted turn can take a long time
	// to unwind, and silence on the wire reads as a dead connection.
	kaStop := make(chan struct{})
	defer close(kaStop)
	go stream.Keepalive(kaStop, 15_000_000_000) // 15s

	res, streamErr := transport.Stream(stream, turn, transport.Options{
		RunID: runID,
		// ★ 必须传**本轮实际的入口 agent**：会话选了谁，谁就是根 ——
		//   还用配置里的默认 agent 会让它的下属事件被标成 subagentRunId，
		//   消息归属与审批卡都会错。
		RootAgent: root,
	})

	// The turn's own bookkeeping runs whatever the renderer managed to do.
	turn.Complete(run.Outcome{
		Intermediates: res.Intermediates,
		Interrupts:    res.Pending,
		Usage:         res.Usage,
	})
	// A stream-side failure is already framed as RUN_ERROR by the transport;
	// it only matters to the archive, so record it and move on.
	if streamErr != nil {
		// ★ 用户主动中止**不是**失败 —— 说成「流错误」既误导，又让人以为
		//   停止按钮坏了。中止由 transport 认出来了（res.Stopped），
		//   这里就不该再把它当错误报。
		if res.Stopped {
			obs.Logger().Info("[api] run stopped by the client", "thread", in.ThreadID)
		} else {
			obs.Logger().Error("[api] run ended with a stream error", "thread", in.ThreadID, "err", streamErr)
		}
	}
	stream.Flush()
}

// reportRunStartFailure frames a pre-drain failure as RUN_ERROR.
//
// A client that already speaks AG-UI expects a terminal event, not a bare
// HTTP body — by this point 200 and the stream are already committed.
func (s *Server) reportRunStartFailure(stream *transport.SSEStream, err error) {
	code := transport.CodeTurnSuperseded
	switch {
	case errors.Is(err, run.ErrStartTimeout):
		code = transport.CodeTurnStartTimeout
	case errors.Is(err, run.ErrSuperseded):
		code = transport.CodeTurnSuperseded
	case errors.Is(err, run.ErrNoCheckpoint):
		code = codeNoCheckpoint
	default:
		code = codeInternal
	}
	_ = stream.Publish(map[string]any{"type": "RUN_ERROR", "code": code, "message": err.Error()})
	obs.Logger().Error("[api] run did not start", "code", code, "err", err)
}

// resolveRunSession finds, or for a plain question mints, the session a run
// belongs to.
//
// ★ 只有「提问」才允许凭空建会话。resume 必须指名：
//
//	对一个不存在的会话答复审批是调用方的 bug，给它静默建个空会话会把
//	这个 bug 埋掉 —— 之后表现为「审批点了没反应」，比 404 难查得多。
//
// ★ 读路径仍然不自动创建（见 TestV2ReadsDoNotCreateUnknownThread）：
//
//	GET 一个未知会话永远是 404。这里是唯一的例外，因为 /v2/run 是写操作，
//	而「第一次提问就是创建会话」正是懒创建的语义。
//
// startRun picks the branch: a question starts a turn, a resume answers one.
func (s *Server) startRun(ctx context.Context, sess storage.Session, in runInput) (*run.Turn, error) {
	if len(in.Resume) == 0 {
		// ★ The server owns history. query is the only thing read out of the
		//   request; messages stays for protocol compatibility, not as a second
		//   source of truth for the transcript.
		query := trimSpace(in.Query)
		if query == "" {
			query = lastUserText(in.Messages)
		}
		if query == "" {
			return nil, errEmptyMessage
		}
		expanded, err := expandCommand(query)
		if err != nil {
			return nil, err
		}
		return s.rt.Submit(ctx, sess.ID(), expanded)
	}

	pending, err := sess.PendingApproval()
	if err != nil {
		return nil, err
	}
	if pending == nil || len(pending.Interrupts) == 0 {
		return nil, run.ErrNoPendingInterrupt
	}

	// A stale or cross-thread interruptId must not answer this approval.
	// /v1 never checked, which let a client answer a decision whose context
	// it no longer had.
	//
	// ★ 校验的是**每一个** id，而不是某一个。
	//
	//	一轮里可以有多个独立待批（模型一次请求四个都要审批的工具）。
	//	客户端拿到几张卡就能答几张 —— 拿哪张都得算数，拿不在表里的才是
	//	过期或跨会话的。
	decisions := make([]run.ApprovalDecision, 0, len(in.Resume))
	for _, entry := range in.Resume {
		if entry.InterruptID == "" {
			return nil, errInterruptMismatch
		}
		if !pending.Has(entry.InterruptID) {
			return nil, errInterruptMismatch
		}
		d := run.ApprovalDecision{InterruptID: entry.InterruptID}
		if p := entry.Payload; p != nil {
			d.Approved = p.Approved
			d.Reason = p.Reason
		}
		decisions = append(decisions, d)
	}
	if len(decisions) == 0 {
		return nil, run.ErrNoPendingInterrupt
	}

	return s.rt.Resume(ctx, sess.ID(), decisions)
}

// lastUserText extracts the query from the incoming messages, tolerating the
// multi-part content shape AG-UI allows.
func lastUserText(msgs []types.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != types.RoleUser {
			continue
		}
		switch content := msgs[i].Content.(type) {
		case string:
			if t := trimSpace(content); t != "" {
				return t
			}
		case []types.InputContent:
			var b []byte
			for _, part := range content {
				if part.Type == types.InputContentTypeText && part.Text != "" {
					b = append(b, []byte(part.Text)...)
				}
			}
			if t := trimSpace(string(b)); t != "" {
				return t
			}
		}
	}
	return ""
}

// defaultAgent is the agent a new session runs as when the client did not
// pick one.
//
// ★ 它恒为 **main**，而且**不是配置项**：「谁是默认」不是配置项，只是
//
//	「新会话没指定时用哪个」的约定 —— 每个 agent 都能当会话的 agent
//	（见 [Config.AgentSpecs]），默认只是其中之一。
//
//	这里读配置而不是写死字符串：默认值必须与 Builder 装配时用的是同一个
//	（agentkit/build/spec.go 的 defaultAgent），两份写法迟早分叉。
//
//	只有两处用它：建会话时定默认值，以及给老会话补齐。
func (s *Server) defaultAgent() string {
	if s.cfgFn == nil {
		return ""
	}
	cfg := s.cfgFn()
	if cfg == nil {
		return ""
	}
	return cfg.DefaultAgent()
}
