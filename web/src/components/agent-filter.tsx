import { useMemo } from "react"

import { collectAgents, formatTokens } from "@/lib/agents"
import { selectActiveContext, selectActiveUsage, selectMessages, useStore } from "@/lib/store"
import type { ContextUsage } from "@/lib/types"
import { cn } from "@/lib/utils"

export function AgentFilter({ className }: { className?: string }) {
  const usage = useStore(selectActiveUsage)
  const context = useStore(selectActiveContext)
  const messages = useStore(selectMessages)
  const filter = useStore((s) => s.agentFilter)
  const setFilter = useStore((s) => s.setAgentFilter)

  const agents = useMemo(() => collectAgents(messages, usage), [messages, usage])

  if (agents.length < 2) return null

  return (
    <div
      data-testid="agent-filter"
      // ★ It lives in the header now, so it has to be able to give way: the
      //   row shrinks and scrolls, the chips inside it do not.
      className={cn(
        "flex min-w-0 shrink items-center gap-1.5 overflow-x-auto",
        className,
      )}
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
          context={context?.[agent]}
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
  context,
  testId,
  onClick,
}: {
  active: boolean
  label: string
  tokens?: number
  context?: ContextUsage
  testId: string
  onClick: () => void
}) {
  // ★ The chip carries the **name** and nothing else. The numbers used to
  //   ride on it, and the header turned into a ledger: four agents meant
  //   four numbers per chip, and the eye could not tell which was which.
  //   The details are in the tooltip, which is where a number you only want
  //   occasionally belongs.
  //
  //   The two numbers are different quantities and must not be rendered as if
  //   they were the same one: the occupancy is how full the context is now,
  //   the spend is what the agent has consumed over the whole session.
  const title = context
    ? [
        // The first line keeps its old shape: the label and the spend.
        tokens === undefined ? label : `${label} — ${tokens} tokens`,
        context.contextWindow > 0
          ? `Context: ≈${formatTokens(context.usedTokens)} / ${formatTokens(context.contextWindow)}`
          : `Context: ≈${formatTokens(context.usedTokens)} tokens`,
        // Where the context gets cut, when it does.
        ...(context.summarizeAt && context.summarizeAt > 0
          ? [`Compacted at: ${context.summarizeAt} tokens`]
          : []),
      ].join("\n")
    : tokens === undefined
      ? label
      : `${label} — ${tokens} tokens`

  return (
    <button
      type="button"
      data-testid={testId}
      aria-pressed={active}
      onClick={onClick}
      title={title}
      className={cn(
        "shrink-0 rounded-md border px-2 py-0.5 text-xs transition-colors",
        active
          ? "border-border bg-accent text-accent-foreground"
          : "border-border/60 text-muted-foreground hover:bg-accent/50 hover:text-accent-foreground",
      )}
    >
      {label}
    </button>
  )
}
