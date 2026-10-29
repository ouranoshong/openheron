import type { AgMessage, PendingApproval } from "@/lib/api"
import { emptyConversation, type ConversationState, type Message, type Usage } from "@/lib/types"

function findToolOwner(messages: Message[], toolCallId: string): Message | undefined {
  if (!toolCallId) return undefined
  return messages.find((m) => m.parts.some((p) => p.kind === "tool" && p.id === toolCallId))
}

export function seedFromHistory(
  raw: AgMessage[],
  pending?: PendingApproval[],
  usage?: Usage,
): ConversationState {
  const messages: Message[] = []
  const seen = new Map<string, number>()
  const uniq = (id: string): string => {
    const n = seen.get(id) ?? 0
    seen.set(id, n + 1)
    return n === 0 ? id : `${id}#${n}`
  }

  for (const m of raw) {
    if (m.role === "tool") {
      const target = findToolOwner(messages, m.toolCallId ?? "") ?? messages.at(-1)
      if (target) {
        for (const p of target.parts) {
          if (p.kind === "tool" && p.id === m.toolCallId) {
            p.result = typeof m.content === "string" ? m.content : ""
            p.phase = "complete"
          }
        }
      }
      continue
    }

    const parts: Message["parts"] = []
    const meta = m.metadata ?? {}
    const reasoning = typeof meta.reasoning === "string" ? meta.reasoning : ""
    if (reasoning) {
      const ms = typeof meta.reasoningMs === "number" ? meta.reasoningMs : undefined
      parts.push({
        kind: "reasoning",
        id: `${m.id}:r${parts.length}`,
        text: reasoning,
        complete: true,
        elapsedMs: ms,
      })
    }
    if (typeof m.content === "string" && m.content) {
      parts.push({ kind: "text", id: `${m.id}:t${parts.length}`, text: m.content, complete: true })
    }
    for (const tc of m.toolCalls ?? []) {
      parts.push({
        kind: "tool",
        id: tc.id,
        name: tc.function.name,
        argsText: tc.function.arguments,
        phase: "complete",
        fromHistory: true,
      })
    }
    messages.push({ id: uniq(m.id || `msg-${messages.length}`), role: m.role, parts, agent: m.name })
  }

  const convo = emptyConversation()
  convo.messages = messages
  if (usage) convo.usage = usage
  // The occupancy is a point-in-time reading, not a total: it does not live
  // inside the usage.
  if (usage?.context) convo.context = usage.context
  if (pending && pending.length > 0) {
    convo.status = "awaiting-approval"
    convo.interrupts = pending.map((p) => ({
      id: p.interruptId,
      reason: p.reason,
      message: p.message,
      toolCallId: p.toolCallId,
      toolName: p.toolName,
      arguments: p.arguments,
      requestedByAgent: p.subagentRunId ?? p.requestedByAgent,
    }))
  }
  return convo
}
