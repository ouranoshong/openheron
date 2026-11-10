package api

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/ouranoshong/openheron/agentkit/spec"
)

// MCPSource reports the MCP servers of one agent.
//
// ★ The API layer asks the builder; it does not read the manager. The
//
//	builder owns the projection, and the projection owns the one rule
//	that matters here: the panel derives nothing.
type MCPSource interface {
	MCPServers(ctx context.Context, agentID string) []MCPServerView
}

// MCPServerView is one server's row.
type MCPServerView struct {
	Name      string
	Transport string
	Status    string
	Error     string
	Tools     []MCPToolView
}

// MCPToolView is one tool of one server.
type MCPToolView struct {
	Name        string
	ExposedName string
	Tier        string
	Status      string
	Reason      string
}

// SetMCPSource injects the source of the MCP server view.
func (s *Server) SetMCPSource(src MCPSource) { s.mcpFn = src }

// mcpServerView is the wire shape of one server.
type mcpServerView struct {
	Name      string        `json:"name"`
	Transport string        `json:"transport"`
	Status    string        `json:"status"`
	Error     string        `json:"error"`
	Tools     []mcpToolView `json:"tools"`
}

// mcpToolView is the wire shape of one tool.
type mcpToolView struct {
	Name        string `json:"name"`
	ExposedName string `json:"exposedName"`
	Tier        string `json:"tier"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
}

// mcpList is the wire shape of GET /v2/mcps.
type mcpList struct {
	Agent   string          `json:"agent"`
	Servers []mcpServerView `json:"servers"`
}

// v2MCPServers reports the MCP servers of one agent.
//
// ★ The endpoint is new and not an extension of /v2/agents: the agents
//
//	view is a configuration view, and the MCP status is runtime state —
//	it changes when a child process dies, and it must not be cached for
//	the tab's lifetime the way the agents list is.
func (s *Server) v2MCPServers(ctx context.Context, c *app.RequestContext) {
	cfg := s.requireConfig(c)
	if cfg == nil {
		return
	}

	agent := c.Query("agent")
	if agent == "" {
		agent = cfg.DefaultAgent()
	}
	if _, ok := cfg.Agents[agent]; !ok {
		s.v2Fail(c, consts.StatusNotFound, "agent_not_found",
			"agent "+agent+" is not declared", false)
		return
	}

	out := mcpList{Agent: agent, Servers: []mcpServerView{}}
	if s.mcpFn != nil {
		for _, srv := range s.mcpFn.MCPServers(ctx, agent) {
			view := mcpServerView{
				Name:      srv.Name,
				Transport: srv.Transport,
				Status:    srv.Status,
				Error:     srv.Error,
				Tools:     []mcpToolView{},
			}
			for _, t := range srv.Tools {
				view.Tools = append(view.Tools, mcpToolView{
					Name:        t.Name,
					ExposedName: t.ExposedName,
					Tier:        t.Tier,
					Status:      t.Status,
					Reason:      t.Reason,
				})
			}
			out.Servers = append(out.Servers, view)
		}
	}
	c.JSON(consts.StatusOK, out)
}

// configuredMCP lists the servers the configuration declares.
//
// ★ The configuration says **what exists**, the panel says **what is up**.
//
//	A configuration view that hid the mcps/ partition would leave the
//	settings UI with nothing to show.
func configuredMCP(cfg *spec.Config) []mcpServerBrief {
	out := make([]mcpServerBrief, 0, len(cfg.MCPs))
	for _, name := range sortedKeys(cfg.MCPs) {
		srv := cfg.MCPs[name]
		if srv == nil {
			continue
		}
		out = append(out, mcpServerBrief{Name: name, Transport: srv.Transport})
	}
	return out
}

// mcpServerBrief is one configured server, as the configuration view shows it.
type mcpServerBrief struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
}
