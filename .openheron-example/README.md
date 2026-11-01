# openheron configuration example

Copy these directories into the project root's `.openheron/`:

```bash
cp -r .openheron-example/{agents,llm,policy,skills} /your/project/.openheron/
```

| Directory | Contents |
|---|---|
| `agents/main.md` | agent declaration — frontmatter (`model` / `max_iterations` …) + **the body is the prompt** |
| `llm/openai.yaml` | vendor profile — `base_url` / `api_key` / `models[]` / thinking tier table |
| `policy/permissions.yaml` | approval tiers — `ask` / `deny` (**anything not named is allowed**) |
| `server/config.yaml` | process parameters — listen address / directories / timeouts / **not hot-reloaded** |
| `skills/<name>/SKILL.md` | eino native skill convention |
| `tools/<tool>.yaml` | external tools and the knowledge base — **the file's presence = enabled** (this sample ships none; external tools are all off by default) |

`state/` (sessions and checkpoints) is created automatically on first startup —
**do not** commit it.

## Which changes take effect automatically

| Directory | Hot reload | Why |
|---|---|---|
| `llm/` `agents/` `policy/` `tools/` | ✅ | the new configuration is used from the next turn |
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
| `disabled_tools` | `agents/*.md` | affects this one agent only. Stored **separately from the global `deny`, never merged** |

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

- **Outer**: the `deny` in `policy/permissions.yaml` — nobody may use it;
- **Inner**: each agent's own `disabled_tools` — affects that one agent only.

★ The outer `deny` takes precedence over the inner `disabled_tools`: it is the one
  guarantee a sub-agent **cannot relax**. The two layers are stored separately and
  never merged — merging them would throw away "who is in the outer layer".

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

**The fallback for an unlisted tool is `allow` (straight through)** — the policy file
has only the `ask` / `deny` columns, so not being named means being allowed. A newly
added tool is therefore directly usable by default; to make it **require a human nod
every time**, add its name to `ask` in `policy/permissions.yaml`; to make it
**completely unusable**, add it to `deny`.

★ The outer `deny` takes precedence over the inner `disabled_tools` — it is the one
  guarantee a sub-agent cannot relax. To narrow down **one** agent's capabilities,
  use its own `disabled_tools`, not the global policy.
