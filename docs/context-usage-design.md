# Context Usage Estimate — Design

> **Status**: implemented.
> The occupancy meter (§2, §4, §5), the compaction with its persistence and the rendering of the compaction marker (§7) are in the code.
> **Scope**: this repository's code surface — the Go backend and the `web/` frontend.
> **Companion**: [architecture.md](architecture.md) (how the code is layered),
> [developer-manual.md](developer-manual.md) (how to change the code),
> [user-manual.md](user-manual.md) (how to use it).

> 中文版：[context-usage-design_zh.md](context-usage-design_zh.md)

---

## 0. In one paragraph

openheron already holds two facts that would answer *"is this conversation about to run out of context?"* — the model's context window (`context_window` in `llm/*.yaml`) and the token counts the endpoint reported (`ResponseMeta.Usage` on every assistant message). Neither is shown anywhere, and the usage that *is* exposed (`GET /v2/sessions/{id}`) is a **cumulative total** over the whole session, which does not answer the question: a session can carry a large total and an almost empty context, or a modest total and a nearly full context.

This design adds one number — the **context occupancy** of each agent — computed from the same baseline eino's own summarization middleware counts from, pushed over SSE while the run is still going, and rendered in the composer meta line and in the per-agent filter.

And it closes the loop the meter opens: when the context really does run out of room, the conversation is **compacted, and the compacted result is persisted** (§7) — because a summary that dies with the turn leaves the conversation exactly as long as it was.

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

Each turn rebuilds the model input from the persisted session history: `buildInput` composes a transient system prompt, the history, and the new query (`agentkit/run/input.go:44`), and the loop feeds it the session's messages (`agentkit/run/loop.go:71`).

Two consequences:

- The context of a session **is** its transcript. There is no compaction, no windowing, no eviction anywhere in the repository.
- The state eino's summarization middleware would rewrite is **discarded at the end of the turn** — the next turn starts again from the transcript. This is the single most important constraint for §7, and the reason the compacted result has to be written back to the session.

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

`summarizeAt` is zero when the agent never compacts — it says `0`, or its model declares no
window to measure against — and the second scale is then simply not shown.

### 2.3 The window is unknown more often than not

`context_window: 0` means *not configured* — the generic template ships exactly that (`initcfg/templates/llm/_generic.yaml:14`). The window is a per-model fact the user declares; openheron does not guess it, and neither should the UI:

- window unknown → show `≈12k tokens` and no bar, with a tooltip saying the window is not configured;
- window known → show `12k / 128k (10 %)`.

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
	// Provider and Model name the model the agent runs on.
	Provider string
	Model    string
}
```

The struct carries numbers only. Ratios are computed by whoever renders them — the frontend already formats token counts itself (`formatTokens`, `web/src/lib/agents.ts:15`), and duplicating a percentage rule in two languages is how they drift.

### 4.2 The computation lives in the transport

The transport already owns the per-call usage: `res.Usage = aggregateUsage(res.Intermediates)` at `transport/transport.go:105` and `transport/transport.go:189`, over the intermediates it appends in order (`transport/transport.go:160`, `transport/transport.go:167`, `transport/transport.go:175`).

A new `transport/context.go` computes the per-agent occupancy from the same list:

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

Two properties matter:

- **The list is ordered.** The intermediates are appended as the events arrive, so "the last message of agent A" is a walk from the end, not a search.
- **Streaming keeps the usage.** The streamed assistant message is the concatenation of the chunks (`message.ConcatChunks`, `message/variant.go:74`), and eino's `ConcatMessages` keeps the usage of the chunks it merges. A streamed turn therefore reports the same numbers as a non-streamed one.

The result joins `transport.Result` next to `Usage` (`transport/transport.go:35`).

### 4.3 The window comes from the builder, not from the API layer

The API layer must not re-derive which model an agent runs on — that is the builder's knowledge, and the repository's rule says so: the effective tool table is asked of `agent.Builder`, never recomputed. The same applies to the model.

So `agentkit/build/context.go` — next to the file that already answers `EffectiveTools` (`agentkit/build/introspect.go:47`) — grows one query:

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

The occupancy changes after **every model call**, so it must not wait for the end of the run. The transport already has the mechanism: `events.NewCustomEvent` with a value, used for the model-retry notice (`transport/transport.go:121`).

The event is emitted right after an assistant message with usage is archived — the two places where the transport appends intermediates (`transport/transport.go:167` for a streamed message, `transport/transport.go:175` for a complete one):

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
Agent: main · deepseek/deepseek-flash · Reasoning: off · Context: ≈18k / 131k (14 %)
```

- The number is `used / window` when the window is known, and `≈18k tokens` when it is not.
- The bar is a thin strip under the number; it turns amber at 80 % of the window and red at 95 %.
- The tooltip states the definition — *"tokens in the most recent request to this agent"* — because a context meter without its definition is a mystery number.

`formatTokens` (`web/src/lib/agents.ts`) is reused for the abbreviation, and it reads
a million as millions: `1311k` is harder to compare at a glance than `1.3M`, and the
window sizes people compare are in the millions.

★ **The percentage never rounds a non-zero ratio down to zero.** `formatPercent`
gives a decimal below 10 % and `<0.1 %` below that: a context holding 0.3 % of its
window is not empty, and "0 %" reads like "there is nothing in there" — the reader
stops watching exactly when the number starts to matter.

### 5.3 The per-agent filter

`web/src/components/agent-filter.tsx` lists the agents that spoke. The occupancy is the more actionable number, and the **cumulative spend** is a different quantity — so the chip carries **both**, the occupancy first:

```
main 18k/131k 600   coder 6.1k/8.2k 600
```

The chip's `title` names them, so the two numbers are never ambiguous:

```
main — 600 tokens
Context: ≈18k / 131k
```

The cumulative total stays on the chip because it is the "how much did this agent cost" number, and it is the one the session-level `usage.byAgent` reports.

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

`context_window` is already a model field, and the templates already carry it. The
summarization threshold is an **agent** field, because the window is the model's and the
threshold is this agent's own policy about how much of that window to fill:

```yaml
# agents/main.md
---
model: deepseek/deepseek-flash
summarize_at: 80%             # of the model's context_window
---
```

Left out, the agent gets 80 % of its window — see §7.8 for the three spellings and what
happens when the window is unknown.

---

## 7. Persisting the compacted conversation

The meter (§4, §5) answers "when is this conversation about to stop fitting?". This section is the other half: **what happens when it does not**. The compacted result has to outlive the turn that produced it, or the conversation never gets shorter.

**This part is implemented.** The middleware, openheron's finalizer, the pickup of the event, the write to the transcript and the two views of a session are all in the code — and so is the client-side rendering of the marker (§7.7): the compacted message reaches the browser with a `compaction` metadata marker, and the browser draws a divider on it instead of a chat bubble.

### 7.1 The trap

eino's summarization middleware rewrites the agent's **in-memory** state (`BeforeModelRewriteState` → `Summarize`, `adk/middlewares/summarization/summarization.go:349`, `:281`). openheron rebuilds the model input from the persisted transcript on every turn (`agentkit/run/loop.go:71`, `agentkit/run/input.go:44`). The rewritten state dies with the turn; the next turn sends the full transcript again.

Mounted as-is, the middleware would compact **within** a turn — useful for a long tool loop — and do nothing for a long conversation. Worse, the meter would climb back to the threshold at the start of every turn, which reads as a bug rather than as a fact.

So the compacted result must be **persisted**, and the session must be read differently afterwards. That is the whole design of this section.

### 7.2 What eino hands over

`Summarize` runs four steps in order (`adk/middlewares/summarization/summarization.go:281`):

1. it emits `before_summarize` with the messages it is about to summarize (`:293`);
2. it generates the summary (`:493`);
3. it runs the **finalizer** with the original messages and the raw summary, and gets back the **replacement history** (`:307`);
4. it calls the `Callback` with the before and after states (`:315`), then emits `after_summarize` carrying the replacement history (`:322`).

Three of those are openheron's way in:

| Hook | What it carries | What openheron uses it for |
|---|---|---|
| `Config.Finalize` | the original messages and the raw summary | **shaping** the replacement history |
| `Config.Callback` | the before and after states, synchronously | **persisting** it |
| `after_summarize` event | the replacement history (`After.Messages`) | the **UI** event |

The default finalizer (`adk/middlewares/summarization/finalizer_builder.go:37`) returns `[system messages…, summary]`. The summary is **one message**, with the recent user messages spliced into its text (`postProcessSummary`, `adk/middlewares/summarization/summarization.go:689`).

The system messages are the agent's own, and openheron's context prompt is transient by design (`agentkit/run/input.go:42`) — it is rebuilt every turn and never persisted. So **openheron supplies its own finalizer** that returns the summary alone.

The summary covers the context messages only: the middleware splits the system messages off first (`splitSystemAndContextMsgs`, `adk/middlewares/summarization/summarization.go:506`).

### 7.3 What gets written

The replacement history is **one message**. That single fact is what makes the persistence simple.

**The compacted message** is appended to `session.jsonl` like any other message (`storage/filestore/transcript.go:172`): one line, one `schema.Message`. The transcript stays append-only (`storage/storage.go:40`), its format version does not move (`formatVersion`, `storage/filestore/session.go:501`), and the existing readers keep working — a session written before this feature reads exactly as it did.

**The boundary** rides on that message itself, in the `Extra` map — the same place openheron already stamps provenance (`message/provenance.go:9`):

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

The transcript is then the **single source of truth**: the effective history is derived by finding the last message that carries the marker. No `state.json` change, no second file to keep in step, and nothing to reconcile after a crash.

### 7.4 The effective history

```
effective history = the last compaction message, and everything after it
```

The session already has the read that returns the transcript. What it lacks is a way to ask for the **effective** view of it, so the read gains a parameter rather than a second method:

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

★ Go has no default parameters, so the default is the zero value: `false` means "everything". The parameter is written out at every call site instead of hiding behind a second method — a reader of `sess.Messages(true)` knows exactly which view is meant, and the compiler makes every caller say which one it wants.

There are two production call sites, and they want different views:

| Call site | View | Why |
|---|---|---|
| the run loop (`agentkit/run/loop.go:71`) | `sess.Messages(true)` | the agent's context is the effective history |
| the messages view (`api/v2_sessions.go:274`) | `sess.Messages(false)` | the UI shows the whole transcript — the superseded messages and the compaction marker |

The implementation stays where it is (`storage/filestore/session.go:229`): the effective view is the suffix of the transcript that starts at the last compaction marker.
The HTTP surface does not change with it: the messages endpoint keeps returning the whole transcript, because that is what the UI renders. If a client ever wants the agent's view instead, `GET /v2/sessions/{id}/messages?effective=1` is the natural extension.


```go
func (fs *FileSession) Messages(effective bool) []*schema.Message {
	// … load the transcript, copy it out …
	if effective {
		out = effectiveView(out)
	}
	return out
}
```

`Len()` (`storage/filestore/session.go:248`) keeps counting the whole transcript: it feeds the sidebar's message count, which is a fact about the **record**, not about the context. The interface changes with it (`storage/storage.go:48`), and so does the test double (`agentkit/run/fakes_test.go:90`).

The existing filter still applies on top: the root agent's context excludes sub-agent messages (`agentkit/run/input.go:48`), and a sub-agent's context lives inside its own run — so **only the root agent's context is ever compacted**. Sub-agent messages carry their own flag (`message.IsSubagent`, `message/provenance.go:106`) and are dropped before the context is built.

### 7.5 Ordering, and what a crash costs

The compacted message is written **before** the turn's own messages are archived (`agentkit/run/loop.go:150`). The order on disk is:

```
session.jsonl:  … old history … │ compaction message │ this turn's messages …
```

which is the semantic order: the compaction summarizes everything before it, and the turn's messages came after it.

A crash between the append and the next read costs nothing: the compaction message is on disk, the next turn reads it as the boundary, and the history is short. A crash **before** the append costs nothing either: the history stays long, and the next turn compacts again.

★ The order matters in one direction only. If the boundary were recorded *before* the message were appended, a crash in between would leave the transcript pointing past its own end — the effective history would be empty and the agent would wake up amnesiac. Appending first cannot produce that state.

### 7.6 What the summary must not lose

| Thing | Where it lives | Survives compaction? |
|---|---|---|
| the user's own words | the summary text — eino splices the recent user messages back in, up to ~30k tokens (`preserveUserMsgsMaxTokens`, `adk/middlewares/summarization/consts.go:29`) | yes, up to the budget |
| the system prompt | rebuilt every turn (`agentkit/run/input.go:44`) | yes |
| the tool schemas | re-sent with every call | yes |
| the approval audit log | `state.json` (`storage/filestore/session.go:483`), not the message log | yes, untouched |
| the original messages | the transcript, superseded but not deleted | yes — they stay on disk |

The last row is why the transcript is not rewritten: **compaction is not deletion**. The old messages stay readable in the transcript and in the UI, and the marker says where the cut happened.

If the user wants the model to be able to re-read the original conversation, `Config.TranscriptFilePath` is the hook: eino appends the path to the summary (`adk/middlewares/summarization/summarization.go:706`), so the summary can point the model at the session's own transcript. It only works if the summarizing model can read files.

### 7.7 What the UI shows

Four things, and they are different from each other:

1. **A divider in the message list.** The compacted message is not a chat bubble. The messages view marks it with a `compaction` metadata entry (`api/v2_convert.go:63`), and the frontend draws a divider on it: *"12 earlier messages · compacted into a summary · ≈62k tokens saved"*. The marker is read during the history hydration (`web/src/lib/store/history.ts:14`), carried on the message (`web/src/lib/types.ts:56`) and rendered by `web/src/components/compaction-divider.tsx`, which the message list puts in place of the bubble (`web/src/components/message-list.tsx:101`). The superseded messages stay above it, visible — the user can still read what was said before the cut.
2. **The meter's second needle.** `context[agent].summarized` counts the compactions (`api/v2_sessions.go:326`). What the last one bought is on the divider, not on the meter: the meter answers "how full is the context now", the divider answers "what did the last cut save".
3. **A live event.** The compaction happens mid-turn, so the UI learns about it from the stream: the transport forwards `after_summarize` as a `CUSTOM "context_compacted"` event (`transport/transport.go:590`), and the reducer puts a boundary into the list at once (`web/src/lib/events.ts:412`). ★ The live event carries the fact, the agent and the time — **not the numbers**. The numbers are measured against the transcript, which the run loop writes when the turn ends; the transport has no transcript to measure against. So the live divider reads *"Earlier messages compacted into a summary"*, and the numbers show up on the next load, when the marker comes with the transcript.
4. **A manual command.** `/compact` compacts on demand: `POST /v2/sessions/{id}/compact` summarizes the effective history (`run.Compact` in `agentkit/run/compact.go`), writes the same marker, and reports the numbers — so the manual divider carries them at once. The command is hidden while a turn runs or an approval waits, and it refuses a context too small to be worth compressing (`run.MinCompactMessages`, `run.MinCompactTokens`): a summary carries a preamble, so a short conversation can come out longer than it went in.

### 7.8 Configuration

The threshold belongs to the **agent**, not to the model: the window is the model's, the
threshold is this agent's own policy about how much of that window to fill before summarizing.

```yaml
# agents/main.md
---
model: deepseek/deepseek-flash
summarize_at: 80%             # of the model's context_window — the trigger
---
```

Three spellings, resolved in `spec.SummarizeAt(threshold, window)`
(`agentkit/spec/summarize.go`):

| Written | Threshold |
|---|---|
| `80%` | 80 % of the window of the model the agent runs on |
| `104858` | an absolute token count |
| `0` (or `off`) | never |
| *(absent)* | 80 % of the window — **the default** |

`Trigger.ContextTokens` is what the middleware compares against (`adk/middlewares/summarization/summarization.go:373`); the upstream recommendation is 80–90 % of the window, which is why the default is 80 %.

★ **A percentage needs a window.** `context_window: 0` means *not configured*, and a percentage
of nothing is nothing: that agent never compacts, and the spec package records it in
`deferred` when it loads the configuration (`noticeCompactionWindows`,
`agentkit/spec/load.go`). An absolute token count needs no window.

★ **The model profile's `summarize_at` is deprecated.** It still parses — deleting it would make
every old `llm/*.yaml` fail at startup, because the decoder is `KnownFields`-strict — but nothing
reads it, and the spec package points at the agent setting instead.

The summarizing model is the agent's own model unless the config names another one — `Config.Model` takes it (`adk/middlewares/summarization/summarization.go:58`).

★ One caveat to keep in mind when reading the meter's second scale: eino's default counter adds the tool schemas **on top of** the baseline, and the baseline already contained them (`adk/middlewares/summarization/summarization.go:420`). The trigger therefore fires a little early. That is the safe direction to be wrong in.

---

## 8. Delivery plan

| Phase | Content | Size |
|---|---|---|
| **P0** | `contract.ContextUsage` · `transport/context.go` · `Builder.Contexts()` · `usage.context` on the session view · `model.contextWindow` on the agent view · composer meta line · unit tests | the meter, correct after a refresh |
| **P1** | `CUSTOM "context_usage"` on the live path · reducer · per-agent filter chips · Playwright spec | the meter moves while the run goes |
| **P2** | **compaction and its persistence** — mount the middleware · own the finalizer · append the compacted message · read the effective history · render the marker | the conversation stops growing — **steps 1-3 are in the code, step 4 (the marker in the interface) is not** |
| **P3** | AG-UI's standard `RUN_FINISHED.usage[]` (`events.WithUsage`) | the vendor-accounting channel, and the upstream record's open item |

P0 and P1 are the meter. P2 is the part that makes the meter mean something: without it, a long conversation can only end in a refusal. P3 is a standards cleanup.

### P2 in steps

P2 is the only phase that touches the session format, so it is split into steps that are each independently shippable:

1. **The middleware, mounted but not persisted.** `deep.Config.Handlers` takes `adk.TypedChatModelAgentMiddleware` values (`adk/prebuilt/deep/deep.go:97`), which is what the summarization middleware is (`adk/middlewares/summarization/summarization.go:272`); openheron already appends its gate handlers there (`agentkit/build/assemble.go:110`, `agentkit/build/gate.go:105`). With the middleware in place, a long **turn** survives a window it would not have survived. The conversation still grows — nothing is persisted yet.
2. **The compacted message.** The finalizer returns the summary alone (§7.2), the callback appends it to the transcript with the `ExtraCompaction` marker (§7.3), and the boundary is read back from it.
3. **The effective history.** `Session.Messages(effective)`, and the run loop asking for the effective view (§7.4). From here the conversation stops growing across turns.
4. **The UI.** The marker in the message list, the compaction count on the meter, the live `context_compacted` event (§7.7).

★ Step 2 without step 3 would be a bug: the compacted message would sit in the transcript and the agent would keep sending the full history, so every turn would compact again. Steps 2 and 3 land together.

---

## 9. Tests

| Layer | What it proves |
|---|---|
| Go — `transport` | the occupancy is the **last** call of each agent, not the sum; a turn with three calls reports the third; an agent that never spoke is absent |
| Go — `api` | `usage.context` on the session view matches the live event for the same transcript; `model.contextWindow` is zero when the model declares none; the compaction marker reaches the browser with its numbers, and a compaction is not dropped for carrying nothing else |
| Go — `build` | `Contexts()` reports the window of the model each agent actually runs on, including a sub-agent with its own `model:`; the **threshold** is each agent's own, resolved against that window; an agent that says nothing gets 80 %; a model without a window gives the agent none |
| Go — `spec` | the threshold is spelled three ways and each lands where it should; an impossible one is a startup error; the threshold is read from the agent file; the old model field still loads and is reported; an agent whose model declares no window is reported |
| Go — `storage` | the effective history is the last compaction message and what follows it; a session with no compaction marker returns the whole transcript; the superseded messages are still on disk |
| Go — `agentkit/run` | the turn's input is built from the effective history; a compaction during the turn is persisted **before** the turn's own messages |
| Go — `transport` | a compaction is announced on the stream as `CUSTOM "context_compacted"`; a run that never compacted announces nothing; a **sub-agent's** compaction is not the session's; the summary is picked out of a replacement history that also carries the system message |
| Go — `run` | a manual compaction writes the boundary and the marker; a history that is too small is refused; a failed summarizer writes nothing |
| Go — `api` | the manual compaction answers with what it achieved; a running turn is a 409; a tiny context is a 400 |
| `bun run test:unit` | the reducer replaces one agent's entry and leaves the others; an unknown window renders no bar and no "0 %"; the compaction marker renders as a divider, not as a bubble; the live event puts a boundary into the list; `/compact` is hidden while a turn runs |
| Playwright | the composer meta line shows the occupancy after a run; the per-agent chips show one occupancy per agent; after a compaction the message list shows the divider, not a bubble, and the earlier messages are still readable |

★ The unit tests must include the case where **the endpoint reports no usage** — `toUsageView` returns nil for a zero usage (`api/v2_types.go:243`) and the meter must be empty, not "0 tokens".

★ The compaction tests must include the **restart** case: compact, close the session, reopen it, and the effective history must still be short. A compaction that only lives in memory is the failure this design exists to prevent.

---

## 10. Risks and trade-offs

| Risk | Mitigation |
|---|---|
| The number is a *lower bound* between calls — the tool results produced after the last call are not counted yet | the UI marks the value with `≈`; the tooltip states the definition |
| Different vendors count differently (tools, reasoning tokens) | the value is the vendor's own number and is labelled as an estimate, never as an exact count |
| The window is usually unconfigured (`context_window: 0`) | the UI degrades to an absolute token count and says the window is not configured |
| A second channel (`usage.context`) next to `usage.byAgent` invites confusion | the two are documented separately (§5.4) and a test keeps the labels apart |
| Compaction that does not survive the turn would make the meter look broken | §7 — the compacted message is appended to the transcript, and the effective history is read from it |
| A summary that drops the user's own words would quietly change what the agent does | eino splices the recent user messages back into the summary (`adk/middlewares/summarization/consts.go:29`); the budget is a knob, not a constant |
| The superseded messages stay on disk, so a session's transcript keeps growing even after compaction | compaction shortens the **context**, not the record — the record is the audit trail; the transcript's size is a separate concern from the window |
| The API layer re-deriving the model would drift from the assembled agent | the window is asked of the builder (§4.3), never recomputed |

---

## 11. Out of scope

- **Predicting the context of the request that has not been made yet.** A pre-flight estimate would need a tokenizer; eino's own default counter estimates at four characters per token (`adk/middlewares/summarization/summarization.go:476`), which is good enough to trigger a compaction and not good enough to show as a number.
- **Predicting when the *model* will refuse.** The endpoint decides that, and the refusal arrives as an error.
- **Cost accounting.** Token counts are not money; prices live with the vendor.
- **Compacting a sub-agent's context.** A sub-agent's context lives inside its own run and dies with it (§7.4); only the root agent's context is long-lived.
- **Editing or deleting the superseded messages.** Compaction is not deletion (§7.6); the record stays intact and readable.
- **Changing the transcript's format.** The compacted message is an ordinary message with an extra marker (§7.3), so `formatVersion` does not move and old sessions keep loading.
