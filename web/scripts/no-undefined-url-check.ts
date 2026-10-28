/**
 * 不发出 URL 里带 `undefined` 的请求。
 *
 * ★ 这类 bug 的隐蔽之处：类型上写成 `string`，调用方用 `!` 断言就能把
 *   undefined 无声送进来，模板字符串再把它拼成字面量：
 *       GET /v2/sessions/undefined/messages
 *   必然 404，且报错里看不出成因。断言只能挡编译期，所以这里验的是
 *   运行期：三处防线各自是否真的拦住了。
 *
 * 触发场景：页面刚打开、或点了 New Thread —— 此时 threadId 就是 undefined，
 * 而 assistant-ui 仍会调 history.load()。
 */

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

/**
 * ★ 这一版只保留 api 层的守卫检查。
 *   原来还有三段针对 history / attachments adapter 的 —— 那两个 adapter
 *   随 assistant-ui 一起删了，界面直接走 store，不再有「无会话时也要
 *   调 history.load」那条路径。
 */

// ── 3) api 层兜底：id 为空在拼 URL 之前就抛 ──
// ★ 需要 await：uploadFiles 是 async 函数，空 id 走的是 rejected promise
//   而不是同步 throw。不 await 就抓不到，还会留下一个 unhandled rejection。
for (const [label, run] of [
  ["getMessages", () => api.getMessages("" as string)],
  ["getThread", () => api.getThread("" as string)],
  ["uploadFiles", () => api.uploadFiles("" as string, [])],
] as const) {
  calls = []
  let msg = ""
  try { await run() } catch (e) { msg = (e as Error).message }
  ok(msg.includes("session id is missing"), `${label} 空 id 直接抛`, JSON.stringify(msg))
  ok(calls.length === 0, `${label} 没发出请求`, JSON.stringify(calls))
}

// ── 4) 有会话时一切照旧：URL 正确 ──
//
// ★ 键名是 threadId，不是 id —— 上一版这里解构成了 undefined，结果是
//   **测试自己**发出了 DELETE /v2/sessions/undefined，正是要防的那种畸形 URL。
//
// 这一段直接用 api（adapter 已随 assistant-ui 删除），验的是
// sessionPath 守卫不会误伤正常路径。
const { threadId } = await (await fetch("/v2/sessions", { method: "POST" })).json()
ok(typeof threadId === "string" && threadId.length > 0, "拿到 threadId", String(threadId).slice(0, 8))
calls = []
await api.getMessages(threadId)
ok(calls.length === 1 && !calls[0].includes("undefined"), "有会话时正常请求", calls[0])
await fetch(`/v2/sessions/${threadId}`, { method: "DELETE" })

console.log(bad ? "\n✗ 仍有畸形 URL" : "\n✓ 不会发出畸形 URL")
process.exit(bad ? 1 : 0)
