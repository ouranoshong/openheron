package spec

import (
	"fmt"
	"openheron/agentkit/spec/decl"
	"sort"
)

// AgentNames 返回全部 agent 名（排序后）。
func (c *Config) AgentNames() []string {
	out := make([]string, 0, len(c.Agents))
	for id := range c.Agents {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// AgentSpecs 把**每个 agent** 翻成完整规格（它自己 + 它的子树）。
//
// ★ 为什么每个 agent 一棵树，而不是只给「能当会话入口」的那几个：
//
//	会话选哪个 agent，就按那个 agent 自己的配置装配 —— 它的正文、模型、
//	工具边界，以及它声明的那几个下属。一个 agent 可以出现在多棵树里
//	（task 既是 main 的下属，又是自己的入口），那不冲突：它不是属于
//	某一棵树，而是「从某个入口看过去是这样一棵树」。
//
// ★ 规格里的 Instruction 是**原始正文**（agents/<id>.md 的 body）——
//
//	路径规则那段是**代码**的部分，由装配层拼在前面（agent 包的
//	instruction()），配置层不该知道项目根在哪。
//
// ★ **深度超过上限的层级不展开** —— 启动期 [Config.validateDepth] 已经把
//
//	「配了但跑不到」的情形变成硬错误，所以这里只需要照着已通过校验的
//	拓扑翻译，不会出现「声明了却没构造」的静默截断。
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

// agentSpecOf 返回一个 agent 的规格：它自己 + 它下面的树。
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

// specsBelow 返回 id 在 depth 层及以上的下挂规格。
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
		// depth >= maxDepth 时不再往下展开 —— 与装配层的同一判据。
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
