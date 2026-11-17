package run

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
)

// ★ The row records the mode the round ran under.
//
// The decision log only holds what a human decided — so a round that ran
// without asking leaves no trace there. The ledger row is where that fact
// lives: afterwards, a scan can answer "was anybody watching".
func TestTheRowRecordsTheModeTheRoundRanUnder(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	if err := sess.SetAuthorizationMode(contract.AuthzTrusted); err != nil {
		t.Fatal(err)
	}

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

	runs := ledgerOf(t, sess)
	if runs[0].AuthorizationMode != contract.AuthzTrusted {
		t.Fatalf("the row must record the mode, got %q", runs[0].AuthorizationMode)
	}
}

// ★ A session that never chose a mode runs under the default — and the row
//
//	says so, so "unattended" and "quiet" are not the same reading.
func TestTheRowRecordsTheDefaultMode(t *testing.T) {
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

	runs := ledgerOf(t, sess)
	if runs[0].AuthorizationMode != contract.AuthzDefault {
		t.Fatalf("the row must record the default, got %q", runs[0].AuthorizationMode)
	}
}

// The row is written once, when the round starts: the round keeps the mode
// it began with, and a row that followed the session's mode would change
// its story halfway through.
func TestTheRowWriterRecordsTheModeOnce(t *testing.T) {
	row := newRunRow(nil, nil, storage.Run{RunID: "r1", SessionID: "s1"}, nil)
	row.noteAuthorizationMode(contract.AuthzFull)

	if row.run.AuthorizationMode != contract.AuthzFull {
		t.Fatalf("mode = %q, want full", row.run.AuthorizationMode)
	}
	// ★ A second note does not overwrite the first: the round's story is
	//	written once.
	row.noteAuthorizationMode(contract.AuthzDefault)
	if row.run.AuthorizationMode != contract.AuthzFull {
		t.Fatalf("the mode must not change halfway through, got %q", row.run.AuthorizationMode)
	}
}
