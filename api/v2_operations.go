package api

import (
	"context"
	"strconv"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/storage"
)

// ── GET /v2/sessions/{threadId}/operations ────────────────────────────────

// operationView is one line of the operation log on the wire.
//
// ★ The wire shape is camelCase while the file's shape is snake_case — the
//
//	same split the run ledger and its view already have.
type operationView struct {
	Seq         uint64 `json:"seq"`
	At          string `json:"at"`
	Kind        string `json:"kind"`
	Actor       string `json:"actor"`
	RunID       string `json:"runId,omitempty"`
	TurnKind    string `json:"turnKind,omitempty"`
	InterruptID string `json:"interruptId,omitempty"`
	ToolName    string `json:"toolName,omitempty"`
	Approved    *bool  `json:"approved,omitempty"`
	AgentID     string `json:"agentId,omitempty"`
	ProjectID   string `json:"projectId,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Title       string `json:"title,omitempty"`
	Path        string `json:"path,omitempty"`
	Name        string `json:"name,omitempty"`
	Size        int64  `json:"size,omitempty"`
	State       string `json:"state,omitempty"`
}

// operationsView is the session's operation timeline.
type operationsView struct {
	ThreadID string          `json:"threadId"`
	Total    int             `json:"total"`
	Items    []operationView `json:"items"`
}

// toOperationView converts one stored operation to its wire shape.
func toOperationView(op storage.Operation) operationView {
	return operationView{
		Seq:         op.Seq,
		At:          op.At.UTC().Format(rfc3339Nano),
		Kind:        string(op.Kind),
		Actor:       string(op.Actor),
		RunID:       op.RunID,
		TurnKind:    op.TurnKind,
		InterruptID: op.InterruptID,
		ToolName:    op.ToolName,
		Approved:    op.Approved,
		AgentID:     op.AgentID,
		ProjectID:   op.ProjectID,
		Mode:        op.Mode,
		Title:       op.Title,
		Path:        op.Path,
		Name:        op.Name,
		Size:        op.Size,
		State:       op.State,
	}
}

// v2Operations returns the session's operation timeline.
//
// ★ The order is **ascending** — the timeline is read from the top down,
//
//	like a log, not like a message list.
//
// ★ `after` gives the lines a client does not have yet, and `limit` keeps
//
//	the last n of what is left: a session's log is never trimmed, so a
//	client that has been away needs the tail, not the whole file.
func (s *Server) v2Operations(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		s.v2FailErr(c, err)
		return
	}

	ops, err := s.operationsOf(sess)
	if err != nil {
		s.v2FailInternal(c, err)
		return
	}

	after := uint64(0)
	if raw := c.Query("after"); raw != "" {
		n, perr := strconv.ParseUint(raw, 10, 64)
		if perr != nil {
			s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody,
				"after must be a sequence number", false)
			return
		}
		after = n
	}
	limit := 0
	if raw := c.Query("limit"); raw != "" {
		n, perr := strconv.Atoi(raw)
		if perr != nil || n < 0 {
			s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody,
				"limit must be a non-negative number", false)
			return
		}
		limit = n
	}

	selected := make([]storage.Operation, 0, len(ops))
	for _, op := range ops {
		if op.Seq <= after {
			continue
		}
		selected = append(selected, op)
	}
	total := len(selected)
	if limit > 0 && len(selected) > limit {
		selected = selected[len(selected)-limit:]
	}

	out := operationsView{
		ThreadID: sess.ID(),
		Total:    total,
		Items:    make([]operationView, 0, len(selected)),
	}
	for _, op := range selected {
		out.Items = append(out.Items, toOperationView(op))
	}
	c.JSON(consts.StatusOK, out)
}

// operationsOf reads a session's operation log.
func (s *Server) operationsOf(sess storage.Session) ([]storage.Operation, error) {
	log := sess.Operations()
	if log == nil {
		return nil, nil
	}
	return log.List(context.Background())
}

// recordOperation appends one line to the session's operation log.
//
// ★ A failed write is logged, not returned: the operation itself already
//
//	happened, and its truth lives in the state it changed. The log is a
//	projection of that state — a missing line is the projection's loss, not
//	the operation's, and refusing the operation would leave the interface
//	saying "that did not happen" about something that did.
func (s *Server) recordOperation(ctx context.Context, sess storage.Session, op storage.Operation) {
	if sess == nil {
		return
	}
	log := sess.Operations()
	if log == nil {
		return
	}
	if err := log.Append(ctx, op); err != nil {
		s.logger().Warn("[api] failed to record the operation",
			"session", sess.ID(), "kind", string(op.Kind), "err", err)
	}
}
