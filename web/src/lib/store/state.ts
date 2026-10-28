/**
 * 状态的形状：会话表、附件、视图状态，以及 store 暴露的动作。
 *
 * ★ 这里只有**形状**，没有行为 —— 行为在 actions.ts，派生值在 selectors.ts。
 *
 *   拆开的原因：原来这三条全在一个 972 行的文件里，而它们是三种不同的
 *   东西 —— 「状态长什么样」「怎么改」「怎么读」。混在一起时，读者要
 *   读完 455 行的 create() 字面量才能知道有哪些状态。
 */
import type { ThreadMeta } from "@/lib/api"
import type { AgEvent } from "@/lib/events"
import type { ConversationState, Interrupt } from "@/lib/types"

/**
 * 一个已上传的附件。
 *
 * ★ 它落在会话的 workspace 目录里，**不在消息体里**。
 *   模型通过工具（read_file / ls）读那个目录 —— 见 openheronrt/loop.go 把
 *   `sess.Workspace()` 喂进 prompt 的那处。所以发送时不需要携带附件，
 *   只要它们在轮次开始前就已落盘。
 */
export interface Attachment {
  name: string
  path: string
  size: number
}

export interface SessionEntry {
  meta: ThreadMeta
  /** 懒加载：只有打开过的会话才有对话状态。 */
  convo?: ConversationState
  loaded: boolean
}

export interface StoreState {
  sessions: Record<string, SessionEntry>
  order: string[]
  /**
   * 待发送的附件，按会话 id 存。
   *
   * ★ 刻意不放进 ConversationState —— 那个类型是 reducer 从 AG-UI 事件
   *   纯粹推导出来的，混进非事件来源的字段会让「状态等于事件回放」这条
   *   不变式破掉。
   */
  attachments: Record<string, Attachment[]>
  uploading: boolean
  /** null = 欢迎界面。侧栏不高亮任何一条，也不会发任何会话请求。 */
  activeId: string | null
  /**
   * 欢迎页选中的 agent。下一轮 `POST /v2/run` 带上它，RUN_STARTED 后清空。
   *
   * ★ 只在「还没有会话」时有意义：已有会话的根 agent 在 threadMeta.agentId，
   *   两处都存会让「当前是哪个 agent」出现两个事实源。
   */
  pendingAgentId: string | null
  loadingList: boolean
  listError?: string
  /**
   * 消息列表的 agent 过滤。null = 全部。
   *
   * ★ 它是**视图状态**，不是对话状态 —— 所以放在 store 顶层而不是
   *   ConversationState 里：ConversationState 是 reducer 从 AG-UI 事件
   *   纯粹推导出来的，混进非事件来源的字段会让「状态等于事件回放」这条
   *   不变式破掉。
   */
  agentFilter: string | null
  setAgentFilter(agent: string | null): void

  // ── actions ────────────────────────────────────────────────────────────
  refreshList(): Promise<void>
  openSession(id: string): Promise<void>
  startNewThread(): void
  renameSession(id: string, title: string): Promise<void>
  deleteSession(id: string): Promise<void>
  /** 切换当前会话（或欢迎页暂存）的根 agent。失败时抛给调用方显示。 */
  switchAgent(agentId: string): Promise<void>

  send(text: string): Promise<void>
  addFiles(files: File[]): Promise<void>
  removeFile(sessionId: string, name: string): Promise<void>
  answer(interrupts: Interrupt[], approved: boolean, reason?: string): Promise<void>
  stop(): Promise<void>
  applyEvent(id: string, ev: AgEvent): void
  loadError(id: string, message: string): void
}

/** 没有切换过的会话与欢迎页都落到 main。 */
export const MAIN_AGENT = "main"
