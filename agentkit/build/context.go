package build

import (
	"strings"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"github.com/ouranoshong/openheron/contract"
)

// Contexts reports the context window of every agent's model.
//
// The builder owns the model-ref resolution and the per-agent model
// override, so the API layer asks here instead of re-deriving it: the answer
// has to be the model the agent actually runs on, not the one the
// configuration mentions.
//
// A sub-agent without its own model_ref runs on its parent's model, so it
// inherits the parent's window.
func (b *Builder) Contexts() map[string]contract.AgentContext {
	snap := b.currentSnapshot()
	if snap == nil {
		return nil
	}
	out := map[string]contract.AgentContext{}

	root, ok := snap.agentSpec.agents[snap.agentSpec.defaultAgent]
	if !ok {
		return out
	}
	base := modelContextOf(snap.cfg, root.ModelRef)
	out[root.ID] = base

	var walk func(specs []decl.SubAgentSpec, parent contract.AgentContext)
	walk = func(specs []decl.SubAgentSpec, parent contract.AgentContext) {
		for _, sp := range specs {
			ctx := parent
			if strings.TrimSpace(sp.ModelRef) != "" {
				ctx = modelContextOf(snap.cfg, sp.ModelRef)
			}
			out[sp.ID] = ctx
			walk(sp.Sub, ctx)
		}
	}
	walk(root.Sub, base)

	// An agent that is not reachable from the default agent still runs on
	// some model, and the interface lists every agent.
	for id, sp := range snap.agentSpec.agents {
		if _, ok := out[id]; ok {
			continue
		}
		out[id] = modelContextOf(snap.cfg, sp.ModelRef)
	}
	return out
}

// modelContextOf reads the window and the compaction threshold of a model.
//
// A ref that does not resolve yields an empty context: the window is a fact
// the configuration declares, and there is nothing to report when it
// declares none.
func modelContextOf(cfg *spec.Config, ref string) contract.AgentContext {
	var out contract.AgentContext
	ref = strings.TrimSpace(ref)
	if cfg == nil || ref == "" {
		return out
	}
	_, m, err := Resolve(cfg, ref)
	if err != nil || m == nil {
		return out
	}
	out.Window = m.ContextWindow
	out.SummarizeAt = m.SummarizeAt
	out.Provider = spec.ProviderIDOf(ref)
	out.Model = m.ID
	return out
}
