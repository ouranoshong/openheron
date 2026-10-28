import { describe, expect, test } from "bun:test"

import { reduceEvent, reduceEvents, type AgEvent } from "../src/lib/events"
import { recordedRun } from "../src/lib/fixtures/recorded-run"
import { emptyConversation, type ConversationState, type Message, type ToolPart } from "../src/lib/types"

const evs = recordedRun

const RECORDED_TYPES = [
  "RUN_STARTED",
  "RUN_FINISHED",
  "TEXT_MESSAGE_START",
  "TEXT_MESSAGE_CONTENT",
  "TEXT_MESSAGE_END",
  "REASONING_START",
  "REASONING_MESSAGE_START",
  "REASONING_MESSAGE_CONTENT",
  "REASONING_MESSAGE_END",
  "REASONING_END",
  "TOOL_CALL_START",
  "TOOL_CALL_ARGS",
  "TOOL_CALL_END",
  "TOOL_CALL_RESULT",
] as const

test("fixture 覆盖了后端实际发出的每一种事件", () => {
  const seen = new Set(evs.map((e) => e.type))
  const missing = RECORDED_TYPES.filter((t) => !seen.has(t))
  expect(missing).toEqual([])
})

function messages(s: ConversationState): Message[] {
  return s.messages
}
function part(s: ConversationState, msgId: string, index: number) {
  return s.messages.find((m) => m.id === msgId)?.parts[index]
}
function toolParts(s: ConversationState): ToolPart[] {
  return s.messages.flatMap((m) => m.parts.filter((p): p is ToolPart => p.kind === "tool"))
}

describe("真后端录制的一轮", () => {
  const s = reduceEvents(emptyConversation(), evs)

  test("事件覆盖了后端实际发出的全部类型", () => {
    expect(new Set(evs.map((e) => e.type)).size).toBeGreaterThanOrEqual(13)
  })

  test("终态是 idle 且没有遗留中断", () => {
    expect(s.status).toBe("idle")
    expect(s.interrupts).toEqual([])
    expect(s.error).toBeUndefined()
  })

  test("没有 part 停在未完结状态", () => {
    for (const m of s.messages) {
      for (const p of m.parts) {
        if (p.kind === "text" || p.kind === "reasoning") {
          expect(`${m.id}/${p.kind}=${p.complete}`).toBe(`${m.id}/${p.kind}=true`)
        }
      }
    }
  })

  test("推理与正文是同一消息上的两段，不会互相覆盖", () => {
    const withBoth = s.messages.find(
      (m) =>
        m.parts.some((p) => p.kind === "reasoning") && m.parts.some((p) => p.kind === "text"),
    )
    expect(withBoth, "there should be one message carrying both reasoning and body text").toBeDefined()

    const reasoning = withBoth!.parts.find((p) => p.kind === "reasoning")!
    const text = withBoth!.parts.find((p) => p.kind === "text")!
    expect(reasoning.text.length).toBeGreaterThan(0)
    expect(text.text.length).toBeGreaterThan(0)
    expect(reasoning.text).not.toBe(text.text)
  })

  test("工具调用三段齐全且拿到了结果", () => {
    const tools = toolParts(s)
    expect(tools.length).toBeGreaterThanOrEqual(1)
    for (const t of tools) {
      expect(t.name.length).toBeGreaterThan(0)
      expect(t.phase).toBe("complete")
      expect(typeof t.result).toBe("string")
      expect(t.result!.length).toBeGreaterThan(0)
    }
  })

  test("TEXT_MESSAGE_START 的 name 落在消息上", () => {
    const one = reduceEvents(emptyConversation(), [
      { type: "TEXT_MESSAGE_START", messageId: "m1", role: "assistant", name: "coder" },
      { type: "TEXT_MESSAGE_CONTENT", messageId: "m1", delta: "hi" },
      { type: "TEXT_MESSAGE_END", messageId: "m1" },
    ])
    expect(one.messages[0]!.agent).toBe("coder")
  })

  test("没有 name 的消息不编造归属", () => {
    const one = reduceEvents(emptyConversation(), [
      { type: "TEXT_MESSAGE_START", messageId: "m1", role: "assistant" },
      { type: "TEXT_MESSAGE_CONTENT", messageId: "m1", delta: "hi" },
      { type: "TEXT_MESSAGE_END", messageId: "m1" },
    ])
    expect(one.messages[0]!.agent).toBeUndefined()
  })

  test("并行的两个 agent 各自的消息不串", () => {
    const one = reduceEvents(emptyConversation(), [
      { type: "TEXT_MESSAGE_START", messageId: "mA", role: "assistant", name: "coder" },
      { type: "TEXT_MESSAGE_START", messageId: "mB", role: "assistant", name: "reviewer" },
      { type: "TEXT_MESSAGE_CONTENT", messageId: "mA", delta: "A1" },
      { type: "TEXT_MESSAGE_CONTENT", messageId: "mB", delta: "B1" },
      { type: "TEXT_MESSAGE_CONTENT", messageId: "mA", delta: "A2" },
      { type: "TEXT_MESSAGE_END", messageId: "mA" },
      { type: "TEXT_MESSAGE_END", messageId: "mB" },
    ])
    const a = one.messages.find((m) => m.id === "mA")!
    const b = one.messages.find((m) => m.id === "mB")!
    expect(a.agent).toBe("coder")
    expect(b.agent).toBe("reviewer")
    const text = (m: Message) => m.parts.filter((p) => p.kind === "text").map((p) => (p.kind === "text" ? p.text : "")).join("")
    expect(text(a)).toBe("A1A2")
    expect(text(b)).toBe("B1")
  })

  test("工具参数是原始 JSON 字符串而非解析后的对象", () => {
    const withArgs = toolParts(s).find((t) => t.argsText.length > 0)
    expect(withArgs).toBeDefined()
    expect(() => JSON.parse(withArgs!.argsText)).not.toThrow()
  })

  test("同一条消息里推理排在正文之前", () => {
    const msg = s.messages.find(
      (m) =>
        m.parts.some((p) => p.kind === "reasoning") && m.parts.some((p) => p.kind === "text"),
    )!
    const kinds = msg.parts.map((p) => p.kind)
    expect(kinds.indexOf("reasoning")).toBeLessThan(kinds.indexOf("text"))
  })
})

describe("RUN_STARTED", () => {
  test("清掉上一轮的审批挂起", () => {
    const before: ConversationState = {
      ...emptyConversation(),
      status: "awaiting-approval",
      interrupts: [{ id: "i1", reason: "tool_call" }],
    }
    const after = reduceEvent(before, { type: "RUN_STARTED", threadId: "t", runId: "r" } as AgEvent)
    expect(after.interrupts).toEqual([])
    expect(after.status).toBe("running")
  })

  test("清掉上一轮的错误", () => {
    const before: ConversationState = {
      ...emptyConversation(),
      status: "error",
      error: { code: "x", message: "boom" },
    }
    const after = reduceEvent(before, { type: "RUN_STARTED" } as AgEvent)
    expect(after.error).toBeUndefined()
  })
})

describe("RUN_FINISHED · interrupt", () => {
  const finished = (interrupts: unknown[]) =>
    ({
      type: "RUN_FINISHED",
      threadId: "t",
      runId: "r",
      outcome: { type: "interrupt", interrupts },
    }) as AgEvent

  test("转入 awaiting-approval 并带上中断", () => {
    const s = reduceEvent(emptyConversation(), finished([
      { id: "int-1", reason: "tool_call", message: "write_file requires approval", toolCallId: "call-1" },
    ]))
    expect(s.status).toBe("awaiting-approval")
    expect(s.interrupts).toHaveLength(1)
    expect(s.interrupts[0]!.id).toBe("int-1")
    expect(s.interrupts[0]!.toolCallId).toBe("call-1")
  })

  test("subagentRunId 归一成 requestedByAgent", () => {
    const s = reduceEvent(emptyConversation(), finished([
      { id: "int-1", reason: "tool_call", toolCallId: "call-1", subagentRunId: "worker" },
    ]))
    expect(s.interrupts[0]!.requestedByAgent).toBe("worker")
  })

  test("根 agent 的中断不带请求方", () => {
    const s = reduceEvent(emptyConversation(), finished([
      { id: "int-2", reason: "tool_call", toolCallId: "call-2" },
    ]))
    expect(s.interrupts[0]!.requestedByAgent).toBeUndefined()
  })

  test("metadata 里的工具名与参数归一成审批内容", () => {
    const s = reduceEvent(emptyConversation(), finished([
      {
        id: "int-1",
        reason: "tool_call",
        message: "tool write_file requires approval",
        toolCallId: "call-1",
        metadata: {
          toolName: "write_file",
          arguments: { file_path: "/tmp/x.txt" },
        },
      },
    ]))
    expect(s.interrupts[0]!.toolName).toBe("write_file")
    expect(s.interrupts[0]!.arguments).toEqual({ file_path: "/tmp/x.txt" })
  })

  test("没有 interrupts 数组时不崩，且不进审批态", () => {
    const s = reduceEvent(emptyConversation(), finished([]))
    expect(s.status).not.toBe("awaiting-approval")
  })

  test("收尾时关掉所有开着的 part", () => {
    const opened = reduceEvents(emptyConversation(), [
      { type: "TEXT_MESSAGE_START", messageId: "m1", role: "assistant" } as AgEvent,
      { type: "TEXT_MESSAGE_CONTENT", messageId: "m1", delta: "半句" } as AgEvent,
    ])
    const closed = reduceEvent(opened, finished([{ id: "i", reason: "r" }]))
    const p = part(closed, "m1", 0)
    expect(p?.kind).toBe("text")
    expect(p?.kind === "text" && p.complete).toBe(true)
  })
})

describe("RUN_ERROR", () => {
  test("记录错误并清掉待审批", () => {
    const before: ConversationState = {
      ...emptyConversation(),
      status: "awaiting-approval",
      interrupts: [{ id: "i1", reason: "tool_call" }],
    }
    const s = reduceEvent(before, {
      type: "RUN_ERROR",
      code: "upstream_error",
      message: "provider unavailable",
    } as AgEvent)
    expect(s.status).toBe("error")
    expect(s.error).toEqual({ code: "upstream_error", message: "provider unavailable" })
    expect(s.interrupts).toEqual([])
  })
})

describe("reasoning", () => {
  test("REASONING_MESSAGE_START 的 role 是 'reasoning'，消息角色仍是 assistant", () => {
    const s = reduceEvent(emptyConversation(), {
      type: "REASONING_MESSAGE_START",
      messageId: "m1",
      role: "reasoning",
    } as unknown as AgEvent)
    expect(messages(s)[0]!.role).toBe("assistant")
  })

  test("推理开着的段可以累加增量", () => {
    const s = reduceEvents(emptyConversation(), [
      { type: "REASONING_START", messageId: "m1" } as AgEvent,
      { type: "REASONING_MESSAGE_START", messageId: "m1", role: "reasoning" } as unknown as AgEvent,
      { type: "REASONING_MESSAGE_CONTENT", messageId: "m1", delta: "a" } as AgEvent,
      { type: "REASONING_MESSAGE_CONTENT", messageId: "m1", delta: "b" } as AgEvent,
      { type: "REASONING_MESSAGE_END", messageId: "m1" } as AgEvent,
    ])
    const r = messages(s)[0]!.parts[0]!
    expect(r.kind).toBe("reasoning")
    expect(r.kind === "reasoning" && r.text).toBe("ab")
  })

  test("思维链记下开始与结束时刻（服务端时间戳）", () => {
    const s = reduceEvents(emptyConversation(), [
      { type: "REASONING_START", messageId: "m1", timestamp: 1000 } as AgEvent,
      { type: "REASONING_MESSAGE_CONTENT", messageId: "m1", delta: "a", timestamp: 1000 } as AgEvent,
      { type: "REASONING_MESSAGE_END", messageId: "m1", timestamp: 2500 } as AgEvent,
    ])
    const r = messages(s)[0]!.parts[0]!
    expect(r.kind).toBe("reasoning")
    if (r.kind !== "reasoning") throw new Error("expected a reasoning part")
    expect(r.startedAt).toBe(1000)
    expect(r.elapsedMs).toBe(1500)
  })

  test("没有时间戳就没有用时（不是 0）", () => {
    const s = reduceEvents(emptyConversation(), [
      { type: "REASONING_START", messageId: "m1" } as AgEvent,
      { type: "REASONING_MESSAGE_CONTENT", messageId: "m1", delta: "a" } as AgEvent,
      { type: "REASONING_MESSAGE_END", messageId: "m1" } as AgEvent,
    ])
    const r = messages(s)[0]!.parts[0]!
    if (r.kind !== "reasoning") throw new Error("expected a reasoning part")
    expect(r.startedAt).toBeUndefined()
    expect(r.elapsedMs).toBeUndefined()
  })

  test("只有 REASONING_END 没有 MESSAGE_END 时也能闭合", () => {
    const s = reduceEvents(emptyConversation(), [
      { type: "REASONING_MESSAGE_CONTENT", messageId: "m1", delta: "x" } as AgEvent,
      { type: "REASONING_END", messageId: "m1" } as AgEvent,
    ])
    const p = messages(s)[0]!.parts[0]!
    expect(p.kind).toBe("reasoning")
    expect(p.kind === "reasoning" && p.complete).toBe(true)
  })
})

describe("工具调用", () => {
  test("三段式：START → ARGS → END → RESULT", () => {
    const s = reduceEvents(emptyConversation(), [
      { type: "TOOL_CALL_START", toolCallId: "c1", toolCallName: "read_file", parentMessageId: "m1" } as AgEvent,
      { type: "TOOL_CALL_ARGS", toolCallId: "c1", delta: '{"file_' } as AgEvent,
      { type: "TOOL_CALL_ARGS", toolCallId: "c1", delta: 'path":"a.go"}' } as AgEvent,
      { type: "TOOL_CALL_END", toolCallId: "c1" } as AgEvent,
      { type: "TOOL_CALL_RESULT", toolCallId: "c1", content: "package main", role: "tool" } as AgEvent,
    ])
    const t = toolParts(s)[0]!
    expect(t.name).toBe("read_file")
    expect(t.argsText).toBe('{"file_path":"a.go"}')
    expect(t.result).toBe("package main")
    expect(t.phase).toBe("complete")
  })

  test("END 之后仍是 running，拿到 RESULT 才 complete", () => {
    const s = reduceEvents(emptyConversation(), [
      { type: "TOOL_CALL_START", toolCallId: "c1", toolCallName: "ls", parentMessageId: "m1" } as AgEvent,
      { type: "TOOL_CALL_END", toolCallId: "c1" } as AgEvent,
    ])
    expect(toolParts(s)[0]!.phase).toBe("running")
  })

  test("RESULT 早于 START 到达时不丢 —— 就地补一条", () => {
    const s = reduceEvents(emptyConversation(), [
      { type: "TOOL_CALL_RESULT", toolCallId: "c9", content: "done", role: "tool" } as AgEvent,
    ])
    const t = toolParts(s)[0]
    expect(t?.id).toBe("c9")
    expect(t?.result).toBe("done")
  })

  test("RESULT 按 toolCallId 跨消息回填", () => {
    const s = reduceEvents(emptyConversation(), [
      { type: "TOOL_CALL_START", toolCallId: "c1", toolCallName: "ls", parentMessageId: "mA" } as AgEvent,
      { type: "TEXT_MESSAGE_START", messageId: "mB", role: "assistant" } as AgEvent,
      { type: "TOOL_CALL_RESULT", toolCallId: "c1", content: "ok", role: "tool" } as AgEvent,
    ])
    expect(toolParts(s)[0]!.result).toBe("ok")
    expect(toolParts(s)).toHaveLength(1)
  })
})

describe("流式健壮性", () => {
  test("CONTEXT 早于 START 时就地建消息", () => {
    const s = reduceEvent(emptyConversation(), {
      type: "TEXT_MESSAGE_CONTENT",
      messageId: "mX",
      delta: "hi",
    } as AgEvent)
    expect(messages(s)).toHaveLength(1)
    expect(messages(s)[0]!.parts[0]!.kind).toBe("text")
  })

  test("未知事件类型原样返回", () => {
    const before = emptyConversation()
    const after = reduceEvent(before, { type: "SOMETHING_NEW" } as unknown as AgEvent)
    expect(after).toBe(before)
  })

  test("重复 END 不会重复闭合或新建 part", () => {
    const s = reduceEvents(emptyConversation(), [
      { type: "TEXT_MESSAGE_START", messageId: "m1", role: "assistant" } as AgEvent,
      { type: "TEXT_MESSAGE_CONTENT", messageId: "m1", delta: "a" } as AgEvent,
      { type: "TEXT_MESSAGE_END", messageId: "m1" } as AgEvent,
      { type: "TEXT_MESSAGE_END", messageId: "m1" } as AgEvent,
    ])
    expect(messages(s)[0]!.parts).toHaveLength(1)
  })

  test("不改入参", () => {
    const before = emptyConversation()
    const snapshot = JSON.stringify(before)
    reduceEvents(before, evs.slice(0, 12))
    expect(JSON.stringify(before)).toBe(snapshot)
  })

  test("每个 delta 都返回新对象（引用要变）", () => {
    let s = emptyConversation()
    s = reduceEvent(s, { type: "TEXT_MESSAGE_START", messageId: "m1", role: "assistant" } as AgEvent)
    const a = s
    s = reduceEvent(s, { type: "TEXT_MESSAGE_CONTENT", messageId: "m1", delta: "x" } as AgEvent)
    expect(s).not.toBe(a)
  })
})

describe("usage", () => {
  test("AG-UI 的 inputTokens/outputTokens 能归一", () => {
    const s = reduceEvent(emptyConversation(), {
      type: "RUN_FINISHED",
      threadId: "t",
      runId: "r",
      outcome: { type: "success", result: { usage: { inputTokens: 10, outputTokens: 5, totalTokens: 15 } } },
    } as AgEvent)
    expect(s.usage).toMatchObject({ promptTokens: 10, completionTokens: 5, totalTokens: 15 })
  })

  test("openheron 自己的 promptTokens/completionTokens 也能归一", () => {
    const s = reduceEvent(emptyConversation(), {
      type: "RUN_FINISHED",
      threadId: "t",
      runId: "r",
      outcome: { type: "success", result: { usage: { promptTokens: 7, completionTokens: 3 } } },
    } as AgEvent)
    expect(s.usage).toMatchObject({ promptTokens: 7, completionTokens: 3, totalTokens: 10 })
  })
})

describe("CUSTOM · model_retry", () => {
  test("记下重试提示", () => {
    const s = reduceEvent(emptyConversation(), {
      type: "CUSTOM",
      name: "model_retry",
      value: { message: "rate limited, retrying" },
    } as AgEvent)
    expect(s.retry?.message).toBe("rate limited, retrying")
  })

  test("下一轮开始时清掉", () => {
    let s = reduceEvent(emptyConversation(), {
      type: "CUSTOM",
      name: "model_retry",
      value: { message: "x" },
    } as AgEvent)
    s = reduceEvent(s, { type: "RUN_STARTED" } as AgEvent)
    expect(s.retry).toBeUndefined()
  })

  test("未知 CUSTOM 原样返回", () => {
    const before = emptyConversation()
    expect(reduceEvent(before, { type: "CUSTOM", name: "whatever" } as AgEvent)).toBe(before)
  })
})
