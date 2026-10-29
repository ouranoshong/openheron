package build

import (
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

func contextTestConfig() *spec.Config {
	return &spec.Config{
		Providers: map[string]*decl.Provider{
			"test": {ID: "test", Type: "openai_compat"},
		},
		Models: map[string]*decl.Model{
			"test/big":   {ID: "big", ContextWindow: 131072},
			"test/small": {ID: "small", ContextWindow: 8000},
			"test/plain": {ID: "plain"},
		},
		Agents: map[string]*decl.Agent{
			"main": {
				ID: "main", ModelRef: "test/big", MaxIterations: 1,
				SubAgents:   []string{"coder", "task"},
				SummarizeAt: decl.Threshold{Percent: 80, Declared: true},
			},
			"coder": {
				ID: "coder", ModelRef: "test/small", MaxIterations: 1,
				SummarizeAt: decl.Threshold{Tokens: 4000, Declared: true},
			},
			"task": {ID: "task", MaxIterations: 1},
		},
	}
}

// The window has to be the one of the model the agent actually runs on.
func TestContextsReportsTheWindowOfTheModelEachAgentRunsOn(t *testing.T) {
	b := newTestBuilderWithConfig(t, contextTestConfig())
	got := b.Contexts()

	main, ok := got["main"]
	if !ok {
		t.Fatal("the default agent has no context")
	}
	if main.Window != 131072 {
		t.Fatalf("main's window = %d, want 131072", main.Window)
	}
	if main.Provider != "test" || main.Model != "big" {
		t.Fatalf("main's model = %s/%s, want test/big", main.Provider, main.Model)
	}

	coder := got["coder"]
	if coder.Window != 8000 {
		t.Fatalf("coder's window = %d, want 8000", coder.Window)
	}

	// A sub-agent without its own model runs on its parent's model.
	task := got["task"]
	if task.Window != 131072 {
		t.Fatalf("task's window = %d, want the parent's 131072", task.Window)
	}
}

// The threshold is the agent's own, not the model's: each agent decides for
// itself, against the window it actually runs on.
func TestContextsReportsTheThresholdOfEachAgent(t *testing.T) {
	b := newTestBuilderWithConfig(t, contextTestConfig())
	got := b.Contexts()

	// 80% of 131072.
	if got["main"].SummarizeAt != 104857 {
		t.Fatalf("main's threshold = %d, want 104857", got["main"].SummarizeAt)
	}
	// An absolute count, on a smaller window.
	if got["coder"].SummarizeAt != 4000 {
		t.Fatalf("coder's threshold = %d, want 4000", got["coder"].SummarizeAt)
	}
	// An agent that says nothing gets the default: 80% of the window it
	// runs on.
	if got["task"].SummarizeAt != 104857 {
		t.Fatalf("task's threshold = %d, want 104857", got["task"].SummarizeAt)
	}
}

// A model that declares no window reports none — the UI must not render a
// ratio against a window of zero.
func TestContextsReportsNoWindowWhenNoneIsDeclared(t *testing.T) {
	cfg := contextTestConfig()
	cfg.Agents["main"] = &decl.Agent{ID: "main", ModelRef: "test/plain", MaxIterations: 1}

	b := newTestBuilderWithConfig(t, cfg)
	got := b.Contexts()

	if got["main"].Window != 0 {
		t.Fatalf("main's window = %d, want 0", got["main"].Window)
	}
}

// A percentage of an unknown window is no threshold at all: there is
// nothing to take a share of.
func TestAPercentageWithoutAWindowIsNoThreshold(t *testing.T) {
	cfg := contextTestConfig()
	cfg.Agents["main"] = &decl.Agent{
		ID: "main", ModelRef: "test/plain", MaxIterations: 1,
		SummarizeAt: decl.Threshold{Percent: 80, Declared: true},
	}

	b := newTestBuilderWithConfig(t, cfg)
	got := b.Contexts()

	if got["main"].SummarizeAt != 0 {
		t.Fatalf("main's threshold = %d, want 0", got["main"].SummarizeAt)
	}
}
