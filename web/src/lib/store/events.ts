import { reduceEvent, type AgEvent } from "@/lib/events"
import { writeActiveThreadId } from "@/lib/active-thread"
import { emptyConversation } from "@/lib/types"

import type { SessionEntry, StoreState } from "./state"

export type Set = (partial: Partial<StoreState> | ((s: StoreState) => Partial<StoreState>)) => void
export type Get = () => StoreState

let currentRunKey: string | null = null

export function handleEvent(set: Set, get: Get, localKey: string, ev: AgEvent): void {
  const key = currentRunKey ?? localKey
  if (ev.type === "RUN_STARTED") {
    const serverId = (ev as { threadId?: string }).threadId
    if (serverId && serverId !== key && key.startsWith("pending:")) {
      promote(set, get, key, serverId)
      applyTo(set, currentRunKey!, ev)
      return
    }
  }
  applyTo(set, key, ev)
}

export function promote(set: Set, get: Get, fromKey: string, serverId: string, agentId?: string): void {
  const s = get()
  const from = s.sessions[fromKey]
  if (!from) return
  set({
    sessions: {
      ...withoutKey(s.sessions, fromKey),
      [serverId]: {
        meta: { ...from.meta, threadId: serverId, ...(agentId ? { agentId } : {}) },
        convo: from.convo,
        loaded: from.loaded,
      },
    },
    order: s.order.map((x) => (x === fromKey ? serverId : x)),
    activeId: s.activeId === null ? serverId : s.activeId,
    pendingAgentId: null,
  })
  writeActiveThreadId(serverId)
  currentRunKey = serverId
}

export function withoutKey(src: Record<string, SessionEntry>, key: string): Record<string, SessionEntry> {
  const out: Record<string, SessionEntry> = {}
  for (const k of Object.keys(src)) if (k !== key) out[k] = src[k]!
  return out
}

export function applyTo(set: Set, key: string, ev: AgEvent): void {
  set((s) => {
    const entry = s.sessions[key]
    if (!entry) return {}
    const convo = reduceEvent(entry.convo ?? emptyConversation(), ev)
    return { sessions: { ...s.sessions, [key]: { ...entry, convo } } }
  })
}

export function getCurrentRunKey(): string | null {
  return currentRunKey
}

export function setCurrentRunKey(key: string | null): void {
  currentRunKey = key
}
