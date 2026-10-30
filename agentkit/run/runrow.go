package run

import (
	"context"
	"sync"
	"time"

	"github.com/ouranoshong/openheron/storage"
)

// runRow is the durable row of one run.
//
// ★ Every write goes through here, so the ledger only ever sees a complete
//
//	snapshot of the run and never a delta. The row is the run's public
//	record: it is what a scan reads, and what a second surface attaches to.
//
// ★ The first terminal write wins. A run that is closed by an abort and
//
//	then by the archive must not change its story halfway through: the
//	reader would see two different endings for the same run.
type runRow struct {
	mu     sync.Mutex
	ledger storage.RunLedger
	run    storage.Run
	closed bool
}

// newRunRow wraps a run's first snapshot.
func newRunRow(ledger storage.RunLedger, r storage.Run) *runRow {
	return &runRow{ledger: ledger, run: r}
}

// set writes a non-terminal state.
func (rw *runRow) set(state storage.RunState) {
	if rw == nil {
		return
	}
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.closed {
		return
	}
	rw.run.State = state
	rw.run.UpdatedAt = time.Now().UTC()
	rw.writeLocked()
}

// finish writes the terminal row.
//
// ★ A closed row stays closed. The run's story is written once: the abort
//
//	and the archive race for it, and whichever lands first is the truth.
func (rw *runRow) finish(state storage.RunState, code, message string) {
	if rw == nil {
		return
	}
	rw.mu.Lock()
	defer rw.mu.Unlock()
	if rw.closed {
		return
	}
	now := time.Now().UTC()
	rw.run.State = state
	rw.run.UpdatedAt = now
	rw.run.EndedAt = now
	rw.run.ErrorCode = code
	rw.run.ErrorMessage = message
	rw.closed = true
	rw.writeLocked()
}

// isClosed reports whether the row already carries a terminal state.
func (rw *runRow) isClosed() bool {
	if rw == nil {
		return true
	}
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return rw.closed
}

// writeLocked appends the current snapshot.
//
// ★ A failed write is logged, not returned: the run itself must not fail
//
//	because its bookkeeping could not be written. The ledger is a record,
//	not the run's execution — and a missing row is visible (the scan will
//	not see the run), while a failed run is not recoverable at all.
func (rw *runRow) writeLocked() {
	if rw.ledger == nil {
		return
	}
	if err := rw.ledger.Append(context.Background(), rw.run); err != nil {
		logf("session=%s run=%s failed to write the run row: %v", rw.run.SessionID, rw.run.RunID, err)
	}
}
