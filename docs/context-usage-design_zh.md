# 上下文使用预估 —— 设计方案

> **状态**：已实现。占用仪表盘（§2、§4、§5）、压缩及其持久化、以及压缩标记的客户端渲染
> （§7）都在代码里。
> **范围**：本仓库的代码面 —— Go 后端与 `web/` 前端。
> 配套阅读：[architecture.md](architecture.md)（代码怎么分层）、
> [developer-manual.md](developer-manual.md)（怎么改代码）、
> [user-manual.md](user-manual.md)（怎么用）。

> English version: [context-usage-design.md](context-usage-design.md)

---

## 0. 一句话概括

openheron 手里已经握有两个足以回答「这个会话是不是快撑爆上下文了？」的事实 —— 模型的上下文窗口（`llm/*.yaml` 里的 `context_window`）与厂商回报的 token 数（每条 assistant 消息上的 `ResponseMeta.Usage`）。两者都没有出现在任何界面上；而**已经**暴露出去的用量（`GET /v2/sessions/{id}`）是整个会话的**累计总和**，回答不了这个问题：一个会话可以总量很大而上下文几乎为空，也可以总量不大而上下文快满了。

本设计新增一个数字 —— 每个 agent 的**上下文占用** —— 它的基线与 eino 自带的摘要中间件所数的是同一个基线，在运行过程中就随 SSE 推给前端，最终渲染在输入框下方的元信息行与按 agent 展开的过滤器里。

它也把仪表盘打开的那个口子接上：上下文真的塞不下时，对话会被**压缩，且压缩结果被持久化**（§7）—— 随轮次一起消失的摘要，等于对话一点没变短。

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

每一轮都从持久化的会话历史重建模型输入：`buildInput` 拼一份临时的系统提示、历史与本轮 query（`agentkit/run/input.go:44`），循环把它与会话消息一起喂进去（`agentkit/run/loop.go:71`）。

两个推论：

- 会话的上下文**就是**它的记录。仓库里没有任何压缩、开窗或淘汰逻辑。
- eino 摘要中间件若改写状态，那份状态**在轮末就被丢弃** —— 下一轮又从记录重来。这是 §7 里最要紧的一条约束，也是压缩结果必须写回会话的原因。

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

`summarizeAt` 为零表示这个 Agent 永不压缩 —— 它自己写了 `0`，或者它的模型没声明窗口可量 —— 此时第二个刻度干脆不显示。

### 2.3 窗口未知的时候比想象中多

`context_window: 0` 的意思是*没配置* —— 通用模板出厂就是这样（`initcfg/templates/llm/_generic.yaml:14`）。窗口是用户声明的逐模型事实，openheron 不猜，界面也不该猜：

- 窗口未知 → 显示 `≈12k tokens`，不画条，tooltip 说明窗口未配置；
- 窗口已知 → 显示 `12k / 128k (10 %)`。

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
	// Provider and Model name the model the agent runs on.
	Provider string
	Model    string
}
```

结构体只带数字。比例由渲染方自己算 —— 前端本来就自己格式化 token 数（`formatTokens`，`web/src/lib/agents.ts:15`），把一条百分比规则抄成两门语言，正是它们分叉的开始。

### 4.2 计算放在 transport

逐次调用的用量本来就是 transport 的：两次 `res.Usage = aggregateUsage(res.Intermediates)` 在 `transport/transport.go:105` 与 `transport/transport.go:189`，对象是它按顺序追加的中间消息（`transport/transport.go:160`、`transport/transport.go:167`、`transport/transport.go:175`）。

新增 `transport/context.go`，从同一份列表算出逐 agent 的占用：

```go
// ContextUsageOf returns the context occupancy of every agent that spoke.
//
// The baseline is each agent's last assistant message carrying usage: the
// endpoint reported the size of that request, and the next request has to
// carry all of it.
//
// The live path and the history path both call this, so the number on screen
// and the number after a refresh cannot disagree.
func ContextUsageOf(msgs []*schema.Message, contexts map[string]contract.AgentContext) map[string]contract.ContextUsage
```

两条性质要紧：

- **这个列表是有序的。** 中间消息随事件到达而追加，所以「agent A 的最后一条消息」是从尾部往回走，不是搜索。
- **流式也留得住用量。** 流式的 assistant 消息是分片的拼接（`message.ConcatChunks`，`message/variant.go:74`），而 eino 的 `ConcatMessages` 会保留它合并的分片上的用量。所以流式一轮报出的数字与非流式一致。

结果挂到 `transport.Result` 上，与 `Usage` 并排（`transport/transport.go:35`）。

### 4.3 窗口来自 builder，而不是 API 层

API 层不许重推某个 agent 跑在哪个模型上 —— 那是 builder 的知识，仓库的规矩也是这么写的：生效工具表要问 `agent.Builder`，不许自己重算。模型同理。

所以 `agentkit/build/context.go` —— 与已经负责回答 `EffectiveTools`（`agentkit/build/introspect.go:47`）的那个文件作伴 —— 再加一个查询：

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

占用在**每次模型调用之后**都会变，所以不能等到运行结束。机制现成：`events.NewCustomEvent` 带一个 value，模型重试提示就是这么发的（`transport/transport.go:121`）。

事件在带用量的 assistant 消息归档之后立刻发出 —— 也就是 transport 追加中间消息的那两处（流式消息 `transport/transport.go:167`，完整消息 `transport/transport.go:175`）：

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
Agent: main · deepseek/deepseek-flash · Reasoning: off · Context: ≈18k / 131k (14 %)
```

- 窗口已知时是 `used / window`，未知时是 `≈18k tokens`。
- 条是数字下面一条细线；到窗口的 80 % 转琥珀色，95 % 转红。
- tooltip 写明口径 —— *「最近一次发给这个 agent 的请求的 token 数」* —— 没有口径的上下文仪表就是个谜。

缩写复用 `formatTokens`（`web/src/lib/agents.ts:15`），不新增格式化器。

### 5.3 按 agent 的过滤器

`web/src/components/agent-filter.tsx` 列出说话的 agent。占用是更能指导行动的数字，而**累计花费**是另一个量 —— 所以 chip 上**两个都带**，占用在前：

```
main 18k/131k 600   coder 6.1k/8.2k 600
```

chip 的 `title` 说清它们各是什么，两个数因此不会混淆：

```
main — 600 tokens
Context: ≈18k / 131k
```

累计总量继续留在 chip 上 —— 它是「这个 agent 花了多少」的数字，也正是会话级 `usage.byAgent` 报的那个。

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

`context_window` 本来就是模型字段，模板里本来就有。摘要阈值是 **Agent** 字段 —— 窗口是模型的，
阈值是这个 Agent 自己「把窗口填到多少就压缩」的策略：

```yaml
# agents/main.md
---
model: deepseek/deepseek-flash
summarize_at: 80%             # 模型 context_window 的份额
---
```

不写就是窗口的 80 % —— 三种写法与「窗口未知时怎么办」见 §7.8。

---

## 7. 持久化压缩后的对话

仪表盘（§4、§5）回答的是「这段对话什么时候快塞不下了」。这一节是另一半：**真塞不下时怎么办**。压缩出来的结果必须活过产生它的那一轮，否则对话的长度一点没变。

**这一部分已经实现。** 中间件、自带的 finalizer、事件的接住、落盘、会话的两个视图、以及压缩标记的客户端渲染（§7.7）都在代码里：压缩消息带着 `compaction` 元数据标记抵达浏览器，浏览器把它画成分隔线，而不是一个聊天气泡。

### 7.1 那个坑

eino 的摘要中间件改写的是 agent 的**内存状态**（`BeforeModelRewriteState` → `Summarize`，`adk/middlewares/summarization/summarization.go:349`，`:281`）。而 openheron 每轮都从持久化记录重建模型输入（`agentkit/run/loop.go:71`，`agentkit/run/input.go:44`）。改写出来的状态随轮次一起没了，下一轮照旧把完整记录再发一遍。

照原样挂上去，中间件只会压**轮内**的状态 —— 这对一轮里很长的工具循环有用，对一段长对话没用。更糟的是仪表盘会在每轮开头又爬回阈值，那看起来像 bug，而不像事实。

所以压缩结果必须**持久化**，之后的会话还得换个读法。这就是这一节的全部内容。

### 7.2 eino 交出来的东西

`Summarize` 按顺序走四步（`adk/middlewares/summarization/summarization.go:281`）：

1. 发 `before_summarize`，带上它即将摘要的消息（`:293`）；
2. 生成摘要（`:493`）；
3. 把原始消息与原始摘要交给 **finalizer**，拿回**替换后的历史**（`:307`）；
4. 用前后两个状态调 `Callback`（`:315`），再发 `after_summarize`，带上替换后的历史（`:322`）。

其中三个是 openheron 的入口：

| 钩子 | 携带什么 | openheron 拿它做什么 |
|---|---|---|
| `Config.Finalize` | 原始消息与原始摘要 | **塑形**替换后的历史 |
| `Config.Callback` | 前后两个状态，同步调用 | **持久化**它 |
| `after_summarize` 事件 | 替换后的历史（`After.Messages`） | **界面**事件 |

默认 finalizer 返回 `[system messages…, summary]`（`adk/middlewares/summarization/finalizer_builder.go:37`）。摘要是**一条消息**，近期的用户原话被拼进了它的正文（`postProcessSummary`，`adk/middlewares/summarization/summarization.go:689`）。

system 消息是 agent 自己的，而 openheron 的上下文提示按设计就是临时的（`agentkit/run/input.go:42`）—— 每轮重建，从不落盘。所以**openheron 自带 finalizer**，只返回摘要那一条。

摘要只覆盖上下文消息：中间件先把 system 消息摘出去（`splitSystemAndContextMsgs`，`adk/middlewares/summarization/summarization.go:506`）。

### 7.3 落盘的是什么

替换后的历史是**一条消息**。就这一个事实，让持久化变得简单。

**压缩消息**像别的消息一样追加到 `session.jsonl`（`storage/filestore/transcript.go:172`）：一行，一条 `schema.Message`。记录仍然是只追加的（`storage/storage.go:40`），格式版本不动（`formatVersion`，`storage/filestore/session.go:501`），既有读法照旧 —— 这个功能之前写的会话读起来一模一样。

**边界**就搭在这条消息上，放在 `Extra` map 里 —— 与 openheron 已经打戳的地方相同（`message/provenance.go:9`）：

```go
// ExtraCompaction marks the message that replaced a stretch of history.
const ExtraCompaction = "openheron_compaction"

// Compaction is what a compacted message carries.
type Compaction struct {
	// Superseded is the number of transcript messages this message replaced.
	Superseded int `json:"superseded"`
	// SavedTokens is how many tokens the compaction removed from the next
	// request, as measured by the counter that triggered it.
	SavedTokens int `json:"saved_tokens"`
	// At is when the compaction happened.
	At time.Time `json:"at"`
}
```

于是记录成了**唯一真相源**：有效历史由「最后一条带标记的消息」推导出来。不动 `state.json`，不多一个要同步的文件，崩了之后也没有要对账的东西。

### 7.4 有效历史

```
有效历史 = 最后一条压缩消息，以及它之后的全部消息
```

会话本来就有一个读法 —— 读整份记录。缺的是**要有效视图**这件事，所以给这个读法加参数，而不是再加一个方法：

```go
// Messages returns the session's messages.
//
// effective selects the view:
//
//	false (the default) — the whole transcript: the audit trail, and what the
//	  UI renders.
//	true — the effective history: the last compaction message and everything
//	  after it, which is what the agent's context is built from.
Messages(effective bool) []*schema.Message
```

★ Go 没有默认参数，所以默认靠零值表达：`false` 就是「全部」。参数在每个调用点写出来，而不是藏在另一个方法后面 —— 读 `sess.Messages(true)` 的人一眼知道要的是哪个视图，编译器也会逼每个调用方表态。

生产代码里有两个调用点，要的视图不一样：

| 调用点 | 视图 | 为什么 |
|---|---|---|
| 运行循环（`agentkit/run/loop.go:71`） | `sess.Messages(true)` | agent 的上下文就是有效历史 |
| 消息视图（`api/v2_sessions.go:274`） | `sess.Messages(false)` | 界面显示整份记录 —— 被取代的消息与压缩标记都在里面 |

实现还在原处（`storage/filestore/session.go:229`）：有效视图就是记录里从最后一条压缩标记开始的那一段后缀。
HTTP 这一层不动：消息接口继续返回整份记录，因为界面渲染的就是它。若将来某个客户端要 agent 那个视图，`GET /v2/sessions/{id}/messages?effective=1` 是自然的扩展。


```go
func (fs *FileSession) Messages(effective bool) []*schema.Message {
	// … 读出记录、拷出来 …
	if effective {
		out = effectiveView(out)
	}
	return out
}
```

`Len()`（`storage/filestore/session.go:248`）继续数整份记录：它喂给侧边栏的消息条数，那是关于**记录**的事实，不是关于上下文的事实。接口跟着改（`storage/storage.go:48`），测试替身也一样（`agentkit/run/fakes_test.go:90`）。

原有的过滤仍然叠在上面：根 agent 的上下文不含子 agent 的消息（`agentkit/run/input.go:48`），而子 agent 的上下文活在自己的那一轮里 —— 所以**只有根 agent 的上下文会被压缩**。子 agent 的消息带自己的标记（`message.IsSubagent`，`message/provenance.go:106`），在构造上下文之前就被丢掉了。

### 7.5 顺序，以及崩了会怎样

压缩消息写在**本轮消息归档之前**（`agentkit/run/loop.go:150`）。磁盘上的顺序是：

```
session.jsonl:  … 旧历史 … │ 压缩消息 │ 本轮的消息 …
```

这正是语义顺序：压缩总结的是它之前的一切，本轮的消息在它之后。

追加之后、下一次读之前崩了，代价为零：压缩消息已在盘上，下一轮读它当边界，历史就是短的。**追加之前**崩了，代价也为零：历史还是长的，下一轮再压一次。

★ 顺序只在一边要紧。如果**先**记边界、**后**追加消息，中途崩了就留下一个指向自身末尾之外的记录 —— 有效历史会变空，agent 一觉醒来什么都不记得。先追加则造不出这种状态。

### 7.6 摘要不能丢的东西

| 东西 | 住在哪 | 压缩后还在吗 |
|---|---|---|
| 用户自己的话 | 摘要正文 —— eino 把近期的用户消息拼回去，最多约 30k token（`preserveUserMsgsMaxTokens`，`adk/middlewares/summarization/consts.go:29`） | 在，但受预算限制 |
| 系统提示 | 每轮重建（`agentkit/run/input.go:44`） | 在 |
| 工具 schema | 每次调用都重发 | 在 |
| 审批审计日志 | `state.json`（`storage/filestore/session.go:483`），不在消息日志里 | 在，没被碰过 |
| 原始消息 | 记录里，被取代但没被删 | 在 —— 还在盘上 |

最后一行正是记录不被重写的原因：**压缩不是删除**。旧消息在记录里、在界面里都还读得到，标记说明切口在哪儿。

若希望模型能回读原始对话，`Config.TranscriptFilePath` 就是那个钩子：eino 会把路径追加进摘要（`adk/middlewares/summarization/summarization.go:706`），摘要因此能把模型指向会话自己的记录。这只在摘要模型能读文件时才成立。

### 7.7 界面显示什么

四样东西，彼此不同：

1. **消息列表里的一条分隔线。** 压缩消息不是聊天气泡。消息视图给它标上一个 `compaction` 元数据项（`api/v2_convert.go:63`），前端把它画成分隔线：*「12 earlier messages · compacted into a summary · ≈62k tokens saved」*（界面文案是英文）。标记在历史水合时被读出来（`web/src/lib/store/history.ts:14`），挂在消息上（`web/src/lib/types.ts:56`），由 `web/src/components/compaction-divider.tsx` 渲染，消息列表把它放在气泡该在的位置上（`web/src/components/message-list.tsx:101`）。被取代的消息留在它上面、仍然可见 —— 用户照样能读到切口之前说了什么。
2. **仪表盘的第二根针。** `context[agent].summarized` 数压缩的次数（`api/v2_sessions.go:326`）。上一次压缩省下了多少，写在分隔线上而不在仪表盘上：仪表盘回答「上下文现在有多满」，分隔线回答「上一次切口省了多少」。
3. **一个实时事件。** 压缩发生在轮内，所以界面从事件流里知道它：transport 把 `after_summarize` 转成 `CUSTOM "context_compacted"` 事件（`transport/transport.go:590`），reducer 当场把边界插进列表（`web/src/lib/events.ts:412`）。★ 实时事件只带**事实、agent 与时间** —— **不带数字**。数字是拿记录量出来的，而记录由运行循环在轮次结束时写入；transport 手里没有记录可量。所以实时分隔线写的是*「earlier messages · compacted into a summary」*，数字要等下一次加载 —— 那时标记跟着记录一起回来了。
4. **一个手动命令。** `/compact` 随叫随压：`POST /v2/sessions/{id}/compact` 摘要有效历史（`agentkit/run/compact.go` 的 `run.Compact`）、盖上同一个标记，并把数字报回来 —— 所以手动压出来的分隔线当场就带数字。轮次在跑或审批在等时，命令不出现；上下文小到不值得压时，它拒绝（`run.MinCompactMessages`、`run.MinCompactTokens`）：摘要带着前言，短对话压完可能反而更长。

### 7.8 配置

阈值属于 **Agent**，不属于模型：窗口是模型的，阈值是这个 Agent 自己「填到多少就压缩」的
策略。

```yaml
# agents/main.md
---
model: deepseek/deepseek-flash
summarize_at: 80%             # 模型 context_window 的份额 —— 触发线
---
```

三种写法，在 `spec.SummarizeAt(threshold, window)`（`agentkit/spec/summarize.go`）里解析：

| 写法 | 阈值 |
|---|---|
| `80%` | 该 Agent 实际所用模型窗口的 80 % |
| `104858` | 绝对 token 数 |
| `0`（或 `off`） | 永不 |
| *(不写)* | 窗口的 80 % —— **默认值** |

`Trigger.ContextTokens` 就是中间件拿去比的那个数（`adk/middlewares/summarization/summarization.go:373`）；上游文档建议设为窗口的 80–90%，默认值取 80 % 就是这个缘故。

★ **百分比需要窗口。** `context_window: 0` 意味着*没配置*，而百分比的百分比是零：那个 Agent
永不压缩，spec 包在加载配置时把它记进 `deferred`（`noticeCompactionWindows`，
`agentkit/spec/load.go`）。绝对的 token 数不需要窗口。

★ **模型档案里的 `summarize_at` 已废弃。** 它照常解析 —— 直接删掉会让所有老 `llm/*.yaml` 启动
即失败，因为解码器是 `KnownFields` 严格的 —— 但没有任何地方读它，spec 包会指路到 Agent 上的
那个设置。

摘要模型默认就是 agent 自己的模型，除非配置另指一个 —— `Config.Model` 接它（`adk/middlewares/summarization/summarization.go:58`）。

★ 读仪表盘第二个刻度时要记住一条：eino 的默认计数器把工具 schema **加在**基线之上，而基线里本来就有它们（`adk/middlewares/summarization/summarization.go:420`）。所以触发线会早一点到 —— 偏早是安全的偏法。

---

## 8. 交付节奏

| 阶段 | 内容 | 规模 |
|---|---|---|
| **P0** | `contract.ContextUsage` · `transport/context.go` · `Builder.Contexts()` · 会话视图上的 `usage.context` · agent 视图上的 `model.contextWindow` · 输入框元信息行 · 单元测试 | 刷新后仍然正确的仪表盘 |
| **P1** | 实时路径的 `CUSTOM "context_usage"` · reducer · 逐 agent 的 chip · Playwright spec | 运行中会动的仪表盘 |
| **P2** | **压缩及其持久化** —— 挂上中间件 · 自带 finalizer · 追加压缩消息 · 读有效历史 · 渲染标记 | 对话不再变长 —— **第 1-3 步已在代码里，第 4 步（界面上的标记）还没有** |
| **P3** | AG-UI 标准 `RUN_FINISHED.usage[]`（`events.WithUsage`） | 厂商记账通道，以及上游记录里的遗留项 |

P0 与 P1 是仪表盘。P2 是让仪表盘有意义的那部分：没有它，一段长对话只能以一次拒绝收场。P3 是标准层面的清理。

### P2 分步

P2 是唯一会动会话格式的阶段，所以拆成各自都能独立发布的几步：

1. **挂上中间件，先不持久化。** `deep.Config.Handlers` 接受 `adk.TypedChatModelAgentMiddleware`（`adk/prebuilt/deep/deep.go:97`），摘要中间件正是它（`adk/middlewares/summarization/summarization.go:272`）；openheron 已经把自己的闸门处理器追加在那里（`agentkit/build/assemble.go:110`，`agentkit/build/gate.go:105`）。中间件挂上之后，一轮里长得撑爆窗口的活儿能扛住。对话还会变长 —— 还没落盘。
2. **压缩消息。** finalizer 只返回摘要（§7.2），callback 把它带 `ExtraCompaction` 标记追加进记录（§7.3），边界再从它读回来。
3. **有效历史。** `Session.Messages(effective)`，运行循环改问有效视图（§7.4）。到这一步，对话跨轮也不长了。
4. **界面。** 消息列表里的标记、仪表盘上的压缩次数、实时的 `context_compacted` 事件（§7.7）。

★ 第 2 步没有第 3 步就是 bug：压缩消息躺在记录里，而 agent 仍在发完整历史，于是每轮都会再压一次。第 2、3 步一起落地。

---

## 9. 测试

| 层 | 证明什么 |
|---|---|
| Go —— `transport` | 占用是每个 agent **最后一次**调用的数字，不是总和；三次调用的轮次报第三次；没说过话的 agent 不出现 |
| Go —— `api` | 同一份记录上，会话视图的 `usage.context` 与实时事件一致；模型没声明窗口时 `model.contextWindow` 为零；压缩标记带着数字抵达浏览器，而空内容的压缩也不会被丢掉 |
| Go —— `build` | `Contexts()` 报的是每个 agent **实际**所用模型的窗口，包括自带 `model:` 的子 agent；**阈值**是每个 agent 自己的，拿那个窗口量；什么都没写的 agent 拿到 80 %；模型没声明窗口就没有阈值 |
| Go —— `spec` | 阈值三种写法各归各位；写不出来的是启动错误；阈值从 Agent 文件读；模型里的旧字段照常加载并被记下；模型没声明窗口的 Agent 会被记下 |
| Go —— `storage` | 有效历史是最后一条压缩消息及其后的消息；没有压缩标记的会话返回完整记录；被取代的消息仍在盘上 |
| Go —— `agentkit/run` | 轮输入由有效历史构造；轮内发生的压缩**先于**本轮消息落盘 |
| Go —— `transport` | 压缩以 `CUSTOM "context_compacted"` 事件在流上宣告；没压缩过的轮次什么也不宣告；**子 agent** 的压缩不是会话的；替换历史里还有 system 消息时，摘要被单独挑出来 |
| Go —— `run` | 手动压缩写下边界与标记；太小的历史被拒绝；摘要失败什么也不写 |
| Go —— `api` | 手动压缩报出战果；轮次在跑是 409；上下文太小是 400 |
| `bun run test:unit` | reducer 替换一个 agent 的项而不动其余；窗口未知时不画条、也不显示「0 %」；压缩标记渲染成分隔线而不是气泡；实时事件把边界插进列表；轮次在跑时 `/compact` 不出现 |
| Playwright | 跑完一轮后输入框元信息行显示占用；逐 agent 的 chip 各显示一个占用；压缩之后消息列表出现的是分隔线而不是气泡，且此前的消息仍读得到 |

★ 单元测试必须包含**厂商没回报用量**这个用例 —— `toUsageView` 对零用量返回 nil（`api/v2_types.go:243`），此时仪表盘必须是空的，而不是「0 tokens」。

★ 压缩的测试必须包含**重启**用例：压缩 → 关掉会话 → 重新打开 → 有效历史仍然是短的。只活在内存里的压缩，正是这个设计要防的失败。

---

## 10. 风险与取舍

| 风险 | 对策 |
|---|---|
| 这个数字在两次调用之间是**下界** —— 最后一次调用之后产出的工具结果还没算进去 | 界面上标 `≈`；tooltip 写明口径 |
| 不同厂商的计数口径不同（工具、推理 token） | 数值就是厂商自己的数，标注为估算，绝不当精确值 |
| 窗口通常没配（`context_window: 0`） | 界面退化成绝对 token 数，并说明窗口未配置 |
| 多一条通道（`usage.context`）挨着 `usage.byAgent`，容易混 | 两者分开记载（§5.4），并有测试守住标签不互换 |
| 压缩活不过一轮，仪表盘看着像坏了 | §7 —— 压缩消息追加进记录，有效历史从它读出来 |
| 摘要若丢了用户自己的话，会悄悄改变 agent 的行为 | eino 把近期的用户消息拼回摘要（`adk/middlewares/summarization/consts.go:29`）；预算是可调的旋钮，不是常量 |
| 被取代的消息仍在盘上，会话的记录即使压缩后也继续变大 | 压缩缩短的是**上下文**，不是**记录** —— 记录是审计线索；记录的大小与窗口是两回事 |
| API 层重推模型会与实际装配的 agent 漂移 | 窗口问 builder（§4.3），绝不重算 |

---

## 11. 不在范围内

- **预估还没发出去的那次请求的上下文。** 事前估算需要分词器；eino 自己的默认计数器按四字符一个 token 估（`adk/middlewares/summarization/summarization.go:476`），那个精度够触发一次压缩，不够当数字展示。
- **预测模型什么时候会拒绝。** 那是厂商的决定，拒绝会以错误形式抵达。
- **成本核算。** token 数不是钱，价格在厂商那边。
- **压缩子 agent 的上下文。** 子 agent 的上下文活在自己的那一轮里，随轮而死（§7.4）；只有根 agent 的上下文是长久的。
- **改写或删除被取代的消息。** 压缩不是删除（§7.6）；记录原样保留、照样读得到。
- **改记录的格式。** 压缩消息是一条带额外标记的普通消息（§7.3），所以 `formatVersion` 不动，旧会话照旧能读。
