---
name: openheron-config-map
description: A factual map of the openheron configuration system. Covers the directory layout, the config root and the two path baselines, strict mode and deferred, the three allow-lists, the tier model, the hot and cold partitions, the six config tools and the known traps. Load it when answering what the configuration is, which key owns this, or why it is designed this way.
context: inline
---

# The openheron configuration map

This skill gives facts and rules only. Ask for the concrete values with `config_get` / `config_notices` / `config_tier` — do not rely on memory.

## Directories (under the config root)

```
agents/<id>.md         agent definition (frontmatter + the body is the Instruction), main is the default agent
llm/<provider>.yaml    the vendor's profile: base_url / api_key / models[]
server/config.yaml     the process itself: listening, directories, timeouts, caps (the cold partition)
tools/<tool>.yaml      http / web_search / wikipedia / kb / filesystem
mcps/<server>.yaml     one MCP server per file
skills/<name>/SKILL.md a skill (only the first level of subdirectories is scanned)
state/                 runtime data, never committed
```

There is no `policy/` partition; a leftover directory of that name is not read, it only lands in `deferred`. Empty subdirectories are not created automatically; only the config root derived by default is created when it is missing, and an explicitly named root that does not exist is a typo.

## The config root: three levels, no probing

`--config-dir` → `$OPENHERON_CONFIG_DIR` → `<projectRoot>/.openheron`. **There is no upward search**, and the home directory is not a fallback either. The project root is `--project-root` or the cwd, and it is decoupled from the config root.

Do not mix the two baselines: relative to `data_dir` → **the config root**; relative to `static_dir` / `corpus_dir` → **the project root**. The typical failure of mixing them is a frontend 404, or an agent grepping inside `.openheron`.

## Loading and "configured but not effective"

Prefill the struct → decode file by file (yaml.v3 writes only the keys that appear in the document) → a few environment variables override at the point of use (read once at startup). The fields of `llm/` and `agents/` have no environment variable override.

- Strict mode `KnownFields(true)`: an unknown field is a hard error **with a line number**; an empty document / comments only → `ErrEmptyDocument`.
- Three shapes: a key that is not recognized = a hard error; recognized but not honored = `deferred`; recognized but missing a runtime precondition = `deferred` and it does not block the startup.
- `${ENV}` not set: only a warning, a 401 at request time; an `@file:` path that cannot be read: a hard error.
- Two tiers of duration: the `timeout` of `llm/*.yaml` is a strict error; a lenient duration (`debounce` / `start_timeout` / `lease_ttl` / the `tools/` timeouts) falls back to **200ms** when it is written wrong, and the `deferred` message **always** says `watch.debounce`. ⇒ Always write the unit.

## The three allow-lists (★ the semantics flipped on 2026-11-08)

**Only what is listed is there.** `tools` / `skills` / `mcps` are all `[]string`; **not written = not a single one, `[]` says the same thing**. The pointer that told "not written" from "written empty" has been deleted.

| Key | What it governs |
|---|---|
| `tools` | Which tools this agent **sees** (the exposed names); it accepts a trailing `*` prefix such as `mcp__github__*` |
| `mcps` | Which servers are **mounted** (child processes are started) |
| `skills` | Which skills can be loaded (the folder names under `skills/`) |

- ★ `disabled_tools` is a **hard error at load time**, not a rename: the old key is still parsed, only so that it can report "renamed to `tools` and the meaning is inverted". **There is no alias**.
- ★ An agent that omits `tools:` has an empty tool table; a `main` that omits `task` "talks but does nothing".
- ★ MCP has two levels: `mcps: [github]` only says "start it up"; `tools:` must also carry `mcp__github__*` for it to be visible. The two layers contradicting each other (the prefix is named but the server is not mounted) is a load-time error.
- A lone `*`, or a `*` that is not at the end, is a hard error at load time; `tools: ["*"]` is not allowed.
- A server name must match `^[a-z0-9]+(_[a-z0-9]+)*$`, consecutive underscores are not allowed; the exposed name `mcp__<server>__<tool>` is assembled in exactly one place.

## The tiers: there is no global list

- The default is **`ask`**. The code's own `builtinAllowedTools` (`ls read_file glob grep task write_todos skill`) **always takes effect** and is the base; the `allows:` of each tool file can only **add upward**, it cannot take a name away.
- To tighten, change that table in the code; to loosen, write `allows:` in the file that provides the tool (using that file's own vocabulary).
- `tools/filesystem.yaml` **is implemented**: it carries only `allows:`, and it is the **tuning knob** for loosening the tier of `ls` / `read_file` / `write_file` / `edit_file` / `glob` / `grep` / `execute`, not a switch — the filesystem tools are always there.
- ★ `config_write` is **always ask**: `alwaysAsk` strips it out of any `allows:` after the merge. A file that can authorize a tool cannot authorize the tool that "rewrites itself".
- The tier (`allow` / `ask`) and the agent's `tools:` list are **two questions**: the first is shared, the second is per agent. A tool can be `allow` and still be invisible to some agent.

## The six config tools (built in, still to be named in `tools:`)

| Tool | What it answers | Tier |
|---|---|---|
| `config_get` | `{path}` reads a single file (with revision / hot); `{}` the partition table, the file list, the configuration in force | allow |
| `config_validate` | `{path, content}` whether it can land, and on which line the error is; `{}` checks the whole tree on disk | allow |
| `config_template` | `{path}` / `{section}` gives the skeleton; with neither you get the index | allow |
| `config_notices` | `deferred[]` / version / configVersion / watch | allow |
| `config_tier` | the tier of `{tool}` / `{agent}`; with neither you get the default and the whole exception table | allow |
| `config_write` | an atomic write that carries `baseRevision` | **ask** |

★ `config_write` **cannot write `server/`** (the write gate `ValidatePath` refuses it), but `config_get` **can read it** (the read gate `ValidateReadPath` only cares about the shape). The two gates are not the same thing.
★ `config_validate {}` is fail-fast: it gives only the first blocking error plus every hint, not the full list of problems.
★ `config_get` does not leak secrets (the configuration only holds `${ENV}` / `@file:` references); over 64 KiB it **refuses** outright instead of truncating.
★ `config_write` returns `applied` / `requiresRestart` — it takes effect at the next `PrepareAgent`; writing the agent itself is allowed too.

## Skills (skills/) and hot vs. cold

- Directory lookup: `$EINO_EXT_SKILLS_DIR` wins, otherwise search upward from the cwd for `.openheron/skills` — this one **does** search upward, unlike the config root.
- The frontmatter is **eino's schema**: `name` / `description` / `context` / `agent` / `model`. `name` matches the directory name; `description` says "when to load it".
- `context: inline` is not a declared constant; a typo does not error, it silently becomes inline. Only `fork` uses `agent:` / `model:`, and a reference that cannot be resolved is an error.
- Hot: `llm/` `agents/` `tools/` `mcps/`; cold: `server/` (a change needs a restart). `skills/` is not in `HotDirs`; a content change takes effect after the agent pool is rebuilt.
- A broken configuration does not kill the process: a failed reload keeps the last good configuration; only a failure on the first load makes the startup fail.

## Known traps (check these first)

- A leftover `disabled_tools` / an omitted `tools:` — the former errors at load time, the latter silently becomes zero tools.
- The `header:` of `llm/<provider>.yaml` is parsed but **never consumed**.
- When provider-level `extra` happens to hold only `temperature` or `max_tokens`, it overrides the agent's value of the same name.
- `agents[].reasoning` is not in the model's vocabulary → silently ignored, **and not reported**; the loader only checks the files under the `validate()` path.
- `models[].summarize_at` is deprecated; the threshold belongs to the agent, and the denominator is the model's `context_window`.
- The old `kb/config.yaml` is not read and is **silent** (`kb_search` disappears without an error); the migration has to `mv` first and start afterwards.
- Every `.yaml` under `server/` is decoded into the same struct, with no file name allow-list — `server/typo.yaml` is merged into the configuration.
- The factory template comments outrun the code: `web_search.provider` only accepts `duckduckgo`; `kb.model` is required when `corpus_dir` is not empty.
