package run

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"openheron/agentkit/obs"
	"openheron/contract"
	"openheron/message"
	"openheron/storage"
)

// newLoop builds the TurnLoop for one session.
//
// There is no withResume parameter: whether a run resumes or starts fresh is
// decided by the checkpoint, which the framework reads itself.
func (rt *Runtime) newLoop(ctx context.Context, sess storage.Session, turn *Turn) *adk.TurnLoop[loopItem, *schema.Message] {
	cfg := adk.TurnLoopConfig[loopItem, *schema.Message]{
		GenInput:      rt.makeGenInput(sess, turn),
		PrepareAgent:  rt.makePrepareAgent(sess),
		OnAgentEvents: rt.makeOnAgentEvents(sess, turn),
	}
	// ★ checkpoint 的存取器由会话提供（storage.Session.GetCheckPointStore）：
	//
	//	没有它 = 这个部署不做 checkpoint，那时恢复路径无从谈起。
	if store, _ := sess.GetCheckPointStore(); store != nil {
		cfg.Store = store
		cfg.CheckpointID = sess.ID()
		cfg.GenResume = rt.makeGenResume(sess)
	}
	return adk.NewTurnLoop(cfg)
}

// makeGenInput assembles one turn's agent input and persists the user message.
//
// Persisting here is safe because GenInput only fires once the previous turn's
// OnAgentEvents has finished archiving. That ordering is the sole guarantee that
// history stays ordered under preemption.
func (rt *Runtime) makeGenInput(sess storage.Session, turn *Turn) func(context.Context, *adk.TurnLoop[loopItem, *schema.Message], []loopItem) (*adk.GenInputResult[loopItem, *schema.Message], error) {
	return func(ctx context.Context, loop *adk.TurnLoop[loopItem, *schema.Message], items []loopItem) (*adk.GenInputResult[loopItem, *schema.Message], error) {
		var (
			consumed  []loopItem
			remaining []loopItem
			queryItem *loopItem
		)
		for i := range items {
			if queryItem == nil && items[i].Query != "" {
				queryItem = &items[i]
				consumed = append(consumed, items[i])
			} else {
				remaining = append(remaining, items[i])
			}
		}

		if queryItem == nil {
			// ★ 没有提问可跑：这一轮是「答复审批」，而框架**没有**走恢复路径
			//   —— 说明 checkpoint 不在（或没有 runner 状态），恢复无从谈起。
			//
			//	这里必须**把轮次判死**，而不是安静地停掉循环：停掉的话轮次永远
			//	不 ready，接口只能等到超时（turn_start_timeout），用户看到的是
			//	「点了批准没反应」。
			turn.fail(ErrNoCheckpoint)
			loop.Stop(adk.WithStopCause("no query items"))
			return &adk.GenInputResult[loopItem, *schema.Message]{
				Input:     &adk.TypedAgentInput[*schema.Message]{Messages: []*schema.Message{message.NewUser("done")}},
				Remaining: items,
			}, nil
		}

		// ★ user 消息在**提交时**打上产生时刻 —— 它是「谁、什么时候说的」
		//   这对出处标记的另一半（归属是哪个 agent 说的，这里是用户说的）。
		//
		//   中间消息（assistant / tool）由 transport 在**产出时**打，
		//   那边是消息真正被生产出来的时刻。两边都记「产生时刻」而不是
		//   「归档时刻」—— 后者会让一轮内所有消息的时间几乎相同。
		userMsg := message.NewUser(queryItem.Query)
		message.StampNow(userMsg)
		if err := sess.Append(userMsg); err != nil {
			rt.logf("session=%s failed to persist user message: %v", sess.ID(), err)
		}

		built, err := rt.cfg.Input.buildInput(ctx, inputParams{
			History:   sess.Messages(),
			Workspace: sess.Workspace(),
		}, queryItem.Query)
		if err != nil {
			return nil, fmt.Errorf("assemble turn input: %w", err)
		}

		return &adk.GenInputResult[loopItem, *schema.Message]{
			Input:     built,
			Consumed:  consumed,
			Remaining: remaining,
		}, nil
	}
}

// makePrepareAgent asks the injected builder for this turn's agent. The
// Runtime never imports the agent package, so tests can inject a fake.
func (rt *Runtime) makePrepareAgent(sess storage.Session) func(context.Context, *adk.TurnLoop[loopItem, *schema.Message], []loopItem) (adk.TypedAgent[*schema.Message], error) {
	return func(ctx context.Context, _ *adk.TurnLoop[loopItem, *schema.Message], _ []loopItem) (adk.TypedAgent[*schema.Message], error) {
		if rt.cfg.AgentBuilder == nil {
			return nil, errors.New("openheronrt: no AgentBuilder configured")
		}
		return rt.cfg.AgentBuilder.Build(ctx, sess.ID(), sess.AgentID(), contract.TurnQuery)
	}
}

// makeOnAgentEvents is the bridge between the TurnLoop and the consumer.
//
// It hands the event iterator to the turn handle, waits for the consumer to
// report back, then archives. openheronrt never renders anything itself.
func (rt *Runtime) makeOnAgentEvents(sess storage.Session, turn *Turn) func(context.Context, *adk.TurnContext[loopItem, *schema.Message], *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]]) error {
	return func(ctx context.Context, _ *adk.TurnContext[loopItem, *schema.Message], events *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]]) error {
		out, err := rt.handoff(ctx, sess, turn, events)
		if err != nil {
			return err
		}
		return rt.finalize(sess, turn, out)
	}
}

// handoff 把事件迭代器交给消费方，等它回报，并把这一轮归档。
//
// ★ 归档在这里，而不在 finalize：事件流一断，产出就再也不会来了。
//
//	「归档优先于呈现」是硬约束 —— 而 finalize 里才有「这一轮算不算完成」
//	的判断，那是产出到手之后的事。
func (rt *Runtime) handoff(ctx context.Context, sess storage.Session, turn *Turn,
	events *adk.AsyncIterator[*adk.TypedAgentEvent[*schema.Message]]) (outcome, error) {
	turn.events = events
	turn.signalReady()

	var out outcome
	select {
	case out = <-turn.done:
	case <-ctx.Done():
		return outcome{}, ctx.Err()
	}

	rt.archive(sess, turn, out.out)
	return out, nil
}

// finalize 结算这一轮：记错、标记轮次结束，并决定这一轮对框架意味着什么。
//
// ★ 与 handoff 分开是因为两件事的**失败方式**不同：
//
//	handoff 失败 = 这一轮拿不到产出（消费方没接上）；
//	finalize 失败 = 产出拿到了，但这一轮不算完成（出错或被审批打断）。
//	混在一个函数里时，读代码分不清「没产出」与「产出了但没完成」。
func (rt *Runtime) finalize(sess storage.Session, turn *Turn, out outcome) error {
	rt.stateFor(sess.ID()).markError(out.out.Err)
	// ★ The turn handle is done the moment it reports back. Leaving it in
	//   the session state made /v2/sessions/{id} keep reporting "running"
	//   forever after a completed turn — the loop stays alive waiting for
	//   the next item, so "a loop exists" says nothing about activity.
	rt.stateFor(sess.ID()).markTurnDone(turn)

	if out.err != nil {
		return out.err
	}
	if out.out.Err != nil {
		return out.out.Err
	}
	if len(out.out.Interrupts) > 0 {
		// Stopping the loop is what makes the checkpoint survive: the
		// framework only writes one when the stop is committed.
		rt.stateFor(sess.ID()).markError(errInterrupted)
		return errInterrupted
	}
	return nil
}

// archive persists the turn's intermediates and, on an interrupt, the pending
// approval.
func (rt *Runtime) archive(sess storage.Session, turn *Turn, out Outcome) {
	for _, msg := range out.Intermediates {
		if err := sess.Append(msg); err != nil {
			rt.logf("session=%s failed to archive intermediate message: %v", sess.ID(), err)
		}
	}

	if len(out.Interrupts) == 0 {
		return
	}

	if err := sess.SetPendingApproval(&storage.PendingApproval{
		Interrupts: out.Interrupts,
	}); err != nil {
		// Fail-closed: without a persisted approval the user could never
		// approve, so surface it loudly rather than silently degrading.
		rt.logf("session=%s FAILED to persist pending approval %v: %v", sess.ID(), out.Interrupts[0].InterruptID, err)
	}
}

// makeGenResume delivers the approval decisions.
//
// openheron does not decide whether a resume is possible — the framework reads the
// checkpoint and judges that itself. The one thing the framework cannot supply
// is the interrupt id, which lives inside a gob payload openheron cannot decode.
//
// ★ Targets 是**全部**待批决定，不是一个。
//
//	框架的契约（eino adk/runner.go:133）：Targets 的 key 是要恢复的组件地址，
//	**不在表里的被中断组件必须重新中断自己**。而重新中断会拿到**新的 uuid**
//	（eino internal/core/interrupt.go:105）—— 所以只答一个的话，其余的
//	下一次就是另一批 id，客户端手里那批旧 id 全部作废。
//
//	Targets 本来就是 map，就是为了「一次答多个」。
func (rt *Runtime) makeGenResume(sess storage.Session) func(context.Context, *adk.TurnLoop[loopItem, *schema.Message], []loopItem, []loopItem, []loopItem) (*adk.GenResumeResult[loopItem, *schema.Message], error) {
	return func(_ context.Context, _ *adk.TurnLoop[loopItem, *schema.Message], canceledItems, unhandledItems, newItems []loopItem) (*adk.GenResumeResult[loopItem, *schema.Message], error) {
		targets := map[string]any{}
		for i := range newItems {
			for id, res := range newItems[i].Approvals {
				if id == "" || res == nil {
					continue
				}
				targets[id] = res
			}
		}
		if len(targets) == 0 {
			return nil, ErrNoPendingInterrupt
		}

		return &adk.GenResumeResult[loopItem, *schema.Message]{
			ResumeParams: &adk.ResumeParams{Targets: targets},
			Consumed:     canceledItems,
			Remaining:    unhandledItems,
		}, nil
	}
}

// logf 是本包的日志出口。
//
// ★ 走统一入口（obs）而不是 log.Printf：级别与前缀只有一处定义，
//
//	而「级别」决定了它会不会出现在默认输出里。
func (rt *Runtime) logf(format string, args ...any) {
	obs.Logger().Warn("[openheronrt] " + fmt.Sprintf(format, args...))
}
