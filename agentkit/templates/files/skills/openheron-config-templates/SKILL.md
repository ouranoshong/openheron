---
name: openheron-config-templates
description: How to get a configuration skeleton and how to change it. Load it when creating or rewriting a file under .openheron, or when a minimal valid agent definition has to be given. The skeleton itself is provided by the config_template tool; this skill only covers the rules of use.
context: inline
---

# The configuration skeletons

## Ask the tool, do not copy a template

The **only exit for a skeleton is `config_template`**, and this skill embeds no file body any more — an embedded template drifts with the code,
while the tool asks `spec.TemplateFor` / `Templates` / `Skeleton`, which is the same answer that `openheron init` and the new-file dialog get.

| What you want to ask | How to call it | What you get |
|---|---|---|
| What skeleton a specific file starts from | `{path: "agents/main.md"}` | `{path, content}` |
| Which templates a partition has | `{section: "tools"}` | `{section, templates}` |
| What can be asked | `{}` | the index |

★ The file template wins over the partition template; the manifest has already settled this, so do not implement a fallback yourself.
★ `llm/` is the **fallback** (the provider's own template + a generic catch-all), the manifest carries no `llm/` template, and it is still read from the templates package.
★ Only one partition is served at a time; a `config_get` over 64 KiB is refused instead of truncated.

## After you have the skeleton

- **Delete the pure comment lines** after copying, and keep at least one real key: a file that is only comments = an empty document = `ErrEmptyDocument`.
- `<...>` is a placeholder and has to be replaced; the `<provider>/<model>` of `model` has to be declared in `llm/`.
- Every duration carries a unit (`5m` / `60s` / `200ms`); do not add keys that the template does not list, strict mode refuses them.
- `api_key` may only be written as `${ENV_VAR}` or `@file:/path`.
- The field names and the rules of effect follow `openheron-config-map`, the way to change them follows `openheron-config-edit`;
  once it is filled in, run `config_validate {path, content}` before `config_write`.

## The one skeleton worth remembering

`tools:` is closed by default, and omitting it means zero tools — this is the most common stumble in a migration, so here is a minimal agent:

````markdown
---
description: <the duty + when to delegate; it goes into the description of the task tool>
model: <provider>/<model>
subagents: []
tools: [ls, read_file, glob, grep, write_todos, skill, config_get, config_validate]
skills: []
mcps: []
---

<the body is the Instruction>
````

- `tools` / `skills` / `mcps` **are all allow-lists**: not written = not a single one, `[]` says the same thing.
- `disabled_tools` is a hard error at load time and must never appear in a skeleton.
- The `tools:` of the entry agent **must carry `task`**, otherwise it "talks but does nothing"; a level-1 agent does not carry `task`.
- To use MCP, mount the server: `mcps: [github]` — and that is the whole step, because **mounting hands the agent all of that server's tools**. The agent's `tools:` names the tools that are **not** MCP tools, so an `mcp__github__*` line does not belong there.

## The skeleton of a SKILL.md

What `config_template {path: "skills/<name>/SKILL.md"}` hands you is the eino frontmatter:
`name` / `description` / `context` / `agent` / `model`. The points:

- Only the first level of subdirectories is scanned; `name` has to match the directory name.
- `description` says "on what task / in what situation to load it", not a summary of the content — the model looks only at it to decide whether to pick the skill.
- `context` only ever says `inline`; do not write `agent:` / `model:`, unless a hub is really configured. A typo does not error, it silently becomes inline.
- The body is the content **injected into the executor**: lists, tables, a skeleton to copy; do not write an introduction of "what this skill is".
- Keep at least one piece of real body text; the template is embedded and is not split into auxiliary files.
