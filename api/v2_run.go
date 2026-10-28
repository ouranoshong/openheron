package api

import (
	"context"
	"errors"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/google/uuid"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/run"
	"github.com/ouranoshong/openheron/storage"
	"github.com/ouranoshong/openheron/transport"
)

var (
	errEmptyMessage      = errors.New("message must not be empty")
	errInterruptMismatch = errors.New("interruptId does not match the pending approval")
)

// runInput is the subset of AG-UI's RunAgentInput openheron acts on.
// The remaining fields (state, tools, context, forwardedProps) are accepted
// and ignored: they belong to the client's view of the run, and openheron's
// authority is the server.
type runInput struct {
	// ThreadID is optional. Empty means "start a conversation": the server
	// mints the id and announces it in RUN_STARTED.
	ThreadID string `json:"threadId"`
	RunID    string `json:"runId"`

	// Query is the user's question. Preferred over Messages.
	Query string `json:"query"`

	// Messages is AG-UI's transcript. Read-only, and only for the last user
	// text; see Query.
	Messages []types.Message `json:"messages"`
	Resume   []resumeEntry   `json:"resume"`
}

// resumeEntry is one interrupt answer.
//
//	resolved | cancelled, which cannot express "denied, and here is why",
//	and openheron's ApprovalResult needs both fields. Both ends of this wire are
//	openheron, so the server branches on payload.approved.
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
//	was the request shape; AG-UI folds that distinction into the presence of
//	RunAgentInput.resume, so the branch lives here instead of the route table.
func (s *Server) v2Run(ctx context.Context, c *app.RequestContext) {
	var in runInput
	if err := c.Bind(&in); err != nil {
		v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "invalid body", false)
		return
	}

	runID := in.RunID
	if runID == "" {
		runID = uuid.NewString()
	}
	ctx = obs.WithRunID(ctx, runID)

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

	root := s.activeAgentID(sess)

	obs.Logger().Info(obs.Prefix(ctx)+"run started",
		"thread", in.ThreadID, "agent", root, "resume", len(in.Resume) > 0)

	turn, err := s.startRun(ctx, sess, in)
	if err != nil {
		v2FailErr(c, err)
		return
	}

	stream := s.newStream(c)

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
		RunID:     runID,
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
		if res.Stopped {
			obs.Logger().Info("[api] run stopped by the client", "thread", in.ThreadID)
		} else {
			obs.Logger().Error("[api] run ended with a stream error", "thread", in.ThreadID, "err", streamErr)
		}
	}
	stream.Flush()
}

// reportRunStartFailure frames a pre-drain failure as RUN_ERROR.
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
// startRun picks the branch: a question starts a turn, a resume answers one.
func (s *Server) startRun(ctx context.Context, sess storage.Session, in runInput) (*run.Turn, error) {
	if len(in.Resume) == 0 {
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
