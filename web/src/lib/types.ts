/**
 * 对话状态的形状。
 *
 * ★ 这个文件里**没有任何框架概念** —— 没有 adapter、没有 runtime、
 *   没有 thread、没有 optimistic slot。全部是纯数据。
 *
 *   代价是所有正确性都变成可以用 bun test 证明的东西：对一遍事件序列，
 *   比对输出。不要在这里引入「生命周期」「回调时序」这类只能靠理解
 *   框架才能确认的概念 —— 那正是上一版改不动的根源。
 *
 * 形状与后端的对应关系：
 *   · Part      ← AG-UI 的 messageId + START/CONTENT/END 三段
 *   · Interrupt ← agtransport.interruptsFrom 构造的 types.Interrupt
 *   · TurnStatus← 后端 threadState（idle / running / waiting_approval）
 */

export type Role = "user" | "assistant" | "system" | "developer" | "tool"

/** 一段正文。complete 之前表示还在流式接收。 */
export interface TextPart {
  kind: "text"
  id: string
  text: string
  complete: boolean
}

/**
 * 一段模型思考。
 *
 * ★ 此前整条链路都在丢弃它：eino 把推理放在 MultiContent 的 reasoning
 *   part 里，agtransport 只取 AssistantDeltaText。Phase 1 补上之后前端
 *   第一次真的能看到东西。
 */
export interface ReasoningPart {
  kind: "reasoning"
  id: string
  text: string
  complete: boolean
  /**
   * 是否**被掐断**（用户按了停止，或那一轮报错）。
   *
   * ★ 与 complete 正交：「不再接收内容」不等于「想完了」。
   */
  interrupted?: boolean
  /**
   * 思维链的**用时**（毫秒）。
   *
   * ★ undefined = **不知道**（没思维链，或没记上）—— 不是 0。
   *
   * ★ 实时流里由归约器在**收尾那一刻**算出来（服务端事件的时间戳之差）；
   *   历史回放里直接取后端存进消息 Extra 的那个值（api/v2_convert.go 带给前端）。
   */
  elapsedMs?: number
  /** 开始时刻（服务端事件的时间戳，epoch ms）。 */
  startedAt?: number
}

/**
 * 一次工具调用。
 *
 * argsText 保持原始 JSON 字符串而不 parse：用户研究 agent 行为时需要看到
 * 真实参数，任何自作聪明的解析或摘要都会让观察失真。
 */
export interface ToolPart {
  kind: "tool"
  /** 协议侧的 toolCallId，全局唯一 —— TOOL_CALL_RESULT 按它回填。 */
  id: string
  name: string
  argsText: string
  /** 没有 result 就说明还在跑或被中断。 */
  result?: string
  isError?: boolean
  /** args 收完 = streaming；拿到 result = complete。 */
  phase: "streaming" | "running" | "complete"
  /** 从服务端历史恢复（而非本轮流式产生）。历史卡片默认折叠。 */
  fromHistory?: boolean
}

export type Part = TextPart | ReasoningPart | ToolPart

export interface Message {
  id: string
  role: Role
  parts: Part[]
  /**
   * 产出这条消息的 agent 名。
   *
   * ★ 多 agent 模式下的归属标记 —— 没有它，「task 到底干了什么」只能
   *   靠顺序猜，而多 agent 的输出是**交错**的。
   *
   * ★ 两个来源合成一个字段：实时事件的 TEXT_MESSAGE_START.name 与历史
   *   回放的 AG-UI message.name（后端 api/v2_convert.go 填的）。界面不
   *   需要知道这条是刚发生的还是刷新后恢复的。
   *
   * ★ 空 = 不知道是谁（user 消息、加这个标记之前的老会话）。
   */
  agent?: string
}

/** 一个待答复的人审点。 */
export interface Interrupt {
  id: string
  reason: string
  message?: string
  toolCallId?: string
  /** 要执行的工具名。实时事件来自 interrupt.metadata，历史回放来自 pendingView。 */
  toolName?: string
  /** 结构化参数（实时是 JSON 字符串，历史是对象）；审批卡只做单行摘要。 */
  arguments?: unknown
  /**
   * 发起这次审批的 agent 名（子 agent 才有）。
   *
   * ★ 两个来源：实时事件里的 subagentRunId，历史回放里的
   *   pendingApproval.subagentRunId / requestedByAgent。合成一个字段，
   *   界面就不必知道「这次是刚发生的还是刷新后恢复的」。
   */
  requestedByAgent?: string
}

/**
 * TurnStatus 不直接用后端的三个值，多一个 awaiting-approval 的独立态。
 *
 * ★ 为什么不用 message 上挂 metadata 表达「待审批」：
 *   上一版就是这么做的（interrupt 存在最后一条 assistant 消息的
 *   metadata.custom.agui.interrupts 上），而 `findRequiresActionAssistant`
 *   用 findLast 找最后一条 —— 消息顺序一变、或者中间插入一条 assistant
 *   消息，中断就再也找不到了。状态该放在状态里，不该寄生在消息上。
 */
export type TurnStatus = "idle" | "running" | "awaiting-approval" | "error"

export interface RunError {
  code: string
  message: string
}

export interface Usage {
  promptTokens: number
  completionTokens: number
  totalTokens: number
  reasoningTokens?: number
  cachedTokens?: number
  /**
   * 用量按 agent 分账：谁花的 token。
   *
   * ★ 它是**整个会话**的分账，不随消息过滤而变 —— 用量是会话级的事实，
   *   过滤只影响「看哪些消息」。
   */
  byAgent?: Record<string, Usage>
}

/**
 * ConversationState 是 reducer 的全部输入与输出。
 *
 * ★ 注意它**没有** threadId / runId 这类"当前指向谁"的字段 ——
 *   那是 store 的事，不是对话内容的事。reducer 只管「这一段对话长什么样」。
 */
export interface ConversationState {
  messages: Message[]
  status: TurnStatus
  interrupts: Interrupt[]
  error?: RunError
  usage?: Usage
  /** 模型重试提示，短暂显示后清掉。 */
  retry?: { message: string }
}

export function emptyConversation(): ConversationState {
  return { messages: [], status: "idle", interrupts: [] }
}
