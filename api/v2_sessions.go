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

	"github.com/ouranoshong/openheron/agentkit/emit"
	"github.com/ouranoshong/openheron/agentkit/run"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
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
		// ProjectID names the project the session belongs to.
		//
		// ★ An empty value means "no project": the session works in the
		//   default project root. ★ It is **not** filled in from the scope —
		//   the client says which project it means, and a server that
		//   guessed would put the session somewhere the user did not ask for.
		ProjectID string `json:"projectId"`
		// AuthorizationMode is the approval mode the session is born with.
		//
		// ★ It is the same field the PATCH route carries, and it means the
		//   same thing: the mode belongs to the conversation. ★ An empty
		//   value means "no choice" — the session is born in the default
		//   mode, the way it always was.
		AuthorizationMode string `json:"authorizationMode"`
		// AuthorizationConfirmed says the person was asked about full
		// authorization and said yes.
		//
		// ★ The server still demands it: the flag is the client's word, and
		//   the server checks that word against the rule it holds.
		AuthorizationConfirmed bool `json:"authorizationConfirmed"`
	}
	_ = c.Bind(&body)

	root := s.defaultAgent()
	if id := trimSpace(body.AgentID); id != "" {
		r, err := s.resolveAgentID(id)
		if err != nil {
			s.v2FailErr(c, err)
			return
		}
		root = r
	}

	// ★ The project is checked **before** the session is created: a request
	//   that names a project that is not there is a wrong request, and
	//   answering it with a session nobody can find is worse.
	projectID := trimSpace(body.ProjectID)
	if projectID != "" {
		snap := s.projectSnapshot(ctx)
		if snap == nil {
			s.v2Fail(c, consts.StatusServiceUnavailable, codeInternal,
				"the project registry is not wired", false)
			return
		}
		if _, ok := snap.Get(projectID); !ok {
			s.v2Fail(c, consts.StatusNotFound, codeProjectNotFound,
				"no such project: "+projectID, false)
			return
		}
	}

	// ★ The mode is checked **before** the session is created, for the same
	//   reason the project is: a session that could not get the mode it asked
	//   for is a session nobody can use, and answering such a request with
	//   one is worse than refusing it.
	mode := contract.AuthzDefault
	modeAsked := false
	if raw := trimSpace(body.AuthorizationMode); raw != "" {
		parsed, err := contract.ParseAuthorizationMode(raw)
		if err != nil {
			// ★ An unknown mode is refused, not defaulted — the same rule the
			//   PATCH route holds: a name nobody recognises must not become
			//   the safest one.
			s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, err.Error(), false)
			return
		}
		mode, modeAsked = parsed, true
	}
	// ★ Full authorization needs the confirmation **before** anything is
	//   created — the same handshake the PATCH route has, and the same
	//   answer: a session is not born in full authorization that nobody
	//   confirmed.
	if modeAsked && mode == contract.AuthzFull && !body.AuthorizationConfirmed {
		s.v2Fail(c, consts.StatusConflict, codeConfirmationRequired,
			"full authorization needs an explicit confirmation", false)
		return
	}

	sess, err := s.sm.Create(ctx, root)
	if err != nil {
		s.v2FailInternal(c, err)
		return
	}

	// ★ The mode is written **with** the session, not after it: a session
	//   that is born in the default mode and switched a moment later has a
	//   window in which the first turn could run in the wrong mode.
	if modeAsked {
		if err := s.applyNewAuthorization(sess, mode, body.AuthorizationConfirmed); err != nil {
			// ★ The session must not stay behind half-made: it exists, it has
			//   no mode anybody chose, and nobody can tell which one it has.
			//   ⇒ It is removed, and the request fails.
			if delErr := s.sm.Delete(ctx, sess.ID()); delErr != nil {
				s.logger().Warn("[api] the session cleanup failed after the authorization write",
					"session", sess.ID(), "err", delErr)
			}
			s.v2FailInternal(c, err)
			return
		}
	}

	// ★ The session file is on disk, and that is a fact. ★ A table that could
	//   not be written does not un-create the session: the session simply
	//   has no project, and the log says why.
	if projectID != "" {
		if err := s.projectRegistry().Attach(ctx, sess.ID(), projectID); err != nil {
			s.logger().Warn("[api] the project table write failed; the session has no project",
				"session", sess.ID(), "project", projectID, "err", err)
			projectID = ""
		}
	}

	// ★ The timeline's first line. ★ It is written **after** the attach, so
	//   it carries the project the session really has — a project that could
	//   not be attached is not the session's project.
	s.recordOperation(ctx, sess, storage.Operation{
		Kind:      storage.OpSessionCreated,
		Actor:     storage.ActorUser,
		AgentID:   sess.AgentID(),
		ProjectID: projectID,
	})

	// ★ The mode a session is born with is a line of its own, and so is the
	//   confirmation: they are the same two facts the PATCH route records,
	//   and a reader sees the same story either way. ★ A session born in the
	//   default mode has nothing to record — nothing was chosen.
	if modeAsked && mode != contract.AuthzDefault {
		if mode == contract.AuthzFull {
			s.recordOperation(ctx, sess, storage.Operation{
				Kind:  storage.OpSessionAuthorizationConfirmed,
				Actor: storage.ActorUser,
				Mode:  string(contract.AuthzFull),
			})
		}
		s.recordOperation(ctx, sess, storage.Operation{
			Kind:  storage.OpSessionAuthorizationChanged,
			Actor: storage.ActorUser,
			Mode:  string(mode),
		})
	}

	meta := s.metaOf(ctx, sess, nil, projectID)
	c.JSON(consts.StatusCreated, meta)
}

// ── GET /v2/sessions ─────────────────────────────────────────────────────

func (s *Server) v2ListSessions(ctx context.Context, c *app.RequestContext) {
	metas, err := s.sm.List(ctx)
	if err != nil {
		s.v2FailInternal(c, err)
		return
	}
	// ★ One read of the table for the whole list: it is a file, and a list of
	//   a hundred sessions must not read it a hundred times.
	snap := s.projectSnapshot(ctx)
	out := threadList{Items: make([]threadMeta, 0, len(metas))}
	for i := range metas {
		m := metas[i]
		sess, err := s.sm.Get(ctx, m.ID)
		if err != nil {
			continue
		}
		pending, _ := sess.PendingApproval()
		detail := s.metaOf(ctx, sess, pending, projectIDOf(snap, sess.ID()))
		out.Items = append(out.Items, detail.threadMeta)
	}
	c.JSON(consts.StatusOK, out)
}

// ── GET /v2/sessions/{threadId} ──────────────────────────────────────────

func (s *Server) v2GetSession(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		s.v2FailErr(c, err)
		return
	}
	pending, err := sess.PendingApproval()
	if err != nil {
		s.v2FailInternal(c, err)
		return
	}
	status, _ := s.rt.StatusOf(ctx, sess.ID())

	detail := s.metaOf(ctx, sess, pending, projectIDOf(s.projectSnapshot(ctx), sess.ID()))
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
		s.v2FailErr(c, err)
		return
	}
	var req renameReq
	if err := c.Bind(&req); err != nil {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "invalid body", false)
		return
	}
	title := trimTitle(req.Title)
	if title == "" {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "title must not be empty", false)
		return
	}
	if err := sess.SetTitle(title); err != nil {
		s.v2FailInternal(c, err)
		return
	}
	s.recordOperation(ctx, sess, storage.Operation{
		Kind:  storage.OpSessionRenamed,
		Actor: storage.ActorUser,
		Title: title,
	})
	pending, _ := sess.PendingApproval()
	c.JSON(consts.StatusOK, s.metaOf(ctx, sess, pending, projectIDOf(s.projectSnapshot(ctx), sess.ID())))
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
		s.v2FailErr(c, err)
		return
	}

	var req switchAgentReq
	if err := c.Bind(&req); err != nil {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "invalid body", false)
		return
	}
	if trimSpace(req.AgentID) == "" {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "agentId must not be empty", false)
		return
	}

	id, err := s.resolveAgentID(req.AgentID)
	if err != nil {
		s.v2FailErr(c, err)
		return
	}
	if err := s.ensureAgentIdle(ctx, sess); err != nil {
		s.v2FailErr(c, err)
		return
	}
	if err := sess.SetAgentID(id); err != nil {
		s.v2FailInternal(c, err)
		return
	}
	s.recordOperation(ctx, sess, storage.Operation{
		Kind:    storage.OpSessionAgentChanged,
		Actor:   storage.ActorUser,
		AgentID: id,
	})

	pending, _ := sess.PendingApproval()
	c.JSON(consts.StatusOK, s.metaOf(ctx, sess, pending, projectIDOf(s.projectSnapshot(ctx), sess.ID())))
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
		s.logger().Warn("[api] persist the default root agent failed", "session", sess.ID(), "err", err)
	}
	return id
}

// ── DELETE /v2/sessions/{threadId} ───────────────────────────────────────

func (s *Server) v2DeleteSession(ctx context.Context, c *app.RequestContext) {
	id := c.Param("id")
	// Look it up first: v2 answers 404 for an unknown thread while v1's
	// delete reports success for anything.
	if _, err := s.sm.Get(ctx, id); err != nil {
		s.v2FailErr(c, err)
		return
	}
	_ = s.rt.Cleanup(ctx, id)
	if err := s.sm.Delete(ctx, id); err != nil {
		s.v2FailInternal(c, err)
		return
	}
	// ★ The session is gone from the disk, so it must go from the table too:
	//   a stale id there would be a session that belongs to a project and
	//   does not exist. ★ A table that could not be written is logged, not
	//   fatal — the session is already gone.
	if reg := s.projectRegistry(); reg != nil {
		if err := reg.Detach(ctx, id); err != nil {
			s.logger().Warn("[api] the project table write failed; the session id is still listed",
				"session", id, "err", err)
		}
	}
	c.JSON(consts.StatusOK, map[string]any{"threadId": id, "deleted": true})
}

// ── GET /v2/sessions/{threadId}/messages ─────────────────────────────────

// v2Messages returns the transcript.
func (s *Server) v2Messages(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		s.v2FailErr(c, err)
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
// The live path and this one share the computation (emit.ContextUsageOf),
// so the number on screen and the number after a refresh cannot disagree.
func (s *Server) sessionContextUsage(history []*schema.Message) map[string]contract.ContextUsage {
	out := emit.ContextUsageOf(history, s.contexts())
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
		s.v2FailErr(c, err)
		return
	}
	pending, _ := s.rt.StatusOf(ctx, id)
	wasRunning := pending.Phase == run.PhaseRunning

	// Abort is idempotent and always succeeds for a live session; an unknown
	// one is the only error.
	if err := s.rt.Abort(ctx, id); err != nil {
		s.v2FailInternal(c, err)
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
		s.v2FailErr(c, err)
		return
	}
	snap, err := s.rt.ScanRuns(ctx, sess.ID())
	if err != nil {
		s.v2FailInternal(c, err)
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
		s.v2FailErr(c, err)
		return
	}
	runID := trimSpace(c.Param("runId"))
	if runID == "" {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "runId is required", false)
		return
	}
	snap, err := s.rt.ScanRuns(ctx, sess.ID())
	if err != nil {
		s.v2FailInternal(c, err)
		return
	}
	for _, r := range snap.Runs {
		if r.RunID == runID {
			c.JSON(consts.StatusOK, toRunView(r))
			return
		}
	}
	s.v2Fail(c, consts.StatusNotFound, codeThreadNotFound, "run not found", false)
}

// ── helpers ──────────────────────────────────────────────────────────────

// metaOf projects a session into the listing shape.
//
//	alone: a session waiting on a human has no loop running, so the runtime
//	would report idle and the sidebar dot would lie.
//
// ★ The project id is passed in rather than read here: the table is a file,
//
//	and this function is called once per session.
func (s *Server) metaOf(ctx context.Context, sess storage.Session, pending *storage.PendingApproval, projectID string) threadDetail {
	meta := threadDetail{
		ThreadID:     sess.ID(),
		MessageCount: sess.Len(),
		State:        s.stateOf(ctx, sess),
		AgentID:      s.activeAgentID(sess),
		ProjectID:    projectID,
		// ★ The mode and the confirmation are the session's own facts, so
		//   they are read from the session and not from anywhere else.
		//   ★ The mode is never empty: a session that never chose one reads
		//   as the default, and "no mode" would be a fourth thing.
		AuthorizationMode:      sess.AuthorizationMode().String(),
		AuthorizationConfirmed: sess.AuthorizationConfirmed(),
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
