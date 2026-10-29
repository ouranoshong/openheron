# Context Usage Estimate — Design

> **Status**: proposal. Nothing described here is implemented yet.
> **Scope**: this repository's code surface — the Go backend and the `web/` frontend.
> **Companion**: [architecture.md](architecture.md) (how the code is layered),
> [developer-manual.md](developer-manual.md) (how to change the code),
> [user-manual.md](user-manual.md) (how to use it).

> 中文版：[context-usage-design_zh.md](context-usage-design_zh.md)

---

## 0. In one paragraph

openheron already holds two facts that would answer *"is this conversation about to run out of context?"* — the model's context window (`context_window` in `llm/*.yaml`) and the token counts the endpoint reported (`ResponseMeta.Usage` on every assistant message). Neither is shown anywhere, and the usage that *is* exposed (`GET /v2/sessions/{id}`) is a **cumulative total** over the whole session, which does not answer the question: a session can carry a large total and an almost empty context, or a modest total and a nearly full context.

This design adds one number — the **context occupancy** of each agent — computed from the same baseline eino's own summarization middleware counts from, pushed over SSE while the run is still going, and rendered in the composer meta line and in the per-agent filter.

---

## 1. What exists today

### 1.1 The context window is configured, and then ignored

`context_window` is part of the model declaration (`agentkit/spec/decl/decl.go:42`):

```go
ContextWindow int `yaml:"context_window"`
```

The factory templates ship it — `128000` for OpenAI (`initcfg/templates/llm/openai.yaml:16`), `131072` for DeepSeek (`initcfg/templates/llm/deepseek.yaml:15`), and `0` for the generic template, where the user must fill it in (`initcfg/templates/llm/_generic.yaml:14`).

It reaches exactly one place: the provider listing of `GET /v2/config` (`api/v2_introspect.go:276`, filled at `api/v2_introspect.go:364`). **No run-time code reads it.** The agent view that the frontend actually uses (`api/v2_introspect.go:77`, built by `modelOf` at `api/v2_introspect.go:197`) does not carry it, so the browser cannot even learn the window of the agent it is talking to.

### 1.2 Token usage is already on disk — but it is a total, not an occupancy

`aggregateUsage` sums the usage the endpoint reported over the assistant messages of a turn (`transport/usage.go:67`); `sessionUsage` does the same over a whole transcript (`api/v2_sessions.go:299`). The numbers reach the client as `usage` on the session view (`api/v2_types.go:192`, filled at `api/v2_sessions.go:283`).

Every one of those numbers is a **sum**. `usage.totalTokens` for a session with twenty calls is the total of twenty calls — it says nothing about how full the context is right now. The context is not the sum of the session; it is the size of **one** request.

The raw material is per-call and already persisted: each assistant message carries `response_meta.usage` (`schema.Message.ResponseMeta`, `json:"response_meta,omitempty"`), and the transcript writes whole messages as JSON lines (`storage/filestore/transcript.go:159`), so the usage survives the round trip.

### 1.3 The context is rebuilt from the transcript every turn

Each turn rebuilds the model input from the persisted session history: `buildInput` composes a transient system prompt, the history, and the new query (`agentkit/run/input.go:44`), and the loop feeds it the session's messages (`agentkit/run/loop.go:69`).

Two consequences:

- The context of a session **is** its transcript. There is no compaction, no windowing, no eviction anywhere in the repository.
- The state eino's summarization middleware would rewrite is **discarded at the end of the turn** — the next turn starts again from the transcript. This is the single most important constraint for §7.

### 1.4 Every agent has its own context

The root agent's input excludes the sub-agent's messages (`agentkit/run/input.go:48`, `withoutSubagentMessages` at `agentkit/run/input.go:64`), and each sub-agent keeps its own message list inside eino. So "the context" is a **per-agent** quantity — a session of five agents has five contexts, of very different sizes.

openheron already attributes messages to their owner (`message.MessageAgent`, used by `sessionUsageByAgent` at `api/v2_sessions.go:316`), so the per-agent split has a home.

### 1.5 eino ships a summarization middleware — openheron does not use it

eino v0.9.21 has `adk/middlewares/summarization` (introduced in eino v0.8.0; see the [upstream documentation](https://www.cloudwego.io/zh/docs/eino/core_modules/eino_adk/eino_adk_chatmodelagentmiddleware/middleware_summarization/)). It hooks `BeforeModelRewriteState` (`adk/middlewares/summarization/summarization.go:335`) and, before every model call, counts the tokens of the state and compacts the history when the count passes a threshold (`shouldSummarize`, `adk/middlewares/summarization/summarization.go:360`).

Its default counter is the fact this design builds on:

- the baseline is the **total tokens of the last assistant message** (`getAssistantTotalTokens`, `adk/middlewares/summarization/summarization.go:457`, reading `ResponseMeta.Usage.TotalTokens`);
- messages produced after that baseline are estimated at roughly four characters per token (`estimateTokenCount`, `adk/middlewares/summarization/summarization.go:476`);
- the threshold is `Trigger.ContextTokens`, defaulting to 160000 (`getTriggerContextTokens`, `adk/middlewares/summarization/summarization.go:373`); the upstream documentation recommends setting it to 80–90 % of the model's window.

openheron imports none of it: there is no `summarization` reference anywhere in the repository, and `deep.Config.Handlers` — where such a middleware would be mounted (`agentkit/build/assemble.go:110`, `adk/prebuilt/deep/deep.go:97`) — carries only openheron's own gate handlers (`agentkit/build/gate.go:105`).

---

## 2. What the UI should show

### 2.1 The number

For an agent `A`:

```
used(A)      = the token count of A's most recent model call
window(A)    = the context window of the model A runs on
summarizeAt(A) = the token count at which A's history would be compacted
```

`used(A)` is read from the **last assistant message of A that carries usage**:

```
used(A) = Usage.TotalTokens            (fallback: Usage.PromptTokens + Usage.CompletionTokens)
```

`TotalTokens` is the endpoint's own count of *that request* — input plus output (`adk/middlewares/summarization/summarization.go:457` reads exactly this field). It is the right number for three reasons:

1. **It is the size of a request, not a sum.** The next request has to carry everything the last one carried, plus what was produced since — so the last request's total is the context's occupancy, to first order.
2. **It is the provider's own tokenizer**, not a guess. The estimate is only as good as the vendor's accounting.
3. **It is the same baseline eino's summarization middleware counts from.** When the middleware is adopted (§7), the number on screen and the number that decides "compact now" cannot drift apart.

### 2.2 Two scales, not one

A single percentage against the window hides the thing the user can act on. Two are shown:

| Scale | Denominator | Answers |
|---|---|---|
| `used / window` | `context_window` from `llm/*.yaml` | *How much of the model's window is this conversation taking?* |
| `used / summarizeAt` | the summarization threshold | *How far is this conversation from being compacted?* |

`summarizeAt` is zero when no threshold is configured, and the second scale is then simply not shown.

### 2.3 The window is unknown more often than not

`context_window: 0` means *not configured* — the generic template ships exactly that (`initcfg/templates/llm/_generic.yaml:14`). The window is a per-model fact the user declares; openheron does not guess it, and neither should the UI:

- window unknown → show `≈12.3k tokens` and no bar, with a tooltip saying the window is not configured;
- window known → show `12.3k / 128k (10 %)`.

A zero window must never render as "0 %".

### 2.4 Where the number is honestly wrong

The design states the error directions rather than hiding them:

- **Under-counts** the tool results produced *after* the last model call. They are not in any request yet, so they are in no token count. They enter the next call's `prompt_tokens`, at which point the number jumps.
- **Over-counts** nothing, but its *scope* differs from the window: `prompt_tokens` includes the system prompt and the tool schemas, which are part of the window too — so this is the right comparison.
- **Reasoning tokens** are reported by some vendors inside `completion_tokens` and by others beside it. Either way the total is the vendor's, and is used as-is.

### 2.5 Per agent, not per session

The occupancy belongs to an agent. The composer meta line shows the session's own agent (`badge.id`); the agent filter shows every agent that spoke, each with its own occupancy (`web/src/components/agent-filter.tsx:29`).

---

## 3. Data flow

```
llm/*.yaml  context_window ─┐
                            ├─► agentkit/build  Builder.Contexts()  ──► map[agent]AgentContext
decl.Agent.ModelRef ────────┘                                            │
                                                                         ▼
                                              transport.Options.Contexts (api/v2_run.go:150)
                                                                         │
  model call ──► assistant message with ResponseMeta.Usage ──► transport.contextUsageOf
                                                                         │
                          ┌──────────────────────────────────────────────┤
                          ▼                                              ▼
              SSE CUSTOM "context_usage"                      transport.Result.Context
              (per agent, per call)                                    │
                                                                         ▼
                                              GET /v2/sessions/{id} → usage.context
                                                                         │
                                                                         ▼
                                      web/src/lib/events.ts (pure reducer)
                                                                         │
                                                                         ▼
                        web/src/lib/store/selectors.ts → components
```

The window is resolved **once per run**, from the builder that assembled the agents — the API layer must not re-derive which model an agent runs on (§4.3).

---

## 4. Backend

### 4.1 The contract

`contract` is the leaf package both the transport and the API layer already share (`contract.Usage` is defined there at `contract/contract.go:32`). The new type goes next to it:

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

The struct carries numbers only. Ratios are computed by whoever renders them — the frontend already formats token counts itself (`formatTokens`, `web/src/lib/agents.ts:15`), and duplicating a percentage rule in two languages is how they drift.

### 4.2 The computation lives in the transport

The transport already owns the per-call usage: `res.Usage = aggregateUsage(res.Intermediates)` at `transport/transport.go:93` and `transport/transport.go:163`, over the intermediates it appends in order (`transport/transport.go:142`, `transport/transport.go:149`, `transport/transport.go:157`).

A new `transport/context.go` computes the per-agent occupancy from the same list:

```go
// contextUsageOf returns the context occupancy of every agent that spoke.
//
// The baseline is each agent's last assistant message carrying usage: the
// endpoint reported the size of that request, and the next request has to
// carry all of it.
func contextUsageOf(msgs []*schema.Message, contexts map[string]contract.AgentContext) []contract.ContextUsage
```

Two properties matter:

- **The list is ordered.** The intermediates are appended as the events arrive, so "the last message of agent A" is a walk from the end, not a search.
- **Streaming keeps the usage.** The streamed assistant message is the concatenation of the chunks (`message.ConcatChunks`, `message/variant.go:74`), and eino's `ConcatMessages` keeps the usage of the chunks it merges. A streamed turn therefore reports the same numbers as a non-streamed one.

The result joins `transport.Result` next to `Usage` (`transport/transport.go:29`).

### 4.3 The window comes from the builder, not from the API layer

The API layer must not re-derive which model an agent runs on — that is the builder's knowledge, and the repository's rule says so: the effective tool table is asked of `agent.Builder`, never recomputed. The same applies to the model.

So `agentkit/build/introspect.go` — the file that already answers `EffectiveTools` (`agentkit/build/introspect.go:47`) — grows one query:

```go
// Contexts reports the context window of every agent's model.
//
// The builder owns the model-ref resolution (build.Resolve) and the
// per-agent model override, so the API layer asks here instead of
// re-deriving it.
func (b *Builder) Contexts() map[string]contract.AgentContext
```

The API layer injects it the way it injects the tool source (`SetToolsSource`, `api/v2_introspect.go:73`), through a narrow interface:

```go
// ContextSource reports what each agent's context allows.
type ContextSource interface {
	Contexts() map[string]contract.AgentContext
}
```

and turns it into the transport's input at the call site (`api/v2_run.go:150`):

```go
res, streamErr := transport.Stream(stream, turn, transport.Options{
	RunID:     runID,
	RootAgent: root,
	Contexts:  s.contexts(),
})
```

`transport.Options` gains one field, `Contexts map[string]contract.AgentContext`. The transport stays ignorant of the configuration — it only turns token counts into a ratio.

### 4.4 The live channel: a `CUSTOM` event

The occupancy changes after **every model call**, so it must not wait for the end of the run. The transport already has the mechanism: `events.NewCustomEvent` with a value, used for the model-retry notice (`transport/transport.go:109`).

The event is emitted right after an assistant message with usage is archived — the two places where the transport appends intermediates (`transport/transport.go:149` for a streamed message, `transport/transport.go:157` for a complete one):

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

One event per agent per model call. The payload is the whole snapshot for that agent — the client replaces, it does not merge, so a dropped event cannot leave a wrong number behind.

The frontend already parses `CUSTOM` events (`web/src/lib/events.ts:317`, `model_retry` at `web/src/lib/events.ts:318`), so the parsing side is an addition, not a new mechanism.

### 4.5 The history channel: `usage.context` on the session view

A page refresh must not empty the meter. The session view already reports the session-wide usage (`api/v2_sessions.go:283`), so the per-agent occupancy goes next to it:

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

The history is computed with the same function as the live path: walk the transcript, take each agent's last assistant message with usage. The transcript carries the usage (`storage/filestore/transcript.go:159`), so the number after a refresh is the number the live path ended on.

`usage.byAgent` keeps its current meaning — a **cumulative** split of the session's spend. `usage.context` is a different thing: a **point-in-time** occupancy. They sit side by side and must not be confused; §5.4 keeps them apart in the UI.

### 4.6 The agent view learns the window

The frontend needs the window of the agent it is talking to. `modelView` (`api/v2_introspect.go:77`) gains one field:

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

`modelOf` already resolves the model through `build.Resolve` (`api/v2_introspect.go:197`), so the window is one field read away.

### 4.7 Optional: the AG-UI standard `usage[]`

AG-UI's `RunFinishedEvent` carries a standard `Usage []TokenUsage` field, populated by `events.WithUsage` (`adk` SDK: `pkg/core/events/run_events.go:205`, the type in `pkg/core/events/token_usage.go`). The upstream design record lists it as **not implemented** — "the SDK's `events.WithUsage` has zero call sites in this repository" (`docs/api-v2-design.md` §13.3, upstream).

It is worth doing, because it is the standard channel, and it is **per (provider, model)** — which is not the same axis as the per-agent occupancy. The two coexist: `usage[]` for vendor accounting, `context_usage` for occupancy. It is not required for the meter and is therefore scheduled separately (§8).

★ Note for whoever implements it: the frontend currently reads `outcome.result.usage` (`web/src/lib/events.ts:355`), while AG-UI's usage lives in the event's **top-level** `usage` array. Reading `outcome.result.usage` finds nothing.

---

## 5. Frontend

The frontend rules in `AGENTS.md` apply: the logic belongs in the pure layers (`lib/sse.ts`, `lib/events.ts`, `lib/store/*`), and a selector must return a reference-stable value.

### 5.1 Types and the pure reducer

`web/src/lib/types.ts` gains:

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

The reducer handles the new event next to `model_retry` (`web/src/lib/events.ts:317`):

```ts
case "CUSTOM": {
  if (event.name === "model_retry") { /* …existing… */ }
  if (event.name === "context_usage") return onContextUsage(state, event)
  return state
}
```

`onContextUsage` replaces the entry for that one agent and leaves the others alone. The map is replaced, not mutated — the store's snapshots are compared by reference.

### 5.2 The composer meta line

`web/src/components/composer-meta.tsx` renders the agent, the model and the reasoning tier (`:34`, `:46`–`:68`). The occupancy joins that row, after the reasoning tier:

```
Agent: main · deepseek/deepseek-flash · Reasoning: off · Context: ≈18.4k / 128k (14 %)
```

- The number is `used / window` when the window is known, and `≈18.4k tokens` when it is not.
- The bar is a thin strip under the number; it turns amber at 80 % of the window and red at 95 %.
- The tooltip states the definition — *"tokens in the most recent request to this agent"* — because a context meter without its definition is a mystery number.

`formatTokens` (`web/src/lib/agents.ts:15`) is reused for the abbreviation; no new formatter.

### 5.3 The per-agent filter

`web/src/components/agent-filter.tsx` lists the agents that spoke, each with its **cumulative** spend (`web/src/components/agent-filter.tsx:35`). The occupancy is the more actionable number, and it is a different quantity, so the chip carries both:

```
main 18.4k/128k   coder 6.1k/128k
```

The chip's `title` (`web/src/components/agent-filter.tsx:62`) explains which is which. The cumulative total stays available — it is the "how much did this agent cost" number, and it is the one the session-level `usage.byAgent` reports.

### 5.4 Keeping the two quantities apart

`usage.byAgent[agent].totalTokens` and `context[agent].usedTokens` are **not** the same number and must never be rendered as if they were:

| | `usage.byAgent` | `context` |
|---|---|---|
| Meaning | how much this agent has consumed, in total | how full this agent's context is, right now |
| Aggregates over | the whole session | one request |
| Falls when | — | the session is compacted or a new session starts |

A test asserts they are rendered under different labels, so a future edit cannot quietly swap them.

### 5.5 The store

`web/src/lib/store/selectors.ts` gains one selector next to `selectActiveUsage` (`:70`):

```ts
export function selectActiveContext(s: StoreState): Record<string, ContextUsage> | undefined {
  return s.activeId ? s.sessions[s.activeId]?.convo?.context : undefined
}
```

It returns the stored map itself — a reference the store already owns — so `Object.is` stays stable and the infinite-render trap of §"zustand" in `AGENTS.md` cannot fire. No `?? []` anywhere: an absent map is `undefined`, and the components treat it as "nothing known yet".

---

## 6. Configuration

Nothing new is required. `context_window` is already a model field, and the templates already carry it.

One addition is worth making when §7 lands: a place to declare the summarization threshold. It belongs next to the window, in the same model block, because the two are the same kind of fact:

```yaml
models:
  - id: deepseek-flash
    context_window: 131072
    summarize_at: 104858      # 80 % of the window
```

Until then, `summarizeAt` is zero and the second scale is not shown.

---

## 7. Phase 2 — adopting the summarization middleware

The meter exists to answer "when will this conversation break?". The honest answer is "when the request stops fitting in the window" — and eino already ships the mechanism that prevents that. Adopting it is a separate, larger piece of work, and it has one catch that must be designed for explicitly.

### 7.1 Where it would be mounted

`deep.Config.Handlers` takes `adk.TypedChatModelAgentMiddleware` values (`adk/prebuilt/deep/deep.go:97`), which is exactly what the summarization middleware is (`adk/middlewares/summarization/summarization.go:272`). openheron already appends its gate handlers there (`agentkit/build/assemble.go:110`, `agentkit/build/gate.go:105`).

The threshold would come from the same model block as the window (§6), and the summary model would be a second model on the same provider.

### 7.2 The catch: compaction does not survive the turn

openheron rebuilds the model input from the persisted transcript on every turn (`agentkit/run/loop.go:69`, `agentkit/run/input.go:44`). The summarization middleware rewrites the **in-memory** agent state (`BeforeModelRewriteState` returns a new state, `adk/middlewares/summarization/summarization.go:335`). When the turn ends, that state is gone, and the next turn sends the full transcript again.

So, mounted as-is, the middleware would compact **within** a turn — which helps a long tool loop — and do nothing for a long conversation. The meter would then show the context climbing back to the threshold at the start of every turn, which is confusing rather than informative.

For compaction to survive, the summary must be written back into the session:

1. the middleware's `Finalize` (or a handler after it) writes the summary into the session transcript as a marked message;
2. the turn's input builder drops the messages that the summary replaced;
3. the compaction count and the summary's own size are recorded, so the meter can show *"compacted 3 times, 4.1k tokens saved"*.

Step 2 is the invasive one: `buildInput` currently sends the whole history (`agentkit/run/input.go:44`), and a session that has been compacted must not send the pre-compaction messages again. The transcript therefore needs a marker that says *where the summary starts*, and `buildInput` has to honour it.

★ This is why the middleware is phase 2 and the meter is phase 1: the meter is useful without compaction (it tells the user the conversation is getting long), and compaction is useful only once its result is durable.

### 7.3 What the middleware would give the meter

The middleware emits internal events when `EmitInternalEvents` is on (`adk/middlewares/summarization/consts.go:22`–`:24`): `before_summarize`, `after_summarize`, `generate_summary`. The transport would forward them as `CUSTOM` events, which gives the UI two more honest facts:

- **how many times** this agent's history was compacted;
- **how many tokens** the compaction saved.

---

## 8. Delivery plan

| Phase | Content | Size |
|---|---|---|
| **P0** | `contract.ContextUsage` · `transport/context.go` · `Builder.Contexts()` · `usage.context` on the session view · `model.contextWindow` on the agent view · composer meta line · unit tests | the meter, correct after a refresh |
| **P1** | `CUSTOM "context_usage"` on the live path · reducer · per-agent filter chips · Playwright spec | the meter moves while the run goes |
| **P2** | AG-UI's standard `RUN_FINISHED.usage[]` (`events.WithUsage`) | the vendor-accounting channel, and the upstream record's open item |
| **P3** | mount the summarization middleware · write the summary back · show the compaction count | compaction that survives the turn |

P0 and P1 are the feature. P2 is a standards cleanup. P3 is the feature's natural sequel, and it is the one that changes the session format.

---

## 9. Tests

| Layer | What it proves |
|---|---|
| Go — `transport` | the occupancy is the **last** call of each agent, not the sum; a turn with three calls reports the third; an agent that never spoke is absent |
| Go — `api` | `usage.context` on the session view matches the live event for the same transcript; `model.contextWindow` is zero when the model declares none |
| Go — `build` | `Contexts()` reports the window of the model each agent actually runs on, including a sub-agent with its own `model:` |
| `bun run test:unit` | the reducer replaces one agent's entry and leaves the others; an unknown window renders no bar and no "0 %" |
| Playwright | the composer meta line shows the occupancy after a run; the per-agent chips show one occupancy per agent |

★ The unit tests must include the case where **the endpoint reports no usage** — `toUsageView` returns nil for a zero usage (`api/v2_types.go:243`) and the meter must be empty, not "0 tokens".

---

## 10. Risks and trade-offs

| Risk | Mitigation |
|---|---|
| The number is a *lower bound* between calls — the tool results produced after the last call are not counted yet | the UI marks the value with `≈`; the tooltip states the definition |
| Different vendors count differently (tools, reasoning tokens) | the value is the vendor's own number and is labelled as an estimate, never as an exact count |
| The window is usually unconfigured (`context_window: 0`) | the UI degrades to an absolute token count and says the window is not configured |
| A second channel (`usage.context`) next to `usage.byAgent` invites confusion | the two are documented separately (§5.4) and a test keeps the labels apart |
| Compaction that does not survive the turn would make the meter look broken | §7.2 — compaction and the write-back land together, or not at all |
| The API layer re-deriving the model would drift from the assembled agent | the window is asked of the builder (§4.3), never recomputed |

---

## 11. Out of scope

- **Predicting the context of the request that has not been made yet.** A pre-flight estimate would need a tokenizer; eino's own default counter estimates at four characters per token (`adk/middlewares/summarization/summarization.go:476`), which is good enough to trigger a compaction and not good enough to show as a number.
- **Predicting when the *model* will refuse.** The endpoint decides that, and the refusal arrives as an error.
- **Cost accounting.** Token counts are not money; prices live with the vendor.
- **Changing the session format.** Only P3 touches it, and it does so as its own step.
