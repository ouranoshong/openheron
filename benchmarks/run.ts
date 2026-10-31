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

const args = parseArgs(process.argv.slice(2))
const base = (args.base ?? "http://127.0.0.1:18099").replace(/\/$/, "")
const configRoot = resolve(args.config ?? ".openheron")
const workRoot = resolve(args.work ?? ".")
const repeats = Number(args.repeats ?? 3)
const here = dirname(new URL(import.meta.url).pathname)

const armsDoc = Bun.YAML.parse(await readFile(join(here, "arms.yaml"), "utf8")) as {
  prices: Record<string, Price>
  arms: Arm[]
  models: Record<string, string>
}
const tasksDoc = Bun.YAML.parse(await readFile(join(here, "tasks.yaml"), "utf8")) as { tasks: Task[] }

const arms = armsDoc.arms.filter((a) => !args.arm || a.id === args.arm)
const tasks = tasksDoc.tasks.filter((t) => !args.kind || t.kind === args.kind)

if (arms.length === 0 || tasks.length === 0) {
  console.error("no arm or task selected")
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
  error?: string
}

const results: Run[] = []

for (const arm of arms) {
  await installArm(arm)
  // The agents are hot-reloaded; give the server a moment to pick them up.
  await sleep(1500)
  console.error(`\n=== arm ${arm.id} (${arm.label}) ===`)

  for (const task of tasks) {
    for (let repeat = 1; repeat <= repeats; repeat++) {
      const run = await runTask(arm, task, repeat)
      results.push(run)
      console.error(
        `  ${run.ok ? "✓" : "✗"} ${task.id} #${repeat}  ¥${run.cost.toFixed(4)}  ` +
          `turns=${run.turns}  ${run.ok ? "" : run.failedChecks.join("; ")}`,
      )
    }
  }
}

// ── the report ────────────────────────────────────────────────────────────

const report = {
  base,
  configRoot,
  repeats,
  prices: armsDoc.prices,
  runs: results,
  summary: summarize(results),
}

const outDir = join(here, "out")
await mkdir(outDir, { recursive: true })
await Bun.write(join(outDir, "report.json"), JSON.stringify(report, null, 2))
console.error(`\n${renderSummary(report)}`)
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
  await cp(join(here, "fixtures", "work"), workRoot, { recursive: true })

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
  }

  try {
    const created = await postJSON("/v2/sessions", {})
    const threadId = created.threadId as string

    const events = await postRun(threadId, task.prompt)
    run.turns = countModelTurns(events)
    run.answer = finalAnswer(events)

    const view = await getJSON(`/v2/sessions/${threadId}/messages`)
    run.usage = (view.usage?.byAgent ?? {}) as Record<string, Usage>

    const { cost, breakdown } = costOf(arm, armsDoc.prices, run.usage)
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
  }
  return run
}

/** The cost of one run, split by role.
 *
 * ★ The agent's model comes from the arm table, and the tokens come from
 *   the endpoint's own accounting on each message. Nothing here is
 *   estimated.
 */
function costOf(
  arm: Arm,
  prices: Record<string, Price>,
  usage: Record<string, Usage>,
): { cost: number; breakdown: Record<string, number> } {
  // ★ The agent's model is a `provider/model` ref; the price table is keyed
  //   by the bare model name. Stripping the prefix here keeps one source
  //   of truth: the arm table.
  const modelOf = new Map(arm.agents.map((a) => [a.id, a.model.split("/").pop() ?? a.model]))
  const breakdown: Record<string, number> = {}
  let cost = 0
  for (const [agent, u] of Object.entries(usage)) {
    const model = modelOf.get(agent)
    if (!model) continue
    const price = prices[model]
    if (!price) continue
    const cached = u.cachedTokens ?? 0
    const fresh = Math.max(0, (u.promptTokens ?? 0) - cached)
    const c =
      (fresh * price.input +
        cached * price.cached_input +
        (u.completionTokens ?? 0) * price.output) /
      1_000_000
    cost += c
    breakdown[agent] = c
  }
  return { cost, breakdown }
}

async function evaluate(check: Check, answer: string, workRoot: string): Promise<boolean> {
  switch (check.type) {
    case "answer_contains":
      return answer.includes(check.value)
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
      byKind: Object.fromEntries(
        ["exec", "decompose", "judge"].map((kind) => {
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

function renderSummary(report: { summary: Record<string, any> }): string {
  const lines: string[] = []
  const base = report.summary[Object.keys(report.summary)[0]] as any
  for (const [arm, s] of Object.entries(report.summary)) {
    const ratio = base ? s.totalCost / base.totalCost : 1
    lines.push(
      `${arm.padEnd(28)} ¥${s.totalCost.toFixed(4).padStart(10)}  ` +
        `pass ${s.passed}/${s.runs}  turns ${s.meanTurns.toFixed(1)}  ratio ${ratio.toFixed(3)}`,
    )
    for (const [kind, k] of Object.entries(s.byKind)) {
      lines.push(`  ${kind.padEnd(26)} ¥${k.totalCost.toFixed(4).padStart(10)}  pass ${k.passed}/${k.runs}`)
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
  const res = await fetch(base + "/v2/run", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
    body: JSON.stringify({ threadId, query: prompt }),
  })
  if (!res.ok || !res.body) throw new Error(`/v2/run → ${res.status} ${await res.text()}`)
  const events: any[] = []
  for await (const ev of readSse(res.body)) {
    try {
      events.push(JSON.parse(ev.data))
    } catch {
      // a frame that will not parse is not a reason to stop reading
    }
  }
  return events
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
