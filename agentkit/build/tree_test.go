package build

import (
	"context"
	"log/slog"
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
	// ★ A **different object** with the same content: the tree's identity
	//   is the object, so republishing the same one would not move it.
	managerOf(t, b).set(configFromSpecs("test/model", []string{"disable_me"}, allowOf(p), nil))
	second := mustSnapshot(t, b)
	if first == second {
		t.Fatal("a new configuration object must produce a new tree")
	}

	for _, name := range []string{"write_file", "read_file", "execute", "disable_me", "unlisted"} {
		if got, want := second.tableFor(second.agentSpec.defaultAgent).Blocked(name), first.tableFor(first.agentSpec.defaultAgent).Blocked(name); got != want {
			t.Fatalf("%s: first gen %q, later gen %q", name, want, got)
		}
	}
}

func TestMaxDepthDefaultsInOnePlace(t *testing.T) {
	cfg := minimalTestConfig()
	cfg.Server.Agent.MaxDepth = 0
	snap, err := newSnapshot(cfg, "", nil, nil, "", "", nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if snap.agentSpec.maxDepth != spec.DefaultMaxDepth {
		t.Fatalf("maxDepth = %d, want %d", snap.agentSpec.maxDepth, spec.DefaultMaxDepth)
	}

	cfg2 := minimalTestConfig()
	cfg2.Server.Agent.MaxDepth = 3
	snap2, err := newSnapshot(cfg2, "", nil, nil, "", "", nil, nil, nil, nil)
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

// TestBuildPullsTheCurrentConfiguration says the builder reads the
// configuration the manager holds **now**.
//
// ★ It is the rule the generation used to carry, and it is a stronger
//
//	one: a caller cannot name an older tree, because it does not name
//	one — it asks for the tree, and the tree is what the manager holds.
func TestBuildPullsTheCurrentConfiguration(t *testing.T) {
	b := newTestBuilder(t)
	if _, _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery, ""); err != nil {
		t.Fatal(err)
	}
	before := mustSnapshot(t, b).cfg

	next := minimalTestConfig()
	managerOf(t, b).set(next)
	if _, _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery, ""); err != nil {
		t.Fatal(err)
	}
	got := mustSnapshot(t, b).cfg
	if got != next {
		t.Fatalf("Build did not pull the current configuration: %p, want %p", got, next)
	}
	if before == next {
		t.Fatal("the test must publish a different configuration object")
	}
}

// TestTheManagerPublishesWhatItHolds says the manager is the one place
// that answers "what is running".
func TestTheManagerPublishesWhatItHolds(t *testing.T) {
	m := testManager(nil)
	if m.Current() == nil {
		t.Fatal("the manager must hold a configuration")
	}
	next := minimalTestConfig()
	m.Publish(next)
	if got := m.Current(); got != next {
		t.Fatalf("Current = %p, want the published configuration %p", got, next)
	}
}

// TestTheTreeMovesAndTheAgentsDoNot is the rule that replaces the
// generation.
//
// ★ The tree's identity is the configuration **object** and the MCP tool
//
//	surface; the agents' identity is the **fingerprint**, and the
//	fingerprint is content. ★ So a new configuration object is a new
//	tree — the table is derived again, the tree's own bookkeeping moves
//	— while an identical configuration leaves the fingerprint alone, and
//	the pooled agents stay.
//
// ★ That is the split the generation never had: it said "everything
//
//	changed" and made every agent pay for it.
func TestTheTreeMovesAndTheAgentsDoNot(t *testing.T) {
	b := newTestBuilderWithConfig(t, minimalTestConfig())

	first, _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery, "")
	if err != nil {
		t.Fatal(err)
	}
	firstTree := mustSnapshot(t, b)

	// ★ The same content, a different object.
	managerOf(t, b).set(minimalTestConfig())
	second, _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery, "")
	if err != nil {
		t.Fatal(err)
	}
	secondTree := mustSnapshot(t, b)

	if secondTree == firstTree {
		t.Fatal("★ a new configuration object must produce a new tree")
	}
	if second != first {
		t.Fatal("★ an identical configuration must not swap the agent: the fingerprint is what says whether it moved")
	}
}

func TestModelIsResolvedAtBuildTime(t *testing.T) {
	b := newTestBuilder(t)
	calls := 0
	b.models = func(context.Context, *spec.Config, string, CallIntent, *slog.Logger) (
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
	if _, _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery, ""); err != nil {
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
		_ CallIntent, _ *slog.Logger) (model.BaseModel[*schema.Message], error) {

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
	if _, _, err := b.Build(ctx, "s1", "", contract.TurnQuery, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Build(ctx, "s1", "worker", contract.TurnQuery, ""); err != nil {
		t.Fatal(err)
	}

	mainEntry := poolEntry(t, b, spec.DefaultAgentID)
	workerEntry := poolEntry(t, b, "worker")
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

	if _, _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery, ""); err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 {
		t.Fatalf("tool providers ran %d times, want exactly 1 per tree", p.calls)
	}
}

func TestSubAgentWithoutModelFallsBackToTheBase(t *testing.T) {
	cfg := configFromSpecs("root/model", nil, nil, []decl.SubAgentSpec{
		{ID: "worker"},
	})
	b := newTestBuilderWithConfig(t, cfg)
	b.models = func(_ context.Context, _ *spec.Config, ref string,
		_ CallIntent, _ *slog.Logger) (model.BaseModel[*schema.Message], error) {

		if ref == "root/model" {
			return modelA, nil
		}
		return modelB, nil
	}

	bc := countBuilds(t)
	if _, _, err := b.Build(context.Background(), "s1", "", contract.TurnQuery, ""); err != nil {
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
