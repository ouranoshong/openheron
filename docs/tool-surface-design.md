# 工具面设计

> 状态：**§2（决定一）已落地**（2026-11-08，提交 `a805473`）；**§3 与 §4 仍是提案**，尚未落地。
> §1 保留的是**动手之前**的代码样子 —— 它是这次改造要解决的问题，不是今天的现状；
> 今天的现状见 §2 各节末尾的「已实施」标记。每一句都指向真实行号。
> 本文按 AGENTS.md 的文档语言规则**只有中文版** —— 架构与代码设计类文档不做双语。

## 0. 一句话

把两件事各自收敛成**一条单义规则**：一个 agent 能用什么工具，以及谁能管理这份配置。

## 1. 改造前（代码如实，历史）

★ **这一节描述的是 2026-11-08 之前的样子。** 它留着是为了记住**为什么**要改 ——
下面的问题 §2 已经解决。想知道今天是什么样，读 §2。

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

- 档位表：`agentkit/build/spec.go:57` `policyOf`，缺省清单在 `agentkit/build/tools.go:61` `builtinAllowedTools`；
- 每个工具文件的 `allows:` 汇总：`agentkit/spec/load.go:259` `AllowedTools`；
- 服务端文件里的 `allows:` 翻译成暴露名：`agentkit/build/mcptools.go:173` `mcpAllowedNames`。

### 1.2 MCP 的名字：三段，各管一件事

```
mcp__github__ls
└─┬─┘└──┬───┘└┬┘
前缀   服务器段 尾段
```

| 段 | 分开的是 | 少了它会怎样 | 常量 |
|---|---|---|---|
| `mcp__` | MCP 工具 ↔ 内置工具 | 服务器暴露 `ls` 就顶掉内置 `ls` | `agentkit/spec/decl/decl.go:324` |
| `<server>__` | 一台服务器 ↔ 另一台 | `mcp_github` 和 `mcp_minimax` 各自的 `ls` 撞成一个 | `agentkit/spec/decl/decl.go:333` |
| `<tool>` | —（服务器自己的名字，只消毒） | 带 `-`/`.`/`:` 的名字过不了厂商函数名集合 | `agentkit/build/mcptools.go:151` |

★ **双下划线是让名字可解析的那一半。** 服务器名必须匹配 `^[a-z0-9]+(_[a-z0-9]+)*$`（`agentkit/spec/decl/decl.go:341`），**不允许连续下划线**，所以 `mcp__a__b__x` 只有一种读法。如果服务器能叫 `a__b`，这个名字就有两种读法，整条规则当场失效。**正则和分隔符是一对。**

命名解决不了的两处，按服务器内部跳过（`agentkit/build/mcptools.go:68` `mcpPlan`）：

| 情形 | 结果 | 理由码 |
|---|---|---|
| 组合名超 64 字符（`agentkit/spec/decl/decl.go:394`） | 跳过那个工具 | `name_too_long` |
| 消毒后与同服务器的另一个工具重名 | 跳过后来的 | `name_collision` |

★ 两种都是**跳过 + 警告，不让进程失败**：名字是远端定的，它今天多一个古怪工具，只该赔掉那一个。

### 1.3 配置面：一份核心，四处消费，前端还自带一套规则

| 能力 | 实现位置 | 重复情况 |
|---|---|---|
| 路径/文件名合法性 | `agentkit/spec/write.go:57` `ValidatePath` | 前端有一份镜像（`web/src/lib/settings-validate.ts:34` `ALLOWED_FILES`） |
| 严格解码 / 未知键 | `agentkit/spec/config.go:441` `decodeStrict` | 前端**逐键重写**（`:254` `validateProvider`、`:319` `validateAgent`、`:201` `validateTool`、`:172` `validateMCP`） |
| 跨文件一致性 | `agentkit/spec/write.go:403` `Validate`（试合并） | 只返回**一条**字符串，无行号、无清单 |
| 模板骨架 | `agentkit/spec/write.go:161` `Skeleton`、`agentkit/spec/templates.go:23` `Templates` | 已统一，但没有对 agent 的出口 |
| manifest（分类/条目/模板） | `api/v2_settings.go:120` 就地拼装 | CLI 与 tool 都问不到 |
| 档位 | `agentkit/build/spec.go:57` `policyOf` | 只经 Builder 可达，配置层问不到 |
| 初始化 | `initcfg/initcfg.go:48` `BuildPlan` + `:165` `copySection` | 自己判断「哪些文件该写」，与 manifest 不共用 |

★ 一个关键事实：**`yaml.v3` 的 `KnownFields` 报错自带行号**（实测：`line 2: field bogus_key not found in type decl.Server`）。前端那套逐键扫描要给的「第几行」，服务端拿得到 —— 所以那份 TS 规则是可以整体删掉的，不是搬过去。

## 2. 决定一：三张表一条规则

### 2.1 规则（✅ 已实施）

> **列出来的才有。**

| 键 | 不写 | `[]` | 管什么 | 类型 |
|---|---|---|---|---|
| `tools` | **没有** | 没有 | **可见性**：这个 agent 看得见哪些工具 | `[]string` |
| `mcps` | **不挂载** | 不挂载 | **挂载**：要不要把那个子进程拉起来 | `[]string` |
| `skills` | **没有** | 没有 | skill 可见性 | `[]string` |
| `allows`（工具文件） | 代码的缺省清单 | 同左 | **审批**：哪些不用点头 | `[]string` |

★ **为什么工具缺省关门、而审批缺省开门。** 两件事的失败形态不同：忘写 `tools` ⇒ agent 什么都干不了，**当轮立刻看得见**；忘写 `allows` ⇒ 工具仍然可用，只是每次要点头，**在审批卡上看得见**。默认值应该选「错了也看得见」的那一侧 —— 而这两侧的「看得见」方向相反。

### 2.2 好处：三态退化成两态，代码是净删除

| 现在 | 之后 |
|---|---|
| `Agent.Skills *[]string` / `MCPServers *[]string` | `[]string`（指针消失） |
| `mcpServersOf(list *[]string)`：nil → 全部（`agentkit/spec/load.go:574`） | **整个函数删掉**，直接用 `a.MCPServers` |
| `skillScopeOf(list *[]string)` 的 nil 分支（`agentkit/build/skill.go:62`） | 删掉 |
| `cloneList` 的 nil 语义与「指针是为了区分没写和空」的注释 | 删掉 |
| 三处指针的文档 | 一行规则 |

★ 这是整个改造里**唯一一处「改语义反而删代码」**的地方。

### 2.3 MCP 的两级：挂载 ≠ 可见

```yaml
# .openheron/agents/main.md
mcps: [github]                            # ① 挂载：把 github 拉起来
tools: [ls, read_file, mcp__github__*]    # ② 可见：这个 agent 看得见 github 的全部工具
```

| 级 | 谁回答 | 缺省 |
|---|---|---|
| 命名 | 代码，`mcp__<server>__<tool>`（**非协商**） | 外部工具必带命名空间 |
| 挂载 | `mcps` | 不挂 |
| 可见 | `tools` | 没有 |

★ **两级都要写，这是刻意的冗余。** `mcps: [github]` 只说「拉起来」；`tools` 里不点名 `mcp__github__*` 就一个工具都看不见。默认关门的代价就是每次都要说「我要」。

★ **`tools` 里必须写完整前缀。** 写 `ls` 拿到的是内置那个，两台 MCP 的 `ls` 一个都拿不到 —— 这是前缀规则直接带来的结果，不是限制。

★ **矛盾要在加载期报错**：`tools` 点了 `mcp__github__*`，而 `mcps` 排除了 `github` ⇒ 前缀永远匹配不到东西。我们自己的配置大声失败。

★ **`mcp__github__*` 这个通配是安全的**，因为双下划线保证了服务器名的右边界：它匹配 `mcp__github__ls`，不匹配 `mcp__github_x__y`（后者是服务器 `github_x`，第 13 位是 `x` 不是 `_`）。

### 2.4 由此删掉的两样东西

**1. `Table.Filter` 没有「不筛」这个分支了。** 缺省关门 ⇒ 白名单永远生效：

```go
// 现在：名单为空就不筛（一行最容易写反的短路，agentkit/build/policy.go:166）
if len(t.disabled) == 0 && len(t.denyMCP) == 0 { return tools, nil }

// 之后：永远只留点名的
if !t.sees(name) { removed++; continue }
```

**2. `denyMCP` / `LayerMCPScope` 退休**（`agentkit/build/policy.go:129`、`:228`）。`mcps` 只管挂载；没挂载的服务器，其工具**根本不存在**，不需要再用前缀去挡。于是「这个工具为什么不在」只剩一个理由：`tools` 没点名（`agentkit/build/introspect.go:36` `reasonOf` 从两个值变成一个）。

### 2.5 迁移：硬失败 + 向导

`disabled_tools` 必须**加载期报错**，且不能只报「字段不存在」—— 因为最可能的反应是**把键名一改就完事**，那会把黑名单静默变成白名单，语义正好翻反。所以留一个只用于报错的过渡字段：

> `agent main: disabled_tools 改名为 tools，而且含义反了 —— tools 说的是这个 agent 拿到哪些工具。删掉这个键，然后列出你要的。`

不做别名：`corpus` → `corpus_dir` 那种「旧键仍生效」在这里会**静默反义**，比不兼容更糟。

★ **这次迁移不会静默咬到正在工作的配置**：`tools` 是全新的键且缺省关门，所以**每一个 agent 文件都必须被改**；改的时候 `mcps`/`skills` 就在旁边。能力收缩和显式编辑发生在同一刻。

### 2.6 两条加载期提示（不是错误）

| 条件 | 提示 | 为什么是提示不是错误 |
|---|---|---|
| `tools` 里有个非 `mcp__` 开头的名字，既不是内置名、也不是已启用 provider 的名字 | 「点名了不存在的工具」 | provider 可能只是还没配；`mcp__*` 跳过（服务器没起来之前无从得知），所以这条检查精确、不误报 |
| `tools` 里有 `skill`，但 `skills` 没写 | 「能调技能工具，但一个技能都加载不了」 | 配置自相矛盾，但语法合法 |

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

## 3. 决定二：配置面收敛成一个核心、四个薄壳

### 3.1 目标形状

```
              ┌──────────────────────────────────┐
              │  spec.Service（文件层事实）        │
              │  Manifest / Read / Diagnostics    │
              │  Write / Delete / Skeleton        │
              │  Notices / Fingerprint            │
              └───────────────┬──────────────────┘
                              │  *spec.Service + *build.Builder
              ┌───────────────▼──────────────────┐
              │  build 暴露 Tiers(agentID)        │
              └──┬─────────────┬─────────────┬───┘
                 │             │             │
        HTTP /v2/…          CLI init      六个 config 工具
        五个薄 handler       薄壳          build/configtools.go
```

### 3.2 三刀

**刀一：manifest 下沉进 `spec`。** 它需要的一切（`spec.Categories`、`Skeleton`、`Templates`、`ListFiles`、`ReadFile`、`ModelRefs`、`ReasoningLevels`、`Fingerprint`）**已经全在 spec 里**，所以是纯搬运；`api/v2_settings.go:120` 从 325 行降到约 80 行。

**刀二：`Validate`（一条字符串）→ `Diagnostics`（一组带行号的 issue）。**

```go
type Issue struct {
    Path     string   // 相对配置根
    Line     int      // 0 = 整份文件/跨文件
    Message  string
    Severity string   // "error" | "hint"
}
```

内部三段：`decodeStrict`（`agentkit/spec/config.go:441`）的 `yaml.TypeError` 映射成带行号的 error → 试合并失败加一条 `Line: 0` 的 error → `Deferred()`（`agentkit/spec/config.go:143` `deferKey`）转成 hint。

★ **一个函数同时服务 HTTP、工具和前端**，这是「同一套核心处理逻辑」的落点。

**刀三：档位出口。** `agentkit/build` 已有 `PolicyTools()`/`EffectiveTools()`，加一个 `Tiers(agentID)` 薄封装。工具侧在 `build` 内实现（`build` 已经 import `spec`，不产生环）。

### 3.3 前端后端化：删什么、留什么

| 文件 | 处置 |
|---|---|
| `web/src/lib/settings-validate.ts`（397 行） | **删掉规则部分**（约 250 行：`ALLOWED_FILES` 与四个 `validate*`）；保留 `Issue` 类型和纯路径辅助 |
| `settings-store.ts` | 保留状态机；`check()` 换成防抖后的服务端调用 |
| `new-file-dialog.tsx` | `resolveTemplate` 的「文件模板优先」逻辑删掉 —— 服务端按 path 直接给骨架 |
| `fake-server.ts` | `/v2/settings/validate` 改为返回 issues 列表，与真实形状对齐 |

★ **代价必须说清楚**：现在校验是同步的（0 ms），之后是一次往返。缓解是 **250 ms 防抖 + 只在有改动时发 + 带 revision 的落款**（回来对不上就丢弃）。不这样做，打字会卡。

★ 这是**净删代码**：约 −250 行 TS，Go 侧 +60 行 `Diagnostics`。

### 3.4 CLI 统一

`initcfg.BuildPlan` 改为消费 `Service.Manifest()` 的 `items`（哪些存在）+ `templates`（写什么），不再自己遍历模板目录。`initcmd.go` 只留参数解析与输出。

★ 效果：`init` 写出来的东西和编辑器「新建文件」拿到的骨架**必然一致**（同一个 `Skeleton`），不会再有第二个真相。

## 4. 决定三：六个 config 工具

放在 `agentkit/build/configtools.go`，与其他 provider 一样注册进 `defaultToolRegistry`。全部只认 `ValidatePath` 放行的路径，没有新的逃逸口。

| 工具 | 输入 | 输出 | 复用 | 档位 |
|---|---|---|---|---|
| `config_validate` | `{path, content}` 或 `{}` | `{ok, issues[], configVersion}` | `Diagnostics` | allow |
| `config_template` | `{path}` 或 `{section}` | `{path, content}` / `{templates}` | `Skeleton` / `Templates` | allow |
| `config_get` | `{path}` 或 `{}` | 单文件：`{content, revision, hot, editable}`；无参：manifest + 生效配置摘要 | `Manifest` / `ReadFile` | allow |
| `config_notices` | `{}` | `{deferred[], version, configVersion, watch}` | `Deferred()` / `Fingerprint()` | allow |
| `config_tier` | `{tool?}` / `{agent?}` | `{defaultTier, allow[], tools[]}` | `build.Tiers` | allow |
| `config_write` | `{path, content, baseRevision?}` | `{revision, applied, configVersion}` | `Write`（`agentkit/spec/write.go:267`） | **ask** |

### 4.1 三个要点

★ **`config_write` 是「加强」不是「包装」。** `Service.Write` 已经是「预校验（`:403` `Validate`）→ 查 baseRevision（`:417` `checkRevision`）→ 拒明文（`:443` `checkSecretFor`）→ 原子替换 → 试合并 → 失败回滚」。用文件工具改配置**做不到**这些：它无法检测「我读完之后别人又改了」，也无法保证「合并失败不留半个文件」。

★ **全树校验今天是 fail-fast 的。** `loadWithMany` 遇到第一个错误就返回，所以 `config_validate {}` 只能给「第一条阻塞错误 + 全部 deferred」。要一次给全部，得把 loader 从「返回 error」改成「累积 issues」—— **本轮不做**：单文件校验已经完整，而改配置本来就是一次一个文件。

★ **`config_get` 不会泄露密钥。** 配置里只有 `${ENV}` 或 `@file:` 引用，值在请求时才解析；读文件拿到的永远是引用。

### 4.2 档位从哪来：代码的缺省清单

五个只读查询进 `builtinAllowedTools`（`agentkit/build/tools.go:61`），与 `task`/`skill` 同一类 —— openheron 自己的工具，没有自然归属的文件。**`config_write` 不在清单里 ⇒ 恒为 ask，且任何文件都放宽不了。**

★ 这关掉了自举漏洞，而且不需要额外规则：`config_*` 这些名字靠「不在代码清单里」就自然不可放宽。少一条规则。

### 4.3 自举循环：允许

`config_write` 允许写任意放行路径，**包括 agent 文件自己**。生效时点是下一轮的 `PrepareAgent`（`agentkit/run/loop.go:25`），不影响当前轮次和正在进行的 LLM 请求。返回里带一句「下一轮生效」即可，不做额外禁止 —— 禁止的收益抵不上规则复杂度。

## 5. 阶段

| 阶段 | 内容 | 判据 |
|---|---|---|
| **P0** | 工具归属翻转：`disabled_tools` → `tools`，三张表统一「列出来的才有」，MCP 两级，删 `denyMCP`/`LayerMCPScope`，模板全显式 | `make check` 绿；`init` 产物能加载；新用例见 §5.1 |
| **P1** | `Service.Manifest()` 下沉，`api/v2_settings.go` 变薄 | 现有 e2e 全绿，零行为变化 |
| **P2** | `Service.Diagnostics`（带行号），`/v2/settings/validate` 返回 `issues[]` | 新用例：未知键 → `line: 2` |
| **P3** | 前端删规则、改防抖 | e2e 绿；手工确认打字不卡 |
| **P4** | `build/configtools.go` 六个工具 | 端到端：agent 自己改配置并 `config_validate` 通过 |
| **P5** | `initcfg` 改吃 manifest | `init` 产物逐字节不变 |
| **P6** | 文档同步 | `make lint-docs` 绿 |

P1 必须先于 P2/P3（manifest 是 Diagnostics 的输入）；P4 必须晚于 P2（工具复用 Diagnostics）。

### 5.1 P0 必须有的新用例

| 用例 | 断言 |
|---|---|
| `tools` 不写 | 零工具 |
| `tools: []` | 零工具（与不写同义） |
| `tools: [ls]` | 只有 `ls` |
| `task` 缺席 | `main` 无法委派（拓扑校验能报出来更佳） |
| `mcps` 不写 | 不挂任何服务器 |
| `mcp__*` 出现在 `tools` 且服务器没挂 | 加载期报错（矛盾） |
| `tools: [mcp__github__*]` | 只匹配 `mcp__github__*`，不匹配 `mcp__github_x__*` |
| `disabled_tools` 仍在 | 加载期报错，文案指明改名且反义 |
| `tools` 里拼错的非 MCP 名字 | 有提示 |

## 6. 风险与代价

**1. 白名单对「宽 agent」更啰嗦。** 工厂的 `main` 现在写 3 个名字（`disabled_tools`），之后要写 7 个（`tools`）。说「不要这三样」本来就比说「只要这七样」短。收益是**可审计**：读一行就知道这个 agent 能干什么，不用在脑子里做减法。

**2. `tools` 少写一个 `task`，`main` 就废了。** 失败形态是「会说话但不会干活」，比报错更难查。所以模板要逐个核准，并在测试里钉住。

**3. 前端的 250 ms 延迟**是新引入的交互成本，必须实测。

**4. 六个工具让 agent 能改自己的配置**，包括 `tools/config` 相关的想法。`config_write` 恒 ask 是这一点上的唯一正确解。

## 7. 与现有文档的关系

**P0 已落地，所以下面四篇已按本文的 §2 同步过。** P1–P6 仍是提案，它们涉及的
部分**还没进**这四篇 —— 落地一个阶段，就同步那一阶段涉及的部分，不要提前写：

- `docs/config-design.md` —— 配置系统（§6.2 的键表、§6.3 的档位模型已按 §2 更新）
- `docs/architecture.md` —— 分层与装配
- `docs/mcp-design.md` —— MCP 接入（§4 的前缀匹配已按 §2 更新）
- `docs/developer-manual.md` —— 开发手册

★ 判据很简单：一篇文档只写**代码里有的东西**。P1–P6 的接口形状还不在树上，
所以它们只出现在本文里。
