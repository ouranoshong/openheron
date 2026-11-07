# MCP 接入设计 —— 运行期才定下来的工具集，与一张静态的策略表

> 状态：**已实现** —— 这份文档设计的功能已经在树里了。
> 依赖 `github.com/cloudwego/eino-ext/components/tool/mcp`（v0.0.9）与
> `github.com/mark3labs/mcp-go`（v0.43.0）进了 `go.mod`；`mcps/` 分区、MCP provider、
> 命名空间、写在文件里的档位、manager 与 `/mcps` 端点全都落地了。
> §1 讲的是代码现状；§2 往后讲的是同一个功能当初是怎么设计的，
> 而每一句话仍然都指着一个文件和一个行号。★ 其中两半**仍然是提案**：
> **沙箱 Kit**（§8）还没有打包 MCP 服务器，**全局 `deny`** 也还在树上（§4.2）——
> MCP 这个功能并没有把它带走。
>
> 范围：MCP（Model Context Protocol）服务器如何变成 openheron agent 可以调用的工具集，
> 这些工具叫什么名字，以及策略层如何决定它们能不能跑。
> 不在范围内：MCP 协议本身、传输方式的实现（那属于 `mcp-go`）、
> 以及沙箱打包（见 `sandbox-kit-design.md` §2.3）。

## 1. 代码现状

### 1.1 工具从哪来

```
mcps/*.yaml → spec.loadMCP → decl.MCPServerCfg
  → mcpManager.Tools (one client per server, one listing)
  → mcpToolProvider.Provide → mcpPlan (the namespace, the skips, the tiers)
  → snapshot.buildHeavy → heavyDeps.tools
  → applyToolConfig → deep.Config.ToolsConfig.Tools
  → deep.New → the eino agent
```

| 步骤 | 位置 |
|---|---|
| 加载一个 `mcps/` 文件 | `agentkit/spec/load.go:256` `loadMCP` |
| 声明结构体 | `agentkit/spec/decl/decl.go:454` `MCPServerCfg` |
| 读一个服务器的工具 | `agentkit/build/mcpmanager.go:89` `mcpManager.Tools` |
| 构造每个 provider 的那一批 | `agentkit/build/tools.go:55` `toolRegistry.Build` |
| 物化，一个快照一次 | `agentkit/build/snapshot.go:47` `buildHeavy` |
| 交给 eino | `agentkit/build/assemble.go:31` `applyToolConfig` |

★ `buildHeavy` 跑在一个 `sync.Once` 之下（`agentkit/build/snapshot.go:40-45`）。
  于是工具集在一个快照的生命周期里是**固定的** —— 它在快照换代时重建，
  而不是在一个工具的行为变化时重建。

★ **MCP provider 是「没有配置变化也能让换代号动起来」的原因。**
  一个服务器可以自己加一个工具，而把清单冻住的快照要看到它就得重启。
  ⇒ builder 在组装这一轮的 agent 之前先问 manager 一声，
  而清单变了就通过与配置变化同一个地方推进换代号
  （`agentkit/build/builder.go:67`、`:105`）。

### 1.2 provider 契约

`agentkit/build/tools.go:19` 就是全部的扩展点：

```go
type toolProvider interface {
	ID() string
	Optional() bool
	Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error)
}
```

注册表是 `defaultToolRegistry`（`agentkit/build/tools.go:35`），里面带着 MCP provider：

```go
		mcpToolProvider{mgr: mgr},
		// projectToolsProvider{}, ...
```

★ MCP provider 是**唯一**一个需要 manager 的 provider：一个 MCP 客户端活得比
  provider 返回的那一批工具更久，所以客户端归 manager 管，而不是归 provider 管
  （`agentkit/build/tools.go:31-34`、`agentkit/build/mcpmanager.go:42`）。
  ★ 别的 provider 挂上一批工具就完事了；服务器是**一条连接**，而连接有生命。

### 1.3 名字就是策略

工具名是每一张决定「这个工具能干什么」的表的键：

| 表 | 位置 | 决定什么 |
|---|---|---|
| `Policy.TierOf` | `agentkit/build/policy.go:49` | `allow` / `ask` / `deny` |
| `Table.Blocked` | `agentkit/build/policy.go:161` | 被 deny（外层）/ 被 disable（内层） |
| `Table.Filter` | `agentkit/build/policy.go:184` | 把工具从 `runCtx.Tools` 里摘掉 |
| `Builder.EffectiveTools` | `agentkit/build/introspect.go:49` | API 报告什么 |

★ **上面每一条查找都是按名字查的，而名字就是全部契约。**
  `TierOf` 是 `slices.Contains` 加一次 map 查找；`Blocked` 是一次 map 查找
  加两次前缀扫描（`agentkit/build/policy.go:168`、`:176`）。
  ★ 那两次前缀扫描正是 MCP 带来的：agent 的 `mcps:` 清单按**命名空间前缀**拒绝，
  agent 的 `disabled_tools` 接受**结尾的 `*` 模式**（§4）。
  其余全是精确匹配 —— 而一个 MCP 工具的档位**在服务器还没起来之前**就能回答（§7.3）。

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
| `tools/` | `agentkit/spec/config.go:63` | 一个工具一个文件，**文件名是白名单**（`agentkit/spec/load.go:165`），热更新 |
| `policy/` | `agentkit/spec/config.go:53` | `ask` / `deny` / `limits`，热更新 |
| `agents/` | `agentkit/spec/config.go:48` | frontmatter，含 `disabled_tools` 与 `mcps:`，热更新 |
| `mcps/` | `agentkit/spec/config.go:72` | 一个服务器一个文件，**文件在就是定义**，热更新 |

★ `policy/` 下**今天**有**两个**文件：`permissions.yaml`（档位）与 `limits.yaml`（数量上限）。
  `limits.yaml` 里没有工具名 —— 那些上限是按 agent 与按类别算的 ——
  所以它在这份设计里不参与。

★ **`mcps/` 是第四个分区，而且是热的。** `agentkit/spec/config.go:72`
  把它登记成 `Editable: true` 与 `hot: true`：配置编辑器可以写它，热重载盯着它，
  加载器把服务器放进 `Config.MCPs`（`agentkit/spec/config.go:119`）。
  ★ 一个资源重建不回来的热分区，就是一个谎称自己是热的分区 ——
  所以客户端的生命挂在声明的指纹上（`agentkit/build/mcpmanager.go:330`）。

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
| 不管客户端生命周期 —— 不拉起、不 `Start`、不 `Initialize`、不 `Close` | 连接归 openheron 管：manager 建它、初始化它、养着它，进程退出时拆掉它（`agentkit/build/mcpmanager.go:395`） |
| 不选传输方式 | stdio / SSE / streamable HTTP 是 `mcp-go` 的 `client` 构造器；openheron 挑一个（`agentkit/build/mcpmanager.go:418`、`:472`） |
| 没有配置格式 | schema 就是 §5 —— `mcps/<server>.yaml`，一个服务器一个文件（`agentkit/spec/load.go:256`） |
| 不支持流式 | 那层包装只实现了 `Info` 与 `InvokableRun`，所以它是 `tool.InvokableTool`，永远不是 `tool.StreamableTool`。整个结果作为一块 JSON 回来 |

★ **这个组件是适配器，不是功能本身。** 生命周期、命名、档位与策略都是 openheron 自己的，
  协议才是组件带来的。provider 只调用它的一个函数（`agentkit/build/mcptools.go:255`），
  而那个调用周围的一切都是 openheron 自己的代码。

★ 依赖版本是兼容的。这个组件自己的 `go.mod` 钉的是
  `github.com/cloudwego/eino v0.6.0`，但它用到的 API 在 v0.9.22 里还在
  （`schema.NewParamsOneOfByJSONSchema`，`eino@v0.9.22/schema/tool.go:297`）。
  已实测：把 `components/tool/mcp v0.0.9` 与 `eino v0.9.22` 、
  `github.com/mark3labs/mcp-go v0.43.0` 一起编译通过。

### 2.3 决定这份设计的三个问题

| # | 问题 | 咬在哪里 | 由什么解决 |
|---|---|---|---|
| 1 | **服务器的工具名可能撞上保留名** —— 一个提供 `ls` 或 `grep` 的 MCP 服务器 | `agentkit/build/tools.go:99` —— `validateBatch` 返回错误，`Build` 往上抛（`agentkit/build/tools.go:73`），于是**整个 agent 起不来**。不是「这个工具被丢掉」—— 是进程没了 | §3 —— `mcp__<server>__ls` |
| 2 | **服务器的工具名可能对 provider 非法** —— 带服务器前缀的名字很常见，比如 `github.create_issue` | 模型 provider 的 function name 字符集不收 `.`；失败发生在调模型的时候，而不是加载的时候 | §3 —— `sanitizeMCPToolName`（`agentkit/build/mcptools.go:150`） |
| 3 | **运行期才有的工具集没法写进静态策略** —— 策略是以名字为键的表，而名字要等连接建立才拿到 | `agentkit/build/policy.go:49` 与 `:161` —— 只按名字查。每一个 MCP 工具都会落进 `allow` 兜底档，而对一个能执行任意进程的工具来说，**这不是一个安全的缺省** | §4 与 §7.3 —— 命名空间的缺省是 `ask`（`agentkit/build/policy.go:55`） |

★ **三条在树里都关上了。** 问题 1 靠命名空间，问题 2 靠规整函数，
  问题 3 靠写在文件里的档位：一个 MCP 工具只有在它自己服务器的文件用 `allows:`
  点名时才 `allow`，其余一律 `ask`。

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
| `server` | **我们定** —— `<server>.yaml` 的文件名 | 必须匹配 `^[a-z0-9]+(_[a-z0-9]+)*$`；不匹配是**加载期硬错误**（`agentkit/spec/load.go:258`） |
| `tool` | **服务器定** | `[A-Za-z0-9_-]` 之外的字符替换成 `_`；大小写保持原样（`agentkit/build/mcptools.go:150`） |

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
  ⇒ provider 把这三样都写进日志（`agentkit/build/mcptools.go:235`），
  而面板把它显示成 `skipped` 并带上原因（`agentkit/build/mcpstatus.go:145`）。

### 3.3 命名空间顺带把问题 1 也关上了

一个提供 `ls` 的服务器现在提供的是 `mcp__<server>__ls`，
不再与 `reservedToolNames`（`agentkit/build/tools.go:46`）相撞。
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
  是 `mcps/<server>.yaml` 里一个**空的** `allows:`，不是共享表里的一个模式。
  那种写法比模式**更强**：它覆盖服务器**还没提供**的工具。

★ **全局 `deny` 还在树上。** 配置设计提议把它去掉（配置设计 §6.3），
  而这份设计就是照着那份提案写的 —— 但 MCP 这个功能**没有**把全局清单一起带走：
  `Policy.Deny` 还在（`agentkit/build/policy.go:51`），而且仍然是**精确匹配**，
  所以一个落在全局 `deny` 里的名字仍然是 denied，哪怕它自己服务器的文件允许它
  （`agentkit/build/mcptools_test.go:166` 就是说这件事的测试）。
  ★ 于是「不被暴露」现在在**三个**地方说：全局 `deny`、一个文件的**白名单**
  （它从 server **实际有的**工具里挑出要暴露的那些），以及某个 agent 的
  **`disabled_tools`**（它说这个 agent 拿不到什么）。
  收窄是那句**关于命名空间**而不是关于工具的陈述 —— 「这个 agent 不要任何 MCP 工具」——
  而前缀正是给一个命名空间起名的写法。

### 4.1 语法规则

| # | 规则 | 理由 |
|---|---|---|
| 1 | 只有**结尾**的 `*` 是前缀模式；`*` 出现在别处是**加载期硬错误** | `mcp*foo` 看着像通配符而其实不是 —— 一条读起来与实际覆盖范围不同的规则，就是一次静默的错配 |
| 2 | 单独一个 `*` 是**加载期硬错误** | `disabled_tools: ["*"]` 会把 agent 自己的工具也一起拿走；那是要写出来的一个决定，不是一个通配符 |
| 3 | `*` 前面不能为空 | 规则 2 |
| 4 | 精确条目与前缀条目**等价** —— 命中任一即把这个工具从那个 agent 那里摘掉 | 这里没有档位可选：那个工具对那个 agent 就是不在 |

### 4.2 前缀语法不新增档位

★ 档位现在住在工具自己的文件里（§7.3），而一个 MCP 工具拿到的是其中两档 ——
  `deny` 那一档并没有跟着全局清单一起消失（§4.2）：

```
allow (if the file's allows list names it) → ask (everything else)
```

★ 落在全局 `deny` 里的名字仍然是 `deny`：全局清单是外层边界，
  而且它是被第一个匹配的那一个（`agentkit/build/policy.go:51`）。
  上面那两档，是全局清单对某个名字没有说法时它拿到的东西。

★ 前缀语法只决定**一条条目是否匹配某个名字** —— 它不新增档位，也不重排任何档位。
  ★ **而且外层边界还在**：全局 `deny` 是第一个被匹配的
  （`agentkit/build/policy.go:165`），所以它不会被某个 agent 的 `disabled_tools` 放松。
  ★ 配置设计提议去掉那份全局清单（配置设计 §6.3），MCP 功能就是照着那份提案写的 ——
  但那份提案没有实现，树上保留着这条保证。

★ **`policy/limits.yaml` 不参与。** 它装的是数量，不是工具名，匹配语义照旧。

### 4.3 前缀语法用在哪

| 位置 | 写在哪 | 含义 |
|---|---|---|
| 某个 agent 的 `disabled_tools` | `agentkit/build/topology.go:70` | 这个 agent 拿不到这些工具 |

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
  （`agentkit/spec/load.go:165` 把文件名列成白名单），而 MCP 里的实体是**服务器**：
  一个服务器有一个命令、一份参数、一个环境、一种传输和一条生命周期。
  把 N 个服务器塞进一个文件，就要在「文件内容就是配置、不带外层包装键」这个分区规则
  之下再套一层列表；而整份文件替换时，也说不清是哪个服务器变了。
  ★ `mcps/` 遵守同一条规则，一个服务器一个文件，加载器用文件名给这个分区建键
  （`agentkit/spec/load.go:256`）。

配置设计 §2 的那条规则 —— *一实体一文件* —— 落在服务器上，不是落在工具上。

### 5.2 字段

| 字段 | 类型 | 缺省 | 含义 |
|---|---|---|---|
| `transport` | `string` | `stdio` | `stdio` 或 `http`；**未知值是硬错误**，不静默退回 stdio。★ `http` 的意思是 **streamable HTTP**；不支持 SSE（§5.7） |
| `command` | `string` | — | 仅 stdio，**必填**：要执行的命令 |
| `args` | `[]string` | `[]` | 仅 stdio：它的参数 |
| `env` | `map[string]string` | `{}` | 仅 stdio：**叠加**在继承来的环境之上的变量（§5.5）；取值与其他配置一样展开。★ http 服务器写 `env:` 是**加载期硬错误**（§5.7） |
| `url` | `string` | — | 仅 http，**必填**：服务器地址 |
| `headers` | `map[string]string` | `{}` | 仅 http：每次请求都带的头 |
| `timeout` | `MCPDuration` | `30s` | 连接 + 初始化 + 取工具清单的上限 |
| `call_timeout` | `MCPDuration` | `60s` | **一次工具调用**的上限（§6.5） |
| `tools` | `[]string` | `[]`→全部 | ★ **过滤器，不是开关** —— 从这个 server **自己有的**工具里挑出来要提供的那些**原始**工具名（不是带命名空间的那个）；不写 = 全部 |
| `allows` | `[]string` | `[]` | 这个 server **自己的**工具名里，哪些**不用点头**就跑 —— ★ 这个 server 提供的其余一切都要先点头，包括那些它**还没提供**的工具 |

★ **`command` 在沙箱的 PATH 里解析，绝不相对项目根。** 工作区不许决定跑什么：
  一个相对项目根解析的命令，等于让一次 checkout 决定 agent 跑哪个可执行文件，
  还带着 agent 的权限。⇒ 可执行文件来自 **Kit** —— mixin 的安装钩子把它放进去（§8）——
  不在 Kit 里的话，就由用户自己装。

★ 所以两边各说各的，从各自的角度描述同一个 server：**mixin** 说装什么，
  **配置**说怎么调用它（`command` 加 `args`）。配置里的 `command`
  点名的正是 Kit 铺下的那个东西。

★ 这几个键在 `tools/` 与 `mcps/` 的每一个文件里都是同一个意思，
  而且它们是用**那个文件自己的词儿**写的：这里的 `allows: [create_issue]` 是
  server 自己的名字，不是合成出来的那个名字。provider 会把它翻译成模型看到的
  那个名字（§7.3）。

★ **缺省是 `ask`，`allows:` 是那个例外。** 没有第二个键，也没有按组的缺省 ——
  同一条规则盖住文件系统工具、HTTP 工具与一个 MCP server。
  见 `docs/config-design.md` §6.6。

★ **文件里没有 `deny`。**「不被暴露」现在说三处：全局 `deny`（§4.2）、这个文件的
  **清单** —— 不在 `tools:` 里的工具不被提供 —— 与某个 agent 的 **`disabled_tools`**。

★ `timeout` 是**宽松**时长：写错就退回默认并记进 `deferred`，
  与 `debounce`、`start_timeout` 完全一样。写错 timeout 改变的是「等多久」，
  不是「会发生什么」—— 它不该让进程起不来。

★ **类型是 `decl.MCPDuration`，而不是这份设计里写的那个包级 `LenientDuration`。**
  差别在**来源**：别的宽松时长用的那份包级清单不带文件也不带键，
  所以它说不出**是哪一个**值写错了。`MCPDuration` 留着原文，
  也留着「这个键到底写没写」，所以 `deferred` 那一条能点出文件与键
  （`agentkit/spec/decl/decl.go:421`、`agentkit/spec/load.go:353`）。

★ `tools: []` 的意思是**全部**，不是**一个都没有**。
  一个表示「什么也没有」的空白名单会与「键缺失」分不出来 —— 而它是对 server 提供什么的
  **过滤器**（§5.3），不是开关。

★ **`http` 就是 streamable HTTP，而且只是它。** `mcp-go` 带着两个 HTTP 客户端 ——
  `client.NewStreamableHttpClient` 与 `client.NewSSEMCPClient` —— 而一个 `transport`
  的取值没法在它们之间做选择。⇒ `http` 指的是**可流式**的那个，是 MCP 规范现在定义的
  那一个；openheron 不说 SSE。★ 另一条路（再加一个 `transport` 取值）会把一个遗留的
  传输方式塞进每一个根本不想要它的用户的配置里，而第二个取值还得是*那些*想要它的人的
  那个值。

★ **`env:` 在 http 服务器上没有意义，所以那儿它是错误。** http 服务器是一个远端进程，
  而本进程的环境不是它的环境。⇒ 声明了 `env:` 的 http 服务器是**加载期硬错误**
  （§5.7），不是一个被悄悄忽略的键。★ 一个什么也不做的键比一次拒绝更糟：
  用户会以为那个服务器是带着这个变量跑的。

★ **有两个字段就是组件自己的。** `tools:` 是组件的 `ToolNameList` ——
  从 server **自己有的**工具里挑出来要留下的那些名字 —— 而 `headers:` 是它的
  `CustomHeaders`（§2.2）。★ 两者都用 server 自己的词儿写，而组件收的也是同一套词儿，
  所以不需要任何翻译。

### 5.3 文件定义 server

没有 `enabled` 字段。服务器是被**定义**的，因为它的文件存在；删掉文件就是删掉这个定义。
这条规则 `tools/` 已经在那些 provider 会读配置文件的工具上遵守了 —— ★ 而且它让
**server 存不存在**只待在一个地方，而不是拆成「文件在不在」和「文件里那个标志说开不开」
两处。

★ **文件定义 server；它不决定 server 提供什么。** server 有哪些工具来自 server 自己，
文件的 `tools:` 只是从里面挑出值得暴露的（§5.2）。

★ **文件说服务器是什么；agent 说谁拿它。** 哪些 agent 加载一个服务器，
  取决于它 frontmatter 里自己的 `mcps:` 清单：★ **缺失 = 全部已配置的服务器，
  `[]` = 一个都没有** —— 与 `skills` 已经在用的那个指针技巧同一个形状，
  出于同一个理由：常见情形是**一个**地方。写好服务器文件，
  每一个没有把自己的清单收窄的 agent 就都拿到它。

★ **这个收窄落在哪里不是白给的**，因为工具集是**整个快照一份**（§1.1）。
  答案在 §11.3：agent 的**表**，也就是 `disabled_tools` 已经在收窄工具的那个地方。

★ **子 agent 有自己的一份清单，就像它自己的 `disabled_tools`。**
  `decl.SubAgentSpec` 自带 `DisabledTools` 与 `Skills`（`agentkit/spec/decl/decl.go:619`），
  所以它也自带一份 `mcps:` 清单（`agentkit/spec/decl/decl.go:629`）。
  ⇒ 清单是从**正在跑的那个 agent** 读的 —— 根 agent，或者这一轮所在的那个子 agent ——
  而不是从根 agent 的清单读的。

★ **一个不存在的名字是加载期硬错误。** 一个写着 `githbu` 的 agent 会安安静静地
  一个 MCP 服务器都拿不到，而「因为笔误而少掉的工具」与「因为服务器倒了而少掉的工具」
  长得一模一样。⇒ 加载器拒绝这个名字（§5.7，`agentkit/spec/load.go:499`）。

★ server 名就是文件名去掉 `.yaml`，它就是命名空间的那一段（§3.1）。
  不匹配 `^[a-z0-9]+(_[a-z0-9]+)*$` 的文件名是**加载期硬错误** ——
  是我们自己写的，我们自己修。★ 配置编辑器也拒绝同样的名字
  （`agentkit/spec/write.go:107`），所以经编辑器写出来的文件也不会破坏这条规则。

### 5.4 这个分区是热的

`mcps/` 与 `tools/`、`policy/` 一起进热更新：改一个服务器的命令或超时，
下一轮就该生效，不必重启。★ 热目录是从分类本身收集起来的
（`agentkit/spec/reload.go:21`），而 `mcps/` 在里面，是因为它自己这么说
（`agentkit/spec/config.go:72`）。

★ 这意味着客户端生命周期要扛得住一次重载：快照换代时，
  stdio 的子进程被拆掉、新的被拉起来。
  一个资源无法重建的热分区，就是一个谎称自己是热的分区。
  ⇒ 客户端在**声明**变了的时候被重建，而不是在快照变了的时候：
  指纹就是声明自己的那份（`agentkit/build/mcpmanager.go:330`），
  而拉起会换掉旧客户端（`agentkit/build/mcpmanager.go:229`）。

### 5.5 子进程的环境

★ **子进程的环境 = openheron 自己的环境，被配置里的 `env:` 覆盖同名项。**

★ **环境是 stdio 的事。** http 服务器没有子进程，所以 `env:` 压根不是它可以声明的字段
  （§5.2、§5.7）。

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
  东西。区别只在于 `execute` 不在 `allows:` 清单里，而 MCP 工具未必在。
  ⇒ 若这个不对称对你重要，就把 `mcp__*` 放进那个 agent 的 `disabled_tools`（§4）。

★ 配置里的 `env:` **不替换**继承来的环境，而是**叠在它上面**。
  `env:` 为空的配置，产出的子进程仍然有一个完整的环境。
  ⇒ 子进程拿到的是 `os.Environ()`，而文件里的那些条目追加在后面
  （`agentkit/build/mcpmanager.go:458`）。

★ 那份环境里的 `PATH` 就是继承来的那个，没有被改动：命令在沙箱的 PATH 里解析，
  绝不相对项目根（`agentkit/build/mcpmanager.go:418`）。

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

# ★ The default is ask, so an empty list means "every tool of this server asks".
#   A trusted server names its exceptions:
allows: [read_issue, list_issues]

# ★ The tools to offer, by the server's own names, picked out of the ones the
#   server actually has. Absent = all of them.
tools: [create_issue, list_issues, search_issues]

# ★ To take one tool out, leave it out of the list above. The list filters what
#   the server offers; it never adds a tool the server does not have.
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
# ★ The default is ask, so the list names the exceptions: the read-only tools
#   run straight through, and write_file / edit_file / execute ask.
allows: [ls, read_file, glob, grep]
# tools: [ls, read_file, glob, grep]   # uncomment for a read-only agent
```

#### `tools/kb.yaml`

```yaml
# .openheron/tools/kb.yaml
corpus_dir: ""
index: default
model: ""
query_instruction: ""
batch_size: 10
max_batch_chars: 1200
top_k: 8
# ★ kb_search reads and does not ask; kb_index writes the index on disk, so it asks.
allows: [kb_search]
```

#### `tools/http.yaml`

```yaml
# .openheron/tools/http.yaml
methods: [get]
headers: {}
timeout: 30s
# ★ The read methods run straight through; the write methods change the outside
#   world, so they ask.
allows: [get]
```

#### ★ 谁写这些文件

| 谁来写 | 写什么 |
|---|---|
| 用户 | 手写 `mcps/<server>.yaml` 这些文件 |
| `openheron init` | ★ 一个**示例** —— `mcps/example.yaml`，而且是**能加载**的那份：文件**定义**了 server（§5.3），所以一份解析不了的文件不是「server 关着」，而是「配置加载不了」。它的 `command` 是一个没装的占位符，于是 server 起不来，**日志里说一句，agent 照跑**。把它复制成 `mcps/<server>.yaml` 并填上你自己的 command，就是定义它（`initcfg/initcfg.go:184`） |
| 配置编辑器 | ★ 同一份模板，走它的「新建文件」对话框 —— 编辑器**能写** `mcps/`（`agentkit/spec/write.go:189`），而且它拒绝不是一个可读回服务器名的文件（`agentkit/spec/write.go:107`） |

### 5.7 什么会大声失败

★ **我们自己的配置在加载期就失败；别人家的内容被丢掉并留个话儿。** 这就是 §3.2 画下的
  那条线，而这张表沿着它走。

| 情况 | 什么时候 | 为什么 |
|---|---|---|
| 不认识的 `transport` | 加载期 | 不静默退回 stdio —— 一个笔误不该跑起另一个 server |
| stdio 服务器缺 `command` | 加载期 | 没有东西可跑 |
| http 服务器缺 `url` | 加载期 | 没有东西可谈 |
| http 服务器上的 `env:` | 加载期 | http 服务器没有子进程可以交给一份环境（§5.2） |
| agent 的 `mcps:` 点名一个不存在的服务器 | 加载期 | 一个笔误会悄悄把这个 agent 收窄成什么都没有（§5.3） |
| `tools:` 点名一个 server 没有的工具 | 运行期 | server 的清单只有它起来之后才知道 —— provider 警告（§6.1），其余的工具照给 |

★ **加载期硬错误会让进程停下来，而这正是要点**：这些是有人手写的文件里的错误，
  下一次启动必须说出来。★ 那一行运行期是例外，因为 server 的工具清单不是我们的 ——
  与 §3.2 对一个合不出名字的工具画的线是同一条。

## 6. 装配路径

### 6.1 provider

provider 照 §1.2 的契约写，注册在注册表里（`agentkit/build/tools.go:41`）：

| 构件 | 做什么 |
|---|---|
| `mcpToolProvider.ID` | 日志与错误里的 provider 名 |
| `mcpToolProvider.Optional` | **true** —— 一个连不上的服务器不该把 agent 拖住 |
| `mcpToolProvider.Provide` | 向 manager 要每个配置好的服务器，取工具清单，给名字加命名空间并规整，返回那一批 |

★ **provider 走的是「某个 agent 要过的」那批服务器**，而不是整个分区：
  `Provide` 遍历 `Config.MCPServersInUse()`（`agentkit/build/mcptools.go:209`），
  而那份清单是 agent 与子 agent 之间的**并集**（§5.3、§11.3）。
  ⇒ 没有 agent 要的服务器永远不会被拉起，也永远不会变成一个子进程。

★ `Optional() == true` 是有代价的，`developer-manual.md` §3.1 第 3 步说得很直白：
  provider 那条 `Warn` 是**唯一**的通知 —— 工具不进表，
  用户看到的是「某个功能好像不见了」。
  对一个 MCP 服务器来说，那恰好是最不该留下的症状，
  所以警告里必须写清服务器名、传输方式与错误。

★ **够不着的服务器是一条警告，不是 API 里的一行。** **配置面**的那些视图**不**暴露
  那些配了却够不着的服务器。用户从警告、从缺失的那些工具就知道；那样一个端点会成为
  要跟第一处同步的第二份真相，而它回答的问题警告已经回答过了。

★ **`/mcps` 那个视图是例外，而且它不是第二份真相**（§11.2）：它是**管理者实时状态的
  投影**，而且它存在是因为用户问了。警告与视图读的是同一份状态 —— 管理者的客户端 ——
  所以两者不可能对某个服务器各说一套。

★ **文件点名而 server 没有的名字，也是一条警告。** 文件的 `tools:` 列的是 server
  **自己**的名字，而那份清单只有它起来之后才知道（§5.7）。⇒ provider 会警告 ——
  它写清服务器、文件要的那个名字，以及 server 实际提供什么 —— 并且把其余的工具照给
  （`agentkit/build/mcptools.go:240`）。
  ★ 悄悄给得比文件要的更少，就是这份设计拒绝的那个「某个功能好像不见了」的症状。

### 6.2 客户端生命周期

组件不管它（§2.2），所以由**管理器**管（§6.6）—— provider 向它要工具，然后拿到：

| 步骤 | stdio | http |
|---|---|---|
| 建立 | `transport.NewStdio` + `client.NewClient` | `transport.NewStreamableHTTP` + `client.NewClient` |
| 启动 | 子进程被拉起 | HTTP 会话打开 |
| 初始化 | 用协议版本与客户端信息 `Initialize` | 同样 |
| 取工具清单 | `ListTools` | 同样 |
| 拆掉 | 声明变了或进程退出时子进程被杀掉 | 会话关闭 |

★ manager 用的是**传输层**的构造器，再在它上面套 `client.NewClient`，
  而不是 `client.NewStdioMCPClient` / `client.NewStreamableHttpClient`：
  更低的那一层才是设置子进程环境与参数的地方
  （`agentkit/build/mcpmanager.go:418`、`:472`），
  而初始化对两种传输来说是同一个函数（`agentkit/build/mcpmanager.go:492`）。

★ 初始化的结果**可以丢掉**：provider 只要工具清单。
  初始化失败与「服务器连不上」是同一种失败 —— 这个服务器不贡献工具，并且说出来。

★ **http 那一列是一个客户端，不是两个。** `mcp-go` 还带着 `client.NewSSEMCPClient`，
  而 openheron 不调它：§5.2 说 `http` 的意思是 **streamable HTTP**，
  而一个配置问不出来的传输方式，是一个永远用不上却还得一直维护的传输方式。

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
  那个组件 README 从没提过的钩子（§2.2，`agentkit/build/mcptools.go:371`）。

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
  ⇒ `call_timeout`（`MCPDuration`，缺省 `60s`）给一次工具调用封顶。
  它是宽松的，与其他只决定「等多久」的时长一样。

### 6.6 mcps 管理器

★ **客户端归一个管理者拥有**，不归 provider。provider 回答的是「这个快照有哪些工具」；
  而客户端活得过 provider，靠的就是管理者。

| 职责 | 意思 |
|---|---|
| **一个 server 一个客户端** | 一个 server 就是一条连接，对 stdio 来说还是一个子进程 |
| **只起某个 agent 要的 server** | 管理者只启动那些 agent 的 `mcps:` 清单里的服务器（§5.3）—— 没有任何 agent 点名的 server 永远不会被启动，也永远不会变成子进程。★ 这份清单是**并集**，因为快照的工具集是所有 agent 共享的（§11.3） |
| **懒启动** | 一个 server 在第一轮要用它时才被拉起来，不是在启动时 |
| **只启动一次** | ★ 这次启动是**单飞**的：两个调用方要同一个 server，拿到的是**那一个**客户端（§6.8） |
| **崩了的子进程自动重启** | 一个死掉的 stdio server 在下一次调用时被重新拉起 —— 崩了不是配置错误，而一个死掉的 server 就那么死着才是最糟的结果 |
| **发现工具清单变了** | 管理者重新列举工具；清单变了就**推进代际**、工具重建 —— 与配置重载是同一条路（§6.7） |
| **服务器变了就换掉它** | 一份不再匹配运行中客户端指纹的声明会关掉旧客户端并拉起新的（`agentkit/build/mcpmanager.go:229`） |
| **进程退出时拆掉** | 管理者在关闭时关掉每一个客户端（`agentkit/build/mcpmanager.go:150`、`main.go:384`） |

★ **工具清单是管理者的活，不是快照的。** server 的工具是运行期才发现的，
  所以一份把它们冻住的快照，要看得见 server 昨天加的那个工具，就得重启。
  ⇒ 管理者去重新列举，而一次变化重新走配置变化走的那条路。

★ **客户端活得过快照 —— 实现就是这样。** 这份设计原本把拆解放在快照换代时；
  树上没有这么做，理由就是管理者本身：它只注册一次，跨快照拥有那些客户端，
  所以一次没碰某个服务器的配置变化，会让那个服务器的子进程继续跑着。
  ★ 换掉客户端的是**声明** —— 指纹就是声明自己的那份
  （`agentkit/build/mcpmanager.go:330`）—— 而另一个拆解时机就是进程退出。

★ **重新列举发生在这一轮装配 agent 之前，而不是在批次构建里面。** 批次是每一代构建
  一次，而代际只在清单变了的时候才动 —— 所以*在*构建里重新列举就永远轮不到跑。
  ⇒ builder 先问管理者，而一次变化在 `main.go` 自己的 `agentConfig` 里推进代际 ——
  与配置变化推进它的地方是同一个（`main.go:511` `bump`、`main.go:498` `set`）——
  于是快照在 agent 装配之前就被重建了。

★ **builder 需要一条推进代际的路。** `ConfigManager` 是只读的
  （`agentkit/build/manager.go:12` —— 它只有 `Current()`，别的都没有），
  所以 builder 在它旁边长出一个 **bump** 回调（`agentkit/build/builder.go:67`），
  而 `main.go` 把那个回调接到重载器用的同一个 `agentConfig.bump` 上
  （`main.go:247`、`main.go:511`）。
  ★ 推进代际的第二条路，就是关于它出错方式的第二条路。

★ **间隔是 30 秒，而且它不是一个配置键。** 某一轮来要一个 server、而上一份清单已经
  比 **30 秒**更老时，管理者才重新列举它 —— 所以一个闲着的 openheron 什么都不列举，
  而没有一轮会跑在一份超过一个间隔老的清单上。
  ★ 没人拧的旋钮就是没人测的旋钮，而一个 server 的工具清单是以天为尺度变化的。

★ **另一条路是后台定时器，它被否掉了。** 它会永远去轮询没人用的 server，
  而且需要一个自带生命周期的 goroutine —— 换一份以天为尺度变化的清单，不值。

### 6.7 从一个文件到一次工具调用

整条路径，从磁盘上那个文件一路走到闸门的那一个决定：

```
.openheron/mcps/github.yaml
  │  spec.loadMCP (agentkit/spec/load.go) — the file name and the fields are
  │  validated here, and a bad one is a startup error
  ▼
decl.MCPServerCfg — transport / command / args / env / allows / tools / timeouts
  │
  │  snapshot.buildHeavy (agentkit/build/snapshot.go)
  │    → mcpToolProvider.Provide (agentkit/build/mcptools.go)
  ▼
the mcps manager (agentkit/build/mcpmanager.go):
  per server → transport.NewStdio / transport.NewStreamableHTTP
            → tr.Start(ctx) → cli.Initialize(ctx) → mcpp.GetTools(ctx, &mcpp.Config{Cli: cli})
  │
  │  each tool:  name = mcp__<server>__<tool>     (sanitized; a bad name is skipped
  │                                                with a warning — §3.2)
  │              tier = the file's allows list, read in the SERVER's own names
  │                    and mapped to the exposed ones
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

★ **档位的解析**是一次查找加一个兜底，而闸门从不去问一个工具来自哪个文件 ——
  装配时填进表里的就是**对外的**名字：

```
Policy.TierOf(name):
  1. the file that provides the tool:
       its allows list, in its own names            → allow
       otherwise                                    → ask
  2. otherwise                                       → ask
```

★ **装配发生在 `agentkit/build`，不在 `agentkit/spec`**（§7.3）：只有 provider 知道
  自己文件的词儿，以及它背后的对外名字；而 `agentkit/spec` 不得 import `kb`
  （那条铁律），也不得 import eino（文件系统工具的名字住在那里）。

★ **重载是这条流程的一部分。** `mcps/` 是热分区，所以改动之后，上面整条链在下一轮
  重跑一遍：加载器重新读文件，快照换代，声明变了的那台服务器拿到新的客户端，
  而档位表用对外的名字重建。闸门从下一轮起读到新档位 —— 不用重启，
  也不会出现「一个工具还揣着旧档位」的空窗期。

★ **而一个自己改了工具清单的 server 走的是同一条路。** 管理者轮询那份清单（§6.6）；
  清单变了就推进代际、工具重建 —— 与配置变化走同一条路，同样不需要重启。

### 6.8 一次启动，一个客户端

★ **两个调用方要同一个 server。** provider 在构建批次时要（§6.1），
  面板在用户打开它时也要（§11.2）。两个都是普通请求，可能在同一刻到达 ——
  `/mcps` 对话框开着的时候，一轮对话开始。

⇒ **启动是按 server 单飞的。** 管理者按服务器名保存一份；第一个调用方启动客户端，
  其余调用方等着那一次启动，拿到同一个客户端。
  ★ 这与 §6.5 为调用守住的是同一条不变量 —— 一个 server，一个客户端 ——
  而它防住的失败也是同一类：一条连接上的两个写者。

★ **一个 server 两个子进程，比一次慢的启动更糟。** 第二个进程会对着一个用户只配过
  一次的 server 握着第二根管道，而两者报出的工具清单可能不同 ——
  于是模型看到的工具就取决于哪个进程答的。

★ **等待的界限就是这次启动本来就有的那个期限**（§5.2）：连接、初始化与取工具清单
  是一个带一个期限的操作。等得更久的调用方拿到的是这次启动报出来的失败 ——
  与它自己去启动这个 server 会拿到的是同一个失败。

## 7. 闸门与档位

### 7.1 档位照旧适用

一个叫 `mcp__github__create_issue` 的工具被查起来与其它工具一模一样：

```
Policy.TierOf("mcp__github__create_issue")
```

除非 server 的文件在 `allows:` 里点了它的名（§7.3），否则它落进 `ask` 档 ——
而这正是每一个工具都遵守的同一条规则。闸门代码一个字都不用改：它读档位，然后动作（§1.3）。
★ 命名空间的缺省是 `ask`，而它是档位查找里的一个**兜底分支**，不是一个特例：
  `Policy.TierOf` 对任何 `mcp__` 之下、而文件没有允许的名字都答 `ask`
  （`agentkit/build/policy.go:55`）。

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
`mcps/<server>.yaml` 里一个**空的** `allows:` 是一行字，
意思是「这个 server 的每个工具都要先点头」（§7.3）。

### 7.3 档位住在工具旁边

★ **一个工具的档位写在它自己的文件里，用那个文件自己的词儿。**
现在**没有全局清单**了（§4）：`ask` 档搬到了它所说的那个工具旁边，
全局 `deny` 也跟着走了 —— 「不被暴露」恰好在两个地方说：
一个文件的清单，以及某个 agent 的 `disabled_tools`：

```yaml
# .openheron/mcps/github.yaml
#
# ★ The default is ask, so an empty list means "every tool of this server asks".
#   A trusted server names its exceptions:
allows: [read_issue, list_issues]
```

★ **为什么要用文件自己的词儿。** 档位是关于**一个工具**的陈述，
  所以它属于那个工具的文件 —— 而在那里它可以用那个文件已经在用的名字。
  原来那个中心文件必须点名内部工具名（`request_post`），而工具自己的文件说的是 `post`；
  ★ 一个工具两套词儿，就是一处等着发生的错配，而一个拼错的名字
  在工具的行为与文件所说的不一致之前，一直是看不见的。

| 位置 | 词儿 | 决定什么 |
|---|---|---|
| 某个文件的 `tools` / `allows` | **那个文件自己的**名字（`create_issue`） | 从 server 提供的工具里暴露哪些，以及其中哪些不用点头 |
| 某个 agent 的 `disabled_tools` | 对外的名字 | 这个 agent 拿不到这些工具 |

★ **顺序**：先提供这个工具的文件，然后是 agent 的收窄 ——
  收窄只把这个工具从那个 agent 那里摘掉，别的一概不管。

★ **文件自己没有 `deny`，而这是刻意的。** 「不被暴露」本来就是清单的活。
  ★ 而**全局 `deny` 还在** —— 这份设计是照着配置设计去掉它的那份提案写的
  （配置设计 §6.3），而 MCP 这个功能没有把它带走（§4.2）。
  这三层不是彼此重复：全局清单说**永不**，文件说**要暴露 server 的哪些工具**，
  agent 说**这个 agent 拿不到什么**。

★ **装配出来的策略形状不变。** `build.Policy` 与 `TierOf`
（`agentkit/build/policy.go:49`）仍然是收一个名字、答一个档位；变的是条目的来源：
  provider 各自从自己文件里的声明、用它自己的词儿装配出来。⇒ 闸门、各张表、
  API 的 `EffectiveTools` 视图与前端都不必学一个新概念。

★ **装配发生在 `agentkit/build`，不在 `agentkit/spec`。** 只有 provider 知道
  自己文件的词儿，以及它背后的对外名字；而 `agentkit/spec` 不得 import `kb`
  （那条铁律），也不得 import eino（文件系统工具的名字住在那里）。

★ **缺省是 `ask` —— 对 server 如此，对别的一切也如此。** 内置那些组不再是例外：
  代码里带着一份它知道的只读工具的 allow 清单，其余一切都要先点头。
  而一个 server 的工具是**运行期才发现**的：openheron 无从知道 `delete_repo` 做什么，
  而在 stdio 之下，那个 server 是带着 agent 自己权限的**子进程**（§7.2）。
  ⇒ 猜错的代价是不对称的 —— 多一次点击，换掉的是一次没人看见的破坏性调用。
  用户信得过的 server 点名它的例外，**一次**就够了：

```yaml
allows: [read_issue, list_issues]
```

★ **档位表是从配置装配出来的，不是从 server 装配出来的。** `allows:` 清单在文件里，
  而清单里一个名字的对外名字，是用那个文件自己的词儿合成的 —— 所以
  `mcp__github__read_issue → allow` 这一条**在 server 还没起来之前**就能装配出来
  （`agentkit/build/mcptools.go:172`）。
  ⇒ 一个工具的档位任何时候都答得出来，包括 server 够不着的时候（§6.1），
  而闸门那次查找从不为等一条连接而停下。

★ **缺省盖住文件没有点名的那些名字。** 既不在文件的 `allows:` 里、也不在任何别处的
  工具，是 `ask` —— 所以 server 明天加的工具，在第一天就要点头，配置一个字都不用动。
  ★ 这与一个空的 `allows:` 用一行说出的是同一条规则（§4）；
  非空的那份就是同一条规则挖掉了几个例外。

★ **档位改动下一轮就生效。** `mcps/` 是**热**分区，所以档位并不特殊：
  策略表随快照一起重建，闸门从下一轮起读到新档位。重载唯一要顾对的是
  **客户端**：声明变了的那台服务器拿到新的客户端（§5.4）。

★ **文件系统工具也有自己的文件** —— `tools/filesystem.yaml` —— 这样它们的档位
  写在它们旁边，这个集合也可以被收窄。见 `docs/config-design.md` §6.6。

### 7.4 run ledger 说什么

★ 一次 MCP 工具调用就是一次普通工具调用，所以它本来就跟别的调用一样进 transcript、
  进事件流。**ledger** 多出来的是 server：一个 run 的行记下这次 run 碰过哪些 MCP server
  （`storage/runledger.go:168`），于是事后复盘能回答「这是哪个 server、
  是不是它把这次 run 弄失败的」，而不必读完整个 transcript。
  ★ 这些名字是从这次 run 用过的带命名空间的工具名里读出来的
  （`agentkit/run/runrow.go:84`），所以那一行不需要单独跟踪工具。

★ ledger 仍然是 **run** 的账本：它装的是这个 run 的状态迁移，不是一次调用一行。
  那一次次的调用在 transcript 里；那一行说的是哪些 server 卷了进来。

## 8. 沙箱 Kit

> 状态：**一份提案。** Kit 还没有打包 MCP 服务器 —— §8 里没有一行在树上。
> MCP 功能本身是已实现的（§1 到 §7、§9 到 §11）；缺的是打包的那一半，
> 而下面这些理由说的是为什么那一半应该长成这个样子。

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

★ **这条路径的 openheron 那一半已经实现；Kit 那一半还没有。**
  子进程继承本进程的环境（§5.5，`agentkit/build/mcpmanager.go:458`），
  所以运行时放进容器变量里的那个哨兵，不需要配置里任何字段点它的名就能到达
  MCP 服务器 —— 那一半在树上。还没有的是把哨兵放进去的那个 Kit：
  下面的打包仍然是一份提案。

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
| `agentkit/spec/decl` | **声明**：`MCPServerCfg`、命名空间常量，以及 agent 的 `MCPServers` 字段 | 任何行为 —— 它是纯声明层（`AGENTS.md` 的依赖铁律） |
| `agentkit/spec` | **加载器**：读 `mcps/<server>.yaml`，校验文件名与字段，展开 `${ENV}` / `@file:` | MCP 客户端、eino、`kb` —— 它不得 import 它们 |
| `agentkit/build/mcptools.go` | **provider** 与**包装层**：那份计划（`mcpPlan`）、per-server 互斥锁、输出封顶 | 加载器的文件遍历 —— 它拿到的是声明 |
| `agentkit/build/mcpmanager.go` | **管理者**：一个 server 一个客户端、单飞启动（§6.8）、懒启动、崩掉的子进程重启、工具清单重新列举、服务器变了与进程退出时的拆解 | 命名与档位 —— 那属于 provider |
| `agentkit/build/tools.go` | **注册表**：`toolProvider`、`defaultToolRegistry`、保留名 | MCP 特有的东西 —— 它只认那个接口（§1.2） |
| `agentkit/build/policy.go` | **档位查找**：`Policy.TierOf`、`Table.Blocked` | 一个工具来自哪个文件 —— 它只看得到名字 |
| `agentkit/build/mcpstatus.go` | **投影**：管理者的状态、那份计划、边界的判定 —— 每个 server 一行（§11.2） | 任何自己的判断 —— 它只读，不评判 |

★ **方向是单向的**：`spec` → `build`。声明层从不 import 装配层，
  而装配层也从不自己去遍历配置目录。

★ **计划是一个自由函数，不是一个方法。** 命名与档位映射有**两个**调用方 ——
  把它们变成 eino 工具的 provider，以及报出它们的投影（§11.2）。
  ⇒ 它们住在 `mcpPlan` 里：一个以文件的声明与 server 自己的工具清单为参数的函数，
  两个调用方都调它。★ provider 上的方法会让投影去够 provider 的实例，
  而映射的第二份拷贝就是关于工具名字与工具档位的第二份真相。

### 9.2 那些类型

| 类型 | 在哪 | 是什么 |
|---|---|---|
| `decl.MCPServerCfg` | `agentkit/spec/decl/decl.go:454` | 一个服务器的声明：传输方式、命令、环境、那几个档位键、白名单、两个超时 |
| `decl.MCPFiles` | `agentkit/spec/decl/decl.go:502` | 这个分区的持有者：`map[string]*MCPServerCfg`，按服务器名做键 |
| `decl.Agent.MCPServers` | `agentkit/spec/decl/decl.go:114` | agent 自己的服务器清单：★ 一个**指针**，所以键缺失意味着全部已配置的服务器，`[]` 意味着一个都没有 —— 与 `Skills` 已经是的那个形状一样 |
| `decl.SubAgentSpec.MCPServers` | `agentkit/spec/decl/decl.go:629` | 子 agent 的同一份清单，就在它自己的 `DisabledTools` 与 `Skills` 旁边（§5.3） |
| `spec.Config.MCPs` | `agentkit/spec/config.go:119` | 加载器把它们放在这儿 |
| `mcpToolProvider` | `agentkit/build/mcptools.go:193` | `toolProvider` 的那个实现（§1.2） |
| `mcpManager` | `agentkit/build/mcpmanager.go:42` | 客户端的属主：每个 server 一个，按服务器名做键，跨快照活着（§6.6） |
| `mcpTool` | `agentkit/build/mcptools.go:311` | 一个工具：eino 工具加上它归属的那个服务器 |
| `mcpPlan` | `agentkit/build/mcptools.go:67` | 那份共享的映射：文件的声明加上 server 自己的工具清单 → 对外的名字、档位，以及被跳过的那些（§9.1） |
| `serialTool` | `agentkit/build/mcptools.go:334` | 把同一个 stdio 服务器的调用串行化起来的那个包装层（§6.5） |
| `build.Policy` | `agentkit/build/policy.go:23` | 装配出来的表：名字 → 档位。**形状不变** —— 变的只是条目从哪儿来（§7.3） |
| `MCPServerView` | `agentkit/build/mcpstatus.go:43` | 一个 server 的一行：名字、传输方式、状态、错误、工具 |
| `run.MCPServers` | `storage/runledger.go:168` | 一次 run 碰过的那些服务器（§7.4） |

### 9.3 设计的哪一块落在了哪一行代码

| 设计 | 代码 |
|---|---|
| 命名空间（§3） | `mcpPlan`（`agentkit/build/mcptools.go:67`）—— 唯一一处拼出 `mcp__<server>__<tool>` 的地方（`agentkit/build/mcptools.go:140`） |
| 跳过并警告（§3.2） | 同一个函数 —— 名字在工具被留下之前先检查一遍（`agentkit/build/mcptools.go:88`） |
| 文件自己的档位（§7.3） | 同一个函数 —— 文件里的名字被映射到对外的那些，而那张表是从配置装配出来的（`agentkit/build/mcptools.go:172`） |
| 串行化（§6.5） | `serialTool`（`agentkit/build/mcptools.go:334`）—— 一个包装层，与 `nettools.go` 里的 `cappedTool` 同一个形状 |
| 输出封顶（§6.4） | 同一族包装层 —— 组件的 `ToolCallResultHandler`（`agentkit/build/mcptools.go:371`） |
| 客户端的生命（§6.2、§6.6） | 管理者 —— `Start` / `Initialize` / `ListTools`，懒启动、崩掉的子进程重启（`agentkit/build/mcpmanager.go:290`），以及进程退出时的拆解（`agentkit/build/mcpmanager.go:150`） |
| 热重载（§5.4） | `agentkit/spec/reload.go` —— 这个分区是热的（`agentkit/spec/config.go:72`），而声明变了就会换掉客户端（`agentkit/build/mcpmanager.go:229`） |
| 工具清单变了（§6.6） | 管理者 —— 重新列举（`agentkit/build/mcpmanager.go:118`），以及推进代际并重建工具（`agentkit/build/builder.go:105`） |
| 一次启动，一个客户端（§6.8） | 管理者 —— 单飞启动（`agentkit/build/mcpmanager.go:215`） |
| 一次 run 碰过的 server（§7.4） | `agentkit/run/runrow.go:84` 与 `storage/runledger.go:168` —— run 的那一行点名它们 |
| 示例与编辑器的模板（§5.6） | `initcfg/templates/mcps/example.yaml`（init 把它写成 `mcps/example.yaml` 的那个文件，`initcfg/initcfg.go:169`）、`agentkit/spec/write.go:217`（`mcpServerTemplate`，编辑器自己那份，经由 `spec.Template`） |
| 一个 agent 加载哪些服务器（§5.3） | `agentkit/spec/decl:114`（那个字段）→ `agentkit/spec/load.go:529`（那个并集）→ `agentkit/build/mcpmanager.go`（那次启动） |
| agent 的收窄（§11.3） | `agentkit/build/policy.go:144` —— 表里那些被拒的命名空间前缀 |
| `/mcps` 那个视图（§11.2） | `agentkit/build/mcpstatus.go:64` —— 管理者的状态、那份计划、边界的判定，合成一处投影 |

★ **有一行搬了家。** 改动清单（§10.1）本来要把带命名空间的 MCP 名字放进
  `spec.Config.EnabledToolNames`；它们进了**装配**层 —— 工具名的全域
  （`agentkit/build/introspect.go:127`、`:144`）—— 因为名字的合成属于装配层，
  而 `agentkit/spec` 不得 import 它。★ 可观察到的行为是一样的：
  API 的 `EffectiveTools` 会报出那些 MCP 名字（`agentkit/build/introspect.go:49`），
  而且它是仅凭配置报出来的，在服务器起来之前就报。

★ **一个函数干三件事**：命名空间、跳过并警告、档位映射都住在
  `mcpPlan` 里，因为它们要的都是同样两样输入 ——
  服务器自己的工具清单，与描述它的那个文件。拆开它们就意味着
  把同样这两样东西到处传。

★ **而且它是一个自由函数，因为有两个调用方。** provider 把这份计划变成 eino 工具；
  投影把它报出来（§11.2）。★ 映射的第二份拷贝就是关于工具名字与工具档位的第二份
  真相 —— 而整个投影的卖点恰恰是它不是第二份真相。

## 10. 改动清单

下面这些**全都落地了**。这一节是那一轮实现做过什么的记录：设计的每一块去了哪里、
测试覆盖了什么、以及哪些文档跟着动了。

### 10.1 代码

| # | 文件 | 改动 | 落在哪儿 |
|---|---|---|---|
| 1 | `go.mod` | 加 `github.com/cloudwego/eino-ext/components/tool/mcp` 与 `github.com/mark3labs/mcp-go` | **已完成** —— `go.mod:12`、`go.mod:18` |
| 2 | `agentkit/spec/config.go` | `DirMCP = "mcps"` 与一条 `Categories` 条目，热更新 | **已完成** —— `agentkit/spec/config.go:26`、`:72` |
| 3 | `agentkit/spec/decl/decl.go` | `MCPServerCfg` 声明结构体、`MCPFiles` 持有者、命名空间常量；`call_timeout`；★ agent 的 `MCPServers` 字段，agent 与子 agent 各有一份（§5.3） | **已完成** —— `agentkit/spec/decl/decl.go:454`、`:502`、`:336`、`:345`、`:353`、`:114`、`:629` |
| 4 | `agentkit/spec/load.go` | `mcps/<server>.yaml` 的加载器，带 §3.1 与 §5.2 的文件名与字段校验，以及 §5.7 的那些加载期硬错误 | **已完成** —— `agentkit/spec/load.go:256`、`:499` |
| 5 | `agentkit/spec/defaults.go` | `EnabledToolNames` 加上带命名空间的 MCP 工具；一个服务器的缺省档位（`ask`） | ★ **搬了家** —— 那些名字进了**装配**层的工具名全域（`agentkit/build/introspect.go:127`），因为名字的合成属于那一层，而 `agentkit/spec` 不得 import 它；缺省档位是命名空间的兜底（`agentkit/build/policy.go:55`） |
| 6 | `agentkit/spec/write.go` | 新建 `mcps/` 文件的脚手架 | **已完成** —— `agentkit/spec/write.go:189`，服务器名校验在 `:107` |
| 7 | `agentkit/build/tools.go` | `mcpToolProvider`，注册在注册表里 | **已完成** —— `agentkit/build/tools.go:41`，provider 在 `agentkit/build/mcptools.go:193` |
| 8 | `agentkit/build/policy.go` | `Blocked` 的前缀匹配器，**以及**档位的**装配**：各 provider 从自己文件里的声明贡献（对外名字 → 档位）（§7.3） | **已完成** —— `agentkit/build/policy.go:108`、`:161`、`:34` |
| 9 | `api/v2_settings.go` | `mcps/` 的可编辑分区 | ★ **通用** —— 那份清单走的是 `spec.Categories`（`api/v2_settings.go:135`），而 `mcps/` 是一个可编辑分类（`agentkit/spec/config.go:72`），所以不需要专门的一段 |
| 10 | `agentkit/build/nettools.go` | 输出封顶，套到 MCP 批次上 | ★ **在别处** —— 封顶落在组件的结果处理器里（`agentkit/build/mcptools.go:371`），用的是同一个常量（`agentkit/build/nettools.go:26`） |
| 11 | `agentkit/build/mcptools.go` | `mcpPlan` —— 那份共享的命名与档位映射（§9.1），把 stdio 调用串行化的 per-server 互斥锁（§6.5），以及子进程的环境装配（§5.5） | **已完成** —— `agentkit/build/mcptools.go:67`、`:334`；★ 环境装配在管理者里（`agentkit/build/mcpmanager.go:458`） |
| 12 | `agentkit/build/mcpmanager.go` | 管理者：一个 server 一个客户端、**单飞启动**（§6.8）、**只懒启动某个 agent 要的**那些服务器（§5.3）、崩了的子进程重启、推进代际的工具清单重新列举，以及快照换代时的拆解（§6.6） | **已完成** —— `agentkit/build/mcpmanager.go:42`；★ 拆解发生在**声明**上（`:229`）与进程退出时（`:150`），而不是快照换代时（§6.6） |
| 13 | `agentkit/run/runrow.go` | run 的那一行记下这次 run 碰过哪些 MCP server（§7.4） | **已完成** —— `agentkit/run/runrow.go:84`、`storage/runledger.go:168` |
| 14 | `initcfg/templates/` 与 `spec.Template` | `mcps/example.yaml` 这个示例，以及新建 `mcps/` 文件时编辑器用的模板 —— ★ 编辑器读不到 init 包里那个文件（编辑器由 API 供给，而 API 不能 import `initcfg`，那是循环），所以它留着自己的**一份拷贝**，并由**一条测试把两者钉住**（§5.6） | **已完成** —— `initcfg/templates/mcps/example.yaml`、`initcfg/initcfg.go:169`、`agentkit/spec/write.go:217`、`initcfg/initcfg_test.go:656` |
| 15 | `api/commands.go` | `mcps` 这个 action 命令与 `commandActionOpenMCPs`（§11.1） | **已完成** —— `api/commands.go:31`、`:61` |
| 16 | `api/v2_mcps.go` | `GET /v2/mcps` —— 那个视图（§11.4） | **已完成** —— `api/v2_mcps.go:74`，路由在 `api/v2_files.go:63` |
| 17 | `api/v2_introspect.go` | `MCPSource` 接口与 `SetMCPSource`：API 层去问 builder，它不直接读管理者（§11.2）；★ 以及配置面视图里那些已配置的服务器（§11.4） | ★ **拆开了** —— 接口与 `SetMCPSource` 在 `api/v2_mcps.go:18`、`:41`；配置面视图列出那些服务器在 `api/v2_introspect.go:388` |
| 18 | `agentkit/build/mcpstatus.go` | `Builder.MCPServers(agentID)` —— 管理者状态的投影（§11.2） | **已完成** —— `agentkit/build/mcpstatus.go:64` |
| 19 | `agentkit/build/policy.go` | 表里 agent 的 `mcps:` 清单：那些被拒的命名空间前缀，以及跟着它们的理由（§11.3） | **已完成** —— `agentkit/build/policy.go:144`，接在 `agentkit/build/topology.go:70` |
| 20 | `main.go` | 那根接线，紧挨着 `SetToolsSource`（`main.go:278`）（§11.2） | **已完成** —— `main.go:247`（那个 bump）、`:279`（`SetMCPSource`）、`:384`（那个 close） |
| 21 | `web/src/lib/api.ts` | `open_mcps` 这个 action 与 `listMCPServers`（§11.5） | **已完成** —— `web/src/lib/api.ts:136`、`:329` |
| 22 | `web/src/components/mcp-panel.tsx` | 那个面板（§11.5） | **已完成** |
| 23 | `web/src/components/composer.tsx` | 打开面板的那个 action 分支（§11.5） | **已完成** —— `web/src/components/composer.tsx:282` |
| 24 | `agentkit/build/builder.go` 与 `agentkit/build/manager.go` | 这一轮的第一步：问管理者某个 server 的工具清单有没有变，变了就取新的快照；以及推进代际的那个 **bump** 回调（§6.6） | **已完成** —— `agentkit/build/builder.go:67`、`:105`；★ 配置管理器仍然是只读的（`agentkit/build/manager.go:12`） |

★ **有三行搬了家，还有一行没有照写落地。** 工具名全域搬到了装配层（第 5 行），
  输出封顶搬进了组件的结果处理器（第 10 行），API 那个 MCP 接口搬进了
  `api/v2_mcps.go`（第 17 行）。快照换代时的拆解（第 12 行）没有发生：
  管理者跨快照拥有那些客户端，换掉客户端的是**声明**变了（§6.6）。

### 10.2 测试

| 领域 | 要证明什么 | 测试 |
|---|---|---|
| 命名 | 一个提供 `ls` 的服务器产出 `mcp__<server>__ls` | `agentkit/build/mcptools_test.go` `TestAnMCPNameIsTheServersOwnNameUnderTheNamespace` |
| 命名 | 模型 provider 不收的那些字符变成 `_` | `agentkit/build/mcptools_test.go` `TestAnMCPNameReplacesTheCharactersTheModelRefuses` |
| 命名 | server 自己的大小写被保留 | `agentkit/build/mcptools_test.go` `TestAnMCPNameKeepsTheServersOwnCase` |
| 命名 | 64 字符的边界，以及越界之后的「跳过 + 警告」这条路 | `agentkit/build/mcptools_test.go` `TestAnMCPNameAtTheBoundaryIsKept`、`TestAnMCPNameOverTheBoundaryIsSkipped` |
| 命名 | 同一服务器里两个规整后同名的工具 —— 第二个被跳过 | `agentkit/build/mcptools_test.go` `TestTwoToolsThatSanitizeAlikeLeaveTheSecondOneSkipped` |
| 匹配 | 文件的 `tools:` 列的是 server **自己**的名字，而缺失意味着全部 | `agentkit/build/mcptools_test.go` `TestTheFileAsksForItsOwnToolsAndNotTheExposedOnes`、`TestAnAbsentToolListMeansEveryTool` |
| 匹配 | 一条前缀规则命中一个 server 的工具，且只命中它的 | `agentkit/build/mcptools_test.go` `TestAPrefixRuleReachesTheToolsOfOneServerOnly` |
| 匹配 | `mcp__*` 够到每一个 MCP 工具，而精确条目仍然摘掉一个工具 | `agentkit/build/mcptools_test.go` `TestAWildcardPrefixReachesEveryMCPTool`、`TestAnExactEntryStillRemovesTheTool` |
| 匹配 | `mcp*foo` 与单独的 `*` 是加载期错误 | `agentkit/spec/mcp_test.go` `TestAWildcardThatIsNotAPrefixPatternIsALoadTimeError`、`TestALoneWildcardIsALoadTimeError`、`TestATrailingStarIsAPrefixPattern` |
| 档位 | 一切都先点头，而文件点名它的例外 | `agentkit/build/mcptools_test.go` `TestEverythingAsksAndTheFileNamesTheExceptions` |
| 档位 | 一个空的 `allows:` 覆盖 server **还没提供**的工具 | `agentkit/build/mcptools_test.go` `TestAnEmptyAllowsListCoversTheToolsTheServerHasNotOfferedYet` |
| 档位 | server 够不着的时候，一个工具的档位也答得出来 | `agentkit/build/mcptools_test.go` `TestTheTierIsAnswerableWhileTheServerIsUnreachable` |
| 档位 | 落在全局 `deny` 里的名字仍然是 denied | `agentkit/build/mcptools_test.go` `TestAnMCPNameInTheGlobalDenyStaysDenied` |
| 档位 | 文件里的 `allows:` 以合成后的名字到达闸门 | `agentkit/build/mcplive_test.go` `TestTheProviderMountsTheToolsOfARealServer` |
| 服务器 | `mcps:` 缺失意味着全部已配置的服务器，`[]` 意味着一个都没有 | `agentkit/build/mcptools_test.go` `TestAnAbsentServerListMeansEveryConfiguredServer`、`TestAnEmptyServerListDeniesEveryConfiguredServer`；`agentkit/spec/mcp_test.go` `TestAnAbsentServerListMeansEveryConfiguredServer`、`TestAnEmptyServerListMeansNoServerAtAll` |
| 服务器 | 子 agent 自己的 `mcps:` 清单是从子 agent 读的 | `agentkit/spec/mcp_test.go` `TestASubAgentsOwnListIsReadFromTheSubAgent` |
| 收窄 | 表拒的是命名空间**前缀**，而 `mcp__github__` 够不到 `mcp__github_x__` | `agentkit/build/mcptools_test.go` `TestTheServerNarrowingDeniesTheNamespacePrefixOnly` |
| 收窄 | 服务器的收窄先于工具规则被报出来 | `agentkit/build/mcptools_test.go` `TestTheServerNarrowingIsReportedBeforeTheToolRule` |
| 加载 | 不认识的 `transport` 是加载期错误，而且不退回 stdio | `agentkit/spec/mcp_test.go` `TestAnUnknownTransportIsALoadTimeError` |
| 加载 | stdio 服务器要 `command`，http 服务器要 `url` | `agentkit/spec/mcp_test.go` `TestAStdioServerNeedsACommand`、`TestAnHTTPServerNeedsAURL` |
| 加载 | http 服务器上的 `env:` 是加载期错误，哪怕它是空的 | `agentkit/spec/mcp_test.go` `TestAnHTTPServerMayNotDeclareAnEnvironment`、`TestAnEmptyEnvironmentOnAnHTTPServerIsStillRefused` |
| 加载 | 不认识的键被拒绝 | `agentkit/spec/mcp_test.go` `TestAnUnknownKeyIsRefused` |
| 加载 | 服务器名就是文件名，而且它必须读得回来 | `agentkit/spec/mcp_test.go` `TestTheMCPFileNameIsTheServerName`、`TestTheServerNameMustBeReadableBack` |
| 加载 | agent 的 `mcps:` 点名一个不存在的服务器是加载期错误 | `agentkit/spec/mcp_test.go` `TestAnAgentNamingAServerThatDoesNotExistIsALoadTimeError` |
| 加载 | 两个超时各有缺省，而写坏的那个降级并被记下来 | `agentkit/spec/mcp_test.go` `TestTheTimeoutsHaveDefaults`、`TestABadTimeoutDegradesAndIsNoticed` |
| 环境 | `env:` 里未设的 `${ENV}` 进 `deferred`，不致命；设了的会被展开 | `agentkit/spec/mcp_test.go` `TestAnUnsetVariableInTheEnvironmentIsDeferredAndNotFatal`、`TestASetVariableInTheEnvironmentIsExpanded` |
| 环境 | 子进程看得见 openheron 的环境，`env:` 覆盖同名项 | `agentkit/build/mcpmanager_test.go` `TestTheChildEnvironmentIsTheProcessesOwnWithTheFileOnTop` |
| 命令 | 一个不在 PATH 上的 `command` 只是一条警告，而工作区永远不参与解析 | `agentkit/build/mcpmanager_test.go` `TestTheCommandIsNeverResolvedAgainstTheProjectRoot` |
| provider | 一个够不着的服务器说得出问题，而其它服务器照活 | `agentkit/build/mcpmanager_test.go` `TestAnUnreachableServerSaysSoAndKeepsTheOtherServersAlive` |
| provider | 一个**真实**服务器的工具以命名空间挂上来，而且一次调用到达服务器并带回它的答案 | `agentkit/build/mcplive_test.go` `TestTheProviderMountsTheToolsOfARealServer`、`TestAMCPtoolCallReachesTheServer` |
| 生命周期 | 声明变了就换掉客户端 | `agentkit/build/mcpmanager_test.go` `TestAChangedDeclarationReplacesTheClient` |
| 管理者 | 崩掉的 stdio 子进程在下一次调用时被重新拉起 | `agentkit/build/mcpmanager_test.go` `TestACrashedChildIsStartedAgainOnTheNextCall` |
| 管理者 | 工具清单变了会被 refresh 报出来，而新鲜的那份不会被重新列举 | `agentkit/build/mcpmanager_test.go` `TestAChangedToolListIsReportedByTheRefresh`、`TestAFreshListingIsNotReListed` |
| 管理者 | 两个调用方要同一个 server，拿到的是**一个**客户端 | `agentkit/build/mcpmanager_test.go` `TestTwoCallersAskingForTheSameServerGetOneClient` |
| 账本 | 一个 run 的那一行点名它碰过的 MCP server，而名字是从带命名空间的工具名里读出来的 | `agentkit/run/runrow_mcp_test.go` `TestARunRowNamesTheMCPServersItTouched`、`TestTheServersAreReadOutOfTheNamespacedToolNames` |
| 编辑器 | 配置编辑器能在 `mcps/` 下面新建文件，并拒绝读不回来的服务器名 | `agentkit/spec/mcp_test.go` `TestTheEditorAcceptsAFileUnderMCPS`、`TestTheEditorRefusesAServerNameThatCouldNotBeReadBack` |
| 命令 | `/mcps` 是一个 action，而它永远不会以 prompt 的形式到达模型 | `api/v2_mcps_test.go` `TestTheMCPCommandIsAnAction`、`TestTheMCPCommandNeverReachesTheModel` |
| 视图 | agent 参数可省略，而一个不存在的 agent 是 404 | `api/v2_mcps_test.go` `TestV2MCPServersReportsTheServersOfTheAgent`、`TestV2MCPServersTakesTheAgentParameter`、`TestV2MCPServersRefusesAnUnknownAgent` |
| 视图 | 配置面视图列出已配置的服务器 | `api/v2_mcps_test.go` `TestV2ConfigListsTheConfiguredServers` |
| 视图 | 每个已配置的服务器恰好出现一次，而 agent 不用的那个 server 是 `unused` 且没有工具行 | `agentkit/build/mcpstatus_test.go` `TestEveryConfiguredServerAppearsExactlyOnce`、`TestAServerTheAgentDoesNotUseIsUnusedAndHasNoToolRows` |
| 视图 | 一个够不着的服务器报出错误并留住它上一次成功的清单 | `agentkit/build/mcpstatus_test.go` `TestAnUnreachableServerReportsTheErrorAndKeepsItsLastListing` |
| 视图 | server 不在清单里时理由是 `mcps`，工具被禁时是 `disabled_tools` | `agentkit/build/mcpstatus_test.go` `TestTheServerNarrowingIsTheReasonTheAgentsViewReports`、`TestTheReasonIsTheToolRuleWhenTheServerIsThere` |
| 视图 | 面板去问管理者，而管理者会启动那个服务器；档位就是闸门自己的那个 | `agentkit/build/mcpstatus_test.go` `TestThePanelAsksTheManagerAndTheManagerStartsTheServer`、`TestThePanelShowsTheTierTheGateWouldUse` |
| 面板 | 面板把两个名字与档位都显示出来，也照实说它够不着什么 | `web/test/e2e/mcps.spec.ts` |
| 面板 | 空状态说的是没有配置任何服务器 | `web/test/e2e/mcps.spec.ts` |
| 面板 | 面板每次打开都重新读，而 **Refresh** 会再读一次 | `web/test/e2e/mcps.spec.ts` |
| 面板 | `/mcps` 永远到不了模型，也不会开一个会话 | `web/test/e2e/mcps.spec.ts` |

★ **这份设计要的三个用例还没有测试。** 同一个 stdio 服务器的调用被串行化（§6.5）、
  `call_timeout` 的截止时间，以及「文件的 `tools:` 点名而 server 没有的名字」那条
  警告（§6.1）。★ 前两者的实现是在的
  （`agentkit/build/mcptools.go:334`、`agentkit/build/mcptools.go:240`）；
  缺的是能证明它的那一个测试。

### 10.3 文档

| 文档 | 改了什么 |
|---|---|
| `docs/architecture.md` | 工具装配路径加上 MCP provider；`mcps/` 分区与 agent 的 `mcps:` 键进入配置表 |
| `docs/config-design.md` | `mcps/` 分区（§6.7）与 agent 的 `disabled_tools` 里的前缀模式（§6.3）变成**已实现**；全局 `deny` 仍然是提案 |
| `docs/user-manual.md` | 怎么配一个服务器、界面上看到的名字长什么样，以及 `/mcps` 命令显示什么（§11.5） |
| `docs/developer-manual.md` | 「加一个分区」的配方加上 `mcps/` 这一例 |
| `README.md` | HTTP 参考加上 `GET /v2/mcps` |
| `docs/mcp-design.md` | 本文档 —— 状态头、§1 与 §10 被改写成描述代码现状 |

## 11. `/mcps` 命令：界面里的 MCP 服务器

★ 界面必须回答一个日志回答不了的问题：**这个 agent 有哪些 MCP 服务器，
  它们起来了吗，跑起来的时候会让我付出什么？** 这三样管理者都知道。
  面板自己不许下一个判断。

### 11.1 它是一个 action，不是一个 prompt

`/mcps` 在服务端的命令表（`api/commands.go:61`）里注册为一个 **action**：

```go
{
    Name: "mcps", Description: "Show the MCP servers of this agent",
    Kind: commandKindAction, Action: commandActionOpenMCPs,
},
```

★ **是 action，不是 prompt**，理由与 `/compact` 是 action 一样（`api/commands.go:69`）：
  prompt 会去问**模型**那些服务器的事，而模型的回答会是关于一个**运行时**掌握的事实
  的第二份、且有损的真相。状态是被读出来的，不是被生成出来的。

★ 这个 action 打开一个面板；它不往对话记录里写一条消息。状态播报不是一轮，
  而一段对话中间夹一个 `/mcps`，读起来就像 agent 说过什么话。

### 11.2 那条规则

> **`/mcps` 去问管理者：这个 agent 能从它的服务器上拿到什么。**

一句话，面板的每一行都由它推出：

| 问题 | 答案来自哪儿 |
|---|---|
| 服务器起来了吗？ | 管理者的客户端（§6.6） |
| 它提供了什么？ | 管理者的清单（§6.6） |
| 这个工具叫什么？ | 那份计划 —— `mcpPlan`（§9.3） |
| 它会问我吗？ | `Policy.TierOf` —— 闸门自己那次查找（§1.3） |
| 这个 agent 能拿到它吗？ | agent 的表（§11.3） |

★ 这五个答案都出自**同一个**函数 `Builder.MCPServers`
  （`agentkit/build/mcpstatus.go:64`），而 API 层只转发它返回的东西
  （`api/v2_mcps.go:74`）。

★ **面板什么都不推导。** 它显示的每一个值，运行时早就有了。
  一个重新判断任何东西的面板就成了一份第二真相，而最先坏掉的正是档位：
  面板会说 `ask`，而闸门说 `allow`。

★ **工具那几行就是 agents 视图已经报出来的判定。** 每个工具的判定由同一个函数算出
  （`agentkit/build/introspect.go:113`），用的是同一条边界，所以 `/mcps` 与
  `/v2/agents/<id>` 不可能对同一个工具报出两个不同的档位。
  ★ 唯一的差别是**名字的全集**：agents 视图走的是观察到的工具，
  面板走的是管理者的清单 —— 所以一个倒下的服务器仍然能显示它提供过什么。

★ **一问就会启动服务器。** 管理者在被问到时才启动一个服务器，而面板就是那个问的人。
  所以第一轮之前敲 `/mcps`，会启动**这个 agent** 要用的那些服务器 ——
  与第一轮本来会为这个 agent 做的那次启动一模一样，不多不少。
  ★ 另一条路（一个严格只读、只报「还没启动」的面板）回答不了用户敲这条命令时
  真正想问的问题：「我的服务器坏了吗？」—— 而答案会变成「跑一轮就知道了」。

★ **面板的那一问走的是同一个单飞启动**（§6.8）。面板与一轮对话可能同一刻开口，
  而它们拿到的是**一个**客户端 —— ★ 两个调用方抢进那次启动，就是一个 server 两个
  子进程，而模型看到的工具就取决于哪个答的。

### 11.3 agent 的收窄，以及它落在哪里

★ §5.3 说 agent 的 `mcps:` 清单决定谁拿到一个服务器，§6.6 说没有任何 agent 点名的
  server 永远不会被启动。两者都没说这个收窄**发生在哪儿**，而这个答案不是白给的：
  工具集是**整个快照一份**（`agentkit/build/snapshot.go:40-45` ——
  `buildHeavy` 跑在一个 `sync.Once` 底下），每个 agent 共享它。

⇒ **收窄发生在 agent 的表里**，也就是 `disabled_tools` 已经在收窄工具的那个地方
  （`agentkit/build/gate.go:33` —— `Table.Filter` 在 agent 开跑之前就把工具从
  `runCtx.Tools` 里摘掉，所以模型从来看不见它们）。

表拿到这个 agent 的服务器清单，而这个 agent 不用的服务器，被拒的方式是
**按它的命名空间前缀**：

| 这个 agent 的 `mcps:` | 表拒掉什么 |
|---|---|
| 缺失 | 什么都不拒 —— 全部已配置的服务器 |
| `[github]` | 除 `github` 以外每个已配置服务器的前缀 |
| `[]` | 每个已配置服务器的前缀 |

★ **是前缀，不是把名字拆开。** §3.1 说合成后的名字从不被拆回两半；
  表匹配的是 `mcp__<server>__` 这个**前缀字符串**，而它是从配置里已经知道的。
  ★ 而且服务器名里不许有连续的下划线（§3.1），所以 `mcp__github__` 够不到
  `mcp__github_x__`。

★ **启动的是并集。** 快照是共享的，所以管理者启动的是**某个** agent 点名的那些服务器：
  没有任何 agent 点名的 server 永远不会被启动；而只有 `task` 点名的 server 会被启动
  —— 但 `main` 够不到它的工具。★ 另一条路（每个 agent 一份工具集）意味着要给每个
  agent 重建一遍工具清单，而工具清单正是快照冻住的东西（§1.1）。

★ **并集取遍那些能跑的 agent，子 agent 也在内。** 子 agent 有自己的一份清单（§5.3），
  而它可能在一轮根 agent 并没有发起的对话里跑起来。⇒ 并集取遍配置声明的每一个 agent
  与每一个子 agent —— 只有子 agent 点名的 server 也会被启动，因为跑起来可能就是它。

### 11.4 那个端点

`GET /v2/mcps?agent=<agentId>` —— `agent` 可省略，省略就是配置里的缺省 agent。

★ 这个端点是**新的**，不是 `/v2/agents` 的延伸：agents 视图是**配置面**的视图
  （它是从配置搭出来的，`api/v2_introspect.go:183`），而 MCP 状态是**运行时**状态 ——
  子进程一死它就变，而且它不能像 agents 列表那样在标签页的生命周期里被缓存
  （`web/src/lib/agent-info.ts:48`）。

```json
{
  "agent": "main",
  "servers": [
    {
      "name": "github",
      "transport": "stdio",
      "status": "ready",
      "error": "",
      "tools": [
        {
          "name": "read_issue",
          "exposedName": "mcp__github__read_issue",
          "tier": "ask",
          "status": "available",
          "reason": ""
        }
      ]
    }
  ]
}
```

| 字段 | 意思 |
|---|---|
| `servers[].status` | `ready` —— 客户端起来了；`unreachable` —— 上一次启动失败，`error` 说明为什么；`unused` —— 这个 agent 没有点名这个服务器 |
| `servers[].error` | 上一次启动的失败文本 |
| `servers[].tools[].name` | server **自己**的名字 —— 文件的词儿（§5.2） |
| `servers[].tools[].exposedName` | 模型看到的那一个名字（§3） |
| `servers[].tools[].tier` | 那个名字在闸门那儿的判定（§1.3） |
| `servers[].tools[].status` | `available` —— 这个 agent 拿得到；`skipped` —— server 提供了它而它没有被暴露（§3.2）；`blocked` —— 暴露了，但这个 agent 拿不到 |
| `servers[].tools[].reason` | 它为什么拿不到：`disabled_tools`、`mcps`、`name_too_long` 或 `name_collision` |

★ **server 那一层更粗，而且它先报。** 一个工具可能同时被两件事挡住 ——
  这个 server 不在这个 agent 的 `mcps:` 清单里，**而且**这个工具在它的
  `disabled_tools` 里。⇒ 理由是 `mcps`：那个 server 压根不在，而更细的那条规则
  对它本来也说不着什么。★ 换个顺序，就会为一个 agent 根本没有的 server 报一条
  工具级的规则。

★ **配置面视图也列出那些服务器。** `GET /v2/config` 加上已配置服务器的名字与传输方式
  （`api/v2_introspect.go:388`）：配置说**什么存在**，面板说**什么起来了**。
  ★ 一个把 `mcps/` 分区藏起来的配置面视图，会让设置界面没东西可显示。

★ **请求正中的换代不是一份混合物。** 管理者的客户端属于某一代（§5.4），
  而面板的那一问走的是与一轮对话同一次启动（§6.8）—— 所以它拿到的是**当前**这一代的
  客户端，或者是启动它的失败。★ 答案是某一代的状态，绝不是两代的混合。

★ **两套词儿都显示。** 文件里写的是 `allows: [read_issue]`；模型看到的是
  `mcp__github__read_issue`。只显示对外名字的面板，用户没法把某一行和自己的文件对上；
  只显示自己名字的面板，则把模型能调的东西藏起来了。

★ **每一个已配置的服务器都出现，且只出现一次。** 这个 agent 不用的服务器是
  `unused` —— 不是不见了。★ 在列表里缺席的服务器，与这个 agent 不用的服务器，
  在读者眼里长得一样，而它们里头只有一个是问题。

★ **`unused` 的服务器没有工具行。** 面板讲的是**这个 agent** 的服务器，
  而这个 agent 不用的服务器的工具不是它的工具 —— 把它们列出来，
  就是把几行永远调不到的东西摆到用户面前。

★ **够不着的服务器，它的工具是上一次成功的那份清单。** 管理者留着上一次成功的清单
  （§6.6 —— 崩掉的子进程会在下一次调用时被重新拉起，清单也跟着回来），
  所以面板能说「github —— 够不着 —— 这些是它上次还在时有的工具」。
  状态说明这份清单是不是当前的；清单说明这个 server 提供什么。

★ **而 `available` 说的是 agent，不是生死。** 它的意思是*这个 agent 的表没有摘掉
  这个工具*，而不是*这次调用会成功*。一个倒下的服务器的那几行是它曾经有的工具；
  这次调用通不通，那是服务器的状态该说的。

★ **理由是记号，解释是面板的事。** `disabled_tools` 与 `mcps` 是机器值，
  与错误体里的 `code` 一样；解释它们的那句英文属于界面，不属于线缆。

### 11.5 那个面板

★ 它是一个**对话框**，与 agent 选择器一样（`web/src/components/agent-picker.tsx:68`）：
  内容是一张服务器表，答案是短暂的，而对话记录不是它该待的地方。

| 元素 | 显示什么 |
|---|---|
| 标题 | 「MCP servers」，以及它们属于哪个 agent |
| 每个 server 一块 | 名字、传输方式、状态、错误那一行 |
| 每个工具一行 | 自己那个名字、对外那个名字、档位 |
| 空状态 | 「No MCP servers are configured」 |

★ 面板每次打开都去取一次 —— 状态是实时状态，而这个标签页的缓存
  （`web/src/lib/commands.ts:9` 那个命令表、agents 列表）不是它该待的地方。
  一个 **Refresh** 按钮重新读一次，因为用户修好一个服务器之后，
  想不关掉对话框就看到结果。

★ 那个 agent 是这一轮的 agent（`web/src/lib/store/selectors.ts:13`），
  而标题会写明是哪一个 —— `task` 的服务器不是 `main` 的服务器。
  ★ 决定一轮的是**正在跑的那个** agent 的清单（§5.3）；面板显示的是下一轮会作为
  根 agent 跑的那个。

## 12. 这份设计不管什么

| 没覆盖 | 为什么 |
|---|---|
| MCP 协议与各种传输 | 它们属于 `mcp-go`；openheron 只挑一个客户端并配它 |
| 带发现、健康检查与重试的 server 注册表 | 一个服务器一个文件，够起步了；注册表是下一轮的事 |
| MCP 的 resources、prompts 与 sampling | eino-ext 那个组件**只**暴露工具 |
| 按工具计的速率限制与配额 | `policy/limits.yaml` 数的是数量，它没有工具名这一维 |
| 不是放在头里的凭据 | `inject` 是头注入；放在 query、cookie 或非 HTTP 协议里的 key 只能进沙箱（§8.1） |
| 一种安装宿主侧 MCP 服务器的 mixin | mixin 是声明；内容属于 workload（`sandbox-kit-design.md` §2.3） |
| ★ **Kit 打包 MCP 服务器** | §8 整节仍然是一份提案：Kit 里还没有带一个 MCP 服务器 |
| ★ **去掉全局 `deny`** | 配置设计提议去掉它（配置设计 §6.3），而 MCP 功能没有把它带走（§4.2） |
| 一个会订阅的面板 | 面板在打开时读一次管理者的状态；它不订阅，也不重试（§11.5） |
| SSE 传输方式 | `http` 的意思是 **streamable HTTP**；SSE 是那个旧的传输方式，openheron 不说它（§5.2） |

★ 这份文档里每一条描述代码的地方都点了文件名与行号。代码动了，行号跟着动 ——
  这是本仓库对每一份其它设计文档守着的契约，
  也是 §10.1 那张表是一份**位置**记录而不是一份**意图**清单的原因。
