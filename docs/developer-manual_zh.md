# 开发者说明

> 面向**要改 openheron 代码的人**。想用它请看 [user-manual.md](user-manual.md)，
> 想了解为什么这样设计请看 [architecture.md](architecture.md)。
>
> 约定与红线写在 [AGENTS.md](../AGENTS.md)（仓库根目录），本文是它的展开。
> **动手前先读 AGENTS.md** —— 里面的语言约定和测试环境约定是硬性的。

> English version: [developer-manual.md](developer-manual.md)

## 1. 环境

| | 版本 |
|---|---|
| Go | 1.27.1+（`go.mod` 里写的） |
| Node / bun | bun（前端包管理、测试、脚本） |
| 端口 | 见 `.env.test`（仓库根，已提交） |

```bash
make build-go     # 只构建后端（不含界面），迭代 Go 代码时最快
make web-dev      # Vite dev server（:5173，代理 /v2 到 :8080）
make dev          # air：Go 自动重建重启
make check        # lint + test，提交前跑这个
```

## 2. 仓库结构

```
openheron/
├── main.go            # 入口：flag 解析、配置根、服务启动
├── initcmd.go         # openheron init
├── kbcmd.go           # openheron kb
├── api/               # HTTP 路由、SSE、错误信封
├── agentkit/
│   ├── spec/          # 配置：装载、校验、热重载、写入闸门
│   │   └── decl/      # 纯声明结构体（yaml tag 集中处）
│   ├── build/         # Agent 装配：工具、策略、provider
│   ├── run/           # 运行循环：输入构造、ADK 驱动、审批桥接
│   └── obs/           # 日志出口（唯一）
├── approval/          # 审批决定的表示与校验
├── contract/          # 跨层数据契约
├── helpers/           # 无状态小工具
├── initcfg/           # init 的出厂模板
├── kb/                # 知识库：语料、索引、检索、评分
├── llm/common/        # 厂商协议层（零 openheron 依赖）
├── message/           # 消息内容与来源
├── persist/           # 原子写文件
├── storage/
│   └── filestore/     # 文件存储实现
├── transport/         # AG-UI 事件发射、用量与归属
├── webui/             # 嵌入式前端资源
└── web/               # React 前端
```

### 2.1 依赖方向

**单向**：上层可以引下层，下层绝不引上层。实测的内部 import 图：

| 层 | 包 | 内部依赖 |
|---|---|---|
| **L0** | `contract` `message` `helpers` `persist` `approval` `agentkit/obs` `llm/common` `webui` | 无 |
| **L1** | `agentkit/spec/decl` `storage` `kb` `agentkit/spec` | `decl` 无；`storage`→`contract`；`kb`→`obs`,`persist`；`spec`→`obs`,`decl`,`kb`,`persist` |
| **L2** | `agentkit/build` `agentkit/run` `storage/filestore` `transport` `llm` `initcfg` | 见 architecture.md §2 |
| **L3** | `api` | 几乎全部 |

两条**不能破**的铁律：

**（一）日志只有一个出口。** 业务包里不要直接 `log.Printf` / `fmt.Fprintln(os.Stderr)`，
统一走 `obs.Logger()`。级别与前缀只在 `agentkit/obs/logger.go:18` 一处定义，
而级别决定了一行会不会出现在默认输出里。

一行的长相跟 hertz 的 logger 一样 —— 日期、带微秒的时间、**调用点的文件与行号**、
级别，然后是消息与属性：

```
2026/10/29 22:28:34.346459 main.go:189: [Info] [main] openheron version=dev
2026/10/29 22:28:34.351002 api/v2_run.go:123: [Warn] [run=abc123] request failed err="connection refused"
```

这个信封由 `agentkit/obs/handler.go`（一个 `slog.Handler`）拼出来 —— slog 自带的文本
handler 拼不出来（它的信封是一串属性，源码也只是又一个属性）。★ 文件与行号是**调用点**
的：slog 把调用点的程序计数器记在记录里，handler 打印的就是它。

**唯一的例外是 `llm/common`** —— 它是零 openheron 依赖的叶子包，
引 `obs` 就破了「协议层不依赖运行时」这条铁律，所以它用带同样 flag 的标准 logger
（`llm/common/logging.go`）。它那几行信封相同，但**没有级别标签**：那个包没有级别。
`llm/common` 里出现任何一处 openheron 内部 import 都算回归。

**（二）声明与行为分离。** `agentkit/spec/decl` 只放纯结构体，不引任何其它包；
`agentkit/spec` 放行为（装载、校验、重载、写入）。

### 2.2 别在 API 层复制业务装配逻辑

需要读「某个 agent 的生效工具表」时，由 `agent.Builder` 暴露只读查询，
**不要**在 API 层重算一遍 `Table.Blocked` / `Policy.TierOf`。
理由很直接：重算就会漂移 —— 策略改一次，API 的答案和运行时的答案就对不上了。

## 3. 开发配方

### 3.1 加一个新工具

以 `web_search` 为例，`agentkit/build/nettools.go` 是完整范本。

1. **实现 provider**。工具 provider 是 `agentkit/build/tools.go:19` 的
   `toolProvider` 接口：

   ```go
   type toolProvider interface {
       // ID 返回 provider 名，用于日志与错误信息。
       ID() string
       // Optional 决定这个 provider 失败时是「跳过」还是「整个服务起不来」。
       Optional() bool
       // Provide 返回该 provider 提供的工具批次。
       Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error)
   }
   ```

   注意 `Provide` 返回的是**一批**工具 —— 一个 provider 可以提供多个。

2. **注册**。在 `agentkit/build/tools.go:29` 的 `defaultToolRegistry` 里加上你的
   provider。位置决定顺序，也决定「可选工具」与「必需工具」的区分。

3. **可选性由 `Optional()` 决定**，不是由返回 error 决定。`Optional() == true` 的
   provider 失败时会打一条 Warn 然后跳过（`tools.go:58-60`）——
   缺 API key 不该让整个服务起不来。
   ★ 但注意这条 Warn 之外**没有别的提示**：工具静默不进表，用户在界面上只会发现
   「这个功能好像没有」。新增可选 provider 时要想清楚这条 Warn 够不够。

4. **闸门自动生效**。`agentkit/build/gate.go` 会用外层 policy + 内层
   `disabled_tools` 过滤工具表。你不需要写任何权限代码。

5. **名字不能撞保留名**。`tools.go:39` 的 `reservedToolNames` 列出了
   `ls` `read_file` `write_file` `edit_file` `glob` `grep` `execute`
   `task` `write_todos` `skill`。撞上会在 `Build` 时报错。
   工具名同时是策略表里的键 —— 撞名会让策略表指向错误的工具。

6. **跨 provider 撞名也会报错**：`tools.go:76` 的 `validateBatch` 用 `seen` 跟踪，
   所以两个 provider 提供同名工具会在启动时失败，而不是谁先谁后地覆盖。

### 3.2 加一个新的配置分区

比如新增 `mcp/` 目录：

1. **`agentkit/spec/config.go`**：加目录常量 `DirMCP = "mcp"`，并在
   `Categories` 里加一项（含 `ID`、`Label`、`Dir`、`Ext`）。**热重载目录**和
   **非热重载目录**要分清楚 —— 改一个目录的语义（热不热）影响用户预期。

2. **`agentkit/spec/load.go`**：加 `loaderFor` 的分支（`config.go:296` 的
   `loaderFor`），并实现 `loadXXX`。

3. **`agentkit/spec/decl/decl.go`**：加纯声明结构体。

4. **热重载自动生效**：`agentkit/spec/config.go:76` 的 `hotDirList()` 是从
   `Categories` 里筛出 `Hot()` 为真的目录 —— 你在第 1 步把分类标成热重载，
   监听集合就自动有了。**不需要**再去 `reload.go` 里手工登记一遍
   （漏登记的后果是「改配置不生效且无任何提示」）。

5. **写入闸门**：`api/v2_settings.go` 的可编辑 section 列表要加。
   不热重载的 section **不能**放进可编辑列表 —— 否则用户在界面上改了，
   重启才生效，而界面上没有任何提示。

6. **默认值**：`agentkit/spec/defaults.go`。

### 3.3 加一条 API 路由

1. **注册**。路由注册集中在 `api/v2_files.go:34-65`。**注册顺序有意义**：
   `/v2` 路由必须**先于** catch-all `GET /*` 注册，否则会被 SPA 回退吞掉。

2. **错误信封**。用 `api/v2_types.go` 的 `v2Fail`，不要自己拼 JSON：
   ```json
   {"error": "...", "code": "...", "retryable": false}
   ```

3. **数据来源**。要接新数据源，在 `api.Server` 上加一个 `SetXxxSource` 方法
   （模式见 `api/server.go:75` 的 `SetSkillsSource`），在 `main.go` 里接线。
   **不要**让 handler 直接去读全局变量。

4. **流式响应**。SSE 出口在 `transport` 层，`api` 只负责把事件写出去。
   事件类型不要新造 —— 用 AG-UI 已有的类型。

### 3.4 加一个 Agent / 改拓扑

改 `agents/*.md` 就够了，不需要动代码。但要注意：

- `model` **必填**，不继承。写错就是启动错误。
- `subagents` 引用的 Agent 必须存在，且不能成环 —— 两者都是**启动错误**。
- 深度超过 `max_depth` 是**启动错误**，不静默截断。

装配逻辑在 `agentkit/build/assemble.go`，校验逻辑在 `agentkit/spec/load.go`。

### 3.5 加一个厂商 / 模型

1. `llm/<vendor>.yaml` 里声明 provider。**协议层不硬编码任何厂商的字段名** ——
   `hint_keys` 只是为了在请求被拒时给出准确提示。

2. `reasoning` 档位表是**翻译层**：Agent 写 `reasoning: high`，
   档位表决定这个端点实际发什么字段。
   ★ **表里没有的档位会被静默忽略** —— Agent 写着 `high` 而端点没有 `high`，
   效果是「完全不思考」，且没有任何报错。新增端点时务必把档位表填全。

3. `reasoning_default` 是 Agent 没写 `reasoning` 时用的档位。
   留空则**什么都不注入**。

4. 采样参数的优先级（见 `llm/provider_test.go` 的断言）：
   **Agent 的显式参数 > `extra` > 厂商默认**。
   `extra` 是多层的，后面的层覆盖前面的层。

### 3.6 上下文压缩

长对话迟早会塞不进模型的窗口。openheron 会压缩它：对话被一段摘要取代，从那以后 agent 带着
摘要继续走。

**阈值**按 Agent 配，写在 `agents/<name>.md` 里：

```yaml
---
model: deepseek/deepseek-flash
summarize_at: 80%              # 模型 context_window 的 80%
---
```

三种写法：`80%`（窗口的份额）、绝对 token 数（`104858`）、`0`（或 `off`）表示永不。
不写这个键，Agent 拿到的是**窗口的 80%** —— 自动压缩默认开着。

★ **阈值属于 Agent，窗口属于模型。** 窗口说上下文有多大；阈值说这个 Agent 把窗口填到多少
才压缩自己的历史。同一个模型可以服务多个策略不同的 Agent。两者只在一处相遇：
`spec.SummarizeAt(threshold, window)`（`agentkit/spec/summarize.go`）—— builder 问那里，
而不是自己解析任何一边。

★ **百分比需要窗口。** 没有 `context_window` 就没什么可取 80%：那个 Agent 永不压缩，
spec 包在加载配置时会把这件事记进 `deferred`（`agentkit/spec/load.go` 的
`noticeCompactionWindows`）。绝对的 token 数不需要窗口，也就不会被记。

★ **模型档案里的旧字段照常解析，但不再被读。** `models[].summarize_at` 已废弃：留着它是为了
让老配置还能加载（字段是 `KnownFields` 严格的，直接删会让所有提到它的 `llm/*.yaml` 启动
即失败），spec 包会指路到 Agent 上的那个设置。

中间件挂在**每个声明了阈值的 Agent 上** —— 根 agent 的压缩才是进记录的那一份；子 agent 的
压缩活在自己的那一轮里，随轮而死，但在那一轮里护住了它自己的窗口。transport 只接**根
agent** 的压缩（`transport/transport.go`）：子 agent 的摘要不是会话历史的边界。

**整条链**，从头到尾：

| 环节 | 在哪 | 发生了什么 |
|---|---|---|
| 触发 | `agentkit/build/summarize.go` | eino 的摘要中间件在每次模型调用前给 agent 的状态数 token，超过阈值就压缩 |
| finalizer | 同文件的 `finalize` | 替换后的历史是 agent 的 system 消息**加上摘要** —— openheron 的上下文提示就是一条 system 消息（`agentkit/run/input.go`），丢掉它会让 agent 在这一轮余下的时间里没有自己的规则 |
| 宣告 | eino 的 `after_summarize` 事件 | 中间件把替换后的历史发到 agent 的事件流上 |
| 接住 | `transport/transport.go` 的 `compactionFrom` | transport 读这个事件，把压缩消息放进 `Result.Compaction` —— 取的是**摘要**，不是同处替换历史里的那条 system 消息 |
| 落盘 | `agentkit/run/loop.go:182` 的 `archiveCompaction` | 消息被追加到记录里，位置在**本轮消息之前** |
| 边界 | `message/compaction.go:103` | 标记搭在这条消息上，所以记录仍是唯一真相源 |

★ **写入顺序不是细节。** 压缩消息总结的是本轮消息之前的一切，所以必须先写。写在后面，摘要
就落在对话末尾 —— 那儿它什么也没总结。

**手动压缩**（`/compact`）是同一件事，只是触发者不同 —— 用户，而不是 token 数：

| 环节 | 在哪 | 做了什么 |
|---|---|---|
| 摘要器 | `agentkit/build/summarize.go` 的 `build.SummarizerFor` | 用该 agent 自己的模型、同一个 finalizer 建一个 eino 中间件，直接调它的 `Summarize` —— 触发线根本不走 |
| 落盘 | `agentkit/run/compact.go` 的 `run.Compact` | 摘要有效历史、盖标记、追加到记录里 —— 标记与估算都与 `archiveCompaction` 相同。摘要覆盖的**消息集合**也与自动压缩一致：子 agent 的消息被滤掉，因为它们不在根 agent 的上下文里 |
| 端点 | `api/v2_compact.go` 的 `POST /v2/sessions/{id}/compact` | 轮次在跑或审批在等时拒绝；返回这次压缩的战果 |

★ **手动那条路不看阈值。** 「关」说的是「别自己压」，不是「不许压」：手动命令是用户开口要的。
所以摘要器与 agent 的 `summarize_at` 无关。

★ **摘要器在 `run` 里是接口，不是 builder 的类型。** `run` 从不 import agent 包 ——
`AgentBuilder` 接口就是为这条规则存在的 —— 所以 `run.Compact` 收一个 `run.Summarizer`，
由 API 层从 builder 那里拿一个给它。

**会话的两个视图**，该问哪个：

```go
Messages(effective bool) []*schema.Message
```

- `false`（默认）—— 整份记录：审计线索，以及界面渲染的东西。
- `true` —— 有效历史：最后一条压缩消息及其后的部分。**agent 的上下文就是它**（`agentkit/run/loop.go`）。

★ 压缩**不是**删除。被取代的消息留在盘上、照样读得到；标记说明切口在哪儿。

**客户端拿这个标记做什么。** 消息视图把标记盖在消息上（`api/v2_convert.go:63`），历史水合把它读
出来（`web/src/lib/store/history.ts:14`），消息列表在气泡该在的位置上画一条分隔线
（`web/src/components/message-list.tsx:101`，由 `web/src/components/compaction-divider.tsx`
绘制）。**轮内**发生的压缩改走事件流通知：transport 发一个 `CUSTOM "context_compacted"` 事件
（`transport/transport.go:590`），reducer 当场把边界插进列表（`web/src/lib/events.ts:412`）。

★ 实时事件**不带数字**。数字 —— 顶替了多少条消息、摘要省下了多少 token —— 是拿记录量出来的，
而记录要等这一轮结束才写；transport 手里没有记录可量。所以实时分隔线写的是*「earlier messages ·
compacted into a summary」*，数字要等下一次加载 —— 那时标记跟着记录一起回来。

★ 摘要必须留住用户自己的话 —— eino 会把近期的用户消息拼回去，openheron 的 finalizer 不动
这件事。有测试钉住它。

★ Go 的接口没有可选参数，所以默认靠零值表达：`false` 就是「全部」，每个调用点都写明自己要
哪个视图。

### 3.7 上下文仪表盘

一个 agent 的上下文有多满，以及那个数字从哪来。

**那个数字**是该 agent **最近一次请求**的大小 —— 厂商自己的计数，不是会话的累加。
`transport.ContextUsageOf`（`transport/context.go`）顺着消息走一遍，留下每个 agent 最后一条
带用量的 assistant 消息。一个会话可以总量很大而上下文几乎为空；只有最近一次请求回答得了
这个问题。

**窗口**来自 builder：`Builder.Contexts`（`agentkit/build/context.go`）用与装配相同的方式解析
每个 agent 的模型，包括继承父 agent 模型的子 agent。API 层问 builder —— 它不该自己重推
某个 agent 跑在哪个模型上。

**两条通道，一个数字**：

| 通道 | 在哪 | 什么时候 |
|---|---|---|
| 实时路径 | transport 在每次模型调用后发 `CUSTOM "context_usage"` 事件 | 运行中 |
| 历史路径 | `GET /v2/sessions/{id}` 带 `usage.context` | 刷新后 |

两条都调 `transport.ContextUsageOf`，所以屏幕上的数字与刷新后的数字不会分叉。

★ **`usage.byAgent` 与 `usage.context` 是两个不同的量。** 前者是累计开销，后者是时点占用。
前端把它们分开，测试也守着标签不互换。

★ **窗口为零表示用户没配。** 占用照报，但不显示比例 —— 显示「0 %」是撒谎。

### 3.8 会话准入 —— 一个会话，一轮

**规则：一个会话至多有一轮活着的轮次。** 对忙着的会话发第二个 `POST /v2/run`
一律被拒（`409 session_busy`）；绝不接管正在跑的那一轮。

★ **闸门住在 `agentkit/run`，不在 API 层。** API 层不得重新实现一条关于运行时
状态的规则（§2.2）—— 运行时拥有那份状态，所以规则也归运行时。

| 函数 | 位置 | 拒绝什么 |
|---|---|---|
| `ensureIdle` | `agentkit/run/runtime.go` | 有轮次执行**或**有审批在等时的**新查询** |
| `ensureNoLiveQuery` | `agentkit/run/runtime.go` | 有轮次执行时的**新查询** —— 但放行**恢复** |
| `claimTurn` | `agentkit/run/runtime.go` | 本身不拒绝：它把「忙不忙」的判定**与**接管放在同一把锁下 |

**为什么恢复不是查询。** 恢复续接的是它所回答的那个被打断的轮次 —— 同一轮对话。
`contract.TurnKind` 就是这么写的，规则随之而来：`conflictTurns` 只拒绝「查询跟在
活轮次后面」这一种。

★ **拒绝必须不改变任何东西。** `Submit` 里的检查点丢弃与待审批清除都排在**闸门
之后**。改之前它们排在前面，于是一个被拒的请求删掉了正在跑那一轮的检查点与审批
—— 别人的轮次被那个本该被拒绝的请求毁了。

★ **占槽才是规则，检查只是话术。** `ensureIdle` 给一句好听的错误，但两个同时到达
的请求可能都通过它。`claimTurn` 把判定与接管放在一把锁下，所以恰好一个赢家。

#### 三处要紧的次序

每一处都先是 bug、后才成为规则：

| 次序 | 为什么 |
|---|---|
| `Complete` 在**归档落地之后**才返回 | 否则 HTTP 响应先走，客户端被告知「完了」而服务端仍说「忙」—— 而且下一轮会读到不完整的历史。 |
| 待审批在**检查点之后**才写 | 框架在 loop 停止时写检查点，而审批原先写在回调**里面**，也就是它**之前**。用户看到了卡片，回答它却得到 `no_checkpoint`。 |
| 闸门读 **loop**，不读轮次指针 | `st.turn` 在归档落地时才清，所以它滞后：已经答完的那一轮仍读作「在跑」。 |

★ **一个读着滞后指针的闸门就是个带洞的闸门。** 当关于同一个轮次的两个事实被读在
不同时刻，就让第二个**等**第一个，而不是去猜它。

#### 前端那一半

composer 必须读**服务端**的答案，而不是自己的内存：

- `GET /v2/sessions/{id}/messages` 带着 `state`；`seedFromHistory` 把它喂进
  `convo.status`（`web/src/lib/store/history.ts`）。少了它，第二个标签页水合出来
  就是空闲，发送按钮可用。
- `runStream` 的 `onError` 带上错误**码**（`web/src/lib/api.ts`）。散文分不出「别处
  正忙」与「失败」—— 码可以。
- `session_busy` 的拒绝要**回滚乐观的用户消息**：服务端根本没收到，记录里就不该
  声称有（`web/src/lib/store/actions.ts` 的 `markBusy`）。

★ **忙着的会话不是失败的会话。** 它拿的是 `convo.busy`，不是 `status: "error"` ——
那一轮没事，只是属于别人。composer 会显示停止按钮，用户据此接管。

## 4. 前端

### 4.1 三层纯函数

| 文件 | 职责 | 依赖 |
|---|---|---|
| `web/src/lib/sse.ts` | SSE 分帧：把字节流切成事件 | **无**（零 import） |
| `web/src/lib/events.ts` | AG-UI 事件解码 + reducer | 只有 `import type`（编译期擦除） |
| `web/src/lib/store.ts` | **barrel**，转出下面这些 | 见下 |
| `web/src/lib/store/state.ts` | 类型契约 | 只有 `import type` |
| `web/src/lib/store/selectors.ts` | 纯 selector + 模块级空常量 | 只有 `import type` |
| `web/src/lib/store/history.ts` | 从 REST 历史重建会话状态 | 只有 `import type` |
| `web/src/lib/store/events.ts` | store 内的事件路由 | **`localStorage`** |
| `web/src/lib/store/actions.ts` | zustand store 本体 + 全部异步 action | **zustand**、`fetch`、`localStorage` |

★ **要精确**：真正框架无关的是 `sse.ts`、`events.ts`、`store/state.ts`、
`store/selectors.ts`、`store/history.ts`。`store.ts` 本身**是 barrel**，
`store/actions.ts` 拥有 zustand 实例 —— 所以 import `store.ts` 就会把
zustand、`fetch`、`localStorage` 一起拉进来。

`store/events.ts` 也不是纯的：它写 `localStorage`（`active-thread.ts`）。
纯度到 `store/history.ts` 为止。

★ **新增逻辑请落在这三层里**，让它们保持纯函数 ——
正确性由 `bun test` 证明，比「改了界面得开浏览器看」快得多。

### 4.2 reducer 的形状

`events.ts` 的 `reduceEvent(state, event)` 是**纯函数**：旧状态进，新状态出。
`store/events.ts` 的 `handleEvent` 在它外面包了一层，处理「当前 run key」
（多 Agent 并行时要区分事件属于哪一轮）。

`closeAllOpenParts` 在流中断时收尾所有还开着的 part（文本、思考、工具调用）——
不收尾的话，界面会永远显示一个转圈的气泡。

### 4.3 ★ zustand selector 的引用稳定性

这是前端最容易踩的坑，表现是 `Maximum update depth exceeded` 白屏。

**规则：selector 必须返回引用稳定的值。**

```tsx
// ✗ 每次调用造新数组 → Object.is 永远判定「变了」→ 无限重渲染
const selectMessages = (s: State) => s.convo?.messages ?? []

// ✓ 模块级空常量
const EMPTY: Message[] = []
const selectMessages = (s: State) => s.convo?.messages ?? EMPTY
```

`web/src/lib/store/selectors.ts` 里就是这个模式：
`EMPTY_MESSAGES`（:16）、`EMPTY_INTERRUPTS`（:17）、`EMPTY_CONVERSATION`（:18）、
`EMPTY_ATTACHMENTS`（:37）。

★ **selector 里不要写 `?? []`** —— 造新数组会让快照永远「变了」。
返回数组的 selector 配 `useShallow`。

### 4.4 构建与嵌入

- `vite.config.ts`：`@` → `src`；dev 端口 `5173`（test 模式 `15173`），
  `strictPort: true`；`/v2` 代理到 `OPENHERON_API_TARGET`。
- `web/dist` 由 `go:embed` 打进二进制（`webui/webui.go`）。
- `make build` 会先构建前端再构建 Go。

## 5. 测试

### 5.1 端口与状态隔离

**端口错开不等于数据隔离。** openheron 的会话写在 `state/` 里，
测试写进去就污染真实历史。测试实例把 `.openheron-test/project` 当项目根并**预置自己的**
`.openheron/`，再用 `OPENHERON_CONFIG_DIR` 指名配置根 ——
配置根**不向上查找**，所以仓库根那份结构上就读不到。**端口与状态必须一起隔离。**

端口定义在仓库根的 `.env.test`（已提交）：

| | 开发 | 测试 |
|---|---|---|
| openheron 本体 | :8080 | **:18099** |
| Vite | :5173 | **:15173** |
| E2E 假后端 | — | **:18180** |

改端口只改这一个文件。

### 5.2 命令

| 命令 | 覆盖 | 前置 |
|---|---|---|
| `make test` | Go 全量（`go test ./...`） | — |
| `cd web && bun run test:unit` | 纯函数层（reducer / 分帧 / store / 高亮） | — |
| `make test-e2e` | 真实浏览器 + 确定性假后端 | 无（自带假后端） |
| `make smoke` | 真后端联调：SSE 分帧、历史回放、审批接线 | **测试实例在跑** |
| `make lint` | Go vet + gofmt + oxlint + tsc | — |
| `make check` | `lint` + `test`，提交前跑这个 | — |

起测试实例：

```bash
make test-env-backend   # 前台起，:18099，独立配置 + 独立会话目录
make test-env-reset     # 只重建测试目录，不启动
make test-env-sync      # ★ 改过开发侧的 agent 定义后必须跑
```

★ **`make test-env-sync` 是必须的，不是可选的** —— 测试实例里的 `agents/ llm/ policy/ skills/`
是从开发侧**复制**的，改完 agent 不同步，测的还是旧定义。

单个冒烟脚本（默认就连 :18099，**不需要手写 `OPENHERON_BASE`**）：

```bash
cd web && bun run smoke:interop            # SSE 分帧、拼接、历史水合
cd web && bun run smoke:no-undefined-url   # 不会发出拼错的 URL
cd web && bun run smoke:hitl               # 审批 resume 走通
```

### 5.3 Go 单元测试

`go test ./...` 覆盖 17 个包。契约主要由这几处锁定：

| 包 | 锁住的契约 |
|---|---|
| `agentkit/spec` | 配置根解析、frontmatter 严格解码、深度校验、环检测、写入闸门 |
| `agentkit/build` | 工具装配、策略档位、闸门、拓扑、嵌套恢复 |
| `agentkit/run` | 输入构造（子 Agent 消息不混入父历史）、事件桥接 |
| `api` | 路由、SSE 帧、错误信封、`threadId` 必填 |
| `kb` | 索引、评分、目录格式（含旧中文表头兼容） |
| `storage/filestore` | 追加写、原子重写、checkpoint、待审批持久化 |
| `transport` | 事件类型、归属、用量分账 |
| `initcfg` | 出厂模板的完整性 |

### 5.4 知识库语料约定（`docs/`）

`kb` 包的语料**就是本仓库的 `docs/`**（`kb/corpus_test.go` 的 `repoCorpusDir`，
可用 `OPENHERON_KB_CORPUS` 覆盖）。所以 `docs/` 下的 markdown 文档要满足抽取契约：

| 要求 | 规则 |
|---|---|
| **有标题** | 第 1 行是 `# Title` |
| **有摘要** | 三选一：frontmatter 里的 `description:` 字段 / 标题下的 TLDR 小节（`一句话` `tldr` `summary` `概要` `摘要`）/ **标题后紧跟的连续引用块** `> ...` |
| **小节行号有效** | 每个小节的 `Line` 必须落在文档行数范围内 |
| **小节不被截断** | 小节数不能超过硬上限（超了会被 `SectionsTruncated` 记下来，测试会挂） |

★ 本仓库的文档用的是**引用块开头**（与上游文档同款）：

```markdown
# 标题

> 一句话说清这份文档讲什么。
> 第二句可以续在同一引用块里。
```

普通段落开头**产不出摘要** —— `TestExtractAgainstRepoDocs` 会报
`no summary extracted; §6.1 promises every doc in this corpus yields one`。

★ 上游的召回记录（`TestRealCorpusRecallMatchesTheV1Record`）**按语料身份守卫**：
`kb-design.md` 在场 = 这是上游那份设计语料，记录必须命中；
不在场 = 换了一份语料，记录不适用，响亮跳过（不是静默跳过）。

★ 语料里同时躺着每份文档的**两个语言版本**。每一个都得各自满足抽取契约 ——
抽取器不会读一种语言来帮另一种。

### 5.5 前端单元测试

`cd web && bun run test:unit`（`bun test test/*.test.ts`）——
**14 个测试文件、271 个用例**，覆盖 `sse.ts` / `events.ts` / `store/` / `highlight.ts` /
`thinking.ts` / `turns.ts` / `approval.ts` / `settings-validate.ts` 等纯函数层。

★ `store.test.ts` 测的是**整个 zustand store**，所以它在跑之前会
stub 掉 `globalThis.localStorage` 和 `globalThis.fetch`（`store.test.ts:35-84`）。
Bun 本身没有 `localStorage`，不 stub 会炸。

★ 事件回放与历史水合**必须收敛到同一个状态** —— `store-parity.test.ts` 专门钉这一点，
外加一块「已知差异，钉住别动」的断言。

### 5.6 E2E

`make test-e2e` → `playwright test`，**8 个 spec、124 个用例**：
`chat` `multi-agent` `agents` `composer-meta` `composer-autocomplete`
`settings` `history` `scroll`。

Playwright 自己起两个服务（`playwright.config.ts` 的 `webServer`）：
假后端（`web/test/serve.ts`）+ Vite（`--mode test`）。

配置是**单 worker、不重试**（`workers: 1`、`retries: 0`、`fullyParallel: false`）——
E2E 共享一个假后端状态，并行会互相污染。

假后端（`web/test/fake-server.ts`，1300+ 行）靠**场景脚本**制造确定性：
19 个命名场景（`simple` `approval` `approvalMulti` `streaming` `multiAgentStream`
`error` `slowRun` …）用 `POST /__fake/scenario?name=…` 切换，
`POST /__fake/reset` 在每个用例前把线程、设置、请求日志全部复位。
线程 id、时间戳、marker 字符串全是写死的常量。

★ `slowRun` 场景**故意不关流** —— 它是拿来测「流卡住时界面怎么表现」的。

### 5.7 ★ 几条别踩的坑

**（一）E2E 用假后端，不要改成真后端。** 真后端依赖模型配合，
失败时分不清是界面坏了还是模型没听话；`retries` 一开，失败变成噪音，三轮后没人看。
与真实后端的协议层验证由 `make smoke` 负责，两者互补。

**（二）端口被占时让它响亮地失败。** `playwright.config.ts` 的
`reuseExistingServer: false` 与 Vite 的 `strictPort: true` 都是刻意的：
复用开发 vite 会让 E2E「全绿」但浏览器在跟真后端说话，`OPENHERON_API_TARGET`
根本没注入。**不要图省事改成 `!CI`**。

**（三）被强杀的 Playwright 会留下孤儿占端口。** 再跑时报 `already used`，
那不是配置坏了 —— 先清进程。
★ 别在 E2E **跑着的时候**清进程：一条 `pkill -x vite` 会把当轮正在用的 webServer 杀掉，
而 Playwright 还在等它的健康检查 —— 表现为「端口一直没起来、测试卡在启动」。要清就先把测试停掉。

**（四）`pkill` 要用 `-x`，不要用 `-f`。** `-f` 匹配完整命令行，
会把执行这条命令的 shell 自己也杀掉（它的命令行里就含这个字符串）。
★ `ps | grep | kill` 是同一种病。要按名字杀就用 `pgrep -x <name>`。
★ 进程名超过 15 个字符时 `comm` 会被内核截断，那时按端口找 PID（`ss -ltnp`）逐个 kill。

**（五）长任务放到后台任务里跑，不要用 shell 的 `&`。** 后台进程活不过那次工具调用，
表现为「跑了一会儿就没了」「端口始终没起来」—— 而日志里只有第一行，看不出是被谁杀的。

**（六）`smoke:hitl` 会抖，约 5 次里 1 次** —— 它依赖模型*选择*调 `write_file` 才能触发审批。
那是模型的自由，不是代码回归；脚本会重试，全都不触发时**明确报错**，不伪装成接线有问题。
★ 它有一个**前置条件**：**真正调工具的那个 agent**，其 `write_file` 必须落在 `ask` 档
（既没被全局 `deny`，也没被它自己的 `disabled_tools` 禁用）—— 工具够不着，
自然没有审批可触发。
★ 别只盯着 `main`：出厂配置里 `main` 确实写着
`disabled_tools: [write_file, edit_file, execute]`，但它只做编排，会把活**委派**给
`task`，而 `task` 的 `write_file` 在 `ask` 档 —— 审批照样触发。
2026-10-29 实测的事件流：`TOOL_CALL_START name=task`（子 agent 是以工具形态被调的），
随后 `TOOL_CALL_START name=write_file subagentRunId=task`，
中断的 `subagentRunId` 也是 `task`。
要确认某档是否可达，看**每个** agent 的生效工具表：`GET /v2/agents` 与
`GET /v2/agents/task` 会给出每个工具的 tier 与 reason。

**（七）改界面必须浏览器验证。** 「我改了，测试也绿了」不构成验收依据 ——
脚本级测试看不见渲染层。

## 6. 排障

| 症状 | 先查 |
|---|---|
| 启动报配置缺失 | 启动日志里的 `config root` 那行 —— 它打印了实际用的根和项目根 |
| 改配置不生效 | 该分区热重载吗？`server/`、`skills/` 要重启 |
| Agent 调不动某工具 | `GET /v2/agents/:agentId` 看生效工具表；再查是内层禁用还是外层 `deny` |
| `reasoning` 像是没用 | 模型档案里声明这个档位了吗？没声明 = 静默忽略 |
| 审批恢复不了 | checkpoint 还在吗？`/v2/sessions/:id` 看 `pendingApprovals` |
| 界面白屏 + `Maximum update depth exceeded` | 有 selector 返回了新数组/新对象吗？ |
| E2E 报 `already used` | 上轮被强杀的孤儿占着端口，清掉再跑 |
| 事件流不完整 | SSE sink 断了？断了要**继续消费**事件流，否则历史会缺 |

★ **归档优先于呈现** —— SSE sink 断裂后要停止渲染但继续消费事件流，
否则 `Outcome.Intermediates` 不完整，历史会缺。

## 7. 已知问题

写下来是为了下次有人撞上时不用重新查一遍。**这些是代码里的现状，不是待办清单** ——
要修的话另开一次改动。

| 现象 | 位置 | 说明 |
|---|---|---|
| **活跃流的用量没有按 Agent 分账** | `contract.Usage` 没有 `byAgent` 字段；前端 `events.ts:479` 的 `toUsage` 只映射扁平计数 | `usage.byAgent` **只在历史水合时才有**。所以新开的一轮里，Agent 过滤条上的累计 token 数是空的 —— 刷新一下（拉历史）才会出现。**上下文占用**是实时的（§3.7）：它走自己的 `context_usage` 事件。E2E 之所以没发现这个缺口，是因为它测的是**带种子的历史线程**。 |
| **SSE 断了不会自动重连** | `web/src/lib/api.ts` | 流断掉就是断掉：走 `onError` → 错误面板，用户得自己重发。没有重连、没有断点续传。 |
| **冲突对话框不显示磁盘上的版本** | `web/src/components/settings/conflict-dialog.tsx:18-23` | 它只显示你那个陈旧的 `baseRevision` 加一句写死的「changed by someone else」，**没有真去取磁盘版本**。也没有「强行覆盖」按钮 —— 「Keep editing」只是清掉冲突标记，下次保存会带着同样的陈旧 revision 再冲突一次。 |
| **`web/index.html` 声明 `lang="zh-CN"`** | `web/index.html:2` | 界面文案全是英文（AGENTS.md 的硬性要求），`lang` 声明却还是中文。会影响读屏软件的朗读语言。 |
| **`tw-shimmer` 是幽灵依赖** | `web/package.json:39` | 全仓库只有清单里这一处提到它，源码、样式、HTML 都没引用。 |
| **四个运行时依赖被放在 `devDependencies`** | `web/package.json` | `lucide-react`（10 个文件引）、`clsx`、`tailwind-merge`、`class-variance-authority` 都被 `src/` 引用并打进 `web/dist`。构建能过（Vite 在构建期解析），但清单是错的。 |
| **`web/scripts/interop-check.ts` 已过时** | 该脚本第 16-20、41-45 行 | 它直接 POST `/v2/run` 且**不带 `threadId`**，而服务端现在要求先 `POST /v2/sessions`（`api/v2_run.go:78`）。所以 `make smoke` 里的这一项会失败（实测报 400 `threadId is required`）。`web/scripts/hitl-smoke.ts` 与 `web/scripts/no-undefined-url-check.ts` 已按新契约写。 |
| **`api/server.go:75` 的注释指向不存在的路由** | 该行 | 注释写的是「GET /v2/spec」，但 `SetSkillsSource` 实际喂的是 `/v2/skills`。 |

## 8. 提交前

```bash
make check        # lint + test
```

- `gofmt -l .` 必须为空。
- 运行时输出（日志、错误、警告、界面文案、写入用户配置的文件内容）**一律英文**。
- 代码注释用英文，写「为什么」不写「是什么」，别把框架语义当契约。
- 测试用例名与测试夹具数据**保持中文**（那是描述性文本，不是运行时输出）。
- 新增/删除用例要同步文档里的条数。
