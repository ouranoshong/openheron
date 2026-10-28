package build

import (
	"context"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/spec"
	"openheron/agentkit/spec/decl"
)

// modelA / modelB 是两个可区分的模型实例 —— 用来证明换代真的换了对象。
//
// ★ 用**指针身份**而不是比较字段：两份字段相等的配置看起来一样，
//
//	而「运行中用的是哪个」正是这里要验的东西。
var (
	modelA = &stubModel{}
	modelB = &stubModel{}
)

// toConfigPolicy 把 agent.Policy 搬成 decl.Policy。
func toConfigPolicy(p Policy) decl.Policy {
	return decl.Policy{Ask: p.Ask, Deny: p.Deny}
}

// verdicts 把结论压成 name → verdict，方便按名字断言。
func verdicts(t *testing.T, in []ToolVerdict) map[string]ToolVerdict {
	t.Helper()
	out := make(map[string]ToolVerdict, len(in))
	for _, v := range in {
		out[v.Name] = v
	}
	return out
}

// introspectBuilder 造一个只带审批三档与拓扑的 Builder。
func introspectBuilder(t *testing.T, p Policy, rootDisabled []string,
	subs []decl.SubAgentSpec) *Builder {

	t.Helper()
	cfg := configFromSpecs("test/model", rootDisabled, toConfigPolicy(p), subs)
	b := newTestBuilderWithConfig(t, cfg)
	mustSnapshot(t, b)
	return b
}

func TestEffectiveToolsDenyBeatsDisable(t *testing.T) {
	// deny 与 disabled_tools 都列了 execute —— 外层必须赢，那是子 agent
	// 不能放松的唯一保证。
	p := NewPolicy(
		[]string{"write_file", "execute"},
		[]string{"execute"},
	)
	b := introspectBuilder(t, p, []string{"execute", "rm_rf"}, nil)

	got := verdicts(t, b.EffectiveTools("main"))

	ex := got["execute"]
	if ex.Tier != TierDeny || ex.Blocked != LayerDeny {
		t.Fatalf("execute = %+v, want deny blocked by the deny layer", ex)
	}
	// 只被本 agent 禁用的：生效档位是 deny，但层是 disable —— 用户要改的是
	// agents/*.md 里的 disabled_tools，不是 policy。
	rm := got["rm_rf"]
	if rm.Tier != TierDeny || rm.Blocked != LayerDisable {
		t.Fatalf("rm_rf = %+v, want deny blocked by the disable layer", rm)
	}
	if got["read_file"].Tier != TierAllow {
		t.Fatalf("read_file = %+v, want allow", got["read_file"])
	}
}

// TestVerdictOfFallsBackToAllow 钉的是「判定」这条规则本身。
func TestVerdictOfFallsBackToAllow(t *testing.T) {
	p := NewPolicy([]string{"execute"}, nil)
	v := verdictOf(newBoundary(p, newTable("main", nil, nil)), "brand_new_write_tool", Observed{})

	if v.Tier != TierAllow || v.Blocked != LayerNone || !v.Reached() {
		t.Fatalf("unlisted tool = %+v, want allow and reachable", v)
	}
}

func TestEffectiveToolsPerAgentTable(t *testing.T) {
	// ★ 同一个工具在不同 agent 下的结论可以不同 —— 那正是分层的意义。
	p := NewPolicy([]string{"write_file"}, nil)
	subs := []decl.SubAgentSpec{{
		ID:            "writer",
		DisabledTools: []string{"write_file"},
	}}
	b := introspectBuilder(t, p, nil, subs)

	root := verdicts(t, b.EffectiveTools("main"))
	if v := root["write_file"]; v.Tier != TierAsk || !v.Reached() {
		t.Fatalf("root write_file = %+v, want ask and reachable", v)
	}

	writer := verdicts(t, b.EffectiveTools("writer"))
	if v := writer["write_file"]; v.Tier != TierDeny || v.Blocked != LayerDisable {
		t.Fatalf("writer write_file = %+v, want deny via disable", v)
	}
	if v := writer["read_file"]; v.Tier != TierAllow {
		t.Fatalf("writer read_file = %+v, want allow (the disable list is per-agent)", v)
	}
}

// TestEffectiveToolsMatchesTheAssembledTable 是这个文件存在的理由。
func TestEffectiveToolsMatchesTheAssembledTable(t *testing.T) {
	p := NewPolicy([]string{"write_file"}, []string{"rm_rf"})
	agentSpec := decl.SubAgentSpec{ID: "worker", DisabledTools: []string{"write_file", "mystery"}}
	b := introspectBuilder(t, p, []string{"execute"}, []decl.SubAgentSpec{agentSpec})

	assembled := specTable(p, agentSpec) // ← subHandlers 用的就是它
	reported := verdicts(t, b.EffectiveTools("worker"))

	universe := append(append([]string{}, p.Ask...), p.Deny...)
	universe = append(universe, agentSpec.DisabledTools...)
	for _, name := range universe {
		if got, want := reported[name].Blocked, assembled.Blocked(name); got != want {
			t.Fatalf("%s: reported block %q, assembled %q", name, got, want)
		}
	}
	// 反向也要成立：表里留着它，报告里就不能说它被摘了。
	for name, v := range reported {
		if assembled.Blocked(name) == LayerNone && v.Blocked != LayerNone {
			t.Fatalf("%s: reported blocked %q but the assembled table keeps it", name, v.Blocked)
		}
	}
}

func TestPolicyToolsExcludesEveryAgentsDisable(t *testing.T) {
	// ★ /v2/config 报的是**策略**，不是某个 agent 的视图。
	p := NewPolicy([]string{"write_file"}, nil)
	b := introspectBuilder(t, p, []string{"execute"}, []decl.SubAgentSpec{
		{ID: "worker", DisabledTools: []string{"write_file"}},
	})

	policy := verdicts(t, b.PolicyTools())
	if v := policy["write_file"]; v.Tier != TierAsk || !v.Reached() {
		t.Fatalf("policy view of write_file = %+v, want ask and reachable", v)
	}

	// 而 agent 视图里它是 deny。
	if v := verdicts(t, b.EffectiveTools("worker"))["write_file"]; v.Tier != TierDeny {
		t.Fatalf("worker view of write_file = %+v, want deny", v)
	}
}

func TestPolicyToolsListsDisabledOnlyNames(t *testing.T) {
	// 配置里提到、却没列进策略的名字必须出现在报告里。
	p := NewPolicy(nil, nil)
	b := introspectBuilder(t, p, []string{"oddly_named"}, []decl.SubAgentSpec{
		{ID: "worker", DisabledTools: []string{"declared_downstream"}},
	})

	got := verdicts(t, b.PolicyTools())

	v, ok := got["oddly_named"]
	if !ok {
		t.Fatalf("oddly_named missing from %+v", got)
	}
	if v.Tier != TierAllow || !v.Reached() {
		t.Fatalf("policy view of oddly_named = %+v, want allow and reachable", v)
	}
	if _, ok := got["declared_downstream"]; !ok {
		t.Fatalf("declared_downstream missing from %+v", got)
	}

	if v := verdicts(t, b.EffectiveTools("worker"))["declared_downstream"]; v.Tier != TierDeny {
		t.Fatalf("worker view = %+v, want deny", v)
	}
}

func TestEffectiveToolsIsSorted(t *testing.T) {
	// 汇报的东西要有稳定顺序，否则同一份配置的两次响应看不出是同一份。
	p := NewPolicy([]string{"zeta"}, []string{"beta"})
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
	// 少报一项比报错更难查；调用方（自省端点）本来就会先校验名字存在。
	p := NewPolicy(nil, nil)
	b := introspectBuilder(t, p, nil, nil)

	got := verdicts(t, b.EffectiveTools("nope"))
	if v := got["read_file"]; v.Tier != TierAllow {
		t.Fatalf("read_file = %+v, want allow", v)
	}
}

func TestEffectiveToolsFindsNestedSubAgents(t *testing.T) {
	p := NewPolicy(nil, nil)
	b := introspectBuilder(t, p, nil, []decl.SubAgentSpec{{
		ID:  "mid",
		Sub: []decl.SubAgentSpec{{ID: "leaf", DisabledTools: []string{"read_file"}}},
	}})

	v := verdicts(t, b.EffectiveTools("leaf"))["read_file"]
	if v.Tier != TierDeny || v.Blocked != LayerDisable {
		t.Fatalf("leaf read_file = %+v, want deny via disable", v)
	}
}

// ── 代际 ─────────────────────────────────────────────────────────────────

func TestNewGenerationRebindsPolicyAndTable(t *testing.T) {
	b := introspectBuilder(t, NewPolicy(nil, nil), nil, nil)

	if v := verdicts(t, b.EffectiveTools("main"))["read_file"]; v.Tier != TierAllow {
		t.Fatalf("before: read_file = %+v, want allow", v)
	}

	// 改了 policy：read_file 降级成 ask。
	next := configFromSpecs("test/model", nil,
		toConfigPolicy(NewPolicy([]string{"read_file"}, nil)), nil)
	managerOf(t, b).set(next)
	// ★ 刻意**不**调 mustSnapshot：自省读口要在下一次 Build 之前就跟上
	//   管理器的新代。靠 Builder 的 b.snap 缓存会让接口慢一拍 —— 用户看到
	//   旧 policy，而下一轮其实已经用新配置。
	if v := verdicts(t, b.EffectiveTools("main"))["read_file"]; v.Tier != TierAsk {
		t.Fatalf("after: read_file = %+v, want ask", v)
	}
}

func TestNewGenerationBumpsTheGeneration(t *testing.T) {
	// ★ 代际必须真的 +1 —— 换绑之后，下一次 Build 才不会复用旧实例。
	b := introspectBuilder(t, NewPolicy(nil, nil), nil, nil)
	before := mustSnapshot(t, b).gen

	managerOf(t, b).set(minimalTestConfig())
	after := mustSnapshot(t, b).gen
	if after != before+1 {
		t.Fatalf("generation = %d, want %d", after, before+1)
	}
}

// TestNewGenerationRebindsTheChatModel 是「模型按引用在 Build 期解析」的落地。
func TestNewGenerationRebindsTheChatModel(t *testing.T) {
	cfg := minimalTestConfig()
	cfg.Agents[spec.DefaultAgentID].ModelRef = "ref/a"
	b := newTestBuilderWithConfig(t, cfg)
	b.models = func(_ context.Context, _ *spec.Config, ref string,
		_ CallIntent) (model.BaseModel[*schema.Message], error) {

		switch ref {
		case "ref/a":
			return modelA, nil
		case "ref/b":
			return modelB, nil
		default:
			return &stubModel{}, nil
		}
	}

	// ★ 模型是**装配期**按引用解析的（入口 agent 自己的 ModelRef）——
	//   所以这里问的是「这一代的快照解析出来的模型是谁」。
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
		t.Fatalf("after a generation bump model = %p, want modelB %p", secondModel, modelB)
	}
}

// TestSnapshotNeverRegresses 钉住 §4.8 的单调性。
func TestSnapshotNeverRegresses(t *testing.T) {
	b := introspectBuilder(t, NewPolicy(nil, nil), nil, nil)
	newer := configFromSpecs("test/model", nil,
		toConfigPolicy(NewPolicy(nil, nil)), nil)

	if _, err := b.snapshotFor(newer, 7); err != nil {
		t.Fatal(err)
	}
	// 一个先读到旧代际的 Build 晚到：不得把快照覆盖回旧代。
	old := configFromSpecs("test/model", nil,
		toConfigPolicy(NewPolicy(nil, nil)), nil)
	got, err := b.snapshotFor(old, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got.gen != 7 {
		t.Fatalf("snapshot regressed to gen %d, want 7", got.gen)
	}
	// ★ 断言的是**代际**：晚到的旧代际不能覆盖新代际 ✅ ✅。
	if got.gen != 7 {
		t.Fatalf("the newer snapshot was overwritten: gen %d, want 7", got.gen)
	}
}

// TestSnapshotForReadsTheGenerationUnderTheLock 钉住代际竞态。
func TestSnapshotForReadsTheGenerationUnderTheLock(t *testing.T) {
	b := introspectBuilder(t, NewPolicy(nil, nil), nil, nil)
	mgr := managerOf(t, b)
	if _, err := mustSnapshot(t, b).heavyDepsOf(context.Background()); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			cfg, _ := mgr.Current()
			mgr.set(cfg) // 只换代际，不换内容
		}()
		go func() {
			defer wg.Done()
			cfg, gen := mgr.Current()
			s, err := b.snapshotFor(cfg, gen)
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

// TestGenerationChangeIsSafeUnderConcurrency 覆盖换代与自省/装配并发的那条路径。
func TestGenerationChangeIsSafeUnderConcurrency(t *testing.T) {
	b := introspectBuilder(t, NewPolicy(nil, nil), nil, nil)
	mgr := managerOf(t, b)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			mgr.set(configFromSpecs("test/model", nil,
				toConfigPolicy(NewPolicy([]string{"read_file"}, nil)), nil))
		}()
		go func() {
			defer wg.Done()
			_ = b.EffectiveTools("main")
			_ = b.PolicyTools()
		}()
		go func() {
			defer wg.Done()
			cfg, gen := mgr.Current()
			s, err := b.snapshotFor(cfg, gen)
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

// ★ 模型选专家**只依据描述** —— 描述空了或与别的雷同，模型就分不出该叫谁，
//
//	而症状（「模型老是不叫那个专家」）从外面看不出原因。
//	这里给它一个能自省出来的结论。
func TestDescriptionVerdictsFlagEmptyAndDuplicates(t *testing.T) {
	cfg := &spec.Config{
		Agents:    map[string]*decl.Agent{},
		Models:    map[string]*decl.Model{"test/model": {ID: "model", Kind: string(decl.ModelKindChat)}},
		Providers: map[string]*decl.Provider{},
		Policy:    decl.Policy{},
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

// 描述不同就该是可区分的 —— 判据不能把「都不同」也报成重复。
func TestDescriptionVerdictsDoNotFlagDistinctDescriptions(t *testing.T) {
	cfg := &spec.Config{
		Agents:    map[string]*decl.Agent{},
		Models:    map[string]*decl.Model{"test/model": {ID: "model", Kind: string(decl.ModelKindChat)}},
		Providers: map[string]*decl.Provider{},
		Policy:    decl.Policy{},
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
