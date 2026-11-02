# Developer Manual

> For **people changing openheron code**. To use it, see
> [user-manual.md](user-manual.md); for why it is designed this way, see
> [architecture.md](architecture.md).
>
> The conventions and red lines live in [AGENTS.md](../AGENTS.md) (repository root);
> this document expands on them. **Read AGENTS.md before you start** — the language
> rules and the test-environment rules in there are hard requirements.

> 中文版：[developer-manual_zh.md](developer-manual_zh.md)

## 1. Environment

| | Version |
|---|---|
| Go | 1.27.1+ (as declared in `go.mod`) |
| Node / bun | bun (frontend package management, tests, scripts) |
| Ports | see `.env.test` (repository root, committed) |

```bash
make build-go     # backend only (no UI) — fastest while iterating on Go code
make web-dev      # Vite dev server (:5173, proxies /v2 to :8080)
make dev          # air: rebuild and restart the Go binary automatically
make check        # lint + test — run this before committing
```

## 2. Repository layout

```
openheron/
├── main.go            # entry point: flags, config root, server startup
├── initcmd.go         # openheron init
├── kbcmd.go           # openheron kb
├── api/               # HTTP routing, SSE, error envelope
├── agentkit/
│   ├── spec/          # configuration: loading, validation, hot reload, write gates
│   │   └── decl/      # pure declaration structs (where the yaml tags live)
│   ├── build/         # agent assembly: tools, policy, providers
│   ├── run/           # run loop: input construction, ADK driving, event consumption
│   ├── emit/          # the derivation: agent events → the run's facts
│   └── obs/           # the log sink (the only one)
├── approval/          # representation and validation of approval decisions
├── benchmarks/        # the cost benchmark: strong parent + cheap workers vs. one strong model
├── contract/          # cross-layer data contracts
├── helpers/           # stateless helpers
├── initcfg/           # factory templates for init
├── kb/                # knowledge base: corpus, index, retrieval, scoring
├── llm/common/        # vendor protocol layer (zero openheron dependencies)
├── message/           # message content and provenance
├── persist/           # atomic file writes
├── storage/
│   ├── filestore/     # file store implementation
│   └── memarbit/      # the in-memory lease arbiter
├── transport/         # AG-UI rendering: the run's events → frames
├── webui/             # embedded frontend assets
└── web/               # React frontend
```

### 2.1 Dependency direction

**One-way**: an upper layer may import a lower one, a lower layer never imports an upper
one. The measured internal import graph:

| Layer | Packages | Internal dependencies |
|---|---|---|
| **L0** | `contract` `message` `helpers` `persist` `approval` `agentkit/obs` `agentkit/fsx` `llm/common` `webui` | none |
| **L1** | `agentkit/spec/decl` `storage` `kb` `agentkit/spec` `agentkit/emit` | `decl` none; `storage`→`contract`; `kb`→`obs`,`persist`; `spec`→`obs`,`decl`,`kb`,`persist`; `emit`→`obs`,`approval`,`contract`,`helpers`,`message` |
| **L2** | `agentkit/build` `agentkit/run` `storage/filestore` `storage/memarbit` `transport` `llm` `initcfg` | see architecture.md §2 |
| **L3** | `api` | almost all of them |

Two rules that **must not be broken**:

**(1) There is exactly one log sink.** Do not call `log.Printf` /
`fmt.Fprintln(os.Stderr)` directly in a business package; go through `obs.Logger()`.
Levels and prefixes are defined in one place, `agentkit/obs/logger.go:18`, and the
level decides whether a line shows up in the default output.

A line reads the way hertz's logger writes it — the date, the time with microseconds,
**the caller's file and line**, the level, then the message and the attributes:

```
2026/10/29 22:28:34.346459 main.go:189: [Info] [main] openheron version=dev
2026/10/29 22:28:34.351002 api/v2_run.go:123: [Warn] [run=abc123] request failed err="connection refused"
```

The envelope is built by `agentkit/obs/handler.go`, a `slog.Handler` — slog's own text
handler cannot produce it (its envelope is a list of attributes, and the source is just
another attribute). ★ The file and the line are the **caller's**: slog captures the call
site's program counter in the record, and that is what the handler prints.

**The only exception is `llm/common`** — it is a leaf package with zero openheron
dependencies, and importing `obs` there would break the "the protocol layer does not
depend on the runtime" rule, so it uses the standard logger with the same flags
(`llm/common/logging.go`). Its lines have the same envelope but **no level tag**:
that package has no levels. Any openheron-internal import inside `llm/common` is a
regression.

**(2) Declaration and behaviour are separate.** `agentkit/spec/decl` holds only plain
structs and imports no other package; `agentkit/spec` holds the behaviour (loading,
validation, reloading, writing).

### 2.2 Do not duplicate assembly logic in the API layer

When you need "the effective tool table of some agent", let `agent.Builder` expose a
read-only query. **Do not** recompute `Table.Blocked` / `Policy.TierOf` in the API
layer. The reason is plain: recomputation drifts — change the policy once and the API's
answer and the runtime's answer stop agreeing.

## 3. Development recipes

### 3.1 Adding a new tool

`agentkit/build/nettools.go` is the complete worked example, using `web_search`.

1. **Implement the provider.** A tool provider is the `toolProvider` interface at
   `agentkit/build/tools.go:19`:

   ```go
   type toolProvider interface {
       // ID returns the provider name, used in logs and error messages.
       ID() string
       // Optional decides whether a failure means "skip" or "the whole service cannot start".
       Optional() bool
       // Provide returns the batch of tools this provider supplies.
       Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error)
   }
   ```

   Note that `Provide` returns a **batch** — one provider may supply several tools.

2. **Register it.** Add your provider to `defaultToolRegistry` at
   `agentkit/build/tools.go:29`. The position determines the order, and it also
   determines how optional and required providers are told apart.

3. **Optionality is decided by `Optional()`, not by returning an error.** A provider
   with `Optional() == true` logs a Warn and is skipped when it fails
   (`tools.go:58-60`) — a missing API key must not stop the whole service from starting.
   ★ But note that this Warn is the **only** notice: the tool silently does not enter
   the table, and the user will only notice that "this feature seems to be missing".
   Think hard about whether that Warn is enough when adding an optional provider.

4. **The gate works on its own.** `agentkit/build/gate.go` filters the tool table with
   the outer policy plus the inner `disabled_tools`. You write no permission code.

5. **Do not collide with a reserved name.** `reservedToolNames` at `tools.go:39` lists
   `ls` `read_file` `write_file` `edit_file` `glob` `grep` `execute` `task`
   `write_todos` `skill`. A collision fails at `Build` time.
   The tool name is also the key in the policy table — a collision would point the
   policy at the wrong tool.

6. **A collision across providers also fails**: `validateBatch` at `tools.go:76` tracks
   names in `seen`, so two providers offering the same tool name fail at startup
   instead of one silently overwriting the other.

### 3.2 Adding a new configuration partition

Say you want a new `mcp/` directory:

1. **`agentkit/spec/config.go`**: add the directory constant `DirMCP = "mcp"` and an entry
   in `Categories` (with `ID`, `Label`, `Dir`, `Ext`). Keep **hot-reloaded** and
   **non-hot-reloaded** directories clearly apart — whether a directory is hot changes
   what users expect.

2. **`agentkit/spec/load.go`**: add the branch in `loaderFor` (`config.go:296`) and
   implement `loadXXX`.

3. **`agentkit/spec/decl/decl.go`**: add the plain declaration struct.

4. **Hot reload comes for free**: `hotDirList()` at `agentkit/spec/config.go:76` filters
   the `Categories` whose `Hot()` is true — mark the category hot in step 1 and the
   watched set includes it automatically. **You do not** need to register it by hand in
   `reload.go` (and forgetting to would mean "config changes do not take effect, with
   no notice at all").

5. **Write gate**: add it to the editable-section list in `api/v2_settings.go`.
   A non-hot-reloaded section **must not** be in the editable list — otherwise the user
   changes it in the UI, it only takes effect after a restart, and the UI says nothing.

6. **Defaults**: `agentkit/spec/defaults.go`.

7. **Factory template**: `openheron init` writes the minimum runnable configuration, so
   a new partition needs one too. Either add a file under `initcfg/templates/<dir>/` —
   for the files only init produces — or reuse the scaffold the config editor already
   has (`spec.ToolTemplates()` / `spec.LimitsTemplate()`). ★ **One file's content has
   one source**: two copies of the same file are two copies that drift.

### 3.3 Adding an API route

1. **Register it.** Routes are registered in one place, `api/v2_files.go:34-65`.
   **Registration order matters**: a `/v2` route must be registered **before** the
   catch-all `GET /*`, or the SPA fallback will swallow it.

2. **Error envelope.** Use `v2Fail` from `api/v2_types.go`; do not hand-roll JSON:
   ```json
   {"error": "...", "code": "...", "retryable": false}
   ```

3. **Data sources.** To wire up a new source, add a `SetXxxSource` method on
   `api.Server` (the pattern is `api/server.go:75`'s `SetSkillsSource`) and connect it
   in `main.go`. **Do not** let a handler read a global variable directly.

4. **Streaming.** The SSE sink lives in `transport`; `api` only writes the events out.
   Do not invent new event types — use the ones AG-UI already defines.

### 3.4 Adding an agent / changing the topology

Editing `agents/*.md` is enough; no code change is needed. But keep in mind:

- `model` is **required** and never inherited. A wrong value is a startup error.
- An agent referenced by `subagents` must exist and must not form a cycle — both are
  **startup errors**.
- Exceeding `max_depth` is a **startup error**, never a silent truncation.

The assembly logic is in `agentkit/build/assemble.go`; the validation logic is in
`agentkit/spec/load.go`.

### 3.5 Adding a vendor / model

1. Declare the provider in `llm/<vendor>.yaml`. **The protocol layer hardcodes no
   vendor's field names** — `hint_keys` only exists to give an accurate hint when a
   request is rejected.

2. The `reasoning` tier table is the **translation layer**: the agent writes
   `reasoning: high`, and the table decides what this endpoint actually sends.
   ★ **A tier missing from the table is silently ignored** — an agent writes `high`
   while the endpoint has no `high`, the effect is "no thinking at all", and there is no
   error. Always fill the table in for a new endpoint.

3. `reasoning_default` is the tier used when the agent names no `reasoning`.
   Leave it empty and **nothing is injected**.

4. Sampling-parameter precedence (see the assertions in `llm/provider_test.go`):
   **the agent's explicit parameters > `extra` > the vendor default**.
   `extra` is layered, with later layers overriding earlier ones.

### 3.6 Context compaction

A long conversation eventually stops fitting in the model's window. openheron compacts it: the
conversation is replaced by a summary, and the summary is what the agent carries forward.

**The threshold** is per agent, in `agents/<name>.md`:

```yaml
---
model: deepseek/deepseek-flash
summarize_at: 80%              # of the model's context_window
---
```

Three spellings: `80%` (a share of the window), an absolute token count (`104858`), and `0`
(or `off`) for never. Leave it out and the agent gets **80% of its window** — compaction is on
by default.

★ **The threshold belongs to the agent, the window to the model.** The window says how big the
context is; the threshold says how much of it this agent fills before its history is summarized.
One model can serve several agents with different policies. The two meet in exactly one place,
`spec.SummarizeAt(threshold, window)` (`agentkit/spec/summarize.go`) — the builder asks there
instead of resolving either on its own.

★ **A percentage needs a window.** No `context_window` means nothing to take 80% of: that agent
never compacts, and the spec package says so in `deferred` when it loads the configuration
(`noticeCompactionWindows` in `agentkit/spec/load.go`). An absolute token count needs no window
and is never reported.

★ **The old model field still parses, and is not read.** `models[].summarize_at` is deprecated:
it keeps an old configuration loading (the field is `KnownFields`-strict, so deleting it would
make every `llm/*.yaml` that mentions it fail at startup) and the spec package points at the
agent setting instead.

The middleware is mounted on **every agent that declares a threshold** — the root's compaction
is the one that reaches the transcript, a sub-agent's lives and dies inside its own run, where
it still protects that agent's window. The derivation only picks up the **root's** compaction
(`agentkit/emit/emit.go`): a sub-agent's summary is not a boundary of the session's history.

**The chain**, end to end:

| Step | Where | What happens |
|---|---|---|
| the trigger | `agentkit/build/summarize.go` | eino's summarization middleware counts the agent's state before every model call and compacts it past the threshold |
| the finalizer | `finalize`, same file | the replacement history is the agent's system messages **plus the summary** — openheron's context prompt is a system message (`agentkit/run/input.go`), and dropping it would leave the agent without its rules for the rest of the turn |
| the announcement | eino's `after_summarize` event | the middleware publishes the replacement history on the agent's event stream |
| the pickup | `compactionFrom` in `agentkit/emit/emit.go` | the derivation reads the event and puts the compacted message on `Artifacts.Compaction` — the **summary**, not the system message that shares the replacement history |
| the write | `archiveCompaction` in `agentkit/run/loop.go:182` | the message is appended to the transcript **before the turn's own messages** |
| the boundary | `message/compaction.go:103` | the marker rides on that message, so the transcript stays the single source of truth |

★ **The write order is not a detail.** The compacted message summarizes everything that came
before the turn's messages, so it has to be written first. Written later, the summary would sit
at the end of the conversation, where it summarizes nothing.

**The manual compaction** (`/compact`) is the same thing with a different trigger — the user
instead of the token count:

| Piece | Where | What it does |
|---|---|---|
| the summarizer | `build.SummarizerFor` in `agentkit/build/summarize.go` | the agent's own model and the same finalizer, built into an eino middleware whose `Summarize` is called directly — the trigger never fires |
| the write | `run.Compact` in `agentkit/run/compact.go` | the effective history is summarized, the marker is stamped, and the summary is appended to the transcript — the same marker and the same estimate as `archiveCompaction`. The summary covers the **same messages** the automatic one would: the sub-agents' messages are filtered out, because they are not in the root agent's context |
| the endpoint | `POST /v2/sessions/{id}/compact` in `api/v2_compact.go` | refuses while a turn runs or an approval waits; returns what the compaction achieved |

★ **The manual path ignores the threshold.** "Off" means "do not compact on your own", not "you
may not compact": a manual command is the user asking for it. The summarizer is therefore built
independently of the agent's `summarize_at`.

★ **The summarizer is an interface in `run`, not the builder's type.** `run` never imports the
agent package — that is the rule the `AgentBuilder` interface exists for — so `run.Compact` takes
a `run.Summarizer` and the API layer hands it one from the builder.

**Two views of a session**, and which one to ask for:

```go
Messages(effective bool) []*schema.Message
```

- `false` (the default) — the whole transcript: the audit trail, and what the UI renders.
- `true` — the effective history: the last compaction message and everything after it. **This
  is what the agent's context is built from** (`agentkit/run/loop.go`).

★ Compaction is **not** deletion. The superseded messages stay on disk and stay readable; the
marker says where the cut happened.

**What the client does with the marker.** The messages view stamps it on the message
(`api/v2_convert.go:63`), the history hydration reads it (`web/src/lib/store/history.ts:14`),
and the message list draws a divider in place of the chat bubble
(`web/src/components/message-list.tsx:101`, drawn by
`web/src/components/compaction-divider.tsx`). A compaction that happens **mid-turn** is
announced on the stream instead: the transport emits a `CUSTOM "context_compacted"` event
(`transport/transport.go:590`) and the reducer puts a boundary into the list at once
(`web/src/lib/events.ts:412`).

★ The live event carries **no numbers**. The numbers — how many messages were superseded, how
many tokens the summary saved — are measured against the transcript, and the transcript is
written when the turn ends; the transport has no transcript to measure against. The live
divider therefore reads *"earlier messages · compacted into a summary"*, and the counts arrive
with the marker on the next load.

★ The summary must keep the user's own words — eino splices the recent user messages back into
it, and openheron's finalizer leaves that alone. A test pins it.

★ The interface has no optional parameters in Go, so the default is the zero value: `false`
means "everything", and every call site says which view it wants.

### 3.7 The context meter

How full an agent's context is, and where the number comes from.

**The number** is the size of that agent's **most recent request** — the endpoint's own
count, not a sum over the session. `emit.ContextUsageOf` (`agentkit/emit/context.go`) walks the
messages and keeps each agent's last assistant message that carried usage. A session can carry
a large total and an almost empty context; only the last request answers the question.

**The window** comes from the builder: `Builder.Contexts` (`agentkit/build/context.go`) resolves
each agent's model the same way the assembly does, including a sub-agent that inherits its
parent's model. The API layer asks the builder — it must not re-derive which model an agent
runs on.

**Two channels, one number**:

| Channel | Where | When |
|---|---|---|
| the live path | the transport emits a `CUSTOM "context_usage"` event after every model call | while the run goes |
| the history path | `GET /v2/sessions/{id}` carries `usage.context` | after a refresh |

Both call `emit.ContextUsageOf`, so the number on screen and the number after a refresh
cannot disagree.

★ **`usage.byAgent` and `usage.context` are different quantities.** The first is a cumulative
spend; the second is a point-in-time occupancy. The frontend keeps them apart, and a test
keeps the labels apart.

★ **A window of zero means the user configured none.** The occupancy is still reported, but no
ratio is shown — "0 %" would be a lie.

### 3.8 Session admission — one session, one turn

**The rule: a session has at most one live turn.** A second `POST /v2/run` on a busy
session is refused with `409 session_busy`; it never takes the running turn over.

★ **The gate lives in `agentkit/run`, not in the API layer.** The API layer must not
re-implement a rule about the runtime's state (§2.2) — the runtime owns that state, so
the runtime owns the rule.

| Function | Where | Refuses |
|---|---|---|
| `ensureIdle` | `agentkit/run/runtime.go` | a new **query** when a turn executes or an approval waits |
| `ensureNoLiveQuery` | `agentkit/run/runtime.go` | a new **query** when a turn executes — but lets a **resume** through |
| `claimTurn` | `agentkit/run/runtime.go` | nothing on its own: it takes the busy test **and** the takeover under one lock |

**Why a resume is not a query.** A resume continues the interrupted turn it answers — the
same round of the conversation. `contract.TurnKind` says so, and the rule follows:
`conflictTurns` refuses only a query behind a live turn.

★ **A refusal must change nothing.** The checkpoint discard and the pending-approval
clear in `Submit` run **after** the gate. Before this change they ran first, so a
refused request deleted the running turn's checkpoint and approval — the other surface's
turn was destroyed by the request that was supposed to be refused.

★ **The claim is the rule; the check is the message.** `ensureIdle` gives a friendly
error, but two simultaneous requests could both pass it. `claimTurn` takes the test and
the takeover under one lock, so exactly one wins.

#### The three orderings that matter

Each of these was a bug before it was a rule:

| Ordering | Why |
|---|---|
| `Complete` returns **after** the archive lands | The HTTP response goes out before the archive otherwise, so the client is told "done" while the server still says "busy" — and the next turn would read an incomplete history. |
| the pending approval is written **after** the checkpoint | The framework writes the checkpoint when the loop stops; the approval was written inside the callback, i.e. *before* it. The user saw the card, and answering it failed with `no_checkpoint`. |
| the gate reads the **loop**, not the turn pointer | `st.turn` is cleared when the archive lands, so it lags: a turn that already produced its answer still reads as running. |

★ **A gate that reads a lagging pointer is a gate with a hole in it.** When a fact
about a turn is read at two different moments, make the second one **wait** for the
first rather than guessing at it.

#### The frontend half

The composer must read the **server's** answer, not its own memory:

- `GET /v2/sessions/{id}/messages` carries `state`; `seedFromHistory` feeds it into
  `convo.status` (`web/src/lib/store/history.ts`). Without it a second tab hydrates as
  idle and its send button works.
- `runStream`'s `onError` carries the error **code** (`web/src/lib/api.ts`). The prose
  cannot tell "busy elsewhere" from "failed" — the code can.
- A `session_busy` refusal **rolls back the optimistic user message**: the server never
  took it, so the transcript must not claim it (`markBusy` in
  `web/src/lib/store/actions.ts`).

★ **A busy session is not a failed session.** It gets `convo.busy`, not
`status: "error"` — the turn is fine, it just belongs to someone else. The composer
shows the stop button, which is how the user takes over.

### 3.9 CI and releases

`.github/workflows/ci.yml` is the whole pipeline: three checks and one release.

| Job | What it runs | Needs |
|---|---|---|
| `go` | `make lint-go` + `make test` | Go |
| `web` | `make lint-web` + `bun run test:unit` | bun |
| `e2e` | `make test-e2e` — the fake backend it ships with | bun + chromium |
| `release` | `make release`, then publish to a GitHub Release | the three above |

★ **The release waits for the checks** (`needs`), so a tag on a red commit publishes
nothing. That is why the pipeline is one file: a job cannot wait for a job in another
workflow, so two files would have to run the checks a second time.

★ **The smokes are not in CI.** `make smoke` needs a model that answers, and a model
that answers is not reproducible on a runner — a failure there would be noise, not a
signal. The E2E is the deterministic half, and it is the half that can gate a release.

★ **The commands are the repo's own.** CI calls `make lint-go` / `make test` /
`make test-e2e` — the commands §5.2 lists — so the recipe lives in the Makefile and
nowhere else. `make lint-go` and `make lint-web` are the two halves of `make lint`,
split so that a job installs only the toolchain it needs.

#### Cutting a release

★ A release is cut by hand: a **tag on `main`**, pushed. GitHub does the rest.

**Before tagging**

1. `main` is green — the badge at the top of the README, or the last run in
   the Actions tab.
2. The working tree is clean and `main` is up to date: `git pull --ff-only`.
   The tag has to point at a commit that **is on `main`** — `main` is
   protected, so a tag on a side branch publishes something nobody merged.
3. The tag name is decided. `v*` is the convention (`v0.1.0`), and the tag is
   also the version stamped into the binary.
4. ★ **The tag ruleset is `release-tag`** (Settings → Rules → Rulesets). It
   refuses to **delete or move** a tag, and that is deliberate: a failed
   release's tag stays in the repository as the record of what was tried.
   Creating tags is allowed.

**Tagging**

```bash
git checkout main
git pull --ff-only
git tag -a v0.1.0 -m "openheron v0.1.0"
git push origin v0.1.0
```

★ Use an **annotated** tag: its message is what the release page shows.

**What happens next**

The workflow runs on the tag — the three checks, then `release` publishes a
**draft**, then `install` installs the published assets on every platform we
ship for, and only then `publish` turns the draft into the release. A binary
nobody can install never becomes public.

**Check the result**

- The release page lists the six platform archives, `checksums.txt` and the
  two installers.
- Verify a download: `sha256sum -c checksums.txt --ignore-missing`.
- Install it for real:
  `curl -fsSL https://github.com/ouranoshong/openheron/releases/latest/download/install.sh | sh`
  — then `openheron version`.

**When it fails**

★ The release stays a **draft**, so nothing public was published. But the
**tag exists**, and the ruleset refuses to delete or move it. A failed release
is therefore fixed by **cutting a new tag** (`v0.1.1`), not by repairing the
old one — and the failed tag stays in the repository as the record of what was
tried.

To publish an existing tag again, run the workflow by hand and give it the tag.

The two installers (`scripts/install.sh`, `scripts/install.ps1`) travel with the
release, so `releases/latest/download/install.sh` always refers to the script
that belongs to the binaries next to it. `make test-install` checks the installer
against a local fake release: the download, the checksum gate (including a
corrupted archive, which must install nothing), the install and the version
check.

★ **The fake release has to look like the real one — including the URL
shapes.** The API lives on another host than the web
(`https://api.github.com`, not `https://github.com/api`), and a fake server
that serves one path for both hides exactly that kind of mistake: the harness
went green for a request the installer never makes in production.

#### How a change lands in `main`

`main` is protected by a ruleset: every change goes through a pull request, force pushes
and deletion are refused, and the three CI checks must be green.

| Rule | What it means in practice |
|---|---|
| A pull request is required | A direct `git push` to `main` is refused — for the owner too |
| Required status checks | The three CI jobs have to pass |
| Required approvals | 0 — a one-person repository has nobody to approve |
| Stale approvals are dismissed | A new commit invalidates the previous approval |
| Branches must be up to date | The pull request has to contain the current `main` |

★ **The required checks are the job display names** — `go (vet + gofmt + tests)` and so
on. Renaming a job therefore breaks the gate **silently**: the merge button then waits
for a check that never arrives. When a job is renamed, the ruleset has to be updated in
the same breath.

★ **Merging does not publish anything.** The release job only runs for a `v*` tag, so a
merge means "the code is on `main`", not "a binary exists".

## 4. The frontend

### 4.1 The pure layers

| File | Responsibility | Dependencies |
|---|---|---|
| `web/src/lib/sse.ts` | SSE framing: cut a byte stream into events | **none** (zero imports) |
| `web/src/lib/events.ts` | AG-UI event decoding + reducer | `import type` only (erased at compile time) |
| `web/src/lib/store.ts` | **barrel**, re-exporting the below | see below |
| `web/src/lib/store/state.ts` | Type contract | `import type` only |
| `web/src/lib/store/selectors.ts` | Pure selectors + module-level empty constants | `import type` only |
| `web/src/lib/store/history.ts` | Rebuild session state from the REST history | `import type` only |
| `web/src/lib/store/events.ts` | Event routing inside the store | **`localStorage`** |
| `web/src/lib/store/actions.ts` | The zustand store itself + all async actions | **zustand**, `fetch`, `localStorage` |

★ **Be precise about this**: the genuinely framework-independent modules are `sse.ts`,
`events.ts`, `store/state.ts`, `store/selectors.ts` and `store/history.ts`.
`store.ts` itself **is a barrel**, and `store/actions.ts` owns the zustand instance —
so importing `store.ts` pulls in zustand, `fetch` and `localStorage` as well.

`store/events.ts` is not pure either: it writes `localStorage`
(`active-thread.ts`). Purity holds up to `store/history.ts`.

★ **Put new logic in these layers** and keep them pure — correctness is proven by
`bun test`, which is far faster than "changed the UI, now open a browser".

### 4.2 The shape of the reducer

`reduceEvent(state, event)` in `events.ts` is a **pure function**: old state in, new
state out. `handleEvent` in `store/events.ts` wraps it and deals with the "current run
key" (which turn an event belongs to, when several agents run in parallel).

`closeAllOpenParts` finishes every part that is still open (text, reasoning, tool
calls) when the stream breaks — without it the interface would show a spinner bubble
forever.

### 4.3 ★ Reference stability in zustand selectors

This is the easiest trap in the frontend; it shows up as a
`Maximum update depth exceeded` white screen.

**The rule: a selector must return a reference-stable value.**

```tsx
// ✗ allocates a new array on every call → Object.is always says "changed" → endless re-render
const selectMessages = (s: State) => s.convo?.messages ?? []

// ✓ a module-level empty constant
const EMPTY: Message[] = []
const selectMessages = (s: State) => s.convo?.messages ?? EMPTY
```

`web/src/lib/store/selectors.ts` uses exactly this pattern: `EMPTY_MESSAGES` (:16),
`EMPTY_INTERRUPTS` (:17), `EMPTY_CONVERSATION` (:18) and `EMPTY_ATTACHMENTS` (:37).

★ **Never write `?? []` in a selector** — allocating a new array makes the snapshot
always look "changed". A selector that returns an array should be paired with
`useShallow`.

### 4.4 ★ Feature memo: virtualizing long conversations

★ **This is a memo, not a to-do.** It records what we know we want, why, and
what has to be true before it is worth doing. Nothing in the tree depends on
it.

**The problem.** A conversation with hundreds of messages renders hundreds of
DOM rows. Each row holds parsed markdown, a tool card, a reasoning block. The
browser's layout and paint cost grows with the row count, and the scroll
container has to lay the whole list out whenever anything inside it changes.

**What is already done** (and why virtualization is not the first move):

| Done | What it removed |
|---|---|
| the frame batching in `web/src/lib/api.ts` | one state update per token → one per frame |
| `memo` on `MessageRow` and `MessageParts` | every row re-rendering per token → only the touched one |
| the `ResizeObserver` in `web/src/components/message-list.tsx` | one forced layout per render → one per content growth |
| the memoized `MarkdownText` | the markdown of the *finished* messages being re-parsed |

★ What is left is the DOM itself: the rows exist, they are just not re-rendered.
A list of 500 finished rows costs layout and paint once per change, not once
per token — which is usually good enough.

**The proposal.** Virtualize the list: render only the rows in the viewport
(plus a small overscan), and keep the total height with spacers. The obvious
choice is `react-window` — it is small, it does one thing, and it is not a
framework.

**What has to be true first** — these are the reasons it is deferred, not
implementation details:

- ★ **The "stick to the bottom" behaviour must survive it.** The scroll
  container pins itself to the newest message while the user is at the
  bottom, and lets go the moment they scroll up
  (`web/src/components/message-list.tsx` — `pinnedRef`). A virtualized list
  has to preserve that, including the case where the newest message arrives
  while the user is reading something older.
- ★ **The row height is not known in advance.** The rows contain parsed
  markdown, code blocks with syntax highlighting and collapsible tool cards —
  their heights change with their content and with the user's clicks. A
  virtualizer that assumes a fixed height will jump. The measurement pass
  has to re-measure on every content change, which is the expensive part
  that virtualization was supposed to avoid.
- ★ **The tool cards and the reasoning blocks fold.** A collapsed card is
  short; an expanded one is tall. The height cache has to be invalidated
  when the user folds one.

**Acceptance.** A conversation with 500 messages must stay responsive while a
turn streams — measured as: the frame rate does not drop below 30fps during
the stream, and the time between two tokens and their appearance stays under
100ms. Today the equivalent check is the scroll spec
(`web/test/e2e/scroll.spec.ts`).

**How to measure before doing it.** React DevTools' Profiler, recording one
long conversation while a turn streams. The three numbers to look at: the
commit duration of the message list, the layout count, and the time spent in
`MarkdownText`. If the layout count is low and the commit duration is small,
the DOM is not the problem and virtualization would buy nothing.

### 4.5 Build and embedding

- `vite.config.ts`: `@` → `src`; dev port `5173` (`15173` in test mode),
  `strictPort: true`; `/v2` is proxied to `OPENHERON_API_TARGET`.
- `web/dist` is embedded into the binary with `go:embed` (`webui/webui.go`).
- `make build` builds the frontend first, then the Go binary.

## 5. Testing

### 5.1 Port and state isolation

**Different ports do not mean different data.** openheron writes its sessions under
`state/`, so a test writing there pollutes the real history. The test instance treats
`.openheron-test/project` as the project root and **pre-seeds its own** `.openheron/`,
then points `OPENHERON_CONFIG_DIR` at the config root — and since the config root
**never searches upwards**, it structurally cannot reach the repository's copy.
**Ports and state must be isolated together.**

The ports live in `.env.test` at the repository root (committed):

| | Development | Test |
|---|---|---|
| openheron itself | :8080 | **:18099** |
| Vite | :5173 | **:15173** |
| E2E fake backend | — | **:18180** |

To change a port, change only that one file.

### 5.2 Commands

| Command | Covers | Requires |
|---|---|---|
| `make test` | All of Go (`go test ./...`) | — |
| `cd web && bun run test:unit` | The pure layers (reducer / framing / store / highlighting) | — |
| `make test-e2e` | A real browser + a deterministic fake backend | nothing (it brings its own fake backend) |
| `make smoke` | Real-backend integration: SSE framing, history replay, approval wiring | **the test instance running, and a reachable model** |
| `make lint` | Go vet + gofmt + oxlint + tsc | — |
| `make lint-go` / `make lint-web` | The two halves of `make lint`, for a job that only has one toolchain | — |
| `make check` | `lint` + `test` — run this before committing | — |
| `make install-hooks` | Install the pre-commit hooks (gitleaks + gofmt) | `pre-commit` |

To start the test instance:

```bash
make test-env-backend   # foreground, :18099, its own config + its own session directory
make test-env-reset     # only rebuild the test directories, do not start
make test-env-sync      # ★ must be run after you change the development-side agent definitions
```

★ **`make test-env-sync` is mandatory, not optional** — the test instance's
`agents/ llm/ policy/ skills/` are **copied** from the development side, so change an
agent without syncing and you are still testing the old definition.

★ **The smokes need a model that answers.** They run a real turn against the
test instance, and the test instance's `llm/` points at a local endpoint. If
that endpoint is down or slow, the smoke fails — and the failure looks like a
protocol bug rather than a missing model. **Check the endpoint first**
(`curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:1234/v1/models`) and
skip the smokes until it answers.

Individual smoke scripts (they connect to :18099 by default, so **you do not write
`OPENHERON_BASE` yourself**):

```bash
cd web && bun run smoke:interop            # SSE framing, concatenation, history hydration
cd web && bun run smoke:no-undefined-url   # no malformed URLs are emitted
cd web && bun run smoke:hitl               # the approval resume path works
```

### 5.3 Go unit tests

`go test ./...` covers 20 packages. The contracts are pinned mainly here:

| Package | Contract it pins |
|---|---|
| `agentkit/spec` | Config root resolution, strict frontmatter decoding, depth validation, cycle detection, write gates |
| `agentkit/build` | Tool assembly, policy tiers, the gate, topology, nested resume, the skill scope |
| `agentkit/run` | Input construction (a sub-agent's messages do not mix into the parent's history), event bridging |
| `api` | Routing, SSE frames, the error envelope, `threadId` being required |
| `kb` | Indexing, scoring, the catalog format (including legacy Chinese header compatibility) |
| `storage/filestore` | Append writes, atomic rewrites, checkpoints, pending-approval persistence |
| `transport` | Event types, attribution, per-agent usage accounting |
| `initcfg` | The completeness of the factory templates |
| `agentkit/fsx` | The write boundary (including symlink escapes), the `ls`/`glob`/`grep` caps, and absolute glob patterns |

### 5.4 The knowledge-base corpus convention (`docs/`)

The `kb` package's corpus **is this repository's `docs/`** (`repoCorpusDir` in
`kb/corpus_test.go`; override it with `OPENHERON_KB_CORPUS`). So the markdown documents
under `docs/` have to satisfy the extraction contract:

| Requirement | Rule |
|---|---|
| **Has a title** | Line 1 is `# Title` |
| **Has a summary** | One of three: a `description:` field in the frontmatter / a TLDR section under the title (`一句话` `tldr` `summary` `概要` `摘要`) / a **contiguous blockquote right after the title**, `> ...` |
| **Section line numbers are valid** | Every section's `Line` must fall within the document's line count |
| **Sections are not truncated** | The section count must stay under the hard cap (exceeding it is recorded in `SectionsTruncated` and fails the test) |

★ The documents in this repository open with a **blockquote** (the same shape the
upstream documents use):

```markdown
# Title

> One sentence saying what this document covers.
> A second sentence may continue in the same blockquote.
```

A plain-paragraph opening **yields no summary** — `TestExtractAgainstRepoDocs` then
reports `no summary extracted; §6.1 promises every doc in this corpus yields one`.

★ The upstream recall record (`TestRealCorpusRecallMatchesTheV1Record`) is **guarded by
the corpus's identity**: `kb-design.md` present means this is the upstream design
corpus and the record must hit; absent means a different corpus, the record does not
apply, and the case skips loudly (not silently).

★ The corpus holds both language versions of each document. Each of them must satisfy
the contract on its own — the extractor does not read one language to help the other.

### 5.5 Frontend unit tests

`cd web && bun run test:unit` (`bun test test/*.test.ts`) — **14 test files, 271
cases**, covering the pure layers: `sse.ts` / `events.ts` / `store/` / `highlight.ts` /
`thinking.ts` / `turns.ts` / `approval.ts` / `settings-validate.ts` and friends.

★ `store.test.ts` exercises the **whole zustand store**, so it stubs
`globalThis.localStorage` and `globalThis.fetch` before running
(`store.test.ts:35-84`). Bun has no `localStorage` of its own; without the stub it
explodes.

★ Event replay and history hydration **must converge on the same state** —
`store-parity.test.ts` pins exactly that, plus a block of "known differences, pinned,
do not touch" assertions.

### 5.6 E2E

`make test-e2e` → `playwright test`, **8 specs, 124 cases**: `chat` `multi-agent`
`agents` `composer-meta` `composer-autocomplete` `settings` `history` `scroll`.

Playwright starts two servers itself (`webServer` in `playwright.config.ts`): the fake
backend (`web/test/serve.ts`) and Vite (`--mode test`).

The configuration is **one worker, no retries** (`workers: 1`, `retries: 0`,
`fullyParallel: false`) — the E2E suite shares one fake-backend state, and running in
parallel would cross-contaminate.

The fake backend (`web/test/fake-server.ts`, 1300+ lines) creates determinism through
**scenario scripts**: 19 named scenarios (`simple` `approval` `approvalMulti`
`streaming` `multiAgentStream` `error` `slowRun` …) selected with
`POST /__fake/scenario?name=…`, and `POST /__fake/reset` restores threads, settings
and the request log before every case. Thread ids, timestamps and marker strings are
all hard-coded constants.

★ The `slowRun` scenario **deliberately never closes the stream** — it is there to
exercise "how does the interface behave when the stream stalls".

### 5.7 ★ Traps to avoid

**(1) E2E uses the fake backend; do not switch it to the real one.** The real backend
depends on the model cooperating, so a failure cannot tell you whether the interface is
broken or the model misbehaved; turn on `retries` and failures become noise that nobody
reads after the third round. Protocol-level verification against a real backend is
`make smoke`'s job — the two are complementary.

**(2) Let a busy port fail loudly.** `reuseExistingServer: false` in
`playwright.config.ts` and `strictPort: true` in Vite are both deliberate: reusing the
development vite makes E2E "all green" while the browser is talking to the real
backend, and `OPENHERON_API_TARGET` was never injected. **Do not "save time" by
changing it to `!CI`.**

**(3) A force-killed Playwright leaves an orphan holding the port.** The next run
reports `already used`, and that is not a broken configuration — clear the process
first.
★ Do not clear processes **while E2E is running**: one `pkill -x vite` kills the
webServer the current run is using, while Playwright is still waiting for its health
check — which shows up as "the port never comes up, the test hangs at startup". Stop
the test first, then clean up.

**(4) Use `pkill` with `-x`, not with `-f`.** `-f` matches the full command line, so it kills
the very shell running the command (whose command line contains that string).
★ `ps | grep | kill` is the same disease. To kill by name, use `pgrep -x <name>`.
★ When a process name exceeds 15 characters the kernel truncates `comm`, and then `-x`
no longer matches — find the PID by port (`ss -ltnp`) and kill them one by one.

**(5) Put long-running work in a background task, not the shell's `&`.** A background
process does not survive the tool call, which shows up as "it ran for a while and then
vanished" or "the port never came up" — and the log holds only the first line, so
nothing tells you who killed it.

**(6) ★ A one-shot measurement measures the wrong moment.** The composer switches
layout when the conversation renders, which is later than the send that triggers
it. Measuring right after the send catches the composer **before** it moves, and
then "after > before" is false for a composer that has not moved *yet*. It passes
on a fast workstation and fails on a CI runner — which is how a timing bug in a
test looks like a flaky test.
★ Measure with `expect.poll`, the way the neighbouring layout assertions do it:
it waits for the state instead of guessing when it arrives.

**(7) `smoke:hitl` is flaky — roughly 1 in 5.** It depends on the model *choosing* to
call `write_file` in order to trigger an approval. That is the model's freedom, not a
code regression; the script retries, and when nothing triggers it **reports an error
explicitly** rather than pretending the wiring is broken.
★ It has a **precondition**: the agent that actually calls the tool must have
`write_file` in the `ask` tier — neither globally denied nor disabled by its own
`disabled_tools`. With the tool out of reach there is no approval to trigger.
★ Do not look at `main` for this. The factory configuration does give `main`
`disabled_tools: [write_file, edit_file, execute]`, but `main` only orchestrates: it
**delegates** the work to `task`, and `task`'s `write_file` is in the `ask` tier — so
the approval fires anyway. Measured event stream:
`TOOL_CALL_START name=task` (a sub-agent is invoked as a tool), then
`TOOL_CALL_START name=write_file subagentRunId=task`, and the interrupt's
`subagentRunId` is `task` too.
To check whether a tier is reachable, look at **each** agent's effective tool table:
`GET /v2/agents` and `GET /v2/agents/task` report the tier and the reason per tool.

**(8) Changing the interface requires browser verification.** "I changed it and the tests
are green" is not evidence — script-level tests cannot see the rendering layer.

### 5.8 ★ The cost benchmark

`benchmarks/` measures what the multi-agent topology costs against one strong
model doing the same work. It is a measurement with assertions, not a unit
test: the runner sweeps the arms, and `analyze.ts` checks the result.

```bash
# the server, pointed at a scratch config root and a scratch project root
./bin/openheron --config-dir $PWD/.openheron-bench --project-root $PWD/benchmarks/out/work

# the sweep: --arm and --task take comma-separated lists
bun run benchmarks/run.ts --base http://127.0.0.1:18099 \
  --config $PWD/.openheron-bench --work $PWD/benchmarks/out/work \
  --arm a2-max-parent-flash-workers-cited,b-max-solo \
  --task write-a-file,two-file-change,add-a-method-to-three-types --repeats 3

# the analysis: per-phase costs, medians, ratios — and the assertions
bun run benchmarks/analyze.ts --report benchmarks/out/report.json --config $PWD/.openheron-bench
```

★ **Three things about it that are easy to get wrong.**

**(1) `--work` is scratch, and the fixture tree is the source.** The runner
wipes the work directory and copies `benchmarks/fixtures/work/` into it before
every task, so every task starts from the same tree and the checks read the
files the agents left behind. Pointing `--work` at the fixture directory
destroys the fixtures; the runner refuses to start if you do.

**(2) The cost is priced by the model the agent ran on, not by the one the
arm table says it should run on.** The transcript carries it per message — the
agent's name is on the message and the model is in its metadata
(`api/v2_convert.go`). Read that, not the arm table: the arm table is the
fallback for the rare case of one agent running on two models, and that case
is reported (`modelConflicts`) rather than priced silently.

**(3) A run that never finished is not a free run.** The provider bills every
call it made, including the ones that never came back — those produce no
message, so no local accounting can see them. A failed run's cost is a
**lower bound**, and the report marks it `costIncomplete`.

★ **And one trap:** the connection subscribes to the run; it does not consume
it, so **a client that gives up leaves the run going** (`api/v2_run.go`). The
runner therefore calls `POST /v2/sessions/{id}/abort` when it times out —
without that, the sweep keeps paying for calls whose answers nobody reads.
The abort is idempotent, so it is safe on a run that already ended.

★ `--repeats 3` is the minimum for a claim: the model is stochastic, and a
single repeat moved by 2× on the same task. Report the **median**.

The findings, the price table's verification against the provider's bill, and
the two shapes the benchmark measures are in `benchmarks/README.md`.

### 5.9 Secrets in the repository

Two layers, because they see different things:

| Where | What it scans | When |
|---|---|---|
| The pre-commit hook | The **staged** diff | On every commit, locally |
| The `secrets` CI job | The **whole history** | On every push and pull request |

★ The hook is the cheap one, and it is the one that stops a key from being
written. The CI job is the one that finds what the hook cannot: a secret that was
committed before the hook existed, or on a machine without it.

Install the hooks with `make install-hooks` (it needs `pre-commit` — `pipx install
pre-commit`). The gitleaks revision is **pinned** in `.pre-commit-config.yaml`, so
a new upstream rule cannot turn every commit red on a day nobody touched the
security setup.

★ **The rules are not loosened.** When gitleaks reports something that is not a
secret, the fix is a **narrow path allowlist** in a `.gitleaks.toml` — a path
that is known to hold fixtures, not a regular expression that matches "anything
that looks like a key". A broad allowlist is a hole with a config file on it.

★ **The report is redacted** (`--redact`): the log says where the finding is, not
what the secret is. A CI log is not a place to print a live key.

★ **The escape hatch is `SKIP=gitleaks git commit`** — for the emergency case,
where the finding is not a secret and the rule needs fixing first. The CI job has
no escape hatch: that is the point of having it.

## 6. Troubleshooting

| Symptom | Check first |
|---|---|
| Startup reports a missing configuration | The `config root` line in the startup log — it prints the root actually used and the project root |
| A config change does not take effect | Is that partition hot-reloaded? `server/` and `skills/` need a restart |
| An agent cannot call a tool | `GET /v2/agents/:agentId` for the effective tool table; then decide whether it is an inner disable or an outer `deny` |
| `reasoning` seems to do nothing | Is that tier declared in the model profile? Undeclared means silently ignored |
| An approval cannot resume | Is the checkpoint still there? Check `pendingApprovals` on `/v2/sessions/:id` |
| White screen + `Maximum update depth exceeded` | Is some selector returning a new array/object? |
| E2E reports `already used` | An orphan from the previous force-killed run holds the port; clear it and retry |
| The event stream is incomplete | Did the SSE sink break? **Keep consuming** the stream, or the history will be missing |

★ **Archive before you present** — after the SSE sink breaks, stop rendering but keep
consuming the event stream; otherwise `Outcome.Intermediates` is incomplete and the
history loses entries.

## 7. Known issues

Written down so the next person to hit one does not have to rediscover it. **These are
the current state of the code, not a to-do list** — fixing one is a separate change.

| Symptom | Where | Explanation |
|---|---|---|
| **Usage on a live stream is not accounted per agent** | `contract.Usage` has no `byAgent` field; the frontend's `toUsage` at `events.ts:479` maps only flat counts | `usage.byAgent` **exists only with history hydration**. So in a fresh turn the agent filter chips show no cumulative token counts — they appear once you reload (pulling history). The **context occupancy** is live (§3.7): it arrives as its own `context_usage` event. E2E missed the gap because it tests a **seeded history thread**. |
| **A broken SSE stream does not reconnect** | `web/src/lib/api.ts` | A broken stream is simply broken: it goes to `onError` → the error panel, and the user has to resend. There is no reconnect and no resume-from-offset. |
| **The conflict dialog does not show the on-disk version** | `web/src/components/settings/conflict-dialog.tsx:18-23` | It shows only your stale `baseRevision` plus a hard-coded "changed by someone else", and **never fetches the disk version**. There is no "overwrite anyway" button either — "Keep editing" merely clears the conflict marker, so the next save resends the same stale revision and conflicts again. |
| **`web/index.html` declares `lang="zh-CN"`** | `web/index.html:2` | The interface is entirely in English (a hard requirement in AGENTS.md), but the `lang` declaration is still Chinese. It affects what a screen reader reads. |
| **`tw-shimmer` is a ghost dependency** | `web/package.json:39` | The only mention of it in the whole repository is the manifest; no source, style or HTML file references it. |
| **Four runtime dependencies sit in `devDependencies`** | `web/package.json` | `lucide-react` (imported by 10 files), `clsx`, `tailwind-merge` and `class-variance-authority` are all imported by `src/` and bundled into `web/dist`. The build passes (Vite resolves them at build time), but the manifest is wrong. |
| **`web/scripts/interop-check.ts` is out of date** | lines 16-20 and 41-45 of that script | It POSTs to `/v2/run` **without a `threadId`**, while the server now requires a session first (`POST /v2/sessions`, see `api/v2_run.go:78`). So that item of `make smoke` fails (measured: 400 `threadId is required`). `web/scripts/hitl-smoke.ts` and `web/scripts/no-undefined-url-check.ts` already follow the new contract. |
| **The comment at `api/server.go:75` points at a route that does not exist** | that line | The comment says "GET /v2/spec", but `SetSkillsSource` actually feeds `/v2/skills`. |

## 8. Before committing

```bash
make check        # lint + test
```

- `gofmt -l .` must print nothing.
- Runtime output (logs, errors, warnings, interface text, the contents of files written
  into the user's configuration) is **always English**.
- Code comments are in English, and they say *why*, not *what*; do not turn framework
  semantics into a contract.
- Test case names and test fixture data **stay in Chinese** (that is descriptive text,
  not runtime output).
- When you add or remove cases, update the counts in the documentation.
