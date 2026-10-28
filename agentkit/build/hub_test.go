package build

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/spec/decl"
)

func TestCollectSpecsFlattensWholeTree(t *testing.T) {
	specs := []decl.SubAgentSpec{
		{ID: "worker", Sub: []decl.SubAgentSpec{{ID: "leaf"}}},
		{ID: "researcher"},
	}
	got := collectSpecs(specs)

	for _, id := range []string{"worker", "leaf", "researcher"} {
		if _, ok := got[id]; !ok {
			t.Fatalf("%q was not collected: %+v", id, got)
		}
	}
	if len(got) != 3 {
		t.Fatalf("unexpected entries: %+v", got)
	}
}

// ★ 缓存必须按 hub 实例（= 配置代际）拆：进程级缓存会让「改了 llm/*.yaml
// 但 skill 还用老模型」重新出现，且没有任何提示。
func TestSkillModelHubCachesWithinAHub(t *testing.T) {
	calls := 0
	h := &skillModelHub{resolve: func(context.Context, string) (model.BaseModel[*schema.Message], error) {
		calls++
		return &stubModel{}, nil
	}}

	for i := 0; i < 3; i++ {
		if _, err := h.Get(context.Background(), "openai/fast"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("the resolver must be called once per hub, got %d", calls)
	}

	// 另一个代际的 hub 必须重新解析。
	other := &skillModelHub{resolve: h.resolve}
	if _, err := other.Get(context.Background(), "openai/fast"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("a new hub must not reuse the previous generation's cache, got %d calls", calls)
	}
}

func TestSkillModelHubReportsEmptyAndMissingResolver(t *testing.T) {
	h := &skillModelHub{resolve: func(context.Context, string) (model.BaseModel[*schema.Message], error) {
		return &stubModel{}, nil
	}}
	if _, err := h.Get(context.Background(), "  "); err == nil {
		t.Fatal("an empty model name must be rejected")
	}

	bare := &skillModelHub{}
	if _, err := bare.Get(context.Background(), "openai/fast"); err == nil {
		t.Fatal("without a resolver the hub must fail loudly, not silently ignore the override")
	}
}

// skill 的 model: 覆盖必须走一遍 name 查表才能知道有没有这个 agent ——
// 报错时要把「可用的名字」带上，否则用户只能去翻 skill 与 agents 两个目录。
func TestSkillAgentHubUnknownNameListsAvailable(t *testing.T) {
	h := &skillAgentHub{
		self:  "main",
		specs: map[string]decl.SubAgentSpec{"worker": {ID: "worker"}},
	}
	_, err := h.Get(context.Background(), "nope", nil)
	if err == nil {
		t.Fatal("an unknown agent must be rejected")
	}
	if !strings.Contains(err.Error(), "worker") {
		t.Fatalf("the error must list available agents, got: %v", err)
	}
}

// 空名字 = 默认 agent：eino 在 skill 没写 agent: 时按这个约定调用。
func TestSkillAgentHubEmptyNameReturnsThePooledAgent(t *testing.T) {
	b := newMinimalBuilder(t)
	// ★ hub 经 Builder 取池（代际号在快照上），不再直接摸池。
	h := &skillAgentHub{builder: b, snap: mustSnapshot(t, b), self: "main"}

	got, err := h.Get(context.Background(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := h.Get(context.Background(), "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != again {
		t.Fatal("the default agent must be the same pooled instance")
	}
}

// fork 到子 agent：必须是新构造的实例（模型可能在每个调用里不同），
// 且带上子 agent 自己的中间件链与下一层拓扑。
func TestSkillAgentHubForkBuildsNamedAgent(t *testing.T) {
	cfg := configFromSpecs("test/model", nil, decl.Policy{}, []decl.SubAgentSpec{
		{ID: "worker", Description: "does the work", Instruction: "work",
			Sub: []decl.SubAgentSpec{{ID: "leaf", Description: "leaf"}}},
	})
	b := newTestBuilderWithConfig(t, cfg)
	snap := mustSnapshot(t, b)
	h := &skillAgentHub{
		builder:  b,
		snap:     snap,
		self:     "main",
		base:     deep.Config{ChatModel: &stubModel{}, WithoutGeneralSubAgent: true},
		policy:   snap.agentSpec.policy,
		maxDepth: snap.agentSpec.maxDepth,
		specs:    collectSpecs(snap.subs()),
	}

	a, err := h.Get(context.Background(), "worker", nil)
	if err != nil {
		t.Fatalf("forking to a declared agent must work: %v", err)
	}
	if a == nil {
		t.Fatal("the fork agent must not be nil")
	}
}

// 同时写了 agent: 与 model: 时，框架解析出的模型必须赢过 spec.ModelRef ——
// 否则 skill 里那行 model: 等于没写，而且不会有任何提示。
func TestSkillAgentHubPrefersSkillModelOverride(t *testing.T) {
	override := &stubModel{}
	cfg := configFromSpecs("test/model", nil, decl.Policy{}, []decl.SubAgentSpec{
		{ID: "worker", Description: "d", ModelRef: "worker/model"},
	})
	b := newTestBuilderWithConfig(t, cfg)
	snap := mustSnapshot(t, b)
	h := &skillAgentHub{
		builder:  b,
		snap:     snap,
		self:     "main",
		base:     deep.Config{ChatModel: &stubModel{}, WithoutGeneralSubAgent: true},
		policy:   snap.agentSpec.policy,
		maxDepth: snap.agentSpec.maxDepth,
		specs:    collectSpecs(snap.subs()),
	}

	a, err := h.Get(context.Background(), "worker",
		&skill.TypedAgentHubOptions[*schema.Message]{Model: override})
	if err != nil {
		t.Fatalf("a model override must not be rejected: %v", err)
	}
	if a == nil {
		t.Fatal("the fork agent must not be nil")
	}
}
