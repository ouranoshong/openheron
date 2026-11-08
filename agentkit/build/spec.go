package build

import (
	"fmt"
	"strings"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

type agentSpec struct {
	agents       map[string]decl.SubAgentSpec
	defaultAgent string
	policy       Policy
	maxDepth     int
	// mcpServers are the configured servers, sorted. The agent's own
	// mcps: list is read against them.
	mcpServers []string
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
		mcpServers:   mcpServersOfConfig(cfg),
	}, nil
}

// policyOf assembles the tier table.
//
// ★ The exceptions come from two places, and both of them are the
//
//	configuration: a tool's own file names the ones that run without a
//	nod (`allows:`), and an MCP server's file names its own — in the
//	server's own vocabulary, translated to the exposed names here.
func policyOf(cfg *spec.Config) Policy {
	allow := builtinAllowedTools()
	allow = append(allow, cfg.AllowedTools()...)
	for name := range mcpAllowedNames(cfg.MCPs) {
		allow = append(allow, name)
	}

	// ★ The merge happens first, the removal last. A file's `allows:` list is
	//   one of the sources above, so checking the names before the merge
	//   would let the file grant the tool that rewrites the file.
	kept := allow[:0]
	for _, name := range allow {
		if alwaysAsk[name] {
			continue
		}
		kept = append(kept, name)
	}
	return NewPolicy(kept)
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
		out[id] = specTable(sp.policy, as, sp.mcpServers)
	}
	return out
}
