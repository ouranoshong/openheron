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
			"test/big":   {ID: "big", ContextWindow: 131072, SummarizeAt: 104858},
			"test/small": {ID: "small", ContextWindow: 8000},
			"test/plain": {ID: "plain"},
		},
		Agents: map[string]*decl.Agent{
			"main":  {ID: "main", ModelRef: "test/big", MaxIterations: 1, SubAgents: []string{"coder", "task"}},
			"coder": {ID: "coder", ModelRef: "test/small", MaxIterations: 1},
			"task":  {ID: "task", MaxIterations: 1},
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
	if main.Window != 131072 || main.SummarizeAt != 104858 {
		t.Fatalf("main's context = %+v, want window 131072 and summarizeAt 104858", main)
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
