package build

import (
	"context"

	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// The states one MCP server is in.
//
// ★ The status is about the server, and the tool rows are about the agent:
//
//	"ready" says the client is up, "unused" says this agent does not list
//	the server, and the tool rows say what this agent can reach.
const (
	MCPServerReady       = "ready"
	MCPServerUnreachable = "unreachable"
	MCPServerUnused      = "unused"
)

// The states one MCP tool is in.
const (
	MCPToolAvailable = "available"
	MCPToolSkipped   = "skipped"
	MCPToolBlocked   = "blocked"
)

// MCPToolView is one tool of one server, as the panel sees it.
type MCPToolView struct {
	// Name is the server's own name — the vocabulary of the file.
	Name string
	// ExposedName is the name the model sees.
	ExposedName string
	// Tier is the gate's own verdict for the exposed name.
	Tier string
	// Status says whether this agent reaches the tool.
	Status string
	// Reason says why it does not, as a token.
	Reason string
}

// MCPServerView is one server's row.
type MCPServerView struct {
	Name      string
	Transport string
	Status    string
	Error     string
	Tools     []MCPToolView
}

// MCPServers projects the manager's state for one agent.
//
// ★ The panel derives nothing: every value it shows is one the runtime
//
//	already holds. The status comes from the manager's client, the names
//	from the plan, the tier from the policy's own lookup, and the verdict
//	from the agent's table — so /mcps and the agents view cannot report
//	two different tiers for one tool.
//
// ★ Asking starts the servers this agent uses: the panel cannot answer
//
//	"is my server broken?" without talking to it, and the answer would
//	otherwise be "run a turn and find out".
func (b *Builder) MCPServers(ctx context.Context, agentID string) []MCPServerView {
	snap := b.currentSnapshot()
	if snap == nil || snap.cfg == nil {
		return nil
	}
	cfg := snap.cfg

	sp := snap.agentSpec.defaultAgent
	if agentID != "" {
		if _, ok := snap.agentSpec.agentSpec(agentID); ok {
			sp = agentID
		}
	}
	agent, _ := snap.agentSpec.agentSpec(sp)
	table := tableOfAgent(snap, sp)
	policy := snap.agentSpec.policy

	names := mcpServersOfConfig(cfg)
	out := make([]MCPServerView, 0, len(names))
	for _, name := range names {
		scfg := cfg.MCPs[name]
		if scfg == nil {
			continue
		}
		view := MCPServerView{Name: name, Transport: scfg.Transport}

		// ★ A server the agent does not use is "unused", not missing: a
		//   server that is absent from the list and a server the agent
		//   does not use look the same to the reader, and only one of
		//   them is a problem.
		if !agentUsesMCP(agent.MCPServers, name) {
			view.Status = MCPServerUnused
			out = append(out, view)
			continue
		}

		own, err := b.mcpTools(ctx, scfg)
		if err != nil {
			view.Status = MCPServerUnreachable
			view.Error = err.Error()
			// ★ The tools of an unreachable server are its last listing:
			//   the manager keeps the last successful list, so the panel
			//   can say what the server had when it was last up.
			view.Tools = mcpToolViews(policy, table, mcpPlan(scfg, own))
			out = append(out, view)
			continue
		}
		view.Status = MCPServerReady
		view.Tools = mcpToolViews(policy, table, mcpPlan(scfg, own))
		out = append(out, view)
	}
	return out
}

// mcpTools asks the manager for the server's own tool names.
//
// ★ It goes through the same single-flight start a turn's does, so the
//
//	panel and a turn that arrive at the same moment get one client — and
//	one child process.
func (b *Builder) mcpTools(ctx context.Context, cfg *decl.MCPServerCfg) ([]string, error) {
	if b.mcp == nil {
		return nil, nil
	}
	_, own, err := b.mcp.Tools(ctx, cfg)
	return own, err
}

// mcpToolViews turns one server's plan into the panel's rows.
func mcpToolViews(policy Policy, table *Table, plan mcpServerPlan) []MCPToolView {
	out := make([]MCPToolView, 0, len(plan.Tools))
	for _, t := range plan.Tools {
		view := MCPToolView{
			Name:        t.Own,
			ExposedName: t.Exposed,
			Tier:        string(policy.TierOf(t.Exposed)),
		}
		switch {
		case t.Reason != "":
			// ★ The server offered it and it was not exposed: the name
			//   could not be composed.
			view.Status = MCPToolSkipped
			view.Reason = t.Reason
		case table != nil && table.Blocked(t.Exposed) != LayerNone:
			// ★ The server narrowing is the coarser one, and it is
			//   reported first: a tool blocked by two things at once says
			//   "mcps", because the finer rule would have said nothing
			//   about a server the agent does not have.
			view.Status = MCPToolBlocked
			view.Reason = reasonOf(table.Blocked(t.Exposed))
		default:
			view.Status = MCPToolAvailable
		}
		out = append(out, view)
	}
	return out
}

// agentUsesMCP reports whether the agent lists the server.
//
// ★ An absent list means every configured server and an empty list means
//
//	none — the pointer tells the two apart.
func agentUsesMCP(list *[]string, name string) bool {
	if list == nil {
		return true
	}
	for _, n := range *list {
		if n == name {
			return true
		}
	}
	return false
}
