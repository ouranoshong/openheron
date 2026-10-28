
import { requireTestBackend, testBackendBase } from "./test-env"

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

async function makeInterruptedThread(attempts: number): Promise<{ id: string; text: string }> {
  for (let i = 1; i <= attempts; i++) {
    const { threadId } = await j(await fetch(`${BASE}/v2/sessions`, { method: "POST" }))
    const res = await fetch(`${BASE}/v2/run`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        threadId,
        runId: "r1",
        messages: [
          {
            id: "u1",
            role: "user",
            content: "用 write_file 工具在 /tmp 写 hitl-probe.txt，内容 x。不要用别的工具。",
          },
        ],
      }),
    })
    const text = await res.text()
    if (text.includes('"type":"interrupt"')) return { id: threadId, text }
    console.log(`  ↻ attempt ${i}: the model did not call write_file, retrying`)
    await fetch(`${BASE}/v2/sessions/${threadId}`, { method: "DELETE" })
  }
  throw new Error(`model never triggered an approval after ${attempts} attempts`)
}

const { id: threadId, text } = await makeInterruptedThread(4)
console.log(`threadId=${threadId}`)

const events = eventsOf(text)
const fin = events.find((e) => e.type === "RUN_FINISHED")
ok(fin?.outcome?.type === "interrupt", "the run ended with an interrupt", fin?.outcome?.type)
const interruptId = fin?.outcome?.interrupts?.[0]?.id
ok(!!interruptId, "got the interruptId", interruptId)
ok(
  fin?.outcome?.interrupts?.[0]?.message === "tool write_file requires approval",
  "the interrupt message is readable (not a raw Go struct dump)",
  fin?.outcome?.interrupts?.[0]?.message,
)

const entry = { interruptId, status: "resolved", payload: { approved: true } }
const resumeText = await (
  await fetch(`${BASE}/v2/run`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ threadId, runId: "r2", resume: [entry], messages: [] }),
  })
).text()
const resumed = eventsOf(resumeText)

const results = resumed.filter((e) => e.type === "TOOL_CALL_RESULT")
ok(results.length > 0, "the tool really ran after the resume", `${results.length} results`)
ok(
  resumed.some((e) => e.type === "RUN_FINISHED" && e.outcome?.type === "success"),
  "the resumed run ended with success",
  resumed.find((e) => e.type === "RUN_FINISHED")?.outcome?.type,
)
ok(
  results.some((r) => String(r.content).includes("hitl-probe.txt")),
  "the file-write result came back",
  JSON.stringify(results[0]?.content ?? null).slice(0, 60),
)

const after = await j(await fetch(`${BASE}/v2/sessions/${threadId}`))
ok(after.state === "idle", "the state is back to idle after the approval", after.state)
ok(!after.pendingApproval, "pendingApproval was cleared")

await fetch(`${BASE}/v2/sessions/${threadId}`, { method: "DELETE" })
console.log(bad ? "\n✗ HITL wiring failed" : "\n✓ HITL wiring passed")
process.exit(bad ? 1 : 0)
