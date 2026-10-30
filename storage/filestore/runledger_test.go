package filestore

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
)

// newLedgerSession lays down a session and returns it with its ledger.
func newLedgerSession(t *testing.T) (*FileSession, storage.RunLedger) {
	t.Helper()
	fm := newManager(t, Options{})
	sess, err := fm.Create(context.Background(), "")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	fs := sess.(*FileSession)
	ledger := fs.RunLedger()
	if ledger == nil {
		t.Fatal("the session must expose a run ledger")
	}
	return fs, ledger
}

func ledgerRow(runID string, state storage.RunState, epoch uint64) storage.Run {
	now := time.Now().UTC()
	return storage.Run{
		RunID:     runID,
		SessionID: "session-1",
		Kind:      contract.TurnQuery,
		State:     state,
		Epoch:     epoch,
		Owner:     "test:1",
		StartedAt: now,
		UpdatedAt: now,
	}
}

// A row must survive a restart: the ledger is the record, and a record
// that only lives in memory is a record that a crash erases.
func TestRunRowSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	fm, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	row := ledgerRow("run-1", storage.RunStarting, 1)
	row.SessionID = sess.ID()
	if err := sess.RunLedger().Append(ctx, row); err != nil {
		t.Fatal(err)
	}

	fresh, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := fresh.Get(ctx, sess.ID())
	if err != nil {
		t.Fatal(err)
	}
	runs, err := reloaded.RunLedger().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	if runs[0].RunID != "run-1" || runs[0].State != storage.RunStarting {
		t.Fatalf("unexpected row: %+v", runs[0])
	}
}

// The reader takes the last row per run id: a run's row is a snapshot, and
// the newest one is the truth.
func TestTheLastRowPerRunIDWins(t *testing.T) {
	ctx := context.Background()
	_, ledger := newLedgerSession(t)

	for _, state := range []storage.RunState{storage.RunStarting, storage.RunRunning, storage.RunCompleted} {
		row := ledgerRow("run-1", state, 1)
		if err := ledger.Append(ctx, row); err != nil {
			t.Fatal(err)
		}
	}

	runs, err := ledger.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	if runs[0].State != storage.RunCompleted {
		t.Fatalf("want the completed row to win, got %s", runs[0].State)
	}
}

// The fence works on the read side: among the open runs, the highest epoch
// owns the session — even when the lower-epoch run wrote its row later.
func TestTheHighestEpochAmongOpenRunsOwnsTheSession(t *testing.T) {
	ctx := context.Background()
	_, ledger := newLedgerSession(t)

	if err := ledger.Append(ctx, ledgerRow("run-old", storage.RunRunning, 3)); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Append(ctx, ledgerRow("run-new", storage.RunRunning, 4)); err != nil {
		t.Fatal(err)
	}
	// The zombie wakes up and writes afterwards.
	if err := ledger.Append(ctx, ledgerRow("run-old", storage.RunRunning, 3)); err != nil {
		t.Fatal(err)
	}

	runs, err := ledger.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	active, ok := storage.ActiveRun(runs)
	if !ok {
		t.Fatal("want an active run")
	}
	if active.RunID != "run-new" {
		t.Fatalf("want run-new to own the session, got %s", active.RunID)
	}
}

// A torn last line is dropped, and the run falls back to the state it was
// really in.
func TestATornLastLineIsDropped(t *testing.T) {
	ctx := context.Background()
	fs, ledger := newLedgerSession(t)

	if err := ledger.Append(ctx, ledgerRow("run-1", storage.RunRunning, 1)); err != nil {
		t.Fatal(err)
	}
	// A crash between two writes leaves half a line behind.
	f, err := os.OpenFile(runsPath(fs.root, fs.id), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"run_id\":\"run-1\",\"sta"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	runs, err := ledger.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("want the torn line to be dropped, got %d rows", len(runs))
	}
	if runs[0].State != storage.RunRunning {
		t.Fatalf("want the run to fall back to running, got %s", runs[0].State)
	}
}

// The epoch never repeats, and it keeps rising across a restart.
func TestTheEpochNeverRepeats(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	fm, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := fm.Create(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := sess.RunLedger().NextEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := sess.RunLedger().NextEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second <= first {
		t.Fatalf("the epoch must rise: %d then %d", first, second)
	}

	row := ledgerRow("run-1", storage.RunRunning, second)
	row.SessionID = sess.ID()
	if err := sess.RunLedger().Append(ctx, row); err != nil {
		t.Fatal(err)
	}

	fresh, err := NewFileManager(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := fresh.Get(ctx, sess.ID())
	if err != nil {
		t.Fatal(err)
	}
	after, err := reloaded.RunLedger().NextEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after <= second {
		t.Fatalf("the epoch must rise across a restart: %d then %d", second, after)
	}
}

// A run the crash left open is closed as interrupted, so the session does
// not read as busy for ever.
func TestReconcileClosesTheRunsACrashLeftOpen(t *testing.T) {
	ctx := context.Background()
	_, ledger := newLedgerSession(t)

	if err := ledger.Append(ctx, ledgerRow("run-open", storage.RunRunning, 1)); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Append(ctx, ledgerRow("run-done", storage.RunCompleted, 2)); err != nil {
		t.Fatal(err)
	}

	closed, err := ledger.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatalf("want 1 run closed, got %d", closed)
	}

	runs, err := ledger.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runs {
		if r.RunID == "run-open" && r.State != storage.RunInterrupted {
			t.Fatalf("want the open run to be interrupted, got %s", r.State)
		}
		if r.RunID == "run-done" && r.State != storage.RunCompleted {
			t.Fatalf("a finished run must not be touched, got %s", r.State)
		}
	}
}

// The ledger is bounded: a session with many runs keeps the last ones and
// drops the rest, and the file stays valid.
func TestTheLedgerIsBounded(t *testing.T) {
	ctx := context.Background()
	_, ledger := newLedgerSession(t)

	for i := 0; i < maxRunLedgerRows+20; i++ {
		row := ledgerRow("run-"+string(rune('a'+i%26))+string(rune('a'+i/26)), storage.RunCompleted, uint64(i+1))
		if err := ledger.Append(ctx, row); err != nil {
			t.Fatal(err)
		}
	}

	runs, err := ledger.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) == 0 {
		t.Fatal("the ledger must keep its recent rows")
	}
	if len(runs) > maxRunLedgerRows {
		t.Fatalf("the ledger must stay bounded, got %d rows", len(runs))
	}
}
