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
llm/<provider>.yaml    the vendor's profile: base_url / models[] (the api key is not in the file)
server/config.yaml     the process itself: listening, directories, timeouts, caps (the cold partition)
tools/<tool>.yaml      http / web_search / wikipedia / kb / filesystem / config
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
| `tools` | Which **non-MCP** tools this agent may use — the built-ins, the ones the files under `tools/` provide, and the `config_*` tools (the exposed names); it accepts a trailing `*` prefix |
| `mcps` | Which servers are **mounted** (child processes are started) — and a mounted server brings **all of its tools** |
| `skills` | Which skills can be loaded (the folder names under `skills/`) |

- ★ `disabled_tools` is a **hard error at load time**, not a rename: the old key is still parsed, only so that it can report "renamed to `tools` and the meaning is inverted". **There is no alias**.
- ★ An agent that omits `tools:` has an empty tool table; a `main` that omits `task` "talks but does nothing".
- ★ `tools` and `mcps` answer **different questions**: `tools` is the allow-list of the tools that are not MCP tools, `mcps` is the servers, and **mounting a server hands the agent all of that server's tools**. The two keys do not have to agree. An `mcp__<server>__*` line in `tools:` says nothing — it is reported as a **notice**, so the file still loads.
- ★ Mounting is not approving: the tier of an MCP tool comes from the `allows:` of the server's own file, and the default there is `ask` too.
- A lone `*`, or a `*` that is not at the end, is a hard error at load time; `tools: ["*"]` is not allowed.
- A server name must match `^[a-z0-9]+(_[a-z0-9]+)*$`, consecutive underscores are not allowed; the exposed name `mcp__<server>__<tool>` is assembled in exactly one place.

## The tiers: there is no global list

- The default is **`ask`**. The code's own `builtinAllowedTools` (`ls read_file glob grep task write_todos skill`) **always takes effect** and is the base; the `allows:` of each tool file can only **add upward**, it cannot take a name away. ★ The `config_*` tools are **not** in that list: their file, `tools/config.yaml`, is what says which of them run without a nod — absent, all six ask.
- To tighten, change that table in the code; to loosen, write `allows:` in the file that provides the tool (naming the tool the way the model sees it: `request_get` in `tools/http.yaml`, `write_file` in `tools/filesystem.yaml`, `config_get` in `tools/config.yaml`). ★ An **MCP** file is the other case: there the names are the **server's own** (`create_issue`), because the exposed name is assembled with the namespace.
- `tools/filesystem.yaml` **is implemented**: it carries only `allows:`, and it is the **tuning knob** for loosening the tier of `ls` / `read_file` / `write_file` / `edit_file` / `glob` / `grep` / `execute`, not a switch — the filesystem tools are always there.
- ★ `config_write` is **not** exempt: `tools/config.yaml` grants it like any other name, and the file that grants it is among the files it edits. ★ The default template does not name it — a question about the configuration should not cost an approval, and a change to it should — so it asks until someone writes its name down.
- The tier (`allow` / `ask`) and the agent's `tools:` list are **two questions**: the first is shared, the second is per agent. A tool can be `allow` and still be invisible to some agent.

## The six config tools (built in, still to be named in `tools:`)

| Tool | What it answers | Side | Tier |
|---|---|---|---|
| `config_get` | `{path}` reads a single file (with revision / hot); `{}` the partition table, the file list, the configuration in force; `{impact: true}` answers "who depends on this file, and who the next turn rebuilds" | `disk` for a file's text, `running` for `impact`; the overview carries both versions | `allow` when `tools/config.yaml` names it, `ask` otherwise |
| `config_validate` | `{path, content}` whether it can land, and on which line the error is; `{}` checks the whole tree on disk | `disk` | `allow` when `tools/config.yaml` names it, `ask` otherwise |
| `config_template` | `{path}` / `{section}` gives the skeleton; with neither you get the index | neither — a skeleton is neither a file nor a tree | `allow` when `tools/config.yaml` names it, `ask` otherwise |
| `config_notices` | `deferred[]` / `runningVersion` / `diskVersion` / `watch` | `running` | allow |
| `config_tier` | the tier of `{tool}` / `{agent}`; with neither you get the default and the whole exception table | `running` | `allow` when `tools/config.yaml` names it, `ask` otherwise |
| `config_write` | an atomic write that carries `baseRevision`; the answer carries `revision` / `runningVersion` / `diskVersion` / `applied` / `requiresRestart` | it wrote the disk, and it reports both versions | **`ask`** — the default file does not name it; `tools/config.yaml` can |

★ **The answers name the side they came from.** `config_get {path}`, `config_validate` and `config_get {impact}` carry a `source` field: `disk` (the files on disk right now) or `running` (the tree this turn runs). The two that report a *state* rather than a *question* — `config_get {}` and `config_notices` — carry the two versions instead: `runningVersion` (the tree this turn runs) and `diskVersion` (the files on disk). A model that has just written a file can see that the disk moved and the running tree did not, instead of reading two numbers that differ by one letter.
★ `config_write` **cannot write `server/`** (the write gate `ValidatePath` refuses it), but `config_get` **can read it** (the read gate `ValidateReadPath` only cares about the shape). The two gates are not the same thing.
★ `config_validate {}` is fail-fast: it gives only the first blocking error plus every hint, not the full list of problems.
★ `config_get` does not leak secrets (the configuration only holds `${ENV}` / `@file:` references); over `max_read_bytes` (64 KiB by default, set in `tools/config.yaml`) it **refuses** outright instead of truncating.
★ `config_write` returns `applied` / `requiresRestart` — it takes effect at the next turn; writing the agent itself is allowed too. ★ `applied: true` means the watcher will pick the file up, **not** that this turn can see it.
★ **The config tools answer from two places.** The **file** answers (`config_get {path}`, `config_validate {path, content}`) read the disk, so a write is visible there at once. The **tree** answers (`config_tier`, the `effective` summary of `config_get {}`, the `deferred` of `config_notices`) come from the tree the turn was built with — a file written in this turn is on disk but in no tree yet, and it joins on the next turn.

## Skills (skills/) and hot vs. cold

- Directory lookup: `$EINO_EXT_SKILLS_DIR` wins, otherwise search upward from the cwd for `.openheron/skills` — this one **does** search upward, unlike the config root.
- The frontmatter is **eino's schema**: `name` / `description` / `context` / `agent` / `model`. `name` matches the directory name; `description` says "when to load it".
- `context: inline` is not a declared constant; a typo does not error, it silently becomes inline. Only `fork` uses `agent:` / `model:`, and a reference that cannot be resolved is an error.
- Hot: `llm/` `agents/` `tools/` `mcps/` `skills/`; cold: `server/` (a change needs a restart). `skills/` is a hot partition too — the dependency walk reads the skill's own file, so editing a `SKILL.md` reaches the agents that name it and takes effect on the next turn.
- A broken configuration does not kill the process: a failed reload keeps the last good configuration; only a failure on the first load makes the startup fail.

## Known traps (check these first)

- A leftover `disabled_tools` / an omitted `tools:` — the former errors at load time, the latter silently becomes zero tools.
- ★ The HTTP and search tools do **not** pick up the environment's proxy on their own: `HTTP_PROXY` / `HTTPS_PROXY` reach the **model** call, not the tool. A tool that cannot leave the machine is fixed by naming the proxy in `http.yaml` / `web_search.yaml` (`proxy:`) — and a proxy **replaces** the public-address rule rather than adding to it, so the target may then be any address the proxy can reach. ★ The environment can be asked for **by name**: `proxy: env` makes the tool read `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` — and it reads them once, when the tool is built, so a later change of the environment does not move the route.
- An agent written in this very turn answers "not in this turn's configuration yet" — **the write succeeded**; the tree catches up on the next turn, and the error says exactly that. Writing the file again is the mistake.
- The `header:` of `llm/<provider>.yaml` is parsed but **never consumed**.
- When provider-level `extra` happens to hold only `temperature` or `max_tokens`, it overrides the agent's value of the same name.
- `agents[].reasoning` is not in the model's vocabulary → a **load-time hard error**, and it names the file and the levels the model does declare. The vocabulary belongs to the model, not to the agent: a level the model does not carry means the request goes out with no reasoning configured at all.
- `models[].summarize_at` is deprecated; the threshold belongs to the agent, and the denominator is the model's `context_window`.
- The old `kb/config.yaml` is not read and is **silent** (`kb_search` disappears without an error); the migration has to `mv` first and start afterwards.
- Every `.yaml` under `server/` is decoded into the same struct, with no file name allow-list — `server/typo.yaml` is merged into the configuration.
- The factory template comments outrun the code: `web_search.provider` only accepts `duckduckgo`; `kb.model` is required when `corpus_dir` is not empty.
