package build

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

type skillHubs struct {
	agent *skillAgentHub
	model *skillModelHub
}

func (b *Builder) newSkillHubs(snap *snapshot, base deep.Config,
	specs []decl.SubAgentSpec, self string) *skillHubs {

	models := snap.models
	if models == nil {
		models = defaultModelFactory
	}
	return &skillHubs{
		agent: &skillAgentHub{
			builder:  b,
			snap:     snap,
			self:     self,
			base:     base,
			policy:   snap.agentSpec.policy,
			maxDepth: snap.agentSpec.maxDepth,
			specs:    collectSpecs(specs),
		},
		model: &skillModelHub{resolve: func(ctx context.Context, ref string) (
			model.BaseModel[*schema.Message], error) {

			return models(ctx, snap.cfg, ref, CallIntent{}, snap.log)
		}},
	}
}

func collectSpecs(subs []decl.SubAgentSpec) map[string]decl.SubAgentSpec {
	out := make(map[string]decl.SubAgentSpec, len(subs))
	var walk func([]decl.SubAgentSpec)
	walk = func(list []decl.SubAgentSpec) {
		for _, s := range list {
			out[s.ID] = s
			walk(s.Sub)
		}
	}
	walk(subs)
	return out
}

// ── ModelHub ─────────────────────────────────────────────────────────────

type skillModelHub struct {
	resolve func(ctx context.Context, ref string) (model.BaseModel[*schema.Message], error)

	mu    sync.Mutex
	cache map[string]model.BaseModel[*schema.Message]
}

func (h *skillModelHub) Get(ctx context.Context, name string) (model.BaseModel[*schema.Message], error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("skill declares an empty model")
	}
	if h.resolve == nil {
		return nil, fmt.Errorf("no model resolver is wired, cannot resolve %q", name)
	}

	h.mu.Lock()
	if m, ok := h.cache[name]; ok {
		h.mu.Unlock()
		return m, nil
	}
	h.mu.Unlock()

	m, err := h.resolve(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("resolve model %q: %w", name, err)
	}
	if m == nil {
		return nil, fmt.Errorf("resolve model %q: resolver returned nil", name)
	}

	h.mu.Lock()
	if h.cache == nil {
		h.cache = map[string]model.BaseModel[*schema.Message]{}
	}
	h.cache[name] = m
	h.mu.Unlock()
	return m, nil
}

// ── AgentHub ─────────────────────────────────────────────────────────────

type skillAgentHub struct {
	builder  *Builder
	snap     *snapshot
	self     string
	base     deep.Config
	root     deep.Config
	policy   Policy
	maxDepth int
	specs    map[string]decl.SubAgentSpec
}

func (h *skillAgentHub) bindRoot(cfg deep.Config) { h.root = cfg }

func (h *skillAgentHub) Get(ctx context.Context, name string,
	opts *skill.TypedAgentHubOptions[*schema.Message]) (adk.TypedAgent[*schema.Message], error) {

	name = strings.TrimSpace(name)
	var overridden model.BaseModel[*schema.Message]
	if opts != nil {
		overridden = opts.Model
	}

	if name == "" || name == h.self {
		if overridden != nil {
			cfg := h.root
			cfg.ChatModel = overridden
			a, err := buildAgent(ctx, cfg)
			if err != nil {
				return nil, fmt.Errorf("build fork of %q with the skill's model: %w", h.self, err)
			}
			return a, nil
		}
		target := h.self
		if name != "" {
			target = name
		}
		if h.builder == nil || h.snap == nil {
			return nil, fmt.Errorf("no builder is wired for skill agent %q", target)
		}
		e, err := h.builder.pooledRoot(ctx, h.snap, target)
		if err != nil {
			return nil, err
		}
		return e.agent, nil
	}

	agentSpec, ok := h.specs[name]
	if !ok {
		return nil, fmt.Errorf("agent %q is not declared under %q (available: %s)",
			name, h.self, strings.Join(h.agentNames(), ", "))
	}

	if h.builder == nil || h.snap == nil {
		return nil, fmt.Errorf("no builder is wired for skill agent %q", name)
	}
	base := h.base
	base.WithoutGeneralSubAgent = true

	a, _, err := h.builder.buildNode(ctx, h.snap, node{
		base:      base,
		agentSpec: &agentSpec,
		table:     specTable(h.policy, agentSpec),
		depth:     0,
		model:     overridden,
	})
	if err != nil {
		return nil, fmt.Errorf("build skill fork agent %q: %w", name, err)
	}
	return a, nil
}

func (h *skillAgentHub) agentNames() []string {
	out := make([]string, 0, len(h.specs))
	for id := range h.specs {
		out = append(out, id)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}
