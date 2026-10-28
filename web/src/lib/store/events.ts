/**
 * 事件推进：把一个 AG-UI 事件写进某个会话条目。
 *
 * ★ 与 actions.ts 分开是因为它是**纯状态转移**（给定 set/get 与事件，
 *   算出下一个状态），没有异步、没有网络 —— 这让它可以被单独读懂。
 */
import { reduceEvent, type AgEvent } from "@/lib/events"
import { writeActiveThreadId } from "@/lib/active-thread"
import { emptyConversation } from "@/lib/types"

import type { SessionEntry, StoreState } from "./state"

export type Set = (partial: Partial<StoreState> | ((s: StoreState) => Partial<StoreState>)) => void
export type Get = () => StoreState

/**
 * currentRunKey 是**这一轮事件应该写进哪个会话条目**。
 *
 * ★ 必须记在模块级，而不是跟着事件流走：
 *   新会话先落在 `pending:<时间戳>` 占位上，RUN_STARTED 到达后会被换成
 *   服务端的真实 id。换了之后，后续每一个事件仍然带着最初的 localKey
 *   回调进来 —— 若直接拿它去写，条目已经不存在了，applyTo 找不到就静默
 *   返回，结果是「只剩本地那条 user 消息，回复永远不来」。
 */
let currentRunKey: string | null = null

/**
 * handleEvent 把一个事件推进到本地槽位。
 *
 * ★ localKey 在拿到服务端 id 之前是 `pending:<时间戳>`。RUN_STARTED 里的
 *   threadId 到达时，这里把占位条目换成真实会话 —— 一次映射，没有第二套
 *   注册表需要在别处同步。
 */
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

/**
 * promote 把本地占位条目换成服务端给的真实会话。
 *
 * ★ agentId 是服务端定的根：它可能与占位时猜的那个不同（没选过时前端
 *   按 main 摆着，服务端给的才算数）。不换的话，底栏显示的根与实际跑的
 *   可能不是同一个 agent。
 */
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
    // ★ 服务端已经收下并落了盘，暂存值再留着只会在下一个新会话里
    //   悄悄沿用 —— 而用户以为那是一次性选择。
    pendingAgentId: null,
  })
  writeActiveThreadId(serverId)
  // ★ 后续事件要写进新键 —— 不更新的话它们会写到已经不存在的占位上。
  currentRunKey = serverId
}

/** withoutKey 返回去掉某个键的副本 —— 用 delete 会让 Record 出现 undefined 值。 */
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

/**
 * currentRunKey 的读写口。
 *
 * ★ 它是模块级可变状态，而写它的地方在 actions.ts（发一轮的那几处）、
 *   读它的地方在这里（事件推进）。跨模块共享可变状态不能靠「导出一个
 *   let」（导入方只能读不能写），所以给它一对函数 ——
 *   写入口只有一个，读者一眼知道谁在改它。
 */
export function getCurrentRunKey(): string | null {
  return currentRunKey
}

export function setCurrentRunKey(key: string | null): void {
  currentRunKey = key
}
