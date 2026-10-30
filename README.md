# <img src="web/public/favicon.svg" alt="openheron" width="72" height="72" /> openheron

**A multi-agent runtime that treats "expert agents" as tools**: every expert agent binds
itself to a model that suits it, while the parent agent only orchestrates and
synthesises — tokens are spent where they actually matter.

Three mechanisms deliver the "token saving":

- **Expert gets expert model** — every agent declares its own `model:` in
  `agents/*.md`, so a simple retrieval does not pay for reasoning capability it does not
  need, and a complex task does not settle for a small model.
- **Tools are isolation** — an expert agent is called as a **bounded capability**: its
  own tool loop and intermediate reasoning stay in its context, and the parent agent
  receives only the structured summary it wrote.
- **Structure is savings** — tokens are saved not by compressing prompts but by drawing
  boundaries so that content which should not cross does not cross.

At the same time, "who may do what" becomes a few hard rules:

- **Tool permissions have two layers and are never merged** — the outer
  `policy/permissions.yaml` sets the global `ask`/`deny`, and each inner agent narrows
  it with its own `disabled_tools`. The outer `deny` beats the inner one, and **a sub
  agent cannot relax it**.
- **Delegation has a depth cap** — the entry agent is level 0, and an agent at depth `d`
  may spawn children if and only if `d < max_depth`; a topology over the cap is a
  **startup error**, never a silent truncation.
- **One session can run several agents** — independent work is dispatched in parallel;
  usage is **accounted per agent**.
- **Configuration is files** — model profiles, prompts and approval policy are files you
  can read and edit.
- **One binary, runs anywhere** — the frontend is embedded into a single file, compiled
  statically across platforms.

★ Stating the boundaries up front is cheaper than explaining them afterwards:

- Isolation is **complete**: an expert's intermediate steps **never enter any turn of
  the parent's context**; and observability is **equally complete** — the expert's
  reasoning and tool calls appear live in the interface (with attribution) and stay in
  the record.
- The token saving is largest on tasks that are **decomposable with verbose tool
  output**; on strongly order-dependent tasks a single agent may be faster and more
  accurate.
- "High performance" means **useful output per token**; it promises neither zero
  latency nor unlimited concurrency.
- Expert-models-only pays off **when the pairing is right**: the factory configuration
  has two agents sharing one model. A wrong model pairing wastes more than no pairing.

★ This repository is the **code face**: a Go backend, a React frontend and the build
configuration, with seven document sets alongside
(`docs/architecture.md`, `docs/user-manual.md`, `docs/developer-manual.md`,
`docs/context-usage-design.md`, `docs/session-concurrency-design.md`,
`docs/run-lifecycle-design.md`, each with a `_zh` Chinese version).
The first three describe what the code **is**; the next two are designs whose
implementation **is already in the code** — context-usage estimation (the usage
dashboard and the persistence of compaction results) and session concurrency (the
one-turn-per-session gate and the cross-client 409).
The last one is a **proposal**: `docs/run-lifecycle-design.md` describes how a turn
is bound to one SSE connection today, and how a run could become a durable row with
a lease and a fence, so the connection becomes a subscriber. Nothing beyond its
§1 is in the code yet.
The finer-grained design documents and the industry comparison live in the **upstream
repository** under `docs/` — for the mechanism breakdown and the trade-offs, see the
upstream `docs/positioning.md`.

> 中文版：[README_zh.md](README_zh.md)

## Quick start

```bash
make build                       # build (frontend + a single binary with the UI embedded)
cd /your/project && openheron init   # generate a minimal runnable configuration in your project
export DEEPSEEK_API_KEY=sk-...  # secrets are read from the environment only
openheron                            # start, then open http://localhost:8080
```

`openheron init` writes four files: `agents/main.md`, `agents/task.md`,
`llm/<provider>.yaml` and `policy/permissions.yaml`. Existing files are **kept, not
overwritten** (unless you pass `--force`), so running it in an already-configured
project is safe.

## Command line

| Command | Effect |
|---|---|
| `openheron [--config-dir <path>] [--project-root <path>]` | Start the server |
| `openheron init [--dir] [--model] [--provider] [--base-url] [--env] [--no-policy] [--force] [--dry-run] [--json]` | Generate a minimal runnable configuration |
| `openheron kb index [--config-dir] [--json] [files...]` | Build the knowledge-base index |
| `openheron kb search [--config-dir] <query>` | Search the knowledge base |
| `openheron kb status [--config-dir] [--json]` | Inspect the index (no embedding service needed) |
| `openheron version` | Print the version |

The listen address, directories and the like can all be overridden by environment
variables: `LISTEN_ADDR`, `DATA_DIR`, `PROJECT_ROOT`, `FRONTEND_MODE`, `STATIC_DIR`.

### How the config root is decided

**Exactly three levels, no upward search**:

```
--config-dir  →  $OPENHERON_CONFIG_DIR  →  <project-root>/.openheron
```

The third (defaulted) level **is created** when it does not exist; the first two are
**not** created when they point nowhere — a typo must not silently grow an empty
configuration tree. In that case startup reports `config: load <root>: ...` rather than
the `openheron init` hint (that hint is for the default root only).

`openheron init` deliberately **does not read** `$OPENHERON_CONFIG_DIR`: it writes to
the directory you point it at, and reading the environment variable would write the new
configuration somewhere else.

If the default root has no `agents/main.md`, startup collapses into a single sentence:

```
no configuration at /path/to/.openheron: run `openheron init` to create one
```

## What the configuration looks like

The directories under the config root split by "is it hot-reloaded":

| Directory | Contents | Hot reload |
|---|---|---|
| `agents/` | One `.md` per agent: frontmatter + body (the body is the prompt) | ✅ |
| `llm/` | One `.yaml` per vendor: endpoint, secret, model list, reasoning tier table | ✅ |
| `policy/` | `permissions.yaml` (approval tiers), `limits.yaml` (quota caps) | ✅ |
| `tools/` | External tools and the knowledge-base config — **enabled by the file's presence** | ✅ |
| `skills/` | `<name>/SKILL.md`, eino's native skill convention | ❌ see below |
| `server/` | The process's own parameters: listen address, directories, timeouts, depth cap | ❌ needs a restart |
| `state/` | Sessions and checkpoints (auto-created, **do not commit**) | — |

`skills/` sits differently from the other directories: it is not "a partition of the
config root" but is **resolved independently** — `$EINO_EXT_SKILLS_DIR` first, and only
if that is unset does it search **upwards** from the current directory for
`.openheron/skills`. In other words it keeps its upward search while the config root no
longer has one.

`.openheron-example/` holds a complete worked example, and
`.openheron-example/README.md` walks through the traps of every field (secrets must be
written as `${ENV}`, models as `<provider>/<model>`, durations with a unit).

### Agent definitions

```yaml
---
description: "task: the concrete executor. Writes files per the task order…"
model: deepseek/deepseek-flash
max_iterations: 50
reasoning: low
disabled_tools: [kb_index]
---
```

The frontmatter is **strictly decoded** — one wrong key is a startup error, never
silently ignored. Available keys: `description`, `model`, `max_iterations`,
`subagents`, `disabled_tools`, `temperature`, `max_tokens`, `reasoning`.

- `model` is required, in the form `<provider>/<model>`, and is **never inherited and
  never falls back** — failing to resolve it is a startup error.
- `subagents` declares the topology; every referenced agent must exist and the graph
  **must not contain a cycle**.
- `reasoning` names a **tier** (such as `high`), which `models[].reasoning` in
  `llm/*.yaml` translates into the differing fields each vendor uses. **If that table
  is missing, the tier is silently ignored** — an agent says `reasoning: high` while
  the endpoint has no `high`, which means "no thinking at all", with no error at all.

### The precedence between the two permission layers

| Layer | Where | Scope |
|---|---|---|
| Outer | `deny` in `policy/permissions.yaml` | Every agent |
| Inner | `disabled_tools` in `agents/*.md` | This one agent only |

The two are **stored separately and never merged** — merging would destroy the
information about *which* layer blocked something. The outer `deny` wins, and it is
the one guarantee a sub agent cannot relax.

There are only two columns of tiers: **a tool that is not named defaults to `allow`
(straight through)**. To make a tool need a human every time, list it under `ask`; to
disable it outright, list it under `deny`.

### The depth cap

`agent.max_depth` in `server/config.yaml` (default `1`, meaning only the entry agent may
delegate) and the same key in `policy/limits.yaml` **resolve to the stricter of the
two**. A declared topology over the cap is a **startup error**, never a silent
truncation.

## The HTTP API

Only the `/v2` family is exposed (the old unprefixed API is gone). **No
authentication, no CORS** — the default `:8080` binds every interface, so set
`listen_addr` explicitly to narrow it.

| Method | Path | Effect |
|---|---|---|
| POST | `/v2/sessions` | Create a session (returns `threadId`) |
| GET | `/v2/sessions` | List sessions |
| GET | `/v2/sessions/:id` | Session details (including pending approvals and past decisions) |
| PATCH | `/v2/sessions/:id` | Rename it |
| PATCH | `/v2/sessions/:id/agent` | Change the session's agent |
| DELETE | `/v2/sessions/:id` | Delete the session |
| GET | `/v2/sessions/:id/messages` | Replay the history + usage (filter with `?agent=`) |
| GET | `/v2/sessions/:id/runs` | The run ledger: every run of the session + the active one |
| GET | `/v2/sessions/:id/runs/:runId` | One run of the session |
| POST | `/v2/sessions/:id/abort` | Abort the running turn |
| POST | `/v2/sessions/:id/compact` | Compact the conversation now |
| POST/GET | `/v2/sessions/:id/files` | Upload / list session workspace files (32 MiB per upload) |
| GET | `/v2/project/files` | List the files under the project root (`?q=` `?limit=`) |
| GET | `/v2/commands` | The slash-command list |
| POST | `/v2/run` | **SSE entry point**: ask a question, or answer an approval |
| GET | `/v2/skills` `/v2/agents` `/v2/agents/:agentId` | Skills / agents and their effective tool tables |
| GET | `/v2/config` `/v2/health` `/v2/runtime` | Effective config / health / runtime status |
| GET/POST/PUT/DELETE | `/v2/settings/item` | Config editor reads and writes (`baseRevision` for optimistic concurrency) |
| GET | `/v2/settings` | The list of editable items |
| POST | `/v2/settings/validate` | Validate a configuration without writing it |

The `POST /v2/run` body: `threadId` (**required** — get it from `POST /v2/sessions`),
`query`, and `resume[]` when answering an approval (each entry carries `interruptId`
plus `payload.approved` / `payload.reason`).

The response is an AG-UI event stream with the event types `RUN_STARTED`,
`TEXT_MESSAGE_START/CONTENT/END`, `REASONING_*`, `TOOL_CALL_START/ARGS/END/RESULT`,
`CUSTOM` (`model_retry` notices), `RUN_FINISHED` and `RUN_ERROR`. A sub agent's output
is **attributed through `subagentRunId` on the event**, not through a separate event
type.

★ **A session runs one turn at a time.** A second `POST /v2/run` on a session that is
already running gets `409` with the code `session_busy` — the running turn is not
disturbed, and nothing about it is discarded. See
[docs/session-concurrency-design.md](docs/session-concurrency-design.md).

### Approvals (human in the loop)

When an `ask`-tier tool is called, the run **interrupts** first and the approval
request is persisted into the session; the client receives an approval card and sends
its decision back in the `resume[]` of the same `POST /v2/run`. Resuming relies on the
**checkpoint store** — the checkpoint is the source of truth for approval resume, and
the framework reads it to decide between resuming and starting a fresh turn.

When an approval is denied, the tool returns a denial message (with an optional
reason), **not** an error — the model sees "denied, the reason is X" and can adjust
accordingly.

## The knowledge base

`kb` is an **optional** local corpus retrieval layer: the corpus is a set of markdown
documents, and the index lives in `<data-dir>/kb/<index>/` (with a `catalog.md` index
page and the derived data).

- `tools/kb.yaml` configures `corpus` (relative to the project root), `index`, `model`
  (which must be a `kind: embedding` model), `batch_size`, `top_k` and so on.
  **Not configuring `corpus` means the knowledge base is off**.
- `openheron kb index` builds the index (there is a run lock: only one run at a time),
  `search` queries it, `status` inspects it.
- On the agent side there are two tools: `kb_search` (retrieval, returning a document
  outline rather than the full text) and `kb_index` (builds the index).
- `kb_index` defaults to the `ask` tier — letting an agent rebuild the index needs a
  human nod.

## The frontend

React 19 + Vite + zustand. The build output in `web/dist` is embedded into the binary
with `go:embed`.

- `frontend_mode` defaults to `embed` (use the interface baked into the binary); `dir`
  reads the build output from disk at `static_dir` (relative to the project root).
- Three layers: `web/src/lib/sse.ts` / `web/src/lib/events.ts` / `web/src/lib/store.ts` are
  framework-free pure functions, with correctness proven by `bun test`; put new logic in
  these layers.
- The interface is mounted on a catch-all `GET /*` with an SPA fallback to
  `index.html`; the `/v2` routes are registered first, so an API path can never be
  swallowed by the SPA.

## Development

```bash
make build-go   # backend only (no frontend rebuild) — fastest while iterating on Go code
make web-dev    # Vite dev server (:5173, proxies /v2 to :8080)
make dev        # rebuild and restart the Go binary automatically (air)
make check      # lint + test
```

### Testing

The test environment isolates **both ports and state**, defined in `.env.test` at the
repository root (committed):

| | Development | Test |
|---|---|---|
| openheron itself | :8080 | **:18099** |
| Vite | :5173 | **:15173** |
| E2E fake backend | — | **:18180** |

```bash
make test-env-backend   # start the test instance in the foreground (:18099, its own config + session directory)
make test-env-reset     # only rebuild the test directories, do not start
make test-env-sync      # ★ must be run after you change the development-side agent definitions
make test-e2e           # a real browser + a deterministic fake backend
cd web && bun run test:unit   # the pure layers (reducer / framing / store / highlighting)
make smoke              # real-backend integration (SSE framing, history replay, approval wiring)
```

Changing the interface **requires browser verification** — script-level tests cannot
see the rendering layer. E2E uses a fake backend and must not be switched to the real
one: the real backend depends on the model cooperating, so a failure cannot tell you
whether the interface is broken or the model misbehaved.

## Related documents

This repository ships five document sets, maintained alongside the code, each in
English and Chinese (**English is the primary version, `_zh` is the Chinese one**):

| Document | Contents |
|---|---|
| [docs/architecture.md](docs/architecture.md) · [中文](docs/architecture_zh.md) | **Architecture** — layering and dependency direction, the configuration system, assembly, the run loop, storage, HTTP, the frontend, the knowledge base |
| [docs/user-manual.md](docs/user-manual.md) · [中文](docs/user-manual_zh.md) | **User manual** — installation, configuration, operating the interface |
| [docs/developer-manual.md](docs/developer-manual.md) · [中文](docs/developer-manual_zh.md) | **Developer manual** — layering, the dependency rules, development recipes, testing and troubleshooting, known issues |
| [docs/context-usage-design.md](docs/context-usage-design.md) · [中文](docs/context-usage-design_zh.md) | **Context usage estimate — design** — the occupancy metric, the data flow, the backend and frontend changes, and how the compacted conversation is persisted (the meter, the compaction and the compaction marker are in the code) |
| [docs/session-concurrency-design.md](docs/session-concurrency-design.md) · [中文](docs/session-concurrency-design_zh.md) | **Session concurrency — design** — why a second surface could cut into a running turn, the one-turn-per-session rule, and how the frontend learns that a session is busy (the gate is in the code) |
| [docs/run-lifecycle-design.md](docs/run-lifecycle-design.md) · [中文](docs/run-lifecycle-design_zh.md) | **Run lifecycle — design** — why a turn is bound to one SSE connection today, and how a run could become a durable row with a lease and a fence, so the connection becomes a subscriber (a proposal: only §1 describes the code) |

The **rest** of the design documents under `docs/` live in the upstream repository and
are not distributed with this one:

| Document | Contents |
|---|---|
| `docs/implementation-audit.md` | **Implementation audit** — what was not built, where the docs drifted (2026-10-23) |
| `docs/hygiene-research.md` | **Code hygiene research** — exported symbols with no exit, third-party archives, dead code (2026-10-23) |
| `docs/agent-architecture-survey.md` | **External agent architecture survey** — the gateway and background-service problems of pi / rakazo / OpenClaw / Hermes / Letta (2026-10-24) |
| `docs/positioning.md` | **Positioning and trade-offs**: expert-gets-expert-model × agent-as-tool — the mechanism breakdown and the boundaries |
| `docs/architecture.md` | Overview and the index of the volumes (**same name, but it describes the upstream volume structure**) |
| `docs/config-design.md` | Configuration schema, layering, validation, hot reload |
| `docs/agent-design.md` | The agent pool, approvals, depth protection |
| `docs/multi-agent-design.md` | Multiple roles, parallel delegation, per-agent accounting and history filtering |
| `docs/api-v2-design.md` | The `/v2` API family and the AG-UI event contract |
| `docs/frontend-design.md` | React frontend architecture and interface |
| `docs/config-editor-design.md` | The config editor (six `/v2/settings` routes + the interface) |
| `docs/checkpoint-store.md` | Checkpoint Store: the source of truth for approval resume |
| `docs/kb-design.md` | The knowledge base |
| `docs/init-design.md` | `openheron init` |
| `docs/llm-common.md` | The vendor protocol layer |

Two more convention documents live in this repository:
**[AGENTS.md](AGENTS.md)** (the project conventions for the people and AI working here;
**[AGENTS_zh.md](AGENTS_zh.md)** is the Chinese version) and
**[.openheron-example/README.md](.openheron-example/README.md)** (how to use the
factory configuration sample).
