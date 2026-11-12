# Architecture

> openheron is a multi-agent runtime: every expert agent binds its own model and tool
> boundary, while the parent agent only orchestrates and synthesises. This document
> explains **how the code is actually organised** and **why it is layered this way**.
>
> Companion to [user-manual.md](user-manual.md) (for users) and
> [developer-manual.md](developer-manual.md) (for people changing the code).
> Every conclusion here points at a concrete file and line.

> 中文版：[architecture_zh.md](architecture_zh.md)

## 1. A map: brain, memory, hands, coordination

An AI Agent is not a single model but a system that keeps running toward a goal. Four
capability domains, and where each one lands in openheron:

| Domain | The question it answers | Where it lands in openheron |
|---|---|---|
| **Brain**: model and planning | Who reasons, and how a goal becomes steps | Each Agent binds its own `model`; `agentkit/build` assembles a bounded sub-agent tree; `agentkit/run` drives the think-act-observe loop |
| **Memory**: context and knowledge | What this turn sees, and where history lives | the attachment list in a message's `Extra`, the transcript and checkpoint in `storage/filestore`, corpus retrieval in `kb` |
| **Hands**: tools and execution | What it can do to the outside world | the filesystem middleware, the `execute` tool, tools from `tools/*.yaml`, MCP servers from `mcps/`, skills from `skills/` |
| **Coordination**: orchestration and guardrails | Who may do what, and how failures are contained | one-way layering, the three `tools` / `mcps` / `skills` allow-lists, the `allow` / `ask` tiers, the depth cap, approval and write gates, session admission, `agentkit/obs` |

One HTTP process (`api`) reads a file-backed configuration (`agentkit/spec`), assembles each
Agent into a bounded tree of sub-agents (`agentkit/build`), and hands it to the eino ADK run
loop (`agentkit/run`); the run streams AG-UI events (`transport`) to the browser (`web/`),
state lands in the file store (`storage/filestore`), and approvals resume from a checkpoint
(`approval` + `storage`).

```
  HTTP /v2/*  ──▶  api  ──▶  agentkit/spec  +  agentkit/build  +  agentkit/run  ──▶  transport  ──▶  web/

  brain: model and planning
  ──────────────────────────────────────────────────────────────────────────
    agentkit/build       assemble each agent into a bounded tree
    agentkit/run         the think-act-observe loop
    agentkit/spec/decl   the declarations: model, tools, mcps, skills

  memory: context and knowledge
  ──────────────────────────────────────────────────────────────────────────
    message              the message and its Extra
    storage/filestore    transcript, checkpoint, the run ledger
    kb                   corpus, index, retrieval

  hands: tools and execution
  ──────────────────────────────────────────────────────────────────────────
    fsx                  the filesystem backend
    agentkit/shell       which shell the execute tool runs commands with
    tools/*.yaml         the external tools
    mcps/                the MCP servers
    skills/              the skills
    agentkit/imaging     image compression and the hashed names

  coordination: orchestration and guardrails
  ──────────────────────────────────────────────────────────────────────────
    api                  routing, SSE, the error envelope
    agentkit/spec        configuration, validation, hot reload
    agentkit/obs         the only log outlet
    approval, the depth cap, session admission, the write gates
```

## 2. The brain: model and planning

A model supplies general intelligence, but a model alone is not an Agent. The brain does three
things: binds a model to a role, breaks a goal into executable steps, and drives the
think-act-observe loop so that each observation decides the next step.

### 2.1 Agent definitions

An agent is a markdown file: **frontmatter + body**. The frontmatter is **strictly
decoded** (`agentkit/spec/load.go:76`'s `loadAgent`) — a single wrong key is a startup
error.

★ **A model that nobody declares is not a startup error — it takes that agent out of
the tree.** The rule is stated in full in `docs/config-design.md` §4.6; what the
architecture has to say about it is where the boundary falls: `validate()` **settles**
the tree (`agentkit/spec/load.go:575` `leaveOutUndeclaredModels`) — the agent is dropped
from `Config.Agents` and from every parent's `subagents:`, and the reason is kept in
`Config.leftOut` (`agentkit/spec/config.go:165`). Everything downstream —
`AgentNames()`, `MCPServersInUse()`, the tier table, the builder — sees the settled
tree and therefore never meets an agent it cannot run. ★ Two consequences worth
knowing before touching this code: the default agent is left out like any other (with
no session possible until it is fixed, but a process that starts, which is where the
editor lives), and the write gate (`agentkit/spec/write.go:472` `checkNoAgentLost`)
compares **settled before** against **settled after**, so a change may add an agent and
may leave a broken one alone, but may not take a working one away.

Available frontmatter keys (`agentkit/spec/decl/decl.go:79`'s `decl.Agent`):

| Key | yaml tag | Meaning |
|---|---|---|
| `description` | `description` | What this agent is for (the parent agent reads it) |
| `model` | `model` | `<provider>/<model>`; **never inherited, never falls back** |
| `max_iterations` | `max_iterations` | Cap on tool-loop iterations |
| `subagents` | `subagents` | The sub-agents it may delegate to |
| `tools` | `tools` | The **non-MCP** tools this agent may use — the **exposed** names, the ones the model sees. ★ **Absent means none** (`agentkit/spec/decl/decl.go:113`) |
| `skills` | `skills` | The skills this agent may load — the folders under `skills/`. ★ **Absent means none** (`agentkit/spec/decl/decl.go:134`) |
| `mcps` | `mcps` | The MCP servers this agent **mounts** — each one starts a child process, and mounting one **hands the agent all of that server's tools**. ★ **Absent means no server** (`agentkit/spec/decl/decl.go:150`) |
| `temperature` | `temperature` | Sampling temperature |
| `max_tokens` | `max_tokens` | Sampling cap |
| `reasoning` | `reasoning` | Reasoning tier name (`low`/`high`/`max`…) |
| `summarize_at` | `summarize_at` | This agent's compaction threshold (absolute token count / percentage / `off`) |

`decl.Agent` also carries three `yaml:"-"` fields (`ID`, `File`, `Instruction`) — the
loader fills them in; they are **not** frontmatter keys.

The body is the prompt itself (`Instruction`).

★ **All three lists default to none, and an absent key is an empty list.** The rule is
one line: **only what is listed**. There is no "everything" and no "everything except"
— a tool the list does not name is not the agent's tool, a skill it does not name is
not its skill, a server it does not name is not mounted. ★ The three lists are plain
slices (`decl.Agent.Tools`, `decl.Agent.Skills`, `decl.Agent.MCPServers`), not
pointers: the pointers went away with the change, because "not declared" and "declared
empty" now mean the same thing — nothing.

★ **The old key is refused, not renamed.** `disabled_tools` was the **opposite** of
`tools` — one removed tools, the other names them — so renaming it alone would have
turned a blacklist into a whitelist without saying so. The loader therefore **refuses**
the file (`agentkit/spec/load.go:107`) and says that the meaning is the other way
round. ★ **There is no alias**: an alias that still worked with the old meaning would
keep the blacklist alive, and an alias that worked with the new one would turn a
blacklist into a whitelist on the user's behalf — the exact confusion the refusal
exists to prevent.

★ **MCP has one key, and mounting is the grant.** `mcps:` says which servers are
**mounted** — mounting a server starts a child process — and it also says that the agent
**gets all of that server's tools**. ★ `tools:` does not name MCP tools:
an `mcp__<server>__*` entry there grants nothing, is dropped, and is reported as a
**notice** (`agentkit/spec/load.go:846` `checkAgentMCPNames`) — not a load-time error,
because the meaning changed rather than contradicting itself.
★ **The cost is real**: narrowing one agent's MCP tools is no longer possible. The
server's own file (`mcps/<server>.yaml`) filters its tools, and that filter is shared by
every agent that mounts it. ★ **Approval is untouched**: an MCP tool's tier comes from
that server file's `allows:`, and the default there is still `ask` — mounting is not
approving.

### 2.2 Agent assembly

`agentkit/build` turns "a declaration" into "a runnable agent".

#### The assembly pipeline

`assemble` in `agentkit/build/assemble.go`:

1. Take the agent's `model` reference and resolve it with `build.Resolve`
   (`provider.go:71`) into a `(provider, model)` pair; failing to resolve is a startup
   error.
2. Compute the **effective tool table**: the snapshot's tool set is **one set for the
   whole snapshot**, and each agent narrows it with its own `tools:` allow-list
   (`agentkit/build/topology.go:72` `specTable`). The tier table (§5.5) then decides
   which of the tools that survive ask a human. ★ **There is no "nothing to do"
   shortcut** (`agentkit/build/policy.go:190`): the list is an allow-list, so an
   **empty** list removes **everything** — the shortcut a blacklist could afford
   ("no rules, keep everything") is exactly the case an allow-list must not have.
3. Recursively assemble the sub-tree declared by `subagents` and fill `SubAgents` into
   eino ADK's `deep.Config` (`assemble.go:100`).
4. `WithoutGeneralSubAgent: true` (`assemble.go:18`'s `baseConfig`, and `hub.go:162`) —
   **eino's built-in "general sub-agent" is switched off**, because it would let the
   model invent sub-agents out of thin air and bypass our own topology and depth
   validation.

★ One more thing in `assemble.go:158-161` that is easy to miss: `base.SubAgents = nil`
and `base.Name/Description/Instruction` are cleared — that is the **per-node** assembly
stripping the inherited sub-agent list, so each node hangs its own subtree instead of
copying the parent's.

#### ★ The tree's identity: what it depends on, not which generation

The assembled agents have to be reusable, so they live in a **pool**. The pool's key is
**(name, fingerprint)** (`agentkit/build/pool.go`):

- The **fingerprint** (`agentkit/build/fingerprint.go`) is what that agent itself depends on:
  its own file, the `llm/<provider>.yaml` its `model:` names, the `tools/*.yaml` its `tools:`
  list names, the skills its `skills:` list names, the **live tool list** of the MCP servers it
  mounts, and **its sub-agents' fingerprints** (recursively — the parent's `task` tool
  description carries the child's name and description).
  ⇒ Changing one `skills/<name>/SKILL.md` rebuilds only the agents that read it; the others'
  fingerprints do not move.
  ★ **The contribution is narrow**: a file that says nothing about an agent counts as no
  contribution.
- The **tree's identity** is **(the configuration object, the MCP tool surface)**
  (`agentkit/build/snapshot.go`, `snapshotFor`). A new configuration object is a new tree (a
  reload publishes a new one); a moved tool surface is a new tree too (the tool set is built
  once — a server that comes up without a new tree leaves that tree's tool set stale).
  ⇒ **Nobody announces that the tree moved; it is compared**: the builder compares the current
  identity with the one it holds before it builds.
  ★ A notification can be forgotten (someone forgets to notify); a comparison cannot.

★ **A round pins the tree it started with** (`agentkit/run/runtime.go`, `turn.pin`): the run's
row records that tree's fingerprint (`storage/runledger.go`, `TreeFingerprint`), and the resumed
round asks for the agent **by fingerprint**, not by name for "the current one".
⇒ The interrupted round continues on the tree it started with; **a new configuration takes
effect on the next round**.
★ If the pinned tree is not in the pool (the process restarted) ⇒ one warning, fall back to
the current tree, **never swap it silently**.

### 2.3 The run loop

`agentkit/run` is a thin layer over eino ADK and does three things:

1. **Build the input** (`agentkit/run/input.go:86`): assemble the system prompt + session
   history + this turn's query into an ADK input. A sub-agent's messages **do not mix
   into the parent's history** (`input.go:136`'s `withoutSubagentMessages`) — this is
   where "isolation" lands at the data level.
2. **Drive the loop** (`agentkit/run/loop.go`): run ADK's run and translate its events
   into AG-UI events.
3. **Bridge approvals** (`agentkit/run/bridge.go`): suspend when a tool asks for
   approval, and resume from the checkpoint once the decision comes back.

#### The event stream

The event types come from the `ag-ui-protocol/ag-ui` Go SDK. The frames are rendered by
`transport/transport.go` from the run's events — the derivation (what the agent's events
*mean*) lives in `agentkit/emit`:

| Event | Emitted at | Meaning |
|---|---|---|
| `RUN_STARTED` | `transport.go:61` | The turn began; carries `threadId` |
| `TEXT_MESSAGE_START/CONTENT/END` | `transport.go:246-263` | Assistant body, incrementally |
| `REASONING_*` | `transport.go:269-280` | Reasoning process |
| `TOOL_CALL_START/ARGS/END/RESULT` | `transport.go:283-314` | Tool calls |
| `CUSTOM` (`model_retry`) | `transport.go:232` | Model-retry notice |
| `RUN_FINISHED` | `transport.go:69`, `:75` | The turn ended; carries result and usage |
| `RUN_ERROR` | `transport.go:240`, `api/v2_run.go:283` | Failure — ★ the code says which kind, and the message says why (see `docs/run-lifecycle-design.md` §8.3) |

A sub-agent's output is **attributed through `subagentRunId` on the event** rather than
through a separate event type — that lets the frontend handle every agent's output with
one reducer.

## 3. Memory: context and knowledge

Short-term memory is the current session's context, long-term memory is what lands on disk,
and outside knowledge arrives through retrieval. All three answer one question: what can the
model see this turn.

### 3.1 Storage

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

### 3.2 ★ Attachments belong to a message, not to a session

A file the user sends is bound to **the message it came with** — the list lives in
the message's `Extra` (`message/attachment.go`), and the model sees it because the
message the transcript holds is the very object the input is built from.

★ **A session-level list could not answer the question that matters.** "What does
this session have" is a different question from "that image you just sent", and
only the second one is the one a user asks.

The pieces:

- `POST /v2/run` carries `attachments[]` — a name and a path **relative to the
  session's workspace**. The server resolves it itself (`api/v2_attachments.go`)
  and refuses a name that escapes the workspace; a name outside is rejected,
  not clamped, because a clamped path would read a different file.
- The list is stamped on the user message **as JSON bytes** — `Extra` is encoded
  with gob when the checkpoint is written, and gob cannot encode a value of an
  unregistered type inside an interface.
- The assembly (`agentkit/run/attachments.go`) splits the list: a **text**
  file becomes an `@` line at the head of the model's copy, holding the
  **absolute** path, and the model reads it with `read_file`; an **image**
  becomes a multimodal part, so the model *sees* it. ★ A binary file, or an
  image that cannot be inlined, is named in the prompt as "cannot be
  processed yet" — it gets no path, because `read_file` on bytes that are
  not text produces noise.
- ★ **The header is a view, not a rewrite.** The transcript keeps the user's
  own words and the relative path in `Extra`; the header is rebuilt for
  history messages too, so "that file you just sent" still names a file.
- ★ **The model gets a copy when a picture is inlined.** The transcript's
  message keeps its text and never carries the base64 (the transcript is a
  JSONL log with an 8 MiB line limit), and the copy's text moves into a
  **text part** — the wire has one `content` field that is either a string or
  a list, and the openai adapter builds both when a message carries a
  `Content` and a multi-content at the same time.
- ★ **There is one request and no fallback** (`llm/common/model.go`). A model
  with no vision fails the turn with the provider's own error; the user
  retries. `llm/common/files.go` and `llm/common/vision.go` are gone. ★ The
  context prompt makes `read_file` an obligation, and asks for `offset` and
  `limit` when a file is large.
- `GET /v2/sessions/{id}/messages` carries the list back in the message's
  metadata, so the history shows which file went with which question.

★ **Deletion is per file, the binding is per message.** `DELETE
/v2/sessions/{id}/files?path=` removes a file from the workspace; a file that
already went out with a message stays with that message, because the message
says the file was part of the question.

### 3.3 The knowledge base

`kb` is an **optional** local corpus retrieval layer.

- The corpus is a set of markdown documents; the directory comes from `corpus_dir` in
  `tools/kb.yaml` (relative to the project root).
- The index lands in `<data-dir>/kb/<index>/` (`build/kb.go:45`), with a `catalog.md`
  index page.
- **Retrieval is a hybrid score**: `scoreDoc` in `kb/score.go:53` computes
  `0.5*lexical + 0.5*cosine` when vectors are available, is **purely lexical** without
  them (which is why `kb status` reports `lexical-only retrieval`), and falls back to
  **pure cosine** when the lexical score is zero.
- The lexical score **weights identifiers**: a term appearing in the title, a section
  heading or a link is worth more than one buried in the prose.
- `openheron kb index` takes a **run lock** (`kb/store.go:146`'s `StartRun`): only one
  run at a time.
- Agents get two tools: `kb_search` (returns a document **outline**, not the full text)
  and `kb_index`. `kb_index` defaults to the `ask` tier — letting an agent rebuild the
  index needs a human nod.

## 4. The hands: tools and execution

Tools are what take an Agent from saying to doing. This part covers where tools come from, how
they are mounted onto an Agent, and how skills are scoped to a particular Agent.

### 4.1 Where tools come from

| Source | Assembled at | Examples |
|---|---|---|
| Filesystem middleware | `agentkit/build/snapshot.go:101` | `ls` `read_file` `write_file` `edit_file` |
| The execute tool | `agentkit/build/execute.go:40` | `execute` |
| Network tools | `build/nettools.go` | `web_search` `http` |
| Knowledge base | `build/kb.go` | `kb_search` `kb_index` |
| Encyclopedia | `build/nettools.go:232` | `wikipedia` |
| The configuration itself | `agentkit/build/configtools.go:53` | `config_get` `config_validate` `config_template` `config_notices` `config_tier` `config_write` |
| MCP servers | `agentkit/build/mcptools.go:194` | `mcp__<server>__<tool>` |

★ **The config tools are always mounted and almost never visible.** They join the batch
for every agent, and an agent sees the ones its own `tools:` list names — like every
other tool that is not an MCP one. Five of them only read and are in the code's exception list; `config_write`
is not, and cannot be added to it from a file (`docs/config-design.md` §6.3).

★ **The tier of a tool is decided by the file that provides it.** A file's own
`allows:` names the tools that run without a nod — `tools/http.yaml` names
`request_get`, `tools/kb.yaml` names `kb_search` — and **everything else that file
provides asks**. The built-in tools that have no file of their own are named in the
code (`agentkit/build/tools.go:92`): the readers (`ls` `read_file` `glob` `grep`) and
the delegation tools (`task` `write_todos` `skill`). An MCP server says it in **its
own vocabulary** — the server's tool names, not the exposed ones.

The external network tools need **no code change**: a provider whose configuration is
absent mounts nothing, so writing the file is what turns the tool on (`tools/*.yaml`).
★ The filesystem middleware is the exception — `fsx.New`
mounts it unconditionally (`agentkit/build/snapshot.go:101`), and the knowledge base is
switched by its **corpus**, not by its file.
★ Note that `openheron init` writes the tool configurations under `tools/` — the
directory is part of the tree the factory ships, because the factory agents name
`request_get`, `duckduckgo_search` and `wikipedia_search`, and a name no provider
offers is reported as a typo. `--no-tools` leaves it out.

The path a tool takes is the same for every source:

`tools/*.yaml` → `spec.loadTool` (`agentkit/spec/load.go:247`) → `decl.ToolsCfg` →
`toolRegistry.Build` (`agentkit/build/tools.go:147`) → `snapshot.buildHeavy`
(`agentkit/build/snapshot.go:84`) → `applyToolConfig` (`agentkit/build/assemble.go:30`) →
`deep.Config.ToolsConfig.Tools` → `deep.New` (`agentkit/build/assemble.go:43`).

The MCP branch joins it at the registry: `mcps/<server>.yaml` → `spec.loadMCP`
(`agentkit/spec/load.go:373`) → `decl.MCPServerCfg` → the MCP provider
(`agentkit/build/mcptools.go:194`), registered at `agentkit/build/tools.go:55` → the same
`toolRegistry.Build` → the same `applyToolConfig`.

The heavy dependencies (the fsx backend and the tool batch) are built once, under a
`sync.Once` (`agentkit/build/snapshot.go:52`). ★ **The MCP client is the one resource on
this path that does not belong to the snapshot.** The provider answers a single question —
"what tools does this snapshot have" — and returns a **batch** of them. The client behind
that batch outlives the batch: it is owned by the manager (`agentkit/build/mcpmanager.go:44`),
because a stdio server is a **child process**, and a child process started for one turn
must still be there for the next one. The provider is **optional** — an unreachable server
is skipped with a warning, never an error, so one dead server cannot keep the agent down.

★ **A server that will not answer must not take the agent down with it.** The teardown of
a failed start waits for the child to exit, and a child that is still installing its own
package does not exit because its stdin closed — so the child is killed first and the
teardown is bounded (`killMCPChild` / `closeMCPClient` at
`agentkit/build/mcpmanager.go:840-856`, with the grace at
`agentkit/build/mcpmanager.go:864`).
A build waits for a server, not for a server's corpse.

★ **The start does not stand on the turn's critical path.** Every mounted server is
started in the **background** when the configuration is known (`agentkit/build/mcpmanager.go:98`
`Warm`), and each server keeps its own state — `not_started`, `starting`, `ready` or
`failed`. A server that is not up yet contributes no tools, and the turn runs without
them; when it comes up, the tool surface changes, the builder notices the difference,
and **the next turn** has its tools.

★ The exposed name is `mcp__<server>__<tool>`, composed in exactly one place
(`agentkit/build/mcptools.go:141`). The server's own name is the namespace segment, and
which server an exposed name belongs to is read back out of it
(`agentkit/spec/decl/decl.go:549` `MCPServerOf`). ★ **The namespace is not negotiable**:
a server name may not contain a run of underscores, so the prefix of one server cannot
reach another.

### 4.2 skills resolve by a different rule

`skills/` is a **nested partition** in `Categories` (`skills/<name>/SKILL.md`), but its
`loader` is nil: the manifest, the path rules and the editor belong to `spec`, while the
content is resolved independently by `build`'s skill backend
(`agentkit/build/skill.go:190` `ResolveSkillsDir`): `$EINO_EXT_SKILLS_DIR` first, and only
if that is unset does it search **upwards** from the current directory for
`.openheron/skills`.

★ **The editor has to ask this question too, and the answer is not assumed.** It
writes `<config root>/skills/`, which is the directory in force only when the probe
landed there; when it did not, a save would land in a tree nobody reads. So the
resolved path is pushed into the config layer at startup (`spec.Service.SetSkillsDir`,
`agentkit/spec/config.go:355`), the write is refused when it points elsewhere
(`agentkit/spec/write.go:151` `validateWritable`), and the manifest marks the section
read-only with a note naming the real directory. ★ The rule lives in `build` only: a
second copy in `spec` would be a second answer to "which skills are loaded".

★ This is a different rule from "the config root never searches upwards". Skills are a
directory tree, and searching upwards lets you reuse skills from your home directory;
the config root is a set of entities, and searching upwards would make the same command
read different configurations.

★ **Which skills an agent may use is a scoped backend.** The `skills` key in
`agents/<name>.md` is an allow-list of the folders under the skills directory, and it
reaches the middleware as `scopedSkillBackend` (`agentkit/build/skill.go:78`). The scope
filters both halves: `List` (`agentkit/build/skill.go:153`) — the skill tool's
description is rendered from it, so it is what the model **sees** — and `Get`
(`agentkit/build/skill.go:167`) — what the model can actually **load**. Filtering
only one half would leave a skill that a model remembers the name of loadable.

★ **The list is a plain slice, and the default is none.** `decl.Agent.Skills`
(`agentkit/spec/decl/decl.go:134`) is `[]string`, not a pointer: "not declared" and
"declared and empty" now mean the same thing — no skill — so there is nothing left to
distinguish. ★ A skill the list does not name is not the agent's skill, and the skill
tool is only worth naming when there is a skill to load — which the loader says so
when the two contradict each other (§2.1).

★ The skill middleware is attached to the **entry agent** — the one the session
selected (`agentkit/build/assemble.go`). A sub-agent does not get the skill tool at all,
so there the list has nothing to filter.

## 5. Coordination: orchestration and guardrails

This part answers "who is allowed to do what": dependency direction, how configuration loads,
tiers and approvals, the depth cap, session admission, and the observation and containment of
what execution produced. The HTTP surface, the frontend and the system boundaries live here
too — they cut across all four domains instead of belonging to one of them. Most of openheron
lives here — the boundaries, more than the capabilities, decide whether an Agent is usable.

### 5.1 Layering and dependency direction

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
| | `agentkit/obs` | none | **The logging assembly**: it *builds* loggers (`New`, `ParseLevel`, `Discard`) and owns the sink — there is no global logger to reach for. Its subpackages `agentkit/obs/hertz` (takes over hertz's log output), `agentkit/obs/hook` (eino's callbacks) and `agentkit/obs/httplog` (the model request log) are leaves too |
| | `llm/common` | none | Vendor protocol layer (zero openheron dependencies, **zero log code**) |
| | `webui` | none | Embedded frontend assets |
| | `agentkit/fsx` | none | **The filesystem backend**: a bounded local backend — writes confined to the project root, ignore rules, result caps (see [fs-backend-design.md](fs-backend-design.md) — that document is Chinese only) |
| | `agentkit/shell` | none | **Which shell runs the commands**: it picks a POSIX shell (or `cmd.exe`) at startup, and it is also the sentence the `execute` tool's description tells the model — the dialect and the runner have to be the same answer |
| | `agentkit/imaging` | none | Compresses uploaded pictures, derives the hashed file names and the thumbnails |
| | `agentkit/templates` | none | **The configuration templates**: the files `openheron init` writes and the config editor scaffolds. ★ It is a leaf so that both the configuration layer and the command layer can read the **same** file — a copy would drift |
| **L1** | `agentkit/spec/decl` | none | **Pure declarations** (where the yaml tags live) **and the shared vocabulary** — the built-in tool names and the MCP namespace (§5.1) |
| | `agentkit/emit` | `obs`, `approval`, `contract`, `helpers`, `message` | The derivation: agent events → the run's facts |
| | `storage` | `contract` | Interfaces for sessions/messages/pending approvals |
| | `kb` | `obs`, `persist` | Knowledge base: corpus, index, retrieval, scoring |
| | `agentkit/spec` | `obs`, `decl`, `persist`, `templates` | Config loading, validation, hot reload, write gates |
| **L2** | `agentkit/build` | `fsx`, `obs`, `obs/httplog`, `spec`, `decl`, `approval`, `contract`, `helpers`, `kb`, `llm/common`, `message` | Agent assembly, tool assembly, the tier table (`agentkit/build/policy.go`) and the approval gate |
| | `agentkit/run` | `emit`, `obs`, `approval`, `contract`, `decl`, `message`, `storage` | Run loop, input construction, event consumption |
| | `storage/filestore` | `obs`, `contract`, `message`, `persist`, `storage` | File store implementation |
| | `storage/memarbit` | `storage` | The in-memory lease arbiter |
| | `transport` | `contract` | AG-UI rendering: the run's events → frames |
| | `llm` | `build`, `spec`, `decl` | Thin wrapper (only `common/` and one test file) |
| | `initcfg` | `spec`, `persist`, `templates` | Factory templates for `openheron init` |
| **L3 entry** | `api` | `build`, `emit`, `imaging`, `obs`, `run`, `spec`, `decl`, `contract`, `message`, `storage`, `transport`, `webui` | HTTP routing, SSE, error envelope |

The same layering, top to bottom, is the direction of the imports:

```
  L3   api                      routing · SSE · the error envelope
       │
  L2   agentkit/build           assembly · the tool registry · the tier table
       agentkit/run             the loop · input · events
       storage/filestore        the file store
       storage/memarbit         the lease arbiter
       transport                AG-UI rendering
       llm                      a thin wrapper over llm/common
       initcfg                  the factory templates
       │
  L1   agentkit/spec/decl       the declarations and the shared vocabulary
       agentkit/emit            agent events → the run's facts
       storage                  the session interfaces
       kb                       corpus · index · retrieval
       agentkit/spec            config load · validate · reload · write gates
       │
  L0   contract  message  helpers  persist  approval  agentkit/obs
       llm/common  webui  agentkit/fsx  agentkit/shell  agentkit/imaging  agentkit/templates
```

The table counts **internal** dependencies only — the third-party packages are not in it.
The MCP path is where two of them enter the tree:
`github.com/cloudwego/eino-ext/components/tool/mcp` (the eino adapter) and
`github.com/mark3labs/mcp-go` (the protocol client), both in `agentkit/build`.
`agentkit/run` imports `agentkit/spec/decl` for the namespace constants alone — the run's
ledger records the servers a turn touched. `storage/filestore` is imported only by the
composition root, `main.go:34`; `api` does not import it.

#### Two dependency rules that must not be broken

**(1) There is exactly one logging assembly.** `agentkit/obs` **builds** loggers (`New`,
`ParseLevel`, `Discard`) and owns the sink; it does not hand out a global one. The composition
root builds the `*slog.Logger` and injects it, so every package that logs takes a
`*slog.Logger` parameter and defaults it to `obs.Discard()`. Levels (Info/Warn/Error) and
prefixes are defined in that one place, and the level decides whether a line shows up in
the default output — so a business package calling `log.Printf` or
`fmt.Fprintln(os.Stderr)` directly bypasses the switch.

The format is hertz's, so the whole output reads as one stream: the date, the time with
microseconds, the caller's file and line, the level, then the message and the attributes
(`agentkit/obs/handler.go`):

```
2026/10/29 22:28:34.346459 main.go:189: [Info] [main] openheron version=dev
```

`llm/common` used to write the request log with the standard logger, and that code moved to
`agentkit/obs/httplog` (`docs/logging-design.md` §6) — the leaf now carries no log code at all.
Any openheron-internal import inside `llm/common` is still a regression.

★ The logger is built at the composition root (`main.installLogger`) around a shared
`slog.LevelVar`, so `server/config.yaml`'s `log_level` and `OPENHERON_LOG_LEVEL` can move the
threshold after the handler exists. Every package takes a `*slog.Logger` parameter; there is
no global to reach for.

**(2) Declaration and behaviour are separate.** `agentkit/spec/decl` holds **plain
structs** and yaml tags (`agentkit/spec/decl/decl.go`) and imports no other package;
`agentkit/spec` holds the **behaviour** (loading, validation, reloading, writing). That
way `decl` can be passed freely between `build`, `api` and `initcfg` without dragging a
dependency chain along.

★ **The vocabulary the two halves share lives in `decl` too, and that is a
dependency-direction fact, not a convenience.** The built-in tool names
(`decl.BuiltinToolNames`, `agentkit/spec/decl/decl.go:348`) are read by **both** ends:
the loader checks the names an agent's `tools:` list mentions against them, and the
builder refuses a provider that claims one of them (`reservedToolNames`,
`agentkit/build/tools.go:126`). Neither end may import the other — the loader sits
below the builder — so the list cannot live in either of them: it lives **below both**,
in the leaf package. ★ A second copy would be two lists that drift, and the drift
would read as "that tool does not exist".

#### Why `agentkit/spec` does **not** import `kb`

**Validating** the knowledge-base config (`tools/kb.yaml`) needs to know whether the
corpus path is relative, whether the model is declared at all, and whether it is
declared as `kind: embedding` (`agentkit/spec/load.go:295` `loadKB`, `agentkit/spec/load.go:944` `validateKB`).

★ All three answers live in the **declarations** (`decl.Model.IsEmbedding`,
`decl.Model.Dimensions`), so the configuration layer does not import the knowledge base
at all — only a test file does (`agentkit/spec/policy_contract_test.go:7`). The
dependency direction therefore stays one-way without `kb` being involved in it: `kb`
depends on `obs` and `persist`, and `agentkit/spec` depends on `obs`, `decl` and
`persist`.

#### Why `agentkit/build` imports `llm/common` and not `llm`

The `llm` package imports `build` (it is a thin wrapper over `build.Resolve`). If `build`
imported `llm` back, that would be a cycle. `build` uses the protocol types in
`llm/common` directly, which avoids the cycle and keeps the protocol layer a leaf. Today
`llm/` holds only `common/` and `provider_test.go`; the imports of `build`/`spec`/`decl`
appear in that test file only.

#### The composition root assembles the logger

The logger is built once, in `main`, and handed down; no package reaches back for it
(`main.newLogger`, `main.go:192`):

```
main(): newLogger() -> installLogger() -> hlog.SetLogger(hertzlog.New(log))
      -> callbacks.AppendGlobalHandlers(hook.New(log))
      -> setupConfig(log, level, sink) -> applyLogLevel / applyLogFile
      -> serve(ctx, st, log)
```

★ **The order is part of the design.** `newLogger` builds the logger over a shared
`slog.LevelVar` and an `obs.Sink`, **before** the configuration is read — the configuration
is what names the level and the file, and the logger has to exist before `setupConfig` can
log anything. So the threshold and the destination move **after** the handler exists:
`applyLogLevel` moves the threshold (`OPENHERON_LOG_LEVEL` > `log_level` > `info`) and
`applyLogFile` re-points the sink at the file.

★ **Two framework hand-offs, and neither is optional.** hertz keeps a process-global logger
registry and eino a process-global callback registry, so `hlog.SetLogger` and
`callbacks.AppendGlobalHandlers` each happen once, in the composition root, before
`hserver.New`. Those two are the only global registrations openheron makes; everything else
is injected.

★ **The destination moves, the logger does not.** `obs.Sink` is the `io.Writer` behind the
handler, so `log.file` can point it at a file after startup (`docs/logging-design.md` §10).
hertz and eino already hold the logger, and a process-global registry cannot be rewritten —
so the sink is the thing that changes.

### 5.2 The config root has exactly three levels

```
--config-dir  →  $OPENHERON_CONFIG_DIR  →  <project-root>/.openheron
```

Implemented in `agentkit/spec/config.go`'s `NewService`. **Ancestor directories are never
searched** — once the config root depends on "where the process was started from", the
same command run from a different directory reads a different configuration, and a test
instance would read the development configuration too.

Only a **defaulted** root is created automatically (`agentkit/spec/config.go:455`'s `os.MkdirAll`). An
explicit root or an env root pointing nowhere is **not** created: a typo must not silently
grow an empty configuration tree.

`spec.NewServiceAt` is a second entry point, used by `openheron init`: it **honours only
the path passed in** and does not read the environment variable — otherwise init would
write the new configuration somewhere the user did not point at.

### 5.3 Directory partitions

| Directory | Category constant | Hot reload | Contents |
|---|---|---|---|
| `agents/` | `spec.DirAgent` | ✅ | One `.md` per agent |
| `llm/` | `spec.DirLLM` | ✅ | One `.yaml` per vendor |
| `tools/` | `spec.DirTools` | ✅ | `http.yaml` / `web_search.yaml` / `wikipedia.yaml` / `kb.yaml` |
| `mcps/` | `spec.DirMCP` | ✅ | One `.yaml` per MCP server |
| `server/` | `spec.DirServer` | ❌ | Process parameters; needs a restart |
| `state/` | `spec.DirState` | — | Sessions and checkpoints; auto-created |
| `skills/` | `spec.DirSkills` | ✅ | `skills/<name>/SKILL.md`, a nested partition resolved independently (see §4.2) |

`Categories` has **six** partitions (`agentkit/spec/config.go:59-104`); five are hot
(`agents/` `llm/` `mcps/` `skills/` `tools/`, see `HotDirs` in `agentkit/spec/reload.go`),
and `server/` is not (`agentkit/spec/config.go:71`). The `skills/` fingerprint walks the
tree recursively, so editing a `SKILL.md` takes effect on the next turn.

★ **There is no `policy/` partition.** A `policy/` directory left over from an older
configuration is **not read** — nothing loads it — and the tier model it used to carry now
lives in the tool files and the agent files instead (§5.5). ★ The configuration **still
starts**: the rest of it may be perfectly good, and a broken file elsewhere would keep it
from loading at all. But the directory raises a **notice**
(`agentkit/spec/config.go:618` `noteRetiredDirs`) that names its new home — a dropped
configuration that says nothing is the worst kind, because the process starts fine and the
setting is gone.

File names under `tools/` are a **whitelist**: `agentkit/spec/load.go:290` errors out on
an unknown file instead of ignoring it — "I wrote kb.yaml and it did not take effect" is
one of the hardest classes of problem to debug.

★ The `mcps/` partition holds **one file per server**
(`agentkit/spec/config.go:26`'s `spec.DirMCP`): the file's presence **is** the definition
— there is no `enabled` key — and the file's name is the server's name, which is also the
namespace segment of that server's tools.

★ **A hot partition that changes does not swap the tree on the spot**: the **write path only
marks it**, and the next turn takes the marks, reads the configuration again and publishes it
before it builds (`agentkit/build/builder.go`, `reloadIfMarked`).
⇒ "The next turn uses it" is a **promise**, not a race with the watcher's poll — the poll would
notice the same change a moment later, but a turn that starts in between would run the old tree.
★ The marks are a **cache, not the truth**: after a restart the marks are empty and the answer
is still right, because the truth is the content hash on disk.

### 5.4 Two loading modes and "deferred"

`spec.Service.Load()` (`agentkit/spec/config.go:537`) validates strictly — a failure is a startup
failure. `LoadUnchecked()` (`agentkit/spec/config.go:560`) skips validation.

`Config.Deferred()` (`config.go:203`) collects **non-fatal** problems (a duration string
that will not parse, for instance), and surfaces them as a `deferred` array in
`/v2/health` and `/v2/config`. The dividing line is: **anything that would make runtime
behaviour wrong is fatal; anything that is merely a hint is deferred.**

### 5.5 The tier model: `ask` by default, the exceptions named

There are **two** tiers, and the default is the safe one:

| Tier | Meaning |
|---|---|
| `allow` | The tool runs without a nod |
| `ask` | The tool interrupts the run and waits for a human decision |

`build.Policy.TierOf` (`agentkit/build/policy.go:55`) answers the tier of one tool
name: a name in the allow list is `allow`, and **every other name is `ask`**. The
mirrored declaration `decl.Tier` (`agentkit/spec/decl/decl.go:258`) holds `TierAllow`
and `TierAsk` and nothing else — there is no third tier.

★ **The default is `ask` because the cost of guessing wrong is asymmetric**: one extra
click against a destructive call nobody saw. The old model — a global list where an
unlisted tool was allowed — had the opposite default, and it was wrong in the
direction that hurts.

The **exceptions are named**, and they are named by the file that provides the tool:

| Source | What it names | Assembled at |
|---|---|---|
| The built-in tools that have no file of their own | `ls` `read_file` `glob` `grep` `task` `write_todos` `skill`, plus the five config tools that only read | `agentkit/build/tools.go:92` `builtinAllowedTools` |
| A tool's own file — `tools/*.yaml` and the knowledge base | that file's `allows:` | `agentkit/spec/load.go:336` `AllowedTools` |
| An MCP server's own file | that server's `allows:`, in the server's own vocabulary, translated to the exposed names | `agentkit/build/mcptools.go:173` `mcpAllowedNames` |

`agentkit/build/spec.go:64` `policyOf` puts the three together and `Policy.TierOf`
reads the result. ★ The table is assembled from the **configuration**, so the tier of
a tool is answerable before its server is even up.

★ **One name is removed after the merge, not left out of it.** `config_write` edits the
configuration, including the file whose `allows:` list would grant it — a file that
could grant it grants itself. `alwaysAsk` (`agentkit/build/tools.go:117`) strips it from
the assembled list, so the exceptions come from three sources and there is exactly one
place that can take one away.

★ **Two questions, and they are kept apart.** "Does this tool need a human?" is the
**tier table** — `Policy.TierOf` — and it is **shared**: every agent reads the same
table. "May this agent use this tool at all?" is the **agent's own list** —
`Table.Blocked` (`agentkit/build/policy.go:163`), the list the agent carries as
`decl.SubAgentSpec` — and it is **per agent**: a tool the list does not name is not in
that agent's tool set, so the model never sees it.

★ **A blocked tool keeps its own tier**, and says why in its `reason` — and there are
now exactly **two** reasons: `tools` for a tool that is not an MCP one
(`agentkit/build/introspect.go:36` `reasonOf`), and `mcps` for an MCP tool whose server
this agent does not mount (`LayerNotMounted`). ★ The old second reason is gone with the
layer it named: a server the agent does not mount has no tools to block, and one it does
mount brings **all** of its tools. So `Table.Blocked` answers `LayerNotLoaded`
(`agentkit/build/policy.go:251`) or `LayerNotMounted` (`agentkit/build/policy.go:258`).

### 5.6 Depth cap and topology validation

`agent.max_depth` in `server/config.yaml` (default `1`) is the cap on delegation depth,
and `retry.max_retries` sits next to it.

★ **The caps live in `server/config.yaml` only.** The `policy/limits.yaml` half — a
second copy of the same keys that could only *tighten* the file — is gone, and
`server/` is **not** a hot partition (`agentkit/spec/config.go:71`), so a cap change
takes effect after a restart.

A declared topology that exceeds the cap is a **startup error**
(`agentkit/spec/load.go:972`'s `validateDepth`) — never a silent truncation. A sub-agent
referencing an unknown agent and a cyclic topology are startup errors too
(`agentkit/spec/load.go:1043` `validateAgentGraph`, `agentkit/spec/load.go:1060` `detectCycle`).

### 5.7 Write gates

The write path of the config editor (`/v2/settings`) has several gates:

- `server/` is **not editable** (`api/v2_settings.go`) — it is not hot-reloaded, so a
  change would need a restart, and the API rejects it outright.
- Literal secrets are rejected: `api_key must be a reference, not a literal: use
  ${ENV_VAR} or @file:/path` — this keeps secrets out of the repository.
- Optimistic concurrency: the write carries `baseRevision`; a mismatch with the revision
  on disk is reported as a conflict.
- A change may not take a **working agent** out of the tree (`agentkit/spec/write.go:472` `checkNoAgentLost`): the settled tree on disk is compared with the settled trial tree,
  and a write, a batch or a delete that would drop an agent that used to work is
  refused. ★ An agent that was already out of the tree is out on both sides, so an
  unrelated edit is never counted as taking it out.
- A **delete** that something still names is refused (`agentkit/spec/write.go:720` `checkNotReferenced`), and the refusal names the referrer: which file still names the
  deleted one, and what to remove. ★ The gate runs before the trial load, because the
  trial load's error is the symptom it replaces — it names the deleted file and hides
  the real reason somewhere else.

### 5.8 The tool gate

`agentkit/build/gate.go` implements "may this tool be used":

- The name is in the allow list → the tool runs, and the gate returns the endpoint
  untouched (`agentkit/build/gate.go:67`).
- The name is not → the call **interrupts** and waits for a human decision
  (`agentkit/build/gate.go:89` for the streaming path, which is the same gate).
- A tool the agent's `tools:` list does not name is **not in its tool set at all** — the
  table removes it before the gate ever sees it, so the model cannot even see it.

★ **There is no `deny` branch.** A tool an agent may not have is not registered for it,
so the gate never has to refuse one: "this agent may not use it" is a fact about the
agent's tool set, not about the tool.

When a human denies a call, the tool returns **a denial message** (with an optional
reason), **not** an error — the model sees "denied, the reason is X" and can adjust
accordingly.

★ The MCP tools go through the **same** gate: they are looked up **by name** like every
other tool (`agentkit/build/policy.go:55`'s `TierOf`), so `mcp__<server>__<tool>` asks
or runs exactly like any other name. The gate does not change — only the namespace does.

### 5.9 Approval resume

The approval request is persisted (`storage`'s `PendingApproval`), and the **checkpoint
store** is the source of truth for resuming. The client sends its decision back in the
`resume[]` array of the same `POST /v2/run` (each entry carries `interruptId` plus
`payload.approved` / `payload.reason`).

Without the checkpoint there is nothing to resume from — the API then returns
`this approval can no longer be resumed: its checkpoint is gone. Send a new message to
continue.` (`api/v2_types.go:131`) instead of silently starting a fresh turn.

### 5.10 Session admission

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

The run's final row is appended by `runRow.finish` (`agentkit/run/runrow.go:63`) after the
messages are archived; `v2Run` waits for `WaitArchived()` / `WaitReleased()`
(`api/v2_run.go:258-259`) before closing the stream. That is what keeps the client's
"your turn is done" and the server's "this session is free" from disagreeing — and it is
why the next turn's input never reads an incomplete history.

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

### 5.11 The HTTP layer

`api` exposes only the `/v2` family (the old unprefixed API is gone; `/health` returns
404 in practice).

- Routes are registered in one place, `api/v2_files.go:40-87`.
- The error envelope is produced by `v2Fail` in `api/v2_types.go`:
  `{"error": "...", "code": "...", "retryable": false}`.
- **`/v2` routes are registered before the catch-all `GET /*`** — the SPA fallback can
  never swallow an API path.
- **No authentication and no CORS.** The default `:8080` binds every interface; set
  `listen_addr` explicitly to narrow it.
- The agent view (`api/v2_introspect.go:118`) reports an agent's tool allow-list as
  **`tools`** — the list itself, not the tools it removes. ★ `skillsRestricted` is
  gone: it existed only because an absent list and an empty one differed, and they no
  longer do.

`threadId` is **required** by `POST /v2/run` (`api/v2_run.go:139`) — create the session
with `POST /v2/sessions` first, then start the run.

### 5.12 The frontend

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

★ The **width** of the chat is one token, not a number repeated in six places:
`--chat-column` (the column), `--prose-column` (the text inside it) and `--chat-gutter`
live in `web/src/index.css`. The column is wide so that a table fits whole, and the prose
stops earlier — a paragraph of sixty characters is a hard read.

★ The **theme** is decided before the first paint, by a script in `web/index.html`, and read
from one place (`web/src/lib/theme.ts`). The light and the dark palette are the same token
set with two values, so a color that exists in one exists in the other.

★ **Motion is CSS-only, and it obeys the system's setting.** The durations and the easings
are tokens in `web/src/index.css` (`--motion-duration-*`), and one global
`prefers-reduced-motion` rule collapses every animation — including the ones
`tw-animate-css` brings — to 0.01ms, so a user who asks for less motion gets the same
interface without the travel. The design is `docs/animation-design.md`.

★ **The brand mark is generated, not drawn twice.** `web/public/favicon.svg` is the source;
`web/src/components/brand-mark.generated.tsx` is written from it by
`web/scripts/generate-brand-mark.ts`, and a unit test fails when the two drift. The mark
is inline SVG, because an SVG inside an `img` is a separate document and the page's
stylesheet cannot reach its parts.

### 5.13 Observability

`agentkit/obs` is the only log sink. Levels and prefixes are defined there and nowhere
else. `agentkit/obs/hook` provides observation hooks (it depends on `obs`, never the
other way round).

### 5.14 Boundaries and what is deliberately absent

Writing these down is cheaper than explaining them later. The following are **not
present today**:

- **No authentication, no multi-tenancy.** The default `:8080` is open on every
  interface.
- **No TLS.** Put a reverse proxy in front to expose it publicly.
- **No distribution.** A single process, a single binary; sessions live on local disk.
- **The knowledge base supports local directory corpora only**, not remote fetching.
- **The old unprefixed HTTP API is gone**; there is only `/v2`.
- **`skills/` still searches upwards**, which differs from the config root's rule
  (see §4.2).
