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

★ `policy/` carries **two** files: `permissions.yaml` (the tiers) and `limits.yaml`
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
| 3 | **A runtime tool set cannot be named in a static policy** — the policy is a table keyed by name, and the names are not known until the connection is up | `agentkit/build/policy.go:39` and `:94` — exact match only. Every MCP tool lands in the `allow` fallback, which is **not** a safe default for a tool that runs an arbitrary process | §4 |

★ Problem 3 is the reason a namespace (§3) and a prefix syntax (§4) are two halves of
one decision rather than two conveniences: **the namespace is what makes a prefix
meaningful**, because a prefix only separates cleanly if the prefix boundary is
unambiguous.

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

## 4. Prefix matching in the policy tables

The tiers and the per-agent narrowing accept a **prefix pattern** in addition to an
exact name.

| Entry | Matches |
|---|---|
| `execute` | exactly the tool named `execute` |
| `mcp__github__*` | every tool of the server named `github` |
| `mcp__*` | every MCP tool, from any server |

### 4.1 The syntax rules

| # | Rule | Reason |
|---|---|---|
| 1 | Only a **trailing** `*` is a prefix pattern; a `*` anywhere else is a **load-time hard error** | `mcp*foo` looks like a wildcard and is not one — a rule whose reach differs from what it reads as is a silent policy mismatch |
| 2 | A lone `*` is a **load-time hard error** | there is no `allow` column to fall back to (§4.2): `deny: ["*"]` is "no tools at all", `ask: ["*"]` is "click through everything". Neither is what "write a star" means |
| 3 | The part before `*` must not be empty | rule 2 |
| 4 | Within one tier, an exact entry and a prefix entry are **equivalent** — any match puts the tool in that tier | the tier is the only thing the gate acts on |

### 4.2 The precedence does not change

```
deny → ask → allow (the fallback)
```

★ unchanged. And the outer policy still beats the inner one: a `deny` cannot be
relaxed by an agent's `disabled_tools`. The prefix syntax only decides **whether an
entry matches a name** — it does not add a tier, and it does not reorder them.

★ **`policy/limits.yaml` is not part of this.** It holds counts, not tool names, so its
matching semantics stay as they are.

### 4.3 Where the prefix syntax applies

| Place | Where it is written | Meaning |
|---|---|---|
| `policy/permissions.yaml` → `ask` | `agentkit/build/policy.go:39` | these tools need a human decision |
| `policy/permissions.yaml` → `deny` | `agentkit/build/policy.go:39` | these tools may not run at all |
| an agent's `disabled_tools` | `agentkit/build/topology.go:64` | this agent does not get these tools |

★ The three places are the same decision written at three levels, so they share one
syntax. An agent that disables `mcp__github__*` disables the whole server for that
agent; it does not need to know the server's tool names.

### 4.4 What it costs

★ A prefix rule is a **blunt instrument**: `deny: ["mcp__*"]` removes every MCP tool
from the agent, and `ask: ["mcp__*"]` puts every one of them behind a click. That is
usually the intent, but it is one line, and it is easy to write by accident. A rule
that names a single server (`mcp__github__*`) is the one worth reaching for first.

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
| `env` | `map[string]string` | `{}` | the child's environment; secrets as `${ENV}` / `@file:`, never inline |
| `url` | `string` | — | http only, **required**: the server address |
| `headers` | `map[string]string` | `{}` | http only: sent on every request |
| `timeout` | `LenientDuration` | `30s` | the ceiling for connect + initialize + list tools |
| `tools` | `[]string` | `[]` → all | a whitelist of tool names to expose |

★ `timeout` is a **lenient** duration: a wrong value degrades to the default and is
recorded in `deferred`, exactly like `debounce` and `start_timeout`. A wrong timeout
changes *how long we wait*, not *what happens* — it must not keep the process from
starting.

★ `tools: []` means **all**, not **none**. An empty whitelist that meant "nothing" would
be indistinguishable from a missing key, and the file's own presence is the switch (§5.3).

### 5.3 The file's presence is the switch

There is no `enabled` field. A server is configured because its file exists; removing
the file removes the server. This is the rule `tools/` already follows, and it keeps the
switch in one place instead of splitting it between "the file is there" and "the flag
inside it says on".

★ The server name is the file name without `.yaml`, and it is the namespace segment
(§3.1). A file name that does not match `^[a-z0-9]+(_[a-z0-9]+)*$` is a **load-time
hard error** — we wrote it, so we can fix it.

### 5.4 The partition is hot

`mcps/` joins the hot partitions, next to `tools/` and `policy/`: changing a server's
command or timeout should take effect on the next turn, without a restart.

★ This means the client lifecycle has to survive a reload: when the snapshot is
replaced, the stdio child process is torn down and a new one is started. A hot
partition whose resources cannot be rebuilt is a partition that lies about being hot.

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

### 6.2 The client lifecycle

The component does not own it (§2.2), so the provider does:

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

## 7. The gate and the tiers

### 7.1 The tiers apply unchanged

A tool named `mcp__github__create_issue` is looked up exactly like any other:

```
Policy.TierOf("mcp__github__create_issue")
```

and lands in `ask` if `mcp__github__*` is in the `ask` list, or in `deny` if
`mcp__github__*` is in the `deny` list. The gate code does not change at all: it reads
the tier and acts (§1.3).

★ **This is the whole point of the namespace.** The gate, the tables, the API's
`EffectiveTools` view and the per-agent narrowing all work on names. A name that is
namespaced is a name they already understand — no new concept has to travel through the
policy layer, the API or the frontend.

### 7.2 The stdio privilege problem

★ Under stdio, the MCP server is a **child process of openheron**: it can write files,
reach the network and read anything openheron can read. The "only public networks"
rule that §6.6 of the config design sets for the HTTP tools **does not apply** — it
bounds what the *tool* may reach, not what the *server* may do.

This is the strongest argument for the namespace and the prefix syntax together: they
are how a class of external tools gets a tier **before** anyone has enumerated its
members. `ask: ["mcp__*"]` is one line that says "every tool from every MCP server
asks first".

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

## 9. The change list

Nothing below is in the tree. This is the list a later implementation round would work
through.

### 9.1 The code

| # | File | Change |
|---|---|---|
| 1 | `go.mod` | add `github.com/cloudwego/eino-ext/components/tool/mcp` and `github.com/mark3labs/mcp-go` |
| 2 | `agentkit/spec/config.go` | `DirMCP = "mcps"` and a `Categories` entry, hot (`hotDirList` at `:77` picks it up) |
| 3 | `agentkit/spec/decl/decl.go` | the `MCPServerCfg` declaration struct; the namespace constants |
| 4 | `agentkit/spec/load.go` | the loader for `mcps/<server>.yaml`, with the file-name and field validation of §3.1 and §5.2 |
| 5 | `agentkit/spec/defaults.go` | `EnabledToolNames` gains the namespaced MCP tools |
| 6 | `agentkit/spec/write.go` | the scaffold for a new `mcps/` file |
| 7 | `agentkit/build/tools.go` | `mcpToolProvider`, registered at the placeholder line `:35` |
| 8 | `agentkit/build/policy.go` | the prefix matcher for `TierOf` and `Blocked` |
| 9 | `api/v2_settings.go` | the editable section for `mcps/` |
| 10 | `agentkit/build/nettools.go` | the output cap, applied to the MCP batch |

### 9.2 The tests

| Area | What it has to prove |
|---|---|
| Naming | a server offering `ls` yields `mcp__<server>__ls` and does not stop the agent from starting |
| Naming | a 64-character boundary, and the skip-and-warn path above it |
| Naming | two tools of one server that sanitize alike — the second is skipped, and the warning says so |
| Matching | a prefix rule hits the tools of one server and only those |
| Matching | `mcp*foo` and a lone `*` are load-time errors |
| Matching | `deny` still beats `disabled_tools` |
| Provider | an unreachable server drops its tools, warns with the server's name, and leaves the other providers' tools alone |
| Lifecycle | a hot reload replaces the client, and the old stdio child is gone |

### 9.3 The documentation

| Document | What changes |
|---|---|
| `docs/developer-manual.md` | the "adding a tool" recipe gains the MCP provider; the "adding a partition" recipe gains the `mcps/` case |
| `docs/user-manual.md` | how to configure a server, and what the names in the interface look like |
| `docs/architecture.md` | the tool assembly path gains the MCP provider |

## 10. What is not in this design

| Not covered | Why |
|---|---|
| The MCP protocol, and the transports | they belong to `mcp-go`; openheron picks a client and configures it |
| A server registry with discovery, health checks and retries | one file per server is enough to start; a registry is a second round |
| MCP resources, prompts and sampling | the eino-ext component exposes **tools** only |
| Per-tool rate limits and quotas | `policy/limits.yaml` counts things; it has no tool-name dimension |
| A mixin that installs a host-side MCP server | a mixin is a declaration; content belongs to the workload (`sandbox-kit-design.md` §2.3) |

★ Every entry in this document that describes code names a file and a line. When the
code moves, the line moves with it — that is the contract this repository keeps for
every other design document, and it is the reason the table in §9.1 is a list of
locations rather than a list of intentions.
