package run

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/storage"
)

// operationsOf reads a session's operation log.
func operationsOf(t *testing.T, sess *fakeSession) []storage.Operation {
	t.Helper()
	rows, err := sess.Operations().List(context.Background())
	if err != nil {
		t.Fatalf("read the operations: %v", err)
	}
	return rows
}

// kindsOf lists the kinds of a session's operations, in order.
func kindsOf(ops []storage.Operation) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, string(op.Kind))
	}
	return out
}

// hasKind reports whether the log carries a line of that kind.
func hasKind(ops []storage.Operation, kind storage.OperationKind) bool {
	for _, op := range ops {
		if op.Kind == kind {
			return true
		}
	}
	return false
}

// countKind reports how many lines of that kind the log carries.
func countKind(ops []storage.Operation, kind storage.OperationKind) int {
	n := 0
	for _, op := range ops {
		if op.Kind == kind {
			n++
		}
	}
	return n
}

// findKind returns the first line of that kind.
func findKind(ops []storage.Operation, kind storage.OperationKind) (storage.Operation, bool) {
	for _, op := range ops {
		if op.Kind == kind {
			return op, true
		}
	}
	return storage.Operation{}, false
}

// ★ The round's start and its end are two lines, and the end names the
//
//	ending. ★ The end is written where every ending passes through — the
//	abort, the archive and the loop's own exit — so one place says it.
func TestTheRoundIsTwoLinesInTheTimeline(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	sess := mustSession(t, sm)

	if _, err := rt.Submit(context.Background(), sess.ID(), "", "go", nil); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitFor(t, func() bool {
		return hasKind(operationsOf(t, sess), storage.OpRunFinished)
	})

	ops := operationsOf(t, sess)
	started := ops[0]
	if started.Kind != storage.OpRunStarted {
		t.Fatalf("want the round's start first, got %q", started.Kind)
	}
	if started.Actor != storage.ActorSystem {
		t.Fatalf("want the system as the actor, got %q", started.Actor)
	}
	if started.RunID == "" {
		t.Fatal("the line names the round")
	}
	if started.TurnKind != contract.TurnQuery.String() {
		t.Fatalf("want the round's kind, got %q", started.TurnKind)
	}

	finished := ops[len(ops)-1]
	if finished.Kind != storage.OpRunFinished {
		t.Fatalf("want the round's end last, got %q", finished.Kind)
	}
	if finished.RunID != started.RunID {
		t.Fatalf("want the same round, got %q and %q", started.RunID, finished.RunID)
	}
	if finished.State != storage.RunCompleted.String() {
		t.Fatalf("want the ending named, got %q", finished.State)
	}
}

// ★ The approval's two moments are two lines: the question and the answer.
//
//	The question is a system fact, the answer is a person's — and the line
//	that carries the answer says so, whoever wrote it.
func TestTheApprovalIsTwoLinesInTheTimeline(t *testing.T) {
	rt, sess := announceRuntime(t)
	ctx := context.Background()

	if _, err := rt.Submit(ctx, sess.ID(), "", "write something", nil); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitFor(t, func() bool {
		return hasKind(operationsOf(t, sess), storage.OpApprovalRequested)
	})

	ops := operationsOf(t, sess)
	requested, ok := findKind(ops, storage.OpApprovalRequested)
	if !ok {
		t.Fatalf("want the question in the log, got %v", kindsOf(ops))
	}
	if requested.InterruptID != "int-1" {
		t.Fatalf("want the interrupt's id, got %q", requested.InterruptID)
	}
	if requested.Actor != storage.ActorSystem {
		t.Fatalf("want the system as the actor, got %q", requested.Actor)
	}
	// ★ The question comes before the round's end: the round raised the
	//   question and then closed, and that is the order it happened in.
	finished, ok := findKind(ops, storage.OpRunFinished)
	if !ok {
		t.Fatalf("want the round's end in the log, got %v", kindsOf(ops))
	}
	if finished.Seq <= requested.Seq {
		t.Fatalf("the question comes first: %d then %d", requested.Seq, finished.Seq)
	}

	if _, err := rt.Resume(ctx, sess.ID(), "", []ApprovalDecision{
		{InterruptID: "int-1", Approved: true},
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return hasKind(operationsOf(t, sess), storage.OpApprovalResolved)
	})

	ops = operationsOf(t, sess)
	resolved, ok := findKind(ops, storage.OpApprovalResolved)
	if !ok {
		t.Fatalf("want the answer in the log, got %v", kindsOf(ops))
	}
	if resolved.Actor != storage.ActorUser {
		t.Fatalf("want the person as the actor, got %q", resolved.Actor)
	}
	if resolved.Approved == nil {
		t.Fatal("the answer carries the decision")
	}
	if !*resolved.Approved {
		t.Fatal("the approval was granted")
	}
}

// The timeline keeps the order it was given: the question comes before the
// answer, and both come after the round that raised the question.
func TestTheTimelineKeepsTheOrderOfTheFacts(t *testing.T) {
	rt, sess := announceRuntime(t)
	ctx := context.Background()

	if _, err := rt.Submit(ctx, sess.ID(), "", "write something", nil); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitFor(t, func() bool {
		return hasKind(operationsOf(t, sess), storage.OpApprovalRequested)
	})
	if _, err := rt.Resume(ctx, sess.ID(), "", []ApprovalDecision{
		{InterruptID: "int-1", Approved: true},
	}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		return countKind(operationsOf(t, sess), storage.OpRunFinished) == 2
	})

	ops := operationsOf(t, sess)
	want := []string{
		string(storage.OpRunStarted),
		string(storage.OpApprovalRequested),
		string(storage.OpRunFinished),
		string(storage.OpRunStarted),
		string(storage.OpApprovalResolved),
		string(storage.OpRunFinished),
	}
	got := kindsOf(ops)
	if len(got) != len(want) {
		t.Fatalf("want %d lines %v, got %d %v", len(want), want, len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d: want %q, got %q (all: %v)", i, want[i], got[i], got)
		}
	}
	for i, op := range ops {
		if op.Seq != uint64(i+1) {
			t.Fatalf("line %d carries seq %d: the file's order and the numbers' order disagree",
				i, op.Seq)
		}
	}
}

// The timeline must not make a round fail: a log that cannot be written is a
// gap in the record, not a broken run.
func TestTheRunSurvivesALogThatCannotBeWritten(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	sess := mustSession(t, sm)
	sess.operations = &failingOperationLog{}

	if _, err := rt.Submit(context.Background(), sess.ID(), "", "go", nil); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitFor(t, func() bool {
		runs, err := sess.RunLedger().List(context.Background())
		return err == nil && len(runs) == 1 && runs[0].State == storage.RunCompleted
	})
}

// failingOperationLog is a log that cannot be written.
type failingOperationLog struct{}

func (l *failingOperationLog) Append(context.Context, storage.Operation) error {
	return errCannotWrite
}

func (l *failingOperationLog) List(context.Context) ([]storage.Operation, error) {
	return nil, nil
}

// errCannotWrite is the failure the fake log reports.
var errCannotWrite = errors.New("the log cannot be written")
