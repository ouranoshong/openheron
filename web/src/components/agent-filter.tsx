import { useMemo } from "react"

import { collectAgents, formatTokens } from "@/lib/agents"
import { selectActiveUsage, selectMessages, useStore } from "@/lib/store"
import { cn } from "@/lib/utils"

export function AgentFilter() {
  const usage = useStore(selectActiveUsage)
  const messages = useStore(selectMessages)
  const filter = useStore((s) => s.agentFilter)
  const setFilter = useStore((s) => s.setAgentFilter)

  const agents = useMemo(() => collectAgents(messages, usage), [messages, usage])

  if (agents.length < 2) return null

  return (
    <div
      data-testid="agent-filter"
      className="border-border/60 flex shrink-0 items-center gap-1.5 overflow-x-auto border-b px-4 py-2"
    >
      <span className="text-muted-foreground shrink-0 text-xs">Agent</span>
      <FilterChip
        active={filter === null}
        label="All"
        testId="agent-filter-all"
        onClick={() => setFilter(null)}
      />
      {agents.map((agent) => (
        <FilterChip
          key={agent}
          active={filter === agent}
          label={agent}
          testId={`agent-filter-${agent}`}
          tokens={usage?.byAgent?.[agent]?.totalTokens}
          onClick={() => setFilter(filter === agent ? null : agent)}
        />
      ))}
    </div>
  )
}

function FilterChip({
  active,
  label,
  tokens,
  testId,
  onClick,
}: {
  active: boolean
  label: string
  tokens?: number
  testId: string
  onClick: () => void
}) {
  return (
    <button
      type="button"
      data-testid={testId}
      aria-pressed={active}
      onClick={onClick}
      title={tokens === undefined ? label : `${label} — ${tokens} tokens`}
      className={cn(
        "shrink-0 rounded-md border px-2 py-0.5 text-xs transition-colors",
        active
          ? "border-border bg-accent text-accent-foreground"
          : "border-border/60 text-muted-foreground hover:bg-accent/50 hover:text-accent-foreground",
      )}
    >
      {label}
      {tokens !== undefined && tokens > 0 && (
        <span className="text-muted-foreground ml-1 tabular-nums">{formatTokens(tokens)}</span>
      )}
    </button>
  )
}
