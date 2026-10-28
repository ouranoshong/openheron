package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"openheron/agentkit/obs"
	"openheron/agentkit/run"
	"openheron/contract"
	"openheron/message"
	"openheron/storage"
)

// Agent switching sentinels. They are mapped onto v2 codes in v2FailErr.
var (
	errAgentNotFound = errors.New("agent not found")
	errAgentBusy     = errors.New("session is busy; finish or stop the run first")
)

// ── POST /v2/sessions ────────────────────────────────────────────────────

func (s *Server) v2CreateSession(ctx context.Context, c *app.RequestContext) {
	// ★ 建会话时顺带指定**入口 agent**。
	//
	//   为什么在这里而不是在 `/v2/run` 里：会话一旦存在，它的根就是会话的
	//   属性。而「新建会话时选的那个 agent」正是前端暂存的值 —— 在建会话的
	//   那一刻落盘，省掉一次「先建、再 PATCH 改根」的往返。
	//
	//   不传就用默认 agent —— 就是 main（见 defaultAgent）。
	var body struct {
		AgentID string `json:"agentId"`
	}
	// ★ 空 body 是合法的（建一个默认根的会话）—— 所以这里**不**因为解析
	//   失败而报错。
	_ = c.Bind(&body)

	// ★ **先校验再建会话**：agentId 写错时不该顺手留下一个空会话 ——
	//   那正是「选了文件又放弃」那种残留的同类问题。
	//
	//   校验在建会话之前，所以「写错的名字」不会留下任何东西。
	root := s.defaultAgent()
	if id := trimSpace(body.AgentID); id != "" {
		r, err := s.resolveAgentID(id)
		if err != nil {
			v2FailErr(c, err)
			return
		}
		root = r
	}

	// ★ 入口 agent 跟会话**一起**落盘：会话从诞生起就有根，之后所有决策
	//   （跑哪棵树、输出算谁的）都读 state 里这一个值。
	sess, err := s.sm.Create(ctx, root)
	if err != nil {
		v2FailInternal(c, err)
		return
	}

	meta := s.metaOf(ctx, sess, nil)
	c.JSON(consts.StatusCreated, meta)
}

// ── GET /v2/sessions ─────────────────────────────────────────────────────

func (s *Server) v2ListSessions(ctx context.Context, c *app.RequestContext) {
	metas, err := s.sm.List(ctx)
	if err != nil {
		v2FailInternal(c, err)
		return
	}
	out := threadList{Items: make([]threadMeta, 0, len(metas))}
	for i := range metas {
		m := metas[i]
		sess, err := s.sm.Get(ctx, m.ID)
		if err != nil {
			continue
		}
		pending, _ := sess.PendingApproval()
		detail := s.metaOf(ctx, sess, pending)
		out.Items = append(out.Items, detail.threadMeta)
	}
	c.JSON(consts.StatusOK, out)
}

// ── GET /v2/sessions/{threadId} ──────────────────────────────────────────

func (s *Server) v2GetSession(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		v2FailErr(c, err)
		return
	}
	pending, err := sess.PendingApproval()
	if err != nil {
		v2FailInternal(c, err)
		return
	}
	status, _ := s.rt.StatusOf(ctx, sess.ID())

	detail := s.metaOf(ctx, sess, pending)
	detail.RunID = status.RunID
	if recs := sess.Decisions(); len(recs) > 0 {
		detail.Decisions = toDecisionViews(recs)
	}
	if !status.StartedAt.IsZero() {
		detail.StartedAt = status.StartedAt.UTC().Format(rfc3339Nano)
	}
	if status.LastError != nil {
		detail.LastError = &lastErrorView{
			Code:    codeInternal,
			Message: status.LastError.Error(),
			At:      time.Now().UTC().Format(rfc3339Nano),
		}
	}
	c.JSON(consts.StatusOK, detail)
}

// ── PATCH /v2/sessions/{threadId} ────────────────────────────────────────

type renameReq struct {
	Title string `json:"title"`
}

func (s *Server) v2RenameSession(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		v2FailErr(c, err)
		return
	}
	var req renameReq
	if err := c.Bind(&req); err != nil {
		v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "invalid body", false)
		return
	}
	title := trimTitle(req.Title)
	if title == "" {
		v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "title must not be empty", false)
		return
	}
	if err := sess.SetTitle(title); err != nil {
		v2FailInternal(c, err)
		return
	}
	pending, _ := sess.PendingApproval()
	c.JSON(consts.StatusOK, s.metaOf(ctx, sess, pending))
}

// trimTitle clamps a user-supplied title the same way List() clamps a
// derived one, so the sidebar never has to defend against long strings.
func trimTitle(v string) string {
	const maxRunes = 120
	v = trimSpace(v)
	if r := []rune(v); len(r) > maxRunes {
		return string(r[:maxRunes])
	}
	return v
}

func trimSpace(v string) string {
	start, end := 0, len(v)
	for start < end && isSpace(v[start]) {
		start++
	}
	for end > start && isSpace(v[end-1]) {
		end--
	}
	return v[start:end]
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

// ── PATCH /v2/sessions/{threadId}/agent ──────────────────────────────────

type switchAgentReq struct {
	AgentID string `json:"agentId"`
}

// v2SwitchAgent changes the session's agent.
//
// ★ 切换只影响之后的轮次：历史消息不改写，正在跑的 turn 也不会换归属。
//
//	所以 running / waiting-approval 时直接 409 —— 否则会留下一轮「前半段
//	按 main 跑、后半段按 task 归档」的记录，而两边都自洽。
func (s *Server) v2SwitchAgent(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		v2FailErr(c, err)
		return
	}

	var req switchAgentReq
	if err := c.Bind(&req); err != nil {
		v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "invalid body", false)
		return
	}
	if trimSpace(req.AgentID) == "" {
		v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "agentId must not be empty", false)
		return
	}

	id, err := s.resolveAgentID(req.AgentID)
	if err != nil {
		v2FailErr(c, err)
		return
	}
	if err := s.ensureAgentIdle(ctx, sess); err != nil {
		v2FailErr(c, err)
		return
	}
	if err := sess.SetAgentID(id); err != nil {
		v2FailInternal(c, err)
		return
	}

	pending, _ := sess.PendingApproval()
	c.JSON(consts.StatusOK, s.metaOf(ctx, sess, pending))
}

// resolveAgentID validates an agent id against the live config.
//
// ★ **每个声明了的 agent 都能当会话的 agent** —— 装配按它自己的配置来
//
//	（它自己的正文、模型、工具边界，以及它声明的那几个下属）。所以这里
//	只查「有没有声明」，没有「能不能当入口」这一层。
//
// ★ 校验在 API 而不是 Builder：400 与 500 的区别对客户端有意义，而
//
//	Builder 只能回一句装配失败。Builder 侧仍有一道同样的检查（防御），
//	两处用的是同一份 config 判断。
func (s *Server) resolveAgentID(id string) (string, error) {
	id = trimSpace(id)
	cfg := s.liveConfig()
	if cfg == nil {
		return "", fmt.Errorf("%w: no configuration is available", errAgentNotFound)
	}
	if _, ok := cfg.Agents[id]; !ok {
		return "", fmt.Errorf("%w: %q is not declared", errAgentNotFound, id)
	}
	return id, nil
}

// ensureAgentIdle rejects a switch while a turn holds the session.
func (s *Server) ensureAgentIdle(ctx context.Context, sess storage.Session) error {
	if p, err := sess.PendingApproval(); err == nil && p != nil && len(p.Interrupts) > 0 {
		return fmt.Errorf("%w: an approval is pending", errAgentBusy)
	}
	if st, ok := s.rt.StatusOf(ctx, sess.ID()); ok {
		if st.Phase == run.PhaseRunning || st.Phase == run.PhaseWaitingApproval {
			return fmt.Errorf("%w: the run is still in progress", errAgentBusy)
		}
	}
	return nil
}

// activeAgentID is the session's agent.
//
// ★ 读 state：会话的根在**创建时**就落盘了（见 v2CreateSession），之后
//
//	一律读它 —— 不再问配置。根是**会话的属性**：会话建好之后，配置怎么
//	改都不影响它。
//
// ★ 唯一的例外是老会话（这个字段落盘之前建的）：它 state 里没有根，
//
//	按默认根（main）补齐并**写回**，之后就是纯读。补齐只发生一次，
//	所以「读」不会一直带着分支跑。
func (s *Server) activeAgentID(sess storage.Session) string {
	if id := sess.AgentID(); id != "" {
		return id
	}
	id := s.defaultAgent()
	if id == "" {
		return ""
	}
	if err := sess.SetAgentID(id); err != nil {
		// 写不回去不该让整次请求失败：读路径仍然能给出默认根。
		obs.Logger().Warn("[api] persist the default root agent failed", "session", sess.ID(), "err", err)
	}
	return id
}

// liveConfig 返回当前配置，没有接线时为 nil。（实现见 v2_settings.go）

// ── DELETE /v2/sessions/{threadId} ───────────────────────────────────────

func (s *Server) v2DeleteSession(ctx context.Context, c *app.RequestContext) {
	id := c.Param("id")
	// Look it up first: v2 answers 404 for an unknown thread while v1's
	// delete reports success for anything.
	if _, err := s.sm.Get(ctx, id); err != nil {
		v2FailErr(c, err)
		return
	}
	_ = s.rt.Cleanup(ctx, id)
	if err := s.sm.Delete(ctx, id); err != nil {
		v2FailInternal(c, err)
		return
	}
	c.JSON(consts.StatusOK, map[string]any{"threadId": id, "deleted": true})
}

// ── GET /v2/sessions/{threadId}/messages ─────────────────────────────────

// v2Messages returns the transcript.
//
// ★ ?agent=xxx 把视图收窄到**某一个 agent 产出的消息**。
//
//	多 agent 模式下这条消息流是混的：入口 agent 的思考、子 agent 的执行、
//	工具结果，全都按时间排在一起。「task 到底改了什么」这个问题，
//	靠顺序读是读不出来的 —— 必须能按 agent 切。
//
//	★ 过滤只影响 messages，**不影响 usage 与 byAgent**：用量是会话级的
//	  事实，而过滤是「看哪些消息」。若两者一起收窄，客户端拿到的
//	  byAgent 各桶之和就对不上 usage。
func (s *Server) v2Messages(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		v2FailErr(c, err)
		return
	}
	history := sess.Messages()
	pending, _ := sess.PendingApproval()

	agent := trimSpace(c.Query("agent"))
	view := messagesView{
		ThreadID:         sess.ID(),
		State:            s.stateOf(ctx, sess),
		Messages:         make([]types.Message, 0, len(history)),
		PendingApprovals: toPendingViews(pending),
		Usage:            toUsageViewWithAgents(sessionUsage(history), sessionUsageByAgent(history)),
		Agent:            agent,
	}
	for i, m := range history {
		// ★ 过滤时**保留原始下标** —— 消息 id 由下标派生（stableID），
		//   改了它，同一条消息在「全部视图」与「按 agent 视图」里会有
		//   两个 id，前端的重连与去重就乱了。
		if agent != "" && message.MessageAgent(m) != agent {
			continue
		}
		if conv, ok := toAgUiMessage(m, i); ok {
			view.Messages = append(view.Messages, conv)
		}
	}
	c.JSON(consts.StatusOK, view)
}

// sessionUsage sums the usage already persisted on the transcript. openheron
// never invented these numbers; they come from the endpoint via eino.
func sessionUsage(history []*schema.Message) contract.Usage {
	var u contract.Usage
	for _, m := range history {
		if m == nil || m.ResponseMeta == nil || m.ResponseMeta.Usage == nil {
			continue
		}
		t := m.ResponseMeta.Usage
		u.PromptTokens += t.PromptTokens
		u.CompletionTokens += t.CompletionTokens
		u.TotalTokens += t.TotalTokens
		u.CachedTokens += t.PromptTokenDetails.CachedTokens
		u.ReasoningTokens += t.CompletionTokensDetails.ReasoningTokens
	}
	return u
}

// sessionUsageByAgent splits the usage by the agent that produced the message.
//
// ★ 归属是**读**消息上的标记，不是这里推断的：标记在写的时候打
//
//	（transport），落进 session.jsonl。所以重启后、历史回放后、
//	换一台机器读同一份文件，分账都一样。
//
// ★ 没有标记的消息不归给任何 agent（而不是塞进一个「unknown」桶）：
//
//	老会话（加标记之前写的）与 user 消息都属于「不知道是谁」，编一个
//	名字出来会让分账看起来比实际更完整。
func sessionUsageByAgent(history []*schema.Message) map[string]contract.Usage {
	out := map[string]contract.Usage{}
	for _, m := range history {
		agent := message.MessageAgent(m)
		if agent == "" {
			continue
		}
		if m == nil || m.ResponseMeta == nil || m.ResponseMeta.Usage == nil {
			continue
		}
		t := m.ResponseMeta.Usage
		u := out[agent]
		u.PromptTokens += t.PromptTokens
		u.CompletionTokens += t.CompletionTokens
		u.TotalTokens += t.TotalTokens
		u.CachedTokens += t.PromptTokenDetails.CachedTokens
		u.ReasoningTokens += t.CompletionTokensDetails.ReasoningTokens
		out[agent] = u
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ─- POST /v2/sessions/{threadId}/abort ────────────────────────────────────

func (s *Server) v2Abort(ctx context.Context, c *app.RequestContext) {
	id := c.Param("id")
	if _, err := s.sm.Get(ctx, id); err != nil {
		v2FailErr(c, err)
		return
	}
	pending, _ := s.rt.StatusOf(ctx, id)
	wasRunning := pending.Phase == run.PhaseRunning

	// Abort is idempotent and always succeeds for a live session; an unknown
	// one is the only error.
	if err := s.rt.Abort(ctx, id); err != nil {
		v2FailInternal(c, err)
		return
	}
	out := map[string]any{"threadId": id, "aborted": wasRunning}
	if !wasRunning {
		out["reason"] = "idle"
	}
	if pending.RunID != "" {
		out["runId"] = pending.RunID
	}
	c.JSON(consts.StatusOK, out)
}

// ── helpers ──────────────────────────────────────────────────────────────

// metaOf projects a session into the listing shape.
//
// ★ State is resolved from the pending approval rather than from the runtime
//
//	alone: a session waiting on a human has no loop running, so the runtime
//	would report idle and the sidebar dot would lie.
func (s *Server) metaOf(ctx context.Context, sess storage.Session, pending *storage.PendingApproval) threadDetail {
	meta := threadDetail{
		ThreadID:     sess.ID(),
		MessageCount: sess.Len(),
		State:        s.stateOf(ctx, sess),
		// ★ 永远给一个具体名字：客户端把「没切过」也当作 main，
		//   而不是再判断一个可能缺席的字段。
		AgentID: s.activeAgentID(sess),
	}
	if status, ok := s.rt.StatusOf(ctx, sess.ID()); ok && !status.StartedAt.IsZero() {
		meta.UpdatedAt = status.StartedAt.UTC().Format(rfc3339Nano)
	}
	// Fall back to creation time when nothing has run yet.
	created := sess.CreatedAt()
	meta.CreatedAt = created.UTC().Format(rfc3339Nano)
	if meta.UpdatedAt == "" {
		meta.UpdatedAt = meta.CreatedAt
	}
	// ★ 标题向**会话**要：推导规则只写在会话层一处 —— 这里再写一遍就
	//   会多出第二套「这个会话叫什么」的答案。
	meta.Title = sess.Meta().Title
	// ★ The waiting state has to travel with the projection: a session paused
	//   on a human has no loop running, so anything derived from the runtime
	//   alone would call it idle and the sidebar dot would lie.
	meta.PendingApprovals = toPendingViews(pending)
	meta.HasPendingApproval = len(meta.PendingApprovals) > 0
	return meta
}

func (s *Server) stateOf(ctx context.Context, sess storage.Session) threadState {
	if sess.HasPending() {
		return stateWaiting
	}
	if status, ok := s.rt.StatusOf(ctx, sess.ID()); ok && status.Phase == run.PhaseRunning {
		return stateRunning
	}
	return stateIdle
}

// titleOf prefers the cached title; the session exposes it without parsing the
// transcript.
// toDecisionViews 把审计记录转成对外形状。
func toDecisionViews(recs []storage.ApprovalRecord) []decisionView {
	out := make([]decisionView, 0, len(recs))
	for _, r := range recs {
		out = append(out, decisionView{
			InterruptID:      r.InterruptID,
			ToolName:         r.ToolName,
			Approved:         r.Approved,
			Reason:           r.Reason,
			DecidedAt:        r.DecidedAt.UTC().Format(rfc3339Nano),
			RequestedByAgent: r.RequestedByAgent,
		})
	}
	return out
}
