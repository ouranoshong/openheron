# 坑点登记册

> 一句话：每一个坑都写清楚**症状**、**根因**、**修法**和**现状** —— 没有哪个坑是「记一下就好」。

> ★ 这份文档是**代码旁边**的东西，不是历史。它只收两种条目：**已经修掉的**（记下来是为了
> 不踩第二次）与**还没修的**（记下来是为了有人能接手）。★ 设计文档讲「现在是什么样」，
> 这里讲「为什么是这样，以及哪里还不对」。
>
> ★ **上游的坑和自己的坑分开写**：eino 与 eino-ext 的坑我们改不了，只能绕 —— 而绕法是我们
> 自己的代码，所以它记在「修法」那一栏里，不记在上游那节里。

## 0. 怎么读

| 栏 | 说的是什么 |
|---|---|
| **症状** | 用户或开发者看到的东西 —— 一句话，不含解释 |
| **根因** | 为什么会这样 |
| **修法** | 我们做了什么，或者打算做什么 |
| **现状** | `已修` / `待修` / `已知边界`（有意为之，不是 bug） |

★ **「待修」必须有出处**：要么是一个 issue，要么是一条 `TODO` 注释，要么是本文档里
另一条坑。★ 一个没有出处的「待修」是许愿，不是登记。

★ **一条坑写三遍是浪费**：细节留在本节，设计文档只留结论并指回这里。

## 1. 上游框架的坑（eino v0.9.22 / eino-ext / hertz v0.10.6）

### 1.1 ★ loop 收尾时读 agent 的状态，agent 还在写

**症状**：`go test -race` 在 `agentkit/run` 报一处数据竞争；生产里偶发地「一轮刚结束就
取消」。

**根因**：eino 的 `TurnLoop` 里，proxy 协程写 `l.capturedCancelErr`
（`adk/turn_loop.go:1889`），loop 收尾时读它（`:1723` 与 `:2006`），**中间没有同步**。
★ 触发条件不是「根 agent 重发中断」，而是**loop 被拆掉时 agent 还在跑** ——
每一次审批都会走这条路。

**修法**：轮次结束前**排空 agent 的事件流**，再让这一轮返回
（`agentkit/emit/emit.go` 的 `Options.Drained`，`agentkit/run/loop.go` 的 `settleAgent`，
上界 1 秒）。★ 写没了，竞态就没了 —— 不需要碰上游。★ **排空不能取消任何东西**：
这一轮的 context 是框架写检查点用的，取消它会把检查点一起带走。

**现状**：`已修`（审批路径上的那条）。★ **`rt.Abort` / `rt.Shutdown` 那条还没修** ——
框架自己停 loop 并取消 agent，写与读是真并发。彻底修需要给 agent 一个独立的 context，
先 quiesce 再停 loop。

### 1.2 ★ 框架有两条包装工具调用的路，我们只实现了一条

**症状**：档位表留在默认值 `ask` 上的工具**没经过人的决策就跑了**。实测：一次调用
`execute` 的 run 直接执行并返回输出，没有触发审批。

**根因**：框架包装工具调用有两条路 —— 同步的 `WrapInvokableToolCall` 与流式的
`WrapStreamableToolCall`。★ shell 的 `execute` 是 filesystem 那批工具里**唯一的流式**工具
（eino 用 `newStreamingExecuteTool` 建它，另外六个走 `InferTool`），而我们的审批闸门
只实现了同步那条。★ 其余那几个 filesystem 工具一直是被覆盖的 —— 这也是验收跑批里
`edit_file` 的审批一直正常的原因，所以这个洞一直没被发现。

**修法**：给闸门补上流式那条包装，两条路共用同一个决策
（`agentkit/build/gate.go` 的 `approvalMiddleware`）。★ 要跑的参数取自**中断时存下的**
那一份，而拒绝是**模型读得到的一条消息**，不是终结这一轮的错误。

**现状**：`已修`。

### 1.3 ★ 审批写在检查点之前 → 孤儿审批

**症状**：`TestApprovalChainEndToEnd` 失败 —— 审批出现了，检查点文件不存在。

**根因**：框架在 loop **停止**时写检查点，而 loop 停止发生在那一轮的回调**返回之后**；
审批却写在那个回调**里面**。所以一直存在一个窗口：用户看到审批卡，回答它却得到
`no_checkpoint`。

**修法**：审批在 loop 退出时写，不在回调返回时写。回调只是**拿着**它。

**现状**：`已修`。★ 这个窗口整个消掉了 —— 不是为了让那个测试变绿。

### 1.4 ★ `deep.Config` 只暴露了 middleware 十二个字段里的三个

**症状**：想在装配层约束文件系统的行为，却找不到挂手的地方。

**根因**：eino 的 `deep.Config` 只透传 middleware 的一小部分字段，其余的只能改上游。

**修法**：**分叉后端，不分叉 middleware** —— 我们的后端（`agentkit/fsx`）自己实现
eino 的 `fsapi.Backend` 接口，装配时把它交给框架（`agentkit/build/snapshot.go` 的
`buildHeavy`）。★ 写边界、`ls`/`glob` 的封顶、垃圾目录的忽略，全在后端这一层。

**现状**：`已修`（见 §4）。

### 1.5 ★ eino 带了摘要中间件，我们没用

**症状**：上下文到了窗口上限，框架不会自动压缩。

**根因**：eino 的 `deep` 带了摘要中间件，而 openheron 的压缩策略是**按 agent** 的
（`summarize_at` 是 agent 的阈值，窗口是模型的），与框架那个全局策略不是一回事。

**修法**：自己实现压缩，落盘并保留「有效历史」（见 `docs/runtime-design.md`）。

**现状**：`已修`。

### 1.6 ★ hertz 的 `Request.Host()` 答的是 URI 的 host，不是 Host 头

**症状**：加了一条「Host 必须是环回」的防线之后，**每一个**请求都被拒了 ——
包括从 `http://127.0.0.1:8080` 打开的界面自己发出来的那些。日志里是
`the request does not name this host`。

**根因**：`c.Request.Host()` 返回的是 **URI 的 host**（`req.URI().Host()`），而**请求行**里
带的是路径（`GET /v2/health HTTP/1.1`）—— URI 没有 host，所以它永远是空。Host **头**在
另一个地方：`c.Request.Header.Host()`。

**修法**：读 `Header.Host()`（`api/guard.go` 的 `hostOf`）。★ 单元测试里用
`ut.Header{Key: "Host", …}` 设置它 —— `ut` 把它当作**普通头**写进去，所以这条路径在
测试里也走得通。

**现状**：`已修` —— `api/guard.go:186`，用例在 `api/guard_test.go`。

### 1.7 ★ hertz 的中间件「返回」不等于「拦下」

**症状**：防线拒掉了一个请求，响应体里确实有那句错误 JSON —— **但状态码是 200**，
而且后面还多出处理器自己写的正文。看起来像「拒绝了，但没拦住」。

**根因**：hertz 的 `RequestContext.Next` 是一个**循环**（`ctx.index++` 之后挨个调用剩下的
处理器），不是「调用链的下一环」。★ 所以中间件 `return` 只是**回到那个循环里**，循环接着
跑到了真正的处理器：它写了正文，还把状态码改回了 200。

**修法**：拒绝时必须 `c.Abort()`（把 `ctx.index` 设成 `AbortIndex`），循环到此为止。

**现状**：`已修` —— `api/server.go` 的 `guardMiddleware`。

## 2. 身份与重建

### 2.1 ★ 「代际」这个全局计数器

**症状**：改一个 `skills/<name>/SKILL.md`，整棵 agent 树在下一轮**全部重建**。

**根因**：树的缓存键是一个**全局单调计数**。★ 计数说「一切都变了」，而配置文件说的是
「这一份变了」。⇒ 每个改动都让所有 agent 陪葬。

**修法**：分两步。★ **第一步（P2）**：池的键从 `(名字, 代际)` 换成 `(名字, 指纹)` ——
指纹是这个 agent **自己**依赖的那些东西的内容哈希。★ **第二步（P4）**：代际这个概念
整体退休，树的身份改成 **（配置对象，MCP 工具面）**，builder **比较**而不是被**通知**。

**现状**：`已修`。★ 代际退休之后，「树不会后退」这条靠的是配置在 builder 的锁**里面**
读 —— 晚到的调用者**不带配置**，所以它带不来一棵旧的树。

### 2.2 ★ 中途试过按配置指针缓存 —— 工具面变了但 agent 不重建

**症状**：`TestTheToolsJoinTheNextTurn` 失败 —— 服务器起来了，它的工具**进不来**。

**根因**：那一步只把配置对象当身份，**没有**把 MCP 工具面算进去。⇒ 服务器起来了，
工具清单变了，可是树的身份没变 ⇒ 树不重建 ⇒ 工具集还是旧的。

**修法**：树的身份是 **（配置对象，MCP 工具面）**，工具面按**当前配置挂载的 server**
取（`agentkit/build/mcpmanager.go` 的 `surfaceHash`）。★ 被卸载的 server 不再拖住树。

**现状**：`已修`。

### 2.3 ★ 清单的比较看不见「从没有到有」

**症状**：一台 server 起来了，它的工具永远进不来。

**根因**：`Refresh` 先读 `prev := e.lastTools()`，再读 `names`；而后台作业落地时
**两份都是新的** —— 相等，于是「变了没有」这个答案永远是「没变」。

**修法**：作业自己比 —— 它拿落地前的清单与落地后的清单比。

**现状**：`已修`。

### 2.4 ★ 工具面要在作业完成时就换，而不是等 `Refresh`

**症状**：工具要到再下一轮才进得来。

**根因**：若只在 `Refresh` 里换工具面，那么「作业在 `Refresh` 之后、装配之前落地」
这一段里工具面不动 —— 而那一批工具是**每一棵树构建一次**的。

**修法**：作业落地时直接把清单写进入口条目，工具面下一次比较就看得见。
★ 这比「推进代际」那条路更窄：作业只改自己的状态，**不通知任何人**。

**现状**：`已修`。

### 2.5 ★ 重试不能变成热循环

**症状**：一台永远起不来的 server 被反复拉起。

**根因**：一次失败之后，谁再问都要重试的话，没有退避。

**修法**：两次尝试之间至少隔一个 `timeout:` —— ★ 用文件自己的期限当退避，少一个旋钮，
而且这个期限本来就是「等多久」的那把尺子。

**现状**：`已修`。

## 3. 会话与轮次的时序缺陷

★ 这几个是实现 §「会话准入」时暴露出来的，每一个都改了设计。前四个有一个共同点，见 §3.5；
§3.6 与 §3.7 是同一个问题的另一面；§3.8 换了一个方向 —— **行与失败的先后**。

### 3.1 ★ `Complete` 在归档落地之前就返回了

**症状**：用户在答案刚到就发下一条，被拒（`409 session_busy`）—— 一次误拒，而且发生在
最普通的时序上。★ 更糟的是**下一轮的输入**是从记录里构建的，所以一个早于上一轮归档就
开跑的轮次，会读到不完整的历史。

**根因**：HTTP 响应在 `transport.Stream` 返回时发出，而 `turn.Complete` 只是**通知**
归档 —— 运行时异步取走结果、把消息写进记录。于是客户端被告知「你这轮结束了」，
而服务端其实还没把这一轮的消息写下去。

**修法**：`Complete` 只在归档落地之后才返回。轮次带一个 `archived` 通道，归档完成时
关掉它，`Complete` 等它。★ 这个等待有上限（30 秒）：归档里的 bug 不该把请求挂死。

**现状**：`已修`。

### 3.2 ★ 待审批写在了检查点之前

见 §1.3 —— 同一个坑的另一个视角：孤儿审批。

**现状**：`已修`。

### 3.3 ★ 一次恢复不该挡住另一次恢复

**症状**：第二个 `Resume` 被「第一次恢复轮次还在收尾」挡住，三个既有测试挂了
（`TestResumeConsumesTheApproval`、`TestMultiplePendingApprovalsAreAnsweredAsAGroup`、
`TestDecisionsAreAppendOnly`）。

**根因**：闸门只问「有没有轮次在跑」，不问「**是哪种**轮次」。★ 而**它们挂得对**：
第一次答复还在收尾时再答复一组审批，是很平常的用法。

**修法**：恢复续接的是它所回答的那个**被打断的轮次** —— 那是同一轮对话，不是新的一轮。
⇒ 闸门只放行「恢复撞恢复」，其余一律拒绝：

| 在跑的轮次 | 新查询 | 新恢复 |
|---|---|---|
| 查询 | **拒绝** —— 跨客户端那一种 | **拒绝** —— 见 §3.6 |
| 恢复 | **拒绝** —— 槽位只有一个 | 允许 —— 同一轮继续 |

★ “查询在跑、恢复来了”被拒，不是误伤：**能答复的时候，抬起那个审批的查询已经结束了**——
审批写在轮次归档之后，而归档之前轮次指针就清了。⇒ “查询在跑”与“审批可答”从不同时成立，
拒绝它不会挡掉任何用法。

**现状**：`已修`。

### 3.4 ★ 起不来的轮次把会话永久锁死

**症状**：一次启动失败之后，这个会话再也起不了新轮次 —— 每个请求都被拒。

**根因**：`Turn.Wait` 在轮次迟迟不就绪时返回 `ErrStartTimeout`（agent 建不起来，loop
从没启动）。那条路上没人调 `fail()`，于是轮次没被释放，`archived` 通道也永远没关。
★ 有了闸门，那就是一个**锁死的会话**，用户连「重新起一轮」来恢复都做不到。

**修法**：启动超时把轮次判失败 —— `Wait` 超时时调 `fail()`，它释放握手，也释放
`archived` 通道。★ 一个既跑不起来、也结束不了的轮次，不该占着会话。

**现状**：`已修`。

### 3.5 ★ 四个时序缺陷的共同点

每一个都是**同一个轮次的两个事实被读在了不同时刻**：

| 事实 A | 事实 B |
|---|---|
| 「agent 干完了」 | 「归档落地了」 |
| 「审批写了」 | 「检查点写了」 |
| 「有轮次在跑」 | 「一轮开始了」 |
| 「轮次放弃了」 | 「槽位空了」 |

★ 每一次的修法都是让**第二个事实等第一个**，而不是去猜它。★ 一个读着滞后指针的闸门
就是个带洞的闸门 —— 无论那检查写得多仔细。

### 3.6 ★ 闸门不问种类，就会放进两个轮次

**症状**：一个 `Submit` 与一个 `Resume` **同时**到达同一个会话 —— 两个都进去了。查询抢走了恢复的槽位，
恢复反过来抢走查询的槽位：先到的那一轮被 `ErrSuperseded` 打死。

**根因**：闸门的判定只问「在跑的轮次是不是查询」，于是「查询撞恢复」与「恢复撞查询」两个方向都放行了。
★ 而两个**预检**（`ensureIdle` 与 `ensureNoLiveQuery`）合起来是**跨类型也互斥**的 —— 预检是友好的错误，闸门才是规矩；
两者说的不是一件事。

★ 预检是**读**，claim 是**写**：两个请求同时到达时，两个预检都读到「会话是空的」，然后两个都去 claim。
★ 预检与 claim 之间那个窗口，就是这条缝。

**修法**：闸门问的是「槽位是不是被占着」，而不是「占着它的是哪种轮次」。⇒ 跨类型也互斥，
只有「恢复撞恢复」放行 —— 那是同一轮对话的两次答复。

**现状**：`已修` —— `agentkit/run/runtime.go` 的 `conflictTurns`；用例在
`agentkit/run/gate_concurrent_test.go`（同时 Submit/Resume 只放行一个，同时 Resume 也只放行一个）。

### 3.7 ★ 放弃的 claim 被复活 —— 会话永久 busy，而且关不掉

**症状**：几个 `Resume` 同时到达同一个会话之后，这个会话**再也起不了新轮次** —— 每个请求都被拒；
而 `Shutdown` 挂在 `Abort` 里**不返回**，进程关不掉。

**根因**：两层。

★ 第一层：`releaseClaim` 把**已经放弃**的 claim 放回槽位。三个 resume 依次占槽：A 占了，B 把 A 顶掉，C 把 B 顶掉；
A 放弃时发现槽位不是它的（走掉），B 放弃时发现槽位是它的 ⇒ 它把 **A 的槽位还回去** —— 可 A 早就放弃了。
⇒ 槽位里留下一个**从未运行过的 loop**（`claimTurn` 建了 loop，装进槽位，然后调用者把它放弃了）。

★ 第二层：eino 的 `TurnLoop.Wait` 自己写着：「**If Run is never called, Wait blocks forever**」。
⇒ `Abort` → `loop.Wait()` 等一个永远不会跑的 loop ⇒ 永久阻塞。

★ 而那个从未运行过的 loop 还让 `ensureNoLiveTurn` 一直读到「有轮次在跑」⇒ 会话永久 busy。

**修法**：`releaseClaim` 只把**还占着槽位**的 claim 放回去 —— 一个放弃了的轮次带着 `released` 标记，
放回去的只能是死轮次。★ 放弃的轮次自己也要打上这个标记。

**现状**：`已修` —— `agentkit/run/runtime.go` 的 `releaseClaim` 与 `agentkit/run/turn.go` 的 `holdsSlot`；
用例在 `agentkit/run/gate_concurrent_test.go`（同时 Resume 只放行一个，且会话不会卡住）。

### 3.8 ★ 行比失败晚到 —— 竞态，而且会话永久 busy

**症状**：`go test -race` 在 `agentkit/run` 报一处数据竞争（`claimTurn` 写 `turn.row`，
`closeRow` 读它）；而生产里的表现是：几个 `Resume` 同时到达之后，**有一轮的行永远不落终态** ——
那一行在账本里一直开着，会话读起来一直 busy。

**根因**：★ **claim 是先占槽、再开行**（`claimTurn` 里 `openRunRow` 在拿到槽位**之后**）。
于是在这两步之间，槽位已经是新轮次的了 —— 顶掉它的那个轮次会立刻 `fail` 它
（`ErrSuperseded`），而它的行**还没到**。

⇒ `closeRow` 看到 `t.row == nil` 就**直接返回**：那一行永远不会被关掉。
⇒ 而 `turn.row` 本身是一个**裸指针**：一个 goroutine 在写它，另一个（顶替它的那个）在读它
⇒ 数据竞争。★ 两个症状是同一个窗口的两面。

**修法**：`turn.row` 收进一把锁（`rowMu`），并且加一道**闩** —— 失败早于行到达时，失败被
**记住**（`rowFailed`），行一到达就补上终态（`setRow`）。★ 终态**取第一个**：与 `runRow` 的
「第一次终态写入获胜」一致 —— 一轮只有一个结局。

**现状**：`已修` —— `agentkit/run/turn.go` 的 `setRow` / `runRow` / `closeRow`；
用例在 `agentkit/run/runrow_test.go`（失败早于行、行到了要补终态；终态取第一个）。


### 3.9 ★ 审批卡在另一个标签页里不会消失

**症状**：同一个会话开了两个标签页。一个在等审批，另一个回答了 —— 于是**回答的那个继续
跑**，而**等的那个**一直举着那张卡；点「同意」被服务端以「这个审批已经被答过了」拒掉。
用户看着一个解不开的死结。

**根因**：★ **审批属于会话，事件属于轮次。** 抛出审批的那一轮在抛出审批的那一刻就结束
了（`agentkit/run/bridge.go:169` 关掉发布方），而审批还在等一个答案 ⇒ 「审批被回答了」
这个事实**无处可去** —— 它不能骑在旧一轮的总线上，那条总线已经死了。

★ 客户端这一侧也没有别的通道：`applyScan`（`web/src/lib/store/actions.ts:504`）只改
`busy`，**不碰审批卡**；于是等着的那一边**没有任何通道**能知道审批已经被消费。

**修法**：★ 加一条**会话级**的公告通道（`agentkit/run/announce.go`），事实有两个：
「审批被回答」与「新一轮开始」。服务端在 `TakePendingApproval` 成功之后发布前者，在
`startLoop` 之后发布后者；客户端订阅 `GET /v2/sessions/{id}/events`，收到前者就把那张卡
拿掉，收到后者就跟上去继续的那一轮。

★ **事实是实时通知，过去归账本** —— 所以通道**没有回放窗口**：一个迟到的订阅者会去挂
一个早就结束的老 run（`docs/runtime-design.md` §1.7）。

★ **通道一开先 ping 一下**（`transport/sse.go` 的 `Ping`）：fetch 在第一个字节到来之前
不会 resolve，而一个还没开始读的客户端，是没听见事实的客户端。

★ **顺带改变了一件事**：错过一轮开始的第二个界面，现在会**跟上**那一轮，而不是说「忙在
别处」。以前只有「打开会话时那一轮已经在跑」才会挂上（`web/test/e2e/session-busy.spec.ts`
钉的是现在这个行为）。

**现状**：`已修` —— 服务端 `agentkit/run/announce.go`、`transport.RenderSession`、
`api/v2_session_events.go`；客户端 `web/src/lib/session-events.ts`、
`web/src/lib/store/actions.ts` 的 `watchSession` / `applySessionFact` / `followRun`。
用例在 `agentkit/run/announce_test.go`、`transport/session_test.go`、
`api/v2_session_events_test.go`、`web/test/session-events.test.ts`、
`web/test/e2e/session-events.spec.ts`。

## 4. 文件系统后端

### 4.1 ★ 一个静默的零结果 bug

**症状**：提示词教模型写的每一次 `glob` 调用都返回「没找到文件」—— 而且是**安静地**返回。

**根因**：提示词教的是 `glob(pattern="<projectRoot>/**/*.go")`，而后端拿 `req.Pattern`
去匹配**相对遍历根**的路径。绝对 pattern 永远匹配不上。

**修法**：绝对 pattern 会被**归一化**成相对遍历根的形式。

**现状**：`已修`（`agentkit/fsx`）。

### 4.2 ★ ripgrep 缺席时的降级

**症状**：机器上没有 `rg` 时，`grep` 工具要么报错，要么慢到不可用。

**根因**：`grep` 工具的引擎是 `rg`，而它不是每个机器都有的。

**修法**：**内置引擎降级** —— `rg` 不在就用自带的搜索。★ 值得在日志里留一行：
一台用内置引擎跑的机器回答的是同样的问题，但**边不一样**。

**现状**：`已修`。★ 降级不是「等价」：内置引擎没有 `rg` 的那些边，答案会少一点。

## 5. MCP

### 5.1 ★ 工具集在装配时定下，中途不变

**症状**：一台 server 在这一轮的中途起来了，模型这一轮**没有**它的工具。

**根因**：工具集是**每一棵树构建一次**的，中途不变。

**修法**：不修。★ 「下一轮」是字面意思的下**一轮**，不是这一轮的后半段 —— 而这一轮
本来就不该为了一台刚起来的 server 而重来一次。

**现状**：`已知边界`。

### 5.2 ★ 一台 server 是一个客户端，不是每次调用一个

**症状**：两个调用方同时要同一台 server，于是起了两个子进程。

**根因**：启动如果没有单飞，两个子进程可能报告**不同的**工具清单 —— 而模型看到的工具
取决于**哪个进程答的**。

**修法**：启动单飞，第一个调用方起客户端，其余调用方找到同一个在飞的作业。
★ 子进程的生命归**管理者**，不归调用方 —— 调用方是**一轮**，一轮结束时的取消会把子进程
一起带走，于是那个工具还在表里却答不上话。

**现状**：`已修`。

### 5.3 ★ 崩掉的子进程要能自己回来

**症状**：一台 server 的子进程死了，之后每次调用都失败。

**根因**：崩了不是配置错误，而一个死掉的 server 就那么死着才是最糟的结果。

**修法**：失败的调用把客户端标记为坏的；下一次调用重新拉起。★ 状态回到「还没起来」，
不是「起不来」—— 后者会被退避挡住。

**现状**：`已修`。

## 6. 附件与图片

### 6.1 ★ 用户消息在模型输入里出现两次

**症状**：模型看到同一条用户消息两遍。实测：

```
total messages=3, user 'hello world' count=2
  [0] role=system content="[Context]\n..."
  [1] role=user   content="hello world"
  [2] role=user   content="hello world"
```

**根因**：修复前 `agentkit/run/loop.go` 先把用户消息 `Append` 进会话（落盘了），
再把 `sess.Messages(true)` 当作**历史**传给 `buildInput`，而 `buildInput` 又追加了一次
query。⇒ 三份。

**修法**：**换顺序，不是加判定** —— `buildInput` 先装配、装配成功才落盘，所以历史里
必然还没有本轮消息，模型看到的一份就是唯一的一份。★ 修完之后实测：

```
total=2, user 'hello world' count=1
transcript user 'hello world' count=1
```

**现状**：`已修`（回归用例 `agentkit/run/input_duplication_test.go`）。★ 这个缺陷
**决定了附件清单该挂在哪条消息上** —— 挂在「历史里的那条」还是「最后追加的那条」，
答案取决于模型实际追问的是哪一条。

### 6.2 ★ 哈希里的 mtime，以及那个循环

**症状**：哈希要**复算**才有校验价值，但文件的 mtime 是内核在写的那一刻定的，写之前
拿不到 —— 而哈希要写进文件名里。⇒ 循环。

**根因**：想要「名 + 大小 + mtime」可复算，就得先知道 mtime；可 mtime 要等写完。

**修法**：**一次写，不改名**。`t0` = 收到这次上传的时刻，`hash` 由
`(名, 大小, t0)` 算出，写入后 `os.Chtimes(path, t0, t0)` 把 mtime 钉成 `t0`。
★ `Chtimes` 是对的而不是人造的：`t0` 是这次上传的**真实时刻**，钉上去之后文件自己的
mtime 与哈希里的 mtime 是同一个值，于是「读文件 → stat → 重算哈希 → 对上名字」成立。
★ 另一条路（先写临时名 → stat → 算哈希 → rename）也成立（改名不改 mtime，改的是
ctime），但要额外给 storage 加「重命名」的能力，而且中途会留下一个临时名。

**现状**：`已修`。

### 6.3 ★ 毫秒而不是纳秒

**症状**：同一个文件在另一台机器上算不出同一个哈希。

**根因**：`UnixNano` 在 NTFS 上是 100ns 精度、在 ext4 上是纳秒，但 **FAT32 只有 2 秒**
—— 纳秒级的哈希在 FAT32 上**不可复算**。

**修法**：毫秒。★ 毫秒是「跨平台都稳」的刻度，而两次上传相隔一毫秒这件事根本不会发生。

**现状**：`已修`。

### 6.4 ★ 分隔符用 0x00

**症状**：`ab` + `12` 与 `a` + `b12` 撞成同一个哈希。

**根因**：直接拼接没有分隔符。

**修法**：名字里不会出现 NUL，所以 `名 ‖ 0x00 ‖ 大小 ‖ 0x00 ‖ mtime` 不会有歧义。

**现状**：`已修`。

## 7. 配置与装载

### 7.1 ★ 坏时长的标签是错的

**症状**：`tools/http.yaml` 里的坏 `timeout` 被报成 `watch.debounce`。

**根因**：`collectBadDurations` 给**每一条**坏时长都加上前缀
"watch.debounce fell back to default" —— 而那条前缀是给**某一个**旋钮写的。
★ 根源是 `decl.BadDurations()` 是一个**不带来源信息**的包级列表：出了事，只知道
「某个时长坏了」，不知道是哪个文件、哪个键。

**修法**：还没修。

**现状**：`待修` —— ★ 出处：`agentkit/spec/decl/decl.go` 的 `BadDurations()` 返回
`[]string`，没有位置信息。修法是让它带 `(文件, 键)`。

### 7.2 ★ 配置根**不**向上查找祖先目录

**症状**：同一条命令换个目录跑，读到另一份配置。

**根因**：上游的规则是「env → 上溯 → 自身」，而 openheron 刻意**不**上溯。

**修法**：配置根只有三级 —— 显式 `--config-dir` → `$OPENHERON_CONFIG_DIR` →
`<projectRoot>/.openheron`（不存在则创建）。★ 一旦配置根取决于「进程是从哪儿启动的」，
测试实例也会读到开发者的那份。

**现状**：`已修`（这是与上游不同的地方，不是 bug）。

### 7.3 ★ 空串 api_key 静默无提醒

**症状**：`llm/<id>.yaml` 里写了 `api_key: ""`，启动没有任何提示，调用时才 401。

**根因**：`expandValue` 对**空串**走 `default` 分支（返回 `resolved=true`），而对
**未设置的环境变量**返回 `resolved=false` ⇒ 后者会 `deferFile` 出一条
"api_key env %s is not set" 的提示，前者什么也不说。★ 同一个字段的两种「没有值」，
待遇不一样。

**修法**：还没修。

**现状**：`待修` —— ★ 出处：`agentkit/spec/load.go` 的 `expandValue` 对空串的处理。

### 7.4 ★ 启动会写文件

**症状**：在一个空项目里跑 `openheron`，目录里冒出一棵 `.openheron/` —— 服务端写的，
没人让它写。

**根因**：`openheron init` 一直要人跑，而「第一次启动时没有配置」原本是一个**报错**
（"no configuration at <root>"）。改成自动初始化之后，**启动路径开始写盘**了。

**修法**：门槛收得很窄 —— 只有**缺省根**（`<projectRoot>/.openheron`，也就是用户没点名的
那一个）且里面**没有 `agents/main.md`** 时才写；`--config-dir` 与 `$OPENHERON_CONFIG_DIR`
点名的根一律不写（用户点过去的空目录是他的决定，不是一个缺口）。★ 这条同时是**测试实例
能隔离**的前提：测试实例点自己的根，所以自动初始化在它那儿永远不发生 —— 那棵树的每一份
文件都是脚本放进去的。★ 判据是「有没有 agent 文件」而不是「树空不空」：已经有
`agents/main.md` 的树就是一份配置（能不能装载是另一回事），这份初始化不碰它；计划本身
只补缺的那些，所以半写状态的树是被补齐，不是被替换。

**现状**：`已修`（2026-11-13，`main.go` 的 `initConfigIfEmpty`；门槛的三个断言在
`autoinit_test.go`）。

### 7.5 ★ 有 provider 却没有文件的那一族，文件没有余量

**症状**：想给某族工具配一份文件，却发现写什么都不改变行为。

**根因**：`allows:` 是**加法** —— 代码缺省是地板，文件只能往上加。★ 而那一族的工具
**已经在代码的缺省清单里**（五个只读的 config 工具），所以新文件**没有任何可授予的余量**；
唯一剩下的那个（`config_write`）当时还被一条代码规则恒锁。⇒ 文件写了等于没写 —— 正是
`docs/config-design.md` §4.3「配了没用」那一类。

**修法**：把那一族的**缺省**从代码里搬进文件 —— 也就是让代码那份清单不再点名它们，于是
文件成为这一族例外的唯一来源。★ 代价是**升级行为变化**：没有那个文件的旧实例，那几个
工具从 `allow` 变 `ask`。⇒ 必须配一条 notice 说清楚「写哪个文件、写什么」，否则老用户
只看到突然多出来的审批卡。

**现状**：已修 —— ★ 出处：`agentkit/build/tools.go` 的 `builtinAllowedTools`（现在只覆盖
没有自己文件的那些）、`agentkit/spec/load.go` 的 `noticeConfigToolsAbsent`，以及
`tools/config.yaml`。★ 后来（2026-11-13）那条「`config_write` 恒 ask」的特例也撤了 ——
它当时是这一族里唯一还锁着的名字，而它锁着的理由是「文件能给自己解锁等于没解锁」。
⇒ 现在的规则是**没有例外**：这一族六个工具都由 `tools/config.yaml` 点名放行；出厂模板
不点名 `config_write`（问配置不该要点头，改配置应该要）。

## 8. 模型与协议

### 8.1 ★ 只重试 429

**症状**：厂商返回 503 或过载错误时，这一轮直接失败。

**根因**：重试的判定只认 `429` / `Too Many Requests` / `qpm limit`。

**修法**：还没修。

**现状**：`待修` —— ★ 出处：`helpers/retry.go` 的 `ApplyModelRetry` 的 `IsRetryAble`。
★ 谨慎：扩大重试范围会把**不该重试**的错误也重试一遍（写坏了的请求重试一万次也是错的）。

### 8.2 ★ 厂商的错误码只按 body 分类

**症状**：某个厂商的特有错误码落进了 `bad_request`，提示语指向错误的排查方向。

**根因**：`classifyBody` 只看 body 的 `type` 与 `code` 字符串，命中不了厂商私有的
数值码；而 `raw.Code != nil` 会兜底成 `KindBadRequest`。

**修法**：还没修。

**现状**：`待修` —— ★ 出处：`llm/common/error.go` 的 `classifyBody`。★ 注意
`KindBadRequest` 的提示语已经会点名「厂商自己的工具 schema 校验器拒了这次请求」，
所以**工具 schema** 那类问题有话说；别的仍然指向错误的方向。

### 8.3 ★ 同一块被发了两遍，参数就自带尾巴

**症状**：`config_write` 报 `invalid character '"' after top-level value`。模型连着换好几种写法都失败，
最后自己猜「是大小限制」——而真因与大小无关。

**根因**：模型包装在流式路径上把「带着 `finish_reason` 的那一块」**发了两遍**：一次在
`release()` 里（它已经进了 `held`），一次紧接着 `sw.Send(msg)`。厂商有时把 `finish_reason` 和工具调用的
最后一块放在同一帧里，框架就把最后一块数了两遍 —— 参数于是变成「一个完整对象 +
它自己的尾巴」，JSON 解析器不认。

★ 2026-11-13 的现场里这条被逐字对上了：8 次失败的参数全部是这个形状，尾巴长度 1～11 字节，
正好是最后一块的长度。

**修法**：带理由的那一块不再进 `held`；它和 `held` 一起放出去，只放一次（`agentkit/build/callguard.go:217`）。
★ 另外加一道防御：参数若能被修成「一个完整对象」（剩下的正好是它自己的后缀），就把尾巴去掉再跑
（`repairDuplicatedTail`，`agentkit/build/callguard.go:133`）。

**现状**：已修 —— ★ 出处：`agentkit/build/callguard.go` 的 `callGuardModel.Stream`。★ 实测：同一段载荷，旧二进制把参数发了两遍
（工具报 unmarshal 错），修完之后 39 块参数拼成合法 JSON，一次写成功。

### 8.4 ★ 截断标记被逐块拼接，守卫反而认不出来

**症状**：一次被砍断的调用照样跑了，工具报 `invalid character '{' after top-level value`。

**根因**：`markTruncatedArgs` 是**逐块**替换的，而框架会把各块拼起来 —— 于是拼出来的是 N 个标记连排，那不是 JSON，
`isTruncatedArgs` 用 `json.Unmarshal` 去认，自然认不出来。★ 现场那一行的参数正好是 224 个
`{"__truncated_by_token_limit":true}` 连排（224 = 2×112，两半一模一样）。

**修法**：标记只落在**最后一块**上，前面的块清空；判标记改成「字符串里有没有这个键」，不再要求整串是 JSON
（`agentkit/build/callguard.go:95`）。

**现状**：已修。

## 9. 沙箱 Kit 的实测发现（宿主侧的，不是 Kit 的）

★ 这一节记的都是**在宿主机上操作**时踩到的坑 —— 它们不在 Kit 里，但会让「Kit 坏了」
的错觉成立。

### 9.1 ★ `sbx rm` 会把已发布的端口一起带走

**症状**：重建沙箱后 `sbx ports` 报 "No published ports"，转发不会自己回来。

**根因**：端口映射挂在沙箱上，删沙箱就删了映射。

**修法**：重建之后必须重新 `sbx ports <name> --publish 8080:8080`。★ 一个失效的端口
映射，看起来和「workload 死了」一模一样。

**现状**：`已知边界`（工具的行为，不是我们的）。

### 9.2 ★ `default` builder 会把 Kit 的 manifest annotation 丢掉

**症状**：镜像构建成功，而它已经不是 Kit 了 —— 描述文件的 annotation 没了。

**根因**：容器驱动的 builder 才保得住 manifest，`default` 的那个不保。

**修法**：`task kit` 指定了容器驱动的 builder。

**现状**：`已修`。

### 9.3 ★ 那个 builder 还需要 `--load`

**症状**：`task kit-check` 去找一个从没导出的镜像。

**根因**：不给 `--load` 的话，结果留在构建缓存里，本地没有镜像可供校验。

**修法**：构建命令带 `--load`。

**现状**：`已修`。

### 9.4 ★ 凭据和它的网络规则必须一起声明

**症状**：只带凭据的 mixin 组合不上。

**根因**：运行时要 inject 的域名**逐字**出现在 allow 列表里，而 workload 的裸 `*`
不满足。

**修法**：mixin 把主机名一并加上，两张 allow 列表取并集。

**现状**：`已修`。

### 9.5 ★ `sbx run --name` 是重启沙箱，不会用改过的 Kit 重建容器

**症状**：改了 Kit，探针看起来像「没跑」。

**根因**：重启会悄悄留着旧镜像。

**修法**：改了 Kit 要 `sbx rm` 再 `sbx run`。

**现状**：`已知边界`（工具的行为）。

### 9.6 ★ 宿主需要 `/dev/kvm`，用户得在 `kvm` 组里

**症状**：沙箱报 "KVM error: Permission denied"，而守护进程自己看着一切正常。

**根因**：**服务那个 socket 的守护进程**才是需要 `/dev/kvm` 的那一个。★ CLI 在够不着
守护进程时会自己拉起一个，而它继承的是**调用者的用户组** —— 于是在一个 KVM 设备按组
授权、而 CLI 的用户不在该组里的宿主上，CLI 自己拉起的那份会失败。

**修法**：把用户加进 `kvm` 组；开设备的是守护进程，不是 CLI。

**现状**：`已知边界`（宿主配置）。

### 9.7 ★ `openheron init` 对配置是全有或全无

**症状**：沙箱起来了、看着一切正常，端口上却没人听。

**根因**：`openheron init` 会对合并后的结果做一次试加载，**拒绝写下一个加载不了的
配置** —— 而预置的 agent 引用了一个谁也没定义的 subagent（`subagents: [task]` 而没有
`agents/task.md`），正是这种情况。★ 什么都不会被写，服务随后以 "no configuration at …"
拒绝启动。★ 而**启动 hook 把这个失败吞了**，于是症状看起来像「端口没人听」。

**修法**：Kit 的提示语现在说的是实际发生的事。

**现状**：`已修`。

### 9.8 ★ `sbx kit pack` 是另一套语法

**症状**：v3 的 Kit 目录被拒绝。

**根因**：`sbx kit pack` 要的是 `spec.yaml` —— v1/v2 的产物格式 —— 并拒绝 v3 的 Kit
目录。

**修法**：v3 的 Kit 以 OCI 镜像或目录的形式流传，**不是 ZIP**。

**现状**：`已知边界`（工具的格式）。

### 9.9 ★ 配方里那个 `apt-get` 步骤被删了

**症状**：基础镜像本来就带着装好的 CA 证书库。

**根因**：那个步骤既多余，又是整个构建里**唯一**需要包镜像的地方。

**修法**：删掉它 —— 构建不再依赖包镜像。

**现状**：`已修`。

### 9.10 ★ `openheron init` 每个配置根只维护一份 provider 配置

**症状**：想种下第二个 provider 的启动 hook 被无视 —— 日志写着
`note: model "deepseek/deepseek-flash" is already declared`。

**根因**：配置根里**已经有**一个可用的模型时，`init` 会复用它，并忽略 `--provider`、
`--env` 与 `--base-url`。

**修法**：第二家厂商的 `llm/*.yaml` 归用户自己写；mixin 不带 startup hook。

**现状**：`已知边界`（现状即结论）。

### 9.11 ★ 端口要通，得同时满足两件事

**症状**：代理拨 bridge 网关 `172.17.0.1:8080` 拿到 connection refused，而 workload 明明活着。

**根因**：★ **不是转发层的问题** —— Kit 根本没声明 `port@1`，运行时于是没有可转发的服务；
手工 `sbx ports --publish` 只绑上一个背后没东西的宿主套接字，客户端看到的是
`HTTP/1.1 501 Not Implemented`。

**修法**：Kit 声明 `port@1`（容器端口 8080），**并且**要开过一次 agent 会话 ——
`--detach` 不开会话，服务就没人拉起。诊断看运行时日志里那条
`TCP proxy failed to dial backend`。

**现状**：`已修`。★ 这两件事缺一不可。

## 10. 文档与约定

### 10.1 ★ 行号引用会漂移，而 lint 只校「行存在」

**症状**：文档里的 `file.go:123` 指着别的函数 —— 读者找过去，看到的是一段不相干的代码。

**根因**：代码一改，文档里的行号就旧了。★ `task lint-docs` 只校**那一行存在**
（文件够不够长），**不校它指的是不是那个东西** —— 所以漂移能一直躺着。

**修法**：改代码时**同时**改文档里的行号。★ 「内容驱动的重定位」：按符号内容去
定位，而不是按旧行号加减。★ 拿不准就**不写行号** —— 写 `agentkit/build/pool.go`
比写一个错的 `agentkit/build/pool.go:31` 好。

**现状**：`待修` —— ★ 这是仓库级的习惯问题，不是某一处。★ 一次专门的重定位值得做：
全仓约 1500 处 `file:line` 引用。

### 10.2 ★ 上游文档与 openheron 的实际不符

**症状**：照上游文档写出来的代码在这里跑不起来 —— 配置根、模块路径、依赖表、结构体
字段，十几处都不一样。

**根因**：上游是另一个仓库、另一套设计。openheron 从上游抽出来的时候，把撤销后的
形态一起继承了（`agentkit/spec` → `agentkit/run` 的破例外，上游撤销了，openheron
**从未有过**）。

**修法**：★ 上游文档**只能当参考，不能当规范**。下表记着每一处差异与原因 ——
左边是上游的说法，右边是 openheron 的实际。★ 表里的 openheron 一侧在 2026-11-12
逐条核对过；上游一侧无法在本仓库核实（上游不在这儿），照抄的是当时的记录。

| 上游的说法 | openheron 的实际 | 原因 |
|---|---|---|
| 配置根按「env → 上溯 → 自身」解析 | 它恰好按**三级**解析：显式 `--config-dir` → `$OPENHERON_CONFIG_DIR` → `<projectRoot>/.openheron`（不存在则创建），并且**刻意没有**上溯祖先目录（`agentkit/spec/config.go:417-418`） | 一旦配置根取决于「进程是从哪儿启动的」，同一条命令换个目录跑就会读到另一份配置 —— 测试实例也会读到开发者的那份 |
| 测试实例靠「预置一份 `.heron/`」来隔离 | 隔离靠 `.env.test` 加 `task test-env-backend` / `test-env-reset` / `test-env-sync` 三个目标：实例跑在 `:18099`，**自带配置根与自带会话目录** | 端口不同不等于数据隔离 —— openheron 把会话写在 `.openheron/state/` 下，一个写在那儿的测试会污染真实历史 |
| `agentkit/spec` 包有 **115** 个 `Test` 函数 | 它有 **228** 个 | 包随代码一起长大 |
| 模块路径是 `heron/...` | 是 `github.com/ouranoshong/openheron/...` | `go.mod` |
| `agentkit/spec` 的 heron 依赖只有 `persist` | 有**五个**：`agentkit/obs`、`agentkit/spec/decl`、`agentkit/templates`、`persist` 与它自己 | `obs` 是日志的装配处；「声明」（`decl`）与「行为」（`spec`）的拆分是 openheron 自己的设计 |
| `docs/architecture.md` §2.2 说 `agentkit/spec` import `kb` | **生产代码**并没有 —— 只有用例文件 `agentkit/spec/policy_contract_test.go:7` 这么做 | 校验知识库配置只需要声明（`decl.Model.IsEmbedding`），不需要 `kb` 包本身 |
| `llm.ConfigFromEnv` | 它是 `build.ConfigFromEnv`（`agentkit/build/config.go:15`） | 配置包不能依赖厂商协议层，而 `llm/common` 是零 openheron 依赖的叶子包 |
| 模型结构体 | openheron 的 `decl.Model` 带 `kind`、`dimensions` 与 `context_window`；`summarize_at` **存在但已废弃** —— 阈值属于 agent（`decl.Agent.SummarizeAt`，`agentkit/spec/decl/decl.go:162`） | 阈值是 agent 的策略，窗口是模型的 —— 两者只在一个地方相遇（`agentkit/spec/summarize.go:25`） |
| agent 结构体 | openheron 的 `decl.Agent` 把 `tools`、`skills` 与 `mcps` 都带成裸 `[]string`：**不写 = 一个都没有**，空列表也是没有（`agentkit/spec/decl/decl.go:113`、`:134`、`:150`） | 「没声明」与「声明为空」现在意思相同，所以那个用来区分它们的指针没了 —— 而它们共享的缺省就是**没有** |
| server 结构体 | openheron 的 `decl.Server` 带 `lease_ttl`（还有 `frontend_mode`，`agentkit/spec/decl/decl.go:273`、`:297`） | 租约的预算必须长于 provider 自己的超时，而两者住在不同的文件里 |
| `internal/atomicfile` | 是 `persist/atomicfile.go`，一个**顶层**叶子包 | openheron 没有 `internal/` 目录；`persist` 装着 `AtomicWrite` 与 `FileLock` 一族 |
| `kb` 不 import 任何 heron 包 | `kb` import 了 `obs` 与 `persist` | `obs` 是日志的装配处，知识库也需要那个共用的原子写 |
| `agentkit/spec` → `agentkit/run` 的破例在上游已于 2026-10-21 撤销 | openheron **从未有过** —— 代码面是在撤销之后抽出来的 | 撤销是上游的一个设计决定；openheron 直接继承了撤销后的形态 |
| `models[].context_window` 是纯元数据，不生效 | 它**生效**：它是压缩阈值的分母（`agentkit/spec/summarize.go:25`） | openheron 实施了压缩与上下文用量看板 —— 见 `docs/runtime-design.md` §3 |
| agent 写了模型没有声明的思考档位会进 `deferred` | **不会** —— 现在是**加载期错误**（`agentkit/spec/load.go:775` 的 `checkReasoning`） | 词汇表属于**模型**；档位名写在 agent 里。★ 这一行在 2026-11-12 复核时改过：旧文档写的是「静默忽略」，那个检查已经搬进了加载期 |
| `go test -race ./...` 全仓是绿的 | 它**曾经不是**：竞态检测器在 `agentkit/run`、`api`、`kb`、`agentkit/spec` 报了竞态 | 四处现在都修好了，`task check`（含 `test-race`）全绿 |
| `tools/` 里的坏超时会被报成 `watch.debounce` | 这个标签是错的：`agentkit/spec/reload.go:124` 给**每一条**坏时长都加上前缀 "watch.debounce fell back to default"，于是 `tools/http.yaml` 里的坏 `timeout` 落到了同一个标签下 | `decl.BadDurations()` 是一个不带来源信息的包级列表（`agentkit/spec/decl/decl.go:851`） |

**现状**：`已知边界`（永远存在，除非 fork）。

### 10.3 ★ 设计文档会把「改造前」的历史一直带着

**症状**：一份设计文档里，现状、提案、改造前的历史、已拍板的决定混在一起，读者要
自己判断哪一句是现在。

**根因**：设计文档是**长出来的** —— 一轮轮改造在同一份文件上留下痕迹。

**修法**：设计文档只留**现状**与**已拍板的决定**；未落地的提案集中到「待决」；
改造前的历史删掉，理由留一句话。★ 坑的细节挪到 `docs/pitfalls.md`。

**现状**：`已修`（2026-11-12，12 份设计文档合并成 4 份主题文档 + 这一份）。

## 11. 已知边界（不是 bug，是有意为之）

★ 这些是**决定**，不是待办。列出来是为了让下一个人不再把它们当 bug 报。

| 边界 | 为什么 |
|---|---|
| 一轮中途起来的 server，工具**进不来** | 工具集是每棵树构建一次的（§5.1） |
| `execute` 之外的文件系统工具**流式**的那条没有 | 只有 `execute` 是流式的（§1.2） |
| `rg` 缺席时的降级**没有** `rg` 的那些边 | 内置引擎是能跑，不是等价（§4.2） |
| 配置根**不**向上查找祖先目录 | 「从哪儿启动」不该决定读哪份配置（§7.2） |
| 子进程的生命归**管理者**，不归调用方 | 调用方是一轮，一轮结束会把子进程带走（§5.2） |
| `sbx rm` 带走已发布的端口 | 工具的行为（§9.1） |
| 宿主需要 `/dev/kvm` 且 CLI 的用户得在 `kvm` 组里 | 守护进程才是需要设备的那一个（§9.6） |
| v3 的 Kit 不以 ZIP 流传 | `sbx kit pack` 是另一套语法（§9.8） |
| 这台 Windows 机器上 Playwright 的转译缓存写不进去 | 换一个 `TEMP` 就好（§12.1） |

## 12. 测试环境

### 12.1 ★ Windows 上 Playwright 的转译缓存目录写不进去

**症状**：`bun run test:e2e` 报 `EPERM: operation not permitted, open
'...\Temp\playwright-transform-cache\...\*.map'`，然后是 `No tests found` —— 而**别的**
spec 正常列得出来。

**根因**：Playwright 把转译结果写在系统临时目录下的 `playwright-transform-cache`。★ 在这台 Windows 机器上，那个目录
（及它的子目录）不接受 Playwright 写新文件 —— 目录能建，文件也能手写，但 Playwright 写的时候是 `EPERM`。

★ 症状只落在**改过的**那个 spec 上：缓存键里有内容的哈希 ⇒ 只有内容变了的那一份要重写。

**修法**：把 `TEMP` 指到仓库里的一个目录：

```powershell
$env:TEMP = 'C:\Users\Ouran\Documents\CodeLab\openheron\tmp\pwtemp'
$env:TMP = $env:TEMP
```

**现状**：`已知边界` —— ★ 这是这台机器的毛病，不是仓库的。★ 别去删系统临时目录（它在工作区之外，
回收站工具不会收它）；换一个 `TEMP` 就够了。

### 12.2 ★ Bun 的假后端会掐掉十秒以上的连接

**症状**：假后端上的会话通道在十秒后被掐断（Bun 的告警：`Bun.serve() timed out a
request after 10 seconds`），于是 E2E 里「另一个界面听见了审批被回答」这件事**时好时坏**
—— 而且坏的时候不像失败，像没发生。

**根因**：`Bun.serve` 的 `idleTimeout` 默认十秒，而会话通道是一条**长连接** —— 它在
没有事实的时候一个字都不说。

**修法**：`idleTimeout: 0`（`web/test/fake-server.ts`）。★ 真后端没有这个问题：它自己
的 keepalive 会在通道空闲时写一帧。

★ **配套的一条**：通道一开就**先 ping 一下**（`transport/sse.go` 的 `Ping`）。fetch 在
第一个字节到来之前不会 resolve —— 不 ping 的话，客户端要等到第一条事实才开读。

## 13. 构建与安装

### 13.1 ★ 非终止的错误让安装器说了谎

**症状**：`task install` 失败了 —— 目标文件「正由另一进程使用」—— 然后它照样打印
`installed: C:\...\openheron.exe`。★ 旧二进制还在跑，而任务说新的已经装上了。

**根因**：Windows 分支是一条 PowerShell 一行命令，`Copy-Item` 失败之后紧接着就是
`Write-Host 'installed: ...'`。★ PowerShell 的**非终止错误**不会中断语句序列：复制失败被记成一条
错误记录，下一句照样跑；`powershell -Command` 的退出码也因此是 0 —— 任务**成功**返回。

★ 同一个毛病在 `uninstall` 里也有，而且**两个平台都有**：Windows 那条用
`-ErrorAction SilentlyContinue` 把失败吞掉；sh 那条**根本没有 `set -e`** —— `rm -f` 失败之后照样
打印 `removed:`，退出码还是 0。

★ ★ **sh 的 `install` 本来就没有这个毛病**：`set -e` 让任何一步失败都中止脚本，`installed:`
根本不会打印。那条分支上真正缺的是**校验** —— 它只知道「复制没报错」，不知道「装上去的是不是
刚构建的那一个」。

**修法**：★ **复制在前，宣称在后**，而且失败必须**终止**。

- Windows：复制挪进 `scripts/install-local.ps1`（`$ErrorActionPreference = "Stop"` + `try/catch`），
  失败时退出码非 0，`installed:` 根本不会打印。
- sh：给 `install` 加一道 `||` 守卫（失败时打印原因与「先停掉正在跑的实例」的提示，然后退出 1）；
  `uninstall` 补上 `set -e` —— `rm -f` 对**不存在**的路径仍然算成功，所以失败必须是真失败。

★ 两个平台都补上了**版本校验**：装完跑一次 `openheron version`，对不上就不算装上 ——
「装上了」要**能被验证**，而不只是「复制没报错」。★ PATH 提示只在那个目录**真的不在用户 PATH 上**
时才打 —— 每次都打的提示等于没有提示。

**现状**：`已修` —— `scripts/install-local.ps1` 与 `Taskfile.yml` 的 `install` / `uninstall`。
★ 发布版的安装器（`scripts/install.ps1`）本来就没这个毛病：它开头就设了
`$ErrorActionPreference = "Stop"`，而且**装完、验完版本**才说装好了。
