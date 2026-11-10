package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
	"github.com/ouranoshong/openheron/contract"
)

// The runs scan is the truth about a session's runs: it lists them and it
// names the one that owns the session.
func TestV2SessionRunsListsTheRunsAndTheActiveOne(t *testing.T) {
	srv, _ := newV2RunServerWith(t, blockingBuilder{})
	sess := mustCreate(t, srv)

	turn, err := srv.rt.Submit(context.Background(), sess.ID(), "", "hold the turn open", nil)
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

	turn, err := srv.rt.Submit(context.Background(), sess.ID(), "", "hold the turn open", nil)
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

	turn, err := srv.rt.Submit(context.Background(), sess.ID(), "", "hold the turn open", nil)
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

// attachAgent says one thing and then holds the turn open, so a test can
// attach to a run that is still running.
type attachAgent struct{ released chan struct{} }

func (attachAgent) Name(context.Context) string        { return "attach" }
func (attachAgent) Description(context.Context) string { return "attach" }

func (a attachAgent) Run(ctx context.Context, _ *adk.TypedAgentInput[*schema.Message],
	_ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]] {
	iter, gen := adk.NewAsyncIteratorPair[*adk.TypedAgentEvent[*schema.Message]]()
	go func() {
		defer gen.Close()
		gen.Send(&adk.TypedAgentEvent[*schema.Message]{
			Output: &adk.AgentOutput{
				MessageOutput: &adk.TypedMessageVariant[*schema.Message]{
					Role:    schema.Assistant,
					Message: &schema.Message{Role: schema.Assistant, Content: "working"},
				},
			},
		})
		select {
		case <-a.released:
		case <-ctx.Done():
		}
	}()
	return iter
}

type attachBuilder struct{ released chan struct{} }

func (b attachBuilder) Build(context.Context, string, string, contract.TurnKind) (adk.TypedAgent[*schema.Message], error) {
	return attachAgent{released: b.released}, nil
}

// Contexts reports what each agent's context allows. The fake has no
// models, so there are no windows.
func (b attachBuilder) Contexts() map[string]contract.AgentContext { return nil }

// A client that comes back for a run it already knows attaches to it.
//
// ★ The stream carries the run's events, and no second run is started: the
//
//	request carries no question, so there is nothing to run.
func TestV2RunAttachesToALiveRun(t *testing.T) {
	released := make(chan struct{})
	srv, captured := newV2RunServerWith(t, attachBuilder{released: released})
	sess := mustCreate(t, srv)

	turn, err := srv.rt.Submit(context.Background(), sess.ID(), "", "hold the turn open", nil)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	t.Cleanup(func() { _ = srv.rt.Abort(context.Background(), sess.ID()) })

	type outcome struct {
		res result
	}
	done := make(chan outcome, 1)
	go func() {
		done <- outcome{res: call(t, srv, "POST", "/v2/run",
			`{"threadId":"`+sess.ID()+`","runId":"`+turn.RunID+`"}`)}
	}()

	// The attached client sees the run's output.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(captured.String(), "working") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	close(released)
	got := (<-done).res

	if got.code != consts.StatusOK {
		t.Fatalf("= %d, want 200: %s", got.code, got.body)
	}
	frames := captured.String()
	if !strings.Contains(frames, turn.RunID) {
		t.Fatalf("the stream must carry the run's id %q: %s", turn.RunID, frames)
	}
	if !strings.Contains(frames, "working") {
		t.Fatalf("the attached client must see the run's output: %s", frames)
	}

	// And no second run was started: the attach is not a new turn.
	snap, err := srv.rt.ScanRuns(context.Background(), sess.ID())
	if err != nil {
		t.Fatalf("ScanRuns: %v", err)
	}
	if len(snap.Runs) != 1 {
		t.Fatalf("the attach must not start a run, got %d runs", len(snap.Runs))
	}
}

// A run that is over cannot be attached to.
//
// ★ The client has to hydrate from the transcript; the run's own facts are
//
//	in the scan. Silently starting a new run here would run the user's
//	message a second time.
func TestV2RunRefusesToAttachToAFinishedRun(t *testing.T) {
	released := make(chan struct{})
	srv, _ := newV2RunServerWith(t, attachBuilder{released: released})
	sess := mustCreate(t, srv)

	turn, err := srv.rt.Submit(context.Background(), sess.ID(), "", "hold the turn open", nil)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	close(released)
	// Wait for the run to be over.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := srv.rt.LiveRun(sess.ID(), turn.RunID); !ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": sess.ID(),
		"runId":    turn.RunID,
	})
	if got.code != consts.StatusNotFound {
		t.Fatalf("= %d, want 404: %s", got.code, got.body)
	}
	var body errBody
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if body.Code != codeRunNotLive {
		t.Fatalf("code = %q, want %q", body.Code, codeRunNotLive)
	}
}
