package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/agentkit/run"
)

// stubCompactor hands out a summarizer that answers with a fixed summary.
type stubCompactor struct {
	content string
	err     error
}

func (s stubCompactor) Summarizer(context.Context, string) (run.Summarizer, error) {
	if s.err != nil {
		return nil, s.err
	}
	return stubSummarizer{content: s.content}, nil
}

type stubSummarizer struct{ content string }

func (s stubSummarizer) Summarize(context.Context, []*schema.Message) (*schema.Message, error) {
	return &schema.Message{Role: schema.User, Content: s.content}, nil
}

// seedCompactable fills the session with a conversation worth compacting.
func seedCompactable(t *testing.T, srv *Server, threadID string, messages int) {
	t.Helper()
	sess, err := srv.sm.Get(context.Background(), threadID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for i := 0; i < messages; i++ {
		if err := sess.Append(agentMessage("main", "line", 20000)); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
}

// The manual compaction writes the same boundary the threshold writes, and
// reports what it achieved.
func TestV2CompactWritesTheBoundary(t *testing.T) {
	srv := newV2Server(t)
	srv.SetCompactorSource(stubCompactor{content: "the conversation so far"})
	id := createThread(t, srv)
	seedCompactable(t, srv, id, 6)

	got := callJSON(t, srv, "POST", "/v2/sessions/"+id+"/compact", nil)
	if got.code != consts.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", got.code, got.body)
	}
	var out struct {
		ThreadID    string `json:"threadId"`
		Superseded  int    `json:"superseded"`
		SavedTokens int    `json:"savedTokens"`
		UsedTokens  int    `json:"usedTokens"`
		At          string `json:"at"`
	}
	if err := json.Unmarshal([]byte(got.body), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if out.ThreadID != id {
		t.Fatalf("threadId = %q, want %q", out.ThreadID, id)
	}
	if out.Superseded != 6 {
		t.Fatalf("superseded = %d, want 6", out.Superseded)
	}
	if out.SavedTokens <= 0 {
		t.Fatalf("savedTokens = %d, want a positive estimate", out.SavedTokens)
	}
	if out.UsedTokens <= 0 {
		t.Fatalf("usedTokens = %d, want the summary's size", out.UsedTokens)
	}
	if out.At == "" {
		t.Fatal("the compaction must say when it happened")
	}

	// The transcript grew by the summary, and the summary is the new
	// boundary.
	view := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/messages", nil)
	var messages messagesView
	if err := json.Unmarshal([]byte(view.body), &messages); err != nil {
		t.Fatalf("decode: %v (%s)", err, view.body)
	}
	found := false
	for _, m := range messages.Messages {
		if m.Metadata["compaction"] != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("the history must carry the compaction marker")
	}
}

// A context that is too small is refused, and the reason says so.
func TestV2CompactRefusesATinyContext(t *testing.T) {
	srv := newV2Server(t)
	srv.SetCompactorSource(stubCompactor{content: "the conversation so far"})
	id := createThread(t, srv)
	seedCompactable(t, srv, id, 2)

	got := callJSON(t, srv, "POST", "/v2/sessions/"+id+"/compact", nil)
	if got.code != consts.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", got.code, got.body)
	}
	if code := decodeCode(t, got.body); code != codeInvalidBody {
		t.Fatalf("code = %q, want %q", code, codeInvalidBody)
	}
}

// An unknown session is a 404, like every other session endpoint.
func TestV2CompactOnAnUnknownThread(t *testing.T) {
	srv := newV2Server(t)
	srv.SetCompactorSource(stubCompactor{content: "the conversation so far"})

	got := callJSON(t, srv, "POST", "/v2/sessions/does-not-exist/compact", nil)
	if got.code != consts.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", got.code, got.body)
	}
}

// A running turn owns the context: compacting under it would cut the
// history the turn is working with.
func TestV2CompactRefusesWhileARunIsInProgress(t *testing.T) {
	srv, _ := newV2RunServerWith(t, blockingBuilder{})
	srv.SetCompactorSource(stubCompactor{content: "the conversation so far"})

	sess := mustCreate(t, srv)
	if _, err := srv.rt.Submit(context.Background(), sess.ID(), "keep going"); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	got := callJSON(t, srv, "POST", "/v2/sessions/"+sess.ID()+"/compact", nil)
	if got.code != consts.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", got.code, got.body)
	}
}
