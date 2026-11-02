# <img src="web/public/favicon.svg" alt="openheron" width="72" height="72" /> openheron

**一个把「专家 Agent」当工具使的多 Agent 运行时**：每个专家 Agent 在配置里绑定适合自己的
大模型，父 Agent 只做编排与合成 —— token 花在真正需要它的地方。

- **专家配专模** —— 每个 agent 在 `agents/*.md` 里声明自己的 `model:`，简单检索不为不需要的
  推理能力付费。
- **工具即隔离** —— 专家 Agent 作为**有界能力**被调用：它自己的工具循环与中间推理留在它
  的上下文里，父 Agent 只拿到它写的那份结构化总结。
- **结构即节省** —— 省 token 不是靠提示词压缩，是靠边界让不该跨界的内容不跨界。

「谁能干什么」变成几条硬规则：

- **工具权限分两层，永不合并** —— 外层 `policy/permissions.yaml` 定全局 `ask`/`deny`，
  内层每个 agent 用自己的 `disabled_tools` 收窄。外层的 `deny` 优先，
  **子 agent 放松不了它**。
- **委派有深度上限** —— 入口 agent 是第 0 层，深度 `d` 的 agent 当且仅当 `d < max_depth`
  才能再生子 agent；拓扑超限**启动期就报错**，不静默截断。
- **一个会话能同时跑多个 agent** —— 互不依赖的活并行分派。
- **配置是文件** —— 模型档案、提示词、审批策略都是能读能改的文件。
- **一个二进制，扔哪都能跑** —— 前端嵌进单文件，跨平台静态编译。

★ 边界先说清楚：省 token 在**可分解、工具输出冗长**的任务上最显著 —— 顺序依赖强的任务，
单 Agent 反而可能更快更准；「高性能」指**单位 token 的有效产出**，不承诺零延迟或无限并发。

> English version: [README.md](README.md)

## 快速开始

```bash
make build                           # 构建（前端 + 含界面的单文件二进制）
cd /your/project && openheron init   # 在你的项目里生成最小可运行配置
export DEEPSEEK_API_KEY=sk-...       # 密钥只从环境变量读
openheron                            # 启动，打开 http://localhost:8080
```

★ **或者一条命令装上发布出来的二进制** —— Linux、macOS、Windows 都一样：

```bash
# Linux / macOS
curl -fsSL https://github.com/ouranoshong/openheron/releases/latest/download/install.sh | sh
```

```powershell
# Windows
irm https://github.com/ouranoshong/openheron/releases/latest/download/install.ps1 | iex
```

安装脚本会挑出对应你平台的产物，用发布页的 `checksums.txt` 校验 sha256，装进
`~/.local/bin`（用 `INSTALL_DIR` 换位置）。钉版本用 `OPENHERON_VERSION=v0.1.0`。
想先看一眼再执行，就从[发布页](https://github.com/ouranoshong/openheron/releases/latest)
把 `install.sh` 下载下来读一遍。

`openheron init` 写六个文件：`agents/main.md`、`agents/task.md`、`llm/<provider>.yaml`、
`policy/permissions.yaml`、`policy/limits.yaml` 和 `server/config.yaml`。已存在的文件
**保留不覆盖**（除非加 `--force`），所以在一个已经配好的项目里跑它是安全的。

`tools/` 默认**不写** —— 文件存在即启用，用哪些工具得由你来定；加 `--tools` 才会写
`tools/http.yaml`、`tools/web_search.yaml`、`tools/wikipedia.yaml` 和 `tools/kb.yaml`。

## 命令行

| 命令 | 作用 |
|---|---|
| `openheron [--config-dir <path>] [--project-root <path>]` | 启动服务 |
| `openheron init [--dir] [--model] [--provider] [--base-url] [--env] [--no-policy] [--tools] [--force] [--dry-run] [--json]` | 生成最小可运行配置 |
| `openheron kb index [--config-dir] [--json] [files...]` | 给知识库建索引 |
| `openheron kb search [--config-dir] <query>` | 检索知识库 |
| `openheron kb status [--config-dir] [--json]` | 看索引现状 |
| `openheron version` | 打印版本号 |

服务端的监听地址、目录等都能用环境变量覆盖：`LISTEN_ADDR`、`DATA_DIR`、`PROJECT_ROOT`、
`FRONTEND_MODE`、`STATIC_DIR`。

## 配置在哪

**只有三级，不向上查找**：

```
--config-dir  →  $OPENHERON_CONFIG_DIR  →  <project-root>/.openheron
```

默认那一级不存在就创建；前两级不创建 —— 打错字不该静默长出一棵空配置树。

| 目录 | 内容 | 热重载 |
|---|---|---|
| `agents/` | 每个 agent 一个 `.md`：frontmatter + 正文（正文就是提示词） | ✅ |
| `llm/` | 每个厂商一个 `.yaml`：端点、密钥、模型清单、思考档位表 | ✅ |
| `policy/` | `permissions.yaml`（审批档位）、`limits.yaml`（配额上限） | ✅ |
| `tools/` | 外部工具与知识库配置 —— **文件存在即启用** | ✅ |
| `skills/` | `<name>/SKILL.md`，eino 原生 skill 约定 | ❌ |
| `server/` | 进程自身参数：监听地址、目录、超时、深度上限 | ❌ 需重启 |
| `state/` | 会话与 checkpoint（自动创建，**不要提交**） | — |

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
`description`、`model`、`max_iterations`、`subagents`、`disabled_tools`、`skills`、
`temperature`、`max_tokens`、`reasoning`。

- `model` 必填，格式 `<provider>/<model>`，**不继承、不回退** —— 解析不了就在启动时报错。
- `subagents` 声明拓扑；引用的 agent 必须存在，且**不能成环**。
- `skills` 是这个 agent 能用的技能白名单，名字就是 `skills/` 下的文件夹名。
  **不写就是全部技能**，写空列表就是**一个都没有**。
- `reasoning` 写的是**档位名**（如 `high`），由 `llm/*.yaml` 的 `models[].reasoning` 表翻译成
  各厂商不同的字段。**这张表不填，档位就被静默忽略** —— agent 写着 `reasoning: high` 而
  端点没有 `high`，等于「完全不思考」，且没有任何报错。

### 权限两层

| 层 | 位置 | 作用范围 |
|---|---|---|
| 外层 | `policy/permissions.yaml` 的 `deny` | 所有 agent |
| 内层 | `agents/*.md` 的 `disabled_tools` | 只影响这一个 agent |

两层**分开存储、从不合并** —— 合并就丢掉了「是哪一层拦的」这个信息。外层 `deny` 优先，
是子 agent 唯一放松不了的保证。

未被点名的工具默认 `allow`（直接放行）。加进 `ask` 就要人每次点头 —— 那一轮会先中断、
等决定；加进 `deny` 就是彻底禁用。

### 深度上限

`server/config.yaml` 的 `agent.max_depth`（默认 `1`，即只有入口 agent 能委派）与
`policy/limits.yaml` 的同名项**取更严的**。声明的拓扑超过上限是**启动错误**，不静默截断。

## HTTP 接口

只暴露 `/v2` 接口族（旧的无前缀接口已下线）。**没有认证、没有 CORS** —— 默认监听
`:8080` 会绑全部网卡，要收窄就显式配 `listen_addr`。

| 方法 | 路径 | 作用 |
|---|---|---|
| POST | `/v2/sessions` | 建会话（返回 `threadId`） |
| POST | `/v2/run` | **SSE 入口**：提问，或回复审批 |
| GET | `/v2/sessions/:id/messages` | 回放历史 + 用量 |
| POST | `/v2/sessions/:id/abort` | 中断在跑的 turn |
| GET | `/v2/agents` `/v2/agents/:agentId` | agent 及其生效工具表 |
| GET | `/v2/config` `/v2/health` `/v2/runtime` | 生效配置 / 健康 / 运行时状态 |
| GET/POST/PUT/DELETE | `/v2/settings/item` | 配置编辑器的读写 |

完整清单、请求体与事件类型见 [docs/user-manual_zh.md](docs/user-manual_zh.md) §9。
`POST /v2/run` 必须带 `threadId`（先 `POST /v2/sessions` 拿到）。

★ **一个会话同时只跑一轮。** 对已经在跑的会话再发一个 `POST /v2/run`，会得到 `409`，
错误码是 `session_busy` —— 正在跑的那一轮不受影响，它的东西也不会被丢掉。

## 知识库

`kb` 是一个**可选**的本地语料检索层：一批 markdown 文档，索引存在 `<data-dir>/kb/<index>/`。
在 `tools/kb.yaml` 里配置（**不配 `corpus` 就是关闭**），用 `openheron kb index` 建索引，
agent 用 `kb_search` 检索。`kb_index` 默认落在 `ask` 档 —— 让 agent 自己重建索引需要人点头。

## 开发

```bash
make build-go   # 只构建后端（不重建前端，用于快速迭代 Go 代码）
make web-dev    # Vite dev server（:5173，代理 /v2 到 :8080）
make dev        # Go 自动重建重启（air）
make check      # lint + test
```

`make test-e2e` 用真实浏览器打确定性假后端；`make smoke` 跑真后端，需要一个连得上的模型。
端口、测试环境与其中的坑见 [docs/developer-manual_zh.md](docs/developer-manual_zh.md) §5。

## 关联文档

本仓库自带七组文档，随代码面一起维护，每组中英一式两份
（**英文是主版本，`_zh` 后缀是中文版**）：

| 文档 | 内容 |
|---|---|
| [docs/architecture.md](docs/architecture.md) · [中文](docs/architecture_zh.md) | **架构设计说明** —— 分层与依赖方向、配置系统、装配、运行循环、存储、HTTP、前端 |
| [docs/user-manual.md](docs/user-manual.md) · [中文](docs/user-manual_zh.md) | **用户使用说明** —— 安装、配置、界面操作 |
| [docs/developer-manual.md](docs/developer-manual.md) · [中文](docs/developer-manual_zh.md) | **开发者说明** —— 分层、依赖铁律、开发配方、测试与排障 |
| [docs/context-usage-design.md](docs/context-usage-design.md) · [中文](docs/context-usage-design_zh.md) | **上下文使用预估设计方案** —— 占用口径、数据流，以及压缩后的对话如何持久化（已实现） |
| [docs/session-concurrency-design.md](docs/session-concurrency-design.md) · [中文](docs/session-concurrency-design_zh.md) | **会话并发设计方案** —— 「一个会话一轮」的规则，以及前端怎么知道会话正忙（已实现） |
| [docs/run-lifecycle-design.md](docs/run-lifecycle-design.md) · [中文](docs/run-lifecycle-design_zh.md) | **run 生命周期设计方案** —— run 怎么才能变成一行持久事实、配上租约与 fence（提案：只有 §1 讲的是代码现状） |
| [docs/fs-backend-design.md](docs/fs-backend-design.md) · [中文](docs/fs-backend-design_zh.md) | **文件系统后端设计方案** —— `agentkit/fsx` 这个分叉，与每个工具都要经过的路径收敛（已实现） |

★ `docs/` 下更细的分册设计与行业对照在**上游仓库**里 —— 机制拆解与取舍见上游的
`docs/positioning.md`。

本仓库另有两份约定文档：**[AGENTS.md](AGENTS.md)**（给在这儿工作的人与 AI 的
项目约定，中文版是 **[AGENTS_zh.md](AGENTS_zh.md)**）与
**[.openheron-example/README.md](.openheron-example/README.md)**
（出厂配置样例的用法）。
