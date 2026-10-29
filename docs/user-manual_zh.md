# 用户使用说明

> openheron 是一个多 Agent 运行时：每个「专家 Agent」绑定自己的模型和工具权限，
> 你可以像用聊天窗口一样用它，也可以把它的 HTTP 接口接到自己的系统里。
>
> 本文面向**使用者**。想改代码请看 [developer-manual.md](developer-manual.md)，
> 想了解设计取舍请看 [architecture.md](architecture.md)。

> English version: [user-manual.md](user-manual.md)

## 1. 安装

需要 **Go 1.27.1+**（构建用）。

```bash
git clone https://github.com/ouranoshong/openheron
cd openheron
make build
```

`make build` 会构建前端并把界面**嵌进二进制**，产物是 `bin/openheron` —— 一个自包含的可执行
文件，扔到哪台机器上都能跑（目标平台之外的机器用交叉编译）。

只想快速试一下：

```bash
make build-go     # 只构建后端（不含界面），迭代 Go 代码时更快
```

## 2. 第一次使用

```bash
cd /你的项目
openheron init                      # 在当前目录下生成配置
export DEEPSEEK_API_KEY=sk-...     # 密钥只从环境变量读
openheron
```

打开 <http://localhost:8080>。

`openheron init` 会写四个文件：

```
你的项目/
└── .openheron/
    ├── agents/
    │   ├── main.md          # 入口 Agent：理解需求、拆任务、委派、验收
    │   └── task.md          # 执行 Agent：真正动手干活
    ├── llm/
    │   └── deepseek.yaml    # 模型档案
    └── policy/
        └── permissions.yaml # 审批策略
```

★ **已存在的文件不会被覆盖**（除非加 `--force`），所以在一个已经配好的项目里跑它是安全的。

`init` 的其它选项：

| 选项 | 作用 |
|---|---|
| `--dir <path>` | 写到指定目录（默认当前目录） |
| `--provider <name>` | 选模型厂商，默认 `deepseek` |
| `--model <name>` | 选模型，默认 `deepseek-flash` |
| `--base-url <url>` | 厂商端点（**含路径**，从厂商文档抄） |
| `--env <NAME>` | 密钥的环境变量名，默认 `<PROVIDER>_API_KEY` |
| `--no-policy` | 不生成 `permissions.yaml` |
| `--force` | 覆盖已存在的文件 |
| `--dry-run` | 只打印要写什么，不落盘 |
| `--json` | 机器可读输出 |

## 3. 配置根：你的配置在哪

**只有三级，不向上查找**：

```
--config-dir  →  $OPENHERON_CONFIG_DIR  →  <项目根>/.openheron
```

- **默认那一档不存在会帮你创建**。
- 前两档指向不存在的目录时**不会创建** —— 打错字不该静默长出一棵空配置树。
  这种情况下启动会报 `config: load <path>: ...`，告诉你哪里不对。
- 配置根**不依赖「进程从哪启动」**：在子目录里跑 `openheron` 读到的还是同一份配置。

如果默认根下没有 `agents/main.md`，启动会直接告诉你：

```
no configuration at /你的项目/.openheron: run `openheron init` to create one
```

## 4. 配置怎么写

### 4.1 Agent（`agents/<name>.md`）

一个 Agent 是一个 markdown 文件：**frontmatter + 正文**。正文就是给模型看的提示词。

```markdown
---
description: "task: the concrete executor. Writes files per the task order."
model: deepseek/deepseek-flash
max_iterations: 50
reasoning: low
disabled_tools: [kb_index]
---

你是执行 Agent。收到任务后：
1. 先读相关文件
2. 按任务单逐项修改
3. 改完跑一遍测试，把失败项原样贴出来
```

frontmatter 可用键：

| 键 | 说明 |
|---|---|
| `description` | 这个 Agent 干什么用的（父 Agent 靠它决定委派给谁） |
| `model` | `<vendor>/<model>`，**必填**，不继承也不回退 |
| `max_iterations` | 工具循环的轮数上限 |
| `subagents` | 它能委派给谁（见 §5） |
| `disabled_tools` | 对**这个 Agent** 生效的工具禁用表 |
| `temperature` / `max_tokens` | 采样参数 |
| `reasoning` | 思考档位：`off` / `low` / `high` / `max`（名字由厂商档案定义） |

★ **frontmatter 写错一个键就是启动错误**，不会被忽略 —— 这是故意的：
一份静默失效的配置比一份报错的配置难查得多。

★ **`model` 不继承**。父 Agent 用了哪个模型，子 Agent 不会跟着用 —— 每个 Agent 都得自己写。
这正是「专家配专模」的落点。

★ **`reasoning` 档位必须在模型档案里声明过才有效**。没声明的档位会被**静默忽略**：
Agent 写着 `reasoning: high`，而端点没有 `high`，实际效果是「完全不思考」，且没有任何报错。
详见 §4.2。

### 4.2 模型档案（`llm/<vendor>.yaml`）

```yaml
type: openai_compat
base_url: https://api.deepseek.com
api_key: ${DEEPSEEK_API_KEY}
timeout: 5m

models:
  - id: deepseek-flash
    context_window: 131072
    summarize_at: 104858        # 上下文窗口的 80%
    reasoning:
      off:  { reasoning_effort: none, thinking: { type: disabled } }
      low:  { reasoning_effort: low,  thinking: { type: enabled } }
      high: { reasoning_effort: high, thinking: { type: enabled } }
      max:  { reasoning_effort: max,  thinking: { type: enabled } }
    reasoning_default: off
```

| 字段 | 说明 |
|---|---|
| `type` | 协议类型，目前是 `openai_compat` |
| `base_url` | 端点，**含路径**，从厂商文档抄 |
| `api_key` | **环境变量名**，写成 `${NAME}` —— 不能写字面量 |
| `timeout` | 超时，带单位（`5m` / `30m`） |
| `models[].id` | 模型名 |
| `models[].context_window` | 上下文窗口 |
| `models[].summarize_at` | 长对话被**压缩**成摘要的 token 数；`0` 表示永不压缩 |
| `models[].reasoning` | **档位表**：档位名 → 这个端点实际发什么字段 |
| `models[].reasoning_default` | Agent 没写 `reasoning` 时用哪档 |
| `models[].timeout` | 覆盖厂商级超时（思考模型通常要调大） |
| `hint_keys` | 端点的字段名，用于请求被拒时给出准确提示 |
| `extra` | 厂商级方言，透传给所有请求 |

★ **`api_key` 只能写环境变量名**（`${OPENAI_API_KEY}`）或文件引用（`@file:/path`）。
在配置里写字面量密钥，配置编辑器会直接拒绝保存 —— 密钥不该进仓库。

★ **`summarize_at` 是长对话能活下去的关键。** 超过那个 token 数，对话会被一段摘要取代，
从那以后 agent 带着摘要继续走。`0` —— 出厂默认值 —— 表示永不压缩：上下文一路涨，直到厂商
拒绝这个请求。通常取 `context_window` 的 80–90%。

压缩缩短的是**上下文**，不是**记录**。被压缩掉的消息仍留在记录里、仍读得到；摘要就写在它们
旁边。你自己的话不会在切口处丢失 —— 摘要把它们留住了。

★ **`reasoning` 档位表不是可选的**。它是「Agent 写 `reasoning: high`」与
「这个端点怎么表达 high」之间的翻译层。表里没有的档位会被静默忽略，
于是「开了 high 档却不思考」这个坑会以最隐蔽的方式出现。

### 4.3 审批策略（`policy/permissions.yaml`）

```yaml
ask:
  - write_file
  - edit_file
  - execute
  - kb_index
deny: []
```

只有两列，语义很直接：

- **在 `ask` 里**：每次调用都要**你点头**。
- **在 `deny` 里**：**谁都不能用**。
- **不在任何一列里**：默认 `allow`（直接放行）。

★ `deny` 是**全局**的。`agents/*.md` 里的 `disabled_tools` 只影响那一个 Agent，
**放松不了外层的 `deny`**。这是子 Agent 唯一放松不了的保证。

### 4.4 外部工具（`tools/`）

**文件存在即启用**，不用改代码。当前支持四个文件名：

| 文件 | 提供 |
|---|---|
| `http.yaml` | 通用 HTTP 请求工具 |
| `web_search.yaml` | 网页搜索 |
| `wikipedia.yaml` | 百科查询 |
| `kb.yaml` | 知识库（见 §7） |

★ `tools/` 下**只认这四个文件名**，写别的名字会**启动报错**，不会被静默忽略。

★ **`openheron init` 不会生成 `tools/` 下的任何文件** —— 这一整个目录要你自己写。
没写就是没启用：对应的工具不会出现在 agent 的工具表里，模型也看不见它。

### 4.5 进程参数（`server/config.yaml`）

| 字段 | 默认 | 说明 |
|---|---|---|
| `listen_addr` | `:8080` | 监听地址 |
| `data_dir` | `state` | 会话与 checkpoint 的存放目录 |
| `static_dir` | `web/dist` | 前端产物目录（`frontend_mode: dir` 时用） |
| `frontend_mode` | `embed` | `embed` 用嵌进二进制的界面，`dir` 读磁盘 |
| `start_timeout` | `60s` | 启动超时 |
| `agent.max_depth` | `1` | 委派深度上限 |

★ **`server/` 不热重载** —— 改了要重启才生效。所以配置编辑器里它是**只读**的，
提交会报 `section "server" is not editable`。

以下环境变量可以覆盖进程参数，优先级高于配置文件：
`LISTEN_ADDR`、`DATA_DIR`、`PROJECT_ROOT`、`FRONTEND_MODE`、`STATIC_DIR`。

### 4.6 技能（`skills/`）

`skills/<name>/SKILL.md`，按 eino 原生的 skill 约定。

★ skills 的查找**是另一套规则**：先看 `$EINO_EXT_SKILLS_DIR`，
没有才从**当前目录向上**找 `.openheron/skills`。这跟配置根「不向上找」不一样 ——
skills 是目录树，向上找是为了能用你家目录里的技能。

## 5. 多 Agent 与委派

### 5.1 谁能委派给谁

在 Agent 的 frontmatter 里声明：

```markdown
---
model: deepseek/deepseek-flash
subagents: [task]
---
```

这表示「这个 Agent 可以把活派给 `task`」。

★ **引用不存在的 Agent、或者声明成环，都是启动错误**，不会被静默截断。

### 5.2 深度上限

深度上限由 `server/config.yaml` 的 `agent.max_depth` 和 `policy/limits.yaml` 的同名项
共同决定，**取更严的**。默认 `1`，意思是「只有入口 Agent 能委派，子 Agent 不能再生子」。

想跑更深的委派链：

```yaml
# policy/limits.yaml
agent:
  max_depth: 3
```

★ 声明的拓扑超过上限是**启动错误**，不是「截断到上限然后继续跑」。
后者会让深度保护形同虚设。

### 5.3 一次会话能同时跑多个 Agent

互不依赖的活可以并行分派。用量**按 Agent 分账**（`/v2/sessions/{id}/messages` 的
`usage.byAgent`），谁花了多少一目了然。

在界面上：
- **会话列表**左边的 Agent 徽标可以按 Agent 过滤历史。
- 消息里的子 Agent 产出带**归属标记**，能看到是谁做的。

## 6. 界面怎么用

| 元素 | 作用 |
|---|---|
| **侧边栏** | 会话列表、Agent 过滤、状态点（有待审批时会亮） |
| **输入框** | 提问；`/` 开头触发斜杠命令；支持拖拽/粘贴附件 |
| **消息列表** | 助手回复、思考过程、工具调用卡片、子 Agent 产出（带归属） |
| **审批卡** | 需要你点头时出现，同意 / 拒绝（可附理由） |
| **工具卡** | 每个工具调用的参数与结果 |
| **设置** | 配置编辑器的入口 |

### 6.1 斜杠命令

| 命令 | 作用 |
|---|---|
| `/new` | 新建会话 |
| `/stop` | 停掉当前这轮 |
| `/attach` | 往当前会话附加文件 |
| `/settings` | 打开配置编辑器 |
| `/agents` | 切换当前会话的 Agent |
| `/skill` | 为这次任务指定一个技能 |
| `/review` | 复查当前改动或给定路径 |
| `/test` | 跑相关测试并汇总失败项 |
| `/explain` | 解释给定代码 |
| `/fix` | 定位根因并修掉给定问题 |

### 6.2 审批怎么走

当模型要调一个 `ask` 档的工具时：

1. 运行**暂停**，界面出现审批卡片。
2. 你选同意或拒绝；拒绝可以附一句理由。
3. 决定被写回会话，模型收到结果继续。

★ 被拒绝时模型收到的是**一句拒绝说明**（带你的理由），**不是**一个错误。
所以它能据此换个做法，而不是直接崩掉。

★ 审批的恢复点存在会话里。**如果那个恢复点丢了**（比如会话目录被清理过），
界面会明确告诉你「这个审批已经无法恢复，发条新消息继续」，而不是静默新开一轮。

### 6.3 配置编辑器

`/settings` 打开。它能改：`llm/` `agents/` `policy/` `tools/`。

写入有几道闸门：

- `server/` **不可编辑**（不热重载，改了要重启）。
- **密钥字面量被拒** —— `api_key must be a reference, not a literal`。
  要用 `${ENV_VAR}` 或 `@file:/path`。
- **乐观并发**：如果文件在别处被改过，保存会报冲突，让你选覆盖还是放弃。

## 7. 知识库

知识库是**可选**的本地语料检索层：把一批 markdown 文档变成 Agent 能检索的资料。

### 7.1 开启

```yaml
# .openheron/tools/kb.yaml
corpus: docs          # 相对项目根的目录
index: default
model: openai/text-embedding-3-small
top_k: 8
```

- `corpus` **不配就是关闭**。
- `corpus` **必须是相对项目根的路径**。写绝对路径会**启动报错** ——
  索引和语料必须同生共死，放到系统目录里的语料会让索引失去意义。
- `model` 必填，且必须指向一个声明了 `kind: embedding` 的模型，
  否则启动报错（`kb.model ... is not an embedding model`）。

索引落在 `<data_dir>/kb/<index>/`。

### 7.2 命令行

```bash
openheron kb index                 # 建索引
openheron kb index docs/a.md       # 只索引指定文件
openheron kb search "配置根怎么解析"  # 检索
openheron kb status                # 看索引现状
openheron kb status --json         # 机器可读
```

三条命令都支持 `--config-dir <path>`，用来指向别的配置根。

★ `kb index` 有**运行锁**：同一时间只能跑一轮，重复调用会等前一轮结束。

★ `kb status` 显示 `lexical-only retrieval` 表示**没有向量** —— 索引是纯词法的，
检索能用但不如混合检索准。配了 embedding 模型并重新索引后才是混合打分。

### 7.3 Agent 怎么用知识库

Agent 拿到两个工具：

- **`kb_search`**：检索，返回文档**大纲**（标题、摘要、带行号的小节标题、相关链接），
  不是全文。要读某一段，用 `read_file` 加行号。
- **`kb_index`**：建索引。这个默认在 `ask` 档 —— **让 Agent 自己重建索引需要你点头**。

## 8. 命令行速查

```
openheron [--config-dir <path>] [--project-root <path>]   启动服务
openheron init [选项]                                     生成最小可运行配置
openheron kb index [--config-dir] [--json] [文件...]       建索引
openheron kb search [--config-dir] <查询>                  检索
openheron kb status [--config-dir] [--json]                看索引现状
openheron version                                         打印版本号
```

## 9. HTTP 接口速查

服务只暴露 `/v2` 接口族。没有认证、没有 CORS；默认 `:8080` 会绑全部网卡，
要在公网暴露请自己套反向代理，或把 `listen_addr` 收到 `127.0.0.1`。

| 方法 | 路径 | 作用 |
|---|---|---|
| POST | `/v2/sessions` | 建会话，返回 `threadId` |
| GET | `/v2/sessions` | 列会话 |
| GET | `/v2/sessions/:id` | 会话详情 |
| PATCH | `/v2/sessions/:id` | 改标题 |
| PATCH | `/v2/sessions/:id/agent` | 换会话的 Agent |
| DELETE | `/v2/sessions/:id` | 删会话 |
| GET | `/v2/sessions/:id/messages` | 历史 + 用量（可 `?agent=` 过滤） |
| POST | `/v2/sessions/:id/abort` | 中断在跑的这轮 |
| POST/GET | `/v2/sessions/:id/files` | 上传 / 列会话工作区文件（单文件上限 32 MiB） |
| GET | `/v2/project/files` | 列项目根文件（`?q=` `?limit=`） |
| GET | `/v2/commands` | 斜杠命令清单 |
| POST | `/v2/run` | **SSE 入口**：提问，或回复审批 |
| GET | `/v2/skills` | 技能清单 |
| GET | `/v2/agents` `/v2/agents/:agentId` | Agent 及其生效工具表 |
| GET | `/v2/config` | 生效配置 |
| GET | `/v2/health` | 健康检查（含 `deferred` 告警） |
| GET | `/v2/runtime` | 运行时状态 |
| GET | `/v2/settings` | 可编辑项清单 |
| GET/POST/PUT/DELETE | `/v2/settings/item` | 配置读写（带 `baseRevision`） |
| POST | `/v2/settings/validate` | 不落盘地校验一份配置 |

### 9.1 跑一轮

```bash
# 1. 建会话
curl -s -X POST localhost:8080/v2/sessions \
  -H 'content-type: application/json' -d '{"agentId":"main"}'
# → {"threadId":"...","state":"idle",...}

# 2. 提问（SSE 流）
curl -N -X POST localhost:8080/v2/run \
  -H 'content-type: application/json' \
  -d '{"threadId":"<上一步的 id>","query":"读一下 README"}'
```

★ **`threadId` 必填**。不传会得到
`threadId is required; create the session with POST /v2/sessions first`。

响应是 **AG-UI 事件流**，事件类型：`RUN_STARTED`、`TEXT_MESSAGE_*`、`REASONING_*`、
`TOOL_CALL_*`、`CUSTOM`（`model_retry`）、`RUN_FINISHED`、`RUN_ERROR`。
子 Agent 的产出靠事件上的 `subagentRunId` 归属。

### 9.2 回审批

审批的决定塞回**同一个** `/v2/run`：

```json
{
  "threadId": "...",
  "resume": [
    {"interruptId": "...", "payload": {"approved": true}},
    {"interruptId": "...", "payload": {"approved": false, "reason": "别动那个文件"}}
  ]
}
```

## 10. 常见问题

**Q：启动报 `no configuration at ...: run \`openheron init\` to create one`**
配置根是空的。在项目根跑 `openheron init`。

**Q：报 `config: load <path>: agents/main.md does not exist or could not be parsed`**
两件事之一：配置根指错了（用 `--config-dir` 或 `$OPENHERON_CONFIG_DIR` 指对），
或者 `agents/main.md` 的 frontmatter 写错了键。

**Q：改了 `agents/main.md` 没生效？**
`agents/` 是热重载的，应该自动生效。没生效看启动日志里的 reload 错误。
`server/` 和 `skills/` **不热重载**，要重启。

**Q：模型不思考，`reasoning: high` 像是没用？**
多半是模型档案里**没有声明 `high` 档**。档位表缺哪档，那档就被静默忽略。
去 `llm/<vendor>.yaml` 的 `models[].reasoning` 补上。

**Q：`kb_search` 找不到东西 / 搜得不准？**
先 `openheron kb status`：显示 `lexical-only retrieval` 说明没有向量，
混合打分没生效。配好 embedding 模型后重新 `kb index`。

**Q：Agent 调不动 `write_file`？**
两种可能：它被这个 Agent 的 `disabled_tools` 禁了，或者被全局 `deny` 禁了。
查 `GET /v2/agents/:agentId` 看生效工具表。默认 `main` 是禁用了
`write_file`/`edit_file`/`execute` 的 —— 出厂配置里它只做编排。

**Q：`/v2/run` 报 `threadId is required`**
先 `POST /v2/sessions` 建会话。

**Q：端口被占 / 界面打不开？**
`listen_addr` 改端口，或用环境变量 `LISTEN_ADDR` 覆盖。

**Q：想看服务到底加载了什么？**
`GET /v2/config` 返回生效配置；`GET /v2/health` 返回健康状态和 `deferred` 告警；
`GET /v2/runtime` 返回运行时状态。启动日志也会打印配置根路径。
