/**
 * 状态层：一个 zustand store 管住全部会话与当前会话。
 *
 * ★ 为什么是 store 而不是 Context + useReducer：
 *   流式下每个 delta 都 setState。Context 方案里侧栏、消息流、composer
 *   会一起重渲染，除非手工 memo 每一层 —— 那正是要避免的脆弱耦合。
 *   store 的选择器订阅让「侧栏只在会话表变化时重渲染」变成一行声明。
 *
 * ★ 为什么不用 TanStack Query：
 *   /v2/sessions 列表确实像 query，但流式 run 不是。Query 缓存与流式 store
 *   并存会产生两个事实源 —— 上一版一半的 bug 出在这里。统一一个 store。
 *
 * ★ 会话只有一个 id。
 *   上一版每个会话前后有两个：本地占位 slot 与服务端 sessionId，
 *   于是有了 lib/session-id.ts 那套注册表去收敛，收敛失败时 history 打
 *   404、历史整个空掉。现在占位这个概念不存在了。
 */

/**
 * 门面：对外只有这一个入口。
 *
 * ★ 拆文件不是为了好看，是为了让「状态长什么样」「怎么改」「怎么读」
 *   三件事各自在一个地方：原来它们全在一个 972 行的文件里，读者要读完
 *   455 行的 create() 字面量才知道状态有哪些。
 *
 * ★ 组件仍然从 "@/lib/store" 导入 —— 门面把三个子模块重新导出，
 *   于是调用点一行都没改。
 */
export { useStore } from "@/lib/store/actions"
export { MAIN_AGENT, type Attachment, type SessionEntry, type StoreState } from "@/lib/store/state"
export { seedFromHistory } from "@/lib/store/history"
export {
  EMPTY_ATTACHMENTS,
  EMPTY_INTERRUPTS,
  EMPTY_MESSAGES,
  selectActiveAgentId,
  selectActiveError,
  selectActiveLoading,
  selectActiveRetry,
  selectActiveUsage,
  selectAttachments,
  selectConversation,
  selectInterrupts,
  selectIsEmptyView,
  selectMessages,
  selectSessionList,
  selectStatus,
} from "@/lib/store/selectors"
