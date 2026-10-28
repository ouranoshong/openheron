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
)

// Builder satisfies agentkit/run.AgentBuilder.
type Builder struct {
	mgr                  ConfigManager
	pool                 *pool
	projectRoot, dataDir string
	models               modelFactory
	tools                *toolRegistry
	mu                   sync.Mutex
	snap                 *snapshot
}

type Option func(*Builder)

func WithPaths(projectRoot, dataDir string) Option {
	return func(b *Builder) {
		b.projectRoot = projectRoot
		b.dataDir = dataDir
	}
}

func NewBuilder(mgr ConfigManager, opts ...Option) *Builder {
	b := &Builder{
		mgr:    mgr,
		pool:   newPool(),
		models: defaultModelFactory,
		tools:  defaultToolRegistry(),
	}
	for _, o := range opts {
		o(b)
	}
	return b
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
	snap, err := b.snapshotFor(cfg, gen)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSpace(agentID)
	if name == "" {
		name = snap.agentSpec.defaultAgent
	}
	if _, ok := snap.agentSpec.agentSpec(name); !ok {
		return nil, fmt.Errorf("%w: %q", errAgentNotDeclared, name)
	}
	e, err := b.pooledRoot(ctx, snap, name)
	if err != nil {
		return nil, err
	}
	return e.agent, nil
}

func (b *Builder) pooledRoot(ctx context.Context, snap *snapshot, name string) (*entry, error) {

	sp, ok := snap.agentSpec.agentSpec(name)
	if !ok {
		return nil, fmt.Errorf("%w: %q", errAgentNotDeclared, name)
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
