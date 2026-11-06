# 配置系统设计说明

> 状态：本文描述的是 **openheron 当前的配置系统**。其中有两处是 **提案，尚未实现**：`mcps/` 分区（§6.7）以及策略表里的前缀匹配（§6.3）。只存在于上游仓库的文档标注为「upstream」。
> 范围：包含 —— 配置根、分区、文件规格、加载与优先级、热更新、依赖铁律。不包含 —— HTTP 接口层（见 `docs/api-v2-design.md`（upstream））与前端。
> 框架版本：eino `v0.9.22`（`go.mod:9`）、`gopkg.in/yaml.v3 v3.0.1`（`go.mod:20`）。

---

## 0. 一句话

**配置与运行数据分家**：配置是可读、可 review 的文件（要不要提交进版本库由你定），
运行数据一律不进库；厂商知识进 `llm/*.yaml`，Go 代码里零厂商知识；**只有一份配置生效**，让「这份配置到底是什么」一眼可辨。

★ 只有一份的理由不是整洁：一旦配置根取决于「进程是从哪儿启动的」，同一条命令换个目录跑就会读到另一份配置，
测试实例也会读到开发者自己的配置（`agentkit/spec/config.go:144-156`）。

---

## 1. 目标与非目标

### 目标

| # | 目标 | 可验证判据 |
|---|---|---|
| 1 | **一个**配置根（项目级路径，或显式指定） | 任一时刻只有一份配置生效，且没有继承链（`agentkit/spec/config.go:171-177`） |
| 2 | 按功能分区 | 新增一类配置不改已有文件布局（`agentkit/spec/config.go:40-66`） |
| 3 | 零新增依赖 | `go.mod` 只把已在缓存的 `gopkg.in/yaml.v3` 转 direct（`go.mod:20`）—— YAML 这一层就是全部成本 |
| 4 | 「配了没用」必须可见 | 未知字段报错；认识但未兑现的键进 `deferred` 并**强制打印**（`agentkit/spec/config.go:398-408`、`main.go:198-200`） |
| 5 | 密钥不入库 | 档案只存 `${ENV}` 引用或 `@file:` 路径 —— 配置编辑器拒绝保存明文（`agentkit/spec/expand.go:9-38`、`agentkit/spec/write.go:473-495`） |

### 非目标

- ❌ **配置根不做上溯搜索。** 上游设计会向祖先目录查找；openheron 故意不做（`agentkit/spec/config.go:144-156`）。
- ❌ **没有家目录那一层。** `~/.openheron` 永远不会被自动认领 —— 若它存在而不在用，运行时会把话说明白，请你显式指名（`main.go:109-126`）。
- ❌ **不让配置承载密钥本体。** 加载器容忍明文密钥（`agentkit/spec/expand.go:35-37`），但编辑器拒绝保存明文（`agentkit/spec/write.go:486-494`）—— 两条路径故意不对称。
- ❌ 不让 **`server/`** 参与热更新（它配的是进程自身，改了要重启）。`llm/` `agents/` `policy/` `tools/` **已实施**热更新，见 §8。

---

## 2. 决策基线

| # | 项 | 决策 | 理由 |
|---|---|---|---|
| 1 | 目录名 | `.openheron` | 短、无歧义、可入版本库（`agentkit/spec/config.go:95`） |
| 2 | 分区 | `server/` `llm/` `agents/` `policy/` `tools/`（会被加载）、`skills/`（技能）、`state/`（运行数据） | 一类配置一个文件集合（`agentkit/spec/config.go:18-26`、`agentkit/spec/config.go:40-66`） |
| 3 | 格式 | **YAML** —— `agents/*.md` 用 YAML frontmatter | 与 agents 的 frontmatter 同构 ⇒ `.openheron/` 全域统一（`agentkit/spec/expand.go:48-65`、`agentkit/spec/load.go:71-95`） |
| 4 | 粒度 | **一实体一文件** | `llm/<provider>.yaml`、`agents/<id>.md`（`agentkit/spec/load.go:16-17`、`agentkit/spec/load.go:71-72`） |
| 5 | 覆盖 | 不适用 —— 只有一份配置，没有「谁覆盖谁」 | 字段级合并让配置来源不可辨。★ 只有一处例外：深度与重试上限取**更严的**（`agentkit/spec/load.go:204`、`agentkit/spec/load.go:409-421`） |
| 6 | 优先级 | `默认 < 配置根 < 环境变量`（★ **只有少数几个变量真做到**，见 §4.4） | env 是一次性覆盖，文件是可提交的本项目管理（`main.go:217-219`） |
| 7 | 未知字段 | **严格报错**，带行号 | `max_retry` 拼成 `max_retries` 应在启动期暴露（`agentkit/spec/config.go:398-408`、`yaml.v3@v3.0.1/decode.go:944`） |
| 8 | 运行数据 | `.openheron/state/` | 配置与数据同根，一条忽略规则一处生效（`agentkit/spec/defaults.go:62`、`agentkit/spec/config.go:224-236`） |
| 9 | skills | 默认在 `.openheron/skills/` —— 由 `$EINO_EXT_SKILLS_DIR` 决定，否则从工作目录上溯找 `.openheron/skills` | ★ 这条规则与配置根**不同**：技能是一棵目录树，从上级目录复用一个无害，而配置根是一组实体（`agentkit/build/skill.go:188-227`） |
| 10 | 根探测 | `--config-dir` → `$OPENHERON_CONFIG_DIR` → `<projectRoot>/.openheron` | ★ **不做上溯搜索** —— 取决于工作目录的根会让同一条命令读到另一份配置（`agentkit/spec/config.go:144-156`） |
| 11 | 多模型 | 一个 provider 文件持 `models[]`，引用 `<provider>/<model>` | 「入口 agent 强模型 + 下属弱模型」是最常见诉求；且与 `agents/*.md` 的 `model:` 引用形式一致（`agentkit/spec/load.go:52-65`、`agentkit/spec/load.go:251-260`） |
| 12 | 模型构造 | **每个 agent 一个实例**（粒度 = provider × model × reasoning 档位 × 端点字段形状 × timeout） | ★ **实施时更正**：原写「一个 provider 一个实例」，理由是「连接池天然共享」。后半句不成立 —— `FromConfig` 每次新建 `http.Client`（`agentkit/build/provider.go:40`、`agentkit/build/provider.go:47`），而 agent 各自声明模型与思考档位，本就是彼此独立的（`agentkit/build/provider.go:81-95`） |
| 13 | 时长分两档 | `llm/*.yaml` 里的 `timeout` 严格报错；`debounce` / `start_timeout` / `tools/` 下的时长降级并记 `deferred` | 前者写错会让超时行为不可预测；后者只影响「等多久」，不该让程序起不来。**降级必须出声**（`agentkit/spec/decl/decl.go:27`、`agentkit/spec/decl/decl.go:325-334`、`agentkit/spec/reload.go:80-84`） |
| 14 | 子 agent 规格 | 放 `decl.SubAgentSpec`，装配层直接用 | `agentkit/build → agentkit/spec` 本就是允许的单向依赖；另立一份会「编译不报错、运行期少个值」（`agentkit/spec/decl/decl.go:412-430`、`agentkit/spec/subagent.go:18-28`、`agentkit/build/spec.go:22`） |
| 15 | MCP 工具命名 | 暴露给模型的名字一律加命名空间 `mcp__<server>__<tool>` | **提案，尚未实现**（见 §6.7）。MCP 工具名**由远端服务器定**：会撞保留工具名（`ls` / `execute`），也常带点号（`github.create_issue`）—— 后者过不了 provider 的 function name 字符集。命名空间把「撞名」变成「不可能」。树上唯一的痕迹是 `agentkit/build/tools.go:35` 的占位注释 |
| 16 | 策略匹配 | 某个 agent 的 `disabled_tools` 接受精确名 **+** 尾部 `*` 的前缀模式 | **提案，尚未实现**（见 §6.3）。档位搬进了工具自己的文件，于是仅剩的那句关于**命名空间**而不是关于某个工具的话是「这个 agent 不要任何 MCP 工具」—— 而前缀正是给命名空间起名的那个东西。今天的表是**精确匹配**（`agentkit/build/policy.go:39-47`、`agentkit/build/policy.go:94-105`） |
| 17 | 档位写在哪 | **跟着工具走**：`ask` 档住在工具自己的文件里（`tools/*.yaml`、`mcps/*.yaml`）；★ **不再有任何全局清单** —— `policy/permissions.yaml` 消失 | **提案 —— 未实施**（见 §6.3、§6.6）。★ 档位是关于**某一个工具**的一句话，所以就该待在那个工具的文件里 —— 而且在那里用的是**同一套词汇**（`http.yaml` 说 `post`，所以它能说 `ask: [post]`；中心那份文件却不得不点名内部名字 `request_post`）。★ 全局 `deny` 跟着一起走：「不被暴露」已经说了两遍（文件的清单、agent 的 `disabled_tools`），第三处就多一处要同步的地方 —— 而它是唯一必须说暴露名的那处 |
| 18 | 缺省档位 | 每一组的缺省由**代码**带着；文件没写就拿缺省，写了就听文件的 | ★ 内置那几组的缺省是**已知**的缺省 —— openheron 知道 `write_file` 会写、`execute` 会跑命令，所以恰好对危险的那几个要点头。MCP server 的工具是**运行期才发现**的：openheron 不知道 `delete_repo` 做什么，而 stdio 之下 server 是带着 agent 权限的子进程 ⇒ 它的缺省是 **ask**，而信任的 server 写一次 `tier: allow` 就行 |

### 「一实体一文件」而非单文件分区表

单文件 `config.yaml` + 具名表（如 `llm.profiles.default`）也能表达同样内容，但：

- 具名表**无法逐字段合并**（数组表按位置、映射表整体替换），而实体文件天然是整体替换；
- 一个 provider 一份文件，`git log` 里能直接看到「谁改了哪个厂商」；
- 与 `agents/*.md` 同一原则，加 agent 不必改既有文件结构。

在 openheron 里，实体单位是「一个 provider 一份文件」（`agentkit/spec/load.go:16-17`），文件里的每个模型同样是一个实体（`agentkit/spec/load.go:52-65`）——
这正是模型用 `<provider>/<model>` 引用、而不是用路径引用的原因。

---

## 3. 目录布局

```
<projectRoot>/.openheron/
├─ agents/main.md            ← agent: frontmatter + body (the body is the prompt)
├─ llm/<provider>.yaml       ← vendor profile: base_url / api_key / models[]
├─ policy/permissions.yaml   ← deny
├─ policy/limits.yaml        ← the caps (depth, retries)
├─ server/config.yaml        ← the process itself: address / dirs / timeouts
├─ tools/<tool>.yaml         ← http / web_search / wikipedia / kb
├─ skills/<name>/SKILL.md    ← skills (resolved by their own rule, see below)
└─ state/                    ← runtime data, never committed
```

空的子目录**不自动创建**：目录不存在就当「没有东西可加载」（`agentkit/spec/config.go:336-338`）。
★ **配置根本身是例外** —— 缺省的那份根不存在时会被建出来（`agentkit/spec/config.go:183-190`），
因为「还没有配置」和「目录不在」本来就是同一件事。

★ **`tools/` 是自成一区的分区** —— 它装着外部工具的配置（`http.yaml`、`web_search.yaml`、`wikipedia.yaml`）
与知识库的配置（`kb.yaml`）（`agentkit/spec/load.go:141-169`），并且**进**热更新（`agentkit/spec/config.go:61-65`），
而 `server/` 不进（`agentkit/spec/config.go:56-60`）。拆分的理由是热更新：`server/` 配的是进程自身、运行期换不掉，
而「改一个工具的超时」本该下一轮就生效。知识库的*索引*是运行数据，落在 `state/kb/<index>`（`kbcmd.go:130`）。

★ **没有 `.config.lock`** —— 配置编辑器走的是**内容哈希的乐观并发**：
`spec.Revision()` 是文件 SHA-256 的前八位十六进制（`agentkit/spec/write.go:266-269`），
带着过期 `baseRevision` 的写入会被判为冲突（`agentkit/spec/write.go:439-456`）。不需要锁文件，所以也没有锁文件。
（样例的忽略规则里仍列着 `.config.lock` —— 那是一处遗留，见下面的忽略规则。）编辑器的设计见 `config-editor-design.md`（upstream）。

`state/` 取代了旧的 `DATA_DIR=./data`，由 `Service.DataDir("")` 创建（`agentkit/spec/config.go:224-236`）：

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

★ **没有 `state/log/`** —— 会话与检查点之外不落别的文件。运行日志走标准错误，不落盘
（落盘就要管轮转与清理，而它对排障的价值远低于成本）。

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
cp -r .openheron-example/{agents,llm,policy,skills} /your/project/.openheron/
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
> 它写什么：`agents/main.md`、`agents/task.md`、`llm/<provider>.yaml`、`policy/permissions.yaml`、
> `policy/limits.yaml`、`server/config.yaml` —— 加上 `--tools` 时才写 `tools/` 下的工具配置
> （`initcmd.go:142-154`、`initcfg/initcfg.go:77-86`、`initcfg/initcfg.go:110`、
> `initcfg/initcfg.go:124-151`、`initcfg/initcfg.go:161-178`）。
> ★ **`tools/` 这一段是可选的**：文件的存在就是打开某个工具的开关，所以 `init` 不会主动写它。

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
| 2 | `OPENHERON_CONFIG_DIR` | 同上 —— `agentkit/spec/config.go:93`、`agentkit/spec/config.go:172` |
| 3 | 兜底 | `<projectRoot>/.openheron` —— `agentkit/spec/config.go:175`；项目根是 `--project-root` 或工作目录（`main.go:86-87`、`agentkit/spec/config.go:158-164`） |

★ **兜底的那份根会被创建；指名的不会。** 只有缺省根在不存在时才会被建出来
（`agentkit/spec/config.go:183-190`）：`--config-dir` 或 `$OPENHERON_CONFIG_DIR` 指向一个不存在的地方是用户笔误，
它不能变成一棵悄悄空着的配置树。

★ **不做上溯搜索 —— 而且 openheron 从来没有做过。** 上游设计会从工作目录向上找 `.heron/`；
这条规则是被**故意去掉**的（`agentkit/spec/config.go:144-156`）：一旦配置根取决于「进程是从哪儿启动的」，
同一条命令换个目录跑就会读到另一份配置，测试实例也会读到开发者自己的配置。
家目录里的配置同样**不是**兜底层：若 `~/.openheron` 存在而不在用，运行时会把话说明白，请你显式指名（`main.go:109-126`）。

★ **真正会覆盖值的环境变量只有少数几个**（§4.4）：它们是在使用点被读到的
（`main.go:217-219`、`main.go:327`、`main.go:341`），而不是靠一层通用覆盖 ——
没有「env 说了算」这条规则，只有若干处「这里会看一眼环境变量」。

### 4.1 缺省值靠「预填充结构体再 decode」

加载器先用内置缺省值**预填充**配置结构体，再把每个文件 decode 到它上面（`agentkit/spec/config.go:274-282`）：
`server/` 段从 `defaultServer()` 起步（`agentkit/spec/defaults.go:58-69`），策略从 `defaultPolicy()`
（`agentkit/spec/defaults.go:9-14`），知识库从 `defaultKB()`（`agentkit/spec/defaults.go:49-56`）。
`loadServer` 是 decode 到预填充结构体的一份**副本**上（`agentkit/spec/load.go:97-104`），
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

每个分区都走它（`agentkit/spec/load.go:23`、`agentkit/spec/load.go:79`、`agentkit/spec/load.go:99`、
`agentkit/spec/load.go:193`、`agentkit/spec/load.go:201`、`agentkit/spec/load.go:128`）。

**为什么严格**：宽松模式下拼错字段名会**静默用默认值**，而「为什么我的配置没生效」是排障成本最高的一类问题。
yaml.v3 的报错自带行号（`yaml.v3@v3.0.1/decode.go:944`）：`line N: field X not found in type Y`。

★ **空文档不是解析错误。** 解码器把一份没有 YAML 的文件报成 `io.EOF` —— 这话是真的，却毫无用处：
「EOF」没告诉用户该做什么。openheron 把它换成 `ErrEmptyDocument`
（「the file is empty (or contains only comments)」）（`agentkit/spec/config.go:396-408`），
因为「只有注释的模板」是一种真实存在的文件形状：照着模板建出来的 `tools/` 文件根本没有文档（`agentkit/spec/load.go:106-133`）。

### 4.3 「配了没用」的三种形态

| 形态 | 处理 | 例子 |
|---|---|---|
| **不认识的键** | 硬报错，带行号 | `max_retry`（拼错）—— `yaml.v3@v3.0.1/decode.go:944` |
| **认识但本版不兑现** | 进 `deferred[]`，**启动必须逐条打印** | `models[].summarize_at` 仍会被解析，但不再被读 —— 阈值归 agent 管（`agentkit/spec/load.go:59-63`） |
| **认识但此刻缺运行期前提** | 同上，且**不阻塞启动** | `api_key` 的 `${ENV}` 未设 —— 请求该端点时才 401（`agentkit/spec/load.go:39-42`） |

```go
func (c *Config) Deferred() []string { return c.deferred }        // agentkit/spec/config.go:115
```

```go
for _, d := range c.Deferred() {
    obs.Logger().Info("[main] config notice", "notice", d)
}                                                                  // main.go:198-200
```

每一条都由 `Config.deferKey` 产生（`agentkit/spec/config.go:134-136`），今天全部的产生者就这些：

| 产生者 | 它说的是什么 |
|---|---|
| `agentkit/spec/load.go:41` | `api_key` 的 `${ENV}` 未设 —— 真正发请求时会 401 |
| `agentkit/spec/load.go:60` | `models[].summarize_at` 不再被读 —— 阈值归 agent 管 |
| `agentkit/spec/load.go:325` | 该模型没有声明 `context_window` —— 该 agent 的自动压缩不会发生 |
| `agentkit/spec/load.go:442` | `policy/limits.yaml` 里的上限比全局基线宽松 —— 它不起作用 |
| `agentkit/spec/load.go:447` | 同上，只是对象是 `retry.max_retries` |
| `agentkit/spec/reload.go:82` | 某个宽松时长解析不了，退回了缺省值 |

★ **最后这条消息对所有宽松时长都写 `watch.debounce`。** 写坏的长时被收进同一个全局列表
（`agentkit/spec/decl/decl.go:338-346`），又在同一处上报（`agentkit/spec/reload.go:80-84`），
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
| `OPENHERON_CONFIG_DIR` | **配置根**（唯一的那一份） | `agentkit/spec/config.go:172`（名字是常量 `ConfigDirEnv`，`agentkit/spec/config.go:93`） |
| `LISTEN_ADDR` | `server.listen_addr` | `main.go:217` |
| `DATA_DIR` | 解析后的数据目录（会话、checkpoint） | `main.go:218` |
| `PROJECT_ROOT` | 交给 agent 的项目根 | `main.go:219` |
| `STATIC_DIR` | `server.static_dir` | `main.go:341` |
| `FRONTEND_MODE` | `server.frontend_mode` | `main.go:327` |
| `EINO_EXT_SKILLS_DIR` | skills 目录 | `agentkit/build/skill.go:189` |

server 侧那五个走同一个辅助函数 —— `override`（`main.go:459`）在环境变量非空时返回环境值，
否则返回配置值。

★ **server 侧那五个覆盖只在启动时读一次。** `server/` 不参与热重载（server 分类带 `hot: false`，
`agentkit/spec/config.go:56-60`），所以环境变量覆盖不是活旋钮 —— 改了要重启。
`EINO_EXT_SKILLS_DIR` 是例外：每次解析 skills 目录都会重新读它（`agentkit/build/skill.go:189`），
所以改动会在 agent 池重建时生效。

★ 另有两个环境变量，**不在上表里**，因为它们不碰配置：

- `OPENHERON_LOG_LEVEL`（`agentkit/obs/logger.go:11`，读于 `agentkit/obs/logger.go:50`）—— 日志级别；
- `OPENHERON_LOG_LLM_REQUESTS`（`llm/common/logging.go:16`，读于 `llm/common/logging.go:81`）——
  是否打印请求体。

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
（`agentkit/spec/load.go:87`）—— 所以**一份合法的配置根本走不到那里**。

这是刻意的取舍：模型档案是**要被 review 和提交**的东西（它决定花谁的钱、存不存数据）。给它一个
进程外的高优先级通道，等于允许「repo 里的配置和线上跑的不一样」而两边都看不出来。留给需要它的
场景再说。

★ **注入密钥的正规做法是引用，不是覆盖。** `api_key` 只能写 `${ENV_VAR}` 或 `@file:/path`
（`agentkit/spec/write.go:493`），值在加载时由 `expandValue` 展开
（`agentkit/spec/expand.go:9`）；而没设的 `${ENV}` **不会**拦住启动 —— 它进 `deferred[]`
（"calls to this endpoint will fail with 401 Unauthorized"，`agentkit/spec/load.go:41`）。
那正是 §4.3 的第三形态：缺的是运行期状态，不是写错。

★ 树里再没有别的环境变量读取。剩下的命中都在测试文件里 —— `OPENHERON_KB_CORPUS`
（`kb/corpus_test.go:12`）、`OPENHERON_LOCK_HELPER` 与 `OPENHERON_LOCK_PATH`
（`persist/filelock_test.go:128`）。

## 5. 配置根：三级，没有探测

配置得先知道项目根才能读任何东西，而项目根本身又是配置项 —— 鸡生蛋问题。openheron 的解法是
**不探测**：配置根要么被点名，要么从一个名字推导出来。

```
① resolve the config root (nothing can be read yet)
   --config-dir                main.go:84
     ↓ unset
   $OPENHERON_CONFIG_DIR       agentkit/spec/config.go:172
     ↓ unset
   <project-root>/.openheron   agentkit/spec/config.go:175, created when missing
```

**上游设计怎么做，以及 openheron 为什么不这么做。** 上游设计从工作目录**向上**找第一个含
`.heron/` 的祖先目录 —— `cd cmd/server && heron` 时 cwd 在子目录，但用户的心智模型是「这是那个
项目」。openheron **刻意丢掉了上溯**：配置根一旦取决于「进程是从哪儿启动的」，同一条命令换个
目录跑就读到另一份配置，而测试实例最终会读到开发者的配置。这条规则写在 `NewService` 的文档注释
里（`agentkit/spec/config.go:144-156`），也写在仓库的 `AGENTS.md` 里。

★ 差别在代码的形状上就看得见：`NewService`（`agentkit/spec/config.go:157`）是**三个**分支、
**没有循环** —— `root` → `os.Getenv(ConfigDirEnv)`（`agentkit/spec/config.go:172`）→
`filepath.Join(projectRoot, DefaultBaseDirName)`（`agentkit/spec/config.go:175`）。整个文件里
没有任何一处沿着祖先目录往上走。

**为何环境变量压过推导出来的默认值。** `$OPENHERON_CONFIG_DIR` 是显式指定，语义上应压过一切
推导。★ 而 `--config-dir` 又压过环境变量（`main.go:84`）—— 命令行是最直接的一次性指定。

★ **只有默认出来的那个根才会被创建。** `NewService` 只在自己推导出根的时候创建目录
（`agentkit/spec/config.go:183-190`）；显式的 `--config-dir`、或者指向空处的
`$OPENHERON_CONFIG_DIR`，都是笔误，而笔误不能悄悄长出一棵空配置树。`Service.RootIsDefaulted`
（`agentkit/spec/config.go:220`）就是为了把两者分开 —— `main.go:135-141` 给出的是
"no configuration at <root>: run `openheron init` to create one"，而不是
"agents/main.md does not exist"。

★ **还有第二个入口，给 `init` 用。** `openheron init` 走 `spec.NewServiceAt`
（`agentkit/spec/config.go:194`），不走 `NewService`：init 往它被指到的目录里写，从不读
`$OPENHERON_CONFIG_DIR`（`initcmd.go:61-65`）。否则 init 会把新配置写到用户没指的地方去。

★ **家目录也不是兜底。** 如果 `~/.openheron/` 存在却不是正在用的配置根，进程会在启动时说一声
（`main.go:109-126`）：配置根从不被搜出来，所以那个目录必须被点名。

### 5.1 两个「根」：`Root` 与 `ProjectRoot`

配置根与项目根是**两个不同的目录**，代码把它们分得很清：

| 方法 | 值 | 用途 |
|---|---|---|
| `Service.Root()`（`agentkit/spec/config.go:212`） | 配置根本身 —— 默认是 `<project-root>/.openheron` | 读配置；`state/` 住在它下面 |
| `Service.ProjectRoot()`（`agentkit/spec/config.go:222`） | 项目根 —— `--project-root`，否则是工作目录（`agentkit/spec/config.go:158-164`） | agent 的工作目录；相对路径的解析基准 |
| `Service.DataDir(v)`（`agentkit/spec/config.go:224`） | `v` 是绝对路径就原样返回，否则落在**配置根**下（`agentkit/spec/config.go:230`）；不存在则创建 | 会话与 checkpoint |
| `Service.ResolveDir(v)`（`agentkit/spec/config.go:238`） | `v` 是绝对路径就原样返回，否则落在**项目根**下（`agentkit/spec/config.go:243`） | `static_dir`、知识库语料 |

★ **两者是解耦的。** `--config-dir /etc/openheron-cfg` 把配置放到别处，而 `--project-root`
（默认：工作目录）仍然是 agent 干活的地方。不解耦的话，「配置根的父目录」就会变成 `/etc` ——
agent 去那儿 grep/glob。

★ **默认的项目根是工作目录，不是配置根的父目录。** 上游设计是从配置根的父目录推出来的；
openheron 改从 `--project-root` 或 cwd 取（`main.go:86-87`、`agentkit/spec/config.go:158-164`）。
配置根取默认值时两者恰好重合（`<cwd>/.openheron` 的父目录就是 `<cwd>`），但配置根一旦被点名，
两者就不再重合。

用错根会得到两种典型故障：前端去配置根下的 `web/dist` 找资源 → **403/404**；agent 被告知
「项目根 = 配置根」→ 在错误目录里 grep/glob。

### 相对路径的解析基准

**相对路径并不是都相对同一个基准。** openheron 有两个：

| 路径 | 基准 | 代码 |
|---|---|---|
| `server.data_dir`（相对） | **配置根** | `agentkit/spec/config.go:229-231` |
| `server.static_dir`（相对） | **项目根** | `agentkit/spec/config.go:238-244`，用在 `main.go:341` |
| `kb.corpus`（相对） | **项目根** | `kbcmd.go:148` |

★ **这个划分是刻意的：状态跟着配置走，工作树跟着项目走。** 相对的 `data_dir` 在配置被搬走时
跟着搬（`--config-dir /etc/openheron-cfg` 会把会话放在它旁边）；相对的 `static_dir` 和相对的
知识库语料属于被服务的那个项目，配置搬走时它们不该跟着搬。

★ **`PROJECT_ROOT` 生效得比配置根晚。** `setupConfig` 先解析出 service（`main.go:184`），之后
才用环境变量覆盖运行期的项目根（`main.go:219`）。于是 `PROJECT_ROOT` 环境变量挪的是 agent 的
工作目录（`main.go:239`、`main.go:245`），**挪不动**默认出来的配置根 —— 那仍然落在工作目录的
`.openheron/` 下。要一起挪，传 `--project-root`。

★ 而且这种情况下两个根确实不是一回事：`static_dir` 相对的是命令行给出的项目根
（`main.go:341`），agent 的工作目录用的却是环境变量覆盖后的那个（`main.go:239`）。设了
`PROJECT_ROOT`，前端的资源目录和 agent 的工作目录就不再共享同一个基准。

### 5.2 测试实例靠「点名的配置根」隔离

openheron 的测试环境集中定义在一处：仓库根的 `.env.test`（已提交），加上 Makefile 的目标。

| | 开发 | 测试 |
|---|---|---|
| openheron 本体 | :8080 | **:18099** |
| Vite | :5173 | **:15173** |
| E2E 假后端 | — | **:18180** |

（`.env.test:25-38`；同一张表也见 `docs/developer-manual.md` §5.1。）

测试实例由 `scripts/test-backend.sh` 启动，挂在三个 Makefile 目标后面：

| 命令 | 做什么 |
|---|---|
| `make test-env-backend` | 前台启动测试实例（`Makefile:312-314`） |
| `make test-env-reset` | 只重建测试目录，不启动（`Makefile:316-318`） |
| `make test-env-sync` | 把开发侧的定义重新复制进测试环境（`Makefile:320-322`） |

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
配置根的，而配置根**从不上溯**（`agentkit/spec/config.go:152`），所以仓库根那个 `.openheron/`
**结构上就够不着** —— 隔离靠的不是「一次恰好停在那里的探测」，而是一个被点名的根
（`scripts/test-backend.sh:38-42`）。

★ **配置根是预置出来的，不是探测出来的。** 脚本会创建 `$PROJECT_DIR/.openheron/` 与
`$PROJECT_DIR/.openheron/server/`（`scripts/test-backend.sh:47`），并写一份默认的
`server/config.yaml`（`scripts/test-backend.sh:79-105`）。没有它们，实例就会对着一棵空配置树
启动，第一个报错是 "agents/main.md does not exist or could not be parsed" —— 读起来像文件丢了，
而不像环境配错了。

★ **`agents/ llm/ policy/ skills/ tools/` 是复制，不是软链。** 它们是**输入**（agent 定义、
厂商协议、权限、工具配置），测试实例要用真 agent 才跑得通一整轮；复制而非软链，是为了测试跑崩
时不会写坏开发配置（`scripts/test-backend.sh:49-73`）。`server/` 与 `state/` 刻意**不在**复制
列表里 —— 那两个由脚本自己管。

★ **`tools/` 在复制列表里是故意的。** 它是一个配置目录（每个外部工具一个文件，外加知识库），
漏掉它意味着 HTTP/搜索工具在测试实例里永远开着 —— 或永远关着 —— 那跟开发侧不是一回事
（`scripts/test-backend.sh:55-59`）。

★ **`make test-env-sync` 是必须的，不是可选的。** 改了开发侧的 agent 定义，不重新复制定义，
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
| `type` | `string` | ✅ | **只用于显示** —— 代码里没有按它分支的地方。它不能为空（`agentkit/spec/load.go:28-30`），但取值自由：`openai_compat` 是出厂模板写的（`initcfg/templates/llm/openai.yaml`） |
| `base_url` | `string` | ✅ | 主机（可带前缀）。**不要写 `/chat/completions`** |
| `api_key` | `string` | | `${ENV}` / `@file:`；空 = 无鉴权（自建网关、本地 ollama）。**`${ENV}` 未设不阻塞启动**，只记提醒，请求时返回 401 |
| `timeout` | `Duration` | | 缺省 `5m`（`agentkit/build/provider.go:31-34`）。**不能依赖 SDK 隐式默认** —— 端点愿意想多久是它说了算 |
| `header` | `map[string]string` | | ★ **解析了，然后没人读** —— 见下 |
| `extra` | `map[string]any` | | 厂商私有请求体，**顶层键覆盖** |
| `hint_keys` | `[]string` | | 该端点使用的**字段名**，供 400 时给准确提示。协议层不内置任何一家的字段名（`llm/common/error.go:177-179`） |
| `models` | `[]Model` | | 它声明的模型；**缺省 = 该 provider 不可用**（引用会解析失败） |

★ **`header:` 是个坑：它解析得过去，然后什么也不会发生。** schema 声明了它
（`agentkit/spec/decl/decl.go:25`），加载器也接受它 —— 严格模式下未知键直接报错
（`agentkit/spec/config.go:398-408`）—— 但从此以后没有任何代码路径读它：
`build.FromConfig` 组装 `common.Config` 时根本没带 header
（`agentkit/build/provider.go:36-43`），而 `common.Config` 里压根没有 header 这个字段
（`llm/common/model.go:28-44`）。本来能承载它的 option 是有的
（`llm/common/option.go:24-26`，`llm/common/request.go:46-48`），但从来没人去填
`RequestOptions.Header`。⇒ **别指望写了它会生效。**

**Model**（模型的固有属性）

| 字段 | 类型 | 说明 |
|---|---|---|
| `id` | `string` | 必填（`agentkit/spec/load.go:56-58`）。传给端点的模型标识 |
| `kind` | `string` | `chat`（缺省）或 `embedding`（`agentkit/spec/decl/decl.go:374-381`）。embedding 模型不能当聊天模型建（`agentkit/build/provider.go:26-29`），知识库也只认 embedding 模型（`agentkit/spec/load.go:348-351`） |
| `dimensions` | `int` | embedding 的维度；`0` = 未知，建索引时学出来（`agentkit/spec/load.go:352-355`） |
| `context_window` | `int` | **0 = 未知**（不是「无限制」）。它是压缩阈值的分母 —— 见下 |
| `summarize_at` | `Threshold` | ★ **已废弃**：仍然解析，好让旧配置继续加载，但没有任何代码读它。阈值现在归 agent（`agentkit/spec/decl/decl.go:45-52`），加载器会明确说出来（`agentkit/spec/load.go:59-63`） |
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
（`agentkit/spec/load.go:310-329`）。

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
没有 `/` 的引用直接报错（`agentkit/spec/load.go:251-260`）。归一化后作为 `Config.Models`
的键（`agentkit/spec/load.go:64`），而 provider id 就是**去掉扩展名的文件名**
（`agentkit/spec/load.go:17`）。

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
同一个 provider、同一个模型，两个 agent 也可能有两个实例 —— 因为 `reasoning` 档位、
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
（`agentkit/build/provider.go:36-49`），`common.New` 再给它套上日志 transport
（`llm/common/model.go:60-64`）。所以：

- **端点私有参数**（思考开关等）→ `WithExtraFields`，且必须在**请求级**下发：请求级 extra 是
  **替换**语义（`go-openai@v0.1.6/chat.go:420-426` —— 原文是 "SetExtraFields will
  override any existing fields with the same key"），调用方须把
  `provider.extra ← model.extra ← reasoning[level]` 三层合完的整一份递下来。
- **跨厂商字段改名**（思维链 `reasoning_content` ↔ `reasoning`）→ `WithRequestPayloadModifier`，
  唯一可行路径（`llm/common/option.go:13-18`，`llm/common/request.go:100-133`）。

#### 启动校验：引用必须能解析

```go
	for _, id := range ids {
		a := c.Agents[id]
		ref, _ := ParseRef(a.ModelRef)
		if _, ok := c.Models[ref.String()]; !ok {
			where := a.File
			if where == "" {
				where = DirAgent + "/" + id + ".md"
			}
			return fmt.Errorf("agent %s (%s) references model %s which is not declared under llm/",
				id, where, a.ModelRef)
		}
	}
```

（`agentkit/spec/load.go:280-291`。）

**为何不静默兜底**：子 agent 静默继承主模型是**成本事故**。同一条纪律在这里同样成立 ——
**拼错的模型名必须在启动期炸出来**。

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
（`initcfg/templates/llm/deepseek.yaml`）：「端点没有声明的档位是**静默忽略**的」。
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
只在启动日志里记一条提醒（`agentkit/spec/load.go:39-45`）。真正的失败点移到**调用该端点**
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
> 形式（`agentkit/spec/decl/decl.go:385-406`）：标签不是字符串就报错 —— "duration must be a
> string with a unit (e.g. 60s / 5m); bare numbers are not allowed"。

★ **但不是每个时长都严格。** `server/config.yaml` 与 `tools/` 下的文件用的是宽松变体
（`agentkit/spec/decl/decl.go:323-334`）：解析不过的值会被记下来并换成默认值，而不是让整次
加载失败。宽松变体覆盖 `watch.debounce`、`start_timeout`、`lease_ttl` 以及 `tools/` 下的
超时（`agentkit/spec/decl/decl.go:266`，`:273`，`:297`，`:303`，`:310`，`:320`）。

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
| 缩进 | 唯一实质代价，靠严格模式兜底：缩进错会报行号而非静默误读。加载器整体是严格的 —— `KnownFields(true)`（`agentkit/spec/config.go:398-408`），所以拼错的键是错误而不是沉默：旧的 `thinking:` 与 `effort:` 键正是这样被拒掉的（`agentkit/spec/config_test.go:870`） |

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
disabled_tools: [execute]         # affects this agent only; stored separately from the global deny, never merged
skills: [refactor]                # the skills this agent may use; absent = every skill, [] = none

temperature: 0.2                  # this call's intent (the same model should use different parameters in different agents)
max_tokens: 8192
reasoning: high                   # a tier name, taken from the keys of models[].reasoning
summarize_at: 80%                 # this agent's compaction threshold, a percentage of the model's context_window
---

The body is the Instruction. Leave it empty to use deep.New's built-in prompt.
```

#### 字段全集

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `model` | `string` | ✅ | `<provider>/<model>`。**不继承、不回退** |
| `description` | `string` | | 进 `task` 工具描述。schema 不把它设成必填 —— 描述为空只是让那份描述里多一条空条目 |
| `max_iterations` | `int` | | `0` = 没声明，此时走 eino 自己的缺省 20（`eino@v0.9.22/adk/react.go:345-348`） |
| `subagents` | `[]string` | | 引用其他 agent 文件名；引用不存在或成环 ⇒ 启动期报错 |
| `disabled_tools` | `[]string` | | **内层**：只影响本 agent。`ask` 档的工具写在这里仍需人批，只是本 agent 根本拿不到 |
| `skills` | `*[]string` | | 技能允许清单。**指针就是重点**：键不存在（`nil`）= 目录下的全部技能，`[]` = 一个都不要（`agentkit/spec/decl/decl.go:94-105`） |
| `temperature` | `*float32` | | 指针：0 与「没配」是两件事 |
| `max_tokens` | `*int` | | 同上 |
| `reasoning` | `string` | | 档位名；不在模型词汇表里 ⇒ 不注入，且**不出声**（见 §6.1） |
| `summarize_at` | `Threshold` | | 这个 agent 的压缩阈值：token 数、模型窗口的百分比，或 `off`（`agentkit/spec/decl/decl.go:120-146`） |

★ **`description` 为什么重要**：它被渲染进 `task` 工具描述
（`eino@v0.9.22/adk/prebuilt/deep/task_tool.go:177-190`，`:182` 那行写出
`- <name>: <desc>`）—— 而 `subagent_type` 拼不中时 eino **返回错误字符串**而非给模型重试信号
（`:164`）。所以描述写不好，模型就选不到它。

| 约束 | 理由 |
|---|---|
| 文件名即 id；`main` 是保留名 | 默认 agent 与其他 agent 是**同一格式的同一个东西** —— 只是文件名恰好叫 `main`（`agentkit/spec/config.go:91`，`:270-272`） |
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

这个上限住在**两处** —— `server/config.yaml` 与 `policy/limits.yaml` —— 实际生效的是两者中
**更紧**的那个（`agentkit/spec/load.go:409-414`，`:218-229`）；缺省是 `1`
（`agentkit/spec/decl/decl.go:228`）。

**校验在启动期**（`validateDepth`，`agentkit/spec/load.go:359-375`），且是**报错**而非运行时
截断。装配层用同一判据，在到顶时**根本不构造**下一层（`agentkit/build/assemble.go:82`）——
双保险：装配期基于配置，运行期 `depthGuard` 基于**实际**嵌套
（`agentkit/build/topology.go:28-62`，挂在链上于 `agentkit/build/gate.go:172-178`）。

#### frontmatter 的三种输入

| 输入 | 行为 |
|---|---|
| `---` … `---` + 正文 | frontmatter 解出字段，正文（去掉分隔符后全部）成为 `Instruction` |
| 无 frontmatter 的纯 `.md` | ★ **在 openheron 里是错误** —— 加载器去解一份空的 frontmatter，报「文件是空的（或者只有注释）」（`agentkit/spec/config.go:396`，`agentkit/spec/load.go:79-81`）。上游设计让纯提示词草稿先过去、随后因缺 `model` 报错；openheron 更早炸，而且消息更清楚 |
| 有 `---` 开头但缺结束分隔符 | **错误**：无法判断正文从哪开始 —— "frontmatter closing delimiter not found"（`agentkit/spec/expand.go:48-65`） |

#### frontmatter 解析必须自写

eino 的 `parseFrontmatter` 在 `eino@v0.9.22/adk/middlewares/skill/filesystem_backend.go:165`，
**未导出**，无法复用。openheron 自写 —— `splitFrontmatter`，约二十行
（`agentkit/spec/expand.go:48-65`）。

`SKILL.md` 已用同一约定（`FrontMatter` + 正文，
`eino@v0.9.22/adk/middlewares/skill/skill.go:47-56`），所以 openheron 的 `agents/*.md` 与 eino
原生 markdown 约定**同构**，不是另立一套。

### 6.3 `policy/permissions.yaml`

审批档位。整个文件就是两张清单，而代码自己的缺省是：

```yaml
# The factory default (spec.defaultPolicy)
ask:   [write_file, edit_file, execute, kb_index]
deny:  []
```

`defaultPolicy` 在 `agentkit/spec/defaults.go:9-14`，而随包发布的模板把同样这张清单写出来
（`initcfg/templates/policy/permissions.yaml`）—— 这个文件是看得见、改得动的，而不是靠约定隐含的。

★ openheron 缺省要**四**个工具，不是三个：`write_file`、`edit_file`、`execute`
—— 以及 **`kb_index`**，因为给知识库建索引会往磁盘上的索引里写。就策略而言它是个普通工具，
所以和其它几个一样进这张清单。

| 字段 | 加载器怎么处理它 | 理由 |
|---|---|---|
| `deny` | 与已有的那份**取并集** | 它是安全边界 |
| `ask` | **整体替换** | 它是一种偏好，不是安全边界 |

这两条规则住在加载器里（`agentkit/spec/load.go:196-197`）；`decl.Policy` 本身只是两个字符串切片
（`agentkit/spec/decl/decl.go:210-213`）。★ 只有一个 `policy/` 目录时只有一份 `permissions.yaml`，
所以那个并集今天是空转 —— 但它是加载器实现的规则，而且并集正是第二层必须面对的东西。

★ 匹配顺序是 `deny → ask → **兜底 allow**`。**只有 `ask` / `deny` 拦得住东西** ✅ —— 未点名的工具
直接放行 ✅（「读文件」「列目录」每次都要点头，用不了 ✅）。

★ **没有 `allow` 那一列** ✅ —— 未点名的工具本来就是放行的，一列写着「直接放行」只会让人以为相反
（「不在 `allow` 里就是不让用」）✅ —— 而事实恰恰相反 ✅ ✅。

闸门在 `agentkit/build/gate.go:65-83`：`allow` 原样放行，`deny` 把它换成一个错误，`ask` 打断这一轮
等人。档位（`allow` / `ask` / `deny`）与两层工具表的语义见 `docs/architecture.md` §3.4；完整的上游
设计见 `agent-design.md`（上游）§5。

#### ★ 档位搬到工具那里 —— 而且全局 deny 跟着一起走（★ 提案 2026-11-06，**未实施**）

★ 这一小节讲的全是**提案**。今天上面那个文件里两张清单都有，而加载器读它们的方式就是
§11.6 记着的那个样子。这个提案把 `ask` 档搬进每个工具自己的文件（§6.6、§6.7）——
并且**把全局 `deny` 整个取消**，于是 `policy/permissions.yaml` **消失**，这个分区只剩
`limits.yaml`。

★ **为什么 `ask` 要搬走。** 档位是关于**某一个工具**的一句话：「这个要先点头」。这句话该待在
那个工具自己的文件里 —— 而把它放在那里，同时也把它放进了**同一套词汇**里：`http.yaml` 说
`post`，所以 `http.yaml` 能说 `ask: [post]`，而中心那份文件却不得不点名内部工具名
`request_post`（§6.6）。

★ **为什么全局 `deny` 跟着一起走。** 「不被暴露」这件事已经能表达两遍，而两处都不需要中心
清单：
- **文件的清单**说这个文件提供什么（§6.6），以及
- **agent 的 `disabled_tools`** 说这个 agent 拿不到什么（§6.2）。

⇒ 同一个决定的第三处，就是多一处要同步的地方 —— 而中心那处是三者里最糟的一处：只有它必须
用**暴露**出来的词汇点名工具，而工具自己的文件说的是它们自己的词儿。

★ **跟着一起失去的：外层边界。** 再没有一层是子 agent 放松不了的 —— 「外层 `deny` 压过
`disabled_tools`」这个保证（今天的代码有，`AGENTS.md` 也记着）在提案里**没有对应物**。
剩下的是 agent 作者自己写下的那点收窄。⇒ 一个必须**到处**都不存在的工具，从提供它的那个
文件里拿掉。

★ **这个文件里的 `ask:` 与 `deny:` 都是加载期错误**，报错信息会指出新家在哪儿。一个被静默
忽略的键，正是 §4.3 说的「配了没用」那一类。

★ 装配好的策略只留**一张**清单（`allow` / `ask`），而 `ask` 那一半由 provider 装配 ——
在 `agentkit/build`，不在 `agentkit/spec`：只有 provider 知道自己文件的词汇和它背后的暴露名，
而 `agentkit/spec` 不得 import `kb`（铁律），也不得 import eino（文件系统工具名住在那里）。

#### ★ 前缀匹配（★ 2026-11-06 决策，**未实施**）

★ **某个 agent 的 `disabled_tools`** 除了**精确工具名**，还接受**尾部 `*` 的前缀模式**：

| 条目 | 匹配 |
|---|---|
| `execute` | 名字正好是 `execute` 的那个工具 |
| `mcp__github__*` | server 名为 `github` 的每一个 MCP 工具 |
| `mcp__*` | 来自任何 server 的每一个 MCP 工具 |

| # | 规则 | 理由 |
|---|---|---|
| 1 | 只有**结尾**的 `*` 是前缀模式；`*` 出现在别处 ⇒ **加载期硬错误** | `mcp*foo` 看着像通配符而其实不是 —— 一条读起来与实际覆盖范围不同的规则，就是一次静默的错配 |
| 2 | 单独一个 `*` ⇒ **加载期硬错误** | `disabled_tools: ["*"]` 会连 agent 自己的工具一起拿走；那是要写出来的一个决定，不是一个通配符 |
| 3 | `*` 前面不能为空 | 规则 2 |
| 4 | 精确条目与前缀条目**等价** —— 命中就是该 agent 拿不到这个工具 | 这里没有档位可选；这个工具对那个 agent 就是不存在 |

★ **这是前缀语法仅剩的用武之地。** 档位搬进了工具自己的文件，在那里，文件自己的 `tier` 不用
任何模式就能覆盖一整类；剩下的是**收窄**，而「这个 agent 不要任何 MCP 工具」是唯一一句关于
**命名空间**而不是关于某个工具的话。

★ **它不新增档位，也不重排档位** —— 模式只决定**一条条目是否匹配某个名字**。

★ **`policy/limits.yaml` 不参与** —— 它装的是数量，不是工具名，匹配语义照旧。

★ **今天的代码只做精确匹配**：`TierOf` 拿名字去比对 `deny` 列表、去查 `ask` 那张 map
（`agentkit/build/policy.go:39`），`Table.Blocked` 再查另外两张 map
（`agentkit/build/policy.go:94`）—— 今天一条 `mcp__github__*` **匹配不到任何东西**，而且它也不是
错误：那条子项只是一个没有任何工具名与之相等的字符串。这份提案（包括语法规则）写在
`docs/mcp-design.md` §4。

#### `policy/` 下认哪些文件

| 文件 | 处理 |
|---|---|
| `limits.yaml` | ✅ **限制规则**（数量上限），见 §12.2 |
| `permissions.yaml` | ★ **提案里它没了** —— 档位搬进了工具自己的文件，全局 `deny` 跟着一起走了。今天它就是上面那份 |
| 其它文件名 | **报错** —— 文件名白名单比键级校验强：拼错就是「文件不存在」，在写下的那一刻就失败，而不是等某个键被读到才发现 |

白名单在 `agentkit/spec/load.go:207`：`policy/ only accepts permissions.yaml and limits.yaml` —— 提案里它只接受 `limits.yaml`。

### 6.4 `server/config.yaml`

进程自身的配置。**它不进热更新** —— 改了要重启（见 §8）。server 分区带着 `hot: false`
（`agentkit/spec/config.go:56-60`），配置编辑器干脆不写它（`agentkit/spec/write.go:73-75`）。

```yaml
# The process's own configuration.
listen_addr: ":8080"        # ★ it must be quoted: a value starting with : parses ambiguously
frontend_mode: embed        # embed | dir — the UI compiled into the binary, or a directory on disk
static_dir: web/dist        # relative to the **project root** (not the config root, not cwd)
data_dir: state             # relative to the **config root**; an empty string means state/

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
string，只看 `Decode` 的错误会漏掉它（`agentkit/spec/decl/decl.go:385-392`）。

★ **降级必须出声**：写错的项进 `deferred`，启动时与其它降级一起打印。悄悄退回默认等于「配了没用」
不可见。

★ **但兜底值对每一个宽松字段都是同一个 200ms，而消息不管写错的是哪个字段，都写 `watch.debounce`。**
`LenientDuration.UnmarshalYAML` 退回 `decl.DefaultDebounce`
（`agentkit/spec/decl/decl.go:325-334`），收集消息的地方把文本写死成
`watch.debounce fell back to default`（`agentkit/spec/reload.go:80-84`）。所以 `tools/http.yaml` 里
`timeout` 写错，报出来的是一条 `watch.debounce` 消息，而超时变成了 200ms —— 而不是这个字段本来该拿
到的 30s。读这条消息时要读成「有个时长没解析出来」，而不是「看门狗跑歪了」。

#### 字段全集

| 字段 | 类型 | 缺省 | 说明 |
|---|---|---|---|
| `listen_addr` | `string` | `:8080` | Hertz 监听地址 |
| `frontend_mode` | `string` | `embed` | `embed` 服务编译进二进制的界面；`dir` 服务磁盘上的一个目录，`static_dir` 指向它。值不认识时只记一条告警，前端不启用（`main.go:330-354`） |
| `static_dir` | `string` | `web/dist` | 相对**项目根** `<root>` 解析（`Service.ResolveDir`），非 cwd、非配置根；只在 `frontend_mode: dir` 时才被读 |
| `data_dir` | `string` | `state` | 相对**配置根**解析（`Service.DataDir`），非项目根、非 cwd；空串等同 `state`；目录不存在时创建 |
| `start_timeout` | `LenientDuration` | `60s` | 与 `agentkit/run.DefaultStartTimeout` 同值（`agentkit/run/runtime.go:26`） |
| `lease_ttl` | `LenientDuration` | `10m` | 一个 run 的租约在无进展的情况下能存活多久；它必须**长于 provider 自己的超时**，否则一个慢但活着的模型会在思考途中被 fence 掉（`agentkit/run/lease.go:20`） |
| `retry.max_retries` | `int` | `5` | 传 0 也用默认 ——「不配」与「配成 0」是两件事（`agentkit/spec/load.go:430-435`） |
| `agent.max_depth` | `int` | `1` | 见 §6.2 |
| `watch.enabled` | `*bool` | `nil`→开 | 用指针区分「没写」与「写了 false」（`agentkit/spec/reload.go:86-91`） |
| `watch.debounce` | `LenientDuration` | `200ms` | 轮询间隔取其一半，下限 20ms（`agentkit/spec/reload.go:141-149`） |

> **★ 项目根 vs 配置根**：`Service.ProjectRoot()`（`agentkit/spec/config.go:222`）是静态资源与
> agent 工作目录所在的那一层；`Service.Root()`（`agentkit/spec/config.go:212`）是配置根 ——
> `policy/`、`tools/`、`state/` 都在它下面。**两者不同**：`static_dir` 按前者解析，`state/` 按后者。
> 把二者混用正是「前端 404 / agent 被告知在 `.openheron` 里工作」的根因。

★ 这里的每一个值都能被环境变量覆盖 —— `LISTEN_ADDR`、`DATA_DIR`、`PROJECT_ROOT`、
`FRONTEND_MODE`、`STATIC_DIR`（`main.go:217-219`、`main.go:327`、`main.go:341`）；全集见 §4.4。

★ **与 `tools/` 和 `policy/` 不同，`server/` 没有文件名白名单。** 它下面的每一个 `.yaml` 都被解进
同一个 `decl.Server` 结构（`agentkit/spec/load.go:97-104`），所以一个手滑的 `server/typo.yaml`
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
`inline`（`agentkit/build/skill.go:263-266`）。

#### 目录怎么定位

```
EINO_EXT_SKILLS_DIR environment variable    ← the override, the highest one
   ↓ unset
look for .openheron/skills/ upward from cwd  ← agentkit/build/skill.go:212 probeProjectSkills
   ↓ not found
skills are not enabled
```

★ **显式配了却不存在会打日志**：由 `EINO_EXT_SKILLS_DIR` 指名的目录不存在会被报出来
（`agentkit/build/skill.go:201-208`）。静默返回「未启用」会让「我配了 skills 怎么没生效」变成一个
查不出原因的问题 —— 属 §4.3 说的「配了没用」。

★ **这次向上找不是配置根那套规则。** 配置根从不向上找（`docs/architecture.md` §3.1），而 skills 目录
会：skills 是一棵目录树，向上找能复用家目录里的 skills；配置根是一组实体，向上找会让同一条命令读到
另一份配置。

#### `agent:` / `model:` 的落地

两行 frontmatter 现在都生效：`agent:` 走 `AgentHub`（`fork` / `fork_with_context` 两种模式），
`model:` 走 `ModelHub`（inline 模式换本轮模型）。实现与边界见 §7。

- `context: inline` 只需要 description；
- 写了 `agent:` 但不在当前 agent 的子树里 ⇒ 报错并列出可用名字（`agentkit/build/hub.go:152-156`）；
- 写了 `model:` 但引用解析不了 ⇒ 报错，不再静默忽略（`agentkit/build/hub.go:87-93`）；
- hub 没有绑定 resolver 时（只有测试会这样）⇒ 报错，而不是悄悄用原模型（`agentkit/build/hub.go:76-78`）。

★ **openheron 在 agent 这一侧还多了一个字段**：`agents/<id>.md` 里的 `skills` 键是 skills 目录下
各文件夹的白名单。它以**指针**形式携带（`agentkit/spec/decl/decl.go:94-105`），因为「没声明」与
「声明了但是空」意思不同 —— 前者是全部 skills，后者是一个都没有。这个范围过滤的是两半
（`agentkit/build/skill.go:83-129`）：`List`，也就是模型**看得见**的；`Get`，也就是它真能**加载**的。
只过滤一半，会留下一个模型记得名字就能加载的 skill。

### 6.6 `tools/<tool>.yaml`（★ 2026-10-24 新增分区）

外部工具与知识库的配置。**一个工具一个文件**（下面的路径相对**配置根** —— 默认的那个就是
`.openheron/`）：

```
.openheron/tools/
├─ filesystem.yaml       → decl.FilesystemCfg (the filesystem tool set)
├─ http.yaml             → decl.HTTPToolsCfg
├─ web_search.yaml       → decl.WebSearchCfg
├─ wikipedia.yaml        → decl.WikipediaCfg
└─ kb.yaml               → decl.KB (the knowledge base)
```

★ **文件的内容就是那个工具自己的配置**（没有外层包装键）—— `http.yaml` 里直接写 `methods:`，不是
`http: { methods: … }`。「**文件在不在**」就是开关：没有 `enabled` 字段，也没有「写个 null 关掉」
这种形态 —— **关它就是删文件**。加载器自己就是这么说的（`agentkit/spec/load.go:106-118`），空文件
会被拒掉，并给出一条提示告诉你该写什么（`agentkit/spec/load.go:127-133`）。

★ **它进热更新**：改一个工具的超时、换一个搜索后端、给知识库换个语料目录，写完**下一轮就生效**，
不必重启进程。tools 分区带着 `hot: true`（`agentkit/spec/config.go:61-65`）。这正是它从
`server/config.yaml` 搬出来的理由：`server/` 配的是进程自身，不进热更新 —— 而这些改动跟「进程
自身」没关系。

★ **同名文件整体替换**：同一层里一个文件名只有一份，`tools/http.yaml` 就是它。这比原来更细 ——
以前改 `server/config.yaml` 里的 `tools.http` 会连带把 `tools.web_search` 也一起换掉（同一份文件、
整体替换），现在它们互不相干。

★ 知识库的 `kb.yaml` 从原来的 `kb/config.yaml` 搬来。**索引目录没搬**：它在运行数据里
（`state/kb/<index>/`，`agentkit/build/kb.go:42`），与语料同生共死。

#### ★ 每个文件说的名字 —— 以及模型看到的名字（★ 提案 2026-11-06，**未实施**）

一个文件用**自己的词汇**声明它的档位，而 provider 负责把那个词汇映射成模型看到的名字。
两者不是一回事，而这正是要点：

| 文件 | 这个文件说的词汇 | 模型看到的名字 |
|---|---|---|
| `filesystem.yaml` | `write_file` `execute` … | 同左 |
| `http.yaml` | 方法：`post` | `request_post` |
| `web_search.yaml` | 后端：`duckduckgo` | `duckduckgo_search` |
| `wikipedia.yaml` | ——（一个工具） | `wikipedia_search` |
| `kb.yaml` | `kb_search` `kb_index` | 同左 |
| `mcps/<server>.yaml` | server 自己的名字：`create_issue` | `mcp__github__create_issue` |

★ **拆分之前，这张表并不存在** —— 策略文件说的是第三列，而工具自己的文件说的是第二列，于是
`http.yaml` 说 `post`，`permissions.yaml` 却不得不说 `request_post`。★ 一个工具两套词汇，
就是等着发生的错配；而名字拼错了，在工具的表现与文件所写不一致之前根本看不出来。

⇒ **一个文件的 `tools:` / `tier:` / `ask:` 写在第二列里。** 由 provider 来翻译；别的任何东西
都不必知道暴露名。

（暴露名在 `agentkit/spec/decl/decl.go:279-286`、`kb/tools.go:16-17` 与 eino 的
`adk/middlewares/filesystem/filesystem.go:38-46`。）

#### ★ 每个文件都带的那几个键（★ 提案 2026-11-06，**未实施**）

`tools/` 与 `mcps/` 里的每个文件都带同样这几个键，而且它们在任何地方都是同一个意思：

| 键 | 类型 | 缺省 | 含义 |
|---|---|---|---|
| `tools` | `[]string` | 全部 | ★ **暴露集合** —— 这个文件要提供的那些名字，用**原始**名字写（这个文件自己的词汇，**不是**暴露出来的那些名字）。不写 = 全部 |
| `tier` | `string` | `allow` | 被暴露的那些的档位：`allow` \| `ask` |
| `ask` | `[]string` | `[]` | 被暴露的那些里要先点头的 —— **用这个文件自己的词汇** |

★ **文件里没有 `deny`，而且提案里连全局 `deny` 也没有了**（§6.3）。「不被暴露」只说两处：这个
  文件的**清单** —— 每个文件把清单叫成合口的名字，HTTP 用 `methods:`，其余用 `tools:` ——
  与某个 agent 的 **`disabled_tools`**。

★ 全局那份清单没了，因为它是同一个决定的第三处，而且是**唯一**必须说暴露名的那处，
  而工具自己的文件说的是它们自己的词儿。⇒ **每一层一个机制，每一层一套词汇。**

★ **一个文件内部的顺序**：`ask` 压过 `tier`。所以 `tier: allow` + `ask: [post]` 的意思是
「只有 post 要点头」，而单写 `tier: ask` 的意思是「这个文件提供的一切都要点头」。

★ **`tier` 正是让 MCP server 的档位能覆盖它还没提供的工具的东西。** server 的工具清单只有
运行期才知道，所以一条点名了工具的规则覆盖不了 server 明天才加的工具。`tier: ask` 覆盖
server 将来会提供的每一个工具 —— 这正是前缀规则对 `ask` 档永远做不到的事（§6.3）。

#### ★ 缺省档位

★ **兜底**：没有被任何文件点名的工具是 **allow** —— 直接放行。正是这条规则让
「读一个文件」不必每次都点头。

★ **每一组都有一个代码缺省**，文件覆盖它：

| 组 | 文件 | 缺省 |
|---|---|---|
| 文件系统工具 | `tools/filesystem.yaml` | `write_file` `edit_file` `execute` → **ask**；其余 → allow |
| HTTP 工具 | `tools/http.yaml` | allow |
| 网页搜索 | `tools/web_search.yaml` | allow |
| 百科 | `tools/wikipedia.yaml` | allow |
| 知识库 | `tools/kb.yaml` | `kb_search` → allow；`kb_index` → **ask**（它要写索引） |
| ★ **一个 MCP server** | `mcps/<server>.yaml` | ★ **ask** —— 这个 server 的每一个工具 |
| 框架自带的（`task` `write_todos` `skill`） | 没有文件 | allow |

★ **对它们全体是同一条规则：没写的键 = 缺省；写了的键 = 说话。** 所以
`tools/filesystem.yaml` 里的 `ask: []` 是明明白白的「这些一个都不点头」，
而把那一行删掉，缺省就回来了。

★ **为什么 MCP server 的缺省是 `ask`，而内置的不是。** 内置那些缺省是**已知**的缺省：
openheron 知道 `write_file` 会写文件、`execute` 会跑命令，所以它知道哪些该问。
而一个 MCP server 的工具是**运行期才发现**的 —— openheron 无从知道 `delete_repo` 做什么 ——
而在 stdio 之下，那个 server 是带着 agent 自己权限的**子进程**（§6.7）。
⇒ 猜错的代价是不对称的：多一次点击，换掉的是一次没人看见的破坏性调用。
用户信得过的 server 说**一次**就够了：

```yaml
tier: allow
```

★ **缺省值就住在读到它的地方。** `tools/filesystem.yaml` 的出厂模板是用代码用的
同一批常量写出来的，于是文件与缺省值不会走偏 —— 而 `openheron init` 会写它，
因为它是 `policy/permissions.yaml` 原来那份 ask 清单的接班人。★ 那些可选的集成
留在 `--tools` 后面：它们存在哪些是用户说了算，而文件系统那套不是可选的。

#### ★ 档位改动下一轮就生效

★ `tools/` 与 `mcps/` 都是**热**分区，所以档位并不特殊：改一个档位，下一轮就生效，
跟这个分区里的任何别的键一样。策略表随快照一起重建，于是闸门从下一轮起读到新档位 ——
不用重启，也没有那个工具还留着旧档位的陈旧窗口。

★ 重载唯一要顾对的是**客户端**：快照换代时，stdio 的子进程被拆掉、新的被拉起来
（`docs/mcp-design.md` §5.4）。一个资源重建不出来的热分区，就是一个谎称自己是热的分区。

#### ★ `tools/filesystem.yaml` —— 文件系统工具集（★ 提案 2026-11-06，**未实施**）

文件系统工具来自框架的中间件，而不是来自某个 provider，所以直到现在它们**没有自己的文件**：
它们的档位住在 `permissions.yaml` 里，而且没有任何办法能把这一套收窄。这个文件给它们一个家：

```yaml
# .openheron/tools/filesystem.yaml
tier: allow
ask:  [write_file, edit_file, execute]
```

这一套就是 eino 的文件系统中间件：`ls`、`read_file`、`write_file`、`edit_file`、
`glob`、`grep`、`execute`（`eino@v0.9.22/adk/middlewares/filesystem/filesystem.go:38-46`）。

★ **文件不在意味着用默认那套工具与默认那些档位。** 这个文件是**调音旋钮**，不是开关：
文件系统工具是 agent 的核心能力，不是可选集成 —— 知识库由语料而不是由文件来开关，也是同一个
理由。★ `openheron init` 照样会写它，所以这些档位是**看得见、改得动**的，而不是靠约定隐含的。

★ **「只读 agent」这三个字现在靠清单来写** —— 不在清单里的工具就是不被提供：

```yaml
# .openheron/tools/filesystem.yaml
tools: [ls, read_file, glob, grep]
tier: allow
```

#### ★ 迁移（旧配置怎么办）

这次搬动是**破坏性的** —— 旧写法会**加载即报错**，不是静默忽略：

| 旧写法 | 现在 | 症状 |
|---|---|---|
| `server/config.yaml` 里的 `tools:` 段 | 拆成 `tools/http.yaml` `tools/web_search.yaml` `tools/wikipedia.yaml` | 启动报错 `field tools not found in type decl.Server` |
| `kb/config.yaml` | `tools/kb.yaml` | **静默** —— 知识库不再被读到，`kb_search` 消失但没有任何报错 |
| `policy/permissions.yaml` 里的 `ask:` | 档位搬进工具自己的文件：`write_file` / `edit_file` / `execute` 进 `tools/filesystem.yaml`，`kb_index` 进 `tools/kb.yaml` | 启动报错，并指出新家在哪儿 —— ★ 故意**不**静默：一个不再被读到的档位会把那些工具丢进兜底 allow 档，而兜底档不拦它们 |

★ 那条「静默」是这次搬动里最需要当心的一处：`kb/` 不在加载的分区清单里
（`agentkit/spec/config.go:40-66`），所以旧的 `kb/config.yaml` 根本不会被读：`corpus` 像是「没配」
一样 —— 而 `corpus` 为空 = 知识库关闭。症状是「kb_search 没了」，但启动日志一个字都不说。
⇒ 迁移时**先 `mv` 再启动**，别指望启动报错提醒你。

#### 字段全集

| 文件 | 字段 | 类型 | 缺省 | 说明 |
|---|---|---|---|---|
| `http.yaml` | `methods` | `[]string` | `[get]` | ★ **只给白名单里的方法**；post/put/delete 能改外部世界，要显式点名 |
| `http.yaml` | `headers` | `map[string]string` | `{}` | 每个请求都带的头 |
| `http.yaml` | `timeout` | `LenientDuration` | `30s` | 单个请求的上限 |
| `web_search.yaml` | `provider` | `string` | `duckduckgo` | 搜索后端；**未知值不静默降级** —— 整组不挂 + 日志出声（`agentkit/build/tools.go:55-63`） |
| `web_search.yaml` | `max_results` | `int` | `10` | 一次搜索最多返回几条 |
| `web_search.yaml` | `timeout` | `LenientDuration` | `60s` | 单次搜索的上限（搜索要翻页，比 HTTP 宽裕） |
| `web_search.yaml` | `region` | `string` | `""`→全球 | duckduckgo 的 `kl` 参数（`wt-wt` / `cn-zh` / …） |
| `wikipedia.yaml` | `top_k` | `int` | `3` | 返回几条 |
| `wikipedia.yaml` | `language` | `string` | `zh` | 维基的语言 |
| `wikipedia.yaml` | `timeout` | `LenientDuration` | `15s` | 单次检索的上限 |
| `wikipedia.yaml` | `base_url` | `string` | `""`→官方 | API 端点（自建镜像用） |
| `kb.yaml` | `corpus` | `string` | `""`→关 | 语料目录，**相对项目根**；绝对路径被拒（索引与语料同生共死） |
| `kb.yaml` | `index` | `string` | `default` | 索引目录名，落在 `state/kb/<index>/` |
| `kb.yaml` | `model` | `string` | `""` | 向量化模型，`<provider>/<model>`；必须 `kind: embedding`；corpus 非空时**必填** |
| `kb.yaml` | `query_instruction` | `string` | `""` | 只加在查询侧的指令前缀 |
| `kb.yaml` | `batch_size` | `int` | `10` | 一次向量化请求的条数上限 |
| `kb.yaml` | `max_batch_chars` | `int` | `1200` | 一批向量化文本的字符数上限 |
| `kb.yaml` | `top_k` | `int` | `8` | `kb_search` 默认召回条数 |
| `kb.yaml` | `splitter.headers` | `map[string]string` | `{}` | ★ 仍然解析，但**自 v2 起不再生效**（分段改由行扫描得出）—— `kb status` 会把这话讲出来 |

★ **这些字段的缺省值住在读它们的 provider 里，不在 `decl` 里。** 声明结构体只带形状
（三个工具配置在 `agentkit/spec/decl/decl.go:294-312`，知识库在
`agentkit/spec/decl/decl.go:352-372`）；值在 provider 里（三个超时在
`agentkit/build/nettools.go:22-27`，搜索的缺省在 `agentkit/build/nettools.go:210-217`，维基的缺省在
`agentkit/build/nettools.go:246-253`），知识库的缺省在 `agentkit/spec/defaults.go:49-56`。出厂模板把
它们都写了出来（`agentkit/spec/write.go:182-216`）—— 那是看清整个配置面最短的一条路。

★ 出厂模板里有两条注释跑在代码前面，两条都要让困惑的人买单：`web_search.yaml` 的模板提到还有第二个
provider `searxng`（`agentkit/spec/write.go:193`），而代码只认 `duckduckgo` —— 写 `searxng` 会被当作
未知 provider 拒掉（`agentkit/build/nettools.go:204-208`）；`kb.yaml` 的模板说 `model` 留空就是纯
词法检索（`agentkit/spec/write.go:209`），而加载器会拒掉它：`kb.model is required when kb.corpus is
set`（`agentkit/spec/load.go:339-342`）。

★ 文件名是**白名单**（`http.yaml` / `web_search.yaml` / `wikipedia.yaml` / `kb.yaml`）：未知文件名在
**加载期**就是硬错误（`agentkit/spec/load.go:167` —— `tools/ only accepts http.yaml, web_search.yaml,
wikipedia.yaml and kb.yaml`）—— 写进去一个永远不生效的段落，症状是「我明明写了却不生效」，极难查。

#### ★ 「连到外面去」的三类工具

`tools/` 下的三类工具与知识库有一个本质差别：**它们的输出会原样进模型上下文**。知识库召回的是自己
写的语料，而这里是别人写的网页 —— 于是「外部内容里的指令」也进了模型脑子。策略层
（`allow`/`ask`/`deny`）管的是「能不能调这个工具」，看不见 URL、也看不见结果里的字。

所以这一族工具的规矩是（实现见 `agentkit/build/nettools.go`）：

| 规矩 | 为什么 |
|---|---|
| **默认全关** | 没配就一个都不挂 —— 挂一个永远搜不到东西的 `web_search` 比不挂更糟。开关是「**那个文件在不在**」，没有 `enabled` 字段 ✅ |
| **只连公网** | 对 HTTP 工具来说 URL 是模型给的。能访问 `127.0.0.1` 的 HTTP 工具 = 模型能自己读本机配置、会话状态、云上元数据服务（`169.254.169.254`） |
| **输出有长度上限**（20000 字节） | 一个 10MB 的页面会把这一轮直接撑爆 —— 表现是「模型突然开始胡说」，不是报错 |
| **写方法要显式点名** | POST/PUT/DELETE 能改外部世界的状态，而模型不需要理由就能编出一个 POST |

文件不在时，每个 provider 都不挂任何工具（`agentkit/build/nettools.go:127-131`、
`agentkit/build/nettools.go:195-199`、`agentkit/build/nettools.go:241-245`），上限是
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

「写下 `tools/http.yaml`」本身就是那个决定 ✅ —— 所以这类工具**默认 allow** ✅，不必再去任何地方
点名一次 ✅（同一件事说两遍）。

| 谁来定 | 管什么 |
|---|---|
| `tools/` 里的文件 | **开不开**这个能力（文件不在 = 工具根本不存在）、**它提供的那一套里暴露哪些**（清单）、**以及那些的档位**（`tier` / `ask`） |
| `policy/permissions.yaml` | ★ **提案里没了** —— 不再有全局边界了（§6.3） |
| 每个 agent 的 `disabled_tools` | **细粒度收窄**：某一个 agent 不用这个工具（内层，它管得住） |

★ 所以一个工具的档位是从**两处**读的，顺序是固定的：先是提供这个工具的那个文件，然后是
agent 的 `disabled_tools` —— 它只为那个 agent 摘掉这个工具，仅此而已。

★ 兜底档是 **allow** ✅ —— 未点名的工具（ls / read_file / task / skill …）直接放行 ✅ ✅。
**只有 `ask` / `deny` 点名的才被拦** ✅ ✅。

★ 知识库（`kb.corpus`）**不是**由文件在不在来开，而是由非空的 `corpus` 来开
（`agentkit/build/kb.go:15`），它的两个工具和其它工具一样走档位：`kb_search` 是普通 `allow`，
而 `kb_index` —— 一个写操作 —— 默认要点头（`tools/kb.yaml`）。

### 6.7 `mcps/<server>.yaml`（★ 2026-11-06 决策，**未实施**）

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

#### 字段全集

| 文件 | 字段 | 类型 | 缺省 | 说明 |
|---|---|---|---|---|
| `<server>.yaml` | `transport` | `string` | `stdio` | `stdio` \| `http`；★ **未知值硬报错**，不静默退回 stdio |
| | `command` | `string` | — | stdio 专用，**必填**：要执行的命令 |
| | `args` | `[]string` | `[]` | stdio 专用：它的参数 |
| | `env` | `map[string]string` | `{}` | **叠加**在继承来的环境之上的变量；★ 子进程的环境就是 openheron 自己的环境，这些覆盖同名项 —— 而这份继承正是把凭据的哨兵送到服务器、而这里一个字段都不用写的原因 |
| | `url` | `string` | — | http 专用，**必填**：服务器地址 |
| | `headers` | `map[string]string` | `{}` | http 专用：每次请求都带的头 |
| | `timeout` | `LenientDuration` | `30s` | 连接 + 初始化 + 取工具清单的上限 |
| | `call_timeout` | `LenientDuration` | `60s` | **一次工具调用**的上限 —— ★ 没有它，一个永不回答的服务器会占着调用锁，同 server 的其它调用全排在它后面 |
| | `tools` | `[]string` | `[]`→全部 | ★ **暴露集合** —— 这个 server 要提供的那些**原始**工具名（不是带命名空间的那个）；不写 = 全部 |
| | `tier` | `string` | `ask` | 这个 server 提供的每个工具的档位 —— ★ **包括它还没提供的那些**；`allow` \| `ask` |
| | `ask` | `[]string` | `[]` | 这个 server **自己的**工具名里要先点头的那些 |

★ `timeout` 是**宽松**时长：写错退回默认并记 `deferred`，
  与 `debounce` / `start_timeout` 同理。写错 timeout 改变的是「等多久」，
  不是「会发生什么」—— 它不该让程序起不来。

★ `tools: []` 是**全部**而不是**一个都没有** —— 一个表示「什么也没有」的空白名单
  与「键缺失」分不出来。

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
  （`agentkit/build/tools.go:39`）。反过来也成立，且是一条规则：
  **只有 MCP 工具可以用 `mcp__` 前缀**。

★ **合成名永远不被读回来** —— 策略层只做整名与前缀匹配，
  所以工具段能保留原始大小写与字符。

#### 「文件在不在」就是开关

没有 `enabled` 字段。删掉文件就是删掉这个 server —— 与 §6.6 同一条规则，
开关只待在一个地方。

#### 与策略层的衔接

★ **这一族工具的档位写在 server 自己的文件里**，用 server 自己的词汇 —— 典型写法：

```yaml
# .openheron/mcps/github.yaml
tier: ask                       # every tool of this server asks first
```

★ 「写 `tier: ask`」就是「这个 server 提供的每个工具都要点头」—— 给**一整类**外部工具定档，
不必先把工具名抄下来。

★ **而且它覆盖 server 还没提供的工具。** server 的工具清单只有运行期才知道，所以一条点名了
工具的规则覆盖不了 server 明天才加的工具。§6.3 的前缀语法对 `ask` 档永远做不到这件事 ——
它在那里只剩某个 agent 的 **`disabled_tools`** 的用武之地（`mcp__*` = 这个 agent 一个 MCP
工具都不要）。

★ **要把 server 里的某一个工具拿掉，由 server 自己的 `tools:` 清单把它漏掉** —— 用 server
  自己的词汇，因为那个决定就该在那里做：

```yaml
# .openheron/mcps/github.yaml
tools: [create_issue, list_issues]
```

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

完整路径 —— 声明凭据的 mixin、换上真值的宿主侧代理、以及机制够不着的情况 ——
见 `docs/mcp-design.md` §8.1。

#### 实施状态

★ **这一节整节都还没实现**：没有 MCP 依赖、没有 `mcps/` 分区、没有加载器、
  没有 provider。`agentkit/build/tools.go:35` 有一行占位注释
  `// mcpToolProvider{}, projectToolsProvider{}, ...` 记着这件事。
  完整设计与改动清单见 `docs/mcp-design.md`。

★ 上面那个例子里的档位键本身也只是提案（§6.6）：今天的策略表只做整名匹配
  （`agentkit/build/policy.go:41`），工具的档位仍来自那一份中心清单。

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
原字符串，openheron 侧的 `Get` 内部按 `ParseRef`（`agentkit/spec/load.go:251`）拆成
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
| `llm/` `agents/` `policy/` `tools/` | ✅ | 已在 `agentkit/spec/reload.go` 实施：`HotDirs` + 轮询 + 换代 |
| `mcps/` | ✅（★ 决策已定，**未实施**） | 见 §6.7 —— 改一个 server 的命令、它的超时**或它的档位**，下一轮就该生效，不必重启 |
| `server/` | ❌ | 配进程自身（listen / 静态目录），改了要重启 |

★ 这条边界**有测试守着**（`TestWatchIgnoresServerDir`，
  `agentkit/spec/reload_test.go:233`）—— 改 `server/config.yaml` 不该触发换代。
  这份清单是**推导出来的**，不是写了两遍：`Categories`
  （`agentkit/spec/config.go:40-66`）带着 `hot` 标志，`hotDirList()`
  （`agentkit/spec/config.go:77`）把它变成 `HotDirs`（`agentkit/spec/reload.go:21`）。

★ `tools/` 是自己独立的一个分区，就跟 `policy/` 一样：写完 `tools/http.yaml`，
  下一轮就用新的方法白名单与超时，中间不必重启。

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
而 `watch` 住在 `server/` 下面（`agentkit/spec/decl/decl.go:274`）—— 那不是热分区。
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
（`agentkit/spec/config.go:265`，在 `agentkit/spec/config.go:262` 的校验之后）。

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
（`agentkit/spec/config.go:262`），`Reloader` 不调用 `onChange`，管理器保留旧一代
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
| `llm/common` **没有任何 openheron 依赖** | 它是叶子包：在这里 import `obs` 会破坏「协议层不依赖运行时」这条规则 —— 标准日志器**只**在这一处被允许 | `go list -deps ./llm/common \| grep '^github.com/ouranoshong/openheron/'` 应只打印 `llm/common` 自身 |
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

★ 与上游设计相比多了**两个**依赖：`agentkit/obs`（唯一的日志出口 —— `spec` 经它打印
热更新结果）与 `agentkit/spec/decl`（承载 yaml 标签的包；即「声明与行为分离」那条
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
| 10 | （提案，**未实施**）MCP 工具一律带 `mcp__` 命名空间，且**只有** MCP 工具能用这个前缀 | 没有命名空间，一个暴露 `ls` 的 server 会让整个 agent 起不来（撞上保留工具名即在加载期报错）；没有「只有 MCP 能用」这一半，内置工具就能占了这个命名空间，策略里的前缀规则会悄悄盖住它 |
| 11 | （提案，**未实施**）某个 agent 的 `disabled_tools` 只支持**尾部** `*` 的前缀模式，其余写法加载期报错 | 一条读起来与实际覆盖范围不同的规则，就是一次静默的错配 |
| 12 | （提案，**未实施**）档位写在**工具自己的文件与自己的词汇里**，而且**没有全局清单**能与它矛盾 | 一个工具的权限就是一处的一个事实；一个说 `post` 的文件就说 `ask: [post]`，不必在两套词汇之间来回翻译 |
| 13 | （提案，**未实施**）文件系统工具有了自己的文件 `tools/filesystem.yaml`，而它是**调音旋钮** —— 文件不在意味着用默认那套工具与默认那些档位 | 文件系统工具是 agent 的核心能力，不是可选集成；这个文件让它们的档位看得见、改得动，又不让它们变成可选项 |

★ 第 **10**、**11** 两行是**提案**：没有 MCP 依赖、没有 `mcps/` 分区、也没有加载器（唯一
的痕迹是 `agentkit/build/tools.go:35` 的占位注释），而策略表今天是**精确匹配**
（`agentkit/build/policy.go:39` 与 `agentkit/build/policy.go:94`）。见 §6.3 与 §6.7。

不显眼的那几行，核实方式如下：

| # | 怎么核实的 |
|---|---|
| 1 | `decodeStrict` 逐文件解到**预填充**的结构体上（`agentkit/spec/config.go:398`），而整个进程只读**一个**配置根（`agentkit/spec/config.go:157`） |
| 2 | `server.data_dir` 按**配置根**解析（`agentkit/spec/config.go:224` `DataDir`）；`server.static_dir` 与 `kb.corpus` 按**项目根**解析（`agentkit/spec/config.go:238` `ResolveDir`）。★ 两个基准，各自写明在 §6 —— 笼统写成「一律按项目根」对 `data_dir` 是错的 |
| 3 | 写入门拒绝字面量 `api_key`（`agentkit/spec/write.go:473` `checkSecret`） |
| 4 | `decodeStrict` 打开了 `yaml.Decoder.KnownFields`（`agentkit/spec/config.go:398`） |
| 5 | `main.go:198` 在加载时逐条打印；这些条目由 `Config.deferKey` 产生（`agentkit/spec/config.go:134`） |
| 6 | `llm/<provider>.yaml` 的 `type` 只检查非空（`agentkit/spec/load.go:28`），`extra` 直接交给 OpenAI 兼容的 ACL（`llm/common/option.go:20`）—— 这里只有**一种**端点方言，所以「不支持的 provider」根本不会发生 |
| 7 | **默认**的配置根不存在时会被创建（`agentkit/spec/config.go:187`）；而 `agents/main.md` 缺失**是**启动期错误（`agentkit/spec/load.go:270`） |
| 8 | `ParseRef`（`agentkit/spec/load.go:251`）与 `validate`（`agentkit/spec/load.go:269`） |
| 9 | `model.WithModel` 是一个请求级选项（`llm/common/request.go:32`）；实例缓存以 agent 名 + 代际为键（`agentkit/build/pool.go:31`） |

---

## 11. 测试矩阵

★ 下表是**实际存在的用例**（`agentkit/spec` 包 **141 个** `Test` 函数），不是计划。从未
落地的用例已被删掉或换成真正覆盖同一行为的那个 —— 列一个不存在的测试比不列更糟。

### 11.1 分层与优先级

| 用例 | 断言 |
|---|---|
| `DefaultsSurviveWhenFileOmitsKeys` | 只写 `listen_addr` 时，`max_retries` / `debounce` / `lease_ttl` 仍保留默认（**预填充生效的直接体现**） |
| `TheOnlyFileInTheRootIsTheOneThatCounts` | 配置根里那份文件说了算，`models` 不与别处合并 |
| `MissingConfigDirectoryIsNotAnError` | 无 `.openheron/` 时加载不崩 —— 它是校验失败，而且错误**点名缺失的 `agents/main.md`** |
| `RelativePathsResolveAgainstProjectDir` | `resolvePath` 按**配置根**解析（`agentkit/spec/config.go:424`）；绝对路径原样。★ 这个函数今天**没有生产调用方**（`DataDir` 自己拼路径，`agentkit/spec/config.go:224`）—— 它钉的是规则，不是一条活的路径 |
| `ProjectRootAndResolveDir` | `ProjectRoot()` 是 `.openheron` 的父目录，与 `Root()` 不同；`ResolveDir` 相对项目根解析，绝对路径原样，空串原样 |
| `TheConfigRootIsNeverSearchedForUpwards` | ★ 配置根**不**上溯：从子目录出发不会命中祖先那份 `.openheron/`，根就是项目根自己的那个目录 |
| `AnExplicitRootWinsOverTheEnvironment` | 显式的 `--config-dir` 压过 `$OPENHERON_CONFIG_DIR`，而且它**不存在时不会被创建** |
| `TheEnvironmentWinsOverTheProjectRootDir` | `$OPENHERON_CONFIG_DIR` 压过项目根自己的那个目录，同样**不会被创建** |
| `UnlistedToolDefaultsToAllow` | 出厂策略下内置只读工具 allow、变更类 ask、未列名 **allow** |

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
| `UnknownModelRefDoesNotFallBack` | 引用未声明的模型 ⇒ 报错，**不静默退化** |
| `SubagentGraphIsAccepted` | 合法拓扑能加载 |
| `UnknownSubagentIsRejected` | `subagents` 引用不存在 ⇒ 报错并列出已声明的 |
| `SubagentGraphCycleIsRejected` | 成环 ⇒ 报错并给出**环路径** |
| `DeclaredDepthOverLimitIsRejected` | 深度超限 ⇒ 报错并指向 `agent.max_depth` |
| `RaisingMaxDepthAcceptsDeeperGraph` | 调大上限后可加载 |
| `SubAgentSpecsMirrorTopology` | 规格树拓扑 + `disabled_tools` + 档位名 + `temperature` / `max_tokens` 都带上 |
| `DenyAndAskComeFromTheSameFile` | `deny` 与 `ask` 出自同一份 `permissions.yaml` |
| `AgentBodyBecomesInstruction` | 正文成为 `Instruction` |
| `FrontmatterAndBodySplit` | frontmatter 切分；**正文原样保留**（含空行后的后续段落） |
| `FrontmatterUnterminatedIsError` | 缺结束分隔符报错 |
| `ModelReasoningVocabularyAndDefault` | `reasoning` 词汇表与 `reasoning_default` 正确解析；`LevelFields` 能取出档位字段 |
| `LegacyThinkingAndEffortAreRejected` | 旧名 `thinking` / `effort` 被**严格模式拒绝** —— 改名不留静默别名 |

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

★ **`go test ./...` 是绿的**：根模块里 **902** 条顶层用例，**20** 个包有测试。

★ **`go test -race ./...` 也是绿的** —— 它曾经不是，而检测器报出的那四处竞态值得写
下来，因为它们是同一个错误的不同形态：**锁就在那儿，只有一条路径没走它。**

| 包 | 竞的是什么 | 现在是什么 |
|---|---|---|
| `agentkit/run` | `Turn.fail` 写 turn 的失败原因（`agentkit/run/turn.go:217`），而另一个 goroutine 在读它 —— 失败这条路可以从事件循环、被抢占的那一轮、关闭路径与启动超时四处到达，而它们会重叠 | 一把互斥锁护住该字段，**原因保持原来的语义**：最后落地的那个失败才是 `Wait` 报告的，所以被抢占那条路仍然回答 `ErrSuperseded` |
| `api` | 读入一份 transcript 会改写会话的 id（`storage/filestore/transcript.go:70`），而另一个 goroutine 在读那个 id（`storage/filestore/session.go:171`）—— id **不是**不可变的，因为 transcript 的 header 才是权威 | 互斥锁移到结构体顶部，现在也护住身份字段；accessor 在锁内读它，id 在锁内读、磁盘操作在锁外做 |
| `kb` | store 在锁外写自己的状态（`kb/store.go:504`、`kb/store.go:653`），而一次检索在读它 —— 其中一处写的是**派生**值（catalog 文件的 stat），另一处写的是另一 goroutine 原地改的那张 map 的**浅拷贝** | stat 在加锁前读出、在锁内赋值；派生状态在读锁内取**深拷贝**快照，因为浅拷贝仍然共享那张 map |
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
| `agents[].reasoning` | ✅ | — | ✅ | ★ 但**档位不在词汇表时不报错，也没有任何报告** —— `LevelFields` 什么也没找到（`agentkit/spec/decl/decl.go:67`），思考参数就悄悄不生效。这是**唯一一个**「配了不生效、而且看不见」的残留（§12.1） |
| `models[].reasoning_default` | ✅ | — | ✅ | agent 没写 `reasoning` 时由 `RequestOptionsFor` 兜底（`agentkit/build/provider.go:100`） |
| `models[].reasoning_field` / `models[].reasoning_field_from` | ✅ | — | ✅ | 端点自己的字段名，以及值从哪里读（`agentkit/build/provider.go:146`） |
| `models[].context_window` | ✅ | ✅ | ✅ | ★ **openheron 把它接上了**：它是压缩阈值的分母 —— `SummarizeAt` 取窗口的一个百分比（`agentkit/spec/summarize.go:25`）。阈值属于 agent，窗口属于模型；模型不声明窗口 ⇒ 该 agent 的压缩不生效，这件事会进 `deferred`（`agentkit/spec/load.go:310`）。它也出现在自省响应里（`api/v2_introspect.go:265`） |
| `models[].kind` / `models[].dimensions` | ✅ | ✅ | ✅ | 只对知识库有意义：`tools/kb.yaml` 配了语料时，`kind: embedding` 与维度数会被校验（`agentkit/spec/load.go:331`） |
| `models[].summarize_at` | ✅ | — | ❌ | **已废弃**：阈值属于 agent，不属于模型。这个键仍被解析，好让旧配置继续能加载，并且它会进 `deferred`，并指向 agent 自己的文件（`agentkit/spec/load.go:60`） |
| `llm/<provider>.yaml` 的 `header:` | ✅ | — | ❌ | 被解析（`agentkit/spec/decl/decl.go:25`）却**从不被消费** —— `FromConfig` 没有把它交给端点（`agentkit/build/provider.go:36`）。全仓没有任何地方读它（§12.1） |
| `server.data_dir` | ✅ | ✅ | ✅ | 相对**配置根**解析（`state` → `<root>/.openheron/state`，`agentkit/spec/config.go:224`），绝对路径原样；目录不存在时创建 |
| `server.static_dir` | ✅ | ✅ | ✅ | 经 `Service.ResolveDir` 按**项目根**解析（`agentkit/spec/config.go:238`）—— 既不是 cwd，也不是配置根；目录不存在只打一条日志（`main.go:347`） |
| `server.lease_ttl` | ✅ | ✅ | ✅ | 一次运行的租约在无进展时能存活多久；它必须**长于 provider 自己的超时** —— provider 的超时结束的是一次卡死的调用，租约只需要比一次慢调用活得久（`agentkit/spec/decl/decl.go:267`） |
| `agents[].skills` | ✅ | ✅ | ✅ | 这个 agent 可用的技能白名单；**不写**表示 skills 目录下的全部技能，**写空列表**表示一个都不要 —— 指针就是用来区分这两者的（`agentkit/spec/decl/decl.go:94`） |
| `SKILL.md` 的 `agent:` / `model:` | ✅ | — | ✅ | 由 `agentkit/build` 包的 `ModelHub` / `AgentHub` 实现，见 §7。它们不是 openheron 配置项，而是 skill 自己的 frontmatter（`agentkit/build/hub.go:64`、`agentkit/build/hub.go:106`） |

**仍然不生效的**

- `models[].summarize_at`：已废弃。★ 它**不是**隐形的 —— 它会进 `deferred`，并把
  agent 自己的文件名点出来。
- `llm/<provider>.yaml` 的 `header:`：被解析，从不被消费，**也没有任何报告**。★ 这是配置层
  剩下的唯一一个「配了不生效、而且看不见」。

---

## 12. 不在本轮范围

| 项 | 理由 | 前置条件 |
|---|---|---|
| RAG / knowledgebase | **已由 `kb` 包实施**，见 [architecture.md](architecture.md) §9 | — |
| MCP / LSP 集成 | openheron 无此需求；MCP 集成是提案 —— 见 [mcp-design.md](mcp-design.md) | — |
| 中间件参数外露（`middlewares/*.yaml`） | openheron 中间件固定，无参数外露需求 | — |
| 配置写入门（`.config.lock`） | **已被配置编辑器推翻** —— 那六条 `/v2/settings` 路由就是写入口（`api/v2_files.go:67`） | — |
| ~~`policy/limits.yaml` 的语义~~ | **已实施** —— 它是**限制规则**，见 §12.2 | — |

### 12.2 `policy/limits.yaml`：限制规则（**已实施**）

〔历史〕这个文件名曾经在四处在场却**没有任何一处定义它的内容**：加载器认出它但不解析、
写入门拒绝它、唯一的测试夹具是一段占位文本。当时的结论是「等规格，不是等工期」——
因为实现它的第一步是**定义它管什么**，而那一步没做。

**定案**：它是**限制规则** —— 数量上的上限。

#### 它与 `permissions.yaml` 的分工（正交）

| 文件 | 回答的问题 | 例子 |
|---|---|---|
| `policy/permissions.yaml` | 这个工具**还能不能跑**（外层边界） | `skill` → `deny` |
| `policy/limits.yaml` | **最多能做多少**（额度上限） | 嵌套深度 ≤ 1 |

一个管「允许吗」，一个管「到这就停」。两份文件可以同时存在而互不干扰。

★ 「要不要人点头」这个问题**不再**由这里回答 —— 它属于工具自己的文件（§6.6），
  就写在那个工具旁边。

#### 语法

```yaml
agent:
  max_depth: 2      # maximum nesting depth; the root agent is level 0
retry:
  max_retries: 3    # maximum number of retries for a model call
```

严格解析：**未知键报错**（与其它分区同一套纪律），目录里的未知文件名字面量也报错
（`agentkit/spec/load.go:207`）。

#### ★ 合并语义：只能收紧，不能放宽

`policy/limits.yaml` 的值与 `server/config.yaml` 的同名值取**更严的**；**本级之内**
（`permissions.yaml` 与 `limits.yaml` 之间）也是取更严的。

这与 `permissions.yaml` 的 `deny` 跨层级取**并集**是同一个思路：限制是**安全边界**，
只往严的方向合并。若允许写个更大的值就能解开限制，那「在项目里放一份 `limits.yaml`
写个大数」就成了一条提权路径。

★ **放宽上限请改 `server/config.yaml`**（进程级配置，需重启）。这个分工是刻意的：放宽
是进程级的决定，不该由一个**能热改**的文件来做；而收紧则**立即生效** —— `policy/` 进热
更新，改完保存即换代。

#### 0 与「配了不生效」

- **0 = 不设限**，沿用 `server/config.yaml` 的全局值。它是一个**有意义的**配置，不是
  「上限为 0」。
- 声明一个**比全局基线更宽松**的值是**空操作** —— 它会被记进 `deferred`，**启动时
  打印**：「`policy/limits.yaml agent.max_depth: 5 is looser than the global baseline
  (1); it has no effect — raise server/config.yaml agent.max_depth to widen the
  limit」。不能让它悄悄躺在那儿：用户会以为上限被放宽了。

#### 收紧了但拓扑越界 ⇒ 启动期报错

`limits.yaml` 把深度上限收紧到与已声明的拓扑冲突时，**加载失败**（不是运行时静默截断）。
热更新时同理：**重载失败、旧配置继续服务**，并打一条 warn ——「配了三层但实际只跑两层」
是最难查的一类问题。

#### 落地清单

| 位置 | 做了什么 |
|---|---|
| `agentkit/spec/decl/decl.go` | `Limits` / `AgentLimit` / `RetryLimit`；`DefaultMaxDepth` / `DefaultMaxRetries`（`agentkit/spec/decl/decl.go:215`） |
| `agentkit/spec/load.go` | `loadPolicy` 的 `limits` 分支：**取更严的**合并（`agentkit/spec/load.go:199`、`agentkit/spec/load.go:211`）；`MaxDepth()` / `MaxRetries()` 报生效值（`agentkit/spec/load.go:409`）；`collectNoopLimits()` 把空操作进 deferred（`agentkit/spec/load.go:437`） |
| `agentkit/spec/load.go` | 收紧后的深度与已声明拓扑冲突 ⇒ **加载失败**（`agentkit/spec/load.go:359` `validateDepth`） |
| `agentkit/spec/write.go` | 写入门**放行** `limits.yaml`（`agentkit/spec/write.go:91`）；`LimitsTemplate()` 给编辑器用（`agentkit/spec/write.go:218`） |
| `api/v2_settings.go` | `/v2/settings` 的模板表多一个**按路径**的键 `policy/limits.yaml`（policy/ 下两种文件形态，一个分区模板放不下）（`api/v2_settings.go:143`） |
| `api/v2_introspect.go` | `/v2/config` 的 `server.maxRetries` / `server.maxDepth` 报**生效值**，不是原始值（`api/v2_introspect.go:387`） |
| `web/src/lib/settings-validate.ts` | 校验**按文件名**分派（`validateContent` 的入参从分区改成路径）；`validateLimits()`（`web/src/lib/settings-validate.ts:128`） |
| `web/src/components/settings/new-file-dialog.tsx` | 模板优先按**路径**取，回落到分区模板（`web/src/components/settings/new-file-dialog.tsx:134`） |
| `.openheron-example/policy/limits.yaml` | 出厂样例（全 0 = 不设限） |

★ **服务端仍然是模板与语义的唯一真相源**：前端只知道「这个文件该长什么样」，而「合并后
仍成立」永远由服务端的试合并来答。

另有两处**刻意保留的环境变量**：`build.ConfigFromEnv` 是过渡形态（主路径已走
`FromConfig`），`EINO_EXT_SKILLS_DIR` 降级为 `.openheron/skills` 的覆盖项。

★ **实施时补的一条**：`server/config.yaml` 新增 `agent.max_depth`（默认 1）。声明的拓扑
深度超过上限时**启动期报错**而非运行时截断 ——「配了三层但实际只跑两层」是最难查的一类
问题。

### 12.1 已知待收敛项（★ 实施时暴露，均非阻塞）

| 项 | 现状 | 建议 |
|---|---|---|
| ~~**业务热更新未完整接线**~~ | ✅ 已闭环：管理器换内容 → 下次 `Build` 拉当前代际 → 快照/池自然换代；不再有 `Reconfigure` 这条人工推送线 | 见 `main.go:196`（重载回调换掉管理器的内容）与 `agentkit/build/pool.go:31`（pool 以 agent 名 + 代际为键） |
| ~~**每 agent 一个模型实例**~~ | ✅ 非缺陷，是设计：实例粒度 = provider × model × reasoning 档位 × 端点字段形状 × timeout，链头是 agent。跨 agent 共享需要一份以这五项为键的缓存 = 第二真相 | 见 §6，以及 §2 里「模型构造」那一行 |
| ~~**`agentkit/spec` 依赖 eino**~~ | ✅ **已消除**：配置只声明模型引用；模型实例在 agent 装配期按引用解析。`agentkit/spec` 重新成为纯声明层 | `go list -deps ./agentkit/spec \| grep -c cloudwego/eino` = **0** |
| 模型没有声明的思考档位 | agent 写了一个不在 `llm/<provider>.yaml` 的 `reasoning:` 词汇表里的档位名：不报错，也**没有 `deferred` 条目** —— `LevelFields` 什么也没找到（`agentkit/spec/decl/decl.go:67`） | 剩下的唯一一个「配了不生效、而且看不见」；词汇表在 `llm/`，档位名在 `agents/`，两个文件从不互相核对 |
| `llm/<provider>.yaml` 的 `header:` | 被解析，从不被消费（`agentkit/spec/decl/decl.go:25`） | 要么把它交给端点，要么把这个键从文件规格里去掉 |
| 已解析未消费的字段 | 见 §11.6 | 按需接线 |

### 实施补充的两处偏离

| 偏离 | 原设计 | 实际 | 原因 |
|---|---|---|---|
| `watch.debounce` 的时长类型 | 与 `timeout` 共用一个 `Duration`（一律报错） | 拆成 `LenientDuration`（写错降级 + 记 deferred） | 两者性质不同：`timeout` 写错会让请求超时行为不可预测，**必须当场报错**；`debounce` 只影响「多久算保存完了」，不该让程序起不来。**降级必须出声**，所以记进 `deferred`。★ 在 openheron 里 `LenientDuration` 不只管 `debounce`，还管 `start_timeout`、`lease_ttl` 以及三个 `tools/` 分区的超时；严格的 `Duration` 只留给 provider 与 model 的 `timeout`（`agentkit/spec/decl/decl.go:266`） |
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
| 配置根不上溯 | 读 `NewService` 的注释（`agentkit/spec/config.go:144`）+ 用例 `TestTheConfigRootIsNeverSearchedForUpwards` 与 `TestNewServiceDoesNotProbeUpwards` |
| `agentkit/spec` 不牵进任何框架 —— 只有标准库加 `yaml.v3` | `agentkit/spec/config.go:3` 的 import |
| 未知字段报错 | `decodeStrict` 打开 `yaml.Decoder.KnownFields`（`agentkit/spec/config.go:398`） |
| `deferred` 列表在启动时被打印 | `main.go:198` |
| `agentkit/spec` 的顶层用例数 | `grep -rhoP '^func \KTest\w+' agentkit/spec/ \| sort -u \| wc -l` → **141** |
| 全仓顶层用例数 | `grep -rhoP '^func \KTest\w+' --include='*_test.go' . \| sort -u \| wc -l` → **903**，其中一条属于嵌套模块 `sandbox-kit/examples/demo-project` ⇒ 根模块里是 **902** |
| 根模块里有测试的包数 | `go list -f '{{.ImportPath}} {{len .TestGoFiles}} {{len .XTestGoFiles}}' ./... \| awk '$2+$3>0' \| wc -l` → **20** |
| 整个测试套件是绿的 | `go test ./...` → 每个包都是 `ok` |
| `-race` 现在是绿的 | `go test -race ./...` → 每个包都是 `ok`；修复过程见 §11.5 |
| `policy/limits.yaml` 只能收紧 | 读 `mergeLimits` / `tighter`（`agentkit/spec/load.go:211`）+ 用例 `TestLimitsTightenButNeverLoosen` 与 `TestLimitsCannotLoosenTheGlobalBaseline` |
| 空操作的上限会进 `deferred` | `collectNoopLimits`（`agentkit/spec/load.go:437`）+ `TestLooserLimitIsReportedAsDeferred` |
| 生效的深度与重试次数 | `MaxDepth()` / `MaxRetries()`（`agentkit/spec/load.go:409`）+ `agentkit/spec/config_test.go` 里的用例 |
| `models[].context_window` 真的生效 | `SummarizeAt`（`agentkit/spec/summarize.go:25`）+ `TestSummarizeAtResolvesAgainstTheWindow` |
| `models[].summarize_at` 已废弃但仍被解析 | `decl.Model.SummarizeAt` 上的注释（`agentkit/spec/decl/decl.go:45`）+ 那条 `deferred`（`agentkit/spec/load.go:60`）+ `TestTheOldModelThresholdIsReportedNotFatal` |
| `llm/<provider>.yaml` 的 `header:` 没有消费方 | `decl.Provider.Header`（`agentkit/spec/decl/decl.go:25`）与 `FromConfig`（`agentkit/build/provider.go:36`）—— 全仓没有任何地方读这个字段 |
| 模型实例是每 agent 一个，不是每模型一个 | pool 的键（`agentkit/build/pool.go:31` `GetOrBuild(ctx, name, gen, fn)`） |
| `AgentHub` / `ModelHub` 已实施 | `agentkit/build/hub.go:64`（`skillModelHub`）与 `agentkit/build/hub.go:106`（`skillAgentHub`） |
| `ConfigFromEnv` 住在 `agentkit/build` | `agentkit/build/config.go:15` |
| `EINO_EXT_SKILLS_DIR` 只是一个兜底 | `resolveSkillsDir`（`agentkit/build/skill.go:188`） |
| 那六条 `/v2/settings` 路由 | `api/v2_files.go:67` |
| `policy/limits.yaml` 的模板键 | `api/v2_settings.go:143` |
| 前端按路径分派校验 | `web/src/lib/settings-validate.ts:128` |
| 前端优先按路径取模板 | `resolveTemplate`（`web/src/lib/settings-validate.ts:108`），由 `web/src/components/settings/new-file-dialog.tsx:134` 调用 |
| `context_window` 在自省响应里可见 | `api/v2_introspect.go:265` |
| `spec` → `run` 的破例在这里从未存在 | `git grep -n "agentkit/run" fa7131a -- agentkit/spec` → 一条注释，别无其它 |

## 附录 B：实施状态

| 项 | 状态 | 位置 |
|---|---|---|
| 单根加载 | ✅ | `agentkit/spec/config.go:157` `NewService`，`agentkit/spec/config.go:273` `loadOnly` |
| 预填结构体逐层 decode（默认值存活） | ✅ | `agentkit/spec/config.go:279`（`defaultServer()` / `defaultPolicy()` / `defaultKB()` 预填） |
| 严格模式（未知字段带行号报错） | ✅ | `agentkit/spec/config.go:398` `decodeStrict` |
| `deferred` + 强制打印 | ✅ | `Config.Deferred()`（`agentkit/spec/config.go:115`），`main.go:198` 启动时逐条打 |
| 配置根三级解析（显式 → env → 项目根自己的目录） | ✅ | `agentkit/spec/config.go:144` |
| **不上溯** | ✅ | `NewService` 的注释（`agentkit/spec/config.go:152`）；`TestTheConfigRootIsNeverSearchedForUpwards` |
| `${ENV}` 缺则提醒（不阻塞）+ `@file:` | ✅ | `agentkit/spec/expand.go:9` `expandValue`，`agentkit/spec/load.go:41` |
| `Duration` 强制带单位 | ✅ | `agentkit/spec/decl/decl.go:385` |
| `LenientDuration` 降级并记 `deferred` | ✅ | `agentkit/spec/decl/decl.go:325`，`agentkit/spec/reload.go:80` |
| frontmatter 切分 | ✅ | `agentkit/spec/expand.go:48` `splitFrontmatter` |
| 模型引用校验（不静默退化） | ✅ | `agentkit/spec/load.go:251` `ParseRef`，`agentkit/spec/load.go:269` `validate` |
| `subagents` 图校验（缺引用 + 成环） | ✅ | `agentkit/spec/load.go:453` `validateAgentGraph`，`agentkit/spec/load.go:470` `detectCycle` |
| `deny` 取并集；`ask` 是**替换** | ✅ | `agentkit/spec/load.go:196`（`deny` 的 `union`）与 `agentkit/spec/load.go:197`（`ask` 是赋值，不是合并） |
| 兜底 `allow` | ✅ | `decl.Policy.TierOf`（`agentkit/spec/decl/decl.go:240`） |
| `envOr` → `cfg.*` | ✅ | `main.go:459` `override`（用在 `main.go:341`） |
| `.openheron/state/` 取代 `./data` | ✅ | `config.Service.DataDir("")`（`agentkit/spec/config.go:224`） |
| `MaxRetries` 配置化 | ✅ | `decl.RetryCfg.MaxRetries`（`agentkit/spec/decl/decl.go:348`）→ `Config.MaxRetries()`（`agentkit/spec/load.go:416`） |
| skills 自动探测 `.openheron/skills` | ✅ | `agentkit/build/skill.go:188` `resolveSkillsDir` / `probeProjectSkills` |
| 写入门拒绝字面量密钥 | ✅ | `agentkit/spec/write.go:473` `checkSecret` |
| `policy/limits.yaml`（限制规则） | ✅ | `agentkit/spec/load.go:199`，`agentkit/spec/decl/decl.go:215` |
| ~~`chatmodel.New` 直读 env~~ | ✅ 已删 | 包已移除，改 `llm/common` |
| `mcps/` 分区 + MCP provider | ❌ **未实施** | 架构设计见 [mcp-design.md](mcp-design.md)；配置面见本文件 §6.7 |
| 策略前缀匹配（尾部 `*`） | ❌ **未实施** | 匹配语义见本文件 §6.3 |

**已落地**：`AgentHub` / `ModelHub`（§7）—— 实现在 `agentkit/build/hub.go`
（`skillAgentHub` / `skillModelHub`），由 `agentkit/build/skill.go` 接线；`context: fork`
的 SKILL.md 会触发它（用例见 `agentkit/build/hub_test.go`）。

另有两处**刻意保留的环境变量**：`build.ConfigFromEnv` 是过渡形态（主路径已走
`FromConfig`），`EINO_EXT_SKILLS_DIR` 降级为 `.openheron/skills` 的覆盖项。

**实施时补的一条**：`server/config.yaml` 新增 `agent.max_depth`（默认 1）。声明的拓扑
深度超过上限时**启动期报错**而非运行时截断 ——「配了三层但实际只跑两层」是最难查的一类
问题。

## 附录 C：把这篇文档落进 openheron 时发现并修正的若干处

| 上游的说法 | openheron 的实际 | 原因 |
|---|---|---|
| 配置根按「env → 上溯 → 自身」解析 | 它恰好按**三级**解析：显式 `--config-dir` → `$OPENHERON_CONFIG_DIR` → `<projectRoot>/.openheron`（不存在则创建），并且**刻意没有**上溯祖先目录（`agentkit/spec/config.go:144`） | 一旦配置根取决于「进程是从哪儿启动的」，同一条命令换个目录跑就会读到另一份配置 —— 测试实例也会读到开发者的那份（见 `AGENTS.md`） |
| 测试实例靠「预置一份 `.heron/`」来隔离 | 隔离靠 `.env.test` 加 `make test-env-backend` / `test-env-reset` / `test-env-sync` 三个目标：实例跑在 `:18099`，**自带配置根与自带会话目录** | 端口不同不等于数据隔离 —— openheron 把会话写在 `.openheron/state/` 下，一个写在那儿的测试会污染真实历史 |
| `agentkit/spec` 包有 **115** 个 `Test` 函数 | 它有 **141** 个 | 包随代码一起长大；上表列的是真正存在的用例 |
| 模块路径是 `heron/...` | 是 `github.com/ouranoshong/openheron/...` | `go.mod` |
| `agentkit/spec` 的 heron 依赖只有 `persist` | 有**四个**：`agentkit/obs`、`agentkit/spec/decl`、`persist` 与它自己 | `obs` 是唯一的日志出口；「声明」（`decl`）与「行为」（`spec`）的拆分是 openheron 自己的设计 —— 见 [architecture.md](architecture.md) §2.1 |
| [architecture.md](architecture.md) §2.2 说 `agentkit/spec` import `kb` | **生产代码**并没有 —— 只有用例文件 `agentkit/spec/policy_contract_test.go:7` 这么做 | 校验知识库配置只需要声明（`decl.Model.IsEmbedding`），不需要 `kb` 包本身；那份文档 §2 的依赖表把测试算进去了 |
| `llm.ConfigFromEnv` | 它是 `build.ConfigFromEnv`（`agentkit/build/config.go:15`） | 配置包不能依赖厂商协议层，而 `llm/common` 是零 openheron 依赖的叶子包 |
| 模型结构体 | openheron 的 `decl.Model` 带 `kind`、`dimensions` 与 `context_window`；`summarize_at` **存在但已废弃** —— 阈值属于 agent（`decl.Agent.SummarizeAt`） | 阈值是 agent 的策略，窗口是模型的 —— 两者只在一个地方相遇（`agentkit/spec/summarize.go:25`） |
| agent 结构体 | openheron 的 `decl.Agent` 带 `skills`，类型是 `*[]string`：不写 = 全部技能，空列表 = 一个都不要 | 「不写」与「写空」必须分得开，否则「一个技能都不要」根本表达不出来 |
| server 结构体 | openheron 的 `decl.Server` 带 `lease_ttl`（还有 `frontend_mode`） | 租约的预算必须长于 provider 自己的超时，而两者住在不同的文件里 |
| `internal/atomicfile` | 是 `persist/atomicfile.go`，一个**顶层**叶子包 | openheron 没有 `internal/` 目录；`persist` 装着 `AtomicWrite` 与 `FileLock` 一族 |
| `kb` 不 import 任何 heron 包 | `kb` import 了 `obs` 与 `persist` | `obs` 是唯一的日志出口，知识库也需要那个共用的原子写 |
| `agentkit/spec` → `agentkit/run` 的破例在上游已于 2026-10-21 撤销 | openheron **从未有过** —— 代码面是在撤销之后抽出来的，`agentkit/spec` 提到 `agentkit/run` 的地方只有一条注释 | 撤销是上游的一个设计决定；openheron 直接继承了撤销后的形态 |
| `models[].context_window` 是纯元数据，不生效 | 它**生效**：它是压缩阈值的分母（`agentkit/spec/summarize.go:25`） | openheron 实施了压缩与上下文用量看板 —— 见 [context-usage-design.md](context-usage-design.md) |
| agent 写了模型没有声明的思考档位会进 `deferred` | 不会 —— 它被静默忽略 | 词汇表在 `llm/`，档位名在 `agents/`，两个文件从不互相核对 |
| `go test -race ./...` 全仓是绿的 | 它**曾经不是**：竞态检测器在 `agentkit/run`、`api`、`kb`、`agentkit/spec` 报了竞态 | `spec` 里那处在测试自己身上；四处现在都修好了，§11.5 逐条记着各自是什么 |
| `tools/` 里的坏超时会被报成 `watch.debounce` | 这个标签是错的：`collectBadDurations` 给**每一条**坏时长都加上前缀 "watch.debounce fell back to default"，于是 `tools/http.yaml` 里的坏 `timeout` 落到了同一个标签下 | `decl.BadDurations()` 是一个不带来源信息的包级列表（`agentkit/spec/decl/decl.go:338`） |

