import type { Message, Usage } from "@/lib/types"

/**
 * 多 agent 相关的纯函数。
 *
 * ★ 放在 lib 而不是组件里：过滤与归集都是**纯函数**，能用 bun test
 *   直接钉住，不必开浏览器。组件只负责画。
 */

/**
 * 会话里出现过的 agent。
 *
 * ★ 取**两个来源的并集**：usage.byAgent 的键（有 token 的）与消息上的
 *   归属标记（干过活的）。只取前者会漏掉「干了活但没花 token」的 agent
 *   （比如只调了个工具就返回的），只取后者会漏掉「花了很多 token 但消息
 *   没落到归档」的。
 *
 * ★ 排序用字典序 —— 顺序稳定，界面不会因为 map 的遍历顺序跳来跳去。
 */
export function collectAgents(messages: Message[], usage?: Usage): string[] {
  const seen = new Set<string>()
  for (const key of Object.keys(usage?.byAgent ?? {})) seen.add(key)
  for (const m of messages) if (m.agent) seen.add(m.agent)
  return [...seen].sort()
}

/**
 * 按 agent 过滤消息。
 *
 * ★ user 消息（没有归属标记）在过滤后**仍然显示** —— 那是用户自己说的话，
 *   隐藏它会让「我刚才问的那句」凭空消失，比多显示一条更糟。
 */
export function filterByAgent(messages: Message[], agent: string | null): Message[] {
  if (!agent) return messages
  return messages.filter((m) => !m.agent || m.agent === agent)
}

/**
 * token 数的紧凑写法。
 *
 * ★ 千以上才缩写 —— 999 显示成 "1.0k" 会让人以为它比 999 多。
 */
export function formatTokens(n: number): string {
  if (n < 1000) return String(n)
  if (n < 10000) return `${(n / 1000).toFixed(1)}k`
  return `${Math.round(n / 1000)}k`
}
