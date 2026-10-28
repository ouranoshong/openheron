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
  addFiles(files: File[]): Promise<void>
  removeFile(sessionId: string, name: string): Promise<void>
  answer(interrupts: Interrupt[], approved: boolean, reason?: string): Promise<void>
  stop(): Promise<void>
  applyEvent(id: string, ev: AgEvent): void
  loadError(id: string, message: string): void
}

export const MAIN_AGENT = "main"
