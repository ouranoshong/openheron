import type { ThreadMeta } from "@/lib/api"
import type { AgEvent } from "@/lib/events"
import type { ConversationState, Interrupt } from "@/lib/types"

export interface Attachment {
  name: string
  path: string
  size: number
}

export interface SessionEntry {
  meta: ThreadMeta
  convo?: ConversationState
  loaded: boolean
}

export interface StoreState {
  sessions: Record<string, SessionEntry>
  order: string[]
  attachments: Record<string, Attachment[]>
  uploading: boolean
  activeId: string | null
  pendingAgentId: string | null
  loadingList: boolean
  listError?: string
  agentFilter: string | null
  setAgentFilter(agent: string | null): void

  // ── actions ────────────────────────────────────────────────────────────
  refreshList(): Promise<void>
  openSession(id: string): Promise<void>
  startNewThread(): void
  renameSession(id: string, title: string): Promise<void>
  deleteSession(id: string): Promise<void>
  switchAgent(agentId: string): Promise<void>

  send(text: string): Promise<void>
  // reattach comes back to a run after the connection dropped. The run
  // outlives the connection, so a broken stream is not a lost turn.
  reattach(id: string, message: string): Promise<void>
  // reloadHistory re-reads the session's transcript — what a client does
  // when it learns a run is over.
  reloadHistory(id: string): Promise<void>
  // scanBusy decides whether the session is busy, from the run ledger. The
  // busy flag may only be cleared on the word of a scan.
  scanBusy(id: string): Promise<void>
  addFiles(files: File[]): Promise<void>
  removeFile(sessionId: string, name: string): Promise<void>
  answer(interrupts: Interrupt[], approved: boolean, reason?: string): Promise<void>
  stop(): Promise<void>
  compact(): Promise<void>
  clearNotice(id: string): void
  applyEvent(id: string, ev: AgEvent): void
  loadError(id: string, message: string): void
  // markBusy records that the session has a live turn this surface does not
  // own, and rolls back the message the server refused.
  markBusy(id: string, message: string): void
}

export const MAIN_AGENT = "main"
