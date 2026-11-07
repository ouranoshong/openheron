package build

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// peakTracker counts the calls in flight and remembers the peak.
//
// ★ The counter lives in the process that runs the **server**, so it says
//
//	what the server saw — which is the thing the serialization is about.
type peakTracker struct {
	mu   sync.Mutex
	cur  int
	peak int
}

func (p *peakTracker) enter() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cur++
	if p.cur > p.peak {
		p.peak = p.cur
	}
	return p.peak
}

func (p *peakTracker) leave() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cur--
}

// childPeak is the tracker of the child process.
var childPeak = &peakTracker{}

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
	// ★ The tool that reports the peak number of calls in flight: a
	//   serialized server never sees two at once.
	s.AddTool(
		mcp.NewTool("concurrent", mcp.WithDescription("Report the peak concurrency")),
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			peak := childPeak.enter()
			defer childPeak.leave()
			time.Sleep(300 * time.Millisecond)
			return mcp.NewToolResultText(strconv.Itoa(peak)), nil
		},
	)
	// ★ The tool that never answers in time: the call has to give up.
	s.AddTool(
		mcp.NewTool("slow", mcp.WithDescription("Take longer than the call timeout")),
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			time.Sleep(5 * time.Second)
			return mcp.NewToolResultText("slow done"), nil
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

// ── the calls of one server ─────────────────────────────────────────────

func TestTheStdioCallsOfOneServerAreSerialized(t *testing.T) {
	cfg := liveMCPConfig(t)
	b := newTestBuilderWithConfig(t, cfg)

	batch, err := b.tools.Build(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatalf("the provider must not fail on a reachable server: %v", err)
	}
	target := findTool(t, batch, "mcp__fake__concurrent")

	// ★ Two calls at once. The server reports the peak it saw, and a
	//   serialized transport never lets two of them overlap.
	var wg sync.WaitGroup
	peaks := make([]string, 2)
	for i := range peaks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := target.(tool.InvokableTool).InvokableRun(context.Background(), "{}")
			if err != nil {
				t.Errorf("call %d: %v", i, err)
				return
			}
			peaks[i] = out
		}(i)
	}
	wg.Wait()

	for i, p := range peaks {
		if !strings.Contains(p, `"1"`) {
			t.Fatalf("call %d saw the peak %s — the calls of one stdio server must be serialized", i, p)
		}
	}
}

func TestTheHTTPCallsOfOneServerAreNotSerialized(t *testing.T) {
	tracker := &peakTracker{}
	mcpSrv := server.NewMCPServer("http-test", "1.0", server.WithToolCapabilities(false))
	mcpSrv.AddTool(
		mcp.NewTool("concurrent", mcp.WithDescription("Report the peak concurrency")),
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			peak := tracker.enter()
			defer tracker.leave()
			time.Sleep(300 * time.Millisecond)
			return mcp.NewToolResultText(strconv.Itoa(peak)), nil
		},
	)
	ts := httptest.NewServer(server.NewStreamableHTTPServer(mcpSrv))
	defer ts.Close()

	cfg := &spec.Config{
		Agents: map[string]*decl.Agent{
			spec.DefaultAgentID: {ID: spec.DefaultAgentID, ModelRef: "test/model"},
		},
		Models: map[string]*decl.Model{"test/model": {ID: "model"}},
		MCPs: decl.MCPFiles{
			"files": {
				Name:        "files",
				Transport:   decl.MCPTransportHTTP,
				URL:         ts.URL + "/mcp",
				Timeout:     decl.MCPDuration{Value: decl.DefaultMCPTimeout},
				CallTimeout: decl.MCPDuration{Value: decl.DefaultMCPCallTimeout},
			},
		},
	}
	b := newTestBuilderWithConfig(t, cfg)

	batch, err := b.tools.Build(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatalf("the provider must not fail on a reachable server: %v", err)
	}
	target := findTool(t, batch, "mcp__files__concurrent")

	var wg sync.WaitGroup
	peaks := make([]string, 2)
	for i := range peaks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, err := target.(tool.InvokableTool).InvokableRun(context.Background(), "{}")
			if err != nil {
				t.Errorf("call %d: %v", i, err)
				return
			}
			peaks[i] = out
		}(i)
	}
	wg.Wait()

	// ★ The serialization is a property of the transport, not of MCP: an
	//   http server stands on its own per request.
	sawTwo := false
	for _, p := range peaks {
		if strings.Contains(p, `"2"`) {
			sawTwo = true
		}
	}
	if !sawTwo {
		t.Fatalf("the calls must not be serialized over http, the peaks were %v", peaks)
	}
}

func TestACallThatExceedsTheTimeoutGivesUpAndReleasesTheLock(t *testing.T) {
	cfg := liveMCPConfig(t)
	cfg.MCPs["fake"].CallTimeout = decl.MCPDuration{Value: 300 * time.Millisecond}
	b := newTestBuilderWithConfig(t, cfg)

	batch, err := b.tools.Build(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatalf("the provider must not fail on a reachable server: %v", err)
	}
	slow := findTool(t, batch, "mcp__fake__slow")

	start := time.Now()
	if _, err := slow.(tool.InvokableTool).InvokableRun(context.Background(), "{}"); err == nil {
		t.Fatal("the call must give up when the server does not answer in time")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("the call took %s, want it to give up at the timeout", elapsed)
	}

	// ★ The lock is released: the next call runs.
	quick := findTool(t, batch, "mcp__fake__read_issue")
	out, err := quick.(tool.InvokableTool).InvokableRun(context.Background(), "{}")
	if err != nil {
		t.Fatalf("the next call must run: %v", err)
	}
	if !strings.Contains(out, "the issue body") {
		t.Fatalf("the result = %q, want the server's answer", out)
	}
}

// ── the notices ─────────────────────────────────────────────────────────

func TestTheFileAskingForAToolTheServerLacksIsNoticed(t *testing.T) {
	cfg := liveMCPConfig(t)
	cfg.MCPs["fake"].Tools = []string{"read_issue", "no_such_tool"}

	var buf strings.Builder
	old := obs.Logger()
	obs.SetLogger(slog.New(obs.NewHandler(&buf, slog.LevelDebug)))
	defer obs.SetLogger(old)

	b := newTestBuilderWithConfig(t, cfg)
	if _, err := b.tools.Build(context.Background(), toolEnv{cfg: cfg}); err != nil {
		t.Fatalf("the provider must not fail on a reachable server: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "no_such_tool") {
		t.Fatalf("the notice must name the tool the server does not have:\n%s", out)
	}
	if !strings.Contains(out, "fake") {
		t.Fatalf("the notice must name the server:\n%s", out)
	}
	// ★ The rest is still offered.
	if !strings.Contains(out, "read_issue") {
		t.Fatalf("the notice must name what the server offers:\n%s", out)
	}
}

// findTool returns the tool with the given exposed name.
func findTool(t *testing.T, batch []tool.BaseTool, name string) tool.BaseTool {
	t.Helper()
	for _, tl := range batch {
		info, err := tl.Info(context.Background())
		if err != nil || info == nil {
			continue
		}
		if info.Name == name {
			return tl
		}
	}
	t.Fatalf("%s is missing from the batch: %v", name, toolNames(batch))
	return nil
}

// TestTheServerFileOnDiskIsWhatTheLoaderProduces loads a real
// `mcps/<server>.yaml` through the real loader.
//
// ★ The other end-to-end test builds its configuration in Go, because the
//
//	command has to name the test binary's own path and the environment has
//	to carry the flag that turns the child into a server — neither exists
//	before the test runs. This one writes the file and lets the loader
//	read it, so the file's name, its fields and the loader's validation
//	are all part of what is proved.
func TestTheServerFileOnDiskIsWhatTheLoaderProduces(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("the test binary's path: %v", err)
	}

	root := t.TempDir()
	base := filepath.Join(root, ".openheron")
	writeFile(t, filepath.Join(base, "llm", "test.yaml"),
		"type: openai\nbase_url: https://example.invalid\nmodels:\n  - id: small\n")
	writeFile(t, filepath.Join(base, "agents", "main.md"),
		"---\nmodel: test/small\n---\nbody\n")
	writeFile(t, filepath.Join(base, "mcps", "demo.yaml"), fmt.Sprintf(
		"transport: stdio\n"+
			"command: %s\n"+
			"args: [\"-test.run=TestNothingRunsInTheChild\"]\n"+
			"env:\n  %s: \"1\"\n"+
			"allows: [read_issue]\n"+
			"timeout: 30s\n"+
			"call_timeout: 60s\n",
		self, mcpServerEnv))

	svc, err := spec.NewService(base, root)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	cfg, err := svc.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	srv, ok := cfg.MCPs["demo"]
	if !ok {
		t.Fatalf("the file's name is the server's name: %v", cfg.MCPs)
	}
	if srv.Transport != decl.MCPTransportStdio {
		t.Fatalf("transport = %q, want stdio", srv.Transport)
	}
	if srv.Timeout.Value != 30*time.Second {
		t.Fatalf("timeout = %s, want 30s", srv.Timeout.Value)
	}
	if srv.CallTimeout.Value != 60*time.Second {
		t.Fatalf("call_timeout = %s, want 60s", srv.CallTimeout.Value)
	}
	if len(srv.Allows) != 1 || srv.Allows[0] != "read_issue" {
		t.Fatalf("allows = %v, want the server's own name", srv.Allows)
	}

	// ★ The file's tier reaches the gate under the composed name.
	b := newTestBuilderWithConfig(t, cfg)
	if got := b.mustPolicy(t).TierOf("mcp__demo__read_issue"); got != TierAllow {
		t.Fatalf("tier = %q, want allow", got)
	}
	if got := b.mustPolicy(t).TierOf("mcp__demo__delete_repo"); got != TierAsk {
		t.Fatalf("tier = %q, want ask", got)
	}

	batch, err := b.tools.Build(context.Background(), toolEnv{cfg: cfg})
	if err != nil {
		t.Fatalf("the provider must not fail on a reachable server: %v", err)
	}
	findTool(t, batch, "mcp__demo__read_issue")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
