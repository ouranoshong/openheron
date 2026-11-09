package build

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

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
			// ★ The commands are placeholders that cannot answer, so the
			//   tests here are about the states around a start — and the
			//   deadline is short, because a dead child is only noticed
			//   when the deadline arrives.
			"github": {Name: "github", Transport: decl.MCPTransportStdio, Command: "true",
				Timeout: decl.MCPDuration{Value: 2 * time.Second}},
			"files": {Name: "files", Transport: decl.MCPTransportHTTP, URL: "https://example.invalid/mcp",
				Timeout: decl.MCPDuration{Value: 2 * time.Second}},
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
	// ★ The start is a background job, so the first read says "starting";
	//   the failure is what the panel reports once the job is over.
	waitForMCPSettled(t, b.mcp, "github")
	waitForMCPSettled(t, b.mcp, "files")
	views = b.MCPServers(context.Background(), spec.DefaultAgentID)
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
	// ★ The agent names **no** tool of the server: the server is there
	//   and offers one, and the agent's own list is what keeps it away.
	cfg.Agents[spec.DefaultAgentID].Tools = []string{"read_file"}
	b := newTestBuilderWithConfig(t, cfg)
	// ★ The server has to be up for the question to be about the agent's
	//   list: a server that is not up has no tools to argue about.
	b.mcp.start = func(context.Context, context.Context, *decl.MCPServerCfg) (mcpClient, error) {
		return &fakeMCPClient{tools: []string{"read_issue"}}, nil
	}

	// ★ The panel's ask is what starts the server, so the wait comes
	//   after it — and the second read is the one that has the tools.
	b.MCPServers(context.Background(), spec.DefaultAgentID)
	waitForMCPSettled(t, b.mcp, "github")
	views := b.MCPServers(context.Background(), spec.DefaultAgentID)
	found := false
	for _, v := range views {
		if v.Name != "github" || len(v.Tools) == 0 {
			continue
		}
		found = true
		if v.Tools[0].Reason != "tools" {
			t.Fatalf("reason = %q, want tools", v.Tools[0].Reason)
		}
	}
	if !found {
		t.Fatal("the server has no tool rows — the assertion would pass for nothing")
	}
}

func TestThePanelAsksTheManagerAndTheManagerStartsTheServer(t *testing.T) {
	b := newTestBuilderWithConfig(t, mcpViewConfig([]string{"github", "files"}))
	// ★ The counter is atomic: the starts are background jobs, and two
	//   servers come up at once.
	var started int32
	b.mcp.start = func(context.Context, context.Context, *decl.MCPServerCfg) (mcpClient, error) {
		atomic.AddInt32(&started, 1)
		return &fakeMCPClient{tools: []string{"read_issue"}}, nil
	}

	views := b.MCPServers(context.Background(), spec.DefaultAgentID)
	// ★ The panel starts the servers it asks about, and the start is a
	//   background job: the panel says "starting" first and the tools
	//   are there on the next read.
	waitForMCPSettled(t, b.mcp, "github")
	waitForMCPSettled(t, b.mcp, "files")
	if atomic.LoadInt32(&started) == 0 {
		t.Fatal("the panel must start the servers it asks about")
	}
	views = b.MCPServers(context.Background(), spec.DefaultAgentID)
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

	// ★ The panel's ask starts the server; the tools are there on the
	//   next read.
	b.MCPServers(context.Background(), spec.DefaultAgentID)
	waitForMCPSettled(t, b.mcp, "github")
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
