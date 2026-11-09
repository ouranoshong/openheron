// Package agent builds the DeepAgent that agentkit/run drives.
// It imports agentkit/run for nothing: the only contract it satisfies is
// AgentBuilder, whose turn kind comes from the contract leaf package.
package build

import (
	"context"
	"errors"
	"fmt"
	"github.com/ouranoshong/openheron/contract"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

// Builder satisfies agentkit/run.AgentBuilder.
type Builder struct {
	mgr                  ConfigManager
	pool                 *pool
	projectRoot, dataDir string
	models               modelFactory
	tools                *toolRegistry
	// mcp owns the MCP clients. They outlive the snapshot — that is what
	// the provider's batch does not.
	mcp *mcpManager
	// svc is the configuration service: where the tree is, how to read it
	// and how to write it without leaving half a file behind. The config_*
	// tools answer through it rather than growing a second way in.
	svc *spec.Service
	// bump moves the generation when a server's tool list changed.
	bump func()
	mu   sync.Mutex
	snap *snapshot
}

type Option func(*Builder)

// WithConfigService wires the configuration service.
//
// ★ Optional: without it the config_* tools are simply not offered, which is
// what a builder assembled by a test wants. Passing it is what makes an agent
// able to read and edit the configuration it runs from.
func WithConfigService(svc *spec.Service) Option {
	return func(b *Builder) {
		b.svc = svc
	}
}

func WithPaths(projectRoot, dataDir string) Option {
	return func(b *Builder) {
		b.projectRoot = projectRoot
		b.dataDir = dataDir
	}
}

func NewBuilder(mgr ConfigManager, opts ...Option) *Builder {
	mcpMgr := newMCPManager()
	b := &Builder{
		mgr:    mgr,
		pool:   newPool(),
		models: defaultModelFactory,
		mcp:    mcpMgr,
		tools:  defaultToolRegistry(mcpMgr),
	}
	for _, o := range opts {
		o(b)
	}
	return b
}

// SetGenerationBump wires the callback that moves the generation.
//
// ★ A server's tool list can change while the configuration stands still,
//
//	and the tools have to be rebuilt for it. The rebuild is the same road
//	a configuration change takes, so the generation moves through the same
//	hook — a second way to move it would be a second way to be wrong
//	about it.
func (b *Builder) SetGenerationBump(fn func()) {
	b.bump = fn
	// ★ The manager needs the same hook: a server that comes up changes
	//   the tool surface, and the surface is rebuilt once per
	//   generation — so the generation has to move when that happens,
	//   not when a turn happens to ask.
	if b.mcp != nil {
		b.mcp.SetBump(fn)
	}
}

// Warm starts every mounted MCP server in the background.
//
// ★ It is the pre-warm: the servers some agent mounts are started as soon
//
//	as the configuration is known, so the first turn usually finds them
//	up. ★ A server that is not up yet is not a reason to hold the turn —
//	its tools join the next one.
func (b *Builder) Warm(cfg *spec.Config) {
	if b.mcp == nil {
		return
	}
	b.mcp.Warm(cfg)
}

// Close tears the MCP clients down.
func (b *Builder) Close() {
	if b.mcp != nil {
		b.mcp.Close()
	}
}

func (b *Builder) currentSnapshot() *snapshot {
	if b.mgr == nil {
		return nil
	}
	cfg, gen := b.mgr.Current()
	s, err := b.snapshotFor(cfg, gen)
	if err != nil {
		return nil
	}
	return s
}

func (b *Builder) Stats() (hits, misses uint64, size int, gen uint64) {
	return b.pool.Stats()
}

// Build returns the agent for this turn.
func (b *Builder) Build(ctx context.Context, _ string, agentID string,
	_ contract.TurnKind) (adk.TypedAgent[*schema.Message], error) {

	if b.mgr == nil {
		return nil, errors.New("agent: no config manager is wired")
	}
	cfg, gen := b.mgr.Current()

	// ★ The re-list happens before the turn's agent is assembled, not
	//   inside the batch build: the batch is built once per generation,
	//   and the generation moves only when a list changes — so a re-list
	//   inside the build would never run.
	if b.mcp != nil && b.mcp.Refresh(ctx, cfg) && b.bump != nil {
		b.bump()
		cfg, gen = b.mgr.Current()
	}

	snap, err := b.snapshotFor(cfg, gen)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(agentID)
	if name == "" {
		name = snap.agentSpec.defaultAgent
	}
	if _, ok := snap.agentSpec.agentSpec(name); !ok {
		return nil, b.agentUnavailable(name, snap)
	}
	e, err := b.pooledRoot(ctx, snap, name)
	if err != nil {
		return nil, err
	}
	return e.agent, nil
}

// agentUnavailable says why an agent is not there.
//
// ★ Two different reasons, and the difference matters to the reader: an agent
//
//	that was never declared is a typo, and an agent that was **left out**
//	(its model is not declared under llm/) is a configuration that loads and
//	runs without it. The second one is why the tree is short an agent, and
//	saying only "not declared" sends the reader looking in the wrong place.
func (b *Builder) agentUnavailable(name string, snap *snapshot) error {
	if msg := leftOutMessage(snap.cfg, name); msg != "" {
		return errors.New(msg)
	}
	return fmt.Errorf("%w: %q", errAgentNotDeclared, name)
}

// leftOutMessage says that an agent was left out of the tree, and why.
//
// ★ One sentence, in one place: the builder says it when a session asks for
//
//	the agent, and the spec says it when the tree has no default agent.
//	Two copies would drift, and the second one to drift is the one nobody
//	reads.
func leftOutMessage(cfg *spec.Config, name string) string {
	if cfg == nil {
		return ""
	}
	why := cfg.LeftOut()[name]
	if why == "" {
		return ""
	}
	return fmt.Sprintf("agent %q is left out of the tree: %s", name, why)
}

func (b *Builder) pooledRoot(ctx context.Context, snap *snapshot, name string) (*entry, error) {

	sp, ok := snap.agentSpec.agentSpec(name)
	if !ok {
		return nil, b.agentUnavailable(name, snap)
	}
	table := snap.tableFor(name)
	if table == nil {
		return nil, fmt.Errorf("agent: %q has no tool table", name)
	}
	return b.pool.GetOrBuild(ctx, name, snap.gen,
		func(ctx context.Context) (adk.TypedAgent[*schema.Message],
			model.BaseModel[*schema.Message], error) {
			return b.assemble(ctx, snap, &sp, table)
		})
}

var errAgentNotDeclared = errors.New("agent is not declared")
