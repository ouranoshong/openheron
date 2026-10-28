/**
 * 历史水合：把服务端历史铺成 reducer 的形状。
 *
 * ★ 服务端是权威副本（api/v2_run.go: "The server owns history"），
 *   所以这里只读不写。
 */
import type { AgMessage, PendingApproval } from "@/lib/api"
import { emptyConversation, type ConversationState, type Message, type Usage } from "@/lib/types"

/**
 * findToolOwner 找出挂着这次工具调用的那条消息。
 *
 * ★ 跨消息搜索：并行时工具结果与它的调用**可能隔着别的 agent 的消息**。
 *   只看最后一条会挂错人。
 */
function findToolOwner(messages: Message[], toolCallId: string): Message | undefined {
  if (!toolCallId) return undefined
  return messages.find((m) => m.parts.some((p) => p.kind === "tool" && p.id === toolCallId))
}

/**
 * seedFromHistory 把服务端历史铺成 reducer 的形状。
 *
 * ★ 服务端是权威副本（api/v2_run.go: "The server owns history"），
 *   所以这里只读不写。工具结果从 toolCalls / toolCallResults 拼回 ——
 *   落盘格式是 OpenAI 风格的 snake_case（session.jsonl），不是 AG-UI 的
 *   camelCase，所以要在这里转。
 */
export function seedFromHistory(
  raw: AgMessage[],
  pending?: PendingApproval[],
  usage?: Usage,
): ConversationState {
  const messages: Message[] = []
  /**
   * ★ 服务端给的消息 id 未必唯一 —— 一轮里连续两次工具调用会产出两条
   *   空内容、无 ToolCallID 的 assistant 消息，按内容取摘要时 id 相同。
   *   （后端已改成把位置算进 id，见 api/v2_convert.go 的 stableID；
   *   这里仍然兜一层：React 的 key 必须唯一，不能指望上游永远正确。）
   */
  const seen = new Map<string, number>()
  const uniq = (id: string): string => {
    const n = seen.get(id) ?? 0
    seen.set(id, n + 1)
    return n === 0 ? id : `${id}#${n}`
  }

  for (const m of raw) {
    if (m.role === "tool") {
      // 工具结果并进它对应的那次调用，不单独成条 —— 与流式时一致。
      //
      // ★ 按 toolCallId **跨消息**找，不能用「最后一条」。
      //
      //   并行时事件是交错的：A 的工具结果可能排在 B 的 assistant 消息
      //   之后。原来用 messages.at(-1) 会把 A 的结果并进 B 的消息 ——
      //   界面上 B 的工具卡显示 A 的输出，而按 agent 过滤时更糟：
      //   A 的活儿挂在了 B 名下。
      //
      //   流式路径本来就是按 toolCallId 找的（events.ts 的 updateToolPart），
      //   这里只是把同一套匹配补齐到回放路径 —— 两路必须一致。
      const target = findToolOwner(messages, m.toolCallId ?? "") ?? messages.at(-1)
      if (target) {
        for (const p of target.parts) {
          if (p.kind === "tool" && p.id === m.toolCallId) {
            p.result = typeof m.content === "string" ? m.content : ""
            p.phase = "complete"
          }
        }
      }
      continue
    }

    const parts: Message["parts"] = []
    // ★ 思维链先于正文 —— 实时流里就是这个顺序（模型先想、再答），
    //   回放保持一致，展开的顺序才不跳。
    //
    //   正文与用时由后端放在 metadata 里（api/v2_convert.go）：
    //   事件流不落盘，不存下来就没有「刷新后想了多久」。
    const meta = m.metadata ?? {}
    const reasoning = typeof meta.reasoning === "string" ? meta.reasoning : ""
    if (reasoning) {
      const ms = typeof meta.reasoningMs === "number" ? meta.reasoningMs : undefined
      parts.push({
        kind: "reasoning",
        // ★ part id 与**实时流同一套方案**（events.ts 的 ensurePart）：
        //   `${messageId}:${r|t}${段序号}`。
        //
        //	两套方案的后果不是「看起来不一样」，而是**会撞 id**：
        //	一条消息里出现两个正文段（推理→正文→推理→正文）时，
        //	历史路径会给出两个 `m1:t` —— React 的 key 撞车，
        //	表现是「刷新后第二段正文不渲染」。
        id: `${m.id}:r${parts.length}`,
        text: reasoning,
        complete: true,
        // ★ 用时由**后端**算好（服务端知道模型真的在想的那段时间，
        //   客户端只知道事件到达的时间）。没有就是不知道，不是 0。
        elapsedMs: ms,
      })
    }
    if (typeof m.content === "string" && m.content) {
      parts.push({ kind: "text", id: `${m.id}:t${parts.length}`, text: m.content, complete: true })
    }
    for (const tc of m.toolCalls ?? []) {
      parts.push({
        kind: "tool",
        id: tc.id,
        name: tc.function.name,
        argsText: tc.function.arguments,
        phase: "complete",
        fromHistory: true,
      })
    }
    // ★ agent 归属从 AG-UI 的 message.name 来（后端 api/v2_convert.go 填的）。
    //   没有 name 的留空 —— 那是 user 消息或加标记之前的老会话，不猜。
    messages.push({ id: uniq(m.id || `msg-${messages.length}`), role: m.role, parts, agent: m.name })
  }

  const convo = emptyConversation()
  convo.messages = messages
  // ★ usage 带上 byAgent 分账 —— 「谁花的 token」是会话级的事实，
  //   历史回放时就要有，否则打开会话看不到分账。
  if (usage) convo.usage = usage
  if (pending && pending.length > 0) {
    convo.status = "awaiting-approval"
    // ★ 全部待批都要重建 —— 少一个就答不了它。
    convo.interrupts = pending.map((p) => ({
      id: p.interruptId,
      reason: p.reason,
      message: p.message,
      toolCallId: p.toolCallId,
      toolName: p.toolName,
      arguments: p.arguments,
      // 历史里两个字段都可能有：subagentRunId 是归属（agent 名），
      // requestedByAgent 是审批记录里的请求方。优先前者，与实时事件一致。
      requestedByAgent: p.subagentRunId ?? p.requestedByAgent,
    }))
  }
  return convo
}
