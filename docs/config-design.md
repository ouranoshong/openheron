# 配置系统设计

> 一句话：配置是一棵**文件树**，不是一个文档；它决定「谁是谁」与「什么要不要人点头」。
>
> 范围：配置根、分区、文件规格、加载与优先级、工具表面、热更新。不包含 HTTP 接口层
> （见 `docs/api-v2-design.md`，上游）与前端。
> 本文按 AGENTS.md 的文档语言规则**只有中文版** —— 架构与代码设计类文档不做双语。
> 每个结论都指到具体文件与行号。

## 0. 一句话

配置不是一份文档，而是**一棵文件树**，树上的每一片叶子回答两个问题：**谁是谁**（哪个 Agent、哪个模型、
哪个工具、哪个 MCP server）与**什么要不要人点头**（一个工具的档位是 `allow` 还是 `ask`）。

这两个答案分别住在两处，**互不重叠**：身份住在文件里；档位住在**提供**该工具的那个文件里。
§6 把这条分工讲完，其余各节讲它怎么落到代码上。

★ 三条贯穿全文的纪律：

1. **只有一份配置生效** —— 配置根三级解析，从不向上找祖先目录（§3）。
2. **缺省是 `ask`，例外被点名** —— 「不被暴露」只在两处说：文件的 `allows:` 与 agent 的 `tools:`（§6.2）。
3. **「配了没用」必须出声** —— 认识但未兑现的键进 `deferred`，启动时逐条打印（§4.3）。

## 1. 目标与非目标

### 目标

| # | 目标 | 可验证判据 |
|---|---|---|
| 1 | **一个**配置根 | 任一时刻只有一份配置生效，没有继承链（`agentkit/spec/config.go:425`） |
| 2 | 按功能分区 | 新增一类配置不改已有文件布局（`agentkit/spec/config.go:59-104`） |
| 3 | 「配了没用」必须可见 | 未知字段报错；认识但未兑现的键进 `deferred` 并**强制打印**（`agentkit/spec/config.go:203`、`main.go:415-417`） |
| 4 | 密钥不入库 | 档案只存 `${ENV}` 引用或 `@file:` 路径 —— 编辑器拒绝保存明文（`agentkit/spec/write.go:926-932`） |
| 5 | 零新增依赖 | `go.mod` 只用已在缓存的 `gopkg.in/yaml.v3 v3.0.1`（`go.mod:26`）—— YAML 这一层就是全部成本 |

### 非目标

- ❌ **配置根不做上溯搜索。** 这条规则写在 `NewService` 的文档注释里（`agentkit/spec/config.go:412-424`）。
- ❌ **没有家目录那一层。** `~/.openheron` 永远不会被自动认领 —— 若它存在而不在用，运行时会把话说明白，
  请你显式指名（`main.go:123-140`）。
- ❌ **不让配置承载密钥本体。** 加载器容忍明文密钥（`agentkit/spec/expand.go:10-38`），但编辑器拒绝保存
  明文（`agentkit/spec/write.go:934-942`）—— 两条路径故意不对称。
- ❌ **不做字段级合并。** 只有一份配置，没有「谁覆盖谁」；字段级合并让配置来源不可辨。
- ❌ **不让 `server/` 参与热更新。** 它配的是进程自身，改了要重启（`agentkit/spec/config.go:71-74`）。

## 2. 目录布局

```
<projectRoot>/.openheron/
├─ agents/main.md            ← agent: frontmatter + body (the body is the prompt)
├─ llm/<provider>.yaml       ← vendor profile: base_url / api_key / models[]
├─ server/config.yaml        ← the process itself: address / dirs / timeouts / the caps
├─ tools/<tool>.yaml         ← http / web_search / wikipedia / filesystem / kb
├─ mcps/<server>.yaml        ← one MCP server: command / transport / tier
├─ skills/<name>/SKILL.md    ← skills (resolved by their own rule, see §5.4)
└─ state/                    ← runtime data, never committed
```

分区清单住在**一处**：`Categories`（`agentkit/spec/config.go:59-104`），每个分区带一个 `hot` 标志。

| 目录 | 类别常量 | 可编辑 | 热 | 内容 |
|---|---|---|---|---|
| `agents/` | `spec.DirAgent`（`agentkit/spec/config.go:19`） | ✅ | ✅ | 每个 Agent 一个 `.md` |
| `llm/` | `spec.DirLLM`（`:20`） | ✅ | ✅ | 每个厂商一个 `.yaml` |
| `tools/` | `spec.DirTools`（`:24`） | ✅ | ✅ | 一个工具一个 `.yaml`（外加知识库） |
| `mcps/` | `spec.DirMCP`（`:26`） | ✅ | ✅ | 一个 MCP server 一个 `.yaml` |
| `skills/` | `spec.DirSkills`（`:22`） | ✅ | ✅ | `skills/<name>/SKILL.md`，**嵌套**分区，独立解析（§5.4） |
| `server/` | `spec.DirServer`（`:21`） | ❌ | ❌ | 进程参数，需重启 |
| `state/` | `spec.DirState`（`:23`） | — | — | 会话与 checkpoint，自动创建 |

★ **`state/` 不是一个配置分区。** 它不在 `Categories` 里 —— 它是运行数据，装载器不读它，编辑器也不写它。

★ **没有 `policy/` 分区。** 那个分区退役了：档位住在**提供**工具的那些文件里，而上限住在
`server/config.yaml`（§6.2、§5.5）。一份仍然带着这个目录的配置**不会被读**，但它**也不沉默**：加载器把
这个退役分区记进 `deferred`，并说明它的内容去了哪儿（`agentkit/spec/config.go:603-609` 的
`retiredDirs`、`:618-627` 的 `noteRetiredDirs`）。

空的子目录**不自动创建**：目录不存在就当「没有东西可加载」（`agentkit/spec/config.go:669-672`）。
★ **配置根本身是例外** —— 缺省的那份根不存在时会被建出来（`agentkit/spec/config.go:451-458`），
因为「还没有配置」和「目录不在」本来就是同一件事。

★ **没有 `.config.lock`。** 配置编辑器走的是**内容哈希的乐观并发**：`Revision` 是文件 SHA-256 的前八位
十六进制（`agentkit/spec/write.go:381`），带着过期 `baseRevision` 的写入会被判为冲突
（`agentkit/spec/write.go:894-911`）。

`state/` 取代了旧的 `DATA_DIR=./data`，由 `Service.DataDir` 创建（`agentkit/spec/config.go:492-504`）：

```
.openheron/state/
├─ sessions/<id>/
│  ├─ session.jsonl      ← the messages (JSONL)
│  ├─ state.json         ← pending approval / title / counts / audit
│  ├─ checkpoints/       ← this run's runner state (one store per session)
│  │  └─ <checkpointID>.ckpt
│  └─ workspace/         ← this session's own working directory
└─ kb/<index>/           ← the knowledge base index
```

★ **`state/` 里不落日志。** 运行日志落在**配置根**下 —— 代码缺省是**只走标准错误**（`log.file`
为空串），而**出厂模板给的是 `logs/openheron.log`**（`server/config.yaml` 的 `log.file`），所以
新项目开箱就有一份落盘的日志。路径相对**配置根**解析，打不开文件只告警、不阻止启动
（`agentkit/spec/config.go:515-527` 的 `Service.LogFile`；见 `docs/runtime-design.md` §4.3）。
★ 出厂模板的 `log.console` 是 `false` —— **文件就是全部的日志**：配置读出、文件打开之后，
标准错误就安静了（`main.go:336`）。
★ 于是 `logs/` 与 `state/` 是同一类东西 —— **运行数据**，进版本库之前要把它排掉。

### 2.1 版本控制边界

```
# Runtime data does not go into version control — the config wants diff / blame / rollback, session content must not leak
state/
logs/
.config.lock

# Safety net for plaintext keys (the normal way to write them is ${ENV} or @file:)
llm/*.local.yaml
*.key
```

（仓库根的 `.openheron-example/.gitignore` 原文。）★ **为何 `state/` 必须排除**：会话是高频变动数据，
进版本库会把历史撑成噪声，且**泄露会话正文**。配置反过来**建议**进库 —— 那样「这台机器上跑的是什么」
才有答案、可 diff、可 blame、可回滚 —— 但那是建议，不是前提：全放本地、只自己看，同样跑得好好的。
★ 在**本**仓库里，本地配置根不入库（`.gitignore` 排除 `/.openheron/`），而样例配置**是**入库的，
放在 `.openheron-example/` 下。

### 2.2 出厂样例

仓库根的 `.openheron-example/` 是一套**经过加载验证**的配置：`agents/` 下的四个 agent、
`llm/openai.yaml`、`server/config.yaml`、`skills/refactor/SKILL.md`，外加那份 `.gitignore`。
它的 `README.md` 列了三条最容易卡住人的规则：`${ENV}` 未设会在启动时**提醒**（不阻塞）→
`model` 必须带 `<provider>/` 前缀、时长必须带单位。

★ **要一份「最小可运行」的**，不必手抄：`openheron init` 在**当前目录**建出一份配置，**只补确实缺的
东西** —— 绝不覆盖已有的同名文件（同名文件是**整体替换**语义）；给了 `--config-dir` 就写到那儿，否则
写 `<目标目录>/.openheron`。它写什么（`initcfg/initcfg.go:56` 的 `BuildPlan`、`:198` 的 `copySection`）：
`agents/` 下的四个 agent、`llm/<provider>.yaml`、`server/config.yaml`、`tools/` 下的五个文件、`mcps/` 下的
三台服务器（`fetch` `time` `deepwiki`），以及 `skills/` 下的五个技能 ——
`openheron-config-<name>/SKILL.md` 四个，加上 `openheron-agent-authoring/SKILL.md`。
★ **`tools/` 这一段是默认写的**：那五个文件是这棵树的一部分 —— 出厂的 agent 会点名 `request_get` /
`duckduckgo_search` / `wikipedia_search`，而没有 provider 提供的名字会被当成笔误报出来；想留下它就加
`--no-tools`。★ **没有 `policy/` 这一段 —— 那个分区已经没了。**

★ **服务端第一次启动时写的是同一棵树。** 缺省根下没有 `agents/main.md` 时，它在装载配置之前先把
出厂树写出来（`main.go:177` 的 `initConfigIfEmpty`），用的就是上面那份计划 —— 所以
「服务端写的」与「命令写的」是同一棵树。★ **点名的根不写**：理由见 §3。

## 3. 配置根：三级，没有探测

配置得先知道项目根才能读任何东西，而项目根本身又是配置项 —— 鸡生蛋问题。openheron 的解法是
**不探测**：配置根要么被点名，要么从一个名字推导出来。

```
① resolve the config root (nothing can be read yet)
   --config-dir                main.go:98
     ↓ unset
   $OPENHERON_CONFIG_DIR       agentkit/spec/config.go:440
     ↓ unset
   <project-root>/.openheron   agentkit/spec/config.go:443, created when missing
```

实现在 `agentkit/spec/config.go:425` 的 `NewService`，而它的文档注释（`:412-424`）写明了为什么
**不向上查找祖先目录**：探测让生效的配置根取决于工作目录，于是同一条命令换个目录跑就会读到另一份
配置，测试实例也会读到开发者自己的配置。★ 差别在代码的形状上就看得见：`NewService`
（`agentkit/spec/config.go:425-460`）是**三个**分支、**没有循环**。

★ **只有默认出来的那个根才会被创建**（`agentkit/spec/config.go:451-458`）：`--config-dir` 或
`$OPENHERON_CONFIG_DIR` 指向一个不存在的地方是用户笔误，它不能变成一棵悄悄空着的配置树。
`Service.RootIsDefaulted`（`agentkit/spec/config.go:488`）就是为了把两者分开 ——
`main.go:149-157` 给出的是 "no configuration at <root>: run `openheron init` to create one"，
而不是 "agents/main.md does not exist"。★ **还有第二个入口，给 `init` 用**：`openheron init` 走
`spec.NewServiceAt`（`agentkit/spec/config.go:462-478`），从不读 `$OPENHERON_CONFIG_DIR` —— 否则 init
会把新配置写到用户没指的地方去。

★ **家目录也不是兜底。** 如果 `~/.openheron/` 存在却不是正在用的配置根，进程会在启动时说一声
（`main.go:123-140` 的 `warnUnusedHomeConfig`）：配置根从不被搜出来，所以那个目录必须被点名。

★ **默认根下还没有 `agents/main.md` 时，服务端自己把树写出来**（`main.go:177` 的
`initConfigIfEmpty`）：写的还是 §2.2 那份计划，模型用 provider 模板自己的缺省
（`deepseek/deepseek-flash`），密钥从环境变量读（`DEEPSEEK_API_KEY`）—— 与不带参数的
`openheron init` 完全一致。★ **「空」的判据是「没有 agent 文件」**：已经有 `agents/main.md`
的树就是一份配置（能不能装载是另一回事），这份初始化不碰它；计划本身只补缺的那些，
所以半写状态的树是被补齐，不是被替换。

★ **点名的根不写**：`--config-dir` 与 `$OPENHERON_CONFIG_DIR` 指的是用户自己选的目录，
它空着是用户的决定，不是一个缺口。★ 这条同时是**测试实例能隔离**的前提：测试实例点
自己的根，所以自动初始化在它那儿永远不发生 —— 那棵树的每一份文件都是脚本放进去的。

### 3.1 两个「根」：`Root` 与 `ProjectRoot`

配置根与项目根是**两个不同的目录**，代码把它们分得很清：

| 方法 | 值 | 用途 |
|---|---|---|
| `Service.Root()`（`agentkit/spec/config.go:480`） | 配置根本身 —— 默认是 `<project-root>/.openheron` | 读配置；`state/` 住在它下面 |
| `Service.ProjectRoot()`（`:490`） | 项目根 —— `--project-root`，否则是工作目录 | agent 的工作目录；相对路径的解析基准 |
| `Service.DataDir(v)`（`:492`） | `v` 是绝对路径就原样返回，否则落在**配置根**下；不存在则创建 | 会话与 checkpoint |
| `Service.LogFile(v)`（`:515`） | 同上，相对**配置根** | 日志文件 |
| `Service.ResolveDir(v)`（`:529`） | `v` 是绝对路径就原样返回，否则落在**项目根**下 | `static_dir`、知识库语料 |

★ **两者是解耦的。** `--config-dir /etc/openheron-cfg` 把配置放到别处，而 `--project-root`（默认：工作
目录）仍然是 agent 干活的地方。不解耦的话，「配置根的父目录」就会变成 `/etc` —— agent 去那儿 grep/glob。
★ **默认的项目根是工作目录，不是配置根的父目录。** 配置根取默认值时两者恰好重合，但配置根一旦被点名，
两者就不再重合。用错根的两种典型故障：前端去配置根下的 `web/dist` 找资源 → **403/404**；agent 被告知
「项目根 = 配置根」→ 在错误目录里 grep/glob。

### 3.2 相对路径的解析基准

**相对路径并不是都相对同一个基准。** openheron 有两个：

| 路径 | 基准 | 代码 |
|---|---|---|
| `server.data_dir`（相对） | **配置根** | `agentkit/spec/config.go:497-499` |
| `server.log.file`（相对） | **配置根** | `agentkit/spec/config.go:520-522` |
| `server.static_dir`（相对） | **项目根** | `agentkit/spec/config.go:529-535`，用在 `main.go:614` |
| `kb.corpus_dir`（相对） | **项目根** | `agentkit/spec/load.go:1045-1048` |

★ **这个划分是刻意的：状态跟着配置走，工作树跟着项目走。** 相对的 `data_dir` 在配置被搬走时
跟着搬；相对的 `static_dir` 与相对的知识库语料属于被服务的那个项目，配置搬走时它们不该跟着搬。

★ **`PROJECT_ROOT` 生效得比配置根晚。** `setupConfig` 先解析出 service（`main.go:368`），之后
才用环境变量覆盖运行期的项目根（`main.go:447`）。于是 `PROJECT_ROOT` 挪的是 agent 的工作目录，
**挪不动**默认出来的配置根 —— 那仍然落在工作目录的 `.openheron/` 下。要一起挪，传 `--project-root`。

### 3.3 测试实例靠「点名的配置根」隔离

测试环境集中定义在一处：仓库根的 `.env.test`（已提交）加 Taskfile.yml 的目标。openheron 本体
:8080 / **:18099**，Vite :5173 / **:15173**，E2E 假后端 — / **:18180**；测试实例由
`scripts/test-backend.sh` 启动（`task test-env-backend` / `test-env-reset` / `test-env-sync`），它点名了
必须隔离的三个环境变量：`OPENHERON_CONFIG_DIR`（**配置根**，唯一的那一份）、`DATA_DIR`（会话数据）、
`LISTEN_ADDR`（`127.0.0.1:18099`）（`scripts/test-backend.sh:128-130`）。

★ **端口与状态必须一起隔离。** 换个端口不等于数据隔离：openheron 把会话写在配置根下的 `state/`，
测试写在那里就污染了真实历史。测试实例是**点名**自己的配置根的，而配置根**从不上溯**
（`agentkit/spec/config.go:412-424`），所以仓库根那个 `.openheron/` **结构上就够不着** —— 隔离靠的不是
「一次恰好停在那里的探测」，而是一个被点名的根。

★ **配置根是预置出来的，不是探测出来的。** 脚本会创建 `$PROJECT_DIR/.openheron/` 与
`$PROJECT_DIR/.openheron/server/`（`scripts/test-backend.sh:47`），并写一份默认的
`server/config.yaml`（`:90-105`）。没有它们，第一个报错是 "agents/main.md does not exist or could not be
parsed" —— 读起来像文件丢了，而不像环境配错了。

★ **`agents/ llm/ skills/ tools/ mcps/` 是复制，不是软链**（`scripts/test-backend.sh:76`）。它们是
**输入**，测试实例要用真 agent 才跑得通一整轮；复制而非软链，是为了测试跑崩时不会写坏开发配置。
`server/` 与 `state/` 刻意**不在**复制列表里 —— 所以 `task test-env-sync` 是**必须的**，不是可选的：
改了开发侧的 agent 定义而不重新复制定义，测试实例跑的还是旧的。★ **端口表只在一个文件里**
（`.env.test`）：Vite、Playwright 和各个冒烟脚本都从它读，改一个端口只需动一个文件。

## 4. 加载与优先级

```
built-in defaults
   ↓ pre-fill the struct
<config root>/               the one and only
   ↓ explicit override
environment variables        highest
```

★ 这条链**大部分是假的**：真正会覆盖值的环境变量只有 7 个，而且它们是在**使用点**被读到的，
而不是靠一层通用覆盖（§4.4）。

### 4.1 缺省值靠「预填充结构体再 decode」

加载器先用内置缺省值**预填充**配置结构体，再把每个文件 decode 到它上面
（`agentkit/spec/config.go:564-593` 的 `loadOnly`）：`server/` 段从 `defaultServer()` 起步
（`agentkit/spec/config.go:570`，定义在 `agentkit/spec/defaults.go:57-75`），知识库从
`defaultKB()` 起步（`agentkit/spec/config.go:571`，定义在 `agentkit/spec/defaults.go:48-55`）。

★ 没有第三份预填充：档位表**不再**是一个配置段 —— 它由提供工具的那些文件装配而成（§6.2）。
`loadServer` 是 decode 到预填充结构体的一份**副本**上（`agentkit/spec/load.go:140-144`），于是
文件里没出现的键保留缺省值。

「只写文档里出现过的键」这个行为不是 openheron 自己写的 —— 它是 yaml.v3 的：`mappingStruct`
遍历文档的键，只写它找到的字段，**没出现的字段一个都不碰**。⇒ 逐层 decode 天然实现「只覆盖出现
的键」，无需自己写 merge 逻辑。

### 4.2 严格模式

每个分区都走同一个严格解码器（`agentkit/spec/config.go:740-748`）：

```go
func decodeStrict(data []byte, v any) error {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return ErrEmptyDocument
		}
		return err
	}
	return nil
}
```

**为什么严格**：宽松模式下拼错字段名会**静默用默认值**，而「为什么我的配置没生效」是排障成本
最高的一类问题。yaml.v3 的报错自带行号：`line N: field X not found in type Y`。

★ **空文档不是解析错误。** 解码器把一份没有 YAML 的文件报成 `io.EOF` —— 这话是真的，却毫无用处：
「EOF」没告诉用户该做什么。openheron 把它换成 `ErrEmptyDocument`（「the file is empty (or contains
only comments)」，`agentkit/spec/config.go:738`），因为「只有注释的模板」是一种真实存在的文件形状：
照着模板建出来的 `tools/` 文件根本没有文档（`agentkit/spec/load.go:236-240` 的 `decodeTool`）。

### 4.3 「配了没用」的三种形态

| 形态 | 处理 | 例子 |
|---|---|---|
| **不认识的键** | 硬报错，带行号 | `max_retry`（拼错） |
| **认识但本版不兑现** | 进 `deferred[]`，**启动必须逐条打印** | `models[].summarize_at` 仍会被解析，但不再被读 —— 阈值归 agent 管 |
| **认识但此刻缺运行期前提** | 同上，且**不阻塞启动** | `api_key` 的 `${ENV}` 未设 —— 请求该端点时才 401 |

`Config.Deferred()`（`agentkit/spec/config.go:203-213`）收集这些句子；启动时逐条打印
（`main.go:415-417`）：

```go
for _, d := range c.Deferred() {
	log.Info("[main] config notice", "notice", d)
}
```

★ **一条提示带着它属于哪个文件。** 内部存的是 `Notice{Path, Message}`
（`agentkit/spec/config.go:189-195`），`Deferred()` 把路径拼回消息 —— 启动日志那一行因此**一个字
都没有变**，而诊断接口能拿到结构化的路径。产生提示的有两个入口：`Config.deferFile`（有文件，
`agentkit/spec/config.go:258`）与 `Config.deferKey`（没有文件，比如退役的分区、watch 的设置，
`:246`）。★ **路径是一个字段，不是消息的前缀** —— 路径由调用点原样给出，消息里不再有它。

今天全部的产生者：

| 产生者 | 它说的是什么 | 属于哪个文件 |
|---|---|---|
| `agentkit/spec/load.go:44-46` | `api_key` 的 `${ENV}` 未设 —— 真正发请求时会 401 | `llm/<provider>.yaml` |
| `agentkit/spec/load.go:64-68` | `models[].summarize_at` 不再被读 —— 阈值归 agent 管 | `llm/<provider>.yaml` |
| `agentkit/spec/load.go:407-410` | 键 `corpus` 已改名为 `corpus_dir` —— 旧键仍然能加载 | `tools/kb.yaml` |
| `agentkit/spec/load.go:552-555` | MCP server 的 `env:` 里某个 `${ENV}` 未设 —— 这一条被略掉 | `mcps/<server>.yaml` |
| `agentkit/spec/load.go:552-570` | MCP server 的超时不是时长 —— 用缺省值 | `mcps/<server>.yaml` |
| `agentkit/spec/load.go:844-847` | `tools:` 点名了一个不存在的工具 | `agents/<id>.md` |
| `agentkit/spec/load.go:844-855` | 点了 `skill` 工具却没有 `skills` —— 技能一个也没有 | `agents/<id>.md` |
| `agentkit/spec/load.go:968-972` | `tools:` 里点名了 MCP 工具 —— 那一行不授予任何东西 | `agents/<id>.md` |
| `agentkit/spec/load.go:1024-1027` | 该模型没有声明 `context_window` —— 该 agent 的自动压缩不会发生 | `llm/<provider>.yaml` |
| `agentkit/spec/load.go:149-151` / `:168-171` | `log_level` 拼错 → 归一为 `info`；`log.*` 的上限是负数 → 归一为 `0` | — |
| `agentkit/spec/config.go:626` | 磁盘上还留着一个退役的分区（`policy/`）—— 它**没有被读**，而这条提示说明它的内容去了哪儿 | — |
| `agentkit/spec/reload.go:122-126` | 某个宽松时长解析不了，退回了缺省值 | — |

★ **最后这条消息对所有宽松时长都写 `watch.debounce`。** 写坏的长时被收进同一个全局列表
（`agentkit/spec/decl/decl.go:835-851`），又在同一处上报，所以 `tools/http.yaml` 的超时写错了，
也会以 `watch.debounce` 的名义出现。读它时要读成「有个宽松时长写错了」，而不是「debounce 写错了」。

`deferred` 不只是被打印：它们也出现在 `/v2/health`（`api/v2_introspect.go:482`）与
`/v2/config`（`api/v2_introspect.go:406`）里。

★ **第三形态的分界**：它**不影响启动**，因为前提缺失是**运行期状态**（密钥尚未注入、CI 里还没
export），不是配置写错。写错的东西（拼错的键、写错的 `@file:` 路径）仍归第一形态硬报错
（`agentkit/spec/expand.go:10-38`）。

### 4.4 环境变量：真正覆盖配置的只有 7 个

| 环境变量 | 覆盖什么 | 在哪里读 |
|---|---|---|
| `OPENHERON_CONFIG_DIR` | **配置根**（唯一的那一份） | `agentkit/spec/config.go:440`（名字是常量 `ConfigDirEnv`，`:131`） |
| `LISTEN_ADDR` | `server.listen_addr` | `main.go:440` |
| `DATA_DIR` | 解析后的数据目录（会话、checkpoint） | `main.go:459` |
| `PROJECT_ROOT` | 交给 agent 的项目根 | `main.go:460` |
| `STATIC_DIR` | `server.static_dir` | `main.go:614` |
| `FRONTEND_MODE` | `server.frontend_mode` | `main.go:600` |
| `EINO_EXT_SKILLS_DIR` | skills 目录 | `agentkit/build/skill.go:194` |

server 侧那三个覆盖走同一个辅助函数 —— `override`（`main.go:839-844`）在环境变量非空时返回环境值，
否则返回配置值。

★ **server 侧那三个覆盖只在启动时读一次。** `server/` 不参与热重载（`agentkit/spec/config.go:71-74`），
所以环境变量覆盖不是活旋钮 —— 改了要重启。`EINO_EXT_SKILLS_DIR` 是例外：每次解析 skills 目录都会
重新读它（`agentkit/build/skill.go:194`），所以改动会在 agent 池重建时生效。

★ 另有三个环境变量**只管日志**，不碰别的配置：`OPENHERON_LOG_LEVEL`（`agentkit/obs/logger.go:11`，读于
`main.go:267`）覆盖 `server/config.yaml` 的 `log_level`；`OPENHERON_LOG_LLM_REQUESTS`
（`agentkit/obs/httplog/logging.go:29`，读于 `agentkit/obs/httplog/logging.go:207-208`）决定是否打印请求体，
变量为空时才由配置决定；`OPENHERON_LOG_FILE`（`agentkit/obs/sink.go:12`，读于 `main.go:306`）覆盖
`server/config.yaml` 的 `log.file` —— ★ 打不开文件**不阻止启动**，进程留在 stderr。

★ 还有第三组确实从环境变量读配置，但**不在主路径上**：`MODEL_TYPE`、`MODEL_TIMEOUT`、
`MODEL_EXTRA_JSON`、`OPENAI_BASE_URL`、`OPENAI_API_KEY`、`OPENAI_MODEL`、`OPENAI_MODEL_ID`、
`ARK_BASE_URL`、`ARK_API_KEY`、`ARK_MODEL`、`ARK_MODEL_ID` —— 全部由 `build.ConfigFromEnv`
（`agentkit/build/config.go:15-44`）读取。

**`llm/*.yaml` 与 `agents/*.md` 里的字段没有环境变量覆盖。** 主路径是 `build.Resolve` +
`build.FromConfig`（`agentkit/build/provider.go:71`、`agentkit/build/provider.go:30`），只读文件。
`ConfigFromEnv` 现在只是「入口 agent 没声明模型」时的兜底（`agentkit/build/snapshot.go:257-259`），
而加载器对没声明模型的 agent 直接报错（`agentkit/spec/load.go:98-100`）—— 所以**一份合法的配置
根本走不到那里**。这是刻意的取舍：模型档案是**要被 review 和提交**的东西；给它一个进程外的高
优先级通道，等于允许「repo 里的配置和线上跑的不一样」而两边都看不出来。

★ **注入密钥的正规做法是引用，不是覆盖。** `api_key` 只能写 `${ENV_VAR}` 或 `@file:/path`，值在
加载时由 `expandValue` 展开（`agentkit/spec/expand.go:10-38`）；而没设的 `${ENV}` **不会**拦住启动
—— 它进 `deferred[]`。★ `@file:` 读不到文件**仍是硬错误** —— 那是路径写错，不是「暂时没注入」。
两者的分界：env 是运行期状态，文件路径是配置本身。★ 变量**设成空串**（`export X=`）与**未设**不是
一回事：空串解析成空密钥，且**一条提醒都不记**（`agentkit/spec/expand.go:30-35`）。

### 4.5 诊断的回答归属到文件

★ **一条 issue 说的是哪个文件，就是哪个文件。** `Issue.Path` 是配置根相对的路径
（`agentkit/spec/diag.go:26-39`），为空只表示「这条规则不针对某一个文件」。

| 问题的来源 | `Issue.Path` |
|---|---|
| 某个文件解析不出来（YAML 报错、缺必填键） | 那个文件（`FileError`，`agentkit/spec/fileerr.go:20`） |
| 某个 agent 的模型没在 `llm/` 里声明 | **那个 agent 的文件**，而且是 **hint** 不是 error —— 整棵树照常装得上，见 §4.6（`agentkit/spec/load.go:692-694`） |
| 某个 agent 的 `reasoning:` 档位没被声明 | 那个 agent 的文件（`agentkit/spec/load.go:775-796` 的 `checkReasoning`） |
| 某个 agent 点名了一个不存在的 MCP server | **那个 agent 的文件**（`agentkit/spec/load.go:891-919` 的 `validateMCP`） |
| 知识库的配置不对 | `tools/kb.yaml`（`agentkit/spec/load.go:1037-1039` 的 `kbErr`） |
| 跨文件的规则（委派图成环、嵌套深度超限） | 空 —— 消息里点名了涉及的文件 |
| 一条提示（`deferred`） | 它属于的那个文件（§4.3 的 `Notice.Path`） |

★ **归属要能在界面上兑现。** 前端按 `Issue.Path` 把问题分成三处：本文件、整棵树、别的文件 —— 别的文件的
问题**以那个文件的名字显示**，而且是**一个链接**。

⚠ **但解析不了就是解析不了。** 别的文件**解析失败**时整棵树装不起来，这次保存也会被服务端拒绝（写入门要
试装一次）。所以 Save 仍然是禁用的 —— 只是理由现在写在**它该在的地方**。★ 分界线是「装不装得上」：
**解析失败**是装不上；**模型没声明**是装得上、只是少了那个 agent。

### 4.6 「留在树外」：一个 agent 的模型没声明

★ **一个 agent 的模型没声明，不是整棵树的事。** 它只说明**那个 agent** 跑不起来：把它从树里摘掉，
剩下的配置照常装配、进程照常启动。理由不是宽容 —— 修它的入口（配置编辑器）就住在这个进程里，
进程起不来的话，连「哪儿错了」都没人告诉你。

落地是四件事，一件都不能少（`agentkit/spec/load.go:672-678` 的 `leaveOutUndeclaredModels`）：

| # | 做什么 | 在哪 |
|---|---|---|
| 1 | 从 `Config.Agents` 里删掉 | `agentkit/spec/load.go:700` |
| 2 | 从**每个**父 agent 的 `subagents:` 里删掉 —— 委派工具不能提供一个跑不起来的工 | `agentkit/spec/load.go:705-710` |
| 3 | 记下**为什么**（`Config.leftOut`，`agentkit/spec/config.go:165`） | `agentkit/spec/config.go:173-178` 的 `LeftOut()` |
| 4 | 出声：一条 `deferred`，路径是**它自己的文件** | `agentkit/spec/load.go:692-694` |

★ **不删父 agent 的 `subagents:` 就是留了个坑。** 委派工具按 `subagents:` 列表提供子 agent，列表里
留着一个装不上的名字，模型会去点它，然后拿到一个运行期错误 —— 那是一个本来可以在加载期就说清楚的
问题。

★ **`main` 也不例外。** 默认 agent 被留在树外时，`DefaultAgent()` 仍然返回 `main`
（`agentkit/spec/config.go:228-243`），**不顶替成别的 agent**：顶替会让会话悄悄跑在用户没点名的
agent 上，而且完全不提原来那个为什么不见了。代价是没有会话能跑 —— 但进程还在，编辑器还在，
这就够了。

**出声的四处**：

| 谁说 | 说什么 | 在哪 |
|---|---|---|
| 启动日志 | `[main] config notice` + 那条 `deferred` | `main.go:415-417` |
| `/v2/health`、`/v2/config` | 同一条 `deferred` | `api/v2_introspect.go:482`、`api/v2_introspect.go:406` |
| 配置编辑器 | 一条 **hint**，落在那个 agent 自己的文件上 | `agentkit/spec/diag.go:71-123` 的 `Diagnostics` |
| 会话真的要用它时 | `agent "x" is left out of the tree: the model ... is not declared under llm/` | `agentkit/build/builder.go:225-234` 的 `leftOutMessage` |

★ **一句话只写一处。** `leftOutMessage` 同时供装配层（`agentkit/build/builder.go:212`）与默认
agent 的检查使用 —— 两份拷贝会漂移，而漂移的那份是没人读的那份。

★ **编辑器里是 hint，不是 error。** hint 说的是「这棵树少了一样东西」，文件本身没坏，保存照常可以
按；error 说的是「现在按不下去」。把降级说成 error，用户会以为自己的文件坏了。

#### 写入门：一次改动不许把能跑的 agent 带出树外

★ 降级一旦成立，写入门就**必须**补上牙：既然「模型没声明」不再报错，那么把 `main` 的 `model:` 改成
一个不存在的模型，就会**静默**让 `main` 消失 —— 这正是用户看不见的那种退化。规则只有一条：**可以往树里
加，可以放着已经坏的不动，不可以把一个本来能跑的 agent 拿出去。** 比较的是**两边都 settle 过**的树
（`agentkit/spec/write.go:417-441` 的 `agentsLostBy`）—— 一个本来就在树外的 agent 两边都不在，改动不碰
它就不该算在改动头上。★ **磁盘上的树装不上时只许增不许减**（`agentkit/spec/write.go:455-472`）：此时没有
任何东西「本来是能跑的」，而编辑器的草稿正是来替换那个坏文件的那个人 —— 拦下它，坏文件就永远修不好。
★ **有意删掉的 agent 不算「掉出树」**（`agentsLostBy` 的第三个参数 `removed`）—— 用户**主动**删一个
agent 是另一件事；不然的话任何 agent 文件都删不掉。

#### 删除门：谁还指着它

★ 删除一个**别人还指着**的文件是一次降级，与写入门是同一条纪律。★ **闸门跑在试装之前**
（`agentkit/spec/write.go:785-811` 的 `checkNotReferenced`）：试装的装载错误正是要取代的症状 —— 它点名
被删掉的那个文件，把真正的原因藏在别处；用户要动手改的是**引用它的那一处**。⇒ 拒绝的文案点名
**谁引用了它**与**移除什么**。★ 被引用的分区只有三个，各有各的引用者（`agentkit/spec/write.go:718-769`
的 `referencesTo`）：`mcps/<server>.yaml` ← 一个 agent 的 `mcps:` 清单；`llm/<provider>.yaml` ← 一个
agent 的 `model:`；`agents/<id>.md` ← 另一个 agent 的 `subagents:` 清单。★ **默认 agent 单独一条拒绝**
（`agentkit/spec/write.go:796-802`）：`the default agent cannot be deleted: a tree needs one` —— 树总得
有一个默认 agent，而试装的装载错误说的是「agents/main.md does not exist」，那读起来像一个坏文件，
而不是一次被拒绝的删除。

## 5. 文件规格

### 5.1 `llm/<provider>.yaml`

```yaml
# .openheron/llm/openai.yaml
type: openai_compat                # free-form label, for display only
base_url: https://api.openai.com/v1
api_key: ${OPENAI_API_KEY}         # ${ENV} or @file:/path
timeout: 5m
extra:                             # provider-level defaults, they go into ExtraFields
  reasoning_effort: low

models:                            # ★ several models under one provider
  - id: gpt-4o
    context_window: 128000
    timeout: 10m                   # overrides the provider level
    extra: { temperature: 0.2 }    # overrides the provider-level extra
  - id: o3
    context_window: 200000
    reasoning:                     # tier vocabulary (see below)
      off:  {}                     # send no thinking field at all
      low:  { reasoning_effort: low }
      high: { reasoning_effort: high }
    reasoning_default: low         # used when the agent writes no reasoning
    reasoning_field: reasoning             # the field this endpoint carries the chain of thought in
    reasoning_field_from: reasoning_content # the source field name when replaying history
```

**Provider**（连接属性，同一 provider 下所有模型共享，形状在 `agentkit/spec/decl/decl.go:19-36`）：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `type` | `string` | ✅ | **只用于显示** —— 代码里没有按它分支的地方（`agentkit/spec/load.go:31-33`） |
| `base_url` | `string` | ✅ | 主机（可带前缀）。**不要写 `/chat/completions`**（`agentkit/spec/load.go:34-36`） |
| `api_key` | `string` | | `${ENV}` / `@file:`；空 = 无鉴权。★ `@file:` 在 YAML 里**必须加引号** —— `@` 是保留字符 |
| `timeout` | `Duration` | | 缺省 `5m`（`agentkit/build/provider.go:42-45`）。**不能依赖 SDK 隐式默认** |
| `header` | `map[string]string` | | ★ **解析了，然后没人读** —— 见 §11 |
| `extra` | `map[string]any` | | 厂商私有请求体，**顶层键覆盖** |
| `hint_keys` | `[]string` | | 该端点使用的**字段名**，供 400 时给准确提示 |
| `models` | `[]Model` | | 它声明的模型；**缺省 = 该 provider 不可用**（`agentkit/build/provider.go:78-84`） |

**Model**（模型的固有属性，形状在 `agentkit/spec/decl/decl.go:38-67`）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | `string` | 必填（`agentkit/spec/load.go:60-62`） |
| `kind` | `string` | `chat`（缺省）或 `embedding`。embedding 模型不能当聊天模型建（`agentkit/build/provider.go:37-40`） |
| `dimensions` | `int` | embedding 的维度；`0` = 未知，建索引时学出来 |
| `context_window` | `int` | **0 = 未知**（不是「无限制」）。它是压缩阈值的分母（`agentkit/spec/summarize.go:25-39`） |
| `summarize_at` | `Threshold` | ★ **已废弃**：仍然解析，好让旧配置继续加载，但没有任何代码读它（`agentkit/spec/load.go:63-68` 会说出来） |
| `timeout` | `Duration` | 覆盖 provider 级。**要允许** —— provider 级只能容纳最慢的那个 |
| `extra` | `map[string]any` | 覆盖 provider 级同名键 |
| `reasoning` | `map[string]map[string]any` | **档位词汇表**：档位名 → 该端点把它发成什么字段。为空 = 该模型不思考 |
| `reasoning_default` | `string` | 建议档位；agent 没写 `reasoning` 时用它 |
| `reasoning_field` / `reasoning_field_from` | `string` | 端点收发思维链的字段名，以及回放历史时的**源**字段名（默认取 `reasoning_field`，`agentkit/build/provider.go:163-172`） |

一个 `llm/<provider>.yaml` 持有该厂商的 `base_url` / `api_key` / `timeout` 与 `models[]` 列表。引用
形式是 **`<provider-id>/<model-id>`**（`model: openai/gpt-4o`）：`ParseRef` 取**第一个** `/`，两侧都
不得为空 —— 没有 `/` 的引用直接报错（`agentkit/spec/load.go:633-637`）。provider id 就是**去掉扩展名的
文件名**（`agentkit/spec/load.go:20`）。

★ **思考档位是三层分离的**：**能力**（`models[].reasoning` 存不存在）、**词汇表**（它的键值对：这个端点
把每个档位发成什么字段）、**默认**（`reasoning_default`），而**意图**（这次调用用哪一档）住在
`agents/*.md` 的 `reasoning` —— ★ 两侧字段同名，但一个是词汇表、一个是选择。**解析顺序**：
`agent.reasoning` → 空则 `model.reasoning_default` → 再空则不注入（`effectiveTier`，
`agentkit/build/provider.go:114-120`）。★ **档位不在词汇表里是加载期错误**：`checkReasoning`
（`agentkit/spec/load.go:775-780`）拒绝它并列出该模型声明的档位 —— 不能容忍的原因是：`reasoning: max`
写在一个只声明 off/low/high 的模型上，请求会**不带任何思考参数**发出去，那是「一点思考都没有」，
而不是「一个没定义的档位」。

**合并链只有一条**：`provider.extra ← model.extra ← reasoning[level]`，顶层键覆盖
（`agentkit/build/provider.go:122-147` 的 `RequestOptionsFor`，`:149-161` 的 `shallowMerge`）。三层合完
的**完整一份**必须整份递下去 —— 请求级 extra 是**替换**不是合并。★ **agent 自己的 `temperature` /
`max_tokens` 赢过同名的 `extra` 键** —— 下发之前那两层会从合完的 map 里被删掉
（`agentkit/build/provider.go:131-136`），否则两者都会往请求体里写 `temperature`，而合并补丁会让
`extra` 那个赢。

#### 时长

`timeout` 等一律走 `time.ParseDuration`（`5m` / `30s`），**不用纳秒整数**。空串 = 不设置。★ **坑**：
`timeout: 60`（缺单位）必须**报错**而非当作 60 秒 —— 自定义 `Duration` 类型强制要求字符串形式
（`agentkit/spec/decl/decl.go:914-937`）。★ **但不是每个时长都严格**：`server/config.yaml` 与 `tools/`、
`mcps/` 下的文件用的是宽松变体（`agentkit/spec/decl/decl.go:807-818`），解析不过的值被记下来并换成
默认值，而不是让整次加载失败。★ **而宽松回退的值到处都一样：`200ms`** —— 也就是 debounce 的默认值
（`agentkit/spec/decl/decl.go:16`）。所以 `start_timeout: 60`（裸数字）既不会变成 60 秒，也不会回退到 60 秒
的默认值：它会变成 **200 毫秒**，而且 deferred 列表里那条提醒不管实际是哪个键写坏了，永远写的是
`watch.debounce`（`agentkit/spec/reload.go:122-126`）。⇒ **把单位写上。**

### 5.2 `agents/<id>.md`

```markdown
---
# .openheron/agents/main.md
description: Management role              # goes into the task tool description, it decides whether the model picks it
model: openai/gpt-4o              # a missing model is an error — no inheritance, no fallback
max_iterations: 20                # 0 = not declared; the framework's own default then applies
subagents: [researcher]           # references other file names; the order is the order of the task description
tools: [ls, read_file, glob, grep] # the allow-list: the tools this agent may use; absent = none
skills: [refactor]                # the allow-list of skills; absent = none, [] = none
mcps: [github]                    # the MCP servers this agent mounts; absent = none, [] = none

temperature: 0.2                  # this call's intent (the same model should use different parameters in different agents)
max_tokens: 8192
reasoning: high                   # a tier name, taken from the keys of models[].reasoning
summarize_at: 80%                 # this agent's compaction threshold, a percentage of the model's context_window
---

The body is the Instruction. Leave it empty to use the framework's built-in prompt.
```

★ **三份清单都是允许清单，而且三份的缺省都是「没有」。** `tools:` 说这个 agent 可以用哪些工具，
`skills:` 说它可以加载哪些技能，`mcps:` 说它挂载哪些 server。★ 「键缺失」与「空清单」现在是**同一
件事**，因为它们意思相同 —— 而它们的意思是**什么都没有**（`agentkit/spec/decl/decl.go:113`、`:134`、
`:150`）。⇒ **列出来的才有**（§6.1）。

字段全集（形状在 `agentkit/spec/decl/decl.go:79-163`）：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `model` | `string` | ✅ | `<provider>/<model>`。**不继承、不回退**（`agentkit/spec/load.go:98-103`） |
| `description` | `string` | | 进 `task` 工具描述。描述写不好，模型就选不到它 |
| `max_iterations` | `int` | | `0` = 没声明，走框架自己的缺省 |
| `subagents` | `[]string` | | 引用其他 agent 文件名；引用不存在或成环 ⇒ 启动期报错（`agentkit/spec/load.go:1140-1147`） |
| `tools` | `[]string` | | **这个 agent 可以用的非 MCP 工具允许清单**。名字是**暴露名**；尾部 `*` 是前缀模式（§6.2） |
| `skills` | `[]string` | | 技能允许清单，名字是 `skills/` 下的**文件夹名** |
| `mcps` | `[]string` | | 这个 agent **挂载**的 MCP server，按**server 名**（§6.3） |
| `temperature` / `max_tokens` | `*float32` / `*int` | | 指针：0 与「没配」是两件事 |
| `reasoning` | `string` | | 档位名；不在模型词汇表里 ⇒ **加载期错误**（§5.1） |
| `summarize_at` | `Threshold` | | 这个 agent 的压缩阈值：token 数、模型窗口的百分比，或 `off` |

★ **`disabled_tools` 是被拒绝的，不是被改名的**（`agentkit/spec/load.go:107-112`）。旧键仍然能解析
—— 它必须能，加载器才能抓住它 —— 但加载会**失败**，报错信息说明这两个键的意思**正好相反**：
`tools` 说这个 agent **拿到**哪些工具，`disabled_tools` 说的是它**不拿**哪些。★ **没有别名**：别名会让
旧键继续以旧意生效，或者 —— 更糟 —— 突然以新意生效。

★ **从 `main` 不可达的 agent 在这里*不是*错误。** openheron **不**做可达性检查 —— 这样的 agent 只是永远
不会被构造（只有从入口 agent 可达的那些才会被建出来），而它仍然出现在自省里。

**frontmatter 的三种输入**：`---` … `---` + 正文 ⇒ 正文成为 `Instruction`；无 frontmatter 的纯 `.md` ⇒
**错误**（加载器去解一份空的 frontmatter，报「文件是空的（或者只有注释）」）；有 `---` 开头但缺结束分隔符
⇒ **错误**（无法判断正文从哪开始）。

★ **frontmatter 解析必须自写**（框架那一份未导出）：`splitFrontmatter`
（`agentkit/spec/expand.go:56-78`）。`SKILL.md` 已用同一约定，所以 openheron 的 `agents/*.md` 与框架原生
markdown 约定**同构**。★ **行号要真，不能只是「有」**：yaml.v3 报的是**它读到的那一块**的行号，而 agent
文件解的是 frontmatter —— 两块只在 `---` 位于第 1 行时才对齐，文件以空行开头时整体偏移，且是**静默**
偏移：一个笃定地指错地方的题注，比没有题注更糟。所以 `splitFrontmatter` 多返回一个起始行，
`parseShift`（`agentkit/spec/diag.go:188-196`）把偏移带到最外层。

#### 深度上限

上限只住在**一处** —— `server/config.yaml` 的 `agent.max_depth` —— 缺省 `1`
（`agentkit/spec/defaults.go:11`、`:71`）。★ 没有第二份拷贝要对账，也没有「两者取更严」这条规则了：
`MaxDepth()` 直接读 server 自己的值（`agentkit/spec/load.go:1125-1128`）。

★ **校验在启动期**（`agentkit/spec/load.go:1069-1075` 的 `validateDepth`），且是**报错**而非运行时截断
—— 「配了三层但实际只跑两层」是最难查的一类问题。装配层用同一判据，在到顶时**根本不构造**下一层。

### 5.3 `tools/<tool>.yaml`

外部工具、知识库与**配置工具**的配置。**一个工具族一个文件**（路径相对**配置根**）：`http.yaml`、
`web_search.yaml`、`wikipedia.yaml`、`filesystem.yaml`、`kb.yaml`、`config.yaml`。

★ **文件的内容就是那个工具自己的配置**（没有外层包装键）—— `http.yaml` 里直接写 `methods:`，不是
`http: { methods: … }`。没有 `enabled` 字段，也没有「写个 null 关掉」这种形态。

★ **文件不决定哪些工具存在 —— 决定的是 provider。** 工具集来自代码：`defaultToolRegistry` 里注册的
那些 provider，以及文件系统中间件（它**无条件**挂载）；★ 文件负责的是**参数**和**档位**。★ **文件名是
白名单**（`agentkit/spec/load.go:203-224` 的 `toolHints` 与 `ToolFileNames`）：未知文件名在**加载期**就是
硬错误（`agentkit/spec/load.go:307-310`）。★ 这份清单**只有一个**家：加载器、写入门与两条错误消息都读
它，所以编辑器存不下一个加载器会拒掉的文件。★ **空文件会被拒绝**，并给出一条提示、告诉用户该写什么
（`agentkit/spec/load.go:236-240`）—— 文件的内容**就是**这个工具的配置，所以「这里没有配置」才是用户
用得上的那句话。

★ **它进热更新**：改一个工具的超时、换一个搜索后端、给知识库换个语料目录，写完**下一轮就生效**。这正是
它从 `server/config.yaml` 搬出来的理由：`server/` 配的是进程自身，不进热更新 —— 而这些改动跟「进程自身」
没关系。★ **同名文件整体替换**：同一层里一个文件名只有一份。

`tools/` 与 `mcps/` 里的每个文件都带同样这两个键，而且它们在任何地方都是同一个意思：

| 键 | 类型 | 缺省 | 含义 |
|---|---|---|---|
| `tools` | `[]string` | 全部 | ★ **过滤器，不是开关** —— 要提供的那些**原始**名字（这个文件自己的词汇，**不是**暴露出来的那些名字），**从 provider 给出的那些工具里挑**。不写 = 全部 |
| `allows` | `[]string` | 空 | **不用点头**的那些名字 —— **用这个文件自己的词汇**。不写或为空 = 这个文件提供的每个工具都要点头 |

★ **缺省是 `ask`，而 `allows:` 是例外。** 除非有别的东西说话，否则每个工具都要先点头。没有分组缺省，
也没有特例：同一条规则盖住文件系统工具、HTTP 工具与一个 MCP server。

★ **一个文件说自己的词汇。** `http.yaml` 说 `request_get`，因为那个文件讲的就是这件事；一个 MCP server 说
`create_issue` —— 不是 `mcp__github__create_issue`。⇒ **每一层一个机制，每一层一套词汇。** ★ **这两个键
回答两个不同的问题，谁也吞不掉谁**：`tools:` 回答**提供什么**，`allows:` 回答**其中哪些不用点头**。

★ **一份 `allows:` 清单能覆盖 server 还没提供的工具**，因为它是关于**服务器**的一句话，而不是关于它眼下
恰好有的那些名字。server 的工具清单只有运行期才知道。

字段全集：

| 文件 | 字段 | 缺省 | 说明 |
|---|---|---|---|
| `http.yaml` | `methods` / `headers` / `timeout` | `[get]` / `{}` / `30s` | ★ **只给白名单里的方法**（post/put/delete 能改外部世界，要显式点名）；每个请求都带的头；单个请求的上限（宽松时长） |
| `http.yaml` | `allows` | `[]` | ★ **不用点头**的那些名字 —— 用**这个文件自己的词汇** |
| `web_search.yaml` | `provider` | `duckduckgo` | 搜索后端；**未知值不静默降级** —— 整组不挂 + 日志出声（`agentkit/build/nettools.go:196-200`） |
| `web_search.yaml` | `max_results` / `timeout` / `region` | `10` / `60s` / `""`→全球 | 一次搜索最多返回几条；单次搜索的上限（搜索要翻页，比 HTTP 宽裕）；duckduckgo 的 `kl` 参数 |
| `web_search.yaml` | `allows` | `[]` | 同上 |
| `wikipedia.yaml` | `top_k` / `language` / `timeout` / `base_url` | `3` / `zh` / `15s` / `""`→官方 | 返回几条 / 维基的语言 / 单次检索的上限 / API 端点（自建镜像用） |
| `wikipedia.yaml` | `allows` | `[]` | 同上 |
| `filesystem.yaml` | `allows` | `[]` | ★ 同上，而且是**加法**（见下） |
| `config.yaml` | `allows` | `[]` | ★ 同上；★ 那一族的名字用**本文件自己的词汇**（`config_get`）；★ `config_write` 也由它放行（见 §6.2） |
| `config.yaml` | `max_read_bytes` | `65536` | `config_get` 最多 handing back 多大的文件；★ **拒绝**而不是截断（见 §7.1）；范围 1024..1048576，越界是加载期硬错误 |
| `kb.yaml` | `corpus_dir` / `index` | `""`→关 / `default` | 语料目录，**相对项目根**；绝对路径被拒（`agentkit/spec/load.go:1045-1048`）；索引目录名，落在 `state/kb/<index>/` |
| `kb.yaml` | `model` | `""` | 向量化模型，`<provider>/<model>`；必须 `kind: embedding`；`corpus_dir` 非空时**必填**（`agentkit/spec/load.go:1049-1052`） |
| `kb.yaml` | `query_instruction` | `""` | 只加在查询侧的指令前缀 |
| `kb.yaml` | `batch_size` / `max_batch_chars` / `top_k` | `10` / `1200` / `8` | 一次向量化请求的条数上限 / 一批的字符数上限 / `kb_search` 默认召回条数 |
| `kb.yaml` | `allows` | `[]` | `kb_search` —— `kb_index` 要写索引，所以要点头 |
| `kb.yaml` | `splitter.headers` | `{}` | ★ 仍然解析，但**不再生效**（`agentkit/spec/load.go:391-395`）—— 见 §11 |

★ **这些字段的缺省值住在读它们的 provider 里，不在 `decl` 里。** 声明结构体只带形状，值在 provider
里（三个超时在 `agentkit/build/nettools.go:21-26`，搜索的缺省在 `agentkit/build/nettools.go:187-195`，
维基的缺省在 `agentkit/build/nettools.go:237-243`），知识库的缺省在 `agentkit/spec/defaults.go:48-55`，
而 `config_get` 的读取上限在 `agentkit/build/configtools.go:466`（`defaultConfigReadBytes`）。

#### ★ 文件系统工具：一个调音旋钮，加上代码里的缺省

文件系统工具来自框架的中间件，而不是来自某个 provider，所以是中间件把它们挂上、没有任何文件能把它们
打开 —— 于是它们的文件是一个**调音旋钮**（`tools/filesystem.yaml`），而文件不在时生效的是代码那份缺省
清单（`agentkit/build/tools.go:95-101`）。这一套就是框架的文件系统中间件：`ls`、`read_file`、
`write_file`、`edit_file`、`glob`、`grep`。

★ **`execute` 不在中间件那一列里 —— 它是 openheron 自己的工具**（`agentkit/build/execute.go`）。中间件
只在配置里带着 shell 时才挂一个 `execute`，而框架给它的描述只字不提这台机器上跑命令的是哪个 shell ——
写命令的是模型，模型得知道方言。所以中间件那个不挂（`agentkit/build/assemble.go:30-40`），换成自己的。
★ 名字仍然保留，provider 不得冒用（`agentkit/build/tools.go:117-150`）。★ **读取器直接放行，写入器要点头**
—— `write_file`、`edit_file` 与 `execute` 不在代码那份清单里，所以它们会为一个人打断这一轮：它们正是那
三个会改动这台机器的工具。

★ **这个文件里的 `allows:` 是加法的**，写在这里的名字是加进代码那份缺省清单 —— 它从来拿不走一个名字
（`agentkit/spec/decl/decl.go:439-452` 的 `FilesystemCfg`）：

```yaml
# .openheron/tools/filesystem.yaml
allows: [write_file, edit_file, execute]   # ★ 三个写入工具，不用点头
```

★ **于是这个文件放宽档位，而 agent 的 `tools:` 收窄它能用的非 MCP 工具。** 这就是那条分工，而且是刻意
的：档位是关于**工具**的一个决定（它该待在工具旁边），而一个 agent 能不能用它，是关于**某一个 agent**
的决定（它该待在那个 agent 的文件里）。两个机制、两套词汇、不重叠。

#### ★ 配置工具：一个自指的例外清单

`tools/config.yaml` 讲的是**配置工具自己**，所以它有一层特别：**它点的名字就是它自己所在的那一层**
——`config_get`、`config_validate`、`config_template`、`config_notices`、`config_tier`、`config_write`，
用的是**本文件自己的词汇**（不是 `mcp__…__config_get`）。

★ **六个工具始终被挂上**（provider 给的），所以这个文件**不是开关**：它点名例外、也调上限，工具在不在
不归它管。★ **它是第一个「自己的文件决定自己档位」的工具族** —— 而这正是它成为自指的那一层的原因：
这一族工具的放行档**不在代码里了**，代码那份清单只覆盖**没有自己文件**的那些（§6.2）。

★ **`config_write` 和其它五个一样，由这个文件点名放行。** 它改的就是写了这个 `allows:` 的那个文件 ——
而这正是「例外住在自己那一族的文件里」这句话的代价：★ 一个能改配置的工具，也能改那份放行清单本身。
⇒ 这是模型本身的性质，不是这一个工具的怪癖 —— 任何能写配置的工具都能放宽配置带的例外。
★ 出厂模板**不点名**它，理由与知识库不点名 `kb_index` 一样：问配置不该要点头，改配置应该要。

★ **文件不在时，那六个工具都要点头**，而代码里已经不再点名它们 —— 所以加载器发一条 notice，
点名这个文件与那五个只读的名字。★ notice **只在有 agent 真的点了这些工具时**才发：没有 agent 用它们，
这个差别对谁都不存在。

★ **`max_read_bytes` 是 `config_get` 的答案大小上限**，而它**拒绝**而不是截断（§7.1）。

#### ★ 「连到外面去」的三类工具

`tools/` 下的三类工具与知识库有一个本质差别：**它们的输出会原样进模型上下文**。知识库召回的是自己写的
语料，而这里是别人写的网页 —— 于是「外部内容里的指令」也进了模型脑子。档位层管的是「能不能调这个
工具」，看不见 URL、也看不见结果里的字。所以这一族工具的规矩是（实现见 `agentkit/build/nettools.go`）：

| 规矩 | 为什么 |
|---|---|
| **默认全关** | 没配就一个都不挂 —— 挂一个永远搜不到东西的 `web_search` 比不挂更糟 |
| **只连公网** | 对 HTTP 工具来说 URL 是模型给的。能访问 `127.0.0.1` 的 HTTP 工具 = 模型能自己读本机配置、会话状态、云上元数据服务 |
| **输出有长度上限**（20000 字节） | 一个 10MB 的页面会把这一轮直接撑爆 —— 表现是「模型突然开始胡说」，不是报错 |
| **写方法要显式点名** | POST/PUT/DELETE 能改外部世界的状态，而模型不需要理由就能编出一个 POST |

「只连公网」是在**拨号那一刻**解析并判的，且连到**刚判过的那个 IP**（`agentkit/build/nettools.go:37-65`）
—— DNS 可以在两次解析之间换答案（rebinding），只在解析时判等于给了绕过。★ **维基那个工具是唯一不在
拨号器后面的**：它的端点来自 `wikipedia.yaml` 的 `base_url`，而框架的维基工具自己建 HTTP 客户端 ——
没有可以注入的钩子。输出上限照样罩着它。

### 5.4 `skills/<name>/SKILL.md`

```markdown
---
name: refactor
description: ...     # ★ this is what decides whether the model picks it
context: inline       # inline | fork | fork_with_context
agent: main           # fork mode takes it from the AgentHub by name
model: fast           # takes it from the ModelHub by name
---
```

frontmatter 的 schema 是框架的，不是 openheron 的：`name` 是 skill 自己的名字 —— **模型**认的身份；
`description` 渲染进 skill 工具的描述里 —— ★ 它决定模型会不会挑它；`context` 说这个 skill 怎么跑；
`agent` 与 `model` 是两处覆盖。

★ **`inline` 不是一个声明出来的常量。** 框架只声明了 `fork` 与 `fork_with_context`，其余任何值 ——
包括空值，也包括写错的值 —— 都落进 inline 分支。所以 `context: inline` 能用，但拼错不会大声报错：
它悄悄地就是 inline。

★ **两行 frontmatter 现在都生效**：`agent:` 走 `AgentHub`（`agentkit/build/hub.go:119`），`model:` 走
`ModelHub`（`agentkit/build/hub.go:71`）。★ **模型必须带请求级参数** —— 框架的 `ModelHub` 返回值会
**整个替换** agent 的模型，返回裸实例会让「skill 换模型」顺带把推理档位与采样参数弄丢，而且不报错。
★ **两个 hub 都要挂，不能只挂一个**：缺 `ModelHub` 时 inline 模式下的 `model:` 被**静默忽略**，缺
`AgentHub` 时 fork 模式直接失败 —— 前者正是「配了没用」的典型。★ **skill 的模型来自配置，而不是来自
调用它的 agent**：hub 解析引用时传的是空的 `CallIntent`，所以档位回落到模型自己的 `reasoning_default`。
skill 写的是*模型*，不是 agent；借用调用方 agent 的逐 agent temperature，会悄悄改掉 skill 自己那行
`model:` 的意思。

**目录怎么定位**：

```
EINO_EXT_SKILLS_DIR environment variable    ← the override, the highest one
   ↓ unset
look for .openheron/skills/ upward from cwd  ← agentkit/build/skill.go:217 probeProjectSkills
   ↓ not found
skills are not enabled
```

★ **显式配了却不存在会打日志**（`agentkit/build/skill.go:206-213`）—— 静默返回「未启用」会让「我配了
skills 怎么没生效」变成一个查不出原因的问题。★ **这次向上找不是配置根那套规则**：配置根从不向上找
（§3），而 skills 目录会 —— skills 是一棵目录树，向上找能复用家目录里的 skills；配置根是一组实体，向上找
会让同一条命令读到另一份配置。★ 这条规则只住在 `build` 里，在 `spec` 里再写一份就是第二个答案。

★ **`skills/` 是唯一嵌套的分区。** 理由不是好看 —— skill 的名字**就是**文件夹名，agent 在 `skills:`
里写的就是它；一个 skill 就是「一个文件夹 + 一个固定文件名」，所以它的路径天生比别的分区深一层。别的
分区写深一层会被**拒掉**：加载器只读一层，`agents/sub/main.md` 这样的文件会被保存、然后永远不会被
加载（`agentkit/spec/write.go:102-125`）。里面的文件名固定 `SKILL.md`
（`agentkit/spec/config.go:34`），文件夹名要能被读回来：`^[a-z0-9]+([_-][a-z0-9]+)*$`
（`agentkit/spec/decl/decl.go:649`）。★ **`skills/` 也是唯一没有 loader 的分区**：skill 文件不是配置层
读的 —— 运行时自己的 backend 读它（`agentkit/build/skill.go:23`）；配置层要这一项只是为了**表面**（清单、
路径规则、编辑器），所以加载循环跳过 `loader == nil` 的分区（`agentkit/spec/config.go:576-582`），而不是
给它一个空 loader。

★ **写入门在这里多一道别处没有的拒绝。** 运行时从哪个目录读 skill 由 `ResolveSkillsDir()`
（`agentkit/build/skill.go:190-215`）回答，它可以**不是**配置根下的 `skills/`。当它不是的时候，编辑器
写进来的文件根本不会被加载 —— 于是保存被拒，清单里这一分区标成只读并**点名真正的目录**
（`agentkit/spec/write.go:151-164` 的 `validateWritable`、`agentkit/spec/config.go:386-395` 的
`skillsElsewhere`）。★ 反过来说：进程启动时**一个 skills 目录都没找到**，不算这种情况 —— 那时配置根
下的 `skills/` 正是探针下次会找到的地方，保存照常，否则「从零建起第一个 skill」就没有入口。

★ **openheron 在 agent 这一侧还多了一个字段**：`agents/<id>.md` 里的 `skills` 键是 skills 目录下各
文件夹的白名单。★ 它只在 agent 压根儿有 `skill` 工具时才有意义。★ 这个范围过滤的是两半
（`agentkit/build/skill.go:89-186` 的 `scopedSkillBackend`）：`List`，也就是模型**看得见**的；`Get`，
也就是它真能**加载**的。只过滤一半，会留下一个模型记得名字就能加载的 skill。

### 5.5 `server/config.yaml`

进程自身的配置。**它不进热更新** —— 改了要重启（§8）。server 分区带着 `Editable: false, hot: false`
（`agentkit/spec/config.go:71-74`），配置编辑器干脆不写它。

| 字段 | 类型 | 缺省 | 说明 |
|---|---|---|---|
| `listen_addr` | `string` | `127.0.0.1:8080` | Hertz 监听地址。★ 必须加引号 —— 以 `:` 开头的值解析得有歧义。★ 缺省是**环回**：光一个 `:8080` 绑的是**所有网卡**，而这台服务器的工具跑的是 shell 命令 |
| `frontend_mode` | `string` | `embed` | `embed` 服务编译进二进制的界面；`dir` 服务磁盘上的一个目录。值不认识时只记一条告警，前端不启用（`main.go:626`） |
| `static_dir` | `string` | `web/dist` | 相对**项目根**解析（`Service.ResolveDir`）；只在 `frontend_mode: dir` 时才被读 |
| `data_dir` | `string` | `state` | 相对**配置根**解析；空串等同 `state`；目录不存在时创建 |
| `log_level` | `string` | `info` | `debug` `info` `warn` `error`；非法值回退 `info` 并记 `deferred`（`agentkit/spec/load.go:149-151`） |
| `log_requests` | `bool` | `false` | 是否在 `debug` 下打印脱敏后的模型请求体；等价于 `OPENHERON_LOG_LLM_REQUESTS=1`，环境变量优先 |
| `log.file` | `string` | `""` | 日志文件；相对**配置根**解析；空串 = 只写 stderr；由 `OPENHERON_LOG_FILE` 覆盖。★ **代码缺省是空串，出厂模板给的是 `logs/openheron.log`** —— 新项目开箱就落盘 |
| `log.console` | `bool` | `true` | 写文件的同时保留 stderr。★ **代码缺省是 `true`，出厂模板给的是 `false`** —— 文件就是全部的日志 |
| `log.max_size_mb` / `max_age_days` / `max_backups` / `compress` | `int` / `int` / `int` / `bool` | `20` / `14` / `5` / `false` | 单个文件的 MB 上限（`0` = 不限，负数归一为 `0`）；轮转文件的保留天数；最多保留几个轮转文件；轮转文件是否 gzip |
| `start_timeout` | `LenientDuration` | `60s` | 一轮从提交到进入运行状态的上限 |
| `lease_ttl` | `LenientDuration` | `10m` | 一个 run 的租约在无进展的情况下能存活多久；它必须**长于 provider 自己的超时** |
| `retry.max_retries` | `int` | `5` | 传 0 也用默认 ——「不配」与「配成 0」是两件事（`agentkit/spec/load.go:1133-1136`） |
| `agent.max_depth` | `int` | `1` | 见 §5.2 |
| `watch.enabled` | `*bool` | `nil`→开 | 用指针区分「没写」与「写了 false」（`agentkit/spec/reload.go:128-137`） |
| `watch.debounce` | `LenientDuration` | `200ms` | 轮询间隔取其一半，下限 20ms（`agentkit/spec/reload.go:205-212`） |

★ **`listen_addr` 就是全部的边界。** 没有账号，也没有令牌 —— 这是一台机器上一个人用的工具。
环回监听时，服务器自己会挡住别处来的请求（`api/guard.go` 的 Host 与 Origin 两条规则）；
一旦把它写到 `0.0.0.0`，那两条规则就不管用了，所以启动时会留下一条告警
（`main.go:447`）：**能碰到这个端口的人就能驱动这个 agent。**

★ **与 `tools/` 和 `mcps/` 不同，`server/` 没有文件名白名单。** 它下面的每一个 `.yaml` 都被解进同一个
`decl.Server` 结构（`agentkit/spec/load.go:140-144`），所以一个手滑的 `server/typo.yaml` 会被并进
配置，而不是被拒掉。`server/config.yaml` 是约定，不是加载器强制的规则。

★ **深度与重试上限只住在这里。** 没有第二份拷贝要对账，也没有「两者取更严」这条规则了 —— 上限是一个
**进程级**设置，它换来的只有一件事：不重启就能收紧一个上限，而这不值得多一个要去查的地方。★ 代价是
这个上限**不进热更新** —— 改它下一次启动才生效。

## 6. 工具表面：三张表一条规则

### 6.1 规则

> **列出来的才有。**

| 键 | 不写 | `[]` | 管什么 | 类型 |
|---|---|---|---|---|
| `tools` | **没有** | 没有 | **非 MCP 工具**：这个 agent 能用哪些（内置工具、`tools/` 下各文件提供的、`config_*`） | `[]string` |
| `mcps` | **不挂载** | 不挂载 | **挂载**：挂哪些服务器；挂载一个就把该服务器的全部工具交给这个 agent | `[]string` |
| `skills` | **没有** | 没有 | skill 可见性 | `[]string` |
| `allows`（工具文件） | 代码的缺省清单 | 同左 | **审批**：哪些不用点头 | `[]string` |

★ **为什么工具缺省关门、而审批缺省开门。** 两件事的失败形态不同：忘写 `tools` ⇒ agent 什么都干不了，
**当轮立刻看得见**；忘写 `allows` ⇒ 工具仍然可用，只是每次要点头，**在审批卡上看得见**。默认值应该
选「错了也看得见」的那一侧 —— 而这两侧的「看得见」方向相反。

★ **三态退化成两态，代码是净删除。** `tools` / `skills` / `mcps` 都是普通切片，不是指针 ——「没声明」与
「声明了但是空的」现在是同一件事，而它们共享的缺省就是**没有**。

### 6.2 档位：没有分区，也没有全局清单

档位只有两个（`agentkit/build/policy.go:16-19`）：

| 档位 | 含义 |
|---|---|
| `allow` | 工具直接跑，不需要人点头 |
| `ask` | 工具打断这一轮，等一个人的决定 |

`Policy.TierOf`（`agentkit/build/policy.go:55-60`）按名字查档位：在 allow 表里就是 `allow`，其余一律
`ask`。★ **为什么缺省是 `ask`。** 猜错的代价不对称：多一次点击，对上的是没人看见的一次破坏性调用。没人点名的
工具，就是 openheron 不敢替它担保的工具 —— 一个 MCP server 的工具是**运行期才发现**的，而 stdio 之下
server 是带着 agent 自己权限的子进程。

例外由**提供这个工具的文件**点名：

| 来源 | 它点名什么 | 用的词汇 | 装配位置 |
|---|---|---|---|
| 没有自己文件的内置工具 | `ls` `read_file` `glob` `grep` `task` `write_todos` `skill` | 暴露名 | `agentkit/build/tools.go:95-101` `builtinAllowedTools` |
| 工具自己的文件 —— `tools/*.yaml` 与知识库 | 那个文件的 `allows:` | **文件自己的** | `agentkit/spec/load.go:430-433` `AllowedTools` |
| MCP server 自己的文件 | 那个 server 的 `allows:` | **server 自己的** | `agentkit/build/mcptools.go:173-184` `mcpAllowedNames` |

★ **代码那份清单之所以存在**，是因为 `task` —— 委派工具 —— **没有自己的文件**，而一个不点一下就不能
委派的 agent 等于不能委派。它也带着文件系统的四个读取器：那个文件是**调音旋钮**，而读取器的放行写在
代码里（§5.3）。

★ **配置工具现在有自己的文件了**，所以它们**不在**这份清单里 —— 一个 provider 的工具，它的例外由
**它自己的文件**点名，这条规则对每一个 provider 都一样（§5.3 的「配置工具」那一节）。

★ **那张表是从配置装配出来的**（`agentkit/build/spec.go:73-80` 的 `policyOf`），所以一个工具的档位在
**它的 server 甚至还没起来之前**就能回答。正因如此，配置编辑器才能对一个没在跑的 server 回答「这个
工具是什么档位」。★ **三处例外合成一张表，没有第四处**：代码清单 + 每个文件的 `allows:` + MCP 服务器的
`allows:`。⇒ **没有哪个工具是例外的** —— 改配置的那个工具也一样，它的档位由 `tools/config.yaml` 点名。

#### ★ 两个问题，分开摆着

| | |
|---|---|
| `Policy.TierOf(name)`（`agentkit/build/policy.go:55`） | **allow** / **ask** —— 这个工具要不要人点头？★ **每个 agent 共享** |
| `Table.Blocked(name)`（`agentkit/build/policy.go:163`） | **这个 agent** 能不能用它？★ **按 agent**：agent 自己的 `tools:` 清单 |

★ **档位清单是共享的，agent 的清单不是。** 一个说「这个工具不用点头」，另一个说「这个 agent 能不能用
它」。一个工具可以是 `allow` 而仍然不在某个 agent 里 —— 这两个答案根本不搭话，而这正是要点：决定档位
的代码不知道当前跑的是哪个 agent，决定 agent 工具的代码不知道档位。

★ **没有 `deny` 这一档。** 一个 agent 不能用的工具**保留自己的档位**，并在它的 `reason` 里说明原因：是
**这个 agent** 拿不到它，不是这个工具不合法（`agentkit/build/introspect.go:36-47` 的 `reasonOf`）。
★ **现在有两个理由**：非 MCP 工具不在某个 agent 的表里，是因为那个 agent 的 `tools:` 清单没点名它；
某个 **MCP** 工具不在，是因为它的 server **没被挂载**。★ **闸门包住例外清单没有点名的一切**
（`agentkit/build/gate.go:43-81`）：`allow` 原样放行，`ask` 打断这一轮等人。没有第三个分支 —— 一个不在
agent 工具集里的工具根本到不了闸门。

#### ★ 前缀模式：只管非 MCP 工具

某个 agent 的 `tools` 除了**精确工具名**，还接受**尾部 `*` 的前缀模式**：`execute` 命中那个工具，
`config_*` 命中每一个以 `config_` 开头的非 MCP 工具，而 `mcp__github__*` **不再匹配任何东西** —— MCP
工具由 `mcps:` 授予，这条会被丢弃并报一条提示。

| # | 规则 | 理由 |
|---|---|---|
| 1 | 只有**结尾**的 `*` 是前缀模式；`*` 出现在别处 ⇒ **加载期硬错误** | `mcp*foo` 看着像通配符而其实不是 —— 一条读起来与实际覆盖范围不同的规则，就是一次静默的错配 |
| 2 | 单独一个 `*` ⇒ **加载期硬错误** | `tools: ["*"]` 会点名世上所有工具 —— 而这份清单是允许清单，那与模式通常的意思正好相反 |
| 3 | `*` 前面不能为空 | 规则 2 |
| 4 | 精确条目与前缀条目**等价** —— 命中就是这个 agent 拿到这个工具 | 这里没有档位可选；这份清单说的就是这个 agent 有什么 |

★ **它住在哪儿。** 加载器拒绝那些不是模式的写法（`agentkit/spec/load.go:131-138` 的
`validateAgentTools`，语法本身在 `agentkit/spec/decl/decl.go:665-668` 的 `ParseToolNamePattern`）；那张
表把前缀模式与精确名放在一起留着（`agentkit/build/policy.go:115-148` 的 `newTable`），而
`Table.Blocked` 匹配它们（`agentkit/build/policy.go:163-182`）。

★ **前缀模式之所以安全，是因为 server 名里不许出现连续的下划线**
（`agentkit/spec/decl/decl.go:610` 的 `MCPServerNamePattern`）：`mcp__github__` 够不到
`mcp__github_x__`，因为后一个 server 名压根儿声明不出来。★ 拼出来的名字从不被拆回去 —— 模式是前缀，
不是名字。★ **它不新增档位，也不重排档位** —— 模式只决定**一条条目是否匹配某个名字**。

### 6.3 挂载即授予

```yaml
# .openheron/agents/main.md
mcps: [github]                            # ① 挂载 github —— 这一行同时就是"用它的全部工具"
tools: [ls, read_file]                    # ② 非 MCP 工具的允许列表；这里不写 MCP 的名字
```

| 问题 | 谁回答 | 缺省 |
|---|---|---|
| 命名 | 代码，`mcp__<server>__<tool>`（**非协商**） | 外部工具必带命名空间 |
| 用不用这台服务器 | `mcps` | 不挂载 |
| 用不用它的哪个工具 | 服务器自己的文件（`tools:` 是过滤器） | 全部 |
| 用不用哪个非 MCP 工具 | `tools` | 没有 |

★ **挂载即授予。** `mcps: [github]` 这一行说了两件事：把子进程拉起来，并把它的全部工具交给这个
agent。再在 `tools:` 里写一遍 `mcp__github__*` 是同一件事的第二种说法 —— 而两种说法迟早会不一致，所以只留
一种。★ **代价是真实的：按 agent 收窄 MCP 工具的能力消失了。** 服务器文件自己的 `tools:` 过滤
（`agentkit/build/mcptools.go:123-133` 的 `mcpOffers`）仍然是**按服务器**的收窄点，但它是所有挂载它的
agent 共享的。

★ **挂载不等于放行。** MCP 工具的档位来自 server 文件的 `allows:`，缺省仍然是 `ask`。⇒ 一个 server
提供的其余一切都要点头，**包括它还没提供的那些工具**。★ **`tools:` 里的 `mcp__*` 条目被丢弃，并以一条
提示报出**，而不是加载期报错（`agentkit/spec/load.go:943-946` 的 `checkAgentMCPNames`）—— 理由：语义
已经变了，不是自相矛盾；硬失败只会挡住一个本来能用的配置。★ **配置工具没有例外**：
`config_write` 的档位同样由 `tools/config.yaml` 点名，和它那五个只读兄弟一样。

`mcps/<server>.yaml` 的字段全集：

| 字段 | 类型 | 缺省 | 说明 |
|---|---|---|---|
| `transport` | `string` | `stdio` | `stdio` \| `http`；★ **未知值硬报错**，不静默退回 stdio（`agentkit/spec/load.go:486-493`）。`http` 只指**可流式 HTTP** |
| `command` / `args` | `string` / `[]string` | — / `[]` | stdio 专用，**`command` 必填**：要执行的命令与它的参数。★ 命令在沙箱的 PATH 里解析，**绝不**对着项目根解析 |
| `env` | `map[string]string` | `{}` | **叠加**在继承来的环境之上的变量；★ 子进程的环境就是 openheron 自己的环境。★ 只属于 stdio：在 http server 上它是**加载期错误**，哪怕这个 map 是空的 |
| `url` / `headers` | `string` / `map[string]string` | — / `{}` | http 专用，**`url` 必填**：服务器地址与每次请求都带的头 |
| `timeout` | `MCPDuration` | `30s` | 连接 + 初始化 + 取工具清单的上限 |
| `call_timeout` | `MCPDuration` | `60s` | **一次工具调用**的上限 —— ★ 没有它，一个永不回答的服务器会占着调用锁 |
| `tools` | `[]string` | `[]`→全部 | ★ **过滤器，不是开关** —— 这个 server 要提供的那些**原始**工具名 |
| `allows` | `[]string` | `[]` | 这个 server **自己的**工具名里**不用点头**的那些 |

★ `timeout` 与 `call_timeout` 是**宽松**时长：写错退回默认并记 `deferred`。写错 timeout 改变的是
「等多久」，不是「会发生什么」—— 它不该让程序起不来。★ **类型是 `MCPDuration`，不是那个包级的宽松
时长清单**：别的键用的那份列表不带来源信息，所以一个坏值说不出是哪个文件、哪个键写错了。

★ **server 存不存在只待在一个地方。** 没有 `enabled` 字段：server 是**因为文件存在**而被**定义**的，
删掉文件就是删掉定义。★ **文件名就是命名空间。** `server` 段是**我们定**的（文件名），必须匹配
`^[a-z0-9]+(_[a-z0-9]+)*$`，不匹配 ⇒ **加载期硬错误**（`agentkit/spec/load.go:471-474`）；`tool` 段
是**服务器定**的，`[A-Za-z0-9_-]` 之外的字符替换成 `_`，大小写保持原样
（`agentkit/build/mcptools.go:151-164`）。★ **命名空间同时解决了「撞保留工具名」**：暴露 `ls` 的 server
变成 `mcp__<server>__ls`，不再与 `ls` / `execute` 那批保留名相撞。反过来也成立，且是一条规则：**只有
MCP 工具可以用 `mcp__` 前缀**。★ **合成名超过 64 个字符会被跳过并给出警告**，一个 server 里两个清洗后
同名的工具，后一个也一样（`agentkit/spec/decl/decl.go:702`、`agentkit/build/mcptools.go:68-121`）。
★ **绝不**是加载期失败：名字是远端服务器定的，而一个多出一个怪工具的 server 该付出的代价是那个工具，
不是整个 agent。

★ **本节没有凭据字段，而这正是要点。** MCP server 需要的 key 是**服务器自己的**事 —— 它知道该读哪个
变量。沙箱运行时把**哨兵**注入容器的环境，而子进程的环境就是本进程的环境，于是服务器自己就找到了它
—— 这里一个字都不用写。★ **一个 server 连不上时**：它的工具整个不挂 + 一条警告（带 server 名、传输方式
与错误），**不影响其它 server，也不影响进程启动**。静默丢掉是最糟的结果 —— 症状会是「这个功能好像
不见了」。

### 6.4 配置面收敛成一个核心、四个薄壳

```
              ┌──────────────────────────────────┐
              │  spec.Service（文件层事实）        │
              │  Manifest / Read / Diagnostics    │
              │  Write / Delete / Skeleton        │
              │  Notices / Fingerprint            │
              └───────────────┬──────────────────┘
                              │  *spec.Service + *spec.Config
              ┌───────────────▼──────────────────┐
              │  build：六个 config 工具（§7）     │
              └──┬─────────────┬─────────────┬───┘
                 │             │             │
        HTTP /v2/…          CLI init      六个 config 工具
        五个薄 handler       薄壳          agentkit/build/configtools.go
```

★ 「一个核心」是字面意义上的：`Manifest` 与 `Diagnostics` 都是 `spec.Service` 的方法，HTTP、CLI 和
工具都只调它们，没有第二个装配点。

**刀一：manifest 下沉进 `spec`。** `spec.Service.Manifest(live *Config)`
（`agentkit/spec/manifest.go:68`）返回 `Manifest`：分区、文件、模板、模型词汇、指纹。它带 `json` tag
直接上线：这份值**就是**一个视图，配置层是唯一知道答案的地方。

**刀二：`Validate`（一条字符串）→ `Diagnostics`（一组带行号的 issue）。** `spec.Issue`
（`agentkit/spec/diag.go:26`）带 `Path` / `Line` / `Severity` / `Message`；`spec.Diagnostics`（`:42`）是
整份答案。`Service.Diagnostics`（`:71`）问「这份内容能不能落地」，`Service.Check`（`:125`）问「磁盘上
这棵树能不能加载」。★ **`Line: 0` 不是第 0 行，是「没有行」。** 跨文件规则没有位置可指，它的消息里点名
文件。★ **解析失败就没有 hint**：hint 挂在 `*Config` 上，而解析失败就没有这个对象 —— 这是边界，不是
遗漏；所以 `validate()` 把 notice 的收集挪到硬规则**之前**（`agentkit/spec/load.go:720-726`），一条规则
失败不该把同一份答案里「你大概不是这个意思」的部分一起吞掉。

**刀三：档位的出口。** `config_tier` 与档位装配在同一个包里，直接调 `policyOf(cfg)`
（`agentkit/build/spec.go:73`）拿到 `Policy`，再 `TierOf`。多加一层封装只会多一个和 `policyOf` 抢位置
的东西。★ **CLI 统一**：`initcfg.BuildPlan`（`initcfg/initcfg.go:56`）取一次 manifest：`items` 是跳过表，
`templates` 是内容；★ **`llm/` 仍从 templates 包读，这是刻意的** —— 它的文件是**备选**（provider 自己的
模板与通用兜底），所以 manifest 根本不带 `llm/` 模板。

## 7. 六个 `config_*` 工具

放在 `agentkit/build/configtools.go`，与其他 provider 一样注册进 `defaultToolRegistry`。名字的常量块在
`agentkit/spec/decl/decl.go:402-420`。

| 工具 | 输入 | 输出 | 复用 | 档位 |
|---|---|---|---|---|
| `config_get` | `{path}` / `{path, impact}` / `{impact}` / `{}` | 单文件：`{path, exists, hot, revision, content, source}`；无参：分区表、文件清单、**生效**配置摘要，外加 `runningVersion` 与 `diskVersion`；★ 带 `impact`：这一份（或整棵树）的影响范围 | `Manifest` / `ReadFile` | ★ 文件点名时 allow，否则 ask |
| `config_validate` | `{path, content}` 或 `{}` | `{ok, issues[], configVersion, source}` | `Diagnostics` / `Check` | ★ 文件点名时 allow，否则 ask |
| `config_template` | `{path}` / `{section}` | `{path, content}` / `{section, templates}`；都没有时给索引 | `TemplateFor` / `Templates` / `Skeleton` | ★ 文件点名时 allow，否则 ask |
| `config_notices` | `{}` | `{deferred[], runningVersion, diskVersion, watch, source}` | `Deferred()` / `Fingerprint()` | ★ 文件点名时 allow，否则 ask |
| `config_tier` | `{tool}` / `{agent}` | `{tool, tier, source}` / `{agent, tools[], source}`；都没有时 `{defaultTier, allow[], source}`。★ 查不到 agent 时按「刚写入待下一轮 / 被 loader 排除 / 根本不存在」分别给诊断 | `policyOf` / `TierOf` / `missingAgentError` | ★ 文件点名时 allow，否则 ask |
| `config_write` | `{path, content, baseRevision?, append?}` | `{path, revision, runningVersion, diskVersion, applied, requiresRestart, note}` | `Write` / `Append`（`agentkit/spec/write.go:488` / `agentkit/spec/write.go:549`） | ★ 文件点名时 allow，否则 ask |

★ **`source` 说的是这个答案从哪一边来**：`disk`（此刻盘上的文件）或 `running`（这一轮跑的那棵树）——
两个常量在 `agentkit/build/configtools.go:87-89`。★ **六个工具里只有 `config_tier` 与 `config_notices`
的 `deferred` 那一侧答「running」** —— 其余答「disk」。

★ **`runningVersion` 与 `diskVersion` 是两个数**：盘上那份和本轮跑的那份，在热更新接手之前一直是不同的。

★ **模型没有删除动作。** `svc.Delete`（`agentkit/spec/write.go:841-892`）留在服务层，删除不可逆 —— 所以
工具面上**不**给模型一个「删掉这个文件」的动作。★ 那道删除门（§4.6）是给**编辑器**用的，不是给模型的。

★ **`config_get` 的第二个参数是 `impact`**（`agentkit/build/configtools.go:108-124`）：它回答
「**下一轮什么会变？**」—— `{path, impact: true}` 答**这一个文件**的影响（谁依赖它、谁会因为它
重建），`{impact: true}` 答**整棵树**的影响。⇒ 答案里的 `source` 是 `running`（它讲的是**下一轮**要用
的那棵树）。★ **不新增第七个工具** —— `config_get` 已经是「问配置」的那一个。

### 7.1 四个要点

★ **`config_write` 是「加强」不是「包装」。** `Service.Write` 已经是「预校验 → 查 baseRevision → 拒
明文 → 试合并 → 失败回滚 → 原子写」。用文件工具改配置**做不到**这些：它无法检测「我读完之后别人又改
了」，也无法保证「合并失败不留半个文件」。★ **它的档位和别的工具一样由文件说话**：档位表是「代码清单 +
每个文件的 `allows:` + MCP 服务器的 `allows:`」合起来的，`tools/config.yaml` 点名它，它就不点头；
不点名，它就要点头。★ 出厂模板不点名它 —— 问配置不该要点头，改配置应该要。

★ **工具自己界定答案大小，不套通用输出截断。** 通用截断砍的是**字符串**，对 JSON 答案来说就是一份解析
不了的文件；而对 `config_get` 来说，它会留下「半个文件 + 匹配的 revision」—— 正是把另一半改没的那条
路。所以 `config_get` 超过上限（`agentkit/build/configtools.go:466` 的 `defaultConfigReadBytes`，
64 KiB；`tools/config.yaml` 的 `max_read_bytes` 可以改）**拒绝**而不是截断，
`config_template` 一次只给一个分区，什么都不问时给索引。★ **`config_get` 不会泄露密钥**：配置里只有
`${ENV}` 或 `@file:` 引用，值在请求时才解析；读文件拿到的永远是引用。

### 7.2 读与写是两把闸门

`agentkit/spec/write.go` 有**两个**校验入口，它们回答的不是同一个问题：

| 入口 | 管什么 | 谁在用 |
|---|---|---|
| `ValidateReadPath`（`agentkit/spec/write.go:89`） | **路径形状**：在配置根之外、分区不存在、文件名这一分区不接受 —— 读也不认 | `config_get`，API 的读路径（`api/v2_settings.go:201`） |
| `ValidatePath`（`agentkit/spec/write.go:70`） | 形状 **+ 可编辑**：只读分区（`server/`）直接拒 | `config_write`，API 的写路径（`api/v2_settings.go:211`） |

★ 写闸门**保留原名** `ValidatePath`：它的既有调用者**全是写者**，而一个写者拿到更严的答案是安全的，一个
写者拿到更松的答案就是动了不该动的文件。★ 拆开的原因是只读的那一节恰恰是读的人最想看的：`server/` 那一节
不是热加载的，读它不是为了改它，是为了知道**进程手里是什么** —— 而写闸门连读都拒绝，所以「能读不能写」
这件事以前在代码里根本没有位置。★ **`config_write` 不能写 `server/`**，但它**读得了** `server/`。

### 7.3 自举循环：允许

`config_write` 允许写任意放行路径，**包括 agent 文件自己**。生效时点是下一轮建树之前
（`agentkit/build/builder.go:285`），不影响当前轮次和正在进行的 LLM 请求。返回里带 `applied` /
`requiresRestart` 说明这次到底生效了没有，不做额外禁止 —— 禁止的收益抵不上规则复杂度。

★ **`applied: true` 也不等于「本轮可见」。** 六个 `config_*` 工具在装配时拿的是**这一轮开始时的
`*spec.Config` 快照**（`agentkit/build/snapshot.go:122-132`），写入只改磁盘；热重载要到下一轮重新装配
才会换掉这份快照。★ **`config_write` 成功时返回里也带 `note`**，把生效时点写在模型刚拿到结果的地方
（`agentkit/build/configtools.go:432-436`）。

★ **`config_tier` 查不到 agent 时，三种情况给三种答案**（`agentkit/build/configtools.go:601-615` 的
`missingAgentError`）：文件在磁盘且没被排除 → 明说「下一轮生效，别重写、别在本轮复查」；被 `leftOut`
排除 → 明说「修文件，下一轮也不会出现」；磁盘上没有文件 → 才是原来那句。★ **装配失败自己成为一个错误码**
（`agent_unavailable`），消息是装配器自己的原话（`agentkit/build/builder.go:225-234`），而 run 层把这条
错误交那一轮 —— 而不是让它顶成「turn start timeout」。

### 7.4 写不下的文件与写不完的调用

★ **一个文件的全文必须装进**一次**工具调用，而一次工具调用受模型自己的输出上限约束。** `config_write`
的语义是「替换整个文件」，所以一个比输出上限还大的文件**根本写不出来**。★ 而更糟的是**写坏**：模型输出
撞到上限时，厂商有时会把断掉的参数 JSON **补齐**，于是参数解析得过、工具跑得动、文件落盘 —— 落下去的是
**半份文件**，而 run 说「完成」、账本说「完成」，事后**没有人能看出那是一次被砍断的写**。

两条规则各自堵一个口：

| 情形 | 规则 | 落点 |
|---|---|---|
| 文件比一次调用大 | `config_write` 接受 `append`：第一块替换文件，之后每一块**加到**盘上已有内容的后面。每一块都走**同一套**闸门（试合并、树还装不装得上、`baseRevision` 对不对得上），所以盘上的文件在**每一块之后**都是能加载的 | `Service.Append`（`agentkit/spec/write.go:547`） |
| 模型没写完这一次 | 截断只发生在**模型**这一层看得见（厂商的 `finish_reason=length`）。参数被换成一份带标记的 JSON，中间件在**审批之前**就拒绝这次调用：没有东西被写下，也不弹审批卡片，模型拿到的是一句「这次被砍断了，少带点」 | `markTruncatedArgs`（`agentkit/build/callguard.go:59`） |
| 参数到了，但不是一份完整 JSON | 参数可能是「一个完整对象 + 它自己的尾巴再来一遍」——尾巴被数了两次。剩下的正好是它自己的后缀时，就把尾巴去掉，工具跑的是那份完整对象；不是这个形状就不动 | `repairDuplicatedTail`（`agentkit/build/callguard.go:133`） |
| 参数根本不是 JSON | 参数被换成一份带标记的 JSON，中间件拒绝这次调用，模型拿到的是一句「参数没到齐，再发一次，或者少带点」 | `markMalformedArgs`（`agentkit/build/callguard.go:84`） |

★ **拒绝的理由是消息，不是错误。** 模型读到「被砍断了」能换条路走（分块写、用 `edit_file`、给 agent 更大的
`max_tokens`）；读到 `unexpected end of JSON input` 它只会**再试一次同样大的调用** —— 那正是把一次手滑放大
成一个死循环的那一步。★ 同理，参数不是 JSON 时回的是「参数没到齐」，不是 Go 的 JSON 报错原文。

★ **闸门顺序是这件事的一半。** 守卫是中间件链的**第一个**（`agentkit/build/gate.go:167`）：eino 把 handler
按倒序套，所以链首就是**最外层**，它先于审批闸门看到这次调用 —— 一个坏掉的调用**不该**变成一张等人点的
卡片。★ 模型那一侧的实现是 `newCallGuardModel`（`agentkit/build/callguard.go:198`），装在
`BuildModel`（`agentkit/build/provider.go:111`）—— 截断只有模型层看得见，它下面的一切都只看到一次
「看起来完整」的调用。

★ **流式路径上，工具调用比理由先到。** 厂商先发工具调用、最后才发 `finish_reason`，所以那一块必须**等**
到理由才能放行（`agentkit/build/callguard.go:217`）。★ 代价是零：工具调用是这一轮的最后一句话，它前面的
正文早发出去了。

★ **带着 `finish_reason` 的那一块，只能放一次。** 厂商有时把 `finish_reason` 和工具调用的**最后一块**放在同一帧里。
那一块若是先进 `held`、再随后又单独发一次，框架就把最后一块数了两遍 —— 参数变成「一个完整对象 +
它自己的尾巴」，没有任何 JSON 解析器会认。★ 这一点在 2026-11-13 的现场里被逐字对上了（见
`docs/pitfalls.md` §8.3），修法是带理由的那一块不再进 `held`。

★ **`config_get` 与 `config_validate` 的空内容是不同的事。** `config_validate` 只给 `path` 不给 `content`，
**不是**「检查盘上那个文件」，而是「把这个文件清空会怎样」—— `agentkit/build/configtools.go:189` 把空字符串
原样交给 `Diagnostics`：`agents/*.md` 会因此误报「文件是空的」，`skills/*/SKILL.md` 反而给出 `ok`。★ 想看盘上
的现状，用 `config_get {path}`（它本来就读盘），或**不带参数**的 `config_validate`（整棵树按盘体检）。

## 8. 热更新

### 8.1 分区与三个关键选择

| 目录 | 热更新 | 理由 |
|---|---|---|
| `llm/` `agents/` `tools/` `mcps/` `skills/` | ✅ | 都在 `Categories` 里带 `hot: true`（`agentkit/spec/config.go:59-104`），于是 `hotDirList()`（`agentkit/spec/config.go:115-127`）把它们放进 `HotDirs`（`agentkit/spec/reload.go:21`） |
| `server/` | ❌ | 配进程自身（listen / 静态目录 / 上限 / `log_level`、`log_requests` 与 `log.file`），改了要重启 |

★ 这条边界**有测试守着**（`TestServerDirIsNotHot`、`TestWatchIgnoresServerDir`）。★ 这份清单是**推导
出来的**，不是写了两遍。★ **一个热分区必须能把资源重建出来。** `mcps/` 是第一个资源是**进程**的分区：
快照换代时 stdio 子进程被拆掉、新的被拉起来，而工具清单变了就通过重载器用的同一个钩子推进代际。★ 一个资源
重建不出来的热分区，就是一个谎称自己是热的分区。★ `skills/` 的指纹是**递归 walk**，改 `SKILL.md`
下一轮生效（`agentkit/spec/reload.go:51-104`）。

**三个关键选择**：

| 选择 | 理由 |
|---|---|
| **stat 轮询**而非 inotify | inotify 在容器 / WSL / 网络文件系统里**会静默失效**（收不到事件且不报错）；轮询行为一致。代价是每秒几次 `stat` |
| **指纹用「文件名+大小+mtime+内容哈希」** | 要避免的是**漏检** —— 目录名、文件名、大小、mtime 全部计入，内容哈希再补上最后一道（§8.3） |
| **防抖 200ms** | 保存大文件产生多条事件，要的是「保存完了」那一刻 |

防抖的缺省值是 200ms（`agentkit/spec/decl/decl.go:16`）；不是正数的值退回缺省
（`agentkit/spec/reload.go:115-120`）；轮询间隔是防抖的一半，下限 20ms
（`agentkit/spec/reload.go:205-212`）。★ 防抖**只读一次**，读在 watcher 启动的时候
（`agentkit/spec/reload.go:205-208`），而 `watch` 住在 `server/` 下面 —— 那不是热分区。所以改
`watch.debounce` 要重启。

### 8.2 首次加载与重载：两个入口

```text
NewReloader(svc, onChange)
  └─ Reload(): svc.Load() succeeds ⇒ publish and call back; fails ⇒ return the error (a failed first load stops the startup)

Reloader.Reload() (at runtime)
  ├─ svc.Load() succeeds ⇒ swap loaded + onChange
  └─ svc.Load() fails ⇒ keep loaded, return the error; Watch writes one log line
```

改坏配置**不让进程死**：`Reload` 失败时旧配置继续服务，`Watch` 只记一条日志
（`agentkit/spec/reload.go:246-247`）；只有**首次**加载失败会让进程起不来（那时没有「旧配置」可用）——
`NewReloader` 返回错误（`agentkit/spec/reload.go:163-172`），`setupConfig` 把它变成启动失败
（`main.go:419-421`）。换代成功会连同版本号与变了哪些分区一起记下来
（`agentkit/spec/reload.go:249-251`）。

### 8.3 指纹与 `Config.Version`

`Service.Fingerprint()` 算出热更新目录的指纹（`agentkit/spec/reload.go:23-34`），`Load()` 结束时写入
`Config.Version`（`agentkit/spec/config.go:556`，在 `:553` 的校验之后）。

**指纹怎么算的**：对 `HotDirs` 里的每个目录（都在配置根之下），喂进 sha256 的是

```
<the absolute path of the directory>\n
<file name> <size> <mtime in nanoseconds> <content hash>\n     ← one line per file, the file names sorted explicitly
```

★ **用「名+大小+mtime」**（`agentkit/spec/reload.go:74-104`）：要避免的是**漏检**，所以目录名、文件名、
大小、mtime 全部计入。目录不存在**不是错误**（新项目就是空的），只是不贡献指纹。★ **后面还加了一个内容
哈希**（`agentkit/spec/reload.go:106-113`）—— **加上去的，不是替掉 mtime**。理由是 mtime **不是关于内容
的事实**：两次写入落在同一个时钟刻度里，时间戳就一样，于是**一个就地改写、长度没变**的文件看起来像没动
过，而那个改动永远不落地。★ 代价是每轮轮询都读一遍文件内容 —— 配置树很小（几十 KB），读的是页缓存，而
换掉的是一类**说不清的漏检**。

**两个用途**：**热更新的变更检测**（`Watch` 每轮比指纹，变了就进入防抖等待，
`agentkit/spec/reload.go:225-243`）与**「这次跑用的是哪份配置」的归因** —— 配置能在两个 turn 之间变化，
指征必须按 turn 记，否则调试会变成猜谜；它由 `/v2/health` 以 `configVersion` 暴露
（`api/v2_introspect.go:481`）。

★★ **「代际」这个概念已经退休。** `main.go` 的 `agentConfig`（`main.go:766-777`）只有 `Current()` 与
`Publish()`，没有计数器。⇒ 树的身份是**（配置对象，MCP 工具面）**
（`agentkit/build/snapshot.go:207-222`），而不是「第几代」。

### 8.4 换代的实际接线：标记 → 按依赖重建 → 一轮钉住自己的树

```
config_write / the file tools land a change
        └─ svc.Write: the gates, then the atomic write, then Mark(rel)
             (agentkit/spec/write.go:484-523)
                                ▼
        the next Builder.Build asks mgr.Current()
                └─ reloadIfMarked: drain the marks → svc.Load() → mgr.Publish(fresh)
                     (agentkit/build/builder.go:285-304)
                                ▼
        snapshotFor: the identity is (config object, tool surface)
                                ▼
        computeDeps: every agent's own fingerprint
                     (agentkit/build/fingerprint.go:69-126)
                     ├─ its own file, its model file, its tools, its skills
                     ├─ its sub-agents' fingerprints, recursively
                     └─ the live tool list of the MCP servers it mounts
                                ▼
        pool.GetOrBuild(name, fingerprint)
                ├─ a changed dependency ⇒ a new fingerprint ⇒ a new entry
                └─ an unchanged one ⇒ the old entry stays in place
                                ▼
        ★ and a RESUME asks for the tree it started with
        (agentkit/run/runtime.go:593-599 → turn.pin
         → agentkit/build/builder.go:236-271 → pool.Lookup)
```

★ **写路径只标记。** 落盘的是**配置服务**，它记下「哪些文件变了」（`agentkit/spec/config.go:298-308`
的 `Mark`）；**重建发生在下一轮建树之前**，而且**只重建受影响的那几个 agent**。★ **标记是缓存不是真相**：
进程重启后标记是空的，答案仍然对 —— 因为真相是磁盘上的内容哈希（`agentkit/spec/config.go:325-345` 的
`DrainMarks`）。

★ **指纹是「谁变了」的答案，也是「我是谁」的答案。** 池的键从 (名字, 代际) 变成 **(名字, 指纹)**
（`agentkit/build/pool.go:48-82`）。★ **指纹与 agent 同一次调用交出来** —— `Build` 返回
`(agent, fingerprint, err)`（`agentkit/build/builder.go:159-201`），中间若有一次重载，记下来的树就错了。
★ **贡献是「窄」的**：`tools/` 分区只算**这个 agent 用到的那些工具**的配置，加上**覆盖了它那些工具的
`allows:` 子集** —— 一份文件对这个 agent 什么都没说，就算作「没贡献」
（`agentkit/build/fingerprint.go:200-215`）。★ 但**文件路径仍然进指纹的映射**：这样「某文件开始给它的
工具开例外」也能被发现。

★ **MCP 服务器的工具清单已经并进指纹**（`agentkit/build/fingerprint.go:285-300`）：清单变了 ⇒ 挂载它
的 agent 指纹变了 ⇒ 它们重建。清单不进「文件哈希」那一栏，因为服务器起来的时候没有改任何文件。

★ **一轮钉住自己开始时的那棵树。** run 的行记下这棵树的指纹（`storage/runledger.go:176` 的
`Run.TreeFingerprint`），恢复那一轮**按指纹要 agent**（`agentkit/run/runtime.go:893-907` 的
`parentRunOf` → `agentkit/run/turn.go:86` 的 `Turn.pin`），而不是按名字要「当前那棵」。⇒ 被打断的那
一轮继续用它开始时的那棵树；**新配置在下一轮生效**。★ 钉住的那棵树不在池里（进程重启过）⇒ 打一条
warn，退回当前那棵，**不静默换树**（`agentkit/build/builder.go:255-260`）。

★ **恢复轮拿到的是池里那一份实例，不是新装的。** 这不是省事，是**必须**：旧树的内容**只存在于那个实例
里** —— 磁盘上的文件已经变了，重新装配出来的是**新树**。⇒ 池里的旧 entry 因此不是缓存，是**旧树的材料**
（`agentkit/build/pool.go:43-47`）。

★ **失败不回调。** `Service.Load` 的语法 / 模型引用 / 拓扑校验失败时，`Reloader` 不调用 `onChange`，
管理器保留上一份配置（`agentkit/spec/reload.go:174-190`），因此不会出现「接口报 A 版、运行跑 B 版」。
★ **没有推送式换绑**：本仓库根本没有 `Reconfigure` 这类入口。换配置 = 管理器换内容；builder 下次问就是新的
一棵；在飞 turn 仍持有旧实例引用，跑完即被 GC 回收。

### 8.5 影响范围查询

模型写完配置后真正要回答的是：**「下一轮什么会变？」** 它有两个子问题：**下一轮会不会变**（改了一个没人
依赖的文件，下一轮什么都不会变）与**下一轮谁会变**（改了 `agents/skill-expert.md`，只有它和它的父会变）。

⇒ 它的形态是 **`config_get` 的第二个参数**，不新增第七个工具（§7）。落地在
`agentkit/build/impact.go:26-73` 的 `impactAnswer`。

| 字段 | 是什么 |
|---|---|
| `dependents` | **直接**依赖这个文件的 agent（它的指纹输入里点名了这个文件） |
| `rebuilds` | 因为这个文件，下一轮会重建的 agent（带 `path`）／ 下一轮会重建的 agent 全体（不带 `path`） |
| `changed` | 盘上这份与运行中那棵树记下的那份**不同**吗 |
| `changedFiles` | 依赖体系里**内容变了的**那些文件（不带 `path` 时） |
| `requiresRestart` | 这个文件落在冷分区（`server/`）⇒ 要重启进程（只在真需要时出现） |

★ **`dependents` 是直接的，`rebuilds` 是传递的**：父 agent 的指纹里含着子 agent 的指纹，所以子 agent
变了，**父也变**（`agentkit/build/impact.go:102-112`、`:153-169`）。★ 所以这两个列表**不是一回事**：改
`agents/skill-expert.md`，`dependents` 只有 `skill-expert` 自己，`rebuilds` 还有它的父 `main`。

★ **它就是「一次不落地的重建」。** 按新配置算每个 agent 的指纹，和运行中那棵树的指纹比，变了就重建
—— 影响范围查询就是那套机制跑一遍但不落地。⇒ **它不需要另建一套依赖图**：依赖图就是指纹的输入清单。

★ **它现算，不读脏集合。** 脏集合是**缓存**：丢了、重启了、清空了，都不影响正确性。但如果影响范围查询
直接读脏集合，重启之后它会答「没有变化」—— 而盘上其实变了。★ **一个会撒谎的答案比没有答案更糟**：
模型会据此认为「不用改」，然后去改第二遍（`agentkit/build/impact.go:26-36`）。

★ **它答不了什么**：**`server/`** —— 冷分区 ⇒ `dependents` / `rebuilds` 为空，`requiresRestart: true`；
★ **配置根之外的文件** —— 不在依赖体系里 ⇒ 不影响任何 agent。★ **`execute`（shell）改出来的配置** ——
**一样算得出来**（真相是内容哈希，不是路径），但那一条**没有过闸门** —— 影响范围查询答的是「会不会变」，
不是「合不合规」。**这是两件事，工具不要混。**

## 9. 依赖铁律与不变量

### 9.1 依赖铁律

| 约束 | 原因 | 验证 |
|---|---|---|
| `agentkit/spec` **不 import** `agentkit/build` / `agentkit/run` / `api` | 配置不该知道谁消费它 | `go list -deps ./agentkit/spec \| grep -E "ouranoshong/openheron/(agentkit/build\|agentkit/run\|api)"` → 空 |
| `agentkit/spec` **不 import** hertz / eino | 声明层只用标准库加 `yaml.v3`；模型只以 `model_ref` 字符串出现，实例在 agent 装配期解析 | `go list -deps ./agentkit/spec \| grep -E "hertz\|cloudwego/eino"` → 空 |
| `agentkit/run` **不 import** `agentkit/spec` | `agentkit/run` 的契约是 `AgentBuilder` 接口；配置是 `agentkit/build` 的实现细节 | `go list -deps ./agentkit/run \| grep "ouranoshong/openheron/agentkit/spec$"` → 空（它只引 `agentkit/spec/decl` 那个叶子） |
| `agentkit/build` → `agentkit/spec` | 单向 | — |
| `llm/common` **没有任何 openheron 依赖** | 它是叶子包：在这里 import `obs` 会破坏「协议层不依赖运行时」这条规则 | `go list -deps ./llm/common \| grep '^github.com/ouranoshong/openheron/'` → 只打印 `llm/common` 自身 |
| `agentkit/spec` → `persist` | 唯一的落盘原语 | `grep -oP 'persist\.[A-Z]\w*' agentkit/spec/*.go \| sort -u` → 只有 `persist.AtomicWrite` |

★ openheron 有**两条**依赖铁律，而且它们不是同一条：第一条是「`agentkit/spec` 不碰 eino」（声明层保持
框架无关），第二条是「`llm/common` 零 openheron 依赖」（厂商协议层保持运行时无关）。

**今天这些命令实际打印什么**（在本仓库实测）：

```
$ go list -deps ./agentkit/spec | grep '^github.com/ouranoshong/openheron/'
github.com/ouranoshong/openheron/agentkit/obs
github.com/ouranoshong/openheron/agentkit/spec/decl
github.com/ouranoshong/openheron/agentkit/templates
github.com/ouranoshong/openheron/persist
github.com/ouranoshong/openheron/agentkit/spec

$ go list -deps ./llm/common | grep '^github.com/ouranoshong/openheron/'
github.com/ouranoshong/openheron/llm/common
```

★ 与「只有 obs、decl、persist」相比多了**一个**依赖：`agentkit/templates` —— 配置模板的**叶子包**
（`openheron init` 写下的文件、编辑器给的脚手架）。它是叶子包，配置层与命令层才能读到同一个文件，
而不必各带一份拷贝。

★ **配置声明与配置行为分离。** `agentkit/spec/decl` 只放纯结构体和 yaml tag
（`agentkit/spec/decl/decl.go`），不引其它包；`agentkit/spec` 放行为（装载、校验、重载、写入）。
`decl` 因此能在 `build`、`api`、`initcfg` 之间自由传递，不带出依赖链。★ **为什么 `agentkit/spec` 不进
`agentkit/run`**：若 `Runtime` 直接读配置，配置就成了运行时的一部分，`agentkit/run` 单测必须准备配置文件。
保持接口反转注入，测试可注入假实现。

### 9.2 不变量

| # | 不变量 | 违反后果 |
|---|---|---|
| 1 | **不做字段级合并** | 配置来源不可辨，「为什么用了这个值」无解 |
| 2 | 相对路径按**写明的**基准解析 —— 而且基准有两个 | 换个 cwd 启动就换了数据目录 |
| 3 | 密钥不入库，只存 `${ENV}` / `@file:` | 仓库泄密 |
| 4 | 未知字段必报错 | 拼写错误静默失效 |
| 5 | `deferred` 必须被打印 | 「配了没用」不可见 = 静默忽略 |
| 6 | 缺失的 `.openheron/` 不是错误 —— 但缺失**默认 agent** 是 | 新项目无法启动 |
| 7 | **模型引用必须带 `<provider>/` 前缀，且必须解析得到** | 拼错的名字静默退化 ⇒「为什么这么贵」查不出 |
| 8 | MCP 工具一律带 `mcp__` 命名空间，且**只有** MCP 工具能用这个前缀 | 没有命名空间，一个暴露 `ls` 的 server 会让整个 agent 起不来 |
| 9 | 某个 agent 的 `tools` 只支持**尾部** `*` 的前缀模式，其余写法加载期报错 | 一条读起来与实际覆盖范围不同的规则，就是一次静默的错配 |
| 10 | 档位写在**工具自己的文件与自己的词汇里** —— 文件的 `allows:` 点名哪些不用点头，而且**没有全局清单**能与它矛盾 | 一个工具的权限就是一处的一个事实 |
| 11 | 文件系统工具有自己的文件 `tools/filesystem.yaml`，而它是一个**调音旋钮** | 这些工具是 agent 的核心能力，所以那个文件不能有权把它们关掉 |
| 12 | agent 的工具清单是**允许清单**，而且缺省是**没有** —— `skills` 与 `mcps` 同理 | 一个继承「除了这些以外都要」的 agent，离「跑了一个没人挑过的工具」只有一步配置之遥 |
| 13 | **缺省档位是 `ask`** | 猜错的代价不对称：多一次点击，换掉的是一次没人看见的破坏性调用 |
| 14 | 一个 agent 的模型没声明 ⇒ **留在树外**，不是整棵树起不来 | 进程起不来 = 连「哪儿错了」都没人告诉你 |

## 10. 测试矩阵

★ 下表是**实际存在的用例**，不是计划（`agentkit/spec` 包 **228** 个 `Test` 函数）。从未落地的用例已被
删掉或换成真正覆盖同一行为的那个 —— 列一个不存在的测试比不列更糟。

### 10.1 配置根与优先级

| 用例 | 断言 |
|---|---|
| `TestDefaultsSurviveWhenFileOmitsKeys` | 只写 `listen_addr` 时，`max_retries` / `debounce` / `lease_ttl` 仍保留默认（**预填充生效的直接体现**） |
| `TestUnknownFieldIsRejectedWithLineNumber` | 未知字段报错且点名 |
| `TestTheConfigRootIsNeverSearchedForUpwards` | ★ 配置根**不**上溯 |
| `TestAnExplicitRootWinsOverTheEnvironment` | 显式的 `--config-dir` 压过 `$OPENHERON_CONFIG_DIR`，而且它**不存在时不会被创建** |
| `TestTheEnvironmentWinsOverTheProjectRootDir` | `$OPENHERON_CONFIG_DIR` 压过项目根自己的那个目录，同样**不会被创建** |

### 10.2 档位与工具表面

| 用例 | 断言 |
|---|---|
| `TestAConfigurationThatNamesNoExceptionsAllowsNothing` | ★ 一份**不点名**任何例外的配置什么都不放行 —— 缺省是 `ask` |
| `TestOnlyTheNamedToolsRunWithoutANod` | ★ 文件用**自己的**词汇点名例外：`tools/http.yaml` 说 `allows: [request_get]` |
| `TestTheKnowledgeBaseNamesItsOwnExceptions` | `kb_search` 不用点头 —— 一个躲在审批后面的只读工具，是模型会学着不用的那种 —— 而 `kb_index` 要点头 |
| `TestALeftOverPolicyDirectoryIsReported` | ★ 一个退役的分区**不**沉默：那条提示点名它，并说明它的内容去了哪儿 |
| `TestPolicyFallsBackToAsk` | ★ 未列名的名字回落到 `ask` |
| `TestAnEmptyListRemovesEverything` | ★ 一份空清单摘掉**一切** —— 允许清单里没有「无事可做」的捷径 |

### 10.3 留在树外与写入闸门

| 用例 | 断言 |
|---|---|
| `TestAWriteThatWouldTakeAWorkingAgentOutIsRefused` | ★ 写入把 `main` 指向未声明的模型 ⇒ 拒绝，磁盘不动 |
| `TestDeletingAMountedServerNamesTheAgentThatMountsIt` | ★ 删除闸门点名**引用它的那个 agent 文件**与移除什么 |
| `TestTheUndeclaredServerErrorLandsOnTheAgentsFile` | ★ 装载期的错误落在**引用它的那个 agent 文件**上 |
| `TestTheOldModelThresholdIsReportedNotFatal` | 已废弃的 `models[].summarize_at` 被**说出来**，且不致命 |

### 10.4 热更新与换代

| 用例 | 断言 |
|---|---|
| `TestServerDirIsNotHot` | ★ 改 `server/` **不触发**换代 |
| `TestToolsDirIsHot` | ★ 改 `tools/` **触发**换代 |
| `TestWatchIgnoresServerDir` | 端到端：真的写一次 `server/config.yaml`，确认 `onChange` 不被调 |
| `TestReloadFailureKeepsLastGood` | 坏配置后当前配置仍是上一份 |
| `TestChangingOneAgentRebuildsItsTreeAndNobodyElse` | ★ 写一个 agent ⇒ 它与它的父被重建，**别的 agent 不重建** |
| `TestChangingASkillRebuildsOnlyTheTreeThatNamesIt` | ★ 写一个 `SKILL.md` ⇒ 只有 `skills:` 里点了它的 agent 被重建 |
| `TestAFileNobodyDependsOnRebuildsNobody` | ★ 写一个**没人依赖**的 `tools/` 文件 ⇒ **没有 agent 被重建** |
| `TestTheImpactQueryAndThePoolAgreeOnWhoIsRebuilt` | ★ 影响范围查询的 `rebuilds` 与「池里真正被重建的 agent 集合」**完全相等** |
| `TestAResumeRunsOnTheTreeTheRoundStartedWith` | ★ 恢复的那一轮要的是**开始时那棵树的指纹**，而不是当前那棵 |
| `TestAPinnedBuildSaysWhenTheTreeIsGone` | ★ 树不在了就说出来：退回当前那棵并打一条 warn —— **不静默换树** |

### 10.5 MCP 与配置工具

| 用例 | 断言 |
|---|---|
| `TestAMountedServerBringsItsTools` | ★ 挂载一个 server，这个 agent 就拿到它的全部工具（挂载即授予） |
| `TestAnMCPEntryInTheToolsListGrantsNothing` | ★ `tools:` 里的 `mcp__*` 条目不授予任何东西 —— 它们被丢弃 |
| `TestTheTierIsAnswerableWhileTheServerIsUnreachable` | ★ 档位表是从**配置**装配出来的，所以答案不必等一次连接 |
| `TestTheConfigToolsSayWhichSideTheyAnswerFrom` | ★ `config_get <path>` 带 `"source": "disk"`；`config_tier` 带 `"source": "running"` |
| `TestAnEditInsideTheConfigRootGoesThroughTheService` | ★ `edit_file` 改配置根里的文件 ⇒ 走配置服务 ⇒ 树装不上时**不落盘** |
| `TestAnEditInsideTheConfigRootWithTheWrongOldStringIsRefused` | `edit_file` 的 `OldString` 不存在 ⇒ **报错**，不是空补丁 |
| `TestAWriteCannotTouchANonEditablePartition` | `write_file` 改 `server/config.yaml` ⇒ 被写闸门拒 |
| `TestASkillIsWrittenIntoItsOwnFolder` | 写 `skills/x/SKILL.md` 会建出文件夹，清单里回的是 `x/SKILL.md` |
| `TestOnlySkillsIsTwoLevelsDeep` | 别的分区写深一层被拒（`agents/sub/main.md` 会被保存、然后永远不会被加载） |
| `TestSkillsWrittenWhereTheProcessDoesNotReadThemAreRefused` | ★ 运行时从别处读技能时，保存被拒，清单里该分区标只读并点名那个目录 |

## 11. 待决

★ 下面每一项都**未实施** —— 它们要么是配置面与代码面之间还没有接上的一根线，要么是一条已知但刻意
没有堵上的洞。**它们不是现状**。

| # | 项 | 现状 | 要定的 |
|---|---|---|---|
| 1 | `llm/<provider>.yaml` 的 `header:` | 形状在 `agentkit/spec/decl/decl.go:27`，加载器接受它（严格模式下未知键直接报错），但 `build.FromConfig` 组装 `common.Config` 时根本没带 header，而 `common.Config` 里压根儿没有这个字段 —— **解析了，从不被消费，也没有任何报告** | 要么把它交给端点，要么从这个键的规格里删掉 |
| 2 | `tools/kb.yaml` 的 `splitter.headers` | 仍然解析，但加载器随后把它清空（`agentkit/spec/load.go:391-395`）—— **自 v2 起不再生效**，分段改由行扫描得出 | 与上一条同类：接线，还是删键 |
| 3 | `tools/kb.yaml` 模板与加载器的说法不一致 | 模板第 12 行写着 "empty = pure lexical retrieval"（`agentkit/templates/files/tools/kb.yaml:12`），而加载器在 `corpus_dir` 非空时**拒绝**空的 `model`（`agentkit/spec/load.go:1049-1052`） | 改模板，还是让加载器接受空 `model` |
| 4 | `execute`（shell）能绕过配置根的写闸门 | `execute` 是 openheron 自己的工具（`agentkit/build/execute.go`），它跑 shell，shell 里的重定向能写配置根 | 堵它等于给 shell 对配置根只读，那是另一件事；当前选择是**不堵**，并把这条边界写在这里 |
| 5 | `write_file`（整篇替换）没有冲突语义 | `edit_file` 的冲突语义是 **`OldString` 必须仍在**（`agentkit/fsx/fsx.go:809`）；而 `write_file` 没有基线可给 —— 框架的写请求里没有 revision 字段 | 要不要给整篇替换也一条基线；差异已写进 `config_write` 的描述（`agentkit/build/configtools.go:345-355`） |
| 6 | 全树校验仍是 fail-fast | `loadWithMany` 遇到第一个错误就返回，所以 `config_validate {}` 给的是「第一条阻塞错误 + 全部 hint」 | 要一次给全部，得把 loader 从「返回 error」改成「累积 issues」；本轮没做 |
| 7 | `.openheron-example/.gitignore` 仍列着 `.config.lock` | 没有任何代码路径会创建这个文件（并发控制走内容哈希的 revision） | 删掉那行，还是保留它作为「曾经有过的东西」的说明 |
| 8 | 一个 agent 的工具清单不能收窄 MCP 工具 | 收窄 MCP 工具的地方是 server 自己的 `tools:` 过滤器，那是**按 server** 的，对所有挂载它的 agent 共享 | 这是一个**已拍板**的代价（§6.3），不是待办；记在这里是因为它是「挂载即授予」明说的交换 |

## 附录 A：本文引用的代码位置

### 配置层（`agentkit/spec`）

| 位置 | 是什么 |
|---|---|
| `agentkit/spec/config.go:19-26` / `:34` | 目录常量 `DirAgent` `DirLLM` `DirServer` `DirSkills` `DirState` `DirTools` `DirKBIndex` `DirMCP` / `SkillFileName = "SKILL.md"` |
| `agentkit/spec/config.go:59-104` / `:115-127` | `Categories`（各分区与 `hot` 标志） / `hotDirList()` |
| `agentkit/spec/config.go:165` / `:173-178` / `:189-195` / `:203-213` | `Config.leftOut` / `LeftOut()` / `Notice{Path, Message}` / `Deferred()` |
| `agentkit/spec/config.go:228-243` / `:246` / `:258` | `DefaultAgent()` / `deferKey` / `deferFile` |
| `agentkit/spec/config.go:298-308` / `:325-345` / `:355-362` / `:386-395` | `Mark` / `DrainMarks` / `SetSkillsDir` / `skillsElsewhere` |
| `agentkit/spec/config.go:412-460` / `:462-478` | `NewService` —— 三级解析与「只创建缺省根」 / `NewServiceAt` —— 给 `init` 的入口 |
| `agentkit/spec/config.go:480` / `:488` / `:490` / `:492-504` / `:515-527` / `:529-535` | `Root` / `RootIsDefaulted` / `ProjectRoot` / `DataDir` / `LogFile` / `ResolveDir` |
| `agentkit/spec/config.go:548-558` / `:564-593` / `:603-609` / `:619-627` | `loadWithMany`（校验后写 `Version`） / `loadOnly`（预填充、跳过无 loader 的分区） / `retiredDirs` / `noteRetiredDirs` |
| `agentkit/spec/config.go:669-672` / `:738` / `:740-748` | 目录不存在不是错误 / `ErrEmptyDocument` / `decodeStrict` |
| `agentkit/spec/defaults.go:11-13` / `:48-55` / `:57-75` | `DefaultMaxDepth` `DefaultMaxRetries` / `defaultKB` / `defaultServer` |
| `agentkit/spec/decl/decl.go:18-36` / `:38-75` / `:79-163` | `Provider` / `Model` / `Agent` |
| `agentkit/spec/decl/decl.go:269-301` / `:319-328` / `:346-371` / `:402-420` | `Server` / `LogCfg` / 内置工具名 / 六个 `config_*` 工具名的常量块与清单 |
| `agentkit/spec/decl/decl.go:429` / `:439-452` / `:487-518` / `:593` / `:602` / `:610` / `:630` / `:649` | `KnownToolNames`（内置工具名 + 六个 `config_*` 工具名） / `FilesystemCfg` —— 调音旋钮 / `ConfigToolsCfg` —— 配置工具的例外与上限（`max_read_bytes` 的范围也在那儿） / `MCPToolPrefix` / `MCPSeparator` / `MCPServerNamePattern` / `MCPServerOf` / `SkillNamePattern` |
| `agentkit/spec/decl/decl.go:665-684` / `:702` / `:805-818` / `:851-857` / `:912-939` | `ParseToolNamePattern` / `MCPMaxExposedNameLen` / `LenientDuration` / `BadDurations` / `Duration` |
| `agentkit/spec/diag.go:26-39` / `:42-46` / `:71` / `:125` / `:188-196` | `Issue` / `Diagnostics` / `Service.Diagnostics` / `Service.Check` / `parseShift` |
| `agentkit/spec/expand.go:10-38` / `:56-78` / `agentkit/spec/fileerr.go:20` | `expandValue` / `splitFrontmatter` / `FileError` —— 带路径的装载错误 |
| `agentkit/spec/load.go:19` / `:76` / `:140` / `:250` / `:470` / `:131-138` / `:203-224` / `:236-242` | `loadProvider` / `loadAgent` / `loadServer` / `loadTool` / `loadMCP` / `validateAgentTools` / `toolHints` 与 `ToolFileNames` / `decodeTool` |
| `agentkit/spec/load.go:292-306` / `:320-322` / `:331-340` / `:356-387` | `loadTool` 的 `config` 分支 / `toolFileList`（两条错误消息共用的一份名单） / `validateConfigReadBytes` / `noticeConfigToolsAbsent`（★ 文件不在时，那五个只读工具要点头 —— 提示只在真有 agent 用它们时发） |
| `agentkit/spec/load.go:389-418` / `:430-459` / `:541-560` / `:568-583` / `:591-611` | `loadKB` / `AllowedTools` / `expandMCPMaps` / `resolveMCPDuration` / `yamlHasKey` |
| `agentkit/spec/load.go:633-642` / `:644-649` / `:672-711` / `:713-760` | `ParseRef` / `ProviderIDOf` / `leaveOutUndeclaredModels` / `validate`（notice 先于硬规则） |
| `agentkit/spec/load.go:775-796` / `:815-858` / `:862-882` / `:891-919` / `:943-973` / `:982-1000` | `checkReasoning` / `noticeAgentTools` / `knownToolNames` / `validateMCP` / `checkAgentMCPNames` / `MCPServersInUse` |
| `agentkit/spec/load.go:1009-1029` / `:1037-1039` / `:1041-1067` / `:1069-1085` / `:1125-1128` / `:1140-1147` | `noticeCompactionWindows` / `kbErr` / `validateKB` / `validateDepth` / `MaxDepth` `MaxRetries` / `validateAgentGraph` `detectCycle` |
| `agentkit/spec/manifest.go:18-37` / `:68` | `Manifest` / `Service.Manifest` |
| `agentkit/spec/reload.go:21` / `:23-34` / `:36-49` / `:51-104` / `:106-113` / `:115-126` / `:128-137` | `HotDirs` / `Fingerprint` / `HotDirFingerprints` / `dirEntryLines`（递归 walk） / `contentHash` / `Debounce` `collectBadDurations` / `WatchEnabled` |
| `agentkit/spec/reload.go:163-172` / `:174-190` / `:204-254` | `NewReloader` / `Reload` / `Watch` |
| `agentkit/spec/subagent.go:9-16` / `agentkit/spec/summarize.go:25-39` | `AgentNames` / `SummarizeAt` |
| `agentkit/spec/toolfiles.go:30-47` / `:61-67` / `:78-82` / `:95-111` | `toolFiles` / `ToolConfigFile` / `ToolConfigFiles` / `ToolAllows` |
| `agentkit/spec/write.go:70-80` / `:89-133` / `:136-141` / `:151-164` / `:166-208` / `:258-278` / `:280-334` | `ValidatePath` / `ValidateReadPath` / `shapeHint` / `validateWritable` / `validateFileName` / `listNested` / `Skeleton` |
| `agentkit/spec/write.go:381` / `:386-402` / `:417-441` / `:466-486` / `:488-528` / `:549-591` / `:598-663` | `Revision`（内容哈希） / `ReadFile` / `agentsLostBy` / `agentsLostByChange` `checkNoAgentLost` / `Write`（闸门 + 原子写 + 标记） / `Append`（★ 分块写：同一套闸门，结果必须是能加载的） / `WriteMany` |
| `agentkit/spec/write.go:718-769` / `:785-811` / `:839-888` / `:892-907` / `:924-932` / `:932-951` | `referencesTo` / `checkNotReferenced` / `Delete` / `checkRevision` / `checkSecretFor` / `checkSecret` |

### 装配层（`agentkit/build`）

| 位置 | 是什么 |
|---|---|
| `agentkit/build/assemble.go:18-28` / `:30-40` / `:136-176` | `baseConfig` / `applyToolConfig` / `assemble` |
| `agentkit/build/builder.go:159-201` / `:211-216` / `:225-234` / `:236-271` / `:285-304` | `Build`（返回 agent **与**它的指纹） / `agentUnavailable` / `leftOutMessage` / `pooledRoot`（钉住的那棵树在这里取回） / `reloadIfMarked`（标记 → 重读 → 发布） |
| `agentkit/build/config.go:15-44` | `ConfigFromEnv` —— 不在主路径上的兜底 |
| `agentkit/build/configgate.go:30-46` / `:57-66` / `:88-101` / `:110-117` | `newConfigGate` / `OwnsPath` / `ReadConfig` / `WriteConfig` |
| `agentkit/build/configtools.go:85-90` / `:92-449` / `:127-129` / `:277-283` / `:307` `:313` `:331` / `:380` `:382` / `:390-391` / `:430-435` / `:466` / `:473-478` / `:599` | `sourceDisk` `sourceRunning` / 六个工具 / `impact` 分支 / `config_notices` / `config_tier` 的三个答案 / `svc.Append`、`svc.Write`、两个版本号、那条 `note` / `defaultConfigReadBytes` / `configReadBytesLimit` / `missingAgentError` |
| `agentkit/build/fingerprint.go:69-78` / `:95-126` / `:133-314` / `:356-375` | `computeDeps` / `depWalker.of` / 各依赖的加入 / `fingerprintOf` |
| `agentkit/build/gate.go:43-81` / `:89-124` / `:167-181` | `newApprovalMiddleware` 与它的同步 / 流式两条路 / `toolChain`（★ 守卫是**链首**，所以它最外层） |
| `agentkit/build/hub.go:71-102` / `:119-176` | `skillModelHub.Get` / `skillAgentHub.Get` |
| `agentkit/build/impact.go:26-73` / `:102-112` / `:119-143` / `:153-169` | `impactAnswer` / `rebuilds` / `changedFiles` / `dependents` |
| `agentkit/build/introspect.go:36-47` / `agentkit/build/manager.go:20-29` | `reasonOf`（两个理由） / `ConfigManager.Current`（只要配置，不要代际） |
| `agentkit/build/mcpmanager.go:98` / `:175` / `:215` / `:246` | `Warm` / `Refresh` / `ToolsOf` / `surfaceHash` |
| `agentkit/build/mcptools.go:68-121` / `:123-133` / `:141-143` / `:151-164` / `:173-184` | `mcpPlan` / `mcpOffers` / `mcpExposedName` / `sanitizeMCPToolName` / `mcpAllowedNames` |
| `agentkit/build/nettools.go:21-26` / `:37-65` / `:67-91` / `:196-200` | 三个超时与输出上限 / `dialPublicOnly` / `isPublicIP` / 搜索后端的唯一取值 |
| `agentkit/build/policy.go:16-19` / `:55-60` / `:115-148` / `:163-182` / `:184-209` / `:242-258` | 两档 / `TierOf` / `newTable` / `Blocked` / `Filter` / `Layer` |
| `agentkit/build/pool.go:34-82` / `:90-99` | `GetOrBuild`（键是名字+指纹） / `Lookup` |
| `agentkit/build/provider.go:30-69` / `:71-86` / `:94-111` / `:113-119` / `:121-146` / `:148-160` / `:162-171` | `FromConfig` / `Resolve` / `BuildModel`（★ 这里装上截断守卫的模型包装） / `effectiveTier` / `RequestOptionsFor` / `shallowMerge` / `ReasoningFields` |
| `agentkit/build/skill.go:89-186` / `:190-215` / `:217-233` | `scopedSkillBackend` / `ResolveSkillsDir` / `probeProjectSkills` |
| `agentkit/build/snapshot.go:147-185` / `:207-222` / `agentkit/build/spec.go:73-80` | `newSnapshot` / `snapshotFor`（身份是（配置对象，工具面）） / `policyOf`（三处合成一张表） |
| `agentkit/build/tools.go:95-101` / `:103-150` / `:152-178` | `builtinAllowedTools` / `reservedTool` 与 `reservedToolNames`（★ 保留名带 owner：拥有它的那个 provider 可以提供它，别的不行） / `toolRegistry.Build` |
| `agentkit/build/callguard.go:30-49` / `:59-110` / `:133-161` / `:194-317` / `:319-465` / `:467-535` / `:537-579` | 两个标记与它们的常量 / `markTruncatedArgs` `markMalformedArgs` `isTruncatedArgs` `isMalformedArgs` `isJSONObject` / `repairDuplicatedTail`（去掉重复的尾巴） / `callGuardModel`（模型包装：★ 只有它看得见截断与重复的尾巴） / `guardCalls` `guardChunks` `markChunksTruncated`（★ 分块的参数要拼起来才能判） / `newCallGuardMiddleware`（★ 链首，所以它先于审批看到这次调用） / `truncationRefusal` `malformedRefusal` |

### 文件后端与运行期

| 位置 | 是什么 |
|---|---|
| `agentkit/fsx/configgate.go:11-35` | `ConfigGate` —— fsx 自己定义的窄接口 |
| `agentkit/fsx/fsx.go:232-250` / `:739-775` / `:776-835` / `:809` | `resolveWrite`（写入边界） / `Write` / `Edit` —— 配置根的写在这里转交 / ★ `OldString` 找不到就报错 —— 文件工具的冲突语义 |
| `agentkit/run/loop.go:125-140` / `:240-260` / `agentkit/run/runtime.go:593-599` / `:893-907` / `agentkit/run/turn.go:80-86` | `makePrepareAgent`（带钉住） / `settleAgent` / 恢复时取父 run 的树 / `parentRunOf` / `Turn.pin` |
| `storage/runledger.go:169-176` | `Run.TreeFingerprint` |
| `main.go:98` / `:111-121` / `:123-140` / `:149-157` / `:367-462` / `:440` `:459` `:460` / `:600` / `:614` / `:766-777` / `:792-821` / `:832-837` / `:839-844` | `--config-dir` / 用法 / `warnUnusedHomeConfig` / `configLoadError` / `setupConfig` / 三个 `override` 读点 / `FRONTEND_MODE` / `STATIC_DIR` / `agentConfig` / `Current` `Publish` / `configVersion` / `override` |
| `api/v2_introspect.go:406` / `:481-482` / `api/v2_settings.go:64-90` / `:91-104` / `:201-216` | `deferred` 与 `configVersion` / `settingsFail` / manifest handler / `readPath` `writePath` |
| `initcfg/initcfg.go:56-94` / `:198-224` | `BuildPlan` / `copySection` |
| `.env.test` / `scripts/test-backend.sh:47` `:76` `:90-105` `:128-130` | 测试环境的约定与隔离脚本 |
