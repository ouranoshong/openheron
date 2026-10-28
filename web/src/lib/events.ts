/**
 * AG-UI 事件 → 对话状态的**纯函数** reducer。
 *
 * ★ 本文件最重要的一条纪律：它必须是纯函数。
 *
 *   `[event, state] → state`，不碰 React、不碰网络、不读时钟、不改入参。
 *   于是正确性可以由 bun test 证明 —— 喂一串事件，比对输出。
 *   上一版的每一处 bug 都是「对不对取决于回调在什么时刻触发」，
 *   那种东西没法用断言钉住，只能靠人肉点页面。
 *
 * ★ part 的身份是 `(messageId, kind)`，不是 messageId 单独决定。
 *
 *   后端在同一条 assistant 消息里交替发 reasoning 与正文（Phase 1 的
 *   emitReasoningStart/emitTextStart 共用 msgID 就是这个原因）。如果按
 *   messageId 找「当前 part」，两段会互相覆盖。
 *
 * ★ 找不到 START 时不报错，而是就地补一个。
 *
 *   流可能从中间接上（切换会话时先渲染历史、随后才收到增量），也可能
 *   收到 CONTENT 却没有对应的 START。让它抛错等于把网络抖动变成白屏；
 *   补一个 open part 则自然收敛。
 */

import type {
  ConversationState,
  Interrupt,
  Usage,
  Message,
  Part,
  Role,
  ToolPart,
} from "./types"

// ── AG-UI 事件类型 ───────────────────────────────────────────────────────

/**
 * 后端实际发出的那几种事件，字段与 agtransport 的 emit* 一一对应。
 *
 * ★ 为什么不 import @ag-ui/core 的类型：
 *
 *   1. 它是 @ag-ui/client 的传递依赖，而那个包已经被删了 —— 前端不该为了
 *      一份类型声明而依赖整个协议 SDK。
 *   2. 官方类型有 38 种事件、我们只用 9 种。union 写全了会让 switch 的
 *      `default` 分支变成「理论上不可达」，于是新增的事件类型会**静默**
 *      落进 default 原样返回 —— 表现是「后端发了新事件，前端什么都不做」。
 *      这里只声明真正处理的那几种，配合下面的 exhaustiveness 断言，
 *      后端加一种事件就会在编译期被挡下。
 *
 * 字段名保持与后端一致（camelCase），不要在这里"翻译"。
 */

export interface RunStartedEvent {
  type: "RUN_STARTED"
  threadId?: string
  runId?: string
}

export interface TextMessageStartEvent {
  type: "TEXT_MESSAGE_START"
  messageId: string
  role?: string
  /**
   * 产出这条消息的 agent 名（AG-UI 的「发送者名字」）。
   *
   * ★ 历史回放走的是另一个字段（AG-UI message.name），但后端把两者
   *   填成了同一个值 —— 前端因此只需要认这一个概念。
   */
  name?: string
}

export interface TextMessageContentEvent {
  type: "TEXT_MESSAGE_CONTENT"
  messageId: string
  delta?: string
}

export interface TextMessageEndEvent {
  type: "TEXT_MESSAGE_END"
  messageId: string
}

export interface ToolCallStartEvent {
  type: "TOOL_CALL_START"
  toolCallId: string
  toolCallName?: string
  parentMessageId?: string
}

export interface ToolCallArgsEvent {
  type: "TOOL_CALL_ARGS"
  toolCallId: string
  delta?: string
}

export interface ToolCallEndEvent {
  type: "TOOL_CALL_END"
  toolCallId: string
}

export interface ToolCallResultEvent {
  type: "TOOL_CALL_RESULT"
  /**
   * 结果所属的**消息** id，与 toolCallId 不是一回事。
   *
   * ★ 记下来是因为 fixture 类型化时它冒出来了：agtransport 的
   *   emitToolResult 传的是 uuid.NewString()，不是 callID。reducer 用
   *   toolCallId 回填就够（工具调用可能挂在别的 parentMessageId 上），
   *   但字段确实存在，后端哪天开始复用它时不会因为类型缺字段而炸。
   */
  messageId?: string
  toolCallId: string
  content?: string
  role?: string
}

/**
 * 思维链的事件带**服务端的时间戳**（epoch ms）。
 *
 * ★ 用它算「想了多久」，而不是客户端的 Date.now()：
 *   - 服务端知道模型**真的**在想的那段时间（客户端只知道它收到事件的时间）；
 *   - 归约器因此保持**纯函数**（不读时钟），单测可复现；
 *   - 组件的渲染里也就没有「 impure call」。
 */
export interface ReasoningStartEvent {
  type: "REASONING_START" | "REASONING_MESSAGE_START"
  messageId: string
  role?: string
  timestamp?: number
}

export interface ReasoningMessageContentEvent {
  type: "REASONING_MESSAGE_CONTENT"
  messageId: string
  delta?: string
  timestamp?: number
}

/** REASONING_END / REASONING_MESSAGE_END 共用。 */
export interface ReasoningEndEvent {
  type: "REASONING_END" | "REASONING_MESSAGE_END"
  messageId: string
  timestamp?: number
}

export interface RawInterruptEvent {
  id: string
  reason: string
  message?: string
  toolCallId?: string
  /** 非标准字段，兼容旧假后端；真后端放在 metadata 里。 */
  toolName?: string
  arguments?: unknown
  metadata?: {
    toolName?: string
    arguments?: unknown
  }
  /**
   * 子 agent 名。根 agent 的中断不带这个字段（见 agtransport 的归属逻辑）。
   */
  subagentRunId?: string
}

/** usage 两种字段名都认，见 toUsage 的注释。 */
export interface UsageEvent {
  inputTokens?: number
  outputTokens?: number
  promptTokens?: number
  completionTokens?: number
  totalTokens?: number
  reasoningTokens?: number
  cachedTokens?: number
  cachedInputTokens?: number
}

export interface RunFinishedEvent {
  type: "RUN_FINISHED"
  threadId?: string
  runId?: string
  outcome?: {
    type?: string
    interrupts?: RawInterruptEvent[]
    result?: { usage?: UsageEvent }
  }
}

export interface RunErrorEvent {
  type: "RUN_ERROR"
  code?: string
  message?: string
}

/** 后端唯一的 CUSTOM 事件（agtransport 里模型重试那条）。 */
export interface CustomEvent {
  type: "CUSTOM"
  name: string
  value?: unknown
}

/** reducer 处理的全部事件类型。 */
export type AgEvent =
  | RunStartedEvent
  | TextMessageStartEvent
  | TextMessageContentEvent
  | TextMessageEndEvent
  | ToolCallStartEvent
  | ToolCallArgsEvent
  | ToolCallEndEvent
  | ToolCallResultEvent
  | ReasoningStartEvent
  | ReasoningMessageContentEvent
  | ReasoningEndEvent
  | RunFinishedEvent
  | RunErrorEvent
  | CustomEvent

// ── 不可变小工具 ──────────────────────────────────────────────────────────
//
// 每个事件只克隆它真正碰到的那几层：消息数组 + 目标消息 + 目标 part 数组。
// 流式下每个 delta 都会走一遍，所以这里不做全量深拷贝 —— 但也**不引入
// 结构共享优化**：先把语义写对，profile 之后再回来调。

function replaceAt<T>(arr: T[], index: number, value: T): T[] {
  const next = arr.slice()
  next[index] = value
  return next
}

function updateMessage(
  state: ConversationState,
  messageId: string,
  fn: (m: Message) => Message,
): ConversationState {
  const idx = state.messages.findIndex((m) => m.id === messageId)
  if (idx < 0) return state
  return { ...state, messages: replaceAt(state.messages, idx, fn(state.messages[idx]!)) }
}

/** 找到（或就地建出）一条消息。 */
function ensureMessage(
  state: ConversationState,
  messageId: string,
  role: Role,
): ConversationState {
  if (state.messages.some((m) => m.id === messageId)) return state
  return {
    ...state,
    messages: [...state.messages, { id: messageId, role, parts: [] }],
  }
}

/**
 * 找到该消息里最后一个未完结的指定 kind 的 part；找不到就补一个。
 *
 * ★ 「未完结」是这里唯一的状态推导：AG-UI 用 START/CONTENT/END 三段表达
 *   一段内容，所以「正在接收的那段」= 该 kind 里最后一个 complete=false 的。
 *   不额外维护 openPartId 指针 —— 那样状态里就多了一份必须与 messages
 *   保持同步的真相，而同步迟早会漏。
 */
function openPart(
  state: ConversationState,
  messageId: string,
  kind: "text" | "reasoning",
  timestamp?: number,
): { state: ConversationState; index: number } {
  // ★ 消息不存在就建一条。流可能从中间接上（切换会话时先渲染历史、
  //   随后才收到增量），也可能 CONTENT 先于 START 到 —— 原样返回 state
  //   等于把那一段静默丢掉，界面上就是"少了一段"。
  const base =
    state.messages.some((m) => m.id === messageId)
      ? state
      : ensureMessage(state, messageId, "assistant")

  const midx = base.messages.findIndex((m) => m.id === messageId)
  if (midx < 0) return { state: base, index: -1 }

  const parts = base.messages[midx]!.parts
  for (let i = parts.length - 1; i >= 0; i--) {
    const p = parts[i]!
    if (p.kind === kind && p.complete === false) return { state: base, index: i }
  }

  // 没有开着的那段 → 新开一段。
  //
  // ★ reasoning 记下**开始时刻**（事件的服务端时间戳）——
  //   「想了多久」是从这里算到收尾那一刻。
  const fresh: Part =
    kind === "text"
      ? { kind: "text", id: `${messageId}:t${parts.length}`, text: "", complete: false }
      : {
          kind: "reasoning",
          id: `${messageId}:r${parts.length}`,
          text: "",
          complete: false,
          startedAt: timestamp,
        }

  const withPart = updateMessage(base, messageId, (m) => ({ ...m, parts: [...m.parts, fresh] }))
  return { state: withPart, index: withPart.messages[midx]!.parts.length - 1 }
}

function closePart(
  state: ConversationState,
  messageId: string,
  kind: "text" | "reasoning",
  opts?: { interrupted?: boolean; timestamp?: number },
): ConversationState {
  const midx = state.messages.findIndex((m) => m.id === messageId)
  if (midx < 0) return state
  const parts = state.messages[midx]!.parts
  for (let i = parts.length - 1; i >= 0; i--) {
    const p = parts[i]!
    if (p.kind === kind && p.complete === false) {
      // ★ 「不再接收内容」不等于「想完了」：被停止/报错掐断的思维链要标出来，
      //   否则界面会说它「Thought for 2.1s」—— 它其实没想完。
      // ★ 收尾那一刻把用时**算出来**（服务端事件的时间戳之差）——
      //   结束之后它就是个定值，不会再涨。
      const next: Part =
        p.kind === "reasoning"
          ? {
              ...p,
              complete: true,
              ...(opts?.interrupted ? { interrupted: true } : {}),
              ...(opts?.timestamp !== undefined && p.startedAt !== undefined
                ? { elapsedMs: opts.timestamp - p.startedAt }
                : {}),
            }
          : { ...p, complete: true }
      return updateMessage(state, messageId, (m) => ({
        ...m,
        parts: replaceAt(m.parts, i, next),
      }))
    }
  }
  return state
}

function appendToOpenPart(
  state: ConversationState,
  messageId: string,
  kind: "text" | "reasoning",
  delta: string,
  timestamp?: number,
): ConversationState {
  const { state: withPart, index } = openPart(state, messageId, kind, timestamp)
  if (index < 0) return state
  return updateMessage(withPart, messageId, (m) => {
    const p = m.parts[index]!
    if (p.kind !== kind) return m
    return { ...m, parts: replaceAt(m.parts, index, { ...p, text: p.text + delta }) }
  })
}

// ── reducer ──────────────────────────────────────────────────────────────

/**
 * reduceEvent 把一个 AG-UI 事件推进到新的状态。
 *
 * 永远返回新对象，不改入参 —— 流式下每个 delta 都调一次，就地改会
 * 让 React 的比较逻辑彻底失效。
 */
export function reduceEvent(state: ConversationState, event: AgEvent): ConversationState {
  switch (event.type) {
    // ── 轮次生命周期 ──────────────────────────────────────────────────
    case "RUN_STARTED":
      // 新一轮开始：清掉上一轮的审批挂起与错误。
      //
      // ★ interrupts 必须在这里清。留着的话，答复过一次之后状态点会一直
      //   显示「待审批」—— 而实际上已经答复完了。
      return { ...state, status: "running", interrupts: [], error: undefined, retry: undefined }

    case "RUN_FINISHED":
      return onRunFinished(state, event)

    case "RUN_ERROR":
      return onRunError(state, event)

    // ── 正文 ──────────────────────────────────────────────────────────
    case "TEXT_MESSAGE_START": {
      const e = event as TextMessageStartEvent
      const role = (e.role ?? "assistant") as Role
      const next = ensureMessage(state, e.messageId, role)
      // ★ 归属跟着**消息**走 —— 多 agent 模式下要按 agent 过滤。
      //
      //   ensureMessage 返回的是**整个 state**，不是那条消息。所以这里必须
      //   用 updateMessage 回到消息上改 —— 直接展开它会把 agent 挂到 state
      //   上（挂上去也没人读），于是实时流里的归属**静默丢失**：
      //   界面不显示 agent 名，按 agent 过滤也筛不掉任何东西。
      //
      //   没有 name 的（老会话 / user 消息）就不标，不猜。
      const withAgent = e.name
        ? updateMessage(next, e.messageId, (m) => ({ ...m, agent: e.name }))
        : next
      return { ...withAgent, status: "running" }
    }

    case "TEXT_MESSAGE_CONTENT":
      return appendToOpenPart(state, event.messageId, "text", event.delta ?? "")

    case "TEXT_MESSAGE_END":
      return closePart(state, event.messageId, "text")

    // ── 推理 ──────────────────────────────────────────────────────────
    //
    // ★ REASONING_START / REASONING_MESSAGE_START 的 role 字面量是
    //   "reasoning"，不是合法的消息角色 —— 所以推理挂在**同 id 的
    //   assistant 消息**上，角色固定 assistant。
    case "REASONING_START":
    case "REASONING_MESSAGE_START":
      return { ...ensureMessage(state, event.messageId, "assistant"), status: "running" }

    case "REASONING_MESSAGE_CONTENT":
      return appendToOpenPart(
        state,
        event.messageId,
        "reasoning",
        event.delta ?? "",
        event.timestamp,
      )

    case "REASONING_MESSAGE_END":
      return closePart(state, event.messageId, "reasoning", { timestamp: event.timestamp })

    case "REASONING_END":
      // 兜底：某些实现只发 REASONING_END 不发 MESSAGE_END。
      return closePart(state, event.messageId, "reasoning", { timestamp: event.timestamp })

    // ── 工具调用 ──────────────────────────────────────────────────────
    case "TOOL_CALL_START":
      return onToolStart(state, event)

    case "TOOL_CALL_ARGS":
      return onToolArgs(state, event)

    case "TOOL_CALL_END":
      return onToolEnd(state, event)

    case "TOOL_CALL_RESULT":
      return onToolResult(state, event)

    // ── 杂项 ──────────────────────────────────────────────────────────
    case "CUSTOM": {
      // model_retry 是后端唯一的 CUSTOM 事件（agtransport 里
      // helpers.IsModelRetryInProgress 分支发的）。
      if (event.name === "model_retry") {
        const value = event.value as { message?: string } | undefined
        return { ...state, retry: { message: value?.message ?? "retrying" } }
      }
      return state
    }

    default:
      // THINKING_* 是 REASONING_* 的废弃别名（core 的 EventType 里标着
      // @deprecated）。不单独处理：真后端发的是 REASONING_*，而对着
      // 废弃别名再写一套分支只会多一条无人走过的路径。
      return state
  }
}

function onRunFinished(state: ConversationState, event: RunFinishedEvent): ConversationState {
  const outcome = event.outcome

  const rawInterrupts = outcome?.type === "interrupt" ? (outcome.interrupts ?? []) : []

  // ★ 数组为空就不进审批态。停在「待审批」等一个永远不会来的答复，
  //   比显示 idle 糟得多 —— 用户会一直以为有东西要批。
  if (rawInterrupts.length > 0) {
    const interrupts: Interrupt[] = rawInterrupts.map((i) => ({
      id: i.id,
      reason: i.reason,
      message: i.message,
      toolCallId: i.toolCallId,
      // 真后端把工具名与参数放在 AG-UI 的 metadata 里；假后端与旧事件可能
      // 直接平铺。两种都认，界面不必知道是实时还是回放。
      toolName: i.toolName ?? i.metadata?.toolName,
      arguments: i.arguments ?? i.metadata?.arguments,
      // 子 agent 的审批要能看出是谁要的；根 agent 不标（那正是「当前会话」
      // 自己，标了反而占地方）。
      requestedByAgent: i.subagentRunId,
    }))
    // ★ 关闭所有还开着的 part：轮次结束就没有"正在接收"了。漏掉的话
    //   界面会一直显示一个转圈没停的 part。
    const closed = closeAllOpenParts(state)
    return { ...closed, status: "awaiting-approval", interrupts, retry: undefined }
  }

  const closed = closeAllOpenParts(state)
  return {
    ...closed,
    status: "idle",
    interrupts: [],
    retry: undefined,
    usage: toUsage(outcome?.result?.usage) ?? closed.usage,
  }
}

function onRunError(state: ConversationState, event: RunErrorEvent): ConversationState {
  return {
    // ★ 报错也是一种「掐断」：思维链没想完，是被错误打断的。
    ...closeAllOpenParts(state, { interrupted: true }),
    status: "error",
    // ★ 中断挂起同时清掉：模型报错后不存在待审批这回事，
    //   留着会让状态点一直红着，而审批卡点了也没用。
    interrupts: [],
    retry: undefined,
    error: { code: (event as { code?: string }).code ?? "unknown", message: event.message ?? "" },
  }
}

function onToolStart(state: ConversationState, event: ToolCallStartEvent): ConversationState {
  const parent = (event as { parentMessageId?: string }).parentMessageId || `tool:${event.toolCallId}`
  const withMsg = ensureMessage(state, parent, "assistant")
  const part: ToolPart = {
    kind: "tool",
    id: event.toolCallId,
    name: event.toolCallName ?? "",
    argsText: "",
    phase: "streaming",
  }
  return {
    ...updateMessage(withMsg, parent, (m) => ({ ...m, parts: [...m.parts, part] })),
    status: "running",
  }
}

function onToolArgs(state: ConversationState, event: ToolCallArgsEvent): ConversationState {
  return updateToolPart(state, event.toolCallId, (p) => ({
    ...p,
    argsText: p.argsText + deltaOf(event),
  }))
}

function onToolEnd(state: ConversationState, event: ToolCallEndEvent): ConversationState {
  // args 收完 → 等待结果。phase 保持 running，界面上显示"执行中"。
  return updateToolPart(state, event.toolCallId, (p) =>
    p.phase === "streaming" ? { ...p, phase: "running" } : p,
  )
}

function onToolResult(state: ConversationState, event: ToolCallResultEvent): ConversationState {
  return updateToolPart(state, event.toolCallId, (p) => ({
    ...p,
    result: event.content ?? "",
    phase: "complete",
  }))
}

/**
 * updateToolPart 按 toolCallId 找到 part 并改。
 *
 * 跨消息搜索而不是只找最后一条：TOOL_CALL_RESULT 只带 toolCallId，
 * 而工具调用可能挂在别的 parentMessageId 上。
 */
function updateToolPart(
  state: ConversationState,
  toolCallId: string,
  fn: (p: ToolPart) => ToolPart,
): ConversationState {
  const midx = state.messages.findIndex((m) => m.parts.some((p) => p.kind === "tool" && p.id === toolCallId))
  if (midx < 0) {
    // 没见过 START 却收到 ARGS/RESULT：就地补一个再走 fn —— 只建不填的话
    // 结果照样丢，界面上工具卡永远是空的。
    const parent = `tool:${toolCallId}`
    const withMsg = ensureMessage(state, parent, "assistant")
    const part: ToolPart = { kind: "tool", id: toolCallId, name: "", argsText: "", phase: "running" }
    const withPart = updateMessage(withMsg, parent, (m) => ({ ...m, parts: [...m.parts, fn(part)] }))
    return { ...withPart, status: "running" }
  }
  const parts = state.messages[midx]!.parts
  const pidx = parts.findIndex((p) => p.kind === "tool" && p.id === toolCallId)
  const target = parts[pidx]!
  if (target.kind !== "tool") return state
  return updateMessage(state, state.messages[midx]!.id, (m) => ({
    ...m,
    parts: replaceAt(m.parts, pidx, fn(target)),
  }))
}

/**
 * closeAllOpenParts 把所有还开着的 text / reasoning part 收尾。
 *
 * ★ 导出来给 store 的 `stop()` 用：用户点「停止」时轮次已经结束，
 *   不会再有关闭事件到达 —— 不主动收尾的话，最后那条消息的转圈
 *   永远停不下来。
 */
export function closeAllOpenParts(
  state: ConversationState,
  opts?: { interrupted?: boolean; timestamp?: number },
): ConversationState {
  let out = state
  for (const m of state.messages) {
    for (const kind of ["text", "reasoning"] as const) {
      const has = m.parts.some((p) => p.kind === kind && p.complete === false)
      if (has) out = closePart(out, m.id, kind, opts)
    }
  }
  return out
}

/**
 * toUsage 把两种 token 字段名归一到一种。
 *
 * ★ AG-UI 的 TokenUsage 用 inputTokens/outputTokens，openheron 的
 *   usageView（api/v2_types.go）用 promptTokens/completionTokens。
 *   两边都在，取决于 usage 是从 RUN_FINISHED 的 result 还是从
 *   /v2/sessions/:id/messages 来的。两种都认，省得以后再为字段名对不上
 *   调一轮。
 */
function toUsage(raw: unknown): Usage | undefined {
  if (!raw || typeof raw !== "object") return undefined
  const u = raw as Record<string, number | undefined>
  const total = u.totalTokens
  const out = {
    promptTokens: u.promptTokens ?? u.inputTokens ?? 0,
    completionTokens: u.completionTokens ?? u.outputTokens ?? 0,
    totalTokens: total ?? (u.promptTokens ?? u.inputTokens ?? 0) + (u.completionTokens ?? u.outputTokens ?? 0),
    reasoningTokens: u.reasoningTokens,
    cachedTokens: u.cachedTokens ?? u.cachedInputTokens,
  }
  return out.totalTokens || out.promptTokens || out.completionTokens ? out : undefined
}

/** AG-UI 的增量字段叫 delta；个别事件用 value。 */
function deltaOf(event: unknown): string {
  const e = event as { delta?: unknown; content?: unknown }
  if (typeof e.delta === "string") return e.delta
  if (typeof e.content === "string") return e.content
  return ""
}

/**
 * reduceEvents 把一整串事件推进到底，返回终态。
 *
 * 存在的理由是测试：断言终态比断言中间态可读，而且它天然覆盖
 * 「第一步的结果会不会被第二步改掉」这类问题。
 */
export function reduceEvents(
  initial: ConversationState,
  events: readonly AgEvent[],
): ConversationState {
  let out = initial
  for (const e of events) out = reduceEvent(out, e)
  return out
}
