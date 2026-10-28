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

// fakeManager 是包内测试用的配置管理器。
//
// ★ 测试不再把模型实例塞进配置：配置里只有 ModelRef，实例由 [modelFactory]
//
//	给桩。这样「装配测试」不依赖厂商端点，也不会把测试桩漏进公开 API。
type fakeManager struct {
	mu  sync.Mutex
	cfg *spec.Config
	gen uint64
}

func (m *fakeManager) Current() (*spec.Config, uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg, m.gen
}

func (m *fakeManager) set(cfg *spec.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
	m.gen++
}

// minimalTestConfig 是一份只够派生出 main 的配置。
func minimalTestConfig() *spec.Config {
	return &spec.Config{
		Agents: map[string]*decl.Agent{
			spec.DefaultAgentID: {
				ID:            spec.DefaultAgentID,
				ModelRef:      "test/model",
				MaxIterations: 1,
			},
		},
		Models: map[string]*decl.Model{
			"test/model": {ID: "model", Kind: string(decl.ModelKindChat)},
		},
	}
}

// testManager 造一个带初代的假管理器。
func testManager(cfg *spec.Config) *fakeManager {
	if cfg == nil {
		cfg = minimalTestConfig()
	}
	return &fakeManager{cfg: cfg, gen: 1}
}

// newTestBuilder 造一个模型被桩替换的 Builder。
func newTestBuilder(t *testing.T) *Builder {
	t.Helper()
	return newTestBuilderWithConfig(t, minimalTestConfig())
}

// newTestBuilderWithConfig 造一个指定配置的 Builder。
func newTestBuilderWithConfig(t *testing.T, cfg *spec.Config) *Builder {
	t.Helper()
	b := NewBuilder(testManager(cfg))
	b.models = func(context.Context, *spec.Config, string, CallIntent) (
		model.BaseModel[*schema.Message], error) {

		return &stubModel{}, nil
	}
	return b
}

// mustSnapshot 让测试拿到当前配置派生出的快照（NewBuilder 不预热）。
func mustSnapshot(t *testing.T, b *Builder) *snapshot {
	t.Helper()
	cfg, gen := b.mgr.Current()
	s, err := b.snapshotFor(cfg, gen)
	if err != nil {
		t.Fatalf("snapshotFor: %v", err)
	}
	return s
}

// managerOf 取测试 Builder 的管理器，用于换代。
func managerOf(t *testing.T, b *Builder) *fakeManager {
	t.Helper()
	m, ok := b.mgr.(*fakeManager)
	if !ok {
		t.Fatalf("builder is not backed by a fakeManager: %T", b.mgr)
	}
	return m
}

// configFromSpecs 把一棵 SubAgentSpec 树翻成真配置。
//
// ★ 测试注入的模型走 ModelRef + modelFactory，不再有「装配好的实例」字段。
//
//	rootRef 给根一个可解析的引用；子 agent 留空时走「回落底板」。
func configFromSpecs(rootRef string, mainDisabled []string,
	policy decl.Policy, subs []decl.SubAgentSpec) *spec.Config {

	cfg := &spec.Config{
		Agents:    map[string]*decl.Agent{},
		Models:    map[string]*decl.Model{"test/model": {ID: "model", Kind: string(decl.ModelKindChat)}},
		Providers: map[string]*decl.Provider{},
		Policy:    policy,
		Server:    decl.Server{Agent: decl.AgentCfg{MaxDepth: specDepth(subs)}},
	}
	cfg.Agents[spec.DefaultAgentID] = &decl.Agent{
		ID:            spec.DefaultAgentID,
		ModelRef:      rootRef,
		MaxIterations: 1,
		DisabledTools: append([]string(nil), mainDisabled...),
		SubAgents:     topLevelIDs(subs),
	}
	addSpecs(cfg.Agents, subs)
	return cfg
}

func addSpecs(dst map[string]*decl.Agent, specs []decl.SubAgentSpec) {
	for _, s := range specs {
		dst[s.ID] = &decl.Agent{
			ID:            s.ID,
			Description:   s.Description,
			Instruction:   s.Instruction,
			ModelRef:      s.ModelRef,
			MaxIterations: s.MaxIter,
			DisabledTools: append([]string(nil), s.DisabledTools...),
			Reasoning:     s.Reasoning,
			Temperature:   s.Temperature,
			MaxTokens:     s.MaxTokens,
			SubAgents:     topLevelIDs(s.Sub),
		}
		addSpecs(dst, s.Sub)
	}
}

// specDepth 返回声明的层数（顶层子 agent = 1）。0 层时用默认 1。
func specDepth(specs []decl.SubAgentSpec) int {
	best := 0
	for _, s := range specs {
		if d := 1 + specDepth(s.Sub); d > best {
			best = d
		}
	}
	if best == 0 {
		return decl.DefaultMaxDepth
	}
	return best
}

func topLevelIDs(specs []decl.SubAgentSpec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.ID)
	}
	return out
}
