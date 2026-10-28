import type { Message } from "@/lib/types"

/**
 * 轮次标记。
 *
 * ★ 服务端历史没有 turn id，user 消息是唯一可靠的轮次边界。工具循环
 *   会产生多条连续的 assistant 消息（一次调用一条），如果把每条都当成
 *   独立消息展示，标签会重复、间距也分不出「同轮片段」和「新一轮」。
 *
 *   这里只派生展示标记，不改 reducer 的数据形状 —— 每条协议消息仍然
 *   对应一个 DOM 节点，现有测试与滚动锚点不受影响。
 */
export interface TurnFlags {
  /** 是否开启新一轮（user 消息或首条消息）。 */
  startsTurn: boolean
  /** 是否渲染可见的角色标签；同轮后续 assistant 片段不再重复。 */
  showRole: boolean
}

export function turnFlags(messages: Message[]): TurnFlags[] {
  const out: TurnFlags[] = []
  let prevRole: Message["role"] | null = null
  let prevAgent: string | undefined

  for (const message of messages) {
    const isUser = message.role === "user"
    // ★ 换了 agent 也要显示标签。
    //
    //   多 agent 会话里连续两条 assistant 可能分属**不同**的 agent ——
    //   而「换人了」正是用户最需要看到的信号。原先只看角色变化，
    //   于是 main 说完、coder 接上，两条都顶着同一个角色，标签只出一次，
    //   看起来像同一个人在说话。
    const agentChanged = message.agent !== prevAgent
    out.push({
      startsTurn: isUser || prevRole === null,
      // 第一个非 user 片段（含 user 之后、或历史以 assistant 开头）显示标签；
      // 连续 assistant 片段只有第一条显示 —— 除非换了 agent。
      showRole: !isUser && (prevRole !== "assistant" || agentChanged),
    })
    prevRole = message.role
    prevAgent = message.agent
  }

  return out
}
