# <img src="web/public/favicon.svg" alt="openheron" width="72" height="72" /> openheron

**一个把「专家 Agent」当工具使的多 Agent 运行时**：每个专家 Agent 在配置里绑定适合自己的
大模型，父 Agent 只做编排与合成 —— token 花在真正需要它的地方。

三条机制拿到「省 token」：

- **专家配专模** —— 每个 agent 在 `agents/*.md` 里声明自己的 `model:`，简单检索不为不需要的
  推理能力付费，复杂任务也不将就小模型。
- **工具即隔离** —— 专家 Agent 作为**有界能力**被调用：它自己的工具循环与中间推理留在它
  的上下文里，父 Agent 只拿到它写的那份结构化总结。
- **结构即节省** —— 省 token 不是靠提示词压缩，是靠边界让不该跨界的内容不跨界。

同时把「谁能干什么」变成几条硬规则：

- **工具权限分两层，永不合并** —— 外层 `policy/permissions.yaml` 定全局 `ask`/`deny`，
  内层每个 agent 用自己的 `disabled_tools` 收窄。外层的 `deny` 优先于内层，
  **子 agent 放松不了它**。
- **委派有深度上限** —— 入口 agent 是第 0 层，深度 `d` 的 agent 当且仅当 `d < max_depth`
  才能再生子 agent；拓扑超限**启动期就报错**，不静默截断。
- **一个会话能同时跑多个 agent** —— 互不依赖的活并行分派；用量**按 agent 分账**。
- **配置是文件** —— 模型档案、提示词、审批策略都是能读能改的文件。
- **一个二进制，扔哪都能跑** —— 前端嵌进单文件，跨平台静态编译。

★ 边界先说清楚，比事后解释便宜：

- 隔离是**完全的**：专家的中间过程**不进父的任何一轮上下文**；而**可观测性同样是完全的** ——
  专家的思考与工具调用实时出现在界面上（带归属），也留在记录里。
- 省 token 在**可分解、工具输出冗长**的任务上最显著；顺序依赖强的任务，单 Agent
  反而可能更快更准。
- 「高性能」指**单位 token 的有效产出**，不承诺零延迟或无限并发。
- 专家配专模要**配对才划算**：出厂配置里两个 agent 共用一个模型。配错模型比不配更浪费。

★ 本仓库是**代码面**：Go 后端 + React 前端 + 构建配置，随附六组文档
（`docs/architecture.md`、`docs/user-manual.md`、`docs/developer-manual.md`、
`docs/context-usage-design.md`、`docs/session-concurrency-design.md`，各带一份 `_zh`
中文版）。
前三组讲的是代码**现在是什么样**；后两组是**已经落进代码**的设计 —— 上下文使用预估
（占用仪表盘与压缩结果的持久化）与会话并发（一个会话一轮的闸门与跨客户端的 409）。
更细的分册设计与行业对照在**上游仓库**的 `docs/` 里 ——
机制拆解与取舍见上游的 `docs/positioning.md`。

> English version: [README.md](README.md)

## 快速开始

```bash
make build                       # 构建（前端 + 含界面的单文件二进制）
cd /your/project && openheron init   # 在你的项目里生成最小可运行配置
export DEEPSEEK_API_KEY=sk-...  # 密钥只从环境变量读
openheron                            # 启动，打开 http://localhost:8080
```

`openheron init` 写四个文件：`agents/main.md`、`agents/task.md`、`llm/<provider>.yaml`、
`policy/permissions.yaml`。已存在的文件**保留不覆盖**（除非加 `--force`），
所以在一个已经配好的项目里跑它是安全的。

## 命令行

| 命令 | 作用 |
|---|---|
| `openheron [--config-dir <path>] [--project-root <path>]` | 启动服务 |
| `openheron init [--dir] [--model] [--provider] [--base-url] [--env] [--no-policy] [--force] [--dry-run] [--json]` | 生成最小可运行配置 |
| `openheron kb index [--config-dir] [--json] [files...]` | 给知识库建索引 |
| `openheron kb search [--config-dir] <query>` | 检索知识库 |
| `openheron kb status [--config-dir] [--json]` | 看索引现状（不需要向量化服务在跑） |
| `openheron version` | 打印版本号 |

服务端的监听地址、目录等都能用环境变量覆盖：`LISTEN_ADDR`、`DATA_DIR`、`PROJECT_ROOT`、
`FRONTEND_MODE`、`STATIC_DIR`。

### 配置根怎么定

**只有三级，不向上查找**：

```
--config-dir  →  $OPENHERON_CONFIG_DIR  →  <project-root>/.openheron
```

第三种（默认派生）**不存在就创建**；前两种指向不存在的目录时**不创建** ——
打错字不该静默长出一棵空配置树。这种情况下启动会报 `config: load <root>: ...`，
而不是那句 `openheron init` 提示（那句只给默认根）。

`openheron init` 刻意**不读** `$OPENHERON_CONFIG_DIR`：它写到你指给它的那个目录，
读环境变量会把新配置写到别处。

启动时若默认根下没有 `agents/main.md`，报错收敛成一句：

```
no configuration at /path/to/.openheron: run `openheron init` to create one
```

## 配置长什么样

配置根下的目录按「是否热重载」分成两类：

| 目录 | 内容 | 热重载 |
|---|---|---|
| `agents/` | 每个 agent 一个 `.md`：frontmatter + 正文（正文就是提示词） | ✅ |
| `llm/` | 每个厂商一个 `.yaml`：端点、密钥、模型清单、思考档位表 | ✅ |
| `policy/` | `permissions.yaml`（审批档位）、`limits.yaml`（配额上限） | ✅ |
| `tools/` | 外部工具与知识库配置 —— **文件存在即启用** | ✅ |
| `skills/` | `<name>/SKILL.md`，eino 原生 skill 约定 | ❌ 见下 |
| `server/` | 进程自身参数：监听地址、目录、超时、深度上限 | ❌ 需重启 |
| `state/` | 会话与 checkpoint（自动创建，**不要提交**） | — |

`skills/` 的定位与其它目录不同：它不是「配置根下的一个分区」，而是**独立解析**的 ——
先看 `$EINO_EXT_SKILLS_DIR`，没有才从当前目录**向上**找 `.openheron/skills`。
也就是说它保留了向上探测，而配置根本身不再上溯。

`.openheron-example/` 里有一套完整样例，`.openheron-example/README.md` 逐项讲清楚了每个
字段的坑（密钥必须写成 `${ENV}`、模型必须写成 `<provider>/<model>`、时长必须带单位）。

### agent 定义

```yaml
---
description: "task: the concrete executor. Writes files per the task order…"
model: deepseek/deepseek-flash
max_iterations: 50
reasoning: low
disabled_tools: [kb_index]
---
```

frontmatter 是**严格解码**的 —— 写错一个键就是启动错误，不会被忽略。可用键：
`description`、`model`、`max_iterations`、`subagents`、`disabled_tools`、`temperature`、
`max_tokens`、`reasoning`。

- `model` 必填，格式 `<provider>/<model>`，**不继承、不回退** —— 解析不了就在启动时报错。
- `subagents` 声明拓扑；引用的 agent 必须存在，且**不能成环**。
- `reasoning` 写的是**档位名**（如 `high`），由 `llm/*.yaml` 的 `models[].reasoning` 表翻译成
  各厂商不同的字段。**这张表不填，档位就被静默忽略** —— agent 写着 `reasoning: high` 而
  端点没有 `high`，等于「完全不思考」，且没有任何报错。

### 权限两层的优先级

| 层 | 位置 | 作用范围 |
|---|---|---|
| 外层 | `policy/permissions.yaml` 的 `deny` | 所有 agent |
| 内层 | `agents/*.md` 的 `disabled_tools` | 只影响这一个 agent |

两层**分开存储、从不合并** —— 合并就丢掉了「是哪一层拦的」这个信息。外层 `deny` 优先，
是子 agent 唯一放松不了的保证。

档位只有两列：**未被点名的工具默认 `allow`（直接放行）**。要让某个工具每次都要人点头，
把它加进 `ask`；要彻底禁用，加进 `deny`。

### 深度上限

`server/config.yaml` 的 `agent.max_depth`（默认 `1`，即只有入口 agent 能委派）与
`policy/limits.yaml` 的同名项**取更严的**。声明的拓扑超过上限是**启动错误**，不静默截断。

## HTTP 接口

只暴露 `/v2` 接口族（旧的无前缀接口已下线）。**没有认证、没有 CORS** —— 默认监听
`:8080` 会绑全部网卡，要收窄就显式配 `listen_addr`。

| 方法 | 路径 | 作用 |
|---|---|---|
| POST | `/v2/sessions` | 建会话（返回 `threadId`） |
| GET | `/v2/sessions` | 列会话 |
| GET | `/v2/sessions/:id` | 会话详情（含待审批与历史决策） |
| PATCH | `/v2/sessions/:id` | 改标题 |
| PATCH | `/v2/sessions/:id/agent` | 换会话的 agent |
| DELETE | `/v2/sessions/:id` | 删会话 |
| GET | `/v2/sessions/:id/messages` | 回放历史 + 用量（可 `?agent=` 过滤） |
| POST | `/v2/sessions/:id/abort` | 中断在跑的 turn |
| POST | `/v2/sessions/:id/compact` | 立刻压缩当前对话 |
| POST/GET | `/v2/sessions/:id/files` | 上传 / 列会话工作区文件（单次 32 MiB 上限） |
| GET | `/v2/project/files` | 列项目根的文件（`?q=` `?limit=`） |
| GET | `/v2/commands` | 斜杠命令清单 |
| POST | `/v2/run` | **SSE 入口**：提问，或回复审批 |
| GET | `/v2/skills` `/v2/agents` `/v2/agents/:agentId` | 技能 / agent 及其生效工具表 |
| GET | `/v2/config` `/v2/health` `/v2/runtime` | 生效配置 / 健康 / 运行时状态 |
| GET/POST/PUT/DELETE | `/v2/settings/item` | 配置编辑器的读写（`baseRevision` 做乐观并发） |
| GET | `/v2/settings` | 可编辑项清单 |
| POST | `/v2/settings/validate` | 不落盘地校验一份配置 |

`POST /v2/run` 的请求体：`threadId`（**必填**，先 `POST /v2/sessions` 拿到）、`query`、
以及回复审批时的 `resume[]`（每项带 `interruptId` 与 `payload.approved` / `payload.reason`）。

响应是 AG-UI 事件流，事件类型：`RUN_STARTED`、`TEXT_MESSAGE_START/CONTENT/END`、
`REASONING_*`、`TOOL_CALL_START/ARGS/END/RESULT`、`CUSTOM`（`model_retry` 重试提示）、
`RUN_FINISHED`、`RUN_ERROR`。子 agent 的产出**通过事件上的 `subagentRunId` 归属**，
而不是另开事件类型。

★ **一个会话同时只跑一轮。** 对已经在跑的会话再发一个 `POST /v2/run`，会得到
`409`，错误码是 `session_busy` —— 正在跑的那一轮不受影响，它的东西也不会被丢掉。
见 [docs/session-concurrency-design_zh.md](docs/session-concurrency-design_zh.md)。

### 审批（人在环）

`ask` 档位的工具被调用时**先中断**并把审批请求持久化到会话；客户端拿到审批卡片，
把决定塞回同一个 `POST /v2/run` 的 `resume[]` 里。恢复靠 **checkpoint store** ——
checkpoint 是审批恢复的真相源，框架自己读它决定是恢复还是新开一轮。

审批被拒时工具返回的是一句拒绝说明（带可选理由），**不是**一个错误 ——
模型看到的是「被拒了，理由是 X」，于是可以据此调整。

## 知识库

`kb` 是一个**可选**的本地语料检索层：语料是一批 markdown 文档，索引存在
`<data-dir>/kb/<index>/`（含 `catalog.md` 目录页与派生数据）。

- `tools/kb.yaml` 配 `corpus`（相对项目根）、`index`、`model`（必须是 `kind: embedding`
  的模型）、`batch_size`、`top_k` 等。**`corpus` 不配 = 知识库关闭**。
- `openheron kb index` 建索引（有运行锁：同时只能跑一轮）、`search` 检索、`status` 看现状。
- agent 侧拿到两个工具：`kb_search`（检索，返回文档大纲而非全文）与 `kb_index`（建索引）。
- `kb_index` 默认落在 `ask` 档 —— 让 agent 自己重建索引需要人点头。

## 前端

React 19 + Vite + zustand，构建产物 `web/dist` 通过 `go:embed` 打进二进制。

- `frontend_mode` 默认 `embed`（用嵌进二进制的界面）；`dir` 则从 `static_dir`（相对项目根）
  读磁盘上的构建产物。
- 三层结构：`web/src/lib/sse.ts` / `web/src/lib/events.ts` / `web/src/lib/store.ts`
  是不依赖框架的纯函数层，正确性由 `bun test` 保证；新增逻辑落在这三层里。
- 界面挂在 catch-all `GET /*` 上，SPA 回退到 `index.html`；`/v2` 路由优先注册，
  所以接口路径永远不会被 SPA 吞掉。

## 开发

```bash
make build-go   # 只构建后端（不重建前端，用于快速迭代 Go 代码）
make web-dev    # Vite dev server（:5173，代理 /v2 到 :8080）
make dev        # Go 自动重建重启（air）
make check      # lint + test
```

### 测试

测试环境**端口与状态都隔离**，定义在仓库根的 `.env.test`（已提交）：

| | 开发 | 测试 |
|---|---|---|
| openheron 本体 | :8080 | **:18099** |
| Vite | :5173 | **:15173** |
| E2E 假后端 | — | **:18180** |

```bash
make test-env-backend   # 前台起测试实例（:18099，独立配置 + 独立会话目录）
make test-env-reset     # 只重建测试目录，不启动
make test-env-sync      # ★ 改过开发侧的 agent 定义后必须跑
make test-e2e           # 真实浏览器 + 确定性假后端
cd web && bun run test:unit   # 纯函数层（reducer / 分帧 / store / 高亮）
make smoke              # 真后端联调（SSE 分帧、历史回放、审批接线）
```

改界面**必须浏览器验证** —— 脚本级测试看不见渲染层。E2E 用假后端，不改成真后端：
真后端依赖模型配合，失败时分不清是界面坏了还是模型没听话。

## 关联文档

本仓库自带五组文档，随代码面一起维护，每组中英一式两份
（**英文是主版本，`_zh` 后缀是中文版**）：

| 文档 | 内容 |
|---|---|
| [docs/architecture.md](docs/architecture.md) · [中文](docs/architecture_zh.md) | **架构设计说明** —— 分层与依赖方向、配置系统、装配、运行循环、存储、HTTP、前端、知识库 |
| [docs/user-manual.md](docs/user-manual.md) · [中文](docs/user-manual_zh.md) | **用户使用说明** —— 安装、配置、界面操作 |
| [docs/developer-manual.md](docs/developer-manual.md) · [中文](docs/developer-manual_zh.md) | **开发者说明** —— 分层、依赖铁律、开发配方、测试与排障、已知问题 |
| [docs/context-usage-design.md](docs/context-usage-design.md) · [中文](docs/context-usage-design_zh.md) | **上下文使用预估设计方案** —— 占用口径、数据流、后端与前端改动，以及压缩后的对话如何持久化（占用仪表盘、压缩持久化与压缩标记的渲染已实现） |
| [docs/session-concurrency-design.md](docs/session-concurrency-design.md) · [中文](docs/session-concurrency-design_zh.md) | **会话并发设计方案** —— 为什么第二个界面能插进正在跑的轮次、「一个会话一轮」的规则，以及前端怎么知道会话正忙（闸门已实现） |

`docs/` 下**其余**设计文档在上游仓库里，不随本仓库分发：

| 文档 | 内容 |
|---|---|
| `docs/implementation-audit.md` | **落地审计报告** —— 哪些没做、哪些是文档漂移（2026-10-23） |
| `docs/hygiene-research.md` | **代码卫生调研** —— 无出口的导出符号、第三方归档、死代码（2026-10-23） |
| `docs/agent-architecture-survey.md` | **外部 Agent 架构调研** —— pi / rakazo / OpenClaw / Hermes / Letta 的 gateway 与后台服务问题（2026-10-24） |
| `docs/positioning.md` | **定位与取舍**：专家配专模 × Agent 即工具 —— 机制拆解与边界 |
| `docs/architecture.md` | 总览与分册索引（**同名，但讲的是上游的分册结构**） |
| `docs/config-design.md` | 配置 schema、分层、校验、热更新 |
| `docs/agent-design.md` | Agent 池、审批、深度保护 |
| `docs/multi-agent-design.md` | 多角色、并行委派、按 agent 分账与历史过滤 |
| `docs/api-v2-design.md` | `/v2` 接口族与 AG-UI 事件契约 |
| `docs/frontend-design.md` | React 前端架构与界面 |
| `docs/config-editor-design.md` | 配置编辑器（`/v2/settings` 六条路由 + 界面） |
| `docs/checkpoint-store.md` | Checkpoint Store：审批恢复的真相源 |
| `docs/kb-design.md` | 知识库 |
| `docs/init-design.md` | `openheron init` |
| `docs/llm-common.md` | 厂商协议层 |

本仓库另有两份约定文档：**[AGENTS.md](AGENTS.md)**（给在这儿工作的人与 AI 的
项目约定，中文版是 **[AGENTS_zh.md](AGENTS_zh.md)**）与
**[.openheron-example/README.md](.openheron-example/README.md)**
（出厂配置样例的用法）。
