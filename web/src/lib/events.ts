
import type {
  ContextUsage,
  ConversationState,
  Interrupt,
  Usage,
  Message,
  Part,
  Role,
  ToolPart,
} from "./types"

export interface RunStartedEvent {
  type: "RUN_STARTED"
  threadId?: string
  runId?: string
}

export interface TextMessageStartEvent {
  type: "TEXT_MESSAGE_START"
  messageId: string
  role?: string
  name?: string
}

export interface TextMessageContentEvent {
  type: "TEXT_MESSAGE_CONTENT"
  messageId: string
  delta?: string
}

export interface TextMessageEndEvent {
  type: "TEXT_MESSAGE_END"
  messageId: string
}

export interface ToolCallStartEvent {
  type: "TOOL_CALL_START"
  toolCallId: string
  toolCallName?: string
  parentMessageId?: string
}

export interface ToolCallArgsEvent {
  type: "TOOL_CALL_ARGS"
  toolCallId: string
  delta?: string
}

export interface ToolCallEndEvent {
  type: "TOOL_CALL_END"
  toolCallId: string
}

export interface ToolCallResultEvent {
  type: "TOOL_CALL_RESULT"
  messageId?: string
  toolCallId: string
  content?: string
  role?: string
}

export interface ReasoningStartEvent {
  type: "REASONING_START" | "REASONING_MESSAGE_START"
  messageId: string
  role?: string
  timestamp?: number
}

export interface ReasoningMessageContentEvent {
  type: "REASONING_MESSAGE_CONTENT"
  messageId: string
  delta?: string
  timestamp?: number
}

export interface ReasoningEndEvent {
  type: "REASONING_END" | "REASONING_MESSAGE_END"
  messageId: string
  timestamp?: number
}

export interface RawInterruptEvent {
  id: string
  reason: string
  message?: string
  toolCallId?: string
  toolName?: string
  arguments?: unknown
  metadata?: {
    toolName?: string
    arguments?: unknown
  }
  subagentRunId?: string
}

// ContextUsageEvent is the payload of the context_usage custom event.
//
// The wire uses snake_case: the server owns the shape, and the reducer
// translates it once, here.
export interface ContextUsageEvent {
  agent?: string
  provider?: string
  model?: string
  used_tokens?: number
  context_window?: number
  summarize_at?: number
  updated_at?: string
}

// ContextCompactedEvent is the payload of the context_compacted custom event.
//
// It says that the context was compacted and when. The numbers — how many
// messages were superseded, how many tokens the summary saved — are measured
// against the transcript, which is written when the turn ends; the client
// reads them from the marker on the next load.
//
// A manual compaction knows its numbers already, so it sends them along.
export interface ContextCompactedEvent {
  agent?: string
  at?: string
  superseded?: number
  savedTokens?: number
}

export interface UsageEvent {
  inputTokens?: number
  outputTokens?: number
  promptTokens?: number
  completionTokens?: number
  totalTokens?: number
  reasoningTokens?: number
  cachedTokens?: number
  cachedInputTokens?: number
}

export interface RunFinishedEvent {
  type: "RUN_FINISHED"
  threadId?: string
  runId?: string
  outcome?: {
    type?: string
    interrupts?: RawInterruptEvent[]
    result?: { usage?: UsageEvent }
  }
}

export interface RunErrorEvent {
  type: "RUN_ERROR"
  code?: string
  message?: string
}

export interface CustomEvent {
  type: "CUSTOM"
  name: string
  value?: unknown
}

export type AgEvent =
  | RunStartedEvent
  | TextMessageStartEvent
  | TextMessageContentEvent
  | TextMessageEndEvent
  | ToolCallStartEvent
  | ToolCallArgsEvent
  | ToolCallEndEvent
  | ToolCallResultEvent
  | ReasoningStartEvent
  | ReasoningMessageContentEvent
  | ReasoningEndEvent
  | RunFinishedEvent
  | RunErrorEvent
  | CustomEvent

function replaceAt<T>(arr: T[], index: number, value: T): T[] {
  const next = arr.slice()
  next[index] = value
  return next
}

// isOpenPart reports whether a part is the open half of a streaming kind.
//
// ★ It is a function rather than an inline comparison because the union
//   has more members than "text" and "reasoning" now: a part of another
//   kind has no `complete` flag at all, and the comparison has to narrow
//   the union before that flag may be read.
function isOpenPart(p: Part, kind: "text" | "reasoning"): boolean {
  if (kind === "text") return p.kind === "text" && p.complete === false
  return p.kind === "reasoning" && p.complete === false
}

function updateMessage(
  state: ConversationState,
  messageId: string,
  fn: (m: Message) => Message,
): ConversationState {
  const idx = state.messages.findIndex((m) => m.id === messageId)
  if (idx < 0) return state
  return { ...state, messages: replaceAt(state.messages, idx, fn(state.messages[idx]!)) }
}

function ensureMessage(
  state: ConversationState,
  messageId: string,
  role: Role,
): ConversationState {
  if (state.messages.some((m) => m.id === messageId)) return state
  return {
    ...state,
    messages: [...state.messages, { id: messageId, role, parts: [] }],
  }
}

function openPart(
  state: ConversationState,
  messageId: string,
  kind: "text" | "reasoning",
  timestamp?: number,
): { state: ConversationState; index: number } {
  const base =
    state.messages.some((m) => m.id === messageId)
      ? state
      : ensureMessage(state, messageId, "assistant")

  const midx = base.messages.findIndex((m) => m.id === messageId)
  if (midx < 0) return { state: base, index: -1 }

  const parts = base.messages[midx]!.parts
  for (let i = parts.length - 1; i >= 0; i--) {
    const p = parts[i]!
    if (isOpenPart(p, kind)) return { state: base, index: i }
  }

  const fresh: Part =
    kind === "text"
      ? { kind: "text", id: `${messageId}:t${parts.length}`, text: "", complete: false }
      : {
          kind: "reasoning",
          id: `${messageId}:r${parts.length}`,
          text: "",
          complete: false,
          startedAt: timestamp,
        }

  const withPart = updateMessage(base, messageId, (m) => ({ ...m, parts: [...m.parts, fresh] }))
  return { state: withPart, index: withPart.messages[midx]!.parts.length - 1 }
}

function closePart(
  state: ConversationState,
  messageId: string,
  kind: "text" | "reasoning",
  opts?: { interrupted?: boolean; timestamp?: number },
): ConversationState {
  const midx = state.messages.findIndex((m) => m.id === messageId)
  if (midx < 0) return state
  const parts = state.messages[midx]!.parts
  for (let i = parts.length - 1; i >= 0; i--) {
    const p = parts[i]!
    if (!isOpenPart(p, kind)) continue
    if (p.kind === "text") {
      const next: Part = { ...p, complete: true }
      return updateMessage(state, messageId, (m) => ({
        ...m,
        parts: replaceAt(m.parts, i, next),
      }))
    }
    if (p.kind === "reasoning") {
      const next: Part = {
        ...p,
        complete: true,
        ...(opts?.interrupted ? { interrupted: true } : {}),
        ...(opts?.timestamp !== undefined && p.startedAt !== undefined
          ? { elapsedMs: opts.timestamp - p.startedAt }
          : {}),
      }
      return updateMessage(state, messageId, (m) => ({
        ...m,
        parts: replaceAt(m.parts, i, next),
      }))
    }
  }
  return state
}

function appendToOpenPart(
  state: ConversationState,
  messageId: string,
  kind: "text" | "reasoning",
  delta: string,
  timestamp?: number,
): ConversationState {
  const { state: withPart, index } = openPart(state, messageId, kind, timestamp)
  if (index < 0) return state
  return updateMessage(withPart, messageId, (m) => {
    const p = m.parts[index]!
    if (p.kind === "text" && kind === "text") {
      return { ...m, parts: replaceAt(m.parts, index, { ...p, text: p.text + delta }) }
    }
    if (p.kind === "reasoning" && kind === "reasoning") {
      return { ...m, parts: replaceAt(m.parts, index, { ...p, text: p.text + delta }) }
    }
    return m
  })
}

// ── reducer ──────────────────────────────────────────────────────────────

export function reduceEvent(state: ConversationState, event: AgEvent): ConversationState {
  switch (event.type) {
    case "RUN_STARTED":
      // ★ The run id is kept: it is the client's way back to this run if
      //   the connection drops.
      return {
        ...state,
        status: "running",
        runId: (event as RunStartedEvent).runId ?? state.runId,
        interrupts: [],
        error: undefined,
        retry: undefined,
      }

    case "RUN_FINISHED":
      return onRunFinished(state, event)

    case "RUN_ERROR":
      return onRunError(state, event)

    case "TEXT_MESSAGE_START": {
      const e = event as TextMessageStartEvent
      const role = (e.role ?? "assistant") as Role
      const next = ensureMessage(state, e.messageId, role)
      const withAgent = e.name
        ? updateMessage(next, e.messageId, (m) => ({ ...m, agent: e.name }))
        : next
      return { ...withAgent, status: "running" }
    }

    case "TEXT_MESSAGE_CONTENT":
      return appendToOpenPart(state, event.messageId, "text", event.delta ?? "")

    case "TEXT_MESSAGE_END":
      return closePart(state, event.messageId, "text")

    case "REASONING_START":
    case "REASONING_MESSAGE_START":
      return { ...ensureMessage(state, event.messageId, "assistant"), status: "running" }

    case "REASONING_MESSAGE_CONTENT":
      return appendToOpenPart(
        state,
        event.messageId,
        "reasoning",
        event.delta ?? "",
        event.timestamp,
      )

    case "REASONING_MESSAGE_END":
      return closePart(state, event.messageId, "reasoning", { timestamp: event.timestamp })

    case "REASONING_END":
      return closePart(state, event.messageId, "reasoning", { timestamp: event.timestamp })

    case "TOOL_CALL_START":
      return onToolStart(state, event)

    case "TOOL_CALL_ARGS":
      return onToolArgs(state, event)

    case "TOOL_CALL_END":
      return onToolEnd(state, event)

    case "TOOL_CALL_RESULT":
      return onToolResult(state, event)

    case "CUSTOM": {
      if (event.name === "model_retry") {
        const value = event.value as { message?: string } | undefined
        return { ...state, retry: { message: value?.message ?? "retrying" } }
      }
      if (event.name === "context_usage") return onContextUsage(state, event)
      if (event.name === "context_compacted") return onContextCompacted(state, event)
      return state
    }

    default:
      return state
  }
}

function onRunFinished(state: ConversationState, event: RunFinishedEvent): ConversationState {
  const outcome = event.outcome

  const rawInterrupts = outcome?.type === "interrupt" ? (outcome.interrupts ?? []) : []

  if (rawInterrupts.length > 0) {
    const interrupts: Interrupt[] = rawInterrupts.map((i) => ({
      id: i.id,
      reason: i.reason,
      message: i.message,
      toolCallId: i.toolCallId,
      toolName: i.toolName ?? i.metadata?.toolName,
      arguments: i.arguments ?? i.metadata?.arguments,
      requestedByAgent: i.subagentRunId,
    }))
    const closed = closeAllOpenParts(state)
    return { ...closed, status: "awaiting-approval", interrupts, retry: undefined }
  }

  const closed = closeAllOpenParts(state)
  return {
    ...closed,
    status: "idle",
    interrupts: [],
    retry: undefined,
    usage: toUsage(outcome?.result?.usage) ?? closed.usage,
  }
}

// onContextUsage records one agent's context occupancy.
//
// The payload is the whole snapshot for that agent: the server replaces, it
// does not merge, so a dropped event cannot leave a wrong number behind.
function onContextUsage(state: ConversationState, event: CustomEvent): ConversationState {
  const value = event.value as ContextUsageEvent | undefined
  const agent = value?.agent
  if (!agent) return state
  const next: ContextUsage = {
    agent,
    provider: value.provider,
    model: value.model,
    usedTokens: value.used_tokens ?? 0,
    contextWindow: value.context_window ?? 0,
    summarizeAt: value.summarize_at,
    updatedAt: value.updated_at,
  }
  return { ...state, context: { ...state.context, [agent]: next } }
}

// onContextCompacted inserts the compaction boundary into the message list.
//
// The compaction happens mid-turn, so the user is watching when it does: the
// divider goes in at once. A live compaction carries no numbers — they are
// measured against the transcript, which is written when the turn ends —
// while a manual one knows them already.
function onContextCompacted(state: ConversationState, event: CustomEvent): ConversationState {
  const value = event.value as ContextCompactedEvent | undefined
  const at = value?.at ?? new Date().toISOString()
  const agent = value?.agent
  const id = `compaction:${agent ?? ""}:${at}`
  // The same compaction can be announced twice; one boundary is one boundary.
  if (state.messages.some((m) => m.id === id)) return state
  const boundary: Message = {
    id,
    role: "system",
    parts: [],
    agent,
    compaction: {
      superseded: value?.superseded ?? 0,
      savedTokens: value?.savedTokens ?? 0,
      at,
    },
  }
  return { ...state, messages: [...state.messages, boundary] }
}

function onRunError(state: ConversationState, event: RunErrorEvent): ConversationState {
  return {
    ...closeAllOpenParts(state, { interrupted: true }),
    status: "error",
    interrupts: [],
    retry: undefined,
    error: { code: (event as { code?: string }).code ?? "unknown", message: event.message ?? "" },
  }
}

function onToolStart(state: ConversationState, event: ToolCallStartEvent): ConversationState {
  const parent = (event as { parentMessageId?: string }).parentMessageId || `tool:${event.toolCallId}`
  const withMsg = ensureMessage(state, parent, "assistant")
  const part: ToolPart = {
    kind: "tool",
    id: event.toolCallId,
    name: event.toolCallName ?? "",
    argsText: "",
    phase: "streaming",
  }
  return {
    ...updateMessage(withMsg, parent, (m) => ({ ...m, parts: [...m.parts, part] })),
    status: "running",
  }
}

function onToolArgs(state: ConversationState, event: ToolCallArgsEvent): ConversationState {
  return updateToolPart(state, event.toolCallId, (p) => ({
    ...p,
    argsText: p.argsText + deltaOf(event),
  }))
}

function onToolEnd(state: ConversationState, event: ToolCallEndEvent): ConversationState {
  return updateToolPart(state, event.toolCallId, (p) =>
    p.phase === "streaming" ? { ...p, phase: "running" } : p,
  )
}

function onToolResult(state: ConversationState, event: ToolCallResultEvent): ConversationState {
  return updateToolPart(state, event.toolCallId, (p) => ({
    ...p,
    result: event.content ?? "",
    phase: "complete",
  }))
}

function updateToolPart(
  state: ConversationState,
  toolCallId: string,
  fn: (p: ToolPart) => ToolPart,
): ConversationState {
  const midx = state.messages.findIndex((m) => m.parts.some((p) => p.kind === "tool" && p.id === toolCallId))
  if (midx < 0) {
    const parent = `tool:${toolCallId}`
    const withMsg = ensureMessage(state, parent, "assistant")
    const part: ToolPart = { kind: "tool", id: toolCallId, name: "", argsText: "", phase: "running" }
    const withPart = updateMessage(withMsg, parent, (m) => ({ ...m, parts: [...m.parts, fn(part)] }))
    return { ...withPart, status: "running" }
  }
  const parts = state.messages[midx]!.parts
  const pidx = parts.findIndex((p) => p.kind === "tool" && p.id === toolCallId)
  const target = parts[pidx]!
  if (target.kind !== "tool") return state
  return updateMessage(state, state.messages[midx]!.id, (m) => ({
    ...m,
    parts: replaceAt(m.parts, pidx, fn(target)),
  }))
}

export function closeAllOpenParts(
  state: ConversationState,
  opts?: { interrupted?: boolean; timestamp?: number },
): ConversationState {
  let out = state
  for (const m of state.messages) {
    for (const kind of ["text", "reasoning"] as const) {
      const has = m.parts.some((p) => isOpenPart(p, kind))
      if (has) out = closePart(out, m.id, kind, opts)
    }
  }
  return out
}

function toUsage(raw: unknown): Usage | undefined {
  if (!raw || typeof raw !== "object") return undefined
  const u = raw as Record<string, number | undefined>
  const total = u.totalTokens
  const out = {
    promptTokens: u.promptTokens ?? u.inputTokens ?? 0,
    completionTokens: u.completionTokens ?? u.outputTokens ?? 0,
    totalTokens: total ?? (u.promptTokens ?? u.inputTokens ?? 0) + (u.completionTokens ?? u.outputTokens ?? 0),
    reasoningTokens: u.reasoningTokens,
    cachedTokens: u.cachedTokens ?? u.cachedInputTokens,
  }
  return out.totalTokens || out.promptTokens || out.completionTokens ? out : undefined
}

function deltaOf(event: unknown): string {
  const e = event as { delta?: unknown; content?: unknown }
  if (typeof e.delta === "string") return e.delta
  if (typeof e.content === "string") return e.content
  return ""
}

export function reduceEvents(
  initial: ConversationState,
  events: readonly AgEvent[],
): ConversationState {
  let out = initial
  for (const e of events) out = reduceEvent(out, e)
  return out
}
