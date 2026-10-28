import type { Message, Usage } from "@/lib/types"

export function collectAgents(messages: Message[], usage?: Usage): string[] {
  const seen = new Set<string>()
  for (const key of Object.keys(usage?.byAgent ?? {})) seen.add(key)
  for (const m of messages) if (m.agent) seen.add(m.agent)
  return [...seen].sort()
}

export function filterByAgent(messages: Message[], agent: string | null): Message[] {
  if (!agent) return messages
  return messages.filter((m) => !m.agent || m.agent === agent)
}

export function formatTokens(n: number): string {
  if (n < 1000) return String(n)
  if (n < 10000) return `${(n / 1000).toFixed(1)}k`
  return `${Math.round(n / 1000)}k`
}
