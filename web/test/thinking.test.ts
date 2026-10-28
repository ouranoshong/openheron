import { describe, expect, test } from "bun:test"

import { formatThinkingDuration, thinkingLabel } from "../src/lib/thinking"

describe("formatThinkingDuration", () => {
  test("sub-second keeps one decimal", () => {
    expect(formatThinkingDuration(400)).toBe("0.4s")
    expect(formatThinkingDuration(999)).toBe("1.0s")
  })

  test("seconds keep one decimal below ten", () => {
    expect(formatThinkingDuration(1000)).toBe("1.0s")
    expect(formatThinkingDuration(3200)).toBe("3.2s")
    expect(formatThinkingDuration(9999)).toBe("10.0s")
  })

  test("ten seconds and above drop the decimal", () => {
    expect(formatThinkingDuration(10_000)).toBe("10s")
    expect(formatThinkingDuration(12_400)).toBe("12s")
    expect(formatThinkingDuration(65_000)).toBe("65s")
  })

  test("nonsense inputs do not produce NaN", () => {
    expect(formatThinkingDuration(0)).toBe("0s")
    expect(formatThinkingDuration(-5)).toBe("0s")
    expect(formatThinkingDuration(Number.NaN)).toBe("0s")
  })
})

describe("thinkingLabel", () => {
  test("while thinking it says Thinking…", () => {
    expect(thinkingLabel({ complete: false, elapsedMs: null })).toBe("Thinking…")
    expect(thinkingLabel({ complete: false, elapsedMs: 3000 })).toBe("Thinking…")
  })

  test("when finished with a known duration it says Thought for N", () => {
    expect(thinkingLabel({ complete: true, elapsedMs: 3200 })).toBe("Thought for 3.2s")
  })

  test("when finished without a duration it just says Thought", () => {
    expect(thinkingLabel({ complete: true, elapsedMs: null })).toBe("Thought")
  })

  test("it never says Thinking once it is over", () => {
    for (const elapsedMs of [null, 0, 1, 3200]) {
      expect(thinkingLabel({ complete: true, elapsedMs })).not.toContain("Thinking")
    }
  })

  test("an interrupted one says Interrupted, not Thought", () => {
    expect(thinkingLabel({ complete: true, elapsedMs: 2100, interrupted: true })).toBe(
      "Interrupted after 2.1s",
    )
    expect(thinkingLabel({ complete: true, elapsedMs: null, interrupted: true })).toBe("Interrupted")
    expect(thinkingLabel({ complete: true, elapsedMs: 2100 })).toBe("Thought for 2.1s")
  })

  test("an interrupted one that is still running still says Thinking…", () => {
    expect(thinkingLabel({ complete: false, elapsedMs: null, interrupted: true })).toBe("Thinking…")
  })
})
