package build

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk/prebuilt/deep"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/contract"
)

func TestSubagentEventsAreForwardedToTheParentStream(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, decl.Policy{}, []decl.SubAgentSpec{
		{ID: "main", ModelRef: "test/model", Description: "根", Sub: []decl.SubAgentSpec{
			{ID: "task", ModelRef: "test/model", Description: "专家"},
		}},
	})
	b := newTestBuilderWithConfig(t, cfg)

	bc := countBuilds(t)
	if _, err := b.Build(context.Background(), "main", "main", contract.TurnQuery); err != nil {
		t.Fatalf("build main: %v", err)
	}
	if bc.n == 0 {
		t.Fatal("the assembly entry was not called")
	}
	for _, in := range bc.inputs {
		if !in.ToolsConfig.EmitInternalEvents {
			t.Fatalf("agent %q: EmitInternalEvents must be on — otherwise the subagent's "+
				"reasoning and tool calls never reach the parent's event stream", in.Name)
		}
	}
}

func TestForwardingIsPairedWithTheContextFilter(t *testing.T) {
	cfg := deep.Config{}
	applyToolConfig(&cfg, heavyDeps{})
	if !cfg.ToolsConfig.EmitInternalEvents {
		t.Fatal("applyToolConfig must turn EmitInternalEvents on")
	}
	if len(cfg.ToolsConfig.Tools) != 0 {
		t.Fatalf("the assembly layer must not hold messages, got %d tools", len(cfg.ToolsConfig.Tools))
	}
}
