package build

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// openheron's context prompt is transient: it is rebuilt every turn and never
// persisted. A system message left in the compacted history would shadow the
// live one.
func TestFinalizeWithoutSystemDropsTheSystemMessage(t *testing.T) {
	original := []*schema.Message{
		{Role: schema.System, Content: "you are the context prompt of openheron"},
		{Role: schema.User, Content: "what is the plan"},
		{Role: schema.Assistant, Content: "the plan is three steps"},
	}
	summary := &schema.Message{
		Role: schema.Assistant,
		Content: "the conversation so far:\n" +
			"<all_user_messages>\n</all_user_messages>\n",
	}

	out, err := finalizeWithoutSystem(context.Background(), original, summary)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("the compacted history has %d messages, want the summary alone", len(out))
	}
	if out[0].Role == schema.System {
		t.Fatal("the compacted history kept a system message")
	}
	if strings.Contains(out[0].Content, "you are the context prompt") {
		t.Fatal("the compacted history carries the transient context prompt")
	}
}

// The user's own words are spliced back into the summary — that is what
// keeps a compacted conversation from losing the intent.
func TestFinalizeWithoutSystemKeepsTheUserMessages(t *testing.T) {
	original := []*schema.Message{
		{Role: schema.System, Content: "context prompt"},
		{Role: schema.User, Content: "do NOT rename the package"},
	}
	summary := &schema.Message{
		Role:    schema.Assistant,
		Content: "summary:\n<all_user_messages>\n</all_user_messages>\n",
	}

	out, err := finalizeWithoutSystem(context.Background(), original, summary)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("the compacted history has %d messages, want 1", len(out))
	}
	if !strings.Contains(out[0].Content, "do NOT rename the package") {
		t.Fatalf("the summary lost the user's own words:\n%s", out[0].Content)
	}
}

// The threshold comes from the model the agent runs on.
func TestSummarizeAtOf(t *testing.T) {
	cfg := &spec.Config{
		Providers: map[string]*decl.Provider{"test": {ID: "test", Type: "openai_compat"}},
		Models: map[string]*decl.Model{
			"test/model": {ID: "model", SummarizeAt: 5000},
			"test/plain": {ID: "plain"},
		},
	}

	if got := summarizeAtOf(cfg, "test/model"); got != 5000 {
		t.Fatalf("summarizeAt = %d, want 5000", got)
	}
	if got := summarizeAtOf(cfg, "test/plain"); got != 0 {
		t.Fatalf("summarizeAt = %d, want 0 for a model that declares none", got)
	}
	if got := summarizeAtOf(cfg, ""); got != 0 {
		t.Fatalf("summarizeAt = %d, want 0 without a model ref", got)
	}
	if got := summarizeAtOf(cfg, "test/missing"); got != 0 {
		t.Fatalf("summarizeAt = %d, want 0 for an unknown model", got)
	}
	if got := summarizeAtOf(nil, "test/model"); got != 0 {
		t.Fatalf("summarizeAt = %d, want 0 without a config", got)
	}
}

// Compaction is opt-in: a model that declares no threshold gets no
// middleware, so a conversation that never crosses one pays nothing.
func TestNoMiddlewareWithoutAThreshold(t *testing.T) {
	b := &Builder{}
	snap := &snapshot{cfg: &spec.Config{
		Providers: map[string]*decl.Provider{"test": {ID: "test", Type: "openai_compat"}},
		Models:    map[string]*decl.Model{"test/model": {ID: "model"}},
	}}

	mw, err := b.summarizationMiddleware(context.Background(), snap, "test/model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if mw != nil {
		t.Fatal("a model without a threshold must not get the compaction middleware")
	}
}
