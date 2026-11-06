# MCP 接入设计 —— 运行期才定下来的工具集，与一张静态的策略表

> 状态：**§1 讲的是代码现状。§2 往后是一份提案** —— §2 往后的东西都还不在树上。
> `go.mod` 里没有 MCP 依赖，没有 `mcp/` 配置目录，也没有任何说这套协议的代码。
>
> 范围：MCP（Model Context Protocol）服务器如何变成 openheron agent 可以调用的工具集，
> 这些工具叫什么名字，以及策略层如何决定它们能不能跑。
> 不在范围内：MCP 协议本身、传输方式的实现（那属于 `mcp-go`）、
> 以及沙箱打包（见 `sandbox-kit-design.md` §8）。

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

★ 那行注释是树上**唯一**的 MCP 痕迹。没有 MCP 依赖，没有 `mcp/` 目录，
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

★ `policy/` 下有**两个**文件：`permissions.yaml`（档位）与 `limits.yaml`（数量上限）。
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
| 3 | **运行期才有的工具集没法写进静态策略** —— 策略是以名字为键的表，而名字要等连接建立才拿到 | `agentkit/build/policy.go:39` 与 `:94` —— 只有精确匹配。每一个 MCP 工具都落进 `allow` 兜底档，而对一个能执行任意进程的工具来说，**这不是一个安全的缺省** | §4 |

★ 问题 3 是「命名空间（§3）」与「前缀语法（§4）」是同一个决定的两个半边、
  而不是两件方便事的原因：**命名空间让前缀才有意义** ——
  因为前缀要切得干净，前缀的边界就不能有歧义。

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

## 4. 策略表里的前缀匹配

档位与每个 agent 的收窄，除了精确名字，还接受一种**前缀模式**。

| 条目 | 匹配 |
|---|---|
| `execute` | 名字正好是 `execute` 的那个工具 |
| `mcp__github__*` | server 名为 `github` 的每一个工具 |
| `mcp__*` | 来自任何 server 的每一个 MCP 工具 |

### 4.1 语法规则

| # | 规则 | 理由 |
|---|---|---|
| 1 | 只有**结尾**的 `*` 是前缀模式；`*` 出现在别处是**加载期硬错误** | `mcp*foo` 看着像通配符而其实不是 —— 一条读起来与实际覆盖范围不同的规则，就是一次静默的策略错配 |
| 2 | 单独一个 `*` 是**加载期硬错误** | 没有 `allow` 那一列可退回（§4.2）：`deny: ["*"]` 是「什么工具都不要」，`ask: ["*"]` 是「什么都点一下」。两者都不是「写个星号」的意思 |
| 3 | `*` 前面不能为空 | 规则 2 |
| 4 | 同一档内，精确条目与前缀条目**等价** —— 命中任一即进这一档 | 闸门只认档位 |

### 4.2 优先级不变

```
deny → ask → allow (the fallback)
```

★ 不变。而且外层策略仍然压过内层：一条 `deny` 不能被某个 agent 的
  `disabled_tools` 放松。前缀语法只决定**一条条目是否匹配某个名字** ——
  它不新增档位，也不改档位顺序。

★ **`policy/limits.yaml` 不参与。** 它装的是数量，不是工具名，匹配语义照旧。

### 4.3 前缀语法用在哪三处

| 位置 | 写在哪 | 含义 |
|---|---|---|
| `policy/permissions.yaml` → `ask` | `agentkit/build/policy.go:39` | 这些工具需要人点头 |
| `policy/permissions.yaml` → `deny` | `agentkit/build/policy.go:39` | 这些工具根本不许跑 |
| 某个 agent 的 `disabled_tools` | `agentkit/build/topology.go:64` | 这个 agent 拿不到这些工具 |

★ 这三处是同一个决定的三个层次，所以共用一套语法。
  一个 agent 禁掉 `mcp__github__*`，就是对这个 agent 禁掉整个 server ——
  它不需要知道那个 server 有哪些工具。

### 4.4 代价是什么

★ 前缀规则是一件**钝器**：`deny: ["mcp__*"]` 会把每一个 MCP 工具都从 agent 里拿走，
  `ask: ["mcp__*"]` 会把它们每一个都放到点头之后。这通常正是本意，
  但它只有一行，而且很容易顺手写下来。
  真正值得先够的是点名单个 server 的那条规则（`mcp__github__*`）。

## 5. `mcp/<server>.yaml` 分区

### 5.1 为什么要单独一个分区

MCP 服务器按服务器一个文件来配：

```
.openheron/mcp/
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
| `env` | `map[string]string` | `{}` | 子进程的环境；密钥写成 `${ENV}` / `@file:`，绝不内联 |
| `url` | `string` | — | 仅 http，**必填**：服务器地址 |
| `headers` | `map[string]string` | `{}` | 仅 http：每次请求都带的头 |
| `timeout` | `LenientDuration` | `30s` | 连接 + 初始化 + 取工具清单的上限 |
| `tools` | `[]string` | `[]`→全部 | 暴露出去的工具名白名单 |

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

`mcp/` 与 `tools/`、`policy/` 一起进热更新：改一个服务器的命令或超时，
下一轮就该生效，不必重启。

★ 这意味着客户端生命周期要扛得住一次重载：快照换代时，
  stdio 的子进程被拆掉、新的被拉起来。
  一个资源无法重建的热分区，就是一个谎称自己是热的分区。

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

### 6.2 客户端生命周期

组件不管它（§2.2），所以 provider 管：

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

## 7. 闸门与档位

### 7.1 档位照旧适用

一个叫 `mcp__github__create_issue` 的工具被查起来与其它工具一模一样：

```
Policy.TierOf("mcp__github__create_issue")
```

`ask` 里有 `mcp__github__*` 就落进 `ask` 档，`deny` 里有 `mcp__github__*`
就落进 `deny` 档。闸门代码一个字都不用改：它读档位，然后动作（§1.3）。

★ **这正是命名空间的意义。** 闸门、各张表、API 的 `EffectiveTools` 视图
  与每个 agent 的收窄，全都在名字上工作。
  一个带命名空间的名字，是它们本来就懂的名字 ——
  策略层、API 与前端都不需要接一个新概念。

### 7.2 stdio 的权限问题

★ stdio 之下，MCP 服务器是 openheron 的**子进程**：它能写文件、能联网，
  能读到 openheron 读得到的一切。
  配置设计 §6.6 给 HTTP 工具定的「只连公网」**在这里不适用** ——
  那条限制的是*工具*能碰到什么，不是*服务器*能做什么。

这是命名空间与前缀语法合起来的最强理由：
它们是「在有人把成员列出来之前，先给一类外部工具定档」的办法。
`ask: ["mcp__*"]` 是一行字，意思是「来自任何 MCP 服务器的每个工具都要先点头」。

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
  那会让 mixin 带上内容，而内容属于 workload（§8）。

## 9. 改动清单

下面这些都还不在树上。这是一轮后来的实现要照着走过的清单。

### 9.1 代码

| # | 文件 | 改动 |
|---|---|---|
| 1 | `go.mod` | 加 `github.com/cloudwego/eino-ext/components/tool/mcp` 与 `github.com/mark3labs/mcp-go` |
| 2 | `agentkit/spec/config.go` | `DirMCP = "mcp"` 与一条 `Categories` 条目，热更新（`hotDirList` 在 `:77` 会自动带上它） |
| 3 | `agentkit/spec/decl/decl.go` | `MCPServerCfg` 声明结构体；命名空间常量 |
| 4 | `agentkit/spec/load.go` | `mcp/<server>.yaml` 的加载器，带 §3.1 与 §5.2 的文件名与字段校验 |
| 5 | `agentkit/spec/defaults.go` | `EnabledToolNames` 加上带命名空间的 MCP 工具 |
| 6 | `agentkit/spec/write.go` | 新建 `mcp/` 文件的脚手架 |
| 7 | `agentkit/build/tools.go` | `mcpToolProvider`，注册在占位注释那一行 `:35` |
| 8 | `agentkit/build/policy.go` | `TierOf` 与 `Blocked` 的前缀匹配器 |
| 9 | `api/v2_settings.go` | `mcp/` 的可编辑分区 |
| 10 | `agentkit/build/nettools.go` | 输出封顶，套到 MCP 批次上 |

### 9.2 测试

| 领域 | 要证明什么 |
|---|---|
| 命名 | 一个提供 `ls` 的服务器产出 `mcp__<server>__ls`，且 agent 仍能启动 |
| 命名 | 64 字符的边界，以及越界之后的「跳过 + 警告」这条路 |
| 命名 | 同一服务器里两个规整后同名的工具 —— 第二个被跳过，且警告里说了 |
| 匹配 | 一条前缀规则命中一个 server 的工具，且只命中它的 |
| 匹配 | `mcp*foo` 与单独的 `*` 是加载期错误 |
| 匹配 | `deny` 仍然压过 `disabled_tools` |
| provider | 一个够不着的服务器丢掉它的工具、警告里带服务器名，其它 provider 的工具不受影响 |
| 生命周期 | 一次热重载换掉客户端，旧的 stdio 子进程没了 |

### 9.3 文档

| 文档 | 要改什么 |
|---|---|
| `docs/developer-manual.md` | 「加一个工具」的配方加上 MCP provider；「加一个分区」的配方加上 `mcp/` 这一例 |
| `docs/user-manual.md` | 怎么配一个服务器，以及界面上看到的名字长什么样 |
| `docs/architecture.md` | 工具装配路径加上 MCP provider |

## 10. 这份设计不管什么

| 没覆盖 | 为什么 |
|---|---|
| MCP 协议与各种传输 | 它们属于 `mcp-go`；openheron 只挑一个客户端并配它 |
| 带发现、健康检查与重试的 server 注册表 | 一个服务器一个文件，够起步了；注册表是下一轮的事 |
| MCP 的 resources、prompts 与 sampling | eino-ext 那个组件**只**暴露工具 |
| 按工具计的速率限制与配额 | `policy/limits.yaml` 数的是数量，它没有工具名这一维 |
| 一种安装宿主侧 MCP 服务器的 mixin | mixin 是声明；内容属于 workload（§8） |

★ 这份文档里每一条描述代码的地方都点了文件名与行号。代码动了，行号跟着动 ——
  这是本仓库对每一份其它设计文档守着的契约，
  也是 §9.1 那张表是一份**位置**清单而不是一份**意图**清单的原因。
