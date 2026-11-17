# 项目与目录设计

> 一句话：运行状态住在**用户级的大本营**里，配置住在它下面；项目是一个目录，会话属于项目，project root 按会话切换。

> 范围：`openheron_root` / `config_root` / `data_dir` / 日志的落点，项目与会话的归属关系，
> 命令行与环境变量，接口与界面上的分组。
> 不包含：配置树自身的结构（见 `docs/config-design.md`）、运行时的一轮（见 `docs/runtime-design.md`）、
> 工具与集成（见 `docs/tools-design.md`）、前端与界面（见 `docs/ui-design.md`）。
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

★ **相对基准是 `openheron_root`，配置根不参与。**
`Service.DataDir`（`agentkit/spec/config.go:905-919`）与 `Service.LogFile`
（`agentkit/spec/config.go:921-941`）都把相对路径 join 到 `s.home`。
⇒ **配置搬到哪儿，数据和日志都跟着 openheron_root 走** —— 配置是配置，状态是状态。

★ 配置根的解析是四级（`agentkit/spec/config.go:682-703`）：
`--config-root` → `$OPENHERON_CONFIG_ROOT` → `$OPENHERON_CONFIG_DIR`（旧名，会被点名）→
`<openheron_root>/configs`（缺省时创建）。
★ 项目根**不推导**配置根（见 §2）：它是一个**作用域**，不是解析里的一级。
★ 只有**缺省派生的**那个根会被创建 —— 点名的根空着，是用户的决定。
★ **默认项目根照同一条规则**：缺省的那个会被建出来 —— 服务端启动时建
（`agentkit/spec/config.go:674-678`），`openheron init` 也建（`initcmd.go:56-69`）；
点名的那个不建，笔误不该长出一个空目录。

### 1.2 名字的由来

`openheron_root` 是**大本营**，`config_root` 是**配置**，两者不是同一个东西 ——
今天的 openheron 没有这个区分，因为配置根同时是「配置的家」「状态的家」和「项目的附属」。
拆开之后，配置根可以被 `--config-root` 指到任何一个目录，而状态留在大本营。

★ **`workspace` 这个名字不能用来指项目根**：会话的上传目录已经叫它了
（`storage.Session.Workspace()`，`storage/storage.go:108`；目录名 `workspace`，
`storage/filestore/session.go:763`；prompt 里那一行是 `storage.Session workspace: …`，
`agentkit/run/input.go:203`）。项目根的容器叫 **`projects/`**。

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

会话自己的记录（`storage/filestore/session.go:730-755` 的 `state` 结构）**不记 project** ——
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
老用户每个项目下的 `.openheron/` 都是服务端自动写的**出厂树**（`main.go:284-309`）。
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

多于一个位置参数报错（`main.go:122-148` `parseServerFlags`）。

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
| 空（多项目模式） | 置顶组 + 每个项目一组（按 `order`）+ 未选项目组（标题 `Default Project`） |
| 有值（单项目模式） | **一组**：作用域项目 = 该项目的会话 **+ 未选项目的会话** |

★ 单项目模式下「未选项目」的会话**并进**那一组，不单独成组 ——
进程只服务一个目录，那些会话没有别的地方可去。

★ **前端不许自己判断模式**：它读服务端给的 `scope`。界面读服务端的答案，不读自己的记忆。

★ **项目组一律画**（`web/src/lib/projects.ts:113` `groupSessions`）：表里有的项目就是一行，
哪怕它一个会话也没有。★ 那一行正是它的动作（改名、改目录、移除）住的地方 ——
一个没有行的项目，用户既改不了名字，也扔不掉。

★ **「空组不画」说的是装会话的篮子**：置顶组和「未选项目」组装的是会话，
空了就没有标题 —— 一个底下什么都没有的标题只是噪声。

★ **一个会话只在一个组里**：置顶的会话只在置顶组里 —— 一条出现两次的会话，
是用户数不清的会话。

★ **置顶组现在是空的**：★ 服务端**还没有** `pinned` 这个字段（代码里没有置顶这个功能），
所以 `meta.pinned` 恒为假。分组函数按设计支持它，等置顶功能落地就亮起来 ——
这不是谎报：没有这个标记就是「没置顶」，而今天确实没有置顶。

### 3.5 ★ 表的界面：两个写面，一边管新的，一边管已有的

★ **设计里没写界面上怎么建项目，写出来才发现那是最大的一个洞**：多项目模式
（`openheron` 不带目录参数）启动时，表是**空的**（`main.go` 只在 `scopeDir != ""` 时
`Ensure` 一个）⇒ **界面上没有任何办法建项目**，而后端的 `POST /v2/projects` 一直在那儿没人调。
⇒ 表的写面必须有一个**入口**，而且它**不能**挂在某个项目上 —— 空表正是最需要它的时候。

★ **入口在输入框的正下方**（`ComposerMeta` 那一行的右端，`web/src/components/projects-dialog.tsx`
的 `ProjectsButton`）：一个项目是**一段对话属于哪儿**的决定，而这件事在第一个字之前就定了。
★ 但它**只在空白页上是按钮** —— 对话页上它是**标签**（§3.6）：一段对话的项目改不了，
在那儿一个开关唯一能做的就是「离开这段对话」。

★ **点开是 `ProjectsDialog`，它管两件事**：**选一个**（列表第一项是 `Default Project` ——
也就是「没有项目」）与**新建一个**。★ **已有的项目**（改名 / 改目录 / 移除）在**侧栏它自己
那一行**上 ——
项目在那里被看见，就该在那里被操作。

| 决定 | 为什么 |
|---|---|
| ★ 入口**不在**侧栏 | 侧栏是**地图**：它管「有哪些项目、每个项目里有什么」，不管「你在哪儿干活」 |
| ★ 入口**不在**设置对话框 | 设置里放的是**配置**；项目表是**用户级状态**，不是配置树的一部分 |
| ★ **单项目模式没有这个入口** | 进程只服务一个目录，新建的项目永远选不上 —— 那是个装饰 |
| ★ **服务端不认识项目时（503）也没有** | `projectsKnown` 为假；给一个注定失败的入口比不给更糟 |
| ★ 新建的项目立刻成为当前项目 | 用户刚建的，接下来这段对话就是他要开始的那一段 |
| ★ **按钮上写着当前项目名** | 一个永远写着「Project」的按钮，对「现在在哪个目录里干活」一个字都没说 |
| ★ **改已有项目在侧栏那一行** | 项目在那里被看见，就该在那里被改（§3.6） |

★ **名字可选，目录必填**：项目**就是**一个目录，而名字可以由目录名推出来
（`projects.Registry.Create` 这么做）。★ **删项目不删会话**：会话回落成「未选项目」，
历史一点不丢 —— 摘掉一个标签不该把工作也摘掉。

★ **但改名时名字是必填的**：项目本来就有名字，空名字会把它拿走，服务端也是这么拒绝的
（`projects.Registry.Update`：「the project name must not be empty」）。⇒ **两处的规则不同，
就该有两个函数**（`projectNameError` / `projectRenameError`），而不是一个带开关的 ——
一个带开关的函数，调用处就得记住「这次是哪一种」。

★ **目录旁边有个文件夹按钮**：它调起**系统自己的**文件夹选择器，让用户不用手打绝对路径。
⇒ ★ **浏览器给不了绝对路径**（`webkitRelativePath` 只是相对路径，`showDirectoryPicker()`
只给名字），所以这个按钮走的是**服务端**：openheron 就在用户机器上，它去开那个窗口
（`agentkit/dirpick` → `POST /v2/fs/pick-directory`）。详见 `docs/pitfalls.md` §15.3。

★ **Escape 归平台管**：对话框里的 Escape 是 Radix 在 document **捕获阶段**接的，
React 的 `preventDefault` 来不及 ⇒ 唯一能拦住的是 `onEscapeKeyDown` 这个 prop
（详见 `docs/pitfalls.md` §15.2）。

### 3.6 ★ 「当前项目」只有一份真相

★ **「当前项目」这个概念天生有两个来源**：屏幕上的会话属于哪个项目，和**新建**的会话属于
哪个项目。⇒ 它们**必须是同一个值**，否则界面上会自己跟自己吵：输入框下方的标签说 A，
`@` 选择器列的是 B 的文件。

⇒ **`activeProjectId` 就是那一份真相**：它的意思是「输入框这头在哪个项目里干活」。
两个面读它，谁也不另存一份：

| 面 | 读的是 |
|---|---|
| 输入框下方的标签 `projects-label` | `selectActiveProjectId` —— 会话的项目优先，没有会话时就是 `activeProjectId` |
| `@` 选择器 | `selectActiveProjectId` —— 本来就是它 |

★ **侧栏的那个选择器被删掉了**：它是一个**开关**，而开关只属于「还没决定」的那一刻 ——
对话页上它的唯一后果是「离开当前会话」，和它的名字对不上。★ 侧栏现在是**一张地图**：
它管「有哪些项目、每个项目里有什么」，不管「你现在在哪儿干活」。

★ **会话打开时，当前项目跟着会话走**（`openSession` 与 `refreshList` 都做这件事）：
屏幕上这段会话属于哪个项目，输入框这头就在哪个项目里干活 —— 这不是猜，是会话自己的归属。
★ **会话列表到达时也得跟上**：启动时会话比列表先到，那时的会话还不知道自己属于哪个项目
（`refreshList` 拿到列表后补上）。

★ **`Default Project` 是一个选择，也是兜底**：项目对话框的列表**第一项**就是它（它代表的
就是「没有项目」），服务端也认 —— 会话落在默认项目根。所以 `resolveActiveProject` **保留
空串**，而且 **null**（「还没选过」）和「表里没有那个项目了」都给出空串。★ 兜底不该是表里
第一个项目：那是用户排在列表前面的，不是他给这段对话挑的目录。★ 一次刷新不该把用户自己
选的 `Default Project` 改掉。

★ **切换会离开当前会话**：会话的归属**不可改**（`PATCH /v2/sessions/:id` 不接受 `projectId`），
所以「切到那个项目」只能是「下一个会话在那个项目里」。⇒ 屏幕上的会话属于别的项目时，
切换把它**留在原地**（侧栏里、带着历史），输入框这头回到空白 ⇒ 下一个问题就在新项目里
另起一个会话。⇒ 这个判断是一个**纯函数**（`projectSwitch`）：它是这套语义里唯一需要
判断的地方，而需要判断的地方就该能被测。

★ **New thread 把项目也放掉**（`web/src/lib/store/actions.ts:442` `startNewThread`）：上一段
会话的项目是**那段会话的**，而会话的归属不可改 —— ⇒ 在用户为它选一个之前，新对话就是
「**没有项目**」，输入框下方的按钮写 `Default Project`，下一段会话落在**默认项目根**。
★ 单项目模式例外：进程只服务一个目录，作用域就是答案，那里没有「没有项目」可选。
★ 侧栏项目行的 **New conversation** 是反过来的一条路：先把输入框放空，再把那个项目放进去
（`web/src/lib/store/actions.ts:238` `startThreadInProject`）—— ★ 顺序反了，项目会被
`startNewThread` 抹掉。

★ **新建的项目走同一条路**（`createProject` → `switchProject`）：刚建的项目就是
「输入框这头在干活的那个项目」。

★ **已有的项目在侧栏它自己那一行上改**（改名 / 改目录 / 移除）：项目在那里被看见，就该在
那里被操作。★ **新建**项目却留在空视图的对话框里 —— 那是「下一段对话在哪儿干活」这个
决定的地方。⇒ 写面分两半，分界线是「新的」与「已有的」。

## 4. project root 按会话切换

会话的 project root = **它所属项目的 root**；未选项目的会话用**默认项目根**。

★ **四处必须永远一致** —— 任何一处不一致，model 就会在一个目录被告知去另一个目录读写：

| 面 | 在哪儿 | 现在是什么 |
|---|---|---|
| prompt 里的 `Project root:` | `agentkit/run/input.go:260` | **会话的** root |
| `@` 提及选择器 | `api/v2_project.go:95-116` 的 `projectRootOf` | **会话的** root |
| fsx 写边界 | `agentkit/fsx/fsx.go:283-292` 的 `writeRoots` | **本轮**的 root（ctx 带的那个） |
| `execute` 的工作目录 | `agentkit/fsx/fsx.go:1007` | **本轮**的 root（同一个 ctx 值） |

★ **第四处是漏掉的那一处**：它一度烤在后端自己的 `Root` 里，而那个 root 是**默认项目根** ——
后端建一次、被所有轮次共用，于是每个会话的命令都跑在「没有项目归属」的那个目录里。
★ 提示词写着会话的目录、写的边界也守着会话的目录，只有命令的工作目录在别处 ——
★ 一个跑在别处的命令，它的输出不说明它自己说明了什么。
（`grep` 的工作目录是同一处，`agentkit/fsx/fsx.go:578`。）

★ **读不用动**：fsx 本来就不设读边界（`agentkit/fsx/fsx.go:14-16`）——
`execute` 都能 cat 任意文件，把 `read_file` 关进笼子买不到安全。多根对读几乎没有成本。

★ **写边界是唯一有架构代价的一步**。后端是**共享**的：`Root` 在 builder 里定下
（`agentkit/build/snapshot.go:109`），
而 backend 被 `heavyOnce` 缓存（`agentkit/build/snapshot.go:85-89`），一个配置版本只建一次。
一个 backend 服务多个 root，只有两条路：

- **(a) ctx 带本轮允许的根**（**已落地**）：`fsx.WithProjectRoot(ctx, root)`，
  `resolveWrite` 额外查 ctx。工具方法都带 ctx（`agentkit/fsx/fsx.go:789`）⇒ 可行，
  而且**边界没有被放宽** —— 会话 A 写不了会话 B 的目录。
- (b) 把所有项目根塞进 `ExtraRoots`：代码少，但一个会话能写另一个项目的目录。

★ **落地时把名字改了一点**：设计里写的是 `fsx.WithWriteRoots`，代码里是
`fsx.WithProjectRoot`（`agentkit/fsx/context.go:25`）。★ 原因是同一个值要管**三件事**：
它既是「本轮允许写哪儿」（`agentkit/fsx/fsx.go:283-292` 的 `writeRoots`），
也是「相对路径的基准」（`agentkit/fsx/fsx.go:265-274` 的 `base`），
还是「命令的工作目录」（`agentkit/fsx/fsx.go:992-1034` 的 `initStreamingCmd` 里那一行）。
一个值、三个消费者 —— 叫 `WriteRoots` 会让人以为它只管写。
★ 三个消费者必须取**同一个**值：提示词说会话的目录、写边界守会话的目录，
而命令跑在别处，那个输出就不说明它自己说明了什么。

★ **子 agent 拿得到同一个 ctx，这不是新问题**：`task` 工具把收到的 ctx 原样交给子 agent
（eino `adk/prebuilt/deep/task_tool.go:174`，`a.InvokableRun(ctx, params, opts...)`），
框架自己还有一条测试断言子 agent 看得见父级的 ctx 值
（eino `adk/prebuilt/deep/deep_test.go:153`，`TestDeepSubAgentSharesSessionValues`）。

★ **root 现在必须传递**：它是**每轮可变**的（按会话），于是
- **prompt 侧**走显式参数：`inputParams` 加 `ProjectRoot`（`agentkit/run/input.go:29-32`）；
- **工具侧**只能走 ctx —— backend 是共享的，边界是**调用时**才知道的事。

### 4.1 root 在轮次开始时读一次，之后不变

★ **轮次进行中不能变更会话的所属项目**，而且 **root 每次都在轮次开始时读最新的值**。

⇒ 解析点就在轮次开始处，一轮之内固定：

```
Submit(ctx, sessionID, …)                       agentkit/run/runtime.go:242
 └ SessionManager.Get(ctx, sessionID)           runtime.go:243
 └ claimTurn(ctx, sess, …)                      runtime.go:255 → :292
 └ runClaimedTurn(ctx, sess, turn, loop, …)     runtime.go:271 → :451
     ★ runCtx, cancel := context.WithCancel(…)  runtime.go:461   ← 注入点
     ★ runCtx = fsx.WithProjectRoot(runCtx, …)  runtime.go:472   ← root 进 ctx
     └ startLoop(runCtx, sess.ID(), turn, loop) runtime.go:499
         └ loop.Run(ctx)                        agentkit/run/bridge.go:121
             ├ GenInput(ctx, …)                 agentkit/run/loop.go:43
             │   └ inputParams{ProjectRoot: …}  loop.go:88 → input.go:107-111
             └ 工具调用拿到的就是同一个 ctx
                ⇒ fsx.resolveWrite 查得到「本轮允许的根」  agentkit/fsx/fsx.go:307-331
```

★ **`runtime.go:472` 为什么是对的地方**：那里造出这一轮的 `runCtx`，
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

### 4.2 ★ 「项目根」只有一个家：这一轮的 ctx

★ **模型眼里的「项目根」曾经有两个来源，而它们说的是两个不同的目录**：

| 来源 | 曾经说什么 | 现在说什么 |
|---|---|---|
| 构建期的 agent 指令 | `The project root directory is: <openheron-root>/projects/default` | **不含任何目录** |
| 每轮上下文提示词 | 本轮会话的 root | 本轮会话的 root（不变） |
| 子 agent 的指令 | 什么也没有 | 本轮会话的 root（不变） |

★ **构建期那一行为什么是错的**：builder 是**进程级**的，它手里只有**默认项目根**
（`main.go:634` 的 `cfgSvc.DefaultProjectRoot()`），而会话的 root 是**每轮**的
（`agentkit/run/runtime.go:472` 从 ctx 读）。★ 两行同时在模型眼前，
模型挑了**读起来像规则**的那一行 ⇒ `.` 被翻译成默认项目根。

★ **修法不是「再添一句」，是「把家收成一个」**：

- **构建期指令不含路径**（`agentkit/build/spec.go:104` 的 `instruction`）：
  它只说「这一轮的工作目录由上下文给出」。
- **解析只有一处**：`agentkit/run/input.go:238` 的 `roundRoot` —— ctx 里的根优先，
  `inputParams.ProjectRoot` 与 `InputConfig.ProjectRoot` 只是回落。★ ctx 是唯一的家。
- **一份渲染，两个读者**：`agentkit/run/input.go:255` 的 `projectRootBlock`
  渲染那几行；root agent 从提示词读到它（`agentkit/run/input.go:189`），
  子 agent 从中间件读到它（`agentkit/build/projectroot.go:35`）。
- **子 agent 的中间件在运行时取根**（`agentkit/build/assemble.go:124`）：
  树是**按指纹池化**的（`agentkit/build/builder.go:189`），
  烤进去的目录会是「建树那一轮」的目录。

★ **为什么要有两条通道**：提示词不进子 agent —— 框架只把 request 交给它
（eino `adk/prebuilt/deep/task_tool.go:174`）—— 所以子 agent 必须另有一条。
★ 但两条通道**用同一个渲染**：`agentkit/run/input.go:286` 的 `RoundProjectRootBlock`
把 `run` 包的渲染交给 `build` 包（`agentkit/build/builder.go:106` 的 `WithRoundBlock`），
否则它们会各说各话。

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
| `GET /v2/project/files` | ★ 加 `?project=<id>` —— **唯一要加参数的端点**：@ 提及该列哪个项目的文件（`api/v2_project.go:95-116` 的 `projectRootOf`）。缺省 = 作用域项目，没有作用域 = 默认项目根。★ 名字不认识 ⇒ `404` + `codeProjectNotFound` |
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
  （cwd 是默认项目根，`agentkit/spec/config.go:756-765`）。新行为里 cwd 不再自动是工作目录
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
（`main.go:503-506`）—— 一个悄悄不再生效的 flag 是最难查的那种错误。
★ `--config-dir` 改成 `--config-root`：`init` 那边的 `--dir` 也改成 `--config-root`
（`initcmd.go:20-21`），**同一个概念不许两个名字**。
★ `openheron init` 的默认目标改成 **`<openheron_root>/configs`**（今天是 `<dir>/.openheron`，
`initcmd.go:76`）—— 它要写的是服务器默认读的那一份。

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

★ **旧名仍然生效，但打一条 warn 点名新名字**（`main.go:599,612,821,835` 与
`agentkit/spec/config.go:686-693` 与 `agentkit/spec/config.go:747-754` 那几个读点）。
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
| 1 | `agentkit/spec/config.go:645-725` `NewService` | 配置根解析：点名 → env → **`<home>/configs`** | ★ 已落地 |
| 2 | `agentkit/spec/config.go:320` `Service` | 加 `home` 字段 | ★ 已落地 |
| 3 | `agentkit/spec/config.go:905-919` `DataDir` | 相对基准 `s.root` → **`s.home`** | ★ 已落地 |
| 4 | `agentkit/spec/config.go:921-941` `LogFile` | 相对基准 `s.root` → **`s.home`** | ★ 已落地 |
| 5 | `main.go:122-148` `parseServerFlags` | 新增 `--openheron-root` / `--default-project-root`；`--config-dir` → `--config-root`；接受一个可选位置参数 | ★ 已落地 |
| 6 | `main.go:150-171` `printServerUsage` | `openheron [flags] [dir]` | ★ 已落地 |
| 7 | `main.go` 的 `warnUnusedHomeConfig` | ★ **删掉** —— home 现在是合法的大本营，不是「废弃的配置根」 | ★ 已落地 |
| 8 | `main.go:284-309` `initConfigIfEmpty` | 出厂树只写进**默认配置根** | ★ 已落地 |
| 9 | `main.go:488-638` `setupConfig` | 解析 openheron_root → 建 Service → 装默认配置树 | ★ 已落地 |
| 10 | `main.go:599,612,821,835` + `agentkit/spec/config.go:686-693` + `agentkit/spec/config.go:747-754` | 环境变量读点改名（+ 旧名 warn） | ★ 已落地 |
| 11 | `main.go:640-792` `setupRuntime`（`:654-668` 注册表接线，`:709-735` `run.Config`） | 数据目录**共享**；按会话取 project root | ★ 已落地 |
| 12 | `agentkit/run/loop.go:81` | `inputParams` 加 `ProjectRoot`（**从会话/项目取**） | ★ 已落地 |
| 13 | `agentkit/run/input.go:260` | prompt 的 `Project root:` 用会话的值 | ★ 已落地 |
| 14 | `api/v2_project.go` `projectRootOf` | `@` 提及按项目取 root（`?project=<id>`） | ★ 已落地 |
| 15 | `agentkit/fsx/context.go:25` + `agentkit/fsx/fsx.go:311-381` | 写边界 per-session（ctx 带本轮允许的根） | ★ 已落地 |
| 16 | 新 `projects` 包（`projects/projects.go:99` `Location` / `:111` `Registry` / `:122` `Open` / `:297` `mutate` / `:228` `RootOf`） | 类型 + 读写 + 文件锁 + `$OPENHERON_PROJECTS_FILE` | ★ 已落地 |
| 17 | `api/v2_projects.go:57/143/183/225` 四个端点 + `api/v2_sessions.go` / `api/v2_types.go:167` `threadMeta` | `/v2/projects` 的 GET/POST/PATCH/DELETE；`projectId` | ★ 已落地 |
| 18 | `initcmd.go:20-21,30-38,76` | `--dir` → `--config-root`；默认 `<openheron_root>/configs` | ★ 已落地 |
| 19 | `agentkit/templates/files/server/config.yaml:23-28,44-48` | 注释：相对基准改成 openheron_root（**值不变**） | ★ 已落地 |
| 20 | `scripts/test-backend.sh:132-136` | 环境变量改名 | ★ 已落地 |
| 21 | `sandbox-kit/openheron/entrypoint.sh:18-19,29,35,38` | 显式 `OPENHERON_CONFIG_ROOT`；`OPENHERON_LISTEN_ADDR`；`OPENHERON_DATA_DIR`；位置参数给项目 | ★ 已落地 |
| 22 | `agentkit/build/skill.go:198-231` `ResolveSkillsDir` | ★ **设计里没写，但不改就坏**：配置根进解析链，否则技能一个都加载不到（见 §8） | ★ 已落地 |
| 23 | `web/src/lib/projects.ts:113` `groupSessions` + `web/src/components/sidebar.tsx` | 侧栏分组 + 新会话选项目 | ★ 已落地 |
| 24 | `web/src/components/projects-dialog.tsx` `ProjectsButton` / `ProjectsDialog` | ★ **表的界面**：输入框下方的按钮（**写着当前项目**）；**选择**与**新建**（见 §3.5） | ★ 已落地 |
| 25 | `agentkit/dirpick` + `api/v2_dirpick.go` `v2PickDirectory` | ★ **写面之二**：目录旁的文件夹按钮 —— **服务端**调起系统自己的选择器（浏览器给不了绝对路径，见 §3.5）；只有本机调用者能用 | ★ 已落地 |
| 26 | `web/src/lib/projects.ts` `projectSwitch` + `web/src/lib/store/actions.ts` `switchProject` / `openSession` / `refreshList` | ★ **「当前项目」一份真相**（见 §3.6）：按钮切换、会话跟着走、`Default Project` 是一个选择 | ★ 已落地 |
| 27 | `web/src/components/sidebar.tsx` + `web/src/components/projects-dialog.tsx` | ★ **侧栏的选择器删掉**；输入框下方的按钮**分两种形态**（空白页是开关、对话页是标签）；`Default Project` 进列表第一项 | ★ 已落地 |
| 28 | `web/src/lib/projects.ts` `foldGroups` + `web/src/components/sidebar.tsx` | ★ **侧栏画成文件树**：三角 + 文件夹图标 + 缩进 + 折叠（记住在本机）+ `role="tree"` | ★ 已落地 |
| 29 | `web/src/components/sidebar.tsx` 的项目行菜单 + `web/src/lib/store/actions.ts` `updateProject` | ★ **写面搬进侧栏**：改名 / 改目录 / 移除都在项目自己那一行上 | ★ 已落地 |
| 30 | `web/src/lib/store/actions.ts` `startNewThread`（`:390`）/ `startThreadInProject`（`:208`） | ★ **New thread 把项目也放掉**：新对话在用户选之前是「没有项目」（§3.6）—— 上一段会话的项目不是它的；项目行的 New conversation 反过来，先放空再放项目 | ★ 已落地 |

## 8. 落地顺序

| 阶段 | 内容 | 风险 | 状态 |
|---|---|---|---|
| **P1** | `openheron_root` + 目录基准 + 环境变量前缀统一 + `init` 默认目标 + `warnUnusedHomeConfig` 删除 | 低 | ★ **已落地** |
| **P2** | `projects` 包（读写 + 文件锁）+ `/v2/projects` 四个端点 | 中 | ★ **已落地** |
| **P3** | 会话归属（`projectId`）+ 侧栏分组（置顶 / 项目 / 未选项目）+ 单项目模式一组 | 中 | ★ **已落地** |
| **P4** | per-session project root（prompt / `@` 提及 / **fsx 写边界**） | ★ 高（唯一有架构代价的） | ★ **已落地** |

★ **P1 落地时多改了两处设计里没写的**，因为不改它们功能就是坏的：

1. **技能目录的解析**（`agentkit/build/skill.go:198-231` `ResolveSkillsDir`）多了一个参数 ——
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

★ **落地后又补了一处设计里没有的：表的写面**（§3.5）—— 设计把 `POST /v2/projects` 写进了
API，却没写**谁**在界面上调它。★ 结果是多项目模式启动时表是空的、空表让侧栏选择器不渲染，
于是**界面上没有任何办法建项目**：后端有端点，前端只读。⇒ 加了输入框下方那个按钮
和那一个对话框（切换 / 新建 / 重命名 / 删除）。
★ 还顺手记了一条**前端会踩的坑**：那个入口的 `open` 状态住在 `ComposerMeta` 的一个 prop 里，
而 prop 的**下标会随 agent 徽章的分支变化** —— React 按下标认孩子，下标一变就是卸载重挂，
挂在里面的对话框就**自己关掉了**。修法是让入口在 DOM 里的位置**恒定**
（`web/src/components/composer-meta.tsx` 的 `contents` 包装）。详见 `docs/pitfalls.md` §15.1。

★ **界面上又提了一个要求：那个按钮要能切换项目**（§3.6）。⇒ 设计里原本只有「写面」，
没有「切换面」：项目表能写了，可**界面上没有一处能说「我现在在哪个项目里」** ——
侧栏那个选择器说的是「新会话属于哪个项目」，输入框这头是另一回事。
⇒ 于是把「当前项目」收成**一份真相**（`activeProjectId`），三个面读它；
★ 顺带发现两件事：**会话打开时当前项目要跟着会话走**（否则按钮和 `@` 选择器说的不是
同一个目录），**`Default Project` 是一个选择**（一次刷新不该把它改掉）。

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
| 钥匙串 | 不变 —— 它是**全局 OS 资源**，本来就不按项目分（`main.go:531`） |
| 知识库的语料目录 | 不变 —— `corpus_dir` 仍相对**默认项目根**（`kb/tools.go:97`）。★ 语料是**一份**的：按会话切换它，就等于每个项目一份索引，而索引落在状态目录里的那一份 |

## 11. 与其它文档的边界

- `docs/config-design.md` —— 配置树的结构、文件规格、加载与优先级、热重载、`config_*` 工具。
  本文只说**配置根住在哪儿**，不说它里面有什么。
- `docs/runtime-design.md` —— 一轮是什么、租约与 fence、准入闸门、上下文与压缩、日志装配。
  本文只说**日志文件落在哪儿**。
- `docs/tools-design.md` —— 工具从哪来、`agentkit/fsx` 的边界、MCP、Docker Sandbox Kit。
  本文只说**写边界是 per-session**，边界本身的规则在那份文档里。
- `docs/ui-design.md` —— 前端与界面。本文只说**侧栏分几组**。
- `docs/sidebar-design.md` —— 侧栏本身：分组、行、两个菜单、一份「当前项目」，以及还缺什么。
  本文只说**项目的表与项目↔会话的关系**；侧栏长什么样、菜单怎么组织在那份文档里。
