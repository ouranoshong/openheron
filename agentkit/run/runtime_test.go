package run

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"github.com/ouranoshong/openheron/approval"
	"github.com/ouranoshong/openheron/contract"
	"github.com/ouranoshong/openheron/message"
	"github.com/ouranoshong/openheron/storage"
)

func newTestRuntime(t *testing.T, b AgentBuilder) (*Runtime, *fakeSessionManager) {
	t.Helper()
	sm := newFakeSessionManager()
	rt := New(Config{
		AgentBuilder:   b,
		SessionManager: sm,
		Input:          InputConfig{},
		StartTimeout:   5 * time.Second,
	})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	return rt, sm
}

// mustSession creates a session with the fake agent's name.
//
// ★ The session's agent id and the agent's name are the same thing in
//
//	production — the builder names the agent after its id — and the
//	derivation compares the two to tell the root agent's compaction from
//	a sub-agent's. The fake has to keep that shape.
func mustSession(t *testing.T, sm *fakeSessionManager) *fakeSession {
	t.Helper()
	s, err := sm.Create(context.Background(), "fake")
	if err != nil {
		t.Fatal(err)
	}
	return s.(*fakeSession)
}

// A plain turn must persist the user message first, then the intermediates the
// consumer reports — that order is what keeps history consistent.
func TestSubmitArchivesInOrder(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)

	waitFor(t, func() bool { return sess.Len() >= 2 })

	msgs := sess.Messages(false)
	if message.UserText(msgs[0]) != "hello there" {
		t.Fatalf("user message must be persisted first, got %q", message.Text(msgs[0]))
	}
	if message.AssistantText(msgs[1]) != "working" {
		t.Fatalf("intermediate not archived: %#v", msgs[1])
	}
}

func TestUserMessageCarriesItsTimestamp(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)

	waitFor(t, func() bool { return sess.Len() >= 1 })

	msgs := sess.Messages(false)
	ts, ok := message.MessageTS(msgs[0])
	if !ok {
		t.Fatal("the user message must carry a timestamp")
	}
	if ts.IsZero() {
		t.Fatal("the user message's timestamp must not be zero")
	}
	if ts.After(time.Now().Add(time.Minute)) {
		t.Fatalf("the timestamp is in the future: %s", ts)
	}
}

func TestSubmitRejectsUnknownSession(t *testing.T) {
	rt, _ := newTestRuntime(t, &fakeBuilder{})
	if _, err := rt.Submit(context.Background(), "session-does-not-exist", "", "hi"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}
}

func TestSubmitRejectsEmptyQuery(t *testing.T) {
	rt, sm := newTestRuntime(t, &fakeBuilder{})
	sess := mustSession(t, sm)
	if _, err := rt.Submit(context.Background(), sess.ID(), "", ""); err == nil {
		t.Fatal("expected an error for an empty query")
	}
}

// PrepareAgent must hand the builder the **session's** root agent, not the
// configured main.  A switch that only reached the API would look applied in
// the sidebar while every turn still ran main.
func TestPrepareAgentPassesSessionAgentID(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)
	if err := sess.SetAgentID("worker"); err != nil {
		t.Fatal(err)
	}

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	drain(t, turn)

	b.mu.Lock()
	ids := append([]string(nil), b.agentIDs...)
	b.mu.Unlock()
	if len(ids) != 1 || ids[0] != "worker" {
		t.Fatalf("builder got agent ids %v, want [worker]", ids)
	}
}

type fakeCheckpoints struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newFakeCheckpoints() *fakeCheckpoints {
	return &fakeCheckpoints{data: map[string][]byte{}}
}

func (s *fakeCheckpoints) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.data[id]
	return b, ok, nil
}

func (s *fakeCheckpoints) Set(_ context.Context, id string, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[id] = b
	return nil
}

func (s *fakeCheckpoints) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, id)
	return nil
}

func TestStaleCheckpointDoesNotSwallowTheNextQuery(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
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

	if err := store.Set(ctx, sess.ID(), []byte("stale runner state")); err != nil {
		t.Fatal(err)
	}

	turn, err := rt.Submit(ctx, sess.ID(), "", "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("the query must not be swallowed by the stale checkpoint: %v", err)
	}
	drain(t, turn)
}

func TestInterruptLeavesACheckpointSoTheResumeIsNotRejected(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("about to write"))
			emit(interruptedEvent("agent:A;tool:call_1", "write_file needs approval"))
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

	turn, err := rt.Submit(ctx, sess.ID(), "", "write the config")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	drain(t, turn)
	waitFor(t, func() bool {
		p, _ := sess.PendingApproval()
		return p != nil
	})

	waitFor(t, func() bool {
		_, ok, _ := store.Get(ctx, sess.ID())
		return ok
	})

	if _, err := rt.Resume(ctx, sess.ID(), "", []ApprovalDecision{
		{InterruptID: "agent:A;tool:call_1", Approved: true},
	}); err != nil {
		t.Fatalf("the resume must not be rejected: %v", err)
	}
}

func TestResumeWithoutACheckpointIsRejected(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(exitEvent())
		}}
	}}
	sm := newFakeSessionManager()
	sm.ckpt = newFakeCheckpoints()
	rt := New(Config{
		AgentBuilder:   b,
		SessionManager: sm,
		Input:          InputConfig{},
		StartTimeout:   5 * time.Second,
	})
	t.Cleanup(func() { _ = rt.Shutdown(context.Background()) })
	ctx := context.Background()
	sess := mustSession(t, sm)

	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{InterruptID: "int-1"}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := rt.Resume(ctx, sess.ID(), "", []ApprovalDecision{{InterruptID: "int-1", Approved: true}}); !errors.Is(err, ErrNoCheckpoint) {
		t.Fatalf("want ErrNoCheckpoint, got %v", err)
	}
	pending, _ := sess.PendingApproval()
	if pending == nil || len(pending.Interrupts) != 1 {
		t.Fatalf("the pending approval must survive a rejected resume: %+v", pending)
	}
}

// An interrupt must record a persisted pending approval carrying the cursor,
// and the cursor must survive independently of the message count.
func TestInterruptRecordsPendingApproval(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("about to write"))
			emit(interruptedEvent("agent:A;tool:call_1", "write_file needs approval"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "write the config")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	drain(t, turn)

	waitFor(t, func() bool {
		p, _ := sess.PendingApproval()
		return p != nil
	})

	pending, _ := sess.PendingApproval()
	if len(pending.Interrupts) != 1 || pending.Interrupts[0].InterruptID != "agent:A;tool:call_1" {
		t.Fatalf("wrong interrupt id: %+v", pending.Interrupts)
	}
	// The interrupt adds no message: the pending set is a separate fact from
	// the transcript.
	if sess.Len() != 2 {
		t.Fatalf("expected 2 messages, got %d", sess.Len())
	}
}

func TestMultiplePendingApprovalsAreAnsweredAsAGroup(t *testing.T) {
	ids := []string{"agent:A;tool:call_1", "agent:B;tool:call_2"}
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("about to write"))
			emit(interruptedEvents(ids))
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "write the config")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn)

	waitFor(t, func() bool {
		p, _ := sess.PendingApproval()
		return p != nil && len(p.Interrupts) == 2
	})

	pending, _ := sess.PendingApproval()
	if len(pending.Interrupts) != 2 {
		t.Fatalf("pending must hold 2 interrupts, got %d", len(pending.Interrupts))
	}
	for i, want := range ids {
		if pending.Interrupts[i].InterruptID != want {
			t.Fatalf("interrupt %d = %q, want %q", i, pending.Interrupts[i].InterruptID, want)
		}
		if !pending.Has(want) {
			t.Fatalf("pending must contain %q", want)
		}
	}

	if _, err := rt.Resume(ctx, sess.ID(), "", []ApprovalDecision{
		{InterruptID: ids[0], Approved: true},
		{InterruptID: ids[1], Approved: true},
	}); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	after, _ := sess.PendingApproval()
	if after != nil && len(after.Interrupts) != 0 {
		t.Fatalf("the whole group must be consumed, got %+v", after.Interrupts)
	}

	if _, err := rt.Resume(ctx, sess.ID(), "", []ApprovalDecision{
		{InterruptID: ids[0], Approved: true},
	}); !errors.Is(err, ErrNoPendingInterrupt) {
		t.Fatalf("a replayed approve must be refused, got %v", err)
	}
}

func TestResumeRefusedWithoutPendingApproval(t *testing.T) {
	rt, sm := newTestRuntime(t, &fakeBuilder{})
	sess := mustSession(t, sm)

	_, err := rt.Resume(context.Background(), sess.ID(),
		"", []ApprovalDecision{{InterruptID: "int-1", Approved: true}})
	if !errors.Is(err, ErrNoPendingInterrupt) {
		t.Fatalf("want ErrNoPendingInterrupt, got %v", err)
	}
}

// The approval must be consumed exactly once: a replayed approve must not
// reach the framework with a stale interrupt id.
func TestResumeConsumesTheApproval(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("resumed"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{InterruptID: "int-42"}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := rt.Resume(ctx, sess.ID(),
		"", []ApprovalDecision{{InterruptID: "int-42", Approved: true, Reason: "ok"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := rt.Resume(ctx, sess.ID(),
		"", []ApprovalDecision{{InterruptID: "int-42", Approved: true}}); !errors.Is(err, ErrNoPendingInterrupt) {
		t.Fatalf("a replayed approve must be refused, got %v", err)
	}
	if p, _ := sess.PendingApproval(); p != nil {
		t.Fatalf("approval was not consumed: %#v", p)
	}
}

// PrepareAgent is deliberately not asserted on the resume path: a resumed turn
// restores its agent from the checkpoint rather than building a fresh one.
func TestApprovalResultCarriesTheDecision(t *testing.T) {
	got, ok := approvalResult(ApprovalDecision{Approved: false, Reason: "unsafe"}).(*approval.Decision)
	if !ok {
		t.Fatalf("approvalResult returned %T", got)
	}
	if got.Approved || got.Reason != "unsafe" {
		t.Fatalf("decision not carried through: %+v", got)
	}
}

func TestApprovalPayloadIsTheSharedDecisionType(t *testing.T) {
	payload := approvalResult(ApprovalDecision{Approved: true})
	if _, ok := payload.(*approval.Decision); !ok {
		t.Fatalf("approval payload is %T — the gate reads *approval.Decision, "+
			"and GetResumeContext is a concrete type assertion", payload)
	}
}

// A second Submit on a busy session must be REFUSED, not preempt: the
// running turn is another surface's, and taking it over would destroy a
// turn the user cannot see. The running turn must survive the refusal.
func TestASecondSubmitIsRefusedWhileATurnRuns(t *testing.T) {
	started := make(chan struct{}, 4)
	b := &fakeBuilder{agent: func(call int, kind contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			started <- struct{}{}
			// Every turn blocks until the framework cancels it.
			<-ctx.Done()
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	first, err := rt.Submit(ctx, sess.ID(), "", "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(5 * time.Second); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	<-started
	firstDone := drainBounded(t, first)

	// The second submit must be refused.
	if _, err := rt.Submit(ctx, sess.ID(), "", "second"); !errors.Is(err, ErrSessionBusy) {
		t.Fatalf("a second Submit must be refused with ErrSessionBusy, got %v", err)
	}

	// The refusal must not have disturbed the running turn.
	select {
	case <-firstDone:
		t.Fatal("the refused submit must not release the running turn")
	case <-time.After(300 * time.Millisecond):
	}

	// And the refused query must not be in the transcript.
	for _, m := range sess.Messages(false) {
		if q := message.UserText(m); q == "second" {
			t.Fatalf("the refused query was persisted anyway: %q", q)
		}
	}
}

// drainBounded waits for the run to finish, in the background.
//
// ★ The run consumes its own events, so there is nothing to drain: the
//
//	test waits for the run's archive.
func drainBounded(t *testing.T, turn *Turn) chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		turn.WaitArchived()
	}()
	return done
}

func TestAbortStopsTheTurn(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			select {
			case <-ctx.Done():
			case <-time.After(10 * time.Second):
			}
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "long running")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	// ★ The run consumes its own events; the test only waits for it.
	go turn.WaitArchived()

	if err := rt.Abort(ctx, sess.ID()); err != nil {
		t.Fatalf("Abort: %v", err)
	}
}

func TestCleanupIsIdempotentForUnknownSession(t *testing.T) {
	rt, _ := newTestRuntime(t, &fakeBuilder{})
	if err := rt.Cleanup(context.Background(), "session-missing"); err != nil {
		t.Fatalf("cleanup of an unknown session must succeed, got %v", err)
	}
}

func TestBuilderFailureSurfacesAsLoopError(t *testing.T) {
	b := &fakeBuilder{err: errBoom}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "", "hi")
	if err != nil {
		t.Fatal(err)
	}
	// ★ The build's reason must reach the waiter, in the builder's own
	//   words. It used to arrive as a start timeout, which named a symptom
	//   and hid the file and the model behind it.
	waitErr := turn.Wait(2 * time.Second)
	if waitErr == nil {
		t.Fatal("a turn whose agent cannot be built must not report success")
	}
	if !errors.Is(waitErr, ErrAgentUnavailable) {
		t.Fatalf("wait error = %v, want ErrAgentUnavailable", waitErr)
	}
	if !errors.Is(waitErr, errBoom) {
		t.Fatalf("the builder's own reason must survive, got %v", waitErr)
	}
}

func TestStatusReportsKnownSessions(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			<-ctx.Done()
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	if len(rt.Status()) != 0 {
		t.Fatalf("no session should be active yet: %v", rt.Status())
	}

	turn, err := rt.Submit(ctx, sess.ID(), "", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	// ★ The run consumes its own events; the test only waits for it.
	go turn.WaitArchived()

	statuses := rt.Status()
	if len(statuses) != 1 || statuses[0].SessionID != sess.ID() {
		t.Fatalf("unexpected status projection: %+v", statuses)
	}
	if statuses[0].Phase != PhaseRunning {
		t.Fatalf("want running, got %v", statuses[0].Phase)
	}
}

// ---- helpers ----

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within the deadline")
}

var _ = strings.TrimSpace

func TestMakeGenResumeTargetsEveryDecision(t *testing.T) {
	rt := &Runtime{}
	gen := rt.makeGenResume(nil)

	res, err := gen(context.Background(), nil, nil, nil, []loopItem{{
		Approvals: map[string]any{
			"ic-1": &approval.Decision{Approved: true},
			"ic-2": &approval.Decision{Approved: false, Reason: "no"},
			"ic-3": &approval.Decision{Approved: true},
		},
	}})
	if err != nil {
		t.Fatalf("makeGenResume: %v", err)
	}
	if res == nil || res.ResumeParams == nil {
		t.Fatal("no resume params")
	}
	if len(res.ResumeParams.Targets) != 3 {
		t.Fatalf("targets = %d, want 3 (%+v)", len(res.ResumeParams.Targets), res.ResumeParams.Targets)
	}
	for _, id := range []string{"ic-1", "ic-2", "ic-3"} {
		if _, ok := res.ResumeParams.Targets[id]; !ok {
			t.Fatalf("target %q missing: %+v", id, res.ResumeParams.Targets)
		}
	}
}

func TestMakeGenResumeRefusesAnEmptyDecisionSet(t *testing.T) {
	rt := &Runtime{}
	gen := rt.makeGenResume(nil)

	if _, err := gen(context.Background(), nil, nil, nil, []loopItem{{}}); !errors.Is(err, ErrNoPendingInterrupt) {
		t.Fatalf("want ErrNoPendingInterrupt, got %v", err)
	}
}

func TestResumeRecordsTheApprovalDecision(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("resumed"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{
			InterruptID: "int-7",
			Detail: &contract.InterruptDetail{
				ToolName:         "write_file",
				RequestedByAgent: "main",
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := rt.Resume(ctx, sess.ID(),
		"", []ApprovalDecision{{InterruptID: "int-7", Approved: true, Reason: "ok"}}); err != nil {
		t.Fatal(err)
	}

	recs := sess.Decisions()
	if len(recs) != 1 {
		t.Fatalf("decisions = %d, want 1", len(recs))
	}
	rec := recs[0]
	if rec.InterruptID != "int-7" {
		t.Errorf("interruptId = %q", rec.InterruptID)
	}
	if rec.ToolName != "write_file" {
		t.Errorf("toolName = %q, want write_file", rec.ToolName)
	}
	if !rec.Approved {
		t.Error("approved = false, want true")
	}
	if rec.Reason != "ok" {
		t.Errorf("reason = %q", rec.Reason)
	}
	if rec.RequestedByAgent != "main" {
		t.Errorf("requestedByAgent = %q", rec.RequestedByAgent)
	}
	if rec.DecidedAt.IsZero() {
		t.Error("decidedAt must be stamped by the server")
	}
}

func TestDecisionsAreAppendOnly(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	for _, id := range []string{"int-1", "int-2"} {
		if err := sess.SetPendingApproval(&storage.PendingApproval{
			Interrupts: []contract.PendingInterrupt{{InterruptID: id}},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := rt.Resume(ctx, sess.ID(),
			"", []ApprovalDecision{{InterruptID: id, Approved: true}}); err != nil {
			t.Fatal(err)
		}
	}

	recs := sess.Decisions()
	if len(recs) != 2 {
		t.Fatalf("decisions = %d, want 2", len(recs))
	}
	if recs[0].InterruptID != "int-1" || recs[1].InterruptID != "int-2" {
		t.Fatalf("the audit must keep the order: %+v", recs)
	}
}

func TestAFailedTurnReportsWhyItNeverStarted(t *testing.T) {
	turn := newTurnHandle("session-x", contract.TurnQuery)
	turn.fail(ErrNoCheckpoint)

	if err := turn.Wait(time.Second); !errors.Is(err, ErrNoCheckpoint) {
		t.Fatalf("Wait must report the failure reason, got %v", err)
	}
	if turn.Events() != nil {
		t.Fatal("a dead turn has no event stream — reading it must not be possible")
	}
}
