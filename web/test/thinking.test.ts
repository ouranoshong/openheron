import { describe, expect, test } from "bun:test"

import { formatThinkingDuration, thinkingLabel } from "../src/lib/thinking"

describe("formatThinkingDuration", () => {
  test("sub-second keeps one decimal", () => {
    // ★ 四舍五入成 "0s" 会读成「没花时间」—— 0.4s 就是 0.4s。
    expect(formatThinkingDuration(400)).toBe("0.4s")
    expect(formatThinkingDuration(999)).toBe("1.0s")
  })

  test("seconds keep one decimal below ten", () => {
    expect(formatThinkingDuration(1000)).toBe("1.0s")
    expect(formatThinkingDuration(3200)).toBe("3.2s")
    expect(formatThinkingDuration(9999)).toBe("10.0s")
  })

  test("ten seconds and above drop the decimal", () => {
    // 一位小数在那个量级上是噪音。
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
    // 已经在算了也不提前报数 —— 「想了多久」只在结束时才有意义。
    expect(thinkingLabel({ complete: false, elapsedMs: 3000 })).toBe("Thinking…")
  })

  test("when finished with a known duration it says Thought for N", () => {
    expect(thinkingLabel({ complete: true, elapsedMs: 3200 })).toBe("Thought for 3.2s")
  })

  test("when finished without a duration it just says Thought", () => {
    // ★ 历史回放没有计时 —— 那是「不知道」，不是「0 秒」。
    expect(thinkingLabel({ complete: true, elapsedMs: null })).toBe("Thought")
  })

  test("it never says Thinking once it is over", () => {
    // ★ 结束之后标题停在 "Thinking" 会让人以为界面卡住了。
    for (const elapsedMs of [null, 0, 1, 3200]) {
      expect(thinkingLabel({ complete: true, elapsedMs })).not.toContain("Thinking")
    }
  })

  test("an interrupted one says Interrupted, not Thought", () => {
    // ★ 被掐断的**没想完** —— 说它「Thought for 2.1s」会让人以为结论是
    //   想透了才说的，而它其实是被砍断的。
    expect(thinkingLabel({ complete: true, elapsedMs: 2100, interrupted: true })).toBe(
      "Interrupted after 2.1s",
    )
    expect(thinkingLabel({ complete: true, elapsedMs: null, interrupted: true })).toBe("Interrupted")
    // 没掐断的仍然是 Thought —— 两者不能混。
    expect(thinkingLabel({ complete: true, elapsedMs: 2100 })).toBe("Thought for 2.1s")
  })

  test("an interrupted one that is still running still says Thinking…", () => {
    // interrupted 只在**收尾**时才有意义 —— 还没结束就说「被掐断」是自相矛盾。
    expect(thinkingLabel({ complete: false, elapsedMs: null, interrupted: true })).toBe("Thinking…")
  })
})
