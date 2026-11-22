# 界面与前端设计

> 一句话：前端是一份**内嵌进单个二进制的静态 SPA**；它只有一个职责 ——
> **把一轮 agent 运行如实呈现出来**。

## 0. 这份文档讲什么

前端由 Go 二进制通过 `//go:embed all:web/dist` 分发（`main.go`），状态在一个 zustand
store 里，事件流经一个纯函数 reducer 变成界面状态。★ **没有任何聊天 UI 框架。**

本文分四块：

| 块 | 讲什么 | 节 |
|---|---|---|
| **前端是什么** | 选型、依赖、产物、目录 | §1 |
| **它怎么跑** | 会话身份、事件流、状态 | §2–§4 |
| **界面长什么样** | 骨架、消息、工具调用、审批、附件、动效、授权、时间线 | §5–§12 |
| **怎么验收** | 三层测试、E2E 覆盖、定位点约定、工程化 | §13–§14 |

★ 本文只讲**现状**与**已经拍板的决定**。未落地的提案集中在 §17；
「改造前是什么样」的叙述删掉了，理由留一句话。坑的细节在 `docs/pitfalls.md`
（前端那一节是 §15）。

★ **本文的前身是上游仓库的 `docs/frontend-design.md`**（heron 时代，987 行）。
2026-11-17 起它的内容并入本文，**以本文为准** —— 上游那份讲的是上游那棵树的界面，
而这棵树已经长出了项目、授权、操作日志与工具组。
★ 并入时**逐条重新核对过代码**：上游有几处说法在这棵树已经不成立
（§3.1 的分帧兼容、§5.6.1 的轮询、§6.2 的表格宽度、§7.1 的卡片默认态），
这些地方本文写的是**现在**，并在原地标出差别。

★ **本文不覆盖配置界面的那部分**：Settings 对话框里的文件树、编辑器与 Secrets 标签页
属于**配置**，不是资源 —— 它们的设计在 `docs/config-design.md`（文件树与写闸门）与
`docs/secrets-design.md`（§5 讲 Secrets 标签页：位置、数据形状、界面规矩与 E2E 断言）。

★ **本文也不讲侧栏**：侧栏的分组、行、菜单与「当前项目」那份真相在
`docs/sidebar-design.md`；项目表与项目↔会话的关系在 `docs/projects-design.md`。
本文讲侧栏时只从**前端这一侧**说它怎么画、什么时候不画。

## 1. 前端是什么

### 1.1 选型

| 层 | 选择 | 为什么 |
|---|---|---|
| 应用骨架 | Vite + React 19 | 单二进制内嵌，只能是静态 SPA |
| 样式 | Tailwind 4 | 无构建期配置成本 |
| 组件 | shadcn（Radix 原语 + 复制进仓库的代码） | 不是依赖，源码在 `web/src/components/ui/` |
| 状态 | zustand | 1.2kB，选择器订阅；见 §4.2 |
| 事件流 | 自己实现的 SSE 分帧（`web/src/lib/sse.ts`） | 见 §3.1 |
| 状态转换 | 自己实现的纯函数 reducer（`web/src/lib/events.ts`） | 见 §3.2 |
| Markdown | react-markdown + remark-gfm | 不开 rehype-raw，见 §6.2 |

★ **这三层是纯函数**：`sse.ts` / `events.ts` / store 的归约都不碰 React、不碰网络、
不读时钟、不改入参 ⇒ 正确性可以由 `bun test` 单独证明（喂一串事件，比对输出）。
★ 上游那轮移除 assistant-ui 的理由正是这一点：**它的语义只能靠翻源码确认**，
八处 bug 全部属于这一类（详见 §16 决策记录）。

### 1.2 依赖清单

以 `web/package.json` 为准，**20 个运行时依赖**（`:22-43`）：

```
@codemirror/commands  @codemirror/lang-markdown  @codemirror/lang-yaml
@codemirror/language  @codemirror/search  @codemirror/state  @codemirror/view
@lezer/highlight
@shikijs/engine-javascript  @shikijs/langs  @shikijs/themes
@uiw/react-codemirror
radix-ui  react  react-dom  react-markdown  remark-gfm  shiki
tw-animate-css  zustand
```

**15 个开发依赖**（`:44-60`）：`@playwright/test` `@tailwindcss/vite` `@types/bun`
`@types/node` `@types/react` `@types/react-dom` `@vitejs/plugin-react`
`class-variance-authority` `clsx` `lucide-react` `oxlint` `tailwind-merge`
`tailwindcss` `typescript` `vite`。

★ 前端**不依赖任何 AG-UI 包**。后端仍然是 AG-UI 协议（见 `docs/api-v2-design.md`），
但那是线上的形状，不是前端的依赖 —— `web/` 下没有任何 `ag-ui` 字样。

★ **多语言没有引任何包。** 文案目录是纯 TypeScript 对象，语言状态是一个
**不依赖框架**的模块（`web/src/lib/i18n.ts`）—— 与 `lib/theme.ts` 同一个写法。
理由：这里的语言只有两种、几乎没有复数、插值只有 `{name}` 一种；i18next 带来的
fallback / plural / context 那一整套语义，与本仓库「一个决定一处」的写法不合。
★ 目录是纯数据 ⇒ `bun test` 能直接验它（键位、占位符、兜底），
这正是双语文档那套纪律在代码里的对应物。

★ 清单里除了 UI 四件，其余都**不进主 chunk**：shiki 的语言/主题与 CodeMirror
都是动态 import 拆出的独立 chunk，只在真的遇到代码块或打开设置弹窗时才拉。

### 1.3 产物分块

★ 「懒加载」有两个含义，只做一个等于没做：

| 懒的对象 | 效果 |
|---|---|
| **懒建**（引擎/实例在第一次用到时才构造） | 省 CPU 与内存 |
| **懒下载**（模块用动态 import，不进首屏 chunk） | 省首屏下载与解析 |

shiki 的核心与 JS 引擎是**动态 import** 的（`web/src/lib/highlight.ts:21-22`），
主题与 10 种语言各自动态 import（`:26-39`）；
设置弹窗是 `lazy()` 进来的（`web/src/App.tsx:5-7`）。
★ 2026-10-23 之前 shiki 只做到了「懒建」：`import { createHighlighterCore } from "shiki/core"`
是**静态**的，于是 shiki 核心与 JS 正则引擎照样躺在首屏 chunk 里 ——
「大多数对话里没有代码块」这个理由完全落空。

**当前产物**（`bun run build`，实测）：

| chunk | 大小 | 什么时候下载 |
|---|---|---|
| `index`（应用代码） | 258 KB | 首屏 |
| `vendor`（react / react-dom） | 219 KB | 首屏 |
| `markdown`（react-markdown 全家） | 120 KB | 首屏 |
| `rolldown-runtime` | 716 B | 首屏 |
| `index.css` | 51 KB | 首屏 |
| `core` + `engine-javascript`（shiki） | 93 + 58 KB | **第一次出现代码块** |
| `typescript` / `tsx` / `jsx` / `javascript` / … | 各 41–181 KB | 按语言 |
| `github-dark` / `github-light`（主题） | 各 11 KB | 按主题 |
| `settings-dialog`（CodeMirror） | 653 KB | **打开设置弹窗** |

★ 首屏三个 JS 块**各自**都远低于 500KB 阈值（合计 597 KB）；
连 CSS 与 runtime 一起算，首屏是 648 KB。
★ **剩下的那块 653 KB 是设置弹窗**，它懒加载，不在首屏路径上。
构建里为此把 `chunkSizeWarningLimit` 放到 700（`web/vite.config.ts:48`）——
阈值是抬了，但**信号还在**：首屏路径上任何块重新涨过 500KB 仍然会报。

★ **待办（上游那轮就提了，没做）**：设置弹窗那一块之所以有 653 KB，是因为
`@uiw/react-codemirror` 把**整套 CodeMirror 6**拖进来了 ——
它的依赖里有 `codemirror` 元包（连带 `@codemirror/autocomplete` 与 `@codemirror/lint`）
和 `@uiw/codemirror-extensions-basic-setup`，而配置里明明写着 `autocompletion: false`。
项目自己已经直接依赖了 `@codemirror/*` 的那几件
（view / state / commands / search / language / lang-markdown / lang-yaml），
去掉这个 React 包装层、改用裸 `EditorView`（约 50 行胶水）即可，
E2E 里那几条编辑器用例能守住行为。

### 1.4 目录结构

```
web/src/
  App.tsx                     直接渲染 Shell，没有 provider
  main.tsx                    React 挂载点
  index.css                   全局样式与 CSS 变量
  app/shell.tsx               布局 + 挂载引导
  components/
    sidebar.tsx               项目分组的会话列表（详见 docs/sidebar-design.md）
    message-list.tsx          消息流 + 自动滚动 + 压缩分隔线
    message-parts.tsx         text / reasoning / attachment / tool 四种 part
    markdown.tsx              markdown 渲染（含代码高亮的覆写点）
    composer.tsx              输入区（自增长、@ / 命令接线、三个入口）
    composer-popup.tsx        输入提示弹层（纯展示）
    composer-meta.tsx         输入框下方的 agent / 模型 / 思考档位 / 上下文
    agent-picker.tsx          /agents 的选择器（列全部 agent）
    skill-picker.tsx          /skill 的选择器（name + description + 模式）
    agent-filter.tsx          多 agent 的过滤条 + 用量分账 chip
    tool-card.tsx             单个工具调用的一行
    tool-group.tsx            连续调用并成的那一段
    approval-card.tsx         审批卡（等待审批时取代 composer）
    authorization-picker.tsx  会话的授权模式（三档）
    operations-panel.tsx      会话的活动时间线
    projects-dialog.tsx       项目表与项目根
    mcp-panel.tsx             /mcps 的面板
    file-preview.tsx          附件的放大浮层
    attachment-chip.tsx       附件的 chip
    code-block.tsx            代码块（懒加载语言 chunk）
    compaction-divider.tsx    压缩边界
    theme-toggle.tsx          明暗切换
    locale-toggle.tsx         语言切换
    settings/                 配置编辑器（★ 对话框与其入口分文件）
      settings-entry.tsx        入口 + ⌘, 监听（首屏，**只几十字节**）
      settings-dialog.tsx       对话框外壳（懒加载）
      file-list.tsx             文件列表
      config-editor.tsx         CodeMirror（懒加载）
      conflict-dialog.tsx       409 冲突
      new-file-dialog.tsx       新建文件
      secrets-panel.tsx         Secrets 标签页
      footer.tsx                保存 / 状态提示
    ui/                       shadcn 组件（Radix 原语 + 复制进仓库的代码）
  lib/
    types.ts                  Message / Part / Interrupt / TurnStatus
    events.ts                 纯函数 reducer 与事件类型声明
    sse.ts                    SSE 分帧
    store.ts                  barrel（导出 store 的公开面）
    store/                    store 的实现，拆成五个文件
      state.ts                 StoreState / SessionEntry / Attachment
      actions.ts               全部 action
      events.ts                事件路由与 promote
      history.ts               seedFromHistory：历史水合
      selectors.ts             全部 selector 与 EMPTY_* 常量
    api.ts                    /v2 类型化客户端 + runStream + sessionStream
    session-events.ts         会话的公告通道（三种事实）
    types.ts / turns.ts       轮次边界与角色标签标记（纯函数）
    agents.ts                 agent 归集、按 agent 过滤、token 缩写（纯函数）
    approval.ts               审批内容的工具名 / 参数摘要（纯函数）
    authorization.ts          三档授权模式的文案
    tool-groups.ts            连续调用的归并与摘要（纯函数）
    tool-titles.ts            工具名 → 人话标题
    tool-icons.ts             工具名 → 图标
    tool-args.ts              工具参数的还原
    highlight.ts              shiki 高亮器（懒加载）
    markdown 相关             见 components/markdown.tsx
    projects.ts               项目分组（纯函数）
    secrets.ts / secrets-api.ts / secrets-store.ts
    settings-api.ts           /v2/settings 类型化客户端
    settings-store.ts         弹窗状态（草稿、选中项）
    settings-validate.ts      客户端校验（★ 分 error/hint 两级）
    commands.ts               命令表缓存、过滤、动作识别、/skill 解析
    agent-info.ts             默认 agent 摘要、缓存与单飞请求
    skill-info.ts             技能的名称/描述/执行模式摘要
    use-agent-badge.ts        Composer 的 agent 信息取数 hook
    composer-triggers.ts      @ / 命令的 caret 解析（纯函数）
    scroll-follow.ts          滚动跟随的判据（纯函数）
    active-thread.ts          「当前是哪个会话」的 localStorage 记忆
    theme.ts                  明暗偏好
    i18n.ts                   语言的决定与持久化（纯逻辑，不依赖框架）
    locales/                  界面文案的目录（英中并排）
      index.ts                合成 catalog，导出 MessageKey
      parts/                  一个界面一份
        common.ts             到处都一样的那些词
        header.ts             页头
        lib.ts                纯函数产出的文案
        sidebar.ts            侧栏
        composer.ts           输入区
        messages.ts           消息区
        projects.ts           项目与两个组标题
        settings.ts           设置面板与 MCP 面板
    use-t.ts                  useT()：组件拿到翻译器的地方
    thinking.ts               思维链的那句英文
    file-kind.ts              文件类型判定（纯函数）
    format-size.ts            字节的显示（纯函数）
    attach.ts                 附件的挑选与校验
    fixtures/                 真后端录下的事件序列与原始帧
web/test/
  *.test.ts                   纯函数层的单测
  e2e/                        Playwright spec（24 个文件，见 §13.3）
    fixtures.ts               e2e 专用夹具（假后端脚本、选择器常量）
  fake-server.ts              确定性假后端
  serve.ts                    假后端可执行入口
```

★ **`web/src/lib/store.ts` 只是一个 barrel**（29 行，只有 re-export），
实现在 `web/src/lib/store/` 的五个文件里。
★ 对外的 import 路径没变（`from "@/lib/store"`）—— 这是**纯内部重构**。

## 2. 会话身份

**一个会话只有一个 id。**

这是整个前端最重要的一条简化。上一版每个会话前后有两个：

```
slot      —— 列表的 key、本地占位
sessionId —— 后端认的 id，所有 HTTP 请求都用它
```

两者必须收敛成同一个，收敛失败时历史加载打 404，历史整个空掉。
现在：**建会话**与**跑一轮**是两次请求 ——
`POST /v2/sessions` 换一个 id（`web/src/lib/api.ts:430-457`），
`POST /v2/run` 带上它跑一轮。

于是：

- 打开页面 / 点 New thread / 什么都不说 → 服务端一个会话都不增加
  （`web/src/lib/store/actions.ts:448-462` 的 `startNewThread` 只动本地状态）。
- 刷新页面 → 回到同一个对话，且那个对话一定真实存在。
- 侧栏条目数只由 `GET /v2/sessions` 决定，不存在「删不掉的占位条目」。

★ 新会话在 store 里先落在 `pending:<时间戳>` 这个**仅内存**的键上
（`web/src/lib/store/actions.ts:616`），建会话成功后**就地换成**真实 id
（`web/src/lib/store/events.ts:25-44` 的 `promote`）。
★ **就地换**是关键：换键而不是搬键，搬键会让这个会话在 `order` 里换位置。

★ 后续事件写进哪个键由**模块级** `currentRunKey` 跟踪
（`web/src/lib/store/events.ts:10`）—— 它必须记在模块级，
因为每个事件回调仍然带着最初的 `localKey` 进来
（`web/src/lib/store/events.ts:12-23` 的 `handleEvent`）。

★ **主路径不等 `RUN_STARTED`**：会话先建、id 先拿到，然后才发流
（`web/src/lib/store/actions.ts:675-712`）。
⇒ `RUN_STARTED` 里的那个 promote 分支降级成**兜底**，
它的触发条件是键仍以 `pending:` 开头（`web/src/lib/store/events.ts:16`）。

## 3. 事件流

### 3.1 SSE 分帧（`web/src/lib/sse.ts`）

`takeEvents(buffer)` 返回 `{ events, rest }`（`web/src/lib/sse.ts:7-22`）：
`rest` 留给下一次调用，因为网络会把一个 JSON 拦腰截断。
`readSse(body)` 是基于它的异步迭代器（`web/src/lib/sse.ts:55-76`）。
帧边界同时认 `\n\n` 与 `\r\n\r\n`（`web/src/lib/sse.ts:24-30`）。

★ **冒号后有没有空格，两种写法都收**（`web/src/lib/sse.ts:40-47`）：
按 SSE 规范剥掉**一个**前导空格。
★ 上游那份文档说「按 `data: ` 严格匹配的解析器会一条都收不到，而且不报错」——
那是上游当时的写法；这里的解析器不依赖这一点，兼容写法被当作
「兼容别的服务端」显式写进了测试（`web/test/sse.test.ts:17-25`）。

★ 测试用**真后端录下的原始帧**（`web/src/lib/fixtures/recorded-frames.ts:2`）——
手写 `data: {...}` 会顺手带上空格，而空格会不会被收正是要验的那件事。

### 3.2 reducer（`web/src/lib/events.ts`）

```
[event, state] → state
```

两个关键设计：

**part 的身份是 `(messageId, kind)`，不是 messageId 单独决定。**
后端在同一 msgID 上交替发推理与正文，按 messageId 找「当前 part」会让两段互相覆盖。
⇒ 「正在接收的那一段」= **该 kind 里最后一个 `complete === false` 的**
（`web/src/lib/events.ts:215-248` 的 `openPart`），
不额外维护 `openPartId` 指针 —— 那样状态里就多了一份必须与 messages
保持同步的真相，同步迟早会漏。

★ 判据抽成了一个函数而不是内联比较（`web/src/lib/events.ts:182-191` 的 `isOpenPart`）：
联合类型变宽之后（`attachment` part 没有 `complete` 字段），
`p.complete` 必须先收窄才能读。

**事件类型本地声明**（`web/src/lib/events.ts:160-174`）：
union 成员 **14 个**，而 type 判别式是 **16 个** ——
`ReasoningStartEvent` 与 `ReasoningEndEvent` 各含两个 type
（`web/src/lib/events.ts:63-68`、`:77-81`）。

★ 上游给的理由是「官方 union 写全会让 switch 的 `default` 分支变成理论上不可达，
于是后端新增事件会**静默**落进 default」——**这个理由在本仓库的代码里没有留下文字依据**，
`default` 分支存在（`web/src/lib/events.ts:394-396`，返回原 state）。
★ 留下来的**是那条测试**：fixture 覆盖了后端实际发出的每一种事件
（`web/test/events.test.ts:9-30`，fixture 源 `web/src/lib/fixtures/recorded-run.ts:3`），
后端加一种事件就在测试期挡下。

### 3.3 后端实际发出的事件

发射点在 `transport/transport.go`，逐帧产生：

| 事件 | 发射点 | 说的是什么 |
|---|---|---|
| `RUN_STARTED` | `transport/transport.go:77` | `threadId` 与 `runId` |
| `RUN_FINISHED` | `transport/transport.go:88`、`:94` | outcome：interrupt / error / success |
| `RUN_ERROR` | `transport/transport.go:372` | `message` 与 `code` |
| `TEXT_MESSAGE_START` / `_CONTENT` / `_END` | `:377` / `:388` / `:394` | 正文 |
| `REASONING_START` + `REASONING_MESSAGE_START` | `:400-403` | 思维链开始 |
| `REASONING_MESSAGE_CONTENT` | `:405` | 思维链增量 |
| `REASONING_MESSAGE_END` + `REASONING_END` | `:409-412` | 思维链结束 |
| `TOOL_CALL_START` / `_ARGS` / `_END` | `:414` / `:424` / `:430` | 一次工具调用 |
| `TOOL_CALL_RESULT` | `:441` | 工具的结果 |
| `CUSTOM "context_usage"` | `:245` | 一个 agent 的上下文占用 |
| `CUSTOM "context_compacted"` | `:268` | 刚刚发生过一次压缩 |
| `CUSTOM "model_retry"` | `:285` | provider 正在重试 |
| `CUSTOM "run_started"` | `:359` | 一轮开始（公告通道用） |
| `CUSTOM "approval_pending"` | `:328` | 有一个审批在等 |
| `CUSTOM "approval_resolved"` | `:343` | 那个审批被答了 |

★ **`REASONING_MESSAGE_START` 的 `role` 必须是字面量 `"reasoning"`**
（`transport/transport.go:400` 的 `reasoningRole`）。
传 `"assistant"` 时 Go 侧不报错，而前端会把整条事件拒收 ——
表现是「推理一出现，后面所有内容都没了」。
这条由 Go 侧的测试钉住（`transport/transport_test.go:670`）。

★ **`TOOL_CALL_RESULT` 只有内容**（`transport/transport.go:447`）：
`events.NewToolCallResultEvent(uuid.NewString(), callID, content)` ——
三个参数是消息 id、调用 id、内容，**没有出错这一位**。
⇒ 界面今天没法知道一次调用是失败了还是成功了（§17.4）。

## 4. 状态

### 4.1 store 的形状

```ts
sessions: Record<string, { meta, convo?, loaded, operations? }>
order: string[]            // 侧栏顺序
activeId: string | null    // null = 欢迎界面
```

类型在 `web/src/lib/store/state.ts:18-32`（`SessionEntry`）与 `:35`（`activeId`）。
★ `convo` 是**懒加载**的：只有打开过的会话才有对话状态
（`web/src/lib/store/actions.ts:376-382` 先放空 `convo` + `loaded: false`，
拉到 `/v2/sessions/:id/messages` 后再填）。

★ 会话条目上还有几样别的东西：`operations`（这个会话的操作日志）、
`pendingAgentId`（欢迎页暂选的 agent）、`projects` / `activeProjectId`
（见 `docs/projects-design.md`）。

### 4.2 selector 必须返回引用稳定的值

zustand 默认用 `Object.is` 比较快照。selector 每次调用都造新数组/新对象
（`order.map(...)`、`?? []`、`emptyConversation()`）的话，比较永远不相等
→ 每次渲染都触发更新 → React 报 `Maximum update depth exceeded`，整页白屏。

所以：

- 空值用**模块级常量**，绝不写 `?? []`：`EMPTY_MESSAGES` / `EMPTY_INTERRUPTS`
  （`web/src/lib/store/selectors.ts:26-27`）、`EMPTY_ATTACHMENTS`（`:62`）、
  `EMPTY_OPERATIONS`（`:69`），另有一个私有的 `EMPTY_CONVERSATION`（`:28`）。
- 返回数组的 selector（`selectSessionList`，`web/src/lib/store/selectors.ts:99-101`）
  **必须**配 `useShallow`（`web/src/components/sidebar.tsx:54`）。
- 其余 selector 返回 store 已持有的引用，引用天然稳定
  （`web/src/lib/store/selectors.ts:30-33`、`:54-60`、`:76-78`、`:94-97`）。

修法不是加 `useCallback`，是让返回值本身稳定。

★ 第二个 zustand store（设置弹窗，`web/src/lib/settings-store.ts`）守同一条规矩：
它的空值是 `EMPTY_ITEMS` / `EMPTY_CATEGORIES` / `EMPTY_SERVER_ISSUES`
（`:312-314`），而返回数组的 selector 明确写着「每次调用都是新的，所以要用 `useShallow`」
（`:332-333`）。

### 4.3 审批挂起放在状态上

```ts
status: "idle" | "running" | "awaiting-approval" | "error"
interrupts: Interrupt[]
```

不放在某条消息的 metadata 上（`web/src/lib/types.ts:87`、`:140-141`）——
上一版是后者，而查找函数用 `findLast` 定位「最后一条 assistant 消息」，
消息顺序一变、或中间插入一条，中断就再也找不到了。

`seedFromHistory` 会从 `GET /v2/sessions/:id/messages` 的 `pendingApprovals`
恢复它（`web/src/lib/store/history.ts:159-170`），所以刷新或切换回来，挂起还在。

★ **审批与授权模式是两件事**：授权模式决定闸门**要不要问**
（`docs/authorization-design.md` 的决策表），审批卡是**问了之后**人怎么答。
两者不冲突，但文档必须分开写 —— 见 §11 与 §8。

### 4.4 批处理：每个 token 不都 setState

★ 流式下每个 delta 都 setState 会让 markdown 每帧重解析一遍
（`web/src/lib/api.ts:665-673` 的 `FRAME_MS` = 33ms 批处理）。

- 紧急帧**不批处理**（`web/src/lib/api.ts:675-680` 的 `URGENT_EVENTS`：
  `RUN_STARTED` / `RUN_FINISHED` / `RUN_ERROR`）—— 用户马上要点的按钮不能等。
- ★ **终止帧**（`web/src/lib/api.ts:682-691` 的 `TERMINAL_EVENTS`）区分
  「轮次正常结束」与「流被截断」：调用方据此决定要不要去恢复那一轮
  （`web/src/lib/store/actions.ts:886-894` 的 `recoverRound`）。

## 5. 界面骨架

### 5.1 布局

```
┌──────────────────────────────────────────────────────────┐
│ ┌──────────┐ ┌────────────────────────────────────────┐ │
│ │ sidebar  │ │ header: 折叠钮 / openheron / 状态点      │ │
│ │  256px   │ ├────────────────────────────────────────┤ │
│ │          │ │                                        │ │
│ │ 项目分组  │ │  message-list（可滚动）                │ │
│ │  会话     │ │                                        │ │
│ │          │ ├────────────────────────────────────────┤ │
│ │          │ │  composer 或 approval-card（互斥）      │ │
│ └──────────┘ └────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────┘
```

侧栏 256px（`--sidebar-width`，`web/src/index.css:56`；用在 `web/src/components/sidebar.tsx:104`）。

★ **对话列的宽度是一个 token**（`--chat-column`，`web/src/index.css:24`），
消息列表、输入框与审批卡都用它 —— 所以「更宽」是一处改动，不是六处。

★ **列里的文字吃满整列。** 正文过去停在 `--prose-column`（46rem），而列是 64rem ——
于是每条助手正文右边空出 224px，而用户的气泡正贴在列的右缘上：宽屏上答案显得被挤在左边，
右半屏空着。⇒ 消息里的段落、标题、清单与引用都吃满容器
（`web/src/components/markdown.tsx:26` 的 `FILL`），代码块与表格本来就吃满整列。
`--prose-column` 留下来给**独自读的那一块**：文档（文件预览，
`web/src/components/file-preview.tsx:189` 的 `measure`）—— 行文是独自读的，
六十个字符长的一段话是难读的。

★ **欢迎页的输入框有它自己的宽度**（`--welcome-column`，52rem = 832px，
`web/src/index.css:51`；用在 `web/src/app/shell.tsx:209`）。它过去跟着对话列走
（1024px），宽屏上横跨半屏 —— 可这一页上只有一句问话，没有长答案可读。
★ **整块一起收**：品牌区、输入框与下面那行元信息共用一个宽度；只窄化输入框的话，
那行元信息会从它两侧溢出来。
⇒ 发出第一句话、界面变成对话之后，输入框回到对话列的宽度
（`web/src/components/composer.tsx:561`）—— 欢迎页是「问一句」，对话是「读一段」。

★ **那行元信息在欢迎页上必须留住名字。** 项目名与授权档位住在它下面；容器窄过 48rem
时，这两个就只剩图标（名字在 tooltip 与读屏标签里）。★ 欢迎页是**第一屏** ——
项目是这段对话在哪个目录里，授权档位是「agent 不用问就能做什么」的决定，
所以欢迎页那一列**留在 48rem 以上**。⇒ 这条是钉着的：
`web/test/e2e/chat.spec.ts` 的「窄下来以后，那一行的事实还留着名字」。

★ **等待审批时审批卡取代 composer，不挂在消息流里**
（`web/src/app/shell.tsx:218` 的二选一）。
审批发生在轮次**结束**之后，此刻输入框本来就不能用；
留着一个灰色的空输入框会让用户以为还能打字，也把真正要做的决定挤到上面去。
不插进消息流是因为流已经收尾，插进去等于要等用户滚动才看得见。

### 5.2 侧栏

★ 本文只讲前端这一侧；分组、行、菜单与「当前项目」那份真相在
`docs/sidebar-design.md`。

- 「New thread」只回到欢迎界面：不碰服务端、不碰 localStorage
  （`web/src/lib/store/actions.ts:448-462`）。
  ★ 清 localStorage 的话刷新页面就回不到上一个对话了。
  ★ 它顺带把「当前项目」清掉 —— **新会话没有项目**（会话的项目不可变，
  继承上一个会话的项目等于把用户没挑过的目录按死；见 `docs/projects-design.md`）。
- ★ **记住的会话没了 ⇒ 清掉那个 id**：服务端一个会话都没有时
  （`web/src/lib/store/actions.ts:314-350`），或打开它时答 404（被删过，
  `:404-412`），localStorage 里那个 id 一定是悬空的 —— 清掉并回欢迎界面。
  不清的话每次刷新都去打那个不存在的会话，界面还甩一条「session not found」。
- 每行有 `data-testid="thread-row"`（`<li>`，`web/src/components/sidebar.tsx:582`）
  与 `data-testid="thread-item"`（可点击按钮，`:587`）两个定位点 ——
  More options 按钮是按钮的**兄弟**而非后代（`:585-592`），开菜单要从行去找。
- 改名走乐观更新，失败回滚（`web/src/lib/store/actions.ts:511-513`）。
- 删除正在看的那条 → 回到欢迎界面（`web/src/lib/store/actions.ts:536`）。
- 侧栏折叠状态持久化在 localStorage（`web/src/app/shell.tsx:21`、`:41-48`）。

### 5.3 自动滚动

判据是**用户离底部有多近**（32px 内，`web/src/lib/scroll-follow.ts:13` 的
`BOTTOM_TOLERANCE`），不是「有没有新消息」。
用户往上翻看历史时来了新 token，把视口拽回底部是最烦人的一种行为。

★ 上翻后 `pinned=false`（`web/src/lib/scroll-follow.ts:46-60`），
右下角出现 `Jump to latest` 按钮（`web/src/components/message-list.tsx:171-194`）。
★ **那个按钮是瞬时滚动，不是平滑滚动**（`web/src/components/message-list.tsx:120-136`）：
平滑滚动会产生一串「位置不在底部」的中间事件，被跟随规则读成「用户滚走了」。

### 5.4 状态点

`awaiting-approval` 与 `running` 各占一档（`web/src/app/shell.tsx:28-33` 的 `DOT` 表）。
★ **等审批时要看得见**：等审批时轮次已经结束，
显示成「Idle」会让状态点在最需要提醒的那一刻什么都不说。

### 5.5 文案

★ **界面文案是一份目录**（`web/src/lib/locales/`）：每一条把英文与中文**并排**放着，
读者用什么语言，屏幕上就是哪一句。⇒ 一句新话只写一次、写在一处、两种语言一起写；
不存在的键是**编译错误**（`MessageKey` 就是 `keyof typeof catalog`），不是界面上的一个洞。
★ 目录按界面分文件（`parts/`），所以两个界面可以同时改文案而不碰同一个文件。

★ **语言在一处决定**（`web/src/lib/i18n.ts`）：存过的偏好 → 浏览器的语言 → 英文。
★ 存过的偏好**压过**浏览器 —— 用户已经说过他要用哪种语言，浏览器只是猜测。
★ `<html lang>` 跟着变（读屏软件靠它决定怎么断句：中文用中文规则念，英文用英文规则念）；
模块在 React 之前跑，所以**第一帧**就是对的。

★ **英文兜底**：一个还没写中文的键显示英文，而不是一个洞 —— 这条规则还在，
只是现在没有键用它了。`web/test/i18n.test.ts` 里的计数断言两种语言都写满。
★ **当前状态**：机制与全部界面文案都在目录里（**313 条**，含主题开关的两条），
中文一栏**全部写完** ⇒ 切到中文，界面说的就是中文。当初先立机制（键位对齐、
兜底、开关、测试）再填中文，是为了让译文落进一个已经稳住的骨架。
★ 两种语言写在同一条里，键位**不会漂**：编译期就知道键对不对。
★ 两条检查住在 `web/test/i18n.test.ts` 里，CI 会跑：**没有没人用的键**
（一条白写的句子翻译器永远不知道它存在），**界面里没有写死的英文**
（写死在组件里的句子翻不了）。★ 第一条检查按**字符串出现**计数，
不只算 `t("…")` —— `tool-titles.ts` 那张表就是一个键一个字符串。

★ **不翻的部分**：服务端发来的句子（API 响应体、`/v2/config` 的 `deferred`）保持英文 ——
AGENTS.md 的运行时规则不管界面是什么语言。模型输出、工具名（`read_file`）、
时间戳格式也不翻：`operations-panel.tsx` 的 `formatAt` 手写格式，
它的注释写着「日志的列不跟着语言跑」。

★ **测试里语言被钉住**（`web/playwright.config.ts` 的 `locale: "en-US"`）：
E2E 断言的是英文文案，一次钉住，那 87 条断言一条都不用改。
⇒ 没有这一行，一个把浏览器设成中文的开发者会看到满屏红。
★ 语言 spec（`web/test/e2e/i18n.spec.ts`）断言 `lang` 属性、记住的偏好，
以及切换后界面文案真的变成中文（当时期望的是英文兜底，中文填好后改为断言中文）。

### 5.5.1 语言开关

页头**最右端**，紧挨主题开关（`web/src/components/locale-toggle.tsx`）：★ 两者都是
**这台机器上的偏好**，都是一下点开。★ 按钮上写的是**要切过去的**那种语言自己的名字
（「中」/「EN」）—— 不读当前语言的人也认得；无障碍标签用文字说明。

★ 两个开关**并成一组**放在行的右端（`web/src/app/shell.tsx`）：语言开关原来夹在
agent 过滤条与主题开关之间，孤零零地杵在标题中间 —— 那是一个外壳按钮，却站在
对话标题该在的位置上。★ 主题开关留在最右（它本来就在那儿），语言开关挪到它旁边。

### 5.6 输入框下面那一行

输入框下面那一行有两半：

* **事实**：agent、厂商/模型、思考档位、上下文占用。
* **入口**：授权选择器、项目、操作时间线（§11、§12）。

### 5.6.1 事实那一半

数据全部来自 `GET /v2/agents`：

- 按**活动 agent 的 id** 选中那一条，不依赖 `agents[]` 顺序
  （`web/src/lib/agent-info.ts:14-22`）。
- `agents[].model.provider` / `model` 组成厂商/模型。
- `model.reasoning` 是**生效档位**（agent 指定，或模型 `reasoning_default`），
  为空显示 `off`。
- `reasoningAvailable` 只放进 `title`，不铺开成选择器
  （`web/src/components/composer-meta.tsx:139-144`）。

★ **取数与缓存**：`web/src/lib/agent-info.ts` 负责取拓扑、按 id 选条目、归一化空
reasoning、模块级缓存与单飞请求；`api.listAgents()` 用 `cache: "no-store"`
（`web/src/lib/api.ts:536`），因为配置支持热更新。

★ **现在有轮询**：每 3 秒查一次**配置指纹**
（`web/src/lib/use-agent-badge.ts:6` 的 `CONFIG_POLL_MS`、
`:64-82`；指纹取自 `GET /v2/health` 的 `configVersion`，
`web/src/lib/agent-info.ts:81-107`），指纹变了才拉整张表。
★ 上游那份文档写的是「没有轮询」—— 那时配置没有热更新通道；
现在配置可以被编辑器改、被 `git pull` 换、被另一台机器共享同一份 config root 换，
而运行期没有推送通道 ⇒ 轮询指纹是唯一说得出口的办法。
★ **隐藏的标签页不查**（可见时补一次）。

★ **切换 agent 不重新取数**：拓扑没没变，变的只是从哪一条读。

**状态**：

- 首次加载显示一行骨架，避免居中版式跳动；
- 请求失败但已有旧值时继续显示旧值；
- 从未成功过则显示 `Agent info unavailable`，`title` 带错误
  （`web/src/components/composer-meta.tsx:126-135`）；
- 加载失败不阻塞输入、发送与审批。

★ **上下文占用**也在这一行（`web/src/components/composer-meta.tsx:244-316` 的
`ContextMeter`，`data-testid="composer-meta-context"`）。

### 5.6.2 版式：换行，不被挤成半个

★ **列窄时一个名字要么整个出现，要么换到下一行 —— 不许被挤成
「deepsee…」**：一个被切掉一半的名字，说得比一个换行的名字更少。
⇒ 每一项都是**一个整体**（`shrink-0`）：它不会为了挤进行里而缩窄，
它只会**换行**；只有在名字比**整行**还长的时候才截断（`max-w-[min(…,100%)]`）。

★ **唯一的例外是模型名**（§5.6.4）：那一行的**优先级**写在下面 ——
上下文占用先保，模型名次之，agent 名**一定出现**。
⇒ 列窄时模型名**可以**被截断，而 agent 名与上下文占用**永不被切**。

★ **分隔符跟着后一项走。** 分隔符是**后一项**的一部分，不是一个自己排版的节点
（`web/src/components/composer-meta.tsx` 的 `Joined`）——
一个自己排版的分隔符会落在行尾（一个悬着的「·」）或者行首（一个接上文的东西）；
⇒ 它跟着它带来的那一项一起换行。

★ **状态句自己占一行**（`notice` / `busy`）：一句状态和旁边的事实是两种读法，
挤在一行里，两种都更难读。

### 5.6.3 列窄时事实只有一行

★ 输入框下面那一块是**手机上最大的一块**，它坐在键盘上面，
它的每一像素都是从聊天区里扣下来的。★ 在 390px 的屏幕上，
它原来占 **65px**（事实两行 + 入口一行）—— ★ 比输入框本身（50px）还高。

★ 列窄时事实**必须只有一行**（§5.6.5：列 < 512px），于是让位的是**标签与细节**，
留下的是**身份与那个数**：

| 项 | 列宽时 | 列窄时 |
|---|---|---|
| agent | `Agent: product-manager` | `product-manager`（「Agent: 」是宽列的标签，页头本来就写着 Agent） |
| 模型 | `deepseek/deepseek-flash` | **让位** —— ★ 它是这一行里优先级最低的一项（§5.6.4） |
| 思考档位 | `Reasoning: low` | **让位**，进模型的 `title` |
| 上下文 | `Context: ≈15k / 1.0M (1.4 %)` | `1.4 %` + 占用条（token 数与窗口进 `title`；★ 占用条短一截，§5.6.4） |
| 入口 | 三个按钮带文字 | **只留图标**，并且**和事实同一行**（§5.6.4） |

★ **让位的东西不许丢**：思考档位进**模型**的 `title`
（`web/src/components/composer-meta.tsx:194`），上下文数字进占用条的 `title`
（`:283`）—— ★ 手机是**按需读**提示气泡的，而那一行是**一眼扫**的。

★ 入口让位的方式也一样：三个词都让位，**三个图标都留下**，而且它们
**留在事实那一行**（§5.6.4）—— ★ 入口还在，入口还认得出来，
而那一行不必再占第二行。

**结果**：65px → 45px → **24px**（一行），省下的 41px 回到聊天区。

### 5.6.4 入口：列窄时只留图标，与事实同一行

★ 三个入口在**同一个** `span` 里（`web/src/components/composer.tsx:744-752`），
那个 `span` 是这一行**最后**的子节点，位置恒定（§11.1）。
★ 列宽时它们在右端；列窄时**还是在右端**（`ms-auto`），但它们**和事实同一行**。

| 入口 | 列宽时 | 列窄时 |
|---|---|---|
| 授权 | 🛡 `Default approval` | 🛡 |
| 项目 | 📁 `Default Project` | 📁 |
| 活动 | 🕘 `Activity` | 🕘 |

★ **为什么**：那一行坐在键盘上面，它的每一像素都是从聊天区里扣出来的；
而一个图标是入口的**形状** —— 是三个入口各自唯一的、认得出来的东西。

★ **怎么腾出这一行**：省下的是**看不见的东西** ——
三个入口的透明边框（`@max-3xl:border-0`）、它们的左右留白（`@max-3xl:px-1`）、
入口之间的间距（`@max-3xl:gap-0.5`），以及占用条的长度（`@max-3xl:w-8`）。
⇒ 三个入口从 84px 宽收到 **64px**，事实与入口**同一行**
（390px 上事实与入口之间还余 21px —— 这 21px 里模型名最多再用掉 14px，
它的上限是「行宽 − 12rem」）。

★ **图标不长大**：入口在小屏幕上也不变大 —— 那一行的高度就是
**24px**（图标按钮的高度），它省下来的每一像素都回到聊天区。

★ **挤不下时谁让位**：**上下文占用先保，模型名次之，agent 名一定出现**。
⇒ 让位的是**模型名**：列窄时它可以缩（`@max-3xl:shrink`），
也可以被截断（上限 = 行宽减去被保下来的那些，约 12rem ——
agent 名、上下文、三个入口与间隙）。
⇒ 而 agent 名与上下文占用是 `shrink-0`：**永不被切**。

| 项 | 挤不下时 |
|---|---|
| agent | **不让位** —— 它是「在跟谁说话」 |
| 上下文 | **不让位** —— 它是那个数 |
| 模型名 | **让位** —— 截成「deepseek/deepse…」，全名在 `title` 里 |

★ 390px 上模型名是**整个**的（还有 21px 余量）；360px 上它开始让位，
那一行**仍然只有一行** —— ★ 让位的是一个名字，不是整行聊天。

★ **让位的词不许丢**：每个入口的 `aria-label`（读屏读到的名字）与
`title`（提示气泡）都还带着它 —— 授权档位的 `title` 现在是
「档位名 — 档位说明」，项目名与活动面板的名字也都在。
★ 项目的名字还有第二个家：侧栏里它就是一个**文件夹**，名字写在那里。

★ **项目名是用户起的，可能比手机还宽。** ⇒ 在**列宽**时名字**截断**
（`web/src/components/projects-dialog.tsx`），而**全名**留在两处：
tooltip（`title`）与读屏读到的标签（`aria-label`）。
★ 名字被截断不等于名字丢了。

### 5.6.5 判据是**列宽**，不是窗口宽

★ 这一行住在**聊天列**里，它有多少地方取决于**列**，不取决于窗口。

★ **平板竖屏就是那个证明**：窗口 960px 宽，看着像台电脑，
可是常驻的侧栏拿走了 256px，列只剩 **~670px** —— ★ 一个手机那么宽的地方，
长在一扇像电脑的窗里。⇒ 那一行按**列**算，不按**窗**算。

⇒ 这一行的规则全是**容器查询**（`@container` + `@max-lg:` / `@max-3xl:`），
容器是 composer 的根（`web/src/components/composer.tsx:515` 的 `@container`）。

| 列宽 | 事实 | 入口 | 模型名 |
|---|---|---|---|
| ≥ 768px（`@3xl`） | 完整版 | 带文字 | 完整 |
| 512–768px | 完整版 | **只留图标** | 可让位 |
| < 512px（`@lg`） | 紧凑版 | **只留图标** | 可让位 |

★ 两个阈值**不是**一回事：入口的词先让（它占 250px，事实占 400px），
事实的紧凑版后让（它省下的 300px 只有列真的挤了才值得省）。


### 5.7 /skill 与 /agents

| 输入 | `/skill` | `/agents` |
|---|---|---|
| 光秃秃 | 打开**skill 选择器** | 打开**agent 选择器** |
| 带一个可用的名字 | 原样发出，服务端展开成「用这个 skill」 | 直接 `PATCH /v2/sessions/:id/agent` |
| 带一个**未知**的名字 | 打开选择器并预填过滤词，**不发送** | 打开选择器并预填过滤词，**不发送** |
| `/skillx` / 正文里的 `/skill` | 不触发，按普通消息发送 | 同 |

- `/skill` 是 **prompt 命令**（`api/commands.go:72-76`），
  `/agents` 是**动作命令，但带参数**（`api/commands.go:52-55`），
  所以后者在发送前被 `web/src/lib/commands.ts:92-102` 的 `agentCommandFor` 截获。
- ★ **没拦未知名字是有意的**：skill 清单是**活**的（新建的 skill 不用重启就能用），
  前端缓存的清单可能过期 —— 拦下来反而会拦住一个刚建的 skill。
- 选择器列出**这一代挂着的** skill：名字、**description**（模型选它的唯一依据）、
  **执行模式**（`inline` / `fork`，`web/src/lib/skill-info.ts:25-27`）；
  选中只**回填命令文本**（`web/src/components/composer.tsx:763-767`），不直接发送。
- 选择器列出**全部** agent（`web/src/lib/agent-info.ts:40-42` 的 `selectableAgents`）
  —— 没有「主 agent」这回事。
- ★ **底栏的 agent 名字也是一扇门**：点它打开**同一个**选择器
  （`web/src/components/composer-meta.tsx:150-177` 的 `onPickAgent`）。
  ★ 一份名单、一种行为 —— 再造一套「点名字换 agent」就是第二份规则。
  ★ 点击**不清空**输入框：命令清空是因为那行字**就是**命令，而点击不是命令。
- ★ **名字按右侧那几个入口的样子渲染**：同一个 `Button` 的 `ghost` / `sm`，
  同一串类名（`web/src/components/composer-meta.tsx:150-177`）。
  ★ hover 就是这一行的 hover —— **不是下划线**。
  ★ 下划线说的是「这是一个链接，通到别处去」；★ 这个名字不离开这一行，
  它打开的就是这一行上已经有的那个选择器。
  ⇒ 一行里四个入口，一套渲染；名字不因为是名字就长得像别的东西。
- ★ **切换请求不做乐观更新**（`web/src/lib/store/actions.ts:464-478`）：
  服务端会以 409 拒绝运行中/待审批的会话，
  先改界面再回滚会让用户看到 task 闪一下、而拒绝原因没地方显示。
  错误留在选择器里（`web/src/components/agent-picker.tsx:107-111`）。
- ★ **没选过就不带 `agentId`**（`web/src/lib/store/actions.ts:1440-1448`）：
  根 agent 由服务端定。前端自作主张发一个 `main` 过去，
  等于把「默认根」按死在界面上 —— 服务端改口时它也不算数。
- ★ 建完会话后，底栏的 agent 用**服务端回的**那个，不用发请求前猜的那个：
  两者可能不同。

### 5.8 ★ 选择器关掉后焦点必须回到输入框

选择器是 Radix Dialog，但**没有 `DialogTrigger`** —— 它是程序化打开的。
而 Radix 的焦点恢复依赖「打开它的那个 trigger」，没有 trigger 就**无处可还原**：
焦点落到 `<body>`。

症状很隐蔽 —— 命令**已经执行完了**，但用户接着敲字发现什么都没进去，
于是会以为「命令没生效」，而不是「焦点跑了」。

所以 `web/src/components/composer.tsx:238-281` 用 `pickerWasOpen` 记住「开过」，
在关掉的那一刻把焦点显式交回输入框（`requestAnimationFrame`）。

★ 用 ref 而不是只看 `picker.open` 的真假（`web/src/components/composer.tsx:112`）：
关掉的那一刻 `open` 已经是 `false` 了，只看它会漏掉「开 → 关」这个跃迁。

★ `/agents <name>` 的**直接切换**路径不开选择器，焦点本来就没丢 ——
但它值得用 spec 钉住，免得以后有人把「恢复焦点」做成「每次切换后都重聚焦」，
那会在用户已经点到别处时把焦点抢回来。

★ 测「焦点还在不在」要用**打字 + Enter**，不能点发送按钮：
按钮在输入框清空后变 `disabled`，焦点会掉到 body —— 那是**测试路径的假象**。

### 5.9 响应式

界面只有**三个**断点，而且它们是 Tailwind 自带的那三个：`sm` 640 / `md` 768 / `lg` 1024。
★ 自定义断点是「第二套真相」：样式表里的一个数字和组件里的一个数字迟早对不上，
而对不上的那天，谁也不知道该改哪一个。

| 视口 | 侧栏 | 聊天 | 弹层 |
|---|---|---|---|
| `< md`（< 768） | **抽屉**，默认关 | 单栏，占满整屏 | 满窗（`dvh`） |
| `≥ md` | 常驻一列（`--sidebar-width`） | 单栏 + gutter | `sm:max-w-*` |

★ 断点本身是 `md`，而 `min-width: 768px` **在 768 上就成立** ——
`web/src/app/shell.tsx:26` 的 `WIDE` 与 Tailwind 的 `md` 是同一个数字。
窄屏/宽屏的判据是 `web/src/lib/use-media-query.ts` 的 `useMediaQuery`：
它订阅查询本身（不是轮询），而且**第一次就同步读出答案** ——
界面在第一个 effect 之前就画出来了，抽屉在桌面上闪一帧就是闪错了版式。

#### 5.9.1 侧栏在窄屏上是抽屉

★ 以前窄屏上的侧栏是**堆在聊天上面**的：整个会话列表先占掉一屏，
要读一段对话得先滚过整个列表。

现在是抽屉（`web/src/components/sidebar-drawer.tsx`）：

- ★ 它是 **Radix Dialog**，不是自己写的浮层。★ Radix 在 document 的
  **capture 阶段**监听 Escape，那个阶段早于 React 的 handler ——
  想留住它只能用 `onEscapeKeyDown`。同一套机制还带来焦点陷阱、滚动锁与
  `aria-modal`，这三样都不值得手写：一个让底下的页面还能滚、或者把焦点丢进
  背后聊天里的抽屉，是跟它所属的界面吵架。
- ★ 抽屉与「折叠」是**两个状态**：折叠是关于桌面的偏好，抽屉是关于屏幕的，
  窄屏用不着第一个（`web/src/app/shell.tsx:47-51`）。
- ★ 宽屏上只渲染**一个**侧栏（`web/src/app/shell.tsx:141-144`）：
  两个都渲染就是两棵会话树，也就是两套读写的会话。
- ★ 选了一个会话就关抽屉：选它是为了读它，而抽屉挡着它。
  判据是「活动会话**变了**」，不是「抽屉开着」——
  否则在当前会话上打开抽屉会立刻把它关掉（`web/src/app/shell.tsx:65-92`）。
- ★ 屏幕变宽时抽屉的状态是**在渲染里**丢掉的，不是在 effect 里：
  effect 会晚一帧才改，抽屉就会在那一帧里开着
  （`web/src/app/shell.tsx:45-82`）。
- ★ 侧栏的高度是**屏幕**的高度，永远不是**会话树**的高度。★ 旧版窄屏把侧栏
  堆在聊天上面，而那一列是 `shrink-0` 的：它有多高就占多高。★ 会话一多，
  整个聊天（连输入框）被推出窗口 —— 有三十段对话的人打开界面只看得见一列
  会话，**输入框不见了**，而且会话越多越是这样。
  现在窄屏上侧栏在抽屉里（`fixed inset-y-0`），树在抽屉内部滚；
  常驻的那一列是 `align-items: stretch` 给的高度，树在它里面滚。

#### 5.9.2 聊天列的高度是框架的高度

★ 这是这一节里最贵的一条规矩。★ flex item 的 `min-height` 默认是 `auto`，
而 `auto` 的意思是「**内容有多高，我就有多高**」。★ 聊天列里装着整段对话，
对话有多高取决于用户聊了多少 —— ★ 所以没有 `min-h-0` 的聊天列会一路长到
对话那么高，把输入框推到几千像素以外，被外层的 `overflow-hidden` 裁掉。

★ 症状是「**闪现，然后消失**」：消息还没加载时对话很短，输入框在；
消息一到，列长高，输入框被推出窗口。★ 消息区同时也不再滚动 ——
它的 `clientHeight` 等于 `scrollHeight`，滚不动，而内容已经在屏幕外面。

★ **只有窄屏中招**：宽屏的容器是 `md:flex-row`，聊天列被拉伸到容器的高度，
自动最小尺寸不生效；窄屏是 `flex-col`，聊天列落在主轴上，`auto` 就落在它身上。

修法是 `min-h-0`（`web/src/app/shell.tsx:156`）：列的高度是框架的高度，
对话在它里面滚。侧栏那一列是同一条规矩（`web/src/components/sidebar.tsx:104`）——
★ 「一个容器的高度是屏幕的高度，永远不是它内容的高度」，这句话要同时管住
会话树和对话记录。

#### 5.9.3 触摸目标

★ 44px 是平台指南都认可的数字。图标还是 32px，**长的是目标**：
一个伪元素盖住最小边长，版式不动 —— 一排按钮在手机上变大，就是一排放不下的按钮。

- `.touch-target`（`web/src/index.css:259`）：在
  `@media (pointer: coarse), (max-width: 767px)` 下，伪元素把命中区域撑到
  `--touch-target`（`web/src/index.css:62`）。
- ★ 带这个类的按钮必须是**孤立的**：目标会长过图标，挨得比最小边长还近的
  邻居会把自己的点击被抢走 —— 所以会话行自己的菜单键不带它（它压在标题上）。

★ 会话行自己的菜单键（`web/src/components/sidebar.tsx:613`）原来在触摸端是
**永远不可达**的：它 `opacity-0`，靠 `group-hover:opacity-100` 显形，
而 Tailwind 把 `group-hover` 编译进 `@media (hover: hover)` ——
触摸屏没有指针，它就永远不出现。
现在窄屏上它一直显形（`.thread-row-menu`，`web/src/index.css:315`），
标题也给它留出了位置（`max-md:pe-8`）。

#### 5.9.4 安全区

★ 刘海与 home 条**不是窗口**。`env()` 在没有它们的设备上答 0，
所以同一份样式表两边都能用；`web/index.html` 的 viewport 带上
`viewport-fit=cover` 之后它才变成非零（`web/src/index.css:68-71`）。

★ 内边距加在**框架**上而不是 body 上（`.app-frame`，`web/src/index.css:286`）：
框架是 `h-dvh`，给 body 加内边距会把框架顶得比窗口还高，
页面就多出一条它本来没有的滚动条。

#### 5.9.5 弹层按动态视口量高度

★ `vh` 是「窗口藏起工具栏之后还剩多少」的**估计**，`dvh` 是**当前**的值：
键盘弹出来、手机浏览器的工具栏收起来，高度都变。
弹层用 `dvh`（`h-[85dvh]`、`max-h-[70dvh]`），不用 `vh`。
★ **设置面板铺满整屏**，它是唯一用 `h-[100dvh]` 的弹层：左边一棵文件树，
右边一个编辑器，而编辑器是这个弹窗存在的理由。

#### 5.9.6 回归网

★ 响应式没有回归网的时候，「看起来对」是唯一的验收标准。
`web/test/e2e/responsive.spec.ts` 用三档视口（390 / 768 / 1440）跑：
页面不横向滚动、聊天列不超 token、抽屉能开能关、弹层不超出视口、
触摸目标够大、那行菜单键看得见。

★ 平板那档（768）是最要紧的一档：它**坐在断点上**，
只有「在断点以上」或「在断点以下」才成立的版式，只有在这里才现形。

★ **会话树的长度**是用户说了算的，所以它有两条专门的回归：
手机上「树再长也挤不掉输入框」，平板上「树再长聊天列也不缩水」。
两条都把树撑到比屏幕还长，然后量输入框的位置**有没有动** ——
量的是「动没动」，不是「在不在」，因为一个被推出窗口的输入框仍然「存在」。

★ **对话记录的长度**同样是用户说了算的，所以它也有两条：
手机上和桌面上各一条「长对话不会把输入框顶出屏幕」。
★ 这两条把记录撑到比屏幕高三千像素，然后量输入框**还在不在窗口里**，
并且量消息区**滚得动**（`scrollHeight > clientHeight`）——
★ 只量输入框不够：一个被推到屏幕外的输入框，位置是「存在」的，
而一个滚不动的消息区，用户看到的是「消息卡住了，推不动」。

★ 窄屏上 `openApp`（`web/test/e2e/fixtures.ts:113`）等不到会话行 ——
会话树在抽屉里，还没被打开。所以窄屏另有一个就绪判据：
页头的状态点与输入框。

## 6. 消息渲染

### 6.1 四种 part

| part | 渲染 |
|---|---|
| `text` | markdown（react-markdown + remark-gfm） |
| `reasoning` | 默认折叠的「Thinking」块，可展开 |
| `tool` | 图标 + 工具名 + 关键参数 + 原始 JSON + 原始结果（§7） |
| `attachment` | 附件 chip（§9） |

分派在 `web/src/components/message-parts.tsx:73-89`。
★ **用户消息按字面渲染**（`web/src/components/message-parts.tsx:51-81`）：
`whitespace-pre-wrap break-words` 直接展示，不经过 Markdown。
原因：`react-markdown` 默认把软换行折叠成空格，多行指令在历史里会变形；
用户写下的 `#` / `**` 也不应被解释成格式。assistant 仍走 Markdown。

★ **reasoning 默认折叠**（`web/src/components/message-parts.tsx:24`）。

### 6.2 markdown 与代码高亮

★ **行文有两个宽度**：消息吃满容器，文档停在可读行宽 —— 判据是**在哪儿读**
（`web/src/components/markdown.tsx:61` 的 `measure`），理由见 §5.1。

★ **不开 `rehype-raw`**（`web/src/components/markdown.tsx:65-66`）——
react-markdown 默认不解析原始 HTML，这是刻意的：
模型输出是不可信输入，开了就等于让模型往页面里塞标签。

语法高亮用 **shiki**（`web/src/lib/highlight.ts` + `web/src/components/markdown.tsx` 的
`code` 组件）。★ **`shiki/core` + 精选语言，不引全量包**
（`web/src/lib/highlight.ts:1`、`:19-25`）：
全量 `codeToHtml` 带所有语言与主题，raw 数 MB；这里只要 10 个语言
（ts/tsx/js/jsx/json/go/python/bash/yaml/diff），用 JS 正则引擎而不是 oniguruma 的 WASM。

三条容易踩的地方：

1. ★ **`structure: "inline"` 是必须的，不是风格选择**
   （`web/src/lib/highlight.ts:68`）。默认的 `"classic"` 返回完整的
   `<pre class="shiki">…</pre>`，而调用方已经有自己的 `<pre>`，
   注入后变成 `<pre>` 套 `<pre>` —— 浏览器把内层当文本，代码块塌成一坨。
2. ★ **高亮失败必须退回纯文本**（`web/src/lib/highlight.ts:70-72` 返回 `null`，
   `web/src/components/code-block.tsx:62-66` 渲染纯文本）。代码块整个消失比没有颜色糟得多。
3. ★ **流式下不高亮正在写的那一段**（`web/src/components/code-block.tsx:45-55`）：
   「文本变过就退回纯文本、安静 250ms 再着色」，等待期间又变了就丢弃那次结果 ——
   否则出现「旧内容的新着色」。

块级与行内的判别用 `InPreContext`（由 `pre` 组件提供，
`web/src/components/markdown.tsx:9`、`:28-30`），**不用
「className 里有没有 language-」**：不带语言标签的围栏代码块 className 是空的，
用那个判据会被当成行内代码。

★ **GFM 表格**：外层 `overflow-x-auto` 承载宽表格
（`web/src/components/markdown.tsx:71`），表格本身 `w-full`
（`:74`），而宽度上下限由**单元格**承担（`:83`：`min-w-[7rem]` / `max-w-[28rem]`）。
★ 上游那份文档写的是「表格 `w-max min-w-full`」—— 那是另一个判据：
`w-max` 会让两列表格撑到全宽，而**列数很多**的时候该靠单元格的 `min-w` 逼出滚动。
★ Tailwind preflight 会清掉表格的默认间距，不补就是一片贴在一起的文字；
不包滚动容器则会把消息列和整页一起撑出横向滚动。

清单同理。preflight 把 `ul` / `ol` 的 `list-style` 与 padding 清成 0，
所以 `ul` / `ol` / `li` 都做了覆写（`web/src/components/markdown.tsx:94-126`）：
无序 `list-disc`、有序 `list-decimal`、左缩进 `pl-5`。
GFM 任务清单带 `contains-task-list`，这类清单改为 `list-none`，只保留复选框。

★ **中英混排。** 全局 `--font-sans` 显式列出平台 CJK 字体回退
（`web/src/index.css:167-169`：PingFang SC / Hiragino Sans GB / Microsoft YaHei /
Noto Sans CJK SC / Source Han Sans SC），不让各平台随机挑字形。
正文为 `15px` + `line-height: 1.75`（`web/src/components/markdown.tsx:170`），
中文密排时仍有行间距；`overflow-wrap: break-word` 让长 URL / 连续拉丁字符
在消息列内折行；支持 `text-autospace` 的浏览器会在 CJK 与拉丁字符之间自动补空隙。

preflight 同样把标题字号/字重重置成正文，所以 `h1`–`h6` 单独覆写
（`web/src/components/markdown.tsx:90-92`）：标题加粗、字号递增，并补 `mt-*` / `mb-*`。

### 6.3 历史消息显示

历史（`GET /v2/sessions/:id/messages` → `seedFromHistory`）与本轮流式事件
走同一个 reducer，但**展示层**有额外约束：历史通常比一屏长，用户是来
查找和复盘的，不是来看最新 token 的。

**轮次分组。** 服务端没有 turn id，`user` 消息是唯一可靠的边界。工具循环
会产出多条连续 assistant 消息，展示时（纯函数在 `web/src/lib/turns.ts:13-19`）：

- 以 `user` 消息开启新一轮；
- 同一轮中只有第一条 assistant 显示角色标签（多 agent 时标签显示 agent 名，
  `web/src/components/message-list.tsx:276-287`）；
- 轮间间距 `mt-6`，轮内片段 `mt-1.5`（`web/src/components/message-list.tsx:275`）；
- 不改协议数据形状，每条消息仍是一个 `[data-testid="message"]` 节点
  （`web/src/components/message-list.tsx:270`），避免破坏滚动锚点与现有宽度断言。

**加载骨架。** 切换会话时 `openSession` 先清空消息再请求。此时
`selectActiveLoading`（`web/src/lib/store/selectors.ts:80-84`）为 true，
消息流渲染骨架而不是空白（`web/src/components/message-list.tsx:301-321`）；
历史到达后直接替换。骨架与底部 composer 版式共存，不会闪欢迎页居中布局。

**离屏代码块不排高亮。** `CodeBlock` 用 `IntersectionObserver`
（`rootMargin: 300px`，`web/src/components/code-block.tsx:30-39`）
等到接近视口才启动 shiki。历史一次挂载几十条消息时，
离屏块的高亮结果用户看不见，却会占满主线程。

★ **不做的事**：

- 不把多条 assistant 消息合并成一条数据 —— 工具调用边界与 reducer 语义会丢；
- 不做全量虚拟滚动 —— 消息高度不定、Markdown 重排与滚动锚定成本高。

### 6.4 会话标题

标题由服务端在用户消息落盘时推导（`storage/filestore/session.go:777-791` 的 `titleOf`），
前端不跑自己的标题模型 —— 那会多一次计费，
两边算出来不一致时用户看到哪个还得看运气。
会话跑完后 `refreshList()` 重新拉一次列表，侧栏标题随之更新。

## 7. 工具调用：一段连续的调用就是一行

★ **工具调用是步骤，不是消息。** 一次调用就是一步，而一步该占一行 ——
一段界面把每一步都摊开，读者要翻过两屏代码块才够得着他们要的答案。

### 7.1 连续的调用并成一段

★ **连着的调用并成一段**：模型一次要两件事（读这个、再搜那个），
那是**一件事**发生了，界面上也就该是**一行**。

* 归并的边界是**一条消息**（`web/src/lib/tool-groups.ts` 的 `segments`）：
  模型的思考夹在两段调用之间，而思考正是读者要看的 ——
  把思考另一边的那次调用并进来，就等于把它藏起来了。
* 段里只有**一次**调用时，它就是一行，不是组：一个写着「1 steps」的条
  比那一步本身说得少。
* 段在跑的时候会在**尾部**长出来（模型又发了一次调用），所以段的 key 是
  **第一次调用的 id** —— key 变了，节点就被卸载重挂，展开的状态跟着没了。

★ **单张工具卡默认收起**（`web/src/components/tool-card.tsx:112-118`）——
  实时的那张也是收起的：一次工具循环会产出多张卡，每张展开都是两块代码区，答案被埋。
  ★ 上游那份文档说「实时运行中的卡片默认展开」—— 这一条已经改了。

### 7.2 组条说了什么

★ 组条只说三件事：**几步**、**用了哪些工具**、**成了没有**。

* ★ 它**不说**每一步的结果 —— 那些结果的形状是上游的工具实现定的，
  这个仓库看不到；猜出来的摘要会撒谎。⇒ 组条说「跑了什么」，
  结果留给展开后的那一步。★ 「逐工具的结果摘要」是一条**待决**（§17.3）：
  它要上游把结果的形状定下来。
* ★ **失败不折叠**：一段里带着错误，组条就用警告色说「N failed」，
  读者不用展开任何东西就知道出了事。
* ★ 段的阶段是**最活的那一步**：还有一步在跑，这一段就还在跑。
* ★ **失败标志有人写了**：一次调用的失败由 safe-tool 中间件记下，
  随线路的 CUSTOM 帧 `tool_failed` 到达，归约层给**那一次**调用打上 `isError`
  （`web/src/lib/events.ts:531`）。★ 归档的消息上盖着同一件事，
  所以**重新加载不会把失败变成成功**（§17.4）。

### 7.3 结果先于参数

★ 展开一步之后，**结果在参数之前**：参数是模型怎么想的，
结果是世界怎么回的 —— 读者要的是后者。

### 7.4 ★ 这一版要同步到哪里

★ 上游那份 `docs/frontend-design.md` 讲的是同一片界面（工具卡）——
它的内容已并入本文（§0），所以**这一版改动写在这里**。
★ 但上游那份还在，它因此**落后于**本文 —— 上游要改的是它自己的那份。

## 8. 审批（HITL）

### 8.1 一次答完整组

★ **一轮里可以有多个独立待批**（模型一次请求四个都要审批的工具），
每个带自己的 id。答复必须**一次**发出去，不是逐个发。

**为什么不能逐个发**：框架的契约是「不在 `Targets` 里的被中断组件会**重新中断自己**」，
而重新中断拿到的是**新的 id**。逐个发的话，答完第一个，其余的 id 立刻作废 ——
后面每一次都会撞上「interruptId does not match the pending approval」。
协议两端本来就是为「一次答多个」设计的：AG-UI 的 `resume` 是**数组**
（`web/src/lib/store/actions.ts:1235-1239` 一次发一份数组）。

★ **提交的是 `payload.approved`，不是 AG-UI 的 `status`**
（`web/src/lib/api.ts:595-599` 的 `ResumeInput`）。
协议里 resume entry 的 `status` 只有 `resolved` / `cancelled`，
描述的是「这个中断被处理掉了没有」，不表达「用户同不同意」；后端读的是 `approved`。
★ 只发 `status` 的后果：用户的「同意」被当成拒绝，工具永远不执行，**而且不报错**。

★ **「拒绝」要求理由**（`web/src/components/approval-card.tsx:77-96`）——
后端把它存进审批记录，且会回灌给模型（`ApprovalResult.Reason`）。
没有理由的拒绝对模型等于凭空断路。

**UI**：一张卡**取代输入框**，标题写「N tool calls need your approval」
（`web/src/lib/approval.ts:7-9`），下面逐条只列三样东西：

| 列 | testid | 是什么 |
|---|---|---|
| 工具名 | `approval-tool` | 优先取事件的 `metadata.toolName`，历史回放取 `pendingView.toolName`；两者都没有时才从兜底文案里取，仍没有则显示 `Tool call` |
| 请求方 | `approval-requested-by` | 只对子 agent 显示 `Requested by <agent>` |
| 参数摘要 | `approval-arguments` | 单行归一 + 180 字符封顶（`web/src/lib/approval.ts:5`、`:55-62`） |

★ **参数摘要显示时允许两行**（`web/src/components/approval-card.tsx:82` 的 `line-clamp-2`），
全文在 `title` 里。上游那份文档写的是「单行、封顶 180 字符」。

★ **请求方的两个来源归一成同一个字段** `Interrupt.requestedByAgent`：
实时事件的 `subagentRunId`（`web/src/lib/events.ts:412`），
与刷新后 `pendingApprovals[].subagentRunId ?? requestedByAgent`
（`web/src/lib/store/history.ts:168`）。
★ **根 agent 的中断不带这个字段** —— 它就是当前会话本身，标出来只是噪声。

★ **不展示**协议字段 `reason`（恒为 `tool_call`）与 `toolCallId`（一串 uuid）——
它们对用户没有信息量，排查时在网络层看。
整组仍然只有**一个**同意/拒绝按钮（`web/src/components/approval-card.tsx:97-112`），
同意就是全同意。
★ 逐条各给一个按钮会让「只批其中一个」变得可疑：
框架的语义是「不在 Targets 里的会重新中断」，用户会看到审批卡闪一次、id 全变。

★ **刷新后**：`GET /v2/sessions/:id/messages` 的 `pendingApprovals` 是**列表**，
`seedFromHistory` 全部重建 —— 少一个就答不了它，而它会一直挂在界面上，
看起来像「点了没反应」。

### 8.2 中断（停止）

★ **运行中，停止按钮取代发送按钮**，不是并排：同一槽位、同一主色，
图标是实心方块（`web/src/components/composer.tsx:698-699`、
`web/src/components/message-list.tsx:333`）。
并排会多占一个槽位，并让人误以为「发送只是暂时灰掉」；
空心方块则会被读成未选中的复选框。

★ **「停止」必须叫服务端真的停。**

`stop()` 只 abort 本地的 SSE 连接**不等于**中止：服务端那一轮会**继续跑到底** ——
模型继续烧 token、工具继续执行副作用（文件已写、命令已跑），而界面已经显示 idle。
那是「假停止」，比没有停止按钮更糟：用户以为停了。

所以 `stop()` 做三件事，顺序是刻意的
（`web/src/lib/store/actions.ts:1263-1269`）：

| 步骤 | 为什么是这个顺序 |
|---|---|
| ① 断本地流、状态回 `idle`、收尾未关闭的 part | 界面**立刻**可输入。不能等服务端的往返 —— `Abort` 里要 `loop.Wait()`，可能有一小段时间。 |
| ② `POST /v2/sessions/:id/abort` | 这才是真的停（`web/src/lib/api.ts:513`）。 |
| ③ 中止失败要**出声** | 否则界面显示 idle 而那一轮其实还在跑 —— 那正是这个函数要消灭的那种「看起来停了其实没停」。 |

★ **收尾未关闭的 part**（`web/src/lib/events.ts:569-573` 的 `closeAllOpenParts`）：
轮次已经结束，不会再有 part 关闭事件到达 ——
不主动收尾的话，最后那条消息的「Thinking…」永远停不下来。

★ **「停止」与「抢占」是两件事**：

- **停止**（`stop()`）= 终止这一轮，用户接着说下一句。
- **抢占**（在回答途中发新消息）= 旧轮次在安全点让位，新消息接管。

★ **「中止」不是「会话废了」** —— 停完能接着发下一条，这是 E2E 钉着的。

## 9. 附件：清单挂在消息上

★ **附件是消息的属性，不是会话的属性。** 用户发来的文件绑在**它随的那条消息**上 ——
清单存在消息的 `Extra` 里（`message/attachment.go`）。模型看得见它，是因为 transcript 存的那条
消息就是装配输入时用的那个对象。

★ 之所以是「消息的属性」而不是「会话的属性」，是因为**只有消息级绑定才能回看**：
用户问「你刚才那张图里是什么」，模型得知道「刚才那张图」是哪一张。会话级挂载只能回答
「这个会话里有哪些文件」，答不了「刚才那张」。

### 9.1 数据模型：清单在消息的 Extra 里

`message.Extra` 是这套代码存「消息上的元数据」的地方（`message/provenance.go:10` 一组
`openheron_*` 键；压缩标记也是同一类）。附件清单跟着同一个机制走。

| 键 | 值 | 落点 |
|---|---|---|
| `openheron_attachments` | 附件清单（**JSON 字节**） | `message/attachment.go:19` |
| `openheron_message_id` | 消息自己的身份 | `message/provenance.go:26` |

清单项是 `message/attachment.go:30` 的 `Attachment{Name, Path, Size, MIME}`。

★ **`Path` 是会话 workspace 里的相对路径**，不是绝对路径：绝对路径是某一台机器的事实，
写进 transcript 就成了一条只在那台机器上成立的记录。

★ **清单存的是 JSON 字节**（`message/attachment.go:58-67` 的 `StampAttachments`），不是
`[]Attachment`。`Extra` 在写 checkpoint 时走 **gob** 编码，而 gob 编不了 interface 里没注册过的
类型 —— 一份 `[]Attachment` 会让 checkpoint 在 stop 的时候写失败（`message/provenance_gob_test.go:68`
有一张类型白名单的用例），留下一半写坏的审批。字节在白名单里，而 JSON 本来就是 transcript 载它的形式。

读的时候要认三种形态（`message/attachment.go:82-121` 的 `ReadAttachments`）：本包写的 `[]byte`、
JSON 往返之后变成的 **base64 字符串**（JSON 把 `[]byte` 编成字符串），以及调用方可能直接塞进去的
`[]Attachment`。★ **JSON 往返之后的那种才是 transcript 真正载着的形态。**

★ **为什么是 `Extra` 而不是消息的正文**：正文是给模型看的，`Extra` 是给程序看的。模型的输入靠
`message/extract.go:60-65` 的 `UserText` 取文本，`Extra` 不参与；同时 `Extra` 随 transcript 的 JSON
往返，**不用改 transcript 的格式版本**。

清单是**只写一次**的：`message/provenance.go:71` 的 `put` 先写为准，`StampAttachments` 复用它 ——
一条消息在管线里被重新盖章时，保留它出生时的样子。

★ **消息有自己的身份**：`message/identity.go:18-23` 的 `StampMessageID`。清单挂在一条**有稳定 ID** 的
消息上，才谈得上「精确回溯这条消息引用了哪些文件」；压缩在 transcript 中间插入一条消息时，
带 ID 的消息不改号。

★ **两个粒度不要混**：删除的粒度是**文件**，绑定的粒度是**消息**（`api/v2_files.go:488`）。
一个文件可能被多条消息引用，而已发消息的附件是历史的一部分。

### 9.2 后端：契约与解析

`POST /v2/run` 的请求体多一个 `attachments[]`（`api/v2_run.go:76`），每一项是
`runAttachment{Name, Path, Size}`（`api/v2_run.go:91`）。

★ **线上没有 content type**：内容类型由**服务端按文件名判定**（`message/attachment.go:149-182` 的
`MIMEOfExt`，在 `api/v2_attachments.go:64` 落到清单上）。客户端要是把一个文本文件说成
`image/png`，服务端就会去把一段文本当图片读，模型于是对着它没有看见过的图片作答。
判定不了的扩展名一律是 `application/octet-stream` —— 猜错的代价是模型读不到文件，诚实作答的
代价只是「它当文本读」。

★ **`Size` 是客户端的说法，服务端不信**：服务端自己 `stat` 了一遍（`api/v2_attachments.go:46`），
清单里记的是文件自己的大小。留着这个字段是为了让「客户端的说法」与「文件的实情」有一处可以
对照的地方，不是为了用它。

解析在 `api/v2_attachments.go:33-68` 的 `resolveAttachments`：

- ★ **客户端给的是相对路径，服务端拼的是自己的 workspace。** 这条规则让「越权读文件」在结构上
  不可能：客户端能写的字符串里没有绝对路径，服务端只做一次拼接再校验。
- 校验用 `storage/filestore/session.go:715-726` 的 `safeJoin` —— **同一个函数**，不另写一份。
- ★ **越界是拒绝，不是夹取**：夹取会解析到另一个文件，模型就会对着另一个文件作答，而且没有任何
  东西说它读错了。
- 路径还要**存在**且不是目录（`api/v2_attachments.go:46`）：一个不在那里的附件会让模型去找一个
  不存在的文件，然后说「文件不见了」—— 而用户相信他发过。

★ **附件算「一个问题」**：`isAttachRequest`（`api/v2_run.go`）判的是「没有 query、没有
messages、没有 resume」，附件也进了这条判据（`:229`）。否则「只有附件、没有文字」的一轮会被误判
成重连，去找一个不存在的 run。

附件在轮次开始**之前**解析（`api/v2_run.go:304`）：一个越界的名字或一个不存在的文件，要在客户端
还听得懂原因的时候被拒。只有附件没有文字的一轮也是一轮（`api/v2_run.go:311`）。

### 9.3 后端：落盘与装配 —— 让模型看见附件

落盘顺序在 `agentkit/run/loop.go:68` 起：造消息、盖时间戳、盖 ID（`:68`），盖附件清单（`:74`），
**先装配、后落盘**（`:81` 装配，`:102` 落盘）。

★ **为什么是「先装配、后落盘」**：装配时历史里还没有这条消息，所以「追加一次」永远是唯一的一份
—— 模型看到的问题只有一份，transcript 也只有一份。★ 而**不能**简单地把追加删掉：落盘失败时
`sess.Append` 只记日志、不中断（`:96`），历史里就没有这条消息，模型这一轮会完全看不到用户的问题
—— 一个写盘失败，换成了「模型答非所问」。换顺序正好解掉这个洞：装配用的就是那条消息对象本身，
**无论落盘成不成功，模型都看得见它**。

`buildInput` 的签名收的是**消息对象**（`agentkit/run/input.go:95-128`），不是文本。装配函数不需要知道
附件是什么，它只需要把消息原样交给模型。

装配按类型分流（`agentkit/run/attachments.go:233-237` 的 `assembleAttachments`）。

**文本类：给路径。**

- `@` 头由 `agentkit/run/attachments.go:148-169` 的 `attachmentPaths` 生成，一行一个路径。
- ★ **绝对路径是硬要求**：`agentkit/fsx/fsx.go:218` 的 `resolve` 把相对路径按**项目根**解析，而
  `Extra` 里的 `path` 是 **workspace 相对**的 —— 直接把那个相对名字交给 `read_file` 会读到另一个
  文件（甚至项目根下的同名文件）。
- 路径含空白时写成 `@"..."`（`agentkit/run/attachments.go:128-134` 的 `mentionPath`），提示词让模型调用
  时去掉引号；路径统一转成正斜杠 —— Windows 的反斜杠在 JSON 参数里是转义符。
- ★ **消息头是视图，不是改写**：`agentkit/run/attachments.go:203-223` 的 `presentMessage` 返回**一条
  副本**，transcript 里留着用户自己的话。★ 副本与落盘那条**共用同一个 `Extra`** —— id 与清单照旧
  跟着消息走，副本就这些事实而言还是那条消息。
- ★ 消息头**不能**写进落盘那条：界面渲染的是 `UserText(msg.Content)`，把机器路径写进用户自己的
  气泡，界面上就会显示一串绝对路径。
- 历史消息也重建 `@` 头（`agentkit/run/input.go:142-148` 的 `withHistoryAttachmentPaths`）：transcript
  只存相对路径，绝对路径是请求时从 workspace 推导出来的。★ **图片不在这里重新内联** —— 把每一轮
  的图片都重新变成 base64，会把每次请求都撑大。
- 内容**不在消息里**：上下文提示词把 `read_file` 写成义务（`agentkit/run/input.go:60` 的
  `attachmentRules`）—— 一条以 `@` 开头的行是系统注入的路径，不是用户的话；附件内容**不在**消息里，
  回答之前必须自己 `read_file`；大文件用 `offset` / `limit` 分段读。
- ★ **二进制附件不给路径**：`agentkit/run/attachments.go:91-102` 的 `isReadableText` 判它是不是能当文本
  读；不能的写进「暂时不能处理」清单（`:173` 的 `blockedReason`、`:183` 的 `blockedNote`）。对
  非文本字节调用 `read_file` 只会得到噪声，而噪声读起来像内容。

**图片类：多模态 part。**

- `agentkit/run/attachments.go:272-300` 的 `inlineImage`：现读盘、现编码，字节进 `Base64Data`、类型进
  `MIMEType`（`:289`）—— eino 推荐的是这个形状，不是 `data:` URL。
- ★ **base64 不落 transcript**：transcript 是 JSONL，一行上限 8 MiB
  （`storage/filestore/transcript.go:22` 的 `maxLineBytes`），一张 2 MiB 的图 base64 之后是
  2.7 MiB。transcript 里只有「哪个文件」。
- 内联上限 `maxInlineImageBytes` = 5 MiB（`agentkit/run/attachments.go:28`）。

**★ wire 上只有一种形状。**

线上只有一个 `content` 字段，它要么是字符串，要么是列表。openai 适配器在一条消息同时带
`Content` 与多模态 part 时会把两个都填上，客户端拒绝序列化：
`can't use both Content and MultiContent properties simultaneously`。

★ 所以归一化落在 **wire 边界**（`llm/common/shape.go:52-58` 的 `normalizeWireShape`），在 `Generate` /
`Stream` 交给适配器之前过一遍（`llm/common/model.go:88`、`:108`）。★ **为什么不是装配层**：装配只管
得到**这一轮**的消息，而历史是持久数据 —— 早于「正文挪进 part」的那段时间里写下的消息同时带着
正文和图片 part，一进模型就死，而会话不能改写自己的历史，下一句关于它的话会以同样的方式死一次。

- ★ **正文挪进 text part，`Content` 清空**（`llm/common/shape.go:93-121` 的 `oneShape`）—— 不是反过来：
  parts 是更丰富的形状，一张没有正文的图片，模型认不出它讲的是什么事。
- 同一个边界还兜住另一种脏历史：只有思维链、没有正文的 assistant 消息。客户端的 `content` 是
  `omitempty`，那条消息在线上变成 `{"role":"assistant"}`，端点直接拒绝**整个请求**。
  `llm/common/shape.go:74-90` 的 `withVisibleAssistantContent` 在副本里补上 `noVisibleResponseText`
  （`:68`），**如实说明**「上一轮没有产出回答」，而不是编造一段回答。
- ★ **一次请求，没有降级**：`llm/common` 里没有 `files.go` / `vision.go`。文件块那条路已于
  2026-11-10 撤回（见 §17.5）。

**回看。**

- `GET /v2/sessions/{id}/messages` 把清单放在消息的 metadata 里带回来（`api/v2_convert.go:76` 的
  `out.Metadata["attachments"]`）—— 历史里看得出哪个文件跟哪个问题一起走的。
- ★ 一条**只有附件没有正文**的消息不再被丢掉（`api/v2_convert.go:124`）：问题可能整个在文件里，
  而一个没有文字也没有 chip 的气泡看起来像什么都没发。

### 9.4 图片落盘：压缩与哈希命名

`agentkit/imaging` 是一个叶子包：只依赖标准库、`golang.org/x/image` 与
`github.com/rwcarlsen/goexif`，不 import openheron 的任何包。

**压什么、压到多小**（`agentkit/imaging/imaging.go:94` 的 `recode`）：

- 只有 **PNG 与 JPEG** 重编码，格式保持不变。★ 格式不变 ⇒ MIME 判定不变、清单不变、模型的
  `image_url` part 不变；而转 JPEG 会把透明通道变成白底，截图与抠图都毁掉。
- 触发条件是「长边 > 1600px」**或**「体积 > 200 KiB」（`imaging.go:43`、`:23`）。★ 是「或」不是
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

- ★ **命名对所有文件，不只是图片**（`api/v2_files.go:201`）：同名覆盖会让**记录说谎** —— 第一条
  消息的清单里写着 `shot.png`，而那个文件已经是第二次上传的了。文本文件才是**最常**重名的那一类
  （`notes.md`、`README.md`）。★ 覆盖要治本，只治图片等于把最常见的情形留在外面。
- 哈希的输入是**原名 + 大小 + mtime**（`imaging.go:356` 的 `hashOf`），三段之间用 `0x00` 分隔 ——
  名字里不会出现 NUL，所以 `ab`+`12` 与 `a`+`b12` 不会撞。大小与 mtime 都写成定宽大端整数
  （`imaging.go:361`），哈希因此不取决于某个数字「怎么拼」。

★ **哈希里的 mtime，以及那个循环**（细节见 `docs/pitfalls.md` §6.2）：文件的 mtime 是内核在写的
那一刻定的，写之前拿不到；而哈希要写进文件名里。**一次写，不改名** —— `t0` = 收到这次上传的时刻，
哈希由 `(名, 大小, t0)` 算出，写入后用 `os.Chtimes` 把 mtime 钉成 `t0`（`api/v2_files.go:226`）。

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
  条目；删除时同步删掉那一条（`uploads.go:80` 的 `forgetUpload`，由 `storage/filestore/session.go:635`
  调用）。
- 列举端点用它还原 `Name`（`api/v2_files.go:458-500` 的 `listWorkspace`），所以前端零改动。
- ★ 旧会话的文件没压也没改名，但 `MIMEOfExt` 照旧判定 ⇒ **继续能用**。新旧两种命名并存于同一个
  workspace，没有任何代码依赖「文件名长什么样」。

### 9.5 前端：发送、乐观气泡、拖拽粘贴

待发清单按会话存在 store 里（`web/src/lib/store/state.ts:33` 的
`attachments: Record<string, Attachment[]>`），一项是 `{name, path, size, mime}`（`state.ts:8`）。

- 发送时清单从 store 里取出来一起发（`web/src/lib/store/actions.ts:628`、`:716`）。
- ★ **发完要清空**（`:577`）—— 否则下一轮会把同一批文件再发一遍。
- ★ **被拒的一轮要把文件还回待发清单**（`:1522` 的 `restorePending`）：服务端没拿到这个问题，文件
  就还是用户的，不是消息的。
- ★ **乐观气泡里也要画附件**（`:1489` 的 `appendLocalUserMessage`）：附件是这条消息的一部分，用户
  发出去的瞬间看不到自己发了什么，就等于没有发出去。★ 文件在前、文字在后 —— chip 读起来是这个
  问题的附件，问题跟在后面。
- 三件交互都要：

| 交互 | 落点 | 处理 |
|---|---|---|
| 纸夹 | `web/src/components/composer.tsx:565-568` | `<input type="file" multiple>` |
| 拖拽 | `composer.tsx:542` 的 `onDragOver` / `:555` 的 `onDrop` | `e.dataTransfer.files` → `acceptFiles` |
| 粘贴 | `composer.tsx:622` 的 `onPaste` | `e.clipboardData.items` 里 `kind === "file"` 的项 |

- ★ 拖拽悬停必须 `preventDefault()`：默认动作是**拒绝**这次拖放，不允许就不会触发 drop。
- ★ **粘贴截图是这个交互里最有价值的那个**：截图进剪贴板，按 Ctrl+V 就发出去了，没有「先存成
  文件再选」的中间步骤。而 `onPaste` 里也必须 `preventDefault()`（`composer.tsx:632`）—— 默认粘贴
  会把图片的**文件名**塞进消息里，而用户要发的是图。

### 9.6 前端：刷新不丢，历史回看

★ **语义要分清**：workspace 里是「会话的全部文件」，某条消息带的是「这一条的附件」—— 两者不是
一回事。所以 `GET /v2/sessions/{id}/files` **不能**用来还原消息上的清单，它只能还原**待发清单**。

- 恢复靠一条判据：workspace 里**没有任何消息认领过**的文件才是「待发」
  （`web/src/lib/store/actions.ts:1539-1541`）。一条消息认领过的文件是跟那个问题一起发出去的，把它放回
  待发清单，下一个问题就会再发一次 —— 用户只问了一次。
- ★ 清单是**合并**而不是覆盖（`:1364`）：用户刚上传的文件比还在路上的那份列举新。
- 真正的消息级清单在 **transcript 里**，历史水合时跟着消息一起回来：`web/src/lib/store/history.ts:16`
  的 `readAttachments` 从 wire 的 metadata 读，`:90` 装配成 part。★ 文件在前、文字在后 —— 与乐观
  气泡同一顺序，所以一条消息不管刚发出去还是从历史读回来，长得一样。
- 前端的 part 类型：`web/src/lib/types.ts:41` 的 `AttachmentPart`，`:50` 的 `Part` 联合类型加了一个
  成员。
- ★ **联合类型变宽要检查所有按 `kind` 分派的地方**：`attachment` part **没有 `complete` 这个
  字段**，所以 `p.kind === kind && p.complete === false` 这种写法在联合类型变宽之后**编译不过**
  （`kind` 是个变量，TypeScript 没法靠它收窄 `p`）。抽了一个 `isOpenPart` 出来做收窄
  （`web/src/lib/events.ts:188`）。
- 渲染：`web/src/components/message-parts.tsx:110` 的 `case "attachment"`。
- ★ **「移除」的语义**：待发清单里的附件 → 移除 = **真的删掉** workspace 里的文件
  （`actions.ts:595` 调 `api.deleteFile`）；已发消息里的附件 → **不删**，它是历史的一部分，删了
  那条消息就说谎了。前端删失败时要把 chip **放回去**（`:599`）—— 一个把服务端还留着的文件藏起来
  的 chip 是在说谎。
- 服务端：`DELETE /v2/sessions/:id/files?path=`（`api/v2_files.go:516-544`），路径相对 workspace，
  越界的名字被拒（`storage/filestore/session.go:624-637`）。★ **删一个不存在的文件不是错误**：调用方
  要的是「这个文件没了」，而它确实没了。

### 9.7 前端：预览 —— 缩略图与放大

字节从哪来：`GET /v2/sessions/:id/files/raw?path=`（路由 `api/v2_files.go:73`，处理函数 `:311`），
走 `ResolveWorkspaceFile` —— 与删除端点同一套越界规则。★ **这是唯一的边界**：路径不能逃出这个
会话的 workspace。

★ **服务端永远不把文件当内容解释，只当字节转发**，响应只有四种，没有第五种
（`api/v2_files.go:273`）：

| 文件类型 | Content-Type | 处置 |
|---|---|---|
| 图片（png/jpeg/gif/webp） | `image/*` | **inline** —— 前端 `<img>` 直接显示 |
| PDF | `application/pdf` | **inline** —— 浏览器原生渲染 |
| 文本类（`text/*`、json、xml、csv…） | **`text/plain; charset=utf-8`** | **inline** —— 前端自己渲染 |
| 其他一切（zip、docx…） | `application/octet-stream` | **attachment** —— 下载 |

★ **文本类一律按 `text/plain` 返回，是这一节最要紧的一条**（`api/v2_files.go:347`）：一个
`text/html` 或 `image/svg+xml` 的上传文件，如果服务端按它自己的 MIME 内联，浏览器就会**执行**它
里面的脚本 —— 而那脚本跑在 openheron 的源头上。一律 `text/plain` 之后，浏览器只把它当**字**。
★ 它顺带解决了「Markdown 谁来渲染」：服务端返回纯文本，前端用**已有的** `MarkdownText`
（`web/src/components/markdown.tsx:53`）/ `CodeBlock`（`web/src/components/code-block.tsx:6`）渲染，
不需要在服务端引一个 HTML 渲染器，也不需要信任上传的 HTML。

★ **SVG 归到「其他」**（`api/v2_files.go:381-388` 的 `isPreviewImage`）：SVG 是 XML，可以带
`<script>`，而浏览器把它当图片渲染时**会执行脚本**。它走 `attachment`（下载）。

★ `max` 只对图片有意义（`api/v2_files.go:328`）：图片走即时缩放 —— 解码 → 缩放 → 编码 → 返回，
**不落盘**，不产生第二份文件，也就**没有第二个要清理的东西**。文本与 PDF 忽略它。

Content-Type 由服务端按扩展名判定（`message.MIMEOfExt`），**不是**按内容嗅探 —— 嗅探要多读一遍，
而且用户改个扩展名就该按扩展名算。判不出来就是 `application/octet-stream`。

分类放在一个**纯函数**模块 `web/src/lib/file-kind.ts`：`fileKind`（`:201`）、`effectiveMime`
（`:133`）、`isImage`（`:177`）、`fileLang`（`:222`）。★ **mime 优先，扩展名其次**（`:198`）：
服务端判定的 mime 比客户端可信；唯一的例外是 `application/octet-stream` —— 那是服务端的
「我不知道」，不携带信息，于是扩展名说了算。

- chip：`web/src/components/attachment-chip.tsx:63` 的 `AttachmentChip`，缩略图 48px（`:41` 的
  `THUMB`）。★ 图片走**缩略图**而不是回形针：「我发的是不是那张图」是发附件这件事里用户最想确认
  的，而回形针答不了。缩略图在**出站时**缩放（`attachment-chip.tsx:181`），chip 不为 48 像素去拉
  一张两兆的图。
- ★ **文件可能已经不在了**（用户在历史里看到一条附件，而那个文件后来被删了）：`<img>` 会
  `onError`，那时**回退成图标 + 文件名**（`attachment-chip.tsx:184`）—— 一个碎图比一句「文件没了」
  更像出了故障。
- 放大是一个**覆盖层**：`web/src/components/file-preview.tsx:28` 的 `FilePreview`，点背景或按 Esc
  关闭（`:47`）。★ 放大是**纯前端状态**，不写进 store：它是一次「看一眼」，不是一个可以跨刷新
  恢复的东西。
- ★ 覆盖层也承载非图片：点开一个 Markdown 附件出来的是渲染后的内容（`file-preview.tsx:185`），点开
  一个 PDF 是浏览器内嵌的 PDF（`:83`）。同一个覆盖层、同一个 Esc —— **一个交互学到底**，而不是
  「图片一种弹法、文本另一种弹法」。
- 字节的 URL 是一个**字符串**而不是一次 fetch（`web/src/lib/api.ts:370` 的 `fileUrl`）：`<img src>`
  把文件交给浏览器自己的图片管线 —— 它的缓存、它的并发限制、它的解码器 —— 而且那个 URL 可以被复制
  出页面。

### 9.8 图片内联的边界

| # | 问题 | 判据 |
|---|---|---|
| 1 | **哪些 MIME 算图片？** | `image/png`、`image/jpeg`、`image/jpg`、`image/gif`、`image/webp`（`agentkit/run/attachments.go:35` 的 `imageMIMEs`）。★ **不含 `image/svg+xml`** —— SVG 是文本，能 `read_file`，走文本那条路就够 |
| 2 | **多大的图片能内联？** | 5 MiB（`agentkit/run/attachments.go:28` 的 `maxInlineImageBytes`）。★ 32 MiB 的上传上限（`api/v2_files.go:127`）是给**文件**的，不是给**图片**的 |
| 3 | **超限了怎么办？** | ★ **不降级成路径** —— 进「暂时不能处理」清单（`agentkit/run/attachments.go:257`）。给路径会让模型去 `read_file` 一张图的字节，那读出来的是噪声，而噪声读起来像内容 |
| 4 | **模型不支持视觉怎么办？** | ★ **不降级** —— 这一轮带着 provider 的错误结束，由用户重试（`llm/common` 里没有 `vision.go`） |
| 5 | **base64 编码在哪做？** | **服务端**，请求时现读现编。★ 不在上传时编 —— 那会把 base64 存进 workspace，而且上传响应要传一大串 base64 |

★ 第 4 条为什么不再兜底：「provider 拒绝」与「provider 收下但不看」是**两回事**。openheron
**无法从配置声明一个模型有没有视觉** —— `decl.Model`（`agentkit/spec/decl/decl.go`）只有 ID /
Kind / Dimensions / ContextWindow / SummarizeAt / Timeout / Reasoning* / Extra，**没有 vision
字段**。所以「这个模型支不支持图片」目前只能是 provider 的事实，不是 openheron 的配置；兜底只能
覆盖「请求被拒」这一种，而「收下但不看」返回 200、不报错，兜底路径不会触发，模型会自己说它看不见
—— 用户至少不会以为模型看过。

## 10. 动效

### 10.1 三条铁律

**默认态即最终态。** 动效只是「到达」的过程，信息在动画结束后必须完整可读、可读屏。去掉全部
动效，界面上不能少任何一条信息、不能少任何一个可点的东西。★ 这条决定了后面所有取舍：凡是
「靠动效才能看懂」的提案，一律不做。

**★ 新增动效不改变元素的最终布局。** 只动 `transform` / `opacity` / `filter` 这类**合成器属性**，
不动 `height` / `margin` / `top` / `width`。

★ **为什么**：消息流靠 `ResizeObserver` 按**内容高度**跟随滚动（`web/src/components/message-list.tsx:122`），
动布局会跟这套跟随机制抢方向盘 —— `web/test/e2e/scroll.spec.ts` 的十条用例正盯着这件事（溢出时
停在视口内 / 用户上翻后不被拽回 / 流式未结束也跟随 / 高亮撑高后仍跟随 …）。

★ **唯一的例外**：折叠面板的展开/收起。它本来就要改布局，动效只是让这次**已经要发生**的布局变化
变平滑 —— 不存在「新增的布局抖动」。

**尊重 `prefers-reduced-motion`。** 一条全局规则（`web/src/index.css:230`），把所有动画降到
「立刻完成」。

★ `0.01ms` 而不是 `0s`：有 `animation-fill-mode: both` 的入场动画在 `0s` 下会被**跳过**（某些浏览器
在 `0s` + `fill-mode: both` 的组合下会跳过关键帧，元素停在起始态）。`0.01ms` 保证终态一定被画
出来。

★ 因为是全局规则，它连 `tw-animate-css` 自带的那几处也一起降级了 —— 不只是本文写的那几处。

### 10.2 动效令牌：一份，不是六处

`web/src/index.css:121` 起的一组，和 `--chat-column` 同一个思路 —— **改一次，不是改六处**：

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

### 10.3 品牌标记：内联 SVG 与两个 mask

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
（`web/src/index.css:369`）。走线与数据点本来就在 mask 之外，不受这个限制：走线走
`stroke-dashoffset`，数据点走 `opacity` + `scale`。

动效只做一件事 —— **电路接通、数据点亮起**，全程一次性，不循环（`web/src/index.css:340` 起的三个
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
（`web/src/index.css:382`）—— SVG 的 `transform-origin` 默认取的是**视口**的中心，不是图元的中心。

**用在哪儿**：只在欢迎页 hero（`web/src/components/message-list.tsx:217`，`size-16` 64px，**播一次
入场**）；页头（`web/src/app/shell.tsx:167`，`size-5` 20px，**不播**）。

★ 页头为什么不播：20px 下四个部件各只有几个像素，细节全糊；而页头是**常驻**的 —— 一个一直动的
东西在常驻位置就是噪声，不是信息。

**无障碍**：内联 SVG 带 `role="img"` + `<title>openheron</title>`
（`web/src/components/brand-mark.generated.tsx:29`、`:40`）；页头那处是装饰件（旁边的文字已经写了
openheron），用 `aria-hidden="true"` 而不是塞一个空的 `alt`。

### 10.4 界面功能性动效（未实施的提案）

★ **本节全部未实施。** 原则：**动效 = 状态变化的回声** —— 每一条都要能回答「界面刚才变了什么」。

| 落点 | 现在 | 提案 | 属性 | 依据 |
|---|---|---|---|---|
| 消息入场 | 无 | 淡入 + 上移 4px | `opacity` / `transform` | 新元素出现；第二条铁律合规（不改布局） |
| 审批卡出现 | 无 | 从下 8px 淡入 | `opacity` / `transform` | ★ 它**取代 composer**，出现得晚、位置显眼 |
| 状态点切换 | 只有 `animate-pulse`（`web/src/app/shell.tsx:29`） | 保留 pulse，另加**颜色过渡** | `background-color` | 状态点的语义由上游文档定义 |
| 「Working…」 | `animate-pulse` 小点（`web/src/components/message-list.tsx:237`） | 保留 | — | 已有 |
| 工具卡展开 | 箭头 `rotate-90`（`web/src/components/tool-card.tsx:135`） | 箭头保留；**内容**用 `grid-template-rows: 0fr → 1fr` 做高度过渡 | `height`（第二条铁律的例外） | 内容本来就要变高 |
| 折叠箭头 | 已有 `transition-transform`（三处各写一遍） | 补上 `--motion-duration-fast` | `transform` | 三个地方各写一遍 = 会漂 |
| 对话框 | `data-open` / `data-closed` 已有（`web/src/components/ui/dialog.tsx:43`、`:64`） | 保留 | — | 现成的模式，新动效照它写 |

★ **审批卡那条是本节唯一「有存在感」的动效**：审批发生时轮次已经结束、输入框被取代，用户此刻最
需要知道「轮子停在我这儿了」。

★ **流式光标（新增，但只能做成 CSS 伪元素）**：现在流式输出**没有光标**，文本一段段蹦出来。

★ 实现约束是硬的：消息流**每帧重渲染**（`web/src/lib/api.ts:673` 的 `FRAME_MS` = 33ms 批处理，
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
（它诚实地表示「状态还挂着」），以及 §10.4 里那个尚未实施的流式光标。
★ **不写 `will-change`**：现代浏览器对 `transform` / `opacity` 的动画会自动提升合成层，常驻的
`will-change` 反而占着合成层不放。

## 11. 授权选择器：输入框下面那一行

★ 会话的**授权模式**（默认 / 受信路径 / 完全授权）是**会话的属性**，
完整的设计在 `docs/authorization-design.md`；这里只讲**界面这一侧**。

### 11.1 位置与形状

位置：**输入框下面那一行的右端**，与「项目」按钮同一行
（`web/src/components/composer.tsx:713-754` 的 `<ComposerMeta … action={…} />`）。

★ **顺序固定，位置就是身份。** React 按**下标**对齐子节点，
一个下标变了就是「卸载了旧的、挂载了新的」，而挂在里面的状态全没了
—— 挂在里面的正是那几个对话框。⇒ 授权选择器、项目按钮与操作时间线的入口**一起**
放进 `ComposerMeta` 的那**一个** `action` 槽，顺序永远是「授权 → 项目 → 活动」，
多一个兄弟节点也不许换位。

★ 三个入口被包进**同一个** `span`，那个 `span` 带着右对齐（`ms-auto`）——
项目按钮自己那份 `ms-auto` 被去掉了，几份都靠自动边距会各推各的。
★ 列窄时入口**只留图标**（§5.6.5：列 < 768px），并且**和事实同一行**（§5.6.4）：
档位名让位，盾牌留着；让位的档位名在 `title` 与 `aria-label` 里都还在。
★ 那一行放不下的时候，这个 `span` 自己**换行**（`flex-wrap`）——
事实的名字不许被挤成半个（§5.6.2），入口让位。

★ **按钮在欢迎页上也不禁用**（§11.5）：那里还没有会话，而档位是**暂存**的 ——
一个点不开的选择，就是一个欢迎页表达不出来的意图。

### 11.2 三档的文案

★ **界面文案是英文**（AGENTS.md 的语言规则：interface copy → English）：

| 档位 | 文案 | 一句话说明（title） |
|---|---|---|
| 默认 | `Default approval` | Tools ask before they run, as the configuration says |
| 中间 | `Trusted paths` | Writes inside the project, the session workspace and the configuration do not ask |
| 完全 | `Full authorization` | Every tool runs without asking |

★ 这三段文案住在**一处**：`web/src/lib/authorization.ts` 的 `authorizationOptions`
—— 选项的顺序（从「问得最多」到「问得最少」）也是定死的，
一个每次渲染都换顺序的列表，用户就得每次重读一遍。

★ **完全授权用警告色**（`--warning` 令牌，不是字面色值），中间档用中性色
（`web/src/components/authorization-picker.tsx:161-169`）⇒ 用户一眼能看出
「这一档不再有人看着」。

### 11.3 服务端是权威，界面不乐观更新

* 选中即写：`PATCH /v2/sessions/{id}/authorization`；
  ★ 界面**不自己乐观更新** —— store 的动作把**服务端回来的 meta** 合进会话条目，
  失败时**再抛出去**，界面停在旧值上。
* ★ **会话在跑的时候也能改**，但要写明「下一轮生效」——
  已经在跑的那一轮用的是**它开始时的那一档**。
  ★ 不能让它看起来已经生效了。
* ★ 落法：这句话写在**弹窗的描述**里
  （`web/src/components/authorization-picker.tsx:188-192`），
  **不**在按钮旁边挂一个「下一轮生效」的小牌子 ——
  这条规则**一直**成立，不是有条件才显示的；
  而一个会来来去去的小牌子会让按钮那一行文字跳。
  ⇒ 按钮上只有档位名（`web/src/components/authorization-picker.tsx:155-169`）。

### 11.4 切到完全授权：一次确认框

流程：**先不带 `confirm` 发** ⇒ 服务端回 **409 `confirmation_required`**
⇒ 界面弹确认框 ⇒ 用户确认 ⇒ **带 `confirm: true` 重发**。

* ★ **确认框是服务端的 409 触发的**，不是界面自己判断的 ——
  服务端记着「本会话已确认过」，第二个界面因此**不会**多弹一次
  （服务端在 `GET /v2/sessions/{id}` 里也带着「已确认」这个事实）。
* ★ **欢迎页上没有会话可问**，所以那一次确认由**客户端**发起 ——
  而服务端在会话诞生时**照样要求那个标志**（§11.5）。
* 确认框里要写清**这一档意味着什么**，不是一句「确定吗」：
  `Full authorization — every tool runs without asking, including shell commands and configuration changes.`
  ★ 理由：这一档把人从回路里整个拿掉了，而「包括改配置」是最重的那一条。
* ★ **Escape 必须能关掉它** —— 而 Radix 在 document 上用捕获阶段听 Escape，
  组件里的 `onKeyDown` 已经太晚 ⇒ 唯一的出口是 **`onEscapeKeyDown`** 那个 prop
  （AGENTS.md 的「平台拥有的键」那条）。

### 11.5 欢迎页上：还没有会话，所以档位是**暂存**的

★ 欢迎页上**还没有会话**，而档位是**会话的属性** —— 所以那里的选择是**暂存**的。

* ★ **暂存住在 store 里**：`pendingAuthorizationMode` 与
  `pendingAuthorizationConfirmed`（`web/src/lib/store/state.ts`），
  ★ 形状与 `pendingAgentId` **一模一样** —— 欢迎页上的选择跟着**建会话的请求**
  一起走（`POST /v2/sessions` 的 `authorizationMode` /
  `authorizationConfirmed`）。
* ★ **暂存的是选择，不是事实**：服务端仍然是权威 ——
  它在建会话时检查那一档，没确认的完全授权仍然是 **409**，
  写失败就把刚建的会话**删掉** ⇒ 「要么生来就是这个档，要么这个会话不存在」
  （`docs/authorization-design.md` §4）。
* ★ **「已确认过」的人不再问第二次**：暂存里的那个确认标志跟着走，
  与会话上「服务端记着已确认」是同一件事。
* ★ **弹窗的描述在两处不一样**：有会话时说的是「下一轮生效」；
  欢迎页上没有正在跑的一轮，说的是这个会话**生来**就在这一档里。
* ★ **换会话时暂存就作废**：会话一建出来，`promote` 就把暂存清掉
  （`web/src/lib/store/events.ts:25-49`）——
  留着它会把**下一个**会话放进这一段会话的档位里。

## 12. 操作时间线：输入框下面那一行的第二个弹窗

★ 会话的**操作时间线**（这个会话上发生过什么，按顺序）是**服务端的那份记录**，
完整的设计在 `docs/session-operations-design.md`；这里只讲**界面这一侧**。

### 12.1 位置与形状

位置：**输入框下面那一行的右端**，接在授权选择器与项目按钮**之后**
（`web/src/components/composer.tsx` 的 `<ComposerMeta … action={…} />`）。

★ **顺序固定，位置就是身份。** React 按**下标**对齐子节点，
一个下标变了就是「卸载了旧的、挂载了新的」，而挂在里面的状态全没了
—— 挂在里面的正是那两个对话框。⇒ 三个入口**一起**放进 `ComposerMeta` 的那
**一个** `action` 槽，顺序永远是「授权 → 项目 → 活动」，
多一个兄弟节点也不许换位。

★ **按钮与它的对话框包在同一个 wrapper 里**
（`web/src/components/operations-panel.tsx`）—— 条件渲染的东西要放在 wrapper **内部**，
放在外面就会挪动这个 entry 的下标。

★ **入口是一个普通按钮，不是下拉菜单**：这棵树里唯一的浮层基建是 `Dialog`，
而这个面板**只有一个入口** —— 一个菜单项不值得一整套 DropdownMenu。

★ 列窄时入口**只留图标**（§5.6.5：列 < 768px，§5.6.4）：`Activity` 这个词让位，时钟留着。
★ 而且它**和事实同一行** —— 那一行是键盘上面最大的一块。

### 12.2 面板

Radix 的 `Dialog`，壳是 `sm:max-w-2xl h-[85dvh] flex flex-col gap-3 p-4`
（`web/src/components/operations-panel.tsx:99`）。★ 它**不**照设置面板 —— 那个铺满整屏
（§5.9.5），这个是一张卡片。
一个可滚动列表（`min-h-0 flex-1 overflow-y-auto`），每条一行：
**时间** + **一句话** + **标识**（run id / interrupt id / 路径 / agent / 档位 / 大小）。

★ **列表是升序**（最早在上）—— 时间线是从上往下读的。
★ **时间是自己写的**（`formatAt`）：浏览器的本地格式跟着 locale 走，
而一个列会随 locale 挪动的日志不是日志。
★ **Esc 走 `onEscapeKeyDown`**：Radix 在 document 的**捕获阶段**监听 Escape，
组件里的 `onKeyDown` 已经太晚（仓库铁律）。
★ **颜色用令牌**：`text-warning`（审批在等）、`text-success`（审批被批准）。

★ **不认识的 `kind` 显示它自己的名字**，而不是消失：客户端不发明一种 kind，
但也不把服务端新加的一种藏起来。

### 12.3 数据与实时

- `web/src/lib/api.ts` —— `listOperations(id, opts)`
- store —— `SessionEntry.operations` + `loadOperations(id)`
- selector —— `selectOperations(s, id)`，模块级 `EMPTY_OPERATIONS`
  ★ **永远不要 `?? []`**：每次新建数组都会让 `Object.is` 报「变了」→ 白屏。

★ **实时靠已有的公告通道**：面板打开时读一次，之后那三个事实
（`run_started` / `approval_pending` / `approval_resolved`）到达时重读一次。
★ **不给日志加新的通道** —— 通道说「有事发生」，日志说明「发生了什么」。

## 13. 验收

**这是整个前端最重要的一节。** 上一轮协作中，10 个脚本级冒烟测试全绿而界面是坏的 ——
因为脚本结构上就看不见渲染层。

### 13.1 三层测试

| 层 | 命令 | 覆盖 |
|---|---|---|
| 纯函数 | `bun run test:unit` | reducer 每个事件分支、分帧边角、store 行为 —— **484 条 / 38 个文件** |
| 界面 | `task test-e2e` | 真实浏览器 + 确定性假后端 —— **311 条 / 24 个 spec** |
| 协议 | `task smoke` | 真后端的 SSE 兼容性 |

★ **CI 跑仓库自己的命令**：`task lint-web` + `bun run test:unit`
（`.github/workflows/ci.yml:80-83`）、`task test-e2e`（`:147`）。
★ **冒烟不在 CI**（`:12`）—— 它们需要一个会答话的模型，而那在 runner 上不可复现。

### 13.2 E2E 为什么用假后端

假后端（`web/test/fake-server.ts:306` 的 `SCENARIOS`，**32 个场景**）按脚本回放
AG-UI 事件序列，**不打真模型**。原因：界面断言必须确定。
曾经的 `smoke:hitl` 有约 1/5 抖动 —— 取决于模型这一次会不会调 `write_file`。
用它做界面断言，测试失败时分不清是界面坏了还是模型没听话，
于是测试会被「重跑一次」，慢慢变成没人相信的东西。

代价是**牺牲了与真实模型的端到端联调**，由 `smoke:interop` 与 `smoke:hitl` 补上。

★ **端口一定要错开，不能「复用开发实例」**：本机开着 vite dev server 时，
E2E 会「全绿」但打的是真后端 —— `OPENHERON_API_TARGET` 根本没注入。
⇒ 两条 webServer 都是 `reuseExistingServer: false`
（`web/playwright.config.ts:49-65`），Vite 那边 `strictPort: true`
（`web/vite.config.ts:40`）。**让这种情况结构上不可能**，而不是靠人记得。

### 13.3 E2E 覆盖什么

`web/test/e2e/` 下 **24 个 spec 文件，共 311 条**（实测）：

| spec 文件 | 条数 | 钉的是什么 |
|---|---|---|
| `chat.spec.ts` | 74 | 会话列表 8 / 会话标题 2 / 发送与流式渲染 3 / 审批（HITL）3 / 多个待批 2 / 中断（停止）7 / 协议：一次往返 2 / 附件 6 / markdown 与高亮 10 / 空状态版式 6 / 消息卡片宽度 5 / **欢迎页宽度 3** / 工具卡 14（语法高亮 4 / 宽度 2 / 图标 4 / 标题 4）/ **连续的调用并成一段 3** |
| `settings.spec.ts` | 44 | 配置编辑器：opening 10 / 配置根 3 / 无档位切换 1 / 只读分区 2 / 编辑与保存 4 / 别的文件的问题 2 / 不在树里的 agent 2 / 文件列表是一棵树 6 / skill 可编辑 3 / 新建文件对话框 9 / 关闭 1 / **弹窗铺满全屏 1** |
| `projects.spec.ts` | 30 | 项目分组、会话只出现一次、侧栏没有项目选择器、树的读屏结构、折叠与记忆、当前项目、新建与改名与改目录与移除 |
| `agents.spec.ts` | 17 | `/skill` 与 `/agents` 的选择器与直接切换、焦点 4 条、未知名字不发消息、没选过 agent 时用服务端的默认根、**点底栏的 agent 名字开同一个选择器 + 点击不清空输入框** |
| `attachments.spec.ts` | 14 | 附件随消息走、拖拽与粘贴、移除、预览 |
| `composer-autocomplete.spec.ts` | 13 | 10 行封顶 / 自动聚焦 / @ 文件提示 / 命令 Tab 补全与 Enter 执行 / 键盘滚动跟随 / 切换后聚焦 |
| `responsive.spec.ts` | 19 | 三档视口（390 / 768 / 1440）：不横向滚动 / 聊天列不超 token / 输入框与发送键在窗口里 / 触摸目标够大 / 行自己的菜单键看得见 / 侧栏是抽屉且能开能关 / 弹层不超出视口 / 断点边上的侧栏仍然常驻 / **树再长也挤不掉输入框（手机）、树再长聊天列也不缩水（平板）、长对话不会把输入框顶出屏幕（手机 + 桌面）** / **宽屏上助手正文吃满整列，与用户气泡共用右边缘** / **设置入口不贴着侧栏的边** |
| `composer-meta.spec.ts` | 17 | 默认 agent / 模型 / 思考档位 / 上下文占用 / 接口不可用 / 设置关闭后刷新 / **窄屏 8 条 + 宽窗窄列 1 条** |
| `scroll.spec.ts` | 10 | 滚动跟随 —— 溢出时停在视口内、用户上翻后不被拽回、流式未结束时也跟随、高亮撑高后仍跟随、打开停在最后一条、切会话停在底部、刷新停在最后一条 |
| `secrets.spec.ts` | 9 | Secrets 标签页：两个命名空间、写入与删除、来源与覆盖、删除的确认 |
| `mcps.spec.ts` | 7 | `/mcps` 面板：两种名字与档位、够不着的服务器、正在启动与起不来、每次打开都重读、Refresh |
| `multi-agent.spec.ts` | 6 | 多 agent 的过滤条：按 agent 切消息、用量分账 chip、角色标签显示 agent 名、实时流里两个 agent 交错、单 agent 不出现过滤条 |
| `session-busy.spec.ts` | 5 | 第二个界面挂上同一轮、关掉第一个继续接收、错过一轮开始的跟上去、运行中写的消息留在输入框、挂上以后仍能停止 |
| `theme.spec.ts` | 5 | 明暗切换、刷新后还记得、没存过偏好时跟随系统、首屏之前就定了主题 |
| `compaction.spec.ts` | 5 | 压缩画成分隔线、被取代的消息仍然读得到、轮内压缩当场画出边界、`/compact` 手动压缩、压缩期间的提示 |
| `authorization.spec.ts` | 7 | 三档、**「下一轮生效」写在弹窗的描述里**、刷新后还在、切到完全授权要确认、Escape 能关掉确认框 |
| `approval-recovery.spec.ts` | 5 | 审批卡不靠刷新：下一轮的审批卡自己弹出来、审批卡还在时不会自己消失、挂上被拒的轮次 |
| `history.spec.ts` | 4 | 轮次标签与用户换行、历史工具卡默认折叠、加载骨架、回到底部 |
| `operations.spec.ts` | 4 | 会话的操作时间线：条目、每条说得清、空态、Escape 能关掉 |
| `motion.spec.ts` | 4 | 品牌标记是内联 SVG、入场后不透明、页头那一份不播、减少动态效果时压到 0.01ms |
| `tool-failure.spec.ts` | 4 | 失败的那次调用写着 failed / 展开后内容照旧 / 历史里仍然是失败 / 成功的那次没有被说成失败 |
| `reattach.spec.ts` | 2 | 连接断了以后重新挂上、重新挂上时只带 run id |
| `session-events.spec.ts` | 2 | 审批在别处被回答后另一个标签页跟上、审批在等的时候通道把那张卡送过来 |
| `i18n.spec.ts` | 4 | 切一次语言：偏好记住了、`lang` 跟着变 / 切到中文：界面文案真的变成中文 / 切回英文 / **语言开关在页头的右端，和主题开关并排** |

★ 条数写死在这张表里，是为了让「加了用例忘了改文档」变成一次 diff 冲突。
★ 上游那份文档的同一张表写的是 **8 个 spec、122 条** —— 那是很久以前，
功能面扩展之后没有回头改它。

### 13.4 定位点约定

语义定位（role / aria / 可见文本），不用 CSS 类名。类名会被 Tailwind 改、
会被 shadcn 重新生成；语义标签是产品契约。

需要稳定的钩子时加 `data-testid`，不用 `data-slot="aui_*"` 那种框架命名的属性。

★ 实测 23 个 spec 里的选择器：`getByText` 67 次、`data-testid` 293 次、
`getByRole` 29 次、`getByLabel` 2 次；**`data-slot` 0 次**；
类名选择器只有 3 处，全在 `web/test/e2e/motion.spec.ts:40`、`:48`、`:58` ——
那是手写 SVG 的 class，不是 Tailwind 工具类（`motion.spec.ts:38-39` 有注释说明）。
常用常量在 `web/test/e2e/fixtures.ts:67-85`，点菜单走 `getByRole`（`:87-111`）。

## 14. 工程化

### 14.1 前端由谁提供

| 模式 | 前端由谁提供 | 生效条件 |
|---|---|---|
| 开发 | Vite dev server（:5173），`/v2` 反代到 Go（:8080） | `bun run dev` |
| 测试 | Vite dev server（:15173），反代到测试实例（:18099） | `bun run dev -- --mode test` |
| 生产 | Go 二进制通过 `//go:embed all:web/dist` 内嵌 | `bun run build` 后 |

**`go:embed` 的模式不能含 `..`，且相对本包目录解析**，所以 embed 语句必须在
仓库根的 `main.go`（`:42-43`），不能放在 `webui/` 里 ——
从那里写 `web/dist` 指的是 `webui/web/dist`，写 `../web/dist` 会被模式校验拒。

★ 早先那包在 `internal/webui/` 时的注释把原因写成「在 `internal/` 下」。
**那是错的**：`internal` 与 `go:embed` 毫无关系，
平移成 `webui/` 之后约束一字未变，变的只是包深度。
判断这类约束先问「是 Go 的哪条规则」，别拿包所在目录当解释。

### 14.2 测试环境：另一套配置与端口

测试**不复用**开发环境。全部约定写在仓库根的 `.env.test`（`:25-39`），
**四处共读同一份**：`web/vite.config.ts:12-13`（`loadEnv`）、
`web/playwright.config.ts:7-24`（`envFile`）、`web/scripts/test-env.ts:6-21`、
`scripts/test-backend.sh:17-20`。

★ **开发的两个端口不在 `.env.test` 里** —— 它们是 `web/vite.config.ts:17-18` 的默认值；
`.env.test` 只管测试那一侧。

| | 开发 | 测试 |
|---|---|---|
| openheron 本体 | :8080 | **:18099**（`OPENHERON_TEST_BACKEND_PORT`） |
| Vite | :5173 | **:15173**（`OPENHERON_TEST_FRONTEND_PORT`） |
| E2E 假后端 | — | **:18180**（`OPENHERON_TEST_FAKE_PORT`） |

命令（`Taskfile.yml`）：

```
task lint-web           # :541-546
task test-env-backend   # :575-579  起测试实例（独立配置 + 独立会话），前台
task test-env-reset     # :581-584  只重建测试目录
task test-env-sync      # :586-589  把开发侧的 agent 定义同步进测试环境
task smoke              # :603-609  冒烟脚本
task test-e2e           # :622-626  浏览器 E2E（自带假后端）
```

★ **端口错开只是第一层，状态必须一起隔离。**
openheron 的会话写在 `<openheron-root>/state/`，
测试若写进去就污染了真实会话历史。
所以 `scripts/test-backend.sh:46-48`、`:132-136` 点名三个根
（project / configs / state），并导出 `OPENHERON_ROOT` / `OPENHERON_CONFIG_ROOT` /
`OPENHERON_DATA_DIR` / `OPENHERON_DEFAULT_PROJECT_ROOT` / `OPENHERON_LISTEN_ADDR`。

★ **隔离靠被点名的根，不靠一个探针。** 配置根**不向上搜索**
（`agentkit/spec/config.go:628-637`：「resolved in exactly three steps」，
且「There is deliberately no upward search for an ancestor's `.openheron/`」），
所以仓库根那份配置对测试实例是**结构性不可达**的 ——
有一条测试专门钉住这件事（`agentkit/spec/init_write_test.go:12` 的
`TestNewServiceDoesNotProbeUpwards`）。
★ 上游那份文档靠「一个恰好停在这里的探针」（`probeUpwards`）来隔离，
本仓库不靠它 —— 端口隔离与数据隔离不依赖「一个恰好停在这里的东西」。

`web/dist` 被 `.gitignore` 忽略，但 `web/dist/.gitkeep` 必须**被 git 跟踪** ——
目标目录命中不到任何文件时 `go:embed` 会**编译期**失败，新 clone 连只改 Go 代码都 build 不出来。
★ 两处都要有例外：`.gitignore:22-24` 的 `!/web/dist/` + `!/web/dist/.gitkeep`，
以及 `web/.gitignore:34-36` 的同一对 —— git 逐层判断，
父目录被排除就不再下降到子文件，只有一处例外是**无效**的。
★ `web/.gitignore:11` **确实**有一行 `dist` 排除整个目录；
救回它的是**同一个文件**里后匹配者胜出的 `:34-36`。
★ `build` 脚本末尾重建它（`web/package.json:8`），
因为 vite 的 `emptyOutDir` 会删掉它。

### 14.3 SPA 回落必须挡 API 前缀

`webui/webui.go:21` 的 `apiPrefixes` 列出 `/v2` `/sessions` `/agents` `/config`
`/health` `/runtime`，`:24-31` 的 `looksLikeAPI` 判它，命中时回 **404 + JSON**
（`:46-51`），不回 index.html。

★ 否则 `GET /v2/sessions/unknown` 的 404 会被回落成 `index.html`，
返回 200 + HTML，客户端拿到一个解析不了的「会话列表」。
正反两组用例在 `webui/webui_test.go:8-22`。

### 14.4 代理目标可覆盖

`web/vite.config.ts:16-17` 读 `OPENHERON_API_TARGET`：

```ts
const apiTarget =
  env.OPENHERON_API_TARGET ?? (isTest ? "http://127.0.0.1:18099" : "http://127.0.0.1:8080")
```

★ 代理**只代理 `/v2`**（`web/vite.config.ts:41-43`）——
静态资源不走代理，所以开发时改前端不需要重启后端。
E2E 启动时注入假后端的地址（`web/playwright.config.ts:45-50`），产品代码零改动。

### 14.5 类型检查分三个 project

| project | 范围 | 解析模式 |
|---|---|---|
| `tsconfig.app.json` | `src` | bundler（`@/` 别名，`:22-24`） |
| `tsconfig.node.json` | `vite.config.ts` | nodenext（`:10`） |
| `tsconfig.test.json` | `playwright.config.ts`、`test/**`、`src/lib/fixtures` | bundler + **同样的 `paths`**（`:22`） |

`web/tsconfig.json:3-7` 的 `references` 指着这三个。
★ `tsconfig.test.json` 必须也声明 `@/*` 映射：被测的 `src/**` 源码内部用
`@/` 导入，没有这个映射会报 `Cannot find module '@/lib/api'`，
而且报错指向测试文件，很容易误判成测试的问题。

★ **`bun run test:unit` 限定 `test/*.test.ts`**（`web/package.json:17`）——
`test/` 下还放着 Playwright 的 `.spec.ts`，被 bun 当成自己的会报
`Playwright Test did not expect test.beforeEach()`。

## 15. 已知限制

- ★ **审批卡只标请求方，不做分组**。`Interrupt` 会显示 `Requested by <agent>`，
  但多个 subagent 同时要审批时仍是一条一条列
  （`web/src/components/approval-card.tsx:53-93` 的 `interrupts.map()`），
  没有按 agent 折叠。真正的分组要等同时挂起多个子 agent 的场景出现再设计。
- ★ **参数摘要是不可展开的**。超过 180 字符截断（`web/src/lib/approval.ts:5`、`:23-62`）；
  ★ 而 `title` **不再**给出全量 —— 它挂的就是那份**已被截断**的字符串
  （`web/src/components/approval-card.tsx:83`）。
  要看全量参数目前得看工具卡片或网络事件。
- ★ **没有 Inspector 第三栏**。原设计里有，未实现。
- ★ **附件的语义是「提前把文件放进 workspace」**，不是随消息发送。
  文件不进消息体，模型靠工具读那个目录（见 §9）。
  compose 阶段移除 chip 只是「这一轮不发它」，服务端上的文件不删 ——
  附件已落盘，与会话历史一样不可逆。
- ★ **选文件会建会话**。没有会话时上传会先 `POST /v2/sessions` 换一个
  （`web/src/lib/store/actions.ts:560`）。选了文件又放弃会留下一个只含文件的会话 ——
  它不是垃圾，但确实是残留。「打开页面 / 点 New thread 零请求」不变，且有专门的 E2E 盯着。
- ★ **设置弹窗那一块 653 KB**（§1.3）—— 上游那轮就提过要换掉 CodeMirror 的 React 包装层，
  这件事没做。

## 16. 决策记录

| 决策 | 理由 |
|---|---|
| 不用 assistant-ui | ★ 它的语义只能靠翻源码确认（上游那轮移除它时的八处 bug 全属这一类） |
| 不用 CopilotKit | 前端要连它的 runtime 端点，而那是 Node 进程 —— 与单二进制 `go:embed` 冲突 |
| 不用 Vercel AI SDK | 生态里没有 AG-UI × AI SDK 的适配器；要自己写转换层，而自定义后端协议、审批中断、resume 全在它的文档快乐路径之外 |
| 不用 Next.js 等 SSR 框架 | 与 `go:embed` 静态分发冲突 |
| 自己写 SSE 分帧 | §3.1 |
| 本地声明事件类型 | §3.2 |
| E2E 用假后端 | §13.2 |
| 界面文案全英文 | 与 AGENTS.md 及后端错误文案一致 |
| 连续的调用并成一段 | §7 —— 三次调用是「一件事」，三张卡是三个盒子 |
| 授权模式是会话的属性 | 见 `docs/authorization-design.md` |

★ 前四行是上游那轮定的，本仓库沿用 —— 它们约束的是**分发形态**（单二进制），
而分发形态没变。

## 17. 待决

### 17.1 ★ 二进制附件还没有处理办法。
PDF、压缩包、docx 走不进「可当文本读」的判据
（`agentkit/run/attachments.go:91-102` 的 `isReadableText`），于是它们**拿不到 `@` 路径**，只在提示词
里被列为「暂时不能处理」（`:183` 的 `blockedNote`）。★ 为什么现在这样：对非文本字节调用
`read_file` 只会得到噪声，而噪声读起来像内容 —— 一个模型对着噪声作答，比一个明说「这个文件我
读不了」的模型更危险。**出处**：`agentkit/run/attachments.go` 的 `isReadableText` 与
`blockedNote`。

### 17.2 ★ §13.3 的表把 spec 文件数与条数写死在表里。
这一条以前是「记一笔交给上游」——
上游那份 `docs/frontend-design.md` 有同一张表，而它的内容已并入本文（§0），
所以这张表现在是**本仓库自己的**。
★ 本仓库当前的数：spec 文件 **24** 个、**311** 条；
`web/test/e2e/chat.spec.ts` **74** 条。
★ 加用例时要改这张表 —— 它的作用是让「加了用例忘了改文档」变成一次 diff 冲突。

### 17.3 ★ 逐工具的结果摘要还没有 —— 这一轮决定交给上游。
组条今天说的是「跑了什么」（几步、哪些工具），
不是「得到了什么」（§7.2）。★ 为什么现在这样：每个工具的**结果长什么样**是上游的工具实现
定的（`read_file` 的结果是一段文件内容，`grep` 的是若干匹配行，
`execute` 的是命令输出）—— 这个仓库看不到那些形状，**猜出来的摘要会撒谎**。
★ **本仓库不实现、不猜**：摘要要么由上游给，要么由上游在工具的结果上加一个
机器可读的摘要位。⇒ 上游要定三件事：
  1. **形状定在哪** —— 摘要属于**结果**（每种工具自己的形状），还是属于**工具的声明**
     （一份模型侧与界面侧共用的描述）？
  2. **摘要的类型** —— 一句话？几项？还是一个结构？★ 界面上能显示的只有短文本，
     结构化的东西得有一个稳定的读法。
  3. **没有摘要时怎么说** —— 工具不给摘要，界面是**说「没有摘要」**，还是**退回结果的第一行**？
     ★ 退回第一行是猜：它可能是一行错误，也可能是 JSON 的开头 —— 说清楚比猜好。
★ 上游不定形状，这一条就落不了地；今天界面上只有内容，没有摘要。
**出处**：`agentkit/fsx/fsx.go` 的后端与上游的 `filesystem` 工具实现。

### 17.4 ★ 「这一次调用失败了」这一位已经落地（不是待办）。
★ **2026-11-17 的决定**：**本地实现** —— 线路另带一个标记，
不改上游的事件类型（上游的 `events.ToolCallResultEvent` 仍然只有内容）。
★ **事实从哪儿来**：safe-tool 中间件（`helpers/middleware.go:43`）是**唯一**看见
工具错误的一层 —— 它把错误吞成 `[tool error] …` 给模型，模型与上层看到的都是字符串。
失败就在那里被记下（`helpers/toolfail.go:123` 的 `recordToolFailure`），
记进**这一轮**的收集器（`helpers/toolfail.go:42` 的 `ToolFailures`），
收集器**随上下文走**（`agentkit/run/runtime.go:497`）——
与项目根、授权档位同一条路：上下文是唯一能到达工具调用的通道。
★ **两个读者，一个事实**：
  - **线路**：CUSTOM 帧 `tool_failed`（`transport/transport.go:460` 的 `emitToolFailed`），
    载荷是 `tool_call_id` / `error` / `tool_name?`；
  - **归档消息**：同一枚章（`message/toolerror.go:23` 的 `StampToolError`），
    落进消息的 `Extra`，所以**重新加载不会把失败变成成功**。
⇒ 服务端是权威，客户端**不嗅探文本**：结果里那一行 `[tool error]` 是给模型看的，
  不是给界面看的判据。
★ **判词不造卡**：失败帧只给**已经在屏上**的那次调用落款
（`web/src/lib/events.ts:531` 的 `onToolFailed`，`create = false`）——
一次调用若不在屏上，它没有可判定的失败。
★ **中断重跑不算失败**：审批在等答案（`compose.IsInterruptRerunError`）时，
  那一轮不是坏了 —— 把它读成失败，审批看起来就像出错。
★ **界面自己说**：一次失败的调用，卡片头部写 `failed`
（`web/src/components/tool-card.tsx:183`），不写 `done` ——
折叠状态说「done」是撒谎。
**出处**：`helpers/toolfail.go`、`helpers/middleware.go`、`contract/runevent.go:43`、
`agentkit/emit/emit.go:339`、`transport/transport.go:460`、`message/toolerror.go`、
`api/v2_convert.go:90`、`web/src/lib/events.ts:531`、`web/src/lib/store/history.ts:62`、
`web/src/components/tool-card.tsx:183`。


### 17.5 ★ 已撤回的路线（不是待办）

**`file` 块那条路已于 2026-11-10 当日撤回。** 那一版把非图片附件做成 chat completions 的 `file`
块并配了降级链；同日晚些时候改成「消息头 `@` 绝对路径 + 模型 `read_file`」。

★ **撤回的理由**：DeepSeek 的 file 块**只收图片**（webp/png/jpeg/gif），文本文件两条路都会被拒 ——
所以它解决不了文本附件的问题；而它要付出的代价是**文件先上传给第三方**，还要管 `file_id` 的
生命周期。`llm/common/files.go`、`llm/common/vision.go` 及其测试全部删除，`llm/common/model.go`
收敛为**一次请求**。

★ **撤回的代价是明确的**：一次请求失败就没有第二次，模型没有视觉时那一轮直接失败（§9.8 第 4 条）。
当前实现以本文 §9.3 为准。

## 附录 A：本文引用的代码位置

★ 行号都是**重新定位**过的：源文档里的行号已经漂移，本表里的每一行都实际打开核对过。
拿不准的地方写文件名而不写行号 —— 写一个错的行号比不写更糟（`docs/pitfalls.md` §10.1）。

### A.1 后端：消息与附件清单

| 位置 | 说的是什么 |
|---|---|
| `message/attachment.go:19` | `ExtraAttachments` 的键 |
| `message/attachment.go:30` | `Attachment{Name, Path, Size, MIME}` |
| `message/attachment.go:58-67` | `StampAttachments`：清单存成 JSON 字节 |
| `message/attachment.go:82-121` | `ReadAttachments`：认三种形态 |
| `message/attachment.go:149-182` | `MIMEOfExt`：按文件名判定类型 |
| `message/provenance.go:10` | `Extra` 里那组 `openheron_*` 键 |
| `message/provenance.go:26` | `ExtraMessageID` |
| `message/provenance.go:71` | `put`：「只写一次」的盖章语义 |
| `message/provenance_gob_test.go:68` | `Extra` 值的 gob 可编码性白名单 |
| `message/extract.go:60-65` | `UserText`：模型看得见的正文 |
| `message/identity.go:18-23` | `StampMessageID`：消息自己的身份 |

### A.2 后端：契约、解析与落盘

| 位置 | 说的是什么 |
|---|---|
| `api/v2_run.go:76` | `runInput.Attachments` |
| `api/v2_run.go:91` | `runAttachment`：线上没有 content type |
| `api/v2_run.go` | `isAttachRequest` |
| `api/v2_run.go:229` | 附件也算一个问题的判据 |
| `api/v2_run.go:304` | 附件在轮次开始之前解析 |
| `api/v2_run.go:311` | 只有附件没有文字的一轮也是一轮 |
| `api/v2_attachments.go:33-68` | `resolveAttachments` |
| `api/v2_attachments.go:46` | 服务端自己 stat；目录不是附件 |
| `api/v2_attachments.go:64` | 类型由服务端按名字判定 |
| `api/v2_files.go:73` | `GET /v2/sessions/:id/files/raw` 路由 |
| `api/v2_files.go:145` | `maxUploadBytes` = 32 MiB |
| `api/v2_files.go:185` | 上传时在服务端压缩 |
| `api/v2_files.go:201` | 哈希命名对**所有**文件 |
| `api/v2_files.go:226` | `os.Chtimes` 把 mtime 钉成 t0 |
| `api/v2_files.go:244` | `RecordUpload`：原名映射 |
| `api/v2_files.go:273` | raw 端点的四种响应 |
| `api/v2_files.go:328` | `max` 只对图片有意义 |
| `api/v2_files.go:347` | 文本类一律 `text/plain` |
| `api/v2_files.go:381-388` | `isPreviewImage`：SVG 不是图片 |
| `api/v2_files.go:458-500` | `listWorkspace`：用原名映射还原 `Name` |
| `api/v2_files.go:516-544` | `v2DeleteFile` |
| `api/v2_convert.go:76` | 清单进 `Metadata["attachments"]` |
| `api/v2_convert.go:124` | 只有附件的消息不再被丢掉 |
| `contract/runevent.go:43` | `RunEventToolFailed`：失败是**单独**的事件 |
| `message/toolerror.go:15` | `ExtraToolError`：失败盖在消息的 `Extra` 上 |
| `message/toolerror.go:23` | `StampToolError`：归档消息上的那枚章 |
| `api/v2_convert.go:90` | 历史里的失败：`Metadata["toolError"]` |
| `storage/storage.go:121` | `Session.ResolveWorkspaceFile` |
| `storage/storage.go:133` | `Session.DeleteWorkspaceFile` |
| `storage/filestore/session.go:609-611` | `ResolveWorkspaceFile` → `safeJoin` |
| `storage/filestore/session.go:624-637` | `DeleteWorkspaceFile`：删一个不存在的文件不是错误 |
| `storage/filestore/session.go:635` | 删文件时清掉原名映射 |
| `storage/filestore/session.go:639-665` | `PutFile` |
| `storage/filestore/session.go:715-726` | `safeJoin`：越界的名字被拒 |
| `storage/filestore/uploads.go:16` | `uploads.json` 住在会话目录，不在 workspace |
| `storage/filestore/uploads.go:38-55` | `RecordUpload`：在会话的锁下写 |
| `storage/filestore/uploads.go:63-72` | `OriginalNames` |
| `storage/filestore/uploads.go:80-100` | `forgetUpload` |
| `storage/filestore/transcript.go:22` | `maxLineBytes` = 8 MiB |

### A.3 后端：装配与 wire

| 位置 | 说的是什么 |
|---|---|
| `helpers/toolfail.go:42` | `ToolFailures`：一轮一个的失败收集器 |
| `helpers/toolfail.go:123` | `recordToolFailure`：唯一看见工具错误的那一层 |
| `helpers/middleware.go:43` | safe-tool 把错误吞成字符串之前，先记下来 |
| `agentkit/run/turn.go:81` | `Turn.toolFailures`：这一轮的收集器 |
| `agentkit/run/bridge.go:388` | 每轮建一个新的收集器 |
| `agentkit/run/runtime.go:497` | 收集器随上下文走（与项目根、授权档位同一条路） |
| `agentkit/run/loop.go:187` | 收集器交给归约层 |
| `agentkit/emit/emit.go:339` | 归约层取走失败：盖章 + 发事件 |
| `agentkit/run/loop.go:68` | 造消息、盖时间戳、盖 ID |
| `agentkit/run/loop.go:74` | `StampAttachments` |
| `agentkit/run/loop.go:81` | 先装配 |
| `agentkit/run/loop.go:102` | 后落盘；写盘失败不中断这一轮 |
| `agentkit/run/bridge.go:29` | `loopItem.Attachments` |
| `agentkit/run/input.go:60` | `attachmentRules`：`read_file` 是义务 |
| `agentkit/run/input.go:95-128` | `buildInput` 收消息对象 |
| `agentkit/run/input.go:142-148` | `withHistoryAttachmentPaths` |
| `agentkit/run/attachments.go:28` | `maxInlineImageBytes` = 5 MiB |
| `agentkit/run/attachments.go:35` | `imageMIMEs` |
| `agentkit/run/attachments.go:91-102` | `isReadableText` |
| `agentkit/run/attachments.go:128-134` | `mentionPath`：绝对路径，含空白时加引号 |
| `agentkit/run/attachments.go:148-169` | `attachmentPaths` |
| `agentkit/run/attachments.go:173-179` | `blockedReason` |
| `agentkit/run/attachments.go:183-190` | `blockedNote` |
| `agentkit/run/attachments.go:203-223` | `presentMessage`：给模型的是一条副本 |
| `agentkit/run/attachments.go:233-237` | `assembleAttachments` |
| `agentkit/run/attachments.go:257` | 超限的图片进「不能处理」清单 |
| `agentkit/run/attachments.go:272-300` | `inlineImage` |
| `agentkit/run/attachments.go:289` | base64 进 `Base64Data` |
| `agentkit/fsx/fsx.go:218` | `resolve`：相对路径按项目根解析 |
| `agentkit/build/snapshot.go:110` | workspace 所在的 dataDir 进 fsx 的 `ExtraRoots`（`:103` 赋值） |
| `llm/common/shape.go:52-58` | `normalizeWireShape` |
| `llm/common/shape.go:68` | `noVisibleResponseText` |
| `llm/common/shape.go:74-90` | `withVisibleAssistantContent` |
| `llm/common/shape.go:93-121` | `oneShape`：正文挪进 text part |
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
| `agentkit/imaging/imaging.go:310-323` | `HashedName` |
| `agentkit/imaging/imaging.go:356-367` | `hashOf`：定宽大端 + `0x00` 分隔 |

### A.5 前端：store 与装配

| 位置 | 说的是什么 |
|---|---|
| `web/src/lib/types.ts:41` | `AttachmentPart` |
| `web/src/lib/types.ts:50` | `Part` 联合类型 |
| `web/src/lib/store/state.ts:8` | `Attachment` |
| `web/src/lib/store/state.ts:33` | 待发清单按会话存放 |
| `web/src/lib/store/actions.ts:586` | `removeFile`：删真文件，失败就把 chip 放回去 |
| `web/src/lib/store/actions.ts:599` | 删失败时把 chip 放回去 |
| `web/src/lib/store/actions.ts:610` | `send` |
| `web/src/lib/store/actions.ts:628` | 取出待发清单 |
| `web/src/lib/store/actions.ts:708` | 发完清空 |
| `web/src/lib/store/actions.ts:716` | 请求体带上附件 |
| `web/src/lib/store/actions.ts:729` | 被拒的一轮把文件还回 |
| `web/src/lib/store/actions.ts:1489` | `appendLocalUserMessage`：乐观气泡 |
| `web/src/lib/store/actions.ts:1522` | `restorePending` |
| `web/src/lib/store/actions.ts:1545` | `restorePendingFiles` |
| `web/src/lib/store/actions.ts:1539` | 「没有消息认领过」才是待发 |
| `web/src/lib/store/actions.ts:1527` | 合并而不是覆盖 |
| `web/src/lib/store/history.ts:16` | `readAttachments` |
| `web/src/lib/store/history.ts:100` | 历史水合时装配 part |
| `web/src/lib/events.ts:188` | `isOpenPart`：联合类型变宽后的收窄 |
| `web/src/lib/api.ts:370` | `fileUrl`：字符串而不是 fetch |
| `web/src/lib/api.ts:752` | 请求体里的 `attachments` |
| `web/src/lib/authorization.ts:26` | `authorizationOptions`：三档的文案与顺序 |
| `web/src/lib/authorization.ts:51` | `authorizationMode`：不认识的名字读作默认 |
| `web/src/lib/store/actions.ts` `setAuthorizationMode` | ★ 改档位：合进服务端回来的 meta，失败再抛 |
| `web/src/lib/store/selectors.ts` `selectAuthorizationMode` / `selectAuthorizationConfirmed` | ★ 引用稳定的原始值 |
| `web/src/components/authorization-picker.tsx:62` | `AuthorizationPicker` |
| `web/src/components/authorization-picker.tsx:77-92` | ★ 欢迎页上选 `full` 时本地弹确认框 |
| `web/src/components/authorization-picker.tsx:188-192` | ★ 弹窗描述写着「下一轮生效」 |
| `web/src/components/authorization-picker.tsx:257` | ★ `onEscapeKeyDown`：平台拥有的键 |
| `web/src/components/composer.tsx:713-754` | 三个入口在同一个 `action` 槽里 |

### A.6 前端：预览与动效

| 位置 | 说的是什么 |
|---|---|
| `web/src/lib/file-kind.ts:133` | `effectiveMime`：服务端的 mime 优先 |
| `web/src/lib/file-kind.ts:177` | `isImage`：SVG 不是图片 |
| `web/src/lib/file-kind.ts:201` | `fileKind` |
| `web/src/lib/file-kind.ts:222` | `fileLang` |
| `web/src/components/composer.tsx:542` | `onDragOver` |
| `web/src/components/composer.tsx:555` | `onDrop` |
| `web/src/components/composer.tsx:565-568` | `<input type="file">` |
| `web/src/components/composer.tsx:622` | `onPaste` |
| `web/src/components/composer.tsx:632` | 粘贴时 `preventDefault()` |
| `web/src/components/attachment-chip.tsx:41` | `THUMB` = 48 |
| `web/src/components/attachment-chip.tsx:63` | `AttachmentChip` |
| `web/src/components/attachment-chip.tsx:181` | 缩略图在出站时缩放 |
| `web/src/components/attachment-chip.tsx:184` | `onError` 回退成图标 |
| `web/src/components/file-preview.tsx:28` | `FilePreview` 覆盖层 |
| `web/src/components/file-preview.tsx:47` | Esc 关闭 |
| `web/src/components/file-preview.tsx:83` | PDF 内嵌 |
| `web/src/components/file-preview.tsx:185` | 文本预览 |
| `web/src/components/markdown.tsx:53` | `MarkdownText` |
| `web/src/components/code-block.tsx:6` | `CodeBlock` |
| `web/src/components/message-parts.tsx:110` | `case "attachment"` |
| `web/src/components/message-list.tsx:122` | `ResizeObserver`：按内容高度跟随 |
| `web/src/components/message-list.tsx:217` | 欢迎页 hero 的品牌标记 |
| `web/src/components/message-list.tsx:237` | 「Working…」的 pulse 点 |
| `web/src/app/shell.tsx:29` | 状态点的 pulse |
| `web/src/app/shell.tsx:167` | 页头的品牌标记（不播） |
| `web/src/components/tool-card.tsx:135` | 折叠箭头 |
| `web/src/components/ui/dialog.tsx:43` | 对话框浮层的 `data-open` |
| `web/src/index.css:121` | 动效令牌 |
| `web/src/index.css:230` | `prefers-reduced-motion` 的全局规则 |
| `web/src/index.css:340` | 三个 `@keyframes` |
| `web/src/index.css:369` | `.mark-parts` 只淡入 |
| `web/src/index.css:382` | `transform-box: fill-box` |
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

### A.7 前端：操作时间线面板

| 位置 | 说的是什么 |
|---|---|
| `web/src/components/operations-panel.tsx:30` | `OperationsPanel`：入口与弹窗 |
| `web/src/components/operations-panel.tsx:71` | ★ 按钮与弹窗住在一个 wrapper 里 |
| `web/src/components/operations-panel.tsx:100` | ★ Esc 走 `onEscapeKeyDown` |
| `web/src/components/operations-panel.tsx:132` | 列表：`min-h-0 flex-1 overflow-y-auto` |
| `web/src/components/operations-panel.tsx:190` | 十一种 `kind` 的英文句子 |
| `web/src/components/operations-panel.tsx:297` | 颜色只用令牌 |
| `web/src/components/operations-panel.tsx:312` | `formatAt`：时间自己写，不跟 locale |
| `web/src/lib/api.ts:121` | `OperationView` |
| `web/src/lib/api.ts:150` | `OperationsView` |
| `web/src/lib/api.ts:505` | `listOperations` |
| `web/src/lib/store/state.ts:27` | `SessionEntry.operations` |
| `web/src/lib/store/state.ts:133` | `loadOperations` |
| `web/src/lib/store/actions.ts:436` | `loadOperations` 的实现 |
| `web/src/lib/store/actions.ts:1111-1117` | ★ 通道的事实到达时重读日志 |
| `web/src/lib/store/selectors.ts:69` | `EMPTY_OPERATIONS` |
| `web/src/lib/store/selectors.ts:150-166` | `selectAuthorizationMode` / `selectAuthorizationConfirmed`（没有会话时读暂存值） |
| `web/src/lib/store/selectors.ts:76` | `selectOperations` |
| `web/src/components/composer.tsx:734` | `action` 槽：授权 → 项目 → 活动 |
| `web/test/operations.test.ts` | selector 的引用稳定性 |
| `web/test/e2e/operations.spec.ts` | 面板的验收 |

### A.8 前端：输入框下面那一行的排版

| 位置 | 说的是什么 |
|---|---|
| `web/src/components/composer-meta.tsx:11` | `META`：那一行的行盒 |
| `web/src/components/composer-meta.tsx:30` | ★ `Joined`：分隔符跟着后一项走 |
| `web/src/components/composer-meta.tsx:119` | 事实那一半：agent / 模型 / 思考档位 / 上下文 |
| `web/src/components/composer-meta.tsx:230-231` | `contents` 包装与 `action` 槽 |
| `web/src/components/composer.tsx:515` | ★ `@container`：那一行的宽度判据是**列**，不是窗口（§5.6.5） |
| `web/src/components/composer.tsx:744` | ★ 入口节点：列窄时与事实同一行，右对齐 |
| `web/src/components/projects-dialog.tsx:73-76` | ★ 项目名截断，全名留在 tooltip 与 `aria-label` |
| `web/src/components/authorization-picker.tsx:161-169` | ★ 列窄时让位的那个词（档位名） |
| `web/src/components/projects-dialog.tsx:90`、`:111` | ★ 列窄时让位的那个词（项目名） |
| `web/src/components/operations-panel.tsx:93` | ★ 列窄时让位的那个词（Activity） |
| `web/src/components/composer-meta.tsx:186` | ★ 模型名：列窄时唯一让位的那一项（`shrink` + 上限） |
| `web/src/components/composer-meta.tsx:300` | 占用条：列窄时短一截 |
| `web/test/e2e/composer-meta.spec.ts:147` | 列窄的验收（不溢出 / 不截断 / 分隔符不落单 / 事实与入口同一行 / 入口只留图标 / 让位的档位在提示里） |
| `web/test/e2e/composer-meta.spec.ts:356` | ★ 宽窗窄列的验收（960×1440：入口只留图标，事实仍是完整版） |
| `web/test/e2e/projects.spec.ts:671` | ★ 项目名很长时那一行也不溢出 |

### A.9 前端：工具调用的渲染

| 位置 | 说的是什么 |
|---|---|
| `web/src/lib/tool-groups.ts:36` | `segments`：连续的调用并成一段 |
| `web/src/lib/tool-groups.ts:85` | `summarize`：几步、哪些工具、成了没有 |
| `web/src/lib/tool-groups.ts:120` | `phaseLabel`：阶段的那句英文 |
| `web/src/components/tool-group.tsx:26` | `ToolGroup`：组条与那几行 |
| `web/src/components/message-parts.tsx:73` | ★ 消息里的调用按段渲染 |
| `web/src/components/tool-card.tsx:197-199` | ★ 结果先于参数 |
| `web/src/components/tool-card.tsx:183` | ★ 一次失败的调用，卡片头部说 `failed` |
| `web/test/tool-failure.test.ts:33` | 失败标志的纯函数用例 |
| `web/test/tool-groups.test.ts:21` | 归并的边界与段的 key |
| `web/test/fake-server.ts:1158` | `toolMany`：一条消息里的两次调用 |
| `web/test/e2e/chat.spec.ts:1263` | 组的验收 |

### A.10 前端：架构与依赖

| 位置 | 说的是什么 |
|---|---|
| `web/package.json:22-43` | 20 个运行时依赖 |
| `web/package.json:44-60` | 15 个开发依赖 |
| `web/package.json:17` | `test:unit` 只跑 `test/*.test.ts` |
| `web/package.json:8` | build 末尾重建 `dist/.gitkeep` |
| `web/src/App.tsx:5-7` | 设置弹窗是 `lazy()` 进来的 |
| `web/vite.config.ts:12-13` | `loadEnv(mode, repoRoot, "OPENHERON_")` |
| `web/vite.config.ts:16-18` | 开发 :5173 → :8080；测试 :15173 → :18099 |
| `web/vite.config.ts:40` | `strictPort: true` |
| `web/vite.config.ts:41-43` | 代理只代理 `/v2` |
| `web/vite.config.ts:48` | `chunkSizeWarningLimit` = 700 |
| `web/vite.config.ts:51-56` | 手动分包：`vendor` 与 `markdown` |
| `web/src/lib/store.ts:1-29` | barrel：store 的公开面 |

### A.11 前端：事件流与状态

| 位置 | 说的是什么 |
|---|---|
| `web/src/lib/sse.ts:7-22` | `takeEvents` |
| `web/src/lib/sse.ts:24-30` | 帧边界认 `\n\n` 与 `\r\n\r\n` |
| `web/src/lib/sse.ts:40-47` | 剥掉一个前导空格 ⇒ 两种写法都收 |
| `web/src/lib/sse.ts:55-76` | `readSse` |
| `web/src/lib/events.ts:63-68` | `ReasoningStartEvent` 含两个 type |
| `web/src/lib/events.ts:160-174` | `AgEvent`：14 个 union 成员 |
| `web/src/lib/events.ts:182-191` | `isOpenPart`：按 kind 收窄 |
| `web/src/lib/events.ts:215-248` | `openPart`：最后一个未完成的 part |
| `web/src/lib/events.ts:394-396` | `default` 分支 |
| `web/src/lib/events.ts:390` | CUSTOM 分派：`tool_failed` |
| `web/src/lib/events.ts:531` | `onToolFailed`：给那次调用打上 `isError` |
| `web/src/lib/events.ts:550` | `create`：判词不造卡 |
| `web/src/lib/events.ts:569-573` | `closeAllOpenParts` |
| `web/src/lib/store/state.ts:18-32` | `SessionEntry` |
| `web/src/lib/store/state.ts:35` | `activeId` |
| `web/src/lib/store/selectors.ts:26-28` | `EMPTY_MESSAGES` / `EMPTY_INTERRUPTS` / `EMPTY_CONVERSATION` |
| `web/src/lib/store/selectors.ts:62` | `EMPTY_ATTACHMENTS` |
| `web/src/lib/store/selectors.ts:69` | `EMPTY_OPERATIONS` |
| `web/src/lib/store/selectors.ts:150-166` | `selectAuthorizationMode` / `selectAuthorizationConfirmed`（没有会话时读暂存值） |
| `web/src/lib/store/selectors.ts:99-101` | `selectSessionList`（配 `useShallow`） |
| `web/src/lib/store/events.ts:10` | 模块级 `currentRunKey` |
| `web/src/lib/store/events.ts:12-23` | `handleEvent` |
| `web/src/lib/store/events.ts:25-49` | `promote`：就地换键（并把暂存的档位清掉） |
| `web/src/lib/store/actions.ts:177` | `useStore = create<StoreState>` |
| `web/src/lib/store/actions.ts:359-385` | `openSession`：`convo` 懒加载 |
| `web/src/lib/store/actions.ts:448-462` | `startNewThread`：不碰服务端 |
| `web/src/lib/store/actions.ts:511-513` | 改名乐观更新与回滚 |
| `web/src/lib/store/actions.ts:536` | 删掉正在看的那条 ⇒ 回欢迎界面 |
| `web/src/lib/store/actions.ts:616` | `pending:<时间戳>` 这个仅内存的键 |
| `web/src/lib/store/actions.ts:480-506` | `setAuthorizationMode`：没有会话时**暂存** |
| `web/src/lib/store/actions.ts:1433-1450` | `ensureSession` 建会话时带上暂存的档位 |
| `web/src/lib/store/actions.ts:696` | 建会话成功后立刻 promote |
| `web/src/lib/store/actions.ts:1218-1261` | 一次答完整组 |
| `web/src/lib/store/actions.ts:1263-1269` | `stop()` 的三步 |
| `web/src/lib/store/history.ts:161-172` | 从 `pendingApprovals` 恢复审批 |
| `web/src/lib/store/history.ts:62` | `readToolError`：历史里的失败是事实 |
| `web/src/lib/store/history.ts:93` | 水合时把失败补回工具卡 |
| `web/src/lib/api.ts:536` | `listAgents` 用 `cache: "no-store"` |
| `web/src/lib/api.ts:595-599` | `ResumeInput`：提交的是 `approved` |
| `web/src/lib/api.ts:665-691` | `FRAME_MS` 与紧急帧 / 终止帧 |
| `web/src/lib/api.ts:430-457` | `createThread`：带上项目与暂存的档位 |
| `web/src/lib/session-events.ts:21-51` | 公告通道的三种事实 |

### A.12 前端：界面骨架与消息

| 位置 | 说的是什么 |
|---|---|
| `web/src/app/shell.tsx:28-33` | 状态点的两档 |
| `web/src/app/shell.tsx:218` | 审批卡取代 composer |
| `web/src/components/sidebar.tsx:104` | 侧栏：宽度是 token，高度是**屏幕**的（`min-h-0`） |
| `web/src/components/sidebar.tsx:582` | `thread-row` |
| `web/src/components/sidebar.tsx:587` | `thread-item` |
| `web/src/components/sidebar.tsx:613` | 行自己的菜单键（窄屏上一直显形） |
| `web/src/app/shell.tsx:26` | `WIDE` —— 断点本身 |
| `web/src/app/shell.tsx:141-144` | 一个侧栏：宽屏常驻，窄屏是抽屉 |
| `web/src/app/shell.tsx:156` | 聊天列：高度是**框架**的（`min-h-0`） |
| `web/src/components/sidebar-drawer.tsx:35-57` | 抽屉本体（Radix Dialog） |
| `web/src/components/sidebar-drawer.tsx:46` | 抽屉的高度是**屏幕**的高度（`inset-y-0`） |
| `web/src/components/sidebar.tsx:129` | 会话树的滚动容器（`min-h-0 flex-1 overflow-y-auto`） |
| `web/src/lib/use-media-query.ts:19` | `useMediaQuery` |
| `web/src/index.css:51` | `--welcome-column` —— 欢迎页那一列，留在 48rem 以上 |
| `web/src/index.css:56` | `--sidebar-width` |
| `web/src/index.css:62` | `--touch-target` |
| `web/src/index.css:68-71` | 安全区的四个 `env()` |
| `web/src/index.css:259` | `.touch-target` —— 伪元素撑开命中区域 |
| `web/src/index.css:286` | `.app-frame` —— 安全区加在框架上 |
| `web/src/index.css:315` | `.thread-row-menu` —— 窄屏上一直显形 |
| `web/test/e2e/responsive.spec.ts` | 三档视口的回归网（§5.9.6） |
| `web/src/lib/scroll-follow.ts:13` | `BOTTOM_TOLERANCE` = 32 |
| `web/src/components/message-list.tsx:120-136` | Jump to latest 是瞬时滚动 |
| `web/src/components/message-list.tsx:270` | 每条消息一个节点 |
| `web/src/components/message-list.tsx:275` | 轮间 `mt-6` / 轮内 `mt-1.5` |
| `web/src/components/message-list.tsx:276-287` | 多 agent 时角色标签显示 agent 名 |
| `web/src/components/message-list.tsx:301-321` | 加载骨架 |
| `web/src/components/composer.tsx:238-281` | 焦点回到输入框 |
| `web/src/components/composer.tsx:698-699` | 停止按钮取代发送按钮 |
| `web/src/components/composer.tsx:744-752` | 三个入口在同一个节点里 |
| `web/src/components/composer-meta.tsx:139-144` | `reasoningAvailable` 只进 title |
| `web/src/components/composer-meta.tsx:244-316` | 上下文占用 |
| `web/src/components/markdown.tsx:65-66` | 不开 `rehype-raw` |
| `web/src/components/markdown.tsx:71` | 表格的滚动容器 |
| `web/src/components/markdown.tsx:74` | 表格本身 `w-full` |
| `web/src/components/markdown.tsx:83` | 单元格的 `min-w` / `max-w` |
| `web/src/components/markdown.tsx:94-126` | 清单的覆写 |
| `web/src/components/markdown.tsx:170` | 正文 15px / 1.75 / break-words |
| `web/src/lib/highlight.ts:20-42` | shiki 的动态 import |
| `web/src/lib/highlight.ts:68` | `structure: "inline"` |
| `web/src/lib/highlight.ts:70-72` | 高亮失败退回纯文本 |
| `web/src/components/code-block.tsx:30-39` | 离屏不排高亮 |
| `web/src/components/code-block.tsx:45-55` | 流式下不高亮正在写的那段 |
| `web/src/lib/turns.ts:13-19` | 轮次边界与角色标签 |
| `web/src/lib/approval.ts:5` | `MAX_ARGUMENT_CHARS` = 180 |
| `web/src/lib/approval.ts:55-59` | 参数摘要归一与截断 |
| `web/src/components/approval-card.tsx:53-93` | 审批卡逐条只列三样 |
| `web/src/components/approval-card.tsx:97-112` | 整组一个同意/拒绝按钮 |
| `web/src/lib/agent-info.ts:14-22` | 按 id 选 agent |
| `web/src/lib/agent-info.ts:81-107` | 配置指纹 |
| `web/src/lib/use-agent-badge.ts:6` | `CONFIG_POLL_MS` = 3000 |
| `web/src/lib/use-agent-badge.ts:64-82` | 轮询指纹 |
| `web/src/lib/commands.ts:92-102` | `agentCommandFor` |
| `web/src/lib/store/history.ts:130` | ★ `fromHistory` —— 今天没有任何读取方 |

### A.13 前端：工程化与验收

| 位置 | 说的是什么 |
|---|---|
| `main.go:42-43` | `//go:embed all:web/dist` |
| `.env.test:25-39` | 测试环境的端口与路径 |
| `web/playwright.config.ts:7-24` | `envFile()` |
| `web/playwright.config.ts:49-65` | 两条 webServer，`reuseExistingServer: false` |
| `web/playwright.config.ts:61` | 注入 `OPENHERON_API_TARGET` |
| `web/test/e2e/fixtures.ts:67-85` | 定位点常量 |
| `web/test/e2e/fixtures.ts:87-111` | 点菜单走 `getByRole` |
| `web/test/fake-server.ts:306` | `SCENARIOS`：32 个场景 |
| `web/test/fake-server.ts:356` | `toolFailed`：一次失败的工具调用 |
| `web/test/e2e/tool-failure.spec.ts:16` | 失败标志的界面验收 |
| `web/test/serve.ts:3-4` | 假后端的入口 |
| `web/src/lib/fixtures/recorded-frames.ts:2` | 真后端录下的原始帧 |
| `web/src/lib/fixtures/recorded-run.ts:3` | 真后端录下的事件序列 |
| `web/test/sse.test.ts:17-25` | 两种写法都收 |
| `web/test/events.test.ts:9-30` | fixture 覆盖后端发出的每一种事件 |
| `web/test/approval.test.ts:23-62` | 参数摘要 ≤ 180 |
| `webui/webui.go:21` | API 前缀 |
| `webui/webui.go:24-31` | `looksLikeAPI` |
| `webui/webui.go:46-51` | 命中前缀回 404 + JSON |
| `webui/webui_test.go:8-22` | 正反两组用例 |
| `scripts/test-backend.sh:17-20` | 读 `.env.test` |
| `scripts/test-backend.sh:46-48` | 点名三个根 |
| `scripts/test-backend.sh:132-136` | 导出那几个根 |
| `agentkit/spec/config.go:628-637` | 配置根三级解析，不向上搜索 |
| `agentkit/spec/init_write_test.go:12` | `TestNewServiceDoesNotProbeUpwards` |
| `Taskfile.yml:541-546` | `lint-web` |
| `Taskfile.yml:575-579` | `test-env-backend` |
| `Taskfile.yml:581-584` | `test-env-reset` |
| `Taskfile.yml:586-589` | `test-env-sync` |
| `Taskfile.yml:603-609` | `smoke` |
| `Taskfile.yml:622-626` | `test-e2e` |
| `.github/workflows/ci.yml:80-83` | CI 跑 `lint-web` 与 `test:unit` |
| `.github/workflows/ci.yml:147` | CI 跑 `test-e2e` |
| `.github/workflows/ci.yml:12` | ★ 冒烟**不在** CI |
| `.gitignore:22-24` | `dist/.gitkeep` 的第一处例外 |
| `web/.gitignore:34-36` | `dist/.gitkeep` 的第二处例外 |

### A.14 前端看到的线路：事件发射点

| 位置 | 说的是什么 |
|---|---|
| `transport/transport.go:77` | `RUN_STARTED` |
| `transport/transport.go:88`、`:94` | `RUN_FINISHED`：interrupt / success |
| `transport/transport.go:373-374` | `RUN_ERROR` |
| `transport/transport.go:379` / `:390` / `:396` | 正文的三个事件 |
| `transport/transport.go:400` | ★ `reasoningRole` = 字面量 `"reasoning"` |
| `transport/transport.go:402-405` | 思维链开始 |
| `transport/transport.go:407` | 思维链增量 |
| `transport/transport.go:411-414` | 思维链结束 |
| `transport/transport.go:416` / `:426` / `:432` | 一次工具调用的三个事件 |
| `transport/transport.go:443-450` | ★ `TOOL_CALL_RESULT` 只有内容 |
| `transport/transport.go:460` | ★ `tool_failed`：失败是**另一帧**，不是结果帧上的标志 |
| `transport/transport.go:247` | `context_usage` |
| `transport/transport.go:270` | `context_compacted` |
| `transport/transport.go:287` | `model_retry` |
| `transport/transport.go:330` | `approval_pending` |
| `transport/transport.go:344` | `approval_resolved` |
| `transport/transport.go:360` | `run_started`（公告通道用） |
| `transport/transport_test.go:670` | ★ 钉住 `REASONING_MESSAGE_START` 的 role |
