---
name: openheron-config-edit
description: The operating procedure for changing the openheron configuration. Lists per scenario which files to change, the check points and the way a change takes effect, plus the call order of the six config tools. Load it before proposing a change plan, or before actually editing .openheron.
context: inline
---

# The configuration change procedure

## The general rules (every scenario, and the shared contract of the three execution experts)

`skill-expert` / `agent-expert` / `task` under `main` are three specialized instances of one and the same execution capability: this section treats the three alike, the only difference being the configuration domain; `main` gives all three the same instruction format.

1. Before starting, read the target file with `config_get`; change only the files the instructions name, one at a time; re-read after the change to confirm the **file**. ★ The **tree** this turn runs is fixed: a file written now is on disk but in no tree yet, and it joins on the next turn.
2. An unknown field is a hard error — rather write one key fewer than invent a key.
3. Every duration carries a unit; every model reference carries the `<provider>/` prefix and has been declared in `llm/`.
4. A secret is written only as `${ENV}` / `@file:`, never in plaintext, never echoed back.
5. `state/` is neither changed nor committed.
6. A write triggers a human approval — write the instruction completely, do not patch it up by trial and error.
7. Decide how the change takes effect: a change under `server/` needs a restart, everything else takes effect on the next turn; state it in the plan.
8. After copying a template, keep at least one real key; a file that is only comments = an empty document = a load failure.
9. Execute strictly by the instruction's **goal / scope / constraints**; if something is unclear, report "what is missing" instead of guessing the scope yourself.
10. Act only inside the named configuration domain; when the work crosses domains, report which execution expert should take it over, and do not overstep.
11. Understanding the intent and the trade-offs of a plan are not this skill's job (that is `main`'s); report only the key items.
12. **After the change you must** run `config_validate` + `config_notices` and carry "error / warning / passed / not verified" into the conclusion; without a validation conclusion do not say "it is fixed". ★ `config_validate` reads the disk and `config_notices` answers from the tree this turn runs — say in the conclusion that the new configuration is confirmed on the next turn.

## The tool call order (do not skip a step)

1. `config_get {path}` — get the body and the `revision`.
2. For a new file, get the skeleton first with `config_template {path}` or `{section}`; do not invent fields from memory.
3. **Before writing** `config_validate {path, content}` — clear the line numbers in `issues[]`.
4. **Every write goes through the configuration service.** `config_write {path, content, baseRevision}` does a whole-tree pre-check → revision conflict detection → refusal of plaintext → atomic replacement → trial merge → rollback on failure.
   ★ `write_file` / `edit_file` are **not** a fallback that bypasses it: a write that lands inside the configuration root is **handed to the same service**, so it runs the same gates and comes back with the same refusal. `edit_file` carries the revision it read, so a change between the read and the write is caught; `write_file` carries none, so a whole-file write is "last writer wins" — the gates still run.
   ⇒ Use `config_write` when you have the whole text; use `edit_file` when you are changing one line. Either way the file that lands is checked the same.
5. After the change `config_validate {}` (the whole tree on disk, fail-fast) and `config_notices {}` (the tree this turn runs).
6. Need to confirm a tier → `config_tier {tool}` / `{agent}`.
7. Need to know what a change will reach → `config_get {impact: true, path}`: who depends on the file, and who the next turn rebuilds.

★ `config_write` is always `ask` and cannot write `server/`; `config_get` can read `server/`. When you hit a `server/` change, report "a human has to edit it and restart".

## What a change reaches

★ **A change reaches only the agents that read the file.** The next turn rebuilds the agents whose own dependencies moved — an agent that never reads the changed file keeps running the one it has. Two questions, two tools:

| Question | Call | Answer |
|---|---|---|
| who depends on this file? | `config_get {impact: true, path: "..."}` | `dependents[]` — the agents the file says something about |
| who does the next turn rebuild? | `config_get {impact: true}` | `rebuilds[]` — every agent whose own dependencies moved |
| did this file change anything at all? | the same call | `changed` — a file nobody depends on has not changed in the sense that matters |
| does it need a restart? | the same call | `requiresRestart` — a partition that is not hot-reloaded reaches no agent at all |

⇒ This is the answer to "I changed it and only some agents moved": that is the rule, not a bug. `dependents[]` is **direct** (the agents this file says something about); `rebuilds[]` is **transitive** (an agent's children are part of it, because the parent's `task` tool description carries them).

## Scenario A: the vendors and the models (llm/)

- The provider id = the file name without its extension; `type` (display only), `base_url` and `models[].id` are required.
- A reference is `<provider>/<model>`; an undeclared model = an error at startup.
- Only `kind: embedding` can be used by the knowledge base; `context_window` not written = 0 = automatic compaction never triggers.
- `reasoning` is the **vocabulary**, `reasoning_default` is the default tier; an agent only writes a tier name, and a tier the model does not declare is a **load-time hard error** that names the file and the levels the model does declare.
- `extra` overrides by **top-level key**, it does not deep-merge; do not put only `temperature` / `max_tokens` in it; `header:` does not take effect.
- Effective: hot.

## Scenario B: the agents (agents/)

- The file name is the id; `model` is **required**, it is neither inherited nor fallen back on; `description` goes into the description of the `task` tool.
- `subagents` referring to something that does not exist, or forming a cycle → an error at startup; unreachable from `main` **does not error but is never constructed**.
- ★ **The three lists are "only what is listed"**: `tools` / `skills` / `mcps` not written = not a single one, `[]` says the same thing.
- ★ `disabled_tools` is a **hard error at load time**, and the error explains "renamed to `tools` and the meaning is inverted"; there is no alias, and renaming the key is not a fix.
- ★ `tools:` uses the exposed names and accepts a trailing `*` prefix; a lone `*` / a `*` that is not at the end is a hard error. `main` must carry `task`; a level-1 agent does not carry `task`.
- ★ `tools` governs the **non-MCP** tools — the built-ins, the ones the files under `tools/` provide, and the `config_*` tools — `mcps` governs the **servers**, and mounting a server hands the agent **all of that server's tools**. The two keys do not have to agree: an `mcp__x__*` line in `tools` says nothing and is reported as a notice (drop the line, put the server in `mcps`).
- `temperature` / `max_tokens` are pointers; a `reasoning` tier the model does not declare is a **load-time hard error** (it names the file and the levels the model does declare); `summarize_at` depends on `context_window`.
- The body is the Instruction, there is no `instruction_file`; the delimiters come in pairs, and an empty document is an error.
- The depth `agent.max_depth` defaults to 1, and exceeding it is an error at startup. Effective: hot.

## Scenario C: the tools and the knowledge base (tools/)

- The file name allow-list: `http.yaml` `web_search.yaml` `wikipedia.yaml` `kb.yaml` `filesystem.yaml`; anything else = a hard error at load time.
- `filesystem.yaml` carries only `allows:`, and it **loosens** the tier of the filesystem tools (added onto the code base, it cannot take a name away); it can neither add nor remove tools. Tightening is done through the agent's `tools:`.
- `allows:` names the tools the way the model sees them (`request_get` / `duckduckgo_search` / `wikipedia_search` / `kb_search`, and `write_file` / `edit_file` / `execute` for the filesystem file); `kb_index` and the write methods ask by default. ★ An **MCP** file is the other case: there the names are the **server's own** (`create_issue`), because the exposed name is assembled with the namespace.
- In an MCP file, `tools:` is the filter and `allows:` is the tier exception — two questions, one cannot substitute for the other. ★ The file's `tools:` narrows the server down to **the servers it applies to**; the agent's `mcps:` decides whether the server is mounted at all.
- The parameters: `http.methods` defaults to `[get]`; `web_search.provider` only accepts `duckduckgo`; `kb.model` is required when `corpus_dir` is not empty and has to be `kind: embedding`; `corpus_dir` is relative to the project root, and an absolute path is refused.
- ★ `proxy:` (`http.yaml` and `web_search.yaml`) is the proxy every request goes through. Empty = **no proxy**, and **nothing is read from the environment**. ★ A proxy **replaces** the public-address rule instead of adding to it: with one configured the target may be any address the proxy can reach — an intranet host is not public, and reaching it is what the proxy is for — so the proxy is the boundary. ★ Three settings: `""` (no proxy), a URL (`http` `https` `socks5` `socks5h`; that proxy, and the environment is ignored), and `env` (the environment decides: `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY`, read once when the tool is built). A wrong one is a hard error at load time.
- Effective: hot (including the tier).

## Scenario D: the MCP servers (mcps/)

- The server name = the file name, and it has to match `^[a-z0-9]+(_[a-z0-9]+)*$` (consecutive underscores are not allowed).
- An unknown value of `transport: stdio | http` is a hard error; stdio requires `command`; http requires `url` and **must not** carry `env:` (not even an empty map).
- `allows:` uses the server's own tool names; empty = every tool asks, including the ones added later.
- `command` is resolved in **PATH**, never relative to the project root (on Windows you may need `npx.cmd` / `uvx.exe`);
  the paths in `args` are **not** expanded with `${ENV}`. An `${ENV}` that is not set in `env:` only drops that entry, it does not block the startup.
- The file's `tools:` is a filter (the original tool names) and `allows:` is the tier exception; a **wrong name in either one does not error**,
  it only silently does not take effect — use the `/mcps` panel to check the tool names the server actually provides, do not copy them from memory.
- The server cannot be reached: the tools are not mounted at all plus one warning, and **the process start is not affected**. `timeout` / `call_timeout` are lenient,
  a wrong value falls back to the default and goes into `deferred`.
- The list of the servers that get started is the **union** of the servers listed by all the agents. Effective: hot — but ★ the stdio child belongs to the **manager**, not to the turn: a turn that ends does not kill it, and only a changed **declaration** restarts it (the declaration is what the child's identity is built from; a change somewhere else in the configuration does not touch it). A server that is still starting answers "the server is starting", and its tools join the **next** turn.

## Scenario E: the process itself (server/)

- The fields: `listen_addr` (quoting `:8080` is recommended), `frontend_mode`, `static_dir` (project root), `data_dir` (config root), `start_timeout`, `lease_ttl`, `retry.max_retries`, `agent.max_depth`, `watch.*`.
- `lease_ttl` has to be longer than the provider timeout; `watch.enabled` not written = on, only `false` turns it off; `retry.max_retries: 0` still follows the default.
- ★ **A restart is mandatory after the change**; `config_write` refuses to write this section, so it has to be changed by hand. The environment variable overrides are also read once, at startup.

## Scenario F: migration (old configuration)

| Old spelling | Now | Symptom |
|---|---|---|
| `disabled_tools:` | `tools:` (**the meaning is inverted**) | an error at startup, and the message explains the rename and the inversion; no alias |
| a missing `tools:` (in the past = everything was granted) | an explicit allow-list | **silent, and it bites the hardest**: an empty tool table, the agent cannot do anything |
| a missing `mcps:` / `skills:` (in the past = all of them) | explicit lists | not silent, but everything that used to be inherited is lost |
| `kb/config.yaml` | `tools/kb.yaml` | **silent**: `kb_search` disappears ⇒ `mv` first, then start |
| the `tools:` section of `server/config.yaml` | split into `tools/*.yaml` | the error `field tools not found` |
| `policy/permissions.yaml` / `limits.yaml` | the `allows:` of each tool file / the limits in `server/config.yaml` | not silent: the retired directory goes into `deferred` |
| the `corpus` of `kb.yaml` | `corpus_dir` | the old key is still loaded, and it goes into `deferred` |

## Scenario G: the skills (skills/)

- `skills/<name>/SKILL.md` is scanned one level deep only; `name` == the directory name; `description` says "when to load me".
- `context: inline` is the default, a typo silently becomes inline; in this project it is always inline, and `agent:` / `model:` are not written.
- The body is the injected content, embedded in the template and self-contained; keep at least one piece of real content, an empty document is an error.
- **The directory is changed on the skill side, the allow-list on the agent side**: after a directory is added or removed, sync the `skills:` of every agent, and issue the two as separate instructions.
- Effective: `skills/` is a hot partition — a content change reaches the agents that name that skill and takes effect on the next turn, no restart.

## Scenario H: the troubleshooting order

1. "It did not take effect" → first `config_notices {}` to see `deferred` and the two versions: `diskVersion` (the disk) against `runningVersion` (the tree this turn runs). They differ for as long as a hot change has not been picked up — that difference **is** the answer to "has my change landed". Then check whether it is `server/` (a restart is needed).
2. "A startup error + a line number" → an unknown field / a leftover `disabled_tools` / a missing unit / an empty document / a model reference / a cycle or an overrun / an `mcps` server that does not exist / a `reasoning` tier the model does not declare.
3. "The tool is gone" → for a non-MCP tool: whether the agent's `tools:` names it, the file name allow-list, the old `kb/` path. For an MCP tool: whether the agent's `mcps:` mounts the server, and whether the server's own file offers that tool.
4. "The behavior is odd but nothing errors" → the 200ms fallback, the single-key override of `extra`, the no-op `header:`, an omitted `tools:`.
5. "I changed it but the old behavior still runs" → the two versions in `config_notices`; a failed reload keeps the last good configuration, so do not take "it did not crash" for "the change is right".
6. "I changed it and only some agents moved" → that is the rule, not a bug: a change reaches the agents whose own dependencies moved. `config_get {impact: true, path}` says which ones, and `config_get {impact: true}` without a path says who the next turn rebuilds.
