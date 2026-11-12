package build

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ouranoshong/openheron/agentkit/obs"
	"github.com/ouranoshong/openheron/agentkit/spec"
	"github.com/ouranoshong/openheron/agentkit/spec/decl"
)

// asyncMCPCfg is a configuration with one mounted server and one agent.
//
// ★ The agent names the server's tools: with the default being "none" on
// both lists, an agent that says nothing gets nothing.
func asyncMCPCfg() *spec.Config {
	return &spec.Config{
		Agents: map[string]*decl.Agent{
			spec.DefaultAgentID: {
				ID:         spec.DefaultAgentID,
				ModelRef:   "test/model",
				MCPServers: []string{"github"},
				Tools:      []string{"mcp__github__*"},
			},
		},
		Models: map[string]*decl.Model{"test/model": {ID: "model", Kind: string(decl.ModelKindChat)}},
		MCPs: decl.MCPFiles{
			"github": {
				Name:      "github",
				Transport: decl.MCPTransportStdio,
				Command:   "true",
				Timeout:   decl.MCPDuration{Value: 500 * time.Millisecond},
			},
		},
	}
}

// blockedStart is a start that never finishes until it is released.
//
// ★ It is what a server that is still installing its own package looks
// like from the outside: the child is alive, and it says nothing.
type blockedStart struct {
	release chan struct{}
	starts  int32
}

// Starts is how many times the start was tried.
func (s *blockedStart) Starts() int32 { return atomic.LoadInt32(&s.starts) }

func (s *blockedStart) start(opCtx, lifeCtx context.Context, cfg *decl.MCPServerCfg,
	_ *slog.Logger) (mcpClient, error) {
	atomic.AddInt32(&s.starts, 1)
	<-s.release
	return &fakeMCPClient{tools: []string{"read_issue"}}, nil
}

// TestAServerThatIsStillStartingDoesNotHoldTheBuild is the promise the whole
// design makes: a server on its way does not decide whether a turn can start.
func TestAServerThatIsStillStartingDoesNotHoldTheBuild(t *testing.T) {
	cfg := asyncMCPCfg()
	b := newTestBuilderWithConfig(t, cfg)

	blocked := &blockedStart{release: make(chan struct{})}
	defer close(blocked.release)
	b.mcp.start = blocked.start

	begin := time.Now()
	batch, err := b.tools.Build(context.Background(), toolEnv{cfg: cfg})
	elapsed := time.Since(begin)
	if err != nil {
		t.Fatalf("the provider must not fail on a server that is starting: %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the build took %s: a server on its way must not hold the build", elapsed)
	}
	for _, name := range toolNames(batch) {
		if strings.HasPrefix(name, "mcp__") {
			t.Fatalf("%s is in the batch, but its server is not up yet", name)
		}
	}
}

// TestTheToolsJoinTheNextTurn is the other half: a server that comes up
// brings its tools with it, on the next turn.
func TestTheToolsJoinTheNextTurn(t *testing.T) {
	cfg := asyncMCPCfg()
	b := newTestBuilderWithConfig(t, cfg)

	blocked := &blockedStart{release: make(chan struct{})}
	b.mcp.start = blocked.start

	// ★ The first turn's snapshot: the server is on its way, so it has no
	//   tools yet.
	first := mustSnapshot(t, b)
	deps, err := first.heavyDepsOf(context.Background())
	if err != nil {
		t.Fatalf("the heavy dependencies: %v", err)
	}
	for _, name := range toolNames(deps.tools) {
		if strings.HasPrefix(name, "mcp__") {
			t.Fatalf("%s is in the first turn's tools, but its server is not up yet", name)
		}
	}

	// The server comes up.
	firstSurface := b.mcp.surfaceHash(b.mgr.Current())
	close(blocked.release)
	waitForMCPSettled(t, b.mcp, "github")
	// ★ The listing and the surface are two steps by one goroutine: the
	//   tools only join the next tree once the surface moved, and that is
	//   what the next turn reads. ★ There is no hook to call any more —
	//   the surface is compared, not announced.
	waitForSurfaceChange(t, b, firstSurface)

	// ★ The next turn's snapshot: a new tree, and the tools are there.
	second := mustSnapshot(t, b)
	if second == first {
		t.Fatal("a changed tool surface must produce a new tree")
	}
	deps, err = second.heavyDepsOf(context.Background())
	if err != nil {
		t.Fatalf("the heavy dependencies: %v", err)
	}
	found := false
	for _, name := range toolNames(deps.tools) {
		if name == "mcp__github__read_issue" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the tools did not join the next turn: %v", toolNames(deps.tools))
	}
}

// TestTheProviderDoesNotWarnWhileAServerIsStarting says "on its way" is not a
// failure.
func TestTheProviderDoesNotWarnWhileAServerIsStarting(t *testing.T) {
	cfg := asyncMCPCfg()
	b := newTestBuilderWithConfig(t, cfg)

	blocked := &blockedStart{release: make(chan struct{})}
	defer close(blocked.release)
	b.mcp.start = blocked.start

	var buf strings.Builder
	b.log = slog.New(obs.NewHandler(&buf, slog.LevelDebug))
	b.mcp.log = b.log

	if _, err := b.tools.Build(context.Background(), toolEnv{cfg: cfg}); err != nil {
		t.Fatalf("the provider must not fail on a server that is starting: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "unreachable") {
		t.Fatalf("a server on its way is not an unreachable server:\n%s", out)
	}
}

// TestOneServerIsStartedOnlyOnce says the warm and the demand share one
// start.
func TestOneServerIsStartedOnlyOnce(t *testing.T) {
	cfg := asyncMCPCfg()
	b := newTestBuilderWithConfig(t, cfg)

	blocked := &blockedStart{release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(blocked.release) })
	defer release()
	b.mcp.start = blocked.start

	// ★ The warm starts the mounted servers…
	b.Warm(cfg)
	// …and the demand asks for the same one while that start is in flight.
	if _, _, err := b.mcp.Tools(cfg.MCPs["github"]); err != nil &&
		!errors.Is(err, errMCPStarting) {
		t.Fatalf("Tools: %v", err)
	}
	release()
	waitForMCPSettled(t, b.mcp, "github")

	if got := blocked.Starts(); got != 1 {
		t.Fatalf("the server was started %d times, want 1 — one server is one client", got)
	}
}

// TestAFailedStartIsRetriedAfterTheBackoff says a server that could not start
// is asked again, and not in a loop.
func TestAFailedStartIsRetriedAfterTheBackoff(t *testing.T) {
	cfg := asyncMCPCfg()
	b := newTestBuilderWithConfig(t, cfg)

	var starts int32
	b.mcp.start = func(context.Context, context.Context, *decl.MCPServerCfg, *slog.Logger) (mcpClient, error) {
		atomic.AddInt32(&starts, 1)
		return nil, errors.New("the command is not on the PATH")
	}

	// The first ask starts it, and it fails.
	b.mcp.Tools(cfg.MCPs["github"])
	waitForMCPSettled(t, b.mcp, "github")
	if got := atomic.LoadInt32(&starts); got != 1 {
		t.Fatalf("the server was started %d times, want 1", got)
	}

	// ★ Inside the backoff the server is not asked again: the file's own
	//   deadline is the interval, so a broken server is not spawned in a
	//   loop.
	b.mcp.Tools(cfg.MCPs["github"])
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&starts); got != 1 {
		t.Fatalf("the server was started %d times, want 1 — the backoff holds", got)
	}

	// ★ After the backoff it is asked again: one failure is not a
	//   permanent verdict.
	time.Sleep(mcpStartTimeout(cfg.MCPs["github"]) + 50*time.Millisecond)
	b.mcp.Tools(cfg.MCPs["github"])
	waitForMCPSettled(t, b.mcp, "github")
	if got := atomic.LoadInt32(&starts); got != 2 {
		t.Fatalf("the server was started %d times, want 2 — a failure is retried", got)
	}
}

// TestThePanelShowsStarting says "on its way" and "could not start" are two
// different things to the reader.
func TestThePanelShowsStarting(t *testing.T) {
	cfg := asyncMCPCfg()
	b := newTestBuilderWithConfig(t, cfg)

	blocked := &blockedStart{release: make(chan struct{})}
	defer close(blocked.release)
	b.mcp.start = blocked.start

	views := b.MCPServers(context.Background(), spec.DefaultAgentID)
	if len(views) != 1 {
		t.Fatalf("views = %d, want 1", len(views))
	}
	if views[0].Status != MCPServerStarting {
		t.Fatalf("status = %q, want starting", views[0].Status)
	}
	if views[0].Error != "" {
		t.Fatalf("a server on its way carries no error, got %q", views[0].Error)
	}
}
