# MCP Integration Design — a runtime tool set, a fixed policy table

> Status: **§1 describes the code as it is. §2 onwards is a proposal** — nothing from §2
> onwards is in the tree. There is no MCP dependency in `go.mod`, no `mcps/` config
> directory, and no code that speaks the protocol.
>
> Scope: how an MCP (Model Context Protocol) server becomes a set of tools an openheron
> agent can call, how those tools are named, and how the policy layer decides whether
> they may run. Not in scope: the MCP protocol itself, the transport implementations
> (they belong to `mcp-go`), and the sandbox packaging (see `sandbox-kit-design.md` §2.3).

## 1. The code as it is

### 1.1 Where the tools come from

```
tools/*.yaml → spec.loadTool → decl.ToolsCfg
  → toolRegistry.Build (one Provide per provider)
  → snapshot.buildHeavy → heavyDeps.tools
  → applyToolConfig → deep.Config.ToolsConfig.Tools
  → deep.New → the eino agent
```

| Step | Where |
|---|---|
| Load one `tools/` file | `agentkit/spec/load.go:141` `loadTool` |
| The declaration struct | `agentkit/spec/decl/decl.go:288` `ToolsCfg` |
| Build every provider's batch | `agentkit/build/tools.go:48` `toolRegistry.Build` |
| Materialize it, once per snapshot | `agentkit/build/snapshot.go:47` `buildHeavy` |
| Hand it to eino | `agentkit/build/assemble.go:31` `applyToolConfig` |

★ `buildHeavy` runs under a `sync.Once` (`agentkit/build/snapshot.go:40-45`). The tool
set is therefore **fixed for the lifetime of one snapshot** — it is rebuilt when the
snapshot generation changes, not when a tool's behaviour changes.

### 1.2 The provider contract

`agentkit/build/tools.go:19` is the whole extension point:

```go
type toolProvider interface {
	ID() string
	Optional() bool
	Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error)
}
```

The registry is `defaultToolRegistry` (`agentkit/build/tools.go:29`), and it carries one
line that names this design:

```go
		// mcpToolProvider{}, projectToolsProvider{}, ...
```

★ That comment is the **only** MCP trace in the tree. There is no MCP dependency, no
`mcps/` directory, and no code that speaks the protocol.

### 1.3 The names are the policy

The tool name is the key of every table that decides what a tool may do:

| Table | Where | What it decides |
|---|---|---|
| `Policy.TierOf` | `agentkit/build/policy.go:39` | `allow` / `ask` / `deny` |
| `Table.Blocked` | `agentkit/build/policy.go:94` | denied (outer) / disabled (inner) |
| `Table.Filter` | `agentkit/build/policy.go:107` | drops the tools from `runCtx.Tools` |
| `Builder.EffectiveTools` | `agentkit/build/introspect.go:47` | what the API reports |

★ **Every one of those lookups is an exact match.** `TierOf` is `slices.Contains` plus a
map lookup; `Blocked` is two map lookups. There is no wildcard, no prefix, no pattern
of any kind.

The gate itself needs no permission code — it reads the tier and acts:

| Piece | Where |
|---|---|
| The synchronous path | `agentkit/build/gate.go:55` `WrapInvokableToolCall` |
| The streaming path | `agentkit/build/gate.go:92` `WrapStreamableToolCall` |
| The one decision both share | `agentkit/build/gate.go:131` `decide` |
| The chain | `agentkit/build/gate.go:172` `toolChain` |

### 1.4 The config partitions

| Partition | Where | Notes |
|---|---|---|
| `tools/` | `agentkit/spec/config.go:62` | one file per tool, **name is a whitelist** (`agentkit/spec/load.go:141`), hot |
| `policy/` | `agentkit/spec/config.go:52` | `ask` / `deny` / `limits`, hot |
| `agents/` | `agentkit/spec/config.go:47` | frontmatter, incl. `disabled_tools`, hot |

★ `policy/` carries **two** files today: `permissions.yaml` (the tiers) and `limits.yaml`
(the counts). `limits.yaml` holds no tool names — the counts are per agent and per
category — so it plays no part in this design.

## 2. What an MCP server changes

### 2.1 Two things an MCP client owns

An MCP server differs from every tool openheron has today in exactly two ways.

| # | What | Why the current code assumes otherwise |
|---|---|---|
| 1 | **A long-lived connection** — a child process (stdio) or an HTTP session | `Provide` returns a batch and returns; nothing in `toolProvider` has a lifetime. `buildHeavy` runs once per snapshot (`agentkit/build/snapshot.go:40`), so a client has to outlive the call that created it |
| 2 | **A tool set decided at runtime** — the server decides which tools it offers | The policy table is keyed by tool name, and every name in it is known when the config is written |

★ The second one is the load-bearing one. The policy layer is a **static table keyed by
name**, and an MCP server's names are not known until the connection is up.

### 2.2 The component: what it is, and what it is not

`github.com/cloudwego/eino-ext/components/tool/mcp` (v0.0.9) is the adapter. Its entire
surface:

```go
func GetTools(ctx context.Context, conf *Config) ([]tool.BaseTool, error)
```

and the struct it takes:

```go
type Config struct {
	Cli                   client.MCPClient
	ToolNameList          []string
	ToolCallResultHandler func(ctx context.Context, name string, result *mcp.CallToolResult) (*mcp.CallToolResult, error)
	CustomHeaders         map[string]string
	Meta                  *mcp.Meta
}
```

★ **The component's own README is out of date.** Its `Config` block lists only `Cli` and
`ToolNameList`; the source has five fields. The three it omits are the useful ones:
`ToolCallResultHandler` is the only place a result can be inspected or rewritten before
it reaches the model, and `CustomHeaders` / `Meta` are how a server is addressed.
Read the source, not the README.

What the component does **not** do:

| Not done | Consequence for openheron |
|---|---|
| No client lifecycle — no spawn, no `Start`, no `Initialize`, no `Close` | openheron owns the connection: create it, initialize it, keep it alive, tear it down when the snapshot is replaced |
| No transport choice | stdio / SSE / streamable HTTP are `mcp-go`'s `client` constructors; openheron picks one |
| No configuration format | there is no server registry schema yet — §5 designs one |
| No streaming | the wrapper implements `Info` and `InvokableRun` only, so the tool is a `tool.InvokableTool`, never a `tool.StreamableTool`. The whole result arrives as one JSON blob |

★ The dependency versions are compatible. The component's own `go.mod` pins
`github.com/cloudwego/eino v0.6.0`, but the APIs it uses still exist in v0.9.22
(`schema.NewParamsOneOfByJSONSchema`, `eino@v0.9.22/schema/tool.go:297`). Verified by
compiling `components/tool/mcp v0.0.9` against `eino v0.9.22` with
`github.com/mark3labs/mcp-go v0.43.0`.

### 2.3 The three problems that decide this design

| # | Problem | Where it bites | Resolved by |
|---|---|---|---|
| 1 | **A server's tool name can collide with a reserved name** — an MCP server that offers `ls` or `grep` | `agentkit/build/tools.go:92` — `validateBatch` returns an error, `Build` propagates it (`agentkit/build/tools.go:66`), and the **whole agent fails to start**. Not "this tool is dropped" — the process is down | §3 |
| 2 | **A server's tool name can be illegal for the provider** — server-prefixed names such as `github.create_issue` are common | the model provider's function-name character set does not accept `.`; the call fails at the model, not at load time | §3 |
| 3 | **A runtime tool set cannot be named in a static policy** — the policy is a table keyed by name, and the names are not known until the connection is up | `agentkit/build/policy.go:39` and `:94` — exact match only. Every MCP tool lands in the `allow` fallback, which is **not** a safe default for a tool that runs an arbitrary process | §4 and §7.3 |

★ Problem 3 is the reason a namespace (§3) and a tier that lives with the tool (§7.3)
are two halves of one decision rather than two conveniences: **the namespace is what
makes a prefix meaningful**, because a prefix only separates cleanly if the prefix
boundary is unambiguous — and the per-file tier is what makes the namespace usable,
because a file can speak about its own tools without knowing their exposed names.

## 3. The namespace: `mcp__<server>__<tool>`

Every MCP tool is exposed to the model under a namespaced name:

```
mcp__<server>__<tool>
```

### 3.1 The rules

| Segment | Who decides | Rule |
|---|---|---|
| `server` | **we do** — the `<server>.yaml` file name | must match `^[a-z0-9]+(_[a-z0-9]+)*$`; a violation is a **load-time hard error** |
| `tool` | **the server does** | characters outside `[A-Za-z0-9_-]` are replaced with `_`; case is preserved |

★ **The server name may not contain a run of underscores.** Otherwise
`mcp__a__b__x` cannot be read back — is that server `a` with tool `b__x`, or server
`a__b` with tool `x`? The prefix syntax in §4 cuts on `mcp__<server>__`, so an
ambiguous boundary would silently widen a rule.

★ **The composed name is never parsed back.** Nothing splits `mcp__github__create_issue`
into its two halves again — the policy layer matches whole names and prefixes only.
That is what lets the tool segment keep its original case and characters.

### 3.2 The three outcomes for a tool that cannot be named

| Case | Rule | Why not the alternative |
|---|---|---|
| Composed name longer than 64 characters | **skip the tool, warn** | truncating collides and drops exactly the part that distinguishes the tool |
| Two tools of one server sanitize to the same name | **skip the second, warn** | the first one wins silently, and the model would call the wrong tool with the other's arguments |
| The name is otherwise legal | expose it | — |

★ **Skip and warn, never fail the process.** The tool name is chosen by a **remote
server**; the user cannot rename it. A server that adds one odd tool should cost that
tool, not the whole agent. The line is the same one `tools/` draws (§1.4): **our own
configuration fails loudly at load time, somebody else's content is dropped with a
notice.**

★ A skipped tool must be **visible**: the warning names the server, the original tool
name, and the composed name it would have had. "A feature seems to be missing" is the
symptom this design refuses to produce.

### 3.3 The namespace closes problem 1 as well

A server that offers `ls` now offers `mcp__<server>__ls`, which does not collide with
`reservedToolNames` (`agentkit/build/tools.go:39`). The reverse follows, and it is a
rule of its own:

> **Only MCP tools may use the `mcp__` prefix.**

Without it, a built-in tool could claim the namespace and a prefix rule written for MCP
would quietly cover it.

## 4. Prefix matching in an agent's `disabled_tools`

★ An agent's `disabled_tools` accepts a **prefix pattern** in addition to an exact
name.

| Entry | Matches |
|---|---|
| `execute` | exactly the tool named `execute` |
| `mcp__github__*` | every tool of the server named `github` |
| `mcp__*` | every MCP tool, from any server |

★ **The tiers are not in this list at all.** They moved into the tool's own file
(§7.3), and a file speaks **its own vocabulary** — so "every tool of this server asks"
is an **empty** `allows:` in `mcps/<server>.yaml`, not a pattern in a shared list. That
form is **stronger** than a pattern: it covers the tools the server has **not offered
yet**.

★ **The global `deny` is gone too** (§6.3 of the configuration design). "Not exposed" is
said in exactly two places now: a file's **whitelist**, which picks the ones to offer out of
what the server has, and an agent's **`disabled_tools`**, which says what that agent does
not get. The narrowing is the one statement left that is about a **namespace** rather than
about a tool — "this agent gets no MCP tools" — and a prefix is what names a namespace.

### 4.1 The syntax rules

| # | Rule | Reason |
|---|---|---|
| 1 | Only a **trailing** `*` is a prefix pattern; a `*` anywhere else is a **load-time hard error** | `mcp*foo` looks like a wildcard and is not one — a rule whose reach differs from what it reads as is a silent mismatch |
| 2 | A lone `*` is a **load-time hard error** | `disabled_tools: ["*"]` would take the agent's own tools away as well; that is a decision to write out, not a wildcard |
| 3 | The part before `*` must not be empty | rule 2 |
| 4 | An exact entry and a prefix entry are **equivalent** — any match removes the tool for that agent | there is no tier to choose here: the tool is simply not there for that agent |

### 4.2 The prefix syntax adds no tier

★ The tiers live in the tools' own files now (§7.3), and only two are left — the
`deny` tier went with the global list:

```
allow (if the file's allows list names it) → ask (everything else)
```

★ The prefix syntax only decides **whether an entry matches a name** — it adds no tier,
and it reorders none. ★ And there is no outer boundary any more: the guarantee that a
global `deny` cannot be relaxed by an agent's `disabled_tools` has **no counterpart** in
the proposal (§6.3 of the configuration design).

★ **`policy/limits.yaml` is not part of this.** It holds counts, not tool names, so its
matching semantics stay as they are.

### 4.3 Where the prefix syntax applies

| Place | Where it is written | Meaning |
|---|---|---|
| an agent's `disabled_tools` | `agentkit/build/topology.go:64` | this agent does not get these tools |

★ It speaks in **exposed** names (`mcp__github__delete_repo`), because that is what the
tool table looks up. The per-file tiers do not (§7.3): a file speaks its own vocabulary,
and the provider translates.

### 4.4 What it costs

★ A prefix rule is a **blunt instrument**: `disabled_tools: ["mcp__*"]` takes every MCP
tool away from that agent. That is usually the intent, but it is one line, and it is easy
to write by accident. A rule that names a single server (`mcp__github__*`) is the one
worth reaching for first.

## 5. The `mcps/<server>.yaml` partition

### 5.1 Why a partition of its own

MCP servers are configured one file per server:

```
.openheron/mcps/
└─ github.yaml
```

★ **It is not a file inside `tools/`.** `tools/` is "one file = one tool"
(`agentkit/spec/load.go:141` whitelists the file names), and the entity in MCP is the
**server**: one server has one command, one argument list, one environment, one
transport and one lifecycle. Putting N servers in one file means a nested list under a
partition whose rule is "the file content is the configuration, with no wrapper key" —
and when the file is replaced wholesale, there is no way to say which server changed.

The rule from §2 of the config design — *one entity, one file* — applies to the server,
not to the tool.

### 5.2 The fields

| Field | Type | Default | Meaning |
|---|---|---|---|
| `transport` | `string` | `stdio` | `stdio` or `http`; an **unknown value is a hard error** — no silent fallback to stdio |
| `command` | `string` | — | stdio only, **required**: the executable to run |
| `args` | `[]string` | `[]` | stdio only: its arguments |
| `env` | `map[string]string` | `{}` | variables **layered over** the inherited environment (§5.5); values are expanded like every other value |
| `url` | `string` | — | http only, **required**: the server address |
| `headers` | `map[string]string` | `{}` | http only: sent on every request |
| `timeout` | `LenientDuration` | `30s` | the ceiling for connect + initialize + list tools |
| `call_timeout` | `LenientDuration` | `60s` | the ceiling for **one tool call** (§6.5) |
| `tools` | `[]string` | `[]` → all | ★ **a filter, not a switch** — the server's **raw** tool names to offer **out of the ones it has** (not the namespaced ones); absent = all of them |
| `allows` | `[]string` | `[]` | the server's **own** tool names that run **without a nod** — ★ everything else this server offers asks, including the tools it has **not offered yet** |

★ **`command` is resolved in the sandbox's PATH, never against the project root.** The
workspace must not get to choose what runs: a command resolved against the project root
would let a checkout decide which executable the agent runs, with the agent's
privileges. ⇒ the executable comes from **the Kit** — the mixin's install hook puts it
there (§8) — and outside a Kit the user installs it themselves.

★ So the two sides describe one server from their own angles: the **mixin** says what to
install, the **config** says how to invoke it (`command` plus `args`). The config's
`command` names what the Kit laid down.

★ The keys mean the same thing in every file of `tools/` and `mcps/`, and they are written
in **that file's own vocabulary**: `allows: [create_issue]` here is the server's own name,
not the composed one. The provider translates it to the name the model sees (§7.3).

★ **The default is `ask`, and `allows:` is the exception.** There is no second key and no
per-group default — the same rule covers the filesystem tools, the HTTP tool and an MCP
server. See `docs/config-design.md` §6.6.

★ **There is no `deny` in a file, and there is no global one either** (§4). "Not
exposed" is said in exactly two places: this file's **whitelist** — a tool that is not
in `tools:` is not offered — and an agent's **`disabled_tools`**.

★ `timeout` is a **lenient** duration: a wrong value degrades to the default and is
recorded in `deferred`, exactly like `debounce` and `start_timeout`. A wrong timeout
changes *how long we wait*, not *what happens* — it must not keep the process from
starting.

★ `tools: []` means **all**, not **none**. An empty whitelist that meant "nothing" would
be indistinguishable from a missing key — and it is a **filter** on what the server
offers (§5.3), not a switch.

### 5.3 The file defines the server

There is no `enabled` field. A server is **defined** because its file exists; removing
the file removes the definition. This is the rule `tools/` already follows for the tools
whose provider reads a configuration file — ★ and it keeps **whether the server exists**
in one place, instead of splitting it between "the file is there" and "the flag inside it
says on".

★ **The file defines the server; it does not decide what the server offers.** The tools
a server has come from the server itself, and the file's `tools:` picks the ones worth
offering out of them (§5.2).

★ **The file says what a server is; the agent says who gets it.** Which agents load a
server is the agent's own `mcps:` list in its frontmatter: ★ **absent = every configured
server, `[]` = none** — the same pointer trick `skills` already uses, for the same
reason: the common case is **one** place. Write the server file, and every agent that
has not narrowed its list gets it.

★ The server name is the file name without `.yaml`, and it is the namespace segment
(§3.1). A file name that does not match `^[a-z0-9]+(_[a-z0-9]+)*$` is a **load-time
hard error** — we wrote it, so we can fix it.

### 5.4 The partition is hot

`mcps/` joins the hot partitions, next to `tools/` and `policy/`: changing a server's
command or timeout should take effect on the next turn, without a restart.

★ This means the client lifecycle has to survive a reload: when the snapshot is
replaced, the stdio child process is torn down and a new one is started. A hot
partition whose resources cannot be rebuilt is a partition that lies about being hot.

### 5.5 The child's environment

★ **The child's environment is openheron's own environment, overlaid with the `env:`
map.** Same name: the configuration wins.

The child is a child process of this one, so it starts from the environment this
process has — including everything the sandbox runtime injected into the container.

★ **That inheritance is what makes the credential path work without configuration**
(§8.1): the runtime puts a **sentinel** into the container's variable, and the MCP
server reads it from there. A configuration that had to name every variable would
force the user to write down the credential's variable name — a fact that belongs to
the MCP server, not to openheron.

The `env:` map is for the variables the MCP server needs **beyond** that: a log level,
a feature flag, a second token the server expects under its own name. Its values go
through the same expansion as everything else in the configuration (`${ENV}` /
`@file:`), and an unset `${ENV}` is not fatal — it is recorded in `deferred`, like every
other missing value.

★ **Inheritance is not a new exposure.** The agent's own tools already read the same
things: `read_file` reads the project, and `execute` runs a command in this
environment. An MCP server that sees the environment sees nothing the agent could not
already see. The difference is only that `execute` is not in the `allows:` list and
an MCP tool may not be. ⇒ If that asymmetry matters to you, put `mcp__*` in that
agent's `disabled_tools` (§4).

★ The configuration's `env:` does not **replace** the inherited environment, it
**layers on top** of it. A configuration with an empty `env:` still produces a child
with a full environment.

### 5.6 The configuration templates

★ **The comments in a template are English**, because a template is content that lands
in the user's configuration (`AGENTS.md`).

#### `mcps/<server>.yaml` — a server over stdio

```yaml
# .openheron/mcps/github.yaml
#
# ★ One MCP server, one file. The file's name is the server's name, and it is
#   the namespace segment of its tools: mcp__github__<tool>.
#
# ★ The tier lives here, next to the server it is about, and it is written in
#   the server's OWN vocabulary — "create_issue", not "mcp__github__create_issue".
transport: stdio
command: npx
args: ["-y", "@modelcontextprotocol/server-github"]
env:
  # ★ Expanded at load time. In a sandbox the variable holds a sentinel and the
  #   host proxy swaps in the real key on the way out (§8.1).
  GITHUB_TOKEN: ${GITHUB_TOKEN}

# ★ The default is ask, so an empty list means "every tool of this server asks".
#   A trusted server names its exceptions:
allows: [read_issue, list_issues]

# ★ The tools to offer, by the server's own names, picked out of the ones the
#   server actually has. Absent = all of them.
tools: [create_issue, list_issues, search_issues]

# ★ To take one tool out, leave it out of the list above. The list filters what
#   the server offers; it never adds a tool the server does not have.
```

#### `mcps/<server>.yaml` — a server over http

```yaml
# .openheron/mcps/files.yaml
transport: http
url: https://mcp.example.com/mcp
headers:
  Authorization: Bearer ${FILES_TOKEN}
timeout: 30s
call_timeout: 60s
```

★ The **host** holds the key and the proxy substitutes it, so the container never sees
it — but an http server on the host may still need the sandbox to authenticate *to it*,
and that is the `headers:` map (§8.1).

#### `tools/filesystem.yaml`

```yaml
# .openheron/tools/filesystem.yaml
#
# ★ The filesystem tool set. These tools are always there — this file tunes them.
#
# ★ The default is ask, so the list names the exceptions: the read-only tools
#   run straight through, and write_file / edit_file / execute ask.
allows: [ls, read_file, glob, grep]
# tools: [ls, read_file, glob, grep]   # uncomment for a read-only agent
```

#### `tools/kb.yaml`

```yaml
# .openheron/tools/kb.yaml
corpus: ""
index: default
model: ""
query_instruction: ""
batch_size: 10
max_batch_chars: 1200
top_k: 8
# ★ kb_search reads and does not ask; kb_index writes the index on disk, so it asks.
allows: [kb_search]
```

#### `tools/http.yaml`

```yaml
# .openheron/tools/http.yaml
methods: [get]
headers: {}
timeout: 30s
# ★ The read methods run straight through; the write methods change the outside
#   world, so they ask.
allows: [get]
```

#### ★ Who writes these files

| Writer | What it writes |
|---|---|
| the user | the `mcps/<server>.yaml` files, by hand |
| `openheron init` | ★ an **example** — and an example cannot be a live `.yaml`: the file **defines** the server (§5.3), so a live one would make every agent that does not narrow its list try to start a server that is not installed. ⇒ `mcps/example.yaml.disabled`: the shape is right there, the name says it is not a configuration yet, and renaming it is the act of defining it |
| the config editor | ★ the same template, through its "new file" dialog — the editor can write `mcps/` |

## 6. The assembly path

### 6.1 The provider

The provider follows the contract of §1.2 and registers at the placeholder line
(`agentkit/build/tools.go:35`):

| Piece | What it does |
|---|---|
| `mcpToolProvider.ID` | the provider name in logs and errors |
| `mcpToolProvider.Optional` | **true** — an unreachable server must not keep the agent down |
| `mcpToolProvider.Provide` | connect to each configured server, list its tools, namespace and sanitize the names, return the batch |

★ `Optional() == true` has a price, and `developer-manual.md` §3.1 step 3 says so
plainly: the provider's `Warn` is the **only** notice — the tools do not enter the
table, and the user sees "this feature seems to be missing". For an MCP server that is
exactly the wrong symptom to leave unexplained, so the warning must name the server,
the transport and the error.

★ **An unreachable server is a warning, not a row.** The API does **not** expose the
servers that are configured but unreachable. The user learns from the warning and from
the tools that are missing; an endpoint that listed "the server you configured is not
there" would be a second truth to keep in sync with the first, and it would answer a
question the warning already answers.

### 6.2 The client lifecycle

The component does not own it (§2.2), so **the manager** does (§6.6) — the provider asks
it for the tools and gets them:

| Step | stdio | http |
|---|---|---|
| create | `client.NewStdioMCPClient` | `client.NewStreamableHttpClient` / `client.NewSSEMCPClient` |
| start | the child process is spawned | the HTTP session opens |
| initialize | `Initialize` with the protocol version and the client info | the same |
| list tools | `GetTools` → `ListTools` | the same |
| teardown | the child process is killed when the snapshot is replaced | the session closes |

★ The initialization result is **discardable**: the provider only needs the tool list.
A failure to initialize is the same failure as an unreachable server — the server does
not contribute tools, and it says so.

### 6.3 The tool batch

Each tool the server offers becomes an eino tool:

| Property | Value |
|---|---|
| Name | `mcp__<server>__<tool>` (§3) |
| Description | the server's description |
| Parameters | the server's JSON Schema, converted with `schema.NewParamsOneOfByJSONSchema` |
| Kind | `tool.InvokableTool` — the component implements `InvokableRun` only |

★ The kind matters once: the gate's synchronous path (`agentkit/build/gate.go:55`)
covers it, and the streaming path (`agentkit/build/gate.go:92`) is not needed. **The
streaming gap that the filesystem tools had does not exist here** — but only because the
component never produces a streaming tool.

### 6.4 The output cap

`tools/` caps every tool's output at 20000 bytes (`agentkit/build/nettools.go:26`,
`cappedTool` at `:98`, `capTool` at `:115`). The cap exists because a tool's output
**goes into the model's context verbatim**, and a 10MB page breaks the turn — it reads
as "the model suddenly starts making things up", not as an error.

★ An MCP tool's output is no different: it is somebody else's text, entering the
context unfiltered. The cap applies to MCP tools too, and the component's
`ToolCallResultHandler` is where it is implemented — the hook the component's README
never mentions (§2.2).

### 6.5 One client per server, and the calls are serialized

★ **One client per server.** The eino-ext component wraps a single
`client.MCPClient`, and every tool of that server shares it. One server is one
connection — and, for stdio, one child process.

★ **The calls to one stdio server are serialized.** This is a correctness decision,
not a performance one. `mcp-go`'s stdio transport writes the request straight to the
child's stdin:

```go
// mcp-go@v0.43.0, client/transport/stdio.go — SendRequest
c.mu.Lock(); c.responses[idKey] = responseChan; c.mu.Unlock()
...
if _, err := c.stdin.Write(requestBytes); err != nil {   // ← no lock
```

`c.stdin` is the pipe from `cmd.StdinPipe()`. POSIX makes a pipe write atomic only up
to `PIPE_BUF` — 4096 bytes on Linux — so a larger JSON-RPC line can be **interleaved
with another call's line**, and the server reads a corrupted frame.

★ And openheron does call tools concurrently: two sessions run at the same time, and
an agent instance is reusable concurrently (`session-concurrency-design.md` §2.5). Two
sessions hitting the same MCP server are two concurrent writes to the same pipe.

⇒ **The provider wraps the stdio tools of one server with a per-server mutex**, and
that wrapper is what the gate and the cap see — the same shape as `cappedTool` in
`nettools.go`. The mutex **waits**; it does not fail the second call. The cost is
head-of-line blocking inside one server, which is the cheaper of the two: the
alternative is a child process per call.

★ **http servers are not serialized.** Each request stands on its own, and an MCP tool
that waits on a slow API should not block the other tools of the same server. The
serialization is a property of the **transport**, not of MCP.

★ **A call has a deadline.** A mutex without one is a way to hang a turn: an MCP
server that never answers would hold the lock for ever, and every other call of that
server would queue behind it. ⇒ `call_timeout` (LenientDuration, default `60s`) bounds
one tool call. It is lenient, like every other duration that only decides *how long we
wait*.

### 6.6 The mcps manager

★ **One manager owns the clients**, not the provider. The provider answers "what tools
does this snapshot have"; the manager is what a client outlives.

| Responsibility | What it means |
|---|---|
| **one client per server** | a server is one connection, and for stdio one child process |
| **start only what the agent asked for** | the manager starts the servers in that agent's `mcps:` list (§5.3) — a server nobody lists is never started, and never becomes a child process |
| **start lazily** | a server is started when a turn first needs it, not at boot |
| **restart a crashed child** | a stdio server that dies is started again on the next call — a crash is not a configuration error, and a dead server that stays dead is the worst outcome |
| **notice a changed tool list** | the manager re-lists the tools; when the list changed, the **generation is bumped** and the tools are rebuilt, exactly like a configuration reload (§6.7) |
| **tear down on the snapshot swap** | when the snapshot is replaced, the old generation's clients are closed and their children killed (§5.4) |

★ **The tool list is the manager's business, not the snapshot's.** A server's tools are
discovered at runtime, so a snapshot that froze them would need a restart to see a tool
the server added yesterday. ⇒ the manager polls, and a change re-enters the same path a
configuration change takes.

### 6.7 From a file to one tool call

The whole path, from the file on disk to the gate's decision:

```
.openheron/mcps/github.yaml
  │  spec.loadMCP (agentkit/spec/load.go) — the file name and the fields are
  │  validated here, and a bad one is a startup error
  ▼
decl.MCPServerCfg — transport / command / args / env / allows / tools / timeouts
  │
  │  snapshot.buildHeavy (agentkit/build/snapshot.go)
  │    → mcpToolProvider.Provide (agentkit/build/mcptools.go)
  ▼
the mcps manager (agentkit/build/mcpmanager.go):
  per server → client.NewStdioMCPClient / client.NewStreamableHttpClient
            → cli.Start(ctx) → cli.Initialize(ctx) → mcpp.GetTools(ctx, &mcpp.Config{Cli: cli})
  │
  │  each tool:  name = mcp__<server>__<tool>     (sanitized; a bad name is skipped
  │                                                with a warning — §3.2)
  │              tier = the file's allows list, read in the SERVER's own names
  │                    and mapped to the exposed ones
  ▼
[]tool.BaseTool — the stdio ones wrapped in the per-server mutex (§6.5)
  │
  ▼
toolRegistry.Build → heavyDeps.tools → applyToolConfig → deep.Config.ToolsConfig.Tools
  │
  ▼
the model calls mcp__github__create_issue
  │
  ▼
the gate: Policy.TierOf("mcp__github__create_issue")
  ├─ allow → the tool runs → the cap truncates the output → the result reaches the model
  └─ ask   → the turn is interrupted for a human → resumed
```

★ **The tier resolution** is one lookup plus a fallback, and the gate never asks which
file a tool came from — the assembly fills the table with the **exposed** names:

```
Policy.TierOf(name):
  1. the file that provides the tool:
       its allows list, in its own names            → allow
       otherwise                                    → ask
  2. otherwise                                       → ask
```

★ **The assembly happens in `agentkit/build`, not in `agentkit/spec`** (§7.3): only the
provider knows its file's vocabulary and the exposed names behind it, and `agentkit/spec`
may not import `kb` (the iron rule) nor eino (where the filesystem tool names live).

★ **The reload is part of the flow.** `mcps/` is a hot partition, so the whole chain
above runs again on the next turn after a change: the loader re-reads the file, the
snapshot is replaced, the stdio child is torn down and a new one started, and the tier
table is rebuilt with the exposed names. The gate reads the new tier from the next turn
on — no restart, and no window where a tool keeps its old tier.

★ **And a server that changed its own tool list takes the same path.** The manager polls
the list (§6.6); when it changed, the generation is bumped and the tools are rebuilt —
the same road a configuration change takes, and the same absence of a restart.

## 7. The gate and the tiers

### 7.1 The tiers apply unchanged

A tool named `mcp__github__create_issue` is looked up exactly like any other:

```
Policy.TierOf("mcp__github__create_issue")
```

and lands in `ask` unless the server's file names it in `allows:` (§7.3) — which is
the same rule every tool follows. The gate code does not change at all: it reads the
tier and acts (§1.3).

★ **This is the whole point of the namespace.** The gate, the tables, the API's
`EffectiveTools` view and the per-agent narrowing all work on names. A name that is
namespaced is a name they already understand — no new concept has to travel through the
policy layer, the API or the frontend.

### 7.2 The stdio privilege problem

★ Under stdio, the MCP server is a **child process of openheron**: it can write files,
reach the network and read anything openheron can read. The "only public networks"
rule that §6.6 of the config design sets for the HTTP tools **does not apply** — it
bounds what the *tool* may reach, not what the *server* may do.

This is the strongest argument for the namespace and for giving each server its own
list: they are how a class of external tools gets a tier **before** anyone has
enumerated its members. An **empty** `allows:` in `mcps/<server>.yaml` is one line that
says "every tool from this server asks first" (§7.3).

### 7.3 The tier lives with the tool

★ **A tool's tier is written in the tool's own file, in that file's own vocabulary.**
There is **no global list** any more (§4): the `ask` tier moved next to the tool it is
about, and the global `deny` went with it — "not exposed" is said in exactly two places,
a file's whitelist and an agent's `disabled_tools`:

```yaml
# .openheron/mcps/github.yaml
#
# ★ The default is ask, so an empty list means "every tool of this server asks".
#   A trusted server names its exceptions:
allows: [read_issue, list_issues]
```

★ **Why the file's own vocabulary.** A tier is a statement about **one tool**, so it
belongs in that tool's file — and there it can use the names that file already uses. The
old central file had to name the internal tool name (`request_post`) while the tool's own
file said `post`; ★ two vocabularies for one tool is a mismatch waiting to happen, and a
mistyped name stays invisible until the tool behaves differently than the file says.

| Where | Vocabulary | Decides |
|---|---|---|
| a file's `tools` / `allows` | **the file's own** names (`create_issue`) | which of the server's tools to offer, and which of those need no nod |
| an agent's `disabled_tools` | exposed names | this agent does not get these tools |

★ **The order**: the file that provides the tool first, then the agent's narrowing —
which removes the tool for that agent and nothing else.

★ **A file has no `deny` of its own**, and that is deliberate: "not exposed" is already
the whitelist's job. ★ And there is **no global `deny` to say "never"** — the two levels
above do not duplicate each other: the file says **which of the server's tools to offer**,
the agent says **what this agent does not get**.

★ **The assembled policy keeps its shape.** `build.Policy` and `TierOf`
(`agentkit/build/policy.go:39`) still take a name and answer a tier; what changed is
where the entries come from: the providers assemble them, each from its own file's
declarations and its own vocabulary. ⇒ the gate, the tables, the API's
`EffectiveTools` view and the frontend do not learn a new concept.

★ **The assembly happens in `agentkit/build`, not in `agentkit/spec`.** Only the provider
knows its file's vocabulary and the exposed names behind it, and `agentkit/spec` may not
import `kb` (the iron rule) nor eino (where the filesystem tool names live).

★ **The default is `ask` for a server — and for everything else.** The built-in groups
are not the exception any more: the code ships an allow list for the tools it knows are
read-only, and every other tool asks. A server's tools are **discovered at runtime**:
openheron cannot know what `delete_repo` does, and under stdio the server is a **child
process** with the agent's own privileges (§7.2). ⇒ the cost of guessing wrong is
asymmetric — one extra click against a destructive call nobody saw. A server the user
trusts names its exceptions **once**:

```yaml
allows: [read_issue, list_issues]
```

★ **A tier change takes effect on the next turn.** `mcps/` is a **hot** partition, so a
tier is not special: the policy table is rebuilt with the snapshot and the gate reads the
new tier from the next turn on. The one thing the reload has to get right is the
**client**: the stdio child process is torn down and a new one started (§5.4).

★ **The filesystem tools get a file too** — `tools/filesystem.yaml` — so their tiers are
written next to them and the set can be narrowed. See `docs/config-design.md` §6.6.

### 7.4 What the run ledger says

★ An MCP tool call is an ordinary tool call, so it already reaches the transcript and
the event stream like any other. What the **ledger** adds is the server: a run's row
records which MCP servers the run touched, so a post-mortem can answer "which server was
this, and was it the reason the run failed" without reading the whole transcript.

★ The ledger stays a **run** ledger: it holds the run's state transitions, not one row
per call. The individual calls are in the transcript; the row says which servers were
involved.

## 8. The sandbox Kit

The Kit (`sandbox-kit-design.md`) packages the openheron process as a v3 workload. MCP
fits into it in two different ways, and they are not symmetric.

| Transport | Where the server runs | What the Kit needs |
|---|---|---|
| stdio | **inside** the sandbox, as a child of the openheron process | the server's binary or its package must be **in the workload image** — it is part of what the workload runs |
| http | **on the host** | a mixin network rule that lets the sandbox reach the host's port, and a host-side service to reach |

★ The mixin model decides this cleanly: a mixin is a **declaration** (a credential and
its network rule), and the workload is the **content**. An MCP server that runs inside
the sandbox is workload content; an MCP server that runs on the host and is reached over
http is a mixin's business — its network rule is the thing that has to change.

★ A third option exists and is deliberately **not** in this design: a mixin that
**installs** a host-side MCP server. That would make the mixin carry content, which is
the boundary `sandbox-kit-design.md` §2.3 draws.

### 8.1 The credential path

An MCP server that needs an authorization key is a different problem from one that
needs none, and the Kit's existing mechanism answers most of it.

#### The mechanism

The Kit already injects the **model** credential without letting it into the sandbox:
the mixin declares a `credential@1` with `proxyManaged: true`, the runtime puts the
sentinel `proxy-managed` into the container's variable, the host holds the real value,
and the **host-side proxy** substitutes it on the way out.

★ **The substitution happens on the outbound request, and the proxy does not know who
sent it.** It matches on the destination domain and the header name. An MCP server is
therefore just another client of that proxy: it sends the sentinel the way it would
send a token, and the proxy swaps in the real value.

```
the host holds the real key
  └─ sbx secret set-custom --host api.github.com --env GITHUB_TOKEN \
       --placeholder proxy-managed --value-stdin
        ↓
the container holds GITHUB_TOKEN=proxy-managed
        ↓
the MCP server reads it and sends Authorization: Bearer proxy-managed
        ↓
the host proxy substitutes the real key ──► api.github.com
```

★ **Nothing in `mcps/<server>.yaml` has to mention the key.** The sentinel is injected
into the **container**, and §5.5 passes the environment through — so the MCP server
finds the variable under the name it expects. The credential's variable name is the
**MCP server's** business, and openheron never learns it.

★ **The http transport takes the same path.** The `headers:` map goes through the same
expansion as everything else, so it can name an environment variable:

```yaml
headers:
  Authorization: Bearer ${MCP_TOKEN}
```

★ In a sandbox that variable holds the **sentinel**, and the host proxy substitutes the
real token on the way out — the same mechanism, the same mixin shape, and the same
`Authorization: Bearer <access_token>` form the mixin's `inject` rule declares. ⇒ There is
**one** credential path, and the transport does not change it.

#### What a mixin declares

| Capability | What it carries |
|---|---|
| `network-policy@1` | the upstream domains — the runtime checks that every inject domain appears literally in the allow list for the same phase, so the credential's domain has to be named |
| `credential@1` | the credential: `service`, `apiKey.name`, `proxyManaged: true`, and the `inject` rule (domain, header, format) |
| `lifecycle@1` | optional: an install hook that puts the server's binary in the sandbox |

★ **The mixin's granularity is the upstream, not the MCP server.** A credential has one
owner per (service, phase), and the environment is injected into the **container** — so
one credential declaration serves every server in that sandbox that reads the variable.
Two MCP servers that both talk to GitHub need **one** credential mixin and **one**
binding, not two.

★ **A mixin may install the MCP server into the sandbox.** The server's binary has to
be there, and the workload image is openheron's own — it cannot carry every MCP
server. The mixin declares the install as a `lifecycle@1` **install** hook (which
registry, which package), and its network rule allows that registry. The mixin still
carries no content: the runtime fetches the bytes, the mixin does not hold them.

★ This is not the option the paragraph above refuses. That one is a mixin that
installs a **host-side** server, which would make the mixin define a service. This
one installs a **guest-side** binary from a registry the mixin names — a declaration
of what to fetch, executed by the runtime.

#### When the mechanism does not reach

| Case | What happens |
|---|---|
| the key goes in a header | ★ the mechanism covers it — `inject` says which header and which format |
| the key goes in a URL query parameter, a cookie, or a non-HTTP protocol | ★ **not expressible**: `inject` is a header injection. The real key has to enter the sandbox (`sbx run --env …`), and then it is the agent's to protect |
| no mixin at all | the variable is simply not set, and the server's own error (a 401) reaches the model as the tool's result |

★ **The fallback is a real downgrade, and it should be said out loud.** A key inside
the VM is readable by the agent's own tools, and it is the user who chose that.

## 9. The core code architecture

### 9.1 The packages

| Package | What it holds | What it must not hold |
|---|---|---|
| `agentkit/spec/decl` | the **declarations**: `MCPServerCfg`, the namespace constants, and the agent's `MCPServers` field | any behaviour — it is the pure declaration layer (`AGENTS.md`'s dependency iron rules) |
| `agentkit/spec` | the **loader**: reads `mcps/<server>.yaml`, validates the file name and the fields, expands `${ENV}` / `@file:` | the MCP client, eino, `kb` — it may not import them |
| `agentkit/build/mcptools.go` | the **provider** and the **wrappers**: the naming, the tier mapping, the per-server mutex, the output cap | the loader's file walking — it receives the declarations |
| `agentkit/build/mcpmanager.go` | the **manager**: one client per server, the lazy start, the restart of a crashed child, the tool-list poll, the teardown on the snapshot swap | the naming and the tiers — those belong to the provider |
| `agentkit/build/tools.go` | the **registry**: `toolProvider`, `defaultToolRegistry`, the reserved names | the MCP specifics — it knows only the interface (§1.2) |
| `agentkit/build/policy.go` | the **tier lookup**: `Policy.TierOf`, `Table.Blocked` | which file a tool came from — it sees names only |

★ **The direction is one-way**: `spec` → `build`. The declaration layer never imports
the assembly layer, and the assembly layer never walks the config directory itself.

### 9.2 The types

| Type | Where | What it is |
|---|---|---|
| `decl.MCPServerCfg` | `agentkit/spec/decl/decl.go` | one server's declaration: the transport, the command, the environment, the tier keys, the whitelist, the two timeouts |
| `decl.MCPFiles` | `agentkit/spec/decl/decl.go` | the partition's holder: `map[string]*MCPServerCfg` keyed by the server name |
| `decl.Agent.MCPServers` | `agentkit/spec/decl/decl.go` | the agent's own list of servers: ★ a **pointer**, so the key absent means every configured server and `[]` means none — the same shape `Skills` already has |
| `spec.Config.MCPs` | `agentkit/spec/config.go` | where the loader puts them |
| `mcpToolProvider` | `agentkit/build/mcptools.go` | the `toolProvider` implementation (§1.2) |
| `mcpManager` | `agentkit/build/mcpmanager.go` | the owner of the clients: one per server, keyed by the server name, living across snapshots (§6.6) |
| `mcpTool` | `agentkit/build/mcptools.go` | one tool: the eino tool plus the server it belongs to |
| `serialTool` | `agentkit/build/mcptools.go` | the wrapper that serializes the calls of one stdio server (§6.5) |
| `build.Policy` | `agentkit/build/policy.go` | the assembled table: name → tier. **Unchanged in shape** — only where the entries come from changes (§7.3) |

### 9.3 Where each piece of the design lands

| The design | The code |
|---|---|
| the namespace (§3) | `mcpToolProvider.Provide` — the one place that composes `mcp__<server>__<tool>` |
| the skip-and-warn (§3.2) | the same function — the name is checked before the tool is kept |
| the file's own tier (§7.3) | the same function — the provider maps the file's names to the exposed ones |
| the serialization (§6.5) | `serialTool` — a wrapper, the same shape as `cappedTool` in `nettools.go` |
| the output cap (§6.4) | the same wrapper family — the component's `ToolCallResultHandler` |
| the client's life (§6.2, §6.6) | the manager — `Start` / `Initialize` / `GetTools`, the lazy start, the restart of a crashed child, and the teardown on the snapshot swap |
| the hot reload (§5.4) | `agentkit/spec/reload.go` — the partition is hot, and the snapshot swap rebuilds the tools |
| a changed tool list (§6.6) | the manager — the poll, and the generation bump that rebuilds the tools |
| the servers a run touched (§7.4) | `agentkit/run/runrow.go` — the run's row names them |
| the example and the editor's template (§5.6) | `agentkit/spec/write.go` (`spec.Template`) and `initcfg/templates/` |
| which servers an agent loads (§5.3) | `agentkit/spec/decl` (the field) → `agentkit/build/mcpmanager.go` (the start) |

★ **One function does three things**: the namespace, the skip-and-warn, and the tier
mapping all live in `mcpToolProvider.Provide`, because they all need the same two
inputs — the server's own tool list and the file that describes it. Splitting them
would mean passing the same two things around.

## 10. The change list

Nothing below is in the tree. This is the list a later implementation round would work
through.

### 10.1 The code

| # | File | Change |
|---|---|---|
| 1 | `go.mod` | add `github.com/cloudwego/eino-ext/components/tool/mcp` and `github.com/mark3labs/mcp-go` |
| 2 | `agentkit/spec/config.go` | `DirMCP = "mcps"` and a `Categories` entry, hot (`hotDirList` at `:77` picks it up) |
| 3 | `agentkit/spec/decl/decl.go` | the `MCPServerCfg` declaration struct, the `MCPFiles` holder, the namespace constants; `call_timeout`; ★ the agent's `MCPServers` field (§5.3) |
| 4 | `agentkit/spec/load.go` | the loader for `mcps/<server>.yaml`, with the file-name and field validation of §3.1 and §5.2 |
| 5 | `agentkit/spec/defaults.go` | `EnabledToolNames` gains the namespaced MCP tools; the default tier of a server (`ask`) |
| 6 | `agentkit/spec/write.go` | the scaffold for a new `mcps/` file |
| 7 | `agentkit/build/tools.go` | `mcpToolProvider`, registered at the placeholder line `:35` |
| 8 | `agentkit/build/policy.go` | the prefix matcher for `Blocked`, **and** the tier **assembly**: the providers contribute (exposed name → tier) from their own file's declarations (§7.3) |
| 9 | `api/v2_settings.go` | the editable section for `mcps/` |
| 10 | `agentkit/build/nettools.go` | the output cap, applied to the MCP batch |
| 11 | `agentkit/build/mcptools.go` | the provider's naming and tier mapping (§6.3, §7.3), the per-server mutex that serializes the stdio calls (§6.5), and the environment assembly for the child process (§5.5) |
| 12 | `agentkit/build/mcpmanager.go` | the manager: one client per server, the lazy start **of the servers that agent asked for** (§5.3), the restart of a crashed child, the tool-list poll that bumps the generation, and the teardown on the snapshot swap (§6.6) |
| 13 | `agentkit/run/runrow.go` | the run's row records which MCP servers the run touched (§7.4) |
| 14 | `initcfg/templates/` and `spec.Template` | the `mcps/example.yaml.disabled` example, and the editor's template for a new `mcps/` file — the editor's manifest is filled per category, so a new category gets its scaffold for free (§5.6) |

### 10.2 The tests

| Area | What it has to prove |
|---|---|
| Naming | a server offering `ls` yields `mcp__<server>__ls` and does not stop the agent from starting |
| Naming | a 64-character boundary, and the skip-and-warn path above it |
| Naming | two tools of one server that sanitize alike — the second is skipped, and the warning says so |
| Matching | a prefix rule hits the tools of one server and only those |
| Matching | `mcp*foo` and a lone `*` are load-time errors |
| Matching | an agent's `disabled_tools` removes the tool for that agent only |
| Provider | an unreachable server drops its tools, warns with the server's name, and leaves the other providers' tools alone |
| Lifecycle | a hot reload replaces the client, and the old stdio child is gone |
| Concurrency | two concurrent calls to one **stdio** server are serialized — the second waits for the first, and neither corrupts the other's request |
| Concurrency | two concurrent calls to one **http** server are not serialized |
| Concurrency | a call that exceeds `call_timeout` returns, and the server's lock is released |
| Environment | the child sees openheron's environment, with `env:` overriding the same names |
| Environment | an unset `${ENV}` in `env:` is deferred, not fatal |
| Credentials | a `credential@1` sentinel reaches the child through the inherited environment, with no field in the configuration naming it |
| Tier | an empty `allows:` covers the tools a server has **not offered yet** |
| Tier | a file's `allows:` names what runs without a nod; everything else asks |
| Manager | a crashed stdio child is started again on the next call |
| Manager | a changed tool list bumps the generation and rebuilds the tools, with no restart |
| Manager | the snapshot swap closes the old generation's clients and kills their children |
| Command | a `command` that is not on the PATH is a warning, and the workspace never resolves it |
| Ledger | a run's row names the MCP servers it touched |
| Editor | the config editor can create a file under `mcps/` |
| Tier | the file's `allows:` is written in the server's own vocabulary and reaches the gate under the composed name |
| Servers | an agent that does not list a server in `mcps:` does not get it, and the server is never started |
| Servers | an absent `mcps:` means every configured server, `[]` means none |

### 10.3 The documentation

| Document | What changes |
|---|---|
| `docs/developer-manual.md` | the "adding a tool" recipe gains the MCP provider; the "adding a partition" recipe gains the `mcps/` case |
| `docs/user-manual.md` | how to configure a server, and what the names in the interface look like |
| `docs/architecture.md` | the tool assembly path gains the MCP provider |

## 11. What is not in this design

| Not covered | Why |
|---|---|
| The MCP protocol, and the transports | they belong to `mcp-go`; openheron picks a client and configures it |
| A server registry with discovery, health checks and retries | one file per server is enough to start; a registry is a second round |
| MCP resources, prompts and sampling | the eino-ext component exposes **tools** only |
| Per-tool rate limits and quotas | `policy/limits.yaml` counts things; it has no tool-name dimension |
| A credential that is not a header | `inject` is a header injection; a key in a query parameter, a cookie or a non-HTTP protocol has to enter the sandbox instead (§8.1) |
| A mixin that installs a host-side MCP server | a mixin is a declaration; content belongs to the workload (`sandbox-kit-design.md` §2.3) |

★ Every entry in this document that describes code names a file and a line. When the
code moves, the line moves with it — that is the contract this repository keeps for
every other design document, and it is the reason the table in §10.1 is a list of
locations rather than a list of intentions.
