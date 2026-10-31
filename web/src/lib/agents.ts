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

// formatTokens abbreviates a token count.
//
// ★ A million reads as "1.3M" and not "1311k": the window sizes people
//
//	compare at a glance are in the millions, and "1311k" takes longer to
//	read than "1.3M".
export function formatTokens(n: number): string {
  if (n < 1000) return String(n)
  if (n < 10000) return `${(n / 1000).toFixed(1)}k`
  if (n < 1000000) return `${Math.round(n / 1000)}k`
  return `${(n / 1000000).toFixed(1)}M`
}

// formatPercent renders a ratio as a percentage.
//
// ★ It never rounds a non-zero ratio down to "0 %". A context holding 0.3 %
//
//	of its window is not empty, and "0 %" reads like "nothing is in there"
//	— the reader stops watching exactly when the number starts to matter.
//
// ★ The precision follows the size: below 10 % a decimal, above it a whole
//
//	number. A percentage with three decimals is noise.
export function formatPercent(ratio: number): string {
  const pct = ratio * 100
  if (pct <= 0) return "0 %"
  if (pct < 0.1) return "<0.1 %"
  if (pct < 10) return `${Math.round(pct * 10) / 10} %`
  return `${Math.round(pct)} %`
}
