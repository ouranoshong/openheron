package api

import (
	"encoding/json"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// ★ The mode is a **session** property, so it is answered by the session's
//
//	own route and reported by the session's own projection. A client that
//	has to ask a second question to learn the mode would keep a second copy
//	of it, and two copies disagree.

// A session that never chose a mode reads as the default — not as an empty
// string, and not as a missing field.
func TestASessionReportsTheDefaultMode(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	var meta threadMeta
	got := callJSON(t, srv, "GET", "/v2/sessions/"+id, nil)
	if err := json.Unmarshal([]byte(got.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if meta.AuthorizationMode != "default" {
		t.Fatalf("authorizationMode = %q, want default", meta.AuthorizationMode)
	}
	if meta.AuthorizationConfirmed {
		t.Fatal("a session that never confirmed must not say it did")
	}
}

// The switch is persisted and reported by the next read.
func TestSwitchingToTrustedIsPersistedAndReported(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+id+"/authorization",
		map[string]any{"mode": "trusted"})
	if got.code != consts.StatusOK {
		t.Fatalf("switch = %d, want 200: %s", got.code, got.body)
	}
	var meta threadMeta
	if err := json.Unmarshal([]byte(got.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if meta.AuthorizationMode != "trusted" {
		t.Fatalf("the answer must carry the new mode, got %q", meta.AuthorizationMode)
	}

	read := callJSON(t, srv, "GET", "/v2/sessions/"+id, nil)
	var detail threadDetail
	if err := json.Unmarshal([]byte(read.body), &detail); err != nil {
		t.Fatalf("decode: %v (%s)", err, read.body)
	}
	if detail.AuthorizationMode != "trusted" {
		t.Fatalf("the mode must survive the round trip, got %q", detail.AuthorizationMode)
	}
}

// ★ Full authorization is refused without the confirmation — **by the
//
//	server**. A client that forgot to ask must not get it by accident, and
//
// the refusal has to be a code the client can branch on.
func TestFullAuthorizationIsRefusedWithoutTheConfirmation(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+id+"/authorization",
		map[string]any{"mode": "full"})
	if got.code != consts.StatusConflict {
		t.Fatalf("= %d, want 409: %s", got.code, got.body)
	}
	var body errBody
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if body.Code != codeConfirmationRequired {
		t.Fatalf("code = %q, want %q", body.Code, codeConfirmationRequired)
	}

	// ★ The refusal changed nothing.
	read := callJSON(t, srv, "GET", "/v2/sessions/"+id, nil)
	var detail threadDetail
	if err := json.Unmarshal([]byte(read.body), &detail); err != nil {
		t.Fatalf("decode: %v (%s)", err, read.body)
	}
	if detail.AuthorizationMode != "default" {
		t.Fatalf("the refused switch must leave the mode alone, got %q", detail.AuthorizationMode)
	}
}

// The confirmed switch lands, and the session remembers that it confirmed.
func TestTheConfirmedSwitchToFullLands(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+id+"/authorization",
		map[string]any{"mode": "full", "confirm": true})
	if got.code != consts.StatusOK {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}
	var meta threadMeta
	if err := json.Unmarshal([]byte(got.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if meta.AuthorizationMode != "full" {
		t.Fatalf("authorizationMode = %q, want full", meta.AuthorizationMode)
	}
	if !meta.AuthorizationConfirmed {
		t.Fatal("the confirmation must be reported, or a second surface asks again")
	}
}

// ★ The confirmation is **once per session**: the second switch to full
//
//	does not ask again. A client that kept its own copy would ask twice,
//	and the person would be asked about a mode they already granted.
func TestTheSecondSwitchToFullDoesNotAskAgain(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	first := callJSON(t, srv, "PATCH", "/v2/sessions/"+id+"/authorization",
		map[string]any{"mode": "full", "confirm": true})
	if first.code != consts.StatusOK {
		t.Fatalf("the first switch = %d, want 200: %s", first.code, first.body)
	}

	// ★ Away and back: the confirmation survives the way back.
	away := callJSON(t, srv, "PATCH", "/v2/sessions/"+id+"/authorization",
		map[string]any{"mode": "default"})
	if away.code != consts.StatusOK {
		t.Fatalf("switching away = %d, want 200: %s", away.code, away.body)
	}

	again := callJSON(t, srv, "PATCH", "/v2/sessions/"+id+"/authorization",
		map[string]any{"mode": "full"})
	if again.code != consts.StatusOK {
		t.Fatalf("the second switch = %d, want 200: %s", again.code, again.body)
	}
}

// ★ Another session asks again: the confirmation is the session's, and it
//
//	dies with it.
func TestAnotherSessionAsksAgain(t *testing.T) {
	srv := newV2Server(t)
	first := createThread(t, srv)
	second := createThread(t, srv)

	ok := callJSON(t, srv, "PATCH", "/v2/sessions/"+first+"/authorization",
		map[string]any{"mode": "full", "confirm": true})
	if ok.code != consts.StatusOK {
		t.Fatalf("the first session = %d, want 200: %s", ok.code, ok.body)
	}

	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+second+"/authorization",
		map[string]any{"mode": "full"})
	if got.code != consts.StatusConflict {
		t.Fatalf("the second session = %d, want 409: %s", got.code, got.body)
	}
	var body errBody
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if body.Code != codeConfirmationRequired {
		t.Fatalf("code = %q, want %q", body.Code, codeConfirmationRequired)
	}
}

// The middle mode needs no confirmation: it only waives writes inside the
// trusted paths, and the person asked for it.
func TestTheMiddleModeNeedsNoConfirmation(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+id+"/authorization",
		map[string]any{"mode": "trusted"})
	if got.code != consts.StatusOK {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}
	var meta threadMeta
	if err := json.Unmarshal([]byte(got.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if meta.AuthorizationConfirmed {
		t.Fatal("the middle mode must not record a full-authorization confirmation")
	}
}

// ★ An unknown mode is refused, not defaulted. A name nobody recognises
//
//	must not become the safest one: the user would think they had widened
//	the mode, and the session would still ask.
func TestAnUnknownModeIsRefused(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)

	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+id+"/authorization",
		map[string]any{"mode": "yolo"})
	if got.code != consts.StatusBadRequest {
		t.Fatalf("= %d, want 400: %s", got.code, got.body)
	}
	var body errBody
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if body.Code != codeInvalidBody {
		t.Fatalf("code = %q, want %q", body.Code, codeInvalidBody)
	}
}

// An unknown session is a 404, not a silent success.
func TestTheAuthorizationRouteKnowsAnUnknownSession(t *testing.T) {
	srv := newV2Server(t)

	got := callJSON(t, srv, "PATCH", "/v2/sessions/ghost/authorization",
		map[string]any{"mode": "trusted"})
	if got.code != consts.StatusNotFound {
		t.Fatalf("= %d, want 404: %s", got.code, got.body)
	}
	var body errBody
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if body.Code != codeThreadNotFound {
		t.Fatalf("code = %q, want %q", body.Code, codeThreadNotFound)
	}
}

// ★ Changing the mode does **not** need an idle session. A round that is
//
//	already running keeps the mode it began with, so the switch is about
//	the next round — and a rule nobody asked for would only make the
//	control useless exactly when a person wants it.
func TestTheModeMayChangeWhileAnApprovalIsPending(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)
	seedApproval(t, srv, id, "int-1", "write_file")

	got := callJSON(t, srv, "PATCH", "/v2/sessions/"+id+"/authorization",
		map[string]any{"mode": "trusted"})
	if got.code != consts.StatusOK {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}
}

// ── the mode a session is born with ───────────────────────────────────────

// countSessions reports how many sessions the server holds.
func countSessions(t *testing.T, srv *Server) int {
	t.Helper()
	got := callJSON(t, srv, "GET", "/v2/sessions", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("list = %d, want 200: %s", got.code, got.body)
	}
	var list threadList
	if err := json.Unmarshal([]byte(got.body), &list); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	return len(list.Items)
}

// ★ A conversation that does not exist yet has no mode of its own, so the
//
//	mode the user picked travels with the creation request. ★ A session
//	born in the default mode and switched a moment later has a window in
//	which the first turn could run in the wrong mode.
func TestASessionIsBornInTheModeItWasCreatedWith(t *testing.T) {
	srv := newV2Server(t)

	got := callJSON(t, srv, "POST", "/v2/sessions", map[string]any{"authorizationMode": "trusted"})
	if got.code != consts.StatusCreated {
		t.Fatalf("create = %d, want 201: %s", got.code, got.body)
	}
	var meta threadDetail
	if err := json.Unmarshal([]byte(got.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if meta.AuthorizationMode != "trusted" {
		t.Fatalf("authorizationMode = %q, want trusted", meta.AuthorizationMode)
	}

	// ★ The mode is the session's own property, so it survives the trip.
	read := callJSON(t, srv, "GET", "/v2/sessions/"+meta.ThreadID, nil)
	var detail threadDetail
	if err := json.Unmarshal([]byte(read.body), &detail); err != nil {
		t.Fatalf("decode: %v (%s)", err, read.body)
	}
	if detail.AuthorizationMode != "trusted" {
		t.Fatalf("the mode must survive, got %q", detail.AuthorizationMode)
	}
}

// ★ Full authorization at birth needs the confirmation too, and the refusal
//
//	leaves **nothing** behind. A session that exists in the default mode
//	because the confirmation was missing is a conversation the user believes
//	is in full authorization and is not.
func TestFullAuthorizationAtBirthNeedsTheConfirmation(t *testing.T) {
	srv := newV2Server(t)
	before := countSessions(t, srv)

	got := callJSON(t, srv, "POST", "/v2/sessions", map[string]any{"authorizationMode": "full"})
	if got.code != consts.StatusConflict {
		t.Fatalf("create = %d, want 409: %s", got.code, got.body)
	}
	var body errBody
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if body.Code != codeConfirmationRequired {
		t.Fatalf("code = %q, want %q", body.Code, codeConfirmationRequired)
	}
	if after := countSessions(t, srv); after != before {
		t.Fatalf("the refusal left %d sessions behind", after-before)
	}
}

// The confirmed birth in full lands, and the session remembers that it
// confirmed — a second surface must not ask again.
func TestTheConfirmedBirthInFullLands(t *testing.T) {
	srv := newV2Server(t)

	got := callJSON(t, srv, "POST", "/v2/sessions", map[string]any{
		"authorizationMode":      "full",
		"authorizationConfirmed": true,
	})
	if got.code != consts.StatusCreated {
		t.Fatalf("create = %d, want 201: %s", got.code, got.body)
	}
	var meta threadDetail
	if err := json.Unmarshal([]byte(got.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if meta.AuthorizationMode != "full" {
		t.Fatalf("authorizationMode = %q, want full", meta.AuthorizationMode)
	}
	if !meta.AuthorizationConfirmed {
		t.Fatal("the confirmation must be reported, or a second surface asks again")
	}
}

// ★ An unknown mode is refused at birth as well, and the refusal leaves
//
//	nothing behind — the same rule the switch holds.
func TestAnUnknownModeAtBirthIsRefused(t *testing.T) {
	srv := newV2Server(t)
	before := countSessions(t, srv)

	got := callJSON(t, srv, "POST", "/v2/sessions", map[string]any{"authorizationMode": "yolo"})
	if got.code != consts.StatusBadRequest {
		t.Fatalf("create = %d, want 400: %s", got.code, got.body)
	}
	var body errBody
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if body.Code != codeInvalidBody {
		t.Fatalf("code = %q, want %q", body.Code, codeInvalidBody)
	}
	if after := countSessions(t, srv); after != before {
		t.Fatalf("the refusal left %d sessions behind", after-before)
	}
}

// A request that names no mode at all is the old request: the session is
// born in the default mode, and it says so.
func TestASessionBornWithoutAModeReadsAsTheDefault(t *testing.T) {
	srv := newV2Server(t)

	got := callJSON(t, srv, "POST", "/v2/sessions", map[string]any{"agentId": ""})
	if got.code != consts.StatusCreated {
		t.Fatalf("create = %d, want 201: %s", got.code, got.body)
	}
	var meta threadDetail
	if err := json.Unmarshal([]byte(got.body), &meta); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if meta.AuthorizationMode != "default" {
		t.Fatalf("authorizationMode = %q, want default", meta.AuthorizationMode)
	}
	if meta.AuthorizationConfirmed {
		t.Fatal("a session that never confirmed must not say it did")
	}
}
