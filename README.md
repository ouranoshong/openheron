# <img src="web/public/favicon.svg" alt="openheron" width="72" height="72" /> openheron

**A multi-agent runtime that treats "expert agents" as tools**: every expert agent binds
itself to a model that suits it, while the parent agent only orchestrates and
synthesises — tokens are spent where they actually matter.

- **Expert gets expert model** — every agent declares its own `model:` in `agents/*.md`,
  so a simple retrieval does not pay for reasoning capability it does not need.
- **Tools are isolation** — an expert agent is called as a **bounded capability**: its own
  tool loop and intermediate reasoning stay in its context, and the parent agent
  receives only the summary it wrote.
- **Structure is savings** — tokens are saved by drawing boundaries, not by compressing
  prompts.

"who may do what" becomes a few hard rules:

- **Tool permissions are named, and the default is to ask** — a tool nobody names asks
  first; the exceptions are the ones a file's own `allows:` list names, and each agent
  says which of them it may use at all.
- **Delegation has a depth cap** — the entry agent is level 0, an agent at depth `d` may
  spawn children if and only if `d < max_depth`; a topology over the cap is a **startup
  error**, never a silent truncation.
- **One session can run several agents** — independent work is dispatched in parallel.
- **Configuration is files** — model profiles, prompts and tool tiers are files you
  can read and edit.
- **One binary, runs anywhere** — the frontend is embedded into a single file, compiled
  statically across platforms.

★ The boundaries, stated up front: the token saving is largest on tasks that decompose
with verbose tool output — on strongly order-dependent tasks a single agent may be faster
and more accurate; and "high performance" means useful output per token, it promises
neither zero latency nor unlimited concurrency.

> 中文版：[README_zh.md](README_zh.md)

## Quick start

```bash
task build                           # build (frontend + a single binary with the UI embedded)
cd /your/project && openheron init   # generate a minimal runnable configuration in your project
export DEEPSEEK_API_KEY=sk-...       # secrets are read from the environment only
openheron                            # start, then open http://localhost:8080
```

★ **Or install a released binary** — one command, on Linux, macOS or Windows:

```bash
# Linux / macOS
curl -fsSL https://github.com/ouranoshong/openheron/releases/latest/download/install.sh | sh
```

```powershell
# Windows
irm https://github.com/ouranoshong/openheron/releases/latest/download/install.ps1 | iex
```

The installer picks the asset for your platform, verifies its sha256 against the
release's `checksums.txt`, and installs it into `~/.local/bin` (`INSTALL_DIR` to
change that). Pin a version with `OPENHERON_VERSION=v0.1.0`. Prefer to look before
you run? Download `install.sh` from the
[releases page](https://github.com/ouranoshong/openheron/releases/latest) and read it.

`openheron init` writes the whole tree: `agents/main.md`, `agents/task.md`,
`agents/agent-expert.md`, `agents/skill-expert.md`, `llm/<provider>.yaml`,
`server/config.yaml`, the five files under `tools/`, the three MCP servers
`mcps/fetch.yaml`, `mcps/time.yaml` and `mcps/deepwiki.yaml`, and the four skills
`skills/openheron-config-<name>/SKILL.md`. Existing files are **kept, not overwritten**
(unless you pass `--force`), so running it in an already-configured project is safe.

Pass `--no-tools` to leave `tools/` out. The tools are part of the tree because the
agents name `request_get`, `duckduckgo_search` and `wikipedia_search`, and a name that
no provider offers is reported as a typo.

★ **`tools/filesystem.yaml` is the one that does not switch anything on.** The filesystem
tools are always mounted, so that file only tunes their tiers: `allows:
[write_file, edit_file, execute]` is how an agent that edits and runs commands on its own
is spelled. Absent, the readers run without a nod and the writers ask.

★ **The three MCP servers need `uvx` on the PATH**, and the two stdio ones download
their package on the first start. A server that cannot be started costs that server's
tools and nothing else — the process still starts, the log says
`[mcp] the server is unreachable`, and `/mcps` shows the server as `unreachable` with the
reason. Starting a server is lazy, so nothing is attempted at boot.

## Command line

| Command | Effect |
|---|---|
| `openheron [--config-dir <path>] [--project-root <path>]` | Start the server |
| `openheron init [--dir] [--model] [--provider] [--base-url] [--env] [--no-tools] [--force] [--dry-run] [--json]` | Generate a runnable configuration |
| `openheron kb index [--config-dir] [--json] [files...]` | Build the knowledge-base index |
| `openheron kb search [--config-dir] <query>` | Search the knowledge base |
| `openheron kb status [--config-dir] [--json]` | Inspect the index |
| `openheron version` | Print the version |

The listen address, directories and the like can all be overridden by environment
variables: `LISTEN_ADDR`, `DATA_DIR`, `PROJECT_ROOT`, `FRONTEND_MODE`, `STATIC_DIR`.

## Where the configuration lives

**Exactly three levels, no upward search**:

```
--config-dir  →  $OPENHERON_CONFIG_DIR  →  <project-root>/.openheron
```

The default level is created when it does not exist; the first two are not — a typo must
not silently grow an empty configuration tree.

| Directory | Contents | Hot reload |
|---|---|---|
| `agents/` | One `.md` per agent: frontmatter + body (the body is the prompt) | ✅ |
| `llm/` | One `.yaml` per vendor: endpoint, secret, model list, reasoning tier table | ✅ |
| `tools/` | External tools and the knowledge-base config — a provider without a configuration mounts nothing. Each file's `allows:` names the tools that run without a nod | ✅ |
| `mcps/` | One `.yaml` per MCP server: transport, command or URL, timeouts, and the server's own `allows:` | ✅ |
| `skills/` | `<name>/SKILL.md`, eino's native skill convention | ❌ |
| `server/` | The process's own parameters: listen address, directories, timeouts, depth cap | ❌ needs a restart |
| `state/` | Sessions and checkpoints (auto-created, **do not commit**) | — |

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
tools: [ls, read_file, write_file, edit_file, glob, grep, execute, write_todos]
mcps: []
---
```

The frontmatter is **strictly decoded** — one wrong key is a startup error, never
silently ignored. Available keys: `description`, `model`, `max_iterations`,
`subagents`, `tools`, `mcps`, `skills`, `temperature`, `max_tokens`, `reasoning`.

- `model` is required, in the form `<provider>/<model>`, and is **never inherited and
  never falls back** — failing to resolve it is a startup error.
- `subagents` declares the topology; every referenced agent must exist and the graph
  **must not contain a cycle**.
- `tools` is the allow-list of the tools this agent may use; the names are the ones the
  model sees. **Not declaring it means no tools at all** — the list is the whole answer.
- `mcps` is the list of MCP servers this agent mounts; mounting one starts a child
  process, so **not declaring it means no server**. A server's tools are named by their
  composed names (`mcp__<server>__<tool>`, or the pattern `mcp__<server>__*`).
- `skills` is the allow-list of skills this agent may use; the names are the folders
  under `skills/`. **Not declaring it means no skill**, and declaring an empty list
  means the same.
- `reasoning` names a **tier** (such as `high`), which `models[].reasoning` in
  `llm/*.yaml` translates into the differing fields each vendor uses. **If that table is
  missing, the tier is silently ignored** — an agent says `reasoning: high` while the
  endpoint has no `high`, which means "no thinking at all", with no error at all.

### The permission model

A tool that nobody names **asks** — the run interrupts and waits for a human. The
exceptions are the tools that run without a nod, and each one is named by **the file that
provides it**:

| Where | What it names | Scope |
|---|---|---|
| `allows:` in `tools/*.yaml` and `mcps/<server>.yaml` | the tools of that file (or that server) that run without a nod | Every agent |
| `tools` in `agents/*.md` | the tools this agent may use at all | This one agent only |

★ The two are **stored separately and never merged** — merging would destroy the
information about *which* layer blocked something. The tier table is shared and a file's
`allows:` only **adds** to it; an agent's own `tools` list decides what that agent sees.

★ **A file speaks its own vocabulary.** `tools/http.yaml` says `request_get`, and an MCP
server says `create_issue` — not `mcp__github__create_issue`. The translation to the name
the model sees happens once, where the tool is mounted.

★ There is **no global deny list**. "Not exposed" is said in exactly two places: a file's
own `allows:` list, and an agent's `tools` list. A third list would be one more thing
to keep in sync — and it was the only one that had to speak the exposed names while the
tools' own files speak theirs.

★ **The old `disabled_tools` key is refused**, with a message that says which way round
the two are: `tools` says which tools an agent **gets**. Renaming the key alone would
have turned a blacklist into a whitelist without saying so.

### The depth cap

`agent.max_depth` in `server/config.yaml` (default `1`, meaning only the entry agent may
delegate). A declared topology over the cap is a **startup error**, never a silent
truncation.

## The HTTP API

Only the `/v2` family is exposed (the old unprefixed API is gone). **No authentication,
no CORS** — the default `:8080` binds every interface, so set `listen_addr` explicitly
to narrow it.

| Method | Path | Effect |
|---|---|---|
| POST | `/v2/sessions` | Create a session (returns `threadId`) |
| POST | `/v2/run` | **SSE entry point**: ask a question, or answer an approval |
| GET | `/v2/sessions/:id/messages` | Replay the history + usage |
| POST | `/v2/sessions/:id/abort` | Abort the running turn |
| GET | `/v2/agents` `/v2/agents/:agentId` | Agents and their effective tool tables |
| GET | `/v2/mcps` | The MCP servers of one agent (`?agent=<agentId>`, optional) |
| GET | `/v2/config` `/v2/health` `/v2/runtime` | Effective config / health / runtime status |
| GET/POST/PUT/DELETE | `/v2/settings/item` | Config editor reads and writes |

The full list, the request bodies and the event types are in
[docs/user-manual.md](docs/user-manual.md) §9. `POST /v2/run` requires `threadId` (from
`POST /v2/sessions`).

★ **A session runs one turn at a time.** A second `POST /v2/run` on a session that is
already running gets `409` with the code `session_busy` — the running turn is not
disturbed, and nothing about it is discarded.

## The knowledge base

`kb` is an **optional** local corpus retrieval layer: a set of markdown documents indexed
under `<data-dir>/kb/<index>/`. Configure it in `tools/kb.yaml` (no `corpus_dir` means the
knowledge base is off), build the index with `openheron kb index`, and let agents
retrieve with `kb_search`. `kb_index` defaults to the `ask` tier — letting an agent
rebuild the index needs a human nod.

## Development

```bash
task build-go   # backend only (no frontend rebuild) — fastest while iterating on Go code
task web-dev    # Vite dev server (:5173, proxies /v2 to :8080)
task dev        # rebuild and restart the Go binary automatically (air)
task check      # lint + test
```

`task test-e2e` drives a real browser against a deterministic fake backend; `task smoke`
runs against the test instance and needs a reachable model. The ports, the test
environment and the traps around them are in
[docs/developer-manual.md](docs/developer-manual.md) §5.

## Related documents

This repository ships **four bilingual document sets**, maintained alongside the code
(**English is the primary version, `_zh` the Chinese one**):

| Document | Contents |
|---|---|
| [docs/architecture.md](docs/architecture.md) · [中文](docs/architecture_zh.md) | **Architecture** — layering and dependency direction, the configuration system, assembly, the run loop, storage, HTTP, the frontend |
| [docs/user-manual.md](docs/user-manual.md) · [中文](docs/user-manual_zh.md) | **User manual** — installation, configuration, operating the interface |
| [docs/developer-manual.md](docs/developer-manual.md) · [中文](docs/developer-manual_zh.md) | **Developer manual** — layering, the dependency rules, development recipes, testing and troubleshooting |

★ And **nine Chinese-only documents** — the architecture and code-design ones. Their Chinese
content took over the plain name, so the link below is the only entry there is:

| Document | Contents |
|---|---|
| [docs/context-usage-design.md](docs/context-usage-design.md) | **Context usage estimate — design** — the occupancy metric, the data flow, and how the compacted conversation is persisted (implemented) |
| [docs/session-concurrency-design.md](docs/session-concurrency-design.md) | **Session concurrency — design** — the one-turn-per-session rule and how the frontend learns that a session is busy (implemented) |
| [docs/run-lifecycle-design.md](docs/run-lifecycle-design.md) | **Run lifecycle — design** — a run as a durable row, a lease with a fence, and the connection demoted to a subscriber (§3-§9 are implemented) |
| [docs/fs-backend-design.md](docs/fs-backend-design.md) | **Filesystem backend — design** — the `agentkit/fsx` fork and the path confinement every tool passes through (implemented) |
| [docs/sandbox-kit-design.md](docs/sandbox-kit-design.md) | **Docker Sandbox Kit packaging — design** — the whole process as a v3 workload Kit inside a microVM, how the model key stays on the host, and the SSE path (the Kit is built and published) |
| [docs/mcp-design.md](docs/mcp-design.md) | **MCP integration — design** — the tool assembly path, the tool namespace, and where an MCP server would run inside the Kit (implemented, except §8) |
| [docs/config-design.md](docs/config-design.md) | **Configuration system — design** — the config root and its three levels, the partitions, loading and precedence, hot reload (the body describes the code as it is) |
| [docs/tool-surface-design.md](docs/tool-surface-design.md) | **Tool surface — design** — which tools an agent gets (`tools:` / `mcps:` / `skills:`) and the six `config_*` tools (§2-§5 are implemented) |
| [docs/logging-design.md](docs/logging-design.md) | **Logging assembly refactor — design** — the composition root, a mutable level, `log_level`, injected loggers, and moving the request log out of `llm/common` (**implemented**) |

★ The **Docker Sandbox Kit** that packages openheron as a sandbox workload lives in
[`sandbox-kit/`](sandbox-kit/) — the descriptor, the content recipe and the entrypoint.
Its design is [docs/sandbox-kit-design.md](docs/sandbox-kit-design.md).

★ The finer-grained design documents and the industry comparison live in the **upstream
repository** under `docs/` — for the mechanism breakdown and the trade-offs, see the
upstream `docs/positioning.md`.

Two more convention documents live in this repository:
**[AGENTS.md](AGENTS.md)** (the project conventions for the people and AI working here;
**[AGENTS_zh.md](AGENTS_zh.md) is the Chinese version) and
**[.openheron-example/README.md](.openheron-example/README.md)** (how to use the factory
configuration sample).
