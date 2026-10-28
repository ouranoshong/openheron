/**
 * 选择器：从状态里读出视图要用的值。
 *
 * ★ 每一条都只返回**引用稳定**的值 —— 见下面的说明。
 */
import { emptyConversation, type ConversationState, type Interrupt, type Message, type TurnStatus, type Usage } from "@/lib/types"

import { MAIN_AGENT, type Attachment, type SessionEntry, type StoreState } from "./state"

/**
 * 当前活动 agent。
 *
 * ★ 已有会话读 threadMeta.agentId（服务端是权威），欢迎页读暂存值。
 *   两处不会同时生效 —— pending 只对「还没有会话」有意义。
 */
export function selectActiveAgentId(s: StoreState): string {
  if (s.activeId) {
    return s.sessions[s.activeId]?.meta.agentId || MAIN_AGENT
  }
  return s.pendingAgentId ?? MAIN_AGENT
}

// ── selectors ─────────────────────────────────────────────────────────────

// ── selectors ─────────────────────────────────────────────────────────────
//
// ★ 每一条都只返回**引用稳定**的值。
//
//   zustand 默认用 Object.is 比较快照。selector 每次调用都造新数组/新对象
//   （`order.map(...)`、`?? []`、`emptyConversation()`）的话，比较永远不相等
//   → 每次渲染都触发更新 → React 报 "Maximum update depth exceeded"，
//   整页白屏。
//
//   修法不是「加 useCallback」，而是让返回值本身稳定：模块级空数组常量，
//   或者由调用方用 useShallow 比较。

/** 模块级空常量。绝不在 selector 里写 `?? []`。 */
export const EMPTY_MESSAGES: Message[] = []
export const EMPTY_INTERRUPTS: Interrupt[] = []
const EMPTY_CONVERSATION: ConversationState = emptyConversation()

/** 当前会话的对话状态；欢迎界面返回同一个空对象。 */
export function selectConversation(s: StoreState): ConversationState {
  if (!s.activeId) return EMPTY_CONVERSATION
  return s.sessions[s.activeId]?.convo ?? EMPTY_CONVERSATION
}

export function selectStatus(s: StoreState): TurnStatus {
  return selectConversation(s).status
}

/**
 * selectMessages 返回的是 store 里那个数组的引用，未改动时始终相等。
 *
 * ★ 调用方必须再用 useShallow 包一层（组件里已经这么做），因为返回类型
 *   是数组 —— zustand 的 Object.is 只比引用，而这里引用其实已经稳定了，
 *   useShallow 是为了防御「将来有人把 map 搬进这个 selector」。
 */
export function selectMessages(s: StoreState): Message[] {
  return selectConversation(s).messages || EMPTY_MESSAGES
}

export function selectInterrupts(s: StoreState): Interrupt[] {
  return selectConversation(s).interrupts || EMPTY_INTERRUPTS
}

/** 空附件列表的模块级常量 —— selector 里绝不能写 `?? []`（见 §5.2）。 */
export const EMPTY_ATTACHMENTS: Attachment[] = []

/**
 * selectActiveLoading —— 当前会话已选中但历史还没到。
 *
 * ★ 与 `messages.length === 0` 分开：空白可能是「加载中」，也可能是
 *   「会话真的没有消息」。前者要骨架，后者才是欢迎区。
 */
export function selectActiveLoading(s: StoreState): boolean {
  if (!s.activeId) return false
  const entry = s.sessions[s.activeId]
  return !!entry && !entry.loaded
}

/**
 * selectIsEmptyView —— 界面是否处于「还没有对话」的状态。
 *
 * ★ 两个条件缺一不可，只看 messages.length === 0 会闪：
 *   打开一个**正在加载**历史的长会话时，会话已选中但 convo 还没到 ——
 *   那一刻 messages 是空的。若据此切到居中布局，历史一到达就再切回去，
 *   输入框会跟着跳一下。
 *
 *   所以要么根本没有活动会话（欢迎界面），要么会话已加载完而确实是空的
 *   （新建了一个会话还没发过消息）。
 */
export function selectIsEmptyView(s: StoreState): boolean {
  if (!s.activeId) return true
  const entry = s.sessions[s.activeId]
  if (!entry) return true
  if (!entry.loaded) return false
  return (entry.convo?.messages.length ?? 0) === 0 && !entry.convo?.error
}

/** 当前会话的待发送附件；欢迎界面返回同一个空数组。 */
export function selectAttachments(s: StoreState): Attachment[] {
  const id = s.activeId
  return (id ? s.attachments[id] : undefined) ?? EMPTY_ATTACHMENTS
}

/**
 * selectSessionList 返回新数组，**必须**配 useShallow 使用。
 *
 * 见 sidebar.tsx：`useStore(useShallow(selectSessionList))`。
 */
export function selectSessionList(s: StoreState): SessionEntry[] {
  return s.order.map((id) => s.sessions[id]!).filter(Boolean)
}

/** 当前会话的 usage / error / retry，都是标量或稳定引用。 */
export function selectActiveError(s: StoreState) {
  return s.activeId ? s.sessions[s.activeId]?.convo?.error : undefined
}

export function selectActiveRetry(s: StoreState) {
  return s.activeId ? s.sessions[s.activeId]?.convo?.retry : undefined
}

/**
 * 当前会话的 usage（含 byAgent 分账）。
 *
 * ★ 引用稳定：它就是 convo.usage 那个对象，convo 不被替换就一直相等。
 */
export function selectActiveUsage(s: StoreState): Usage | undefined {
  return s.activeId ? s.sessions[s.activeId]?.convo?.usage : undefined
}
