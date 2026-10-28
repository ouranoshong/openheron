package api

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/cloudwego/eino/schema"

	"openheron/contract"
	"openheron/message"
)

// agentMessage 造一条带归属标记与用量的中间消息。
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

// seedAgentMessages 往会话里塞几条分属不同 agent 的消息。
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

// 用量要能**按 agent 分账** —— 多 agent 模式下「谁花的 token」是头号问题。
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
	// 总量必须等于各桶之和 —— 否则「谁花的」与「花了多少」对不上。
	var sum int
	for _, b := range view.Usage.ByAgent {
		sum += b.TotalTokens
	}
	if sum != view.Usage.TotalTokens {
		t.Fatalf("the buckets sum to %d, want the total %d", sum, view.Usage.TotalTokens)
	}
}

// ★ ?agent=xxx 把视图收窄到某一个 agent 产出的消息。
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

// ★ 过滤**不**收窄用量：用量是会话级的事实。
//
// 若过滤时把 byAgent 也收窄，客户端拿到的各桶之和就对不上总量 ——
// 而「总量」在过滤前后本该是同一个数（同一个会话）。
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

// 一个都不匹配时返回空列表，而不是 404 —— 过滤条件不认识与「会话不存在」
// 是两回事。
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

// ★ 历史回放要把**产生时刻**带出去。
//
// AG-UI 的 Message 有个通用的 Metadata 字段，走它 —— 前端拿到的历史就带着
// 时间，不必再要一个专门的接口。
//
// ★ 原样透出存储值（不重新格式化）：存储格式是**定宽**的，而
//
//	time.RFC3339Nano 会丢掉末尾的零 —— 重新格式化一次就把「可按字典序
//	排序」弄丢了，而那正是这个时间戳存在的理由之一。
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

	// messagesView 不带 metadata 字段（那是 AG-UI 的形状），所以直接看原始 JSON。
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
		// ★ 契约里的名字是 createdAt —— 不是内部键 openheron_ts。
		ts, ok := m.Metadata["createdAt"].(string)
		if !ok || ts == "" {
			t.Fatalf("message %d has no timestamp in its metadata: %v", i, m.Metadata)
		}
	}
}

// 加这个字段是为了让 api 层与测试都能问「这条消息是谁的」，
// 而不必各自去猜 Extra 的键名。
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
