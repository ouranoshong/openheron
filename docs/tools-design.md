# 工具与集成设计

> 一句话：工具是**有档位的**，来源是**文件**；MCP 只是另一种来源。

> 这份文档讲四件事：工具从哪来、文件工具的边界在哪、MCP 怎么变成工具，
> 外加一层把这些工具整体关进沙箱的 Kit。
> 每个结论都指向具体文件与行号。本文按 AGENTS.md 的文档语言规则**只有中文版** ——
> 架构与代码设计类文档不做双语。

## 0. 一句话

一个 agent 能用什么，由**四把钥匙**决定；一个工具跑不跑，由**档位**决定。
档位只有两档（`allow` / `ask`），缺省是 `ask`，例外由**提供这个工具的那个文件**点名。
MCP 服务器是另一种来源：它带的工具进同一张档位表，只是暴露名多了一层命名空间。

## 1. 工具从哪来

### 1.1 四把钥匙，两种风格

一个 agent 的工具面由四个键共同决定，三个在 agent 自己的 frontmatter 里，
一个在**工具自己的文件**里：

| 键 | 位置 | 缺省 | 管什么 |
|---|---|---|---|
| `tools` | `agents/<id>.md` | **一个都没有** | 这个 agent 能用哪些**非 MCP 工具**（内置工具、`tools/` 下各文件提供的、`config_*`） |
| `mcps` | `agents/<id>.md` | **一个都不挂载** | 挂哪些服务器；挂载一个就把该 server 的**全部**工具交给这个 agent |
| `skills` | `agents/<id>.md` | **一个都没有** | 这个 agent 能加载哪些技能 |
| `allows` | `tools/*.yaml`、`mcps/<server>.yaml` | 代码的缺省清单 | **审批**：哪些工具不用点头 |

字段的落点：`decl.Agent.Tools`（`agentkit/spec/decl/decl.go:113`）、
`decl.Agent.Skills`（`agentkit/spec/decl/decl.go:134`）、
`decl.Agent.MCPServers`（`agentkit/spec/decl/decl.go:150`）、
`decl.SubAgentSpec.MCPServers`（`agentkit/spec/decl/decl.go:958`）。
三个都是**普通切片**，不是指针 —— 「没声明」与「声明了但是空的」是同一件事。

★ **两种风格，而且它们的缺省是反着选的。**

- **白名单**（`tools` / `mcps` / `skills`）：**列出来的才有**。
- **例外清单**（`allows`）：**点名的不用点头**，其余一律要问。

★ 为什么工具缺省关门、审批缺省开门：两件事的失败形态不同。
忘写 `tools` ⇒ agent 什么都干不了，**当轮立刻看得见**；
忘写 `allows` ⇒ 工具仍然可用，只是每次要点头，**在审批卡上看得见**。
默认值该选「错了也看得见」的那一侧 —— 而这两侧的「看得见」方向相反。

★ **旧键 `disabled_tools` 被拒绝，而不是改名**（`agentkit/spec/load.go:107`）。
它与 `tools` 含义正好相反：只改个名就等于把黑名单悄悄变成白名单。
也没有别名 —— 那会让黑名单继续活着。

★ **`tools:` 不点名 MCP 工具。** `mcp__<server>__*` 的条目现在什么也不授予，
它们被丢弃并留下一条**提示**（`agentkit/spec/load.go:943` 的 `checkAgentMCPNames`）——
不是加载期错误，因为语义已经变了，不是自相矛盾。
代价是真实的：**按 agent 收窄 MCP 工具的能力没有了**；收窄的地方变成 server 自己的文件。

★ **一个 agent 的 `tools:` 接受尾部 `*` 的前缀模式**（`decl.ParseToolNamePattern`，
`agentkit/spec/decl/decl.go:665`）。但只有**结尾**的 `*` 是模式，`mcp*foo` 与单独的 `*`
都是加载期硬错误（`agentkit/spec/load.go:113` 的 `validateAgentTools`）：
一条读起来与实际覆盖范围不同的规则，就是一次静默的错配。

### 1.2 名字就是策略：`mcp__<server>__<tool>`

每个 MCP 工具都以带命名空间的名字暴露给模型：

```
mcp__github__read_issue
└─┬─┘└──┬───┘└─────┬─────┘
前缀   服务器段     工具段
```

| 段 | 谁定 | 规则 | 少了它会怎样 |
|---|---|---|---|
| `mcp__` | 协议层定的 | 常量 `decl.MCPToolPrefix`（`agentkit/spec/decl/decl.go:593`） | 服务器暴露一个 `ls` 就顶掉内置 `ls` |
| `<server>__` | **我们定** —— `<server>.yaml` 的文件名 | 常量 `decl.MCPSeparator`（`agentkit/spec/decl/decl.go:602`）；server 名匹配 `^[a-z0-9]+(_[a-z0-9]+)*$`（`agentkit/spec/decl/decl.go:610`） | `mcp__github__ls` 与 `mcp__gitlab__ls` 撞成一个 |
| `<tool>` | **服务器定** | `[A-Za-z0-9_-]` 之外的字符换成 `_`；大小写保持原样（`agentkit/build/mcptools.go:151`） | 带 `.`/`:` 的名字过不了厂商的函数名集合 |

★ **server 名里不许出现连续的下划线。** 否则 `mcp__a__b__x` 有两种读法 ——
那是 server `a` 的工具 `b__x`，还是 server `a__b` 的工具 `x`？
**正则和分隔符是一对**：边界有歧义，前缀规则就会悄悄放大。
不匹配这个形状的文件名是**加载期硬错误**（`agentkit/spec/load.go:473`）。

★ **合成名永远不被读回来拆开。** 策略层只做整名与前缀匹配。
这正是工具段能保留原始大小写与字符的原因。
一个工具属于哪台 server，是从它的**名字**读出来的
（`decl.MCPServerOf`，`agentkit/spec/decl/decl.go:630`）。

名字在**一处**拼装：`mcpExposedName`（`agentkit/build/mcptools.go:141`）。
provider 与面板都走它，所以它们不可能对某个工具叫什么有分歧。

★ **名字不合法时跳过并警告，绝不让进程起不来。** 工具名是**远端服务器**定的，
用户改不了 —— 一个多出一个怪工具的服务器，应该只赔掉那个工具，不该赔掉整个 agent。

| 情况 | 结果 | 理由码 |
|---|---|---|
| 合成名超过 64 个字符（`decl.MCPMaxExposedNameLen`，`agentkit/spec/decl/decl.go:702`） | 跳过那个工具 | `name_too_long` |
| 同一 server 里两个工具规整后同名 | 跳过后来的那个 | `name_collision` |

两个理由码是机器值（`agentkit/build/mcptools.go:27-30`），与错误体里的 `code` 一样；
解释它们的那句英文属于界面，不属于线缆。
被跳过的工具必须**看得见**：日志里写清 server 名、原始工具名与它本来会有的合成名
（`agentkit/build/mcptools.go:244`），面板把它显示成 `skipped` 并带上原因。

★ **只有 MCP 工具可以用 `mcp__` 前缀。** 否则一个非 MCP 工具就能占了命名空间，
而为 MCP 写的前缀规则会悄悄盖住它。

### 1.3 批次、档位与闸门

**批次**：一个快照里的工具集建一次，所有 agent 共享它
（`agentkit/build/snapshot.go:77` 的 `heavyDepsOf`，在一个 `sync.Once` 之下）。
装配路径是一条线：

```
tools/*.yaml → spec.loadTool → decl.ToolsCfg
  → toolRegistry.Build → snapshot.buildHeavy
  → applyToolConfig → deep.Config.ToolsConfig.Tools → deep.New

mcps/<server>.yaml → spec.loadMCP → decl.MCPServerCfg
  → mcpToolProvider.Provide → 同一个 toolRegistry.Build → 同一个 applyToolConfig
```

`toolProvider` 是全部的扩展点（`agentkit/build/tools.go:49`）：
`ID()` / `Optional()` / `Provide()`。
注册表 `defaultToolRegistry`（`agentkit/build/tools.go:65`）里带着 MCP provider。
★ MCP provider 是**唯一**一个需要 manager 的 provider：MCP 客户端活得比 provider
返回的那一批工具更久，所以客户端归 manager 管，不归 provider 管。

★ **`Optional() == true` 是有代价的**（`agentkit/build/mcptools.go:199`）：
provider 那条 `Warn` 是**唯一**的通知 —— 工具不进表，用户看到的是「某个功能好像不见了」。
所以警告里必须写清 server 名、传输方式与错误。

**档位**：只有两档。

| 档位 | 含义 |
|---|---|
| `allow` | 工具直接跑，不需要人点头 |
| `ask` | 工具打断这一轮，等一个人的决定 |

`build.Policy.TierOf`（`agentkit/build/policy.go:55`）按名字查档位：
在 allow 表里就是 `allow`，其余一律 `ask`。
★ **缺省是 `ask`** —— 猜错的代价不对称，多点一次换来的是一次没人看见的破坏性调用不被执行。

例外由**提供这个工具的那个文件**点名，三处合成一张表：

| 来源 | 它点名什么 | 装配位置 |
|---|---|---|
| 没有自己文件的内置工具 | `ls` `read_file` `glob` `grep` `task` `write_todos` `skill` | `agentkit/build/tools.go:95` 的 `builtinAllowedTools` |
| 工具自己的文件 —— `tools/*.yaml` 与知识库 | 那个文件的 `allows:` | `agentkit/spec/load.go:430` 的 `AllowedTools` |
| MCP server 自己的文件 | 那个 server 的 `allows:`，**用它自己的词汇**，翻译成暴露出来的名字 | `agentkit/build/mcptools.go:173` 的 `mcpAllowedNames` |

三处合成的地方是 `agentkit/build/spec.go:73` 的 `policyOf`。
★ **合并之后没有摘除**：例外来自三处，就没有第四处能拿走一个。
`config_write` 改的是配置本身，而它和其它工具一样由 `tools/config.yaml` 点名放行 ——
点名一个工具为例外的规则，是一条得有人记住的规则。

★ **档位与可见性是两个问题。** 「这个工具要不要人点头」是共享的档位表；
「这个 agent 能不能用它」是逐 agent 的 `Table.Blocked`
（`agentkit/build/policy.go:163`），来自它以 `decl.SubAgentSpec` 带在身上的清单。
清单没点名的工具不在工具集里，模型也看不见。
一个 agent 不许用的工具**保住自己的档位**，并在 `reason` 里说清为什么
（`agentkit/build/introspect.go:36` 的 `reasonOf`）：
非 MCP 工具的理由是 `tools`，MCP 工具的理由是 `mcps`。

**闸门**：`agentkit/build/gate.go` 实现「工具能不能用」。

- 名字在 allow 表里 → 直接跑，闸门原样放行（`agentkit/build/gate.go:67`）。
- 名字不在 → 调用中断，等一个人的决定。
- ★ **两条路共用一个决策**：同步那条（`agentkit/build/gate.go:55`）与
  流式那条（`agentkit/build/gate.go:89`），决策在 `agentkit/build/gate.go:126` 的 `decide`。
  ★ 只实现同步那条会漏掉 `execute` —— 它是 filesystem 那批工具里**唯一的流式**工具，
  而它恰恰是档位表上最常被点名的那个。

★ 没有 `deny` 这一支：一个 agent 拿不到的工具不会注册给它，闸门从不需要拒绝谁。
人拒绝一次调用时，工具返回的是一句拒绝说明（带可选理由），**不是**错误 ——
模型看到「被拒了，理由是 X」就可以据此调整。
★ 要跑的参数取自中断时存下的那一份：恢复时模型可能发了别的，工具必须跑**人批过的那条**。

★ **收窄发生在 agent 的表里**，也就是它的 `tools` 与 `mcps` 两份清单已经在决定一切的那个地方
（`agentkit/build/gate.go:33` 的 `BeforeAgent` 在 agent 开跑之前就把工具从
`runCtx.Tools` 里摘掉，所以模型从来看不见它们）。
表拿到这个 agent 的 server 清单，而一个 MCP 工具属于哪台 server 是从**它的名字**读出来的：

| 这个 agent 的 `mcps:` | 表对 MCP 工具怎么答 |
|---|---|
| 缺失 | 一个 MCP 工具都拿不到 —— 它的 server 没挂载 |
| `[github]` | `github` 的工具全部归它；别的 server 的工具一个都不归它 |
| `[]` | 一个 MCP 工具都拿不到 |

## 2. 文件系统后端

`agentkit/fsx` 是 `github.com/cloudwego/eino-ext/adk/backend/local@v0.2.2` 的**有界分支**
（文件头保留上游许可与版本，`agentkit/fsx/fsx.go:1-20`）。

### 2.1 为什么只能在后端这一层改

`deep.Config` 只把 middleware **十七个**字段里的三个露出来：Backend / Shell /
StreamingShell（`eino@v0.9.22/adk/prebuilt/deep/deep.go:219-223`）。
`LsToolConfig`、`GlobToolConfig`、`CustomSystemPrompt`、`UseMultiModalRead`
与卸载（offloading）那几个旋钮一个都够不着
（`eino@v0.9.22/adk/middlewares/filesystem/filesystem.go:221` 的 `MiddlewareConfig`）。
★ 而上游那份卸载逻辑只挂在 `NewMiddleware` —— 那个 struct 式的构造函数 ——
没挂在 `NewTyped` 上，而 `deep` 调的正是 `NewTyped`。
所以在我们这条路径上，任何一层都没有截断：后端返回 3 万条，工具把它们 join 起来，
模型全读进去。

★ **复制 middleware 是另一条路，但它更贵**：它引用 eino 的 **internal** 包（照抄编译不过），
是几千行生产代码与几千行上游测试，还握着七个工具的**提示词文本**（中英各一套）。
复制它唯一能多买到的是 `FileInfo` 的**渲染**（一个 size 列、一句更好看的截断标记）——
那只是外观层，代价却是分叉工具契约和提示词。

结论：要约束 `ls` 和 `glob`，就得在它们**读盘的那一层**约束。

★ **三层各管一件事**：

| 层 | 管 | 永远不碰 |
|---|---|---|
| `agentkit/fsx`（后端） | **行为**：写只能落在哪、一次遍历能走多远、什么时候停、一次返回多少 | 模型看到的文字 |
| 工具层（上游，不改） | **契约**：工具名、参数、输出形状 | 哪些路径可达 |
| 提示词（`agentkit/build/spec.go`、`agentkit/run/input.go`） | **措辞**：告诉模型代码在哪 | 任何后端能强制的事 |

在后端里落实的规则，模型无视提示词也绕不过去；只写在提示词里的规则，绕得过。

### 2.2 写边界

`resolve`（`agentkit/fsx/fsx.go:218`）被所有方法共用，只决定一个请求路径**指的是什么**：
空路径是 project root（**绝不是 `/`**），相对路径挂在它下面。

★ **读是故意不收敛的。** 工具集里本来就有 `execute`，它能 `cat` 任何进程读得到的文件 ——
所以限制 `read_file` 并没有换来安全，只换来了「项目就在隔壁目录却莫名 file not found」。
原始事故需要的从来不是读禁令，而是**遍历的上限**。

真正的边界是写，它走 `resolveWrite`（`agentkit/fsx/fsx.go:240`）：

```go
if !s.inside(abs) {
    return "", fmt.Errorf("path %q is outside the project root %q", abs, s.root)
}
for dir := filepath.Dir(abs); ; dir = filepath.Dir(dir) {
    real, err := filepath.EvalSymlinks(dir)
    if err == nil {
        if !s.inside(real) {
            return "", fmt.Errorf("path %q resolves outside the project root %q", abs, s.root)
        }
        return abs, nil
    }
    if !errors.Is(err, fs.ErrNotExist) {
        return "", fmt.Errorf("cannot verify that %q is inside %q: %w", abs, s.root, err)
    }
    if filepath.Dir(dir) == dir {
        return abs, nil
    }
}
```

两个点：

- **要向上找祖先，因为文件可能还不存在。** `EvalSymlinks` 解不了不存在的路径，
  所以循环走到最深的已存在目录再检查。没有这一步，当 `link` 指向外部时，
  `write_file` 写 `<root>/link/new.txt` 能通过纯字面的检查。
- ★ **它 fail-closed。** 只有「这个组件不存在」才继续向上走。其它任何失败 ——
  权限、I/O、符号链接成环 —— 都是我们看不进去的目录，于是直接拒绝写。
  把任何错误都当成「这里没有符号链接」而返回未经检查的字面路径，
  等于一个读不了的祖先就能写到任何地方。

`s.roots` 是 project root 加 `ExtraRoots`（会话工作区与 skills 目录 ——
它们可能活在项目之外）。每个 root 在 `New` 里一次性变成绝对、去符号链接的真实路径
（`agentkit/fsx/fsx.go:156`），所以比较的是真实路径。

★ 拒绝是**错误**，不是空结果：模型能拿到一句可以据此行动的话。

★ **落在配置根里的写走另一条路。** `ConfigGate`（`agentkit/fsx/fsx.go:121`）
把这类写交给配置服务，于是它们拿到编辑器那几道闸门
（`agentkit/build/snapshot.go:109` 的 `newConfigGate`）。

### 2.3 有界的 `ls` 与 `glob`

上限是**包内常量**，不是配置：没人拧的旋钮就是没人测的旋钮
（`agentkit/fsx/fsx.go:65-80`）。

| 常量 | 值 | 含义 |
|---|---|---|
| `maxLsEntries` | 200 | 一次 `ls` 的结果，外加一行封顶提示 |
| `maxGlobResults` | 200 | 一次 `glob` 的结果，外加一行封顶提示 |
| `maxGlobDepth` | 16 | 相对遍历根的目录深度 |
| `maxGrepMatches` | 200 | 一次 `grep` 的结果，外加一行封顶提示 |
| `walkBudget` | 10s | 一次遍历（glob）或一次搜索（grep）最多跑多久 |

★ **`walkBudget` 管的是开销，不是结果。** 深度与结果上限说「回来多少」，
时间预算说「这一轮能被堵多久」。在挂载盘上跑一个模式，会访问几百万个条目、
回来两个匹配 —— 而工具调用是同步的，没有预算这一轮就会一直挂在盘上。
预算耗尽时遍历停下，并在结果里说明：一个看起来完整、其实被截断的搜索，
正是模型得出「这个文件不存在」的路径。

`ls`（`agentkit/fsx/fsx.go:282`）：

- `os.ReadDir` 本来就按文件名排好序，**只排一次**。
- 目录以 `/` 结尾（`agentkit/fsx/fsx.go:317`）：渲染层丢掉 `IsDir`，
  那个斜杠是唯一能告诉模型「这是一个可以列、不能读的目录」的记号。
- 超过上限时列表以一行合成文本收尾（`agentkit/fsx/fsx.go:309`）——
  `ls` 工具只渲染 `FileInfo.Path`，所以上限只能表达成「又多一条条目」。
- 每条检查一次 `ctx.Err()`。

`glob`（`agentkit/fsx/fsx.go:577`）的四条性质：

| 性质 | 落点 |
|---|---|
| 深度上限、结果上限、忽略列表，从一个允许的 root 起步 | `agentkit/fsx/fsx.go:621-641` |
| 一个读不了的子树**跳过自己**而不是打断整次遍历 | `agentkit/fsx/fsx.go:606`（`walkErr != nil` 返回 nil） |
| 撞到上限就**停下遍历**（`filepath.SkipAll`），而不是继续 stat 一百万条再排序 200 条 | `agentkit/fsx/fsx.go:639` |
| **按模式剪枝**：目录名对不上模式的下一段，它就不可能装着匹配 | `agentkit/fsx/fsx.go:629` 的 `mayHold` |

★ 剪枝那一条的效果：`docs/**` 只走 `docs/`，`*.go` 一个目录都不走。

★ **绝对 pattern 会被归一化。** 运行提示词教模型写 `glob(pattern="<projectRoot>/**/*.go")`，
而匹配是相对遍历根的路径算的 —— 不归一化的话，提示词教的每一次 glob 调用
都返回「没找到文件」，而且是**安静地**返回。
`globPattern`（`agentkit/fsx/fsx.go:724`）把绝对 pattern 变成相对遍历根的形式；
指向遍历根之外的被拒绝，并同时报出两个路径（这是 pattern 与 path 自相矛盾，
不是收敛规则）。★ 于是提示词里现有的建议继续可用，这件事不需要改任何提示词文本。

### 2.4 垃圾目录按名字忽略；会话目录不忽略

`.git`、`node_modules`、`$RECYCLE.BIN`、`System Volume Information` 按 basename 跳过
（`agentkit/fsx/fsx.go:84`，判定在 `agentkit/fsx/fsx.go:278`）。

★ **`.openheron/state` 不跳过。** 上传的文件就在它下面，运行提示词把模型指向那里。
保护上下文的是结果上限，不是忽略。

★ 这个名单是**按 basename** 匹配的，所以它在整棵树里跳过每一个叫这个名字的目录 ——
它挡的是**遍历成本**，不是安全边界。

★ `glob` **不认 `.gitignore`**（`rg` 认）。这个不对称是上游的，保留，
并在相关处用 `shortcut:` 注释写明。读 `.gitignore` 本身还没做（§5）。

### 2.5 ripgrep 缺席时的降级

★ **引擎在构造期定一次。** `fsx.New` 里 `exec.LookPath("rg")` 一次
（`agentkit/fsx/fsx.go:187`），记在 `Local` 的 `rgPath` 上；空就是内置引擎。
不每次调用都试一次 —— 一台没有 ripgrep 的机器不该为每一次搜索付一次失败的进程，
而且第一次调用不该是发现这件事的那一次。
启动日志里会打印挑中的是哪个引擎（`agentkit/build/snapshot.go:120`）。

★ **为什么不用系统的 grep。** 这个工具的输出是**数据**（路径、行号、一行文本），
而 grep 的输出是**给人看的文本**。`path:line:content` 是没法可靠地解析回去的：

| 现象 | 实测 |
|---|---|
| 单个文件不给文件名 | `grep -n beta a.txt` → `2:beta` |
| 上下文行换分隔符 | `path-3-gamma` |
| 路径里有冒号 | `C:\...\a.txt:2:beta` —— 盘符自带冒号 |
| 内容里带 `12:` | 内容自己带冒号时，贪婪切与非贪婪切都会切错一边 |
| 方言不同 | GNU（Linux）、BSD（macOS）、busybox（容器）、Git 自带的 GNU 3.0 |

★ 而 ripgrep 的 `--json` 给的是**结构化事件** —— 上游当初读 `--json` 而不是文本输出，
正是为这个。**所以降级不是「换个 grep」，是「自己算」**：
内置引擎在 `agentkit/fsx/grep_fallback.go`（入口 `grepBuiltin`，`:235`）。

★ **换不掉框架的 grep 工具。** 文件工具那六个全部由中间件从 `Backend` 挂上，
而 `deep.Config` 只暴露 Backend / Shell / StreamingShell 三个字段 ——
想不要框架的 `grep`，就得连整套文件工具一起不要。
所以缝只有一条：`GrepRaw` 返回的那份 `[]GrepMatch`。
层与层之间只差一件事：**谁去找匹配** —— 工具本体、渲染、输出模式、分页都在 eino 那边。

★ **pattern 编不过就按字面量搜**：模型要的是一个字符串，给它一个字符串比拒绝它好 ——
但结果里会写明「这个 pattern 不是合法正则，是按字面搜的」。
而按字面搜**什么都没搜到**时，调用直接失败，而不是回答「没有匹配」：
一个从来没被照写的意思解释过的 pattern，「没有匹配」是个没人问过的问题的答案。

★ **内置引擎的行为边界**（`rg` 与内置引擎的逐项对照）：

| 维度 | `rg` | 内置引擎 | 差异怎么处置 |
|---|---|---|---|
| 正则方言 | Rust regex（RE2 家族） | Go `regexp`（RE2） | 同族；lookaround 与反向引用两边都不支持 |
| `\d` `\w` | Unicode 感知 | ASCII | 已知差异 |
| 大小写 | `-i` | `(?i)` | 一致 |
| 多行 | `-U --multiline-dotall` | `(?s)` 整文件匹配，行号取起始行 | 一致 |
| 文件过滤 | `--glob` | doublestar | `--glob` 不含 `/` 时按 basename 匹配，同样处理 |
| 类型过滤 | `--type` 内置类型表 | 一张小的扩展名表 | 表外的类型**报错**，不静默搜错文件 |
| 忽略规则 | 尊重 `.gitignore` / `.ignore` | 不认 | 只多搜不少搜 |
| 隐藏文件 | 默认跳过 | 同样跳过 | 一致 |
| 二进制 | 自动跳过 | 前 8 KiB 见 NUL 即跳过 | 一致 |
| 符号链接 | 不跟随 | 不跟随 | 一致 |
| 上限 | 200 条 | 同样 200 条 | 提示语一字不变 |
| 深度 | 没有 | 16 层（和 `glob` 的遍历一样） | **会少搜** |

★ **方向大体是安全的**：内置引擎不读 `.gitignore`，所以它**多**搜而不是少搜 ——
于是「这个文件不存在」的结论在两个引擎下都成立。会少搜的只有上面那一条深度上限，
而它跟 `glob` 的遍历是同一个数。

★ **封顶提示挂在最后一条匹配的内容上，而不是做成一条匹配**
（`agentkit/fsx/fsx.go:532`）。渲染层数的是它拿到的路径与条目：
一条自己占位的提示会把「两个文件」变成「三个文件」，
而在 `files_with_matches` 模式下（那是默认模式）它只会渲染路径，
于是那个被搜索的目录会混进「命中的文件」里。

★ **跑命令的程序归调用方管。** 后端不再写死 `/bin/sh -c`：
`Config.Shell` 是程序加它自己的开关，`Config.ShellPathDir` 会被放到子进程 PATH 的最前面 ——
一个在 PATH 之外找到的 shell（Git for Windows、MSYS2）否则找不到自己那套 `ls`。
命令接在开关后面（`agentkit/fsx/fsx.go:871`），`cmd.Dir = s.root`（`:880`），
所以 shell 命令里的相对路径现在意味着 project root。

★ **探测在 `agentkit/shell`**（`agentkit/shell/shell.go:90` 的 `Detect`），一份快照一次：

1. PATH 上的 `sh` 或 `bash`；
2. Git for Windows 与 MSYS2 的常见安装位；
3. WSL 启动器 —— 排在最后：它把命令跑在 Linux 文件系统里，那边的 `C:\Users` 是 `/mnt/c/Users`；
4. 一个都没有就 `cmd.exe /c`。

★ **每个候选都要验一下活**（`agentkit/shell/shell.go:152`）：Windows 自带的那个
`bash.exe` 每台机器都有，没有 WSL 发行版时它启动、打一行错、退出码非零 ——
把它当成 shell 比没有 shell 更糟。

★ **`execute` 工具是 openheron 自己的**（`agentkit/build/execute.go:40`），
框架那个不挂：框架的描述只字不提这台机器上跑命令的是哪个 shell，
而写命令的是模型 —— 描述是唯一能让模型知道方言的地方，
于是方言写死在描述里（`agentkit/build/execute.go:109`）。
名字仍然保留，provider 不得冒用（`agentkit/build/tools.go:117-150`）。

## 3. MCP 集成

MCP 服务器与 openheron 今天的每一个工具，差别恰好在两处。

### 3.1 客户端占有的两样东西

| # | 是什么 | 现有代码为什么假定不是这样 |
|---|---|---|
| 1 | **一条长连接** —— 一个子进程（stdio）或一个 HTTP 会话 | `Provide` 返回一批就返回；`toolProvider` 里没有任何东西有生命周期 |
| 2 | **运行期才定下来的工具集** —— 服务器决定它提供哪些工具 | 策略表以工具名为键，而表里的每个名字在写配置时就已经知道了 |

★ **第二条才是承重的那条。** 策略层是一张**以名字为键的静态表**，
而 MCP 服务器的名字要等连接建立之后才知道。

适配器是 `github.com/cloudwego/eino-ext/components/tool/mcp`。
★ **它的 README 是过时的** —— 它的 `Config` 在 README 里只列了 `Cli` 与 `ToolNameList`，
源码里有五个字段。少掉的那三个恰好是有用的：`ToolCallResultHandler` 是结果进入模型
之前唯一能被检查或改写的地方，而 `CustomHeaders` / `Meta` 决定怎么寻址一个服务器。
**读源码，别读 README。**

这个组件**不**做的事：

| 没做 | 对 openheron 的后果 |
|---|---|
| 不管客户端生命周期 —— 不拉起、不 `Start`、不 `Initialize`、不 `Close` | 连接归 openheron 管：manager 建它、初始化它、养着它，进程退出时拆掉它 |
| 不选传输方式 | stdio / streamable HTTP 由 openheron 挑（`agentkit/build/mcpmanager.go:786`、`:897`） |
| 没有配置格式 | schema 是 §3.2 说的 `mcps/<server>.yaml`，一个 server 一个文件 |
| 不支持流式 | 那层包装只实现了 `Info` 与 `InvokableRun`，所以它是 `tool.InvokableTool`，永远不是 `tool.StreamableTool`。整个结果作为一块 JSON 回来 |

★ **这个组件是适配器，不是功能本身。** 生命周期、命名、档位与策略都是 openheron 自己的，
协议才是组件带来的。

### 3.2 `mcps/<server>.yaml` 分区

★ **它不是 `tools/` 里的一个文件。** `tools/` 的规则是「一个文件 = 一个工具」，
而 MCP 里的实体是**服务器**：一个服务器有一个命令、一份参数、一个环境、一种传输
和一条生命周期。把 N 个服务器塞进一个文件，就要在「文件内容就是配置、
不带外层包装键」这个分区规则之下再套一层列表；而整份文件替换时，
也说不清是哪个服务器变了。
⇒ 配置设计 §2 的那条规则 —— *一实体一文件* —— 落在服务器上，不是落在工具上。

| 字段 | 类型 | 缺省 | 含义 |
|---|---|---|---|
| `transport` | `string` | `stdio` | `stdio` 或 `http`；**未知值是硬错误**，不静默退回 stdio（`agentkit/spec/load.go:486`） |
| `command` | `string` | — | 仅 stdio，**必填**（`agentkit/spec/load.go:508`） |
| `args` | `[]string` | `[]` | 仅 stdio：它的参数 |
| `env` | `map[string]string` | `{}` | 仅 stdio：**叠加**在继承来的环境之上的变量；http 服务器写 `env:` 是**加载期硬错误**（`agentkit/spec/load.go:504`） |
| `url` | `string` | — | 仅 http，**必填**（`agentkit/spec/load.go:497`） |
| `headers` | `map[string]string` | `{}` | 仅 http：每次请求都带的头 |
| `timeout` | `MCPDuration` | `30s` | 连接 + 初始化 + 取工具清单的上限 |
| `call_timeout` | `MCPDuration` | `60s` | **一次工具调用**的上限 |
| `tools` | `[]string` | `[]`→全部 | ★ **过滤器，不是开关** —— 从这个 server **自己有的**工具里挑出来要提供的那些**原始**工具名；不写 = 全部（`agentkit/build/mcptools.go:123`） |
| `allows` | `[]string` | `[]` | 这个 server **自己的**工具名里，哪些**不用点头**就跑 |

★ **`http` 就是 streamable HTTP，而且只是它。** `mcp-go` 还带着一个 SSE 客户端，
而一个 `transport` 的取值没法在它们之间做选择 —— 一个配置问不出来的传输方式，
是一个永远用不上却还得一直维护的传输方式。

★ **`env:` 在 http 服务器上没有意义，所以那儿它是错误。** http 服务器是一个远端进程，
而本进程的环境不是它的环境。★ 一个什么也不做的键比一次拒绝更糟：
用户会以为那个服务器是带着这个变量跑的。

★ **类型是 `decl.MCPDuration`**（`agentkit/spec/decl/decl.go:717`），
而不是别的宽松时长用的那份包级清单。差别在**来源**：别的宽松时长用的那份清单
不带文件也不带键，所以它说不出**是哪一个**值写错了。
`MCPDuration` 留着原文，也留着「这个键到底写没写」，
所以 `deferred` 那一条能点出文件与键。

★ **`timeout` 是宽松时长**：写错就退回默认并记进 `deferred`。
写错 timeout 改变的是「等多久」，不是「会发生什么」—— 它不该让进程起不来。

★ **没有 `enabled` 字段。** 服务器是被**定义**的，因为它的文件存在；
删掉文件就是删掉这个定义。★ 这让**server 存不存在**只待在一个地方，
而不是拆成「文件在不在」和「文件里那个标志说开不开」两处。

★ **文件说服务器是什么；agent 说谁拿它。** 哪些 agent 加载一个服务器，
取决于它 frontmatter 里自己的 `mcps:` 清单 —— 缺失与 `[]` **都表示一个都不挂载**
（`decl.Agent.MCPServers` 是普通切片，缺省即空）。
挂载就是授予：挂一个 server，这个 agent 就拿到它的**全部**工具。
★ 子 agent 有自己的一份清单（`decl.SubAgentSpec.MCPServers`），
所以清单是从**正在跑的那个 agent** 读的 —— 根 agent，或者这一轮所在的那个子 agent。

★ **一个不存在的名字是加载期硬错误**（`agentkit/spec/load.go:912`）。
一个写着 `githbu` 的 agent 会安安静静地一个 MCP 服务器都拿不到，
而「因为笔误而少掉的工具」与「因为服务器倒了而少掉的工具」长得一模一样。

★ **`command` 在沙箱的 PATH 里解析，绝不相对项目根**（`agentkit/build/mcpmanager.go:883`）。
工作区不许决定跑什么：一个相对项目根解析的命令，
等于让一次 checkout 决定 agent 跑哪个可执行文件，还带着 agent 的权限。

★ **子进程的环境 = openheron 自己的环境，被配置里的 `env:` 覆盖同名项。**
`env:` 为空的配置，产出的子进程仍然有一个完整的环境。
★ 正是这份继承让**凭据路径不需要任何配置**（§4.2）：运行时把**哨兵**放进容器里的那个变量，
MCP server 从那儿读到它 —— 而那是 **MCP server 的事**，不是 openheron 的。

★ **什么会大声失败**：

| 情况 | 什么时候 | 为什么 |
|---|---|---|
| 不认识的 `transport` | 加载期 | 不静默退回 stdio —— 一个笔误不该跑起另一个 server |
| stdio 服务器缺 `command` | 加载期 | 没有东西可跑 |
| http 服务器缺 `url` | 加载期 | 没有东西可谈 |
| http 服务器上的 `env:` | 加载期 | http 服务器没有子进程可以交给一份环境 |
| agent 的 `mcps:` 点名一个不存在的服务器 | 加载期 | 一个笔误会悄悄把这个 agent 收窄成什么都没有 |
| MCP server 起不来（命令不在 PATH 上、握手超时） | 运行期 | provider 警告，它不贡献工具，这一轮照常跑 |
| `tools:` 点名一个 server 没有的工具 | 运行期 | server 的清单只有它起来之后才知道 —— provider 警告，其余的工具照给 |

★ **我们自己的配置在加载期就失败；别人家的内容被丢掉并留个话儿。**
加载期硬错误会让进程停下来，而这正是要点：这些是有人手写的文件里的错误，
下一次启动必须说出来。

★ **这个分区是热的**（`agentkit/spec/config.go:85`，`Editable: true, hot: true`）：
改一个服务器的命令或超时，下一轮就该生效，不必重启。
★ 一个资源无法重建的热分区，就是一个谎称自己是热的分区 ——
所以换掉客户端的是**声明**变了（指纹就是声明自己的那份），
而不是快照换代时。

★ **谁写这些文件**：

| 谁来写 | 写什么 |
|---|---|
| 用户 | 手写 `mcps/<server>.yaml` |
| `openheron init` | ★ **三台服务器** —— `mcps/fetch.yaml`、`mcps/time.yaml`、`mcps/deepwiki.yaml`（`initcfg/initcfg.go:164`），而且每一份都是**能加载**的：文件**定义**了 server，所以一份解析不了的文件不是「server 关着」，而是「配置加载不了」 |
| 配置编辑器 | ★ 脚手架走它的「新建文件」对话框 —— 编辑器**能写** `mcps/`（`agentkit/spec/write.go:309`），而且它拒绝不是一个可读回服务器名的文件（`agentkit/spec/write.go:185`） |

★ 脚手架本身在 `agentkit/templates/files/mcps/_example.yaml`：
★ **开头那个下划线就是「不写下去」的标记** —— 它是新服务器的起点，
不是一台被装上的 server。

### 3.3 装配路径与客户端生命周期

从磁盘上那个文件一路走到闸门的那一个决定：

```
.openheron/mcps/github.yaml
  │  spec.loadMCP (agentkit/spec/load.go:368) — 文件名与字段在这里校验，
  │  写坏了一个是启动期错误
  ▼
decl.MCPServerCfg — transport / command / args / env / allows / tools / 两个超时
  │
  │  snapshot.buildHeavy (agentkit/build/snapshot.go:84)
  │    → mcpToolProvider.Provide (agentkit/build/mcptools.go:201)
  ▼
the mcps manager (agentkit/build/mcpmanager.go:44):
  per server → transport.NewStdio / transport.NewStreamableHTTP
            → tr.Start(ctx) → cli.Initialize(ctx) → mcpp.GetTools(ctx, &mcpp.Config{Cli: cli})
  │
  │  each tool:  name = mcp__<server>__<tool>     (sanitized; a bad name is skipped
  │                                                with a warning)
  │              tier = the file's allows list, read in the SERVER's own names
  │                    and mapped to the exposed ones
  ▼
[]tool.BaseTool — the stdio ones wrapped in the per-server mutex
  │
  ▼
toolRegistry.Build → heavyDeps.tools → applyToolConfig → deep.Config.ToolsConfig.Tools
  │
  ▼
the model calls mcp__github__create_issue
  │
  ▼
the gate: Policy.TierOf("mcp__github__create_issue")
  ├─ allow → the tool runs → the cap truncates the output → the result reaches the model
  └─ ask   → the turn is interrupted for a human → resumed
```

★ **档位的解析**是一次查找，而闸门从不去问一个工具来自哪个文件 ——
装配时填进表里的就是**对外的**名字。那份 allow 清单是从配置装配出来的，
所以 `mcp__github__read_issue → allow` 这一条在 server 还没起来之前就已经在表里了。

★ **装配发生在 `agentkit/build`，不在 `agentkit/spec`**：只有 provider 知道
自己文件的词儿，以及它背后的对外名字；而 `agentkit/spec` 不得 import `kb`
（那条铁律），也不得 import eino（文件系统工具的名字住在那里）。

**客户端归一个管理者拥有**，不归 provider。provider 回答的是「这个快照有哪些工具」；
而客户端活得过 provider，靠的就是管理者（`mcpManager`，`agentkit/build/mcpmanager.go:44`）。

| 职责 | 落点 |
|---|---|
| **一个 server 一个客户端** | 一个 server 就是一条连接，对 stdio 来说还是一个子进程 |
| **只起某个 agent 要的 server** | 管理者只启动那些 agent 的 `mcps:` 清单里的服务器 —— 这份清单是**并集**（`agentkit/spec/load.go:982` 的 `MCPServersInUse`），因为快照的工具集是所有 agent 共享的 |
| **子进程的生命归管理者** | 拉起子进程的那个上下文由**管理者的生命周期**派生，不是调用方的（`agentkit/build/mcpmanager.go:447`） |
| **崩了的子进程自动重启** | 一个死掉的 stdio server 在下一次调用时被重新拉起 |
| **发现工具清单变了** | 管理者重新列举；清单变了就**换掉工具面**、工具重建 |
| **服务器变了就换掉它** | 一份不再匹配运行中客户端指纹的声明会关掉旧客户端并拉起新的 |
| **进程退出时拆掉** | 管理者在关闭时关掉每一个客户端（`agentkit/build/mcpmanager.go:267`） |

★ **子进程的生命归管理者，不归调用方。** 调用方是**一轮**，而一轮会结束：
轮次结束时运行期取消它交给 builder 的那个上下文，而 `exec.CommandContext`
在上下文取消时会杀掉子进程 —— 那个工具于是**还在表里**，却再也答不上话。
⇒ 管理者的 API 因此**不收上下文**：`Tools` 与 `dispatch` 都只收配置 ——
这样「谁拥有这个子进程」不会被交给下一个调用方去决定。

★ **客户端活得过快照 —— 实现就是这样。** 管理者只注册一次，跨快照拥有那些客户端，
所以一次没碰某个服务器的配置变化，会让那个服务器的子进程继续跑着。
换掉客户端的是**声明**，而另一个拆解时机就是进程退出。

★ **一个 server 一个 client，且调用串行化。** eino-ext 那个组件包的是单个
`client.MCPClient`，该 server 的所有工具共用它。
★ **同一个 stdio server 的调用被串行化，这是正确性决定，不是性能决定**：
`mcp-go` 的 stdio 传输把请求直接写进子进程的 stdin，
而 POSIX 只保证不超过 `PIPE_BUF`（Linux 上 4096 字节）的管道写是原子的 ——
更大的 JSON-RPC 行会**与另一次调用的行交错**，服务器读到的是一个坏掉的帧。
⇒ provider 给同一 server 的 stdio 工具包一把 per-server 的互斥锁
（`serialTool`，`agentkit/build/mcptools.go:343`）。
★ 这把锁是**等**，不是让第二次调用失败。
★ **http server 不串行化** —— 串行化是**传输方式**的属性，不是 MCP 的属性。

★ **调用有截止时间。** 没有截止时间的互斥锁就是一条把轮次挂死的路：
一个永不回答的 MCP server 会永远占着锁。⇒ `call_timeout` 给一次工具调用封顶。

★ **一次启动，一个客户端。** provider 在构建批次时要，面板在用户打开它时也要，
两个都是普通请求，可能在同一刻到达。⇒ **启动是按 server 单飞的**：
第一个调用方启动客户端，其余调用方等着那一次启动，拿到同一个客户端。
★ 一个 server 两个子进程，比一次慢的启动更糟 —— 两者报出的工具清单可能不同，
于是模型看到的工具就取决于哪个进程答的。

★ **失败的启动不能拖住装配。** 初始化失败之后的拆解要**等子进程退出**，
而一个还在装自己依赖的子进程不会因为 stdin 关了就退出。
⇒ 失败路径**先杀掉子进程**（`killMCPChild`，`agentkit/build/mcpmanager.go:840`）
再拆解，而拆解本身也有一个上限（`closeMCPClient`，`:856`，宽限 5s）。
★ **装配等的是一个 server，不是一个 server 的尸体。**

★ **输出封顶。** `tools/` 把每个工具的输出封在 20000 字节
（`agentkit/build/nettools.go:25`）。这个封顶存在的原因是工具的输出**原样进模型上下文**，
而一个 10MB 的页面会把这一轮撑爆 —— 表现是「模型突然开始胡说」，不是报错。
★ MCP 工具的输出没有不同：它也是别人的文本，未经滤就进了上下文。
封顶对 MCP 工具同样适用，而实现它的位置就是组件的 `ToolCallResultHandler`
（`agentkit/build/mcptools.go:384` 的 `capMCPToolResult`）。

★ **拆 stdio 客户端时那两行 `ERROR: Error reading from stdout` 不是故障。**
`mcp-go` 的 reader 醒来拿到的是 `file already closed`，被当成传输错误打到 stderr，
**每个 stdio 子进程一行**，而且绕过了 `log_level` 与 `log.file`。
⇒ `mcpCommandLogger`（`agentkit/build/mcpmanager.go:756`）把 `WithCommandLogger`
接回进程 logger：只有「`Error reading from stdout` + 关闭管道措辞」降级为 `Debug`，
其它错误仍是 `Error`。

★ **间隔是 30 秒，而且它不是一个配置键。** 某一轮来要一个 server、
而上一份清单已经比 **30 秒**更老时，管理者才重新列举它
（`mcpRelistInterval`，`agentkit/build/mcpmanager.go:36`）。
★ 没人拧的旋钮就是没人测的旋钮，而一个 server 的工具清单是以天为尺度变化的。

★ **没有人通知「树变了」，只有比较。** 快照的身份是**（配置对象，MCP 工具面）**
（`agentkit/build/snapshot.go:207` 的 `snapshotFor`）；
builder 每次建树前把当前的身份和手上那份比一比。
★ 通知的路会漏（有人忘了通知），比较的路不会漏 —— 而且它只有一条。

### 3.4 非阻塞启动：每台 server 一个状态，起来了就进下一轮

★ **MCP 的启动不站在那一轮的关键路径上。** 一台慢的 server 不该决定一轮能不能开始：
文件里的 `timeout:`（缺省 30s）是**接得上**的上限，
而那一轮自己的启动上限是 `start_timeout` —— 一台冷启动要装依赖的 server
就能把那一轮的预算吃光，客户端最后拿到的是「turn start timeout」，它既不点名是哪个 server，
也不说为什么。

八条规则：

| | 规则 | 为什么 |
|---|---|---|
| R1 | **启动是一次后台作业**，装配不等待它 | 一台 server 不该决定一轮能不能开始 |
| R2 | **每个 server 有一个状态** | 「还没起来」与「起不来」是两件事；混起来就只剩「不可用」 |
| R3 | **没起来的 server 不贡献工具**，而那一轮照跑 | 挂一个调不通的工具比没有这个工具更糟 |
| R4 | **起来了就换掉工具面**，下一轮的工具清单里就有它 | 工具集是每一棵树构建一次的；工具面不动，它就不动 |
| R5 | **一次启动，一个客户端** | 单飞不因为异步而变 |
| R6 | **失败的退避重试** | 一次失败不该是永远的失败，也不该是热循环 |
| R7 | **期限属于作业，不属于那一轮** | 那一轮的预算是给模型的 |
| R8 | **子进程活得比那一轮久** | 树要这台 server，不只是这一轮要 |

状态机（`mcpState`，`agentkit/build/mcpmanager.go:340-351`）：

```
                   声明变了（指纹不同）
         ┌────────────────────────────────────────┐
         │                                        │
   not_started ──(被要求)──► starting ──(握手成功)──► ready
         ▲                      │                        │
         │                      │(到期限 / 命令起不来)      │(子进程死了)
         │                      ▼                        ▼
         └──(退避到期，再被要求)── failed ◄───────────────┘
```

| 状态 | 意思 | 面板上的样子 |
|---|---|---|
| `not_started` | 声明在树里，还没有谁起过它 | 这个 agent 不挂它时是 `unused` |
| `starting` | 一次启动在飞（单飞） | `starting`（**新状态**） |
| `ready` | 客户端在，清单在手 | `ready` |
| `failed` | 上一次启动失败，带着原因 | `unreachable` |

★ **状态是 server 的，投影是 agent 的。** 「这个 agent 不用它」（`unused`）与
「这台 server 起不来」（`unreachable`）是两句不同的话；新增的 `starting` 站在它们的对面：
**它不是失败，它是在路上。**

★ **预热是一个作业，它遍历 agent 配置、取挂载的并集、逐台在后台起起来**
（`mcpManager.Warm`，`agentkit/build/mcpmanager.go:98`）。
★ 没有任何 agent 点名的 server 永远不会被启动，连后台都不启动。
★ **每台 server 的启动是它自己的 goroutine** —— 三台各要一分钟的 server 不该花三分钟。
预热挂在配置的加载与重载上（`main.go:490`），而它在锁**外**调它
（`main.go:781-792` 的 `agentConfig.set`）——
拿着锁起进程会把每一个读配置的调用都堵住。

★ **预热不是唯一的起法**：一次要求（provider 或面板）也会起，走的是同一个单飞。
那正是「失败的退避重试」能成立的原因 —— 而**退避就是文件自己的 `timeout:`**
（`mcpStartTimeout`，`agentkit/build/mcpmanager.go:371`）：
★ 刚失败过的 server 在那个期限过去之前不会被再问一次，
所以起不来的 server 不会被拉成热循环，而起得来的 server 也不会被放弃。

★ **「还在起」不是失败。** provider 对 `errMCPStarting` 与 `errMCPNotStarted`
**不告警**（`agentkit/build/mcptools.go:222`）：那会给每一轮都打一条
「server 正在起」的警告。

★ **面板会先报「在路上」，再报「好了」。** 用户打开 `/mcps` 可能看到一台 server
正在起 —— 而它起来之后，面板要**再读一次**才看得见那批工具（面板不订阅）。

★ **冷启动之后的第一轮，模型看不见那台 server 的工具。** 一个「查外网」的 agent
在第一轮可能凭记忆回答，而不是去查。⇒ 这是异步的价钱，不是 bug；
而「自然加入下一轮」就是这个价钱的另一面。

### 3.5 闸门与档位

一个叫 `mcp__github__create_issue` 的工具被查起来与其它工具一模一样：

```
Policy.TierOf("mcp__github__create_issue")
```

除非 server 的文件在 `allows:` 里点了它的名，否则它落进 `ask` 档 ——
而这正是每一个工具都遵守的同一条规则。闸门代码一个字都不用改。
★ 命名空间的缺省是 `ask`，而它是档位查找里的一个**兜底分支**，不是一个特例。

★ **这正是命名空间的意义。** 闸门、各张表、API 的 `EffectiveTools` 视图
与每个 agent 的收窄，全都在名字上工作。
一个带命名空间的名字，是它们本来就懂的名字 —— 策略层、API 与前端都不需要接一个新概念。

★ **stdio 之下，MCP 服务器是 openheron 的子进程**：它能写文件、能联网，
能读到 openheron 读得到的一切。给 HTTP 工具定的「只连公网」在这里**不适用** ——
那条限制的是*工具*能碰到什么，不是*服务器*能做什么。
这是命名空间与「给每个 server 自己的档位」合起来的最强理由：
`mcps/<server>.yaml` 里一个**空的** `allows:` 是一行字，
意思是「这个 server 的每个工具都要先点头」。

★ **一个工具的档位写在它自己的文件里，用那个文件自己的词儿。**

```yaml
# .openheron/mcps/github.yaml
#
# ★ The default is ask, so an empty list means "every tool of this server asks".
#   A trusted server names its exceptions:
allows: [read_issue, list_issues]
```

★ **为什么要用文件自己的词儿。** 档位是关于**一个工具**的陈述，
所以它属于那个工具的文件 —— 而在那里它可以用那个文件已经在用的名字。
原来那个中心文件必须用**对外的**词儿点名那个工具，而工具自己的文件说的是 `create_issue`；
★ 一个工具两套词儿，就是一处等着发生的错配，
而一个拼错的名字在工具的行为与文件所说的不一致之前，一直是看不见的。

★ **缺省盖住文件没有点名的那些名字。** 既不在文件的 `allows:` 里、也不在任何别处的
工具，是 `ask` —— 所以 server 明天加的工具，在第一天就要点头，配置一个字都不用动。
★ 这与一个空的 `allows:` 用一行说出的是同一条规则；非空的那份就是同一条规则挖掉了几个例外。

★ **档位表是从配置装配出来的，不是从 server 装配出来的。**
⇒ 一个工具的档位任何时候都答得出来，包括 server 够不着的时候，
而闸门那次查找从不为等一条连接而停下。

★ **run ledger 说什么。** 一次 MCP 工具调用就是一次普通工具调用，所以它本来就跟别的调用一样
进 transcript、进事件流。**ledger** 多出来的是 server：一个 run 的行记下这次 run
碰过哪些 MCP server（`storage/runledger.go:168`），
名字是从这次 run 用过的带命名空间的工具名里读出来的（`agentkit/run/runrow.go:141`），
于是事后复盘能回答「这是哪个 server、是不是它把这次 run 弄失败的」，
而不必读完整个 transcript。

### 3.6 `/mcps` 命令与面板

★ 界面必须回答一个日志回答不了的问题：**这个 agent 有哪些 MCP 服务器，
它们起来了吗，跑起来的时候会让我付出什么？** 这三样管理者都知道。**面板自己不许下一个判断。**

★ **`/mcps` 是一个 action，不是一个 prompt**（`api/commands.go:61`）。
prompt 会去问**模型**那些服务器的事，而模型的回答会是关于一个**运行时**掌握的事实的
第二份、且有损的真相。状态是被读出来的，不是被生成出来的。
★ 这个 action 打开一个面板；它不往对话记录里写一条消息。

面板的每一行都由一个函数推出（`Builder.MCPServers`，`agentkit/build/mcpstatus.go:77`），
API 层只转发它返回的东西（`api/v2_mcps.go:74`）：

| 问题 | 答案来自哪儿 |
|---|---|
| 服务器起来了吗？ | 管理者的客户端 |
| 它提供了什么？ | 管理者的清单 |
| 这个工具叫什么？ | 那份计划 —— `mcpPlan`（`agentkit/build/mcptools.go:68`） |
| 它会问我吗？ | `Policy.TierOf` —— 闸门自己那次查找 |
| 这个 agent 能拿到它吗？ | agent 的表 |

★ **面板什么都不推导。** 它显示的每一个值，运行时早就有了。
一个重新判断任何东西的面板就成了一份第二真相，而最先坏掉的正是档位：
面板会说 `ask`，而闸门说 `allow`。

★ **工具那几行就是 agents 视图已经报出来的判定**（`agentkit/build/introspect.go:116`），
用的是同一条边界，所以 `/mcps` 与 `/v2/agents/<id>` 不可能对同一个工具报出两个不同的档位。
★ 唯一的差别是**名字的全集**：agents 视图走的是观察到的工具，
面板走的是管理者的清单 —— 所以一个倒下的服务器仍然能显示它提供过什么。

★ **一问就会启动服务器。** 管理者在被问到时才启动一个服务器，而面板就是那个问的人。
★ 另一条路（一个严格只读、只报「还没启动」的面板）回答不了用户敲这条命令时真正想问的问题：
「我的服务器坏了吗？」—— 而答案会变成「跑一轮就知道了」。

端点是 `GET /v2/mcps?agent=<agentId>`（`api/v2_files.go:76`）——
`agent` 可省略，省略就是配置里的缺省 agent；一个不存在的 agent 是 404。

★ 这个端点是**新的**，不是 `/v2/agents` 的延伸：agents 视图是**配置面**的视图，
而 MCP 状态是**运行时**状态 —— 子进程一死它就变，
而且它不能像 agents 列表那样在标签页的生命周期里被缓存。

| 字段 | 意思 |
|---|---|
| `servers[].status` | `ready` —— 客户端起来了；`unreachable` —— 上一次启动失败，`error` 说明为什么；`unused` —— 这个 agent 没有点名这个服务器；`starting` —— 正在起 |
| `servers[].error` | 上一次启动的失败文本 |
| `servers[].tools[].name` | server **自己**的名字 —— 文件的词儿 |
| `servers[].tools[].exposedName` | 模型看到的那一个名字 |
| `servers[].tools[].tier` | 那个名字在闸门那儿的判定 |
| `servers[].tools[].status` | `available` —— 这个 agent 拿得到；`skipped` —— server 提供了它而它没有被暴露；`blocked` —— 暴露了，但这个 agent 拿不到 |
| `servers[].tools[].reason` | 它为什么拿不到：`tools`、`mcps`、`name_too_long` 或 `name_collision` |

★ **拿不到的理由只剩一个，就是 agent 自己的清单。**
★ `name_too_long` 与 `name_collision` 是另一类 —— 它们说的是 server 选的**名字**。

★ **配置面视图也列出那些服务器**（`api/v2_mcps.go:121`）：配置说**什么存在**，
面板说**什么起来了**。★ 一个把 `mcps/` 分区藏起来的配置面视图，
会让设置界面没东西可显示。

★ **每一个已配置的服务器都出现，且只出现一次。** 这个 agent 不用的服务器是 `unused` ——
不是不见了。★ 在列表里缺席的服务器，与这个 agent 不用的服务器，在读者眼里长得一样，
而它们里头只有一个是问题。
★ **`unused` 的服务器没有工具行** —— 面板讲的是**这个 agent** 的服务器。

★ **够不着的服务器，它的工具是上一次成功的那份清单。** 管理者留着上一次成功的清单，
所以面板能说「github —— 够不着 —— 这些是它上次还在时有的工具」。
状态说明这份清单是不是当前的；清单说明这个 server 提供什么。

★ **面板是一个对话框**（`web/src/components/mcp-panel.tsx`），与 agent 选择器一样：
内容是一张服务器表，答案是短暂的，而对话记录不是它该待的地方。
★ 它每次打开都去取一次 —— 状态是实时状态，而这个标签页的缓存不是它该待的地方。
一个 **Refresh** 按钮重新读一次，因为用户修好一个服务器之后，
想不关掉对话框就看到结果。

## 4. 沙箱 Kit

### 4.1 形态：一个 workload Kit

★ 这份文档记录的决定是：**openheron 的整个进程**跑在 microVM 里，
宿主只留一条转发出来的 HTTP/SSE 端口。
理由是：这是唯一一层**连读路径一起罩住**的隔离，而且对 openheron **一行代码都不用改** ——
边界是往外挪的，不是往里挪的。

★ **往里那条接缝已经放弃**：把服务留在宿主、只把 `execute` 搬进 VM，
加固的是命令路径，读路径照旧敞着。★ 而且这两条不值得叠起来：
**Kit 是外层边界，里面就不需要 microVM 了** —— 沙箱已经提供了 `execute` 工具
本来打算去要的那层隔离。一条命令路径上叠两层隔离，多了一层。

Docker 的 Kit 规范（v3）是打包格式。要紧的事实都取自规范本身：

- Kit 就是**一个 OCI 镜像**，manifest annotation
  `vnd.docker.sandbox.kit.descriptor` 带着描述文件；镜像层带着内容。
  没有 Kit 专属的 media type：不读这个 annotation 的引擎就当普通镜像跑。
- 已发布的 Kit 只有两种：**`workload`** 提供根文件系统与启动契约；
  **`mixin`** 提供一层叠加。一次组合里有且仅有一个 workload。
- 描述文件用 YAML 写；内容配方是普通 Dockerfile 文本。默认的写法是**配对文件**：
  `<stem>.yaml` 与 `<stem>.dockerfile` 并排放，靠文件名主干约定互相找到。
- 解码是**严格**的：任何位置的任何不认识的字段都是错误 ——
  一个拼错的键被静静忽略，就等于一份策略被静静漏掉。
- 运行时保证一条平台底线：`bash`、`sh`、`curl`、`git`、一份装好的 CA 证书库，
  以及一个非 root 的默认用户 `agent`，uid `1000`，家目录 `/home/agent`。

openheron 是 **workload**，不是 mixin：它的层就是 agent 干活那台机器的根文件系统。
★ workload 的镜像配置承载启动契约，而规范明确说描述文件不重复它。

三个描述文件：

| 文件 | 角色 |
|---|---|
| `sandbox-kit/openheron/openheron.yaml` | workload |
| `sandbox-kit/openheron-deepseek/openheron-deepseek.yaml` | 每厂商一份 mixin（DeepSeek） |
| `sandbox-kit/openheron-openai/openheron-openai.yaml` | 每厂商一份 mixin（OpenAI） |

workload 声明了五个 capability：

| capability | 说什么 | 为什么 |
|---|---|---|
| `com.docker.sandbox/sbx@1` | 这是准备好让宿主按 agent 的方式启动的 | 承载启动契约：基础镜像必须提供的 shell 与用户账户 |
| `com.docker.sandbox/long-running@1` | 不依赖挂着的会话而持续运行 | openheron 是服务，不是会话：它得起来、得服务、得一直活着 |
| `com.docker.sandbox/port@1` | 容器端口 `8080` | ★ 没有它，运行时没有可转发的服务 —— 沙箱是起来的，只是没人应答 |
| `com.docker.sandbox/network-policy@1` | 运行阶段放行 `*` | ★ 边界是隔离，不是允许列表；一个装不了依赖、连不上自己 MCP server 的 agent，干不了它被交付的那份活 |
| `com.docker.sandbox/lifecycle@1` | 一个 `startup` hook | 初始化待的地方 |

★ **「什么都不写」不等于「放开」。** 运行时是默认拒绝的，
而「某个阶段整块省略 = 那个阶段什么都没有」—— 所以省略掉这项 capability，
agent 反而**一点出网都没有**。`*` 才是把「我不管」这件事表达出来的写法。

★ **workload 不声明任何凭据。** 用哪家 provider、哪个端点、哪个变量名，都是用户的事；
而一个只负责交付 openheron 的 workload，没资格替用户挑。
凭据因此移进 **mixin** —— 叠加在 workload 上、带声明而不带内容的覆盖层。

★ **mixin 是一个文件，不是一镜像。** 描述文件就是它的全部内容：
一个只放着一个 YAML 文件的目录，就是一份完整的 mixin，
运行时自己把这份纯声明构建成镜像。
★ 所以文件形态是**用户**能拿的那一种：复制一个 YAML，改一行指向自己的端点。

★ **任意多个厂商 mixin 都可以叠到同一个 workload 上**，而且互不干扰：
allow 列表按阶段取并集，每份 mixin 的凭据有自己的 service 名。
★ 但两个声称**同一个 service** 的 mixin 合不了 —— 运行时会拒绝：
`one credential has one owner`。

★ **Kit 能从哪来是策略，不是细节。** 运行时维护一份 Kit 来源的允许列表，
默认只有 `docker.io/` 与 `dhi.io/`；别的来源会被拒，而拒绝的报错里就写着想放行它的命令。
★ **registry 必须提供 HTTPS** —— 没有明文 HTTP 的选项。

★ **发行模型约束了这次集成。** 发布产物是静态、无依赖的二进制
（`CGO_ENABLED=0`），在 Linux 上交叉编译产出。
任何需要 cgo 的集成都会破坏发布模型。Kit 不会：Kit 就是一个镜像，
openheron 以同一份静态二进制进到里面。
★ 但**配方里那个二进制是动态链接的**（`sandbox-kit/openheron/openheron.dockerfile:11`），
基础镜像是 Ubuntu，所以它要的 libc 在里面；代价是二进制更大，
而且这个 Kit 绑在基础镜像的 libc 上。
★ **它带的是宿主的架构** —— amd64 宿主上构建出来的 Kit 就是 amd64 镜像。

★ **平台矩阵**：Intel Mac 根本没有 microVM 这条路，而这是平台的属性。
发布矩阵有六个目标，microVM 隔离够得着五个。
★ 这是一个真实的取舍：Intel Mac 上的用户继续用普通二进制；进 Kit 的用户拿到完整的边界。

### 4.2 凭据路径

Kit 用的机制是 `credential@1` 带 `apiKey.proxyManaged: true`：

- `apiKey.name` 是**容器内的环境变量名**；
- 有了 `proxyManaged: true`，真值**留在宿主**，容器看到的是**哨兵值** `proxy-managed`；
- 边界（宿主侧的代理）在出站请求命中 `inject` 规则时把真凭据换上去；
- 宿主的密钥库是唯一的来源 —— 一个 Kit 只能声明**需要什么**，不能指明密钥在哪。

★ **替换发生在出站请求上，而代理不知道请求是谁发的。** 它匹配的是目标域名与头名。
所以 MCP 服务器不过是那个代理的另一个客户端：它照发令牌的样子把哨兵发出去，
代理再把真值换上去。

```
the host holds the real key
  └─ sbx secret set-custom --host api.github.com --env GITHUB_TOKEN \
       --placeholder proxy-managed --value-stdin
        ↓
the container holds GITHUB_TOKEN=proxy-managed
        ↓
the MCP server reads it and sends Authorization: Bearer proxy-managed
        ↓
the host proxy substitutes the real key ──► api.github.com
```

★ **`mcps/<server>.yaml` 里一个字段都不用为它写。** 哨兵注入的是**容器**，
而子进程继承本进程的环境 —— 于是 MCP server 按它自己的约定就找到了那个变量。
★ **凭据的变量名是 MCP server 的事，openheron 不需要知道。**

★ **http 传输走的是同一条路。** `headers:` 这张表与其他一切一样展开，
所以它能引用一个环境变量：在沙箱里那个变量装的是**哨兵**，
宿主代理在出站时把真令牌换上去 —— 同一套机制、同一个 mixin 形状。
⇒ 凭据路径只有**一条**，传输方式不改它。

★ **用户只跑一条命令**：

```sh
printf '%s' "$DEEPSEEK_API_KEY" | sbx secret set-custom \
    --host api.deepseek.com --env DEEPSEEK_API_KEY \
    --placeholder proxy-managed --value-stdin
```

★ `--placeholder proxy-managed` 就是全部机关：它让宿主那边的占位符
**与运行时注入的是同一个字符串**，两边自动对上，用户完全不必再传环境变量。
`--value-stdin` 让密钥既不进进程参数，也不进 shell history。

★ **两种跑法，它们只差一件事 —— 容器最后拿着什么**：

| | 容器里是什么 | 真密钥进不进 VM |
|---|---|---|
| **占位符** | `proxy-managed` | 不进 |
| **真密钥** | `sk-…` | **进** |

★ 用真密钥直接注入时，运行时变量给什么就是什么 ——
agent 可以从环境里把它读出来，它跑的任何东西都能把它发到任何地方。
★ Kit 的 `credential@1` 声明不会挡路：注入的值盖过了这个 capability
本来要装进去的哨兵，所以同一个 Kit 两种跑法都能用。

★ **mixin 的粒度是上游，不是 MCP 服务器。** 一份凭据在（service, 阶段）上只有一个属主，
而环境是注入到**容器**的 —— 所以一份凭据声明就服务沙箱里所有读那个变量的服务器。
两个都跟 GitHub 打交道的 MCP 服务器，需要**一份**凭据 mixin 与**一次**绑定，不是两份。

★ **凭据和它的网络规则必须一起声明。** 运行时要每个 inject 域名**逐字**出现在
同阶段的 allow 列表里，而 workload 的裸 `*` 不满足这条检查 ——
mixin 于是把主机名一并加上，两张 allow 列表按阶段取并集。

★ **机制够不着的时候**：key 放在 URL query、cookie 或非 HTTP 协议里就**表达不了** ——
`inject` 是头注入。真值只能进沙箱（`sbx run --env …`），此后它就是 agent 要保护的东西。
★ 落在 VM 里的 key 能被 agent 自己的工具读到，而这是用户自己选的。

### 4.3 启动契约与 VM 里的路径语义

★ **工作区是挂在 VM 里的同一个绝对路径上 —— 但 workload 并不从那里启动。**
镜像配置拥有 `workdir`，而一个 workload Kit 完全拥有这些字段。
"agent 从工作区启动"是**内置** agent 的行为，Kit workload 跑在它镜像说的地方。
★ 所以 Kit 有一件 Dockerfile 自己做不了的活：让进程**从工作区启动**。

入口脚本（`sandbox-kit/openheron/entrypoint.sh`）只有三件事：

```sh
cd "${WORKSPACE_DIR:-$PWD}" || exit 1
openheron init --dir "$PWD" || \
    echo "[entrypoint] init did not complete; the configuration was left as it was, and with none at all the server will not start"
exec openheron
```

★ **先 `cd`，其余都跟着来**：工作目录就是项目根，配置根是 `<cwd>/.openheron`。
★ **入口脚本故意再初始化一遍** —— `openheron init` 是幂等的，
所以在 startup hook 与入口脚本里各跑一次，代价是一次空操作，
却把「hook 与入口脚本谁先谁后」这个依赖去掉了。

★ **Kit 一个 openheron 环境变量都没设。** 入口脚本 `cd` 进工作区，
openheron 自己的默认值从那里落到了正确的位置：`PROJECT_ROOT`（默认是工作目录）、
配置根（默认 `<projectRoot>/.openheron`）、`DATA_DIR`（默认是配置根下的 `state`）、
`LISTEN_ADDR`（默认 `:8080`）。
★ 来自**运行时**而不是来自 Kit 的变量：`HTTPS_PROXY` 与 `HTTP_PROXY`
（凭据替换正是靠它们），以及 `WORKSPACE_DIR`。

★ **`$PROJECT_ROOT` 刻意不在 Kit 设的那几项里。** 它挪不动配置根 ——
设了反而会让容器有两个根：一个只设了 `$PROJECT_ROOT` 的容器会得到两个不同的根，
agent 在一个目录里干活，配置却从另一个目录读。

★ **VM 里的路径语义**：工作区挂在与宿主**同一个绝对路径**上，于是
`cmd.Dir = s.root`（`agentkit/fsx/fsx.go:880`）含义不变；
shell 命令里的相对路径仍然指项目根；配置根是 `<工作区>/.openheron` ——
既是用户在宿主上编辑的那个地方，也是 `openheron init` 写入的那个地方；
报错里引用的路径，仍然是用户看到的那几个路径。
★ guest 路径一旦和宿主路径不同，模型被告知过的每一个路径都会在**最不该错的地方**错掉。
★ 而且配置就不再是用户管的了：落在挂载之外的配置根，是会随沙箱一起消失的配置根。

★ **端口要声明，宿主侧归运行时。** workload 为容器端口 `8080` 声明 `port@1`。
运行时在启动时把它发布到一个临时的回环端口上；用户从 `sbx ports` 里读端口。
★ Kit 不能钉死宿主端口 —— 两个 Kit 要同一个就会撞。
★ **family 留在默认值**（`ipv4`），因为运行时还没实现 `dual`，
而被拒的描述文件意味着沙箱根本起不来。

★ **到底是什么把服务拉起来的。** 入口脚本就是 agent 的启动命令，而拉起它的是一次 agent 会话：

- `sbx run … --detach` 建起沙箱、跑完启动 hook，却**不开 agent 会话** ——
  所以没有东西把服务拉起来。沙箱是配好的，也是哑的。
- 不带 `--detach` 的 `sbx run …` 会开 agent 会话，服务随之启动。
- ★ **而且它会一直活着** —— 靠的是 `long-running@1`：运行时不得仅因为没有会话
  挂着就停掉持有它的沙箱。于是客户端可以分离，而服务继续在跑、继续应答。

★ **状态默认就已经是持久的。** 数据目录的缺省是 `<configRoot>/state`，
而配置根是 `<工作区>/.openheron` ⇒ 默认的数据目录是 **`<工作区>/.openheron/state`** ——
在挂载上。★ 挂载属于用户、比沙箱活得久，所以对话能挺过沙箱的停止、重建，甚至移除。
规范里的 `volume@1` capability 在这件事上并不需要；
★ `DATA_DIR` 就是那个开关：绝对路径优先于默认值。

★ **边界是往外挪，这正是它便宜的原因。** Go 代码一行不改：
agent 模型、档位表、审批流程、checkpoint store、文件工具及其收敛、API 面、
内嵌的前端、发布模型 —— 一个都不动。

★ **边界自身的风险**：这条边界有过已公开的逃逸（`CVE-2026-77179`、
`CVE-2026-79994`，都在 `0.42.0` 版里修好）。官方给的缓解办法是
**clone 模式并且不要加读写主机挂载** —— 而这恰恰是 openheron 用不了的：
agent 必须往工作区里写，也必须往用户要编辑的那份配置里写。
⇒ 对这个负载来说，缓解办法不可用，答案只能是版本下限：**`sbx` 0.42.0 或更新**。

★ **agent 造出来的文件在宿主上归 uid `1000` 所有。** workload 以 `agent` 用户运行，
而挂载并不带 openheron 能依赖的 uid 映射。

### 4.4 实测

★ 2026-11-04 在真实沙箱上实测：`sbx` v0.51.0、Docker Engine 29.9.2、Linux on WSL2，
Kit 由 `sandbox-kit/` 构建（二进制本地烤入）。

| 问题 | 答案 |
|---|---|
| workload 的环境带不带 `WORKSPACE_DIR`？ | **带** —— 宿主的路径，也是工作区挂载的路径 |
| 进程是不是从工作区启动的？ | **是** —— `PWD` 与 `OLDPWD` 都是工作区 |
| 配置是不是落在挂载上？ | **是** —— `.openheron/{agents,llm,mcps,server,state}` 在宿主上，会话也在 |
| 凭据到不到模型？ | **到** —— 容器里是 `DEEPSEEK_API_KEY=proxy-managed`；代理换上真值，调用返回了 "ok" |
| 发布版配方构建得出来吗？ | **能** —— `task kit` 产出 `openheron-kit:<version>`，里面的二进制自报同一个版本号 |
| 纯声明的 mixin 没有 Dockerfile 也构建得出来吗？ | **能** —— `task kit-mixin` 只靠描述文件就产出镜像 |
| 两个厂商 mixin 能组合吗？ | **能** —— 两个哨兵都在容器里，DeepSeek 那边回了 200 |
| 两个同 service 的 mixin 能组合吗？ | **不能** —— "one credential has one owner" |
| 默认允许来自别的 registry 的 Kit 吗？ | **不允许** |
| registry 引用必须是 HTTPS 吗？ | **是** |
| 端口转发会不会缓冲 SSE？ | **不会** —— 10.3 秒里 1339 帧，首帧 +21ms |
| agent 写出的文件归谁？ | 归宿主用户 |
| 运行时会把声明的端口发布出来吗？ | **会** —— 临时的回环端口 |
| detached 起得来服务吗？ | **起不来** —— 入口脚本是 agent 的启动命令，而 `--detach` 不开会话 |
| 服务比客户端活得久吗？ | **活得久** —— `long-running@1` 留着它 |

★ **`WORKSPACE_DIR` 是运行时注入的沙箱级环境变量** —— 不是 Kit 给的，也不是镜像给的。
★ **读它有两条路，而且不对称**：workload 的环境不设限，
所以入口脚本直接 `${WORKSPACE_DIR:-$PWD}`；hook 的环境是**默认拒绝**的，
所以 hook 必须声明这个名字（`env: [WORKSPACE_DIR]`）。

★ **这次实测是脚本，不是记录稿。** `sandbox-kit/acceptance/run.sh` 把整件事跑一遍 ——
前置检查、干净的沙箱、挂载、凭据、一次模型调用、流的形状、客户端的离去，
以及从每个视角看端口 —— 任何一项不过就非零退出。
★ **帧间跨度才是要紧的验收标准**：几秒的跨度配上几毫秒的首帧，
意味着帧是随着答案生成陆续到的。若跨度很短而末帧很晚，
就说明转发层把整个响应体攥在手里到最后才放出来。

★ **而且边界真的把 agent 约束在它的活儿里。** 仓库带了一个可以指给沙箱的工作区
（`sandbox-kit/examples/demo-project`，一个带 bug 的小 Go 程序）：
agent 读了文件、跑了那个失败的测试、**在动手改之前先问了一句** ——
一次审批，问的是 `edit_file` —— 改掉了一个字符，又跑了一遍测试。
改动落在宿主上，因为工作区*就是*宿主上的一个目录。

## 5. 待决

★ 这一节里的每一条都是**未实施**的 —— 它们是决定，不是现状。

### 5.1 来自沙箱 Kit 的三条未定决定

1. **发布的 registry。** openheron 是把 Kit 发到自己控制的 registry，
   还是每个用户从源码树自己构建？
2. **skills 目录。** 它在 VM 里该放哪 —— 工作区，还是 volume？
3. **`sbx` 版本下限。** `0.42.0` 或更新 —— 因为已公开逃逸的官方缓解办法
   是 openheron 用不了的那种模式。是由 Kit 声明这个下限，还是由安装器去检查？

### 5.2 来自文件系统后端的五条

1. **提示词仍然写着「永远传绝对路径」。** 现在它只是冗余 —— 绝对路径可用，
   省略路径意味着 project root。把提示词收窄成「相对路径，或省略」是另一件事，
   有它自己的模型行为风险。
2. **`glob` 不认 `.gitignore`。** `rg` 认。如果某项目的构建产物不在垃圾名单里，
   `glob` 就会走进去。★ 读 `.gitignore` 本身还没做。
3. **每 agent 的 root。** 整个树共享一个后端实例。如果某个子 agent 该比它的父 agent
   看得更少，就按节点建一个后端 —— 仍然是后端的改动，仍然不用分叉 middleware。
4. **真要改工具层时**（一个 size 列、真正的截断措辞），**不要**复制 middleware：
   把 `deep.Config.Backend` 设为 nil，在 `agentkit/fsx` 之上写那七个工具，
   作为 `cfg.Middlewares` 传进去。约 300 行，不引 `adk/internal`，不分叉提示词。
5. **遍历根刻意不设限。** `glob` 与 `grep` 仍然可以被指到项目之外 ——
   agent 得能去模块缓存里查依赖的源码，而 `execute` 本来也能 `cat` 它。
   真正防止它变成卡死的是时间预算。若该把模块缓存加成 root，
   那是构建层的改动，不是后端的。

### 5.3 来自 MCP 的一条

★ **Kit 打包 MCP 服务器**这件事本身仍然是提案：Kit 里还没有带一个 MCP 服务器。

| 传输 | 服务器跑在哪 | Kit 需要什么 |
|---|---|---|
| stdio | **沙箱内**，作为 openheron 进程的子进程 | 服务器的二进制或它的包必须**在 workload 镜像里** —— 它是 workload 所跑内容的一部分 |
| http | **宿主上** | 一条让沙箱够得到宿主端口的 mixin 网络规则，和一个够得着的宿主侧服务 |

★ mixin 模型在这里把界线划得很干净：mixin 是**声明**（一份凭据与它的网络规则），
workload 是**内容**。跑在沙箱里的 MCP 服务器是 workload 内容；
跑在宿主上、通过 http 够到的 MCP 服务器是 mixin 的事。

★ **故意不**选第三种路：一种**安装**宿主侧 MCP 服务器的 mixin ——
那会让 mixin 带上内容，而内容属于 workload。

★ mixin **可以**把 MCP 服务器装进沙箱：它把这件事声明成一个 `lifecycle@1` 的
**install** 钩子（哪个 registry、哪个包），它的网络规则放行那个 registry。
★ mixin 本身仍然不带内容：字节是运行时去取的，不是 mixin 揣着的。
★ 这与上面那个拒绝的选项不是一回事：那一个是把**宿主侧**服务器装起来；
这一个是从 mixin 点名的 registry 把**客侧**二进制取下来 —— 一句「取什么」的声明。

## 附录 A：本文引用的代码位置

每一条都打开过文件、读过那一行。行号随代码移动。

### A.1 工具面、档位与闸门

| 位置 | 是什么 |
|---|---|
| `agentkit/spec/decl/decl.go:113` | `decl.Agent.Tools` |
| `agentkit/spec/decl/decl.go:134` | `decl.Agent.Skills` |
| `agentkit/spec/decl/decl.go:150` | `decl.Agent.MCPServers` |
| `agentkit/spec/decl/decl.go:958` | `decl.SubAgentSpec.MCPServers` |
| `agentkit/spec/decl/decl.go:366` | `decl.BuiltinToolNames` |
| `agentkit/spec/decl/decl.go:402-409` | 六个 `config_*` 工具名 |
| `agentkit/spec/decl/decl.go:487-518` | `decl.ConfigToolsCfg` —— `tools/config.yaml` 的形状与 `max_read_bytes` 的范围 |
| `agentkit/spec/decl/decl.go:665` | `decl.ParseToolNamePattern` |
| `agentkit/spec/load.go:107` | 拒绝旧键 `disabled_tools` |
| `agentkit/spec/load.go:131` | `validateAgentTools`（拒绝假通配符） |
| `agentkit/spec/load.go:430` | `Config.AllowedTools`（`tools/` 各文件的 `allows:`） |
| `agentkit/spec/load.go:356-387` | `noticeConfigToolsAbsent`（★ 文件不在时的提示） |
| `agentkit/spec/load.go:943` | `checkAgentMCPNames`（`tools:` 里的 `mcp__*` 只报提示） |
| `agentkit/spec/load.go:982` | `Config.MCPServersInUse`（并集） |
| `agentkit/build/tools.go:49` | `toolProvider` 接口 |
| `agentkit/build/tools.go:65` | `defaultToolRegistry`（MCP provider 注册处） |
| `agentkit/build/tools.go:95` | `builtinAllowedTools` |
| `agentkit/build/tools.go:126` | `reservedToolNames`（★ 带 owner：拥有它的 provider 可以提供它） |
| `agentkit/build/tools.go:152` | `toolRegistry.Build` |
| `agentkit/build/tools.go:180` | `validateBatch` |
| `agentkit/build/policy.go:55` | `Policy.TierOf` |
| `agentkit/build/policy.go:115` | `newTable`（丢掉 `tools:` 里的 MCP 条目） |
| `agentkit/build/policy.go:163` | `Table.Blocked` |
| `agentkit/build/policy.go:184` | `Table.Filter` |
| `agentkit/build/policy.go:242-258` | `LayerNotLoaded` / `LayerNotMounted` |
| `agentkit/build/spec.go:73` | `policyOf`（三处合成一张表） |
| `agentkit/build/topology.go:72` | `specTable` |
| `agentkit/build/gate.go:33` | 收窄发生在 agent 开跑之前 |
| `agentkit/build/gate.go:55` | 同步那条闸门 |
| `agentkit/build/gate.go:89` | 流式那条闸门 |
| `agentkit/build/gate.go:126` | `decide`（两条路共用的那一个决策） |
| `agentkit/build/introspect.go:36` | `reasonOf` |
| `agentkit/build/introspect.go:116` | `verdictOf` |
| `agentkit/build/execute.go:40` | openheron 自己的 `execute` 工具 |
| `agentkit/build/execute.go:109` | 工具描述里那句方言 |
| `agentkit/build/nettools.go:25` | `defaultToolOutputBytes`（20000） |

### A.2 MCP

| 位置 | 是什么 |
|---|---|
| `agentkit/spec/decl/decl.go:593` | `decl.MCPToolPrefix` |
| `agentkit/spec/decl/decl.go:602` | `decl.MCPSeparator` |
| `agentkit/spec/decl/decl.go:610` | `decl.MCPServerNamePattern` |
| `agentkit/spec/decl/decl.go:630` | `decl.MCPServerOf` |
| `agentkit/spec/decl/decl.go:692-694` | `stdio` / `http` |
| `agentkit/spec/decl/decl.go:702` | `decl.MCPMaxExposedNameLen`（64） |
| `agentkit/spec/decl/decl.go:692-695` | 两个缺省超时 |
| `agentkit/spec/decl/decl.go:717` | `decl.MCPDuration` |
| `agentkit/spec/decl/decl.go:750` | `decl.MCPServerCfg` |
| `agentkit/spec/decl/decl.go:798` | `decl.MCPFiles` |
| `agentkit/spec/config.go:26` | `spec.DirMCP` |
| `agentkit/spec/config.go:85` | `mcps/` 分类（可编辑、热） |
| `agentkit/spec/load.go:470` | `loadMCP` |
| `agentkit/spec/load.go:473` | server 名的形状是加载期硬错误 |
| `agentkit/spec/load.go:486-494` | `transport` 的取值 |
| `agentkit/spec/load.go:496-499` | http 的 `url` / `env`，stdio 的 `command` |
| `agentkit/spec/load.go:524-525` | 两个超时的缺省 |
| `agentkit/spec/load.go:912-915` | agent 点名一个不存在的 server 是加载期错误 |
| `agentkit/spec/write.go:185` | 编辑器拒绝读不回来的 server 名 |
| `agentkit/spec/write.go:309` | `mcps/` 的脚手架 |
| `agentkit/build/mcptools.go:27-30` | 两个跳过理由码 |
| `agentkit/build/mcptools.go:68` | `mcpPlan`（共享的映射） |
| `agentkit/build/mcptools.go:123` | `mcpOffers`（`tools:` 是过滤器） |
| `agentkit/build/mcptools.go:141` | `mcpExposedName`（唯一拼名的地方） |
| `agentkit/build/mcptools.go:151` | `sanitizeMCPToolName` |
| `agentkit/build/mcptools.go:173` | `mcpAllowedNames` |
| `agentkit/build/mcptools.go:194-199` | `mcpToolProvider` 与 `Optional` |
| `agentkit/build/mcptools.go:201-232` | `Provide`（「还在起」不告警） |
| `agentkit/build/mcptools.go:244` | 跳过工具的警告 |
| `agentkit/build/mcptools.go:264-269` | 组件的 `Config`（含输出封顶） |
| `agentkit/build/mcptools.go:299-309` | stdio 的 per-server 互斥锁 |
| `agentkit/build/mcptools.go:343` | `serialTool` |
| `agentkit/build/mcptools.go:384` | `capMCPToolResult` |
| `agentkit/build/mcpmanager.go:36` | `mcpRelistInterval`（30s） |
| `agentkit/build/mcpmanager.go:44` | `mcpManager` |
| `agentkit/build/mcpmanager.go:98` | `Warm`（统一的后台作业） |
| `agentkit/build/mcpmanager.go:149` | `Tools`（不等待） |
| `agentkit/build/mcpmanager.go:175` | `Refresh` |
| `agentkit/build/mcpmanager.go:215` | `ToolsOf`（实时清单） |
| `agentkit/build/mcpmanager.go:246` | `surfaceHash`（工具面） |
| `agentkit/build/mcpmanager.go:267` | `Close` |
| `agentkit/build/mcpmanager.go:285` | `mcpEntry`（状态字段） |
| `agentkit/build/mcpmanager.go:340-351` | 四个状态 |
| `agentkit/build/mcpmanager.go:358` / `:361` | `errMCPStarting` / `errMCPNotStarted` |
| `agentkit/build/mcpmanager.go:371` | `mcpStartTimeout`（退避用的期限） |
| `agentkit/build/mcpmanager.go:447` | `dispatch`（单飞 + 退避） |
| `agentkit/build/mcpmanager.go:504` | `runStart`（那个后台作业） |
| `agentkit/build/mcpmanager.go:646` | `mcpFingerprint` |
| `agentkit/build/mcpmanager.go:756` | `mcpCommandLogger` |
| `agentkit/build/mcpmanager.go:786` | `startStdioClient` |
| `agentkit/build/mcpmanager.go:840` | `killMCPChild` |
| `agentkit/build/mcpmanager.go:856` | `closeMCPClient` |
| `agentkit/build/mcpmanager.go:883` | `mcpChildEnv` |
| `agentkit/build/mcpmanager.go:897` | `startHTTPClient` |
| `agentkit/build/mcpstatus.go:16-27` | 四个 server 状态与三个 tool 状态 |
| `agentkit/build/mcpstatus.go:77` | `Builder.MCPServers`（面板的那一个投影） |
| `agentkit/build/mcpstatus.go:159` | `mcpToolViews` |
| `agentkit/build/snapshot.go:77` | `heavyDepsOf`（`sync.Once`） |
| `agentkit/build/snapshot.go:84` | `buildHeavy` |
| `agentkit/build/snapshot.go:120` | 启动日志里打印搜索引擎 |
| `agentkit/build/snapshot.go:207` | `snapshotFor`（树的身份） |
| `agentkit/build/builder.go:104` | `Builder.Warm` |
| `agentkit/build/builder.go:159-201` | `Build`（先列举，再建树） |
| `agentkit/build/manager.go:13-29` | `ConfigManager`（只有 `Current` 与 `Publish`） |
| `api/commands.go:61` | `/mcps` 这个 action |
| `api/v2_files.go:76` | `GET /v2/mcps` |
| `api/v2_mcps.go:74` | `v2MCPServers` |
| `api/v2_mcps.go:121` | `configuredMCP`（配置面视图） |
| `agentkit/run/runrow.go:88` | `noteMCPServers` |
| `agentkit/run/runrow.go:141` | `mcpServersOf`（从名字读出 server） |
| `storage/runledger.go:168` | `Run.MCPServers` |
| `main.go:490` | 预热挂在配置的加载与重载上 |
| `main.go:522` | `SetMCPSource` |
| `initcfg/initcfg.go:164` | init 写 `mcps/` 分区 |
| `agentkit/templates/files/mcps/_example.yaml` | 脚手架（开头那个下划线是标记） |

### A.3 文件系统后端

| 位置 | 是什么 |
|---|---|
| `agentkit/fsx/fsx.go:1-20` | 包注释（分叉的上游版本与理由） |
| `agentkit/fsx/fsx.go:65-80` | 五个上限常量 |
| `agentkit/fsx/fsx.go:84` | `ignoredNames` |
| `agentkit/fsx/fsx.go:156` | `New`（每个 root 一次性绝对化） |
| `agentkit/fsx/fsx.go:187` | `exec.LookPath("rg")` |
| `agentkit/fsx/fsx.go:218` | `resolve`（空路径是 root） |
| `agentkit/fsx/fsx.go:240` | `resolveWrite`（fail-closed） |
| `agentkit/fsx/fsx.go:265` | `inside` |
| `agentkit/fsx/fsx.go:278` | `ignored` |
| `agentkit/fsx/fsx.go:282` | `LsInfo` |
| `agentkit/fsx/fsx.go:309` | `ls` 的封顶提示 |
| `agentkit/fsx/fsx.go:317` | 目录以 `/` 结尾 |
| `agentkit/fsx/fsx.go:409` | `GrepRaw` |
| `agentkit/fsx/fsx.go:444` | pattern 前加 `--` |
| `agentkit/fsx/fsx.go:454` | 搜索共用 `walkBudget` |
| `agentkit/fsx/fsx.go:457` | `cmd.Dir = s.root` |
| `agentkit/fsx/fsx.go:492` | `maxGrepMatches` |
| `agentkit/fsx/fsx.go:517` | 预算耗尽的错误 |
| `agentkit/fsx/fsx.go:532-539` | 封顶提示挂在最后一条匹配上 |
| `agentkit/fsx/fsx.go:548` | `SearchEngine` |
| `agentkit/fsx/fsx.go:577` | `GlobInfo` |
| `agentkit/fsx/fsx.go:593` | 遍历的期限 |
| `agentkit/fsx/fsx.go:606` | 读不了的子树跳过自己 |
| `agentkit/fsx/fsx.go:621-641` | 深度、忽略、模式剪枝、结果上限 |
| `agentkit/fsx/fsx.go:695` | `mayHold`（按模式剪枝） |
| `agentkit/fsx/fsx.go:724` | `globPattern`（绝对 pattern 归一化） |
| `agentkit/fsx/fsx.go:837` | `ExecuteStreaming` |
| `agentkit/fsx/fsx.go:871` | `initStreamingCmd` |
| `agentkit/fsx/fsx.go:883-885` | shell 目录进子进程 PATH |
| `agentkit/fsx/grep_fallback.go:235` | `grepBuiltin`（内置引擎） |
| `agentkit/shell/shell.go:90` | `Detect` |
| `agentkit/shell/shell.go:152` | 候选 shell 的活性检查 |
| `agentkit/shell/shell.go:240` | `Note`（模型读到的方言） |
| `agentkit/build/snapshot.go:101` | `fsx.New`（两个调用点之一） |
| `agentkit/build/snapshot.go:109` | `newConfigGate`（配置根里的写走服务） |
| `agentkit/build/skill.go:251` | `fsx.New`（另一个调用点：技能目录） |

### A.4 沙箱 Kit

| 位置 | 是什么 |
|---|---|
| `sandbox-kit/openheron/openheron.yaml:18-19` | `schemaVersion: "3"` / `kind: workload` |
| `sandbox-kit/openheron/openheron.yaml:45` | `sbx@1` |
| `sandbox-kit/openheron/openheron.yaml:48` | `long-running@1` |
| `sandbox-kit/openheron/openheron.yaml:67-71` | `port@1`（容器端口 8080） |
| `sandbox-kit/openheron/openheron.yaml:81-85` | `network-policy@1`（运行阶段放行 `*`） |
| `sandbox-kit/openheron/openheron.yaml:93-98` | `lifecycle@1` 的 startup hook |
| `sandbox-kit/openheron/entrypoint.sh:5` | `cd "${WORKSPACE_DIR:-$PWD}"` |
| `sandbox-kit/openheron/entrypoint.sh:13` | `openheron init --dir "$PWD"` |
| `sandbox-kit/openheron/entrypoint.sh:16` | `exec openheron` |
| `sandbox-kit/openheron/openheron.dockerfile:11` | 那个二进制是动态链接的 |
| `sandbox-kit/openheron/openheron.dockerfile:19` | 基础镜像 |
| `sandbox-kit/openheron/openheron.dockerfile:34` | `WORKDIR` |
| `sandbox-kit/openheron-deepseek/openheron-deepseek.yaml:37-41` | mixin 的网络规则 |
| `sandbox-kit/openheron-deepseek/openheron-deepseek.yaml:59-69` | mixin 的 `credential@1` |
| `sandbox-kit/acceptance/run.sh` | 验收跑批（§6 的可重复形式） |
| `main.go:153` | 没有配置就不肯启动 |
| `main.go:445-447` | `LISTEN_ADDR` / `DATA_DIR` / `PROJECT_ROOT` 的覆盖顺序 |
| `main.go:453` | 会话目录 |
| `agentkit/spec/config.go:425` | `NewService`（项目根的缺省是工作目录） |
| `agentkit/spec/config.go:490` | `Service.ProjectRoot` |
| `agentkit/spec/config.go:492` | `Service.DataDir`（缺省 `state`） |
