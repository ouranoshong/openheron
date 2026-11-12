package build

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

type fakeManager struct {
	mu  sync.Mutex
	cfg *spec.Config
}

// Current returns the configuration the manager holds.
//
// ★ There is no generation beside it: the configuration **object** is the
//
//	tree's identity, and a counter said "everything changed" where the
//	object says "**this** changed".
func (m *fakeManager) Current() *spec.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

func (m *fakeManager) set(cfg *spec.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
}

// Publish accepts a configuration the builder read itself.
//
// ★ The real manager publishes the same way: the configuration is
//
//	replaced, and the tree's identity moves with it.
func (m *fakeManager) Publish(cfg *spec.Config) { m.set(cfg) }

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
	return &fakeManager{cfg: cfg}
}

func newTestBuilder(t *testing.T) *Builder {
	t.Helper()
	return newTestBuilderWithConfig(t, minimalTestConfig())
}

func newTestBuilderWithConfig(t *testing.T, cfg *spec.Config) *Builder {
	t.Helper()
	b := NewBuilder(testManager(cfg))
	b.models = func(context.Context, *spec.Config, string, CallIntent, *slog.Logger) (
		model.BaseModel[*schema.Message], error) {

		return &stubModel{}, nil
	}
	// ★ The builder owns the MCP clients, and a client owns a child process.
	//   A test that starts a server and never closes the builder leaves that
	//   child alive — on Linux it is an orphan nobody notices, on Windows it
	//   keeps a handle on the test executable, and `go test` then cannot
	//   delete it: "unlinkat ...build.test.exe: Access is denied" turns the
	//   whole suite red over a test that passed.
	t.Cleanup(b.Close)
	return b
}

func mustSnapshot(t *testing.T, b *Builder) *snapshot {
	t.Helper()
	s, err := b.snapshotFor()
	if err != nil {
		t.Fatalf("snapshotFor: %v", err)
	}
	return s
}

// waitForSurfaceChange waits until the MCP tool surface moved, and returns
// the new one.
//
// ★ The surface is part of the tree's identity: a server that comes up
//
//	offers tools the tree does not have yet, and the next tree is a
//	different one. ★ The listing and the surface are two steps by one
//	goroutine, so "ready" does not imply "the surface moved" — a test
//	that waited only for ready could be handed the previous tree, and
//	would read that as a missing rebuild.
func waitForSurfaceChange(t *testing.T, b *Builder, before string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := b.mcp.surfaceHash(b.mgr.Current()); got != before {
			return got
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("the tool surface never moved past %q", before)
	return ""
}

func managerOf(t *testing.T, b *Builder) *fakeManager {
	t.Helper()
	m, ok := b.mgr.(*fakeManager)
	if !ok {
		t.Fatalf("builder is not backed by a fakeManager: %T", b.mgr)
	}
	return m
}

// poolEntry returns the pool's entry for an agent, whatever fingerprint it
// was built at.
//
// ★ The pool holds one entry per (agent, fingerprint), so a test that wants
//
//	"the entry for this agent" has to pick one — and there is only one
//	unless the agent was rebuilt at a new fingerprint.
func poolEntry(t *testing.T, b *Builder, name string) *entry {
	t.Helper()
	b.pool.mu.Lock()
	defer b.pool.mu.Unlock()
	m, ok := b.pool.ents[name]
	if !ok || len(m) == 0 {
		t.Fatalf("no pool entry for %q", name)
	}
	for _, e := range m {
		return e
	}
	return nil
}

// configFromSpecs builds a configuration for the assembly tests.
//
// ★ The **allow list** is the configuration's own: the tier table is
//
//	assembled from the configuration, so a test that wants a tool to run
//	without a nod says so in the configuration.
//
// ★ mainTools is the root agent's **tool allow-list** — the same list the
//
//	configuration's `tools:` key carries. ★ A nil list means **no tools**,
//	which is the default everywhere else too, so a test that wants the
//	whole set must say so.
//
// allToolsExcept is the allow-list a test wants when it used to say "these
// tools are blocked".
//
// ★ The list became an **allow-list**, so "everything but X" has to be
//
//	written out. This helper keeps that intent readable: it is the built-in
//	tool set minus the named ones.
func allToolsExcept(except ...string) []string {
	drop := make(map[string]bool, len(except))
	for _, n := range except {
		drop[n] = true
	}
	out := make([]string, 0, len(decl.BuiltinToolNames))
	for _, n := range decl.BuiltinToolNames {
		if !drop[n] {
			out = append(out, n)
		}
	}
	return out
}

// allTools is every built-in tool: the allow-list of an agent that may use
// them all.
func allTools() []string { return allToolsExcept() }

func configFromSpecs(rootRef string, mainTools []string,
	allow []string, subs []decl.SubAgentSpec) *spec.Config {

	cfg := &spec.Config{
		Agents:    map[string]*decl.Agent{},
		Models:    map[string]*decl.Model{"test/model": {ID: "model", Kind: string(decl.ModelKindChat)}},
		Providers: map[string]*decl.Provider{},
		Server:    decl.Server{Agent: decl.AgentCfg{MaxDepth: specDepth(subs)}},
	}
	if len(allow) > 0 {
		cfg.Tools.HTTP = &decl.HTTPToolsCfg{Allows: allow}
	}
	cfg.Agents[spec.DefaultAgentID] = &decl.Agent{
		ID:            spec.DefaultAgentID,
		ModelRef:      rootRef,
		MaxIterations: 1,
		Tools:         append([]string(nil), mainTools...),
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
			Tools:         append([]string(nil), s.Tools...),
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
		return spec.DefaultMaxDepth
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
