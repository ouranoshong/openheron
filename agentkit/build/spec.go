package build

import (
	"fmt"
	"strings"

	"openheron/agentkit/spec"
	"openheron/agentkit/spec/decl"
)

type agentSpec struct {
	agents       map[string]decl.SubAgentSpec
	defaultAgent string
	policy       Policy
	maxDepth     int
}

func specOf(cfg *spec.Config, projectRoot string) (agentSpec, error) {
	if cfg == nil {
		return agentSpec{}, fmt.Errorf("agent: nil config")
	}
	agentSpecs, err := cfg.AgentSpecs()
	if err != nil {
		return agentSpec{}, err
	}
	agents := make(map[string]decl.SubAgentSpec, len(agentSpecs))
	for id, sp := range agentSpecs {
		sp.Instruction = instruction(projectRoot, sp.Instruction)
		if sp.MaxIter <= 0 {
			sp.MaxIter = defaultMaxIterations
		}
		agents[id] = sp
	}
	defaultAgent := cfg.DefaultAgent()
	if _, ok := agents[defaultAgent]; !ok {
		return agentSpec{}, fmt.Errorf("agent: the default agent %q is not declared", defaultAgent)
	}
	return agentSpec{
		agents:       agents,
		defaultAgent: defaultAgent,
		policy:       policyOf(cfg),
		maxDepth:     cfg.MaxDepth(),
	}, nil
}

func policyOf(cfg *spec.Config) Policy {
	return NewPolicy(cfg.Policy.Ask, cfg.Policy.Deny)
}

func (s *agentSpec) agentSpec(id string) (decl.SubAgentSpec, bool) {
	sp, ok := s.agents[id]
	return sp, ok
}

func (s *agentSpec) subsOf(id string) []decl.SubAgentSpec {
	sp, ok := s.agents[id]
	if !ok {
		return nil
	}
	return sp.Sub
}

const defaultMaxIterations = 50

func instruction(projectRoot, body string) string {
	rule := fmt.Sprintf(`You are a helpful assistant exploring a Go codebase.

The project root directory is: %s

- Always pass ABSOLUTE paths to filesystem tools (ls, read_file, glob, grep).
- When the user names a relative path, prepend the project root.
- When writing or editing a file and then claiming it compiles, run the build
  first with the execute tool; never assume a build succeeded.
- Anything that mutates the machine (write_file, edit_file, execute) requires
  explicit human approval. Explain what you intend to do before asking.`,
		projectRoot)
	if body = strings.TrimSpace(body); body == "" {
		return rule
	}
	return rule + "\n\n" + body
}

func (s *snapshot) subs() []decl.SubAgentSpec {
	out := make([]decl.SubAgentSpec, 0, len(s.agentSpec.agents))
	for _, sp := range s.agentSpec.agents {
		out = append(out, sp)
	}
	return out
}

func (s *snapshot) tableFor(agentID string) *Table {
	return s.tables[agentID]
}

func tablesOf(sp agentSpec) map[string]*Table {
	out := make(map[string]*Table, len(sp.agents))
	for id, as := range sp.agents {
		out[id] = specTable(sp.policy, as)
	}
	return out
}
