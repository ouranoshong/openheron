import { describe, expect, test } from "bun:test"

import { reduceEvent, type AgEvent } from "../src/lib/events"
import { seedFromHistory } from "../src/lib/store"
import type { AgMessage } from "../src/lib/api"

function compactedMessage(meta?: Record<string, unknown>): AgMessage {
  return {
    id: "m-1",
    role: "user",
    content: "summary of the conversation",
    ...(meta ? { metadata: meta } : {}),
  } as AgMessage
}

function compactedEvent(value: Record<string, unknown>): AgEvent {
  return { type: "CUSTOM", name: "context_compacted", value } as AgEvent
}

// The marker rides on the message: the history has to pick it up, or the
// compaction is invisible after a reload.
test("历史水合读出压缩标记", () => {
  const convo = seedFromHistory([
    compactedMessage({ compaction: { superseded: 12, savedTokens: 62000, at: "2026-10-29T13:04:11Z" } }),
  ])

  const mark = convo.messages[0]?.compaction
  expect(mark?.superseded).toBe(12)
  expect(mark?.savedTokens).toBe(62000)
  expect(mark?.at).toBe("2026-10-29T13:04:11Z")
})

// An ordinary message is not a compaction.
test("没有标记的消息不是压缩", () => {
  const convo = seedFromHistory([compactedMessage()])
  expect(convo.messages[0]?.compaction).toBeUndefined()
})

// The marker's presence is what makes the message a boundary; the numbers
// inside it are details and may be missing.
test("标记在就算边界，数字可以缺", () => {
  const convo = seedFromHistory([
    compactedMessage({ compaction: { superseded: 0, savedTokens: 0 } }),
  ])
  expect(convo.messages[0]?.compaction).toBeDefined()
  expect(convo.messages[0]?.compaction?.superseded).toBe(0)
})

// The compaction happens mid-turn: the boundary goes into the list at once.
test("实时事件插进一条边界", () => {
  const state = reduceEvent(
    { ...seedFromHistory([]), messages: [] },
    compactedEvent({ agent: "main", at: "2026-10-29T13:04:11.512Z" }),
  )

  expect(state.messages).toHaveLength(1)
  expect(state.messages[0]?.compaction?.at).toBe("2026-10-29T13:04:11.512Z")
  expect(state.messages[0]?.agent).toBe("main")
})

// One compaction is one boundary, however often it is announced.
test("同一个压缩只插一次边界", () => {
  const event = compactedEvent({ agent: "main", at: "2026-10-29T13:04:11.512Z" })
  const once = reduceEvent(seedFromHistory([]), event)
  const twice = reduceEvent(once, event)

  expect(twice.messages).toHaveLength(1)
})

describe("实时边界没有数字", () => {
  test("数字是零，因为它们在轮结束后才量得出来", () => {
    const state = reduceEvent(
      seedFromHistory([]),
      compactedEvent({ agent: "main", at: "2026-10-29T13:04:11.512Z" }),
    )
    expect(state.messages[0]?.compaction?.superseded).toBe(0)
    expect(state.messages[0]?.compaction?.savedTokens).toBe(0)
  })
})
