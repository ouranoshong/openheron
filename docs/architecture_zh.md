# 架构设计说明

> 说明 openheron 的代码怎么组织、为什么这样分层。面向改代码的人；使用者看
> [user-manual.md](user-manual.md)，开发流程看 [developer-manual.md](developer-manual.md)。
> 每个结论都指向具体文件与行号。English version: [architecture.md](architecture.md)

## 1. 认知地图：大脑 / 记忆 / 手脚 / 协调与约束

AI Agent 不是单一模型，而是围绕目标持续运行的一套系统。按能力域分成四块，openheron 的每个
模块都落进其中一块：

| 能力域 | 它回答什么 | openheron 里对应什么 |
|---|---|---|
| **大脑**：模型与规划 | 谁推理、目标怎么拆成步骤 | 每个 Agent 绑定自己的 `model`；`agentkit/build` 装配成有界的子 Agent 树；`agentkit/run` 跑「思考→行动→观察」循环 |
| **记忆**：上下文与知识 | 这一轮看见什么、历史存在哪 | 消息 `Extra` 里的附件清单、`storage/filestore` 的 transcript 与 checkpoint、`kb` 的语料检索 |
| **手脚**：工具与执行 | 能对外做什么 | 文件系统中间件、`execute` 工具、`tools/*.yaml` 的工具、`mcps/` 的 MCP server、`skills/` 的技能 |
| **协调与约束**：编排与安全 | 谁被允许做什么、出错怎么兜底 | 分层依赖、`tools` / `mcps` / `skills` 三张白名单、档位 `allow` / `ask`、深度上限、审批与写入闸门、会话准入、`agentkit/obs` |

一个 HTTP 进程（`api`）读一份文件化配置（`agentkit/spec`），把每个 Agent 装配成有界的子
Agent 树（`agentkit/build`），交给 eino ADK 的运行循环（`agentkit/run`）执行；过程以 AG-UI
事件流（`transport`）推给浏览器（`web/`），状态落在文件存储（`storage/filestore`），审批靠
checkpoint（`approval` + `storage`）恢复。

```
  HTTP /v2/*  ──▶  api  ──▶  agentkit/spec  +  agentkit/build  +  agentkit/run  ──▶  transport  ──▶  web/

  brain: model and planning
  ──────────────────────────────────────────────────────────────────────────
    agentkit/build       assemble each agent into a bounded tree
    agentkit/run         the think-act-observe loop
    agentkit/spec/decl   the declarations: model, tools, mcps, skills

  memory: context and knowledge
  ──────────────────────────────────────────────────────────────────────────
    message              the message and its Extra
    storage/filestore    transcript, checkpoint, the run ledger
    kb                   corpus, index, retrieval

  hands: tools and execution
  ──────────────────────────────────────────────────────────────────────────
    fsx                  the filesystem backend
    agentkit/shell       which shell the execute tool runs commands with
    tools/*.yaml         the external tools
    mcps/                the MCP servers
    skills/              the skills
    agentkit/imaging     image compression and the hashed names

  coordination: orchestration and guardrails
  ──────────────────────────────────────────────────────────────────────────
    api                  routing, SSE, the error envelope
    agentkit/spec        configuration, validation, hot reload
    agentkit/obs         the only log outlet
    approval, the depth cap, session admission, the write gates
```

## 2. 大脑：模型与规划

模型提供通用智能，但本身不等于 Agent。大脑要做三件事：把模型绑定到具体角色，把目标拆成可执行
的步骤，并驱动「思考→行动→观察」的循环，按执行结果决定下一步。

### 2.1 Agent 定义

一个 Agent 是一个 markdown 文件：frontmatter + 正文。frontmatter 严格解码
（`agentkit/spec/load.go:76` 的 `loadAgent`），写错一个键就是启动错误。

模型没人声明**不是**启动错误，而是把那个 Agent 留在树外：`validate()` 会 settle 这棵树
（`agentkit/spec/load.go:672` 的 `leaveOutUndeclaredModels`）—— Agent 从 `Config.Agents` 里
删掉，每个父 Agent 的 `subagents:` 里也删掉，理由留在 `Config.leftOut`
（`agentkit/spec/config.go:165`）。下游（`AgentNames()`、`MCPServersInUse()`、档位表、
装配层）看到的都是 settle 之后的树。默认 Agent 也一样被留在树外。写入门
（`agentkit/spec/write.go:474` 的 `checkNoAgentLost`）比的是 settle 后的 before 与 after：
可以加 Agent、可以放着已坏的不动，不能把一个能跑的拿走。规则见 `docs/config-design.md` §4.6。

frontmatter 可用键（`agentkit/spec/decl/decl.go:79` 的 `decl.Agent`）：

| 键 | yaml tag | 含义 |
|---|---|---|
| `description` | `description` | 这个 Agent 是干什么的（给父 Agent 看） |
| `model` | `model` | `<provider>/<model>`，**不继承、不回退** |
| `max_iterations` | `max_iterations` | 工具循环轮数上限 |
| `subagents` | `subagents` | 可委派的子 Agent 列表 |
| `tools` | `tools` | 这个 Agent 可用的**非 MCP 工具**，用暴露名。不写表示一个都没有（`agentkit/spec/decl/decl.go:113`） |
| `skills` | `skills` | 可加载的技能，`skills/` 下的文件夹。不写表示一个都没有（`agentkit/spec/decl/decl.go:134`） |
| `mcps` | `mcps` | 挂载的 MCP server；挂载即起子进程，并把该 server 全部工具交给这个 Agent。不写表示一个都不挂（`agentkit/spec/decl/decl.go:150`） |
| `temperature` | `temperature` | 采样温度 |
| `max_tokens` | `max_tokens` | 采样上限 |
| `reasoning` | `reasoning` | 思考档位名（`low`/`high`/`max`…） |
| `summarize_at` | `summarize_at` | 该 Agent 的压缩阈值（绝对 token 数 / 百分比 / `off`） |

`decl.Agent` 另有三个 `yaml:"-"` 字段（`ID`、`File`、`Instruction`），由装载器填充，不是
frontmatter 键。正文就是提示词本身（`Instruction`）。

三张清单默认都是空的，且「不写」等于「写空」：列出来的才有，没有「全都要」也没有
「除了这些都要」。它们都是普通切片（`decl.Agent.Tools`、`decl.Agent.Skills`、
`decl.Agent.MCPServers`），不是指针 —— 指针跟着这次改动一起没了，因为「没声明」与
「声明了但是空的」现在是同一件事。

旧键 `disabled_tools` 被**拒绝**而不是改名（`agentkit/spec/load.go:107`）：它与 `tools`
含义正好相反，只改个名就等于把黑名单悄悄变成白名单。也没有别名 —— 那会让黑名单继续活着。

`mcps:` 挂载就是授予。`tools:` 不点名 MCP 工具：写进去的 `mcp__<server>__*` 不授予任何东西，
会被丢弃并报一条提示（`agentkit/spec/load.go:943` 的 `checkAgentMCPNames`），不是装载期错误。
代价是再也不能只收窄某一个 Agent 的 MCP 工具 —— `mcps/<server>.yaml` 过滤自己的工具，
那个过滤器由所有挂载它的 Agent 共享。审批不受影响：档位来自 server 文件的 `allows:`，
缺省仍是 `ask`。

### 2.2 Agent 装配

`agentkit/build` 把声明变成可运行的 Agent。

#### 装配管线

`agentkit/build/assemble.go` 的 `assemble`：

1. 取 agent 的 `model` 引用，用 `build.Resolve`（`provider.go:71`）解出 `(provider, model)`；
   解不出就是启动错误。
2. 算生效工具表：snapshot 的工具集是整个 snapshot 共用的一套，每个 Agent 用自己的 `tools:`
   白名单收窄（`agentkit/build/topology.go:72` 的 `specTable`），档位表（§5.5）再决定活下来
   的工具里哪些要问人。白名单没有「没有规则就全都留着」的捷径
   （`agentkit/build/policy.go:190`）：空清单意味着全部移除。
3. 按 `subagents` 递归装配子树，把 `SubAgents` 填进 eino ADK 的 `deep.Config`
   （`assemble.go:100`）。
4. `WithoutGeneralSubAgent: true`（`assemble.go:18` 的 `baseConfig`、`hub.go:162`）：关掉 eino
   自带的通用子 Agent，否则模型能凭空造子 agent，绕过拓扑与深度校验。

`assemble.go:158-161` 还有一处：`base.SubAgents = nil` 且
`base.Name/Description/Instruction` 被清空，那是逐节点装配时剥掉继承来的子 agent 列表。

#### ★ 树的身份：它依赖什么，而不是第几代

装配出来的 agent 要能被复用，所以它们在一个**池**里。池的键是**（名字, 指纹）**
（`agentkit/build/pool.go`）：

- **指纹**（`agentkit/build/fingerprint.go`）是这个 agent **自己**依赖的那些东西：它自己的文件、
  它 `model:` 指向的 `llm/<provider>.yaml`、它 `tools:` 点名的那些 `tools/*.yaml`、它 `skills:`
  列出的那些技能、它挂载的 MCP server **当前的实时工具清单**，以及**它子 agent 的指纹**（递归 ——
  父的 `task` 工具描述里嵌着子 agent 的名字与描述）。
  ⇒ 改一个 `skills/<name>/SKILL.md` 只重建**读它的那几个** agent；别人的指纹不动。
  ★ **贡献是「窄」的**：一份文件对这个 agent 什么都没说，就算作没贡献。
- **树的身份**是**（配置对象，MCP 工具面）**（`agentkit/build/snapshot.go` 的 `snapshotFor`）。
  配置换了对象就是新树（重载发布的是新对象）；工具面变了也是新树（工具集是按快照建的 ——
  服务器起来了却没有换树，那棵树里的工具集就是旧的）。
  ⇒ **没有人通知「树变了」，只有比较**：builder 每次建树前把当前的身份和手上那份比一比。
  ★ 通知的路会漏（有人忘了通知），比较的路不会漏。

★ **一轮钉住自己开始时的那棵树**（`agentkit/run/runtime.go` 的 `turn.pin`）：run 的行记下
这棵树的指纹（`storage/runledger.go` 的 `TreeFingerprint`），恢复那一轮**按指纹要 agent**，
而不是按名字要「当前那棵」。
⇒ 被打断的那一轮继续用它开始时的那棵树；**新配置在下一轮生效**。
★ 钉住的那棵树不在池里（进程重启过）⇒ 打一条 warn，退回当前那棵，**不静默换树**。

### 2.3 运行循环

`agentkit/run` 是 eino ADK 的一层封装，做三件事：构造输入、驱动循环、桥接审批。

1. **构造输入**（`agentkit/run/input.go:86`）：系统提示词 + 会话历史 + 本轮 query 组装成
   ADK 输入。子 Agent 的消息不混进父的历史（`input.go:136` 的 `withoutSubagentMessages`）。
2. **驱动循环**（`agentkit/run/loop.go`）：跑 ADK 的 run，把事件转成 AG-UI 事件。
3. **桥接审批**（`agentkit/run/bridge.go`）：工具请求审批时挂起，结果回来后从 checkpoint 恢复。

#### 事件流

事件类型来自 `ag-ui-protocol/ag-ui` 的 Go SDK。帧由 `transport/transport.go` 从 run 的事件
渲染；推导（agent 的事件意味着什么）在 `agentkit/emit`：

| 事件 | 发射点 | 含义 |
|---|---|---|
| `RUN_STARTED` | `transport.go:77` | 本轮开始，带 `threadId` |
| `TEXT_MESSAGE_START/CONTENT/END` | `transport.go:265-282` | 助手正文增量 |
| `REASONING_*` | `transport.go:288-297` | 思考过程 |
| `TOOL_CALL_START/ARGS/END/RESULT` | `transport.go:302-336` | 工具调用 |
| `CUSTOM`（`model_retry`） | `transport.go:246` | 模型重试提示 |
| `RUN_FINISHED` | `transport.go:88`, `:94` | 本轮结束，带结果与用量。★ 被 run 截断的流**不写终态帧**（`RenderStream`），客户端于是重新挂回来，而不是相信一个假的 success（`docs/runtime-design.md` §2.3） |
| `RUN_ERROR` | `transport.go:259`, `api/v2_run.go:283` | 失败 —— 码说明哪一类，消息说明为什么（见 `docs/runtime-design.md` §5.4） |

子 Agent 的产出通过事件上的 `subagentRunId` 归属，不另开事件类型，前端能用同一套 reducer
处理所有 Agent 的输出。

## 3. 记忆：上下文与知识

短期记忆是当前会话的上下文，长期记忆落盘，外部知识靠检索接入。三者都回答同一个问题：这一轮
模型能看见什么。

### 3.1 存储

`storage` 定义接口（`SessionManager` / `Session`），`storage/filestore` 提供实现。磁盘布局
（`storage/filestore/session.go`）：

- `state.json` —— 会话状态（原子重写）
- transcript —— 消息追加（`O_APPEND`）
- checkpoint —— 审批恢复点
- `runs.jsonl` —— run 账本：每次 run 一行，追加写入（`O_APPEND`），保留最后 200 行

内存只是缓存，磁盘才是真相源。会话列表（`List()`）只读 `state.json`，不打开 transcript，
所以列会话是廉价的。

### 3.2 ★ 附件挂在消息上，不是挂在会话上

用户发来的文件绑在**它随的那条消息**上 —— 清单存在消息的 `Extra` 里
（`message/attachment.go`），模型看得见它，是因为 transcript 存的那条消息就是装配输入时用的
那个对象。「这个会话有哪些文件」与「你刚发的那张图」是两个问题，用户问的是第二个。

- `POST /v2/run` 带 `attachments[]` —— 一个名字和一个相对会话 workspace 的路径。服务端自己
  解析（`api/v2_attachments.go`），拒绝越界的名字；越界是拒绝而不是夹取，因为夹取会解析到
  另一个文件。
- 清单以 **JSON 字节**盖在那条用户消息上 —— `Extra` 在写 checkpoint 时走 gob 编码，而 gob
  编不了 interface 里没注册过的类型。
- 装配（`agentkit/run/attachments.go`）按类型分流：**文本**文件在模型副本的消息头变成一行
  `@`，写绝对路径，模型用 `read_file` 去读；**图片**变成多模态 part，模型是看见它。二进制
  文件以及内联不了的图片会在提示词里被列为「暂时不能处理」—— 它们不拿路径，对非文本字节
  用 `read_file` 只会读出噪声。
- **消息头是视图，不是改写**：transcript 保留用户自己的话，以及 `Extra` 里那个相对路径；
  历史消息也会重建消息头，所以隔一轮再问「刚才那个文件」仍然叫得出名字。
- **有图要内联时，给模型的是一条副本**：transcript 里那条留着正文，而且永远不带 base64
  （transcript 是 8 MiB 行上限的 JSONL 日志）；副本的正文挪进一个 **text part** —— 线上只有
  一个 `content` 字段，它要么是字符串要么是列表，而 openai 适配器在一条消息同时带 `Content`
  与多模态 part 时会把两个都填上。
- **一次请求，没有降级**（`llm/common/model.go`）：模型没有视觉时，这一轮带着 provider 的
  错误结束，由用户重试；`llm/common/files.go` 与 `llm/common/vision.go` 已经删掉。上下文
  提示词把 `read_file` 写成义务，文件大时要求用 `offset`、`limit` 分段读。
- `GET /v2/sessions/{id}/messages` 把清单放在消息的 metadata 里带回来，历史里看得出哪个
  文件跟哪个问题一起走的。

删除的粒度是文件，绑定的粒度是消息：`DELETE /v2/sessions/{id}/files?path=` 把文件从
workspace 里拿走；已经随消息发出去的文件留在那条消息上。

### 3.3 知识库

`kb` 是一个可选的本地语料检索层。

- 语料是一批 markdown 文档，目录由 `tools/kb.yaml` 的 `corpus_dir` 指定（相对项目根）。
- 索引落在 `<data-dir>/kb/<index>/`（`build/kb.go:45`），含 `catalog.md` 目录页。
- **检索是混合打分**：`kb/score.go:53` 的 `scoreDoc` 有向量时算
  `0.5*词法分 + 0.5*余弦相似度`；无向量（或维度不符）时纯词法（所以 `kb status` 会报
  `lexical-only retrieval`），词法分为 0 时纯余弦。
- 词法分给标识符加权：出现在标题/小节标题/链接里的词比出现在正文里的词值钱。
- `openheron kb index` 有运行锁（`kb/store.go:146` 的 `StartRun`）：同时只能跑一轮。
- agent 侧拿到两个工具：`kb_search`（返回文档大纲而非全文）与 `kb_index`。`kb_index` 默认
  落在 `ask` 档 —— 让 agent 自己重建索引需要人点头。

## 4. 手脚：工具与执行

工具让 Agent 从「能说」变成「能做」。这一块管工具从哪来、怎么挂到 Agent 上，以及技能怎么被
限定到具体 Agent。

### 4.1 工具来源

| 来源 | 装配位置 | 例子 |
|---|---|---|
| 文件系统中间件 | `agentkit/build/snapshot.go:101` | `ls` `read_file` `write_file` `edit_file` |
| execute 工具 | `agentkit/build/execute.go:40` | `execute` |
| 网络工具 | `build/nettools.go` | `web_search` `http` |
| 知识库 | `build/kb.go` | `kb_search` `kb_index` |
| 百科 | `build/nettools.go:232` | `wikipedia` |
| 配置本身 | `agentkit/build/configtools.go:92` | `config_get` `config_validate` `config_template` `config_notices` `config_tier` `config_write` |
| MCP server | `agentkit/build/mcptools.go:194` | `mcp__<server>__<tool>` |

config 工具总是被挂上，但几乎从不可见：它们进每个 Agent 的批次，Agent 只看见自己 `tools:`
点名的那些，与其它非 MCP 工具一样。其中五个只读，`tools/config.yaml` 点名它们时它们不用点头；
`config_write` 是第六个，同一个文件点名它就像点名别的工具一样（`docs/config-design.md` §6.2）。

一个工具的档位由提供它的那个文件决定：文件自己的 `allows:` 点名不用问的工具
（`tools/http.yaml` 点 `request_get`，`tools/kb.yaml` 点 `kb_search`），该文件提供的其余
一切都问。没有自己文件的内置工具由代码点名（`agentkit/build/tools.go:95`）：读取类
（`ls` `read_file` `glob` `grep`）与委派类（`task` `write_todos` `skill`）。MCP server 用
**它自己的词汇**说这件事，不是暴露出来的名字。对外网络工具不用改代码：配置缺失的 provider
什么都不挂，写下 `tools/*.yaml` 才是把工具挂上去的那一步。文件系统中间件是例外 ——
`fsx.New` 无条件挂载（`agentkit/build/snapshot.go:101`）；知识库由它的 **corpus** 开关，
而不是由文件开关。`openheron init` 会写 `tools/` 下的工具配置，因为出厂 agent 会点名
`request_get`、`duckduckgo_search` 和 `wikipedia_search`；`--no-tools` 可以留下不写。

工具的来路都一样：

`tools/*.yaml` → `spec.loadTool`（`agentkit/spec/load.go:250`）→ `decl.ToolsCfg` →
`toolRegistry.Build`（`agentkit/build/tools.go:152`）→ `snapshot.buildHeavy`
（`agentkit/build/snapshot.go:84`）→ `applyToolConfig`（`agentkit/build/assemble.go:30`）→
`deep.Config.ToolsConfig.Tools` → `deep.New`（`agentkit/build/assemble.go:43`）。

MCP 这一支在注册表处并进来：`mcps/<server>.yaml` → `spec.loadMCP`
（`agentkit/spec/load.go:470`）→ `decl.MCPServerCfg` → MCP provider
（`agentkit/build/mcptools.go:194`），注册在 `agentkit/build/tools.go:55` → 同一个
`toolRegistry.Build` → 同一个 `applyToolConfig`。

重依赖（fsx 后端 + 工具批次）在 `sync.Once` 下只构建一次
（`agentkit/build/snapshot.go:52`）。MCP 客户端是这条路径上唯一不属于 snapshot 的资源：
provider 只回答「这个 snapshot 有哪些工具」并交回一批工具，背后的客户端由 manager 拥有
（`agentkit/build/mcpmanager.go:44`），因为 stdio server 是子进程，下一轮还得在。provider 是
可选的 —— 连不上的 server 只跳过并告警，绝不报错。启动失败后先杀掉子进程再拆解，拆解有
上限（`agentkit/build/mcpmanager.go:840-856` 的 `killMCPChild` / `closeMCPClient`、
`agentkit/build/mcpmanager.go:864`），不会因为一个还在装依赖的子进程卡住装配。

启动不站在那一轮的关键路径上：配置一被读到，所有挂载的 server 就在后台起起来
（`agentkit/build/mcpmanager.go:98` `Warm`），每台各有状态 —— `not_started`、`starting`、
`ready` 或 `failed`。还没起来的 server 不贡献工具，那一轮照跑；起来之后工具面变了，builder
比出来了，下一轮就有它的工具。

暴露名是 `mcp__<server>__<tool>`，只在一处拼装（`agentkit/build/mcptools.go:141`）；server
自己的名字就是命名空间那一段，暴露名属于哪台 server 是从它读回来的
（`agentkit/spec/decl/decl.go:630` 的 `MCPServerOf`）。命名空间不可协商：server 名里不许
出现连续的下划线，所以一个 server 的前缀伸不到另一个 server 去。

### 4.2 skills 的解析是另一套

`skills/` 是配置层 `Categories` 里的**嵌套分区**（`skills/<name>/SKILL.md`），但它的 `loader`
为 nil：清单、路径规则与编辑器归 `spec`，内容由 `build` 的 skill backend 独立解析
（`agentkit/build/skill.go:190` 的 `ResolveSkillsDir`）—— 先看 `$EINO_EXT_SKILLS_DIR`，没有才
从当前目录**向上**找 `.openheron/skills`。这与「配置根不向上查找」是两条不同的规则。

编辑器写的是 `<配置根>/skills/`，那个目录只有在探针正好落在那里时才是生效的那一个；不是的
话保存会落进一棵没人读的树。所以解析结果在启动时被推进配置层
（`spec.Service.SetSkillsDir`，`agentkit/spec/config.go:355`），指向别处时保存被拒
（`agentkit/spec/write.go:151` 的 `validateWritable`），清单里那个分区标成只读并点名真正的
目录。这条规则只住在 `build` 里，在 `spec` 里再写一份就是第二个答案。

一个 Agent 能用哪些技能，由 `agents/<name>.md` 的 `skills` 键限定：它是 skills 目录下文件夹
的白名单，以 `scopedSkillBackend`（`agentkit/build/skill.go:78`）的形式到达中间件，同时管住
`List`（`agentkit/build/skill.go:153`，技能工具说明从这里渲染，即模型看得见的）与 `Get`
（`agentkit/build/skill.go:167`，模型真正调得动的）。只管一半的话，模型只要记得名字就能调到
自己看不见的技能。`decl.Agent.Skills`（`agentkit/spec/decl/decl.go:134`）是普通切片，默认
一个都没有；清单没点名的技能就不是这个 Agent 的技能，技能工具只有在确实有技能可加载时才
值得点名，两者矛盾时装载器会说出来（§2.1）。技能中间件挂在**入口 agent**
（`agentkit/build/assemble.go`），子 agent 拿不到技能工具。

## 5. 协调与约束：编排与安全

这一块管「谁被允许做什么」：依赖方向、配置怎么加载、档位与审批、深度上限、会话准入，以及对
执行结果的观察与兜底。接口面、前端与系统边界也放在这里 —— 它们横切四块，不属于其中任何一块。
openheron 的篇幅大部分在这里 —— 边界比能力更决定一个 Agent 能不能用。

### 5.1 分层与依赖方向

依赖单向：上层可引下层，下层不引上层。下表是生产代码的 import 图，不含测试文件与第三方包。

| 层 | 包 | 内部依赖 | 职责 |
|---|---|---|---|
| **L0 叶子** | `contract` | 无 | 跨层数据契约（事件、消息、中断） |
| | `message` | 无 | 消息内容与来源（provenance） |
| | `helpers` | 无 | 无状态小工具 |
| | `persist` | 无 | 原子写文件 |
| | `approval` | 无 | 审批决定的表示与校验 |
| | `agentkit/obs` | 无 | **日志装配处**：造 logger（`New`、`ParseLevel`、`Discard`）并持有 sink，没有全局 logger 可回取。子包 `agentkit/obs/hertz`（接管 hertz 日志）、`agentkit/obs/hook`（eino callback）、`agentkit/obs/httplog`（模型请求日志）同样是叶子 |
| | `llm/common` | 无 | 厂商协议层，零 openheron 依赖、零日志代码 |
| | `webui` | 无 | 嵌入式前端资源 |
| | `agentkit/fsx` | 无 | **文件系统后端**：写收敛在项目根、忽略规则、结果上限（见 [tools-design.md](tools-design.md)，只有中文） |
| | `agentkit/shell` | 无 | **哪个 shell 跑命令**：启动时挑一个 POSIX shell（或者 `cmd.exe`），它同时是 `execute` 工具描述里告诉模型的那句话 —— 方言与执行必须是同一个答案 |
| | `agentkit/imaging` | 无 | 上传图片的压缩、哈希文件名与缩略图 |
| | `agentkit/templates` | 无 | **配置模板**：`openheron init` 写下的文件、编辑器给的脚手架。是叶子包，配置层与命令层才能读到同一个文件 |
| **L1** | `agentkit/spec/decl` | 无 | 纯声明结构体（yaml tag 集中处）与两端共用的词汇 —— 内置工具名、MCP 命名空间（§5.1） |
| | `agentkit/emit` | `obs`, `approval`, `contract`, `helpers`, `message` | agent 事件 → run 的事实 |
| | `storage` | `contract` | 会话/消息/待审批的接口 |
| | `kb` | `obs`, `persist` | 知识库：语料、索引、检索、评分 |
| | `agentkit/spec` | `obs`, `decl`, `persist`, `templates` | 配置装载、校验、热重载、写入闸门 |
| **L2** | `agentkit/build` | `fsx`, `obs`, `obs/httplog`, `spec`, `decl`, `approval`, `contract`, `helpers`, `kb`, `llm/common`, `message` | Agent 装配、工具装配、档位表（`agentkit/build/policy.go`）与审批闸门 |
| | `agentkit/run` | `emit`, `obs`, `approval`, `contract`, `decl`, `message`, `storage` | 运行循环、输入构造、事件消费 |
| | `storage/filestore` | `obs`, `contract`, `message`, `persist`, `storage` | 文件存储实现 |
| | `storage/memarbit` | `storage` | 内存租约仲裁器 |
| | `transport` | `contract` | AG-UI 渲染：run 的事件 → 帧 |
| | `llm` | `build`, `spec`, `decl` | 薄封装（目录下只有 `common/` 与一个测试文件） |
| | `initcfg` | `spec`, `persist`, `templates` | `openheron init` 的出厂模板 |
| **L3 入口** | `api` | `build`, `emit`, `imaging`, `obs`, `run`, `spec`, `decl`, `contract`, `message`, `storage`, `transport`, `webui` | HTTP 路由、SSE、错误信封 |

同样的分层，从上到下就是引用方向：

```
  L3   api                      routing · SSE · the error envelope
       │
  L2   agentkit/build           assembly · the tool registry · the tier table
       agentkit/run             the loop · input · events
       storage/filestore        the file store
       storage/memarbit         the lease arbiter
       transport                AG-UI rendering
       llm                      a thin wrapper over llm/common
       initcfg                  the factory templates
       │
  L1   agentkit/spec/decl       the declarations and the shared vocabulary
       agentkit/emit            agent events → the run's facts
       storage                  the session interfaces
       kb                       corpus · index · retrieval
       agentkit/spec            config load · validate · reload · write gates
       │
  L0   contract  message  helpers  persist  approval  agentkit/obs
       llm/common  webui  agentkit/fsx  agentkit/shell  agentkit/imaging  agentkit/templates
```

MCP 路径带进来两个第三方包：`github.com/cloudwego/eino-ext/components/tool/mcp`（eino
适配器）与 `github.com/mark3labs/mcp-go`（协议客户端），都在 `agentkit/build` 里。
`agentkit/run` 引 `agentkit/spec/decl` 只为命名空间常量 —— run 账本要记这一轮碰过哪些 server。
`storage/filestore` 只在组合根 `main.go:35` 被引用，`api` 不直接引它。

#### 两条不能破的依赖铁律

**（一）日志只有一处装配。** `agentkit/obs` 造 logger（`New`、`ParseLevel`、`Discard`）并持有
sink；组合根造出 `*slog.Logger` 再注入下去，每个要记日志的包接收一个 `*slog.Logger` 形参，
零值用 `obs.Discard()`。级别与前缀只在这一处定义，业务包直接调 `log.Printf` 或
`fmt.Fprintln(os.Stderr)` 就会绕开这个开关。格式与 hertz 一致（`agentkit/obs/handler.go`）：

```
2026/10/29 22:28:34.346459 main.go:431: [Info] [main] openheron version=dev
```

请求日志原来在 `llm/common`，已搬到 `agentkit/obs/httplog`（`docs/runtime-design.md` §4.4）；
该叶子包现在没有日志代码。`llm/common` 里任何一处 import openheron 内部包都算回归。
logger 由组合根 `main.installLogger` 围绕共享的 `slog.LevelVar` 建起来，所以
`server/config.yaml` 的 `log_level` 与 `OPENHERON_LOG_LEVEL` 能在 handler 建好之后移动门限。

**（二）配置声明与配置行为分离。** `agentkit/spec/decl` 只放纯结构体和 yaml tag
（`agentkit/spec/decl/decl.go`），不引其它包；`agentkit/spec` 放行为（装载、校验、重载、
写入）。`decl` 因此能在 `build`、`api`、`initcfg` 之间自由传递，不带出依赖链。

内置工具名 `decl.BuiltinToolNames`（`agentkit/spec/decl/decl.go:348`）两端都要读：装载器拿它
核对 Agent 的 `tools:` 列表，装配器拿它拒绝冒用这些名字的 provider
（`reservedToolNames`，`agentkit/build/tools.go:126`）。两端互相不能引，所以清单住在两边之下
的叶子包里；抄一份就是两份会漂移的清单。

#### `agentkit/spec` 为什么**不**引 `kb`

`tools/kb.yaml` 的校验要判断语料路径是否相对、模型是否声明、是否 `kind: embedding`
（`agentkit/spec/load.go:389` 的 `loadKB`、`agentkit/spec/load.go:1041` 的 `validateKB`）。
三个答案都在声明里（`decl.Model.IsEmbedding`、`decl.Model.Dimensions`），所以配置层不引知识库
—— 只有测试文件引（`agentkit/spec/policy_contract_test.go:7`）。`kb` 依赖 `obs` 和
`persist`，`agentkit/spec` 依赖 `obs`、`decl` 与 `persist`，方向仍然单向。

#### `agentkit/build` 为什么引 `llm/common` 而不是 `llm`

`llm` 对 `build.Resolve` 是薄封装，`build` 若反过来引 `llm` 就成环。`build` 直接用
`llm/common` 的协议类型，既避免环，也让协议层保持叶子。现在 `llm/` 下只有 `common/` 与
`provider_test.go`，对 `build`/`spec`/`decl` 的引用只出现在测试里。

#### 组合根装配 logger

logger 只在 `main` 里造一次再传下去（`main.newLogger`，`main.go:238`）：

```
main(): newLogger() -> installLogger() -> hlog.SetLogger(hertzlog.New(log))
      -> callbacks.AppendGlobalHandlers(hook.New(log))
      -> setupConfig(log, level, sink) -> applyLogLevel / applyLogFile
      -> serve(ctx, st, log)
```

`newLogger` 围绕共享的 `slog.LevelVar` 与 `obs.Sink` 建好 logger，**在**配置读出来**之前**：
等级与文件路径由配置决定，而 logger 必须先存在，`setupConfig` 才能打第一行日志。门限与去处
都在 handler 建好之后才动：`applyLogLevel` 移门限（`OPENHERON_LOG_LEVEL` > `log_level` >
`info`），`applyLogFile` 把 sink 改向文件。hertz 与 eino 各持一个进程级注册表，所以
`hlog.SetLogger` 与 `callbacks.AppendGlobalHandlers` 各做一次，都在 `hserver.New` 之前 ——
这是仅有的两处全局注册，其余全是注入。`obs.Sink` 是 handler 背后的 `io.Writer`，所以
`log.file` 能在启动后改向文件（`docs/runtime-design.md` §4.3）；变的是 sink，不是 logger。

### 5.2 配置根只有三级

```
--config-dir  →  $OPENHERON_CONFIG_DIR  →  <project-root>/.openheron
```

实现在 `agentkit/spec/config.go` 的 `NewService`。**不向上查找祖先目录**：否则同一条命令换个
启动目录就读到另一份配置，测试实例也会读到开发配置。只有默认派生的根会被自动创建
（`agentkit/spec/config.go:455` 的 `os.MkdirAll`）；显式 root / env root 指向不存在的路径时
不创建。

★ **第一次启动时写的是整棵树**，不只是那个目录：当配置根是缺省派生的那个、而且里面没有
`agents/main.md` 时，服务端先把出厂树写出来 —— 与 `openheron init` 建的是同一份计划 ——
再去装载配置（`main.go` 里的 `initConfigIfEmpty`）。★ **点名的根永远不会被写**：用户
指过去的空目录是用户的决定，测试实例也靠这一点 —— 它的根是点名的，所以从来不会有人
往它底下写配置。

`spec.NewServiceAt` 是给 `openheron init` 用的另一条入口，只认显式传入的路径，
不读环境变量。

### 5.3 目录分区

| 目录 | 类别常量 | 热重载 | 内容 |
|---|---|---|---|
| `agents/` | `spec.DirAgent` | ✅ | 每个 Agent 一个 `.md` |
| `llm/` | `spec.DirLLM` | ✅ | 每个厂商一个 `.yaml` |
| `tools/` | `spec.DirTools` | ✅ | `http.yaml` / `web_search.yaml` / `wikipedia.yaml` / `kb.yaml` |
| `mcps/` | `spec.DirMCP` | ✅ | 每个 MCP server 一个 `.yaml` |
| `skills/` | `spec.DirSkills` | ✅ | `skills/<name>/SKILL.md`，嵌套分区，独立解析（见 §4.2） |
| `server/` | `spec.DirServer` | ❌ | 进程参数，需重启 |
| `state/` | `spec.DirState` | — | 会话与 checkpoint，自动创建 |

`Categories` 有**六个**分区（`agentkit/spec/config.go:59-104`），其中五个是热分区
（`agents/` `llm/` `mcps/` `skills/` `tools/`，见 `agentkit/spec/reload.go` 的 `HotDirs`），
`server/` 不热（`agentkit/spec/config.go:71`）。`skills/` 的指纹是递归 walk，改 `SKILL.md`
下一轮生效。

**没有 `policy/` 分区**（`agentkit/spec/config.go:619` `noteRetiredDirs`）：它当年承载的
档位模型现在住在工具文件与 Agent 文件里（§5.5）。遗留的 `policy/` 目录不会被读，但会留下
一条提示说清新家在哪儿，不会一声不吭地丢掉配置。`tools/` 下的
文件名是白名单，未知文件直接报错（`agentkit/spec/load.go:308`）。`mcps/` 一个 server 一个
文件（`agentkit/spec/config.go:26` 的 `spec.DirMCP`），文件在定义就在，没有 `enabled` 键，
文件名就是 server 名，也是其工具命名空间那一段。

★ **热分区变了之后不是立刻换树**：**写路径只做标记**，下一轮建树之前把标记取走、重读一次配置、
再把新配置发布出去（`agentkit/build/builder.go` 的 `reloadIfMarked`）。
⇒ 「下一轮用它」是**承诺**，不是与轮询的赛跑 —— 轮询晚一刻钟也会发现同一个改动，
但夹在中间的那一轮会跑旧的树。★ 标记是**缓存不是真相**：进程重启后标记是空的，答案仍然对，
因为真相是磁盘上的内容哈希。

### 5.4 装载的两种模式与「deferred」

`spec.Service.Load()`（`agentkit/spec/config.go:537`）严格校验，失败即启动失败；
`LoadUnchecked()`（`agentkit/spec/config.go:560`）跳过校验。`Config.Deferred()`
（`config.go:203`）收集非致命问题（比如某个 duration 字符串没法解析），以 `deferred` 数组
出现在 `/v2/health` 与 `/v2/config` 里。分界是：会导致运行时行为错乱的是致命的。

### 5.5 档位模型：默认 `ask`，例外被点名

档位只有两个：

| 档位 | 含义 |
|---|---|
| `allow` | 工具直接跑，不需要人点头 |
| `ask` | 工具打断这一轮，等一个人的决定 |

`build.Policy.TierOf`（`agentkit/build/policy.go:55`）按名字查档位：在 allow 表里就是
`allow`，其余一律 `ask`。`decl.Tier`（`agentkit/spec/decl/decl.go:258`）只有 `TierAllow`
与 `TierAsk`。默认是 `ask` —— 猜错的代价不对称，多点一次换来的是一次没人看见的破坏性调用
不被执行；旧的全局清单恰好把默认值放在会伤人的那一侧。

例外由提供这个工具的文件点名：

| 来源 | 它点名什么 | 装配位置 |
|---|---|---|
| 没有自己文件的内置工具 | `ls` `read_file` `glob` `grep` `task` `write_todos` `skill` | `agentkit/build/tools.go:95` `builtinAllowedTools` |
| 工具自己的文件 —— `tools/*.yaml` 与知识库 | 那个文件的 `allows:` | `agentkit/spec/load.go:430` `AllowedTools` |
| MCP server 自己的文件 | 那个 server 的 `allows:`，用它自己的词汇，翻译成暴露出来的名字 | `agentkit/build/mcptools.go:173` `mcpAllowedNames` |

`agentkit/build/spec.go:73` 的 `policyOf` 把三处合成一张表，`Policy.TierOf` 读它。表由配置
装配，所以一个工具的档位在它的 server 起来之前就能回答。★ **合并之后没有摘除**：例外来自三处，
就没有第四处能拿走一个。`config_write` 改的是配置本身，而它和其它工具一样由
`tools/config.yaml` 点名放行 —— 点名一个工具为例外的规则，是一条得有人记住的规则。

档位与可见性是两个问题，分开摆着：「这个工具要不要人点头」是共享的档位表
`Policy.TierOf`；「这个 Agent 能不能用它」是逐 Agent 的 `Table.Blocked`
（`agentkit/build/policy.go:163`），来自它以 `decl.SubAgentSpec` 带在身上的清单。清单没点名
的工具不在工具集里，模型也看不见。被挡住的工具保留自己的档位，并在 `reason` 里说明：
非 MCP 工具是 `tools`，MCP 工具是 server 没挂载（`LayerNotMounted`）
（`agentkit/build/introspect.go:36` 的 `reasonOf`）。`Table.Blocked` 回答 `LayerNotLoaded`
（`agentkit/build/policy.go:251`）或 `LayerNotMounted`（`agentkit/build/policy.go:258`）。

### 5.6 深度上限与拓扑校验

委派深度上限是 `server/config.yaml` 里的 `agent.max_depth`（默认 `1`），
`retry.max_retries` 就在旁边。上限只住在这里 —— `policy/limits.yaml` 那份只能收紧的副本
没有了，而 `server/` 不是热分区（`agentkit/spec/config.go:71`），改上限要重启。

声明的拓扑超过上限是启动错误（`agentkit/spec/load.go:1069` 的 `validateDepth`），不静默截断；
引用不存在的 agent、成环也都是启动错误（`agentkit/spec/load.go:1140` 的
`validateAgentGraph`、`agentkit/spec/load.go:1157` 的 `detectCycle`）。

### 5.7 写入闸门

配置编辑器（`/v2/settings`）的写路径有几道闸门：

- `server/` 不可编辑（`api/v2_settings.go`）：它不热重载，接口直接拒绝。
- 密钥字面量被拒：`api_key must be a reference, not a literal: use ${ENV_VAR} or @file:/path`。
- 乐观并发：带 `baseRevision`，与磁盘上的 revision 不符就报冲突。
- 一次改动不许把一个本来能跑的 Agent 带出树外（`agentkit/spec/write.go:474`
  `checkNoAgentLost`）：比磁盘上 settle 后的树与试装后 settle 的树。本来就在树外的 Agent
  两边都不在，所以一次无关的编辑不会被算成「把它弄丢」。
- **还有东西指着它**的删除被拒（`agentkit/spec/write.go:722` `checkNotReferenced`），拒绝
  点名是谁在指、要移除什么。这道闸门跑在试装之前：试装的装载错误正是它要取代的症状。

### 5.8 工具闸门

`agentkit/build/gate.go` 实现「工具能不能用」：

- 名字在 allow 表里 → 直接跑，闸门原样放行（`agentkit/build/gate.go:67`）。
- 名字不在 → 调用中断，等一个人的决定（流式那条路是 `agentkit/build/gate.go:89`）。
- 这个 Agent 的 `tools:` 没点名的工具压根不在它的工具表里，闸门连看都看不到。

没有 `deny` 这一支：一个 Agent 拿不到的工具不会注册给它，闸门从不需要拒绝谁。人拒绝一次
调用时，工具返回的是一句拒绝说明（带可选理由），**不是**错误 —— 模型看到「被拒了，理由是
X」就可以据此调整。MCP 工具走同一道闸门，按名字查 `TierOf`
（`agentkit/build/policy.go:55`），所以 `mcp__<server>__<tool>` 同样要人点头或直接放行。

### 5.9 审批恢复

审批请求被持久化（`storage` 的 `PendingApproval`），checkpoint store 是恢复的真相源。客户端
把决定塞回同一个 `POST /v2/run` 的 `resume[]` 里（每项带 `interruptId` 与
`payload.approved` / `payload.reason`）。checkpoint 没了就恢复不了，接口返回
`this approval can no longer be resumed: its checkpoint is gone. Send a new message to continue.`
（`api/v2_types.go:131`），而不是静默新开一轮。

### 5.10 会话准入

一个会话至多有一轮活着的轮次。对忙着的会话发第二个 `POST /v2/run` 一律被拒
（`409 session_busy`），绝不接管正在跑的那一轮。规则住在 `agentkit/run`，就在它守护的状态
旁边：

- `ensureIdle`（`agentkit/run/runtime.go`）在有轮次执行**或**有审批在等时，拒绝新查询。
- `ensureNoLiveQuery` 同样拒绝新查询，但放行恢复：恢复续接的是被打断的那一轮。
- `claimTurn` 把「忙不忙」与「接管」放在同一把锁下，两个同时到达的请求不可能都通过。

拒绝不改变任何东西：不删检查点、不清审批、不落消息。run 的终态行由 `runRow.finish`
（`agentkit/run/runrow.go:63`）在消息归档后追加；`v2Run` 等 `WaitArchived()` /
`WaitReleased()`（`api/v2_run.go:258-259`）才收流，所以客户端的「这轮完了」与服务端的
「这个会话空了」不会各说各话。待审批在 **loop 退出时**写，不是在那一轮的回调返回时 ——
框架正是在 loop 退出时写检查点。

前端读服务端的答案，而不是自己的内存：会话的 `state` 来自服务端对 run 账本的扫描，并喂给
composer 的守卫，所以第二个标签页水合出来就是「忙」。run 账本是一次 run 的持久记录，
每次 run 一行，由 run 自己的 goroutine 写（`agentkit/run/runrow.go`），由
`GET /v2/sessions/{id}/runs` 读回来；终态行落在消息落盘之后，单是这个次序就让 busy 标记
不用超时也天然诚实。被崩溃留下的、还开着的 run 会在启动巡检时被收成 `interrupted`。

### 5.11 HTTP 层

`api` 只暴露 `/v2` 接口族（旧的无前缀接口已下线，实测 `/health` 返回 404）。

- 全部 `/v2` 路由在 `api/v2_files.go:40-87` 一处注册。
- 错误信封统一由 `api/v2_types.go` 的 `v2Fail` 产出：
  `{"error": "...", "code": "...", "retryable": false}`。
- `/v2` 路由先注册，catch-all `GET /*` 后注册 —— SPA 回退不会吞掉接口。
- 没有认证、没有 CORS。默认 `:8080` 绑全部网卡，要收窄就显式配 `listen_addr`。
- Agent 视图（`api/v2_introspect.go:118`）把 Agent 的工具白名单报成 `tools` —— 报清单本身，
  不是它移除掉的工具。`skillsRestricted` 没了：它存在只是因为「不写」与「写空」曾经不同。
- `POST /v2/run` 的 `threadId` 必填（`api/v2_run.go:139`）：先 `POST /v2/sessions` 拿会话，
  再发起 run。

### 5.12 前端

React 19 + Vite + zustand，构建产物 `web/dist` 通过 `go:embed` 打进二进制
（`webui/webui.go`）。`frontend_mode` 默认 `embed`（用嵌进二进制的界面）；`dir` 则从
`static_dir`（相对项目根）读磁盘上的构建产物。

界面分三层。`lib/sse.ts`（SSE 分帧）与 `lib/events.ts`（事件解码 + reducer）不依赖任何框架
—— 前者零 import，后者只有 `import type`；`lib/store/` 下的 `state.ts` / `selectors.ts` /
`history.ts` 同样是纯的。`lib/store.ts` 是 barrel，转出的 `store/actions.ts` 拥有 zustand
实例，所以 import 它会一并拉进 zustand、`fetch` 与 `localStorage`。新增逻辑落在纯函数这几层，
正确性由 `bun test` 证明，比开浏览器看快得多。

zustand 的 selector 必须返回引用稳定的值：每次调用造新数组/新对象会让 `Object.is` 永远判定
「变了」，表现为 `Maximum update depth exceeded` 白屏。selector 里不要写 `?? []`；用模块级
常量或 store 里的 selector。

对话的宽度是一个 token，不是同一个数字抄六遍：`--chat-column`（那一列）、`--prose-column`
（里面的文字）与 `--chat-gutter`（两侧留白）住在 `web/src/index.css`。列要宽，宽表才看得全；
正文停得更早。

主题在首屏之前就定了，由 `web/index.html` 里的一段脚本决定，并且只从一处读：
`web/src/lib/theme.ts`。浅色与暗色是同一套 token 的两组取值，一边有的颜色另一边也有。

动效只有 CSS，而且听系统的：时长与缓动是 `web/src/index.css` 里的 token
（`--motion-duration-*`），一条全局的 `prefers-reduced-motion` 规则把所有动画 —— 包括
`tw-animate-css` 自带的那几处 —— 压到 0.01ms。方案在 `docs/ui-design.md`。

品牌标记是生成的，不是画了两遍：`web/public/favicon.svg` 是源，
`web/src/components/brand-mark.generated.tsx` 由 `web/scripts/generate-brand-mark.ts` 写出来，
两者一漂移单测就失败。标记是内联 SVG —— 装在 `img` 里的 SVG 是另一个文档，页面的样式表
选不进去它的部件。

### 5.13 可观测性

`agentkit/obs` 是唯一日志出口，级别与前缀只在这里定义。`agentkit/obs/hook` 提供观测钩子
（依赖 `obs`，不反向依赖）。

### 5.14 边界与不做的事

以下都是当前没有的：

- 没有认证、没有多租户。默认 `:8080` 对全部网卡开放。
- 没有 TLS。要在公网暴露得自己套反向代理。
- 没有分布式。单进程、单二进制；会话在本地磁盘。
- 知识库只支持本地目录语料，不支持远程抓取。
- 旧的无前缀 HTTP 接口已下线，只有 `/v2`。
- `skills/` 仍保留向上探测，与配置根的规则不同（见 §4.2）。
