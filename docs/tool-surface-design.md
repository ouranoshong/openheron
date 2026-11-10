# 工具面设计

> 状态：**§2、§3、§4 与 §5 的 P0–P5 全部落地**（2026-11-08）。§3 与 §4 里的每一处
> 「之后」都已经是「现在」，原文按落地时的实情改写过一次 —— 包括与提案不一致的地方，
> 它们各自带着理由。
> §1 保留的是**动手之前**的代码样子 —— 它是这次改造要解决的问题，不是今天的现状；
> 今天的现状见 §2 各节末尾的「已实施」标记。每一句都指向真实行号。
> 本文按 AGENTS.md 的文档语言规则**只有中文版** —— 架构与代码设计类文档不做双语。

## 0. 一句话

把两件事各自收敛成**一条单义规则**：一个 agent 能用什么工具，以及谁能管理这份配置。

## 1. 改造前（代码如实，历史）

★ **这一节描述的是 2026-11-08 之前的样子。** 它留着是为了记住**为什么**要改 ——
下面的问题 §2 已经解决。想知道今天是什么样，读 §2。

★ **本节的行号是改造前的行号，今天多半已经移动**（有的函数已经不在了，比如前端那份
`validatePath`）。一个笃定地指错地方的行号比没有行号更糟，所以这里只保证**符号名**仍然
查得到：`grep -rn 'policyOf' agentkit/`。要逐行对照，看 `a805473^` 那一版。

### 1.1 工具归属：四把钥匙，两种风格

改造前，一个 agent 的工具面由四个地方共同决定，其中三个在 agent 自己的 frontmatter 里，
一个在工具自己的文件里：

| 键 | 位置 | 缺省 | 当时做什么 |
|---|---|---|---|
| `disabled_tools` | `agents/<id>.md` | 空 = 全给 | **减法**：从全集里拿掉这些 |
| `skills` | `agents/<id>.md` | 不写 = 全部 | 三态：不写 = 全部，`[]` = 没有，有名 = 这些 |
| `mcps` | `agents/<id>.md` | 不写 = 全部 | 三态：不写 = 全部服务器，`[]` = 不挂 |
| `allows` | `tools/*.yaml`、`mcps/<server>.yaml` | 代码的缺省清单 | **加法**：点名不用审批的 |

★ **两种风格混在一起是这里的主要问题。** 三个键里两个是「缺省全给」，一个是减法；
第四个（`allows`）又是加法。一个读者要在脑子里同时维持四套缺省。

★ **而三态是靠指针实现的**：`Skills *[]string`、`MCPServers *[]string`，`nil` 与 `[]`
是两件事。于是每个消费方都得自己展开一次 —— `mcpServersOf`（nil → 全部）、
`skillScopeOf`（nil → 全部）、`cloneList`（连指针一起复制）。三处各自的注释都在解释
同一个取舍，而那只是同一个问题的三个副本。

配套的装配在（这些**今天仍在**，只是喂给它的东西变了）：

- 档位表：`agentkit/build/spec.go:64` `policyOf`，缺省清单在 `agentkit/build/tools.go:79` `builtinAllowedTools`；
- 每个工具文件的 `allows:` 汇总：`agentkit/spec/load.go:326` `AllowedTools`；
- 服务端文件里的 `allows:` 翻译成暴露名：`agentkit/build/mcptools.go:173` `mcpAllowedNames`。

### 1.2 MCP 的名字：三段，各管一件事

```
mcp__github__ls
└─┬─┘└──┬───┘└┬┘
前缀   服务器段 尾段
```

| 段 | 分开的是 | 少了它会怎样 | 常量 |
|---|---|---|---|
| `mcp__` | MCP 工具 ↔ 内置工具 | 服务器暴露 `ls` 就顶掉内置 `ls` | `agentkit/spec/decl/decl.go:352` |
| `<server>__` | 一台服务器 ↔ 另一台 | `mcp_github` 和 `mcp_minimax` 各自的 `ls` 撞成一个 | `agentkit/spec/decl/decl.go:333` |
| `<tool>` | —（服务器自己的名字，只消毒） | 带 `-`/`.`/`:` 的名字过不了厂商函数名集合 | `agentkit/build/mcptools.go:151` |

★ **双下划线是让名字可解析的那一半。** 服务器名必须匹配 `^[a-z0-9]+(_[a-z0-9]+)*$`（`agentkit/spec/decl/decl.go:369`），**不允许连续下划线**，所以 `mcp__a__b__x` 只有一种读法。如果服务器能叫 `a__b`，这个名字就有两种读法，整条规则当场失效。**正则和分隔符是一对。**

命名解决不了的两处，按服务器内部跳过（`agentkit/build/mcptools.go:68` `mcpPlan`）：

| 情形 | 结果 | 理由码 |
|---|---|---|
| 组合名超 64 字符（`agentkit/spec/decl/decl.go:394`） | 跳过那个工具 | `name_too_long` |
| 消毒后与同服务器的另一个工具重名 | 跳过后来的 | `name_collision` |

★ 两种都是**跳过 + 警告，不让进程失败**：名字是远端定的，它今天多一个古怪工具，只该赔掉那一个。

### 1.3 配置面：一份核心，四处消费，前端还自带一套规则

| 能力 | 实现位置 | 重复情况 |
|---|---|---|
| 路径/文件名合法性 | `spec.ValidatePath` | 前端有一份镜像（`settings-validate.ts` 的 `ALLOWED_FILES`） |
| 严格解码 / 未知键 | `spec.decodeStrict` | 前端**逐键重写**（`validateProvider` / `validateAgent` / `validateTool` / `validateMCP`） |
| 跨文件一致性 | `spec.Service.Validate`（试合并） | 只返回**一条**字符串，无行号、无清单 |
| 模板骨架 | `spec.Skeleton` / `spec.Templates` | 已统一，但没有对 agent 的出口 |
| manifest（分类/条目/模板） | `api/v2_settings.go` 的 handler 就地拼装 | CLI 与 tool 都问不到 |
| 档位 | `build.policyOf` | 只经 Builder 可达，配置层问不到 |
| 初始化 | `initcfg.BuildPlan` + `copySection` | 自己判断「哪些文件该写」，与 manifest 不共用 |

★ **这张表不写行号**（理由见本节开头）：它记的是改造**之前**的代码，「实现位置」一列给的是
**符号名**，它们在改动里存活了下来，所以还查得到。
★ 一处要补一句：路径闸门后来**拆成了两把** —— `ValidateReadPath`（只管形状）与
`ValidatePath`（形状 + 可编辑），见 §4.4。改造前只有后者。

★ 一个关键事实：**`yaml.v3` 的 `KnownFields` 报错自带行号**（实测：`line 2: field bogus_key not found in type decl.Server`）。前端那套逐键扫描要给的「第几行」，服务端拿得到 —— 所以那份 TS 规则是可以整体删掉的，不是搬过去。

## 2. 决定一：三张表一条规则

### 2.1 规则（✅ 已实施）

> **列出来的才有。**

| 键 | 不写 | `[]` | 管什么 | 类型 |
|---|---|---|---|---|
| `tools` | **没有** | 没有 | **非 MCP 工具**：这个 agent 能用哪些**非 MCP 工具**（内置工具、`tools/` 下各文件提供的、`config_*`） | `[]string` |
| `mcps` | **不挂载** | 不挂载 | **挂载**：挂哪些服务器；挂载一个就把该服务器的全部工具交给这个 agent | `[]string` |
| `skills` | **没有** | 没有 | skill 可见性 | `[]string` |
| `allows`（工具文件） | 代码的缺省清单 | 同左 | **审批**：哪些不用点头 | `[]string` |

★ **为什么工具缺省关门、而审批缺省开门。** 两件事的失败形态不同：忘写 `tools` ⇒ agent 什么都干不了，**当轮立刻看得见**；忘写 `allows` ⇒ 工具仍然可用，只是每次要点头，**在审批卡上看得见**。默认值应该选「错了也看得见」的那一侧 —— 而这两侧的「看得见」方向相反。

### 2.2 好处：三态退化成两态，代码是净删除

| 现在 | 之后 |
|---|---|
| `Agent.Skills *[]string` / `MCPServers *[]string` | `[]string`（指针消失） |
| `mcpServersOf(list *[]string)`：nil → 全部（`agentkit/spec/load.go:657`） | **整个函数删掉**，直接用 `a.MCPServers` |
| `skillScopeOf(list *[]string)` 的 nil 分支（`agentkit/build/skill.go:62`） | 删掉 |
| `cloneList` 的 nil 语义与「指针是为了区分没写和空」的注释 | 删掉 |
| 三处指针的文档 | 一行规则 |

★ 这是整个改造里**唯一一处「改语义反而删代码」**的地方。

### 2.3 MCP：一个键管到底（2026-11-10 改）

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

★ **挂载即授予，这是用户定的语义（2026-11-10）。** `mcps: [github]` 这一行说了两件事：把子进程拉起来，并把它的全部工具交给这个 agent。再在 `tools:` 里写一遍 `mcp__github__*` 是同一件事的第二种说法 —— 而两种说法迟早会不一致（改了一处忘了另一处），所以只留一种。

★ **代价是真实的：按 agent 收窄 MCP 工具的能力消失了。** 服务器文件自己的 `tools:` 过滤（`mcps/<server>.yaml`，`mcpOffers`）仍然是**按服务器**的收窄点，但它是所有挂载它的 agent 共享的。审批层不受影响：MCP 工具的 tier 来自服务器文件的 `allows:`，缺省 `ask` —— **挂载不等于放行**（`agentkit/build/mcptools.go:107`）。

★ **`tools:` 里的 `mcp__*` 条目被丢弃，并以一条提示报出**，而不是加载期报错（`agentkit/spec/load.go:836` `checkAgentMCPNames`）。理由：语义已经变了，不是自相矛盾 —— 硬失败只会挡住一个本来能用的配置。提示要说清两件事：`tools` 不点名 MCP 工具，`mcps:` 才是挂载服务器并授予工具的地方。

★ **`mcp__github__*` 这个通配不再有意义，但边界规则仍然成立。** 双下划线保证服务器名的右边界：它匹配 `mcp__github__ls`，不匹配 `mcp__github_x__y`（后者是服务器 `github_x`）。命名空间三段式**不可协商**（`agentkit/spec/decl/decl.go:461`）。

### 2.4 由此删掉的两样东西

**1. `Table.Filter` 没有「不筛」这个分支了。** 缺省关门 ⇒ 白名单永远生效：

```go
// 现在：名单为空就不筛（一行最容易写反的短路，agentkit/build/policy.go:166）
if len(t.disabled) == 0 && len(t.denyMCP) == 0 { return tools, nil }

// 之后：永远只留点名的
if !t.sees(name) { removed++; continue }
```

**2. `denyMCP` / `LayerMCPScope` 退休。** 全局 deny 清单和 MCP 前缀 deny 那一层一起没了
（`agentkit/build/policy.go` 现在只有 `allow` 与 `ask` 两档）。`mcps` 只管挂载；没挂载的服务器，
其工具**根本不存在**，不需要再用前缀去挡。于是「这个工具为什么不在」只剩**两个**理由，
而它们要指给用户看的那把钥匙是不同的：非 MCP 工具是 `tools` 没点名，某个 MCP 工具是它的服务器
**没挂载**（`agentkit/build/introspect.go:36` `reasonOf`：新增 `LayerNotMounted` ⇒ `"mcps"`）。

### 2.5 迁移：硬失败 + 向导

`disabled_tools` 必须**加载期报错**，且不能只报「字段不存在」—— 因为最可能的反应是**把键名一改就完事**，那会把黑名单静默变成白名单，语义正好翻反。所以留一个只用于报错的过渡字段：

> `agent main: disabled_tools 改名为 tools，而且含义反了 —— tools 说的是这个 agent 拿到哪些工具。删掉这个键，然后列出你要的。`

不做别名：`corpus` → `corpus_dir` 那种「旧键仍生效」在这里会**静默反义**，比不兼容更糟。

★ **这次迁移不会静默咬到正在工作的配置**：`tools` 是全新的键且缺省关门，所以**每一个 agent 文件都必须被改**；改的时候 `mcps`/`skills` 就在旁边。能力收缩和显式编辑发生在同一刻。

### 2.6 三条加载期提示（不是错误）

| 条件 | 提示 | 为什么是提示不是错误 |
|---|---|---|
| `tools` 里有个非 `mcp__` 开头的名字，既不是内置名、也不是已启用 provider 的名字 | 「点名了不存在的工具」 | provider 可能只是还没配；`mcp__*` 跳过（服务器没起来之前无从得知），所以这条检查精确、不误报 |
| `tools` 里有 `skill`，但 `skills` 没写 | 「能调技能工具，但一个技能都加载不了」 | 配置自相矛盾，但语法合法 |
| `tools` 里有 `mcp__<server>__*` | 「`tools` 不点名 MCP 工具，`mcps:` 才是挂载服务器并授予工具的地方」 | 语义已经变了（挂载即授予），不是自相矛盾 —— 拦下启动只会挡住一个本来能用的配置 |

### 2.7 模板策略：让缺省值咬不到人

出厂配置里**不依赖任何缺省**，每个 agent 都显式写全：

```markdown
---
description: …
model: deepseek/deepseek-flash
tools: [ls, read_file, glob, grep, task, write_todos, skill]
mcps: []
---
```

★ `main` 必须有 `task`，否则它不能委派 —— 失败形态是「它会说话但不会干活」。这是改造里最容易漏的一步。

## 3. 决定二：配置面收敛成一个核心、四个薄壳（✅ 已实施）

### 3.1 形状

```
              ┌──────────────────────────────────┐
              │  spec.Service（文件层事实）        │
              │  Manifest / Read / Diagnostics    │
              │  Write / Delete / Skeleton        │
              │  Notices / Fingerprint            │
              └───────────────┬──────────────────┘
                              │  *spec.Service + *spec.Config
              ┌───────────────▼──────────────────┐
              │  build：六个 config 工具（§4）     │
              └──┬─────────────┬─────────────┬───┘
                 │             │             │
        HTTP /v2/…          CLI init      六个 config 工具
        五个薄 handler       薄壳          agentkit/build/configtools.go
```

★ 「一个核心」是字面意义上的：`Manifest` 与 `Diagnostics` 都是 `spec.Service` 的方法，
HTTP、CLI 和工具都只调它们，没有第二个装配点。下面三刀是这三条腿的实现。

### 3.2 三刀

**刀一：manifest 下沉进 `spec`（✅）。** `spec.Service.Manifest(live *Config)`
（`agentkit/spec/manifest.go:18`）返回 `Manifest`（`:18`）：分区、文件、模板、模型词汇、
指纹。`live` 只被读两处 —— agent 模板里的可用模型、模型选择器的档位词汇 —— 因为那两个是
**进程手里**的值，不是磁盘上的值。

它带 `json` tag 直接上线：这份值**就是**一个视图，配置层是唯一知道答案的地方，再在
`api` 里声明一遍结构体就是第二个要同步的东西（`build.SkillInfo` 同理）。

★ **代价与收益都记在这里**：`api/v2_settings.go` 从 325 行降到 264 行，不是提案里估的
「约 80 行」。少掉的 61 行是那段拼装；剩下的 264 行是**五个端点**的 HTTP 管道
（错误映射、请求体、`revision` 落款），它们没有别的地方可去。收益不在行数上，在
「CLI 与工具能问到同一个答案」上。

顺带修掉三个真问题：`ReasoningLevels` 与 `ModelRefs` 都在 nil 检查**之前**读
`len(c.Models)`，而 `openheron init` 跑的正是「还没有配置」这个状态；分区标签被一次全局
替换写成了 `"decl.Server"`，没人读所以没人发现。

**刀二：`Validate`（一条字符串）→ `Diagnostics`（一组带行号的 issue）（✅）。**
`spec.Issue`（`agentkit/spec/diag.go:26`）带 `Path` / `Line` / `Severity` / `Message`；
`spec.Diagnostics`（`:42`）是整份答案。`Service.Diagnostics`（`:71`）问「这份内容能不能
落地」，`Service.Check`（`:100`）问「磁盘上这棵树能不能加载」。

★ **`Line: 0` 不是第 0 行，是「没有行」。** 跨文件规则没有位置可指，它的消息里点名文件；
渲染层照这个约定办事（`web/src/lib/settings-validate.ts:31` `issueLine`）。

★ **行号要真，不能只是「有」。** yaml.v3 报的是**它读到的那一块**的行号，而 agent 文件解的
是 frontmatter —— 两块只在 `---` 位于第 1 行时才对齐。文件以空行开头时整体偏移，且是
**静默**偏移：一个笃定地指错地方的题注，比没有题注更糟。所以
`splitFrontmatter` 多返回一个起始行（`agentkit/spec/expand.go`），`parseShift`
（`agentkit/spec/diag.go:188`）把偏移带到最外层。

★ **解析失败就没有 hint。** hint 挂在 `*Config` 上，而解析失败就没有这个对象 —— 这是
边界，不是遗漏。所以 `validate()` 把 notice 的收集挪到硬规则**之前**：一条规则失败不该
把同一份答案里「你大概不是这个意思」的部分一起吞掉。

**刀三：档位的出口（✅，但与提案不同）。** 提案是给 `build` 加一个 `Tiers(agentID)`
薄封装；落地时不需要它 —— `config_tier` 与档位装配在同一个包里，直接调
`policyOf(cfg)`（`agentkit/build/spec.go:64`）拿到 `Policy`，再 `TierOf`。多加一层
封装只会多一个和 `policyOf` 抢位置的东西。

### 3.3 前端后端化：删什么、留什么（✅）

| 文件 | 处置 | 行数 |
|---|---|---|
| `web/src/lib/settings-validate.ts` | **规则整段删除**：`ALLOWED_FILES`、`MCP_SERVER_NAME`、`validatePath`、`validateContent` 与四个 `validate*`、`check` | 397 → 100 |
| `settings-store.ts` | 保留状态机；`edit()` 不再本地校验，改为防抖后的服务端调用（`VALIDATE_DEBOUNCE_MS`，`web/src/lib/settings-store.ts:201`） | 288 → 344（多出来的是防抖与时效检查） |
| `new-file-dialog.tsx` | 文件名不再本地校验：提交后由服务端定夺，被拒时**对话框不关**，把服务端那句话放在输入框下面 | — |
| `fake-server.ts` | `/v2/settings/validate` 返回 `issues[]`；`/v2/settings/item` 的写入走 `pathProbe`，与服务端的拒绝形状一致 | — |

★ **`resolveTemplate` 留下了。** 提案说「服务端按 path 直接给骨架」，但 manifest 已经把
「文件模板优先于分区模板」这件事算完了，客户端只是查表加回退 —— 删掉它反而要在客户端之外
再造一个「按 path 取骨架」的端点，为一件事加一个入口。

★ **代价说清楚了**：校验从 0 ms 变成一次往返，缓解是 **250 ms 防抖 + 落款**
（`web/src/lib/settings-store.ts:229` 的 `validateSeq`）。一个回包只有在**仍是最新的一次**
且**仍是选中的那个文件**时才被采纳 —— 否则两张答案赛跑，赢的是慢的那张，也就是关于旧文本
的那张。保存自己再问一次（`cancelPendingValidate` + 立即 validate）：防抖窗口里点的保存
不能凭旧判决就落盘。

★ **净删**：TS 侧少了约 300 行规则与它们的用例（311 → 271 个单测），Go 侧多了
`diag.go` 的 200 行，其中大半是注释。

★ **一条规则是「搬到服务端」而不是「删掉」。** 前端原本拿模型的 `reasoning` 词汇表检查
agent 的档位，而 loader 不检查 —— 于是 vendor 没有的档位能一路加载成功，发出去就是
「一点思考都没有」。现在它在 `validate()` 里（`agentkit/spec/load.go:668` `checkReasoning`），检查的是**每一个**文件，不只是在这个编辑器里改过的那些。

### 3.4 CLI 统一（✅）

`initcfg.BuildPlan`（`initcfg/initcfg.go:56`）取一次 manifest：`items` 是跳过表，
`templates` 是内容。`copySection`（`:198`）按分区前缀挑，`p.add`（`:275`）查
`items` 而不是 `os.Stat`。

`llm/` 仍从 templates 包读，这是刻意的：它的文件是**备选**（provider 自己的模板与通用
兜底），所以 manifest 根本不带 `llm/` 模板。

★ 判据落实了：`init` 的产物**逐字节不变**（同样的文件、同样的哈希，与「新建文件」问的是
同一份模板），而 `init` 与「新建文件」现在问的是同一个东西。★ `skills/` 是唯一的**嵌套**
分区，所以 `copySection` 走的是 `templates.Nested` 那条路（`:198`）—— 扁平的文件树看不见
`skills/<name>/SKILL.md`。

## 4. 决定三：六个 config 工具（✅ 已实施）

放在 `agentkit/build/configtools.go`，与其他 provider 一样注册进 `defaultToolRegistry`
（`agentkit/build/tools.go`）。★ 读的那几个只认 `ValidateReadPath` 放行的路径，
写的那一个只认 `ValidatePath` 放行的路径 —— 两把闸门，没有新的逃逸口（§4.4）。

| 工具 | 输入 | 输出 | 复用 | 档位 |
|---|---|---|---|---|
| `config_get` | `{path}` 或 `{}` | 单文件：`{path, exists, hot, revision, content}`；无参：分区表、文件清单、**生效**配置摘要 | `Manifest` / `ReadFile` | allow |
| `config_validate` | `{path, content}` 或 `{}` | `{ok, issues[], configVersion}` | `Diagnostics` / `Check` | allow |
| `config_template` | `{path}` / `{section}` | `{path, content}` / `{section, templates}`；都没有时给索引 | `TemplateFor` / `Templates` / `Skeleton` | allow |
| `config_notices` | `{}` | `{deferred[], version, configVersion, watch}` | `Deferred()` / `Fingerprint()` | allow |
| `config_tier` | `{tool}` / `{agent}` | `{tool, tier}` / `{agent, tools[]}`；都没有时 `{defaultTier, allow[]}` | `policyOf` / `TierOf` | allow |
| `config_write` | `{path, content, baseRevision?}` | `{path, revision, configVersion, applied, requiresRestart, note}` | `Write`（`agentkit/spec/write.go`） | **ask** |

### 4.1 四个要点

★ **`config_write` 是「加强」不是「包装」。** `Service.Write` 已经是「预校验 → 查
baseRevision → 拒明文 → 原子替换 → 试合并 → 失败回滚」。用文件工具改配置**做不到**这些：
它无法检测「我读完之后别人又改了」，也无法保证「合并失败不留半个文件」。测试钉住了后者：
被拒的写入之后，文件与写入前逐字节相同。

★ **恒 ask 需要一条规则，不能只靠「不在清单里」。** 档位表是「代码清单 + 每个文件的
`allows:` + MCP 服务器的 `allows:`」合起来的，所以一个文件只要写
`allows: [config_write]` 就能给它自己解锁。`alwaysAsk`（`agentkit/build/tools.go:93`）
在**合并之后**把它剥掉 —— 一个能给自己授权的文件，授的是重写它自己的工具。这一条有测试
（本仓库的测试配置正好写了 `allows: [config_write]`）。

★ **工具自己界定答案大小，不套通用输出截断。** 通用截断砍的是**字符串**，对 JSON 答案来说
就是一份解析不了的文件；而对 `config_get` 来说，它会留下「半个文件 + 匹配的 revision」——
正是把另一半改没的那条路。所以 `config_get` 超过 64 KiB（
`agentkit/build/configtools.go:340`）**拒绝**而不是截断，`config_template` 一次只给一个
分区，什么都不问时给索引。

★ **`config_get` 不会泄露密钥。** 配置里只有 `${ENV}` 或 `@file:` 引用，值在请求时才解析；
读文件拿到的永远是引用。

### 4.2 全树校验仍是 fail-fast

`loadWithMany` 遇到第一个错误就返回，所以 `config_validate {}` 给的是「第一条阻塞错误 +
全部 hint」。要一次给全部，得把 loader 从「返回 error」改成「累积 issues」——
**本轮没做**：单文件校验已经完整，而改配置本来就是一次一个文件。

### 4.3 自举循环：允许

`config_write` 允许写任意放行路径，**包括 agent 文件自己**。生效时点是下一轮的
`PrepareAgent`（`agentkit/run/loop.go`），不影响当前轮次和正在进行的 LLM 请求。返回里带
`applied` / `requiresRestart` 说明这次到底生效了没有，不做额外禁止 —— 禁止的收益抵不上
规则复杂度。

★ 它**不能**写 `server/`：那一节不是热加载的，写闸门直接拒绝。
★ 但它**读得了** `server/` —— 读与写是两把闸门：`ValidateReadPath` 只管路径形状，
不问这一节能不能编辑，而只读的那一节恰恰是读的人最想看的（§4.4）。

### 4.4 读与写是两把闸门

★ `agentkit/spec/write.go` 现在有**两个**校验入口，它们回答的不是同一个问题：

| 入口 | 管什么 | 谁在用 |
|---|---|---|
| `ValidateReadPath` | **路径形状**：在配置根之外、分区不存在、文件名这一分区不接受 —— 读也不认 | `config_get`，API 的读路径（`api/v2_settings.go` 的 `readPath`） |
| `ValidatePath` | 形状 **+ 可编辑**：只读分区（`server/`）直接拒 | `config_write`，API 的写路径（`writePath`） |

★ 写闸门**保留原名** `ValidatePath`：它的既有调用者**全是写者**，而一个写者拿到更严的
答案是安全的，一个写者拿到更松的答案就是动了不该动的文件。
★ 拆开的原因是只读的那一节恰恰是读的人最想看的：`server/` 那一节不是热加载的，
读它不是为了改它，是为了知道**进程手里是什么** —— 而写闸门连读都拒绝，
所以「能读不能写」这件事以前在代码里根本没有位置。

## 5. 阶段

| 阶段 | 内容 | 判据 | 状态 |
|---|---|---|---|
| **P0** | 工具归属翻转：`disabled_tools` → `tools`，三张表统一「列出来的才有」，MCP 两级，删 `denyMCP`/`LayerMCPScope`，模板全显式 | `task check` 绿；`init` 产物能加载；用例见 §5.1 | ✅ `a805473` |
| **P0′** | MCP 语义翻转（2026-11-10，用户决定）：`mcps:` **挂载即授予**该服务器的全部工具；`tools:` 只管**内置**工具；`tools:` 里的 `mcp__*` 从加载期错误变成一条提示 | `task check` 绿；用例见 §5.1 | ✅ 本次改动 |
| **P1** | `Service.Manifest()` 下沉，`api/v2_settings.go` 变薄 | 现有 e2e 全绿，零行为变化 | ✅ |
| **P2** | `Service.Diagnostics`（带行号），`/v2/settings/validate` 返回 `issues[]` | 新用例：未知键 → `line: 3` | ✅ |
| **P3** | 前端删规则、改防抖 | e2e 绿；单测 311 → 271（规则用例搬去 Go） | ✅ |
| **P4** | `build/configtools.go` 六个工具 | 端到端：模型真的调 `config_tier` 并拿到 `{"tool":"config_write","tier":"ask"}` | ✅ |
| **P5** | `initcfg` 改吃 manifest | `init` 产物逐字节不变 | ✅ |
| **P6** | 文档同步 | `task lint-docs` 绿 | ✅ |

P1 必须先于 P2/P3（manifest 是 Diagnostics 的输入）；P4 必须晚于 P2（工具复用
Diagnostics）。这条顺序是真实的：P3 的 `issues` 就是 P2 的形状。

### 5.1 P0 / P0′ 必须有的新用例

| 用例 | 断言 |
|---|---|
| `tools` 不写 | 零工具 |
| `tools: []` | 零工具（与不写同义） |
| `tools: [ls]` | 只有 `ls` |
| `task` 缺席 | `main` 无法委派（拓扑校验能报出来更佳） |
| `mcps` 不写 | 不挂任何服务器 |
| `mcps: [github]` | 该服务器的全部工具都归这个 agent（挂载即授予） |
| `mcp__*` 出现在 `tools` | 被丢弃 + 一条提示（不再是加载期错误） |
| `tools: [mcp__github__*]` | 不授予任何东西 —— MCP 工具由 `mcps:` 授予 |
| 服务器没挂，而它的工具存在 | 挡在 `LayerNotMounted`，内省面板的理由指向 `mcps` |
| `disabled_tools` 仍在 | 加载期报错，文案指明改名且反义 |
| `tools` 里拼错的非 MCP 名字 | 有提示 |

## 6. 风险与代价

**1. 白名单对「宽 agent」更啰嗦。** 工厂的 `main` 从前写 3 个名字（`disabled_tools`），现在写 6 个（`tools`）。说「不要这三样」本来就比说「只要这六样」短。收益是**可审计**：读一行就知道这个 agent 能干什么，不用在脑子里做减法。

**2. `tools` 少写一个 `task`，`main` 就废了。** 失败形态是「会说话但不会干活」，比报错更难查。所以模板要逐个核准，并在测试里钉住。

**3. 前端的 250 ms 延迟是实打实的交互成本。** 打字时不卡（防抖就是为了这个），但状态栏那句
「Valid」会比从前晚半拍出现，而一次保存如果撞上窗口，会先等一次往返。

**4. 六个工具让 agent 能改自己的配置。** `config_write` 恒 ask 是这一点上的唯一正确解，
但「恒 ask」需要 §4.1 那条 `alwaysAsk` 规则，不能只靠「不在代码清单里」。

## 7. 与现有文档的关系

**P0–P6 全部落地，下面四篇已按本文的 §2、§3、§4 同步过** —— 配置系统、分层与装配、
MCP 接入、开发手册各自把「验证在前端」的段落改成了「验证在服务端」，并把六个 config 工具
写进了它们的工具清单：

- `docs/config-design.md` —— 配置系统（键表、档位模型、校验的落点）
- `docs/architecture.md` —— 分层与装配
- `docs/mcp-design.md` —— MCP 接入（命名空间与挂载即授予）
- `docs/developer-manual.md` —— 开发手册（前端的纯函数层不再有校验规则）

★ 判据很简单：一篇文档只写**代码里有的东西**。本文 §3 与 §4 现在写的是树上的东西，
所以它们也进了上面四篇。
