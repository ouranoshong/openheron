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

★ **或者一条命令装上发布出来的二进制。** 每个 `v*` tag 都会发布同样的单文件可执行程序，
覆盖六个平台 —— 界面是嵌进去的，旁边不需要再装任何东西。一条命令就够了：

```bash
# Linux / macOS
curl -fsSL https://github.com/ouranoshong/openheron/releases/latest/download/install.sh | sh
```

```powershell
# Windows
irm https://github.com/ouranoshong/openheron/releases/latest/download/install.ps1 | iex
```

安装脚本会挑出对应你平台的产物，**用发布页的 `checksums.txt` 校验 sha256**，装进
`~/.local/bin`。校验不过的下载**不会被安装**。

| 变量 | 作用 |
|---|---|
| `OPENHERON_VERSION` | 装指定 tag，而不是最新版 |
| `INSTALL_DIR` | 装到哪（默认 `~/.local/bin`） |
| `OPENHERON_REPO` | 从别的 `owner/repo` 装 |
| `OPENHERON_TOKEN` | 私有仓库用的 token |
| `OPENHERON_API_URL` | API 主机（默认 `https://api.github.com`；GitHub Enterprise 是 `https://<host>/api/v3`） |

想先看一眼再执行，就从[发布页](https://github.com/ouranoshong/openheron/releases/latest)
把 `install.sh` 下载下来读一遍 —— 脚本本身就是这个 release 的一部分，就在它装的那些
二进制旁边。

不用脚本的话，从[发布页](https://github.com/ouranoshong/openheron/releases/latest)取产物，
并用同目录下的 `checksums.txt` 校验：

| 平台 | 产物 |
|---|---|
| Linux x64 | `openheron_<version>_linux_amd64.tar.gz` |
| Linux arm64 | `openheron_<version>_linux_arm64.tar.gz` |
| macOS x64 | `openheron_<version>_darwin_amd64.tar.gz` |
| macOS arm64 | `openheron_<version>_darwin_arm64.tar.gz` |
| Windows x64 | `openheron_<version>_windows_amd64.zip` |
| Windows arm64 | `openheron_<version>_windows_arm64.zip` |

### 在沙箱里跑

★ **跑 openheron 不止有二进制这一条路。** 如果你希望 agent 的文件工具与
shell 工具根本够不到你的机器，openheron 有 Docker Sandbox Kit 的形态：
整个进程 —— HTTP/SSE 服务、文件工具、shell 工具 —— 都跑在一个 microVM 里，
而你指定的那个工作区是唯一穿过边界的东西。

**需要什么。** 一个 Docker 账号（`sbx login`）、一个虚拟化支持
（Linux 上要 KVM），以及按 openheron 读密钥的方式绑在宿主上的模型密钥：

```sh
printf '%s' "$DEEPSEEK_API_KEY" | sbx secret set-custom \
    --host api.deepseek.com --env DEEPSEEK_API_KEY \
    --placeholder proxy-managed --value-stdin
```

★ `proxy-managed` 是个哨兵值：容器里看到的是这个字符串，
宿主上的代理在出网那一刻换上真值。密钥本身从不进沙箱。

**怎么起。** 两个 Kit，运行时分别解析 —— **workload**（服务本身）
与一份 **mixin**（凭据，以及与它配套的网络规则）。已发布的那一套：

```sh
sbx run docker.io/ouranoshong/openheron-kit:latest \
    --kit docker.io/ouranoshong/openheron-kit-deepseek:latest \
    --name openheron ~/my-project
```

★ mixin 是一份声明，所以它也可以是**一个只放着一个 YAML 文件的目录**：
不用镜像、不用构建，而改一行就能把它指向你自己的端点。
workload 带着根文件系统，所以它只能是镜像。

**怎么打开。** 运行时会把 workload 声明的端口发布到一个临时的回环端口上：

```sh
sbx ports openheron
```

```
HOST IP     HOST PORT   SANDBOX PORT   PROTOCOL
127.0.0.1   32772       8080           tcp
::1         32772       8080           tcp
```

★ 浏览器打开 **http://127.0.0.1:32772** —— 同一个界面、同一个 API、同一个 agent。

**有什么不一样。** 不多，而且全是对你有利的：

- **工作区**是你宿主上的一个目录，以同一个路径挂进沙箱。
  agent 的改动落在那儿，你看得见它们一个个到达。
- **配置**也落在那儿：`.openheron/` 在第一次启动时写好，
  从此它就是你的文件，你随便改。
- **密钥**留在宿主上，沙箱里只有哨兵值。
- **审批**照旧，而且默认是安全的那一档：每个工具都要先问一声，
  而 `.openheron/tools/` 与 `.openheron/mcps/` 下的文件点名例外（见 §4.3）。

★ 分步教程见
[`sandbox-kit/TUTORIAL.md`](../sandbox-kit/TUTORIAL.md)（中文：
[`sandbox-kit/TUTORIAL_zh.md`](../sandbox-kit/TUTORIAL_zh.md)），
背后的设计见 [`docs/sandbox-kit-design.md`](sandbox-kit-design_zh.md)。

```bash
sha256sum -c checksums.txt --ignore-missing
```

★ 二进制是静态的（`CGO_ENABLED=0`），所以那个平台上随便哪台机器都能跑 ——
不用装 Go，不用装 Node，也没有运行时要伺候。

## 2. 第一次使用

```bash
cd /your/project
openheron init                      # 在当前目录下生成配置
export DEEPSEEK_API_KEY=sk-...     # 密钥只从环境变量读
openheron
```

打开 <http://localhost:8080>。

`openheron init` 会写五个文件：

```
your/project/
└── .openheron/
    ├── agents/
    │   ├── main.md          # 入口 Agent：理解需求、拆任务、委派、验收
    │   └── task.md          # 执行 Agent：真正动手干活
    ├── llm/
    │   └── deepseek.yaml    # 模型档案
    ├── mcps/
    │   └── example.yaml      # 一个 MCP 服务器的示例（它的 command 是占位符）
    └── server/
        └── config.yaml      # 进程自身参数（不热重载）
```

★ `mcps/` 下那个文件是**示例**，不是配置 —— 见 §4.7。

★ **没有 `policy/` 下的文件。** 档位有缺省，例外由工具的来处点名（§4.3），
而一份「说的就是代码已经在做的事」的文件是噪音 —— ★ 配置树里的噪音会被当成意思读。
要自己点例外，就写在那个工具自己的文件里。

★ **已存在的文件不会被覆盖**（除非加 `--force`），所以在一个已经配好的项目里跑它是安全的。

`init` 的其它选项：

| 选项 | 作用 |
|---|---|
| `--dir <path>` | 写到指定目录（默认当前目录） |
| `--provider <name>` | 选模型厂商，默认 `deepseek` |
| `--model <name>` | 选模型，默认 `deepseek-flash` |
| `--base-url <url>` | 厂商端点（**含路径**，从厂商文档抄） |
| `--env <NAME>` | 密钥的环境变量名，默认 `<PROVIDER>_API_KEY` |
| `--tools` | 同时生成 `tools/` 下的工具配置 |
| `--force` | 覆盖已存在的文件 |
| `--dry-run` | 只打印要写什么，不落盘 |
| `--json` | 机器可读输出 |

## 3. 配置根：你的配置在哪

**只有三级，不向上查找**：

```
--config-dir  →  $OPENHERON_CONFIG_DIR  →  <project-root>/.openheron
```

- **默认那一档不存在会帮你创建**。
- 前两档指向不存在的目录时**不会创建** —— 打错字不该静默长出一棵空配置树。
  这种情况下启动会报 `config: load <path>: ...`，告诉你哪里不对。
- 配置根**不依赖「进程从哪启动」**：在子目录里跑 `openheron` 读到的还是同一份配置。

如果默认根下没有 `agents/main.md`，启动会直接告诉你：

```
no configuration at /your/project/.openheron: run `openheron init` to create one
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
tools: [ls, read_file, write_file, edit_file, glob, grep, execute, write_todos]
mcps: []
summarize_at: 80%
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
| `summarize_at` | 这个 Agent 的上下文什么时候被**压缩**成摘要：模型窗口的 `80%`（默认）、绝对 token 数（`104858`）、或 `0` 表示永不 |
| `subagents` | 它能委派给谁（见 §5） |
| `tools` | **这个 Agent 能用的工具** —— 就是模型看见的那些名字。★ **不写就是一个工具都没有**：这张清单就是全部答案。MCP 的工具按组合名写，或按前缀写（`mcp__github__*`，见 §4.8） |
| `skills` | 这个 Agent 能加载的技能 —— `skills/` 下文件夹的白名单。**不写就是没有技能**，写空列表也一样 |
| `mcps` | 这个 Agent **挂载**的 MCP 服务器 —— **不写就是不起任何服务器**，写空列表也一样。挂载一个就是起一个子进程（见 §4.8） |
| `temperature` / `max_tokens` | 采样参数 |
| `reasoning` | 思考档位：`off` / `low` / `high` / `max`（名字由厂商档案定义） |

★ **frontmatter 写错一个键就是启动错误**，不会被忽略 —— 这是故意的：
一份静默失效的配置比一份报错的配置难查得多。

★ **`model` 不继承**。父 Agent 用了哪个模型，子 Agent 不会跟着用 —— 每个 Agent 都得自己写。
这正是「专家配专模」的落点。

★ **`reasoning` 档位必须在模型档案里声明过才有效**。没声明的档位会被**静默忽略**：
Agent 写着 `reasoning: high`，而端点没有 `high`，实际效果是「完全不思考」，且没有任何报错。
详见 §4.2。

★ **`summarize_at` 是长对话活下来的原因**。越过这个点，对话会被一段摘要取代，从那以后
agent 带着摘要往前走。它有三种写法：

| 写法 | 含义 |
|---|---|
| `summarize_at: 80%` | 该 Agent 实际所用模型 `context_window` 的 80% |
| `summarize_at: 104858` | 绝对 token 数 |
| `summarize_at: 0` | 永不压缩 —— 上下文一直长到端点拒绝请求 |

不写这个键，Agent 拿到的是**窗口的 80%** —— 自动压缩默认开着。常用区间是 80–90%：
正好填满窗口的请求就是会被拒绝的请求，剩下的那点余量正是摘要自己需要的。

★ **百分比需要窗口**。模型没声明 `context_window`，就没什么可取 80%：那个 Agent 永远不压缩，
而 openheron 加载配置时会说出来。在 `llm/<vendor>.yaml` 里声明 `context_window`，
或者在 Agent 上写一个绝对的 `summarize_at`。

### 4.2 模型档案（`llm/<vendor>.yaml`）

```yaml
type: openai_compat
base_url: https://api.deepseek.com
api_key: ${DEEPSEEK_API_KEY}
timeout: 5m

models:
  - id: deepseek-flash
    context_window: 131072
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
| `models[].context_window` | 上下文窗口 —— 压缩阈值就是拿它量的 |
| `models[].reasoning` | **档位表**：档位名 → 这个端点实际发什么字段 |
| `models[].reasoning_default` | Agent 没写 `reasoning` 时用哪档 |
| `models[].timeout` | 覆盖厂商级超时（思考模型通常要调大） |
| `hint_keys` | 端点的字段名，用于请求被拒时给出准确提示 |
| `extra` | 厂商级方言，透传给所有请求 |

★ **`api_key` 只能写环境变量名**（`${OPENAI_API_KEY}`）或文件引用（`@file:/path`）。
在配置里写字面量密钥，配置编辑器会直接拒绝保存 —— 密钥不该进仓库。

★ **压缩阈值属于 Agent，不属于模型。** `context_window` 是模型的；`summarize_at` 是 Agent 的 ——
这个 Agent 把自己的上下文填到多少才压缩，是它自己的策略。同一个模型可以服务多个策略不同的
Agent。模型档案里留着的 `summarize_at` 不再被读，openheron 加载配置时会说出来。

压缩缩短的是**上下文**，不是**记录**。被压缩掉的消息仍留在记录里、仍读得到；摘要就写在它们
旁边。你自己的话不会在切口处丢失 —— 摘要把它们留住了。

★ **`reasoning` 档位表不是可选的**。它是「Agent 写 `reasoning: high`」与
「这个端点怎么表达 high」之间的翻译层。表里没有的档位会被静默忽略，
于是「开了 high 档却不思考」这个坑会以最隐蔽的方式出现。

### 4.3 哪些工具要问

**你不用去决定。** 没人点名的工具**一律先问**：这一轮暂停，界面上出现审批卡片
（见 §6.2）。默认是安全的那一档 —— 多点一次，换来的是一次没人看见的破坏性调用
不被执行。

档位只有两个：

| 档位 | 含义 |
|---|---|
| `allow` | 直接跑，不问 |
| `ask` | 等你点头 |

**例外只有两处来点**，就这两处：

| 位置 | 它决定什么 |
|---|---|
| 提供这个工具的那个文件里的 `allows:` 行 | 这个文件提供的哪些工具**不用**问；其余一律问 |
| Agent 里的 `tools:` 行 | 这个 Agent **能用的**工具 —— 不在里面的工具就不在它的工具表里，模型连看都看不见 |

★ **没有全局清单。** 没有一份文件让你列出「哪些工具要问」，也没有一份让你列出
「哪些工具谁都不能用」。文件点自己的例外，Agent 点自己的例外，两者从不合并 ——
于是一个 Agent 的生效工具表，永远只靠配置就能回答。

★ **代码自带一份缺省清单，文件只能往上加。** 读取类（`ls` `read_file` `glob` `grep`）
与委派类（`task` `write_todos` `skill`）永远不用问 —— 没有任何文件能把它拿走。
其余的一律要问，直到有文件点了名。

★ **改机器的那三个工具也有自己的文件。** `write_file`、`edit_file`、`execute` 缺省要问，
而放宽它们的地方就是 `tools/filesystem.yaml`：

```yaml
# .openheron/tools/filesystem.yaml
allows: [write_file, edit_file, execute]
```

★ 那个文件是**调音旋钮，不是开关**：文件系统工具一直都在，所以它既不能加一个也不能删一个，
只能挪它们的档位。**放宽是文件的活，收窄是 agent 的活**（`tools`）—— 因为收窄是
按 agent 来的，而放宽是关于这个工具本身的决定。

有自己文件的工具，在那个文件里说：

```yaml
# .openheron/tools/http.yaml
methods: [get]
allows: [request_get]
```

`request_get` 不用问；`request_post` 要问 —— 而且 `methods` 不点 `post` 的话它压根
不会被提供。知识库说的是同一件事：`kb.yaml` 放行 `kb_search` —— 检索是读，而
`kb_index` 要往磁盘上的索引里写，所以要问。

MCP server 在它自己的文件里说，用的是**它自己的词汇** —— server 自己的工具名，
不是暴露出来的那些（见 §4.7）：

```yaml
# .openheron/mcps/github.yaml
allows: [read_issue, list_issues]
```

★ **档位不是对工具的判决。** 一个 Agent 根本用不了的工具保留自己的档位，并在理由里
说清：Agent 的生效工具表会报 `blocked`，理由是 `tools`（§4.8）。
没有第三档，也没有全局拒绝 —— 一个工具要么对这个 Agent 可用，要么压根不在它的
工具表里。

### 4.4 外部工具（`tools/`）

**不用改代码** —— 配置缺失的 provider 什么都不挂，所以写下这个文件才是把工具挂上去的那一步。
当前支持四个文件名：

| 文件 | 提供 |
|---|---|
| `http.yaml` | 通用 HTTP 请求工具 |
| `web_search.yaml` | 网页搜索 |
| `wikipedia.yaml` | 百科查询 |
| `kb.yaml` | 知识库（见 §7） |

★ 知识库是唯一的例外：它的文件只承载设置 —— 真正开关它的是**非空 `corpus_dir`**，而不是
文件在不在。

★ `tools/` 下**只认这四个文件名**，写别的名字会**启动报错**，不会被静默忽略。

★ **`openheron init` 默认不写 `tools/` 下的任何文件** —— 这一整个目录要你自己写；加
`--tools` 才会把上面那四个文件写出来。没写就是没启用：对应的工具不会出现在 agent 的
工具表里，模型也看不见它。

★ **这个文件的 `allows:` 行决定哪些不用问** —— `tools/http.yaml` 点 `request_get`，
`tools/kb.yaml` 点 `kb_search`。这个文件提供的其余一切都问（§4.3）。

### 4.5 进程参数（`server/config.yaml`）

| 字段 | 默认 | 说明 |
|---|---|---|
| `listen_addr` | `:8080` | 监听地址 |
| `data_dir` | `state` | 会话与 checkpoint 的存放目录 |
| `static_dir` | `web/dist` | 前端产物目录（`frontend_mode: dir` 时用） |
| `frontend_mode` | `embed` | `embed` 用嵌进二进制的界面，`dir` 读磁盘 |
| `start_timeout` | `60s` | 启动超时 |
| `lease_ttl` | `10m` | 一轮在没有进展的情况下能持有多久的租约——**必须长于 provider 自己的超时** |
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

★ **一个 Agent 能用哪些技能，由它自己声明**。`agents/<name>.md` 的 `skills` 键是
白名单，名字就是 `skills/` 下的文件夹名：

```yaml
skills: [refactor, review]
```

不写这个键就是**目录里的全部技能**；写空列表就是**一个都没有**。不在名单里的技能
不会出现在给模型看的技能清单里，点名去调也会失败 —— 看不见的东西调不到。
名单里写了目录里没有的名字，Agent 装配时日志会报出来，不会被忽略。

### 4.7 MCP 服务器（`mcps/`）

MCP 服务器是一个把工具提供给模型的程序。openheron **一个文件挂一个服务器**，
而且**文件在就是定义** —— 没有 `enabled` 这个键。

`mcps/<server>.yaml` —— 文件名就是服务器的名字：

```yaml
# .openheron/mcps/github.yaml
transport: stdio
command: npx
args: ["-y", "@modelcontextprotocol/server-github"]
env:
  GITHUB_TOKEN: ${GITHUB_TOKEN}
timeout: 30s
call_timeout: 60s
tools: []
allows: [read_issue, list_issues]
```

同一个服务器走 HTTP 的写法：

```yaml
# .openheron/mcps/remote.yaml
transport: http
url: https://mcp.example.com/mcp
headers:
  Authorization: Bearer ${REMOTE_TOKEN}
timeout: 30s
call_timeout: 60s
```

| 字段 | 用在哪 | 说明 |
|---|---|---|
| `transport` | 两者 | `stdio`（一个子进程）或 `http`（**streamable HTTP** —— 没有 SSE 这个传输方式）。默认 `stdio` |
| `command` | stdio | 要执行的命令。**必填** |
| `args` | stdio | 命令的参数 |
| `env` | stdio | 子进程的环境变量，叠在继承来的那份之上 |
| `url` | http | 服务器的地址。**必填** |
| `headers` | http | 每次请求都带上 |
| `timeout` | 两者 | 连接、握手、列工具的时限（默认 `30s`） |
| `call_timeout` | 两者 | 单次工具调用的时限（默认 `60s`） |
| `tools` | 两者 | 对**服务器自己的**工具名的**过滤**；不写或写空就是全都要 |
| `allows` | 两者 | 服务器自己的工具名里**不用问**的那些；其余都要问 |

★ **文件名必须匹配 `^[a-z0-9]+(_[a-z0-9]+)*$`。** 带一串连续下划线的名字会被拒绝：
暴露出来的名字 `mcp__a__b__x` 以后没法反解回来 —— 那是服务器 `a` 带工具 `b__x`，
还是服务器 `a__b` 带工具 `x`？

★ **`openheron init` 写的是 `mcps/example.yaml`** —— 那是**示例**，而且是一份**能加载**的
示例：`mcps/` 下的文件**定义**一台服务器，所以一份解析不了的文件不是「服务器关着」，
而是「配置加载不了」。★ 它的 `command` 是一个没装的占位符，于是服务器起不来，
**日志里说一句，Agent 照跑**。**把它复制成 `mcps/<server>.yaml` 并填上你自己的
command，这个动作本身就是定义那台服务器。**

★ **会在启动时报错的**：写了不认识的 `transport`；stdio 服务器没有 `command`；
http 服务器没有 `url`；http 服务器上写了 `env:`；Agent 点名了一个不存在的服务器。

★ **不会启动报错的**：`timeout` 写坏了 —— 它退回到默认值，并在告警里报出来；
`env:` 里的 `${ENV}` 没设 —— 那一条被跳过，并在告警里报出来。写错的 timeout 改的是
「等多久」，不是「会怎样」，所以它不该拦住服务器启动。

**你会看到的名字。** 服务器自己的工具 `create_issue` 到模型那里叫
`mcp__github__create_issue`。服务器自己的名字和暴露出来的名字在 `/mcps` 面板里都列出来
（见 §6.8）。

★ **文件里不必写出凭据的名字。** 在沙箱里那个变量装的是 `credential@1` 哨兵，
宿主上的代理在出网时才把真值换上去 —— 于是哨兵顺着继承的环境到达服务器，
http 服务器的 `headers:` 也是一回事。

★ **`mcps/` 是热的。** 改了下一轮就生效 —— 不用重启。

### 4.8 一个 Agent 能拿到哪些服务器

有两根杠杆，互相独立。

**Agent frontmatter 里的 `mcps:` 键**决定这个 Agent 能用哪些服务器：

```yaml
---
model: deepseek/deepseek-flash
mcps: [github]
---
```

| `mcps:` 这个键 | 含义 |
|---|---|
| 不写 | **不起**任何服务器 |
| `[]` | **一个都没有** |
| `[github]` | 正好这些服务器 |

★ 子 Agent 有**自己**的名单，和它自己的 `skills`、`tools` 并排 —— 读的是正在跑的
那个 Agent 的名单，不是根 Agent 的。

★ **挂载不等于看得见。** `mcps:` 说哪些服务器被**拉起来**；`tools:` 说这个 Agent 看得见
其中哪些工具。两处都要写：

```yaml
---
model: deepseek/deepseek-flash
mcps: [github]                  # 把 github 拉起来
tools: [ls, mcp__github__*]     # 再让这个 Agent 看见它的工具
---
```

★ 点了 `mcp__github__*` 而 `mcps:` 排除了 `github`，是**启动错误**：那个前缀永远匹配不到
东西，而一条永远匹配不到的规则是错误，不是规则。

**第二根杠杆是 `tools`，而且它接受前缀。** 一个精确的名字，或一个**结尾**的 `*`：

```yaml
tools: [ls, read_file, mcp__github__*]
```

- `mcp__github__*` —— `github` 这个服务器的全部工具。
- `mcp__*` —— 所有服务器的全部 MCP 工具。
- `mcp__github__create_issue` —— 就那一个工具。

★ **`mcp*foo` 和单独一个 `*` 会在启动时报错。** 只有**结尾**的 `*` 才是前缀模式，
而单独一个 `*` 等于在一张「说明有什么」的清单里写「什么都有」：一条作用范围和字面读法
不一致的规则，就是一次无声的错配。

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

深度上限就是 `server/config.yaml` 里的 `agent.max_depth`。默认 `1`，意思是「只有入口
Agent 能委派，子 Agent 不能再生子」。

想跑更深的委派链：

```yaml
# .openheron/server/config.yaml
agent:
  max_depth: 3
```

★ 声明的拓扑超过上限是**启动错误**，不是「截断到上限然后继续跑」。
后者会让深度保护形同虚设。

★ **`server/` 不热重载**（§4.5）—— 上限调深要重启才生效。

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
| `/mcps` | 看看这个 Agent 的 MCP 服务器 —— 见 §6.8 |
| `/compact` | **立刻压缩当前对话** —— 见 §6.6 |
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

`/settings` 打开。它能改：`llm/` `agents/` `tools/` `mcps/`。

写入有几道闸门：

- `server/` **不可编辑**（不热重载，改了要重启）。
- **密钥字面量被拒** —— `api_key must be a reference, not a literal`。
  要用 `${ENV_VAR}` 或 `@file:/path`。
- **没有 YAML 内容的文件被拒** —— 纯空、或者只有注释的文件根本没有配置。报错会点名
  这个文件需要哪些字段；在 `tools/` 下还会说清是哪个工具缺了什么。
- **`mcps/` 下的文件必须叫 `<server>.yaml`**，服务器名字还要匹配 §4.7 的模式 ——
  别的名字在保存时就被拒。
- **乐观并发**：如果文件在别处被改过，保存会报冲突，让你选覆盖还是放弃。

### 6.4 上下文仪表盘

输入框下面那一行的末尾，是**你正在对话的那个 agent** 的上下文占用：

```
Agent: main · deepseek/deepseek-flash · Reasoning: off · Context: ≈18k / 131k (14 %)
```

- 那个数字是该 agent **最近一次请求**的大小 —— 不是整段对话的总量。一段长对话可能花了很多，
  上下文却还很空。
- 它是厂商自己的计数，所以是估算：`≈` 是故意写在那儿的。
- 进度条到窗口的 80 % 变琥珀色，95 % 变红。
- 模型**没声明**上下文窗口时，仪表盘只报 token 数（`Context: ≈18k tokens`），不报比例。
  想看进度条，就在 `llm/<vendor>.yaml` 里写 `context_window`。

多个 agent 同时在场时，**agent 过滤器**里每个 agent 各有一个数字 —— 每个 agent 有自己的上下文，
而且大小通常不一样。

★ 占用与 agent 的**花费**是两个数。仪表盘讲的是上下文还剩多少空间；花费讲的是这一整段会话
里这个 agent 花了多少。

### 6.5 压缩分隔线

上下文被压缩时，对话里会出现一条**分隔线**（界面文案是英文）：

```
────────────  12 earlier messages · compacted into a summary · ≈62k tokens saved  ────────────
```

- 分隔线标的是**模型读到哪里为止**。它上面，agent 读的是一段摘要；那些消息本身不再发出去。
- 消息仍然留在记录里、仍然读得到 —— 压缩缩短的是**上下文**，不是**记录**。往上滚，切口之前的
  对话还在。
- 数字是估算：`≈62k` 是厂商自己的 token 计数减去摘要的大小，说的是这一刀省下了多少空间。
- **轮内**发生的压缩会当场画出分隔线，但不带数字：数字是拿记录量出来的，而记录要等这一轮
  结束才写。重新打开这个会话，数字就出来了。

★ 摘要本身不显示。它是模型的工作材料，不是你的对话 —— 而且被它顶替的那些消息还在屏幕上。

### 6.6 `/compact` —— 手动压缩

上下文到了 agent 的阈值会**自己**被压缩（见 §4.1）。`/compact` 做的事一样，只是**由你开口**：
它把对话总结成一段摘要，当场让那段摘要成为新的边界。

```
/compact
```

- 它花**一次模型调用** —— 摘要由 agent 自己的模型生成，与自动压缩走的是同一条路。
- 它**与阈值无关**。自己不压缩的 agent（`summarize_at: 0`）照样能手动压：「关」说的是
  「别自己压」，不是「不许压」。
- 压完分隔线就出来了，带着数字。压的过程中，composer 上写着
  `Compacting the conversation…`。
- 分隔线上面的消息仍然读得到 —— 压缩缩短的是**上下文**，不是**记录**。

★ **上下文太小的时候它会拒绝。** 摘要带着前言和你自己的话，短对话压完可能反而**更长**。
少于四条消息、或者上下文不到大约 4k token 时，`/compact` 会说没什么值得压的，然后什么也不做。

★ **轮次在跑、或者有审批在等的时候，它也会拒绝。** 两者都占着上下文：在它们底下切一刀，
切的就是它们正在用的那份历史。先把轮次停掉。

同一件事也有 HTTP 入口 —— `POST /v2/sessions/{id}/compact` —— 脚本可以用。

### 6.7 同一个会话开两个窗口

一个会话**同时只跑一轮**。同一个会话开在两个窗口里 —— 两个标签页，或者一个标签页
加另一台机器 —— 那么只有一个能跑轮次。

另一个会告诉你：

```
This session is running in another window — stop it to take over
```

★ **什么都不会丢。** 被拒的那条消息没有发出去，所以它不会出现在对话里。正在跑的
那一轮照跑，毫发无损。

★ **停止按钮就是接管的方式。** 它停掉这个会话的轮次 —— 不管它属于谁 —— 然后你就
能发消息了。拥有那个轮次的窗口会看到它停下来。

★ **这是服务端的规矩，不是浏览器的。** 任何跟接口说话的客户端都得到同样的答复；
会话的 `state` 对每个看它的客户端都报 `"running"`。在轮次运行期间打开的第二个
窗口，水合出来就是「忙」，发送按钮已经是禁用的。

★ **连接断了也不会丢掉这一轮。** 轮次属于服务端，不属于碰巧在看着它的那条连接：
服务端照样归档。连接断掉的客户端带着 run 的 id 回来——`POST /v2/run`，带
`runId`、**不带问题**——就能拿到这一轮剩下的部分。`GET /v2/sessions/{id}/runs`
说这个 run 还跑不跑；它不跑的时候，那一轮的历史就在那里。

### 6.8 `/mcps` —— 这个 Agent 的 MCP 服务器

`/mcps` 是一个**动作**，不是提示词：它打开一个对话框，**不写进对话记录，也永远不到模型那里**。
状态是**读**出来的，不是生成出来的 —— 让模型去描述这些服务器，等于造一个关于运行时
已知事实的、有损的第二份真相。

对话框标题是 "MCP servers"，每个已配置的服务器一块：

- 服务器的名字、它的传输方式（`stdio` 或 `http`）、以及它的状态 —— `ready`、
  `unreachable` 或 `unused`；
- 每个工具一行：服务器自己的名字、暴露出来的名字（`mcp__github__create_issue`）、
  档位（`allow` 或 `ask`）、以及状态。

一个服务器都没配时，对话框说的是 "No MCP servers are configured"。

★ **打开对话框会把它问到的那些服务器启动起来**，所以它能回答「我的服务器是不是坏了」——
否则答案只能是「跑一轮看看」。这个 Agent 没用到的服务器是 `unused`，不带工具行。

**Refresh** 按钮会重读一次状态，所以你改完服务器不用关掉对话框就能看到效果。

## 7. 知识库

知识库是**可选**的本地语料检索层：把一批 markdown 文档变成 Agent 能检索的资料。

★ **「corpus」是什么意思。** 「corpus」是信息检索里对「检索系统据以作答的那批文档」的叫法；
在这里它就是**一个目录**。`corpus_dir` 是知识库要索引的那个目录 —— 一条相对项目根的路径，
而只有它下面的 `.md` 文件会被索引（`kb/index.go:21`）。

### 7.1 开启

```yaml
# .openheron/tools/kb.yaml
corpus_dir: docs      # a directory relative to the project root
index: default
model: openai/text-embedding-3-small
top_k: 8
```

- `corpus_dir` **不配就是关闭**。
- `corpus_dir` **必须是相对项目根的路径**。写绝对路径会**启动报错** ——
  索引和语料必须同生共死，放到系统目录里的语料会让索引失去意义。
- `model` 必填，且必须指向一个声明了 `kind: embedding` 的模型，
  否则启动报错（`kb.model ... is not an embedding model`）。

索引落在 `<data_dir>/kb/<index>/`。

### 7.2 命令行

```bash
openheron kb index                 # build the index
openheron kb index docs/a.md       # index only the given files
openheron kb search "how is the config root resolved"   # search
openheron kb status                # inspect the index
openheron kb status --json         # machine-readable
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
openheron [--config-dir <path>] [--project-root <path>]   start the server
openheron init [options]                                   generate a minimal runnable configuration
openheron kb index [--config-dir] [--json] [files...]      build the index
openheron kb search [--config-dir] <query>                 search
openheron kb status [--config-dir] [--json]                inspect the index
openheron version                                         print the version
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
| GET | `/v2/sessions/:id/runs` | run 账本：这个会话的每一次 run，以及当前那一次 |
| GET | `/v2/sessions/:id/runs/:runId` | 这个会话的某一次 run |
| POST | `/v2/sessions/:id/abort` | 中断在跑的这轮 |
| POST | `/v2/sessions/:id/compact` | 立刻压缩当前对话（见 §6.6） |
| POST/GET | `/v2/sessions/:id/files` | 上传 / 列会话工作区文件（单文件上限 32 MiB） |
| GET | `/v2/project/files` | 列项目根文件（`?q=` `?limit=`） |
| GET | `/v2/commands` | 斜杠命令清单 |
| POST | `/v2/run` | **SSE 入口**：提问，或回复审批（见 §6.7） |
| GET | `/v2/skills` | 技能清单 |
| GET | `/v2/agents` `/v2/agents/:agentId` | Agent 及其生效工具表 |
| GET | `/v2/mcps` | 某个 Agent 的 MCP 服务器（`?agent=`）；见 §6.8 |
| GET | `/v2/config` | 生效配置（其中的 `policy` 字段带着缺省档位与那些不用问的工具名） |
| GET | `/v2/health` | 健康检查（含 `deferred` 告警） |
| GET | `/v2/runtime` | 运行时状态 |
| GET | `/v2/settings` | 可编辑项清单 |
| GET/POST/PUT/DELETE | `/v2/settings/item` | 配置读写（带 `baseRevision`） |
| POST | `/v2/settings/validate` | 不落盘地校验一份配置 |

### 9.1 跑一轮

```bash
# 1. Create a session
curl -s -X POST localhost:8080/v2/sessions \
  -H 'content-type: application/json' -d '{"agentId":"main"}'
# → {"threadId":"...","state":"idle",...}

# 2. Ask (SSE stream)
curl -N -X POST localhost:8080/v2/run \
  -H 'content-type: application/json' \
  -d '{"threadId":"<the id from step 1>","query":"read the README"}'
```

★ **`threadId` 必填**。不传会得到
`threadId is required; create the session with POST /v2/sessions first`。

★ **一个会话同时只跑一轮。** 对已经在跑的会话再发一个请求，会得到 `409`，错误码是
`session_busy` —— 正在跑的那一轮不受影响（见 §6.7）。

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
    {"interruptId": "...", "payload": {"approved": false, "reason": "do not touch that file"}}
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
两种可能：它不在这个 Agent 的 `tools:` 清单里 —— 那模型连看都看不见 —— 或者它
本来就只是个 `ask` 档工具，而那张审批卡片没人答。查
`GET /v2/agents/:agentId` 看生效工具表。默认 `main` 是禁用了
`write_file`/`edit_file`/`execute` 的 —— 出厂配置里它只做编排。

**Q：`/v2/run` 报 `threadId is required`**
先 `POST /v2/sessions` 建会话。

**Q：模型看不见 MCP 服务器的工具？**
打开 `/mcps`（见 §6.8）。这个 Agent 没用到的服务器写着 `unused`；够不着的工具写着
`blocked`，括号里的理由会点名 `tools` —— 这个 Agent 自己的清单没覆盖它。
写着 `unreachable` 的服务器旁边带着出错原因，说清它为什么起不来。

**Q：MCP 服务器起不来？**
启动报错会点名是哪个文件的哪个字段。`mcps/` 是热的，所以改完下一轮就生效 —— 不用重启。

**Q：端口被占 / 界面打不开？**
`listen_addr` 改端口，或用环境变量 `LISTEN_ADDR` 覆盖。

**Q：想看服务到底加载了什么？**
`GET /v2/config` 返回生效配置；`GET /v2/health` 返回健康状态和 `deferred` 告警；
`GET /v2/runtime` 返回运行时状态。启动日志也会打印配置根路径。
