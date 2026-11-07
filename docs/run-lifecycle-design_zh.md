# Run 生命周期设计 —— 一轮比连接活得久

> openheron 把一轮对话绑在一条 HTTP 连接上：连接负责抽干事件流，
> 落盘靠连接回吐的东西。本文拆掉这根绑定——run 变成一个自带身份、
> 所有权和真相的持久对象，SSE 连接退化成它的一个可选视图。
>
> ★ **第 1 节描述的是代码现状**，那里的每条结论都指着树上的一行。
>   **提案逐节标注**，因为它现在已经落地了一半：
>
> | 节 | 状态 |
> |---|---|
> | §1 今天的「一轮」是什么 | **代码现状** |
> | §2 目标模型 | §3-§9 已经长出来的形状 |
> | §3 D1 —— run 是一行事实 | **已实现** |
> | §4 D2 —— 通知是提示，扫描才是真相 | **已实现** —— 服务端扫，客户端问 |
> | §5 D3 —— 租约与 fence | **已实现** —— 仲裁器、进展驱动的续租、读侧的 fence |
> | §6 D4 —— 驱动可插拔 | **已实现** —— 接口、文件驱动、内存仲裁器 |
> | §7 事件管线 | **已实现** —— 推导搬进 `agentkit/emit`，run 自己消费 |
> | §8 API 表面 | **已实现** —— 扫描路由与挂上 |
> | §9 前端 | **已实现** —— 挂上与扫描出来的 busy 标记 |
>
> 把「是什么」和「要做什么」混在一份设计里，文档漂移就是这么开始的。

> 英文版：[run-lifecycle-design.md](run-lifecycle-design.md)
>
> 关联文档：[session-concurrency-design.md](session-concurrency-design.md)
> 描述了「一个会话一轮」的准入闸门——本文是它的**继任方案**，不是它的补充。

## 1. 今天的「一轮」是什么

### 1.1 保管链

一条用户消息今天走的路径如下，图中每个箭头都是树里真实存在的函数调用：

```
POST /v2/run
  │
  ├─ api/v2_run.go:192   startRun → run.Runtime.Submit
  │    └─ agentkit/run/runtime.go:177   run.Runtime.Submit
  │         └─ agentkit/run/runtime.go:227   claimTurn        (the gate: a mutex)
  │              └─ agentkit/run/runtime.go:279   openRunRow     ← the row is written
  │              └─ agentkit/run/runtime.go:336   runClaimedTurn
  │                   └─ agentkit/run/bridge.go:99   startLoop → a goroutine
  │                        └─ agentkit/run/loop.go:21   newLoop → adk.TurnLoop
  │
  ├─ agentkit/run/loop.go:110  makeOnAgentEvents  (inside the loop goroutine)
  │    └─ agentkit/run/loop.go:119  emit.Derive(events, …)   ← the run reads its own events
  │         └─ agentkit/emit/emit.go:150  src.Next()       ← the ONLY consumer
  │         └─ agentkit/emit/emit.go:…    publishes contract.RunEvent to the subscribers
  │    └─ agentkit/run/loop.go:134  rt.archive(sess, turn, out)
  │         └─ agentkit/run/loop.go:…    sess.Append(msg)   ← the transcript
  │    └─ agentkit/run/loop.go:166  rt.finishRun(turn, out)  ← the terminal row
  │
  └─ api/v2_run.go:157  turn.Subscribe()      (the connection subscribes)
       api/v2_run.go:160  transport.Render(stream, sess.ID(), turn.RunID, sub.Events())
            └─ transport/transport.go:60   Render: contract.RunEvent → AG-UI frames
       api/v2_run.go:165  turn.WaitArchived()   (the response waits for the archive)
```

★ **落盘路径不再穿过渲染路径。** run 自己读 agent 的事件
（`agentkit/run/loop.go:119`），推导它们的含义，归档结果。连接订阅 run 发布
的东西（`api/v2_run.go:157`）并把它渲染出来——它是 run 的一个视图，不是它的
产出管线。连接死了不再会拖住归档。

★ 账本的那一行同样由 run 自己的 goroutine 写：`openRunRow` 在 loop 启动之前
写下它，终态行由 `rt.finishRun` 写。这一行是关于 run 的事实，transcript 是
关于对话的事实。

### 1.2 五个后果

★ 其中两个已经被 §3 部分解决——行存在了，于是 run 有了痕迹和身份；另外三个
原封不动。下面逐条标出状态，读者才知道提案的哪一部分还在承重。

**C1 —— run 没有持久身份。** —— *§3 已部分解决*
`Turn.RunID`（`agentkit/run/turn.go:31`）现在在 turn 被认领时被赋值
（`agentkit/run/runtime.go:214`），API 也把自己那一层铸的 id 传下去
（`api/v2_run.go:218`），于是 SSE 帧里的 id 和账本行里的 id 是同一个。
`Runtime.StatusOf` 从扫描里把它读回来，所以
`GET /v2/sessions/{threadId}` 返回的是一个真实的 `runId`
（`api/v2_sessions.go:94`），不再是空字符串。**还缺的是：**客户端从来不传
`runId`（`web/src/lib/store/actions.ts:250-254`），所以「挂回一个你知道的
run」还不存在——那是 §8。

**C2 —— 崩溃会静默地丢掉这一轮。** —— *§3 已部分解决*
run 的那一行在 loop 启动之前就写好了（`agentkit/run/runtime.go:259`），所以
崩溃会留下痕迹：那一行还开着，启动时的对账把它收成 `interrupted`
（`storage/filestore/manager.go:245`）。会话不再读起来像「从没跑过」——
它读起来像「有一轮被中断了」。**还缺的是：**消息仍然随崩溃一起丢了，因为
transcript 是渲染器写的（§7）。那一行记录了 run 发生过，但不记录它说了什么。

**C3 —— 处理器死了会把会话卡死。**
`agentkit/run/runtime.go:326` 把 run 与请求解绑：
`context.WithCancel(context.WithoutCancel(ctx))`。存下来的 `turn.cancel`
只在一个地方被调用——`agentkit/run/bridge.go:129`，而且是在 loop 退出**之后**。
但只要 `agentkit/run/loop.go:120` 阻塞在 `turn.done` 上，loop 就退不出来；
而 `turn.done` 只有 `Complete`（处理器）或 `fail`（`agentkit/run/turn.go:194`）
会喂。所以一个没调 `Complete` 就死掉的处理器——panic、worker 被杀——会让 run
的 goroutine 永远阻塞，会话永远 busy。`Abort` 是唯一的逃生口
（`agentkit/run/runtime.go:565`），而没有任何东西会自动调用它。
★ 账本让这个卡死变得**看得见**——扫描会显示一个处于 `running` 却没有主人的
run——但它并不能解开：闸门照样拒这个会话。解开它要靠 §5 的租约和 §7 的管线。
（普通的客户端断开**不属于**这一类：处理器会继续跑，抽干也会继续，因为
`transport/transport.go:160-172` 只在写失败之后停止**发帧**——deriver 那个循环
（`agentkit/emit/emit.go:168`）里的累积照走不误。）

**C4 —— 活着的 run 对第二个界面是不可见的。**
`turn.events` 是单消费者的 `*adk.AsyncIterator`（`agentkit/run/turn.go:33`），
谁先抽干谁就占住它。第二个浏览器无法挂到一个正在跑的 turn 上，而
`GET /v2/sessions/{threadId}/messages`（`api/v2_sessions.go:269`）只显示已经
归档的部分——于是新开的页面看到一段说到一半的对话，却完全没有 indication
说还有东西在跑。

**C5 —— 所有权是先到先得，而且没有「活着」这回事。**
`claimTurn`（`agentkit/run/runtime.go:207-241`）就是一把互斥锁：谁先拿到锁，
谁就占住这个会话直到自己跑完。run 现在带着一个 epoch
（`agentkit/run/runtime.go:259`），账本因此能给两个主人排序——但没有租约、
没有过期，也没有办法区分「持有者正在推进」和「持有者卡住了」。今天存在的那些
内存态身份检查——`markTurnDone`（`agentkit/run/bridge.go:51`）和 `loopExited`
里的 loop 身份检查（`agentkit/run/bridge.go:115-124`）——是手写的、建立在
指针之上的 fence。它们在一个进程内有效，出了这个进程就不作数。

### 1.3 上一轮的闸门

[session-concurrency-design.md](session-concurrency-design.md) 定下了规则：
一个会话一轮；第二条 query 被 409 `session_busy` 拒掉
（`api/v2_types.go:48`、`api/v2_types.go:110-116`）；抢占被彻底移除。
这条规则保留。这里改变的是**闸门由什么构成**——指针上的互斥锁换成带 fence
的租约，而 busy 状态不再是内存投影，变成扫描出来的事实。

## 2. 目标模型

### 2.1 run 是单位，连接是视图

```
                     POST /v2/run  (runId = the client's run id)
                              │
                              ▼
                 agentkit/run:  Runtime.Submit
                     │
                     ├─ ledger.NextEpoch(session)  →  epoch n
                     ├─ arbiter.Claim(session, runId, epoch n)  →  Lease{ExpiresAt}
                     ├─ ledger.Append(Run{state: starting, epoch: n})
                     ├─ start the run's own goroutine ─────────────────────┐
                     │      eino events                                   │
                     │        ├─ agentkit/emit: derive                    │
                     │        │     ├─ archive artifacts → transcript      │
                     │        │     └─ contract.RunEvent → subscribers     │
                     │        └─ ledger.Append(Run{state: …}) on each      │
                     │           transition                               │
                     └─ return a Run handle (not bound to the request) ────┘
                              │
   GET /v2/sessions/{id}/runs ── scan ──▶  the truth (ledger + arbiter)
                              │
   POST /v2/run (same runId) ── attach ─▶  subscribe to the run's events
```

### 2.2 数据流

上面的控制流说的是谁调用谁。这张图说的是**数据往哪走**——它画出来的形状就是
整个提案的主张：三个出口、一个所有者、一个真相。

```
  ① events — the model's stream, readable exactly once
  ┌────────────────────────────────────────────┐
  │ run's own goroutine   (agentkit/emit)      │
  └────────────────────────────────────────────┘

                        │
            ┌─────────────┴─────────────┐
            ▼                          ▼
    ② archive artifacts                 ③ contract.RunEvent
            │                           │
            ▼                           ▼
  ┌────────────────────┐      ┌──────────────────────────┐
  │ transcript         │      │ subscribers              │
  │ session.jsonl      │      │ bounded channel, drop    │
  └────────────────────┘      └──────────────────────────┘
            │  ⑤ terminal row               │  ④ render
            │     AFTER the                 ▼
            │     archive             ┌──────────────────────────┐
            ▼                         │ SSE connection           │
  ┌────────────────────────┐          │ (a view only)            │
  │ ledger                 │          └──────────────────────────┘
  │ runs.jsonl             │
  │ the run's own row      │
  └────────────────────────┘
            │  ⑥ scan = the truth
            ▼
  ┌────────────────────────────────────────────┐
  │ GET /v2/sessions/{id}/runs                 │
  │ activeRunId · state · epoch                │
  │ ← the truth, not the stream                │
  └────────────────────────────────────────────┘
                        │  ⑦ only a scan clears busy
                        ▼
              ┌────────────────────┐
              │ frontend           │
              │ (can attach)       │
              └────────────────────┘
```

★ **三个出口，一个所有者。** run 自己的 goroutine 是唯一读模型流的东西，它喂
三个出口：**transcript**（发生过什么）、**订阅者**（谁在看）、**账本**（现在什么
为真）。连接只是第三个出口上的一扇窗——而不是那个拥有产出的人。

★ **账本收两次写。** 一次是 run 在每次状态迁移时写自己那一行，另一次是**终态
行**（5）——它写在归档落地**之后**，于是「这一轮结束了」和「消息已经落盘」成了
同一句话（§3.4）。就这一个次序，让忙标记不用任何超时也保持诚实。

★ **(6) 和 (7) 是设计的另一半。** 真相是扫账本，而客户端只有在这次扫描发话时
才可以清掉忙标记（§4.2）。流是提示，账本是记录。

### 2.3 四条规则

**R1 —— run 是一个持久对象。** 它有身份（run id）、有生命周期（一个状态机）、
有一行账本记录。不管有没有人在看它，它都存在；它能以痕迹的形式活过重启。

**R2 —— 连接是订阅者，不是所有者。** 它可以挂上、摘下、再挂上。它的死不会
让 run 停下来，也不会改变落盘的内容。

**R3 —— 通知是提示，扫描才是真相。** SSE 流可能迟到、不完整、重复或者干脆
丢。任何关于「现在在干什么」的问题都由扫描账本回答，绝不靠相信推送。

**R4 —— 所有权是租约，每一次持久写入都带 fence。** 租约说「谁可以跑」，
fence 说「谁可以记账」。两者都由可插拔的驱动裁决，于是这套协议能一路从
单进程搬到多进程。

## 3. D1 —— run 是一行事实

★ **本节已实现。** 契约在 `storage/runledger.go`，文件驱动在
`storage/filestore/runledger.go`，写行在 `agentkit/run/runrow.go`。

### 3.1 那一行

run 的记录是**生命周期**，不是载荷。transcript 已经管着内容与数字；这一行
管的是时序与所有权。

```go
// storage/runledger.go
package storage

// RunState is where a run is in its lifecycle.
type RunState uint8

const (
    RunStarting        RunState = iota // claimed, not yet executing
    RunRunning                          // executing
    RunWaitingApproval                  // ended by raising an interrupt
    RunCompleted                        // finished normally
    RunFailed                           // finished with an error
    RunAborted                          // stopped by the user
    RunInterrupted                      // the owner vanished mid-run (reconciled)
)

// Run is one row of the run ledger.
//
// ★ It carries lifecycle facts only. Usage, message counts and the transcript
//   itself belong to the session's messages — keeping them here would give the
//   numbers two homes, and the dashboard would eventually disagree with itself.
type Run struct {
    RunID     string
    SessionID string
    Kind      contract.TurnKind // query or resume
    State     RunState
    Epoch     uint64            // the fence this run holds
    Owner     string            // the instance that claimed it
    // ParentRunID is the run a resume continues. Empty for a query.
    ParentRunID string
    StartedAt   time.Time
    UpdatedAt   time.Time
    EndedAt     time.Time
    ErrorCode    string
    ErrorMessage string
}
```

★ 状态在线上是**名字**而不是数字（`storage/runledger.go`——
`RunState.MarshalJSON`）。一份比二进制活得更久的账本，必须在常量重新编号之后
仍然读得懂。

### 3.2 账本

```go
// storage/runledger.go
//
// ★ Append writes one COMPLETE snapshot of a run, not a delta. The reader
//   takes the last row per run id, and the highest epoch among the open runs
//   is the session's current owner. Both rules are order-tolerant, which is
//   what makes a fenced-out owner harmless: it cannot write itself back into
//   the lead.
type RunLedger interface {
    // Append writes one complete snapshot of a run.
    Append(ctx context.Context, r Run) error
    // List returns one row per run, newest row per run id wins.
    List(ctx context.Context) ([]Run, error)
    // NextEpoch allocates the fence epoch for a new run of this session.
    NextEpoch(ctx context.Context) (uint64, error)
    // Reconcile closes the runs that were still open when the process stopped.
    Reconcile(ctx context.Context) (int, error)
}
```

★ **为什么每行是快照而不是增量。** 增量日志要求每个读者自己做折叠；快照
日志让每一行都能独立读懂，也让最后一行被截断时可以直接丢弃（run 退回它
上一刻真正处于的状态）。代价是字段重复——在这个量级上无关紧要。

★ **为什么是「未结束的 run 里 epoch 最高的赢」，而不是「最后一行赢」。**
「最后一行赢」是**同一个 run 内部**的规则（`storage.FoldRuns`）；**跨 run**
时由 epoch 裁决（`storage.ActiveRun`）：一个来自 epoch 3 的僵尸持有者在
epoch 4 接管之后醒来写了一行，读者仍然认为 epoch 4 是持有者。这就是 fence
在读侧起作用——那里既便宜又不需要锁。

★ **epoch 由账本分配，不由仲裁器分配。** epoch 必须跨重启单调，而它不能住在
一个易失的结构里——账本才是活得比进程久的那个东西。§5 的租约带着账本发出来
的 epoch；租约管的是「还活着吗」，epoch 管的是「谁在前」。一个数字两个家，
就是两个真相。

### 3.3 这一行放在哪

放在会话目录里，和会话的其它文件作伴（`storage/filestore/session.go`
定义了现有的文件名）：

```
<root>/<sessionId>/session.jsonl     the transcript (append-only)
<root>/<sessionId>/state.json        the session state (rewritten atomically)
<root>/<sessionId>/runs.jsonl        the run ledger (append-only)  ← new
```

★ **`runs.jsonl` 不是 `state.json`。** `state.json` 每次变更都是整体重写；
把一份会变长的 run 列表塞进去，
意味着每次状态迁移都要重写这个会话的全部历史。追加式账本与 transcript 同形，
复用同一套追加机制，而且账本自己的写入就是那道持久性屏障。

★ **账本是有界的。** 一个无限增长的账本就是泄漏。文件驱动默认保留最后 N 行
（N = 200，`storage/filestore/runledger.go`——`maxRunLedgerRows`），用
`state.json` 同一套原子重写来裁剪：常见情况下文件仍然是追加式的，最坏情况下
它有上界。

### 3.4 这一行就是归档屏障

★ **终态行写在归档落地之后。** 单是这个次序，就让 busy 标记天然诚实：

```
run goroutine:  … archive → transcript  →  ledger.Append(state: completed)
scan:           sees "completed" ⟹ the messages are on disk
```

★ 实现是 `rt.finishRun`（`agentkit/run/loop.go:145`），调用点在归档处
（`agentkit/run/loop.go:130`）。有一个例外：以抛审批结束的 run 要等 loop 退出
——审批和 checkpoint 是在那里写的（`agentkit/run/bridge.go:152`）——因为扫描
不能比「用户要答的那个问题已经落盘」更早地宣布这一轮结束。

★ **终态是 run 自己的真相，不是「没报错」。** 消费者看到的那个错误会被带进
outcome（`api/v2_run.go`——`Outcome.Err`），所以模型超时的 run 记作 `failed`，
用户停掉的 run 记作 `aborted`。少了这一步，账本就成了唯一会撒谎的地方：它会把
一个中途死掉的 turn 记成 `completed`。被取消的 turn 也**不是**会话的最后一个
错误——用户停掉一轮，不等于会话出了故障。

今天同样的保证要靠一条 30 秒的等待通道来买
（`agentkit/run/turn.go:156-165`、`agentkit/run/turn.go:173`），因为归档在
连接的下游。有了这一行，等待就消失了：***这一行就是那道屏障***，没有超时
需要设。

## 4. D2 —— 通知是提示，扫描才是真相

### 4.1 服务端

★ **这一半已实现。** SSE 流的形状不变——还是一条 HTTP 响应上的 AG-UI 帧。
变的是它的权威：

- run 的 goroutine **无论有没有订阅者**都会写终态行。流的结束不是 run 的结束。
- `GET /v2/sessions/{threadId}/runs` 扫描账本，回答「发生过什么、正在发生什么」
  （`api/v2_sessions.go:423`）。
- `GET /v2/sessions/{threadId}` 的 `state` 来自扫描，而不是来自易失的 map：
  `stateOf`（`api/v2_sessions.go:506`）问的是 `StatusOf`
  （`agentkit/run/runtime.go:711`），而 `StatusOf` 问的是账本
  （`agentkit/run/runtime.go:749`——`activeRun`）。

★ **易失的 map 降级为缓存。** `sessionState`
（`agentkit/run/bridge.go:30-39`）留着——它持有活的 loop 和活的 turn，这两样
本来就是内存里的东西——但它不再是**真相**。`StatusOf` 混着一个易失来源
（map 里的时间戳）和两个持久来源（会话文件里的待审批、账本里的活动 run）；
现在由持久的那一边决定阶段。

### 4.2 客户端

★ **busy 标记变成扫描出来的事实，而不是从流里推出来的结论。** 今天客户端
是从「流结束了」推断出「会话空闲了」。在新约定下：

| 今天 | 提案 |
|---|---|
| 流结束 → `onDone` → 假定空闲（`web/src/lib/store/actions.ts:271-275`） | 流结束 → **重新扫描** → 再决定 |
| `stop()` 乐观地置 `busy: false`（`web/src/lib/store/actions.ts:339`） | `stop()` → 发 abort → **重新扫描** → 再决定 |
| 409 `session_busy` → `markBusy`（`web/src/lib/store/actions.ts:464-485`） | 不变——但这条消息背后是一个扫描出来的事实 |
| 页面加载 → `seedFromHistory` 读 `state`（`web/src/lib/store/history.ts:100-112`） | 不变——现在扫的是账本 |

规则就一句话：**客户端只有在扫描发话时才可以清掉 `busy`。** 一个没有伴随
扫描就结束的流，让标记保持原样。

★ 为什么这件事比看上去重要：今天的客户端可能在**危险的方向**上出错。如果
服务端还在收尾而客户端自行假定「空闲」，下一条消息就会发进一个服务端认为
busy 的会话——上一轮正是用归档等待把这事糊过去的。有了「只有扫描能清标记」
这条规则，这一类 bug 是**消失**，而不是被计时器绕过去。

### 4.3 扫描回答什么

★ **已实现**，这就是线上的形状（`api/v2_types.go`——`runsView`）：

```
GET /v2/sessions/{threadId}/runs
{
  "items": [
    {
      "runId": "…", "state": "completed", "kind": "query",
      "epoch": 7, "startedAt": "…", "endedAt": "…"
    }
  ],
  "activeRunId": "…"      // the run that currently owns the session, if any
}
```

★ `error` 只在 run 失败时出现，`activeRunId` 只在有活动 run 时出现——空闲会话
扫出来是 `{"items": []}`。一个永远存在却永远为空的字段，是客户端还得学会
忽略的噪音。

`activeRunId` 是 UI 最需要的那个字段：它让一个 run **可以被挂上**（§8.1），
也顶替了今天 `GET /v2/sessions/{threadId}` 返回的那个空 `runId`
（`api/v2_sessions.go:94`——现在它返回活动 run 的 id）。

## 5. D3 —— 用租约和 fence 取代先到先得

★ **已实现。** epoch 早就是真的了（§3.2），现在租约也是。

### 5.1 为什么互斥锁不够

`claimTurn`（`agentkit/run/runtime.go:207-241`）里的互斥锁回答的是「谁先到」。
它不回答「谁还活着」。于是有三个失效模式：

1. **持有者停滞。** 持有者攥着锁却没有推进——模型调用挂住、GC 停顿、loop 里
   有 bug。别人接管不了，会话卡死，唯一的出路是 `Abort`。
2. **持有者变僵尸。** 持有者被暂停过（虚拟机冻结、笔记本休眠、调试器断点）
   然后醒来。它的 `loopExited`（`agentkit/run/bridge.go:115-143`）会去清
   早已不属于它的状态——今天靠指针身份检查兜住，那是一道恰好免费的 fence。
3. **第二个进程。** 一旦有两个 openheron 进程能看到同一个会话目录，互斥锁
   就什么都保护不了：它是一把在某个地址空间里的锁，守护的却是另一个地址
   空间里的资源。

★ **停滞有两种，治法不一样。** **进程级**的停滞——一次漫长的 GC 停顿、虚拟机
冻结、笔记本休眠——会停住一切，包括续租，于是租约自然过期。**run 级**的停滞
——模型调用挂住、loop 里的 bug、一次永不返回的写——只停住 run 自己的活儿；
靠定时器续的租约看不见它，因为定时器不关心 run 在干什么。

★ 所以 fence 治 2，仲裁者治 3，而 1 需要在租约之上加一层**进展驱动的续租**
（§5.2）。靠定时器的租约只能让进程级那一半成立，run 级那一半今天怎么卡的，
将来还怎么卡。

### 5.2 租约

```go
// storage/lease.go
package storage

// Lease is a time-bounded claim on a session.
//
// ★ Epoch is the fence: it is monotonically increasing per session, and every
//   durable write the holder makes carries it. A holder whose epoch is behind
//   the session's current epoch has been fenced out and must not write.
type Lease struct {
    RunID     string
    SessionID string
    Epoch     uint64
    Owner     string
    ExpiresAt time.Time
}

// LeaseArbiter decides who owns a session.
//
// ★ The arbiter does NOT allocate the epoch. The epoch has to be monotonic
//   across restarts, so it cannot live in a volatile structure — the ledger
//   allocates it (RunLedger.NextEpoch) and the lease carries the number it
//   was given. The arbiter's job is liveness: who holds the claim, and
//   until when. Keeping the two apart is what lets the ledger's read rule
//   stay simple: "highest epoch wins" needs no knowledge of liveness, and
//   "the lease expired" needs no knowledge of write order.
type LeaseArbiter interface {
    // Claim acquires the session's lease. It fails with ErrLeaseHeld when
    // another run holds a live lease, and succeeds when the lease is expired
    // or is already ours.
    Claim(ctx context.Context, sessionID, runID, owner string, epoch uint64, ttl time.Duration) (Lease, error)
    // Renew extends a lease the caller still holds.
    Renew(ctx context.Context, l Lease, ttl time.Duration) (Lease, error)
    // Release gives the lease back.
    Release(ctx context.Context, l Lease) error
    // Fence reports the session's current epoch.
    Fence(ctx context.Context, sessionID string) (uint64, error)
    // Held reports the session's current lease, if any.
    Held(ctx context.Context, sessionID string) (Lease, bool)
}
```

★ **正是租约让闸门说得出「停滞」而不只是「忙」。** 准入闸门问的是会话忙不忙；
答案过去来自轮次槽位——「有一轮在跑」——而一个停滞的持有者会把那个槽位一直
占着。现在闸门也问仲裁器：租约过期的持有者已经不再占着这个会话，别的 run
可以接手（`agentkit/run/runtime.go`——`leaseExpired`）；而 epoch 说得出此后
两个里谁才是持有者。

★ **仲裁器是内存的那个**（`storage/memarbit`）。它在一个进程内裁决，什么都
活不过重启——账本的对账器会收掉崩溃留下的 run（§3.3），下一轮拿一个新的
epoch。

★ **run 会续租 —— 靠进展，不是靠定时器。** 租约在 run **报告进展**时续：
run 发布它的事件，每发布一次就是一次进展报告。TTL（默认 10 分钟，
`agentkit/run/lease.go`——`DefaultLeaseTTL`）是**预算**，不是日程：一次 TTL
之内什么都没报告的 run 丢掉租约，不管它是不是活着的。续租被节流到 TTL 的
四分之一：流式模型每个 token 都报一次进展，每个 token 续一次租约等于每个
token 抢一次锁，白白地。

★ **TTL 是配置项**（`server.lease_ttl`，默认 10m）。它必须**长于 provider
自己的超时**——provider 的超时负责收拾真正挂死的调用，租约只需要比「慢但
活着」的调用撑得更久——但两者住在不同的地方（超时按端点在 `llm/<id>.yaml`
里），所以这个关系是运维要守的，不是代码能强制的。`server/` 不热重载：改了
要重启。

★ **为什么不靠定时器。** 定时器续租会让一个卡在模型调用里的 run 一直攥着
租约——续租的 goroutine 不关心 run 在干什么，它只是一遍遍说「我还活着」。
而这正是 §5.1 说的「持有者停滞」，定时器看不见它。进展驱动的续租能看见：
没有进展就没有续租，租约过期，会话就能被别人接手。

★ **慢模型不是停滞的 run —— 但 TTL 得知道这个区别。** 一个要四分钟才回话的
模型没有进展可报，30 秒的 TTL 会在它好好干活的时候把它 fence 掉。解法是
**一个比 provider 自己的超时更长的 TTL**，不是一个说谎的定时器：provider
的超时（`agentkit/templates/files/llm/_generic.yaml:10`，出厂 5m）负责收拾真正挂死的
调用，TTL 只需要比「**慢但活着**的调用」撑得更久。两者都是配置。

★ **租约管的是一轮，不是一个会话的空档。** 一个以抛审批结束的 run 会释放
它的租约——那一轮已经结束。让会话不接新 query 的是**待审批**，那是会话级的
持久事实，不是租约（`agentkit/run/runtime.go:348-353`）。两把锁，两个作用域：
租约说「这一轮现在占着会话」，审批说「这个会话欠用户一个答复」。

★ **丢租约等于把自己 fence 掉。** 如果续租失败是因为别人持有着租约，这个
run 就取消自己的上下文并停下。它不会继续为一个自己已经不拥有的会话写
「running」行——fence 让这些写入无害，但停下来更好。

### 5.3 fence

fence 是一个按会话单调递增的整数。它只回答一个问题：**这次写入来自当前
持有者吗？**

它在两个地方被强制执行，别处没有：

1. **读侧**（§3.2）：在未结束的 run 里，epoch 最高的那个是持有者。一条
   过期的行抢不了头。
2. **写侧**（可选但推荐）：`ledger.Append` 拒绝 epoch 低于会话当前 epoch 的行。

★ **fence 的职责是排序，不是持久性。** 一个崩掉的进程留下的行还在盘上；告诉你
它们过期的是**租约**，不是 epoch。把两者分开，正是账本读规则能保持简单的
原因：「epoch 最高者胜」不需要知道谁还活着，「租约过期了」也不需要知道写入
顺序。

### 5.4 租约和 fence 干的是两件不同的事

| | 租约 | fence |
|---|---|---|
| 回答的问题 | 现在谁可以跑？ | 这条事实谁可以记？ |
| 生命周期 | 有时限，靠进展续租 | 单调递增，绝不复用 |
| 它防住的失效 | 停滞的持有者永远占着会话 | 僵尸持有者污染记录 |
| 由谁强制 | 仲裁者（续租、过期） | epoch 比较（读与写） |

★ **租约检测的是「持有者不再上报」，不是「持有者不再思考」。** 有了进展驱动
的续租，两种停滞就分得开：进程冻住了 → 什么也不报 → 丢租约；模型慢但活着
→ 同样什么也不报 → 靠一个比 provider 超时更长的 TTL 兜住，而不是靠租约。
租约回答的是「这个会话我能接手吗」，所以它必须能在没有人参与的情况下回答
「能」。

★ 今天代码里有 fence、没有租约：epoch 是真的
（`storage.RunLedger.NextEpoch` 分配它，`storage.ActiveRun` 比较它），但所有权
仍然由一把互斥锁决定。`markTurnDone`（`agentkit/run/bridge.go:51`）和
`loopExited`（`agentkit/run/bridge.go:119-124`）里的指针身份检查是建立在地址上
的 fence——它们在一个进程内有效，出了这个进程就不作数。租约来取代它们。

## 6. D4 —— 驱动可插拔

### 6.1 接口住在 `storage`

`storage`（[architecture.md](architecture.md) 分层表里的 L1）本来就是持有
会话与待审批接口的包（`storage/storage.go`）。run 账本和租约仲裁者
也该住在那里，理由相同：`agentkit/run`（L2）只能依赖接口，而 API 层（L3）
不许装配业务逻辑（AGENTS.md 的铁律）。

★ **账本这一半已实现，租约那一半还没有。**

```go
// storage — interfaces and value types
type RunLedger interface { … }      // storage/runledger.go   ← 已实现
type LeaseArbiter interface { … }   // storage/lease.go       ← 还不存在
type Run struct { … }               // storage/runledger.go   ← 已实现
type Lease struct { … }             // storage/lease.go       ← 还不存在

var ErrLeaseHeld  = errors.New("lease held by another run")
var ErrFenceStale = errors.New("the run's fence is stale")   ← storage/runledger.go
```

★ `ErrFenceStale` 已经定义在账本的契约旁边，因为 fence 已经是账本里的一个
事实：epoch 在那里分配、也在那里比较。这个错误码是**可选的**写侧检查在仲裁器
出现之后要返回的东西。

### 6.2 驱动，按到达顺序

| 次序 | 驱动 | 账本 | 仲裁者 | 它换来什么 |
|---|---|---|---|---|
| 1 | `storage/memarbit` | 内存 | 内存 | 协议本身，单进程跑通。什么都不活过重启。 |
| 2 | `storage/filestore` | `runs.jsonl` | 内存 | 持久性：崩溃留下痕迹，对账器能把它收尾。 |
| 3 | 数据库驱动 | 一张表 | 一张表，epoch 由 `UPDATE … SET epoch = epoch + 1` 分配 | 多个进程共用一个会话存储。 |

★ **「先内存后 DB」这句话的诚实读法。** 本仓库根本没有数据库——整个存储就是
文件（`storage/filestore`）。引入 DB 驱动不是「以后调一调」，而是新增一个
依赖和一套部署故事。而中间那一步——**文件**——在这里几乎是白捡的：
transcript 本来就是一个追加式的 JSONL 文件，`persist` 本来就有原子重写和
文件锁。

★ 所以实际的次序是**内存 → 文件 → 数据库**，而内存驱动不是一次性用品：它是
那个不依赖任何 IO 就把**协议**（租约、fence、对账、订阅）证明掉的东西。文件
驱动随后用一个每会话一个新文件换来持久性。

★ **文件驱动的账本已经实现**（`storage/filestore/runledger.go`），所以表中
第二行今天有一半是真的：账本就是那个文件，仲裁器还不存在。epoch 来自账本而
不是仲裁器——§3.2 讲了为什么，而数据库那一行也得用同样的办法发号。

### 6.3 内存驱动的代价

如果账本只在内存里，那么：

- 崩溃会丢掉 run 记录 → C2 没被修掉，对账器没有任何东西可对。
- 重启分不清「本来在跑」和「从没跑过」→ 侧栏那个点又开始说谎，
  和上一轮之前一模一样。

★ 这就是为什么在本提案里文件驱动不是可选项。「先内存」对**协议**是对的次序，
对**账本**是错的次序。提案把两者分开：**账本**随包就持久（文件），
**仲裁者**随包是内存的，而它是以后变成数据库行的那个东西。

## 7. 事件管线：从单消费者到发布订阅

★ **本节已实现**（除了 §8.1 的 attach）。推导搬进了 `agentkit/emit`，run
自己消费自己的事件，连接退化成订阅者。

### 7.1 为什么原来的管线把耦合焊死了

transport 的 `streamEvents` 一次干两件事：

- 它**渲染** AG-UI 帧（`e.emit…`），
- 它**推导**归档载荷——中间产物、审批、压缩与用量。

★ 推导没法做两遍：流式分支要在模型的流上调用 `mo.MessageStream.Recv()`，
再把分片折成一条消息。**第二个读者会把分片劈开。** 于是渲染器必然是唯一
消费者——而渲染器就是 SSE 的出口，于是出口拥有了 run 的产出。连接一死，
这一轮就永远归档不了。

### 7.2 拆分

★ **已实现。** 两件事，两个包：

| 之前 | 现在 |
|---|---|
| `transport` —— 一趟做完推导与渲染 | `agentkit/emit` —— 只推导：eino event → 归档产物 + `contract.RunEvent` |
| `transport` —— AG-UI 发射器 | `transport` —— 只渲染：`contract.RunEvent` → AG-UI 帧 → SSE |

`agentkit/emit` 是一个 L1 包：它只依赖 `contract`、`message`、`approval`、
`helpers` 和 `obs`，不依赖任何上层。`agentkit/run` 调它；`transport` 不需要
调——transport 收到 `contract.RunEvent` 然后渲染。

★ 线上没变：帧还是一个对一个的。变的是**谁在推导**。transport 自己的契约测试
（`transport/transport_test.go`）仍然断言那些帧序列，它同时驱动推导与渲染
——这次拆分不该在网线上看得见。

### 7.3 run 的事件词汇表

```go
// contract/runevent.go
package contract

// RunEventKind is what happened in a run.
type RunEventKind uint8

const (
    RunEventTextStart RunEventKind = iota
    RunEventTextDelta
    RunEventTextEnd
    RunEventReasoningStart
    RunEventReasoningDelta
    RunEventReasoningEnd
    RunEventToolStart
    RunEventToolArgs
    RunEventToolEnd
    RunEventToolResult
    RunEventAnnounce    // a message that produced no visible delta
    RunEventCompaction
    RunEventContextUsage
    RunEventInterrupt
    RunEventRetry       // the provider is retrying
    RunEventError
)

// RunEvent is one thing that happened in a run.
//
// ★ It is openheron's own vocabulary, not AG-UI's. The run must not depend on
//   the wire format: the transport maps RunEvent onto AG-UI frames, and a
//   second wire format would be a second renderer, not a second run.
type RunEvent struct {
    Kind RunEventKind
    RunID string
    MsgID string
    Text  string
    Role  string
    Owner string
    ToolID string
    ToolName string
    Sub  string
    Agent string
    At   time.Time
    Usage ContextUsage
    Interrupts []Interrupt
    Pending []PendingInterrupt
    Code string
}
```

★ 没有 `RunEventMessage`，也没有 `RunEventDone`。归档的消息是推导的**产物**
——它们随推导一起回来，而不是从事件流里过，因为它们是关于 run 的事实，不是
发生在观看者面前的事。而「结束」不是一个事件：它是**订阅通道的关闭**
（§7.4）。

★ 为什么不直接复用 AG-UI 的事件类型：`contract` 是 L0 叶子（见
[architecture.md](architecture.md) 的分层表），而 AG-UI 是传输层的关注点。
把 AG-UI 的类型放进 `contract`，等于让领域层依赖线上格式。这层映射实际上
是一一对应的，因此 `transport` 保持很薄。

### 7.4 订阅者

★ **已实现。** run 向它的订阅者发布；订阅者就是那条 SSE 连接。

```go
// agentkit/run/publish.go
type publisher struct {
    mu     sync.Mutex
    window []contract.RunEvent              // a BOUNDED window, not the turn's history
    subs   map[*subscription]struct{}
    closed bool
}
```

- 慢的订阅者拿到一个**有界**通道；通道满了，run 就**踢掉这个订阅者**。慢的
  读者绝不能拖慢 run——那就是从前「出口卡住循环」的坑。
- ★ **被卡住的订阅者才是要紧的那一种，它和「慢」不是一回事。** 慢的读者终究
  会把通道排空；卡住的那个是 socket 写永不返回：对端消失了却没发 RST，内核
  还在往一个没人读的缓冲区里塞字节。所以发布方**绝不能在满通道上等**——它
  踢掉订阅者而不是等它，run 继续走。拆分之前，同一种情况会卡住 run，因为写
  socket 的就是 run 自己。
- 消失的订阅者什么也带不走。run 继续跑，继续归档。
- ★ **run 的事件日志是有界的窗口，不是这一轮的历史。** 中途挂上来的客户端从
  窗口里拿到那些事件，于是它能渲染这一轮的尾巴，不必等这一轮结束。窗口留
  最后 N 个事件（N = 1000，`agentkit/run/publish.go`——`windowSize`），最老的
  被丢掉，于是长的一轮花的是**固定**的内存，而不是一个随输出变长的内存。run
  结束时把窗口丢掉。

★ **窗口的回放与登记在同一把锁里**，所以一个订阅者每个事件恰好看一次：先是
窗口里的那些，然后是随后来的那些。

★ **run 在这一轮**结束**时才关掉订阅——而一个以抛审批结束的这一轮，在审批
落盘之前不算结束。** 这不是细节：客户端是从通道关闭那一刻知道「有审批」的
（渲染器在通道关闭时写 `RUN_FINISHED`），所以一个在审批落盘之前就关掉的通道，
等于邀请客户端去回答一个服务端还找不到的审批——那个答复会被
`no_pending_interrupt` 拒掉，而用户看不出理由。审批是在 loop 退出时写的（它
要恢复的那个检查点必须先在），所以那一轮的发布方改在那里关。

★ **两件事，两个存储。** 「以后再回来」是 **transcript** 的活：客户端打开一个
会话时从 `GET /v2/sessions/{id}/messages` 水合，看到的是**已经结束**的那一轮
（`web/src/lib/store/actions.ts:86` → `seedFromHistory`）。日志只服务「它还在
跑的时候挂上来」——一个实时视图，永远不是最终视图。

★ **所以窗口无论有没有人看都会被填满**，没有分支需要判断。它的大小是一个
上界，于是没人看的一轮为一份没人读的日志付**恒定**的代价——而 run 真正的
产出（transcript）反正都要写。另一种做法（等有人挂上来再开始建窗口）意味着
猜——而且会漏掉最要紧的那种情况：挂到一半的客户端。

★ **要不要持久化是另一个决定**——见 §13 Q1。目前只在内存里。

### 7.5 刻意不做的事

- **不做多订阅者协议。** 两个浏览器可以同时挂到一个 run 上（那只是两个
  订阅者），但 run 除了那条有界通道之外不为他们做仲裁。
- **不做持久化的事件日志。** 见 §13 Q1。
- **不改 AG-UI 的线上格式。** 帧还是那些帧，只是换了个生产者。

## 8. API 表面

### 8.1 改动的路由

**`POST /v2/run`** —— 三种情况，由客户端送来的 `runId` 决定：

| 会话有 | 请求带 | 结果 |
|---|---|---|
| 没有活着的 run | 一个 `runId`（或者没有——服务端自己铸） | **创建**这个 run；响应里流出它 |
| 有一个活着的 run，id **正是**它 | 同一个 `runId`，**而且没有问题** | **挂上**：订阅这个活着的 run，并回放它的窗口 |
| 有一个活着的 run，id **不同** | 任意 `runId` | **409 `session_busy`**——与今天一致 |

★ 第一行**已实现**：客户端不发 run id 时由服务端铸一个
（`api/v2_run.go`），并把它传给运行时（`api/v2_run.go:218`），于是帧里的 id
和账本行里的 id 是同一个。

★ 第二行**已实现**（服务端）。要挂上的那个请求**不带问题**：带问题就意味着
「开始一轮新的」，在这里开始一轮新的等于把用户那句话悄悄跑第二遍。服务端按
id 找到那个活着的 run（`agentkit/run/runtime.go`——`LiveRun`），订阅它并渲染
它的窗口——于是晚到的客户端看到的是这一轮的尾巴，不是一条空流。

★ **结束的 run 挂不上。** 客户端只能从 transcript 水合；这一轮自己的事实在
扫描里。服务端回 **404 `run_not_live`**，而不是开始一轮新的——客户端对这个
拒绝的回答是「去加载历史」，它必须能把它和「会话可以重试」分得开。

★ 第二行是新增的能力：重连。今天客户端无法续上它弄丢的流
（`web/src/lib/store/actions.ts:250-254` 压根不发 `runId`），所以连接一断，
这一轮的输出就没了。有了已知的 run id，客户端要同一个 run，就能拿到剩下的
部分。

★ 因此客户端必须**留着 run id**。扫描会返回它（`activeRunId`），SSE 帧里本来
就带着它（AG-UI 的 `runId`）。

**`GET /v2/sessions/{threadId}`** —— `state` 来自扫描。★ **已实现**：
`stateOf`（`api/v2_sessions.go:506`）问的是 `StatusOf`，而 `StatusOf` 读的是
账本。`api/v2_sessions.go:94` 返回的 `runId` 不再是空的——它是活动 run 的 id，
也就是扫描报作 `activeRunId` 的同一个值。★ **不是**第二个字段：一个值，
一个名字。

**`POST /v2/sessions/{threadId}/abort`** —— 中止的是**run**，不是 loop。
它取消 run 的上下文，（有界地）等终态行落地，并返回它中止的那个 run id。
★ run id **已实现**（`api/v2_sessions.go:405`）。其余是提案：现在的实现
（`api/v2_sessions.go:385` → `agentkit/run/runtime.go:565`）是直接操纵
loop；在提案下，run 自己的收尾会写那一行，所以中止就是「停掉这个 run」，
那一行随后而来。

### 8.2 新增的路由

★ **已实现。**

```
GET /v2/sessions/{threadId}/runs              the scan: every run of the session
GET /v2/sessions/{threadId}/runs/{runId}      one run
```

两个都读账本（`api/v2_sessions.go:423`）。两个都不写。

### 8.3 错误信封

不新增错误码。现有的那些已经能表达意思：

| 码 | 在哪儿产生 | 含义 |
|---|---|---|
| `session_busy` | `api/v2_types.go:48` | 另一个 run 占着这个会话 |
| `run_not_live` | `api/v2_types.go` | 挂上的那一轮已经不在跑了 |
| `no_pending_interrupt` | `api/v2_types.go:36` | 这个审批已经被答过了 |
| `turn_superseded` | `api/v2_types.go:49` | 为兼容保留；抢占那条路已经没有了 |
| `turn_start_timeout` | `api/v2_types.go:50` | 这个 run 始终没能变得可流出 |

★ `turn_superseded` 留着，因为删掉它会打断按它分支的客户端——但在提案下，
它只可能由启动超时那条路的兄弟产生，绝不会由接管产生。会话并发设计文档的 §6.3
已经提过这一点；本提案不改变它。

★ `run_not_live` 是唯一新增的错误码，而它存在是因为挂上需要它。「你要的那一
轮已经没了」既不是「会话忙」也不是「没有这个 run」：客户端对它的回答是「去
加载历史」，它必须能把这和「会话可以重试」分得开。

## 9. 前端

★ **已实现。** 重新挂上（§9.2）与扫描出来的 busy 标记（§9.1）都在树里：服务端
说真话，客户端去问。

### 9.1 busy 标记变成扫描出来的事实

★ **已实现。** `ConversationState.busy`（`web/src/lib/types.ts`）的含义是
「这个会话有一个本界面不拥有的活着的 turn」。它**只由扫描设置和清除**：

- **置上**：扫描报出一个本界面不拥有的 `activeRunId`。
- **清掉**：扫描报出没有活跃的 run。

★ `stop()`（`web/src/lib/store/actions.ts`）改了形状：它发 abort，然后
**重新扫描**，而不是自己去置 `busy: false`。乐观地置它，正是这个设计要消灭的
「相信通知」式的错误——abort 说「我问了」，扫描说「会话空了」。

★ 流的结束是另一处：流结束什么也说明不了这个会话（别的界面可能正在跑），
所以客户端在那里也重新扫一次。

★ **扫描失败就让标记留在原样。** 猜正是扫描要治的病：扫不出来的时候不能把
忙标记清掉——那行字是用户和「被拒的消息」之间唯一的挡板。

★ 本界面自己拥有的那一轮不算「忙」。扫描拿活动 run 的 id 和会话自己的 run id
比 ✓。

### 9.2 挂上

★ **已实现。** store 记着自己启动的那个 run 的 id（`RUN_STARTED` 帧带着它，
归约器把它留下来）。当流**没有以终止帧结束**时，是连接死了——不是 run 死
了——客户端于是问扫描：这个 run 还活着吗？

- 活着 → 用同一个 `runId`、**不带问题**再 `POST /v2/run` → **挂上**，这一轮
  剩下的部分渲染出来。
- 不活着 → run 结束了，transcript 就是真相，把它加载进来。

★ **「流没有以终止帧结束」就是这个信号。** `RUN_FINISHED` 说 run 结束了，
`RUN_ERROR` 说它坏了；两者都没有的流结束，说的是连接断了。这是协议层的事
实，也是客户端唯一需要的那一个。

★ 这是整个设计看得见的回报：**第二个标签页可以围观一个正在跑的 turn，
而不是被拒之门外**；掉线的连接可以续上，而不是丢掉输出。

### 9.3 store

store 的形状几乎不变——reducer 仍然是纯函数，选择器仍然返回引用稳定的值
（AGENTS.md 的规矩）。变的是两个值的**来源**：

| 值 | 今天 | 提案 |
|---|---|---|
| `busy` | 从流的结束和 409 推断出来 | 扫描 |
| run id | ✓ 留着了（`web/src/lib/events.ts` —— 归约器），并在重新挂上时发出去 | 留着，并在重新挂上时发出去 |

## 10. 改动落点

★ **✓ = 已落地**，其余仍是提案。

| 文件 | 改动 |
|---|---|
| `storage/runledger.go` | ✓ **新增** —— `Run`、`RunState`、`RunLedger`、`FoldRuns`、`ActiveRun` |
| `storage/lease.go` | ✓ **新增** —— `Lease`、`LeaseArbiter`、`ErrLeaseHeld` |
| `storage/memarbit/arbiter.go` | ✓ **新增** —— 内存仲裁器 |
| `storage/filestore/runledger.go` | ✓ **新增** —— `runs.jsonl` 驱动 |
| `storage/filestore/session.go` | ✓ 会话目录多出 `runs.jsonl`；`FileSession` 多一个账本句柄 |
| `storage/filestore/manager.go` | ✓ 启动时的巡检给未结束的 run 收尾 |
| `agentkit/emit/` | ✓ **新增** —— 从 `transport` 里拆出来的推导层 |
| `contract/runevent.go` | ✓ **新增** —— `RunEvent`、`RunEventKind` |
| `agentkit/run/runtime.go` | ✓ `Submit`/`Resume` 带着 run id；`claimTurn` 打开那一行并申领租约；`StatusOf` 读扫描 |
| `agentkit/run/publish.go` | ✓ **新增** —— 发布方、订阅与窗口 |
| `agentkit/run/turn.go` | ✓ `RunID` 被赋值；turn 自己收掉自己那一行。**往后：** `Turn` 变成带订阅者的 `Run` |
| `agentkit/run/loop.go` | ✓ 终态行写在归档之后；run 自己推导并归档。**往后：** 租约 |
| `agentkit/run/bridge.go` | ✓ loop 退出时收掉那一行与发布方；`sessionState` 降级为缓存 |
| `agentkit/run/lease.go` | ✓ **新增** —— run 的租约、进展驱动的续租 |
| `agentkit/run/errors.go` | ✓ `ErrSessionBusy` 保留；租约错误映射到它 |
| `transport/transport.go` | ✓ 变成渲染器：`contract.RunEvent` → AG-UI 帧 |
| `api/v2_run.go` | ✓ run id 只铸一次并往下传；处理器订阅而不是抽干。**往后：** 挂上 |
| `api/v2_sessions.go` | ✓ `state` 来自扫描；两条扫描路由；中止报出它停掉的 run |
| `api/v2_files.go` | ✓ 两条新路由 |
| `web/src/lib/store/actions.ts` | ✓ 留住 run id；✓ 流断了之后重新挂上；重新扫描而不是假定 |
| `web/src/lib/store/history.ts` | `busy` 来自扫描 |
| `web/src/lib/api.ts` | ✓ run 请求带上 `runId`；✓ 加上 runs 扫描 |
| `web/src/lib/events.ts` | ✓ 归约器留住 run id |
| `web/src/lib/types.ts` | 会话 meta 上加 `activeRunId` |

## 11. 测试

★ **✓ = 测试已存在**，其余是计划。

**Go —— 账本**（`storage/filestore`、`storage/filestore/runledger_test.go`）：
- ✓ 一行 run 记录能活过存储的重启；
- ✓ 每个 run id 取最后一行；
- ✓ 在未结束的 run 里，epoch 最高的那个是持有者，哪怕 epoch 更低的那个写得更晚；
- ✓ 被截断的最后一行被丢弃，run 退回它上一刻的状态；
- ✓ epoch 从不复用，连重启也不复用；
- ✓ 账本始终在自己的上界之内。

**Go —— 仲裁者**（`storage/memarbit/arbiter_test.go`）：
- ✓ 对一把活着的租约二次申领失败，报 `ErrLeaseHeld`；
- ✓ 过期的租约可以被申领，新的 epoch 更高；
- ✓ 被 fence 掉的持有者续租失败；
- ✓ **报告了进展**的 run 保住租约；
- ✓ **什么都没报告**的 run 丢掉租约——哪怕它活着。这就是 §5.1 说的 run 级停滞；
- ✓ 一次过期的释放不会把新持有者的租约放掉；
- ✓ fence 报出持有者的 epoch。

**Go —— runtime**（`agentkit/run/lease_test.go`）：
- ✓ 活着的持有者占着会话：第二个 run 被拒；
- ✓ **停滞的持有者不会永远占着会话**——租约是预算，闸门问的就是它；
- ✓ 报告了进展的 run 保住租约。

**Go —— runtime**（`agentkit/run`、`agentkit/run/runrow_test.go`、
`agentkit/run/publish_test.go`）：
- ✓ **没有任何订阅者**时，run 依然归档——这是「一轮比连接活得久」的直接检验；
- ✓ 订阅者消失不影响 run；
- ✓ 队列溢出的订阅者被**踢掉**，run 继续归档——§7.4 那个卡住的订阅者；
- ✓ run 的事件窗口**不超出它的上界**——没人看的轮次，代价是有界的；
- ✓ 晚到的订阅者先拿到窗口的回放，再拿到实时事件；
- ✓ run 在被交出去之前就已经入账；
- ✓ 终态行落在归档之后（账本说 `completed` 时，transcript 里已经有答案）；
- ✓ 没能启动的 turn 自己收掉自己那一行；
- ✓ 扫描报出活动 run，run 结束后就不再报它；
- ✓ resume 点名它答复的那一个 run；
- ✓ `StatusOf` 报出活动 run 的 id；
- **没有任何订阅者**时，run 依然归档它的消息——这是「一轮比连接活得久」的
  直接检验；
- 订阅者消失不影响 run；
- **通道溢出的订阅者被踢掉，run 继续归档**——这就是 §7.4 那个 socket 写永不
  返回的「卡住的订阅者」；
- 处理器死掉的 run 依然写出终态行（C3 那一幕）；
- 丢了租约的 run 会停下，并且不写 `running` 行；
- 没人挂上来的 run，它的**事件日志不超出窗口大小**——没人看的轮次，代价是有界的
  （§7.4）。

**Go —— 对账器**（`storage/filestore/runledger_test.go`）：
- ✓ 崩溃时留下的 `running` run 会在启动时被收成 `interrupted`，且发生在服务
  开始接受请求之前（钩子已经存在：`storage/storage.go`——
  `SessionManager.Inspect`）。

**Go —— API**（`api/v2_runs_test.go`）：
- ✓ 扫描列出所有 run 并报出活动的那一个；
- ✓ 会话自己的路由带着活动 run 的 id；
- ✓ 按 id 取一个 run，不认识的 id 报 404；
- ✓ 空闲会话扫出来是空列表。

**前端 —— 单测**：
- `busy` 只由扫描清除，不由流的结束清除；
- `stop()` 会重新扫描；
- 重新挂上时 run id 不变。

**E2E**（假后端，`web/test/e2e/`）：
- ✓ 掉线的连接重新挂上并收到这一轮剩下的部分（`web/test/e2e/reattach.spec.ts`）；
- ✓ 重新挂上时带着 run 的 id，且不带问题；
- 第二个标签页挂上一个正在跑的 turn 并看到它的输出；
- 409 仍然回滚乐观气泡（上一轮的行为必须活过这次改动）。

## 12. 分期

**第 1 期 —— run 变成一个事实。** ✓ **已落地。** 账本、那一行、扫描路由、
对账器。SSE 仍然驱动着 run，但 run 的**那一行**已经独立于连接被写下，于是
崩溃会留下痕迹。这一期修掉 C1 和 C2，并给前端一个真的可以扫的东西。

**第 2 期 —— run 自己消费。** ✓ **已落地。** `agentkit/emit` 的拆分、订阅者、
run 的事件窗口，以及**挂上**。run 自己读自己的事件，推导含义，归档结果；连接
订阅 run 发布的东西，而掉了线的客户端带着 run 的 id 回来，就能拿到这一轮剩下
的部分。这一期修掉 C3 和 C4——耦合本身。它也是「**卡住的订阅者不再是一种
场景**」的那一期：run 不再拥有 socket，wedge 住的连接根本够不着 run。

**第 3 期 —— 租约取代互斥锁。** ✓ **已落地。** 仲裁者、**进展驱动的续租**、
fence。闸门的行为不变（仍然是「一个会话一轮」，仍然是 409）；只是内部换掉了。
这一期治好了**两种**持有者停滞——进程级那一半靠「进程一停，租约就过期」，run
级那一半靠「续租由进展驱动」——并为第二个进程铺路。

★ **第 3 期只能排在第 2 期后面，理由就是那个续租。** 进展驱动的续租要求 run
能看见自己的进展，而在第 2 期之前 run 什么也看不见：是 transport 抽干事件、
再把归档载荷回吐。由连接进度驱动的续租会把「连接断了但 run 还活着」的租约
判死——而那正是租约要拆掉的耦合。

★ **次序是在第 1 期施工期间改的，理由值得写下来。** 原计划把租约放在第 2 期。
它不能放在第 2 期：**进展驱动的续租要求 run 能看见自己的进展**，而今天 run
看不见那些事件——是 transport 抽干它们，再把归档的产物回吐（`agentkit/run/loop.go:115`）。
一个由连接进度驱动的续租，会把「连接断了但 run 还活着」的租约判死，而这正是
租约要拆掉的那根耦合。所以管线必须先拆，租约才能诚实。

★ 新次序的好处是：**第 1 期不依赖任何一期**。它是关于 run 的一个事实，由 run
自己的 goroutine 写下；而它也是另外两期的地基——租约需要一个持久的 epoch，
而 epoch 是账本给的。

★ **第 2 期是最贵的一期**，也是唯一真正把 turn 与连接解绑的一期。第 1、3 期
单独做也值得：第 1 期让 run 的事实成真，第 3 期让所有权有界——那比今天好得多。

## 13. 待决问题

**Q1 —— run 的事件日志要不要持久化？窗口又该多大？**
每会话第三个文件（`events.jsonl`）能让一个几小时后才重连的客户端回放这一轮
的增量，而不只是它的最终 transcript。transcript 已经给出一个正确的最终
视图，所以这是锦上添花。代价是第三个文件、第三套裁剪策略、第三样会坏掉的
东西。

★ 这个问题的后半截比前半截要紧，而且**跟持久化无关**：**窗口有多大**
（§7.4）。一个随轮次一起长的日志是个只在高负载下才显形的内存泄漏，所以这个
大小是一道**上界**而不是一个默认值——和账本已有的那道上界一个性质（§3.3）。

**Q2 —— 第二个进程在范围内吗？**
租约和 fence 是为它设计的，但内存仲裁者跨进程什么也强制不了——它是一套
协议，不是一把锁。问题是：DB 驱动是真实需求，还是仅仅做准备。

**Q3 —— 谁可以回答审批？**
待审批是会话级的（`storage/storage.go:104`），所以两个界面都可能尝试。
`TakePendingApproval` 是原子的，于是第二个会拿到 `ErrNoPendingInterrupt`
（`agentkit/run/runtime.go:482`）——这是对的，但 UX 需要说明「别人已经
答过了」。要不要告诉第二个界面是**谁**答的？

**Q4 —— run 的那一行要不要带上用量合计？**
这里刻意不放（§3.1）：数字归 transcript 管。但一份「这一轮花了多少」的
run 列表，就得对 message log 做一次范围扫描。往行里缓存一个合计，就是给一个
目前是推导出来的数字安了第二个家——正是 AGENTS.md 警告的那种重复。

**Q5 —— `turn_superseded` 怎么办？**
为兼容保留（§8.3）。等代码上线之后，它所命名的抢占路径已经不存在了。经过
一个弃用窗口就可以退休。
