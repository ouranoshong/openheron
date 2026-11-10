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
   HTTP /v2/*  ────▶ │     api      │  routing, SSE sink, error envelope
                    └──────┬───────┘
              ┌────────────┼────────────┬───────────┐
              ▼            ▼            ▼           ▼
        agentkit/spec  agentkit/build  agentkit/run  transport
          config load   agent assembly  run loop     AG-UI events
              │            │            │           │
              ▼            ▼            ▼           ▼
         decl(decls)    llm/common     storage      webui
         kb(corpus)     approval       persist      │
         persist                       filestore    web/(React)
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
| | `agentkit/obs` | 无 | **日志装配处**：它**造** logger（`New`、`ParseLevel`、`Discard`）并持有 sink —— 没有全局 logger 可回取。它的子包 `agentkit/obs/hertz`（接管 hertz 的日志输出）、`agentkit/obs/hook`（eino 的 callback）与 `agentkit/obs/httplog`（模型请求日志）同样是叶子 |
| | `llm/common` | 无 | 厂商协议层（零 openheron 依赖，**零日志代码**） |
| | `webui` | 无 | 嵌入式前端资源 |
| | `agentkit/fsx` | 无 | **文件系统后端**：有界的本地后端 —— 写收敛在项目根、忽略规则、结果上限（见 [fs-backend-design.md](fs-backend-design.md)，只有中文） |
| | `agentkit/templates` | 无 | **配置模板**：`openheron init` 写下的文件、配置编辑器给出的脚手架。★ 它是叶子包，配置层与命令层才能读到**同一个**文件 —— 拷贝会漂移 |
| **L1** | `agentkit/spec/decl` | 无 | **纯声明**结构体（yaml tag 集中处）**以及两边都要读的那套词汇** —— 内置工具名与 MCP 命名空间（§2.1） |
| | `agentkit/emit` | `obs`, `approval`, `contract`, `helpers`, `message` | 推导层：agent 事件 → run 的事实 |
| | `storage` | `contract` | 会话/消息/待审批的接口 |
| | `kb` | `obs`, `persist` | 知识库：语料、索引、检索、评分 |
| | `agentkit/spec` | `obs`, `decl`, `persist`, `templates` | 配置装载、校验、热重载、写入闸门 |
| **L2** | `agentkit/build` | `obs`, `spec`, `decl`, `approval`, `contract`, `helpers`, `kb`, `llm/common`, `message` | Agent 装配、工具装配、档位表（`agentkit/build/policy.go`）与审批闸门 |
| | `agentkit/run` | `emit`, `obs`, `approval`, `contract`, `decl`, `message`, `storage` | 运行循环、输入构造、事件消费 |
| | `storage/filestore` | `obs`, `contract`, `message`, `persist`, `storage` | 文件存储实现 |
| | `storage/memarbit` | `storage` | 内存租约仲裁器 |
| | `transport` | `contract` | AG-UI 渲染：run 的事件 → 帧 |
| | `llm` | `build`, `spec`, `decl` | 薄封装（只有 `common/` 与一个测试文件） |
| | `initcfg` | `spec`, `persist` | `openheron init` 的出厂模板 |
| **L3 入口** | `api` | `build`, `obs`, `run`, `spec`, `decl`, `contract`, `message`, `storage`, `filestore`, `transport`, `webui` | HTTP 路由、SSE、错误信封 |

这张表只统计**内部**依赖 —— 第三方包不在表里。MCP 这条路径带进来两个第三方包：
`github.com/cloudwego/eino-ext/components/tool/mcp`（eino 适配器）与
`github.com/mark3labs/mcp-go`（协议客户端），两者都在 `agentkit/build` 里。
`agentkit/run` 引 `agentkit/spec/decl` 只为命名空间常量 —— run 账本要记下这一轮碰过
哪些 server。

### 2.1 两条不能破的依赖铁律

**（一）日志只有一处装配。** `agentkit/obs` **造** logger（`New`、`ParseLevel`、`Discard`）
并持有 sink；它不发放全局的那一个。组合根造出 `*slog.Logger` 再注入下去，
于是每个要记日志的包接收一个 `*slog.Logger` 形参，零值用 `obs.Discard()`。
级别（Info/Warn/Error）与前缀只在这一处定义，
而级别决定了一行会不会出现在默认输出里 —— 业务包直接调 `log.Printf` 或
`fmt.Fprintln(os.Stderr)` 就会绕开这个开关。

格式跟 hertz 一样，于是整份输出读起来是一股：日期、带微秒的时间、调用点的文件与行号、
级别，然后是消息与属性（`agentkit/obs/handler.go`）：

```
2026/10/29 22:28:34.346459 main.go:189: [Info] [main] openheron version=dev
```

`llm/common` 原来用标准 logger 打请求日志，那段代码搬到了
`agentkit/obs/httplog`（`docs/logging-design.md` §6）—— 这个叶子包现在一行日志代码都没有。
`llm/common` 里任何一处 import openheron 内部包都仍然算回归。

★ logger 现在由组合根（`main.installLogger`）围绕一个共享的 `slog.LevelVar` 建起来，
所以 `server/config.yaml` 的 `log_level` 与 `OPENHERON_LOG_LEVEL` 能在 handler 建好之后
移动门限。每个包接收一个 `*slog.Logger` 形参；没有全局可回取。

**（二）配置声明与配置行为分离。** `agentkit/spec/decl` 只放**纯结构体**和 yaml tag
（`agentkit/spec/decl/decl.go`），不引任何其它包；`agentkit/spec` 放**行为**
（装载、校验、重载、写入）。这样 `decl` 可以在 `build`、`api`、`initcfg` 之间自由传递，
不会拉出一条依赖链。

★ **两边共用的那套词汇也住在 `decl` 里，而这是依赖方向的事实，不是图方便。** 内置工具名
（`decl.BuiltinToolNames`，`agentkit/spec/decl/decl.go:347`）被**两端**读：装载器拿它核对
Agent 的 `tools:` 列表里点到的名字，装配器拿它拒绝冒用这些名字的 provider
（`reservedToolNames`，`agentkit/build/tools.go:75`）。两端互相不能引 —— 装载器在装配器
下层 —— 所以这份清单不能住在其中任何一边：它住在**两边之下**，那个叶子包里。★ 抄一份
就是两份会漂移的清单，而漂移读起来就是「那个工具不存在」。

### 2.2 `agentkit/spec` 为什么**不**引 `kb`

知识库配置（`tools/kb.yaml`）的**校验**需要知道语料路径是不是相对路径、模型有没有被声明、
以及它是不是声明为 `kind: embedding`（`agentkit/spec/load.go:285` 的 `loadKB`、
`agentkit/spec/load.go:934` 的 `validateKB`）。

★ 这三个答案都在**声明**里（`decl.Model.IsEmbedding`、`decl.Model.Dimensions`），
  所以配置层根本不引知识库 —— 只有测试文件引（`agentkit/spec/policy_contract_test.go:7`）。
  于是依赖方向仍然单向，而 `kb` 并不参与其中：`kb` 依赖 `obs` 和 `persist`，
  `agentkit/spec` 依赖 `obs`、`decl` 与 `persist`。

### 2.3 `agentkit/build` 为什么引 `llm/common` 而不是 `llm`

`llm` 包会引 `build`（它是对 `build.Resolve` 的薄封装）。若 `build` 反过来引 `llm`，
就成了环。`build` 直接用 `llm/common` 的协议类型，既避免了环，也让协议层保持叶子。

### 2.4 组合根装配 logger

logger 只在 `main` 里造一次，然后一路传下去；没有哪个包回头去取
（`main.newLogger`，`main.go:192`）：

```
main(): newLogger() -> installLogger() -> hlog.SetLogger(hertzlog.New(log))
      -> callbacks.AppendGlobalHandlers(hook.New(log))
      -> setupConfig(log, level, sink) -> applyLogLevel / applyLogFile
      -> serve(ctx, st, log)
```

★ **顺序本身就是设计。** `newLogger` 围绕一个共享的 `slog.LevelVar` 与一个 `obs.Sink`
把 logger 建起来，**在**配置读出来**之前** —— 等级与文件路径由配置说了算，而 logger
必须在 `setupConfig` 能打第一行日志之前就存在。所以门限与去处都在 handler 建好**之后**
才动：`applyLogLevel` 移门限（`OPENHERON_LOG_LEVEL` > `log_level` > `info`），
`applyLogFile` 把 sink 改向文件。

★ **两次框架交接，一次都不能少。** hertz 持有进程级 logger 注册表，eino 持有进程级
callback 注册表，所以 `hlog.SetLogger` 与 `callbacks.AppendGlobalHandlers` 各做一次，
都在组合根里，且都在 `hserver.New` 之前。这是 openheron 唯一的两处全局注册，其余全是注入。

★ **动的是去处，不是 logger。** `obs.Sink` 是 handler 背后的 `io.Writer`，所以 `log.file`
能在启动后把它改向文件（`docs/logging-design.md` §10）。hertz 与 eino 已经持有那个 logger，
而进程级注册表改不掉 —— 所以变的是 sink。

## 3. 配置系统

### 3.1 配置根只有三级

```
--config-dir  →  $OPENHERON_CONFIG_DIR  →  <project-root>/.openheron
```

实现在 `agentkit/spec/config.go` 的 `NewService`。**不向上查找祖先目录** ——
配置根一旦依赖「进程从哪启动」，同一条命令换个目录就读到另一份配置，
测试实例也会因此读到开发配置。

只有**默认派生**的根会被自动创建（`agentkit/spec/config.go:277` 的 `os.MkdirAll`），
显式 root / env root 指向不存在的位置时**不创建**：打错字不该静默长出一棵空配置树。

`spec.NewServiceAt` 是另一条入口，供 `openheron init` 用：它**只认显式传入的路径**，
不读环境变量 —— 否则 init 会把新配置写到用户没指的地方。

### 3.2 目录分区

| 目录 | 类别常量 | 热重载 | 内容 |
|---|---|---|---|
| `agents/` | `spec.DirAgent` | ✅ | 每个 Agent 一个 `.md` |
| `llm/` | `spec.DirLLM` | ✅ | 每个厂商一个 `.yaml` |
| `tools/` | `spec.DirTools` | ✅ | `http.yaml` / `web_search.yaml` / `wikipedia.yaml` / `kb.yaml` |
| `mcps/` | `spec.DirMCP` | ✅ | 每个 MCP server 一个 `.yaml` |
| `server/` | `spec.DirServer` | ❌ | 进程参数，需重启 |
| `state/` | `spec.DirState` | — | 会话与 checkpoint，自动创建 |
| `skills/` | — | ❌ | **独立解析**（见 §3.6） |

★ **没有 `policy/` 这个分区。** 五个分区是 `llm/` `agents/` `server/` `tools/` `mcps/`
（`agentkit/spec/config.go:40`），而旧配置遗留下来的 `policy/` 目录**不会被读** ——
没人加载它。它当年承载的档位模型如今住进了工具文件与 Agent 文件
（§3.4）。★ 配置**照样能起来**：其余部分可能完全没问题，而别处一个坏文件会让整份配置
根本加载不了。但这个目录会留下一条**提示**（`agentkit/spec/config.go:533` `noteRetiredDirs`），并说清新家在哪儿 —— 被丢掉却一声不吭的配置是最糟的一种：
进程起来得好好的，而那个设置已经没了。

`tools/` 下的文件名是**白名单**：`agentkit/spec/load.go:213` 对未知文件直接报错，
不会静默忽略 —— 「我写了 kb.yaml 但它没生效」是最难查的一类问题。

★ `mcps/` 分区**一个 server 一个文件**（`agentkit/spec/config.go:25` 的 `spec.DirMCP`）：
文件在，定义就在 —— 没有 `enabled` 键 —— 而且文件名就是 server 的名字，也就是这个 server
工具的命名空间那一段。

### 3.3 Agent 定义

一个 Agent 是一个 markdown 文件：**frontmatter + 正文**。frontmatter 是**严格解码**的
（`agentkit/spec/load.go:76` 的 `loadAgent`）—— 写错一个键就是启动错误。

★ **模型没人声明不是启动错误 —— 它把那个 Agent 留在树外。** 规则本身写在
`docs/config-design.md` §4.6，架构这一层要说的是**边界落在哪儿**：`validate()` 会**settle**
这棵树（`agentkit/spec/load.go:565` 的 `leaveOutUndeclaredModels`）—— 那个 Agent 从
`Config.Agents` 里被删掉，每个父 Agent 的 `subagents:` 里也被删掉，理由留在
`Config.leftOut` 里（`agentkit/spec/config.go:139`）。下游的一切 —— `AgentNames()`、
`MCPServersInUse()`、档位表、装配层 —— 看到的都是 settle 之后的树，于是永远碰不到一个
它跑不起来的 Agent。★ 动这段代码之前要知道两件事：默认 Agent 也一样被留在树外（结果是
改好之前没有会话能跑，但进程照常起来，编辑器就在进程里），以及写入门
（`agentkit/spec/write.go:472` 的 `checkNoAgentLost`）比的是**settle 后的 before** 与
**settle 后的 after** —— 于是可以加一个 Agent、可以放着已经坏的不动，不能把一个能跑的拿走。

frontmatter 可用键（`agentkit/spec/decl/decl.go:78` 的 `decl.Agent`）：

| 键 | yaml tag | 含义 |
|---|---|---|
| `description` | `description` | 这个 Agent 是干什么的（给父 Agent 看） |
| `model` | `model` | `<provider>/<model>`，**不继承、不回退** |
| `max_iterations` | `max_iterations` | 工具循环轮数上限 |
| `subagents` | `subagents` | 可委派的子 Agent 列表 |
| `tools` | `tools` | 这个 Agent 可用的**非 MCP 工具** —— **暴露出来**的名字，模型看见的那些。★ **不写表示一个都没有**（`agentkit/spec/decl/decl.go:111`） |
| `skills` | `skills` | 这个 Agent 可加载的技能 —— `skills/` 下的文件夹。★ **不写表示一个都没有**（`agentkit/spec/decl/decl.go:132`） |
| `mcps` | `mcps` | 本 Agent **挂载**的 MCP server —— 每一个都会起一个子进程，而挂载一个就是**把它的全部工具交给这个 Agent**。★ **不写表示一个都不挂**（`agentkit/spec/decl/decl.go:148`） |
| `temperature` | `temperature` | 采样温度 |
| `max_tokens` | `max_tokens` | 采样上限 |
| `reasoning` | `reasoning` | 思考档位名（`low`/`high`/`max`…） |

`decl.Agent` 里还有三个 `yaml:"-"` 字段（`ID`、`File`、`Instruction`）——
它们由装载器填充，**不是 frontmatter 键**。

正文就是提示词本身（`Instruction`）。

★ **三张清单默认都是一个都没有，而「不写」等于「写空」。** 规则就一行：**列出来的才有**。
没有「全都要」，也没有「除了这些都要」—— 清单没点名的工具就不是这个 Agent 的工具，
没点名的技能就不是它的技能，没点名的 server 就不会被挂载。★ 这三张清单是普通切片
（`decl.Agent.Tools`、`decl.Agent.Skills`、`decl.Agent.MCPServers`），不是指针：指针
跟着这次改动一起没了，因为「没声明」与「声明了但是空的」现在是一回事 —— 什么都没有。

★ **旧键是被拒绝，不是被改名。** `disabled_tools` 与 `tools` 的含义**正好相反** ——
一个删工具，另一个点名工具 —— 所以只把键改个名，就等于把黑名单悄悄变成了白名单。
于是装载器**拒绝**这个文件（`agentkit/spec/load.go:99`），并说清两者的方向是反的。
★ **没有别名**：一个仍按旧义工作的别名会让黑名单继续活着，一个按新义工作的别名则等于
替用户把黑名单翻成白名单 —— 那正是这次拒绝要防的误会。

★ **MCP 只有一个键，而挂载就是授予。** `mcps:` 说哪些 server 被**挂载** ——
挂载一个 server 就是起一个子进程 —— 它同时也是说这个 Agent **拿到了它的全部工具**。
★ `tools:` 不点名 MCP 工具：写进去的 `mcp__<server>__*` 不授予任何东西，会被丢弃，
并以一条**提示**报出（`agentkit/spec/load.go:836` 的 `checkAgentMCPNames`）——
不是装载期错误，因为那是语义变了，不是自相矛盾。
★ **代价是真实的**：再也不能只收窄某一个 Agent 的 MCP 工具。server 自己的文件
（`mcps/<server>.yaml`）过滤它自己的工具，而那个过滤器是所有挂载它的 Agent 共享的。
★ **审批不受影响**：MCP 工具的档位来自那个 server 文件的 `allows:`，缺省仍然是 `ask` ——
挂载不等于放行。

### 3.4 档位模型：默认 `ask`，例外被点名

档位只有**两个**，而默认是安全的那一个：

| 档位 | 含义 |
|---|---|
| `allow` | 工具直接跑，不需要人点头 |
| `ask` | 工具打断这一轮，等一个人的决定 |

`build.Policy.TierOf`（`agentkit/build/policy.go:55`）回答一个工具名的档位：名字在
allow 表里就是 `allow`，**其余一律是 `ask`**。镜像的声明 `decl.Tier`
（`agentkit/spec/decl/decl.go:240`）只有 `TierAllow` 与 `TierAsk`，再没有第三个档位。

★ **默认是 `ask`，因为猜错的代价是不对称的**：多点一次，换来的是一次没人看见的破坏性
调用不被执行。旧模型 —— 一份全局清单，没被列名的工具即放行 —— 恰好是相反的默认值，
而它错在了会伤人的那一侧。

**例外是被点名的**，而且由提供这个工具的文件来点：

| 来源 | 它点名什么 | 装配位置 |
|---|---|---|
| 没有自己文件的内置工具 | `ls` `read_file` `glob` `grep` `task` `write_todos` `skill`，外加五个只读的 config 工具 | `agentkit/build/tools.go:79` `builtinAllowedTools` |
| 工具自己的文件 —— `tools/*.yaml` 与知识库 | 那个文件的 `allows:` | `agentkit/spec/load.go:326` `AllowedTools` |
| MCP server 自己的文件 | 那个 server 的 `allows:`，用它自己的词汇，翻译成暴露出来的名字 | `agentkit/build/mcptools.go:173` `mcpAllowedNames` |

`agentkit/build/spec.go:64` 的 `policyOf` 把三处合成一张表，`Policy.TierOf` 读这张表。
★ 这张表由**配置**装配，所以一个工具的档位在它的 server 起来之前就能回答。

★ **有一个名字是在合并之后被摘掉的，不是一开始就不在里面。** `config_write` 改的是配置
本身，包括那个 `allows:` 列表会授予它的文件 —— 一个能授予它的文件授的是自己。
`alwaysAsk`（`agentkit/build/tools.go:93`）在合并之后把它摘掉，于是例外来自三处，而能拿走
一个例外的只有一处。

★ **两个问题，而且分开摆着。** 「这个工具要不要人点头？」是**档位表** ——
`Policy.TierOf` —— 它是**共享的**：每个 Agent 读同一张表。「这个 Agent 到底能不能用这个
工具？」是**这个 Agent 自己的清单** —— `Table.Blocked`
（`agentkit/build/policy.go:130`），也就是 Agent 以 `decl.SubAgentSpec` 带在身上的那份
清单 —— 它是**逐 Agent** 的：清单没点名的工具不在这个 Agent 的工具集里，模型也就看不见。

★ **被挡住的工具保留自己的档位**，并在 `reason` 里说清原因 —— 而现在原因有**两个**：
非 MCP 工具是 `tools`，MCP 工具是它的 server **没挂载**（`LayerNotMounted`）
（`agentkit/build/introspect.go:36` 的 `reasonOf`）。★ 旧的那个原因跟着它命名的那一层
一起没了：没挂载的 server 本就没有工具可挡，而挂载了就把**全部**工具给了。所以
`Table.Blocked` 回答 `LayerNotLoaded`（`agentkit/build/policy.go:251`）或者
`LayerNotMounted`（`agentkit/build/policy.go:258`）。

### 3.5 深度上限与拓扑校验

委派深度的上限是 `server/config.yaml` 里的 `agent.max_depth`（默认 `1`），
`retry.max_retries` 就在它旁边。

★ **上限只住在 `server/config.yaml` 里。** `policy/limits.yaml` 那一半 —— 同样键名的第二份
拷贝，只能*收紧*文件里的值 —— 没有了，而 `server/` **不是**热分区
（`agentkit/spec/config.go:52`），所以改上限要重启才生效。

声明的拓扑超过上限是**启动错误**（`agentkit/spec/load.go:962` 的 `validateDepth`），
不静默截断。子 agent 引用不存在的 agent、拓扑成环，也都是启动错误
（`agentkit/spec/load.go:1033` 的 `validateAgentGraph`、`agentkit/spec/load.go:1050` 的 `detectCycle`）。

### 3.6 skills 的解析是另一套

`skills/` **不是**「配置根下的一个分区」，而是独立解析的
（`agentkit/build/skill.go:190` 的 `ResolveSkillsDir`）：先看 `$EINO_EXT_SKILLS_DIR`，
没有才从当前目录**向上**找 `.openheron/skills`。

★ **编辑器也得问这个问题，而且不许自己猜。** 它写的是 `<配置根>/skills/`，而那个目录只有在
探针正好落在那里时才是生效的那一个；不是的话，保存会落进一棵没人读的树。所以解析结果在启动时
被推进配置层（`spec.Service.SetSkillsDir`，`agentkit/spec/config.go:284`），指向别处时保存被拒
（`agentkit/spec/write.go:151` 的 `validateWritable`），清单里那个分区标成只读并点名真正的目录。
★ 这条规则只住在 `build` 里：在 `spec` 里再写一份，就是「到底加载了哪些技能」的第二个答案。

★ 这跟「配置根不向上查找」是两条不同的规则。skills 是目录树，
向上找是为了能复用用户家目录里的技能；配置根是实体集合，向上找会让同一命令读到不同配置。

★ **一个 agent 能用哪些技能，是一个被限定的后端。** `agents/<name>.md` 的 `skills` 键是
skills 目录下文件夹的白名单，它以 `scopedSkillBackend`（`agentkit/build/skill.go:76`）的形式
到达中间件。这个限定同时管住两半：`List`（`agentkit/build/skill.go:147`）—— 技能工具的
说明就是从它渲染出来的，也就是模型**看得见**的；`Get`
（`agentkit/build/skill.go:161`）—— 模型真正**调得动**的。只管一半的话，一个模型只要记得
名字，照样能调到自己看不见的技能。

★ **这个列表是普通切片，默认一个都没有。** `decl.Agent.Skills`
（`agentkit/spec/decl/decl.go:125`）是 `[]string`，不是指针：「没声明」与「声明了但是空的」
现在是一回事 —— 一个技能都没有 —— 也就没什么可区分的了。★ 清单没点名的技能就不是这个
Agent 的技能；而技能工具只有在确实有技能可加载时才值得点名 —— 两者互相矛盾时装载器会
说出来（§3.3）。

★ 技能中间件挂在**入口 agent** —— 会话选中的那个（`agentkit/build/assemble.go`）。子 agent
根本拿不到技能工具，所以在那儿这个列表没有可过滤的对象。

### 3.7 装载的两种模式与「deferred」

`spec.Service.Load()`（`agentkit/spec/config.go:336`）严格校验，失败即启动失败。
`LoadUnchecked()`（`agentkit/spec/config.go:359`）跳过校验。

`Config.Deferred()`（`config.go:124`）收集**非致命**的问题（比如某个 duration 字符串
没法解析），以 `deferred` 数组出现在 `/v2/health` 与 `/v2/config` 里。
致命与非致命的分界是：**会导致运行时行为错乱的是致命的**，只是提示的是 deferred。

### 3.8 写入闸门

配置编辑器（`/v2/settings`）的写路径有多道闸门：

- `server/` **不可编辑**（`api/v2_settings.go`）—— 它不热重载，改了要重启，
  接口直接拒绝。
- 密钥字面量被拒：`api_key must be a reference, not a literal: use ${ENV_VAR} or @file:/path`
  —— 防止密钥进了仓库。
- 乐观并发：带 `baseRevision`，与磁盘上的 revision 不符就报冲突。
- 一次改动不许把一个**本来能跑的 Agent** 带出树外（`agentkit/spec/write.go:472` `checkNoAgentLost`）：拿磁盘上 settle 后的树与试装后 settle 的树比，写、批写或删除
  里凡是会把一个本来能跑的 Agent 弄丢的都拒绝。★ 本来就在树外的 Agent 两边都不在，
  所以一次无关的编辑永远不会被算成「把它弄丢」。
- **还有东西指着它**的删除被拒（`agentkit/spec/write.go:708` `checkNotReferenced`），
  而且拒绝点名是谁在指：是哪个文件还在指着被删的那个、要移除什么。★ 这道闸门跑在
  试装之前，因为试装的装载错误正是它要取代的症状 —— 那个错误点名被删掉的文件，
  把真正的原因藏在别处。

## 4. Agent 装配

`agentkit/build` 把「声明」变成「可运行的 Agent」。

### 4.1 装配管线

`agentkit/build/assemble.go` 的 `assemble`：

1. 取 agent 的 `model` 引用，用 `build.Resolve`（`provider.go:58`）解出
   `(provider, model)`；解不出就是启动错误。
2. 算出**生效工具表**：snapshot 的工具集是**整个 snapshot 共用的一套**，每个 Agent 用自己
   的 `tools:` 白名单去收窄它（`agentkit/build/topology.go:72` 的 `specTable`）。档位表
   （§3.4）再决定活下来的工具里哪些要问人。★ **这里没有「没事可做」的捷径**
   （`agentkit/build/policy.go:145`）：既然是白名单，**空**清单就意味着**全部**移除 ——
   黑名单可以有的那个捷径（「没有规则就全都留着」）恰恰是白名单不能有的那种情况。
3. 按 `subagents` 递归装配子树，把 `SubAgents` 填进 eino ADK 的 `deep.Config`
   （`assemble.go:97`）。
4. `WithoutGeneralSubAgent: true`（`assemble.go:25` 的 `baseConfig`，
   以及 `hub.go:162`）——
   **关掉 eino 自带的「通用子 Agent」**，因为那会让模型能凭空造子 agent，
   绕过我们自己的拓扑与深度校验。

★ `assemble.go:157-158` 还有一处容易看漏：`base.SubAgents = nil` 且
`base.Name/Description/Instruction` 被清空 —— 那是**逐节点**装配时剥掉继承来的
子 agent 列表，好让每个节点挂自己的子树，而不是把父的子 agent 复制一遍。

### 4.2 工具闸门

`agentkit/build/gate.go` 实现「工具能不能用」：

- 名字在 allow 表里 → 工具直接跑，闸门原样放行（`agentkit/build/gate.go:67`）。
- 名字不在 → 调用**中断**，等一个人的决定（流式那条路是 `agentkit/build/gate.go:99`，
  它是同一道闸门）。
- 这个 Agent 的 `tools:` 清单没点名的工具**压根不在它的工具表里** —— 表先把它摘掉，
  闸门连看都看不到，模型自然也看不见。

★ **没有 `deny` 这一支。** 一个 Agent 拿不到的工具不会注册给它，闸门因此从来不需要
拒绝谁：「这个 Agent 不能用它」是关于这个 Agent 的工具表的事实，不是关于这个工具的事实。

人拒绝一次调用时，工具返回的是**一句拒绝说明**（带可选理由），**不是**一个错误 ——
模型看到的是「被拒了，理由是 X」，于是可以据此调整。

★ MCP 工具走的是**同一道**闸门：它们和其它工具一样**按名字**查
（`agentkit/build/policy.go:55` 的 `TierOf`），所以 `mcp__<server>__<tool>` 同样是要人
点头或直接放行。闸门没变 —— 变的只是命名空间。

### 4.3 工具来源

| 来源 | 装配位置 | 例子 |
|---|---|---|
| 文件系统中间件 | `agentkit/build/snapshot.go:56` | `ls` `read_file` `write_file` `edit_file` `execute` |
| 网络工具 | `build/nettools.go` | `web_search` `http` |
| 知识库 | `build/kb.go` | `kb_search` `kb_index` |
| 百科 | `build/nettools.go:236` | `wikipedia` |
| 配置本身 | `agentkit/build/configtools.go:62` | `config_get` `config_validate` `config_template` `config_notices` `config_tier` `config_write` |
| MCP server | `agentkit/build/mcptools.go:194` | `mcp__<server>__<tool>` |

★ **config 工具总是被挂上，但几乎从不可见。** 它们进每个 Agent 的批次，一个 Agent 只看见
自己 `tools:` 清单点名的那些 —— 与其它非 MCP 工具一样。其中五个只读，在代码的例外清单里；
`config_write` 不在里面，且文件加不进去（§6.3）。

★ **一个工具的档位由提供它的那个文件决定。** 文件自己的 `allows:` 点名那些不用问的
工具 —— `tools/http.yaml` 点 `request_get`，`tools/kb.yaml` 点 `kb_search` ——
而**这个文件提供的其余一切都问**。没有自己文件的内置工具由代码点名
（`agentkit/build/tools.go:62`）：读取类（`ls` `read_file` `glob` `grep`）与委派类
（`task` `write_todos` `skill`）。MCP server 用**它自己的词汇**说这件事 —— server
自己的工具名，不是暴露出来的那些。

对外的网络工具**不用改代码**：配置缺失的 provider 什么都不挂，所以写下这个文件才是把工具
挂上去的那一步（`tools/*.yaml`）。★ 文件系统中间件是
例外 —— `fsx.New` 无条件挂载它（`agentkit/build/snapshot.go:56`），知识库则是由它的
**corpus** 开关，而不是由文件开关。
★ 注意 `openheron init` 会写 `tools/` 下的工具配置 —— 那一整个目录是出厂树的一部分，
因为出厂的 agent 会点名 `request_get`、`duckduckgo_search` 和 `wikipedia_search`，而没有
provider 提供的名字会被当成笔误报出来。`--no-tools` 可以把它留下不写。

工具的来路都一样：

`tools/*.yaml` → `spec.loadTool`（`agentkit/spec/load.go:213`）→ `decl.ToolsCfg` →
`toolRegistry.Build`（`agentkit/build/tools.go:90`）→ `snapshot.buildHeavy`
（`agentkit/build/snapshot.go:47`）→ `applyToolConfig`（`agentkit/build/assemble.go:31`）→
`deep.Config.ToolsConfig.Tools` → `deep.New`（`agentkit/build/assemble.go:40`）。

MCP 这一支在注册表处并进来：`mcps/<server>.yaml` → `spec.loadMCP`
（`agentkit/spec/load.go:329`）→ `decl.MCPServerCfg` → MCP provider
（`agentkit/build/mcptools.go:194`），注册在 `agentkit/build/tools.go:42` → 同一个
`toolRegistry.Build` → 同一个 `applyToolConfig`。

★ **MCP 客户端是这条路径上唯一不属于 snapshot 的资源。** 重依赖只构建一次，
在 `sync.Once` 之下（`agentkit/build/snapshot.go:41`），而 provider 只回答一个问题 ——
「这个 snapshot 有哪些工具」，然后交回**一批**工具。那批工具背后的客户端活得比这批工具久：
它由 manager 拥有（`agentkit/build/mcpmanager.go:42`），因为 stdio 的 server 是一个
**子进程**，为一轮启动的子进程下一轮还得在。provider 是**可选**的 —— 连不上的 server
只跳过并告警，绝不报错，所以一个死掉的 server 不能把 agent 拖下水。

★ **一个不答话的 server 也不能把 agent 拖下水。** 启动失败之后的拆解要等子进程退出，
而一个还在装自己依赖的子进程不会因为 stdin 关了就退出 —— 所以先杀掉子进程，拆解也有
上限（`agentkit/build/mcpmanager.go:485`、`agentkit/build/mcpmanager.go:501`）。
装配等的是一个 server，不是一个 server 的尸体。

★ **启动不站在那一轮的关键路径上。** 配置一被读到，所有挂载的 server 就在**后台**
起起来（`agentkit/build/mcpmanager.go:129` `Warm`），而每台 server 各自有一个状态 ——
`not_started`、`starting`、`ready` 或 `failed`。还没起来的 server 不贡献工具，
那一轮照跑；它起来之后工具面变了，代际推进，于是**下一轮**就有它的工具。

★ 暴露出来的名字是 `mcp__<server>__<tool>`，只在一处拼装（`agentkit/build/mcptools.go:141`）。
server 自己的名字就是命名空间那一段，而一个暴露名属于哪台 server 是从它**读回来**的
（`agentkit/spec/decl/decl.go:498` 的 `MCPServerOf`）。★ **命名空间不可协商**：server 名里
不许出现连续的下划线，所以一个 server 的前缀伸不到另一个 server 去。

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
| `RUN_STARTED` | `transport.go:61` | 本轮开始，带 `threadId` |
| `TEXT_MESSAGE_START/CONTENT/END` | `transport.go:246-263` | 助手正文增量 |
| `REASONING_*` | `transport.go:269-280` | 思考过程 |
| `TOOL_CALL_START/ARGS/END/RESULT` | `transport.go:283-314` | 工具调用 |
| `CUSTOM`（`model_retry`） | `transport.go:232` | 模型重试提示 |
| `RUN_FINISHED` | `transport.go:69`, `:75` | 本轮结束，带结果与用量 |
| `RUN_ERROR` | `transport.go:240`, `api/v2_run.go:251` | 失败 —— ★ 码说明是哪一类，消息说明为什么（见 `docs/run-lifecycle-design.md` §8.3） |

子 Agent 的产出**通过事件上的 `subagentRunId` 归属**，而不是另开事件类型 ——
这让前端能用同一套 reducer 处理所有 Agent 的输出。

### 5.2 审批恢复

审批请求被持久化（`storage` 的 `PendingApproval`），**checkpoint store** 是恢复的真相源。
客户端把决定塞回同一个 `POST /v2/run` 的 `resume[]` 里（每项带 `interruptId` 与
`payload.approved` / `payload.reason`）。

checkpoint 没了就恢复不了 —— 这时接口返回
`this approval can no longer be resumed: its checkpoint is gone. Send a new message to continue.`
（`api/v2_types.go:131`），而不是静默新开一轮。

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

### 5.4 ★ 附件挂在消息上，不是挂在会话上

用户发来的文件绑在**它随的那条消息**上 —— 清单存在消息的 `Extra` 里
（`message/attachment.go`），而模型看得见它，是因为 transcript 存的那条消息
**就是**装配输入时用的那个对象。

★ **会话级清单答不了那个真问题。** 「这个会话有哪些文件」与「你刚发的那张图」
是两个问题，而用户问的是第二个。

几块拼图：

- `POST /v2/run` 带 `attachments[]` —— 一个名字和一个**相对会话 workspace 的
  路径**。服务端自己解析（`api/v2_attachments.go`），并拒绝越界的名字；
  越界是**拒绝**而不是夹取，因为夹取会解析到另一个文件。
- 清单以 **JSON 字节**盖在那条用户消息上 —— `Extra` 在写 checkpoint 时走 gob
  编码，而 gob 编不了 interface 里没注册过的类型。
- 装配（`agentkit/run/attachments.go`）按类型分流：**文本**文件在模型副本的
  消息头变成一行 `@`，写的是**绝对**路径，模型用 `read_file` 去读；**图片**变成
  多模态 part，模型是**看见**它。★ 二进制文件，以及内联不了的图片，会在提示词里
  被列为「暂时不能处理」—— 它们不拿路径，因为对非文本字节用 `read_file` 只会
  读出噪声。
- ★ **消息头是视图，不是改写。** transcript 保留用户自己的话，以及 `Extra` 里
  那个相对路径；历史消息也会重建消息头，所以隔一轮再问「刚才那个文件」仍然
  叫得出名字。
- ★ **有图要内联时，给模型的是一条副本。** transcript 里那条留着正文，而且
  **永远不带 base64**（transcript 是 8 MiB 行上限的 JSONL 日志）；副本的正文
  挪进一个 **text part** —— 线上只有一个 `content` 字段，它要么是字符串要么是
  列表，而 openai 适配器在一条消息同时带 `Content` 与多模态 part 时会把两个
  都填上。
- ★ **一次请求，没有降级**（`llm/common/model.go`）。模型没有视觉时，这一轮带着
  provider 的错误结束，由用户重试；`llm/common/files.go` 与
  `llm/common/vision.go` 已经删掉。★ 上下文提示词把 `read_file` 写成义务，
  文件大时要求用 `offset`、`limit` 分段读。
- `GET /v2/sessions/{id}/messages` 把清单放在消息的 metadata 里带回来，于是历史
  里看得出哪个文件跟哪个问题一起走的。

★ **删除的粒度是文件，绑定的粒度是消息。** `DELETE
/v2/sessions/{id}/files?path=` 把文件从 workspace 里拿走；已经随消息发出去的
文件留在那条消息上 —— 那条消息说了这个文件是问题的一部分。

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
- Agent 视图（`api/v2_introspect.go:118`）把 Agent 的工具白名单报成 **`tools`** ——
  报的是那份清单本身，不是它移除掉的工具。★ `skillsRestricted` 没了：它存在只是因为
  「不写」与「写空」曾经不同，现在一样了。

`POST /v2/run` 的 `threadId` **必填**（`api/v2_run.go:113`）——
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

★ 对话的**宽度**是一个 token,不是同一个数字抄六遍：`--chat-column`（那一列）、
`--prose-column`（里面的文字）与 `--chat-gutter`（两侧留白）住在
`web/src/index.css`。列要宽,宽表才看得全；正文停得更早 —— 六十个字符长的一段话是难读的。

★ **主题**在首屏之前就定了,由 `web/index.html` 里的一段脚本决定,并且只从一处读：
`web/src/lib/theme.ts`。浅色与暗色是同一套 token 的两组取值,一边有的颜色另一边也有。

★ **动效只有 CSS,而且听系统的。** 时长与缓动是 `web/src/index.css` 里的 token
（`--motion-duration-*`），一条全局的 `prefers-reduced-motion` 规则把所有动画 ——
包括 `tw-animate-css` 自带的那几处 —— 压到 0.01ms：要求少动效的用户拿到的是同一个
界面，只是没有那趟位移。方案在 `docs/animation-design.md`。

★ **品牌标记是生成的，不是画了两遍。** `web/public/favicon.svg` 是源，
`web/src/components/brand-mark.generated.tsx` 由 `web/scripts/generate-brand-mark.ts`
写出来，两者一漂移单测就失败。标记是内联 SVG —— 装在 `img` 里的 SVG 是另一个文档，
页面的样式表选不进去它的部件。

## 9. 知识库

`kb` 是一个**可选**的本地语料检索层。

- 语料是一批 markdown 文档，目录由 `tools/kb.yaml` 的 `corpus_dir` 指定（相对项目根）。
- 索引落在 `<data-dir>/kb/<index>/`（`build/kb.go:42`），含 `catalog.md` 目录页。
- **检索是混合打分**：`kb/score.go:53` 的 `scoreDoc` 在有向量时算
  `0.5*词法分 + 0.5*余弦相似度`，无向量时**纯词法**（所以 `kb status` 会报
  `lexical-only retrieval`）。
- 词法分给**标识符加权**：出现在标题/小节标题/链接里的词比出现在正文里的词值钱。
- `openheron kb index` 有**运行锁**（`kb/store.go:146` 的 `StartRun`）：同时只能跑一轮。
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
