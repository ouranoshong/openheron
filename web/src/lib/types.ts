
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

// CompactionMark is what a compacted message carries.
//
// It marks the boundary of the effective history: the model sees the
// summary, not the messages above the boundary. Those messages stay in the
// transcript — compaction is not deletion — so the user can still read them.
export interface CompactionMark {
  // superseded is how many transcript messages the summary replaced.
  superseded: number
  // savedTokens is the estimated number of tokens the compaction removed
  // from the next request. It is an estimate.
  savedTokens: number
  // at is when the compaction happened.
  at?: string
}

export interface Message {
  id: string
  role: Role
  parts: Part[]
  agent?: string
  // compaction is set on the message that replaced a stretch of history.
  // Such a message is a boundary, not a chat bubble.
  compaction?: CompactionMark
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
  // context is the per-agent context occupancy. It is a point-in-time
  // reading, unlike byAgent, which is a cumulative spend.
  context?: Record<string, ContextUsage>
}

// ContextUsage is one agent's context occupancy.
//
// "used" is the size of that agent's most recent request, not a sum over the
// session: a session can carry a large total and an almost empty context.
// It is the provider's own count, so it is an estimate of the occupancy.
export interface ContextUsage {
  agent: string
  provider?: string
  model?: string
  usedTokens: number
  // contextWindow is the model's window. Zero means the user did not
  // configure one, and no ratio may be shown.
  contextWindow: number
  // summarizeAt is the token count at which the history would be compacted.
  // Zero means never.
  summarizeAt?: number
  // summarized counts the compactions this agent went through.
  summarized?: number
  updatedAt?: string
}

export interface ConversationState {
  messages: Message[]
  status: TurnStatus
  interrupts: Interrupt[]
  error?: RunError
  usage?: Usage
  // context is the per-agent context occupancy, keyed by agent. It is a
  // different quantity from usage.byAgent — that one is a cumulative spend,
  // this one is a point-in-time reading — so it does not live inside it.
  context?: Record<string, ContextUsage>
  retry?: { message: string }
  // notice is a transient line about something the server is doing right
  // now. It is not an error: the work is still going on.
  notice?: string
}

export function emptyConversation(): ConversationState {
  return { messages: [], status: "idle", interrupts: [] }
}
