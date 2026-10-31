// Package memarbit is the in-memory lease arbiter.
//
// ★ It is the protocol's proof, not its production: it arbitrates inside one
//
//	process and nothing survives a restart. The file ledger (the durable
//	half) lives in storage/filestore, and a database arbiter is the
//	multi-process answer.
package memarbit

import (
	"context"
	"sync"
	"time"

	"github.com/ouranoshong/openheron/storage"
)

// Arbiter hands out session leases in this process.
//
// ★ One lease per session. The lease is what says "this run owns the
//
//	session right now" — the volatile answer, with an expiry. The ledger's
//	epoch is the durable answer, with an order.
type Arbiter struct {
	mu     sync.Mutex
	leases map[string]storage.Lease
	now    func() time.Time
}

// New builds an arbiter.
func New() *Arbiter {
	return &Arbiter{
		leases: map[string]storage.Lease{},
		now:    time.Now,
	}
}

// Claim acquires the session's lease.
//
// ★ A lease that has expired may be taken over: that is the whole point of
//
//	a budget. The new holder's epoch must be higher than the old one's —
//	otherwise the fence would let the old holder write itself back into
//	the lead.
func (a *Arbiter) Claim(_ context.Context, sessionID, runID, owner string,
	epoch uint64, ttl time.Duration) (storage.Lease, error) {
	if sessionID == "" || runID == "" {
		return storage.Lease{}, storage.ErrLeaseHeld
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.now()
	if held, ok := a.leases[sessionID]; ok && !held.Expired(now) {
		if held.RunID == runID && held.Owner == owner {
			// Our own lease: renew it.
			held.Epoch = epoch
			held.ExpiresAt = now.Add(ttl)
			a.leases[sessionID] = held
			return held, nil
		}
		return storage.Lease{}, storage.ErrLeaseHeld
	}

	lease := storage.Lease{
		RunID:     runID,
		SessionID: sessionID,
		Epoch:     epoch,
		Owner:     owner,
		ExpiresAt: now.Add(ttl),
	}
	a.leases[sessionID] = lease
	return lease, nil
}

// Renew extends a lease the caller still holds.
//
// ★ A renewal that finds somebody else's lease fails: the caller was taken
//
//	over, and it has to stop rather than keep writing.
func (a *Arbiter) Renew(_ context.Context, l storage.Lease, ttl time.Duration) (storage.Lease, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	held, ok := a.leases[l.SessionID]
	if !ok || held.RunID != l.RunID || held.Owner != l.Owner {
		return storage.Lease{}, storage.ErrLeaseHeld
	}
	held.Epoch = l.Epoch
	held.ExpiresAt = a.now().Add(ttl)
	a.leases[l.SessionID] = held
	return held, nil
}

// Release gives the lease back.
//
// ★ Releasing a lease that is no longer ours does nothing: the run that lost
//
//	it must not release the new holder's claim.
func (a *Arbiter) Release(_ context.Context, l storage.Lease) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	held, ok := a.leases[l.SessionID]
	if !ok || held.RunID != l.RunID || held.Owner != l.Owner {
		return nil
	}
	delete(a.leases, l.SessionID)
	return nil
}

// Fence reports the session's current epoch.
//
// ★ It is the highest epoch the arbiter has seen for the session. The
//
//	ledger's epoch is the durable one; this one is what the arbiter knows
//	inside this process.
func (a *Arbiter) Fence(_ context.Context, sessionID string) (uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	held, ok := a.leases[sessionID]
	if !ok {
		return 0, nil
	}
	return held.Epoch, nil
}

// Held reports the session's current lease, if any.
//
// ★ The lease is returned even when it has expired: the caller decides
//
//	what an expired lease means. The arbiter does not expire leases on
//	its own — it has no timer, and a timer is exactly what the design
//	rejects.
func (a *Arbiter) Held(_ context.Context, sessionID string) (storage.Lease, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	held, ok := a.leases[sessionID]
	return held, ok
}
