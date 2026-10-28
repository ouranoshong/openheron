// Package openheronrt owns turn orchestration: admission, preemption, recovery and
// the exact order in which results reach durable storage.
//
// It renders nothing, speaks no HTTP, and never decides on its own whether a
// run may resume — the checkpoint is the authority on that.
package run

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"openheron/approval"
	"openheron/contract"
	"openheron/storage"
)

// DefaultStartTimeout bounds how long a caller waits for a turn to become
// streamable before giving up on it.
const DefaultStartTimeout = 60 * time.Second

// AgentBuilder produces the agent for one turn. It is injected so openheronrt
// never imports the agent package, which keeps its tests free of a real model.
type AgentBuilder interface {
	// Build returns the agent for this turn. agentID 是会话选定的入口 agent；
	// 空串 = 配置里的 main（旧会话与未切换过的会话都是这个值）。
	Build(ctx context.Context, sessionID, agentID string, kind contract.TurnKind) (adk.TypedAgent[*schema.Message], error)
}

// Config wires the Runtime's collaborators.
type Config struct {
	AgentBuilder   AgentBuilder
	SessionManager storage.SessionManager
	// ★ checkpoint 的存取器**不在这里**：它由会话提供
	//	（storage.Session.GetCheckPointStore）—— 一个会话的 checkpoint
	//	住在它自己的目录里，存取器自然也归它。
	//
	//	没有存取器 = 不做 checkpoint：那时审批中断无法恢复
	//	（见 [hasCheckpoint]）。
	Input        InputConfig
	StartTimeout time.Duration
}

// ApprovalDecision is one decision about one outstanding interrupt.
//
// ★ 它带自己的 InterruptID：一次可以带**多个**决定（一次答完整组待批）。
//
//	理由见 [storage.PendingApproval] —— 逐个答复在原理上走不通：没被答复的那些
//	会带着新 id 重新中断，客户端手里那批旧 id 就全废了。
type ApprovalDecision struct {
	// InterruptID is the decision's target: the id of one outstanding
	// interrupt. It is the framework's address, not a UUID.
	InterruptID string `json:"interruptId"`
	Approved    bool   `json:"approved"`
	Reason      string `json:"reason,omitempty"`
}

// TurnPhase is the observable stage of a session.
type TurnPhase uint8

const (
	PhaseIdle TurnPhase = iota
	PhaseRunning
	PhaseWaitingApproval
	PhaseStopped
)

func (p TurnPhase) String() string {
	switch p {
	case PhaseIdle:
		return "idle"
	case PhaseRunning:
		return "running"
	case PhaseWaitingApproval:
		return "waiting_approval"
	case PhaseStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// SessionStatus is the projection returned by Status.
type SessionStatus struct {
	SessionID string
	Kind      contract.TurnKind
	Phase     TurnPhase
	StartedAt time.Time
	LastError error
	// RunID identifies the in-flight turn. Empty when idle — the protocol id
	// only exists while a run is open.
	RunID string
}

// Runtime orchestrates turns across sessions.
type Runtime struct {
	cfg Config

	mu     sync.Mutex
	states map[string]*sessionState
}

// New builds a Runtime. It performs no IO and starts nothing.
func New(cfg Config) *Runtime {
	if cfg.StartTimeout <= 0 {
		cfg.StartTimeout = DefaultStartTimeout
	}
	return &Runtime{cfg: cfg, states: make(map[string]*sessionState)}
}

// Submit admits a query, preempting any turn already running for the session.
func (rt *Runtime) Submit(ctx context.Context, sessionID, query string) (*Turn, error) {
	sess, err := rt.cfg.SessionManager.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if query == "" {
		return nil, errors.New("query must not be empty")
	}

	// ★ 一次提问**取代**之前被打断 / 被停掉的那一轮 —— 所以先把那一轮的状态
	//   扔掉。
	//
	//	框架的契约（eino adk/turn_loop.go:569）：checkpoint 里有 runner
	//	状态（被打断、或在轮次中途被取消）时，下一次 Run 一定走**恢复**路径
	//	—— 而恢复路径只会「恢复那个被打断的 agent」，不会跑新的提问。
	//
	//	于是提问被吞掉：轮次永远不 ready，接口只能等到超时
	//	（turn_start_timeout），日志里还留下一句「loop exited: no pending
	//	interrupt」—— 与真实原因（上一轮被停掉了）毫不相干。
	//
	//	★ 什么时候会留下 checkpoint：
	//
	//	 · 中途「停止」—— 框架在 stop 落地时会写 checkpoint（canceled 态）；
	//	 · 被打断（等审批）—— 框架写 checkpoint（可恢复态）。
	//
	//	两者的 checkpoint 都不是「新的提问」要用的东西：新的提问从头开始。
	rt.discardCheckpoint(ctx, sess)

	// ★ 待批记录与 checkpoint **同进同出**：checkpoint 没了，那张审批卡就再也
	//   答不了 —— 留着只会让用户点一个必然失败的按钮。
	if pending, perr := sess.PendingApproval(); perr == nil && pending != nil && len(pending.Interrupts) > 0 {
		if err := sess.ClearPendingApproval(); err != nil {
			rt.logf("session=%s failed to clear the stale pending approval: %v", sessionID, err)
		}
	}

	return rt.startTurn(ctx, sess, loopItem{Query: query}, contract.TurnQuery)
}

// discardCheckpoint 清掉会话的 checkpoint（若有）。
//
// ★ 幂等：框架自己在干净退出时也会删它，重复删无害。
func (rt *Runtime) discardCheckpoint(ctx context.Context, sess storage.Session) {
	_, deleter := sess.GetCheckPointStore()
	if deleter == nil {
		return
	}
	if err := deleter.Delete(ctx, sess.ID()); err != nil {
		rt.logf("session=%s failed to discard the stale checkpoint: %v", sess.ID(), err)
	}
}

// Resume delivers approval decisions for the pending interrupts.
//
// The decisions are refused unless the session holds a persisted pending
// record: the framework exposes no interrupt id to the application, so that
// record is the only gate (architecture.md decision 7 and 8).
//
// ★ decisions 必须覆盖**全部**待批项。
//
//	框架的契约：不在 Targets 里的被中断组件会重新中断自己，而重新中断
//	拿到的是**新的 id** —— 那些旧 id 从此再也不可能被答复。所以要么一次
//	答完整组，要么剩下的会带着新 id 回来（那也能工作，但用户会看到审批
//	卡闪一次、id 全变）。
//
//	★ 取走待批记录是在**答复之前**：框架一旦恢复就会改写 checkpoint，
//
//	旧记录留着会让人以为还能答第二遍。
func (rt *Runtime) Resume(ctx context.Context, sessionID string, decisions []ApprovalDecision) (*Turn, error) {
	sess, err := rt.cfg.SessionManager.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	// ★ 先看 checkpoint 在不在 —— 恢复路径要读它（eino adk/turn_loop.go:1861）。
	//
	//	没有它，框架会在恢复时炸，而那时轮次已经起来了：接口只能等到超时
	//	（turn_start_timeout），用户看到的是「点了批准没反应」。
	//
	//	这里先查一遍，把「没法恢复」变成一句干脆的拒绝 —— 而且要在
	//	TakePendingApproval **之前**：待批记录被取走就再也答不了第二遍。
	//	（待批留着，界面上的那张卡也还在 —— 用户看到的会是一句
	//	「这个审批恢复不了了，重新提问吧」，而不是卡片凭空消失。）
	if !rt.hasCheckpoint(ctx, sess) {
		return nil, fmt.Errorf("%w (session %s)", ErrNoCheckpoint, sessionID)
	}

	pending, err := sess.TakePendingApproval()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoPendingInterrupt, err)
	}
	if pending == nil || len(pending.Interrupts) == 0 {
		return nil, ErrNoPendingInterrupt
	}

	approvals := map[string]any{}
	for _, d := range decisions {
		if d.InterruptID == "" {
			continue
		}
		approvals[d.InterruptID] = approvalResult(d)
	}
	if len(approvals) == 0 {
		return nil, ErrNoPendingInterrupt
	}

	// ★ 记审计 —— 这一步是「人做了什么」的**唯一**持久痕迹。
	//
	//	待批记录刚被取走（TakePendingApproval），那条「谁批了什么」的线索
	//	就此消失；不留审计的话，事后无从查起。
	//
	//	★ 时刻由**服务端**盖：客户端可以传任何时间，而审计的时间必须是
	//	真的发生时刻。
	rt.recordDecisions(sess, pending, decisions)

	item := loopItem{Approvals: approvals}
	return rt.startTurn(ctx, sess, item, contract.TurnResume)
}

// recordDecisions 把这一轮的决定写进会话的审计记录。
//
// ★ 工具名与发起方来自**被取走前**的那份待批 —— 答复之后它就没了，
//
//	所以要在同一个位置取。
func (rt *Runtime) recordDecisions(sess storage.Session, pending *storage.PendingApproval, decisions []ApprovalDecision) {
	details := map[string]*contract.InterruptDetail{}
	if pending != nil {
		for _, it := range pending.Interrupts {
			details[it.InterruptID] = it.Detail
		}
	}

	now := time.Now().UTC()
	recs := make([]storage.ApprovalRecord, 0, len(decisions))
	for _, d := range decisions {
		if d.InterruptID == "" {
			continue
		}
		rec := storage.ApprovalRecord{
			InterruptID: d.InterruptID,
			Approved:    d.Approved,
			Reason:      d.Reason,
			DecidedAt:   now,
		}
		if det := details[d.InterruptID]; det != nil {
			rec.ToolName = det.ToolName
			rec.RequestedByAgent = det.RequestedByAgent
		}
		recs = append(recs, rec)
	}
	if len(recs) == 0 {
		return
	}
	if err := sess.RecordDecision(recs...); err != nil {
		// ★ 审计写不进去**不能**让这一轮失败 —— 决定已经答复了，
		//	工具要照常执行；只是审计缺了一条。
		rt.logf("session=%s failed to record the approval decisions: %v", sess.ID(), err)
	}
}

// hasCheckpoint 报告这个会话还有没有可恢复的 checkpoint。
//
// ★ 没有存取器时一律当作「有」：那就根本没有 checkpoint 这件事，
//
//	不该由这里拦下。
func (rt *Runtime) hasCheckpoint(ctx context.Context, sess storage.Session) bool {
	store, _ := sess.GetCheckPointStore()
	if store == nil {
		return true
	}
	// ★ 「能不能恢复」由会话回答（它读文件并解码 —— 文件在不等于能用）。
	ok, err := sess.HasCheckpoint(ctx)
	if err != nil {
		rt.logf("session=%s failed to read the checkpoint: %v", sess.ID(), err)
		return false
	}
	return ok
}

// approvalResult maps the HTTP decision onto the payload the gated tool reads
// back on resume.
//
// ★ 它必须是 [approval.Decision] —— **同一个具体类型**，不是「结构相同」。
//
//	eino 的 tool.GetResumeContext[T] 是具体类型断言
//	（internal/core/resume.go:102），断言失败 ⇒ hasData=false ⇒
//	闸门拿不到决定。而闸门此前把「拿不到决定」当成「照常执行」，
//	于是**每一次审批都被忽略**，工具照跑。
//
//	各自定义一份同名结构（openheronrt.ApprovalResult / agent.ApprovalResult）
//	编译期完全看不出来，只有运行期才炸 —— 而炸的方式是放行。
func approvalResult(d ApprovalDecision) any {
	return &approval.Decision{Approved: d.Approved, Reason: d.Reason}
}

// startTurn binds a fresh turn, superseding any predecessor, and gets a loop
// running for it.
func (rt *Runtime) startTurn(ctx context.Context, sess storage.Session, item loopItem, kind contract.TurnKind) (*Turn, error) {
	turn := newTurnHandle(sess.ID(), kind)

	st := rt.stateFor(sess.ID())

	st.markStarted(time.Now())

	st.mu.Lock()
	prev := st.turn
	st.turn = turn
	st.mu.Unlock()

	if prev != nil && prev != turn {
		// Release the outgoing turn: its Wait must fail with ErrSuperseded and
		// its OnAgentEvents handshake must not block on a Complete that will
		// never arrive.
		prev.fail(ErrSuperseded)
	}

	loop := rt.newLoop(ctx, sess, turn)
	st.setLoop(loop)

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	turn.cancel = cancel

	rt.startLoop(runCtx, sess.ID(), loop, cancel)

	if ok, _ := loop.Push(item, adk.WithPreempt[loopItem, *schema.Message](adk.AfterToolCalls)); !ok {
		rt.logf("session=%s push was rejected: the loop is stopping or its buffer is full", sess.ID())
	}

	return turn, nil
}

// Abort stops the current turn immediately.
func (rt *Runtime) Abort(ctx context.Context, sessionID string) error {
	st, ok := rt.lookupState(sessionID)
	if !ok {
		return nil
	}
	loop := st.currentLoop()
	if loop == nil {
		return nil
	}

	loop.Stop(adk.WithImmediate())

	// Release the in-flight turn as well. Without this the loop would wait on
	// an OnAgentEvents handshake that nobody can complete: a consumer that
	// stopped reading would block the loop, which would keep the iterator
	// open, which would block the consumer. Fail the turn to break the cycle.
	if turn := st.currentTurn(); turn != nil {
		turn.fail(context.Canceled)
	}

	loop.Wait()

	st.clearTurn()
	rt.removeStateIfIdle(sessionID)
	return nil
}

// Cleanup stops the turn, removes the checkpoint and forgets the session.
//
// It is safe to call for an unknown id, which is what lets the session layer
// inject it as a teardown hook without first checking existence.
func (rt *Runtime) Cleanup(ctx context.Context, sessionID string) error {
	_ = rt.Abort(ctx, sessionID)

	// ★ checkpoint 的存取器由**会话**提供 —— 而这里只有 id，所以先把会话
	//	拿出来（拿不到就说明它已经没了，那也没有 checkpoint 要删）。
	if sess, err := rt.cfg.SessionManager.Get(ctx, sessionID); err == nil {
		if _, deleter := sess.GetCheckPointStore(); deleter != nil {
			if err := deleter.Delete(ctx, sessionID); err != nil {
				rt.logf("session=%s checkpoint delete failed during cleanup: %v", sessionID, err)
			}
		}
	}

	rt.mu.Lock()
	delete(rt.states, sessionID)
	rt.mu.Unlock()
	return nil
}

// Shutdown stops every running loop.
func (rt *Runtime) Shutdown(ctx context.Context) error {
	rt.mu.Lock()
	ids := make([]string, 0, len(rt.states))
	for id := range rt.states {
		ids = append(ids, id)
	}
	rt.mu.Unlock()

	for _, id := range ids {
		if err := rt.Abort(ctx, id); err != nil {
			rt.logf("shutdown: session=%s abort failed: %v", id, err)
		}
	}
	return nil
}

// Status reports the observable state of every known session.
func (rt *Runtime) Status() []SessionStatus {
	rt.mu.Lock()
	ids := make([]string, 0, len(rt.states))
	for id := range rt.states {
		ids = append(ids, id)
	}
	rt.mu.Unlock()

	out := make([]SessionStatus, 0, len(ids))
	for _, id := range ids {
		if st, ok := rt.lookupState(id); ok {
			st.mu.Lock()
			phase := PhaseIdle
			if st.turn != nil {
				phase = PhaseRunning
			}
			kind := contract.TurnQuery
			if st.turn != nil {
				kind = st.turn.Kind
			}
			started, lastErr := st.startedAt, st.lastError
			st.mu.Unlock()
			out = append(out, SessionStatus{
				SessionID: id, Phase: phase, Kind: kind,
				StartedAt: started, LastError: lastErr,
			})
		}
	}
	return out
}

// StatusOf reports one session's phase, resolving the waiting-approval state
// from the session itself.
//
// ★ SessionStatus.Phase cannot express "waiting for a human" on its own: the
//
//	approval lives in the session file, not in the volatile runtime state, and
//	a session with a pending approval has no loop running — it would report
//	PhaseIdle and the sidebar dot would lie.
func (rt *Runtime) StatusOf(ctx context.Context, sessionID string) (SessionStatus, bool) {
	sm := rt.cfg.SessionManager
	if sm == nil {
		return SessionStatus{}, false
	}
	sess, err := sm.Get(ctx, sessionID)
	if err != nil {
		return SessionStatus{}, false
	}
	pending, err := sess.PendingApproval()
	if err != nil {
		return SessionStatus{}, false
	}

	out := SessionStatus{SessionID: sessionID, Phase: PhaseIdle}
	if pending != nil {
		out.Phase = PhaseWaitingApproval
	}
	if st, ok := rt.lookupState(sessionID); ok {
		st.mu.Lock()
		out.StartedAt, out.LastError, out.Kind = st.startedAt, st.lastError, contract.TurnQuery
		if st.turn != nil {
			if out.Phase != PhaseWaitingApproval {
				out.Phase = PhaseRunning
			}
			out.Kind = st.turn.Kind
		}
		st.mu.Unlock()
	}
	return out, true
}

func (rt *Runtime) lookupState(sessionID string) (*sessionState, bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	st, ok := rt.states[sessionID]
	return st, ok
}
