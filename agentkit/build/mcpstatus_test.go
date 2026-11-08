package build

import (
	"context"
	"errors"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// mcpViewConfig is a configuration with two servers and one agent.
//
// ★ The agent names the mounted servers' tools: with the default being
//
//	"none" on both lists, an agent that says nothing gets nothing — and a
//	panel that showed every tool as blocked would say nothing about the
//	server.
func mcpViewConfig(agentServers []string) *spec.Config {
	tools := make([]string, 0, len(agentServers))
	for _, srv := range agentServers {
		tools = append(tools, decl.MCPToolPrefix+srv+decl.MCPSeparator+"*")
	}
	return &spec.Config{
		Agents: map[string]*decl.Agent{
			spec.DefaultAgentID: {
				ID:         spec.DefaultAgentID,
				ModelRef:   "test/model",
				MCPServers: agentServers,
				Tools:      tools,
			},
		},
		Models: map[string]*decl.Model{"test/model": {ID: "model"}},
		MCPs: decl.MCPFiles{
			"github": {Name: "github", Transport: decl.MCPTransportStdio, Command: "true"},
			"files":  {Name: "files", Transport: decl.MCPTransportHTTP, URL: "https://example.invalid/mcp"},
		},
	}
}

func TestEveryConfiguredServerAppearsExactlyOnce(t *testing.T) {
	b := newTestBuilderWithConfig(t, mcpViewConfig([]string{"github", "files"}))
	views := b.MCPServers(context.Background(), spec.DefaultAgentID)
	if len(views) != 2 {
		t.Fatalf("servers = %d, want 2", len(views))
	}
	seen := map[string]bool{}
	for _, v := range views {
		if seen[v.Name] {
			t.Fatalf("%s appears twice", v.Name)
		}
		seen[v.Name] = true
	}
}

func TestAServerTheAgentDoesNotUseIsUnusedAndHasNoToolRows(t *testing.T) {
	list := []string{"github"}
	b := newTestBuilderWithConfig(t, mcpViewConfig(list))
	views := b.MCPServers(context.Background(), spec.DefaultAgentID)

	byName := map[string]MCPServerView{}
	for _, v := range views {
		byName[v.Name] = v
	}
	if byName["files"].Status != MCPServerUnused {
		t.Fatalf("status = %q, want unused — a server the agent does not use is not missing",
			byName["files"].Status)
	}
	if len(byName["files"].Tools) != 0 {
		t.Fatalf("an unused server must have no tool rows, got %d", len(byName["files"].Tools))
	}
}

func TestAnUnreachableServerReportsTheErrorAndKeepsItsLastListing(t *testing.T) {
	b := newTestBuilderWithConfig(t, mcpViewConfig([]string{"github", "files"}))
	b.mcp.start = func(context.Context, context.Context, *decl.MCPServerCfg) (mcpClient, error) {
		return nil, errors.New("the command is not on the PATH")
	}

	views := b.MCPServers(context.Background(), spec.DefaultAgentID)
	for _, v := range views {
		if v.Status != MCPServerUnreachable {
			t.Fatalf("%s status = %q, want unreachable", v.Name, v.Status)
		}
		if v.Error == "" {
			t.Fatalf("%s must carry the failure", v.Name)
		}
	}
}

// ★ The agent's own list is the whole answer for what it may use: a tool
//
//	it does not name is blocked, and the reason says so.
func TestTheAgentsViewReportsTheToolsListAsTheReason(t *testing.T) {
	cfg := mcpViewConfig([]string{"github"})
	// ★ The agent mounts the server but names **no** tool of it: the
	//   server's tools are in the universe (the tier table knows them), and
	//   the agent does not have them.
	cfg.Agents[spec.DefaultAgentID].Tools = []string{"read_file"}
	cfg.MCPs["github"].Allows = []string{"read_issue"}
	b := newTestBuilderWithConfig(t, cfg)

	reason := ""
	for _, v := range b.EffectiveTools(spec.DefaultAgentID) {
		if v.Name == "mcp__github__read_issue" {
			reason = v.Reason
		}
	}
	if reason != "tools" {
		t.Fatalf("reason = %q, want tools — the agent's list is what decides", reason)
	}
}

func TestTheReasonIsTheToolRuleWhenTheServerIsThere(t *testing.T) {
	cfg := mcpViewConfig([]string{"github"})
	cfg.Agents[spec.DefaultAgentID].Tools = []string{"mcp__github__read_issue"}
	b := newTestBuilderWithConfig(t, cfg)

	views := b.MCPServers(context.Background(), spec.DefaultAgentID)
	for _, v := range views {
		if v.Name != "github" || len(v.Tools) == 0 {
			continue
		}
		if v.Tools[0].Reason != "tools" {
			t.Fatalf("reason = %q, want tools", v.Tools[0].Reason)
		}
	}
}

func TestThePanelAsksTheManagerAndTheManagerStartsTheServer(t *testing.T) {
	b := newTestBuilderWithConfig(t, mcpViewConfig([]string{"github", "files"}))
	started := 0
	b.mcp.start = func(context.Context, context.Context, *decl.MCPServerCfg) (mcpClient, error) {
		started++
		return &fakeMCPClient{tools: []string{"read_issue"}}, nil
	}

	views := b.MCPServers(context.Background(), spec.DefaultAgentID)
	if started == 0 {
		t.Fatal("the panel must start the servers it asks about")
	}
	for _, v := range views {
		if v.Name != "github" {
			continue
		}
		if v.Status != MCPServerReady {
			t.Fatalf("status = %q, want ready", v.Status)
		}
		if len(v.Tools) != 1 {
			t.Fatalf("tools = %d, want 1", len(v.Tools))
		}
		tool := v.Tools[0]
		if tool.Name != "read_issue" {
			t.Fatalf("the row must carry the server's own name, got %q", tool.Name)
		}
		if tool.ExposedName != "mcp__github__read_issue" {
			t.Fatalf("the row must carry the exposed name, got %q", tool.ExposedName)
		}
		if tool.Tier != string(TierAsk) {
			t.Fatalf("tier = %q, want ask — the namespace's default", tool.Tier)
		}
		if tool.Status != MCPToolAvailable {
			t.Fatalf("status = %q, want available", tool.Status)
		}
	}
}

func TestThePanelShowsTheTierTheGateWouldUse(t *testing.T) {
	list := []string{"github"}
	cfg := mcpViewConfig(list)
	cfg.MCPs["github"].Allows = []string{"read_issue"}
	b := newTestBuilderWithConfig(t, cfg)
	b.mcp.start = func(context.Context, context.Context, *decl.MCPServerCfg) (mcpClient, error) {
		return &fakeMCPClient{tools: []string{"read_issue", "delete_repo"}}, nil
	}

	for _, v := range b.MCPServers(context.Background(), spec.DefaultAgentID) {
		if v.Name != "github" {
			continue
		}
		tiers := map[string]string{}
		for _, tool := range v.Tools {
			tiers[tool.Name] = tool.Tier
		}
		if tiers["read_issue"] != string(TierAllow) {
			t.Fatalf("read_issue tier = %q, want allow", tiers["read_issue"])
		}
		if tiers["delete_repo"] != string(TierAsk) {
			t.Fatalf("delete_repo tier = %q, want ask", tiers["delete_repo"])
		}
	}
}
