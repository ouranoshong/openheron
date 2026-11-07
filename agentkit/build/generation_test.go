package build

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/contract"
)

func TestFirstGenerationAndLaterOnesDeriveTheSameTable(t *testing.T) {
	p := NewPolicy([]string{"write_file"})
	cfg := configFromSpecs("test/model", []string{"disable_me"}, allowOf(p), nil)
	b := newTestBuilderWithConfig(t, cfg)

	first := mustSnapshot(t, b)
	managerOf(t, b).set(cfg)
	second := mustSnapshot(t, b)

	for _, name := range []string{"write_file", "read_file", "execute", "disable_me", "unlisted"} {
		if got, want := second.tableFor(second.agentSpec.defaultAgent).Blocked(name), first.tableFor(first.agentSpec.defaultAgent).Blocked(name); got != want {
			t.Fatalf("%s: first gen %q, later gen %q", name, want, got)
		}
	}
}

func TestMaxDepthDefaultsInOnePlace(t *testing.T) {
	cfg := minimalTestConfig()
	cfg.Server.Agent.MaxDepth = 0
	snap, err := newSnapshot(1, cfg, "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snap.agentSpec.maxDepth != spec.DefaultMaxDepth {
		t.Fatalf("maxDepth = %d, want %d", snap.agentSpec.maxDepth, spec.DefaultMaxDepth)
	}

	cfg2 := minimalTestConfig()
	cfg2.Server.Agent.MaxDepth = 3
	snap2, err := newSnapshot(1, cfg2, "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snap2.agentSpec.maxDepth != 3 {
		t.Fatalf("maxDepth = %d, want 3", snap2.agentSpec.maxDepth)
	}
}

func TestIntrospectionWorksBeforeFirstBuild(t *testing.T) {
	cfg := configFromSpecs("test/model", nil,
		allowOf(NewPolicy(nil)), nil)
	b := newTestBuilderWithConfig(t, cfg)
	if b.snap != nil {
		t.Fatal("NewBuilder must not pre-derive a snapshot")
	}
	got := verdicts(t, b.EffectiveTools("main"))
	if v := got["read_file"]; v.Tier != TierAllow {
		t.Fatalf("read_file = %+v, want allow even before the first Build", v)
	}
	if len(b.PolicyTools()) == 0 {
		t.Fatal("PolicyTools returned nothing before the first Build")
	}
}

func TestBuildPullsTheCurrentGeneration(t *testing.T) {
	b := newTestBuilder(t)
	if _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery); err != nil {
		t.Fatal(err)
	}
	before := mustSnapshot(t, b).gen

	managerOf(t, b).set(minimalTestConfig())
	if _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery); err != nil {
		t.Fatal(err)
	}
	if got := mustSnapshot(t, b).gen; got != before+1 {
		t.Fatalf("Build did not pull the new generation: gen=%d, want %d", got, before+1)
	}
}

func TestManagerGenerationIsMonotonic(t *testing.T) {
	m := testManager(nil)
	_, g1 := m.Current()
	m.set(minimalTestConfig())
	_, g2 := m.Current()
	m.set(minimalTestConfig())
	_, g3 := m.Current()
	if !(g1 < g2 && g2 < g3) {
		t.Fatalf("generations must increase: %d, %d, %d", g1, g2, g3)
	}
}

func TestModelIsResolvedAtBuildTime(t *testing.T) {
	b := newTestBuilder(t)
	calls := 0
	b.models = func(context.Context, *spec.Config, string, CallIntent) (
		model.BaseModel[*schema.Message], error) {
		calls++
		return &stubModel{}, nil
	}

	if calls != 0 {
		t.Fatalf("NewBuilder must not resolve models, got %d calls", calls)
	}
	mustSnapshot(t, b)
	if calls != 0 {
		t.Fatalf("deriving a snapshot must not resolve models, got %d calls", calls)
	}
	if _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery); err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Fatal("Build must resolve the root model")
	}
}

func TestSwitchedRootUsesItsOwnModel(t *testing.T) {
	cfg := configFromSpecs("root/model", nil, nil, []decl.SubAgentSpec{
		{ID: "worker", ModelRef: "worker/model"},
	})
	b := newTestBuilderWithConfig(t, cfg)
	b.models = func(_ context.Context, _ *spec.Config, ref string,
		_ CallIntent) (model.BaseModel[*schema.Message], error) {

		switch ref {
		case "root/model":
			return modelA, nil
		case "worker/model":
			return modelB, nil
		default:
			return &stubModel{}, nil
		}
	}

	ctx := context.Background()
	if _, err := b.Build(ctx, "s1", "", contract.TurnQuery); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Build(ctx, "s1", "worker", contract.TurnQuery); err != nil {
		t.Fatal(err)
	}

	b.pool.mu.Lock()
	mainEntry := b.pool.ents[spec.DefaultAgentID]
	workerEntry := b.pool.ents["worker"]
	b.pool.mu.Unlock()
	if mainEntry == nil || workerEntry == nil {
		t.Fatalf("missing pool entries: main=%v worker=%v", mainEntry, workerEntry)
	}
	if mainEntry.model != modelA {
		t.Fatalf("main model = %p, want modelA %p", mainEntry.model, modelA)
	}
	if workerEntry.model != modelB {
		t.Fatalf("worker model = %p, want modelB %p (it must not inherit main's)", workerEntry.model, modelB)
	}
}

func TestRootOnlyPhasesStayAtRoot(t *testing.T) {
	cfg := configFromSpecs("root/model", nil, nil, []decl.SubAgentSpec{
		{ID: "worker", ModelRef: "worker/model"},
	})
	b := newTestBuilderWithConfig(t, cfg)
	p := &fakeProvider{id: "fake", tools: []tool.BaseTool{&stubTool{name: "fake_tool"}}}
	b.tools = registryOf(p)

	if _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 {
		t.Fatalf("tool providers ran %d times, want exactly 1 per generation", p.calls)
	}
}

func TestSubAgentWithoutModelFallsBackToTheBase(t *testing.T) {
	cfg := configFromSpecs("root/model", nil, nil, []decl.SubAgentSpec{
		{ID: "worker"},
	})
	b := newTestBuilderWithConfig(t, cfg)
	b.models = func(_ context.Context, _ *spec.Config, ref string,
		_ CallIntent) (model.BaseModel[*schema.Message], error) {

		if ref == "root/model" {
			return modelA, nil
		}
		return modelB, nil
	}

	bc := countBuilds(t)
	if _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, cfg := range bc.inputs {
		if cfg.Name != "worker" {
			continue
		}
		found = true
		if cfg.ChatModel != modelA {
			t.Fatalf("worker model = %p, want the root fallback %p", cfg.ChatModel, modelA)
		}
	}
	if !found {
		t.Fatal("the worker sub-agent was never assembled")
	}
}
