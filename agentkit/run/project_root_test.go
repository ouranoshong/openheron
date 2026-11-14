package run

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/agentkit/fsx"
	"github.com/ouranoshong/openheron/contract"
)

// ★ The project root is **per session**, and it reaches the round through
//
//	the context. ★ The context is the only channel that reaches both
//	consumers — the prompt that names the directory and the tools that
//	write in it — so the test looks at what the agent sees: that is the
//	context every tool call gets.
func TestTheRoundCarriesTheSessionsProjectRoot(t *testing.T) {
	seen := ""
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			seen = fsx.ProjectRoot(ctx)
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	sm := newFakeSessionManager()
	rt := New(Config{
		AgentBuilder:   b,
		SessionManager: sm,
		Input:          InputConfig{ProjectRoot: "/default/root"},
		StartTimeout:   5 * time.Second,
		ProjectRootFor: func(_ context.Context, _ string) string { return "/work/beta" },
	})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })

	sess := mustSession(t, sm)
	turn, err := rt.Submit(context.Background(), sess.ID(), "", "hello there", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)

	if seen != "/work/beta" {
		t.Fatalf("the round's project root = %q, want %q", seen, "/work/beta")
	}
}

// ★ Without an injected resolver the round keeps the process-level answer:
//
//	the default project root. ★ A session that belongs to no project has
//	nowhere else to work.
func TestWithoutAResolverTheRoundKeepsTheDefaultRoot(t *testing.T) {
	seen := ""
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			seen = fsx.ProjectRoot(ctx)
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	sm := newFakeSessionManager()
	rt := New(Config{
		AgentBuilder:   b,
		SessionManager: sm,
		Input:          InputConfig{ProjectRoot: "/default/root"},
		StartTimeout:   5 * time.Second,
	})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })

	sess := mustSession(t, sm)
	turn, err := rt.Submit(context.Background(), sess.ID(), "", "hello there", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)

	if seen != "/default/root" {
		t.Fatalf("the round's project root = %q, want %q", seen, "/default/root")
	}
}

// ★ The prompt names the round's root. ★ A model that is told about one
//
//	directory and refused in another is being lied to, and the refusal
//	arrives as "permission denied" with no explanation.
func TestThePromptNamesTheRoundsProjectRoot(t *testing.T) {
	c := InputConfig{ProjectRoot: "/default/root"}

	in, err := c.buildInput(context.Background(), inputParams{
		ProjectRoot: "/work/beta",
	}, schema.UserMessage("hello"))
	if err != nil {
		t.Fatal(err)
	}
	prompt := in.Messages[0].Content
	if !strings.Contains(prompt, "Project root: /work/beta") {
		t.Fatalf("the prompt must name the round's root:\n%s", prompt)
	}
	if strings.Contains(prompt, "/default/root") {
		t.Fatalf("the prompt must not name the process's root:\n%s", prompt)
	}
}

// ★ A round without a root of its own falls back to the process's answer.
func TestThePromptFallsBackToTheConfiguredRoot(t *testing.T) {
	c := InputConfig{ProjectRoot: "/default/root"}

	in, err := c.buildInput(context.Background(), inputParams{}, schema.UserMessage("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(in.Messages[0].Content, "Project root: /default/root") {
		t.Fatalf("the prompt must name the configured root:\n%s", in.Messages[0].Content)
	}
}
