package api

import (
	"context"
	"errors"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/agentkit/run"
)

// CompactorSource hands out the summarizer of an agent.
//
// The builder owns the model resolution, so the API layer asks it rather
// than re-deriving which model an agent runs on — the same rule the
// effective tool table follows.
type CompactorSource interface {
	Summarizer(ctx context.Context, agentID string) (run.Summarizer, error)
}

// SetCompactorSource injects the source of the summarizers.
func (s *Server) SetCompactorSource(src CompactorSource) { s.compactorFn = src }

// ─- POST /v2/sessions/{threadId}/compact ──────────────────────────────────

// v2Compact compacts the session's context now.
//
// It is the manual counterpart of the threshold: the same summary, the same
// marker, the same transcript. Only the trigger differs — the user asked.
//
// The compaction is refused while a turn is running or an approval is
// waiting: both mean the context belongs to something else right now.
func (s *Server) v2Compact(ctx context.Context, c *app.RequestContext) {
	id := c.Param("id")
	sess, err := s.sm.Get(ctx, id)
	if err != nil {
		v2FailErr(c, err)
		return
	}

	pending, _ := s.rt.StatusOf(ctx, sess.ID())
	switch pending.Phase {
	case run.PhaseRunning, run.PhaseWaitingApproval:
		v2Fail(c, consts.StatusConflict, codeSessionBusy,
			"a run is in progress; stop it before compacting", false)
		return
	}

	if s.compactorFn == nil {
		v2FailInternal(c, errors.New("no compactor is wired"))
		return
	}
	sum, err := s.compactorFn.Summarizer(ctx, sess.AgentID())
	if err != nil {
		v2FailInternal(c, err)
		return
	}

	res, err := run.Compact(ctx, sess, sum)
	if err != nil {
		if errors.Is(err, run.ErrNothingToCompact) {
			v2Fail(c, consts.StatusBadRequest, codeInvalidBody, err.Error(), false)
			return
		}
		v2FailInternal(c, err)
		return
	}

	c.JSON(consts.StatusOK, map[string]any{
		"threadId":    sess.ID(),
		"superseded":  res.Superseded,
		"savedTokens": res.SavedTokens,
		"usedTokens":  res.UsedTokens,
		"at":          res.At.UTC().Format(time.RFC3339Nano),
	})
}
