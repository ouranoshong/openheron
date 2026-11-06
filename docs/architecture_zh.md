# 架构设计说明

> openheron 是一个多 Agent 运行时：每个专家 Agent 绑定自己的模型与工具边界，父 Agent 只做
> 编排与合成。本文说明**代码实际是怎么组织的**，以及**为什么这样分层**。
>
> 与 [user-manual.md](user-manual.md)（面向使用者）、
> [developer-manual.md](developer-manual.md)（面向改代码的人）配套。
> 文中的每个结论都指向具体文件与行号。

> English version: [architecture.md](architecture.md)

## 1. 一句话概括

一个 HTTP 服务进程（`api`）加载一份**文件化配置**（`agentkit/spec`），把每个 Agent 装配成
一棵**有界的子 Agent 树**（`agentkit/build`），交给 eino ADK 的**运行循环**（`agentkit/run`）
执行，过程以 **AG-UI 事件流**（`transport`）推给浏览器（`web/`）；状态落在**文件存储**
（`storage/filestore`）里，审批靠 **checkpoint**（`approval` + `storage`）恢复。

```
                    ┌──────────────┐
   HTTP /v2/*  ────▶ │     api      │  路由、SSE 出口、错误信封
                    └──────┬───────┘
              ┌────────────┼────────────┬───────────┐
              ▼            ▼            ▼           ▼
        agentkit/spec  agentkit/build  agentkit/run  transport
         配置装载        Agent 装配       运行循环     AG-UI 事件
              │            │            │           │
              ▼            ▼            ▼           ▼
         decl(声明)     llm/common    storage      webui
         kb(语料)      approval       persist(原子写)  │
         persist                     filestore      web/(React)
```

## 2. 分层与依赖方向

依赖是**单向**的：上层可以引下层，下层**绝不**引上层。下面这张表是实际 import 图
（用 `grep '"github.com/ouranoshong/openheron/'` 对每个包统计得出）：

| 层 | 包 | 内部依赖 | 职责 |
|---|---|---|---|
| **L0 叶子** | `contract` | 无 | 跨层数据契约（事件、消息、中断） |
| | `message` | 无 | 消息内容与来源（provenance） |
| | `helpers` | 无 | 无状态小工具 |
| | `persist` | 无 | 原子写文件 |
| | `approval` | 无 | 审批决定的表示与校验 |
| | `agentkit/obs` | 无 | **日志出口**（唯一的 `Logger()`） |
| | `llm/common` | 无 | 厂商协议层（零 openheron 依赖） |
| | `webui` | 无 | 嵌入式前端资源 |
| | `agentkit/fsx` | 无 | **文件系统后端**：有界的本地后端 —— 写收敛在项目根、忽略规则、结果上限（见 [fs-backend-design_zh.md](fs-backend-design_zh.md)） |
| **L1** | `agentkit/spec/decl` | 无 | **纯声明**结构体（yaml tag 集中处） |
| | `agentkit/emit` | `obs`, `approval`, `contract`, `helpers`, `message` | 推导层：agent 事件 → run 的事实 |
| | `storage` | `contract` | 会话/消息/待审批的接口 |
| | `kb` | `obs`, `persist` | 知识库：语料、索引、检索、评分 |
| | `agentkit/spec` | `obs`, `decl`, `persist` | 配置装载、校验、热重载、写入闸门 |
| **L2** | `agentkit/build` | `obs`, `spec`, `decl`, `approval`, `contract`, `helpers`, `kb`, `llm/common`, `message` | Agent 装配、工具装配、策略闸门 |
| | `agentkit/run` | `emit`, `obs`, `approval`, `contract`, `message`, `storage` | 运行循环、输入构造、事件消费 |
| | `storage/filestore` | `obs`, `contract`, `message`, `persist`, `storage` | 文件存储实现 |
| | `storage/memarbit` | `storage` | 内存租约仲裁器 |
| | `transport` | `contract` | AG-UI 渲染：run 的事件 → 帧 |
| | `llm` | `build`, `spec`, `decl` | 薄封装（只有 `common/` 与一个测试文件） |
| | `initcfg` | `spec`, `persist` | `openheron init` 的出厂模板 |
| **L3 入口** | `api` | `build`, `obs`, `run`, `spec`, `decl`, `contract`, `message`, `storage`, `filestore`, `transport`, `webui` | HTTP 路由、SSE、错误信封 |

### 2.1 两条不能破的依赖铁律

**（一）日志只有一个出口。** 所有运行时日志走 `agentkit/obs` 的 `Logger()`
（`agentkit/obs/logger.go:18`）。级别（Info/Warn/Error）与前缀只在这一处定义，
而级别决定了一行会不会出现在默认输出里 —— 业务包直接调 `log.Printf` 或
`fmt.Fprintln(os.Stderr)` 就会绕开这个开关。

格式跟 hertz 一样，于是整份输出读起来是一股：日期、带微秒的时间、调用点的文件与行号、
级别，然后是消息与属性（`agentkit/obs/handler.go`）：

```
2026/10/29 22:28:34.346459 main.go:189: [Info] [main] openheron version=dev
```

**唯一的例外**是 `llm/common`：它是零 openheron 依赖的叶子包，
引 `obs` 就破了「协议层不依赖运行时」这条铁律，所以它用标准 logger。
`llm/common` 里任何一处 import openheron 内部包都算回归。

**（二）配置声明与配置行为分离。** `agentkit/spec/decl` 只放**纯结构体**和 yaml tag
（`agentkit/spec/decl/decl.go`），不引任何其它包；`agentkit/spec` 放**行为**
（装载、校验、重载、写入）。这样 `decl` 可以在 `build`、`api`、`initcfg` 之间自由传递，
不会拉出一条依赖链。

### 2.2 `agentkit/spec` 为什么**不**引 `kb`

知识库配置（`tools/kb.yaml`）的**校验**需要知道语料路径是不是相对路径、模型有没有被声明、
以及它是不是声明为 `kind: embedding`（`agentkit/spec/load.go:171` 的 `loadKB`、
`load.go:331` 的 `validateKB`）。

★ 这三个答案都在**声明**里（`decl.Model.IsEmbedding`、`decl.Model.Dimensions`），
  所以配置层根本不引知识库 —— 只有测试文件引（`agentkit/spec/policy_contract_test.go:7`）。
  于是依赖方向仍然单向，而 `kb` 并不参与其中：`kb` 依赖 `obs` 和 `persist`，
  `agentkit/spec` 依赖 `obs`、`decl` 与 `persist`。

### 2.3 `agentkit/build` 为什么引 `llm/common` 而不是 `llm`

`llm` 包会引 `build`（它是对 `build.Resolve` 的薄封装）。若 `build` 反过来引 `llm`，
就成了环。`build` 直接用 `llm/common` 的协议类型，既避免了环，也让协议层保持叶子。

## 3. 配置系统

### 3.1 配置根只有三级

```
--config-dir  →  $OPENHERON_CONFIG_DIR  →  <project-root>/.openheron
```

实现在 `agentkit/spec/config.go` 的 `NewService`。**不向上查找祖先目录** ——
配置根一旦依赖「进程从哪启动」，同一条命令换个目录就读到另一份配置，
测试实例也会因此读到开发配置。

只有**默认派生**的根会被自动创建（`config.go:186` 的 `os.MkdirAll`），
显式 root / env root 指向不存在的位置时**不创建**：打错字不该静默长出一棵空配置树。

`spec.NewServiceAt` 是另一条入口，供 `openheron init` 用：它**只认显式传入的路径**，
不读环境变量 —— 否则 init 会把新配置写到用户没指的地方。

### 3.2 目录分区

| 目录 | 类别常量 | 热重载 | 内容 |
|---|---|---|---|
| `agents/` | `spec.DirAgent` | ✅ | 每个 Agent 一个 `.md` |
| `llm/` | `spec.DirLLM` | ✅ | 每个厂商一个 `.yaml` |
| `policy/` | `spec.DirPolicy` | ✅ | `permissions.yaml` + `limits.yaml` |
| `tools/` | `spec.DirTools` | ✅ | `http.yaml` / `web_search.yaml` / `wikipedia.yaml` / `kb.yaml` |
| `server/` | `spec.DirServer` | ❌ | 进程参数，需重启 |
| `state/` | `spec.DirState` | — | 会话与 checkpoint，自动创建 |
| `skills/` | — | ❌ | **独立解析**（见 §3.6） |

`tools/` 下的文件名是**白名单**：`agentkit/spec/load.go:130` 对未知文件直接报错，
不会静默忽略 —— 「我写了 kb.yaml 但它没生效」是最难查的一类问题。

### 3.3 Agent 定义

一个 Agent 是一个 markdown 文件：**frontmatter + 正文**。frontmatter 是**严格解码**的
（`agentkit/spec/load.go:65` 的 `loadAgent`）—— 写错一个键就是启动错误。

frontmatter 可用键（`agentkit/spec/decl/decl.go:67` 的 `decl.Agent`）：

| 键 | yaml tag | 含义 |
|---|---|---|
| `description` | `description` | 这个 Agent 是干什么的（给父 Agent 看） |
| `model` | `model` | `<provider>/<model>`，**不继承、不回退** |
| `max_iterations` | `max_iterations` | 工具循环轮数上限 |
| `subagents` | `subagents` | 可委派的子 Agent 列表 |
| `disabled_tools` | `disabled_tools` | 对本 Agent 生效的工具禁用表 |
| `temperature` | `temperature` | 采样温度 |
| `max_tokens` | `max_tokens` | 采样上限 |
| `reasoning` | `reasoning` | 思考档位名（`low`/`high`/`max`…） |

`decl.Agent` 里还有三个 `yaml:"-"` 字段（`ID`、`File`、`Instruction`）——
它们由装载器填充，**不是 frontmatter 键**。

正文就是提示词本身（`Instruction`）。

### 3.4 权限：两层，永不合并

| 层 | 位置 | 作用域 |
|---|---|---|
| 外层 | `policy/permissions.yaml` 的 `deny` | 所有 Agent |
| 内层 | `agents/*.md` 的 `disabled_tools` | 只影响这一个 Agent |

两层**分开存储、从不合并** —— 合并就丢掉了「是哪一层拦的」这个信息。
外层 `deny` 优先，是子 Agent 唯一放松不了的保证。

档位只有两列（`decl.TierOf`，`decl/decl.go:122`）：`deny` → `ask` → 否则 `allow`。
**未被点名的工具默认 `allow`（直接放行）**。要让某个工具每次都要人点头，
把它加进 `ask`。

### 3.5 深度上限与拓扑校验

`server/config.yaml` 的 `agent.max_depth`（默认 `1`）与 `policy/limits.yaml` 的同名项
**取更严的**（`spec/load.go:357` 的 `globalMaxDepth`）。

声明的拓扑超过上限是**启动错误**（`spec/load.go:293` 的 `validateDepth`），
不静默截断。子 agent 引用不存在的 agent、拓扑成环，也都是启动错误
（`load.go:396`、`load.go:404`）。

### 3.6 skills 的解析是另一套

`skills/` **不是**「配置根下的一个分区」，而是独立解析的
（`agentkit/build/skill.go:41`）：先看 `$EINO_EXT_SKILLS_DIR`，
没有才从当前目录**向上**找 `.openheron/skills`。

★ 这跟「配置根不向上查找」是两条不同的规则。skills 是目录树，
向上找是为了能复用用户家目录里的技能；配置根是实体集合，向上找会让同一命令读到不同配置。

★ **一个 agent 能用哪些技能，是一个被限定的后端。** `agents/<name>.md` 的 `skills` 键是
skills 目录下文件夹的白名单，它以 `scopedSkillBackend`（`agentkit/build/skill.go`）的形式
到达中间件。这个限定同时管住两半：`List` —— 技能工具的说明就是从它渲染出来的，也就是模型
**看得见**的；`Get` —— 模型真正**调得动**的。只管一半的话，一个模型只要记得名字，照样能调
到自己看不见的技能。

★ 这个列表用**指针**带着走（`decl.Agent.Skills`），因为「没声明」和「声明了但是空的」是两件
事：前者是全部技能，后者是一个都没有。

★ 技能中间件挂在**入口 agent** —— 会话选中的那个（`agentkit/build/assemble.go`）。子 agent
根本拿不到技能工具，所以在那儿这个列表没有可过滤的对象。

### 3.7 装载的两种模式与「deferred」

`spec.Service.Load()`（`config.go:245`）严格校验，失败即启动失败。
`LoadUnchecked()`（`config.go:268`）跳过校验。

`Config.Deferred()`（`config.go:114`）收集**非致命**的问题（比如某个 duration 字符串
没法解析），以 `deferred` 数组出现在 `/v2/health` 与 `/v2/config` 里。
致命与非致命的分界是：**会导致运行时行为错乱的是致命的**，只是提示的是 deferred。

### 3.8 写入闸门

配置编辑器（`/v2/settings`）的写路径有多道闸门：

- `server/` **不可编辑**（`api/v2_settings.go`）—— 它不热重载，改了要重启，
  接口直接拒绝。
- 密钥字面量被拒：`api_key must be a reference, not a literal: use ${ENV_VAR} or @file:/path`
  —— 防止密钥进了仓库。
- 乐观并发：带 `baseRevision`，与磁盘上的 revision 不符就报冲突。

## 4. Agent 装配

`agentkit/build` 把「声明」变成「可运行的 Agent」。

### 4.1 装配管线

`agentkit/build/assemble.go` 的 `assemble`：

1. 取 agent 的 `model` 引用，用 `build.Resolve`（`provider.go:58`）解出
   `(provider, model)`；解不出就是启动错误。
2. 按 `disabled_tools` + 外层 policy 算出**生效工具表**。
3. 按 `subagents` 递归装配子树，把 `SubAgents` 填进 eino ADK 的 `deep.Config`
   （`assemble.go:97`）。
4. `WithoutGeneralSubAgent: true`（`assemble.go:25` 的 `baseConfig`，
   以及 `hub.go:162`）——
   **关掉 eino 自带的「通用子 Agent」**，因为那会让模型能凭空造子 agent，
   绕过我们自己的拓扑与深度校验。

★ `assemble.go:145-148` 还有一处容易看漏：`base.SubAgents = nil` 且
`base.Name/Description/Instruction` 被清空 —— 那是**逐节点**装配时剥掉继承来的
子 agent 列表，好让每个节点挂自己的子树，而不是把父的子 agent 复制一遍。

### 4.2 工具闸门

`agentkit/build/gate.go` 实现「工具能不能用」：

- 外层 `deny` → 工具根本**不注册**给这个 agent，模型看不见它。
- 外层 `ask` → 工具注册，但调用前**中断**，等人工决定。
- 内层 `disabled_tools` → 对该 agent 同样不注册。

被拒绝时，工具返回的是**一句拒绝说明**（带可选理由），**不是**一个错误 ——
模型看到的是「被拒了，理由是 X」，于是可以据此调整。

### 4.3 工具来源

| 来源 | 装配位置 | 例子 |
|---|---|---|
| 文件系统中间件 | `build/tools.go:40` | `ls` `read_file` `write_file` `edit_file` `execute` |
| 网络工具 | `build/nettools.go` | `web_search` `http` |
| 知识库 | `build/kb.go` | `kb_search` `kb_index` |
| 百科 | `build/nettools.go:236` | `wikipedia` |

外部工具是**文件存在即启用**（`tools/*.yaml`），不需要改代码。
★ 注意 `openheron init` **默认不写** `tools/` 下的任何文件 —— 那一整个目录要用户自己写；
文件存在即启用，所以开一个工具是一个决定，不是一个副作用。`--tools` 会写那四个配置文件。

## 5. 运行循环

`agentkit/run` 是 eino ADK 的一层封装，负责三件事：

1. **构造输入**（`agentkit/run/input.go:44`）：把系统提示词 + 会话历史 + 本轮 query
   组装成 ADK 输入。子 Agent 的消息**不混进父的历史**（`input.go:64` 的
   `withoutSubagentMessages`）—— 这是「隔离」在数据层面的落点。
2. **驱动循环**（`agentkit/run/loop.go`）：跑 ADK 的 run，把事件转成 AG-UI 事件。
3. **桥接审批**（`agentkit/run/bridge.go`）：工具请求审批时挂起，
   审批结果回来后从 checkpoint 恢复。

### 5.1 事件流

事件类型来自 `ag-ui-protocol/ag-ui` 的 Go SDK。帧由 `transport/transport.go`
从 run 的事件渲染出来——推导（agent 的事件**意味着什么**）在
`agentkit/emit`：

| 事件 | 发射点 | 含义 |
|---|---|---|
| `RUN_STARTED` | `transport.go:54` | 本轮开始，带 `threadId` |
| `TEXT_MESSAGE_START/CONTENT/END` | `transport.go:239-256` | 助手正文增量 |
| `REASONING_*` | `transport.go:262-273` | 思考过程 |
| `TOOL_CALL_START/ARGS/END/RESULT` | `transport.go:276-306` | 工具调用 |
| `CUSTOM`（`model_retry`） | `transport.go:220` | 模型重试提示 |
| `RUN_FINISHED` | `transport.go:62`, `:68` | 本轮结束，带结果与用量 |
| `RUN_ERROR` | `transport.go:233` | 失败 |

子 Agent 的产出**通过事件上的 `subagentRunId` 归属**，而不是另开事件类型 ——
这让前端能用同一套 reducer 处理所有 Agent 的输出。

### 5.2 审批恢复

审批请求被持久化（`storage` 的 `PendingApproval`），**checkpoint store** 是恢复的真相源。
客户端把决定塞回同一个 `POST /v2/run` 的 `resume[]` 里（每项带 `interruptId` 与
`payload.approved` / `payload.reason`）。

checkpoint 没了就恢复不了 —— 这时接口返回
`this approval can no longer be resumed: its checkpoint is gone. Send a new message to continue.`
（`api/v2_types.go:121`），而不是静默新开一轮。

### 5.3 会话准入

**一个会话至多有一轮活着的轮次。** 对忙着的会话发第二个 `POST /v2/run` 一律被
拒（`409 session_busy`）—— 绝不接管正在跑的那一轮。

这条规则住在 `agentkit/run`，就住在它所守护的状态旁边：

- `ensureIdle`（`agentkit/run/runtime.go`）在有轮次执行**或**有审批在等时，拒绝
  **新查询**。
- `ensureNoLiveQuery` 同样拒绝新查询，但放行**恢复**：恢复续接的是它所回答的那个
  被打断的轮次 —— 同一轮对话，不是新的一轮。
- `claimTurn` 把「忙不忙」的判定与「接管」放在**同一把锁**下，所以两个同时到达
  的请求不可能都通过。

拒绝**不改变任何东西**：不删检查点、不清审批、不落消息。正在跑的那一轮毫发无损。

`Complete` 只在这一轮的消息落盘之后才返回（`agentkit/run/turn.go`）。这让客户端
的「你这轮完了」与服务端的「这个会话空了」不可能各说各话 —— 也让下一轮的输入绝不
会读到不完整的历史。

待审批是在 **loop 退出时**写的，不是在那一轮的回调返回时：框架正是在 loop 退出时
写检查点，而一张审批只有在它要恢复的那个检查点存在时才回答得了。

前端读服务端的答案，而不是自己的内存：会话的 `state` 来自服务端对 **run
账本**的扫描，并喂给 composer 的守卫 —— 于是第二个标签页水合出来就是「忙」，
而不是「空闲」。

run 账本是一次 run 的持久记录：每次 run 一行，由 run 自己的 goroutine 写
（`agentkit/run/runrow.go`），由 `GET /v2/sessions/{id}/runs` 读回来。run 的
**终态行落在它的消息落盘之后**——单是这个次序，就让 busy 标记不用超时也天然
诚实。被崩溃留下的、还开着的 run 会在启动巡检时被收成 `interrupted`，于是会话
不会永远读起来是忙。

## 6. 存储

`storage` 定义接口（`SessionManager` / `Session`），`storage/filestore` 提供实现。

磁盘布局（`storage/filestore/session.go`）：

- `state.json` —— 会话状态（原子重写）
- transcript —— 消息追加（`O_APPEND`）
- checkpoint —— 审批恢复点
- `runs.jsonl` —— run 账本：每次 run 一行，追加写入（`O_APPEND`），保留最后 200 行

**内存只是缓存**，磁盘才是真相源。会话列表（`List()`）只读 `state.json`，
不打开 transcript —— 所以列会话是廉价的。

## 7. HTTP 层

`api` 只暴露 `/v2` 接口族（旧的无前缀接口已下线，实测 `/health` 返回 404）。

- 路由注册集中在 `api/v2_files.go:34-65`。
- 错误信封统一由 `api/v2_types.go` 的 `v2Fail` 产出：
  `{"error": "...", "code": "...", "retryable": false}`。
- **`/v2` 路由先注册，catch-all `GET /*` 后注册** —— SPA 回退不会吞掉接口。
- **没有认证、没有 CORS。** 默认 `:8080` 绑全部网卡，要收窄就显式配 `listen_addr`。

`POST /v2/run` 的 `threadId` **必填**（`api/v2_run.go:78`）——
先 `POST /v2/sessions` 拿到会话，再发起 run。

## 8. 前端

React 19 + Vite + zustand，构建产物 `web/dist` 通过 `go:embed` 打进二进制
（`webui/webui.go`）。

`frontend_mode` 默认 `embed`（用嵌进二进制的界面）；`dir` 则从 `static_dir`
（相对项目根）读磁盘上的构建产物。

界面分三层。`lib/sse.ts`（SSE 分帧）与 `lib/events.ts`（事件解码 + reducer）
**不依赖任何框架** —— 前者零 import，后者只有 `import type`；
`lib/store/` 下的 `state.ts` / `selectors.ts` / `history.ts` 同样是纯的。
`lib/store.ts` 是 barrel，转出的 `store/actions.ts` 拥有 zustand 实例，
所以 import 它会一并拉进 zustand、`fetch` 与 `localStorage`。

★ 新增逻辑落在这几层里，让它们保持纯函数 ——
正确性由 `bun test` 证明，比「改了界面得开浏览器看」快得多。

★ zustand 的 selector 必须返回**引用稳定**的值：每次调用造新数组/新对象会让
`Object.is` 永远判定「变了」，表现为 `Maximum update depth exceeded` 白屏。
selector 里不要写 `?? []`；用模块级常量或 store 里的 selector。

## 9. 知识库

`kb` 是一个**可选**的本地语料检索层。

- 语料是一批 markdown 文档，目录由 `tools/kb.yaml` 的 `corpus` 指定（相对项目根）。
- 索引落在 `<data-dir>/kb/<index>/`（`build/kb.go:42`），含 `catalog.md` 目录页。
- **检索是混合打分**：`kb/score.go:53` 的 `scoreDoc` 在有向量时算
  `0.5*词法分 + 0.5*余弦相似度`，无向量时**纯词法**（所以 `kb status` 会报
  `lexical-only retrieval`）。
- 词法分给**标识符加权**：出现在标题/小节标题/链接里的词比出现在正文里的词值钱。
- `openheron kb index` 有**运行锁**（`kb/store.go:100` 的 `StartRun`）：同时只能跑一轮。
- agent 侧拿到两个工具：`kb_search`（返回文档**大纲**而非全文）与 `kb_index`。
  `kb_index` 默认落在 `ask` 档 —— 让 agent 自己重建索引需要人点头。

## 10. 可观测性

`agentkit/obs` 是唯一日志出口。级别与前缀只在这里定义。
`agentkit/obs/hook` 提供观测钩子（依赖 `obs`，不反向依赖）。

## 11. 边界与不做的事

写清楚比事后解释便宜。以下是**当前没有**的：

- **没有认证、没有多租户。** 默认 `:8080` 对全部网卡开放。
- **没有 TLS。** 要在公网暴露得自己套反向代理。
- **没有分布式。** 单进程、单二进制；会话在本地磁盘。
- **知识库只支持本地目录语料**，不支持远程抓取。
- **旧的无前缀 HTTP 接口已下线**，只有 `/v2`。
- **`skills/` 仍保留向上探测**，与配置根的规则不同（见 §3.6）。
