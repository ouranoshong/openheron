#!/usr/bin/env bun
// The transcript analyzer.
//
// ★ It answers the question the summary in report.json cannot: **where did
//   the money go?** A total says A cost more than B. It does not say whether
//   the extra money was the planning, the handoff, the worker's own tokens,
//   or the parent re-reading what the worker had already done.
//
// ★ The split comes from the transcript, not from an estimate. Every
//   assistant message carries the endpoint's own token accounting
//   (response_meta.usage) and the agent that produced it
//   (openheron_agent), so the per-stage cost is a record.
//
// ★ The stages are the ones the arrangement has:
//     analyze — the parent works out what has to happen, before it delegates
//     handoff — the parent writes the task order (the `task` tool call)
//     child   — the worker executes
//     accept  — the parent checks the worker's report
//   A solo parent has no handoff or child: its turns are split at the first
//   write/edit/execute call into analyze and land.
//
// ★ Two checks run on every number, and both must pass:
//     1. The staged sum equals the cost the runner recorded for the run.
//     2. The decomposition identity holds:
//        (land saved) - (coordination) - (extra analysis) == A - B.
//   A benchmark whose own arithmetic does not close is not a measurement.
//
// Usage:
//   bun run benchmarks/analyze.ts [--report out/report.json] [--config <root>]
//                                  [--report-limit 2000]

import { readFile } from "node:fs/promises"
import { existsSync } from "node:fs"
import { join, dirname, resolve } from "node:path"

type Usage = {
  promptTokens: number
  completionTokens: number
  totalTokens: number
  cachedTokens: number
}
type Price = { input: number; output: number; cached_input: number }
type Arm = { id: string; label: string; agents: { id: string; model: string }[] }
type Stage = {
  cost: number
  cached: number
  fresh: number
  out: number
  turns: number
}

const here = dirname(new URL(import.meta.url).pathname)
const args = parseArgs(process.argv.slice(2))
const reportPath = resolve(args.report ?? join(here, "out", "report.json"))
const configRoot = resolve(args.config ?? join(here, "..", ".openheron-bench"))
const sessionsDir = join(configRoot, "state", "sessions")
// ★ A report longer than this is flagged: it means the worker pasted file
//   contents or full command output, which is the thing arm A′ exists to
//   stop. It does not fail the run — it is a finding, not a verdict.
const reportLimit = Number(args["report-limit"] ?? 2000)

const report = JSON.parse(await readFile(reportPath, "utf8")) as {
  prices: Record<string, Price>
  baseline?: string
  runs: {
    arm: string
    task: string
    kind: string
    repeat: number
    ok: boolean
    failedChecks: string[]
    cost: number
    costIncomplete?: boolean
    threadId?: string
    error?: string
  }[]
}

// ★ The arm table lives in arms.yaml, not in the report: the report records
//   what ran, the arm table records what an arm *is*. The analyzer needs
//   the arm table to know which model priced which agent.
const armsDoc = Bun.YAML.parse(await readFile(join(here, "arms.yaml"), "utf8")) as {
  arms: { id: string; label: string; agents: { id: string; model: string }[] }[]
}

if (!existsSync(sessionsDir)) {
  console.error(`no session directory at ${sessionsDir}`)
  process.exit(2)
}

// ── the transcripts ────────────────────────────────────────────────────────

type Msg = {
  role?: string
  content?: string
  tool_calls?: { id?: string; function: { name: string; arguments?: string } }[]
  tool_call_id?: string
  tool_name?: string
  extra?: Record<string, any>
  response_meta?: { usage?: any }
}

/** readSession returns the transcript of one session, or null. */
async function readSession(id: string): Promise<Msg[] | null> {
  const path = join(sessionsDir, id, "session.jsonl")
  if (!existsSync(path)) return null
  const raw = await readFile(path, "utf8")
  const out: Msg[] = []
  for (const line of raw.split("\n")) {
    if (!line.trim()) continue
    try {
      out.push(JSON.parse(line))
    } catch {
      // a line that does not parse is not a reason to drop the transcript
    }
  }
  return out
}

// ── the cost of one message ────────────────────────────────────────────────

function bareModel(ref: string): string {
  return ref.split("/").pop() ?? ref
}

/** costOf prices one message's usage with the model's price. */
function costOf(usage: any, model: string, prices: Record<string, Price>): Stage | null {
  const price = prices[model]
  if (!price || !usage) return null
  const cached = usage.prompt_token_details?.cached_tokens ?? 0
  const fresh = Math.max(0, (usage.prompt_tokens ?? 0) - cached)
  const out = usage.completion_tokens ?? 0
  return {
    cost: (fresh * price.input + cached * price.cached_input + out * price.output) / 1_000_000,
    cached,
    fresh,
    out,
    turns: 1,
  }
}

function addStage(dst: Record<string, Stage>, key: string, s: Stage | null) {
  if (!s) return
  const cur = (dst[key] ??= { cost: 0, cached: 0, fresh: 0, out: 0, turns: 0 })
  cur.cost += s.cost
  cur.cached += s.cached
  cur.fresh += s.fresh
  cur.out += s.out
  cur.turns += s.turns
}

// ── the stages ─────────────────────────────────────────────────────────────

type Analysis = {
  stages: Record<string, Stage>
  // ★ The per-sub-agent cost breakdown, kept out of `stages` so it cannot be
  //   double counted into the total.
  subStages: Record<string, Stage>
  delegated: boolean
  reportChars: number
  acceptRereads: number
  total: number
  // ★ How the parent used the `task` tool. The sub-agent **is** a tool: the
  //   parent pays for the arguments it writes and for the result it reads
  //   back. Arm A″ calls it twice — once to have a scout investigate, once to
  //   have the worker implement — so the counts and the report sizes per
  //   sub-agent are what tell the two shapes apart.
  taskCalls: { sub: string; argsChars: number }[]
  reportCharsBySub: Record<string, number>
}

/** subAgentOf reads the subagent_type out of a `task` tool call. */
function subAgentOf(tc: { function: { name: string; arguments?: string } }): string {
  if (tc.function.name !== "task") return ""
  try {
    const args = JSON.parse(tc.function.arguments ?? "{}")
    return typeof args.subagent_type === "string" ? args.subagent_type : ""
  } catch {
    return ""
  }
}

/**
 * segment splits a transcript into the stages of the arrangement.
 *
 * ★ The parent's handoff is the assistant turn that calls the `task` tool:
 *   that is the moment the work moves to a sub-agent. Everything before it is
 *   the parent's planning, everything after it is the parent's acceptance.
 *
 * ★ Arm A″ calls `task` **twice**: first with `subagent_type: scout` to have
 *   the code investigated, then with `subagent_type: task` to have it
 *   implemented. The turns between those two calls are the parent's decision
 *   phase — reading the scout's findings and writing the work order — and
 *   they are counted separately, because that is where the parent's money
 *   goes once it stops reading the code itself.
 */
function segment(msgs: Msg[], prices: Record<string, Price>): Analysis {
  const stages: Record<string, Stage> = {}
  const subStages: Record<string, Stage> = {}
  const parentTurns = msgs.filter(
    (m) => m.role === "assistant" && m.extra?.openheron_agent === "main",
  )
  // ★ Every sub-agent's own turns, keyed by the agent's id — "scout" and
  //   "task" are separate sub-agents and they are priced separately.
  const subTurns = msgs.filter(
    (m) =>
      m.role === "assistant" &&
      m.extra?.openheron_agent &&
      m.extra?.openheron_agent !== "main",
  )

  // The parent's `task` calls, in order, with the sub-agent each one names.
  const taskCalls: { sub: string; argsChars: number }[] = []
  parentTurns.forEach((m) => {
    for (const tc of m.tool_calls ?? []) {
      if (tc.function.name !== "task") continue
      taskCalls.push({
        sub: subAgentOf(tc),
        argsChars: (tc.function.arguments ?? "").length,
      })
    }
  })

  // The implement call is the last `task` call that is not a scout call.
  const implementIdx = (() => {
    for (let i = taskCalls.length - 1; i >= 0; i--) {
      if (taskCalls[i].sub !== "scout") return i
    }
    return taskCalls.length - 1
  })()
  const hasTask = taskCalls.length > 0

  // Which parent turn carries each `task` call.
  const parentTurnOfCall: number[] = []
  parentTurns.forEach((m, i) => {
    for (const tc of m.tool_calls ?? []) {
      if (tc.function.name === "task") parentTurnOfCall.push(i)
    }
  })

  const handoffTurn = hasTask ? parentTurnOfCall[implementIdx] : -1
  const firstCallTurn = hasTask ? parentTurnOfCall[0] : -1

  let total = 0
  let reportChars = 0
  const reportCharsBySub: Record<string, number> = {}
  // ★ The tool result carries the id of the call it answers, so a scout
  //   report and an implementation report do not get added together.
  const callIdBySub = new Map<string, string>()
  parentTurns.forEach((m) => {
    for (const tc of m.tool_calls ?? []) {
      if (tc.function.name === "task" && tc.id) {
        callIdBySub.set(tc.id, subAgentOf(tc))
      }
    }
  })
  for (const m of msgs) {
    if (m.role !== "tool" || m.extra?.openheron_agent !== "main") continue
    if (m.tool_name !== "task") continue
    const len = (m.content ?? "").length
    reportChars += len
    const sub = callIdBySub.get(m.tool_call_id ?? "") ?? "unknown"
    reportCharsBySub[sub] = (reportCharsBySub[sub] ?? 0) + len
  }

  if (handoffTurn >= 0) {
    parentTurns.forEach((m, i) => {
      let key: string
      if (i < firstCallTurn) key = "analyze"
      else if (i === handoffTurn) key = "handoff"
      else if (i > handoffTurn) key = "accept"
      else key = "decide" // between the scout call and the implementation call
      addStage(stages, key, costOf(m.response_meta?.usage, bareModel(m.extra?.openheron_model), prices))
    })
    for (const m of subTurns) {
      const sub = String(m.extra?.openheron_agent)
      const c = costOf(m.response_meta?.usage, bareModel(m.extra?.openheron_model), prices)
      // ★ The per-sub-agent breakdown is kept **beside** the stages, not
      //   inside them: "child" and "child:scout" are the same money, and
      //   putting both in the stage map would double the total.
      addStage(subStages, `child:${sub}`, c)
      addStage(stages, "child", c)
    }
  } else {
    // A solo parent: the landing starts at the first write/edit/execute call.
    const landIdx = parentTurns.findIndex((m) =>
      (m.tool_calls ?? []).some((tc) =>
        ["write_file", "edit_file", "execute"].includes(tc.function.name),
      ),
    )
    const cut = landIdx < 0 ? parentTurns.length : landIdx
    parentTurns.forEach((m, i) => {
      addStage(stages, i < cut ? "analyze" : "land", costOf(m.response_meta?.usage, bareModel(m.extra?.openheron_model), prices))
    })
  }

  // ★ How many times the parent went back to the files after the report came
  //   back. This is the direct measure of the thing arm A′ is testing: a
  //   parent that trusts a citation does not re-read the file.
  let acceptRereads = 0
  if (handoffTurn >= 0) {
    for (const m of parentTurns.slice(handoffTurn + 1)) {
      for (const tc of m.tool_calls ?? []) {
        if (["read_file", "grep", "glob", "ls"].includes(tc.function.name)) acceptRereads++
      }
    }
  }

  for (const s of Object.values(stages)) total += s.cost
  return {
    stages,
    subStages,
    delegated: handoffTurn >= 0,
    reportChars,
    acceptRereads,
    total,
    taskCalls,
    reportCharsBySub,
  }
}

// ── the run ────────────────────────────────────────────────────────────────

type Row = {
  arm: string
  task: string
  kind: string
  repeat: number
  ok: boolean
  failedChecks: string[]
  cost: number
  analysis: Analysis
  matched: boolean
  error?: string
  // ★ A run that never finished — the sweep's timeout cut it off. It is
  //   **not** a wrong answer and it is **not** a free run: its cost was
  //   never finalized, so counting it as ¥0 would flatter whichever arm it
  //   happened to land on.
  incomplete: boolean
}

const modelOf = new Map<string, string>()
for (const arm of armsDoc.arms) {
  for (const a of arm.agents) modelOf.set(`${arm.id}:${a.id}`, bareModel(a.model))
}

const rows: Row[] = []
for (const run of report.runs) {
  const row: Row = {
    arm: run.arm,
    task: run.task,
    kind: run.kind,
    repeat: run.repeat,
    ok: run.ok,
    failedChecks: run.failedChecks ?? [],
    cost: run.cost,
    analysis: {
      stages: {},
      delegated: false,
      reportChars: 0,
      acceptRereads: 0,
      total: 0,
      taskCalls: [],
      reportCharsBySub: {},
    },
    matched: false,
    error: run.error,
    // ★ Whether the run's cost is complete comes from the report, not
    //   from a second guess here: the report is where the runner recorded
    //   that a failed run's spend cannot be seen from the transcript. Two
    //   derivations of the same fact would eventually disagree.
    incomplete: run.costIncomplete ?? !!run.error,
  }
  if (run.threadId) {
    const msgs = await readSession(run.threadId)
    if (msgs) {
      row.analysis = segment(msgs, report.prices)
      row.matched = true
    }
  }
  rows.push(row)
}

// ── the checks ─────────────────────────────────────────────────────────────

const unmatched = rows.filter((r) => !r.matched)
let identityFailures = 0
let sumFailures = 0

for (const r of rows) {
  if (!r.matched) continue
  // ★ An unfinished run is a different case: its recorded cost is zero
  //   because the sweep killed it before the turn was archived, while the
  //   transcript still holds the part it did spend. Comparing the two is
  //   not a failure — it is the price of the run that was cut off, and it
  //   is reported so the arm's total is not read as free.
  if (r.incomplete) {
    if (Math.abs(r.analysis.total - r.cost) > 1e-4) {
      console.error(
        `  note: ${r.arm}/${r.task} #${r.repeat} was cut off after ¥${r.analysis.total.toFixed(4)} ` +
          `of spend that the recorded cost (¥${r.cost.toFixed(4)}) does not carry`,
      )
    }
    continue
  }
  // check 1: the staged sum equals the recorded cost
  if (Math.abs(r.analysis.total - r.cost) > 1e-4) {
    sumFailures++
    console.error(
      `✗ ${r.arm}/${r.task}: staged ¥${r.analysis.total.toFixed(6)} != recorded ¥${r.cost.toFixed(6)}`,
    )
  }
}

// ── the report ─────────────────────────────────────────────────────────────

const arms = [...new Set(rows.map((r) => r.arm))]
const baseline = report.baseline && arms.includes(report.baseline) ? report.baseline : arms[0]

const g = (r: Row, k: string): number => r.analysis.stages[k]?.cost ?? 0
const gt = (r: Row, k: string): number => r.analysis.stages[k]?.turns ?? 0

console.log("═".repeat(100))
console.log("per-run breakdown  (¥, from the transcript's own token accounting)")
console.log("═".repeat(100))
console.log(
  "arm".padEnd(34) +
    "task".padEnd(28) +
    "deleg".padEnd(7) +
    "taskcalls".padStart(10) +
    "analyze".padStart(9) +
    "handoff".padStart(9) +
    "child".padStart(9) +
    "accept".padStart(9) +
    "land".padStart(9) +
    "total".padStart(9) +
    "re-read".padStart(9) +
    "report".padStart(8),
)
for (const r of rows) {
  if (!r.matched) continue
  console.log(
    r.arm.padEnd(34) +
      r.task.padEnd(28) +
      (r.analysis.delegated ? "yes" : "no").padEnd(7) +
      r.analysis.taskCalls
        .map((c) => c.sub || "?")
        .join(",")
        .padStart(10) +
      g(r, "analyze").toFixed(4).padStart(9) +
      g(r, "handoff").toFixed(4).padStart(9) +
      g(r, "child").toFixed(4).padStart(9) +
      g(r, "accept").toFixed(4).padStart(9) +
      g(r, "land").toFixed(4).padStart(9) +
      r.analysis.total.toFixed(4).padStart(9) +
      String(r.analysis.acceptRereads).padStart(9) +
      String(r.analysis.reportChars).padStart(8) +
      (r.incomplete ? "  ★ INCOMPLETE: " + (r.error ?? "") : "") +
      (Object.keys(r.analysis.reportCharsBySub).length > 1
        ? `  [reports: ${Object.entries(r.analysis.reportCharsBySub)
            .map(([k, v]) => `${k}=${v}`)
            .join(" ")}]`
        : ""),
  )
}

// ── the medians ────────────────────────────────────────────────────────────

/**
 * median returns the middle value of a list.
 *
 * ★ The median, not the mean: one run that wanders for thirty rounds moves
 *   the mean of three runs by a third and says nothing about the typical
 *   run. One repetition is noise; the median of three is the number that
 *   survives it.
 */
function median(values: number[]): number {
  if (!values.length) return 0
  const s = [...values].sort((a, b) => a - b)
  const mid = s.length >> 1
  return s.length % 2 ? s[mid] : (s[mid - 1] + s[mid]) / 2
}

console.log()
console.log("═".repeat(100))
console.log("per-task medians  (a run that never finished is excluded, and reported)")
console.log("═".repeat(100))
console.log(
  "task".padEnd(30) +
    "arm".padEnd(34) +
    "n".padStart(3) +
    "median¥".padStart(11) +
    "min¥".padStart(10) +
    "max¥".padStart(10) +
    "spread".padStart(9) +
    "passed".padStart(8),
)
const medians = new Map<string, Map<string, { med: number; n: number; passed: number; incomplete: number }>>()
for (const arm of arms) {
  const perTask = new Map<string, { med: number; n: number; passed: number; incomplete: number }>()
  const tasks = [...new Set(rows.filter((r) => r.arm === arm).map((r) => r.task))]
  for (const task of tasks) {
    const list = rows.filter((r) => r.arm === arm && r.task === task)
    const done = list.filter((r) => !r.incomplete)
    const costs = done.map((r) => r.cost)
    const entry = {
      med: median(costs),
      n: done.length,
      passed: list.filter((r) => r.ok).length,
      incomplete: list.length - done.length,
    }
    perTask.set(task, entry)
    console.log(
      task.padEnd(30) +
        arm.padEnd(34) +
        String(entry.n).padStart(3) +
        entry.med.toFixed(4).padStart(11) +
        (costs.length ? Math.min(...costs).toFixed(4) : "-").padStart(10) +
        (costs.length ? Math.max(...costs).toFixed(4) : "-").padStart(10) +
        (costs.length > 1 ? (Math.max(...costs) / Math.min(...costs)).toFixed(2) + "x" : "-").padStart(9) +
        `${entry.passed}/${list.length}`.padStart(8) +
        (entry.incomplete ? `  ★ ${entry.incomplete} incomplete` : ""),
    )
  }
  medians.set(arm, perTask)
}

console.log()
console.log("═".repeat(100))
console.log("arm medians compared to the baseline")
console.log("═".repeat(100))
const baseMed = medians.get(baseline)
for (const [arm, perTask] of medians) {
  if (arm === baseline) continue
  let sum = 0
  let baseSum = 0
  let compared = 0
  const detail: string[] = []
  for (const [task, e] of perTask) {
    const b = baseMed?.get(task)
    if (!b) continue
    sum += e.med
    baseSum += b.med
    compared++
    detail.push(`${task}: ${e.med.toFixed(4)} vs ${b.med.toFixed(4)} = ${(e.med / b.med).toFixed(3)}`)
  }
  if (!compared) continue
  console.log(
    `${arm} vs ${baseline}:  median-of-medians ¥${sum.toFixed(4)} vs ¥${baseSum.toFixed(4)}  ` +
      `ratio ${(sum / baseSum).toFixed(3)}`,
  )
  for (const d of detail) console.log(`    ${d}`)
}

console.log()
console.log("═".repeat(100))
console.log("per-arm totals")
console.log("═".repeat(100))
const armAgg = new Map<
  string,
  {
    cost: number
    passed: number
    runs: number
    incomplete: number
    stages: Record<string, number>
    subStages: Record<string, number>
    delegated: number
    rereads: number
    reportChars: number
    over: number
    // ★ The shape of the parent's `task` usage: how many calls, and which
    //   sub-agent each one named. Arm A″ must show scout *and* task.
    callsBySub: Record<string, number>
    reportBySub: Record<string, number>
  }
>()
for (const r of rows) {
  const a = armAgg.get(r.arm) ?? {
    cost: 0, passed: 0, runs: 0, incomplete: 0, stages: {}, subStages: {}, delegated: 0, rereads: 0, reportChars: 0, over: 0,
    callsBySub: {}, reportBySub: {},
  }
  a.cost += r.cost
  a.runs++
  if (r.incomplete) a.incomplete++
  if (r.ok) a.passed++
  if (r.analysis.delegated) {
    a.delegated++
    a.rereads += r.analysis.acceptRereads
    a.reportChars += r.analysis.reportChars
    if (r.analysis.reportChars > reportLimit) a.over++
    for (const c of r.analysis.taskCalls) {
      const key = c.sub || "?"
      a.callsBySub[key] = (a.callsBySub[key] ?? 0) + 1
    }
    for (const [k, v] of Object.entries(r.analysis.reportCharsBySub)) {
      a.reportBySub[k] = (a.reportBySub[k] ?? 0) + v
    }
  }
  for (const [k, s] of Object.entries(r.analysis.stages)) a.stages[k] = (a.stages[k] ?? 0) + s.cost
  for (const [k, s] of Object.entries(r.analysis.subStages)) a.subStages[k] = (a.subStages[k] ?? 0) + s.cost
  armAgg.set(r.arm, a)
}

const base = armAgg.get(baseline)
for (const [arm, a] of armAgg) {
  const ratio = base && base.cost ? a.cost / base.cost : 1
  console.log(
    `${arm.padEnd(34)} ¥${a.cost.toFixed(4).padStart(9)}  pass ${a.passed}/${a.runs}  ` +
      `delegated ${a.delegated}  ratio ${ratio.toFixed(3)}${arm === baseline ? "  (baseline)" : ""}` +
      (a.incomplete
        ? `  ★ ${a.incomplete} run(s) never finished: their cost is a lower bound, not the whole`
        : ""),
  )
  const s = a.stages
  const ss = a.subStages
  console.log(
    `${" ".repeat(34)}   analyze ¥${(s.analyze ?? 0).toFixed(4)}  handoff ¥${(s.handoff ?? 0).toFixed(4)}  ` +
      `child ¥${(s.child ?? 0).toFixed(4)}  accept ¥${(s.accept ?? 0).toFixed(4)}  land ¥${(s.land ?? 0).toFixed(4)}` +
      `  decide ¥${(s.decide ?? 0).toFixed(4)}  scout ¥${(ss["child:scout"] ?? 0).toFixed(4)}`,
  )
  if (Object.keys(a.callsBySub).length) {
    const calls = Object.entries(a.callsBySub)
      .map(([k, v]) => `${k}×${v}`)
      .join(" ")
    const reps = Object.entries(a.reportBySub)
      .map(([k, v]) => `${k}=${v}字`)
      .join(" ")
    console.log(`${" ".repeat(34)}   task calls: ${calls}   reports: ${reps}`)
  }
  if (a.delegated) {
    console.log(
      `${" ".repeat(34)}   accept re-reads ${a.rereads} over ${a.delegated} delegated runs  ` +
        `report chars ${a.reportChars}${a.over ? `  ★ ${a.over} run(s) over ${reportLimit} chars` : ""}`,
    )
  }
}

// ── the decomposition identity ─────────────────────────────────────────────

console.log()
console.log("═".repeat(100))
console.log("decomposition check  (land saved) - (coordination) - (extra analysis) == B - A")
console.log("═".repeat(100))
for (const arm of arms) {
  if (arm === baseline) continue
  // ★ Pair the runs by task **and** repeat index: the same task, the same
  //   repetition, one against the other. A run that never finished has no
  //   cost to pair, so it drops out here — and the medians above report it.
  const aRows = rows.filter((r) => r.arm === arm && r.matched && !r.incomplete)
  const bRows = rows.filter((r) => r.arm === baseline && r.matched && !r.incomplete)
  if (!aRows.length || !bRows.length) continue
  const bKey = new Map(bRows.map((r) => [`${r.task}#${r.repeat}`, r]))
  let aTot = 0, bTot = 0, landSaved = 0, coord = 0, extraAnalysis = 0
  let pairs = 0
  for (const r of aRows) {
    const b = bKey.get(`${r.task}#${r.repeat}`)
    if (!b) continue
    pairs++
    aTot += r.analysis.total
    bTot += b.analysis.total
    if (r.analysis.delegated) {
      const bLand = g(b, "land")
      const childCost = g(r, "child")
      landSaved += bLand - childCost
      // ★ The coordination tax is everything the parent does between the
      //   work being scoped and the work being checked: the work order it
      //   writes, the findings it reads to write it (the "decide" stage),
      //   and the acceptance pass.
      coord += g(r, "handoff") + g(r, "decide") + g(r, "accept")
      extraAnalysis += g(r, "analyze") - g(b, "analyze")
    }
  }
  if (!pairs) continue
  // ★ The net saving is what delegation buys: B - A. It is the *opposite*
  //   sign of A - B, and getting that backwards makes a saving look like a
  //   loss.
  const netSaving = landSaved - coord - extraAnalysis
  const bMinusA = bTot - aTot
  const ok = Math.abs(netSaving - bMinusA) < 1e-6
  if (!ok) identityFailures++
  console.log(
    `${arm} vs ${baseline}  (${pairs} paired runs):  land saved ¥${landSaved.toFixed(4)}  ` +
      `-  coordination ¥${coord.toFixed(4)}  -  extra analysis ¥${extraAnalysis.toFixed(4)}  ` +
      `=  net saving ¥${netSaving.toFixed(4)}`,
  )
  console.log(
    `${" ".repeat(arm.length + 4)}  B-A = ¥${bMinusA.toFixed(4)}   A/B = ${(aTot / bTot).toFixed(3)}   ` +
      `${ok ? "✓ identity holds" : "✗ IDENTITY BROKEN"}`,
  )
}

console.log()
if (unmatched.length) {
  console.log(`⚠ ${unmatched.length} run(s) had no transcript to analyze:`)
  for (const r of unmatched) console.log(`   ${r.arm}/${r.task} #${r.repeat} (${r.error ?? "no threadId"})`)
}
if (sumFailures) console.log(`✗ ${sumFailures} run(s): the staged sum does not match the recorded cost`)
else console.log(`✓ every matched run: the staged sum equals the recorded cost`)
if (identityFailures) console.log(`✗ ${identityFailures} decomposition identity check(s) failed`)
else console.log(`✓ every decomposition identity holds`)

process.exit(sumFailures + identityFailures > 0 ? 1 : 0)

// ── the plumbing ───────────────────────────────────────────────────────────

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
