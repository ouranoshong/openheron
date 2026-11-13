---
name: openheron-agent-authoring
description: How to write an agent definition that openheron actually accepts and can dispatch. Covers the design sequence from intent to the finished file, what each field decides, the three allow-lists, the topology, the shape of the Instruction body, and the closing checklist. Load it when creating, changing or reviewing agents/<id>.md, or when asked how to make a new agent for openheron.
context: inline
---

# Authoring an agent for openheron

This skill is the **recipe**, not the reference: the shape and the meaning of a field belong to
`openheron-config-map`, the skeleton to `openheron-config-templates`, the act of changing a file to
`openheron-config-edit`, and the closing check to `openheron-config-validate`. Load this one when the
question is "what should this agent look like", and load the others for the details it points at.

## The design sequence

Work through these in order; each step decides what the next one can be.

1. **The intent in one sentence** — what the agent is for, and what it must *not* do. If the sentence
   needs an "and", it is two agents.
2. **The role and the boundary** — one capability split into a specialist. A specialist owns one
   part of the configuration tree and never crosses into another one.
3. **`description`** — the text the upper model reads inside the `task` tool to decide whether to
   delegate. It is not a summary of the file.
4. **`model`** — `<provider>/<model>`, taken from what `llm/` really declares. Ask the configuration
   tools for the list; do not recall it.
5. **`tools:`** — the smallest set that lets the agent finish its job.
6. **`skills:`** — the skills it actually needs, by folder name.
7. **`mcps:`** — the servers it mounts; mounting one hands it all of that server's tools.
8. **`subagents`** — where it sits in the tree, and whether it may delegate at all.
9. **The body** — the Instruction that tells it how to work, in the structure below.
10. **The closing check** — the checklist at the end of this skill.

## What each field is deciding

| Field | What you are deciding | The trap |
|---|---|---|
| `description` | whether the upper model picks this agent | a content summary — then the model has no trigger to delegate |
| `model` | which model answers | a model that `llm/` does not declare: a startup error |
| `tools:` | which non-MCP tools exist for this agent | omitting it silently yields zero tools; writing `disabled_tools` is a load-time hard error |
| `skills:` | which skills are visible | one missing folder name makes that skill invisible |
| `mcps:` | which servers are mounted | expecting `tools:` to grant MCP tools — it does not |
| `subagents` | the delegation topology | an agent that is not reachable from the entry point is never built |
| the body | how the agent works | an empty body is an empty document, and that is a load failure |

## Writing the description

The description is the **dispatch contract**: the duty, when to delegate to it, and where its
boundary ends. A workable shape:

```
<the duty>. <when to delegate to it>. <what it does not do>.
```

- weak: "configuration expert" — a label; the upper model has no trigger to act on.
- usable: "Agent-domain execution expert. Creates and modifies the frontmatter and the Instruction of
  .openheron/agents/*.md, checks the model reference, the topology, the depth and the three lists, and
  reports the key conclusions only. Dispatch to it when an agent has to be created, changed or deleted,
  when the subagents topology changes, or when an agent's skills allow-list has to be synced."

The second one names the directory it owns, the kind of work it takes, and the situations that should
route to it.

## The three allow-lists

They answer three different questions, and all of them are **"only what is listed"** — an absent key
means not a single one, and `[]` means the same:

- `tools:` — which non-MCP tools this agent may call: the built-ins, the ones the files under `tools/`
  provide, and the `config_*` tools. Full names, or a trailing `*` prefix.
- `skills:` — which skill folders this agent may load. Folder names, exactly.
- `mcps:` — which servers this agent mounts. **Mounting hands the agent all of that server's tools**,
  so one line is a wide grant; name only the servers the job needs.

⇒ Start from the least: list a tool only when the agent would be blocked without it. A short list is
easier to reason about and easier to approve.

## The topology

- The entry agent (`main`) is the one that delegates; it must carry `task` in its `tools:`.
- An agent that may not delegate carries `subagents: []` and does **not** name `task` — a `task` entry
  there would promise a capability the topology does not grant.
- The depth cap defaults to 1: level 0 delegates to level 1, and level 1 stops there. A deeper chain
  is a startup error.
- Every `subagents` entry must exist and must not form a cycle. An agent that cannot be reached from
  the entry point is never built — no error, no agent.

## The body of the Instruction

The body is what the agent reads on every turn, so it is a **contract**, not an introduction. A
structure that has held up:

1. **Who you are** — one sentence: the role, and the tree it owns.
2. **Scope** — what it owns, what it must not touch, and where the neighbouring domain lives.
3. **Red lines** — the non-negotiables: one file at a time, read before and after the change, a write
   triggers a human approval, secrets only as references, runtime data is never written.
4. **Before you act** — which skill to load, and which tool to ask for a skeleton.
5. **The procedure** — numbered steps, with the per-field rules next to the step that needs them.
6. **The consistency check** — what to re-verify every time.
7. **The report** — the shape of the answer: result / changes / validation / open points, and nothing
   else.

Keep the body in the language the agents of that project already use; do not mix two languages inside
one file.

## A worked example

A level-1 agent that owns one configuration directory:

```markdown
---
description: <the duty>. <when to delegate>. <the boundary>.
model: <provider>/<model>
max_iterations: 30
subagents: []
tools:
  - ls
  - read_file
  - glob
  - grep
  - write_file
  - edit_file
  - write_todos
  - skill
  - config_get
  - config_validate
  - config_template
  - config_tier
  - config_notices
  - config_write
skills:
  - openheron-config-map
  - openheron-config-edit
  - openheron-config-templates
  - openheron-config-validate
mcps: []
temperature: 0.1
max_tokens: 2048
summarize_at: 80%
---

<body per the structure above>
```

The file name is the id. The agent only becomes usable once it hangs on the entry agent's
`subagents`.

## The closing checklist

| # | Check | Who answers |
|---|---|---|
| 1 | the file name is the id, and every frontmatter key is legal | `config_validate {path, content}` |
| 2 | `model` resolves under `llm/` | the partition table of `config_get {}` |
| 3 | `tools:` is non-empty, no `disabled_tools`, no lone `*` | `config_validate` + `config_tier {agent}` |
| 4 | `skills:` names folders that exist | the partition table + the disk |
| 5 | `mcps:` names servers that exist | `config_get {path: "mcps/<name>.yaml"}` |
| 6 | `subagents` resolves, is acyclic, and is reachable from the entry agent | read the chain |
| 7 | the body is present and non-empty | `config_validate` |
| 8 | the whole tree is still loadable | `config_validate {}` + `config_notices {}` |

A change under `agents/` is **hot**: it takes effect on the next turn, with no restart. ★ Only the
agents that depend on the changed file are rebuilt — an agent that never reads it keeps running the
one it has. `config_get {impact: true, path}` says which ones.

## The traps this skill cannot see

The tools report the hard errors; the silent ones live in `openheron-config-validate`. The ones that
bite an agent definition are: `tools:` omitted, a skill folder missing from `skills:`, an agent
unreachable from the entry point, and a `temperature` overridden by the provider's `extra`.
★ A `reasoning` tier the model does not declare is **not** one of them: that one is a load-time
hard error, and it names the file and the levels the model does declare.
