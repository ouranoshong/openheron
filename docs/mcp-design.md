# MCP Integration Design — a runtime tool set, a fixed policy table

> Status: **implemented** — the feature this document designed is in the tree. The
> dependencies `github.com/cloudwego/eino-ext/components/tool/mcp` (v0.0.9) and
> `github.com/mark3labs/mcp-go` (v0.43.0) are in `go.mod`; the `mcps/` partition, the
> MCP provider, the namespace, the per-file tiers, the manager and the `/mcps` endpoint
> all landed. §1 describes the code as it is; §2 onwards describes the same feature as
> it was designed, and every claim still names a file and a line. ★ Two halves are
> **still proposals**: the **sandbox Kit** (§8) does not package MCP servers yet, and
> the **global `deny`** is still in the tree (§4.2) — the MCP feature did not remove
> it.
>
> Scope: how an MCP (Model Context Protocol) server becomes a set of tools an openheron
> agent can call, how those tools are named, and how the policy layer decides whether
> they may run. Not in scope: the MCP protocol itself, the transport implementations
> (they belong to `mcp-go`), and the sandbox packaging (see `sandbox-kit-design.md` §2.3).

## 1. The code as it is

### 1.1 Where the tools come from

```
mcps/*.yaml → spec.loadMCP → decl.MCPServerCfg
  → mcpManager.Tools (one client per server, one listing)
  → mcpToolProvider.Provide → mcpPlan (the namespace, the skips, the tiers)
  → snapshot.buildHeavy → heavyDeps.tools
  → applyToolConfig → deep.Config.ToolsConfig.Tools
  → deep.New → the eino agent
```

| Step | Where |
|---|---|
| Load one `mcps/` file | `agentkit/spec/load.go:256` `loadMCP` |
| The declaration struct | `agentkit/spec/decl/decl.go:454` `MCPServerCfg` |
| Read one server's tools | `agentkit/build/mcpmanager.go:89` `mcpManager.Tools` |
| Build every provider's batch | `agentkit/build/tools.go:55` `toolRegistry.Build` |
| Materialize it, once per snapshot | `agentkit/build/snapshot.go:47` `buildHeavy` |
| Hand it to eino | `agentkit/build/assemble.go:31` `applyToolConfig` |

★ `buildHeavy` runs under a `sync.Once` (`agentkit/build/snapshot.go:40-45`). The tool
set is therefore **fixed for the lifetime of one snapshot** — it is rebuilt when the
snapshot generation changes, not when a tool's behaviour changes.

★ **The MCP provider is the reason the generation can move without a configuration
change.** A server may add a tool on its own, and the snapshot that froze the list
would need a restart to see it. ⇒ the builder asks the manager before it assembles the
turn's agent, and a changed listing moves the generation through the same place a
configuration change moves it (`agentkit/build/builder.go:67`, `:105`).

### 1.2 The provider contract

`agentkit/build/tools.go:19` is the whole extension point:

```go
type toolProvider interface {
	ID() string
	Optional() bool
	Provide(ctx context.Context, env toolEnv) ([]tool.BaseTool, error)
}
```

The registry is `defaultToolRegistry` (`agentkit/build/tools.go:35`), and it carries
the MCP provider:

```go
		mcpToolProvider{mgr: mgr},
		// projectToolsProvider{}, ...
```

★ The MCP provider is the **only** one that needs a manager: an MCP client outlives
the batch the provider returns, so the manager — not the provider — owns it
(`agentkit/build/tools.go:31-34`, `agentkit/build/mcpmanager.go:42`). ★ The other
providers mount a tool set and are done; a server is a **connection**, and a connection
has a life.

### 1.3 The names are the policy

The tool name is the key of every table that decides what a tool may do:

| Table | Where | What it decides |
|---|---|---|
| `Policy.TierOf` | `agentkit/build/policy.go:49` | `allow` / `ask` / `deny` |
| `Table.Blocked` | `agentkit/build/policy.go:161` | denied (outer) / disabled (inner) |
| `Table.Filter` | `agentkit/build/policy.go:184` | drops the tools from `runCtx.Tools` |
| `Builder.EffectiveTools` | `agentkit/build/introspect.go:49` | what the API reports |

★ **Every one of those lookups is a name lookup, and the name is the whole contract.**
`TierOf` is `slices.Contains` plus a map lookup; `Blocked` is a map lookup plus two
prefix scans (`agentkit/build/policy.go:168`, `:176`). ★ The two prefix scans are the
ones MCP brought: the agent's `mcps:` list denies a **namespace prefix**, and an
agent's `disabled_tools` accepts a **trailing `*` pattern** (§4). Everything else is an
exact match — and the tier of an MCP tool is answerable **before the server is even
up** (§7.3).

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
| `tools/` | `agentkit/spec/config.go:63` | one file per tool, **name is a whitelist** (`agentkit/spec/load.go:165`), hot |
| `policy/` | `agentkit/spec/config.go:53` | `ask` / `deny` / `limits`, hot |
| `agents/` | `agentkit/spec/config.go:48` | frontmatter, incl. `disabled_tools` and `mcps:`, hot |
| `mcps/` | `agentkit/spec/config.go:72` | one file per server, **the file's presence is the definition**, hot |

★ `policy/` carries **two** files today: `permissions.yaml` (the tiers) and `limits.yaml`
(the counts). `limits.yaml` holds no tool names — the counts are per agent and per
category — so it plays no part in this design.

★ **`mcps/` is the fourth partition, and it is hot.** `agentkit/spec/config.go:72`
registers it with `Editable: true` and `hot: true`: the configuration editor can write
it, the reloader watches it, and the loader puts the servers into `Config.MCPs`
(`agentkit/spec/config.go:119`). ★ A hot partition whose resources cannot be rebuilt is
a partition that lies about being hot — so the client's life is tied to the
declaration's fingerprint (`agentkit/build/mcpmanager.go:330`).

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
| No client lifecycle — no spawn, no `Start`, no `Initialize`, no `Close` | openheron owns the connection: the manager creates it, initializes it, keeps it alive, and tears it down when the process goes away (`agentkit/build/mcpmanager.go:395`) |
| No transport choice | stdio / SSE / streamable HTTP are `mcp-go`'s `client` constructors; openheron picks one (`agentkit/build/mcpmanager.go:418`, `:472`) |
| No configuration format | the schema is §5 — `mcps/<server>.yaml`, one file per server (`agentkit/spec/load.go:256`) |
| No streaming | the wrapper implements `Info` and `InvokableRun` only, so the tool is a `tool.InvokableTool`, never a `tool.StreamableTool`. The whole result arrives as one JSON blob |

★ **The component is the adapter, not the feature.** openheron brings the lifecycle,
the naming, the tiers and the policy; the component brings the protocol. The provider
calls exactly one function of it (`agentkit/build/mcptools.go:255`), and everything
around that call is openheron's own code.

★ The dependency versions are compatible. The component's own `go.mod` pins
`github.com/cloudwego/eino v0.6.0`, but the APIs it uses still exist in v0.9.22
(`schema.NewParamsOneOfByJSONSchema`, `eino@v0.9.22/schema/tool.go:297`). Verified by
compiling `components/tool/mcp v0.0.9` against `eino v0.9.22` with
`github.com/mark3labs/mcp-go v0.43.0`.

### 2.3 The three problems that decide this design

| # | Problem | Where it bites | Resolved by |
|---|---|---|---|
| 1 | **A server's tool name can collide with a reserved name** — an MCP server that offers `ls` or `grep` | `agentkit/build/tools.go:99` — `validateBatch` returns an error, `Build` propagates it (`agentkit/build/tools.go:73`), and the **whole agent fails to start**. Not "this tool is dropped" — the process is down | §3 — `mcp__<server>__ls` |
| 2 | **A server's tool name can be illegal for the provider** — server-prefixed names such as `github.create_issue` are common | the model provider's function-name character set does not accept `.`; the call fails at the model, not at load time | §3 — `sanitizeMCPToolName` (`agentkit/build/mcptools.go:150`) |
| 3 | **A runtime tool set cannot be named in a static policy** — the policy is a table keyed by name, and the names are not known until the connection is up | `agentkit/build/policy.go:49` and `:161` — a name lookup only. Every MCP tool would land in the `allow` fallback, which is **not** a safe default for a tool that runs an arbitrary process | §4 and §7.3 — the namespace's default is `ask` (`agentkit/build/policy.go:55`) |

★ **All three are closed in the tree.** Problem 1 by the namespace, problem 2 by the
sanitizer, problem 3 by the per-file tier: an MCP tool is `allow` only when its own
server's file names it in `allows:`, and `ask` otherwise.

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
| `server` | **we do** — the `<server>.yaml` file name | must match `^[a-z0-9]+(_[a-z0-9]+)*$`; a violation is a **load-time hard error** (`agentkit/spec/load.go:258`) |
| `tool` | **the server does** | characters outside `[A-Za-z0-9_-]` are replaced with `_`; case is preserved (`agentkit/build/mcptools.go:150`) |

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
symptom this design refuses to produce. ⇒ the provider logs the three
(`agentkit/build/mcptools.go:235`), and the panel shows the tool as `skipped` with its
reason (`agentkit/build/mcpstatus.go:145`).

### 3.3 The namespace closes problem 1 as well

A server that offers `ls` now offers `mcp__<server>__ls`, which does not collide with
`reservedToolNames` (`agentkit/build/tools.go:46`). The reverse follows, and it is a
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

★ **The global `deny` is still in the tree.** The configuration design proposes to drop
it (§6.3 of the configuration design), and this design was written against that
proposal — but the MCP feature did **not** take the global list with it:
`Policy.Deny` is still there (`agentkit/build/policy.go:51`) and it still matches
**exactly**, so a name in the global `deny` stays denied even when the server's own
file allows it (`agentkit/build/mcptools_test.go:166` is the test that says so). ★
"Not exposed" is therefore said in **three** places now: the global `deny`, a file's
**whitelist**, which picks the ones to offer out of what the server has, and an agent's
**`disabled_tools`**, which says what that agent does not get. The narrowing is the
statement that is about a **namespace** rather than about a tool — "this agent gets no
MCP tools" — and a prefix is what names a namespace.

### 4.1 The syntax rules

| # | Rule | Reason |
|---|---|---|
| 1 | Only a **trailing** `*` is a prefix pattern; a `*` anywhere else is a **load-time hard error** | `mcp*foo` looks like a wildcard and is not one — a rule whose reach differs from what it reads as is a silent mismatch |
| 2 | A lone `*` is a **load-time hard error** | `disabled_tools: ["*"]` would take the agent's own tools away as well; that is a decision to write out, not a wildcard |
| 3 | The part before `*` must not be empty | rule 2 |
| 4 | An exact entry and a prefix entry are **equivalent** — any match removes the tool for that agent | there is no tier to choose here: the tool is simply not there for that agent |

### 4.2 The prefix syntax adds no tier

★ The tiers live in the tools' own files now (§7.3), and an MCP tool gets two of
them — the `deny` tier did **not** go away with the global list (§4.2):

```
allow (if the file's allows list names it) → ask (everything else)
```

★ A name in the global `deny` is still `deny`: the global list is the outer boundary,
and it is matched first (`agentkit/build/policy.go:51`). The two tiers above are what
an MCP tool gets when the global list has nothing to say about it.

★ The prefix syntax only decides **whether an entry matches a name** — it adds no tier,
and it reorders none. ★ **And the outer boundary is still there**: the global `deny` is
matched first (`agentkit/build/policy.go:165`), so it cannot be relaxed by an agent's
`disabled_tools`. ★ The configuration design proposes to remove that global list
(§6.3 of the configuration design), and the MCP feature was written against that
proposal — but the removal did not happen, and the tree keeps the guarantee.

★ **`policy/limits.yaml` is not part of this.** It holds counts, not tool names, so its
matching semantics stay as they are.

### 4.3 Where the prefix syntax applies

| Place | Where it is written | Meaning |
|---|---|---|
| an agent's `disabled_tools` | `agentkit/build/topology.go:70` | this agent does not get these tools |

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
(`agentkit/spec/load.go:165` whitelists the file names), and the entity in MCP is the
**server**: one server has one command, one argument list, one environment, one
transport and one lifecycle. Putting N servers in one file means a nested list under a
partition whose rule is "the file content is the configuration, with no wrapper key" —
and when the file is replaced wholesale, there is no way to say which server changed.
★ `mcps/` follows the same rule, one file per server, and the loader keys the partition
by the file name (`agentkit/spec/load.go:256`).

The rule from §2 of the config design — *one entity, one file* — applies to the server,
not to the tool.

### 5.2 The fields

| Field | Type | Default | Meaning |
|---|---|---|---|
| `transport` | `string` | `stdio` | `stdio` or `http`; an **unknown value is a hard error** — no silent fallback to stdio. ★ `http` means **streamable HTTP**; SSE is not supported (§5.7) |
| `command` | `string` | — | stdio only, **required**: the executable to run |
| `args` | `[]string` | `[]` | stdio only: its arguments |
| `env` | `map[string]string` | `{}` | stdio only: variables **layered over** the inherited environment (§5.5); values are expanded like every other value. ★ an http server with `env:` is a **load-time hard error** (§5.7) |
| `url` | `string` | — | http only, **required**: the server address |
| `headers` | `map[string]string` | `{}` | http only: sent on every request |
| `timeout` | `MCPDuration` | `30s` | the ceiling for connect + initialize + list tools |
| `call_timeout` | `MCPDuration` | `60s` | the ceiling for **one tool call** (§6.5) |
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

★ **There is no `deny` in a file.** "Not exposed" is said in three places: the global
`deny` (§4.2), this file's **whitelist** — a tool that is not in `tools:` is not
offered — and an agent's **`disabled_tools`**.

★ `timeout` is a **lenient** duration: a wrong value degrades to the default and is
recorded in `deferred`, exactly like `debounce` and `start_timeout`. A wrong timeout
changes *how long we wait*, not *what happens* — it must not keep the process from
starting.

★ **The type is `decl.MCPDuration`, not the package-level `LenientDuration` this
design named.** The difference is the **source**: the package-level list every other
lenient duration uses carries no file and no key, so it cannot say **which** value was
wrong. `MCPDuration` keeps the raw text and whether the key was there at all, so the
deferred entry can name the file and the key (`agentkit/spec/decl/decl.go:421`,
`agentkit/spec/load.go:353`).

★ `tools: []` means **all**, not **none**. An empty whitelist that meant "nothing" would
be indistinguishable from a missing key — and it is a **filter** on what the server
offers (§5.3), not a switch.

★ **`http` is streamable HTTP, and only that.** `mcp-go` ships two HTTP clients —
`client.NewStreamableHttpClient` and `client.NewSSEMCPClient` — and one `transport`
value cannot pick between them. ⇒ `http` means the **streamable** one, the transport
the MCP specification now defines; openheron does not speak SSE. ★ The alternative (a
third `transport` value) would put a legacy transport into the configuration of every
user who never wants it, and the second value would have to be *the* value for the
people who do.

★ **`env:` has no meaning on an http server, so it is an error there.** An http server
is a remote process, and this process's environment is not its environment. ⇒ an http
server that declares `env:` is a **load-time hard error** (§5.7), not a key that is
quietly ignored. ★ A key that does nothing is worse than a refusal: the user would
believe the server runs with that variable set.

★ **Two of the fields are the component's own.** `tools:` is the component's
`ToolNameList` — the server's **own** names to keep out of the ones it has — and
`headers:` is its `CustomHeaders` (§2.2). ★ Both are written in the server's own
vocabulary, and the component takes them in that same vocabulary, so nothing has to be
translated.

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

★ **Where that narrowing happens is not free**, because the tool set is **one set for
the whole snapshot** (§1.1). The answer is in §11.3: the agent's **table**, where
`disabled_tools` already narrows the tools.

★ **A sub-agent has a list of its own, like its own `disabled_tools`.**
`decl.SubAgentSpec` carries `DisabledTools` and `Skills` of its own
(`agentkit/spec/decl/decl.go:619`), so it carries an `mcps:` list of its own too
(`agentkit/spec/decl/decl.go:629`). ⇒ the list is read from **the agent that is
running** — the root, or the sub-agent the turn is in — and not from the root's list.

★ **A name that does not exist is a load-time hard error.** An agent that lists
`githbu` would silently get no MCP server at all, and a tool that is missing because of
a typo looks exactly like a tool that is missing because the server is down. ⇒ the
loader refuses the name (§5.7, `agentkit/spec/load.go:499`).

★ The server name is the file name without `.yaml`, and it is the namespace segment
(§3.1). A file name that does not match `^[a-z0-9]+(_[a-z0-9]+)*$` is a **load-time
hard error** — we wrote it, so we can fix it. ★ The configuration editor refuses the
same names (`agentkit/spec/write.go:107`), so a file written through the editor cannot
break the rule either.

### 5.4 The partition is hot

`mcps/` joins the hot partitions, next to `tools/` and `policy/`: changing a server's
command or timeout should take effect on the next turn, without a restart. ★ The hot
directories are collected from the categories themselves
(`agentkit/spec/reload.go:21`), and `mcps/` is one of them because it says so
(`agentkit/spec/config.go:72`).

★ This means the client lifecycle has to survive a reload: when the snapshot is
replaced, the stdio child process is torn down and a new one is started. A hot
partition whose resources cannot be rebuilt is a partition that lies about being hot.
⇒ the client is rebuilt when the **declaration** changes, not when the snapshot does:
the fingerprint is the declaration's own (`agentkit/build/mcpmanager.go:330`), and the
start replaces the old client (`agentkit/build/mcpmanager.go:229`).

### 5.5 The child's environment

★ **The child's environment is openheron's own environment, overlaid with the `env:`
map.** Same name: the configuration wins.

★ **The environment is stdio's business.** An http server has no child process, so
`env:` is not a field it may declare at all (§5.2, §5.7).

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
with a full environment. ⇒ the child gets `os.Environ()` and the file's entries are
appended (`agentkit/build/mcpmanager.go:458`).

★ The `PATH` in that environment is the inherited one, unchanged: the command is
resolved in the sandbox's PATH and never against the project root
(`agentkit/build/mcpmanager.go:418`).

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
corpus_dir: ""
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
| `openheron init` | ★ an **example** — and an example cannot be a live `.yaml`: the file **defines** the server (§5.3), so a live one would make every agent that does not narrow its list try to start a server that is not installed. ⇒ `mcps/example.yaml.disabled`: the shape is right there, the name says it is not a configuration yet, and renaming it is the act of defining it (`initcfg/initcfg.go:193`) |
| the config editor | ★ the same template, through its "new file" dialog — the editor can write `mcps/` (`agentkit/spec/write.go:189`), and it refuses a file name that is not a readable server name (`agentkit/spec/write.go:107`) |

### 5.7 What fails loudly

★ **Our own configuration fails at load time; somebody else's content is dropped with a
notice.** That is the line §3.2 draws, and it is the line this table follows.

| Case | When | Why |
|---|---|---|
| an unknown `transport` | load time | no silent fallback to stdio — a typo must not run a different server |
| `command` missing on a stdio server | load time | there is nothing to run |
| `url` missing on an http server | load time | there is nothing to talk to |
| `env:` on an http server | load time | an http server has no child to hand an environment to (§5.2) |
| an agent's `mcps:` naming a server that does not exist | load time | a typo would silently narrow the agent to nothing (§5.3) |
| `tools:` naming a tool the server does not have | runtime | the server's list is only known once it is up — the provider warns (§6.1) and offers the rest |

★ **A load-time hard error stops the process**, and that is the point: these are
mistakes in a file somebody wrote by hand, and the next start has to say so. ★ The
runtime row is the exception because the server's tool list is not ours — the same line
§3.2 draws for a tool whose name cannot be composed.

## 6. The assembly path

### 6.1 The provider

The provider follows the contract of §1.2 and registers in the registry
(`agentkit/build/tools.go:41`):

| Piece | What it does |
|---|---|
| `mcpToolProvider.ID` | the provider name in logs and errors |
| `mcpToolProvider.Optional` | **true** — an unreachable server must not keep the agent down |
| `mcpToolProvider.Provide` | ask the manager for each configured server, list its tools, namespace and sanitize the names, return the batch |

★ **The provider walks the servers some agent asked for**, not the whole partition:
`Provide` iterates `Config.MCPServersInUse()` (`agentkit/build/mcptools.go:209`), and
that list is the **union** over the agents and the sub-agents (§5.3, §11.3). ⇒ a
server no agent lists is never started, and never becomes a child process.

★ `Optional() == true` has a price, and `developer-manual.md` §3.1 step 3 says so
plainly: the provider's `Warn` is the **only** notice — the tools do not enter the
table, and the user sees "this feature seems to be missing". For an MCP server that is
exactly the wrong symptom to leave unexplained, so the warning must name the server,
the transport and the error.

★ **An unreachable server is a warning, not a row.** The **configuration** views do
**not** expose the servers that are configured but unreachable. The user learns from the
warning and from the tools that are missing; an endpoint that listed "the server you
configured is not there" as part of the configuration would be a second truth to keep in
sync with the first, and it would answer a question the warning already answers.

★ **The `/mcps` view is the exception, and it is not a second truth** (§11.2): it is a
**projection of the manager's live state**, and it exists because the user asked for it.
The warning and the view read the same state — the manager's client — so they cannot
disagree about a server.

★ **A name the file asks for and the server does not have is a warning too.** The
file's `tools:` lists the server's **own** names, and the server's list is only known
once it is up (§5.7). ⇒ the provider warns — it names the server, the name the file
asked for, and what the server actually offers — and it offers the rest
(`agentkit/build/mcptools.go:240`). ★ Silently offering less than the file asked for is
the same "a feature seems to be missing" symptom the design refuses.

### 6.2 The client lifecycle

The component does not own it (§2.2), so **the manager** does (§6.6) — the provider asks
it for the tools and gets them:

| Step | stdio | http |
|---|---|---|
| create | `transport.NewStdio` + `client.NewClient` | `transport.NewStreamableHTTP` + `client.NewClient` |
| start | the child process is spawned | the HTTP session opens |
| initialize | `Initialize` with the protocol version and the client info | the same |
| list tools | `ListTools` | the same |
| teardown | the child process is killed when the declaration changes, or when the process goes away | the session closes |

★ The manager uses the **transport** constructors and `client.NewClient` on top of
them, not `client.NewStdioMCPClient` / `client.NewStreamableHttpClient`: the lower
level is where the child's environment and the arguments are set
(`agentkit/build/mcpmanager.go:418`, `:472`), and the initialization is one function
for both transports (`agentkit/build/mcpmanager.go:492`).

★ The initialization result is **discardable**: the provider only needs the tool list.
A failure to initialize is the same failure as an unreachable server — the server does
not contribute tools, and it says so.

★ **The http column is one client, not two.** `mcp-go` ships `client.NewSSEMCPClient`
as well, and openheron does not call it: §5.2 says `http` means **streamable HTTP**,
and a transport that the configuration cannot ask for is a transport that would have to
be maintained without ever being used.

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
never mentions (§2.2, `agentkit/build/mcptools.go:371`).

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
server would queue behind it. ⇒ `call_timeout` (`MCPDuration`, default `60s`) bounds
one tool call. It is lenient, like every other duration that only decides *how long we
wait*.

### 6.6 The mcps manager

★ **One manager owns the clients**, not the provider. The provider answers "what tools
does this snapshot have"; the manager is what a client outlives.

| Responsibility | What it means |
|---|---|
| **one client per server** | a server is one connection, and for stdio one child process |
| **start only what an agent asked for** | the manager starts the servers in the `mcps:` lists of the agents (§5.3) — a server no agent lists is never started, and never becomes a child process. ★ the list is the **union**, because the snapshot's tool set is shared by every agent (§11.3) |
| **start lazily** | a server is started when a turn first needs it, not at boot |
| **start once** | ★ the start is **single-flight**: two callers asking for the same server get the **one** client (§6.8) |
| **restart a crashed child** | a stdio server that dies is started again on the next call — a crash is not a configuration error, and a dead server that stays dead is the worst outcome |
| **notice a changed tool list** | the manager re-lists the tools; when the list changed, the **generation is bumped** and the tools are rebuilt, exactly like a configuration reload (§6.7) |
| **replace a server that changed** | a declaration that no longer matches the running client's fingerprint closes the old client and starts a new one (`agentkit/build/mcpmanager.go:229`) |
| **tear down when the process goes away** | the manager closes every client on shutdown (`agentkit/build/mcpmanager.go:150`, `main.go:384`) |

★ **The tool list is the manager's business, not the snapshot's.** A server's tools are
discovered at runtime, so a snapshot that froze them would need a restart to see a tool
the server added yesterday. ⇒ the manager re-lists, and a change re-enters the same
path a configuration change takes.

★ **The client outlives the snapshot — and the implementation keeps it that way.** This
design put the teardown on the snapshot swap; the tree does not, and the reason is the
manager itself: it is registered once and it owns the clients across snapshots, so a
configuration change that does not touch a server leaves that server's child running.
★ What replaces the client is the **declaration** — the fingerprint is the
declaration's own (`agentkit/build/mcpmanager.go:330`) — and the only other teardown
is the process going away.

★ **The re-list happens before the turn's agent is assembled, not inside the batch
build.** The batch is built once per generation, and the generation moves only when a
list changes — so a re-list *inside* the build would never run. ⇒ the builder asks the
manager first, and a change moves the generation in `main.go`'s own `agentConfig` — the
same place a configuration change moves it (`main.go:511` `bump`, `main.go:498` `set`) —
so the snapshot is rebuilt before the agent is assembled.

★ **The builder needs a way to move the generation.** `ConfigManager` is read-only
(`agentkit/build/manager.go:12` — it has `Current()` and nothing else), so the builder
grows a **bump** callback beside it (`agentkit/build/builder.go:67`), and `main.go`
wires that to the same `agentConfig.bump` the reloader uses (`main.go:247`,
`main.go:511`). ★ A second way to move the generation would be a second way to be
wrong about it.

★ **The interval is 30 s, and it is not a configuration key.** The manager re-lists a
server when a turn asks for it and the previous listing is older than **30 s** — so an
idle openheron re-lists nothing, and no turn runs on a list that is more than one
interval old. ★ A knob nobody turns is a knob nobody tests, and a server's tool list
changes on the scale of days.

★ **A background ticker was the other option, and it was rejected.** It would keep
polling servers nobody is using, for ever, and it would need a goroutine with a
lifecycle of its own — for a list that changes on the scale of days.

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
  per server → transport.NewStdio / transport.NewStreamableHTTP
            → tr.Start(ctx) → cli.Initialize(ctx) → mcpp.GetTools(ctx, &mcpp.Config{Cli: cli})
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
snapshot is replaced, a server whose declaration changed gets a new client, and the
tier table is rebuilt with the exposed names. The gate reads the new tier from the
next turn on — no restart, and no window where a tool keeps its old tier.

★ **And a server that changed its own tool list takes the same path.** The manager polls
the list (§6.6); when it changed, the generation is bumped and the tools are rebuilt —
the same road a configuration change takes, and the same absence of a restart.

### 6.8 One start, one client

★ **Two callers ask for the same server.** The provider asks when it builds the batch
(§6.1), and the panel asks when the user opens it (§11.2). Both are ordinary requests
that can arrive at the same moment — a turn starting while the `/mcps` dialog is open.

⇒ **The start is single-flight per server.** The manager keeps one entry per server
name; the first caller starts the client, and every other caller waits for that same
start and gets the same client. ★ This is the invariant §6.5 protects for the calls —
one server, one client — and the failure it prevents is the same kind: two writers on
one connection.

★ **Two child processes for one server would be worse than a slow start.** The second
process would hold a second pipe to a server the user configured once, and the two
could report different tool lists — so the tools the model sees would depend on which
process answered.

★ **The wait is bounded by the deadline the start already has** (§5.2): connect,
initialize and list tools are one operation with one deadline. A caller that waits
longer than that gets the failure the start reports — the same failure it would have
got by starting the server itself.

## 7. The gate and the tiers

### 7.1 The tiers apply unchanged

A tool named `mcp__github__create_issue` is looked up exactly like any other:

```
Policy.TierOf("mcp__github__create_issue")
```

and lands in `ask` unless the server's file names it in `allows:` (§7.3) — which is
the same rule every tool follows. The gate code does not change at all: it reads the
tier and acts (§1.3). ★ The namespace's default is `ask`, and it is a **fallthrough
branch** of the tier lookup, not a special case: `Policy.TierOf` answers `ask` for any
name under `mcp__` that the files do not allow
(`agentkit/build/policy.go:55`).

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
the whitelist's job. ★ And the **global `deny` is still there** — this design was
written against the configuration design's proposal to drop it (§6.3 of the
configuration design), and the MCP feature did not take it along (§4.2). The three
levels do not duplicate each other: the global list says **never**, the file says
**which of the server's tools to offer**, the agent says **what this agent does not
get**.

★ **The assembled policy keeps its shape.** `build.Policy` and `TierOf`
(`agentkit/build/policy.go:49`) still take a name and answer a tier; what changed is
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

★ **The tier table is assembled from the configuration, not from the server.** The
`allows:` list is in the file, and the exposed name of a name in it is composed from
the file's own vocabulary — so the entry `mcp__github__read_issue → allow` can be
assembled **before the server is even up** (`agentkit/build/mcptools.go:172`). ⇒ the
tier of a tool is answerable at any time, including while the server is unreachable
(§6.1), and the gate's lookup never waits for a connection.

★ **And the default covers the names the file does not name.** A tool that is in
neither the file's `allows:` nor anywhere else is `ask` — so a tool the server adds
tomorrow asks on its first day, and the configuration does not have to be touched. ★
This is the same rule an empty `allows:` states in one line (§4); a non-empty one is
that same rule with a few exceptions carved out.

★ **A tier change takes effect on the next turn.** `mcps/` is a **hot** partition, so a
tier is not special: the policy table is rebuilt with the snapshot and the gate reads the
new tier from the next turn on. The one thing the reload has to get right is the
**client**: a server whose declaration changed gets a new client (§5.4).

★ **The filesystem tools get a file too** — `tools/filesystem.yaml` — so their tiers are
written next to them and the set can be narrowed. See `docs/config-design.md` §6.6.

### 7.4 What the run ledger says

★ An MCP tool call is an ordinary tool call, so it already reaches the transcript and
the event stream like any other. What the **ledger** adds is the server: a run's row
records which MCP servers the run touched (`storage/runledger.go:168`), so a
post-mortem can answer "which server was this, and was it the reason the run failed"
without reading the whole transcript. ★ The names are read out of the namespaced tool
names the run made (`agentkit/run/runrow.go:84`), so the row does not need the tools
to be tracked separately.

★ The ledger stays a **run** ledger: it holds the run's state transitions, not one row
per call. The individual calls are in the transcript; the row says which servers were
involved.

## 8. The sandbox Kit

> Status: **a proposal.** The Kit does not package MCP servers yet — nothing in §8 is in
> the tree. The MCP feature itself is implemented (§1 to §7, §9 to §11); what is missing
> is the packaging half, and the reasoning below is why that half should look the way
> it describes.

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

★ **The openheron half of this path is implemented; the Kit half is not.** The child
process inherits this process's environment (§5.5, `agentkit/build/mcpmanager.go:458`),
so a sentinel the runtime put into the container's variable reaches the MCP server with
no field in the configuration naming it — that half is in the tree. What is not in the
tree is the Kit that puts the sentinel there: the packaging below is still a proposal.

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
| `agentkit/build/mcptools.go` | the **provider** and the **wrappers**: the plan (`mcpPlan`), the per-server mutex, the output cap | the loader's file walking — it receives the declarations |
| `agentkit/build/mcpmanager.go` | the **manager**: one client per server, the single-flight start (§6.8), the lazy start, the restart of a crashed child, the tool-list re-list, the teardown when the server changes and when the process goes away | the naming and the tiers — those belong to the provider |
| `agentkit/build/tools.go` | the **registry**: `toolProvider`, `defaultToolRegistry`, the reserved names | the MCP specifics — it knows only the interface (§1.2) |
| `agentkit/build/policy.go` | the **tier lookup**: `Policy.TierOf`, `Table.Blocked` | which file a tool came from — it sees names only |
| `agentkit/build/mcpstatus.go` | the **projection**: the manager's state, the plan, the boundary's verdict — one row per server (§11.2) | any decision of its own — it reads, it does not evaluate |

★ **The direction is one-way**: `spec` → `build`. The declaration layer never imports
the assembly layer, and the assembly layer never walks the config directory itself.

★ **The plan is a free function, not a method.** The naming and the tier mapping are
needed by **two** callers — the provider, which turns them into eino tools, and the
projection, which reports them (§11.2). ⇒ they live in `mcpPlan`, a function of the
file's declaration and the server's own tool list, and both callers call it. ★ A method
on the provider would make the projection reach for the provider instance, and a second
copy of the mapping would be a second truth about a tool's name and a tool's tier.

### 9.2 The types

| Type | Where | What it is |
|---|---|---|
| `decl.MCPServerCfg` | `agentkit/spec/decl/decl.go:454` | one server's declaration: the transport, the command, the environment, the tier keys, the whitelist, the two timeouts |
| `decl.MCPFiles` | `agentkit/spec/decl/decl.go:502` | the partition's holder: `map[string]*MCPServerCfg` keyed by the server name |
| `decl.Agent.MCPServers` | `agentkit/spec/decl/decl.go:114` | the agent's own list of servers: ★ a **pointer**, so the key absent means every configured server and `[]` means none — the same shape `Skills` already has |
| `decl.SubAgentSpec.MCPServers` | `agentkit/spec/decl/decl.go:629` | the same list for a sub-agent, next to its `DisabledTools` and `Skills` (§5.3) |
| `spec.Config.MCPs` | `agentkit/spec/config.go:119` | where the loader puts them |
| `mcpToolProvider` | `agentkit/build/mcptools.go:193` | the `toolProvider` implementation (§1.2) |
| `mcpManager` | `agentkit/build/mcpmanager.go:42` | the owner of the clients: one per server, keyed by the server name, living across snapshots (§6.6) |
| `mcpTool` | `agentkit/build/mcptools.go:311` | one tool: the eino tool plus the server it belongs to |
| `mcpPlan` | `agentkit/build/mcptools.go:67` | the shared mapping: the file's declaration plus the server's own tool list → the exposed names, the tiers, and the skips (§9.1) |
| `serialTool` | `agentkit/build/mcptools.go:334` | the wrapper that serializes the calls of one stdio server (§6.5) |
| `build.Policy` | `agentkit/build/policy.go:23` | the assembled table: name → tier. **Unchanged in shape** — only where the entries come from changes (§7.3) |
| `MCPServerView` | `agentkit/build/mcpstatus.go:43` | one server's row: the name, the transport, the state, the error, the tools |
| `run.MCPServers` | `storage/runledger.go:168` | the servers one run touched (§7.4) |

### 9.3 Where each piece of the design landed

| The design | The code |
|---|---|
| the namespace (§3) | `mcpPlan` (`agentkit/build/mcptools.go:67`) — the one place that composes `mcp__<server>__<tool>` (`agentkit/build/mcptools.go:140`) |
| the skip-and-warn (§3.2) | the same function — the name is checked before the tool is kept (`agentkit/build/mcptools.go:88`) |
| the file's own tier (§7.3) | the same function — the file's names are mapped to the exposed ones, and the table is assembled from the configuration (`agentkit/build/mcptools.go:172`) |
| the serialization (§6.5) | `serialTool` (`agentkit/build/mcptools.go:334`) — a wrapper, the same shape as `cappedTool` in `nettools.go` |
| the output cap (§6.4) | the same wrapper family — the component's `ToolCallResultHandler` (`agentkit/build/mcptools.go:371`) |
| the client's life (§6.2, §6.6) | the manager — `Start` / `Initialize` / `ListTools`, the lazy start, the restart of a crashed child (`agentkit/build/mcpmanager.go:290`), and the teardown when the process goes away (`agentkit/build/mcpmanager.go:150`) |
| the hot reload (§5.4) | `agentkit/spec/reload.go` — the partition is hot (`agentkit/spec/config.go:72`), and a changed declaration replaces the client (`agentkit/build/mcpmanager.go:229`) |
| a changed tool list (§6.6) | the manager — the re-list (`agentkit/build/mcpmanager.go:118`), and the generation bump that rebuilds the tools (`agentkit/build/builder.go:105`) |
| one start, one client (§6.8) | the manager — the single-flight start (`agentkit/build/mcpmanager.go:215`) |
| the servers a run touched (§7.4) | `agentkit/run/runrow.go:84` and `storage/runledger.go:168` — the run's row names them |
| the example and the editor's template (§5.6) | `agentkit/spec/write.go:189` (`spec.Template`), `initcfg/templates/mcps/example.yaml.disabled` and `initcfg/initcfg.go:193` |
| which servers an agent loads (§5.3) | `agentkit/spec/decl:114` (the field) → `agentkit/spec/load.go:529` (the union) → `agentkit/build/mcpmanager.go` (the start) |
| the agent's narrowing (§11.3) | `agentkit/build/policy.go:144` — the table's denied namespace prefixes |
| the `/mcps` view (§11.2) | `agentkit/build/mcpstatus.go:64` — the manager's state, the provider's naming, and the boundary's verdict, in one projection |

★ **One row moved.** The change list (§10.1) put the namespaced MCP names into
`spec.Config.EnabledToolNames`; they went into the **build** layer instead — the tool
name universe (`agentkit/build/introspect.go:127`, `:144`) — because the composition of
a name belongs to the build layer and `agentkit/spec` must not import it. ★ The
observable behaviour is the same: the API's `EffectiveTools` reports the MCP names
(`agentkit/build/introspect.go:49`), and it reports them from the configuration alone,
before a server is up.

★ **One function does three things**: the namespace, the skip-and-warn, and the tier
mapping all live in `mcpPlan`, because they all need the same two inputs — the server's
own tool list and the file that describes it. Splitting them would mean passing the
same two things around.

★ **And it is a free function, because there are two callers.** The provider turns the
plan into eino tools; the projection reports it (§11.2). ★ A second copy of the
mapping would be a second truth about a tool's name and a tool's tier — and the whole
point of the projection is that it is not a second truth.

## 10. The change list

Everything below **landed**. This section is the record of what the implementation
round did: where each piece of the design went, what the tests cover, and which
documents moved with it.

### 10.1 The code

| # | File | Change | Where it landed |
|---|---|---|---|
| 1 | `go.mod` | add `github.com/cloudwego/eino-ext/components/tool/mcp` and `github.com/mark3labs/mcp-go` | **done** — `go.mod:12`, `go.mod:18` |
| 2 | `agentkit/spec/config.go` | `DirMCP = "mcps"` and a `Categories` entry, hot | **done** — `agentkit/spec/config.go:26`, `:72` |
| 3 | `agentkit/spec/decl/decl.go` | the `MCPServerCfg` declaration struct, the `MCPFiles` holder, the namespace constants; `call_timeout`; ★ the agent's `MCPServers` field, on the agent **and** on a sub-agent (§5.3) | **done** — `agentkit/spec/decl/decl.go:454`, `:502`, `:336`, `:345`, `:353`, `:114`, `:629` |
| 4 | `agentkit/spec/load.go` | the loader for `mcps/<server>.yaml`, with the file-name and field validation of §3.1 and §5.2, and the load-time hard errors of §5.7 | **done** — `agentkit/spec/load.go:256`, `:499` |
| 5 | `agentkit/spec/defaults.go` | `EnabledToolNames` gains the namespaced MCP tools; the default tier of a server (`ask`) | ★ **moved** — the names went into the **build** layer's tool name universe (`agentkit/build/introspect.go:127`), because the composition of a name belongs there and `agentkit/spec` must not import it; the default tier is the namespace's fallback (`agentkit/build/policy.go:55`) |
| 6 | `agentkit/spec/write.go` | the scaffold for a new `mcps/` file | **done** — `agentkit/spec/write.go:189`, and the server-name check at `:107` |
| 7 | `agentkit/build/tools.go` | `mcpToolProvider`, registered in the registry | **done** — `agentkit/build/tools.go:41`, the provider at `agentkit/build/mcptools.go:193` |
| 8 | `agentkit/build/policy.go` | the prefix matcher for `Blocked`, **and** the tier **assembly**: the providers contribute (exposed name → tier) from their own file's declarations (§7.3) | **done** — `agentkit/build/policy.go:108`, `:161`, `:34` |
| 9 | `api/v2_settings.go` | the editable section for `mcps/` | ★ **generic** — the manifest walks `spec.Categories` (`api/v2_settings.go:135`), and `mcps/` is an editable category (`agentkit/spec/config.go:72`), so no dedicated section was needed |
| 10 | `agentkit/build/nettools.go` | the output cap, applied to the MCP batch | ★ **elsewhere** — the cap sits in the component's result handler (`agentkit/build/mcptools.go:371`) and reuses the same constant (`agentkit/build/nettools.go:26`) |
| 11 | `agentkit/build/mcptools.go` | `mcpPlan` — the shared naming and tier mapping (§9.1), the per-server mutex that serializes the stdio calls (§6.5), and the environment assembly for the child process (§5.5) | **done** — `agentkit/build/mcptools.go:67`, `:334`; ★ the environment assembly is in the manager (`agentkit/build/mcpmanager.go:458`) |
| 12 | `agentkit/build/mcpmanager.go` | the manager: one client per server, the **single-flight start** (§6.8), the lazy start **of the servers some agent asked for** (§5.3), the restart of a crashed child, the tool-list re-list that bumps the generation, and the teardown on the snapshot swap (§6.6) | **done** — `agentkit/build/mcpmanager.go:42`; ★ the teardown is on the **declaration** (`:229`) and on the process going away (`:150`), not on the snapshot swap (§6.6) |
| 13 | `agentkit/run/runrow.go` | the run's row records which MCP servers the run touched (§7.4) | **done** — `agentkit/run/runrow.go:84`, `storage/runledger.go:168` |
| 14 | `initcfg/templates/` and `spec.Template` | the `mcps/example.yaml.disabled` example, and the editor's template for a new `mcps/` file — the editor's manifest is filled per category, so a new category gets its scaffold for free (§5.6) | **done** — `initcfg/templates/mcps/example.yaml.disabled`, `initcfg/initcfg.go:193`, `agentkit/spec/write.go:189` |
| 15 | `api/commands.go` | the `mcps` action command and `commandActionOpenMCPs` (§11.1) | **done** — `api/commands.go:31`, `:61` |
| 16 | `api/v2_mcps.go` | `GET /v2/mcps` — the view (§11.4) | **done** — `api/v2_mcps.go:74`, the route at `api/v2_files.go:63` |
| 17 | `api/v2_introspect.go` | the `MCPSource` interface and `SetMCPSource`: the API layer asks the builder, it does not read the manager (§11.2); ★ and the configured servers in the configuration view (§11.4) | ★ **split** — the interface and `SetMCPSource` are in `api/v2_mcps.go:18`, `:41`; the configuration view lists the servers at `api/v2_introspect.go:388` |
| 18 | `agentkit/build/mcpstatus.go` | `Builder.MCPServers(agentID)` — the projection of the manager's state (§11.2) | **done** — `agentkit/build/mcpstatus.go:64` |
| 19 | `agentkit/build/policy.go` | the agent's `mcps:` list in the table: the denied namespace prefixes, and the reason that goes with them (§11.3) | **done** — `agentkit/build/policy.go:144`, wired at `agentkit/build/topology.go:70` |
| 20 | `main.go` | the wiring, next to `SetToolsSource` (`main.go:278`) (§11.2) | **done** — `main.go:247` (the bump), `:279` (`SetMCPSource`), `:384` (the close) |
| 21 | `web/src/lib/api.ts` | the `open_mcps` action and `listMCPServers` (§11.5) | **done** — `web/src/lib/api.ts:136`, `:329` |
| 22 | `web/src/components/mcp-panel.tsx` | the panel (§11.5) | **done** |
| 23 | `web/src/components/composer.tsx` | the action case that opens the panel (§11.5) | **done** — `web/src/components/composer.tsx:282` |
| 24 | `agentkit/build/builder.go` and `agentkit/build/manager.go` | the turn's first step: ask the manager whether a server's tool list changed, and take the new snapshot when it did; the **bump** callback that moves the generation (§6.6) | **done** — `agentkit/build/builder.go:67`, `:105`; ★ the config manager stays read-only (`agentkit/build/manager.go:12`) |

★ **Three rows moved, and one did not land as written.** The tool name universe went
to the build layer (row 5), the output cap went into the component's result handler
(row 10), and the API's MCP interface went into `api/v2_mcps.go` (row 17). The teardown
on the snapshot swap (row 12) did not happen: the manager owns the clients across
snapshots, and what replaces a client is a changed **declaration** (§6.6).

### 10.2 The tests

| Area | What it proves | The test |
|---|---|---|
| Naming | a server offering `ls` yields `mcp__<server>__ls` | `agentkit/build/mcptools_test.go` `TestAnMCPNameIsTheServersOwnNameUnderTheNamespace` |
| Naming | the characters the model provider refuses become `_` | `agentkit/build/mcptools_test.go` `TestAnMCPNameReplacesTheCharactersTheModelRefuses` |
| Naming | the server's own case is kept | `agentkit/build/mcptools_test.go` `TestAnMCPNameKeepsTheServersOwnCase` |
| Naming | the 64-character boundary, and the skip-and-warn path above it | `agentkit/build/mcptools_test.go` `TestAnMCPNameAtTheBoundaryIsKept`, `TestAnMCPNameOverTheBoundaryIsSkipped` |
| Naming | two tools of one server that sanitize alike — the second is skipped | `agentkit/build/mcptools_test.go` `TestTwoToolsThatSanitizeAlikeLeaveTheSecondOneSkipped` |
| Matching | the file's `tools:` lists the server's **own** names, and an absent list means all | `agentkit/build/mcptools_test.go` `TestTheFileAsksForItsOwnToolsAndNotTheExposedOnes`, `TestAnAbsentToolListMeansEveryTool` |
| Matching | a prefix rule hits the tools of one server and only those | `agentkit/build/mcptools_test.go` `TestAPrefixRuleReachesTheToolsOfOneServerOnly` |
| Matching | `mcp__*` reaches every MCP tool, and an exact entry still removes one tool | `agentkit/build/mcptools_test.go` `TestAWildcardPrefixReachesEveryMCPTool`, `TestAnExactEntryStillRemovesTheTool` |
| Matching | `mcp*foo` and a lone `*` are load-time errors | `agentkit/spec/mcp_test.go` `TestAWildcardThatIsNotAPrefixPatternIsALoadTimeError`, `TestALoneWildcardIsALoadTimeError`, `TestATrailingStarIsAPrefixPattern` |
| Tier | everything asks, and the file names the exceptions | `agentkit/build/mcptools_test.go` `TestEverythingAsksAndTheFileNamesTheExceptions` |
| Tier | an empty `allows:` covers the tools a server has **not offered yet** | `agentkit/build/mcptools_test.go` `TestAnEmptyAllowsListCoversTheToolsTheServerHasNotOfferedYet` |
| Tier | the tier of a tool is answerable while the server is unreachable | `agentkit/build/mcptools_test.go` `TestTheTierIsAnswerableWhileTheServerIsUnreachable` |
| Tier | a name in the global `deny` stays denied | `agentkit/build/mcptools_test.go` `TestAnMCPNameInTheGlobalDenyStaysDenied` |
| Tier | the file's `allows:` reaches the gate under the composed name | `agentkit/build/mcplive_test.go` `TestTheProviderMountsTheToolsOfARealServer` |
| Servers | an absent `mcps:` means every configured server, `[]` means none | `agentkit/build/mcptools_test.go` `TestAnAbsentServerListMeansEveryConfiguredServer`, `TestAnEmptyServerListDeniesEveryConfiguredServer`; `agentkit/spec/mcp_test.go` `TestAnAbsentServerListMeansEveryConfiguredServer`, `TestAnEmptyServerListMeansNoServerAtAll` |
| Servers | a sub-agent's own `mcps:` list is read from the sub-agent | `agentkit/spec/mcp_test.go` `TestASubAgentsOwnListIsReadFromTheSubAgent` |
| Narrowing | the table denies the namespace **prefix**, and `mcp__github__` does not reach `mcp__github_x__` | `agentkit/build/mcptools_test.go` `TestTheServerNarrowingDeniesTheNamespacePrefixOnly` |
| Narrowing | the server narrowing is reported before the tool rule | `agentkit/build/mcptools_test.go` `TestTheServerNarrowingIsReportedBeforeTheToolRule` |
| Loading | an unknown `transport` is a load-time error, and there is no fallback to stdio | `agentkit/spec/mcp_test.go` `TestAnUnknownTransportIsALoadTimeError` |
| Loading | a stdio server needs a `command`, an http server needs a `url` | `agentkit/spec/mcp_test.go` `TestAStdioServerNeedsACommand`, `TestAnHTTPServerNeedsAURL` |
| Loading | `env:` on an http server is a load-time error, even when it is empty | `agentkit/spec/mcp_test.go` `TestAnHTTPServerMayNotDeclareAnEnvironment`, `TestAnEmptyEnvironmentOnAnHTTPServerIsStillRefused` |
| Loading | an unknown key is refused | `agentkit/spec/mcp_test.go` `TestAnUnknownKeyIsRefused` |
| Loading | the server name is the file name, and it must be readable back | `agentkit/spec/mcp_test.go` `TestTheMCPFileNameIsTheServerName`, `TestTheServerNameMustBeReadableBack` |
| Loading | an agent's `mcps:` naming a server that does not exist is a load-time error | `agentkit/spec/mcp_test.go` `TestAnAgentNamingAServerThatDoesNotExistIsALoadTimeError` |
| Loading | the timeouts have defaults, and a bad one degrades and is noticed | `agentkit/spec/mcp_test.go` `TestTheTimeoutsHaveDefaults`, `TestABadTimeoutDegradesAndIsNoticed` |
| Environment | an unset `${ENV}` in `env:` is deferred, not fatal; a set one is expanded | `agentkit/spec/mcp_test.go` `TestAnUnsetVariableInTheEnvironmentIsDeferredAndNotFatal`, `TestASetVariableInTheEnvironmentIsExpanded` |
| Environment | the child sees openheron's environment, with `env:` overriding the same names | `agentkit/build/mcpmanager_test.go` `TestTheChildEnvironmentIsTheProcessesOwnWithTheFileOnTop` |
| Command | a `command` that is not on the PATH is a warning, and the workspace never resolves it | `agentkit/build/mcpmanager_test.go` `TestTheCommandIsNeverResolvedAgainstTheProjectRoot` |
| Provider | an unreachable server says so, and the other servers stay alive | `agentkit/build/mcpmanager_test.go` `TestAnUnreachableServerSaysSoAndKeepsTheOtherServersAlive` |
| Provider | the tools of a **real** server are mounted under the namespace, and a call reaches the server and comes back | `agentkit/build/mcplive_test.go` `TestTheProviderMountsTheToolsOfARealServer`, `TestAMCPtoolCallReachesTheServer` |
| Lifecycle | a changed declaration replaces the client | `agentkit/build/mcpmanager_test.go` `TestAChangedDeclarationReplacesTheClient` |
| Manager | a crashed stdio child is started again on the next call | `agentkit/build/mcpmanager_test.go` `TestACrashedChildIsStartedAgainOnTheNextCall` |
| Manager | a changed tool list is reported by the refresh, and a fresh listing is not re-listed | `agentkit/build/mcpmanager_test.go` `TestAChangedToolListIsReportedByTheRefresh`, `TestAFreshListingIsNotReListed` |
| Manager | two callers asking for the same server get **one** client | `agentkit/build/mcpmanager_test.go` `TestTwoCallersAskingForTheSameServerGetOneClient` |
| Ledger | a run's row names the MCP servers it touched, and the names come out of the namespaced tool names | `agentkit/run/runrow_mcp_test.go` `TestARunRowNamesTheMCPServersItTouched`, `TestTheServersAreReadOutOfTheNamespacedToolNames` |
| Editor | the config editor can create a file under `mcps/`, and refuses a server name that could not be read back | `agentkit/spec/mcp_test.go` `TestTheEditorAcceptsAFileUnderMCPS`, `TestTheEditorRefusesAServerNameThatCouldNotBeReadBack` |
| Command | `/mcps` is an action, and it never reaches the model as a prompt | `api/v2_mcps_test.go` `TestTheMCPCommandIsAnAction`, `TestTheMCPCommandNeverReachesTheModel` |
| View | the agent parameter is optional, and an unknown agent is a 404 | `api/v2_mcps_test.go` `TestV2MCPServersReportsTheServersOfTheAgent`, `TestV2MCPServersTakesTheAgentParameter`, `TestV2MCPServersRefusesAnUnknownAgent` |
| View | the configuration view lists the configured servers | `api/v2_mcps_test.go` `TestV2ConfigListsTheConfiguredServers` |
| View | every configured server appears exactly once, and a server the agent does not use is `unused` with no tool rows | `agentkit/build/mcpstatus_test.go` `TestEveryConfiguredServerAppearsExactlyOnce`, `TestAServerTheAgentDoesNotUseIsUnusedAndHasNoToolRows` |
| View | an unreachable server reports the error and keeps its last listing | `agentkit/build/mcpstatus_test.go` `TestAnUnreachableServerReportsTheErrorAndKeepsItsLastListing` |
| View | the reason is `mcps` when the server is not in the list, and `disabled_tools` when the tool is | `agentkit/build/mcpstatus_test.go` `TestTheServerNarrowingIsTheReasonTheAgentsViewReports`, `TestTheReasonIsTheToolRuleWhenTheServerIsThere` |
| View | the panel asks the manager, and the manager starts the server; the tier is the gate's own | `agentkit/build/mcpstatus_test.go` `TestThePanelAsksTheManagerAndTheManagerStartsTheServer`, `TestThePanelShowsTheTierTheGateWouldUse` |
| Panel | the panel shows both names and the tier, and it says what it cannot reach | `web/test/e2e/mcps.spec.ts` |
| Panel | the empty state says that no server is configured | `web/test/e2e/mcps.spec.ts` |
| Panel | the panel re-reads every time it opens, and **Refresh** re-reads it | `web/test/e2e/mcps.spec.ts` |
| Panel | `/mcps` never reaches the model and never opens a session | `web/test/e2e/mcps.spec.ts` |

★ **Three cases the design asked for have no test yet.** The serialization of one
stdio server's calls (§6.5), the `call_timeout` deadline, and the warning for a name
the file's `tools:` asks for and the server does not have (§6.1). ★ The
implementation of the first two is there (`agentkit/build/mcptools.go:334`,
`agentkit/build/mcptools.go:240`); what is missing is the test that would prove it.

### 10.3 The documentation

| Document | What changed |
|---|---|
| `docs/architecture.md` | the tool assembly path gains the MCP provider; the `mcps/` partition and the agent's `mcps:` key enter the configuration tables |
| `docs/config-design.md` | the `mcps/` partition (§6.7) and the prefix pattern in an agent's `disabled_tools` (§6.3) become **implemented**; the global `deny` stays a proposal |
| `docs/user-manual.md` | how to configure a server, what the names in the interface look like, and what the `/mcps` command shows (§11.5) |
| `docs/developer-manual.md` | the "adding a partition" recipe gains the `mcps/` case |
| `README.md` | the HTTP reference gains `GET /v2/mcps` |
| `docs/mcp-design.md` | this document — the status header, §1 and §10 are rewritten to describe the code as it is |

## 11. The `/mcps` command: the MCP servers in the interface

★ The interface has to answer a question the log cannot: **which MCP servers does this
agent have, are they up, and what will they cost me when they run?** The manager knows
all three. The panel must not compute a verdict of its own.

### 11.1 It is an action, not a prompt

`/mcps` is registered in the server's command table (`api/commands.go:61`) as an
**action**:

```go
{
    Name: "mcps", Description: "Show the MCP servers of this agent",
    Kind: commandKindAction, Action: commandActionOpenMCPs,
},
```

★ **An action, not a prompt**, for the reason `/compact` is one (`api/commands.go:69`):
a prompt would ask the **model** to describe the servers, and the model's answer would
be a second, lossy truth about a fact the **runtime** holds. The status is read, not
generated.

★ The action opens a panel; it does not write a message into the transcript. A status
readout is not a turn, and a `/mcps` in the middle of a dialogue would read as if the
agent had said something.

### 11.2 The rule

> **`/mcps` asks the manager what this agent would get from its servers.**

One sentence, and every row of the panel follows from it:

| The question | The answer comes from |
|---|---|
| is the server up? | the manager's client (§6.6) |
| what did it offer? | the manager's listing (§6.6) |
| what is the tool called? | the plan — `mcpPlan` (§9.3) |
| will it ask me? | `Policy.TierOf` — the gate's own lookup (§1.3) |
| can this agent reach it? | the agent's table (§11.3) |

★ All five answers come out of **one** function, `Builder.MCPServers`
(`agentkit/build/mcpstatus.go:64`), and the API layer only forwards what it returns
(`api/v2_mcps.go:74`).

★ **The panel derives nothing.** Every value it shows is one the runtime already holds.
A panel that re-decides anything becomes a second truth, and the first thing that
breaks is the tier: the panel would say `ask` while the gate says `allow`.

★ **The tool rows are the verdicts the agents view already reports.** The per-tool
verdict is computed by the same function (`agentkit/build/introspect.go:113`), over the
same boundary, so `/mcps` and `/v2/agents/<id>` cannot report two different tiers for
one tool. ★ The only difference is the **name universe**: the agents view walks the
observed tools, the panel walks the manager's listing — so a server that is down still
shows what it offered.

★ **Asking starts the servers.** The manager starts a server when it is asked for, and
the panel asks. So `/mcps` before the first turn starts the servers **this agent** uses —
the same start the first turn would do for this agent, and no more. ★ The alternative (a
strictly read-only panel that reports "not started") cannot answer the question the user
typed the command for: "is my server broken?" — and the answer would be "run a turn and
find out".

★ **The panel's ask goes through the same single-flight start** (§6.8). The panel and a
turn can ask at the same moment, and they get **one** client — ★ two callers racing into
the start would be two child processes for one server, and the tools the model sees
would depend on which one answered.

### 11.3 The per-agent narrowing, and where it lands

★ §5.3 says the agent's `mcps:` list decides who gets a server, and §6.6 says a server
no agent lists is never started. Neither says **where** the narrowing happens, and the
answer is not free: the tool set is **one set for the whole snapshot**
(`agentkit/build/snapshot.go:40-45` — `buildHeavy` runs under a `sync.Once`), and every
agent shares it.

⇒ **The narrowing happens in the agent's table**, exactly where `disabled_tools` already
narrows the tools (`agentkit/build/gate.go:33` — `Table.Filter` drops the tools from
`runCtx.Tools` before the agent runs, so the model never sees them).

The table gets the agent's server list, and a server the agent does not use is denied
**by its namespace prefix**:

| The agent's `mcps:` | The table denies |
|---|---|
| absent | nothing — every configured server |
| `[github]` | the prefix of every configured server except `github` |
| `[]` | the prefix of every configured server |

★ **The prefix, not the parsed name.** §3.1 says the composed name is never split back
into its two halves; the table matches `mcp__<server>__` as a **prefix string** it
already knows from the configuration. ★ And a server name may not contain a run of
underscores (§3.1), so `mcp__github__` cannot reach `mcp__github_x__`.

★ **The start is the union.** The snapshot is shared, so the manager starts the servers
that **some** agent lists: a server no agent lists is never started, and a server only
`task` lists is started — but `main` cannot reach its tools. ★ The alternative (a tool
set per agent) would mean rebuilding the tool list for every agent, and the tool list
is what the snapshot freezes (§1.1).

★ **The union is over the agents that can run, sub-agents included.** A sub-agent has a
list of its own (§5.3), and it may run inside a turn the root did not ask for. ⇒ the
union is taken over every agent and every sub-agent that the configuration declares —
a server only a sub-agent lists is started, because that sub-agent may be the one
running.

### 11.4 The endpoint

`GET /v2/mcps?agent=<agentId>` — `agent` is optional, and absent means the config's
default agent.

★ The endpoint is **new**, not an extension of `/v2/agents`: the agents view is a
**configuration** view (it is built from the config, `api/v2_introspect.go:183`), and
the MCP status is **runtime** state — it changes when a child process dies, and it must
not be cached for the tab's lifetime the way the agents list is
(`web/src/lib/agent-info.ts:48`).

```json
{
  "agent": "main",
  "servers": [
    {
      "name": "github",
      "transport": "stdio",
      "status": "ready",
      "error": "",
      "tools": [
        {
          "name": "read_issue",
          "exposedName": "mcp__github__read_issue",
          "tier": "ask",
          "status": "available",
          "reason": ""
        }
      ]
    }
  ]
}
```

| Field | Meaning |
|---|---|
| `servers[].status` | `ready` — the client is up; `unreachable` — the last start failed, and `error` says why; `unused` — this agent does not list the server |
| `servers[].error` | the failure text of the last start |
| `servers[].tools[].name` | the server's **own** name — the vocabulary of the file (§5.2) |
| `servers[].tools[].exposedName` | the name the model sees (§3) |
| `servers[].tools[].tier` | the gate's verdict for that name (§1.3) |
| `servers[].tools[].status` | `available` — this agent reaches it; `skipped` — the server offered it and it was not exposed (§3.2); `blocked` — exposed, but this agent does not reach it |
| `servers[].tools[].reason` | why it is not available: `disabled_tools`, `mcps`, `name_too_long` or `name_collision` |

★ **The server narrowing is the coarser one, and it is reported first.** A tool can be
blocked by two things at once — the server is not in this agent's `mcps:` list, **and**
the tool is in its `disabled_tools`. ⇒ the reason is `mcps`: the server is not there at
all, and the finer rule would have said nothing about it. ★ The other order would
report a tool rule for a server the agent does not have.

★ **The configuration view lists the servers too.** `GET /v2/config` gains the
configured servers' names and transports (`api/v2_introspect.go:388`): the
configuration says **what exists**, the panel says **what is up**. ★ A configuration
view that hid the `mcps/` partition would leave the settings UI with nothing to show.

★ **A swap in the middle of the request is not a mixture.** The manager's clients
belong to a generation (§5.4), and the panel's ask goes through the same start a
turn's does (§6.8) — so it gets the **current** generation's client, or the failure
of starting it. ★ The answer is one generation's state, never a mixture of two.

★ **Both vocabularies are shown.** The file says `allows: [read_issue]`; the model sees
`mcp__github__read_issue`. A panel that shows only the exposed name leaves the user
unable to connect a row to their own file, and a panel that shows only the own name
hides what the model can call.

★ **Every configured server appears, exactly once.** A server the agent does not use is
`unused` — not missing. ★ A server that is absent from the list and a server the agent
does not use look the same to the reader, and only one of them is a problem.

★ **An `unused` server has no tool rows.** The panel is about **this agent's** servers,
and the tools of a server it does not use are not its tools — listing them would put
rows in front of the user that no call can ever reach.

★ **The tools of an unreachable server are its last listing.** The manager keeps the
last successful list (§6.6 — a crashed child is started again on the next call, and the
list comes back with it), so the panel can say "github — unreachable — these are the
tools it had when it was last up". The status says whether the listing is current; the
listing says what the server offers.

★ **And `available` is about the agent, not about liveness.** It means *this agent's
table does not remove the tool* — not *the call would succeed*. A downed server's rows
are the tools it had; whether the call would work is the server's status to say.

★ **The reasons are tokens, the copy is the panel's.** `disabled_tools` and `mcps` are
machine values, like the `code` of an error body; the English sentence that explains
them belongs to the interface, not to the wire.

### 11.5 The panel

★ It is a **dialog**, like the agent picker (`web/src/components/agent-picker.tsx:68`):
the content is a table of servers, the answer is transient, and the transcript is not
the place for it.

| Element | What it shows |
|---|---|
| the title | "MCP servers", and the agent they belong to |
| one block per server | the name, the transport, the status, the error line |
| one row per tool | the own name, the exposed name, the tier |
| the empty state | "No MCP servers are configured" |

★ The panel fetches when it opens, every time — the status is live state, and the
tab's caches (the command table at `web/src/lib/commands.ts:9`, the agents list) are
not the place for it. A **Refresh** button re-reads it, because the user fixes a
server and wants to see the fix without closing the dialog.

★ The agent is the session's agent (`web/src/lib/store/selectors.ts:13`), and the title
says which one — the servers of `task` are not the servers of `main`. ★ The list that
applies to a turn is the one of the agent **that is running** (§5.3); the panel shows
the one the next turn runs as.

## 12. What is not in this design

| Not covered | Why |
|---|---|
| The MCP protocol, and the transports | they belong to `mcp-go`; openheron picks a client and configures it |
| A server registry with discovery, health checks and retries | one file per server is enough to start; a registry is a second round |
| MCP resources, prompts and sampling | the eino-ext component exposes **tools** only |
| Per-tool rate limits and quotas | `policy/limits.yaml` counts things; it has no tool-name dimension |
| A credential that is not a header | `inject` is a header injection; a key in a query parameter, a cookie or a non-HTTP protocol has to enter the sandbox instead (§8.1) |
| A mixin that installs a host-side MCP server | a mixin is a declaration; content belongs to the workload (`sandbox-kit-design.md` §2.3) |
| ★ **The Kit packaging MCP servers** | the whole of §8 is still a proposal: the Kit does not carry an MCP server yet |
| ★ **Removing the global `deny`** | the configuration design proposes it (§6.3 of the configuration design), and the MCP feature did not take it along (§4.2) |
| A panel that subscribes | the panel reads the manager's state when it opens; it does not subscribe, and it does not retry (§11.5) |
| The SSE transport | `http` means **streamable HTTP**; SSE is the older transport, and openheron does not speak it (§5.2) |

★ Every entry in this document that describes code names a file and a line. When the
code moves, the line moves with it — that is the contract this repository keeps for
every other design document, and it is the reason the table in §10.1 is a record of
locations rather than a list of intentions.
