package api

import (
	"encoding/json"
	"testing"

	"github.com/ouranoshong/openheron/contract"
)

type stubContextSource map[string]contract.AgentContext

func (s stubContextSource) Contexts() map[string]contract.AgentContext { return s }

// The session view carries the context occupancy next to the usage: they are
// different quantities, and the meter needs the second one.
func TestV2MessagesReportsTheContextUsage(t *testing.T) {
	srv := newV2Server(t)
	srv.SetContextSource(stubContextSource{
		"main":  {Window: 131072, SummarizeAt: 104858, Provider: "deepseek", Model: "flash"},
		"coder": {Window: 8000, Provider: "deepseek", Model: "small"},
	})
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
	if len(view.Usage.Context) != 3 {
		t.Fatalf("the context covers %d agents, want 3 (%s)", len(view.Usage.Context), got.body)
	}

	// The occupancy is the last request of that agent, not the sum.
	main := view.Usage.Context["main"]
	if main.UsedTokens != 20 {
		t.Fatalf("main's occupancy = %d tokens, want 20", main.UsedTokens)
	}
	if main.ContextWindow != 131072 {
		t.Fatalf("main's window = %d, want 131072", main.ContextWindow)
	}
	if main.SummarizeAt != 104858 {
		t.Fatalf("main's summarizeAt = %d, want 104858", main.SummarizeAt)
	}
	if main.Provider != "deepseek" || main.Model != "flash" {
		t.Fatalf("main's model = %s/%s, want deepseek/flash", main.Provider, main.Model)
	}

	coder := view.Usage.Context["coder"]
	if coder.UsedTokens != 60 {
		t.Fatalf("coder's occupancy = %d tokens, want 60 (its last request)", coder.UsedTokens)
	}
	if coder.ContextWindow != 8000 {
		t.Fatalf("coder's window = %d, want 8000", coder.ContextWindow)
	}
}

// Without a window the occupancy is still reported, but there is no ratio to
// show: the client has to know the difference between "0" and "not
// configured".
func TestV2MessagesReportsTheOccupancyWithoutAWindow(t *testing.T) {
	srv := newV2Server(t)
	srv.SetContextSource(stubContextSource{})
	id := createThread(t, srv)
	seedAgentMessages(t, srv, id)

	got := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/messages", nil)
	var view messagesView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if view.Usage == nil || len(view.Usage.Context) != 3 {
		t.Fatalf("the context must be reported even without a window (%s)", got.body)
	}
	if view.Usage.Context["main"].ContextWindow != 0 {
		t.Fatalf("the window must be zero, not missing: %+v", view.Usage.Context["main"])
	}
}

// A session with no assistant message has no occupancy at all.
func TestV2MessagesWithoutUsageHasNoContext(t *testing.T) {
	srv := newV2Server(t)
	srv.SetContextSource(stubContextSource{"main": {Window: 131072}})
	id := createThread(t, srv)

	got := callJSON(t, srv, "GET", "/v2/sessions/"+id+"/messages", nil)
	var view messagesView
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if view.Usage != nil {
		t.Fatalf("a session without usage must report no usage (%s)", got.body)
	}
}
