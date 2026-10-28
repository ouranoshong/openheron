import { describe, expect, test } from "bun:test"

import { reduceEvents, type AgEvent } from "../src/lib/events"
import { emptyConversation, type Message, type Part } from "../src/lib/types"
import { seedFromHistory } from "../src/lib/store/history"
import type { AgMessage } from "../src/lib/api"

/**
 * 事件回放与历史水合**必须得到同一份对话**。
 *
 * ★ 两条路通向同一个界面：一条是实时流（reducer 逐事件归约），
 *   一条是刷新后的历史（seedFromHistory）。若两者不等价，
 *   用户的观感就是「刷新一下，界面变了」—— 而那几乎无法排查，
 *   因为两条路各自都测得通。
 *
 * ★ 已知且**刻意**的差异只有一个：历史里的工具卡带 fromHistory
 *   （默认折叠）。本测试把那条差异**扣掉**再比 —— 其余必须逐字段相同。
 */

/**
 * 扣掉两处**已知的内部差异**，让两条路的产物可比。
 *
 * ★ fromHistory：历史里的工具卡默认折叠（实时 reducer 不设这个标记）——
 *   这是**刻意**的 UI 差异，两条路的界面本就该长得不一样。
 *
 * ★ startedAt：只有流路径有（它是归约器内部算 elapsedMs 用的中间量）。
 *   历史没有服务端时间戳可用 —— 用时由后端**直接**放进 metadata.reasoningMs。
 *   组件不渲染 startedAt（见 message-parts.tsx 的注释），所以它不可见。
 */
function normalize(messages: Message[]): unknown[] {
  return messages.map((m) => ({
    id: m.id,
    role: m.role,
    agent: m.agent,
    parts: m.parts.map((p: Part) => {
      const { fromHistory: _h, startedAt: _s, ...rest } = p as Part & {
        fromHistory?: boolean
        startedAt?: number
      }
      return rest
    }),
  }))
}

describe("事件回放 与 历史水合 的等价性", () => {
  test("纯文本一轮：两条路得到同一份消息", () => {
    const events: AgEvent[] = [
      { type: "RUN_STARTED", threadId: "t1", runId: "r1" },
      { type: "TEXT_MESSAGE_START", messageId: "m1", role: "assistant", name: "main" },
      { type: "TEXT_MESSAGE_CONTENT", messageId: "m1", delta: "答完了" },
      { type: "TEXT_MESSAGE_END", messageId: "m1" },
      { type: "RUN_FINISHED", threadId: "t1", runId: "r1" },
    ]
    const history: AgMessage[] = [
      { id: "m1", role: "assistant", content: "答完了", name: "main" },
    ]

    const replayed = normalize(reduceEvents(emptyConversation(), events).messages)
    const hydrated = normalize(seedFromHistory(history).messages)
    expect(replayed).toEqual(hydrated)
  })

  test("思维链 + 正文：顺序与用时都一致", () => {
    const events: AgEvent[] = [
      { type: "REASONING_START", messageId: "m1", role: "assistant", timestamp: 1000 },
      { type: "REASONING_MESSAGE_CONTENT", messageId: "m1", delta: "先看看这个仓库", timestamp: 1000 },
      { type: "REASONING_END", messageId: "m1", timestamp: 3000 },
      { type: "TEXT_MESSAGE_START", messageId: "m1", role: "assistant", name: "main" },
      { type: "TEXT_MESSAGE_CONTENT", messageId: "m1", delta: "答完了" },
      { type: "TEXT_MESSAGE_END", messageId: "m1" },
    ]
    const history: AgMessage[] = [
      {
        id: "m1",
        role: "assistant",
        content: "答完了",
        name: "main",
        metadata: { reasoning: "先看看这个仓库", reasoningMs: 2000 },
      },
    ]

    const replayed = normalize(reduceEvents(emptyConversation(), events).messages)
    const hydrated = normalize(seedFromHistory(history).messages)
    expect(replayed).toEqual(hydrated)
  })

  test("工具调用 + 工具结果：并进同一次调用", () => {
    const events: AgEvent[] = [
      { type: "TOOL_CALL_START", toolCallId: "call_1", toolCallName: "ls", parentMessageId: "m2" },
      { type: "TOOL_CALL_ARGS", toolCallId: "call_1", delta: '{"path":"."}' },
      { type: "TOOL_CALL_END", toolCallId: "call_1" },
      { type: "TOOL_CALL_RESULT", toolCallId: "call_1", content: "a b c" },
    ]
    const history: AgMessage[] = [
      {
        id: "m2",
        role: "assistant",
        content: "",
        toolCalls: [{ id: "call_1", type: "function", function: { name: "ls", arguments: '{"path":"."}' } }],
      },
      { id: "m3", role: "tool", content: "a b c", toolCallId: "call_1" },
    ]

    const replayed = normalize(reduceEvents(emptyConversation(), events).messages)
    const hydrated = normalize(seedFromHistory(history).messages)
    expect(replayed).toEqual(hydrated)
  })

  test("用户消息：两条路都成一条 user 消息", () => {
    const events: AgEvent[] = [
      { type: "TEXT_MESSAGE_START", messageId: "u1", role: "user" },
      { type: "TEXT_MESSAGE_CONTENT", messageId: "u1", delta: "hi" },
      { type: "TEXT_MESSAGE_END", messageId: "u1" },
    ]
    const history: AgMessage[] = [{ id: "u1", role: "user", content: "hi" }]

    const replayed = normalize(reduceEvents(emptyConversation(), events).messages)
    const hydrated = normalize(seedFromHistory(history).messages)
    expect(replayed).toEqual(hydrated)
  })
})

describe("两条路的已知差异（钉住，别忘）", () => {
  test("历史里的工具卡带 fromHistory（默认折叠），实时的不带", () => {
    const events: AgEvent[] = [
      { type: "TOOL_CALL_START", toolCallId: "call_1", toolCallName: "ls", parentMessageId: "m2" },
      { type: "TOOL_CALL_ARGS", toolCallId: "call_1", delta: "{}" },
      { type: "TOOL_CALL_END", toolCallId: "call_1" },
      { type: "TOOL_CALL_RESULT", toolCallId: "call_1", content: "ok" },
    ]
    const history: AgMessage[] = [
      {
        id: "m2",
        role: "assistant",
        content: "",
        toolCalls: [{ id: "call_1", type: "function", function: { name: "ls", arguments: "{}" } }],
      },
      { id: "m3", role: "tool", content: "ok", toolCallId: "call_1" },
    ]

    const replayed = reduceEvents(emptyConversation(), events).messages[0]!.parts[0]!
    const hydrated = seedFromHistory(history).messages[0]!.parts[0]!
    expect((replayed as { fromHistory?: boolean }).fromHistory).toBeUndefined()
    expect((hydrated as { fromHistory?: boolean }).fromHistory).toBe(true)
  })

  test("历史里没有 startedAt —— 用时由后端直接给", () => {
    const history: AgMessage[] = [
      {
        id: "m1",
        role: "assistant",
        content: "答完了",
        metadata: { reasoning: "先想", reasoningMs: 2000 },
      },
    ]
    const part = seedFromHistory(history).messages[0]!.parts[0]!
    expect((part as { startedAt?: number }).startedAt).toBeUndefined()
    // ★ 用时在 —— 那才是用户看得见的那个数。
    expect((part as { elapsedMs?: number }).elapsedMs).toBe(2000)
  })
})
