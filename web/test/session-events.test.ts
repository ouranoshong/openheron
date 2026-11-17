import { describe, expect, test } from "bun:test"

import {
  applySessionFact,
  parseSessionFact,
  type ApprovalPending,
  type ApprovalResolved,
  type RunStarted,
} from "../src/lib/session-events"
import { emptyConversation, type ConversationState } from "../src/lib/types"

// waiting builds a conversation that holds one approval.
function waiting(ids: string[] = ["int-1"]): ConversationState {
  const convo = emptyConversation()
  convo.status = "awaiting-approval"
  convo.interrupts = ids.map((id) => ({
    id,
    reason: "tool_call",
    toolName: "write_file",
  }))
  return convo
}

describe("会话事实的解析", () => {
  test("审批在等，读出那张卡的内容", () => {
    const fact = parseSessionFact({
      type: "CUSTOM",
      name: "approval_pending",
      value: {
        interrupt_id: "int-1",
        reason: "tool_call",
        message: "write_file needs approval",
        tool_call_id: "call-1",
        tool_name: "write_file",
        arguments: { path: "config.yaml" },
        requested_by_agent: "task",
        subagent_run_id: "sub-1",
        run_id: "run-1",
      },
    })
    expect(fact).toEqual({
      kind: "approval-pending",
      interrupt: {
        id: "int-1",
        reason: "tool_call",
        message: "write_file needs approval",
        toolCallId: "call-1",
        toolName: "write_file",
        arguments: { path: "config.yaml" },
        // ★ 子 agent 的 run id 说了算——跟扫描那条路一个规矩。
        requestedByAgent: "sub-1",
      },
      runId: "run-1",
    } satisfies ApprovalPending)
  })

  test("审批被回答，读出那张卡的名字和决定", () => {
    const fact = parseSessionFact({
      type: "CUSTOM",
      name: "approval_resolved",
      value: {
        interrupt_id: "int-1",
        approved: true,
        reason: "it is my own config",
        run_id: "run-2",
      },
    })
    expect(fact).toEqual({
      kind: "approval-resolved",
      interruptId: "int-1",
      approved: true,
      reason: "it is my own config",
      runId: "run-2",
    } satisfies ApprovalResolved)
  })

  test("新一轮开始，读出那一轮的 id 和种类", () => {
    const fact = parseSessionFact({
      type: "CUSTOM",
      name: "run_started",
      value: { run_id: "run-3", kind: "resume" },
    })
    expect(fact).toEqual({
      kind: "run-started",
      runId: "run-3",
      turnKind: "resume",
    } satisfies RunStarted)
  })

  test("不认识的帧不是事实", () => {
    expect(parseSessionFact({ type: "CUSTOM", name: "context_usage", value: {} })).toBeNull()
    expect(parseSessionFact({ type: "RUN_STARTED", runId: "run-1" })).toBeNull()
    expect(parseSessionFact(null)).toBeNull()
    expect(parseSessionFact("nonsense")).toBeNull()
  })

  test("半懂的帧被丢掉——没有名字的审批指不到任何一张卡", () => {
    expect(
      parseSessionFact({ type: "CUSTOM", name: "approval_resolved", value: { approved: true } }),
    ).toBeNull()
    expect(parseSessionFact({ type: "CUSTOM", name: "run_started", value: {} })).toBeNull()
    expect(parseSessionFact({ type: "CUSTOM", name: "approval_pending", value: {} })).toBeNull()
  })
})

describe("会话事实的折叠", () => {
  test("审批在等，卡出现，这一边在等答案", () => {
    const convo = applySessionFact(emptyConversation(), {
      kind: "approval-pending",
      interrupt: { id: "int-1", reason: "tool_call", toolName: "write_file" },
      runId: "run-1",
    })
    expect(convo.interrupts.map((i) => i.id)).toEqual(["int-1"])
    expect(convo.status).toBe("awaiting-approval")
  })

  test("这张卡已经在手上，就不添第二张", () => {
    // ★ 发起那一轮的那一边是从 run 自己的流里看到这张卡的，
    //   通道说的是同一张卡——不是两张。
    const before = waiting(["int-1"])
    const after = applySessionFact(before, {
      kind: "approval-pending",
      interrupt: { id: "int-1", reason: "tool_call", toolName: "write_file" },
      runId: "run-1",
    })
    expect(after.interrupts).toHaveLength(1)
    expect(after.interrupts).toBe(before.interrupts)
  })

  test("审批被回答，那张卡消失", () => {
    const convo = applySessionFact(waiting(), {
      kind: "approval-resolved",
      interruptId: "int-1",
      approved: true,
      reason: "",
      runId: "run-2",
    })
    expect(convo.interrupts).toEqual([])
    expect(convo.status).toBe("idle")
  })

  test("还有别的审批在等，就还在等", () => {
    const convo = applySessionFact(waiting(["int-1", "int-2"]), {
      kind: "approval-resolved",
      interruptId: "int-1",
      approved: true,
      reason: "",
      runId: "run-2",
    })
    expect(convo.interrupts.map((i) => i.id)).toEqual(["int-2"])
    expect(convo.status).toBe("awaiting-approval")
  })

  test("不是这张卡的事实，不动这个会话", () => {
    const before = waiting()
    const after = applySessionFact(before, {
      kind: "approval-resolved",
      interruptId: "int-9",
      approved: true,
      reason: "",
      runId: "run-2",
    })
    expect(after).toBe(before)
  })

  test("新一轮开始，这一边跟上去", () => {
    const convo = applySessionFact(waiting(), {
      kind: "run-started",
      runId: "run-3",
      turnKind: "resume",
    })
    expect(convo.status).toBe("running")
    expect(convo.interrupts).toEqual([])
    expect(convo.busy).toBe(false)
  })

  test("新一轮不写下 run id——那把钥匙属于真正在看这一轮的那一边", () => {
    const convo = applySessionFact(waiting(), {
      kind: "run-started",
      runId: "run-3",
      turnKind: "resume",
    })
    expect(convo.runId).toBeUndefined()
  })
})
