# 会话操作事件日志（设计）

> 一句话：**每个会话目录里一份 `operations.jsonl`，一行一条操作事件，append-only，
> 永不裁剪。** 它回答一个问题 —— 这个会话上发生过什么，按时间顺序。

> 范围：日志的形状、写盘点、读面、界面、失败策略与落地顺序。
> 不包含：轮次的状态机（见 `docs/runtime-design.md`）、审批闸门本身
> （见 `docs/authorization-design.md`）、对话与工作区的语义（见 `docs/ui-design.md`）。
> 本文按 AGENTS.md 的文档语言规则**只有中文版** —— 架构与代码设计类文档不做双语。

★ **本文描述的是现状。** 操作时间线已经落地：词汇在 `storage/operations.go`，
文件在会话目录下的 `operations.jsonl`，写点在 API 层与运行时，
`/v2` 有 `GET /v2/sessions/{id}/operations` 这条路由，界面在输入框下面那一行。
⇒ 下面凡是写「是」「就是」的地方，指的是**代码今天的样子**；
§8 记着它落地时**已经拍板的五条** —— 它们各自的理由也记在那里。

---

## 1. 为什么要有它

★ **在这条时间线落地之前**，一个会话的「发生过什么」散在四个地方，
**没有一个地方能按时间回答这个问题**：

| 去处 | 记什么 | 寿命 | 回答不了什么 |
|---|---|---|---|
| `session.jsonl` | 对话消息 | 追加，无上限 | 「谁动过这个会话」 |
| `state.json` | **当前**状态 + 最近 50 条审批决定 | 整体重写 | 「档位昨天是什么」「文件删过没有」 |
| `runs.jsonl` | 轮次快照 | 追加，**最多 200 行** | 「人做过什么」「这一轮为什么结束」 |
| 公告通道 | 三个实时事实 | **内存，不落盘** | 进程一重启就没了 |

三个具体的问题，今天都答不出来：

1. **授权档位的历史**：`state.json` 只存**当前**档位（`storage/filestore/session.go:745-754`）。
   「昨天有人把它调到 `trusted`，今天又调回 `default`」——除了会话记录里那两个字段，
   什么都不剩。
2. **文件的历史**：工作区只存**现在**的文件（`storage/filestore/session.go:773-775`）。
   「上周上传过什么、后来删了哪个」没有答案。
3. **审批的完整历史**：`state.json` 的 `decisions` 只留**最近 50 条**
   （`storage/filestore/session.go:28` 的 `maxAuditRecords`，裁剪在 `:471-473`），
   而且只有「决定」，没有「挂起」。

★ **时间线要记的是「顺序」**，而顺序正是上面每一处都丢掉的。

★ **这个缺口现在补上了**：会话目录下的 `operations.jsonl`（§5.2）
把人的操作与系统的事件按发生顺序记成一行一条，**永不裁剪**。
下面 §5 讲的是它今天的样子。

---

## 2. 现状：会话目录里今天有什么

### 2.1 四个文件，各管一件事

会话目录由 `sessionDir(root, id)` 划定（`storage/filestore/session.go:766`），
里面的文件名是三个常量（`storage/filestore/session.go:760-764`）：

| 文件 | 谁写 | 形状 | 寿命 |
|---|---|---|---|
| `session.jsonl` | 会话追加消息 | 首行 header（`storage/filestore/transcript.go:25-32`），之后一行一条消息 | 追加，**无上限** |
| `state.json` | 每次状态变化**整体重写** | 一个 JSON 对象（`storage/filestore/session.go:730-755`） | 当前状态 + 最近 50 条决定 |
| `runs.jsonl` | 运行账本 | 一行一条 run 快照 | 追加，**最多 200 行**（`storage/filestore/runledger.go:29-36`） |
| `operations.jsonl` | ★ **操作时间线**（§5.2） | 一行一条操作事件 | 追加，**无上限** |
| `workspace/` | 上传 | 目录 | 会话的工作区 |

★ **`state.json` 整体重写**（`storage/filestore/session.go:825` 的 `writeState`），
所以任何「会变长的列表」都不能住在里面 —— 住进去就意味着每次变化都要重写整份历史。
`decisions` 之所以要有 50 条的上限，正是这个原因。

### 2.2 追加是怎么做的

- **对话**：`initTranscript` 用 `O_CREATE|O_EXCL` 写首行 header 并 `Sync`
  （`storage/filestore/transcript.go:109-135`）；之后每条消息用 `O_APPEND` 追加
  （`storage/filestore/transcript.go:172-191`）。
- **账本**：`Append` 用 `O_CREATE|O_APPEND` 写一行并 `Sync`
  （`storage/filestore/runledger.go:83-118`）；超过 200 行时走 tmp→rename 原子裁剪
  （`storage/filestore/runledger.go:126-164`）。

★ **`Sync` 是账本要的，对话不要**：账本那一行是「消息已经在盘上了」的凭据
（`storage/filestore/runledger.go:107-108`），对话的消息有 checkpoint 兜底。

### 2.3 读是怎么做的

`readRunRows` 扫全文件，**坏行跳过并计数**（`storage/filestore/runledger.go:261-295`）：
崩在两次写之间的最后一行会被丢掉，那一行之前的行都还在。
★ 这就是「torn tail」——**尾行写坏了，不是整个文件坏了**。

### 2.4 会话内的单调序号已经有了

`NextEpoch` 分配轮次的围栏 epoch：**从文件播种**，之后在内存里续
（`storage/filestore/runledger.go:200-218`）。
★ 播种是必须的：一个只在内存里的计数器重启后会重复，而重复的序号会让读者
分不出新旧两个主人。

### 2.5 「事件」今天有四个去处

1. **对话** —— `session.jsonl`。
2. **当前状态与审批决定** —— `state.json` 的 `decisions`
   （`storage/filestore/session.go:743`），上限 50 条。
3. **轮次生命周期** —— `runs.jsonl`，一行一条快照，同一个 run 的多张快照由
   `storage.FoldRuns` 折叠成最后一张（`storage/runledger.go:227-241`）。
4. **实时公告** —— 内存里的公告通道（`agentkit/run/announce.go`），
   ★ **不落盘**：进程一停，事实就没了。

公告通道今天只说三个事实（`contract/runevent.go:52-83`）：

- `RunEventApprovalPending` —— 有审批在等（`agentkit/run/announce.go:141-149`）
- `RunEventApprovalResolved` —— 审批答了（`agentkit/run/announce.go:168-182`）
- `RunEventRunStarted` —— 新的轮次接管了会话（`agentkit/run/announce.go:190-196`）

★ 公告通道是**会话级**的事实，不是轮次级的 —— 因为审批比提出它的那一轮活得久
（`agentkit/run/announce.go:151-167`）。这一条决定了下面日志的归属：**日志是会话的**。

---

## 3. 现状：可以复用的零件

| 需要什么 | 今天有什么 | 在哪儿 |
|---|---|---|
| 一个会话内的追加文件 | 账本的 `Append` | `storage/filestore/runledger.go:83-118` |
| 容错的读 | `readRunRows` | `storage/filestore/runledger.go:261-295` |
| 会话内的单调序号 | `NextEpoch` 的播种模式 | `storage/filestore/runledger.go:200-218` |
| 会话级的附属存储 | `Session.RunLedger()` | `storage/storage.go:178-184` |
| 事实的形状 | `storage.ApprovalRecord` | `storage/storage.go:207-214` |
| 线路的挂法 | 路由表 | `api/v2_files.go:40-122` |
| 事实的形状（线上） | `decisionView` | `api/v2_types.go:306-313` |

★ `Session.RunLedger()`（`storage/storage.go:178-184`）是这套设计的模板：
**会话自己提供它的附属存储**，附属存储知道自己在会话目录里的位置。

---

## 4. 现状：写盘点

这些是「人会做的、会话会经历的操作」在代码里的落点。**每一处都写了一行**：

| 事件 | 写在哪 | 写的人 |
|---|---|---|
| `session.created` | `api/v2_sessions.go:147`（创建与挂项目之后） | API 层 |
| `session.renamed` | `api/v2_sessions.go:261`（`SetTitle` 之后） | API 层 |
| `session.agent_changed` | `api/v2_sessions.go:331`（`SetAgentID` 之后） | API 层 |
| `session.authorization_confirmed` | `api/v2_authorization.go:87`（确认记下之后）；建会话时带着档位的是 `api/v2_sessions.go:160` | API 层 |
| `session.authorization_changed` | `api/v2_authorization.go:97`（档位改掉之后）；建会话时带着档位的是 `api/v2_sessions.go:166` | API 层 |
| `file.uploaded` | `api/v2_files.go:252`（`PutFile` 之后） | API 层 |
| `file.deleted` | `api/v2_files.go:535`（`DeleteWorkspaceFile` 之后） | API 层 |
| `run.started` | `agentkit/run/runtime.go:399`（账本的第一行之后） | 运行时 |
| `run.finished` | `agentkit/run/runrow.go:101`（终态行之后） | 运行时 |
| `approval.requested` | `agentkit/run/bridge.go:332`（审批落盘之后、公告之前） | 运行时 |
| `approval.resolved` | `agentkit/run/runtime.go:769`（`RecordDecision` 之后） | 运行时 |

★ **两处顺序是现成的规矩，日志照抄了**：

- 授权：**先记确认，再改档位**（`api/v2_authorization.go:70-96`）——
  确认是一个关于人的事实，与档位写不写得成无关。
- 审批：**先落盘，再公告**（`agentkit/run/bridge.go:220-234`）——
  「不落盘就不能回答的审批，别公告出去」。

★ `runRow.finish` 是轮次收尾的**唯一收口**（`agentkit/run/runrow.go:72-89`）：
`bridge.go:261`（等审批）、`bridge.go:287`（正常结束）、`loop.go:294`（失败）、
`turn.go:419`（中止）全都走它。日志记轮次的结束，就记在这里 —— 一处，而不是四处。

---

## 5. 现在是这样

### 5.1 落地的形状

> ★ **一份文件、一套词汇、一个追加器。**
> 词汇在 `storage/operations.go`（`Operation` 与 `OperationLog`），
> 文件在 `storage/filestore/operations.go`（`fileOperationLog`），
> 线路在 `api/v2_operations.go`，界面在 `web/src/components/operations-panel.tsx`。

> ★ **会话自己提供它的附属存储**：`Session.Operations()`
> （`storage/storage.go:184`，实现 `storage/filestore/session.go:359`）——
> 与 `Session.RunLedger()` 同一个形状，附属存储知道自己在会话目录里的位置。

### 5.2 文件名与位置

**`operations.jsonl`**，与 `session.jsonl` / `runs.jsonl` 同目录。

★ **为什么不叫 `events.jsonl`**：`/v2/sessions/:id/events` 已经是**实时公告通道**的路由
（`api/v2_files.go:73`，实现见 `api/v2_session_events.go:22-91`）。
两个同名的东西，一个转瞬即逝、一个落盘留痕 —— 名字必须分开。
**「operations」是落盘的时间线，「events」是实时的公告。**

★ **它与会话同生共死**：删会话就是删目录（`api/v2_sessions.go:384-408`），
时间线跟着一起没了。这是**会话内**日志的固有限制，见 §5.8。

### 5.3 一行长什么样

JSONL，一行一个 JSON 对象：

```json
{"seq":7,"at":"2026-11-16T12:39:53.9730104Z","kind":"session.authorization_changed","actor":"user","mode":"trusted"}
```

| 字段 | 类型 | 含义 |
|---|---|---|
| `seq` | uint64 | 会话内单调递增，从 1 开始 |
| `at` | RFC3339Nano，UTC | 事件发生的时刻 |
| `kind` | string | 事件名：`<对象>.<过去式动词>` |
| `actor` | string | `user`（人做的）/ `system`（系统做的） |
| 其余字段 | 按 `kind` 而定 | 见 §5.5 |

★ **没有 header 行**，与 `runs.jsonl` 一致。理由：`session.jsonl` 的 header 是因为它的行
是 eino 的消息（一个外来形状，需要版本检查）；`operations.jsonl` 的行是 openheron 自己的
结构，而读者本来就是**容错**的（坏行跳过，`storage/filestore/runledger.go:277-284`）。
★ 形状真要变，JSON 加字段是兼容的；真要改语义，那时再引入 header。

★ **`Approved` 用指针，不用 `bool`**：审批那一行要能表达「被拒绝了」。
`bool` + `omitempty` 会把 `false` 一起吞掉 —— 拒绝的审批会变成「没有结论的审批」。
契约层已经有这个坑：`contract.RunEvent.Approved` 是裸 `bool`
（`contract/runevent.go:132`），所以它不能带 `omitempty`。

### 5.4 ★ 载荷的规矩

> **一行只带「看懂这条时间线所需的最小字段」，不带别处已经完整保存的正文。**
> 工具参数、消息正文、文件内容一律不进日志 —— 它们在 `session.jsonl` 里。

> ★ **日志是投影，不是真相。**
> **没有任何一处逻辑把日志当权威来读**：闸门读 `state.json`，
> 轮次状态读 `runs.jsonl`，对话读 `session.jsonl`。
> 日志缺一行，那些家不受影响；日志与家不一致时，**家说了算**。
> 这与公告通道的规矩是同一条（`api/v2_session_events.go:30-34`：
> 「事实是通知，真相是那次扫描」）。

★ 所以一行**可以**重复别处的字段（比如轮次的终态 `state`、档位 `mode`），
但那只是**给人看的投影**；写日志的代码**永远不从日志读回**一个值。

### 5.5 事件清单

| `kind` | `actor` | 什么时候写 | 这一行带什么 |
|---|---|---|---|
| `session.created` | `user` | 创建会话并挂好项目之后（`api/v2_sessions.go:147` 之前） | `agent_id`、`project_id` |
| `session.renamed` | `user` | `SetTitle` 成功之后 | `title` |
| `session.agent_changed` | `user` | `SetAgentID` 成功之后 | `agent_id` |
| `session.authorization_confirmed` | `user` | 完全授权的确认**被记下**之后（`api/v2_authorization.go:79` 之后；建会话时带着档位的是 `api/v2_sessions.go:160`） | `mode`（固定是 `full`） |
| `session.authorization_changed` | `user` | 档位**真的改掉**之后（`api/v2_authorization.go:93` 之后；建会话时带着档位的是 `api/v2_sessions.go:166`） | `mode` |
| `file.uploaded` | `user` | `PutFile` 成功之后 | `path`（工作区相对）、`name`（原名）、`size` |
| `file.deleted` | `user` | `DeleteWorkspaceFile` 成功之后 | `path` |
| `run.started` | `system` | 账本的第一行写成功之后（`agentkit/run/runtime.go:393` 之后） | `run_id`、`turn_kind`（query / resume） |
| `run.finished` | `system` | 轮次的终态行写成功之后（`agentkit/run/runrow.go:72` 那个收口里） | `run_id`、`state`（终态名） |
| `approval.requested` | `system` | 审批**落盘之后**（与公告同一个位置，`agentkit/run/bridge.go:233`） | `interrupt_id`、`tool_name` |
| `approval.resolved` | `user` | 决定**落盘之后**（`agentkit/run/runtime.go:762` 之后） | `interrupt_id`、`tool_name`、`approved` |

★ **三个设计上的取舍，写在这里免得以后重新吵**：

1. **「确认」和「改档位」是两行，不是一行。**
   代码的顺序就是先确认、后改档位（`api/v2_authorization.go:79` → `:83`），
   两行把这个顺序如实记下来。而且两件事可能**只发生一件**：确认记下了、档位写失败了，
   日志里就只有「确认」那一行 —— 那正是真相。
2. **审批的「挂起」也要记。**
   `state.json` 的 `decisions` 只记**决定**（`storage/storage.go:207-214`），
   「有一个审批在等」这个事实今天只活在内存的公告通道里。
3. **★ `approval.resolved` 与 `state.json` 的 `decisions` 字段重复，但那份是工作集。**
   `decisions` 只留最近 50 条（`storage/filestore/session.go:28`），
   界面上的审批卡片读它；**完整记录在日志里**。
   两行由**同一次写入**产生（同一个 `RecordDecision` 调用旁边写一行），
   所以近 50 条之内两者必然一致；**超出 50 条的那部分只在日志里**，
   `decisions` 根本没有 —— 它是工作集，不是档案。

★ **`actor` 没有 `agent` 这一档**：今天改档位的**唯一**入口是 HTTP 路由 ——
`PATCH /v2/sessions/{id}/authorization`（`api/v2_authorization.go:93`）与
`POST /v2/sessions`（建会话时带着档位，`api/v2_authorization.go:129`），
两处加起来是全仓仅有的 `SetAuthorizationMode` 非测试调用。
等真的有了「agent 自己改档位」的工具，再加这一档 —— 日志不需要为它预留形状。

★ **工具调用与消息正文不进日志。**
   它们在 `session.jsonl` 里，而且那才是它们的家。
   一份文件里同时有两种真相，是两份文件对同一件事各说各话的开始。

### 5.6 体积：无上限

**append-only，永不裁剪**，与 `session.jsonl` 一样。

★ **为什么账本有 200 行上限，日志没有**：
`runs.jsonl` 是**快照视图** —— 最后一行代表当前状态，早于 200 行的那些 run 早就不活跃了，
裁掉不损失什么。日志是**相反的东西**：它的每一行都是一次不可替代的事件，
裁掉一行就是「当时发生了什么」永远查不到了。

代价：长期会话的目录会慢慢变大。上限是一次性的、不可逆的裁剪。

### 5.7 坏行与缺口

读的时候**坏行跳过并计数**（照 `readRunRows`，`storage/filestore/runledger.go:261-295`），
崩在两次写之间的尾行会被丢掉。

★ **序号因此会出现缺口** —— 缺口是「那一行在崩溃时写坏了」的证据，
界面上可以把它标出来，而不是假装日志是完整的。

### 5.8 ★ 写失败怎么办

> **写日志失败，不改操作的结果。** 记一条 warning，操作照常成功。

理由：操作的事实落在**它自己的家里**（`state.json`、工作区、账本），日志是索引。
索引缺一行，是索引的损失，不是事实的损失。
★ 这与代码今天的做法一致：`state.json` 写失败时 `SetAuthorizationMode` 会**回滚**
（`storage/filestore/session.go:534-547`）—— 那是**状态**写失败，必须回滚；
而项目表写失败时只是 warning（`api/v2_sessions.go:137-141`）—— 那是**附属记录**写失败，
事实照样成立。日志属于后者。

★ **但要说清楚代价**：授权档位的**历史**只有这一个家 ——
`state.json` 只存**当前**档位。所以「谁在什么时候把它调成了 `trusted`」这个事实，
**只有日志有**。日志缺这一行，这个事实就没了。
⇒ 这是本设计**唯一一处**「日志是某个事实的唯一家」，其余事件的操作结果都能从别处重建。

★ **会话被删除时，日志跟着没了**（§5.2）：所以**没有** `session.deleted` 这一行 ——
它要写进一个已经被删掉的目录。跨会话的审计（「谁在什么时候删了哪个会话」）
需要一个项目级或全局的日志，**本设计不做**（见 §8）。

### 5.9 存储面

`storage/operations.go`：

```go
// Operation is one operation recorded in a session's operation log.
type Operation struct {
    Seq   uint64        `json:"seq"`
    At    time.Time     `json:"at"`
    Kind  OperationKind `json:"kind"`
    Actor OperationActor `json:"actor"`

    // 按 kind 而定的可选字段
    RunID       string `json:"run_id,omitempty"`
    TurnKind    string `json:"turn_kind,omitempty"`
    InterruptID string `json:"interrupt_id,omitempty"`
    ToolName    string `json:"tool_name,omitempty"`
    Approved    *bool  `json:"approved,omitempty"`
    AgentID     string `json:"agent_id,omitempty"`
    ProjectID   string `json:"project_id,omitempty"`
    Mode        string `json:"mode,omitempty"`
    Title       string `json:"title,omitempty"`
    Path        string `json:"path,omitempty"`
    Name        string `json:"name,omitempty"`
    Size        int64  `json:"size,omitempty"`
    State       string `json:"state,omitempty"`
}
```

★ **轮次的那一行用 `turn_kind`，不用 `kind`** —— `kind` 已经是「这一行是什么事件」了
（`session.created` / `run.started`）。一个字段一个意思。

`storage.Session` 有一个方法，形状照 `RunLedger()`（`storage/storage.go:178-184`）：

```go
// Operations returns this session's operation log.
Operations() OperationLog
```

实现是 `storage/filestore/operations.go`，照 `fileRunLedger` 的形状：
`operationsPath(root, id)`、`Append`（`O_APPEND` + 写 + `Sync`）、`List`（扫全文件、坏行跳过）、
一个从文件播种的 `seq` 计数器。

★ **一个 mutex 同时护住「分配 seq」和「写这一行」。**
两个写者（API 层与运行时）可以并发 —— 人的操作与轮次的起止没有互斥。
如果分配与写入分开锁，两行可能以「seq 大的先落盘」结束，**文件顺序与 seq 顺序就不一致**，
界面按 seq 排会和文件顺序对不上。

★ **每行都 `Sync`**：一次操作之后进程崩了，这一行必须还在 ——
丢一行等于日志说了一件**没发生**的事。
（对话的追加不 `Sync`，因为消息有 checkpoint 兜底；操作没有。）

★ **`seq` 必须从文件播种**（照 `NextEpoch`，`storage/filestore/runledger.go:200-218`）：
只在内存里数会，重启后从 1 开始，和文件里已有的行撞号。

### 5.10 写盘点

按 §5.5 的清单，**十一行、十一处写点**。归属规则：

> **谁做的操作，谁写那一行** —— 除非那条操作的**执行者**与**回答者**不是同一个。
> API 层写**七行**（人做的：会话的四件私事 + 文件的两件），
> 运行时写**四行**（轮次起止两条 + 审批两条）。
> ★ 两边写的是**同一个追加器**（会话自己提供它的附属存储），
> 所以它们的行在同一个文件里、同一个序号空间里。

★ **`approval.resolved` 是唯一的例外**：**写它的是运行时**（答复走的是 Resume 那条路，
`agentkit/run/runtime.go:657-761`），但它的 `actor` 是 `user` ——
**是人答的**，只是答的动作发生在运行时的路上。`actor` 记的是**谁**，
不是**哪段代码写的**。

运行时那几处的具体落点：

| 行 | 写在哪 | 为什么是这里 |
|---|---|---|
| `run.started` | `agentkit/run/runtime.go:393` 之后 | 账本的第一行写成功 = 这一轮存在了 |
| `run.finished` | `agentkit/run/runrow.go:72` 的 `finish` 里，终态行写成功之后 | 四个收尾路径都走它（`bridge.go:261`、`bridge.go:287`、`loop.go:294`、`turn.go:419`），一处就够 |
| `approval.requested` | `agentkit/run/bridge.go:233` 旁边 | ★ 与公告同一个位置、**同一个顺序**：落盘之后才公告 |
| `approval.resolved` | `agentkit/run/runtime.go:762` 旁边 | ★ 与 `RecordDecision` **同一次调用**产生，两边必然一致 |

### 5.11 读面

**路由**：`GET /v2/sessions/:id/operations`，注册在路由表里（`api/v2_files.go:40-122`），
紧挨着 `/runs`。

**响应**：

```json
{
  "threadId": "0f0a…",
  "total": 42,
  "items": [
    {"seq":1,"at":"2026-11-16T12:39:50.1Z","kind":"session.created","actor":"user","agentId":"main","projectId":"work"},
    {"seq":2,"at":"2026-11-16T12:40:02.4Z","kind":"run.started","actor":"system","runId":"9c1…","turnKind":"query"}
  ]
}
```

★ 线上的形状用 camelCase（照 `decisionView`，`api/v2_types.go:306-313`），
文件里的形状用 snake_case —— 这是这棵树一贯的分界（`storage.Run` 用 snake_case，
`runView` 用 camelCase，`api/v2_types.go:256-287`）。

**查询参数**：

| 参数 | 意思 |
|---|---|
| `after=<seq>` | 只给 `seq` 更大的（增量刷新） |
| `limit=<n>` | 在选中的集合里**取最后 n 条**（顺序仍然是升序） |

两者可以同时给：先按 `after` 过滤，再取最后 `limit` 条。

★ **顺序是升序**（最早在前）：时间线是**从上往下读**的，像日志，
不像消息列表那样倒序。界面如果要新的在最上面，由界面自己倒。

★ **`total` 是过滤前的条数**：界面上可以写「共 42 条，显示最后 200 条」。

★ **v1 一次读全文件**（和 `readRunRows` 一样），不实现 tail 读。
上限既然没设，几千行的会话就要读几千行 —— 每行一百多字节，几百 KB，可以接受。
真要优化，是把 `List` 换成从文件尾往前读，那是以后的事。

★ **和公告通道的关系：通道说「有事发生」，日志说明「发生了什么」。**
两者**不合并**。通道是内存里的实时事实（`agentkit/run/announce.go`），
它的三个事实（`run_started` / `approval_pending` / `approval_resolved`）
在日志里都有对应的行；但通道仍然只说「发生了」，
因为它得马上送到，而日志要落盘。

### 5.12 界面

**入口**：输入框下方那一行的**最右端** —— `<ComposerMeta>` 的 `action` 节点
（`web/src/components/composer.tsx:726-734`）：

```tsx
action={
  <span className="ms-auto flex max-w-full shrink-0 flex-wrap items-center justify-end gap-1 @max-3xl:gap-0.5">
    <AuthorizationPicker />
    <ProjectsButton />
    <OperationsPanel />   // ← 新入口，接在最后
  </span>
}
```

★ `ms-auto` 把这一组推到那一行的右端；`ComposerMeta` 把它渲染成那一行的
**最后一个子节点**（`web/src/components/composer-meta.tsx:200-204`，
`body` 用 `<div className="contents">` 包着，所以那一行里只有两个盒子参与排版）。

★ **列窄时这个入口只留时钟**（聊天列 < 768px —— ★ 判据是**列宽**，不是窗口宽，
`docs/ui-design.md` §5.6.5）：`Activity` 这个词让位，时钟留着，
而且它**和事实同一行**（`web/src/components/operations-panel.tsx:90`）。
★ 让位的词没有丢：它在 `title` 与 `aria-label` 里。

★ **新按钮接在最后，不许插在中间**：React 按 index 对齐孩子，
插在中间会把后面那个 entry 换掉 —— 那个 entry 里的弹窗会自己关上。

★ **按钮和它的弹窗包在同一个 wrapper 里**（`web/src/components/operations-panel.tsx:68`：
一个 `<span>` 包住按钮 + 那个 `Dialog`）。条件渲染的东西要放在 wrapper **内部**，
放在外面就会挪动这个 entry 的 index。

★ **入口是一个普通按钮，不是下拉菜单**：下拉菜单要新引入 `radix-ui` 的 DropdownMenu 基建
（这棵树现在只有 `Dialog`），而这个面板**只有一个入口** ——
一个菜单项不值得一整套浮层基建。

**面板**：`web/src/components/operations-panel.tsx`。
Radix 的 `Dialog`，壳是 `sm:max-w-2xl h-[85vh] flex flex-col gap-3 p-4`
（照 `web/src/components/settings/settings-dialog.tsx:47-48`）。
一个可滚动列表（`min-h-0 flex-1 overflow-y-auto`），每条一行：
**时间** + **一句话** + **标识**（run id / interrupt id / 路径 / agent / 档位 / 大小）。

★ **列表是升序**（最早在上）—— 时间线是从上往下读的。
★ **时间是自己写的**（`formatAt`）：浏览器的本地格式跟着 locale 走，
而一个列会随 locale 挪动的日志不是日志。

★ **Esc 走 `onEscapeKeyDown`**：Radix 在 document 的**捕获阶段**监听 Escape，
组件里的 `onKeyDown` 已经太晚（仓库铁律）。

★ **颜色用令牌**：`text-warning`（审批在等）、`text-success`（审批被批准），
其余是默认色 —— 浅色与深色是同一套令牌的两个值。

★ **不认识的 `kind` 显示它自己的名字**，而不是消失：客户端不发明一种 kind，
但也不把服务端新加的一种藏起来。

**数据**：

- `web/src/lib/api.ts` —— `OperationView` / `OperationsView` / `listOperations(id, opts)`
- store —— `SessionEntry` 加 `operations`，action 是 `loadOperations(id)`
  （`web/src/lib/store/actions.ts`）
- selector —— `selectOperations(s, id)`，模块级 `EMPTY_OPERATIONS`
  （`web/src/lib/store/selectors.ts`）
  ★ **永远不要 `?? []`**：每次新建数组都会让 `Object.is` 报「变了」→ 白屏。

**实时**：★ **不给日志加新的通道。**
面板打开时读一次（换会话时再读一次）；之后由**已有的**公告通道触发刷新 ——
那三个事实（`run_started` / `approval_pending` / `approval_resolved`）在
`web/src/lib/session-events.ts:86`、`:96`、`:108`，
store 收到它们时重读一次日志。
通道说「有事发生」，面板重读日志。


### 5.13 测试面

**Go**：

- `storage/filestore/operations_test.go` —— 追加、读回、坏行跳过、
  ★ **并发追加后 `seq` 与文件顺序一致**、重启后 `seq` 接着数。
- `api/v2_operations_test.go` —— 路由、响应形状、`after` / `limit`、坏查询、
  授权那两行的顺序、上传与删除。
- `agentkit/run/operations_test.go` —— 轮次起止、审批那两行、行的先后，
  以及**日志写不进去时那一轮照样跑完**。

**前端**：

- `web/test/operations.test.ts` —— selector 的**引用稳定性**
  （同一个 id 取两次，`Object.is` 必须为真）。
- `web/test/e2e/operations.spec.ts` —— 面板打开、条目渲染、空态。
- `web/test/fake-server.ts` 加 `/v2/sessions/:id/operations`
  （照 `^/v2/sessions/([^/]+)/runs$` 那一支）。

### 5.14 它是怎么落地的

1. **存储面**：`storage/operations.go`、`storage/filestore/operations.go`、
   `Session.Operations()`、测试里的 fake 补齐。
2. **写点**：API 层七处 + 运行时四处（十一行）。
3. **读面**：`api/v2_operations.go` 路由 + `api.ts` + store 的切片与 selector。
4. **界面**：面板组件 + 入口按钮 + Playwright 规格。
5. **文档**：本篇从「提案」改成「现状」，
   `docs/api-v2-design.md` 加路由行与响应形状，
   `docs/ui-design.md` 加面板，`docs/pitfalls.md` 记新坑，
   双语四对同步。

---

## 6. 这份日志不做什么

- **不记工具调用与消息正文** —— 那是 `session.jsonl` 的事（§5.5）。
- **不跨会话** —— 它与会话同生共死（§5.8）。
- **不替代任何一处真相** —— 闸门不读它，轮次状态不读它（§5.4）。
- **不实时推送** —— 面板读一次，之后由已有的公告通道触发重读（§5.12）。

## 7. 与已有文档的关系

| 文档 | 关系 |
|---|---|
| `docs/runtime-design.md` | 轮次的生命周期（`run.*` 两行的来源） |
| `docs/authorization-design.md` | 授权模式（`session.authorization_*` 两行的来源） |
| `docs/api-v2-design.md` | `/v2` 的线路（新路由的位置，§7.5） |
| `docs/ui-design.md` | 界面（面板的位置，§12） |
| `docs/config-design.md` | 不涉及 —— 档位表本身与本日志无关 |
| `docs/pitfalls.md` | 落地时新坑的登记处 |

---

## 8. 已定的五条

1. ★ **「完全授权的确认」单独一行。**
   两行，因为代码的顺序是两件事，而且**可能只发生一件**：确认记下了、档位写失败了，
   日志里就只有「确认」那一行 —— 那正是真相。
   并成一行会少一行，也会看不出「确认了却没改成」。
2. **跨会话的日志不做。**
   已经有其它日志各管一摊：`state.json` 管审批决定（近 50 条），
   `runs.jsonl` 管轮次（近 200 行）。跨会话的审计（「谁在什么时候删了哪个会话」）
   是**另一份设计** —— 真要做，它就不该住在会话目录里。
3. **面板是弹窗（Radix `Dialog`），不是侧栏。**
   这棵树里唯一的弹窗基建就是它（`web/src/components/ui/dialog.tsx`），
   而右侧抽屉得自己实现焦点陷阱与 Esc。
4. **「轮次结束」那一行带终态 `state`。**
   `agentkit/run/runrow.go:72` 那个收口知道终态。
   虽然账本里也有 —— §5.4 说清了日志是投影，两边由同一次写入产生，不会分歧。
5. **入口是普通按钮，不做下拉菜单。**
   这个面板只有一个入口，一整套 DropdownMenu 基建（这棵树现在只有 `Dialog`）
   不值得。
