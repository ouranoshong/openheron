package memarbit

import (
	"context"
	"testing"
	"time"

	"github.com/ouranoshong/openheron/storage"
)

// A second claim on a live lease fails.
func TestASecondClaimOnALiveLeaseFails(t *testing.T) {
	a := New()
	ctx := context.Background()

	if _, err := a.Claim(ctx, "s1", "run-1", "owner-1", 1, time.Minute); err != nil {
		t.Fatalf("the first claim must succeed: %v", err)
	}
	if _, err := a.Claim(ctx, "s1", "run-2", "owner-2", 2, time.Minute); err != storage.ErrLeaseHeld {
		t.Fatalf("want ErrLeaseHeld, got %v", err)
	}
}

// An expired lease can be taken over.
//
// ★ That is the whole point of a budget: the owner that stopped reporting
//
//	does not hold the session for ever.
func TestAnExpiredLeaseCanBeTakenOver(t *testing.T) {
	a := New()
	a.now = func() time.Time { return time.Unix(1000, 0) }
	ctx := context.Background()

	if _, err := a.Claim(ctx, "s1", "run-1", "owner-1", 1, time.Second); err != nil {
		t.Fatalf("the first claim must succeed: %v", err)
	}
	// The clock moves past the lease's expiry.
	a.now = func() time.Time { return time.Unix(1002, 0) }

	lease, err := a.Claim(ctx, "s1", "run-2", "owner-2", 2, time.Second)
	if err != nil {
		t.Fatalf("an expired lease must be claimable: %v", err)
	}
	if lease.RunID != "run-2" {
		t.Fatalf("the new holder must be run-2, got %q", lease.RunID)
	}
	if lease.Epoch != 2 {
		t.Fatalf("the new holder must hold the higher epoch, got %d", lease.Epoch)
	}
}

// A renewal by a holder that was taken over fails.
func TestARenewalByAFencedOutHolderFails(t *testing.T) {
	a := New()
	a.now = func() time.Time { return time.Unix(1000, 0) }
	ctx := context.Background()

	first, err := a.Claim(ctx, "s1", "run-1", "owner-1", 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return time.Unix(1002, 0) }
	if _, err := a.Claim(ctx, "s1", "run-2", "owner-2", 2, time.Second); err != nil {
		t.Fatal(err)
	}

	if _, err := a.Renew(ctx, first, time.Second); err != storage.ErrLeaseHeld {
		t.Fatalf("want ErrLeaseHeld, got %v", err)
	}
}

// A holder that keeps reporting progress keeps its lease.
//
// ★ The renewal is what progress buys. A run that reports nothing loses the
//
//	lease even while it is alive — that is the run-level stall.
func TestAHolderThatReportsProgressKeepsItsLease(t *testing.T) {
	a := New()
	a.now = func() time.Time { return time.Unix(1000, 0) }
	ctx := context.Background()

	lease, err := a.Claim(ctx, "s1", "run-1", "owner-1", 1, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// The clock moves, but the holder keeps renewing.
	for i := 0; i < 5; i++ {
		a.now = func() time.Time { return time.Unix(int64(1000+i), 0) }
		if _, err := a.Renew(ctx, lease, 10*time.Second); err != nil {
			t.Fatalf("the renewal must succeed: %v", err)
		}
	}
	held, ok := a.Held(ctx, "s1")
	if !ok || held.Expired(a.now()) {
		t.Fatal("a holder that reports progress must keep its lease")
	}
}

// A holder that reports nothing loses the lease.
func TestAHolderThatReportsNothingLosesItsLease(t *testing.T) {
	a := New()
	a.now = func() time.Time { return time.Unix(1000, 0) }
	ctx := context.Background()

	if _, err := a.Claim(ctx, "s1", "run-1", "owner-1", 1, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return time.Unix(1011, 0) }

	held, ok := a.Held(ctx, "s1")
	if !ok {
		t.Fatal("the lease must still be there — it expires, it does not vanish")
	}
	if !held.Expired(a.now()) {
		t.Fatal("a lease nobody renewed must have expired")
	}
	// And the session is free.
	if _, err := a.Claim(ctx, "s1", "run-2", "owner-2", 2, 10*time.Second); err != nil {
		t.Fatalf("an expired lease must be claimable: %v", err)
	}
}

// Releasing a lease that is no longer ours does not touch the new holder's
// claim.
func TestReleasingAForeignLeaseDoesNothing(t *testing.T) {
	a := New()
	a.now = func() time.Time { return time.Unix(1000, 0) }
	ctx := context.Background()

	first, err := a.Claim(ctx, "s1", "run-1", "owner-1", 1, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return time.Unix(1002, 0) }
	if _, err := a.Claim(ctx, "s1", "run-2", "owner-2", 2, time.Second); err != nil {
		t.Fatal(err)
	}

	if err := a.Release(ctx, first); err != nil {
		t.Fatal(err)
	}
	held, ok := a.Held(ctx, "s1")
	if !ok || held.RunID != "run-2" {
		t.Fatal("a stale release must not free the new holder's lease")
	}
}

// The fence reports the epoch the session's current holder holds.
func TestTheFenceReportsTheCurrentEpoch(t *testing.T) {
	a := New()
	ctx := context.Background()

	if _, err := a.Claim(ctx, "s1", "run-1", "owner-1", 7, time.Minute); err != nil {
		t.Fatal(err)
	}
	epoch, err := a.Fence(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if epoch != 7 {
		t.Fatalf("the fence must report the holder's epoch, got %d", epoch)
	}
}
