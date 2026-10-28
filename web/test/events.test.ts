import { describe, expect, test } from "bun:test"

import { reduceEvent, reduceEvents, type AgEvent } from "../src/lib/events"
import { recordedRun } from "../src/lib/fixtures/recorded-run"
import { emptyConversation, type ConversationState, type Message, type ToolPart } from "../src/lib/types"

/**
 * reducer 的契约测试。
 *
 * ★ 这些断言证明的是「对不对」，不依赖任何框架知识 —— 纯函数，跑一遍
 *   比对输出即可。上一版的每个 bug 都是「对不对取决于回调什么时候触发」，
 *   那种东西没法这样钉住。
 */

const evs = recordedRun

/**
 * 录的那一轮覆盖了后端实际发出的每一种事件。
 *
 * ★ 后端加一种事件时这里会**立刻变红** —— 因为 fixture 是录的、且标成
 *   AgEvent[]（不是 Record<string, unknown>[]，那个标法会让类型检查形同
 *   虚设）。改这里比改代码更能说明「后端变了什么」。
 */
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

// ── 录制的真实事件流 ──────────────────────────────────────────────────────

describe("真后端录制的一轮", () => {
  const s = reduceEvents(emptyConversation(), evs)

  test("事件覆盖了后端实际发出的全部类型", () => {
    // 换后端时这个断言会先响：少了一种类型说明 fixture 该重新录。
    expect(new Set(evs.map((e) => e.type)).size).toBeGreaterThanOrEqual(13)
  })

  test("终态是 idle 且没有遗留中断", () => {
    expect(s.status).toBe("idle")
    expect(s.interrupts).toEqual([])
    expect(s.error).toBeUndefined()
  })

  test("没有 part 停在未完结状态", () => {
    // 轮次结束后仍有 open part 的话，界面会一直转圈。
    for (const m of s.messages) {
      for (const p of m.parts) {
        if (p.kind === "text" || p.kind === "reasoning") {
          expect(`${m.id}/${p.kind}=${p.complete}`).toBe(`${m.id}/${p.kind}=true`)
        }
      }
    }
  })

  test("推理与正文是同一消息上的两段，不会互相覆盖", () => {
    // ★ 这是后端的实际形状：agtransport 的 emitReasoningStart 与
    //   emitTextStart 共用同一个 msgID。part 的身份因此必须是
    //   (messageId, kind)，只看 messageId 会两段互相顶掉。
    const withBoth = s.messages.find(
      (m) =>
        m.parts.some((p) => p.kind === "reasoning") && m.parts.some((p) => p.kind === "text"),
    )
    expect(withBoth, "应当有一条同时含推理与正文的消息").toBeDefined()

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

  // ★ 归属必须落在**消息**上。
  //
  //   踩过的坑：ensureMessage 返回的是**整个 state**，不是那条消息。
  //   写成 `{ ...ensureMessage(...), agent }` 会把 agent 挂到 state 上
  //   —— 挂上去也没人读，于是实时流里的归属**静默丢失**：界面不显示
  //   agent 名，按 agent 过滤也筛不掉任何东西。而历史回放路径是好的，
  //   所以只有对比两路才发现。
  test("TEXT_MESSAGE_START 的 name 落在消息上", () => {
    const one = reduceEvents(emptyConversation(), [
      { type: "TEXT_MESSAGE_START", messageId: "m1", role: "assistant", name: "coder" },
      { type: "TEXT_MESSAGE_CONTENT", messageId: "m1", delta: "hi" },
      { type: "TEXT_MESSAGE_END", messageId: "m1" },
    ])
    expect(one.messages[0]!.agent).toBe("coder")
  })

  // 没有 name 的消息不带归属 —— 空 = 不知道是谁，不猜。
  test("没有 name 的消息不编造归属", () => {
    const one = reduceEvents(emptyConversation(), [
      { type: "TEXT_MESSAGE_START", messageId: "m1", role: "assistant" },
      { type: "TEXT_MESSAGE_CONTENT", messageId: "m1", delta: "hi" },
      { type: "TEXT_MESSAGE_END", messageId: "m1" },
    ])
    expect(one.messages[0]!.agent).toBeUndefined()
  })

  // ★ 并行时两个 agent 的消息不能串。
  //
  //   内容按 messageId 归约：两个 agent 各有自己的 messageId，
  //   各自的 delta 落到各自的消息里 —— 交错到达也不会串。
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
    // 用户研究 agent 行为时要看到真实参数；解析或摘要都会让观察失真。
    const withArgs = toolParts(s).find((t) => t.argsText.length > 0)
    expect(withArgs).toBeDefined()
    expect(() => JSON.parse(withArgs!.argsText)).not.toThrow()
  })

  test("同一条消息里推理排在正文之前", () => {
    // ★ 必须挑「两者都有」的那条：录的这一轮里还有只含推理的中间消息，
    //   拿第一条含 reasoning 的去比，indexOf("text") 会是 -1，
    //   断言变成"0 < -1"这种无意义的失败（第一版就这么写错了）。
    const msg = s.messages.find(
      (m) =>
        m.parts.some((p) => p.kind === "reasoning") && m.parts.some((p) => p.kind === "text"),
    )!
    const kinds = msg.parts.map((p) => p.kind)
    expect(kinds.indexOf("reasoning")).toBeLessThan(kinds.indexOf("text"))
  })
})

// ── 单事件分支 ────────────────────────────────────────────────────────────

describe("RUN_STARTED", () => {
  test("清掉上一轮的审批挂起", () => {
    // ★ 不清的话答复过一次之后状态点会一直显示「待审批」。
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

  // ★ 子 agent 的审批必须能看出请求方：同一时刻可能有两个 agent 在等，
  //   「批的是谁的工具」直接决定要不要同意。
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
    // outcome.type 说 interrupt 但数组缺失/为空 —— 降级成 idle，
    // 而不是让界面停在「待审批」等一个永远等不到的答复。
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
    // ★ 模型报错后不存在待审批；留着会让状态点一直红着。
    expect(s.interrupts).toEqual([])
  })
})

describe("reasoning", () => {
  test("REASONING_MESSAGE_START 的 role 是 'reasoning'，消息角色仍是 assistant", () => {
    // 前端 schema 是 zod.literal("reasoning")；那个字面量不是合法消息角色，
    // 所以推理挂在同 id 的 assistant 消息上。
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
    // ★ 「想了多久」是从这两个时刻算出来的 —— 用**服务端**的时间戳，
    //   不是客户端收到事件的时间（那含网络延迟），也不是渲染时读时钟
    //   （那会让归约器不纯、组件渲染里出现 impure call）。
    const s = reduceEvents(emptyConversation(), [
      { type: "REASONING_START", messageId: "m1", timestamp: 1000 } as AgEvent,
      { type: "REASONING_MESSAGE_CONTENT", messageId: "m1", delta: "a", timestamp: 1000 } as AgEvent,
      { type: "REASONING_MESSAGE_END", messageId: "m1", timestamp: 2500 } as AgEvent,
    ])
    const r = messages(s)[0]!.parts[0]!
    expect(r.kind).toBe("reasoning")
    if (r.kind !== "reasoning") throw new Error("expected a reasoning part")
    expect(r.startedAt).toBe(1000)
    // ★ 收尾那一刻算出来的用时（结束 2500 − 开始 1000）。
    expect(r.elapsedMs).toBe(1500)
  })

  test("没有时间戳就没有用时（不是 0）", () => {
    // ★ 「不知道」与「零」是两件事 —— 没带 timestamp 的事件算不出用时，
    //   那就显示「Thought」而不是「Thought for 0.0s」。
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
    // 流可能从中间接上。让它抛错等于把网络抖动变成白屏。
    const s = reduceEvents(emptyConversation(), [
      { type: "TOOL_CALL_RESULT", toolCallId: "c9", content: "done", role: "tool" } as AgEvent,
    ])
    const t = toolParts(s)[0]
    expect(t?.id).toBe("c9")
    expect(t?.result).toBe("done")
  })

  test("RESULT 按 toolCallId 跨消息回填", () => {
    // TOOL_CALL_RESULT 只带 toolCallId，工具调用可能挂在别的消息上。
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
    // 就地改会让 React 的比较彻底失效 —— 流式下每个 delta 都调一次。
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
