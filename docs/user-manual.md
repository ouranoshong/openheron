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

openheron is a self-contained executable — the interface is embedded in the binary, so
nothing has to be installed next to it. How you install it depends on where you are:

| Where you are | Read |
|---|---|
| You just want to use it | §1.1 Install a release |
| You are going to change the code | §1.2 Build it from source |
| You are on Windows | §1.3 On Windows |
| You want the agent's tools to have no way to reach your machine | §1.4 Running it in a sandbox |

### 1.1 Install a release

Every `v*` tag publishes the same self-contained executable for six platforms. One
command does the whole thing:

```bash
# Linux / macOS
curl -fsSL https://github.com/ouranoshong/openheron/releases/latest/download/install.sh | sh
```

```powershell
# Windows
irm https://github.com/ouranoshong/openheron/releases/latest/download/install.ps1 | iex
```

The installer picks the asset for your platform, **verifies its sha256 against the
release's `checksums.txt`**, and installs it into `~/.local/bin`
(`%LOCALAPPDATA%\Programs\openheron` on Windows). A download that does not
match the checksum is not installed.

| Variable | Effect |
|---|---|
| `OPENHERON_VERSION` | Install a specific tag instead of the latest release |
| `INSTALL_DIR` | Where to install (default `~/.local/bin`, or `%LOCALAPPDATA%\Programs\openheron` on Windows) |
| `OPENHERON_REPO` | Install from another `owner/repo` |
| `OPENHERON_TOKEN` | A token, for a private repository |
| `OPENHERON_API_URL` | The API host (default `https://api.github.com`; a GitHub Enterprise one is `https://<host>/api/v3`) |

★ Prefer to look before you run? Download `install.sh` from the
[releases page](https://github.com/ouranoshong/openheron/releases/latest) and read it —
the script is part of the release, next to the binaries it installs.

**Without the script**, take the archive from the
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

```bash
sha256sum -c checksums.txt --ignore-missing
```

★ The binary is static (`CGO_ENABLED=0`), so it runs on any machine of that platform —
no Go, no Node, no runtime to install.

★ Once it is installed: running it is §2, and where the key comes from is §4.2.

### 1.2 Build it from source

You only need this one if you are going to change the code. You need **Go 1.27.1+**
(to build), plus [go-task](https://taskfile.dev) and [Bun](https://bun.sh): the
interface is built with Bun and embedded into the binary, so the two build tools are
not optional.

```bash
git clone https://github.com/ouranoshong/openheron
cd openheron
task build
```

`task build` builds the frontend and **embeds the interface into the binary**, producing
`bin/openheron` — a self-contained executable that runs on any machine (cross-compile
for platforms other than your build host).

If you just want to try it quickly:

```bash
task build-go     # backend only (no UI) — faster while iterating on Go code
```

★ On Windows the product is `bin\openheron.exe` — the executable keeps the `.exe` suffix
there. And `python3` on Windows is the Microsoft Store alias — it exists and runs
nothing — so the repository's own scripts call `python` there. It only matters for the
document checks (`task lint-docs`).

### 1.3 On Windows

★ **Two things differ from the Unix installer.** The location is
`%LOCALAPPDATA%\Programs\openheron` — not `~/.local/bin` — and it **does not touch
your PATH**: the script only prints a hint when that directory is not on it. Add it
yourself, or call the binary by its full path:

```powershell
# put the install directory on the user PATH
$dir = "$env:LOCALAPPDATA\Programs\openheron"
[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';' + $dir, 'User')
```

★ Open a **new** terminal afterwards: a shell keeps the PATH it started with.

From `cmd.exe` there is no `irm`, so hand the line to PowerShell:

```cmd
powershell -c "irm https://github.com/ouranoshong/openheron/releases/latest/download/install.ps1 | iex"
```

**Where the tools differ.** The file tools (`read_file`, `write_file`, `edit_file`,
`ls` and `glob`) are the runtime's own code, and they work on Windows as they are;
only `grep` needs [ripgrep](https://github.com/BurntSushi/ripgrep) (`rg`) on top —
Windows does not ship it, and without it you get
`ripgrep (rg) is not installed or not in PATH` (§10).

**The shell tool finds a shell by itself.** `execute` needs a program to run the
command in, and openheron looks for one when the process starts, in this order: `sh`
or `bash` on PATH → Git for Windows (`C:\Program Files\Git\usr\bin\bash.exe`) and
MSYS2 (`C:\msys64\usr\bin\sh.exe`) → the WSL launcher
(`C:\Windows\System32\bash.exe`, **last**, because it runs the command in a Linux
filesystem, where `C:\Users` is `/mnt/c/Users`).

★ Each candidate is checked by running one trivial command, so the `bash.exe`
that ships with Windows — it is on every machine and does nothing without a
WSL distribution — is not mistaken for a shell. The first candidate that runs
wins, and a shell found outside PATH gets its own directory put first in the
child's PATH, so `ls` and its neighbours are found.

★ **With no POSIX shell at all the tool falls back to `cmd.exe`**, and its
description tells the model to write Windows commands (`dir`, `type`, `copy`)
rather than POSIX ones (`ls`, `cat`, `cp`). ★ The dialect is never a guess:
the description the model reads and the program that runs the command are the
same answer, decided once at startup. The other way out is to leave `execute`
out of that agent's `tools:` list — the factory configuration already does
that.

### 1.4 Running it in a sandbox

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
sbx run docker.io/ouranoshong/openheron-kit:latest \
    --kit docker.io/ouranoshong/openheron-kit-deepseek:latest \
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
- **Approvals** still apply, and the default is the safe one: every tool asks
  first, and the files under `.openheron/tools/` and `.openheron/mcps/` name the
  exceptions (see §4.3).

★ The step-by-step guide is
[`sandbox-kit/TUTORIAL.md`](../sandbox-kit/TUTORIAL.md) (Chinese:
[`sandbox-kit/TUTORIAL_zh.md`](../sandbox-kit/TUTORIAL_zh.md)), and the design
behind it is in [`docs/sandbox-kit-design.md`](sandbox-kit-design.md) — that document is
Chinese only, like every design document in this repository.

## 2. First run

### 2.1 Getting it running

```bash
cd /your/project
openheron init                      # generate the configuration in the current directory
export DEEPSEEK_API_KEY=sk-...     # secrets are read from the environment only
openheron
```

Open <http://localhost:8080>.

★ The key is read from the environment, and **how** you set it depends on the system
you are on — macOS and Linux see §4.2, Windows see §2.2.

### 2.2 Running it for the first time on Windows

In PowerShell, from nothing to running:

```powershell
cd C:\path\to\your-project
openheron init                      # generate the configuration in the current directory
$env:DEEPSEEK_API_KEY = "sk-..."    # secrets are read from the environment only
openheron
```

★ When `openheron` is not on the PATH, call it by its full path:
`%LOCALAPPDATA%\Programs\openheron\openheron.exe` (§1.3).

`init` prints one line when it is done:

```
the api key is read from the environment at request time: export DEEPSEEK_API_KEY=...
```

★ That line uses **the Unix `export`**, which runs in neither PowerShell nor cmd.exe — on
Windows the same thing is `$env:DEEPSEEK_API_KEY = "sk-..."` (PowerShell) or
`set DEEPSEEK_API_KEY=sk-...` (cmd.exe).

#### How to set the key

One command, four ways of writing it, and they do not reach the same far:

- **The PowerShell window you are in** — `$env:DEEPSEEK_API_KEY = "sk-..."`: that window only.
- **Every terminal this user opens from now on** — `[Environment]::SetEnvironmentVariable('DEEPSEEK_API_KEY', 'sk-...', 'User')`: permanently; ★ **you have to open a new terminal**.
- **The cmd.exe window you are in** — `set DEEPSEEK_API_KEY=sk-...`: that window only.
- **The cmd.exe window, for this user** — `setx DEEPSEEK_API_KEY sk-...`: permanently; ★ **the value is truncated at 1024 characters**.

★ **A shell keeps the environment it started with** — the window that ran `setx` or
`[Environment]::SetEnvironmentVariable` does not see the variable; you have to **open
another terminal**.

★ **The key is read when the configuration is loaded** (§4.2), so the variable has to
reach **the process that starts openheron**: setting it in another terminal afterwards
does nothing for a server that is already running.

★ **On Windows the variable name is case-insensitive** — `$env:deepseek_api_key` is the
`DEEPSEEK_API_KEY` variable; on macOS and Linux it is not (§4.2).

**Putting the key in a file.** To keep the secret out of the environment, point
`@file:` at a file:

```yaml
api_key: '@file:C:\Users\you\deepseek.key'   # single quotes
api_key: "@file:C:/Users/you/deepseek.key"    # or forward slashes
```

★ **Double quotes with backslashes do not work on Windows**: `\` is an escape character
inside a YAML double-quoted scalar and `\U` is not a legal escape, so the whole profile
fails to parse (`yaml: did not find expected hexdecimal number`). Use single quotes, or
write the path with forward slashes. ★ The path has to be **absolute**: a relative one is
relative to **the directory openheron was started from** (§4.2).

**How to tell that it worked.** Open `http://localhost:8080/v2/config` in a browser; the
provider entry reads `hasApiKey` and it should be `true`; and the startup log must not
carry `api_key env DEEPSEEK_API_KEY is not set` (§4.2).

#### Before it starts

★ **The place `init` writes to and the place the server reads from may not be the same.**
`init` always writes `.openheron` under the current directory and it **does not look at**
`$OPENHERON_CONFIG_DIR`; the server reads `--config-dir` → `$OPENHERON_CONFIG_DIR` →
`.openheron` under the project root (§3). With that environment variable set, the server
comes up with "no configuration at ...", because it is not reading the tree `init` wrote.

★ **When the child process cannot find the command on Windows**, change `command` to
`uvx.exe` in `mcps/fetch.yaml` and `mcps/time.yaml` — the two MCP servers need `uvx` on
the PATH (§2.3).

If the port is taken, change it **before** it starts: `$env:LISTEN_ADDR = ":9090"` (§10).

### 2.3 What init writes

`openheron init` writes the whole tree:

```
your/project/
└── .openheron/
    ├── agents/
    │   ├── main.md           # entry agent: understand the request, plan, dispatch
    │   ├── task.md           # general execution expert: llm/ tools/ mcps/ server/
    │   ├── agent-expert.md   # agent-domain expert: agents/*.md
    │   └── skill-expert.md   # skill-domain expert: skills/<name>/SKILL.md
    ├── skills/
    │   ├── openheron-config-map/SKILL.md        # the facts of the configuration system
    │   ├── openheron-config-edit/SKILL.md       # the procedure for changing it
    │   ├── openheron-config-templates/SKILL.md  # how to get a skeleton
    │   └── openheron-config-validate/SKILL.md   # the silent failures
    ├── llm/
    │   └── deepseek.yaml    # model profile
    ├── mcps/
    │   ├── fetch.yaml        # fetch a page and turn it into markdown
    │   ├── time.yaml         # the current time and time-zone conversion
    │   └── deepwiki.yaml     # public repository documentation and Q&A
    ├── tools/
    │   ├── filesystem.yaml   # the tiers of the filesystem tools
    │   ├── http.yaml         # request_get
    │   ├── web_search.yaml   # duckduckgo_search
    │   ├── wikipedia.yaml    # wikipedia_search
    │   └── kb.yaml           # the knowledge base (off: its corpus_dir is empty)
    └── server/
        └── config.yaml      # process parameters (not hot-reloaded)
```

★ Two of the three MCP servers are stdio ones — `fetch` and `time` — and they need `uvx`
on the PATH and download their package on the first start; a server that cannot be
started costs that server's tools and nothing else (§4.7).

★ **There is no `policy/` file.** The tiers have a default and the exceptions are
named where the tool comes from (§4.3), so a file that says what the code already
does is noise — ★ noise in a configuration tree is read as meaning. Name your
exceptions in the tool's own file instead.

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
| `--no-tools` | Leave the tool configurations under `tools/` unwritten |
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
tools: [ls, read_file, write_file, edit_file, glob, grep, execute, write_todos]
mcps: []
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
| `tools` | The tools **this agent may use** — every tool that is **not** an MCP one: the built-ins, the ones the files under `tools/` provide, and the `config_*` tools. ★ **Not declared means no tools at all**: the list is the whole answer. ★ MCP tools are **not** named here — the agent gets them by mounting the server (see §4.8) |
| `skills` | The skills this agent may load — an allow-list of the folders under `skills/`. **Not declared means no skill**, and so does an empty list |
| `mcps` | The MCP servers this agent **mounts** — **not declared means no server**, and so does an empty list. Mounting one starts a child process **and hands the agent all of that server's tools** (see §4.8) |
| `temperature` / `max_tokens` | Sampling parameters |
| `reasoning` | Reasoning tier: `off` / `low` / `high` / `max` (names defined by the vendor profile) |

★ **One wrong frontmatter key is a startup error** and is never ignored — on purpose: a
configuration that fails silently is far harder to debug than one that fails loudly.

★ **`model` is not inherited.** The model the parent agent uses does not carry over to
sub-agents — every agent has to name its own. That is exactly where "expert gets expert
model" lands.

★ **A model nobody declares leaves that agent out — it does not stop openheron.** The
agent is dropped from the tree and from every parent's `subagents:`, and the reason is
said in four places: the startup log, `/v2/health`, a grey notice on that agent's own
file in the config editor (§6.3), and the error a session gets if it asks for it.
★ The default agent is no exception: with no usable model, `main` is left out too, and
until you fix it **no session can start** — but openheron does start, which is what lets
you open the editor and fix the file. ★ The editor refuses the change that would cause
it: pointing a working agent at a model that does not exist is rejected at save time.

★ **A message that cannot start says why, not "timed out".** If the agent you are talking
to cannot be built, the chat shows the reason itself — `agent "main" is left out of the
tree: the model openai/gpt-4o-mini is not declared under llm/` — and the run ends with
the code `agent_unavailable`. ★ That code means "your configuration is broken", so the
answer is to open the editor and fix the file, not to send the message again.

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
config editor refuse to save it — secrets do not belong in the repository. ★ The
`@file:` form needs **quotes** in YAML; the subsection below says where the key comes
from on each system.

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

#### The key: where it comes from

The profile names **where** the secret is — never the secret itself. Two forms work,
and both are read **when the configuration is loaded**:

```yaml
# .openheron/llm/deepseek.yaml
api_key: ${DEEPSEEK_API_KEY}                  # an environment variable
api_key: "@file:/etc/openheron/deepseek.key"  # the same key, read from a file
```

★ **The quotes around `@file:` are not optional.** `@` is a reserved character in
YAML, so an unquoted `api_key: @file:/path` does not parse at all ("found character
that cannot start any token") and the profile never loads. `${NAME}` needs no quotes.

★ **The key is read when the configuration is loaded** — so set the variable
**before** you start openheron. A variable you export in another terminal afterwards
is not seen by the running server; saving the profile file again reloads the
configuration, and that reload reads the variable once more.

★ **How the variable is set on Windows, see §2.2** — the four ways of writing it in
PowerShell and cmd.exe, the case-insensitive variable name, and the `@file:` quoting pit
on Windows are all there.

**macOS and Linux.**

```sh
export DEEPSEEK_API_KEY=sk-...
```

To make it permanent, put the line in `~/.zshrc` (macOS), `~/.bashrc` (Linux with
bash) or `~/.profile` (any POSIX shell).

★ **On macOS and Linux the variable name is case-sensitive.** `$env:deepseek_api_key` is
another variable there, and the profile then reads
`api_key env DEEPSEEK_API_KEY is not set`; on Windows it is the other way round
(§2.2).

★ **The variable has to reach the process, not your login.** A `~/.bashrc` line is
read by **interactive bash only**: a systemd unit, a launchd job, a container and a CI
runner each get an environment of their own, and none of them reads your shell's rc
files. Give it to whatever starts openheron:

| openheron runs in | where the variable goes |
|---|---|
| a terminal | `export ...` (macOS and Linux) / `$env:... = ...` (Windows, see §2.2) |
| a systemd unit (Linux) | `Environment=DEEPSEEK_API_KEY=sk-...` in the unit file, then `systemctl daemon-reload` |
| a launchd job (macOS) | `EnvironmentVariables` in the job's plist |
| a container | `docker run -e DEEPSEEK_API_KEY=sk-...`, or `--env-file .env` |
| CI | the runner's secret store — the variable, not the key |

**The `@file:` form.** The file holds the same secret the variable would, and it is
read at load time with its content trimmed. Two things to know:

- ★ **A relative path is relative to the directory openheron was started from**, not
  to the configuration root — use an absolute path.
- ★ **A path that does not exist is a startup error**, not a reminder:
  `config: parse llm/deepseek.yaml: api_key: read secret file: open /etc/openheron/deepseek.key: no such file or directory`.
  The difference is deliberate: an unset variable is a runtime state, a wrong path is
  a mistake in the configuration.

**How to tell that it worked.** A variable that is not set is named in the startup log:

```
[main] config notice notice="llm/deepseek.yaml: api_key env DEEPSEEK_API_KEY is not set; calls to this endpoint will fail with 401 Unauthorized"
```

★ That reminder **does not stop the server**: openheron starts, the agent builds, and
the endpoint answers `401 Unauthorized` the first time it is called. The same notices
are under `deferred` in `GET /v2/config` and `GET /v2/health`.

★ **An empty variable is not an unset one.** `DEEPSEEK_API_KEY=` — set, but with
nothing in it — resolves to an empty key and produces **no reminder at all**, so the
absence of a notice is not the absence of a problem. The field to look at is
`hasApiKey` in `GET /v2/config`; the provider entry reads

```json
{"id":"deepseek","baseUrl":"https://api.deepseek.com","hasApiKey":true,"models":[{"id":"deepseek-flash","contextWindow":1310720}]}
```

`hasApiKey` is `true` when a key was found and `false` when there is none — and the
key itself never leaves the process. ★ **How the variable is emptied decides which of
the two you get**: `$env:DEEPSEEK_API_KEY = ""` leaves an empty value behind (silent),
while `$env:DEEPSEEK_API_KEY = $null` removes the variable (the reminder appears);
`export DEEPSEEK_API_KEY=` and `unset DEEPSEEK_API_KEY` are the same pair on a Unix
shell.

★ **The name of the variable is yours to choose.** `openheron init --env DEEPSEEK_API_KEY`
writes the profile with `${DEEPSEEK_API_KEY}`, and a profile that names another
variable needs that other variable in the environment — what matters is that the two
name the same one.

### 4.3 Which tools ask

**You do not have to decide.** A tool nobody names **asks** first: the run pauses and an
approval card appears (see §6.2). The default is the safe one — one extra click
against a destructive call nobody saw.

There are only two tiers:

| Tier | Meaning |
|---|---|
| `allow` | The tool runs without asking |
| `ask` | The tool waits for your nod |

**Two places name the exceptions**, and that is all of them:

| Where | What it decides |
|---|---|
| The `allows:` line of the file that provides the tool | Which of that file's tools run **without** asking; everything else the file provides asks |
| The `tools:` line of an agent | Which tools that agent **may use** — a tool that is not in it is not in its tool set, so the model does not even see it |

★ **There is no global list.** There is no file where you list the tools that ask, and
none where you list the tools nobody may use. A file names its own exceptions, an agent
names its own, and the two never merge — so the effective tool table of an agent is
always answerable from the configuration alone.

★ **The code carries a default list, and a file only adds to it.** The readers
(`ls` `read_file` `glob` `grep`) and the delegation tools (`task` `write_todos`
`skill`) run without a nod, always — no file can take that away. Everything else asks
until a file names it.

★ **The three tools that change the machine have a file too.** `write_file`,
`edit_file` and `execute` ask by default, and `tools/filesystem.yaml` is where you widen
them:

```yaml
# .openheron/tools/filesystem.yaml
allows: [write_file, edit_file, execute]
```

★ That file is a **tuner, not a switch**: the filesystem tools are always mounted, so the
file cannot add or remove one — it only moves their tier. **Widening is the file's job;
narrowing is the agent's** (`tools`), because narrowing is per agent and
widening is a decision about the tool.

The tools that come with a file of their own say so in that file:

```yaml
# .openheron/tools/http.yaml
methods: [get]
allows: [request_get]
```

`request_get` runs without asking; a `request_post` asks — and it is not even offered
unless `methods` names `post`. The knowledge base says the same thing: `kb.yaml` allows
`kb_search` — searching reads, while `kb_index` writes to the index on disk, so it
asks.

An MCP server says it in its own file and in **its own vocabulary** — the server's own
tool names, not the exposed ones (see §4.7):

```yaml
# .openheron/mcps/github.yaml
allows: [read_issue, list_issues]
```

★ **A tier is not a verdict about the tool.** A tool an agent may not use keeps its own
tier and says why: the effective tool table of an agent reports `blocked` with the
reason `tools` (§4.8). There is no third tier and no global
refusal — a tool is either available to that agent, or it is not in that agent's tool
set at all.

★ **The one exception cannot be widened.** `config_write` (§4.9) edits the
configuration itself, including the file whose `allows:` list would grant it — so a file
that could name it would be granting the tool that rewrites that file. It always asks,
whatever any file says.

### 4.4 External tools (`tools/`)

**No code change needed** — a provider whose configuration is absent mounts nothing, so
writing the file is what turns the tool on. Four file names are recognised today:

| File | Provides |
|---|---|
| `http.yaml` | A generic HTTP request tool |
| `web_search.yaml` | Web search |
| `wikipedia.yaml` | Encyclopedia lookup |
| `kb.yaml` | The knowledge base (see §7) |

★ The knowledge base is the one exception: its file only holds the settings — it is
switched on by a **non-empty `corpus_dir`**, not by the file's presence.

★ **Only these four file names are accepted** under `tools/`; any other name is a
**startup error** and is never silently ignored.

★ **`openheron init` writes the five files above** — the tool configurations are part
of the tree, because the factory agents name `request_get`, `duckduckgo_search` and
`wikipedia_search`, and a name no provider offers is reported as a typo. Pass
`--no-tools` and that directory stays empty. Not writing a file means not enabled: the
tool does not appear in the agent's tool table, and the model cannot see it.

★ **The file's `allows:` line decides what runs without asking** — `tools/http.yaml`
names `request_get`, `tools/kb.yaml` names `kb_search`. Everything else that file
provides asks (§4.3).

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
| `log_level` | `info` | `debug`, `info`, `warn` or `error` — decides which lines reach the output |
| `log_requests` | `false` | Whether to print the redacted model request body and the raw `tools` array at `debug` level; same effect as `OPENHERON_LOG_LLM_REQUESTS=1` |
| `log.file` | `""` | A file to write the log to, relative to the **config root** — empty means stderr only |

★ **`server/` is not hot-reloaded** — a change only takes effect after a restart. That is
why it is **read-only** in the config editor, and submitting it fails with
`section "server" is not editable`.

These environment variables override the file and take precedence:
`LISTEN_ADDR`, `DATA_DIR`, `PROJECT_ROOT`, `FRONTEND_MODE`, `STATIC_DIR`, `OPENHERON_LOG_LEVEL`,
`OPENHERON_LOG_FILE`, `OPENHERON_LOG_LLM_REQUESTS`.

★ `log_requests` and `OPENHERON_LOG_LLM_REQUESTS` are two spellings of the same switch: the
first sets the project default, the second decides one run (**the environment variable wins** —
the config is consulted only when it is unset). When on, every model request writes a redacted
body line and a raw `tools` line at `debug` level. With both off, a **rejected** request still
leaves its raw `tools` bytes, because that is the turn worth keeping. This is a content switch;
it has nothing to do with where `log.file` writes.

★ A `log_level` that is not one of the four falls back to `info`; the config's `deferred`
notices carry the reason.

★ The log goes to the standard error unless `log.file` names a path — relative to the
**config root**. The file is rotated by size, and a file that cannot be opened is a warning
rather than a failed start: the process stays on the standard error and says why.

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

★ **A skill is a folder, and that is what the editor asks for.** `/settings` lists each
partition as a folder in a file tree, so a skill appears as `skills/code-review/SKILL.md`:
type the skill's name, and the file inside it is always `SKILL.md`. The name is the same
one you write in an agent's `skills:` list, and it has to be readable back —
lowercase letters and digits, single `-` or `_` separators, so `code-review` and
`code_review` stay two different skills.

★ **When openheron reads skills from somewhere else, the section says so and refuses the
save.** The rule above means the directory in force can be a different one from
`<config root>/skills/`. Writing into the one nobody loads would change nothing, so the
editor shows what the process actually reads and keeps the section read-only. A process
that found no skills directory at all is not that case: `<config root>/skills/` is then
where the next start will look, and creating the first skill there works.

### 4.7 MCP servers (`mcps/`)

An MCP server is a program that offers tools to a model. openheron mounts **one server
per file**, and **the file's presence is the definition** — there is no `enabled` key.

`mcps/<server>.yaml` — the file's name is the server's name:

```yaml
# .openheron/mcps/github.yaml
transport: stdio
command: npx
args: ["-y", "@modelcontextprotocol/server-github"]
env:
  GITHUB_TOKEN: ${GITHUB_TOKEN}
timeout: 30s
call_timeout: 60s
tools: []
allows: [read_issue, list_issues]
```

The same server over HTTP:

```yaml
# .openheron/mcps/remote.yaml
transport: http
url: https://mcp.example.com/mcp
headers:
  Authorization: Bearer ${REMOTE_TOKEN}
timeout: 30s
call_timeout: 60s
```

| Field | Applies to | Meaning |
|---|---|---|
| `transport` | both | `stdio` (a child process) or `http` (**streamable HTTP** — there is no SSE transport). Defaults to `stdio` |
| `command` | stdio | The executable to run. **Required** |
| `args` | stdio | The command's arguments |
| `env` | stdio | The child's environment, layered over the inherited one |
| `url` | http | The server's address. **Required** |
| `headers` | http | Sent on every request |
| `timeout` | both | The ceiling for connect, initialize and list tools (default `30s`) |
| `call_timeout` | both | The ceiling for one tool call (default `60s`) |
| `tools` | both | A **filter** on the server's own tool names; absent or empty means all of them |
| `allows` | both | The server's own tool names that run **without** asking; everything else asks |

★ **The file's name must match `^[a-z0-9]+(_[a-z0-9]+)*$`.** A name with a run of
underscores is refused: the exposed name `mcp__a__b__x` could not be read back later —
is that the server `a` with the tool `b__x`, or the server `a__b` with the tool `x`?

★ **`openheron init` writes three servers** — `mcps/fetch.yaml`, `mcps/time.yaml` and
`mcps/deepwiki.yaml`. ★ Each of them **defines** a server, so a file that cannot be
parsed is not a server that is off, it is a configuration that does not load. ★ The two
stdio ones run `uvx`, and `uvx` is not installed by default — so the server does not
come up, **the log says so, and the agent carries on**. ★ **Writing a file of your own
under `mcps/` with your own command in it is the act of defining that server**; copy
one of the three and change the `command:`.

★ **The example that is not installed** — `mcps/_example.yaml` in the templates — is the
skeleton the settings editor starts a new server from. It is a scaffold, not a file to
install, so `openheron init` does not write it.

★ **The first start of a stdio server may be slow, and `timeout:` is the ceiling for
it.** The command is often a package runner (`npx`, `uvx`), and the first run installs
the server — that install is part of the start, so a server that needs a minute to
install does not come up within `timeout: 30s`. ★ It then does not come up at all, and
the turn carries on without its tools: run the command once by hand to warm the cache,
or give the file a longer `timeout:`.

★ **What fails at startup:** an unknown `transport`; a stdio server with no `command`; an
http server with no `url`; `env:` on an http server; an agent naming a server that does
not exist.

★ **What does not fail:** a bad `timeout` — it degrades to the default and is reported in
the notices; an unset `${ENV}` in `env:` — that entry is left out and reported. A wrong
timeout changes *how long we wait*, not *what happens*, so it must not keep a server from
starting.

**The names you will see.** A server's tool `create_issue` reaches the model as
`mcp__github__create_issue`. The server's own name and the exposed name are both shown
in the `/mcps` panel (see §6.8).

★ **The file does not have to name the credential.** In a sandbox the variable holds the
`credential@1` sentinel and the host's proxy swaps in the real value on the way out — so
the sentinel reaches the server through the inherited environment, and the same holds for
an http server's `headers:`.

★ **`mcps/` is hot.** A change takes effect on the next turn — no restart.

### 4.8 Which servers an agent gets

**The `mcps:` key in the agent's frontmatter** decides which servers the agent gets — and
mounting one **is** the grant:

```yaml
---
model: deepseek/deepseek-flash
mcps: [github]
---
```

| The `mcps:` key | Means |
|---|---|
| absent | **no** server |
| `[]` | **none** |
| `[github]` | exactly those servers — and all of their tools |

★ A sub-agent carries a list of its **own**, next to its own `skills` and `tools` — the
list is read from the agent that is running, not from the root's.

★ **Mounting is the grant.** `mcps:` says which servers are **started**, and the agent
**gets all of that server's tools** — there is no second list to write.

★ **`tools:` names the tools that are not MCP tools.** An `mcp__<server>__*` entry there
grants nothing: it is dropped, and the configuration reports a **notice** saying so. The
server belongs in `mcps:`.

★ **Approval is separate from mounting.** Mounting gives the agent the tools; whether a
tool runs without a nod comes from the server's own file (`mcps/<server>.yaml`), and the
default there is `ask` — **mounting is not approving**.

**`tools` takes a trailing `*` as a pattern.** An exact name, or a **trailing** `*`:

```yaml
tools: [ls, read_file, config_*]
```

- `config_*` — every tool whose name starts with `config_`.
- `ls` — exactly that one tool.

★ **`mcp*foo` and a lone `*` are refused at startup.** Only a *trailing* `*` is a pattern,
and a lone `*` would be a way of saying "everything" in a list whose whole point is to say
what is there: a rule whose reach differs from what it reads as is a silent mismatch.

### 4.9 The configuration tools

Six tools let an agent read and change the configuration it runs from. They are always
mounted and, like every other tool that is not an MCP one, only **visible** to the agents
whose `tools:` list names them:

| Tool | What it answers | Tier |
|---|---|---|
| `config_get` | One file (`{path}`: its text, revision, and whether it is hot), or `{}`: the partitions, the files that exist, and what the running process holds | allow |
| `config_validate` | `{path, content}`: would this file load, and where exactly is it wrong — the server reports the line. `{}`: check the tree on disk | allow |
| `config_template` | `{path}`: the skeleton that file starts from. `{section}`: that partition's templates. Neither: an index of what to ask for | allow |
| `config_notices` | What the loader noticed but did not refuse: renamed keys, retired directories, values that fell back to a default | allow |
| `config_tier` | `{tool}`: whether it asks. `{agent}`: that agent's tools and their tiers. Neither: the default tier and the whole exception list. Answers use this turn's configuration snapshot, so an agent written earlier in the same turn is not visible yet — the tool says so instead of claiming it does not exist | allow |
| `config_write` | Writes a file: checked against the tree, checked against the revision you read, and refused whole if the merge breaks. Takes effect on the next turn — `applied` means the watcher will pick it up, not that this turn can already see it | **ask** |

```markdown
---
# .openheron/agents/main.md
tools: [ls, read_file, glob, grep, task, write_todos, config_get, config_validate]
---
```

★ **Give `config_get` and `config_validate` before `config_write`.** The first two only
read; the third changes the configuration and always interrupts for approval. An agent
that can validate but not write is a useful reviewer; an agent that can only write is a
loader error waiting to happen.

★ **`config_write` cannot write `server/`, and `config_get` can still read it.** The
partition configures the process itself and is not hot-reloaded, so a change needs a
restart — the write path refuses it rather than saving something that silently does
nothing. Reading is a different question, and it is the one an agent asks when it wants
to know what the process is actually running with.

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

The cap is `agent.max_depth` in `server/config.yaml`. The default `1` means "only the
entry agent may delegate; a sub-agent may not spawn children".

For a deeper delegation chain:

```yaml
# .openheron/server/config.yaml
agent:
  max_depth: 3
```

★ A declared topology that exceeds the cap is a **startup error**, not "truncate to the
cap and carry on". The latter would make the depth protection meaningless.

★ **`server/` is not hot-reloaded** (§4.5) — a deeper cap needs a restart.

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
| **Header** | The agent filter (each chip names an agent; hover it for its context occupancy and spend), the theme switch, and the status of the current turn |
| **Sidebar** | The session list, the "New thread" button, the settings entry |
| **Composer** | Ask a question; `/` opens the slash commands; supports drag/paste attachments |
| **Message list** | Assistant replies, reasoning, tool-call cards, sub-agent output (with attribution) |
| **Approval card** | Appears when your nod is needed: approve / deny (with an optional reason) |
| **Tool card** | The arguments and result of each tool call |
| **Settings** | Entry point to the config editor |

★ The **theme switch** is in the header: light or dark. It follows your desktop until you
pick one, and the choice is remembered.
★ A **table** in a reply has a width of its own: it fills the column, a long cell wraps
inside it, and a table with many columns scrolls inside its own frame instead of pushing
the page sideways.

### 6.1 Slash commands

| Command | Effect |
|---|---|
| `/new` | Start a new session |
| `/stop` | Stop the current turn |
| `/attach` | Attach files to the **next** message — see §6.9 |
| `/settings` | Open the config editor |
| `/agents` | Switch the session's agent |
| `/mcps` | Show the MCP servers of this agent — see §6.8 |
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

`/settings` opens it. It can edit `llm/`, `agents/`, `tools/` and `mcps/`.

The write path has several gates:

- **Nothing is checked in the browser any more.** The editor sends the draft to the
  server and shows the server's answer — each issue with the line it is on. Typing is
  debounced by 250 ms, and an answer that belongs to an older draft is dropped.
- **Every issue names the file it is about.** A problem in another file is shown under
  **that** file's name — and it is a link: click it and the editor opens that file. Your
  own file's problems stay on your own file. ★ A file that does not even parse still takes
  the whole tree with it, so a broken file elsewhere still blocks the save; the status
  line says which file is the reason.
- **A grey line is a hint, a red line is an error.** A hint says "this tree is short
  something" and does not block anything; an error is what keeps Save disabled. An agent
  whose model is not declared is a **hint** on that agent's own file: the tree loads
  without it (see §4.1), so the file in front of you can be perfectly fine.
- **A change that would take a working agent out of the tree is refused.** Repointing an
  agent at a model nobody declares would otherwise make it disappear without a word. The
  refusal is shown on the file you are editing — that is the change being made — and Save
  stays disabled until the agent is back in the tree. Adding an agent, or editing
  something unrelated, is never refused this way.
- **A delete that something still names is refused, and the refusal names the referrer.**
  Deleting `mcps/fetch.yaml` while an agent still mounts it would leave that agent
  pointing at a server that is not there. The message says **which file still names it**
  and **what to remove** — remove that reference first, then delete the file. The same
  goes for a model profile an agent uses and for an agent another agent delegates to.
- `server/` is **not editable** (not hot-reloaded, so a change needs a restart).
- **Literal secrets are rejected** — `api_key must be a reference, not a literal`. Use
  `${ENV_VAR}` or `@file:/path`.
- **A file with no YAML content is rejected** — a file that is empty, or holds nothing
  but comments, carries no configuration at all. The message names the fields the file
  needs; under `tools/` it says which tool is missing what.
- **A file under `mcps/` must be named `<server>.yaml`**, and the server name must match
  the pattern of §4.7 — any other name is refused at save time.
- **Optimistic concurrency**: if the file changed elsewhere, saving reports a conflict
  and lets you choose to overwrite or to back off.
- **A new file whose name the server refuses keeps the dialog open**, with what you typed
  still there and the server's sentence beside the field — the file name rules are the
  server's (`server/` is read-only, `tools/` takes five names, `mcps/` takes
  `<server>.yaml`), so the refusal comes back from the server, not from a copy in the
  dialog.

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

### 6.8 `/mcps` — the MCP servers of this agent

`/mcps` is an **action**, not a prompt: it opens a dialog, and it never writes into the
transcript and never reaches the model. The status is **read**, not generated — a model
describing the servers would be a second, lossy truth about a fact the runtime already
holds.

The dialog is titled "MCP servers" and holds one block per configured server:

- the server's name, its transport (`stdio` or `http`), and its state — `ready`,
  `starting`, `unreachable` or `unused`;
- one row per tool: the server's own name, the exposed name (`mcp__github__create_issue`),
  the tier (`allow` or `ask`), and the state.

A configuration with no server says "No MCP servers are configured".

★ **Opening the dialog starts the servers it asks about**, so it can answer "is my server
broken?" — otherwise the answer would be "run a turn and find out". A server the agent
does not use is `unused` and carries no tool rows.

★ **A server that cannot start does not stop the turn.** The agent runs without that
server's tools — the rest of the tool surface is unaffected — and the panel carries the
reason next to the server. The only thing a broken server costs you is its own tools.

★ **A server on its way is a state of its own: `starting`.** The servers are started in
the background as soon as the configuration is known, so the first turn usually finds
them up. A server that is still starting says `starting` and carries **no error** —
there is no error yet, it is on its way. The tools it brings join the **next** turn.

The **Refresh** button re-reads the state, so you can fix a server and see the fix without
closing the dialog.

### 6.9 Sending attachments

Three ways in, and they all end in the same place — a row of chips above the composer:

| How | What happens |
|---|---|
| The **paperclip** button | Opens the file picker |
| **Dragging** files onto the composer | The composer highlights while the drag is over it |
| **Pasting** | Ctrl+V with an image on the clipboard attaches it — no "save it as a file first" |

★ **A file is attached to a *message*, not to a session.** The chips above the
composer are waiting to go out with your **next** message; once you send, they
leave the composer and appear on the message itself. A file that has been sent
stays with that message — you can still see which one it was weeks later, after
a reload.

★ **The model really gets them — by opening them.** A picture goes in as an
image, so the model *sees* it rather than reading bytes as text. ★ Any other
text file is named by an `@` line at the top of the message, holding an
absolute path; the model calls `read_file` on it. The file's bytes are not in
the request, so the first thing the model does is open the file — for a large
one it reads in chunks with `offset` and `limit`. ★ A binary attachment (a
PDF, an archive) is named in the prompt as "cannot be processed yet": the
model tells you so instead of pretending it read it.
★ **There is no automatic fallback.** A picture the model cannot see fails the
turn with the provider's error; a file the model did not open is not re-sent
behind your back. If an answer looks like the file was never read, tell the
model to read it again.

★ **Accepting a picture is not the same as seeing it.** Whether a picture reaches
the model's eyes is the provider's business: some models read it, some take the
request and never look at it — DeepSeek's `deepseek-v4-pro` does the latter. ★
When that happens the model says so itself; openheron cannot declare a model's
vision in the configuration, so it does not pretend to know.

★ **The × on a chip deletes the file.** Not just from the list: the file is
removed from the session's workspace as well. ★ A file that already went out
with a message is **not** deleted — that message says the file was part of the
question, and deleting it would make the record lie.

★ **You can look at a file before you send it, and after.** A picture shows as
a thumbnail on its chip; everything else shows the icon of its kind. Click it
and the file opens in place: the picture at full size, a PDF in the reader, a
Markdown file rendered, code highlighted, text as text. ★ The same works on
the attachments in the messages above — those are history, so they have no
×, but they open just the same. ★ A file that is gone (deleted with the ×, or
cleaned out of the workspace) falls back to its icon instead of a broken image.

★ **A picture is compressed on the way in.** It keeps its format — a PNG
stays a PNG, with its transparency — and it is scaled down when it is larger
than 1600 pixels on its longest edge. ★ A GIF and a WebP are stored as they
are: the first would lose its animation, and the second cannot be re-encoded
here. ★ The file's **name** in the workspace carries a short hash, so
uploading the same name twice gives two files instead of one overwriting the
other — and the interface still shows you the name you recognise.

★ **A reload does not lose anything.** The files you have not sent yet come
back (the server keeps them), and the files you already sent stay on their
messages.

## 7. The knowledge base

The knowledge base is an **optional** local corpus retrieval layer: it turns a set of
markdown documents into material an agent can search.

★ **What "corpus" means.** "Corpus" is the information-retrieval term for the collection of
documents a search answers from; here it is **one directory**. `corpus_dir` is the directory the
knowledge base indexes — a path relative to the project root, and only the `.md` files under it
are indexed (`kb/index.go:21`).

### 7.1 Turning it on

```yaml
# .openheron/tools/kb.yaml
corpus_dir: docs      # a directory relative to the project root
index: default
model: openai/text-embedding-3-small
top_k: 8
```

- **Not configuring `corpus_dir` means it is off.**
- `corpus_dir` **must be a path relative to the project root**. An absolute path is a
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
| GET | `/v2/mcps` | The MCP servers of one agent (`?agent=`); see §6.8 |
| GET | `/v2/config` | The effective configuration (its `policy` field carries the default tier and the names that run without a nod) |
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
Two possibilities: it is not in that agent's `tools:` list — then the model does not even
see it — or it is an `ask` tool and nobody answered the approval card. Check
`GET /v2/agents/:agentId` for the effective tool table. In the factory configuration
`main` keeps only the readers and the delegation tool, because it orchestrates and hands
the writing to `task`.

**Q: `/v2/run` says `threadId is required`**
Create the session with `POST /v2/sessions` first.

**Q: The model does not see the MCP server's tools?**
Open `/mcps` (see §6.8). A server the agent does not mount reads `unused`; a tool the agent
cannot reach reads `blocked`, and the reason names `mcps` — the agent does not mount that
server. A server that reads `unreachable` carries the error that says why it could not be
started.

**Q: An MCP server does not start?**
The startup error names the file and the field. `mcps/` is hot, so a fix takes effect on
the next turn — no restart.

**Q: The panel says a server is `starting`?**
It is on its way: the servers are started in the background when the configuration is
known. The tools it brings join the **next** turn — press **Refresh** to see them.

**Q: The port is taken / the interface will not open?**
Change the port in `listen_addr`, or override it with the `LISTEN_ADDR` environment
variable.

**Q: How do I see what the server actually loaded?**
`GET /v2/config` returns the effective configuration; `GET /v2/health` returns the
health status and any `deferred` notices; `GET /v2/runtime` returns the runtime status.
The startup log also prints the config root path.

**Q: On Windows the agent's shell tool fails, or runs commands the model wrote
for another system?**
The `execute` tool picks its shell when the server starts — see §1.3 ("On
Windows"). It prefers a POSIX shell and falls back to `cmd.exe` when the
machine has none; either way the tool's description tells the model which
dialect to write, so the command is written for the shell that runs it.
★ The startup log names the shell it picked.

**Q: Is the `grep` tool weaker without ripgrep?**
It searches with its own engine: the same regular expressions, the same caps, the same
budget, and the same answer to "is this file there". What it does not do is read
`.gitignore`, and it knows fewer file types — a type it does not know is refused
rather than guessed at. ★ The startup log names the engine that was picked; install
ripgrep to get the other one.
