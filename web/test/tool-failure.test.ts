import { describe, expect, test } from "bun:test"

import { reduceEvent, reduceEvents, type AgEvent } from "../src/lib/events"
import { seedFromHistory } from "../src/lib/store/history"
import { emptyConversation, type ConversationState, type ToolPart } from "../src/lib/types"
import type { AgMessage } from "../src/lib/api"

function toolParts(s: ConversationState): ToolPart[] {
  return s.messages.flatMap((m) => m.parts.filter((p): p is ToolPart => p.kind === "tool"))
}

// ★ The failure is a frame of its own: the tool result frame carries the
//   content and nothing else, and the content says what the **model** read —
//   not what happened to the call.
describe("CUSTOM · tool_failed", () => {
  const events: AgEvent[] = [
    { type: "TOOL_CALL_START", toolCallId: "c1", toolCallName: "read_file", parentMessageId: "m1" },
    { type: "TOOL_CALL_ARGS", toolCallId: "c1", delta: "{}" },
    { type: "TOOL_CALL_END", toolCallId: "c1" },
    {
      type: "TOOL_CALL_RESULT",
      toolCallId: "c1",
      content: "[tool error] file not found",
      role: "tool",
    },
    {
      type: "CUSTOM",
      name: "tool_failed",
      value: { tool_call_id: "c1", tool_name: "read_file", error: "file not found" },
    },
  ] as AgEvent[]

  test("失败的那次调用被打上标记", () => {
    const s = reduceEvents(emptyConversation(), events)
    expect(toolParts(s)).toHaveLength(1)
    expect(toolParts(s)[0]!.isError).toBe(true)
  })

  test("内容照旧留着 —— 模型读到的东西不能被抹掉", () => {
    const s = reduceEvents(emptyConversation(), events)
    expect(toolParts(s)[0]!.result).toBe("[tool error] file not found")
  })

  test("标记落在自己的那次调用上，别人的失败不算", () => {
    const s = reduceEvents(emptyConversation(), [
      { type: "TOOL_CALL_START", toolCallId: "c1", toolCallName: "read_file", parentMessageId: "m1" },
      { type: "TOOL_CALL_RESULT", toolCallId: "c1", content: "ok", role: "tool" },
      { type: "TOOL_CALL_START", toolCallId: "c2", toolCallName: "grep", parentMessageId: "m1" },
      { type: "TOOL_CALL_RESULT", toolCallId: "c2", content: "[tool error] boom", role: "tool" },
      {
        type: "CUSTOM",
        name: "tool_failed",
        value: { tool_call_id: "c2", error: "boom" },
      },
    ] as AgEvent[])
    const parts = toolParts(s)
    expect(parts).toHaveLength(2)
    expect(parts.find((p) => p.id === "c1")!.isError).toBeUndefined()
    expect(parts.find((p) => p.id === "c2")!.isError).toBe(true)
  })

  test("判词不会凭空造出一张卡", () => {
    const s = reduceEvents(emptyConversation(), [
      {
        type: "CUSTOM",
        name: "tool_failed",
        value: { tool_call_id: "c9", error: "a call nobody saw" },
      },
    ] as AgEvent[])
    expect(toolParts(s)).toHaveLength(0)
  })

  test("没有 call id 的帧不改任何东西", () => {
    const before = reduceEvents(emptyConversation(), events)
    const after = reduceEvent(before, {
      type: "CUSTOM",
      name: "tool_failed",
      value: { error: "nobody knows which call" },
    } as AgEvent)
    expect(after).toBe(before)
  })

  test("成功的调用没有标记", () => {
    const s = reduceEvents(emptyConversation(), events.slice(0, 4))
    expect(toolParts(s)[0]!.isError).toBeUndefined()
  })
})

// ★ The history has to show the same failure the live stream showed: a
//   reload that lost it would show a failed call as a successful one.
describe("历史里的失败", () => {
  const history: AgMessage[] = [
    {
      id: "m1",
      role: "assistant",
      content: "",
      toolCalls: [{ id: "c1", type: "function", function: { name: "read_file", arguments: "{}" } }],
    },
    {
      id: "t1",
      role: "tool",
      content: "[tool error] file not found",
      toolCallId: "c1",
      metadata: { toolError: "file not found" },
    },
  ]

  test("水合出来的工具卡带着失败", () => {
    const c = seedFromHistory(history)
    const part = c.messages[0]!.parts[0]!
    expect(part.kind === "tool" && part.isError).toBe(true)
    expect(part.kind === "tool" && part.result).toBe("[tool error] file not found")
  })

  test("没有失败标记的结果照旧是成功的", () => {
    const c = seedFromHistory([
      history[0]!,
      { id: "t1", role: "tool", content: "package main", toolCallId: "c1" },
    ])
    const part = c.messages[0]!.parts[0]!
    expect(part.kind === "tool" && part.isError).toBeUndefined()
  })

  test("两条路说的是同一件事", () => {
    const replayed = reduceEvents(emptyConversation(), [
      { type: "TOOL_CALL_START", toolCallId: "c1", toolCallName: "read_file", parentMessageId: "m1" },
      { type: "TOOL_CALL_RESULT", toolCallId: "c1", content: "[tool error] file not found", role: "tool" },
      {
        type: "CUSTOM",
        name: "tool_failed",
        value: { tool_call_id: "c1", error: "file not found" },
      },
    ] as AgEvent[])
    const live = toolParts(replayed)[0]!
    const past = seedFromHistory(history).messages[0]!.parts[0]!
    expect(live.isError).toBe(past.kind === "tool" ? past.isError : undefined)
  })
})
