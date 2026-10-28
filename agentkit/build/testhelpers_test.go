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

func testManager(cfg *spec.Config) *fakeManager {
	if cfg == nil {
		cfg = minimalTestConfig()
	}
	return &fakeManager{cfg: cfg, gen: 1}
}

func newTestBuilder(t *testing.T) *Builder {
	t.Helper()
	return newTestBuilderWithConfig(t, minimalTestConfig())
}

func newTestBuilderWithConfig(t *testing.T, cfg *spec.Config) *Builder {
	t.Helper()
	b := NewBuilder(testManager(cfg))
	b.models = func(context.Context, *spec.Config, string, CallIntent) (
		model.BaseModel[*schema.Message], error) {

		return &stubModel{}, nil
	}
	return b
}

func mustSnapshot(t *testing.T, b *Builder) *snapshot {
	t.Helper()
	cfg, gen := b.mgr.Current()
	s, err := b.snapshotFor(cfg, gen)
	if err != nil {
		t.Fatalf("snapshotFor: %v", err)
	}
	return s
}

func managerOf(t *testing.T, b *Builder) *fakeManager {
	t.Helper()
	m, ok := b.mgr.(*fakeManager)
	if !ok {
		t.Fatalf("builder is not backed by a fakeManager: %T", b.mgr)
	}
	return m
}

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
