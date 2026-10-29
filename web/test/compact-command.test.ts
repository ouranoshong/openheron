import { describe, expect, test } from "bun:test"

import { filterCommands } from "../src/lib/commands"
import { reduceEvent, type AgEvent } from "../src/lib/events"
import type { SlashCommand } from "../src/lib/api"

const compact: SlashCommand = {
  name: "compact",
  description: "Compact the conversation into a summary",
  kind: "action",
  action: "compact",
}

const stop: SlashCommand = {
  name: "stop",
  description: "Stop the current run",
  kind: "action",
  action: "stop_run",
}

// A running turn owns the context: compacting under it would cut the history
// the turn is working with.
describe("压缩命令的可见性", () => {
  test("空闲时可见", () => {
    const got = filterCommands([compact, stop], "", { status: "idle", uploading: false })
    expect(got.map((c) => c.name)).toEqual(["compact"])
  })

  test("运行中隐藏", () => {
    const got = filterCommands([compact, stop], "", { status: "running", uploading: false })
    expect(got.map((c) => c.name)).toEqual(["stop"])
  })

  test("等审批时隐藏", () => {
    const got = filterCommands([compact, stop], "", {
      status: "awaiting-approval",
      uploading: false,
    })
    expect(got.map((c) => c.name)).toEqual([])
  })
})

// The manual compaction knows its numbers; the live one does not.
describe("压缩边界上的数字", () => {
  const compacted = (value: Record<string, unknown>): AgEvent =>
    ({ type: "CUSTOM", name: "context_compacted", value }) as AgEvent

  test("手动压缩带着数字来", () => {
    const state = reduceEvent(
      { messages: [], status: "idle", interrupts: [] },
      compacted({ agent: "main", at: "2026-10-30T00:00:00.000Z", superseded: 42, savedTokens: 62000 }),
    )

    expect(state.messages).toHaveLength(1)
    expect(state.messages[0]?.compaction?.superseded).toBe(42)
    expect(state.messages[0]?.compaction?.savedTokens).toBe(62000)
  })

  test("实时压缩没有数字", () => {
    const state = reduceEvent(
      { messages: [], status: "idle", interrupts: [] },
      compacted({ agent: "main", at: "2026-10-30T00:00:00.000Z" }),
    )

    expect(state.messages[0]?.compaction?.superseded).toBe(0)
    expect(state.messages[0]?.compaction?.savedTokens).toBe(0)
  })
})
