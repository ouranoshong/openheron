# 密钥体系设计

> 一句话：秘密按**名字**引用，名字自带命名空间；环境变量是覆盖层，钥匙串是存放地。

> 范围：LLM 与 MCP 两侧的密钥命名、解析、写入面（HTTP 与 CLI）、界面入口。
> 不包含：MCP 协议本身（见 `docs/tools-design.md`）、配置树的结构（见 `docs/config-design.md`）。
> 本文按 AGENTS.md 的文档语言规则**只有中文版** —— 架构与代码设计类文档不做双语。
>
> ★ **本文描述的是现状。** 方案已经落地：代码、接口、命令行与界面都在，
> 下面每一句都指着一行代码。§8 记着落地的顺序与当时的取舍，§9 记着仍未
> 拍板的部分。

## 0. 一句话

密钥的**位置**曾经写在配置文件里（`api_key: ${DEEPSEEK_API_KEY}`），于是「哪个变量装哪把钥匙」
成了一份配置树的一部分 —— 而它本来是运行期的事实。现在这件事拆开了：

- **名字**由约定给出，不写在文件里：provider `x` ⇒ 变量 `LLM_X_API_KEY`、钥匙串条目 `llm:x`；
  server `s` ⇒ 变量 `MCP_S_TOKEN`、钥匙串条目 `mcp:s`。
- **值**从环境变量读（覆盖层），环境变量没有就从**系统钥匙串**读（存放地）。
- **两侧共用一个解析器**：解析器按**名字**工作，不按文件工作 ⇒ LLM 侧与 MCP 侧天然同构，
  前缀又把两类的命名空间彻底分开。

★ 三个词要分清：**存放地**（钥匙串，持久）、**覆盖层**（环境变量，临时压过存放地）、
**引用**（配置文件里怎么指名这把钥匙）。这三件事曾经挤在一个字段里，所以每一个都说不清。

★ **文件名里的那个 `api_key` 行没了。** 加载器**拒绝**它并说清新写法
（`agentkit/spec/load.go:44-52`），编辑器与诊断也一样（`agentkit/spec/write.go:929-945`）。
这不是兼容性保留 —— 那个字段现在只用来**抓住旧文件**。

## 1. 命名：两类密钥，两个命名空间

| 组 | 环境变量（覆盖层） | 钥匙串 user（存放地） | 引用方式 |
|---|---|---|---|
| LLM | `LLM_<PROVIDER>_API_KEY` | `llm:<provider>` | **隐式** —— provider id 就是引用 |
| MCP | `MCP_<SERVER>_TOKEN` | `mcp:<server>` | **显式** —— `${MCP_<SERVER>_TOKEN}` |

两组共用一个钥匙串 service：`openheron`（`agentkit/spec/secret.go:33-60`）。

### 1.1 名字怎么反解回 user

| 名字 | ⇒ 钥匙串 user |
|---|---|
| `LLM_DEEPSEEK_API_KEY` | 去 `LLM_` 前缀与 `_API_KEY` 后缀 ⇒ `DEEPSEEK` ⇒ 小写 ⇒ `llm:deepseek` |
| `MCP_GITHUB_TOKEN` | 去 `MCP_` 前缀与 `_TOKEN` 后缀 ⇒ `GITHUB` ⇒ 小写 ⇒ `mcp:github` |

★ **id 里带下划线也不歧义**：只剥一次前缀、一次后缀。`llm/foo_api_key` ⇒
`LLM_FOO_API_KEY_API_KEY` ⇒ 剥一次后缀 ⇒ `foo_api_key` ✔。实现是 `SecretUserFor`
（`agentkit/spec/secret.go:89-98`）。

★ **前缀是命名空间，不是装饰**：`llm:` 与 `mcp:` 让「删一组」这件事的作用域写在**名字**里，
而不是写在调用方的记忆里（§4.1）。

### 1.2 id 的形状

两侧同一条：`^[a-z0-9]+(_[a-z0-9]+)*$`（`decl.ProviderNamePattern`，
`agentkit/spec/decl/decl.go:620-633`）。

★ MCP 侧直接沿用现成的 `decl.ValidMCPServerName`（`agentkit/spec/decl/decl.go:641`），
LLM 侧取同一条形状。装载器里两侧各有一处先例：MCP server 的名字今天就是这么校验的
（`agentkit/spec/load.go:489`），LLM 侧照它写（`agentkit/spec/load.go:28-30`）。

★ **不许 `-`**：推导名不做 `-`→`_` 映射 —— `initcfg` 里那段映射
（旧的 `initcfg/initcfg.go:382-384`）是「变量名可以自己挑」的遗产，方案落地后它作废，
`openheron init --env` 也随之退役（§6）。

## 2. 解析：一个解析器，两个入口

### 2.1 解析链

```
resolve(name):                          // name = "LLM_DEEPSEEK_API_KEY" / "MCP_GITHUB_TOKEN"
  1. v, ok := os.LookupEnv(name)
       ok && v == ""   → 空串（“设了，但是空的”）
       ok && v == "@file:/path" → 读那个文件（读不到是硬错误）
       ok             → (v, "env:"+name)                    ← 覆盖层优先
  2. user, ok := SecretUserFor(name)                        // 只有 LLM_/MCP_ 前缀才反解
       ok → store.Get("openheron", user)
              命中          → (key, "keyring:"+user)
              ErrNotFound  → 落空
              其它 error    → 落空，但**标记「钥匙串不可用」**
  3. → ("", "none")
```

实现是 `secretResolver.resolve`（`agentkit/spec/secret.go:272-311`）。

★ **「没找到」与「后端不可用」必须分开。** 无 D-Bus 的 Linux、锁着的钥匙串、没有钥匙串的 CI
runner —— 这三种都返回**普通 error 而不是 `ErrNotFound`**。混为一谈 ⇒ 日志会说「钥匙串里没有
这个密钥」，而真相是「这台机器没有钥匙串」。★ 而「不可用」是**软失败**：机器没有钥匙串是
机器的事实，不是配置坏了 —— 树照常装载，只记一条提示。

★ **Linux 上先判可用性**：`DBUS_SESSION_BUS_ADDRESS` 为空 ⇒ 直接判不可用，不去试 D-Bus 的
autolaunch（那会白等一次进程启动）。

★ **钥匙串是一个全局 OS 资源**，所以它**默认不接**：`Service` 上没有装 store 时，解析器走的是
`nullStore`（`agentkit/spec/secret.go:220-231`），它回答「不可用」，而不是去碰开发者自己的
凭据库。真正接上它的地方只有三处，都是**明确地问了**：服务端的组合根
（`main.go:385`）、`openheron init`（`initcmd.go:69`）与 `openheron secret`（`secretcmd.go:32-44`）。

### 2.2 两个入口共用一个名字

| 侧 | 引用 | 解析 |
|---|---|---|
| LLM | 装载器按 provider id **合成** `LLM_<P>_API_KEY` | `resolve(那个名字)` |
| MCP | YAML 里写 `${MCP_<SERVER>_TOKEN}` | `expandMCPMaps` 遇带前缀的 `${...}` ⇒ `resolve` |

★ **MCP 侧 YAML 里写的那个字符串，就是环境变量的名字。** 于是「env 优先」不是一条额外规则，
而是同一个名字在两个地方含义相同：环境里有就用环境的，没有就用钥匙串的。

★ **解析器按名字工作，不按文件工作** ⇒ 跨组引用也允许（`mcps/foo.yaml` 里写
`${LLM_DEEPSEEK_API_KEY}` 也能拿到模型那把）。命名空间在**名字**里，不在文件里。

★ **没有前缀的 `${...}` 行为不变**（只查环境）：`${GITHUB_TOKEN}` 仍然是「查环境变量」，
不是「取那把钥匙」。判定在 `derivedSecretRef`（`agentkit/spec/expand.go:56-73`）。

### 2.3 值的形态：`@file:` 还在

环境变量的值可以是 `@file:/path` —— 沿用 `expandValue` 已有的词汇（`agentkit/spec/expand.go:10-39`），
密钥从那个文件读，内容去掉首尾空白。

★ 这是环境变量**值**的形态，不是钥匙串的形态，两者互斥：环境变量命中即止。
★ 路径读不到是**硬错误**（`agentkit/spec/secret.go:278-292` 的分界：路径写错是配置错，
不是运行期状态）。

## 3. 配置文件：字段退场

### 3.1 LLM 侧

```yaml
# .openheron/llm/deepseek.yaml
type: openai_compat
base_url: https://api.deepseek.com
timeout: 5m

models:
  - id: deepseek-flash
    context_window: 131072
    reasoning:
      off:  { reasoning_effort: none, thinking: { type: disabled } }
      low:  { reasoning_effort: low,  thinking: { type: enabled } }
      high: { reasoning_effort: high, thinking: { type: enabled } }
      max:  { reasoning_effort: max,  thinking: { type: enabled } }
    reasoning_default: low
```

★ **`api_key` 这一行没有了**（`agentkit/spec/decl/decl.go:32` 的字段只用来抓住旧文件）。

★ 加载器**拒绝**这一行，而不是忽略它（`agentkit/spec/load.go:44-52`）—— 拒绝的话说的是
「密钥从 `LLM_DEEPSEEK_API_KEY` 来（或从钥匙串条目 `llm:deepseek` 来）；删掉这一行」。
★ 明文密钥曾经在**编辑器保存**时被拦、启动装载器却照单全收；字段一退场，这个洞跟着消失。

### 3.2 MCP 侧

```yaml
# mcps/github.yaml —— stdio：密钥进子进程的环境
transport: stdio
command: npx
args: ["-y", "@modelcontextprotocol/server-github"]
env:
  GITHUB_TOKEN: ${MCP_GITHUB_TOKEN}
```

```yaml
# mcps/remote.yaml —— http：密钥进请求头
transport: http
url: https://mcp.example.com/mcp
headers:
  Authorization: Bearer ${MCP_REMOTE_TOKEN}
```

★ MCP 侧**保留引用**（LLM 侧隐式）是有理由的：一个 server 可能要**好几把**密钥，而 LLM 侧
一个 provider 只有一把 —— 「一把」的那侧可以隐式，「多把」的那侧必须点名。命名空间与解析器
仍是同一套。

★ `env:` 与 `headers:` 各自受 transport 的约束：stdio 才有 `env:`，http 才有 `headers:`
（`agentkit/spec/decl/decl.go:764-775`），http server 声明 `env:` 是启动期硬错误
（`agentkit/spec/load.go:504-507`）。这条不变。

## 4. 写入面

### 4.1 HTTP：`/v2/secrets`

| 路由 | 作用 | 影响面 |
|---|---|---|
| `GET /v2/secrets` | 两组的清单：每组有哪些条目、各自的有效来源。**不回显值** | — |
| `PUT /v2/secrets/:group/:name` | **写入** ⇒ 钥匙串 `llm:<provider>` | 一条 |
| `DELETE /v2/secrets/:group/:name` | 删这一条 | 一条 |
| `DELETE /v2/secrets/:group` | **删整组** | **只删那一组** |

路由在 `api/v2_files.go:96-99`，实现是 `api/v2_secrets.go`。

写入的四条规矩：

1. **值只进钥匙串** —— 不进配置树、不进日志、不回显。请求体是 `{"value":"…"}`：密钥在 body 里，
   不在 path 里（path 会进访问日志）。
2. **响应必须说清有效来源**：`{"stored":"keyring:llm:deepseek","effective":"env:LLM_DEEPSEEK_API_KEY","shadowed":true}`。
   ★ 宿主环境里已经设了变量时，往钥匙串写**不生效** —— 接口必须说这件事，否则用户写完发现
   什么都没变。
3. **写完要生效**：钥匙串不是文件，热更新看不见它 ⇒ 写入成功后必须走**同一条热更新路径**
   重新解析（`Service.MarkSecret`，`agentkit/spec/config.go:370-385`）。
4. **暴露面不新增**：这个服务今天没有鉴权，本地进程本来就能 `PUT /v2/settings/item` 改配置树；
   钥匙串写入不制造新的一类面，但**值不得进任何日志路径**。

★ **读一律不回显值**：`GET` 只给「存不存在 / 哪来的 / 有没有被覆盖」，连长度都不给（长度也是信息）。

### 4.2 CLI：`openheron secret`

```
openheron secret set llm deepseek     # 从 stdin 读密钥，写进钥匙串 llm:deepseek
openheron secret set mcp github       # 同上，写进 mcp:github
openheron secret get llm deepseek     # 打到 stdout（给管道用）
openheron secret list                 # 两组清单 + 有效来源
openheron secret remove llm deepseek  # 删一条
openheron secret remove llm           # 删整组
```

实现是 `secretcmd.go`（命令在 `main.go:68` 接上）。

★ `set` **从 stdin 读**（不进 argv、不回显）—— 与沙箱 Kit 的 `sbx secret set-custom --value-stdin`
同一套习惯（`sandbox-kit/TUTORIAL.md:67-69`）。
★ `get` 把密钥打到 stdout：这是它的用途（喂管道），但它**是一个会泄密的命令**，文档要写明
→「别在共享终端里跑」。
★ `remove llm`（整组）需要读配置树才知道有哪些实体；`remove llm deepseek`（一条）不需要。

★ 命令与接口同构（`llm` / `mcp` 两组、同一套动作），因为它们说的是同一件事的两面。

## 5. 界面入口：Settings 里的 Secrets 标签页

### 5.1 位置

Settings 对话框（`web/src/components/settings/settings-dialog.tsx:23-119`）加一个标签页：

```
┌ Settings ──────────────────────────── [ Files ] [ Secrets ]  ✕ ┐
│ left (w-56)        │ right                                       │
│ ▾ Providers        │ deepseek                                    │
│   deepseek ●       │ Stored in: keyring:llm:deepseek             │
│   openai   ○       │ In effect:  keyring:llm:deepseek            │
│ ▾ MCP servers      │ Environment: LLM_DEEPSEEK_API_KEY (set)     │
│   github   ●       │ ⚠ The environment variable wins.            │
│                    │ [ Set value… ]  [ Remove ]                  │
└───────────────────────────────────────────────────────────────────┘
```

面板是 `web/src/components/settings/secrets-panel.tsx:41-124`，标签条是同文件的
`TabBar`（`:122-147`）。

★ **为什么不进左侧那棵文件树**：那棵树是**从服务端给的路径搭出来的**
（`web/src/components/settings/file-list.tsx:23-26` 的注释写得很清楚：分区自己长什么样，这棵树
不关心）。秘密没有路径，进树里就是破坏那个模型。

★ **为什么不是侧边栏第二个按钮**：秘密是**配置**，配置聚在一处。

★ 组的标签由**服务端**给（`api/v2_secrets.go:38-43` 的 `label`），客户端不自带一份名单 ——
服务器拥有分区表，客户端自带一份就是第二个真相。

### 5.2 数据：一个载荷，两组，同一段渲染代码

```jsonc
GET /v2/secrets
{ "groups": [
    { "id": "llm", "label": "Providers", "items": [
        { "name": "deepseek", "stored": true,
          "source":    "keyring:llm:deepseek",
          "effective": "keyring:llm:deepseek",
          "shadowed":  false,
          "envVar":    "LLM_DEEPSEEK_API_KEY" }]},
    { "id": "mcp", "label": "MCP servers", "items": [ /* 同形 */ ]}
]}
```

→★ **名字取「配置里的实体」** —— 钥匙串不能枚举，所以清单由配置驱动；实体已经消失的残留条目
不可见、也删不掉（已知边界，§7.8）。

### 5.3 界面规矩

| 规矩 | 做法 |
|---|---|
| **值只写不读** | 面板只显示 `stored` / `not set` 与来源；输入框 `type="password"`、`autoComplete="off"`，写完清空；DOM 里永不出现值 |
| **`shadowed` 必须看得见** | 环境变量覆盖时显示警告条：「The environment variable … wins — the stored key is not in effect.」 |
| 状态标记 | `stored` / `not set` 两个词 + 一枚 token 色的圆点（`--success` / `--warning`） |
| 删除确认 | 删整组时确认框写明数量与范围：「Remove 1 provider secret? Only the Providers group is affected; MCP server secrets are untouched.」 |
| 文案 | **全部英文**（仓库规矩） |

★ 新逻辑都在**纯函数层**（`web/src/lib/secrets.ts`）：`sourceLabel()` 把「地方」翻成句子、
`statusOf()` 给出状态与警告、`envState()` 回答变量有没有东西、`removeGroupMessage()` 写出
确认那句话。`bun test` 直接覆盖它们（`web/test/secrets.test.ts`）。

### 5.4 前端要碰的文件

| 文件 | 角色 |
|---|---|
| `web/src/lib/secrets-api.ts` | 类型化客户端（`list` / `set` / `remove` / `removeGroup`）+ 类型 + 错误码 |
| `web/src/lib/secrets.ts` | **纯函数**：来源的句子、状态、警告、确认文案 |
| `web/src/lib/secrets-store.ts` | zustand store：groups、loading、error、busy 项、动作 |
| `web/src/components/settings/secrets-panel.tsx` | §5.1 那两栏 |
| `web/src/components/settings/settings-dialog.tsx` | 标签条 + 正文切换 |
| `web/src/lib/settings-store.ts` | 加 `tab: "files" \| "secrets"` |

★ 两条前端铁律：zustand 选择器返回**引用稳定**的值（模块级空常量）；颜色只用 token，不写字面量。
★ 错误沿用 `ApiError`（`web/src/lib/api.ts:240-264`），服务端给什么码前端就按什么码分。

### 5.5 E2E 断言了什么

`web/test/e2e/secrets.spec.ts`：

1. **值永远不出现在 DOM 里** —— 存完之后断言页面正文不含那个值。这条比任何截图都有说服力。
2. 设置 ⇒ 行显示 `stored`；删除 ⇒ 显示 `not set`。
3. `shadowed: true` 时出现那条覆盖提示。
4. 删整组 ⇒ 确认框点名数量与范围；确认后**只有本组**的行变了。
5. 标签条切换时文件树消失 —— 秘密不是文件树的分支。

## 6. 老配置

★ **本仓库没有别的用户**，所以不做兼容层，只写清楚怎么改。判定与话术三条：

| 老写法 | 结果 |
|---|---|
| `llm/x.yaml` 里的 `api_key: ${DEEPSEEK_API_KEY}` | **启动期报错**，话术说清新写法与那把钥匙该放哪儿 |
| `llm/x.yaml` 里的 `api_key: @file:/path` 或字面量 | **启动期报错**，并给新写法 |
| `mcps/s.yaml` 里的 `env: {FOO: ${GITHUB_TOKEN}}` | **仍然能用**（无前缀 ⇒ 仍只查环境），但**不再享受钥匙串兜底** ⇒ deferred 提示点名 |

★ 迁移说明写在用户手册（`docs/user-manual.md` 与 `_zh`）与本文 §6，别只写在提交信息里。

★ `api_key` 字段**从结构体里删掉**会让旧文件撞上严格解码
（`agentkit/spec/config.go:829-841` 的 `decodeStrict`，`KnownFields(true)`）—— 那句报错是
yaml 自己的话（「field api_key not found in type spec.Provider」），对人无意义。⇒ **字段保留但
降级**：不再由 YAML 给值，只用来**捕获旧文件并给一句人话**（`agentkit/spec/decl/decl.go:23-32`）。

★ **`openheron init --env` 退役**：变量名不再由人挑。替代路径是 `openheron secret set`。

## 7. 代价与边界

1. **新增一个依赖**：`github.com/zalando/go-keyring`（`go.mod:22`）。★ 它没有文件后端（密钥不会
   因为「用了 keyring 库」反而落到磁盘上），无 cgo，代理可达，CI 有网。
   ⇒ `docs/config-design.md` 的「配置层零新增依赖」要写明：**密钥后端**是显式例外。
2. **env 优先 ⇒ 钥匙串写入可能被环境覆盖**（§4.1 的第 2 条）—— 已在接口与界面上显式化，
   但它是**新的一种用户困惑**，文档必须写。
3. **变量名不能自定义**：`openheron init --env MY_KEY` 退役。
4. **id 受限**：不许 `-`、不许大写。已有的 `my-provider.yaml` 要改名，★ 连带改 agent 里的
   `model:` 引用（`docs/config-design.md` §5.1 讲的是这个引用形式）。
5. **钥匙串是每用户每机器的**：一份配置树共享给多台机器 ≠ 共享密钥；每台机器要么各存一条，
   要么各走环境变量。
6. **同一把钥匙喂两个 provider / 两个 server 做不到**：user 名字带组前缀，一对一。
   ★ 这是本方案**唯一彻底失去的能力**（今天两个 provider 可以写同一个 `${SHARED_KEY}`）。
   文档里要明说，别装作没有。
7. **空串与未设第一次可分辨**：解析器知道变量叫什么，两者的区别就在眼前
   （`agentkit/spec/expand.go:78-88`）。以前分不出来（`docs/pitfalls.md` §7.3）。
8. **钥匙串不能枚举**：实体已经消失的残留条目看不见、也删不掉（§5.2）。
9. **没有人工确认**：`/v2/secrets` 的写入不弹确认框，因为这个服务今天本来就没有鉴权，
   而本地进程本来就能改配置树。若这个服务将来要暴露到 localhost 之外，得加一道确认。

## 8. 它是怎么落地的

★ 顺序是有理由的：**先把读做对，再开写，最后上界面** —— 否则会在一个还在变的约定上写密钥，
也会在一个还不存在的接口上建界面。

| 期 | 做了什么 |
|---|---|
| **第 1 期：读侧** | `go.mod` 加 `github.com/zalando/go-keyring`；新 `agentkit/spec/secret.go`（解析器、`SecretStore` 接口、钥匙串后端、Linux 的 D-Bus 预检）；`agentkit/spec/load.go` 两侧接入；`decl.go` 的 `api_key` 降级 + provider id 形状校验；写闸门与诊断换成「这一行已退休」的话术；`api/v2_introspect.go` 的 `providerView` 加 `keySource` |
| **第 2 期：写侧与界面** | 新 `api/v2_secrets.go`（§4.1 的路由表 + `shadowed` 语义 + 写完走热更新路径）；新 `secretcmd.go`（§4.2 的 CLI）；前端四层（§5.4）+ 假后端的 `/v2/secrets` |
| **第 3 期：外围** | 模板与样例去 `api_key`；两份 skills 的 `SKILL.md`；`.openheron-example/llm/openai.yaml`；沙箱 Kit 的两个 mixin 与两份 TUTORIAL（容器里注入的变量名就是 openheron 读取的变量名）；文档 |

★ **`SecretStore` 是接口的原因**：钥匙串是**全局 OS 资源**，测试绝不能碰开发者真的钥匙串 ——
一次 `go test` 往人家凭据库里写条目是不可接受的副作用（`agentkit/spec/secret_test.go` 里的
`fakeSecretStore` 就是这个原因）。

## 9. 待决

- **钥匙串 service 名**：现在定的是 `openheron`。它是全局的一格，撞名要用户自己解决 ——
  暂定不变，若将来支持多实例需要再议。
- **`/v2/secrets` 的写入要不要人工确认**：现在按 `/v2/settings/item` 的规格办（本地进程本来就能
  改配置树）。若这个服务将来要暴露到 localhost 之外，得加一道确认。
- **同一把钥匙喂多个实体**：本方案没有这条能力（§7.6）。若将来钥匙串后端支持「共享条目」，
  再回来补。
