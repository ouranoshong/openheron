package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// fakeMCPSource is a projection that answers from a fixed list.
type fakeMCPSource struct {
	servers []MCPServerView
	asked   []string
}

func (f *fakeMCPSource) MCPServers(_ context.Context, agentID string) []MCPServerView {
	f.asked = append(f.asked, agentID)
	return f.servers
}

func mcpConfig(t *testing.T) *spec.Config {
	t.Helper()
	cfg := &spec.Config{
		Agents: map[string]*decl.Agent{
			"main": {ID: "main", ModelRef: "test/small"},
			"task": {ID: "task", ModelRef: "test/small"},
		},
		Models: map[string]*decl.Model{"test/small": {ID: "small"}},
		MCPs: decl.MCPFiles{
			"github": {Name: "github", Transport: decl.MCPTransportStdio},
			"files":  {Name: "files", Transport: decl.MCPTransportHTTP},
		},
	}
	return cfg
}

func TestV2MCPServersReportsTheServersOfTheAgent(t *testing.T) {
	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return mcpConfig(t) })

	src := &fakeMCPSource{servers: []MCPServerView{{
		Name:      "github",
		Transport: "stdio",
		Status:    "ready",
		Tools: []MCPToolView{{
			Name:        "read_issue",
			ExposedName: "mcp__github__read_issue",
			Tier:        "ask",
			Status:      "available",
		}},
	}}}
	srv.SetMCPSource(src)

	got := callJSON(t, srv, "GET", "/v2/mcps", nil)
	if got.code != 200 {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	var view struct {
		Agent   string          `json:"agent"`
		Servers []mcpServerView `json:"servers"`
	}
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if view.Agent != "main" {
		t.Fatalf("agent = %q, want main — the parameter is optional", view.Agent)
	}
	if len(view.Servers) != 1 {
		t.Fatalf("servers = %d, want 1 (%s)", len(view.Servers), got.body)
	}
	if len(view.Servers[0].Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(view.Servers[0].Tools))
	}
	tool := view.Servers[0].Tools[0]
	if tool.Name != "read_issue" || tool.ExposedName != "mcp__github__read_issue" {
		t.Fatalf("the row must carry both names, got %+v", tool)
	}
	if tool.Tier != "ask" {
		t.Fatalf("tier = %q, want ask", tool.Tier)
	}
}

func TestV2MCPServersTakesTheAgentParameter(t *testing.T) {
	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return mcpConfig(t) })
	src := &fakeMCPSource{}
	srv.SetMCPSource(src)

	got := callJSON(t, srv, "GET", "/v2/mcps?agent=task", nil)
	if got.code != 200 {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	if len(src.asked) != 1 || src.asked[0] != "task" {
		t.Fatalf("the source must be asked for the named agent, got %v", src.asked)
	}
}

func TestV2MCPServersRefusesAnUnknownAgent(t *testing.T) {
	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return mcpConfig(t) })
	srv.SetMCPSource(&fakeMCPSource{})

	got := callJSON(t, srv, "GET", "/v2/mcps?agent=nope", nil)
	if got.code != 404 {
		t.Fatalf("= %d (%s), want 404", got.code, got.body)
	}
}

func TestV2ConfigListsTheConfiguredServers(t *testing.T) {
	srv := newV2Server(t)
	srv.SetConfigSource(func() *spec.Config { return mcpConfig(t) })

	got := callJSON(t, srv, "GET", "/v2/config", nil)
	if got.code != 200 {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	var view struct {
		MCPServers []mcpServerBrief `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	if len(view.MCPServers) != 2 {
		t.Fatalf("mcpServers = %d, want 2 (%s)", len(view.MCPServers), got.body)
	}
	if view.MCPServers[0].Name != "files" || view.MCPServers[1].Name != "github" {
		t.Fatalf("mcpServers = %+v, want them sorted", view.MCPServers)
	}
}

// ── the command ─────────────────────────────────────────────────────────

func TestTheMCPCommandIsAnAction(t *testing.T) {
	srv := newV2Server(t)
	got := callJSON(t, srv, "GET", "/v2/commands", nil)
	if got.code != 200 {
		t.Fatalf("= %d (%s)", got.code, got.body)
	}
	var view commandList
	if err := json.Unmarshal([]byte(got.body), &view); err != nil {
		t.Fatalf("decode: %v (%s)", err, got.body)
	}
	var found *commandView
	for i := range view.Commands {
		if view.Commands[i].Name == "mcps" {
			found = &view.Commands[i]
		}
	}
	if found == nil {
		t.Fatalf("/mcps is missing from the command table: %s", got.body)
	}
	if found.Kind != commandKindAction {
		t.Fatalf("kind = %q, want action — a prompt would ask the model to describe the servers", found.Kind)
	}
	if found.Action != commandActionOpenMCPs {
		t.Fatalf("action = %q, want %q", found.Action, commandActionOpenMCPs)
	}
}

func TestTheMCPCommandNeverReachesTheModel(t *testing.T) {
	if _, err := expandCommand("/mcps"); err == nil {
		t.Fatal("/mcps must not be sent to the model")
	}
}
