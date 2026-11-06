# MCP 接入设计 —— 运行期才定下来的工具集，与一张静态的策略表

> 状态：**§1 讲的是代码现状。§2 往后是一份提案** —— §2 往后的东西都还不在树上。
> `go.mod` 里没有 MCP 依赖，没有 `mcps/` 配置目录，也没有任何说这套协议的代码。
>
> 范围：MCP（Model Context Protocol）服务器如何变成 openheron agent 可以调用的工具集，
> 这些工具叫什么名字，以及策略层如何决定它们能不能跑。
> 不在范围内：MCP 协议本身、传输方式的实现（那属于 `mcp-go`）、
> 以及沙箱打包（见 `sandbox-kit-design.md` §2.3）。

## 1. 代码现状

### 1.1 工具从哪来

```
tools/*.yaml → spec.loadTool → decl.ToolsCfg
  → toolRegistry.Build (one Provide per provider)
  → snapshot.buildHeavy → heavyDeps.tools
  → applyToolConfig → deep.Config.ToolsConfig.Tools
  → deep.New → the eino agent
```

| 步骤 | 位置 |
|---|---|
| 加载一个 `tools/` 文件 | `agentkit/spec/load.go:141` `loadTool` |
| 声明结构体 | `agentkit/spec/decl/decl.go:288` `ToolsCfg` |
| 构造每个 provider 的那一批 | `agentkit/build/tools.go:48` `toolRegistry.Build` |
| 物化，一个快照一次 | `agentkit/build/snapshot.go:47` `buildHeavy` |
| 交给 eino | `agentkit/build/assemble.go:31` `applyToolConfig` |

★ `buildHeavy` 跑在一个 `sync.Once` 之下（`agentkit/build/snapshot.go:40-45`）。
  于是工具集在一个快照的生命周期里是**固定的** —— 它在快照换代时重建，
  而不是在一个工具的行为变化时重建。

### 1.2 provider 契约

`agentkit/build/tools.go:19` 就是全部的扩展点：

```go
type toolProvider interface {
	ID() string
	Optional() bool
	Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error)
}
```

注册表是 `defaultToolRegistry`（`agentkit/build/tools.go:29`），里面有一行点出了这份设计：

```go
		// mcpToolProvider{}, projectToolsProvider{}, ...
```

★ 那行注释是树上**唯一**的 MCP 痕迹。没有 MCP 依赖，没有 `mcps/` 目录，
  也没有任何说这套协议的代码。

### 1.3 名字就是策略

工具名是每一张决定「这个工具能干什么」的表的键：

| 表 | 位置 | 决定什么 |
|---|---|---|
| `Policy.TierOf` | `agentkit/build/policy.go:39` | `allow` / `ask` / `deny` |
| `Table.Blocked` | `agentkit/build/policy.go:94` | 被 deny（外层）/ 被 disable（内层） |
| `Table.Filter` | `agentkit/build/policy.go:107` | 把工具从 `runCtx.Tools` 里摘掉 |
| `Builder.EffectiveTools` | `agentkit/build/introspect.go:47` | API 报告什么 |

★ **上面每一条查找都是精确匹配。** `TierOf` 是 `slices.Contains` 加一次 map 查找；
  `Blocked` 是两次 map 查找。没有通配符、没有前缀、没有任何模式。

闸门本身不需要写任何权限代码 —— 它读档位然后动作：

| 构件 | 位置 |
|---|---|
| 同步那条路 | `agentkit/build/gate.go:55` `WrapInvokableToolCall` |
| 流式那条路 | `agentkit/build/gate.go:92` `WrapStreamableToolCall` |
| 两条路共用的那一个决策 | `agentkit/build/gate.go:131` `decide` |
| 整条链 | `agentkit/build/gate.go:172` `toolChain` |

### 1.4 配置分区

| 分区 | 位置 | 备注 |
|---|---|---|
| `tools/` | `agentkit/spec/config.go:62` | 一个工具一个文件，**文件名是白名单**（`agentkit/spec/load.go:141`），热更新 |
| `policy/` | `agentkit/spec/config.go:52` | `ask` / `deny` / `limits`，热更新 |
| `agents/` | `agentkit/spec/config.go:47` | frontmatter，含 `disabled_tools`，热更新 |

★ `policy/` 下**今天**有**两个**文件：`permissions.yaml`（档位）与 `limits.yaml`（数量上限）。
  `limits.yaml` 里没有工具名 —— 那些上限是按 agent 与按类别算的 ——
  所以它在这份设计里不参与。

## 2. MCP 服务器改变了什么

### 2.1 MCP 客户端占有的两样东西

MCP 服务器与 openheron 今天的每一个工具，差别恰好在两处。

| # | 是什么 | 现有代码为什么假定不是这样 |
|---|---|---|
| 1 | **一条长连接** —— 一个子进程（stdio）或一个 HTTP 会话 | `Provide` 返回一批就返回；`toolProvider` 里没有任何东西有生命周期。`buildHeavy` 一个快照跑一次（`agentkit/build/snapshot.go:40`），于是客户端必须活得比创建它的那次调用更久 |
| 2 | **运行期才定下来的工具集** —— 服务器决定它提供哪些工具 | 策略表以工具名为键，而表里的每个名字在写配置时就已经知道了 |

★ 第二条才是承重的那条。策略层是一张**以名字为键的静态表**，
  而 MCP 服务器的名字要等连接建立之后才知道。

### 2.2 这个组件：是什么，不是什么

`github.com/cloudwego/eino-ext/components/tool/mcp`（v0.0.9）是那层适配器。它的全部接口：

```go
func GetTools(ctx context.Context, conf *Config) ([]tool.BaseTool, error)
```

以及它接受的那个结构体：

```go
type Config struct {
	Cli                   client.MCPClient
	ToolNameList          []string
	ToolCallResultHandler func(ctx context.Context, name string, result *mcp.CallToolResult) (*mcp.CallToolResult, error)
	CustomHeaders         map[string]string
	Meta                  *mcp.Meta
}
```

★ **这个组件自己的 README 是过时的。** 它的 `Config` 只列了 `Cli` 与 `ToolNameList`，
  源码里有五个字段。少掉的那三个恰好是有用的：`ToolCallResultHandler` 是结果进入模型
  之前唯一能被检查或改写的地方，而 `CustomHeaders` / `Meta` 决定怎么寻址一个服务器。
  读源码，别读 README。

这个组件**不**做的事：

| 没做 | 对 openheron 的后果 |
|---|---|
| 不管客户端生命周期 —— 不拉起、不 `Start`、不 `Initialize`、不 `Close` | 连接归 openheron 管：建它、初始化它、养着它、快照换代时拆掉它 |
| 不选传输方式 | stdio / SSE / streamable HTTP 是 `mcp-go` 的 `client` 构造器；openheron 挑一个 |
| 没有配置格式 | 还没有 server 注册表的 schema —— §5 设计一个 |
| 不支持流式 | 那层包装只实现了 `Info` 与 `InvokableRun`，所以它是 `tool.InvokableTool`，永远不是 `tool.StreamableTool`。整个结果作为一块 JSON 回来 |

★ 依赖版本是兼容的。这个组件自己的 `go.mod` 钉的是
  `github.com/cloudwego/eino v0.6.0`，但它用到的 API 在 v0.9.22 里还在
  （`schema.NewParamsOneOfByJSONSchema`，`eino@v0.9.22/schema/tool.go:297`）。
  已实测：把 `components/tool/mcp v0.0.9` 与 `eino v0.9.22` 、
  `github.com/mark3labs/mcp-go v0.43.0` 一起编译通过。

### 2.3 决定这份设计的三个问题

| # | 问题 | 咬在哪里 | 由什么解决 |
|---|---|---|---|
| 1 | **服务器的工具名可能撞上保留名** —— 一个提供 `ls` 或 `grep` 的 MCP 服务器 | `agentkit/build/tools.go:92` —— `validateBatch` 返回错误，`Build` 往上抛（`agentkit/build/tools.go:66`），于是**整个 agent 起不来**。不是「这个工具被丢掉」—— 是进程没了 | §3 |
| 2 | **服务器的工具名可能对 provider 非法** —— 带服务器前缀的名字很常见，比如 `github.create_issue` | 模型 provider 的 function name 字符集不收 `.`；失败发生在调模型的时候，而不是加载的时候 | §3 |
| 3 | **运行期才有的工具集没法写进静态策略** —— 策略是以名字为键的表，而名字要等连接建立才拿到 | `agentkit/build/policy.go:39` 与 `:94` —— 只有精确匹配。每一个 MCP 工具都落进 `allow` 兜底档，而对一个能执行任意进程的工具来说，**这不是一个安全的缺省** | §4 与 §7.3 |

★ 问题 3 是「命名空间（§3）」与「住在工具旁边的档位（§7.3）」是同一个决定的两个半边、
  而不是两件方便事的原因：**命名空间让前缀才有意义** ——
  因为前缀要切得干净，前缀的边界就不能有歧义 ——
  而「按文件写的档位」是让命名空间变得可用的那半边：
  一个文件可以在不知道自己那些工具对外叫什么名字的情况下，谈它自己的工具。

## 3. 命名空间：`mcp__<server>__<tool>`

每个 MCP 工具都以带命名空间的名字暴露给模型：

```
mcp__<server>__<tool>
```

### 3.1 规则

| 段 | 谁定 | 规则 |
|---|---|---|
| `server` | **我们定** —— `<server>.yaml` 的文件名 | 必须匹配 `^[a-z0-9]+(_[a-z0-9]+)*$`；不匹配是**加载期硬错误** |
| `tool` | **服务器定** | `[A-Za-z0-9_-]` 之外的字符替换成 `_`；大小写保持原样 |

★ **server 名里不许出现连续的下划线。** 否则 `mcp__a__b__x` 读不回来 ——
  那是 server `a` 的工具 `b__x`，还是 server `a__b` 的工具 `x`？
  §4 的前缀语法正是按 `mcp__<server>__` 切的，边界有歧义就会把一条规则悄悄放大。

★ **合成名永远不被读回来。** 没有任何地方把 `mcp__github__create_issue` 拆回两半 ——
  策略层只做整名与前缀匹配。这正是工具段能保留原始大小写与字符的原因。

### 3.2 名字不合法的三种归宿

| 情况 | 规则 | 为什么不用另一种做法 |
|---|---|---|
| 合成名超过 64 个字符 | **跳过该工具 + 警告** | 截断会撞名，而且丢掉的正是区分它的那一截 |
| 同一服务器里两个工具规整后同名 | **跳过第二个 + 警告** | 否则第一个悄悄赢，模型会拿着另一个的参数去调它 |
| 其余情况 | 暴露出去 | — |

★ **跳过并警告，绝不让进程起不来。** 工具名是**远端服务器**定的，用户改不了。
  一个多出一个怪工具的服务器，应该只赔掉那个工具，不该赔掉整个 agent。
  这条线与 `tools/` 划的那条一样（§1.4）：**自己写的配置在加载期大声报错，
  别人给的内容掉了要出声。**

★ 被跳过的工具必须**看得见**：警告里要写清服务器名、原始工具名，
  以及它本来会有的合成名。「某个功能好像不见了」是这份设计拒绝出现的症状。

### 3.3 命名空间顺带把问题 1 也关上了

一个提供 `ls` 的服务器现在提供的是 `mcp__<server>__ls`，
不再与 `reservedToolNames`（`agentkit/build/tools.go:39`）相撞。
反过来也成立，而且它本身就是一条规则：

> **只有 MCP 工具可以用 `mcp__` 前缀。**

否则一个内置工具就能占了命名空间，而为 MCP 写的前缀规则会悄悄盖住它。

## 4. 某个 agent 的 `disabled_tools` 里的前缀匹配

★ 某个 agent 的 `disabled_tools` 除了精确名字，还接受一种**前缀模式**。

| 条目 | 匹配 |
|---|---|
| `execute` | 名字正好是 `execute` 的那个工具 |
| `mcp__github__*` | server 名为 `github` 的每一个工具 |
| `mcp__*` | 来自任何 server 的每一个 MCP 工具 |

★ **档位根本不在这张表里。** 它们搬进了工具自己的文件（§7.3），
  而一个文件讲的是**自己的词儿** —— 所以「这个 server 的每个工具都要先点头」
  是 `mcps/<server>.yaml` 里的 `tier: ask`，不是共享表里的一个模式。
  那种写法比模式**更强**：它覆盖服务器**还没提供**的工具。

★ **全局 `deny` 也没了**（配置设计 §6.3）。现在「不被暴露」恰好在两个地方说：
  一个文件的**白名单**，说这个文件提供什么；以及某个 agent 的 **`disabled_tools`**，
  说这个 agent 拿不到什么。收窄是唯一一条**关于命名空间**而不是关于工具的陈述 ——
  「这个 agent 不要任何 MCP 工具」—— 而前缀正是给一个命名空间起名的写法。

### 4.1 语法规则

| # | 规则 | 理由 |
|---|---|---|
| 1 | 只有**结尾**的 `*` 是前缀模式；`*` 出现在别处是**加载期硬错误** | `mcp*foo` 看着像通配符而其实不是 —— 一条读起来与实际覆盖范围不同的规则，就是一次静默的错配 |
| 2 | 单独一个 `*` 是**加载期硬错误** | `disabled_tools: ["*"]` 会把 agent 自己的工具也一起拿走；那是要写出来的一个决定，不是一个通配符 |
| 3 | `*` 前面不能为空 | 规则 2 |
| 4 | 精确条目与前缀条目**等价** —— 命中任一即把这个工具从那个 agent 那里摘掉 | 这里没有档位可选：那个工具对那个 agent 就是不在 |

### 4.2 前缀语法不新增档位

★ 档位没变，而且它们现在住在工具自己的文件里（§7.3）：

```
allow → ask (the fallback is allow)
```

★ 前缀语法只决定**一条条目是否匹配某个名字** —— 它不新增档位，也不重排任何档位。
  ★ 而且现在没有外层边界了：那条「全局 `deny` 不能被某个 agent 的 `disabled_tools`
  放松」的保证，在提案里**没有对应物**（配置设计 §6.3）。

★ **`policy/limits.yaml` 不参与。** 它装的是数量，不是工具名，匹配语义照旧。

### 4.3 前缀语法用在哪

| 位置 | 写在哪 | 含义 |
|---|---|---|
| 某个 agent 的 `disabled_tools` | `agentkit/build/topology.go:64` | 这个 agent 拿不到这些工具 |

★ 它用的是**对外的**名字（`mcp__github__delete_repo`），因为工具表查的就是它。
  按文件写的档位不是这样（§7.3）：一个文件讲自己的词儿，由 provider 去翻译。

### 4.4 代价是什么

★ 前缀规则是一件**钝器**：`disabled_tools: ["mcp__*"]` 把每一个 MCP 工具
  都从那个 agent 那里拿走。这通常正是本意，但它只有一行，而且很容易顺手写下来。
  真正值得先够的是点名单个 server 的那条规则（`mcp__github__*`）。

## 5. `mcps/<server>.yaml` 分区

### 5.1 为什么要单独一个分区

MCP 服务器按服务器一个文件来配：

```
.openheron/mcps/
└─ github.yaml
```

★ **它不是 `tools/` 里的一个文件。** `tools/` 的规则是「一个文件 = 一个工具」
  （`agentkit/spec/load.go:141` 把文件名列成白名单），而 MCP 里的实体是**服务器**：
  一个服务器有一个命令、一份参数、一个环境、一种传输和一条生命周期。
  把 N 个服务器塞进一个文件，就要在「文件内容就是配置、不带外层包装键」这个分区规则
  之下再套一层列表；而整份文件替换时，也说不清是哪个服务器变了。

配置设计 §2 的那条规则 —— *一实体一文件* —— 落在服务器上，不是落在工具上。

### 5.2 字段

| 字段 | 类型 | 缺省 | 含义 |
|---|---|---|---|
| `transport` | `string` | `stdio` | `stdio` 或 `http`；**未知值是硬错误**，不静默退回 stdio |
| `command` | `string` | — | 仅 stdio，**必填**：要执行的命令 |
| `args` | `[]string` | `[]` | 仅 stdio：它的参数 |
| `env` | `map[string]string` | `{}` | **叠加**在继承来的环境之上的变量（§5.5）；取值与其他配置一样展开 |
| `url` | `string` | — | 仅 http，**必填**：服务器地址 |
| `headers` | `map[string]string` | `{}` | 仅 http：每次请求都带的头 |
| `timeout` | `LenientDuration` | `30s` | 连接 + 初始化 + 取工具清单的上限 |
| `call_timeout` | `LenientDuration` | `60s` | **一次工具调用**的上限（§6.5） |
| `tools` | `[]string` | `[]`→全部 | ★ **暴露集合** —— 这个 server 要提供的那些**原始**工具名（不是带命名空间的那个）；不写 = 全部 |
| `tier` | `string` | `ask` | 这个 server 提供的每一个工具的档位 —— ★ 包括那些它**还没提供**的工具；`allow` \| `ask` |
| `ask` | `[]string` | `[]` | 这个 server **自己的**工具名里，哪些要先点头 |

★ **`command` 在沙箱的 PATH 里解析，绝不相对项目根。** 工作区不许决定跑什么：
  一个相对项目根解析的命令，等于让一次 checkout 决定 agent 跑哪个可执行文件，
  还带着 agent 的权限。⇒ 可执行文件来自 **Kit** —— mixin 的安装钩子把它放进去（§8）——
  不在 Kit 里的话，就由用户自己装。

★ 所以两边各说各的，从各自的角度描述同一个 server：**mixin** 说装什么，
  **配置**说怎么调用它（`command` 加 `args`）。配置里的 `command`
  点名的正是 Kit 铺下的那个东西。

★ 这几个键在 `tools/` 与 `mcps/` 的每一个文件里都是同一个意思，
  而且它们是用**那个文件自己的词儿**写的：这里的 `ask: [create_issue]` 是
  server 自己的名字，不是合成出来的那个名字。provider 会把它翻译成模型看到的
  那个名字（§7.3）。

★ **把缺省再写一遍的那一行，什么也没说。** 文件系统那组的缺省档位是 `allow`，
  所以 `tools/filesystem.yaml` 只需要它的 `ask:` 清单 —— 模板只在**缺省不是代码的缺省**
  时才写档位（MCP server 的 `tier: ask`，那一行同时盖住了服务器还没提供的那些工具）。
  见 `docs/config-design.md` §6.6。

★ **文件里没有 `deny`，全局的也没有**（§4）。「不被暴露」只说两处：这个文件的
  **清单** —— 不在 `tools:` 里的工具不被提供 —— 与某个 agent 的 **`disabled_tools`**。

★ `timeout` 是**宽松**时长：写错就退回默认并记进 `deferred`，
  与 `debounce`、`start_timeout` 完全一样。写错 timeout 改变的是「等多久」，
  不是「会发生什么」—— 它不该让进程起不来。

★ `tools: []` 的意思是**全部**，不是**一个都没有**。
  一个表示「什么也没有」的空白名单会与「键缺失」分不出来，
  而这个分区的开关就是文件本身在不在（§5.3）。

### 5.3 文件在不在就是开关

没有 `enabled` 字段。服务器被配置，是因为它的文件存在；删掉文件就是删掉这个服务器。
这条规则 `tools/` 已经在用，它让开关只待在一个地方，
而不是一半在「文件在不在」、另一半在「文件里那个标志说开不开」。

★ server 名就是文件名去掉 `.yaml`，它就是命名空间的那一段（§3.1）。
  不匹配 `^[a-z0-9]+(_[a-z0-9]+)*$` 的文件名是**加载期硬错误** ——
  是我们自己写的，我们自己修。

### 5.4 这个分区是热的

`mcps/` 与 `tools/`、`policy/` 一起进热更新：改一个服务器的命令或超时，
下一轮就该生效，不必重启。

★ 这意味着客户端生命周期要扛得住一次重载：快照换代时，
  stdio 的子进程被拆掉、新的被拉起来。
  一个资源无法重建的热分区，就是一个谎称自己是热的分区。

### 5.5 子进程的环境

★ **子进程的环境 = openheron 自己的环境，被配置里的 `env:` 覆盖同名项。**

子进程是本进程的子进程，所以它从本进程的环境起步 —— 包括沙箱运行时注入到容器里的
那些东西。

★ **正是这份继承让凭据路径不需要任何配置**（§8.1）：运行时把**哨兵**放进容器里的那个
  变量，MCP server 从那儿读到它。若要配置把每个变量都点名，用户就得把凭据的变量名写下来
  —— 而那是 **MCP server 的事**，不是 openheron 的。

`env:` 这张表是给 MCP server **除此之外**需要的变量用的：一个日志级别、一个特性开关、
第二个它按自己名字找的令牌。取值与其他配置一样展开（`${ENV}` / `@file:`），
而 `${ENV}` 未设**不致命** —— 与其他缺失值一样记进 `deferred`。

★ **继承不是新增的暴露面。** agent 自己的工具读的是同一批东西：`read_file` 读项目，
  `execute` 在这个环境里跑命令。一个看得见环境的 MCP server，看不见任何 agent 看不见的
  东西。区别只在于 `execute` 出厂在 `ask` 档，而 MCP 工具未必是。
  ⇒ 若这个不对称对你重要，就把 `mcp__*` 放进 `ask` 档（§4.4）。

★ 配置里的 `env:` **不替换**继承来的环境，而是**叠在它上面**。
  `env:` 为空的配置，产出的子进程仍然有一个完整的环境。

### 5.6 配置模板

★ **模板里的注释是英文**，因为模板是写进用户配置里的内容（`AGENTS.md`）。

#### `mcps/<server>.yaml` —— 一个走 stdio 的服务器

```yaml
# .openheron/mcps/github.yaml
#
# ★ One MCP server, one file. The file's name is the server's name, and it is
#   the namespace segment of its tools: mcp__github__<tool>.
#
# ★ The tier lives here, next to the server it is about, and it is written in
#   the server's OWN vocabulary — "create_issue", not "mcp__github__create_issue".
transport: stdio
command: npx
args: ["-y", "@modelcontextprotocol/server-github"]
env:
  # ★ Expanded at load time. In a sandbox the variable holds a sentinel and the
  #   host proxy swaps in the real key on the way out (§8.1).
  GITHUB_TOKEN: ${GITHUB_TOKEN}

# ★ The tier of every tool this server offers — including the ones it has not
#   offered yet. Default: ask.
tier: allow

# ★ The ones that ask first, on top of the tier above.
ask: [create_issue]

# ★ The tools to offer, by the server's own names. Absent = all of them.
tools: [create_issue, list_issues, search_issues]

# ★ To take one tool out, leave it out of the list above. The whitelist is
#   how a file says what it offers.
```

#### `mcps/<server>.yaml` —— 一个走 http 的服务器

```yaml
# .openheron/mcps/files.yaml
transport: http
url: https://mcp.example.com/mcp
headers:
  Authorization: Bearer ${FILES_TOKEN}
timeout: 30s
call_timeout: 60s
```

★ **宿主**持有那把 key、由代理替换它，所以容器永远看不到它 —— 但宿主上的一个 http
  服务器仍然可能需要沙箱向*它*做认证，而那就是 `headers:` 这张表（§8.1）。

#### `tools/filesystem.yaml`

```yaml
# .openheron/tools/filesystem.yaml
#
# ★ The filesystem tool set. These tools are always there — this file tunes them.
#
# ★ The default tier here is allow, so only the ones that ask are named. Writing
#   "tier: allow" would say nothing.
ask: [write_file, edit_file, execute]
# tools: [ls, read_file, glob, grep]   # uncomment for a read-only agent
```

#### `tools/kb.yaml`

```yaml
# .openheron/tools/kb.yaml
corpus: ""
index: default
model: ""
query_instruction: ""
batch_size: 10
max_batch_chars: 1200
top_k: 8
# ★ kb_index writes the index on disk, so it asks. kb_search does not.
ask: [kb_index]
```

#### `tools/http.yaml`

```yaml
# .openheron/tools/http.yaml
methods: [get]
headers: {}
timeout: 30s
# ★ The write methods change the outside world, so they ask.
ask: [post, put, delete]
```

#### ★ 谁写这些文件

| 谁来写 | 写什么 |
|---|---|
| 用户 | 手写 `mcps/<server>.yaml` 这些文件 |
| `openheron init` | ★ 一个**示例** —— 而示例不能是一份活的 `.yaml`，因为文件在不在就是开关（§5.3），而一份活文件会去启动一个没装的 server。⇒ `mcps/example.yaml.disabled`：形状在那儿，名字说明它还不是一份配置，改名就是启用它 |
| 配置编辑器 | ★ 同一份模板，走它的「新建文件」对话框 —— 编辑器**能写** `mcps/` |

## 6. 装配路径

### 6.1 provider

provider 照 §1.2 的契约写，注册在占位注释那一行（`agentkit/build/tools.go:35`）：

| 构件 | 做什么 |
|---|---|
| `mcpToolProvider.ID` | 日志与错误里的 provider 名 |
| `mcpToolProvider.Optional` | **true** —— 一个连不上的服务器不该把 agent 拖住 |
| `mcpToolProvider.Provide` | 连上每个配置好的服务器，取工具清单，给名字加命名空间并规整，返回那一批 |

★ `Optional() == true` 是有代价的，`developer-manual.md` §3.1 第 3 步说得很直白：
  provider 那条 `Warn` 是**唯一**的通知 —— 工具不进表，
  用户看到的是「某个功能好像不见了」。
  对一个 MCP 服务器来说，那恰好是最不该留下的症状，
  所以警告里必须写清服务器名、传输方式与错误。

★ **够不着的服务器是一条警告，不是 API 里的一行。** API **不**暴露那些配了
  却够不着的服务器。用户从警告、从缺失的那些工具就知道；那样一个端点会成为
  要跟第一处同步的第二份真相，而它回答的问题警告已经回答过了。

### 6.2 客户端生命周期

组件不管它（§2.2），所以由**管理器**管（§6.6）—— provider 向它要工具，然后拿到：

| 步骤 | stdio | http |
|---|---|---|
| 建立 | `client.NewStdioMCPClient` | `client.NewStreamableHttpClient` / `client.NewSSEMCPClient` |
| 启动 | 子进程被拉起 | HTTP 会话打开 |
| 初始化 | 用协议版本与客户端信息 `Initialize` | 同样 |
| 取工具清单 | `GetTools` → `ListTools` | 同样 |
| 拆掉 | 快照换代时子进程被杀掉 | 会话关闭 |

★ 初始化的结果**可以丢掉**：provider 只要工具清单。
  初始化失败与「服务器连不上」是同一种失败 —— 这个服务器不贡献工具，并且说出来。

### 6.3 工具批次

服务器提供的每个工具变成一个 eino 工具：

| 属性 | 值 |
|---|---|
| 名字 | `mcp__<server>__<tool>`（§3） |
| 描述 | 服务器给的描述 |
| 参数 | 服务器的 JSON Schema，用 `schema.NewParamsOneOfByJSONSchema` 转换 |
| 种类 | `tool.InvokableTool` —— 组件只实现了 `InvokableRun` |

★ 这个种类只在一处要紧：闸门的同步那条路（`agentkit/build/gate.go:55`）罩得住它，
  流式那条路（`agentkit/build/gate.go:92`）不需要。
  **文件系统工具曾经有的那个流式缺口在这里不存在** ——
  但那只是因为这个组件从不产出流式工具。

### 6.4 输出封顶

`tools/` 把每个工具的输出封在 20000 字节（`agentkit/build/nettools.go:26`，
`cappedTool` 在 `:98`，`capTool` 在 `:115`）。
这个封顶存在的原因是工具的输出**原样进模型上下文**，
而一个 10MB 的页面会把这一轮撑爆 —— 表现是「模型突然开始胡说」，不是报错。

★ MCP 工具的输出没有不同：它也是别人的文本，未经滤就进了上下文。
  封顶对 MCP 工具同样适用，而实现它的位置就是组件的 `ToolCallResultHandler` ——
  那个组件 README 从没提过的钩子（§2.2）。

### 6.5 一个 server 一个 client，且调用串行化

★ **一个 server 一个 client。** eino-ext 那个组件包的是单个 `client.MCPClient`，
  该 server 的所有工具共用它。一个 server 就是一条连接 —— 对 stdio 来说，是一个子进程。

★ **同一个 stdio server 的调用被串行化。** 这是**正确性**决定，不是性能决定。
  `mcp-go` 的 stdio 传输把请求直接写进子进程的 stdin：

```go
// mcp-go@v0.43.0, client/transport/stdio.go — SendRequest
c.mu.Lock(); c.responses[idKey] = responseChan; c.mu.Unlock()
...
if _, err := c.stdin.Write(requestBytes); err != nil {   // ← no lock
```

`c.stdin` 是 `cmd.StdinPipe()` 给的管道。POSIX 只保证不超过 `PIPE_BUF`
（Linux 上 4096 字节）的管道写是原子的 —— 更大的 JSON-RPC 行会**与另一次调用的行交错**，
服务器读到的是一个坏掉的帧。

★ 而 openheron 确实会并发调工具：两个会话同时跑，agent 实例也可并发复用
  （`session-concurrency-design.md` §2.5）。两个会话打同一个 MCP server，
  就是对同一条管道的两次并发写。

⇒ **provider 给同一 server 的 stdio 工具包一把 per-server 的互斥锁**，
  而闸门与封顶看到的正是这个包装层 —— 与 `nettools.go` 里 `cappedTool` 同一个形状。
  这把锁是**等**，不是让第二次调用失败。代价是同一 server 内的队头阻塞，
  而这是两种代价里较便宜的一种：另一种是每次调用开一个子进程。

★ **http server 不串行化。** 每个请求彼此独立，而一个等慢 API 的 MCP 工具
  不该把同 server 的其它工具一起堵住。串行化是**传输方式**的属性，不是 MCP 的属性。

★ **调用有截止时间。** 没有截止时间的互斥锁就是一条把轮次挂死的路：
  一个永不回答的 MCP server 会永远占着锁，同 server 的其它调用全排在它后面。
  ⇒ `call_timeout`（LenientDuration，缺省 `60s`）给一次工具调用封顶。
  它是宽松的，与其他只决定「等多久」的时长一样。

### 6.6 mcps 管理器

★ **客户端归一个管理者拥有**，不归 provider。provider 回答的是「这个快照有哪些工具」；
  而客户端活得过 provider，靠的就是管理者。

| 职责 | 意思 |
|---|---|
| **一个 server 一个客户端** | 一个 server 就是一条连接，对 stdio 来说还是一个子进程 |
| **懒启动** | 一个 server 在第一轮要用它时才被拉起来，不是在启动时 |
| **崩了的子进程自动重启** | 一个死掉的 stdio server 在下一次调用时被重新拉起 —— 崩了不是配置错误，而一个死掉的 server 就那么死着才是最糟的结果 |
| **发现工具清单变了** | 管理者重新列举工具；清单变了就**推进代际**、工具重建 —— 与配置重载是同一条路（§6.7） |
| **快照换代时拆掉旧客户端** | 快照被替换时，旧一代的客户端被关掉，它们的子进程被杀掉（§5.4） |

★ **工具清单是管理者的活，不是快照的。** server 的工具是运行期才发现的，
  所以一份把它们冻住的快照，要看得见 server 昨天加的那个工具，就得重启。
  ⇒ 管理者去轮询，而一次变化重新走配置变化走的那条路。

### 6.7 从一个文件到一次工具调用

整条路径，从磁盘上那个文件一路走到闸门的那一个决定：

```
.openheron/mcps/github.yaml
  │  spec.loadMCP (agentkit/spec/load.go) — the file name and the fields are
  │  validated here, and a bad one is a startup error
  ▼
decl.MCPServerCfg — transport / command / args / env / tier / ask / tools / timeouts
  │
  │  snapshot.buildHeavy (agentkit/build/snapshot.go)
  │    → mcpToolProvider.Provide (agentkit/build/mcptools.go)
  ▼
the mcps manager (agentkit/build/mcpmanager.go):
  per server → client.NewStdioMCPClient / client.NewStreamableHttpClient
            → cli.Start(ctx) → cli.Initialize(ctx) → mcpp.GetTools(ctx, &mcpp.Config{Cli: cli})
  │
  │  each tool:  name = mcp__<server>__<tool>     (sanitized; a bad name is skipped
  │                                                with a warning — §3.2)
  │              tier = the file's tier and ask list, read in the SERVER's own
  │                    names and mapped to the exposed ones
  ▼
[]tool.BaseTool — the stdio ones wrapped in the per-server mutex (§6.5)
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

★ **档位的解析**是两步查找，而闸门从不去问一个工具来自哪个文件 ——
  装配时填进表里的就是**对外的**名字：

```
Policy.TierOf(name):
  1. the file that provides the tool:
       its per-item ask list, in its own names       → ask
       its tier (allow | ask)                        → that tier
  2. otherwise                                       → allow
```

★ **装配发生在 `agentkit/build`，不在 `agentkit/spec`**（§7.3）：只有 provider 知道
  自己文件的词儿，以及它背后的对外名字；而 `agentkit/spec` 不得 import `kb`
  （那条铁律），也不得 import eino（文件系统工具的名字住在那里）。

★ **重载是这条流程的一部分。** `mcps/` 是热分区，所以改动之后，上面整条链在下一轮
  重跑一遍：加载器重新读文件，快照换代，stdio 的子进程被拆掉、新的被拉起来，
  而档位表用对外的名字重建。闸门从下一轮起读到新档位 —— 不用重启，
  也不会出现「一个工具还揣着旧档位」的空窗期。

★ **而一个自己改了工具清单的 server 走的是同一条路。** 管理者轮询那份清单（§6.6）；
  清单变了就推进代际、工具重建 —— 与配置变化走同一条路，同样不需要重启。

## 7. 闸门与档位

### 7.1 档位照旧适用

一个叫 `mcp__github__create_issue` 的工具被查起来与其它工具一模一样：

```
Policy.TierOf("mcp__github__create_issue")
```

当 server 的文件写着 `tier: ask`（§7.3）时它落进 `ask` 档，当它写着 `tier: allow`
时它落进 `allow` 档。闸门代码一个字都不用改：它读档位，然后动作（§1.3）。

★ **这正是命名空间的意义。** 闸门、各张表、API 的 `EffectiveTools` 视图
  与每个 agent 的收窄，全都在名字上工作。
  一个带命名空间的名字，是它们本来就懂的名字 ——
  策略层、API 与前端都不需要接一个新概念。

### 7.2 stdio 的权限问题

★ stdio 之下，MCP 服务器是 openheron 的**子进程**：它能写文件、能联网，
  能读到 openheron 读得到的一切。
  配置设计 §6.6 给 HTTP 工具定的「只连公网」**在这里不适用** ——
  那条限制的是*工具*能碰到什么，不是*服务器*能做什么。

这是命名空间与「给每个 server 自己的档位」合起来的最强理由：
它们是「在有人把成员列出来之前，先给一类外部工具定档」的办法。
`mcps/<server>.yaml` 里的 `tier: ask` 是一行字，
意思是「这个 server 的每个工具都要先点头」（§7.3）。

### 7.3 档位住在工具旁边

★ **一个工具的档位写在它自己的文件里，用那个文件自己的词儿。**
现在**没有全局清单**了（§4）：`ask` 档搬到了它所说的那个工具旁边，
全局 `deny` 也跟着走了 —— 「不被暴露」恰好在两个地方说：
一个文件的清单，以及某个 agent 的 `disabled_tools`：

```yaml
# .openheron/mcps/github.yaml
tier: ask                       # every tool of this server asks first
```

★ **为什么要用文件自己的词儿。** 档位是关于**一个工具**的陈述，
  所以它属于那个工具的文件 —— 而在那里它可以用那个文件已经在用的名字。
  原来那个中心文件必须点名内部工具名（`request_post`），而工具自己的文件说的是 `post`；
  ★ 一个工具两套词儿，就是一处等着发生的错配，而一个拼错的名字
  在工具的行为与文件所说的不一致之前，一直是看不见的。

| 位置 | 词儿 | 决定什么 |
|---|---|---|
| 某个文件的 `tools` / `tier` / `ask` | **那个文件自己的**名字（`create_issue`） | 这个文件提供什么，以及那些的档位 |
| 某个 agent 的 `disabled_tools` | 对外的名字 | 这个 agent 拿不到这些工具 |

★ **顺序**：先提供这个工具的文件，然后是 agent 的收窄 ——
  收窄只把这个工具从那个 agent 那里摘掉，别的一概不管。

★ **文件自己没有 `deny`，而这是刻意的。** 「不被暴露」本来就是清单的活。
  ★ 而且**没有全局 `deny` 来说「永不」** —— 上面那两层不是彼此重复：
  文件说**这个文件提供什么**，agent 说**这个 agent 拿不到什么**。

★ **装配出来的策略形状不变。** `build.Policy` 与 `TierOf`
（`agentkit/build/policy.go:39`）仍然是收一个名字、答一个档位；变的是条目的来源：
  provider 各自从自己文件里的声明、用它自己的词儿装配出来。⇒ 闸门、各张表、
  API 的 `EffectiveTools` 视图与前端都不必学一个新概念。

★ **装配发生在 `agentkit/build`，不在 `agentkit/spec`。** 只有 provider 知道
  自己文件的词儿，以及它背后的对外名字；而 `agentkit/spec` 不得 import `kb`
  （那条铁律），也不得 import eino（文件系统工具的名字住在那里）。

★ **一个 server 的缺省档位是 `ask`。** 内置那些组有**已知**的缺省 ——
  openheron 知道 `write_file` 会写文件、`execute` 会跑命令，所以它知道哪些该问。
  而一个 server 的工具是**运行期才发现**的：openheron 无从知道 `delete_repo` 做什么，
  而在 stdio 之下，那个 server 是带着 agent 自己权限的**子进程**（§7.2）。
  ⇒ 猜错的代价是不对称的 —— 多一次点击，换掉的是一次没人看见的破坏性调用。
  用户信得过的 server 说**一次**就够了：

```yaml
tier: allow
```

★ **档位改动下一轮就生效。** `mcps/` 是**热**分区，所以档位并不特殊：
  策略表随快照一起重建，闸门从下一轮起读到新档位。重载唯一要顾对的是
  **客户端**：stdio 的子进程被拆掉、新的被拉起来（§5.4）。

★ **文件系统工具也有自己的文件** —— `tools/filesystem.yaml` —— 这样它们的档位
  写在它们旁边，这个集合也可以被收窄。见 `docs/config-design.md` §6.6。

### 7.4 run ledger 说什么

★ 一次 MCP 工具调用就是一次普通工具调用，所以它本来就跟别的调用一样进 transcript、
  进事件流。**ledger** 多出来的是 server：一个 run 的行记下这次 run 碰过哪些 MCP server，
  于是事后复盘能回答「这是哪个 server、是不是它把这次 run 弄失败的」，
  而不必读完整个 transcript。

★ ledger 仍然是 **run** 的账本：它装的是这个 run 的状态迁移，不是一次调用一行。
  那一次次的调用在 transcript 里；那一行说的是哪些 server 卷了进来。

## 8. 沙箱 Kit

Kit（`sandbox-kit-design.md`）把 openheron 进程打包成一个 v3 workload。
MCP 落进它有两种方式，而且这两者并不对称。

| 传输 | 服务器跑在哪 | Kit 需要什么 |
|---|---|---|
| stdio | **沙箱内**，作为 openheron 进程的子进程 | 服务器的二进制或它的包必须**在 workload 镜像里** —— 它是 workload 所跑内容的一部分 |
| http | **宿主上** | 一条让沙箱够得到宿主端口的 mixin 网络规则，以及一个够得着的宿主侧服务 |

★ mixin 模型在这里把界线划得很干净：mixin 是**声明**
  （一份凭据与它的网络规则），workload 是**内容**。
  跑在沙箱里的 MCP 服务器是 workload 内容；
  跑在宿主上、通过 http 够到的 MCP 服务器是 mixin 的事 ——
  要变的是它的网络规则。

★ 还有第三种选择，**故意不**在这份设计里：一种**安装**宿主侧 MCP 服务器的 mixin。
  那会让 mixin 带上内容，而内容属于 workload —— 这是 `sandbox-kit-design.md` §2.3
  划的那条边界。

### 8.1 凭据路径

需要授权 key 的 MCP 服务器与不需要的，是两个不同的问题 —— 而 Kit 已有的机制
答得出大部分。

#### 机制

Kit 已经能在**不让密钥进沙箱**的前提下注入**模型**凭据：mixin 声明一个
`proxyManaged: true` 的 `credential@1`，运行时把哨兵 `proxy-managed` 放进容器的变量，
宿主持真值，而**宿主侧的代理**在出站时把它换掉。

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
  而 §5.5 把环境透传下去 —— 于是 MCP server 按它自己的约定就找到了那个变量。
  凭据的变量名是 **MCP server 的事**，openheron 不需要知道。

★ **http 传输走的是同一条路。** `headers:` 这张表与其他一切一样展开，
  所以它能引用一个环境变量：

```yaml
headers:
  Authorization: Bearer ${MCP_TOKEN}
```

★ 在沙箱里那个变量装的是**哨兵**，宿主代理在出站时把真令牌换上去 ——
  同一套机制、同一个 mixin 形状、同样是 mixin 的 `inject` 规则所声明的
  `Authorization: Bearer <access_token>` 那种形式。⇒ 凭据路径只有**一条**，
  传输方式不改它。

#### mixin 要声明什么

| 能力 | 装什么 |
|---|---|
| `network-policy@1` | 上游域名 —— 运行时会检查每个注入域名都**字面**出现在同一阶段的允许列表里，所以凭据的域名必须被点名 |
| `credential@1` | 凭据本身：`service`、`apiKey.name`、`proxyManaged: true`，以及 `inject` 规则（域名、头名、格式） |
| `lifecycle@1` | 可选：一个把服务器二进制装进沙箱的安装钩子 |

★ **mixin 的粒度是上游，不是 MCP 服务器。** 一份凭据在（service, 阶段）上只有一个属主，
  而环境是注入到**容器**的 —— 所以一份凭据声明就服务沙箱里所有读那个变量的服务器。
  两个都跟 GitHub 打交道的 MCP 服务器，需要**一份**凭据 mixin 与**一次**绑定，不是两份。

★ **mixin 可以把 MCP 服务器装进沙箱。** 服务器的二进制得在沙箱里，而 workload 镜像是
  openheron 自己的 —— 它装不下每一个 MCP 服务器。mixin 把这件事声明成一个
  `lifecycle@1` 的 **install** 钩子（哪个 registry、哪个包），它的网络规则放行那个
  registry。mixin 本身仍然不带内容：字节是运行时去取的，不是 mixin 揣着的。

★ 这与上面那段拒绝的选项不是一回事。那一个是把**宿主侧**服务器装起来，那会让 mixin
  定义一个服务；这一个是从 mixin 点名的 registry 把**客侧**二进制取下来 ——
  一句「取什么」的声明，由运行时去执行。

#### 机制够不着的时候

| 情况 | 会怎样 |
|---|---|
| key 放在头里 | ★ 机制覆盖它 —— `inject` 说明是哪个头、什么格式 |
| key 放在 URL query、cookie 或非 HTTP 协议里 | ★ **表达不了**：`inject` 是头注入。真值只能进沙箱（`sbx run --env …`），此后它就是 agent 要保护的东西 |
| 根本没有 mixin | 那个变量就是没设，服务器自己的错误（401）作为工具结果到达模型 |

★ **兜底那一条是真的降级，而且应该说出来。** 落在 VM 里的 key 能被 agent 自己的工具读到，
  而这是用户自己选的。

## 9. 核心代码架构

### 9.1 那些包

| 包 | 装什么 | 装不得什么 |
|---|---|---|
| `agentkit/spec/decl` | **声明**：`MCPServerCfg` 与命名空间常量 | 任何行为 —— 它是纯声明层（`AGENTS.md` 的依赖铁律） |
| `agentkit/spec` | **加载器**：读 `mcps/<server>.yaml`，校验文件名与字段，展开 `${ENV}` / `@file:` | MCP 客户端、eino、`kb` —— 它不得 import 它们 |
| `agentkit/build/mcptools.go` | **provider** 与**包装层**：命名、档位映射、per-server 互斥锁、输出封顶 | 加载器的文件遍历 —— 它拿到的是声明 |
| `agentkit/build/mcpmanager.go` | **管理者**：一个 server 一个客户端、懒启动、崩掉的子进程重启、工具清单轮询、快照换代时的拆解 | 命名与档位 —— 那属于 provider |
| `agentkit/build/tools.go` | **注册表**：`toolProvider`、`defaultToolRegistry`、保留名 | MCP 特有的东西 —— 它只认那个接口（§1.2） |
| `agentkit/build/policy.go` | **档位查找**：`Policy.TierOf`、`Table.Blocked` | 一个工具来自哪个文件 —— 它只看得到名字 |

★ **方向是单向的**：`spec` → `build`。声明层从不 import 装配层，
  而装配层也从不自己去遍历配置目录。

### 9.2 那些类型

| 类型 | 在哪 | 是什么 |
|---|---|---|
| `decl.MCPServerCfg` | `agentkit/spec/decl/decl.go` | 一个服务器的声明：传输方式、命令、环境、那几个档位键、白名单、两个超时 |
| `decl.MCPFiles` | `agentkit/spec/decl/decl.go` | 这个分区的持有者：`map[string]*MCPServerCfg`，按服务器名做键 |
| `spec.Config.MCPs` | `agentkit/spec/config.go` | 加载器把它们放在这儿 |
| `mcpToolProvider` | `agentkit/build/mcptools.go` | `toolProvider` 的那个实现（§1.2） |
| `mcpManager` | `agentkit/build/mcpmanager.go` | 客户端的属主：每个 server 一个，按服务器名做键，跨快照活着（§6.6） |
| `mcpTool` | `agentkit/build/mcptools.go` | 一个工具：eino 工具加上它归属的那个服务器 |
| `serialTool` | `agentkit/build/mcptools.go` | 把同一个 stdio 服务器的调用串行化起来的那个包装层（§6.5） |
| `build.Policy` | `agentkit/build/policy.go` | 装配出来的表：名字 → 档位。**形状不变** —— 变的只是条目从哪儿来（§7.3） |

### 9.3 设计的哪一块落在哪一行代码

| 设计 | 代码 |
|---|---|
| 命名空间（§3） | `mcpToolProvider.Provide` —— 唯一一处拼出 `mcp__<server>__<tool>` 的地方 |
| 跳过并警告（§3.2） | 同一个函数 —— 名字在工具被留下之前先检查一遍 |
| 文件自己的档位（§7.3） | 同一个函数 —— provider 把文件里的名字映射到对外的名字 |
| 串行化（§6.5） | `serialTool` —— 一个包装层，与 `nettools.go` 里的 `cappedTool` 同一个形状 |
| 输出封顶（§6.4） | 同一族包装层 —— 组件的 `ToolCallResultHandler` |
| 客户端的生命（§6.2、§6.6） | 管理者 —— `Start` / `Initialize` / `GetTools`，懒启动、崩了的子进程重启，以及快照换代时的拆解 |
| 热重载（§5.4） | `agentkit/spec/reload.go` —— 这个分区是热的，而快照换代会重建工具 |
| 工具清单变了（§6.6） | 管理者 —— 轮询，以及推进代际并重建工具 |
| 一次 run 碰过的 server（§7.4） | `agentkit/run/runrow.go` —— run 的那一行点名它们 |
| 示例与编辑器的模板（§5.6） | `agentkit/spec/write.go`（`spec.Template`）与 `initcfg/templates/` |

★ **一个函数干三件事**：命名空间、跳过并警告、档位映射都住在
  `mcpToolProvider.Provide` 里，因为它们要的都是同样两样输入 ——
  服务器自己的工具清单，与描述它的那个文件。拆开它们就意味着
  把同样这两样东西到处传。

## 10. 改动清单

下面这些都还不在树上。这是一轮后来的实现要照着走过的清单。

### 10.1 代码

| # | 文件 | 改动 |
|---|---|---|
| 1 | `go.mod` | 加 `github.com/cloudwego/eino-ext/components/tool/mcp` 与 `github.com/mark3labs/mcp-go` |
| 2 | `agentkit/spec/config.go` | `DirMCP = "mcps"` 与一条 `Categories` 条目，热更新（`hotDirList` 在 `:77` 会自动带上它） |
| 3 | `agentkit/spec/decl/decl.go` | `MCPServerCfg` 声明结构体、`MCPFiles` 持有者、命名空间常量；`call_timeout` |
| 4 | `agentkit/spec/load.go` | `mcps/<server>.yaml` 的加载器，带 §3.1 与 §5.2 的文件名与字段校验 |
| 5 | `agentkit/spec/defaults.go` | `EnabledToolNames` 加上带命名空间的 MCP 工具；一个服务器的缺省档位（`ask`） |
| 6 | `agentkit/spec/write.go` | 新建 `mcps/` 文件的脚手架 |
| 7 | `agentkit/build/tools.go` | `mcpToolProvider`，注册在占位注释那一行 `:35` |
| 8 | `agentkit/build/policy.go` | `Blocked` 的前缀匹配器，**以及**档位的**装配**：各 provider 从自己文件里的声明贡献（对外名字 → 档位）（§7.3） |
| 9 | `api/v2_settings.go` | `mcps/` 的可编辑分区 |
| 10 | `agentkit/build/nettools.go` | 输出封顶，套到 MCP 批次上 |
| 11 | `agentkit/build/mcptools.go` | provider 的命名与档位映射（§6.3、§7.3），把 stdio 调用串行化的 per-server 互斥锁（§6.5），以及子进程的环境装配（§5.5） |
| 12 | `agentkit/build/mcpmanager.go` | 管理者：一个 server 一个客户端、懒启动、崩了的子进程重启、推进代际的工具清单轮询，以及快照换代时的拆解（§6.6） |
| 13 | `agentkit/run/runrow.go` | run 的那一行记下这次 run 碰过哪些 MCP server（§7.4） |
| 14 | `initcfg/templates/` 与 `spec.Template` | `mcps/example.yaml.disabled` 这个示例，以及新建 `mcps/` 文件时编辑器用的模板 —— 编辑器的清单是按类别填的，所以新类别白拿一份脚手架（§5.6） |

### 10.2 测试

| 领域 | 要证明什么 |
|---|---|
| 命名 | 一个提供 `ls` 的服务器产出 `mcp__<server>__ls`，且 agent 仍能启动 |
| 命名 | 64 字符的边界，以及越界之后的「跳过 + 警告」这条路 |
| 命名 | 同一服务器里两个规整后同名的工具 —— 第二个被跳过，且警告里说了 |
| 匹配 | 一条前缀规则命中一个 server 的工具，且只命中它的 |
| 匹配 | `mcp*foo` 与单独的 `*` 是加载期错误 |
| 匹配 | 某个 agent 的 `disabled_tools` 只为那个 agent 摘掉这个工具 |
| provider | 一个够不着的服务器丢掉它的工具、警告里带服务器名，其它 provider 的工具不受影响 |
| 生命周期 | 一次热重载换掉客户端，旧的 stdio 子进程没了 |
| 并发 | 同一个 **stdio** server 的两次并发调用被串行化 —— 第二次等第一次，且两者的请求都没有写坏 |
| 并发 | 同一个 **http** server 的两次并发调用**不**被串行化 |
| 并发 | 超过 `call_timeout` 的调用会返回，且那把锁被释放 |
| 环境 | 子进程看得见 openheron 的环境，`env:` 覆盖同名项 |
| 环境 | `env:` 里未设的 `${ENV}` 进 `deferred`，不致命 |
| 凭据 | `credential@1` 的哨兵经继承的环境到达子进程，而配置里没有任何字段提到它 |
| 档位 | 一个 server 的 `tier: ask` 覆盖它**还没提供**的工具 |
| 档位 | 文件里的 `tier` 压过 allow 兜底，仅此而已 |
| 管理者 | 崩掉的 stdio 子进程在下一次调用时被重新拉起 |
| 管理者 | 工具清单变了会推进代际并重建工具，且不重启 |
| 管理者 | 快照换代会关掉旧一代的客户端并杀掉它们的子进程 |
| 命令 | 一个不在 PATH 上的 `command` 只是一条警告，而工作区永远不参与解析 |
| 账本 | 一个 run 的那一行点名它碰过的 MCP server |
| 编辑器 | 配置编辑器能在 `mcps/` 下面新建文件 |
| 档位 | 文件里的 `ask:` 用 server 自己的词儿写，到闸门时用的是合成后的名字 |

### 10.3 文档

| 文档 | 要改什么 |
|---|---|
| `docs/developer-manual.md` | 「加一个工具」的配方加上 MCP provider；「加一个分区」的配方加上 `mcps/` 这一例 |
| `docs/user-manual.md` | 怎么配一个服务器，以及界面上看到的名字长什么样 |
| `docs/architecture.md` | 工具装配路径加上 MCP provider |

## 11. 这份设计不管什么

| 没覆盖 | 为什么 |
|---|---|
| MCP 协议与各种传输 | 它们属于 `mcp-go`；openheron 只挑一个客户端并配它 |
| 带发现、健康检查与重试的 server 注册表 | 一个服务器一个文件，够起步了；注册表是下一轮的事 |
| MCP 的 resources、prompts 与 sampling | eino-ext 那个组件**只**暴露工具 |
| 按工具计的速率限制与配额 | `policy/limits.yaml` 数的是数量，它没有工具名这一维 |
| 不是放在头里的凭据 | `inject` 是头注入；放在 query、cookie 或非 HTTP 协议里的 key 只能进沙箱（§8.1） |
| 一种安装宿主侧 MCP 服务器的 mixin | mixin 是声明；内容属于 workload（`sandbox-kit-design.md` §2.3） |

★ 这份文档里每一条描述代码的地方都点了文件名与行号。代码动了，行号跟着动 ——
  这是本仓库对每一份其它设计文档守着的契约，
  也是 §10.1 那张表是一份**位置**清单而不是一份**意图**清单的原因。
