/**
 * 互操作验证：用**前端自己的分帧器**去读 Go 端产出的 SSE。
 *
 * ★ 存在的理由不是"测试一下"，而是这里有一个具体的、会静默炸掉的协议缝隙：
 *
 *   openheron 的 SSE 分帧来自 hertz 的 sse.Publisher，写出的是 `data:{...}` ——
 *   冒号后没有空格。SSE 规范允许（字段值只剥掉一个前导空格），绝大多数
 *   手写解析器也兼容，但 AG-UI 是跨语言契约：Go 端写、JS 端读。任何一环
 *   按 `data: ` 严格匹配，表现都是**流安静地变空** —— 不报错、不抛异常，
 *   只是永远收不到消息。
 *
 *   那正是本脚本最早抓到的问题（脚本自己先写成了 `data: `）。
 *
 * ★ 为什么用 src/lib/sse.ts 而不是 AG-UI 的官方客户端：
 *   官方客户端已经被移除了（前端不再依赖它）。而且用**自己那个**解析器
 *   才有意义 —— 验的是生产路径，不是某个第三方库恰好也兼容。
 *
 *   反过来，E2E 用的是 test/fake-server.ts 那个假后端；本脚本打的是
 *   **真后端**。两者互补：假后端保证确定性，真后端保证协议没漂。
 *
 * 用法：先起 `scripts/test-backend.sh --reset`（测试实例，:18099），再
 *   bun run scripts/interop-check.ts
 */

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

// 1) 原始字节：先确认后端写出来的到底是什么形态。
//
//    这一步不能省：解析器可能悄悄容忍某种分帧，而我们需要的恰恰是
//    「知道后端写的是什么」，而不是「解析器碰巧能读」。
const rawRes = await fetch(`${BASE}/v2/run`, {
  method: "POST",
  headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
  body: JSON.stringify({ query: "只回复两个字：好的" }),
})
if (!rawRes.ok) throw new Error(`run: ${rawRes.status} ${await rawRes.text()}`)
const rawText = await rawRes.text()
const rawLines = rawText.split("\n").filter((l) => l.startsWith("data:"))
check(rawLines.length > 0, "SSE 有帧", `${rawLines.length} 帧`)

const noSpace = rawLines.filter((l) => !l.startsWith("data: ")).length
console.log(
  noSpace > 0
    ? `  ↳ 其中 ${noSpace}/${rawLines.length} 帧为 data:{...}（无空格分帧，解析器必须容忍）`
    : "  ↳ 全部为 data: {...}（标准分帧）",
)

// RUN_STARTED 必须带回服务端分配的 threadId —— 新协议的核心。
const firstEvent = JSON.parse(rawLines[0]!.slice(5)) as AgEvent & { threadId?: string }
check(firstEvent.type === "RUN_STARTED", "首事件是 RUN_STARTED", String(firstEvent.type))
check(
  !!firstEvent.threadId && firstEvent.threadId !== "undefined",
  "RUN_STARTED 回传了真实 threadId",
  firstEvent.threadId ?? "(空)",
)

// 2) 用生产分帧器重跑一次，逐事件喂 reducer。
const runRes = await fetch(`${BASE}/v2/run`, {
  method: "POST",
  headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
  body: JSON.stringify({ query: "只回复两个字：好的" }),
})
if (!runRes.ok || !runRes.body) throw new Error("run failed")

const events: AgEvent[] = []
for await (const ev of readSse(runRes.body)) {
  events.push(JSON.parse(ev.data) as AgEvent)
}

const types = [...new Set(events.map((e) => e.type))]
check(events.length > 0, "src/lib/sse.ts 收到事件", `${events.length} 个`)
check(types.includes("TEXT_MESSAGE_CONTENT"), "收到 TEXT_MESSAGE_CONTENT")
check(types.includes("RUN_FINISHED"), "收到 RUN_FINISHED")

// 3) reducer 能把整条流吃下去并落到 idle。
let convo = emptyConversation()
for (const e of events) convo = reduceEvent(convo, e)
check(convo.status === "idle", "reducer 终态是 idle", convo.status)

const text = convo.messages
  .flatMap((m) => m.parts)
  .filter((p) => p.kind === "text")
  .map((p) => (p.kind === "text" ? p.text : ""))
  .join("")
check(text.length > 0, "reducer 拼出了正文", JSON.stringify(text))
console.log(`  ↳ 模型回复: ${JSON.stringify(text)}`)

const allClosed = convo.messages.every((m) =>
  m.parts.every((p) => p.kind === "tool" || p.complete),
)
check(allClosed, "没有 part 停在未完结状态")

// 4) 历史水合后必须与流式一致 —— 否则界面在刷新后会变样。
const threadId = firstEvent.threadId!
const hist = (await (await fetch(`${BASE}/v2/sessions/${threadId}/messages`)).json()) as {
  messages: { role: string; content: string }[]
}
const assistant = hist.messages.filter((m) => m.role === "assistant").pop()
check(assistant?.content === text, "流式拼接结果 == 历史水合内容", JSON.stringify(assistant?.content))

// 5) 分帧器的边角：后端的真实字节逐字节喂进去不能丢事件。
const whole = rawText.endsWith("\n\n") ? rawText : rawText + "\n\n"
let buffer = ""
let chunked = 0
for (let i = 0; i < whole.length; i++) {
  const { events: got, rest } = takeEvents(buffer + whole[i]!)
  buffer = rest
  chunked += got.length
}
check(chunked === rawLines.length, "逐字节喂入不丢事件", `${chunked}/${rawLines.length}`)

console.log(failed ? "\n✗ 互操作验证失败" : "\n✓ 互操作验证通过")
process.exit(failed ? 1 : 0)
