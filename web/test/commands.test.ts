import { describe, expect, test } from "bun:test"

import type { SlashCommand } from "../src/lib/api"
import { actionCommandFor, agentCommandFor, filterCommands, skillCommandFor } from "../src/lib/commands"

const commands: SlashCommand[] = [
  { name: "new", description: "Start a new thread", kind: "action", action: "new_thread" },
  { name: "stop", description: "Stop the current run", kind: "action", action: "stop_run" },
  { name: "attach", description: "Attach files to this thread", kind: "action", action: "attach_files" },
  { name: "agents", description: "Switch the active agent", kind: "action", action: "open_agents" },
  { name: "review", description: "Review the current changes", kind: "prompt", argumentHint: "[path]" },
]

describe("filterCommands", () => {
  test("matches name or description", () => {
    expect(filterCommands(commands, "rev", { status: "idle", uploading: false }).map((c) => c.name)).toEqual([
      "review",
    ])
    expect(filterCommands(commands, "start", { status: "idle", uploading: false }).map((c) => c.name)).toEqual([
      "new",
    ])
  })

  test("stop is only offered while a run is live", () => {
    expect(filterCommands(commands, "", { status: "idle", uploading: false }).map((c) => c.name)).not.toContain(
      "stop",
    )
    expect(
      filterCommands(commands, "", { status: "running", uploading: false }).map((c) => c.name),
    ).toContain("stop")
  })

  test("attach is hidden while an upload is in flight", () => {
    expect(
      filterCommands(commands, "", { status: "idle", uploading: true }).map((c) => c.name),
    ).not.toContain("attach")
  })

  test("empty query returns the usable set in registry order", () => {
    expect(filterCommands(commands, "", { status: "running", uploading: false }).map((c) => c.name)).toEqual([
      "new",
      "stop",
      "attach",
      "agents",
      "review",
    ])
  })
})

describe("actionCommandFor", () => {
  test("matches a whole action command", () => {
    expect(actionCommandFor("/new", commands)?.action).toBe("new_thread")
    expect(actionCommandFor("  /NEW  ", commands)?.action).toBe("new_thread")
  })

  test("arguments and prompt commands are not actions", () => {
    expect(actionCommandFor("/new x", commands)).toBeNull()
    expect(actionCommandFor("/review", commands)).toBeNull()
    expect(actionCommandFor("/nope", commands)).toBeNull()
  })
})

describe("agentCommandFor", () => {
  test("/agents 打开选择器，/agents name 直接切换", () => {
    expect(agentCommandFor("/agents", commands)).toEqual({ mode: "open" })
    expect(agentCommandFor("  /AGENTS task  ", commands)).toEqual({ mode: "switch", agentId: "task" })
  })

  test("正文里的 /agents 与 /agentsx 都不触发", () => {
    expect(agentCommandFor("please run /agents", commands)).toBeNull()
    expect(agentCommandFor("/agentsx", commands)).toBeNull()
    expect(agentCommandFor("/agents\nmore text", commands)).toBeNull()
  })

  test("服务端没有登记这个命令时不吞消息", () => {
    const without = commands.filter((c) => c.name !== "agents")
    expect(agentCommandFor("/agents", without)).toBeNull()
    expect(agentCommandFor("/agents task", without)).toBeNull()
  })
})

describe("skillCommandFor", () => {
  const withSkill: SlashCommand[] = [
    ...commands,
    { name: "skill", description: "Use a named skill for this task", kind: "prompt" },
  ]

  test("没有名字 → 打开选择器", () => {
    expect(skillCommandFor("/skill", withSkill)).toEqual({ mode: "open" })
    expect(skillCommandFor("/skill   ", withSkill)).toEqual({ mode: "open" })
  })

  test("有名字 → 按那个名字执行", () => {
    expect(skillCommandFor("/skill refactor", withSkill)).toEqual({
      mode: "run",
      name: "refactor",
    })
  })

  test("必须整条匹配 —— 正文里提到不算", () => {
    expect(skillCommandFor("/skillx refactor", withSkill)).toBeNull()
    expect(skillCommandFor("please /skill refactor", withSkill)).toBeNull()
  })

  test("老后端没有 /skill 时不吞这行文本", () => {
    expect(skillCommandFor("/skill refactor", commands)).toBeNull()
  })
})
