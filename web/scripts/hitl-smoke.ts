/**
 * HITL 接线验证：用 assistant-ui 实际发出的 resume 格式打后端。
 *
 * ★ 要验的不是「审批能不能过」（后端已有测试覆盖），而是前端这一层
 *   翻译出来的 JSON 形状对不对 —— status 与 payload.approved 是两件事，
 *   只发其中一半的表现是「点了同意，工具永远不执行，也不报错」。
 *
 * ⚠ 本脚本依赖模型**选择**调用 write_file 来触发审批，模型不调就会失败 ——
 *   那是模型的自由，不是代码回归（实测约每 5 次抖 1 次）。
 *   所以重试若干次；全都不触发时明确报错，不伪装成接线有问题。
 */

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
    console.log(`  ↻ 第 ${i} 次模型没调 write_file，重试`)
    await fetch(`${BASE}/v2/sessions/${threadId}`, { method: "DELETE" })
  }
  throw new Error(`model never triggered an approval after ${attempts} attempts`)
}

// 1) 造一个待审批的会话
const { id: threadId, text } = await makeInterruptedThread(4)
console.log(`threadId=${threadId}`)

const events = eventsOf(text)
const fin = events.find((e) => e.type === "RUN_FINISHED")
ok(fin?.outcome?.type === "interrupt", "轮次以 interrupt 结束", fin?.outcome?.type)
const interruptId = fin?.outcome?.interrupts?.[0]?.id
ok(!!interruptId, "拿到 interruptId", interruptId)
ok(
  fin?.outcome?.interrupts?.[0]?.message === "tool write_file requires approval",
  "中断文案可读（非 Go 结构体 dump）",
  fin?.outcome?.interrupts?.[0]?.message,
)

// 2) 前端卡提交的形状：status 给协议，payload.approved 给 openheron
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
ok(results.length > 0, "续跑后工具真的执行了", `${results.length} 条结果`)
ok(
  resumed.some((e) => e.type === "RUN_FINISHED" && e.outcome?.type === "success"),
  "续跑以 success 结束",
  resumed.find((e) => e.type === "RUN_FINISHED")?.outcome?.type,
)
ok(
  results.some((r) => String(r.content).includes("hitl-probe.txt")),
  "写文件结果回来了",
  JSON.stringify(results[0]?.content ?? null).slice(0, 60),
)

const after = await j(await fetch(`${BASE}/v2/sessions/${threadId}`))
ok(after.state === "idle", "审批走完后回到 idle", after.state)
ok(!after.pendingApproval, "pendingApproval 已清空")

await fetch(`${BASE}/v2/sessions/${threadId}`, { method: "DELETE" })
console.log(bad ? "\n✗ HITL 接线失败" : "\n✓ HITL 接线通过")
process.exit(bad ? 1 : 0)
