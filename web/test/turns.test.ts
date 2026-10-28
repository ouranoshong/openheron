import { describe, expect, test } from "bun:test"

import { turnFlags } from "../src/lib/turns"
import type { Message } from "../src/lib/types"

function msg(id: string, role: Message["role"]): Message {
  return { id, role, parts: [] }
}

describe("turnFlags", () => {
  test("空历史没有标记", () => {
    expect(turnFlags([])).toEqual([])
  })

  test("同一轮的多条 assistant 只显示一次标签", () => {
    const flags = turnFlags([
      msg("u1", "user"),
      msg("a1", "assistant"),
      msg("a2", "assistant"),
      msg("a3", "assistant"),
    ])

    expect(flags.map((f) => f.startsTurn)).toEqual([true, false, false, false])
    expect(flags.map((f) => f.showRole)).toEqual([false, true, false, false])
  })

  test("新的 user 消息开启新一轮", () => {
    const flags = turnFlags([
      msg("u1", "user"),
      msg("a1", "assistant"),
      msg("u2", "user"),
      msg("a2", "assistant"),
    ])

    expect(flags.map((f) => f.startsTurn)).toEqual([true, false, true, false])
    expect(flags.map((f) => f.showRole)).toEqual([false, true, false, true])
  })

  test("历史以 assistant 开头时仍显示角色", () => {
    const flags = turnFlags([msg("a1", "assistant"), msg("a2", "assistant")])
    expect(flags.map((f) => f.showRole)).toEqual([true, false])
  })

  test("换了 agent 就重新显示标签", () => {
    const flags = turnFlags([
      msg("u1", "user"),
      { ...msg("a1", "assistant"), agent: "main" },
      { ...msg("a2", "assistant"), agent: "coder" },
      { ...msg("a3", "assistant"), agent: "coder" },
    ])
    expect(flags.map((f) => f.showRole)).toEqual([false, true, true, false])
  })

  test("同一个 agent 的连续片段只显示一次标签", () => {
    const flags = turnFlags([
      msg("u1", "user"),
      { ...msg("a1", "assistant"), agent: "coder" },
      { ...msg("a2", "assistant"), agent: "coder" },
    ])
    expect(flags.map((f) => f.showRole)).toEqual([false, true, false])
  })

  test("developer/system 之后的 assistant 重新显示角色", () => {
    const flags = turnFlags([
      msg("u1", "user"),
      msg("d1", "developer"),
      msg("a1", "assistant"),
      msg("a2", "assistant"),
    ])
    expect(flags.map((f) => f.showRole)).toEqual([false, true, true, false])
  })
})
