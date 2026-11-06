# Architecture

> openheron is a multi-agent runtime: every expert agent binds its own model and tool
> boundary, while the parent agent only orchestrates and synthesises. This document
> explains **how the code is actually organised** and **why it is layered this way**.
>
> Companion to [user-manual.md](user-manual.md) (for users) and
> [developer-manual.md](developer-manual.md) (for people changing the code).
> Every conclusion here points at a concrete file and line.

> 中文版：[architecture_zh.md](architecture_zh.md)

## 1. In one paragraph

An HTTP server process (`api`) loads a **file-based configuration** (`agentkit/spec`),
assembles each agent into a **bounded sub-agent tree** (`agentkit/build`), hands the tree
to eino ADK's **run loop** (`agentkit/run`), and streams the whole thing as an **AG-UI
event stream** (`transport`) to the browser (`web/`). State lives in a **file store**
(`storage/filestore`); approvals resume from a **checkpoint** (`approval` + `storage`).

```
                    ┌──────────────┐
   HTTP /v2/*  ────▶ │     api      │  routing, SSE sink, error envelope
                    └──────┬───────┘
              ┌────────────┼────────────┬───────────┐
              ▼            ▼            ▼           ▼
        agentkit/spec  agentkit/build  agentkit/run  transport
          config load   agent assembly  run loop     AG-UI events
              │            │            │           │
              ▼            ▼            ▼           ▼
         decl(decls)    llm/common     storage      webui
         kb(corpus)     approval       persist      │
         persist                       filestore    web/(React)
```

## 2. Layering and dependency direction

Dependencies are **one-way**: an upper layer may import a lower one, a lower layer
**never** imports an upper one. The table below is the actual import graph, obtained by
counting `grep '"github.com/ouranoshong/openheron/'` hits per package:

| Layer | Package | Internal dependencies | Responsibility |
|---|---|---|---|
| **L0 leaves** | `contract` | none | Cross-layer data contracts (events, messages, interrupts) |
| | `message` | none | Message content and provenance |
| | `helpers` | none | Stateless helpers |
| | `persist` | none | Atomic file writes |
| | `approval` | none | Approval decisions: representation and validation |
| | `agentkit/obs` | none | **The log sink** (the only `Logger()`) |
| | `llm/common` | none | Vendor protocol layer (zero openheron dependencies) |
| | `webui` | none | Embedded frontend assets |
| | `agentkit/fsx` | none | **The filesystem backend**: a bounded local backend — writes confined to the project root, ignore rules, result caps (see [fs-backend-design.md](fs-backend-design.md)) |
| **L1** | `agentkit/spec/decl` | none | **Pure declarations** (where the yaml tags live) |
| | `agentkit/emit` | `obs`, `approval`, `contract`, `helpers`, `message` | The derivation: agent events → the run's facts |
| | `storage` | `contract` | Interfaces for sessions/messages/pending approvals |
| | `kb` | `obs`, `persist` | Knowledge base: corpus, index, retrieval, scoring |
| | `agentkit/spec` | `obs`, `decl`, `persist` | Config loading, validation, hot reload, write gates |
| **L2** | `agentkit/build` | `obs`, `spec`, `decl`, `approval`, `contract`, `helpers`, `kb`, `llm/common`, `message` | Agent assembly, tool assembly, policy gates |
| | `agentkit/run` | `emit`, `obs`, `approval`, `contract`, `message`, `storage` | Run loop, input construction, event consumption |
| | `storage/filestore` | `obs`, `contract`, `message`, `persist`, `storage` | File store implementation |
| | `storage/memarbit` | `storage` | The in-memory lease arbiter |
| | `transport` | `contract` | AG-UI rendering: the run's events → frames |
| | `llm` | `build`, `spec`, `decl` | Thin wrapper (only `common/` and one test file) |
| | `initcfg` | `spec`, `persist` | Factory templates for `openheron init` |
| **L3 entry** | `api` | `build`, `obs`, `run`, `spec`, `decl`, `contract`, `message`, `storage`, `filestore`, `transport`, `webui` | HTTP routing, SSE, error envelope |

### 2.1 Two dependency rules that must not be broken

**(1) There is exactly one log sink.** All runtime logging goes through
`agentkit/obs`'s `Logger()` (`agentkit/obs/logger.go:18`). Levels (Info/Warn/Error) and
prefixes are defined in that one place, and the level decides whether a line shows up in
the default output — so a business package calling `log.Printf` or
`fmt.Fprintln(os.Stderr)` directly bypasses the switch.

The format is hertz's, so the whole output reads as one stream: the date, the time with
microseconds, the caller's file and line, the level, then the message and the attributes
(`agentkit/obs/handler.go`):

```
2026/10/29 22:28:34.346459 main.go:189: [Info] [main] openheron version=dev
```

**The only exception** is `llm/common`: it is a leaf package with zero openheron
dependencies, and importing `obs` there would break the "the protocol layer does not
depend on the runtime" rule, so it uses the standard logger. Any openheron-internal
import inside `llm/common` is a regression.

**(2) Declaration and behaviour are separate.** `agentkit/spec/decl` holds **only plain
structs** and yaml tags (`agentkit/spec/decl/decl.go`) and imports no other package;
`agentkit/spec` holds the **behaviour** (loading, validation, reloading, writing). That
way `decl` can be passed freely between `build`, `api` and `initcfg` without dragging a
dependency chain along.

### 2.2 Why `agentkit/spec` does **not** import `kb`

**Validating** the knowledge-base config (`tools/kb.yaml`) needs to know whether the
corpus path is relative, whether the model is declared at all, and whether it is
declared as `kind: embedding` (`agentkit/spec/load.go:171` `loadKB`, `load.go:331`
`validateKB`).

★ All three answers live in the **declarations** (`decl.Model.IsEmbedding`,
`decl.Model.Dimensions`), so the configuration layer does not import the knowledge base
at all — only a test file does (`agentkit/spec/policy_contract_test.go:7`). The
dependency direction therefore stays one-way without `kb` being involved in it: `kb`
depends on `obs` and `persist`, and `agentkit/spec` depends on `obs`, `decl` and
`persist`.

### 2.3 Why `agentkit/build` imports `llm/common` and not `llm`

The `llm` package imports `build` (it is a thin wrapper over `build.Resolve`). If `build`
imported `llm` back, that would be a cycle. `build` uses the protocol types in
`llm/common` directly, which avoids the cycle and keeps the protocol layer a leaf.

## 3. The configuration system

### 3.1 The config root has exactly three levels

```
--config-dir  →  $OPENHERON_CONFIG_DIR  →  <project-root>/.openheron
```

Implemented in `agentkit/spec/config.go`'s `NewService`. **Ancestor directories are never
searched** — once the config root depends on "where the process was started from", the
same command run from a different directory reads a different configuration, and a test
instance would read the development configuration too.

Only a **defaulted** root is created automatically (`config.go:186`'s `os.MkdirAll`). An
explicit root or an env root pointing nowhere is **not** created: a typo must not silently
grow an empty configuration tree.

`spec.NewServiceAt` is a second entry point, used by `openheron init`: it **honours only
the path passed in** and does not read the environment variable — otherwise init would
write the new configuration somewhere the user did not point at.

### 3.2 Directory partitions

| Directory | Category constant | Hot reload | Contents |
|---|---|---|---|
| `agents/` | `spec.DirAgent` | ✅ | One `.md` per agent |
| `llm/` | `spec.DirLLM` | ✅ | One `.yaml` per vendor |
| `policy/` | `spec.DirPolicy` | ✅ | `permissions.yaml` + `limits.yaml` |
| `tools/` | `spec.DirTools` | ✅ | `http.yaml` / `web_search.yaml` / `wikipedia.yaml` / `kb.yaml` |
| `server/` | `spec.DirServer` | ❌ | Process parameters; needs a restart |
| `state/` | `spec.DirState` | — | Sessions and checkpoints; auto-created |
| `skills/` | — | ❌ | **Resolved independently** (see §3.6) |

File names under `tools/` are a **whitelist**: `agentkit/spec/load.go:130` errors out on
an unknown file instead of ignoring it — "I wrote kb.yaml and it did not take effect" is
one of the hardest classes of problem to debug.

### 3.3 Agent definitions

An agent is a markdown file: **frontmatter + body**. The frontmatter is **strictly
decoded** (`agentkit/spec/load.go:65`'s `loadAgent`) — a single wrong key is a startup
error.

Available frontmatter keys (`agentkit/spec/decl/decl.go:67`'s `decl.Agent`):

| Key | yaml tag | Meaning |
|---|---|---|
| `description` | `description` | What this agent is for (the parent agent reads it) |
| `model` | `model` | `<provider>/<model>`; **never inherited, never falls back** |
| `max_iterations` | `max_iterations` | Cap on tool-loop iterations |
| `subagents` | `subagents` | The sub-agents it may delegate to |
| `disabled_tools` | `disabled_tools` | Tool deny-list effective for this agent |
| `temperature` | `temperature` | Sampling temperature |
| `max_tokens` | `max_tokens` | Sampling cap |
| `reasoning` | `reasoning` | Reasoning tier name (`low`/`high`/`max`…) |

`decl.Agent` also carries three `yaml:"-"` fields (`ID`, `File`, `Instruction`) — the
loader fills them in; they are **not** frontmatter keys.

The body is the prompt itself (`Instruction`).

### 3.4 Permissions: two layers, never merged

| Layer | Where | Scope |
|---|---|---|
| Outer | `deny` in `policy/permissions.yaml` | Every agent |
| Inner | `disabled_tools` in `agents/*.md` | This one agent only |

The two are **stored separately and never merged** — merging would destroy the
information about *which* layer blocked a tool. The outer `deny` wins, and it is the
one guarantee a sub-agent cannot relax.

There are only two columns of tiers (`build.Policy.TierOf`, `agentkit/build/policy.go:39`;
the mirrored `decl.Policy.TierOf` at `agentkit/spec/decl/decl.go:240` is unused): `deny` →
`ask` → otherwise `allow`. **A tool that is not named defaults to `allow` (straight
through)**. To make a tool require a human every time, list it under `ask`.

### 3.5 Depth cap and topology validation

`agent.max_depth` in `server/config.yaml` (default `1`) and the same key in
`policy/limits.yaml` **resolve to the stricter of the two**
(`spec/load.go:357`'s `globalMaxDepth`).

A declared topology that exceeds the cap is a **startup error**
(`spec/load.go:293`'s `validateDepth`) — never a silent truncation. A sub-agent
referencing an unknown agent and a cyclic topology are startup errors too
(`load.go:396`, `load.go:404`).

### 3.6 skills resolve by a different rule

`skills/` is **not** "a partition of the config root"; it is resolved independently
(`agentkit/build/skill.go:41`): `$EINO_EXT_SKILLS_DIR` first, and only if that is unset
does it search **upwards** from the current directory for `.openheron/skills`.

★ This is a different rule from "the config root never searches upwards". Skills are a
directory tree, and searching upwards lets you reuse skills from your home directory;
the config root is a set of entities, and searching upwards would make the same command
read different configurations.

★ **Which skills an agent may use is a scoped backend.** The `skills` key in
`agents/<name>.md` is an allow-list of the folders under the skills directory, and it
reaches the middleware as `scopedSkillBackend` (`agentkit/build/skill.go`). The scope
filters both halves: `List` — the skill tool's description is rendered from it, so it is
what the model **sees** — and `Get` — what the model can actually **load**. Filtering
only one half would leave a skill that a model remembers the name of loadable.

★ The list is carried as a **pointer** (`decl.Agent.Skills`), because "not declared"
and "declared and empty" mean different things: the first is every skill, the second is
none.

★ The skill middleware is attached to the **entry agent** — the one the session
selected (`agentkit/build/assemble.go`). A sub-agent does not get the skill tool at all,
so there the list has nothing to filter.

### 3.7 Two loading modes and "deferred"

`spec.Service.Load()` (`config.go:245`) validates strictly — a failure is a startup
failure. `LoadUnchecked()` (`config.go:268`) skips validation.

`Config.Deferred()` (`config.go:114`) collects **non-fatal** problems (a duration string
that will not parse, for instance), and surfaces them as a `deferred` array in
`/v2/health` and `/v2/config`. The dividing line is: **anything that would make runtime
behaviour wrong is fatal; anything that is merely a hint is deferred.**

### 3.8 Write gates

The write path of the config editor (`/v2/settings`) has several gates:

- `server/` is **not editable** (`api/v2_settings.go`) — it is not hot-reloaded, so a
  change would need a restart, and the API rejects it outright.
- Literal secrets are rejected: `api_key must be a reference, not a literal: use
  ${ENV_VAR} or @file:/path` — this keeps secrets out of the repository.
- Optimistic concurrency: the write carries `baseRevision`; a mismatch with the revision
  on disk is reported as a conflict.

## 4. Agent assembly

`agentkit/build` turns "a declaration" into "a runnable agent".

### 4.1 The assembly pipeline

`assemble` in `agentkit/build/assemble.go`:

1. Take the agent's `model` reference and resolve it with `build.Resolve`
   (`provider.go:58`) into a `(provider, model)` pair; failing to resolve is a startup
   error.
2. Compute the **effective tool table** from `disabled_tools` plus the outer policy.
3. Recursively assemble the sub-tree declared by `subagents` and fill `SubAgents` into
   eino ADK's `deep.Config` (`assemble.go:97`).
4. `WithoutGeneralSubAgent: true` (`assemble.go:25`'s `baseConfig`, and `hub.go:162`) —
   **eino's built-in "general sub-agent" is switched off**, because it would let the
   model invent sub-agents out of thin air and bypass our own topology and depth
   validation.

★ One more thing in `assemble.go:145-148` that is easy to miss: `base.SubAgents = nil`
and `base.Name/Description/Instruction` are cleared — that is the **per-node** assembly
stripping the inherited sub-agent list, so each node hangs its own subtree instead of
copying the parent's.

### 4.2 The tool gate

`agentkit/build/gate.go` implements "may this tool be used":

- Outer `deny` → the tool is **never registered** for this agent, and the model cannot
  see it.
- Outer `ask` → the tool is registered, but a call **interrupts** and waits for a human
  decision.
- Inner `disabled_tools` → likewise not registered for that agent.

When a call is denied, the tool returns **a denial message** (with an optional reason),
**not** an error — the model sees "denied, the reason is X" and can adjust accordingly.

### 4.3 Where tools come from

| Source | Assembled at | Examples |
|---|---|---|
| Filesystem middleware | `build/tools.go:40` | `ls` `read_file` `write_file` `edit_file` `execute` |
| Network tools | `build/nettools.go` | `web_search` `http` |
| Knowledge base | `build/kb.go` | `kb_search` `kb_index` |
| Encyclopedia | `build/nettools.go:236` | `wikipedia` |

The external network tools need **no code change**: a provider whose configuration is
absent mounts nothing, so writing the file is what turns the tool on (`tools/*.yaml`).
★ The filesystem middleware is the exception — `fsx.New`
mounts it unconditionally (`agentkit/build/snapshot.go:56`), and the knowledge base is
switched by its **corpus**, not by its file.
★ Note that `openheron init` **writes nothing** under `tools/` by default — that whole
directory is yours to write, and a provider without a configuration mounts nothing, so
turning a tool on is a decision, not a side effect. `--tools` writes the four
configuration files.

## 5. The run loop

`agentkit/run` is a thin layer over eino ADK and does three things:

1. **Build the input** (`agentkit/run/input.go:44`): assemble the system prompt + session
   history + this turn's query into an ADK input. A sub-agent's messages **do not mix
   into the parent's history** (`input.go:64`'s `withoutSubagentMessages`) — this is
   where "isolation" lands at the data level.
2. **Drive the loop** (`agentkit/run/loop.go`): run ADK's run and translate its events
   into AG-UI events.
3. **Bridge approvals** (`agentkit/run/bridge.go`): suspend when a tool asks for
   approval, and resume from the checkpoint once the decision comes back.

### 5.1 The event stream

The event types come from the `ag-ui-protocol/ag-ui` Go SDK. The frames are rendered by
`transport/transport.go` from the run's events — the derivation (what the agent's events
*mean*) lives in `agentkit/emit`:

| Event | Emitted at | Meaning |
|---|---|---|
| `RUN_STARTED` | `transport.go:54` | The turn began; carries `threadId` |
| `TEXT_MESSAGE_START/CONTENT/END` | `transport.go:239-256` | Assistant body, incrementally |
| `REASONING_*` | `transport.go:262-273` | Reasoning process |
| `TOOL_CALL_START/ARGS/END/RESULT` | `transport.go:276-306` | Tool calls |
| `CUSTOM` (`model_retry`) | `transport.go:220` | Model-retry notice |
| `RUN_FINISHED` | `transport.go:62`, `:68` | The turn ended; carries result and usage |
| `RUN_ERROR` | `transport.go:233` | Failure |

A sub-agent's output is **attributed through `subagentRunId` on the event** rather than
through a separate event type — that lets the frontend handle every agent's output with
one reducer.

### 5.2 Approval resume

The approval request is persisted (`storage`'s `PendingApproval`), and the **checkpoint
store** is the source of truth for resuming. The client sends its decision back in the
`resume[]` array of the same `POST /v2/run` (each entry carries `interruptId` plus
`payload.approved` / `payload.reason`).

Without the checkpoint there is nothing to resume from — the API then returns
`this approval can no longer be resumed: its checkpoint is gone. Send a new message to
continue.` (`api/v2_types.go:121`) instead of silently starting a fresh turn.

### 5.3 Session admission

**A session has at most one live turn.** A second `POST /v2/run` on a busy session is
refused with `409 session_busy` — it never takes the running turn over.

The rule lives in `agentkit/run`, next to the state it guards:

- `ensureIdle` (`agentkit/run/runtime.go`) refuses a **new query** when a turn is
  executing or an approval is waiting.
- `ensureNoLiveQuery` refuses a new query when a turn is executing, but lets a
  **resume** through: a resume continues the interrupted turn it answers — the same
  round of the conversation, not a new one.
- `claimTurn` takes the busy test and the takeover under **one** lock, so two
  simultaneous requests cannot both pass.

A refusal changes **nothing**: no checkpoint discarded, no approval cleared, no
message persisted. The running turn is untouched.

`Complete` returns only after the turn's messages are on disk
(`agentkit/run/turn.go`). That is what keeps the client's "your turn is done" and the
server's "this session is free" from disagreeing — and it is why the next turn's input
never reads an incomplete history.

The pending approval is written when the **loop exits**, not when the turn's callback
returns: the framework writes the checkpoint at the loop's exit, and an approval is only
answerable if the checkpoint it resumes from exists.

The frontend reads the server's answer rather than its own memory: the session's
`state` comes from the server's scan of the **run ledger** and feeds the composer's
guard, so a second tab hydrates as busy instead of idle.

The run ledger is the durable record of the runs: one row per run, written by the run's
own goroutine (`agentkit/run/runrow.go`), read back by
`GET /v2/sessions/{id}/runs`. A run's **terminal row lands after its messages are on
disk** — that single ordering is what lets the busy flag be honest without a timeout.
A run that a crash left open is closed as `interrupted` during the startup pass, so
a session never reads as busy for ever.

## 6. Storage

`storage` defines the interfaces (`SessionManager` / `Session`); `storage/filestore`
provides the implementation.

On-disk layout (`storage/filestore/session.go`):

- `state.json` — session state (atomically rewritten)
- transcript — messages, appended (`O_APPEND`)
- checkpoint — the approval resume point
- `runs.jsonl` — the run ledger: one row per run, appended (`O_APPEND`), bounded to
  the last 200 rows

**Memory is only a cache**; disk is the source of truth. Listing sessions (`List()`) reads
only `state.json` and never opens the transcript — so listing is cheap.

## 7. The HTTP layer

`api` exposes only the `/v2` family (the old unprefixed API is gone; `/health` returns
404 in practice).

- Routes are registered in one place, `api/v2_files.go:34-65`.
- The error envelope is produced by `v2Fail` in `api/v2_types.go`:
  `{"error": "...", "code": "...", "retryable": false}`.
- **`/v2` routes are registered before the catch-all `GET /*`** — the SPA fallback can
  never swallow an API path.
- **No authentication and no CORS.** The default `:8080` binds every interface; set
  `listen_addr` explicitly to narrow it.

`threadId` is **required** by `POST /v2/run` (`api/v2_run.go:78`) — create the session
with `POST /v2/sessions` first, then start the run.

## 8. The frontend

React 19 + Vite + zustand. The build output in `web/dist` is embedded into the binary
with `go:embed` (`webui/webui.go`).

`frontend_mode` defaults to `embed` (use the interface baked into the binary); `dir`
reads the build output from disk at `static_dir` (relative to the project root).

The UI is split into layers. `lib/sse.ts` (SSE framing) and `lib/events.ts` (event
decoding + reducer) **depend on no framework** — the former has zero imports, the latter
only `import type`; `state.ts` / `selectors.ts` / `history.ts` under `lib/store/` are
pure as well. `lib/store.ts` is a barrel; the `store/actions.ts` it re-exports owns the
zustand instance, so importing it pulls in zustand, `fetch` and `localStorage` too.

★ Put new logic in these layers and keep them pure — correctness is proven by
`bun test`, which is far faster than "changed the UI, now open a browser".

★ A zustand selector must return a **reference-stable** value: allocating a new
array/object on every call makes `Object.is` decide "it changed" forever, which shows up
as a `Maximum update depth exceeded` white screen. Never write `?? []` in a selector;
use a module-level constant or a selector from the store.

## 9. The knowledge base

`kb` is an **optional** local corpus retrieval layer.

- The corpus is a set of markdown documents; the directory comes from `corpus` in
  `tools/kb.yaml` (relative to the project root).
- The index lands in `<data-dir>/kb/<index>/` (`build/kb.go:42`), with a `catalog.md`
  index page.
- **Retrieval is a hybrid score**: `scoreDoc` in `kb/score.go:53` computes
  `0.5*lexical + 0.5*cosine` when vectors are available, and is **purely lexical**
  without them (which is why `kb status` reports `lexical-only retrieval`).
- The lexical score **weights identifiers**: a term appearing in the title, a section
  heading or a link is worth more than one buried in the prose.
- `openheron kb index` takes a **run lock** (`kb/store.go:100`'s `StartRun`): only one
  run at a time.
- Agents get two tools: `kb_search` (returns a document **outline**, not the full text)
  and `kb_index`. `kb_index` defaults to the `ask` tier — letting an agent rebuild the
  index needs a human nod.

## 10. Observability

`agentkit/obs` is the only log sink. Levels and prefixes are defined there and nowhere
else. `agentkit/obs/hook` provides observation hooks (it depends on `obs`, never the
other way round).

## 11. Boundaries and what is deliberately absent

Writing these down is cheaper than explaining them later. The following are **not
present today**:

- **No authentication, no multi-tenancy.** The default `:8080` is open on every
  interface.
- **No TLS.** Put a reverse proxy in front to expose it publicly.
- **No distribution.** A single process, a single binary; sessions live on local disk.
- **The knowledge base supports local directory corpora only**, not remote fetching.
- **The old unprefixed HTTP API is gone**; there is only `/v2`.
- **`skills/` still searches upwards**, which differs from the config root's rule
  (see §3.6).
