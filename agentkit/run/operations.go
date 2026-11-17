package run

import (
	"context"

	"github.com/ouranoshong/openheron/storage"
)

// recordOperation appends one line to the session's operation log.
//
// ★ A failed write is logged, not returned: the run must not fail because
//
//	its bookkeeping could not be written. The ledger's row says the same —
//	a missing row is visible (the scan will not see the run), while a
//	failed run is not recoverable at all.
//
// ★ The line is a projection, never a source: nothing in the runtime reads
//
//	the log back. The gate reads the session record, the scan reads the
//	ledger, the transcript reads the transcript.
func (rt *Runtime) recordOperation(sess storage.Session, op storage.Operation) {
	if sess == nil {
		return
	}
	log := sess.Operations()
	if log == nil {
		return
	}
	if err := log.Append(context.Background(), op); err != nil {
		rt.logf("session=%s failed to record the operation %s: %v", sess.ID(), op.Kind, err)
	}
}
