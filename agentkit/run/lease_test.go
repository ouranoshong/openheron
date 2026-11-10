package run

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
	"github.com/ouranoshong/openheron/storage/memarbit"
)

// newLeasedRuntime builds a runtime whose leases are short.
//
// ★ The TTL is short on purpose: a test cannot wait ten minutes for a lease
//
//	to run out, and the length of the budget is not what these tests are
//	about.
func newLeasedRuntime(t *testing.T, b AgentBuilder, ttl time.Duration) (*Runtime, *fakeSessionManager) {
	t.Helper()
	sm := newFakeSessionManager()
	rt := New(Config{
		AgentBuilder:   b,
		SessionManager: sm,
		Input:          InputConfig{},
		StartTimeout:   5 * time.Second,
		LeaseArbiter:   memarbit.New(),
		LeaseTTL:       ttl,
	})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	return rt, sm
}

// A live owner holds the session: a second run is refused.
//
// ★ The lease does not weaken the gate — it strengthens it. The refusal is
//
//	the same 409 the turn slot has always produced.
func TestALiveOwnerHoldsTheSession(t *testing.T) {
	started := make(chan struct{}, 4)
	rt, sm := newLeasedRuntime(t, &fakeBuilder{agent: blockingAgent(started)}, time.Minute)
	ctx := context.Background()
	sess := mustSession(t, sm)

	if _, err := rt.Submit(ctx, sess.ID(), "", "hold the turn open", nil); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	t.Cleanup(func() { _ = rt.Abort(context.Background(), sess.ID()) })
	<-started

	if _, err := rt.Submit(ctx, sess.ID(), "", "second", nil); err == nil {
		t.Fatal("a second run must be refused while the owner holds the session")
	}
}

// A stalled owner does not hold the session for ever.
//
// ★ The lease is a budget: an owner that stops reporting progress loses it,
//
//	and the session may be taken over. The turn slot alone cannot say that —
//	its owner is still there, holding the pointer.
//
// ★ This is the failure the lease exists for. The blocking agent reports
//
//	nothing, so its lease runs out.
func TestAStalledOwnerDoesNotHoldTheSessionForEver(t *testing.T) {
	started := make(chan struct{}, 4)
	rt, sm := newLeasedRuntime(t, &fakeBuilder{agent: blockingAgent(started)}, 120*time.Millisecond)
	ctx := context.Background()
	sess := mustSession(t, sm)

	if _, err := rt.Submit(ctx, sess.ID(), "", "hold the turn open", nil); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	t.Cleanup(func() { _ = rt.Abort(context.Background(), sess.ID()) })
	<-started

	// The owner stalls: it reports no progress, so its lease runs out.
	time.Sleep(200 * time.Millisecond)

	if _, err := rt.Submit(ctx, sess.ID(), "", "take over", nil); err != nil {
		t.Fatalf("a stalled owner must not hold the session for ever: %v", err)
	}
}

// A run that reports progress keeps its lease.
//
// ★ Progress is the only thing that renews a lease. An owner that is alive
//
//	and working is never fenced out, however long its turn takes.
func TestAProgressingRunKeepsItsLease(t *testing.T) {
	rt, sm := newLeasedRuntime(t, &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			for i := 0; i < 5; i++ {
				emit(textEvent("working"))
				time.Sleep(40 * time.Millisecond)
			}
			emit(exitEvent())
		}}
	}}, 150*time.Millisecond)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "work for a while", nil)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)

	// The run finished on its own: it was never fenced out.
	runs := ledgerOf(t, sess)
	if len(runs) != 1 || runs[0].State != storage.RunCompleted {
		t.Fatalf("a progressing run must finish, got %+v", runs)
	}
}
