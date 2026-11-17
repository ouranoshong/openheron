import { describe, expect, test } from "bun:test"

import { phaseLabel, segments, summarize, type ToolRun } from "../src/lib/tool-groups"
import type { Part, ToolPart } from "../src/lib/types"

// tool is one call, with the phase the test wants.
function tool(id: string, name = "read_file", phase: ToolPart["phase"] = "complete"): ToolPart {
  return { kind: "tool", id, name, argsText: "{}", phase }
}

// text is a part that breaks a run.
function text(id: string): Part {
  return { kind: "text", id, text: "hi", complete: true }
}

describe("segments —— 连续的工具调用归并成一段", () => {
  test("连着的两次调用并成一段", () => {
    const out = segments([tool("c1"), tool("c2")])
    expect(out).toHaveLength(1)
    expect(out[0]!.kind).toBe("run")
    const run = (out[0] as { run: ToolRun }).run
    expect(run.parts.map((p) => p.id)).toEqual(["c1", "c2"])
  })

  test("★ 思考夹在中间 ⇒ 两段：思考是读者要看的，不该被并进去", () => {
    const out = segments([tool("c1"), text("t1"), tool("c2")])
    expect(out.map((s) => s.kind)).toEqual(["run", "part", "run"])
  })

  test("一次调用也是一段", () => {
    const out = segments([tool("c1")])
    expect(out).toHaveLength(1)
    expect(out[0]!.kind).toBe("run")
  })

  test("★ 段的 key 是第一次调用的 id —— 段会在尾部变长，key 不能跟着变", () => {
    const one = segments([tool("c1")])[0] as { run: ToolRun }
    const two = segments([tool("c1"), tool("c2")])[0] as { run: ToolRun }
    expect(one.run.key).toBe("c1")
    expect(two.run.key).toBe("c1")
  })

  test("不是调用的部分照旧按顺序走", () => {
    const out = segments([text("t1"), tool("c1"), text("t2")])
    expect(out.map((s) => s.kind)).toEqual(["part", "run", "part"])
  })
})

describe("summarize —— 这一段说了什么", () => {
  const run = (parts: ToolPart[]): ToolRun => ({ parts, key: parts[0]!.id })

  test("几次调用、用了哪些工具", () => {
    const s = summarize(run([tool("c1", "read_file"), tool("c2", "grep"), tool("c3", "read_file")]))
    expect(s.count).toBe(3)
    // ★ 同样的工具只说一次，顺序是它们跑过的顺序。
    expect(s.titles).toEqual(["Read file", "Search content"])
  })

  test("★ 名单里没有的工具保留自己的名字", () => {
    const s = summarize(run([tool("c1", "mcp__github__create_issue")]))
    expect(s.titles).toEqual(["mcp__github__create_issue"])
  })

  test("★ 有一步在跑 ⇒ 这一段还在跑", () => {
    const s = summarize(run([tool("c1", "read_file", "complete"), tool("c2", "grep", "running")]))
    expect(s.phase).toBe("running")
  })

  test("★ 有一步失败 ⇒ 这一段说失败，而且说了几步失败", () => {
    const failed: ToolPart = { ...tool("c2", "execute"), isError: true }
    const s = summarize(run([tool("c1"), failed]))
    expect(s.failed).toBe(1)
    expect(s.phase).toBe("failed")
  })

  test("都完了就是完了", () => {
    expect(summarize(run([tool("c1")])).phase).toBe("complete")
  })
})

describe("phaseLabel —— 界面上的那个词", () => {
  test("英文", () => {
    expect(phaseLabel("complete")).toBe("done")
    expect(phaseLabel("running")).toBe("running")
    expect(phaseLabel("streaming")).toBe("reading arguments")
    expect(phaseLabel("failed")).toBe("failed")
  })
})
