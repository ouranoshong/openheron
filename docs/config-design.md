# Configuration System Design

> Status: this document describes **openheron's configuration system as it is**. The `mcps/` partition is **implemented** (§6.7), the **prefix pattern in an agent's `disabled_tools`** is implemented (§6.3), and so is the **retirement of the `policy/` partition**: the default tier is `ask`, and the exceptions are named by the file that **provides** the tool (§6.3, §6.6). ★ There is **no global tier list** any more — "not exposed" is said in exactly two places. Documents that live in the upstream repository are marked "upstream".
> Scope: in scope — the config root, the partitions, the file specs, loading and precedence, hot reload, and the dependency iron rules. Out of scope — the HTTP API surface (see `docs/api-v2-design.md` (upstream)) and the frontend.
> Framework versions: eino `v0.9.22` (`go.mod:9`), `gopkg.in/yaml.v3 v3.0.1` (`go.mod:20`).

---

## 0. One sentence

**Configuration and runtime data live apart**: the configuration is readable, reviewable files (whether they enter version control is your call), runtime data never enters the repository; vendor knowledge goes into `llm/*.yaml` and the Go code carries none of it; **exactly one configuration is in effect**, so "what this configuration actually is" is recognizable at a glance.

★ The reason there is only one is not tidiness: a config root that depends on "where the process was started from" makes the same command read a different configuration in another directory, and makes a test instance read the developer's own configuration (`agentkit/spec/config.go:161-173`).

---

## 1. Goals and non-goals

### Goals

| # | Goal | Verifiable criterion |
|---|---|---|
| 1 | **One** config root (project-level path, or explicitly named) | At any moment exactly one configuration is in effect, and there is no inheritance chain (`agentkit/spec/config.go:161-167`) |
| 2 | Partition by function | Adding one kind of configuration does not change the existing file layout (`agentkit/spec/config.go:40-70`) |
| 3 | Zero new dependencies | `go.mod` only promotes `gopkg.in/yaml.v3` to a direct dependency (`go.mod:20`) — the YAML layer is the entire cost |
| 4 | "Configured but not in effect" must be visible | An unknown field is an error; a known-but-unfulfilled key goes into `deferred` and is **printed** (`agentkit/spec/config.go:415-425`, `main.go:198-200`) |
| 5 | Secrets stay out of the repository | A profile stores only a `${ENV}` reference or an `@file:` path — the config editor refuses to save a literal (`agentkit/spec/expand.go:9-38`, `agentkit/spec/write.go:441-533`) |

### Non-goals

- ❌ **No upward search for the config root.** The upstream design searched ancestor directories; openheron does not, on purpose (`agentkit/spec/config.go:161-173`).
- ❌ **No home-directory layer.** `~/.openheron` is never picked up on its own — if one exists and is not the root in use, the runtime says so and asks for it to be named (`main.go:109-126`).
- ❌ **The configuration does not carry the secret itself.** The loader tolerates a literal key (`agentkit/spec/expand.go:35-37`), but the editor rejects one (`agentkit/spec/write.go:441-533`) — the two paths are deliberately not symmetric.
- ❌ **`server/` does not take part in hot reload** (it configures the process itself, so a change needs a restart). `llm/` `agents/` `tools/` `mcps/` **do** hot-reload — see §8. ★ The **depth and retry caps** live in `server/` for exactly that reason: there is one copy of each number, and it is not swapped at runtime (§12.2).

---

## 2. Decision baseline

| # | Item | Decision | Reason |
|---|---|---|---|
| 1 | Directory name | `.openheron` | short, unambiguous, may enter version control (`agentkit/spec/config.go:105`) |
| 2 | Partitions | `server/` `llm/` `agents/` `tools/` `mcps/` (loaded), `skills/` (skills), `state/` (runtime data) | one file set per kind of configuration (`agentkit/spec/config.go:18-26`, `agentkit/spec/config.go:40-70`) |
| 3 | Format | **YAML** — plus YAML frontmatter for `agents/*.md` | isomorphic to the agents' frontmatter ⇒ the whole `.openheron/` speaks one dialect (`agentkit/spec/expand.go:48-65`, `agentkit/spec/load.go:72-95`) |
| 4 | Granularity | **one entity, one file** | `llm/<provider>.yaml`, `agents/<id>.md` (`agentkit/spec/load.go:17`, `agentkit/spec/load.go:72`) |
| 5 | Overriding | not applicable — there is only one configuration, so there is no "who overrides whom" | a field-level merge makes the source of a value unreadable. ★ There is **no** "the stricter of the two" rule any more: the depth and retry caps have **one** home (`server/config.yaml`), so there is nothing to reconcile (`agentkit/spec/load.go:671-684`) |
| 6 | Precedence | built-in defaults < config root < environment variables (★ **only a handful of variables really do this**, see §4.4) | an env var is a one-off override, a file is the project's own management under version control (`main.go:217-219`) |
| 7 | Unknown fields | **hard error, with the line number** | `max_retry` misspelled as `max_retries` must surface at startup (`agentkit/spec/config.go:415-425`, `yaml.v3@v3.0.1/decode.go:944`) |
| 8 | Runtime data | `.openheron/state/` | the configuration and the data share one root, so one ignore rule covers both (`agentkit/spec/defaults.go:62`, `agentkit/spec/config.go:241-253`) |
| 9 | skills | `.openheron/skills/` by default — resolved by `$EINO_EXT_SKILLS_DIR`, otherwise by an upward search for `.openheron/skills` | ★ this is a **different rule** from the config root: a skill is a directory tree, and reusing one from a parent directory is harmless, whereas the config root is a set of entities (`agentkit/build/skill.go:188-227`) |
| 10 | Root resolution | `--config-dir` → `$OPENHERON_CONFIG_DIR` → `<projectRoot>/.openheron` | ★ **no upward search** — a root that depends on the working directory makes the same command read a different configuration (`agentkit/spec/config.go:161-173`) |
| 11 | Multiple models | one provider file holds `models[]`, referenced as `<provider>/<model>` | "a strong model for the entry agent, weak models for the sub-agents" is the commonest ask; it also matches the `model:` reference form of `agents/*.md` (`agentkit/spec/load.go:53-65`, `agentkit/spec/load.go:437-446`) |
| 12 | Model construction | **one instance per agent** (granularity = provider × model × reasoning tier × endpoint field shape × timeout) | ★ **corrected during implementation**: the original text said "one instance per provider", on the grounds that "the connection pool is naturally shared". The second half does not hold — `FromConfig` builds a new `http.Client` every time (`agentkit/build/provider.go:40`, `agentkit/build/provider.go:47`), and each agent declares its own model and reasoning tier in the first place, so they are independent to begin with (`agentkit/build/provider.go:81-95`) |
| 13 | Durations in two tiers | the `timeout` in `llm/*.yaml` is a hard error; `debounce` / `start_timeout` / the `tools/` timeouts fall back and are recorded in `deferred` | a wrong `timeout` makes the timeout behaviour unpredictable; a wrong `debounce` only affects "how long to wait" and must not stop the program. **The fallback must be audible** (`agentkit/spec/decl/decl.go:584`, `agentkit/spec/decl/decl.go:509-518`, `agentkit/spec/reload.go:80-84`) |
| 14 | Sub-agent spec | lives in `decl.SubAgentSpec`, the assembly layer uses it directly | `agentkit/build → agentkit/spec` is an allowed one-way dependency anyway; a second copy would "compile fine and be one value short at runtime" (`agentkit/spec/decl/decl.go:613-631`, `agentkit/spec/subagent.go:18-28`, `agentkit/build/spec.go:22`) |
| 15 | MCP tool naming | every name exposed to the model is namespaced `mcp__<server>__<tool>` | **implemented** (see §6.7). MCP tool names are **decided by the remote server**: they collide with reserved tool names (`ls` / `execute`) and often carry dots (`github.create_issue`) — the latter does not pass the provider's function-name character set. The namespace turns "a collision" into "impossible". The prefix and the separator are constants (`agentkit/spec/decl/decl.go:336`, `:345`), the exposed name is composed in one place (`agentkit/build/mcptools.go:140`), and the provider is registered in the tool registry (`agentkit/build/tools.go:35`, `:41`) |
| 16 | Prefix matching | an agent's `disabled_tools` accepts an exact name **+** a trailing `*` prefix pattern | **implemented** (see §6.3): `newTable` keeps the prefix patterns (`agentkit/build/policy.go:98`) and `Table.Blocked` matches them (`agentkit/build/policy.go:146`). ★ The **global deny** is gone with the partition, so there is no second layer left to match — the prefix syntax is the **only** place it lives. The agent's `mcps:` list lands in the same table as a namespace prefix (`agentkit/build/policy.go:129` `denyMCPNamespaces`) |
| 17 | Where a tier is written | **with the tool**: each file's `allows:` names what runs **without a nod**, and everything else asks; ★ there is **no global list** any more — `policy/permissions.yaml` is gone | **implemented** (see §6.3, §6.6): the server's own file names its exceptions in the server's own vocabulary, the `tools/` files name theirs, and the table is assembled from the configuration. ★ a tier is a statement about **one tool**, so it belongs in that tool's file — and there it uses **the same vocabulary** (`http.yaml` says `post`, so it can say `allows: [post]`; the central file had to name the internal `request_post`). ★ the global list went with it: "not exposed" is said twice already (a file's whitelist, an agent's `disabled_tools`), and a third place is one more to keep in sync — and it was the only one that had to speak the exposed names |
| 18 | The default tier | **`ask`, everywhere** — the code carries one allow list for the tools that have **no file of their own**, and a file's `allows:` names the exceptions for the tools that file provides | ★ **implemented** (see §6.3): the default is `ask` (`agentkit/build/policy.go:55` `TierOf`), the code's list covers exactly the tools without a file (`agentkit/build/tools.go:61` `builtinAllowedTools`), and the table is assembled from the configuration, so the tier is answerable before the server is even up (`agentkit/build/mcptools.go:172` `mcpAllowedNames`). ★ one rule for everything: the filesystem tools, the HTTP tool and an MCP server behave the same way, and there is no per-group default to remember. ★ the code's list exists because `task` — the delegation tool — has **no file of its own**, and an agent that cannot delegate without a click cannot delegate at all. An MCP server's tools are **discovered at runtime**: openheron cannot know what `delete_repo` does, and under stdio the server is a child process with the agent's own privileges ⇒ asking by default is the safe side of the trade |
| 19 | Which MCP servers an agent loads | the agent's own `mcps:` list, in its frontmatter | **implemented**. ★ a server is a **capability**, and a capability belongs to the agent that uses it — the researcher does not need the deploy server. ★ absent = every configured server, `[]` = none (the same pointer trick as `skills`), so the common case is **one** place: write the server file, and every agent that has not narrowed its list gets it (`agentkit/spec/decl/decl.go:114`, `agentkit/spec/load.go:554` `mcpServersOf`) |
| 20 | The knowledge base's directory key | **`corpus_dir`**, not `corpus` | ★ the value is a **directory**, and every other directory in the configuration says so: `data_dir`, `static_dir`. "Corpus" is the information-retrieval term for the collection of documents a search answers from — a user who does not know the term should still be able to read the key. ★ the old key still loads, so the rename does not break a working configuration; it says so in `deferred` (`agentkit/spec/decl/decl.go:541-554`, `agentkit/spec/load.go:209-215`) |

### "One entity, one file" rather than a single-file partition table

A single `config.yaml` with a named table such as `llm.profiles.default` can express the same content, but:

- a named table **cannot be merged field by field** (an array table merges by position, a map table is replaced wholesale), whereas entity files are wholesale replacements by nature;
- one file per provider means `git log` shows directly "who changed which vendor";
- the same principle as `agents/*.md`: adding an agent does not change the existing file structure.

In openheron the unit is "one provider, one file" (`agentkit/spec/load.go:17`), and the models inside it are entities too (`agentkit/spec/load.go:53-65`) — which is why a model is referenced as `<provider>/<model>` and not as a path.

---

## 3. Directory layout

```
<projectRoot>/.openheron/
├─ agents/main.md            ← agent: frontmatter + body (the body is the prompt)
├─ llm/<provider>.yaml       ← vendor profile: base_url / api_key / models[]
├─ server/config.yaml        ← the process itself: address / dirs / timeouts / the caps
├─ tools/<tool>.yaml         ← http / web_search / wikipedia / kb
├─ mcps/<server>.yaml        ← one MCP server: command / transport / tier
├─ skills/<name>/SKILL.md    ← skills (resolved by their own rule, see below)
└─ state/                    ← runtime data, never committed
```

★ **There is no `policy/` directory.** The partition is gone: the tiers live in the files that
**provide** the tools, and the caps live in `server/config.yaml` (§6.3, §12.2). ★ A
configuration that still carries the directory is **not read** — and it is **not
silent** either: the loader names the retired partition in `deferred` and says where its
content went (`agentkit/spec/config.go:305-338` `noteRetiredDirs`).

The empty directories are **not** created automatically: a missing directory simply means "nothing to load" (`agentkit/spec/config.go:377-381`). ★ The **config root itself** is the exception — the defaulted root is created when it does not exist (`agentkit/spec/config.go:200-207`), because "no configuration yet" and "the directory is not there" are the same state.

★ **`tools/` is a partition of its own** — it holds the configuration of the external tools (`http.yaml`, `web_search.yaml`, `wikipedia.yaml`) and of the knowledge base (`kb.yaml`) (`agentkit/spec/load.go:165-193`), and it **is** hot-reloaded (`agentkit/spec/config.go:56-60`), while `server/` is not (`agentkit/spec/config.go:51-55`). The reason for the split is hot reload: `server/` configures the process itself and cannot be swapped at runtime, while "change one tool's timeout" should take effect on the next turn. The knowledge base *index* is runtime data and lives under `state/kb/<index>` (`kbcmd.go:130`).

★ **There is no `.config.lock`** — the config editor uses **optimistic concurrency on a content hash** instead: `spec.Revision()` is the first eight hex characters of the file's SHA-256 (`agentkit/spec/write.go:265-270`), and a write that carries a stale `baseRevision` is rejected as a conflict (`agentkit/spec/write.go:439-456`). No lock file is needed, so none is created. (The example's ignore file still lists `.config.lock` — a leftover; see the ignore rules below.) The editor's design is in `config-editor-design.md` (upstream).

`state/` replaces the old `DATA_DIR=./data`; it is created by `Service.DataDir("")` (`agentkit/spec/config.go:241-253`):

```
.openheron/state/
├─ sessions/<id>/
│  ├─ session.jsonl      ← the messages (JSONL)
│  ├─ state.json         ← pending approval / title / counts / audit
│  ├─ checkpoints/       ← this run's runner state (one store per session)
│  │  └─ <checkpointID>.ckpt
│  └─ workspace/         ← this session's own working directory
└─ kb/<index>/           ← the knowledge base index
```

★ **The checkpoints live in the session's own directory** — `<sessions>/<sessionID>/checkpoints/<checkpointID>.ckpt` (`storage/filestore/checkpoint.go:39`, `storage/filestore/checkpoint.go:64-67`). A session's persisted state is finally in one place: deleting the session directory deletes all of it. A damaged checkpoint is quarantined as `<checkpointID>.ckpt.corrupt` rather than silently overwritten (`storage/filestore/checkpoint.go:22`, `storage/filestore/checkpoint.go:185-194`). The **old flat layout** (`state/checkpoints/<id>.ckpt`) is still consulted as a read-only fallback (`storage/filestore/checkpoint.go:86-96`), otherwise the turn waiting for a human approval in an old session could never be resumed. See `checkpoint-store.md` (upstream).

★ **There is no `state/log/`** — apart from sessions and checkpoints, nothing else is written. The runtime log goes to standard error and is not persisted (persisting it means managing rotation and cleanup, and its value for troubleshooting is far below its cost).

### The version-control boundary

```
# Runtime data does not go into version control — the config wants diff / blame / rollback, session content must not leak
state/
.config.lock

# Safety net for plaintext keys (the normal way to write them is ${ENV} or @file:)
llm/*.local.yaml
*.key
```

**Why `state/` must be excluded**: sessions and logs are high-frequency data, they turn the history into noise, and they **leak session content**.

The other way round, the configuration is **recommended** to enter version control — that is what makes "what is running on this machine" an answerable question, diffable, blameable, rollbackable. But that is a **recommendation**, not a precondition: keeping everything local works just as well. What is not negotiable is `state/`: **it must be excluded** — it holds the session transcripts.

★ In **this** repository the local config root is not tracked at all: `.gitignore` excludes `/.openheron/` ("the real local configuration (the factory sample is in `.openheron-example/`); untracked, so that personal configuration and secrets stay out of the repo"), and the sample configuration **is** committed under `.openheron-example/`. The example's ignore file is what the block above shows — and it still lists `.config.lock`, which no code path creates any more.

### The factory sample you can copy

`.openheron-example/` in the repository root is a configuration that **loads successfully** (it declares a sub-agent team and a reasoning tier table):

```bash
cp -r .openheron-example/{agents,llm,skills} /your/project/.openheron/
```

Its `README.md` lists the three rules that trip people up most: an unset `${ENV}` **reminds** at startup (it does not block), `model` must carry the `<provider>/` prefix, and a duration must carry a unit.

> ★ **For a "minimal runnable" configuration, do not copy by hand**: `openheron init` creates one in the **current directory**, and it **only writes what is genuinely missing** —
>   it never overwrites a file that already exists (an existing file means **wholesale replacement**,
>   so writing one by hand would silently swap out the user's own).
>   With `--config-dir` it writes there, otherwise under `<target>/.openheron`.
>   The design is in `init-design.md` (upstream).
>
> What it writes: `agents/main.md`, `agents/task.md`, `llm/<provider>.yaml`, `mcps/example.yaml`, `server/config.yaml` — and, with `--tools`, the tool configurations under `tools/` (`initcmd.go:138-150`, `initcfg/initcfg.go:77-86`, `initcfg/initcfg.go:110`, `initcfg/initcfg.go:167`, `initcfg/initcfg.go:185`).
> ★ **The `tools/` section is opt-in**: `init` does not write it unless `--tools` is given — a provider whose configuration is absent returns nothing, so writing the file is what mounts the tool (`agentkit/build/nettools.go`).
> ★ **There is no `policy/` section — the partition is gone.** The tiers are named by the
> files that **provide** the tools (§6.3, §6.6), and the caps are already written into
> `server/config.yaml` (§12.2). ★ A file that says what the code already does is noise,
> and noise in a configuration tree is read as meaning.

---

## 4. Loading and precedence

```
built-in defaults
   ↓ pre-fill the struct
<config root>/               the one and only
   ↓ explicit override
environment variables        highest
```

**There is exactly one config root**, resolved in three steps:

| Order | Source | Notes |
|---|---|---|
| 1 | `--config-dir <path>` | points at the **config root itself** (inside it are `llm/` `agents/`) — `main.go:84-85` |
| 2 | `OPENHERON_CONFIG_DIR` | the same — `agentkit/spec/config.go:103`, `agentkit/spec/config.go:189` |
| 3 | Fallback | `<projectRoot>/.openheron` — `agentkit/spec/config.go:192`; the project root is `--project-root` or the working directory (`main.go:86-87`, `agentkit/spec/config.go:175-185`) |

★ **The fallback root is created; a named one is not.** Only the defaulted root is created when it is missing (`agentkit/spec/config.go:200-207`): `--config-dir` or `$OPENHERON_CONFIG_DIR` pointing nowhere is a user typo, and it must not turn into a silently empty configuration tree.

★ **There is no upward search — and openheron never had one.** The upstream design walked up from the working directory looking for `.heron/`; that rule was removed on purpose (`agentkit/spec/config.go:161-173`): once the config root depends on "where the process was started from", the same command run from another directory reads a different configuration, and a test instance ends up reading the developer's own configuration. A configuration in the home directory is **not** a fallback layer either: if `~/.openheron` exists but is not the root in use, the runtime says so and asks for it to be named (`main.go:109-126`).

★ **Only a handful of environment variables really do override a value** (§4.4): the ones that do are read at the point of use (`main.go:217-219`, `main.go:327`, `main.go:341`), not by a generic overlay — there is no "the environment wins" rule, only a list of places where the environment is consulted.

### 4.1 Defaults come from "pre-filling the struct, then decoding"

The loader **pre-fills** the configuration struct with the built-in defaults and then decodes each file on top of it (`agentkit/spec/config.go:291-299`): the `server/` section starts from `defaultServer()` (`agentkit/spec/defaults.go:58-69`), the knowledge base from `defaultKB()` (`agentkit/spec/defaults.go:49-56`). ★ There is no third prefill: the tier table is **not** a configuration section any more — it is assembled from the files that provide the tools (§6.3). `loadServer` decodes into a **copy** of the pre-filled struct (`agentkit/spec/load.go:97-104`), so a key that is absent from the file keeps its default.

That "only the keys that appear are written" behaviour is not openheron's own code — it is yaml.v3's. `mappingStruct` (`yaml.v3@v3.0.1/decode.go:878`) walks the keys of the document and writes only the fields it finds:

```go
if info, ok := sinfo.FieldsMap[sname]; ok {   // yaml.v3@v3.0.1/decode.go:921
    ...
    var field reflect.Value
    if info.Inline == nil {
        field = out.Field(info.Num)
    } else {
        field = d.fieldByIndex(n, out, info.Inline)
    }
    d.unmarshal(n.Content[i+1], field)          // decode.go:935 ← only the keys that appear are written
}
// fields that do not appear: never touched
```

⇒ Decoding layer by layer implements "only the keys that appear are overwritten" for free; there is no merge logic to write.

**Verified**: this is not an assumption, it is the result of reading `mappingStruct` in `yaml.v3@v3.0.1` — the version openheron depends on (`go.mod:20`). The upstream text cited the same file's `yaml.v3@v3.0.1/decode.go:930-934`; in `v3.0.1` that block sits at `:929-934` and the `d.unmarshal` call at `:935`, so the upstream citation still holds.

### 4.2 Strict mode

```go
func decodeStrict(data []byte, v any) error {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return ErrEmptyDocument
		}
		return err
	}
	return nil
}
```

Every partition goes through it (`agentkit/spec/load.go:23`, `agentkit/spec/load.go:79`, `agentkit/spec/load.go:99`, `agentkit/spec/load.go:207`, `agentkit/spec/load.go:215`, `agentkit/spec/load.go:130`).

**Why strict**: in a lenient mode a misspelled field name **silently falls back to the default**, and "why did my configuration not take effect" is the most expensive class of problem to diagnose. yaml.v3's error carries the line number itself (`yaml.v3@v3.0.1/decode.go:944`): `line N: field X not found in type Y`.

★ **An empty document is not a parse error.** The decoder reports a file with no YAML in it as `io.EOF`, which is true and useless — "EOF" tells a user nothing about what to do. openheron turns it into `ErrEmptyDocument` ("the file is empty (or contains only comments)") (`agentkit/spec/config.go:400-413`), because the comment-only template is a real file shape: a `tools/` file built from the template has no document at all (`agentkit/spec/load.go:106-133`).

### 4.3 The three shapes of "configured but not in effect"

| Shape | Handling | Example |
|---|---|---|
| **A key it does not recognize** | hard error, with the line number | `max_retry` (misspelled) — `yaml.v3@v3.0.1/decode.go:944` |
| **A key it recognizes but this version does not honour** | goes into `deferred[]`, **must be printed line by line at startup** | `models[].summarize_at` is parsed but no longer read — the threshold belongs to the agent (`agentkit/spec/load.go:59-63`) |
| **A key it recognizes but whose runtime prerequisite is missing right now** | the same, and it **does not block startup** | the `${ENV}` of `api_key` is not set — the 401 only comes back when that endpoint is actually requested (`agentkit/spec/load.go:39-42`) |

```go
func (c *Config) Deferred() []string { return c.deferred }        // agentkit/spec/config.go:115
```

```go
for _, d := range c.Deferred() {
    obs.Logger().Info("[main] config notice", "notice", d)
}                                                                  // main.go:198-200
```

Every entry is produced by `Config.deferKey` (`agentkit/spec/config.go:151-153`), and these are all the producers today:

| Producer | What it says |
|---|---|
| `agentkit/spec/load.go:42` | the `${ENV}` of `api_key` is not set — the call fails with 401 when it is made |
| `agentkit/spec/load.go:61` | `models[].summarize_at` is no longer read — the threshold belongs to the agent |
| `agentkit/spec/load.go:213` | the key `corpus` in `tools/kb.yaml` was renamed to `corpus_dir` — the old key still loads |
| `agentkit/spec/load.go:351` | an `${ENV}` in an MCP server's `env:` is not set — the entry is left out |
| `agentkit/spec/load.go:372` | an MCP server's timeout is not a duration — the default is used |
| `agentkit/spec/load.go:581` | the model declares no `context_window` — automatic compaction is inactive for that agent |
| `agentkit/spec/config.go:329` | a retired partition (`policy/`) is still on disk — it was **not read**, and the notice says where its content went |
| `agentkit/spec/reload.go:82` | a lenient duration did not parse and fell back to the default |

★ **The last message names `watch.debounce` for every lenient duration.** The bad durations are collected in one global list (`agentkit/spec/decl/decl.go:338-346`) and reported at one place (`agentkit/spec/reload.go:80-84`), so a wrong `tools/http.yaml` timeout also shows up under `watch.debounce`. Read the message as "a lenient duration was wrong", not as "the debounce was wrong".

The `deferred` entries are not only printed: they also surface in `/v2/health` and `/v2/config` (`api/v2_introspect.go:375`, `api/v2_introspect.go:450`).

This adds a third layer to the binary "strict / lenient": it neither lets a spelling mistake through, nor closes the door on future features and runtime state.

**The dividing line of the third shape**: it **does not affect startup**, because a missing prerequisite is **runtime state** (the secret has not been injected yet, CI has not exported it yet), not a configuration mistake. A mistake — a misspelled key, a wrong `@file:` path — still belongs to the first shape and is a hard error (`agentkit/spec/expand.go:13-22`).

### 4.4 Environment variables: only seven of them override anything

The priority chain in §2 says "env > config root", but that holds for a handful of variables only. What openheron actually reads today:

| Environment variable | What it overrides | Where it is read |
|---|---|---|
| `OPENHERON_CONFIG_DIR` | **the config root** (the one and only copy) | `agentkit/spec/config.go:189` (the name is the constant `ConfigDirEnv`, `agentkit/spec/config.go:103`) |
| `LISTEN_ADDR` | `server.listen_addr` | `main.go:217` |
| `DATA_DIR` | the resolved data directory (sessions, checkpoints) | `main.go:218` |
| `PROJECT_ROOT` | the project root the runtime hands to the agent | `main.go:219` |
| `STATIC_DIR` | `server.static_dir` | `main.go:341` |
| `FRONTEND_MODE` | `server.frontend_mode` | `main.go:327` |
| `EINO_EXT_SKILLS_DIR` | the skills directory | `agentkit/build/skill.go:189` |

The five server-side ones go through one helper — `override` (`main.go:459`) returns the
environment value when it is non-empty, the configuration value otherwise.

★ **The five server-side overrides are read once, at startup.** `server/` is not hot-reloaded (the
server category carries `hot: false`, `agentkit/spec/config.go:51-55`), so an environment override
is not a live knob — it needs a restart. `EINO_EXT_SKILLS_DIR` is the exception: it is read each
time the skills directory is resolved (`agentkit/build/skill.go:189`), so a change is picked up
when the agent pool is rebuilt.

★ Two more environment variables exist, and they are **not** in the table because they do not
touch the configuration:

- `OPENHERON_LOG_LEVEL` (`agentkit/obs/logger.go:11`, read at `agentkit/obs/logger.go:50`) — the
  log level;
- `OPENHERON_LOG_LLM_REQUESTS` (`llm/common/logging.go:16`, read at `llm/common/logging.go:81`) —
  whether the request body is logged.

★ And a third group reads the environment for configuration but is **not on the main path**:
`MODEL_TYPE`, `MODEL_TIMEOUT`, `MODEL_EXTRA_JSON`, `OPENAI_BASE_URL`, `OPENAI_API_KEY`,
`OPENAI_MODEL`, `OPENAI_MODEL_ID`, `ARK_BASE_URL`, `ARK_API_KEY`, `ARK_MODEL`, `ARK_MODEL_ID` —
all of them read by `build.ConfigFromEnv` (`agentkit/build/config.go:15`). The upstream design
calls this `llm.ConfigFromEnv`; in openheron the model assembly lives in `agentkit/build`, and
`llm/` holds only `llm/common` — the protocol leaf.

**The fields in `llm/*.yaml` and `agents/*.md` have no environment override.**
`agentkit/build/config.go` does hold a complete `ConfigFromEnv`, but it is **not on the main
path** — the main path is `build.Resolve` + `build.FromConfig` (`agentkit/build/provider.go:58`
and `agentkit/build/provider.go:19`), which read the files and nothing else. `ConfigFromEnv` is
only the fallback `buildModel` uses when the entry agent declares no model
(`agentkit/build/snapshot.go:126`, `agentkit/build/snapshot.go:139`), and the loader rejects an
agent without a model outright — "must declare model (no inheritance, no fallback)"
(`agentkit/spec/load.go:87`) — so **a valid configuration never gets there**.

This is a deliberate trade-off: a model profile is something to be **reviewed and committed** (it
decides whose money is spent and whether data is stored). Giving it an out-of-process
high-priority channel amounts to allowing "the configuration in the repository and the
configuration running in production are not the same" while neither side can see it. Leave it
for the scenario that needs it.

★ **The supported way to inject a secret is a reference, not an override.** `api_key` may only
be `${ENV_VAR}` or `@file:/path` (`agentkit/spec/write.go:490`); the value is expanded at load
time by `expandValue` (`agentkit/spec/expand.go:9`), and a `${ENV}` that is not set does **not**
stop the startup — it goes into `deferred[]` ("calls to this endpoint will fail with 401
Unauthorized", `agentkit/spec/load.go:42`). That is §4.3's third form: the value is missing at
run time, not written wrong.

★ Nothing else in the tree reads the environment. The remaining hits are in test files —
`OPENHERON_KB_CORPUS` (`kb/corpus_test.go:12`), `OPENHERON_LOCK_HELPER` and `OPENHERON_LOCK_PATH`
(`persist/filelock_test.go:128`).

## 5. The config root: three levels, no probing

The configuration needs to know the project root before it can read anything, and the project
root is itself a configuration value — the chicken-and-egg problem. openheron breaks it by **not
probing**: the config root is either named, or derived from one name.

```
① resolve the config root (nothing can be read yet)
   --config-dir                main.go:84
     ↓ unset
   $OPENHERON_CONFIG_DIR       agentkit/spec/config.go:172
     ↓ unset
   <project-root>/.openheron   agentkit/spec/config.go:175, created when missing
```

**What the upstream design did, and why openheron does not do it.** The upstream design searched
**upwards** from the working directory for the first ancestor that contained a `.heron/`
directory — `cd cmd/server && heron` leaves the cwd in a subdirectory while the user's mental
model is "that is the project". openheron **dropped the upward search on purpose**: once the
effective config root depends on "where the process was started from", the same command run from
another directory reads a different configuration, and a test instance ends up reading the
developer's own configuration. The rule is written into the `NewService` doc comment
(`agentkit/spec/config.go:161-173`) and into the repository's `AGENTS.md`.

★ The difference is visible in the shape of the code: `NewService` (`agentkit/spec/config.go:174`)
has **three** branches and **no loop** — `root` → `os.Getenv(ConfigDirEnv)`
(`agentkit/spec/config.go:189`) → `filepath.Join(projectRoot, DefaultBaseDirName)`
(`agentkit/spec/config.go:192`). There is no walk over the ancestors anywhere in the file.

**Why the environment variable outranks the derived default.** `$OPENHERON_CONFIG_DIR` is an
explicit naming; it should outweigh anything derived. ★ And `--config-dir` outranks the
environment variable (`main.go:84`): the command line is the most direct one-off statement of
intent.

★ **Only the defaulted root is created.** `NewService` creates the directory only when it derived
it itself (`agentkit/spec/config.go:200-207`); an explicit `--config-dir` or an
`$OPENHERON_CONFIG_DIR` pointing nowhere is a typo, and a typo must not silently grow an empty
configuration tree. `Service.RootIsDefaulted` (`agentkit/spec/config.go:237`) exists to tell the
two apart — `main.go:135-141` answers "no configuration at <root>: run `openheron init` to create
one" instead of "agents/main.md does not exist".

★ **A second entry point, for `init`.** `openheron init` uses `spec.NewServiceAt`
(`agentkit/spec/config.go:211`), not `NewService`: init writes into the directory it was pointed
at and never reads `$OPENHERON_CONFIG_DIR` (`initcmd.go:61-65`). Otherwise init would write the
new configuration somewhere else than the user asked for.

★ **The home directory is not a fallback either.** If `~/.openheron/` exists but is not the config
root in use, the process says so at startup (`main.go:109-126`): the config root is never
searched for, so that directory has to be named.

### 5.1 Two roots: `Root` and `ProjectRoot`

The config root and the project root are **two different directories**, and the code keeps them
apart:

| Method | Value | Used for |
|---|---|---|
| `Service.Root()` (`agentkit/spec/config.go:229`) | the config root itself — `<project-root>/.openheron` by default | reading the configuration; `state/` lives under it |
| `Service.ProjectRoot()` (`agentkit/spec/config.go:239`) | the project root — `--project-root`, else the working directory (`agentkit/spec/config.go:175-185`) | the agent's workdir; the base for relative paths |
| `Service.DataDir(v)` (`agentkit/spec/config.go:241`) | `v` if absolute, otherwise under the **config root** (`agentkit/spec/config.go:247`); created when missing | sessions and checkpoints |
| `Service.ResolveDir(v)` (`agentkit/spec/config.go:255`) | `v` if absolute, otherwise under the **project root** (`agentkit/spec/config.go:260`) | `static_dir`, the knowledge-base corpus |

★ **They are decoupled.** `--config-dir /etc/openheron-cfg` puts the configuration elsewhere
while `--project-root` (default: the working directory) stays where the agent works. Merging
them would make "the parent of the config root" the agent's workplace — `/etc` — and the agent
would grep and glob there.

★ **The default project root is the working directory, not the parent of the config root.** The
upstream design derived it from the config root's parent; openheron takes it from
`--project-root` or the cwd (`main.go:86-87`, `agentkit/spec/config.go:175-185`). With the
default config root the two happen to coincide (`<cwd>/.openheron` → parent `<cwd>`), but they
stop coinciding the moment the config root is named.

Using the wrong root gives two typical failures: the frontend looks for the assets in the config
root's `web/dist` → **403/404**; the agent is told "the project root is the config root" → it
greps and globs in the wrong directory.

### The base for relative paths

**Relative paths are not all relative to the same base.** openheron has two:

| Path | Base | Code |
|---|---|---|
| `server.data_dir` (relative) | the **config root** | `agentkit/spec/config.go:246-248` |
| `server.static_dir` (relative) | the **project root** | `agentkit/spec/config.go:255-261`, used at `main.go:341` |
| `kb.corpus_dir` (relative) | the **project root** | `kbcmd.go:148` |

★ **The split is deliberate: state travels with the configuration, the working tree travels with
the project.** A relative `data_dir` follows the configuration when the configuration moves
(`--config-dir /etc/openheron-cfg` puts the sessions next to it); a relative `static_dir` and a
relative knowledge-base corpus belong to the project being served, and must not move when the
configuration moves.

★ **`PROJECT_ROOT` is applied later than the config root.** `setupConfig` resolves the service
first (`main.go:184`) and only then overrides the runtime project root from the environment
(`main.go:219`). So the `PROJECT_ROOT` environment variable moves the agent's workdir
(`main.go:239`, `main.go:245`) but **not** the defaulted config root — that still lands in the
working directory's `.openheron/`. To move both, pass `--project-root`.

★ And the two roots really are different in that case: `static_dir` resolves against the
flag-based project root (`main.go:341`), while the agent's workdir uses the environment-overridden
one (`main.go:239`). With `PROJECT_ROOT` set, the frontend's assets and the agent's working
directory stop sharing a base.

### 5.2 The test instance is isolated by a named config root

openheron's test environment is defined in one place: `.env.test` at the repository root
(committed), plus the Makefile targets.

| | Development | Test |
|---|---|---|
| openheron itself | :8080 | **:18099** |
| Vite | :5173 | **:15173** |
| E2E fake backend | — | **:18180** |

(`.env.test:25-38`; the same table is in `docs/developer-manual.md` §5.1.)

The instance is started by `scripts/test-backend.sh`, behind three Makefile targets:

| Command | What it does |
|---|---|
| `make test-env-backend` | start the test instance in the foreground (`Makefile:312-314`) |
| `make test-env-reset` | rebuild the test directories only, do not start (`Makefile:316-318`) |
| `make test-env-sync` | re-copy the development-side definitions into the test environment (`Makefile:320-322`) |

The script names the three environment variables that have to be isolated, and moves the working
directory (`scripts/test-backend.sh:37-45`):

| | Value | Effect |
|---|---|---|
| `OPENHERON_CONFIG_DIR` | `$OPENHERON_TEST_ROOT/project/.openheron` | **the config root** (the one and only copy) |
| `DATA_DIR` | `$OPENHERON_TEST_ROOT/state` | the session data |
| `LISTEN_ADDR` | `:18099` | the listen address |
| (cwd) | `cd $OPENHERON_TEST_ROOT/project` | the project root is the working directory |

(`scripts/test-backend.sh:117-119` and `scripts/test-backend.sh:137`.)

★ **Ports and state must be isolated together.** A different port is not data isolation:
openheron writes its sessions under the config root's `state/`, so a test writing there pollutes
the real history (`AGENTS.md`, "Rules"). The test instance **names** its config root, and
because the config root **never searches upwards** (`agentkit/spec/config.go:169`), the
repository's own `.openheron/` is **structurally unreachable** — the isolation does not depend
on "a probe that happens to stop here", it depends on a named root
(`scripts/test-backend.sh:38-42`).

★ **The config root is pre-seeded, not probed into existence.** The script creates
`$PROJECT_DIR/.openheron/` and `$PROJECT_DIR/.openheron/server/`
(`scripts/test-backend.sh:47`) and writes a default `server/config.yaml`
(`scripts/test-backend.sh:79-105`). Without them the instance would start against an empty
configuration tree, and the first error would be "agents/main.md does not exist or could not be
parsed" — which reads like a lost file rather than a misconfigured environment.

★ **`agents/ llm/ skills/ tools/` are copied, not symlinked.** They are **inputs** (agent
definitions, vendor protocols, tool configuration), so the test instance needs real
agents to get through a whole turn; copying rather than symlinking means a crashing test cannot
damage the development configuration (`scripts/test-backend.sh:49-73`). `server/` and `state/` are
deliberately **not** copied — the script manages those itself.

★ **`tools/` is in the copy list on purpose.** It is a configuration directory (one file per
external tool, plus the knowledge base), and leaving it out means the HTTP/search tools are
always on — or always off — in the test instance, which is not the same thing as development
(`scripts/test-backend.sh:55-59`).

★ **`make test-env-sync` is mandatory, not optional.** Change an agent definition on the
development side and the test instance keeps running the old one until the definitions are copied
again (`AGENTS.md`, "Rules"; `docs/developer-manual.md` §5.2).

★ **The port table lives in one file.** `.env.test` holds every convention of the test
environment; Vite, Playwright and the smoke scripts all read it, so changing a port means
touching one file instead of five (`AGENTS.md`, "Ports: testing has its own environment"). The
remaining isolation details are in `docs/frontend-design.md` §11.2 (upstream).

## 6. File Specification

### 6.1 `llm/<provider>.yaml`

```yaml
# .openheron/llm/openai.yaml
type: openai_compat                # free-form label, for display only
base_url: https://api.openai.com/v1
api_key: ${OPENAI_API_KEY}         # ${ENV} or @file:/path
timeout: 5m
extra:                             # provider-level defaults, they go into ExtraFields
  reasoning_effort: low

models:                            # ★ multiple models: several models under one provider
  - id: gpt-4o
    context_window: 128000
    timeout: 10m                   # overrides the provider level (reasoning models are an order of magnitude slower)
    extra:                         # overrides the provider-level extra
      temperature: 0.2
  - id: o3
    context_window: 200000
    reasoning:                     # tier vocabulary (see below)
      off:  {}                     # send no thinking field at all
      low:  { reasoning_effort: low }
      high: { reasoning_effort: high }
    reasoning_default: low         # used when the agent writes no reasoning
    reasoning_field: reasoning             # the field this endpoint carries the chain of thought in
    reasoning_field_from: reasoning_content # the source field name when replaying history (defaults to the same)
```

#### The full field set

**Provider** (connection properties, shared by every model under the same provider)

| Field | Type | Required | Description |
|---|---|---|---|
| `type` | `string` | ✅ | **Display only** — no code branches on it. It must be non-empty (`agentkit/spec/load.go:28-30`), but the value itself is free-form: `openai_compat` is what the factory template ships (`agentkit/templates/files/llm/openai.yaml`) |
| `base_url` | `string` | ✅ | Host (a path prefix is allowed). **Do not write `/chat/completions`** |
| `api_key` | `string` | | `${ENV}` / `@file:`; empty = no authentication (a self-hosted gateway, a local ollama). **An unset `${ENV}` does not block startup**, it only records a reminder; the request comes back 401 |
| `timeout` | `Duration` | | Default `5m` (`agentkit/build/provider.go:31-34`). **Do not rely on the SDK's implicit default** — the endpoint decides how long it is willing to think |
| `header` | `map[string]string` | | ★ **Parsed, then never read** — see below |
| `extra` | `map[string]any` | | Vendor-private request body, **top-level keys override** |
| `hint_keys` | `[]string` | | The field names this endpoint uses, so that a 400 can be explained accurately. The protocol layer hardcodes no vendor's field names (`llm/common/error.go:177-179`) |
| `models` | `[]Model` | | The models it declares; **absent = the provider is unusable** (a reference fails to resolve) |

★ **`header:` is a trap: it parses, and then nothing happens.** The schema declares it
(`agentkit/spec/decl/decl.go:26`), the loader accepts it — strict mode makes an unknown
key an error (`agentkit/spec/config.go:415-425`) — and from there on no code path reads
it: `build.FromConfig` assembles the `common.Config` without a header
(`agentkit/build/provider.go:36-43`), and `common.Config` has no header field at all
(`llm/common/model.go:28-44`). The option that would carry it exists
(`llm/common/option.go:24-26`, `llm/common/request.go:46-48`), but nothing ever fills
`RequestOptions.Header`. ⇒ **Do not write it expecting it to work.**

**Model** (the inherent properties of one model)

| Field | Type | Description |
|---|---|---|
| `id` | `string` | Required (`agentkit/spec/load.go:56-58`). The model identifier handed to the endpoint |
| `kind` | `string` | `chat` (the default) or `embedding` (`agentkit/spec/decl/decl.go:575-579`). An embedding model cannot be built as a chat model (`agentkit/build/provider.go:26-29`), and the knowledge base refuses to point at anything but an embedding model (`agentkit/spec/load.go:611-615`) |
| `dimensions` | `int` | The embedding dimension; `0` = unknown, learned at index time (`agentkit/spec/load.go:364-367`) |
| `context_window` | `int` | **0 = unknown** (not "unlimited"). It is the denominator of the compaction threshold — see below |
| `summarize_at` | `Threshold` | ★ **Deprecated**: still parsed so an old configuration keeps loading, but nothing reads it. The threshold belongs to the agent now (`agentkit/spec/decl/decl.go:46-53`), and the loader says so out loud (`agentkit/spec/load.go:59-63`) |
| `timeout` | `Duration` | Overrides the provider level. **It has to be allowed** — the provider level can only accommodate the slowest model, and that takes the timeout protection away from the fast ones |
| `extra` | `map[string]any` | Overrides the provider-level key of the same name |
| `reasoning` | `map[string]map[string]any` | **Tier vocabulary**: tier name → the fields this endpoint sends it as. Empty = the model does not think |
| `reasoning_default` | `string` | The suggested tier; used when the agent writes no `reasoning` (it takes effect through `RequestOptionsFor`) |
| `reasoning_field` | `string` | The field the endpoint carries the chain of thought in; empty = the underlying library's native name (`reasoning_content`) |
| `reasoning_field_from` | `string` | The **source** field name when replaying history; defaults to `reasoning_field` (`agentkit/build/provider.go:146-155`) |

★ **`context_window` is load-bearing today.** The upstream design called it "pure
metadata, because there is no summarization" — openheron **has** summarization. The
window is what the agent's `summarize_at` percentage is measured against
(`agentkit/spec/summarize.go:25-39`), and the compaction middleware is only built when
the resolved threshold is above zero (`agentkit/build/summarize.go:36-40`). A model that
declares no window therefore has **no** threshold: a percentage of nothing is nothing,
automatic compaction never fires, and openheron says so instead of doing nothing
quietly (`agentkit/spec/load.go:322-341`).

#### Multiple models: one provider declares several

One `llm/<provider>.yaml` holds the vendor's `base_url` / `api_key` / `timeout` plus the
`models[]` list. The reference form is **`<provider-id>/<model-id>`**:

```yaml
# agents/main.md
model: openai/gpt-4o        # hangs the agent on a model

# agents/researcher.md
model: openai/gpt-4o-mini   # the cheap retrieval tier
```

It parses into `Ref{ProviderID, ModelID}`: `ParseRef` takes the **first** `/`, and
neither side may be empty — a reference without a `/` is an error
(`agentkit/spec/load.go:437-446`). The normalized form is the key of `Config.Models`
(`agentkit/spec/load.go:64`), and the provider id is the **file name** without the
extension (`agentkit/spec/load.go:17`).

#### Request-level `model` override

**Within one instance**, the model name is switched by a request-level option rather
than by building one instance per model:

```
acl/chat_model.go:560  options := model.GetCommonOptions(&model.Options{
                              Model: &c.config.Model,   ← the construction-time value serves only as the "default"
                              …}, opts...)
acl/chat_model.go:588  req := &openai.ChatCompletionRequest{
                              Model: *options.Model,     ← ★ this is what really enters the request body
```

(`eino-ext/libs/acl/openai@v0.1.17/chat_model.go:560` and `:588-589`.)

`GetCommonOptions` merges the caller's `opts` over the construction-time defaults, and
`model.WithModel(name)` is exactly such an opt (`eino@v0.9.22/components/model/option.go:211-224`).
openheron uses that channel twice: `RequestOptionsFor` puts the model's own id into
`RequestOptions.Model` (`agentkit/build/provider.go:126-128`), and
`RequestOptions.Options()` turns it into `model.WithModel` (`llm/common/request.go:31-33`).

★ **The instance granularity is "one per agent", not "one per provider".** A request-level
option being able to switch models without one instance per model does not mean there is
only ever one instance: the granularity is decided by the chain below, and the head of
the chain is the agent itself.

```
agent (each declares model: on its own)
  ↓
(provider, model, reasoning tier, reasoning_field, timeout)
  ↓ FromConfig: the model name and the timeout are fixed at construction time
(common.Config{HTTPClient: &http.Client{…}})
  ↓ AttachRequestOptions: tier → the field shape of the endpoint; the RequestOptions are baked into the optionedModel
two agents each hold a ChatModel and a connection pool
```

`resolveModel` is called once per agent node
(`agentkit/build/snapshot.go:118-137`), and it goes through `BuildModel`
(`agentkit/build/provider.go:81-95`), which builds a fresh client every time. So even
the same provider and the same model can end up as two instances — because the reasoning
tier, the `reasoning_field`, or the model-level `timeout` differ:

```
deepseek/deepseek-reasoner  used by main        → ChatModel A + http.Client A
deepseek/deepseek-chat      used by researcher  → ChatModel B + http.Client B
                                              ↑ the same provider, two instances
```

★ **This is not a convergence backlog item; it is the design.** The model choice of one
agent has **nothing to do** with another's (`agents/*.md` each write their own `model:`),
and they may well point at different vendors; even when they point at the same one, the
reasoning tier and the field shape of the endpoint still come from each model's own
profile. Sharing an instance would require proving that all of these are **equal** —
which means a cache keyed on them, which means a second source of truth.

⇒ **Instance granularity = the agent.** The overhead that is really being paid (a handful
of extra clients in a local single-user tool) buys the removal of a hidden coupling.

> The acl has **two** option channels, and they are easy to confuse:
> - **The common channel** (`GetCommonOptions`): `model.WithModel` / `WithTemperature` / `WithMaxTokens` / `WithTopP` / `WithStop`
> - **The vendor channel** (`GetImplSpecificOptions`, the `openaiOptions` struct with **8** fields): `ExtraFields` / `ReasoningEffort` / `ExtraHeader` / `RequestBodyModifier` / `RequestPayloadModifier` / `ResponseMessageModifier` / `ResponseChunkMessageModifier` / `MaxCompletionTokens`
>
> (`eino-ext/libs/acl/openai@v0.1.17/option.go:48-57`.)
>
> ★ **The vendor channel has no Model** — the dialect and the renames go through it, but
> switching models must go through the common channel.

#### Endpoint dialects and field renames

Every agent holds **its own** `*http.Client` — `build.FromConfig` builds one per
instance (`agentkit/build/provider.go:36-49`), and `common.New` wraps it in the logging
transport (`llm/common/model.go:60-64`). So:

- **Endpoint-private parameters** (the thinking switch and the like) → `WithExtraFields`,
  and they have to be handed down at the **request level**: the request-level extra is
  **replace** semantics (`go-openai@v0.1.6/chat.go:420-426` — "SetExtraFields will
  override any existing fields with the same key"), so the caller must pass down the one
  complete map that `provider.extra ← model.extra ← reasoning[level]` produced.
- **Cross-vendor field renames** (the chain of thought `reasoning_content` ↔ `reasoning`)
  → `WithRequestPayloadModifier`, the only workable path
  (`llm/common/option.go:13-18`, `llm/common/request.go:100-133`).

#### Startup validation: a reference must resolve

```go
	for _, id := range ids {
		a := c.Agents[id]
		ref, _ := ParseRef(a.ModelRef)
		if _, ok := c.Models[ref.String()]; !ok {
			where := a.File
			if where == "" {
				where = DirAgent + "/" + id + ".md"
			}
			return fmt.Errorf("agent %s (%s) references model %s which is not declared under llm/",
				id, where, a.ModelRef)
		}
	}
```

(`agentkit/spec/load.go:292-303`.)

**Why there is no silent fallback**: a sub-agent that silently inherits the main model
is a **cost incident**. The same discipline holds here — **a mistyped model name has to
explode at startup**.

#### The reasoning tiers: capability / vocabulary / intent

| Layer | What it is | Where it lives |
|---|---|---|
| **Capability** | Whether this model **can** think at all | whether `models[].reasoning` **exists** |
| **Vocabulary** | What the endpoint **sends** each tier as | the **key-value pairs** of `models[].reasoning` |
| **Default** | Which tier to use when the agent did not choose | `models[].reasoning_default` |
| **Intent** | Which tier **this call** uses | the `reasoning` of `agents/*.md` |

★ **The two sides are called `reasoning`, but one is a vocabulary and one is a choice**:
the provider declares "which tiers exist and what shape they have", the agent only writes
"which one to use". The family resemblance is intentional — while reading a
configuration they are two sides of the same thing.

```yaml
# llm/<provider>.yaml —— the same high, a different shape at every endpoint
reasoning:                 # vocabulary: which tiers this endpoint has
  off:  {}
  low:  { reasoning_effort: low }
  high: { reasoning_effort: high }
reasoning_default: low     # the tier to use when the agent writes no reasoning
```
```yaml
# agents/researcher.md
reasoning: high          # ★ only the tier name — it does not need to know what shape "high" has at this vendor
```

**The order the tier is resolved in**: `agent.reasoning` → empty, then
`model.reasoning_default` → still empty, then nothing is injected
(`effectiveTier`, `agentkit/build/provider.go:97-103`).

**There is only one merge chain**: `provider.extra ← model.extra ← reasoning[level]`,
top-level keys override (`agentkit/build/provider.go:105-113`). The one complete map
that comes out of the three layers has to be handed down whole — `WithExtraFields` is
**replace**, not merge (`go-openai@v0.1.6/chat.go:420-426`), so passing only one of the
layers loses the rest.

★ **A tier that is not in the vocabulary does not fail — but in openheron it is also
completely silent.** `RequestOptionsFor` simply skips the level it does not know
(`agentkit/build/provider.go:110`: `if lvl, ok := m.LevelFields(level); ok`); the `!ok`
branch injects nothing and reports nothing. There is **no** `deferred` entry for it.
The upstream design wanted a reminder here, and the factory template says so plainly
(`agentkit/templates/files/llm/deepseek.yaml`): "a tier the endpoint does not declare is
**silently ignored**". ⇒ **The vocabulary table is not optional**: an agent writes
`reasoning: high` while the model has no `high`, which means "no thinking at all", with
no error and no message. Declaring the table is what makes `high` / `low` take effect.

#### The two layers of `extra`

```yaml
extra:                    # provider level: the default for every model
  reasoning_effort: low
models:
  - id: gpt-4o
    extra:                # model level: overrides the key of the same name
      temperature: 0.2
```

`shallowMerge` merges by **top-level key** (`agentkit/build/provider.go:132-144`) — no
deep merge, because the semantics of "change `thinking.type` while keeping
`thinking.other`" are not predictable.

**★ `extra` treats every provider alike**, there is no "ark may not use extra" branch
any more: openheron runs every endpoint through the same base
(`eino-ext/libs/acl/openai`), so a per-vendor capability table would only ever reject
configurations that work.

★ **The agent's own `temperature` / `max_tokens` win over an `extra` key of the same
name** — the merged map has those two keys deleted before it goes down
(`agentkit/build/provider.go:114-119`), because the two would otherwise both write
`temperature` into the request body and the merge patch would let the `extra` one win.

★ **But there is one hole in that rule, and it is worth knowing about.** The request-level
option is only attached when the merged map is **non-empty**
(`llm/common/request.go:49-51`). The provider-level `extra` is *also* handed to the acl
client as the construction-level default (`agentkit/build/provider.go:42` →
`llm/common/model.go:71` → `eino-ext/libs/acl/openai@v0.1.17/chat_model.go:571`). So if
the provider's `extra` consists of **exactly** `temperature` (or exactly `max_tokens`)
and nothing else, and the agent sets that same key, the merged map becomes empty, no
request-level option is attached, and the construction-level `extra` survives — and it
is applied as a JSON merge patch over the request body
(`go-openai@v0.1.6/internal/marshaller.go:38`), which **overwrites** the agent's value.
Verified against a local endpoint: provider `extra: {temperature: 0.9}` plus an agent
`temperature: 0.1` puts `"temperature": 0.9` into the request body. Add a second key to
the provider's `extra` and the request-level option attaches, the merged map replaces
the construction-level one, and the body carries `0.1`. ⇒ **Do not put `temperature` or
`max_tokens` in the provider-level `extra` alone.**

#### Value expansion

```go
func expandValue(v string) (value string, resolved bool, err error) {
	v = strings.TrimSpace(v)

	switch {
	case strings.HasPrefix(v, "@file:"):
		p := strings.TrimSpace(strings.TrimPrefix(v, "@file:"))
		if p == "" {
			return "", false, fmt.Errorf("@file: missing path after the prefix")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return "", false, fmt.Errorf("read secret file: %w", err)
		}
		return strings.TrimSpace(string(b)), true, nil

	case strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}"):
		name := strings.TrimSpace(v[2 : len(v)-1])
		if name == "" {
			return "", false, fmt.Errorf("${} is missing a variable name")
		}
		val, ok := os.LookupEnv(name)
		if !ok {
			return "", false, nil
		}
		return strings.TrimSpace(val), true, nil

	default:
		return v, true, nil
	}
}
```

(`agentkit/spec/expand.go:9-38`.)

**★ A missing `${ENV}` is not fatal**: when the variable is not set, the expansion
returns an empty value and `resolved=false`, **startup is not blocked**, and only a
reminder is recorded (`agentkit/spec/load.go:39-45`). The real failure point moves to
the moment the endpoint is called — an empty `api_key` makes the endpoint answer 401
explicitly, the protocol layer classifies it as an `auth` error and says "check that
api_key is set, not expired, and authorized for this model"
(`llm/common/error.go:18`, `:72-73`, `:159-175`).

Why the early fail-fast design was changed: keys are often injected at run time
(container / CI / local shell), and "a provider is declared but has no key right now"
must not keep **the whole process** from starting — especially when the template holds
providers that no agent references at the moment. Moving the failure point from startup
to the request neither harms the unused provider nor loses the "this has to be fixed
eventually" visibility. The error is still attributable inside the request, and not a
null pointer a few hundred lines away.

> An unreadable `@file:` **is still a hard error** — that is a mistyped path, not "not
> injected yet". The dividing line: an environment variable is run-time state, a file
> path is part of the configuration itself.

#### Durations

`timeout` and everything else of that kind go through `time.ParseDuration` (`5m` /
`30s`), **never a nanosecond integer**. An empty string = "not set" (the provider
factory decides the default), not "timeout 0".

> **The trap**: `timeout: 60` (no unit) must be **rejected**, not read as 60 seconds.
> The custom `Duration` type enforces the string form (`agentkit/spec/decl/decl.go:584-600`):
> a non-string tag is an error — "duration must be a string with a unit (e.g. 60s / 5m);
> bare numbers are not allowed".

★ **But not every duration is strict.** `server/config.yaml` and the `tools/` files use
the lenient variant (`agentkit/spec/decl/decl.go:509-522`): a value that does not parse
is recorded and replaced by a default instead of failing the load. The lenient variant
covers `watch.debounce`, `start_timeout`, `lease_ttl` and the `tools/` timeouts
(`agentkit/spec/decl/decl.go:506`, `:275`, `:282`, `:306`, `:312`, `:319`).

★ **And the lenient fallback is the same value everywhere: `200ms`** — the debounce
default (`agentkit/spec/decl/decl.go:14`, `:325-334`). So `start_timeout: 60` (bare
number) does not become 60 seconds and does not fall back to the 60-second default
either: it becomes **200 milliseconds**, and the reminder in the deferred list always
names `watch.debounce` whatever key actually failed
(`agentkit/spec/reload.go:80-84`). A `lease_ttl` that lands on 200ms expires the lease
almost immediately. ⇒ **Write the unit.**

#### The YAML dialect risks (verified)

| Item | Conclusion |
|---|---|
| `yes`/`no`/`on`/`off` | **No risk as the value of a string field** — yaml.v3 follows the YAML 1.2 core schema, and the resolver's boolean list is only `true/True/TRUE/false/False/FALSE` (`yaml.v3@v3.0.1/resolve.go:52-53`); the rest land as strings. ★ **But when the target field is a `bool`, yaml.v3 additionally accepts the YAML 1.1 spellings for compatibility** (`yaml.v3@v3.0.1/decode.go:683-693`) — `watch.enabled: off` means `false`, and there is no way to write the string "off" there |
| `listen_addr: :8080` | **No quoting needed** — yaml.v3 parses it as the plain scalar `":8080"`. The separator is "colon + space"; `: 8080` (with a space) is a parse error |
| `base_url: https://...` | No quoting needed — the separator is "colon + space", `://` is safe |
| Indentation | The only real cost, and strict mode covers it: an indentation error reports a line number instead of silently misreading. The loader is strict in general — `KnownFields(true)` (`agentkit/spec/config.go:415-425`), so a misspelled key is an error, not silence: the legacy `thinking:` and `effort:` keys are rejected exactly that way (`agentkit/spec/config_test.go:864`) |

#### `extra` and the provider capability matrix (★ the key trap, revised after implementation)

openheron runs on a **single base** — every endpoint goes through
`eino-ext/libs/acl/openai` — so the old matrix (verified per eino-ext package) **does
not apply to the current architecture**. It is kept here as the comparison for "if the
multi-package route is ever taken again"; the versions below are the ones in the module
cache, and openheron depends on **none** of them:

| eino-ext package | `ExtraFields` (construction) | `WithExtraFields` (request level) |
|---|---|---|
| `openai` (`v0.1.13`) | ✅ `chatmodel.go:180` | ✅ `option.go:37` |
| `deepseek` (`v0.1.7`) | ❌ | ✅ `option.go:51` |
| `qwen` (`v0.1.9`) | ❌ | ✅ `option.go:45` |
| `openrouter` (`v0.1.10`) | ✅ `chatmodel.go:141` | ❌ |
| `ark` (`v0.1.71`) | ❌ | ❌ |

**⇒ The key lesson**: the extension points of the packages come in **all three
combinations** (both, request-level only, neither). When the code branches per vendor,
"which one is it" must be **declared in the configuration** — otherwise the same `extra`
takes effect on one endpoint and silently does nothing on another.

The problem disappears on the single-base route: `extra` uniformly goes through
`common.WithExtraFields` (`llm/common/option.go:20-22`) at the request level, and the
semantics of `go-openai@v0.1.6/chat.go:420-426` are "overlay the request body by key" —
an unknown key is simply sent along, and the endpoint handles it or rejects it. That is
less work than maintaining a capability table on the openheron side.

★ **What openheron does *not* have**: a vendor-specific branch of any kind. There is no
ark package, no `Region` / `AccessKey` / `SecretKey` / `RetryTimes` — those four fields
belonged to `components/model/ark` and openheron never used them. The thinking switch
that ark used to turn on by default is now an ordinary `extra` key, and the environment
variable `MODEL_EXTRA_JSON` only feeds the **env-only fallback path**
(`agentkit/build/config.go:15-44`), not the `llm/` configuration.

### 6.2 `agents/<id>.md`

```markdown
---
# .openheron/agents/main.md
description: Management role              # goes into the task tool description, it decides whether the model picks it
model: openai/gpt-4o              # a missing model is an error — no inheritance, no fallback
max_iterations: 20                # 0 = not declared; eino's own default (20) then applies
subagents: [researcher]           # references other file names; the order is the order of the task description
disabled_tools: [execute]         # affects this agent only; the tool is simply not there for this agent
skills: [refactor]                # the skills this agent may use; absent = every skill, [] = none
mcps: [github]                    # the MCP servers this agent loads; absent = every configured server, [] = none

temperature: 0.2                  # this call's intent (the same model should use different parameters in different agents)
max_tokens: 8192
reasoning: high                   # a tier name, taken from the keys of models[].reasoning
summarize_at: 80%                 # this agent's compaction threshold, a percentage of the model's context_window
---

The body is the Instruction. Leave it empty to use deep.New's built-in prompt.
```

#### The full field set

| Field | Type | Required | Description |
|---|---|---|---|
| `model` | `string` | ✅ | `<provider>/<model>`. **No inheritance, no fallback** |
| `description` | `string` | | Goes into the `task` tool description. The schema does not make it mandatory — an empty description simply produces an empty entry in that description |
| `max_iterations` | `int` | | `0` = not declared, and then eino's own default of 20 applies (`eino@v0.9.22/adk/react.go:345-348`) |
| `subagents` | `[]string` | | References other agent file names; a reference that does not exist or a cycle ⇒ an error at startup |
| `disabled_tools` | `[]string` | | it affects this agent only. A tool in the `ask` tier that is written here still needs a human, it is just that this agent never gets it at all — and there is no outer layer left for this one to be inner to |
| `skills` | `*[]string` | | The allow-list of skills. **The pointer is the point**: the key absent (`nil`) = every skill in the directory, `[]` = none (`agentkit/spec/decl/decl.go:95-106`) |
| `mcps` | `*[]string` | | The MCP servers this agent loads, by **server name** (the file name under `mcps/`). ★ **The pointer is the point**, the same way it is for `skills`: the key absent (`nil`) = every configured server, `[]` = none (`agentkit/spec/decl/decl.go:114`, `agentkit/spec/load.go:554` `mcpServersOf`) |
| `temperature` | `*float32` | | A pointer: 0 and "not configured" are two different things |
| `max_tokens` | `*int` | | The same |
| `reasoning` | `string` | | A tier name; a tier that is not in the model's vocabulary ⇒ nothing is injected, **silently** (see §6.1) |
| `summarize_at` | `Threshold` | | This agent's compaction threshold: a token count, a percentage of the model's window, or `off` (`agentkit/spec/decl/decl.go:121-126`) |

★ **Why the server list is per agent.** A server is a **capability**, and a capability
belongs to the agent that uses it: the researcher does not need the deploy server, and
the deploy agent does not need GitHub. ⇒ the `mcps/<server>.yaml` file says **what a
server is** (its command, its transport, its tier), and the agent's `mcps:` says **who
gets it**.

★ **The two lists are different granularities, and they compose.** `mcps:` picks the
**servers**; `disabled_tools` picks the **tools** — and it accepts a prefix, so
`disabled_tools: [mcp__github__*]` removes one server's tools from an agent that loads
two (§6.3). ★ Neither of them is a global list: both belong to one agent — and there is
no global list left to contradict them, because "not exposed" is said in exactly two
places (§6.3).

★ **A server that does not exist is a load-time error.** An agent naming a server under
`mcps/` that no file declares ⇒ the load fails, and the message lists the servers that
**are** declared (`agentkit/spec/load.go:499` `validateMCP`). ★ The reason: a tool that
is missing because of a typo looks exactly like a tool that is missing because the
server is down, and only one of those two is a configuration error.

★ **The list that is started is the union.** The tool set is one set for the whole
build, so the manager starts the servers **some** agent lists
(`agentkit/spec/load.go:529` `MCPServersInUse`); a server only a sub-agent lists is
started too — that sub-agent may be the one running. ★ A sub-agent carries a list of
its own (`agentkit/spec/decl/decl.go:604` `SubAgentSpec.MCPServers`), and **its** list
narrows **its own** table, not its parent's.

★ **Why `description` matters**: it is rendered into the `task` tool description
(`eino@v0.9.22/adk/prebuilt/deep/task_tool.go:177-190`, the line at `:182` writes
`- <name>: <desc>`) — and when `subagent_type` does not match, eino **returns an error
string** instead of giving the model a retry signal (`:164`). So a description that does
not say what the agent is for means the model never picks it.

| Constraint | Why |
|---|---|
| The file name is the id; `main` is a reserved name | The default agent and the other agents are **the same kind of thing in the same format** — the file just happens to be called `main` (`agentkit/spec/config.go:101`, `:270-272`) |
| `model` is mandatory (`description` is only needed for a sub-agent) | `model` being mandatory is deliberate — "a sub-agent silently inherits the main model" is a **cost incident**; `description` decides whether the model picks the agent, and a missing one only means it does not get picked, not that the startup fails |
| `model: <provider>/<model>` | The provider prefix is mandatory. Without it, "same name, different vendor" cannot be told apart, and there is no way to validate the reference at startup |
| The body is the prompt, **there is no `instruction_file` indirection** | One more layer is one more place to misconfigure |
| `subagents: [file name]` declares the topology | A reference to a file that does not exist is an error at startup |
| ★ **An agent that is not reachable from `main` is *not* an error here** | openheron does **not** check reachability. Such an agent is simply never built — only the agents reachable from the entry agent are constructed (`agentkit/build/assemble.go:81-96`) — while it still shows up in the introspection (`agentkit/spec/subagent.go:18-28`). The upstream design wanted an error; openheron does not have one |
| The whole file with that name is overwritten | The net gain of "one file, one entity" over merging a list |
| A nesting depth over `agent.max_depth` ⇒ an error | "configured three levels, actually only two run" is the hardest kind of problem to track down |

#### The depth cap

```yaml
# server/config.yaml
agent:
  max_depth: 1        # the root agent is level 0; only d < max_depth may spawn children
```

The cap lives in **one** place — `server/config.yaml` — and the default is `1`
(`agentkit/spec/defaults.go:11`). ★ There is no second copy to reconcile and no "the
stricter of the two" rule any more: `MaxDepth()` reads the server's own value
(`agentkit/spec/load.go:671`). ★ The price is that the cap is **not hot-reloaded** —
`server/` is a cold partition, so a change to the cap takes effect on the next start
(§12.2).

**The validation happens at startup** (`validateDepth`, `agentkit/spec/load.go:622-638`),
and it is an **error** rather than a truncation at run time. The assembly layer uses the
same criterion and simply **does not construct** the next level when the cap is reached
(`agentkit/build/assemble.go:82`) — two lines of defence: the assembly side is based on
the configuration, the run-time `depthGuard` on the **actual** nesting
(`agentkit/build/topology.go:28-62`, wired into the chain at
`agentkit/build/gate.go:172-178`).

#### The three inputs of a frontmatter

| Input | Behaviour |
|---|---|
| `---` … `---` plus the body | The frontmatter yields the fields, the body (everything after the delimiter) becomes the `Instruction` |
| A plain `.md` with no frontmatter | ★ **An error in openheron** — the loader decodes an empty frontmatter and reports "the file is empty (or contains only comments)" (`agentkit/spec/config.go:413`, `agentkit/spec/load.go:79-81`). The upstream design let a pure prompt draft through and failed later on the missing `model`; openheron fails earlier and with a clearer message |
| A `---` at the start with no closing delimiter | **An error**: "frontmatter closing delimiter not found" (`agentkit/spec/expand.go:48-65`) |

#### The frontmatter parser has to be written by hand

eino's `parseFrontmatter` is at `eino@v0.9.22/adk/middlewares/skill/filesystem_backend.go:165`,
**unexported**, so it cannot be reused. openheron writes its own — `splitFrontmatter`,
about twenty lines (`agentkit/spec/expand.go:48-65`).

`SKILL.md` already uses the same convention (`FrontMatter` plus the body,
`eino@v0.9.22/adk/middlewares/skill/skill.go:47-56`), so openheron's `agents/*.md` is
**isomorphic** to eino's native markdown convention, not a separate set of rules.

### 6.3 ★ The tiers: no partition, no global list (✅ implemented 2026-11-07)

★ **There is no `policy/` partition.** The tiers live in the files that **provide** the tools, and
the **default is `ask`**. Two facts, one rule:

| | |
|---|---|
| the default tier | **`ask`** — a tool nobody names asks first |
| the exceptions | **named**: by the file that provides the tool (`allows:`), and — for the tools that have no file of their own — by the code |

★ **Why `ask` is the default.** The cost of guessing wrong is asymmetric: one extra click
against a destructive call nobody saw. A tool nobody names is a tool openheron cannot vouch
for — an MCP server's tools are **discovered at runtime**, and under stdio the server is a
child process with the agent's own privileges. ★ The cost of the other default is an agent
that cannot delegate without a click, and `task` is in the code's list for exactly that
reason: an agent that cannot delegate at all cannot do its work.

#### Where the exceptions come from

| Source | What it names | Vocabulary |
|---|---|---|
| `agentkit/build/tools.go:61` `builtinAllowedTools` | the built-in tools that have no file of their own: the filesystem readers (`ls` `read_file` `glob` `grep`), the delegation tools (`task` `write_todos`) and the skill reader (`skill`) | the exposed names |
| `agentkit/spec/load.go:235` `AllowedTools` | the tools of the files that **are** written — `tools/http.yaml` says `allows: [request_get]`, `tools/kb.yaml` says `allows: [kb_search]` | **the file's own** |
| `agentkit/build/mcptools.go:172` `mcpAllowedNames` | an MCP server's own tools | **the server's own** |

★ **A file speaks its own vocabulary.** `http.yaml` says `request_get` because that is what
the file is about; the HTTP tool's own names are the exposed ones
(`agentkit/spec/decl/decl.go:248-253`). An MCP server says `create_issue` — not
`mcp__github__create_issue` — and the translation to the exposed name happens **once**,
where the tool is mounted (`agentkit/build/mcptools.go:66` `mcpPlan`).

★ **The table is assembled from the configuration**, so the tier of a tool is answerable
**before its server is even up** (`agentkit/build/spec.go:57` `policyOf`,
`agentkit/build/policy.go:55` `TierOf`). That is what lets the configuration editor answer
"what tier does this tool have" for a server that is not running.

#### "Not exposed" is said in exactly two places

- a **file's own `allows:` list** — what that file offers without a nod (§6.6, §6.7);
- an **agent's `disabled_tools`** — what that agent does not get at all (§6.2).

⇒ A third, global list is one more place to keep in sync — and it was the worst of the
three: the only one that had to name the tools in the **exposed** vocabulary while the
tools' own files speak theirs.

★ **There is no `deny` tier.** The global deny list is gone with the partition
(`agentkit/build/policy.go:15-20` — only `allow` and `ask` exist), and a tool an agent may
not use **keeps its own tier** and says why in its `reason`: it is **this agent** that
cannot have it, not that the tool is illegal (`agentkit/build/introspect.go:111` `verdictOf`).

★ **The gate wraps everything the exception list does not name**
(`agentkit/build/gate.go:43` `newApprovalMiddleware`): `allow` returns the endpoint
untouched, `ask` interrupts the turn for a human. There is no third branch — a tool that
is not in the agent's tool set never reaches the gate.

#### ★ Prefix matching (★ decided 2026-11-06, ✅ implemented for an agent's `disabled_tools`)

★ **An agent's `disabled_tools`** accepts, in addition to an **exact tool name**, a
**prefix pattern with a trailing `*`**:

| Entry | Matches |
|---|---|
| `execute` | the tool named exactly `execute` |
| `mcp__github__*` | every MCP tool of the server named `github` |
| `mcp__*` | every MCP tool, from any server |

| # | Rule | Why |
|---|---|---|
| 1 | only a **trailing** `*` is a prefix pattern; a `*` anywhere else ⇒ **a hard error at load time** | `mcp*foo` looks like a wildcard and is not one — a rule whose reach differs from what it reads as is a silent mismatch |
| 2 | a lone `*` ⇒ **a hard error at load time** | `disabled_tools: ["*"]` would take the agent's own tools away as well; that is a decision to write out, not a wildcard |
| 3 | the part before `*` must not be empty | rule 2 |
| 4 | an exact entry and a prefix entry are **equivalent** — a match removes the tool for that agent | there is no tier to choose here; the tool is simply not there for that agent |

★ **Where it lives.** The loader refuses the shapes that are not patterns
(`agentkit/spec/load.go:110` `validateDisabledTools`, the syntax itself in
`agentkit/spec/decl/decl.go:341` `ParseToolNamePattern`); the table keeps the prefix
patterns next to the exact names (`agentkit/build/policy.go:98` `newTable`), and
`Table.Blocked` matches them (`agentkit/build/policy.go:146`).

★ **The agent's `mcps:` list lands in the same table, as a namespace prefix.** A server
the agent does not list is denied by the prefix `mcp__<server>__`
(`agentkit/build/policy.go:129` `denyMCPNamespaces`), and the reason it reports is
`mcps` (`agentkit/build/policy.go:228` `LayerMCPScope`). ★ The narrowing is reported
**before** the tool rule: the server is not there at all, so the finer rules would have
said nothing about it.

★ **This is the one remaining place the prefix syntax earns its keep.** The MCP tier
lives in the server's own file, where a file's own `allows:` covers a whole class
without a pattern at all; what is left is the **narrowing**, and "this agent gets no
MCP tools" is the one statement that is about a **namespace** rather than about a tool.

★ **It adds no tier and reorders none** — the pattern only decides **whether an entry
matches a name**.

### 6.4 `server/config.yaml`

The process's own configuration. **It does not take part in hot reload** — a change needs a
restart (see §8). The server category carries `hot: false` (`agentkit/spec/config.go:51-55`),
and the config editor refuses to write it at all (`agentkit/spec/write.go:74-77`).

```yaml
# The process's own configuration.
listen_addr: ":8080"        # ★ it must be quoted: a value starting with : parses ambiguously
frontend_mode: embed        # embed | dir — the UI compiled into the binary, or a directory on disk
static_dir: web/dist        # relative to the **project root** (not the config root, not cwd)
data_dir: state             # relative to the **config root**; an empty string means state/

start_timeout: 60s          # the cap on one turn, from submission to entering the running state
                            # ⚠️ a lenient duration (see "durations come in two tiers" below)
lease_ttl: 10m              # how long a run's lease survives without progress

retry:
  max_retries: 5            # → helpers.ApplyModelRetry

agent:
  max_depth: 1              # the nesting depth cap; the entry agent is level 0

watch:
  enabled: true             # not written = on; an explicit false = off
  debounce: 200ms           # a lenient duration; a typo falls back to the default and is recorded in deferred

# ★ There is **no** `tools:` section here — the external tools' configuration moved to
#   the `tools/` directory (see §6.6).
#
#   The reason for the move is **hot reload**: server/ configures the process itself and
#   does not take part in hot reload, while "change one tool's timeout" is the kind of
#   change that should take effect on the next turn.
```

#### Durations come in two tiers (★ a distinction that only showed up while implementing it)

| Type | Used for | On a typo | Why |
|---|---|---|---|
| `Duration` | `timeout` in `llm/*.yaml` | **a hard error, with the line number** | a typo makes the request's timeout unpredictable, so it has to surface on the spot |
| `LenientDuration` | `debounce` `start_timeout` `lease_ttl` and the `tools/` timeouts | falls back to the default + **recorded in `deferred`** | it only affects "how long to wait"; it should not stop the program from starting |

Both have to carry a unit (`5m` / `60s`) — `timeout: 300` is rejected, because yaml can
"successfully" decode an `!!int` node into a string, and only looking at `Decode`'s error would
miss it (`agentkit/spec/decl/decl.go:584-591`).

★ **A downgrade has to make noise**: the broken entry goes into `deferred` and is printed at
startup together with the other downgrades. Falling back to the default silently is "configured
but useless" without the word "configured".

★ **But the fallback is the same 200ms for every lenient field, and the message names
`watch.debounce` whatever field was wrong.** `LenientDuration.UnmarshalYAML` falls back to
`decl.DefaultDebounce` (`agentkit/spec/decl/decl.go:509-518`), and the collector hardcodes the
text `watch.debounce fell back to default` (`agentkit/spec/reload.go:80-84`). So a typo in
`tools/http.yaml`'s `timeout` shows up as a `watch.debounce` message, and the timeout becomes
200ms rather than the 30s the field would otherwise get. Read the message as "a duration did not
parse", not as "the watcher misfired".

#### The full field list

| Field | Type | Default | Meaning |
|---|---|---|---|
| `listen_addr` | `string` | `:8080` | the Hertz listen address |
| `frontend_mode` | `string` | `embed` | `embed` serves the UI compiled into the binary; `dir` serves a directory on disk, and `static_dir` points at it. An unknown value is a warning, and the front end is off (`main.go:330-354`) |
| `static_dir` | `string` | `web/dist` | relative to the **project root** `<root>` (`Service.ResolveDir`), not cwd, not the config root; only read in `frontend_mode: dir` |
| `data_dir` | `string` | `state` | relative to the **config root** (`Service.DataDir`), not the project root, not cwd; an empty string means `state`; the directory is created when it does not exist |
| `start_timeout` | `LenientDuration` | `60s` | the same value as `agentkit/run.DefaultStartTimeout` (`agentkit/run/runtime.go:26`) |
| `lease_ttl` | `LenientDuration` | `10m` | how long a run's lease survives without progress; it must be **longer than the provider's own timeout**, or a slow but alive model is fenced out while it is thinking (`agentkit/run/lease.go:20`) |
| `retry.max_retries` | `int` | `5` | passing 0 uses the default — "not configured" and "configured as 0" are two different things (`agentkit/spec/load.go:442-447`) |
| `agent.max_depth` | `int` | `1` | see §6.2 |
| `watch.enabled` | `*bool` | `nil` → on | a pointer, to tell "not written" from "written as false" (`agentkit/spec/reload.go:86-91`) |
| `watch.debounce` | `LenientDuration` | `200ms` | the polling interval is half of it, with a floor of 20ms (`agentkit/spec/reload.go:141-149`) |

> **★ The project root vs the config root**: `Service.ProjectRoot()` (`agentkit/spec/config.go:239`)
> is the directory the static assets and the agent's working directory live in;
> `Service.Root()` (`agentkit/spec/config.go:229`) is the configuration root — `tools/`
> `mcps/` and `state/` are under it. **They are different**: `static_dir` resolves against the former,
> `state/` against the latter. Mixing them up is exactly what "the front end 404s" and "the agent
> is told to work inside `.openheron`" come from.

★ Every one of these values can also be overridden by an environment variable — `LISTEN_ADDR`,
`DATA_DIR`, `PROJECT_ROOT`, `FRONTEND_MODE`, `STATIC_DIR` (`main.go:217-219`, `main.go:327`,
`main.go:341`); §4.4 lists the whole set.

★ **Unlike `tools/` and `mcps/`, `server/` has no file-name whitelist.** Every `.yaml` under it
is decoded into the same `decl.Server` struct (`agentkit/spec/load.go:97-104`), so a stray
`server/typo.yaml` is merged into the configuration rather than rejected. `server/config.yaml` is
the convention, not a rule the loader enforces.

### 6.5 `skills/<name>/SKILL.md`

eino's native convention (`eino@v0.9.22/adk/middlewares/skill/filesystem_backend.go:46-48`): only
**first-level** subdirectories are scanned, `<dir>/<skill>/SKILL.md` is read — **deeply nested
ones are ignored**.

```markdown
---
name: refactor
description: ...     # ★ this is what decides whether the model picks it
context: inline       # inline | fork | fork_with_context
agent: main           # fork mode takes it from the AgentHub by name
model: fast           # takes it from the ModelHub by name
---
```

The frontmatter schema is eino's, not openheron's (`eino@v0.9.22/adk/middlewares/skill/skill.go:48-54`):

| Field | Type | Meaning |
|---|---|---|
| `name` | `string` | the skill's own name — the identity the **model** uses |
| `description` | `string` | rendered into the skill tool's description — ★ this is what decides whether the model picks it at all |
| `context` | `inline` / `fork` / `fork_with_context` | how the skill runs |
| `agent` | `string` | the agent a fork runs as (through the `AgentHub`) |
| `model` | `string` | the model override (through the `ModelHub`) |

★ **`inline` is not a declared constant.** eino declares only `fork` and `fork_with_context`
(`eino@v0.9.22/adk/middlewares/skill/skill.go:40-45`), and every other value — an empty one, and
a typo — falls into the inline branch (`eino@v0.9.22/adk/middlewares/skill/skill.go:430-440`).
So `context: inline` works, but a misspelling does not fail loudly: it silently means inline.
openheron's own listing reports an empty mode as `inline`
(`agentkit/build/skill.go:263-266`).

#### How the directory is located

```
EINO_EXT_SKILLS_DIR environment variable    ← the override, the highest one
   ↓ unset
look for .openheron/skills/ upward from cwd  ← agentkit/build/skill.go:212 probeProjectSkills
   ↓ not found
skills are not enabled
```

★ **Configured explicitly but missing ⇒ a log line.** A directory named by
`EINO_EXT_SKILLS_DIR` that does not exist is reported
(`agentkit/build/skill.go:201-208`). Silently returning "not enabled" turns "I configured skills
and nothing happened" into a problem nobody can trace — §4.3's "configured but useless".

★ **This upward search is not the config-root rule.** The config root never searches upwards
(`docs/architecture.md` §3.1), but the skills directory does: skills are a directory tree, and
searching upwards lets you reuse skills from your home directory; the config root is a set of
entities, and searching upwards would make the same command read a different configuration.

#### How `agent:` and `model:` land

Both frontmatter lines are effective today: `agent:` goes through the `AgentHub` (the `fork` /
`fork_with_context` modes), `model:` through the `ModelHub` (in inline mode it swaps this turn's
model). The implementation and the boundaries are in §7.

- `context: inline` needs only the description;
- an `agent:` that is not in the current agent's subtree ⇒ an error that lists the available names
  (`agentkit/build/hub.go:152-156`);
- a `model:` that cannot be resolved ⇒ an error, no longer silently ignored
  (`agentkit/build/hub.go:87-93`);
- a hub with no resolver wired (only the tests do that) ⇒ an error rather than quietly keeping the
  original model (`agentkit/build/hub.go:76-78`).

★ **openheron adds one more field, on the agent side**: the `skills` key in `agents/<id>.md` is
the allow-list of the folders under the skills directory. It is carried as a **pointer**
(`agentkit/spec/decl/decl.go:95-106`), because "not declared" and "declared and empty" mean
different things — the first is every skill, the second is none. The scope filters both halves
(`agentkit/build/skill.go:83-129`): `List`, which is what the model **sees**, and `Get`, which is
what it can actually **load**. Filtering only one half would leave a skill that a model remembers
the name of loadable.

### 6.6 `tools/<tool>.yaml` (★ a new partition, 2026-10-24)

The external tools' and the knowledge base's configuration. **One tool, one file** (the paths are
relative to the **config root** — `.openheron/` when it is the default one):

```
.openheron/tools/
├─ http.yaml             → decl.HTTPToolsCfg
├─ web_search.yaml       → decl.WebSearchCfg
├─ wikipedia.yaml        → decl.WikipediaCfg
└─ kb.yaml               → decl.KB (the knowledge base)
```

★ **The file's content is that tool's own configuration** (there is no outer wrapper key) —
`http.yaml` writes `methods:` directly, not `http: { methods: … }`. There is no `enabled` field
and no "write a null to turn it off" shape.

★ **The file does not decide which tools exist — the provider does.** The tool set comes
from the code: the providers registered in `defaultToolRegistry`
(`agentkit/build/tools.go:35`) and the filesystem tools that `fsx.New`
(`agentkit/build/snapshot.go:56`) mounts unconditionally. ★ The file's part is the
**parameters** and the **tier** — which of the tools run without a nod.

★ Today the two coincide for three of the families: `httpRequestToolProvider`,
`webSearchToolProvider` and `wikipediaToolProvider` return **nothing** when their
configuration is absent (`agentkit/build/nettools.go`: `if cfg == nil { return nil, nil }`),
so for them "the file is there" happens to be the switch. ★ That is the **provider's**
decision, not something the file's shape implies — and the filesystem tools show the
other case: they are **always** there, and they have **no file of their own**, so their
tiers live in the code (§6.3). The loader's own rule — the file name
is a whitelist, an unknown name is a startup error (`agentkit/spec/load.go:165-193`) —
is about **which files are read**, not about which tools exist.

★ An empty file is rejected with a hint that names what to write instead
(`agentkit/spec/load.go:151-158`): the content **is** the tool's configuration, so "there
is no configuration" is the useful thing to say.

★ **It takes part in hot reload**: change a tool's timeout, swap a search backend, point the
knowledge base at another corpus — written down, **it takes effect on the next turn**, without
restarting the process. The tools category carries `hot: true`
(`agentkit/spec/config.go:56-60`). That is exactly why it moved out of `server/config.yaml`:
`server/` configures the process itself and does not take part in hot reload, while these changes
have nothing to do with "the process itself".

★ **A same-named file replaces the whole entry**: within one level there is only one file per
name, `tools/http.yaml` is it. This is finer than before: changing `tools.http` in
`server/config.yaml` used to replace `tools.web_search` along with it (one file, replaced
wholesale), now they are unrelated.

★ The knowledge base's `kb.yaml` moved over from the old `kb/config.yaml`. **The index directory
did not move**: it lives in the runtime data (`state/kb/<index>/`,
`agentkit/build/kb.go:42`), and it lives and dies with the corpus.

#### ★ The names each file speaks — and the names the model sees (✅ implemented)

A file declares its tiers in **its own vocabulary**, and the provider maps that to the
name the model sees. The two are not the same, and that is the point:

| File | The vocabulary the file speaks | The names the model sees |
|---|---|---|
| `http.yaml` | the method: `post` | `request_post` |
| `web_search.yaml` | the provider: `duckduckgo` | `duckduckgo_search` |
| `wikipedia.yaml` | — (one tool) | `wikipedia_search` |
| `kb.yaml` | `kb_search` `kb_index` | the same |
| `mcps/<server>.yaml` | the server's own name: `create_issue` | `mcp__github__create_issue` |

★ **The filesystem tools have no row, and that is the point.** They come from the
framework's middleware rather than from a provider, so they have **no file of their
own** — their exceptions live in the code (§6.3), and only there.

★ **Before the split, this table did not exist** — the policy file spoke the third
column while the tool's own file spoke the second, so `http.yaml` said `post` and
`permissions.yaml` had to say `request_post`. ★ Two vocabularies for one tool is a
mismatch waiting to happen, and a mistyped name is invisible until the tool behaves
differently than the file says.

⇒ **A file's `tools:` and `allows:` are written in the second column.** The provider
translates; nothing else has to know the exposed name.

★ **The MCP provider composes the exposed name in exactly one place**
(`agentkit/build/mcptools.go:140` `mcpExposedName`), so the provider and the projection
cannot disagree about what a tool is called — and the file's own `allows:` names the
server's **own** tools, never the exposed ones
(`agentkit/build/mcptools.go:122` `mcpOffers`).

(The exposed names are `agentkit/spec/decl/decl.go:252-257`, `kb/tools.go:16-17` and eino's
`adk/middlewares/filesystem/filesystem.go:38-46`.)

#### ★ The keys every file carries (✅ implemented)

Every file in `tools/` and in `mcps/` carries the same keys, and they mean the same
thing everywhere:

| Key | Type | Default | Meaning |
|---|---|---|---|
| `tools` | `[]string` | all of them | ★ **a filter, not a switch** — the **raw** names (this file's own vocabulary, *not* the exposed ones) to offer **out of the tools the provider gives**. Absent = all of them |
| `allows` | `[]string` | empty | the names that run **without a nod** — **in this file's own vocabulary**. Absent or empty = every tool this file offers asks |

★ **The default is `ask`, and `allows:` is the exception.** Every tool asks first unless
something says otherwise. There is no per-group default and no special case: the same
rule covers the filesystem tools, the HTTP tool and an MCP server.

★ **The code ships one allow list**, for the tools that have **no file of their own** —
`ls`, `read_file`, `glob`, `grep`, `task`, `write_todos`, `skill`
(`agentkit/build/tools.go:61` `builtinAllowedTools`). ★ The delegation tools are in it
because an agent that cannot delegate without a click cannot delegate at all, and the
filesystem readers are in it because they only read.

★ **A file's `allows:` names the exceptions for the tools that file provides**, in that
file's own vocabulary — `tools/http.yaml` says `allows: [request_get]`,
`tools/kb.yaml` says `allows: [kb_search]`. The loader collects them
(`agentkit/spec/load.go:235` `AllowedTools`), and the table is the union of that list,
the code's list and the servers' lists (§6.3). ★ A file that is **absent** contributes
nothing: the tool is not mounted either, so there is no tier to answer.

★ **There is no `deny` in a file, and there is no global list any more** (§6.3). "Not
exposed" is said in exactly two places: the file's **whitelist** — each file calls it
what fits it, `methods:` for HTTP (`http.yaml`), `tools:` for the rest — and an agent's
**`disabled_tools`**.

★ **The global list went away because it was the third place for one decision, and the
only one that had to speak the **exposed** names while the tools' own files speak
theirs. ⇒ one mechanism per level, one vocabulary per level.**

★ **The two keys answer two different questions, and neither subsumes the other:**
`tools:` says **what is offered**, and `allows:` says **which of those run without a
nod**. Everything else asks.

★ **`allows:` is the whole of it.** There is no second key and no per-group default:
the default is `ask`, and the list names the exceptions. A file that lists nothing asks
about everything it offers.

★ **An `allows:` list covers the tools the server has not offered yet**, because it is
a statement about the **server**, not about the names it happens to have right now. A
server's tool list is only known at runtime, so a rule that named the tools could not
cover a tool the server adds tomorrow — the list can, because it names the exceptions
in the server's own vocabulary and the rest asks by default.

#### ★ The default is ask, and `allows:` is the exception

★ **One rule, everywhere.** A tool asks first unless something says otherwise. The
exceptions are named in the **`allows:`** list of the file that provides the tool, in
that file's own vocabulary — and, for the tools that have **no file of their own**, in
the code.

★ **The namespace's default is `ask`** (`agentkit/build/policy.go:55` `TierOf`) — ★ the
table is assembled from the **configuration**, so the tier is answerable before the
server is even up (`agentkit/build/mcptools.go:172` `mcpAllowedNames`).

★ **The code ships one allow list**, for the tools that have no file of their own:

```
ls  read_file  glob  grep
task  write_todos  skill
```

★ **Why the code has one at all.** `task` — the delegation tool — has **no file of its
own**: it comes from the framework's agent, not from a `tools/` entry. Under "ask by
default" it would ask before every delegation, and an agent that cannot delegate
without a click cannot delegate at all. ⇒ the framework's own tools are the exception
that only the code can express.

★ **A file's `allows:` names the exceptions for the tools that file provides.** So
`tools/http.yaml` may widen the set (`request_post` does not ask) or narrow it
(`request_get` asks after all).

| File | Its `allows:` | The rest |
|---|---|---|
| `tools/http.yaml` | the read methods | the write methods → ask |
| `tools/web_search.yaml` | `duckduckgo_search` | — |
| `tools/wikipedia.yaml` | `wikipedia_search` | — |
| `tools/kb.yaml` | `kb_search` | `kb_index` → ask (it writes the index) |
| ★ `mcps/<server>.yaml` | usually empty | ★ **everything asks** — the server's tools are discovered at runtime, openheron cannot know what `delete_repo` does, and under stdio the server is a **child process** with the agent's own privileges (§6.7) ⇒ the cost of guessing wrong is asymmetric: one extra click against a destructive call nobody saw |

★ **A trusted MCP server says so once** — and the exceptions are the server's own tool
names:

```yaml
allows: [read_issue, list_issues]
```

★ **The shipped templates carry their own `allows:` lines** — `tools/http.yaml` says
`allows: [request_get]`, `tools/web_search.yaml` says `allows: [duckduckgo_search]`,
`tools/wikipedia.yaml` says `allows: [wikipedia_search]` and `tools/kb.yaml` says
`allows: [kb_search]` — so the tiers are **visible and editable** rather than implied,
and a fresh workspace starts with its read-only tools running straight through. ★ The
optional integrations stay behind `--tools`: which of them exist is the user's call, and
the filesystem set is not optional.

#### ★ A tier change takes effect on the next turn

★ `tools/` and `mcps/` are **hot** partitions, so a tier is not special: changing one
takes effect on the next turn, like any other key in the partition. The policy table is
rebuilt with the snapshot, so the gate reads the new tier from the next turn on — no
restart, and no stale window where a tool keeps the old tier.

★ The one thing a reload has to get right is the **client**: when the snapshot is
replaced, the stdio child process is torn down and a new one is started (§5.4 of
`docs/mcp-design.md`). A hot partition whose resources cannot be rebuilt is a partition
that lies about being hot.

#### ★ The filesystem tools: their exceptions live in the code

The filesystem tools come from the framework's middleware rather than from a provider, so
they have **no file of their own** — and that is exactly why their exceptions live in
the code (`agentkit/build/tools.go:61` `builtinAllowedTools`).

The set is eino's filesystem middleware: `ls`, `read_file`, `write_file`, `edit_file`,
`glob`, `grep`, `execute` (`eino@v0.9.22/adk/middlewares/filesystem/filesystem.go:38-46`).

```
ls  read_file  glob  grep
```

★ **The readers run straight through; the writers ask.** `write_file`, `edit_file` and
`execute` are not in the list, so they interrupt the turn for a human — which is the
right way round: they are the three tools that change the machine.

★ **A file of their own would be a tuner, not a switch** — the filesystem tools are the
agent's core capability, not an optional integration. ★ **`tools/filesystem.yaml` is not
implemented and is not going to be**: `tools/` accepts `http.yaml`, `web_search.yaml`,
`wikipedia.yaml` and `kb.yaml` and nothing else (`agentkit/spec/load.go:191`), so writing
that file is a **load-time error**, not a silent no-op. ★ The narrowing is already
expressible, and in the place that owns it: an agent's `disabled_tools` (§6.2).

★ **The consequence, stated plainly**: `write_file`, `edit_file` and `execute` have no
file, so **nothing can name them in an `allows:` list** — they ask, always
(`agentkit/build/tools.go:61` is a closed list in the code). A user who wants an
unattended agent that runs commands cannot get one today; the levers are narrower than
they were under `policy/permissions.yaml`, which could move `execute` out of `ask`. That
is a **known gap**, not a documented feature — an agent's own `disabled_tools` can take a
tool away and nothing can hand one back.

#### ★ Migration (what about the old configuration)

This move is **destructive** — the old spelling **errors out at load time**, it is not silently
ignored:

| Old spelling | Now | Symptom |
|---|---|---|
| the `tools:` section in `server/config.yaml` | split into `tools/http.yaml` `tools/web_search.yaml` `tools/wikipedia.yaml` | a startup error: `field tools not found in type decl.Server` |
| `kb/config.yaml` | `tools/kb.yaml` | **silent** — the knowledge base is no longer read, `kb_search` disappears and nothing says so |
| `ask:` in `policy/permissions.yaml` | the tiers live in the files that **provide** the tools: `tools/http.yaml` `tools/web_search.yaml` `tools/wikipedia.yaml` `tools/kb.yaml` and the `mcps/<server>.yaml` files name their own `allows:` | ★ **not silent**: the retired directory is reported in `deferred` and printed at startup — "policy/ is gone and was not read", together with where its content went (`agentkit/spec/config.go:329`) |
| `policy/limits.yaml` | the caps live in `server/config.yaml`: `agent.max_depth` and `retry.max_retries` | ★ **the same notice** — and a topology deeper than the cap is a **startup error** rather than a silent truncation (`agentkit/spec/load.go:615` `validateDepth`) |
| the `corpus` key in `tools/kb.yaml` | `corpus_dir` | ★ **not silent**: the old key still loads and `deferred` carries a rename notice at startup — ★ the knowledge base keeps working until the key is renamed, so nothing goes dark (`agentkit/spec/load.go:209-215`) |

★ That "silent" one is the part of this move to watch. `kb/` is not one of the loaded categories
(`agentkit/spec/config.go:40-70`), so an old `kb/config.yaml` is simply never read: its `corpus`
key (now `corpus_dir` — see the table above) looks "not configured" — and an empty corpus = the
knowledge base off. The symptom is "`kb_search` is gone", and the startup log does not say a
word. ⇒ when migrating, **`mv` the file first, then start** — do not expect the startup error to
remind you.

★ **And the same is true of `policy/`** — a leftover `policy/` directory is **not read**,
but it is **not silent** either: the loader names the retired partition in `deferred`
and says where its content went (`agentkit/spec/config.go:305-338`
`noteRetiredDirs`), so the notice is printed at startup. ★ The direction of the change
is the safe one: a tool nobody names **asks**, and the code's own list keeps the readers
and the delegation tools out of the way. But the *exemptions* the old file granted — a
search backend, an HTTP GET — are gone with it, and the replacement is a line in the
file that **provides** the tool (§6.6).

#### The full field list

| File | Field | Type | Default | Meaning |
|---|---|---|---|---|
| `http.yaml` | `methods` | `[]string` | `[get]` | ★ **only the whitelisted methods**; post/put/delete can change the outside world, so they have to be named explicitly |
| `http.yaml` | `headers` | `map[string]string` | `{}` | the headers carried by every request |
| `http.yaml` | `timeout` | `LenientDuration` | `30s` | the cap on a single request |
| `web_search.yaml` | `provider` | `string` | `duckduckgo` | the search backend; **an unknown value does not degrade silently** — the whole group is not mounted and the log speaks (`agentkit/build/tools.go:64-66`) |
| `web_search.yaml` | `max_results` | `int` | `10` | how many results one search returns at most |
| `web_search.yaml` | `timeout` | `LenientDuration` | `60s` | the cap on one search (a search paginates, so it gets more room than HTTP) |
| `web_search.yaml` | `region` | `string` | `""` → worldwide | duckduckgo's `kl` parameter (`wt-wt` / `cn-zh` / …) |
| `wikipedia.yaml` | `top_k` | `int` | `3` | how many results |
| `wikipedia.yaml` | `language` | `string` | `zh` | the language of the wiki |
| `wikipedia.yaml` | `timeout` | `LenientDuration` | `15s` | the cap on one lookup |
| `wikipedia.yaml` | `base_url` | `string` | `""` → the official one | the API endpoint (for a self-hosted mirror) |
| `kb.yaml` | `corpus_dir` | `string` | `""` → off | the corpus directory, **relative to the project root**; an absolute path is rejected (the index and the corpus live and die together) |
| `kb.yaml` | `index` | `string` | `default` | the index directory name, landing in `state/kb/<index>/` |
| `kb.yaml` | `model` | `string` | `""` | the embedding model, `<provider>/<model>`; it must be `kind: embedding`; **required** when `corpus_dir` is non-empty |
| `kb.yaml` | `query_instruction` | `string` | `""` | an instruction prefix added on the query side only |
| `kb.yaml` | `batch_size` | `int` | `10` | how many entries one embedding request carries at most |
| `kb.yaml` | `max_batch_chars` | `int` | `1200` | the character cap of one batch's text |
| `kb.yaml` | `top_k` | `int` | `8` | how many entries `kb_search` recalls by default |
| `kb.yaml` | `splitter.headers` | `map[string]string` | `{}` | ★ parsed, but **no longer effective** since v2 (the sections come from line scanning) — `kb status` says so out loud |
| `http.yaml` | `allows` | `[]string` | `[]` | ★ the names that run **without a nod** — in **this file's own vocabulary** (`request_get`, `request_post`, …). Absent = every method this file offers asks |
| `web_search.yaml` | `allows` | `[]string` | `[]` | the same: `duckduckgo_search` |
| `wikipedia.yaml` | `allows` | `[]string` | `[]` | the same: `wikipedia_search` |
| `kb.yaml` | `allows` | `[]string` | `[]` | the same: `kb_search` — `kb_index` writes the index and asks |

★ **The defaults of these fields live in the provider that reads them, not in `decl`.** The
declaration structs carry the shape only (`agentkit/spec/decl/decl.go:303-321` for the three tool
configurations, `agentkit/spec/decl/decl.go:538-556` for the knowledge base); the values are in
the provider (`agentkit/build/nettools.go:22-27` for the three timeouts,
`agentkit/build/nettools.go:210-217` for the search defaults,
`agentkit/build/nettools.go:246-253` for the Wikipedia defaults) and in
`agentkit/spec/defaults.go:49-56` for the knowledge base. The shipped templates spell them out
(`agentkit/spec/write.go:219-256`) — that file is the shortest way to see the whole surface.

★ Two of the shipped templates' comments are ahead of the code, and both cost a confused user:
the `web_search.yaml` template mentions a second provider, `searxng`
(`agentkit/spec/write.go:233`), but the code accepts `duckduckgo` and nothing else — a `searxng`
value is rejected as an unknown provider (`agentkit/build/nettools.go:204-208`); and the
`kb.yaml` template says an empty `model` means pure lexical retrieval
(`agentkit/spec/write.go:249`), but the loader rejects it: `kb.model is required when kb.corpus_dir
is set` (`agentkit/spec/load.go:603`).

★ The file name is a **whitelist** (`http.yaml` / `web_search.yaml` / `wikipedia.yaml` /
`kb.yaml`): an unknown file name is a **hard error at load time**
(`agentkit/spec/load.go:191` — `tools/ only accepts http.yaml, web_search.yaml, wikipedia.yaml and
kb.yaml`) — writing a section that will never take effect has the symptom "I wrote it and it did
not take effect", which is extremely hard to debug.

#### ★ The three kinds of tools that reach outside

The three kinds of tools under `tools/` differ from the knowledge base in one essential way:
**their output enters the model context verbatim**. The knowledge base recalls text you wrote
yourself; here it is a page somebody else wrote — so "the instructions inside external content"
get into the model's head as well. The tier layer (`allow`/`ask`) governs "may this tool
be called"; it cannot see the URL, and it cannot see the words in the result.

So the rules of this family are (the implementation is in `agentkit/build/nettools.go`):

| Rule | Why |
|---|---|
| **off by default** | with nothing configured not one is mounted — mounting a `web_search` that never finds anything is worse than not mounting it. For this family the switch is "**whether that file is there**", there is no `enabled` field ✅ |
| **the public network only** | for the HTTP tool the URL is the model's. An HTTP tool that can reach `127.0.0.1` = the model can read this machine's configuration, the session state, the cloud metadata service (`169.254.169.254`) |
| **the output has a length cap** (20000 bytes) | a 10MB page blows this turn up — the symptom is "the model suddenly starts talking nonsense", not an error |
| **write methods have to be named explicitly** | POST/PUT/DELETE can change the state of the outside world, and the model does not need a reason to invent a POST |

Each provider mounts nothing when its file is absent
(`agentkit/build/nettools.go:129-131`, `agentkit/build/nettools.go:197-199`,
`agentkit/build/nettools.go:243-245`), and the cap is `defaultToolOutputBytes`
(`agentkit/build/nettools.go:26`).

"The public network only" is decided **at the moment of dialing**, and it connects to **the IP it
just checked** (`agentkit/build/nettools.go:48-76`) — DNS can change its answer between two
lookups (rebinding), so judging only at resolve time is a bypass.

★ **The Wikipedia tool is the one that is not behind the dialer.** The public-only client guards
the two tools the model can point somewhere: the HTTP tool and the web search. The Wikipedia
tool's endpoint comes from `wikipedia.yaml`'s `base_url`, and eino-ext's Wikipedia tool builds its
own HTTP client — there is no hook to inject one (`agentkit/build/nettools.go:259-266`). The
output cap does cover it (`agentkit/build/nettools.go:270`).

★ **MCP is the fourth kind of this family, and it is the more dangerous one**: the table above
limits "what the **tool** can reach", while a stdio MCP server is a **child process** of
openheron — it can write files and it can reach the network, so the "public network only" rule
does not hold for it. ⇒ giving the MCP tools a tier relies on the **namespace plus the
server's own file** (§6.7), not on giving each tool a network rule of its own.

#### ★ Once it is enabled it is usable: the tiers and the fine-grained control

"Writing `tools/http.yaml` **is** that decision ✅ — the tool is mounted, and it does
not have to be named once more anywhere ✅ (saying the same thing twice). ★ But
"mounted" is not "allowed": the tier is a **separate** question, and its answer is the
file's own `allows:`.

| Who decides | What it governs |
|---|---|
| ★ **the provider** (`defaultToolRegistry`, `fsx.New`) | ★ **the tool set itself** — which tools exist at all |
| the file in `tools/` | the **parameters** of those tools, and **which of them run without a nod** (`allows:`). ★ For MCP it also carries the server's definition — and `tools:` picks the ones to offer out of what the server offers |
| ★ **the code** (`builtinAllowedTools`) | the exceptions for the tools that have **no file of their own** — the filesystem readers and the framework's `task` (§6.3) |
| each agent's `mcps:` | **which servers this agent loads** (§6.2) |
| each agent's `disabled_tools` | **narrowing**: this one agent does not get these tools at all (the inner layer, it can control that) |

★ A tool's tier is therefore read from **two** places, and the order is fixed: the
file that provides the tool — or the code, for the tools that have no file — and then
the agent's `disabled_tools`, which removes the tool for that agent and nothing else.

★ **The fallback tier is `ask`** ✅ — a tool that nobody names in an `allows:` list
interrupts the turn for a human ✅ ✅. The code's own allow list is what keeps the
read-only tools and the framework's `task` from asking every single time ✅.

★ The knowledge base (`kb.corpus_dir`) is **not** switched on by the file's presence but by a
non-empty `corpus_dir` (`agentkit/build/kb.go:15`), and its two tools go through the tiers like
everything else: `kb_search` is in `allows:`, and `kb_index` — a write operation — asks
by default (`tools/kb.yaml`).

### 6.7 `mcps/<server>.yaml` (★ decided 2026-11-06, ✅ implemented)

The configuration of an MCP server. **One file per server**:

```
.openheron/mcps/
└─ github.yaml
```

★ **It does not go into `tools/`** — §2's decision 4 is "one entity, one file", and the
entity in MCP is the **server**: a server has its own command, arguments, environment,
transport and lifecycle. `tools/`'s rule is "one file = one tool" (§6.6's file-name
whitelist); putting N servers into one file means a nested list under a partition whose
rule is "the file content is the configuration, with no wrapper key" — and when the whole
file is replaced, there is no way to say which server changed.

★ The complete architectural design (the assembly path, the client lifecycle, the relation
to the gate, where it lands in the sandbox) is in `docs/mcp-design.md`. This section covers
the **configuration surface** only.

★ **The file says what a server is; the agent says who gets it.** `mcps/<server>.yaml`
describes the server — its command, its transport, its tier. ★ Which agents load it is
the agent's own `mcps:` list (§6.2): absent = every configured server, `[]` = none.
⇒ the common case is **one** place: write the server file, and every agent that has
not narrowed its list gets it.

#### The whole field set

| File | Field | Type | Default | Meaning |
|---|---|---|---|---|
| `<server>.yaml` | `transport` | `string` | `stdio` | `stdio` \| `http`; ★ an **unknown value is a hard error**, no silent fallback to stdio. ★ `http` means **streamable HTTP only** — there is no SSE transport |
| | `command` | `string` | — | stdio only, **required**: the executable to run. ★ It is resolved in the sandbox's PATH, **never** against the project root |
| | `args` | `[]string` | `[]` | stdio only: its arguments |
| | `env` | `map[string]string` | `{}` | variables **layered over** the inherited environment; ★ the child's environment is openheron's own, with these overriding the same names — and that inheritance is what carries a credential's sentinel to the server without a field here. ★ stdio only: on an http server it is a **load-time error**, even when the map is empty |
| | `url` | `string` | — | http only, **required**: the server address |
| | `headers` | `map[string]string` | `{}` | http only: sent on every request |
| | `timeout` | `MCPDuration` | `30s` | the ceiling for connect + initialize + list tools |
| | `call_timeout` | `MCPDuration` | `60s` | the ceiling for **one tool call** — ★ without it, a server that never answers holds the call lock and every other call of that server queues behind it |
| | `tools` | `[]string` | `[]` → all | ★ **a filter, not a switch** — the server's **raw** tool names to offer **out of the ones it has** (not the namespaced ones); absent or empty = all of them |
| | `allows` | `[]string` | `[]` | the server's **own** tool names that run **without a nod** — ★ everything else this server offers asks, including the tools it has **not offered yet** |

★ `timeout` and `call_timeout` are **lenient** durations: a wrong value degrades to the
default and is recorded in `deferred`, exactly like `debounce` and `start_timeout`. A
wrong timeout changes *how long we wait*, not *what happens* — it must not keep the
process from starting.

★ ★ **The type is `MCPDuration`, not the package-level lenient list.** The lenient
durations every other key uses carry no source, so a bad value cannot say which file
and which key were wrong (`agentkit/spec/decl/decl.go:421`). This is a deliberate
deviation from the original design, and it is the reason the two MCP timeouts have
their own type.

★ `tools: []` means **all**, not **none**. An empty whitelist that meant "nothing" would
be indistinguishable from a missing key.

#### The load-time errors

Every one of these fails the **load**, not the run — the file names the mistake:

| The mistake | What it says |
|---|---|
| a server name that does not match `^[a-z0-9]+(_[a-z0-9]+)*$` | the file's name **is** the server's name, and a name that could not be read back is refused (`agentkit/spec/load.go:256` `loadMCP`) |
| `transport:` with an unknown value | no silent fallback to stdio — a typo must not run a different server than the file names |
| a stdio server without `command` | `command is required for a stdio server` |
| an http server without `url` | `url is required for an http server` |
| an http server that declares `env:` | ★ **even when the map is empty**: an http server is a remote process, this process's environment is not its environment, and a key that does nothing is worse than a refusal (`agentkit/spec/load.go:375` `yamlHasKey` — the decoder cannot answer it, because an `env:` with no value decodes into a nil map) |
| an unknown key | the same strict decoding as every other partition |

★ **An agent naming a server that does not exist is a load-time error too** — that one
belongs to the agent's file, not the server's (§6.2, `agentkit/spec/load.go:499`
`validateMCP`).

#### The hot reload

★ **The partition is hot.** The category carries `hot: true`
(`agentkit/spec/config.go:61-69`), so `hotDirList()` (`agentkit/spec/config.go:87`) puts
`mcps/` into `HotDirs` (`agentkit/spec/reload.go:21`). Changing a server's command, its
timeout **or its tier** takes effect on the next round, without a restart.

★ **What a reload has to get right is the client.** The stdio child process is torn down
and a new one is started; a changed declaration replaces the client
(`agentkit/build/mcpmanager.go:215` `ensure`), and a changed tool list bumps the
generation through the same hook the configuration reloader uses
(`agentkit/build/builder.go:67`, `:105`). ★ A hot partition whose resources cannot be
rebuilt is a partition that lies about being hot.

#### The file name is the namespace

The server name is `<server>.yaml` without `.yaml`, and it is at the same time the
namespace segment of the tool name:

```
mcp__<server>__<tool>
```

| Segment | Who decides | Rule |
|---|---|---|
| `server` | **we do** — the file name | must match `^[a-z0-9]+(_[a-z0-9]+)*$`; a violation is a **load-time hard error** |
| `tool` | **the server does** | characters outside `[A-Za-z0-9_-]` are replaced with `_`; case is preserved |

★ **The server name may not contain a run of underscores** — otherwise
`mcp__a__b__x` cannot be read back (is that server `a` with tool `b__x`, or server
`a__b` with tool `x`?), and §6.3's prefix syntax cuts on `mcp__<server>__`.

★ **The namespace also settles "colliding with a reserved tool name"**: a server that
offers `ls` becomes `mcp__<server>__ls`, and no longer collides with the reserved names
like `ls` / `execute` (`agentkit/build/tools.go:46`). The reverse also holds, and it is a
rule of its own: **only MCP tools may use the `mcp__` prefix**.

★ **The composed name is never parsed back** — the tier layer matches whole names and
prefixes only, so the tool segment can keep its original case and characters. The name
is composed in exactly one place (`agentkit/build/mcptools.go:140` `mcpExposedName`).

★ **A composed name longer than 64 characters is skipped with a warning**, and so is
the second of two tools of one server that sanitize alike
(`agentkit/spec/decl/decl.go:406`, `agentkit/build/mcptools.go:27-28`). ★ Never a
load-time failure: the name is chosen by a remote server, and a server that adds one odd
tool should cost that tool, not the whole agent.

#### The file defines the server

There is no `enabled` field. A server is **defined** because its file exists; removing
the file removes the definition. ★ **Whether the server exists** lives in one place
only — not split between "the file is there" and "the flag inside it says on".

★ **The file defines the server; it does not decide what the server offers.** The tools
a server has come from the server itself, and the file's `tools:` picks the ones worth
offering out of them (§6.7's key table).

#### The junction with the tier table

★ **The tier of this family of tools is written in the server's own file**, in the
server's own vocabulary — the typical form:

```yaml
# .openheron/mcps/github.yaml
#
# ★ The default is ask, so an empty list means "every tool of this server asks".
#   A trusted server names its exceptions:
allows: [read_issue, list_issues]
```

★ An empty `allows:` means "every tool this server offers needs a nod" — a whole
**class** of external tools gets a tier, without writing the tool names down first.

★ **And it covers the tools the server has not offered yet.** A server's tool list is
known only at runtime, so a rule that named the tools could not cover a tool the server
adds tomorrow. The prefix syntax in §6.3 could never do that for the `ask` tier — it
survives there only for an agent's **`disabled_tools`** (`mcp__*` = this agent gets no
MCP tool at all).

★ **To take one tool out of a server, the server's own `tools:` whitelist leaves it
out** — in the server's own vocabulary, which is where that decision belongs:

```yaml
# .openheron/mcps/github.yaml
tools: [create_issue, list_issues]
```

★ And `allows:` is the other half of the same file: it names the ones that need **no
nod**. The whitelist says *which* are offered; the allow list says *which of those run
straight through*.

★ **The tier is answerable before the server is even up.** The table is assembled from
the **configuration** — the file's `allows:` composed into the exposed names
(`agentkit/build/mcptools.go:172` `mcpAllowedNames`) — so the gate's lookup never waits
for a connection. ★ And the namespace's default is `ask`
(`agentkit/build/policy.go:55` `TierOf`).

★ **The permissions of a stdio server are the permissions of the process**: the MCP server
is a child process that openheron starts, it can write files and it can reach the network.
The "public network only" limit that §6.6 gives the HTTP tools **does not hold** for it
(that limit says what the *tool* may touch, not what the *server* may do). This is the
reason the tier belongs to the server's file rather than to a shared list.

★ **When one server cannot be reached**: its tools are not mounted at all + one warning
(with the server name, the transport and the error), **and it affects neither the other
servers nor the startup of the process**. Silently dropping it is the worst outcome — the
symptom would be "this feature seems to be missing".

#### The credential

★ **This section has no credential fields, and that is the point.** The key an MCP
server needs is the **server's** business — it knows which variable it reads. The
sandbox runtime injects a **sentinel** into the container's environment, and the
child's environment is this process's environment (`docs/mcp-design.md` §5.5), so the
server finds it without anything being written here.

★ **The one thing the file does carry is the environment itself** — `env:` is where the
server's own variables are named, and an unset `${ENV}` is **deferred**, not fatal: the
entry is left out and the server starts with the inherited environment instead
(`agentkit/spec/load.go:327` `expandMCPMaps`). ★ A missing value is a downgrade, not a
reason to keep the process from starting.

The full path — the mixin that declares the credential, the host-side proxy that
substitutes it, and the cases where the mechanism does not reach — is in
`docs/mcp-design.md` §8.1.

#### The template

★ **The configuration editor offers the scaffold.** `Template(spec.DirMCP, …)`
(`agentkit/spec/write.go:189`) writes a **loadable** file and says what is missing — a
file under `mcps/` **defines** a server, so a file that cannot be parsed is not a server
that is off, it is a configuration that does not load. The write gate refuses a file
whose name is not `<server>.yaml` with a valid server name
(`agentkit/spec/write.go:107`).

★ **The factory sample is a loadable file, too.** `openheron init` writes
`mcps/example.yaml`, from the template file `agentkit/templates/files/mcps/example.yaml` —
★ **one file, two readers**: the configuration editor scaffolds it
(`agentkit/spec/write.go`) and `openheron init` writes it (`initcfg/initcfg.go`). ★ Both
live in different layers, so the file lives in a **leaf package** they can both depend
on — a copy would drift, and the copy is what the dependency shape used to force. Its
`command` is a placeholder that is not installed: the server does not come up, the log
says so, and the agent carries on. ★ **Copying it to `mcps/<server>.yaml` and putting your
own command in it is the act of defining that server.**

#### Implementation status

★ **This section is implemented.** The partition, the loader, the provider, the tier
table and the editor's scaffold are all in the tree; the cases are in
`agentkit/spec/mcp_test.go`, `agentkit/build/mcptools_test.go`,
`agentkit/build/mcpmanager_test.go` and `agentkit/build/mcpstatus_test.go` (§11.7).

★ **The whole model is implemented, and so is its neighbourhood.** The global tier list
is gone (§6.3), the default is `ask`, and the exceptions are named by the file that
**provides** the tool — the server's own file for MCP (§6.7), the `tools/` files for the
rest (§6.6). ★ The one thing that is still **not** implemented is a file of the
filesystem tools' own: their exceptions live in the code, because they have no file to
live in (§6.6).

## 7. AgentHub / ModelHub: eino gives the interfaces only

Verified: `eino@v0.9.22/adk/middlewares/skill/skill.go:82-97` provides the two
**interfaces** `TypedAgentHub` / `TypedModelHub`, and eino itself implements nothing —
openheron has to build its own. ✅ openheron has built it: the implementation is
`agentkit/build/hub.go`.

```
SKILL.md frontmatter
  ├─ agent: <name>             → AgentHub.Get(ctx, name, opts)   eino@v0.9.22/adk/middlewares/skill/skill.go:527
  └─ model: <provider>/<model> → ModelHub.Get(ctx, name)        eino@v0.9.22/adk/middlewares/skill/skill.go:520
```

| Hub | Returns | When it is needed |
|---|---|---|
| `AgentHub` | `adk.TypedAgent[*schema.Message]` | `context: fork` / `fork_with_context` |
| `ModelHub` | `model.BaseModel[*schema.Message]` | the frontmatter declares `model:` |

**`ModelHub` and `agents/*.md` share one reference syntax**: eino passes the raw
frontmatter string as `name`, and openheron's `Get` splits it with `ParseRef`
(`agentkit/spec/load.go:437`) into `<provider>/<model>` and looks it up in `Config.Models`
(`agentkit/build/provider.go:58-73`). So "which model an agent uses" and "which model a
skill uses" are the same reference syntax — the user does not have to learn two of them.

`AgentHub.Get(ctx, "", opts)` must return the **default agent** — eino says so explicitly
("when `skill.Agent` is empty, `Get("")` is called, and the implementation should return
the default agent", `eino@v0.9.22/adk/middlewares/skill/skill.go:146-147`).

`adk.TypedResumableAgent[M]` embeds `TypedAgent[M]`
(`eino@v0.9.22/adk/interface.go:481-485`), so `deep.New`'s return value
(`eino@v0.9.22/adk/prebuilt/deep/deep.go:171`) **directly satisfies** the return type of
`AgentHub.Get` — **no adapter layer** is needed. openheron's own build path goes through
`agentkit/build/assemble.go:39-41`.

#### Implementation (`agentkit/build/hub.go`; `agentkit/build/skill.go` only wires it up, ✅ implemented)

| | Behaviour |
|---|---|
| `ModelHub.Get(ref)` | goes through `llm.BuildModel`: parses the reference + builds the instance + **attaches the request-level options** (tier / temperature / extra). The cache is split per hub instance (= one configuration generation), so after a hot reload the reference is resolved again |
| `AgentHub.Get("")` | the current agent's pooled instance (eino's default convention) |
| `AgentHub.Get(name)` | a name **declared in this agent's subtree**: rebuilt through the sub-agent's middleware chain (filter → approval → depth guard), carrying its own next-level topology; not cached (the `model:` may change every time) |
| `AgentHub.Get` on an unknown name | errors and lists the available names — so "the skill says `agent: writer` but `writer` is not under this agent" does not become an untraceable empty result |
| `opts.Model` | takes precedence over `decl.SubAgentSpec.ModelRef`: it is the product of eino resolving `model:`, and overriding it by the reference afterwards would make that line of the configuration void |

**★ The model must carry the request-level options.** eino's `ModelHub` return value
**replaces** the agent's model wholesale (`WrapModel`,
`eino@v0.9.22/adk/middlewares/skill/skill.go:281-300`). Returning a bare instance would
make "the skill switches the model" silently take the reasoning tier and the sampling
parameters with it, and it would not report an error. openheron attaches them in
`agentkit/build/provider.go:93`.

**★ Both hubs must be wired, not just one.** A missing `ModelHub` makes the `model:` in
inline mode **silently ignored** by eino
(`eino@v0.9.22/adk/middlewares/skill/skill.go:282-284`); a missing `AgentHub` makes fork
mode fail outright (`eino@v0.9.22/adk/middlewares/skill/skill.go:511-513`). The former is
exactly the "it was configured but nothing happened" case.

★ **The skill's model comes from the configuration, not from the calling agent.** The hub
resolves the reference with an empty `CallIntent` (`agentkit/build/hub.go:44`), so the tier
falls back to the model's own `reasoning_default`
(`agentkit/build/provider.go:97-103`). A skill names a *model*, not an agent; borrowing the
calling agent's per-agent temperature would silently change what the skill's own `model:`
line says.

**How they are injected**: `agent.newSkillHubs` binds **this generation's** snapshot
`snap.models` into the `ModelHub` (`agentkit/build/hub.go:24-47`), and every resolution
reads the same generation's `snap.cfg`. There is no push-style entry point in this
repository at all — the hub and the assembly are the same generation, so "the skill uses
the new configuration while the agent uses the old one" cannot happen.

The tests that back these claims are in `agentkit/build/hub_test.go`:
`TestSkillModelHubCachesWithinAHub` (`:33`),
`TestSkillModelHubReportsEmptyAndMissingResolver` (`:58`),
`TestSkillAgentHubUnknownNameListsAvailable` (`:72`),
`TestSkillAgentHubEmptyNameReturnsThePooledAgent` (`:86`),
`TestSkillAgentHubForkBuildsNamedAgent` (`:103`),
`TestSkillAgentHubPrefersSkillModelOverride` (`:129`).

The agent pool and the approval chain are described in `agent-design.md (upstream)`; the
agent assembly API is in `agent-design-v2.md (upstream)`.

---

## 8. Hot reload (P1, ✅ implemented)

### 8.1 Partitions

| Directory | Hot reload | Why |
|---|---|---|
| `llm/` `agents/` `tools/` | ✅ | implemented in `agentkit/spec/reload.go`: `HotDirs` + polling + a generation swap |
| `mcps/` | ✅ | implemented: the category carries `hot: true` (`agentkit/spec/config.go:61-69`), so `hotDirList()` (`agentkit/spec/config.go:87`) puts it into `HotDirs` (`agentkit/spec/reload.go:21`) — see §6.7: changing a server's command, its timeout **or its tier** takes effect on the next round, without a restart |
| `server/` | ❌ | it configures the process itself (listen / static dir / the caps), a change needs a restart |

★ This boundary **is guarded by a test** (`TestWatchIgnoresServerDir`,
`agentkit/spec/reload_test.go:233`) — editing `server/config.yaml` must not trigger a
generation swap. The list itself is derived rather than written twice: `Categories`
(`agentkit/spec/config.go:40-70`) carries the `hot` flag and `hotDirList()`
(`agentkit/spec/config.go:87`) turns it into `HotDirs` (`agentkit/spec/reload.go:21`).

★ **A hot partition has to be able to rebuild its resources.** `mcps/` is the first
partition whose resources are **processes**: when the snapshot is replaced, the stdio
child is torn down and a new one is started, and a changed tool list bumps the
generation through the same hook the reloader uses (§6.7). ★ A hot partition whose
resources cannot be rebuilt is a partition that lies about being hot.

★ `tools/` is a partition of its own: write `tools/http.yaml` and the next round uses
the new method whitelist and the new timeout — no restart in between. ★ The same is true
of a **tier**: the tier table is rebuilt with the snapshot, so a changed `allows:` takes
effect on the next turn (§6.6).

**This split is not a convenience of the implementation, it is semantics**: one directory
takes effect when it is edited and the other does not, and mixing them makes people think
"I changed the listen address, why did nothing happen".

### 8.2 The three key choices

| Choice | Why |
|---|---|
| **stat polling** rather than inotify | inotify **silently fails** in containers / WSL / on network file systems (no event arrives and no error is reported); polling behaves the same everywhere. The price is a few `stat` calls per second |
| **the fingerprint is "name + size + mtime"** rather than a content hash | reading every file on every round is pure waste, the configuration is tiny and mtime is precise enough. What has to be avoided is a **missed change**, so the directory name, the file name, the size and the mtime all go in |
| **a 200ms debounce** | saving a large file produces several events; what is wanted is the moment "the save is finished" |
| **a failure does not call `onChange`** | the caller is the generation-swap hook; a false call makes people believe the configuration was swapped |

The debounce's default is 200ms (`agentkit/spec/decl/decl.go:14`); a value that is not
positive falls back to it (`agentkit/spec/reload.go:73-78`), and the poll interval is half
of the debounce with a 20ms floor (`agentkit/spec/reload.go:146-149`).

★ The debounce is read **once**, when the watcher starts
(`agentkit/spec/reload.go:142-145`), and `watch` lives under `server/`
(`agentkit/spec/decl/decl.go:504`) — which is not hot. So changing `watch.debounce` needs a
restart; it is not one of the things a hot reload picks up.

### 8.3 The first load and the reload: two entry points

```text
NewReloader(svc, onChange)
  └─ Reload(): svc.Load() succeeds ⇒ publish and call back; fails ⇒ return the error (a failed first load stops the startup)

Reloader.Reload() (at runtime)
  ├─ svc.Load() succeeds ⇒ swap loaded + onChange
  └─ svc.Load() fails ⇒ keep loaded, return the error; Watch writes one log line
```

A broken configuration **does not kill the process**: when `Reload` fails the old
configuration keeps serving and `Watch` writes one log line
(`agentkit/spec/reload.go:220-222`); only the **first** load failure keeps the process
from starting (there is no "old configuration" to fall back on then) — `NewReloader`
returns the error (`agentkit/spec/reload.go:103-109`) and `setupConfig` turns it into a
startup failure (`main.go:195-204`). A successful swap is logged with the version and the
directories that changed (`agentkit/spec/reload.go:215-218`).

### 8.4 The fingerprint and `Config.Version`

`Service.Fingerprint()` computes the fingerprint of the hot-reload directories, and `Load()`
writes it into `Config.Version` at the end (`agentkit/spec/config.go:282`, after the
validation at `agentkit/spec/config.go:280`).

**How the fingerprint is computed**: for each directory of `HotDirs` under the configuration
root, what goes into the sha256 is

```
<the absolute path of the directory>\n
<file name> <size> <mtime in nanoseconds>\n     ← one line per file, the file names sorted explicitly
```

★ **"name + size + mtime" rather than a content hash**: the configuration is tiny, and
reading every file on every poll is pure waste; what has to be avoided is a **missed
change**, so the directory name, the file name, the size and the mtime all go in
(`agentkit/spec/reload.go:23-34` and `:51-71`). A directory that does not exist **is not
an error** (a new project is empty), it simply contributes nothing to the fingerprint.

**Two uses**

1. **Change detection for the hot reload** — `Watch` compares the fingerprints on every
   round, and a change enters the debounce wait (`agentkit/spec/reload.go:151-181`).
2. **Attribution of "which configuration this run used"** — the configuration can change
   between two turns, so the fingerprint has to be recorded per turn, otherwise debugging
   becomes guessing. It is exposed as `configVersion` by `/v2/health`
   (`api/v2_introspect.go:449`).

★ The fingerprint is **no longer** the generation key of the agent pool: the pool needs a
monotonically increasing counter of a comparable size, and a fingerprint is a string. The
generation is published together with the configuration under one lock by the `agentConfig`
in `main.go` (`main.go:440-457`; the manager interface is `build.ConfigManager`,
`agentkit/build/manager.go:12-14`) — see `agent-design-v2.md (upstream)` §4.2.

### 8.5 The wiring of the generation swap

```
spec.Reloader.Reload() succeeds
        │
        └─ onChange ──► agentConfig.set(cfg)        (cfg + gen are published under one lock, main.go:452)
                                │
                                ▼
                the next Builder.Build asks mgr.Current()   (agentkit/build/builder.go:75)
                                │
                                ├─ snapshotFor: the generation only moves forward (agentkit/build/snapshot.go:104-116)
                                │     ├─ spec / policy / table
                                │     ├─ models / tools
                                │     └─ a new heavyDeps (backend / tool set, built lazily, agentkit/build/snapshot.go:40-45)
                                ▼
                        pool.GetOrBuild(name, gen)   (agentkit/build/builder.go:105)
                          (the entry carries the generation; an old generation no longer hits, agentkit/build/pool.go:33)
```

★ **A failure does not call back**: when `Service.Load` fails the syntax / model reference /
topology validation (`agentkit/spec/config.go:280`), `Reloader` does not call `onChange`, and
the manager keeps the previous
generation (`agentkit/spec/reload.go:111-127`). So there is no need for a separate
`validateModels`, and "the interface reports generation A while the run executes generation
B" cannot happen.

★ **There is no push-style rebinding**: this repository has no `Reconfigure` entry point at
all. Changing the configuration = the manager swaps its content; the next build asks and
gets the new generation. A turn that is in flight still holds the old instance reference and
it is collected by the GC once it finishes — no drain signal is needed.

★ **The model instances are rebuilt with the generation too**: the `sync.Once` of
`heavyDepsOf` belongs to the snapshot (`agentkit/build/snapshot.go:29-45`), so a new
snapshot necessarily means new instances. After editing `llm/*.yaml` or `agents/main.md` the
manager's generation advances by one (`main.go:452-457`), the pool entry's generation no
longer matches (`agentkit/build/pool.go:33`), and `/v2/agents` and the running agents see the
new model at the same time.

---

## 9. Dependency iron rules

| Constraint | Reason | Verification |
|---|---|---|
| `agentkit/spec` **does not import** `agentkit/build` / `agentkit/run` / `api` | configuration must not know who consumes it | `go list -deps ./agentkit/spec \| grep -E "ouranoshong/openheron/(agentkit/build\|agentkit/run\|api)"` must be empty |
| `agentkit/spec` **does not import** hertz / eino | the declaration layer uses only the standard library plus `yaml.v3`; a model appears only as the `model_ref` string, and the instance is resolved at agent-assembly time (see §12.1) | `go list -deps ./agentkit/spec \| grep -E "hertz\|cloudwego/eino"` must be empty |
| `agentkit/run` **does not import** `agentkit/spec` | the contract of `agentkit/run` is the `AgentBuilder` interface; configuration is an implementation detail of `agentkit/build` | `go list -deps ./agentkit/run \| grep "ouranoshong/openheron/agentkit/spec"` must be empty |
| `agentkit/build` → `agentkit/spec` | one-way | — |
| `llm/common` **has no openheron dependency at all** | it is a leaf package: importing `obs` there would break the "the protocol layer does not depend on the runtime" rule — this is the **one** place the standard logger is allowed | `go list -deps ./llm/common \| grep '^github.com/ouranoshong/openheron/'` must print only `llm/common` itself |
| `agentkit/spec` → `persist` | the only persistence primitive it needs | `go list -deps ./agentkit/spec \| grep '^github.com/ouranoshong/openheron/'` must print only `agentkit/obs`, `agentkit/spec/decl`, `persist` and `agentkit/spec` itself |

★ openheron has **two** dependency iron rules, and they are not the same rule: the
first is "`agentkit/spec` does not pull in eino" (the declaration layer stays
framework-free), the second is "`llm/common` has zero openheron dependencies" (the
vendor protocol layer stays runtime-free). Both are restated in
[architecture.md](architecture.md) §2.1.

**What the commands actually print today** (measured in this repository):

```
$ go list -deps ./agentkit/spec | grep '^github.com/ouranoshong/openheron/'
github.com/ouranoshong/openheron/agentkit/obs
github.com/ouranoshong/openheron/agentkit/spec/decl
github.com/ouranoshong/openheron/persist
github.com/ouranoshong/openheron/agentkit/spec

$ go list -deps ./llm/common | grep '^github.com/ouranoshong/openheron/'
github.com/ouranoshong/openheron/llm/common

$ go list -deps ./agentkit/spec | grep -E "hertz|cloudwego/eino"        # empty
$ go list -deps ./agentkit/run  | grep "ouranoshong/openheron/agentkit/spec"   # empty
$ go list -deps ./agentkit/spec | grep -E "ouranoshong/openheron/(agentkit/build|agentkit/run|api)"   # empty
```

★ Note the two **extra** dependencies compared to the upstream design: `agentkit/obs`
(the one log sink — `spec` logs the hot-reload outcome through it) and
`agentkit/spec/decl` (the package that holds the yaml tags; the "declaration and
behaviour are separate" rule, see [architecture.md](architecture.md) §2.1). Neither of
them drags in a framework: `decl` imports nothing of ours at all.

**Why `agentkit/spec` does not go into `agentkit/run`**: if `Runtime` read the
configuration directly, the configuration would become part of the runtime, and the
`agentkit/run` unit tests would have to prepare configuration files. Keep the
interface inverted and injected, so the tests can inject a fake implementation.

### 9.1 ~~Exception~~: `agentkit/spec` → `agentkit/run` once held — upstream retracted it, openheron inherited the retracted form

**This history is kept, because it is the basis for "why is the structure like this
now".**

Upstream, the configuration write and the checkpoint write both needed "the reader
must never see a half-written file". `storage/filestore/checkpoint.go` originally
carried its own `writeAtomic`; S1 extracted the shared implementation into
`internal/atomicfile`; in 2026-10 the decision was made to move it back into
`agentkit/run`, and `agentkit/spec` imported `agentkit/run` for a while.

★ **openheron never had this exception.** The code surface was extracted after the
retraction, and in the very first commit `agentkit/spec` mentions `agentkit/run` only
in a **comment** — the dependency was never there:

```
$ git grep -n "agentkit/run" fa7131a -- agentkit/spec
fa7131a:agentkit/spec/config.go:26:// 谁消费它由消费方决定 —— agentkit/run 尤其不能依赖它（否则单测要准备配置文件）。
```

★ **The packages upstream named have different names here**:

| Upstream | openheron |
|---|---|
| `internal/atomicfile` | `persist/atomicfile.go` — a **top-level** leaf package (openheron has no `internal/` directory) |
| `storage/filestore/checkpoint.go` | still `storage/filestore/checkpoint.go`, and it calls `persist.AtomicWrite` (`storage/filestore/checkpoint.go:232`) |
| `agentkit/run` | still `agentkit/run` |
| `heron/persist` | `persist` (top-level) |

★ **The conclusion is unchanged**: the atomic-write / file-lock primitives live in a
leaf package `persist` that depends only on `fmt` / `os` / `path/filepath` and holds
no runtime concept at all (`persist/atomicfile.go:3`); `agentkit/spec`, `kb` and
`storage/filestore` each depend on `persist`, and the reverse dependency does not
exist.

★ **And therefore the rule that came with it**: `agentkit/spec` may use only **one**
symbol of `persist` — `persist.AtomicWrite`. Reaching for `Runtime` / `Session` /
`Turn` would be the real break — that would make the configuration know who consumes
it. The grep below exists so that the rule drifts into a diff conflict instead of
quietly decaying:

```
$ grep -oP 'persist\.[A-Z]\w*' agentkit/spec/*.go | sort -u
agentkit/spec/write.go:persist.AtomicWrite
```

★ The same rule now governs `kb` as well: only the persistence primitives are allowed
(`persist.AtomicWrite` / `persist.FileLock` and kin) — `kb` reaches for
`persist.ErrLocked`, `persist.FileLock`, `persist.OpenFileLock` and
`persist.RunLockPath`, and for nothing else.

---

## 10. Invariants

| # | Invariant | Consequence of breaking it |
|---|---|---|
| 1 | **No field-level merging** | the configuration source becomes untraceable — "why did this value take effect" has no answer |
| 2 | Relative paths resolve against a **written-down** base — and there are two of them | changing cwd changes the data directory |
| 3 | Secrets do not go into the repository; only `${ENV}` / `@file:` | the repository leaks them |
| 4 | Unknown fields must error | a typo silently takes effect |
| 5 | `deferred` must be printed | "it was configured but had no effect" is invisible = a silent ignore |
| 6 | There is **no** `extra` × provider capability matrix | a field the endpoint does not know surfaces as a request-time provider error, not as a startup error |
| 7 | A missing `.openheron/` is not an error — but a missing **default agent** is | a new project cannot start |
| 8 | **A model reference must carry the `<provider>/` prefix and must resolve** | a mistyped name silently degrades to another model ⇒ "why is this so expensive" cannot be traced |
| 9 | The model name is overridden at request level; **one instance per agent, not per model** | one instance per model ⇒ pointless construction overhead |
| 10 | ✅ **implemented** MCP tools always carry the `mcp__` namespace, and **only** MCP tools may use that prefix | without the namespace, a server that exposes `ls` takes the whole agent down (it collides with a reserved tool name, which is a load-time error); without the "only MCP" half, a built-in tool could take the namespace and a prefix rule in an agent's `disabled_tools` would quietly shadow it |
| 11 | ✅ **implemented** An agent's `disabled_tools` supports only a **trailing** `*` prefix pattern; every other spelling errors at load time | a rule that reads differently from what it actually covers is a silent mismatch |
| 12 | ✅ **implemented** A tier is written **in the tool's own file and vocabulary** — the file's `allows:` names what runs without a nod, and there is **no global list** to contradict it | one tool's permission is one fact in one place; a file that says `post` says `allows: [post]`, and nothing has to translate between two vocabularies |
| 13 | ❌ **withdrawn** — the filesystem tools do **not** get a file of their own; their exceptions live in the code (`agentkit/build/tools.go:61`) | ★ the file was proposed as a **tuner**, but a tuner is only worth having if it can widen as well as narrow, and the widening it promised is exactly what the tier model now cannot express for a file-less tool. ★ `tools/` accepts four fixed names (`agentkit/spec/load.go:191`), so the file would be a load-time error; the narrowing it was for is an agent's `disabled_tools` (§6.2, §6.6) |

★ **Rows 10 and 11 are implemented.** The namespace is composed in one place
(`agentkit/build/mcptools.go:140`), the prefix syntax is parsed by
`decl.ParseToolNamePattern` (`agentkit/spec/decl/decl.go:341`) and refused by the loader
(`agentkit/spec/load.go:110` `validateDisabledTools`), the table keeps the patterns
(`agentkit/build/policy.go:98` `newTable`) and matches them
(`agentkit/build/policy.go:146` `Table.Blocked`), and the agent's `mcps:` list lands in
the same table as a namespace prefix (`agentkit/build/policy.go:129`
`denyMCPNamespaces`). ★ There is **no global list** left for the prefix syntax to
belong to — it is the only place a name is matched by prefix (§6.3).

★ **Row 12 holds in full.** The MCP half: the server's own file names its exceptions in
the server's own vocabulary, and the table is assembled from the configuration
(`agentkit/build/mcptools.go:172` `mcpAllowedNames`). The `tools/` half: each file
names its own exceptions (`agentkit/spec/load.go:235` `AllowedTools`). ★ And the tools
that have **no file of their own** are the exception the code can express
(`agentkit/build/tools.go:61` `builtinAllowedTools`) — which is why the default is
`ask` and the exceptions are named (§6.3).

The verification of the rows that are not obvious from the code:

| # | Verified by |
|---|---|
| 1 | `decodeStrict` decodes file by file onto the **prefilled** struct (`agentkit/spec/config.go:415`), and the process reads exactly **one** configuration root (`agentkit/spec/config.go:174`) |
| 2 | `server.data_dir` resolves against the **configuration root** (`agentkit/spec/config.go:241` `DataDir`); `server.static_dir` and `kb.corpus_dir` resolve against the **project root** (`agentkit/spec/config.go:255` `ResolveDir`). ★ Two bases, each named in §6 — a single "project root" rule would be wrong for `data_dir` |
| 3 | the write gate rejects a literal `api_key` (`agentkit/spec/write.go:441` `checkSecret`) |
| 4 | `decodeStrict` turns on `yaml.Decoder.KnownFields` (`agentkit/spec/config.go:415`) |
| 5 | `main.go:198` prints every entry on load; the entries come from `Config.deferKey` (`agentkit/spec/config.go:151`) |
| 6 | `llm/<provider>.yaml`'s `type` is only checked for non-emptiness (`agentkit/spec/load.go:29-30`), and `extra` is handed straight to the OpenAI-compatible ACL (`llm/common/option.go:20`) — there is only **one** endpoint dialect here, so "an unsupported provider" cannot occur |
| 7 | the **defaulted** configuration root is created when it is absent (`agentkit/spec/config.go:200-207`); the absence of `agents/main.md` **is** a startup error (`agentkit/spec/load.go:456-458`) |
| 8 | `ParseRef` (`agentkit/spec/load.go:437`) and `validate` (`agentkit/spec/load.go:455`) |
| 9 | `model.WithModel` is a request option (`llm/common/request.go:32`); the instance cache is keyed by agent name + generation (`agentkit/build/pool.go:31`) |
| 10 | the namespace is composed in exactly one place (`agentkit/build/mcptools.go:140` `mcpExposedName`), and the prefix and the separator are constants (`agentkit/spec/decl/decl.go:336`, `:345`) |
| 11 | `decl.ParseToolNamePattern` (`agentkit/spec/decl/decl.go:341`) + the loader's refusal (`agentkit/spec/load.go:110` `validateDisabledTools`) + the table that keeps and matches the patterns (`agentkit/build/policy.go:98` `newTable`, `:146` `Table.Blocked`) |
| 12 | the MCP half: `mcpAllowedNames` (`agentkit/build/mcptools.go:172`); the `tools/` half: `AllowedTools` (`agentkit/spec/load.go:235`); the tools without a file: `builtinAllowedTools` (`agentkit/build/tools.go:61`) — and the default (`agentkit/build/policy.go:55` `TierOf`) |

---

## 11. Test matrix

★ The table below lists **cases that actually exist** (the `agentkit/spec` package has
**164** `Test` functions), not a plan. A case that never landed has been removed or
replaced by the real case that covers the same behaviour — listing a test that does
not exist is worse than not listing it.

### 11.1 Layering and precedence

| Case | Assertion |
|---|---|
| `DefaultsSurviveWhenFileOmitsKeys` | when only `listen_addr` is written, `max_retries` / `debounce` / `lease_ttl` keep their defaults (the direct manifestation of prefilling taking effect) |
| `TheOnlyFileInTheRootIsTheOneThatCounts` | the file in the configuration root is the one that counts; `models` is not merged with anything else |
| `MissingConfigDirectoryIsNotAnError` | with no `.openheron/` the load does not crash — it fails validation, and the error **names the missing `agents/main.md`** |
| `RelativePathsResolveAgainstProjectDir` | `resolvePath` resolves against the **configuration root** (`agentkit/spec/config.go:441`); an absolute path is returned as-is. ★ The function has **no production caller** today (`DataDir` does its own join, `agentkit/spec/config.go:241`) — this case pins the rule, not a live path |
| `ProjectRootAndResolveDir` | `ProjectRoot()` is the parent of `.openheron` and differs from `Root()`; `ResolveDir` resolves a relative path against the project root, an absolute path as-is, and the empty string as-is |
| `TheConfigRootIsNeverSearchedForUpwards` | ★ the configuration root does **not** probe upwards: from a subdirectory the ancestor's `.openheron/` is not picked up, and the root is the project root's own directory |
| `AnExplicitRootWinsOverTheEnvironment` | an explicit `--config-dir` beats `$OPENHERON_CONFIG_DIR`, and it is **not** created when it does not exist |
| `TheEnvironmentWinsOverTheProjectRootDir` | `$OPENHERON_CONFIG_DIR` beats the project root's own directory, and it is **not** created either |
| `AConfigurationThatNamesNoExceptionsAllowsNothing` | ★ a configuration that names **no** exceptions allows **nothing** — the default is `ask`, so an unlisted name asks (`agentkit/spec/policy_contract_test.go:12`) |

### 11.2 Values and types

| Case | Assertion |
|---|---|
| `ExpandEnvUnsetIsNonFatal` | `${VAR}` unset ⇒ **returns an empty value + resolved=false, no error** |
| `ExpandEnvSetResolves` | `${VAR}` set ⇒ the trimmed value + resolved=true |
| `ProviderMissingKeyIsReminderNotFatal` | a provider missing its key still loads; `Deferred()` carries a notice naming the variable **and** its consequence ("401 Unauthorized") |
| `ExpandAtFile` | `@file:` is read and trimmed; a missing file is an error |
| `ExpandLiteralPassesThrough` | a literal passes through as-is |
| `DurationRequiresUnit` | `60` (no unit) errors; `60s` is correct |
| `TimeoutMissingUnitIsRejected` | `timeout: 300` is rejected **at load time** |
| `DebounceFallsBackWithoutFailing` | a wrong `debounce` **does not** fail the configuration load; it falls back to the default |
| `UnknownFieldIsRejectedWithLineNumber` | an unknown field errors and is named by name |

### 11.3 Declarations and graph validation

| Case | Assertion |
|---|---|
| `AgentWithoutModelIsRejected` | a missing `model` errors (no inheritance) |
| `ModelRefWithoutProviderPrefixIsRejected` | a reference without a `<provider>/` prefix errors |
| `UnknownModelRefDoesNotFallBack` | a reference to an undeclared model ⇒ an error, **no silent degradation** |
| `SubagentGraphIsAccepted` | a legal topology loads |
| `UnknownSubagentIsRejected` | `subagents` naming an agent that does not exist ⇒ an error that lists the declared ones |
| `SubagentGraphCycleIsRejected` | a cycle ⇒ an error that gives the **cycle path** |
| `DeclaredDepthOverLimitIsRejected` | a depth over the cap ⇒ an error that points at `agent.max_depth` |
| `RaisingMaxDepthAcceptsDeeperGraph` | raising the cap makes the same graph load |
| `SubAgentSpecsMirrorTopology` | the topology of the spec tree + `disabled_tools` + the reasoning gear name + `temperature` / `max_tokens` are all carried over |
| `OnlyTheNamedToolsRunWithoutANod` | ★ the file names the exceptions in **its own** vocabulary: `tools/http.yaml` says `allows: [request_get]`, so `request_get` runs without a nod and `request_post` asks (`agentkit/spec/policy_contract_test.go:53`) |
| `AgentBodyBecomesInstruction` | the body becomes `Instruction` |
| `FrontmatterAndBodySplit` | the frontmatter is split off; **the body is preserved verbatim** (including the paragraphs after a blank line) |
| `FrontmatterUnterminatedIsError` | a missing closing delimiter errors |
| `ModelReasoningVocabularyAndDefault` | the `reasoning` vocabulary and `reasoning_default` parse correctly; `LevelFields` pulls the gear fields out |
| `LegacyThinkingAndEffortAreRejected` | the old names `thinking` / `effort` are **rejected in strict mode** — a rename leaves no silent alias behind |

### 11.4 Hot reload

| Case | Assertion |
|---|---|
| `VersionChangesWithContent` | changed content ⇒ a changed `Version`; unchanged ⇒ it stays stable |
| `ServerDirIsNotHot` | ★ changing `server/` **does not** trigger a generation change |
| `ToolsDirIsHot` | ★ changing `tools/` **does** trigger a generation change |
| `WatchIgnoresServerDir` | end to end: really write a `server/config.yaml` and confirm `onChange` is not called |
| `ReloadFailureKeepsLastGood` | after a broken configuration `Current()` is still the previous one; **and `onChange` is not called** |
| `NewReloaderFailsWhenFirstLoadFails` | a failure on the first screen returns an error immediately |
| `WatchDetectsChangeAndSwaps` | the change is found within 3 seconds, the generation is swapped and the new configuration takes effect |
| `WatchExitsOnContextCancel` | `Watch` exits with the ctx |
| `WatchEnabledDistinguishesUnsetFromFalse` | not written = on; an explicit `false` = off |
| `LogReloadFailedIsSwappable` | the degradation really did make a sound |
| `DeferredIsCollected` | "configured but not in effect" lands in `deferred` (here: an `api_key` env variable that was never set) |

### 11.5 Regression

★ **`go test ./...` is green**: **980** top-level cases in the root module, **21**
packages carry tests.

★ **`go test -race ./...` is green as well** — it was not, and the four races it
reported are worth writing down, because each one was the same mistake in a
different place: **the lock was there, and one path did not go through it.**

| Package | What raced | What it is now |
|---|---|---|
| `agentkit/run` | `Turn.fail` wrote the turn's failure reason (`agentkit/run/turn.go:217`) while another goroutine read it — the failure path is reachable from the event loop, a superseding turn, the shutdown path and the start timeout, and they overlap | a mutex guards the field, and the reason keeps its **old** meaning: the last failure to land is the one `Wait` reports, so the supersede path still answers with `ErrSuperseded` |
| `api` | loading a transcript rewrites the session's id (`storage/filestore/transcript.go:70`) while another goroutine read that id (`storage/filestore/session.go:171`) — the id is **not** immutable, because the transcript's header is authoritative | the mutex moved to the top of the struct and now covers the identity fields too; the accessors read under it, and the id is read under it and the disk work happens outside it |
| `kb` | the store wrote its own state outside the lock (`kb/store.go:504`, `kb/store.go:653`) while a search read it — one write was a **derived** value (the catalog file's stat), the other was a **shallow** copy of a map another goroutine mutates in place | the stat is read before the lock and assigned inside it; the derived state is snapshotted as a **deep** copy under the read lock, because a shallow copy still shares the map |
| `agentkit/spec` | ★ this one was in the **test itself**: the capture callback wrote the changed-partition slice from the watcher's goroutine (`agentkit/spec/reload_test.go:305`) while the test goroutine read it (`agentkit/spec/reload_test.go:322`) — neither side was synchronized | the notification travels through a channel and the test waits for the watcher to stop before it reads — and before it puts the package-level hook back, because the watcher reads that variable too |

★ Each of the three production races came with the same second half: **the scope of
the lock was never written down.** A mutex in the middle of a struct reads as "it
guards the fields below it", and one code path acted on that reading. The fix was
not only the missing lock — it was the sentence saying what the lock is for, next
to the lock.

### 11.6 ★ Effective status of parsed fields

This section is **for the person reading the configuration**: which fields really
affect the run, and which ones are merely recognised. A row marked ❌ is
"configured but not in effect" — the most expensive class of problem §4.3 talks
about; they stay in the table so that wiring them up later has a checklist that
cannot be missed.

| Field | Parsed | Validated | Really in effect | Explanation |
|---|---|---|---|---|
| `agents[].temperature` | ✅ | ✅ | ✅ | goes into the tail of the merge chain of `RequestOptionsFor` together with `max_tokens`; a same-named `extra` key is deleted first, so it cannot be overwritten by the extra written afterwards (`agentkit/build/provider.go:105`) |
| `agents[].max_tokens` | ✅ | ✅ | ✅ | same as above |
| `agents[].reasoning` | ✅ | — | ✅ | ★ but **a gear name that is not in the vocabulary is not an error, and it is not reported either** — `LevelFields` simply finds nothing (`agentkit/spec/decl/decl.go:68`), so the thinking parameters quietly do not take effect. This is the one remaining "configured but not in effect, and invisible" (§12.1) |
| `models[].reasoning_default` | ✅ | — | ✅ | when the agent declares no `reasoning`, `RequestOptionsFor` falls back to it (`agentkit/build/provider.go:100`) |
| `models[].reasoning_field` / `models[].reasoning_field_from` | ✅ | — | ✅ | the endpoint's own field name and where it reads the value from (`agentkit/build/provider.go:146`) |
| `models[].context_window` | ✅ | ✅ | ✅ | ★ **openheron made it take effect**: it is the denominator of the compaction threshold — `SummarizeAt` takes a percentage of the window (`agentkit/spec/summarize.go:25`). The threshold belongs to the agent, the window to the model; the model also declares no window ⇒ the agent's compaction is inactive, and that is reported in `deferred` (`agentkit/spec/load.go:588`). It is also visible in the introspection response (`api/v2_introspect.go:265`) |
| `models[].kind` / `models[].dimensions` | ✅ | ✅ | ✅ | they only matter to the knowledge base: `kind: embedding` and the dimension count are validated when `tools/kb.yaml` sets a corpus directory (`agentkit/spec/load.go:611-615`) |
| `models[].summarize_at` | ✅ | — | ❌ | **deprecated**: the threshold belongs to the agent, not to the model. The key is still parsed so that an old configuration keeps loading, and it lands in `deferred` with a pointer to the agent's own file (`agentkit/spec/load.go:61`) |
| `llm/<provider>.yaml`'s `header:` | ✅ | — | ❌ | parsed (`agentkit/spec/decl/decl.go:26`) and **never consumed** — `FromConfig` does not hand it to the endpoint (`agentkit/build/provider.go:36`). Nothing in the tree reads it (§12.1) |
| `server.data_dir` | ✅ | ✅ | ✅ | resolved against the **configuration root** (`state` → `<root>/.openheron/state`, `agentkit/spec/config.go:241`); an absolute path is returned as-is; the directory is created when it does not exist |
| `server.static_dir` | ✅ | ✅ | ✅ | resolved against the **project root** by `Service.ResolveDir` (`agentkit/spec/config.go:255`) — neither the cwd nor the configuration root; a missing directory only logs a warning (`main.go:347`) |
| `server.lease_ttl` | ✅ | ✅ | ✅ | how long a run's lease survives without progress; it must be **longer than the provider's own timeout** — the provider's timeout ends a call that is hung, the lease only has to outlast a call that is slow (`agentkit/spec/decl/decl.go:282`) |
| `agents[].skills` | ✅ | ✅ | ✅ | the allow-list of skills this agent may use; **absent** means every skill in the skills directory and an **empty list** means none — the pointer is what tells the two apart (`agentkit/spec/decl/decl.go:105`) |
| `SKILL.md`'s `agent:` / `model:` | ✅ | — | ✅ | implemented by the `ModelHub` / `AgentHub` of the `agentkit/build` package, see §7. They are not openheron configuration keys but the skill's own frontmatter (`agentkit/build/hub.go:64`, `agentkit/build/hub.go:106`) |

**Still not in effect**

- `models[].summarize_at`: deprecated. ★ It is **not** invisible — it lands in
  `deferred`, and the notice names the agent's own file.
- `llm/<provider>.yaml`'s `header:`: parsed and never consumed, **and not reported
  either**. ★ This is the only "configured but not in effect, and invisible" that is
  left in the configuration layer.

### 11.7 ★ MCP

★ These cases live in four files: `agentkit/spec/mcp_test.go` (the loader),
`agentkit/build/mcptools_test.go` (the names and the tiers),
`agentkit/build/mcpmanager_test.go` (the client) and `agentkit/build/mcpstatus_test.go`
(the projection).

| Case | Assertion |
|---|---|
| `TheMCPFileNameIsTheServerName` | the file's name **is** the server's name, and it is the namespace segment of that server's tools |
| `TheServerNameMustBeReadableBack` | a name with a run of underscores is refused |
| `AnUnknownTransportIsALoadTimeError` | a typo in `transport:` does not fall back to stdio |
| `AStdioServerNeedsACommand` | a stdio server without `command` is a load-time error |
| `AnHTTPServerNeedsAURL` | an http server without `url` is a load-time error |
| `AnHTTPServerMayNotDeclareAnEnvironment` | `env:` on an http server is refused — it has no child process to hand an environment to |
| `AnEmptyEnvironmentOnAnHTTPServerIsStillRefused` | ★ the key's **presence** is what is refused, not its value: an `env:` with no value decodes into a nil map, and "the key is there and empty" has to differ from "the key is absent" |
| `AnUnknownKeyIsRefused` | the same strict decoding as every other partition |
| `TheTimeoutsHaveDefaults` | `timeout` defaults to `30s` and `call_timeout` to `60s` |
| `ABadTimeoutDegradesAndIsNoticed` | a bad timeout falls back to the default and lands in `deferred` — it does not keep the process from starting |
| `AnUnsetVariableInTheEnvironmentIsDeferredAndNotFatal` | an unset `${ENV}` is a downgrade, not a fatal error |
| `ASetVariableInTheEnvironmentIsExpanded` | `${ENV}` is expanded at load time |
| `AnAgentNamingAServerThatDoesNotExistIsALoadTimeError` | the error names the declared servers |
| `AnAbsentServerListMeansEveryConfiguredServer` | the pointer is what tells "every" from "none" |
| `AnEmptyServerListMeansNoServerAtAll` | ★ and the empty list really narrows the agent down to nothing |
| `ASubAgentsOwnListIsReadFromTheSubAgent` | a sub-agent's list is its own, and it narrows **its own** table |
| `AWildcardThatIsNotAPrefixPatternIsALoadTimeError` | `mcp*foo` reads like a wildcard and is not one |
| `ALoneWildcardIsALoadTimeError` | `disabled_tools: ["*"]` is a decision to write out, not a wildcard |
| `ATrailingStarIsAPrefixPattern` | `mcp__github__*` is a prefix pattern |
| `TheEditorAcceptsAFileUnderMCPS` | the editor's file-name whitelist knows `mcps/<server>.yaml` |
| `TheEditorRefusesAServerNameThatCouldNotBeReadBack` | and it refuses a name that could not be read back |
| `AnMCPNameIsTheServersOwnNameUnderTheNamespace` | the exposed name is `mcp__<server>__<tool>` |
| `AnMCPNameReplacesTheCharactersTheModelRefuses` | the characters outside the model's function-name set become `_` |
| `AnMCPNameKeepsTheServersOwnCase` | ★ the case is preserved: a lowercased copy would be a second name to keep in sync |
| `AnMCPNameAtTheBoundaryIsKept` / `AnMCPNameOverTheBoundaryIsSkipped` | a composed name of 64 characters is kept; one character more is **skipped with a warning**, never a load-time failure |
| `TwoToolsThatSanitizeAlikeLeaveTheSecondOneSkipped` | the collision costs the second tool, not the whole agent |
| `TheFileAsksForItsOwnToolsAndNotTheExposedOnes` | the file speaks the server's own vocabulary |
| `AnAbsentToolListMeansEveryTool` | `tools:` is a filter, not a switch |
| `EverythingAsksAndTheFileNamesTheExceptions` | the namespace's default is `ask`, and the file names the exceptions |
| `AnEmptyAllowsListCoversTheToolsTheServerHasNotOfferedYet` | the allow list is a statement about the **server**, not about the names it happens to have |
| `TheTierIsAnswerableWhileTheServerIsUnreachable` | ★ the tier table is assembled from the **configuration**, so the answer does not wait for a connection |
| `AnMCPNameNobodyNamesAsks` | ★ **there is no global deny any more**: a tool nobody names asks, MCP names included (`agentkit/build/mcptools_test.go:166`) |
| `TheServerNarrowingDeniesTheNamespacePrefixOnly` | the agent's `mcps:` list denies `mcp__<server>__` and nothing else |
| `TheServerNarrowingIsReportedBeforeTheToolRule` | the narrowing is the reason reported, because the server is not there at all |
| `APrefixRuleReachesTheToolsOfOneServerOnly` | `mcp__github__*` reaches that server's tools and no other |
| `AWildcardPrefixReachesEveryMCPTool` | `mcp__*` reaches every MCP tool, from any server |
| `AnExactEntryStillRemovesTheTool` | an exact entry keeps working the way it always did |
| `TwoCallersAskingForTheSameServerGetOneClient` | one server is one client, and the start is single-flight |
| `ACrashedChildIsStartedAgainOnTheNextCall` | a crashed child is started again on the next call |
| `AChangedDeclarationReplacesTheClient` | a changed declaration replaces the client — this is what makes the partition hot |
| `AFreshListingIsNotReListed` | the tool list is re-listed only when the previous listing is older than 30 seconds |
| `AChangedToolListIsReportedByTheRefresh` | a changed list bumps the generation through the same hook the configuration reloader uses |
| `TheChildEnvironmentIsTheProcessesOwnWithTheFileOnTop` | the child's environment is this process's environment with the file's `env:` layered on top |
| `TheCommandIsNeverResolvedAgainstTheProjectRoot` | ★ the command is resolved in the sandbox's PATH, never against the project root |
| `AnUnreachableServerSaysSoAndKeepsTheOtherServersAlive` | an unreachable server costs its own tools and nothing else |
| `EveryConfiguredServerAppearsExactlyOnce` | the projection lists every configured server exactly once |
| `AServerTheAgentDoesNotUseIsUnusedAndHasNoToolRows` | ★ an `unused` server has **no tool rows** — the `mcps` reason belongs to the agents view |
| `AnUnreachableServerReportsTheErrorAndKeepsItsLastListing` | the status carries the error and the last known listing |
| `TheServerNarrowingIsTheReasonTheAgentsViewReports` | the `mcps` reason is reported where the agent's own table is read |
| `TheReasonIsTheToolRuleWhenTheServerIsThere` | and the tool rule is the reason when the server **is** there |
| `ThePanelAsksTheManagerAndTheManagerStartsTheServer` | the panel **starts** the servers it asks about |
| `ThePanelShowsTheTierTheGateWouldUse` | the panel and the gate read the same tier |

### 11.8 ★ The tiers

★ These cases live in `agentkit/spec/policy_contract_test.go` (the configuration side),
`agentkit/build/policy_test.go` (the table) and `agentkit/build/introspect_test.go` (the
projection); the two the `agentkit/spec` package owns are in §11.1 and §11.3.

| Case | Assertion |
|---|---|
| `TheKnowledgeBaseNamesItsOwnExceptions` | `kb_search` runs without a nod — a read-only tool behind approval is one the model learns not to use — and `kb_index` asks, because indexing writes to the index on disk (`agentkit/spec/policy_contract_test.go:28`) |
| `ALeftOverPolicyDirectoryIsReported` | ★ a retired partition is **not** silent: the notice names it and says where its content went (`agentkit/spec/config_test.go:392`) |
| `AnEmptyRetiredDirectoryIsNotReported` | an empty directory carries nothing that was dropped, so it raises no notice the user cannot act on (`agentkit/spec/config_test.go:419`) |
| `PolicyTierOrder` | a name in the allow list is `allow`; a name that is not is `ask` (`agentkit/build/policy_test.go:39`) |
| `PolicyFallsBackToAsk` | ★ an unlisted name falls back to `ask` — the opposite of the old default (`agentkit/build/policy_test.go:54`) |
| `TheDisableLayerRemovesTheTool` | an agent's `disabled_tools` removes the tool (`agentkit/build/policy_test.go:66`) |
| `DisableIsScopedToItsOwnAgent` | and the removal is scoped to that agent alone (`agentkit/build/policy_test.go:100`) |
| `ApprovalMiddlewareAsksEvenIfFilterMissed` | the gate asks even when the filter did not remove the tool (`agentkit/build/policy_test.go:159`) |
| `ApprovalMiddlewareLetsAllowTierThrough` | an `allow` tool runs without a nod (`agentkit/build/policy_test.go:177`) |
| `VerdictOfFallsBackToAsk` | an unlisted tool is `ask` and still reachable (`agentkit/build/introspect_test.go:64`) |

---

## 12. Out of scope for this round

| Item | Reason | Prerequisite |
|---|---|---|
| RAG / knowledge base | **implemented by the `kb` package**, see [architecture.md](architecture.md) §9 | — |
| MCP / LSP integration | ✅ **the MCP half is implemented** — the `mcps/` partition, the provider and the tier model (§6.3, §6.6, §6.7); the architecture is in [mcp-design.md](mcp-design.md). The LSP half stays out of scope | — |
| Middleware parameter exposure (`middlewares/*.yaml`) | openheron's middlewares are fixed; there is no need to expose parameters | — |
| Configuration write gate (`.config.lock`) | **overturned by the configuration editor** — those six `/v2/settings` routes are the write entry point (`api/v2_files.go:67`) | — |
| ~~The caps in `policy/limits.yaml`~~ | **retired** — the caps live in `server/config.yaml` only, see §12.2 | — |

### 12.2 The caps live in `server/config.yaml` only

〔History〕There used to be a second file for the caps — `policy/limits.yaml` — which
could only **tighten** the values in `server/config.yaml` and was hot-reloaded. It is
gone. The section that follows records **why**, and what replaced it.

**Decision**: a cap is a **process-level** setting. There is exactly one place to write
it, and it is `server/config.yaml`:

```yaml
agent:
  max_depth: 2      # maximum nesting depth; the root agent is level 0
retry:
  max_retries: 3    # maximum number of retries for a model call
```

★ **Why the second file went.** It was a second copy of a number that only one place
should own, and it bought one thing: the ability to tighten a cap **without a restart**.
That is not worth a second place to look. ★ And the direction was the wrong one to
optimize for: relaxing a cap is the decision that should be deliberate, and it already
needed a restart; tightening getting faster is a convenience, not a safety property.

★ **What the "only tighten" rule became.** It does not exist any more, because a rule
about how two copies of a number merge is only needed when there are two copies. There
is one: the value in `server/config.yaml` **is** the cap.

★ **The caps are not hot-reloaded.** `server/` configures the process itself
(§6.4), so a change takes effect **on the next start**. That is the price of not having
the second copy, and it is the same price every other key under `server/` already pays.

#### A topology that exceeds the cap ⇒ an error at startup

When the declared topology nests deeper than `agent.max_depth`, the **load fails**
(`agentkit/spec/load.go:615` `validateDepth`) — it is never a silent truncation at
runtime. "You configured three levels but only two actually run" is the hardest class
of problem to track down.

#### Implementation checklist

| Location | What it is |
|---|---|
| `agentkit/spec/decl/decl.go` | `Server.Agent.MaxDepth` and `Server.Retry.MaxRetries` |
| `agentkit/spec/load.go:671` | `MaxDepth()` reads the server value — there is no merge any more |
| `agentkit/spec/load.go:679` | `MaxRetries()` reads the server value |
| `agentkit/spec/load.go:615` | `validateDepth` — a declared topology over the cap is a **load error** |
| `agentkit/spec/config.go:329` | `noteRetiredDirs` — a leftover `policy/` directory is reported in `deferred`, so the startup log says where its content went |
| `api/v2_introspect.go` | `server.maxRetries` / `server.maxDepth` of `/v2/config` report the **effective** value |

★ **The server remains the single source of truth for templates and semantics**: the
frontend only knows "what this file should look like", and "does it still hold after
the merge" is always answered by the server's trial merge.

There are also two **deliberately retained environment variables**:
`build.ConfigFromEnv` is a transitional shape (the main path goes through
`FromConfig`), and `EINO_EXT_SKILLS_DIR` is demoted to an override of
`.openheron/skills`.

### 12.1 Known convergence items (★ exposed during implementation, none blocking)

| Item | Current state | Suggestion |
|---|---|---|
| ~~**The business hot reload is not fully wired**~~ | ✅ closed: the manager swaps the content → the next `Build` pulls the current generation → the snapshot / the pool changes generation naturally; there is no longer such a manual push line as `Reconfigure` | see `main.go:196` (the reloader callback swaps the manager's content) and `agentkit/build/pool.go:31` (the pool is keyed by agent name + generation) |
| ~~**One model instance per agent**~~ | ✅ not a defect, by design: the instance granularity is provider × model × reasoning gear × endpoint field shape × timeout, and the head of the chain is the agent. Sharing across agents would need a cache keyed by those five things = a second source of truth | see §6, and the "model construction" row of §2 |
| ~~**`agentkit/spec` depends on eino**~~ | ✅ **eliminated**: the configuration declares only a model reference; the model instance is resolved by reference during agent assembly. `agentkit/spec` is once again a pure declaration layer | `go list -deps ./agentkit/spec \| grep -c cloudwego/eino` = **0** |
| A reasoning gear the model does not declare | the agent names a gear that is not in the `reasoning:` vocabulary of `llm/<provider>.yaml`: no error, and **no `deferred` entry either** — `LevelFields` simply finds nothing (`agentkit/spec/decl/decl.go:68`) | the only remaining "configured but not in effect, and invisible"; the vocabulary lives in `llm/`, the gear name in `agents/`, and the two files are never cross-checked |
| `llm/<provider>.yaml`'s `header:` | parsed, never consumed (`agentkit/spec/decl/decl.go:26`) | either hand it to the endpoint or drop the key from the file specification |
| Fields parsed but not consumed | see §11.6 | wire them up as needed |

### Two deviations added during implementation

| Deviation | Original design | Actual | Reason |
|---|---|---|---|
| The duration type of `watch.debounce` | it shared one `Duration` with `timeout` (an error in every case) | split into `LenientDuration` (a wrong value degrades + is recorded in `deferred`) | the two are different in nature: a wrong `timeout` makes the request's timeout behaviour unpredictable and **must error on the spot**; `debounce` only affects "how long counts as saved" and should not keep the program from starting. **The degradation must make a sound**, so it is recorded in `deferred`. ★ In openheron `LenientDuration` covers not only `debounce` but also `start_timeout`, `lease_ttl` and the timeouts of the three `tools/` sections; the strict `Duration` is left to the provider's and the model's `timeout` (`agentkit/spec/decl/decl.go:28-55`) |
| The `models[].reasoning` vocabulary | the original document gave no concrete fields | implemented: gear name → endpoint field shape, `reasoning_default` is the default gear | an agent naming a gear the model does not have **does not block startup**; ★ but openheron neither reports it nor records it in `deferred` — that is the open item in §12.1, not a solved one |

---

## Appendix A: Verification records

| Assertion | How it was verified |
|---|---|
| `agentkit/spec` does not depend on `agentkit/build` / `agentkit/run` / `api` | `go list -deps ./agentkit/spec \| grep -E "ouranoshong/openheron/(agentkit/build\|agentkit/run\|api)"` → empty |
| `agentkit/spec` pulls in neither hertz nor eino | `go list -deps ./agentkit/spec \| grep -E "hertz\|cloudwego/eino"` → empty; the count of each is **0** |
| `agentkit/run` does not depend on `agentkit/spec` | `go list -deps ./agentkit/run \| grep "ouranoshong/openheron/agentkit/spec"` → empty |
| `llm/common` has zero openheron dependencies | `go list -deps ./llm/common \| grep '^github.com/ouranoshong/openheron/'` → only `llm/common` itself |
| the openheron dependencies of `agentkit/spec` are exactly `obs`, `spec/decl`, `persist` and itself | `go list -deps ./agentkit/spec \| grep '^github.com/ouranoshong/openheron/'` → the four lines written above |
| `agentkit/spec` uses only one symbol of `persist` | `grep -oP 'persist\.[A-Z]\w*' agentkit/spec/*.go \| sort -u` → `agentkit/spec/write.go:persist.AtomicWrite` |
| `kb` reaches for the persistence primitives only | `grep -rhoP 'persist\.\w+' kb/ \| sort -u` → `persist.AtomicWrite`, `persist.ErrLocked`, `persist.FileLock`, `persist.OpenFileLock`, `persist.RunLockPath` |
| `persist` is a leaf package | the imports of `persist/atomicfile.go:3` → `fmt` / `os` / `path/filepath` |
| the checkpoint uses the shared atomic write | `storage/filestore/checkpoint.go:232` |
| the configuration root does not probe upwards | read the `NewService` comment (`agentkit/spec/config.go:144`) + the cases `TestTheConfigRootIsNeverSearchedForUpwards` and `TestNewServiceDoesNotProbeUpwards` |
| `agentkit/spec` pulls in no framework — only the standard library plus `yaml.v3` | the imports of `agentkit/spec/config.go:3` |
| unknown fields error | `decodeStrict` turns on `yaml.Decoder.KnownFields` (`agentkit/spec/config.go:415`) |
| the `deferred` list is printed at startup | `main.go:198` |
| the number of top-level cases in `agentkit/spec` | `grep -rhoP '^func \KTest\w+' agentkit/spec/ \| sort -u \| wc -l` → **164** |
| the number of top-level cases in the whole repository | `grep -rhoP '^func \KTest\w+' --include='*_test.go' . \| sort -u \| wc -l` → **981**, of which one belongs to the nested module `sandbox-kit/examples/demo-project` ⇒ **980** in the root module |
| the number of packages carrying tests in the root module | `go list -f '{{.ImportPath}} {{len .TestGoFiles}} {{len .XTestGoFiles}}' ./... \| awk '$2+$3>0' \| wc -l` → **21** |
| the whole test suite is green | `go test ./...` → every package `ok` |
| `-race` is green now | `go test -race ./...` → every package is `ok`; §11.5 records what each of the four races was |
| the caps come from `server/config.yaml` and nothing else | `MaxDepth()` / `MaxRetries()` (`agentkit/spec/load.go:671`, `:679`) + the cases in `agentkit/spec/config_test.go` |
| a leftover `policy/` directory is reported | `noteRetiredDirs` (`agentkit/spec/config.go:329`) + `TestALeftOverPolicyDirectoryIsReported` and `TestAnEmptyRetiredDirectoryIsNotReported` |
| `models[].context_window` really takes effect | `SummarizeAt` (`agentkit/spec/summarize.go:25`) + `TestSummarizeAtResolvesAgainstTheWindow` |
| `models[].summarize_at` is deprecated but still parsed | the comment on `decl.Model.SummarizeAt` (`agentkit/spec/decl/decl.go:46`) + the `deferred` entry (`agentkit/spec/load.go:61`) + `TestTheOldModelThresholdIsReportedNotFatal` |
| `llm/<provider>.yaml`'s `header:` has no consumer | `decl.Provider.Header` (`agentkit/spec/decl/decl.go:26`) and `FromConfig` (`agentkit/build/provider.go:36`) — nothing in the tree reads the field |
| the model instance is one per agent, not per model | the key of the pool (`agentkit/build/pool.go:31` `GetOrBuild(ctx, name, gen, fn)`) |
| `AgentHub` / `ModelHub` are implemented | `agentkit/build/hub.go:64` (`skillModelHub`) and `agentkit/build/hub.go:106` (`skillAgentHub`) |
| `ConfigFromEnv` lives in `agentkit/build` | `agentkit/build/config.go:15` |
| `EINO_EXT_SKILLS_DIR` is only a fallback | `resolveSkillsDir` (`agentkit/build/skill.go:188`) |
| the six `/v2/settings` routes | `api/v2_files.go:67` |
| the `policy/limits.yaml` template key | `api/v2_settings.go:143` |
| the frontend dispatches the validation by path | `web/src/lib/settings-validate.ts:128` |
| the frontend prefers the template by path | `resolveTemplate` (`web/src/lib/settings-validate.ts:108`), called from `web/src/components/settings/new-file-dialog.tsx:134` |
| `context_window` is visible in the introspection response | `api/v2_introspect.go:265` |
| the `spec` → `run` exception never existed here | `git grep -n "agentkit/run" fa7131a -- agentkit/spec` → a comment, and nothing else |
| the MCP namespace is composed in one place | `mcpExposedName` (`agentkit/build/mcptools.go:140`) + the prefix and the separator constants (`agentkit/spec/decl/decl.go:336`, `:345`) |
| an agent's `disabled_tools` prefix pattern | `decl.ParseToolNamePattern` (`agentkit/spec/decl/decl.go:369`) + the loader's refusal (`agentkit/spec/load.go:110` `validateDisabledTools`) + the table that keeps and matches the patterns (`agentkit/build/policy.go:108` `newTable`, `:161` `Table.Blocked`) |
| the MCP tier is answerable without the server | `mcpAllowedNames` (`agentkit/build/mcptools.go:172`) + the default tier (`agentkit/build/policy.go:55` `TierOf`) + the case `TestTheTierIsAnswerableWhileTheServerIsUnreachable` |
| the `mcps/` partition is hot | the `mcps` category carries `hot: true` (`agentkit/spec/config.go:61-69`) + `hotDirList()` (`agentkit/spec/config.go:87`) → `HotDirs` (`agentkit/spec/reload.go:21`) |

## Appendix B: Implementation status

| Item | Status | Location |
|---|---|---|
| Single-root loading | ✅ | `agentkit/spec/config.go:174` `NewService`, `agentkit/spec/config.go:290` `loadOnly` |
| Prefilled struct decoded layer by layer (the defaults survive) | ✅ | `agentkit/spec/config.go:296-298` (the `defaultServer()` / `defaultPolicy()` / `defaultKB()` prefills) |
| Strict mode (an unknown field errors with a line number) | ✅ | `agentkit/spec/config.go:415` `decodeStrict` |
| `deferred` + forced printing | ✅ | `Config.Deferred()` (`agentkit/spec/config.go:132`), printed one by one at startup (`main.go:198`) |
| Three-level configuration-root resolution (explicit → env → the project root's own directory) | ✅ | `agentkit/spec/config.go:144` |
| **No upward search** | ✅ | the `NewService` comment (`agentkit/spec/config.go:169`); `TestTheConfigRootIsNeverSearchedForUpwards` |
| `${ENV}` missing ⇒ a reminder (does not block) + `@file:` | ✅ | `agentkit/spec/expand.go:9` `expandValue`, `agentkit/spec/load.go:42` |
| `Duration` requires a unit | ✅ | `agentkit/spec/decl/decl.go:584` |
| `LenientDuration` degrades + records `deferred` | ✅ | `agentkit/spec/decl/decl.go:509`, `agentkit/spec/reload.go:80` |
| frontmatter splitting | ✅ | `agentkit/spec/expand.go:48` `splitFrontmatter` |
| model reference validation (no silent degradation) | ✅ | `agentkit/spec/load.go:437` `ParseRef`, `agentkit/spec/load.go:455` `validate` |
| `subagents` graph validation (a dangling reference + a cycle) | ✅ | `agentkit/spec/load.go:716` `validateAgentGraph`, `agentkit/spec/load.go:733` `detectCycle` |
| the default tier is `ask`, and a file's `allows:` is the exception | ✅ | `Policy.TierOf` (`agentkit/build/policy.go:55`); the exceptions come from `builtinAllowedTools` (`agentkit/build/tools.go:61`), `Config.AllowedTools` (`agentkit/spec/load.go:235`) and `mcpAllowedNames` (`agentkit/build/mcptools.go:172`), assembled by `policyOf` (`agentkit/build/spec.go:57`) |
| `envOr` → `cfg.*` | ✅ | `main.go:459` `override` (used at `main.go:341`) |
| `.openheron/state/` replaces `./data` | ✅ | `config.Service.DataDir("")` (`agentkit/spec/config.go:241`) |
| `MaxRetries` is configurable | ✅ | `decl.RetryCfg.MaxRetries` (`agentkit/spec/decl/decl.go:534`) → `Config.MaxRetries()` (`agentkit/spec/load.go:679`) |
| skills auto-detect `.openheron/skills` | ✅ | `agentkit/build/skill.go:188` `resolveSkillsDir` / `probeProjectSkills` |
| the write gate rejects a literal secret | ✅ | `agentkit/spec/write.go:441` `checkSecret` |
| ~~`chatmodel.New` reads the environment directly~~ | ✅ deleted | the package is gone; it is `llm/common` now |
| the `mcps/` partition + the MCP provider | ✅ | the partition (`agentkit/spec/config.go:61-69`), the loader (`agentkit/spec/load.go:269` `loadMCP`), the provider (`agentkit/build/tools.go:78`) and the tier table (`agentkit/build/mcptools.go:172`) — the configuration surface is §6.7 of this document |
| prefix matching (a trailing `*`) | ✅ **for an agent's `disabled_tools`** | the loader's refusal (`agentkit/spec/load.go:110` `validateDisabledTools`), the table that keeps the patterns (`agentkit/build/policy.go:98` `newTable`) and the one that matches them (`agentkit/build/policy.go:146` `Table.Blocked`) — the matching semantics are §6.3 of this document |
| the caps in `server/config.yaml` | ✅ | `MaxDepth()` / `MaxRetries()` read the server value (`agentkit/spec/load.go:671`, `:679`); a topology over the cap is a load error (`agentkit/spec/load.go:615` `validateDepth`) — §12.2 |

**Landed**: `AgentHub` / `ModelHub` (§7) — implemented in `agentkit/build/hub.go`
(`skillAgentHub` / `skillModelHub`) and wired by `agentkit/build/skill.go`; the
`context: fork` `SKILL.md` triggers it (the cases are in
`agentkit/build/hub_test.go`).

There are also two **deliberately retained environment variables**:
`build.ConfigFromEnv` is a transitional shape (the main path goes through
`FromConfig`), and `EINO_EXT_SKILLS_DIR` is demoted to an override of
`.openheron/skills`.

**One added during implementation**: `server/config.yaml` gained
`agent.max_depth` (default 1). When the declared topology depth exceeds the cap it
**errors at startup** rather than truncating at runtime — "you configured three levels
but only two actually run" is the hardest class of problem to track down.

## Appendix C: Corrections found while internalizing this document into openheron

| The upstream says | openheron's reality | Why |
|---|---|---|
| the configuration root is resolved by "env → probing upwards → itself" | it is resolved in exactly **three** levels: an explicit `--config-dir` → `$OPENHERON_CONFIG_DIR` → `<projectRoot>/.openheron` (created when missing), and there is deliberately **no upward search** of ancestor directories (`agentkit/spec/config.go:144`) | once the configuration root depends on "where the process was started from", the same command run from another directory reads a different configuration — and a test run ends up reading the developer's own configuration too (see `AGENTS.md`) |
| the test instance is isolated by "pre-seeding a `.heron/`" | the isolation is `.env.test` plus the `make test-env-backend` / `test-env-reset` / `test-env-sync` targets: the instance runs on `:18099` with **its own configuration root and its own session directory** | different ports are not data isolation — openheron writes the sessions under `.openheron/state/`, so a test that writes there pollutes the real history |
| the `agentkit/spec` package has **115** `Test` functions | it has **164** | the package grew with the code; the table above lists the cases that actually exist |
| the module path is `heron/...` | it is `github.com/ouranoshong/openheron/...` | `go.mod` |
| the only heron dependency of `agentkit/spec` is `persist` | there are **four**: `agentkit/obs`, `agentkit/spec/decl`, `persist` and itself | `obs` is the one log sink, and the split of "declaration" (`decl`) from "behaviour" (`spec`) is openheron's own — see [architecture.md](architecture.md) §2.1 |
| [architecture.md](architecture.md) §2.2 says `agentkit/spec` imports `kb` | the **production** code does not — only the case file `agentkit/spec/policy_contract_test.go:7` does | validating the knowledge-base configuration only needs the declaration (`decl.Model.IsEmbedding`), not the `kb` package itself; the dependency list in §2 of that document counts the test |
| `llm.ConfigFromEnv` | it is `build.ConfigFromEnv` (`agentkit/build/config.go:15`) | the configuration package must not depend on the vendor protocol layer, and `llm/common` is a leaf package with zero openheron dependencies |
| the model struct | openheron's `decl.Model` carries `kind`, `dimensions` and `context_window`; `summarize_at` **exists but is deprecated** — the threshold belongs to the agent (`decl.Agent.SummarizeAt`) | the threshold is the agent's policy; the window is the model's — the two meet in exactly one place (`agentkit/spec/summarize.go:25`) |
| the agent struct | openheron's `decl.Agent` carries `skills` as a `*[]string`: absent = every skill, an empty list = none | "absent" and "empty" have to be told apart, otherwise "no skills at all" cannot be expressed |
| the server struct | openheron's `decl.Server` carries `lease_ttl` (and `frontend_mode`) | the lease's budget must be longer than the provider's own timeout, and the two live in different files |
| `internal/atomicfile` | it is `persist/atomicfile.go`, a **top-level** leaf package | openheron has no `internal/` directory; `persist` holds `AtomicWrite` and the `FileLock` family |
| `kb` imports no heron package | `kb` imports `obs` and `persist` | `obs` is the one log sink, and the knowledge base needs the shared atomic write |
| the `agentkit/spec` → `agentkit/run` exception was retracted upstream on 2026-10-21 | openheron **never had it** — the code surface was extracted after the retraction, and `agentkit/spec` mentions `agentkit/run` only in a comment | the retraction was a design decision upstream; openheron inherited the retracted form |
| `models[].context_window` is pure metadata and takes no effect | it **takes effect**: it is the denominator of the compaction threshold (`agentkit/spec/summarize.go:25`) | openheron implements compaction and the context-usage dashboard — see [context-usage-design.md](context-usage-design.md) |
| an agent naming a reasoning gear the model does not declare lands in `deferred` | it does **not** — it is silently ignored | the vocabulary lives in `llm/`, the gear name in `agents/`, and the two files are never cross-checked |
| `go test -race ./...` is green across the whole repository | it **was** not: the race detector reported races in `agentkit/run`, `api`, `kb` and `agentkit/spec` | the one inside `spec` was in the test itself; all four are fixed now, and §11.5 records what each one was |
| a bad `tools/` timeout is reported as `watch.debounce` | the label is fixed: `collectBadDurations` prefixes **every** bad duration with "watch.debounce fell back to default", so a bad `timeout` in `tools/http.yaml` lands under the same label | `decl.BadDurations()` is a package-level list with no attribution (`agentkit/spec/decl/decl.go:528`) |

