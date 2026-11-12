# 运行时设计

> 一句话：一轮是**一行事实**，不是一个连接；连接只是它的视图。

本文讲 openheron 的运行时：run 的生命周期、会话准入、上下文用量与压缩、日志装配。
四件事共用同一条主线 —— **谁拥有真相，谁只是被通知**。账本说「这一轮是什么」，
扫描说「现在在干什么」，SSE 流只是一条提示；准入闸门住在服务端，界面读服务端的
答案而不是自己的内存；用量与压缩都有唯一的计算点；日志只有一处装配。

每条结论都指着具体文件与行号，行号是核过的。本文按 AGENTS.md 的文档语言规则
**只有中文版** —— 架构与代码设计类文档不做双语。

配套阅读：[architecture_zh.md](architecture_zh.md)（代码怎么分层）、
[developer-manual_zh.md](developer-manual_zh.md)（怎么改代码）、
[user-manual_zh.md](user-manual_zh.md)（怎么用）。

---

## 0. 一句话

**一轮（run）是一个持久对象，一个连接（HTTP + SSE）是它的一个可选视图。**

拆开是四个断言，每一个都有代码撑着：

| 断言 | 落在哪 |
|---|---|
| 一轮有一行账本记录，崩溃也留下痕迹 | `storage/runledger.go`（契约）、`storage/filestore/runledger.go`（文件驱动） |
| 连接可以挂上、摘下、再挂上；连接死了一轮照样归档 | `agentkit/run/publish.go`（发布订阅）、`agentkit/emit/`（推导） |
| 所有权是租约 + fence，不是先到先得 | `storage/lease.go`、`storage/memarbit/arbiter.go` |
| 「现在在干什么」由扫描回答，不由推送回答 | `agentkit/run/runtime.go` 的 `ScanRuns`、`api/v2_sessions.go` 的 `v2SessionRuns` |

★ **这套形状不是为了好看。** 旧的形状把一轮绑在一条连接上：连接负责抽干事件流，
落盘靠连接回吐的东西。于是连接死了那一轮就永远归档不了，第二个界面只能看到说到一半
的对话，却完全没有提示说还有东西在跑。现在的形状把这三件事拆开 —— run 自己读
自己的事件、自己归档，账本记下它发生过什么，连接只是把它渲染出来。

---

## 1. run 是单位，连接是视图

### 1.1 那一行：run 的事实是什么

run 的记录是**生命周期**，不是载荷。transcript 已经管着内容与数字；这一行管的是
时序与所有权。

契约在 `storage/runledger.go`：`Run` 结构（`:134`）带 `RunID`、`SessionID`、
`Kind`（query / resume）、`State`、`Epoch`（它持有的 fence）、`Owner`、
`ParentRunID`（一个 resume 续接的那一轮）、三个时间戳、`ErrorCode` /
`ErrorMessage`、`MCPServers`（这一轮碰过的 server）与 `TreeFingerprint`。

★ **这一行只带生命周期事实。** 用量、消息条数、transcript 本身都属于会话的消息 ——
把它们塞进这一行，就是给同一个数字安第二个家，仪表盘迟早会自己跟自己不一致
（`storage/runledger.go:127-133` 的注释就是这条铁律）。

状态机在 `storage/runledger.go:32`：

| 状态 | 含义 |
|---|---|
| `starting` | 已申领，尚未执行 |
| `running` | 正在执行 |
| `waiting_approval` | 以抛审批结束 |
| `completed` | 正常结束 |
| `failed` | 出错结束 |
| `aborted` | 被用户停掉 |
| `interrupted` | 主人中途消失，被对账器收尾 |

★ **状态在线上是名字，不是数字**（`storage/runledger.go:55` 的 `String()`，
`:93` 的 `MarshalJSON`）。一份比二进制活得更久的账本，必须在常量重新编号之后仍然
读得懂。★ 而且**不认识的名字是错误，不是零**（`storage/runledger.go:101`）：零是
`starting`，一份把坏行读成 `starting` 的账本会让每一个跑完的 run 看起来又活了。
★ **`waiting_approval` 是终态**（`storage/runledger.go:83` 的 `IsTerminal`）：抛审批
的 run 已经结束了，回答它的是**新的一轮**（带着 `ParentRunID`），会话此时由待审批
那把锁占着 —— 是另一把锁、另一个生命周期。

两个字段值得单独说：

- **`Owner`** 是写下这一行的实例标识（`agentkit/run/runtime.go:160` 的
  `defaultInstanceID`：主机名 + 进程号）。它回答事故后的第一个问题：「这一行是谁写的」。
- ★ **`TreeFingerprint`** 是这一轮跑的那棵 agent 树的指纹
  （`agentkit/run/loop.go:147` 的 `turn.row.noteTree`）。恢复续接的是它所回答的那一轮，
  所以它必须跑在**同一棵树**上 —— 名字不够，因为同一个名字背后的 agent 可能已经被
  重建过；指纹才是「同一棵树」的凭证（`storage/runledger.go:169-176`）。

### 1.2 账本与归档屏障

账本是一个接口（`storage/runledger.go:187`）：`Append` 写一行完整快照、`List` 读出
每个 run 的一行、`NextEpoch` 分配 fence 号、`Reconcile` 收掉进程死时还开着的 run。

★ **每写一行是完整快照，不是增量。** 读方按 run id 取最后一行，跨 run 时由 epoch
裁决 —— 两条规则都与顺序无关，这正是「被 fence 掉的持有者无害」的原因：它写不回
自己那一行（`storage/runledger.go:179-186`）。代价是字段重复，在这个量级上无关紧要。

两个读规则分开住在两个函数里：

- `storage.FoldRuns`（`storage/runledger.go:214`）—— **同一个 run 内部**，「最后一行赢」。
- `storage.ActiveRun`（`storage/runledger.go:238`）—— **跨 run**，「未结束的 run 里
  epoch 最高的那个是持有者」。★ 不是「最后一行赢」：一个来自 epoch 3 的僵尸持有者在
  epoch 4 接管之后醒来写了一行，读者仍然认为 epoch 4 是持有者 —— 这就是 fence 在
  读侧起作用的地方，那里既便宜又不需要锁。

★ **epoch 由账本分配，不由仲裁器分配**（`storage/filestore/runledger.go:200`）。
epoch 必须跨重启单调，而它不能住在一个易失的结构里 —— 账本才是活得比进程久的那个东西。
租约带着账本发出来的号；租约管「还活着吗」，epoch 管「谁在前」。

文件驱动住在 `storage/filestore/runledger.go`：

| 事实 | 落点 |
|---|---|
| 账本文件名 `runs.jsonl`，与会话目录里的其它文件作伴 | `:29` |
| 上界 200 行；无限增长的账本就是泄漏 | `:36` |
| 追加写 + `Sync` —— 这一行是持久性屏障，要扛住崩溃而不只是进程退出 | `:109` |
| 裁剪走原子重写（tmp → rename），读者永远看不到半截账本 | `:126` |
| 坏行被丢弃而不是读失败：常见情形是崩在两次写之间 | `:173` |
| 对账：把进程死时还开着的 run 收成 `interrupted` | `:232` |

★ **`runs.jsonl` 不是 `state.json`。** `state.json` 每次变更都是整体重写；把一份会
变长的 run 列表塞进去，意味着每次状态迁移都要重写这个会话的全部历史。追加式账本
与 transcript 同形，复用同一套追加机制。

★ **对账跑在启动时，在服务开始接受请求之前**（`storage/filestore/manager.go:267`，
挂在 `Inspect` 钩子上，`storage/filestore/manager.go:219`）。一个进程死时还开着的
run 没有主人了：没有东西会结束它，也没有东西会归档它的消息。留着那一行会让会话
永远读起来是忙的。★ 而且对账器不替别人背锅 —— 原来的 `Owner` 留着
（`storage/filestore/runledger.go:229-231`）。

#### 归档屏障

★ **终态行写在归档落地之后。** 单是这个次序，就让忙标记天然诚实：扫描看到
`completed` 就知道消息已经在盘上，不必等任何超时。

实现顺序在 `agentkit/run/loop.go:212-216`：`rt.archive` → `turn.markArchived()` →
`rt.finishRun`。`finishRun`（`agentkit/run/loop.go:277`）把消费者的错误带进终态：
模型超时记作 `failed`，用户停掉记作 `aborted`（`agentkit/run/turn.go:330` 的
`failedState`）。★ 少了这一步，账本就成了唯一会撒谎的地方 —— 它会把一个中途死掉
的 turn 记成 `completed`。

★ **一个例外：以抛审批结束的 run 要等 loop 退出。** 审批和 checkpoint 是在那里写的
（`agentkit/run/bridge.go:227` 的 `writeHeldApproval`），而扫描不能比「用户要答的那
个问题已经落盘」更早地宣布这一轮结束。所以那一行的终态写在 `closeApprovalRow`
（`agentkit/run/bridge.go:180`），排在审批落盘之后。

★ **第一行在 loop 启动之前就写。** `claimTurn` 先 `openRunRow`
（`agentkit/run/runtime.go:298`），再开跑；写不进去就把槽位还回去并拒绝 —— 一个
记不下来的 run 不能被拥有（`agentkit/run/runtime.go:300-304`）。

### 1.3 通知是提示，扫描才是真相

服务端：

| 事实 | 落点 |
|---|---|
| `GET /v2/sessions/{id}/runs` —— 扫描，回答「发生过什么、正在发生什么」 | `api/v2_sessions.go:422` |
| `GET /v2/sessions/{id}/runs/{runId}` —— 取一行 | `api/v2_sessions.go:443` |
| 扫描读的是账本，不是易失的运行时状态 | `agentkit/run/runtime.go:932`（`ScanRuns`） |
| 会话的 `state` 来自扫描 | `api/v2_sessions.go:505`（`stateOf`）→ `agentkit/run/runtime.go:804`（`StatusOf`）→ `agentkit/run/runtime.go:842`（`activeRun`） |
| 扫描**永不写**：会修账本的读，就分不清「是什么」与「刚被改成什么」 | `api/v2_sessions.go:418-421` |

扫描的形状是 `runsView`（`api/v2_types.go`）：`items` 是每一行 run（`runId`、
`state`、`kind`、`epoch`、时间戳，失败时带 `error`），`activeRunId` 是此刻占着
这个会话的那一个。

★ `error` 只在 run 失败时出现，`activeRunId` 只在有活动 run 时出现 —— 空闲会话扫出来
是 `{"items": []}`。一个永远存在却永远为空的字段，是客户端还得学会忽略的噪音。
`activeRunId` 是 UI 最需要的那个字段：它让一个 run **可以被挂上**。

★ **易失的 map 降级为缓存。** `sessionState`（`agentkit/run/bridge.go:39`）留着 ——
它持有活的 loop 和活的 turn，这两样本来就是内存里的东西 —— 但它不再是**真相**。
`StatusOf` 混着一个易失来源（map 里的时间戳）和两个持久来源（会话文件里的待审批、
账本里的活动 run），现在由持久的那一边决定阶段。

客户端：

| 标记从前从哪来 | 标记现在从哪来 |
|---|---|
| 流结束 → 假定空闲 | 流结束 → **重新扫描**（`web/src/lib/store/actions.ts:369`） |
| `stop()` 乐观地置 `busy: false` | `stop()` → 发 abort → **重新扫描** → 再决定（`web/src/lib/store/actions.ts:574`） |
| 409 `session_busy` → 标记忙 | 不变 —— 但这条消息背后现在是一个扫描出来的事实 |
| 页面加载 → 读 `state` | 不变 —— 扫的是账本 |

★ **一句话规则：客户端只有在扫描发话时才可以清掉 `busy`。** 扫描失败就让标记留在
原样（`web/src/lib/store/actions.ts:452-454`）—— 猜正是扫描要治的病。

★ **「栅栏换扫描」到客户端为止，而且只能到客户端为止。** 响应路径上那道栅栏回答的
是另一个问题：「一个刚读完这个响应结尾的调用方，能不能立刻发下一条？」账本回答得
比栅栏更晚 —— 终态行写在归档之后，那句「这一轮结束了」落地的那一刻，正是栅栏打开
的那一刻。所以换成扫描的**准入闸门**会在同一个窗口里拒绝下一条消息。扫描买到的是
「客户端不必再从流里去推断空闲」；它买不到「服务端不用等就能给出的服务端承诺」。
这个不对称是对的，见 §2.3。

### 1.4 租约与 fence：两件不同的事

租约（`storage/lease.go:23`）带 `RunID`、`SessionID`、`Epoch`、`Owner` 与
`ExpiresAt`；仲裁器（`storage/lease.go:58`）有五个方法：`Claim`、`Renew`、
`Release`、`Fence`、`Held`。

★ **仲裁器不分配 epoch。** epoch 必须跨重启单调，所以它不能住在易失结构里 ——
账本分配它（`RunLedger.NextEpoch`），租约带着拿到的号走。仲裁器的活儿是**存活性**：
谁占着这个会话，占到什么时候。两者分开，账本的读规则才能保持简单：「epoch 最高者
胜」不需要知道谁还活着，「租约过期了」也不需要知道写入顺序
（`storage/lease.go:45-57`）。

| | 租约 | fence |
|---|---|---|
| 回答的问题 | 现在谁可以跑？ | 这条事实谁可以记？ |
| 生命周期 | 有时限，靠进展续租 | 单调递增，绝不复用 |
| 它防住的失效 | 停滞的持有者永远占着会话 | 僵尸持有者污染记录 |
| 由谁强制 | 仲裁者（续租、过期） | epoch 比较（读与写） |

★ **停滞有两种，治法不一样。** **进程级**的停滞 —— 一次漫长的 GC 停顿、虚拟机冻结、
笔记本休眠 —— 会停住一切，包括续租，于是租约自然过期。**run 级**的停滞 —— 模型
调用挂住、loop 里的 bug —— 只停住 run 自己的活儿；靠定时器续的租约看不见它，因为
定时器不关心 run 在干什么。⇒ 所以 **fence 治僵尸，仲裁者治跨进程，而 run 级停滞
需要在租约之上加一层进展驱动的续租**。

★ **进展驱动的续租。** run 发布它的事件，每发布一次就是一次进展报告；发布方上的
`onProgress` 就是续租的触发点（`agentkit/run/runtime.go:318`）。TTL 是**预算**，
不是日程：一次 TTL 之内什么都没报告的 run 丢掉租约，不管它是不是活着的
（`agentkit/run/lease.go:105`）。★ 续租被节流到 TTL 的四分之一：流式模型每个 token
都报一次进展，每个 token 续一次租约等于每个 token 抢一次锁。

★ **TTL 是配置项**（`server.lease_ttl`，`agentkit/spec/decl/decl.go:297`；默认 10 分钟，
`agentkit/run/lease.go:20`）。它必须**长于 provider 自己的超时** —— provider 的
超时负责收拾真正挂死的调用，租约只需要比「慢但活着」的调用撑得更久。两者住在不同的
地方（超时按端点在 `llm/<id>.yaml` 里），所以这个关系是运维要守的，不是代码能强制的。
`server/` 不热重载：改了要重启。

★ **丢租约等于把自己 fence 掉。** 续租失败意味着别人持有着租约，这个 run 就取消
自己的上下文并停下（`agentkit/run/runtime.go:318-325`）。它不会继续为一个自己已经
不拥有的会话写 `running` 行 —— fence 让这些写入无害，但停下来更好。

★ **闸门现在会问仲裁器。** `ensureNoLiveTurn` 与 `claimTurn` 都问「上一个持有者的
租约过期了吗」（`agentkit/run/runtime.go:460`、`agentkit/run/runtime.go:280`）：过期
的持有者已经不再占着这个会话，别的 run 可以接手，而 epoch 说得出此后两个里谁才是
持有者。★ 没有仲裁器时一律按「没过期」处理（`agentkit/run/lease.go:28`）—— 闸门
不能因为没人接上仲裁器就变弱。

### 1.5 驱动可插拔

接口住在 `storage`（L1 层），因为 `agentkit/run`（L2）只能依赖接口，而 API 层（L3）
不许装配业务逻辑。

| 驱动 | 账本 | 仲裁器 | 它换来什么 |
|---|---|---|---|
| `storage/filestore` | `runs.jsonl` | — | 持久性：崩溃留下痕迹，对账器能把它收尾 |
| `storage/memarbit` | — | 内存 | 协议本身，单进程跑通 |
| 数据库驱动（**未实施**） | 一张表 | 一张表，epoch 由 `UPDATE … SET epoch = epoch + 1` 分配 | 多个进程共用一个会话存储 |

★ **内存仲裁器是协议的证明，不是生产件**（`storage/memarbit/arbiter.go:1-8` 的包
注释）：它在一个进程内裁决，什么都活不过重启。

★ **epoch 的分配与随后的追加不是原子的。** 两个并发分配可能发出同一个号 —— 但准入
闸门保证一个会话同时只有一个分配者（`storage/filestore/runledger.go:195-199`）。
★ 而且**分配出去却没写下的号会留一个洞**，这没关系：epoch 是排序装置，不是稠密计数器
（`storage/filestore/runledger.go:189-193`）。

★ **仲裁器没有定时器。** 租约过期由持有者自己的续租失败来发现，仲裁器只回答「现在的
租约是什么、过期了吗」（`storage/memarbit/arbiter.go:129-135`）—— 一个定时器正是
这套设计拒绝的东西。

### 1.6 事件管线：从单消费者到发布订阅

★ **推导没法做两遍。** 流式分支要在模型的流上调用 `Recv()`，再把分片折成一条消息。
**第二个读者会把分片劈开。** 于是渲染器必然是唯一消费者 —— 而渲染器就是 SSE 的出口，
于是出口拥有了 run 的产出。连接一死，这一轮就永远归档不了。

拆法是两件事、两个包：**推导**在 `agentkit/emit/`（L1），eino 事件 → 归档产物 +
`contract.RunEvent`；**渲染**在 `transport/`（L2），`contract.RunEvent` → AG-UI 帧 →
SSE。`agentkit/emit` 只依赖 `contract`、`message`、`approval`、`helpers` 与 `obs`，
不依赖任何上层。入口是 `emit.Derive`（`agentkit/emit/emit.go:131`）：它读一遍 agent
的事件，同时产出**归档产物**（`Artifacts`，`agentkit/emit/emit.go:107`）与**发布出去
的事件**。

★ **run 的事件词汇表是 openheron 自己的，不是 AG-UI 的**（`contract/runevent.go:13`
的 `RunEventKind`、`:56` 的 `RunEvent`）。run 不该依赖线上格式；`transport` 把
`RunEvent` 映射到 AG-UI 帧。第二种线上格式会是第二个渲染器，不是第二个 run。

★ **没有 `RunEventMessage`，也没有 `RunEventDone`。** 归档的消息是推导的**产物** ——
它们随推导一起回来，而不是从事件流里过。「结束」不是一个事件：它是**订阅通道的关闭**。

发布方在 `agentkit/run/publish.go:34`：一把锁、一个有界的 `window`、一个订阅者集合、
一个 `closed` 标志，外加一个 `onProgress` 回调。

- ★ **发布方绝不在满通道上等。** 慢的订阅者会被踢掉（`agentkit/run/publish.go:58` 的
  `Publish`）。★ **被卡住的订阅者才是要紧的那一种，它和「慢」不是一回事**：慢的读者
  终究会把通道排空；卡住的那个是 socket 写永不返回 —— 对端消失了却没发 RST，内核
  还在往一个没人读的缓冲区里塞字节。发布方踢掉它而不是等它，run 继续走。拆分之前，
  同一种情况会卡住 run，因为写 socket 的就是 run 自己。
- ★ **窗口是有界的，不是这一轮的历史。** 窗口留最后 1000 个事件
  （`agentkit/run/publish.go:19`），最老的被丢掉，于是长的一轮花的是**固定**的内存。
  没人看的一轮为一份没人读的日志付**恒定**的代价，而 run 真正的产出（transcript）
  反正都要写。另一种做法（等有人挂上来再开始建窗口）意味着猜 —— 而且会漏掉最要紧
  的那种情况：挂到一半的客户端。
- ★ **窗口的回放与登记在同一把锁里**（`agentkit/run/publish.go:94` 的 `Subscribe`），
  所以一个订阅者每个事件恰好看一次：先是窗口里的那些，然后是随后来的那些。
- ★ **run 在这一轮结束**时才关掉订阅** —— 而一个以抛审批结束的这一轮，在审批落盘
  之前不算结束**（`agentkit/run/bridge.go:169`）。这不是细节：客户端是从通道关闭那一刻
  知道「有审批」的，所以一个在审批落盘之前就关掉的通道，等于邀请客户端去回答一个
  服务端还找不到的审批。

★ **两件事，两个存储。** 「以后再回来」是 **transcript** 的活：客户端打开一个会话时
从 `GET /v2/sessions/{id}/messages` 水合，看到的是**已经结束**的那一轮。窗口只服务
「它还在跑的时候挂上来」—— 一个实时视图，永远不是最终视图。

---

## 2. 会话准入

### 2.1 服务端是权威

规则：**一个会话至多有一轮活着的轮次。对忙着的会话发第二个 `POST /v2/run` 一律
拒绝（409），且不改变任何东西。**

守卫从每个标签页的内存搬进 `agentkit/run`。★ **客户端侧的守卫是礼貌，服务端侧的
守卫才是规则。** 任何不是 openheron Web UI 的客户端 —— `curl`、另一个 AG-UI 客户端、
未来的移动端 —— 都得到同样的保护。

闸门是**两个方法**，不是一个：

| 方法 | 守什么 | 用在哪 |
|---|---|---|
| `ensureIdle`（`agentkit/run/runtime.go:431`） | 有活轮次**和**有待审批都拒绝 | 新查询（`Submit`，`agentkit/run/runtime.go:224`） |
| `ensureNoLiveQuery`（`agentkit/run/runtime.go:505`） | 只在有**查询**轮次在跑时拒绝 | 恢复（`Resume`，`agentkit/run/runtime.go:550`） |

★ **闸门问的是在跑那一轮的种类**（`agentkit/run/runtime.go:485` 的 `conflictTurns`）：

| 在跑的轮次 | 新查询 | 新恢复 |
|---|---|---|
| 查询 | **拒绝** —— 跨客户端那一种 | 允许 —— 它回答的就是这一轮自己的审批 |
| 恢复 | **拒绝** —— 新的一轮 | 允许 —— 同一轮继续 |

理由：恢复续接的是它所回答的那个被打断的轮次 —— 那是同一轮对话，不是新的一轮
（`contract.TurnKind` 就是这么写的，`contract/contract.go:12`）。★ **查询才是不能
重叠的东西**，而那恰恰是「另一个标签页插进来」。

★ **竞态由「占槽」关掉，不是由「检查」关掉。** `ensureIdle` 是给人看的友好提示；
权威的是 `claimTurn`（`agentkit/run/runtime.go:265`）—— 它把「忙不忙」的判定与
「接管」放在**同一把锁**下。两个同时到达的请求不可能都通过它：输家拿到
`ErrSessionBusy`，而不是半启动的一轮。

★ **Resume 先申领槽位，再消费审批**（`agentkit/run/runtime.go:558-583`）。
`TakePendingApproval` 会清掉记录并落盘，所以在那之后的任何拒绝都会**吃掉用户的
决定** —— 卡片消失了，而那个审批再也答不上。先申领意味着拒绝时审批原样留着。

★ **恢复钉住父轮次的那棵树**（`agentkit/run/runtime.go:567-573`）：父 run id 与树的
指纹一起从账本里查出来（`agentkit/run/runtime.go:867` 的 `parentRunOf`），指纹写在
`turn.pin` 上。⇒ 被打断的那一轮继续用它开始时的那棵树；**新配置在下一轮生效**。

### 2.2 「忙」就是忙 —— 包括等着审批

一个正在等审批的会话**不是**空闲的。上下文属于那个待决的事：检查点正是恢复要接着
走的地方，待审批正是用户在回答的东西。发一条新查询就得把两者都丢掉。

闸门的两半**持久性不同**：

| 检查项 | 来源 | 扛得住重启吗？ |
|---|---|---|
| 存在待审批 | `sess.PendingApproval()` —— 读会话文件 | **扛得住** |
| 有活轮次在跑 | 运行时的内存 `sessionState`（`turn` + `loop` 这一对） | **扛不住**（但租约与账本补上了这一半） |

★ **拒绝必须是惰性的。** `Submit` 里那些破坏性的动作 —— 丢弃检查点、清掉待审批 ——
排在闸门**之后**（`agentkit/run/runtime.go:236-242`）。★ 只在确认会话归我们之后，
它们才是对的：一个被拒的请求不删检查点、不清审批、不落消息。

★ **「有活轮次」问的是 loop，不是轮次指针。** `st.turn` 在轮次归档完才清（滞后），
而 `st.loop` 在 loop 活着期间一直都在（更长寿）—— 两者**都在**，才读作「忙」
（`agentkit/run/runtime.go:440-451`）。

★ **中止会清掉那张作废的审批**（`agentkit/run/runtime.go:700`，`clearVoidApproval` 在
`:710`）。否则「先停、再接管」接管不了 —— 闸门在有待审批时拒绝新查询，而那张审批
属于一个已经不存在了的轮次。★ 只有**孤儿**审批（检查点已经没了）值得留着：它同样
答不了，但那不是这次调用的业务，卡片留在屏幕上。

### 2.3 状态投影是前端的事实来源

`stateOf`（`api/v2_sessions.go:505`）**只读一次阶段**：问 `StatusOf` 一次，按
`PhaseWaitingApproval` / `PhaseRunning` / 其它映射成 `waiting_approval` /
`running` / `idle`。

★ **一次读，一个答案。** 阶段本身已经算进了待审批（`StatusOf` 从会话自己的待批记录
推导 `PhaseWaitingApproval`，`agentkit/run/runtime.go:813-821`），分开读两遍可能看见
它们不一致 —— 两次读之间待审批被清了。

★ **闸门与 `stateOf` 刻意读的不是同一个东西。** `stateOf` 走 `StatusOf`，而它是从
**run 账本**推导 `PhaseRunning` 的 —— 也就是对这个会话的 run 做的那次扫描。闸门则
直接读 `turn` + `loop` 这一对。两者是在同一口气里被释放的：终态行落在归档那条路上
（`agentkit/run/loop.go:216`），闸门由 `markTurnDone`（`agentkit/run/bridge.go:67`）
晚一点点放开。于是，一轮答案落地后的片刻，**界面**可能已经说着「空闲」，而**闸门**
还没放行下一次查询。

这个不对称是对的：

| | 会滞后吗 | 错了的后果 |
|---|---|---|
| `stateOf`（呈现） | 可能早说一会儿「空闲」 | composer 提前可用了；下一条消息被 409 拒掉、气泡回滚 —— 可以恢复，真相晚一点到 |
| 闸门（准入） | **绝不能**拒掉一个空闲的会话 | 误拒会把用户锁在一个他明明能用的会话外面 |

★ 把两者改成读同一个东西，就等于给它们同一个滞后 —— 而闸门的滞后会变成误拒。

前端：`seedFromHistory` 认服务端的 `state`（`web/src/lib/store/history.ts:144`）：
`state === "running"` 就把 `status` 置成 `running` 且 `busy` 置真。

★ **水合出来的「running」一定是别人的轮次。** 刚打开会话的那个界面不可能拥有
一轮它没启动的轮次 —— 拥有轮次的界面自己拿着事件流，永远不会重新水合。所以水合时
**忙**标记也会置上，composer 会说清楚那是谁的轮次。待审批那一支保持优先：审批卡比
「在跑」更具体。

### 2.4 409 是一等公民

`ErrSessionBusy`（`agentkit/run/errors.go:43`）变成带自己错误码的 409：
**`session_busy`**（`api/v2_types.go:48`），映射在 `api/v2_types.go:120`，
`retryable: true` —— 正在跑的那轮结束后冲突自行解除，稍后重试是有意义的。

★ **`agent_busy` 改名成 `session_busy`**：旧名字说的是错的话。它的三处调用点
（切 agent、压缩、运行路径）**全都是会话级**的拒绝，而并行委派存在的前提下，多个
agent **可以**合法地同时忙 —— 客户端若把 `agent_busy` 读成「这个 agent 正在干活」，
就会得出恰恰相反的结论（`api/v2_types.go:40-48`）。

两种成因靠消息文本区分（「an approval is pending」与「a turn is already running on
this session」），客户端不必多一个码就能分辨。

前端必须处理被拒的发送，**且不破坏本地视图**：**错误码不能丢** —— `runStream` 把
`body.code` 一起交给 `onError`（`web/src/lib/api.ts:541`，签名在 `:451`），只剩散文
的话前端分不清「忙」与「失败」；**乐观气泡要回滚** —— 那条消息根本没到服务端，却留在
屏幕上像是发出去过（`web/src/lib/store/actions.ts:339-347`）；**会话被标记为忙，不是
失败**。

★ **停止并接管。** 忙着的会话给一个「停止」动作：用户可以先停掉另一轮再发。停止
按钮留着，它的意思是「停掉这个会话的轮次」，不论谁持有 —— 而这需要客户端身份才能
做得更细，那是被明确否掉的（见 §7）。

---

## 3. 上下文用量与压缩

### 3.1 两个刻度，不是一个

对 agent `A`：

```
used(A)        = A 最近一次模型调用的 token 数
window(A)      = A 所用模型的上下文窗口
summarizeAt(A) = A 的历史会被压缩时的 token 数
```

★ **两个刻度，不是一个：**

| 刻度 | 分母 | 回答的问题 |
|---|---|---|
| `used / window` | `llm/*.yaml` 里的 `context_window` | *这个会话占了模型窗口的多大比例？* |
| `used / summarizeAt` | 摘要阈值 | *离被压缩还差多少？* |

`summarizeAt` 为零表示这个 Agent 永不压缩 —— 它自己写了 `off`，或者它的模型没声明
窗口可量 —— 此时第二个刻度干脆不显示。

`used(A)` 取自 **A 最后一条带用量的 assistant 消息**的 `Usage.TotalTokens`
（退化时取 `PromptTokens + CompletionTokens`，`agentkit/emit/context.go:45-48`）。
它作为占用数字有三条理由：

1. **它是单次请求的大小，不是总和。** 下一次请求要带上上一次带过的全部东西，
   所以上一次请求的总量就是上下文占用，一阶近似。
2. **它是厂商自己的分词器数出来的**，不是猜的。
3. ★ **它与 eino 摘要中间件所数的基线是同一个** —— 屏幕上的数字与「该压缩了」的
   判定依据不会分叉。

★ **这个数字在哪儿不诚实：**

- **少算**了最后一次模型调用**之后**才产出的工具结果。它们还没进过任何请求；等
  下一次调用时它们会进入该次请求的 `prompt_tokens`，届时数字会跳一下。⇒ 界面上标
  `≈`，tooltip 写明口径。
- **不会多算**，而且它的**口径**与窗口一致：`prompt_tokens` 里含系统提示与工具
  schema，这些同样占窗口。
- **推理 token** 有的厂商算在 `completion_tokens` 里，有的单列。两种情况都照厂商的
  总数用。

★ **窗口未知的时候比想象中多**（`context_window: 0` 的意思是*没配置*）：显示
`≈12k tokens`，**不画条**，tooltip 说明窗口未配置。零窗口绝不能渲染成「0 %」。

★ **按 agent，不按会话。** 上下文是**逐 agent** 的量 —— 根 agent 的输入里不含子
agent 的消息（`agentkit/run/input.go:136` 的 `withoutSubagentMessages`），每个子
agent 自持一份消息列表。一个五 agent 的会话有五份大小迥异的上下文。

### 3.2 计算放在 transport，窗口来自 builder

★ **一处漂移要说清楚：这段计算曾经住在 `transport/`，管线拆分之后它搬到了推导层
`agentkit/emit/context.go:21`（`ContextUsageOf`）。** 理由与拆分本身同源：占用是
从归档产物里量出来的，而归档产物是推导的产物；把它留在渲染层，会让渲染层再次拥有
run 的产出。**`transport` 现在只渲染**（`transport/transport.go:1-13` 的包注释）。
窗口**仍然来自 builder** —— 这一半没有搬家。

契约在 `contract`（`contract/contract.go:78` 与 `:93`）：`ContextUsage` 带
`Agent`、`Provider`、`Model`、`UsedTokens`、`ContextWindow`、`SummarizeAt`、
`Summarized`、`UpdatedAt`；`AgentContext` 带 `Window`、`SummarizeAt`、`Provider`、
`Model`。

★ **结构体只带数字。** 比例由渲染方自己算 —— 把一条百分比规则抄成两门语言，正是
它们分叉的开始。

窗口**每次运行解析一次**，来源是装配出这些 agent 的那个 builder：
`Builder.Contexts()`（`agentkit/build/context.go:20`）返回一个
`map[string]contract.AgentContext`。

★ **API 层不许重推某个 agent 跑在哪个模型上。** builder 拥有模型引用的解析
（`build.Resolve`）与逐 agent 的模型覆盖，所以答案必须是那个 agent **实际**跑着的
模型，而不是配置里提到的那一个。没有自己 `model:` 的子 agent 跑在父 agent 的模型
上，所以它继承父的窗口（`agentkit/build/context.go:18-19`）—— ★ **但阈值是它自己的**
（`agentkit/build/context.go:41-44`）：一个与父共用模型的子 agent 仍然自己决定什么时候
压缩。

阈值属于 **Agent**，窗口属于 **Model**。两者在 `spec.SummarizeAt`
（`agentkit/spec/summarize.go:25`）这一处相遇：

| 写法 | 阈值 |
|---|---|
| `80%` | 该 Agent 实际所用模型窗口的 80 % |
| `104858` | 绝对 token 数 |
| `off` | 永不 |
| *(不写)* | 窗口的 80 % —— **默认值**（`agentkit/spec/summarize.go:13`） |

★ **百分比需要窗口。** `context_window: 0` 意味着*没配置*，而百分比的百分比是零：
那个 Agent 永不压缩，spec 包在加载配置时把它记进 `deferred`
（`agentkit/spec/load.go:912` 的 `noticeCompactionWindows`）。绝对的 token 数不需要窗口。

★ **模型档案里的 `summarize_at` 已废弃。** 它照常解析 —— 直接删掉会让所有老
`llm/*.yaml` 启动即失败，因为解码器是严格的 —— 但没有任何地方读它，spec 包会指路到
Agent 上的那个设置（`agentkit/spec/load.go:63-67`）。

★ **agent 视图认得窗口了。** `modelView.ContextWindow`（`api/v2_introspect.go:105`）
让前端知道「我正在对话的这个 agent 的窗口有多大」；厂商清单里也有同一个字段
（`api/v2_introspect.go:321` 的 `modelBrief`）。

### 3.3 实时通道与历史通道

占用在**每次模型调用之后**都会变，所以不能等到运行结束。

**实时通道**：一个 `CUSTOM` 事件，`name` 是 `context_usage`，`value` 是该 agent 的
占用快照（`agent`、`provider`、`model`、`used_tokens`、`context_window`、
`summarize_at`、`updated_at`）。

发射点：`transport/transport.go:191` 的 `emitContextUsage`。★ **载荷是该 agent 的完整
快照 —— 客户端是替换而不是合并，所以丢一个事件不会留下错数字。**

**历史通道**：会话视图上的 `usage.context`（`api/v2_sessions.go:279-284`）。
★ **历史用与实时路径同一个函数算**（`agentkit/emit/context.go:21`）：顺着记录走一遍，
取每个 agent 最后一条带用量的 assistant 消息。⇒ 刷新后的数字就是实时路径收尾时的
那个数字，两条通道不可能各说各话。

★ **两个量必须分开**：

| | `usage.byAgent` | `usage.context` |
|---|---|---|
| 含义 | 这个 agent 一共消耗了多少 | 这个 agent 的上下文现在有多满 |
| 累加自 | 整个会话 | 一次请求 |
| 什么时候会掉 | —— | 会话被压缩，或换新会话 |

★ **压缩次数**数在 `context[agent].summarized` 上（`api/v2_sessions.go:325` 的
`compactionCount`）—— 压缩的消息留在记录里（压缩不是删除），所以这个数是关于**记录**
的事实。

前端：reducer 处理 `CUSTOM`（`web/src/lib/events.ts:382-383`），`onContextUsage`
（`web/src/lib/events.ts:425`）替换该 agent 的那一项，其余不动；selector
`selectActiveContext`（`web/src/lib/store/selectors.ts:103`）返回 store 自己持有的
那个 map —— 引用稳定，`Object.is` 判定不变。

### 3.4 压缩：落盘的是什么、有效历史、摘要不能丢的东西

★ **那个坑：** eino 的摘要中间件改写的是 agent 的**内存状态**，而 openheron 每轮都
从持久化记录重建模型输入（`agentkit/run/loop.go:81` 的 `sess.Messages(true)`）。
改写出来的状态随轮次一起没了，下一轮照旧把完整记录再发一遍。⇒ **压缩结果必须持久化**，
之后的会话还得换个读法。

**中间件怎么挂上。** `agentkit/build/summarize.go:28` 的 `summarizationMiddleware`
在 agent 有阈值时才建它 —— 压缩是逐 agent 的，一段永远越不过阈值的对话不该背一个
用不上的中间件。★ 阈值是 `Trigger.ContextTokens`（`:44`），而且必须开
`EmitInternalEvents`（`:49`）：否则 `after_summarize` 事件不存在，就没有东西可持久化。

★ **finalizer 用的是 eino 的默认实现**（`agentkit/build/summarize.go:172`）：替换后的
历史**保留 system 消息** —— 上下文提示（规则、项目根、workspace）就是一条 system
消息，丢掉它会让 agent 在这一轮剩下的时间里没有那些规则。摘要是**唯一**值得进
记录的那一条，由推导层从替换历史里挑出来（`agentkit/emit/emit.go:400` 的
`compactionFrom`）。

**落盘的是什么。** 替换后的历史是**一条消息**。就这一个事实，让持久化变得简单：

- **压缩消息**像别的消息一样追加进 transcript（`agentkit/run/loop.go:362` 的
  `archiveCompaction`）：一行，一条 `schema.Message`。格式版本不动
  （`formatVersion`，`storage/filestore/session.go:661`），既有读法照旧。
- **边界**搭在这条消息上，放在 `Extra` map 里（`message/compaction.go:14` 的
  `ExtraCompaction`），与 openheron 已经打戳的地方相同。`Compaction` 结构带着
  `Superseded`、`SavedTokens`、`At` 三个数（`message/compaction.go:21`）。

⇒ 记录成了**唯一真相源**：有效历史由「最后一条带标记的消息」推导出来。不动
`state.json`，不多一个要同步的文件，崩了之后也没有要对账的东西。

**有效历史。**

```
有效历史 = 最后一条压缩消息，以及它之后的全部消息
```

实现是 `message.EffectiveView`（`message/compaction.go:103`），由
`Session.Messages(effective)` 这个读法暴露（`storage/filestore/session.go:344`，
接口在 `storage/storage.go:48`）。★ **Go 没有默认参数，所以默认靠零值表达**：`false`
就是「全部」。参数在每个调用点写出来，而不是藏在另一个方法后面。

生产代码里有两个调用点，要的视图不一样：

| 调用点 | 视图 | 为什么 |
|---|---|---|
| 运行循环（`agentkit/run/loop.go:81`） | `sess.Messages(true)` | agent 的上下文就是有效历史 |
| 消息视图（`api/v2_sessions.go:274`） | `sess.Messages(false)` | 界面显示整份记录 —— 被取代的消息与压缩标记都在里面 |

★ **顺序：压缩消息在本轮消息之前**（`agentkit/run/loop.go:326`，`archiveCompaction`
在归档循环之前跑）。这正是语义顺序：压缩总结的是它之前的一切，本轮的消息在它之后。
⇒ 追加之后、下一次读之前崩了，代价为零（压缩消息已在盘上）；**追加之前**崩了，
代价也为零（历史还是长的，下一轮再压一次）。★ 顺序只在一边要紧：如果**先**记边界、
**后**追加消息，中途崩了就留下一个指向自身末尾之外的记录 —— 有效历史会变空。

★ **只有根 agent 的上下文会被压缩。** 子 agent 的上下文活在自己的那一轮里，随轮而死；
子 agent 的消息带自己的标记（`message.IsSubagent`），在构造上下文之前就被丢掉了
（`agentkit/run/input.go:136`）。推导层也只认根 agent 的压缩
（`agentkit/emit/emit.go:268-283`）—— 子 agent 的摘要绝不能到 transcript 里，否则
会在子 agent 的摘要处切掉根的有效历史。

**摘要不能丢的东西。**

| 东西 | 住在哪 | 压缩后还在吗 |
|---|---|---|
| 用户自己的话 | 摘要正文 —— eino 把近期的用户消息拼回去 | 在，但受预算限制 |
| 系统提示 | 每轮重建 | 在 |
| 工具 schema | 每次调用都重发 | 在 |
| 审批审计日志 | `state.json`，不在消息日志里 | 在，没被碰过 |
| 原始消息 | 记录里，被取代但没被删 | 在 —— 还在盘上 |

最后一行正是记录不被重写的原因：★ **压缩不是删除**。旧消息在记录里、在界面里都还
读得到，标记说明切口在哪儿。

**界面显示什么。** 四样东西，彼此不同：

1. **消息列表里的一条分隔线。** 压缩消息不是聊天气泡。消息视图给它标上一个
   `compaction` 元数据项，前端把它画成分隔线（`web/src/components/compaction-divider.tsx`）；
   被取代的消息留在它上面、仍然可见。
2. **仪表盘的第二根针。** `context[agent].summarized` 数压缩的次数。上一次压缩省下
   多少，写在分隔线上而不在仪表盘上：仪表盘回答「上下文现在有多满」，分隔线回答
   「上一次切口省了多少」。
3. **一个实时事件。** 压缩发生在轮内，所以界面从事件流里知道它：
   `CUSTOM "context_compacted"`（`transport/transport.go:211`）。★ **实时事件只带
   事实、agent 与时间 —— 不带数字。** 数字是拿记录量出来的，而记录由运行循环在轮次
   结束时写入；transport 手里没有记录可量。
4. **一个手动命令。** `/compact` 随叫随压：`POST /v2/sessions/{id}/compact`
   （`api/v2_compact.go:35`）摘要有效历史（`agentkit/run/compact.go:70` 的
   `run.Compact`）、盖上同一个标记，并把数字报回来 —— 所以手动压出来的分隔线当场
   就带数字。★ 轮次在跑或审批在等时命令被拒（409，`api/v2_compact.go:46`）；上下文
   小到不值得压时它也拒绝（`agentkit/run/compact.go:34` 的 `MinCompactMessages` 与
   `:36` 的 `MinCompactTokens`）—— 摘要带着前言，短对话压完可能反而更长。

---

## 4. 日志装配

### 4.1 只有一处装配

★ **日志只有一处装配。** `agentkit/obs` 造 logger 并持有 sink：`New`
（`agentkit/obs/logger.go:19`）、`Discard`（`:30`）、`ParseLevel`（`:39`）、
`LevelFromEnv`（`:71`）。

没有全局 logger 可回取 —— `obs.Logger()` / `obs.SetLogger()` / `obs.DefaultLogger()`
都已删除，`grep 'obs.Logger()'` 的结果是 0。★ **业务包直接调 `log.Printf` 或
`fmt.Fprintln(os.Stderr)` 就会绕开这个开关。**

级别优先级：**`$OPENHERON_LOG_LEVEL` > `server/config.yaml` 的 `log_level` > `info`**
（`main.go:216` 的 `applyLogLevel`）。取值 `debug / info / warn(warning) / error`，
大小写与空白不敏感；空值即 `info`。★ **非法值不致命**：回退 `info` 并记一条
`deferred` 提示。★ **告警必须在切换等级之前发出**（`main.go:226-230`），否则
`error` 等级会把告警自己吞掉。

★ **门限可以后移，是因为 handler 持有的是 `slog.Leveler` 而不是定值。** 一个共享的
`*slog.LevelVar` 被 handler 每次取用，所以移动它会移动**整个输出**，包括用 `With` /
`WithGroup` 派生出来的 logger（`agentkit/obs/logger.go:13-18`）。

### 4.2 组合根与注入

logger 只在 `main` 里造一次再传下去：`newLogger()` → `installLogger()`
（`hlog.SetLogger` + `callbacks.AppendGlobalHandlers`）→ `setupConfig`
（`applyLogLevel` / `applyLogFile`）→ `setupRuntime` → `api.New(..., log)` →
`hserver.New`。

- `main.newLogger`（`main.go:192`）围绕共享的 `slog.LevelVar` 与 `obs.Sink` 建好
  logger，**在**配置读出来**之前** —— 等级与文件路径由配置决定，而 logger 必须先
  存在，`setupConfig` 才能打第一行日志。
- `main.installLogger`（`main.go:203`）完成两次框架交接：`hlog.SetLogger`
  （`main.go:209`）与 `callbacks.AppendGlobalHandlers`（`main.go:210`）。
  ★ **这是仅有的两处全局注册**，其余全是注入。

★ **顺序不能动。** hertz 与 eino 各持一个进程级注册表，所以这两次交接必须在任何
hlog / callbacks 使用之前完成，也必须在 `hserver.New` 之前 —— 否则 hertz 的启动横幅
不会按最终等级输出。

注入方式：

| 模块 | 注入方式 |
|---|---|
| `api` | `New(rt, sm, log, opts...)`；`v2Fail` / `v2FailErr` 是 `*Server` 方法 |
| `agentkit/run` | `run.Config{Logger: log}` |
| `agentkit/build` | `Builder.WithLogger(log)` |
| `storage/filestore` | `Options{Logger: log}` |
| `kb` | `NewStore(dir, kb.WithLogger(log))` |
| `agentkit/emit` | `emit.Options{Logger: log}` |
| `agentkit/spec` | `NewReloader(svc, cb, spec.WithLogger(log))` |
| `obs/hook` | `hook.New(log)` |

★ **兼容技巧：** 所有构造函数零值默认 `obs.Discard()`，所以近百处既有构造调用不用
改；只有断言日志的测试需要显式注入 buffer logger。★ **一条支持 DI 的旁证：**
`Builder.Warm` 与 `Reloader.Watch` 会起后台 goroutine 并打日志，在它们启动后替换全局
logger 就是竞态；组合根先装配正好消掉它（用 `task test-race` 验证）。

### 4.3 文件输出：sink，而不是第二个 logger

★ **为什么是 sink，而不是第二个 logger。** logger 在配置读出来之前就建好了，而
`installLogger` 已经把它交给了 hertz 与 eino —— 两个进程级注册表，运行期不能换。
配置文件路径只有读完之后才知道，所以**能换的只有 writer**。

`agentkit/obs/sink.go:25` 的 `Sink` 实现 `io.Writer`，`Set`（`:66`）原子改向；
`obs.Handler` 与两份注册表都不动。启动时 `main.newLogger` 就建好 sink
（`main.go:199`），logger 从第一天起写在 sink 后面。

接线在 `main.applyLogFile`（`main.go:254`）：

| 位置 | 做什么 |
|---|---|
| `main.newLogger` | 建 `obs.NewSink(os.Stderr)` |
| `main.setupConfig` | 在 reloader 回调里用 `sync.Once` 调一次 `applyLogFile`；重载不换路径 |
| `main.applyLogFile` | 解析路径 + 建目录 + 0600 探针；挂 `lumberjack.Logger`（`main.go:278`）；`console` 决定是否 `io.MultiWriter(os.Stderr, file)`（`main.go:290`） |
| `main.serve` | 退出时 `defer` 关闭 rotator，最后几行日志仍进得去文件 |

★ **探针的理由：** `lumberjack` 第一次写入才开文件，且以 0644 创建。不先探一次，
权限错误和 0600 都到不了启动期 —— 而启动期是唯一有人能读到那条告警的时候
（`main.go:271`）。

★ **落不进文件是警告，不是失败的启动。** 服务的活儿是服务，而它的日志正是失败原因
唯一的容身之处；为了去处把进程拉下去，等于拿掉唯一能解释原因的地方。进程继续走
stderr（`main.go:236-240`）。

★ **路径落在配置根**（`spec.Service.LogFile` 解析），`$OPENHERON_LOG_FILE` 覆盖它一次
（`main.go:260`）。`server/` 不热更新：改了要重启。

★ **sink 同时是失败兜底。** 目标写失败一次就切回 fallback（stderr），并且只报一次
（`agentkit/obs/sink.go:74`）—— 不重试满盘，也不让日志静默消失。

★ **`format` 这一轮不做。** JSON 需要第二个 handler，而现在的 `Handler` 是手写的文本
封装。留作后续迭代 —— **不预留空键**，配置里没有 `format`
（`agentkit/spec/decl/decl.go:315-318`）。

### 4.4 叶子包的纪律

★ **`llm/common` 里任何一处 import openheron 内部包都算回归。** 那个包是零 openheron
依赖的叶子，厂商协议层，零日志代码。

请求日志曾经住在那里，那不是协议逻辑，是**观测逻辑** —— 它住在 L0 叶子包里本身就是
架构错位。现在它在 `agentkit/obs/httplog`：

- ★ **`httplog` 连父包 `obs` 都不 import**，只依赖标准库 `log/slog` 与 `net/http`，
  与 `agentkit/obs/hertz` 并列成「obs = 日志构造 + 适配器」。
- 导出 `Wrap`（`agentkit/obs/httplog/logging.go:52`）与 `WithRequestBodyDump`。
- 幂等检查是 `ok && t.log == log`，否则一个先被 `nil` 包过的 client 会永远打不出日志。
- ★ **被拒绝的请求（4xx/5xx）不受两个开关约束**
  （`agentkit/obs/httplog/logging.go:161`）：恰恰是那一轮被拒的请求值得留证，等运维
  想起开关再复现，往往已经不复现了。body 由 `req.GetBody` 回放取得，**不额外缓冲
  成功的请求**；读完错误正文后原样放回 `resp.Body`，调用方仍能解析。成功轮次保持安静。
- ★ **`tools` 原始字节单独记一行**（`agentkit/obs/httplog/logging.go:263`）：脱敏那条
  会把 body 经 `map` 重新序列化，重复的 JSON 键被吞掉、顺序被重排，而端点报
  「non-unique elements」这类拒绝只能靠**发出去的原始字节**判断。
- ★ **`shapes` 只记每条消息的形状**：序号、角色、有没有正文、工具调用数量 ——
  一个问题、一段回答都不进日志。

★ **第三方库也会绕开这套出口。** `mcp-go` 的 stdio transport 默认用 `log.Default()`
直写 stderr。`agentkit/build/mcpmanager.go:748` 的 `mcpCommandLogger` 经
`WithCommandLogger` 把它接回注入的 `*slog.Logger`，于是 `log_level` / `log.file` /
统一前缀重新生效。

---

## 5. API 表面

`/v2` 路由全部在 `api/v2_files.go:40` 一处注册。

### 5.1 `POST /v2/run`

请求体带 `threadId`（**必填**）、`query` 或 `resume[]`，以及可选的 `runId` 与
`attachments[]`。

★ **run 的 id 在这里定下来**（`api/v2_run.go:129-137`）：客户端没带就铸一个，带了就
用它，并把它放进 obs 上下文与 SSE 帧。一个 id，一个 run。

三种情况，由客户端送来的 `runId` 与有没有问题决定：

| 会话有 | 请求带 | 结果 |
|---|---|---|
| 没有活着的 run | 一个 `runId`（或者没有——服务端自己铸） | **创建**这个 run；响应里流出它 |
| 有一个活着的 run，id **正是**它 | 同一个 `runId`，**而且没有问题** | **挂上**：订阅这个活着的 run，并回放它的窗口 |
| 有一个活着的 run，id **不同** | 任意 `runId` | **409 `session_busy`** |

★ **挂上请求不带问题**（`api/v2_run.go:224` 的 `isAttachRequest`）：带问题就意味着
「开始一轮新的」，在这里开始一轮新的等于把用户那句话悄悄跑第二遍。挂上走
`attachRun`（`api/v2_run.go:241`），它按 id 找到那个活着的 run
（`agentkit/run/runtime.go:910` 的 `LiveRun`），订阅它并渲染它的窗口。

★ **结束的 run 挂不上。** 服务端回 **404 `run_not_live`**
（`api/v2_run.go:244`，码在 `api/v2_types.go:58`），而不是开始一轮新的 —— 客户端对
这个拒绝的回答是「去加载历史」，它必须能把它和「会话可以重试」分得开。

响应在归档与释放之后才收（`api/v2_run.go:201` 与 `:209`）：`turn.WaitArchived()`
（消息在盘上）与 `turn.WaitReleased()`（槽位空了）。

★ **两个等待不是一回事。** 归档是落盘，放置是「会话空了」；中间那段窗口里，响应如果
结束，客户端就拿到一个它还使不上的会话，而下一条消息会收到一条关于它刚跑完的那轮
的「a turn is already running」。那个窗口很窄，但它正好是脚本客户端会发下一条消息
的时候。

### 5.2 扫描与状态

| 路由 | 回答什么 |
|---|---|
| `GET /v2/sessions/{id}/runs` | 这个会话的每一次 run，加上 `activeRunId` |
| `GET /v2/sessions/{id}/runs/{runId}` | 一行 run |
| `GET /v2/sessions/{id}` | 会话的 `state` 与 `runId`（`api/v2_sessions.go:90-93`） |
| `GET /v2/sessions/{id}/messages` | 整份记录 + `state` + `usage`（含 `usage.context`） |

★ 扫描的路由**只读**（`api/v2_files.go:57-58`）。

### 5.3 中止与压缩

| 路由 | 语义 |
|---|---|
| `POST /v2/sessions/{id}/abort` | 中止的是**run**，不是 loop；返回它中止的那个 run id（`api/v2_sessions.go:404-406`） |
| `POST /v2/sessions/{id}/compact` | 手动压缩；轮次在跑或审批在等时 409（`api/v2_compact.go:46`） |

★ **中止会顺带清掉作废的审批**（见 §2.2），否则「先停、再接管」接管不了。

### 5.4 错误信封

统一由 `api/v2_types.go` 的 `v2Fail` 产出：`{"error": "...", "code": "...", "retryable": false}`。

| 码 | 在哪儿产生 | 含义 |
|---|---|---|
| `session_busy` | `api/v2_types.go:48` | 会话忙 —— 有活轮次，或有待审批 |
| `run_not_live` | `api/v2_types.go:58` | 挂上的那一轮已经不在跑了 |
| `no_pending_interrupt` | `api/v2_types.go:36` | 这个审批已经被答过了 |
| `no_checkpoint` | `api/v2_types.go:37` | 这个审批的检查点没了 |
| `turn_superseded` | `api/v2_types.go:49` | 为兼容保留；抢占那条路已经没有了 |
| `turn_start_timeout` | `api/v2_types.go:50` | 这个 run 始终没能变得可流出 |
| `agent_unavailable` | `transport/transport.go:43` | ★ 这个 agent 装不起来 —— 配置坏了，不是这一轮坏了 |

★ `agent_unavailable` 存在是因为**启动超时不能代替原因**：装配 agent 失败时（模型没
声明、文件解析不了），那一轮从未变得可流出，于是超时的症状顶了上来 —— 用户看到
「turn start timeout」，看不到是哪个文件、哪个模型。所以装配失败自己成为一个错误码，
消息是装配器自己的原话，客户端的下一个动作也据此不同：改文件，而不是重试
（`api/v2_run.go:275-279`）。

---

## 6. 测试矩阵

### 6.1 Go —— 账本与仲裁

`storage/filestore/runledger_test.go`：

| 用例 | 断言 |
|---|---|
| `TestRunRowSurvivesARestart` | 一行 run 记录能活过存储的重启 |
| `TestTheLastRowPerRunIDWins` | 每个 run id 取最后一行 |
| `TestTheHighestEpochAmongOpenRunsOwnsTheSession` | 未结束的 run 里 epoch 最高的那个是持有者，哪怕 epoch 更低的那个写得更晚 |
| `TestATornLastLineIsDropped` | 被截断的最后一行被丢弃，run 退回它上一刻的状态 |
| `TestTheEpochNeverRepeats` | epoch 从不复用，连重启也不复用 |
| `TestReconcileClosesTheRunsACrashLeftOpen` | 崩溃留下的 `running` run 在启动时被收成 `interrupted` |
| `TestTheLedgerIsBounded` | 账本始终在自己的上界之内 |

`storage/memarbit/arbiter_test.go`：

| 用例 | 断言 |
|---|---|
| `TestASecondClaimOnALiveLeaseFails` | 对一把活着的租约二次申领失败，报 `ErrLeaseHeld` |
| `TestAnExpiredLeaseCanBeTakenOver` | 过期的租约可以被申领，新的 epoch 更高 |
| `TestARenewalByAFencedOutHolderFails` | 被 fence 掉的持有者续租失败 |
| `TestAHolderThatReportsProgressKeepsItsLease` | **报告了进展**的 run 保住租约 |
| `TestAHolderThatReportsNothingLosesItsLease` | **什么都没报告**的 run 丢掉租约 —— 这就是 run 级停滞 |
| `TestReleasingAForeignLeaseDoesNothing` | 一次过期的释放不会把新持有者的租约放掉 |
| `TestTheFenceReportsTheCurrentEpoch` | fence 报出持有者的 epoch |

### 6.2 Go —— 运行时

`agentkit/run/runrow_test.go`：

| 用例 | 断言 |
|---|---|
| `TestTheRunIsRecordedBeforeItIsHandedOut` | run 在被交出去之前就已经入账 |
| `TestTheTerminalRowLandsAfterTheArchive` | 终态行落在归档之后 |
| `TestATurnThatNeverStartsClosesItsRow` | 没能启动的 turn 自己收掉自己那一行 |
| `TestTheScanNamesTheActiveRun` | 扫描报出活动 run，run 结束后就不再报它 |
| `TestTheResumeNamesItsParentRun` | resume 点名它答复的那一个 run |
| `TestAFailedRunIsNotRecordedAsCompleted` | 失败的 run 不会记成 `completed` |
| `TestAStoppedRunIsRecordedAsAborted` | 用户停掉的 run 记成 `aborted` |
| `TestStatusOfReportsTheActiveRunID` | `StatusOf` 报出活动 run 的 id |

`agentkit/run/publish_test.go`：

| 用例 | 断言 |
|---|---|
| `TestARunWithNoSubscriberStillArchives` | **没有任何订阅者**时，run 依然归档 —— 这是「一轮比连接活得久」的直接检验 |
| `TestASubscriberThatVanishesDoesNotStopTheRun` | 订阅者消失不影响 run |
| `TestASubscriberThatStallsIsDroppedAndTheRunKeepsGoing` | 通道溢出的订阅者被**踢掉**，run 继续归档 |
| `TestTheEventWindowIsBounded` | 没人挂上来的 run，它的事件日志不超出窗口大小 |
| `TestALateSubscriberGetsTheWindow` | 晚到的订阅者先拿到窗口的回放，再拿到实时事件 |
| `TestALateSubscriberAfterTheRunClosedStillGetsTheWindow` | run 关掉之后才来的订阅者仍然拿到窗口 |

`agentkit/run/gate_test.go`：

| 用例 | 断言 |
|---|---|
| `TestSubmitRefusalIsInert` | 拒绝之后：待审批还在、记录未变、跑着的那一轮没有被释放 |
| `TestSubmitRefusesWhileAnApprovalIsPending` | 待审批挡住新查询 |
| `TestResumeIsAllowedWhileAnApprovalIsPending` | 恢复仍然可用 —— 闸门不挡回答 |
| `TestRefusedResumeKeepsTheApproval` | 被拒的恢复让待审批原样保留 |
| `TestSubmitIsNotRefusedWhileTheTurnIsArchiving` | 收尾窗口：答案已落地的轮次**不**被拒 |
| `TestAStartTimeoutReleasesTheSession` | 起不来的轮次不会把会话永久锁死 |
| `TestAbortClearsTheVoidApproval` | 中止一个正在等审批的轮次会清掉那张审批 |
| `TestTheSessionIsReleasedBeforeTheResponseCanEnd` | 会话在响应能结束之前就被释放 |
| `TestAFailedTurnReleasesItsWaiter` | 失败的轮次会释放等着它的人 |

`agentkit/run/lease_test.go`：

| 用例 | 断言 |
|---|---|
| `TestALiveOwnerHoldsTheSession` | 活着的持有者占着会话：第二个 run 被拒 |
| `TestAStalledOwnerDoesNotHoldTheSessionForEver` | **停滞的持有者不会永远占着会话** —— 租约是预算，闸门问的就是它 |
| `TestAProgressingRunKeepsItsLease` | 报告了进展的 run 保住租约 |

### 6.3 Go —— HTTP 与存储

| 文件 | 覆盖什么 |
|---|---|
| `api/v2_runs_test.go` | 扫描列出所有 run 并报出活动的那一个；会话自己的路由带着活动 run 的 id；按 id 取一个 run，不认识的 id 报 404；空闲会话扫出来是空列表；挂上一个活着的 run；挂上一个已经结束的 run 被拒 |
| `api/v2_busy_test.go` | 409 + `code == "session_busy"`，且该冲突可重试；会话仍然报 `"state":"running"` |
| `api/v2_context_test.go` | 同一份记录上，会话视图的 `usage.context` 与实时事件一致；模型没声明窗口时窗口为零；没有用量时没有占用 |
| `api/v2_compact_test.go` | 手动压缩写下边界；太小的历史被拒；轮次在跑是 409 |
| `storage/filestore/compaction_test.go` | 有效历史是最后一条压缩消息及其后的消息；没有压缩标记的会话返回完整记录；被取代的消息仍在盘上 |
| `transport/context_test.go` | 占用是每个 agent **最后一次**调用的数字，不是总和；没说过话的 agent 不出现 |
| `transport/compaction_test.go` | 压缩以 `CUSTOM "context_compacted"` 事件在流上宣告；子 agent 的压缩不是会话的 |

### 6.4 前端

| 文件 | 覆盖什么 |
|---|---|
| `web/test/store-busy.test.ts` | 乐观气泡被回滚；会话被标记为忙而不是失败；普通失败仍然是失败；扫描报出一个不属于本界面的活动 run 时算忙；没有活动 run 时算空闲；本界面自己的 run 不算忙；**扫描失败时标记保持不动** |
| `web/test/session-busy.test.ts` | 服务端的 `running` 水合成 `running`；水合出的 `running` 标记为忙；空闲的会话不忙；待审批优先于 running |
| `web/test/context-usage.test.ts` | `context_usage` 事件记下该 agent 的占用；一个 agent 的事件不动别人的占用；没有窗口时占用照报、窗口是零；历史水合把占用带进来；`selectActiveContext` 返回同一个引用，没有会话时是 `undefined` 而不是空数组 |
| `web/test/compaction.test.ts` | 历史水合读出压缩标记；标记在就算边界，数字可以缺；实时事件插进一条边界；同一个压缩只插一次边界；实时边界没有数字 |
| `web/test/compact-command.test.ts` | `/compact` 空闲时可见、运行中隐藏、等审批时隐藏；手动压缩带着数字来，实时压缩没有数字 |
| `web/test/events.test.ts` | `RUN_STARTED` 记住 run id；没有 run id 的帧不会抹掉已有的那个 |

### 6.5 E2E（假后端）

| spec | 断言 |
|---|---|
| `web/test/e2e/reattach.spec.ts` | 连接断了以后重新挂上，这一轮不丢；重新挂上时只带 run id，不带问题 |
| `web/test/e2e/session-busy.spec.ts` | 第二个界面看到会话正忙，发送按钮被禁用；竞态下的第二个请求被拒绝，消息不留痕；忙着的会话可以停止另一轮来接管 |
| `web/test/e2e/compaction.spec.ts` | 压缩消息渲染成分隔线而不是气泡；被取代的消息仍然读得到；轮内发生的压缩当场画出边界；`/compact` 手动压缩后画出边界 |

### 6.6 命令

| 命令 | 覆盖 |
|---|---|
| `task test` | 完整 Go 套件 |
| `task test-race` | 同一套件在 race detector 下 |
| `cd web && bun run test:unit` | 纯函数层（reducer / framing / store / highlighting） |
| `task test-e2e` | 真浏览器 + 确定性假后端 |
| `task lint-go` / `task lint-web` | 两半 lint |

★ **E2E 用假后端，绝不用真后端。** 真后端依赖模型配合，失败时你分不清是界面坏了
还是模型没听话；协议层的验证是 `task smoke` 的活，两者互补。

---

## 7. 待决

以下都**未实施**，标出来是为了不要被当成现状读。

1. ★ **run 的事件日志要不要持久化？窗口又该多大？**
   每会话第三个文件（`events.jsonl`）能让一个几小时后才重连的客户端回放这一轮的
   增量，而不只是它的最终 transcript。transcript 已经给出一个正确的最终视图，所以
   这是锦上添花；代价是第三个文件、第三套裁剪策略、第三样会坏掉的东西。
   ★ 这个问题的后半截比前半截要紧，而且**跟持久化无关**：**窗口有多大**。
   ★ **窗口已经是上界**（1000，`agentkit/run/publish.go:19`）—— 一个随轮次一起长的
   日志是个只在高负载下才显形的内存泄漏。持久化那一半仍待决。

2. ★ **第二个进程在范围内吗？**
   租约和 fence 是为它设计的，但内存仲裁器跨进程什么也强制不了 —— 它是一套协议，
   不是一把锁。问题是：DB 驱动是真实需求，还是仅仅做准备。数据库驱动这一行
   （§1.5）是**未实施**的。

3. ★ **谁可以回答审批？要不要告诉第二个界面「是谁答的」？**
   待审批是会话级的，所以两个界面都可能尝试。`TakePendingApproval` 是原子的，于是
   第二个会拿到 `ErrNoPendingInterrupt` —— 这是对的，但 UX 需要说明「别人已经答过
   了」。

4. ★ **run 的那一行要不要带上用量合计？**
   这里刻意不放（§1.1）：数字归 transcript 管。但一份「这一轮花了多少」的 run 列表，
   就得对 message log 做一次范围扫描。往行里缓存一个合计，就是给一个目前是推导出来
   的数字安了第二个家。

5. ★ **`turn_superseded` 什么时候退休？**
   为兼容保留（§5.4）。它所命名的抢占路径已经不存在了 —— 常规路径不再产出它；
   经过一个弃用窗口就可以退休。

6. ★ **AG-UI 标准的 `usage[]`（`events.WithUsage`）。**
   AG-UI 的 `RunFinishedEvent` 带一个标准 `Usage []TokenUsage` 字段，本仓库零调用。
   值得做 —— 它是标准通道，而且是**逐 (provider, model)** 的，与逐 agent 的占用不是
   一个轴。两者并存：`usage[]` 管厂商记账，`context_usage` 管占用。
   ★ 提醒实现者：前端现在读的是 `outcome.result.usage`，而 AG-UI 的用量在事件的
   **顶层** `usage` 数组里。

7. ★ **会话列表里的 `state`：侧栏要不要给每个在跑的会话画一个「运行中」的点？**
   `GET /v2/sessions` 每行已经返回 `state`，但侧栏只画待审批那个点。建议要，但作为
   单独一次改动 —— 它需要一个刷新触发条件，而侧栏不轮询。

8. ★ **日志的 JSON 格式。**
   `log.format` 需要第二个 handler，而现在的 `Handler` 是手写的文本封装。留作后续
   迭代 —— **不预留空键**，配置里没有这个键（§4.3）。

---

## 附录 A：本文引用的代码位置

★ 每一行都打开读过。行号会随代码漂移；漂了以后以文件与符号为准。

### A.1 run 的生命周期

- `storage/runledger.go` — `ErrFenceStale`(:21)、七个状态(:32)、状态用名字上线(:55)、
  `waiting_approval` 是终态(:83)、不认识的名字是错误(:101)、`Run`(:134)、
  `RunLedger`(:187)、`FoldRuns`(:214)、`ActiveRun`(:238)、`SortRunsByStart`(:262)
- `storage/lease.go` — `ErrLeaseHeld`(:14)、`Lease`(:23)、`LeaseArbiter`(:58)、
  仲裁器不分配 epoch(:45)
- `storage/memarbit/arbiter.go` — 包注释(:1)、`Arbiter`(:24)、`Held` 连过期租约一起
  返回(:136)
- `storage/filestore/runledger.go` — `runs.jsonl`(:29)、上界 200(:36)、`Append`(:83)、
  `Sync`(:109)、裁剪(:126)、`List` 丢坏行(:173)、`NextEpoch`(:200)、epoch 分配的洞
  (:189)、分配不原子(:195)、`Reconcile`(:232)、不替别人背锅(:229)
- `storage/filestore/manager.go` — `Inspect`(:219)、账本对账(:267)
- `storage/filestore/session.go` — `Messages(effective)`(:344)、`formatVersion`(:661)
- `storage/storage.go` — `Messages(effective bool)`(:48)、`RunLedger()`(:149)

### A.2 运行时

- `agentkit/run/runtime.go` — `defaultInstanceID`(:160)、`newRunID`(:187)、
  `Submit`(:215)、闸门之后的破坏性动作(:236)、`claimTurn`(:265)、租约过期可接管
  (:280)、`openRunRow`(:298)、记不下来就拒绝(:300)、申领租约(:312)、进展续租与丢租
  即取消(:318)、释放被换下的轮次(:402)、run 上下文解绑(:409)、`ensureIdle`(:431)、
  `ensureNoLiveTurn`(:452)、问 loop 不问指针(:440)、`conflictTurns`(:485)、
  `ensureNoLiveQuery`(:505)、`Resume` 预检(:550)、父 run 与父树(:567)、先申领再消费
  审批(:557)、`TakePendingApproval`(:575)、`Abort`(:658)、清作废审批(:700)、
  `clearVoidApproval`(:710)、`StatusOf`(:804)、待审批推导阶段(:813)、`activeRun`(:842)、
  `parentRunOf`(:867)、`LiveRun`(:910)、`ScanRuns`(:932)
- `agentkit/run/loop.go` — 读有效历史(:81)、装配失败要判失败(:139)、记下树(:147)、
  行变 running(:168)、`emit.Derive`(:177)、归档→标记→终态行(:212)、`finishRun`(:277)、
  压缩消息在前(:326)、`archiveCompaction`(:362)
- `agentkit/run/bridge.go` — `sessionState`(:39)、`markTurnDone`(:67)、`loopExited`(:134)、
  抛审批的 run 在这里关发布方(:169)、`closeApprovalRow`(:180)、`closeOpenRow`(:203)、
  `writeHeldApproval`(:227)
- `agentkit/run/publish.go` — 窗口上界 1000(:19)、`publisher`(:34)、`Publish` 永不阻塞
  (:58)、`Subscribe` 回放窗口(:94)
- `agentkit/run/turn.go` — `Wait` 超时判失败(:158)、`WaitReleased`(:246)、`WaitArchived`
  (:259)、归档等待上界 30 秒(:272)、`failedState`(:330)
- `agentkit/run/lease.go` — 默认 TTL 10 分钟(:20)、`leaseExpired`(:28)、进展驱动的续租
  与节流(:105)
- `agentkit/run/errors.go` — `ErrSessionBusy`(:43)
- `agentkit/run/compact.go` — `MinCompactMessages`(:34)、`MinCompactTokens`(:36)、
  `run.Compact`(:70)
- `agentkit/run/input.go` — `withoutSubagentMessages`(:136)

### A.3 事件管线

- `agentkit/emit/emit.go` — 包注释(:1)、错误码(:45)、`Sink` 不得阻塞(:70)、`Derive`(:131)、
  只有根 agent 的压缩(:268)、`compactionFrom`(:400)
- `agentkit/emit/context.go` — `ContextUsageOf`(:21)、占用取 `TotalTokens`(:45)
- `agentkit/emit/usage.go` — `aggregateUsage`(:71)
- `contract/runevent.go` — `RunEventKind`(:13)、`RunEvent`(:56)
- `contract/contract.go` — `TurnKind`(:12)、`ContextUsage`(:78)、`AgentContext`(:93)
- `transport/transport.go` — 包注释(:1)、`CodeAgentUnavailable`(:43)、`Render`(:56)、
  `CUSTOM "context_usage"`(:191)、`CUSTOM "context_compacted"`(:211)

### A.4 会话准入

- `api/v2_run.go` — run id 定下来(:129)、挂上分支(:161)、连接订阅(:193)、等归档(:201)、
  等释放(:209)、`isAttachRequest`(:224)、`attachRun`(:241)、挂不上 404(:244)、
  `agent_unavailable` 映射(:275)
- `api/v2_sessions.go` — 会话的 `runId`(:90)、`ensureAgentIdle`(:220)、消息视图读整份
  记录(:274)、`usage.context`(:279)、`sessionContextUsage`(:309)、`compactionCount`(:325)、
  `v2Abort`(:385)、中止返回 run id(:404)、`v2SessionRuns`(:422)、只读(:418)、
  `v2SessionRun`(:443)、`stateOf`(:505)
- `api/v2_types.go` — `session_busy` 与改名理由(:40)、`run_not_live`(:58)、
  `ErrSessionBusy` → 409(:120)、`no_checkpoint` 的消息(:131)
- `api/v2_files.go` — 路由注册(:40)、两条扫描路由(:57)
- `api/v2_compact.go` — `v2Compact`(:35)、轮次在跑 → 409(:46)
- `api/v2_introspect.go` — `modelView.ContextWindow`(:105)、`modelBrief.ContextWindow`(:321)

### A.5 上下文用量与压缩

- `agentkit/build/context.go` — `Builder.Contexts`(:20)、子 agent 继承窗口(:18)、
  阈值是自己的(:41)
- `agentkit/build/summarize.go` — 摘要中间件(:28)、`Trigger.ContextTokens`(:44)、
  `EmitInternalEvents`(:49)、finalizer(:172)
- `agentkit/spec/summarize.go` — 默认阈值 80 %(:13)、`SummarizeAt`(:25)
- `agentkit/spec/load.go` — 模型档案里的 `summarize_at` 已废弃(:63)、
  `noticeCompactionWindows`(:912)
- `agentkit/spec/decl/decl.go` — `context_window`(:45)、`server.lease_ttl`(:297)、
  `server.log_level`(:277)、`LogCfg` 没有 `format` 键(:315)
- `message/compaction.go` — `ExtraCompaction`(:14)、`Compaction`(:21)、`EffectiveView`(:103)
- `web/src/lib/events.ts` — reducer 处理两个 `CUSTOM`(:382)、`onContextUsage`(:425)、
  `onContextCompacted`(:447)
- `web/src/lib/store/history.ts` — 水合时认服务端的 `state`(:144)
- `web/src/lib/store/selectors.ts` — `selectActiveContext`(:103)
- `web/src/lib/store/actions.ts` — 409 回滚与标记(:339)、`reattach`(:387)、流结束后
  重新扫描(:369)、`scanBusy`(:448)、扫描失败保持原样(:452)、`stop` 后重新扫描(:574)
- `web/src/lib/api.ts` — `listRuns`(:342)、`onError` 带 code(:451)、把 `body.code` 传下去
  (:541)
- `web/src/lib/types.ts` — `busy` 的含义(:151)

### A.6 日志装配

- `agentkit/obs/logger.go` — handler 持 `Leveler`(:13)、`New`(:19)、`Discard`(:30)、
  `ParseLevel`(:39)、`LevelFromEnv`(:71)
- `agentkit/obs/sink.go` — `Sink`(:25)、`NewSink`(:34)、`Set`(:66)、失败降级(:74)
- `agentkit/obs/httplog/logging.go` — `Wrap`(:52)、被拒绝的请求(:161)、`tools` 原始字节
  (:263)
- `agentkit/obs/hertz/hertz.go` — hertz 适配器(:32)
- `agentkit/build/mcpmanager.go` — `mcpCommandLogger`(:748)
- `main.go` — `newLogger`(:192)、`installLogger`(:203)、`hlog.SetLogger`(:209)、
  `callbacks.AppendGlobalHandlers`(:210)、`applyLogLevel`(:216)、告警在切换等级之前
  (:226)、`applyLogFile`(:254)、0600 探针(:271)、`lumberjack`(:278)、`io.MultiWriter`
  (:290)、落不进文件是警告(:236)
