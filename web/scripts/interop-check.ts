
import { readSse, takeEvents } from "../src/lib/sse"
import { reduceEvent, type AgEvent } from "../src/lib/events"
import { emptyConversation } from "../src/lib/types"
import { requireTestBackend, testBackendBase } from "./test-env"

const BASE = testBackendBase()
await requireTestBackend()

let failed = false
const check = (ok: boolean, label: string, extra = "") => {
  console.log(`${ok ? "✓" : "✗"} ${label}${extra ? ` — ${extra}` : ""}`)
  if (!ok) failed = true
}

// ★ /v2/run does not create sessions: the thread comes from POST /v2/sessions
//   and must be carried on every run. Posting /v2/run without a threadId is
//   answered with 400 "threadId is required; create the session with
//   POST /v2/sessions first".
const { threadId } = (await (
  await fetch(`${BASE}/v2/sessions`, { method: "POST", headers: { "Content-Type": "application/json" } })
).json()) as { threadId: string }

const rawRes = await fetch(`${BASE}/v2/run`, {
  method: "POST",
  headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
  body: JSON.stringify({ threadId, query: "只回复两个字：好的" }),
})
if (!rawRes.ok) throw new Error(`run: ${rawRes.status} ${await rawRes.text()}`)
const rawText = await rawRes.text()
const rawLines = rawText.split("\n").filter((l) => l.startsWith("data:"))
check(rawLines.length > 0, "the SSE stream has frames", `${rawLines.length} frames`)

const noSpace = rawLines.filter((l) => !l.startsWith("data: ")).length
console.log(
  noSpace > 0
    ? `  ↳ ${noSpace}/${rawLines.length} frames are data:{...} (no space after the colon; the parser must tolerate it)`
    : "  ↳ all frames are data: {...} (standard framing)",
)

const firstEvent = JSON.parse(rawLines[0]!.slice(5)) as AgEvent & { threadId?: string }
check(firstEvent.type === "RUN_STARTED", "the first event is RUN_STARTED", String(firstEvent.type))
check(
  !!firstEvent.threadId && firstEvent.threadId !== "undefined",
  "RUN_STARTED carried a real threadId",
  firstEvent.threadId ?? "(empty)",
)
check(
  firstEvent.threadId === threadId,
  "RUN_STARTED echoed the threadId we asked for",
  `${firstEvent.threadId} vs ${threadId}`,
)

const runRes = await fetch(`${BASE}/v2/run`, {
  method: "POST",
  headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
  body: JSON.stringify({ threadId, query: "只回复两个字：好的" }),
})
if (!runRes.ok || !runRes.body) throw new Error("run failed")

const events: AgEvent[] = []
for await (const ev of readSse(runRes.body)) {
  // ★ A keepalive frame carries no data. It is not an event, and
  //   parsing it as one fails the whole check on a slow model.
  if (!ev.data) continue
  events.push(JSON.parse(ev.data) as AgEvent)
}

const types = [...new Set(events.map((e) => e.type))]
check(events.length > 0, "src/lib/sse.ts received events", `${events.length} events`)
check(types.includes("TEXT_MESSAGE_CONTENT"), "received TEXT_MESSAGE_CONTENT")
check(types.includes("RUN_FINISHED"), "received RUN_FINISHED")

let convo = emptyConversation()
for (const e of events) convo = reduceEvent(convo, e)
check(convo.status === "idle", "the reducer's final state is idle", convo.status)

const text = convo.messages
  .flatMap((m) => m.parts)
  .filter((p) => p.kind === "text")
  .map((p) => (p.kind === "text" ? p.text : ""))
  .join("")
check(text.length > 0, "the reducer assembled the body text", JSON.stringify(text))
console.log(`  ↳ model reply: ${JSON.stringify(text)}`)

const allClosed = convo.messages.every((m) =>
  m.parts.every((p) => p.kind === "tool" || p.complete),
)
check(allClosed, "no part is left unfinished")

// The history is fetched after both runs, so its last assistant message is the
// one the second run streamed — that is what has to match.
const hist = (await (await fetch(`${BASE}/v2/sessions/${threadId}/messages`)).json()) as {
  messages: { role: string; content: string }[]
}
const assistant = hist.messages.filter((m) => m.role === "assistant").pop()
check(assistant?.content === text, "the streamed text matches the hydrated history", JSON.stringify(assistant?.content))

const whole = rawText.endsWith("\n\n") ? rawText : rawText + "\n\n"
let buffer = ""
let chunked = 0
for (let i = 0; i < whole.length; i++) {
  const { events: got, rest } = takeEvents(buffer + whole[i]!)
  buffer = rest
  chunked += got.length
}
check(chunked === rawLines.length, "feeding the stream byte by byte loses no events", `${chunked}/${rawLines.length}`)

console.log(failed ? "\n✗ interop verification failed" : "\n✓ interop verification passed")
process.exit(failed ? 1 : 0)
