package storage

import (
	"context"
	"errors"
	"time"
)

// ErrLeaseHeld is returned when another run holds the session's lease.
//
// ★ It is a conflict, not a failure: the caller may retry once the lease
//
//	expires or its holder releases it. The HTTP layer maps it to 409.
var ErrLeaseHeld = errors.New("the session's lease is held by another run")

// Lease is a time-bounded claim on a session.
//
// ★ Epoch is the fence: it is monotonically increasing per session, and
//
//	every durable write the holder makes carries it. A holder whose epoch
//	is behind the session's current epoch has been fenced out and must not
//	write.
type Lease struct {
	// RunID is the run that holds the lease.
	RunID string
	// SessionID is the session the lease covers.
	SessionID string
	// Epoch is the fence this lease holds.
	Epoch uint64
	// Owner names the instance that holds it.
	Owner string
	// ExpiresAt is when the lease runs out.
	//
	// ★ A lease is a **budget**, not a schedule: a holder that stops
	//	reporting progress loses it, however alive it is. That is what
	//	tells a stalled owner from a working one.
	ExpiresAt time.Time
}

// Expired reports whether the lease has run out.
func (l Lease) Expired(now time.Time) bool {
	return !l.ExpiresAt.After(now)
}

// LeaseArbiter decides who owns a session.
//
// ★ The arbiter does NOT allocate the epoch. The epoch has to be monotonic
//
//	across restarts, so it cannot live in a volatile structure — the ledger
//	allocates it (RunLedger.NextEpoch) and the lease carries the number it
//	was given. The arbiter's job is liveness: who holds the claim, and
//	until when.
//
// ★ Keeping the two apart is what lets the ledger's read rule stay simple:
//
//	"highest epoch wins" needs no knowledge of liveness, and "the lease
//	expired" needs no knowledge of write order.
type LeaseArbiter interface {
	// Claim acquires the session's lease.
	//
	// It fails with ErrLeaseHeld when another run holds a live lease, and
	// succeeds when the lease is expired or is already ours.
	Claim(ctx context.Context, sessionID, runID, owner string, epoch uint64, ttl time.Duration) (Lease, error)
	// Renew extends a lease the caller still holds.
	//
	// It fails with ErrLeaseHeld when the lease has been taken over — the
	// caller is no longer the owner and must stop.
	Renew(ctx context.Context, l Lease, ttl time.Duration) (Lease, error)
	// Release gives the lease back.
	Release(ctx context.Context, l Lease) error
	// Fence reports the session's current epoch.
	Fence(ctx context.Context, sessionID string) (uint64, error)
	// Held reports the session's current lease, if any.
	//
	// ★ It is how a caller tells "the owner is still there" from "the
	//	owner's lease ran out" — the difference between a busy session and
	//	a stalled one. Without the expiry the gate could not tell them
	//	apart, and a stalled owner would hold the session for ever.
	Held(ctx context.Context, sessionID string) (Lease, bool)
}
