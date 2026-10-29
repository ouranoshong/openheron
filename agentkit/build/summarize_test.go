package build

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// openheron's context prompt is a system message in the agent's state. A
// compaction that dropped it would leave the agent without its rules for
// the rest of the turn — the prompt is transient, but it is not optional.
func TestFinalizeKeepsTheSystemMessage(t *testing.T) {
	original := []*schema.Message{
		{Role: schema.System, Content: "you are the context prompt of openheron"},
		{Role: schema.User, Content: "what is the plan"},
		{Role: schema.Assistant, Content: "the plan is three steps"},
	}
	summary := &schema.Message{
		Role:    schema.Assistant,
		Content: "the conversation so far:\n<all_user_messages>\n</all_user_messages>\n",
	}

	out, err := finalize(context.Background(), original, summary)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("the compacted history has %d messages, want the system message and the summary", len(out))
	}
	if out[0].Role != schema.System {
		t.Fatal("the compacted history lost the system message")
	}
	if !strings.Contains(out[0].Content, "you are the context prompt") {
		t.Fatal("the compacted history lost the context prompt")
	}
	if out[1].Role == schema.System {
		t.Fatal("the summary must not be a system message")
	}
}

// The user's own words are spliced back into the summary — that is what
// keeps a compacted conversation from losing the intent.
func TestFinalizeKeepsTheUserMessages(t *testing.T) {
	original := []*schema.Message{
		{Role: schema.System, Content: "context prompt"},
		{Role: schema.User, Content: "do NOT rename the package"},
	}
	summary := &schema.Message{
		Role:    schema.Assistant,
		Content: "summary:\n<all_user_messages>\n</all_user_messages>\n",
	}

	out, err := finalize(context.Background(), original, summary)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("the compacted history has %d messages, want 2", len(out))
	}
	found := false
	for _, m := range out {
		if strings.Contains(m.Content, "do NOT rename the package") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the summary lost the user's own words:\n%s", out[len(out)-1].Content)
	}
}

// An agent that says nothing gets the default threshold: 80% of the window
// of the model it runs on.
func TestTheDefaultThresholdIsEightyPercent(t *testing.T) {
	b := &Builder{}
	snap := &snapshot{cfg: &spec.Config{
		Providers: map[string]*decl.Provider{"test": {ID: "test", Type: "openai_compat"}},
		Models:    map[string]*decl.Model{"test/model": {ID: "model", ContextWindow: 100000}},
	}}

	mw, err := b.summarizationMiddleware(context.Background(), snap,
		&decl.SubAgentSpec{ID: "main", ModelRef: "test/model"}, &summarizingModel{})
	if err != nil {
		t.Fatal(err)
	}
	if mw == nil {
		t.Fatal("an agent that says nothing must get the default threshold")
	}
}

// A model that declares no window leaves the agent with no threshold: there
// is nothing to take 80% of.
func TestNoThresholdWithoutAWindow(t *testing.T) {
	b := &Builder{}
	snap := &snapshot{cfg: &spec.Config{
		Providers: map[string]*decl.Provider{"test": {ID: "test", Type: "openai_compat"}},
		Models:    map[string]*decl.Model{"test/model": {ID: "model"}},
	}}

	mw, err := b.summarizationMiddleware(context.Background(), snap,
		&decl.SubAgentSpec{ID: "main", ModelRef: "test/model"}, &summarizingModel{})
	if err != nil {
		t.Fatal(err)
	}
	if mw != nil {
		t.Fatal("a model without a window must not give the agent a threshold")
	}
}

// The threshold belongs to the agent: a percentage of the window of the
// model it runs on.
func TestTheThresholdComesFromTheAgent(t *testing.T) {
	b := &Builder{}
	snap := &snapshot{cfg: &spec.Config{
		Providers: map[string]*decl.Provider{"test": {ID: "test", Type: "openai_compat"}},
		Models:    map[string]*decl.Model{"test/model": {ID: "model", ContextWindow: 100000}},
	}}

	mw, err := b.summarizationMiddleware(context.Background(), snap, &decl.SubAgentSpec{
		ID:          "main",
		ModelRef:    "test/model",
		SummarizeAt: decl.Threshold{Percent: 80, Declared: true},
	}, &summarizingModel{})
	if err != nil {
		t.Fatal(err)
	}
	if mw == nil {
		t.Fatal("an agent with a threshold must get the compaction middleware")
	}
}

// An agent that says "off" never compacts, even on a model with a window.
func TestAnAgentThatSaysOffNeverCompacts(t *testing.T) {
	b := &Builder{}
	snap := &snapshot{cfg: &spec.Config{
		Providers: map[string]*decl.Provider{"test": {ID: "test", Type: "openai_compat"}},
		Models:    map[string]*decl.Model{"test/model": {ID: "model", ContextWindow: 100000}},
	}}

	mw, err := b.summarizationMiddleware(context.Background(), snap, &decl.SubAgentSpec{
		ID:          "main",
		ModelRef:    "test/model",
		SummarizeAt: decl.Threshold{Off: true, Declared: true},
	}, &summarizingModel{})
	if err != nil {
		t.Fatal(err)
	}
	if mw != nil {
		t.Fatal("an agent that says off must not get the compaction middleware")
	}
}
