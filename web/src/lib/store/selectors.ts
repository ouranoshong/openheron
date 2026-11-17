import type { OperationView } from "@/lib/api"
import { authorizationMode, type AuthorizationMode } from "@/lib/authorization"
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

// selectNotice is the transient line about work the server is doing.
export function selectNotice(s: StoreState): string | undefined {
  return selectConversation(s).notice
}

// selectBusyElsewhere says the session has a live turn this surface does
// not own.
//
// ★ It returns a primitive, not a fresh object: a selector that built a
//   new value on every call would make Object.is report "changed" every
//   time, and the store would re-render forever.
export function selectBusyElsewhere(s: StoreState): boolean {
  return selectConversation(s).busy === true
}

export function selectMessages(s: StoreState): Message[] {
  return selectConversation(s).messages || EMPTY_MESSAGES
}

export function selectInterrupts(s: StoreState): Interrupt[] {
  return selectConversation(s).interrupts || EMPTY_INTERRUPTS
}

export const EMPTY_ATTACHMENTS: Attachment[] = []

// EMPTY_OPERATIONS is the answer for a session whose log has not been read.
//
// ★ It is a **module-level constant**: a selector that built a new array on
//   every call would make Object.is report "changed" every time, and the
//   store would re-render forever (AGENTS.md on zustand selectors).
export const EMPTY_OPERATIONS: OperationView[] = []

// selectOperations is the operation log of one session.
//
// ★ It hands back the array the store already holds, so the reference stays
//   stable — and a session without a log answers with the shared empty
//   constant, not with a fresh array.
export function selectOperations(s: StoreState, id: string): OperationView[] {
  return s.sessions[id]?.operations ?? EMPTY_OPERATIONS
}

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

// selectActiveProjectId answers "which project is the composer working in".
//
// ★ It returns a **string**, not an object: a selector that built a new
//   value on every call would make Object.is report "changed" every time,
//   and the store would re-render forever.
//
// ★ The session's own project comes first — a conversation belongs to the
//   project it was born in. ★ A thread that does not exist yet falls back
//   to the composer's own choice, and **null** there means "no project" —
//   the default project root, which is what a new conversation starts on.
export function selectActiveProjectId(s: StoreState): string {
  const id = s.activeId ? s.sessions[s.activeId]?.meta.projectId : undefined
  return id || s.activeProjectId || ""
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

// selectAuthorizationMode is the authorization mode of the session on screen.
//
// ★ It returns a **primitive**, not a fresh object: a selector that built a
// new value on every call would make Object.is report "changed" every time,
// and the store would re-render forever (AGENTS.md on zustand selectors).
//
// ★ A session that does not exist yet has no mode, and the default is what
//   the server gives it when it creates it. ★ The **staged** mode is what
//   the user picked on the welcome page: it is the mode the conversation is
//   about to be born in, and it travels with the creation request.
export function selectAuthorizationMode(s: StoreState): AuthorizationMode {
  if (!s.activeId) return s.pendingAuthorizationMode ?? "default"
  return authorizationMode(s.sessions[s.activeId]?.meta.authorizationMode)
}

// selectAuthorizationConfirmed says whether this session has already
// confirmed full authorization.
//
// ★ It is the **server's** fact and not the client's memory: the server
//   remembers it per session, so a second surface must not ask a second
//   time. ★ Without a session there is no fact yet — only the client's word
//   that the person already said yes, and it goes with the session that is
//   about to be born.
export function selectAuthorizationConfirmed(s: StoreState): boolean {
  if (!s.activeId) return s.pendingAuthorizationConfirmed
  return s.sessions[s.activeId]?.meta.authorizationConfirmed === true
}
