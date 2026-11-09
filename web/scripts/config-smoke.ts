import { requireTestBackend, testBackendBase } from "./test-env"

// A config_write turn, end to end, against a real model.
//
// ★ What this covers that no Go test can: the tool is in the **ask** tier, so
//   the framework raises an interrupt, the interrupt reaches the client as an
//   approval, and the approved resume is what actually writes the file. The Go
//   tests prove Service.Write refuses what it should; only this proves the
//   wiring between "the model calls the tool" and "a human said yes".
//
// ★ It fails loudly when its own prerequisite is missing. `config_write` is
//   invisible unless the agent that runs the turn names it in its own `tools:`
//   list — the same shape as smoke:hitl, except that one is documented and this
//   one says it in the output, because a smoke that reports "flaky" when it is
//   really unconfigured teaches people to ignore it.

const BASE = testBackendBase()
await requireTestBackend()

const j = (r: Response) => r.json() as Promise<any>
const eventsOf = (text: string) =>
  text
    .split("\n")
    .filter((l) => l.startsWith("data:"))
    .flatMap((l) => {
      try {
        return [JSON.parse(l.slice(5).replace(/^: /, ""))]
      } catch {
        return []
      }
    })

let bad = false
const ok = (c: boolean, l: string, e = "") => {
  console.log(`${c ? "✓" : "✗"} ${l}${e ? ` — ${e}` : ""}`)
  if (!c) bad = true
}

const PROBE = "agents/smoke-probe.md"

// ── the prerequisite ─────────────────────────────────────────────────────

type Verdict = { name: string; tier: string; reason?: string }

async function configWriteVerdict(agent: string): Promise<Verdict | undefined> {
  const detail = await j(await fetch(`${BASE}/v2/agents/${agent}`))
  const tools: Verdict[] = detail.effectiveTools ?? []
  return tools.find((t) => t.name === "config_write")
}

// ★ Which agent runs the turn is a choice, not an assumption. The factory
//   tree puts the configuration tools on the **execution** agents; `main` is
//   the dispatcher and does not name `config_write`. Asking `main` for it is
//   asking for a tool it cannot see — the round ends in a refusal, and the
//   smoke reports "not configured" about an agent that was never the one
//   that could have called it.
async function agentWithConfigWrite(): Promise<string | undefined> {
  const detail = await j(await fetch(`${BASE}/v2/agents`))
  const agents: { id: string; tools?: string[] }[] = detail.agents ?? []
  return agents.find((a) => (a.tools ?? []).includes("config_write"))?.id
}

const agent = await agentWithConfigWrite()
const verdict = agent ? await configWriteVerdict(agent) : undefined

if (!verdict) {
  console.error(
    [
      "✗ no agent names config_write in its tool table.",
      "",
      "  This smoke needs an agent that may call it, and the tier must be ask:",
      "",
      "      .openheron/agents/<agent>.md",
      "      tools: [..., config_write]",
      "",
      "  The tool is deliberately absent from the dispatcher: handing an",
      "  agent the pen that edits its own configuration is a decision, not a",
      "  default. See docs/user-manual.md §4.9.",
      "  After editing, run: task test-env-sync",
    ].join("\n"),
  )
  process.exit(2)
}
console.log(`using agent ${agent} (its tool table names config_write)`)
if (verdict.reason) {
  console.error(
    `✗ config_write is blocked for agent ${agent} by its own tools: list (reason: ${verdict.reason}).`,
  )
  process.exit(2)
}
ok(verdict.tier === "ask", "config_write is in the ask tier", verdict.tier)
if (verdict.tier !== "ask") {
  // Nothing else can be asserted: an allow-tier config_write would have run
  // without an approval, which is the one thing this smoke exists to catch.
  console.error("✗ config_write must always ask; see agentkit/build/tools.go's alwaysAsk")
  process.exit(1)
}

// ── the probe file ───────────────────────────────────────────────────────

const manifest = await j(await fetch(`${BASE}/v2/settings`))
const ref: string | undefined = manifest.modelRefs?.[0]
if (!ref) {
  console.error("✗ the test instance declares no models, so no agent file can be written")
  process.exit(2)
}

const probe = [
  "---",
  "description: a file this smoke wrote through config_write",
  `model: ${ref}`,
  "tools: [ls]",
  "---",
  "",
  "smoke-probe",
  "",
].join("\n")

const exists = async (): Promise<boolean> => {
  const item = await j(await fetch(`${BASE}/v2/settings/item?path=${encodeURIComponent(PROBE)}`))
  return !!item.exists
}

const cleanup = async () => {
  if (await exists()) {
    await fetch(`${BASE}/v2/settings/item?path=${encodeURIComponent(PROBE)}`, { method: "DELETE" })
  }
}

// A leftover from a previous run would make "the write did not happen yet"
// unfalsifiable.
await cleanup()
ok(!(await exists()), "the probe file does not exist before the run")

// ── the turn ─────────────────────────────────────────────────────────────

async function runWithApproval(attempts: number): Promise<{ id: string; text: string }> {
  for (let i = 1; i <= attempts; i++) {
    const { threadId } = await j(await fetch(`${BASE}/v2/sessions`, { method: "POST" }))

    // The agent belongs to the session, not to the run: /v2/run has no agent
    // field, and the server takes the session's active agent.
    await fetch(`${BASE}/v2/sessions/${threadId}/agent`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agentId: agent }),
    })

    const prompt = [
      `Call the config_write tool exactly once with path "${PROBE}" and this content,`,
      "verbatim, and then stop:",
      "",
      "----- BEGIN CONTENT -----",
      probe,
      "----- END CONTENT -----",
      "",
      "Do not call any other tool. Do not change the content.",
    ].join("\n")

    const res = await fetch(`${BASE}/v2/run`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        threadId,
        runId: "r1",
        messages: [{ id: "u1", role: "user", content: prompt }],
      }),
    })
    const text = await res.text()
    if (text.includes('"type":"interrupt"')) return { id: threadId, text }
    console.log(`  ↻ attempt ${i}: the model did not call config_write, retrying`)
    await fetch(`${BASE}/v2/sessions/${threadId}`, { method: "DELETE" })
  }
  throw new Error(`the model never called config_write after ${attempts} attempts`)
}

const { id: threadId, text } = await runWithApproval(4)
console.log(`threadId=${threadId}`)

const events = eventsOf(text)
const fin = events.find((e) => e.type === "RUN_FINISHED")
ok(fin?.outcome?.type === "interrupt", "the run ended with an interrupt", fin?.outcome?.type)

const interrupt = fin?.outcome?.interrupts?.[0]
ok(!!interrupt?.id, "got the interruptId", interrupt?.id)
ok(
  typeof interrupt?.message === "string" && interrupt.message.length > 0,
  "the interrupt message is readable",
  interrupt?.message,
)

// ★ The whole point of the ask tier: nothing has been written yet.
ok(!(await exists()), "the file was NOT written before the approval")

// ── the approval ─────────────────────────────────────────────────────────

const resumeText = await (
  await fetch(`${BASE}/v2/run`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      threadId,
      runId: "r2",
      resume: [{ interruptId: interrupt?.id, status: "resolved", payload: { approved: true } }],
      messages: [],
    }),
  })
).text()
const resumed = eventsOf(resumeText)

ok(
  resumed.some((e) => e.type === "RUN_FINISHED" && e.outcome?.type === "success"),
  "the resumed run ended with success",
  resumed.find((e) => e.type === "RUN_FINISHED")?.outcome?.type,
)

const results = resumed.filter((e) => e.type === "TOOL_CALL_RESULT")
ok(results.length > 0, "the tool really ran after the resume", `${results.length} results`)
ok(
  results.some((r) => String(r.content).includes("smoke-probe.md")),
  "config_write reported the path it wrote",
  JSON.stringify(results[0]?.content ?? null).slice(0, 80),
)

// ★ And the file is on disk, through the server's own read path — not through
//   a filesystem call this script could have made on its own.
const item = await j(await fetch(`${BASE}/v2/settings/item?path=${encodeURIComponent(PROBE)}`))
ok(!!item.exists, "the file exists after the approval")
ok(
  String(item.content ?? "").includes("smoke-probe"),
  "the file holds what the model was told to write",
  String(item.content ?? "").slice(0, 60),
)
ok(
  String(item.revision ?? "") !== "",
  "the write produced a revision", // a revision is what makes the next edit safe
  item.revision,
)

// ── put the tree back ────────────────────────────────────────────────────

await fetch(`${BASE}/v2/settings/item?path=${encodeURIComponent(PROBE)}`, { method: "DELETE" })
ok(!(await exists()), "the probe file is gone again — the test instance is as it was")

await fetch(`${BASE}/v2/sessions/${threadId}`, { method: "DELETE" })

console.log(bad ? "\n✗ config_write wiring failed" : "\n✓ config_write wiring passed")
process.exit(bad ? 1 : 0)
