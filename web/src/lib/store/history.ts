import type { AgMessage, PendingApproval, ThreadMeta } from "@/lib/api"
import {
  emptyConversation,
  type AttachmentPart,
  type CompactionMark,
  type ConversationState,
  type Message,
  type Usage,
} from "@/lib/types"

// readAttachments reads the attachment list off the wire metadata.
//
// ★ The server stamps it on the message (api/v2_convert.go). The list is
//   what lets the history answer "that image you just sent" — the files
//   belong to the message, not to the session.
function readAttachments(raw: unknown): AttachmentPart[] {
  if (!Array.isArray(raw)) return []
  const out: AttachmentPart[] = []
  for (const item of raw) {
    if (!item || typeof item !== "object") continue
    const a = item as Record<string, unknown>
    const name = typeof a.name === "string" ? a.name : ""
    const path = typeof a.path === "string" ? a.path : ""
    if (!name && !path) continue
    out.push({
      kind: "attachment",
      id: "",
      name,
      path,
      size: typeof a.size === "number" ? a.size : 0,
      mime: typeof a.mime === "string" ? a.mime : "",
    })
  }
  return out
}

// readCompaction reads the compaction marker off the wire metadata.
//
// The server stamps it on the message that replaced a stretch of history
// (api/v2_convert.go). The marker's presence is what makes the message a
// boundary; the numbers inside it are details, and they may be missing.
function readCompaction(raw: unknown): CompactionMark | undefined {
  if (!raw || typeof raw !== "object") return undefined
  const m = raw as Record<string, unknown>
  return {
    superseded: typeof m.superseded === "number" ? m.superseded : 0,
    savedTokens: typeof m.savedTokens === "number" ? m.savedTokens : 0,
    at: typeof m.at === "string" ? m.at : undefined,
  }
}

function findToolOwner(messages: Message[], toolCallId: string): Message | undefined {
  if (!toolCallId) return undefined
  return messages.find((m) => m.parts.some((p) => p.kind === "tool" && p.id === toolCallId))
}

export function seedFromHistory(
  raw: AgMessage[],
  pending?: PendingApproval[],
  usage?: Usage,
  state?: ThreadMeta["state"],
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
    // ★ The files come first, the text after — the same order the
    //   optimistic bubble uses, so a message looks the same whether it was
    //   just sent or read back from the transcript.
    const attachments = readAttachments(meta.attachments)
    for (const [i, a] of attachments.entries()) {
      parts.push({ ...a, id: `${m.id}:a${i}` })
    }
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
    const compaction = readCompaction(meta.compaction)
    messages.push({
      id: uniq(m.id || `msg-${messages.length}`),
      role: m.role,
      parts,
      agent: m.name,
      ...(compaction ? { compaction } : {}),
    })
  }

  const convo = emptyConversation()
  convo.messages = messages
  if (usage) convo.usage = usage
  // The occupancy is a point-in-time reading, not a total: it does not live
  // inside the usage.
  if (usage?.context) convo.context = usage.context
  // ★ The server's state is the truth about whether this session is busy.
  //   Without it a second tab hydrates as idle and its send button works —
  //   the tab has no memory of the turn another tab started.
  //
  // ★ The busy **flag** does not go up here, though. The flag says the
  //   live turn belongs to somebody else, and this surface is about to
  //   find out whether it can watch that turn instead (attachToLiveRun):
  //   a flag that went up and came down again would flash the warning at
  //   a surface that is following the turn after all.
  if (state === "running") {
    convo.status = "running"
  }
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
