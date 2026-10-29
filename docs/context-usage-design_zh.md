# 上下文使用预估 —— 设计方案

> **状态**：提案。本文描述的一切都尚未实现。
> **范围**：本仓库的代码面 —— Go 后端与 `web/` 前端。
> 配套阅读：[architecture.md](architecture.md)（代码怎么分层）、
> [developer-manual.md](developer-manual.md)（怎么改代码）、
> [user-manual.md](user-manual.md)（怎么用）。

> English version: [context-usage-design.md](context-usage-design.md)

---

## 0. 一句话概括

openheron 手里已经握有两个足以回答「这个会话是不是快撑爆上下文了？」的事实 —— 模型的上下文窗口（`llm/*.yaml` 里的 `context_window`）与厂商回报的 token 数（每条 assistant 消息上的 `ResponseMeta.Usage`）。两者都没有出现在任何界面上；而**已经**暴露出去的用量（`GET /v2/sessions/{id}`）是整个会话的**累计总和**，回答不了这个问题：一个会话可以总量很大而上下文几乎为空，也可以总量不大而上下文快满了。

本设计新增一个数字 —— 每个 agent 的**上下文占用** —— 它的基线与 eino 自带的摘要中间件所数的是同一个基线，在运行过程中就随 SSE 推给前端，最终渲染在输入框下方的元信息行与按 agent 展开的过滤器里。

---

## 1. 今天已经有什么

### 1.1 上下文窗口配了，然后被忽略

`context_window` 是模型声明的一部分（`agentkit/spec/decl/decl.go:42`）：

```go
ContextWindow int `yaml:"context_window"`
```

出厂模板带着它 —— OpenAI 是 `128000`（`initcfg/templates/llm/openai.yaml:16`），DeepSeek 是 `131072`（`initcfg/templates/llm/deepseek.yaml:15`），通用模板是 `0`，留给用户自己填（`initcfg/templates/llm/_generic.yaml:14`）。

它只流到一个地方：`GET /v2/config` 的厂商列表（`api/v2_introspect.go:276`，在 `api/v2_introspect.go:364` 填入）。**运行时没有任何代码读它。** 前端真正用的 agent 视图（`api/v2_introspect.go:77`，由 `modelOf` 在 `api/v2_introspect.go:197` 构造）并不带它，所以浏览器连「我正在对话的这个 agent 的窗口有多大」都拿不到。

### 1.2 token 用量早就落盘了 —— 但那是总和，不是占用

`aggregateUsage` 把厂商回报的用量按一轮里的 assistant 消息加起来（`transport/usage.go:67`）；`sessionUsage` 对整份记录做同样的事（`api/v2_sessions.go:299`）。这些数字以会话视图上的 `usage` 抵达客户端（`api/v2_types.go:192`，在 `api/v2_sessions.go:283` 填入）。

这些数字**全是累加值**。二十次调用的会话，`usage.totalTokens` 是二十次调用的总和 —— 它对「上下文现在有多满」一个字也没说。上下文不是会话的总和，上下文是**一次**请求的大小。

原材料是逐次的，而且已经持久化：每条 assistant 消息都带 `response_meta.usage`（`schema.Message.ResponseMeta`，`json:"response_meta,omitempty"`），记录文件把整条消息按 JSON 行写出（`storage/filestore/transcript.go:159`），所以用量能原样穿过这一轮往返。

### 1.3 上下文每轮都从记录里重建

每一轮都从持久化的会话历史重建模型输入：`buildInput` 拼一份临时的系统提示、历史与本轮 query（`agentkit/run/input.go:44`），循环把它与会话消息一起喂进去（`agentkit/run/loop.go:69`）。

两个推论：

- 会话的上下文**就是**它的记录。仓库里没有任何压缩、开窗或淘汰逻辑。
- eino 摘要中间件若改写状态，那份状态**在轮末就被丢弃** —— 下一轮又从记录重来。这是 §7 里最要紧的一条约束。

### 1.4 每个 agent 有自己的上下文

根 agent 的输入里不含子 agent 的消息（`agentkit/run/input.go:48`，`withoutSubagentMessages` 在 `agentkit/run/input.go:64`），每个子 agent 在 eino 内部自持一份消息列表。所以「上下文」是一个**逐 agent** 的量 —— 五个 agent 的会话有五份大小迥异的上下文。

openheron 已经把消息归属到各自的 agent（`message.MessageAgent`，被 `sessionUsageByAgent` 用在 `api/v2_sessions.go:316`），所以按 agent 拆分是有位置的。

### 1.5 eino 带了摘要中间件 —— openheron 没用

eino v0.9.21 有 `adk/middlewares/summarization`（eino v0.8.0 引入；见[上游文档](https://www.cloudwego.io/zh/docs/eino/core_modules/eino_adk/eino_adk_chatmodelagentmiddleware/middleware_summarization/)）。它挂在 `BeforeModelRewriteState` 上（`adk/middlewares/summarization/summarization.go:335`），在每次模型调用前给状态数 token，超过阈值就压缩历史（`shouldSummarize`，`adk/middlewares/summarization/summarization.go:360`）。

它的默认计数器正是本设计的地基：

- 基线是**最后一条 assistant 消息的 total tokens**（`getAssistantTotalTokens`，`adk/middlewares/summarization/summarization.go:457`，读的是 `ResponseMeta.Usage.TotalTokens`）；
- 该基线之后新增的消息按大约四字符一个 token 估算（`estimateTokenCount`，`adk/middlewares/summarization/summarization.go:476`）；
- 阈值是 `Trigger.ContextTokens`，默认 160000（`getTriggerContextTokens`，`adk/middlewares/summarization/summarization.go:373`）；上游文档建议把它设成模型窗口的 80–90%。

openheron 一点都没引：仓库里没有任何 `summarization` 引用，而它本该挂载的 `deep.Config.Handlers`（`agentkit/build/assemble.go:110`，`adk/prebuilt/deep/deep.go:97`）上只有 openheron 自己的闸门处理器（`agentkit/build/gate.go:105`）。

---

## 2. 界面要显示什么

### 2.1 那个数字

对 agent `A`：

```
used(A)        = A 最近一次模型调用的 token 数
window(A)      = A 所用模型的上下文窗口
summarizeAt(A) = A 的历史会被压缩时的 token 数
```

`used(A)` 取自 **A 最后一条带用量的 assistant 消息**：

```
used(A) = Usage.TotalTokens            （退化时取 Usage.PromptTokens + Usage.CompletionTokens）
```

`TotalTokens` 是厂商对**那一次请求**的自报数 —— 输入加输出（`adk/middlewares/summarization/summarization.go:457` 读的就是这个字段）。它作为占用数字有三条理由：

1. **它是单次请求的大小，不是总和。** 下一次请求要带上上一次带过的全部东西，再加上这之后产出的部分 —— 所以上一次请求的总量就是上下文占用，一阶近似。
2. **它是厂商自己的分词器数出来的**，不是猜的。这个估算的上限就是厂商的记账精度。
3. **它与 eino 摘要中间件所数的基线是同一个。** 等中间件接上（§7），屏幕上的数字与「该压缩了」的判定依据不会分叉。

### 2.2 两个刻度，不是一个

只给一个相对窗口的百分比，会把用户真正能据此行动的东西藏起来。所以显示两个：

| 刻度 | 分母 | 回答的问题 |
|---|---|---|
| `used / window` | `llm/*.yaml` 里的 `context_window` | *这个会话占了模型窗口的多大比例？* |
| `used / summarizeAt` | 摘要阈值 | *离被压缩还差多少？* |

`summarizeAt` 为零表示没配阈值，此时第二个刻度干脆不显示。

### 2.3 窗口未知的时候比想象中多

`context_window: 0` 的意思是*没配置* —— 通用模板出厂就是这样（`initcfg/templates/llm/_generic.yaml:14`）。窗口是用户声明的逐模型事实，openheron 不猜，界面也不该猜：

- 窗口未知 → 显示 `≈12.3k tokens`，不画条，tooltip 说明窗口未配置；
- 窗口已知 → 显示 `12.3k / 128k (10 %)`。

零窗口绝不能渲染成「0 %」。

### 2.4 这个数字在哪儿不诚实

设计上把误差方向讲明白，而不是藏起来：

- **少算**了最后一次模型调用**之后**才产出的工具结果。它们还没进过任何请求，所以不在任何 token 数里；等下一次调用时它们会进入该次请求的 `prompt_tokens`，届时数字会跳一下。
- **不会多算**，而且它的**口径**与窗口一致：`prompt_tokens` 里含系统提示与工具 schema，这些同样占窗口 —— 所以这个对比是对的。
- **推理 token** 有的厂商算在 `completion_tokens` 里，有的单列。两种情况都照厂商的总数用。

### 2.5 按 agent，不按会话

占用属于 agent。输入框下方的元信息行显示会话自己的 agent（`badge.id`）；agent 过滤器列出每个说话的 agent，各带各的占用（`web/src/components/agent-filter.tsx:29`）。

---

## 3. 数据流

```
llm/*.yaml  context_window ─┐
                            ├─► agentkit/build  Builder.Contexts()  ──► map[agent]AgentContext
decl.Agent.ModelRef ────────┘                                            │
                                                                         ▼
                                              transport.Options.Contexts (api/v2_run.go:150)
                                                                         │
  模型调用 ──► 带 ResponseMeta.Usage 的 assistant 消息 ──► transport.contextUsageOf
                                                                         │
                          ┌──────────────────────────────────────────────┤
                          ▼                                              ▼
              SSE CUSTOM "context_usage"                      transport.Result.Context
              （逐 agent，逐调用）                                    │
                                                                         ▼
                                              GET /v2/sessions/{id} → usage.context
                                                                         │
                                                                         ▼
                                      web/src/lib/events.ts（纯 reducer）
                                                                         │
                                                                         ▼
                        web/src/lib/store/selectors.ts → 组件
```

窗口**每次运行解析一次**，来源是装配出这些 agent 的那个 builder —— API 层不许自己重推某个 agent 跑在哪个模型上（§4.3）。

---

## 4. 后端

### 4.1 契约

`contract` 是 transport 与 API 层本来就共享的叶子包（`contract.Usage` 就定义在 `contract/contract.go:32`）。新类型放在它旁边：

```go
// ContextUsage is one agent's context occupancy.
//
// "Used" is the size of the agent's most recent model call — the same
// baseline eino's summarization middleware counts from, so the number the UI
// shows and the number that decides "compact now" never disagree.
type ContextUsage struct {
	Agent         string `json:"agent"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	UsedTokens    int    `json:"used_tokens"`
	ContextWindow int    `json:"context_window"`
	// SummarizeAt is the token count at which this agent's history would be
	// compacted. Zero when no threshold is configured.
	SummarizeAt int `json:"summarize_at,omitempty"`
	// Summarized counts the compactions this agent has been through.
	Summarized int `json:"summarized,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// AgentContext is what one agent's context allows.
type AgentContext struct {
	// Window is the model's context window. Zero means "not configured".
	Window int
	// SummarizeAt is the token count that triggers compaction. Zero means
	// "never".
	SummarizeAt int
}
```

结构体只带数字。比例由渲染方自己算 —— 前端本来就自己格式化 token 数（`formatTokens`，`web/src/lib/agents.ts:15`），把一条百分比规则抄成两门语言，正是它们分叉的开始。

### 4.2 计算放在 transport

逐次调用的用量本来就是 transport 的：两次 `res.Usage = aggregateUsage(res.Intermediates)` 在 `transport/transport.go:93` 与 `transport/transport.go:163`，对象是它按顺序追加的中间消息（`transport/transport.go:142`、`transport/transport.go:149`、`transport/transport.go:157`）。

新增 `transport/context.go`，从同一份列表算出逐 agent 的占用：

```go
// contextUsageOf returns the context occupancy of every agent that spoke.
//
// The baseline is each agent's last assistant message carrying usage: the
// endpoint reported the size of that request, and the next request has to
// carry all of it.
func contextUsageOf(msgs []*schema.Message, contexts map[string]contract.AgentContext) []contract.ContextUsage
```

两条性质要紧：

- **这个列表是有序的。** 中间消息随事件到达而追加，所以「agent A 的最后一条消息」是从尾部往回走，不是搜索。
- **流式也留得住用量。** 流式的 assistant 消息是分片的拼接（`message.ConcatChunks`，`message/variant.go:74`），而 eino 的 `ConcatMessages` 会保留它合并的分片上的用量。所以流式一轮报出的数字与非流式一致。

结果挂到 `transport.Result` 上，与 `Usage` 并排（`transport/transport.go:29`）。

### 4.3 窗口来自 builder，而不是 API 层

API 层不许重推某个 agent 跑在哪个模型上 —— 那是 builder 的知识，仓库的规矩也是这么写的：生效工具表要问 `agent.Builder`，不许自己重算。模型同理。

所以 `agentkit/build/introspect.go` —— 已经负责回答 `EffectiveTools`（`agentkit/build/introspect.go:47`）的那个文件 —— 再加一个查询：

```go
// Contexts reports the context window of every agent's model.
//
// The builder owns the model-ref resolution (build.Resolve) and the
// per-agent model override, so the API layer asks here instead of
// re-deriving it.
func (b *Builder) Contexts() map[string]contract.AgentContext
```

API 层像注入工具源那样注入它（`SetToolsSource`，`api/v2_introspect.go:73`），走一个窄接口：

```go
// ContextSource reports what each agent's context allows.
type ContextSource interface {
	Contexts() map[string]contract.AgentContext
}
```

并在调用点（`api/v2_run.go:150`）转成 transport 的入参：

```go
res, streamErr := transport.Stream(stream, turn, transport.Options{
	RunID:     runID,
	RootAgent: root,
	Contexts:  s.contexts(),
})
```

`transport.Options` 多一个字段 `Contexts map[string]contract.AgentContext`。transport 仍然不碰配置 —— 它只把 token 数变成比例。

### 4.4 实时通道：一个 `CUSTOM` 事件

占用在**每次模型调用之后**都会变，所以不能等到运行结束。机制现成：`events.NewCustomEvent` 带一个 value，模型重试提示就是这么发的（`transport/transport.go:109`）。

事件在带用量的 assistant 消息归档之后立刻发出 —— 也就是 transport 追加中间消息的那两处（流式消息 `transport/transport.go:149`，完整消息 `transport/transport.go:157`）：

```json
{
  "type": "CUSTOM",
  "name": "context_usage",
  "value": {
    "agent": "main",
    "provider": "deepseek",
    "model": "deepseek-flash",
    "used_tokens": 18422,
    "context_window": 131072,
    "summarize_at": 104858,
    "updated_at": "2026-10-29T13:04:11.512Z"
  }
}
```

每个 agent 每次模型调用一个事件。载荷是该 agent 的完整快照 —— 客户端是**替换**而不是合并，所以丢一个事件不会留下错数字。

前端本来就会解析 `CUSTOM` 事件（`web/src/lib/events.ts:317`，`model_retry` 在 `web/src/lib/events.ts:318`），所以解析侧是加法，不是新机制。

### 4.5 历史通道：会话视图上的 `usage.context`

刷新页面不该把仪表盘清空。会话视图本来就在报会话级用量（`api/v2_sessions.go:283`），逐 agent 的占用就放在它旁边：

```json
"usage": {
  "promptTokens": 18422,
  "completionTokens": 931,
  "totalTokens": 19353,
  "byAgent": { "main": { "...": "..." } },
  "context": {
    "main": {
      "agent": "main",
      "used_tokens": 18422,
      "context_window": 131072,
      "summarize_at": 104858,
      "updated_at": "2026-10-29T13:04:11.512Z"
    }
  }
}
```

历史用与实时路径**同一个函数**算：顺着记录走一遍，取每个 agent 最后一条带用量的 assistant 消息。记录里带着用量（`storage/filestore/transcript.go:159`），所以刷新后的数字就是实时路径收尾时的那个数字。

`usage.byAgent` 保持原义 —— 会话**累计**开销的拆分。`usage.context` 是另一回事：**时点**占用。两者并排，容易混淆；§5.4 让界面把它们分开。

### 4.6 agent 视图认得窗口了

前端需要知道自己对话的那个 agent 的窗口。`modelView`（`api/v2_introspect.go:77`）加一个字段：

```go
type modelView struct {
	Provider           string   `json:"provider"`
	Model              string   `json:"model"`
	Reasoning          string   `json:"reasoning"`
	ReasoningAvailable []string `json:"reasoningAvailable"`
	ReasoningDefault   string   `json:"reasoningDefault"`
	// ContextWindow is the model's context window. Zero means the user did
	// not configure one.
	ContextWindow int `json:"contextWindow"`
}
```

`modelOf` 本来就通过 `build.Resolve` 解析模型（`api/v2_introspect.go:197`），窗口只是一个字段的距离。

### 4.7 可选：AG-UI 标准 `usage[]`

AG-UI 的 `RunFinishedEvent` 带一个标准 `Usage []TokenUsage` 字段，由 `events.WithUsage` 填（`adk` SDK：`pkg/core/events/run_events.go:205`，类型在 `pkg/core/events/token_usage.go`）。上游设计记录把它列为**未实现** —— 「SDK 的 `events.WithUsage` 在本仓库零调用」（上游 `docs/api-v2-design.md` §13.3）。

这件事值得做，因为它是标准通道，而且它是**逐 (provider, model)** 的 —— 与逐 agent 的占用不是一个轴。两者并存：`usage[]` 管厂商记账，`context_usage` 管占用。它不是仪表盘的必要条件，所以单独排期（§8）。

★ 留给实现者的一条提醒：前端现在读的是 `outcome.result.usage`（`web/src/lib/events.ts:355`），而 AG-UI 的用量在事件的**顶层** `usage` 数组里。读 `outcome.result.usage` 什么也读不到。

---

## 5. 前端

`AGENTS.md` 里的前端规矩照样适用：逻辑要落在纯函数层（`lib/sse.ts`、`lib/events.ts`、`lib/store/*`），selector 必须返回引用稳定的值。

### 5.1 类型与纯 reducer

`web/src/lib/types.ts` 新增：

```ts
export interface ContextUsage {
  agent: string
  provider?: string
  model?: string
  usedTokens: number
  contextWindow: number
  summarizeAt?: number
  summarized?: number
  updatedAt?: string
}

export interface ConversationState {
  // …existing fields…
  context?: Record<string, ContextUsage>
}
```

reducer 在 `model_retry` 旁边处理新事件（`web/src/lib/events.ts:317`）：

```ts
case "CUSTOM": {
  if (event.name === "model_retry") { /* …existing… */ }
  if (event.name === "context_usage") return onContextUsage(state, event)
  return state
}
```

`onContextUsage` 替换该 agent 的那一项，其余不动。map 是替换不是原地改 —— store 的快照按引用比较。

### 5.2 输入框下方的元信息行

`web/src/components/composer-meta.tsx` 渲染 agent、模型与推理档位（`:34`、`:46`–`:68`）。占用加在这一行，排在推理档位之后：

```
Agent: main · deepseek/deepseek-flash · Reasoning: off · Context: ≈18.4k / 128k (14 %)
```

- 窗口已知时是 `used / window`，未知时是 `≈18.4k tokens`。
- 条是数字下面一条细线；到窗口的 80 % 转琥珀色，95 % 转红。
- tooltip 写明口径 —— *「最近一次发给这个 agent 的请求的 token 数」* —— 没有口径的上下文仪表就是个谜。

缩写复用 `formatTokens`（`web/src/lib/agents.ts:15`），不新增格式化器。

### 5.3 按 agent 的过滤器

`web/src/components/agent-filter.tsx` 列出说话的 agent，每个带着它的**累计**开销（`web/src/components/agent-filter.tsx:35`）。占用才是更能指导行动的数字，而且它是另一个量，所以 chip 上两个都带：

```
main 18.4k/128k   coder 6.1k/128k
```

chip 的 `title`（`web/src/components/agent-filter.tsx:62`）说明哪个是哪个。累计总量继续保留 —— 它是「这个 agent 花了多少」的数字，也正是会话级 `usage.byAgent` 报的那个。

### 5.4 把两个量分开

`usage.byAgent[agent].totalTokens` 与 `context[agent].usedTokens` **不是**同一个数字，绝不能渲染得像同一个：

| | `usage.byAgent` | `context` |
|---|---|---|
| 含义 | 这个 agent 一共消耗了多少 | 这个 agent 的上下文现在有多满 |
| 累加自 | 整个会话 | 一次请求 |
| 什么时候会掉 | —— | 会话被压缩，或换新会话 |

有一条测试断言它们渲染在不同的标签下，这样以后的改动不会悄悄地把它俩换掉。

### 5.5 store

`web/src/lib/store/selectors.ts` 在 `selectActiveUsage`（`:70`）旁边加一个 selector：

```ts
export function selectActiveContext(s: StoreState): Record<string, ContextUsage> | undefined {
  return s.activeId ? s.sessions[s.activeId]?.convo?.context : undefined
}
```

它返回的就是 store 自己持有的那个 map —— 引用稳定，`Object.is` 判定不变，`AGENTS.md` 里「zustand」那条无限重渲染的坑就烧不起来。全程不写 `?? []`：没有就是 `undefined`，组件把它当成「还什么都不知道」。

---

## 6. 配置

不需要新增任何东西。`context_window` 本来就是模型字段，模板里本来就有。

等 §7 落地时，有一处值得加：声明摘要阈值的地方。它该挨着窗口，在同一个模型块里，因为两者是同一类事实：

```yaml
models:
  - id: deepseek-flash
    context_window: 131072
    summarize_at: 104858      # 窗口的 80 %
```

在那之前 `summarizeAt` 为零，第二个刻度不显示。

---

## 7. 第二阶段 —— 接入摘要中间件

这个仪表盘存在的意义是回答「这段对话什么时候会撑爆」。诚实的答案是「当请求塞不进窗口的时候」—— 而防止这件事的机制 eino 已经带了。接上它是另一件更大的事，而且有一个必须正面设计的坑。

### 7.1 挂在哪里

`deep.Config.Handlers` 接受 `adk.TypedChatModelAgentMiddleware`（`adk/prebuilt/deep/deep.go:97`），而摘要中间件正是它（`adk/middlewares/summarization/summarization.go:272`）。openheron 已经把自己的闸门处理器追加在那里（`agentkit/build/assemble.go:110`，`agentkit/build/gate.go:105`）。

阈值来自与窗口同一个模型块（§6），摘要模型用同一厂商的另一个模型。

### 7.2 坑：压缩活不过一轮

openheron 每轮都从持久化记录重建模型输入（`agentkit/run/loop.go:69`，`agentkit/run/input.go:44`）。而摘要中间件改写的是**内存里**的 agent 状态（`BeforeModelRewriteState` 返回新状态，`adk/middlewares/summarization/summarization.go:335`）。轮一结束，那份状态就没了，下一轮照旧把完整记录再发一遍。

所以照原样挂上去，中间件只会压**轮内**的状态 —— 这对一轮里很长的工具循环有用，对一段长对话没用。届时仪表盘会显示上下文在每轮开头又爬回阈值，那不是解释，是困惑。

要让压缩活得下去，摘要必须写回会话：

1. 中间件的 `Finalize`（或它之后的一个 handler）把摘要作为带标记的消息写进会话记录；
2. 轮输入的构造丢掉被摘要替掉的那批消息；
3. 压缩次数与摘要自身的大小都要记下来，仪表盘才能显示「已压缩 3 次，省下 4.1k token」。

第 2 步最是侵入性的：`buildInput` 现在是把整段历史发出去（`agentkit/run/input.go:44`），被压缩过的会话不能再把压缩前的消息发一遍。所以记录里需要一个标记说明*摘要从哪开始*，`buildInput` 得认这个标记。

★ 这就是为什么中间件排在第二阶段、仪表盘排在第一阶段：没有压缩时仪表盘依然有用（它告诉用户对话变长了），而压缩只有在结果能持久化之后才有用。

### 7.3 中间件能喂给仪表盘什么

开着 `EmitInternalEvents` 时中间件会发内部事件（`adk/middlewares/summarization/consts.go:22`–`:24`）：`before_summarize`、`after_summarize`、`generate_summary`。transport 把它们转成 `CUSTOM` 事件透传，界面就能多知道两件老实事：

- 这个 agent 的历史被压缩了**几次**；
- 压缩**省下**了多少 token。

---

## 8. 交付节奏

| 阶段 | 内容 | 规模 |
|---|---|---|
| **P0** | `contract.ContextUsage` · `transport/context.go` · `Builder.Contexts()` · 会话视图上的 `usage.context` · agent 视图上的 `model.contextWindow` · 输入框元信息行 · 单元测试 | 刷新后仍然正确的仪表盘 |
| **P1** | 实时路径的 `CUSTOM "context_usage"` · reducer · 逐 agent 的 chip · Playwright spec | 运行中会动的仪表盘 |
| **P2** | AG-UI 标准 `RUN_FINISHED.usage[]`（`events.WithUsage`） | 厂商记账通道，以及上游记录里的遗留项 |
| **P3** | 挂上摘要中间件 · 摘要写回 · 显示压缩次数 | 活得过一轮的压缩 |

P0 与 P1 是这个功能本身。P2 是标准层面的清理。P3 是这个功能天然的后继，也是唯一会动会话格式的阶段。

---

## 9. 测试

| 层 | 证明什么 |
|---|---|
| Go —— `transport` | 占用是每个 agent **最后一次**调用的数字，不是总和；三次调用的轮次报第三次；没说过话的 agent 不出现 |
| Go —— `api` | 同一份记录上，会话视图的 `usage.context` 与实时事件一致；模型没声明窗口时 `model.contextWindow` 为零 |
| Go —— `build` | `Contexts()` 报的是每个 agent **实际**所用模型的窗口，包括自带 `model:` 的子 agent |
| `bun run test:unit` | reducer 替换一个 agent 的项而不动其余；窗口未知时不画条、也不显示「0 %」 |
| Playwright | 跑完一轮后输入框元信息行显示占用；逐 agent 的 chip 各显示一个占用 |

★ 单元测试必须包含**厂商没回报用量**这个用例 —— `toUsageView` 对零用量返回 nil（`api/v2_types.go:243`），此时仪表盘必须是空的，而不是「0 tokens」。

---

## 10. 风险与取舍

| 风险 | 对策 |
|---|---|
| 这个数字在两次调用之间是**下界** —— 最后一次调用之后产出的工具结果还没算进去 | 界面上标 `≈`；tooltip 写明口径 |
| 不同厂商的计数口径不同（工具、推理 token） | 数值就是厂商自己的数，标注为估算，绝不当精确值 |
| 窗口通常没配（`context_window: 0`） | 界面退化成绝对 token 数，并说明窗口未配置 |
| 多一条通道（`usage.context`）挨着 `usage.byAgent`，容易混 | 两者分开记载（§5.4），并有测试守住标签不互换 |
| 活不过一轮的压缩会让仪表盘看着像坏了 | §7.2 —— 压缩与写回要么一起落地，要么都不做 |
| API 层重推模型会与实际装配的 agent 漂移 | 窗口问 builder（§4.3），绝不重算 |

---

## 11. 不在范围内

- **预估还没发出去的那次请求的上下文。** 事前估算需要分词器；eino 自己的默认计数器按四字符一个 token 估（`adk/middlewares/summarization/summarization.go:476`），那个精度够触发一次压缩，不够当数字展示。
- **预测模型什么时候会拒绝。** 那是厂商的决定，拒绝会以错误形式抵达。
- **成本核算。** token 数不是钱，价格在厂商那边。
- **改会话格式。** 只有 P3 会碰，而且它是独立的一步。
