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
task init          # 创建 .openheron/ —— 服务端读的配置（每个克隆只需一次）
task build-go     # 只构建后端（不含界面），迭代 Go 代码时最快
task web-dev      # Vite dev server（:5173，代理 /v2 到 :8080）
task dev          # air：Go 自动重建重启
task check        # lint + test，提交前跑这个
```

★ **没有配置，服务端就不启动**：它只读一个配置根 —— `$OPENHERON_CONFIG_DIR`，
否则 `<项目根>/.openheron` —— 为空时报 `no configuration at <root>: run
openheron init to create one`。仓库里带的是 `.openheron-example/`（样例），
不是 `.openheron/`（真正那份：里面是你的模型和 API key，所以被 git 忽略了）。
**所以新克隆之后第一个命令就是 `task init`。** 它只写缺的文件，已经在的保留，
跑两遍不会有任何变化。

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

**（一）日志只有一处装配。** `agentkit/obs` **造** logger，由组合根把它造好的那一个
注入下去 —— 没有全局的那一个可走。业务包里不要直接 `log.Printf` / `fmt.Fprintln(os.Stderr)`，
用你的构造函数拿到的那个 `*slog.Logger`。级别与前缀只在一处定义 ——
`agentkit/obs/logger.go:39` 解析等级，`agentkit/obs/runid.go:22` 造前缀 ——
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

**已经没有例外了。** `llm/common` 原来用标准 logger 打请求日志，那段代码搬到了
`agentkit/obs/httplog`，所以这个叶子包一行日志都没有，每一行都带级别。
`llm/common` 里出现任何一处 openheron 内部 import 都仍然算回归。

★ 组合根围绕一个共享的 `slog.LevelVar` 建 logger，并逐层传下去。没有全局 logger：
需要记日志的包接收一个 `*slog.Logger` 形参，零值用 `obs.Discard()`。
★ **模块不回取全局** —— 这正是 `task test-race` 有意义的前提：运行期被换掉的包级变量
与 `Builder.Warm`、`Reloader.Watch` 起的后台 goroutine 赛跑。

**（二）声明与行为分离。** `agentkit/spec/decl` 只放纯结构体，不引任何其它包；
`agentkit/spec` 放行为（装载、校验、重载、写入）。

★ **两边共用的那套词汇也住在 `decl` 里，而这是依赖方向的事实，不是图方便。** 内置工具名
（`decl.BuiltinToolNames`，`agentkit/spec/decl/decl.go:347`）被**两端**读：装载器拿它核对
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

2. **注册**。在 `agentkit/build/tools.go:52` 的 `defaultToolRegistry` 里加上你的
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
   （`agentkit/spec/decl/decl.go:347`）：`ls` `read_file` `write_file` `edit_file` `glob`
   `grep` `execute` `task` `write_todos` `skill`。撞上会在 `Build` 时报错。
   ★ **它们住在 `decl` 而不是 build 包里，是因为两边都要用**：装配器拒绝冒用这些名字的
   provider（`reservedToolNames`，`agentkit/build/tools.go:75`），装载器则核对 Agent 的
   `tools:` 列表里点到的名字。两边互相不能引，所以这份清单住在两边之下。
   工具名同时是档位表里的键 —— 撞名会让档位表指向错误的工具。

   ★ **来自服务器的名字是带命名空间的。** MCP 工具挂在 `decl.MCPToolPrefix`
   （`agentkit/spec/decl/decl.go:401`）与 `decl.MCPSeparator`
   （`agentkit/spec/decl/decl.go:410`）之下，于是 agent 的表用一个前缀就能收窄一整个
   服务器，而不必列出它所有工具的名字。★ **拼出来的名字从不被解析回去**：
   `Table.Blocked`（`agentkit/build/policy.go:130`）匹配的是前缀，所以没有任何代码
   需要把一个名字拆开才能知道它属于哪个服务器。

6. **跨 provider 撞名也会报错**：`tools.go:118` 的 `validateBatch` 用 `seen` 跟踪，
   所以两个 provider 提供同名工具会在启动时失败，而不是谁先谁后地覆盖。

#### MCP provider

`mcpToolProvider` 的注册方式与其他 provider 一样 —— 在
`agentkit/build/tools.go:52` 的 `defaultToolRegistry` 里 —— 它也遵守
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
  （`agentkit/build/mcptools.go:343`）；同一台 stdio 服务器的调用走 `serialTool`
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
- **起不来的服务器不会拖住装配。** 启动失败之后的拆解要等子进程退出，而一个还在
  装自己依赖的子进程不会因为 stdin 关了就退出 —— 所以 `startStdioClient`
  （`agentkit/build/mcpmanager.go:432`）先杀掉子进程
  （`agentkit/build/mcpmanager.go:485`），拆解也有上限
  （`agentkit/build/mcpmanager.go:501`）。
  ★ 装配等的是一个 server，不是一个 server 的尸体：不杀它，一个起不来的服务器会
  把这一轮一起拖下水，而客户端收到的是「turn start timeout」——
  一个既不点名是哪个 server、也不说为什么的症状。
- **启动是一个后台作业，每台 server 各自有一个状态。** 管理器在后台起那些服务器
  （`Warm`，`agentkit/build/mcpmanager.go:123`），每个条目带四态之一
  （`mcpState`，`agentkit/build/mcpmanager.go:297`）：`not_started`、`starting`、
  `ready`、`failed`。★ `Tools` 从不等 —— 作业在飞时它答 `errMCPStarting`，
  而 provider 跳过那台 server 而且**不告警**：正在起不等于坏了。
  ★ 一台 server 带来的工具在**下一轮**进来：作业拿落地前后的清单比，
  变了就推进代际（`runStart`，`agentkit/build/mcpmanager.go:450`）。
  ★ 失败的启动会重试，但不会早于文件自己的 `timeout:`
  （`mcpStartTimeout`，`agentkit/build/mcpmanager.go:328`）——
  起不来的 server 不能被拉成热循环。
- **工具列表在一轮来要时重新列出**，条件是上一次列出的时间超过 30 秒
  （`agentkit/build/mcpmanager.go:36`，`mcpRelistInterval`）。
  ★ 这个间隔是写死的值，不是配置项：没人动的旋钮就是没人测的旋钮，而服务器
  的工具列表是以「天」为尺度变化的。
- **列表变了就推一代。** `Refresh`（`agentkit/build/mcpmanager.go:118`）报告列表
  是否变化，builder 通过与配置重载器同一个钩子推一代
  （`agentkit/build/builder.go:67`）。这次重列发生在该轮 agent 装配**之前**
  （`agentkit/build/builder.go:105`），因为这一批工具每代只建一次 —— 把重列放进
  构建里就永远不会跑到了。

### 3.2 加一个新的配置分区

比如新增 `mcps/` 目录：

★ 这个目录不再只是假设 —— `docs/mcp-design.md` 设计了它（本仓库的设计文档只有中文），
  而且这个分区已经在树上。下面这份配方就是它走的路子。

1. **`agentkit/spec/config.go`**：加目录常量 `DirMCP = "mcps"`，并在
   `Categories` 里加一项（含 `ID`、`Label`、`Dir`、`Ext`、`Editable`）。**热重载目录**和
   **非热重载目录**要分清楚 —— 改一个目录的语义（热不热）影响用户预期。

2. **`agentkit/spec/load.go`**：加 `loaderFor` 的分支（`agentkit/spec/config.go:544` 的
   `loaderFor`），并实现 `loadXXX`。

   ★ **`skills/` 之前没有的两种形状。** 一个分区可以**不被加载**：`loader` 留 nil，加载循环跳过它
   —— 那是给「文件由配置层之外的东西读」的分区准备的（技能由 skill backend 读 `SKILL.md`），这时
   `Categories` 是**表面**（清单、路径规则、编辑器），而不是加载器的清单。一个分区也可以是
   **嵌套的**（`Category.Nested`）：`skills/<name>/SKILL.md`，一个实体一个文件夹，文件夹名就是
   实体的名字。★ 深度不是随便给的：别的分区写深一层会被 `ValidateReadPath` 拒掉，因为加载器只读
   一层，两层下的文件会被保存、然后永远不被加载。

3. **`agentkit/spec/decl/decl.go`**：加纯声明结构体。

4. **热重载自动生效**：`agentkit/spec/config.go:114` 的 `hotDirList()` 是从
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
- **装载**由 `loadMCP`（`agentkit/spec/load.go:329`）完成：一个文件，一个服务器。
- ★ **这个分区的「热」意味着客户端会被重建。** 声明就是服务器的身份，所以声明变了
  就是服务器变了：旧客户端被拆掉，重新起一个新的
  （`agentkit/build/mcpmanager.go:215`），依据是那份声明的指纹
  （`agentkit/build/mcpmanager.go:330`）。★ 一个资源没有跟着重建的热分区，就是在
  撒谎说自己热。
- **写入闸门**拒绝一个读不回来的名字：`validateFileName`
  （`agentkit/spec/write.go:166`）只接受 `<server>.yaml` 且服务器名合法
  （`agentkit/spec/write.go:185`）—— 文件名就是服务器名，而服务器名就是这台服务器
  工具的命名空间段。
- **脚手架**是 `Skeleton`（`agentkit/spec/write.go:280`），其中 `mcps/` 那一支
  （`agentkit/spec/write.go:307`）写出的是一份**能加载**的文件：★ `mcps/` 下的文件
  **定义**一台 server，所以一份解析不了的文件不是「server 关着」，而是「配置加载不了」。
  可选的那些字段写成注释，而 `command` 是一个没装的占位符 —— 于是 server 起不来，
  日志里说一句，agent 照跑。
- ★ **脚手架是一个文件，而开头那个下划线就是「这不是要装的东西」的意思。**
  `agentkit/templates/files/mcps/_example.yaml` 是配置编辑器给出的骨架
  （`agentkit/spec/write.go`），而 `openheron init` 写的是它旁边的三台服务器 ——
  `mcps/fetch.yaml`、`mcps/time.yaml` 和 `mcps/deepwiki.yaml`。★ 编辑器由 API 供给，
  而 API 不能 import `initcfg` —— 那是循环 —— 所以文件住在一个**两边都能依赖的叶子包**
  `agentkit/templates` 里。★ 拷贝会漂移。

### 3.3 加一条 API 路由

1. **注册**。路由注册集中在 `api/v2_files.go:34-65`。**注册顺序有意义**：
   `/v2` 路由必须**先于** catch-all `GET /*` 注册，否则会被 SPA 回退吞掉。

2. **错误信封**。用 `api/v2_types.go` 的 `v2Fail`，不要自己拼 JSON：
   ```json
   {"error": "...", "code": "...", "retryable": false}
   ```

3. **数据来源**。要接新数据源，在 `api.Server` 上加一个 `SetXxxSource` 方法
   （模式见 `api/server.go:113` 的 `SetSkillsSource`），在 `main.go` 里接线。
   **不要**让 handler 直接去读全局变量。

4. **流式响应**。SSE 出口在 `transport` 层，`api` 只负责把事件写出去。
   事件类型不要新造 —— 用 AG-UI 已有的类型。

### 3.4 加一个 Agent / 改拓扑

改 `agents/*.md` 就够了，不需要动代码。但要注意：

- `model` **必填**，不继承。★ 但**没人声明的模型**不再是启动错误：那个 Agent 被**留在
  树外**，其余配置照常加载（`docs/config-design.md` §4.6）。默认 Agent 也一样 —— 改好之前
  没有会话能跑，但进程起得来，而改它的编辑器就住在进程里。
- `subagents` 引用的 Agent 必须存在，且不能成环 —— 两者都是**启动错误**。★ 被留在树外的
  子 Agent 会从它的父 Agent 的 `subagents:` 里被删掉（`agentkit/spec/load.go:565`），于是
  委派工具永远不会提供一个跑不起来的工；而 `subagents:` 里写着这样一个 Agent 既不算成环
  也不算悬空引用 —— settle 之后的树里已经没有它了。
- 深度超过 `max_depth` 是**启动错误**，不静默截断。

★ **写入门是「留在树外」的另一半。** `agentkit/spec/write.go:472` 的 `checkNoAgentLost`
拿**磁盘上 settle 后的树**和**试装后 settle 的树**比，拒绝掉任何会把一个能跑的 Agent 带出
树外的写、批写或删除。没有它，把某个 Agent 的模型改成一个不存在的，它就会一声不吭地消失 ——
那正是装载期那条规则不再负责的那种故障。★ 不要用「比较原始文件集合」来简化它：本来就坏掉的
Agent 在两边都不在，把它算成「被弄丢」会让任何一次无关的编辑都失败。

★ **删除门跑在试装之前。** `checkNotReferenced`（`agentkit/spec/write.go:706`）读的是
**现在**这棵树，于是它能拒绝一个「还有东西指着它」的删除 —— 而拒绝的话点名**是谁在指**
与要移除什么。★ 它跑在试装之前，是因为试装的装载错误正是它要取代的症状：那个错误点名
被删掉的文件，把真正的原因藏在别处。★ 用户要动手改的是引用它的那一处，不是那个被删的文件。

★ **有意删掉的 Agent 不算「掉出树」**（`agentsLostBy` 的 `removed` 参数）—— 写入门问的是
「本来能跑的 Agent 是不是不再能跑了」，而用户主动删掉一个 Agent 是另一件事；不分开的话，
任何 Agent 文件都删不掉。

装配逻辑在 `agentkit/build/assemble.go`，校验逻辑在 `agentkit/spec/load.go`。

#### ★ Agent 装不起来时，那一轮必须被告知

`agentkit/run/loop.go:125` 的 `makePrepareAgent` 是向 builder 要这一轮的 agent 的地方 ——
也是配置问题变成运行期问题的地方。★ 这个错误必须交给那一轮（`turn.fail`）：框架会记下它、
停下循环，可没有人告诉那一轮「你失败了」，于是等待方一直等到**启动超时**，客户端拿到的是
「turn start timeout」—— 一个既不点名文件也不点名的症状。

这个失败有自己的码（`agent_unavailable`，`transport/transport.go:43`）与装配器自己的原话
（`agentkit/run/errors.go:57` 的 `agentUnavailable`）：哨兵只负责分类，不做前缀，所以用户只读
一遍原因。★ 同一条规矩也适用于 `makeGenInput` 里的输入装配 —— 框架记下、却没人交给那一轮
的错误，就是客户端永远看不到的错误。

#### Agent 身上的三张清单

`tools:`、`skills:` 与 `mcps:` 都是**白名单**，而且**三张默认都是一个都没有** ——
「不写」与「写空」是一回事，而它们的意思是「什么都没有」。

| 键 | 它点名什么 | 不写表示 |
|---|---|---|
| `tools` | 这个 agent 可用的**非 MCP 工具** —— **暴露出来**的名字，模型看见的那些 | 没有工具 |
| `skills` | 这个 agent 可加载的技能 —— `skills/` 下的文件夹 | 没有技能 |
| `mcps` | 这个 agent **挂载**的 MCP server —— 每一个都会起一个子进程，而挂载一个就是**把它的全部工具交给这个 agent** | 没有 server |

★ **不要写 `disabled_tools`。** 它与 `tools` 的含义**正好相反** —— 一个删工具，另一个
点名工具 —— 所以装载器**拒绝**这个文件（`agentkit/spec/load.go:99`），而不是把键改个名：
只改名字就等于把黑名单悄悄变成了白名单。那条消息会说清两者的方向是反的。★ **没有
别名**：一个仍按旧义工作的别名会让黑名单继续活着，一个按新义工作的别名则等于替用户把
黑名单翻成白名单。

★ **工具集是 snapshot 的，而清单负责收窄它。** snapshot 的工具是整个 snapshot 共用的一套；
每个 agent 的 `tools:` 清单移除这个 agent 不许用的东西
（`agentkit/build/topology.go:72` 的 `specTable`），模型永远看不见被清单移除的工具。
★ **这里没有「没事可做」的捷径**（`agentkit/build/policy.go:145`）：**空**清单意味着
**全部**移除。

★ **MCP 只有一个键，而挂载就是授予。** `mcps:` 说哪些 server 被**挂载** ——
而挂载一个就是把这个 agent **该服务器的全部工具**给它。★ `tools:` 不点名 MCP 工具：
写进去的 `mcp__<server>__*` 不授予任何东西，会被丢弃，并以一条**提示**报出
（`agentkit/spec/load.go:836` 的 `checkAgentMCPNames`）—— 不是装载期错误，因为那是语义
变了，不是自相矛盾。★ **代价是真实的**：再也不能只收窄某一个 agent 的 MCP 工具 ——
收窄的地方是 server 自己的文件，而那个过滤器是所有挂载它的 agent 共享的。
★ **审批不受影响**：MCP 工具的档位来自那个 server 文件的 `allows:`，缺省仍然是 `ask` ——
挂载不等于放行。

#### 三条提示

三条都是**提示**，不是错误 —— 配置是合法的，只是没有说出作者的本意
（`agentkit/spec/load.go:708` 的 `noticeAgentTools`）：

| 提示 | 它意味着什么 | 该怎么办 |
|---|---|---|
| `tools` 点名了一个不存在的工具 | 内置工具就是 `decl.BuiltinToolNames` 里那些，而某个 provider 的工具只有在 `tools/` 下写了它的文件时才存在 | 核对拼写；provider 缺失就去写那个 `tools/` 文件 |
| `tools` 点名了 `skill` 工具，而 `skills` 是空的 | 配置合法，但自相矛盾：agent 能要一个技能，却没有技能可加载 | 在 `skills:` 里点名那个技能，或者把 `skill` 从 `tools:` 里去掉 |
| `tools` 点名了 MCP 工具 | `tools` 不点名 MCP 工具；`mcps:` 才是挂载一个 server 并把它的工具交给这个 agent 的地方 | 删掉那些 `mcp__*` 行，把 server 写进 `mcps:` |

★ **`mcp__*` 名字会被「不存在」这项检查跳过**：server 的工具是运行时才发现的，所以它现在
还没有的名字不算拼错。上面那条 MCP 提示是另一回事 —— 它说的是那一行不做它看起来要做的事。

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
| 落盘 | `agentkit/run/loop.go:303` 的 `archiveCompaction` | 消息被追加到记录里，位置在**本轮消息之前** |
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
| `go` | `task lint-go` + `task test` + `task test-race` | Go |
| `web` | `task lint-web` + `bun run test:unit` | bun |
| `e2e` | `task test-e2e` —— 用它自带的假后端 | bun + chromium |
| `secrets` | gitleaks 扫整个历史（§5.9） | — |
| `install-script` | `task test-install` —— 用本地假发布测安装脚本 | — |
| `release` | `task release`，然后建一份**草稿** GitHub Release | 上面四个检查 |
| `install` | 在我们发布的每个平台上装那份草稿里的产物 | `release` |
| `publish` | 把草稿转成公开的 release | `install` |

★ **发布等检查**（`needs`），所以一个红 commit 上的 tag 什么也发不出去。流水线因此是
**一个文件**：job 没法等另一个 workflow 里的 job，两个文件就意味着检查要跑第二遍。

★ **发布还要过安装闸门。** `install` job 装的就是 `release` job 造出来的那批产物，
在我们要发布的每个平台上都装一遍，全装成了 `publish` 才把 release 公开 —— 一个没人
装得上的二进制，不该先发布出去。★ **失败的那条腿把原因写在注解里**，不只是写在日志
里：job 日志要管理员权限才读得到，注解谁都能从 API 读到。

★ **冒烟不在 CI 里。** `task smoke` 需要一个答得上话的模型，而答得上话的模型在 runner
上不可复现 —— 在那里失败是噪声，不是信号。E2E 才是确定的那一半，也是能给发布当闸门的
那一半。

★ **跑的就是仓库自己的命令。** CI 调的是 `task lint-go` / `task test` /
`task test-race` / `task test-e2e` —— §5.2 列的那些命令 —— 所以配方只在 Taskfile.yml 里
有一份。`task lint-go` 与 `task lint-web` 是 `task lint` 的两半，拆开是为了让每个 job
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
`task test-install` 用一个本地假发布测安装脚本：下载、校验和这道闸（含一个被破坏的
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

★ **版本号只住在一处：Taskfile.yml 里的 `KIT_VERSION`。** 其它地方要么从那里拿，
要么根本不写版本号：

| 位置 | 版本号从哪来 |
|---|---|
| `Taskfile.yml` 的 `KIT_VERSION: …` | **源头** |
| `sandbox-kit/openheron/openheron.yaml` 的 `args.version.default` | 构建时盖进去（对暂存副本 `sed`） |
| 发布出来的镜像 tag | `{{.KIT_VERSION}}` |
| 指南（`sandbox-kit/TUTORIAL.md`） | 它写的是 `latest`，CI 把它指向那次发布 |

★ **所以一次发版就是一个只动两行的 PR。** 开一个 PR，改 Taskfile.yml 里的
`KIT_VERSION` 与描述文件里的 `args.version.default`，别的都不用动：指南写的是
`latest`，而 CI 从你打的 tag 推导版本。

```sh
# the whole contract of a release pull request — $NEXT is the version you tag
sed -i "s/^  KIT_VERSION: .*/  KIT_VERSION: $NEXT/" Taskfile.yml
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

### 3.10 ★ 往消息上挂东西

消息带的比正文多：一个 id、一个压缩标记、一份附件清单。它们全都挂在
`message.Extra` 上，而有**四条规则**不到踩过坑是不知道的。

**1. `Extra` 里的值必须在 gob 的白名单上。** 写 checkpoint 时 `Extra` 走 gob
编码，而 gob 编不了 interface 里没注册过的类型。所以一个
`[]message.Attachment` 会在 **stop 的时候**让 checkpoint 写失败 —— 离当初写下
它的那段代码看起来没问题已经很久了 —— 并留下一份写坏的审批。白名单是
`string` / `int64` / `int` / `bool` / `float64` / `[]byte`。

★ **所以结构化的值存成 JSON 字节**，而读的那一侧要认得它可能遇到的每一种形态：
本包写的字节、JSON 往返之后变成的 **base64 字符串**（JSON 把 `[]byte` 编成
字符串 —— 而 transcript 就是 JSON），以及调用方可能直接塞进去的结构体切片。

**2. 盖章只写一次。** `message.put` 是只写一次的，这是故意的：一条消息的身份在
它出生时就定了，而它在管道里被再盖一次章时，留下的应该是它出生时带的那些。

**3. 模型要看见 transcript 存着的那个对象。** 这一轮的用户消息是**先 build 后
落盘**（`agentkit/run/loop.go`）：`buildInput` 收到的是消息对象本身，
`sess.Append` 在它成功之后才发生。★ 另一个次序有个洞 —— `Append` 失败只记日志
不中断，于是落盘失败会让模型完全看不到那个问题。同一个次序也是附件清单能到
达模型的原因：它盖在那一条消息上，而模型拿到的就是那一条消息。

★ **消息有自己的 id**（`message/identity.go`）。`api/v2_convert.go` 兜底用的
派生 id 把内容与消息的**位置**混在一起，所以压缩标记插在它前面就会给它重新
编号 —— 而前端是拿这个 id 给消息做 key 的，于是每次压缩都会重建整棵消息树。
兜底是它能安全上线的原因：消息带 id 之前写的 transcript 仍然拿得到一个能用的
id，而且派生方式与从前一模一样。

★ **一条消息不能同时带正文和多模态 part。** 线上只有一个 `content` 字段：
它要么是字符串，要么是列表。openai 适配器既把 `inMsg.Content` 填进
`Content`，又用 part 拼出 `MultiContent`，于是两者都满的消息在**发出去之前**
就被客户端拒了 ——

```
can't use both Content and MultiContent properties simultaneously
```

★ 所以正文要挪进一个 **text part**，`Content` 清空。而这种情况下给模型看的是
一条**副本**：transcript 里那条留着正文，而且永远不带图片的 base64（transcript
是 8 MiB 行上限的 JSONL 日志，2 MiB 的图 base64 之后是 2.7 MiB）。
★ **这个坑只有在真模型上才暴露** —— 假后端不序列化，Go 用例也碰不到适配器。
它属于 `task smoke`，不属于 CI。

**4. 形状在 wire 边界定下来，不在装配层。** 上面那条规则管的是**这一轮**装配
出来的消息。历史是持久数据，它带着写下它时的形状：在装配学会挪正文之前写下的
会话里，存着一条**同时有 `Content` 和图片 part** 的消息 —— 它每次被加载都会撞上
上面那条规则。★ 会话不能改写自己的历史，所以修复只能发生在**出去的路上**，
而且要对**每一条**消息做，不能只对这一轮产生的那一条。

`llm/common/shape.go`（`normalizeWireShape`）在 `Generate` / `Stream` 里、
请求交给适配器之前把它定下来：正文挪进一个 **text part**，`Content` 清空，
而废弃的 `MultiContent` 被**清空而不是合并**。★ part 是更丰富的形状 —— 它装得下
图片，而一张没有正文的图片，模型认不出它讲的是什么事。原消息不动：只有发出去
的那份副本变了，而副本与原消息共用 `Extra`，于是 id 与附件清单照旧跟着走。

★ 同一个边界上还有一条规则。一轮只输出了思维链就结束 —— 思考预算耗尽，
或流被中途切断 —— 会以空 `Content`、没有 `tool_calls`、只有 `ReasoningContent`
的形状写进 transcript。客户端把 `content` 声明成 `omitempty`，于是这条消息在线上
既没有 `content` 也没有 `tool_calls`，端点会拒绝**整个请求**
（"content or tool_calls must be set"）。★ 所以 `normalizeWireShape` 还会调用
`withVisibleAssistantContent`：把 `noVisibleResponseText` 放进副本的 `Content`，
**如实说明发生了什么**而不是编造一个回答；transcript 与思维链都不动。带
`tool_calls` 的 assistant 消息本来就合法，原样放过。

### 3.11 ★ 上传的文件落在哪里

上传会在会话的 workspace 里变成一个文件，而**两个决定就在那一刻做出**：
压不压，叫什么。

**名字里带一段哈希。** 文件存成 `<original name>-<8 hex characters><extension>`，
其中哈希是原名、落盘大小与修改时间的 `sha256`，用 `0x00` 分隔。

★ **为什么**：同名传两次不能互相覆盖。覆盖会让记录说谎 —— 第一条消息的附件清单
指着的那个文件，已经是第二次的了。★ 哈希负责唯一，而原名留在前面，人还是
认得出这个文件。

★ **这个哈希可以复算。** 读文件、stat、把三个输入哈希一遍，名字就对上了。
★ 这是因为修改时间被**钉住**了：`os.Chtimes` 把它设成上传那一刻，于是哈希里的
时间与文件上的时间是同一个值。★ 时间取毫秒而不是纳秒 —— FAT32 的粒度是两秒，
纳秒级的哈希在它上面复算不出来。

★ **原名记在会话的旁边**，在 transcript 旁边的 `uploads.json` 里 ——
**不在 workspace 里**，因为 workspace 是模型会读的那个目录。列举端点用它显示
用户输入时的名字；没有它，界面上列出来的就是一堆哈希。

**图片在服务端被压缩。** `agentkit/imaging` 干的这件事：`Compress` 把长边缩到
1600 像素，并且**用同一种格式**重新编码 —— PNG 仍是 PNG，透明也留着。
★ 放在服务端是因为收益在那里：进请求的 base64 是从压过的字节编出来的。

★ **有三样东西是故意不动的。** **GIF** 原样存 —— 解码只取第一帧，重新编码就
把动画扔了。**WebP** 原样存 —— Go 里没有 WebP 编码器。**JPEG 的 EXIF 方向**在
缩放**之前**被应用 —— Go 的 JPEG 编码器不写 EXIF，手机照片会歪；方向用
`github.com/rwcarlsen/goexif` 读出来，图片先摆正。

**字节由一个端点送出**：`GET /v2/sessions/:id/files/raw`，它把名字解析到
workspace 里，然后用四种形态之一回答：

| 种类 | 回答 |
|---|---|
| 图片 | 它自己的 image 类型，inline；`?max=<pixels>` 在送出的路上缩放 |
| PDF | `application/pdf`，inline |
| 文本 | **`text/plain; charset=utf-8`**，inline |
| 其它 | `application/octet-stream`，作为下载 |

★ **文本那一条最要紧。** 一个叫 `.html` 或 `.svg` 的文件可以带着脚本，而浏览器
把它当文档渲染 —— 或者当图片渲染 —— 都会**在这个源上**执行那个脚本。★ 所以
服务端**从不解释内容**：它把字节交出去，并且报一个意思是「这是字符」的类型。
界面自己渲染 Markdown 与代码，所以这样并不吃亏。★ `image/svg+xml` 因此在这里
不算图片，`agentkit/imaging` 出于同样的理由把 SVG 当文本。

★ 唯一的边界是 workspace：路径由会话解析，越界的名字被拒。

### 3.12 ★ 上传的文件怎么到模型手里

★ **非图片的文本文件不内联。** 模型看到的那条用户消息，开头是每个文件一行 `@`，
写的是**绝对**路径：

```text
@"<workspace>/notes-a1b2.md"

what does it say?
```

★ 路径必须是绝对的：`agentkit/fsx/fsx.go` 把相对 `path` 按项目根解析，而不是
按会话的 workspace —— workspace 相对的名字会让 `read_file` 去读另一个文件。
路径里带空白时加引号，上下文提示词会让模型调用时把引号去掉。

★ 这个消息头是一个**视图**，不是改写。transcript 保留用户自己的话，以及
`Extra`（`openheron_attachments`）里那个 workspace 相对的 `path`；界面用
`UserText` 和 metadata 渲染气泡与附件 chip。历史消息也会重建这个消息头，所以
隔一轮再问「刚才那个文件」，模型仍然叫得出文件名。

**图片仍然是 image part。** `inlineImage` 在请求发出时才读字节，上限由
`maxInlineImageBytes` 管着。超限的图片，以及二进制附件（PDF、压缩包），会在
提示词里被列为「暂时不能处理」—— 它们**不**拿 `@` 路径，因为对非文本字节用
`read_file` 只会读出噪声。

★ **没有降级链。** `llm/common/model.go` 一次请求发完就结束。端点拒绝图片时，
这一轮带着它自己的错误结束，由用户换模型或换说法重试；`llm/common/files.go`
与 `llm/common/vision.go` 连同 file 块与重试路径一起删掉了。

★ 上下文提示词（`agentkit/run/input.go`）把读文件写成义务：每个 `@` 路径都
`MUST call read_file`；文件大时用 `offset` 和 `limit` 分段读 —— 这个工具一次
最多返回 2000 行。模型没读文件时由用户再提示一次，没有自动的第二次尝试。

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
- `task build` 会先构建前端再构建 Go。

### 4.6 ★ 版式的几个 token 与主题

★ **对话的宽度是一个 token,不是同一个数字抄六遍。** `web/src/index.css` 里定义三个：

| token | 是什么 |
|---|---|
| `--chat-column` | 对话所在的那一列（64rem） |
| `--prose-column` | 里面的**文字**停在哪（46rem） |
| `--chat-gutter` | 这一列两侧的留白 |

★ 两个宽度**故意不一样**：列要宽，宽表与宽代码才看得全；而六十个字符长的一段话是难读的，
所以正文停得更早。**块级内容**吃满整列，只有文字被按住 —— 这也是为什么这个上限挂在
文字元素上（`markdown.tsx` 的 `PROSE`），而不是挂在消息气泡上。

★ 表格有**两个**上限，各回答一个问题：每列 **7rem 的下限**让列多的表撑出去、在自己的框里
横向滚动；每格 **28rem 的上限**让长句在格子里换行，而不是把表格撑宽。
★ 没有上限的表格，宽度等于最长单元格；没有下限的列，会被挤成一条缝。

★ **主题**在**首屏之前**就定了 —— 由 `web/index.html` 里的一段脚本决定，
并且只从一处读：`web/src/lib/theme.ts`。需要主题的组件用 `useSyncExternalStore` 订阅；
设置里的 CodeMirror 主题也跟着走，所以编辑开着时切换主题会重绘它。
★ CodeMirror 那个编辑器特意给了 `theme="none"`：库的默认是 `theme="light"`，
它会把编辑器刷成**白色**，而那条规则在样式表里排在我们的**后面** —— 所以它赢了，
于是暗色下成了白字白底。编辑器改成吃应用自己的 token。
★ 浅色与暗色是**同一套 token 的两组取值**，一边有的颜色另一边也有 ——
组件里写死的 `sky-500` 是 bug：状态色是 `--info`、`--warning` 与 `--success`。

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
| `task test` | Go 全量（`go test ./...`） | — |
| `task test-race` | 同一套在竞态检测器下跑（`go test -race -count=1 ./...`） | — |
| `cd web && bun run test:unit` | 纯函数层（reducer / 分帧 / store / 高亮） | — |
| `task test-e2e` | 真实浏览器 + 确定性假后端 | 无（自带假后端） |
| `task smoke` | 真后端联调：SSE 分帧、历史回放、审批接线 | **测试实例在跑，且模型可达** |
| `task lint` | Go vet + gofmt + oxlint + tsc | — |
| `task lint-go` / `task lint-web` | `task lint` 的两半，给只装了一种工具链的 job 用 | — |
| `task check` | `lint` + `test`，提交前跑这个 | — |
| `task install-hooks` | 装 pre-commit 钩子（gitleaks + gofmt） | `pre-commit` |

起测试实例：

```bash
task test-env-backend   # 前台起，:18099，独立配置 + 独立会话目录
task test-env-reset     # 只重建测试目录，不启动
task test-env-sync      # ★ 改过开发侧的 agent 定义后必须跑
```

★ **`task test-env-sync` 是必须的，不是可选的** —— 测试实例里的 `agents/ llm/ tools/ skills/`
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
cd web && bun run smoke:config             # config_write 的审批往返（见下面第（八）条）
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

★ **竞态检测器也要跑**：`task test-race`（`go test -race ./...`）。普通套件是绿的而
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
**22 个测试文件、284 个用例**，覆盖 `sse.ts` / `events.ts` / `store/` / `highlight.ts` /
`thinking.ts` / `turns.ts` / `approval.ts` / `settings-store.ts` 等层。

★ **客户端一条配置规则都不留了。** 它从前留了一整套：分区表的副本、tools/ 的文件白名单、
MCP server 名的正则，和一整份逐键扫描 —— 那是在校验还同步的年代写的。服务端一直都拿着
行号（yaml.v3 会报），所以那份副本什么也没挣到，只挣到了漂移，而且已经漂了：它写 `corpus`，
loader 写的是 `corpus_dir`。`settings-validate.ts` 现在只剩字符串整形与渲染：
`issueLine`、`suggestPath`、`resolveTemplate`。

★ **换来的是「带落款的往返」。** 一次编辑在打字停下 250 ms 后发一个问题
（`web/src/lib/settings-store.ts:240` `scheduleValidate`），保存则立刻再问一次并取消挂起的
那个。每个回包都带落款（`:229` `validateSeq`）：不再是最新的、或不再是选中的那个文件的，
直接丢掉 —— 否则两张答案赛跑，赢的是慢的那张，也就是关于用户已经改过的文本的那张。

★ **有一条规则是「搬到服务端」而不是「删掉」。** agent 的 `reasoning` 档位现在在
`agentkit/spec/load.go:668` 的 `checkReasoning` 里，拿模型的词汇表检查 —— 检查的是
**每一个**文件，不只是在这个编辑器里改过的那些。
★ 删一条客户端规则，只有在服务端已经有它的时候才叫简化；下一条规则该先问这一句。

★ `store.test.ts` 测的是**整个 zustand store**，所以它在跑之前会
stub 掉 `globalThis.localStorage` 和 `globalThis.fetch`（`store.test.ts:35-84`）。
Bun 本身没有 `localStorage`，不 stub 会炸。

★ 事件回放与历史水合**必须收敛到同一个状态** —— `store-parity.test.ts` 专门钉这一点，
外加一块「已知差异，钉住别动」的断言。

### 5.6 E2E

`task test-e2e` → `playwright test`，**15 个 spec、194 个用例**：
`agents` `attachments` `chat` `compaction` `composer-autocomplete` `composer-meta` `history` `mcps`
`motion` `multi-agent` `reattach` `scroll` `session-busy` `settings` `theme`。

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
与真实后端的协议层验证由 `task smoke` 负责，两者互补。

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

**（八）`smoke:config` 需要一份不可能作为出厂默认的配置。**
`config_write` 刻意不在任何模板里 —— 把改自己配置的那支笔交给 agent 是个决定，不是默认
—— 所以出厂就没有哪个 agent 能调它，这一轮的冒烟会没有任何东西可触发。因此脚本
**先检查自己的前置条件**，缺了就带着该补的那一行以 `2` 退出，而不是看起来像「抖」：

```yaml
# .openheron/agents/main.md
tools: [..., config_write]
```

然后 `task test-env-sync`。★ 当 `config_write` **不在** `ask` 档时它也拒绝运行：
allow 档的 `config_write` 会在没有审批的情况下写好文件，而那正是这条冒烟存在要抓的东西。
这也是它叫 `task smoke-config`、而不进 `task smoke` 的原因 —— 出厂安装上它永远不可能通过。

★ 它会把树放回去：探针文件最后通过 API 删掉，并断言它确实没了。留下一个 agent 文件的
冒烟会改变下一次跑的东西。

**（九）改界面必须浏览器验证。** 「我改了，测试也绿了」不构成验收依据 ——
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

装钩子用 `task install-hooks`（需要 `pre-commit` —— `pipx install pre-commit
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
| 启动时报 `disabled_tools` was renamed to `tools` | ★ 两者含义**正好相反** —— 一个删工具，另一个点名工具。删掉 `disabled_tools`，在 `tools:` 里列出这个 agent 该有的工具（`agentkit/spec/load.go:99`） |
| 提示说 `tools` 点名了 MCP 工具 | ★ `tools` 不点名 MCP 工具，而 `mcps:` 才是挂载一个 server 并把它的工具交给它的地方：删掉那些 `mcp__*` 行，把 server 写进 `mcps:`（`agentkit/spec/load.go:836`） |
| Agent 调不动某个 MCP 工具 | 那个 server 在这个 agent 的 `mcps:` 里吗？挂载**就是**授予 —— 这个 agent 的 `tools:` 清单与它无关（`agentkit/spec/decl/decl.go:148`） |
| 提示说 `tools` 点名了一个不存在的工具 | 内置工具就是 `decl.BuiltinToolNames` 里那些（`agentkit/spec/decl/decl.go:347`）；某个 provider 的工具只有在 `tools/` 下写了它的文件时才存在 |
| 提示说 `tools` 点名了 `skill` 工具，而 `skills` 是空的 | 配置合法但自相矛盾：在 `skills:` 里点名那个技能，或者把 `skill` 从 `tools:` 里去掉 |
| Agent 调不动某工具 | `GET /v2/agents/:agentId` 看生效工具表；再读工具的 `reason` —— `tools`，或者默认的 `ask` 档（它会问） |
| 某个 agent **一个**工具都没有 | ★ 工具清单是白名单，而且**默认一个都没有**：不写 `tools:` 就是没有工具。列出这个 agent 该有的工具 |
| 残留的 `policy/` 目录好像还在管你的旧档位 | 这个分区退役了：档位住在提供工具的那些文件里，而默认档位是 `ask`。启动日志里会说明 —— 那条提示落在 `deferred` 里 |
| `reasoning` 像是没用 | 模型档案里声明这个档位了吗？没声明 = 静默忽略 |
| 编辑器把**别的**文件的问题算在你正在改的这个文件头上 | ★ 每条 issue 都点明它说的是哪个文件（`Issue.Path`），别的文件的问题显示在**那个**文件的名字下面，而且是个能跳过去的链接。★ 但整棵树要装得起来：哪儿坏了一个文件，这次保存照样被拒，所以先修那个文件 |
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
| **活跃流的用量没有按 Agent 分账** | `contract.Usage` 没有 `byAgent` 字段；前端 `events.ts:552` 的 `toUsage` 只映射扁平计数 | `usage.byAgent` **只在历史水合时才有**。所以新开的一轮里，Agent 过滤条提示里的累计 token 数是空的 —— 刷新一下（拉历史）才会出现。**上下文占用**是实时的（§3.7）：它走自己的 `context_usage` 事件。E2E 之所以没发现这个缺口，是因为它测的是**带种子的历史线程**。 |
| **SSE 断了不会自动重连** | `web/src/lib/api.ts` | 流断掉就是断掉：走 `onError` → 错误面板，用户得自己重发。没有重连、没有断点续传。 |
| **冲突对话框不显示磁盘上的版本** | `web/src/components/settings/conflict-dialog.tsx:18-23` | 它只显示你那个陈旧的 `baseRevision` 加一句写死的「changed by someone else」，**没有真去取磁盘版本**。也没有「强行覆盖」按钮 —— 「Keep editing」只是清掉冲突标记，下次保存会带着同样的陈旧 revision 再冲突一次。 |
| **四个运行时依赖被放在 `devDependencies`** | `web/package.json` | `lucide-react`（10 个文件引）、`clsx`、`tailwind-merge`、`class-variance-authority` 都被 `src/` 引用并打进 `web/dist`。构建能过（Vite 在构建期解析），但清单是错的。 |
| **`api/server.go:112` 的注释指向不存在的路由** | 该行 | 注释写的是「GET /v2/spec」，但 `SetSkillsSource` 实际喂的是 `/v2/skills`。 |
| **残留的 `policy/` 目录被丢弃了 —— 而配置照样加载** | 装载器只走五个分区（`agentkit/spec/config.go:533`）；退役的目录只被「注意到」，不会被读（`noteRetiredDirs`，`agentkit/spec/config.go:410`） | `policy/` 分区在 2026-11-07 退役了：档位住在提供工具的那些文件里，而默认档位是 `ask`。从前留下的 `policy/permissions.yaml` **不会被读** —— 以前被禁的工具现在会问，以前不用点头就过的工具现在也会问。★ 那条提示落在 `deferred` 里（启动日志与 `/v2/health` 都带着它），并说清新档位在哪儿；配置编辑器直接拒绝这个路径（「unknown config section」，`agentkit/spec/write.go:72`）。★ **空的** `policy/` 目录不报任何东西 —— 它里面本来就没有被丢掉的东西。 |

## 8. 提交前

```bash
task check        # lint + test
```

- `gofmt -l .` 必须为空。
- 运行时输出（日志、错误、警告、界面文案、写入用户配置的文件内容）**一律英文**。
- 代码注释用英文，写「为什么」不写「是什么」，别把框架语义当契约。
- 测试用例名与测试夹具数据**保持中文**（那是描述性文本，不是运行时输出）。
- 新增/删除用例要同步文档里的条数。
