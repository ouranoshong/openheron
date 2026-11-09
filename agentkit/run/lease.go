package run

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/ouranoshong/openheron/storage"
)

// DefaultLeaseTTL is how long a run's lease survives without progress.
//
// ★ It is a **budget**, not a schedule, and it has to be longer than the
//
//	provider's own timeout: a model that is slow but alive must not be
//	fenced out while it is thinking. The provider's timeout
//	(`initcfg/templates/llm/_generic.yaml:10`, 5m by default) ends a call
//	that is hung, so the lease only has to outlast a call that is slow.
const DefaultLeaseTTL = 10 * time.Minute

// leaseExpired says whether the session's current owner's lease has run out.
//
// ★ False when there is no lease at all: the gate must not weaken just
//
//	because nobody wired an arbiter. Only an expired lease lets a new run
//	take over a busy session.
func (rt *Runtime) leaseExpired(sessionID string) bool {
	if rt.cfg.LeaseArbiter == nil {
		return false
	}
	lease, ok := rt.cfg.LeaseArbiter.Held(context.Background(), sessionID)
	if !ok {
		return false
	}
	return lease.Expired(time.Now())
}

// runLease is one run's claim on its session.
//
// ★ The lease answers "may I run?"; the ledger's epoch answers "am I still
//
//	the owner?". The two are kept apart on purpose (§5.4 of the design
//	document): the lease knows about liveness and nothing else, and the
//	epoch knows about order and nothing else.
type runLease struct {
	arbiter storage.LeaseArbiter
	ttl     time.Duration
	log     *slog.Logger

	mu      sync.Mutex
	lease   storage.Lease
	renewed time.Time
}

// newRunLease builds the lease holder for one run.
//
// ★ A nil arbiter means no lease at all — the runtime then behaves as it
//
//	did before the lease existed, which is what a caller that did not wire
//	one expects.
func newRunLease(arbiter storage.LeaseArbiter, ttl time.Duration, log *slog.Logger) *runLease {
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}
	return &runLease{arbiter: arbiter, ttl: ttl, log: log}
}

// claim takes the session's lease.
//
// ★ It fails with storage.ErrLeaseHeld when another run holds a live lease.
//
//	That is the case the turn slot alone cannot catch: the previous owner
//	is still there, but it is not making progress any more.
func (l *runLease) claim(sessionID, runID, owner string, epoch uint64) error {
	if l == nil || l.arbiter == nil {
		return nil
	}
	lease, err := l.arbiter.Claim(context.Background(), sessionID, runID, owner, epoch, l.ttl)
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.lease = lease
	l.renewed = time.Now()
	l.mu.Unlock()
	return nil
}

// renew extends the lease when the run reports progress.
//
// ★ Progress is the only thing that renews a lease. A timer would keep the
//
//	lease alive for a run that is stuck in a hung model call — which is
//	exactly the failure the lease exists to catch.
//
// ★ The renewal is throttled: a streaming model reports progress on every
//
//	token, and a renewal per token would be a lock per token for nothing.
//
// ★ It reports false when the lease is gone: the run was taken over, and
//
//	it has to stop rather than keep writing for a session it no longer
//	owns.
func (l *runLease) renew() bool {
	if l == nil || l.arbiter == nil {
		return true
	}
	l.mu.Lock()
	if time.Since(l.renewed) < l.ttl/4 {
		l.mu.Unlock()
		return true
	}
	lease := l.lease
	l.renewed = time.Now()
	l.mu.Unlock()

	if lease.SessionID == "" {
		return true
	}
	if _, err := l.arbiter.Renew(context.Background(), lease, l.ttl); err != nil {
		return false
	}
	return true
}

// release gives the lease back.
func (l *runLease) release() {
	if l == nil || l.arbiter == nil {
		return
	}
	l.mu.Lock()
	lease := l.lease
	l.lease = storage.Lease{}
	l.mu.Unlock()
	if lease.SessionID == "" {
		return
	}
	if err := l.arbiter.Release(context.Background(), lease); err != nil {
		if l.log != nil {
			l.log.Warn("[openheronrt] failed to release the lease",
				"session", lease.SessionID, "err", err)
		}
	}
}
