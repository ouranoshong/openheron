import type { Interrupt } from "./types"

/**
 * 审批卡只回答三个问题：**谁**要调**什么**、**参数长什么样**。
 *
 * ★ 协议里的 reason（"tool_call"）与 toolCallId 是给程序看的，不往界面上放：
 *   前者对用户没有信息量，后者只是一串 uuid。真需要排查时它在网络层。
 */
const MESSAGE_TOOL = /^tool\s+(\S+)\s+requires approval\.?$/i
const MAX_ARGUMENT_CHARS = 180

export function approvalTitle(count: number): string {
  return count === 1 ? "Approval required" : `${count} tool calls need your approval`
}

/** 工具名：优先结构化字段，其次从后端生成的兜底文案里取。 */
export function approvalToolName(
  interrupt: Pick<Interrupt, "toolName" | "message">,
): string {
  return (
    interrupt.toolName?.trim() ||
    MESSAGE_TOOL.exec(interrupt.message?.trim() ?? "")?.[1] ||
    "Tool call"
  )
}

/** 自定义说明；和工具名重复的「tool X requires approval」不再展示一遍。 */
export function approvalMessage(
  interrupt: Pick<Interrupt, "toolName" | "message">,
): string | undefined {
  const message = interrupt.message?.trim()
  if (!message) return undefined

  const name = interrupt.toolName?.trim() || MESSAGE_TOOL.exec(message)?.[1]
  if (!name) return message

  const generic = new RegExp(
    `^(?:tool\\s+)?${escapeRegExp(name)}\\s+requires\\s+approval\\.?$`,
    "i",
  )
  return generic.test(message) ? undefined : message
}

/**
 * 参数摘要：单行、封顶 180 字符。
 *
 * ★ 审批要的是「这个动作会碰什么」，不是完整请求体。write_file 的 content
 *   可能几千字，铺开会把按钮挤出屏幕 —— 关键路径、命令本身通常在开头。
 */
export function approvalArguments(
  interrupt: Pick<Interrupt, "arguments">,
): string | undefined {
  const value = parseArguments(interrupt.arguments)
  if (value === undefined) return undefined

  let text: string
  if (typeof value === "string") {
    text = value
  } else {
    try {
      text = JSON.stringify(value)
    } catch {
      return undefined
    }
  }

  const oneLine = text.replace(/\s+/g, " ").trim()
  if (!oneLine || oneLine === "{}") return undefined
  return oneLine.length > MAX_ARGUMENT_CHARS
    ? `${oneLine.slice(0, MAX_ARGUMENT_CHARS - 1)}…`
    : oneLine
}

/** 实时事件里 arguments 是 JSON 字符串，历史回放里已经是对象。 */
function parseArguments(value: unknown): unknown {
  if (typeof value !== "string") return value
  const text = value.trim()
  if (!text) return undefined
  try {
    return JSON.parse(text)
  } catch {
    return text
  }
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
}
