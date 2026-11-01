package build

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/adk"
	fsapi "github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/helpers"
)

func (b *Builder) baseConfig(snap *snapshot, root decl.SubAgentSpec) deep.Config {
	cfg := deep.Config{
		Name:                   root.ID,
		Description:            root.Description,
		Instruction:            root.Instruction,
		MaxIteration:           root.MaxIter,
		WithoutGeneralSubAgent: true,
	}
	helpers.ApplyModelRetry(&cfg, snap.cfg.MaxRetries())
	return cfg
}

func applyToolConfig(cfg *deep.Config, h heavyDeps) {
	cfg.Backend = h.backend
	cfg.StreamingShell = h.streamingShell
	cfg.ToolsConfig.Tools = append(cfg.ToolsConfig.Tools, h.tools...)
	cfg.ToolsConfig = withUnknownToolsReply(cfg.ToolsConfig)
	cfg.ToolsConfig.EmitInternalEvents = true
}

var buildAgent = func(ctx context.Context, cfg deep.Config) (adk.TypedAgent[*schema.Message], error) {
	return deep.New(ctx, &cfg)
}

type node struct {
	base      deep.Config
	agentSpec *decl.SubAgentSpec
	table     *Table
	depth     int
	root      bool
	backend   fsapi.Backend
	model     model.BaseModel[*schema.Message]
}

func (b *Builder) buildNode(ctx context.Context, snap *snapshot, n node) (
	adk.TypedAgent[*schema.Message], model.BaseModel[*schema.Message], error) {

	cfg := n.base
	sp := n.agentSpec

	if sp != nil {
		cfg.Name = sp.ID
		cfg.Description = sp.Description
		cfg.Instruction = sp.Instruction
		if sp.MaxIter > 0 {
			cfg.MaxIteration = sp.MaxIter
		}
	}

	if n.model != nil {
		cfg.ChatModel = n.model
	} else if !n.root && sp != nil {
		mdl, err := resolveModel(ctx, snap, *sp, cfg.ChatModel)
		if err != nil {
			return nil, nil, err
		}
		cfg.ChatModel = mdl
	}
	if cfg.ChatModel == nil {
		return nil, nil, fmt.Errorf("agent %q has no model", cfg.Name)
	}

	var children []adk.TypedAgent[*schema.Message]
	if sp != nil && n.depth < snap.agentSpec.maxDepth {
		children = make([]adk.TypedAgent[*schema.Message], 0, len(sp.Sub))
		for i := range sp.Sub {
			childSpec := sp.Sub[i]
			child, _, err := b.buildNode(ctx, snap, node{
				base:      n.base,
				agentSpec: &childSpec,
				depth:     n.depth + 1,
			})
			if err != nil {
				return nil, nil, fmt.Errorf("build sub-agent %q: %w", childSpec.ID, err)
			}
			children = append(children, child)
		}
	}
	cfg.SubAgents = children

	if n.root {
		self := ""
		if sp != nil {
			self = sp.ID
		}
		hubs := b.newSkillHubs(snap, n.base, snap.agentSpec.subsOf(self), self)
		handlers, err := b.skillHandlers(ctx, n.backend, hubs, scopeOf(sp))
		if err != nil {
			return nil, nil, err
		}
		boundary := newBoundary(snap.agentSpec.policy, n.table)
		cfg.Handlers = append(handlers, toolChain(boundary, snap.agentSpec.maxDepth)...)
		hubs.agent.bindRoot(cfg)
	} else if sp != nil {
		cfg.Handlers = subHandlers(snap.agentSpec.policy, *sp, snap.agentSpec.maxDepth)
	}

	// The compaction middleware goes on every agent that declares a
	// threshold. The root's compaction is the one that reaches the
	// transcript; a sub-agent's lives and dies inside its own run, where it
	// still protects the agent from filling its own window.
	if sp != nil {
		if mw := b.compactionMiddleware(ctx, snap, sp, cfg.ChatModel); mw != nil {
			cfg.Handlers = append(cfg.Handlers, mw)
		}
	}

	agent, err := buildAgent(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return agent, cfg.ChatModel, nil
}

func (b *Builder) assemble(ctx context.Context, snap *snapshot,
	entry *decl.SubAgentSpec, table *Table) (
	adk.TypedAgent[*schema.Message], model.BaseModel[*schema.Message], error) {

	heavy, err := snap.heavyDepsOf(ctx)
	if err != nil {
		return nil, nil, err
	}
	if entry == nil {
		return nil, nil, errors.New("agent: no entry agent to assemble")
	}
	entrySp := *entry

	cfg := b.baseConfig(snap, entrySp)
	applyToolConfig(&cfg, heavy)

	mdl, err := resolveModel(ctx, snap, entrySp, nil)
	if err != nil {
		return nil, nil, err
	}
	cfg.ChatModel = mdl

	base := cfg
	base.Handlers = nil
	base.SubAgents = nil
	base.Name, base.Description, base.Instruction = "", "", ""

	agent, mdl, err := b.buildNode(ctx, snap, node{
		base:      base,
		agentSpec: &entrySp,
		table:     table,
		depth:     0,
		root:      true,
		backend:   heavy.backend,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build agent %q: %w", entrySp.ID, err)
	}
	return agent, mdl, nil
}

func (b *Builder) skillHandlers(ctx context.Context, backend fsapi.Backend, hubs *skillHubs,
	scope skillScope) ([]adk.TypedChatModelAgentMiddleware[*schema.Message], error) {

	mw, err := skillChain(ctx, backend, hubs, scope)
	if err != nil {
		obs.Logger().Warn("[agent] skill middleware disabled", "err", err)
		return nil, nil
	}
	if mw == nil {
		return nil, nil
	}
	return []adk.TypedChatModelAgentMiddleware[*schema.Message]{mw}, nil
}

// scopeOf reads the skill allow-list of an agent.
//
// ★ A nil spec means no restriction: an agent that says nothing about skills
//
//	gets every skill in the directory, which is what it got before the field
//	existed.
func scopeOf(sp *decl.SubAgentSpec) skillScope {
	if sp == nil {
		return skillScope{all: true}
	}
	return skillScopeOf(sp.Skills)
}
