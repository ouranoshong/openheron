
export type Role = "user" | "assistant" | "system" | "developer" | "tool"

export interface TextPart {
  kind: "text"
  id: string
  text: string
  complete: boolean
}

export interface ReasoningPart {
  kind: "reasoning"
  id: string
  text: string
  complete: boolean
  interrupted?: boolean
  elapsedMs?: number
  startedAt?: number
}

export interface ToolPart {
  kind: "tool"
  id: string
  name: string
  argsText: string
  result?: string
  isError?: boolean
  phase: "streaming" | "running" | "complete"
  fromHistory?: boolean
}

export type Part = TextPart | ReasoningPart | ToolPart

export interface Message {
  id: string
  role: Role
  parts: Part[]
  agent?: string
}

export interface Interrupt {
  id: string
  reason: string
  message?: string
  toolCallId?: string
  toolName?: string
  arguments?: unknown
  requestedByAgent?: string
}

export type TurnStatus = "idle" | "running" | "awaiting-approval" | "error"

export interface RunError {
  code: string
  message: string
}

export interface Usage {
  promptTokens: number
  completionTokens: number
  totalTokens: number
  reasoningTokens?: number
  cachedTokens?: number
  byAgent?: Record<string, Usage>
}

export interface ConversationState {
  messages: Message[]
  status: TurnStatus
  interrupts: Interrupt[]
  error?: RunError
  usage?: Usage
  retry?: { message: string }
}

export function emptyConversation(): ConversationState {
  return { messages: [], status: "idle", interrupts: [] }
}
