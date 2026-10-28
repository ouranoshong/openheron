import { describe, expect, test } from "bun:test"

import {
  approvalArguments,
  approvalMessage,
  approvalTitle,
  approvalToolName,
} from "../src/lib/approval"

describe("approval content", () => {
  test("工具名优先用结构化字段", () => {
    expect(
      approvalToolName({
        toolName: "write_file",
        message: "tool edit_file requires approval",
      }),
    ).toBe("write_file")
  })

  test("没有结构化字段时从兜底文案里取工具名", () => {
    expect(approvalToolName({ message: "tool write_file requires approval" })).toBe(
      "write_file",
    )
  })

  test("确实没有工具名时用中性占位，不把 reason 当内容", () => {
    expect(approvalToolName({ message: "something happened" })).toBe("Tool call")
  })

  test("和工具名重复的兜底说明不再展示", () => {
    expect(
      approvalMessage({
        toolName: "write_file",
        message: "tool write_file requires approval",
      }),
    ).toBeUndefined()
  })

  test("自定义说明保留", () => {
    expect(
      approvalMessage({
        toolName: "write_file",
        message: "This will overwrite an existing file.",
      }),
    ).toBe("This will overwrite an existing file.")
  })

  test("参数既认 JSON 字符串也认对象", () => {
    expect(approvalArguments({ arguments: '{"file_path":"/tmp/x.txt"}' })).toBe(
      '{"file_path":"/tmp/x.txt"}',
    )
    expect(approvalArguments({ arguments: { file_path: "/tmp/x.txt" } })).toBe(
      '{"file_path":"/tmp/x.txt"}',
    )
  })

  test("参数压成单行并封顶，避免 write_file 的 content 把按钮挤走", () => {
    const got = approvalArguments({ arguments: { path: "/tmp/x", content: "x".repeat(500) } })
    expect(got).toBeDefined()
    expect(got!.length).toBeLessThanOrEqual(180)
    expect(got!.endsWith("…")).toBe(true)
    expect(got).not.toContain("\n")
  })

  test("空参数不占一行", () => {
    expect(approvalArguments({ arguments: {} })).toBeUndefined()
    expect(approvalArguments({ arguments: "" })).toBeUndefined()
  })

  test("标题区分单个与整组", () => {
    expect(approvalTitle(1)).toBe("Approval required")
    expect(approvalTitle(3)).toBe("3 tool calls need your approval")
  })
})
