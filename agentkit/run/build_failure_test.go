package run

import (
	"context"
	"errors"
	"testing"
	"time"
)

// ★ **The reason a turn never starts must reach the client.** The build is
//
//	where a configuration problem becomes a runtime one — an agent whose
//	model is not declared, a skill that cannot be read back, a provider whose
//	file is broken. Before this, the build error went nowhere: the turn was
//	never marked failed, `Wait` sat until the start timeout, and the client
//	was told "turn start timeout" — a symptom that names neither the file nor
//	the reason.
func TestTheBuildFailureReachesTheWaiter(t *testing.T) {
	reason := errors.New(`agent "main" is left out of the tree: the model openai/gpt-4o-mini is not declared under llm/`)
	b := &fakeBuilder{err: reason}
	sm := newFakeSessionManager()
	rt := New(Config{
		AgentBuilder:   b,
		SessionManager: sm,
		Input:          InputConfig{},
		StartTimeout:   5 * time.Second,
	})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = turn.Wait(5 * time.Second)
	if err == nil {
		t.Fatal("a turn whose agent cannot be built must not report success")
	}
	if errors.Is(err, ErrStartTimeout) {
		t.Fatalf("★ the build failure must not be reported as a start timeout: %v", err)
	}
	if !errors.Is(err, reason) {
		t.Fatalf("the waiter must get the build's own reason, got %v", err)
	}
}

// ★ The same failure must not leave the session busy for ever: the turn that
//
//	never ran has to release its slot, or the session reads as busy and every
//	later run is refused.
func TestAFailedBuildReleasesTheSession(t *testing.T) {
	b := &fakeBuilder{err: errors.New("the agent cannot be built")}
	sm := newFakeSessionManager()
	rt := New(Config{
		AgentBuilder:   b,
		SessionManager: sm,
		Input:          InputConfig{},
		StartTimeout:   5 * time.Second,
	})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err == nil {
		t.Fatal("the build failure must reach the waiter")
	}

	waitFor(t, func() bool {
		_, err := rt.Submit(ctx, sess.ID(), "", "again", nil)
		return err == nil
	})
}

// ★ The builder's own words must survive: the sentinel says what KIND of
//
//	failure this is, and the wrapped error says which file and which model.
func TestTheBuildFailureIsReadable(t *testing.T) {
	reason := errors.New("agent \"main\" is left out of the tree")
	b := &fakeBuilder{err: reason}
	sm := newFakeSessionManager()
	rt := New(Config{
		AgentBuilder:   b,
		SessionManager: sm,
		Input:          InputConfig{},
		StartTimeout:   5 * time.Second,
	})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	sess := mustSession(t, sm)

	turn, err := rt.Submit(context.Background(), sess.ID(), "", "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	err = turn.Wait(5 * time.Second)
	if err == nil {
		t.Fatal("the build failure must reach the waiter")
	}
	if !errors.Is(err, ErrAgentUnavailable) {
		t.Fatalf("the failure must be the agent-unavailable one, got %v", err)
	}
	// ★ The message is the builder's **own** — the sentinel classifies, it
	//   does not prefix. The user must read the reason once, not twice.
	if got := err.Error(); got != reason.Error() {
		t.Fatalf("the builder's message must survive intact, got %q", got)
	}
}
