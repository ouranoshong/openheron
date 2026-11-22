# 授权模式设计（会话级）

> 一句话：会话多一个**授权模式**，三档 —— 默认 / 受信路径可改 / 完全授权；
> 它是**会话的属性**，随会话走；闸门按**轮次上下文**读它，工具面一个都不动。

> 范围：模式的三档语义、它住在哪儿（会话记录）、`/v2` 的线路形状、闸门怎么读它、
> 输入框下面那一行的界面，以及审计与测试。
> 不包含：档位表本身的语义（见 `docs/config-design.md`）、文件系统写边界
> （见 `docs/tools-design.md`）、运行时的一轮（见 `docs/runtime-design.md`）。
> 本文按 AGENTS.md 的文档语言规则**只有中文版** —— 架构与代码设计类文档不做双语。

★ **本文描述的是现状。** 授权模式已经落地：词汇在 `contract` 与 `agentkit/authz`，
模式住在会话记录（`storage/filestore/session.go`）里，闸门（`agentkit/build/gate.go`）
按轮次上下文读它，`/v2` 有 `PATCH /v2/sessions/{id}/authorization` 这条路由，
界面在输入框下面那一行。⇒ 下面凡是写「是」「就是」的地方，指的是**代码今天的样子**；
§11 记着它落地的顺序，§12 记着**已经定了的七条**，§13 记着那条「不堵」的决定
与它带来的两条后果。

## 0. 三档语义

| 档位 | 线上的名字 | 谁点头 | 放行什么 |
|---|---|---|---|
| 默认授权 | `default` | 人（现状不变） | 档位表说 `allow` 的直接跑，`ask` 的照旧问 |
| 受信路径可改 | `trusted` | 人，但**受信路径内的写不问** | `ask` 档里，**受信路径**内的写文件、以及**声明为受信写入的 MCP 工具**不再问；其余照旧问 |
| 完全授权 | `full` | 没有人 | `ask` 档全部直接跑 |

★ **受信路径 = 三处目录**：这一轮的**项目根**、这个会话的**上传目录**、
**配置根**。§2 说清每一处从哪儿来。

★ **三档都只决定「要不要点头」，不决定「有没有这个工具」。**
「没暴露」在这棵树里只由两处说：一个文件自己的 `allows:` 列表，一个 agent 自己的
`tools` 列表（`agentkit/spec/decl/decl.go:110-129`、`docs/config-design.md`）。
⇒ **完全授权不是「工具可见性」的放宽**：一个 agent 的 `tools:` 里没有 `write_file`，
那么完全授权也变不出 `write_file`。模式是闸门的参数，不是工具面的参数。

★ **完全授权也不越过写边界。** 后端写边界之外的目标，
`agentkit/fsx` 的后端**照旧拒绝**（`agentkit/fsx/fsx.go:307-331` 的 `resolveWrite`）——
那是技术边界，不是权限边界。模式回答「要不要人点头」，后端回答「这个路径能不能写」。
两个问题，两个地方。

★ **受信路径是后端写边界的子集 —— 这不是矛盾。**
后端允许写的是「项目根 + 数据目录 + 技能目录」
（`agentkit/fsx/fsx.go:283-292` 的 `writeRoots`），受信路径只多认了**配置根**，
少认了数据目录的其它部分与（在配置根外的）技能目录。
⇒ 两个集合，两个问题：后端答「能不能写」，模式答「要不要问」。

## 1. 为什么它是会话的属性

今天的档位是**进程级**的：`build.Policy` 在建树时从配置里装好
（`agentkit/build/policy.go:31-47`），同一棵树里所有 agent 共用一张表，
表的内容来自各工具文件自己的 `allows:`。

要让「这个会话不问、那个会话要问」，档位表就得分身 —— 而**它不能分身**：
agent 是**按指纹池化**的（`agentkit/build/builder.go:266-313`），同一棵树建一次，
所有会话共用同一个 agent 对象，闸门（`agentkit/build/gate.go:43-81`）是那条链上的一环。
⇒ 把模式烧进 agent，等于让一个会话的模式漏给所有会话。

⇒ **模式住在会话记录里，闸门在调用时从上下文读。**
上下文是**每轮**的（`agentkit/run/runtime.go:451-462`），所以它天然是每会话的。

★ **不放进 `POST /v2/run` 的请求体。** 那样的话，「谁问谁说了算」——
A 界面用完全授权跑了一轮，B 界面读到的却还是默认，两边对同一个会话的答案不一样。
而这棵树的一贯做法是「服务端的答案是权威」（`api/v2_run.go:24-33`）：
模式是**会话的属性**，任何界面问服务端都是同一个答案。

★ **不放进配置文件。** 配置树里的档位是**工具级**的（哪个工具不问），
模式是**会话级**的（这个会话里哪些不问）。两者混在一起，
「这一轮到底用哪张表」就会有两个答案。

## 2. 受信路径：三处目录

用户嘴里的「工作区可修改」，落在三处目录上 —— 这三处就是这一轮**属于你**的目录：

| 目录 | 是什么 | 从哪儿来 | 在中间档里 |
|---|---|---|---|
| **项目根** | 这一轮会话干活的目录 | 上下文里的 project root（`agentkit/fsx/context.go:25-30`） | 免问 |
| **会话上传目录** | 这个会话自己的上传目录 | 会话记录（`storage/storage.go:84` 的 `Session.Workspace()`） | 免问 |
| **配置根** | 配置树所在的地方 | 配置服务（`agentkit/build/configgate.go:30-46` 的 `newConfigGate`） | 免问 |
| 数据目录的其它部分 | 别的会话的目录、检查点 | `data_dir` | 照旧问 |
| 技能目录（在配置根外时） | 旧布局的项目内技能目录 | `agentkit/build/skill.go:198-231` | 照旧问 |

★ **线上的名字用 `trusted`，不用 `workspace`。**
这棵树里 `workspace` 已经被占了 —— 它指**会话的上传目录**
（`storage/storage.go:84` 的 `Session.Workspace()`，目录名 `workspace`，
`storage/filestore/session.go:666`）。`docs/projects-design.md` §1.2 专门写了这句：
**「`workspace` 这个名字不能用来指项目根」**。
而中间档的范围**不止一个目录**，拿任何一个目录的名字去叫它都会误导
（`workspace` 会被读成上传目录，`project` 会被读成只有项目根）。
⇒ 叫 `trusted`：**受信路径** —— 说清的是**规则**（这几处可信），不是**地方**。

★ **配置根在不在项目根里，取决于布局。**
配置根的解析是 `--config-root` → `$OPENHERON_CONFIG_ROOT` → `<openheron_root>/configs`
（`docs/projects-design.md` §1.1）。若 openheron 的大本营就在项目目录里
（`<projectRoot>/.openheron/configs`），那它**本来就在项目根内**，
中间档对它的放行是顺带的；若它在大本营里（默认的 `~/.openheron/configs`），
它就**不在**任何项目根里 —— ⇒ **显式把它列进受信路径，才让默认布局也成立**。

★ **把配置根列进来，意味着 `config_write` 在中间档也免问。**
这一条要认真掂量：agent 可以**无人值守地改配置**（改 agent、改工具的 `allows:`、
改策略）。⇒ 缓解是配置服务**自己**的检查，它们一个都不少
（`agentkit/spec/write.go:489-508`：路径可写性、密钥不得落盘、版本号、
**改完必须还能加载**、**工作中的 agent 不能消失**）——
「改坏了起不来」这一类错，后端自己挡得住；「改出一个更宽松的策略」这一类，
中间档放行了，但那是**人在界面上选了这一档**，不是 agent 自己决定放行自己。
★ 而完全授权档里，这件事也一样成立 —— 区别只是完全授权连 `execute` 都不问。

★ ★ **中间档下 agent 能改自己的档位。**
配置根在受信路径里 ⇒ agent 改 `tools/*.yaml` 的 `allows:`（把工具从 `ask` 挪到 `allow`）
或 `mcps/*.yaml` 的声明，都是**免问**的。⇒ 它可以把自己变成完全授权。
★ 这不是新问题，是「批准一次配置改动」这一步被去掉了 ——
今天改档位要人点头，点头之后它照样变宽（`docs/config-design.md` 讲的是档位这件事）。
⇒ 但中间档把**那一次点头**也去掉了，所以这个口子比今天**宽一档**。
⇒ **决定：不堵**（§13）—— 缓解是**看得见**，不是**拦得住**。

★ **判定按路径，不按目录名单。**
所以：项目根下的 `.openheron/skills`（技能目录的向上探测会找到它，
`agentkit/build/skill.go:233-249` 的 `probeProjectSkills`）也在受信路径内；
反过来，一个**符号链接**从项目根指向外面，那个目标**不在**受信路径内 ——
路径判定看的是它**落在哪**，不是它**从哪来**。

## 3. 数据面：模式住在会话记录里

会话状态落在 `state.json`（`storage/filestore/session.go:641-658` 的 `state` 结构，
原子重写，`storage/filestore/session.go:302-312` 的 `stateForWrite`）。
模式是**会话的一个字段**，与 `agent_id`、`title` 同级：

```
state.json
  { "version": 1, "title": "…", "agent_id": "main",
    "authorization": "default", … }
```

* `storage.Session` 有四个方法：`AuthorizationMode() contract.AuthorizationMode`、
  `SetAuthorizationMode(contract.AuthorizationMode) error`、
  `AuthorizationConfirmed() bool`、`SetAuthorizationConfirmed(bool) error`
  （`storage/storage.go:84-106`）。
* **缺省是 `default`**：老会话的 `state.json` 里没有这个字段，读出来就是 `default`
  —— 不是错误，也不是空档。⇒ **升级不需要迁移**。
* 写入失败即失败（像 `SetAgentID` 那样：先改内存再落盘，失败则回滚，
  `storage/filestore/session.go:509-529`）。⇒ 界面上的档位与盘上的一致。

★ **模式不进 `projects.json`。** 那是项目↔会话的归属表（`docs/projects-design.md` §2.2：
「归属只有这一份真相」）；模式是会话自己的属性，两处都记就会出现「表说 A、会话说 B」。

## 4. 线路面：`/v2` 的两个形状

```
GET   /v2/sessions/{id}                    → 多 authorizationMode 与 authorizationConfirmed 两个字段
PATCH /v2/sessions/{id}/authorization      → {"mode": "full", "confirm": true} → 200 + 会话元数据
POST  /v2/sessions                         → {"agentId", "projectId", "authorizationMode", "authorizationConfirmed"} → 201 + 会话元数据
```

* `authorizationMode` 与 `authorizationConfirmed` 加在 `threadMeta` 上
  （`api/v2_types.go:192-206`），**永远发**，像 `agentId` 与 `projectId` 那样：
  一个缺失的字段说的是「这个版本不知道有授权模式」，
  空字符串说的是「这个会话没有模式」—— 两件事。
  ★ `authorizationConfirmed` 也**永远发**：第二个界面要知道**不必再问**，
  否则它会多弹一次确认框。
* 单独一条路由，而不是并进 `PATCH /v2/sessions/{id}`：那条路由是**改名**
  （`api/v2_sessions.go:241-268`），而换 agent 已经单独开了一条
  （`PATCH /v2/sessions/:id/agent`，`api/v2_files.go:47`）。⇒ 一个关注点一条路由。
* 路由挂在 `api/v2_files.go` 的注册处（`api/v2_files.go:43-52` 那一段），
  跟会话的其它写路径放在一起；处理器在 `api/v2_authorization.go`。
* 未知的档位名 → **400 `invalid_body`**（`api/v2_types.go:28-64` 的错误码表）。
  一个不认识的档位名不能被悄悄当成 `default` —— 那会让用户以为自己放宽了。

★ **切到完全授权要一次确认（每个会话一次）。**
请求体多一个 `confirm`：

| 请求 | 结果 |
|---|---|
| `{"mode":"full"}`，本会话还没确认过 | **409 `confirmation_required`** —— 客户端应当弹确认框，重发时带 `confirm: true` |
| `{"mode":"full","confirm":true}`，本会话还没确认过 | 200，并记下「本会话已确认」 |
| `{"mode":"full"}`，本会话**已**确认过 | 200（不再问） |
| `{"mode":"default"}` / `{"mode":"trusted"}` | 200，**不需要**确认 |

* 「已确认」是**会话的一个字段**（与模式同级，`state.json` 里
  `"authorization_confirmed": true`）⇒ 它随会话走，随会话死。
  ⇒ 同一个会话里切走再切回 `full`，**不再问第二次**；换一个会话，**重新问**。
* 为什么在**服务端**拦：这一棵树的规矩是「服务端的答案是权威」。
  若确认只是客户端的事，一个忘了弹框的客户端就能把会话推进完全授权 ——
  而这恰恰是**最不该靠客户端自觉**的一步。
  ⇒ 服务端拒绝时给的是一句可执行的句子（`"full authorization needs an explicit confirmation"`），
  界面照它弹框，重发一次即可。
* 错误码表里多一个 `confirmation_required`（`api/v2_types.go:58-64` 那一段）。
  ★ 它是**冲突**（409）而不是「请求写错了」（400）：请求本身是好的，
  同一个请求体加上 `confirm: true` 就被接受 ⇒ 客户端读到它时的动作是
  「问那个人，然后重发」，不是「你的请求有问题」。

★ **会话也可以「生来」带着一个档位。**

`POST /v2/sessions` 的请求体多了 `authorizationMode` 与 `authorizationConfirmed`
两个字段，与 `PATCH` 那条路由**同形**：

| 请求 | 结果 |
|---|---|
| 不带 `authorizationMode` | 201，会话生来就是 `default`（老行为不变） |
| `{"authorizationMode":"trusted"}` | 201，会话生来就是 `trusted` |
| `{"authorizationMode":"full"}`，没有 `authorizationConfirmed` | **409 `confirmation_required`** —— 与 `PATCH` 同一个码、同一个理由 |
| `{"authorizationMode":"full","authorizationConfirmed":true}` | 201，会话生来就是 `full`，并记下「本会话已确认」 |
| `{"authorizationMode":"yolo"}` | **400 `invalid_body`** |

★ **为什么要有这条路**：欢迎页上**还没有会话**，而档位是**会话的属性**。
若会话只能生来就是 `default`、事后再改，第一轮就可能跑在错误的档位下 ——
那一轮读的是**它开始时**的那一档（§5.1），而改档位对**已经在跑**的那一轮无效。
⇒ 用户在欢迎页上选的档位，必须**跟着建会话的请求一起走**。

★ 形状与 `agentId` / `projectId` 一致：三者都是「会话**生来**就带着的东西」。

★ 校验**在建会话之前**做完（`api/v2_sessions.go:87-108` 的 `v2CreateSession`）：
一个不认识的档位名、一次没确认的完全授权，都**不留下会话**。

★ 档位**写失败就把刚建的会话删掉**（`storage.SessionManager.Delete`）——
「要么生来就是这个档，要么这个会话不存在」。

★ 操作日志记两行，与 `PATCH` 那条路由**同样的两行**：
`session.authorization_confirmed` 与 `session.authorization_changed`（§8）。

★ **改模式不需要会话空闲。** 换 agent 要空闲（`api/v2_sessions.go:354-365` 的
`ensureAgentIdle`），因为**换 agent 会换掉那一轮的 agent**。
模式不会：**这一轮已经跑起来了，它的模式就是它开始时的那一档**（§5）。
⇒ 界面在跑着的时候也能改，只是它说的是「下一轮生效」。

## 5. 执行面：闸门怎么读它

### 5.1 一轮读一次

`agentkit/run/runtime.go:451-462` 的 `runClaimedTurn` 里，
project root 是这样进上下文的：

```go
runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
runCtx = fsx.WithProjectRoot(runCtx, rt.projectRootOf(runCtx, sess.ID()))
```

模式走**同一条路**，紧挨着它：

```go
runCtx = authz.WithMode(runCtx, sess.AuthorizationMode())
runCtx = authz.WithSessionWorkspace(runCtx, sess.Workspace())
```

★ 受信路径的三处里，**两处是每会话的**（项目根、会话上传目录）⇒ 跟着上下文走；
**一处是进程级的**（配置根）⇒ 它由**检查器自己**拿着 ——
闸门那一侧知道配置服务（`agentkit/build/configgate.go:30-46`），
运行时不知道，也不该知道。

★ `WithoutCancel` **保留值**，所以模式会一路走到工具调用那里
（`agentkit/fsx/context.go:21-24` 记着这件事：框架把同一个上下文交给模型、
交给子代理、交给每一次工具调用）。

★ **一轮读一次，不是每次调用读一次。** 一轮里模式变了，这一轮不变 ——
和「这一轮的项目根不随项目改名而变」是同一条规则
（`agentkit/run/runtime.go:454-457`）。⇒ 界面改模式 ⇒ **下一轮**生效。

### 5.2 闸门的决策表

`agentkit/build/gate.go:88-119` 的 `WrapInvokableToolCall` 是这样：

```go
if m.policy.TierOf(name) == TierAllow {
    return endpoint, nil     // 不问
}
```

它先问 `waived`（`agentkit/build/gate.go:73-86`），再落到 `decide`：

| 档位表 | 模式 | 结果 |
|---|---|---|
| `allow` | 任意 | 跑（不变） |
| `ask` | `default` | 问（不变） |
| `ask` | `trusted` | **受信路径内的写文件**，以及**声明为受信写入的 MCP 工具**：跑；其余：问 |
| `ask` | `full` | 跑 |

★ **模式从上下文读，不从 agent 读**（`agentkit/build/gate.go:59-72`）：
agent 是**按指纹池化**的，烧进去的模式会从一个会话漏给所有会话。

★ **同步与流式两条路共用同一份决策。**
框架包装工具调用有两条路（`WrapInvokableToolCall` 与 `WrapStreamableToolCall`），
而 shell 的 `execute` 是 filesystem 那批工具里**唯一的流式**工具
（`docs/pitfalls.md` §1.2 就是这个坑）。
⇒ 新逻辑必须落在 `decide`（`agentkit/build/gate.go:126-157`）那一个地方，
两条包装都调它 —— 不是只改一条。

★ **`full` 也不能跳过「这一次调用是不是被截断了」那道检查**
（`agentkit/build/gate.go:174` 的 `newCallGuardMiddleware`）：那是**模型自己**的输出上限，
不是人的授权。⇒ 模式的闸门排在它**后面**（`toolChain` 的顺序不动），
所以那道检查照旧先跑。

### 5.3 子代理继承

`task` 子代理拿到的是**同一条工具链**（`agentkit/build/assemble.go:117-120`
的 `toolChain` / `subHandlers`，`agentkit/build/gate_test.go:16-33` 断言两者一样），
而子代理的调用跑在**同一个上下文**里。
⇒ **模式对子代理一样生效**：`main` 委托给 `task`，`task` 的 `write_file`
在 `trusted` 档下若落在受信路径里就不问。

★ 这正是 `smoke:hitl` 观察到的形状（`docs/pitfalls.md` §「审批」一段）：
审批真正发生在 `task` 的 `write_file` 上，不是 `main` 上。
⇒ 只在根 agent 上做模式，等于这个功能在真实形状下**不起作用**。

### 5.4 prompt 里那一行：告诉模型当前档位

★ **模型要知道自己这一轮处在哪一档** —— 不是为了好看，
是为了让它**少绕路**：在中间档里直接写文件，而不是拿 `execute` 去写
（`execute` 那一档照旧要问，§6）。

prompt 是每轮拼的（`agentkit/run/input.go:182-224` 的 `contextPrompt`），
而它**已经拿到上下文** ⇒ 这一行直接读模式，不必另开一条通道：

```go
// 一行，三种说法
mode := authz.ModeOf(ctx)
```

| 档位 | 那一行 |
|---|---|
| `default` | `Approval mode: default — tools ask before they run.` |
| `trusted` | `Approval mode: trusted — writes inside the project root, the session workspace and the configuration run without asking.` |
| `full` | `Approval mode: full — every tool runs without asking.` |

★ **三档都说**，包括默认档：模型知道「这一档要人点头」，
才不会以为工具卡住了是出了错。
★ 这一行放在 `[Context]` 那一段的**末尾**（附件之前）——
它是**环境**的事实，不是这一轮的问题。

## 6. 「受信路径」怎么判路径

闸门看到的是**工具名**和**参数 JSON**。要判「这次写落在受信路径里吗」，它需要：

1. **目标路径** —— 从参数里取。框架的写工具把路径放在 `file_path`
   （prompt 里就是这么教的，`agentkit/run/input.go:260-264`：
   `read_file(file_path="…")`）。
   ★ 实现时**要从工具自己的 schema 取参数名**，不要把 `file_path` 写死 ——
   框架改一个名字，这里就会静默地变成「不在受信路径里的写」，
   而症状是「怎么还在问」。
2. **三处根** —— 项目根与会话上传目录在上下文里
   （`agentkit/fsx/context.go:36-42` 的 `fsx.ProjectRoot(ctx)`，
   以及新增的 `authz.SessionWorkspace(ctx)`），
   配置根在**检查器自己**手里。
3. **判定** —— 归 `agentkit/fsx` 所有：路径解析、符号链接、根目录的规范化都在那儿
   （`agentkit/fsx/fsx.go:243-274` 的 `resolve` 与 `base`）。

⇒ **闸门那一侧放一个检查器**，它问 fsx 要判定：

```go
// InsideRoot reports whether the absolute path abs lands inside dir.
func (s *Local) InsideRoot(ctx context.Context, dir, abs string) bool
```

★ **单根的方法更稳**：路径解析与符号链接的走路逻辑只写一遍
（`agentkit/fsx/fsx.go:315-344` 的 `resolveInside` 那一套），
合并由闸门做 —— 于是「三处根」是**闸门的一个集合**，
而 fsx 只回答「这个路径在这个根里吗」。

⇒ 落法：`resolveWrite` 的走路逻辑抽成 `resolveInside(ctx, p, roots)`
（`agentkit/fsx/fsx.go:321-344`），
fsx 暴露两个单根的方法：`AbsolutePath`（`agentkit/fsx/fsx.go:353`）
与 `InsideRoot`（`agentkit/fsx/fsx.go:369`）；
闸门那一侧拿三处根，分别问一遍，**任一处答「在」就免问**
（`agentkit/build/scope.go:54-91` 的 `trusted`）。

★ **两族的参数形状不一样，闸门不能一刀切。**
文件系统工具给的是 `file_path`（绝对路径，或相对**项目根**的相对路径）；
配置工具给的是 `path`，**相对配置根**（`agentkit/build/configtools.go:356-362`：
`"path": {Desc: "relative path, e.g. agents/main.md"}`）。
⇒ 一律 join 到项目根，配置工具的 `agents/main.md` 会被算成
「项目根下的 agents/main.md」—— 一个**错的**答案。
⇒ 闸门要**按族解析**：文件系统族按项目根解，配置族按配置根解。
★ 配置族的相对路径**必然**落在配置根里（配置服务拒绝别处，
`agentkit/spec/write.go:489-494`）⇒ 中间档下它们一律免问。

★ **相对路径按项目根解。** `resolve` 已经是这么做的
（`agentkit/fsx/fsx.go:243-256`：相对路径 join 到这一轮的根上）——
所以闸门拿到 `"./src/main.go"` 与拿到绝对路径，答案一样。

★ **判不出来就问。** 参数不是 JSON、路径取不到、路径不在任何一处根里 ——
都走「问」。⇒ **fail-closed**：一个判不了的写，宁可多问一次，
不可少问一次。

★ **`execute` 不在中间档的放行范围里。** shell 能写任何地方，
受信路径这个框管不到它（`agentkit/fsx/fsx.go:17-19` 记着：读本来就不设限，
因为 `execute` 什么都能读）。⇒ `trusted` 档下 `execute` **照旧问**。
只有 `full` 档不问。

★ **`config_write` 在中间档免问**（配置根在受信路径里）。
它自己的检查一个不少（§2）：路径可写性、密钥不得落盘、版本号、
改完必须还能加载、工作中的 agent 不能消失
（`agentkit/spec/write.go:489-508`）。
⇒ 也就是说：中间档下 agent 能改配置，但**改不出一个装不上的配置**。

### 6.1 MCP 的写类工具：**由服务器自己声明**

MCP 工具的参数是**服务器自己的**（`agentkit/spec/decl/decl.go:820-825`：
「名字是服务器自己的，不是暴露出来的那些」），闸门读不出「这次写落在哪」。
⇒ 中间档要放行 MCP 的写类工具，只能靠**声明**，而声明的形状这棵树里已经有了：
**服务器自己的文件说它自己的工具**（`allows:` 就是这么用的）。

⇒ **`mcps/<server>.yaml` 多一个键**：

```yaml
# mcps/github.yaml
command: github-mcp
allows: [read_issue]          # 这两个工具不问（现状的语义）
writes: [create_issue]        # ★ 新键：这个工具写受信路径
```

* `writes:` 里是**服务器自己的工具名**（与 `allows:` 同一套词汇）。
* 语义：**中间档**（`trusted`）下，这些工具免问；
  **默认档**下它们照旧问（除非 `allows:` 也点了名）；
  **完全档**下一切都免问，不必提。
* 装配：`agentkit/build/mcptools.go:173-184` 的 `mcpAllowedNames` 旁边加一个
  `mcpTrustedWriteNames` —— 同样在**服务器还没起来**的时候就能从配置里算出
  「`mcp__github__create_issue` 是个受信写入」这张表。
* ★ **这条声明是用户的配置，openheron 验不了它。**
  服务器说「我只写项目里」，openheron 没法验证它真的只写项目里 ——
  与 `allows:` 一样：**信任来自那个文件**，而那个文件在你的配置根里。

★ **这条声明与「agent 能改自己的档位」叠在一起**：中间档下 agent 能改
`mcps/*.yaml`，也就能把**自己的**写工具加进 `writes:`。
⇒ 同样**不堵**（§13）—— 理由与后果都在那儿写着。

## 7. 界面：输入框下面那一行

位置：**输入框下面那一行的右端**，与「项目」按钮同一行
（`web/src/components/composer.tsx:713-754` 的 `<ComposerMeta … action={…} />`）。

★ **顺序固定，位置就是身份。** React 按**下标**对齐子节点，
一个下标变了就是「卸载了旧的、挂载了新的」，而挂在里面的状态全没了。
`ComposerMeta` 已经把这一行的内容包进一个 `contents` 里、把 `action` 放在最后
（`web/src/components/composer-meta.tsx:230-231`）。
⇒ 授权选择器、项目按钮与操作时间线的入口**一起**放进那一个 `action` 槽，
顺序永远是「授权 → 项目 → 活动」，多一个兄弟节点也不许换位。
★ 落法：三个入口被包进**同一个** `span`（`web/src/components/composer.tsx:744-752`），
那个 `span` 带着右对齐（`ms-auto`）—— 项目按钮自己那份 `ms-auto` 被去掉了，
几份都靠自动边距会各推各的。

★ **界面文案是英文**（AGENTS.md 的语言规则：interface copy → English）：

| 档位 | 文案 | 一句话说明（title） |
|---|---|---|
| 默认 | `Default approval` | Tools ask before they run, as the configuration says |
| 中间 | `Trusted paths` | Writes inside the project, the session workspace and the configuration do not ask |
| 完全 | `Full authorization` | Every tool runs without asking |

★ 这三段文案住在**一处**：`web/src/lib/authorization.ts` 的 `authorizationOptions`
—— 选项的顺序（从「问得最多」到「问得最少」）也是定死的，
一个每次渲染都换顺序的列表，用户就得每次重读一遍。

★ **完全授权要用警告色**（`--warning` 令牌，不是字面色值），
中间档用中性色。⇒ 用户一眼能看出「这一档不再有人看着」。
（`web/src/components/authorization-picker.tsx:161-169`。）

* 选中即写：`PATCH /v2/sessions/{id}/authorization`，失败就把界面弹回原值
  （界面不自己乐观更新 —— 服务端的答案是权威）。
  ★ 落法：store 的动作把**服务端回来的 meta** 合进会话条目
  （`web/src/lib/store/actions.ts` 的 `setAuthorizationMode`），失败时**再抛出去**，
  界面停在旧值上。
* **会话在跑的时候也能改**，但要写明「下一轮生效」——
  已经在跑的那一轮用的是**它开始时的那一档**。
  ★ 不能让它看起来已经生效了。
* ★ 落法：这句话写在**弹窗的描述**里
  （`web/src/components/authorization-picker.tsx:188-192`），
  **不**在按钮旁边挂一个「下一轮生效」的小牌子 ——
  这条规则**一直**成立，不是有条件才显示的；
  而一个会来来去去的小牌子会让按钮那一行文字跳。
  ⇒ 按钮上只有档位名（`web/src/components/authorization-picker.tsx:155-169`）。

★ **切到完全授权要弹一次确认框。**
流程：**先不带 `confirm` 发** ⇒ 服务端回 **409 `confirmation_required`**
⇒ 界面弹确认框 ⇒ 用户确认 ⇒ **带 `confirm: true` 重发**。

* 确认框里要写清**这一档意味着什么**，不是一句「确定吗」：
  `Full authorization — every tool runs without asking, including shell commands and configuration changes.`
  ★ 理由：这一档把人从回路里整个拿掉了，而「包括改配置」是最重的那一条（§2）。
* ★ **确认框不每次弹**：服务端记着「本会话已确认过」（§4），
  ⇒ 第二次切到 `full` 直接成功，界面**不该**再弹。
  ⇒ 界面不能自己记这个 —— **服务端说什么就是什么**
  （所以 `GET /v2/sessions/{id}` 也要带上「已确认」这个事实，
  否则第二个界面会多弹一次）。
* 确认框用的是现成的对话框（`web/src/components/ui/dialog.tsx`），
  **Escape 必须能关掉它** —— 而 Radix 在 document 上用捕获阶段听 Escape，
  所以要靠 `onEscapeKeyDown` 那个 prop（AGENTS.md 的「平台拥有的键」那条）。

### 7.1 欢迎页上：还没有会话，所以档位是**暂存**的

★ 欢迎页上**还没有会话**，而档位是**会话的属性** —— 所以那里的选择是**暂存**的。

* ★ **按钮不禁用**。它曾经是 `disabled={!activeId}` ⇒ 欢迎页上点不开，
  而档位又只能改一个**已经存在**的会话 ⇒ **欢迎页完全没法表达授权意图**，
  第一轮永远跑在默认档上（用户想「不询问」也得先发一条消息、再改档，
  而改动「下一轮生效」—— 那一轮已经跑在默认档里了）。
  ⇒ 按钮**一直**可点。
* ★ **暂存的是选择，不是事实**：store 里存着 `pendingAuthorizationMode` 与
  `pendingAuthorizationConfirmed`（`web/src/lib/store/state.ts`），
  它们**跟着建会话的请求一起走**（§4 的 `POST /v2/sessions`）。
  ⇒ 服务端仍然是权威：它在建会话时检查那一档，没确认的完全授权仍然是 409，
  写失败就把刚建的会话删掉 ⇒ 「要么生来就是这个档，要么这个会话不存在」。
* ★ **确认框在欢迎页上由客户端问**：有会话时那一次确认是**服务端**用 409
  触发的（§4），而欢迎页上没有会话可问 ⇒ 客户端自己问，
  服务端在会话诞生时**照样要求那个标志** ——
  「客户端问、服务端管」与 `PATCH` 那条路由是**同一套保证**。
  ⇒ 已经确认过的人**不再问第二次**（暂存里的那个确认标志跟着走）。
* ★ **弹窗的描述在两处不一样**：有会话时说的是「下一轮生效」；
  欢迎页上没有正在跑的一轮，说的是这个会话**生来**就在这一档里。

### 7.2 列窄时：只留盾牌

★ **列窄时**（聊天列 < 768px，★ 判据是列宽不是窗口宽 —— `docs/ui-design.md` §5.6.5）
入口**只留图标**（`docs/ui-design.md` §5.6.4）：
档位名让位，盾牌留着，而且它**和事实同一行**。

★ **让位的档位名不许丢**：按钮的 `aria-label`（读屏读到的名字）与
`title`（提示气泡）都还带着它 —— ★ `title` 现在是「档位名 — 档位说明」，
所以提示气泡里既说得清是哪一档，也说得清那一档意味着什么。
★ 而且点开弹窗就能看见当前档位（三个选项里的那个勾）。

## 8. 审计

* **决策日志只记人的决定**（`storage.ApprovalRecord`，`storage/storage.go:172-179`）。
  ⇒ 中间档与完全档**不写**决策记录：那里没有人做决定，
  写一堆「自动同意」会把「谁点了头」这条线搅浑。
* **但那一轮跑在什么档位下，得有地方能查** ⇒ **run 账本的行多一个字段**
  （`storage.Run`，`storage/runledger.go:176-185`）：`authorization_mode`。
  事后再看一次账本，就知道「那一轮是无人看管地跑的」。
  ★ 那一行在**这一轮开始时**写（`agentkit/run/runrow.go` 的 `noteAuthorizationMode`），
  写的值与这一轮上下文里的是**同一个读**（`agentkit/run/runtime.go:465-480`）——
  两次读可能不一致（用户可以在两次读之间改档位），而那会让账本说这一轮做过它没做的事。
* ★ **模式的变化不进会话的公告通道**（`GET /v2/sessions/{id}/events`）：
  那条通道带的是「审批被回答了」「新一轮开始了」这类**别人看不见就会卡住**的事实；
  模式变了，另一个界面顶多是显示旧值，而它下次刷新会话就看到了。
  ⇒ **不进**（这条已经定了）。
* ★ **档位表被放宽时留一行日志**（§13）：`[config] the tier table was widened` ——
  不阻止，但喊一声。中间档下 agent 能改自己的档位（§2 的 ★★），
  这一行是那条事实**看得见**的地方。
  ★ 它进的是**日志**，不是 notice：notice 是给**读答案的人**看的，
  而这一行是给**事后查的人**看的（`docs/runtime-design.md` §4 讲的是日志装配）。
* 工具调用本身照旧进 transcript（`Outcome.Intermediates`），
  ⇒ 「做了什么」在消息里，「当时允不允许」在账本里。两件事，两个地方。

## 9. 兼容性

* 老会话读出来是 `default` ⇒ **不改行为**。
* 老客户端不认识 `authorizationMode` ⇒ 多一个字段而已（`api/v2_types.go:159-183`
  的约定就是「永远发」）。
* ★ **闸门的默认分支是「问」**：模式没进上下文（老代码路径、单元测试直接造 agent）
  时，`ModeOf(ctx)` 返回 `default`，行为与今天一致。
  ⇒ **加这个功能不会放宽任何一条现有的路**。

## 10. 测试

| 层 | 用例 | 落在 |
|---|---|---|
| 词汇 | 三个名字都认；不认识的名字**报错**而不是回落默认 | `contract/authz_test.go` |
| 上下文 | 没进模式 ⇒ 读作默认；一轮的模式不漏给另一轮 | `agentkit/authz/context_test.go` |
| 闸门 | 三档 × {`allow` 工具, `ask` 工具} 的决策表 | `agentkit/build/gate_authz_test.go` |
| 闸门 | `trusted` 档：受信路径内外的 `write_file` 一次不问一次问 | 同上 |
| 闸门 | `trusted` 档：**三处根各测一遍**（项目根 / 会话上传目录 / 配置根） | `agentkit/build/scope_test.go` |
| 闸门 | `trusted` 档：数据目录的其它部分**仍然要问** | 同上 |
| 闸门 | `trusted` 档：`config_write` 的 `path` **相对配置根**解（不是项目根） | 同上 |
| 闸门 | `trusted` 档：`execute`（shell）**照旧问** | 同上 |
| 闸门 | `trusted` 档：MCP 工具**在 `writes:` 里**的免问、**不在**的照旧问 | 同上 + `agentkit/build/mcpwrites_test.go` |
| 闸门 | `default` 档：MCP 工具在 `writes:` 里**照旧问** | 同上 |
| 闸门 | 同步与流式**两条路**给出同一个答案（`execute` 是流式那个） | `agentkit/build/gate_authz_test.go` |
| 闸门 | 模式没进上下文 ⇒ 问（fail-closed）；没有后端的 scope ⇒ 问 | 同上 |
| MCP | `writes:` 是服务器自己的工具名；与 `allows:` 是两份名单 | `agentkit/spec/mcpwrites_test.go` + `agentkit/build/mcpwrites_test.go` |
| 上下文 | 一轮读一次；账本行记的是**这一轮**的档位 | `agentkit/run/runrow_authz_test.go` |
| prompt | 三档各拼出那一行；默认档也有；那一行在 `[Context]` 里 | `agentkit/run/input_authz_test.go` |
| 存储 | 落盘 → 读回；老 `state.json`（无该字段）读出 `default` | `storage/filestore/session_authz_test.go` |
| 存储 | 「已确认」随会话走；换会话重新问 | 同上 |
| 配置 | 档位表被放宽 ⇒ 记一行日志；没放宽（或是收窄）⇒ 没有 | `agentkit/spec/write_tier_test.go` |
| 接口 | `PATCH` 改档位 → `GET` 读得到；未知名 → 400；未知会话 → 404 | `api/v2_authorization_test.go` |
| 接口 | 切 `full` 不带 `confirm` → **409 `confirmation_required`** | 同上 |
| 接口 | 带 `confirm: true` → 200；再切一次不再要确认；换会话重新要 | 同上 |
| 接口 | 改档位**不**要求会话空闲 | 同上 |
| 接口 | `POST /v2/sessions` 带着 `authorizationMode` → 会话**生来**就是那一档 | 同上 |
| 接口 | 建会话时 `full` 而没有确认 → **409**，且**不留下会话** | 同上 |
| 接口 | 建会话时档位名不认识 → **400**，且**不留下会话** | 同上 |
| 接口 | 生来就带档位的会话，时间线记**同样那两行** | `api/v2_operations_test.go` |
| 前端单测 | store 的动作改档位；失败时界面回弹 | `web/test/authorization.test.ts` |
| 前端单测 | 409 → 弹确认框 → 带 `confirm` 重发 | 同上 |
| 前端单测 | 没有会话时**暂存**；建会话时把暂存带上去；确认是关于人的事实 | 同上 |
| 端到端 | 输入框下面那一行能选三档、选中即生效、刷新后还在 | `web/test/e2e/authorization.spec.ts` |
| 端到端 | 切到完全授权会弹一次确认框；确认后不再弹 | 同上 |
| 端到端 | **欢迎页上也能改档位**，它跟着新会话走 | 同上 |

★ **界面改动必须有一个 Playwright 用例**（AGENTS.md 的 Testing 一节）。
★ **`smoke:hitl` 那个前置条件仍然成立**：中间档要生效，
`task` 的 `write_file` 必须在 `ask` 档**且**在 `task` 自己的 `tools:` 列表里
（`docs/pitfalls.md` 那段「`smoke:hitl` 有一个前置条件」）。

## 11. 落地的顺序

★ 记下来是因为**顺序有讲究**：词汇 → 数据面 → 执行面 → MCP → 线路面 → prompt
→ 留声 → 审计 → 界面 → 文档。前一半是**后端自己的一条链**，
每一层只依赖前面已经定下的形状；界面那一层只依赖线路面的契约。

1. **词汇**：`contract.AuthorizationMode` + `ParseAuthorizationMode`（`contract/authz.go`）；
   `agentkit/authz`（模式与受信路径的上下文载体，照 `agentkit/fsx/context.go` 的样子写）。
2. **数据面**：`storage.Session` 的四个方法（模式 + 「已确认」）+ `state.json` 的字段
   （`storage/filestore/session.go`）。
3. **执行面**：fsx 的单根判定（把 `resolveWrite` 的走路逻辑抽成 `resolveInside`）
   + 闸门那一侧的三根合并（`agentkit/build/scope.go`）+ 决策表
   + `runClaimedTurn` 里的一次读取（模式 + 会话上传目录进上下文）。
4. **MCP 的声明**：`mcps/<server>.yaml` 的 `writes:` 键 + 加载器
   + `mcpTrustedWriteNames` + 闸门的那一条规则。
5. **线路面**：`/v2/sessions/{id}/authorization`（含 `confirm`）
   + `threadMeta.authorizationMode` / `authorizationConfirmed`
   + 错误码 `confirmation_required`。
6. **prompt**：那一行（§5.4）。
7. **留声**：配置写路径在档位表被放宽时记一行日志（§13）。
8. **审计**：run 账本行多一个字段 `authorization_mode`。
9. **界面**：选择器 + 确认框 + store + Playwright 用例。
10. **文档**：本文改成「现状」；`docs/config-design.md`（档位那一节 + `mcps/` 的文件规格）、
    `docs/api-v2-design.md`（路由表、请求形状与错误码表）、
    `docs/ui-design.md`（输入框下面那一行 + 确认框）、
    `docs/tools-design.md`（写边界与受信路径）各加一段。

## 12. 已经定了的七条

这七条是**决定**，不再是待决 —— 下面写的是它们各自**落在哪儿**：

| # | 决定 | 落在 |
|---|---|---|
| 1 | 完全授权**要一次确认**（每会话一次） | §4 的 `confirm` + 409 `confirmation_required` + 会话字段；§7 的确认框 |
| 2 | 模式**不进**会话的公告通道 | §8 |
| 3 | prompt **要**告诉模型当前档位 | §5.4 |
| 4 | 中间档**要**放行 MCP 的写类工具 | §6.1 的 `mcps/<server>.yaml` 的 `writes:` 键 |
| 5 | **不要**进程级的默认档位 | 不做 |
| 6 | 受信路径**不要**让用户自己配 | 不做（三处是代码里的三个事实） |
| 7 | 中间档下 agent 能改自己的档位 —— **不堵** | §13：改成「分配看得见」+ 档位表放宽时留一行日志 |

## 13. 中间档下 agent 能改自己的档位 —— **决定：不堵**

**决定：不拦。** 中间档就是「把配置也交给 agent」这一档的**代价**，
把它写成文档里的明话，而不是假装它不存在。

★ **缓解不是「拦得住」，是「看得见」** —— 分配要**清晰**，四个落点：

| 谁分配什么 | 在哪儿定 | 怎么看得见 |
|---|---|---|
| **模式**（这一轮是哪一档） | 人在界面上选，每会话一个值 | 输入框下面那一行；`GET /v2/sessions/{id}` 的 `authorizationMode`；run 账本行的 `authorization_mode`；prompt 里那一行（§5.4） |
| **工具**（哪些不问） | **配置里的档位表** —— `tools/*.yaml` 的 `allows:`、`mcps/*.yaml` 的 `allows:` 与 `writes:` | `GET /v2/config` 的 `policy.allow` 与 `tools` 表；`GET /v2/agents/{id}` 的 `effectiveTools` |
| **受信路径**（哪些写免问） | **代码里的三个事实**（项目根、会话上传目录、配置根） | prompt 里那一行；选择器那一行的 title |

⇒ ★ **「哪些工具不问」只有一份名单**（档位表）。
模式**不新增**第二套名单，它只是把**受信路径**加进那张表的判断里。
⇒ 于是「现在到底什么不问」是一次查询的事：
`GET /v2/config` 的 `tools` 表 + 那一档 + 那三处路径。

⇒ **不堵，但留声。**
配置写路径在**发现档位表被放宽**时记一行日志：
`[config] the tier table was widened` —— **不阻止，但喊一声**。
* 落法现成：写路径已经在算「改完的树」（`agentkit/spec/write.go` 的 `loadWith`），
  拿改前改后的 `AllowedTools()` 比一下（`agentkit/spec/load.go:449`）——
  新的 allow 名字多于旧的 ⇒ 放宽了 ⇒ 记一行。
  ★ 那一行进**日志**：配置服务本来没有 logger（它的「要告诉调用方的事」走
  notice），所以它多了一个 `WithServiceLogger` 选项
  （`agentkit/spec/config.go`），没接 logger 的调用方（库用户、测试）那边就是安静的。
* 这一行进**日志**（`docs/runtime-design.md` §4 讲的是日志装配），
  不是审批 ⇒ 它不拦任何人，但它在事后**查得到**。
* ★ 与「改了什么」那条线合起来：配置文件本身变了（原子写 + revision），
  档位表变宽了（日志有一行），这一轮跑在哪一档（账本有一行）——
  ⇒ **三件事各在一个地方**，凑起来是一次完整的事后复盘。

### 两条原本待决的小事 —— 已定

1. **`writes:` 声明要不要在装配时校验**（比如服务器真的提供了这个工具吗）？
   —— **要**：装配时若某个名字服务器没有，记一条 warning
   （`agentkit/build/mcptools.go`：`[mcp] the file declares a writer the server does not have`），
   与 `allows:` 那条同一种说法、同一处地方。别让它静静躺着。
2. **完全授权的确认，要不要也记进 run 账本？** —— **省**：
   账本行只记**模式**（`authorization_mode`），
   「已确认」在**会话记录**里查得到（`state.json` 的 `authorization_confirmed`）。
   再多一个字段就是同一件事的第二个家，而它带不来新的问题 ——
   模式本身已经说明「这一轮是无人看管地跑的」。
