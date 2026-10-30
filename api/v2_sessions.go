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

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/run"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
	"github.com/ouranoshong/openheron/transport"
)

// Agent switching sentinels. They are mapped onto v2 codes in v2FailErr.
var (
	errAgentNotFound = errors.New("agent not found")
	errAgentBusy     = errors.New("session is busy; finish or stop the run first")
)

// ── POST /v2/sessions ────────────────────────────────────────────────────

func (s *Server) v2CreateSession(ctx context.Context, c *app.RequestContext) {
	var body struct {
		AgentID string `json:"agentId"`
	}
	_ = c.Bind(&body)

	root := s.defaultAgent()
	if id := trimSpace(body.AgentID); id != "" {
		r, err := s.resolveAgentID(id)
		if err != nil {
			v2FailErr(c, err)
			return
		}
		root = r
	}

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
func (s *Server) activeAgentID(sess storage.Session) string {
	if id := sess.AgentID(); id != "" {
		return id
	}
	id := s.defaultAgent()
	if id == "" {
		return ""
	}
	if err := sess.SetAgentID(id); err != nil {
		obs.Logger().Warn("[api] persist the default root agent failed", "session", sess.ID(), "err", err)
	}
	return id
}

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
func (s *Server) v2Messages(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		v2FailErr(c, err)
		return
	}
	history := sess.Messages(false)
	pending, _ := sess.PendingApproval()

	agent := trimSpace(c.Query("agent"))
	usage := toUsageViewWithAgents(sessionUsage(history), sessionUsageByAgent(history))
	if ctxUsage := s.sessionContextUsage(history); len(ctxUsage) > 0 {
		if usage == nil {
			usage = &usageView{}
		}
		usage.Context = toContextViews(ctxUsage)
	}
	view := messagesView{
		ThreadID:         sess.ID(),
		State:            s.stateOf(ctx, sess),
		Messages:         make([]types.Message, 0, len(history)),
		PendingApprovals: toPendingViews(pending),
		Usage:            usage,
		Agent:            agent,
	}
	for i, m := range history {
		if agent != "" && message.MessageAgent(m) != agent {
			continue
		}
		if conv, ok := toAgUiMessage(m, i); ok {
			view.Messages = append(view.Messages, conv)
		}
	}
	c.JSON(consts.StatusOK, view)
}

// sessionContextUsage reports the context occupancy of every agent that
// spoke, read from the transcript.
//
// The live path and this one share the computation (transport.ContextUsageOf),
// so the number on screen and the number after a refresh cannot disagree.
func (s *Server) sessionContextUsage(history []*schema.Message) map[string]contract.ContextUsage {
	out := transport.ContextUsageOf(history, s.contexts())
	if len(out) == 0 {
		return nil
	}
	for agent, u := range out {
		u.Summarized = compactionCount(history, agent)
		out[agent] = u
	}
	return out
}

// compactionCount counts the compactions an agent's history went through.
//
// The compacted messages stay in the transcript — compaction is not deletion —
// so the count is a fact about the record.
func compactionCount(history []*schema.Message, agent string) int {
	n := 0
	for _, m := range history {
		if !message.IsCompaction(m) {
			continue
		}
		if agent != "" && message.MessageAgent(m) != agent {
			continue
		}
		n++
	}
	return n
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

// ── GET /v2/sessions/{threadId}/runs ──────────────────────────────────────

// v2SessionRuns returns the session's run ledger.
//
// ★ It is the scan. Every question about "what is happening" is answered
//
//	here and never by trusting a push: the stream may be late, incomplete
//	or lost, the ledger is the record.
//
// ★ It never writes. A read that repairs the ledger would make the scan
//
//	unable to tell "what is" from "what was just fixed".
func (s *Server) v2SessionRuns(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		v2FailErr(c, err)
		return
	}
	snap, err := s.rt.ScanRuns(ctx, sess.ID())
	if err != nil {
		v2FailInternal(c, err)
		return
	}
	out := runsView{Items: make([]runView, 0, len(snap.Runs)), ActiveRunID: snap.ActiveRunID}
	for _, r := range snap.Runs {
		out.Items = append(out.Items, toRunView(r))
	}
	c.JSON(consts.StatusOK, out)
}

// ── GET /v2/sessions/{threadId}/runs/{runId} ──────────────────────────────

// v2SessionRun returns one run of the session.
func (s *Server) v2SessionRun(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		v2FailErr(c, err)
		return
	}
	runID := trimSpace(c.Param("runId"))
	if runID == "" {
		v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "runId is required", false)
		return
	}
	snap, err := s.rt.ScanRuns(ctx, sess.ID())
	if err != nil {
		v2FailInternal(c, err)
		return
	}
	for _, r := range snap.Runs {
		if r.RunID == runID {
			c.JSON(consts.StatusOK, toRunView(r))
			return
		}
	}
	v2Fail(c, consts.StatusNotFound, codeThreadNotFound, "run not found", false)
}

// ── helpers ──────────────────────────────────────────────────────────────

// metaOf projects a session into the listing shape.
//
//	alone: a session waiting on a human has no loop running, so the runtime
//	would report idle and the sidebar dot would lie.
func (s *Server) metaOf(ctx context.Context, sess storage.Session, pending *storage.PendingApproval) threadDetail {
	meta := threadDetail{
		ThreadID:     sess.ID(),
		MessageCount: sess.Len(),
		State:        s.stateOf(ctx, sess),
		AgentID:      s.activeAgentID(sess),
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
	meta.Title = sess.Meta().Title
	//   on a human has no loop running, so anything derived from the runtime
	//   alone would call it idle and the sidebar dot would lie.
	meta.PendingApprovals = toPendingViews(pending)
	meta.HasPendingApproval = len(meta.PendingApprovals) > 0
	return meta
}

// stateOf reports the session's observable state.
//
// ★ One read, one answer: the phase already accounts for the pending
//
//	approval (StatusOf derives PhaseWaitingApproval from the session's
//	own pending record), so reading both separately could see them
//	disagree.
func (s *Server) stateOf(ctx context.Context, sess storage.Session) threadState {
	status, ok := s.rt.StatusOf(ctx, sess.ID())
	if !ok {
		return stateIdle
	}
	switch status.Phase {
	case run.PhaseWaitingApproval:
		return stateWaiting
	case run.PhaseRunning:
		return stateRunning
	default:
		return stateIdle
	}
}

// titleOf prefers the cached title; the session exposes it without parsing the
// transcript.
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
