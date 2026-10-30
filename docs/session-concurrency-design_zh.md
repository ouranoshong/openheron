# 会话并发设计 —— 一个会话，一轮

> 状态：**已实现**。闸门、跨客户端的 409 与前端忙态处理都已在代码里 —— §5 指着
> 每一块的落点。§2 讲的仍是**改动之前**的代码，所以它读起来像一段历史。
>
> ★ **继任方案。** [run-lifecycle-design_zh.md](run-lifecycle-design_zh.md) 提议
> 把闸门的互斥锁换成租约与 fence，并把 SSE 连接降级为订阅者。那是一份提案 ——
> 里面的东西都还不在树上。本文定下的规则（「一个会话一轮」）不变。
>
> 范围：`/v2` 的运行路径（`POST /v2/run`）、会话状态投影
> （`GET /v2/sessions/{id}` 与 `GET /v2/sessions/{id}/messages`），以及前端
> 「知道会话正忙」的能力。

## 1. 问题

两个浏览器标签页（或两个窗口，或一个标签页加一个 `curl`）打开同一个会话。
第一个发了消息，第二个也发了消息。结果**不是**两轮并行跑，而是**第二条
消息把第一轮打死了**。

> ★ **本节里的行号是改动之前的位置。** 留着它们是为了「改造前」的状态仍然
> 可读；改动之后代码在哪，见 §5。

前端看起来拦住了这件事 —— 在同一个标签页里确实拦住了：

```tsx
// web/src/components/composer.tsx:47
const busy = status === "running"
```

`status` 来自 zustand store，所以它是**每个标签页各自的内存**。新标签页从
`emptyConversation()` 起步（`web/src/lib/types.ts:125`），也就是
`status: "idle"`。没有任何东西告诉新标签页这个会话正忙，于是它的输入框是
可用的，发送按钮是可点的。

### 1.1 三个缺口

| # | 缺口 | 位置 |
|---|---|---|
| 1 | 「忙」的守卫活在每个标签页的内存里，不在服务端 | `web/src/components/composer.tsx:47` |
| 2 | 服务端已经算出了「这个会话在不在跑」，前端把这个答案扔了 | `api/v2_sessions.go:441` → `web/src/lib/store/actions.ts:85` |
| 3 | 服务端没有准入闸门：第二轮**抢占**第一轮 | `agentkit/run/runtime.go:239` |

**缺口 2 展开。** `stateOf` 已经回答了这个问题：

```go
// api/v2_sessions.go:441
func (s *Server) stateOf(ctx context.Context, sess storage.Session) threadState {
	if sess.HasPending() {
		return stateWaiting
	}
	if status, ok := s.rt.StatusOf(ctx, sess.ID()); ok && status.Phase == run.PhaseRunning {
		return stateRunning
	}
	return stateIdle
}
```

`v2Messages` 把它放进线上的 `view.State`（`api/v2_sessions.go:288`）。
前端收到了它 —— 然后把它塞进 `meta.state`
（`web/src/lib/store/actions.ts:85`），而 composer 读的是 `convo.status`。
**两条状态线，其中一条是死的。** `seedFromHistory` 只会置
`awaiting-approval`（`web/src/lib/store/history.ts:107`），从不置 `running`，
所以新标签页无论服务端说了什么，水合出来都是 idle。

**缺口 3 展开。** `Runtime.Submit` 不拒绝第二轮 —— 它接管：

```go
// agentkit/run/runtime.go:239
if prev != nil && prev != turn {
	// Release the outgoing turn: its Wait must fail with ErrSuperseded and
	// its OnAgentEvents handshake must not block on a Complete that will
	// never arrive.
	prev.fail(ErrSuperseded)
}
```

被抢占的标签页收到 `RUN_ERROR{code:"turn_superseded"}`
（`api/v2_run.go:184`）。新标签页继续跑。在用户看来，这读作「应用把我的
消息弄丢了」。

### 1.2 附带的破坏

`Submit` 在启动新轮次**之前**做了两件破坏性的事
（`agentkit/run/runtime.go:112-118`）：

```go
rt.discardCheckpoint(ctx, sess)      // 删掉正在跑那轮的检查点
if pending, perr := sess.PendingApproval(); ... {
	sess.ClearPendingApproval()       // 丢掉正在跑那轮的待审批
}
```

所以第二个标签页不只是取消了第一个标签页的轮次 —— 它还删掉了那一轮本该
用来恢复的检查点，清掉了第一个标签页正显示着的那张审批卡。等第一个标签页
去回答那张卡时，它拿到的是 `no_checkpoint`（`api/v2_types.go:121`）。
用户的决定被无声地丢弃了。

### 1.3 上游的立场

上游（`heron-bk`）把抢占写成是**有意设计**的产品语义，不是意外：

> ★ 「停止」与「抢占」是两件事：
> - **停止**（`stop()`）= 终止这一轮，用户接着说下一句。
> - **抢占**（在回答途中发新消息）= 旧轮次在安全点让位，新消息接管。
>
> — `heron-bk/docs/frontend-design.md:817`

由此得出两点：

1. 抢占是一个**同客户端**的动作：用户打断**自己**正在进行的回答。
2. 上游前端用与本仓库相同的方式挡住了跨客户端的情况 ——
   `heron-bk/web/src/components/composer.tsx:69` 里是一模一样的
   `const busy = status === "running"`。**上游的守卫有同一个洞。** 两个
   仓库都继承着它。

本设计**去掉抢占**（见 [§4](#4-决策)）。这是一次有意收窄上游语义的决定，
理由是：在没有客户端身份的前提下，无法区分「用户打断自己」与「另一个标签页
插进来」，而猜错的代价（无声地毁掉另一个界面的轮次、检查点与审批）远高于
拒绝的代价。

## 2. 今天实际发生了什么

```
标签页 A                        标签页 B
──────                         ───────
发「hello」
POST /v2/run
  Submit → startTurn
  loop 在跑，SSE 在流

                               打开会话（新标签页）
                               GET /v2/sessions/{id}/messages
                                 → view.State = "running"
                                 → 存进 meta.state
                                 → convo.status = "idle"   ← 被忽略

                               发「world」（能点：status 是 idle）
                               POST /v2/run
                                 Submit → discardCheckpoint()   ← 删掉 A 的检查点
                                            ClearPendingApproval()← 丢掉 A 的审批
                                            startTurn → prev.fail(ErrSuperseded)
                               200，SSE 在流

  SSE: RUN_ERROR{code:"turn_superseded"}
  status → "error"
  如果它当时正等审批：
    去回答 → 409 no_checkpoint
```

记录里两条用户消息都在（两次 `Append` 都成功），所以历史看起来很合理 ——
这正是它读作「应用把我的消息弄丢了」而不是「两个客户端撞车」的原因。

### 2.1 锁的粒度

**互斥的单位是会话 id。** 一个会话，一轮活着的轮次 —— 两个**不同**的会话
互不干扰地并发跑，而同一会话上的两个客户端互相排斥。

这不是一个新决定：它就是运行时**已经**在用的粒度。`Runtime` 用会话 id 给
它的每会话状态建索引：

```go
// agentkit/run/runtime.go:91
states map[string]*sessionState
```

而每一个轮次级的事实 —— 活轮次、loop、开始时间、最后一次错误 —— 都挂在
那个 id 的 `sessionState` 上（`agentkit/run/bridge.go:29`）。§5.1 的闸门读的
正是那份状态。**互斥是按会话 id 的，不是按会话对象，也不是按进程。**

由此有两个推论，它们的性质并不相同：

| 范围 | 互斥掉的是什么 | 机制 |
|---|---|---|
| **同一进程内** | 同一会话 id 上的两轮 | `Runtime.states[sessionID]` —— 内存态，权威 |
| **跨进程** | 同一会话 id 上的两轮 | **没有** —— 见下 |

### 2.2 闸门的两半持久性不同

`ensureIdle`（§5.1）检查两个事实，而它们**并不一样持久**：

| 检查项 | 来源 | 扛得住重启吗？ |
|---|---|---|
| 存在待审批 | `sess.PendingApproval()` —— 读会话文件 | **扛得住** |
| 有活轮次在跑 | 运行时的内存 `sessionState`（`turn`+`loop` 这一对，§5.1.1 洞 2） | **扛不住** |

待审批是持久状态（`storage/filestore/session.go:43`，落进会话的
`state.json`）。活轮次是**易失的执行状态** —— 这是有意为之：`sessionState`
「刻意不是会话：会话是持久数据，它是易失执行状态」
（`agentkit/run/bridge.go:27-28`）。

所以这条规则的诚实说法是：

> **同一个服务端进程内，一个会话 id 至多有一轮活着的轮次。跨进程只有
> 「待审批」那一半被强制。**

### 2.3 为什么「单进程」是成立的假设

openheron 是**单操作者、单进程、无鉴权**的运行时
（`heron-bk/docs/api-v2-design.md:229` —— 「heron 是单机、单操作者、无鉴权
的运行时」）。两个进程指着同一个配置根不是受支持的部署：它们本来就会在会话
文件上撞车（`storage/filestore` 用普通 `O_APPEND` 写记录，且会话缓存在进程
内，`storage/filestore/manager.go:105`）。

所以闸门里「内存态」的那一半，对这个代码库来说正是合适的范围。但把它写
明白很重要：**这个互斥是单个进程的运行时的属性，不是分布式锁。** 将来
openheron 若长出第二个指向同一状态目录的服务端进程，这道闸门对「活轮次」
那一半就退化成尽力而为，修法必须落在存储层（每个会话 id 一个锁文件）——
而不是落在运行时里。

### 2.4 这把锁**不是**什么

- **不是按 agent。** 两个会话跑同一个 agent 互不相干；
  `heron-bk/docs/agent-design.md:91` 明确说 agent 实例可以安全地并发复用。
- **不是按用户或按客户端。** 没有客户端身份（D7），所以除了会话 id 之外
  没有别的东西可以拿来做锁的**键**。
- **不是全局锁。** 全局锁会把互不相关的会话也串起来 —— 运行时本来就是并发
  跑它们的（`Runtime.states` 是 map，不是单个槽位），这里也不改变这一点。

### 2.5 并发的两条轴 —— 本设计只碰其中一条

openheron 有**两种互相独立的并发**，把它们混为一谈是这里最容易犯的错。

| 轴 | 什么在并发跑 | 住在哪 | 本设计动它吗？ |
|---|---|---|---|
| **A · 轮次级** | 两个*会话*同时跑 | 每个会话 id 一个 `Runtime.states` 条目 | **不动** —— 原样 |
| **B · agent 级** | 一轮之内的*多个 agent* | 一轮那个 loop 内部 | **不动** —— 原样 |
| **C · 跨客户端** | 同一会话上的两个*客户端* | 一个会话 id，一个轮次槽位 | **动** —— 这就是要治的 |

**轴 B 就是并行委派那个特性，它不受影响。** 上游说得很明确：并行是框架给的，
不是 openheron 自己造的：

> **① 并行是框架给的，不是自己造的**
> eino 已经能并行跑多个 `task` 调用。heron **不引入** supervisor / workflow /
> 自造调度器
>
> — `heron-bk/docs/multi-agent-design.md:50`

而且两个特性是正交的：

> ★ 两者**正交**：切到 `pm` 之后，pm 依然可以并行分派给它的 3 个工程师。
>
> — `heron-bk/docs/multi-agent-design.md:18`

**为什么轴 B 在机制上不受影响。** 子 agent 是它父 agent 的一个**工具**
（`agentkit/build/assemble.go:97` 把子节点放进 `cfg.SubAgents`），框架并发
执行同级的工具调用。而这些子 agent 全都跑在**父轮次内部**：

- 它们共享父轮次的 loop，也就共享它的 `sessionState` 条目。
- 它们从不调用 `Runtime.Submit` —— `Builder.Build` 把会话 id 这个参数整个
  忽略掉了（`agentkit/build/builder.go:69`，形参就是 `_ string`）。
- 运行时的轮次记账是**每会话 id 一轮**，而并行的子 agent **不是轮次**：
  它们是一轮内部的工具调用。

所以 §5.1 的闸门看不见它们，而且**也不该看见**：一个扇出给三个工程师的
轮次，在**一个**会话 id 上仍然是**一**轮。把它拒掉会直接毁掉并行委派特性。

真正要紧的区分是：

- **一轮之内**：agent 级的并行要多少有多少。
- **同一会话的轮次之间**：一次只有一轮 —— 这才是本设计强制的。

对忙着的会话发第二个 `/v2/run`，那是第二个**轮次**，不是第二个 agent。
若是后者，这道闸门就写错了。

## 3. 规则

> **一个会话至多有一轮活着的轮次。对忙着的会话发第二个 `/v2/run` 一律
> 拒绝（409），且不改变任何东西。**

三部分，各有明确的归属：

### 3.1 服务端是权威

守卫从每个标签页的内存搬进 `agentkit/run`。`Runtime.Submit` 与
`Runtime.Resume` 在会话已有活轮次时拒绝。

这是唯一不可协商的部分：**客户端侧的守卫是礼貌，服务端侧的守卫才是规则。**
任何不是 openheron Web UI 的客户端 —— `curl`、另一个 AG-UI 客户端、未来的
移动端 —— 都得到同样的保护。

### 3.2 状态投影是前端的事实来源

composer 不再读自己的内存来判断「忙」，改读服务端的答案。
`GET /v2/sessions/{id}/messages` 已经带着 `state`；前端必须用起来。

### 3.3 「忙」就是忙 —— 包括「等着审批」

一个正在等审批的会话**不是**空闲的。上下文属于那个待决的事：检查点正是
恢复要接着走的地方，待审批正是用户在回答的东西。发一条新查询就得把两者
都丢掉。

`ensureAgentIdle`（`api/v2_sessions.go:221`）对「切换 agent」已经用的正是
这条规则，`v2Compact`（`api/v2_compact.go:45`）对「压缩」也是。**只有运行
路径没有。** 让三者一致，正是本设计的要点。

## 4. 决策

| # | 决策 | 理由 |
|---|---|---|
| D1 | 服务端拒绝第二个轮次（409），绝不抢占 | 抢占无法区分「用户打断自己」与「另一个标签页插进来」。拒绝在两种情况下都是安全的；抢占在其中一种情况下是破坏性的。 |
| D2 | 拒绝**不改变任何东西** —— 不删检查点，不清审批 | 当前的破坏（§1.2）比冲突本身更糟。拒绝必须是惰性的。 |
| D3 | 「忙」包含「等着审批」 | 上下文属于那个待决的事。与 `ensureAgentIdle` 和 `v2Compact` 一致。 |
| D4 | 前端读服务端的 `state` | 单一事实来源。服务端已经算出来了，前端把它扔了。 |
| D5 | 忙着的会话给一个「停止并接管」的动作 | 「忙」不是死路。用户可以先停掉另一轮再发。 |
| D6 | 状态投影保持 `idle`/`running`/`waiting_approval` | `state` 是**会话级**的事实。「running」的意思是「这个会话有一轮活着的轮次」，与哪个客户端持有无关。加一个「别处在忙」的状态需要第二条轴和第二个线上字段。 |
| D7 | 不做客户端身份，不做归属跟踪 | 见 D1。规则是「一个会话一轮」，不是「一个客户端一轮」。 |

### 4.1 为什么不是「第二个轮次排队等」？

排队被考虑过并否决：它会让第二个标签页的发送悄无声息地排在用户看不见的
一轮后面，而这个队列没有主人 —— 它什么时候排空？拒绝是可见且可行动的。

### 4.2 为什么不是「保留抢占，但要显式标志」？

`POST /v2/run {"preempt": true}` 能在不产生歧义的前提下保住上游那个动作。
这一轮否决了它，因为它需要**客户端身份**才安全（见 D1）—— 没有身份时，
第二个标签页发来的 `preempt: true` 照样会毁掉第一个标签页的轮次。而带上
身份就是个大得多的改动（身份签发、存储、过期、重启后「这个客户端是谁」）。
D5 的「停止并接管」覆盖了真实需求 —— 「我想打断那一轮」—— 且不需要这些。

如果以后还想要抢占，应当作为独立设计重新引入，并带上身份，而不是偷渡成一个
标志位。

## 5. 设计

### 5.1 服务端：准入闸门

**新哨兵**放在 `agentkit/run/errors.go`：

```go
// ErrSessionBusy is returned when a session already has a live turn.
//
// ★ It is a conflict, not a failure: the caller may retry once the
//   session goes idle. The HTTP layer maps it to 409.
ErrSessionBusy = errors.New("session is busy")
```

**闸门**在 `agentkit/run/runtime.go` 的 `Submit` 与 `Resume` 里：

```go
// Submit admits a query into the session.
//
// ★ A session has at most one live turn. A second Submit is refused with
//   ErrSessionBusy rather than preempting the first one: preemption
//   cannot tell "the user interrupting themselves" from "another tab
//   cutting in", and the second case destroys a turn the user cannot
//   see — along with its checkpoint and its pending approval.
func (rt *Runtime) Submit(ctx context.Context, sessionID, query string) (*Turn, error) {
	sess, err := rt.cfg.SessionManager.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if query == "" {
		return nil, errors.New("query must not be empty")
	}

	if err := rt.ensureIdle(ctx, sess); err != nil {
		return nil, err
	}

	// From here on the session is ours: the checkpoint and the pending
	// approval belong to a turn that is over, and discarding them is
	// what makes the next turn start from a clean slate.
	rt.discardCheckpoint(ctx, sess)
	if pending, perr := sess.PendingApproval(); perr == nil && pending != nil && len(pending.Interrupts) > 0 {
		if err := sess.ClearPendingApproval(); err != nil {
			rt.logf("session=%s failed to clear the stale pending approval: %v", sessionID, err)
		}
	}

	return rt.startTurn(ctx, sess, loopItem{Query: query}, contract.TurnQuery)
}
```

**`ensureIdle` 就是闸门。** 它住在 `agentkit/run`，因为运行时状态在那里；
API 层不得重新实现它（AGENTS.md：「不要在 API 层复制业务装配逻辑」）。

```go
// ensureIdle refuses the request when the session already owes the user
// something.
//
// ★ Both cases mean the context belongs to something else right now:
//   - a live turn: it is running, or it is waiting for an approval;
//   - a pending approval: the checkpoint it would resume from is the
//     session's context, and answering it is the only way forward.
//
// ★ The check is a read. It does not touch the checkpoint and it does not
//   clear the pending approval — a refused request must change nothing
//   (design decision D2). Those two side effects live in Submit, after
//   the gate.
// ★ It asks about the loop, not the turn pointer — see §5.1.1 Hole 2.
func (rt *Runtime) ensureIdle(ctx context.Context, sess storage.Session) error {
	if p, err := sess.PendingApproval(); err == nil && p != nil && len(p.Interrupts) > 0 {
		return fmt.Errorf("%w: an approval is pending on this session", ErrSessionBusy)
	}
	return rt.ensureNoLiveTurn(ctx, sess)
}
```

以及那个更窄的（用于恢复）：

```go
// ensureNoLiveTurn refuses the request when a turn is executing.
//
// ★ It asks about the LOOP, not the turn pointer. st.turn is cleared when
//   the turn finishes archiving (markTurnDone), so it lags: a turn that
//   has already produced its answer still reads as "running" for a
//   moment, and refusing on that would be a false positive. The loop is
//   the thing that is actually executing.
func (rt *Runtime) ensureNoLiveTurn(ctx context.Context, sess storage.Session) error {
	st, ok := rt.lookupState(sess.ID())
	if !ok {
		return nil
	}
	st.mu.Lock()
	turn, loop := st.turn, st.loop
	st.mu.Unlock()
	if turn != nil && loop != nil {
		return fmt.Errorf("%w: a turn is already running on this session", ErrSessionBusy)
	}
	return nil
}
```

> `ensureIdle` 读的就是 `ensureAgentIdle` 读的那两个事实
> （`api/v2_sessions.go:221`）—— 一个待审批与一个运行时阶段。API 层那份
> 留在原处（切 agent 是 API 层的关切）；运行路径这份住在它所守护的运行时
> 状态旁边。§7.3 落地后两者合一。

**`PhaseWaitingApproval` 很关键。** `StatusOf` 从会话自己的待批记录推导它
（`agentkit/run/runtime.go:379`），所以一个刚刚停下来要审批的会话仍然报
「忙」—— 尽管没有任何轮次在执行。这正是 D3。

**轮次记账不再抢占。** `claimTurn` 把「忙不忙」的判定与接管放在同一把锁下，
而被换下的轮次是被「释放」而不是被「判失败」：

```go
// agentkit/run/runtime.go — claimTurn
st.mu.Lock()
prev := st.turn
busy := prev != nil && st.loop != nil && conflictTurns(kind, prev.Kind)
if !busy {
	st.turn = turn
	st.loop = loop
	// ...
}
st.mu.Unlock()
```

有了闸门，`prev` 只可能是**已经结束**的轮次（它的 `Complete` 落地了，但状态还
没清掉）。它必须被释放，而不是被判失败 —— `runClaimedTurn` 做的正是这件事：

```go
// agentkit/run/runtime.go — runClaimedTurn
if prev != nil && prev != turn {
	// The outgoing turn has already completed — the claim would have
	// refused a live one. Release its handshake so the OnAgentEvents
	// side is not left waiting for a Complete that will never arrive.
	prev.fail(ErrSuperseded)
}
```

调用本身留着：它释放握手。变的只是它的**含义** —— 从「新一轮接管了」变成
「上一轮已经结束了」。`ErrSuperseded` 仍是那个信号；HTTP 层的映射不动。


> **竞态由「占槽」关掉，不是由「检查」关掉。** 上面那个检查是给人看的
> 错误提示；权威的是 `claimTurn` —— 它把「忙不忙」的判定与「接管」放在
> **同一把锁**下。两个同时到达的请求不可能都通过它：输家拿到
> `ErrSessionBusy`，而不是半启动的一轮。

### 5.1.2 实施时发现的四个时序缺陷

上面三个洞是设计错误。把它们实现出来，又暴露了四个 —— 全都关于**时序**，
而且每一个都改了设计。

#### 时序 1 —— `Complete` 在归档落地之前就返回了

HTTP 响应在 `transport.Stream` 返回时发出，而 `turn.Complete` 紧接着被调用。
但 `Complete` 只是**通知**归档：运行时异步取走结果、把消息写进记录。所以
客户端被告知「你这轮结束了」，而服务端其实还没把这一轮的消息写下去。

两个后果，都很糟：

- **闸门**看到一个仍然忙着的会话。用户在答案刚到就发下一条，会被拒 ——
  一次误拒，而且发生在最普通的时序上。
- **下一轮的输入**是从记录里构建的（`makeGenInput` 读 `sess.Messages(true)`），
  所以一个早于上一轮归档就开跑的轮次，会读到不完整的历史。

**修法：`Complete` 只在归档落地之后才返回。** 轮次带一个 `archived` 通道，
归档完成时关掉它，`Complete` 等它。于是 HTTP 响应离开服务端时历史已经是
完整的，闸门的「忙」与客户端的「完了」不可能各说各话。

这个等待有上限（30 秒）：归档里的 bug 不该把请求挂死。

#### 时序 2 —— 待审批写在了检查点之前

`TestApprovalChainEndToEnd` 抓住了这一点：它等待审批出现，然后断言检查点
文件存在。审批在了，检查点不在。

次序**一直**是错的。框架在 loop **停止**时写检查点 —— 而 loop 停止发生在
那一轮的回调**返回之后**。审批却写在那个回调**里面**。所以一直存在一个
窗口：用户看到了审批卡，回答它却得到 `no_checkpoint` —— 正是上游文档描述
的「孤儿审批」（`heron-bk/docs/checkpoint-store.md:303`）。

**修法：审批在 loop 退出时写，不在回调返回时写。** 回调只是**拿着**它；
`loopExited` 写它，那时框架已经写好了检查点。审批从此不再早于它需要的东西。

这不只是为了修那个测试：它把孤儿窗口整个消掉了。

#### 时序 3 —— 一次恢复不该挡住另一次恢复

第一版实现里，第二次 `Resume` 会被「第一次恢复轮次还在收尾」挡住。那让三个
既有测试挂了（`TestResumeConsumesTheApproval`、
`TestMultiplePendingApprovalsAreAnsweredAsAGroup`、
`TestDecisionsAreAppendOnly`），而**它们挂得对**：第一次答复还在收尾时再答复
一组审批，是很平常的用法。

解开它的区分是：**恢复续接的是它所回答的那个被打断的轮次** —— 那是同一轮
对话，不是新的一轮（`contract.TurnKind` 就是这么写的）。**查询**才是新的一轮，
而那恰恰是不能重叠的东西。

所以闸门问的是在跑那一轮的**种类**：

| 在跑的轮次 | 新查询 | 新恢复 |
|---|---|---|
| 查询 | **拒绝** —— 跨客户端那一种 | 允许 —— 它回答的就是这一轮自己的审批 |
| 恢复 | **拒绝** —— 新的一轮 | 允许 —— 同一轮继续 |

`agentkit/run/runtime.go` 里的 `conflictTurns` 就是这条规则。

#### 时序 4 —— 起不来的轮次把会话永久锁死

`Turn.Wait` 在轮次迟迟不就绪时返回 `ErrStartTimeout` —— agent 建不起来，
loop 从没启动。那条路上没人调 `fail()`，于是轮次没被释放，`archived` 通道
也永远没关。

有了闸门，那就是一个**锁死的会话**：槽位一直占着，之后每个请求都被拒。
用户连「重新起一轮」来恢复都做不到。

**修法：启动超时把轮次判失败。** `Wait` 超时时调 `fail()`，它释放握手，也
释放 `archived` 通道。一个既跑不起来、也结束不了的轮次，不该占着会话。

#### 四者的共同点

每一个都是**同一个轮次的两个事实被读在了不同时刻**：「agent 干完了」与
「归档落地了」、「审批写了」与「检查点写了」、「有轮次在跑」与「一轮开始了」、
「轮次放弃了」与「槽位空了」。

每一次的修法都是让第二个事实**等**第一个，而不是去猜它。一个读着滞后指针
的闸门就是个带洞的闸门 —— 无论那检查写得多仔细。

### 5.2 服务端：HTTP 映射

`ErrSessionBusy` 变成带自己错误码的 409：**`session_busy`**。

现有错误码是 `agent_busy`（`api/v2_types.go:48`）。这里**改名**而不是复用，
因为旧名字说的是错的话：

| | 今天的 `agent_busy` | 改后的 `session_busy` |
|---|---|---|
| 它实际守的是什么 | **会话**，不是 agent | 会话 |
| 它自己的错误消息 | `"session is busy; finish or stop the run first"`（`api/v2_sessions.go:25`） | 不变 |
| 它的三处调用点 | 切 agent、压缩，以及（新增的）运行路径 | 不变 |

这三处**全都是会话级**的拒绝。它自己的消息早就写着「session is busy」——
名字是唯一对不上的那个。而在并行委派存在的前提下（见 §2.5），`session_busy`
更是主动误导：多个 agent **可以**合法地同时忙，就在一轮之内。客户端若把
`agent_busy` 读成「这个 agent 正在干活」，就会得出恰恰相反的结论。

```go
// api/v2_types.go
const (
	// ...
	// codeSessionBusy is the session-level conflict: a turn is running,
	// or an approval is waiting, so this request must not start.
	//
	// ★ It used to be called agent_busy. That name was wrong: the three
	//   call sites (agent switch, compaction, run) all guard the session,
	//   and parallel delegation means several agents can legitimately be
	//   busy at once.
	codeSessionBusy = "session_busy"
)
```

```go
// api/v2_types.go — v2FailErr
case errors.Is(err, run.ErrSessionBusy):
	v2Fail(c, consts.StatusConflict, codeSessionBusy, err.Error(), true)
```

`retryable: true` —— 正在跑的那轮结束后冲突自行解除，所以稍后重试是有意义
的。这与 `errAgentBusy` 现有的待遇一致（`api/v2_types.go:113`）。

> **改名波及三处调用点与一个测试文件。** Go 标识符 `codeAgentBusy` 与线上值
> `agent_busy` 都改：
>
> | 位置 | 改什么 |
> |---|---|
> | `api/v2_types.go:48` | 常量：`codeAgentBusy` → `codeSessionBusy`，`"agent_busy"` → `"session_busy"` |
> | `api/v2_types.go:113` | `errAgentBusy` 那一支（切 agent） |
> | `api/v2_sessions.go:223`、`api/v2_sessions.go:227` | `errAgentBusy` 的两处包裹（切 agent） |
> | `api/v2_compact.go:46` | 压缩的拒绝 |
> | `api/v2_agent_switch_test.go:120`、`api/v2_agent_switch_test.go:285` | 两处断言 |
> | `web/test/fake-server.ts:1355` | 假后端那个 409 |
>
> Go 哨兵 `errAgentBusy`（`api/v2_sessions.go:25`）**不**改名 —— 它是内部值，
> 不是线上错误码，而且它的消息本来就写着「session is busy」。改的只有**线上**
> 错误码。
>
> 两种成因靠消息文本区分（「an approval is pending」与「a turn is already
> running on this session」），客户端不必多一个码就能分辨。

### 5.3 服务端：`state` 必须正好是前端需要的

`stateOf` 已经回答了，但它有个次序上的微妙之处值得写明，因为 §5.4 依赖它：

```go
// api/v2_sessions.go:441
func (s *Server) stateOf(ctx context.Context, sess storage.Session) threadState {
	if sess.HasPending() {
		return stateWaiting
	}
	if status, ok := s.rt.StatusOf(ctx, sess.ID()); ok && status.Phase == run.PhaseRunning {
		return stateRunning
	}
	return stateIdle
}
```

- `waiting_approval` 优先于 `running`：等审批的会话比仅仅在跑的会话更忙，
  而界面要显示审批卡。
- `running` 是**会话级**的：它的意思是「这个会话有一轮活着的轮次」，不是
  「这个客户端持有一轮」。

`StatusOf` 从会话自己的待批记录报出 `PhaseWaitingApproval`
（`agentkit/run/runtime.go:379`）—— 所以待审批这一档由同一次查询覆盖，
`stateOf` 不需要第二个来源。

**有一处缺口要补。** `stateOf` 分开读 `HasPending()` 与 `StatusOf()`，
而 `StatusOf` 又读了一次待批记录。那是同一个事实读了两次，它们可能不一致
（两次读之间待审批被清了）。修法是让 `stateOf` **只读一次**阶段：

```go
// after
func (s *Server) stateOf(ctx context.Context, sess storage.Session) threadState {
	// ★ One read, one answer: the phase already accounts for the pending
	//   approval (StatusOf derives PhaseWaitingApproval from the session's
	//   own pending record), so reading both separately could see them
	//   disagree.
	status, ok := s.rt.StatusOf(ctx, sess.ID())
	if !ok {
		return stateIdle
	}
	switch status.Phase {
	case run.PhaseWaitingApproval:
		return stateWaiting
	case run.PhaseRunning:
		return stateRunning
	default:
		return stateIdle
	}
}
```

> **闸门与 `stateOf` 刻意读的不是同一个东西。** `stateOf` 走 `StatusOf`，
> 而它是从 `st.turn` 推导 `PhaseRunning` 的 —— 那个滞后的指针（§5.1.1 洞 2）。
> 闸门则直接读 `turn`+`loop` 这一对。于是，一轮答案落地后的片刻，**界面**可能
> 还说「running」，而**闸门**已经放行下一次查询了。
>
> 这个不对称是对的：
>
> | | 会滞后吗 | 错了的后果 |
> |---|---|---|
> | `stateOf`（呈现） | 可能多说一会儿「running」 | composer 短暂显示停止按钮 —— 无害，下一个事件就消 |
> | 闸门（准入） | **绝不能**拒掉一个空闲的会话 | 误拒会把用户锁在一个他明明能用的会话外面 |
>
> 把两者改成读同一个东西，就等于给它们同一个滞后 —— 而闸门的滞后会变成误拒。
> 所以它们保持分离，闸门是更严的那个。

### 5.4 前端：一条状态线

**问题。** `convo.status` 是 store 自己的事件归约状态
（`web/src/lib/events.ts:292`）；`meta.state` 是服务端的答案
（`web/src/lib/store/actions.ts:85`）。composer 读第一条，侧栏的点也读第
一条。第二条是死的。

**修法：一条线，而且是服务端那条。**

`convo.status` 仍是唯一的状态字段。服务端的 `state` 在水合时喂给它，之后
由事件流维持最新。

**水合**（`web/src/lib/store/actions.ts:80-90`）：

```ts
const view = await api.getMessages(id)
set((s) => ({
	sessions: {
		...s.sessions,
		[id]: {
			meta: { ...(s.sessions[id]?.meta ?? stubMeta(id)), state: view.state },
			convo: seedFromHistory(view.messages, view.pendingApprovals, view.usage, view.state),
			loaded: true,
		},
	},
}))
```

★ **水合出来的「running」一定是别人的轮次。** 刚打开会话的那个界面不可能拥有
一轮它没启动的轮次 —— 拥有轮次的界面自己拿着事件流，永远不会重新水合
（`openSession` 对已加载的会话直接返回）。所以水合时**忙**标记也会置上，
composer 会说清楚那是谁的轮次。

**`seedFromHistory` 认服务端的 state**（`web/src/lib/store/history.ts:30`）：

```ts
export function seedFromHistory(
	raw: AgMessage[],
	pending?: PendingApproval[],
	usage?: Usage,
	state?: ThreadMeta["state"],
): ConversationState {
	// ...
	const convo = emptyConversation()
	convo.messages = messages
	if (usage) convo.usage = usage
	if (usage?.context) convo.context = usage.context

	// ★ The server's state is the truth about whether this session is
	//   busy. Without this a second tab hydrates as idle and its send
	//   button works — the tab has no memory of the turn another tab
	//   started.
	//
	// ★ A hydrated "running" is ALWAYS somebody else's turn — see above.
	if (state === "running") {
		convo.status = "running"
		convo.busy = true
	}
	if (pending && pending.length > 0) {
		convo.status = "awaiting-approval"
		convo.interrupts = pending.map(/* ... */)
	}
	return convo
}
```

待审批那一支保持优先：审批卡比「在跑」更具体。

**composer**（`web/src/components/composer.tsx:47`）不变：

```ts
const busy = status === "running"
```

它现在读到的是对的东西了。守卫没改，改的是它的**输入** —— 这正是整个修复的
要点：composer 的逻辑从来没错，它读的是每个标签页各自的内存。

**要不要新增一个 `busy-elsewhere` 状态？** 不要 —— D6。`status: "running"`
的意思是「这个会话有一轮活着的轮次」。composer 为它显示停止按钮。至于这轮
是**这个**标签页的还是别的标签页的，不改变用户能做什么：停掉它，或者等。

但有个体验问题：如果标签页 B 为标签页 A 拥有的那一轮显示停止按钮，而标签页
B 的用户点了它 —— **标签页 B 刚刚杀掉了标签页 A 的轮次。** 这可接受吗？

与今天一样（标签页 B 的发送也杀了它），但**是显式的**而非意外的。而且这是
标签页 B 继续往下走的唯一途径。另一个选择 ——「你不能停不是你启动的轮次」——
需要客户端身份，D7 拒绝了它。

所以：停止按钮留着，它的意思是「停掉这个会话的轮次」，不论谁持有。按钮的
提示文案应当说明这一点。

### 5.5 前端：409 是一等公民

发送路径必须处理被拒的发送，**且不破坏本地视图**。今天，发送失败后乐观
追加的用户消息仍留在记录里（`appendLocalUserMessage`，
`web/src/lib/store/actions.ts:524`），状态置为 `status: "error"`
（`loadError`，`web/src/lib/store/actions.ts:442`）。那条消息根本没到服务端，
却留在屏幕上，像是发出去过。

**先服务端。** 拒绝以非 2xx 响应抵达，发生在流开始之前，所以它是一个纯
JSON 错误体 —— `runStream` 已经处理了（`web/src/lib/api.ts:319-323`）：

```ts
if (!res.ok) {
	const body = (await res.json().catch(() => null)) as ApiErrorBody | null
	handlers.onError?.(body?.error ?? `HTTP ${res.status}`)
	return
}
```

但**错误码**被丢掉了 —— 只剩 `body.error`（散文）。前端需要 `code` 才能
区分「忙」与「失败」。修法：

```ts
// web/src/lib/api.ts — runStream
if (!res.ok) {
	const body = (await res.json().catch(() => null)) as ApiErrorBody | null
	handlers.onError?.(body?.error ?? `HTTP ${res.status}`, body?.code)
	return
}
```

```ts
// web/src/lib/api.ts — RunHandlers
export interface RunHandlers {
	onEvent: (ev: AgEvent) => void
	onError: (message: string, code?: string) => void
	onDone: () => void
}
```

**store**（`web/src/lib/store/actions.ts:250-269`）区分「忙」与「失败」，并
在被拒时把乐观消息回滚：

```ts
run = runStream(
	{ threadId, query: trimmed },
	{
		onEvent: (ev) => handleEvent(set, get, localKey, ev as AgEvent),
		onError: (message, code) => {
			run = null
			setCurrentRunKey(null)
			if (code === "session_busy") {
				// ★ The session is busy elsewhere. The message never
				//   reached the server, so the optimistic bubble must go
				//   back — otherwise the transcript claims a message that
				//   was never sent.
				get().rollbackLocalMessage(localKey)
				get().setBusyElsewhere(localKey, message)
				return
			}
			get().loadError(localKey, message)
		},
		onDone: () => { /* ... */ },
	},
)
```

**composer** 显示「别处在忙」的状态。这就是「停止并接管」的界面（D5）：用户
被告知会话正忙，并拿到停止按钮。

### 5.6 前端：停止并接管

用户应当得到的流程：

1. 标签页 B 尝试发送 → 409 `session_busy`。
2. 标签页 B 说：「这个会话正忙 —— 另一个窗口在跑它。」并给一个**停止**按钮。
3. 点停止调 `POST /v2/sessions/{id}/abort`（`api/v2_sessions.go:386`）——
   现成的端点，本来就是真正的停止（`heron-bk/docs/frontend-design.md:801`）。
4. 会话变 idle → 标签页 B 的输入框可用 → 用户再发一次。

现有的 `stop()`（`web/src/lib/store/actions.ts:318`）做的正是这件事：断本地
流、`POST /abort`、失败要出声。**它不需要改** —— 只需要在「别处在忙」这个
状态下够得着，而今天够不着（composer 显示的是发送按钮，不是停止按钮）。

所以 §5.5 里的前端改动就是解锁它的那个：`state` 说忙时 `status` 变成
`"running"`，composer 为 `busy` 渲染停止按钮
（`web/src/components/composer.tsx:588`）。

于是 `session_busy` 拒绝只需要说明**为什么**忙，好让用户明白那个停止按钮会
作用在别人持有的轮次上。服务端的消息文本承担这件事（「a turn is already
running on this session」）。

### 5.7 改造后的完整流程

```
标签页 A                        标签页 B
──────                         ───────
发「hello」
POST /v2/run → 200，SSE 在流

                               打开会话
                               GET /v2/sessions/{id}/messages
                                 → view.State = "running"
                                 → seedFromHistory 置 status = "running"
                               composer：停止按钮，发送禁用

                               （用户点停止）
                               POST /v2/sessions/{id}/abort
                                 → 标签页 A 的 SSE：RUN_ERROR（已停止）
                                 → 会话变 idle

                               发「world」→ 200，SSE 在流
```

撞车的情况：

```
标签页 A                        标签页 B
──────                         ───────
发「hello」→ 在跑
                               发「world」
                               POST /v2/run
                                 ensureIdle → ErrSessionBusy
                                 → 409 {"code":"session_busy",
                                        "error":"session is busy: a turn is
                                                 already running on this session"}
                               乐观消息回滚
                               composer 显示停止 + 「别处在忙」

  标签页 A 的轮次：未受影响
  检查点：未受影响
  待审批：未受影响
```

## 6. 改动落在哪里

| 范围 | 文件 | 改动 |
|---|---|---|
| 哨兵 | `agentkit/run/errors.go` | 加 `ErrSessionBusy` |
| 闸门 | `agentkit/run/runtime.go` | 加 `ensureIdle` 与 `ensureNoLiveQuery`；在 `Submit` 与 `Resume` 里调用；加 `claimTurn`（原子的判定-接管）与 `conflictTurns`（查询 vs 恢复）；把破坏性的 `discardCheckpoint` / `ClearPendingApproval` 挪到闸门之后；`Abort` 清掉作废的审批 |
| 次序 | `agentkit/run/turn.go` | `Complete` 等归档；`archived` 通道 |
| 次序 | `agentkit/run/loop.go` | 归档改为「拿着」待审批，而不是写下它 |
| 次序 | `agentkit/run/bridge.go` | loop 退出时写下待审批 —— 在检查点之后 |
| HTTP | `api/v2_types.go` | 把 `run.ErrSessionBusy` 映射成 409 `session_busy`；`agent_busy` 改名为 `session_busy` |
| HTTP | `api/v2_sessions.go` | `stateOf` 只读一次阶段 |
| 前端 | `web/src/lib/api.ts` | `onError` 带上错误 `code` |
| 前端 | `web/src/lib/store/history.ts` | `seedFromHistory` 接服务端的 `state` |
| 前端 | `web/src/lib/store/actions.ts` | 传 `view.state`；处理 `session_busy`；回滚乐观消息（`markBusy`） |
| 前端 | `web/src/lib/store/selectors.ts` | `selectBusyElsewhere` |
| 前端 | `web/src/lib/types.ts` | `ConversationState.busy` |
| 前端 | `web/src/components/composer-meta.tsx` | 「别处在忙」那一行 |
| 前端 | `web/src/components/composer.tsx` | 把那一行传下去 |
| 测试 | `agentkit/run/gate_test.go` | 闸门：拒绝、惰性、恢复这一条例外 |
| 测试 | `agentkit/run/runtime_test.go` | 抢占那个用例改成拒绝用例 |
| 测试 | `api/v2_busy_test.go` | 409 + `session_busy`；跑着的那一轮存活 |
| 测试 | `web/test/session-busy.test.ts` | 水合置 `running` |
| 测试 | `web/test/store-busy.test.ts` | 409 回滚气泡并标记忙 |
| 测试 | `web/test/e2e/session-busy.spec.ts` | 第二个界面看到会话正忙 |
| 测试 | `web/test/fake-server.ts` | `busySession` 场景 |
| 文档 | `docs/architecture.md` + `_zh` | 准入规则（§5.3） |
| 文档 | `docs/user-manual.md` + `_zh` | 会话忙时用户看到什么（§6.7） |
| 文档 | `docs/developer-manual.md` + `_zh` | 闸门怎么工作、三处次序（§3.8） |

### 6.1 `Resume` 也需要闸门

`Runtime.Resume`（`agentkit/run/runtime.go:330`）回答审批。它**不能**因为
「有待审批」被拦 —— 待审批正是它要回答的东西。但它必须因为「有活轮次」被拦，
用的是同一个 `ensureNoLiveTurn`（§5.1.1 洞 2 说明了它为什么问 loop 而不问
轮次指针）：

```go
func (rt *Runtime) Resume(ctx context.Context, sessionID string, decisions []ApprovalDecision) (*Turn, error) {
	// ...
	// ★ The pending approval is what this call is for — do not refuse on
	//   it. But a live turn means somebody else's turn is running, and
	//   this one would take it over.
	//
	// ★ And it goes FIRST: TakePendingApproval consumes the record and
	//   writes the disk, so a refusal after that point would eat the
	//   user's decision (§5.1.1 Hole 1).
	if err := rt.ensureNoLiveTurn(ctx, sess); err != nil {
		return nil, err
	}
	// ...
}
```

所以闸门是**两个方法**，不是一个：

- `ensureIdle` —— 用于新查询：有活轮次**和**有待审批都拒绝。
- `ensureNoLiveTurn` —— 用于恢复：只在有活轮次时拒绝。

`ensureIdle` 就是 `ensureNoLiveTurn` 外加待审批那一项检查。这样每个方法只
守一条清晰的规则。

### 6.2 `Submit` 的破坏性步骤挪到闸门之后

今天（`agentkit/run/runtime.go:112-118`）检查点被丢弃、待审批被清除，
都发生在 `startTurn` **之前**。只有在确认会话归我们之后，它们才是对的。
闸门把它们挪动：

```
现在:   Get → discardCheckpoint → ClearPendingApproval → startTurn
改造后: Get → ensureIdle（在这里拒绝，什么都不碰）→ discardCheckpoint
                                                    → ClearPendingApproval
                                                    → startTurn
```

这就是 D2 —— 拒绝必须是惰性的 —— 也是阻止第二个标签页毁掉第一个标签页的
检查点与审批的那一步（§1.2）。

### 6.3 `turn_superseded` 错误码留着

`turn_superseded` 仍会被产出：两个客户端一起冲过闸门（§5.1 的竞态说明），
以及 `startTurn` 的握手释放，都用它。错误码与它的映射
（`api/v2_run.go:184`）不变。只是**常规路径**不再产出它。

## 7. 测试

### 7.1 Go —— 闸门

`agentkit/run/gate_test.go`：

| 用例 | 断言 |
|---|---|
| `TestSubmitRefusalIsInert` | 拒绝之后：待审批还在、记录未变、跑着的那一轮没有被释放 |
| `TestSubmitRefusesWhileAnApprovalIsPending` | 待审批挡住新查询 |
| `TestResumeIsAllowedWhileAnApprovalIsPending` | 恢复仍然可用 —— 闸门不挡回答 |
| `TestRefusedResumeKeepsTheApproval` | 被拒的恢复让待审批原样保留 —— 用户的决定没被吃掉（§5.1.1 洞 1） |
| `TestSubmitIsNotRefusedWhileTheTurnIsArchiving` | 收尾窗口：答案已落地的轮次**不**被拒 —— 没有误拒 |
| `TestAStartTimeoutReleasesTheSession` | 起不来的轮次不会把会话永久锁死（§5.1.2 时序 4） |
| `TestAbortClearsTheVoidApproval` | 中止一个正在等审批的轮次会清掉那张审批 —— 先停再接管真的接管得了（§5.1.1 洞 3） |

`agentkit/run/runtime_test.go`：

| 用例 | 断言 |
|---|---|
| `TestASecondSubmitIsRefusedWhileATurnRuns` | 活会话上的第二个 `Submit` 返回 `ErrSessionBusy`，跑着的那一轮存活，被拒的查询没有落盘 |

「拒绝是惰性的」那条用例最重要：它是 §1.2 的回归测试。

### 7.2 Go —— HTTP 映射

`api/v2_busy_test.go`：

| 用例 | 断言 |
|---|---|
| `TestV2RunOnBusySessionIsConflict` | 409 + `code == "session_busy"`，且该冲突可重试 |
| `TestV2RunOnBusySessionDoesNotDisturbTheRun` | 会话仍然报 `"state":"running"` |

### 7.3 前端 —— 单测

`web/test/session-busy.test.ts` —— 水合：

| 用例 | 断言 |
|---|---|
| 服务端的 running 水合成 running | `seedFromHistory(..., "running")` → `status === "running"` |
| 水合出的 running 标记为忙 | 同一次调用 → `busy === true` —— 它属于别的界面 |
| 空闲的会话不忙 | `seedFromHistory(..., "idle")` → 没有置 `busy` |
| 待审批优先于 running | `seedFromHistory(..., "running", [approval])` → `status === "awaiting-approval"` |

`web/test/store-busy.test.ts` —— 拒绝：

| 用例 | 断言 |
|---|---|
| 乐观气泡被回滚 | 被拒的发送之后，记录里没有用户气泡 |
| 会话被标记为忙，不是失败 | `status === "running"`，`error.code === "session_busy"` |
| 普通失败仍然是失败 | 非 409 的失败置 `status: "error"`，且不置 `busy` |

### 7.4 前端 —— E2E

`web/test/e2e/session-busy.spec.ts`：

| spec | 断言 |
|---|---|
| 第二个界面看到会话正忙 | 忙的那一行在，停止按钮在，发送按钮不在 |
| 竞态下的请求被拒且不留痕 | 被拒的消息不在记录里，界面说明了原因 |
| 忙着的会话可以被接管 | 停掉那一轮后，忙的那一行消失 |

E2E 需要假后端（`web/test/fake-server.ts`）把一个会话的状态跨两个界面保持
住 —— 它按线程存着 `state`，而 `busySession` 场景会让一轮一直开着，会话因此
保持忙碌。

> 上游规则照旧：E2E 用假后端，绝不用真后端（AGENTS.md，「E2E 用假后端，
> 不要改成真后端」）。与真后端的那一半由 `make smoke` 覆盖。

## 8. 待决问题

1. ~~**`ensureIdle` 与 `startTurn` 之间的竞态。**~~ **已解决。** 闸门现在
   是一次比较并交换：`claimTurn` 把「忙不忙」的判定与接管放在同一把锁下，
   所以两个同时到达的请求不可能都通过。输家拿到 `ErrSessionBusy` —— 一个
   409，而不是半启动的一轮。检查（`ensureIdle`）留着，作为那句友好的、
   说明**是哪一种**冲突的错误提示。

2. **会话列表里的 `state`。** `GET /v2/sessions` 每行已经返回 `state`
   （`api/v2_types.go:151`），但侧栏只画待审批那个点
   （`web/src/components/sidebar.tsx:125`）。侧栏要不要给每个在跑的会话画一个
   「运行中」的点，让会话多的用户一眼看出哪个正忙？**建议：要，但作为单独
   一次改动** —— 它需要一个刷新触发条件，而侧栏不轮询。

3. **同一标签页刷新后忙状态还在吗？** **在，而且它现在就是恢复路径。**
   刷新会水合出 `state: "running"`，并显示忙的那一行与停止按钮：用户可以
   停掉那个孤儿轮次并接管。改造之前，刷新得到的是一个空闲的 composer ——
   那一轮其实还在服务端跑着，界面却一个字都不说（AGENTS.md 的「假停止」
   那条）。**不需要额外工作。**

## 9. 落地

这次改动是一个完整的单元 —— 没有前端修法，只做闸门，第二个标签页的 409 就
只是一句原始错误消息；没有闸门，只做前端修法，读到的 `state` 服务端算了却
没人执行。

建议在这次改动内部的次序：

1. `agentkit/run`：`ErrSessionBusy`、`ensureIdle`、`ensureNoLiveTurn`、
   `Submit`/`Resume` 里的闸门、破坏性步骤挪到闸门之后，以及 §8.1 的比较并
   交换。
2. `api`：409 映射、`stateOf` 只读一次阶段，以及 `agent_busy` → `session_busy`
   的改名（§5.2）。
3. 前端：`onError` 带上错误码、`seedFromHistory` 接 state、水合时传它、
   409 处理与回滚。
4. 测试：先 Go（闸门是契约），再前端单测，再 E2E spec。
5. 文档：架构（规则）、用户手册（用户看到什么）、开发者手册（闸门与排查）——
   中英一起，标识符逐条对应。

**上游备注。** `heron-bk/docs/frontend-design.md:817` 把抢占写成产品语义，
而本设计去掉它。上游仓库是**另一套代码**（`module heron`，自己的 git 历史），
本仓库不 import 它一行 —— 两者只共享血缘，不共享代码。所以这里**不需要**动
它，本设计也没有动。上游那些文档会继续描述旧行为，直到有人把这个设计移植
过去；那是一次独立的、有意的移植，不是本次工作的前置条件。

`agent_busy` → `session_busy` 的改名（§5.2）同理：它只动**本**仓库的线上
契约。上游自己的 `api/v2_types.go` 保留 `codeAgentBusy`，它的错误码表也继续
列着 `agent_busy` —— 不变，因为这次工作并不修改上游。
