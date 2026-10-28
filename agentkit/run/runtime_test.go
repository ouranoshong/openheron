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

	"openheron/approval"
	"openheron/contract"
	"openheron/message"
	"openheron/storage"
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

func mustSession(t *testing.T, sm *fakeSessionManager) *fakeSession {
	t.Helper()
	s, err := sm.Create(context.Background(), "")
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

	turn, err := rt.Submit(ctx, sess.ID(), "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn, Outcome{
		Intermediates: []*schema.Message{message.NewAssistant("working", nil)},
	})

	waitFor(t, func() bool { return sess.Len() >= 2 })

	msgs := sess.Messages()
	if message.UserText(msgs[0]) != "hello there" {
		t.Fatalf("user message must be persisted first, got %q", message.Text(msgs[0]))
	}
	if message.AssistantText(msgs[1]) != "working" {
		t.Fatalf("intermediate not archived: %#v", msgs[1])
	}
}

// ★ user 消息在**提交时**打上产生时刻。
//
// 它是「谁、什么时候说的」这对出处标记的另一半（归属是哪个 agent 说的，
// 这里是用户说的）。中间消息的时刻由 transport 在产出时打（见那边）。
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

	turn, err := rt.Submit(ctx, sess.ID(), "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn, Outcome{Intermediates: []*schema.Message{message.NewAssistant("working", nil)}})

	waitFor(t, func() bool { return sess.Len() >= 1 })

	msgs := sess.Messages()
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
	if _, err := rt.Submit(context.Background(), "session-does-not-exist", "hi"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("want ErrSessionNotFound, got %v", err)
	}
}

func TestSubmitRejectsEmptyQuery(t *testing.T) {
	rt, sm := newTestRuntime(t, &fakeBuilder{})
	sess := mustSession(t, sm)
	if _, err := rt.Submit(context.Background(), sess.ID(), ""); err == nil {
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

	turn, err := rt.Submit(ctx, sess.ID(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	drain(t, turn, Outcome{})

	b.mu.Lock()
	ids := append([]string(nil), b.agentIDs...)
	b.mu.Unlock()
	if len(ids) != 1 || ids[0] != "worker" {
		t.Fatalf("builder got agent ids %v, want [worker]", ids)
	}
}

// fakeCheckpoints 是一份内存里的 checkpoint 存储。
//
// ★ 不用 [FileStore]：它要求 checkpoint id 是规范 UUID，而测试里的会话 id
//
//	是 "session-1"。这里要验的是**运行时怎么处理 checkpoint**，不是文件
//	布局。
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

// ★ 上一轮留下的 checkpoint 不该吞掉新的提问。
//
//	框架的契约（eino adk/turn_loop.go:569）：checkpoint 里有 runner 状态
//	（被打断、或在轮次中途被取消）时，下一次 Run 一定走**恢复**路径 —— 而
//	恢复路径只恢复那个被打断的 agent，不会跑新的提问。于是提问被吞掉：
//	轮次永远不 ready，接口只能等到超时（turn_start_timeout）。
//
//	这条用例用「上个 checkpoint 还在」的现场来钉它。
func TestStaleCheckpointDoesNotSwallowTheNextQuery(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("working"))
			emit(exitEvent())
		}}
	}}
	sm := newFakeSessionManager()
	// ★ checkpoint 的存取器由**会话**提供（storage.Session.GetCheckPointStore），
	//	所以它挂在仓库上，而不是 Runtime 的配置里。
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

	// 上一轮留下的 checkpoint（中途被取消时框架写下的那一份）。
	if err := store.Set(ctx, sess.ID(), []byte("stale runner state")); err != nil {
		t.Fatal(err)
	}

	turn, err := rt.Submit(ctx, sess.ID(), "hello there")
	if err != nil {
		t.Fatal(err)
	}
	// ★ 提问必须真的跑起来 —— 轮次要 ready。
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("the query must not be swallowed by the stale checkpoint: %v", err)
	}
	drain(t, turn, Outcome{
		Intermediates: []*schema.Message{message.NewAssistant("working", nil)},
	})
}

// ★ 中断之后 checkpoint 还在 ⇒ 答复**不该**被「没有 checkpoint」拒掉。
//
//	这条钉的是另一头：恢复路径要读 checkpoint（eino adk/turn_loop.go:1861），
//	所以「有没有 checkpoint」是答复的前置条件 —— 但只有在真的没有时才拒。
//
//	（框架在中断/取消落地时会写 checkpoint，见 adk/turn_loop.go:2010。）
func TestInterruptLeavesACheckpointSoTheResumeIsNotRejected(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("about to write"))
			emit(interruptedEvent("agent:A;tool:call_1", "write_file needs approval"))
			emit(exitEvent())
		}}
	}}
	sm := newFakeSessionManager()
	// ★ checkpoint 的存取器由**会话**提供（storage.Session.GetCheckPointStore），
	//	所以它挂在仓库上，而不是 Runtime 的配置里。
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

	turn, err := rt.Submit(ctx, sess.ID(), "write the config")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	drain(t, turn, Outcome{
		Intermediates: []*schema.Message{message.NewAssistant("about to write", nil)},
		Interrupts:    []contract.PendingInterrupt{{InterruptID: "agent:A;tool:call_1"}},
	})
	waitFor(t, func() bool {
		p, _ := sess.PendingApproval()
		return p != nil
	})

	// ★ 中断必须留下 checkpoint —— 否则答复会被「没有 checkpoint」拒掉。
	waitFor(t, func() bool {
		_, ok, _ := store.Get(ctx, sess.ID())
		return ok
	})

	if _, err := rt.Resume(ctx, sess.ID(), []ApprovalDecision{
		{InterruptID: "agent:A;tool:call_1", Approved: true},
	}); err != nil {
		t.Fatalf("the resume must not be rejected: %v", err)
	}
}

// ★ 答复审批时 checkpoint 没了 ⇒ **干脆拒绝**，而不是让轮次起来又等超时。
//
//	框架的恢复路径要读 checkpoint（eino adk/turn_loop.go:1861）；没有它，
//	恢复无从谈起。待批记录也不能被取走 —— 取走了用户就没法答第二遍。
func TestResumeWithoutACheckpointIsRejected(t *testing.T) {
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(exitEvent())
		}}
	}}
	sm := newFakeSessionManager()
	// ★ 这个仓库**做** checkpoint（存取器在会话上），只是这一个会话还没写。
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

	// 有待批记录，但没有 checkpoint。
	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: []contract.PendingInterrupt{{InterruptID: "int-1"}},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := rt.Resume(ctx, sess.ID(), []ApprovalDecision{{InterruptID: "int-1", Approved: true}}); !errors.Is(err, ErrNoCheckpoint) {
		t.Fatalf("want ErrNoCheckpoint, got %v", err)
	}
	// ★ 待批记录必须还在：用户还能重试。
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

	turn, err := rt.Submit(ctx, sess.ID(), "write the config")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	drain(t, turn, Outcome{
		Intermediates: []*schema.Message{message.NewAssistant("about to write", nil)},
		Interrupts:    []contract.PendingInterrupt{{InterruptID: "agent:A;tool:call_1"}},
	})

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

// ★ 一轮里**多个**待批要一次答完整组。
//
// 并行委派时多个子 agent 同时被拦下，它们的待批落在**同一个**中断事件里
// （框架收成 composite，再摊平成一组 root cause）—— 而每个都是**独立**
// 的决定，各带各的 id。
//
// 这一条钉的是「成组」语义：这一组**一起**记、**一起**取、**一起**消费。
// 逐个发的话，答完第一个，其余的 id 立刻作废（不在 Targets 里的被中断
// 组件会带着**新 id** 重新中断，eino adk/runner.go:133）。
func TestMultiplePendingApprovalsAreAnsweredAsAGroup(t *testing.T) {
	ids := []string{"agent:A;tool:call_1", "agent:B;tool:call_2"}
	// ★ 假 agent **不**自己中断 —— 待批由 drain 注入（与既有用例同一模式）。
	//   「一个事件里带多个待批」那一层由 transport 的用例钉（框架把一轮
	//   里所有被拦下的工具收成**一个** composite 事件）。
	b := &fakeBuilder{agent: func(int, contract.TurnKind) adk.TypedAgent[*schema.Message] {
		return &fakeAgent{script: func(ctx context.Context, emit func(*adk.TypedAgentEvent[*schema.Message]) bool) {
			emit(textEvent("about to write"))
			emit(exitEvent())
		}}
	}}
	rt, sm := newTestRuntime(t, b)
	ctx := context.Background()
	sess := mustSession(t, sm)

	turn, err := rt.Submit(ctx, sess.ID(), "write the config")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	drain(t, turn, Outcome{
		Intermediates: []*schema.Message{message.NewAssistant("about to write", nil)},
		Interrupts: []contract.PendingInterrupt{
			{InterruptID: ids[0]},
			{InterruptID: ids[1]},
		},
	})

	waitFor(t, func() bool {
		p, _ := sess.PendingApproval()
		return p != nil && len(p.Interrupts) == 2
	})

	// ★ 两个都记下来了，各带各的 id —— 不是只记第一个。
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

	// ★ 一次答完整组：两个决定都在**同一个**请求里。
	//
	//   ★ 不对 resume turn 调 Wait —— 假循环的 ready 握手要靠调用方去消费
	//     事件才会完成（既有用例也是这个形状）。
	if _, err := rt.Resume(ctx, sess.ID(), []ApprovalDecision{
		{InterruptID: ids[0], Approved: true},
		{InterruptID: ids[1], Approved: true},
	}); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	// 整组被一次消费掉 —— 不是一个一个。
	after, _ := sess.PendingApproval()
	if after != nil && len(after.Interrupts) != 0 {
		t.Fatalf("the whole group must be consumed, got %+v", after.Interrupts)
	}

	// ★ 不能重放：组已经没了，再答一次必须是「没有待批」。
	if _, err := rt.Resume(ctx, sess.ID(), []ApprovalDecision{
		{InterruptID: ids[0], Approved: true},
	}); !errors.Is(err, ErrNoPendingInterrupt) {
		t.Fatalf("a replayed approve must be refused, got %v", err)
	}
}

func TestResumeRefusedWithoutPendingApproval(t *testing.T) {
	rt, sm := newTestRuntime(t, &fakeBuilder{})
	sess := mustSession(t, sm)

	_, err := rt.Resume(context.Background(), sess.ID(),
		[]ApprovalDecision{{InterruptID: "int-1", Approved: true}})
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
		[]ApprovalDecision{{InterruptID: "int-42", Approved: true, Reason: "ok"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := rt.Resume(ctx, sess.ID(),
		[]ApprovalDecision{{InterruptID: "int-42", Approved: true}}); !errors.Is(err, ErrNoPendingInterrupt) {
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

// ★ 决定载荷的类型必须是 [approval.Decision] —— **闸门读的那个类型**。
//
//	openheronrt 与 agent 不能互相 import（依赖铁律），所以这个「同一个类型」
//	只能靠**共享叶子包**保证：两边都写 `approval.Decision`，谁改成自己的
//	结构，编译器立刻报错。
//
//	这条曾经不成立：openheronrt 送 `*openheronrt.ApprovalResult`、闸门读
//	`*agent.ApprovalResult`，两者结构相同、类型不同 ⇒ eino 的
//	GetResumeContext 具体类型断言必然失败 ⇒ 闸门拿不到决定 ⇒ 而闸门把
//	「拿不到决定」当成「照常执行」⇒ **每一次审批都被忽略**。
//
//	这条断言本身是「同包内」的，钉不住跨包那一半 —— 跨包那半由
//	`agentkit/build` 包的 `TestApprovalGateRefusesTheToolWhenTheDecisionTypeIsWrong`
//	从行为上钉住。
func TestApprovalPayloadIsTheSharedDecisionType(t *testing.T) {
	payload := approvalResult(ApprovalDecision{Approved: true})
	if _, ok := payload.(*approval.Decision); !ok {
		t.Fatalf("approval payload is %T — the gate reads *approval.Decision, "+
			"and GetResumeContext is a concrete type assertion", payload)
	}
}

// A newer Submit must supersede the in-flight turn: the outgoing turn's
// handshake is released with ErrSuperseded, and history stays ordered.
func TestPreemptionSupersedesPreviousTurn(t *testing.T) {
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

	first, err := rt.Submit(ctx, sess.ID(), "first")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(5 * time.Second); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	<-started
	firstDone := drainBounded(t, first)

	if _, err := rt.Submit(ctx, sess.ID(), "second"); err != nil {
		t.Fatal(err)
	}
	<-started

	select {
	case <-firstDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the superseded turn was never released")
	}

	waitFor(t, func() bool {
		n := 0
		for _, m := range sess.Messages() {
			if message.UserText(m) != "" {
				n++
			}
		}
		return n >= 2
	})

	// Both queries must be recorded, and in the order they were submitted.
	var queries []string
	for _, m := range sess.Messages() {
		if q := message.UserText(m); q != "" {
			queries = append(queries, q)
		}
	}
	if len(queries) < 2 || queries[0] != "first" || queries[1] != "second" {
		t.Fatalf("history out of order under preemption: %v", queries)
	}
}

func drainBounded(t *testing.T, turn *Turn) chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		events := turn.Events()
		for {
			if _, ok := events.Next(); !ok {
				break
			}
		}
		turn.Complete(Outcome{})
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

	turn, err := rt.Submit(ctx, sess.ID(), "long running")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	go func() {
		events := turn.Events()
		for {
			if _, ok := events.Next(); !ok {
				break
			}
		}
		turn.Complete(Outcome{})
	}()

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

	turn, err := rt.Submit(ctx, sess.ID(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	// The turn may never become ready when PrepareAgent fails; Wait must not
	// hang forever, it must time out or report the failure.
	waitErr := turn.Wait(2 * time.Second)
	if waitErr != nil && !errors.Is(waitErr, ErrStartTimeout) {
		t.Fatalf("unexpected wait error: %v", waitErr)
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

	turn, err := rt.Submit(ctx, sess.ID(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if err := turn.Wait(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	go func() {
		events := turn.Events()
		for {
			if _, ok := events.Next(); !ok {
				break
			}
		}
		turn.Complete(Outcome{})
	}()

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

// ★ 一次答完整组：所有决定都要进框架的 Targets。
//
//	框架的契约（eino adk/runner.go:133）：不在 Targets 里的被中断组件会
//	**重新中断自己**，而重新中断拿到的是**新的 id**（框架每次都发新的
//	uuid）。所以只 target 一个的话，其余的 id 就全废了 —— 客户端手里那批
//	旧 id 再也答不了，症状是「interruptId does not match the pending
//	approval」。
//
//	这一条钉的就是那个缺陷：原先 makeGenResume 只取**第一个**带决定的
//	item，塞进 Targets 的也只有一个。
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

// 没有决定时必须报错，而不是发一个空的 Targets —— 那会让框架把**所有**
// 被中断组件都当成「没被答复」，全部重新中断。
func TestMakeGenResumeRefusesAnEmptyDecisionSet(t *testing.T) {
	rt := &Runtime{}
	gen := rt.makeGenResume(nil)

	if _, err := gen(context.Background(), nil, nil, nil, []loopItem{{}}); !errors.Is(err, ErrNoPendingInterrupt) {
		t.Fatalf("want ErrNoPendingInterrupt, got %v", err)
	}
}

// 答复之后，待批记录就**取走了** —— 那次决定必须留下审计。
//
// ★ 这是「上周那次到底批没批」唯一的持久痕迹：
//
//	不留审计，决定在答复的那一刻就再也找不回来了。
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
		[]ApprovalDecision{{InterruptID: "int-7", Approved: true, Reason: "ok"}}); err != nil {
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

// 审计是**只追加**的 —— 后来的答复不能改写先前那一条。
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
			[]ApprovalDecision{{InterruptID: id, Approved: true}}); err != nil {
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

// ★ 判死的轮次：Wait 必须**报出原因**。
//
//	判死的轮次**没有事件流**（事件流是 makeOnAgentEvents 才挂上的）——
//	调用方若把它当成「起来了」，读 Events() 会拿到 nil 迭代器，
//	于是接口在 nil 上崩，而不是给客户端一句「这一轮没起来」。
//
//	（这条钉的是 api 层那个 panic：接口层拿 Wait 的返回值决定是
//	「开流」还是「报 RUN_ERROR」。）
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
