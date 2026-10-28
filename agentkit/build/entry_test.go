package build

import (
	"context"
	"testing"

	"openheron/agentkit/spec"
	"openheron/agentkit/spec/decl"
	"openheron/contract"
)

func TestTheEntryAgentBuildsItsOwnTree(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, decl.Policy{},
		[]decl.SubAgentSpec{{ID: "worker", ModelRef: "test/model"}})
	cfg.Agents[spec.DefaultAgentID].SubAgents = []string{"worker"}
	cfg.Agents["worker"].SubAgents = nil

	b := newTestBuilderWithConfig(t, cfg)

	bc := countBuilds(t)
	if _, err := b.Build(context.Background(), "main", "main", contract.TurnQuery); err != nil {
		t.Fatalf("build main: %v", err)
	}
	if bc.n != 2 {
		t.Fatalf("main's tree must build main + worker, got %d builds", bc.n)
	}
	for _, in := range bc.inputs {
		if in.Name == "worker" && len(in.SubAgents) != 0 {
			t.Fatalf("worker under main must have no children, got %v", in.SubAgents)
		}
	}

	bc2 := countBuilds(t)
	if _, err := b.Build(context.Background(), "main", "worker", contract.TurnQuery); err != nil {
		t.Fatalf("build worker: %v", err)
	}
	if bc2.n != 1 {
		t.Fatalf("worker's own tree must build only itself, got %d builds", bc2.n)
	}
	if got := entryName(bc2); got != "worker" {
		t.Fatalf("the entry must be worker, got %q", got)
	}
}

func entryName(bc *buildCount) string {
	if len(bc.inputs) == 0 {
		return ""
	}
	return bc.inputs[len(bc.inputs)-1].Name
}

func TestTheDefaultAgentNeedsNoConfiguration(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, decl.Policy{},
		[]decl.SubAgentSpec{{ID: "worker", ModelRef: "test/model"}})
	b := newTestBuilderWithConfig(t, cfg)

	bc := countBuilds(t)
	if _, err := b.Build(context.Background(), "main", "", contract.TurnQuery); err != nil {
		t.Fatalf("build the default agent: %v", err)
	}
	if bc.n != 2 {
		t.Fatalf("the default agent must build main + worker, got %d builds", bc.n)
	}
	if got := entryName(bc); got != spec.DefaultAgentID {
		t.Fatalf("the default agent must be main, got %q", got)
	}
}
