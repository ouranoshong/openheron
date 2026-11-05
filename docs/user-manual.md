# User Manual

> openheron is a multi-agent runtime: every "expert agent" binds its own model and tool
> permissions, so you can use it like a chat window or wire its HTTP API into your own
> system.
>
> This manual is for **users**. To change the code, see
> [developer-manual.md](developer-manual.md); for the design trade-offs, see
> [architecture.md](architecture.md).

> 中文版：[user-manual_zh.md](user-manual_zh.md)

## 1. Installation

You need **Go 1.27.1+** (to build).

```bash
git clone https://github.com/ouranoshong/openheron
cd openheron
make build
```

`make build` builds the frontend and **embeds the interface into the binary**, producing
`bin/openheron` — a self-contained executable that runs on any machine (cross-compile
for platforms other than your build host).

If you just want to try it quickly:

```bash
make build-go     # backend only (no UI) — faster while iterating on Go code
```

★ **Or install a released binary.** Every `v*` tag publishes the same self-contained
executable for six platforms — the interface is embedded, so nothing has to be installed
next to it. One command does the whole thing:

```bash
# Linux / macOS
curl -fsSL https://github.com/ouranoshong/openheron/releases/latest/download/install.sh | sh
```

```powershell
# Windows
irm https://github.com/ouranoshong/openheron/releases/latest/download/install.ps1 | iex
```

The installer picks the asset for your platform, **verifies its sha256 against the
release's `checksums.txt`**, and installs it into `~/.local/bin`. A download that does
not match the checksum is not installed.

| Variable | Effect |
|---|---|
| `OPENHERON_VERSION` | Install a specific tag instead of the latest release |
| `INSTALL_DIR` | Where to install (default `~/.local/bin`) |
| `OPENHERON_REPO` | Install from another `owner/repo` |
| `OPENHERON_TOKEN` | A token, for a private repository |
| `OPENHERON_API_URL` | The API host (default `https://api.github.com`; a GitHub Enterprise one is `https://<host>/api/v3`) |

Prefer to look before you run? Download `install.sh` from the
[releases page](https://github.com/ouranoshong/openheron/releases/latest) and read it —
the script is part of the release, next to the binaries it installs.

Without the script, take the archive from the
[releases page](https://github.com/ouranoshong/openheron/releases/latest) and check it
against the `checksums.txt` next to it:

| Platform | Asset |
|---|---|
| Linux x64 | `openheron_<version>_linux_amd64.tar.gz` |
| Linux arm64 | `openheron_<version>_linux_arm64.tar.gz` |
| macOS x64 | `openheron_<version>_darwin_amd64.tar.gz` |
| macOS arm64 | `openheron_<version>_darwin_arm64.tar.gz` |
| Windows x64 | `openheron_<version>_windows_amd64.zip` |
| Windows arm64 | `openheron_<version>_windows_arm64.zip` |

### Running it in a sandbox

★ **The binary is not the only way to run openheron.** If you would rather the
agent's file and shell tools could not reach your machine at all, openheron
ships as a Docker Sandbox Kit: the whole process — the HTTP/SSE server, the
file tools and the shell tool — runs inside a microVM, and the workspace you
name is the only thing that crosses the boundary.

**What it takes.** A Docker account (`sbx login`), a hypervisor (KVM on
Linux), and the model key bound on the host the way openheron reads it:

```sh
printf '%s' "$DEEPSEEK_API_KEY" | sbx secret set-custom \
    --host api.deepseek.com --env DEEPSEEK_API_KEY \
    --placeholder proxy-managed --value-stdin
```

★ `proxy-managed` is a sentinel: the container sees that string, and the
host's proxy swaps in the real value on the way out. The key itself never
enters the sandbox.

**Starting it.** Two Kits, and the runtime resolves them separately — the
**workload** (the server) and a **mixin** (the credential and the network
rule that goes with it). The published set:

```sh
sbx run docker.io/ouranoshong/openheron-kit:0.0.7 \
    --kit docker.io/ouranoshong/openheron-kit-deepseek:0.0.7 \
    --name openheron ~/my-project
```

★ A mixin is a declaration, so it can also be a **directory holding one YAML
file**: no image, no build, and editing one line points it at your own
endpoint. The workload has a root filesystem, so it stays an image.

**Opening it.** The runtime publishes the port the workload declares, on an
ephemeral loopback port:

```sh
sbx ports openheron
```

```
HOST IP     HOST PORT   SANDBOX PORT   PROTOCOL
127.0.0.1   32772       8080           tcp
::1         32772       8080           tcp
```

★ Open **http://127.0.0.1:32772** — the same interface, the same API, the
same agent.

**What is different.** Not much, and all of it in your favour:

- The **workspace** is a directory on your host, mounted into the sandbox at
  the same path. The agent's edits land there, and you can watch them arrive.
- The **configuration** lands in that workspace too: `.openheron/` is written
  on the first start, and it is yours to edit from then on.
- The **key** stays on the host; the sandbox holds a sentinel.
- **Approvals** still apply, and the policy is yours:
  `policy/permissions.yaml` in the workspace decides which tools ask first.

★ The step-by-step guide is
[`sandbox-kit/TUTORIAL.md`](../sandbox-kit/TUTORIAL.md) (Chinese:
[`sandbox-kit/TUTORIAL_zh.md`](../sandbox-kit/TUTORIAL_zh.md)), and the design
behind it is in [`docs/sandbox-kit-design.md`](sandbox-kit-design.md).

```bash
sha256sum -c checksums.txt --ignore-missing
```

★ The binary is static (`CGO_ENABLED=0`), so it runs on any machine of that platform —
no Go, no Node, no runtime to install.

## 2. First run

```bash
cd /your/project
openheron init                      # generate the configuration in the current directory
export DEEPSEEK_API_KEY=sk-...     # secrets are read from the environment only
openheron
```

Open <http://localhost:8080>.

`openheron init` writes six files:

```
your/project/
└── .openheron/
    ├── agents/
    │   ├── main.md          # entry agent: understand, split, delegate, verify
    │   └── task.md          # executor agent: does the actual work
    ├── llm/
    │   └── deepseek.yaml    # model profile
    ├── policy/
    │   ├── permissions.yaml # approval policy
    │   └── limits.yaml      # caps on quantities
    └── server/
        └── config.yaml      # process parameters (not hot-reloaded)
```

★ **Existing files are not overwritten** (unless you pass `--force`), so running it in
an already-configured project is safe.

Other `init` options:

| Option | Effect |
|---|---|
| `--dir <path>` | Write into the given directory (default: current directory) |
| `--provider <name>` | Choose the vendor, default `deepseek` |
| `--model <name>` | Choose the model, default `deepseek-flash` |
| `--base-url <url>` | The vendor endpoint (**with the path**, copy it from their docs) |
| `--env <NAME>` | The environment variable holding the secret, default `<PROVIDER>_API_KEY` |
| `--no-policy` | Do not generate the `policy/` section (`permissions.yaml` and `limits.yaml`) |
| `--tools` | Also generate the tool configurations under `tools/` |
| `--force` | Overwrite existing files |
| `--dry-run` | Print what would be written, write nothing |
| `--json` | Machine-readable output |

## 3. The config root: where your configuration lives

**Exactly three levels, no upward search**:

```
--config-dir  →  $OPENHERON_CONFIG_DIR  →  <project-root>/.openheron
```

- **The default level is created for you** when it does not exist.
- The first two levels are **not** created when they point nowhere — a typo must not
  silently grow an empty configuration tree. Startup then reports
  `config: load <path>: ...`, which tells you where it went wrong.
- The config root **does not depend on "where the process was started from"**: running
  `openheron` in a subdirectory still reads the same configuration.

If the default root has no `agents/main.md`, startup says so directly:

```
no configuration at /your/project/.openheron: run `openheron init` to create one
```

## 4. Writing configuration

### 4.1 Agents (`agents/<name>.md`)

An agent is a markdown file: **frontmatter + body**. The body is the prompt the model
sees.

```markdown
---
description: "task: the concrete executor. Writes files per the task order."
model: deepseek/deepseek-flash
max_iterations: 50
reasoning: low
disabled_tools: [kb_index]
summarize_at: 80%
---

你是执行 Agent。收到任务后：
1. 先读相关文件
2. 按任务单逐项修改
3. 改完跑一遍测试，把失败项原样贴出来
```

Available frontmatter keys:

| Key | Meaning |
|---|---|
| `description` | What this agent is for (the parent agent uses it to decide whom to delegate to) |
| `model` | `<vendor>/<model>`, **required**, never inherited and never falls back |
| `max_iterations` | Cap on tool-loop iterations |
| `summarize_at` | When this agent's context is **compacted** into a summary: `80%` of the model's window (the default), an absolute token count (`104858`), or `0` for never |
| `subagents` | Whom it may delegate to (see §5) |
| `disabled_tools` | Tool deny-list effective for **this agent** |
| `skills` | The skills this agent may use — an allow-list of the folders under `skills/` (**not declared means every skill**, an empty list means none) |
| `temperature` / `max_tokens` | Sampling parameters |
| `reasoning` | Reasoning tier: `off` / `low` / `high` / `max` (names defined by the vendor profile) |

★ **One wrong frontmatter key is a startup error** and is never ignored — on purpose: a
configuration that fails silently is far harder to debug than one that fails loudly.

★ **`model` is not inherited.** The model the parent agent uses does not carry over to
sub-agents — every agent has to name its own. That is exactly where "expert gets expert
model" lands.

★ **A `reasoning` tier only works if the model profile declares it.** An undeclared tier
is **silently ignored**: an agent says `reasoning: high`, the endpoint has no `high`, and
the effect is "no thinking at all" — with no error and no message. See §4.2.

★ **`summarize_at` is what keeps a long conversation alive.** Past that point the
conversation is replaced by a summary, and the summary is what the agent carries forward from
there on. It comes in three spellings:

| Spelling | Means |
|---|---|
| `summarize_at: 80%` | 80% of the `context_window` of the model this agent runs on |
| `summarize_at: 104858` | An absolute token count |
| `summarize_at: 0` | Never compact — the context grows until the endpoint refuses the request |

Leave the key out and the agent gets **80% of its window** — compaction is on by default.
80–90% is the usual range: a request that exactly fills the window is a request that gets
refused, and the room that is left is what the summary itself needs.

★ **A percentage needs a window.** If the model declares no `context_window`, there is
nothing to take 80% of: that agent never compacts, and openheron says so when it loads the
configuration. Declare `context_window` in `llm/<vendor>.yaml`, or set an absolute
`summarize_at` on the agent.

### 4.2 Model profiles (`llm/<vendor>.yaml`)

```yaml
type: openai_compat
base_url: https://api.deepseek.com
api_key: ${DEEPSEEK_API_KEY}
timeout: 5m

models:
  - id: deepseek-flash
    context_window: 131072
    reasoning:
      off:  { reasoning_effort: none, thinking: { type: disabled } }
      low:  { reasoning_effort: low,  thinking: { type: enabled } }
      high: { reasoning_effort: high, thinking: { type: enabled } }
      max:  { reasoning_effort: max,  thinking: { type: enabled } }
    reasoning_default: off
```

| Field | Meaning |
|---|---|
| `type` | Protocol type; currently `openai_compat` |
| `base_url` | The endpoint, **including the path**, copied from the vendor docs |
| `api_key` | An **environment variable name**, written as `${NAME}` — never a literal |
| `timeout` | Timeout, with a unit (`5m` / `30m`) |
| `models[].id` | Model name |
| `models[].context_window` | Context window — the size the compaction threshold is measured against |
| `models[].reasoning` | **Tier table**: tier name → what this endpoint actually sends |
| `models[].reasoning_default` | Which tier to use when the agent names no `reasoning` |
| `models[].timeout` | Overrides the vendor-level timeout (thinking models usually need more) |
| `hint_keys` | The endpoint's field names, used to give an accurate hint when a request is rejected |
| `extra` | Vendor-level dialect, passed through to every request |

★ **`api_key` may only be an environment variable name** (`${OPENAI_API_KEY}`) or a file
reference (`@file:/path`). Writing a literal secret into the configuration makes the
config editor refuse to save it — secrets do not belong in the repository.

★ **The compaction threshold belongs to the agent, not to the model.** `context_window` is the
model's; `summarize_at` is the agent's, because how much of that window this agent fills before
its history is summarized is this agent's own policy. One model can serve several agents with
different policies. A `summarize_at` left in a model profile is ignored, and openheron says so
when it loads the configuration.

Compaction shortens the **context**, not the **record**. The messages that were compacted away
stay in the transcript and stay readable; the summary is written next to them. Your own words
survive the cut: the summary keeps them.

★ **The `reasoning` tier table is not optional.** It is the translation layer between
"the agent writes `reasoning: high`" and "how this endpoint expresses high". A tier
missing from the table is silently ignored, so "I turned on high and it still does not
think" shows up in the most hidden way possible.

### 4.3 The approval policy (`policy/permissions.yaml`)

```yaml
ask:
  - write_file
  - edit_file
  - execute
  - kb_index
deny: []
```

There are only two columns, and the semantics are plain:

- **Listed under `ask`**: every call needs **your nod**.
- **Listed under `deny`**: **nobody may use it**.
- **Listed in neither**: defaults to `allow` (straight through).

★ `deny` is **global**. The `disabled_tools` in `agents/*.md` only affects that one
agent and **cannot relax the outer `deny`**. It is the one guarantee a sub-agent cannot
loosen.

### 4.4 External tools (`tools/`)

**Enabled by the presence of the file** — no code change needed. Four file names are
recognised today:

| File | Provides |
|---|---|
| `http.yaml` | A generic HTTP request tool |
| `web_search.yaml` | Web search |
| `wikipedia.yaml` | Encyclopedia lookup |
| `kb.yaml` | The knowledge base (see §7) |

★ **Only these four file names are accepted** under `tools/`; any other name is a
**startup error** and is never silently ignored.

★ **`openheron init` writes nothing under `tools/` by default** — that whole directory is
yours to write. Pass `--tools` and it writes the four files above. Not writing a file
means not enabled: the tool does not appear in the agent's tool table, and the model
cannot see it.

### 4.5 Process parameters (`server/config.yaml`)

| Field | Default | Meaning |
|---|---|---|
| `listen_addr` | `:8080` | Listen address |
| `data_dir` | `state` | Where sessions and checkpoints live |
| `static_dir` | `web/dist` | Frontend build output (used when `frontend_mode: dir`) |
| `frontend_mode` | `embed` | `embed` uses the interface baked into the binary; `dir` reads from disk |
| `start_timeout` | `60s` | Startup timeout |
| `lease_ttl` | `10m` | How long a run's lease survives without progress — **longer than the provider's own timeout** |
| `agent.max_depth` | `1` | Cap on delegation depth |

★ **`server/` is not hot-reloaded** — a change only takes effect after a restart. That is
why it is **read-only** in the config editor, and submitting it fails with
`section "server" is not editable`.

These environment variables override the file and take precedence:
`LISTEN_ADDR`, `DATA_DIR`, `PROJECT_ROOT`, `FRONTEND_MODE`, `STATIC_DIR`.

### 4.6 Skills (`skills/`)

`skills/<name>/SKILL.md`, following eino's native skill convention.

★ Skills are looked up by **a different rule**: `$EINO_EXT_SKILLS_DIR` first, and only if
that is unset does it search **upwards** from the current directory for
`.openheron/skills`. That differs from the config root's "never search upwards" — skills
are a directory tree, and searching upwards lets you use skills from your home directory.

★ **Which skills an agent may use is declared by that agent.** The `skills` key in
`agents/<name>.md` is an allow-list, and the names are the folders under `skills/`:

```yaml
skills: [refactor, review]
```

Not declaring the key means **every** skill in the directory; declaring an empty list
means **none**. A skill outside the list is not advertised to the model, and asking for
it by name fails — a model cannot load what it cannot see. A name that is not in the
directory is reported in the log when the agent is built, rather than ignored.

## 5. Multiple agents and delegation

### 5.1 Who may delegate to whom

Declare it in the agent's frontmatter:

```markdown
---
model: deepseek/deepseek-flash
subagents: [task]
---
```

This means "this agent may hand work to `task`".

★ **Referencing an unknown agent, or declaring a cycle, is a startup error** — never a
silent truncation.

### 5.2 The depth cap

The cap is decided by `agent.max_depth` in `server/config.yaml` together with the same
key in `policy/limits.yaml`, and **the stricter one wins**. The default `1` means "only
the entry agent may delegate; a sub-agent may not spawn children".

For a deeper delegation chain:

```yaml
# policy/limits.yaml
agent:
  max_depth: 3
```

★ A declared topology that exceeds the cap is a **startup error**, not "truncate to the
cap and carry on". The latter would make the depth protection meaningless.

### 5.3 Several agents in one session

Independent work can be dispatched in parallel. Usage is **accounted per agent**
(`usage.byAgent` in `/v2/sessions/{id}/messages`), so it is obvious who spent what.

In the interface:
- The **agent badges** in the session list filter the history by agent.
- A sub-agent's output in a message carries an **attribution marker**, so you can see
  who produced it.

## 6. Using the interface

| Element | What it does |
|---|---|
| **Sidebar** | Session list, agent filter, status dot (lights up when an approval is pending) |
| **Composer** | Ask a question; `/` opens the slash commands; supports drag/paste attachments |
| **Message list** | Assistant replies, reasoning, tool-call cards, sub-agent output (with attribution) |
| **Approval card** | Appears when your nod is needed: approve / deny (with an optional reason) |
| **Tool card** | The arguments and result of each tool call |
| **Settings** | Entry point to the config editor |

### 6.1 Slash commands

| Command | Effect |
|---|---|
| `/new` | Start a new session |
| `/stop` | Stop the current turn |
| `/attach` | Attach files to the current session |
| `/settings` | Open the config editor |
| `/agents` | Switch the session's agent |
| `/compact` | **Compact the conversation now** — see §6.6 |
| `/skill` | Pick a named skill for this task |
| `/review` | Review the current changes or the given paths |
| `/test` | Run the relevant tests and summarise failures |
| `/explain` | Explain how the given code works |
| `/fix` | Find the root cause and fix the given problem |

### 6.2 How an approval goes

When the model wants to call an `ask`-tier tool:

1. The run **pauses** and an approval card appears.
2. You approve or deny; a denial can carry a one-line reason.
3. The decision is written back to the session and the model continues with the result.

★ On denial the model receives **a denial message** (with your reason), **not** an
error — so it can adjust its approach instead of falling over.

★ The approval's resume point is stored in the session. **If that resume point is lost**
(e.g. the session directory was cleaned out), the interface tells you plainly that the
approval can no longer be resumed and asks you to send a new message, instead of
silently starting a fresh turn.

### 6.3 The config editor

`/settings` opens it. It can edit `llm/`, `agents/`, `policy/` and `tools/`.

The write path has several gates:

- `server/` is **not editable** (not hot-reloaded, so a change needs a restart).
- **Literal secrets are rejected** — `api_key must be a reference, not a literal`. Use
  `${ENV_VAR}` or `@file:/path`.
- **A file with no YAML content is rejected** — a file that is empty, or holds nothing
  but comments, carries no configuration at all. The message names the fields the file
  needs; under `tools/` it says which tool is missing what.
- **Optimistic concurrency**: if the file changed elsewhere, saving reports a conflict
  and lets you choose to overwrite or to back off.

### 6.4 The context meter

The line under the input box ends with the **context occupancy** of the agent you are talking
to:

```
Agent: main · deepseek/deepseek-flash · Reasoning: off · Context: ≈18k / 131k (14 %)
```

- The number is the size of that agent's **most recent request** — not a total over the
  conversation. A long conversation can have spent a lot and still have plenty of room.
- It is the vendor's own count, so it is an estimate: the `≈` is there on purpose.
- The bar turns amber at 80 % of the window and red at 95 %.
- If the model declares **no** context window, the meter shows the token count alone
  (`Context: ≈18k tokens`) and no ratio. Declare `context_window` in
  `llm/<vendor>.yaml` to get the bar.

When several agents are in play, each one gets its own figure in the **agent filter** above
the conversation — every agent has its own context, and they are rarely the same size.

★ The occupancy and the agent's **spend** are different numbers. The meter is about room left
in the context; the spend is about what the agent has cost you over the whole session.

### 6.5 The compaction divider

When the context is compacted, the conversation gets a **divider**:

```
────────────  12 earlier messages · compacted into a summary · ≈62k tokens saved  ────────────
```

- The divider marks the **boundary of what the model reads**. Above it, the agent reads a
  summary; the messages themselves are not sent any more.
- The messages stay in the transcript and stay readable — compaction shortens the
  **context**, not the **record**. Scroll up and the conversation before the cut is still
  there.
- The counts are estimates: `≈62k` is the vendor's own token count minus the size of the
  summary, and it says how much room the cut bought.
- A compaction that happens **during** a run draws the divider at once, without the
  counts: the counts are measured against the transcript, which is written when the run
  ends. Reload the thread and they are there.

★ The summary itself is not shown. It is the model's working material, not your
conversation — and the messages it replaces are still on screen.

### 6.6 `/compact` — compacting on demand

The context is compacted **by itself** when it reaches the agent's threshold (see §4.1). `/compact`
does the same thing **when you say so**: it summarizes the conversation and makes that summary the
new boundary, right away.

```
/compact
```

- It costs **one model call** — the summary is generated the same way the automatic compaction
  generates it, by the agent's own model.
- It is **independent of the threshold**. An agent that never compacts by itself (`summarize_at: 0`)
  can still be compacted by hand: "off" means "do not do it on your own", not "you may not".
- The divider appears when it is done, with the counts. While it runs, the composer says
  `Compacting the conversation…`.
- The messages above the divider stay readable — compaction shortens the **context**, not the
  **record**.

★ **It refuses a context that is too small to be worth compressing.** A summary carries a preamble
and your own messages, so a short conversation can come out *longer* than it went in. Below four
messages, or below roughly 4k tokens of context, `/compact` says there is nothing worth
compacting yet and changes nothing.

★ **It refuses while a turn is running or an approval is waiting.** Both own the context: cutting
the history under them would be cutting the history they are working with. Stop the turn first.

The same thing is available over HTTP — `POST /v2/sessions/{id}/compact` — for scripts.

### 6.7 Two windows on one session

A session runs **one turn at a time**. If the same session is open in two windows — two
tabs, or a tab and another machine — only one of them can be running a turn.

The other one says so:

```
This session is running in another window — stop it to take over
```

★ **Nothing is lost.** The refused message is not sent, so it does not appear in the
conversation. The turn that is running keeps going, untouched.

★ **The stop button is the way to take over.** It stops the session's turn — whoever
owns it — and then you can send. The window that owned the turn sees it stop.

★ **This is a server-side rule, not a browser one.** Any client that talks to the API
gets the same answer, and the session's `state` reports `"running"` to every client
looking at it. A second window opened while a turn is running hydrates as busy, with the
send button already disabled.

★ **A broken connection does not lose the turn.** The turn belongs to the server, not
to the connection that happened to be watching it: the server archives it either way.
A client whose connection drops comes back with the run's id — `POST /v2/run` with
`runId` and **no question** — and gets the rest of the turn. `GET
/v2/sessions/{id}/runs` says whether the run is still going, and the run's own history
is there for the times when it is not.

## 7. The knowledge base

The knowledge base is an **optional** local corpus retrieval layer: it turns a set of
markdown documents into material an agent can search.

### 7.1 Turning it on

```yaml
# .openheron/tools/kb.yaml
corpus: docs          # a directory relative to the project root
index: default
model: openai/text-embedding-3-small
top_k: 8
```

- **Not configuring `corpus` means it is off.**
- `corpus` **must be a path relative to the project root**. An absolute path is a
  **startup error** — the index and its corpus must have the same lifetime, and a corpus
  living in a system directory would rob the index of meaning.
- `model` is required and must point at a model declared with `kind: embedding`;
  otherwise startup errors out (`kb.model ... is not an embedding model`).

The index lands in `<data_dir>/kb/<index>/`.

### 7.2 Command line

```bash
openheron kb index                 # build the index
openheron kb index docs/a.md       # index only the given files
openheron kb search "how is the config root resolved"   # search
openheron kb status                # inspect the index
openheron kb status --json         # machine-readable
```

All three accept `--config-dir <path>` to point at a different config root.

★ `kb index` takes a **run lock**: only one run at a time, and a second call waits for
the first to finish.

★ `kb status` reporting `lexical-only retrieval` means **there are no vectors** — the
index is purely lexical. Search still works but is less accurate than the hybrid. It
becomes a hybrid score once you configure an embedding model and re-index.

### 7.3 How agents use the knowledge base

Agents get two tools:

- **`kb_search`**: retrieval, returning a document **outline** (title, summary, section
  headings with line numbers, links to other documents), not the full text. To read a
  section, call `read_file` with the line number.
- **`kb_index`**: builds the index. It defaults to the `ask` tier — **letting an agent
  rebuild the index needs your nod**.

## 8. Command-line reference

```
openheron [--config-dir <path>] [--project-root <path>]   start the server
openheron init [options]                                   generate a minimal runnable configuration
openheron kb index [--config-dir] [--json] [files...]      build the index
openheron kb search [--config-dir] <query>                 search
openheron kb status [--config-dir] [--json]                inspect the index
openheron version                                         print the version
```

## 9. HTTP API reference

The service exposes only the `/v2` family. There is no authentication and no CORS; the
default `:8080` binds every interface, so put a reverse proxy in front to expose it
publicly, or narrow `listen_addr` to `127.0.0.1`.

| Method | Path | Effect |
|---|---|---|
| POST | `/v2/sessions` | Create a session; returns `threadId` |
| GET | `/v2/sessions` | List sessions |
| GET | `/v2/sessions/:id` | Session details |
| PATCH | `/v2/sessions/:id` | Rename the session |
| PATCH | `/v2/sessions/:id/agent` | Change the session's agent |
| DELETE | `/v2/sessions/:id` | Delete the session |
| GET | `/v2/sessions/:id/messages` | History + usage (filter with `?agent=`) |
| GET | `/v2/sessions/:id/runs` | The run ledger: every run of the session + the active one |
| GET | `/v2/sessions/:id/runs/:runId` | One run of the session |
| POST | `/v2/sessions/:id/abort` | Abort the running turn |
| POST | `/v2/sessions/:id/compact` | Compact the conversation now (see §6.6) |
| POST/GET | `/v2/sessions/:id/files` | Upload / list session workspace files (32 MiB per file) |
| GET | `/v2/project/files` | List files under the project root (`?q=` `?limit=`) |
| GET | `/v2/commands` | The slash-command list |
| POST | `/v2/run` | **SSE entry point**: ask a question, or answer an approval (see §6.7) |
| GET | `/v2/skills` | The skill list |
| GET | `/v2/agents` `/v2/agents/:agentId` | Agents and their effective tool tables |
| GET | `/v2/config` | The effective configuration |
| GET | `/v2/health` | Health check (includes `deferred` notices) |
| GET | `/v2/runtime` | Runtime status |
| GET | `/v2/settings` | The list of editable items |
| GET/POST/PUT/DELETE | `/v2/settings/item` | Read/write config (carries `baseRevision`) |
| POST | `/v2/settings/validate` | Validate a configuration without writing it |

### 9.1 Running a turn

```bash
# 1. Create a session
curl -s -X POST localhost:8080/v2/sessions \
  -H 'content-type: application/json' -d '{"agentId":"main"}'
# → {"threadId":"...","state":"idle",...}

# 2. Ask (SSE stream)
curl -N -X POST localhost:8080/v2/run \
  -H 'content-type: application/json' \
  -d '{"threadId":"<the id from step 1>","query":"read the README"}'
```

★ **`threadId` is required.** Without it you get
`threadId is required; create the session with POST /v2/sessions first`.

★ **A session runs one turn at a time.** A second request on a session that is already
running gets `409` with the code `session_busy` — and the running turn is not disturbed
(see §6.7).

The response is an **AG-UI event stream**, with event types `RUN_STARTED`,
`TEXT_MESSAGE_*`, `REASONING_*`, `TOOL_CALL_*`, `CUSTOM` (`model_retry`),
`RUN_FINISHED` and `RUN_ERROR`. A sub-agent's output is attributed through
`subagentRunId` on the event.

### 9.2 Answering an approval

The decision goes back into the **same** `/v2/run`:

```json
{
  "threadId": "...",
  "resume": [
    {"interruptId": "...", "payload": {"approved": true}},
    {"interruptId": "...", "payload": {"approved": false, "reason": "do not touch that file"}}
  ]
}
```

## 10. FAQ

**Q: Startup says `no configuration at ...: run \`openheron init\` to create one`**
The config root is empty. Run `openheron init` in the project root.

**Q: `config: load <path>: agents/main.md does not exist or could not be parsed`**
One of two things: the config root points at the wrong place (fix it with
`--config-dir` or `$OPENHERON_CONFIG_DIR`), or the frontmatter of `agents/main.md` has
a wrong key.

**Q: I changed `agents/main.md` and nothing happened?**
`agents/` is hot-reloaded, so it should have taken effect. If it did not, look for the
reload error in the startup log. `server/` and `skills/` are **not** hot-reloaded and
need a restart.

**Q: The model does not think — `reasoning: high` seems to do nothing?**
Most likely the model profile **does not declare the `high` tier**. A tier missing from
the table is silently ignored. Add it under `models[].reasoning` in
`llm/<vendor>.yaml`.

**Q: `kb_search` finds nothing, or finds the wrong things?**
Run `openheron kb status` first: `lexical-only retrieval` means there are no vectors
and the hybrid score is not in effect. Configure an embedding model and re-index.

**Q: An agent cannot call `write_file`?**
Two possibilities: it is disabled for that agent by `disabled_tools`, or it is denied
globally by `deny`. Check `GET /v2/agents/:agentId` for the effective tool table. By
default `main` has `write_file`/`edit_file`/`execute` disabled — in the factory
configuration it only orchestrates.

**Q: `/v2/run` says `threadId is required`**
Create the session with `POST /v2/sessions` first.

**Q: The port is taken / the interface will not open?**
Change the port in `listen_addr`, or override it with the `LISTEN_ADDR` environment
variable.

**Q: How do I see what the server actually loaded?**
`GET /v2/config` returns the effective configuration; `GET /v2/health` returns the
health status and any `deferred` notices; `GET /v2/runtime` returns the runtime status.
The startup log also prints the config root path.
