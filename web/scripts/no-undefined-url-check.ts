
import { api } from "../src/lib/api"
import { requireTestBackend, testBackendBase } from "./test-env"

const BASE = testBackendBase()
await requireTestBackend()
const realFetch = globalThis.fetch
let calls: string[] = []
globalThis.fetch = ((i: any, init?: any) => {
  const url = typeof i === "string" ? i : String(i?.url ?? "")
  calls.push(`${init?.method ?? "GET"} ${url}`)
  return realFetch(typeof i === "string" && i.startsWith("/") ? BASE + i : i, init)
}) as any

let bad = false
const ok = (c: boolean, l: string, e = "") => {
  console.log(`${c ? "✓" : "✗"} ${l}${e ? ` — ${e}` : ""}`)
  if (!c) bad = true
}
const leak = () => calls.filter((c) => /undefined|null|\[object/.test(c))

for (const [label, run] of [
  ["getMessages", () => api.getMessages("" as string)],
  ["getThread", () => api.getThread("" as string)],
  ["uploadFiles", () => api.uploadFiles("" as string, [])],
] as const) {
  calls = []
  let msg = ""
  try { await run() } catch (e) { msg = (e as Error).message }
  ok(msg.includes("session id is missing"), `${label} throws on an empty id`, JSON.stringify(msg))
  ok(calls.length === 0, `${label} issued no request`, JSON.stringify(calls))
}

const { threadId } = await (await fetch("/v2/sessions", { method: "POST" })).json()
ok(typeof threadId === "string" && threadId.length > 0, "got the threadId", String(threadId).slice(0, 8))
calls = []
await api.getMessages(threadId)
ok(calls.length === 1 && !calls[0].includes("undefined"), "a request with a session id is well-formed", calls[0])
await fetch(`/v2/sessions/${threadId}`, { method: "DELETE" })

console.log(bad ? "\n✗ malformed URLs remain" : "\n✓ no malformed URLs are sent")
process.exit(bad ? 1 : 0)
