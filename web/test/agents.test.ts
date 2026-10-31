import { describe, expect, test } from "bun:test"

import { collectAgents, filterByAgent, formatPercent, formatTokens } from "../src/lib/agents"
import type { Message, Usage } from "../src/lib/types"

function msg(agent: string | undefined, role: Message["role"] = "assistant"): Message {
  return { id: `m-${agent ?? "none"}-${role}`, role, parts: [], agent }
}

describe("collectAgents", () => {
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

  test("from a thousand it abbreviates", () => {
    expect(formatTokens(1000)).toBe("1.0k")
    expect(formatTokens(3400)).toBe("3.4k")
    expect(formatTokens(12000)).toBe("12k")
  })

  test("a million reads as millions", () => {
    // "1311k" takes longer to compare at a glance than "1.3M".
    expect(formatTokens(1311000)).toBe("1.3M")
    expect(formatTokens(2000000)).toBe("2.0M")
  })
})

describe("formatPercent", () => {
  test("a non-zero ratio is never rounded down to zero", () => {
    // 0.3 % is not 0 %. The reader stops watching exactly when the number
    // starts to matter.
    expect(formatPercent(4300 / 1311000)).toBe("0.3 %")
    expect(formatPercent(0.0005)).toBe("<0.1 %")
  })

  test("the precision follows the size", () => {
    expect(formatPercent(0)).toBe("0 %")
    expect(formatPercent(14.06 / 100)).toBe("14 %")
    expect(formatPercent(99.6 / 100)).toBe("100 %")
  })
})
