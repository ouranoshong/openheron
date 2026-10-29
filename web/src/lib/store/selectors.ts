import {
  emptyConversation,
  type ContextUsage,
  type ConversationState,
  type Interrupt,
  type Message,
  type TurnStatus,
  type Usage,
} from "@/lib/types"

import { MAIN_AGENT, type Attachment, type SessionEntry, type StoreState } from "./state"

export function selectActiveAgentId(s: StoreState): string {
  if (s.activeId) {
    return s.sessions[s.activeId]?.meta.agentId || MAIN_AGENT
  }
  return s.pendingAgentId ?? MAIN_AGENT
}

// ── selectors ─────────────────────────────────────────────────────────────

// ── selectors ─────────────────────────────────────────────────────────────

export const EMPTY_MESSAGES: Message[] = []
export const EMPTY_INTERRUPTS: Interrupt[] = []
const EMPTY_CONVERSATION: ConversationState = emptyConversation()

export function selectConversation(s: StoreState): ConversationState {
  if (!s.activeId) return EMPTY_CONVERSATION
  return s.sessions[s.activeId]?.convo ?? EMPTY_CONVERSATION
}

export function selectStatus(s: StoreState): TurnStatus {
  return selectConversation(s).status
}

export function selectMessages(s: StoreState): Message[] {
  return selectConversation(s).messages || EMPTY_MESSAGES
}

export function selectInterrupts(s: StoreState): Interrupt[] {
  return selectConversation(s).interrupts || EMPTY_INTERRUPTS
}

export const EMPTY_ATTACHMENTS: Attachment[] = []

export function selectActiveLoading(s: StoreState): boolean {
  if (!s.activeId) return false
  const entry = s.sessions[s.activeId]
  return !!entry && !entry.loaded
}

export function selectIsEmptyView(s: StoreState): boolean {
  if (!s.activeId) return true
  const entry = s.sessions[s.activeId]
  if (!entry) return true
  if (!entry.loaded) return false
  return (entry.convo?.messages.length ?? 0) === 0 && !entry.convo?.error
}

export function selectAttachments(s: StoreState): Attachment[] {
  const id = s.activeId
  return (id ? s.attachments[id] : undefined) ?? EMPTY_ATTACHMENTS
}

export function selectSessionList(s: StoreState): SessionEntry[] {
  return s.order.map((id) => s.sessions[id]!).filter(Boolean)
}

export function selectActiveError(s: StoreState) {
  return s.activeId ? s.sessions[s.activeId]?.convo?.error : undefined
}

export function selectActiveRetry(s: StoreState) {
  return s.activeId ? s.sessions[s.activeId]?.convo?.retry : undefined
}

export function selectActiveUsage(s: StoreState): Usage | undefined {
  return s.activeId ? s.sessions[s.activeId]?.convo?.usage : undefined
}

// selectActiveContext returns the per-agent context occupancy of the active
// session.
//
// It hands back the map the store already holds, so the reference stays
// stable: a new array or object here would make every render look like a
// change (see AGENTS.md on zustand selectors).
export function selectActiveContext(s: StoreState): Record<string, ContextUsage> | undefined {
  return s.activeId ? s.sessions[s.activeId]?.convo?.context : undefined
}
