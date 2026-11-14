package run

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
)

// ledgerOf returns the session's ledger rows.
func ledgerOf(t *testing.T, sess *fakeSession) []storage.Run {
	t.Helper()
	runs, err := sess.RunLedger().List(context.Background())
	if err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	return runs
}

// The run must be a row in the ledger before the caller is told it started:
// a run that is not recorded cannot be scanned.
func TestTheRunIsRecordedBeforeItIsHandedOut(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello there", nil)
	if err != nil {
		t.Fatal(err)
	}

	runs := ledgerOf(t, sess)
	if len(runs) != 1 {
		t.Fatalf("want 1 row, got %d", len(runs))
	}
	if runs[0].RunID != turn.RunID {
		t.Fatalf("the row must carry the turn's run id: %q vs %q", runs[0].RunID, turn.RunID)
	}
	if runs[0].State != storage.RunStarting && runs[0].State != storage.RunRunning {
		t.Fatalf("want an open run, got %s", runs[0].State)
	}
	if runs[0].Epoch == 0 {
		t.Fatal("the run must hold a fence")
	}
	if runs[0].Owner == "" {
		t.Fatal("the row must name its owner")
	}
}

// The terminal row lands AFTER the archive. That ordering is the whole
// point of the row: a scan that sees "completed" knows the messages are
// already on disk.
func TestTheTerminalRowLandsAfterTheArchive(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello there", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)

	waitFor(t, func() bool {
		runs := ledgerOf(t, sess)
		return len(runs) == 1 && runs[0].State == storage.RunCompleted
	})

	// The archive must have landed by the time the row says so.
	if sess.Len() < 2 {
		t.Fatalf("the row says completed but the transcript holds %d messages", sess.Len())
	}
	msgs := sess.Messages(false)
	if message.AssistantText(msgs[1]) != "working" {
		t.Fatalf("the assistant message is not on disk: %#v", msgs)
	}
}

// A turn that never runs must close its own row. The loop may never reach
// the archive, and an open row would leave the session busy for ever.
func TestATurnThatNeverStartsClosesItsRow(t *testing.T) {
	// The agent cannot be built, so the loop never starts.
	b := &fakeBuilder{err: errors.New("boom")}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "never starts", nil)
	if err != nil {
		t.Fatal(err)
	}
	// ★ The build's own reason, not a start timeout: the turn knows why it
	//   never ran, and the row below carries the same reason.
	if err := turn.Wait(100 * time.Millisecond); !errors.Is(err, ErrAgentUnavailable) {
		t.Fatalf("Wait = %v, want ErrAgentUnavailable", err)
	}

	waitFor(t, func() bool {
		runs := ledgerOf(t, sess)
		return len(runs) == 1 && runs[0].State.IsTerminal()
	})
	runs := ledgerOf(t, sess)
	if runs[0].State != storage.RunFailed {
		t.Fatalf("want the run to have failed, got %s", runs[0].State)
	}
}

// The scan is the truth: it names the run that owns the session, and it
// stops naming it once the run is over.
func TestTheScanNamesTheActiveRun(t *testing.T) {
	release := make(chan struct{})
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			<-release
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello there", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	snap, err := rt.ScanRuns(ctx, sess.ID())
	if err != nil {
		t.Fatal(err)
	}
	if snap.ActiveRunID != turn.RunID {
		t.Fatalf("want the active run %q, got %q", turn.RunID, snap.ActiveRunID)
	}

	close(release)
	drain(t, turn)

	waitFor(t, func() bool {
		snap, err := rt.ScanRuns(ctx, sess.ID())
		return err == nil && snap.ActiveRunID == ""
	})
}

// A resume is a new run, and it names the run it answers.
func TestTheResumeNamesItsParentRun(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(interruptedEvent("int-1", "write the config"))
			emit(exitEvent())
		}}
	}}
	sm := newFakeSessionManager()
	store := newFakeCheckpoints()
	sm.ckpt = store
	rt := New(Config{
		AgentBuilder:   b,
		SessionManager: sm,
		Input:          InputConfig{},
		StartTimeout:   5 * time.Second,
	})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "write the config", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)
	waitFor(t, func() bool {
		p, _ := sess.PendingApproval()
		return p != nil && len(p.Interrupts) == 1
	})
	waitFor(t, func() bool {
		runs := ledgerOf(t, sess)
		return len(runs) == 1 && runs[0].State == storage.RunWaitingApproval
	})

	resume, err := rt.Resume(ctx, sess.ID(), "", []ApprovalDecision{{InterruptID: "int-1", Approved: true}})
	if err != nil {
		t.Fatal(err)
	}
	if resume.RunID == turn.RunID {
		t.Fatal("a resume must be a run of its own")
	}

	waitFor(t, func() bool { return len(ledgerOf(t, sess)) == 2 })
	runs := ledgerOf(t, sess)
	if runs[1].ParentRunID != turn.RunID {
		t.Fatalf("want the resume to name %q as its parent, got %q", turn.RunID, runs[1].ParentRunID)
	}
	if runs[1].Kind != contract.TurnResume {
		t.Fatalf("want a resume run, got %s", runs[1].Kind)
	}
}

// A run that failed must not read as "completed". The error the consumer
// saw is the run's ending.
func TestAFailedRunIsNotRecordedAsCompleted(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(errorEvent(errors.New("model_network: the provider timed out")))
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello there", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)

	waitFor(t, func() bool {
		runs := ledgerOf(t, sess)
		return len(runs) == 1 && runs[0].State.IsTerminal()
	})
	runs := ledgerOf(t, sess)
	if runs[0].State != storage.RunFailed {
		t.Fatalf("want the run to have failed, got %s", runs[0].State)
	}
	if runs[0].ErrorCode != "failed" {
		t.Fatalf("want the failure code, got %q", runs[0].ErrorCode)
	}
}

// A run the user stopped is "aborted", not "failed" — and it does not
// become the session's last error.
func TestAStoppedRunIsRecordedAsAborted(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(errorEvent(context.Canceled))
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello there", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)

	waitFor(t, func() bool {
		runs := ledgerOf(t, sess)
		return len(runs) == 1 && runs[0].State.IsTerminal()
	})
	runs := ledgerOf(t, sess)
	if runs[0].State != storage.RunAborted {
		t.Fatalf("want the run to have been aborted, got %s", runs[0].State)
	}

	status, ok := rt.StatusOf(ctx, sess.ID())
	if !ok {
		t.Fatal("want a status")
	}
	if status.LastError != nil {
		t.Fatalf("a stopped run must not become the session's last error: %v", status.LastError)
	}
}

// The session's status comes from the ledger, not from the volatile state:
// a live run has an id, and an idle session has none.
func TestStatusOfReportsTheActiveRunID(t *testing.T) {
	release := make(chan struct{})
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			<-release
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello there", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	status, ok := rt.StatusOf(ctx, sess.ID())
	if !ok {
		t.Fatal("want a status")
	}
	if status.Phase != PhaseRunning {
		t.Fatalf("want the session to be running, got %s", status.Phase)
	}
	if status.RunID != turn.RunID {
		t.Fatalf("want the run id %q, got %q", turn.RunID, status.RunID)
	}

	close(release)
	drain(t, turn)
	waitFor(t, func() bool {
		status, ok := rt.StatusOf(ctx, sess.ID())
		return ok && status.Phase == PhaseIdle && status.RunID == ""
	})
}

// ★ The row can arrive **after** the turn has already failed.
//
//	The claim takes the session's slot before it opens the row, and the turn
//	that replaces it can fail it in that window — so the failure and the row
//	meet in either order.
//
// ★ The order that matters is the one that loses: a run whose row is never
//
//	closed stays open in the ledger, and the session reads as busy for ever.
func TestATurnThatFailedBeforeItsRowArrivedStillClosesTheRow(t *testing.T) {
	turn := newTurnHandle("s1", contract.TurnResume)
	turn.fail(ErrSuperseded)

	row := newRunRow(nil, storage.Run{RunID: "r1", SessionID: "s1"}, nil)
	turn.setRow(row)

	if !row.isClosed() {
		t.Fatal("the row must be closed: the turn failed before the row arrived")
	}
	if row.run.State != storage.RunAborted {
		t.Fatalf("the row must carry the failure, got %q", row.run.State)
	}
	if row.run.ErrorCode != "superseded" {
		t.Fatalf("the row must name the failure, got %q", row.run.ErrorCode)
	}
}

// ★ The first ending wins: a turn that is superseded and then times out must
//
//	not change its story halfway through — the reader would see two endings
//	for the same run.
func TestTheFirstFailureIsTheRowsEnding(t *testing.T) {
	turn := newTurnHandle("s1", contract.TurnResume)
	turn.fail(ErrSuperseded)
	turn.fail(ErrStartTimeout)

	row := newRunRow(nil, storage.Run{RunID: "r1", SessionID: "s1"}, nil)
	turn.setRow(row)

	if row.run.ErrorCode != "superseded" {
		t.Fatalf("the row must keep the first ending, got %q", row.run.ErrorCode)
	}
}
