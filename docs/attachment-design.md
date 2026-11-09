# 附件发送设计 —— 让「发附件」真的把文件送到模型眼前

> 状态：**提案，未实施。** 本文描述的是 §1 那个**已经存在但没接通**的能力，
> 以及把它接通所需要的改动。§3 起每一节都标明了「现状」与「提案」，
> 凡是提案的部分都是**代码里还没有的**。
>
> 范围：附件的上传→落盘→**随消息绑定**→进入模型上下文→回看。
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

## 1. 问题：现状是什么，缺的是什么

### 1.1 已经有的部分（比「没做」要多）

上传这条链路的**大半段是真的通的**：

| 环节 | 落点 | 状态 |
|---|---|---|
| HTTP 上传接口 | `api/v2_files.go:92` `v2UploadFiles` | ✅ 已实现，multipart 多文件 |
| 大小上限 | `api/v2_files.go:90` `maxUploadBytes = 32 << 20` | ✅ 32 MiB |
| 路径逃逸防护 | `storage/filestore/session.go:591` `safeJoin` | ✅ 绝对路径与 `..` 一律拒绝 |
| 落盘位置 | `storage/filestore/session.go:515` `PutFile` → `<state>/<session>/workspace/` | ✅ 会话自己的 workspace |
| 列出附件 | `api/v2_files.go:143` `v2ListFiles` | ✅ |
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

★ 第 5 条是本设计里最省事也最正确的一条：**transcript 里只存「哪个文件」，
不存「文件的内容」**。图片在请求时现读现编。

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

校验用 `storage/filestore/session.go:591` 的 `safeJoin` —— **同一个函数**，
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
| 4 | **模型不支持视觉怎么办？** | 内联会报错。★ 捕获它，降级成给路径，并让这一轮**继续跑** —— 而不是整轮失败 |
| 5 | **base64 编码在哪做？** | **服务端**，请求时现读现编（§4.4.2）。★ 不在上传时编 —— 那会把 base64 存进 workspace，且上传响应要传 2.7 MiB 的字符串 |

★ 第 4 条是真实风险：`llm/common/model.go:68` 用的是
`eino-ext/libs/acl/openai` 的 client，它把 `MultiContent` 转成 OpenAI 的
多模态格式 —— 但**模型本身**支不支持 vision 是另一回事。
deepseek 的模型就不支持图片。§6 第 4 条的降级路径是**必须的**，不是可选的。

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
| 8 | `agentkit/run/input.go` + `agentkit/run/attachments.go`（**新增，已做**） | ★ **重复的 query 追加（已修，见 §4.5）**；图片内联；文本清单进提示 |
| 9 | `api/v2_convert.go`（**已做**） | `toAgUiMessage` 透出 `Metadata["attachments"]`；★ 只有附件没有正文的消息**不再被丢掉** |
| 10 | `message/identity.go`（**新增，已做**） | ★ 消息 ID：`StampMessageID` / `MessageID`（见 §4.7） |
| 11 | `agentkit/emit/emit.go`（**已做**） | ★ 流式消息盖上自己的 msgID（见 §4.7.3） |

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
| 8 | `agentkit/run/attachments_test.go`（**已落地**） | 图片内联生成 `image_url` part；文本附件只给路径；★ 超大图片降级但**不静默**；★ 文件不在了**不**让整轮失败 |
| 9 | `api/v2_attachments_test.go` + `api/v2_attachments_wire_test.go`（**已落地**） | 带附件的一轮**不被** `isAttachRequest` 误判；`..` 与绝对路径被拒；★ 清单从 metadata 透出；★ 上传路径是相对的；★ 删除端点 |
| 10 | `web/test/store-attachments.test.ts`（**新增，已落地**） | ★ 请求体带附件；★ 发完清空；★ 被拒时还回；★ 刷新后恢复待发清单 |
| 11 | `web/test/e2e/attachments.spec.ts`（**新增，已落地**） | ★ 附件随消息发出、出现在历史里、刷新不丢、拖拽、粘贴、★ × 删的是真文件 |

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

---

## 9. 文档连带修改

| # | 文件 | 改什么 |
|---|---|---|
| 1 | `docs/user-manual.md` + `_zh` | ★ 拖拽/粘贴从「已宣称」变成「真的」；附件清单随消息回看要写进去 |
| 2 | `docs/architecture.md` + `_zh` | 附件从「会话级」改成「消息级」—— 架构章节若提到 workspace 的语义，要跟着改 |
| 3 | `docs/developer-manual.md` + `_zh` | 已知问题表里如果提到消息重复，要删掉（§4.5 修掉了） |

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
| 4 | ★ 图片附件让模型**看得见** | 冒烟：传一张截图，问图里的字，模型答得对 | ⏳ 需真模型 |
| 5 | ★ 带附件的一轮**不被**当成重连 | `api` 层用例 | ✅ |
| 6 | ★ 越权路径被拒 | `..` / 绝对路径的用例 | ✅ |
| 7 | ★ 刷新后历史里的附件还在 | Playwright | ✅ |
| 8 | ★ 拖拽与粘贴真的能用 | Playwright（假后端） | ✅ |
| 9 | ★ 超大图片降级为路径，**不静默** | 用例 | ✅ |
| 10 | ★ 不支持 vision 的模型不会让整轮失败 | 用例 | ✅ |
| 11 | ★ 只有附件没有文字的一轮也是一轮 | `api` 层用例 | ✅ |
| 12 | ★ 删一个附件删的是真文件 | `api` + Playwright | ✅ |

★ 第 3、4 条要**真的模型**才能验，所以它们属于 `task smoke` 而不是 CI
（AGENTS.md：冒烟不进 CI，因为它们需要一个会回答的模型）。
第 1、2、5、6、9、10、11、12 条是 Go 用例，进 CI。
第 7、8 条是 Playwright，用假后端，进 CI。

★ **第 10 条的落法**：内联失败**不**让整轮失败 —— 文件还在，
模型可以把它当文本读或者用工具取。提示词里会写明「这个文件没有内联」，
所以模型不会声称自己看见了一张没看见的图。
