package api

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
)

func agentMessage(agent, content string, tokens int) *schema.Message {
	m := &schema.Message{
		Role:    schema.Assistant,
		Content: content,
		ResponseMeta: &schema.ResponseMeta{
			Usage: &schema.TokenUsage{
				PromptTokens:     tokens,
				CompletionTokens: tokens,
				TotalTokens:      tokens * 2,
			},
		},
	}
	message.StampAgent(m, agent)
	message.StampNow(m)
	return m
}

func seedAgentMessages(t *testing.T, srv *Server, threadID string) {
	t.Helper()
	sess, err := srv.sm.Get(context.Background(), threadID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, m := range []*schema.Message{
		agentMessage("main", "planning", 10),
		agentMessage("coder", "patching", 20),
		agentMessage("coder", "verifying", 30),
		agentMessage("reviewer", "reading", 40),
	} {
		if err := sess.Append(m); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
}

func TestV2MessagesReportsUsageByAgent(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)
	seedAgentMessages(t, srv, id)

	got := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/messages", nil)
	if got.code != 200 {
		t.Fatalf("status = %d, want 200 (%s)", got.code, got.body)
	}
	var view messagesView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if view.Usage == nil {
		t.Fatal("usage must be reported")
	}
	if len(view.Usage.ByAgent) != 3 {
		t.Fatalf("byAgent must have 3 buckets, got %d (%s)", len(view.Usage.ByAgent), got.body)
	}
	want := map[string]int{"main": 20, "coder": 100, "reviewer": 80}
	for agent, total := range want {
		bucket, ok := view.Usage.ByAgent[agent]
		if !ok {
			t.Fatalf("byAgent is missing %q (%s)", agent, got.body)
		}
		if bucket.TotalTokens != total {
			t.Fatalf("byAgent[%s].totalTokens = %d, want %d", agent, bucket.TotalTokens, total)
		}
	}
	var sum int
	for _, b := range view.Usage.ByAgent {
		sum += b.TotalTokens
	}
	if sum != view.Usage.TotalTokens {
		t.Fatalf("the buckets sum to %d, want the total %d", sum, view.Usage.TotalTokens)
	}
}

func TestV2MessagesFiltersByAgent(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)
	seedAgentMessages(t, srv, id)

	got := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/messages?agent=coder", nil)
	if got.code != 200 {
		t.Fatalf("status = %d, want 200 (%s)", got.code, got.body)
	}
	var view messagesView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if view.Agent != "coder" {
		t.Fatalf("agent = %q, want coder", view.Agent)
	}
	if len(view.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (%s)", len(view.Messages), got.body)
	}
	for _, m := range view.Messages {
		if m.Content != "patching" && m.Content != "verifying" {
			t.Fatalf("unexpected message %q in the coder view", m.Content)
		}
	}
}

func TestV2MessagesFilterKeepsSessionWideUsage(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)
	seedAgentMessages(t, srv, id)

	all := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/messages", nil)
	one := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/messages?agent=coder", nil)

	var allView, oneView messagesView
	if err := json.Unmarshal([]byte(all.body), &allView); err != nil {
		t.Fatalf("decode all: %v", err)
	}
	if err := json.Unmarshal([]byte(one.body), &oneView); err != nil {
		t.Fatalf("decode one: %v", err)
	}
	if oneView.Usage == nil || allView.Usage == nil {
		t.Fatal("both views must report usage")
	}
	if oneView.Usage.TotalTokens != allView.Usage.TotalTokens {
		t.Fatalf("the total must not change with the filter: %d vs %d",
			oneView.Usage.TotalTokens, allView.Usage.TotalTokens)
	}
	if len(oneView.Usage.ByAgent) != len(allView.Usage.ByAgent) {
		t.Fatalf("the breakdown must not change with the filter: %d vs %d",
			len(oneView.Usage.ByAgent), len(allView.Usage.ByAgent))
	}
}

func TestV2MessagesFilterWithNoMatchReturnsEmpty(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)
	seedAgentMessages(t, srv, id)

	got := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/messages?agent="+url.QueryEscape("nobody"), nil)
	if got.code != 200 {
		t.Fatalf("status = %d, want 200 (%s)", got.code, got.body)
	}
	var view messagesView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if len(view.Messages) != 0 {
		t.Fatalf("messages = %d, want 0", len(view.Messages))
	}
}

func TestV2MessagesExposeTheTimestamp(t *testing.T) {
	srv := newV2Server(t)
	id := createThread(t, srv)
	seedAgentMessages(t, srv, id)

	got := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/messages", nil)
	if got.code != 200 {
		t.Fatalf("status = %d, want 200 (%s)", got.code, got.body)
	}
	var view messagesView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}

	var raw struct {
		Messages []struct {
			Metadata map[string]any `json:"metadata"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(got.body), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if len(raw.Messages) == 0 {
		t.Fatal("no messages")
	}
	for i, m := range raw.Messages {
		ts, ok := m.Metadata["createdAt"].(string)
		if !ok || ts == "" {
			t.Fatalf("message %d has no timestamp in its metadata: %v", i, m.Metadata)
		}
	}
}

func TestMessageAgentReadsTheStamp(t *testing.T) {
	m := agentMessage("reviewer", "hi", 1)
	if got := message.MessageAgent(m); got != "reviewer" {
		t.Fatalf("agent = %q, want reviewer", got)
	}
	plain := &schema.Message{Role: schema.User, Content: "hi"}
	if got := message.MessageAgent(plain); got != "" {
		t.Fatalf("an unstamped message must have no agent, got %q", got)
	}
}

var _ = contract.Usage{}
