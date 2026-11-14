# 项目与目录设计

> 一句话：运行状态住在**用户级的大本营**里，配置住在它下面；项目是一个目录，会话属于项目，project root 按会话切换。

> 范围：`openheron_root` / `config_root` / `data_dir` / 日志的落点，项目与会话的归属关系，
> 命令行与环境变量，接口与界面上的分组。
> 不包含：配置树自身的结构（见 `docs/config-design.md`）、运行时的一轮（见 `docs/runtime-design.md`）、
> 工具与集成（见 `docs/tools-design.md`）、界面资源与动效（见 `docs/ui-design.md`）。
> 本文按 AGENTS.md 的文档语言规则**只有中文版** —— 架构与代码设计类文档不做双语。
>
> ★ **本文描述的东西已经全部落在代码里** —— P1（三个根、环境变量前缀、`init` 的默认目标）、
> P2（`projects` 包与 `/v2/projects`）、P3（会话归属与侧栏分组）、P4（按会话切换的
> project root）。★ 下面凡是写「要变成」的地方，说的都是**现状**；§7 记着每一处的
> 落点，§8 记着落地顺序与**设计之外多改的那几处**，§9 记着不在范围内的部分。

## 0. 一句话

今天 openheron 的四个目录缠在一起：**配置根下面长着数据目录和日志目录**，而配置根本身
又是从项目根推出来的（`<projectRoot>/.openheron`）。于是：

- 「配置」是**可搬家的** —— 换个目录跑，同一份配置就没了；
- 「状态」是**跟着配置走的** —— 数据和日志跟着配置根搬家；
- 「项目」是**进程级的** —— 一个进程只服务一个目录，改目录要重启。

提案把它们拆开：**一个 `openheron_root` 是用户级的大本营**，配置、状态、日志、项目表
都住在它下面；**项目只是一个目录**，会话属于项目，**project root 按会话切换**。

```
~/.openheron/                    ← openheron_root（用户级大本营）
  configs/                       ← config_root（一份，所有项目共享）
    agents/ llm/ mcps/ tools/ skills/ server/ policy/
  state/                         ← data_dir（一份，所有项目共享）
    sessions/<id>/ kb/<index>/ checkpoints/
    projects.json                ← 项目登记 + 会话归属（一对多）
  logs/openheron.log             ← 日志（一个文件）
  projects/default/              ← 默认项目根
```

★ 拆开之后，「配置」与「状态」不再绑死：配置可以指到任何地方，数据与日志永远在用户级
的大本营里；「项目」不再绑在进程上，同一个进程可以同时知道好几个项目。

## 1. 四个根，各管什么

| 名字 | 是什么 | 谁定 | 粒度 |
|---|---|---|---|
| **openheron_root** | 用户级大本营：配置、状态、日志、项目表都在它下面 | `--openheron-root` / `$OPENHERON_ROOT` | 进程级 |
| **config_root** | 配置树：`agents/ llm/ mcps/ tools/ skills/ server/` | `--config-root` / `$OPENHERON_CONFIG_ROOT` | 进程级 |
| **data_dir** | 会话、checkpoint、KB 索引、`projects.json` | `server.data_dir` / `$OPENHERON_DATA_DIR` | 进程级 |
| **默认项目根** | 没有项目的会话在哪个目录干活 | `--default-project-root` / `$OPENHERON_DEFAULT_PROJECT_ROOT` | 进程级 |
| **作用域项目** | 这个进程关注哪个项目 | **位置参数** `openheron [dir]` | 进程级（可空） |

### 1.1 解析规则

**一条规则走到底：每一级都「点名优先、相对路径回落到 `openheron_root`」。**

| 路径 | flag | 环境变量 | 默认 | 相对基准 |
|---|---|---|---|---|
| openheron_root | `--openheron-root` | `OPENHERON_ROOT` | `~/.openheron` | — |
| config_root | `--config-root` | `OPENHERON_CONFIG_ROOT` | `<openheron_root>/configs` | openheron_root |
| data_dir | `server.data_dir` | `OPENHERON_DATA_DIR` | `<openheron_root>/state` | openheron_root |
| 日志文件 | `log.file` | `OPENHERON_LOG_FILE` | `<openheron_root>/logs/openheron.log` | openheron_root |
| 默认项目根 | `--default-project-root` | `OPENHERON_DEFAULT_PROJECT_ROOT` | `<openheron_root>/projects/default` | openheron_root |
| 作用域项目 | 位置参数 `openheron [dir]` | — | 无（= 全部项目） | — |

★ **相对基准从「配置根」改成「openheron_root」是这一版的核心改动。**
`Service.DataDir`（`agentkit/spec/config.go:784`）今天把相对路径 join 到 `s.root`，
`Service.LogFile`（`agentkit/spec/config.go:806`）也一样。两处都要改成 join `home`。
⇒ 于是**配置搬到哪儿，数据和日志都跟着 openheron_root 走** —— 配置是配置，状态是状态。

★ 配置根今天的解析是三级（`agentkit/spec/config.go:505-548`）：
`--config-dir` → `$OPENHERON_CONFIG_DIR` → `<projectRoot>/.openheron`。
提案里第三级换成 **`<openheron_root>/configs`**，第四级才是项目根 ——
但项目根不再推导配置根（见 §2），所以它不是一个解析级，而是**作用域**。

### 1.2 名字的由来

`openheron_root` 是**大本营**，`config_root` 是**配置**，两者不是同一个东西 ——
今天的 openheron 没有这个区分，因为配置根同时是「配置的家」「状态的家」和「项目的附属」。
拆开之后，配置根可以被 `--config-root` 指到任何一个目录，而状态留在大本营。

★ **`workspace` 这个名字不能用来指项目根**：会话的上传目录已经叫它了
（`storage.Session.Workspace()`，`storage/storage.go:84`；目录名 `workspace`，
`storage/filestore/session.go:666`；prompt 里那一行是 `storage.Session workspace: …`，
`agentkit/run/input.go:198-203`）。项目根的容器叫 **`projects/`**。

## 2. 项目：一个目录，不是一棵配置树

### 2.1 projects.json

项目登记落在**数据目录**里：

```
<data_dir>/projects.json
```

```json
{
  "version": 1,
  "projects": [
    { "id": "openheron", "name": "openheron", "root": "C:/.../openheron", "order": 0, "sessions": ["s1", "s2"] },
    { "id": "codelab",   "name": "CodeLab",   "root": "C:/.../CodeLab",   "order": 1, "sessions": [] }
  ]
}
```

`sessions` 是**一对多**：一个项目 id 对着多个会话 id；反过来读就是「这个会话属于哪个项目」。

★ **它落在数据目录而不是配置根**，因为归属关系会变，而配置是「用户写给 agent 的东西」。
数据目录已经可以单独配（`server.data_dir` / `$OPENHERON_DATA_DIR`），用户要搬就整个搬走。

★ `sessions` 数组**没有 `omitempty`**：项目存在但还没有会话时写 `"sessions": []` ——
空数组和「没这个字段」是两件事，前者说「这个项目还没有会话」，后者说「这个文件是旧版本写的」。

### 2.2 归属的唯一真相

**表里 `projects[].sessions` 有这个 id ⇒ 会话属于该项目；没有 ⇒ 未选项目。**

会话自己的记录（`storage/filestore/session.go:643-658` 的 `state` 结构）**不记 project** ——
归属只有这一份真相，两处都记就会出现「表说 A、会话说 B」的分裂，而没有任何地方能仲裁。

★ 代价是**表丢了会怎样**：所有会话回落成「未选项目」，用默认项目根干活。
历史、workspace、checkpoint 一样都不丢（会话文件在 `<data_dir>/sessions/<id>/`），
只是分组没了。这是个**可预期**的坏法 —— 不是「一半在一半不在」。

### 2.3 换项目 = 新建项目

**不提供「把会话从 A 项目搬到 B 项目」这条写路径。** 会话创建时定项目，之后不变。

⇒ 表里的 `sessions[]` **只增不换**：创建时 append，删除时 remove。
⇒ 少了一条写路径，也少了一个歧义：搬会话时，它的 transcript、workspace 和 checkpoint
落在哪个项目下，是个没有好答案的问题。

### 2.4 写表：锁与原子写

★ **多进程会同时写这张表。** 今天两个 openheron 进程各写各的会话目录，互不相干；
现在它们要写同一份 `projects.json`。所以：

- **进程内**互斥锁（一个进程里多 goroutine 写表）
- **跨进程**文件锁（两个进程同时写表）
- 写之前**重读一次盘**（用户可能直接手改了这个 JSON —— 它是用户级文件，用户当然能改）
- 写用 `persist.AtomicWrite`（`persist/atomicfile.go:32`，Windows 上带 rename 重试）

★ 写表失败**不让会话创建失败**：会话文件已经落盘了，那是事实；归属写不进去，
会话就是「未选项目」，日志报一条。★ 反过来（表里有 id 而文件不存在），
列表时跳过 —— **以文件为准**。

### 2.5 配置目录的两级

| 级 | 是什么 | 怎么定 |
|---|---|---|
| **用户级（默认）** | `<openheron_root>/configs` | 默认即是；`--config-root` / `$OPENHERON_CONFIG_ROOT` 可改指 |
| **项目级** | 某个项目自己的配置树 | `projects.json` 里登记 `configDir`（可选字段，先留口不实现） |

★ **项目级配置要显式登记，不靠「探测 `<R>/.openheron` 存在就用」**：
老用户每个项目下的 `.openheron/` 都是服务端自动写的**出厂树**（`main.go:283-308`）。
按「探测就用」的规则，每个项目都会用自己那份出厂树，用户级共享配置的意义就没了。
显式登记没有这个副作用：老的 `.openheron/` 还在原地，用户想用就登记一行。

⇒ 每会话的配置根解析：**显式点名 → 项目登记的 `configDir` → 默认配置根**。

## 3. 两种模式：区别只在「渲染几组」

### 3.1 命令行

`openheron` 接受**一个**可选位置参数 —— 项目目录（flag 必须写在前面，Go 的 flag 解析器
遇到第一个非 flag 参数就停）：

```bash
openheron              # 多项目模式：全部项目，界面渲染多组
openheron .            # 单项目模式：只关注这个目录，界面渲染一组
openheron /work/api    # 同上
```

多于一个位置参数报错（`main.go:121-147` `parseServerFlags`）。

### 3.2 位置参数同时定两件事

```bash
openheron .
 → 作用域 = 项目「.」      （进程只关注它）
  → 默认项目根 = 「.」      （除非显式给了 --default-project-root）
```

★ 为什么默认项目根也要跟着变成这个目录：**单项目模式下「未选项目」的会话并进那一组**
（§3.4），而它们的实际工作目录必须就是这个目录，否则界面在说谎。

★ **`openheron .` 自动登记那个目录**：表里没有就加一条（`id` = 目录名，重名加后缀，
`root` = 绝对路径）。不登记的话，新建会话**无处归属** —— 归属的唯一真相是表。
自动登记不是「替用户猜」：用户明确指了一个目录，就是一次表态。

### 3.3 服务端没有模式分支

**两种模式下服务端完全一样**：同一张表、同一份会话列表、同一套 API。

| | 多项目模式 | 单项目模式 |
|---|---|---|
| `GET /v2/sessions` | 全部会话，每条带 `projectId` | **完全一样**（不过滤） |
| `POST /v2/sessions` | body 带 `projectId` | **完全一样**（前端一样传） |
| 区别 | `scope` 为空 | `scope` 有值 |

★ 早先的草案里服务端按模式过滤列表、并在单项目模式下自动归属会话 —— **那是多余的**。
前端本来就要按 `projectId` 分组（多项目模式已经要了），多一个 `scope` 让它决定渲染几组，
成本几乎为零；而服务端一旦按模式分支，接口在两种模式下形状就不同了，那才是麻烦的开始。

### 3.4 前端：渲染几组

分组是**纯函数**（仓库铁律：新逻辑放纯函数层，`bun test` 覆盖）：

```
groupSessions(sessions, projects, scope) → Group[]
```

| scope | 返回 |
|---|---|
| 空（多项目模式） | 置顶组 + 每个项目一组（按 `order`）+ 未选项目组 |
| 有值（单项目模式） | **一组**：作用域项目 = 该项目的会话 **+ 未选项目的会话** |

★ 单项目模式下「未选项目」的会话**并进**那一组，不单独成组 ——
进程只服务一个目录，那些会话没有别的地方可去。

★ **前端不许自己判断模式**：它读服务端给的 `scope`。界面读服务端的答案，不读自己的记忆。

★ **空组不画**（`web/src/lib/projects.ts:95` `groupSessions`）：侧栏是会话的列表，
一个底下什么都没有的标题只是噪声。

★ **一个会话只在一个组里**：置顶的会话只在置顶组里 —— 一条出现两次的会话，
是用户数不清的会话。

★ **置顶组现在是空的**：★ 服务端**还没有** `pinned` 这个字段（代码里没有置顶这个功能），
所以 `meta.pinned` 恒为假。分组函数按设计支持它，等置顶功能落地就亮起来 ——
这不是谎报：没有这个标记就是「没置顶」，而今天确实没有置顶。

## 4. project root 按会话切换

会话的 project root = **它所属项目的 root**；未选项目的会话用**默认项目根**。

★ 三处必须永远一致 —— 任何一处不一致，model 就会在一个目录被告知去另一个目录读写：

| 面 | 今天 | 要变成 |
|---|---|---|
| prompt 里的 `Project root:` | `agentkit/run/input.go:170-186`，用的是 runtime 的值 | **会话的** root |
| `@` 提及选择器 | `api/v2_project.go:72`，用 `cfgSvc.ProjectRoot()` | **会话的** root |
| fsx 写边界 | `agentkit/build/snapshot.go:101-110`，`Root` 写死 | **per-session** |

★ **读不用动**：fsx 本来就不设读边界（`agentkit/fsx/fsx.go:14-16`）——
`execute` 都能 cat 任意文件，把 `read_file` 关进笼子买不到安全。多根对读几乎没有成本。

★ **写边界是唯一有架构代价的一步**。今天的边界是 `Root + ExtraRoots`
（`agentkit/fsx/fsx.go:240-247`），`Root` 在 builder 里写死（`agentkit/build/snapshot.go:102`），
而 backend 被 `heavyOnce` 缓存（`agentkit/build/snapshot.go:78-82`），一个配置版本只建一次。
一个 backend 服务多个 root，只有两条路：

- **(a) ctx 带本轮允许的根**（**已落地**）：`fsx.WithProjectRoot(ctx, root)`，
  `resolveWrite` 额外查 ctx。工具方法都带 ctx（`agentkit/fsx/fsx.go:739`）⇒ 可行，
  而且**边界没有被放宽** —— 会话 A 写不了会话 B 的目录。
- (b) 把所有项目根塞进 `ExtraRoots`：代码少，但一个会话能写另一个项目的目录。

★ **落地时把名字改了一点**：设计里写的是 `fsx.WithWriteRoots`，代码里是
`fsx.WithProjectRoot`（`agentkit/fsx/context.go:25`）。★ 原因是同一个值要管**两件事**：
它既是「本轮允许写哪儿」，也是「相对路径的基准」和「提示词里那个目录」。
一个值、两个消费者 —— 叫 `WriteRoots` 会让人以为它只管写。

★ **子 agent 拿得到同一个 ctx，这不是新问题**：`task` 工具把收到的 ctx 原样交给子 agent
（eino `adk/prebuilt/deep/task_tool.go:174`，`a.InvokableRun(ctx, params, opts...)`），
框架自己还有一条测试断言子 agent 看得见父级的 ctx 值
（eino `adk/prebuilt/deep/deep_test.go:153`，`TestDeepSubAgentSharesSessionValues`）。

★ **今天为什么不需要传递**：project root 是**进程级常量**，它被烘焙进两处 ——
prompt 里那行 `Project root: %s`（`agentkit/run/input.go:170-186`），
和 fsx 后端的 `Root`（`agentkit/build/snapshot.go:101-110`，backend 被 `heavyOnce` 缓存）。
两处都是「建一次、用到底」，所以压根没有「传递」这回事。
★ **新设计第一次需要传递**：root 变成**每轮可变**（按会话），于是
- **prompt 侧**走显式参数：`inputParams` 加 `ProjectRoot`（`agentkit/run/input.go:26-29`）；
- **工具侧**只能走 ctx —— backend 是共享的，写边界是**调用时**才知道的事。

### 4.1 root 在轮次开始时读一次，之后不变

★ **轮次进行中不能变更会话的所属项目**，而且 **root 每次都在轮次开始时读最新的值**。

⇒ 解析点就在轮次开始处，一轮之内固定：

```
Submit(ctx, sessionID, …)                       agentkit/run/runtime.go:240
 └ SessionManager.Get(ctx, sessionID)           runtime.go:241
 └ claimTurn(ctx, sess, …)                      runtime.go:253 → :290
 └ runClaimedTurn(ctx, sess, turn, loop, …)     runtime.go:269 → :440
     ★ runCtx, cancel := context.WithCancel(…)  runtime.go:450   ← 注入点
     ★ runCtx = fsx.WithProjectRoot(runCtx, …)  runtime.go:461   ← root 进 ctx
     └ startLoop(runCtx, sess.ID(), turn, loop) runtime.go:463
         └ loop.Run(ctx)                        agentkit/run/bridge.go:121
             ├ GenInput(ctx, …)                 agentkit/run/loop.go:43
             │   └ inputParams{ProjectRoot: …}  loop.go:88 → input.go:191
             └ 工具调用拿到的就是同一个 ctx
                ⇒ fsx.resolveWrite 查得到「本轮允许的根」  agentkit/fsx/fsx.go:298
```

★ **`runtime.go:461` 为什么是对的地方**：那里造出这一轮的 `runCtx`，
它一路传到 `loop.Run(ctx)`（`bridge.go:121`）⇒ 框架给 `GenInput` 的 ctx、
以及**所有工具调用**的 ctx，都是它。而且那里用的是 `context.WithoutCancel(ctx)` ——
**丢取消、留值**，所以放进去的东西一定传得下去。

★ **一次解析，两处消费**：prompt（`inputParams`）与工具（ctx）。
★ **一轮之内不变**：root 在 ctx 里 ⇒ 这一轮从头到尾是同一个值；
即便有人在轮次进行中改了项目的 root，这一轮仍用开始时读到的那一个，下一轮才生效。

★ **谁来解析**：`projects.Registry.RootOf(ctx, sessionID)` —— 查表，O(1)。
`run.Config` 多一个注入点（`ProjectRootFor`）；**没注入就回落**到 `InputConfig.ProjectRoot`
（它从「进程的项目根」变成「默认项目根」）⇒ 既有测试一行不用改。

★ **解析不在 API 层**：「会话 → 项目 → 目录」是领域逻辑，住在 `projects` 包，`run` 调用它。
API 层只负责把 `projectId` 写进表、把 `projectId` 读出来。

## 5. 接口

```
GET    /v2/projects            → { items:[...], scope:{projectId,name,root}|null }
POST   /v2/projects            → 加一个项目
PATCH  /v2/projects/:id        → 改名 / 改 root / 改 order
DELETE /v2/projects/:id        → 删项目（会话回落为「未选项目」，不删会话）

POST   /v2/sessions            → body 带 projectId ⇒ 写进表
GET    /v2/sessions            → 不过滤；每条带 projectId（从表查）
```

★ `scope` 里的名字字段叫 **`name`**（不是 `projectName`）—— 它和 `items[]` 里那条项目的
`name` 是同一个东西，客户端拿到 scope 就能直接把它渲染成那一组的小标题。

★ `GET /v2/sessions` 的 `projectId` **从表查**（表是权威），
`pinned`（置顶）从会话查（那是会话属性，不是归属）。

★ 删项目时它的会话**不删**，只是回落成「未选项目」。

★ **没接注册表的服务器答 503，不是空列表**（`api/v2_projects.go`）：
「没有项目」和「这个版本不认识项目」是两件事。

### 5.1 36 个端点里只有 4 个动

| 类别 | 数量 | 变化 |
|---|---|---|
| **带会话 id 的**（`/v2/sessions/:id/**`） | 13 | ★ **一个字都不改** —— 会话 id 是 uuid 全局唯一（`storage/filestore/manager.go:310`），服务端从会话就能查出它的项目 |
| **进程级 / 配置级**（`/v2/agents`、`/v2/config`、`/v2/settings*`、`/v2/secrets*`、`/v2/mcps`、`/v2/skills`、`/v2/commands`、`/v2/health`、`/v2/runtime`、`POST /v2/run`） | 19 | ★ **不用加项目维度** —— 「项目 = 目录，配置共享」，这些端点讲的是**这一份配置**的事 |
| **动的** | 4 | 见下 |
| **新增** | 4 | `/v2/projects` 的 GET/POST/PATCH/DELETE |

★ 关键推论：**「项目 = 目录，配置共享」这条决定省掉了整个「每个端点加项目维度」的工作**。
若每个项目各有一套配置树，36 个端点全都要问「哪个项目」；现在只有 4 个动。

| 端点 | 变什么 |
|---|---|
| `GET /v2/project/files` | ★ 加 `?project=<id>` —— **唯一要加参数的端点**：@ 提及该列哪个项目的文件（`api/v2_project.go:95` 的 `projectRootOf`）。缺省 = 作用域项目，没有作用域 = 默认项目根。★ 名字不认识 ⇒ `404` + `codeProjectNotFound` |
| `POST /v2/sessions` | body 从 `{agentId}` 变成 `{agentId, projectId?}`；**响应**加 `projectId`。★ `projectId` 不认识 ⇒ `404`，而且**会话不会被创建** —— 一个指名了不存在项目的请求，不该留下一个无处归属的会话 |
| `GET /v2/sessions` | 响应每条加 `projectId`（**从 `projects.json` 查**） |
| `GET /v2/sessions/:id` | 响应加 `projectId` |

★ **`PATCH /v2/sessions/:id` 不接受 `projectId`** —— 「换项目 = 新建项目」，
就不搬会话，那条写路径不存在（`api/v2_sessions.go` 的 body 仍只有 `title`）。

★ `POST /v2/sessions` 的写表失败**不让会话创建失败**：会话文件已经落盘，那是事实；
归属写不进去，会话就是「未选项目」，日志报一条。

★ **删会话时把 id 从表里摘掉**（`api/v2_sessions.go`）：会话文件没了，表里还挂着它，
就是一个「属于某个项目、却不存在」的会话。

### 5.2 兼容性

- **老客户端**：不带 `projectId` ⇒ 会话是「未选项目」⇒ agent 在**默认项目根**干活。
  接口形状没变，客户端照常能用。
- ★ **一处行为变化**：老行为是「在哪个目录启动，agent 就在哪儿干活」
  （cwd 是默认项目根，`agentkit/spec/config.go:686-693`）。新行为里 cwd 不再自动是工作目录
  ⇒ **要保留这个直觉就写 `openheron .`**（位置参数会顺手把默认项目根也设成这个目录）。

## 6. 命令行与环境变量

### 6.1 flag

| flag | 覆盖什么 | 默认 |
|---|---|---|
| `--openheron-root` | openheron_root | `~/.openheron` |
| `--config-root` | 配置根 | `<openheron_root>/configs` |
| `--default-project-root` | 默认项目根 | `<openheron_root>/projects/default` |

★ `--project-root` 这个名字**退役**：它今天的含义是「agent 的工作目录」，
提案里那个位置由 `--default-project-root` 接手（语义更准：它是**默认**，不是唯一的），
而「进程关注哪个项目」由位置参数接手。★ **它仍然作为别名生效**，用了会打一条 warn
（`main.go:494-500`）—— 一个悄悄不再生效的 flag 是最难查的那种错误。
★ `--config-dir` 改成 `--config-root`：`init` 那边的 `--dir` 也改成 `--config-root`
（`initcmd.go:20-21`），**同一个概念不许两个名字**。
★ `openheron init` 的默认目标改成 **`<openheron_root>/configs`**（今天是 `<dir>/.openheron`，
`initcmd.go:61`）—— 它要写的是服务器默认读的那一份。

### 6.2 环境变量

| 变量 | 覆盖什么 | 旧名 |
|---|---|---|
| `OPENHERON_ROOT` | openheron_root | —（新） |
| `OPENHERON_CONFIG_ROOT` | 配置根 | `OPENHERON_CONFIG_DIR` |
| `OPENHERON_DATA_DIR` | 数据目录 | `DATA_DIR` |
| `OPENHERON_DEFAULT_PROJECT_ROOT` | 默认项目根 | `PROJECT_ROOT` |
| `OPENHERON_LISTEN_ADDR` | 监听地址 | `LISTEN_ADDR` |
| `OPENHERON_FRONTEND_MODE` | 前端模式 | `FRONTEND_MODE` |
| `OPENHERON_STATIC_DIR` | 前端目录 | `STATIC_DIR` |
| `OPENHERON_LOG_FILE` | 日志文件 | 已合规 |
| `OPENHERON_LOG_LEVEL` | 日志等级 | 已合规 |
| `OPENHERON_LOG_LLM_REQUESTS` | 请求体日志 | 已合规 |
| `OPENHERON_PROJECTS_FILE` | `projects.json` 的位置 | —（新，可选） |

★ **旧名仍然生效，但打一条 warn 点名新名字**（`main.go:587,599,760,774` 与
`agentkit/spec/config.go:641-698` 那几个读点）。
环境变量常写在 shell profile 或 CI 的 env 块里，**静默失效最难查** ——
用户以为改了端口，其实没改。

★ **不归 openheron 管、不加前缀的**：`EINO_EXT_SKILLS_DIR`（eino 的约定，
`agentkit/build/skill.go:190-233`）、`LLM_<PROVIDER>_API_KEY` / `MCP_<SERVER>_TOKEN`
（密钥命名约定，`agentkit/spec/secret.go:68-75`）、`MODEL_TYPE` / `OPENAI_*` / `ARK_*`
（provider 自己的名字，`agentkit/build/config.go:16-45`）。

★ 这条改名不是洁癖：`LISTEN_ADDR` / `DATA_DIR` / `STATIC_DIR` / `FRONTEND_MODE` /
`PROJECT_ROOT` 都是**通用名**，环境里别的工具都可能设。
CI 里有一个 `LISTEN_ADDR=0.0.0.0:9000`，openheron 的监听地址就被改了 ——
加前缀是把「这是谁的环境变量」写进名字里。

### 6.3 Kit 的两处连带

`sandbox-kit/openheron/entrypoint.sh`：

- `:23` `export LISTEN_ADDR="[::]:8080"` → `export OPENHERON_LISTEN_ADDR="[::]:8080"`
- `:13` `openheron init --dir "$PWD"` ⇒ 造出 `<workspace>/.openheron`，
  而新默认配置根是 `<openheron_root>/configs`，**服务器不再读它** ⇒ Kit 会造一份没人读的配置。

⇒ 修法：entrypoint 改成 **`export OPENHERON_CONFIG_ROOT="$PWD/.openheron"`** ——
显式点名工作区自带的配置。语义正好：Kit 的配置跟着挂载的工作区走。

## 7. 代码改动清单

★ 「状态」一列说这一条**落在代码里了没有**。已落地的那几行，行号是**改完之后**的。

| # | 位置 | 改什么 | 状态 |
|---|---|---|---|
| 1 | `agentkit/spec/config.go:599` `NewService` | 配置根解析：点名 → env → **`<home>/configs`** | ★ 已落地 |
| 2 | `agentkit/spec/config.go:318` `Service` | 加 `home` 字段 | ★ 已落地 |
| 3 | `agentkit/spec/config.go:784` `DataDir` | 相对基准 `s.root` → **`s.home`** | ★ 已落地 |
| 4 | `agentkit/spec/config.go:806` `LogFile` | 相对基准 `s.root` → **`s.home`** | ★ 已落地 |
| 5 | `main.go:121-147` `parseServerFlags` | 新增 `--openheron-root` / `--default-project-root`；`--config-dir` → `--config-root`；接受一个可选位置参数 | ★ 已落地 |
| 6 | `main.go:149-166` `printServerUsage` | `openheron [flags] [dir]` | ★ 已落地 |
| 7 | `main.go` 的 `warnUnusedHomeConfig` | ★ **删掉** —— home 现在是合法的大本营，不是「废弃的配置根」 | ★ 已落地 |
| 8 | `main.go:283-308` `initConfigIfEmpty` | 出厂树只写进**默认配置根** | ★ 已落地 |
| 9 | `main.go:481-623` `setupConfig` | 解析 openheron_root → 建 Service → 装默认配置树 | ★ 已落地 |
| 10 | `main.go:587,599,760,774` + `agentkit/spec/config.go:641-698` | 环境变量读点改名（+ 旧名 warn） | ★ 已落地 |
| 11 | `main.go:633` `setupRuntime`（`:650-666` 注册表接线，`:695-721` `run.Config`） | 数据目录**共享**；按会话取 project root | ★ 已落地 |
| 12 | `agentkit/run/loop.go:88` | `inputParams` 加 `ProjectRoot`（**从会话/项目取**） | ★ 已落地 |
| 13 | `agentkit/run/input.go:191` | prompt 的 `Project root:` 用会话的值 | ★ 已落地 |
| 14 | `api/v2_project.go` `projectRootOf` | `@` 提及按项目取 root（`?project=<id>`） | ★ 已落地 |
| 15 | `agentkit/fsx/context.go:25` + `agentkit/fsx/fsx.go:298` | 写边界 per-session（ctx 带本轮允许的根） | ★ 已落地 |
| 16 | 新 `projects` 包（`projects/projects.go:99` `Location` / `:111` `Registry` / `:122` `Open` / `:297` `mutate` / `:228` `RootOf`） | 类型 + 读写 + 文件锁 + `$OPENHERON_PROJECTS_FILE` | ★ 已落地 |
| 17 | `api/v2_projects.go:57/143/183/225` 四个端点 + `api/v2_sessions.go` / `api/v2_types.go:159` `threadMeta` | `/v2/projects` 的 GET/POST/PATCH/DELETE；`projectId` | ★ 已落地 |
| 18 | `initcmd.go:20-21,30-38,61` | `--dir` → `--config-root`；默认 `<openheron_root>/configs` | ★ 已落地 |
| 19 | `agentkit/templates/files/server/config.yaml:23-28,44-48` | 注释：相对基准改成 openheron_root（**值不变**） | ★ 已落地 |
| 20 | `scripts/test-backend.sh:132-136` | 环境变量改名 | ★ 已落地 |
| 21 | `sandbox-kit/openheron/entrypoint.sh:18-19,29,35,38` | 显式 `OPENHERON_CONFIG_ROOT`；`OPENHERON_LISTEN_ADDR`；`OPENHERON_DATA_DIR`；位置参数给项目 | ★ 已落地 |
| 22 | `agentkit/build/skill.go:198` `ResolveSkillsDir` | ★ **设计里没写，但不改就坏**：配置根进解析链，否则技能一个都加载不到（见 §8） | ★ 已落地 |
| 23 | `web/src/lib/projects.ts:95` `groupSessions` + `web/src/components/sidebar.tsx` | 侧栏分组 + 新会话选项目 | ★ 已落地 |

## 8. 落地顺序

| 阶段 | 内容 | 风险 | 状态 |
|---|---|---|---|
| **P1** | `openheron_root` + 目录基准 + 环境变量前缀统一 + `init` 默认目标 + `warnUnusedHomeConfig` 删除 | 低 | ★ **已落地** |
| **P2** | `projects` 包（读写 + 文件锁）+ `/v2/projects` 四个端点 | 中 | ★ **已落地** |
| **P3** | 会话归属（`projectId`）+ 侧栏分组（置顶 / 项目 / 未选项目）+ 单项目模式一组 | 中 | ★ **已落地** |
| **P4** | per-session project root（prompt / `@` 提及 / **fsx 写边界**） | ★ 高（唯一有架构代价的） | ★ **已落地** |

★ **P1 落地时多改了两处设计里没写的**，因为不改它们功能就是坏的：

1. **技能目录的解析**（`agentkit/build/skill.go:198` `ResolveSkillsDir`）多了一个参数 ——
   **配置根**。★ 配置根搬到 `<openheron_root>/configs` 之后，向上探测 `<cwd>/.openheron/skills`
   找的是**旧布局**，技能会**一个都加载不到**。现在顺序是：`$EINO_EXT_SKILLS_DIR` →
   `<配置根>/skills` → 向上探测。配置根排在探测前面，是因为配置编辑器就往那儿写。
2. **`openheron init` 的 `--dir` 退役**（`initcmd.go:30-39`）—— 它指的是**项目**目录，
   而 `init` 写的是**配置根**；拿旧名字当新名字会把配置写到没人要求的地方。
   旧名会报一条说清替代者的错。

★ **P3 与 P4 是一个整体发出去的**：分组按项目显示，而 agent 在同一个项目里干活 ——
「界面在说谎」的中间态没有存在过。

★ **P3 落地时多改了三处设计里没写的**：

1. **侧栏多了一个项目选择器**（`web/src/components/sidebar.tsx`）—— ★ 设计只说「前端一样传
   `projectId`」，但多项目模式下**新会话属于哪个项目**得有个说法。默认是 `order` 最小的那个
   （用户放在最上面的那个），用户可以改。
2. **`DELETE /v2/sessions/:id` 把 id 从表里摘掉** —— 设计只说了「删项目时会话不删」，
   反过来（删会话）也得让表知道，否则表里挂着一个不存在的会话。
3. **`POST /v2/sessions` 的 `projectId` 不认识 ⇒ 404，而且不建会话** —— 设计没写这条错误路径。
   ★ 理由：指名了不存在的东西还留下一个「无处归属」的会话，是更坏的结果。

## 9. 迁移：不做自动的

★ **提案不做任何自动迁移** —— 历史数据由用户手动处理。四步：

| # | 手动操作 | 怎么做 |
|---|---|---|
| 1 | 老配置搬家 | `~/.openheron/{agents,llm,mcps,tools,skills,server,policy}/` → `~/.openheron/configs/`（或 `--config-root` 指过去） |
| 2 | 老会话搬家 | `<项目>/.openheron/state/` → `~/.openheron/state/`（或 `data_dir` 指过去） |
| 3 | 老项目登记 | 在 `projects.json` 里给每个项目加一条（`root` = 那个目录） |
| 4 | 老日志 | 无所谓，留在原地 |

★ 首启**不会**自动搬任何东西：`<openheron_root>/configs` 不存在就创建，
并在里面写出厂树（`initConfigIfEmpty` 的老规矩，只是根换了地方）。

## 10. 不在范围内

| 事项 | 状态 |
|---|---|
| 项目级配置树（`projects[].configDir`） | 字段留口，**先不实现** |
| 把会话从 A 项目搬到 B 项目 | **不做** —— 换项目就是新建项目 + 新会话 |
| 会话记录里记 project 字段 | **不做** —— 归属只有表一个真相 |
| 一进程多配置树（每个项目一套 agents） | **不做** —— 配置共享是这一版的前提 |
| 自动迁移历史数据 | **不做** —— §9 的四步手动做 |
| 钥匙串 | 不变 —— 它是**全局 OS 资源**，本来就不按项目分（`main.go:393`） |
| 知识库的语料目录 | 不变 —— `corpus_dir` 仍相对**默认项目根**（`kb/tools.go:97`）。★ 语料是**一份**的：按会话切换它，就等于每个项目一份索引，而索引落在状态目录里的那一份 |

## 11. 与其它文档的边界

- `docs/config-design.md` —— 配置树的结构、文件规格、加载与优先级、热重载、`config_*` 工具。
  本文只说**配置根住在哪儿**，不说它里面有什么。
- `docs/runtime-design.md` —— 一轮是什么、租约与 fence、准入闸门、上下文与压缩、日志装配。
  本文只说**日志文件落在哪儿**。
- `docs/tools-design.md` —— 工具从哪来、`agentkit/fsx` 的边界、MCP、Docker Sandbox Kit。
  本文只说**写边界是 per-session**，边界本身的规则在那份文档里。
- `docs/ui-design.md` —— 附件与动效。本文只说**侧栏分几组**。
