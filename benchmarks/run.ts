#!/usr/bin/env bun
// The benchmark runner.
//
// ★ It drives a **running** server over HTTP: it swaps each arm's agents
//   into the config root (the agents are hot-reloaded), runs every task
//   against a fresh copy of the fixture tree, and reads the usage back
//   from the transcript.
//
// ★ The cost comes from the transcript, not from an estimate: every
//   assistant message carries the endpoint's own token accounting
//   (ResponseMeta) and the agent that produced it, so the per-role split
//   is the durable record rather than a number the benchmark made up.
//
// Usage:
//   bun run benchmarks/run.ts \
//     --base http://127.0.0.1:18099 \
//     --config <the config root> \
//     --work <the project root the agents may touch> \
//     [--repeats 3] [--arm a-max-parent-flash-workers] [--kind exec]
//
// ★ --config must point at a **scratch** config root. The runner rewrites
//   its agents/ directory on every arm.

import { mkdir, readFile, rm, cp } from "node:fs/promises"
import { existsSync } from "node:fs"
import { dirname, join, resolve } from "node:path"

type Price = { input: number; output: number; cached_input: number }
type AgentSpec = { file: string; id: string; role: string; model: string }
type Arm = { id: string; label: string; agents: AgentSpec[] }
type Check =
  | { type: "answer_contains"; value: string }
  | { type: "file_contains"; value: string; contains: string }
  | { type: "file_exists"; value: string }
type Task = { id: string; kind: string; prompt: string; checks: Check[] }
type Usage = { promptTokens: number; completionTokens: number; totalTokens: number; cachedTokens: number }

const here = dirname(new URL(import.meta.url).pathname)

const args = parseArgs(process.argv.slice(2))
const base = (args.base ?? "http://127.0.0.1:18099").replace(/\/$/, "")
const configRoot = resolve(args.config ?? ".openheron-bench")
// ★ The work directory is **scratch**: the runner wipes it before every
//   task. It must not be the fixture directory — that is the source.
const workRoot = resolve(args.work ?? join(here, "out", "work"))
const fixtureRoot = join(here, "fixtures", "work")
const outDir = join(here, "out")
const repeats = Number(args.repeats ?? 3)
// ★ The timeout is a guard against a hung model, not a budget. It is
//   generous on purpose: an arm that delegates twice (a scout pass and
//   an implementation pass) needs roughly twice the wall clock of a solo
//   arm, and a timeout that cuts the slower shape off would bias the
//   comparison against it. The run is aborted when the timeout fires, so
//   the extra allowance is paid for at most once per run.
const runTimeoutMs = Number(args.timeout ?? 600_000)

const armsDoc = Bun.YAML.parse(await readFile(join(here, "arms.yaml"), "utf8")) as {
  prices: Record<string, Price>
  arms: Arm[]
  models: Record<string, string>
  baseline?: string
}
const tasksDoc = Bun.YAML.parse(await readFile(join(here, "tasks.yaml"), "utf8")) as { tasks: Task[] }

// ★ --task and --arm take a comma-separated list, so a sweep can name
//   several of them without being run once per name. The old single-value
//   form still works: "a,b" splits into two, and a lone "a" stays one.
const wantedTasks = splitList(args.task)
const wantedArms = splitList(args.arm)
const arms = armsDoc.arms.filter((a) => !wantedArms.length || wantedArms.includes(a.id))
const tasks = tasksDoc.tasks.filter(
  (t) =>
    (!args.kind || t.kind === args.kind) && (!wantedTasks.length || wantedTasks.includes(t.id)),
)

/** splitList turns "a,b" into ["a","b"] and an empty value into []. */
function splitList(value: string | undefined): string[] {
  if (!value) return []
  return value
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
}

// ★ The baseline arm is declared in arms.yaml, not guessed from the order
//   of the arms: "one strong model does everything" is the alternative
//   the decomposition has to beat, so it is the only meaningful
//   denominator. When the baseline is not part of this run (a filtered
//   sweep), the summary falls back to the first arm it has.
const baselineArm = armsDoc.baseline ?? arms[0]?.id

if (arms.length === 0 || tasks.length === 0) {
  console.error("no arm or task selected")
  process.exit(2)
}

// ★ The work directory gets wiped before every task. Pointing it at the
//   fixture directory would delete the fixtures — and at the repository
//   root would delete the repository.
if (workRoot === fixtureRoot || workRoot === resolve(".") || workRoot === resolve(here)) {
  console.error(
    `--work must be a scratch directory, not ${workRoot}\n` +
      `it is wiped before every task (the fixtures live in ${fixtureRoot})`,
  )
  process.exit(2)
}

type Run = {
  arm: string
  task: string
  kind: string
  repeat: number
  ok: boolean
  failedChecks: string[]
  answer: string
  turns: number
  usage: Record<string, Usage>
  cost: number
  costBreakdown: Record<string, number>
  // ★ The thread id is the session's own id, so the transcript of a run can
  //   be found without guessing. analyze.ts needs it: the per-turn cost
  //   breakdown (who spent what before and after the delegation) lives in
  //   the transcript, not in this summary.
  threadId: string
  // ★ A run that never finished has no final cost. The calls it made and
  //   never got an answer for are billed by the provider, and the endpoint
  //   reports no usage for them, so no accounting here can see them.
  //   `cost` is what the transcript proves; the rest is unknown. Reporting
  //   a zero would read as "this run was free", and it was not.
  costIncomplete: boolean
  // ★ An agent that ran on more than one model: its token bucket cannot be
  //   split between them, so its price is a fallback rather than a
  //   measurement. Empty in every ordinary run.
  modelConflicts: string[]
  error?: string
}

const results: Run[] = []

/** byCategory is the cost split by what was paid for.
 *
 * ★ The cached input is 8× cheaper than the fresh one, and the input a
 *   delegation has to read — the workers' output — is fresh by
 *   definition. A single total hides that.
 *
 * ★ It is declared here, above the first `writeReport()` call, because a
 *   `const` is in its temporal dead zone until this line runs: declaring it
 *   further down made every sweep die with "Cannot access 'byCategory'
 *   before initialization" before a single task ran.
 */
const byCategory = { cachedInput: 0, freshInput: 0, output: 0 }

const report: Record<string, unknown> = {
  base,
  configRoot,
  repeats,
  prices: armsDoc.prices,
  baseline: baselineArm,
  runs: results,
  summary: {},
}

async function writeReport() {
  report.runs = results
  report.summary = summarize(results)
  report.costByCategory = { ...byCategory }
  await mkdir(outDir, { recursive: true })
  await Bun.write(join(outDir, "report.json"), JSON.stringify(report, null, 2))
}

for (const arm of arms) {
  await installArm(arm)
  // The agents are hot-reloaded; give the server a moment to pick them up.
  await sleep(1500)
  console.error(`\n=== arm ${arm.id} (${arm.label}) ===`)

  for (const task of tasks) {
    for (let repeat = 1; repeat <= repeats; repeat++) {
      const run = await runTask(arm, task, repeat)
      results.push(run)
      // ★ The report is written after every run: a sweep that is killed
      //   halfway must not lose the runs it already paid for.
      await writeReport()
      console.error(
        `  ${run.ok ? "✓" : "✗"} ${task.id} #${repeat}  ¥${run.cost.toFixed(4)}  ` +
          `turns=${run.turns}  ${run.ok ? "" : run.failedChecks.join("; ")}`,
      )
    }
  }
}

// ── the report ────────────────────────────────────────────────────────────

// ★ The report is written after every run, so it must exist before the
//   first one: a sweep that is killed halfway must not lose the runs it
//   already paid for.

await writeReport()
console.error(`\n${renderSummary(report as any)}`)
console.error(`\nfull report: ${join(outDir, "report.json")}`)

// ── the pieces ────────────────────────────────────────────────────────────

async function installArm(arm: Arm) {
  const dir = join(configRoot, "agents")
  await rm(dir, { recursive: true, force: true })
  await mkdir(dir, { recursive: true })
  for (const agent of arm.agents) {
    const src = join(here, agent.file)
    let text = await readFile(src, "utf8")
    // The model comes from the arm table, so the prompt file and the price
    // table can never drift apart.
    text = text.replace(/^model: .*$/m, `model: ${agent.model}`)
    await Bun.write(join(dir, `${agent.id}.md`), text)
  }
}

async function runTask(arm: Arm, task: Task, repeat: number): Promise<Run> {
  // Every task starts from the same tree.
  await rm(workRoot, { recursive: true, force: true })
  await mkdir(workRoot, { recursive: true })
  await cp(fixtureRoot, workRoot, { recursive: true })

  const run: Run = {
    arm: arm.id,
    task: task.id,
    kind: task.kind,
    repeat,
    ok: false,
    failedChecks: [],
    answer: "",
    turns: 0,
    usage: {},
    cost: 0,
    costBreakdown: {},
    costIncomplete: false,
    modelConflicts: [],
  }

  try {
    const created = await postJSON("/v2/sessions", {})
    const threadId = created.threadId as string
    run.threadId = threadId

    const events = await postRun(threadId, task.prompt)
    run.turns = countModelTurns(events)
    run.answer = finalAnswer(events)

    // ★ A run that ended in RUN_ERROR produced nothing to grade — it is
    //   an error, not a failed answer. Without this, a quota that ran out
    //   halfway through the sweep looks like a task the model could not
    //   do, and the report's cost column says ¥0 for it.
    const runError = events.find((e) => e.type === "RUN_ERROR")
    if (runError) {
      throw new Error(
        `${runError.code ?? "run_error"}: ${String(runError.message ?? "").slice(0, 200)}`,
      )
    }

    const view = await getJSON(`/v2/sessions/${threadId}/messages`)
    run.usage = (view.usage?.byAgent ?? {}) as Record<string, Usage>

    // ★ The model each agent ran on is read from the transcript, not from
    //   the arm table. The arm table says which model an agent should run
    //   on; the message says which one it did. Pricing the measurement is
    //   what makes "the strong model costs X" a fact about the run rather
    //   than a fact about the configuration.
    const measured = measuredModels(view.messages)
    run.modelConflicts = measured.conflicts
    // ★ The measured model wins. The arm table's mapping is the fallback
    //   for an agent that ran on two models — its token bucket cannot be
    //   split between them, so its price is a guess, and the conflict is
    //   reported so the guess is visible rather than silent.
    const modelOf: Record<string, string> = { ...armModelOf(arm), ...measured.models }

    const { cost, breakdown } = costOf(armsDoc.prices, run.usage, modelOf)
    run.cost = cost
    run.costBreakdown = breakdown

    const failed: string[] = []
    for (const check of task.checks) {
      const ok = await evaluate(check, run.answer, workRoot)
      if (!ok) failed.push(`${check.type}:${check.value}`)
    }
    run.failedChecks = failed
    run.ok = failed.length === 0
  } catch (e) {
    run.error = (e as Error).message
    // ★ A failed run is not a free run: the provider bills every call it
    //   made, and the calls that never came back are the ones this
    //   transcript cannot see. The cost below is the part the transcript
    //   proves; the flag says the rest is unknown.
    run.costIncomplete = true
  }
  return run
}

/**
 * armModelOf is the arm table's own answer to "which model does this agent
 * run on".
 *
 * ★ It is the fallback, not the source: the run's own messages say which
 *   model each agent actually used, and that is what the run is priced
 *   with.
 */
function armModelOf(arm: Arm): Record<string, string> {
  const out: Record<string, string> = {}
  for (const a of arm.agents) {
    out[a.id] = a.model.split("/").pop() ?? a.model
  }
  return out
}

/**
 * measuredModels reads the model each agent actually ran on, from the
 * transcript the API serves.
 *
 * ★ The arm table says which model an agent *should* use; the message
 *   says which one it *did*. The second is the one worth pricing: this
 *   benchmark exists to compare what the strong model costs against what
 *   the cheap one costs, and that comparison is only as good as the model
 *   attribution behind it.
 *
 *   The agent's name is on the message (`name`), and the model it ran on
 *   is in the metadata (`metadata.model`) — both written by the server
 *   when it archived the message.
 */
function measuredModels(messages: any[]): {
  models: Record<string, string>
  conflicts: string[]
} {
  const seen: Record<string, Set<string>> = {}
  for (const m of messages ?? []) {
    const agent = m?.name
    const model = m?.metadata?.model
    if (!agent || !model) continue
    const bare = model.split("/").pop() ?? model
    ;(seen[agent] ??= new Set()).add(bare)
  }
  const models: Record<string, string> = {}
  const conflicts: string[] = []
  for (const [agent, set] of Object.entries(seen)) {
    if (set.size === 1) {
      models[agent] = [...set][0]
      continue
    }
    // ★ One agent, two models: its token bucket cannot be split between
    //   them, so any single price for it is a guess. The guess is
    //   reported rather than made silently.
    conflicts.push(`${agent}: ${[...set].sort().join(" / ")}`)
  }
  return { models, conflicts }
}

/** The cost of one run, split by role.
 *
 * ★ The tokens come from the endpoint's own accounting on each message,
 *   and the model they are priced with is the one the agent ran on.
 */
function costOf(
  prices: Record<string, Price>,
  usage: Record<string, Usage>,
  modelOf: Record<string, string>,
): { cost: number; breakdown: Record<string, number> } {
  const breakdown: Record<string, number> = {}
  let cost = 0
  for (const [agent, u] of Object.entries(usage)) {
    const model = modelOf[agent]
    if (!model) continue
    const price = prices[model]
    if (!price) continue
    const cached = u.cachedTokens ?? 0
    const fresh = Math.max(0, (u.promptTokens ?? 0) - cached)
    const output = u.completionTokens ?? 0
    const c =
      (fresh * price.input + cached * price.cached_input + output * price.output) /
      1_000_000
    cost += c
    breakdown[agent] = c
    // ★ The three categories are not interchangeable. The cached input is
    //   8× cheaper, and the input that a delegation *has* to read — the
    //   workers' output — is fresh by definition. A cost report that
    //   adds them up hides that.
    byCategory.cachedInput += (cached * price.cached_input) / 1_000_000
    byCategory.freshInput += (fresh * price.input) / 1_000_000
    byCategory.output += (output * price.output) / 1_000_000
  }
  return { cost, breakdown }
}

async function evaluate(check: Check, answer: string, workRoot: string): Promise<boolean> {
  switch (check.type) {
    case "answer_contains":
      // ★ Case-insensitive: the model's capitalisation is not a fact about
      //   the answer. "Memory does:" answers "what breaks first" just as
      //   well as "memory does".
      return answer.toLowerCase().includes(check.value.toLowerCase())
    case "file_exists":
      return existsSync(join(workRoot, check.value))
    case "file_contains": {
      const path = join(workRoot, check.value)
      if (!existsSync(path)) return false
      return (await readFile(path, "utf8")).includes(check.contains)
    }
    default:
      return false
  }
}

function summarize(runs: Run[]) {
  const byArm = new Map<string, Run[]>()
  for (const r of runs) {
    const list = byArm.get(r.arm) ?? []
    list.push(r)
    byArm.set(r.arm, list)
  }
  const out: Record<string, unknown> = {}
  for (const [arm, list] of byArm) {
    const total = list.reduce((s, r) => s + r.cost, 0)
    const passed = list.filter((r) => r.ok).length
    out[arm] = {
      runs: list.length,
      passed,
      passRate: list.length ? passed / list.length : 0,
      totalCost: total,
      meanCost: list.length ? total / list.length : 0,
      meanTurns: list.length ? list.reduce((s, r) => s + r.turns, 0) / list.length : 0,
      // ★ The kinds are derived from the runs, not written down here.
      //   A hardcoded list silently drops every kind that was added later:
      //   `big` was in the task set and in the totals, but missing from
      //   `byKind`, so the per-kind breakdown did not add up to the
      //   arm total and the summary printed a story that the numbers did
      //   not support. The order of first appearance is stable.
      byKind: Object.fromEntries(
        [...new Set(list.map((r) => r.kind))].map((kind) => {
          const k = list.filter((r) => r.kind === kind)
          return [
            kind,
            {
              runs: k.length,
              passed: k.filter((r) => r.ok).length,
              totalCost: k.reduce((s, r) => s + r.cost, 0),
            },
          ]
        }),
      ),
    }
  }
  return out
}

function renderSummary(report: { summary: Record<string, any> }): any {
  const lines: string[] = []
  const armNames = Object.keys(report.summary)
  // ★ The baseline is the arm named on the command line, not "the first
  //   one in the object". The per-kind ratio is the number that actually
  //   answers "is the strong-parent/weak-worker split worth it", and it
  //   is the number that used to have to be computed by hand.
  const baseName = baselineArm && report.summary[baselineArm] ? baselineArm : armNames[0]
  const base = report.summary[baseName] as any
  for (const [arm, s] of Object.entries(report.summary)) {
    const ratio = base ? s.totalCost / base.totalCost : 1
    lines.push(
      `${arm.padEnd(28)} ¥${s.totalCost.toFixed(4).padStart(10)}  ` +
        `pass ${s.passed}/${s.runs}  turns ${s.meanTurns.toFixed(1)}  ` +
        `ratio ${ratio.toFixed(3)}${arm === baseName ? "  (baseline)" : ""}`,
    )
    for (const [kind, k] of Object.entries(s.byKind)) {
      const bk = base?.byKind?.[kind]
      const kRatio = bk?.totalCost ? k.totalCost / bk.totalCost : 1
      lines.push(
        `  ${kind.padEnd(26)} ¥${k.totalCost.toFixed(4).padStart(10)}  ` +
          `pass ${k.passed}/${k.runs}  ratio ${kRatio.toFixed(3)}`,
      )
    }
  }
  return lines.join("\n")
}

// ── the HTTP ──────────────────────────────────────────────────────────────

async function postJSON(path: string, body: unknown): Promise<any> {
  const res = await fetch(base + path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`${path} → ${res.status} ${await res.text()}`)
  return res.json()
}

async function getJSON(path: string): Promise<any> {
  const res = await fetch(base + path)
  if (!res.ok) throw new Error(`${path} → ${res.status} ${await res.text()}`)
  return res.json()
}

/** postRun starts a run and collects its frames.
 *
 * ★ The frames are read to the end: the run's cost is only final once the
 *   turn is archived.
 */
async function postRun(threadId: string, prompt: string): Promise<any[]> {
  const controller = new AbortController()
  const res = await fetch(base + "/v2/run", {
    method: "POST",
    signal: controller.signal,
    headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
    body: JSON.stringify({ threadId, query: prompt }),
  })
  if (!res.ok || !res.body) throw new Error(`/v2/run → ${res.status} ${await res.text()}`)
  const events: any[] = []
  // ★ A run that never finishes must not take the sweep with it. The
  //   timeout is generous — it is a guard against a hung model, not a
  //   budget — and a timed-out run is recorded as a failed one.
  const timer = setTimeout(() => controller.abort(), runTimeoutMs)
  try {
    for await (const ev of readSse(res.body)) {
      try {
        events.push(JSON.parse(ev.data))
      } catch {
        // a frame that will not parse is not a reason to stop reading
      }
    }
  } catch (e) {
    // ★ The run does not stop when the connection dies — it goes on
    //   spending money on a run nobody is watching (api/v2_run.go:164:
    //   "a connection that dies leaves the run going"). That is the
    //   right behaviour for a user who closes the browser and expects
    //   the agent to keep working; it is the wrong behaviour for a
    //   benchmark that has given up. Abort it explicitly, or the provider
    //   bills for calls whose answers nobody will read.
    //
    //   The abort is idempotent (api/v2_sessions.go:395), so it is safe
    //   on a run that already ended — which is the case for every error
    //   except the timeout.
    await abortRun(threadId)
    if ((e as Error).name === "AbortError") {
      throw new Error(`the run did not finish within ${runTimeoutMs / 1000}s`)
    }
    throw e
  } finally {
    clearTimeout(timer)
  }
  return events
}

/** abortRun stops a run the client has given up on.
 *
 * ★ POST /v2/sessions/{id}/abort is idempotent and succeeds for a live
 *   session (api/v2_sessions.go:395), so this is safe to call on a run
 *   that already ended.
 */
async function abortRun(threadId: string): Promise<void> {
  try {
    await postJSON(`/v2/sessions/${threadId}/abort`, {})
  } catch {
    // The run is already gone: there is nothing left to stop, and a
    // failure here must not mask the timeout that caused it.
  }
}

/** readSse cuts a byte stream into SSE events. */
async function* readSse(body: ReadableStream<Uint8Array>): AsyncGenerator<{ data: string }> {
  const reader = body.getReader()
  const decoder = new TextDecoder()
  let buffer = ""
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    let idx: number
    while ((idx = buffer.indexOf("\n\n")) >= 0) {
      const raw = buffer.slice(0, idx)
      buffer = buffer.slice(idx + 2)
      const data = raw
        .split("\n")
        .filter((l) => l.startsWith("data:"))
        .map((l) => l.slice(5).trim())
        .join("\n")
      if (data) yield { data }
    }
  }
}

/** countModelTurns counts the model's own turns.
 *
 * ★ A model turn is one assistant message with content: it is the unit
 *   the model spends tokens on.
 */
function countModelTurns(events: any[]): number {
  // ★ The frames do not carry a per-call token count; the transcript does.
  //   Count the assistant text messages instead: one per model turn, in the
  //   common case of one message per call.
  const ids = new Set<string>()
  for (const ev of events) {
    if (ev.type === "TEXT_MESSAGE_START" && ev.messageId) ids.add(ev.messageId)
  }
  return ids.size
}

function finalAnswer(events: any[]): string {
  const chunks = new Map<string, string>()
  let last = ""
  for (const ev of events) {
    if (ev.type === "TEXT_MESSAGE_CONTENT" && ev.messageId) {
      chunks.set(ev.messageId, (chunks.get(ev.messageId) ?? "") + (ev.delta ?? ""))
    }
    if (ev.type === "TEXT_MESSAGE_START" && ev.messageId) {
      last = ev.messageId
    }
  }
  return chunks.get(last) ?? ""
}

function parseArgs(argv: string[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]
    if (!a.startsWith("--")) continue
    const key = a.slice(2)
    const next = argv[i + 1]
    if (next && !next.startsWith("--")) {
      out[key] = next
      i++
    } else {
      out[key] = "true"
    }
  }
  return out
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms))
}
