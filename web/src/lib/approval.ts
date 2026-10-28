import type { Interrupt } from "./types"

const MESSAGE_TOOL = /^tool\s+(\S+)\s+requires approval\.?$/i
const MAX_ARGUMENT_CHARS = 180

export function approvalTitle(count: number): string {
  return count === 1 ? "Approval required" : `${count} tool calls need your approval`
}

export function approvalToolName(
  interrupt: Pick<Interrupt, "toolName" | "message">,
): string {
  return (
    interrupt.toolName?.trim() ||
    MESSAGE_TOOL.exec(interrupt.message?.trim() ?? "")?.[1] ||
    "Tool call"
  )
}

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
