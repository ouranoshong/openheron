# openheron configuration example

Copy these directories into the project root's `.openheron/`:

```bash
cp -r .openheron-example/{agents,llm,skills} /your/project/.openheron/
```

| Directory | Contents |
|---|---|
| `agents/main.md` | agent declaration — frontmatter (`model` / `max_iterations` …) + **the body is the prompt** |
| `llm/openai.yaml` | vendor profile — `base_url` / `api_key` / `models[]` / thinking tier table |
| `server/config.yaml` | process parameters — listen address / directories / timeouts / **not hot-reloaded** |
| `skills/<name>/SKILL.md` | eino native skill convention |
| `tools/<tool>.yaml` | external tools and the knowledge base — **a provider without a configuration mounts nothing** (this sample ships none; external tools are all off by default) |

`state/` (sessions and checkpoints) is created automatically on first startup —
**do not** commit it.

## Which changes take effect automatically

| Directory | Hot reload | Why |
|---|---|---|
| `llm/` `agents/` `tools/` `mcps/` | ✅ | the new configuration is used from the next turn |
| `server/` | ❌ | it configures the process itself, so a change needs a restart |

A broken configuration **does not kill the process** — the previous one is kept and
one log line is written.

## Three rules that trip people up

1. **`api_key` must be written as `${ENV}`** — an environment variable that is not
   set does **not** block startup, it only records one reminder in the startup log;
   the "no permission" (401) only comes back when that endpoint is actually
   requested. You can also use `@file:/path` to read the key from a file (a wrong
   path is an error at startup). **Do not write the key into this file** — it
   normally belongs in version control.

2. **`model` must be written as `<provider>/<model>`** — `provider` is the file name
   under `llm/`, and `model` is the `id` declared in that file's `models[]`. A
   reference that cannot be resolved is an error at startup; it **never silently
   degrades to another model**.

3. **Durations must carry a unit** — `timeout: 5m`; writing `timeout: 300` is
   rejected (otherwise `300` would be read as 300 nanoseconds).

## Thinking tiers

Tier names are defined by `models[].reasoning` in `llm/*.yaml`; an agent only writes
the tier name:

```yaml
# agents/main.md
reasoning: high
```

**The same `high` may map to a completely different field at different endpoints** —
that is exactly why the vocabulary table exists. When an agent writes a tier the
model does not have, startup is **not blocked**, but a line in the startup log says
so ("the thinking parameters for this tier were not sent") — that is the only
visible clue.

## Which skills an agent may use

`skills/` holds the skills; **the agent decides which of them it may use**, with the
`skills` key in its own frontmatter:

```yaml
# agents/main.md
skills: [refactor]
```

The names are the **folders** under `skills/` — the skill's own frontmatter name is
accepted as well. Not declaring the key means **every** skill in the directory;
declaring an empty list means **none**.

A skill outside the list is not advertised to the model, and asking for it by name
fails: a model cannot load what it cannot see. A name that is not in the directory is
reported in the log when the agent is built, rather than ignored.

★ Only the **entry agent** — the one the session selected — carries the skill tool at
all. A sub-agent has none, so a list on a sub-agent has nothing to filter.

## Sub-agents (multiple roles)

`subagents: [researcher, coder, reviewer]` in `agents/main.md` declares the topology.
A sub-agent and any other agent **are the same thing in the same format** — the only
difference is the file name.

Every sub-agent gets **its own** approval and tool-filtering middleware assembled
separately (you cannot count on the parent agent propagating it), and each one
builds its own model instance from its own `model:`.

| Switch | Location | Effect |
|---|---|---|
| `agent.max_depth` | `server/config.yaml` | max nesting depth (the entry is level 0). The default `1` = only the entry agent may delegate |
| `tools` | `agents/*.md` | the tools this agent may use. **Absent = none** — the list is the whole answer. The tier table itself is **shared**; this list is per agent |

A declared topology that exceeds `max_depth` is **an error at startup**; it is never
silently truncated.

### The four roles of the factory sample

| Role | What it does | Can it write files? |
|---|---|---|
| `main` | orchestration: dispatch, summarize | No (writing and execution are disabled by default) |
| `researcher` | read-only retrieval: locate implementations, trace call chains | No |
| `coder` | change files per the conclusions, run tests | **Yes** |
| `reviewer` | read the changes, find problems | No |

"Who may write" is controlled by **two** layers, and neither may be missing:

- **The tier table**: a tool nobody names **asks**; a file's `allows:` names the
  exceptions, and that list is **shared** by every agent;
- **Each agent's own `tools`**: the built-in tools *that* agent may use at all.
  The tools of an MCP server are **not** in that list — the agent mounts the
  server in `mcps:` and gets all of its tools.

★ The two are stored separately and never merged — merging them would throw
  away "who narrowed what". ★ A file's `allows:` only **adds** to the tier
  table; an agent's `tools` list can only take a tool away, never hand one
  back.

### Parallel delegation

Several **mutually independent** things can be sent as multiple task calls in one
message so that they run at the same time:

- "implement module A and module B separately" → two `coder`s in parallel;
- "first look up A's implementation, then look up B's" → two `researcher`s in
  parallel (both are read-only and do not tread on each other);
- "review it once the change is done" → **serial**; the `reviewer` must wait for the
  `coder` to finish.

★ Parallelism only holds for **mutually independent** tasks. When the output of one
  step is the input of the next, running in parallel only yields a pile of
  conclusions based on the old world.

★ When several sub-agents need human approval at the same time, several approval
  cards pop up **simultaneously** — answer them **together**; answer only one and
  the others will ask again.

★ In parallel, each approval card names **who** requested it ("Requested by coder") —
  that is derived from the framework's address chain, not guessed. See
  `docs/multi-agent-design.md`.

## Default approval tiers

**The default tier is `ask`** — a tool nobody names asks first. The exceptions
are named, and each one is named by **the file that provides the tool**:

```yaml
# tools/http.yaml
allows: [request_get]
```

The built-in tools that have **no file of their own** — the filesystem readers
(`ls` `read_file` `glob` `grep`), the delegation tools (`task` `write_todos`) and
the skill reader — run without a nod; the code knows them. Everything else
asks: `write_file`, `edit_file`, `execute`, and any MCP tool the server's own
file does not name.

★ A tool a file names in its `allows:` uses **that file's own vocabulary** — an
  MCP server says `create_issue`, not `mcp__github__create_issue`, and the
  translation happens once, where the tool is mounted.

★ **"Not exposed" is said in exactly two places**: a tool's own `allows:` list
  (what runs without a nod) and an agent's `tools` list (what that agent may
  use at all). There is no third, global list to keep in sync.

★ To narrow down **one** agent's capabilities, use its own `tools` list. ★ And
  to widen them — to let an agent write and run commands unattended — name the
  tools in that same list, or in the file that provides them.

★ An MCP server's tools are narrowed by the **server's own file** (`tools:` is
  the filter there), not by the agent: the agent either mounts the server — and
  then has all of its tools — or does not.
