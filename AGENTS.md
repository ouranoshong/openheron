# AGENTS.md

Project conventions for the people and AI working in this repository.

The Chinese version of this file is **[AGENTS_zh.md](AGENTS_zh.md)**; the two correspond one to
one — change one and change the other.

> ★ **This repository is the code surface**: Go backend + React frontend + build configuration.
>
> It ships **ten** bilingual document sets, maintained alongside the code
> (**English is the main version; the `_zh` suffix marks the Chinese version**):
> `README.md` / `README_zh.md`,
> `docs/architecture.md` / `docs/architecture_zh.md` (architecture),
> `docs/user-manual.md` / `docs/user-manual_zh.md` (user guide),
> `docs/developer-manual.md` / `docs/developer-manual_zh.md` (developer guide),
> `docs/context-usage-design.md` / `docs/context-usage-design_zh.md` (context-usage estimation design),
> `docs/session-concurrency-design.md` / `docs/session-concurrency-design_zh.md` (session concurrency design),
> `docs/run-lifecycle-design.md` / `docs/run-lifecycle-design_zh.md` (run lifecycle design),
> `docs/fs-backend-design.md` / `docs/fs-backend-design_zh.md` (filesystem backend design),
> `docs/microvm-execution-design.md` / `docs/microvm-execution-design_zh.md` (microVM execution isolation),
> `docs/sandbox-kit-design.md` / `docs/sandbox-kit-design_zh.md` (Docker Sandbox Kit packaging).
> **Every other design document under `docs/` still lives in the upstream repository** ——
> any `docs/xxx.md` reference below that is not one of these ten is the upstream copy.
> If you cannot get hold of it before changing semantics, ask first; do not change
> configuration or interfaces from memory.
>
> ★ The first four sets describe **what the code surface already is**; `docs/context-usage-design.md`
> covers **context usage and compaction** — the usage dashboard (§2, §4, §5), compaction and its
> persistence (§7), and the rendering of compaction markers in the interface (§7.7) are all
> already in the code.
> `docs/session-concurrency-design.md` covers **session concurrency** — the admission gate for
> one turn per session (§5), the cross-client 409 (§5.2), and the frontend busy-state handling
> (§5.4-§5.6) are all already in the code.
> `docs/run-lifecycle-design.md` is the odd one out: it is a **proposal**, not a description.
> Its §1 describes the code as it is — a turn is bound to one SSE connection today — while its
> §2 onwards describe what would be built: a run as a durable row, a lease with a fence, and the
> connection demoted to a subscriber. **Nothing from §2 onwards is in the tree.**
> `docs/microvm-execution-design.md` is a **proposal** too: its §1 describes where `execute`
> runs today (`/bin/sh` as a child of the server process) and the single `StreamingShell`
> interface it would plug into; its §2 onwards describe running the tool inside a microVM.
> **Nothing from §2 onwards is in the tree.**
> `docs/sandbox-kit-design.md` is a **proposal** as well, and the other half of that pair:
> its §1 describes the openheron process as it is — one process with an HTTP/SSE server,
> the file tools and the shell tool sharing one backend — while its §2 onwards describe
> packaging that whole process as a Docker Sandbox Kit, so the boundary moves outward
> instead of inward and the read path is covered too.
> **Nothing from §2 onwards is in the tree.**
> `docs/fs-backend-design.md` describes the **filesystem backend** — the bounded fork of
> `eino-ext`'s local backend (§5) and the confinement rule every tool passes through — and it is
> **implemented**: `agentkit/fsx` is wired in at both call sites.
>
> ★ One directory is not a document set: **`sandbox-kit/`** holds the Docker Sandbox Kits that
> package openheron as a sandbox workload — `openheron/` (the workload: descriptor, content
> recipe, entrypoint) and `openheron-credential/` (the mixin: the model credential and its
> network rule). They are a **separate artifact**: `make release` still produces the six static
> binaries and nothing else. Their design is `docs/sandbox-kit-design.md`, and `make kit`
> builds them.
>
> Write these documents **from the code, not from imagination**: every conclusion must point at a
> line in the code. When the code changes, the documents change with it — documentation drift is
> worse than no documentation.
>
> A design that proposes something new may go past the code — but it must say so where the
> description stops and the proposal starts, and label the proposal as not implemented.
>
> ★ **Both versions must stay in sync**: editing the English version means editing the `_zh`
> version too, and vice versa. Code identifiers, line references, commands and paths must
> correspond one to one — you can compare them set by set with
> `grep -oP '`[a-zA-Z0-9_./:$-]+`' | sort -u`.
> ★ Note that the **backtick span has to match too**: `` `POST /v2/run` `` and `` `POST` `/v2/run` ``
> say the same thing but yield different identifier sets — the comparison will report a
> difference. Do not let "different phrasing" hide "different content".

## Language rules (mandatory)

**Everything the runtime emits is English**: log lines, error messages, warnings, user-facing
notifications, and **interface copy**.

| Scope | Rule | Example |
|---|---|---|
| Log | English | `obs.Logger().Info("[main] config updated", "version", v)` |
| Error | English | `errors.New("query must not be empty")` |
| Warning | English | `fmt.Fprintln(os.Stderr, "[config] hot reload failed, keeping previous config:", err)` |
| Notification | English | `c.deferKey("watch.debounce fell back to default: %s", msg)` |
| API response body | English | `c.JSON(400, map[string]string{"error": "thread not found"})` |
| **Interface copy** | **English** | `"Run in a separate agent"` |
| **Content written into user configuration** | **English** | the header of `catalog.md`, the comment block of `policy/limits.yaml` |

### Code comments

★ **No Chinese comments in the code, for now.** A batch of Chinese comments that used to live here
has been removed wholesale — comments have to be rewritten **point by point**, **in English**.

When you add comments, follow these rules:

- **English**. Every new comment is English — the same language as runtime output.
- **Write the "why", not the "what"** — especially where the design is deliberately
  counter-intuitive. That is where comments earn their keep: `func New() { … }` needs no comment;
  "why `time.Now()` cannot be used here" does.
- **Do not encode framework semantics in comments as a contract.** The previous frontend's core
  problem was a set of behaviors you had to read the framework source before daring to change
  them; they were all held together by comments, and every change made them wrong. Today
  `lib/sse.ts` / `lib/events.ts` / `lib/store.ts` depend on no framework, and `bun test` proves
  them correct; **put new logic in those three layers** and keep them pure functions.
- **Do not lower the information density.** The **why**s that used to be written in Chinese
  (counter-intuitive design trade-offs, pitfalls hit, pointers to upstream docs) must be
  translated **point by point**, not compressed into a single sentence. Keep the `★` markers —
  they mark "there is a trap here"; dropping them downgrades a warning to small talk.
- **Point at upstream docs**: `docs/config-design.md`, `docs/api-v2-design.md` and the like are
  the design basis; do not restate their content in comments.

### Outside that scope, Chinese stays

| Scope | Why |
|---|---|
| **Test case names** | Descriptive text, not runtime output |
| **Test fixture data** | Sample values used by assertions (e.g. `"答完了"`); changing them detaches the assertion from the behavior under test |

★ **The factory agent prompts (`initcfg/templates/agents/*.md`, `.openheron-example/`) are
English — do not turn them back into Chinese.** They are both model prompts and file content the
user reads — so they follow the rules above. The context prompt built by `inputctx` is English
anyway (it is for the LLM), so that rule does not apply to it.

The test: **will this string be printed at runtime, returned to a caller, or written into a file
the user reads?** Yes → English; no → keep Chinese.

Why: error messages get grepped, get collected by log pipelines, and get consumed by the `Kind`
classification in `llm/common`; mixing languages breaks retrieval and matching while debugging,
and it mixes two languages into the `deferred` field of `/v2/config`. Interface copy is the same
story — the user reads the interface, not the source, and a Chinese/English mix makes "is this a
bug or did I misconfigure it?" harder to answer.

**There is exactly one log outlet**: `obs.Logger()` (`github.com/ouranoshong/openheron/agentkit/obs`).
Do not call `log.Printf` / `fmt.Fprintln(os.Stderr)` directly in business packages — the level
(Info / Warn / Error) and the prefix are defined in one place, and the level decides whether a
line shows up in the default output.

There is exactly one exception: `llm/common` uses the standard logger — it is a leaf package with
zero openheron dependencies, and importing `obs` would break that iron rule
(`docs/architecture.md` §2.1, "the two dependency iron rules").

## Other conventions

- **Do not duplicate business assembly logic in the API layer** — when you need "the effective
  tool table of some agent", let `agent.Builder` expose a read-only query instead of recomputing
  `Table.Blocked` / `Policy.TierOf`.
- **Config root resolution has exactly three levels**: explicit `--config-dir` →
  `$OPENHERON_CONFIG_DIR` → `<projectRoot>/.openheron` (created if missing).
  **No upward search of ancestor directories** — once the config root depends on "where the
  process was started from", the same command run from another directory reads a different
  config, and test instances end up reading the development config too. To change the config
  root, name it; do not add another lookup rule.
- **Archival comes before presentation** — when the SSE sink breaks, stop rendering but keep
  consuming the event stream, otherwise `Outcome.Intermediates` is incomplete and the history
  has holes.
- **`deny` beats `disabled_tools`** — the outer policy is the one guarantee a sub-agent cannot
  relax.
- Before changing configuration semantics, read upstream `docs/config-design.md`; before changing
  an interface, read `docs/api-design.md`; before adding a `/v2` endpoint, read
  `docs/api-v2-design.md`; before touching the frontend, read `docs/frontend-design.md`; before
  touching the test environment (ports / isolation directories), read `docs/frontend-design.md`
  §11.2 and `docs/config-design.md` §5.2. **Those upstream documents are not in this
  repository** — if you cannot get them, ask first. The `docs/architecture.md` /
  `docs/user-manual.md` / `docs/developer-manual.md` shipped here (and their `_zh` Chinese
  versions) describe **this code surface** as it actually is — just read them.
  `docs/context-usage-design.md` covers context usage and compaction: the usage dashboard and
  compaction persistence are already in the code; read it before changing context-usage code.

### Commit messages

`Conventional Commits`, one line, English, no decoration:

```
feat(ci): install job reads the assets from the artifact
fix(install): the API host is not the web host
docs(readme): drop the API table, it lives in the user manual
```

- The subject says **what changed and why it matters** — not a list of the files
  (the diff lists those) and not a walkthrough of the mechanics.
- One line. A body is optional; when there is one it says **why**, not what — the
  diff already answers what.
- A commit is a note to whoever reads it next, which is usually you in six months.
  Write it for that reader.
- No emoji, no decorative symbols, no exclamation marks.

### Frontend (`web/`)

- **A zustand selector must return a reference-stable value.** Building a new array/object on
  every call makes `Object.is` always report "changed" → `Maximum update depth exceeded` white
  screen. Use a module-level empty constant; a selector that returns an array pairs with
  `useShallow`.
- **Never write `?? []` in zustand selectors or in components.** Building a new array/object makes
  the snapshot always "changed" → infinite re-render → white screen. Use a module-level constant
  or a selector in the store.
- **Every interface change needs a Playwright spec** (reasons and acceptance requirements are in
  the "Testing" section below).

## Testing

### Ports: testing has its own environment

**All ports and paths are defined in `.env.test` at the repository root (committed)**; Vite,
Playwright and the smoke scripts all read the same file. To change a port, change only that one
file.

| | Dev | Test |
|---|---|---|
| openheron itself | :8080 | **:18099** |
| Vite | :5173 | **:15173** |
| E2E fake backend | — | **:18180** |

### Commands

| Command | Covers | Requires |
|---|---|---|
| `make test` | Full Go suite (`go test ./...`) | — |
| `cd web && bun run test:unit` | Pure-function layers (reducer / framing / store / highlighting) | — |
| `make test-e2e` | Real browser + deterministic fake backend | nothing (it ships its own fake backend) |
| `make test-install` | The release installer against a local fake release | nothing |
| `make smoke` | Real-backend integration: SSE framing, history replay, approval wiring | **the test instance must be running** |
| `make lint` | Go vet + gofmt + oxlint + tsc | — |
| `make lint-go` / `make lint-web` | The two halves of `make lint`, for a job with only one toolchain | — |
| `make check` | `lint` + `test`; run this before committing | — |

★ **CI runs the repo's own commands**: `make lint-go` + `make test`, `make lint-web` +
`bun run test:unit`, `make test-e2e`. The smokes are **not** in CI — they need a model
that answers, and that is not reproducible on a runner. A `v*` tag publishes a release
only after all three checks are green (developer-manual §3.9).

★ **Secrets are scanned twice.** The pre-commit hook (`make install-hooks`) scans the
staged diff; the `secrets` CI job scans the whole history. A key that was committed
before the hook existed is still in the history — only the CI scan finds that one.

★ **Never push to `main`.** It is protected: push a branch, open a pull request, and the
owner merges. A merge publishes nothing — only a `v*` tag does.

Start the test instance (a prerequisite of `make smoke`):

```bash
make test-env-backend   # foreground, :18099, separate config + separate session directory
make test-env-reset     # rebuild the test directories only, do not start
make test-env-sync      # ★ must run after you change agent definitions on the dev side
```

Single smoke scripts (they connect to :18099 by default, **no need to write `OPENHERON_BASE`**):

```bash
cd web && bun run smoke:interop            # SSE framing, concatenation, history hydration
cd web && bun run smoke:no-undefined-url   # never sends a malformed URL
cd web && bun run smoke:hitl               # approval resume works end to end
```

### Rules

- **Interface changes must be verified in a browser.** "I changed it and the tests are green" is
  not an acceptance basis — script-level tests cannot see the rendering layer; last round all 10
  smoke scripts were green while the interface was broken.
- **E2E uses the fake backend — do not switch it to the real backend.** The real backend depends
  on model cooperation, and when it fails you cannot tell a broken interface from a model that
  did not obey; with `retries` on, failures become noise and after three rounds nobody reads
  them. Protocol-level verification against the real backend is `make smoke`'s job; the two
  complement each other.
- **`smoke:hitl` is flaky, about 1 in 5** — it depends on the model *choosing* to call
  `write_file` to trigger an approval. That is the model's freedom, not a code regression; the
  script retries, and when nothing triggers it **reports the error explicitly** instead of
  pretending the wiring is broken. Do not use retry counts to cover it up.
- ★ **`smoke:hitl` has a prerequisite; without it the script fails 100% of the time (that is not
  "flaky")**: for **the agent that actually calls the tool**, its `write_file` must land in the
  `ask` tier (neither globally `deny`-ed nor disabled by its own `disabled_tools`) — otherwise
  the model cannot call that tool at all, and no approval can be triggered.
  ★ Do not stare at `main`: the factory config does list
  `disabled_tools: [write_file, edit_file, execute]` for `main`, but `main` only orchestrates and
  **delegates** the work to `task`, whose `write_file` sits in the `ask` tier — the approval
  still triggers. Observed event stream on 2026-10-29: `TOOL_CALL_START name=task` (the
  sub-agent is invoked as a tool), then `TOOL_CALL_START name=write_file subagentRunId=task`;
  the interrupted `subagentRunId` is `task` too.
  To check whether a tier is reachable, look at **each** agent's effective tool table:
  `curl -s :18099/v2/agents | grep -A3 disabledTools` and
  `curl -s :18099/v2/agents/task` (`effectiveTools` gives the tier and the reason).
  ★ `.openheron/` is **local untracked** configuration; changing it affects your own real
  agents (opening up `write_file` means that agent really can write files) — ask yourself
  first.
- **Different ports are not data isolation.** openheron writes sessions under `.openheron/state/`;
  a test that writes there pollutes the real history. The test instance treats
  `.openheron-test/project` as the project root and **pre-seeds its own `.openheron/`**, then
  names the config root with `OPENHERON_CONFIG_DIR` — the config root **does not search
  upward**, so the one at the repository root is structurally unreachable. Ports and state must
  be isolated **together**.
- **`make test-env-sync` is mandatory, not optional.** The test instance's
  `agents/ llm/ policy/ skills/` are **copied** from the dev side; without syncing, a changed
  agent definition is not what gets tested.
- **`pkill` needs `-x`; do not use `-f "bin/openheron"`.** `-f` matches the full command line
  and kills the very shell that ran the command (its command line contains that string), which
  shows up as an unresponsive command that looks hung. The correct form is `pkill -x openheron`.
  ★ **`ps | grep | kill` is the same disease**: it also matches the full command line, so the
    shell running it gets killed as well (its command line contains the search string). To kill
    by name use `pgrep -x <name>` (it looks at `comm`, not the command line).
  ★ When a process name exceeds 15 characters the kernel truncates `comm`
    (`openheron-multi-root` becomes `openheron-multi-roo`) and `-x` no longer matches — then
    find the PID by port (`ss -ltnp`) and kill them one by one.
- **Let it fail loudly when a port is taken.** `playwright.config.ts`'s
  `reuseExistingServer: false` and Vite's `strictPort: true` are deliberate: reusing the dev
  Vite would let E2E go "all green" while the browser talks to the real backend, and
  `OPENHERON_API_TARGET` would never be injected. **Do not "simplify" it to `!CI`.**
- **A force-killed Playwright leaves orphans holding ports** (the webServer children do not exit
  with it). A later run reports `already used` — that is not a broken config — clean up the
  processes first.
  ★ The symptom is `Error: http://127.0.0.1:18180/v2/health is already used`: the port is held
    by the previous round's Vite / fake backend, and `reuseExistingServer: false` is deliberate —
    so it **fails loudly** (which is exactly what we want). Clean the orphans, then run.
  ★ Do not clean processes **while** E2E is running: one `pkill -x vite` kills the webServer
    the current round is using, while Playwright is still waiting for its health check — the
    symptom is "the port never comes up and the test hangs at startup". Stop the test first.
- **Put long-running work in a background task**, not in a shell `&`. A background process does
  not survive the tool call that started it (it is killed when the call ends), which shows up
  as "it ran for a while and then vanished" and "the port never comes up" — while the log holds
  only the first line, so you cannot tell who killed it.
- **When you add or remove cases, sync the counts in the docs.** The tree in
  `docs/frontend-design.md` hard-codes the counts, so that "forgot to update" turns into a diff
  conflict instead of silent drift — the last 65/14 drifted for three rounds unnoticed.
- **After touching `bun run build`, dependencies, or the build flow, verify a fresh clone**
  (`rm -rf node_modules && bun install`, then run it again). Passing locally does not mean the
  manifest is right: ghost dependencies (imported in code but missing from `package.json`) only
  surface after a clean reinstall. The `.gitignore` exception rules have the same kind of trap —
  git decides level by level, and once a parent directory is excluded it no longer descends into
  children; the nearer rule wins.

The full isolation details are in `docs/frontend-design.md` §11.2 and
`docs/config-design.md` §5.2.

## Related documents

### Shipped here (maintained alongside the code surface)

| Document | Content |
|---|---|
| `README.md` / `README_zh.md` | **Project overview** — positioning, quick start, commands, configuration at a glance, HTTP reference |
| `docs/architecture.md` / `docs/architecture_zh.md` | **Architecture** — layering and dependency direction, configuration system, assembly, run loop, storage, HTTP, frontend, knowledge base |
| `docs/user-manual.md` / `docs/user-manual_zh.md` | **User guide** — installation, configuration, interface operation (for users, no internal design) |
| `docs/developer-manual.md` / `docs/developer-manual_zh.md` | **Developer guide** — layering, dependency iron rules, development recipes, testing and troubleshooting, known issues |
| `docs/context-usage-design.md` / `docs/context-usage-design_zh.md` | **Context-usage estimation design** — accounting rules, data flow, backend and frontend changes, persistence of compaction results and effective history (**the usage dashboard and compaction persistence are implemented**) |
| `docs/session-concurrency-design.md` / `docs/session-concurrency-design_zh.md` | **Session concurrency design** — why cross-client preemption happened, the "one turn per session" rule and its implementation, and how the frontend learns that a session is busy (**the admission gate and the 409 are implemented**) |
| `docs/fs-backend-design.md` / `docs/fs-backend-design_zh.md` | **Filesystem backend design** — why `eino-ext`'s local backend was forked into `agentkit/fsx`, how paths are confined to the project root, and how `ls`/`glob` are bounded (**implemented**) |
| `docs/run-lifecycle-design.md` / `docs/run-lifecycle-design_zh.md` | **Run lifecycle design** — why a turn is bound to one SSE connection today, and how a run becomes a durable row with a lease and a fence, so the connection can be demoted to a subscriber (**a proposal: §1 describes the code, §2 onwards do not exist yet**) |
| `docs/microvm-execution-design.md` / `docs/microvm-execution-design_zh.md` | **MicroVM execution isolation** — where `execute` runs today, the single `StreamingShell` interface a microVM would plug into, and what running the tool inside a microVM would and would not protect (**a proposal: §1 describes the code, §2 onwards do not exist yet**) |
| `docs/sandbox-kit-design.md` / `docs/sandbox-kit-design_zh.md` | **Docker Sandbox Kit packaging** — the openheron process as it is, a v3 workload Kit that would carry the whole process into a microVM, how the model key stays on the host, and the SSE path that has to be measured (**a proposal: §1 describes the code, §2 onwards do not exist yet**) |

★ The file without a suffix is the **English main version**; `_zh` is the Chinese version; the two
correspond one to one — change one and change the other.

### Upstream repository (not distributed with this repository)

★ The documents below live in the **upstream repository**. When you cite them, make sure you are
reading the upstream copy — `docs/architecture.md` **exists in both repositories** with different
content: the one here describes **this code surface** as it actually is, the upstream one is the
overview and the index of the volumes.

| Document | Content |
|---|---|
| `docs/implementation-audit.md` | **Implementation audit** — what was not built, what is documentation drift (2026-10-23) |
| `docs/hygiene-research.md` | **Code hygiene research** — exported symbols with no consumer, third-party archives, dead code (2026-10-23) |
| `docs/agent-architecture-survey.md` | **Survey of external agent architectures** — the gateway and background-service problems of pi / rakazo / OpenClaw / Hermes / Letta (2026-10-24) |
| `docs/positioning.md` | **Positioning and trade-offs** — expert per expert model × agent as a tool: mechanism breakdown and boundaries (2026-10-25) |
| `docs/config-design.md` | Config schema, layering, validation, hot reload |
| `docs/api-design.md` | The old unprefixed APIs (**retired on 2026-10-23 together with M3**, kept as history) |
| `docs/api-v2-design.md` | The `/v2` API family and the AG-UI event contract |
| `docs/frontend-design.md` | React frontend architecture and interface |
| `docs/agent-design.md` | Agent pool, approvals, depth protection |
| `docs/agent-design-v2.md` | Agent assembly API v2 (`deep.Config` leaves openheron) |
| `docs/multi-agent-design.md` | Multiple roles, parallel delegation, per-agent accounting and history filtering |
| `docs/config-editor-design.md` | The config editor (six `/v2/settings` routes + the interface) |
| `docs/deep-config-fields.md` | Field reference for `deep.Config` |
| `docs/checkpoint-store.md` | Checkpoint Store: the source of truth for approval resume |
| `docs/kb-design.md` | The knowledge base |
| `docs/init-design.md` | `openheron init` — the minimal runnable initialization of the secondary (project-level) configuration |
| `docs/llm-common.md` | The vendor protocol layer |
