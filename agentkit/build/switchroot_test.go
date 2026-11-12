package build

import (
	"context"
	"errors"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/contract"
)

func TestEveryDeclaredAgentCanBeTheSessionAgent(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, nil, []decl.SubAgentSpec{
		{ID: "worker", ModelRef: "test/model", Sub: []decl.SubAgentSpec{
			{ID: "leaf", ModelRef: "test/model"},
		}},
	})
	b := newTestBuilderWithConfig(t, cfg)

	ctx := context.Background()
	if _, err := b.Build(ctx, "s1", "worker", contract.TurnQuery); err != nil {
		t.Fatalf("a declared agent must be usable as the session agent: %v", err)
	}
	if _, err := b.Build(ctx, "s1", "leaf", contract.TurnQuery); err != nil {
		t.Fatalf("a nested declared agent must be usable as the session agent: %v", err)
	}
	if _, err := b.Build(ctx, "s1", "ghost", contract.TurnQuery); !errors.Is(err, errAgentNotDeclared) {
		t.Fatalf("an undeclared agent must be rejected, got %v", err)
	}
}

// TestPoolSeparatesFingerprints is the pool's new rule.
//
// ★ The key is the fingerprint, so two fingerprints are two agents — and
//
//	each one is built once.
func TestPoolSeparatesFingerprints(t *testing.T) {
	p := newPool()
	c := &countingBuild{}

	old, err := p.GetOrBuild(context.Background(), "task", "fp1", c.fn("task"))
	if err != nil {
		t.Fatal(err)
	}
	cur, err := p.GetOrBuild(context.Background(), "task", "fp2", c.fn("task"))
	if err != nil {
		t.Fatal(err)
	}
	if old == cur {
		t.Fatal("★ entries from different fingerprints must not be shared")
	}
	if c.count() != 2 {
		t.Fatalf("each fingerprint must build once, got %d builds", c.count())
	}
}

func TestBuildSwitchableRootIsPooledSeparately(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, nil, []decl.SubAgentSpec{
		{ID: "worker", Description: "worker", Instruction: "do work", ModelRef: "test/model"},
	})
	b := newTestBuilderWithConfig(t, cfg)

	ctx := context.Background()
	mainAgent, err := b.Build(ctx, "s1", "", contract.TurnQuery)
	if err != nil {
		t.Fatalf("build main: %v", err)
	}
	worker, err := b.Build(ctx, "s1", "worker", contract.TurnQuery)
	if err != nil {
		t.Fatalf("build worker as root: %v", err)
	}
	if worker == nil {
		t.Fatal("the switched root must not be nil")
	}
	if worker == mainAgent {
		t.Fatal("the switched root must not reuse the main instance")
	}

	again, err := b.Build(ctx, "s1", "worker", contract.TurnQuery)
	if err != nil {
		t.Fatal(err)
	}
	if again != worker {
		t.Fatal("the second turn must hit the pool, not rebuild")
	}
}

func TestBuildRejectsUnknownRoot(t *testing.T) {
	b := newTestBuilder(t)
	_, err := b.Build(context.Background(), "s1", "ghost", contract.TurnQuery)
	if !errors.Is(err, errAgentNotDeclared) {
		t.Fatalf("want errAgentNotDeclared, got %v", err)
	}
}

func TestBuildRootWithoutModelFailsLoudly(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, nil, []decl.SubAgentSpec{
		{ID: "worker", Description: "worker"},
	})
	b := newTestBuilderWithConfig(t, cfg)

	_, err := b.Build(context.Background(), "s1", "worker", contract.TurnQuery)
	if err == nil {
		t.Fatal("a root without a model reference must be rejected")
	}
}
