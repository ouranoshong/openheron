import { describe, expect, test } from "bun:test"

import { collectAgents, filterByAgent, formatTokens } from "../src/lib/agents"
import type { Message, Usage } from "../src/lib/types"

function msg(agent: string | undefined, role: Message["role"] = "assistant"): Message {
  return { id: `m-${agent ?? "none"}-${role}`, role, parts: [], agent }
}

describe("collectAgents", () => {
  // agent 名要同时来自「谁花的」与「谁干活的」两个来源 ——
  // 只取一边会漏掉另一半的情况。
  test("union of the usage buckets and the message stamps", () => {
    const usage: Usage = {
      promptTokens: 0,
      completionTokens: 0,
      totalTokens: 0,
      byAgent: { coder: { promptTokens: 0, completionTokens: 0, totalTokens: 10 } },
    }
    const agents = collectAgents([msg("main"), msg("reviewer")], usage)
    expect(agents).toEqual(["coder", "main", "reviewer"])
  })

  test("a message without a stamp contributes nothing", () => {
    expect(collectAgents([msg(undefined, "user")])).toEqual([])
  })

  test("the order is stable", () => {
    const a = collectAgents([msg("zeta"), msg("alpha")])
    const b = collectAgents([msg("alpha"), msg("zeta")])
    expect(a).toEqual(b)
  })
})

describe("filterByAgent", () => {
  // user 消息没有归属标记 —— 过滤后仍要显示，
  // 否则用户会看到自己刚问的那句话凭空消失。
  test("keeps the user messages", () => {
    const messages = [msg(undefined, "user"), msg("coder"), msg("reviewer")]
    const out = filterByAgent(messages, "coder")
    expect(out.map((m) => m.agent ?? "user")).toEqual(["user", "coder"])
  })

  test("no filter keeps everything, by reference", () => {
    const messages = [msg("coder")]
    expect(filterByAgent(messages, null)).toBe(messages)
  })

  test("an agent with no messages yields only the user messages", () => {
    const messages = [msg(undefined, "user"), msg("coder")]
    expect(filterByAgent(messages, "nobody")).toHaveLength(1)
  })
})

describe("formatTokens", () => {
  test("below a thousand it stays exact", () => {
    expect(formatTokens(0)).toBe("0")
    expect(formatTokens(999)).toBe("999")
  })

  // 999 写成 "1.0k" 会让人以为它比 999 多。
  test("from a thousand it abbreviates", () => {
    expect(formatTokens(1000)).toBe("1.0k")
    expect(formatTokens(3400)).toBe("3.4k")
    expect(formatTokens(12000)).toBe("12k")
  })
})
