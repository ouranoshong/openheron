package run

import (
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/storage"
)

func TestARunRowNamesTheMCPServersItTouched(t *testing.T) {
	row := newRunRow(nil, storage.Run{RunID: "r1", SessionID: "s1"}, nil)
	row.noteMCPServers([]string{"github", "files"})

	if len(row.run.MCPServers) != 2 {
		t.Fatalf("mcp servers = %v, want two", row.run.MCPServers)
	}
	if row.run.MCPServers[0] != "files" || row.run.MCPServers[1] != "github" {
		t.Fatalf("mcp servers = %v, want them sorted", row.run.MCPServers)
	}

	// The same server twice is one server.
	row.noteMCPServers([]string{"github"})
	if len(row.run.MCPServers) != 2 {
		t.Fatalf("mcp servers = %v, want the server once", row.run.MCPServers)
	}
}

func TestTheServersAreReadOutOfTheNamespacedToolNames(t *testing.T) {
	msgs := []*schema.Message{
		{ToolCalls: []schema.ToolCall{
			{Function: schema.FunctionCall{Name: "mcp__github__read_issue"}},
			{Function: schema.FunctionCall{Name: "execute"}},
		}},
		{ToolCalls: []schema.ToolCall{
			{Function: schema.FunctionCall{Name: "mcp__files__read_file"}},
			{Function: schema.FunctionCall{Name: "mcp__github__create_issue"}},
		}},
	}
	got := mcpServersOf(msgs)
	if len(got) != 2 || got[0] != "files" || got[1] != "github" {
		t.Fatalf("servers = %v, want [files github]", got)
	}
}
