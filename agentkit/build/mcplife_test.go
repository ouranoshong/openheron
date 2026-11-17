package build

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
)

// The server a turn started must outlive that turn.
//
// ★ The child's life used to come from the caller, and the caller is a
//
//	turn: the run cancels the context it hands to the builder when the
//	turn ends, and the child died with it. Every later call to that
//	server's tools then answered "transport error: failed to write
//	request" — for a tool that was still offered, and could not be
//	called.
func TestTheServerATurnStartedOutlivesThatTurn(t *testing.T) {
	cfg := liveMCPConfig(t)
	b := newTestBuilderWithConfig(t, cfg)

	// ★ The turn's context: this is what the run hands to the builder.
	turnCtx, endTurn := context.WithCancel(context.Background())

	// The first turn asks for the tools; the server is not up yet, so
	// this turn's own dispatch is the one that starts it.
	if _, err := b.tools.Build(turnCtx, toolEnv{cfg: cfg}); err != nil {
		t.Fatalf("the first build: %v", err)
	}
	waitForMCPSettled(t, b.mcp, "fake")

	batch, err := b.tools.Build(turnCtx, toolEnv{cfg: cfg})
	if err != nil {
		t.Fatalf("the second build: %v", err)
	}
	if _, err := callReadIssue(t, batch); err != nil {
		t.Fatalf("the call must reach the server: %v", err)
	}

	// ★ The turn ends.
	endTurn()
	time.Sleep(300 * time.Millisecond)

	// ★ The next turn calls the same tool, on the same client.
	out, err := callReadIssue(t, batch)
	if err != nil {
		t.Fatalf("the call must still reach the server after the turn ended: %v", err)
	}
	if !strings.Contains(out, "the issue body") {
		t.Fatalf("the result = %q, want the server's answer", out)
	}
}

// The server the warm started belongs to the manager, not to a turn.
//
// ★ It is the same rule from the other side: the warm and the turn's
//
//	dispatch share one client, and neither of them may own its life.
func TestTheServerTheWarmStartedOutlivesATurn(t *testing.T) {
	cfg := liveMCPConfig(t)
	b := newTestBuilderWithConfig(t, cfg)

	startLiveServer(t, b, cfg, "fake")

	turnCtx, endTurn := context.WithCancel(context.Background())
	batch, err := b.tools.Build(turnCtx, toolEnv{cfg: cfg})
	if err != nil {
		t.Fatalf("the build: %v", err)
	}
	if _, err := callReadIssue(t, batch); err != nil {
		t.Fatalf("the call must reach the server: %v", err)
	}

	endTurn()
	time.Sleep(300 * time.Millisecond)

	if _, err := callReadIssue(t, batch); err != nil {
		t.Fatalf("the call must still reach the server after the turn ended: %v", err)
	}
}

// A server that broke must come back.
//
// ★ The break is the hook the failing call uses: the child is gone, and
//
//	the entry says so. The next turn asks for the tools again, and the
//	server has to be there — a server that stays down is worse than one
//	that never came up, because the tool is still offered.
func TestTheServerComesBackAfterItBroke(t *testing.T) {
	cfg := liveMCPConfig(t)
	b := newTestBuilderWithConfig(t, cfg)

	turnCtx, endTurn := context.WithCancel(context.Background())
	defer endTurn()
	if _, err := b.tools.Build(turnCtx, toolEnv{cfg: cfg}); err != nil {
		t.Fatalf("the first build: %v", err)
	}
	waitForMCPSettled(t, b.mcp, "fake")
	batch, err := b.tools.Build(turnCtx, toolEnv{cfg: cfg})
	if err != nil {
		t.Fatalf("the second build: %v", err)
	}
	if _, err := callReadIssue(t, batch); err != nil {
		t.Fatalf("the call must reach the server: %v", err)
	}

	// ★ The child dies.
	b.mcp.markBroken("fake")()
	if got := mcpStateOf(b.mcp, "fake"); got != mcpNotStarted {
		t.Fatalf("the state after the break = %q, want %q", got, mcpNotStarted)
	}

	// ★ The next turn asks for the tools again.
	nextCtx, endNext := context.WithCancel(context.Background())
	defer endNext()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		batch, err = b.tools.Build(nextCtx, toolEnv{cfg: cfg})
		if err != nil {
			t.Fatalf("the next build: %v", err)
		}
		target := lookupTool(batch, "mcp__fake__read_issue")
		if target != nil {
			out, err := target.(tool.InvokableTool).InvokableRun(context.Background(), "{}")
			if err == nil && strings.Contains(out, "the issue body") {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the server never came back: %q", mcpStateOf(b.mcp, "fake"))
}

// callReadIssue calls the one tool the fake server allows.
func callReadIssue(t *testing.T, batch []tool.BaseTool) (string, error) {
	t.Helper()
	target := findTool(t, batch, "mcp__fake__read_issue")
	return target.(tool.InvokableTool).InvokableRun(context.Background(), "{}")
}

// lookupTool returns the tool with the given exposed name, or nil.
//
// ★ It is findTool without the fatal: a test that waits for a server to
//
//	come back has to be allowed to look and not find it.
func lookupTool(batch []tool.BaseTool, name string) tool.BaseTool {
	for _, tl := range batch {
		info, err := tl.Info(context.Background())
		if err != nil || info == nil {
			continue
		}
		if info.Name == name {
			return tl
		}
	}
	return nil
}
