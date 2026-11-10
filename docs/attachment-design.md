# 附件发送设计 —— 让「发附件」真的把文件送到模型眼前

> 状态：**已实施。** 本文描述的是 §1 那个**已经存在但没接通**的能力，
> 以及把它接通所需要的改动。§3 起每一节都标明了「现状」与「提案」，
> ★ 凡是标着「已实施」的段落，代码里已经有了；标着「提案」的段落仍然是提案。
>
> 范围：附件的上传→落盘→**随消息绑定**→进入模型上下文→回看→预览。
> 不在范围内：MCP 服务器的文件工具、sandbox Kit 里的文件传输、
> `docs/mcp-design.md` §8（那还是提案）。
>
> 框架版本：eino `v0.9.22`（`go.mod:9`）、
> AG-UI community go `v0.0.0-20261010025249-2aabfc8cada0`。
>
> 本文按 AGENTS.md 的文档语言规则**只有中文版** —— 架构与代码设计类文档不做双语。

---

## 0. 一句话

**附件必须是一条消息的属性，不是会话的属性。** 今天上传的文件停在会话的
workspace 里，与任何一条消息都没有关系；消息发出时附件就被丢掉了。
本设计让每条用户消息记住自己带了哪些附件，并让模型在那一轮就看见它们。

★ 之所以是「消息的属性」而不是「会话的属性」，是因为**只有消息级绑定才能回看**：
用户问「你刚才那张图里是什么」，模型得知道「刚才那张图」是哪一张。
会话级挂载只能回答「这个会话里有哪些文件」，答不了「刚才那张」。

---

## 0.1 ★ 当前实现（2026-11-10 起，代码以此为准）

2026-11-10 决定：**非图片附件不再内联、不再上线 file 块、不再有任何降级重试。**
本文 §4.4.1 之后的许多段落（尤其 **§15 全节**、§6.1 的图片降级、§7 的落地
清单与后文的验收表）是上一版实现的快照，保留作历史；**描述现状时以下表为准**。

| 环节 | 现在的代码 | 落点 |
|---|---|---|
| 绑定 | 附件清单仍是消息级，`Extra` 存 workspace 相对的 `path` | `message/attachment.go`、`api/v2_attachments.go` |
| 呈现 | 非图片**文本**附件在模型副本的消息头插入一行 `@绝对路径`；transcript 的 `Content` 不动 | `agentkit/run/attachments.go` 的 `attachmentPaths` / `presentMessage` |
| 回看 | 历史消息按 `Extra` 重建 `@` 头，不重新内联图片 | `agentkit/run/input.go` 的 `withHistoryAttachmentPaths` |
| 图片 | 仍是 `inlineImage` 的 image part，上限 `maxInlineImageBytes`；超限图片与二进制附件列入提示词「暂时不能处理」，**不给路径** | `agentkit/run/attachments.go` |
| wire | `llm/common/model.go` 一次请求；`llm/common/files.go`、`llm/common/vision.go` 及各自测试**已删除** | — |
| wire 形状 | `normalizeWireShape` 统一双形状；★ 另给「只有思维链、没有正文与工具调用」的历史 assistant 消息补一个可见占位正文（副本，transcript 不动） | `llm/common/shape.go` 的 `withVisibleAssistantContent` |
| 指令 | 上下文提示词把 `read_file` 写成义务，大文件用 `offset` / `limit` 分段读；模型没读由**用户再提示一次**，没有自动兜底 | `agentkit/run/input.go` 的 `attachmentRules` |

★ **绝对路径是硬要求**：`agentkit/fsx/fsx.go` 的 `resolve` 把相对路径按**项目根**
解析，而 `Extra` 里的 `path` 是 **workspace 相对** —— 直接把那个相对名字交给
`read_file` 会读到另一个文件（甚至项目根下的同名文件）。★ 路径含空白时写成
`@"..."`，提示词让模型调用时去掉引号。

★ `read_file` 的 `offset` / `limit` 定义在
`eino@v0.9.22/adk/middlewares/filesystem/filesystem.go:585-593`，工具层默认
一次最多返回 2000 行（同文件 `:181` 的用例验证）—— 分段读取不是一句口号，
工具真的支持。★ 二进制附件（PDF、压缩包等）先按 MIME 与扩展名判为
「暂时不能处理」，**不给** `@` 路径：对非文本字节调用 `read_file` 只会得到噪声。
这一块留待后续优化。

---

## 1. 问题：现状是什么，缺的是什么

### 1.1 已经有的部分（比「没做」要多）

上传这条链路的**大半段是真的通的**：

| 环节 | 落点 | 状态 |
|---|---|---|
| HTTP 上传接口 | `api/v2_files.go:112` `v2UploadFiles` | ✅ 已实现，multipart 多文件 |
| 大小上限 | `api/v2_files.go:110` `maxUploadBytes = 32 << 20` | ✅ 32 MiB |
| 路径逃逸防护 | `storage/filestore/session.go:628` `safeJoin` | ✅ 绝对路径与 `..` 一律拒绝 |
| 落盘位置 | `storage/filestore/session.go:552` `PutFile` → `<state>/<session>/workspace/` | ✅ 会话自己的 workspace |
| 列出附件 | `api/v2_files.go:377` `v2ListFiles` | ✅ |
| 前端上传入口 | `web/src/components/composer.tsx:505` `<input type="file">` | ✅ 纸夹按钮 |
| 前端附件 chip | `web/src/components/composer.tsx:477` | ✅ 名字 + 大小 + 移除 |
| `/attach` 命令 | `web/src/lib/commands.ts` → `attach_files` | ✅ |
| 模型能读到 workspace | `agentkit/build/snapshot.go:59` `roots := []string{s.dataDir}` | ✅ fsx 的 `ExtraRoots` 含 dataDir |
| 提示词告知 workspace | `agentkit/run/input.go:118` | ✅ "Files uploaded for this session live there" |

也就是说：**文件确实传上来了，模型也确实能 `read_file` 读到它们。**

### 1.2 缺的那一环：附件与消息之间没有绑定

★ **上传是会话级的，消息是消息级的，两者之间没有任何关系。**

断点有五处：

| # | 断点 | 证据 |
|---|---|---|
| 1 | **发送时附件被丢弃** | `web/src/lib/store/actions.ts:211` `send(text)` 只发 `{threadId, query}`；`web/src/lib/api.ts:464` 的请求体里没有附件字段 |
| 2 | **历史里看不出这条消息带了附件** | `agentkit/run/loop.go:66` 落盘的是 `message.NewUser(queryItem.Query)` —— 纯文本 |
| 3 | **刷新页面附件即失** | `web/src/lib/store/state.ts:20` `attachments: Record<string, Attachment[]>` 只活在内存里 |
| 4 | **「移除」只改前端** | `web/src/lib/store/actions.ts:202` `removeFile` 只过滤 store 的 map；服务端 workspace 里的文件还在 |
| 5 | **拖拽/粘贴完全没写** | `web/src` 里 `onDrop` / `onPaste` / `dataTransfer` 零匹配 —— 但 `docs/user-manual.md:768` 已经宣称 "supports drag/paste attachments" |

★ 第 5 条是**文档漂移**：用户手册承诺了一个不存在的能力。

### 1.3 后果：模型不知道你传了东西

`agentkit/run/input.go:120-121` 的提示词只说了「这个会话的 workspace 里有上传的文件」：

```
storage.Session workspace: /path/to/state/<session>/workspace
  Files uploaded for this session live there. Use filesystem tools with this path.
```

这句话有两个问题：

1. **它说的是「这个会话」，不是「这一轮」。** 模型看到的是一份**会话级清单**，
   而不是「用户刚刚附加了这三个文件」。用户传了 `report.pdf`，模型却要自己去
   `ls` 一遍整个 workspace 才能猜出哪个是刚传的。
2. **它把「文件名」和「意图」丢掉了。** 用户说的是「看看这个截图里的报错」，
   模型看到的是 workspace 里躺着 `a.png`、`b.png`、`c.png`。

★ 还有一个更硬的问题：**workspace 是持久的，会越积越多。**
一个会话传了 20 个文件之后，第 21 轮的提示词仍然只有一句
「文件在 workspace 里」—— 模型没有任何办法区分新旧。

### 1.4 ★ 顺带查出的既有缺陷：用户消息在模型输入里出现两次

这不是附件的问题，但**它决定了附件清单该挂在哪**，所以必须先说清楚。

★ **本节描述的是修复前的状态**（§1.4 末尾给出修复后的实测）。
修法见 §4.5 —— 那一节已实施。

`agentkit/run/loop.go`（**修复前**）：

```go
userMsg := message.NewUser(queryItem.Query)
message.StampNow(userMsg)
if err := sess.Append(userMsg); err != nil {   // ← 落盘了

built, err := rt.cfg.Input.buildInput(ctx, inputParams{
    History:   sess.Messages(true),              // ← 历史已经含这条
    Workspace: sess.Workspace(),
}, queryItem.Query)                              // ← 又追加一次
```

而 `agentkit/run/input.go`（**修复前**）：

```go
if query != "" {
    messages = append(messages, newUser(query))  // ← 第三份
}
```

**实测**（探针跑在真实的 `agentkit/run` 上，`fakeSession` 的 `Append`/`Messages`
语义与 `storage/filestore/session.go:270`/`:344` 一致）：

```
total messages=3, user 'hello world' count=2
  [0] role=system content="[Context]\n..."
  [1] role=user   content="hello world"
  [2] role=user   content="hello world"
```

模型看到同一条用户消息两遍。**这对附件设计是致命的**：如果附件清单挂在
「历史里的那条消息」上，而模型实际追问的是「最后追加的那条」，附件就丢了；
反过来挂在最后那条上，历史回看时又看不到。

★ 顺带一提，重复的**只有文本**，因为 `Append` 的那条和 `newUser(query)` 那条
是**两个不同的 `*schema.Message` 对象**——这一点在 §3 里会被利用。

★ **这个缺陷已经修好了**（§4.5），修法与附件功能无关，独立成立。
修复后的实测（同一探针）：

```
total=2, user 'hello world' count=1
  [0] role=system content="[Context]\n..."
  [1] role=user   content="hello world"
transcript user 'hello world' count=1
```

模型输入从 3 条变 2 条，transcript 仍是 1 份。

★ **修法是换顺序，不是加判定**：`buildInput` 先装配、装配成功才落盘，
所以历史里必然还没有本轮消息，模型看到的一份就是唯一的一份
（详见 §4.5）。回归用例见 `agentkit/run/input_duplication_test.go`。

---

## 2. 决策基线

| # | 项 | 决策 | 理由 |
|---|---|---|---|
| 1 | 绑定语义 | **消息级**：每条用户消息记住自己的附件 | 只有消息级能回看（「刚才那张图」） |
| 2 | 进入模型的方式 | **混合**：文本给路径让模型 `read_file`；图片走多模态内联 | 文本内联会吃光上下文；图片给路径模型看不见 |
| 3 | 附件的存放 | 仍在会话 workspace，**不放进消息体** | 消息体进 transcript，transcript 是 JSONL，base64 图片会把它撑爆 |
| 4 | 图片怎么送 | 请求时按需读盘 → base64 → `image_url` part | 模型要「看见」，不是「知道路径」 |
| 5 | 历史回看 | 附件清单随消息持久化，图片**按需重读** | 不把 base64 写进 transcript |
| 6 | 前端交互 | 拖拽 + 粘贴 + 纸夹**三件都要** | 用户手册已经承诺了拖拽粘贴 |
| 7 | 「移除」的语义 | 移除 = 从**待发清单**移除；已发的附件不删 | 已发消息的附件是历史的一部分 |
| 8 | 图片怎么落盘 | **服务端压缩**（只压 PNG/JPEG，最长边 1600px，保持原格式） | 压缩的收益在服务端才兑现（base64 是压后才编的） |
| 9 | 落盘的文件名 | **所有文件**都叫 `原名-哈希8位.ext`，哈希 = sha256(名 ‖ 大小 ‖ mtime) | 同名覆盖会让**记录说谎**（§13.1）；覆盖要治本，不能只治图片 |
| 10 | 预览 | **缩略图 + 点击放大**，字节走一个只读端点 | 用户要确认「我传的是不是那张图」（§14） |

★ 第 5 条是本设计里最省事也最正确的一条：**transcript 里只存「哪个文件」，
不存「文件的内容」**。图片在请求时现读现编。

★ 第 8-10 条的细节在 §13 与 §14。

---

## 3. 数据模型：附件清单挂在消息上

### 3.1 清单长什么样

`message.Extra` 已经是这套代码存「消息上的元数据」的地方
（`message/provenance.go:10-16` 一组 `openheron_*` 键，
`message/compaction.go:14` 的 `ExtraCompaction` 也是一个）。
附件清单是同一类东西，**跟着同一个机制走**：

```go
// message/attachment.go （新增）

// ExtraAttachments 标记一条消息带了哪些附件。
const ExtraAttachments = "openheron_attachments"

// Attachment 是附件清单里的一项。
//
// ★ Path 是**会话 workspace 里的相对路径**，不是绝对路径：绝对路径会
//
//	随机器变，写进 transcript 就成了一条只在那一台机器上成立的记录。
type Attachment struct {
    Name string `json:"name"`
    Path string `json:"path"`     // 相对 workspace
    Size int64  `json:"size"`
    MIME string `json:"mime"`
}
```

`put()`（`message/provenance.go:60`）已经是「只写一次」的语义，
`StampAttachments` 直接复用它。

★ **清单在 `Extra` 里存的是 JSON 字节，不是 `[]Attachment`。**

`Extra` 的值在 checkpoint 写入时走 **gob** 编码，而 gob 编不了
interface 里没注册过的类型 —— `[]message.Attachment` 会让 checkpoint
在 stop 的时候写失败（实测：`gob: type not registered for interface:
[]message.Attachment`），留下一半写坏的审批。

字节在白名单里（`string / int64 / int / bool / float64 / []byte`），
而 JSON 本来就是 transcript 载它的形式。所以：

```go
// StampAttachments 把清单写成 JSON 字节。
//
// ★ 存的是字节，不是 []Attachment：Extra 在 checkpoint 写入时走 gob，
//   而 gob 编不了 interface 里没注册过的类型 —— 一个 []Attachment 会
//   让 checkpoint 在 stop 时写失败。字节在白名单里。
func StampAttachments(msg *schema.Message, atts []Attachment)
```

★ **读的时候要认三种形态**（`ReadAttachments`）：本包写的 `[]byte`、
JSON 往返之后变成的 **base64 字符串**（JSON 把 `[]byte` 编成字符串），
以及调用方可能直接塞进去的 `[]Attachment`。JSON 往返之后的那种形态
才是 transcript 真正载着的形态。

### 3.2 为什么用 `Extra` 而不是消息的正文

★ 因为**正文是给模型看的，`Extra` 是给程序看的**。

- 模型的输入靠 `message.UserText(m)`（`message/extract.go:60`）取文本；
  `Extra` 不参与，模型不会看到 JSON 清单。
- `Extra` 会随 transcript 的 JSON 往返（`storage/filestore/transcript.go:159`
  `marshalLine` → `json.Marshal(msg)`），**不用改 transcript 的格式版本**。
- 它和 compaction 标记同构——那已经证明这条路走得通。

### 3.3 ★ 一个必须先解决的次序问题：挂在哪条消息上

§1.4 查出的重复问题在这里咬人。回顾一下模型实际收到什么：

```
[0] system  "[Context]..."
[1] user    "hello world"    ← Append 落盘的那条（在 History 里）
[2] user    "hello world"    ← buildInput 用 query 追加的那条
```

附件清单只有一份，该挂哪条？

★ **答案：挂在 `[2]`（buildInput 追加的那条）上，`[1]` 不挂。**

理由是**模型和 transcript 各取所需**：

- **模型**看的是 `[2]`——它是一轮里最后一条用户消息，是这一轮真正的问题。
  附件清单挂在它上面，模型这一轮立刻知道「用户附加了这三个文件」。
- **transcript / 回看**看的是 `[1]`——它是落盘的那条。

★ 这就要求：**`[2]` 必须在落盘时就被替换成 `[1]` 那一条**，
否则两份消息在 transcript 里就是两条记录，而模型只认后一条。

**§4 的做法就是让 `[2]` 不再是一个新的 `schema.Message`，而是 `[1]` 本身。**
这同时把 §1.4 的重复一并修掉——**一个改动，两个收益**。

---

## 4. 后端：把附件接到 run 上

### 4.1 契约：`/v2/run` 带附件

`api/v2_run.go:28` 的 `runInput` 加一个字段：

```go
type runInput struct {
    ThreadID string `json:"threadId"`
    RunID    string `json:"runId"`
    Query    string `json:"query"`

    // Attachments 是这一轮随问题带来的附件。
    //
    // ★ 只带**名字与相对路径**，不带内容：内容由服务端按 session 的
    //   workspace 解析。客户端给不出别的路径，也就无法让模型读到
    //   workspace 以外的东西。
    Attachments []runAttachment `json:"attachments,omitempty"`

    Messages []types.Message `json:"messages"`
    Resume   []resumeEntry    `json:"resume"`
}

type runAttachment struct {
    Name string `json:"name"`
    Path string `json:"path"`   // 相对 session workspace
    Size int64  `json:"size"`
}
```

★ **线上没有 content type。** 内容类型由**服务端按文件名判定**
（`message.MIMEOfExt`）：客户端要是把一个文本文件说成 `image/png`，
服务端就会去把一段文本当图片读，模型于是对着它没有看见过的图片作答。
判定不了的扩展名一律是 `application/octet-stream` —— 猜错的代价是模型
读不到文件，诚实作答的代价只是「它当文本读」。

★ **`Size` 是客户端的说法，服务端不信。** 服务端自己 `stat` 了一遍，
清单里记的是文件自己的大小。留着这个字段是为了让「客户端的说法」与
「文件的实情」有一处可以对照的地方，不是为了用它。

★ **`isAttachRequest` 要跟着改**（`api/v2_run.go:193`）：它现在用
「没有 query、没有 messages、没有 resume」来判断「这是重连而不是新问题」。
带附件的一轮**有** query，所以不受影响——但如果将来出现「只有附件没有文字」
的一轮，它必须仍被认成新问题。§4.5 给了判据。

### 4.2 解析：服务端认路径，但只认自己 workspace 里的

★ **客户端给的是相对路径，服务端拼的是自己的 workspace。**
这条规则让「越权读文件」在结构上不可能：客户端能写的字符串里没有绝对路径，
服务端只做一次 `filepath.Join(workspace, rel)` 再校验。

`api/v2_run.go` 新增：

```go
// resolveAttachments turns the client's relative paths into absolute ones
// inside this session's workspace.
//
// ★ A path that escapes the workspace is refused, not clamped: a clamped
//   path would silently read a different file, and the model would answer
//   about the wrong thing.
func resolveAttachments(sess storage.Session, in []runAttachment) ([]message.Attachment, error)
```

校验用 `storage/filestore/session.go:628` 的 `safeJoin` —— **同一个函数**，
不要另写一份。它已经处理了 `..`、绝对路径和 Windows 的 `\abs.txt`
（`session.go:79-86` `isAbsPath`）。

★ 顺带一提：`safeJoin` 在 `storage/filestore` 包内，是**未导出**的。
`api` 包要用它，要么把它导出，要么在 `storage.Session` 上加一个方法。
**建议后者** —— 见 §7.2。

★ **落地时用的是后者**：`storage.Session` 上多了两个方法，
`ResolveWorkspaceFile(name)` 与 `DeleteWorkspaceFile(name)`，
`filestore` 里各自转调 `safeJoin`。★ **越界的名字是拒绝，不是夹取** ——
夹取会解析到另一个文件，模型就会对着另一个文件作答，而且没有任何东西
说它读错了。

★ **路径必须是 workspace 里的相对路径，所以上传与列举接口返回的也是
相对路径**（`POST/GET /v2/sessions/:id/files` 的 `path`）。
客户端要把这个路径原样送回下一轮的问题里，它得是**服务端自己认得的
那个名字**，而不是某台机器上的一个目录。

### 4.3 ★ 落盘：清单写进那条用户消息

`agentkit/run/loop.go:66` 现在是：

```go
userMsg := message.NewUser(queryItem.Query)
message.StampNow(userMsg)
if err := sess.Append(userMsg); err != nil { ... }
```

改成：

```go
userMsg := message.NewUser(queryItem.Query)
message.StampNow(userMsg)
message.StampMessageID(userMsg, uuid.NewString())
// ★ The attachment list belongs to the message, not to the session: only
//   a message-level binding can answer "that image you just sent".
if len(queryItem.Attachments) > 0 {
    message.StampAttachments(userMsg, queryItem.Attachments)
}
// ★ Built first, persisted second — see §4.5. The model sees this very
//   object, so the attachment list on it reaches the model.
built, err := rt.cfg.Input.buildInput(ctx, inputParams{...}, userMsg)
if err != nil { ... }
sess.Append(userMsg)
```

`loopItem`（`agentkit/run/bridge.go:17`）加一个字段：

```go
type loopItem struct {
    Query string

    // Attachments are the files that came with this query.
    Attachments []message.Attachment

    Approvals map[string]any
}
```

### 4.4 ★ 装配：让模型看见附件

这是整个设计的核心。`agentkit/run/input.go` 现在把 query 变成一条纯文本消息，
改成**按附件类型分流**。

★ **`buildInput` 的签名已经是「收消息对象」了**（§4.5 已实施）：

```go
// buildInput returns the agent input for one turn.
func (c InputConfig) buildInput(ctx context.Context, p inputParams, query *schema.Message) (*adk.TypedAgentInput[*schema.Message], error)
```

★ **所以附件方案不需要再改这个签名** —— 附件清单盖在那条消息上，
`buildInput` 拿到的那条消息**就是**带清单的那条。这是 §4.5 的直接收益：
装配函数不用知道附件是什么，它只需要把消息原样交给模型。

#### 4.4.1 文本类附件：给路径

> ★ **本节先被 §15 推翻，随后 §15 也在同一天废弃（2026-11-10）**。
> 当前实现见 §0.1：文本附件在消息头给一行 `@` 绝对路径，由模型用
> `read_file` 读；不内联、不进 file 块、没有降级。下面这段「只给路径」的
> 论证保留下来，记的是当时为什么那么想。

模型已经有 `read_file`（fsx 后端），workspace 也在 `ExtraRoots` 里
（`agentkit/build/snapshot.go:59`）。所以文本附件**不需要内联**，
只需要**把清单告诉模型**：

```
The user attached 2 files to this message:
  - report.pdf (2.1 MiB) — /abs/path/workspace/report.pdf
  - notes.txt (18 B) — /abs/path/workspace/notes.txt
Read them with read_file if the question needs their content.
```

★ 路径给**绝对路径**，因为 `agentkit/run/input.go:92` 的提示词已经教模型
「总是给文件系统工具传绝对路径」，而 fsx 的 `resolve` 接受绝对路径。

#### 4.4.2 图片类附件：多模态内联

图片不一样：模型**要看见**，给路径等于没给。

eino 支持这条路（`schema.ChatMessagePartTypeImageURL`，
`eino@v0.9.22/schema/message.go:330`），
OpenAI 的 acl 也支持（`eino-ext/libs/acl/openai@v0.1.17/chat_model.go`
的 `toOpenAIMultiContent`）。

所以一条带图片的用户消息是这样构造的（`agentkit/run/attachments.go`
的 `inlineImage`）：

```go
func inlineImage(workspace string, att message.Attachment) (schema.MessageInputPart, error) {
    // 现读盘，现编码
    data, err := os.ReadFile(filepath.Join(workspace, att.Path))
    if err != nil {
        return schema.MessageInputPart{}, err
    }
    if len(data) > maxInlineImageBytes {
        return schema.MessageInputPart{}, fmt.Errorf(...)
    }
    b64 := base64.StdEncoding.EncodeToString(data)
    return schema.MessageInputPart{
        Type: schema.ChatMessagePartTypeImageURL,
        Image: &schema.MessageInputImage{
            MessagePartCommon: schema.MessagePartCommon{
                // ★ 字节进 Base64Data，类型进 MIMEType —— eino 推荐的是
                //   这个，不是 data: URL。
                Base64Data: &b64,
                MIMEType:   mime,
            },
            Detail: schema.ImageURLDetailAuto,
        },
    }, nil
}
```

★ **用 `Base64Data` 而不是 `data:` URL**：eino 的
`MessagePartCommon` 两者都收，但推荐的是前者，openai 的适配器也是按
它转成 provider 要的形状的。

★ **base64 现读现编，不落 transcript。** 这是 §2 决策 5 的兑现：
transcript 里只有 `{"name":"shot.png","path":"shot.png","size":...,"mime":"image/png"}`，
图片的字节在**每次请求时**从 workspace 读出来重新编码。

★ 为什么图片不落 transcript：`storage/filestore/transcript.go:22`
`maxLineBytes = 8 << 20`（8 MiB）是一行 JSONL 的上限。一张 2 MiB 的图
base64 之后是 2.7 MiB —— 一张就吃掉行上限的三分之一，五张就写不进去了。
★ 而且 transcript 是**追加写**的（`session.go:184` `O_APPEND`），
写坏了那一行，整条历史的尾部就断了。

#### 4.4.3 ★ 两个必须记住的形状约束

★ **约束一：给模型看的是一条副本，transcript 里的那条不动。**

装配（`agentkit/run/attachments.go` 的 `assembleAttachments`）**返回**一条
副本，而不是就地改消息。理由有两条：

- transcript 里不能有 base64（上面刚说过）；
- 副本与 transcript 那条**共用同一个 `Extra`**，所以 id 与附件清单照旧跟着
  消息走 —— 副本就这些事实而言还是那条消息。

★ **约束二：一条消息不能同时有 `Content` 和多模态 part。**

线上只有一个 `content` 字段，它要么是字符串，要么是列表。openai 适配器
（`eino-ext/libs/acl/openai@v0.1.17/chat_model.go` 的
`buildMessageFromUserInputMultiContent`）在有 `UserInputMultiContent` 时
**照样**把 `inMsg.Content` 填进 `Content`，于是两个都满了，客户端拒绝
序列化：

```
json: error calling MarshalJSON for type *openai.ChatCompletionMessage:
can't use both Content and MultiContent properties simultaneously
```

★ 所以副本的正文要**挪进一个 text part**，`Content` 清空：

```
UserInputMultiContent = [ {text: "图里写了什么？"}, {image_url: ...} ]
Content               = ""
```

★ 正文在前，图片在后：正文是问题，图片是问题问的东西。

⚠️ 这个坑只有在**真模型**上才暴露 —— 假后端不序列化，Go 用例不碰适配器。
所以它属于 `task smoke` 那一档，而不是 CI。

#### 4.4.4 ★ 约束三：形状在 **wire 边界**统一，不在装配层

★ **本节已实施。** 上面两条约束管的是**这一轮的消息**。但真模型上跑出来
的失败说明：光在装配层归一化**不够**。

装配只管得到**这一轮**的消息。**历史**是持久数据，它进模型时带着它自己的形状 ——
而 2026-11-09 之前的装配是**就地改消息**的（把图片 part 挂到消息对象上），
于是那条被挂过的消息连同 base64 一起写进了 transcript。实测一个真实会话的
transcript：

```
{"role":"user","content":"这个","user_input_multi_content":[{"type":"image_url",
 "image":{"base64data":"iVBORw0KGgo...","mimetype":"image/png"}}]}
```

★ 这条消息**同时有正文和图片 part**，所以它一进模型就撞上约束二 ——
哪怕这一轮的消息是干净的。而它不会自己好：会话不能改写自己的历史，
下一句关于它的话会以同样的方式死一次。

★ **所以归一化必须落在 wire 边界**，也就是消息最后一次成形的地方：

```go
// llm/common/shape.go
func normalizeWireShape(in []*schema.Message) []*schema.Message
```

`llm/common/model.go` 的 `Generate` / `Stream` 在交给适配器**之前**过一遍它。
放在这里有三个理由：

1. **它盖住所有来源。** 这一轮的、历史的、将来任何一条装配路径产出的消息，
   都在这里过一遍 —— 规则只写一次。
2. **它只影响发出去的那一份。** 副本，原消息不动（transcript 里那份不动）。
3. **它离约束最近。** 约束是 provider 的：线上只有一个 `content` 字段。
   在最后一层统一形状，等于让每个出口都拿到合法的形状。

★ **正文挪进 text part，`Content` 清空** —— 不是反过来。parts 是更丰富的形状：
它装得下图片，而一张没有正文的图片，模型认不出它讲的是什么事。

★ **已废弃的 `MultiContent`（老名字的同一个字段）被清空而不是合并**：
它在 wire 上已经没有位置了，合并只会造出第三种形状。

★ **同一个边界还兜住另一种脏历史：空的 assistant 消息。** 一轮只输出了思维链
就结束（思考预算耗尽 / 流被中途切断），会以空 `Content`、没有 `tool_calls`、
只有 `ReasoningContent` 的形状写进 transcript —— `message.HasContent` 把思维链
也算「有内容」，于是它被正常归档。而客户端的 `content` 是 `omitempty`，这条消息
在线上变成 `{"role":"assistant"}`，端点直接拒绝**整个请求**：
`Invalid assistant message: content or tool_calls must be set`。
`withVisibleAssistantContent` 在副本里补上 `noVisibleResponseText`，如实说明
「上一轮没有产出回答」，而不是编造一段回答；transcript 与思维链都不动。
带 `tool_calls` 的消息本来就合法，原样放过。★ DeepSeek 侧实测：`content` 键
存在（哪怕值为空串）即通过，完全没有 `content` 键才 400 —— 这正是 `omitempty`
造成的。

回归用例：`llm/common/shape_test.go`（12 例，其中 4 例覆盖空 assistant 消息）。

### 4.5 ★ 修掉重复：让最后那条消息**就是**落盘那条

★ **本节已实施。** 改法不是「判定历史里有没有这条」，而是
**换掉顺序**：`buildInput` 先装配，装配成功才落盘。

现在（`agentkit/run/loop.go`）：

```go
userMsg := message.NewUser(queryItem.Query)
message.StampNow(userMsg)
message.StampMessageID(userMsg, uuid.NewString())

// ★ Build first, persist second. The message the model sees is the
//   same object the transcript will hold, so anything stamped on it
//   — the id, an attachment list — reaches the model too. And a
//   build that fails leaves no half-written question behind: the
//   transcript only learns about the turn once the turn can start.
built, err := rt.cfg.Input.buildInput(ctx, inputParams{
    History:   sess.Messages(true),
    Workspace: sess.Workspace(),
}, userMsg)
if err != nil {
    fail := fmt.Errorf("assemble turn input: %w", err)
    turn.fail(fail)
    return nil, fail
}

if err := sess.Append(userMsg); err != nil {
    // ★ A failed write does not cost the model the question: the
    //   input is already assembled and carries it. The transcript
    //   loses the record, and the turn still answers.
    rt.logf("session=%s failed to persist user message: %v", sess.ID(), err)
}
```

`buildInput` 的签名随之改成**收消息对象**（`agentkit/run/input.go`）：

```go
func (c InputConfig) buildInput(ctx context.Context, p inputParams, query *schema.Message) (*adk.TypedAgentInput[*schema.Message], error)
```

★ **两件事同时成立**：

1. **重复从结构上消失。** build 时历史里还没有这条消息，
   所以「追加一次」永远是唯一的一份 —— 不需要任何判定。
   ★ 这比 §1.4 最初设想的「靠判定避免」更可靠：判定是猜的
   （用户重复问同一句话怎么办？），而顺序是确定的。
2. **模型和 transcript 共用同一个对象。** 盖在消息上的
   附件清单、消息 ID、provenance，模型这一轮就看得见。

#### 4.5.1 ★ 为什么不能简单地「删掉追加」

★ **直接删掉 `if query != "" { ... }` 那一段、而落盘仍在前，是有洞的。**

`sess.Append(userMsg)` 失败时，`agentkit/run/loop.go` **只记日志、不中断**：

```go
if err := sess.Append(userMsg); err != nil {
    rt.logf("session=%s failed to persist user message: %v", sess.ID(), err)
}
```

于是历史里**没有**这条用户消息。若此时 buildInput 又不再追加，
**模型这一轮就完全看不到用户的问题了** —— 一个写盘失败，换成了「模型答非所问」。

★ **换顺序正好解掉这个洞**：落盘移到 build 之后，
build 时用的就是那条消息对象本身，**无论落盘成不成功，模型都看得见它**。
所以顺序不是「先 build 后 append」的偏好，是**唯一自洽的顺序**。

⚠️ **这改变了现有行为，回归用例已落地**
（`agentkit/run/input_duplication_test.go`）：

| 用例 | 钉住的行为 |
|---|---|
| `TestBuildInputCarriesTheQuestionOnce` | 模型只看到一份问题 |
| `TestBuildInputCarriesTheMessageObject` | 模型看到的是**那条对象**本身（ID 也在） |
| `TestBuildInputKeepsBothWhenTheUserRepeatedTheQuestion` | 同一句话问两遍 → 三份都在（历史两份 + 本轮一份） |
| `TestBuildInputWithoutAQuestion` | 没有问题的轮次也合法 |

落盘侧的用例在 `agentkit/run/input_persist_test.go`
与 `agentkit/run/input_identity_test.go`：
落盘的消息带 ID；**模型看到的 ID 与 transcript 里的 ID 相同**。

### 4.6 ★ 只有附件、没有文字的一轮

用户可能只拖进来一张图，一个字没打。

- `api/v2_run.go:265` 现在会 `query == ""` → `errEmptyMessage`。
- ★ 但 `isAttachRequest`（`:193`）判的是「没有 query **且**没有 messages **且**没有 resume」。
  一个「只有附件」的一轮，query 为空、messages 为空、resume 为空 ——
  **它会被误判成「重连」**，于是去 `LiveRun` 找一个不存在的 run，返回 404。

所以 `isAttachRequest` 必须加上附件判据：

```go
func isAttachRequest(in runInput) bool {
    return strings.TrimSpace(in.RunID) != "" &&
        strings.TrimSpace(in.Query) == "" &&
        len(in.Messages) == 0 &&
        len(in.Resume) == 0 &&
        len(in.Attachments) == 0        // ← 新增
}
```

★ 这是「附件是消息的属性」这个决策的直接后果：**带附件的一轮永远是新问题**。

### 4.7 ★ 消息 ID：每条消息有自己的身份

★ **本节已实施。** 它不是附件功能的一部分，但附件方案挂在它上面。

#### 4.7.1 原来的 ID 是算出来的

`schema.Message`（eino）**没有 ID 字段**。openheron 在 AG-UI 那一层
自己造了一个（`api/v2_convert.go` 的 `stableID`）：

```go
// stableID derives a message id from content **and position**
b = append(b, []byte(message.Text(m))...)   // 内容
b = strconv.AppendInt(b, int64(index), 10)  // 位置
```

它是**内容 + 位置的哈希**，不是消息自己的身份。

★ **实测出来的缺陷**：ID 里含 `index`，而压缩会在 transcript 中间插入一条
边界消息 —— **后面所有消息的 index 都变了**：

```
before:  q2 的 id = msg-bbf8a2218036491d
after:   q2 的 id = msg-bbf89f2180364404     ← CHANGED
```

后果是具体的：前端 `seedFromHistory`（`web/src/lib/store/history.ts`）
用 `m.id` 当 React key，压缩前后的两次水合会给出**不同的 key**，
同一条消息被当成两条，React 会卸载重建整棵子树 —— 滚动位置、展开状态、
工具卡的结果全丢。

★ 而且 `GET /v2/sessions/:id/messages` 返回的是 `sess.Messages(false)`
（**完整** transcript），而模型看到的是**有效**视图（压缩边界之后）。
同一个消息在两个视图里 index 不同 → **ID 不同**。

#### 4.7.2 现在的做法：ID 存在消息自己身上

```go
// message/identity.go
const ExtraMessageID = "openheron_message_id"

func StampMessageID(msg *schema.Message, id string)
func MessageID(msg *schema.Message) string
func HasMessageID(msg *schema.Message) bool
```

★ 三个要点：

1. **存进 `Extra`**，不新增字段 —— `schema.Message` 是 eino 的类型，
   不能加字段；而 `Extra map[string]any` 就是为这个留的
   （`message/provenance.go` 的 `put`）。它随 transcript 的 JSON 往返，
   **不用改 transcript 的格式版本**。
2. **ID 必须是 `string`** —— checkpoint 用 gob 编码消息
   （`storage/filestore/checkpoint.go`），而 gob **不能编码接口里
   未注册的值**（`message/provenance_gob_test.go` 有一张类型白名单）。
   uuid 正好是字符串。
3. **落盘时生成一次，永不改变** —— `StampMessageID` 复用 `put` 的
   「先写为准」语义。

读取侧（`api/v2_convert.go`）：

```go
func messageIDOf(m *schema.Message, index int) string {
    if id := message.MessageID(m); id != "" {
        return id
    }
    return stableID(m, index)
}
```

★ **退回 `stableID` 是必须的**：旧 transcript 没有 ID，
它们仍然要拿到和从前一模一样的 ID，否则前端 reducer 的 key 就乱了。
**这就是这一刀能安全上线的原因。**

#### 4.7.3 落点：三处 + 一处

| 落点 | 盖的是什么 |
|---|---|
| `agentkit/run/loop.go` | 用户消息（`GenInput`） |
| `agentkit/run/loop.go` | 助手/工具的中间消息（`archive`） |
| `agentkit/run/loop.go` + `agentkit/run/compact.go` | compaction 边界 |
| `agentkit/emit/emit.go` | ★ 流式消息 —— 见下 |

★ **compaction 边界必须也有 ID**：它是唯一一条**落在 transcript 中间**的
消息，也就是唯一一条会给邻居改号的消息。

★ **`emit` 层那一处是关键**：`emit` 早就用 `uuid.NewString()` 生成
流式的 msgID（`agentkit/emit/emit.go` 的 `newStreamState`），而归档的
消息是**另一个对象**。所以同一条助手消息在流式里叫 `uuid-A`，
在 transcript 里叫 `uuid-B` —— **前端看到的是两条消息**。
现在 `emit` 把自己的 msgID 盖到消息上，两边就一致了。

实测（`agentkit/emit/identity_test.go`）：

```
streamed id=ba3bfd03-bf83-4d27-8462-52fbec864aed
archived id=ba3bfd03-bf83-4d27-8462-52fbec864aed
```

#### 4.7.4 验收

实测（压缩前后）：

```
before: "q1" index=0 id=id-q1
        "a1" index=1 id=id-a1
        "q2" index=2 id=id-q2
after:  "q1" index=0 id=id-q1        ← 不变
        "a1" index=1 id=id-a1        ← 不变
        "summary" index=2 id=id-marker
        "q2" index=3 id=id-q2        ← 不变（position 变了，id 没变）
```

★ 对照：**没有 ID 的消息**仍然会改号
（`msg-bbf8a22...` → `msg-bbf8a12...`）—— 这正是要覆盖的场景。

回归用例：`api/v2_message_id_test.go`、`message/identity_test.go`、
`agentkit/emit/identity_test.go`。

#### 4.7.5 这对附件方案的意义

附件清单挂在消息上（§3.1），消息有稳定 ID，于是：

- 前端渲染历史时，`Metadata["attachments"]` 里的清单**跟着那条消息走**，
  key 用真 ID，**压缩前后不变**；
- 用户问「刚才那张图」，模型从历史里读到的是**带附件清单的那条消息**，
  而不是「workspace 里有个叫 shot.png 的文件」；
- ★ **附件的删除粒度**也清楚了：删文件是文件级的事，
  而「这条消息引用了哪些文件」是消息级 —— 有稳定 ID 就能精确回溯。

---

## 5. 前端：三件交互都要，附件要活过刷新

### 5.1 发送：把附件带上

`web/src/lib/store/actions.ts:211` 的 `send(text)` 现在只发文字。附件清单要从
store 里取出来一起发：

```ts
async send(text) {
  // ...
  const attachments = get().attachments[threadId] ?? []
  run = runStream(
    {
      threadId,
      query: trimmed,
      attachments: attachments.map((a) => ({
        name: a.name,
        path: a.path,
        size: a.size,
        mime: a.mime,
      })),
    },
    // ...
  )
}
```

`web/src/lib/api.ts:464` 的请求体加 `attachments`。

★ **发完要清空待发清单**（`attachments[threadId] = []`），
否则下一轮会把同一批文件再发一遍。

### 5.2 乐观气泡：附件也要画上去

`web/src/lib/store/actions.ts:673` `appendLocalUserMessage` 现在造一条纯文本
消息。附件是这条消息的一部分，所以**乐观气泡里就该有附件 chip** ——
否则用户发出去的瞬间看不到自己发了什么。

### 5.3 拖拽与粘贴：把承诺补上

`web/src/components/composer.tsx` 的 textarea 上加三个 handler：

| 交互 | 事件 | 处理 |
|---|---|---|
| 拖拽文件进来 | `onDrop` | `e.dataTransfer.files` → `addFiles` |
| 拖拽悬停高亮 | `onDragOver` | `e.preventDefault()`（不 prevent 就不会触发 drop） |
| 粘贴截图 | `onPaste` | `e.clipboardData.items` 里 `kind === "file"` 的项 |

★ **粘贴截图是这个交互里最有价值的那个**：截图进剪贴板，用户按 Ctrl+V
就发出去了，没有「先存成文件再选」的中间步骤。

⚠️ 一个坑：**`onPaste` 里必须 `e.preventDefault()`**，否则浏览器的默认粘贴
会把图片的二进制塞进 textarea（textarea 收不了二进制，但某些浏览器会把
文件名作为文本插进去）。

### 5.4 ★ 刷新不丢：从会话的附件清单恢复

`web/src/lib/store/state.ts:20` 的 `attachments` 只在内存里，刷新即失。
★ 但**服务端已经有真相了**：`GET /v2/sessions/{id}/files`
（`api/v2_files.go:143`）列的就是 workspace 里的文件。

所以恢复的路径是：会话水合时（`reloadHistory` 一类的地方）拉一次
`listFiles`，填进 `attachments[sessionId]`。

★ 这里有个语义选择要说明白：**workspace 里是「会话的全部附件」，
而某条消息带的是「这一条的附件」**。两者不是一回事 ——
所以 `GET /v2/sessions/{id}/files` **不能直接**用来还原消息上的清单。
它只能还原**待发清单**（用户还没发出去的那些）。

★ **落地时补了一条判据**：workspace 里**没有任何消息认领过**的文件才是
「待发」。一条消息认领过的文件是**跟那条问题一起发出去的**，
把它放回待发清单，下一个问题就会再发一次 —— 用户只问了一次。
判据是拿文件清单和 transcript 里的消息级清单对一下
（`web/src/lib/store/actions.ts` 的 `restorePendingFiles`）。

★ 清单是**合并**而不是覆盖的：用户刚上传的文件比还在路上的那份列举新。

★ 真正的消息级清单在 **transcript 里**（§3.1 的 `Extra`），
所以历史水合时它会跟着消息一起回来（§5.5）。

### 5.5 ★ 历史回看：附件跟着消息回来

`api/v2_convert.go:20` `toAgUiMessage` 把 `*schema.Message` 映射成 AG-UI 的
`types.Message`。附件清单要在这里透出去：

```go
if atts, ok := message.ReadAttachments(m); ok {
    if out.Metadata == nil {
        out.Metadata = types.Metadata{}
    }
    out.Metadata["attachments"] = atts
}
```

★ 走 `Metadata` 而不是 `Content`：AG-UI 的 `types.Message.Content` 是
`any`（字符串或 `[]InputContent`），前端 `web/src/lib/types.ts:49` 的
`Message` 已经把内容拆成 `parts` 数组，而 `Part` 联合类型
（`types.ts:32`）目前只有 `text | reasoning | tool`。

前端要加一种 part：

```ts
export interface AttachmentPart {
  kind: "attachment"
  id: string
  name: string
  path: string
  size: number
  mime: string
}

export type Part = TextPart | ReasoningPart | ToolPart | AttachmentPart
```

★ **`Part` 联合类型加一个成员，要检查所有按 `kind` 分派的地方** ——
`web/src/lib/events.ts` 的 reducer 里就有好几处（`:221`、`:250`、`:282`、`:524`），
另有三处按 `"tool"` 精确匹配（`:499`、`:508`、`:511` 附近）。
漏一处就是附件渲染不出来。

★ **落地时这四处都改了**，而且改法不止是「加一个 case」：
`attachment` part **没有 `complete` 这个字段**，所以
`p.kind === kind && p.complete === false` 这种写法在联合类型变宽之后
**编译不过**（`kind` 是个变量，TypeScript 没法靠它收窄 `p`）。
抽了一个 `isOpenPart(p, kind)` 出来做收窄，四个地方都用它。

### 5.6 「移除」的语义要说清楚

★ 原来 `removeFile`（`web/src/lib/store/actions.ts:202`）只改前端 map，
服务端文件还在 —— **这是 bug**，但修的方向要小心：

- **待发清单里的附件** → 移除 = 真的删掉 workspace 里的文件。
  要加一个 `DELETE /v2/sessions/:id/files` 端点。
- **已发消息里的附件** → **不删**。它是历史的一部分，删了那条消息就说谎了。

★ 所以「删除文件」这个动作的粒度是**文件**，而附件清单的粒度是**消息**。
两个粒度不要混：一个文件可能被多条消息引用（用户可以两次引用同一个文件）。

★ **落地时端点是 `DELETE /v2/sessions/:id/files?path=<相对路径>`**：
`path` 是 workspace 里的相对名，越界的名字被拒（`DeleteWorkspaceFile`
走的是 `safeJoin`）。★ **删一个不存在的文件不是错误** ——
调用方要的是「这个文件没了」，而它确实没了。

★ 前端删失败时**要把 chip 放回去**：一个把服务端还留着的文件藏起来的
chip 是在说谎。

---

## 6. 图片内联的几个边界

| # | 问题 | 判据 |
|---|---|---|
| 1 | **哪些 MIME 算图片？** | `image/png`、`image/jpeg`、`image/gif`、`image/webp`。★ 不含 `image/svg+xml` —— SVG 是文本，能 `read_file`，走 §4.4.1 就够了 |
| 2 | **多大的图片能内联？** | 单独一个上限，比如 5 MiB（压缩后）。★ 32 MiB 的上传上限是给**文件**的，不是给**图片**的 |
| 3 | **超限了怎么办？** | **不静默降级** —— 走 §4.4.1 给路径，并在清单里标注「太大，未内联」。★ 静默降级会让模型以为它看见了图 |
| 4 | **模型不支持视觉怎么办？** | 内联会报错。★ 捕获它，降级成给路径，并让这一轮**继续跑** —— 而不是整轮失败。**✅ 已实现**，见 §6.1 |
| 5 | **base64 编码在哪做？** | **服务端**，请求时现读现编（§4.4.2）。★ 不在上传时编 —— 那会把 base64 存进 workspace，且上传响应要传 2.7 MiB 的字符串 |

★ 第 4 条是真实风险：`llm/common/model.go:68` 用的是
`eino-ext/libs/acl/openai` 的 client，它把 `MultiContent` 转成 OpenAI 的
多模态格式 —— 但**模型本身**支不支持 vision 是另一回事。

★ **实测（2026-11-10，DeepSeek 官方文档 + 真实请求）**：DeepSeek 的
`ChatCompletionRequestUserMessage.content` **明确接受内容块数组**，
part 有 `text` / `image_url` / `file` 三种，`image_url.url` 收 base64 data URL
（`data:image/jpeg;base64,...`），支持 JPEG / PNG / GIF / WebP，`detail`
枚举含 `auto`。**openheron 生成的形状与它逐字段对得上**，不需要改。

★ 但**同一个 API 下的不同模型能力不同**，实测：

| 模型 | 带图请求 | 结果 |
|---|---|---|
| `deepseek-flash` | HTTP 200 | ★ **看得见** —— prompt_tokens 1036，模型答出「五二小学山海二班」的圆形班徽 |
| `deepseek-v4-pro` | HTTP 200 | ⚠️ **静默忽略** —— prompt_tokens 只有 100（图片没进去），模型自答 "I cannot view the image" |

★ 所以「provider 拒绝」和「provider 收下但不看」是**两回事**：
§6.1 的降级只覆盖前者（4xx）。后者不报错，降级路径**不会触发**，
模型会自己说它看不见 —— 用户至少不会以为模型看过。

★ openheron **无法从配置声明一个模型有没有视觉**：
`agentkit/spec/decl/decl.go:38` 的 `decl.Model` 只有 ID / Kind / Dimensions /
ContextWindow / SummarizeAt / Timeout / Reasoning* / Extra，**没有 vision 字段**。
所以「这个模型支不支持图片」目前只能是 provider 的事实，不是 openheron 的配置。

### 6.1 ★ 模型没有视觉时：去掉图片，再问一遍

★ **落点**：`llm/common/vision.go` 的 `withoutImages` + `llm/common/model.go`
里 `Generate` / `Stream` 的重试。

★ **为什么落在模型层**：拒绝是 provider 抛的，而请求是在那里发出去的。
更早的层（装配）看不到这个错误，更晚的层（run loop）已经拿不到这次请求了。

★ **怎么做**：请求带着图片被拒 → **去掉图片，重问一遍**。文件还在 workspace
里，提示词里也点着它的名字（§4.4.1 的清单列了**所有**附件，图片也在内），
所以模型仍然知道有个文件在那儿。

★ **模型必须被告知**：重试的那条消息里多一个 text part，说明图片被拿走了
以及为什么。★ **不告知就是撒谎** —— 一个不知道自己图片没了的模型，会对着
它没看见的图作答，而用户分不出「模型看了但没找到」和「模型根本没拿到」。

★ **只重试一次，而且只在请求确实带了图片时**：别的失败照原样报出去 ——
第一次那个错才是真正的原因，一把坏钥匙不该变成「是图片的问题」。

★ **降级只覆盖「provider 明确拒绝」**。§6 的对照表里那个静默忽略图片的模型
返回的是 200，不报错，所以降级路径**不会**触发 —— 它会自己说看不见。
那条路是**模型的诚实**，不是 openheron 的兜底；openheron 能兜的只有
「请求被拒」这一种。

⚠️ 代价：带图失败时会多发一次请求。换来的是**整轮不死**。

---

## 7. 改动清单

### 7.1 后端

| # | 文件 | 改动 |
|---|---|---|
| 1 | `message/attachment.go`（**新增，已做**） | `Attachment` 类型、`ExtraAttachments` 键、`StampAttachments` / `ReadAttachments`；★ 清单存 JSON 字节（gob 白名单）；`MIMEOfExt` 按文件名判定类型 |
| 2 | `storage/storage.go`（**已做**） | `Session` 接口加 `ResolveWorkspaceFile` / `DeleteWorkspaceFile` |
| 3 | `storage/filestore/session.go`（**已做**） | 实现新方法；★ `safeJoin` 保持未导出，让接口来封装 |
| 4 | `api/v2_run.go` + `api/v2_attachments.go`（**新增，已做**） | `runInput.Attachments`、`isAttachRequest` 加附件判据、`resolveAttachments` |
| 5 | `api/v2_files.go`（**已做**） | `DELETE /v2/sessions/:id/files?path=`（删单个文件）；★ 上传与列举返回 **workspace 相对路径** |
| 6 | `agentkit/run/bridge.go`（**已做**） | `loopItem.Attachments` |
| 7 | `agentkit/run/loop.go`（**已做**） | 落盘时 `StampAttachments`；★ 调用 `buildInput` 传**消息对象**（见 §4.5） |
| 8 | `agentkit/run/input.go` + `agentkit/run/attachments.go`（**新增，已做**） | ★ **重复的 query 追加（已修，见 §4.5）**；图片内联；文本清单进提示；★ **装配返回副本**，正文挪进 text part（§4.4.3） |
| 9 | `api/v2_convert.go`（**已做**） | `toAgUiMessage` 透出 `Metadata["attachments"]`；★ 只有附件没有正文的消息**不再被丢掉** |
| 10 | `message/identity.go`（**新增，已做**） | ★ 消息 ID：`StampMessageID` / `MessageID`（见 §4.7） |
| 11 | `agentkit/emit/emit.go`（**已做**） | ★ 流式消息盖上自己的 msgID（见 §4.7.3） |
| 12 | `llm/common/vision.go`（**新增，已做**） | ★ 模型没有视觉时的降级：去掉图片重问一遍，并告知模型（§6.1） |
| 13 | `llm/common/shape.go`（**新增，已做**） | ★ **形状在 wire 边界统一**（§4.4.4）：正文挪进 text part、`Content` 清空、废弃的 `MultiContent` 清空 |
| 14 | `llm/common/files.go`（**新增，已做**） | ★ **file 块上 wire**（§15）：标记、file 块（扁平）、transport、端点拒绝时的降级 |
| 15 | `llm/common/model.go`（**已做**） | ★ 装 transport；file part 备成标记；降级链接上「先文件、后图片」 |

### 7.2 前端

| # | 文件 | 改动 |
|---|---|---|
| 1 | `web/src/lib/types.ts`（**已做**） | `AttachmentPart` + `Part` 联合类型 |
| 2 | `web/src/lib/store/state.ts`（**已做**） | `Attachment` 加 `mime` |
| 3 | `web/src/lib/store/actions.ts`（**已做**） | `send` 带上附件；发完清空；`appendLocalUserMessage` 造附件 part；`removeFile` 调删除端点；★ 被拒的一轮把附件**还回**待发清单；★ 刷新后按 workspace 恢复待发清单 |
| 4 | `web/src/lib/api.ts`（**已做**） | `runStream` 请求体加 `attachments`；新增 `deleteFile` / `listFiles` |
| 5 | `web/src/components/composer.tsx`（**已做**） | ★ `onDrop` / `onDragOver` / `onPaste` |
| 6 | `web/src/lib/events.ts`（**已做**） | ★ 检查所有 `switch (p.kind)`；`attachment` part 没有 `complete`，抽出 `isOpenPart` 做收窄 |
| 7 | `web/src/lib/store/history.ts`（**已做**） | 从 `metadata.attachments` 重建 `attachment` part |
| 8 | `web/src/components/message-parts.tsx` + `web/src/components/attachment-chip.tsx`（**新增，已做**） | 渲染 `attachment` part；★ 待发 chip 与历史 chip **是两个 test id** |

### 7.3 测试

| # | 位置 | 用例 |
|---|---|---|
| 1 | `agentkit/run/input_duplication_test.go`（**已落地**） | ★ **用户消息只出现一次**；模型看到的是那条对象本身 |
| 2 | `agentkit/run/input_persist_test.go`（**已落地**） | 落盘的消息带 ID；每条消息的 ID 不同 |
| 3 | `agentkit/run/input_identity_test.go`（**已落地**） | ★ **模型看到的 ID 与 transcript 里的 ID 相同** |
| 4 | `api/v2_message_id_test.go`（**已落地**） | ★ 压缩前后 ID 不变；无 ID 时退回派生的 |
| 5 | `message/identity_test.go`（**已落地**） | ★ ID 扛得住 checkpoint 的 gob 编码 |
| 6 | `agentkit/emit/identity_test.go`（**已落地**） | ★ 流式 ID 与归档 ID 一致 |
| 7 | `message/attachment_test.go`（**已落地**） | ★ 清单存 JSON 字节；★ 三种形态都读得回来 |
| 8 | `agentkit/run/attachments_test.go`（**已落地**） | 图片内联生成 `image_url` part；文本附件只给路径；★ 超大图片降级但**不静默**；★ 文件不在了**不**让整轮失败；★ 消息不能同时带正文和 part；★ transcript 里没有 base64 |
| 9 | `api/v2_attachments_test.go` + `api/v2_attachments_wire_test.go`（**已落地**） | 带附件的一轮**不被** `isAttachRequest` 误判；`..` 与绝对路径被拒；★ 清单从 metadata 透出；★ 上传路径是相对的；★ 删除端点 |
| 10 | `web/test/store-attachments.test.ts`（**新增，已落地**） | ★ 请求体带附件；★ 发完清空；★ 被拒时还回；★ 刷新后恢复待发清单 |
| 11 | `web/test/e2e/attachments.spec.ts`（**新增，已落地**） | ★ 附件随消息发出、出现在历史里、刷新不丢、拖拽、粘贴、★ × 删的是真文件 |
| 12 | `llm/common/vision_test.go`（**新增，已落地**） | ★ 拒绝图片的 provider 也能答；★ 重试不带图；★ 模型被告知；★ 别的错照原样报；★ 没有图就不重试 |
| 13 | `llm/common/shape_test.go`（**新增，已落地**） | ★ 双形状的消息到了 wire 只剩一种形状；★ 正文进 part、图片保位；★ 原消息不动；★ 单形状不复制；★ 盖章跟着走；★ 废弃字段被清空 |
| 14 | `llm/common/files_test.go`（**新增，已落地**） | ★ file part 在 wire 上是**扁平**的 file 块；★ 标记不漏到端点；★ 端点拒绝 file 块时这一轮仍然答得出来，重试带的是**文件内容**；★ 二进制文件被去掉且模型被告知；★ 标记不留在消息上；★ 没有文件的请求原样发出（§15.8） |

★ AGENTS.md 要求「**每一处界面改动都要有 Playwright 规格**」——
拖拽、粘贴、chip 渲染三处都有（`attachments.spec.ts`）。

---

## 8. 不做什么（以及为什么）

| # | 不做 | 理由 |
|---|---|---|
| 1 | **不做附件的会话级引用计数** | 一个文件可能被多条消息引用，引用计数会让「删文件」变成一个分布式问题。§5.6 的粒度是文件 |
| 2 | **不把 base64 写进 transcript** | §4.4.2。8 MiB 的行上限扛不住 |
| 3 | **不做附件的在线预览** | 那是另一个产品功能，且图片已经内联给模型看了 |
| 4 | **不把附件塞进 `messages` 字段** | `api/v2_run.go:60` 说得很清楚：`Messages` 是 AG-UI 的 transcript，**只读**，只有 query 是真的 |
| 5 | **不新增 `/v2` 端点来传附件** | ★ 附件走 `POST /v2/run` 的请求体。★ 新增端点要读 `docs/api-v2-design.md`（upstream，不在本仓库） |
| 6 | **不动 `docs/mcp-design.md` §8** | MCP 服务器的文件工具是另一条路，与本文正交 |
| 7 | **不做 WebP 与 GIF 的压缩** | Go 没有 WebP 编码器；GIF 解码只取第一帧，压缩等于毁掉动画。§13.5 |
| 8 | **不做基于内容的去重** | 哈希的输入是「名+大小+mtime」，它解决的是**命名唯一**（不覆盖），不是「同一张图只存一份」。后者要内容哈希，是另一件事 |
| 9 | **不改写已有的旧文件** | 旧会话里的文件没压也没改名，**继续能用**（§13.8）。回写它们要读每一个文件、猜它当初的样子 —— 猜错了就是改坏用户的记录 |
| 10 | **不做 Files API 的 `file_id` 形态** | ★ 那条路要先用 Files API 把文件传上去，拿到 `file-api-...` 才能填（§15.2）。而 DeepSeek 的 file 块**只收图片** —— 文本文件两条路都会被拒，所以它解决不了文本附件的问题；它要付出的代价是**文件先上传给第三方**，还要管 file_id 的生命周期。2026-11-10 决定：先不做 |

---

## 9. 文档连带修改

| # | 文件 | 改什么 |
|---|---|---|
| 1 | `docs/user-manual.md` + `_zh` | ★ 拖拽/粘贴从「已宣称」变成「真的」；附件清单随消息回看要写进去 |
| 2 | `docs/architecture.md` + `_zh` | 附件从「会话级」改成「消息级」—— 架构章节若提到 workspace 的语义，要跟着改 |
| 3 | `docs/developer-manual.md` + `_zh` | 已知问题表里如果提到消息重复，要删掉（§4.5 修掉了） |
| 4 | `docs/user-manual.md` + `_zh`（**§13/§14 实施时**） | ★ 上传的图会被压到最长边 1600px；★ **所有文件**的落盘名都会带哈希后缀，但界面上一律显示**原名**；GIF 与 WebP 不压；★ 缩略图与放大的用法 |
| 5 | `docs/developer-manual.md` + `_zh`（**§13/§14 实施时**） | ★ 新端点 `GET /v2/sessions/:id/files/raw`；★ 落盘名的哈希规则与可复算性；★ 原名映射（`uploads.json`） |

★ 三处都是**双语**文档，改一处必须改另一处，`task lint-docs` 会查。

---

## 10. 实施顺序

建议按这个顺序切，每一刀都能独立验证：

1. ~~**§4.5 单独一刀** —— 修掉用户消息重复。~~ **✅ 已完成。**
   ★ **它自己就是个 bug fix**，而且是附件方案的前置条件。
   已落地：`buildInput` 改收**消息对象** + **先 build 后 append**
   （`agentkit/run/input.go`、`agentkit/run/loop.go`），
   回归用例在 `agentkit/run/input_duplication_test.go` 等四处。
   ★ **它不依赖任何附件的东西**，所以即使附件方案被推翻，这一刀依然成立。
2. **§4.7 单独一刀** —— 给消息一个稳定 ID。**✅ 已完成。**
   ★ **它同样不依赖附件**：压缩会改掉幸存消息的 ID（实测见 §4.7.1），
   前端因此在每次压缩后重建整棵消息树。已落地：`message/identity.go`、
   `api/v2_convert.go`、`agentkit/emit/emit.go`。
3. **§3 + §4.1-§4.3** —— 清单的模型与落盘。此时模型还看不见清单，
   但 transcript 里已经有了 —— 可以用 `GET /v2/sessions/:id/messages` 验证。**✅ 已完成。**
4. **§4.4** —— 装配：文本给路径、图片内联。模型第一次真的看见附件。**✅ 已完成。**
5. **§5.5 + §7.2** —— 前端回看。历史里能看到附件 chip 了。**✅ 已完成。**
6. **§5.1 + §5.3 + §7.3** —— 发送带附件、拖拽、粘贴、E2E。**✅ 已完成。**
7. **§5.6 + §7.1(5)** —— 删除语义收尾。**✅ 已完成。**
   ★ 端点是 `DELETE /v2/sessions/:id/files?path=`，路径相对 workspace，
   越界的名字被拒。
8. **§4.4.4** —— 形状在 wire 边界统一。**✅ 已完成。**
   ★ **它是被真模型逼出来的**：装配层已经归一化了这一轮的消息，
   但历史里 2026-11-09 之前写的消息仍然带着双形状，一进模型就死。
   落在 `llm/common/shape.go`，在 `Generate` / `Stream` 交给适配器之前过一遍。
9. **§13** —— 图片落盘：压缩 + 哈希命名。**✅ 已完成。**
   ★ **它必须在预览之前**：预览要显示的就是压缩后的那张图，
   顺序反了会看到两张不一样的图。
   已落地：`agentkit/imaging`（压缩 + EXIF 摆正 + 哈希命名）、
   `api/v2_files.go`（上传流程）、`storage/filestore/uploads.go`（原名映射）。
10. **§14** —— 前端预览：缩略图 + 放大 + 一切文件都能点开。**✅ 已完成。**
    已落地：`web/src/lib/file-kind.ts`（纯函数分类）、
    `web/src/components/file-preview.tsx`（覆盖层）、
    `web/src/components/attachment-chip.tsx`（缩略图）、`api/v2_files.go` 的
    `GET /v2/sessions/:id/files/raw`（四形态响应）。

★ 前两刀单独切是有意的：**它们都不依赖任何附件的东西**，
所以即使后面的设计被推翻，这两个 bug fix 依然成立。
★ 而且第 1 刀是第 3 步的**前提**：附件清单要挂在消息上，
而「模型看到的那条」与「落盘的那条」必须是同一个对象。

★ **落地时多走了一刀**：清单在 `Extra` 里存的是 **JSON 字节**而不是
`[]Attachment`（gob 编不了没注册过的类型，checkpoint 会在 stop 时写失败）。
见 §3.1。

---

## 11. 与既有设计文档的关系

| 文档 | 关系 |
|---|---|
| `docs/fs-backend-design.md` | ★ workspace 已在 fsx 的 `ExtraRoots` 里（`agentkit/build/snapshot.go:59`），本文不重复那份边界 |
| `docs/run-lifecycle-design.md` | ★ 附件是**消息级**的，不影响 run 的账本与租约 |
| `docs/context-usage-design.md` | ★ 内联的图片会占 token —— 上文的用量估算将来要把它算进去，本文只标出这个口子 |
| `docs/mcp-design.md` | §8（MCP 服务器的文件工具）仍未实施，与本文正交 |
| `docs/config-design.md` | 不涉及配置分区 |
| `docs/api-v2-design.md`（upstream） | ★ **本文不新增端点**（除 §7.1(5) 的删除端点）—— 若最终要加，先读 upstream 那份 |

---

## 12. 验收判据

| # | 判据 | 怎么验 | 状态 |
|---|---|---|---|
| 1 | ★ 用户消息在模型输入里**只出现一次** | `agentkit/run` 的回归用例 | ✅ |
| 2 | ★ 附件清单随消息落盘，读回一致 | transcript JSONL 里有 `openheron_attachments` | ✅ |
| 3 | ★ 文本附件让模型 `read_file` 读得到 | 冒烟：传一个 `.md`，模型引用它的内容 | ⏳ 需真模型 |
| 4 | ★ 图片附件让模型**看得见** | ★ **已验**（2026-11-10，deepseek-flash）：传一张 2.1 MiB 的班徽图，模型答出「五二小学山海二班」的圆形班徽 | ✅ |
| 5 | ★ 带附件的一轮**不被**当成重连 | `api` 层用例 | ✅ |
| 6 | ★ 越权路径被拒 | `..` / 绝对路径的用例 | ✅ |
| 7 | ★ 刷新后历史里的附件还在 | Playwright | ✅ |
| 8 | ★ 拖拽与粘贴真的能用 | Playwright（假后端） | ✅ |
| 9 | ★ 超大图片降级为路径，**不静默** | 用例 | ✅ |
| 10 | ★ 不支持 vision 的模型不会让整轮失败 | 用例（`llm/common/vision_test.go`：一个拒绝图片的假 provider） | ✅ |
| 11 | ★ 只有附件没有文字的一轮也是一轮 | `api` 层用例 | ✅ |
| 12 | ★ 删一个附件删的是真文件 | `api` + Playwright | ✅ |
| 13 | ★ 一条消息不能同时带正文和多模态 part | `agentkit/run` 用例（§4.4.3） | ✅ |
| 14 | ★ **形状在 wire 边界统一**，历史里的旧消息也不炸 | `llm/common/shape_test.go`（§4.4.4） | ✅ |
| 15 | ★ 上传的图被压到最长边 1600px，格式不变 | `agentkit/imaging` 用例（§13.5） | ✅ |
| 16 | ★ 落盘名是 `原名-哈希8位.ext`，**同名不再覆盖**（图与文本都要） | `api` 用例（§13.4、§13.9） | ✅ |
| 17 | ★ 哈希**可复算**：读文件 → stat → 重算 → 对上名字 | `api` 用例（§13.3） | ✅ |
| 18 | ★ GIF 与 WebP **不压**（不毁动画、不谎称能编码） | `agentkit/imaging` 用例（§13.5） | ✅ |
| 19 | ★ 前端能预览：待发与历史都有缩略图，点击开大图 | Playwright（§14.6） | ✅ |
| 20 | ★ **改名之后用户看到的仍是原名**（刷新后待发 chip 也不变） | `api` 用例（§13.4.1） | ✅ |
| 21 | ★ JPEG 的 EXIF 方向被摆正，手机照片不会歪 | `agentkit/imaging` 用例（§13.6） | ✅ |
| 22 | ★ **一切文件都能点开**：图片看图、PDF 内嵌、文本/代码/Markdown 看内容 | `api` + Playwright（§14.7） | ✅ |
| 23 | ★ 文本类一律按 `text/plain` 返回（XSS 的那道闸） | `api` 用例（§14.2.1） | ✅ |

★ 第 3、4 条要**真的模型**才能验，所以它们属于 `task smoke` 而不是 CI
（AGENTS.md：冒烟不进 CI，因为它们需要一个会回答的模型）。
第 1、2、5、6、9、10、11、12、13、14 条是 Go 用例，进 CI。
第 7、8 条是 Playwright，用假后端，进 CI。

★ **第 13 条是踩出来的**：它在真模型上才暴露（假后端不序列化，Go 用例不碰
适配器），错误是 openai 客户端拒绝序列化 ——
`can't use both Content and MultiContent properties simultaneously`。
所以它虽然有 Go 用例守着，**第一次遇到它的地方仍然是真模型**。

★ **第 14 条是第 13 条的延伸**：修掉第 13 条只让**这一轮**的消息合法了，
而**历史**里 2026-11-09 之前写的消息仍然带着双形状，一进模型就死。
归一化因此落在 wire 边界（§4.4.4），而不是装配层。

★ **第 4 条的实测记录**（deepseek-flash，2.1 MiB 的 PNG）：

```
prompt_tokens 1036          ← 图片真的进了 prompt
"这是「五二小学山海二班」的圆形班徽——外圈上部「山海少年」…"
```

⚠️ 但**同一个 API 下的 `deepseek-v4-pro` 收下请求却看不见图**
（prompt_tokens 只有 100，模型自答 "I cannot view the image"）。
详见 §6 的模型能力对照表。

★ **第 10 条的落法**：内联失败**不**让整轮失败（走路径，模型被告知）；
provider 拒绝图片**也不**让整轮失败（去掉图片重问一遍，模型被告知）。
见 §6.1。

★ **第 15-23 条属于 §13 与 §14**：第 15-18、20、21、23 条是 Go 用例，
第 19、22 条是 Playwright。

---

## 13. 图片落盘：压缩与哈希命名

★ **本节已实施。** 前面 §1-§12 是「附件怎么到模型眼前」，
这一节是「附件怎么**躺在 workspace 里**」—— 落盘那一刻就该定下来的两件事：
**压不压**、**叫什么**。

### 13.1 ★ 现状：原名落盘，同名直接覆盖

`api/v2_files.go:141` 现在的落盘是：

```go
path, putErr := sess.PutFile(fh.Filename, f)
```

而 `storage/filestore/session.go:552` 的 `PutFile` 落到 `os.Create(dst)` ——
**同名就是截断覆盖**。

★ **本小节描述的是修复前的状态。** 现在的落盘是
`api/v2_files.go` 里的「压缩 → 哈希命名 → `PutFile` → `os.Chtimes` 钉住 mtime
→ `RecordUpload` 记下原名」，见 §13.7。

后果不是「文件丢了」，是**记录开始说谎**：同一个会话里传两次 `shot.png`，
第一次那份被无声吃掉；而第一条消息的附件清单里写着 `shot.png`，
那份清单说「这条消息带了这个文件」，而那个文件已经是第二次的了。
★ 用户重传一张图（很常见：拍糊了重拍、截错图重截）就会撞上。

★ **文本文件也一样**，而且更常：`notes.md`、`README.md`、`screenshot.png`
—— 重名是常态。★ 所以 §13.2 第 6 条把改名范围定成**所有文件**：
只治图片等于把最常见的情形留在外面。

★ 这也解释了为什么「哈希命名」不是洁癖：**它要解决的是覆盖，不是好看。**

### 13.2 决策

| # | 项 | 决策 |
|---|---|---|
| 1 | 压缩在哪做 | **服务端**，上传时 |
| 2 | 压哪些 | **只压 PNG 与 JPEG** |
| 3 | 压成什么 | **保持原格式**（PNG 仍是 PNG，JPEG 仍是 JPEG） |
| 4 | 压到什么程度 | **最长边 1600px** |
| 5 | 落盘名 | **原名-哈希8位.ext** |
| 6 | 命名的适用范围 | **所有上传文件**（不只是图片） |
| 7 | 哈希的输入 | 原名 + 落盘大小 + 上传时刻的 mtime |

★ **第 1 条为什么不在前端**：前端的 canvas 压缩要处理 CORS 画布污染、
EXIF 方向、以及「用户绕过界面直接调 API」的情况 —— 而服务端只有一条路。
★ 而且压缩的收益在服务端才兑现：**base64 是压缩后才编码的**，
压过的图进模型时小得多（2.1 MiB 的 PNG 常常能落到 200 KiB 上下）。

★ **第 3 条为什么重要**：格式不变 ⇒ MIME 判定不变（§6 的白名单）、
附件清单不变、模型的 `image_url` part 不变、DeepSeek 的
「JPEG / PNG / GIF / WebP」兼容面不变。转 JPEG 能压得更小，
但**透明通道会变成白底** —— 截图、抠图、带透明背景的图都会毁掉。

★ **第 6 条是后加的**（2026-11-10 的决定）：**覆盖要治本**。
只给图片改名，文本文件仍然同名覆盖 —— 而「同名 `notes.md` 被第二次上传吃掉」
是同一个毛病。★ 而且文本文件才是**最常**重名的那一类（`notes.md`、
`screenshot.png`），只治图片等于把最常见的情形留在外面。
★ 代价是文本文件的路径也变了（§13.4.1 记了这件事怎么补偿）。

### 13.3 ★ 哈希里的 mtime，以及那个循环

「名 + 大小 + mtime」的哈希要能**复算**，才有校验价值：
拿到 (名, 大小, mtime) 的人应该能重算出同一个哈希。

★ **循环在这里**：文件的 mtime 是内核在**写的那一刻**定的，写之前拿不到；
而哈希要写进文件名里 —— 所以要么先写后改名，要么把 mtime 事后钉回去。

**本设计选后者：一次写，不改名。**

```
t0 = 收到这次上传的时刻（毫秒精度）
size = 落盘字节数（图片是压缩后的，文本是原始的）
hash = sha256(name ‖ 0x00 ‖ size ‖ 0x00 ‖ t0.UnixMilli) 的前 8 位 hex
最终名 = stem + "-" + hash + ext
写入 → os.Chtimes(path, t0, t0)   ← 把 mtime 钉成 t0
```

★ **为什么 `Chtimes` 是对的而不是人造的**：t0 是**这次上传的真实时刻**，
不是随便挑的一个数。钉上去之后，文件自己的 mtime 与哈希里的 mtime
**是同一个值**，于是「读文件 → stat → 重算哈希 → 对上名字」这条校验链成立。

★ **对比另一条路**（先写临时名 → stat → 算哈希 → rename）：
它在 POSIX 与 Windows 上也成立（**改名不改 mtime**，改的是 ctime），
但要额外给 storage 加一个「重命名」的能力，而且中途会留下一个
临时名（列举 workspace 时能看见它）。一次写更干净。

★ **毫秒而不是纳秒**：UnixNano 在 NTFS 上是 100ns 精度、在 ext4 上是纳秒，
但 FAT32 只有 2 秒 —— 纳秒级的哈希在 FAT32 上**不可复算**。
毫秒是「跨平台都稳」的刻度，而两次上传相隔一毫秒这件事根本不会发生。

★ **分隔符用 0x00**：名字里不会出现 NUL，所以 `name ‖ size ‖ mtime`
不会有歧义 —— 否则 `ab` + `12` 和 `a` + `b12` 会撞。

### 13.4 命名规则的细节

```
stem = 原名去掉扩展名，清洗掉路径分隔符与控制字符，截断到 60 字符
ext  = 原扩展名，保持原样（判定 MIME 时会小写化，不受影响）
最终名 = stem + "-" + hash8 + ext
```

★ **原名进哈希，清洗后的 stem 进文件名**：清单里的 `Name` 仍然是
**原名**（用户认得的那个），`Path` 是落盘名。两者都存，
各有各的用处：`Name` 给人看，`Path` 给机器用。

★ **清洗是必须的**：`safeJoin` 会拒绝越界的名字，而一个叫
`../../etc/passwd` 的上传名应该在这里就被改掉，而不是走到落盘那一步才报错。

### 13.4.1 ★ 改名之后，原名从哪来

改名有代价：**workspace 里的文件名不再等于用户上传时的名字**。
而「用户看到的是原名」是硬要求 —— 附件清单里的 `Name` 是它，
待发 chip 上写的也是它。

三个地方要拿到原名：

| 谁要 | 什么时候 | 原名从哪来 |
|---|---|---|
| 上传响应 | 刚上传完 | ★ 就在手上（`fh.Filename`）—— **精确** |
| 附件清单（已发） | 消息落盘时 | 随清单存进消息的 `Extra` —— **精确** |
| 待发清单（刷新后恢复） | 前端列举 workspace | ★ **只能从 workspace 反推** —— 见下 |

★ 前端 `web/src/lib/store/actions.ts:792` 的 `restorePendingFiles` 现在拿
workspace 里的**文件名**当 chip 的显示名（`merged.push({ name: f.name, … })`）。
改名之后它会显示 `图片相关-a3f9c2b1.png` —— **用户认不出自己传的文件**。

★ 两条路：

1. **从文件名反推**：显示名 = 去掉 `-hash8` 后缀。
   - 优点：零额外状态。
   - 缺点：**不精确** —— 清洗（§13.4）改过非法字符的 stem 与原名不同；
     而且原名自己以 `-8位hex` 结尾时会歧义。
2. **服务端记一份原名映射**：`path → 原名`，存在**会话目录**里
   （`<session>/uploads.json`，与 workspace **平级**）。
   - ★ 放会话目录而不是 workspace：workspace 是**模型看得见的工作区**，
     映射对模型没有意义，混进去只是噪音。
   - 列举端点用它还原 `Name` ⇒ **前端零改动**。
   - 删文件时同步删掉那一条。

★ **本设计选 2**：原名是**事实**，不该靠从文件名猜。
★ 映射的生命周期很短 —— 文件一旦被某条消息认领，原名就进了附件清单，
映射里那一条不再被读到；删文件时清掉。

★ 落点：`storage.Session` 加两个方法（与 `PutFile` / `DeleteWorkspaceFile` 同一层）：

```go
// RecordUpload remembers the name the file had before it was renamed.
RecordUpload(path, originalName string) error

// OriginalName returns the name the file had before it was renamed.
OriginalName(path string) string
```

★ 写映射要**原子**（临时文件 + rename）：同一个会话可能并发上传，
而「读—改—写」不是原子的就会丢条目。

★ **`DeleteWorkspaceFile` 要一起清那一条**：留下一条指向已删文件的映射，
下次列举时它会被当成「有个文件叫这个名字」而显示成一个不存在的 chip。

### 13.5 压缩规则

| 项 | 规则 |
|---|---|
| 触发条件 | 长边 > **1600px** **或** 体积 > **200 KiB** |
| 缩放 | 等比，`golang.org/x/image/draw` 的 **CatmullRom** |
| PNG 输出 | `png.Encoder{CompressionLevel: png.BestCompression}` |
| JPEG 输出 | `jpeg.Options{Quality: 85}` |
| GIF | **不压** |
| WebP | **不压** |

★ **触发条件是「或」不是「且」**：一张 800×600 但有 3 MiB 的 PNG
（未压缩位图）要压，一张 4000×200 但只有 90 KiB 的 JPEG 不必压 ——
重新编码一张小图是白费力气，还白白掉画质。

★ **GIF 不压，因为压了就毁**：Go 的 `gif.Decode` 只取**第一帧**，
解码一张动图再编码回去，得到的是一张静止图。动图变静图不是压缩，
是丢数据。

★ **WebP 不压，因为 Go 没有 WebP 编码器**：`golang.org/x/image/webp`
只有**解码器**；编码要么绑 CGO（`chai2010/webp` 之类），
要么没有纯 Go 实现。WebP 本身已经是压缩格式，原样落盘是诚实的做法。
★ 这一条要在用户手册里说清楚 —— 「有些格式不会被压缩」是用户能看见的事实。

★ **依赖**：新增 `golang.org/x/image`（`draw` 与 `webp` 解码）。
PNG / JPEG / GIF 的编解码在标准库里，不需要新依赖。

### 13.6 JPEG 的 EXIF 方向

Go 的 `jpeg.Encode` **不写 EXIF**。而手机照片的方向信息（Orientation tag）
常常只在 EXIF 里 —— 于是重编码过的照片可能被转 90 度或 180 度。

★ **已决定：引入 `github.com/rwcarlsen/goexif`（纯 Go，MIT）**，
读出 Orientation，需要时**先按它把图摆正，再缩放、再编码**。

★ **为什么值得多一个依赖**：EXIF 方向是**静默**的错 —— 图转了 90 度，
没有报错，只是模型看到的世界是歪的，用户看到的预览也是歪的。
而手机照片是「图片附件」里占比最大的一类。

★ **怎么用**：

```go
x, err := exif.Decode(bytes.NewReader(data))
if err == nil {
    tag, err := x.Get(exif.Orientation)
    if err == nil {
        if v, ok := tag.Int(0); ok { ori = int(v) }
    }
}
```

★ **八个取值对应八个几何变换**（EXIF 规范）：

| 值 | 含义 | 变换 |
|---|---|---|
| 1 | 正常 | 原样 |
| 2 | 水平镜像 | 翻转 |
| 3 | 旋转 180° | 旋转 |
| 4 | 垂直镜像 | 翻转 |
| 5 | 转置（沿主对角线） | 旋转 + 翻转 |
| 6 | 顺时针 90° | 旋转 |
| 7 | 反转置（沿副对角线） | 旋转 + 翻转 |
| 8 | 逆时针 90° | 旋转 |

★ **手机最常见的是 6 与 8**，所以**用例至少锁住这两个**：
构造一张四边颜色不同的图、给它写上 orientation=6 的 EXIF，
压完之后断言「上边还是上边」—— 一个写错了方向的测试会安静地放过一个歪的图。

★ **PNG 没有这个问题**（EXIF 是 JPEG 的机制），GIF 不压（§13.5），
所以**只有 JPEG 需要这一段**。

★ **依赖清单**：新增 `golang.org/x/image`（`draw` 与 `webp` 解码）与
`github.com/rwcarlsen/goexif`（EXIF 读取）。PNG / JPEG / GIF 的编解码
在标准库里。★ 两个都是纯 Go，**不引入 CGO** —— 这一条对本项目很关键
（`task test-race` 已经依赖 cgo，能少一个就少一个）。

### 13.7 落点与流程

新包 `agentkit/imaging`（只依赖标准库、`golang.org/x/image` 与 `goexif`，
不依赖 openheron 的其他包），对外两个函数：

```go
// Compress 按 §13.5 的规则处理一张图：不是 PNG/JPEG、或不值得压，
// 就原样返回。changed 说的是「字节变了」。
func Compress(name string, data []byte) (out []byte, changed bool, err error)

// HashedName 按 §13.3 算出落盘名 —— 对**所有**文件，不只是图片。
func HashedName(name string, size int64, mtime time.Time) string
```

★ `Compress` 的内部顺序是**摆正 → 缩放 → 编码**：EXIF 方向要在缩放之前
处理，否则转的是缩放后的图（方向反了）。

`api/v2_files.go` 的上传流程变成：

```
读上传字节
  ↓ 是 PNG/JPEG？
  ├─ 是 → 读 EXIF 方向 → 需要时摆正 → 缩放（最长边 1600px）→ 重编码（保持原格式）
  └─ 否 → 原样
  ↓
size = len(bytes)
t0 = now()
落盘名 = HashedName(原名, size, t0)        ← 对所有文件
sess.PutFile(落盘名, bytes.NewReader(bytes))
  ↓
os.Chtimes(path, t0, t0)                   ← mtime 与哈希里的一致（§13.3）
  ↓
sess.RecordUpload(相对路径, 原名)           ← 原名映射（§13.4.1）
  ↓
返回 { name: 原名, path: 相对路径, size, mime }
```

★ **`PutFile` 的签名不变** —— 它只是个「把字节写进 workspace」的东西，
压缩与命名是**上传语义**，属于上层。

★ **文本文件：不压缩，但照样改名。** ★ 压缩只对 PNG/JPEG（§13.5），
**命名对所有文件**（§13.2 第 6 条）。

★ **模型读文件不受改名影响**：§4.4.1 的清单给模型的是**绝对路径**
（`assembleAttachments` 里的 `filepath.Join(workspace, att.Path)`），
模型 `read_file` 的是那个绝对路径，与文件名长什么样无关。
★ 而清单里的 `Name` 仍是**原名** —— 模型看到的是「`notes.md` 在这儿」，
不是一个哈希后缀。

### 13.8 对既有链路的影响

| 落点 | 影响 |
|---|---|
| 附件清单的 `Name` | 仍是**原名**（用户认得的） |
| 附件清单的 `Path` | 变成哈希名（相对 workspace）—— **所有文件** |
| 附件清单的 `Size` | 落盘后的真实大小 —— `api/v2_attachments.go:46` 本来就用 `os.Stat` 取真值，**不用改** |
| 附件清单的 `MIME` | `MIMEOfExt(path)` 按扩展名判定，格式不变 ⇒ **不用改** |
| 提示词里的路径 | `assembleAttachments` 给的是**绝对路径**，模型 `read_file` 不受影响 ⇒ **不用改** |
| 模型侧 | 图片的 base64 变小 ⇒ 请求更快、更省 token；`image_url` part 的形状**不用改** |
| 列举端点 `GET /v2/sessions/:id/files` | `Name` 要用**原名映射**还原（§13.4.1），否则刷新后待发 chip 显示哈希名 |
| 前端 | ★ **零改动** —— 前端一直用服务端给的 `name` 与 `path`，不猜文件名 |
| 旧会话 | 旧文件没压也没改名，但 `MIMEOfExt(path)` 照旧判定 ⇒ **继续能用** |

★ **没有格式迁移问题**：新旧两种命名并存于同一个 workspace，
没有任何代码依赖「文件名长什么样」。

★ **模型的工作区会多出一堆哈希名** —— 它 `ls` workspace 时看到的是
`notes-a3f9c2b1.md` 而不是 `notes.md`。★ 但提示词里的清单给的是
**原名 + 绝对路径**，模型要读哪个文件是明确的；而且 §4.4.1 本来就教它
「总是用文件系统工具查真实代码」。

### 13.9 测试

| 位置 | 用例 |
|---|---|
| `agentkit/imaging` | ★ PNG 压完仍是 PNG；★ JPEG 压完仍是 JPEG；★ 长边 ≤ 1600px 且体积 ≤ 200 KiB 时**不动**；★ GIF 与 WebP **原样**；★ EXIF orientation=6 的 JPEG 压完**方向不变**；★ 哈希对同样的三个输入稳定；★ 哈希随 name/size/mtime 任一变化而变；★ 清洗后的名字不含分隔符 |
| `storage/filestore` | ★ `RecordUpload` / `OriginalName` 往返；★ 原名映射是**原子**的（并发写不丢条目）；★ 删文件时映射条目一起消失 |
| `api` | ★ 上传一张图后落盘名是 `原名-哈希8位.ext`；★ 落盘文件的 mtime 与哈希里的一致（**可复算**）；★ 清单里 `Name` 是原名、`Path` 是哈希名；★ **两次上传同名文件 ⇒ 两个文件，互不覆盖**（图与文本都要）；★ 文本文件**也**用哈希名；★ 列举端点返回的 `Name` 是**原名**而不是哈希名；★ 上传响应里的 `Name` 是原名 |
| `agentkit/run` | ★ 压过的图内联时 MIME 仍对；★ 模型清单里的路径是绝对路径，读得到 |

---

## 14. 前端预览：缩略图与放大

★ **本节已实施。**

### 14.1 现状：chip 上只有一个回形针

`web/src/components/attachment-chip.tsx` 现在渲染的是
回形针图标 + 文件名 + 大小。用户传了一张图，**看不出那是张什么图** ——
而「发附件」这个动作里，用户最想确认的恰恰就是「我传的是不是那张图」。

★ 这不是锦上添花：§13 的压缩与改名会让这件事**更必要** ——
落盘名变成哈希之后，用户在 workspace 里已经认不出自己的文件了，
界面上的那张缩略图就是唯一的认领方式。

### 14.2 字节从哪来：一个只读端点

现在**没有**取单个文件的端点（`GET /v2/sessions/:id/files` 只列举），
所以预览需要一个新的：

```
GET /v2/sessions/:id/files/raw?path=<相对路径>[&max=<像素上限>]
```

★ **走 `ResolveWorkspaceFile`** —— 与删除端点同一套越界规则，
越界的名字被拒。★ **这是唯一的边界**：路径不能逃出这个会话的 workspace。

#### 14.2.1 ★ 推翻「只允许图片」：用「服务端不解释内容」换回自由

★ **原设计（2026-11-10 上午）**：这个端点只允许图片 MIME，非图片返回 415。
理由是「它存在的意义是给界面看图，能取任意文件就扩大了泄漏面」。

★ **这条被推翻了**（2026-11-10 下午，代码的主人的决定）：目标是
「让一切都变得好用起来」，而「点开一个 `.md` 却被拒」正是最别扭的那种不好用。

★ **但推翻的方式不是放弃安全 —— 是换一个更硬的安全边界**：

> **服务端永远不把文件当内容解释，只当字节转发。**

具体地，端点按文件的类型给出**四种**响应，**没有第五种**：

| 文件类型 | Content-Type | 处置 |
|---|---|---|
| 图片（png/jpeg/gif/webp） | `image/*` | **inline** —— 前端 `<img>` 直接显示 |
| PDF | `application/pdf` | **inline** —— 浏览器原生渲染 |
| 文本类（`text/*`、json、xml、csv、markdown…） | **`text/plain; charset=utf-8`** | **inline** —— ★ 前端自己渲染 |
| 其他一切（zip、docx、exe…） | `application/octet-stream` | **attachment** —— 下载 |

★ **文本类一律按 `text/plain` 返回，是这一节最要紧的一条。**

- 它**堵掉了 XSS**：一个 `text/html` 或 `image/svg+xml` 的上传文件，
  如果服务端按它自己的 MIME 内联，浏览器就会**执行**它里面的脚本 ——
  而那脚本跑在 openheron 的源头上（`<img>` 不执行，但 `<iframe>`/`<object>` 会）。
  ★ 一律 `text/plain` 之后，浏览器只把它当**字**。
- 它**顺带解决了「Markdown 谁来渲染」**：服务端返回纯文本，
  前端用**已有的** `MarkdownText` / `CodeBlock` 渲染（§14.5）——
  不需要在服务端引一个 HTML 渲染器，也不需要信任上传的 HTML。

★ **SVG 归到「其他」**：SVG 是 XML，可以带 `<script>`，而浏览器把它当图片渲染时
**会执行脚本**。★ 所以 SVG 走 `attachment`（下载），而不是「图片 inline」。
★ 注意这与 §6 的判断一致：SVG 在附件方案里**从来不是图片**，它是文本。

★ **PDF inline 的风险**：浏览器自带的 PDF 阅读器有沙箱，主流浏览器不执行
PDF 里的 JavaScript。★ 但 PDF 仍是「执行别的程序留下的东西」，
所以它只对**这个会话自己的 workspace** 开放（§14.2 的路径边界）。

★ **`max` 只对图片有意义**：文本与 PDF 忽略它（缩放一段文字没有意义）。
图片走即时缩放：服务端解码 → 缩放 → 编码 → 返回。★ **不落盘** ——
不产生第二份文件，也就**没有第二个要清理的东西**。
chip 用 `max=320`，放大用原图（不带 `max`）。

★ **CPU 代价可以忽略**：320px 的小图解码+缩放+编码是毫秒级的；
而没有 `max` 的原图是零处理（直接读文件字节）。

★ **Content-Type 由服务端按扩展名判定**（`message.MIMEOfExt`），
**不是**按文件内容嗅探 —— 嗅探要多读一遍，而且用户改个扩展名就该按
扩展名算（§3.1 的同名理由）。★ 判不出来就是 `application/octet-stream`。

### 14.3 ★ 按类型分派：图片、文档、代码各有各的看的方式

★ 「一切都能点开看」不是「一切都用同一种方式看」。分派表：

| 附件类型 | chip 里 | 消息里 | 点开 |
|---|---|---|---|
| **图片** | 48px 缩略图 | 缩略图（大一点） | lightbox（原图，可缩放） |
| **PDF** | PDF 图标 | PDF 图标 | 内嵌预览（浏览器原生） |
| **Markdown** | 文件图标 | 文件图标 | 渲染后的 Markdown |
| **代码 / 文本 / CSV** | 文件图标 | 文件图标 | 代码高亮 / 纯文本 |
| **其他**（zip、docx…） | 类型图标 | 类型图标 | 下载 |

★ **分类靠 mime + 扩展名**，且 mime 优先（服务端判定的，比扩展名可信）。

★ **前端已经有渲染能力，不用新依赖**（`web/src/components/`）：

- `MarkdownText`（`markdown.tsx`）—— Markdown
- `CodeBlock({ lang, text })`（`code-block.tsx`）+ `getHighlighter` /
  `SUPPORTED_LANGS` / `langOf` / `looksLikeJson`（`lib/highlight.ts`）—— 代码
- lucide-react ^1.55 自带 `FileTextIcon` / `FileCodeIcon` / `FileImageIcon` /
  `FileArchiveIcon` / `FileSpreadsheetIcon` / `FileIcon` —— 类型图标

★ **分类逻辑放进 `web/src/lib/file-kind.ts`** —— 一个**纯函数**模块，
不 import 任何框架（AGENTS.md 的规矩：纯逻辑放 lib 的纯层，`bun test` 能证明它对）。

★ **文本预览要有上限**：一个 3 MiB 的 `.log` 全塞进 DOM 是灾难。
★ 超过阈值（比如 256 KiB）就只显示**头几行 + 「已截断」**，
并提供一个「下载全文」的链接。★ 界面文案是英文。

★ **两处 chip 同一套**：待发区（`composer.tsx:497`）与消息里
（`message-parts.tsx:98`）都用 `AttachmentChip`，只是 testId 与 ×按钮不同。
★ 所以预览逻辑只写一次。

★ **不做「本地 objectURL 预览」**：那会让前端多一套图片来源，
而上传本来就要走服务端（要走压缩）。等上传的这段时间里用文件名占位，
足够了。

★ **文件可能已经不在了**：用户可以在历史里看到一条附件，而那个文件
后来被删了（或者 workspace 被清过）。`<img>` 会 onError ——
那时**回退成图标 + 文件名**，而不是留一个碎图。
★ 一个碎图比一句「文件没了」更像出了故障。

★ **chip 上悬停显示完整文件名**（`title` 属性）：芯片里的名字是截断的，
完整名字要能一眼看到。

### 14.4 放大：一个覆盖层

点击缩略图 → 打开大图（覆盖层），点击背景或按 Esc 关闭。

★ **放大是纯前端状态**，不写进 store：它是一次「看一眼」，
不是一个可以跨刷新恢复的东西。★ 真要写进 store，就得处理
「刷新后还开着一个大图」这种没人想要的状态。

★ **大图用原图**（不带 `max`）：放大就是为了看细节，缩略图没意义。
★ 一次放大只请求一张图，不会把整条历史的附件都拉一遍。

★ **覆盖层也承载非图片**：点开一个 Markdown 附件，出来的就是
渲染后的内容；点开一个 PDF，出来的是浏览器内嵌的 PDF。
★ 同一个覆盖层、同一个 Esc —— **一个交互学到底**，
而不是「图片一种弹法、文本另一种弹法」。

### 14.5 改动面

| 位置 | 改动 |
|---|---|
| `web/src/lib/file-kind.ts`（**新增**） | ★ 纯函数：按 mime + 扩展名分类，返回 `{ kind, icon, label }` |
| `web/src/lib/api.ts` | 新增 `fileUrl(id, path, max?)` —— 拼 URL，**不 fetch**（`<img src>` 自己取） |
| `web/src/lib/store/state.ts` | `Attachment` 已有 `mime` ⇒ **不用改** |
| `web/src/components/attachment-chip.tsx` | 图片 ⇒ 缩略图；其余 ⇒ 类型图标；点击 ⇒ 打开预览 |
| `web/src/components/file-preview.tsx`（**新增**） | ★ 覆盖层：图片 / PDF / Markdown / 代码 / 文本；Esc + 点背景关闭 |
| `web/src/components/message-parts.tsx` | 历史里的附件 chip 走同一套（**只改传参**） |
| `web/src/components/composer.tsx` | 待发 chip 走同一套（**只改传参**） |
| `web/test/file-kind.test.ts`（**新增**） | ★ 分类的纯函数用例 |
| `web/test/e2e/attachments.spec.ts` | ★ 上传图 ⇒ chip 有缩略图 ⇒ 点击 ⇒ 大图；★ 文本附件 ⇒ 图标 ⇒ 点击 ⇒ 内容 |

★ **`<img src>` 而不是 fetch + blob**：浏览器的图片缓存、并发限制、
解码都在 `<img>` 这一侧更成熟，而且 URL 可以直接被复制。

★ **不用 `URL.createObjectURL`**：那是本地文件的预览，
而这里的文件在服务端。

### 14.6 测试

| 位置 | 用例 |
|---|---|
| `web/test/file-kind.test.ts`（单测） | ★ 图片/PDF/Markdown/代码/文本/其他各自归到对的类；★ mime 优先于扩展名；★ 认不出的归「其他」 |
| `web/test/`（单测） | ★ `fileUrl` 拼出正确的路径与 `max` |
| `web/test/e2e/attachments.spec.ts` | ★ 待发 chip 有缩略图；★ 历史消息里的附件有缩略图；★ 点击开大图、Esc 关闭；★ 文件被删后回退成图标而不是碎图；★ **文本附件点开能看到内容**；★ **PDF 附件点开是内嵌预览** |

### 14.7 ★ 服务端端点的测试

| 位置 | 用例 |
|---|---|
| `api` | ★ raw 端点返回图片的原始字节与 `image/*`；★ 文本类返回 **`text/plain; charset=utf-8`**（不是它自己的 MIME —— 这是 XSS 的那道闸）；★ PDF 返回 `application/pdf`；★ 其它返回 `application/octet-stream` + `Content-Disposition: attachment`；★ `max=320` 的图比原图小；★ 越界路径被拒；★ 不存在的文件是 404 |

---

## 15. ★ 文件进 content：file 块（**已废弃**，2026-11-10 当日撤回）

> ★ **整节是历史记录，不要再照它改代码。** 这一版把非图片附件做成
> `file_data` 块并配了 `withoutFiles` 降级链；同日晚些时候决定改成
> 「消息头 `@` 绝对路径 + 模型 `read_file`」，`llm/common/files.go`、
> `llm/common/files_test.go`、`llm/common/vision.go`、
> `llm/common/vision_test.go` 全部删除，`llm/common/model.go` 收敛为一次请求。
> **当前实现以 §0.1 为准。** 下面的接口形状与实测数据留着，是因为它们解释过
> 为什么那条路走不通，以及为什么撤回。

### 15.1 问题：文件不在 content 里

§4.4.1 原来的决定是「文本类附件只给路径，模型用 `read_file` 读」。
那条决定在**图片**上是对的，在**其它文件**上不对：用户要的是
「我发的东西，模型在这一次请求里就看见了」，而一条只有路径的消息，
模型这一轮是**没有**那个文件的。

★ **2026-11-10 的要求**：上传的文件必须作为 chat completions 的
**content block** 进 user message 的 `content`。

★ 依据是 chat completions 接口本身：`messages[].content` 可以是
`string | object[]`，数组元素有三种 —— `text` / `image_url` / `file`，
而 `file` 块用 `file_id` 或 `file_data`（base64 data URL），
`filename` 可选。

★ **§4.4.1 的结论就此作废**：文本文件不再只给路径。路径仍然给
（模型可以再读一次），但内容**也**进 content。

### 15.2 形状：扁平的 file part

```json
{
  "role": "user",
  "content": [
    { "type": "text", "text": "这个文件里写了什么？" },
    { "type": "file", "file_data": "data:text/markdown;base64,IyBNYWdp...", "filename": "notes.md" }
  ]
}
```

★ **是扁平的**：`file_data` 与 `filename` 和 `type` **同级**，
外面没有 `"file"` 对象。

这一条是踩出来的：`go-openai` 的 `ChatMessagePart` 把文件建模成
`{"type":"file","file":{...}}`（嵌套），第一版就照它写了，
端点回的是

```
400 file must have a file_id or file_data
```

—— 它读到了那个 part，却在自己该找字段的地方没找到。
改成扁平之后，同一个端点的下一句变成了「这是**不支持的文件格式**」
（§15.4），也就是说：**形状对了，它开始看内容了**。

★ `file_id` 与 `file_data` **互斥**，且必居其一。本次**不做** `file_id`
那条路（2026-11-10 的决定）：它要先用 Files API 把文件传上去，
拿到 `file-api-...` 才能填，而 DeepSeek 的 file 块**只收图片**
（webp/png/jpeg/gif）—— 文本文件两条路都会被拒，
所以那条路解决不了文本附件的问题。

### 15.3 ★ 适配器造不出这个块：标记 + transport

eino 的 schema **有**这个 part（`schema.ChatMessagePartTypeFileURL`
配 `schema.MessageInputFile`，`eino@v0.9.22/schema/message.go`），
go-openai 的 `ChatMessagePart` 也**有** `File` 字段 ——
但中间那个适配器不认：

```
eino-ext/libs/acl/openai@v0.1.17/chat_model.go
  buildMessageFromUserInputMultiContent
```

它的转换器只处理 text / audio_url / image_url / video_url，
`file_url` 落进 default 分支，**在发出一个字节之前**就报
`unsupported chat message part type: file_url`。

★ 能改 body 的钩子只有一个：`acl.WithRequestPayloadModifier`。
但它**只留一个** modifier，而 reasoning 字段改名已经用了它
（`llm/common/request.go:100`）。再加一个就把前一个**顶掉**了 ——
reasoning 内容会悄悄不再改名。

★ 所以走的是**最后一道**：标记 + transport。

| 步骤 | 位置 | 做什么 |
|---|---|---|
| 装配 | `agentkit/run/attachments.go` `inlineFile` | 产出 eino 的 file part（语义正确，transcript 不碰） |
| 备件 | `llm/common/files.go` `prepareFileParts` | 把 file part 换成一个**标记**（一个 text part） |
| 上线 | `llm/common/files.go` `fileTransport` | 在 body 的最后一步，把标记换回**真的 file 块** |

★ 标记是**纯 ASCII**（`[[openheron-file:` … `]]`）。第一版用的是
U+2063 不可见分隔符，结果是**静默失效**：某些 JSON 编码器会把它
写成 `\u2063`，transport 就找不到自己的标记，于是文件变成一大坨
base64 文本发给模型 —— 没有报错，什么都没坏，就是不对。
**能扛住所有编码器的标记，比用户打不出来的标记值钱。**

★ transport 是 body 的**最后一个**触碰点，所以它跟 reasoning 改名
那种 modifier 是**叠加**而不是竞争。

### 15.4 ★ 降级链：端点不认 file 块时，文件还在

```
1. file 块（接口的形状）
2. 端点拒绝 → 文本文件：内容作为 text part 进 content
             二进制文件：去掉，并明确告知模型
3. 端点拒绝 → 图片：去掉图片，并明确告知模型（§6.1）
```

★ 第 2 级不是「把文件丢了」：文本文件的内容**仍然在 content 里**，
模型不用再调一次 `read_file`。这是实测里 DeepSeek 的走法：

```
400 You have uploaded an unsupported file. Please make sure your file is
    valid and has one of the following formats: webp, png, jpeg, and gif.
```

—— DeepSeek 的 file 块**只收图片**。所以对 DeepSeek 而言，
文本附件走的是第 2 级：内容以 text part 进 content。

★ 二进制文件不能这么做：它的字节放进 text part 是噪声。
那种情况是**去掉 + 明确告知**，路径还在提示词里，
模型可以用 `read_file` 读。

★ 「明确告知」是硬要求：模型不知道自己的文件没了，
就会回答一个它从没读过的文件，而用户分不出
「模型读了但没找到」和「模型根本没读」。

### 15.5 token 代价

★ 内联只发生在**本轮**：transcript 里存的是**引用**
（§4.4.2 那条规则不变），每个请求现读盘、现编码。

★ 但一轮之内**会重复**：工具循环里的每一次模型调用都会带上
这一轮的文件内容（同一个消息对象在 ADK 的消息列表里）。
历史里的旧轮次**不**重新内联 —— 那一轮的附件只有路径。

### 15.6 验收（真实端点，deepseek-flash）

★ 抓真实 wire 上的请求体（经本地转发代理落到磁盘）：

```
[0] role=system  content=<string>
[1] role=system  content=<string>
[2] role=user    parts=text,file
     part[0].text = "What is the magic word in the attached file? ..."
     part[1] = {"type":"file",
                 "file_data":"data:text/markdown;base64,IyBNYWdpYyBub3Rl...",
                 "filename":"magic-note.md"}
```

★ 端点拒绝后的重试（同一个 content 数组，第二个元素换成 text part）：

```
part[1].text = "The attached file \"magic-note.md\" reads:\n\n# Magic note\nThe magic word is PLATYPUS-42.\n"
```

★ 模型答 `PLATYPUS-42`，`outcome: success`；这一轮**没有调
`read_file`** —— 内容就在 content 里。

### 15.7 改动面

| 位置 | 改什么 |
|---|---|
| `agentkit/run/attachments.go` | ★ `inlineFile` / `inlineAttachment`：非图片附件产出 file part；`maxInlineFileBytes`（5 MiB）；提示词改成「文件内容已随这条消息给出」 |
| `llm/common/files.go`（**新增**） | ★ 标记、file 块、transport、降级（`withoutFiles`）、文本判定（`isTextual`） |
| `llm/common/model.go` | ★ 装 transport（`withFileBlocks`）；`prepareFileParts`；降级链接上「先文件、后图片」 |

### 15.8 测试

| 位置 | 用例 |
|---|---|
| `llm/common/files_test.go`（**新增**） | ★ file part 在 wire 上是**扁平**的 file 块（`file_data` + `filename`）；★ 标记不漏到端点；★ 端点拒绝 file 块时这一轮仍然答得出来，且重试带的是**文件内容**；★ 二进制文件被去掉且模型被告知；★ 标记不留在消息上（transcript 的那条消息还带着 file part）；★ 没有文件的请求原样发出 |
| `agentkit/run/attachments_test.go` | ★ 文本附件变成 file part（名字、类型、字节都对）；★ 超大文件退回路径且**不静默** |


---
