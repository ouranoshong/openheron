# 用户使用说明

> openheron 是一个多 Agent 运行时：每个「专家 Agent」绑定自己的模型和工具权限，
> 你可以像用聊天窗口一样用它，也可以把它的 HTTP 接口接到自己的系统里。
>
> 本文面向**使用者**。想改代码请看 [developer-manual.md](developer-manual.md)，
> 想了解设计取舍请看 [architecture.md](architecture.md)。

> English version: [user-manual.md](user-manual.md)

## 1. 安装

openheron 是一个自包含的可执行程序 —— 界面已经嵌在二进制里，旁边不需要再装任何东西。
怎么装，按你的处境挑一条路：

| 你的处境 | 看哪节 |
|---|---|
| 只想尽快用起来 | §1.1 装发布版 |
| 要改代码 | §1.2 从源码构建 |
| 在 Windows 上 | §1.3 在 Windows 上 |
| 想让 agent 的工具够不到你的机器 | §1.4 在沙箱里跑 |

### 1.1 装发布版

每个 `v*` tag 都发布同样的单文件可执行程序，覆盖六个平台。一条命令就够了：

```bash
# Linux / macOS
curl -fsSL https://github.com/ouranoshong/openheron/releases/latest/download/install.sh | sh
```

```powershell
# Windows
irm https://github.com/ouranoshong/openheron/releases/latest/download/install.ps1 | iex
```

安装脚本会挑出对应你平台的产物，**用发布页的 `checksums.txt` 校验 sha256**，装进
`~/.local/bin`（Windows 上是 `%LOCALAPPDATA%\Programs\openheron`）。校验不过的下载
**不会被安装**。

| 变量 | 作用 |
|---|---|
| `OPENHERON_VERSION` | 装指定 tag，而不是最新版 |
| `INSTALL_DIR` | 装到哪（缺省 `~/.local/bin`，Windows 上是 `%LOCALAPPDATA%\Programs\openheron`） |
| `OPENHERON_REPO` | 从别的 `owner/repo` 装 |
| `OPENHERON_TOKEN` | 私有仓库用的 token |
| `OPENHERON_API_URL` | API 主机（默认 `https://api.github.com`；GitHub Enterprise 是 `https://<host>/api/v3`） |

★ 想先看一眼再执行，就从[发布页](https://github.com/ouranoshong/openheron/releases/latest)
把 `install.sh` 下载下来读一遍 —— 脚本本身就是这个 release 的一部分，就在它装的那些
二进制旁边。

**不用脚本的话**，从[发布页](https://github.com/ouranoshong/openheron/releases/latest)取产物，
并用同目录下的 `checksums.txt` 校验：

| 平台 | 产物 |
|---|---|
| Linux x64 | `openheron_<version>_linux_amd64.tar.gz` |
| Linux arm64 | `openheron_<version>_linux_arm64.tar.gz` |
| macOS x64 | `openheron_<version>_darwin_amd64.tar.gz` |
| macOS arm64 | `openheron_<version>_darwin_arm64.tar.gz` |
| Windows x64 | `openheron_<version>_windows_amd64.zip` |
| Windows arm64 | `openheron_<version>_windows_arm64.zip` |

```bash
sha256sum -c checksums.txt --ignore-missing
```

★ 二进制是静态的（`CGO_ENABLED=0`），所以那个平台上随便哪台机器都能跑 ——
不用装 Go，不用装 Node，也没有运行时要伺候。

★ 装好之后：跑起来见 §2，密钥怎么设见 §4.2。

### 1.2 从源码构建

要改代码才需要这条。需要 **Go 1.27.1+**，另外还要 [go-task](https://taskfile.dev) 和
[Bun](https://bun.sh)：界面是用 Bun 构建再嵌进二进制的，所以这两个构建工具不是可选项。

```bash
git clone https://github.com/ouranoshong/openheron
cd openheron
task build
```

`task build` 会构建前端并把界面**嵌进二进制**，产物是 `bin/openheron` —— 一个自包含的
可执行文件，扔到哪台机器上都能跑（目标平台之外的机器用交叉编译）。

只想快速试一下：

```bash
task build-go     # 只构建后端（不含界面），迭代 Go 代码时更快
```

★ Windows 上的产物是 `bin\openheron.exe` —— 可执行文件在那边保留 `.exe` 后缀。
而 Windows 上的 `python3` 是 Microsoft Store 的转发器 —— 存在，但什么也不干 ——
仓库自己的脚本在那边调的是 `python`。这只影响文档校验（`task lint-docs`）。

### 1.3 在 Windows 上

★ **装的位置和 PATH 两处和 Unix 版不一样。** 位置是 `%LOCALAPPDATA%\Programs\openheron`
（不是 `~/.local/bin`），而且**它不碰你的 PATH**：脚本发现那个目录不在 PATH 上时只打印
一行提示，所以要么自己加，要么用全路径调用那个可执行文件：

```powershell
# 把安装目录加进用户 PATH
$dir = "$env:LOCALAPPDATA\Programs\openheron"
[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';' + $dir, 'User')
```

★ 改完**开一个新的终端**：已经开着的 shell 留着它启动时的那份 PATH。

`cmd.exe` 里没有 `irm`，把这一行交给 PowerShell：

```cmd
powershell -c "irm https://github.com/ouranoshong/openheron/releases/latest/download/install.ps1 | iex"
```

**工具差在哪。** 文件工具（`read_file`、`write_file`、`edit_file`、`ls`、`glob`）是运行时
自己的代码，在 Windows 上照常工作；只有 `grep` 要额外的
[ripgrep](https://github.com/BurntSushi/ripgrep)（`rg`）—— 它不自带，没有就会报
`ripgrep (rg) is not installed or not in PATH`（§10）。

**shell 工具自己找 shell。** `execute` 需要一个程序来跑命令，openheron 在进程启动时按这个
顺序找：PATH 上的 `sh` 或 `bash` → Git for Windows（`C:\Program Files\Git\usr\bin\bash.exe`）
与 MSYS2（`C:\msys64\usr\bin\sh.exe`）→ WSL 启动器（`C:\Windows\System32\bash.exe`，
**排在最后**，因为它把命令跑在 Linux 文件系统里，那边的 `C:\Users` 是 `/mnt/c/Users`）。

★ 每个候选都要跑一条最简单的命令验一下活，所以 Windows 自带的那个 `bash.exe` ——
每台机器都有，没有 WSL 发行版时什么也不干 —— 不会被当成 shell。第一个能跑的候选就是它；
在 PATH 之外找到的 shell，它自己那个目录会被放到子进程 PATH 的最前面，于是 `ls` 之类的
工具也找得到。

★ **一个 POSIX shell 都没有时，工具退回 `cmd.exe`**，而它的描述会告诉模型写 Windows 命令
（`dir`、`type`、`copy`），而不是 POSIX 的那套（`ls`、`cat`、`cp`）。★ 方言不是猜的：
模型读到的那段描述与真正跑命令的程序是同一个答案，在启动时一次定下来。另一条路是把
`execute` 从那个 Agent 的 `tools:` 清单里去掉 —— 出厂配置本来就是这么做的。

### 1.4 在沙箱里跑

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
背后的设计见 [`docs/tools-design.md`](tools-design.md) —— 那篇文档只有中文，
本仓库的设计文档都是这样。

## 2. 第一次使用

### 2.1 跑起来

```bash
cd /your/project
export DEEPSEEK_API_KEY=sk-...     # 密钥只从环境变量读
openheron                           # 配置在第一次启动时写出来
```

打开 <http://localhost:8080>。

★ 密钥从环境变量读，而**怎么设**取决于你在哪个系统上 —— macOS 与 Linux 见 §4.2，
Windows 见 §2.2。

★ **服务端会自己写配置**：在还没有配置的项目里第一次启动时，它写出的那棵树与
`openheron init` 写的完全一样（§2.3），用 DeepSeek 的 provider 模板，密钥从
`DEEPSEEK_API_KEY` 读。★ 要换模型、换 provider，或者只想把配置写出来而先不启动
服务端，就用 `openheron init`。

### 2.2 在 Windows 上跑第一次

PowerShell 里，从零到跑起来：

```powershell
cd C:\path\to\your-project
$env:DEEPSEEK_API_KEY = "sk-..."    # 密钥只从环境变量读
openheron
```

★ `openheron` 不在 PATH 上的时候，用全路径调
`%LOCALAPPDATA%\Programs\openheron\openheron.exe`（§1.3）。

`init` 跑完会打印一行提示：

```
the api key is read from the environment at request time: export DEEPSEEK_API_KEY=...
```

★ 服务端自己写配置时，启动日志里报的是同一件事。

★ 那行提示用的是 **Unix 的 `export`**，在 PowerShell 和 cmd.exe 里都跑不了 —— Windows 上
对应的是 `$env:DEEPSEEK_API_KEY = "sk-..."`（PowerShell）或 `set DEEPSEEK_API_KEY=sk-...`
（cmd.exe）。

#### 密钥怎么设

同一条命令，四种写法，作用范围不一样：

- **当前这个 PowerShell 窗口** —— `$env:DEEPSEEK_API_KEY = "sk-..."`：只这个窗口。
- **这个用户以后开的每个终端** —— `[Environment]::SetEnvironmentVariable('DEEPSEEK_API_KEY', 'sk-...', 'User')`：长期；★ **要开一个新终端**才生效。
- **cmd.exe 当前窗口** —— `set DEEPSEEK_API_KEY=sk-...`：只这个窗口。
- **cmd.exe 用户级** —— `setx DEEPSEEK_API_KEY sk-...`：长期；★ **值会被截断到 1024 个字符**。

★ **已经开着的 shell 留着它启动时的那份环境** —— 跑 `setx` 或
`[Environment]::SetEnvironmentVariable` 的那个窗口看不到这个变量，得**另开一个终端**。

★ **密钥是在加载配置时读的**（§4.2），所以变量要设到**启动 openheron 的那个进程**上：
事后在另一个终端补设的，运行中的服务看不到。

★ **Windows 上变量名不区分大小写** —— `$env:deepseek_api_key` 就是 `DEEPSEEK_API_KEY`
这个变量；macOS 与 Linux 上不是（§4.2）。

**把密钥放进文件。** 不想让密钥待在环境变量里，就用 `@file:` 指向一个文件：

```yaml
api_key: '@file:C:\Users\you\deepseek.key'   # 单引号
api_key: "@file:C:/Users/you/deepseek.key"    # 或者正斜杠
```

★ **Windows 上双引号加反斜杠不行**：YAML 的双引号里 `\` 是转义符，`\U` 不是合法转义，
整个档案解析失败（`yaml: did not find expected hexdecimal number`）。用单引号，或者把
路径写成正斜杠。★ 路径要**绝对**的：相对路径是相对**启动 openheron 时所在的目录**（§4.2）。

**怎么确认配好了。** 浏览器打开 `http://localhost:8080/v2/config`，那个 provider 条目里
`hasApiKey` 应该是 `true`；启动日志里没有 `api_key env DEEPSEEK_API_KEY is not set`
这一行，才算设上了（§4.2）。

#### 启动前

★ **`init` 写的地方和服务读的地方可能不是一个。** `init` 一定写当前目录下的
`.openheron`，它**不看** `$OPENHERON_CONFIG_DIR`；而服务读的顺序是 `--config-dir` →
`$OPENHERON_CONFIG_DIR` → 项目根下的 `.openheron`（§3）。这个环境变量设着的时候，
服务起来会报 "no configuration at ..."，因为它读的不是 `init` 写的那一棵 ——
★ 而它也不会在那儿写一棵：你点名的根是你自己的。

★ **Windows 上子进程找不到命令时**，把 `mcps/fetch.yaml` 和 `mcps/time.yaml` 里的
`command` 改成 `uvx.exe` —— 那两个 MCP 服务器要 PATH 里有 `uvx`（§2.3）。

端口被占就在**启动之前**换一个：`$env:LISTEN_ADDR = ":9090"`（§10）。

### 2.3 配置里有什么

整棵树由 `openheron init` 写出 —— 服务端自己第一次启动时写的也是这棵树：

```
your/project/
└── .openheron/
    ├── agents/
    │   ├── main.md           # 入口 Agent：理解需求、给方案、分派、汇总
    │   ├── task.md           # 通用执行专家：llm/ tools/ mcps/ server/
    │   ├── agent-expert.md   # agent 域专家：agents/*.md
    │   └── skill-expert.md   # 技能域专家：skills/<name>/SKILL.md
    ├── skills/
    │   ├── openheron-config-map/SKILL.md        # 配置系统的事实地图
    │   ├── openheron-config-edit/SKILL.md       # 修改配置的规程
    │   ├── openheron-config-templates/SKILL.md  # 骨架怎么拿
    │   ├── openheron-config-validate/SKILL.md   # 静默失效
    │   └── openheron-agent-authoring/SKILL.md   # 怎么写一个 agent
    ├── llm/
    │   └── deepseek.yaml    # 模型档案
    ├── mcps/
    │   ├── fetch.yaml        # 抓网页转 markdown
    │   ├── time.yaml         # 当前时间与时区换算
    │   └── deepwiki.yaml     # 公开仓库文档与问答
    ├── tools/
    │   ├── filesystem.yaml   # 文件系统工具的档位
    │   ├── http.yaml         # request_get
    │   ├── web_search.yaml   # duckduckgo_search
    │   ├── wikipedia.yaml    # wikipedia_search
    │   └── kb.yaml           # 知识库（关着：corpus_dir 是空的）
    └── server/
        └── config.yaml      # 进程自身参数（不热重载）
```

★ 那三个 MCP 服务器里有两个是 stdio 的 —— `fetch` 和 `time`，要 PATH 里有 `uvx`，而且
第一次启动时会下载自己的包；起不来的服务器只赔上它自己的工具，别的什么都不影响（§4.7）。

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
| `--no-tools` | 把 `tools/` 下的工具配置留下不写 |
| `--force` | 覆盖已存在的文件 |
| `--dry-run` | 只打印要写什么，不落盘 |
| `--json` | 机器可读输出 |

## 3. 配置根：你的配置在哪

**只有三级，不向上查找**：

```
--config-dir  →  $OPENHERON_CONFIG_DIR  →  <project-root>/.openheron
```

- **默认那一档不存在会帮你创建** —— 里面的树也会帮你写，第一次启动时写（§2.1）。
- 前两档指向不存在的目录时**不会创建** —— 打错字不该静默长出一棵空配置树。
  这种情况下启动会报 `config: load <path>: ...`，告诉你哪里不对。
- 配置根**不依赖「进程从哪启动」**：在子目录里跑 `openheron` 读到的还是同一份配置。
- ★ **你点名的根永远不会被写**：`--config-dir` 与 `$OPENHERON_CONFIG_DIR` 指的是你选的
  目录，它空着是你的决定，不是一个缺口。

如果默认根下没有 `agents/main.md`、而服务端也没能写出来，启动会直接告诉你：

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
| `tools` | **这个 Agent 能用的工具** —— **所有非 MCP 工具**：内置工具、`tools/` 下各文件提供的工具，以及 `config_*` 工具。★ **不写就是一个工具都没有**：这张清单就是全部答案。★ MCP 的工具**不写在这里** —— 挂载那个 server 就会得到它的工具（见 §4.8） |
| `skills` | 这个 Agent 能加载的技能 —— `skills/` 下文件夹的白名单。**不写就是没有技能**，写空列表也一样 |
| `mcps` | 这个 Agent **挂载**的 MCP 服务器 —— **不写就是不起任何服务器**，写空列表也一样。挂载一个就是起一个子进程，**并把它的全部工具交给这个 Agent**（见 §4.8） |
| `temperature` / `max_tokens` | 采样参数 |
| `reasoning` | 思考档位：`off` / `low` / `high` / `max`（名字由厂商档案定义） |

★ **frontmatter 写错一个键就是启动错误**，不会被忽略 —— 这是故意的：
一份静默失效的配置比一份报错的配置难查得多。

★ **`model` 不继承**。父 Agent 用了哪个模型，子 Agent 不会跟着用 —— 每个 Agent 都得自己写。
这正是「专家配专模」的落点。

★ **模型没人声明，只会把那个 Agent 留在树外 —— 不会让 openheron 起不来。** 那个 Agent
会从树里摘掉，每个父 Agent 的 `subagents:` 里也会删掉，理由在四个地方说：启动日志、
`/v2/health`、配置编辑器里**它自己文件上那条灰色的提示**（§6.3），以及会话真的要用它时
拿到的那句错。★ 默认 Agent 也不例外：没有可用的模型时 `main` 同样留在树外，在你改好之前
**没有会话能跑** —— 但 openheron 照常起来，这正是你能打开编辑器改这个文件的原因。
★ 编辑器会拦下造成这种结果的改动：把一个本来能跑的 Agent 指向一个不存在的模型，保存时就被拒。

★ **跑不起来的那条消息说的是原因，不是「超时」。** 如果你要对话的那个 Agent 装不起来，
聊天里给出的就是原因本身 —— `agent "main" is left out of the tree: the model
openai/gpt-4o-mini is not declared under llm/` —— 这一轮以 `agent_unavailable` 这个码结束。
★ 这个码意味着「你的配置坏了」，所以该做的是打开编辑器改文件，而不是把消息再发一遍。

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
在配置里写字面量密钥，配置编辑器会直接拒绝保存 —— 密钥不该进仓库。★ `@file:`
这种写法在 YAML 里**必须加引号**；下面那节讲各系统的密钥从哪来。

★ **压缩阈值属于 Agent，不属于模型。** `context_window` 是模型的；`summarize_at` 是 Agent 的 ——
这个 Agent 把自己的上下文填到多少才压缩，是它自己的策略。同一个模型可以服务多个策略不同的
Agent。模型档案里留着的 `summarize_at` 不再被读，openheron 加载配置时会说出来。

压缩缩短的是**上下文**，不是**记录**。被压缩掉的消息仍留在记录里、仍读得到；摘要就写在它们
旁边。你自己的话不会在切口处丢失 —— 摘要把它们留住了。

★ **`reasoning` 档位表不是可选的**。它是「Agent 写 `reasoning: high`」与
「这个端点怎么表达 high」之间的翻译层。表里没有的档位会被静默忽略，
于是「开了 high 档却不思考」这个坑会以最隐蔽的方式出现。

#### 密钥从哪来

档案只写**密钥在哪**，从不写密钥本身。有两种写法，两种都在**加载配置时**读取：

```yaml
# .openheron/llm/deepseek.yaml
api_key: ${DEEPSEEK_API_KEY}                  # 一个环境变量
api_key: "@file:/etc/openheron/deepseek.key"  # 同一个密钥，从文件读
```

★ **`@file:` 的引号不能省。** `@` 是 YAML 的保留字符，不加引号的
`api_key: @file:/path` 根本解析不了（"found character that cannot start any token"），
那个档案永远加载不上。`${NAME}` 不需要引号。

★ **密钥是在加载配置时读的** —— 所以要在**启动 openheron 之前**把变量设好。
事后再在另一个终端里 export，运行中的服务看不到它；把档案重新保存一次会重载配置，
重载时会再读一遍这个变量。

★ **Windows 上怎么设这个变量，见 §2.2** —— PowerShell 与 cmd.exe 的四种写法、
「变量名不区分大小写」，还有 `@file:` 在 Windows 上的引号坑，都在那里。

**macOS 与 Linux。**

```sh
export DEEPSEEK_API_KEY=sk-...
```

想长期有效，就把这一行写进 `~/.zshrc`（macOS）、`~/.bashrc`（Linux 上的 bash）
或 `~/.profile`（任何 POSIX shell）。

★ **macOS 与 Linux 上变量名区分大小写。** `$env:deepseek_api_key` 在那边是另一个变量，
那边档案会读到 `api_key env DEEPSEEK_API_KEY is not set`；Windows 上相反，不区分（§2.2）。

★ **变量要到达的是进程，不是你的登录会话。** `~/.bashrc` 那一行只有**交互式 bash**
会读：systemd 单元、launchd 作业、容器和 CI runner 各自有自己的环境，
它们都不读你的 shell 的 rc 文件。要把它交给真正启动 openheron 的那个东西：

| openheron 跑在哪 | 变量放哪 |
|---|---|
| 终端里 | `export ...`（macOS 与 Linux）/ `$env:... = ...`（Windows，见 §2.2） |
| systemd 单元（Linux） | 单元文件里的 `Environment=DEEPSEEK_API_KEY=sk-...`，然后 `systemctl daemon-reload` |
| launchd 作业（macOS） | 作业 plist 里的 `EnvironmentVariables` |
| 容器里 | `docker run -e DEEPSEEK_API_KEY=sk-...`，或 `--env-file .env` |
| CI | runner 自己的密钥库 —— 存变量，不是存密钥 |

**`@file:` 这种写法。** 文件里放的就是那个变量本来装的密钥，在加载时读，内容会去掉首尾空白。
两件事要知道：

- ★ **相对路径是相对启动 openheron 时所在的那个目录**，不是相对配置根 —— 用绝对路径。
- ★ **路径不存在是启动错误**，不是提醒：
  `config: parse llm/deepseek.yaml: api_key: read secret file: open /etc/openheron/deepseek.key: no such file or directory`。
  这个区别是刻意的：变量没设是运行期状态，路径写错是配置本身写错了。

**怎么知道配好了。** 哪个变量没设，启动日志会点名：

```
[main] config notice notice="llm/deepseek.yaml: api_key env DEEPSEEK_API_KEY is not set; calls to this endpoint will fail with 401 Unauthorized"
```

★ 这条提醒**不会拦住服务**：openheron 照常起来，Agent 照常建起来，
而那个端点第一次被调用时回的是 `401 Unauthorized`。同一批提醒也在
`GET /v2/config` 与 `GET /v2/health` 的 `deferred` 里。

★ **变量设成空串不等于没设。** `DEEPSEEK_API_KEY=` —— 设了，但里面是空的 ——
会解析成一个空密钥，而且**一条提醒都不会有**，所以没有提醒不等于没有问题。
要看的是 `GET /v2/config` 里的 `hasApiKey` 字段，那个 provider 条目长这样：

```json
{"id":"deepseek","baseUrl":"https://api.deepseek.com","hasApiKey":true,"models":[{"id":"deepseek-flash","contextWindow":1310720}]}
```

找到密钥时 `hasApiKey` 是 `true`，没有密钥时是 `false` —— 而密钥本身永远不出进程。
★ **把变量掏空的方式决定了你落在哪一种**：`$env:DEEPSEEK_API_KEY = ""` 会留下一个
空值（静默），`$env:DEEPSEEK_API_KEY = $null` 是把变量删掉（提醒照常出现）；
Unix shell 上是 `export DEEPSEEK_API_KEY=` 与 `unset DEEPSEEK_API_KEY` 这一对。

★ **变量名是你自己定的。** `openheron init --env DEEPSEEK_API_KEY`
会把档案写成 `${DEEPSEEK_API_KEY}`；档案写了别的变量名，环境里就得有那个变量 ——
要紧的是两边说的是同一个。

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

★ **有一个例外放宽不了。** `config_write`（§4.9）改的是配置本身，包括那个 `allows:`
列表会授予它的文件 —— 一个能点名它的文件，授的是重写它自己的工具。所以它**永远**要点头，
不管哪个文件怎么说。

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

★ **`openheron init` 会写 `tools/` 下的那五个文件** —— 工具配置是这棵树的一部分，
因为出厂的 agent 会点名 `request_get`、`duckduckgo_search` 和 `wikipedia_search`，
而没有 provider 提供的名字会被当成笔误报出来。加 `--no-tools` 那个目录就空着。
没写就是没启用：对应的工具不会出现在 agent 的工具表里，模型也看不见它。

★ **这个文件的 `allows:` 行决定哪些不用问** —— `tools/http.yaml` 点 `request_get`，
`tools/kb.yaml` 点 `kb_search`。这个文件提供的其余一切都问（§4.3）。

★ **代理是挪边界，不是加边界。** `http.yaml` 和 `web_search.yaml` 有一个可选的 `proxy:` 键。
空值就是**不走代理**：工具自己连目标，并拒绝解析不到公网地址的目标 —— 正是这条拒绝让模型
没法被拿来探这台机器自己的内网。配了代理就是**换掉**这条规则 —— 代理存在的意义就是去够直接
够不到的东西（内网地址不是公网地址，够到它正是代理的用处）—— 所以配了代理之后目标可以是
代理够得到的任何地址，边界就落在代理上。支持的协议是 `http`、`https`、`socks5` 和
`socks5h`；写错协议是**启动报错**。

★ **这个键有三种写法**，第三种是写出来的：`""` 是不走代理，一个 URL 点名那个代理 ——
环境变量被忽略 —— `env` 则把路线交给环境：`HTTP_PROXY`、`HTTPS_PROXY` 和 `NO_PROXY`。
缺省不能是「环境里有什么算什么」：那会让**恰好**导出了代理的机器上每一个工具都改道，
而为此买单的是这台机器的主人。环境变量**只读一次**，在工具被装配的时候 —— 所以事后改环境，
不会让跑着的工具换路线。

```yaml
# .openheron/tools/http.yaml
methods: [get]
proxy: http://127.0.0.1:7890
allows: [request_get]
```

```yaml
# .openheron/tools/http.yaml
methods: [get]
proxy: env
allows: [request_get]
```

★ **模型 API 调用是另一个客户端**：它读环境变量里的 `HTTP_PROXY` 和 `HTTPS_PROXY`，
沙箱 Kit 的凭据替换走的就是这条路。`proxy:` 键是给**工具**用的，只有被点名（`proxy: env`）
的时候才会去看环境变量。

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
| `watch.enabled` | `true` | 热重载监视器要不要开 —— 关掉它，**任何**改动都得重启才生效 |
| `watch.debounce` | `200ms` | 监视器读到被改的文件之前等多久 |
| `retry.max_retries` | `5` | 一轮里一次模型调用最多重试几次 |
| `log_level` | `info` | `debug`、`info`、`warn`、`error` —— 决定哪些行会进输出 |
| `log_requests` | `false` | 是否在 `debug` 下打印脱敏后的模型请求体与 `tools` 原始字节；等价于 `OPENHERON_LOG_LLM_REQUESTS=1` |
| `log.file` | `""` | 日志写到的文件，相对**配置根**解析 —— 空串 = 只写标准错误 |

★ **`server/` 不热重载** —— 改了要重启才生效。所以配置编辑器里它是**只读**的，
提交会报 `section "server" is not editable`。

以下环境变量可以覆盖进程参数，优先级高于配置文件：
`LISTEN_ADDR`、`DATA_DIR`、`PROJECT_ROOT`、`FRONTEND_MODE`、`STATIC_DIR`、`OPENHERON_LOG_LEVEL`、
`OPENHERON_LOG_FILE`、`OPENHERON_LOG_LLM_REQUESTS`。

★ `log_requests` 与 `OPENHERON_LOG_LLM_REQUESTS` 是同一个开关的两种写法：前者定项目默认值，
后者给单次运行下定论（**环境变量优先**，变量为空才看配置）。打开后每次模型请求会在 `debug`
下记一行脱敏后的请求体和一行 `tools` 原始字节；两个都关着时，**被拒绝的请求**仍会留下
`tools` 原始字节，因为那一轮最需要留证。它只管内容，不关 `log.file` 的落盘。

★ `log_level` 不是这四个之一时回退 `info`；配置的 `deferred` 提示里带着原因。

★ 日志默认写标准错误；`log.file` 指了路径就同时落盘 —— 路径相对**配置根**解析。文件按大小
轮转，而打不开文件只是告警、不是启动失败：进程留在标准错误上，并把原因写在那儿。

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

★ **一个技能就是一层文件夹，编辑器问的就是这个名字。** `/settings` 把每个分区显示成文件树
里的一个文件夹，于是技能长成 `skills/code-review/SKILL.md`：你只填技能名，文件夹里那个
文件名永远是 `SKILL.md`。这个名就是你在 Agent 的 `skills:` 里写的那个，而且必须能被读回来 ——
小写字母和数字，单个 `-` 或 `_` 分隔，所以 `code-review` 和 `code_review` 是两个技能。

★ **当 openheron 从别处读技能时，这个分区会说明并拒绝保存。** 上面那条规则意味着生效的目录
可能不是 `<配置根>/skills/`。往一个没人加载的目录里写等于什么都没改，所以编辑器会显示进程实际
在读的那个目录，并把这一分区标成只读。启动时**一个技能目录都没找到**不算这种情况：那时
`<配置根>/skills/` 正是下次启动会去找的地方，在那儿建第一个技能是有效的。

★ **`skills/` 是热的。** 改配置根下的 `SKILL.md` 下一轮就生效 —— 不用重启。技能目录在**别处**
就不在监视范围内：那里的改动要等下次启动才到达 Agent。

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

★ **`openheron init` 写三台服务器** —— `mcps/fetch.yaml`、`mcps/time.yaml` 和
`mcps/deepwiki.yaml`。★ 每一份都**定义**一台服务器，所以一份解析不了的文件不是
「服务器关着」，而是「配置加载不了」。★ 两个 stdio 的跑 `uvx`，而 `uvx` 默认没装 ——
于是服务器起不来，**日志里说一句，Agent 照跑**。★ **自己在 `mcps/` 下写一份文件、
填上自己的 command，这个动作本身就是定义那台服务器**；复制那三个之一、改掉 `command:`
就行。

★ **那个不安装的示例** —— 模板里的 `mcps/_example.yaml` —— 是设置界面新建服务器时的
骨架。它是脚手架，不是要安装的文件，所以 `openheron init` 不写它。

★ **stdio 服务器的第一次启动可能很慢，而 `timeout:` 就是这次启动的上限。** 命令常常
是一个包运行器（`npx`、`uvx`），第一次跑会把服务器装上 —— 那次安装是启动的一部分，
所以一台要装一分钟的服务器，在 `timeout: 30s` 里起不来。
★ 于是它就根本起不来，而这一轮照跑，只是没有它的工具：自己手动跑一次那条命令把缓存
捂热，或者给那个文件写一个更长的 `timeout:`。

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

**Agent frontmatter 里的 `mcps:` 键**决定这个 Agent 能拿到哪些服务器 —— 而挂载一个
**就是**授予：

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
| `[github]` | 正好这些服务器 —— 连同它们的全部工具 |

★ 子 Agent 有**自己**的名单，和它自己的 `skills`、`tools` 并排 —— 读的是正在跑的
那个 Agent 的名单，不是根 Agent 的。

★ **挂载就是授予。** `mcps:` 说哪些服务器被**拉起来**，而这个 Agent **拿到了那个服务器
的全部工具** —— 不需要再写第二份清单。

★ **`tools:` 点名的是所有非 MCP 工具。** 写进去的 `mcp__<server>__*` 不授予任何东西：它会被丢弃，
配置会报一条**提示**说清这件事。那个 server 该写进 `mcps:`。

★ **审批与挂载是两件事。** 挂载把工具交给 Agent；而某个工具要不要点头，由那个 server
自己的文件（`mcps/<server>.yaml`）决定，而那里的缺省是 `ask` ——
**挂载不等于放行**。

**`tools` 接受结尾的 `*` 前缀模式。** 一个精确的名字，或一个**结尾**的 `*`：

```yaml
tools: [ls, read_file, config_*]
```

- `config_*` —— 每一个以 `config_` 开头的工具。
- `ls` —— 就那一个工具。

★ **`mcp*foo` 和单独一个 `*` 会在启动时报错。** 只有**结尾**的 `*` 才是前缀模式，
而单独一个 `*` 等于在一张「说明有什么」的清单里写「什么都有」：一条作用范围和字面读法
不一致的规则，就是一次无声的错配。

### 4.9 配置工具

有六个工具让 Agent 读取并修改**它自己跑在其中的那份配置**。它们总是被挂上，而和别的
非 MCP 工具一样，只有 `tools:` 清单点了名的 Agent 才**看得见**它们：

| 工具 | 它回答什么 | 答的是哪一边 | 档位 |
|---|---|---|---|
| `config_get` | 单个文件（`{path}`：正文、revision、是否热加载），或 `{}`：分区表、现有文件、以及**进程手里**那份配置。带 `impact` 时它答的是另一个问题：谁依赖这个文件，下一轮会重建谁 | 单个文件的正文来自**磁盘**（`disk`）；概览两边都带 | allow |
| `config_validate` | `{path, content}`：这份内容能不能加载，**错在第几行** —— 行号由服务端给。`{}`：检查磁盘上这棵树 | **磁盘**（`disk`） | allow |
| `config_template` | `{path}`：这个文件从什么骨架开始。`{section}`：那个分区的模板。都不给：一份「能问什么」的索引 | 两边都不是 —— 骨架既不是文件也不是树 | allow |
| `config_notices` | loader 发现了但没拒绝的事：改了名的键、退休的目录、回落到缺省的值 | **运行中**那棵树（`running`） | allow |
| `config_tier` | `{tool}`：它要不要点头。`{agent}`：那个 Agent 的工具与各自档位。都不给：缺省档位与整张例外表。答案用的是**本轮跑着的那棵树**，本轮刚写入的 Agent 还看不见 —— 工具会明说这一点，而不是假装它不存在 | **运行中**那棵树（`running`） | allow |
| `config_write` | 写一个文件：对整棵树预检、对你读到的 revision 校验、合并失败就整份拒绝。**下一轮**生效 —— `applied` 说的是监视器会接住这份改动，不是说本轮就能看见 | 写入落在**磁盘**上；答案里两边都带 | **ask** |

★ **每个答案都说自己答的是哪一边。** 那个字段叫 `source`，取值是 `disk`（此刻磁盘上的
文件）或 `running`（本轮据以建树的那棵）。两者并不矛盾 —— 它们答的是不同的问题：本轮早先
写下的改动已经在磁盘的正文里，还不在运行中的树里。

★ **概览与写入的答案改带两个版本号**：`runningVersion` 与 `diskVersion`。只要一份改动还没
被接住，这两个号就不一样 —— 而这个差别正是「我的改动落地了吗」的答案。

★ **`config_get` 还有第二个问题：`impact`。** 它不读文件，而是回答「谁依赖这个文件，
下一轮会重建谁」：

| 问法 | 里面有什么 |
|---|---|
| 带 `path` | `dependents`（这个文件对哪些 Agent 有话说）、`rebuilds`（下一轮会重建的 Agent）、`changed`（这个文件对某个 Agent 的贡献动了吗），以及改动没法不重启就生效时的 `requiresRestart` |
| 不带 | `changedFiles`（对某个 Agent 的贡献动过的那些文件），以及同样那份 `rebuilds` |

★ **影响范围的答案来自运行中那棵树**（`running`），尽管它拿磁盘跟那棵树比：它走的依赖图
是运行中那棵树的，答的是「磁盘会把那棵树改成什么样」。所以带 `path` 的 `config_get` 答
`disk`，带 `impact` 的 `config_get` 答 `running` —— 两个问题，两边。

★ **一份改动只到达读了那个文件的 Agent。** 下一轮重建的是**自己的依赖动过**的那些
Agent —— 从没读过那个被改文件的 Agent，手里那份原样保留。所以改一个 Agent 的文件，
重建的是它和它的父 Agent，其余的树不动。

★ **`dependents` 是直接的，`rebuilds` 是传递的。** 父 Agent 带着子 Agent 的身份，所以子
Agent 一变，父也得重建：改 `agents/skill-expert.md`，`dependents` 里只有 `skill-expert`
自己，`rebuilds` 里还有它的父。

★ **`requiresRestart` 是冷分区上诚实的答案。** `server/` 下的文件配的是进程，不是
Agent：没有任何东西到达 Agent，所以答案是「什么都不会变，而且你得重启」，而不是「什么
都不会发生」。

```markdown
---
# .openheron/agents/main.md
tools: [ls, read_file, glob, grep, task, write_todos, config_get, config_validate]
---
```

★ **先给 `config_get` 和 `config_validate`，再给 `config_write`。** 前两个只读；第三个
改配置，而且每次都会停下来等审批。能校验不能写的 Agent 是个有用的审阅者；只会写的
Agent 是一次迟早要来的加载错误。

★ **`config_write` 写不了 `server/`，而 `config_get` 读得了。** 那一节配置的是进程自己，
不热加载，改了要重启 —— 所以写入路径直接拒绝它，而不是存下去一个悄悄不生效的值。
读是另一个问题，而且正是 agent 想知道「进程到底跑着什么」时会问的那个。

★ **文件工具走的是同一套检查。** `write_file` 与 `edit_file` 落在配置根里的路径会交给
配置服务，于是同样的闸门照跑：树必须还装得上、明文的密钥不许落进模型档案里、能跑的
Agent 不许从树里消失。★ `edit_file` 也经服务读文件，所以它带着 revision，跟它抢跑的那次
改动会被拒掉；`write_file` 不带 revision，所以整篇写入是「后写者赢」—— 闸门照样跑。

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
| **标题栏** | Agent 过滤（每个 chip 只写 agent 名，悬停看它的上下文占用与用量）、主题开关，以及这一轮的状态 |
| **侧边栏** | 会话列表、「新建会话」按钮、设置入口 |
| **输入框** | 提问；`/` 开头触发斜杠命令；支持拖拽/粘贴附件 |
| **消息列表** | 助手回复、思考过程、工具调用卡片、子 Agent 产出（带归属） |
| **审批卡** | 需要你点头时出现，同意 / 拒绝（可附理由） |
| **工具卡** | 每个工具调用的参数与结果 |
| **设置** | 配置编辑器的入口 |

★ **主题开关**在标题栏上：浅色或暗色。在你手动选之前，它跟着你的系统走；选过之后就记住了。
★ 回答里的**表格有自己的宽度**：它占满整列，长句子在自己的格子里换行，
而列很多的表在**自己的框里**横向滚动，不会把整页推歪。

### 6.1 斜杠命令

| 命令 | 作用 |
|---|---|
| `/new` | 新建会话 |
| `/stop` | 停掉当前这轮 |
| `/attach` | 往**下一条**消息附加文件 —— 见 §6.9 |
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

★ **被打断的那一轮回到它开始时的那棵树。** 为审批而暂停的那一轮会记下它跑的是哪棵树，
恢复时继续用那棵树 —— 所以期间改的配置不会改变那一轮的结论。这份改动在**下一轮**生效
（§4.9）。★ 期间 openheron 重启过的话，那棵树已经没了：这一轮改用当前那棵继续跑，
日志里会说明。

### 6.3 配置编辑器

`/settings` 打开。它能改：`llm/` `agents/` `tools/` `mcps/`。

写入有几道闸门：

- **浏览器里不再做任何校验。** 编辑器把草稿发给服务端，显示服务端那份答案 —— 每条 issue
  带着它所在的行。打字会 250 ms 防抖，属于旧草稿的回包直接丢掉。
- **每条 issue 都点明它说的是哪个文件。** 别的文件上的问题显示在**那个**文件的名字下面，
  而且它是个链接 —— 点一下编辑器就打开那个文件。你自己这个文件的问题就留在自己身上。
  ★ 连解析都解析不了的文件仍然会拖垮整棵树，所以别的文件坏成这样，这次保存照样会被拒，
  状态栏会写出是哪个文件坏在哪儿。
- **灰色那行是 hint，红色那行是 error。** hint 说的是「这棵树少了一样东西」，它什么都不拦；
  error 才是让 Save 按不下去的那个。模型没声明的 Agent 得到的是**它自己文件上的一条 hint**：
  整棵树少它照样装得上（见 §4.1），所以眼前这个文件可能一点毛病没有。
- **「让一个本来能跑的 Agent 掉出树」的改动会被拒。** 把某个 Agent 的模型改成一个没人声明的，
  它本来会一声不吭地消失。拒绝显示在**你正在编辑的文件**上 —— 那正是被改的东西 —— 并且在
  那个 Agent 回到树里之前 Save 一直是禁用的。新增 Agent、或者改一个不相干的地方，永远不会
  这样被拒。
- **「还有东西指着它」的删除会被拒，而且拒绝会点名是谁在指。** 还有一个 Agent 挂着
  `mcps/fetch.yaml` 时把它删掉，那个 Agent 就指着一个不存在的服务器了。那句话会写明
  **是哪个文件还在指着它**、**要移除什么** —— 先把引用去掉，再删这个文件。
  模型档案（某个 Agent 在用）与被委派的 Agent（另一个 Agent 在委派它）也一样。
- `server/` **不可编辑**（不热重载，改了要重启）。
- **密钥字面量被拒** —— `api_key must be a reference, not a literal`。
  要用 `${ENV_VAR}` 或 `@file:/path`。
- **没有 YAML 内容的文件被拒** —— 纯空、或者只有注释的文件根本没有配置。报错会点名
  这个文件需要哪些字段；在 `tools/` 下还会说清是哪个工具缺了什么。
- **`mcps/` 下的文件必须叫 `<server>.yaml`**，服务器名字还要匹配 §4.7 的模式 ——
  别的名字在保存时就被拒。
- **乐观并发**：如果文件在别处被改过，保存会报冲突，让你选覆盖还是放弃。
- **新建文件的文件名被服务端拒绝时，对话框不关**：你写的东西还在，服务端那句话就在输入框
  旁边 —— 文件名规则是服务端的（`server/` 只读、`tools/` 只认五个名字、`mcps/` 只认
  `<server>.yaml`），所以拒绝是从服务端回来的，不是对话框里那份副本给的。

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
  `starting`、`unreachable` 或 `unused`；
- 每个工具一行：服务器自己的名字、暴露出来的名字（`mcp__github__create_issue`）、
  档位（`allow` 或 `ask`）、以及状态。

一个服务器都没配时，对话框说的是 "No MCP servers are configured"。

★ **打开对话框会把它问到的那些服务器启动起来**，所以它能回答「我的服务器是不是坏了」——
否则答案只能是「跑一轮看看」。这个 Agent 没用到的服务器是 `unused`，不带工具行。

★ **起不来的服务器不会让这一轮停掉。** 这个 Agent 照跑，只是没有那个服务器的工具 ——
其余的工具不受影响 —— 而对话框里那个服务器旁边就带着原因。
一个坏掉的服务器，代价只有它自己那些工具。

★ **「正在启动」是它自己的一态：`starting`。** 服务器在配置一被读到时就在后台起起来，
所以第一轮通常赶得上。一台还在起的服务器写着 `starting`，而且**不带错误** ——
还没有错误，它正在路上。它带来的工具在**下一轮**进来。

**Refresh** 按钮会重读一次状态，所以你改完服务器不用关掉对话框就能看到效果。

### 6.9 发附件

三个入口，最后都落在同一个地方 —— 输入框上方的一排 chip：

| 怎么进来 | 会发生什么 |
|---|---|
| **回形针**按钮 | 打开文件选择器 |
| **把文件拖**到输入框上 | 拖拽经过时输入框会高亮 |
| **粘贴** | 剪贴板里有图时 Ctrl+V 就贴上了 —— 不用「先存成文件再选」 |

★ **文件是挂在一条*消息*上的，不是挂在会话上的。** 输入框上方的 chip 是等着
跟你的**下一条**消息一起发出去的；发出去之后它们就离开输入框，出现在那条消息
本身。已经发出去的文件留在那条消息上 —— 几周之后刷新一下，你仍然看得出
当时发的是哪一个文件。

★ **模型是真的拿到它们了 —— 靠自己去打开。** 图片是作为图片放进去的，所以模型是
**看见**它，而不是把字节当文本读；其它文本文件在消息开头以 `@` 行的形式给出绝对
路径，模型用 `read_file` 去读。★ 文件内容不在请求里，所以模型第一件事就是打开那个
文件 —— 文件很大时用 `offset` 和 `limit` 分段读。★ 二进制附件（PDF、压缩包）会在
提示词里被标成「暂时不能处理」：模型会如实告诉你，而不是假装读过。
★ **没有自动兜底。** 模型看不了图片时，这一轮会带着 provider 的错误结束；模型没去
读的文件也不会在背后重发一遍。如果回答看起来像是没读过文件，就再提示模型读一次。

★ **收下图片不等于看见图片。** 图片有没有真的进到模型的眼里，是 provider 的事：
有的模型会读它，有的模型收下请求却不看它（DeepSeek 的 `deepseek-v4-pro` 就是
这样）。★ 这种情况下模型会**自己说出来** —— openheron 没有办法在配置里声明一个
模型支不支持视觉，所以它不假装知道。

★ **chip 上的 × 是真删。** 不只是从列表里拿走：文件也从会话的 workspace 里
删掉了。★ 已经随消息发出去的文件**不会**被删 —— 那条消息说了这个文件是问题
的一部分，删了它，记录就在说谎。

★ **文件在发之前能看，发之后也能看。** 图片在 chip 上是缩略图，其它文件是
它那一类的图标。点一下就地打开：图片是原图，PDF 用阅读器，Markdown 渲染出来，
代码带高亮，文本就是文本。★ 上方消息里的附件同样如此 —— 它们是历史，所以没有
×，但照样打得开。★ 文件已经不在了（被 × 删了，或者 workspace 被清过）时，
回退成图标，而不是留一个碎图。

★ **图片在进来的时候会被压缩。** 格式不变 —— PNG 仍是 PNG，透明也留着 ——
长边超过 1600 像素时会被缩下来。★ GIF 与 WebP 原样存：前者会丢动画，后者
在这里没法重新编码。★ 文件在 workspace 里的**名字**带一小段哈希，所以同名传
两次是两个文件，而不是后者盖掉前者 —— 而界面上仍然显示你认得的那个名字。

★ **刷新不会丢东西。** 还没发出去的文件会回来（服务端留着它们），已经发出去的
文件留在各自的消息上。

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
配置根是空的，而服务端没能自己写出来 —— 它会在第一次启动时写。启动日志里说了自动
初始化为什么失败；在项目根跑 `openheron init` 做的是同一件事，只是模型和 provider
由你定。

**Q：报 `config: load <path>: agents/main.md does not exist or could not be parsed`**
两件事之一：配置根指错了（用 `--config-dir` 或 `$OPENHERON_CONFIG_DIR` 指对），
或者 `agents/main.md` 的 frontmatter 写错了键。

**Q：改了 `agents/main.md` 没生效？**
`agents/` 是热重载的，应该自动生效。没生效看启动日志里的 reload 错误。
`server/` **不热重载**，要重启。`skills/` 也是热的 —— 配置根下的 `SKILL.md` 下一轮就生效
（§4.6）—— 但技能目录在别处就不在监视范围内。

**Q：改了某个 Agent 的文件，别的 Agent 也会跟着变吗？**
不会，而这正是要点。一份改动只到达读了那个文件的 Agent：下一轮重建的是你改的那个
Agent 和它的父 Agent，其余的照旧留着。改之前想知道这个集合里有哪些，带 `impact` 的
`config_get` 会告诉你（§4.9）。

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
打开 `/mcps`（见 §6.8）。这个 Agent 没挂载的服务器写着 `unused`；够不着的工具写着
`blocked`，括号里的理由会点名 `mcps` —— 这个 Agent 没挂载那个 server。
写着 `unreachable` 的服务器旁边带着出错原因，说清它为什么起不来。

**Q：MCP 服务器起不来？**
启动报错会点名是哪个文件的哪个字段。`mcps/` 是热的，所以改完下一轮就生效 —— 不用重启。

**Q：面板说某台服务器 `starting`？**
它正在起：配置一被读到，服务器就在后台起起来。它带来的工具在**下一轮**进来 ——
按 **Refresh** 就看得见。

**Q：端口被占 / 界面打不开？**
`listen_addr` 改端口，或用环境变量 `LISTEN_ADDR` 覆盖。

**Q：想看服务到底加载了什么？**
`GET /v2/config` 返回生效配置；`GET /v2/health` 返回健康状态和 `deferred` 告警；
`GET /v2/runtime` 返回运行时状态。启动日志也会打印配置根路径。

**Q：Windows 上 agent 的 shell 工具失败，或者跑的是模型按另一套系统写的命令？**
`execute` 工具在服务启动时挑自己的 shell —— 见 §1.3（「在 Windows 上」）。它优先要
POSIX shell，机器上一个都没有时退回 `cmd.exe`；两种情况工具的描述都会告诉模型
该写哪一套方言，于是命令是照着真正跑它的那个 shell 写的。★ 启动日志会打印它挑中
的是哪个 shell。

**Q：没有 ripgrep 时，`grep` 工具是不是就弱了？**
它用自己的引擎搜：同样的正则、同样的上限、同样的预算，对「这个文件在不在」给出的
答案也一样。少的是：不读 `.gitignore`，认识的文件类型更少 —— 不认识的类型会被
拒绝，而不是猜一个。★ 启动日志会打印挑中的是哪个引擎；想用另一个就装上 ripgrep。
