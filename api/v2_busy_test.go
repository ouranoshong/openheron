package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// A second run on a session that already has a live turn must be refused
// with 409 and the code "session_busy" — and the running turn must not be
// disturbed by the refusal.
func TestV2RunOnBusySessionIsConflict(t *testing.T) {
	srv, _ := newV2RunServerWith(t, blockingBuilder{})
	sess := mustCreate(t, srv)

	// Hold the session open with a live turn. The turn is submitted
	// directly: an HTTP run would not return until the stream closed, and
	// the blocking agent never closes it.
	if _, err := srv.rt.Submit(context.Background(), sess.ID(), "", "hold the turn open", nil); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	t.Cleanup(func() { _ = srv.rt.Abort(context.Background(), sess.ID()) })

	got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": sess.ID(),
		"query":    "second",
	})
	if got.code != consts.StatusConflict {
		t.Fatalf("= %d, want 409: %s", got.code, got.body)
	}
	var body errBody
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if body.Code != codeSessionBusy {
		t.Fatalf("code = %q, want %q", body.Code, codeSessionBusy)
	}
	if !body.Retryable {
		t.Fatal("a busy session clears on its own, so the conflict is retryable")
	}
}

// The refusal must not have disturbed the running turn, and the session
// must still report itself as running.
func TestV2RunOnBusySessionDoesNotDisturbTheRun(t *testing.T) {
	srv, _ := newV2RunServerWith(t, blockingBuilder{})
	sess := mustCreate(t, srv)

	if _, err := srv.rt.Submit(context.Background(), sess.ID(), "", "hold the turn open", nil); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	t.Cleanup(func() { _ = srv.rt.Abort(context.Background(), sess.ID()) })

	if got := callJSON(t, srv, "POST", "/v2/run", map[string]any{
		"threadId": sess.ID(),
		"query":    "second",
	}); got.code != consts.StatusConflict {
		t.Fatalf("= %d, want 409: %s", got.code, got.body)
	}

	// The session still reports a live turn.
	detail := callJSON(t, srv, "GET", "/v2/sessions/"+sess.ID(), nil)
	if detail.code != consts.StatusOK {
		t.Fatalf("get session = %d (%s)", detail.code, detail.body)
	}
	if !strings.Contains(detail.body, `"state":"running"`) {
		t.Fatalf("the session must still be running: %s", detail.body)
	}
}
