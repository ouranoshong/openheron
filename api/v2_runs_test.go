package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// The runs scan is the truth about a session's runs: it lists them and it
// names the one that owns the session.
func TestV2SessionRunsListsTheRunsAndTheActiveOne(t *testing.T) {
	srv, _ := newV2RunServerWith(t, blockingBuilder{})
	sess := mustCreate(t, srv)

	turn, err := srv.rt.Submit(context.Background(), sess.ID(), "", "hold the turn open")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	t.Cleanup(func() { _ = srv.rt.Abort(context.Background(), sess.ID()) })

	got := callJSON(t, srv, "GET", "/v2/sessions/"+sess.ID()+"/runs", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}
	var view runsView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if len(view.Items) != 1 {
		t.Fatalf("want 1 run, got %d: %s", len(view.Items), got.body)
	}
	if view.Items[0].RunID != turn.RunID {
		t.Fatalf("want the run %q, got %q", turn.RunID, view.Items[0].RunID)
	}
	if view.ActiveRunID != turn.RunID {
		t.Fatalf("want the active run %q, got %q", turn.RunID, view.ActiveRunID)
	}
	if view.Items[0].Epoch == 0 {
		t.Fatal("the run must carry a fence")
	}
}

// The session's own route carries the active run's id. It used to be
// always empty: the runtime never assigned the turn's run id.
func TestV2SessionCarriesTheActiveRunID(t *testing.T) {
	srv, _ := newV2RunServerWith(t, blockingBuilder{})
	sess := mustCreate(t, srv)

	turn, err := srv.rt.Submit(context.Background(), sess.ID(), "", "hold the turn open")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	t.Cleanup(func() { _ = srv.rt.Abort(context.Background(), sess.ID()) })

	got := callJSON(t, srv, "GET", "/v2/sessions/"+sess.ID(), nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}
	var detail threadDetail
	if err := json.Unmarshal([]byte(got.body), &detail); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if detail.RunID != turn.RunID {
		t.Fatalf("want the run id %q, got %q", turn.RunID, detail.RunID)
	}
}

// One run of the session, by id.
func TestV2SessionRunByID(t *testing.T) {
	srv, _ := newV2RunServerWith(t, blockingBuilder{})
	sess := mustCreate(t, srv)

	turn, err := srv.rt.Submit(context.Background(), sess.ID(), "", "hold the turn open")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	t.Cleanup(func() { _ = srv.rt.Abort(context.Background(), sess.ID()) })

	got := callJSON(t, srv, "GET", "/v2/sessions/"+sess.ID()+"/runs/"+turn.RunID, nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}
	var view runView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if view.RunID != turn.RunID {
		t.Fatalf("want the run %q, got %q", turn.RunID, view.RunID)
	}

	missing := callJSON(t, srv, "GET", "/v2/sessions/"+sess.ID()+"/runs/run-does-not-exist", nil)
	if missing.code != consts.StatusNotFound {
		t.Fatalf("= %d, want 404: %s", missing.code, missing.body)
	}
}

// A session with no runs scans as an empty list, not as an error.
func TestV2SessionRunsOfAnIdleSession(t *testing.T) {
	srv, _ := newV2RunServerWith(t, blockingBuilder{})
	sess := mustCreate(t, srv)

	got := callJSON(t, srv, "GET", "/v2/sessions/"+sess.ID()+"/runs", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}
	var view runsView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if len(view.Items) != 0 {
		t.Fatalf("want no runs, got %d", len(view.Items))
	}
	if view.ActiveRunID != "" {
		t.Fatalf("want no active run, got %q", view.ActiveRunID)
	}
}
