package build

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// The environment variable that turns this test binary into an MCP server.
//
// ★ The server is the test binary itself: the provider starts a child
//
//	process, and the child has to be something that speaks the protocol.
//	The variable is how the child knows which mode to run in — and it
//	arrives through the very `env:` map the configuration describes.
const mcpServerEnv = "OPENHERON_TEST_MCP_SERVER"

func TestMain(m *testing.M) {
	if os.Getenv(mcpServerEnv) == "1" {
		runFakeMCPServer()
		return
	}
	os.Exit(m.Run())
}

// runFakeMCPServer serves one MCP tool over stdio and exits.
func runFakeMCPServer() {
	s := server.NewMCPServer("openheron-test", "1.0", server.WithToolCapabilities(false))
	s.AddTool(
		mcp.NewTool("read_issue", mcp.WithDescription("Read one issue")),
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("the issue body"), nil
		},
	)
	s.AddTool(
		mcp.NewTool("delete_repo", mcp.WithDescription("Delete the repository")),
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("deleted"), nil
		},
	)
	if err := server.ServeStdio(s); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// liveMCPConfig is a configuration whose one server is this test binary.
func liveMCPConfig(t *testing.T) *spec.Config {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("the test binary's path: %v", err)
	}
	return &spec.Config{
		Agents: map[string]*decl.Agent{
			spec.DefaultAgentID: {ID: spec.DefaultAgentID, ModelRef: "test/model"},
		},
		Models: map[string]*decl.Model{"test/model": {ID: "model"}},
		MCPs: decl.MCPFiles{
			"fake": {
				Name:        "fake",
				Transport:   decl.MCPTransportStdio,
				Command:     self,
				Args:        []string{"-test.run=TestNothingRunsInTheChild"},
				Env:         map[string]string{mcpServerEnv: "1"},
				Allows:      []string{"read_issue"},
				Timeout:     decl.MCPDuration{Value: decl.DefaultMCPTimeout},
				CallTimeout: decl.MCPDuration{Value: decl.DefaultMCPCallTimeout},
			},
		},
	}
}

// TestNothingRunsInTheChild exists so the child's `-test.run` matches
// nothing: the child is the MCP server, not a test run.
func TestNothingRunsInTheChild(t *testing.T) {}

func TestTheProviderMountsTheToolsOfARealServer(t *testing.T) {
	cfg := liveMCPConfig(t)
	b := newTestBuilderWithConfig(t, cfg)

	batch, err := b.tools.Build(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatalf("the provider must not fail on a reachable server: %v", err)
	}

	names := map[string]tool.BaseTool{}
	for _, tl := range batch {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("tool info: %v", err)
		}
		names[info.Name] = tl
	}

	// ★ The namespace: the server's own name under mcp__.
	for _, want := range []string{"mcp__fake__read_issue", "mcp__fake__delete_repo"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("%s is missing from the batch: %v", want, toolNames(batch))
		}
	}

	// The tier comes from the file's own vocabulary: read_issue is allowed,
	// everything else asks.
	if got := b.mustPolicy(t).TierOf("mcp__fake__read_issue"); got != TierAllow {
		t.Fatalf("tier = %q, want allow", got)
	}
	if got := b.mustPolicy(t).TierOf("mcp__fake__delete_repo"); got != TierAsk {
		t.Fatalf("tier = %q, want ask", got)
	}
}

func TestAMCPtoolCallReachesTheServer(t *testing.T) {
	cfg := liveMCPConfig(t)
	b := newTestBuilderWithConfig(t, cfg)

	batch, err := b.tools.Build(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatalf("the provider must not fail on a reachable server: %v", err)
	}
	var target tool.BaseTool
	for _, tl := range batch {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if info.Name == "mcp__fake__read_issue" {
			target = tl
		}
	}
	if target == nil {
		t.Fatal("the tool is missing from the batch")
	}
	inv, ok := target.(tool.InvokableTool)
	if !ok {
		t.Fatal("the tool is not invokable")
	}
	out, err := inv.InvokableRun(context.Background(), "{}")
	if err != nil {
		t.Fatalf("the call must reach the server: %v", err)
	}
	if !strings.Contains(out, "the issue body") {
		t.Fatalf("the result = %q, want the server's answer", out)
	}
}

// mustPolicy returns the assembled policy of the current snapshot.
func (b *Builder) mustPolicy(t *testing.T) Policy {
	t.Helper()
	snap := mustSnapshot(t, b)
	return snap.agentSpec.policy
}

func toolNames(batch []tool.BaseTool) []string {
	out := make([]string, 0, len(batch))
	for _, tl := range batch {
		info, err := tl.Info(context.Background())
		if err != nil || info == nil {
			continue
		}
		out = append(out, info.Name)
	}
	return out
}
