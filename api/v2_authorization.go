package api

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
)

// ── PATCH /v2/sessions/{threadId}/authorization ───────────────────────────

type authorizationReq struct {
	// Mode is the mode to switch to: "default", "trusted" or "full".
	Mode string `json:"mode"`
	// Confirm says the person has been asked and said yes.
	Confirm bool `json:"confirm"`
}

// v2SetAuthorization changes the session's approval mode.
//
// ★ The mode is a **session property**, so this route is the only way to
//
//	change it. The server's answer is the authority — a mode the client
//	guessed, or one a client kept to itself, would be a mode nobody
//	confirmed.
//
// ★ Full authorization needs an explicit confirmation, **once per session**.
//
//	The server holds that fact (the session's own field), so a second
//	surface does not ask a second time, and a client that forgot to ask
//	does not get full authorization by accident.
//
// ★ Changing the mode does **not** require an idle session. A round that is
//
//	already running keeps the mode it began with, the way it keeps the
//	project root it began with — so the switch is about the **next** round,
//	and refusing it while a turn runs would be a rule nobody asked for.
func (s *Server) v2SetAuthorization(ctx context.Context, c *app.RequestContext) {
	sess, err := s.session(ctx, c)
	if err != nil {
		s.v2FailErr(c, err)
		return
	}

	var req authorizationReq
	if err := c.Bind(&req); err != nil {
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, "invalid body", false)
		return
	}
	mode, err := contract.ParseAuthorizationMode(req.Mode)
	if err != nil {
		// ★ An unknown mode is refused, not defaulted: a name nobody
		//   recognises must not become the safest one — the user would
		//   think they had widened the mode, and the session would still
		//   ask.
		s.v2Fail(c, consts.StatusBadRequest, codeInvalidBody, err.Error(), false)
		return
	}

	// ★ The confirmation is demanded **before** anything is written: a
	//   refused switch leaves the session exactly as it was.
	if mode == contract.AuthzFull && !sess.AuthorizationConfirmed() && !req.Confirm {
		s.v2Fail(c, consts.StatusConflict, codeConfirmationRequired,
			"full authorization needs an explicit confirmation", false)
		return
	}

	// ★ The confirmation is recorded **first**. It is a fact about the
	//   person ("they said yes to full authorization for this session"),
	//   and it is independent of whether the mode write lands. ⇒ A mode
	//   write that fails leaves the session's authorization exactly as
	//   narrow as it was — the safe direction — while the recorded
	//   confirmation is consumed by the next switch to full, which is what
	//   "once per session" means.
	if mode == contract.AuthzFull && req.Confirm && !sess.AuthorizationConfirmed() {
		if err := sess.SetAuthorizationConfirmed(true); err != nil {
			s.v2FailInternal(c, err)
			return
		}
		// ★ The confirmation is its own line. ★ It is a fact about the person
		//   and it can happen without the mode change — a confirmation that
		//   was recorded while the mode write failed is still a confirmation,
		//   and merging the two would hide exactly that.
		s.recordOperation(ctx, sess, storage.Operation{
			Kind:  storage.OpSessionAuthorizationConfirmed,
			Actor: storage.ActorUser,
			Mode:  string(contract.AuthzFull),
		})
	}
	if err := sess.SetAuthorizationMode(mode); err != nil {
		s.v2FailInternal(c, err)
		return
	}
	s.recordOperation(ctx, sess, storage.Operation{
		Kind:  storage.OpSessionAuthorizationChanged,
		Actor: storage.ActorUser,
		Mode:  string(mode),
	})

	pending, _ := sess.PendingApproval()
	c.JSON(consts.StatusOK, s.metaOf(ctx, sess, pending, projectIDOf(s.projectSnapshot(ctx), sess.ID())))
}

// applyNewAuthorization gives a session that was just created its mode.
//
// ★ The mode is written **with** the session, not after it: a session that
//
//	is born in the default mode and switched a moment later has a window in
//	which the first turn could run in the wrong mode.
//
// ★ The confirmation is written **first**, and it is a fact about the
//
//	person ("they said yes to full authorization"), independent of whether
//	the mode write lands. ⇒ A mode write that fails leaves the session's
//	authorization exactly as narrow as it was — the safe direction.
//
// ★ The caller removes the session when this fails: a session that exists
//
//	without the mode anybody chose is a session nobody can reason about.
func (s *Server) applyNewAuthorization(sess storage.Session, mode contract.AuthorizationMode, confirmed bool) error {
	if mode == contract.AuthzFull && confirmed && !sess.AuthorizationConfirmed() {
		if err := sess.SetAuthorizationConfirmed(true); err != nil {
			return err
		}
	}
	return sess.SetAuthorizationMode(mode)
}
