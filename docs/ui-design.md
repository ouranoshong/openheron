# 界面与资源设计

> 一句话：用户给的东西**挂在消息上**；界面把同一件事讲两遍。

## 0. 一句话

一份附件有两条命：一条给**模型**看（一个 `@` 路径，或一个图片 part），一条给**人**看
（缩略图与放大）。两条命各有边界 —— 模型那侧的绑定粒度是**消息**，人那侧的删除粒度是
**文件** —— 而它们必须在同一个名字上对齐。

界面这一侧的另一件事是动效：它只负责说「刚才发生了什么」，不承载任何信息本身；
所有动效都能被系统的「减少动态效果」关掉，关掉之后界面少了一分生气，但一条信息都没少。

★ 本文只讲**现状**与**已经拍板的决定**。未落地的提案集中在 §3；「改造前是什么样」的
叙述删掉了，理由留一句话。坑的细节在 `docs/pitfalls.md`。

## 1. 附件：清单挂在消息上

★ **附件是消息的属性，不是会话的属性。** 用户发来的文件绑在**它随的那条消息**上 ——
清单存在消息的 `Extra` 里（`message/attachment.go`）。模型看得见它，是因为 transcript 存的那条
消息就是装配输入时用的那个对象。

★ 之所以是「消息的属性」而不是「会话的属性」，是因为**只有消息级绑定才能回看**：
用户问「你刚才那张图里是什么」，模型得知道「刚才那张图」是哪一张。会话级挂载只能回答
「这个会话里有哪些文件」，答不了「刚才那张」。

### 1.1 数据模型：清单在消息的 Extra 里

`message.Extra` 是这套代码存「消息上的元数据」的地方（`message/provenance.go:10` 一组
`openheron_*` 键；压缩标记也是同一类）。附件清单跟着同一个机制走。

| 键 | 值 | 落点 |
|---|---|---|
| `openheron_attachments` | 附件清单（**JSON 字节**） | `message/attachment.go:19` |
| `openheron_message_id` | 消息自己的身份 | `message/provenance.go:26` |

清单项是 `message/attachment.go:30` 的 `Attachment{Name, Path, Size, MIME}`。

★ **`Path` 是会话 workspace 里的相对路径**，不是绝对路径：绝对路径是某一台机器的事实，
写进 transcript 就成了一条只在那台机器上成立的记录。

★ **清单存的是 JSON 字节**（`message/attachment.go:58` 的 `StampAttachments`），不是
`[]Attachment`。`Extra` 在写 checkpoint 时走 **gob** 编码，而 gob 编不了 interface 里没注册过的
类型 —— 一份 `[]Attachment` 会让 checkpoint 在 stop 的时候写失败（`message/provenance_gob_test.go:68`
有一张类型白名单的用例），留下一半写坏的审批。字节在白名单里，而 JSON 本来就是 transcript 载它的形式。

读的时候要认三种形态（`message/attachment.go:82` 的 `ReadAttachments`）：本包写的 `[]byte`、
JSON 往返之后变成的 **base64 字符串**（JSON 把 `[]byte` 编成字符串），以及调用方可能直接塞进去的
`[]Attachment`。★ **JSON 往返之后的那种才是 transcript 真正载着的形态。**

★ **为什么是 `Extra` 而不是消息的正文**：正文是给模型看的，`Extra` 是给程序看的。模型的输入靠
`message/extract.go:60` 的 `UserText` 取文本，`Extra` 不参与；同时 `Extra` 随 transcript 的 JSON
往返，**不用改 transcript 的格式版本**。

清单是**只写一次**的：`message/provenance.go:71` 的 `put` 先写为准，`StampAttachments` 复用它 ——
一条消息在管线里被重新盖章时，保留它出生时的样子。

★ **消息有自己的身份**：`message/identity.go:18` 的 `StampMessageID`。清单挂在一条**有稳定 ID** 的
消息上，才谈得上「精确回溯这条消息引用了哪些文件」；压缩在 transcript 中间插入一条消息时，
带 ID 的消息不改号。

★ **两个粒度不要混**：删除的粒度是**文件**，绑定的粒度是**消息**（`api/v2_files.go:460`）。
一个文件可能被多条消息引用，而已发消息的附件是历史的一部分。

### 1.2 后端：契约与解析

`POST /v2/run` 的请求体多一个 `attachments[]`（`api/v2_run.go:76`），每一项是
`runAttachment{Name, Path, Size}`（`api/v2_run.go:91`）。

★ **线上没有 content type**：内容类型由**服务端按文件名判定**（`message/attachment.go:149` 的
`MIMEOfExt`，在 `api/v2_attachments.go:64` 落到清单上）。客户端要是把一个文本文件说成
`image/png`，服务端就会去把一段文本当图片读，模型于是对着它没有看见过的图片作答。
判定不了的扩展名一律是 `application/octet-stream` —— 猜错的代价是模型读不到文件，诚实作答的
代价只是「它当文本读」。

★ **`Size` 是客户端的说法，服务端不信**：服务端自己 `stat` 了一遍（`api/v2_attachments.go:46`），
清单里记的是文件自己的大小。留着这个字段是为了让「客户端的说法」与「文件的实情」有一处可以
对照的地方，不是为了用它。

解析在 `api/v2_attachments.go:33` 的 `resolveAttachments`：

- ★ **客户端给的是相对路径，服务端拼的是自己的 workspace。** 这条规则让「越权读文件」在结构上
  不可能：客户端能写的字符串里没有绝对路径，服务端只做一次拼接再校验。
- 校验用 `storage/filestore/session.go:628` 的 `safeJoin` —— **同一个函数**，不另写一份。
- ★ **越界是拒绝，不是夹取**：夹取会解析到另一个文件，模型就会对着另一个文件作答，而且没有任何
  东西说它读错了。
- 路径还要**存在**且不是目录（`api/v2_attachments.go:46`）：一个不在那里的附件会让模型去找一个
  不存在的文件，然后说「文件不见了」—— 而用户相信他发过。

★ **附件算「一个问题」**：`isAttachRequest`（`api/v2_run.go:224`）判的是「没有 query、没有
messages、没有 resume」，附件也进了这条判据（`:229`）。否则「只有附件、没有文字」的一轮会被误判
成重连，去找一个不存在的 run。

附件在轮次开始**之前**解析（`api/v2_run.go:304`）：一个越界的名字或一个不存在的文件，要在客户端
还听得懂原因的时候被拒。只有附件没有文字的一轮也是一轮（`api/v2_run.go:311`）。

### 1.3 后端：落盘与装配 —— 让模型看见附件

落盘顺序在 `agentkit/run/loop.go:67` 起：造消息、盖时间戳、盖 ID（`:67`），盖附件清单（`:73`），
**先装配、后落盘**（`:80` 装配，`:96` 落盘）。

★ **为什么是「先装配、后落盘」**：装配时历史里还没有这条消息，所以「追加一次」永远是唯一的一份
—— 模型看到的问题只有一份，transcript 也只有一份。★ 而**不能**简单地把追加删掉：落盘失败时
`sess.Append` 只记日志、不中断（`:96`），历史里就没有这条消息，模型这一轮会完全看不到用户的问题
—— 一个写盘失败，换成了「模型答非所问」。换顺序正好解掉这个洞：装配用的就是那条消息对象本身，
**无论落盘成不成功，模型都看得见它**。

`buildInput` 的签名收的是**消息对象**（`agentkit/run/input.go:86`），不是文本。装配函数不需要知道
附件是什么，它只需要把消息原样交给模型。

装配按类型分流（`agentkit/run/attachments.go:233` 的 `assembleAttachments`）。

**文本类：给路径。**

- `@` 头由 `agentkit/run/attachments.go:148` 的 `attachmentPaths` 生成，一行一个路径。
- ★ **绝对路径是硬要求**：`agentkit/fsx/fsx.go:218` 的 `resolve` 把相对路径按**项目根**解析，而
  `Extra` 里的 `path` 是 **workspace 相对**的 —— 直接把那个相对名字交给 `read_file` 会读到另一个
  文件（甚至项目根下的同名文件）。
- 路径含空白时写成 `@"..."`（`agentkit/run/attachments.go:128` 的 `mentionPath`），提示词让模型调用
  时去掉引号；路径统一转成正斜杠 —— Windows 的反斜杠在 JSON 参数里是转义符。
- ★ **消息头是视图，不是改写**：`agentkit/run/attachments.go:203` 的 `presentMessage` 返回**一条
  副本**，transcript 里留着用户自己的话。★ 副本与落盘那条**共用同一个 `Extra`** —— id 与清单照旧
  跟着消息走，副本就这些事实而言还是那条消息。
- ★ 消息头**不能**写进落盘那条：界面渲染的是 `UserText(msg.Content)`，把机器路径写进用户自己的
  气泡，界面上就会显示一串绝对路径。
- 历史消息也重建 `@` 头（`agentkit/run/input.go:128` 的 `withHistoryAttachmentPaths`）：transcript
  只存相对路径，绝对路径是请求时从 workspace 推导出来的。★ **图片不在这里重新内联** —— 把每一轮
  的图片都重新变成 base64，会把每次请求都撑大。
- 内容**不在消息里**：上下文提示词把 `read_file` 写成义务（`agentkit/run/input.go:51` 的
  `attachmentRules`）—— 一条以 `@` 开头的行是系统注入的路径，不是用户的话；附件内容**不在**消息里，
  回答之前必须自己 `read_file`；大文件用 `offset` / `limit` 分段读。
- ★ **二进制附件不给路径**：`agentkit/run/attachments.go:91` 的 `isReadableText` 判它是不是能当文本
  读；不能的写进「暂时不能处理」清单（`:173` 的 `blockedReason`、`:183` 的 `blockedNote`）。对
  非文本字节调用 `read_file` 只会得到噪声，而噪声读起来像内容。

**图片类：多模态 part。**

- `agentkit/run/attachments.go:272` 的 `inlineImage`：现读盘、现编码，字节进 `Base64Data`、类型进
  `MIMEType`（`:289`）—— eino 推荐的是这个形状，不是 `data:` URL。
- ★ **base64 不落 transcript**：transcript 是 JSONL，一行上限 8 MiB
  （`storage/filestore/transcript.go:22` 的 `maxLineBytes`），一张 2 MiB 的图 base64 之后是
  2.7 MiB。transcript 里只有「哪个文件」。
- 内联上限 `maxInlineImageBytes` = 5 MiB（`agentkit/run/attachments.go:28`）。

**★ wire 上只有一种形状。**

线上只有一个 `content` 字段，它要么是字符串，要么是列表。openai 适配器在一条消息同时带
`Content` 与多模态 part 时会把两个都填上，客户端拒绝序列化：
`can't use both Content and MultiContent properties simultaneously`。

★ 所以归一化落在 **wire 边界**（`llm/common/shape.go:52` 的 `normalizeWireShape`），在 `Generate` /
`Stream` 交给适配器之前过一遍（`llm/common/model.go:88`、`:108`）。★ **为什么不是装配层**：装配只管
得到**这一轮**的消息，而历史是持久数据 —— 早于「正文挪进 part」的那段时间里写下的消息同时带着
正文和图片 part，一进模型就死，而会话不能改写自己的历史，下一句关于它的话会以同样的方式死一次。

- ★ **正文挪进 text part，`Content` 清空**（`llm/common/shape.go:93` 的 `oneShape`）—— 不是反过来：
  parts 是更丰富的形状，一张没有正文的图片，模型认不出它讲的是什么事。
- 同一个边界还兜住另一种脏历史：只有思维链、没有正文的 assistant 消息。客户端的 `content` 是
  `omitempty`，那条消息在线上变成 `{"role":"assistant"}`，端点直接拒绝**整个请求**。
  `llm/common/shape.go:74` 的 `withVisibleAssistantContent` 在副本里补上 `noVisibleResponseText`
  （`:68`），**如实说明**「上一轮没有产出回答」，而不是编造一段回答。
- ★ **一次请求，没有降级**：`llm/common` 里没有 `files.go` / `vision.go`。文件块那条路已于
  2026-11-10 撤回（见 §3）。

**回看。**

- `GET /v2/sessions/{id}/messages` 把清单放在消息的 metadata 里带回来（`api/v2_convert.go:76` 的
  `out.Metadata["attachments"]`）—— 历史里看得出哪个文件跟哪个问题一起走的。
- ★ 一条**只有附件没有正文**的消息不再被丢掉（`api/v2_convert.go:110`）：问题可能整个在文件里，
  而一个没有文字也没有 chip 的气泡看起来像什么都没发。

### 1.4 图片落盘：压缩与哈希命名

`agentkit/imaging` 是一个叶子包：只依赖标准库、`golang.org/x/image` 与
`github.com/rwcarlsen/goexif`，不 import openheron 的任何包。

**压什么、压到多小**（`agentkit/imaging/imaging.go:94` 的 `recode`）：

- 只有 **PNG 与 JPEG** 重编码，格式保持不变。★ 格式不变 ⇒ MIME 判定不变、清单不变、模型的
  `image_url` part 不变；而转 JPEG 会把透明通道变成白底，截图与抠图都毁掉。
- 触发条件是「长边 > 1600px」**或**「体积 > 200 KiB」（`imaging.go:43`、`:46`）。★ 是「或」不是
  「且」：一张 800×600 却有 3 MiB 的 PNG 要压，一张 4000×200 只有 90 KiB 的 JPEG 不必压 ——
  重新编码一张小图是白费力气，还白白掉画质。
- 缩放用 `golang.org/x/image/draw` 的 `CatmullRom`（`imaging.go:137`），合成用 `draw.Over`、
  目标画布透明 —— 白底会把 PNG 的每个透明像素变成不透明，模型看到的是一张白框。
- PNG 用 `BestCompression`，JPEG 用 `Quality: 85`（`imaging.go:144`、`:149`）。
- ★ **GIF 不压**：Go 的 `gif.Decode` 只取第一帧，解码一张动图再编码回去得到的是一张静止图 ——
  动图变静图不是压缩，是丢数据。
- ★ **WebP 不压**：Go 没有 WebP 编码器，`golang.org/x/image/webp` 只有解码器。WebP 本身已经是压缩
  格式，原样落盘是诚实的做法。

★ **压缩在服务端做，不在前端**：前端的 canvas 压缩要处理 CORS 画布污染、EXIF 方向、以及「用户
绕过界面直接调 API」的情况 —— 而服务端只有一条路。而且压缩的收益在服务端才兑现：**base64 是压缩后
才编码的**，压过的图进模型时小得多。

**JPEG 的 EXIF 方向**（`imaging.go:212` 的 `orient`）：

- Go 的 `jpeg.Encode` **不写 EXIF**，而手机照片的方向信息常常只在 EXIF 里 —— 于是重编码过的照片
  可能被转 90 度或 180 度。
- ★ 方向要在**缩放之前**处理（`imaging.go:124`）：否则转的是缩放后的图，方向反了。
- 八个取值对应八个几何变换，而且它是**像素的置换**而不是重采样 —— 每个 EXIF 方向都是 90 度的
  整数倍，重采样在这里只会白白地弄糊图。
- ★ **值得多一个依赖**：EXIF 方向是**静默**的错 —— 图转了 90 度，没有报错，只是模型看到的世界是
  歪的，用户看到的预览也是歪的。而手机照片是图片附件里占比最大的一类。

**叫什么**（`imaging.go:310` 的 `HashedName`）：

```
stem  = 原名去扩展名，清洗掉 Windows 不许的字符，截到 60 个 rune
最终名 = stem + "-" + hash8 + ext
```

- ★ **命名对所有文件，不只是图片**（`api/v2_files.go:184`）：同名覆盖会让**记录说谎** —— 第一条
  消息的清单里写着 `shot.png`，而那个文件已经是第二次上传的了。文本文件才是**最常**重名的那一类
  （`notes.md`、`README.md`）。★ 覆盖要治本，只治图片等于把最常见的情形留在外面。
- 哈希的输入是**原名 + 大小 + mtime**（`imaging.go:356` 的 `hashOf`），三段之间用 `0x00` 分隔 ——
  名字里不会出现 NUL，所以 `ab`+`12` 与 `a`+`b12` 不会撞。大小与 mtime 都写成定宽大端整数
  （`imaging.go:361`），哈希因此不取决于某个数字「怎么拼」。

★ **哈希里的 mtime，以及那个循环**（细节见 `docs/pitfalls.md` §6.2）：文件的 mtime 是内核在写的
那一刻定的，写之前拿不到；而哈希要写进文件名里。**一次写，不改名** —— `t0` = 收到这次上传的时刻，
哈希由 `(名, 大小, t0)` 算出，写入后用 `os.Chtimes` 把 mtime 钉成 `t0`（`api/v2_files.go:191`）。

★ **`Chtimes` 是对的而不是人造的**：`t0` 是这次上传的**真实时刻**，钉上去之后文件自己的 mtime 与
哈希里的 mtime 是同一个值，于是「读文件 → stat → 重算哈希 → 对上名字」这条校验链成立。另一条路
（先写临时名 → stat → 算哈希 → rename）也成立（改名不改 mtime，改的是 ctime），但要额外给 storage
加「重命名」的能力，而且中途会留下一个临时名。

★ **毫秒而不是纳秒**：NTFS 上是 100ns、ext4 上是纳秒，但 **FAT32 只有 2 秒** —— 纳秒级的哈希在
FAT32 上不可复算。毫秒是「跨平台都稳」的刻度，而两次上传相隔一毫秒这件事根本不会发生。

**原名从哪来**：

- 改名有代价：workspace 里的文件名不再等于用户上传时的名字，而「用户看到的是原名」是硬要求 ——
  清单里的 `Name` 是它，待发 chip 上写的也是它。
- ★ 选**服务端记一份原名映射**，而不是从文件名反推：原名是**事实**，不该靠猜。反推不精确（清洗
  改过非法字符的 stem 与原名不同；原名自己以 `-8位hex` 结尾时也歧义）。
- 映射住在会话目录里的 `uploads.json`（`storage/filestore/uploads.go:16`），与 workspace **平级**。
  ★ 放会话目录而不是 workspace：workspace 是**模型看得见的工作区**，映射对模型没有意义，混进去
  只是噪音。
- 写入在会话的锁下（`uploads.go:38` 的 `RecordUpload`）—— 读—改—写不是原子的就会在并发上传时丢
  条目；删除时同步删掉那一条（`uploads.go:80` 的 `forgetUpload`，由 `storage/filestore/session.go:548`
  调用）。
- 列举端点用它还原 `Name`（`api/v2_files.go:412` 的 `listWorkspace`），所以前端零改动。
- ★ 旧会话的文件没压也没改名，但 `MIMEOfExt` 照旧判定 ⇒ **继续能用**。新旧两种命名并存于同一个
  workspace，没有任何代码依赖「文件名长什么样」。

### 1.5 前端：发送、乐观气泡、拖拽粘贴

待发清单按会话存在 store 里（`web/src/lib/store/state.ts:24` 的
`attachments: Record<string, Attachment[]>`），一项是 `{name, path, size, mime}`（`state.ts:5`）。

- 发送时清单从 store 里取出来一起发（`web/src/lib/store/actions.ts:264`、`:331`）。
- ★ **发完要清空**（`:324`）—— 否则下一轮会把同一批文件再发一遍。
- ★ **被拒的一轮要把文件还回待发清单**（`:345` 的 `restorePending`）：服务端没拿到这个问题，文件
  就还是用户的，不是消息的。
- ★ **乐观气泡里也要画附件**（`:748` 的 `appendLocalUserMessage`）：附件是这条消息的一部分，用户
  发出去的瞬间看不到自己发了什么，就等于没有发出去。★ 文件在前、文字在后 —— chip 读起来是这个
  问题的附件，问题跟在后面。
- 三件交互都要：

| 交互 | 落点 | 处理 |
|---|---|---|
| 纸夹 | `web/src/components/composer.tsx:546` | `<input type="file" multiple>` |
| 拖拽 | `composer.tsx:523` 的 `onDragOver` / `:536` 的 `onDrop` | `e.dataTransfer.files` → `acceptFiles` |
| 粘贴 | `composer.tsx:601` 的 `onPaste` | `e.clipboardData.items` 里 `kind === "file"` 的项 |

- ★ 拖拽悬停必须 `preventDefault()`：默认动作是**拒绝**这次拖放，不允许就不会触发 drop。
- ★ **粘贴截图是这个交互里最有价值的那个**：截图进剪贴板，按 Ctrl+V 就发出去了，没有「先存成
  文件再选」的中间步骤。而 `onPaste` 里也必须 `preventDefault()`（`composer.tsx:611`）—— 默认粘贴
  会把图片的**文件名**塞进消息里，而用户要发的是图。

### 1.6 前端：刷新不丢，历史回看

★ **语义要分清**：workspace 里是「会话的全部文件」，某条消息带的是「这一条的附件」—— 两者不是
一回事。所以 `GET /v2/sessions/{id}/files` **不能**用来还原消息上的清单，它只能还原**待发清单**。

- 恢复靠一条判据：workspace 里**没有任何消息认领过**的文件才是「待发」
  （`web/src/lib/store/actions.ts:814`）。一条消息认领过的文件是跟那个问题一起发出去的，把它放回
  待发清单，下一个问题就会再发一次 —— 用户只问了一次。
- ★ 清单是**合并**而不是覆盖（`:824`）：用户刚上传的文件比还在路上的那份列举新。
- 真正的消息级清单在 **transcript 里**，历史水合时跟着消息一起回来：`web/src/lib/store/history.ts:16`
  的 `readAttachments` 从 wire 的 metadata 读，`:90` 装配成 part。★ 文件在前、文字在后 —— 与乐观
  气泡同一顺序，所以一条消息不管刚发出去还是从历史读回来，长得一样。
- 前端的 part 类型：`web/src/lib/types.ts:41` 的 `AttachmentPart`，`:50` 的 `Part` 联合类型加了一个
  成员。
- ★ **联合类型变宽要检查所有按 `kind` 分派的地方**：`attachment` part **没有 `complete` 这个
  字段**，所以 `p.kind === kind && p.complete === false` 这种写法在联合类型变宽之后**编译不过**
  （`kind` 是个变量，TypeScript 没法靠它收窄 `p`）。抽了一个 `isOpenPart` 出来做收窄
  （`web/src/lib/events.ts:188`）。
- 渲染：`web/src/components/message-parts.tsx:102` 的 `case "attachment"`。
- ★ **「移除」的语义**：待发清单里的附件 → 移除 = **真的删掉** workspace 里的文件
  （`actions.ts:231` 调 `api.deleteFile`）；已发消息里的附件 → **不删**，它是历史的一部分，删了
  那条消息就说谎了。前端删失败时要把 chip **放回去**（`:244`）—— 一个把服务端还留着的文件藏起来
  的 chip 是在说谎。
- 服务端：`DELETE /v2/sessions/:id/files?path=`（`api/v2_files.go:470`），路径相对 workspace，
  越界的名字被拒（`storage/filestore/session.go:537`）。★ **删一个不存在的文件不是错误**：调用方
  要的是「这个文件没了」，而它确实没了。

### 1.7 前端：预览 —— 缩略图与放大

字节从哪来：`GET /v2/sessions/:id/files/raw?path=`（路由 `api/v2_files.go:64`，处理函数 `:265`），
走 `ResolveWorkspaceFile` —— 与删除端点同一套越界规则。★ **这是唯一的边界**：路径不能逃出这个
会话的 workspace。

★ **服务端永远不把文件当内容解释，只当字节转发**，响应只有四种，没有第五种
（`api/v2_files.go:245`）：

| 文件类型 | Content-Type | 处置 |
|---|---|---|
| 图片（png/jpeg/gif/webp） | `image/*` | **inline** —— 前端 `<img>` 直接显示 |
| PDF | `application/pdf` | **inline** —— 浏览器原生渲染 |
| 文本类（`text/*`、json、xml、csv…） | **`text/plain; charset=utf-8`** | **inline** —— 前端自己渲染 |
| 其他一切（zip、docx…） | `application/octet-stream` | **attachment** —— 下载 |

★ **文本类一律按 `text/plain` 返回，是这一节最要紧的一条**（`api/v2_files.go:319`）：一个
`text/html` 或 `image/svg+xml` 的上传文件，如果服务端按它自己的 MIME 内联，浏览器就会**执行**它
里面的脚本 —— 而那脚本跑在 openheron 的源头上。一律 `text/plain` 之后，浏览器只把它当**字**。
★ 它顺带解决了「Markdown 谁来渲染」：服务端返回纯文本，前端用**已有的** `MarkdownText`
（`web/src/components/markdown.tsx:43`）/ `CodeBlock`（`web/src/components/code-block.tsx:6`）渲染，
不需要在服务端引一个 HTML 渲染器，也不需要信任上传的 HTML。

★ **SVG 归到「其他」**（`api/v2_files.go:335` 的 `isPreviewImage`）：SVG 是 XML，可以带
`<script>`，而浏览器把它当图片渲染时**会执行脚本**。它走 `attachment`（下载）。

★ `max` 只对图片有意义（`api/v2_files.go:300`）：图片走即时缩放 —— 解码 → 缩放 → 编码 → 返回，
**不落盘**，不产生第二份文件，也就**没有第二个要清理的东西**。文本与 PDF 忽略它。

Content-Type 由服务端按扩展名判定（`message.MIMEOfExt`），**不是**按内容嗅探 —— 嗅探要多读一遍，
而且用户改个扩展名就该按扩展名算。判不出来就是 `application/octet-stream`。

分类放在一个**纯函数**模块 `web/src/lib/file-kind.ts`：`fileKind`（`:197`）、`effectiveMime`
（`:129`）、`isImage`（`:173`）、`fileLang`（`:218`）。★ **mime 优先，扩展名其次**（`:194`）：
服务端判定的 mime 比客户端可信；唯一的例外是 `application/octet-stream` —— 那是服务端的
「我不知道」，不携带信息，于是扩展名说了算。

- chip：`web/src/components/attachment-chip.tsx:62` 的 `AttachmentChip`，缩略图 48px（`:40` 的
  `THUMB`）。★ 图片走**缩略图**而不是回形针：「我发的是不是那张图」是发附件这件事里用户最想确认
  的，而回形针答不了。缩略图在**出站时**缩放（`attachment-chip.tsx:179`），chip 不为 48 像素去拉
  一张两兆的图。
- ★ **文件可能已经不在了**（用户在历史里看到一条附件，而那个文件后来被删了）：`<img>` 会
  `onError`，那时**回退成图标 + 文件名**（`attachment-chip.tsx:182`）—— 一个碎图比一句「文件没了」
  更像出了故障。
- 放大是一个**覆盖层**：`web/src/components/file-preview.tsx:27` 的 `FilePreview`，点背景或按 Esc
  关闭（`:45`）。★ 放大是**纯前端状态**，不写进 store：它是一次「看一眼」，不是一个可以跨刷新
  恢复的东西。
- ★ 覆盖层也承载非图片：点开一个 Markdown 附件出来的是渲染后的内容（`file-preview.tsx:181`），点开
  一个 PDF 是浏览器内嵌的 PDF（`:81`）。同一个覆盖层、同一个 Esc —— **一个交互学到底**，而不是
  「图片一种弹法、文本另一种弹法」。
- 字节的 URL 是一个**字符串**而不是一次 fetch（`web/src/lib/api.ts:302` 的 `fileUrl`）：`<img src>`
  把文件交给浏览器自己的图片管线 —— 它的缓存、它的并发限制、它的解码器 —— 而且那个 URL 可以被复制
  出页面。

### 1.8 图片内联的边界

| # | 问题 | 判据 |
|---|---|---|
| 1 | **哪些 MIME 算图片？** | `image/png`、`image/jpeg`、`image/jpg`、`image/gif`、`image/webp`（`agentkit/run/attachments.go:35` 的 `imageMIMEs`）。★ **不含 `image/svg+xml`** —— SVG 是文本，能 `read_file`，走文本那条路就够 |
| 2 | **多大的图片能内联？** | 5 MiB（`agentkit/run/attachments.go:28` 的 `maxInlineImageBytes`）。★ 32 MiB 的上传上限（`api/v2_files.go:110`）是给**文件**的，不是给**图片**的 |
| 3 | **超限了怎么办？** | ★ **不降级成路径** —— 进「暂时不能处理」清单（`agentkit/run/attachments.go:257`）。给路径会让模型去 `read_file` 一张图的字节，那读出来的是噪声，而噪声读起来像内容 |
| 4 | **模型不支持视觉怎么办？** | ★ **不降级** —— 这一轮带着 provider 的错误结束，由用户重试（`llm/common` 里没有 `vision.go`） |
| 5 | **base64 编码在哪做？** | **服务端**，请求时现读现编。★ 不在上传时编 —— 那会把 base64 存进 workspace，而且上传响应要传一大串 base64 |

★ 第 4 条为什么不再兜底：「provider 拒绝」与「provider 收下但不看」是**两回事**。openheron
**无法从配置声明一个模型有没有视觉** —— `decl.Model`（`agentkit/spec/decl/decl.go`）只有 ID /
Kind / Dimensions / ContextWindow / SummarizeAt / Timeout / Reasoning* / Extra，**没有 vision
字段**。所以「这个模型支不支持图片」目前只能是 provider 的事实，不是 openheron 的配置；兜底只能
覆盖「请求被拒」这一种，而「收下但不看」返回 200、不报错，兜底路径不会触发，模型会自己说它看不见
—— 用户至少不会以为模型看过。

## 2. 动效

### 2.1 三条铁律

**默认态即最终态。** 动效只是「到达」的过程，信息在动画结束后必须完整可读、可读屏。去掉全部
动效，界面上不能少任何一条信息、不能少任何一个可点的东西。★ 这条决定了后面所有取舍：凡是
「靠动效才能看懂」的提案，一律不做。

**★ 新增动效不改变元素的最终布局。** 只动 `transform` / `opacity` / `filter` 这类**合成器属性**，
不动 `height` / `margin` / `top` / `width`。

★ **为什么**：消息流靠 `ResizeObserver` 按**内容高度**跟随滚动（`web/src/components/message-list.tsx:120`），
动布局会跟这套跟随机制抢方向盘 —— `web/test/e2e/scroll.spec.ts` 的十条用例正盯着这件事（溢出时
停在视口内 / 用户上翻后不被拽回 / 流式未结束也跟随 / 高亮撑高后仍跟随 …）。

★ **唯一的例外**：折叠面板的展开/收起。它本来就要改布局，动效只是让这次**已经要发生**的布局变化
变平滑 —— 不存在「新增的布局抖动」。

**尊重 `prefers-reduced-motion`。** 一条全局规则（`web/src/index.css:185`），把所有动画降到
「立刻完成」。

★ `0.01ms` 而不是 `0s`：有 `animation-fill-mode: both` 的入场动画在 `0s` 下会被**跳过**（某些浏览器
在 `0s` + `fill-mode: both` 的组合下会跳过关键帧，元素停在起始态）。`0.01ms` 保证终态一定被画
出来。

★ 因为是全局规则，它连 `tw-animate-css` 自带的那几处也一起降级了 —— 不只是本文写的那几处。

### 2.2 动效令牌：一份，不是六处

`web/src/index.css:76` 起的一组，和 `--chat-column` 同一个思路 —— **改一次，不是改六处**：

```css
--motion-duration-fast: 120ms;
--motion-duration-base: 200ms;
--motion-duration-slow: 320ms;
--motion-ease-out: cubic-bezier(0.22, 1, 0.36, 1);
--motion-ease-in-out: cubic-bezier(0.65, 0, 0.35, 1);
```

★ **为什么不给 `.dark` 单独一套**：动效时长与缓动跟明暗无关。明暗只影响**颜色**，而颜色已经有
`--info` / `--warning` / `--success` 那套 token 了。

★ **一个颜色也不许写字面量**：动效里出现的颜色一律走 token —— 这是 AGENTS.md 的「A color is a
token, never a literal」。

### 2.3 品牌标记：内联 SVG 与两个 mask

**★ 为什么必须内联**：装在 `<img>` 里的 SVG 是一个**独立的文档**，宿主页面的 CSS 选不进去它的
任何内部元素 —— 这是浏览器的规定，不是实现细节。所以 `<img>` 形态下，喙、颈、电路走线、数据点
**一个都动不了**，能动的只有整个 `<img>` 本身。

现在的形态：`web/src/components/brand-mark.tsx:14` 的 `BrandMark` 是一个薄封装，真正的图形在
`web/src/components/brand-mark.generated.tsx` 里 —— 那是**从 `web/public/favicon.svg` 生成的**
（`web/scripts/generate-brand-mark.ts:27`）。

★ **一份源，多个生成物**：`web/public/favicon.svg` 是唯一的源；`bun run gen:brand` 重新生成，
`bun run gen:brand:check` 检查漂移（`web/package.json:11`），`dev` 与 `build` 都先跑一遍生成。

- ★ **漂移检查在单测里**（`web/test/brand-mark.test.ts:56`）：重新生成一遍，和仓库里那份逐字节
  比对。CI 跑 `bun run test:unit`，所以**生成物过时会在 CI 上失败**，而不是等到某个人的浏览器里
  发现标签页和界面对不上。
- ★ 生成物是**提交进仓库**的：前端最后是 `go:embed` 进单二进制的，构建机上不该依赖一个「先跑个
  脚本」的步骤。

★ **结构上的一个坑：主体与描边是两个 mask。** `favicon.svg` 用两个 mask 叠出「深色描边 + 渐变主体」
—— `openheronBody` 挖主体（缩放 1.3，`web/public/favicon.svg:36`），`openheronHalo`（放大 1.365 倍）
挖描边（`:50`）。

★ **两个 mask 里是同一批路径的两份拷贝**，各带同样的 class（`mark-parts` / `mark-beak` /
`mark-neck` / `mark-stem` / `mark-bar`）⇒ 一条 class 同时命中两份拷贝，**主体与描边永远同步**，
不需要任何「引用」机制。body 的那一份还多一个 `mark-eye`（喙上的眼睛洞 —— 它是主体的一部分，
所以跟着主体一起淡入，否则会留下一个孤零零的黑点）。

★ **`<use>` 走不通**：把四个路径收进一个 `<g id="mark-parts">`、两个 mask 各用
`<use href="#mark-parts"/>` 引用它，是**错的** —— `<use>` 是影子树（shadow tree），宿主页面的 CSS
选择器**选不进去**影子树里的元素（能穿过去的只有继承属性：颜色可以，动画不行）。所以现在用的是
两份拷贝 + 同名 class。

★ **mask 里的部件只能做 opacity 动画**：body 的缩放是 1.3、halo 是 1.365，同样的位移在两份拷贝里
走的距离不同 ⇒ 描边和主体立刻错开。所以 `.mark-parts` 只淡入，**不做位移**
（`web/src/index.css:235`）。走线与数据点本来就在 mask 之外，不受这个限制：走线走
`stroke-dashoffset`，数据点走 `opacity` + `scale`。

动效只做一件事 —— **电路接通、数据点亮起**，全程一次性，不循环（`web/src/index.css:206` 起的三个
`@keyframes`）：

| 部件 | 动作 | 时序 |
|---|---|---|
| `.mark-trace`（电路走线） | `stroke-dashoffset` 从全长到 0 —— 电流接通 | 0ms 起，`--motion-duration-slow` |
| `.mark-dot`（数据点） | 淡入 + 一次轻微放大回弹（`scale(0.6)→1`） | 走线结束后 |
| `.mark-parts`（主体四件） | 整体淡入，**无位移** | 0ms 起，与走线并行 |

★ **主体不做位移**：位移会被读成「生长」，而这个标记是静态的品牌符号，不该有生长感。
★ **只播一次**：用 `animation` 而不是 `transition`，且不写 `infinite`。

★ 两个实现细节：走线的虚线长度用 `pathLength="1"` 归一化（`web/public/favicon.svg:77`），样式表
因此不必知道这条路径有多长，`stroke-dasharray: 1` 就是「整条」；数据点的放大用的是 CSS 的
**`scale` 属性**而不是 `transform` —— 数据点自带一个 `transform` 属性（它要落在画布的固定位置），
而 CSS 的 `transform` 会**整个盖掉**属性值，`scale` 是独立属性，它排在 `transform` 之前施加，
两者共存。★ 圆点要绕**自己的圆心**长，所以配了 `transform-box: fill-box`
（`web/src/index.css:248`）—— SVG 的 `transform-origin` 默认取的是**视口**的中心，不是图元的中心。

**用在哪儿**：只在欢迎页 hero（`web/src/components/message-list.tsx:214`，`size-16` 64px，**播一次
入场**）；页头（`web/src/app/shell.tsx:80`，`size-5` 20px，**不播**）。

★ 页头为什么不播：20px 下四个部件各只有几个像素，细节全糊；而页头是**常驻**的 —— 一个一直动的
东西在常驻位置就是噪声，不是信息。

**无障碍**：内联 SVG 带 `role="img"` + `<title>openheron</title>`
（`web/src/components/brand-mark.generated.tsx:29`、`:35`）；页头那处是装饰件（旁边的文字已经写了
openheron），用 `aria-hidden="true"` 而不是塞一个空的 `alt`。

### 2.4 界面功能性动效（未实施的提案）

★ **本节全部未实施。** 原则：**动效 = 状态变化的回声** —— 每一条都要能回答「界面刚才变了什么」。

| 落点 | 现在 | 提案 | 属性 | 依据 |
|---|---|---|---|---|
| 消息入场 | 无 | 淡入 + 上移 4px | `opacity` / `transform` | 新元素出现；第二条铁律合规（不改布局） |
| 审批卡出现 | 无 | 从下 8px 淡入 | `opacity` / `transform` | ★ 它**取代 composer**，出现得晚、位置显眼 |
| 状态点切换 | 只有 `animate-pulse`（`web/src/app/shell.tsx:20`） | 保留 pulse，另加**颜色过渡** | `background-color` | 状态点的语义由上游文档定义 |
| 「Working…」 | `animate-pulse` 小点（`web/src/components/message-list.tsx:233`） | 保留 | — | 已有 |
| 工具卡展开 | 箭头 `rotate-90`（`web/src/components/tool-card.tsx:90`） | 箭头保留；**内容**用 `grid-template-rows: 0fr → 1fr` 做高度过渡 | `height`（第二条铁律的例外） | 内容本来就要变高 |
| 折叠箭头 | 已有 `transition-transform`（三处各写一遍） | 补上 `--motion-duration-fast` | `transform` | 三个地方各写一遍 = 会漂 |
| 对话框 | `data-open` / `data-closed` 已有（`web/src/components/ui/dialog.tsx:42`、`:64`） | 保留 | — | 现成的模式，新动效照它写 |

★ **审批卡那条是本节唯一「有存在感」的动效**：审批发生时轮次已经结束、输入框被取代，用户此刻最
需要知道「轮子停在我这儿了」。

★ **流式光标（新增，但只能做成 CSS 伪元素）**：现在流式输出**没有光标**，文本一段段蹦出来。

★ 实现约束是硬的：消息流**每帧重渲染**（`web/src/lib/api.ts:463` 的 `FRAME_MS` = 33ms 批处理，
每帧重新解析 markdown），所以光标**不能是 React 元素** —— 那样每帧都要重挂一次 DOM，成本和闪烁
都不可接受。只能做成 CSS 伪元素（`::after` 画一根竖线，`opacity` 呼吸，不占 DOM、不引发重渲染）。

★ 呼吸用 `opacity` 而不是 `width` / `height`：后者每帧触发布局，在每帧重渲染的列表里就是一场
灾难。

★ **验收的形状**：动效本身很难用 Playwright 断言（时序脆弱，等动画结束就要 sleep）—— 所以断言的
是**终态**，不是过程：消息发出后消息**在屏幕上**（入场动效没有把元素留在起始态）、审批卡出现后
按钮**可点**（入场动画没有挡住交互）、工具卡展开后内容**在**（高度过渡没有把内容留在 0fr）、用户
上翻后新消息到达**仍在视口内不被拽回**、`prefers-reduced-motion: reduce` 下消息**立刻可见**。
现有落点：`web/test/e2e/motion.spec.ts`（品牌标记与降级规则）、`web/test/e2e/scroll.spec.ts`
（动效不破坏跟随）。

★ **若折叠面板的高度过渡在实测中与滚动跟随有摩擦**，退回「直接切换 + 箭头旋转」。

★ **明确不做**（已拍板，不再讨论）：**不引入动画库** —— `tw-animate-css` 已经够用，而前端是
嵌进单个二进制的（`go:embed`），多一个依赖就多一份分发体积；★ `tw-shimmer` 曾经装了但全仓零
引用，2026-11-09 被删掉了 —— 它是弹药，但不是这里需要的弹药：这里要的是「淡入 + 接通」两件
小事，它一件都不做。**不做滚动联动**（`animation-timeline: view()`）—— 那是网站项目的叙事手法，
而 openheron 是应用界面，而且它要的是「不改布局」。**不做氛围型动效**（背景光晕、粒子、渐变
流动）—— 基调已定：克制。**不循环播放装饰性动画** —— 唯一的循环是已有的 `animate-pulse`
（它诚实地表示「状态还挂着」），以及 §2.4 里那个尚未实施的流式光标。
★ **不写 `will-change`**：现代浏览器对 `transform` / `opacity` 的动画会自动提升合成层，常驻的
`will-change` 反而占着合成层不放。

## 3. 待决

1. ★ **二进制附件还没有处理办法。** PDF、压缩包、docx 走不进「可当文本读」的判据
   （`agentkit/run/attachments.go:91` 的 `isReadableText`），于是它们**拿不到 `@` 路径**，只在提示词
   里被列为「暂时不能处理」（`:183` 的 `blockedNote`）。★ 为什么现在这样：对非文本字节调用
   `read_file` 只会得到噪声，而噪声读起来像内容 —— 一个模型对着噪声作答，比一个明说「这个文件我
   读不了」的模型更危险。**出处**：`agentkit/run/attachments.go` 的 `isReadableText` 与
   `blockedNote`。
2. ★ **上游 `docs/frontend-design.md` §10.3 把 spec 文件数与条数写死在表里。** 那份文档不在本
   仓库，本仓库改不了它；给动效加 E2E 用例时，那张表也得跟着改 —— **记一笔交给上游**。

### 3.1 ★ 已撤回的路线（不是待办）

**`file` 块那条路已于 2026-11-10 当日撤回。** 那一版把非图片附件做成 chat completions 的 `file`
块并配了降级链；同日晚些时候改成「消息头 `@` 绝对路径 + 模型 `read_file`」。

★ **撤回的理由**：DeepSeek 的 file 块**只收图片**（webp/png/jpeg/gif），文本文件两条路都会被拒 ——
所以它解决不了文本附件的问题；而它要付出的代价是**文件先上传给第三方**，还要管 `file_id` 的
生命周期。`llm/common/files.go`、`llm/common/vision.go` 及其测试全部删除，`llm/common/model.go`
收敛为**一次请求**。

★ **撤回的代价是明确的**：一次请求失败就没有第二次，模型没有视觉时那一轮直接失败（§1.8 第 4 条）。
当前实现以本文 §1.3 为准。

## 附录 A：本文引用的代码位置

★ 行号都是**重新定位**过的：源文档里的行号已经漂移，本表里的每一行都实际打开核对过。
拿不准的地方写文件名而不写行号 —— 写一个错的行号比不写更糟（`docs/pitfalls.md` §10.1）。

### A.1 后端：消息与附件清单

| 位置 | 说的是什么 |
|---|---|
| `message/attachment.go:19` | `ExtraAttachments` 的键 |
| `message/attachment.go:30` | `Attachment{Name, Path, Size, MIME}` |
| `message/attachment.go:58` | `StampAttachments`：清单存成 JSON 字节 |
| `message/attachment.go:82` | `ReadAttachments`：认三种形态 |
| `message/attachment.go:149` | `MIMEOfExt`：按文件名判定类型 |
| `message/provenance.go:10` | `Extra` 里那组 `openheron_*` 键 |
| `message/provenance.go:26` | `ExtraMessageID` |
| `message/provenance.go:71` | `put`：「只写一次」的盖章语义 |
| `message/provenance_gob_test.go:68` | `Extra` 值的 gob 可编码性白名单 |
| `message/extract.go:60` | `UserText`：模型看得见的正文 |
| `message/identity.go:18` | `StampMessageID`：消息自己的身份 |

### A.2 后端：契约、解析与落盘

| 位置 | 说的是什么 |
|---|---|
| `api/v2_run.go:76` | `runInput.Attachments` |
| `api/v2_run.go:91` | `runAttachment`：线上没有 content type |
| `api/v2_run.go:224` | `isAttachRequest` |
| `api/v2_run.go:229` | 附件也算一个问题的判据 |
| `api/v2_run.go:304` | 附件在轮次开始之前解析 |
| `api/v2_run.go:311` | 只有附件没有文字的一轮也是一轮 |
| `api/v2_attachments.go:33` | `resolveAttachments` |
| `api/v2_attachments.go:46` | 服务端自己 stat；目录不是附件 |
| `api/v2_attachments.go:64` | 类型由服务端按名字判定 |
| `api/v2_files.go:64` | `GET /v2/sessions/:id/files/raw` 路由 |
| `api/v2_files.go:110` | `maxUploadBytes` = 32 MiB |
| `api/v2_files.go:168` | 上传时在服务端压缩 |
| `api/v2_files.go:184` | 哈希命名对**所有**文件 |
| `api/v2_files.go:191` | `os.Chtimes` 把 mtime 钉成 t0 |
| `api/v2_files.go:209` | `RecordUpload`：原名映射 |
| `api/v2_files.go:245` | raw 端点的四种响应 |
| `api/v2_files.go:300` | `max` 只对图片有意义 |
| `api/v2_files.go:319` | 文本类一律 `text/plain` |
| `api/v2_files.go:335` | `isPreviewImage`：SVG 不是图片 |
| `api/v2_files.go:412` | `listWorkspace`：用原名映射还原 `Name` |
| `api/v2_files.go:470` | `v2DeleteFile` |
| `api/v2_convert.go:76` | 清单进 `Metadata["attachments"]` |
| `api/v2_convert.go:110` | 只有附件的消息不再被丢掉 |
| `storage/storage.go:97` | `Session.ResolveWorkspaceFile` |
| `storage/storage.go:109` | `Session.DeleteWorkspaceFile` |
| `storage/filestore/session.go:522` | `ResolveWorkspaceFile` → `safeJoin` |
| `storage/filestore/session.go:537` | `DeleteWorkspaceFile`：删一个不存在的文件不是错误 |
| `storage/filestore/session.go:548` | 删文件时清掉原名映射 |
| `storage/filestore/session.go:552` | `PutFile` |
| `storage/filestore/session.go:628` | `safeJoin`：越界的名字被拒 |
| `storage/filestore/uploads.go:16` | `uploads.json` 住在会话目录，不在 workspace |
| `storage/filestore/uploads.go:38` | `RecordUpload`：在会话的锁下写 |
| `storage/filestore/uploads.go:63` | `OriginalNames` |
| `storage/filestore/uploads.go:80` | `forgetUpload` |
| `storage/filestore/transcript.go:22` | `maxLineBytes` = 8 MiB |

### A.3 后端：装配与 wire

| 位置 | 说的是什么 |
|---|---|
| `agentkit/run/loop.go:67` | 造消息、盖时间戳、盖 ID |
| `agentkit/run/loop.go:73` | `StampAttachments` |
| `agentkit/run/loop.go:80` | 先装配 |
| `agentkit/run/loop.go:96` | 后落盘；写盘失败不中断这一轮 |
| `agentkit/run/bridge.go:28` | `loopItem.Attachments` |
| `agentkit/run/input.go:51` | `attachmentRules`：`read_file` 是义务 |
| `agentkit/run/input.go:86` | `buildInput` 收消息对象 |
| `agentkit/run/input.go:128` | `withHistoryAttachmentPaths` |
| `agentkit/run/attachments.go:28` | `maxInlineImageBytes` = 5 MiB |
| `agentkit/run/attachments.go:35` | `imageMIMEs` |
| `agentkit/run/attachments.go:91` | `isReadableText` |
| `agentkit/run/attachments.go:128` | `mentionPath`：绝对路径，含空白时加引号 |
| `agentkit/run/attachments.go:148` | `attachmentPaths` |
| `agentkit/run/attachments.go:173` | `blockedReason` |
| `agentkit/run/attachments.go:183` | `blockedNote` |
| `agentkit/run/attachments.go:203` | `presentMessage`：给模型的是一条副本 |
| `agentkit/run/attachments.go:233` | `assembleAttachments` |
| `agentkit/run/attachments.go:257` | 超限的图片进「不能处理」清单 |
| `agentkit/run/attachments.go:272` | `inlineImage` |
| `agentkit/run/attachments.go:289` | base64 进 `Base64Data` |
| `agentkit/fsx/fsx.go:218` | `resolve`：相对路径按项目根解析 |
| `agentkit/build/snapshot.go:89` | workspace 所在的 dataDir 进 fsx 的 `ExtraRoots`（`:103` 赋值） |
| `llm/common/shape.go:52` | `normalizeWireShape` |
| `llm/common/shape.go:68` | `noVisibleResponseText` |
| `llm/common/shape.go:74` | `withVisibleAssistantContent` |
| `llm/common/shape.go:93` | `oneShape`：正文挪进 text part |
| `llm/common/model.go:88` | `Generate` 之前的归一化 |
| `llm/common/model.go:108` | `Stream` 之前的归一化 |

### A.4 后端：图片落盘

| 位置 | 说的是什么 |
|---|---|
| `agentkit/imaging/imaging.go:43` | `compressMaxEdge` = 1600 |
| `agentkit/imaging/imaging.go:46` | `compressMaxBytes` = 200 KiB |
| `agentkit/imaging/imaging.go:94` | `recode`：只有 PNG/JPEG 重编码 |
| `agentkit/imaging/imaging.go:124` | 方向在缩放**之前** |
| `agentkit/imaging/imaging.go:137` | `CatmullRom` + `draw.Over` |
| `agentkit/imaging/imaging.go:144` | PNG 用 `BestCompression` |
| `agentkit/imaging/imaging.go:149` | JPEG 用 `Quality: 85` |
| `agentkit/imaging/imaging.go:212` | `orient`：八个 EXIF 取值 |
| `agentkit/imaging/imaging.go:310` | `HashedName` |
| `agentkit/imaging/imaging.go:356` | `hashOf`：定宽大端 + `0x00` 分隔 |

### A.5 前端：store 与装配

| 位置 | 说的是什么 |
|---|---|
| `web/src/lib/types.ts:41` | `AttachmentPart` |
| `web/src/lib/types.ts:50` | `Part` 联合类型 |
| `web/src/lib/store/state.ts:5` | `Attachment` |
| `web/src/lib/store/state.ts:24` | 待发清单按会话存放 |
| `web/src/lib/store/actions.ts:231` | `removeFile`：删真文件，失败就把 chip 放回去 |
| `web/src/lib/store/actions.ts:244` | 删失败时把 chip 放回去 |
| `web/src/lib/store/actions.ts:255` | `send` |
| `web/src/lib/store/actions.ts:264` | 取出待发清单 |
| `web/src/lib/store/actions.ts:324` | 发完清空 |
| `web/src/lib/store/actions.ts:331` | 请求体带上附件 |
| `web/src/lib/store/actions.ts:345` | 被拒的一轮把文件还回 |
| `web/src/lib/store/actions.ts:748` | `appendLocalUserMessage`：乐观气泡 |
| `web/src/lib/store/actions.ts:781` | `restorePending` |
| `web/src/lib/store/actions.ts:804` | `restorePendingFiles` |
| `web/src/lib/store/actions.ts:814` | 「没有消息认领过」才是待发 |
| `web/src/lib/store/actions.ts:824` | 合并而不是覆盖 |
| `web/src/lib/store/history.ts:16` | `readAttachments` |
| `web/src/lib/store/history.ts:90` | 历史水合时装配 part |
| `web/src/lib/events.ts:188` | `isOpenPart`：联合类型变宽后的收窄 |
| `web/src/lib/api.ts:302` | `fileUrl`：字符串而不是 fetch |
| `web/src/lib/api.ts:531` | 请求体里的 `attachments` |

### A.6 前端：预览与动效

| 位置 | 说的是什么 |
|---|---|
| `web/src/lib/file-kind.ts:129` | `effectiveMime`：服务端的 mime 优先 |
| `web/src/lib/file-kind.ts:173` | `isImage`：SVG 不是图片 |
| `web/src/lib/file-kind.ts:197` | `fileKind` |
| `web/src/lib/file-kind.ts:218` | `fileLang` |
| `web/src/components/composer.tsx:523` | `onDragOver` |
| `web/src/components/composer.tsx:536` | `onDrop` |
| `web/src/components/composer.tsx:546` | `<input type="file">` |
| `web/src/components/composer.tsx:601` | `onPaste` |
| `web/src/components/composer.tsx:611` | 粘贴时 `preventDefault()` |
| `web/src/components/attachment-chip.tsx:40` | `THUMB` = 48 |
| `web/src/components/attachment-chip.tsx:62` | `AttachmentChip` |
| `web/src/components/attachment-chip.tsx:179` | 缩略图在出站时缩放 |
| `web/src/components/attachment-chip.tsx:182` | `onError` 回退成图标 |
| `web/src/components/file-preview.tsx:27` | `FilePreview` 覆盖层 |
| `web/src/components/file-preview.tsx:45` | Esc 关闭 |
| `web/src/components/file-preview.tsx:81` | PDF 内嵌 |
| `web/src/components/file-preview.tsx:181` | 文本预览 |
| `web/src/components/markdown.tsx:43` | `MarkdownText` |
| `web/src/components/code-block.tsx:6` | `CodeBlock` |
| `web/src/components/message-parts.tsx:102` | `case "attachment"` |
| `web/src/components/message-list.tsx:120` | `ResizeObserver`：按内容高度跟随 |
| `web/src/components/message-list.tsx:214` | 欢迎页 hero 的品牌标记 |
| `web/src/components/message-list.tsx:233` | 「Working…」的 pulse 点 |
| `web/src/app/shell.tsx:20` | 状态点的 pulse |
| `web/src/app/shell.tsx:80` | 页头的品牌标记（不播） |
| `web/src/components/tool-card.tsx:90` | 折叠箭头 |
| `web/src/components/ui/dialog.tsx:42` | 对话框浮层的 `data-open` |
| `web/src/index.css:76` | 动效令牌 |
| `web/src/index.css:185` | `prefers-reduced-motion` 的全局规则 |
| `web/src/index.css:206` | 三个 `@keyframes` |
| `web/src/index.css:235` | `.mark-parts` 只淡入 |
| `web/src/index.css:248` | `transform-box: fill-box` |
| `web/src/components/brand-mark.tsx:14` | `BrandMark`：内联 SVG 的理由 |
| `web/src/components/brand-mark.generated.tsx:29` | `role="img"` / `aria-hidden` |
| `web/src/components/brand-mark.generated.tsx:35` | `<title>openheron</title>` |
| `web/public/favicon.svg:36` | body mask，缩放 1.3 |
| `web/public/favicon.svg:50` | halo mask，缩放 1.365 |
| `web/public/favicon.svg:77` | 走线的 `pathLength="1"` |
| `web/scripts/generate-brand-mark.ts:27` | 源是 `public/favicon.svg` |
| `web/package.json:11` | `gen:brand` / `gen:brand:check` |
| `web/test/brand-mark.test.ts:56` | 漂移检查 |
| `web/test/e2e/motion.spec.ts` | 品牌标记与降级规则的验收 |
| `web/test/e2e/attachments.spec.ts` | 附件的验收 |
