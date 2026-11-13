package run

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
	"github.com/ouranoshong/openheron/storage/memarbit"
)

// ★ These tests are about the **gate under concurrency**, not about the gate.
//
//   The sequential tests (gate_test.go) ask "what happens when a second
//   request arrives while a turn runs" — they prove the gate reads the
//   state correctly.
//
//   These ask "what happens when several requests arrive at the same
//   instant" — they prove the session ends up with **exactly one owner**.
//   A gate that checks and then claims in two steps would pass every
//   sequential test and let two turns through at once.

// runConcurrent releases n calls at the same instant and collects what each
// one got.
//
// ★ The gate is a lock, so the calls have to overlap for the test to mean
// anything: a scheduler that happens to run them one after another would
// prove nothing. The barrier releases them together, and the count of
// admitted calls is what the assertion is about.
func runConcurrent(t *testing.T, n int, call func(i int) error) []error {
	t.Helper()
	var wg sync.WaitGroup
	gate := make(chan struct{})
	errs := make([]error, n)

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-gate
			errs[i] = call(i)
		}(i)
	}
	close(gate)
	wg.Wait()
	return errs
}

// countAdmitted reports how many calls were admitted and how many were
// refused as busy.
func countAdmitted(errs []error) (admitted, busy, other int) {
	for _, err := range errs {
		switch {
		case err == nil:
			admitted++
		case errors.Is(err, ErrSessionBusy):
			busy++
		default:
			other++
		}
	}
	return admitted, busy, other
}

// newClaimRuntime builds a runtime that is **not** torn down when the test
// ends.
//
// ★ The shutdown waits for the session's loop, and eino's TurnLoop.Wait
//
//	blocks for ever when Run was never called — the framework says so in
//	its own contract ("If Run is never called, Wait blocks forever").
//	A claim builds a loop and hands it to the caller, and the caller is
//	the one that runs it: a bare claim leaves a loop that nobody will
//	ever run.
//
//	★ Production never hits that, because Submit and Resume run the loop
//	  they claimed. A test that claims without running does, so it may
//	  not shut the runtime down — the claim is the whole point of the
//	  test, and the process goes away with it.
func newClaimRuntime(t *testing.T, b AgentBuilder, arb storage.LeaseArbiter, ttl time.Duration) (*Runtime, *fakeSessionManager) {
	t.Helper()
	sm := newFakeSessionManager()
	rt := New(Config{
		AgentBuilder:   b,
		SessionManager: sm,
		Input:          InputConfig{},
		StartTimeout:   5 * time.Second,
		LeaseArbiter:   arb,
		LeaseTTL:       ttl,
	})
	return rt, sm
}

// newArbitratedRuntime builds a runtime with a lease arbiter and the ordinary
// shutdown cleanup.
//
// ★ The lease is the second half of the ownership rule, and it is reached
// after the turn slot is claimed. If the two disagreed, the session would
// have an owner that the gate does not know about.
func newArbitratedRuntime(t *testing.T, b AgentBuilder, arb storage.LeaseArbiter, ttl time.Duration) (*Runtime, *fakeSessionManager) {
	t.Helper()
	rt, sm := newClaimRuntime(t, b, arb, ttl)
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	return rt, sm
}

// expireLease makes the session's current lease run out.
//
// ★ The lease is the liveness half of the ownership rule, and "the owner's
//
//	lease ran out" is the only thing that lets another run take a busy
//	session over. The arbiter does not age leases on its own — it stores
//	an expiry — so the test ages it by renewing it with a budget that is
//	already spent.
func expireLease(t *testing.T, arb *memarbit.Arbiter, sessionID string) {
	t.Helper()
	held, ok := arb.Held(context.Background(), sessionID)
	if !ok {
		t.Fatalf("the session holds no lease to expire")
	}
	if _, err := arb.Renew(context.Background(), held, -time.Second); err != nil {
		t.Fatalf("expire the lease: %v", err)
	}
}

// ★ Simultaneous queries on one session: exactly one is admitted.
//
//	This is the property the admission gate exists for. Two queries that
//	both got in would fight over the session's context — the second would
//	take the turn over from the first, along with its checkpoint and its
//	pending approval.
//
//	★ The gate holds because claimTurn checks and claims under one lock
//	  (the busy check and the takeover are in the same critical section).
//	  A refactor that moves the claim out of the lock would break this
//	  test — and nothing else would.
func TestConcurrentQueriesAdmitExactlyOne(t *testing.T) {
	started := make(chan struct{}, 64)
	b := &fakeBuilder{agent: blockingAgent(started)}
	rt, sm := newTestRuntime(t, b)
	sess := mustSession(t, sm)
	ctx := context.Background()

	const n = 16
	errs := runConcurrent(t, n, func(i int) error {
		_, err := rt.Submit(ctx, sess.ID(), "", "concurrent", nil)
		return err
	})

	admitted, busy, other := countAdmitted(errs)
	t.Logf("queries: admitted=%d busy=%d other=%d", admitted, busy, other)
	if admitted != 1 {
		t.Errorf("exactly one query must be admitted, got %d", admitted)
	}
	if busy != n-1 {
		t.Errorf("the other %d must be refused with ErrSessionBusy, got %d", n-1, busy)
	}
	if other != 0 {
		t.Errorf("%d requests failed for another reason", other)
	}
}

// ★ The same, with the lease arbiter wired — the production shape.
func TestConcurrentQueriesAdmitExactlyOneWithLease(t *testing.T) {
	started := make(chan struct{}, 64)
	b := &fakeBuilder{agent: blockingAgent(started)}
	rt, sm := newArbitratedRuntime(t, b, memarbit.New(), time.Minute)
	sess := mustSession(t, sm)
	ctx := context.Background()

	const n = 16
	errs := runConcurrent(t, n, func(i int) error {
		_, err := rt.Submit(ctx, sess.ID(), "", "concurrent", nil)
		return err
	})

	admitted, busy, other := countAdmitted(errs)
	t.Logf("queries with lease: admitted=%d busy=%d other=%d", admitted, busy, other)
	if admitted != 1 {
		t.Errorf("exactly one query must be admitted, got %d", admitted)
	}
	if busy != n-1 {
		t.Errorf("the other %d must be refused with ErrSessionBusy, got %d", n-1, busy)
	}
	if other != 0 {
		t.Errorf("%d requests failed for another reason", other)
	}
}

// ★ Simultaneous claims of one session's turn slot: exactly one is admitted.
//
//	★ This tests claimTurn rather than Resume, and that is deliberate.
//
//	claimTurn is **the gate**: the check and the claim happen in one
//	critical section, and everything above it — Submit, Resume — is a
//	caller. A test that drives Resume instead would be testing the
//	approval's consumption order as much as the gate, and it would need a
//	checkpoint to make the resumed turn real.
//
//	★ The kind matters: a resume behind a resume is allowed (it is the same
//	round of the conversation), so the test claims with the query kind — the
//	one that conflicts with itself.
func TestConcurrentClaimsAdmitExactlyOne(t *testing.T) {
	rt, sm := newClaimRuntime(t, &fakeBuilder{}, nil, 0)
	sess := mustSession(t, sm)
	ctx := context.Background()

	const n = 16
	errs := runConcurrent(t, n, func(i int) error {
		// ★ The winner keeps the slot. Giving it back would let the next
		//   request in, and the count would say nothing: the gate admits
		//   one at a time, and "one at a time" is only visible while the
		//   holder holds on.
		_, _, _, _, err := rt.claimTurn(ctx, sess, contract.TurnQuery, "", "")
		return err
	})

	admitted, busy, other := countAdmitted(errs)
	t.Logf("claims: admitted=%d busy=%d other=%d", admitted, busy, other)
	if admitted != 1 {
		t.Errorf("exactly one claim must be admitted, got %d", admitted)
	}
	if busy != n-1 {
		t.Errorf("the other %d must be refused with ErrSessionBusy, got %d", n-1, busy)
	}
	if other != 0 {
		t.Errorf("%d claims failed for another reason", other)
	}
}

// ★ The same, with the lease arbiter wired — the production shape.
func TestConcurrentClaimsWithLeaseAdmitExactlyOne(t *testing.T) {
	rt, sm := newClaimRuntime(t, &fakeBuilder{}, memarbit.New(), time.Minute)
	sess := mustSession(t, sm)
	ctx := context.Background()

	const n = 16
	errs := runConcurrent(t, n, func(i int) error {
		_, _, _, _, err := rt.claimTurn(ctx, sess, contract.TurnQuery, "", "")
		return err
	})

	admitted, busy, other := countAdmitted(errs)
	t.Logf("claims with lease: admitted=%d busy=%d other=%d", admitted, busy, other)
	if admitted != 1 {
		t.Errorf("exactly one claim must be admitted, got %d", admitted)
	}
	if busy != n-1 {
		t.Errorf("the other %d must be refused with ErrSessionBusy, got %d", n-1, busy)
	}
	if other != 0 {
		t.Errorf("%d claims failed for another reason", other)
	}
}

// ★ A query and a resume at the same instant: exactly one of them is
// admitted.
//
//	★ The session's slot holds one turn, and the two kinds may not both
//	take it: a query is a new round, and it must not start while a
//	resume is answering an approval — nor the other way round. The rule
//	is in conflictTurns, and the pre-checks agree with it: ensureIdle
//	refuses a query behind anything live, and ensureNoLiveQuery refuses
//	a resume behind a live query.
//
//	★ The test is at the claim level, and that is deliberate. The
//	pre-checks are reads that happen before the claim, so two requests
//	that arrive together both pass them — the claim is the only place
//	where the two are serialized against each other.
//
//	★ Twenty rounds, each on its own session: one collision proves
//	nothing about a race.
func TestConcurrentQueryAndResumeClaimsAdmitExactlyOne(t *testing.T) {
	rt, sm := newClaimRuntime(t, &fakeBuilder{}, nil, 0)
	ctx := context.Background()

	const rounds = 20
	for round := 0; round < rounds; round++ {
		sess := mustSession(t, sm)

		var wg sync.WaitGroup
		gate := make(chan struct{})
		var queryErr, resumeErr error

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-gate
			_, _, _, _, queryErr = rt.claimTurn(ctx, sess, contract.TurnQuery, "", "")
		}()
		go func() {
			defer wg.Done()
			<-gate
			_, _, _, _, resumeErr = rt.claimTurn(ctx, sess, contract.TurnResume, "", "")
		}()
		close(gate)
		wg.Wait()

		admitted := 0
		for _, err := range []error{queryErr, resumeErr} {
			if err == nil {
				admitted++
				continue
			}
			if !errors.Is(err, ErrSessionBusy) {
				t.Fatalf("round %d: unexpected failure: %v", round, err)
			}
		}
		if admitted != 1 {
			t.Fatalf("round %d: exactly one of the two must be admitted, got %d "+
				"(query=%v resume=%v)", round, admitted, queryErr, resumeErr)
		}
	}
}

// ★ Simultaneous resumes: exactly one of them consumes the approval.
//
//	★ The gate lets every resume through — a resume behind a resume is the
//	  same round of the conversation, and two answers to two groups of
//	  approvals are both legitimate. What only one of them can have is the
//	  pending approval: it is the user's decision, and it is consumed once.
//
//	★ So the loser must fail rather than take the session with it. That is
//	  the part worth pinning: a resume that finds nothing to resume must
//	  give the slot back, or it would lock the session against the answer
//	  that did arrive.
func TestConcurrentResumesAdmitExactlyOne(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("resumed"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	sess := mustSession(t, sm)
	ctx := context.Background()

	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{InterruptID: "int-1"}},
	}); err != nil {
		t.Fatal(err)
	}

	const n = 8
	errs := runConcurrent(t, n, func(i int) error {
		_, err := rt.Resume(ctx, sess.ID(), "",
			[]ApprovalDecision{{InterruptID: "int-1", Approved: true}})
		return err
	})

	admitted, _, other := countAdmitted(errs)
	t.Logf("resumes: admitted=%d refused=%d other=%d", admitted, n-admitted, other)
	if admitted != 1 {
		t.Errorf("exactly one resume must consume the approval, got %d", admitted)
	}
	for i, err := range errs {
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrNoPendingInterrupt) && !errors.Is(err, ErrSessionBusy) {
			t.Errorf("resume %d failed for another reason: %v", i, err)
		}
	}
}

// ★ The lease is what makes a stalled owner give the session up. Requests
// that arrive after the lease ran out must still not both take over.
//
//	★ This is the case the turn slot alone cannot catch — the slot says a
//	  turn is live, but its owner is not making progress any more.
//
//	★ The takeover is still exclusive, and the lease is what makes it so:
//	  the first request to claim the lease holds it, and the arbiter hands
//	  it to one holder at a time.
func TestConcurrentTakeoverAfterLeaseExpiryAdmitsExactlyOne(t *testing.T) {
	started := make(chan struct{}, 64)
	b := &fakeBuilder{agent: blockingAgent(started)}
	arb := memarbit.New()
	rt, sm := newArbitratedRuntime(t, b, arb, time.Minute)
	sess := mustSession(t, sm)
	ctx := context.Background()

	// A turn that holds the session.
	if _, err := rt.Submit(ctx, sess.ID(), "", "first", nil); err != nil {
		t.Fatalf("first Submit: %v", err)
	}

	// Its lease is gone: the owner never reported progress.
	expireLease(t, arb, sess.ID())

	const n = 8
	errs := runConcurrent(t, n, func(i int) error {
		_, err := rt.Submit(ctx, sess.ID(), "", "takeover", nil)
		return err
	})

	admitted, busy, other := countAdmitted(errs)
	t.Logf("takeover: admitted=%d busy=%d other=%d", admitted, busy, other)
	if admitted != 1 {
		t.Errorf("exactly one request must take the session over, got %d", admitted)
	}
	if other != 0 {
		t.Errorf("%d requests failed for another reason", other)
	}
}

// ★ A live lease held by another run refuses the turn, even when the
// session looks idle.
//
//	★ This is the cross-process half of the ownership rule. The turn slot
//	  lives in this process's memory, so a session whose owner died in
//	  another process looks free here — and the lease is what says it is
//	  not.
//
//	★ The cost of the rule is the point too: the session stays locked
//	  until the lease runs out. That is a budget, not a deadlock — the
//	  owner that stalled stops renewing, and the lease expires.
func TestALiveLeaseFromAnotherRunRefusesTheTurn(t *testing.T) {
	arb := memarbit.New()
	rt, sm := newArbitratedRuntime(t, &fakeBuilder{}, arb, time.Minute)
	sess := mustSession(t, sm)
	ctx := context.Background()

	// Another instance holds the session's lease.
	if _, err := arb.Claim(ctx, sess.ID(), "other-run", "other-instance", 1, time.Minute); err != nil {
		t.Fatalf("claim the foreign lease: %v", err)
	}

	_, err := rt.Submit(ctx, sess.ID(), "", "mine", nil)
	if !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("want ErrSessionBusy while another run holds the lease, got %v", err)
	}
}

// ★ An expired lease does not lock the session: the run that held it may
// be gone, and the next request has to be let in.
//
//	★ The counterpart of the rule above, and the reason the expiry exists:
//	  a lease that never ran out would keep a dead owner's session locked
//	  for ever.
func TestAnExpiredLeaseDoesNotLockTheSession(t *testing.T) {
	arb := memarbit.New()
	rt, sm := newArbitratedRuntime(t, &fakeBuilder{}, arb, time.Minute)
	sess := mustSession(t, sm)
	ctx := context.Background()

	// Another instance held the lease, and its budget ran out.
	if _, err := arb.Claim(ctx, sess.ID(), "other-run", "other-instance", 1, time.Minute); err != nil {
		t.Fatalf("claim the foreign lease: %v", err)
	}
	expireLease(t, arb, sess.ID())

	if _, err := rt.Submit(ctx, sess.ID(), "", "mine", nil); err != nil {
		t.Fatalf("an expired lease must not lock the session: %v", err)
	}
}
