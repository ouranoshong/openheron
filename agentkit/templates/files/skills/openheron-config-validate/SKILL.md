---
name: openheron-config-validate
description: The closing end of configuration validation. Covers how to ask the six config tools, the list of the silent failures the tools cannot find, and how to report the validation conclusion. Load it before reporting a conclusion after a configuration change, or when the user asks to check the configuration, why it did not take effect, or whether it can start.
context: inline
---

# The configuration validation

## The validation has already been answered by the tools

**Do not rewrite the rules here, and do not write a static pre-check script.** The server's `Diagnostics` / `Check` carry
`Path` / `Line` / `Severity` / `Message`, the `KnownFields` error of `yaml.v3` carries a line number by itself,
and the offset of the frontmatter is corrected by `parseShift` — all of these are what `config_validate` returns.

| Step | Call | What to look at |
|---|---|---|
| 1 | `config_validate {path, content}` | clear the `issues[]` before the write (`Line: 0` means "no line", not line 0; the message of a cross-file rule names the file) |
| 2 | `config_validate {}` | the whole tree on disk. **fail-fast**: only the first blocking error plus every hint, not the full list of problems |
| 3 | `config_notices {}` | `deferred[]` / `version` / `configVersion` / `watch` |
| 4 | `config_tier {tool}` or `{agent}` | the tier and the effective tool table (the result after `alwaysAsk`) |
| 5 | `config_get {}` | the partition table, the file list, **the configuration the process holds** |

★ A parse failure has no hints (the hints hang off the `*Config`), but the collection of the notices is ordered **before** the hard rules — one failing rule does not swallow the "you probably did not mean this".
★ `config_write` comes with its own whole-tree pre-check and `baseRevision` conflict detection; after a refused write the file is byte for byte the same as before the write.
★ When only one file is changed, the single-file validation is already complete; the fail-fast of `{}` is a known trade-off.

## What the tools cannot find (the thing this skill really keeps)

**Silent failures**: they do not error, they do not block the startup, and `config_validate` may even say `ok`. These have to be checked by a human.

| Symptom | The real cause |
|---|---|
| the agent cannot call a single tool | **`tools:` omitted** — the allow-list is closed by default, an empty tool table; a `main` that omits `task` cannot delegate |
| the skill is completely invisible | the agent's `skills:` allow-list is missing the directory name (the allow-list filters both `List` and `Get`) |
| the MCP server runs but its tools are invisible | the agent mounts it, so the **server's own file** decides: its `tools:` filter does not offer that tool (a wrong name does not error), or the server was never reached |
| the MCP tool asks for a nod every time although `allows:` was configured | the name in the file does not match the tool name the server really provides — **a wrong name does not error**, check it with `/mcps`; the same goes for the `tools:` filter |
| the MCP server never starts at all | it is not named by any agent's `mcps:`; the file being there only "defines" it, mounting it is what starts the process |
| a tier is written and nothing thinks at all | `agents[].reasoning` is not in the model's vocabulary — silently ignored, **and there is no deferred** |
| `kb_search` disappears and the log says not a word | the old `kb/config.yaml` is not in a loaded partition; the migration has to `mv` first and start afterwards |
| the `temperature` in the request is not the value the agent wrote | the provider-level `extra` happens to hold only `temperature` / `max_tokens` |
| `header:` is configured and nothing happens | it is parsed and then never consumed |
| the timeout / the lease suddenly becomes 200ms | a lenient duration falls back to `DefaultDebounce`; the `deferred` message **always** says `watch.debounce`, no matter which key was actually written wrong |
| the request returns 401 | `${ENV}` is not set: it only goes into `deferred`, and the failure moves to the moment the endpoint is called |
| the model never compacts | `context_window` is not declared, or `summarize_at` is written on the model side (deprecated) |
| a certain agent never ran | unreachable from `main` — it does not error, but it is never constructed |
| `context: fork` does not take effect | a typo in `context` does not error, it silently becomes inline; `inline` is not a declared constant |
| the template and the code disagree | the factory template comments outrun the code: `web_search.provider` only accepts `duckduckgo`; an empty `kb.model` is not a pure lexical retrieval |
| "it did not crash, so it is right" | a failed reload **keeps the last good configuration** and writes only one log line; the retired `policy/` directory only goes into `deferred` |
| an odd value under `server/` | `server/typo.yaml` is merged into the same struct — this section has no file name allow-list |
| an MCP tool is missing | the assembled name exceeds 64 characters or collides after the sanitizing: it is skipped plus a warning, **not** a load failure |

## The hard errors at load time (for reading `issues[]`)

A leftover `disabled_tools` (**renamed to `tools` and the meaning is inverted, no alias**), `tools: ["*"]` or a `*` that is not at the end,
`mcps` naming a server that does not exist,
an unknown field with a line number, an empty document / comments only, a bare number in `timeout: 60`, a `model` without a prefix or with an undeclared reference,
`subagents` in a cycle / over the limit / referring to something that does not exist, a missing `kb.model`, an unknown file name under `tools/`,
a `mcps` server name that does not match the regex, an http server carrying `env:`, an `@file:` that cannot be read.

★ An `mcp__x__*` line in `tools:` is **not** on this list: it is a **notice** (the tools list is about the built-in tools, and `mcps:` is what mounts a server and gives the agent its tools).

## The runtime confirmation

1. The `deferred` and the `configVersion` of `config_notices {}` — confirm that what is running really is the one you have just changed.
2. For a change in a hot partition, watch whether the generation change happens (on a failure `onChange` is not called and the old configuration keeps serving).
3. A change under `server/` is **verified after the restart**; `config_write` does not write this section at all, it has to be changed by hand.
4. A content change under `skills/` takes effect after the agent pool is rebuilt.
5. The `applied` / `requiresRestart` in the `config_write` return say whether this change really took effect — it takes effect at the next `PrepareAgent`.

## The output contract

Report only the key conclusions, do not paste the whole file and do not list the checking process:

1. **Error**: `file path:line number` + one sentence on the cause + what it blocks (the startup / the load / the generation change).
2. **Warning**: the `deferred` entries and the silent failures listed above, pointing out that they "do not error but will not take effect".
3. **Passed**: one sentence, naming the way it was verified (single file / whole tree / notices / after a restart).
4. **Not verified**: what genuinely cannot be confirmed (the reachability of the endpoint, whether the secret was injected, the blocking errors that follow beyond `{}`), marked clearly and not glossed over.
