# 配置系统设计说明

> 状态：本文描述的是 **openheron 当前的配置系统**。`mcps/` 分区**已实施**（§6.7），某个 agent 的 `tools` 里的**允许清单**已实施（§6.3），**`policy/` 分区的退场**同样已实施：缺省档位是 `ask`，而例外由**提供**该工具的那个文件点名（§6.3、§6.6）。★ 已经**没有全局档位清单** —— 「不被暴露」只在两处说。只存在于上游仓库的文档标注为「upstream」。
> 范围：包含 —— 配置根、分区、文件规格、加载与优先级、热更新、依赖铁律。不包含 —— HTTP 接口层（见 `docs/api-v2-design.md`（upstream））与前端。
> 框架版本：eino `v0.9.22`（`go.mod:9`）、`gopkg.in/yaml.v3 v3.0.1`（`go.mod:20`）。
> 本文按 AGENTS.md 的文档语言规则**只有中文版** —— 架构与代码设计类文档不做双语。

---

## 0. 一句话

**配置与运行数据分家**：配置是可读、可 review 的文件（要不要提交进版本库由你定），
运行数据一律不进库；厂商知识进 `llm/*.yaml`，Go 代码里零厂商知识；**只有一份配置生效**，让「这份配置到底是什么」一眼可辨。

★ 只有一份的理由不是整洁：一旦配置根取决于「进程是从哪儿启动的」，同一条命令换个目录跑就会读到另一份配置，
测试实例也会读到开发者自己的配置（`agentkit/spec/config.go:242-254`）。

---

## 1. 目标与非目标

### 目标

| # | 目标 | 可验证判据 |
|---|---|---|
| 1 | **一个**配置根（项目级路径，或显式指定） | 任一时刻只有一份配置生效，且没有继承链（`agentkit/spec/config.go:242-248`） |
| 2 | 按功能分区 | 新增一类配置不改已有文件布局（`agentkit/spec/config.go:40-70`） |
| 3 | 零新增依赖 | `go.mod` 只把已在缓存的 `gopkg.in/yaml.v3` 转 direct（`go.mod:20`）—— YAML 这一层就是全部成本 |
| 4 | 「配了没用」必须可见 | 未知字段报错；认识但未兑现的键进 `deferred` 并**强制打印**（`agentkit/spec/config.go:553-567`、`main.go:206-208`） |
| 5 | 密钥不入库 | 档案只存 `${ENV}` 引用或 `@file:` 路径 —— 配置编辑器拒绝保存明文（`agentkit/spec/expand.go:9-38`、`agentkit/spec/write.go:528-620`） |

### 非目标

- ❌ **配置根不做上溯搜索。** 上游设计会向祖先目录查找；openheron 故意不做（`agentkit/spec/config.go:242-254`）。
- ❌ **没有家目录那一层。** `~/.openheron` 永远不会被自动认领 —— 若它存在而不在用，运行时会把话说明白，请你显式指名（`main.go:110-127`）。
- ❌ **不让配置承载密钥本体。** 加载器容忍明文密钥（`agentkit/spec/expand.go:35-37`），但编辑器拒绝保存明文（`agentkit/spec/write.go:528-620`）—— 两条路径故意不对称。
- ❌ 不让 **`server/`** 参与热更新（它配的是进程自身，改了要重启）。`llm/` `agents/` `tools/` `mcps/` **已实施**热更新，见 §8。★ **深度与重试上限**正是为此住在 `server/` 里：每个数字只有一份，而且不在运行时被换掉（§12.2）。

---

## 2. 决策基线

| # | 项 | 决策 | 理由 |
|---|---|---|---|
| 1 | 目录名 | `.openheron` | 短、无歧义、可入版本库（`agentkit/spec/config.go:105`） |
| 2 | 分区 | `server/` `llm/` `agents/` `tools/` `mcps/`（会被加载）、`skills/`（技能）、`state/`（运行数据） | 一类配置一个文件集合（`agentkit/spec/config.go:18-26`、`agentkit/spec/config.go:40-70`） |
| 3 | 格式 | **YAML** —— `agents/*.md` 用 YAML frontmatter | 与 agents 的 frontmatter 同构 ⇒ `.openheron/` 全域统一（`agentkit/spec/expand.go:48-65`、`agentkit/spec/load.go:75-98`） |
| 4 | 粒度 | **一实体一文件** | `llm/<provider>.yaml`、`agents/<id>.md`（`agentkit/spec/load.go:18`、`agentkit/spec/load.go:75`） |
| 5 | 覆盖 | 不适用 —— 只有一份配置，没有「谁覆盖谁」 | 字段级合并让配置来源不可辨。★ 已经**没有**「两者取更严」这条规则：深度与重试上限只有**一个**家（`server/config.yaml`），所以没有什么要对账（`agentkit/spec/load.go:756-769`） |
| 6 | 优先级 | `默认 < 配置根 < 环境变量`（★ **只有少数几个变量真做到**，见 §4.4） | env 是一次性覆盖，文件是可提交的本项目管理（`main.go:225-227`） |
| 7 | 未知字段 | **严格报错**，带行号 | `max_retry` 拼成 `max_retries` 应在启动期暴露（`agentkit/spec/config.go:553-567`、`yaml.v3@v3.0.1/decode.go:944`） |
| 8 | 运行数据 | `.openheron/state/` | 配置与数据同根，一条忽略规则一处生效（`agentkit/spec/defaults.go:62`、`agentkit/spec/config.go:314-326`） |
| 9 | skills | 默认在 `.openheron/skills/` —— 由 `$EINO_EXT_SKILLS_DIR` 决定，否则从工作目录上溯找 `.openheron/skills` | ★ 这条规则与配置根**不同**：技能是一棵目录树，从上级目录复用一个无害，而配置根是一组实体（`agentkit/build/skill.go:182`）。★ 也正因为两个根可能不同，编辑器保存 skill 前要问一次**运行时**从哪儿读（§6.5） |
| 10 | 根探测 | `--config-dir` → `$OPENHERON_CONFIG_DIR` → `<projectRoot>/.openheron` | ★ **不做上溯搜索** —— 取决于工作目录的根会让同一条命令读到另一份配置（`agentkit/spec/config.go:242-254`） |
| 11 | 多模型 | 一个 provider 文件持 `models[]`，引用 `<provider>/<model>` | 「入口 agent 强模型 + 下属弱模型」是最常见诉求；且与 `agents/*.md` 的 `model:` 引用形式一致（`agentkit/spec/load.go:55-68`、`agentkit/spec/load.go:499-508`） |
| 12 | 模型构造 | **每个 agent 一个实例**（粒度 = provider × model × reasoning 档位 × 端点字段形状 × timeout） | ★ **实施时更正**：原写「一个 provider 一个实例」，理由是「连接池天然共享」。后半句不成立 —— `FromConfig` 每次新建 `http.Client`（`agentkit/build/provider.go:40`、`agentkit/build/provider.go:47`），而 agent 各自声明模型与思考档位，本就是彼此独立的（`agentkit/build/provider.go:81-95`） |
| 13 | 时长分两档 | `llm/*.yaml` 里的 `timeout` 严格报错；`debounce` / `start_timeout` / `tools/` 下的时长降级并记 `deferred` | 前者写错会让超时行为不可预测；后者只影响「等多久」，不该让程序起不来。**降级必须出声**（`agentkit/spec/decl/decl.go:612`、`agentkit/spec/decl/decl.go:537-546`、`agentkit/spec/reload.go:80-84`） |
| 14 | 子 agent 规格 | 放 `decl.SubAgentSpec`，装配层直接用 | `agentkit/build → agentkit/spec` 本就是允许的单向依赖；另立一份会「编译不报错、运行期少个值」（`agentkit/spec/decl/decl.go:641-659`、`agentkit/spec/subagent.go:18-28`、`agentkit/build/spec.go:23`） |
| 15 | MCP 工具命名 | 暴露给模型的名字一律加命名空间 `mcp__<server>__<tool>` | **已实施**（见 §6.7）。MCP 工具名**由远端服务器定**：会撞保留工具名（`ls` / `execute`），也常带点号（`github.create_issue`）—— 后者过不了 provider 的 function name 字符集。命名空间把「撞名」变成「不可能」。前缀与分隔符是常量（`agentkit/spec/decl/decl.go:364`、`:345`），暴露名只在一处合成（`agentkit/build/mcptools.go:140`），provider 注册在工具注册表里（`agentkit/build/tools.go:35`、`:41`） |
| 16 | 前缀匹配 | 某个 agent 的 `tools` 接受精确名 **+** 尾部 `*` 的前缀模式 | **已实施**（见 §6.3）：`newTable` 留着前缀模式（`agentkit/build/policy.go:100`），`Table.Blocked` 匹配它们（`agentkit/build/policy.go:130`）。★ **全局 deny** 跟着分区一起没了，所以已经没有第二层可供匹配 —— 前缀语法是它**唯一**的所在。★ 这份清单是**允许清单**，所以一个模式点名的是这个 agent **拿到**什么，而不是拿不到什么 |
| 17 | 档位写在哪 | **跟着工具走**：每个文件的 `allows:` 点名**不用点头**的那些，其余一律要点头；★ 已经**不再有任何全局清单** —— `policy/permissions.yaml` 没了 | **已实施**（见 §6.3、§6.6）：server 自己的文件用 server 自己的词汇点名它的例外，`tools/` 下的文件点名自己的例外，而那张表是从配置装配出来的。★ 档位是关于**某一个工具**的一句话，所以就该待在那个工具的文件里 —— 而且在那里用的是**同一套词汇**（`http.yaml` 说 `post`，所以它能说 `allows: [post]`；中心那份文件却不得不点名内部名字 `request_post`）。★ 全局清单跟着一起走了：「不被暴露」已经说了两遍（文件的 `allows:`、agent 的 `tools:`），第三处就多一处要同步的地方 —— 而它是唯一必须说暴露名的那处 |
| 18 | 缺省档位 | **到处都是 `ask`** —— 代码带一份**缺省** allow 清单，而文件的 `allows:` 对它覆盖的那些工具**加进**去 | ★ **已实施**（见 §6.3）：缺省是 `ask`（`agentkit/build/policy.go:55` `TierOf`），代码那份清单是**地板**（`agentkit/build/tools.go:61` `builtinAllowedTools`），文件只能往上加；那张表是从配置装配出来的，所以档位在 server 还没起来之前就能回答（`agentkit/build/mcptools.go:172` `mcpAllowedNames`）。★ 全体同一条规则：文件系统工具、HTTP 工具与一个 MCP server 行为一致，也没有需要记的分组缺省。★ 代码那份清单之所以存在，是因为 `task` —— 委派工具 —— **没有自己的文件**，而一个不点一下就不能委派的 agent 等于不能委派。MCP server 的工具是**运行期才发现**的：openheron 不知道 `delete_repo` 做什么，而 stdio 之下 server 是带着 agent 自己权限的子进程 ⇒ 默认点头是这场交易里安全的那一侧 |
| 19 | 一个 agent 加载哪些 MCP server | agent 自己的 `mcps:` 清单，写在它的 frontmatter 里 | **已实施**。★ 一个 server 是**能力**，而能力属于用到它的那个 agent —— 研究员不需要部署 server。★ **键缺失 = 不加载任何 server**，`[]` = 不加载任何 server —— 这两件事现在是同一件事，而且同一件事的意思是**什么都没有**：那个用来分辨「没声明」与「声明为空」的指针没了，因为这两者现在意思相同（`agentkit/spec/decl/decl.go:135`）。⇒ 挂载一个 server 是**写下来**的决定：server 文件说 server *是什么*，agent 的 `mcps:` 说**谁启动它** |
| 20 | 知识库的目录键 | **`corpus_dir`**，不是 `corpus` | ★ 它的值是一个**目录**，而配置里别的目录键都这么说：`data_dir`、`static_dir`。「corpus」是信息检索里对「检索系统据以作答的那批文档」的叫法 —— 不懂这个词的人也该读得懂这个键。★ 旧键仍然能加载，所以这次改名不会弄坏一份能用的配置；它会在 `deferred` 里说出来（`agentkit/spec/decl/decl.go:569-582`、`agentkit/spec/load.go:231-237`） |

### 「一实体一文件」而非单文件分区表

单文件 `config.yaml` + 具名表（如 `llm.profiles.default`）也能表达同样内容，但：

- 具名表**无法逐字段合并**（数组表按位置、映射表整体替换），而实体文件天然是整体替换；
- 一个 provider 一份文件，`git log` 里能直接看到「谁改了哪个厂商」；
- 与 `agents/*.md` 同一原则，加 agent 不必改既有文件结构。

在 openheron 里，实体单位是「一个 provider 一份文件」（`agentkit/spec/load.go:18`），文件里的每个模型同样是一个实体（`agentkit/spec/load.go:55-68`）——
这正是模型用 `<provider>/<model>` 引用、而不是用路径引用的原因。

---

## 3. 目录布局

```
<projectRoot>/.openheron/
├─ agents/main.md            ← agent: frontmatter + body (the body is the prompt)
├─ llm/<provider>.yaml       ← vendor profile: base_url / api_key / models[]
├─ server/config.yaml        ← the process itself: address / dirs / timeouts / the caps
├─ tools/<tool>.yaml         ← http / web_search / wikipedia / kb
├─ mcps/<server>.yaml        ← one MCP server: command / transport / tier
├─ skills/<name>/SKILL.md    ← skills (resolved by their own rule, see below)
└─ state/                    ← runtime data, never committed
```

★ **没有 `policy/` 目录。** 那个分区已经没了：档位住在**提供**工具的那些文件里，而上限住在
`server/config.yaml`（§6.3、§12.2）。★ 一份仍然带着这个目录的配置**不会被读** —— 但它
**也不沉默**：加载器把这个退役的分区记进 `deferred`，并说明它的内容去了哪儿
（`agentkit/spec/config.go:410-419` `noteRetiredDirs`）。

空的子目录**不自动创建**：目录不存在就当「没有东西可加载」（`agentkit/spec/config.go:481-485`）。
★ **配置根本身是例外** —— 缺省的那份根不存在时会被建出来（`agentkit/spec/config.go:281-288`），
因为「还没有配置」和「目录不在」本来就是同一件事。

★ **`tools/` 是自成一区的分区** —— 它装着外部工具的配置（`http.yaml`、`web_search.yaml`、`wikipedia.yaml`）
与知识库的配置（`kb.yaml`）（`agentkit/spec/load.go:187-215`），并且**进**热更新（`agentkit/spec/config.go:56-60`），
而 `server/` 不进（`agentkit/spec/config.go:51-55`）。拆分的理由是热更新：`server/` 配的是进程自身、运行期换不掉，
而「改一个工具的超时」本该下一轮就生效。知识库的*索引*是运行数据，落在 `state/kb/<index>`（`kbcmd.go:130`）。

★ **没有 `.config.lock`** —— 配置编辑器走的是**内容哈希的乐观并发**：
`spec.Revision()` 是文件 SHA-256 的前八位十六进制（`agentkit/spec/write.go:265-270`），
带着过期 `baseRevision` 的写入会被判为冲突（`agentkit/spec/write.go:526-543`）。不需要锁文件，所以也没有锁文件。
（样例的忽略规则里仍列着 `.config.lock` —— 那是一处遗留，见下面的忽略规则。）编辑器的设计见 `config-editor-design.md`（upstream）。

`state/` 取代了旧的 `DATA_DIR=./data`，由 `Service.DataDir("")` 创建（`agentkit/spec/config.go:314-326`）：

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

★ **checkpoint 住在会话自己的目录里** —— `<sessions>/<sessionID>/checkpoints/<checkpointID>.ckpt`
（`storage/filestore/checkpoint.go:39`、`storage/filestore/checkpoint.go:64-67`）。
一个会话的持久化物终于在同一个目录里：删会话目录 = 删全。
损坏的 checkpoint 会被另存为 `<checkpointID>.ckpt.corrupt`，而不是被悄悄覆盖
（`storage/filestore/checkpoint.go:22`、`storage/filestore/checkpoint.go:185-194`）。
**旧的平铺布局**（`state/checkpoints/<id>.ckpt`）仍作为只读回退被查询（`storage/filestore/checkpoint.go:86-96`），
否则老会话里还等着人审批的那一轮就再也恢复不了。见 `checkpoint-store.md`（upstream）。

★ **`state/` 里不落日志。** 会话与检查点之外，`state/` 里不落别的文件。运行日志**默认**走
标准错误；落盘是可选的：`server/config.yaml` 的 `log.file` 指定一个路径（相对**配置根**）
才会写文件，由 `lumberjack` 按大小轮转，打不开文件只告警、不阻止启动
（见 §6.4 与 `docs/logging-design.md` §10）。

### 版本控制边界

```
# Runtime data does not go into version control — the config wants diff / blame / rollback, session content must not leak
state/
.config.lock

# Safety net for plaintext keys (the normal way to write them is ${ENV} or @file:)
llm/*.local.yaml
*.key
```

**为何 `state/` 必须排除**：会话与日志是高频变动数据，进版本库会把历史撑成噪声，且**泄露会话内容**。

反过来，配置**建议**进库 —— 那样「这台机器上跑的是什么」才有答案、可 diff、可 blame、可回滚。
但那是**建议**，不是前提：全放本地、只自己看，同样跑得好好的。真正不可让的是 `state/`：**它必须排除**
—— 里面是会话正文。

★ 在**本**仓库里，本地配置根根本不入库：`.gitignore` 排除了 `/.openheron/`
（"the real local configuration (the factory sample is in `.openheron-example/`); untracked, so that personal configuration and secrets stay out of the repo"），
而样例配置**是**入库的，放在 `.openheron-example/` 下。上面那段就是样例的忽略文件 ——
它仍列着 `.config.lock`，而现在没有任何代码路径会创建这个文件。

### 可直接复制的出厂样例

仓库根的 `.openheron-example/` 是一套**经过加载验证**的配置（含子 agent 与思考档位）：

```bash
cp -r .openheron-example/{agents,llm,skills} /your/project/.openheron/
```

它的 `README.md` 列了三条最容易卡住人的规则：`${ENV}` 未设会在启动时**提醒**（不阻塞）、
`model` 必须带 `<provider>/` 前缀、时长必须带单位。

> ★ **要一份「最小可运行」的**，不必手抄：`openheron init` 会在**当前目录**建出
>   一份配置，并且**只补确实缺的东西** ——
>   绝不覆盖已有的同名文件（同名文件是**整体替换**语义，
>   随手写一份等于把用户自己那份悄悄换掉）。
>   给了 `--config-dir` 就写到那儿，否则写 `<目标目录>/.openheron`。
>   方案见 `init-design.md`（upstream）。
>
> 它写什么：`agents/main.md`、`agents/task.md`、`agents/agent-expert.md`、
> `agents/skill-expert.md`、`llm/<provider>.yaml`、`server/config.yaml`、
> `tools/` 下的五个文件、`mcps/fetch.yaml`、`mcps/time.yaml`、`mcps/deepwiki.yaml`，
> 以及 `skills/openheron-config-<name>/SKILL.md` 四个技能
> （`initcmd.go:138-150`、`initcfg/initcfg.go:77-86`、`initcfg/initcfg.go:110`、
> `initcfg/initcfg.go:167`、`initcfg/initcfg.go:185`）。
> ★ **`tools/` 这一段是默认写的**：那五个文件是这棵树的一部分 —— 出厂的 agent 会点名
> `request_get` / `duckduckgo_search` / `wikipedia_search`，而没有 provider 提供的名字
> 会被当成笔误报出来。★ 想留下它就加 `--no-tools`（`agentkit/build/nettools.go`）。
> ★ **没有 `policy/` 这一段 —— 那个分区已经没了。** 档位由**提供**工具的那些文件点名
> （§6.3、§6.6），而上限已经写进 `server/config.yaml`（§12.2）。★ 一份「说的就是代码已经在做的事」
> 的文件是噪音，而配置树里的噪音会被当成意思读。

---

## 4. 加载与优先级

```
built-in defaults
   ↓ pre-fill the struct
<config root>/               the one and only
   ↓ explicit override
environment variables        highest
```

**配置根只有一个**，来源三级：

| 顺序 | 来源 | 说明 |
|---|---|---|
| 1 | `--config-dir <path>` | 指向**配置根本身**（里面直接有 `llm/` `agents/`）—— `main.go:84-85` |
| 2 | `OPENHERON_CONFIG_DIR` | 同上 —— `agentkit/spec/config.go:103`、`agentkit/spec/config.go:270` |
| 3 | 兜底 | `<projectRoot>/.openheron` —— `agentkit/spec/config.go:273`；项目根是 `--project-root` 或工作目录（`main.go:87-88`、`agentkit/spec/config.go:256-266`） |

★ **兜底的那份根会被创建；指名的不会。** 只有缺省根在不存在时才会被建出来
（`agentkit/spec/config.go:281-288`）：`--config-dir` 或 `$OPENHERON_CONFIG_DIR` 指向一个不存在的地方是用户笔误，
它不能变成一棵悄悄空着的配置树。

★ **不做上溯搜索 —— 而且 openheron 从来没有做过。** 上游设计会从工作目录向上找 `.heron/`；
这条规则是被**故意去掉**的（`agentkit/spec/config.go:242-254`）：一旦配置根取决于「进程是从哪儿启动的」，
同一条命令换个目录跑就会读到另一份配置，测试实例也会读到开发者自己的配置。
家目录里的配置同样**不是**兜底层：若 `~/.openheron` 存在而不在用，运行时会把话说明白，请你显式指名（`main.go:110-127`）。

★ **真正会覆盖值的环境变量只有少数几个**（§4.4）：它们是在使用点被读到的
（`main.go:225-227`、`main.go:415`、`main.go:429`），而不是靠一层通用覆盖 ——
没有「env 说了算」这条规则，只有若干处「这里会看一眼环境变量」。

### 4.1 缺省值靠「预填充结构体再 decode」

加载器先用内置缺省值**预填充**配置结构体，再把每个文件 decode 到它上面（`agentkit/spec/config.go:372-380`）：
`server/` 段从 `defaultServer()` 起步（`agentkit/spec/defaults.go:58-69`），知识库从 `defaultKB()`
（`agentkit/spec/defaults.go:49-56`）。★ 没有第三份预填充：档位表**不再**是一个配置段 ——
它由提供工具的那些文件装配而成（§6.3）。
`loadServer` 是 decode 到预填充结构体的一份**副本**上（`agentkit/spec/load.go:100-107`），
于是文件里没出现的键保留缺省值。

「只写文档里出现过的键」这个行为不是 openheron 自己写的 —— 它是 yaml.v3 的。
`mappingStruct`（`yaml.v3@v3.0.1/decode.go:878`）遍历文档的键，只写它找到的字段：

```go
if info, ok := sinfo.FieldsMap[sname]; ok {   // yaml.v3@v3.0.1/decode.go:921
    ...
    var field reflect.Value
    if info.Inline == nil {
        field = out.Field(info.Num)
    } else {
        field = d.fieldByIndex(n, out, info.Inline)
    }
    d.unmarshal(n.Content[i+1], field)          // decode.go:935 ← only the keys that appear are written
}
// fields that do not appear: never touched
```

⇒ 逐层 decode 天然实现「只覆盖出现的键」，无需自己写 merge 逻辑。

**已核实**：这不是假设，是逐行读过 `yaml.v3@v3.0.1` 的 `mappingStruct` 实现得出的结论 ——
也就是 openheron 实际依赖的那个版本（`go.mod:20`）。上游文本引的是同一个文件的 `yaml.v3@v3.0.1/decode.go:930-934`；
在 `v3.0.1` 里那一段位于 `:929-934`，`d.unmarshal` 调用位于 `:935`，所以上游的引用依然成立。

### 4.2 严格模式

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

每个分区都走它（`agentkit/spec/load.go:24`、`agentkit/spec/load.go:82`、`agentkit/spec/load.go:102`、
`agentkit/spec/load.go:229`、`agentkit/spec/load.go:237`、`agentkit/spec/load.go:133`）。

**为什么严格**：宽松模式下拼错字段名会**静默用默认值**，而「为什么我的配置没生效」是排障成本最高的一类问题。
yaml.v3 的报错自带行号（`yaml.v3@v3.0.1/decode.go:944`）：`line N: field X not found in type Y`。

★ **空文档不是解析错误。** 解码器把一份没有 YAML 的文件报成 `io.EOF` —— 这话是真的，却毫无用处：
「EOF」没告诉用户该做什么。openheron 把它换成 `ErrEmptyDocument`
（「the file is empty (or contains only comments)」）（`agentkit/spec/config.go:504-518`），
因为「只有注释的模板」是一种真实存在的文件形状：照着模板建出来的 `tools/` 文件根本没有文档（`agentkit/spec/load.go:109-136`）。

### 4.3 「配了没用」的三种形态

| 形态 | 处理 | 例子 |
|---|---|---|
| **不认识的键** | 硬报错，带行号 | `max_retry`（拼错）—— `yaml.v3@v3.0.1/decode.go:944` |
| **认识但本版不兑现** | 进 `deferred[]`，**启动必须逐条打印** | `models[].summarize_at` 仍会被解析，但不再被读 —— 阈值归 agent 管（`agentkit/spec/load.go:61-65`） |
| **认识但此刻缺运行期前提** | 同上，且**不阻塞启动** | `api_key` 的 `${ENV}` 未设 —— 请求该端点时才 401（`agentkit/spec/load.go:40-43`） |

```go
func (c *Config) Deferred() []string                                  // agentkit/spec/config.go:169
```

```go
for _, d := range c.Deferred() {
    log.Info("[main] config notice", "notice", d)
}                                                                  // main.go:353-354
```

★ **一条提示带着它属于哪个文件。** 内部存的是 `Notice{Path, Message}`
（`agentkit/spec/config.go:146-182`），`Deferred()` 把路径拼回消息 —— 启动日志那一行因此
**一个字都没有变**，而诊断接口能拿到结构化的路径。

产生提示的有两个入口：`Config.deferFile`（有文件）与 `Config.deferKey`（没有文件，
比如退役的分区、watch 的设置）（`agentkit/spec/config.go:211-225`）。

★ **路径是一个字段，不是消息的前缀。** 以前路径是拼进消息里的，于是调用点要自己把
文件名和扩展名拼起来 —— 而那个文件名**已经带着扩展名**了，所以提示写成了
`mcps/memory.yaml.yaml`。现在路径由调用点原样给出（`agentkit/spec/load.go:399`），
消息里不再有它。

今天全部的产生者就这些：

| 产生者 | 它说的是什么 | 属于哪个文件 |
|---|---|---|
| `agentkit/spec/load.go:43` | `api_key` 的 `${ENV}` 未设 —— 真正发请求时会 401 | `llm/<provider>.yaml` |
| `agentkit/spec/load.go:63` | `models[].summarize_at` 不再被读 —— 阈值归 agent 管 | `llm/<provider>.yaml` |
| `agentkit/spec/load.go:276` | 键 `corpus` 已改名为 `corpus_dir` —— 旧键仍然能加载 | `tools/kb.yaml` |
| `agentkit/spec/load.go:399` | MCP server 的 `env:` 里某个 `${ENV}` 未设 —— 这一条被略掉 | `mcps/<server>.yaml` |
| `agentkit/spec/load.go:440` | MCP server 的超时不是时长 —— 用缺省值 | `mcps/<server>.yaml` |
| `agentkit/spec/load.go:704` | `tools:` 点名了一个不存在的工具 | `agents/<id>.md` |
| `agentkit/spec/load.go:693` | 点了 `skill` 工具却没有 `skills` —— 技能一个也没有 | `agents/<id>.md` |
| `agentkit/spec/load.go:867` | 该模型没有声明 `context_window` —— 该 agent 的自动压缩不会发生 | `llm/<provider>.yaml` |
| `agentkit/spec/config.go:417` | 磁盘上还留着一个退役的分区（`policy/`）—— 它**没有被读**，而这条提示说明它的内容去了哪儿 | — |
| `agentkit/spec/reload.go:82` | 某个宽松时长解析不了，退回了缺省值 | — |

★ **最后这条消息对所有宽松时长都写 `watch.debounce`。** 写坏的长时被收进同一个全局列表
（`agentkit/spec/decl/decl.go:366-374`），又在同一处上报（`agentkit/spec/reload.go:80-84`），
所以 `tools/http.yaml` 的超时写错了，也会以 `watch.debounce` 的名义出现。
读它时要读成「有个宽松时长写错了」，而不是「debounce 写错了」。

`deferred` 不只是被打印：它们也出现在 `/v2/health` 与 `/v2/config` 里
（`api/v2_introspect.go:375`、`api/v2_introspect.go:450`）。

这比二元「严格 / 宽容」多一层：既不放过拼写错误，又给未来特性和运行期状态留了门。

**第三形态的分界**：它**不影响启动**，因为前提缺失是**运行期状态**（密钥尚未注入、
CI 里还没 export），不是配置写错。写错的东西（拼错的键、写错的 `@file:` 路径）
仍归第一形态硬报错（`agentkit/spec/expand.go:13-22`）。

### 4.4 环境变量：真正覆盖配置的只有 7 个

§2 的优先级链写的是「env > 配置根」，但这只对少数变量成立。openheron 今天真正读到的：

| 环境变量 | 覆盖什么 | 在哪里读 |
|---|---|---|
| `OPENHERON_CONFIG_DIR` | **配置根**（唯一的那一份） | `agentkit/spec/config.go:270`（名字是常量 `ConfigDirEnv`，`agentkit/spec/config.go:103`） |
| `LISTEN_ADDR` | `server.listen_addr` | `main.go:225` |
| `DATA_DIR` | 解析后的数据目录（会话、checkpoint） | `main.go:226` |
| `PROJECT_ROOT` | 交给 agent 的项目根 | `main.go:227` |
| `STATIC_DIR` | `server.static_dir` | `main.go:429` |
| `FRONTEND_MODE` | `server.frontend_mode` | `main.go:415` |
| `EINO_EXT_SKILLS_DIR` | skills 目录 | `agentkit/build/skill.go:185` |

server 侧那五个走同一个辅助函数 —— `override`（`main.go:554`）在环境变量非空时返回环境值，
否则返回配置值。

★ **server 侧那五个覆盖只在启动时读一次。** `server/` 不参与热重载（server 分类带 `hot: false`，
`agentkit/spec/config.go:51-55`），所以环境变量覆盖不是活旋钮 —— 改了要重启。
`EINO_EXT_SKILLS_DIR` 是例外：每次解析 skills 目录都会重新读它（`agentkit/build/skill.go:185`），
所以改动会在 agent 池重建时生效。

★ 另有两个环境变量，**不在上表里**，因为它们不碰配置：

- `OPENHERON_LOG_LEVEL`（`agentkit/obs/logger.go:11`，读于 `agentkit/obs/logger.go:71`）——
  日志级别；它**覆盖** `server/config.yaml` 的 `log_level`（见 §6.4）；
- `OPENHERON_LOG_LLM_REQUESTS`（`agentkit/obs/httplog/logging.go:25`，读于
  `agentkit/obs/httplog/logging.go:103`）—— 是否打印请求体。★ 请求体本身现在还要
  `log_level: debug` 才可见：这个变量只决定「读不读、脱不脱敏」。

★ 还有第三组确实从环境变量读配置，但**不在主路径上**：`MODEL_TYPE`、`MODEL_TIMEOUT`、
`MODEL_EXTRA_JSON`、`OPENAI_BASE_URL`、`OPENAI_API_KEY`、`OPENAI_MODEL`、`OPENAI_MODEL_ID`、
`ARK_BASE_URL`、`ARK_API_KEY`、`ARK_MODEL`、`ARK_MODEL_ID` —— 全部由 `build.ConfigFromEnv`
（`agentkit/build/config.go:15`）读取。上游设计把它叫 `llm.ConfigFromEnv`；在 openheron 里
模型装配住在 `agentkit/build`，`llm/` 只剩 `llm/common` —— 那个协议叶子包。

**`llm/*.yaml` 与 `agents/*.md` 里的字段没有环境变量覆盖。** `agentkit/build/config.go` 里的确有
一整套 `ConfigFromEnv`，但它**不在主路径上** —— 主路径是 `build.Resolve` + `build.FromConfig`
（`agentkit/build/provider.go:58` 与 `agentkit/build/provider.go:19`），只读文件，别的一律不读。
`ConfigFromEnv` 现在只是 `buildModel` 在「入口 agent 没声明模型」时的兜底
（`agentkit/build/snapshot.go:126`、`agentkit/build/snapshot.go:139`），而加载器对没声明模型的
agent 直接报错 —— "must declare model (no inheritance, no fallback)"
（`agentkit/spec/load.go:90`）—— 所以**一份合法的配置根本走不到那里**。

这是刻意的取舍：模型档案是**要被 review 和提交**的东西（它决定花谁的钱、存不存数据）。给它一个
进程外的高优先级通道，等于允许「repo 里的配置和线上跑的不一样」而两边都看不出来。留给需要它的
场景再说。

★ **注入密钥的正规做法是引用，不是覆盖。** `api_key` 只能写 `${ENV_VAR}` 或 `@file:/path`
（`agentkit/spec/write.go:577`），值在加载时由 `expandValue` 展开
（`agentkit/spec/expand.go:9`）；而没设的 `${ENV}` **不会**拦住启动 —— 它进 `deferred[]`
（"calls to this endpoint will fail with 401 Unauthorized"，`agentkit/spec/load.go:43`）。
那正是 §4.3 的第三形态：缺的是运行期状态，不是写错。

★ 树里再没有别的环境变量读取。剩下的命中都在测试文件里 —— `OPENHERON_KB_CORPUS`
（`kb/corpus_test.go:12`）、`OPENHERON_LOCK_HELPER` 与 `OPENHERON_LOCK_PATH`
（`persist/filelock_test.go:128`）。

### 4.5 诊断的回答归属到文件

★ **一条 issue 说的是哪个文件，就是哪个文件。** `Issue.Path` 是配置根相对的路径
（`agentkit/spec/diag.go:26`），为空只表示「这条规则不针对某一个文件」。

以前的诊断把**整棵树**的失败都记在被编辑的文件头上（`agentkit/spec/diag.go:149` 那一行
无条件写 `Path: rel`）。于是用户在编辑 `agents/main.md` 时，看到的是

```
agent agent-expert (agents/agent-expert.md) references model openai/gpt-4o-mini which is not declared under llm/
```

—— 一条**别的文件**的问题，挂在**当前**文件上，而当前文件里根本没有它能改的地方。

现在归属是这样定的：

| 问题的来源 | `Issue.Path` |
|---|---|
| 某个文件解析不出来（YAML 报错、缺必填键） | 那个文件（`FileError`，`agentkit/spec/fileerr.go:20`） |
| 某个 agent 的模型没在 `llm/` 里声明 | **那个 agent 的文件**，而且是 **hint** 不是 error —— 整棵树照常装得上，见 §4.6（`agentkit/spec/load.go:556`） |
| 某个 agent 的 `reasoning:` 档位没被声明 | 那个 agent 的文件（`agentkit/spec/load.go:620` `checkReasoning`） |
| 知识库的配置不对 | `tools/kb.yaml`（`agentkit/spec/load.go:884` `kbErr`） |
| 跨文件的规则（委派图成环、嵌套深度超限） | 空 —— 消息里点名了涉及的文件 |
| 一条提示（`deferred`） | 它属于的那个文件（§4.3 的 `Notice.Path`） |

★ **归属要能在界面上兑现。** 前端按 `Issue.Path` 把问题分成三处：本文件、整棵树、
别的文件（`web/src/lib/settings-validate.ts` `groupIssues`）—— 别的文件的问题**以那个
文件的名字显示**，而且是**一个链接**：点一下就跳到那个文件去改。

⚠ **但解析不了就是解析不了。** 别的文件**解析失败**时整棵树装不起来，这次保存也会被服务端
拒绝（写入门要试装一次）。所以 Save 仍然是禁用的 —— 只是理由现在写在**它该在的地方**。

★ 分界线是「装不装得上」：**解析失败**是装不上，属于这一段的 ⚠；**模型没声明**是装得上、
只是少了那个 agent，属于下一段。

### 4.6 「留在树外」：一个 agent 的模型没声明

★ **一个 agent 的模型没声明，不是整棵树的事。** 它只说明**那个 agent** 跑不起来：把它从树
里摘掉，剩下的配置照常装配、进程照常启动。理由不是宽容 —— 修它的入口（配置编辑器）就住在
这个进程里，进程起不来的话，连「哪儿错了」都没人告诉你。

```go
// agentkit/spec/load.go:602（validate 里，main 文件存在性检查之后）
c.leaveOutUndeclaredModels(ids)
ids = c.AgentNames()
```

落地是四件事，一件都不能少：

| # | 做什么 | 在哪 |
|---|---|---|
| 1 | 从 `Config.Agents` 里删掉 | `agentkit/spec/load.go:536` `leaveOutUndeclaredModels` |
| 2 | 从**每个**父 agent 的 `subagents:` 里删掉 —— 委派工具不能提供一个跑不起来的工 | 同上，`slices.DeleteFunc` |
| 3 | 记下**为什么**（`Config.leftOut`，`agentkit/spec/config.go:131`） | `agentkit/spec/config.go:139` `LeftOut()` |
| 4 | 出声：一条 `deferred`，路径是**它自己的文件** | `agentkit/spec/load.go:556` |

★ **不删父 agent 的 `subagents:` 就是留了个坑。** 委派工具按 `subagents:` 列表提供子 agent，
列表里留着一个装不上的名字，模型会去点它，然后拿到一个运行期错误 —— 那是一个本来可以在加载期
就说清楚的问题。

★ **`main` 也不例外。** 默认 agent 被留在树外时，`DefaultAgent()` 仍然返回 `main`
（`agentkit/spec/config.go:194`），**不顶替成别的 agent**：顶替会让会话悄悄跑在用户没点名的
agent 上，而且完全不提原来那个为什么不见了。代价是没有会话能跑 —— 但进程还在，编辑器还在，
这就够了。

#### 出声的四处

| 谁说 | 说什么 | 在哪 |
|---|---|---|
| 启动日志 | `[main] config notice` + 那条 `deferred` | `main.go:206-208` |
| `/v2/health`、`/v2/config` | 同一条 `deferred` | `api/v2_introspect.go:375`、`api/v2_introspect.go:450` |
| 配置编辑器 | 一条 **hint**，落在那个 agent 自己的文件上 | `agentkit/spec/diag.go:161` |
| 会话真的要用它时 | `agent "x" is left out of the tree: the model ... is not declared under llm/` | `agentkit/build/builder.go:168` `leftOutMessage` |

★ **一句话只写一处。** `leftOutMessage` 同时供装配层（`agentkit/build/builder.go:154`）与
默认 agent 的检查（`agentkit/build/spec.go:43`）使用 —— 两份拷贝会漂移，而漂移的那份是没人
读的那份。

★ **编辑器里是 hint，不是 error。** hint 说的是「这棵树少了一样东西」，文件本身没坏，保存
照常可以按；error 说的是「现在按不下去」。把降级说成 error，用户会以为自己的文件坏了。

★ **会话要跑那个 agent 时，它说的是那句话 —— 而不是「turn start timeout」。**
装配 agent 失败时（模型没声明、文件解析不了），那一轮**从未变得可流出**，于是超时的症状顶了
上来：用户看到「turn start timeout」，看不到是哪个文件、哪个模型。这是「降级必须出声」的最后一环：
降级的原因要一路走到用户眼前。

所以装配失败自己成为一个错误码（`agent_unavailable`，`transport/transport.go:43`），消息是装配器
自己的原话（`agentkit/run/errors.go:57` 的 `agentUnavailable`），而 run 层把这条错误交那一轮
（`agentkit/run/loop.go:108` 的 `makePrepareAgent`）—— 之前它**哪儿都没去**：框架记下了错误、
停了循环，可没有人告诉那一轮「你失败了」，于是等待方一直等到启动超时。

★ **那个超时的用法也变了。** 一轮什么时候会「迟迟不变为可流出」？装配一直不回来的时候。
一轮**装配失败**不再走那条路 —— 它有自己的错误码，而客户端的下一步也随之不同：
改一个文件，而不是重试。

#### 写入门：一次改动不许把能跑的 agent 带出树外

★ 降级一旦成立，写入门就**必须**补上牙：既然「模型没声明」不再报错，那么把 `main` 的
`model:` 改成一个不存在的模型，就会**静默**让 `main` 消失。这正是用户看不见的那种退化。

```go
// agentkit/spec/write.go:502 / :785 —— 写与删除各有一次试装，各有一道闸门
if err := s.checkNoAgentLost(trial, rel, nil); err != nil {
	return "", nil, err
}
```

批写那道是同一个判断，写在原地而不是包成一次调用 —— 它的错误要带上**整批**路径
（`agentkit/spec/write.go:598` `relsOf(files)`），而不是其中一个文件：
`if lost := s.agentsLostByChange(trial, nil); len(lost) > 0 { … }`。

规则是一条：**可以往树里加，可以放着已经坏的不动，不可以把一个本来能跑的 agent 拿出去。**
比较的是**两边都 settle 过**的树（`agentkit/spec/write.go:411` `agentsLostBy`）—— 一个本来
就已经在树外的 agent 在两边都不在，改动不碰它就不该算在改动头上。

★ **磁盘上的树装不上时，只许增不许减**（`agentkit/spec/write.go:343` `agentsLostByChange`
返回 nil）。此时没有任何东西「本来是能跑的」，而编辑器的草稿正是来替换那个坏文件的那个人 ——
拦下它，坏文件就永远修不好。

★ 诊断里也有一道，落在**被编辑的文件**上（`agentkit/spec/diag.go:96`）：用户应该在**打字的时候**
听到它，而不是在按下 Save 之后。它排在那个文件自身问题的**后面** —— 本文件的问题是用户此刻
能改的，掉出树是它的**后果**；先读后果的人无从下手。

#### 删除门：谁还指着它

★ 删除一个**别人还指着**的文件是一次降级，与写入门是同一条纪律：树里少了一个被点名的
server / provider / agent，于是它不再被加载，而那份引用变成了一个「指向不存在的东西」的指针。

★ **闸门跑在试装之前**（`agentkit/spec/write.go:773` `checkNotReferenced`）。试装的装载错误
正是要取代的症状：它点名被删掉的那个文件，把真正的原因藏在别处 ——
`mcps/fetch.yaml: agent task lists the MCP server "fetch", which is not declared under mcps/ (declared: deepwiki, time)`。
★ 用户要动手改的是**引用它的那一处**，不是那个被删的文件。

⇒ 拒绝的文案点名**谁引用了它**与**移除什么**：

```
mcps/fetch.yaml: it is still referenced: agents/task.md (mcps: [fetch]); remove that reference first, then delete this file
```

★ 路径是**一个字段**，不是消息的前缀 —— `WriteError.Rel` 由 API 拼成
`rel + ": " + message`（`api/v2_settings.go:65` `settingsFail`），消息自己再说一遍路径，
读者就要读两遍。

★ 被引用的分区只有三个，各有各的引用者（`agentkit/spec/write.go:639` `referencesTo`）：

| 被删的文件 | 谁引用它 |
|---|---|
| `mcps/<server>.yaml` | 一个 agent 的 `mcps:` 清单 |
| `llm/<provider>.yaml` | 一个 agent 的 `model:` |
| `agents/<id>.md` | 另一个 agent 的 `subagents:` 清单 |

★ **有意删掉的 agent 不算「掉出树」**（`agentsLostBy` 的第三个参数 `removed`，
`agentkit/spec/write.go:411`）：写入门问的是「本来能跑的 agent 是不是不再能跑了」，
而用户**主动**删掉一个 agent 是另一件事 —— 不然的话任何 agent 文件都删不掉。

★ **默认 agent 单独一条拒绝**：`the default agent cannot be deleted: a tree needs one`
—— 树总得有一个默认 agent，而试装的装载错误说的是「agents/main.md does not exist」，
那读起来像一个坏文件，而不是一次被拒绝的删除。

★ **磁盘上的树装不上时，删除闸门退化为试装的错误** —— 与「只许增不许减」同一纪律：
不知道谁引用了什么的时候，不能凭猜测拒绝。

★ **装载期那个错误落在引用它的那个文件上**（`agentkit/spec/load.go:757` `validateMCP`
返回 `&FileError{Path: agentFile(a, id)}`，路径由 `agentkit/spec/load.go:772` 给出）：
诊断落在「整棵树」上时，用户点不进去。

## 5. 配置根：三级，没有探测

配置得先知道项目根才能读任何东西，而项目根本身又是配置项 —— 鸡生蛋问题。openheron 的解法是
**不探测**：配置根要么被点名，要么从一个名字推导出来。

```
① resolve the config root (nothing can be read yet)
   --config-dir                main.go:84
     ↓ unset
   $OPENHERON_CONFIG_DIR       agentkit/spec/config.go:253
     ↓ unset
   <project-root>/.openheron   agentkit/spec/config.go:256, created when missing
```

**上游设计怎么做，以及 openheron 为什么不这么做。** 上游设计从工作目录**向上**找第一个含
`.heron/` 的祖先目录 —— `cd cmd/server && heron` 时 cwd 在子目录，但用户的心智模型是「这是那个
项目」。openheron **刻意丢掉了上溯**：配置根一旦取决于「进程是从哪儿启动的」，同一条命令换个
目录跑就读到另一份配置，而测试实例最终会读到开发者的配置。这条规则写在 `NewService` 的文档注释
里（`agentkit/spec/config.go:242-254`），也写在仓库的 `AGENTS.md` 里。

★ 差别在代码的形状上就看得见：`NewService`（`agentkit/spec/config.go:247`）是**三个**分支、
**没有循环** —— `root` → `os.Getenv(ConfigDirEnv)`（`agentkit/spec/config.go:270`）→
`filepath.Join(projectRoot, DefaultBaseDirName)`（`agentkit/spec/config.go:273`）。整个文件里
没有任何一处沿着祖先目录往上走。

**为何环境变量压过推导出来的默认值。** `$OPENHERON_CONFIG_DIR` 是显式指定，语义上应压过一切
推导。★ 而 `--config-dir` 又压过环境变量（`main.go:84`）—— 命令行是最直接的一次性指定。

★ **只有默认出来的那个根才会被创建。** `NewService` 只在自己推导出根的时候创建目录
（`agentkit/spec/config.go:281-288`）；显式的 `--config-dir`、或者指向空处的
`$OPENHERON_CONFIG_DIR`，都是笔误，而笔误不能悄悄长出一棵空配置树。`Service.RootIsDefaulted`
（`agentkit/spec/config.go:310`）就是为了把两者分开 —— `main.go:136-142` 给出的是
"no configuration at <root>: run `openheron init` to create one"，而不是
"agents/main.md does not exist"。

★ **还有第二个入口，给 `init` 用。** `openheron init` 走 `spec.NewServiceAt`
（`agentkit/spec/config.go:284`），不走 `NewService`：init 往它被指到的目录里写，从不读
`$OPENHERON_CONFIG_DIR`（`initcmd.go:61-65`）。否则 init 会把新配置写到用户没指的地方去。

★ **家目录也不是兜底。** 如果 `~/.openheron/` 存在却不是正在用的配置根，进程会在启动时说一声
（`main.go:110-127`）：配置根从不被搜出来，所以那个目录必须被点名。

### 5.1 两个「根」：`Root` 与 `ProjectRoot`

配置根与项目根是**两个不同的目录**，代码把它们分得很清：

| 方法 | 值 | 用途 |
|---|---|---|
| `Service.Root()`（`agentkit/spec/config.go:310`） | 配置根本身 —— 默认是 `<project-root>/.openheron` | 读配置；`state/` 住在它下面 |
| `Service.ProjectRoot()`（`agentkit/spec/config.go:320`） | 项目根 —— `--project-root`，否则是工作目录（`agentkit/spec/config.go:256-266`） | agent 的工作目录；相对路径的解析基准 |
| `Service.DataDir(v)`（`agentkit/spec/config.go:314`） | `v` 是绝对路径就原样返回，否则落在**配置根**下（`agentkit/spec/config.go:320`）；不存在则创建 | 会话与 checkpoint |
| `Service.ResolveDir(v)`（`agentkit/spec/config.go:328`） | `v` 是绝对路径就原样返回，否则落在**项目根**下（`agentkit/spec/config.go:341`） | `static_dir`、知识库语料 |

★ **两者是解耦的。** `--config-dir /etc/openheron-cfg` 把配置放到别处，而 `--project-root`
（默认：工作目录）仍然是 agent 干活的地方。不解耦的话，「配置根的父目录」就会变成 `/etc` ——
agent 去那儿 grep/glob。

★ **默认的项目根是工作目录，不是配置根的父目录。** 上游设计是从配置根的父目录推出来的；
openheron 改从 `--project-root` 或 cwd 取（`main.go:87-88`、`agentkit/spec/config.go:256-266`）。
配置根取默认值时两者恰好重合（`<cwd>/.openheron` 的父目录就是 `<cwd>`），但配置根一旦被点名，
两者就不再重合。

用错根会得到两种典型故障：前端去配置根下的 `web/dist` 找资源 → **403/404**；agent 被告知
「项目根 = 配置根」→ 在错误目录里 grep/glob。

### 相对路径的解析基准

**相对路径并不是都相对同一个基准。** openheron 有两个：

| 路径 | 基准 | 代码 |
|---|---|---|
| `server.data_dir`（相对） | **配置根** | `agentkit/spec/config.go:327-329` |
| `server.static_dir`（相对） | **项目根** | `agentkit/spec/config.go:328-334`，用在 `main.go:429` |
| `kb.corpus_dir`（相对） | **项目根** | `kbcmd.go:148` |

★ **这个划分是刻意的：状态跟着配置走，工作树跟着项目走。** 相对的 `data_dir` 在配置被搬走时
跟着搬（`--config-dir /etc/openheron-cfg` 会把会话放在它旁边）；相对的 `static_dir` 和相对的
知识库语料属于被服务的那个项目，配置搬走时它们不该跟着搬。

★ **`PROJECT_ROOT` 生效得比配置根晚。** `setupConfig` 先解析出 service（`main.go:184`），之后
才用环境变量覆盖运行期的项目根（`main.go:227`）。于是 `PROJECT_ROOT` 环境变量挪的是 agent 的
工作目录（`main.go:239`、`main.go:245`），**挪不动**默认出来的配置根 —— 那仍然落在工作目录的
`.openheron/` 下。要一起挪，传 `--project-root`。

★ 而且这种情况下两个根确实不是一回事：`static_dir` 相对的是命令行给出的项目根
（`main.go:429`），agent 的工作目录用的却是环境变量覆盖后的那个（`main.go:239`）。设了
`PROJECT_ROOT`，前端的资源目录和 agent 的工作目录就不再共享同一个基准。

### 5.2 测试实例靠「点名的配置根」隔离

openheron 的测试环境集中定义在一处：仓库根的 `.env.test`（已提交），加上 Taskfile.yml 的目标。

| | 开发 | 测试 |
|---|---|---|
| openheron 本体 | :8080 | **:18099** |
| Vite | :5173 | **:15173** |
| E2E 假后端 | — | **:18180** |

（`.env.test:25-38`；同一张表也见 `docs/developer-manual.md` §5.1。）

测试实例由 `scripts/test-backend.sh` 启动，挂在三个 Taskfile.yml 目标后面：

| 命令 | 做什么 |
|---|---|
| `task test-env-backend` | 前台启动测试实例（`Taskfile.yml:529-533`） |
| `task test-env-reset` | 只重建测试目录，不启动（`Taskfile.yml:535-538`） |
| `task test-env-sync` | 把开发侧的定义重新复制进测试环境（`Taskfile.yml:540-543`） |

脚本点名了必须隔离的三个环境变量，并把工作目录挪过去（`scripts/test-backend.sh:37-45`）：

| | 值 | 效果 |
|---|---|---|
| `OPENHERON_CONFIG_DIR` | `$OPENHERON_TEST_ROOT/project/.openheron` | **配置根**（唯一的那一份） |
| `DATA_DIR` | `$OPENHERON_TEST_ROOT/state` | 会话数据 |
| `LISTEN_ADDR` | `:18099` | 监听地址 |
| （cwd） | `cd $OPENHERON_TEST_ROOT/project` | 项目根就是工作目录 |

（`scripts/test-backend.sh:117-119` 与 `scripts/test-backend.sh:137`。）

★ **端口与状态必须一起隔离。** 换个端口不等于数据隔离：openheron 把会话写在配置根下的
`state/`，测试写在那里就污染了真实历史（`AGENTS.md` 的 "Rules"）。测试实例是**点名**自己的
配置根的，而配置根**从不上溯**（`agentkit/spec/config.go:250`），所以仓库根那个 `.openheron/`
**结构上就够不着** —— 隔离靠的不是「一次恰好停在那里的探测」，而是一个被点名的根
（`scripts/test-backend.sh:38-42`）。

★ **配置根是预置出来的，不是探测出来的。** 脚本会创建 `$PROJECT_DIR/.openheron/` 与
`$PROJECT_DIR/.openheron/server/`（`scripts/test-backend.sh:47`），并写一份默认的
`server/config.yaml`（`scripts/test-backend.sh:79-105`）。没有它们，实例就会对着一棵空配置树
启动，第一个报错是 "agents/main.md does not exist or could not be parsed" —— 读起来像文件丢了，
而不像环境配错了。

★ **`agents/ llm/ skills/ tools/` 是复制，不是软链。** 它们是**输入**（agent 定义、
厂商协议、工具配置），测试实例要用真 agent 才跑得通一整轮；复制而非软链，是为了测试跑崩
时不会写坏开发配置（`scripts/test-backend.sh:49-73`）。`server/` 与 `state/` 刻意**不在**复制
列表里 —— 那两个由脚本自己管。

★ **`tools/` 在复制列表里是故意的。** 它是一个配置目录（每个外部工具一个文件，外加知识库），
漏掉它意味着 HTTP/搜索工具在测试实例里永远开着 —— 或永远关着 —— 那跟开发侧不是一回事
（`scripts/test-backend.sh:55-59`）。

★ **`task test-env-sync` 是必须的，不是可选的。** 改了开发侧的 agent 定义，不重新复制定义，
测试实例跑的还是旧的（`AGENTS.md` 的 "Rules"；`docs/developer-manual.md` §5.2）。

★ **端口表只在一个文件里。** `.env.test` 装着测试环境的全部约定；Vite、Playwright 和各个
冒烟脚本都从它读，改一个端口只需动一个文件，而不是五个（`AGENTS.md` 的 "Ports: testing has
its own environment"）。隔离的其余细节见 `docs/frontend-design.md` §11.2（上游）。

## 6. 文件规格

### 6.1 `llm/<provider>.yaml`

```yaml
# .openheron/llm/openai.yaml
type: openai_compat                # free-form label, for display only
base_url: https://api.openai.com/v1
api_key: ${OPENAI_API_KEY}         # ${ENV} or @file:/path
timeout: 5m
extra:                             # provider-level defaults, they go into ExtraFields
  reasoning_effort: low

models:                            # ★ multiple models: several models under one provider
  - id: gpt-4o
    context_window: 128000
    timeout: 10m                   # overrides the provider level (reasoning models are an order of magnitude slower)
    extra:                         # overrides the provider-level extra
      temperature: 0.2
  - id: o3
    context_window: 200000
    reasoning:                     # tier vocabulary (see below)
      off:  {}                     # send no thinking field at all
      low:  { reasoning_effort: low }
      high: { reasoning_effort: high }
    reasoning_default: low         # used when the agent writes no reasoning
    reasoning_field: reasoning             # the field this endpoint carries the chain of thought in
    reasoning_field_from: reasoning_content # the source field name when replaying history (defaults to the same)
```

#### 字段全集

**Provider**（连接属性，同一 provider 下所有模型共享）

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `type` | `string` | ✅ | **只用于显示** —— 代码里没有按它分支的地方。它不能为空（`agentkit/spec/load.go:29-31`），但取值自由：`openai_compat` 是出厂模板写的（`agentkit/templates/files/llm/openai.yaml`） |
| `base_url` | `string` | ✅ | 主机（可带前缀）。**不要写 `/chat/completions`** |
| `api_key` | `string` | | `${ENV}` / `@file:`；空 = 无鉴权（自建网关、本地 ollama）。**`${ENV}` 未设不阻塞启动**，只记提醒，请求时返回 401 |
| `timeout` | `Duration` | | 缺省 `5m`（`agentkit/build/provider.go:31-34`）。**不能依赖 SDK 隐式默认** —— 端点愿意想多久是它说了算 |
| `header` | `map[string]string` | | ★ **解析了，然后没人读** —— 见下 |
| `extra` | `map[string]any` | | 厂商私有请求体，**顶层键覆盖** |
| `hint_keys` | `[]string` | | 该端点使用的**字段名**，供 400 时给准确提示。协议层不内置任何一家的字段名（`llm/common/error.go:177-179`） |
| `models` | `[]Model` | | 它声明的模型；**缺省 = 该 provider 不可用**（引用会解析失败） |

★ **`header:` 是个坑：它解析得过去，然后什么也不会发生。** schema 声明了它
（`agentkit/spec/decl/decl.go:26`），加载器也接受它 —— 严格模式下未知键直接报错
（`agentkit/spec/config.go:553-567`）—— 但从此以后没有任何代码路径读它：
`build.FromConfig` 组装 `common.Config` 时根本没带 header
（`agentkit/build/provider.go:36-43`），而 `common.Config` 里压根没有 header 这个字段
（`llm/common/model.go:28-44`）。本来能承载它的 option 是有的
（`llm/common/option.go:24-26`，`llm/common/request.go:46-48`），但从来没人去填
`RequestOptions.Header`。⇒ **别指望写了它会生效。**

**Model**（模型的固有属性）

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | `string` | 必填（`agentkit/spec/load.go:58-60`）。传给端点的模型标识 |
| `kind` | `string` | `chat`（缺省）或 `embedding`（`agentkit/spec/decl/decl.go:575-579`）。embedding 模型不能当聊天模型建（`agentkit/build/provider.go:26-29`），知识库也只认 embedding 模型（`agentkit/spec/load.go:694-698`） |
| `dimensions` | `int` | embedding 的维度；`0` = 未知，建索引时学出来（`agentkit/spec/load.go:368-371`） |
| `context_window` | `int` | **0 = 未知**（不是「无限制」）。它是压缩阈值的分母 —— 见下 |
| `summarize_at` | `Threshold` | ★ **已废弃**：仍然解析，好让旧配置继续加载，但没有任何代码读它。阈值现在归 agent（`agentkit/spec/decl/decl.go:46-53`），加载器会明确说出来（`agentkit/spec/load.go:61-65`） |
| `timeout` | `Duration` | 覆盖 provider 级。**要允许** —— provider 级只能容纳最慢的那个，那会让快模型失去超时保护 |
| `extra` | `map[string]any` | 覆盖 provider 级同名键 |
| `reasoning` | `map[string]map[string]any` | **档位词汇表**：档位名 → 该端点把它发成什么字段。为空 = 该模型不思考 |
| `reasoning_default` | `string` | 建议档位；agent 没写 `reasoning` 时用它（经 `RequestOptionsFor` 生效） |
| `reasoning_field` | `string` | 端点收发思维链的字段名；空 = 用底层库原生名（`reasoning_content`） |
| `reasoning_field_from` | `string` | 回放历史时的**源**字段名；默认取 `reasoning_field`（`agentkit/build/provider.go:146-155`） |

★ **`context_window` 今天是有用的。** 上游设计把它叫「纯元数据，因为没有 summarization」——
openheron **有** summarization。窗口就是 agent 的 `summarize_at` 百分比所对着的那个分母
（`agentkit/spec/summarize.go:25-39`），而压缩中间件只有在解析出来的阈值大于零时才建
（`agentkit/build/summarize.go:36-40`）。所以没声明窗口的模型**没有**阈值：零的百分之多少
还是零，自动压缩永远不触发 —— openheron 会把这件事说出来，而不是安静地什么都不做
（`agentkit/spec/load.go:345-364`）。

#### 多模型：一个 provider 声明多个模型

一个 `llm/<provider>.yaml` 持有该厂商的 `base_url` / `api_key` / `timeout`，以及
`models[]` 列表。引用形式是 **`<provider-id>/<model-id>`**：

```yaml
# agents/main.md
model: openai/gpt-4o        # hangs the agent on a model

# agents/researcher.md
model: openai/gpt-4o-mini   # the cheap retrieval tier
```

解析成 `Ref{ProviderID, ModelID}`：`ParseRef` 取**第一个** `/`，两侧都不得为空 ——
没有 `/` 的引用直接报错（`agentkit/spec/load.go:499-508`）。归一化后作为 `Config.Models`
的键（`agentkit/spec/load.go:67`），而 provider id 就是**去掉扩展名的文件名**
（`agentkit/spec/load.go:18`）。

#### 请求级 `model` 覆盖

**同一个实例内**，模型名靠请求级 option 切换，而不是每个模型各建一个实例：

```
acl/chat_model.go:560  options := model.GetCommonOptions(&model.Options{
                              Model: &c.config.Model,   ← the construction-time value serves only as the "default"
                              …}, opts...)
acl/chat_model.go:588  req := &openai.ChatCompletionRequest{
                              Model: *options.Model,     ← ★ this is what really enters the request body
```

（`eino-ext/libs/acl/openai@v0.1.17/chat_model.go:560` 与 `:588-589`。）

`GetCommonOptions` 会用调用方的 `opts` 覆盖构造期默认值，而 `model.WithModel(name)` 正是
这样一个 opt（`eino@v0.9.22/components/model/option.go:211-224`）。openheron 走这条通道两次：
`RequestOptionsFor` 把模型自己的 id 放进 `RequestOptions.Model`
（`agentkit/build/provider.go:126-128`），`RequestOptions.Options()` 再把它变成
`model.WithModel`（`llm/common/request.go:31-33`）。

★ **实例粒度是「每个 agent 一个」，不是「一个 provider 一个」。** 请求级 option
能「不按模型建实例」，不代表实际就只有一个实例：粒度由下面这条链决定，而链头是
agent 自己。

```
agent (each declares model: on its own)
  ↓
(provider, model, reasoning tier, reasoning_field, timeout)
  ↓ FromConfig: the model name and the timeout are fixed at construction time
(common.Config{HTTPClient: &http.Client{…}})
  ↓ AttachRequestOptions: tier → the field shape of the endpoint; the RequestOptions are baked into the optionedModel
two agents each hold a ChatModel and a connection pool
```

`resolveModel` 每个 agent 节点调一次（`agentkit/build/snapshot.go:118-137`），走的是
`BuildModel`（`agentkit/build/provider.go:81-95`），每次都新建一个客户端。所以即使
同一个 provider、同一个模型，两个 agent 也可能有两个实例 —— 因为 reasoning 档位、
`reasoning_field` 不同，或模型级 `timeout` 不同：

```
deepseek/deepseek-reasoner  used by main        → ChatModel A + http.Client A
deepseek/deepseek-chat      used by researcher  → ChatModel B + http.Client B
                                              ↑ the same provider, two instances
```

★ **这不是待收敛项，是设计。** agent 之间的模型选择**互不相干**
（`agents/*.md` 各写各的 `model:`），它们本来就可能指向不同厂商；而即便指向
同一家，思考档位与端点字段形状仍由各自模型档案决定。共享实例要成立，得先证明
这几项**全部**相等 —— 那就得上一个以它们为键的缓存，也就是第二份真相。

⇒ **实例粒度 = agent。** 真正多花的那点开销（本地单用户工具里 N 个客户端）换掉的是
一个隐藏耦合。

> acl 有**两条** option 通道，容易混淆：
> - **通用通道**（`GetCommonOptions`）：`model.WithModel` / `WithTemperature` / `WithMaxTokens` / `WithTopP` / `WithStop`
> - **厂商通道**（`GetImplSpecificOptions`，结构体 `openaiOptions` 有 **8** 个字段）：`ExtraFields` / `ReasoningEffort` / `ExtraHeader` / `RequestBodyModifier` / `RequestPayloadModifier` / `ResponseMessageModifier` / `ResponseChunkMessageModifier` / `MaxCompletionTokens`
>
> （`eino-ext/libs/acl/openai@v0.1.17/option.go:48-57`。）
>
> ★ **厂商通道里没有 Model** —— 方言与改名只能走它，但切模型必须走通用通道。

#### 端点方言与字段改名

每个 agent 持有**自己的** `*http.Client` —— `build.FromConfig` 每个实例造一个
（`agentkit/build/provider.go:36-49`），装配层给它套上日志 transport
（`agentkit/obs/httplog/logging.go:33`），`common.New` 拿到的已经是包好的实例。
★ 所以协议层里没有一行日志代码 —— 包装是观测，不是协议。所以：

- **端点私有参数**（思考开关等）→ `WithExtraFields`，且必须在**请求级**下发：请求级 extra 是
  **替换**语义（`go-openai@v0.1.6/chat.go:420-426` —— 原文是 "SetExtraFields will
  override any existing fields with the same key"），调用方须把
  `provider.extra ← model.extra ← reasoning[level]` 三层合完的整一份递下来。
- **跨厂商字段改名**（思维链 `reasoning_content` ↔ `reasoning`）→ `WithRequestPayloadModifier`，
  唯一可行路径（`llm/common/option.go:13-18`，`llm/common/request.go:100-133`）。

#### 启动校验：引用解析不了，就把那个 agent 留在树外

```go
// agentkit/spec/load.go:536
func (c *Config) leaveOutUndeclaredModels(ids []string) {
	for _, id := range ids {
		a := c.Agents[id]
		ref, _ := ParseRef(a.ModelRef)
		if _, ok := c.Models[ref.String()]; ok {
			continue
		}
		c.deferFile(where, "agent %s (%s) references model %s which is not declared under %s/; the agent is left out", ...)
		c.leftOut[id] = reason
		delete(c.Agents, id)
	}
}
```

★ **这里曾经是一句硬报错**（`return &FileError{...}`），代价是整棵树跟着一个 agent 一起
装不上：进程退出 1，配置编辑器也就跟着没了 —— 而编辑器正是唯一能改好这个文件的地方。
现在只剩「留在树外」这一个后果，完整规则见 **§4.6**。

**为何不静默兜底**：子 agent 静默继承主模型是**成本事故**。这条纪律没有松 —— 松的是**收场
方式**：拼错的模型名仍然必须让人知道（启动日志、`/v2/health`、编辑器里的一条 hint、会话
真的要用它时的那句错），只是不再用「进程起不来」这种方式说。**没有兜底，只有降级，而且降级
出声。**

#### 思考档位：能力 / 词汇表 / 意图 三层分离

| 层 | 是什么 | 落在哪 |
|---|---|---|
| **能力** | 这个模型**能不能**思考 | `models[].reasoning` **存不存在** |
| **词汇表** | 这个端点把每个档位**发成什么字段** | `models[].reasoning` 的**键值对** |
| **默认** | agent 没选时用哪一档 | `models[].reasoning_default` |
| **意图** | 这次调用**用哪一档** | `agents/*.md` 的 `reasoning` |

★ **两侧字段同名 `reasoning`，但一个是词汇表、一个是选择**：provider 声明
「有哪些档位、各是什么形状」，agent 只写「用哪一档」。名字同族是有意的 ——
读配置时它们是同一件事的两面。

```yaml
# llm/<provider>.yaml —— the same high, a different shape at every endpoint
reasoning:                 # vocabulary: which tiers this endpoint has
  off:  {}
  low:  { reasoning_effort: low }
  high: { reasoning_effort: high }
reasoning_default: low     # the tier to use when the agent writes no reasoning
```
```yaml
# agents/researcher.md
reasoning: high          # ★ only the tier name — it does not need to know what shape "high" has at this vendor
```

**档位的解析顺序**：`agent.reasoning` → 空则 `model.reasoning_default` → 再空则不注入
（`effectiveTier`，`agentkit/build/provider.go:97-103`）。

**合并链只有一条**：`provider.extra ← model.extra ← reasoning[level]`，
顶层键覆盖（`agentkit/build/provider.go:105-113`）。三层合完的**完整一份**必须整份递下去 ——
`WithExtraFields` 是**替换**不是合并（`go-openai@v0.1.6/chat.go:420-426`），只传其中一层会把
其余的丢掉。

★ **档位不在词汇表里不报错，但在 openheron 里它也完全不出声。**
`RequestOptionsFor` 干脆跳过它不认识的档位（`agentkit/build/provider.go:110`：
`if lvl, ok := m.LevelFields(level); ok`）；`!ok` 那条分支什么都不注入，也什么都不报告。
**没有**对应的 `deferred` 条目。上游设计想在这里留一条提醒，出厂模板把话说得很直白
（`agentkit/templates/files/llm/deepseek.yaml`）：「端点没有声明的档位是**静默忽略**的」。
⇒ **这张词汇表不是可选的**：agent 写着 `reasoning: high` 而模型根本没有 `high`，
意思就是「完全不思考」，没有错误也没有消息。正是这张表让 `high` / `low` 真的生效。

#### `extra` 的两层合并

```yaml
extra:                    # provider level: the default for every model
  reasoning_effort: low
models:
  - id: gpt-4o
    extra:                # model level: overrides the key of the same name
      temperature: 0.2
```

`shallowMerge` 按**顶层键覆盖**（`agentkit/build/provider.go:132-144`）—— 不做深合并 ——
深合并的语义在「改 `thinking.type` 而保留 `thinking.other`」这件事上不可预测。

**★ `extra` 现在对所有 provider 一视同仁**，不再有「ark 不许用 extra」的分支：
openheron 的所有端点都走同一个底座（`eino-ext/libs/acl/openai`），一张按厂商分支的
能力表只会在它过期时拒掉能用的配置。

★ **agent 自己的 `temperature` / `max_tokens` 赢过同名的 `extra` 键** —— 下发之前那两层
会从合完的 map 里被删掉（`agentkit/build/provider.go:114-119`），否则两者都会往请求体里写
`temperature`，而合并补丁会让 `extra` 那个赢。

★ **但这条规则有一个洞，值得知道。** 请求级 option 只有在合完的 map **非空**时才会挂上
（`llm/common/request.go:49-51`）。而 provider 级的 `extra` **同时**被当作构造期默认值交给了
acl 客户端（`agentkit/build/provider.go:42` → `llm/common/model.go:71` →
`eino-ext/libs/acl/openai@v0.1.17/chat_model.go:571`）。所以如果 provider 的 `extra`
**恰好只有** `temperature`（或恰好只有 `max_tokens`）而没有别的键，而 agent 又设了同一个键，
合完的 map 就成了空的，请求级 option 没挂上，构造期那份 `extra` 活了下来 —— 而它是作为
JSON 合并补丁盖在请求体上的（`go-openai@v0.1.6/internal/marshaller.go:38`），**覆盖**掉
agent 的值。对着本地端点实测：provider `extra: {temperature: 0.9}` 加上 agent 的
`temperature: 0.1`，进请求体的是 `"temperature": 0.9`。给 provider 的 `extra` 再加一个键，
请求级 option 就挂上了，合完的 map 顶掉构造期那份，请求体里就是 `0.1`。
⇒ **别把 `temperature` 或 `max_tokens` 单独放进 provider 级的 `extra`。**

#### 取值展开

```go
func expandValue(v string) (value string, resolved bool, err error) {
	v = strings.TrimSpace(v)

	switch {
	case strings.HasPrefix(v, "@file:"):
		p := strings.TrimSpace(strings.TrimPrefix(v, "@file:"))
		if p == "" {
			return "", false, fmt.Errorf("@file: missing path after the prefix")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return "", false, fmt.Errorf("read secret file: %w", err)
		}
		return strings.TrimSpace(string(b)), true, nil

	case strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}"):
		name := strings.TrimSpace(v[2 : len(v)-1])
		if name == "" {
			return "", false, fmt.Errorf("${} is missing a variable name")
		}
		val, ok := os.LookupEnv(name)
		if !ok {
			return "", false, nil
		}
		return strings.TrimSpace(val), true, nil

	default:
		return v, true, nil
	}
}
```

（`agentkit/spec/expand.go:9-38`。）

**★ 缺 `${ENV}` 不致命**：环境变量未设置时返回空值 + `resolved=false`，**不阻塞启动**，
只在启动日志里记一条提醒（`agentkit/spec/load.go:40-47`）。真正的失败点移到**调用该端点**
那一刻 —— 空 `api_key` 会让端点明确回 401，协议层归成 `auth` 错误并提示「检查 api_key
是否设置、是否过期、是否有该模型的权限」（`llm/common/error.go:18`，`:72-73`，`:159-175`）。

为什么改掉早期的 fail-fast：密钥常在运行时才注入（容器 / CI / 本地 shell），
而「声明了 provider 却暂时没密钥」不该让**整个进程**起不来 —— 尤其是模板里
留着待用、当前没被任何 agent 引用的 provider。把失败点从启动移到请求，既不
误伤未用到的 provider，又保留了「迟早要修」的可见性。错误也仍在请求内可归因，
不是几百行外的空指针。

> `@file:` 读不到文件**仍是硬错误** —— 那是路径写错，不是「暂时没注入」。
> 两者的分界：env 是运行期状态，文件路径是配置本身。

#### 时长

`timeout` 等一律走 `time.ParseDuration`（`5m` / `30s`），**不用纳秒整数**。空串 = 不设置（由 provider
工厂决定默认），不是「超时 0」。

> **坑**：`timeout: 60`（缺单位）必须**报错**而非当作 60 秒。自定义 `Duration` 类型强制要求字符串
> 形式（`agentkit/spec/decl/decl.go:612-628`）：标签不是字符串就报错 —— "duration must be a
> string with a unit (e.g. 60s / 5m); bare numbers are not allowed"。

★ **但不是每个时长都严格。** `server/config.yaml` 与 `tools/` 下的文件用的是宽松变体
（`agentkit/spec/decl/decl.go:537-550`）：解析不过的值会被记下来并换成默认值，而不是让整次
加载失败。宽松变体覆盖 `watch.debounce`、`start_timeout`、`lease_ttl` 以及 `tools/` 下的
超时（`agentkit/spec/decl/decl.go:534`，`:275`，`:282`，`:306`，`:312`，`:319`）。

★ **而宽松回退的值到处都一样：`200ms`** —— 也就是 debounce 的默认值
（`agentkit/spec/decl/decl.go:14`，`:325-334`）。所以 `start_timeout: 60`（裸数字）既不会变成
60 秒，也不会回退到 60 秒的默认值：它会变成 **200 毫秒**，而且 deferred 列表里那条提醒
不管实际是哪个键写坏了，永远写的是 `watch.debounce`（`agentkit/spec/reload.go:80-84`）。
一个落到 200ms 的 `lease_ttl` 几乎立刻就把租约耗尽了。⇒ **把单位写上。**

#### YAML 方言风险（已核实）

| 项 | 结论 |
|---|---|
| `yes`/`no`/`on`/`off` | **作为字符串字段的值没有风险** —— yaml.v3 走 YAML 1.2 core schema，解析器认的布尔只有 `true/True/TRUE/false/False/FALSE`（`yaml.v3@v3.0.1/resolve.go:52-53`），其余落成字符串。★ **但当目标字段是 `bool` 时，yaml.v3 为了兼容还会额外接受 YAML 1.1 的写法**（`yaml.v3@v3.0.1/decode.go:683-693`）—— `watch.enabled: off` 的意思是 `false`，而且在那里没法写出字符串 "off" |
| `listen_addr: :8080` | **不需要加引号** —— yaml.v3 把它解析成纯标量 `":8080"`。分隔符是「冒号 + 空格」，`: 8080`（带空格）才是解析错误 |
| `base_url: https://...` | 无需引号 —— 分隔符是「冒号 + 空格」，`://` 安全 |
| 缩进 | 唯一实质代价，靠严格模式兜底：缩进错会报行号而非静默误读。加载器整体是严格的 —— `KnownFields(true)`（`agentkit/spec/config.go:553-567`），所以拼错的键是错误而不是沉默：旧的 `thinking:` 与 `effort:` 键正是这样被拒掉的（`agentkit/spec/config_test.go:864`） |

#### `extra` 与 provider 能力矩阵（★ 关键坑，已实施后修订）

openheron 走**单底座** —— 所有端点都走 `eino-ext/libs/acl/openai` —— 所以那张按 eino-ext
各包分别核实的矩阵**已不适用于当前架构**。这里保留它，作为「若将来回到多包路线」的对照；
下面标的是模块缓存里的版本，openheron **一个都不依赖**：

| eino-ext 包 | `ExtraFields`（构造） | `WithExtraFields`（请求级） |
|---|---|---|
| `openai`（`v0.1.13`） | ✅ `chatmodel.go:180` | ✅ `option.go:37` |
| `deepseek`（`v0.1.7`） | ❌ | ✅ `option.go:51` |
| `qwen`（`v0.1.9`） | ❌ | ✅ `option.go:45` |
| `openrouter`（`v0.1.10`） | ✅ `chatmodel.go:141` | ❌ |
| `ark`（`v0.1.71`） | ❌ | ❌ |

**⇒ 关键教训**：各包的扩展点**三种组合**都有（都有、只有请求级、都没有）。做成按厂商分支的
代码时，**「哪一档」这件事必须在配置里声明**，否则同一个 `extra` 在有的端点上生效、有的静默
失效。

单底座路线下这个问题消失：`extra` 统一走 `common.WithExtraFields`（`llm/common/option.go:20-22`）
在请求级下发，而 `go-openai@v0.1.6/chat.go:420-426` 的语义是**按键覆盖请求体** —— 不认识就
带着发，端点自会处理或报错。这比在 openheron 侧维护一张能力表更省事。

★ **openheron 没有的东西**：任何按厂商分支的逻辑。没有 ark 包，没有 `Region` /
`AccessKey` / `SecretKey` / `RetryTimes` —— 那四个字段属于 `components/model/ark`，
openheron 本来一个都没用。ark 当年默认开着的思考开关，现在就是一条普通的 `extra` 键；
而环境变量 `MODEL_EXTRA_JSON` 只喂**纯环境变量那条兜底路径**
（`agentkit/build/config.go:15-44`），跟 `llm/` 配置无关。

### 6.2 `agents/<id>.md`

```markdown
---
# .openheron/agents/main.md
description: Management role              # goes into the task tool description, it decides whether the model picks it
model: openai/gpt-4o              # a missing model is an error — no inheritance, no fallback
max_iterations: 20                # 0 = not declared; eino's own default (20) then applies
subagents: [researcher]           # references other file names; the order is the order of the task description
tools: [ls, read_file, glob, grep] # the allow-list: the tools this agent may use; absent = none
skills: [refactor]                # the allow-list of skills; absent = none, [] = none
mcps: [github]                    # the MCP servers this agent mounts; absent = none, [] = none

temperature: 0.2                  # this call's intent (the same model should use different parameters in different agents)
max_tokens: 8192
reasoning: high                   # a tier name, taken from the keys of models[].reasoning
summarize_at: 80%                 # this agent's compaction threshold, a percentage of the model's context_window
---

The body is the Instruction. Leave it empty to use deep.New's built-in prompt.
```

★ **三份清单都是允许清单，而且三份的缺省都是「没有」。** `tools:` 说这个 agent 可以用哪些
工具，`skills:` 说它可以加载哪些技能，`mcps:` 说它挂载哪些 server。★ 「键缺失」与「空
清单」现在是**同一件事**，因为它们意思相同 —— 而它们的意思是**什么都没有**
（`agentkit/spec/decl/decl.go:104`、`:125`、`:135`）。⇒ **列出来的才有**。

#### 字段全集

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `model` | `string` | ✅ | `<provider>/<model>`。**不继承、不回退** |
| `description` | `string` | | 进 `task` 工具描述。schema 不把它设成必填 —— 描述为空只是让那份描述里多一条空条目 |
| `max_iterations` | `int` | | `0` = 没声明，此时走 eino 自己的缺省 20（`eino@v0.9.22/adk/react.go:345-348`） |
| `subagents` | `[]string` | | 引用其他 agent 文件名；引用不存在或成环 ⇒ 启动期报错 |
| `tools` | `[]string` | | **这个 agent 可以用的工具允许清单。** ★ **键缺失 = 一个都没有** —— 清单没点名的工具不是这个 agent 的工具，模型压根儿看不见它（`agentkit/spec/decl/decl.go:104`）。名字是**暴露名**（模型看见的那个）；MCP 工具是 `mcp__<server>__<tool>`，尾部 `*` 是前缀模式（§6.3）。★ 这与档位是**两个问题**：档位说这个工具要不要人点头，这份清单说这个 agent 能不能用它 |
| `skills` | `[]string` | | 技能允许清单，名字是 `skills/` 下的**文件夹名**（技能自己 frontmatter 里的名字也认）。★ **键缺失 = 一个都没有**，`[]` = 也没有 —— 那个用来分辨两者的指针没了，因为「没声明」与「声明为空」现在意思相同（`agentkit/spec/decl/decl.go:125`） |
| `mcps` | `[]string` | | 这个 agent **挂载**的 MCP server，按**server 名**（`mcps/` 下的文件名）。★ **键缺失 = 一个都不挂载**，`[]` = 也没有 —— 与 `skills` 同一条规则（`agentkit/spec/decl/decl.go:135`）。挂载一个 server 会起一个子进程，所以它是要写下来的决定，绝不是缺省 |
| `temperature` | `*float32` | | 指针：0 与「没配」是两件事 |
| `max_tokens` | `*int` | | 同上 |
| `reasoning` | `string` | | 档位名；不在模型词汇表里 ⇒ 不注入，且**不出声**（见 §6.1） |
| `summarize_at` | `Threshold` | | 这个 agent 的压缩阈值：token 数、模型窗口的百分比，或 `off`（`agentkit/spec/decl/decl.go:142-147`） |

★ **`disabled_tools` 是被拒绝的，不是被改名的。** 旧键仍然能解析 —— 它必须能，加载器才能
抓住它 —— 但加载会**失败**，报错信息说明这两个键的意思**正好相反**
（`agentkit/spec/load.go:96`）：`tools` 说这个 agent **拿到**哪些工具，`disabled_tools`
说的是它**不拿**哪些。★ **没有别名**：别名会让旧键继续以旧意生效，或者 —— 更糟 —— 突然
以新意生效。

★ **为什么 server 清单是每个 agent 各有一份。** 一个 server 是**能力**，而能力属于用到它的
那个 agent：研究员不需要部署 server，部署 agent 也不需要 GitHub。⇒ `mcps/<server>.yaml`
文件说 **server 是什么**（它的命令、它的传输方式、它的档位），agent 的 `mcps:` 说 **谁启动
它**。

★ **两份清单是两种粒度，而且可以叠加。** `mcps:` 选**要挂载的服务器**；`tools:` 选**这个
agent 能看见的工具** —— 而它接受前缀，所以 `tools: [mcp__github__*]` 能让一个挂了两个
server 的 agent 只看见其中一个的工具（§6.3）。★ **两层都得写**：`mcps: [github]` 说
「启动它」，而一份没有 `mcp__github__*` 的 `tools:` 说这个 agent 看不见它的任何东西。
★ 两层之间矛盾就是**加载期错误**（`agentkit/spec/load.go:771` `checkAgentMCPNames`）：
在 `tools:` 里点名 `mcp__github__*` 而 `mcps:` 里没有 `github`，永远不可能匹配得上 ——
一条永远匹配不上的规则不是规则，是笔误。

★ **不存在的 server 是加载期错误。** 某个 agent 点名了一个 `mcps/` 下没有文件声明的
server ⇒ 加载失败，报错信息会列出**确实**声明过的那些 server
（`agentkit/spec/load.go:755` `validateMCP`）。★ 理由：一个因为拼写错误而缺失的工具，
和一个因为 server 挂了而缺失的工具长得一模一样 —— 而这两者里只有一个是配置错误。

★ **被启动的那份清单是并集。** 工具集对整次装配只有一份，所以管理器启动的是**某个**
agent 列出的那些 server（`agentkit/spec/load.go:829` `MCPServersInUse`）；只有子 agent
列出的 server 也会被启动 —— 那个子 agent 可能正是跑起来的那一个。★ 子 agent 带着**自己**
的清单（`agentkit/spec/decl/decl.go:698` `SubAgentSpec.MCPServers`），而**它**的清单收窄的
是**它自己**那张表，不是它父 agent 的。

★ **`description` 为什么重要**：它被渲染进 `task` 工具描述
（`eino@v0.9.22/adk/prebuilt/deep/task_tool.go:177-190`，`:182` 那行写出
`- <name>: <desc>`）—— 而 `subagent_type` 拼不中时 eino **返回错误字符串**而非给模型重试信号
（`:164`）。所以描述写不好，模型就选不到它。

| 约束 | 理由 |
|---|---|
| 文件名即 id；`main` 是保留名 | 默认 agent 与其他 agent 是**同一格式的同一个东西** —— 只是文件名恰好叫 `main`（`agentkit/spec/config.go:101`，`:270-272`） |
| `model` 必填（`description` 仅子 agent 需要） | `model` 必填是刻意的 ——「子 agent 静默继承主模型」是**成本事故**；`description` 决定模型选不选它，缺了只是选不中，不会启动失败 |
| `model: <provider>/<model>` | 必须带 provider 前缀。无前缀时「同名不同厂」无从分辨，且无法在启动期校验它解析得到 |
| 正文即提示词，**无 `instruction_file` 间接层** | 多一层就多一处可配错的地方 |
| `subagents: [文件名]` 声明拓扑 | 引用不存在的文件在启动时报错 |
| ★ **从 `main` 不可达的 agent 在这里*不是*错误** | openheron **不**做可达性检查。这样的 agent 只是永远不会被构造 —— 只有从入口 agent 可达的那些才会被建出来（`agentkit/build/assemble.go:81-96`）—— 而它仍然出现在自省里（`agentkit/spec/subagent.go:18-28`）。上游设计想要的是报错，openheron 没有这条 |
| 同名文件整体覆盖 | 「一个文件一个实体」相对列表合并的净收益 |
| 嵌套深度超 `agent.max_depth` ⇒ 报错 | 「配了三层但实际只跑两层」是最难查的一类问题 |

#### 深度上限

```yaml
# server/config.yaml
agent:
  max_depth: 1        # the root agent is level 0; only d < max_depth may spawn children
```

这个上限只住在**一处** —— `server/config.yaml` —— 缺省是 `1`
（`agentkit/spec/defaults.go:11`）。★ 没有第二份拷贝要对账，也没有「两者取更严」这条规则了：
`MaxDepth()` 直接读 server 自己的值（`agentkit/spec/load.go:756`）。★ 代价是这个上限**不进
热更新** —— `server/` 是冷分区，所以改这个上限下一次启动才生效（§12.2）。

**校验在启动期**（`validateDepth`，`agentkit/spec/load.go:706-723`），且是**报错**而非运行时
截断。装配层用同一判据，在到顶时**根本不构造**下一层（`agentkit/build/assemble.go:82`）——
双保险：装配期基于配置，运行期 `depthGuard` 基于**实际**嵌套
（`agentkit/build/topology.go:28-62`，挂在链上于 `agentkit/build/gate.go:172-178`）。

#### frontmatter 的三种输入

| 输入 | 行为 |
|---|---|
| `---` … `---` + 正文 | frontmatter 解出字段，正文（去掉分隔符后全部）成为 `Instruction` |
| 无 frontmatter 的纯 `.md` | ★ **在 openheron 里是错误** —— 加载器去解一份空的 frontmatter，报「文件是空的（或者只有注释）」（`agentkit/spec/config.go:518`，`agentkit/spec/load.go:82-84`）。上游设计让纯提示词草稿先过去、随后因缺 `model` 报错；openheron 更早炸，而且消息更清楚 |
| 有 `---` 开头但缺结束分隔符 | **错误**：无法判断正文从哪开始 —— "frontmatter closing delimiter not found"（`agentkit/spec/expand.go:48-65`） |

#### frontmatter 解析必须自写

eino 的 `parseFrontmatter` 在 `eino@v0.9.22/adk/middlewares/skill/filesystem_backend.go:165`，
**未导出**，无法复用。openheron 自写 —— `splitFrontmatter`，约二十行
（`agentkit/spec/expand.go:48-65`）。

`SKILL.md` 已用同一约定（`FrontMatter` + 正文，
`eino@v0.9.22/adk/middlewares/skill/skill.go:47-56`），所以 openheron 的 `agents/*.md` 与 eino
原生 markdown 约定**同构**，不是另立一套。

### 6.3 ★ 档位：没有分区，也没有全局清单（✅ 2026-11-07 已实施）

★ **没有 `policy/` 分区。** 档位住在**提供**工具的那些文件里，而**缺省是 `ask`**。
两件事，一条规则：

| | |
|---|---|
| 缺省档位 | **`ask`** —— 没人点名的工具先问一句 |
| 例外 | **被点名**：代码带一份缺省清单，而文件的 `allows:` 对它**加进**去 —— 文件里的任何东西都缩不了它 |

★ **为什么缺省是 `ask`。** 猜错的代价不对称：多一次点击，对上的是没人看见的一次破坏性调用。
没人点名的工具，就是 openheron 不敢替它担保的工具 —— 一个 MCP server 的工具是**运行期才发现**的，
而 stdio 之下 server 是带着 agent 自己权限的子进程。★ 换成另一个缺省，代价是一个不点一下就不能
委派的 agent，而 `task` 正是为此留在代码那份清单里：一个根本不能委派的 agent 做不了它的活。

#### 例外从哪儿来

| 来源 | 它点名的是什么 | 用的词汇 |
|---|---|---|
| `agentkit/build/tools.go:62` `builtinAllowedTools` | **缺省**那一份：文件系统读取器（`ls` `read_file` `glob` `grep`）、委派工具（`task` `write_todos`）与技能读取器（`skill`）。★ 它永远生效 —— 文件是往上加，所以要把一个名字从里面去掉，得改这张表 | 暴露名 |
| `agentkit/spec/load.go:299` `AllowedTools` | **确实写了**的那些文件的工具 —— `tools/http.yaml` 说 `allows: [request_get]`，`tools/kb.yaml` 说 `allows: [kb_search]` | **文件自己的** |
| `agentkit/build/mcptools.go:173` `mcpAllowedNames` | 一个 MCP server 自己的工具 | **server 自己的** |

★ **一个文件说自己的词汇。** `http.yaml` 说 `request_get`，因为那个文件讲的就是这件事；
HTTP 工具自己的名字就是暴露名（`agentkit/spec/decl/decl.go:272-278`）。一个 MCP server 说
`create_issue` —— 不是 `mcp__github__create_issue` —— 而到暴露名的翻译只发生**一次**，
就在工具被挂上去的那一处（`agentkit/build/mcptools.go:141` `mcpExposedName`）。

★ **那张表是从配置装配出来的**，所以一个工具的档位在**它的 server 甚至还没起来之前**就能回答
（`agentkit/build/spec.go:64` `policyOf`、`agentkit/build/policy.go:55` `TierOf`）。正因如此，
配置编辑器才能对一个没在跑的 server 回答「这个工具是什么档位」。

#### ★ 两个问题，分开

★ **档位与 agent 的工具清单是两个不同的问题**，而代码把它们放在两个不同的地方：

| | |
|---|---|
| `Policy.TierOf(name)`（`agentkit/build/policy.go:55`） | **allow** / **ask** —— 这个工具要不要人点头？★ **每个 agent 共享**：代码那份清单，加上每个文件的 `allows:`，加上每个 server 的 `allows:` |
| `Table.Blocked(name)`（`agentkit/build/policy.go:130`） | **这个 agent** 能不能用它？★ **按 agent**：agent 自己的 `tools:` 清单 |

★ **档位清单是共享的，agent 的清单不是。** 一个说「这个工具不用点头」，另一个说「这个
agent 能不能用它」。一个工具可以是 `allow` 而仍然不在某个 agent 里 —— 这两个答案根本不
搭话，而这正是要点：决定档位的代码不知道当前跑的是哪个 agent，决定 agent 工具的代码不知
道档位。

#### 「不被暴露」只在两处说

- **文件自己的 `allows:` 清单** —— 这个文件不点头就提供的那些（§6.6、§6.7）；
- **某个 agent 的 `tools:` 清单** —— 这个 agent 可以用哪些（§6.2）。

⇒ 第三处全局清单，就是多一处要同步的地方 —— 而它是三者里最糟的一处：只有它必须用**暴露**出来的
词汇点名工具，而工具自己的文件说的是它们自己的词儿。

★ **没有 `deny` 这一档。** 全局 deny 清单跟着分区一起没了
（`agentkit/build/policy.go:15-20` —— 只剩 `allow` 与 `ask`），而一个 agent 不能用的工具**保留自己
的档位**，并在它的 `reason` 里说明原因：是**这个 agent** 拿不到它，不是这个工具不合法
（`agentkit/build/introspect.go:112` `verdictOf`）。★ **现在只有一个理由，就是一个**：工具不在
某个 agent 的表里，是因为那个 agent 的 `tools:` 清单没点名它
（`agentkit/build/introspect.go:36` `reasonOf`）。旧的 `"disabled_tools"` 与 `"mcps"` 两个理由
没了，`LayerMCPScope` 那一层也没了 —— agent 没挂载的 server 压根儿没有工具可挡，而挂了却没
点名的 server，被同一份清单挡住。

★ **闸门包住例外清单没有点名的一切**（`agentkit/build/gate.go:43` `newApprovalMiddleware`）：
`allow` 原样放行，`ask` 打断这一轮等人。没有第三个分支 —— 一个不在 agent 工具集里的工具根本到不了闸门。

#### ★ 前缀匹配（★ 2026-11-06 决策，对某个 agent 的 `tools` ✅ 已实施）

★ **某个 agent 的 `tools`** 除了**精确工具名**，还接受**尾部 `*` 的前缀模式**：

| 条目 | 匹配 |
|---|---|
| `execute` | 名字正好是 `execute` 的那个工具 |
| `mcp__github__*` | server 名为 `github` 的每一个 MCP 工具 |
| `mcp__*` | 来自任何 server 的每一个 MCP 工具 |

| # | 规则 | 理由 |
|---|---|---|
| 1 | 只有**结尾**的 `*` 是前缀模式；`*` 出现在别处 ⇒ **加载期硬错误** | `mcp*foo` 看着像通配符而其实不是 —— 一条读起来与实际覆盖范围不同的规则，就是一次静默的错配 |
| 2 | 单独一个 `*` ⇒ **加载期硬错误** | `tools: ["*"]` 会点名世上所有工具 —— 而这份清单是允许清单，那与模式通常的意思正好相反；那是要写出来的一个决定，不是一个通配符 |
| 3 | `*` 前面不能为空 | 规则 2 |
| 4 | 精确条目与前缀条目**等价** —— 命中就是这个 agent 拿到这个工具 | 这里没有档位可选；这份清单说的就是这个 agent 有什么 |

★ **它住在哪儿。** 加载器拒绝那些不是模式的写法
（`agentkit/spec/load.go:123` `validateAgentTools`，语法本身在
`agentkit/spec/decl/decl.go:434` `ParseToolNamePattern`）；那张表把前缀模式与精确名放
在一起留着（`agentkit/build/policy.go:100` `newTable`），而 `Table.Blocked` 匹配它们
（`agentkit/build/policy.go:130`）。

★ **前缀模式之所以安全，是因为 server 名里不许出现连续的下划线**
（`agentkit/spec/decl/decl.go:418` `MCPServerNamePattern`）：`mcp__github__` 够不到
`mcp__github_x__`，因为后一个 server 名压根儿声明不出来。★ 拼出来的名字从不被拆回去 ——
模式是前缀，不是名字。

★ **MCP 命名空间有两层，而它们不是同一个决定。** `mcps:` 说哪些 server 被**挂载**（作为子
进程启动）；`tools:` 说 agent **看见**它们的哪些工具 —— `mcp__<server>__<tool>`，或者模式
`mcp__<server>__*`。★ **两层都得写**：`mcps: [github]` 说「启动它」，而一份没有
`mcp__github__*` 的 `tools:` 说这个 agent 看不见它的任何东西。★ **挂载**才是启动进程的那件事；
**可见**才是把名字放进模型工具集的那件事 —— 而一个挂载了、却没被 `tools:` 点名的 server，
就是一个跑着但看不见的 server。

★ **这是前缀语法仅剩的用武之地。** MCP 的档位住在 server 自己的文件里，在那里，文件自己的
`allows:` 不用任何模式就能覆盖一整类；剩下的是**可见性**，而「这个 agent 看不见那个 server
的任何工具」是唯一一句关于**命名空间**而不是关于某个工具的话。

★ **它不新增档位，也不重排档位** —— 模式只决定**一条条目是否匹配某个名字**。

### 6.4 `server/config.yaml`

进程自身的配置。**它不进热更新** —— 改了要重启（见 §8）。server 分区带着 `hot: false`
（`agentkit/spec/config.go:51-55`），配置编辑器干脆不写它（`agentkit/spec/write.go:74-77`）。

★ `log_level` 默认 `info`，取值 `debug` / `info` / `warn` / `error`，由 `OPENHERON_LOG_LEVEL`
覆盖。非法值不致命：`loadServer` 归一为 `info` 并记一条 `deferred`（`agentkit/spec/load.go`
的 `normalizeLogLevel`）。和 `server/` 的其余字段一样，它改了要重启。

★ `log.file` 默认**空**，也就是只写 stderr。填了路径才会落盘，由 `lumberjack` 按大小轮转；
`OPENHERON_LOG_FILE` 覆盖路径。打不开文件**不阻止启动**：进程留在 stderr，原因写在同一行
日志里（`main.applyLogFile`）。设计见 `docs/logging-design.md` §10。

```yaml
# The process's own configuration.
listen_addr: ":8080"        # ★ it must be quoted: a value starting with : parses ambiguously
frontend_mode: embed        # embed | dir — the UI compiled into the binary, or a directory on disk
static_dir: web/dist        # relative to the **project root** (not the config root, not cwd)
data_dir: state             # relative to the **config root**; an empty string means state/
log_level: info             # debug | info | warn | error; $OPENHERON_LOG_LEVEL overrides it

log:                        # ★ omitted `file` = stderr only (the default)
  file: ""                  # relative to the **config root**; e.g. logs/openheron.log
  console: true             # keep stderr alongside the file
  max_size_mb: 20           # one file's cap; 0 = no cap
  max_age_days: 14          # 0 = keep everything
  max_backups: 5            # 0 = keep every rotated file
  compress: false           # gzip the rotated files

start_timeout: 60s          # the cap on one turn, from submission to entering the running state
                            # ⚠️ a lenient duration (see "durations come in two tiers" below)
lease_ttl: 10m              # how long a run's lease survives without progress

retry:
  max_retries: 5            # → helpers.ApplyModelRetry

agent:
  max_depth: 1              # the nesting depth cap; the entry agent is level 0

watch:
  enabled: true             # not written = on; an explicit false = off
  debounce: 200ms           # a lenient duration; a typo falls back to the default and is recorded in deferred

# ★ There is **no** `tools:` section here — the external tools' configuration moved to
#   the `tools/` directory (see §6.6).
#
#   The reason for the move is **hot reload**: server/ configures the process itself and
#   does not take part in hot reload, while "change one tool's timeout" is the kind of
#   change that should take effect on the next turn.
```

#### 时长分两档（★ 实施时才发现的区分）

| 类型 | 用于 | 写错时 | 理由 |
|---|---|---|---|
| `Duration` | `llm/*.yaml` 的 `timeout` | **硬报错，带行号** | 写错会让请求超时行为不可预测，必须当场暴露 |
| `LenientDuration` | `debounce` `start_timeout` `lease_ttl` 以及 `tools/` 下的各个超时 | 退回默认 + **记 `deferred`** | 只影响「等多久」，不该让程序起不来 |

两者都必须带单位（`5m` / `60s`）—— `timeout: 300` 会被拒绝，因为 yaml 能把 `!!int` 节点"成功"解进
string，只看 `Decode` 的错误会漏掉它（`agentkit/spec/decl/decl.go:612-619`）。

★ **降级必须出声**：写错的项进 `deferred`，启动时与其它降级一起打印。悄悄退回默认等于「配了没用」
不可见。

★ **但兜底值对每一个宽松字段都是同一个 200ms，而消息不管写错的是哪个字段，都写 `watch.debounce`。**
`LenientDuration.UnmarshalYAML` 退回 `decl.DefaultDebounce`
（`agentkit/spec/decl/decl.go:537-546`），收集消息的地方把文本写死成
`watch.debounce fell back to default`（`agentkit/spec/reload.go:80-84`）。所以 `tools/http.yaml` 里
`timeout` 写错，报出来的是一条 `watch.debounce` 消息，而超时变成了 200ms —— 而不是这个字段本来该拿
到的 30s。读这条消息时要读成「有个时长没解析出来」，而不是「看门狗跑歪了」。

#### 字段全集

| 字段 | 类型 | 缺省 | 说明 |
|---|---|---|---|
| `listen_addr` | `string` | `:8080` | Hertz 监听地址 |
| `frontend_mode` | `string` | `embed` | `embed` 服务编译进二进制的界面；`dir` 服务磁盘上的一个目录，`static_dir` 指向它。值不认识时只记一条告警，前端不启用（`main.go:418-442`） |
| `static_dir` | `string` | `web/dist` | 相对**项目根** `<root>` 解析（`Service.ResolveDir`），非 cwd、非配置根；只在 `frontend_mode: dir` 时才被读 |
| `data_dir` | `string` | `state` | 相对**配置根**解析（`Service.DataDir`），非项目根、非 cwd；空串等同 `state`；目录不存在时创建 |
| `log_level` | `string` | `info` | `debug` `info` `warn` `error`；非法值回退 `info` 并记 `deferred`；由 `OPENHERON_LOG_LEVEL` 覆盖 |
| `log.file` | `string` | `""` | 日志文件；相对**配置根**解析（`Service.LogFile`），空串 = 只写 stderr；由 `OPENHERON_LOG_FILE` 覆盖；打不开文件只告警、不阻止启动 |
| `log.console` | `bool` | `true` | 写文件的同时保留 stderr；`false` 只写文件 |
| `log.max_size_mb` | `int` | `20` | 单个文件的 MB 上限；`0` = 不按大小轮转；负数归一为 `0` 并记 `deferred` |
| `log.max_age_days` | `int` | `14` | 轮转文件的保留天数；`0` = 不按天数清理 |
| `log.max_backups` | `int` | `5` | 最多保留几个轮转文件；`0` = 全部保留 |
| `log.compress` | `bool` | `false` | 轮转文件是否 gzip |
| `start_timeout` | `LenientDuration` | `60s` | 与 `agentkit/run.DefaultStartTimeout` 同值（`agentkit/run/runtime.go:26`） |
| `lease_ttl` | `LenientDuration` | `10m` | 一个 run 的租约在无进展的情况下能存活多久；它必须**长于 provider 自己的超时**，否则一个慢但活着的模型会在思考途中被 fence 掉（`agentkit/run/lease.go:20`） |
| `retry.max_retries` | `int` | `5` | 传 0 也用默认 ——「不配」与「配成 0」是两件事（`agentkit/spec/load.go:467-472`） |
| `agent.max_depth` | `int` | `1` | 见 §6.2 |
| `watch.enabled` | `*bool` | `nil`→开 | 用指针区分「没写」与「写了 false」（`agentkit/spec/reload.go:86-91`） |
| `watch.debounce` | `LenientDuration` | `200ms` | 轮询间隔取其一半，下限 20ms（`agentkit/spec/reload.go:141-149`） |

> **★ 项目根 vs 配置根**：`Service.ProjectRoot()`（`agentkit/spec/config.go:320`）是静态资源与
> agent 工作目录所在的那一层；`Service.Root()`（`agentkit/spec/config.go:310`）是配置根 ——
> `tools/`、`mcps/` 与 `state/` 都在它下面。**两者不同**：`static_dir` 按前者解析，`state/` 按后者。
> 把二者混用正是「前端 404 / agent 被告知在 `.openheron` 里工作」的根因。

★ 这里的每一个值都能被环境变量覆盖 —— `LISTEN_ADDR`、`DATA_DIR`、`PROJECT_ROOT`、
`FRONTEND_MODE`、`STATIC_DIR`（`main.go:225-227`、`main.go:415`、`main.go:429`）；全集见 §4.4。

★ **与 `tools/` 和 `mcps/` 不同，`server/` 没有文件名白名单。** 它下面的每一个 `.yaml` 都被解进
同一个 `decl.Server` 结构（`agentkit/spec/load.go:100-107`），所以一个手滑的 `server/typo.yaml`
会被并进配置，而不是被拒掉。`server/config.yaml` 是约定，不是加载器强制的规则。

### 6.5 `skills/<name>/SKILL.md`

eino 原生约定（`eino@v0.9.22/adk/middlewares/skill/filesystem_backend.go:46-48`）：只扫**一级子
目录**，读 `<dir>/<skill>/SKILL.md` —— **deeply nested 的会被忽略**。

```markdown
---
name: refactor
description: ...     # ★ this is what decides whether the model picks it
context: inline       # inline | fork | fork_with_context
agent: main           # fork mode takes it from the AgentHub by name
model: fast           # takes it from the ModelHub by name
---
```

frontmatter 的 schema 是 eino 的，不是 openheron 的
（`eino@v0.9.22/adk/middlewares/skill/skill.go:48-54`）：

| 字段 | 类型 | 含义 |
|---|---|---|
| `name` | `string` | skill 自己的名字 —— **模型**认的身份 |
| `description` | `string` | 渲染进 skill 工具的描述里 —— ★ 它决定模型会不会挑它 |
| `context` | `inline` / `fork` / `fork_with_context` | 这个 skill 怎么跑 |
| `agent` | `string` | fork 用的 agent（走 `AgentHub`） |
| `model` | `string` | 模型覆盖（走 `ModelHub`） |

★ **`inline` 不是一个声明出来的常量。** eino 只声明了 `fork` 与 `fork_with_context`
（`eino@v0.9.22/adk/middlewares/skill/skill.go:40-45`），其余任何值 —— 包括空值，也包括写错的
值 —— 都落进 inline 分支（`eino@v0.9.22/adk/middlewares/skill/skill.go:430-440`）。所以
`context: inline` 能用，但拼错不会大声报错：它悄悄地就是 inline。openheron 自己的清单把空模式报成
`inline`（`agentkit/build/skill.go:255-267`）。

#### 目录怎么定位

```
EINO_EXT_SKILLS_DIR environment variable    ← the override, the highest one
   ↓ unset
look for .openheron/skills/ upward from cwd  ← agentkit/build/skill.go:208 probeProjectSkills
   ↓ not found
skills are not enabled
```

★ **显式配了却不存在会打日志**：由 `EINO_EXT_SKILLS_DIR` 指名的目录不存在会被报出来
（`agentkit/build/skill.go:197-204`）。静默返回「未启用」会让「我配了 skills 怎么没生效」变成一个
查不出原因的问题 —— 属 §4.3 说的「配了没用」。

★ **这次向上找不是配置根那套规则。** 配置根从不向上找（`docs/architecture.md` §3.1），而 skills 目录
会：skills 是一棵目录树，向上找能复用家目录里的 skills；配置根是一组实体，向上找会让同一条命令读到
另一份配置。

#### 编辑器里怎么改（★ 2026-11-08，✅ 已实施）

★ **skills 是唯一嵌套的分区**：`skills/<name>/SKILL.md`。理由不是好看 —— skill 的名字**就是**
文件夹名，agent 在 `skills:` 里写的就是它；一个 skill 就是「一个文件夹 + 一个固定文件名」，所以
它的路径天生比别的分区深一层。别的分区写深一层会被**拒掉**：加载器只读一层，
`agents/sub/main.md` 这样的文件会被保存、然后永远不被加载。

| 规则 | 在哪 |
|---|---|
| 只有这一个分区允许两层 | `agentkit/spec/write.go:136` `shapeHint`；拒绝在 `ValidateReadPath` 里 |
| 里面的文件名固定 `SKILL.md` | `agentkit/spec/config.go:33` `SkillFileName` |
| 文件夹名要能被读回来：`^[a-z0-9]+([_-][a-z0-9]+)*$` | `agentkit/spec/decl/decl.go:467` `SkillNamePattern` |
| 只列**有 `SKILL.md`** 的文件夹 | `agentkit/spec/write.go:256` `listNested` |
| 新建时文件夹跟着建、删除时空文件夹一起删 | `agentkit/spec/write.go:280` 与 `Delete` 里那一支 |
| 热更新：改一个 `SKILL.md`，下一轮生效 | `Category{hot: true}`（`agentkit/spec/config.go:100`）；指纹的遍历是**递归**的，两层下的文件也算 |

★ **`skills/` 是唯一没有 loader 的分区。** skill 文件不是配置层读的 —— 运行时自己的 backend 读它
（`agentkit/build/skill.go:23`）。配置层要这一项只是为了**表面**：清单、路径规则、编辑器，
所以加载循环跳过 `loader == nil` 的分区，而不是给它一个空 loader。

★ **写入门在这里多一道别处没有的拒绝。** 运行时从哪个目录读 skill 由 `ResolveSkillsDir()`
（`agentkit/build/skill.go:182`）回答：`$EINO_EXT_SKILLS_DIR`，否则从工作目录向上找
`.openheron/skills`。它**可以不是**配置根下的 `skills/`。当它不是的时候，编辑器写进来的文件
根本不会被加载 —— 于是保存被拒，清单里这一分区标成只读并**点名真正的目录**
（`agentkit/spec/write.go:150` `validateWritable`、`agentkit/spec/config.go:299` `skillsElsewhere`）。

★ 反过来说：进程启动时**一个 skills 目录都没找到**，不算这种情况。那时配置根下的 `skills/` 正是
探针下次会找到的地方，保存照常 —— 否则「从零建起第一个 skill」这件事就没有入口。

#### `agent:` / `model:` 的落地

两行 frontmatter 现在都生效：`agent:` 走 `AgentHub`（`fork` / `fork_with_context` 两种模式），
`model:` 走 `ModelHub`（inline 模式换本轮模型）。实现与边界见 §7。

- `context: inline` 只需要 description；
- 写了 `agent:` 但不在当前 agent 的子树里 ⇒ 报错并列出可用名字（`agentkit/build/hub.go:152-156`）；
- 写了 `model:` 但引用解析不了 ⇒ 报错，不再静默忽略（`agentkit/build/hub.go:87-93`）；
- hub 没有绑定 resolver 时（只有测试会这样）⇒ 报错，而不是悄悄用原模型（`agentkit/build/hub.go:76-78`）。

★ **openheron 在 agent 这一侧还多了一个字段**：`agents/<id>.md` 里的 `skills` 键是 skills 目录下
各文件夹的白名单。★ **缺省是一个都没有**，与 `tools:` 同一条规则 —— agent 没点名的 skill
不是它的 skill（`agentkit/spec/decl/decl.go:125`）。★ 它只在 agent 压根儿有 `skill` 工具时
才有意义：`tools:` 点名了 `skill` 而 `skills:` 是空的，加载器会说出来
（`agentkit/spec/load.go:679` `noticeAgentTools`）—— 那是一份合法但自相矛盾的配置。这个范围
过滤的是两半（`agentkit/build/skill.go:147-180`）：`List`，也就是模型**看得见**的；`Get`，
也就是它真能**加载**的。只过滤一半，会留下一个模型记得名字就能加载的 skill。

### 6.6 `tools/<tool>.yaml`（★ 2026-10-24 新增分区）

外部工具与知识库的配置。**一个工具一个文件**（下面的路径相对**配置根** —— 默认的那个就是
`.openheron/`）：

```
.openheron/tools/
├─ http.yaml             → decl.HTTPToolsCfg
├─ web_search.yaml       → decl.WebSearchCfg
├─ wikipedia.yaml        → decl.WikipediaCfg
├─ filesystem.yaml       → decl.FilesystemCfg (the filesystem tools' tiers)
└─ kb.yaml               → decl.KB (the knowledge base)
```

★ **文件的内容就是那个工具自己的配置**（没有外层包装键）—— `http.yaml` 里直接写 `methods:`，不是
`http: { methods: … }`。没有 `enabled` 字段，也没有「写个 null 关掉」这种形态。

★ **文件不决定哪些工具存在 —— 决定的是 provider。** 工具集来自代码：`defaultToolRegistry` 里注册的
那些 provider（`agentkit/build/tools.go:35`），以及 `fsx.New`（`agentkit/build/snapshot.go:56`）
无条件挂载的文件系统工具。★ 文件负责的是**参数**和**档位** —— 哪些工具不用点头。

★ 今天这两件事在三个族上恰好重合：`httpRequestToolProvider`、`webSearchToolProvider` 与
`wikipediaToolProvider` 在配置缺失时返回**空**（`agentkit/build/nettools.go`：
`if cfg == nil { return nil, nil }`），所以对它们来说「文件在不在」碰巧成了开关。★ 那是 **provider
的决定**，不是文件形状蕴含的 —— 而文件系统工具是另一种情况：它们**一直都在**，所以它们的文件
（见下文 §6.6）调的是它们的档位，没法把它们开起来或关掉。加载器自己的规矩 —— 文件名是白名单，
未知文件名是启动错误（`agentkit/spec/load.go:187-215`）—— 说的是**读哪些文件**，不是哪些工具存在。

★ 空文件会被拒绝，并给出一条提示、告诉用户该写什么（`agentkit/spec/load.go:173-180`）：文件的内容
**就是**这个工具的配置，所以「这里没有配置」才是用户用得上的那句话。

★ **它进热更新**：改一个工具的超时、换一个搜索后端、给知识库换个语料目录，写完**下一轮就生效**，
不必重启进程。tools 分区带着 `hot: true`（`agentkit/spec/config.go:56-60`）。这正是它从
`server/config.yaml` 搬出来的理由：`server/` 配的是进程自身，不进热更新 —— 而这些改动跟「进程
自身」没关系。

★ **同名文件整体替换**：同一层里一个文件名只有一份，`tools/http.yaml` 就是它。这比原来更细 ——
以前改 `server/config.yaml` 里的 `tools.http` 会连带把 `tools.web_search` 也一起换掉（同一份文件、
整体替换），现在它们互不相干。

★ 知识库的 `kb.yaml` 从原来的 `kb/config.yaml` 搬来。**索引目录没搬**：它在运行数据里
（`state/kb/<index>/`，`agentkit/build/kb.go:42`），与语料同生共死。

#### ★ 每个文件说的名字 —— 以及模型看到的名字（✅ 已实施）

一个文件用**自己的词汇**声明它的档位，而 provider 负责把那个词汇映射成模型看到的名字。
两者不是一回事，而这正是要点：

| 文件 | 这个文件说的词汇 | 模型看到的名字 |
|---|---|---|
| `http.yaml` | 方法：`post` | `request_post` |
| `web_search.yaml` | 后端：`duckduckgo` | `duckduckgo_search` |
| `wikipedia.yaml` | ——（一个工具） | `wikipedia_search` |
| `kb.yaml` | `kb_search` `kb_index` | 同左 |
| `filesystem.yaml` | ——（一个工具） | `ls` `read_file` … 同左 |
| `mcps/<server>.yaml` | server 自己的名字：`create_issue` | `mcp__github__create_issue` |

★ **文件系统工具那一行是唯一不需要翻译的一行。** 它们来自框架的中间件而不是来自某个 provider，
而中间件的名字本来就是暴露名 —— 所以它们的文件说的就是模型看到的那套词汇。

★ **拆分之前，这张表并不存在** —— 策略文件说的是第三列，而工具自己的文件说的是第二列，于是
`http.yaml` 说 `post`，`permissions.yaml` 却不得不说 `request_post`。★ 一个工具两套词汇，
就是等着发生的错配；而名字拼错了，在工具的表现与文件所写不一致之前根本看不出来。

⇒ **一个文件的 `tools:` 与 `allows:` 写在第二列里。** 由 provider 来翻译；别的任何东西
都不必知道暴露名。

★ **MCP provider 只在一处合成暴露名**
（`agentkit/build/mcptools.go:140` `mcpExposedName`），所以 provider 与那个投影不可能
对「一个工具叫什么」有分歧 —— 而文件自己的 `allows:` 点名的永远是 server **自己的**工具名，
从来不是暴露名（`agentkit/build/mcptools.go:122` `mcpOffers`）。

（暴露名在 `agentkit/spec/decl/decl.go:252-257`、`kb/tools.go:16-17` 与 eino 的
`adk/middlewares/filesystem/filesystem.go:38-46`。）

#### ★ 每个文件都带的那几个键（✅ 已实施）

`tools/` 与 `mcps/` 里的每个文件都带同样这几个键，而且它们在任何地方都是同一个意思：

| 键 | 类型 | 缺省 | 含义 |
|---|---|---|---|
| `tools` | `[]string` | 全部 | ★ **过滤器，不是开关** —— 要提供的那些**原始**名字（这个文件自己的词汇，**不是**暴露出来的那些名字），**从 provider 给出的那些工具里挑**。不写 = 全部 |
| `allows` | `[]string` | 空 | **不用点头**的那些名字 —— **用这个文件自己的词汇**。不写或为空 = 这个文件提供的每个工具都要点头 |

★ **缺省是 `ask`，而 `allows:` 是例外。** 除非有别的东西说话，否则每个工具都要先点头。
没有分组缺省，也没有特例：同一条规则盖住文件系统工具、HTTP 工具与一个 MCP server。

★ **代码带一份缺省 allow 清单** —— `ls`、`read_file`、`glob`、`grep`、`task`、
`write_todos`、`skill`（`agentkit/build/tools.go:61` `builtinAllowedTools`）。★ 它永远生效：文件是
往它上面**加**，而文件里的任何东西都拿不走一个名字。委派工具在里面，是因为一个不点一下
就不能委派的 agent 等于不能委派；而读取器在里面，是因为它们只读。

★ **文件的 `allows:` 点名的是它提供的那些工具的例外**，用那个文件自己的词汇 ——
`tools/http.yaml` 说 `allows: [request_get]`，`tools/kb.yaml` 说 `allows: [kb_search]`，而
`tools/filesystem.yaml` 正是 `write_file` / `edit_file` / `execute` 被放宽的地方。
加载器把它们收集起来（`agentkit/spec/load.go:299` `AllowedTools`），而那张表是三份清单的并集：
这份、代码那份、以及各 server 那份（§6.3）。★ 对各个 provider 来说，一个**不在**的文件什么都不贡献：
那个工具也没被挂上，所以没有档位可回答。★ 文件系统工具是例外：它们不管文件在不在都会被挂上，
所以文件不在只是让代码那份缺省原样留着。

★ **文件里没有 `deny`，而且已经没有全局清单了**（§6.3）。「不被暴露」只说两处：这个
  文件的**清单** —— 每个文件把清单叫成合口的名字，HTTP 用 `methods:`（`http.yaml`），其余用 `tools:` ——
  与某个 agent 的 **`tools:`**。

★ **全局那份清单之所以没了，是因为它是同一个决定的第三处，而且是**唯一**必须说暴露名的
  那处，而工具自己的文件说的是它们自己的词儿。⇒ **每一层一个机制，每一层一套词汇。**

★ **这两个键回答两个不同的问题，谁也吞不掉谁：** `tools:` 回答**提供什么**，
  `allows:` 回答**其中哪些不用点头**。其余的一律要点头。

★ **`allows:` 就是全部。** 没有第二个键，也没有分组缺省：缺省就是 `ask`，而清单点名的
  是那些例外。一个什么都不列的文件，对它提供的每一样东西都要点头。

★ **一份 `allows:` 清单能覆盖 server 还没提供的工具**，因为它是关于**服务器**的一句话，
  而不是关于它眼下恰好有的那些名字。server 的工具清单只有运行期才知道，所以一条点名了工具的
  规则覆盖不了 server 明天才加的工具 —— 而清单能，因为它用 server 自己的词汇点名了那些例外，
  其余的默认要点头。

#### ★ 缺省是 ask，而 `allows:` 是例外

★ **同一条规则，处处适用。** 除非有别的东西说话，否则一个工具都要先点头。例外写在提供这个
  工具的那个文件的 **`allows:`** 清单里，用那个文件自己的词汇 —— 而对于**没有自己的文件**的工具，
  写在代码里。

★ **命名空间的缺省是 `ask`**（`agentkit/build/policy.go:55` `TierOf`）—— ★ 那张表是从**配置**
装配出来的，所以档位在 server 还没起来之前就能回答
（`agentkit/build/mcptools.go:172` `mcpAllowedNames`）。

★ **代码自带一份 allow 清单**，给那些没有自己的文件的工具：

```
ls  read_file  glob  grep
task  write_todos  skill
```

★ **代码为什么要有这么一份。** `task` —— 委派工具 —— **没有自己的文件**：它来自框架的
  agent，而不是一个 `tools/` 条目。在「缺省点头」之下它每次委派都要点头，而一个不点一下
  就不能委派的 agent 等于不能委派。⇒ 框架自带的那些工具正是只有代码才能表达的例外。

★ **文件的 `allows:` 点名的是它提供的那些工具的例外。** 于是 `tools/http.yaml` 可以放宽这一套
  （`request_post` 不用点头），也可以收紧（`request_get` 终究还是要点头）。

| 文件 | 它的 `allows:` | 其余 |
|---|---|---|
| `tools/http.yaml` | 读方法 | 写方法 → ask |
| `tools/web_search.yaml` | `duckduckgo_search` | — |
| `tools/wikipedia.yaml` | `wikipedia_search` | — |
| `tools/kb.yaml` | `kb_search` | `kb_index` → ask（它要写索引） |
| ★ `mcps/<server>.yaml` | 通常是空的 | ★ **一切都点头** —— server 的工具是运行期才发现的，openheron 无从知道 `delete_repo` 做什么，而在 stdio 之下那个 server 是带着 agent 自己权限的**子进程**（§6.7）⇒ 猜错的代价是不对称的：多一次点击，换掉的是一次没人看见的破坏性调用 |

★ **用户信得过的 MCP server 说一次就够了** —— 而例外就是 server 自己的工具名：

```yaml
allows: [read_issue, list_issues]
```

★ **出厂模板自己带着 `allows:` 行** —— `tools/http.yaml` 说
`allows: [request_get]`，`tools/web_search.yaml` 说 `allows: [duckduckgo_search]`，
`tools/wikipedia.yaml` 说 `allows: [wikipedia_search]`，`tools/kb.yaml` 说
`allows: [kb_search]` —— 于是档位是**看得见、改得动**的，而不是靠约定隐含的，
而一份新工作区从一开始就让它的只读工具直接放行。★ 那些可选的集成
留在 `tools/` 那五个文件里：它们的档位是**看得见、改得动**的，而一份新工作区从一开始
就让它的只读工具直接放行。★ `--no-tools` 只是让那一段不写出来而已。

#### ★ 档位改动下一轮就生效

★ `tools/` 与 `mcps/` 都是**热**分区，所以档位并不特殊：改一个档位，下一轮就生效，
跟这个分区里的任何别的键一样。策略表随快照一起重建，于是闸门从下一轮起读到新档位 ——
不用重启，也没有那个工具还留着旧档位的陈旧窗口。

★ 重载唯一要顾对的是**客户端**：快照换代时，stdio 的子进程被拆掉、新的被拉起来
（`docs/mcp-design.md` §5.4）。一个资源重建不出来的热分区，就是一个谎称自己是热的分区。

#### ★ 文件系统工具：一个调音旋钮，加上代码里的缺省

文件系统工具来自框架的中间件，而不是来自某个 provider，所以是中间件把它们挂上、没有任何文件能
把它们打开 —— 于是它们的文件是一个**调音旋钮**（`tools/filesystem.yaml`），而文件不在时生效的
是代码那份缺省清单（`agentkit/build/tools.go:61` `builtinAllowedTools`）。

这一套就是 eino 的文件系统中间件：`ls`、`read_file`、`write_file`、`edit_file`、
`glob`、`grep`、`execute`（`eino@v0.9.22/adk/middlewares/filesystem/filesystem.go:38-46`）。

```
ls  read_file  glob  grep
```

★ **读取器直接放行，写入器要点头。** `write_file`、`edit_file` 与 `execute` 不在清单里，
所以它们会为一个人打断这一轮 —— 这个方向是对的：它们正是那三个会改动这台机器的工具。

★ **它们有自己的文件了，而那是一个调音旋钮，不是开关** —— 文件系统工具是 agent 的核心能力，
中间件无条件把它们挂上，所以 `tools/filesystem.yaml` 加不了也删不了一个工具。它只带 `allows:`
一样东西（§6.6 的字段清单），而这恰恰是档位模型需要的：给一个不属于任何 provider 文件的工具，
留一处点名例外的位置。

★ **这个文件里的 `allows:` 是加法的。** 写在这里的名字是加进代码那份缺省清单
（`agentkit/build/tools.go:61`）—— 它从来拿不走一个名字：

```yaml
# .openheron/tools/filesystem.yaml
#
# ★ 三个写入工具，不用点头 —— 一个自己动手改文件、跑命令的 agent。
#   文件不在（或 `allows: []`）时它们要点头，那就是缺省。
allows: [write_file, edit_file, execute]
```

★ **于是这个文件放宽档位，而 agent 的 `tools:` 收窄可见性。** 这就是那条分工，而且是刻意的：
档位是关于**工具**的一个决定（它该待在工具旁边），而一个 agent 能不能用它，是关于**某一个
agent** 的决定（它该待在那个 agent 的文件里）。两个机制、两套词汇、不重叠 —— 也没有第三处能把
同一个决定再做一遍。

★ **这两者互不干涉。** 文件的 `allows:` 说一个工具要不要人点头；agent 的 `tools:` 说这个
agent 有没有这个工具。一个没点名 `write_file` 的 agent 压根儿拿不到它 —— **没有审批卡片，
没有档位，没有问题**。而点了名的 agent 仍然要点头，因为那张档位表是每个 agent 共享的
（§6.3）。

★ **它说的工具名就是中间件自己的名字** —— `ls`、`read_file`、`write_file`、
`edit_file`、`glob`、`grep`、`execute`（`eino@v0.9.22/adk/middlewares/filesystem/filesystem.go:38-46`）。
它们同时也是暴露名，所以这里没有翻译，不像一个 MCP server 自己的词汇那样（§6.7）。

#### ★ 迁移（旧配置怎么办）

这次搬动是**破坏性的** —— 旧写法会**加载即报错**，不是静默忽略：

| 旧写法 | 现在 | 症状 |
|---|---|---|
| `server/config.yaml` 里的 `tools:` 段 | 拆成 `tools/http.yaml` `tools/web_search.yaml` `tools/wikipedia.yaml` | 启动报错 `field tools not found in type decl.Server` |
| `kb/config.yaml` | `tools/kb.yaml` | **静默** —— 知识库不再被读到，`kb_search` 消失但没有任何报错 |
| `policy/permissions.yaml` 里的 `ask:` | 档位住在**提供**工具的那些文件里：`tools/http.yaml` `tools/web_search.yaml` `tools/wikipedia.yaml` `tools/kb.yaml` 与 `mcps/<server>.yaml` 各自点名自己的 `allows:` | ★ **不静默**：退役的目录被记进 `deferred` 并在启动时打印 —— 「policy/ 没了，没有被读」，连同它的内容去了哪儿（`agentkit/spec/config.go:410`） |
| `policy/limits.yaml` | 上限住在 `server/config.yaml`：`agent.max_depth` 与 `retry.max_retries` | ★ **同一份提示** —— 而比上限更深的拓扑是一次**启动错误**，不是静默截断（`agentkit/spec/load.go:916` `validateDepth`） |
| `tools/kb.yaml` 里的 `corpus` 键 | `corpus_dir` | ★ **不静默**：旧键仍然能加载，启动时 `deferred` 里带着一条改名提示 —— ★ 在键被改名之前知识库照常工作，所以不会「黑掉」（`agentkit/spec/load.go:231-237`） |
| `agents/<id>.md` 里的 `disabled_tools:` | `tools:` —— ★ 意思**正好相反**：旧键说的是这个 agent **不拿**哪些工具，新键说的是它**拿**哪些 | ★ **启动即报错**，而且报错信息会说明（`agentkit/spec/load.go:96`）：「删掉 `disabled_tools`，把这个 agent 该有的工具列出来」。**没有别名**：别名会让旧键继续以旧意生效，或者 —— 更糟 —— 突然以新意生效 |
| 缺失的 `mcps:`（过去表示「全部已配置的 server」） | 一份显式清单 —— ★ **现在缺失表示不挂载任何 server** | ★ **不静默，而且会咬人**：agent 失去了它过去继承来的每一个 MCP 工具，工具表把它们报成被挡下。`skills:` 同理 —— 过去缺失表示全部技能，现在表示一个都没有 |
| 缺失的 `tools:`（过去表示「没有被 disable 的全部工具」） | 一份显式清单 —— ★ **现在缺失表示一个工具都没有** | ★ **不静默，而且咬得最狠**：一个没有 `tools:` 这一行的 agent 有一张**空**的工具表。`Table.Filter` 再也没有「无事可做」的捷径（`agentkit/build/policy.go:145`）—— 一份空的允许清单摘掉一切 |

★ 那条「静默」是这次搬动里最需要当心的一处：`kb/` 不在加载的分区清单里
（`agentkit/spec/config.go:40-70`），所以旧的 `kb/config.yaml` 根本不会被读：它那个 `corpus`
键（现在是 `corpus_dir` —— 见上面那张表）像是「没配」一样 —— 而语料为空 = 知识库关闭。症状是
「`kb_search` 没了」，但启动日志一个字都不说。
⇒ 迁移时**先 `mv` 再启动**，别指望启动报错提醒你。

★ **`policy/` 也一样** —— 一个留下来的 `policy/` 目录**不会被读**，但它**也不沉默**：
加载器把这个退役的分区记进 `deferred`，并说明它的内容去了哪儿
（`agentkit/spec/config.go:410-419` `noteRetiredDirs`），所以那条提示会在启动时打印出来。
★ 这个改动的方向是安全的那一侧：没人点名的工具**要点头**，而代码自己那份清单把读取器与
委派工具让开。但旧文件批出去的那些*豁免* —— 一个搜索后端、一个 HTTP GET —— 跟着它一起没了，
替代品是**提供**该工具的那个文件里的一行（§6.6）。

#### 字段全集

| 文件 | 字段 | 类型 | 缺省 | 说明 |
|---|---|---|---|---|
| `http.yaml` | `methods` | `[]string` | `[get]` | ★ **只给白名单里的方法**；post/put/delete 能改外部世界，要显式点名 |
| `http.yaml` | `headers` | `map[string]string` | `{}` | 每个请求都带的头 |
| `http.yaml` | `timeout` | `LenientDuration` | `30s` | 单个请求的上限 |
| `web_search.yaml` | `provider` | `string` | `duckduckgo` | 搜索后端；**未知值不静默降级** —— 整组不挂 + 日志出声（`agentkit/build/tools.go:64-66`） |
| `web_search.yaml` | `max_results` | `int` | `10` | 一次搜索最多返回几条 |
| `web_search.yaml` | `timeout` | `LenientDuration` | `60s` | 单次搜索的上限（搜索要翻页，比 HTTP 宽裕） |
| `web_search.yaml` | `region` | `string` | `""`→全球 | duckduckgo 的 `kl` 参数（`wt-wt` / `cn-zh` / …） |
| `wikipedia.yaml` | `top_k` | `int` | `3` | 返回几条 |
| `wikipedia.yaml` | `language` | `string` | `zh` | 维基的语言 |
| `wikipedia.yaml` | `timeout` | `LenientDuration` | `15s` | 单次检索的上限 |
| `wikipedia.yaml` | `base_url` | `string` | `""`→官方 | API 端点（自建镜像用） |
| `kb.yaml` | `corpus_dir` | `string` | `""`→关 | 语料目录，**相对项目根**；绝对路径被拒（索引与语料同生共死） |
| `kb.yaml` | `index` | `string` | `default` | 索引目录名，落在 `state/kb/<index>/` |
| `kb.yaml` | `model` | `string` | `""` | 向量化模型，`<provider>/<model>`；必须 `kind: embedding`；`corpus_dir` 非空时**必填** |
| `kb.yaml` | `query_instruction` | `string` | `""` | 只加在查询侧的指令前缀 |
| `kb.yaml` | `batch_size` | `int` | `10` | 一次向量化请求的条数上限 |
| `kb.yaml` | `max_batch_chars` | `int` | `1200` | 一批向量化文本的字符数上限 |
| `kb.yaml` | `top_k` | `int` | `8` | `kb_search` 默认召回条数 |
| `kb.yaml` | `splitter.headers` | `map[string]string` | `{}` | ★ 仍然解析，但**自 v2 起不再生效**（分段改由行扫描得出）—— `kb status` 会把这话讲出来 |
| `http.yaml` | `allows` | `[]string` | `[]` | ★ **不用点头**的那些名字 —— 用**这个文件自己的词汇**（`request_get`、`request_post`、…）。不写 = 这个文件提供的每个方法都要点头 |
| `web_search.yaml` | `allows` | `[]string` | `[]` | 同上：`duckduckgo_search` |
| `wikipedia.yaml` | `allows` | `[]string` | `[]` | 同上：`wikipedia_search` |
| `kb.yaml` | `allows` | `[]string` | `[]` | 同上：`kb_search` —— `kb_index` 要写索引，所以要点头 |
| `filesystem.yaml` | `allows` | `[]string` | `[]` | ★ 同上，而且是**加法**：`write_file` / `edit_file` / `execute` 在这里点名后就不用点头。它**没有别的键** —— 工具是中间件挂上的，所以除了档位没有可配的东西 |

★ **这些字段的缺省值住在读它们的 provider 里，不在 `decl` 里。** 声明结构体只带形状
（三个工具配置在 `agentkit/spec/decl/decl.go:331-349`，知识库在
`agentkit/spec/decl/decl.go:566-584`）；值在 provider 里（三个超时在
`agentkit/build/nettools.go:22-27`，搜索的缺省在 `agentkit/build/nettools.go:210-217`，维基的缺省在
`agentkit/build/nettools.go:246-253`），知识库的缺省在 `agentkit/spec/defaults.go:49-56`。出厂模板把
它们都写了出来（`agentkit/spec/write.go:280-322` `Skeleton`、`:334` `ToolTemplates`）—— 那是看清整个配置面最短的一条路。

★ 出厂模板里有两条注释跑在代码前面，两条都要让困惑的人买单：`web_search.yaml` 的模板提到还有第二个
provider `searxng`（`agentkit/templates/files/tools/web_search.yaml:3`），而代码只认 `duckduckgo` —— 写 `searxng` 会被当作
未知 provider 拒掉（`agentkit/build/nettools.go:204-208`）；`kb.yaml` 的模板说 `model` 留空就是纯
词法检索（`agentkit/templates/files/tools/kb.yaml:7`），而加载器会拒掉它：`kb.model is required when kb.corpus_dir is
set`（`agentkit/spec/load.go:897`）。

★ 文件名是**白名单**（`filesystem.yaml` / `http.yaml` / `kb.yaml` / `web_search.yaml` /
`wikipedia.yaml`）：未知文件名在**加载期**就是硬错误（`agentkit/spec/load.go:213`，那条消息由
`ToolFileNames` 拼出来）—— 写进去一个永远不生效的段落，症状是「我明明写了却不生效」，极难查。
★ 这份清单**只有一个**家：`toolHints`（`agentkit/spec/load.go:176`），加载器、写入门与两条错误
消息都读它，所以编辑器存不下一个加载器会拒掉的文件。

#### ★ 「连到外面去」的三类工具

`tools/` 下的三类工具与知识库有一个本质差别：**它们的输出会原样进模型上下文**。知识库召回的是自己
写的语料，而这里是别人写的网页 —— 于是「外部内容里的指令」也进了模型脑子。档位层
（`allow`/`ask`）管的是「能不能调这个工具」，看不见 URL、也看不见结果里的字。

所以这一族工具的规矩是（实现见 `agentkit/build/nettools.go`）：

| 规矩 | 为什么 |
|---|---|
| **默认全关** | 没配就一个都不挂 —— 挂一个永远搜不到东西的 `web_search` 比不挂更糟。就这一族而言，开关是「**那个文件在不在**」，没有 `enabled` 字段 ✅ |
| **只连公网** | 对 HTTP 工具来说 URL 是模型给的。能访问 `127.0.0.1` 的 HTTP 工具 = 模型能自己读本机配置、会话状态、云上元数据服务（`169.254.169.254`） |
| **输出有长度上限**（20000 字节） | 一个 10MB 的页面会把这一轮直接撑爆 —— 表现是「模型突然开始胡说」，不是报错 |
| **写方法要显式点名** | POST/PUT/DELETE 能改外部世界的状态，而模型不需要理由就能编出一个 POST |

文件不在时，每个 provider 都不挂任何工具（`agentkit/build/nettools.go:129-131`、
`agentkit/build/nettools.go:197-199`、`agentkit/build/nettools.go:243-245`），上限是
`defaultToolOutputBytes`（`agentkit/build/nettools.go:26`）。

「只连公网」是在**拨号那一刻**解析并判的，且连到**刚判过的那个 IP** ——
（`agentkit/build/nettools.go:48-76`）—— DNS 可以在两次解析之间换答案（rebinding），只在解析时判
等于给了绕过。

★ **维基那个工具是唯一不在拨号器后面的。** 只连公网的客户端守住的是模型能指到哪儿的两个工具：HTTP
工具与网页搜索。维基工具的端点来自 `wikipedia.yaml` 的 `base_url`，而 eino-ext 的维基工具自己建
HTTP 客户端 —— 没有可以注入的钩子（`agentkit/build/nettools.go:259-266`）。输出上限照样罩着它
（`agentkit/build/nettools.go:270`）。

★ **MCP 是这一族的第四类，而它更危险**：上面那张表限制的是「**工具**能碰到什么」，而 stdio 的 MCP
server 是 openheron 的**子进程** —— 它能写文件、能联网，「只连公网」那套限制在它身上不成立。
⇒ 给 MCP 工具定档靠的是**命名空间 + server 自己的文件**（§6.7），而不是给每个工具单独设一道网络规则。

#### ★ 启用了就能用：档位与细粒度控制

「写下 `tools/http.yaml`」本身就是那个决定 ✅ —— 工具被挂上了，不必再去任何地方
点名一次 ✅（同一件事说两遍）。★ 但「挂上了」不等于「放行」：档位是**另一个**问题，
它的答案是这个文件自己的 `allows:`。

| 谁来定 | 管什么 |
|---|---|
| ★ **provider**（`defaultToolRegistry`、`fsx.New`） | ★ **工具集本身** —— 到底哪些工具存在 |
| `tools/` 里的文件 | 那些工具的**参数**，以及**其中哪些不用点头**（`allows:`）。★ 对 MCP 它还承载 server 的定义 —— `tools:` 从 server 提供的工具里挑要暴露的 |
| ★ **代码**（`builtinAllowedTools`） | **缺省**的那些例外 —— 读取器、委派工具与技能读取器。文件往这份清单上加；只有代码能缩它（§6.3） |
| 每个 agent 的 `mcps:` | **这个 agent 挂载哪些 server**（§6.2） |
| 每个 agent 的 `tools:` | **挂载的那些工具里，这个 agent 可以用哪些** —— 那份允许清单（§6.2） |

★ 所以一个工具的档位是从**提供这个工具的那个文件**读的 —— 或者，对于没有文件的工具，是从
代码读的。★ **agent 自己的清单是另一个问题**，而且单独问：`Table.Blocked`
（`agentkit/build/policy.go:130`）回答它，而 `Policy.TierOf`
（`agentkit/build/policy.go:55`）回答档位。⇒ 两个答案从不合并，谁也不覆盖谁。

★ **兜底档是 `ask`** ✅ —— 没有任何 `allows:` 清单点名的工具，会打断这一轮、等人来点 ✅ ✅。
代码自己那份 allow 清单，就是让那些只读工具与框架自带的 `task` 不必每次都点头的东西 ✅。

★ 知识库（`kb.corpus_dir`）**不是**由文件在不在来开，而是由非空的 `corpus_dir` 来开
（`agentkit/build/kb.go:15`），它的两个工具和其它工具一样走档位：`kb_search` 在
`allows:` 里，而 `kb_index` —— 一个写操作 —— 默认要点头（`tools/kb.yaml`）。

### 6.7 `mcps/<server>.yaml`（★ 2026-11-06 决策，✅ 已实施）

MCP 服务器的配置。**一个 server 一个文件**：

```
.openheron/mcps/
└─ github.yaml
```

★ **它不进 `tools/`** —— §2 决策 4 是「一实体一文件」，而 MCP 的实体是**服务器**：
一个服务器有它自己的命令、参数、环境、传输方式与生命周期。`tools/` 的规则是
「一个文件 = 一个工具」（§6.6 的文件名白名单），把 N 个 server 塞进一个文件，就得在
一个「文件内容就是配置、不带外层包装键」的分区规则之下再套一层列表；而整份文件替换时
也说不清是哪个 server 变了。

★ 架构层面的完整设计（装配路径、客户端生命周期、与闸门的关系、沙箱里的落法）
见 `docs/mcp-design.md`。本节只管**配置面**。

★ **文件说 server 是什么；agent 说谁挂载它。** `mcps/<server>.yaml` 描述这个 server ——
它的命令、它的传输方式、它的档位。★ 哪些 agent 挂载它，看的是 agent 自己的 `mcps:` 清单
（§6.2）：**键缺失 = 不挂载任何 server**，`[]` = 不挂载任何 server。
⇒ 挂载要写**两处**：server 文件说这个 server *是什么*，agent 的 `mcps:` 说*谁启动它* ——
而第三处说这个 agent **看见**它的什么（`tools:`，§6.2）。★ 挂载一个 server 会起一个
**子进程**，所以它是要写下来的决定，绝不是缺省。

#### 字段全集

| 文件 | 字段 | 类型 | 缺省 | 说明 |
|---|---|---|---|---|
| `<server>.yaml` | `transport` | `string` | `stdio` | `stdio` \| `http`；★ **未知值硬报错**，不静默退回 stdio。★ `http` 只指**可流式 HTTP** —— 没有 SSE 传输 |
| | `command` | `string` | — | stdio 专用，**必填**：要执行的命令。★ 它在沙箱的 PATH 里解析，**绝不**对着项目根解析 |
| | `args` | `[]string` | `[]` | stdio 专用：它的参数 |
| | `env` | `map[string]string` | `{}` | **叠加**在继承来的环境之上的变量；★ 子进程的环境就是 openheron 自己的环境，这些覆盖同名项 —— 而这份继承正是把凭据的哨兵送到服务器、而这里一个字段都不用写的原因。★ 只属于 stdio：在 http server 上它是**加载期错误**，哪怕这个 map 是空的 |
| | `url` | `string` | — | http 专用，**必填**：服务器地址 |
| | `headers` | `map[string]string` | `{}` | http 专用：每次请求都带的头 |
| | `timeout` | `MCPDuration` | `30s` | 连接 + 初始化 + 取工具清单的上限 |
| | `call_timeout` | `MCPDuration` | `60s` | **一次工具调用**的上限 —— ★ 没有它，一个永不回答的服务器会占着调用锁，同 server 的其它调用全排在它后面 |
| | `tools` | `[]string` | `[]`→全部 | ★ **过滤器，不是开关** —— 这个 server 要提供的那些**原始**工具名，**从它自己有的那些里挑**（不是带命名空间的那个）；不写或写空 = 全部 |
| | `allows` | `[]string` | `[]` | 这个 server **自己的**工具名里**不用点头**的那些 —— ★ 这个 server 提供的其余一切都要点头，**包括它还没提供的那些工具** |

★ `timeout` 与 `call_timeout` 是**宽松**时长：写错退回默认并记 `deferred`，
  与 `debounce` / `start_timeout` 同理。写错 timeout 改变的是「等多久」，
  不是「会发生什么」—— 它不该让程序起不来。

★ ★ **类型是 `MCPDuration`，不是那个包级的宽松时长清单。** 别的键用的那份宽松时长清单
不带来源信息，所以一个坏值说不出是哪个文件、哪个键写错了
（`agentkit/spec/decl/decl.go:449`）。这是相对原设计的一次刻意偏离，也正是两个 MCP
超时有自己类型的原因。

★ `tools: []` 是**全部**而不是**一个都没有** —— 一个表示「什么也没有」的空白名单
  与「键缺失」分不出来。

#### 加载期错误

下面每一条都是**加载**失败，不是运行期失败 —— 文件会点名是哪一处写错了：

| 写错的地方 | 它说什么 |
|---|---|
| server 名不匹配 `^[a-z0-9]+(_[a-z0-9]+)*$` | 文件名**就是**server 名，而一个读不回来的名字会被拒（`agentkit/spec/load.go:336` `loadMCP`） |
| `transport:` 写了未知值 | 不静默退回 stdio —— 一个拼写错误不该跑起另一个 server，而不是文件指名的那个 |
| stdio server 没有 `command` | `command is required for a stdio server` |
| http server 没有 `url` | `url is required for an http server` |
| http server 声明了 `env:` | ★ **哪怕这个 map 是空的**：http server 是远端进程，本进程的环境不是它的环境，而一个不起作用的键比一次拒绝更糟（`agentkit/spec/load.go:457` `yamlHasKey` —— 解码器答不了这个问题，因为一个没有值的 `env:` 会解成 nil map） |
| 未知键 | 与其它分区同一套严格解码 |

★ **某个 agent 点名一个不存在的 server，也是加载期错误** —— 那一条属于 agent 的文件，
不属于 server 的（§6.2，`agentkit/spec/load.go:755` `validateMCP`）。

#### 热更新

★ **这个分区是热的。** 那个分类带着 `hot: true`
（`agentkit/spec/config.go:61-69`），于是 `hotDirList()`（`agentkit/spec/config.go:87`）
把 `mcps/` 放进 `HotDirs`（`agentkit/spec/reload.go:21`）。改一个 server 的命令、它的
超时**或它的档位**，下一轮就生效，不必重启。

★ **重载唯一要顾对的是客户端。** stdio 的子进程被拆掉、新的被拉起来；声明变了就换掉
那个客户端（`agentkit/build/mcpmanager.go:215` `ensure`），而工具清单变了就通过配置
重载器用的同一个钩子推进代际（`agentkit/build/builder.go:67`、`:105`）。
★ 一个资源重建不出来的热分区，就是一个谎称自己是热的分区。

#### 文件名就是命名空间

server 名 = `<server>.yaml` 去掉 `.yaml`，它同时是工具名的命名空间那一段：

```
mcp__<server>__<tool>
```

| 段 | 谁定 | 规则 |
|---|---|---|
| `server` | **我们定**（文件名） | 必须匹配 `^[a-z0-9]+(_[a-z0-9]+)*$`；不匹配 ⇒ **加载期硬错误** |
| `tool` | **服务器定** | `[A-Za-z0-9_-]` 之外的字符替换成 `_`；大小写保持原样 |

★ **server 名里不许有连续的下划线** —— 否则 `mcp__a__b__x` 读不回来
  （server `a` 的工具 `b__x`？还是 server `a__b` 的工具 `x`？），
  而 §6.3 的前缀语法正是按 `mcp__<server>__` 切的。

★ **命名空间同时解决了「撞保留工具名」**：暴露 `ls` 的 server 变成
  `mcp__<server>__ls`，不再与 `ls` / `execute` 那批保留名相撞
  （`agentkit/build/tools.go:46`）。反过来也成立，且是一条规则：
  **只有 MCP 工具可以用 `mcp__` 前缀**。

★ **合成名永远不被读回来** —— 档位层只做整名与前缀匹配，
  所以工具段能保留原始大小写与字符。名字只在一处合成
  （`agentkit/build/mcptools.go:140` `mcpExposedName`）。

★ **合成名超过 64 个字符会被跳过并给出警告**，一个 server 里两个清洗后同名的工具，
  后一个也一样（`agentkit/spec/decl/decl.go:434`、`agentkit/build/mcptools.go:27-28`）。
★ **绝不**是加载期失败：名字是远端服务器定的，而一个多出一个怪工具的 server
  该付出的代价是那个工具，不是整个 agent。

#### 文件定义 server

没有 `enabled` 字段。server 是**因为文件存在**而被**定义**的，删掉文件就是删掉定义。★ **server 存不存在**
只待在一个地方，不是拆成「文件在不在」和「里面的标志说开」两处。

★ **文件定义 server；它不决定 server 提供什么。** server 有哪些工具来自 server 自己，文件的 `tools:`
只是从里面挑值得暴露的（§6.7 的键表）。

#### 与档位表的衔接

★ **这一族工具的档位写在 server 自己的文件里**，用 server 自己的词汇 —— 典型写法：

```yaml
# .openheron/mcps/github.yaml
#
# ★ The default is ask, so an empty list means "every tool of this server asks".
#   A trusted server names its exceptions:
allows: [read_issue, list_issues]
```

★ 「空的 `allows:`」就是「这个 server 提供的每个工具都要点头」—— 给**一整类**外部工具定档，
不必先把工具名抄下来。

★ **而且它覆盖 server 还没提供的工具。** server 的工具清单只有运行期才知道，所以一条点名了
工具的规则覆盖不了 server 明天才加的工具。§6.3 的前缀语法对 `ask` 档永远做不到这件事 ——
它在那里只剩某个 agent 的 **`tools:`** 的用武之地（`mcp__*` = 这个 agent 一个 MCP
工具都看不见）。

★ **要把 server 里的某一个工具拿掉，由 server 自己的 `tools:` 清单把它漏掉** —— 用 server
  自己的词汇，因为那个决定就该在那里做：

```yaml
# .openheron/mcps/github.yaml
tools: [create_issue, list_issues]
```

★ 而 `allows:` 是同一个文件的另一半：它点名那些**不用点头**的。清单说*哪些*被提供，
allow 清单说*其中哪些*直接放行。

★ **档位在 server 还没起来之前就能回答。** 那张表是从**配置**装配出来的 —— 文件的
`allows:` 被合成成暴露名（`agentkit/build/mcptools.go:172` `mcpAllowedNames`）—— 所以闸门
的查表从不必等一次连接。★ 而命名空间的缺省是 `ask`
（`agentkit/build/policy.go:55` `TierOf`）。

★ **stdio server 的权限就是进程的权限**：MCP server 是 openheron 拉起的子进程，
  它能写文件、能联网。§6.6 给 HTTP 工具定的「只连公网」在它身上**不成立**
  （那条限制的是*工具*能碰到什么，不是*服务器*能做什么）。
  这是档位该属于 server 的文件、而不是一份共享列表的理由。

★ **一个 server 连不上时**：它的工具整个不挂 + 一条警告（带 server 名、
  传输方式与错误），**不影响其它 server，也不影响进程启动**。
  静默丢掉是最糟的结果 —— 症状会是「这个功能好像不见了」。

#### 凭据

★ **本节没有凭据字段，而这正是要点。** MCP server 需要的 key 是**服务器自己的**事 ——
  它知道该读哪个变量。沙箱运行时把**哨兵**注入容器的环境，
  而子进程的环境就是本进程的环境（`docs/mcp-design.md` §5.5），
  于是服务器自己就找到了它 —— 这里一个字都不用写。

★ **这个文件唯一带的环境相关的东西就是环境本身** —— `env:` 是点名 server 自己那些变量的
地方，而一个没设的 `${ENV}` 是**延后处理**而不是致命错误：那一项被略掉，server 带着继承
来的环境启动（`agentkit/spec/load.go:407` `expandMCPMaps`）。★ 一个缺值是一次降级，
不是让进程起不来的理由。

完整路径 —— 声明凭据的 mixin、换上真值的宿主侧代理、以及机制够不着的情况 ——
见 `docs/mcp-design.md` §8.1。

#### 模板

★ **配置编辑器提供脚手架。** `Template(spec.DirMCP, …)`（`agentkit/spec/write.go:280`）
写出一份**能加载**、并说明缺了什么的文件 —— `mcps/` 下的文件**定义**一个 server，所以一份
解析不了的文件不是「server 关着」，而是「配置加载不了」。写入门拒绝名字不是
`<server>.yaml`、或者 server 名不合法的文件（`agentkit/spec/write.go:185`）。

★ **那个例子是脚手架，不是要装的东西。** 它住在模板文件
`agentkit/templates/files/mcps/_example.yaml` —— ★ **开头那个下划线就是「不写下去」的
标记**：`openheron init` 写的是它旁边的三台服务器（`mcps/fetch.yaml`、`mcps/time.yaml`、
`mcps/deepwiki.yaml`），而这个文件只由配置编辑器当脚手架读（`agentkit/spec/write.go`）。
★ 两边在不同层，所以文件住在一个**两边都能依赖的叶子包**里 —— 拷贝会漂移，而拷贝正是
原先的依赖形状逼出来的。它的 `command` 是一个没装的占位符：server 起不来，日志里说一句，
agent 照跑。★ **把它复制成 `mcps/<server>.yaml` 并填上你自己的 command，就是定义那个
server 这个动作。**

#### 实施状态

★ **这一节已实施。** 分区、加载器、provider、档位表与编辑器的脚手架都在树上；用例在
`agentkit/spec/mcp_test.go`、`agentkit/build/mcptools_test.go`、
`agentkit/build/mcpmanager_test.go` 与 `agentkit/build/mcpstatus_test.go`（§11.7）。

★ **整个模型都已实施，它周边的东西也一样。** 全局档位清单没了（§6.3），缺省是 `ask`，
而例外由**提供**该工具的那个文件点名 —— 对 MCP 是 server 自己的文件（§6.7），其余的是
`tools/` 下的文件（§6.6）。★ 文件系统工具也有了自己的文件（`tools/filesystem.yaml`），
而它是一个**调音旋钮**：中间件不管那个文件在不在都把工具挂上，所以它只能放宽、不能开关（§6.6）。

## 7. AgentHub / ModelHub：eino 只给接口

已核实：`eino@v0.9.22/adk/middlewares/skill/skill.go:82-97` 提供 `TypedAgentHub` /
`TypedModelHub` 两个**接口**，eino 自己**全仓无实现** —— openheron 需自建。
✅ openheron 已经建好了：实现在 `agentkit/build/hub.go`。

```
SKILL.md frontmatter
  ├─ agent: <name>             → AgentHub.Get(ctx, name, opts)   eino@v0.9.22/adk/middlewares/skill/skill.go:527
  └─ model: <provider>/<model> → ModelHub.Get(ctx, name)        eino@v0.9.22/adk/middlewares/skill/skill.go:520
```

| Hub | 返回 | 何时需要 |
|---|---|---|
| `AgentHub` | `adk.TypedAgent[*schema.Message]` | `context: fork` / `fork_with_context` |
| `ModelHub` | `model.BaseModel[*schema.Message]` | frontmatter 声明了 `model:` |

**`ModelHub` 与 `agents/*.md` 共用同一套解析**：eino 传的 `name` 是 frontmatter 里的
原字符串，openheron 侧的 `Get` 内部按 `ParseRef`（`agentkit/spec/load.go:499`）拆成
`<provider>/<model>`，查 `Config.Models`（`agentkit/build/provider.go:58-73`）。这样
**「agent 用哪个模型」与「skill 用哪个模型」是同一套引用语法**，不需要用户记两套。

`AgentHub.Get(ctx, "", opts)` 必须返回**默认 agent** —— eino 明写「当 `skill.Agent` 为空时
调用 `Get("")`，实现应返回默认 agent」
（`eino@v0.9.22/adk/middlewares/skill/skill.go:146-147`）。

`adk.TypedResumableAgent[M]` 内嵌 `TypedAgent[M]`
（`eino@v0.9.22/adk/interface.go:481-485`），故 `deep.New` 的返回值
（`eino@v0.9.22/adk/prebuilt/deep/deep.go:171`）**直接满足** `AgentHub.Get` 的返回
类型，**无需适配层**。openheron 自己的装配路径走
`agentkit/build/assemble.go:39-41`。

#### 实现（`agentkit/build/hub.go`；`agentkit/build/skill.go` 只做接线，✅ 已实施）

| | 行为 |
|---|---|
| `ModelHub.Get(ref)` | 走 `llm.BuildModel`：解析引用 + 建实例 + **装请求级参数**（档位 / temperature / extra）。缓存按 hub 实例拆（= 一个配置代际），热更新后重新解析 |
| `AgentHub.Get("")` | 当前 agent 的池内实例（eino 的默认约定） |
| `AgentHub.Get(name)` | 本 agent **子树里声明过**的 name：按子 agent 的中间件链重新构造（过滤 → 审批 → 深度守卫），带自己的下一层拓扑；不缓存（`model:` 可能每次换模型） |
| `AgentHub.Get` 传未知 name | 报错并列出可用的名字 —— 不让「skill 写了 `agent: writer` 但 `writer` 不在这个 agent 下面」变成一个查不出的空结果 |
| `opts.Model` | 优先于 `decl.SubAgentSpec.ModelRef`：这是 eino 解析 `model:` 的产物，回头再按 ref 覆盖等于让那行配置失效 |

**★ 模型必须带请求级参数。** eino 的 `ModelHub` 返回值会**整个替换** agent 的模型
（`WrapModel`，`eino@v0.9.22/adk/middlewares/skill/skill.go:281-300`）。返回裸实例会让
「skill 换模型」顺带把推理档位与采样参数弄丢，而且不报错。openheron 在
`agentkit/build/provider.go:93` 把它们装上。

**★ 两个 hub 都要挂，不能只挂一个。** 缺 `ModelHub` 时 inline 模式下的 `model:` 被 eino
**静默忽略**（`eino@v0.9.22/adk/middlewares/skill/skill.go:282-284`）；缺 `AgentHub` 时
fork 模式直接失败（`eino@v0.9.22/adk/middlewares/skill/skill.go:511-513`）。前者正是
「配了没用」的典型。

★ **skill 的模型来自配置，而不是来自调用它的 agent。** hub 解析引用时传的是空的
`CallIntent`（`agentkit/build/hub.go:44`），所以档位回落到模型自己的 `reasoning_default`
（`agentkit/build/provider.go:97-103`）。skill 写的是*模型*，不是 agent；借用调用方
agent 的逐 agent temperature，会悄悄改掉 skill 自己那行 `model:` 的意思。

**注入方式**：`agent.newSkillHubs` 把**这一代快照**的 `snap.models` 绑进
`ModelHub`（`agentkit/build/hub.go:24-47`），每次解析都读同一代的 `snap.cfg`。
本仓库里**没有任何推送式入口** —— hub 与装配同代，不会出现「skill 用新配置、
agent 用旧配置」。

支撑这些说法的测试在 `agentkit/build/hub_test.go`：
`TestSkillModelHubCachesWithinAHub`（`:33`）、
`TestSkillModelHubReportsEmptyAndMissingResolver`（`:58`）、
`TestSkillAgentHubUnknownNameListsAvailable`（`:72`）、
`TestSkillAgentHubEmptyNameReturnsThePooledAgent`（`:86`）、
`TestSkillAgentHubForkBuildsNamedAgent`（`:103`）、
`TestSkillAgentHubPrefersSkillModelOverride`（`:129`）。

Agent 池与审批链见 `agent-design.md (upstream)`；agent 装配 API 见
`agent-design-v2.md (upstream)`。

---

## 8. 热更新（P1，✅ 已实施）

### 8.1 分区

| 目录 | 热更新 | 理由 |
|---|---|---|
| `llm/` `agents/` `tools/` | ✅ | 已在 `agentkit/spec/reload.go` 实施：`HotDirs` + 轮询 + 换代 |
| `mcps/` | ✅ | 已实施：那个分类带着 `hot: true`（`agentkit/spec/config.go:61-69`），于是 `hotDirList()`（`agentkit/spec/config.go:87`）把它放进 `HotDirs`（`agentkit/spec/reload.go:21`）—— 见 §6.7：改一个 server 的命令、它的超时**或它的档位**，下一轮就生效，不必重启 |
| `server/` | ❌ | 配进程自身（listen / 静态目录 / 上限 / `log_level` 与 `log.file`），改了要重启 |

★ 这条边界**有测试守着**（`TestWatchIgnoresServerDir`，
  `agentkit/spec/reload_test.go:233`）—— 改 `server/config.yaml` 不该触发换代。
  这份清单是**推导出来的**，不是写了两遍：`Categories`
  （`agentkit/spec/config.go:40-70`）带着 `hot` 标志，`hotDirList()`
  （`agentkit/spec/config.go:87`）把它变成 `HotDirs`（`agentkit/spec/reload.go:21`）。

★ **一个热分区必须能把资源重建出来。** `mcps/` 是第一个资源是**进程**的分区：快照换代时
stdio 子进程被拆掉、新的被拉起来，而工具清单变了就通过重载器用的同一个钩子推进代际
（§6.7）。★ 一个资源重建不出来的热分区，就是一个谎称自己是热的分区。

★ `tools/` 是自己独立的一个分区：写完 `tools/http.yaml`，
  下一轮就用新的方法白名单与超时，中间不必重启。★ **档位**也一样：档位表随快照一起重建，
  所以改过的 `allows:` 下一轮就生效（§6.6）。

**这个划分不是实现的便利，是语义**：一个改了就生效、另一个改了不算，混在一起会让人以为
「我改了 listen 怎么没反应」。

### 8.2 三个关键选择

| 选择 | 理由 |
|---|---|
| **stat 轮询**而非 inotify | inotify 在容器 / WSL / 网络文件系统里**会静默失效**（收不到事件且不报错）；轮询行为一致。代价是每秒几次 `stat` |
| **指纹用「文件名+大小+mtime」**而非内容哈希 | 每轮读全部文件是纯浪费，配置很小、mtime 足够精确。要避免的是**漏检**，所以目录名、文件名、大小、mtime 全部计入 |
| **防抖 200ms** | 保存大文件产生多条事件，要的是「保存完了」那一刻 |
| **失败不调 `onChange`** | 调用方是换代钩子，误调会让人以为换过了 |

防抖的缺省值是 200ms（`agentkit/spec/decl/decl.go:14`）；不是正数的值退回缺省
（`agentkit/spec/reload.go:73-78`）；轮询间隔是防抖的一半，下限 20ms
（`agentkit/spec/reload.go:146-149`）。

★ 防抖**只读一次**，读在 watcher 启动的时候（`agentkit/spec/reload.go:142-145`），
而 `watch` 住在 `server/` 下面（`agentkit/spec/decl/decl.go:532`）—— 那不是热分区。
所以改 `watch.debounce` 要重启；它不在热更新顺手带上的那批里。

### 8.3 首次加载与重载：两个入口

```text
NewReloader(svc, onChange)
  └─ Reload(): svc.Load() succeeds ⇒ publish and call back; fails ⇒ return the error (a failed first load stops the startup)

Reloader.Reload() (at runtime)
  ├─ svc.Load() succeeds ⇒ swap loaded + onChange
  └─ svc.Load() fails ⇒ keep loaded, return the error; Watch writes one log line
```

改坏配置**不让进程死**：`Reload` 失败时旧配置继续服务，`Watch` 只记一条日志
（`agentkit/spec/reload.go:220-222`）；只有**首次**加载失败会让进程起不来（那时没有
「旧配置」可用）—— `NewReloader` 返回错误（`agentkit/spec/reload.go:103-109`），
`setupConfig` 把它变成启动失败（`main.go:195-204`）。换代成功会连同版本号与变了哪些
分区一起记下来（`agentkit/spec/reload.go:215-218`）。

### 8.4 指纹与 `Config.Version`

`Service.Fingerprint()` 算出热更新目录的指纹，`Load()` 结束时写入 `Config.Version`
（`agentkit/spec/config.go:355`，在 `agentkit/spec/config.go:352` 的校验之后）。

**指纹怎么算的**：对 `HotDirs` 里的每个目录（都在配置根之下），喂进 sha256 的是

```
<the absolute path of the directory>\n
<file name> <size> <mtime in nanoseconds>\n     ← one line per file, the file names sorted explicitly
```

★ **用「名+大小+mtime」而非内容哈希**：配置很小，而每次轮询都读全部文件是纯
浪费；要避免的是**漏检**，所以目录名、文件名、大小、mtime 全部计入
（`agentkit/spec/reload.go:23-34` 与 `:51-71`）。目录不存在**不是错误**
（新项目就是空的），只是不贡献指纹。

**两个用途**

1. **热更新的变更检测** —— `Watch` 每轮比指纹，变了就进入防抖等待
   （`agentkit/spec/reload.go:151-181`）。
2. **「这次跑用的是哪份配置」的归因** —— 配置能在两个 turn 之间变化，指征必须按
   turn 记，否则调试会变成猜谜。它由 `/v2/health` 以 `configVersion` 暴露
   （`api/v2_introspect.go:449`）。

★ 指纹**不再**是 Agent Pool 的代际键：池需要可比大小的单调计数，而指纹是字符串。
代际由 `main.go` 的 `agentConfig`（`main.go:440-457`；管理器接口是
`build.ConfigManager`，`agentkit/build/manager.go:12-14`）在同一把锁内与配置一起发布，
见 `agent-design-v2.md (upstream)` §4.2。

### 8.5 换代的实际接线

```
spec.Reloader.Reload() succeeds
        │
        └─ onChange ──► agentConfig.set(cfg)        (cfg + gen are published under one lock, main.go:452)
                                │
                                ▼
                the next Builder.Build asks mgr.Current()   (agentkit/build/builder.go:75)
                                │
                                ├─ snapshotFor: the generation only moves forward (agentkit/build/snapshot.go:104-116)
                                │     ├─ spec / policy / table
                                │     ├─ models / tools
                                │     └─ a new heavyDeps (backend / tool set, built lazily, agentkit/build/snapshot.go:40-45)
                                ▼
                        pool.GetOrBuild(name, gen)   (agentkit/build/builder.go:105)
                          (the entry carries the generation; an old generation no longer hits, agentkit/build/pool.go:33)
```

★ **失败不回调**：`Service.Load` 的语法 / 模型引用 / 拓扑校验失败时
（`agentkit/spec/config.go:361`），`Reloader` 不调用 `onChange`，管理器保留旧一代
（`agentkit/spec/reload.go:111-127`）。因此不需要另写 `validateModels`，也不会出现
「接口报 A 代、运行跑 B 代」。

★ **没有推送式换绑**：本仓库根本没有 `Reconfigure` 这类入口。换配置 = 管理器换内容；
builder 下次问就是新的一代。在飞 turn 仍持有旧实例引用，跑完即被 GC 回收 ——
不需要排空信号。

★ **模型实例也随代际重建**：`heavyDepsOf` 的 `sync.Once` 属于快照
（`agentkit/build/snapshot.go:29-45`），新快照必然新实例。改 `llm/*.yaml` 或
`agents/main.md` 后管理器的代际 +1（`main.go:452-457`），池里那条记录的代际对不上了
（`agentkit/build/pool.go:33`），`/v2/agents` 与运行期同时看到新模型。

---

## 9. 依赖铁律

| 约束 | 原因 | 验证 |
|---|---|---|
| `agentkit/spec` **不 import** `agentkit/build` / `agentkit/run` / `api` | 配置不该知道谁消费它 | `go list -deps ./agentkit/spec \| grep -E "ouranoshong/openheron/(agentkit/build\|agentkit/run\|api)"` 应为空 |
| `agentkit/spec` **不 import** hertz / eino | 声明层只用标准库加 `yaml.v3`；模型只以 `model_ref` 字符串出现，实例在 agent 装配期解析（见 §12.1） | `go list -deps ./agentkit/spec \| grep -E "hertz\|cloudwego/eino"` 应为空 |
| `agentkit/run` **不 import** `agentkit/spec` | `agentkit/run` 的契约是 `AgentBuilder` 接口；配置是 `agentkit/build` 的实现细节 | `go list -deps ./agentkit/run \| grep "ouranoshong/openheron/agentkit/spec"` 应为空 |
| `agentkit/build` → `agentkit/spec` | 单向 | — |
| `llm/common` **没有任何 openheron 依赖** | 它是叶子包：在这里 import `obs` 会破坏「协议层不依赖运行时」这条规则 —— 请求日志搬到了 `agentkit/obs/httplog`，这个叶子包一行日志代码都没有 | `go list -deps ./llm/common \| grep '^github.com/ouranoshong/openheron/'` 应只打印 `llm/common` 自身 |
| `agentkit/spec` → `persist` | 唯一的落盘原语 | `go list -deps ./agentkit/spec \| grep '^github.com/ouranoshong/openheron/'` 应只打印 `agentkit/obs`、`agentkit/spec/decl`、`persist` 与 `agentkit/spec` 自身 |

★ openheron 有**两条**依赖铁律，而且它们不是同一条：第一条是「`agentkit/spec` 不碰
eino」（声明层保持框架无关），第二条是「`llm/common` 零 openheron 依赖」（厂商协议层
保持运行时无关）。两条都在 [architecture.md](architecture.md) §2.1 里重述了一遍。

**今天这些命令实际打印什么**（在本仓库实测）：

```
$ go list -deps ./agentkit/spec | grep '^github.com/ouranoshong/openheron/'
github.com/ouranoshong/openheron/agentkit/obs
github.com/ouranoshong/openheron/agentkit/spec/decl
github.com/ouranoshong/openheron/persist
github.com/ouranoshong/openheron/agentkit/spec

$ go list -deps ./llm/common | grep '^github.com/ouranoshong/openheron/'
github.com/ouranoshong/openheron/llm/common

$ go list -deps ./agentkit/spec | grep -E "hertz|cloudwego/eino"        # empty
$ go list -deps ./agentkit/run  | grep "ouranoshong/openheron/agentkit/spec"   # empty
$ go list -deps ./agentkit/spec | grep -E "ouranoshong/openheron/(agentkit/build|agentkit/run|api)"   # empty
```

★ 与上游设计相比多了**两个**依赖：`agentkit/obs`（日志的**装配处** —— `spec` 经它打印
热更新结果；它**造** logger，而 `spec` 接收注入进来的那一个）与 `agentkit/spec/decl`
（承载 yaml 标签的包；即「声明与行为分离」那条
规则，见 [architecture.md](architecture.md) §2.1）。两者都不牵进框架：`decl` 一个本仓
的包都不 import。

**为什么 `agentkit/spec` 不进 `agentkit/run`**：若 `Runtime` 直接读配置，配置就成了运行
时的一部分，`agentkit/run` 单测必须准备配置文件。保持接口反转注入，测试可注入假实现。

### 9.1 ~~破例~~：`agentkit/spec` → `agentkit/run` 曾经成立 —— 上游已撤销，openheron 直接继承了撤销后的形态

**这段历史留着，因为它是「为什么现在是这个结构」的依据。**

在上游，写配置与写 checkpoint 都需要「读者永远看不到写了一半的内容」。
`storage/filestore/checkpoint.go` 原来自带一份 `writeAtomic`；S1 在
`internal/atomicfile` 抽出了共用实现；2026-10 决定并把它挪回 `agentkit/run`，于是
`agentkit/spec` 一度 import `agentkit/run`。

★ **openheron 从来没有过这个破例。** 代码面是在撤销之后才抽出来的，在最初那一次提交
里 `agentkit/spec` 提到 `agentkit/run` 的地方**只有一条注释** —— 依赖从来没有存在过：

```
$ git grep -n "agentkit/run" fa7131a -- agentkit/spec
fa7131a:agentkit/spec/config.go:26:// 谁消费它由消费方决定 —— agentkit/run 尤其不能依赖它（否则单测要准备配置文件）。
```

★ **上游点名的那些包，在这里换了名字**：

| 上游 | openheron |
|---|---|
| `internal/atomicfile` | `persist/atomicfile.go` —— 一个**顶层**叶子包（openheron 没有 `internal/` 目录） |
| `storage/filestore/checkpoint.go` | 仍是 `storage/filestore/checkpoint.go`，它调用 `persist.AtomicWrite`（`storage/filestore/checkpoint.go:232`） |
| `agentkit/run` | 仍是 `agentkit/run` |
| `heron/persist` | `persist`（顶层） |

★ **结论没有变**：原子写与文件锁这族原语住在一个叶子包 `persist` 里，它只依赖 `fmt` /
`os` / `path/filepath`，不含任何运行时概念（`persist/atomicfile.go:3`）；
`agentkit/spec`、`kb` 与 `storage/filestore` 各自依赖 `persist`，反向依赖不存在。

★ **随之而来的那条规则**：`agentkit/spec` 里**只准**用 `persist` 的**一个**符号 ——
`persist.AtomicWrite`。用上 `Runtime` / `Session` / `Turn` 才是真的破 —— 那会让配置知道
谁消费它。下面这条 grep 就是为了让这条规则漂成一次 diff 冲突，而不是悄悄烂掉：

```
$ grep -oP 'persist\.[A-Z]\w*' agentkit/spec/*.go | sort -u
agentkit/spec/write.go:persist.AtomicWrite
```

★ 同一条规则现在也管住 `kb`：**只准用持久化原语**（`persist.AtomicWrite` /
`persist.FileLock` 一族）—— `kb` 只碰了 `persist.ErrLocked`、`persist.FileLock`、
`persist.OpenFileLock` 与 `persist.RunLockPath`，别的什么也没碰。

---

## 10. 不变量

| # | 不变量 | 违反后果 |
|---|---|---|
| 1 | **不做字段级合并** | 配置来源不可辨，「为什么用了这个值」无解 |
| 2 | 相对路径按**写明的**基准解析 —— 而且基准有两个 | 换个 cwd 启动就换了数据目录 |
| 3 | 密钥不入库，只存 `${ENV}` / `@file:` | 仓库泄密 |
| 4 | 未知字段必报错 | 拼写错误静默失效 |
| 5 | `deferred` 必须被打印 | 「配了没用」不可见 = 静默忽略 |
| 6 | **没有** `extra` × provider 能力矩阵 | 端点不认识的字段会变成请求期的厂商报错，而不是启动期报错 |
| 7 | 缺失的 `.openheron/` 不是错误 —— 但缺失**默认 agent** 是 | 新项目无法启动 |
| 8 | **模型引用必须带 `<provider>/` 前缀，且必须解析得到** | 拼错的名字静默退化到别的模型 ⇒「为什么这么贵」查不出 |
| 9 | 模型名**请求级**覆盖，**每 agent 一个实例，不是每模型一个** | 每模型各建实例 ⇒ 无谓的构造开销 |
| 10 | ✅ **已实施** MCP 工具一律带 `mcp__` 命名空间，且**只有** MCP 工具能用这个前缀 | 没有命名空间，一个暴露 `ls` 的 server 会让整个 agent 起不来（撞上保留工具名即在加载期报错）；没有「只有 MCP 能用」这一半，内置工具就能占了这个命名空间，某个 agent 的 `tools:` 里的前缀规则会悄悄盖住它 |
| 11 | ✅ **已实施** 某个 agent 的 `tools` 只支持**尾部** `*` 的前缀模式，其余写法加载期报错 | 一条读起来与实际覆盖范围不同的规则，就是一次静默的错配 |
| 12 | ✅ **已实施** 档位写在**工具自己的文件与自己的词汇里** —— 文件的 `allows:` 点名哪些不用点头，而且**没有全局清单**能与它矛盾 | 一个工具的权限就是一处的一个事实；一个说 `post` 的文件就说 `allows: [post]`，不必在两套词汇之间来回翻译 |
| 13 | ✅ **已实施** —— 文件系统工具有自己的文件 `tools/filesystem.yaml`，而它是一个**调音旋钮**：`allows:` 点名它们的例外，而中间件不管文件在不在都把工具挂上 | ★ 这些工具是 agent 的核心能力，所以那个文件不能有权把它们关掉 —— 它也不能收窄，因为收窄是按 agent 的（§6.2）。★ 它的 `allows:` 是**加法**的，于是文件放宽档位、agent 的 `tools:` 收窄可见性：每一层一个机制（`decl.FilesystemCfg`、`agentkit/spec/load.go:299` `AllowedTools`） |
| 14 | ✅ **已实施** agent 的工具清单是**允许清单**，而且缺省是**没有** —— `skills` 与 `mcps` 同理 | 一个继承「除了这些以外都要」的 agent，离「跑了一个没人挑过的工具」只有一步配置之遥；★ 而旧键的两个意思**正好相反**，所以只改名字会把一份黑名单变成白名单、还不吭声 —— 于是旧键被拒绝（`agentkit/spec/load.go:96`） |

★ **第 10、11 两行已实施。** 命名空间只在一处合成
（`agentkit/build/mcptools.go:141`），前缀语法由 `decl.ParseToolNamePattern`
（`agentkit/spec/decl/decl.go:434`）解析、被加载器拒绝
（`agentkit/spec/load.go:123` `validateAgentTools`），那张表留着这些模式
（`agentkit/build/policy.go:100` `newTable`）并匹配它们
（`agentkit/build/policy.go:130` `Table.Blocked`）。
★ 已经**没有一份全局清单**让前缀语法归属于它 —— 它是唯一一处按前缀匹配名字的地方（§6.3）。

★ **第 12 行完整成立。** MCP 那一半：server 自己的文件用 server 自己的词汇点名它的例外，
而那张表是从配置装配出来的（`agentkit/build/mcptools.go:173` `mcpAllowedNames`）。
`tools/` 那一半：每个文件点名自己的例外（`agentkit/spec/load.go:299` `AllowedTools`）。
★ 而那些**没有自己的文件**的工具，正是代码能表达的例外
（`agentkit/build/tools.go:62` `builtinAllowedTools`）—— 这就是为什么缺省是
`ask`、而例外要被点名（§6.3）。

★ **第 14 行就是现在 agent 配置的整体形状。** `Table.Load`
（`agentkit/build/policy.go:85`）是一份允许清单，`Table.Blocked`
（`agentkit/build/policy.go:130`）问的是这份清单有没有点名那个工具，而
`Table.Filter`（`agentkit/build/policy.go:145`）**没有**「无事可做」的捷径 ——
空清单摘掉一切，因为黑名单负担得起的那个捷径，恰恰是允许清单不能有的那个。

不显眼的那几行，核实方式如下：

| # | 怎么核实的 |
|---|---|
| 1 | `decodeStrict` 逐文件解到**预填充**的结构体上（`agentkit/spec/config.go:553`），而整个进程只读**一个**配置根（`agentkit/spec/config.go:247`） |
| 2 | `server.data_dir` 按**配置根**解析（`agentkit/spec/config.go:314` `DataDir`）；`server.static_dir` 与 `kb.corpus_dir` 按**项目根**解析（`agentkit/spec/config.go:328` `ResolveDir`）。★ 两个基准，各自写明在 §6 —— 笼统写成「一律按项目根」对 `data_dir` 是错的 |
| 3 | 写入门拒绝字面量 `api_key`（`agentkit/spec/write.go:562` `checkSecret`） |
| 4 | `decodeStrict` 打开了 `yaml.Decoder.KnownFields`（`agentkit/spec/config.go:553`） |
| 5 | `main.go:206` 在加载时逐条打印；这些条目由 `Config.deferKey` 产生（`agentkit/spec/config.go:232`） |
| 6 | `llm/<provider>.yaml` 的 `type` 只检查非空（`agentkit/spec/load.go:30-31`），`extra` 直接交给 OpenAI 兼容的 ACL（`llm/common/option.go:20`）—— 这里只有**一种**端点方言，所以「不支持的 provider」根本不会发生 |
| 7 | **默认**的配置根不存在时会被创建（`agentkit/spec/config.go:281-288`）；而 `agents/main.md` 缺失**是**启动期错误（`agentkit/spec/load.go:481-483`） |
| 8 | `ParseRef`（`agentkit/spec/load.go:499`）与 `validate`（`agentkit/spec/load.go:558`） |
| 9 | `model.WithModel` 是一个请求级选项（`llm/common/request.go:32`）；实例缓存以 agent 名 + 代际为键（`agentkit/build/pool.go:31`） |
| 10 | 命名空间只在一处合成（`agentkit/build/mcptools.go:141` `mcpExposedName`），前缀与分隔符是常量（`agentkit/spec/decl/decl.go:401`、`:382`） |
| 11 | `decl.ParseToolNamePattern`（`agentkit/spec/decl/decl.go:434`）+ 加载器的拒绝（`agentkit/spec/load.go:123` `validateAgentTools`）+ 留着并匹配那些模式的那张表（`agentkit/build/policy.go:100` `newTable`、`:130` `Table.Blocked`） |
| 12 | MCP 那一半：`mcpAllowedNames`（`agentkit/build/mcptools.go:173`）；`tools/` 那一半：`AllowedTools`（`agentkit/spec/load.go:299`）；没有文件的工具：`builtinAllowedTools`（`agentkit/build/tools.go:62`）—— 以及缺省（`agentkit/build/policy.go:55` `TierOf`） |
| 14 | 三份清单都是裸切片，所以「键缺失」与「空清单」是同一件事（`agentkit/spec/decl/decl.go:104`、`:125`、`:135`）；旧键被拒绝（`agentkit/spec/load.go:96`）；清单是允许清单（`agentkit/build/policy.go:85` `Table.Load`），而 `Filter` 没有「无事可做」的捷径（`agentkit/build/policy.go:145`） |

---

## 11. 测试矩阵

★ 下表是**实际存在的用例**（`agentkit/spec` 包 **180 个** `Test` 函数），不是计划。从未
落地的用例已被删掉或换成真正覆盖同一行为的那个 —— 列一个不存在的测试比不列更糟。

### 11.1 分层与优先级

| 用例 | 断言 |
|---|---|
| `DefaultsSurviveWhenFileOmitsKeys` | 只写 `listen_addr` 时，`max_retries` / `debounce` / `lease_ttl` 仍保留默认（**预填充生效的直接体现**） |
| `TheOnlyFileInTheRootIsTheOneThatCounts` | 配置根里那份文件说了算，`models` 不与别处合并 |
| `MissingConfigDirectoryIsNotAnError` | 无 `.openheron/` 时加载不崩 —— 它是校验失败，而且错误**点名缺失的 `agents/main.md`** |
| `RelativePathsResolveAgainstProjectDir` | `resolvePath` 按**配置根**解析（`agentkit/spec/config.go:528`）；绝对路径原样。★ 这个函数今天**没有生产调用方**（`DataDir` 自己拼路径，`agentkit/spec/config.go:314`）—— 它钉的是规则，不是一条活的路径 |
| `ProjectRootAndResolveDir` | `ProjectRoot()` 是 `.openheron` 的父目录，与 `Root()` 不同；`ResolveDir` 相对项目根解析，绝对路径原样，空串原样 |
| `TheConfigRootIsNeverSearchedForUpwards` | ★ 配置根**不**上溯：从子目录出发不会命中祖先那份 `.openheron/`，根就是项目根自己的那个目录 |
| `AnExplicitRootWinsOverTheEnvironment` | 显式的 `--config-dir` 压过 `$OPENHERON_CONFIG_DIR`，而且它**不存在时不会被创建** |
| `TheEnvironmentWinsOverTheProjectRootDir` | `$OPENHERON_CONFIG_DIR` 压过项目根自己的那个目录，同样**不会被创建** |
| `AConfigurationThatNamesNoExceptionsAllowsNothing` | ★ 一份**不点名**任何例外的配置什么都不放行 —— 缺省是 `ask`，所以未列名就是要点头（`agentkit/spec/policy_contract_test.go:12`） |

### 11.2 取值与类型

| 用例 | 断言 |
|---|---|
| `ExpandEnvUnsetIsNonFatal` | `${VAR}` 未设 ⇒ **返回空值 + resolved=false，不报错** |
| `ExpandEnvSetResolves` | `${VAR}` 已设 ⇒ 去空白取值 + resolved=true |
| `ProviderMissingKeyIsReminderNotFatal` | 缺密钥的 provider 仍能加载；`Deferred()` 里有同时点名变量**与**后果的提醒（"401 Unauthorized"） |
| `ExpandAtFile` | `@file:` 读取并 TrimSpace；文件不存在报错 |
| `ExpandLiteralPassesThrough` | 字面量原样通过 |
| `DurationRequiresUnit` | `60`（缺单位）报错；`60s` 正确 |
| `TimeoutMissingUnitIsRejected` | `timeout: 300` 在**加载期**就被拒 |
| `DebounceFallsBackWithoutFailing` | `debounce` 写错**不让配置加载失败**，退回默认 |
| `UnknownFieldIsRejectedWithLineNumber` | 未知字段报错且点名 |

### 11.3 声明与图校验

| 用例 | 断言 |
|---|---|
| `AgentWithoutModelIsRejected` | 缺 `model` 报错（不继承） |
| `ModelRefWithoutProviderPrefixIsRejected` | 无 `<provider>/` 前缀报错 |
| `UnknownModelRefDoesNotFallBack` | 引用未声明的模型 ⇒ **那个 agent 留在树外**，树照常装得上，提示点名那个模型 —— **不报错，也不静默回退到别的模型**（§4.6） |
| `SubagentGraphIsAccepted` | 合法拓扑能加载 |
| `UnknownSubagentIsRejected` | `subagents` 引用不存在 ⇒ 报错并列出已声明的 |
| `SubagentGraphCycleIsRejected` | 成环 ⇒ 报错并给出**环路径** |
| `DeclaredDepthOverLimitIsRejected` | 深度超限 ⇒ 报错并指向 `agent.max_depth` |
| `RaisingMaxDepthAcceptsDeeperGraph` | 调大上限后可加载 |
| `SubAgentSpecsMirrorTopology` | 规格树拓扑 + `tools` + 档位名 + `temperature` / `max_tokens` 都带上 |
| `OnlyTheNamedToolsRunWithoutANod` | ★ 文件用**自己的**词汇点名例外：`tools/http.yaml` 说 `allows: [request_get]`，于是 `request_get` 不用点头而 `request_post` 要点头（`agentkit/spec/policy_contract_test.go:53`） |
| `AgentBodyBecomesInstruction` | 正文成为 `Instruction` |
| `FrontmatterAndBodySplit` | frontmatter 切分；**正文原样保留**（含空行后的后续段落） |
| `FrontmatterUnterminatedIsError` | 缺结束分隔符报错 |
| `ModelReasoningVocabularyAndDefault` | `reasoning` 词汇表与 `reasoning_default` 正确解析；`LevelFields` 能取出档位字段 |
| `LegacyThinkingAndEffortAreRejected` | 旧名 `thinking` / `effort` 被**严格模式拒绝** —— 改名不留静默别名 |
| `AWriteThatWouldTakeAWorkingAgentOutIsRefused` | ★ 写入把 `main` 指向未声明的模型 ⇒ `ErrMergeRejected`，磁盘不动（`agentkit/spec/write_gate_agent_test.go:16`） |
| `ABatchThatWouldTakeAWorkingAgentOutIsRefused` | 批写同样被拒，整批一个字节都不落盘 |
| `ADeleteThatWouldTakeAWorkingAgentOutIsRefused` | 删掉 agent 引用的 provider 文件 ⇒ 同样被拒 |
| `TestDeletingAMountedServerNamesTheAgentThatMountsIt` | ★ 删除闸门点名**引用它的那个 agent 文件**与移除什么（§4.6） |
| `TestDeletingAMountedServerNamesEveryAgentThatMountsIt` | 两个引用者 ⇒ 两个都点名 |
| `TestDeletingAProviderNamesTheAgentsThatUseIt` | provider 同理，`model:` 一并点名 |
| `TestDeletingADelegatedAgentNamesItsParent` | 被委派的 agent 同理，`subagents:` 一并点名 |
| `TestDeletingTheDefaultAgentIsRefusedWithItsOwnReason` | ★ 默认 agent 单独一条拒绝，不靠装载错误说事 |
| `TestDeletingAnAgentNobodyNamesIsAllowed` | 没人引用的 agent 删得掉 |
| `TestTheUndeclaredServerErrorLandsOnTheAgentsFile` | ★ 装载期的错误落在**引用它的那个 agent 文件**上 |
| `AWriteThatBringsAnAgentIntoTheTreeIsAccepted` | 把 agent **带进**树里的改动放行 —— 闸门只管往外拿 |
| `AChangeUnrelatedToABrokenAgentIsAccepted` | 树里本来就有一个坏的 agent 时，无关改动放行（比的是 settle 后的树） |
| `DiagnosticsRefuseAChangeThatWouldTakeAWorkingAgentOut` | 诊断里也拦，且 issue 落在**被编辑的文件**上 |
| `TheFilesOwnProblemComesBeforeTheGate` | 本文件的问题排在前，闸门后果排在后 |

### 11.4 热更新

| 用例 | 断言 |
|---|---|
| `VersionChangesWithContent` | 内容变则 `Version` 变；不变则稳定 |
| `ServerDirIsNotHot` | ★ 改 `server/` **不触发**换代 |
| `ToolsDirIsHot` | ★ 改 `tools/` **触发**换代 |
| `WatchIgnoresServerDir` | 端到端：真的写一次 `server/config.yaml`，确认 `onChange` 不被调 |
| `ReloadFailureKeepsLastGood` | 坏配置后 `Current()` 仍是上一份；**且不调 `onChange`** |
| `NewReloaderFailsWhenFirstLoadFails` | 首屏失败即返回错误 |
| `WatchDetectsChangeAndSwaps` | 3 秒内发现变更并换代，新配置生效 |
| `WatchExitsOnContextCancel` | `Watch` 随 ctx 退出 |
| `WatchEnabledDistinguishesUnsetFromFalse` | 没写 = 开；显式 `false` = 关 |
| `LogReloadFailedIsSwappable` | 降级确实出了声 |
| `DeferredIsCollected` | 「配了不生效」进 `deferred`（此处：一个从未设置过的 `api_key` 环境变量） |

### 11.5 回归

★ **`go test ./...` 是绿的**：根模块里 **977** 条顶层用例，**20** 个包有测试。

★ **`go test -race ./...` 也是绿的** —— 它曾经不是，而检测器报出的那四处竞态值得写
下来，因为它们是同一个错误的不同形态：**锁就在那儿，只有一条路径没走它。**

| 包 | 竞的是什么 | 现在是什么 |
|---|---|---|
| `agentkit/run` | `Turn.fail` 写 turn 的失败原因（`agentkit/run/turn.go:217`），而另一个 goroutine 在读它 —— 失败这条路可以从事件循环、被抢占的那一轮、关闭路径与启动超时四处到达，而它们会重叠 | 一把互斥锁护住该字段，**原因保持原来的语义**：最后落地的那个失败才是 `Wait` 报告的，所以被抢占那条路仍然回答 `ErrSuperseded` |
| `api` | 读入一份 transcript 会改写会话的 id（`storage/filestore/transcript.go:70`），而另一个 goroutine 在读那个 id（`storage/filestore/session.go:171`）—— id **不是**不可变的，因为 transcript 的 header 才是权威 | 互斥锁移到结构体顶部，现在也护住身份字段；accessor 在锁内读它，id 在锁内读、磁盘操作在锁外做 |
| `kb` | store 在锁外写自己的状态（`kb/store.go:504`、`kb/store.go:694`），而一次检索在读它 —— 其中一处写的是**派生**值（catalog 文件的 stat），另一处写的是另一 goroutine 原地改的那张 map 的**浅拷贝** | stat 在加锁前读出、在锁内赋值；派生状态在读锁内取**深拷贝**快照，因为浅拷贝仍然共享那张 map |
| `agentkit/spec` | ★ 这个在**测试自己**里：捕获回调在 watcher 的 goroutine 上写「哪些分区变了」这个切片（`agentkit/spec/reload_test.go:305`），而测试 goroutine 在读它（`agentkit/spec/reload_test.go:322`）—— 两边都没有同步 | 通知改走 channel，测试在读之前先等 watcher 停下来，也在把包级钩子放回去之前等它停下来 —— 因为 watcher 也在读那个变量 |

★ 三个生产竞态各自都带着同一个后半句：**这把锁的范围从来没有被写下来过。** 放在结构体
中间的互斥锁读起来就是「它护下面那些字段」，而有一条代码路径就照这个读法行事了。
修的不只是漏掉的那把锁 —— 还有锁旁边那句「它是干什么的」。

### 11.6 ★ 已解析字段的生效状态

这一节是**给读配置的人看的**：哪些字段真的影响运行，哪些只是被认识。标 ❌ 的行就是
「配了不生效」——那正是 §4.3 说的最贵的一类问题；它们留在表里是为了在接线时有一个不会
漏的清单。

| 字段 | 解析 | 校验 | 真正生效 | 说明 |
|---|---|---|---|---|
| `agents[].temperature` | ✅ | ✅ | ✅ | 与 `max_tokens` 一起进 `RequestOptionsFor` 的合并链末端；同名的 `extra` 键会被删掉，避免被后写入的 extra 覆盖（`agentkit/build/provider.go:105`） |
| `agents[].max_tokens` | ✅ | ✅ | ✅ | 同上 |
| `agents[].reasoning` | ✅ | — | ✅ | ★ 但**档位不在词汇表时不报错，也没有任何报告** —— `LevelFields` 什么也没找到（`agentkit/spec/decl/decl.go:68`），思考参数就悄悄不生效。这是**唯一一个**「配了不生效、而且看不见」的残留（§12.1） |
| `models[].reasoning_default` | ✅ | — | ✅ | agent 没写 `reasoning` 时由 `RequestOptionsFor` 兜底（`agentkit/build/provider.go:100`） |
| `models[].reasoning_field` / `models[].reasoning_field_from` | ✅ | — | ✅ | 端点自己的字段名，以及值从哪里读（`agentkit/build/provider.go:146`） |
| `models[].context_window` | ✅ | ✅ | ✅ | ★ **openheron 把它接上了**：它是压缩阈值的分母 —— `SummarizeAt` 取窗口的一个百分比（`agentkit/spec/summarize.go:25`）。阈值属于 agent，窗口属于模型；模型不声明窗口 ⇒ 该 agent 的压缩不生效，这件事会进 `deferred`（`agentkit/spec/load.go:671`）。它也出现在自省响应里（`api/v2_introspect.go:265`） |
| `models[].kind` / `models[].dimensions` | ✅ | ✅ | ✅ | 只对知识库有意义：`tools/kb.yaml` 配了语料目录时，`kind: embedding` 与维度数会被校验（`agentkit/spec/load.go:694-698`） |
| `models[].summarize_at` | ✅ | — | ❌ | **已废弃**：阈值属于 agent，不属于模型。这个键仍被解析，好让旧配置继续能加载，并且它会进 `deferred`，并指向 agent 自己的文件（`agentkit/spec/load.go:63`） |
| `llm/<provider>.yaml` 的 `header:` | ✅ | — | ❌ | 被解析（`agentkit/spec/decl/decl.go:26`）却**从不被消费** —— `FromConfig` 没有把它交给端点（`agentkit/build/provider.go:36`）。全仓没有任何地方读它（§12.1） |
| `server.data_dir` | ✅ | ✅ | ✅ | 相对**配置根**解析（`state` → `<root>/.openheron/state`，`agentkit/spec/config.go:314`），绝对路径原样；目录不存在时创建 |
| `server.static_dir` | ✅ | ✅ | ✅ | 经 `Service.ResolveDir` 按**项目根**解析（`agentkit/spec/config.go:328`）—— 既不是 cwd，也不是配置根；目录不存在只打一条日志（`main.go:347`） |
| `server.lease_ttl` | ✅ | ✅ | ✅ | 一次运行的租约在无进展时能存活多久；它必须**长于 provider 自己的超时** —— provider 的超时结束的是一次卡死的调用，租约只需要比一次慢调用活得久（`agentkit/spec/decl/decl.go:310`） |
| `agents[].skills` | ✅ | ✅ | ✅ | 这个 agent 可用的技能白名单；★ **不写表示一个技能都没有**，而**写空列表**意思相同 —— 那个用来区分两者的指针没了（`agentkit/spec/decl/decl.go:125`） |
| `agents[].tools` | ✅ | ✅ | ✅ | 这个 agent 可用的工具白名单；★ **不写表示一个工具都没有**（`agentkit/spec/decl/decl.go:104`）。名字是暴露名；一个不存在的名字是一条**提示**，不是错误（`agentkit/spec/load.go:679` `noticeAgentTools`） |
| `agents[].mcps` | ✅ | ✅ | ✅ | 这个 agent 挂载的 server；★ **不写表示一个 server 都不挂载**（`agentkit/spec/decl/decl.go:135`）。`tools:` 里点名了一个这个 agent 没挂载的 server，是一次**加载期错误**（`agentkit/spec/load.go:771`） |
| `SKILL.md` 的 `agent:` / `model:` | ✅ | — | ✅ | 由 `agentkit/build` 包的 `ModelHub` / `AgentHub` 实现，见 §7。它们不是 openheron 配置项，而是 skill 自己的 frontmatter（`agentkit/build/hub.go:64`、`agentkit/build/hub.go:106`） |

**仍然不生效的**

- `models[].summarize_at`：已废弃。★ 它**不是**隐形的 —— 它会进 `deferred`，并把
  agent 自己的文件名点出来。
- `llm/<provider>.yaml` 的 `header:`：被解析，从不被消费，**也没有任何报告**。★ 这是配置层
  剩下的唯一一个「配了不生效、而且看不见」。

### 11.7 ★ MCP

★ 这些用例分布在四个文件里：`agentkit/spec/mcp_test.go`（加载器）、
`agentkit/build/mcptools_test.go`（名字与档位）、
`agentkit/build/mcpmanager_test.go`（客户端）与 `agentkit/build/mcpstatus_test.go`
（那个投影）。

| 用例 | 断言 |
|---|---|
| `TheMCPFileNameIsTheServerName` | 文件名**就是**server 名，同时是那个 server 的工具名的命名空间那一段 |
| `TheServerNameMustBeReadableBack` | 带连续下划线的名字被拒 |
| `AnUnknownTransportIsALoadTimeError` | `transport:` 里的拼写错误不会退回 stdio |
| `AStdioServerNeedsACommand` | 没有 `command` 的 stdio server 是加载期错误 |
| `AnHTTPServerNeedsAURL` | 没有 `url` 的 http server 是加载期错误 |
| `AnHTTPServerMayNotDeclareAnEnvironment` | http server 上的 `env:` 被拒 —— 它没有子进程可以把环境交给 |
| `AnEmptyEnvironmentOnAnHTTPServerIsStillRefused` | ★ 被拒的是这个键的**存在**，不是它的值：一个没有值的 `env:` 会解成 nil map，而「键在但是空的」必须与「键不在」分得开 |
| `AnUnknownKeyIsRefused` | 与其它分区同一套严格解码 |
| `TheTimeoutsHaveDefaults` | `timeout` 缺省 `30s`，`call_timeout` 缺省 `60s` |
| `ABadTimeoutDegradesAndIsNoticed` | 坏掉的超时退回缺省并进 `deferred` —— 它不让进程起不来 |
| `AnUnsetVariableInTheEnvironmentIsDeferredAndNotFatal` | 没设的 `${ENV}` 是一次降级，不是致命错误 |
| `ASetVariableInTheEnvironmentIsExpanded` | `${ENV}` 在加载期被展开 |
| `AnAgentNamingAServerThatDoesNotExistIsALoadTimeError` | 报错信息点名已声明的 server |
| `AnAbsentServerListMeansNoServerAtAll` | ★ 缺失的 `mcps:` **不挂载任何 server** —— 那个把「全部」与「一个都没有」分开的指针没了 |
| `AnEmptyServerListMeansNoServerAtAll` | ★ 而空清单真的把这个 agent 收窄到什么都不剩 |
| `ASubAgentsOwnListIsReadFromTheSubAgent` | 子 agent 的清单是它自己的，收窄的是**它自己**那张表 |
| `AWildcardThatIsNotAPrefixPatternIsALoadTimeError` | `mcp*foo` 看着像通配符而其实不是 |
| `ALoneWildcardIsALoadTimeError` | `tools: ["*"]` 是要写出来的一个决定，不是一个通配符 |
| `ATrailingStarIsAPrefixPattern` | `mcp__github__*` 是前缀模式 |
| `TheEditorAcceptsAFileUnderMCPS` | 编辑器的文件名白名单认得 `mcps/<server>.yaml` |
| `TheEditorRefusesAServerNameThatCouldNotBeReadBack` | 并且拒绝一个读不回来的名字 |
| `AnMCPNameIsTheServersOwnNameUnderTheNamespace` | 暴露名是 `mcp__<server>__<tool>` |
| `AnMCPNameReplacesTheCharactersTheModelRefuses` | 模型 function name 字符集之外的字符变成 `_` |
| `AnMCPNameKeepsTheServersOwnCase` | ★ 大小写保持原样：一份小写副本就是第二个要同步的名字 |
| `AnMCPNameAtTheBoundaryIsKept` / `AnMCPNameOverTheBoundaryIsSkipped` | 64 个字符的合成名留着；多一个字符则**跳过并给出警告**，绝不加载期失败 |
| `TwoToolsThatSanitizeAlikeLeaveTheSecondOneSkipped` | 撞名的代价是后一个工具，不是整个 agent |
| `TheFileAsksForItsOwnToolsAndNotTheExposedOnes` | 文件说的是 server 自己的词汇 |
| `AnAbsentToolListMeansEveryTool` | `tools:` 是过滤器，不是开关 |
| `EverythingAsksAndTheFileNamesTheExceptions` | 命名空间的缺省是 `ask`，例外由文件点名 |
| `AnEmptyAllowsListCoversTheToolsTheServerHasNotOfferedYet` | 那份 allow 清单是关于**服务器**的一句话，不是关于它眼下恰好有的那些名字 |
| `TheTierIsAnswerableWhileTheServerIsUnreachable` | ★ 档位表是从**配置**装配出来的，所以答案不必等一次连接 |
| `AnMCPNameNobodyNamesAsks` | ★ 已经**没有全局 deny** 了：没人点名的工具要点头，MCP 的名字也不例外（`agentkit/build/mcptools_test.go:166`） |
| `AnEmptyListBlocksEveryTool` | ★ 这份清单是允许清单：一份空清单挡下**每一个**工具，MCP 的也不例外 |
| `AListedToolIsNotBlocked` | 而被点名的工具不被挡下 —— 清单是精确的，所以清单没点名的同类工具仍然被挡 |
| `TheServerNarrowingDeniesTheNamespacePrefixOnly` | 一个 server 的前缀够不到另一个：server 名里不许出现连续的下划线 |
| `APrefixRuleReachesTheToolsOfOneServerOnly` | `mcp__github__*` 只够到那个 server 的工具 |
| `AWildcardPrefixReachesEveryMCPTool` | `mcp__*` 够到来自任何 server 的每一个 MCP 工具 —— 而且够不到任何不是 MCP 工具的东西 |
| `AnExactEntryReachesThatToolOnly` | 精确条目点名一个工具，它就够到那个工具，别的都不够 |
| `TwoCallersAskingForTheSameServerGetOneClient` | 一个 server 一个客户端，启动是 single-flight 的 |
| `ACrashedChildIsStartedAgainOnTheNextCall` | 崩掉的子进程在下次调用时被重新拉起 |
| `AChangedDeclarationReplacesTheClient` | 声明变了就换掉客户端 —— 这正是这个分区能热的原因 |
| `AFreshListingIsNotReListed` | 只有当上一份清单超过 30 秒时才重新取一次工具清单 |
| `AChangedToolListIsReportedByTheRefresh` | 清单变了就通过配置重载器用的同一个钩子推进代际 |
| `TheChildEnvironmentIsTheProcessesOwnWithTheFileOnTop` | 子进程的环境是本进程的环境，文件的 `env:` 叠在上面 |
| `TheCommandIsNeverResolvedAgainstTheProjectRoot` | ★ 命令在沙箱的 PATH 里解析，绝不对着项目根解析 |
| `AnUnreachableServerSaysSoAndKeepsTheOtherServersAlive` | 连不上的 server 只赔掉自己的工具，别的都不赔 |
| `EveryConfiguredServerAppearsExactlyOnce` | 那个投影把每个已配置的 server 恰好列一次 |
| `AServerTheAgentDoesNotUseIsUnusedAndHasNoToolRows` | ★ `unused` 的 server **没有工具行** —— `tools` 那个理由属于 agents 视图 |
| `AnUnreachableServerReportsTheErrorAndKeepsItsLastListing` | 状态里带着错误与最后一次已知的清单 |
| `TheAgentsViewReportsTheToolsListAsTheReason` | ★ 工具不在某个 agent 里，理由就是 **`tools`** —— 只有一个理由，而且它就是那个 agent 自己的清单 |
| `TheReasonIsTheToolRuleWhenTheServerIsThere` | 而 server **在**的时候，理由就是工具规则 |
| `ThePanelAsksTheManagerAndTheManagerStartsTheServer` | 那个面板**会启动**它问到的 server |
| `ThePanelShowsTheTierTheGateWouldUse` | 面板与闸门读到的是同一个档位 |

### 11.8 ★ 档位

★ 这些用例住在 `agentkit/spec/policy_contract_test.go`（配置侧）、
`agentkit/build/policy_test.go`（那张表）与 `agentkit/build/introspect_test.go`（投影）；
而 `agentkit/spec` 包自己负责的那两条在 §11.1 与 §11.3。

| 用例 | 断言 |
|---|---|
| `TheKnowledgeBaseNamesItsOwnExceptions` | `kb_search` 不用点头 —— 一个躲在审批后面的只读工具，是模型会学着不用的那种 —— 而 `kb_index` 要点头，因为建索引会往磁盘上的索引里写（`agentkit/spec/policy_contract_test.go:28`） |
| `ALeftOverPolicyDirectoryIsReported` | ★ 一个退役的分区**不**沉默：那条提示点名它，并说明它的内容去了哪儿（`agentkit/spec/config_test.go:392`） |
| `AnEmptyRetiredDirectoryIsNotReported` | 空目录里没有任何被丢掉的东西，所以它不会抛出一条用户无法处理的提示（`agentkit/spec/config_test.go:419`） |
| `PolicyTierOrder` | 在 allow 清单里的名字是 `allow`；不在的是 `ask`（`agentkit/build/policy_test.go:39`） |
| `PolicyFallsBackToAsk` | ★ 未列名的名字回落到 `ask` —— 与旧缺省正好相反（`agentkit/build/policy_test.go:54`） |
| `TheAllowListDecidesWhatTheAgentHas` | ★ agent 的 `tools:` 清单就是全部答案：它点名的工具不被挡，它没点名的不被放（`agentkit/build/policy_test.go:69`） |
| `TheToolsListIsScopedToItsOwnAgent` | 而且这份清单只作用于那个 agent（`agentkit/build/policy_test.go:106`） |
| `AnEmptyListRemovesEverything` | ★ 而一份空清单摘掉**一切** —— 允许清单里没有「无事可做」的捷径（`agentkit/build/policy_test.go:123`） |
| `ApprovalMiddlewareAsksEvenIfFilterMissed` | 即使过滤器没摘掉这个工具，闸门也仍然要点头（`agentkit/build/policy_test.go:159`） |
| `ApprovalMiddlewareLetsAllowTierThrough` | `allow` 档的工具不用点头就放行（`agentkit/build/policy_test.go:177`） |
| `VerdictOfFallsBackToAsk` | 未列名的工具是 `ask`，而且仍然够得着（`agentkit/build/introspect_test.go:64`） |

### 11.9 ★ 配置表面：文件树与 skills

配置编辑器不是「几个 YAML 表单」，而是**一棵文件树**；`skills/` 是它唯一的嵌套分区
（§6.5）。这一节的用例钉的就是这两件事。

| 用例 | 断言 |
|---|---|
| `TestCategoriesCoverTheSurfaceAndEveryLoaderIsComplete` | ★ 每个分区要么有 loader，要么是那个嵌套且无 loader 的 `skills` —— 表面与「加载器读什么」不再是同一个集合 |
| `TestASkillIsWrittenIntoItsOwnFolder` | 写 `skills/x/SKILL.md` 会建出文件夹，清单里回的是 `x/SKILL.md` |
| `TestDeletingASkillRemovesItsFolder` | 删掉之后空文件夹一起消失 —— 留下来就是谁都看不见、谁都删不掉的技能 |
| `TestASkillNameThatCannotBeReadBackIsRefused` | 空格、大写、连续下划线都被拒，消息说的是**名字** |
| `TestOnlySkillsIsTwoLevelsDeep` | 别的分区写深一层被拒（`agents/sub/main.md` 会被保存、然后永远不会被加载） |
| `TestSkillsWrittenWhereTheProcessDoesNotReadThemAreRefused` | ★ 运行时从别处读技能时，保存被拒，清单里该分区标只读并点名那个目录 |
| `TestSkillsAreWritableWhenTheProcessResolvedNone` | 反过来的那一面：一个目录都没找到时，写配置根下的 `skills/` 是**建起第一个技能**的唯一入口 |
| `TestSettingsManifestListsSkillsAsNestedItems` | 清单里 skills 是分区、路径两层；只有 `SKILL.md` 的文件夹才算一个技能 |
| `a row is indented under its folder`（浏览器） | 树：分区是标题、文件缩进在它下面，行的可访问名字仍是整条路径 |
| `a skill is editable › …`（浏览器） | 技能能打开、能保存；新建对话框问的是**技能名**，显示的是 `skills/<name>/SKILL.md` |
| `TestTheBuildFailureReachesTheWaiter` | ★ 装配失败到达等待方时**不是**启动超时，而是装配器自己的理由 |
| `TestAFailedBuildReleasesTheSession` | 装配失败也要释放会话 —— 否则它永远忙着 |
| `TestTheBuildFailureIsReadable` | 消息一字不改：哨兵只分类，不做前缀 |
| `TestARunThatCannotStartCarriesTheReason`（api） | ★ 客户端拿到 `agent_unavailable` 与那条原因，而不是 `turn_start_timeout` |
| `装不起来的 Agent 把原因说清楚`（浏览器） | 聊天里看得见原因，且**不含** timeout |

---

## 12. 不在本轮范围

| 项 | 理由 | 前置条件 |
|---|---|---|
| RAG / knowledgebase | **已由 `kb` 包实施**，见 [architecture.md](architecture.md) §9 | — |
| MCP / LSP 集成 | ✅ **MCP 那一半已实施** —— `mcps/` 分区、provider 与档位模型（§6.3、§6.6、§6.7）；架构见 [mcp-design.md](mcp-design.md)。LSP 那一半仍不在范围内 | — |
| 中间件参数外露（`middlewares/*.yaml`） | openheron 中间件固定，无参数外露需求 | — |
| 配置写入门（`.config.lock`） | **已被配置编辑器推翻** —— 那六条 `/v2/settings` 路由就是写入口（`api/v2_files.go:67`） | — |
| ~~`policy/limits.yaml` 里的上限~~ | **已退役** —— 上限只住在 `server/config.yaml`，见 §12.2 | — |

### 12.2 上限只住在 `server/config.yaml`

〔沿革〕上限以前有第二份文件 —— `policy/limits.yaml` —— 它只能**收紧**
`server/config.yaml` 里的值，而且是热更新的。它已经没了。下面这一节记的是**为什么**，
以及什么替代了它。

**决定**：上限是一个**进程级**设置。写它的地方只有一处，就是 `server/config.yaml`：

```yaml
agent:
  max_depth: 2      # 最大嵌套深度；根 agent 是第 0 层
retry:
  max_retries: 3    # 一次模型调用最多重试几次
```

★ **第二份文件为什么走了。** 它是同一个数字的第二份拷贝，而那个数字本该只有一处拥有者；
它换来的只有一件事：**不重启**就能收紧一个上限。这不值得多一个要去查的地方。★ 而且那个方向
本来就不该优化：放宽一个上限才该是深思熟虑的决定，而它本来就需要重启；收紧变快只是一种便利，
不是一条安全属性。

★ **「只能收紧」那条规则变成了什么。** 它已经不存在了，因为「两份拷贝怎么合并」这条规则
只在有两份拷贝时才需要。现在只有一份：`server/config.yaml` 里那个值**就是**上限。

★ **上限不进热更新。** `server/` 配的是进程自身（§6.4），所以改动**下一次启动**才生效。
这就是不要第二份拷贝的代价，而 `server/` 下其它每个键本来就在付同样的代价。

#### 拓扑超过上限 ⇒ 启动期报错

当声明的拓扑嵌套得比 `agent.max_depth` 更深时，**加载失败**
（`agentkit/spec/load.go:916` `validateDepth`）—— 它永远不会是运行时的静默截断。
「配了三层但实际只跑两层」是最难查的一类问题。

#### 落地清单

| 位置 | 它是什么 |
|---|---|
| `agentkit/spec/decl/decl.go` | `Server.Agent.MaxDepth` 与 `Server.Retry.MaxRetries` |
| `agentkit/spec/load.go:756` | `MaxDepth()` 读 server 里的值 —— 已经没有任何合并 |
| `agentkit/spec/load.go:980` | `MaxRetries()` 读 server 里的值 |
| `agentkit/spec/load.go:916` | `validateDepth` —— 声明出来的拓扑超过上限是一次**加载错误** |
| `agentkit/spec/config.go:410` | `noteRetiredDirs` —— 留着的 `policy/` 目录被记进 `deferred`，所以启动日志会说明它的内容去了哪儿 |
| `api/v2_introspect.go` | `/v2/config` 的 `server.maxRetries` / `server.maxDepth` 报的是**生效值** |

★ **server 仍然是模板与语义的唯一事实来源**：前端只知道「这个文件该长什么样」，而
「合并之后还成立吗」永远由 server 试合并一次来回答。

还有两个**刻意保留的环境变量**：`build.ConfigFromEnv` 是过渡形态（主路径走
`FromConfig`），而 `EINO_EXT_SKILLS_DIR` 被降级成 `.openheron/skills` 的一处覆盖。

### 12.1 已知待收敛项（★ 实施时暴露，均非阻塞）

| 项 | 现状 | 建议 |
|---|---|---|
| ~~**业务热更新未完整接线**~~ | ✅ 已闭环：管理器换内容 → 下次 `Build` 拉当前代际 → 快照/池自然换代；不再有 `Reconfigure` 这条人工推送线 | 见 `main.go:204`（重载回调换掉管理器的内容）与 `agentkit/build/pool.go:31`（pool 以 agent 名 + 代际为键） |
| ~~**每 agent 一个模型实例**~~ | ✅ 非缺陷，是设计：实例粒度 = provider × model × reasoning 档位 × 端点字段形状 × timeout，链头是 agent。跨 agent 共享需要一份以这五项为键的缓存 = 第二真相 | 见 §6，以及 §2 里「模型构造」那一行 |
| ~~**`agentkit/spec` 依赖 eino**~~ | ✅ **已消除**：配置只声明模型引用；模型实例在 agent 装配期按引用解析。`agentkit/spec` 重新成为纯声明层 | `go list -deps ./agentkit/spec \| grep -c cloudwego/eino` = **0** |
| 模型没有声明的思考档位 | agent 写了一个不在 `llm/<provider>.yaml` 的 `reasoning:` 词汇表里的档位名：不报错，也**没有 `deferred` 条目** —— `LevelFields` 什么也没找到（`agentkit/spec/decl/decl.go:68`） | 剩下的唯一一个「配了不生效、而且看不见」；词汇表在 `llm/`，档位名在 `agents/`，两个文件从不互相核对 |
| `llm/<provider>.yaml` 的 `header:` | 被解析，从不被消费（`agentkit/spec/decl/decl.go:26`） | 要么把它交给端点，要么把这个键从文件规格里去掉 |
| 已解析未消费的字段 | 见 §11.6 | 按需接线 |

### 实施补充的两处偏离

| 偏离 | 原设计 | 实际 | 原因 |
|---|---|---|---|
| `watch.debounce` 的时长类型 | 与 `timeout` 共用一个 `Duration`（一律报错） | 拆成 `LenientDuration`（写错降级 + 记 deferred） | 两者性质不同：`timeout` 写错会让请求超时行为不可预测，**必须当场报错**；`debounce` 只影响「多久算保存完了」，不该让程序起不来。**降级必须出声**，所以记进 `deferred`。★ 在 openheron 里 `LenientDuration` 不只管 `debounce`，还管 `start_timeout`、`lease_ttl` 以及三个 `tools/` 分区的超时；严格的 `Duration` 只留给 provider 与 model 的 `timeout`（`agentkit/spec/decl/decl.go:28-55`） |
| `models[].reasoning` 词汇表 | 原文档未给具体字段 | 已实施：档位名 → 端点字段形状，`reasoning_default` 为默认档 | agent 写了一个该模型没有的档位**不拦启动**；★ 但 openheron 既不报告它，也不把它记进 `deferred` —— 那是 §12.1 里的未决项，不是一个已解决的问题 |

---

## 附录 A：核实记录

| 断言 | 核实方式 |
|---|---|
| `agentkit/spec` 不依赖 `agentkit/build` / `agentkit/run` / `api` | `go list -deps ./agentkit/spec \| grep -E "ouranoshong/openheron/(agentkit/build\|agentkit/run\|api)"` → 空 |
| `agentkit/spec` 既不牵进 hertz 也不牵进 eino | `go list -deps ./agentkit/spec \| grep -E "hertz\|cloudwego/eino"` → 空；两者的计数都是 **0** |
| `agentkit/run` 不依赖 `agentkit/spec` | `go list -deps ./agentkit/run \| grep "ouranoshong/openheron/agentkit/spec"` → 空 |
| `llm/common` 零 openheron 依赖 | `go list -deps ./llm/common \| grep '^github.com/ouranoshong/openheron/'` → 只有 `llm/common` 自身 |
| `agentkit/spec` 的 openheron 依赖恰好是 `obs`、`spec/decl`、`persist` 与它自己 | `go list -deps ./agentkit/spec \| grep '^github.com/ouranoshong/openheron/'` → 上面写的那四行 |
| `agentkit/spec` 只用 `persist` 的一个符号 | `grep -oP 'persist\.[A-Z]\w*' agentkit/spec/*.go \| sort -u` → `agentkit/spec/write.go:persist.AtomicWrite` |
| `kb` 只碰持久化原语 | `grep -rhoP 'persist\.\w+' kb/ \| sort -u` → `persist.AtomicWrite`、`persist.ErrLocked`、`persist.FileLock`、`persist.OpenFileLock`、`persist.RunLockPath` |
| `persist` 是叶子包 | `persist/atomicfile.go:3` 的 import → `fmt` / `os` / `path/filepath` |
| checkpoint 用的是共用的原子写 | `storage/filestore/checkpoint.go:232` |
| 配置根不上溯 | 读 `NewService` 的注释（`agentkit/spec/config.go:213`）+ 用例 `TestTheConfigRootIsNeverSearchedForUpwards` 与 `TestNewServiceDoesNotProbeUpwards` |
| `agentkit/spec` 不牵进任何框架 —— 只有标准库加 `yaml.v3` | `agentkit/spec/config.go:3` 的 import |
| 未知字段报错 | `decodeStrict` 打开 `yaml.Decoder.KnownFields`（`agentkit/spec/config.go:553`） |
| `deferred` 列表在启动时被打印 | `main.go:206` |
| `agentkit/spec` 的顶层用例数 | `grep -rhoP '^func \KTest\w+' agentkit/spec/ \| sort -u \| wc -l` → **180** |
| 全仓顶层用例数 | `grep -rhoP '^func \KTest\w+' --include='*_test.go' . \| sort -u \| wc -l` → **1014**，其中一条属于嵌套模块 `sandbox-kit/examples/demo-project` ⇒ 根模块里是 **1013** |
| 根模块里有测试的包数 | `go list -f '{{.ImportPath}} {{len .TestGoFiles}} {{len .XTestGoFiles}}' ./... \| awk '$2+$3>0' \| wc -l` → **21** |
| 整个测试套件是绿的 | `go test ./...` → 每个包都是 `ok` |
| `-race` 现在是绿的 | `go test -race ./...` → 每个包都是 `ok`；修复过程见 §11.5 |
| 上限只来自 `server/config.yaml`，没有别处 | `MaxDepth()` / `MaxRetries()`（`agentkit/spec/load.go:756`、`:679`）+ `agentkit/spec/config_test.go` 里的用例 |
| 遗留的 `policy/` 目录会被报出来 | `noteRetiredDirs`（`agentkit/spec/config.go:410`）+ `TestALeftOverPolicyDirectoryIsReported` 与 `TestAnEmptyRetiredDirectoryIsNotReported` |
| `models[].context_window` 真的生效 | `SummarizeAt`（`agentkit/spec/summarize.go:25`）+ `TestSummarizeAtResolvesAgainstTheWindow` |
| `models[].summarize_at` 已废弃但仍被解析 | `decl.Model.SummarizeAt` 上的注释（`agentkit/spec/decl/decl.go:46`）+ 那条 `deferred`（`agentkit/spec/load.go:63`）+ `TestTheOldModelThresholdIsReportedNotFatal` |
| `llm/<provider>.yaml` 的 `header:` 没有消费方 | `decl.Provider.Header`（`agentkit/spec/decl/decl.go:26`）与 `FromConfig`（`agentkit/build/provider.go:36`）—— 全仓没有任何地方读这个字段 |
| 模型实例是每 agent 一个，不是每模型一个 | pool 的键（`agentkit/build/pool.go:31` `GetOrBuild(ctx, name, gen, fn)`） |
| `AgentHub` / `ModelHub` 已实施 | `agentkit/build/hub.go:64`（`skillModelHub`）与 `agentkit/build/hub.go:106`（`skillAgentHub`） |
| `ConfigFromEnv` 住在 `agentkit/build` | `agentkit/build/config.go:15` |
| `EINO_EXT_SKILLS_DIR` 只是一个兜底 | `ResolveSkillsDir`（`agentkit/build/skill.go:184`） |
| 那六条 `/v2/settings` 路由 | `api/v2_files.go:67` |
| manifest 只在一处装配，在 `spec` 里 | `(s *Service) Manifest`（`agentkit/spec/manifest.go:68`）与只负责序列化它的 handler（`api/v2_settings.go:91`） |
| 校验回的是一组带行号的 issue | `Issue`（`agentkit/spec/diag.go:26`）、`(s *Service) Diagnostics`（`agentkit/spec/diag.go:71`） |
| 行号能穿过 frontmatter 块 | `parseShift`（`agentkit/spec/diag.go:188`）+ `splitFrontmatter` 返回的起始行（`agentkit/spec/expand.go:48`） |
| 编辑器问服务端，而不是自己校验 | `scheduleValidate`（`web/src/lib/settings-store.ts:240`）与它用的防抖（`:201`） |
| 前端优先按路径取模板 | `resolveTemplate`（`web/src/lib/settings-validate.ts:94`），由 `web/src/components/settings/new-file-dialog.tsx` 调用 |
| `context_window` 在自省响应里可见 | `api/v2_introspect.go:265` |
| `spec` → `run` 的破例在这里从未存在 | `git grep -n "agentkit/run" fa7131a -- agentkit/spec` → 一条注释，别无其它 |
| MCP 命名空间只在一处合成 | `mcpExposedName`（`agentkit/build/mcptools.go:140`）+ 前缀与分隔符常量（`agentkit/spec/decl/decl.go:364`、`:345`） |
| 某个 agent 的 `tools` 前缀模式 | `decl.ParseToolNamePattern`（`agentkit/spec/decl/decl.go:434`）+ 加载器的拒绝（`agentkit/spec/load.go:123` `validateAgentTools`）+ 留着并匹配那些模式的那张表（`agentkit/build/policy.go:100` `newTable`、`:130` `Table.Blocked`） |
| 旧的 `disabled_tools` 键被拒绝 | 加载器的拒绝（`agentkit/spec/load.go:96`）—— 这个字段存在，只是为了能抓住那个还在写它的文件（`agentkit/spec/decl/decl.go:113`） |
| agent 的三份清单缺省都是「没有」 | `decl.Agent.Tools` / `.Skills` / `.MCPServers` 都是裸切片（`agentkit/spec/decl/decl.go:104`、`:125`、`:135`）—— 「没声明」与「声明为空」现在是同一件事 |
| 不等 server 起来就能回答 MCP 档位 | `mcpAllowedNames`（`agentkit/build/mcptools.go:172`）+ 缺省档位（`agentkit/build/policy.go:55` `TierOf`）+ 用例 `TestTheTierIsAnswerableWhileTheServerIsUnreachable` |
| `mcps/` 分区是热的 | 那个 `mcps` 分类带着 `hot: true`（`agentkit/spec/config.go:61-69`）+ `hotDirList()`（`agentkit/spec/config.go:87`）→ `HotDirs`（`agentkit/spec/reload.go:21`） |

## 附录 B：实施状态

| 项 | 状态 | 位置 |
|---|---|---|
| 单根加载 | ✅ | `agentkit/spec/config.go:247` `NewService`，`agentkit/spec/config.go:363` `loadOnly` |
| 预填结构体逐层 decode（默认值存活） | ✅ | `agentkit/spec/config.go:377-379`（`defaultServer()` / `defaultPolicy()` / `defaultKB()` 预填） |
| 严格模式（未知字段带行号报错） | ✅ | `agentkit/spec/config.go:553` `decodeStrict` |
| `deferred` + 强制打印 | ✅ | `Config.Deferred()`（`agentkit/spec/config.go:197`），`main.go:206` 启动时逐条打 |
| 配置根三级解析（显式 → env → 项目根自己的目录） | ✅ | `agentkit/spec/config.go:213` |
| **不上溯** | ✅ | `NewService` 的注释（`agentkit/spec/config.go:250`）；`TestTheConfigRootIsNeverSearchedForUpwards` |
| `${ENV}` 缺则提醒（不阻塞）+ `@file:` | ✅ | `agentkit/spec/expand.go:9` `expandValue`，`agentkit/spec/load.go:43` |
| `Duration` 强制带单位 | ✅ | `agentkit/spec/decl/decl.go:612` |
| `LenientDuration` 降级并记 `deferred` | ✅ | `agentkit/spec/decl/decl.go:537`，`agentkit/spec/reload.go:80` |
| frontmatter 切分 | ✅ | `agentkit/spec/expand.go:48` `splitFrontmatter` |
| 模型引用校验（不静默退化） | ✅ | `agentkit/spec/load.go:499` `ParseRef`，`agentkit/spec/load.go:558` `validate` |
| `subagents` 图校验（缺引用 + 成环） | ✅ | `agentkit/spec/load.go:987` `validateAgentGraph`，`agentkit/spec/load.go:985` `detectCycle` |
| 缺省档是 `ask`，而文件的 `allows:` 是例外 | ✅ | `Policy.TierOf`（`agentkit/build/policy.go:55`）；例外来自 `builtinAllowedTools`（`agentkit/build/tools.go:61`）、`Config.AllowedTools`（`agentkit/spec/load.go:299`）与 `mcpAllowedNames`（`agentkit/build/mcptools.go:172`），由 `policyOf`（`agentkit/build/spec.go:64`）装配 |
| `envOr` → `cfg.*` | ✅ | `main.go:554` `override`（用在 `main.go:429`） |
| `.openheron/state/` 取代 `./data` | ✅ | `config.Service.DataDir("")`（`agentkit/spec/config.go:314`） |
| `MaxRetries` 配置化 | ✅ | `decl.RetryCfg.MaxRetries`（`agentkit/spec/decl/decl.go:562`）→ `Config.MaxRetries()`（`agentkit/spec/load.go:980`） |
| skills 自动探测 `.openheron/skills` | ✅ | `agentkit/build/skill.go:184` `ResolveSkillsDir` / `:208` `probeProjectSkills` |
| 写入门拒绝字面量密钥 | ✅ | `agentkit/spec/write.go:562` `checkSecret` |
| ~~`chatmodel.New` 直读 env~~ | ✅ 已删 | 包已移除，改 `llm/common` |
| `mcps/` 分区 + MCP provider | ✅ | 那个分区（`agentkit/spec/config.go:61-69`）、加载器（`agentkit/spec/load.go:336` `loadMCP`）、provider（`agentkit/build/tools.go:78`）与档位表（`agentkit/build/mcptools.go:172`）—— 配置面见本文件 §6.7 |
| 前缀匹配（尾部 `*`） | ✅ **对某个 agent 的 `tools` 而言** | 加载器的拒绝（`agentkit/spec/load.go:123` `validateAgentTools`）、留着那些模式的表（`agentkit/build/policy.go:100` `newTable`）与匹配它们的那张表（`agentkit/build/policy.go:130` `Table.Blocked`）—— 匹配语义见本文件 §6.3 |
| agent 的工具清单是允许清单 | ✅ | `Table.Load`（`agentkit/build/policy.go:85`）、`Table.Blocked`（`agentkit/build/policy.go:130`）、`Table.Filter`（`agentkit/build/policy.go:145`）—— 三份清单（`tools` `skills` `mcps`）缺省都是「没有」，而旧键被拒绝（`agentkit/spec/load.go:96`） |
| `server/config.yaml` 里的上限 | ✅ | `MaxDepth()` / `MaxRetries()` 读 server 里的值（`agentkit/spec/load.go:756`、`:679`）；拓扑超过上限是一次加载错误（`agentkit/spec/load.go:916` `validateDepth`）—— §12.2 |

**已落地**：`AgentHub` / `ModelHub`（§7）—— 实现在 `agentkit/build/hub.go`
（`skillAgentHub` / `skillModelHub`），由 `agentkit/build/skill.go` 接线；`context: fork`
的 `SKILL.md` 会触发它（用例见 `agentkit/build/hub_test.go`）。

另有两处**刻意保留的环境变量**：`build.ConfigFromEnv` 是过渡形态（主路径已走
`FromConfig`），`EINO_EXT_SKILLS_DIR` 降级为 `.openheron/skills` 的覆盖项。

**实施时补的一条**：`server/config.yaml` 新增 `agent.max_depth`（默认 1）。声明的拓扑
深度超过上限时**启动期报错**而非运行时截断 ——「配了三层但实际只跑两层」是最难查的一类
问题。

## 附录 C：把这篇文档落进 openheron 时发现并修正的若干处

| 上游的说法 | openheron 的实际 | 原因 |
|---|---|---|
| 配置根按「env → 上溯 → 自身」解析 | 它恰好按**三级**解析：显式 `--config-dir` → `$OPENHERON_CONFIG_DIR` → `<projectRoot>/.openheron`（不存在则创建），并且**刻意没有**上溯祖先目录（`agentkit/spec/config.go:213`） | 一旦配置根取决于「进程是从哪儿启动的」，同一条命令换个目录跑就会读到另一份配置 —— 测试实例也会读到开发者的那份（见 `AGENTS.md`） |
| 测试实例靠「预置一份 `.heron/`」来隔离 | 隔离靠 `.env.test` 加 `task test-env-backend` / `test-env-reset` / `test-env-sync` 三个目标：实例跑在 `:18099`，**自带配置根与自带会话目录** | 端口不同不等于数据隔离 —— openheron 把会话写在 `.openheron/state/` 下，一个写在那儿的测试会污染真实历史 |
| `agentkit/spec` 包有 **115** 个 `Test` 函数 | 它有 **180** 个 | 包随代码一起长大；上表列的是真正存在的用例 |
| 模块路径是 `heron/...` | 是 `github.com/ouranoshong/openheron/...` | `go.mod` |
| `agentkit/spec` 的 heron 依赖只有 `persist` | 有**四个**：`agentkit/obs`、`agentkit/spec/decl`、`persist` 与它自己 | `obs` 是日志的装配处；「声明」（`decl`）与「行为」（`spec`）的拆分是 openheron 自己的设计 —— 见 [architecture.md](architecture.md) §2.1 |
| [architecture.md](architecture.md) §2.2 说 `agentkit/spec` import `kb` | **生产代码**并没有 —— 只有用例文件 `agentkit/spec/policy_contract_test.go:7` 这么做 | 校验知识库配置只需要声明（`decl.Model.IsEmbedding`），不需要 `kb` 包本身；那份文档 §2 的依赖表把测试算进去了 |
| `llm.ConfigFromEnv` | 它是 `build.ConfigFromEnv`（`agentkit/build/config.go:15`） | 配置包不能依赖厂商协议层，而 `llm/common` 是零 openheron 依赖的叶子包 |
| 模型结构体 | openheron 的 `decl.Model` 带 `kind`、`dimensions` 与 `context_window`；`summarize_at` **存在但已废弃** —— 阈值属于 agent（`decl.Agent.SummarizeAt`） | 阈值是 agent 的策略，窗口是模型的 —— 两者只在一个地方相遇（`agentkit/spec/summarize.go:25`） |
| agent 结构体 | openheron 的 `decl.Agent` 把 `tools`、`skills` 与 `mcps` 都带成裸 `[]string`：**不写 = 一个都没有**，空列表也是没有 | 「没声明」与「声明为空」现在意思相同，所以那个用来区分它们的指针没了 —— 而它们共享的缺省就是**没有**（`agentkit/spec/decl/decl.go:104`、`:125`、`:135`） |
| server 结构体 | openheron 的 `decl.Server` 带 `lease_ttl`（还有 `frontend_mode`） | 租约的预算必须长于 provider 自己的超时，而两者住在不同的文件里 |
| `internal/atomicfile` | 是 `persist/atomicfile.go`，一个**顶层**叶子包 | openheron 没有 `internal/` 目录；`persist` 装着 `AtomicWrite` 与 `FileLock` 一族 |
| `kb` 不 import 任何 heron 包 | `kb` import 了 `obs` 与 `persist` | `obs` 是日志的装配处，知识库也需要那个共用的原子写 |
| `agentkit/spec` → `agentkit/run` 的破例在上游已于 2026-10-21 撤销 | openheron **从未有过** —— 代码面是在撤销之后抽出来的，`agentkit/spec` 提到 `agentkit/run` 的地方只有一条注释 | 撤销是上游的一个设计决定；openheron 直接继承了撤销后的形态 |
| `models[].context_window` 是纯元数据，不生效 | 它**生效**：它是压缩阈值的分母（`agentkit/spec/summarize.go:25`） | openheron 实施了压缩与上下文用量看板 —— 见 [context-usage-design.md](context-usage-design.md) |
| agent 写了模型没有声明的思考档位会进 `deferred` | 不会 —— 它被静默忽略 | 词汇表在 `llm/`，档位名在 `agents/`，两个文件从不互相核对 |
| `go test -race ./...` 全仓是绿的 | 它**曾经不是**：竞态检测器在 `agentkit/run`、`api`、`kb`、`agentkit/spec` 报了竞态 | `spec` 里那处在测试自己身上；四处现在都修好了，§11.5 逐条记着各自是什么 |
| `tools/` 里的坏超时会被报成 `watch.debounce` | 这个标签是错的：`collectBadDurations` 给**每一条**坏时长都加上前缀 "watch.debounce fell back to default"，于是 `tools/http.yaml` 里的坏 `timeout` 落到了同一个标签下 | `decl.BadDurations()` 是一个不带来源信息的包级列表（`agentkit/spec/decl/decl.go:528`） |

