package build

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

var (
	modelA = &stubModel{}
	modelB = &stubModel{}
)

// allowOf is the exception list of a policy — the configuration's own half.
func allowOf(p Policy) []string { return p.Allow }

func verdicts(t *testing.T, in []ToolVerdict) map[string]ToolVerdict {
	t.Helper()
	out := make(map[string]ToolVerdict, len(in))
	for _, v := range in {
		out[v.Name] = v
	}
	return out
}

func introspectBuilder(t *testing.T, p Policy, rootTools []string,
	subs []decl.SubAgentSpec) *Builder {

	t.Helper()
	cfg := configFromSpecs("test/model", rootTools, allowOf(p), subs)
	b := newTestBuilderWithConfig(t, cfg)
	mustSnapshot(t, b)
	return b
}

// ★ The tool table reports the tier the gate would use, and the layer
//
//	that took the tool away from this agent.
func TestEffectiveToolsReportTheTierAndTheBlockingLayer(t *testing.T) {
	p := NewPolicy([]string{"read_file"})
	b := introspectBuilder(t, p, allToolsExcept("execute", "write_file"), nil)

	got := verdicts(t, b.EffectiveTools("main"))

	ex := got["execute"]
	if ex.Tier != TierAsk || ex.Blocked != LayerNotLoaded {
		t.Fatalf("execute = %+v, want ask, blocked by the allow-list", ex)
	}
	rm := got["write_file"]
	if rm.Tier != TierAsk || rm.Blocked != LayerNotLoaded {
		t.Fatalf("write_file = %+v, want ask, blocked by the allow-list", rm)
	}
	if got["read_file"].Tier != TierAllow {
		t.Fatalf("read_file = %+v, want allow", got["read_file"])
	}
}

// ★ A tool the agent's list names but the tier table does not **asks** — the
//
//	default is ask, and the allow-list says nothing about the tier.
func TestVerdictOfFallsBackToAsk(t *testing.T) {
	p := NewPolicy(nil)
	v := verdictOf(newBoundary(p, newTable("main", []string{"brand_new_write_tool"}, nil)),
		"brand_new_write_tool", Observed{})

	if v.Tier != TierAsk || v.Blocked != LayerNone || !v.Reached() {
		t.Fatalf("a named tool = %+v, want ask and reachable", v)
	}
}

func TestEffectiveToolsPerAgentTable(t *testing.T) {
	p := NewPolicy(nil)
	subs := []decl.SubAgentSpec{{
		ID:    "writer",
		Tools: allToolsExcept("write_file"),
	}}
	b := introspectBuilder(t, p, allTools(), subs)

	root := verdicts(t, b.EffectiveTools("main"))
	if v := root["write_file"]; v.Tier != TierAsk || !v.Reached() {
		t.Fatalf("root write_file = %+v, want ask and reachable", v)
	}

	writer := verdicts(t, b.EffectiveTools("writer"))
	if v := writer["write_file"]; v.Tier != TierAsk || v.Blocked != LayerNotLoaded {
		t.Fatalf("writer write_file = %+v, want ask, blocked by the disable layer", v)
	}
	if v := writer["read_file"]; v.Tier != TierAllow {
		t.Fatalf("writer read_file = %+v, want allow (the disable list is per-agent)", v)
	}
}

func TestEffectiveToolsMatchesTheAssembledTable(t *testing.T) {
	p := NewPolicy([]string{"write_file"})
	agentSpec := decl.SubAgentSpec{ID: "worker", Tools: allToolsExcept("write_file", "mystery")}
	b := introspectBuilder(t, p, allToolsExcept("execute"), []decl.SubAgentSpec{agentSpec})

	assembled := specTable(p, agentSpec)
	reported := verdicts(t, b.EffectiveTools("worker"))

	universe := append([]string{}, p.Allow...)
	universe = append(universe, agentSpec.Tools...)
	for _, name := range universe {
		if got, want := reported[name].Blocked, assembled.Blocked(name); got != want {
			t.Fatalf("%s: reported block %q, assembled %q", name, got, want)
		}
	}
	for name, v := range reported {
		if assembled.Blocked(name) == LayerNone && v.Blocked != LayerNone {
			t.Fatalf("%s: reported blocked %q but the assembled table keeps it", name, v.Blocked)
		}
	}
}

func TestPolicyToolsExcludesEveryAgentsDisable(t *testing.T) {
	p := NewPolicy(nil)
	b := introspectBuilder(t, p, []string{"execute"}, []decl.SubAgentSpec{
		{ID: "worker", Tools: allToolsExcept("write_file")},
	})

	policy := verdicts(t, b.PolicyTools())
	if v := policy["write_file"]; v.Tier != TierAsk || !v.Reached() {
		t.Fatalf("policy view of write_file = %+v, want ask and reachable", v)
	}

	if v := verdicts(t, b.EffectiveTools("worker"))["write_file"]; v.Tier != TierAsk ||
		v.Blocked != LayerNotLoaded {
		t.Fatalf("worker view of write_file = %+v, want ask, blocked by the disable layer", v)
	}
}

// ★ The policy view is the tier table on its own: it carries **no
//
//	agent's narrowing**, so every tool it lists is reachable there.
func TestPolicyToolsCarriesNoAgentsNarrowing(t *testing.T) {
	p := NewPolicy(nil)
	// ★ The root names a tool the worker does not: the policy view must
	//   show it as reachable, and the worker's own view must block it.
	b := introspectBuilder(t, p, append(allTools(), "oddly_named"), []decl.SubAgentSpec{
		{ID: "worker", Tools: allToolsExcept("write_file")},
	})

	got := verdicts(t, b.PolicyTools())

	v, ok := got["oddly_named"]
	if !ok {
		t.Fatalf("oddly_named missing from %+v", got)
	}
	if v.Tier != TierAsk || !v.Reached() {
		t.Fatalf("policy view of oddly_named = %+v, want ask and reachable", v)
	}
	if _, ok := got["write_file"]; !ok {
		t.Fatalf("write_file missing from %+v", got)
	}

	// ★ The agent's own view is the one that narrows.
	if v := verdicts(t, b.EffectiveTools("worker"))["write_file"]; v.Tier != TierAsk ||
		v.Blocked != LayerNotLoaded {
		t.Fatalf("worker view = %+v, want ask, blocked by the allow-list", v)
	}
}

func TestEffectiveToolsIsSorted(t *testing.T) {
	p := NewPolicy([]string{"zeta"})
	b := introspectBuilder(t, p, nil, nil)

	first := b.EffectiveTools("main")
	second := b.EffectiveTools("main")
	if len(first) == 0 {
		t.Fatal("the tool list must not be empty")
	}
	for i := 1; i < len(first); i++ {
		if first[i-1].Name > first[i].Name {
			t.Fatalf("not sorted at %d: %s > %s", i, first[i-1].Name, first[i].Name)
		}
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("two calls disagree at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func TestEffectiveToolsUnknownAgentFallsBackToRoot(t *testing.T) {
	p := NewPolicy(nil)
	b := introspectBuilder(t, p, nil, nil)

	got := verdicts(t, b.EffectiveTools("nope"))
	if v := got["read_file"]; v.Tier != TierAllow {
		t.Fatalf("read_file = %+v, want allow", v)
	}
}

func TestEffectiveToolsFindsNestedSubAgents(t *testing.T) {
	p := NewPolicy(nil)
	b := introspectBuilder(t, p, nil, []decl.SubAgentSpec{{
		ID:  "mid",
		Sub: []decl.SubAgentSpec{{ID: "leaf", Tools: allToolsExcept("read_file")}},
	}})

	v := verdicts(t, b.EffectiveTools("leaf"))["read_file"]
	if v.Tier != TierAllow || v.Blocked != LayerNotLoaded {
		t.Fatalf("leaf read_file = %+v, want allow, blocked by the disable layer", v)
	}
}

func TestNewGenerationRebindsPolicyAndTable(t *testing.T) {
	b := introspectBuilder(t, NewPolicy(nil), append(allTools(), "brand_new_tool"), nil)

	if v := verdicts(t, b.EffectiveTools("main"))["brand_new_tool"]; v.Tier != TierAsk {
		t.Fatalf("before: brand_new_tool = %+v, want ask", v)
	}

	next := configFromSpecs("test/model", append(allTools(), "brand_new_tool"),
		allowOf(NewPolicy([]string{"brand_new_tool"})), nil)
	managerOf(t, b).set(next)
	if v := verdicts(t, b.EffectiveTools("main"))["brand_new_tool"]; v.Tier != TierAllow {
		t.Fatalf("after: brand_new_tool = %+v, want allow", v)
	}
}

// TestANewConfigurationRebuildsTheTree says a new configuration is a new
// tree.
//
// ★ The identity is the configuration **object** plus the MCP tool
//
//	surface, and a reload publishes a new object — so the tree follows
//	without anybody announcing that it moved.
func TestANewConfigurationRebuildsTheTree(t *testing.T) {
	b := introspectBuilder(t, NewPolicy(nil), nil, nil)
	before := mustSnapshot(t, b)

	next := minimalTestConfig()
	managerOf(t, b).set(next)
	after := mustSnapshot(t, b)
	if after == before {
		t.Fatal("a new configuration must produce a new tree")
	}
	if after.cfg != next {
		t.Fatalf("the tree = %p, want the configuration the manager holds %p", after.cfg, next)
	}
}

func TestANewConfigurationRebindsTheChatModel(t *testing.T) {
	cfg := minimalTestConfig()
	cfg.Agents[spec.DefaultAgentID].ModelRef = "ref/a"
	b := newTestBuilderWithConfig(t, cfg)
	b.models = func(_ context.Context, _ *spec.Config, ref string,
		_ CallIntent, _ *slog.Logger) (model.BaseModel[*schema.Message], error) {

		switch ref {
		case "ref/a":
			return modelA, nil
		case "ref/b":
			return modelB, nil
		default:
			return &stubModel{}, nil
		}
	}

	first := mustSnapshot(t, b)
	firstModel, err := resolveModel(context.Background(), first, first.agentSpec.agents[spec.DefaultAgentID], nil)
	if err != nil {
		t.Fatal(err)
	}
	if firstModel != modelA {
		t.Fatalf("model = %p, want modelA %p", firstModel, modelA)
	}

	next := minimalTestConfig()
	next.Agents[spec.DefaultAgentID].ModelRef = "ref/b"
	managerOf(t, b).set(next)

	snap := mustSnapshot(t, b)
	secondModel, err := resolveModel(context.Background(), snap, snap.agentSpec.agents[spec.DefaultAgentID], nil)
	if err != nil {
		t.Fatal(err)
	}
	if secondModel != modelB {
		t.Fatalf("after a new configuration model = %p, want modelB %p", secondModel, modelB)
	}
}

// TestTheSnapshotFollowsTheManager is the rule the generation used to
// carry.
//
// ★ The tree never regresses, and it does not need a counter to say so:
//
//	a caller cannot ask for an older tree, because it does not name one —
//	snapshotFor reads the manager under the builder's own lock, so the
//	order of the reads is the order of the builds.
func TestTheSnapshotFollowsTheManager(t *testing.T) {
	b := introspectBuilder(t, NewPolicy(nil), nil, nil)
	mgr := managerOf(t, b)

	first := configFromSpecs("test/model", nil,
		allowOf(NewPolicy(nil)), nil)
	mgr.set(first)
	if got := mustSnapshot(t, b).cfg; got != first {
		t.Fatalf("the tree = %p, want the configuration the manager holds %p", got, first)
	}

	second := configFromSpecs("test/model", nil,
		allowOf(NewPolicy(nil)), nil)
	mgr.set(second)
	if got := mustSnapshot(t, b).cfg; got != second {
		t.Fatalf("the tree = %p, want the newer configuration %p", got, second)
	}
}

// TestSnapshotForReadsTheConfigurationUnderTheLock says the tree is read
// and built as one step.
//
// ★ The configuration is read **inside** the builder's lock, so a caller
//
//	that arrived late with an older configuration cannot put an older
//	tree back — it does not carry one at all.
func TestSnapshotForReadsTheConfigurationUnderTheLock(t *testing.T) {
	b := introspectBuilder(t, NewPolicy(nil), nil, nil)
	mgr := managerOf(t, b)
	if _, err := mustSnapshot(t, b).heavyDepsOf(context.Background()); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			cfg := mgr.Current()
			mgr.set(cfg)
		}()
		go func() {
			defer wg.Done()
			s, err := b.snapshotFor()
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := s.heavyDepsOf(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestConfigurationChangeIsSafeUnderConcurrency(t *testing.T) {
	b := introspectBuilder(t, NewPolicy(nil), nil, nil)
	mgr := managerOf(t, b)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			mgr.set(configFromSpecs("test/model", nil,
				allowOf(NewPolicy([]string{"read_file"})), nil))
		}()
		go func() {
			defer wg.Done()
			_ = b.EffectiveTools("main")
			_ = b.PolicyTools()
		}()
		go func() {
			defer wg.Done()
			s, err := b.snapshotFor()
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := s.heavyDepsOf(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestDescriptionVerdictsFlagEmptyAndDuplicates(t *testing.T) {
	cfg := &spec.Config{
		Agents:    map[string]*decl.Agent{},
		Models:    map[string]*decl.Model{"test/model": {ID: "model", Kind: string(decl.ModelKindChat)}},
		Providers: map[string]*decl.Provider{},
		Server:    decl.Server{Agent: decl.AgentCfg{MaxDepth: 2}},
	}
	cfg.Agents["main"] = &decl.Agent{
		ID: "main", ModelRef: "test/model", MaxIterations: 1,
		Description: "负责拆解需求与验收汇报",
	}
	cfg.Agents["a"] = &decl.Agent{
		ID: "a", ModelRef: "test/model", MaxIterations: 1,
		Description: "后端开发工程师：实现接口与领域逻辑",
	}
	cfg.Agents["b"] = &decl.Agent{
		ID: "b", ModelRef: "test/model", MaxIterations: 1,
		Description: "后端开发工程师：实现接口与领域逻辑",
	}
	cfg.Agents["c"] = &decl.Agent{
		ID: "c", ModelRef: "test/model", MaxIterations: 1,
		Description: "",
	}

	b := newTestBuilderWithConfig(t, cfg)
	mustSnapshot(t, b)

	byID := map[string]DescriptionVerdict{}
	for _, v := range b.DescriptionVerdicts() {
		byID[v.AgentID] = v
	}

	if got := byID["a"]; got.Distinct || len(got.Duplicates) != 1 || got.Duplicates[0] != "b" {
		t.Errorf("a = %+v, want a duplicate of b", got)
	}
	if got := byID["b"]; got.Distinct || len(got.Duplicates) != 1 || got.Duplicates[0] != "a" {
		t.Errorf("b = %+v, want a duplicate of a", got)
	}
	if got := byID["c"]; !got.Empty || got.Distinct {
		t.Errorf("c = %+v, want an empty and non-distinct verdict", got)
	}
	if got := byID["main"]; !got.Distinct || len(got.Duplicates) != 0 {
		t.Errorf("main = %+v, want distinct", got)
	}
}

func TestDescriptionVerdictsDoNotFlagDistinctDescriptions(t *testing.T) {
	cfg := &spec.Config{
		Agents:    map[string]*decl.Agent{},
		Models:    map[string]*decl.Model{"test/model": {ID: "model", Kind: string(decl.ModelKindChat)}},
		Providers: map[string]*decl.Provider{},
		Server:    decl.Server{Agent: decl.AgentCfg{MaxDepth: 2}},
	}
	cfg.Agents["main"] = &decl.Agent{ID: "main", ModelRef: "test/model", MaxIterations: 1,
		Description: "负责拆解需求与验收汇报"}
	cfg.Agents["fe"] = &decl.Agent{ID: "fe", ModelRef: "test/model", MaxIterations: 1,
		Description: "前端开发工程师：实现页面、组件、样式与交互"}
	cfg.Agents["be"] = &decl.Agent{ID: "be", ModelRef: "test/model", MaxIterations: 1,
		Description: "后端开发工程师：实现接口、领域逻辑、数据与配置"}

	b := newTestBuilderWithConfig(t, cfg)
	mustSnapshot(t, b)

	for _, v := range b.DescriptionVerdicts() {
		if !v.Distinct {
			t.Errorf("%s = %+v, want distinct", v.AgentID, v)
		}
	}
}
