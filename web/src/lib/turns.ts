import type { Message } from "@/lib/types"

export interface TurnFlags {
  startsTurn: boolean
  showRole: boolean
}

export function turnFlags(messages: Message[]): TurnFlags[] {
  const out: TurnFlags[] = []
  let prevRole: Message["role"] | null = null
  let prevAgent: string | undefined

  for (const message of messages) {
    const isUser = message.role === "user"
    const agentChanged = message.agent !== prevAgent
    out.push({
      startsTurn: isUser || prevRole === null,
      showRole: !isUser && (prevRole !== "assistant" || agentChanged),
    })
    prevRole = message.role
    prevAgent = message.agent
  }

  return out
}
