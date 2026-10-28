package spec

import (
	"fmt"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
	"sort"
)

func (c *Config) AgentNames() []string {
	out := make([]string, 0, len(c.Agents))
	for id := range c.Agents {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (c *Config) AgentSpecs() (map[string]decl.SubAgentSpec, error) {
	out := make(map[string]decl.SubAgentSpec, len(c.Agents))
	for _, id := range c.AgentNames() {
		spec, err := c.agentSpecOf(id)
		if err != nil {
			return nil, err
		}
		out[id] = spec
	}
	return out, nil
}

func (c *Config) agentSpecOf(id string) (decl.SubAgentSpec, error) {
	a, ok := c.Agents[id]
	if !ok {
		return decl.SubAgentSpec{}, fmt.Errorf("agent %q is not declared", id)
	}
	subs, err := c.specsBelow(id, 1, c.MaxDepth())
	if err != nil {
		return decl.SubAgentSpec{}, err
	}
	return decl.SubAgentSpec{
		ID:            a.ID,
		Description:   a.Description,
		Instruction:   a.Instruction,
		MaxIter:       a.MaxIterations,
		ModelRef:      a.ModelRef,
		DisabledTools: append([]string(nil), a.DisabledTools...),
		Reasoning:     a.Reasoning,
		Temperature:   a.Temperature,
		MaxTokens:     a.MaxTokens,
		Sub:           subs,
	}, nil
}

func (c *Config) specsBelow(id string, depth, maxDepth int) ([]decl.SubAgentSpec, error) {
	if depth > 64 {
		return nil, fmt.Errorf("agent nesting is too deep (>64 levels), likely a config error")
	}
	a, ok := c.Agents[id]
	if !ok {
		return nil, fmt.Errorf("agent %q is not declared", id)
	}

	out := make([]decl.SubAgentSpec, 0, len(a.SubAgents))
	for _, sub := range a.SubAgents {
		sa, ok := c.Agents[sub]
		if !ok {
			return nil, fmt.Errorf("agent %q subagents references unknown agent %q", id, sub)
		}
		spec := decl.SubAgentSpec{
			ID:            sa.ID,
			Description:   sa.Description,
			Instruction:   sa.Instruction,
			MaxIter:       sa.MaxIterations,
			ModelRef:      sa.ModelRef,
			DisabledTools: append([]string(nil), sa.DisabledTools...),
			Reasoning:     sa.Reasoning,
			Temperature:   sa.Temperature,
			MaxTokens:     sa.MaxTokens,
			Sub:           []decl.SubAgentSpec{},
		}
		if depth < maxDepth && len(sa.SubAgents) > 0 {
			nested, err := c.specsBelow(sub, depth+1, maxDepth)
			if err != nil {
				return nil, err
			}
			spec.Sub = append(spec.Sub, nested...)
		}
		out = append(out, spec)
	}
	return out, nil
}
