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
│   ├── run/           # 运行循环：输入构造、ADK 驱动、事件消费
│   ├── emit/          # 推导层：agent 事件 → run 的事实
│   └── obs/           # 日志出口（唯一）
├── approval/          # 审批决定的表示与校验
├── benchmarks/        # 成本基准：强模型父 + 弱模型子 vs 强模型单干
├── contract/          # 跨层数据契约
├── helpers/           # 无状态小工具
├── initcfg/           # init 的出厂模板
├── kb/                # 知识库：语料、索引、检索、评分
├── llm/common/        # 厂商协议层（零 openheron 依赖）
├── message/           # 消息内容与来源
├── persist/           # 原子写文件
├── storage/
│   ├── filestore/     # 文件存储实现
│   └── memarbit/      # 内存租约仲裁器
├── transport/         # AG-UI 渲染：run 的事件 → 帧
├── webui/             # 嵌入式前端资源
└── web/               # React 前端
```

### 2.1 依赖方向

**单向**：上层可以引下层，下层绝不引上层。实测的内部 import 图：

| 层 | 包 | 内部依赖 |
|---|---|---|
| **L0** | `contract` `message` `helpers` `persist` `approval` `agentkit/obs` `agentkit/fsx` `llm/common` `webui` | 无 |
| **L1** | `agentkit/spec/decl` `storage` `kb` `agentkit/spec` `agentkit/emit` | `decl` 无；`storage`→`contract`；`kb`→`obs`,`persist`；`spec`→`obs`,`decl`,`templates`,`persist`；`emit`→`obs`,`approval`,`contract`,`helpers`,`message` |
| **L2** | `agentkit/build` `agentkit/run` `storage/filestore` `storage/memarbit` `transport` `llm` `initcfg` | 见 architecture.md §2 |
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

★ **两边共用的那套词汇也住在 `decl` 里，而这是依赖方向的事实，不是图方便。** 内置工具名
（`decl.BuiltinToolNames`，`agentkit/spec/decl/decl.go:291`）被**两端**读：装载器拿它核对
Agent 的 `tools:` 列表里点到的名字，装配器拿它拒绝冒用这些名字的 provider
（`reservedToolNames`，`agentkit/build/tools.go:75`）。两端互相不能引 —— 装载器在装配器
下层 —— 所以这份清单不能住在其中任何一边：它住在**两边之下**，那个叶子包里。★ 抄一份
就是两份会漂移的清单，而漂移读起来就是「那个工具不存在」。

### 2.2 别在 API 层复制业务装配逻辑

需要读「某个 agent 的生效工具表」时，由 `agent.Builder` 暴露只读查询，
**不要**在 API 层重算一遍 `Table.Blocked` / `Policy.TierOf`。
理由很直接：重算就会漂移 —— 档位表改一次，API 的答案和运行时的答案就对不上了。

★ **这两个回答的是不同的问题，而 API 层必须把它们分开。**
`Policy.TierOf` 回答「这个工具要不要人点头？」，**每个 agent 都一样**；
`Table.Blocked` 回答「这个 agent 到底能不能用它？」，**逐 agent 而定**。
重算其中任何一个，都是同一个错误犯两遍。

## 3. 开发配方

### 3.1 加一个新工具

以 `web_search` 为例，`agentkit/build/nettools.go` 是完整范本。

1. **实现 provider**。工具 provider 是 `agentkit/build/tools.go:20` 的
   `toolProvider` 接口：

   ```go
   type toolProvider interface {
       // ID returns the provider name, used in logs and error messages.
       ID() string
       // Optional decides whether a failure means "skip" or "the whole service cannot start".
       Optional() bool
       // Provide returns the batch of tools this provider supplies.
       Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error)
   }
   ```

   注意 `Provide` 返回的是**一批**工具 —— 一个 provider 可以提供多个。

2. **注册**。在 `agentkit/build/tools.go:36` 的 `defaultToolRegistry` 里加上你的
   provider。位置决定顺序，也决定「可选工具」与「必需工具」的区分。

3. **可选性由 `Optional()` 决定**，不是由返回 error 决定。`Optional() == true` 的
   provider 失败时会打一条 Warn 然后跳过（`tools.go:101`）——
   缺 API key 不该让整个服务起不来。
   ★ 但注意这条 Warn 之外**没有别的提示**：工具静默不进表，用户在界面上只会发现
   「这个功能好像没有」。新增可选 provider 时要想清楚这条 Warn 够不够。

4. **闸门自动生效**。`agentkit/build/gate.go` 会用 agent 自己的 `tools:` 白名单过滤
   工具表 —— 那份清单说的是这个 agent 到底能用哪些工具 —— 并且把**凡是档位表没有点名的
   工具**都裹进审批里。★ 这是**两个不同的问题**：白名单逐 agent 而定，档位表则是每个
   agent 共享的。★ 默认档位是 `ask`，所以例外都是被点名的那些：代码认识的那些内置工具
   （`agentkit/build/tools.go:62`），以及某个文件自己的 `allows:` 列出的那些。
   你不需要写任何权限代码。

5. **名字不能撞保留名**。名字住在 `decl.BuiltinToolNames`
   （`agentkit/spec/decl/decl.go:291`）：`ls` `read_file` `write_file` `edit_file` `glob`
   `grep` `execute` `task` `write_todos` `skill`。撞上会在 `Build` 时报错。
   ★ **它们住在 `decl` 而不是 build 包里，是因为两边都要用**：装配器拒绝冒用这些名字的
   provider（`reservedToolNames`，`agentkit/build/tools.go:75`），装载器则核对 Agent 的
   `tools:` 列表里点到的名字。两边互相不能引，所以这份清单住在两边之下。
   工具名同时是档位表里的键 —— 撞名会让档位表指向错误的工具。

   ★ **来自服务器的名字是带命名空间的。** MCP 工具挂在 `decl.MCPToolPrefix`
   （`agentkit/spec/decl/decl.go:373`）与 `decl.MCPSeparator`
   （`agentkit/spec/decl/decl.go:382`）之下，于是 agent 的表用一个前缀就能收窄一整个
   服务器，而不必列出它所有工具的名字。★ **拼出来的名字从不被解析回去**：
   `Table.Blocked`（`agentkit/build/policy.go:130`）匹配的是前缀，所以没有任何代码
   需要把一个名字拆开才能知道它属于哪个服务器。

6. **跨 provider 撞名也会报错**：`tools.go:118` 的 `validateBatch` 用 `seen` 跟踪，
   所以两个 provider 提供同名工具会在启动时失败，而不是谁先谁后地覆盖。

#### MCP provider

`mcpToolProvider` 的注册方式与其他 provider 一样 —— 在
`agentkit/build/tools.go:36` 的 `defaultToolRegistry` 里 —— 它也遵守
`agentkit/build/tools.go:20` 的同一份 `toolProvider` 契约。但它有四处不是通用的：

- **客户端活得比这一批工具久。** ★ 这一批工具每个快照只建一次：
  `buildHeavy` 跑在一个 `sync.Once` 之下（`agentkit/build/snapshot.go:41`），
  所以由 provider 持有的客户端会随快照一起被拆掉。**管理器**才持有这些客户端
  （`agentkit/build/mcpmanager.go:42`），provider 是向管理器要它们的
  （`agentkit/build/mcpmanager.go:89`）。
- **名字带命名空间，而且只在一处拼装。** `mcpPlan`
  （`agentkit/build/mcptools.go:68`）把服务器自己的工具名映射成暴露出来的名字与
  它们的档位；名字本身只在 `mcpExposedName`
  （`agentkit/build/mcptools.go:141`）里拼一次。provider 与 `/mcps` 投影都走
  `mcpPlan`，所以两者不可能对「一个工具叫什么」有分歧。
- **这个 provider 是可选的，而那条 Warn 是唯一的提示。** 够不着的服务器不该把
  agent 拖垮，所以 `Provide`（`agentkit/build/mcptools.go:201`）打一条 Warn，
  里面写明服务器、传输方式与错误（`agentkit/build/mcptools.go:217`），然后
  带着其余服务器的工具继续走。★ 那条 Warn 是**唯一**的提示 —— 工具根本没进表，
  用户看到的只是「这个功能好像没有」。
- **输出有上限，stdio 调用被串行化。** 上限放在组件的结果处理器里
  （`agentkit/build/mcptools.go:376`）；同一台 stdio 服务器的调用走 `serialTool`
  （`agentkit/build/mcptools.go:335`），它带一把按服务器的互斥锁和一个调用截止时间。

#### MCP 客户端的生命周期

给需要调试它的人：

- **一个服务器一个客户端。** 管理器为每个服务器持有一个条目
  （`agentkit/build/mcpmanager.go:42`），这个条目拥有该服务器的客户端、它的工具
  列表，以及它那把按服务器的互斥锁。
- **启动是单飞的。** `ensure`（`agentkit/build/mcpmanager.go:215`）在锁下启动
  客户端，所以同时来问的 provider 与 `/mcps` 面板拿到的是同一个客户端、同一个子
  进程。★ 一个服务器跑两个子进程，会让模型看到的工具取决于哪个进程先应答。
- **崩掉的子进程会在下一次调用时重新拉起。** 管道断了时 `markBroken`
  （`agentkit/build/mcpmanager.go:290`）关掉客户端，下一次调用重新起一个。
  ★ **超时不是连接断了**：服务器可能只是慢，所以截止时间到了就放弃，不会拆掉
  客户端（`agentkit/build/mcptools.go:358`）。
- **工具列表在一轮来要时重新列出**，条件是上一次列出的时间超过 30 秒
  （`agentkit/build/mcpmanager.go:34`，`mcpRelistInterval`）。
  ★ 这个间隔是写死的值，不是配置项：没人动的旋钮就是没人测的旋钮，而服务器
  的工具列表是以「天」为尺度变化的。
- **列表变了就推一代。** `Refresh`（`agentkit/build/mcpmanager.go:118`）报告列表
  是否变化，builder 通过与配置重载器同一个钩子推一代
  （`agentkit/build/builder.go:67`）。这次重列发生在该轮 agent 装配**之前**
  （`agentkit/build/builder.go:105`），因为这一批工具每代只建一次 —— 把重列放进
  构建里就永远不会跑到了。

### 3.2 加一个新的配置分区

比如新增 `mcps/` 目录：

★ 这个目录不再只是假设 —— `docs/mcp-design.md` 设计了它，而且这个分区已经在树上。
  下面这份配方就是它走的路子。

1. **`agentkit/spec/config.go`**：加目录常量 `DirMCP = "mcps"`，并在
   `Categories` 里加一项（含 `ID`、`Label`、`Dir`、`Ext`、`Editable`）。**热重载目录**和
   **非热重载目录**要分清楚 —— 改一个目录的语义（热不热）影响用户预期。

2. **`agentkit/spec/load.go`**：加 `loaderFor` 的分支（`config.go:340` 的
   `loaderFor`），并实现 `loadXXX`。

3. **`agentkit/spec/decl/decl.go`**：加纯声明结构体。

4. **热重载自动生效**：`agentkit/spec/config.go:81` 的 `hotDirList()` 是从
   `Categories` 里筛出 `Hot()` 为真的目录 —— 你在第 1 步把分类标成热重载，
   监听集合就自动有了。**不需要**再去 `reload.go` 里手工登记一遍
   （漏登记的后果是「改配置不生效且无任何提示」）。

5. **写入闸门**：在第 1 步把那一项标成 `Editable: true` —— ★ **清单**才是真相，
   settings 接口读的就是这个标志（`api/v2_settings.go`）；客户端从清单里拿可编辑的
   分类，而不是自己维护一份列表。不热重载的 section **不能**可编辑 —— 否则用户在
   界面上改了，重启才生效，而界面上没有任何提示。

6. **默认值**：`agentkit/spec/defaults.go`。

7. **出厂模板**：`openheron init` 要写出最小可运行配置，新分区也得有模板。把文件放进
   `agentkit/templates/files/<dir>/` —— ★ 那是命令层（`initcfg`）与配置层
   （`agentkit/spec`，它供给编辑器）**都能依赖**的**叶子**包 —— 然后从那儿读。
   ★ **一份文件的内容只有一个来源**：同一个文件的两份拷贝，就是两份会漂移的拷贝，而拷贝
   正是原先的依赖形状逼出来的。那些不是磁盘文件的分区骨架（agents 的 frontmatter 带着
   可用的模型列表）留在 `spec.Skeleton(catDir, …)`。

   ★ **init 按目录复制一个分区，不按文件名清单。** `initcfg` 的 `copySection` 问模板包
   这个分区有哪些文件，然后把拿到的都复制过去 —— ★ 所以
   `agentkit/templates/files/<dir>/` 里多一个模板文件，init 就多写一个，一行 Go 都不用改。
   ★ `llm/` 是唯一的例外：它里面的文件是**候选**（provider 自己的模板与通用兜底），
   一堆候选不是一个要复制的分区。

#### `mcps/` 分区

`mcps/` 分区是一个 MCP 服务器一个文件，而且**文件的存在本身就是定义** ——
没有 `enabled` 这个键。

- **声明**由常量 `DirMCP = "mcps"`（`agentkit/spec/config.go:25`）与
  `Categories` 里那一项（`agentkit/spec/config.go:66`）给出 —— 可编辑、热重载、
  `.yaml`。
- **装载**由 `loadMCP`（`agentkit/spec/load.go:306`）完成：一个文件，一个服务器。
- ★ **这个分区的「热」意味着客户端会被重建。** 声明就是服务器的身份，所以声明变了
  就是服务器变了：旧客户端被拆掉，重新起一个新的
  （`agentkit/build/mcpmanager.go:215`），依据是那份声明的指纹
  （`agentkit/build/mcpmanager.go:330`）。★ 一个资源没有跟着重建的热分区，就是在
  撒谎说自己热。
- **写入闸门**拒绝一个读不回来的名字：`validateFileName`
  （`agentkit/spec/write.go:88`）只接受 `<server>.yaml` 且服务器名合法
  （`agentkit/spec/write.go:107`）—— 文件名就是服务器名，而服务器名就是这台服务器
  工具的命名空间段。
- **脚手架**是 `Skeleton`（`agentkit/spec/write.go:161`），其中 `mcps/` 那一支
  （`agentkit/spec/write.go:186`）写出的是一份**能加载**的文件：★ `mcps/` 下的文件
  **定义**一台 server，所以一份解析不了的文件不是「server 关着」，而是「配置加载不了」。
  可选的那些字段写成注释，而 `command` 是一个没装的占位符 —— 于是 server 起不来，
  日志里说一句，agent 照跑。
- ★ **示例是一个文件，两层读它。** `agentkit/templates/files/mcps/example.yaml` 既是
  `openheron init` 写下的模板（`initcfg/initcfg.go`），也是配置编辑器给出的脚手架
  （`agentkit/spec/write.go`）。★ 编辑器由 API 供给，而 API 不能 import `initcfg` ——
  那是循环 —— 所以文件住在一个**两边都能依赖的叶子包** `agentkit/templates` 里。★ 拷贝会漂移。

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

#### Agent 身上的三张清单

`tools:`、`skills:` 与 `mcps:` 都是**白名单**，而且**三张默认都是一个都没有** ——
「不写」与「写空」是一回事，而它们的意思是「什么都没有」。

| 键 | 它点名什么 | 不写表示 |
|---|---|---|
| `tools` | 这个 agent 可用的工具 —— **暴露出来**的名字，模型看见的那些 | 没有工具 |
| `skills` | 这个 agent 可加载的技能 —— `skills/` 下的文件夹 | 没有技能 |
| `mcps` | 这个 agent **挂载**的 MCP server —— 每一个都会起一个子进程 | 没有 server |

★ **不要写 `disabled_tools`。** 它与 `tools` 的含义**正好相反** —— 一个删工具，另一个
点名工具 —— 所以装载器**拒绝**这个文件（`agentkit/spec/load.go:96`），而不是把键改个名：
只改名字就等于把黑名单悄悄变成了白名单。那条消息会说清两者的方向是反的。★ **没有
别名**：一个仍按旧义工作的别名会让黑名单继续活着，一个按新义工作的别名则等于替用户把
黑名单翻成白名单。

★ **工具集是 snapshot 的，而清单负责收窄它。** snapshot 的工具是整个 snapshot 共用的一套；
每个 agent 的 `tools:` 清单移除这个 agent 不许用的东西
（`agentkit/build/topology.go:72` 的 `specTable`），模型永远看不见被清单移除的工具。
★ **这里没有「没事可做」的捷径**（`agentkit/build/policy.go:145`）：**空**清单意味着
**全部**移除。

★ **MCP 有两层。** `mcps:` 说哪些 server 被**挂载**；`tools:` 说它那些工具里哪些被
agent **看见** —— `mcp__<server>__<tool>`，或者模式 `mcp__<server>__*`。★ `mcps: [github]`
而 `tools:` 里没有 `mcp__github__*`，那是把 server 起起来，agent 一个都看不见。
★ 两层互相矛盾是**装载期错误**（`agentkit/spec/load.go:643` 的 `checkAgentMCPNames`）：
点了 `mcp__github__*` 而 `mcps:` 里没有 `github`，这条规则永远不可能命中。

#### 两条提示

两条都是**提示**，不是错误 —— 配置是合法的，只是没有说出作者的本意
（`agentkit/spec/load.go:540` 的 `noticeAgentTools`）：

| 提示 | 它意味着什么 | 该怎么办 |
|---|---|---|
| `tools` 点名了一个不存在的工具 | 内置工具就是 `decl.BuiltinToolNames` 里那些，而某个 provider 的工具只有在 `tools/` 下写了它的文件时才存在 | 核对拼写；provider 缺失就去写那个 `tools/` 文件 |
| `tools` 点名了 `skill` 工具，而 `skills` 是空的 | 配置合法，但自相矛盾：agent 能要一个技能，却没有技能可加载 | 在 `skills:` 里点名那个技能，或者把 `skill` 从 `tools:` 里去掉 |

★ **`mcp__*` 名字会被这项检查跳过**：server 的工具是运行时才发现的，所以它现在还没有的
名字不算拼错。

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
agent** 的压缩（`agentkit/emit/emit.go`）：子 agent 的摘要不是会话历史的边界。

**整条链**，从头到尾：

| 环节 | 在哪 | 发生了什么 |
|---|---|---|
| 触发 | `agentkit/build/summarize.go` | eino 的摘要中间件在每次模型调用前给 agent 的状态数 token，超过阈值就压缩 |
| finalizer | 同文件的 `finalize` | 替换后的历史是 agent 的 system 消息**加上摘要** —— openheron 的上下文提示就是一条 system 消息（`agentkit/run/input.go`），丢掉它会让 agent 在这一轮余下的时间里没有自己的规则 |
| 宣告 | eino 的 `after_summarize` 事件 | 中间件把替换后的历史发到 agent 的事件流上 |
| 接住 | `agentkit/emit/emit.go` 的 `compactionFrom` | 推导层读这个事件，把压缩消息放进 `Artifacts.Compaction` —— 取的是**摘要**，不是同处替换历史里的那条 system 消息 |
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
（`transport/transport.go:208`），reducer 当场把边界插进列表（`web/src/lib/events.ts:412`）。

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
`emit.ContextUsageOf`（`agentkit/emit/context.go`）顺着消息走一遍，留下每个 agent 最后一条
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

两条都调 `emit.ContextUsageOf`，所以屏幕上的数字与刷新后的数字不会分叉。

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

### 3.9 CI 与发版

`.github/workflows/ci.yml` 就是整条流水线：检查、一份草稿发布，以及挡在它前面的安装闸门。

| job | 跑什么 | 需要 |
|---|---|---|
| `go` | `make lint-go` + `make test` + `make test-race` | Go |
| `web` | `make lint-web` + `bun run test:unit` | bun |
| `e2e` | `make test-e2e` —— 用它自带的假后端 | bun + chromium |
| `secrets` | gitleaks 扫整个历史（§5.9） | — |
| `install-script` | `make test-install` —— 用本地假发布测安装脚本 | — |
| `release` | `make release`，然后建一份**草稿** GitHub Release | 上面四个检查 |
| `install` | 在我们发布的每个平台上装那份草稿里的产物 | `release` |
| `publish` | 把草稿转成公开的 release | `install` |

★ **发布等检查**（`needs`），所以一个红 commit 上的 tag 什么也发不出去。流水线因此是
**一个文件**：job 没法等另一个 workflow 里的 job，两个文件就意味着检查要跑第二遍。

★ **发布还要过安装闸门。** `install` job 装的就是 `release` job 造出来的那批产物，
在我们要发布的每个平台上都装一遍，全装成了 `publish` 才把 release 公开 —— 一个没人
装得上的二进制，不该先发布出去。★ **失败的那条腿把原因写在注解里**，不只是写在日志
里：job 日志要管理员权限才读得到，注解谁都能从 API 读到。

★ **冒烟不在 CI 里。** `make smoke` 需要一个答得上话的模型，而答得上话的模型在 runner
上不可复现 —— 在那里失败是噪声，不是信号。E2E 才是确定的那一半，也是能给发布当闸门的
那一半。

★ **跑的就是仓库自己的命令。** CI 调的是 `make lint-go` / `make test` /
`make test-race` / `make test-e2e` —— §5.2 列的那些命令 —— 所以配方只在 Makefile 里
有一份。`make lint-go` 与 `make lint-web` 是 `make lint` 的两半，拆开是为了让每个 job
只装自己需要的工具链。

★ **竞态检测器是 `go` 这个 job 里的一个 step，不是单独的 job。** 分支保护本来就要求
这个 job 的名字，所以放在里面的 step 自动被它罩住 —— 而新开一个 job 会是一个**没人等**
的检查；没人等的检查不是闸门。同理，这个 job 的**名字**不能改：改了就是一个新的检查
名字，旧的那个会变成"缺失"。

#### 怎么切一个版本

★ 发版是手动做的：**在 `main` 上打一个 tag** 推上去，剩下的交给 GitHub。

**打 tag 之前**

1. `main` 是绿的 —— README 顶部的徽章，或者 Actions 页最后一轮。
2. 工作树干净、`main` 是最新的：`git pull --ff-only`。★★ tag 必须指向
   **已经在 `main` 上的那个提交** —— `main` 受保护，打在侧分支上的 tag
   会发布一份没人合并过的东西。
3. tag 名定好了。约定是 `v*`（`v0.1.0`），而且 tag 就是打进二进制里的
   那个版本号。
4. ★ **tag 的规则叫 `release-tag`**（Settings → Rules → Rulesets）。它
   禁止**删除或移动** tag —— 这是故意的：失败的发版，那个 tag 就留在仓库里
   当「试过什么」的记录。创建 tag 是允许的。

**打 tag**

```bash
git checkout main
git pull --ff-only
git tag -a v0.1.0 -m "openheron v0.1.0"
git push origin v0.1.0
```

★ 用**附注 tag**：那段说明就是发布页上显示的 tag 描述。

**接下来发生什么**

工作流在这个 tag 上跑起来 —— 三项检查，然后 `release` 先发**草稿**，
`install` 在我们发布的每一个平台上把刚发出去的产物装一遍并跑起来，最后
`publish` 才把草稿转成正式发布。装不上的二进制不会变成公开产物。

**核对结果**

- 发布页上有六个平台的产物、`checksums.txt` 和两个安装脚本。
- 校验一次下载：`sha256sum -c checksums.txt --ignore-missing`。
- 真的装一次：
  `curl -fsSL https://github.com/ouranoshong/openheron/releases/latest/download/install.sh | sh`
  —— 然后 `openheron version`。

**失败了怎么办**

★ 发布停在**草稿**，所以没有公开出去任何东西。★★ 但**tag 已经存在了**
⚠️，而 ruleset 禁止删除它 ⚠️ —— ★★ 所以失败的发版是**再打一个 tag**
（`v0.1.1`）来修，而不是去补救那个旧的 —— ★★ 失败的那个 tag 就留在
仓库里，当作「试过什么」的记录。

要把已有的 tag 再发一次，手动跑一遍工作流，把 tag 填进去。

两个安装脚本（`scripts/install.sh`、`scripts/install.ps1`）随 release 一起发布，
所以 `releases/latest/download/install.sh` 指向的永远是跟旁边那些二进制同一版本的脚本。
`make test-install` 用一个本地假发布测安装脚本：下载、校验和这道闸（含一个被破坏的
产物 —— 它必须什么都装不上）、安装，以及版本核对。

★ **假发布必须长得像真的 —— 包括 URL 形状。** API 和网页不在一个主机上
（`https://api.github.com`，不是 `https://github.com/api`），★ 而一个把
两条路径当同一条的假服务器，恰恰会盖住这一类错误：★ harness 当时是绿的
—— ★★ 它测的是一个安装脚本在生产里根本不会发的请求。

#### 改动怎么进 `main`

`main` 由一条 ruleset 保护：所有改动都得走 PR，强推与删除被拒，三项 CI 检查必须绿。

| 规则 | 实际意味着什么 |
|---|---|
| 必须走 PR | 直接 `git push` 到 `main` 会被拒 —— 仓库主人也一样 |
| 必须过的状态检查 | 三个 CI job 都要通过 |
| 需要的批准数 | 0 —— 单人仓库里没有人能批 |
| 作废过期的批准 | 推了新 commit，之前的批准就作废 |
| 分支须与 main 同步 | PR 必须包含当前 `main` |

★ **要过的状态检查就是 job 的显示名** —— `go (vet + gofmt + tests)` 之类。所以给 job
改名会把这道闸门**悄悄弄坏**：合并按钮会一直等一个永远不会来的检查。改 job 名的时候，
ruleset 要在同一件事里一起改。

★ **合并不会发布任何东西。** 发布 job 只为 `v*` tag 跑，所以合并意味着「代码在 `main`
上了」，不意味着「有二进制了」。

#### Kit 的版本号

★ **版本号只住在一处：Makefile 里的 `KIT_VERSION`。** 其它地方要么从那里拿，
要么根本不写版本号：

| 位置 | 版本号从哪来 |
|---|---|
| `Makefile` 的 `KIT_VERSION ?= …` | **源头** |
| `sandbox-kit/openheron/openheron.yaml` 的 `args.version.default` | 构建时盖进去（对暂存副本 `sed`） |
| 发布出来的镜像 tag | `$(KIT_VERSION)` |
| 指南（`sandbox-kit/TUTORIAL.md`） | 它写的是 `latest`，CI 把它指向那次发布 |

★ **所以一次发版就是一个只动两行的 PR。** 开一个 PR，改 Makefile 里的
`KIT_VERSION` 与描述文件里的 `args.version.default`，别的都不用动：指南写的是
`latest`，而 CI 从你打的 tag 推导版本。

```sh
# the whole contract of a release pull request — $NEXT is the version you tag
sed -i "s/^KIT_VERSION ?= .*/KIT_VERSION ?= $NEXT/" Makefile
sed -i "s|^    default: .*|    default: \"$NEXT\"|" sandbox-kit/openheron/openheron.yaml
```
★ **既然构建总会覆盖它，描述文件里为什么还要写版本号。** 那份值是个占位 ——
但必须是**合法**的占位：描述文件的 `pattern` 不接受不是版本号的东西，而从本地
目录安装 Kit 时 `sbx` 是直接读这个文件的。一个解析不了的占位，就是一份根本读不了
的描述文件。

★ **`latest` 是指针，不是另一次构建。** CI 用发布推上去的那个镜像创建它，
所以两个 tag 的 digest 相同，而 job 会检查这一点。一个指向别处的 `latest`，
就是指南在讲一句假话。

★ **版本 tag 是消费者钉住的那个。** `latest` 会走，`0.0.7` 不会。两个都发布，
教程里说了什么时候用哪个。

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

### 4.4 ★ 功能备忘录：长会话的虚拟化

★ **这是一份备忘录，不是待办清单。** 它记下我们知道自己要什么、为什么、以及
做什么之前得先成立什么。树里没有任何东西依赖它。

**问题。** 几百条消息的会话渲染几百行 DOM。每一行装着解析过的 markdown、工具
卡、思考块。浏览器的布局与绘制成本随行数增长，而滚动容器里只要有东西变了，
就得把整个列表重新布局一遍。

**已经做掉的**（这也是虚拟化不是第一步的原因）：

| 已做 | 消掉了什么 |
|---|---|
| `web/src/lib/api.ts` 里的按帧合并 | 每个 token 一次状态更新 → 每帧一次 |
| `MessageRow` 与 `MessageParts` 上的 `memo` | 每个 token 重渲染所有行 → 只重渲染被改的那行 |
| `web/src/components/message-list.tsx` 里的 `ResizeObserver` | 每次渲染一次强制回流 → 每次内容增长一次 |
| `MarkdownText` 的 memo | **已结束**的那些消息的 markdown 被重解析 |

★ 剩下的是 DOM 本身：行还在，只是它们不再被重渲染。500 行已结束的消息，
成本是「每次变化布局绘制一次」，不是「每个 token 一次」——通常这就够了。

**提案。** 虚拟化这个列表：只渲染视口里的行（外加一点 overscan），用占位符
撑起总高度。明显的选择是 `react-window`——它很小，只做一件事，而且不是框架。

**得先成立什么**——这些是推迟的理由，不是实现细节：

- ★ **「贴底」的行为必须活下来。** 滚动容器在用户位于底部时把自己钉在最新
  的消息上，用户一往上滚就放手（`web/src/components/message-list.tsx`——
  `pinnedRef`）。虚拟化的列表得保住这一点，包括「用户在看旧消息时 newest
  消息到达」这种情况。
- ★ **行高事先不知道。** 行里装着解析过的 markdown、语法高亮的代码块、可折叠
  的工具卡——它们的高度随内容变，也随用户的点击变。假设固定高度的虚拟器会
  跳。重测量的那一遍恰恰是虚拟化本来要省掉的那部分开销。
- ★ **工具卡与思考块会折叠。** 收起的卡很矮，展开的很高。高度缓存在用户折叠
  时必须失效。

**验收。** 500 条消息的会话在一轮流式期间必须保持响应——量化成：流式期间帧率
不低于 30fps，一个 token 到达与它出现之间的间隔不超过 100ms。今天等价的检查
是滚动那条 spec（`web/test/e2e/scroll.spec.ts`）。

**动手之前先量。** React DevTools 的 Profiler，录一段长会话里的一轮流式。要看
的三个数：消息列表的 commit 时长、布局的次数、以及花在 `MarkdownText` 上的
时间。如果布局次数低、commit 时长小，那么 DOM 就不是瓶颈，虚拟化买不到什么。

### 4.5 构建与嵌入

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
| `make test-race` | 同一套在竞态检测器下跑（`go test -race -count=1 ./...`） | — |
| `cd web && bun run test:unit` | 纯函数层（reducer / 分帧 / store / 高亮） | — |
| `make test-e2e` | 真实浏览器 + 确定性假后端 | 无（自带假后端） |
| `make smoke` | 真后端联调：SSE 分帧、历史回放、审批接线 | **测试实例在跑，且模型可达** |
| `make lint` | Go vet + gofmt + oxlint + tsc | — |
| `make lint-go` / `make lint-web` | `make lint` 的两半，给只装了一种工具链的 job 用 | — |
| `make check` | `lint` + `test`，提交前跑这个 | — |
| `make install-hooks` | 装 pre-commit 钩子（gitleaks + gofmt） | `pre-commit` |

起测试实例：

```bash
make test-env-backend   # 前台起，:18099，独立配置 + 独立会话目录
make test-env-reset     # 只重建测试目录，不启动
make test-env-sync      # ★ 改过开发侧的 agent 定义后必须跑
```

★ **`make test-env-sync` 是必须的，不是可选的** —— 测试实例里的 `agents/ llm/ tools/ skills/`
是从开发侧**复制**的，改完 agent 不同步，测的还是旧定义。

★ **冒烟脚本需要一个答得上话的模型。** 它们对着测试实例真跑一轮，而测试实例的
`llm/` 指向一个本地端点。端点不通或很慢时，冒烟会失败——而且那个失败看起来
像协议出 bug，而不是模型不在。**先查端点**
（`curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:1234/v1/models`），
它不答应就先别跑冒烟。

单个冒烟脚本（默认就连 :18099，**不需要手写 `OPENHERON_BASE`**）：

```bash
cd web && bun run smoke:interop            # SSE 分帧、拼接、历史水合
cd web && bun run smoke:no-undefined-url   # 不会发出拼错的 URL
cd web && bun run smoke:hitl               # 审批 resume 走通
```

### 5.3 Go 单元测试

`go test ./...` 覆盖 20 个包。契约主要由这几处锁定：

| 包 | 锁住的契约 |
|---|---|
| `agentkit/spec` | 配置根解析、frontmatter 严格解码、深度校验、环检测、写入闸门 |
| `agentkit/build` | 工具装配、allow/ask 档位表、闸门、拓扑、嵌套恢复、技能范围 |
| `agentkit/run` | 输入构造（子 Agent 消息不混入父历史）、事件桥接 |
| `api` | 路由、SSE 帧、错误信封、`threadId` 必填 |
| `kb` | 索引、评分、目录格式（含旧中文表头兼容） |
| `storage/filestore` | 追加写、原子重写、checkpoint、待审批持久化 |
| `transport` | 事件类型、归属、用量分账 |
| `initcfg` | 出厂模板的完整性 |
| `agentkit/fsx` | 写边界（含符号链接逃逸）、`ls`/`glob`/`grep` 的上限、绝对 glob pattern |

★ **竞态检测器也要跑**：`make test-race`（`go test -race ./...`）。普通套件是绿的而
  检测器是红的，这种事比人们以为的更常见 —— 竞态要两个 goroutine 碰面才成立，而
  普通套件不在乎它们什么时候碰上。它抓到的那三个生产竞态（2026-11-06）是同一个错误：
  **锁就在那儿，只有一条路径没走它。** 放在结构体中间的互斥锁读起来就是「它护下面
  那些字段」，`FileSession` 的身份字段正是照这个读法落到了保护之外。
  ⇒ 给带锁的类型加字段时，把「这把锁护什么」写在**锁旁边**，并且那个字段的每一次
  读写都要走它。

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
**21 个测试文件、327 个用例**，覆盖 `sse.ts` / `events.ts` / `store/` / `highlight.ts` /
`thinking.ts` / `turns.ts` / `approval.ts` / `settings-validate.ts` 等纯函数层。

★ **客户端自己不留「哪些分区可编辑」的清单。** 真相是 **manifest** —— 分区表归服务端
所有（`agentkit/spec/config.go` 的 `Categories`）—— 客户端把 manifest 里可编辑的分区
传给校验器（`web/src/lib/settings-validate.ts` 的 `validatePath(path, editable)`）。
★ 客户端自己留一份清单就是**第二份真相**：分区一加上来，客户端就说它不存在 ——
「mcps/ is not editable」就是这么来的。

★ `store.test.ts` 测的是**整个 zustand store**，所以它在跑之前会
stub 掉 `globalThis.localStorage` 和 `globalThis.fetch`（`store.test.ts:35-84`）。
Bun 本身没有 `localStorage`，不 stub 会炸。

★ 事件回放与历史水合**必须收敛到同一个状态** —— `store-parity.test.ts` 专门钉这一点，
外加一块「已知差异，钉住别动」的断言。

### 5.6 E2E

`make test-e2e` → `playwright test`，**12 个 spec、152 个用例**：
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

**（六）★ 一次性的测量量错了时刻。** 输入框换版式发生在对话渲染出来的时候，比触发它的那次
发送**晚** —— 发送完立刻量，量到的是**还没动**的输入框，于是「after > before」
对一个「还没开始动」的输入框也是假的。★ 在快的机器上过、在 CI runner 上红 ——
测试里的时序问题看着就像 flaky。
★ 用 `expect.poll` 量 —— 旁边的版式断言都是这么写的：等状态到位，而不是猜它
什么时候到。

**（七）`smoke:hitl` 会抖，约 5 次里 1 次** —— 它依赖模型*选择*调 `write_file` 才能触发审批。
那是模型的自由，不是代码回归；脚本会重试，全都不触发时**明确报错**，不伪装成接线有问题。
★ 它有一个**前置条件**：**真正调工具的那个 agent**，其 `write_file` 必须落在 `ask` 档
—— ★ 而那就是**默认值**：没有任何文件在 `allows:` 里点到它 —— 并且这个 agent 自己的
`tools:` 清单必须点到它，否则这个工具压根不在它的工具集里。工具够不着，自然没有审批
可触发。
★ 别只盯着 `main`：出厂配置里 `main` 的是
`tools: [ls, read_file, glob, grep, task, write_todos]` —— 读取类与委派工具，没有写入类
—— 但它只做编排，会把活**委派**给 `task`，而 `task` 的 `tools:` 清单点了 `write_file`，
它在 `ask` 档 —— 审批照样触发。
2026-10-29 实测的事件流：`TOOL_CALL_START name=task`（子 agent 是以工具形态被调的），
随后 `TOOL_CALL_START name=write_file subagentRunId=task`，
中断的 `subagentRunId` 也是 `task`。
要确认某档是否可达，看**每个** agent 的生效工具表：`GET /v2/agents` 与
`GET /v2/agents/task` 会给出每个工具的 tier 与 reason。

**（八）改界面必须浏览器验证。** 「我改了，测试也绿了」不构成验收依据 ——
脚本级测试看不见渲染层。

### 5.8 ★ 成本基准

`benchmarks/` 衡量的是多 agent 拓扑相对于「一个强模型做同样的事」多花或少花多少钱。
它是带断言的度量，不是单元测试：跑手扫各个 arm，`analyze.ts` 校验结果。

```bash
# 服务端，指向一个 scratch 配置根与一个 scratch 工程根
./bin/openheron --config-dir $PWD/.openheron-bench --project-root $PWD/benchmarks/out/work

# 扫描：--arm 与 --task 接逗号分隔的列表
bun run benchmarks/run.ts --base http://127.0.0.1:18099 \
  --config $PWD/.openheron-bench --work $PWD/benchmarks/out/work \
  --arm a2-max-parent-flash-workers-cited,b-max-solo \
  --task write-a-file,two-file-change,add-a-method-to-three-types --repeats 3

# 分析：分段成本、中位数、比值 —— 以及那些断言
bun run benchmarks/analyze.ts --report benchmarks/out/report.json --config $PWD/.openheron-bench
```

★ **关于它，有三件容易搞错的事。**

**（一）`--work` 是 scratch，fixture 树才是源头。** 跑手在每个任务前都会清空
work 目录并把 `benchmarks/fixtures/work/` 拷进去，所以每个任务都从同一棵树开始，
判据读的是 agent 留下的文件。把 `--work` 指向 fixture 目录会毁掉 fixture；
跑手会拒绝这样启动。

**（二）成本按 agent 实际跑的那个模型定价，不是按 arm 表说它该跑的那个。**
transcript 逐条带着它 —— agent 的名字在消息上，模型在消息的 metadata 里
（`api/v2_convert.go`）。读那个，别读 arm 表：arm 表只在「一个 agent 跑了两个模型」
这种少见情况下兜底，而这种情况会被报出来（`modelConflicts`），不会被悄悄定价掉。

**（三）没跑完的 run 不是免费的 run。** 厂商为它发出的每一次调用计费，
包括那些没回来的 —— 它们不产生消息，所以任何本地记账都看不见它们。
失败 run 的成本是**下界**，报告里用 `costIncomplete` 标出来。

★ **还有一个坑：** 连接只是*订阅*这个 run，并不消费它，所以**客户端放弃之后
run 还在跑**（`api/v2_run.go`）。因此跑手超时时会调
`POST /v2/sessions/{id}/abort` —— 不这么做，扫描会继续为那些没人读的答案付钱。
abort 是幂等的，所以对已经结束的 run 调用它也安全。

★ `--repeats 3` 是下结论的最低要求：模型是随机的，同一个任务单次重复之间能差 2×。
报**中位数**。

结论、价格表与厂商账单的对账、以及这个基准测量的两种形态，都写在
`benchmarks/README.md`。

### 5.9 仓库里的密钥

两层，因为它们看到的不是一回事：

| 位置 | 扫什么 | 什么时候 |
|---|---|---|
| pre-commit 钩子 | **暂存区**的差异 | 每次提交，本地 |
| CI 的 `secrets` job | **全部历史** | 每次 push 与 pull request |

★ 钩子是便宜的那一层，也是拦住一个密钥被写进去的那一层。CI 那一层找的是钩子
找不到的东西：★ 钩子存在之前提交进去的密钥，或者在没有钩子的机器上提交的那次。

装钩子用 `make install-hooks`（需要 `pre-commit` —— `pipx install pre-commit
`）。★ gitleaks 的版本在 `.pre-commit-config.yaml` 里**钉死** —— ★★ 上游新增
一条规则，不该在某天把所有人的提交都变红。

★ **规则不放松。** gitleaks 报了一个不是密钥的东西，修法是 `.gitleaks.toml` 里
一条**窄的路径 allowlist** —— ★★ 一个已知放夹具的路径，★★ 而不是一条「凡是像密钥
就算」的宽正则。★★ 宽 allowlist 就是一个洞加一个配置文件。

★ **报告是脱敏的**（`--redact`）：日志说发现哪，不说密钥是什么 ——
CI 日志不是打印活密钥的地方。

★ **CI 那层跑的是二进制，不是 action。** gitleaks 的 **action** 会做 license
校验 —— 组织有 license 没问题，对其他人就只是多一道手续。★ 扫描器本身是
MIT，所以 job 直接下载发布出来的二进制来跑。★ **校验和也钉死** ——
从网络拉一个不校验的二进制，正是这个 job 要抓的东西 —— ★★ 也就是说，
这个 job 反而是「二进制被掉包」最早被发现的地方。

★ **逃生口是 `SKIP=gitleaks git commit`** —— 给「这不是密钥、规则待修」的紧急情况
。★ CI 那层没有逃生口 —— 有它就是为了这个。

★ **CI 那一层的范围到底有多大。** 扫的是
`git log -p -U0 --full-history --all --diff-filter=tuxdb` —— checkout 拿到的
每一个 ref —— 而 checkout 拿到的东西很多：`fetch-depth: 0` 是「所有分支与 tag
的全部历史」，而 pull request 是以它的 merge commit 被 checkout 的，那个
merge 的 parent 就是该分支自己的提交。merge commit 自己没有 diff，所以
gitleaks 报的**提交数**比 git 报的低；但内容照样扫到了，因为它随 parent 一起
到。

★ **别加 `--log-opts`。** 它会把上面那条命令**换成**你给的参数，而 `--all`
不在其中：扫描范围会悄悄缩到 HEAD 可达的提交
（`gitleaks/sources/git.go:93-94`）。一个缩窄了却照样通过的扫描，是最糟的一
种缺口 —— 所以这个 flag 一直不加，除非有人测出默认范围确实漏了什么。

## 6. 排障

| 症状 | 先查 |
|---|---|
| 启动报配置缺失 | 启动日志里的 `config root` 那行 —— 它打印了实际用的根和项目根 |
| 改配置不生效 | 该分区热重载吗？`llm/`、`agents/`、`tools/`、`mcps/` 是热的；`server/`、`skills/` 要重启 |
| 启动时报 `disabled_tools` was renamed to `tools` | ★ 两者含义**正好相反** —— 一个删工具，另一个点名工具。删掉 `disabled_tools`，在 `tools:` 里列出这个 agent 该有的工具（`agentkit/spec/load.go:96`） |
| 启动时报 `names the MCP tool` … `but does not mount the server` | ★ 挂载与可见是两层：把那个 server 加进 `mcps:`，或者把那个工具从 `tools:` 里去掉（`agentkit/spec/load.go:643`） |
| 提示说 `tools` 点名了一个不存在的工具 | 内置工具就是 `decl.BuiltinToolNames` 里那些（`agentkit/spec/decl/decl.go:291`）；某个 provider 的工具只有在 `tools/` 下写了它的文件时才存在 |
| 提示说 `tools` 点名了 `skill` 工具，而 `skills` 是空的 | 配置合法但自相矛盾：在 `skills:` 里点名那个技能，或者把 `skill` 从 `tools:` 里去掉 |
| Agent 调不动某工具 | `GET /v2/agents/:agentId` 看生效工具表；再读工具的 `reason` —— `tools`，或者默认的 `ask` 档（它会问） |
| 某个 agent **一个**工具都没有 | ★ 工具清单是白名单，而且**默认一个都没有**：不写 `tools:` 就是没有工具。列出这个 agent 该有的工具 |
| 残留的 `policy/` 目录好像还在管你的旧档位 | 这个分区退役了：档位住在提供工具的那些文件里，而默认档位是 `ask`。启动日志里会说明 —— 那条提示落在 `deferred` 里 |
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
| **残留的 `policy/` 目录被丢弃了 —— 而配置照样加载** | 装载器只走五个分区（`agentkit/spec/config.go:40-71`）；退役的目录只被「注意到」，不会被读（`noteRetiredDirs`，`agentkit/spec/config.go:329`） | `policy/` 分区在 2026-11-07 退役了：档位住在提供工具的那些文件里，而默认档位是 `ask`。从前留下的 `policy/permissions.yaml` **不会被读** —— 以前被禁的工具现在会问，以前不用点头就过的工具现在也会问。★ 那条提示落在 `deferred` 里（启动日志与 `/v2/health` 都带着它），并说清新档位在哪儿；配置编辑器直接拒绝这个路径（「unknown config section」，`agentkit/spec/write.go:72`）。★ **空的** `policy/` 目录不报任何东西 —— 它里面本来就没有被丢掉的东西。 |

## 8. 提交前

```bash
make check        # lint + test
```

- `gofmt -l .` 必须为空。
- 运行时输出（日志、错误、警告、界面文案、写入用户配置的文件内容）**一律英文**。
- 代码注释用英文，写「为什么」不写「是什么」，别把框架语义当契约。
- 测试用例名与测试夹具数据**保持中文**（那是描述性文本，不是运行时输出）。
- 新增/删除用例要同步文档里的条数。
